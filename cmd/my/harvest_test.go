package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/bert"
	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/decode/cper"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/preflight"
	pt "github.com/agenticode/my/internal/preflight/preflighttest"
	"github.com/agenticode/my/internal/scan"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/testutil"
	"github.com/agenticode/my/internal/view"
)

// harvestTree is a host after a crash: the files of a running boot, a
// pstore dump of the boot before (lines of the real capture
// 7.3.0-mce-fatal in the framing of fs/pstore) and a boot error region
// made from the ACPI layouts.
func harvestTree(t *testing.T) string {
	t.Helper()
	b := testutil.ReadTestdata(t, "real", "7.3.0-mce-fatal", "console.log")
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	var dump strings.Builder
	dump.WriteString("Panic#1 Part1\n")
	for _, l := range lines[len(lines)-12:] {
		dump.WriteString("<0>" + l + "\n")
	}
	region := cper.StatusBlockBytes(0, cper.SevFatal, cper.Entry{SectionType: cper.SectionPlatformMemory, Severity: cper.SevFatal,
		Data: cper.MemSection(cper.MemError{PhysicalAddress: model.Some(uint64(0x7700000)), Row: model.Some(0x1a2f), Column: model.Some(0x238), Bank: model.Some(3)})})
	f := pt.Kept().With(pt.Files{
		"proc/sys/kernel/random/boot_id": "0123456789abcdef0123456789abcdef\n",
		"sys/fs/pstore":                  "",
		"var/lib/systemd/pstore/dmesg-erst-7708347470224293889":   dump.String(),
		"sys/firmware/acpi/tables/data/BERT":                      string(region),
		"proc/uptime":                                             "300.00 1000.00\n",
		"sys/devices/system/machinecheck/machinecheck0/print_all": "",
	})
	return pt.Tree(t, f)
}

// useHarvest points "my harvest" at a tree as if it were the running host.
func useHarvest(t *testing.T, root string, change func(*harvestEnv)) {
	t.Helper()
	old, oldOS := harvestProbe, hostOS
	harvestProbe = func(he *harvestEnv) {
		he.scan.Root, he.scan.Journalctl, he.scan.Mcelog, he.scan.NvidiaSMI = root, "-", "-", "-"
		// The boot of the tree started at its btime and has run 300 s.
		he.scan.Now = time.Unix(1794805260+300, 0).UTC()
		he.scan.Preflight = func(o *preflight.Options) {
			o.Processes = func() ([]string, error) { return nil, nil }
			o.FreeBytes = func(string) (uint64, error) { return 1 << 30, nil }
		}
		he.freeBytes = func(string) (uint64, error) { return 1 << 30, nil }
		he.euid = 0
		if change != nil {
			change(he)
		}
	}
	hostOS = "linux"
	t.Cleanup(func() { harvestProbe, hostOS = old, oldOS })
}

