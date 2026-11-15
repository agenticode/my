package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter/rasdaemon"
	"github.com/agenticode/my/internal/adapter/rasdaemon/storetest"
	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/testutil"
)

// fleetServer puts a stand-in server in the place of the store the
// commands connect to: three hosts in the tables of one database. It
// returns the server and the settings the commands connected with.
func fleetServer(t *testing.T, dialect string) (*storetest.Server, *[]rasdaemon.StoreConfig) {
	t.Helper()
	srv := storetest.NewServer(dialect)
	srv.Load(t, "intel-skx-0.6.sql", "web-01", true)
	srv.Load(t, "ghes-1.0.sql", "db-07", true)
	srv.Load(t, "amd-upgraded-0.8.sql", "web-02", true)
	var got []rasdaemon.StoreConfig
	saved := openStore
	openStore = func(ctx context.Context, cfg rasdaemon.StoreConfig) (*rasdaemon.Store, error) {
		got = append(got, cfg)
		return rasdaemon.NewStore(ctx, srv.DB(t), cfg.Backend, cfg.Schema, srv.Origin())
	}
	t.Cleanup(func() { openStore = saved })
	return srv, &got
}

// rowReads counts the statements that read the rows of a host.
func rowReads(srv *storetest.Server) int {
	n := 0
	for _, q := range srv.Statements() {
		if strings.HasPrefix(q, "SELECT * FROM") {
			n++
		}
	}
	return n
}

