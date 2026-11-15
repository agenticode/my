package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter/rasdaemon"
	"github.com/agenticode/my/internal/adapter/rasdaemon/storetest"
	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/exporter"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/serve"
	"github.com/agenticode/my/internal/testutil"
)

// newTestFleet returns a source that reads the stand-in store, with a
// clock the test moves.
func newTestFleet(t *testing.T, srv *storetest.Server, only string, at *time.Time) *fleetSource {
	t.Helper()
	saved := openStore
	openStore = func(ctx context.Context, cfg rasdaemon.StoreConfig) (*rasdaemon.Store, error) {
		return rasdaemon.NewStore(ctx, srv.DB(t), cfg.Backend, cfg.Schema, srv.Origin())
	}
	t.Cleanup(func() { openStore = saved })
	env := &cli.Env{Stdout: io.Discard, Stderr: io.Discard, Getenv: func(string) string { return "" }}
	f := newFleetSource(env, i18n.EN, engine.DefaultConfig(), fleetFlags{backend: srv.Dialect}, only, 5*time.Minute)
	f.clock = func() time.Time { return *at }
	t.Cleanup(f.close)
	return f
}

// scanFleetOnce runs one scan and returns its state.
func scanFleetOnce(t *testing.T, f *fleetSource) *fleetState {
	t.Helper()
	st, err := f.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return st.(*fleetState)
}

// newRow is a corrected error of a slot of web-01.
func newRow(at time.Time) map[string]any {
	return map[string]any{"timestamp": at, "err_count": int64(1), "err_type": "Corrected", "err_msg": "memory read error",
		"label": "CPU_SrcID#1_MC#1_Chan#2_DIMM#1", "mc": int64(1), "top_layer": int64(2), "middle_layer": int64(1), "lower_layer": int64(-1)}
}

// TestFleetSourceReadsWhatChanged follows a store over several scans: the
// first reads every host, a later one only the host that got a new row.
func TestFleetSourceReadsWhatChanged(t *testing.T) {
	srv := storetest.NewServer(storetest.MySQL)
	srv.Load(t, "intel-skx-0.6.sql", "web-01", true)
	srv.Load(t, "ghes-1.0.sql", "db-07", true)
	srv.Load(t, "amd-upgraded-0.8.sql", "web-02", true)
	at := time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)
	f := newTestFleet(t, srv, "", &at)

	st := scanFleetOnce(t, f)
	perHost := rowReads(srv) / 3
	if st.read != 3 || len(st.names) != 3 || rowReads(srv) == 0 || rowReads(srv)%3 != 0 {
		t.Fatalf("first scan: %d hosts read, hosts %v, %d row reads", st.read, st.names, rowReads(srv))
	}
	var b strings.Builder
	st.Metrics().WriteTo(&b)
	x := testutil.MustParseExposition(t, b.String())
	for series, want := range map[string]float64{
		`my_dimm_verdict{host="db-07",slot="NODE 1 CPU1_DIMM_B1",serial=""}`:              3,
		`my_dimm_verdict{host="web-01",slot="CPU_SrcID#0_MC#0_Chan#1_DIMM#0",serial=""}`:  2,
		`my_dimm_ce_total{host="web-01",slot="CPU_SrcID#1_MC#1_Chan#2_DIMM#1",serial=""}`: 1,
		`my_source_grade{host="web-02",source="rasdaemon",status="partial"}`:              3,
		`my_source_grade{host="db-07",source="rasdaemon",status="ok"}`:                    3,
	} {
		if got, ok := x.Series[series]; !ok || got != want {
			t.Errorf("%s = %v (present %v), want %v", series, got, ok, want)
		}
	}
	if got := st.summary(i18n.EN); got != "mysql://db.example:3306/rasdaemon, 3 hosts: 2 replace now, 1 consider replacement" {
		t.Errorf("summary: %s", got)
	}
	if got := st.summary(i18n.KO); got != "mysql://db.example:3306/rasdaemon, 호스트 3개: 즉시 교체 2, 교체 고려 1" {
		t.Errorf("summary in Korean: %s", got)
	}

	// Nothing new: no host is read.
	before := rowReads(srv)
	at = at.Add(5 * time.Minute)
	if st = scanFleetOnce(t, f); st.read != 0 || rowReads(srv) != before || len(st.names) != 3 {
		t.Errorf("a scan without new rows read %d hosts with %d statements", st.read, rowReads(srv)-before)
	}

	// One new row for web-01: that host is read, and it alone.
	host := "web-01"
	srv.Add("mc_event", &host, newRow(at))
	at = at.Add(5 * time.Minute)
	st = scanFleetOnce(t, f)
	if st.read != 1 || rowReads(srv)-before != perHost {
		t.Errorf("after one new row: %d hosts read with %d statements, want 1 with %d", st.read, rowReads(srv)-before, perHost)
	}
	if v, _ := st.Metrics().Value(exporter.DIMMCorrected, "web-01", "CPU_SrcID#1_MC#1_Chan#2_DIMM#1", ""); v != 2 {
		t.Errorf("the new error is not counted: %v", v)
	}
	if v, _ := st.Metrics().Value(exporter.DIMMVerdict, "db-07", "NODE 1 CPU1_DIMM_B1", ""); v != 3 {
		t.Errorf("the host that was not read lost its series: %v", v)
	}

	// A host that is new to the store shows up with its first row.
	fresh := "web-03"
	srv.Add("mc_event", &fresh, newRow(at))
	at = at.Add(5 * time.Minute)
	if st = scanFleetOnce(t, f); st.read != 1 || len(st.names) != 4 {
		t.Errorf("a new host: %d hosts read, hosts %v", st.read, st.names)
	}

	// Rows without a host name are counted and belong to no host.
	srv.Add("mc_event", nil, newRow(at))
	at = at.Add(5 * time.Minute)
	if st = scanFleetOnce(t, f); st.unnamed != 1 || st.read != 0 || len(st.names) != 4 {
		t.Errorf("a row without a host name: unnamed %d, %d hosts read, hosts %v", st.unnamed, st.read, st.names)
	}
}

