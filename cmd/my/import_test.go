package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/bundle"
	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/testutil"
)

// TestImportHostTree builds a case from a copy of a host's files, the way
// someone who was sent the files does.
func TestImportHostTree(t *testing.T) {
	root := hostTree(t)
	file := filepath.Join(t.TempDir(), "case.mycase")
	code, out, errOut := runEnv(fixed, "import", root, "-o", file)
	if code != cli.ExitOK || errOut != "" {
		t.Fatalf("exit code %d, stderr %q", code, errOut)
	}
	wantLines(t, out,
		root+": copy of a host's files",
		"  hostinfo: ok, 2 records",
		"  rasdaemon: ok, 1,286 records, 1,285 error records (grade A), 1 action",
		"  klog: ok, 807 records, 797 error records (grade A), 7 actions",
		"  smbios: ok, 32 records",
		// The kernel log and the database report the same errors.
		"host r12-n034: 2,082 error records and 8 actions added, 797 reported by more than one source and counted once",
		"case bundle written: "+file,
		"  host r12-n034, finding: replace now",
		"look at it: my open "+file,
	)
	// The case is the one a scan of the same files gives.
	_, scanned, _ := runEnv(fixed, "dimm", "CPU0_C1", "--root", root)
	_, opened, _ := runEnv(fixed, "open", file, "dimm", "CPU0_C1")
	if i := strings.Index(opened, "\n"); scanned != opened[i+1:] {
		t.Errorf("the imported case differs from a scan of the same files:\n--- scan\n%s\n--- import\n%s", scanned, opened)
	}
	m, err := bundle.ReadManifest(file)
	if err != nil || len(m.Hosts) != 1 || m.Hosts[0].CE != 1284 || m.Hosts[0].UE != 1 {
		t.Errorf("manifest hosts = %+v, %v", m.Hosts, err)
	}

	// Without -o the import is looked at right away, and the exit code is
	// the grade of the host.
	code, out, errOut = runEnv(fixed, "import", root)
	if code != 3 || errOut != "" {
		t.Fatalf("exit code %d, stderr %q", code, errOut)
	}
	wantLines(t, out, root+": copy of a host's files", "host finding: replace now", " CPU0   ░░   ░░   ██", "my import ... -o case.mycase")
	code, out, _ = runEnv(fixed, "import", root, "--json")
	var e struct {
		View string `json:"view"`
		Data struct {
			Inputs []struct {
				Path, What string
				Sources    []struct{ Kind, Status string }
			}
			Overview struct {
				Ranking []struct{ Slot string }
			}
		}
	}
	if err := json.Unmarshal([]byte(out), &e); err != nil || code != 3 || e.View != "overview" || len(e.Data.Inputs) != 1 ||
		e.Data.Inputs[0].What != "copy of a host's files" || len(e.Data.Inputs[0].Sources) != 5 || e.Data.Overview.Ranking[0].Slot != "CPU0_C1" {
		t.Errorf("--json: exit code %d, %v\n%s", code, err, out)
	}
}

func TestImportLooseFiles(t *testing.T) {
	syslog := testutil.Testdata(t, "klog", "intel-skx.messages")
	dmi := testutil.Testdata(t, "smbios", "dmidecode-t17-ddr4.txt")
	code, out, errOut := runEnv(fixed, "import", syslog, dmi, "--host", "r07-n011")
	if code != 2 || errOut != "" {
		t.Fatalf("exit code %d, stderr %q\n%s", code, errOut, out)
	}
	wantLines(t, out,
		syslog+": kernel log",
		"  klog: ok, 7 records, 3 error records (grade A), 1 action",
		dmi+": dmidecode output",
		// A source that was read in part says what it left out.
		"  smbios: partly read, 6 records, 2 not usable",
		`      skipped handle 0x1105: Size "a lot" is unreadable`,
		"host r07-n011: 3 error records and 1 action added",
		"host r07-n011  ",
		"host finding: consider replacement",
		" missing  smbios: partly read",
	)

	// Times without a zone are taken as UTC unless --tz says otherwise.
	_, utc, _ := runEnv(fixed, "import", syslog, "--json")
	_, seoul, _ := runEnv(fixed, "import", syslog, "--json", "--tz", "Asia/Seoul")
	if utc == seoul || !strings.Contains(utc, `"to": "2026-02-27T`) || !strings.Contains(seoul, `"to": "2026-02-26T`) {
		t.Errorf("--tz Asia/Seoul does not move the times nine hours back")
	}

	// --ledger keeps the import; the ledger opens like any case.
	kept := filepath.Join(t.TempDir(), "kept.db")
	if code, _, errOut := runEnv(fixed, "import", syslog, "--ledger", kept); code != 2 {
		t.Fatalf("--ledger: exit code %d, stderr %q", code, errOut)
	}
	if code, out, _ := run("open", kept, "why", "--oneline"); code != 2 || !strings.Contains(out, "consider replacement") {
		t.Errorf("the kept ledger: exit code %d\n%s", code, out)
	}
}

