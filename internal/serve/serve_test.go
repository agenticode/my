package serve

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agenticode/my/internal/exporter"
	"github.com/agenticode/my/internal/testutil"
)

// state is a State that counts how often it was closed.
type state struct {
	ce     float64
	closed atomic.Int32
}

func (s *state) Metrics() *exporter.Set {
	set := &exporter.Set{}
	set.Add(exporter.DIMMVerdict, 1, "h1", "CPU0_C1", "S1")
	set.Add(exporter.DIMMCorrected, s.ce, "h1", "CPU0_C1", "S1")
	return set
}
func (s *state) Close() { s.closed.Add(1) }

// source hands out what the test queued: states and errors, in order.
type source struct {
	mu    sync.Mutex
	queue []any
	scans int
	// block makes a scan wait for the end of its context.
	block bool
}

func (s *source) Scan(ctx context.Context) (State, error) {
	s.mu.Lock()
	s.scans++
	var next any
	if len(s.queue) > 0 {
		next, s.queue = s.queue[0], s.queue[1:]
	}
	block := s.block
	s.mu.Unlock()
	if block {
		<-ctx.Done()
		// A scan that returns its state after the server stopped.
		return &late, nil
	}
	switch v := next.(type) {
	case *state:
		return v, nil
	case error:
		return nil, v
	case string:
		panic(v)
	}
	return nil, nil
}

var late state

func (s *source) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scans
}

var ctx = context.Background()

// get asks the handler and returns status, header and body.
func get(t *testing.T, h http.Handler, method, path string, header ...string) (int, http.Header, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Header(), rec.Body.String()
}

// clock is a clock a test moves.
type clock struct{ t time.Time }

func (c *clock) now() time.Time {
	c.t = c.t.Add(250 * time.Millisecond)
	return c.t
}

func newServer(src Source, logs *[]string) (*Server, *clock) {
	c := &clock{t: time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)}
	opts := Options{Metrics: true, Now: c.now}
	if logs != nil {
		opts.OnScan = func(st State, took time.Duration, err error) {
			if err != nil {
				*logs = append(*logs, fmt.Sprintf("scan failed after %s: %v", took, err))
				return
			}
			*logs = append(*logs, fmt.Sprintf("scan finished in %s: %v corrected errors", took, st.(*state).ce))
		}
	}
	return New(src, opts), c
}

func TestBeforeTheFirstScan(t *testing.T) {
	s, _ := newServer(&source{}, nil)
	h := s.Handler()
	if code, _, body := get(t, h, "GET", "/metrics"); code != http.StatusServiceUnavailable || !strings.Contains(body, "first scan has not finished") {
		t.Errorf("/metrics = %d %q", code, body)
	}
	if code, _, _ := get(t, h, "GET", "/-/ready"); code != http.StatusServiceUnavailable {
		t.Errorf("/-/ready = %d", code)
	}
	if code, _, body := get(t, h, "GET", "/-/healthy"); code != http.StatusOK || body != "ok\n" {
		t.Errorf("/-/healthy = %d %q", code, body)
	}
}

func TestMetricsAfterAScan(t *testing.T) {
	st := &state{ce: 12}
	var logs []string
	s, _ := newServer(&source{queue: []any{st}}, &logs)
	s.ScanOnce(ctx)
	h := s.Handler()

	code, hd, body := get(t, h, "GET", "/metrics")
	if code != http.StatusOK || hd.Get("Content-Type") != "text/plain; version=0.0.4; charset=utf-8" || hd.Get("Content-Encoding") != "" {
		t.Fatalf("/metrics = %d, header %v", code, hd)
	}
	if hd.Get("Content-Length") != fmt.Sprint(len(body)) {
		t.Errorf("Content-Length %s for %d bytes", hd.Get("Content-Length"), len(body))
	}
	x := testutil.MustParseExposition(t, body)
	for series, want := range map[string]float64{
		`my_dimm_verdict{host="h1",slot="CPU0_C1",serial="S1"}`:  1,
		`my_dimm_ce_total{host="h1",slot="CPU0_C1",serial="S1"}`: 12,
		`my_exporter_scan_failures_total`:                        0,
		// The clock of the test moves a quarter of a second per reading.
		`my_exporter_last_scan_duration_seconds`:  0.25,
		`my_exporter_last_scan_timestamp_seconds`: 1794562200.5,
	} {
		if got, ok := x.Series[series]; !ok || got != want {
			t.Errorf("%s = %v (present %v), want %v", series, got, ok, want)
		}
	}
	info := 0
	for series, v := range x.Series {
		if strings.HasPrefix(series, "my_exporter_build_info{") {
			info++
			if v != 1 || !strings.Contains(series, `engine="`) || !strings.Contains(series, `version="`) {
				t.Errorf("build info %s = %v", series, v)
			}
		}
	}
	if info != 1 {
		t.Errorf("%d build info series", info)
	}

	// A client that takes gzip gets the same body compressed.
	code, hd, zipped := get(t, h, "GET", "/metrics", "Accept-Encoding", "deflate, gzip;q=0.8")
	if code != http.StatusOK || hd.Get("Content-Encoding") != "gzip" || hd.Get("Vary") != "Accept-Encoding" {
		t.Fatalf("gzip: %d, header %v", code, hd)
	}
	zr, err := gzip.NewReader(strings.NewReader(zipped))
	if err != nil {
		t.Fatal(err)
	}
	plainBody, _ := io.ReadAll(zr)
	if string(plainBody) != body {
		t.Error("the gzip body differs from the plain body")
	}
	if _, hd, _ := get(t, h, "GET", "/metrics", "Accept-Encoding", "gzip;q=0, identity"); hd.Get("Content-Encoding") != "" {
		t.Error("gzip sent to a client that refuses it")
	}
	// HEAD: the headers of GET and no body.
	if code, hd, b := get(t, h, "HEAD", "/metrics"); code != http.StatusOK || b != "" || hd.Get("Content-Length") != fmt.Sprint(len(body)) {
		t.Errorf("HEAD /metrics = %d, %d bytes, Content-Length %s", code, len(b), hd.Get("Content-Length"))
	}
	if code, _, _ := get(t, h, "GET", "/-/ready"); code != http.StatusOK {
		t.Errorf("/-/ready = %d after a scan", code)
	}
	if len(logs) != 1 || logs[0] != "scan finished in 250ms: 12 corrected errors" {
		t.Errorf("log = %q", logs)
	}
}