// TestFleetSourceAgesFindings: a host without new rows is read again when
// its findings are six hours old, because findings are as of the time they
// are made. Not every such host is read by one scan.
func TestFleetSourceAgesFindings(t *testing.T) {
	srv := storetest.NewServer(storetest.PostgreSQL)
	for i := 0; i < 6; i++ {
		srv.Load(t, "ghes-1.0.sql", fmt.Sprintf("n%02d", i), true)
	}
	at := time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)
	f := newTestFleet(t, srv, "", &at)
	if st := scanFleetOnce(t, f); st.read != 6 {
		t.Fatalf("first scan read %d hosts", st.read)
	}
	at = at.Add(fleetMaxAge - time.Minute)
	if st := scanFleetOnce(t, f); st.read != 0 {
		t.Errorf("%d hosts read before their findings were six hours old", st.read)
	}
	// Six hours on: the oldest findings are made again, one host per scan
	// for a fleet of this size, until all are fresh.
	at = at.Add(2 * time.Minute)
	total := 0
	for i := 0; i < 6; i++ {
		st := scanFleetOnce(t, f)
		if st.read != 1 {
			t.Fatalf("scan %d after six hours read %d hosts, want 1", i, st.read)
		}
		total += st.read
		at = at.Add(5 * time.Minute)
	}
	if st := scanFleetOnce(t, f); total != 6 || st.read != 0 {
		t.Errorf("%d hosts read again, then %d more", total, st.read)
	}
}