func openLedger(t *testing.T, path string) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), path, store.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func sourceKinds(t *testing.T, st *store.Store) []string {
	t.Helper()
	srcs, err := st.Sources(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, s := range srcs {
		out = append(out, s.Kind)
	}
	return out
}

// TestHarvestPhases: what the boot before left is read first and is on
// disk before anything else is read; the rest follows, and the run says
// so in one line per phase.
func TestHarvestPhases(t *testing.T) {
	root := harvestTree(t)
	ledger := filepath.Join(t.TempDir(), "ledger.db")
	var atA []string
	var onDisk int
	useHarvest(t, root, func(he *harvestEnv) {
		he.afterPhaseA = func() {
			// The database file alone, without its write-ahead log, holds
			// phase A: the checkpoint wrote it there.
			copyOnly := filepath.Join(t.TempDir(), "copy.db")
			b, err := os.ReadFile(ledger)
			if err != nil {
				t.Fatal(err)
			}
			os.WriteFile(copyOnly, b, 0o600)
			st, err := store.Open(context.Background(), copyOnly, store.ReadOnly)
			if err != nil {
				t.Fatalf("the database file alone: %v", err)
			}
			defer st.Close()
			atA = sourceKinds(t, st)
			evs, _ := st.EventList(context.Background(), store.EventFilter{WithDuplicates: true})
			onDisk = len(evs)
		}
	})
	code, out, errOut := run("harvest", "--ledger", ledger)
	if code != cli.ExitOK || out != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	lines := strings.Split(strings.TrimSpace(errOut), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], "phase A: 3 sources") || !strings.Contains(lines[1], "phase B:") || !strings.Contains(lines[2], "on record in") {
		t.Errorf("stderr:\n%s", errOut)
	}
	if strings.Join(atA, " ") != "hostinfo pstore bert" && strings.Join(atA, " ") != "hostinfo klog pstore bert" {
		t.Errorf("sources on disk after phase A: %q", atA)
	}
	if onDisk < 2 {
		t.Errorf("%d records on disk after phase A", onDisk)
	}
	st := openLedger(t, ledger)
	kinds := sourceKinds(t, st)
	if len(kinds) < 6 || kinds[0] != "hostinfo" {
		t.Errorf("sources %q", kinds)
	}
	scans, _ := st.Scans(context.Background())
	if len(scans) != 1 || scans[0].FinishedAt.IsZero() || scans[0].Note != "harvest" {
		t.Errorf("scans %+v", scans)
	}
	if checks, _ := st.CaptureChecks(context.Background(), scans[0].Host); len(checks) != len(preflight.Names) {
		t.Errorf("%d capture checks", len(checks))
	}
	if last, _ := st.Meta(context.Background(), "harvest.last_run"); last == "" {
		t.Error("no harvest.last_run")
	}
}

// TestHarvestNothingNew: a second run over the same host adds no row.
func TestHarvestNothingNew(t *testing.T) {
	root := harvestTree(t)
	ledger := filepath.Join(t.TempDir(), "ledger.db")
	useHarvest(t, root, nil)
	if code, _, errOut := run("harvest", "--ledger", ledger); code != 0 {
		t.Fatal(errOut)
	}
	count := func() string {
		st, err := store.Open(context.Background(), ledger, store.ReadOnly)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		c, _ := st.RecordCounts(context.Background())
		scans, _ := st.Scans(context.Background())
		srcs, _ := st.Sources(context.Background(), 0)
		var samples int
		st.Update(context.Background(), func(w *store.Writer) error { return nil })
		return fmt.Sprintf("%v scans %d sources %d samples %d", c, len(scans), len(srcs), samples)
	}
	first := count()
	code, _, errOut := run("harvest", "--ledger", ledger)
	if code != 0 || !strings.Contains(errOut, "nothing new") {
		t.Fatalf("exit %d, %s", code, errOut)
	}
	if again := count(); again != first {
		t.Errorf("a run that found nothing new changed the ledger:\n%s\n%s", first, again)
	}
}

// TestHarvestLock: a second run while one holds the ledger ends at once
// and changes nothing.
func TestHarvestLock(t *testing.T) {
	root := harvestTree(t)
	ledger := filepath.Join(t.TempDir(), "ledger.db")
	useHarvest(t, root, nil)
	unlock, held, err := lockFile(ledger)
	if err != nil || held {
		t.Fatal(err, held)
	}
	defer unlock()
	code, _, errOut := run("harvest", "--ledger", ledger)
	if code != cli.ExitOK || !strings.Contains(errOut, "another harvest") {
		t.Errorf("exit %d, %s", code, errOut)
	}
	if _, err := os.Stat(ledger); err == nil {
		t.Error("the ledger was written")
	}
}

