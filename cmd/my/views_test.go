package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/synth"
	"github.com/agenticode/my/internal/term"
	"github.com/agenticode/my/internal/testledger"
	"github.com/agenticode/my/internal/testutil"

	_ "modernc.org/sqlite" // to build a ledger of an old schema by hand
)

// TestMain runs the tests, or, when the test binary was started as a
// vendor analyzer (see plugin_test.go), plays that part.
func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeAnalyzerEnv); mode != "" {
		os.Exit(fakeAnalyzer(mode))
	}
	testledger.Main(m)
}

// runEnv runs the binary with an environment.
func runEnv(env map[string]string, args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	e := &cli.Env{Stdout: &out, Stderr: &errb, Getenv: func(k string) string { return env[k] }}
	code = app().Run(context.Background(), e, args)
	return code, out.String(), errb.String()
}

// demo is the ledger of the demo scenario.
func demo(t *testing.T) string { return testledger.Path(t, "demo", 1, "") }

// TestJSONGolden pins the JSON output of every command that shows
// findings: the envelope and, in it, the data of the figure.
func TestJSONGolden(t *testing.T) {
	ledger := demo(t)
	for _, tt := range []struct {
		name string
		args []string
		code int
	}{
		{"overview", nil, 3},
		{"dimm", []string{"dimm", "CPU0_C1"}, 3},
		{"why", []string{"why", "CPU1_D1"}, 1},
		{"map", []string{"map", "CPU0_C1", "--bank", "0/3"}, 3},
		{"timeline", []string{"timeline", "CPU0_C1"}, 3},
		{"timeline-host", []string{"timeline"}, 3},
	} {
		args := append(append([]string{"open", ledger}, tt.args...), "--json")
		code, out, errOut := run(args...)
		if code != tt.code || errOut != "" {
			t.Errorf("%s: exit code %d, stderr %q", tt.name, code, errOut)
			continue
		}
		var e struct {
			View     string          `json:"view"`
			Version  int             `json:"version"`
			Lang     string          `json:"lang"`
			Host     string          `json:"host"`
			Grade    string          `json:"grade"`
			ExitCode int             `json:"exit_code"`
			Data     json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(out), &e); err != nil {
			t.Errorf("%s: not JSON: %v", tt.name, err)
			continue
		}
		if e.Version != jsonVersion || e.Lang != "en" || e.Host != "r12-n034" || e.ExitCode != tt.code || len(e.Data) < 20 {
			t.Errorf("%s: envelope = %+v", tt.name, e)
		}
		testutil.Golden(t, "json/"+tt.name+".json", []byte(out))
	}
	// The sentences follow --lang; codes and numbers do not.
	_, en, _ := run("open", ledger, "why", "CPU0_C1", "--json")
	_, ko, _ := run("open", ledger, "why", "CPU0_C1", "--json", "--lang", "ko")
	if !strings.Contains(ko, `"grade_text": "즉시 교체"`) || !strings.Contains(ko, `"code": "ue.located"`) || !strings.Contains(en, `"grade_text": "replace now"`) {
		t.Errorf("--lang ko --json:\n%s", ko)
	}
}

// TestTextMatchesTheRenderer checks that a command prints what the
// renderer draws for the same case: its golden files are the reference.
func TestTextMatchesTheRenderer(t *testing.T) {
	ledger := demo(t)
	golden := func(name string) string {
		b, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "internal", "term", "testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	for _, tt := range []struct {
		golden string
		args   []string
	}{
		{"demo-dimm.en.txt", []string{"dimm", "CPU0_C1"}},
		{"demo-dimm.ko.txt", []string{"dimm", "CPU0_C1", "--lang", "ko"}},
		{"demo-dimm.ascii.txt", []string{"dimm", "--ascii", "CPU0_C1"}},
		{"demo-dimm.w60.txt", []string{"dimm", "CPU0_C1", "--width", "60"}},
		{"demo-dimm.color.txt", []string{"dimm", "CPU0_C1", "--color", "always"}},
		{"demo-why.ko.txt", []string{"why", "--lang=ko", "CPU0_C1"}},
		{"demo-map.ascii.txt", []string{"map", "CPU0_C1", "--ascii"}},
		{"demo-timeline.en.txt", []string{"timeline", "CPU0_C1"}},
		{"demo-timeline-host.ko.txt", []string{"timeline", "--lang", "ko"}},
		{"demo-oneline.en.txt", []string{"why", "CPU0_C1", "--oneline"}},
		{"demo-oneline.ko.txt", []string{"why", "3B9C56", "--oneline", "--lang", "ko"}},
	} {
		_, out, errOut := run(append([]string{"open", ledger}, tt.args...)...)
		if out != golden(tt.golden) || errOut != "" {
			t.Errorf("my open LEDGER %s differs from %s:\n%s%s", strings.Join(tt.args, " "), tt.golden, out, errOut)
		}
	}
}