// TestFleetOverview reads the store of a fleet: without --host the hosts
// with their findings, the worst first; with --host the screens of one.
func TestFleetOverview(t *testing.T) {
	for _, dialect := range []string{storetest.MySQL, storetest.PostgreSQL} {
		t.Run(dialect, func(t *testing.T) {
			srv, got := fleetServer(t, dialect)
			code, out, errOut := runEnv(fixed, "--fleet", dialect)
			if code != 3 || errOut != "" {
				t.Fatalf("exit code %d, stderr %q\n%s", code, errOut, out)
			}
			wantLines(t, out, "3 hosts in this ledger", "web-02  replace now", "db-07   replace now", "web-01  consider replacement")
			if a, b, c := strings.Index(out, "web-02 "), strings.Index(out, "db-07 "), strings.Index(out, "web-01 "); !(a < b && b < c) {
				t.Errorf("the hosts are not listed worst first:\n%s", out)
			}
			if len(*got) != 1 || (*got)[0].Backend != dialect || (*got)[0].User != "rasdaemon" || (*got)[0].Database != "rasdaemon" {
				t.Errorf("settings the command connected with: %+v", *got)
			}
			// The screen docs/compatibility.md quotes is this screen.
			if dialect == storetest.MySQL {
				doc, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "docs", "compatibility.md"))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(doc), "```\n$ my --fleet mysql\n"+out+"```\n") {
					t.Errorf("docs/compatibility.md does not quote the screen of my --fleet mysql:\n%s", out)
				}
			}

			// The same as JSON: the hosts view.
			code, out, _ = runEnv(fixed, "--fleet", dialect, "--json")
			var doc struct {
				View string `json:"view"`
				Data []struct {
					Name  string `json:"name"`
					Grade string `json:"grade"`
					DIMMs int    `json:"dimms"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(out), &doc); err != nil || code != 3 || doc.View != "hosts" || len(doc.Data) != 3 ||
				doc.Data[0].Name != "web-02" || doc.Data[2].Name != "web-01" || doc.Data[2].Grade != "consider-replacement" {
				t.Errorf("--json: exit code %d, %v, %+v", code, err, doc)
			}

			// One host of the fleet, with the commands that go on from it.
			before := rowReads(srv)
			code, out, errOut = runEnv(fixed, "--fleet", dialect, "--host", "db-07")
			if code != 3 || errOut != "" {
				t.Fatalf("--host: exit code %d, stderr %q", code, errOut)
			}
			wantLines(t, out, "host db-07 ", "host finding: replace now", "NODE 1 CPU1_DIMM_B1", "sources  rasdaemon 11 records",
				"my --fleet "+dialect+" --host db-07 dimm NODE 1 CPU1_DIMM_B1", "my --fleet "+dialect+" --host db-07 bundle -o case.mycase")
			// Only the rows of that host were read: a third of what the
			// overview of the three hosts read, twice.
			if n := rowReads(srv) - before; n == 0 || n*6 != before {
				t.Errorf("%d row reads for one host, %d for three hosts read twice", n, before)
			}
			for _, q := range srv.Statements() {
				if !strings.HasPrefix(q, "SELECT ") {
					t.Errorf("a statement that is not a SELECT: %s", q)
				}
			}

			code, out, _ = runEnv(fixed, "why", "CPU2_DIMM_C2", "--oneline", "--fleet", dialect, "--host", "DB-07")
			if code != 3 || !strings.HasPrefix(out, "db-07 CPU2_DIMM_C2: replace now (finding") {
				t.Errorf("my why on a host of the fleet: exit code %d\n%s", code, out)
			}
			code, out, _ = runEnv(fixed, "dimm", "CPU_SrcID#0_MC#0_Chan#1_DIMM#0", "--fleet", dialect, "--host", "web-01")
			if code != 2 || !strings.Contains(out, "consider replacement") {
				t.Errorf("my dimm on a host of the fleet: exit code %d\n%s", code, out)
			}
			code, out, _ = runEnv(fixed, "timeline", "--fleet", dialect, "--host", "web-02", "--json")
			if code != 3 || !strings.Contains(out, `"view": "timeline"`) || !strings.Contains(out, `"host": "web-02"`) {
				t.Errorf("my timeline on a host of the fleet: exit code %d\n%s", code, out)
			}
		})
	}
}

// TestFleetNeedsAHost: a command that shows one host says so before it
// reads the rows of every host.
func TestFleetNeedsAHost(t *testing.T) {
	srv, _ := fleetServer(t, storetest.MySQL)
	for _, args := range [][]string{{"dimm", "CPU2_DIMM_C2"}, {"why"}, {"map", "CPU2_DIMM_C2"}, {"timeline"}, {"bundle", "-o", filepath.Join(t.TempDir(), "x.mycase")}} {
		code, out, errOut := runEnv(fixed, append(args, "--fleet", "mysql")...)
		if code != cli.ExitUsage || out != "" || !strings.Contains(errOut, "the store holds 3 hosts: name one with --host (db-07, web-01, web-02)") {
			t.Errorf("%v: exit code %d, stderr %q", args, code, errOut)
		}
	}
	if n := rowReads(srv); n != 0 {
		t.Errorf("%d row reads before the command said that it needs a host", n)
	}
	code, _, errOut := runEnv(fixed, "--fleet", "mysql", "--host", "web-09")
	if code != cli.ExitUsage || !strings.Contains(errOut, "no host named web-09 in mysql://db.example:3306/rasdaemon (3 hosts: db-07, web-01, web-02)") {
		t.Errorf("unknown host: exit code %d, stderr %q", code, errOut)
	}
	code, _, errOut = runEnv(fixed, "why", "--fleet", "mysql", "--lang", "ko")
	if code != cli.ExitUsage || !strings.Contains(errOut, "저장소에 호스트가 3개임: --host로 하나를 지정 (db-07, web-01, web-02)") {
		t.Errorf("Korean: exit code %d, stderr %q", code, errOut)
	}

	// A store with one host needs no --host.
	one := storetest.NewServer(storetest.MySQL)
	one.Load(t, "ghes-1.0.sql", "db-07", false)
	openStore = func(ctx context.Context, cfg rasdaemon.StoreConfig) (*rasdaemon.Store, error) {
		return rasdaemon.NewStore(ctx, one.DB(t), cfg.Backend, cfg.Schema, one.Origin())
	}
	if code, out, _ := runEnv(fixed, "why", "--oneline", "--fleet", "mysql"); code != 3 || !strings.Contains(out, "db-07 NODE 1 CPU1_DIMM_B1: replace now") {
		t.Errorf("one host: exit code %d\n%s", code, out)
	}
	// Many hosts: the message names the first few.
	many := storetest.NewServer(storetest.MySQL)
	for _, h := range []string{"n01", "n02", "n03", "n04", "n05", "n06", "n07", "n08", "n09", "n10"} {
		many.Load(t, "ghes-1.0.sql", h, true)
	}
	openStore = func(ctx context.Context, cfg rasdaemon.StoreConfig) (*rasdaemon.Store, error) {
		return rasdaemon.NewStore(ctx, many.DB(t), cfg.Backend, cfg.Schema, many.Origin())
	}
	if _, _, errOut := runEnv(fixed, "why", "--fleet", "mysql"); !strings.Contains(errOut, "the store holds 10 hosts: name one with --host (n01, n02, n03, n04, n05, n06, n07, n08, ...)") {
		t.Errorf("many hosts: stderr %q", errOut)
	}
}

// TestFleetSettings: the settings are those of rasdaemon, from the file of
// --fleet-env first and from the environment for the rest.
func TestFleetSettings(t *testing.T) {
	_, got := fleetServer(t, storetest.PostgreSQL)
	file := filepath.Join(t.TempDir(), "rasdaemon.env")
	if err := os.WriteFile(file, []byte("# the fleet store\nRASDAEMON_DB_BACKEND=postgresql\nRAS_PG_HOST=pg.example\nRAS_PG_USER=reader\nRAS_PG_SCHEMA=\"ras\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"MY_NOW": fixed["MY_NOW"], "RAS_PG_PASSWORD": "from-the-environment", "RAS_PG_USER": "ignored", "RAS_PG_PORT": "6432", "RAS_MYSQL_HOST": "other"}
	code, out, errOut := runEnv(env, "--fleet-env", file)
	// The stand-in holds its tables in the schema rasdaemon: the schema
	// of the file is not there.
	if code != cli.ExitFailure || !strings.Contains(errOut, "no rasdaemon tables in schema ras") {
		t.Fatalf("exit code %d, stderr %q\n%s", code, errOut, out)
	}
	want := rasdaemon.StoreConfig{Backend: "postgresql", Host: "pg.example", Port: 6432, User: "reader", Password: "from-the-environment",
		Database: "rasdaemon", Schema: "ras", ConnectTimeout: 10 * time.Second}
	if len(*got) != 1 || !reflect.DeepEqual((*got)[0], want) {
		t.Errorf("settings:\n got  %+v\n want %+v", *got, want)
	}
	if strings.Contains(errOut+out, "from-the-environment") {
		t.Error("the password is in the output")
	}

	// From the environment alone, with the backend on the command line.
	*got = nil
	code, out, _ = runEnv(map[string]string{"MY_NOW": fixed["MY_NOW"], "RASDAEMON_DB_BACKEND": "mysql", "RAS_PG_HOST": "10.0.0.5"}, "--fleet", "pg", "--host", "db-07", "--json")
	if code != 3 || len(*got) != 1 || (*got)[0].Backend != "postgresql" || (*got)[0].Host != "10.0.0.5" || (*got)[0].Schema != "rasdaemon" {
		t.Errorf("exit code %d, settings %+v\n%s", code, *got, out)
	}

	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"unknown backend", []string{"--fleet", "oracle"}, `--fleet: unknown backend "oracle": want mysql or postgresql`},
		{"sqlite3", []string{"--fleet", "sqlite3"}, "--fleet: the sqlite3 backend keeps a file on each host"},
		{"file that is not there", []string{"--fleet-env", filepath.Join(t.TempDir(), "none.env")}, "--fleet-env open "},
		{"with --root", []string{"--fleet", "mysql", "--root", t.TempDir()}, "--fleet reads a store and --root a copy of a host's files: give one of the two"},
		{"--since without a store", []string{"--since", "30d"}, "--since goes with --fleet"},
		{"--since that is no time", []string{"--fleet", "mysql", "--since", "lately"}, "--since lately: want a date (2026-10-01)"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			code, out, errOut := runEnv(fixed, tt.args...)
			if code != cli.ExitUsage || out != "" || !strings.Contains(errOut, tt.want) {
				t.Errorf("exit code %d, stdout %q, stderr %q; want %d and %q", code, out, errOut, cli.ExitUsage, tt.want)
			}
		})
	}
	bad := filepath.Join(t.TempDir(), "bad.env")
	os.WriteFile(bad, []byte("RASDAEMON_DB_BACKEND mysql\n"), 0o600)
	if code, _, errOut := runEnv(fixed, "--fleet-env", bad); code != cli.ExitUsage || !strings.Contains(errOut, "--fleet-env "+bad+": line 1: want NAME=value") {
		t.Errorf("malformed file: exit code %d, stderr %q", code, errOut)
	}
	noBackend := filepath.Join(t.TempDir(), "nobackend.env")
	os.WriteFile(noBackend, []byte("RAS_PG_HOST=pg.example\n"), 0o600)
	if code, _, errOut := runEnv(fixed, "--fleet-env", noBackend); code != cli.ExitUsage || !strings.Contains(errOut, "--fleet-env "+noBackend+": no backend named: want mysql or postgresql") {
		t.Errorf("file without a backend: exit code %d, stderr %q", code, errOut)
	}
}

func TestFleetSince(t *testing.T) {
	fleetServer(t, storetest.MySQL)
	count := func(args ...string) (ce, ue int64) {
		t.Helper()
		code, out, errOut := runEnv(fixed, append(args, "--fleet", "mysql", "--host", "db-07", "--json")...)
		var doc struct {
			Data struct {
				Host struct {
					CE int64 `json:"ce"`
					UE int64 `json:"ue"`
				} `json:"host"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(out), &doc); err != nil || code > 4 {
			t.Fatalf("%v: exit code %d, %v, stderr %q", args, code, err, errOut)
		}
		return doc.Data.Host.CE, doc.Data.Host.UE
	}
	allCE, allUE := count()
	// The rows of db-07 run from 2 to 6 October 2026.
	ce, ue := count("--since", "2026-10-05")
	if allCE == 0 || ce >= allCE || ue == 0 || ue > allUE {
		t.Errorf("since 5 October: CE %d, UE %d; in all CE %d, UE %d", ce, ue, allCE, allUE)
	}
	// Days back from the clock of the run, 13 November: 60 days hold every row, 10 days none.
	if ce, ue := count("--since", "60d"); ce != allCE || ue != allUE {
		t.Errorf("since 60 days: CE %d, UE %d; in all CE %d, UE %d", ce, ue, allCE, allUE)
	}
	if ce, ue := count("--since", "240h"); ce != 0 || ue != 0 {
		t.Errorf("since 240 hours: CE %d, UE %d", ce, ue)
	}
}

