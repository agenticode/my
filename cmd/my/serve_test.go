package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/synth"
	"github.com/agenticode/my/internal/term"
	"github.com/agenticode/my/internal/testledger"
	"github.com/agenticode/my/internal/testutil"
)

// lockedBuffer is a buffer the server writes its log to while the test
// reads it.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// server is a running "my serve".
type server struct {
	t    *testing.T
	url  string
	log  *lockedBuffer
	stop context.CancelFunc
	done chan int
}

var listenRE = regexp.MustCompile(`http://127\.0\.0\.1:[0-9]+`)

// startServe runs "my serve" with the arguments on a free port of the
// loopback interface and waits until it listens.
func startServe(t *testing.T, env map[string]string, args ...string) *server {
	t.Helper()
	s := &server{t: t, log: &lockedBuffer{}, done: make(chan int, 1)}
	ctx, stop := context.WithCancel(context.Background())
	s.stop = stop
	e := &cli.Env{Stdout: io.Discard, Stderr: s.log, Getenv: func(k string) string { return env[k] }}
	go func() {
		s.done <- app().Run(ctx, e, append([]string{"serve", "--listen", "127.0.0.1:0"}, args...))
	}()
	t.Cleanup(func() { s.end() })
	deadline := time.Now().Add(20 * time.Second)
	for {
		if s.url = listenRE.FindString(s.log.String()); s.url != "" {
			return s
		}
		select {
		case code := <-s.done:
			s.done <- code
			t.Fatalf("my serve ended with exit code %d before it listened:\n%s", code, s.log.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("my serve did not listen:\n%s", s.log.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// end stops the server and returns its exit code.
func (s *server) end() int {
	s.stop()
	select {
	case code := <-s.done:
		s.done <- code
		return code
	case <-time.After(30 * time.Second):
		s.t.Errorf("my serve did not stop:\n%s", s.log.String())
		return -1
	}
}

// get asks the server.
func (s *server) get(path string) (int, string) {
	s.t.Helper()
	c := &http.Client{Timeout: 10 * time.Second}
	resp, err := c.Get(s.url + path)
	if err != nil {
		s.t.Fatalf("GET %s: %v\n%s", path, err, s.log.String())
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		s.t.Fatalf("GET %s: %v", path, err)
	}
	return resp.StatusCode, string(b)
}

// metrics waits for /metrics to satisfy ok and returns it parsed.
func (s *server) metrics(ok func(x *testutil.Exposition) bool) *testutil.Exposition {
	s.t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		code, body := s.get("/metrics")
		if code == http.StatusOK {
			if x := testutil.MustParseExposition(s.t, body); ok == nil || ok(x) {
				return x
			}
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("/metrics did not get there: %d\n%s\nlog:\n%s", code, body, s.log.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// tree writes the host tree of a scenario below dir and returns its root.
func tree(t *testing.T, dir, scenario string) string {
	t.Helper()
	root := filepath.Join(dir, scenario)
	if err := testledger.Generate(t, scenario, 1, nil).WriteHostTree(root, synth.FlavorIntel); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestServeMetrics runs the exporter on a copy of the demo host and checks
// that its metrics say what the commands say about the same host.
func TestServeMetrics(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	root := hostTree(t)
	s := startServe(t, fixed, "--metrics", "--root", root, "--interval", "1h")
	x := s.metrics(nil)

	// The overview of the same host, as the command prints it.
	code, out, errOut := runEnv(fixed, "--root", root, "--json", "--all")
	if code != 3 {
		t.Fatalf("my --json: exit code %d, stderr %q", code, errOut)
	}
	var ov struct {
		Host string `json:"host"`
		Data struct {
			Ranking []struct {
				Slot string `json:"slot"`
				DIMM struct {
					Serial string `json:"serial"`
				} `json:"dimm"`
				CE       int64 `json:"ce"`
				UE       int64 `json:"ue"`
				Faults   int   `json:"faults"`
				ExitCode int   `json:"-"`
				Grade    string
			} `json:"ranking"`
			Sources []struct {
				Kind   string `json:"kind"`
				Status string `json:"status"`
			} `json:"sources"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &ov); err != nil {
		t.Fatal(err)
	}
	grades := map[string]float64{"ok": 0, "observe": 1, "consider-replacement": 2, "replace-now": 3, "hold-replacement": 4}
	if len(ov.Data.Ranking) != 16 {
		t.Fatalf("%d DIMMs in the overview", len(ov.Data.Ranking))
	}
	for _, r := range ov.Data.Ranking {
		labels := fmt.Sprintf(`{host="%s",slot="%s",serial="%s"}`, ov.Host, r.Slot, r.DIMM.Serial)
		for metric, want := range map[string]float64{"my_dimm_verdict": grades[r.Grade], "my_dimm_ce_total": float64(r.CE), "my_dimm_ue_total": float64(r.UE)} {
			if got, ok := x.Series[metric+labels]; !ok || got != want {
				t.Errorf("%s%s = %v (present %v), the overview says %v", metric, labels, got, ok, want)
			}
		}
		faults := 0.0
		for series, v := range x.Series {
			if strings.HasPrefix(series, fmt.Sprintf(`my_dimm_faults{host="%s",slot="%s",`, ov.Host, r.Slot)) {
				faults += v
			}
		}
		if faults != float64(r.Faults) {
			t.Errorf("my_dimm_faults of %s sum to %v, the overview says %d", r.Slot, faults, r.Faults)
		}
	}
	for _, src := range ov.Data.Sources {
		series := fmt.Sprintf(`my_source_grade{host="%s",source="%s",status="%s"}`, ov.Host, src.Kind, src.Status)
		if _, ok := x.Series[series]; !ok {
			t.Errorf("no series %s for a source of the overview", series)
		}
	}
	if x.Series["my_exporter_scan_failures_total"] != 0 || x.Series["my_exporter_last_scan_timestamp_seconds"] == 0 {
		t.Errorf("exporter metrics: %v failures, last scan %v", x.Series["my_exporter_scan_failures_total"], x.Series["my_exporter_last_scan_timestamp_seconds"])
	}

	if code, body := s.get("/-/ready"); code != http.StatusOK || body != "ok\n" {
		t.Errorf("/-/ready = %d %q", code, body)
	}
	if code, body := s.get("/"); code != http.StatusOK || !strings.Contains(body, "/metrics") {
		t.Errorf("/ = %d %q", code, body)
	}

	// A clean stop: exit code 0, the log says so, and the ledger of the
	// scan is gone with its directory.
	if code := s.end(); code != cli.ExitOK {
		t.Errorf("exit code %d after a stop", code)
	}
	log := s.log.String()
	wantLines(t, log, "listening on http://127.0.0.1:", "host r12-n034, finding replace now, 16 DIMMs, 2 with errors or a finding", "stopped")
	if strings.Count(log, "\n") != 3 {
		t.Errorf("log:\n%s", log)
	}
	left, _ := os.ReadDir(tmp)
	for _, e := range left {
		if strings.HasPrefix(e.Name(), "my-") {
			t.Errorf("%s was left behind in the temporary directory", e.Name())
		}
	}
}

// TestServeScansAgain changes the host under a running exporter. The next
// scan shows the change; a scan that cannot read the host is counted and
// leaves the metrics as they were.
func TestServeScansAgain(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", filepath.Join(dir, "tmp"))
	if err := os.Mkdir(filepath.Join(dir, "tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	demo, other := tree(t, dir, "demo"), tree(t, dir, "offline-growing")
	root := filepath.Join(dir, "host")
	if err := os.Rename(demo, root); err != nil {
		t.Fatal(err)
	}
	s := startServe(t, nil, "--metrics", "--root", root, "--interval", "1s", "--lang", "ko")
	const c1 = `my_dimm_verdict{host="r12-n034",slot="CPU0_C1",serial="3B9C56"}`
	first := s.metrics(func(x *testutil.Exposition) bool { return x.Series[c1] == 3 })
	stamp := first.Series["my_exporter_last_scan_timestamp_seconds"]

	// The exporter scans again without being asked.
	s.metrics(func(x *testutil.Exposition) bool { return x.Series["my_exporter_last_scan_timestamp_seconds"] > stamp })

	// The host goes away: scans fail, the metrics stay.
	if err := os.Rename(root, demo); err != nil {
		t.Fatal(err)
	}
	gone := s.metrics(func(x *testutil.Exposition) bool { return x.Series["my_exporter_scan_failures_total"] >= 1 })
	if gone.Series[c1] != 3 {
		t.Errorf("after a failed scan %s = %v", c1, gone.Series[c1])
	}

	// Another host takes its place: the metrics are those of the new host.
	if err := os.Rename(other, root); err != nil {
		t.Fatal(err)
	}
	now := s.metrics(func(x *testutil.Exposition) bool {
		_, old := x.Series[c1]
		return !old
	})
	offlined := 0.0
	for series, v := range now.Series {
		if strings.HasPrefix(series, "my_dimm_offlined_pages{") {
			offlined += v
		}
	}
	if offlined < 2 || now.Series["my_exporter_scan_failures_total"] < 1 {
		t.Errorf("after the change: %v pages offline, %v failed scans", offlined, now.Series["my_exporter_scan_failures_total"])
	}

	if code := s.end(); code != cli.ExitOK {
		t.Errorf("exit code %d", code)
	}
	// The log is in the language that was asked for, and a scan that found
	// what the scan before found is not logged again.
	log := s.log.String()
	wantLines(t, log, "에서 요청을 받음", "호스트 r12-n034, 소견 즉시 교체, DIMM 16개", "스캔 실패, 직전 스캔의 상태 유지", "종료")
	if n := strings.Count(log, "호스트 r12-n034"); n != 1 {
		t.Errorf("the scan of the first host is logged %d times:\n%s", n, log)
	}
}

func TestServeWithoutMetrics(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	s := startServe(t, fixed, "--root", hostTree(t))
	if code, _ := s.get("/metrics"); code != http.StatusNotFound {
		t.Errorf("/metrics = %d without --metrics", code)
	}
	if code, body := s.get("/-/healthy"); code != http.StatusOK || body != "ok\n" {
		t.Errorf("/-/healthy = %d %q", code, body)
	}
	s.end()
	wantLines(t, s.log.String(), "/metrics is off; start with --metrics")
}

func TestServeMistakes(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"address without a port", []string{"serve", "--listen", "localhost"}, "--listen localhost: want an address such as 127.0.0.1:10163 or :10163"},
		{"port out of range", []string{"serve", "--listen", "127.0.0.1:99999"}, "--listen 127.0.0.1:99999: want an address"},
		{"port by name", []string{"serve", "--listen", ":http"}, "--listen :http: want an address"},
		{"interval without a unit", []string{"serve", "--interval", "5"}, "--interval 5: want a time of at least 1s, such as 30s or 5m"},
		{"interval too short", []string{"serve", "--interval", "200ms"}, "--interval 200ms: want a time of at least 1s"},
		{"interval of zero", []string{"serve", "--interval", "0s"}, "--interval 0s: want a time"},
		{"stray argument", []string{"serve", "CPU0_C1"}, `unexpected argument "CPU0_C1"`},
		{"root that is a file", []string{"serve", "--root", file}, "--root " + file + ": not a directory"},
		{"unknown flag", []string{"serve", "--ledger", "x.db"}, "flag provided but not defined: -ledger"},
		{"unknown threshold", []string{"serve", "--set", "nope=1"}, "nope"},
		{"unknown language", []string{"serve", "--lang", "fr"}, `unknown language "fr"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			code, out, errOut := run(tt.args...)
			if code != cli.ExitUsage || out != "" || !strings.Contains(errOut, tt.want) || !strings.Contains(errOut, `run "my help serve" for usage`) {
				t.Errorf("exit code %d, stdout %q, stderr %q; want %d and %q", code, out, errOut, cli.ExitUsage, tt.want)
			}
		})
	}
	// A message in the language that was asked for.
	if code, _, errOut := run("serve", "--interval", "1", "--lang", "ko"); code != cli.ExitUsage || !strings.Contains(errOut, "1s 이상의 시간이어야 함") {
		t.Errorf("exit code %d, stderr %q", code, errOut)
	}
}

// TestServeCannotListen: a port that is taken is a failure at once, before
// any scan.
func TestServeCannotListen(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	code, out, errOut := run("serve", "--metrics", "--listen", ln.Addr().String(), "--root", hostTree(t))
	if code != cli.ExitFailure || out != "" || !strings.Contains(errOut, "my serve: listen tcp "+ln.Addr().String()) {
		t.Errorf("exit code %d, stdout %q, stderr %q", code, out, errOut)
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("%d entries left in the temporary directory", len(left))
	}
}

func TestListenAddress(t *testing.T) {
	for in, want := range map[string]string{
		"127.0.0.1:10163": "127.0.0.1:10163", ":10163": ":10163", "10163": ":10163", "[::1]:9100": "[::1]:9100", "0.0.0.0:0": "0.0.0.0:0",
		"metrics.example:9100": "metrics.example:9100",
	} {
		if got, err := listenAddress(in); err != nil || got != want {
			t.Errorf("listenAddress(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "localhost", "127.0.0.1", "::1", ":", "127.0.0.1:", ":-1", ":65536", "1.2.3.4:80:90", "70000"} {
		if got, err := listenAddress(in); err == nil {
			t.Errorf("listenAddress(%q) = %q, want an error", in, got)
		}
	}
}

// TestServeHelp checks the help of the command in both languages: every
// flag it takes, none it does not take, and lines that fit.
func TestServeHelp(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.EN, i18n.KO} {
		for _, args := range [][]string{{"serve", "--help"}, {"help", "serve"}, {"serve", "?"}} {
			code, out, errOut := run(append(append([]string{}, args...), "--lang", string(lang))...)
			if code != cli.ExitOK || errOut != "" {
				t.Fatalf("%v: exit code %d, stderr %q", args, code, errOut)
			}
			flat := strings.Join(strings.Fields(out), " ")
			if lang == i18n.EN {
				wantLines(t, flat, "Usage: my serve --metrics [--listen ADDR] [--interval TIME] [flags]", "It changes nothing on the host.",
					"No metric has an address as a label.", "default 127.0.0.1:10163", "has no authentication", "the exit code is then 0")
			} else if !strings.Contains(out, "사용법:") || !strings.Contains(out, "옵션:") || !strings.Contains(flat, "주소를 라벨로 쓰는 지표는 없습니다") {
				t.Errorf("%v: not Korean:\n%s", args, out)
			}
			// A server shows no grade and scans on its own schedule: the
			// paragraphs about that are not in its help.
			for _, not := range []string{"Exit code: the grade", "종료 코드: 보여준", "my open case.mycase serve", "--json", "--ledger"} {
				if strings.Contains(out, not) {
					t.Errorf("%v (%s): the help says %q", args, lang, not)
				}
			}
			known := map[string]bool{"--help": true}
			newServeOptions().fs.VisitAll(func(f *flag.Flag) {
				if f.Name == "h" || f.Name == "help" {
					return
				}
				known["--"+f.Name] = true
				if !strings.Contains(out, "--"+f.Name) {
					t.Errorf("help of serve (%s) lacks --%s", lang, f.Name)
				}
			})
			for _, f := range flagRE.FindAllString(out, -1) {
				if !known[f] {
					t.Errorf("help of serve (%s) names %s, which the command does not take", lang, f)
				}
			}
			for _, line := range strings.Split(out, "\n") {
				if term.Width(line) > term.DefaultWidth {
					t.Errorf("help of serve (%s): line wider than %d columns: %q", lang, term.DefaultWidth, line)
				}
			}
		}
	}
	// The command is in the command list, in both languages.
	if _, out, _ := run("help"); !strings.Contains(out, "\n  serve ") {
		t.Errorf("the command list lacks serve:\n%s", out)
	}
	if _, out, _ := run("help", "--lang", "ko"); !strings.Contains(out, "exporter로 실행") {
		t.Errorf("the Korean command list lacks serve:\n%s", out)
	}
}