func TestOverviewCommand(t *testing.T) {
	ledger := demo(t)
	code, out, errOut := run("open", ledger)
	if code != 3 || errOut != "" {
		t.Fatalf("exit code %d, stderr %q", code, errOut)
	}
	wantLines(t, out,
		"host r12-n034  Example Systems R760  2026-07-31 to 2026-11-13  CE 1,284, UE 1  observation grade A",
		"host finding: replace now",
		" CPU0   ░░   ░░   ██   ░░   ░░   ░░   ░░   ░░",
		" 1  CPU0_C1  3B9C56  1,231   1  row         ▃▄▄▄▅▅▆▆▇█ ↑  replace now  R-UE-01 R-ROW-03 R-REP-02",
		" 14 DIMMs without errors",
		// The next commands are spelled for the case that is open.
		"my open "+ledger+" dimm CPU0_C1: examination of this DIMM",
	)
	// --all lists the quiet DIMMs too.
	_, out, _ = run("open", ledger, "--all")
	if n := strings.Count(out, "\n"); n < 30 || !strings.Contains(out, "CPU0_A1") || strings.Contains(out, "DIMMs without errors") {
		t.Errorf("--all:\n%s", out)
	}
	// The language follows the locale, and --lang wins over it.
	_, out, _ = runEnv(map[string]string{"LANG": "ko_KR.UTF-8"}, "open", ledger)
	wantLines(t, out, "호스트 소견: 즉시 교체", "진찰 화면")
	_, out, _ = runEnv(map[string]string{"LANG": "ko_KR.UTF-8"}, "open", ledger, "--lang", "en")
	wantLines(t, out, "host finding: replace now")
	// A locale without UTF-8 gets ASCII glyphs; COLUMNS sets the width.
	_, out, _ = runEnv(map[string]string{"LANG": "C", "COLUMNS": "70"}, "open", ledger)
	wantLines(t, out, " CPU0   ::   ::   ##   ::   ::   ::   ::   ::")
	for _, line := range strings.Split(out, "\n") {
		if term.Width(line) > 70 && !strings.Contains(line, ledger) {
			t.Errorf("line wider than COLUMNS: %q", line)
		}
	}
	// Output that is not a terminal has no colour, whatever the terminal
	// type says; NO_COLOR is not needed for that.
	_, out, _ = runEnv(map[string]string{"TERM": "xterm-256color"}, "open", ledger)
	if strings.Contains(out, "\x1b[") {
		t.Error("escape sequences in output that does not go to a terminal")
	}
}

func TestExitCodeIsTheGrade(t *testing.T) {
	ledger := demo(t)
	for _, tt := range []struct {
		args []string
		code int
	}{
		{nil, 3},                             // the host
		{[]string{"why"}, 3},                 // the host
		{[]string{"timeline"}, 3},            // the host
		{[]string{"dimm", "CPU0_C1"}, 3},     // replace now
		{[]string{"why", "CPU1_D1"}, 1},      // observe
		{[]string{"map", "CPU1_D1"}, 1},      // observe
		{[]string{"timeline", "CPU1_D1"}, 1}, // observe
		{[]string{"dimm", "CPU0_A1"}, 0},     // ok
	} {
		if code, _, errOut := run(append([]string{"open", ledger}, tt.args...)...); code != tt.code {
			t.Errorf("my open LEDGER %s: exit code %d, want %d (%s)", strings.Join(tt.args, " "), code, tt.code, errOut)
		}
	}
	hold := testledger.Path(t, "system-channel", 1, "")
	if code, out, _ := run("open", hold); code != 4 || !strings.Contains(out, "??") {
		t.Errorf("a host with a hold finding: exit code %d", code)
	}
	// Thresholds change findings: a single-bit fault that is ignored below
	// a higher repeat count stays "observe", but the row rule can be made
	// to need more errors than the DIMM has.
	if code, _, _ := run("open", ledger, "why", "CPU0_C1", "--set", "rule.row.min_errors=100000"); code != 3 {
		t.Errorf("R-UE-01 does not depend on that threshold: exit code %d", code)
	}
	code, _, errOut := run("open", ledger, "--set", "nope=1")
	if code != cli.ExitUsage || !strings.Contains(errOut, "nope") {
		t.Errorf("unknown threshold: exit code %d, stderr %q", code, errOut)
	}
}