// TestFleetSourceFullListing: every twelfth scan lists the store in full.
// It finds a host whose rows were removed, and rows that the listing of
// new rows did not show.
func TestFleetSourceFullListing(t *testing.T) {
	srv := storetest.NewServer(storetest.MySQL)
	srv.Load(t, "ghes-1.0.sql", "db-07", true)
	srv.Load(t, "ghes-1.0.sql", "db-08", true)
	at := time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)
	high := "db-09"
	row := newRow(at)
	row["id"] = int64(500)
	srv.Add("mc_event", &high, row)
	f := newTestFleet(t, srv, "", &at)
	if st := scanFleetOnce(t, f); len(st.names) != 3 {
		t.Fatalf("hosts %v", st.names)
	}

	// A row with a number below the highest one seen, as a row has that
	// another host stored a moment earlier and that became visible a
	// moment later: a listing of new rows does not show it. The host
	// db-08 leaves the store.
	low := "db-07"
	row = newRow(at)
	row["id"] = int64(400)
	srv.Add("mc_event", &low, row)
	srv.Remove("db-08")
	srv.Remove("db-09")
	for f.scans < fleetFullEvery-1 {
		at = at.Add(time.Minute)
		if st := scanFleetOnce(t, f); st.read != 0 || len(st.names) != 3 {
			t.Fatalf("scan %d: %d hosts read, hosts %v", f.scans, st.read, st.names)
		}
	}
	at = at.Add(time.Minute)
	st := scanFleetOnce(t, f)
	if f.scans != fleetFullEvery || st.read != 1 || len(st.names) != 1 || st.names[0] != "db-07" {
		t.Errorf("the full listing: scan %d, %d hosts read, hosts %v", f.scans, st.read, st.names)
	}
	if _, ok := st.Metrics().Value(exporter.SourceGrade, "db-08", "rasdaemon", "ok"); ok {
		t.Error("a host that left the store still has series")
	}
}

// TestFleetSourceFailures: a host that cannot be read fails the scan and
// is read by the next one; the findings for the other hosts stay.
func TestFleetSourceFailures(t *testing.T) {
	srv := storetest.NewServer(storetest.MySQL)
	srv.Load(t, "ghes-1.0.sql", "db-07", true)
	srv.Load(t, "intel-skx-0.6.sql", "web-01", true)
	at := time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)
	f := newTestFleet(t, srv, "", &at)

	srv.Fail["GROUP BY"] = errors.New("Error 2013 (HY000): Lost connection to MySQL server during query")
	if _, err := f.Scan(context.Background()); err == nil || !strings.Contains(err.Error(), "Lost connection") {
		t.Fatalf("the listing fails: %v", err)
	}
	// The table of machine checks cannot be read: db-07 has none and is
	// read, web-01 fails.
	srv.Fail = map[string]error{"FROM `extlog_event` WHERE": errors.New("Error 1142 (42000): SELECT command denied")}
	if _, err := f.Scan(context.Background()); err == nil || !strings.Contains(err.Error(), "host db-07: table extlog_event: Error 1142") {
		t.Fatalf("a host that cannot be read: %v", err)
	}
	srv.Fail = map[string]error{}
	st := scanFleetOnce(t, f)
	if st.read != 2 || len(st.names) != 2 {
		t.Errorf("after the failure: %d hosts read, hosts %v", st.read, st.names)
	}

	// --host names a host the store does not hold.
	other := newTestFleet(t, srv, "web-09", &at)
	if _, err := other.Scan(context.Background()); err == nil || !strings.Contains(err.Error(), "no host named web-09 in mysql://db.example:3306/rasdaemon (2 hosts: db-07, web-01)") {
		t.Errorf("a host that is not in the store: %v", err)
	}
	// --host names one that is there: the server shows that host alone.
	one := newTestFleet(t, srv, "DB-07", &at)
	if st := scanFleetOnce(t, one); st.read != 1 || len(st.names) != 1 || st.names[0] != "db-07" {
		t.Errorf("--host: %d hosts read, hosts %v", st.read, st.names)
	}
}