func TestAFailedScanKeepsTheState(t *testing.T) {
	first, second := &state{ce: 12}, &state{ce: 15}
	var logs []string
	s, _ := newServer(&source{queue: []any{first, errors.New("ledger: disk full"), "index out of range", nil, second}}, &logs)
	h := s.Handler()
	series := func() map[string]float64 {
		t.Helper()
		code, _, body := get(t, h, "GET", "/metrics")
		if code != http.StatusOK {
			t.Fatalf("/metrics = %d %s", code, body)
		}
		return testutil.MustParseExposition(t, body).Series
	}
	const ce = `my_dimm_ce_total{host="h1",slot="CPU0_C1",serial="S1"}`

	s.ScanOnce(ctx)
	stamp := series()["my_exporter_last_scan_timestamp_seconds"]

	// An error, a fault inside the scan, a scan that returns nothing: each
	// is a failed scan, and the metrics stay those of the first.
	for i := 1; i <= 3; i++ {
		s.ScanOnce(ctx)
		got := series()
		if got[ce] != 12 || got["my_exporter_scan_failures_total"] != float64(i) || got["my_exporter_last_scan_timestamp_seconds"] != stamp {
			t.Errorf("after %d failed scans: %v", i, got)
		}
		if first.closed.Load() != 0 {
			t.Fatal("the state in use was closed by a failed scan")
		}
	}
	if len(logs) != 4 || logs[1] != "scan failed after 250ms: ledger: disk full" || !strings.Contains(logs[2], "stopped with a fault: index out of range") ||
		!strings.Contains(logs[3], "returned nothing") {
		t.Errorf("log = %q", logs)
	}

	// The next scan that works replaces the state and closes the old one.
	s.ScanOnce(ctx)
	got := series()
	if got[ce] != 15 || got["my_exporter_scan_failures_total"] != 3 || got["my_exporter_last_scan_timestamp_seconds"] <= stamp {
		t.Errorf("after the scan that worked: %v", got)
	}
	if first.closed.Load() != 1 || second.closed.Load() != 0 {
		t.Errorf("closed: first %d, second %d", first.closed.Load(), second.closed.Load())
	}
}

func TestTheFirstScanFails(t *testing.T) {
	s, _ := newServer(&source{queue: []any{errors.New("rasdaemon store: connection refused\nmore"), &state{ce: 1}}}, nil)
	h := s.Handler()
	s.ScanOnce(ctx)
	code, _, body := get(t, h, "GET", "/metrics")
	if code != http.StatusServiceUnavailable || body != "no scan has succeeded yet; the last one failed: rasdaemon store: connection refused\n" {
		t.Errorf("/metrics = %d %q", code, body)
	}
	if code, _, body := get(t, h, "GET", "/-/ready"); code != http.StatusServiceUnavailable || !strings.Contains(body, "connection refused") {
		t.Errorf("/-/ready = %d %q", code, body)
	}
	s.ScanOnce(ctx)
	code, _, body = get(t, h, "GET", "/metrics")
	if code != http.StatusOK {
		t.Fatalf("/metrics = %d after a scan that worked", code)
	}
	// The scan that failed before is still counted.
	if x := testutil.MustParseExposition(t, body); x.Series["my_exporter_scan_failures_total"] != 1 {
		t.Errorf("failures = %v", x.Series["my_exporter_scan_failures_total"])
	}
}