func TestCommandLineMistakes(t *testing.T) {
	ledger := demo(t)
	for _, tt := range []struct {
		name string
		args []string
		code int
		want string
	}{
		{"no DIMM named", []string{"open", ledger, "dimm"}, cli.ExitUsage, "name one DIMM by its slot or serial number"},
		{"two DIMMs named", []string{"open", ledger, "dimm", "CPU0_C1", "CPU1_D1"}, cli.ExitUsage, "name one DIMM"},
		{"unknown DIMM", []string{"open", ledger, "dimm", "CPU7_X1"}, cli.ExitUsage, `no slot or DIMM "CPU7_X1" in host r12-n034. DIMMs with errors or a finding: CPU0_C1, CPU1_D1`},
		{"empty slot", []string{"open", ledger, "map", "CPU0_C2"}, cli.ExitUsage, "slot CPU0_C2 holds no DIMM"},
		{"bank without errors", []string{"open", ledger, "map", "CPU0_C1", "--bank", "1"}, cli.ExitUsage, "banks with errors: rank0 dev7 bg0 bank3"},
		{"bad bank", []string{"open", ledger, "map", "CPU0_C1", "--bank", "x/y"}, cli.ExitUsage, "--bank x/y: want a bank"},
		{"bad rows", []string{"open", ledger, "map", "CPU0_C1", "--rows", "9-3"}, cli.ExitUsage, "--rows 9-3: want a range of rows"},
		{"bad days", []string{"open", ledger, "timeline", "--days", "-3"}, cli.ExitUsage, "--days -3"},
		{"bad colour", []string{"open", ledger, "--color", "blue"}, cli.ExitUsage, "--color blue: want auto, always or never"},
		{"bad language", []string{"open", ledger, "--lang", "xx"}, cli.ExitUsage, `unknown language "xx"`},
		{"unknown flag", []string{"open", ledger, "why", "--frob"}, cli.ExitUsage, "flag provided but not defined"},
		{"extra argument", []string{"open", ledger, "why", "CPU0_C1", "x"}, cli.ExitUsage, `unexpected argument "x"`},
		{"unknown host", []string{"open", ledger, "--host", "h9"}, cli.ExitUsage, "no host named h9 in the ledger (hosts: r12-n034)"},
		{"unknown command after the file", []string{"open", ledger, "frob"}, cli.ExitUsage, `unknown command "frob"`},
		{"no file", []string{"open"}, cli.ExitUsage, "name the case to open"},
		{"a flag where the file belongs", []string{"open", "--json"}, cli.ExitUsage, "name the case to open"},
		{"missing file", []string{"open", filepath.Join(t.TempDir(), "none.db")}, cli.ExitFailure, "no such file"},
		{"not a ledger", []string{"open", filepath.Join(testutil.RepoRoot(t), "go.mod")}, cli.ExitFailure, "neither a MemorY ledger nor a case bundle"},
		{"a directory", []string{"open", t.TempDir()}, cli.ExitFailure, "is a directory"},
		{"no scan flags on a case", []string{"open", ledger, "--root", "/tmp"}, cli.ExitUsage, "flag provided but not defined: -root"},
		{"a word that is no command", []string{"CPU0_C1"}, cli.ExitUsage, `unknown command "CPU0_C1"`},
		{"a command after the flags", []string{"--root", "/tmp", "why"}, cli.ExitUsage, "the command comes before its flags: my why [flags]"},
		{"an argument of a scan", []string{"--root", "/tmp", "CPU0_C1"}, cli.ExitUsage, `unexpected argument "CPU0_C1"`},
		{"root that is no directory", []string{"--root", filepath.Join(testutil.RepoRoot(t), "go.mod")}, cli.ExitUsage, "not a directory"},
	} {
		code, out, errOut := run(tt.args...)
		if code != tt.code || !strings.Contains(errOut, tt.want) || out != "" {
			t.Errorf("%s: exit code %d, stdout %q, stderr %q; want %d and %q", tt.name, code, out, errOut, tt.code, tt.want)
		}
	}
	// An error is written in the language of the reader.
	_, _, errOut := run("open", ledger, "dimm", "--lang", "ko")
	if !strings.Contains(errOut, "DIMM 하나를 슬롯 이름이나 시리얼로 지정해야 함") || !strings.Contains(errOut, `"my help dimm"`) {
		t.Errorf("Korean usage error: %q", errOut)
	}
}