// TestHarvestCannotWrite: a ledger that cannot be written ends the run with
// 70 and stays as it was: a read-only file, a directory closed to writing,
// and a write that fails during phase B.
func TestHarvestCannotWrite(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes read-only files")
	}
	root := harvestTree(t)
	dir := t.TempDir()
	ledger := filepath.Join(dir, "ledger.db")
	useHarvest(t, root, nil)
	if code, _, errOut := run("harvest", "--ledger", ledger); code != 0 {
		t.Fatal(errOut)
	}
	before, _ := os.ReadFile(ledger)

	os.Chmod(ledger, 0o444)
	code, _, errOut := run("harvest", "--ledger", ledger)
	if code != cli.ExitFailure || !strings.Contains(errOut, "cannot be written") {
		t.Errorf("read-only ledger: exit %d, %s", code, errOut)
	}
	os.Chmod(ledger, 0o644)
	if after, _ := os.ReadFile(ledger); string(after) != string(before) {
		t.Error("the read-only ledger changed")
	}

	os.Chmod(dir, 0o555)
	code, _, errOut = run("harvest", "--ledger", ledger)
	os.Chmod(dir, 0o755)
	if code != cli.ExitFailure || !strings.Contains(errOut, "cannot be written") {
		t.Errorf("read-only directory: exit %d, %s", code, errOut)
	}
	if after, _ := os.ReadFile(ledger); string(after) != string(before) {
		t.Error("the ledger in a read-only directory changed")
	}

	// A write that fails in phase B: its source is rolled back.
	useHarvest(t, root, func(he *harvestEnv) { he.extra = []adapter.Adapter{failing{}} })
	code, _, errOut = run("harvest", "--ledger", ledger)
	if code != cli.ExitFailure || !strings.Contains(errOut, "no space left") {
		t.Errorf("failing write: exit %d, %s", code, errOut)
	}
	st := openLedger(t, ledger)
	evs, _ := st.EventList(context.Background(), store.EventFilter{WithDuplicates: true})
	for _, e := range evs {
		if e.Source == "failing" {
			t.Errorf("a record of the failed source is in the ledger: %+v", e)
		}
	}
}

// failing is a source whose record the ledger cannot take.
type failing struct{}

func (failing) Kind() string   { return "failing" }
func (failing) Origin() string { return "failing" }
func (failing) Collect(ctx context.Context, sink adapter.Sink) adapter.Report {
	err := sink.Event(&model.Event{ErrorDT: time.Now(), ErrorSeverity: model.SevCE, Source: "failing", RawRef: "x", Fingerprint: "failing-1"})
	if err == nil {
		err = fmt.Errorf("write ledger: no space left on device")
	}
	return adapter.Report{Kind: "failing", Status: model.SourceError, SinkErr: err}
}

// TestHarvestLowSpace: below 32 MiB only phase A is read.
func TestHarvestLowSpace(t *testing.T) {
	root := harvestTree(t)
	ledger := filepath.Join(t.TempDir(), "ledger.db")
	useHarvest(t, root, func(he *harvestEnv) { he.freeBytes = func(string) (uint64, error) { return 20 << 20, nil } })
	code, _, errOut := run("harvest", "--ledger", ledger)
	if code != 0 || !strings.Contains(errOut, "20 MiB free") || strings.Contains(errOut, "phase B") {
		t.Fatalf("exit %d, %s", code, errOut)
	}
	if kinds := strings.Join(sourceKinds(t, openLedger(t, ledger)), " "); strings.Contains(kinds, "smbios") || !strings.Contains(kinds, "pstore") {
		t.Errorf("sources %q", kinds)
	}
}

// TestHarvestDeadline: sources not read by the deadline are named, the
// passes run, and the exit code is 0.
func TestHarvestDeadline(t *testing.T) {
	root := harvestTree(t)
	ledger := filepath.Join(t.TempDir(), "ledger.db")
	useHarvest(t, root, nil)
	code, _, errOut := run("harvest", "--ledger", ledger, "--deadline", "1ns")
	if code != 0 || !strings.Contains(errOut, "not read: the run reached its deadline of 1ns") {
		t.Fatalf("exit %d, %s", code, errOut)
	}
	// Nothing was read: the run leaves no scan behind, and says when it ran.
	st := openLedger(t, ledger)
	if scans, _ := st.Scans(context.Background()); len(scans) != 0 {
		t.Errorf("scans %+v", scans)
	}
	if last, _ := st.Meta(context.Background(), "harvest.last_run"); last == "" {
		t.Error("no harvest.last_run")
	}
	if code, _, _ := run("harvest", "--ledger", ledger, "--deadline", "0s"); code != cli.ExitUsage {
		t.Errorf("--deadline 0s: exit %d", code)
	}
}

