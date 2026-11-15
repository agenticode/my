// Package serve is the HTTP server behind "my serve". It reads its data
// through a Source, again and again with a pause in between, and answers
// from the latest state that was read:
//
//	/metrics     the metrics of the state in the Prometheus text format
//	/-/healthy   200 while the process runs
//	/-/ready     200 once the first scan has finished
//
// The server only reads. A scan that fails leaves the last state in place
// and is counted in my_exporter_scan_failures_total, so that metrics which
// have gone stale can be told from metrics that are current.
package serve

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/agenticode/my/internal/buildinfo"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/exporter"
)

// State is what one scan found. The server keeps the latest one and closes
// the one before it.
type State interface {
	// Metrics returns the metrics of the state. The server reads the set
	// once, when it takes the state over.
	Metrics() *exporter.Set
	// Close releases what the state holds: its ledger, its files.
	Close()
}

// Source reads the data the server shows.
type Source interface {
	// Scan reads the data again and returns the new state. The context is
	// cancelled when the server stops.
	Scan(ctx context.Context) (State, error)
}

// Options configure a Server.
type Options struct {
	// Listen is the address to listen on: "127.0.0.1:10163", ":10163".
	Listen string
	// Metrics turns on /metrics.
	Metrics bool
	// Interval is the pause between the end of one scan and the start of
	// the next.
	Interval time.Duration
	// OnScan is called after every scan: with the state it gave and the
	// time it took, or with the reason it failed. The caller logs it.
	OnScan func(st State, took time.Duration, err error)
	// OnListen is called with the address the server listens on, before
	// the first request is answered.
	OnListen func(addr net.Addr)
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// DefaultListen is where the server listens unless told otherwise: the
// loopback interface, so that nothing outside the host can read it until
// the operator says so.
const DefaultListen = "127.0.0.1:10163"

// DefaultInterval is the pause between scans.
const DefaultInterval = 5 * time.Minute

// Server serves the latest state of a Source.
type Server struct {
	src  Source
	opts Options

	// mu guards state. A request holds it for reading while it uses the
	// state, so that a state is closed only when no request reads it.
	mu    sync.RWMutex
	state State

	// page is the body of /metrics, rendered when a scan ends.
	page atomic.Pointer[page]

	// What the scans did. Only the scan loop sets these.
	failures int
	lastOK   time.Time
	lastDur  time.Duration
	lastErr  string
}

// page is one rendering of /metrics.
type page struct {
	// ready is false until a scan has succeeded; reason says why.
	ready  bool
	reason string
	// found is the part about the hosts: the metrics of the state.
	found []byte
	// plain is the whole body, found and the metrics about the exporter,
	// and gz the same compressed.
	plain []byte
	gz    []byte
}

// New returns a server for the source.
func New(src Source, opts Options) *Server {
	if opts.Listen == "" {
		opts.Listen = DefaultListen
	}
	if opts.Interval <= 0 {
		opts.Interval = DefaultInterval
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	s := &Server{src: src, opts: opts}
	s.page.Store(&page{reason: "the first scan has not finished"})
	return s
}

// Run listens, scans and serves until the context is cancelled. It returns
// an error when it cannot listen, and nil after a clean stop.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.opts.Listen)
	if err != nil {
		return err
	}
	if s.opts.OnListen != nil {
		s.opts.OnListen(ln.Addr())
	}
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
	}

	scanCtx, stopScans := context.WithCancel(ctx)
	scans := make(chan struct{})
	go func() {
		defer close(scans)
		s.loop(scanCtx)
	}()
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()

	select {
	case <-ctx.Done():
	case err = <-served:
		// The listener failed under the server. Nothing serves any more.
	}
	stopScans()
	grace, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if serr := srv.Shutdown(grace); serr != nil {
		srv.Close()
	}
	cancel()
	<-scans
	s.mu.Lock()
	if s.state != nil {
		s.state.Close()
		s.state = nil
	}
	s.mu.Unlock()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// loop scans until the context is cancelled.
func (s *Server) loop(ctx context.Context) {
	for {
		s.ScanOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.opts.Interval):
		}
	}
}