// TestFleetRowsWithoutAHostName: rows that name no host are not read, and
// the command says how many there are.
func TestFleetRowsWithoutAHostName(t *testing.T) {
	srv, _ := fleetServer(t, storetest.MySQL)
	srv.Add("mc_event", nil, map[string]any{"err_type": "Corrected"})
	empty := ""
	srv.Add("mc_event", &empty, map[string]any{"err_type": "Corrected"})
	code, out, errOut := runEnv(fixed, "--fleet", "mysql")
	if code != 3 || !strings.Contains(out, "3 hosts in this ledger") ||
		errOut != "2 rows of the store carry no host name and were not read; RASDAEMON_HOSTNAME gives rasdaemon the name of its host\n" {
		t.Errorf("exit code %d, stderr %q\n%s", code, errOut, out)
	}

	// A store that holds nothing else has nothing to show.
	only := storetest.NewServer(storetest.MySQL)
	only.Load(t, "ghes-1.0.sql", "", false)
	openStore = func(ctx context.Context, cfg rasdaemon.StoreConfig) (*rasdaemon.Store, error) {
		return rasdaemon.NewStore(ctx, only.DB(t), cfg.Backend, cfg.Schema, only.Origin())
	}
	code, _, errOut = runEnv(fixed, "--fleet", "mysql")
	if code != cli.ExitFailure || !strings.Contains(errOut, "carry no host name") || !strings.Contains(errOut, "my: mysql://db.example:3306/rasdaemon holds no rows of a named host") {
		t.Errorf("only rows without a host: exit code %d, stderr %q", code, errOut)
	}
}