// TestServeFleet runs the server on the store of a fleet: the metrics of
// every host on one page, and the detail of a DIMM read when it is asked
// for.
func TestServeFleet(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	srv, _ := fleetServer(t, storetest.PostgreSQL)
	s := startServe(t, fixed, "--metrics", "--fleet", "postgresql", "--interval", "1s")
	x := s.metrics(nil)
	for series, want := range map[string]float64{
		`my_dimm_verdict{host="db-07",slot="NODE 1 CPU1_DIMM_B1",serial=""}`:             3,
		`my_dimm_verdict{host="web-01",slot="CPU_SrcID#0_MC#0_Chan#1_DIMM#0",serial=""}`: 2,
		`my_dimm_verdict{host="web-02",slot="cxl:mem0",serial="0x1234"}`:                 2,
		`my_exporter_scan_failures_total`:                                                0,
	} {
		if got, ok := x.Series[series]; !ok || got != want {
			t.Errorf("%s = %v (present %v), want %v", series, got, ok, want)
		}
	}
	hosts := map[string]bool{}
	for series := range x.Series {
		if i := strings.Index(series, `host="`); i >= 0 {
			hosts[series[i+6:i+6+strings.IndexByte(series[i+6:], '"')]] = true
		}
	}
	if len(hosts) != 3 {
		t.Errorf("hosts on the page: %v", hosts)
	}

	// The server follows the store: a new row shows on a later page.
	host := "web-01"
	srv.Add("mc_event", &host, newRow(time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)))
	const series = `my_dimm_ce_total{host="web-01",slot="CPU_SrcID#1_MC#1_Chan#2_DIMM#1",serial=""}`
	s.metrics(func(x *testutil.Exposition) bool { return x.Series[series] == 2 })

	// The detail of a DIMM of one host is what the command prints for it.
	for path, args := range map[string][]string{
		"/api/dimm/CPU2_DIMM_C2?host=db-07":                            {"why", "CPU2_DIMM_C2", "--host", "db-07"},
		"/api/dimm?host=web-02&lang=ko":                                {"why", "--host", "web-02", "--lang", "ko"},
		"/api/dimm/NODE%201%20CPU1_DIMM_B1?host=DB-07":                 {"why", "NODE 1 CPU1_DIMM_B1", "--host", "db-07"},
		"/api/dimm/cxl:mem0?host=web-02&all=1":                         {"why", "cxl:mem0", "--host", "web-02"},
		"/api/dimm/CPU_SrcID%230_MC%230_Chan%231_DIMM%230?host=web-01": {"why", "CPU_SrcID#0_MC#0_Chan#1_DIMM#0", "--host", "web-01"},
	} {
		code, body := s.get(path)
		_, want, errOut := runEnv(fixed, append(args, "--fleet", "postgresql", "--json")...)
		if code != http.StatusOK || errOut != "" || body != want {
			t.Errorf("GET %s = %d and differs from my %s --json (stderr %q):\n%s\nwant:\n%s", path, code, strings.Join(args, " "), errOut, body, want)
		}
	}
	for path, want := range map[string]struct {
		code int
		text string
	}{
		"/api/dimm/CPU2_DIMM_C2":             {http.StatusBadRequest, "name the host with ?host=NAME: the store holds 3 hosts (db-07, web-01, web-02)"},
		"/api/dimm/CPU2_DIMM_C2?host=web-09": {http.StatusNotFound, "no host named web-09 in postgresql://db.example:5432/rasdaemon?schema=rasdaemon (3 hosts: db-07, web-01, web-02)"},
		"/api/dimm/CPU9_Z9?host=db-07":       {http.StatusNotFound, `no slot or DIMM "CPU9_Z9" in host db-07.`},
		"/api/dimm?host=db-07&lang=fr":       {http.StatusBadRequest, `unknown language "fr"`},
	} {
		if code, body := s.get(path); code != want.code || !strings.Contains(body, strings.ReplaceAll(want.text, `"`, `\"`)) {
			t.Errorf("GET %s = %d %s, want %d and %q", path, code, body, want.code, want.text)
		}
	}

	// Rows without a host name are in no finding, and the log says so.
	srv.Add("mc_event", nil, newRow(time.Date(2026, 11, 13, 9, 1, 0, 0, time.UTC)))
	deadline := time.Now().Add(30 * time.Second)
	for !strings.Contains(s.log.String(), "1 row of the store carries no host name and was not read") {
		if time.Now().After(deadline) {
			t.Fatalf("the log does not name the row without a host:\n%s", s.log.String())
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The store goes away: the scans fail, the page stays.
	srv.Fail["GROUP BY"] = errors.New("pq: the database system is shutting down")
	kept := s.metrics(func(x *testutil.Exposition) bool { return x.Series["my_exporter_scan_failures_total"] >= 1 })
	if kept.Series[series] != 2 {
		t.Errorf("after a failed scan %s = %v", series, kept.Series[series])
	}
	if code := s.end(); code != cli.ExitOK {
		t.Errorf("exit code %d", code)
	}
	log := s.log.String()
	wantLines(t, log, "postgresql://db.example:5432/rasdaemon?schema=rasdaemon, 3 hosts: 2 replace now, 1 consider replacement",
		"scan failed, the state of the scan before stays: rasdaemon store postgresql://db.example:5432/rasdaemon?schema=rasdaemon: table ", "stopped")
	for _, q := range srv.Statements() {
		if !strings.HasPrefix(q, "SELECT ") {
			t.Errorf("a statement that is not a SELECT: %s", q)
		}
	}
	// The log line docs/exporter.md quotes is a line of this log, but for
	// the time the scan took.
	doc, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "docs", "exporter.md"))
	if err != nil {
		t.Fatal(err)
	}
	const line = ": postgresql://db.example:5432/rasdaemon?schema=rasdaemon, 3 hosts: 2 replace now, 1 consider replacement\n"
	if !strings.Contains(log, line) || !regexp.MustCompile("```\nscan took [0-9]+ms"+regexp.QuoteMeta(line)+"```\n").Match(doc) {
		t.Errorf("docs/exporter.md does not quote the log line of a scan of the fleet:\n%s", log)
	}
}