func TestMapOptions(t *testing.T) {
	ledger := demo(t)
	_, out, _ := run("open", ledger, "map", "CPU0_C1", "--rows", "0x1a2f-0x1a30", "--rank", "0", "--device", "7")
	if strings.Count(out, "\n 0x1a") != 2 || !strings.Contains(out, " 0x1a2f ") || strings.Contains(out, "0x1a2e") {
		t.Errorf("--rows 0x1a2f-0x1a30:\n%s", out)
	}
	for in, want := range map[string][2]int{"3": {-1, 3}, "0/3": {0, 3}, " 1 / 2 ": {1, 2}, "0x10": {-1, 16}} {
		bg, bank, err := parseBank(in)
		if err != nil || bank.V != want[1] || (want[0] >= 0) != bg.Valid || (bg.Valid && bg.V != want[0]) {
			t.Errorf("parseBank(%q) = %v, %v, %v", in, bg, bank, err)
		}
	}
	for _, bad := range []string{"", "a", "1/", "/1", "-1", "1/2/3"} {
		if _, _, err := parseBank(bad); err == nil {
			t.Errorf("parseBank(%q) accepted", bad)
		}
	}
	if from, to, err := parseRows("7"); err != nil || from.V != 7 || to.V != 7 {
		t.Errorf("parseRows(7) = %v, %v, %v", from, to, err)
	}
	for _, bad := range []string{"", "x", "5-", "-5", "5-2"} {
		if _, _, err := parseRows(bad); err == nil {
			t.Errorf("parseRows(%q) accepted", bad)
		}
	}
}