// TestFleetFailures: a store that cannot be reached or read fails the
// command, with the name of the store and without the password.
func TestFleetFailures(t *testing.T) {
	srv, _ := fleetServer(t, storetest.MySQL)
	env := map[string]string{"MY_NOW": fixed["MY_NOW"], "RAS_MYSQL_PASSWORD": "s3cret-pw"}

	srv.Fail["FROM `mce_record` WHERE"] = errors.New("Error 1142 (42000): SELECT command denied to user 'reader'@'10.0.0.5' for table 'mce_record'")
	code, out, errOut := runEnv(env, "--fleet", "mysql", "--host", "web-01")
	if code != cli.ExitFailure || out != "" || !strings.Contains(errOut, "my: mysql://db.example:3306/rasdaemon, host web-01: table mce_record: Error 1142") {
		t.Errorf("a table that cannot be read: exit code %d, stderr %q", code, errOut)
	}
	srv.Fail = map[string]error{"GROUP BY": errors.New("Error 2013 (HY000): Lost connection to MySQL server during query")}
	code, _, errOut = runEnv(env, "--fleet", "mysql")
	if code != cli.ExitFailure || !strings.Contains(errOut, "Lost connection") {
		t.Errorf("the host listing fails: exit code %d, stderr %q", code, errOut)
	}

	openStore = func(context.Context, rasdaemon.StoreConfig) (*rasdaemon.Store, error) {
		return nil, errors.New("rasdaemon store mysql://db.example:3306/rasdaemon: dial tcp 10.0.0.9:3306: connect: connection refused")
	}
	code, _, errOut = runEnv(env, "--fleet", "mysql")
	if code != cli.ExitFailure || !strings.Contains(errOut, "my: rasdaemon store mysql://db.example:3306/rasdaemon: dial tcp") || strings.Contains(errOut, "s3cret-pw") {
		t.Errorf("a store that cannot be reached: exit code %d, stderr %q", code, errOut)
	}
}