func TestOnlyReads(t *testing.T) {
	s, _ := newServer(&source{queue: []any{&state{}}}, nil)
	s.ScanOnce(ctx)
	h := s.Handler()
	for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		for _, path := range []string{"/metrics", "/", "/-/healthy"} {
			if code, hd, _ := get(t, h, method, path); code != http.StatusMethodNotAllowed || hd.Get("Allow") != "GET, HEAD" {
				t.Errorf("%s %s = %d, Allow %q", method, path, code, hd.Get("Allow"))
			}
		}
	}
}

func TestIndexAndUnknownPaths(t *testing.T) {
	s, _ := newServer(&source{}, nil)
	h := s.Handler()
	code, hd, body := get(t, h, "GET", "/")
	if code != http.StatusOK || !strings.HasPrefix(body, "my ") || !strings.Contains(body, "/metrics ") || !strings.Contains(body, "/-/ready ") ||
		hd.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("/ = %d %q %v", code, body, hd)
	}
	for _, path := range []string{"/metricsx", "/metrics/", "/debug/pprof/", "/-/quit", "/../etc/passwd"} {
		// A path that is not clean is sent to its clean form, which is not
		// found either.
		if code, _, _ := get(t, h, "GET", path); code != http.StatusNotFound && code/100 != 3 {
			t.Errorf("%s = %d", path, code)
		}
	}

	// Without --metrics there is no /metrics, and the index does not name it.
	off := New(&source{}, Options{})
	h = off.Handler()
	if code, _, _ := get(t, h, "GET", "/metrics"); code != http.StatusNotFound {
		t.Errorf("/metrics = %d with metrics off", code)
	}
	if _, _, body := get(t, h, "GET", "/"); strings.Contains(body, "/metrics") {
		t.Errorf("index names /metrics with metrics off:\n%s", body)
	}
	if off.opts.Listen != DefaultListen || off.opts.Interval != DefaultInterval {
		t.Errorf("defaults: %+v", off.opts)
	}
}

// fetch asks a running server.
func fetch(url string) (int, string, error) {
	c := &http.Client{Timeout: 5 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), err
}

func TestRun(t *testing.T) {
	a, b, c := &state{ce: 1}, &state{ce: 2}, &state{ce: 3}
	src := &source{queue: []any{a, b, c}}
	addr := make(chan net.Addr, 1)
	s := New(src, Options{Listen: "127.0.0.1:0", Metrics: true, Interval: 20 * time.Millisecond, OnListen: func(a net.Addr) { addr <- a }})
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- s.Run(runCtx) }()

	var base string
	select {
	case a := <-addr:
		base = "http://" + a.String()
	case err := <-done:
		t.Fatalf("Run returned before it listened: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the server did not listen")
	}

	// The server scans on its own: the third state shows up without a call.
	const want = `my_dimm_ce_total{host="h1",slot="CPU0_C1",serial="S1"} 3`
	deadline := time.Now().Add(5 * time.Second)
	for {
		code, body, err := fetch(base + "/metrics")
		if err == nil && code == http.StatusOK && strings.Contains(body, want) {
			testutil.MustParseExposition(t, body)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no third scan in the metrics: %d %v\n%s", code, err, body)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Scans that return nothing after the queue ran out fail; the state stays.
	if code, body, err := fetch(base + "/-/ready"); err != nil || code != http.StatusOK || body != "ok\n" {
		t.Errorf("/-/ready = %d %q %v", code, body, err)
	}

	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v after a clean stop", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after its context ended")
	}
	if a.closed.Load() != 1 || b.closed.Load() != 1 || c.closed.Load() != 1 {
		t.Errorf("closed: %d %d %d, want every state closed once", a.closed.Load(), b.closed.Load(), c.closed.Load())
	}
	if _, _, err := fetch(base + "/metrics"); err == nil {
		t.Error("the server still answers after Run returned")
	}
}

func TestRunCannotListen(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	src := &source{}
	s := New(src, Options{Listen: ln.Addr().String()})
	if err := s.Run(ctx); err == nil || !strings.Contains(err.Error(), "address already in use") {
		t.Errorf("Run on a port in use = %v", err)
	}
	if src.count() != 0 {
		t.Error("a scan ran although the server could not listen")
	}
	if err := New(src, Options{Listen: "not an address"}).Run(ctx); err == nil {
		t.Error("Run with a malformed address returned nil")
	}
}

// TestRunStopsDuringAScan stops the server while a scan is under way: the
// scan is told to stop, and what it returns late is closed.
func TestRunStopsDuringAScan(t *testing.T) {
	late.closed.Store(0)
	src := &source{block: true}
	s := New(src, Options{Listen: "127.0.0.1:0"})
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- s.Run(runCtx) }()
	deadline := time.Now().Add(5 * time.Second)
	for src.count() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no scan started")
		}
		time.Sleep(5 * time.Millisecond)
	}
	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return while a scan was under way")
	}
	if late.closed.Load() != 1 {
		t.Errorf("the state of the scan that ended late was closed %d times", late.closed.Load())
	}
}