// TestScanOfAHostTree runs the commands the way they run on a host: they
// scan. The host here is a copy of a host's files.
func TestScanOfAHostTree(t *testing.T) {
	ds := testledger.Generate(t, "demo", 1, nil)
	root := filepath.Join(t.TempDir(), "root")
	if err := ds.WriteHostTree(root, synth.FlavorIntel); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := run("--root", root)
	if code != 3 || errOut != "" {
		t.Fatalf("exit code %d, stderr %q\n%s", code, errOut, out)
	}
	wantLines(t, out,
		"host r12-n034",
		"CE 1,284, UE 1  observation grade A",
		" CPU0   ░░   ░░   ██   ░░   ░░   ░░   ░░   ░░",
		"CPU0_C1  3B9C56  1,231   1  row",
		" sources  hostinfo 2 records, rasdaemon 1,286 records, klog",
		"my --root "+root+" dimm CPU0_C1",
	)
	if strings.Contains(out, "missing") {
		t.Errorf("every source was readable, yet something is missing:\n%s", out)
	}
	code, out, errOut = run("dimm", "CPU0_C1", "--root", root)
	if code != 3 || errOut != "" || !strings.Contains(out, "replace now") || !strings.Contains(out, "row fault") {
		t.Errorf("my dimm on a scan: exit code %d, stderr %q\n%s", code, errOut, out)
	}
	if code, out, _ = run("why", "--oneline", "--root", root); code != 3 || strings.Count(out, "\n") != 2 {
		t.Errorf("my why --oneline on a scan: exit code %d\n%s", code, out)
	}

	// --ledger keeps the scan, and the ledger can be opened later.
	kept := filepath.Join(t.TempDir(), "kept.db")
	if code, _, errOut = run("--root", root, "--ledger", kept); code != 3 {
		t.Fatalf("--ledger: exit code %d, stderr %q", code, errOut)
	}
	if code, out, _ = run("open", kept, "timeline", "CPU0_C1"); code != 3 || !strings.Contains(out, "peak") {
		t.Errorf("the kept ledger: exit code %d\n%s", code, out)
	}
	// A second scan into it adds nothing twice.
	if code, _, errOut = run("--root", root, "--ledger", kept); code != 3 {
		t.Fatalf("second scan: exit code %d, stderr %q", code, errOut)
	}
	s, err := store.Open(context.Background(), kept, store.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	totals, _ := s.Totals(context.Background(), store.EventFilter{})
	var n int64
	for _, x := range totals {
		n += x.CE + x.UE
	}
	scans, _ := s.Scans(context.Background())
	if n != 1285 || len(scans) != 2 {
		t.Errorf("after two scans: %d errors, %d scans", n, len(scans))
	}
}

func TestScanSaysWhatRootWouldAdd(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	ds := testledger.Generate(t, "demo", 1, nil)
	root := filepath.Join(t.TempDir(), "root")
	if err := ds.WriteHostTree(root, synth.FlavorIntel); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "sys", "firmware", "dmi", "tables", "DMI"), 0); err != nil {
		t.Fatal(err)
	}
	code, out, _ := run("--root", root)
	// Without SMBIOS the DIMMs have no serial numbers, and the scan says
	// why. The errors are still found and graded.
	if code != 3 {
		t.Errorf("exit code %d", code)
	}
	wantLines(t, out, " missing  smbios: not readable (permission denied; the SMBIOS tables are readable as root.")
}

func TestSeveralHosts(t *testing.T) {
	// One ledger, two hosts: a fleet in one file.
	ledger := filepath.Join(t.TempDir(), "two.db")
	s, err := store.Open(context.Background(), ledger, store.Create)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"demo", "single-bit"} {
		ds := testledger.Generate(t, name, 1, nil)
		if _, err := ingest.Run(context.Background(), s, []adapter.Adapter{ds.Adapter()}, ingest.Options{Kind: model.ScanSynthetic, Now: ds.Scenario.End}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := run("open", ledger)
	if code != 3 || errOut != "" {
		t.Fatalf("exit code %d, stderr %q", code, errOut)
	}
	wantLines(t, out, "2 hosts in this ledger. Name one with --host to see its figures.", "r12-n034", "synth-single-bit", "replace now", "observe")
	code, out, _ = run("open", ledger, "--host", "synth-single-bit")
	if code != 1 || !strings.Contains(out, "host synth-single-bit") {
		t.Errorf("--host: exit code %d\n%s", code, out)
	}
	code, _, errOut = run("open", ledger, "dimm", "CPU0_C1")
	if code != cli.ExitUsage || !strings.Contains(errOut, "the ledger holds several hosts: name one with --host (r12-n034, synth-single-bit)") {
		t.Errorf("a DIMM without a host: exit code %d, stderr %q", code, errOut)
	}
	if code, _, _ = run("open", ledger, "dimm", "CPU0_C1", "--host", "r12-n034"); code != 3 {
		t.Errorf("a DIMM of a named host: exit code %d", code)
	}
	code, out, _ = run("open", ledger, "--json")
	if code != 3 || !strings.Contains(out, `"view": "hosts"`) {
		t.Errorf("--json of several hosts: exit code %d\n%s", code, out)
	}
}

func TestOpenAnOlderLedger(t *testing.T) {
	// A ledger of the first schema version, as an old binary left it. It
	// is shown from a copy that is brought up to date; the file stays as
	// it is.
	ledger := filepath.Join(t.TempDir(), "old.db")
	schema, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "internal", "store", "migrations", "0001_ledger.sql"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", ledger)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{string(schema), "PRAGMA application_id = 0x4D594C47", "PRAGMA user_version = 1",
		"INSERT INTO host (name) VALUES ('old-host')"} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	before, _ := os.ReadFile(ledger)
	code, out, errOut := run("open", ledger)
	after, _ := os.ReadFile(ledger)
	if code != 0 || !strings.Contains(out, "host old-host") {
		t.Errorf("exit code %d, stderr %q\n%s", code, errOut, out)
	}
	if !bytes.Equal(before, after) {
		t.Error("opening a case changed its file")
	}
}