func TestImportMistakes(t *testing.T) {
	root := hostTree(t)
	dir := t.TempDir()
	taken := filepath.Join(dir, "taken.mycase")
	os.WriteFile(taken, []byte("mine"), 0o644)
	gomod := filepath.Join(testutil.RepoRoot(t), "go.mod")
	for _, tt := range []struct {
		name string
		args []string
		code int
		want string
	}{
		{"no files", []string{"import"}, cli.ExitUsage, "name the files to import"},
		{"a file of no known kind", []string{"import", gomod}, cli.ExitFailure, "not an input this tool reads: text of no known kind"},
		// One input that is not understood stops the import of the others.
		{"one of two is unknown", []string{"import", root, gomod, "-o", filepath.Join(dir, "x.mycase")}, cli.ExitFailure, "not an input this tool reads"},
		{"a missing file", []string{"import", filepath.Join(dir, "none.log")}, cli.ExitFailure, "no such file"},
		{"a ledger", []string{"import", demo(t)}, cli.ExitFailure, `show it with "my open"`},
		{"anonymizing nothing", []string{"import", root, "--anonymize"}, cli.ExitUsage, "--anonymize goes with -o"},
		{"a key without anonymizing", []string{"import", root, "--key-file", gomod}, cli.ExitUsage, "--key-file goes with --anonymize"},
		{"a bad time", []string{"import", root, "--captured-at", "yesterday"}, cli.ExitUsage, "--captured-at yesterday: want a date"},
		{"a bad time zone", []string{"import", root, "--tz", "Mars/Olympus"}, cli.ExitUsage, "--tz Mars/Olympus: want a time zone name"},
		{"an output that exists", []string{"import", root, "-o", taken}, cli.ExitUsage, "exists; --force replaces it"},
		{"a scan flag", []string{"import", root, "--root", root}, cli.ExitUsage, "unknown flag --root"},
	} {
		code, out, errOut := runEnv(fixed, tt.args...)
		if code != tt.code || !strings.Contains(errOut, tt.want) || out != "" {
			t.Errorf("%s: exit code %d, stdout %q, stderr %q; want %d and %q", tt.name, code, out, errOut, tt.code, tt.want)
		}
	}
	if b, _ := os.ReadFile(taken); string(b) != "mine" {
		t.Error("a file that was there was replaced")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("a failed import left %d files behind", len(entries)-1)
	}
	// An import can be anonymized on the way out.
	out := filepath.Join(dir, "anon.mycase")
	code, text, errOut := runEnv(fixed, "import", root, "-o", out, "--anonymize")
	if code != cli.ExitOK || !strings.Contains(text, "r12-n034 -> host-") {
		t.Fatalf("exit code %d, stderr %q\n%s", code, errOut, text)
	}
	if m, err := bundle.ReadManifest(out); err != nil || !m.Anonymized || strings.Contains(m.Hosts[0].Name, "r12") {
		t.Errorf("manifest = %+v, %v", m, err)
	}
}