// TestHarvestKilledAfterPhaseA: a run that dies after phase A leaves phase
// A in the ledger and a scan without a finish; status and my open say that
// totals may count an error twice.
func TestHarvestKilledAfterPhaseA(t *testing.T) {
	if os.Getenv("MY_TEST_HARVEST_DIE") != "" {
		return // the helper process: see dieAfterPhaseA
	}
	root := harvestTree(t)
	ledger := filepath.Join(t.TempDir(), "ledger.db")
	cmd := exec.Command(os.Args[0], "-test.run", "^TestHarvestHelperProcess$")
	cmd.Env = append(os.Environ(), "MY_TEST_HARVEST_DIE=1", "MY_TEST_HARVEST_ROOT="+root, "MY_TEST_HARVEST_LEDGER="+ledger)
	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 3 {
		t.Fatalf("helper: %v\n%s", err, out)
	}
	// Only the database file: the write-ahead log of a process that died
	// is what a power loss would take.
	os.Remove(ledger + "-wal")
	os.Remove(ledger + "-shm")
	st := openLedger(t, ledger)
	kinds := strings.Join(sourceKinds(t, st), " ")
	if !strings.Contains(kinds, "pstore") || !strings.Contains(kinds, "bert") || strings.Contains(kinds, "smbios") {
		t.Errorf("sources after the death: %q", kinds)
	}
	scans, _ := st.Scans(context.Background())
	if len(scans) != 1 || !scans[0].FinishedAt.IsZero() {
		t.Errorf("scans %+v", scans)
	}
	st.Close()
	_, out2, _ := run("harvest", "status", "--ledger", ledger)
	if !strings.Contains(out2, "did not finish") {
		t.Errorf("status:\n%s", out2)
	}
	_, out3, _ := run("open", ledger)
	if !strings.Contains(strings.Join(strings.Fields(out3), " "), "did not finish: the ledger passes did not run after it") {
		t.Errorf("my open:\n%s", out3)
	}
}

// TestHarvestHelperProcess runs a harvest that dies once phase A is on
// disk, as a reset would end it.
func TestHarvestHelperProcess(t *testing.T) {
	if os.Getenv("MY_TEST_HARVEST_DIE") == "" {
		t.Skip("helper process of TestHarvestKilledAfterPhaseA")
	}
	useHarvest(t, os.Getenv("MY_TEST_HARVEST_ROOT"), func(he *harvestEnv) { he.afterPhaseA = func() { os.Exit(3) } })
	run("harvest", "--ledger", os.Getenv("MY_TEST_HARVEST_LEDGER"))
	os.Exit(4)
}

// systemctlStandIn writes a program that logs its arguments and returns
// the path of the log.
func systemctlStandIn(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	prog := filepath.Join(dir, "systemctl")
	os.WriteFile(prog, []byte("#!/bin/sh\necho \"$*\" >> "+log+"\n"), 0o755)
	old := harvestSystemctl
	harvestSystemctl = prog
	t.Cleanup(func() { harvestSystemctl = old })
	return log
}

func useUnitDir(t *testing.T) (unitDir string) {
	t.Helper()
	unitDir, runDir := t.TempDir(), t.TempDir()
	oldUnit, oldRun, oldCheck, oldProbe := harvestUnitDir, harvestRunDir, binaryCheck, installProbe
	harvestUnitDir, harvestRunDir = unitDir, runDir
	binaryCheck = func(string) string { return "" }
	installProbe = func(euid *int, binary *string) { *euid, *binary = 0, "/usr/local/bin/my" }
	t.Cleanup(func() { harvestUnitDir, harvestRunDir, binaryCheck, installProbe = oldUnit, oldRun, oldCheck, oldProbe })
	return unitDir
}

func calls(t *testing.T, log string) string {
	b, _ := os.ReadFile(log)
	return string(b)
}