var flagRE = regexp.MustCompile(`--[a-z][a-z-]*`)

// TestHelp checks the help of every command: both languages, every flag
// the command takes, and the reading guides.
func TestHelp(t *testing.T) {
	topics := map[string]func(*cli.Env) *flag.FlagSet{}
	for _, cmd := range []string{"overview", "dimm", "why", "map", "timeline"} {
		topics[cmd] = nil
	}
	for _, tt := range []struct {
		args  []string
		topic string
		want  []string
	}{
		{[]string{"scan", "--help"}, "overview", []string{"Usage: my [scan] [flags]", "--all", "--ledger FILE", "Slot map.", "Ranking.", "Grades and exit codes."}},
		{[]string{"dimm", "--help"}, "dimm", []string{"Usage: my dimm <slot|serial> [flags]", "Topology bars.", "Address map.", "Timeline.", "my open case.mycase dimm"}},
		{[]string{"help", "why"}, "why", []string{"--oneline", "Finding. A grade is a finding"}},
		{[]string{"map", "?"}, "map", []string{"--bank N", "--rows A-B", "A horizontal line is a row fault"}},
		{[]string{"help", "timeline"}, "timeline", []string{"--days N", "B boot, U uncorrected error"}},
		{[]string{"gpu", "?"}, "gpu", []string{"Usage: my gpu [index|serial|uuid|pci-address] [--days N] [flags]", "--days N", "GPU memory. A GPU does not report where in its memory an error was", "my open case.mycase gpu"}},
		{[]string{"help", "verify"}, "verify", []string{"Usage: my verify --expect PLAN [flags]", "--expect PLAN", "The command injects nothing and changes nothing", "The exit code is 0 when every injection was found as planned and 1 when one was not", "Verification. A plan lists what a test injected"}},
		{[]string{"export", "--help"}, "export", []string{"Usage: my export fmfm [-o FILE] [--records] [flags]", "--records", "It holds no action descriptor (CPAD)", "The exit code is 0 when the log was written"}},
		{[]string{"open", "--help"}, "open", []string{"Usage: my open FILE", "The file is not changed"}},
		{[]string{"import", "--help"}, "import", []string{"Usage: my import <file|directory>... [-o FILE]", "--captured-at TIME", "An input that is not recognised stops the import"}},
		{[]string{"help", "bundle"}, "bundle", []string{"Usage: my bundle [slot|serial] -o FILE", "--anonymize", "never the contents of memory", "The exit code is 0"}},
		{[]string{"report", "--help"}, "report", []string{"Usage: my report [FILE] -o OUT.html [--audience customer|fa] [--figures DIR]", "--audience WHO", "--figures DIR", "nothing is loaded from elsewhere", "Printing it from a browser gives a PDF", "The exit code is 0"}},
	} {
		for _, lang := range []i18n.Lang{i18n.EN, i18n.KO} {
			args := append(append([]string{}, tt.args...), "--lang", string(lang))
			code, out, errOut := run(args...)
			if code != cli.ExitOK || errOut != "" {
				t.Errorf("%v: exit code %d, stderr %q", args, code, errOut)
				continue
			}
			// Sentences are wrapped: compare them without the line breaks.
			flat := strings.Join(strings.Fields(out), " ")
			if lang == i18n.EN {
				wantLines(t, flat, tt.want...)
			} else if !strings.Contains(out, "사용법:") || !strings.Contains(out, "종료 코드") || !strings.Contains(out, "옵션:") {
				t.Errorf("%v: not Korean:\n%s", args, out)
			}
			// Every flag of the command is in its help, and the help names
			// no flag the command does not take.
			o := newOptions(&cli.Env{Getenv: func(string) string { return "" }}, "my", sourceFor(tt.topic))
			registerFlags(o, tt.topic)
			known := map[string]bool{"--help": true, "--force": true}
			o.fs.VisitAll(func(f *flag.Flag) {
				if f.Name == "h" || f.Name == "help" {
					return
				}
				known["--"+f.Name] = true
				if !strings.Contains(out, "--"+f.Name) {
					t.Errorf("help of %s (%s) lacks --%s", tt.topic, lang, f.Name)
				}
			})
			for _, f := range flagRE.FindAllString(out, -1) {
				if !known[f] {
					t.Errorf("help of %s (%s) names %s, which the command does not take", tt.topic, lang, f)
				}
			}
			for _, line := range strings.Split(out, "\n") {
				if term.Width(line) > term.DefaultWidth {
					t.Errorf("help of %s (%s): line wider than %d columns: %q", tt.topic, lang, term.DefaultWidth, line)
				}
			}
		}
	}
	// The command list, and the topics that are not commands.
	_, out, _ := run("help")
	wantLines(t, out, "my [flags]", "scan ", "dimm ", "why ", "map ", "timeline ", "bundle ", "report ", "open ", "import ", "version ")
	// "my --help" is the command list; "my scan" is the command without a name.
	if _, same, _ := run("--help"); same != out {
		t.Errorf("my --help differs from my help:\n%s", same)
	}
	if strings.Contains(out, "synth") || strings.Contains(out, "analyze") || strings.Contains(out, "reading") {
		t.Errorf("the command list shows a hidden command:\n%s", out)
	}
	_, out, _ = run("help", "--lang", "ko")
	wantLines(t, out, "사용법:", "이 호스트를 스캔", "판정 근거")
	flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	_, out, _ = run("help", "reading")
	wantLines(t, flat(out), "Slot map.", "Ranking.", "Finding.", "Topology bars.", "Address map.", "Timeline.", "Grades and exit codes.", "Observation grades.")
	_, out, _ = run("help", "reading", "--ascii", "--lang", "ko")
	wantLines(t, flat(out), "슬롯 맵.", "## 즉시 교체", ":: 정상")
	_, out, _ = run("help", "rules")
	wantLines(t, out, "R-UE-01", "R-MAP-01", "R-HOST-01", "R-HOST-02", "hold replacement", "an uncorrected error on this DIMM")
	if n := strings.Count(out, "\n  R-"); n != 15 {
		t.Errorf("%d rules listed, want 15:\n%s", n, out)
	}
	_, out, _ = run("help", "thresholds")
	wantLines(t, out, "rule.row.min_errors", "trend.recent_days")
}