// An import records the path of each file as it was given. Files below a
// home directory carry the name of an account in that path, and an
// anonymized bundle must not.
func TestImportAnonymizesHomeDirectories(t *testing.T) {
	log, err := os.ReadFile(filepath.Join(hostTree(t), "var", "log", "dmesg"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	home := filepath.Join(dir, "home", "alice", "cases")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(home, "dmesg")
	if err := os.WriteFile(file, log, 0o644); err != nil {
		t.Fatal(err)
	}

	plain := filepath.Join(dir, "plain.mycase")
	if code, _, errOut := runEnv(fixed, "import", file, "-o", plain); code != cli.ExitOK {
		t.Fatalf("exit code %d, stderr %q", code, errOut)
	}
	if src, err := bundle.ReadSources(plain); err != nil || !strings.Contains(src.Scans[0].Sources[0].Origin, "/home/alice/") {
		t.Fatalf("the test is vacuous: the plain bundle does not record the path: %+v, %v", src, err)
	}

	out := filepath.Join(dir, "anon.mycase")
	code, text, errOut := runEnv(fixed, "import", file, "-o", out, "--anonymize")
	if code != cli.ExitOK || !strings.Contains(text, "replaced in the paths of the files that were read") {
		t.Fatalf("exit code %d, stderr %q\n%s", code, errOut, text)
	}
	m, err := bundle.ReadManifest(out)
	if err != nil || m.Anonymization == nil || m.Anonymization.Users < 1 {
		t.Fatalf("manifest = %+v, %v", m, err)
	}
	src, err := bundle.ReadSources(out)
	if err != nil {
		t.Fatal(err)
	}
	if origin := src.Scans[0].Sources[0].Origin; strings.Contains(origin, "alice") || !strings.Contains(origin, "/home/user-") || !strings.HasSuffix(origin, "/cases/dmesg") {
		t.Errorf("origin = %q", origin)
	}
	// The findings still point at their records, under the new path.
	code, why, errOut := runEnv(fixed, "open", out, "why")
	if errOut != "" || strings.Contains(why, "alice") || !strings.Contains(why, "/cases/dmesg#L") {
		t.Errorf("exit code %d, stderr %q\n%s", code, errOut, why)
	}
}

// TestImportDatesAKernelLogByTheDatabase imports the two files a host most
// often yields: the rasdaemon database and a kernel log saved with dmesg.
// The log has no dates. The errors both report must be counted once, and
// the log takes its dates from the database.
func TestImportDatesAKernelLogByTheDatabase(t *testing.T) {
	root := hostTree(t)
	db := filepath.Join(root, "var", "lib", "rasdaemon", "ras-mc_event.db")
	dmesg := filepath.Join(root, "var", "log", "dmesg")
	for _, tt := range []struct {
		name  string
		flags []string
	}{
		{"no dates at all", nil},
		{"dated by the time of the file", []string{"--mtime"}},
		{"dated by a capture time that is a week off", []string{"--captured-at", "2026-11-20T00:00:00Z"}},
	} {
		args := append([]string{"import", db, dmesg, "--host", "r12-n034"}, tt.flags...)
		code, out, errOut := runEnv(fixed, args...)
		if code != 3 || errOut != "" {
			t.Errorf("%s: exit code %d, stderr %q", tt.name, code, errOut)
			continue
		}
		flat := strings.Join(strings.Fields(out), " ")
		for _, want := range []string{
			"time-align: ok, 797 records",
			"the times of 797 records were set by 797 errors another source reports with a date: their boot started 2026-10-28 01:",
			"797 reported by more than one source and counted once",
			"CE 1,284, UE 1 observation grade A",
		} {
			if !strings.Contains(flat, want) {
				t.Errorf("%s: output lacks %q:\n%s", tt.name, want, out)
			}
		}
	}
	// The kernel log alone stays without dates, and says so.
	code, out, _ := runEnv(fixed, "import", dmesg)
	flat := strings.Join(strings.Fields(out), " ")
	if code != 3 || strings.Contains(out, "time-align") || !strings.Contains(flat, "804 records have only seconds since boot") || !strings.Contains(out, "CE 796, UE 1") {
		t.Errorf("the kernel log alone: exit code %d\n%s", code, out)
	}
}