// TestHarvestInstall: exactly the unit files and the systemctl commands,
// and uninstall takes back exactly those.
func TestHarvestInstall(t *testing.T) {
	unitDir := useUnitDir(t)
	log := systemctlStandIn(t)

	code, out, errOut := run("harvest", "install", "--timer", "--dry-run")
	if code != 0 || calls(t, log) != "" || !strings.Contains(out, "--dry-run: nothing was done") {
		t.Fatalf("dry run: exit %d, calls %q, %s %s", code, calls(t, log), out, errOut)
	}
	if entries, _ := os.ReadDir(unitDir); len(entries) != 0 {
		t.Fatalf("a dry run wrote %d files", len(entries))
	}

	code, out, errOut = run("harvest", "install", "--timer")
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	want := unitFiles("/usr/local/bin/my", "", true)
	entries, _ := os.ReadDir(unitDir)
	if len(entries) != 2 {
		t.Errorf("%d files in the unit directory", len(entries))
	}
	for name, text := range want {
		b, err := os.ReadFile(filepath.Join(unitDir, name))
		if err != nil || string(b) != text {
			t.Errorf("%s: %v", name, err)
		}
	}
	if got := calls(t, log); got != "daemon-reload\nenable my-harvest.service my-harvest.timer\n" {
		t.Errorf("systemctl calls %q", got)
	}
	for _, w := range []string{"writes " + filepath.Join(unitDir, "my-harvest.service"), "runs " + harvestSystemctl + " daemon-reload", "installed"} {
		if !strings.Contains(out, w) {
			t.Errorf("install output lacks %q:\n%s", w, out)
		}
	}
	// Enabled, as systemctl enable would make it.
	os.MkdirAll(filepath.Join(unitDir, "multi-user.target.wants"), 0o755)
	os.Symlink("../my-harvest.service", filepath.Join(unitDir, "multi-user.target.wants", "my-harvest.service"))
	_, out, _ = run("harvest", "status", "--ledger", filepath.Join(t.TempDir(), "none.db"))
	if flat := strings.Join(strings.Fields(out), " "); !strings.Contains(flat, "my-harvest.service installed and enabled") || !strings.Contains(flat, "my-harvest.timer installed, not enabled") || !strings.Contains(flat, "is not there yet") {
		t.Errorf("status:\n%s", out)
	}

	os.Remove(log)
	code, out, errOut = run("harvest", "uninstall")
	if code != 0 {
		t.Fatalf("uninstall: exit %d %s %s", code, out, errOut)
	}
	if got := calls(t, log); got != "disable --now my-harvest.timer my-harvest.service\ndaemon-reload\n" {
		t.Errorf("systemctl calls %q", got)
	}
	for name := range want {
		if _, err := os.Stat(filepath.Join(unitDir, name)); err == nil {
			t.Errorf("%s is still there", name)
		}
	}
}

// TestHarvestInstallRefuses: a unit file of that name that install did not
// write is left alone; install needs root and systemd; uninstall leaves a
// foreign file too.
func TestHarvestInstallRefuses(t *testing.T) {
	unitDir := useUnitDir(t)
	log := systemctlStandIn(t)
	foreignUnit := "[Service]\nExecStart=/opt/site/harvest\n"
	os.WriteFile(filepath.Join(unitDir, "my-harvest.service"), []byte(foreignUnit), 0o644)
	code, _, errOut := run("harvest", "install")
	if code != cli.ExitFailure || !strings.Contains(errOut, "was not written by my harvest install") || calls(t, log) != "" {
		t.Errorf("foreign unit: exit %d, %s, calls %q", code, errOut, calls(t, log))
	}
	code, _, errOut = run("harvest", "uninstall")
	if code != 0 || !strings.Contains(errOut, "left alone") || calls(t, log) != "" {
		t.Errorf("uninstall with a foreign unit: exit %d, %s, calls %q", code, errOut, calls(t, log))
	}
	if b, _ := os.ReadFile(filepath.Join(unitDir, "my-harvest.service")); string(b) != foreignUnit {
		t.Error("the foreign unit changed")
	}
	os.Remove(filepath.Join(unitDir, "my-harvest.service"))

	installProbe = func(euid *int, binary *string) { *euid, *binary = 1000, "/usr/local/bin/my" }
	if code, _, errOut := run("harvest", "install"); code != cli.ExitFailure || !strings.Contains(errOut, "as root") {
		t.Errorf("not root: exit %d, %s", code, errOut)
	}
	installProbe = func(euid *int, binary *string) { *euid, *binary = 0, "/usr/local/bin/my" }
	os.RemoveAll(harvestRunDir)
	if code, _, errOut := run("harvest", "install"); code != cli.ExitFailure || !strings.Contains(errOut, "not the init system") {
		t.Errorf("no systemd: exit %d, %s", code, errOut)
	}
}