func TestServeFleetMistakes(t *testing.T) {
	fleetServer(t, storetest.MySQL)
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"with --root", []string{"serve", "--fleet", "mysql", "--root", t.TempDir()}, "--fleet reads a store and --root a copy of a host's files"},
		{"--since without a store", []string{"serve", "--since", "30d"}, "--since goes with --fleet"},
		{"--host without a store", []string{"serve", "--host", "db-07"}, "--host goes with --fleet: it names one host of the store"},
		{"--since that is no time", []string{"serve", "--fleet", "mysql", "--since", "lately"}, "--since lately: want a date (2026-10-01)"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			code, out, errOut := run(tt.args...)
			if code != cli.ExitUsage || out != "" || !strings.Contains(errOut, tt.want) {
				t.Errorf("exit code %d, stdout %q, stderr %q; want %d and %q", code, out, errOut, cli.ExitUsage, tt.want)
			}
		})
	}
	var _ serve.State = (*fleetState)(nil)
}

// TestUnitsStartTheCommand keeps the systemd units of contrib/ in step
// with the command: they start "my serve" with flags it takes.
func TestUnitsStartTheCommand(t *testing.T) {
	for unit, wantFleet := range map[string]bool{"my-exporter.service": false, "my-fleet-exporter.service": true} {
		b, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "contrib", "systemd", unit))
		if err != nil {
			t.Fatal(err)
		}
		var start string
		for _, line := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(line, "ExecStart="); ok {
				start = v
			}
		}
		f := strings.Fields(start)
		if len(f) < 3 || f[0] != "/usr/local/bin/my" || f[1] != "serve" {
			t.Fatalf("%s: ExecStart=%s", unit, start)
		}
		o := newServeOptions()
		// systemd puts the value of the variable in; the tests have none.
		args := strings.Fields(strings.ReplaceAll(strings.Join(f[2:], " "), "${RASDAEMON_DB_BACKEND}", "postgresql"))
		if _, err := cli.Parse(o.fs, args); err != nil {
			t.Errorf("%s: my serve does not take %q: %v", unit, args, err)
		}
		if !o.metrics || o.listen != serve.DefaultListen || o.sf.set() != wantFleet {
			t.Errorf("%s: metrics %v, listen %q, fleet %v", unit, o.metrics, o.listen, o.sf.set())
		}
	}
}