// sourceFor returns the source a help topic is about: "open" works on a
// case, the others on the host.
func sourceFor(topic string) *source {
	if topic == "open" || topic == "import" {
		return &source{}
	}
	return nil
}

// registerFlags adds the flags a command has beside the common ones, as
// the command itself does.
func registerFlags(o *options, topic string) {
	switch topic {
	case "overview":
		o.fs.Bool("all", false, "")
	case "why":
		o.fs.Bool("oneline", false, "")
		o.fs.Bool("all", false, "")
	case "map":
		o.fs.String("bank", "", "")
		o.fs.Int("rank", -1, "")
		o.fs.Int("device", -1, "")
		o.fs.String("rows", "", "")
	case "timeline", "gpu":
		o.fs.Int("days", 0, "")
	case "verify":
		o.fs.String("expect", "", "")
	case "export":
		o.fs.Bool("records", false, "")
		o.fs.Bool("all", false, "")
		o.fs.Bool("force", false, "")
	case "import":
		o.fs.String("ledger", "", "")
		o.fs.String("captured-at", "", "")
		o.fs.Bool("mtime", false, "")
		o.fs.String("tz", "", "")
		o.fs.String("nvidia-query", "", "")
		o.fs.Bool("anonymize", false, "")
		o.fs.String("key-file", "", "")
		o.fs.Bool("force", false, "")
		o.fs.Bool("all", false, "")
	case "bundle":
		o.fs.Bool("anonymize", false, "")
		o.fs.String("key-file", "", "")
		o.fs.Bool("force", false, "")
	case "report":
		o.fs.String("audience", "", "")
		o.fs.Bool("all", false, "")
		o.fs.String("figures", "", "")
		o.fs.Bool("force", false, "")
	}
}
