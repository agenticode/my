package scan

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/synth"
	"github.com/agenticode/my/internal/testledger"
	"github.com/agenticode/my/internal/testutil"
)

var ctx = context.Background()

// tree writes the files of the demo host as a platform of the given
// flavor would hold them, and returns the root.
func tree(t *testing.T, fl synth.Flavor) (string, *synth.Dataset) {
	t.Helper()
	ds := testledger.Generate(t, "demo", 1, nil)
	root := filepath.Join(t.TempDir(), "root")
	if err := ds.WriteHostTree(root, fl); err != nil {
		t.Fatal(err)
	}
	return root, ds
}

func ledger(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// inventory writes the source inventory of a run as "kind status" lines.
func inventory(srcs []model.Source) string {
	var out []string
	for _, s := range srcs {
		out = append(out, s.Kind+" "+string(s.Status))
	}
	return strings.Join(out, ", ")
}

func source(t *testing.T, srcs []model.Source, kind string) model.Source {
	t.Helper()
	for _, s := range srcs {
		if s.Kind == kind {
			return s
		}
	}
	t.Fatalf("no %s in the inventory: %s", kind, inventory(srcs))
	return model.Source{}
}

func write(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func TestScanOfAHostTree(t *testing.T) {
	root, ds := tree(t, synth.FlavorIntel)
	s := ledger(t)
	res, err := Run(ctx, s, Options{Root: root, Now: ds.Scenario.End, TempDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := inventory(res.Sources), "hostinfo ok, rasdaemon ok, klog ok, edac-sysfs ok, smbios ok, slot-map ok"; got != want {
		t.Errorf("inventory = %s\nwant        %s", got, want)
	}
	if res.Host != "r12-n034" || res.Scan.Kind != model.ScanImport {
		t.Errorf("host %q, kind %q", res.Host, res.Scan.Kind)
	}
	if k := source(t, res.Sources, "klog"); !strings.HasSuffix(k.Origin, filepath.Join("var", "log", "dmesg")) || k.Events == 0 {
		t.Errorf("kernel log source = %+v", k)
	}
	totals, err := s.Totals(ctx, store.EventFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var ce, ue int64
	for _, x := range totals {
		ce, ue = ce+x.CE, ue+x.UE
	}
	// Every error is counted once, though the database, the kernel log and
	// the counters all report it.
	if ce != 1284 || ue != 1 {
		t.Errorf("CE %d, UE %d; the host had 1,284 and 1", ce, ue)
	}
}

func TestCopyIsReadAsOfItsOwnTime(t *testing.T) {
	// No clock is given: the copy says when it was made. The counters of
	// the running boot count back from that moment; read as of today they
	// would no longer line up with the located records, and errors would
	// be counted twice.
	root, ds := tree(t, synth.FlavorIntel)
	s := ledger(t)
	res, err := Run(ctx, s, Options{Root: root, TempDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Scan.StartedAt.Equal(ds.Scenario.End) {
		t.Errorf("scan time = %v, want the time of the files, %v", res.Scan.StartedAt, ds.Scenario.End)
	}
	totals, err := s.Totals(ctx, store.EventFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var n int64
	for _, x := range totals {
		n += x.CE + x.UE
	}
	if n != 1285 {
		t.Errorf("%d errors, want 1,285", n)
	}
	// Reading the copy again adds nothing.
	if _, err := Run(ctx, s, Options{Root: root, TempDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	totals, _ = s.Totals(ctx, store.EventFilter{})
	n = 0
	for _, x := range totals {
		n += x.CE + x.UE
	}
	if n != 1285 {
		t.Errorf("%d errors after a second scan, want 1,285", n)
	}
}

func TestScanOfNothing(t *testing.T) {
	root := t.TempDir()
	res, err := Run(ctx, ledger(t), Options{Root: root, Now: time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := inventory(res.Sources), "hostinfo absent, rasdaemon absent, klog absent, edac-sysfs absent, smbios absent"; got != want {
		t.Errorf("inventory = %s\nwant        %s", got, want)
	}
	if k := source(t, res.Sources, "klog"); !strings.Contains(k.Detail, "no kernel log file found") {
		t.Errorf("kernel log detail = %q", k.Detail)
	}
	if res.Host != "localhost" || res.Events != 0 {
		t.Errorf("host %q, %d events", res.Host, res.Events)
	}
}

func TestScanSaysWhatItMayNotRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	root, ds := tree(t, synth.FlavorIntel)
	for _, rel := range []string{"sys/firmware/dmi/tables/DMI", "var/log/dmesg", "var/lib/rasdaemon/ras-mc_event.db"} {
		if err := os.Chmod(filepath.Join(root, filepath.FromSlash(rel)), 0); err != nil {
			t.Fatal(err)
		}
	}
	res, err := Run(ctx, ledger(t), Options{Root: root, Now: ds.Scenario.End, TempDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"smbios", "klog", "rasdaemon"} {
		src := source(t, res.Sources, kind)
		if src.Status != model.SourceUnreadable || !strings.Contains(src.Detail, "root") {
			t.Errorf("%s: status %s, detail %q; want unreadable with what root would add", kind, src.Status, src.Detail)
		}
	}
	// The counters are world-readable: the scan still has something.
	if src := source(t, res.Sources, "edac-sysfs"); src.Status != model.SourceOK {
		t.Errorf("edac-sysfs: %s", src.Status)
	}
}

func TestLogFilePreference(t *testing.T) {
	root, ds := tree(t, synth.FlavorIntel)
	// A syslog file carries dates and earlier boots: it is read, and the
	// capture of the running boot beside it is not.
	messages := string(testutil.ReadTestdata(t, "klog", "intel-skx.messages"))
	write(t, filepath.Join(root, "var", "log", "messages"), messages, 0o644)
	srcs := Sources(ctx, Options{Root: root, Now: ds.Scenario.End})
	var logs []string
	for _, a := range srcs {
		if a.Kind() == "klog" {
			logs = append(logs, a.Origin())
		}
	}
	if len(logs) != 1 || !strings.HasSuffix(logs[0], filepath.Join("var", "log", "messages")) {
		t.Errorf("kernel log sources = %v", logs)
	}
}

func TestDmidecodeTextInATree(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "sos_commands", "hardware", "dmidecode"), string(testutil.ReadTestdata(t, "smbios", "dmidecode-t17-ddr4.txt")), 0o644)
	res, err := Run(ctx, ledger(t), Options{Root: root, Now: time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	// The fixture holds two blocks that cannot be used: the source is read
	// in part, and says so.
	if src := source(t, res.Sources, "smbios"); src.Status != model.SourcePartial || src.Records != 6 || !strings.HasSuffix(src.Origin, "dmidecode") {
		t.Errorf("smbios source = %+v", src)
	}
}

func TestRasdaemonAtTheSourceBuildPath(t *testing.T) {
	root, ds := tree(t, synth.FlavorIntel)
	from := filepath.Join(root, "var", "lib", "rasdaemon", "ras-mc_event.db")
	to := filepath.Join(root, filepath.FromSlash(RasdaemonPaths[1]))
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
	res, err := Run(ctx, ledger(t), Options{Root: root, Now: ds.Scenario.End, TempDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if src := source(t, res.Sources, "rasdaemon"); src.Status != model.SourceOK || src.Origin != to {
		t.Errorf("rasdaemon source = %+v", src)
	}
}

// fakeJournalctl writes a program that stands in for journalctl. With the
// arguments of the probe (-n 1) it runs probe, otherwise read.
func fakeJournalctl(t *testing.T, probe, read string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "journalctl")
	script := fmt.Sprintf("#!/bin/sh\ncase \" $* \" in\n*\" -n 1 \"*)\n%s\n;;\n*\" -b 0 \"*)\n%s\n;;\n*)\n%s\n;;\nesac\n", probe, "cat \"$BOOT0\" 2>/dev/null", read)
	write(t, path, script, 0o755)
	return path
}

func TestJournalOfALiveHost(t *testing.T) {
	root, ds := tree(t, synth.FlavorIntel)
	fixture := testutil.Testdata(t, "klog", "amd.journal.json")
	prog := fakeJournalctl(t, "head -1 "+fixture, "cat "+fixture)
	res, err := Run(ctx, ledger(t), Options{Root: root, Live: true, Now: ds.Scenario.End, TempDir: t.TempDir(), Journalctl: prog})
	if err != nil {
		t.Fatal(err)
	}
	k := source(t, res.Sources, "klog")
	// The fixture holds one entry that is not valid JSON, hence "partial".
	if k.Status != model.SourcePartial || !strings.HasPrefix(k.Origin, "journalctl ") || k.Events != 4 || k.Actions != 2 {
		t.Errorf("kernel log source = %+v", k)
	}
	if res.Scan.Kind != model.ScanLive {
		t.Errorf("scan kind = %q", res.Scan.Kind)
	}
	// One kernel log source: the journal covers the running boot too.
	n := 0
	for _, s := range res.Sources {
		if s.Kind == "klog" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d kernel log sources: %s", n, inventory(res.Sources))
	}
}

func TestJournalDeniedFallsBackToTheRingBuffer(t *testing.T) {
	root, ds := tree(t, synth.FlavorIntel)
	write(t, filepath.Join(root, "dev", "kmsg"), string(testutil.ReadTestdata(t, "klog", "kmsg.txt")), 0o644)
	denied := `echo "Hint: You are currently not seeing messages from other users and the system." >&2`
	prog := fakeJournalctl(t, denied, denied)
	res, err := Run(ctx, ledger(t), Options{Root: root, Live: true, Now: ds.Scenario.End, TempDir: t.TempDir(), Journalctl: prog})
	if err != nil {
		t.Fatal(err)
	}
	var logs []model.Source
	for _, s := range res.Sources {
		if s.Kind == "klog" {
			logs = append(logs, s)
		}
	}
	if len(logs) != 2 {
		t.Fatalf("kernel log sources: %s", inventory(res.Sources))
	}
	if logs[0].Status != model.SourceUnreadable || !strings.Contains(logs[0].Detail, "systemd-journal") {
		t.Errorf("journal entry = %+v", logs[0])
	}
	if logs[1].Status == model.SourceUnreadable || !strings.HasSuffix(logs[1].Origin, filepath.Join("dev", "kmsg")) || logs[1].Records == 0 {
		t.Errorf("ring buffer entry = %+v", logs[1])
	}
}

func TestNoJournalNoRingBufferFallsBackToFiles(t *testing.T) {
	root, ds := tree(t, synth.FlavorIntel)
	srcs := Sources(ctx, Options{Root: root, Live: true, Now: ds.Scenario.End, Journalctl: "-"})
	var logs []string
	for _, a := range srcs {
		if a.Kind() == "klog" {
			logs = append(logs, a.Origin())
		}
	}
	if len(logs) != 1 || !strings.HasSuffix(logs[0], filepath.Join("var", "log", "dmesg")) {
		t.Errorf("kernel log sources = %v", logs)
	}
	// A journalctl that is not there is the same as none.
	srcs = Sources(ctx, Options{Root: root, Live: true, Now: ds.Scenario.End, Journalctl: filepath.Join(root, "no-such-program")})
	if len(srcs) != 5 {
		t.Errorf("%d sources", len(srcs))
	}
}

func TestJournalThatTakesTooLong(t *testing.T) {
	root, ds := tree(t, synth.FlavorIntel)
	fixture := testutil.Testdata(t, "klog", "amd.journal.json")
	// Reading every boot never ends; the running boot answers at once.
	prog := fakeJournalctl(t, "head -1 "+fixture, "head -3 "+fixture+"; exec sleep 30")
	t.Setenv("BOOT0", fixture)
	start := time.Now()
	res, err := Run(ctx, ledger(t), Options{Root: root, Live: true, Now: ds.Scenario.End, TempDir: t.TempDir(), Journalctl: prog, JournalTimeout: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Errorf("the scan took %s", d)
	}
	k := source(t, res.Sources, "klog")
	if k.Status != model.SourcePartial || !strings.Contains(k.Detail, "running boot was read in full") || k.Events == 0 {
		t.Errorf("kernel log source = %+v", k)
	}
}

func TestJournalErrors(t *testing.T) {
	j := &journal{prog: fakeJournalctl(t, "echo 'Failed to open journal: No such file or directory' >&2; exit 1", "exit 1")}
	if status, detail := j.probe(ctx); status != model.SourceError || !strings.Contains(detail, "Failed to open journal") {
		t.Errorf("probe = %s, %q", status, detail)
	}
	j = &journal{prog: fakeJournalctl(t, "echo '-- No entries --'", "true")}
	if status, _ := j.probe(ctx); status != model.SourceAbsent {
		t.Errorf("probe of an empty journal = %s", status)
	}
	j = &journal{prog: filepath.Join(t.TempDir(), "missing")}
	if status, _ := j.probe(ctx); status != model.SourceAbsent {
		t.Errorf("probe without a program = %s", status)
	}
	// The program fails after the probe said yes.
	j = &journal{prog: fakeJournalctl(t, "true", "echo 'journal corrupted' >&2; exit 1")}
	rep := j.Collect(ctx, &adapter.Collected{})
	if rep.Status != model.SourceError || !strings.Contains(rep.Detail, "journal corrupted") {
		t.Errorf("report = %+v", rep)
	}
}

func TestKmsgRecords(t *testing.T) {
	rec := "6,1340,18412233,-;EDAC MC1: Giving out device\n SUBSYSTEM=edac\n DEVICE=+edac:mc1\n"
	if got := string(records(nil, []byte(rec))); got != "6,1340,18412233,-;EDAC MC1: Giving out device\n" {
		t.Errorf("records = %q", got)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "kmsg")
	write(t, path, rec+"4,1341,18412300,-;second\n", 0o644)
	text, lost, err := readKmsg(path, maxKmsg)
	if err != nil || lost != 0 || strings.Count(string(text), "\n") != 2 || strings.Contains(string(text), "SUBSYSTEM") {
		t.Errorf("readKmsg = %q, %d, %v", text, lost, err)
	}
	k := &kmsg{path: filepath.Join(dir, "none")}
	if status, _ := k.probe(); status != model.SourceAbsent {
		t.Errorf("probe of a missing buffer = %s", status)
	}
	if rep := k.Collect(ctx, &adapter.Collected{}); rep.Status != model.SourceAbsent {
		t.Errorf("collect of a missing buffer = %+v", rep)
	}
	if os.Geteuid() != 0 {
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		k = &kmsg{path: path}
		if status, detail := k.probe(); status != model.SourceUnreadable || !strings.Contains(detail, "dmesg_restrict") {
			t.Errorf("probe of a closed buffer = %s, %q", status, detail)
		}
	}
}