// ScanOnce runs one scan and takes its state over. Run calls it on its
// schedule; it is exported for tests and must not run twice at a time.
func (s *Server) ScanOnce(ctx context.Context) {
	start := s.opts.Now()
	st, err := s.scan(ctx)
	if ctx.Err() != nil {
		// The server is stopping: what the scan read is of no use.
		if st != nil {
			st.Close()
		}
		return
	}
	end := s.opts.Now()
	if err != nil {
		s.failures++
		s.lastErr = err.Error()
		s.render(nil)
		if s.opts.OnScan != nil {
			s.opts.OnScan(nil, end.Sub(start), err)
		}
		return
	}
	set := st.Metrics()

	s.mu.Lock()
	old := s.state
	s.state = st
	s.mu.Unlock()
	if old != nil {
		old.Close()
	}
	s.lastOK, s.lastDur, s.lastErr = end, end.Sub(start), ""
	s.render(set)
	if s.opts.OnScan != nil {
		s.opts.OnScan(st, s.lastDur, nil)
	}
}

// scan calls the source. A source that panics has failed: the server
// keeps the state it has.
func (s *Server) scan(ctx context.Context) (st State, err error) {
	defer func() {
		if r := recover(); r != nil {
			st, err = nil, fmt.Errorf("the scan stopped with a fault: %v\n%s", r, debug.Stack())
		}
	}()
	st, err = s.src.Scan(ctx)
	if err == nil && st == nil {
		err = errors.New("the scan returned nothing")
	}
	return st, err
}

// render writes the body of /metrics. set holds the metrics of a new
// state; nil keeps the metrics of the state before and renews only the
// figures about the exporter.
func (s *Server) render(set *exporter.Set) {
	prev := s.page.Load()
	if set == nil && !prev.ready {
		s.page.Store(&page{reason: "no scan has succeeded yet; the last one failed: " + firstLine(s.lastErr)})
		return
	}
	p := &page{ready: true, found: prev.found}
	if set != nil {
		var f bytes.Buffer
		set.WriteTo(&f)
		p.found = f.Bytes()
	}
	var b bytes.Buffer
	b.Write(p.found)
	self := &exporter.Set{}
	self.Add(exporter.BuildInfo, 1, buildinfo.Version, buildinfo.Revision(), engine.Version)
	self.Add(exporter.LastScan, float64(s.lastOK.UnixMilli())/1000)
	self.Add(exporter.ScanDuration, s.lastDur.Seconds())
	self.Add(exporter.ScanFailures, float64(s.failures))
	self.WriteTo(&b)

	p.plain = b.Bytes()
	var z bytes.Buffer
	zw := gzip.NewWriter(&z)
	zw.Write(p.plain)
	zw.Close()
	p.gz = z.Bytes()
	s.page.Store(p)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// Handler returns the HTTP handler of the server.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	if s.opts.Metrics {
		mux.HandleFunc("/metrics", s.metrics)
	}
	mux.HandleFunc("/-/healthy", func(w http.ResponseWriter, _ *http.Request) { plain(w, http.StatusOK, "ok\n") })
	mux.HandleFunc("/-/ready", func(w http.ResponseWriter, _ *http.Request) {
		if p := s.page.Load(); !p.ready {
			plain(w, http.StatusServiceUnavailable, p.reason+"\n")
			return
		}
		plain(w, http.StatusOK, "ok\n")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			plain(w, http.StatusNotFound, "not found; / lists what this server answers\n")
			return
		}
		plain(w, http.StatusOK, s.index())
	})
	return readOnly(mux)
}

// readOnly lets only requests through that read.
func readOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			plain(w, http.StatusMethodNotAllowed, "this server only answers GET and HEAD\n")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func plain(w http.ResponseWriter, status int, body string) {
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	fmt.Fprint(w, body)
}

// index is the page at "/": what the server answers.
func (s *Server) index() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", buildinfo.String())
	if s.opts.Metrics {
		b.WriteString("/metrics     metrics in the Prometheus text format\n")
	}
	b.WriteString("/-/healthy   200 while the process runs\n")
	b.WriteString("/-/ready     200 once the first scan has finished\n")
	return b.String()
}

// metrics answers /metrics from the page the last scan rendered.
func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	p := s.page.Load()
	if !p.ready {
		// No data yet. A scrape that fails shows as a target that is down,
		// which is what the exporter is until its first scan succeeds.
		plain(w, http.StatusServiceUnavailable, p.reason+"\n")
		return
	}
	h := w.Header()
	h.Set("Content-Type", exporter.ContentType)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	h.Add("Vary", "Accept-Encoding")
	body := p.plain
	if acceptsGzip(r) {
		h.Set("Content-Encoding", "gzip")
		body = p.gz
	}
	h.Set("Content-Length", fmt.Sprint(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		w.Write(body)
	}
}

// acceptsGzip reports whether the client takes a gzip body.
func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.EqualFold(strings.TrimSpace(name), "gzip") && strings.ReplaceAll(params, " ", "") != "q=0" {
			return true
		}
	}
	return false
}