// TestFleetBundleAndLedger: one host of the fleet becomes a case bundle,
// and --ledger keeps what was read of the fleet.
func TestFleetBundleAndLedger(t *testing.T) {
	fleetServer(t, storetest.PostgreSQL)
	dir := t.TempDir()
	file := filepath.Join(dir, "db-07.mycase")
	code, out, errOut := runEnv(fixed, "bundle", "-o", file, "--fleet", "postgresql", "--host", "db-07")
	// The store holds 3 corrected errors of db-07 that name no DIMM. The
	// line counts them with the errors of the host and says that no
	// finding covers them.
	if code != cli.ExitOK || errOut != "" || !strings.Contains(out, "host db-07: 2 DIMMs, CE 7, UE 3, finding: replace now; CE 3, UE 0 tied to no DIMM are in no finding") {
		t.Fatalf("bundle: exit code %d, stderr %q\n%s", code, errOut, out)
	}
	code, out, _ = runEnv(fixed, "open", file)
	if code != 3 || !strings.Contains(out, "host db-07 ") || !strings.Contains(out, "sources  rasdaemon 11 records") {
		t.Errorf("the bundle of a host of the fleet: exit code %d\n%s", code, out)
	}

	kept := filepath.Join(dir, "fleet.db")
	if code, _, errOut := runEnv(fixed, "--fleet", "postgresql", "--ledger", kept); code != 3 {
		t.Fatalf("--ledger: exit code %d, stderr %q", code, errOut)
	}
	code, out, _ = runEnv(fixed, "open", kept)
	if code != 3 || !strings.Contains(out, "3 hosts in this ledger") {
		t.Errorf("the kept ledger: exit code %d\n%s", code, out)
	}
	if code, out, _ = runEnv(fixed, "open", kept, "why", "--oneline", "--host", "web-01"); code != 2 || !strings.Contains(out, "web-01 CPU_SrcID#0_MC#0_Chan#1_DIMM#0: consider replacement") {
		t.Errorf("a host of the kept ledger: exit code %d\n%s", code, out)
	}
}

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)
	for in, want := range map[string]time.Time{
		"":                     {},
		"30d":                  now.Add(-30 * 24 * time.Hour),
		"72h":                  now.Add(-72 * time.Hour),
		"2026-10-01":           time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		"2026-10-01T06:00:00Z": time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC),
	} {
		if got, err := parseSince(in, now); err != nil || !got.Equal(want) {
			t.Errorf("parseSince(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"lately", "0d", "-3d", "d", "30m", "1e3d", "999999d"} {
		if got, err := parseSince(in, now); err == nil {
			t.Errorf("parseSince(%q) = %v, want an error", in, got)
		}
	}
}