// TestInstallCheck: the binary a unit runs as root at every boot must be
// one only root can replace.
func TestInstallCheck(t *testing.T) {
	for _, p := range []string{"/home/alice/bin/my", "/root/my", "/tmp/my", "/run/user/1000/my", "/var/tmp/my"} {
		if why := installCheck(p); !strings.HasPrefix(why, "below") {
			t.Errorf("%s: %q", p, why)
		}
	}
	dir := t.TempDir()
	open := filepath.Join(dir, "open")
	os.WriteFile(open, []byte("x"), 0o777)
	os.Chmod(open, 0o777)
	if os.Geteuid() != 0 {
		if why := installCheck(open); why != "not owned by root" && !strings.HasPrefix(why, "below") {
			t.Errorf("a file of this user: %q", why)
		}
	} else if why := installCheck(open); why != "writable by others than its owner" && !strings.HasPrefix(why, "below") {
		t.Errorf("a file writable by all: %q", why)
	}
}

// TestHarvestUnitsInContrib: the unit files in contrib are those install
// writes for /usr/local/bin/my.
func TestHarvestUnitsInContrib(t *testing.T) {
	for name, text := range unitFiles("/usr/local/bin/my", "", true) {
		b := testutil.ReadTestdata(t, "..", "contrib", "systemd", name)
		if string(b) != text {
			t.Errorf("contrib/systemd/%s differs from what my harvest install writes", name)
		}
	}
	svc := unitFiles("/usr/local/bin/my", "/srv/my/ledger.db", false)["my-harvest.service"]
	if !strings.Contains(svc, "ExecStart=/usr/local/bin/my harvest --ledger /srv/my/ledger.db\n") {
		t.Errorf("ExecStart with another ledger:\n%s", svc)
	}
	for _, line := range []string{"Type=oneshot", "PrivateNetwork=yes", "StateDirectory=my", "CapabilityBoundingSet=CAP_DAC_READ_SEARCH CAP_SYSLOG", "After=local-fs.target systemd-journal-flush.service systemd-pstore.service rasdaemon.service"} {
		if !strings.Contains(svc, "\n"+line+"\n") {
			t.Errorf("the service lacks %q", line)
		}
	}
	if strings.Contains(svc, "Before=") || strings.Contains(svc, "Requires=") {
		t.Error("the service orders something after it or requires something")
	}
}

// TestHarvestHelp: the help names every flag, in both languages.
func TestHarvestHelp(t *testing.T) {
	for _, lang := range []string{"en", "ko"} {
		code, out, _ := run("help", "harvest", "--lang", lang)
		if code != 0 {
			t.Fatal(code)
		}
		for _, f := range []string{"--ledger", "--deadline", "--timer", "--now", "--dry-run", "--lang"} {
			if !strings.Contains(out, f) {
				t.Errorf("%s help lacks %s", lang, f)
			}
		}
	}
	if code, _, errOut := run("harvest", "reboot"); code != cli.ExitUsage || !strings.Contains(errOut, "unknown subcommand") {
		t.Errorf("unknown subcommand: %d %s", code, errOut)
	}
	_ = io.Discard
	_ = bert.Kind
	_ = scan.Options{}
}

// TestOverviewNamesTheKeptLedger: the overview of a scan of this host ends
// with the command that opens the kept ledger, when it is there.
func TestOverviewNamesTheKeptLedger(t *testing.T) {
	old := harvestLedger
	t.Cleanup(func() { harvestLedger = old })
	harvestLedger = filepath.Join(t.TempDir(), "ledger.db")
	s := &session{o: &options{}, lang: "en", prefix: "my"}
	has := func() bool {
		for _, h := range s.hints(&view.Overview{}) {
			if h.Command == "my open "+harvestLedger {
				return true
			}
		}
		return false
	}
	if has() {
		t.Error("named without a ledger")
	}
	os.WriteFile(harvestLedger, []byte("x"), 0o600)
	if !has() {
		t.Error("not named with a ledger")
	}
	s.o.root = "/somewhere"
	if has() {
		t.Error("named for a copy of a host's files")
	}
}
