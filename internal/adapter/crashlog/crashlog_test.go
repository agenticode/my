package crashlog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/klog"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/testutil"
)

// The kernel lines of the dumps below are the last lines of a real
// capture, testdata/real/7.3.0-mce-fatal/console.log: a fatal machine
// check of an emulated Skylake-SP that panicked the guest. The pstore
// framing around them (the header of pstore_dump, the "<N>" priority that
// kmsg_dump_get_buffer writes with syslog set, the file names of
// pstore_mkfile and the ids of efi-pstore.c and erst.c) is written from the
// kernel source; no pstore file of a real host is in these tests.
func fatalLines(t *testing.T) []string {
	t.Helper()
	b := testutil.ReadTestdata(t, "real", "7.3.0-mce-fatal", "console.log")
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	return lines[len(lines)-40:]
}

// kmsg writes console lines the way a pstore record holds them: with the
// priority in front (KERN_EMERG for the machine check lines).
func kmsg(lines []string) string {
	var b strings.Builder
	for _, l := range lines {
		prio := "<6>"
		if strings.Contains(l, "[Hardware Error]") || strings.Contains(l, "Kernel panic") {
			prio = "<0>"
		}
		b.WriteString(prio + l + "\n")
	}
	return b.String()
}

// The record time of the dumps, and the boot start the machine check
// record gives: TIME 1794739502 at 58.251524 s after boot.
const dumpTime = 1794739503

var bootStart = time.Unix(1794739502, 0).UTC().Add(-58251524 * time.Microsecond)

func efiID(ts uint64, part, count int) uint64 { return (ts*100+uint64(part))*1000 + uint64(count) }

// splitParts cuts the text into n parts, Part 1 the newest.
func splitParts(lines []string, n int) []string {
	per := (len(lines) + n - 1) / n
	var parts []string
	for i := 0; i < n; i++ {
		lo, hi := len(lines)-(i+1)*per, len(lines)-i*per
		lo = max(lo, 0)
		parts = append(parts, kmsg(lines[lo:hi]))
	}
	return parts // parts[0] = Part 1
}

func write(t *testing.T, dir, name, content string, mtime time.Time) string {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if !mtime.IsZero() {
		os.Chtimes(p, mtime, mtime)
	}
	return p
}

// efiStore writes a Panic dump of three parts as /sys/fs/pstore shows it
// for the EFI backend.
func efiStore(t *testing.T, dir string) {
	parts := splitParts(fatalLines(t), 3)
	for i, text := range parts {
		no := i + 1
		write(t, dir, fmt.Sprintf("dmesg-efi_pstore-%d", efiID(dumpTime, no, 1)), fmt.Sprintf("Panic#1 Part%d\n%s", no, text), time.Unix(dumpTime, 0))
	}
}

type ledger struct {
	*store.Store
	t *testing.T
}

func newLedger(t *testing.T) *ledger {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &ledger{st, t}
}

func (l *ledger) run(adapters ...adapter.Adapter) *ingest.Result {
	l.t.Helper()
	res, err := ingest.Run(context.Background(), l.Store, adapters, ingest.Options{Host: "h1", Now: time.Unix(dumpTime+3600, 0)})
	if err != nil {
		l.t.Fatal(err)
	}
	return res
}

// events returns the error records of the ledger; panics returns the
// Informational records that say a boot ended in a panic.
func (l *ledger) events() []*model.Event {
	l.t.Helper()
	var out []*model.Event
	for _, e := range l.all() {
		if e.ErrorSeverity != model.SevInformational {
			out = append(out, e)
		}
	}
	return out
}

func (l *ledger) panics() []*model.Event {
	l.t.Helper()
	var out []*model.Event
	for _, e := range l.all() {
		if e.ErrorSeverity == model.SevInformational && e.ErrorType == model.ErrorTypePanic {
			out = append(out, e)
		}
	}
	return out
}

func (l *ledger) all() []*model.Event {
	l.t.Helper()
	ev, err := l.EventList(context.Background(), store.EventFilter{WithDuplicates: true, WithRaw: true})
	if err != nil {
		l.t.Fatal(err)
	}
	return ev
}

// ended checks that every boot with a dump of a panic is known to have
// ended: in a panic, or in a stop when the text shows the hardware error
// that caused it.
func (l *ledger) ended(want int) {
	l.t.Helper()
	boots, err := l.Boots(context.Background(), "h1")
	if err != nil {
		l.t.Fatal(err)
	}
	n := 0
	for _, b := range boots {
		if b.End != "" {
			n++
			if !strings.HasPrefix(b.EndRef, "event:") || b.EndSource == "" {
				l.t.Errorf("boot %s ended %q without its record (%q, %q)", b.BootID, b.End, b.EndRef, b.EndSource)
			}
		}
	}
	if n != want {
		l.t.Errorf("%d boots with an end, want %d: %+v", n, want, boots)
	}
}

func source(t *testing.T, res *ingest.Result, kind string) model.Source {
	t.Helper()
	for _, s := range res.Sources {
		if s.Kind == kind {
			return s
		}
	}
	t.Fatalf("no source %s in %+v", kind, res.Sources)
	return model.Source{}
}

// TestEFIDump: the parts of an EFI dump are put in order, the headers are
// taken out, and the machine check record in it becomes one fatal event of
// the boot that crashed, dated by its own TIME field.
func TestEFIDump(t *testing.T) {
	dir := t.TempDir()
	efiStore(t, dir)
	l := newLedger(t)
	res := l.run(NewDir(KindPstore, dir, Options{ModTime: true}))
	src := source(t, res, KindPstore)
	if src.Status != model.SourceOK || src.Events != 2 {
		t.Fatalf("source %+v", src)
	}
	if p := l.panics(); len(p) != 1 || p[0].Raw != "" || p[0].Source != "pstore/panic" {
		t.Errorf("panic records %+v", p)
	}
	l.ended(1)
	ev := l.events()
	if len(ev) != 1 {
		t.Fatalf("%d events", len(ev))
	}
	e := ev[0]
	if e.ErrorSeverity != model.SevFatal && e.ErrorSeverity != model.SevUE {
		t.Errorf("severity %s", e.ErrorSeverity)
	}
	if !e.ErrorDT.Equal(time.Unix(1794739502, 0)) || e.TimeQuality != model.TimeExact {
		t.Errorf("time %s %s", e.ErrorDT, e.TimeQuality)
	}
	if !e.OsPhysicalAddress.Valid || e.OsPhysicalAddress.V != 0x12345040 {
		t.Errorf("address %+v", e.OsPhysicalAddress)
	}
	if !strings.HasPrefix(e.RawRef, filepath.Join(dir, fmt.Sprintf("dmesg-efi_pstore-%d", efiID(dumpTime, 1, 1)))+"+2#L") {
		t.Errorf("raw ref %s", e.RawRef)
	}
	if strings.Contains(e.Raw, "Part") {
		t.Errorf("a header line is part of the record: %q", e.Raw)
	}
	boots, _ := l.Boots(context.Background(), "h1")
	if len(boots) != 1 || !boots[0].StartedAt.Truncate(time.Second).Equal(bootStart.Truncate(time.Second)) {
		t.Errorf("boots %+v, want a start at %s", boots, bootStart)
	}
	wantNote(t, src.Notes, "Panic dump in 3 parts", "written 2026-")
}

func wantNote(t *testing.T, notes []string, parts ...string) {
	t.Helper()
	all := strings.Join(notes, "\n")
	for _, p := range parts {
		if !strings.Contains(all, p) {
			t.Errorf("notes lack %q:\n%s", p, all)
		}
	}
}

// TestSameAsTheConsole: the lines of the dump and of the console log are
// the same records: one fingerprint, stored once.
func TestSameAsTheConsole(t *testing.T) {
	dir := t.TempDir()
	efiStore(t, dir)
	console := write(t, t.TempDir(), "console.log", strings.Join(fatalLines(t), "\n")+"\n", time.Time{})
	l := newLedger(t)
	l.run(klog.NewFile(console, klog.Options{}))
	res := l.run(NewDir(KindPstore, dir, Options{ModTime: true}))
	// The dump adds the record of its panic and no error.
	if src := source(t, res, KindPstore); src.Events != 2 || res.Events != 1 {
		t.Errorf("the dump added %d events (source %+v)", res.Events, src)
	}
	if ev := l.events(); len(ev) != 1 {
		t.Errorf("%d events, want 1", len(ev))
	}
}

// TestStoreAndArchive: the same dump in /sys/fs/pstore and in the archive
// of systemd-pstore, read in one run, is one set of records; the
// dmesg.txt beside the archived parts is not read again.
func TestStoreAndArchive(t *testing.T) {
	root := t.TempDir()
	live := filepath.Join(root, "sys/fs/pstore")
	efiStore(t, live)
	// src/pstore/pstore.c, process_dmesg_files: <id without the last five
	// digits>/<last three digits>/<file>, and dmesg.txt with the parts in
	// order, headers included.
	arch := filepath.Join(root, "var/lib/systemd/pstore")
	sub := fmt.Sprintf("%d/001", dumpTime)
	parts := splitParts(fatalLines(t), 3)
	var whole strings.Builder
	for i := len(parts) - 1; i >= 0; i-- {
		name := fmt.Sprintf("dmesg-efi_pstore-%d", efiID(dumpTime, i+1, 1))
		text := fmt.Sprintf("Panic#1 Part%d\n%s", i+1, parts[i])
		write(t, arch, sub+"/"+name, text, time.Unix(dumpTime+60, 0))
		whole.WriteString(text)
	}
	write(t, arch, sub+"/dmesg.txt", whole.String(), time.Unix(dumpTime+60, 0))
	l := newLedger(t)
	res := l.run(NewDir(KindPstore, live, Options{ModTime: true}), NewDir(KindPstore, arch, Options{}))
	if res.Events != 2 || len(l.events()) != 1 || len(l.panics()) != 1 {
		t.Errorf("%d events added, want the error and the panic", res.Events)
	}
	var archSrc model.Source
	for _, s := range res.Sources {
		if s.Origin == arch {
			archSrc = s
		}
	}
	if archSrc.Events != 2 {
		t.Errorf("archive source %+v", archSrc)
	}
	wantNote(t, archSrc.Notes, "1 dmesg.txt files that repeat the parts beside them: not read", "2 records were already in the ledger")
	if boots, _ := l.Boots(context.Background(), "h1"); len(boots) != 1 {
		t.Errorf("%d boots, want 1: %+v", len(boots), boots)
	}
}

// TestArchiveTextOnly: an archive whose parts are gone is read from the
// dmesg.txt systemd-pstore wrote, with its time from the directory name.
func TestArchiveTextOnly(t *testing.T) {
	arch := t.TempDir()
	parts := splitParts(fatalLines(t), 2)
	// No machine check TIME in the text: the time of the directory dates
	// the boot.
	var whole strings.Builder
	for i := len(parts) - 1; i >= 0; i-- {
		whole.WriteString(fmt.Sprintf("Panic#1 Part%d\n%s", i+1, strings.ReplaceAll(parts[i], "TIME 1794739502", "TIME 0")))
	}
	write(t, arch, fmt.Sprintf("%d/001/dmesg.txt", dumpTime), whole.String(), time.Time{})
	l := newLedger(t)
	l.run(NewDir(KindPstore, arch, Options{}))
	ev := l.events()
	if len(ev) != 1 || ev[0].TimeQuality != model.TimeEstimated {
		t.Fatalf("events %+v", ev)
	}
	// The last line of the dump is 58.275447 s after boot, written at the
	// record time.
	want := time.Unix(dumpTime, 0).Add(-58275447 * time.Microsecond).Add(58251524 * time.Microsecond)
	if d := ev[0].ErrorDT.Sub(want); d < -time.Second || d > time.Second {
		t.Errorf("time %s, want %s", ev[0].ErrorDT, want)
	}
}

// TestERSTRuns: ERST gives consecutive ids to the records of a boot; two
// dumps of one boot are told apart by their headers, and a gap in the ids
// starts another dump.
func TestERSTRuns(t *testing.T) {
	dir := t.TempDir()
	lines := fatalLines(t)
	base := uint64(1794739000) << 32
	write(t, dir, fmt.Sprintf("dmesg-erst-%d", base+1), "Oops#1 Part1\n"+kmsg(lines[:10]), time.Unix(dumpTime-30, 0))
	parts := splitParts(lines, 2)
	write(t, dir, fmt.Sprintf("dmesg-erst-%d", base+2), "Panic#2 Part1\n"+parts[0], time.Unix(dumpTime, 0))
	write(t, dir, fmt.Sprintf("dmesg-erst-%d", base+3), "Panic#2 Part2\n"+parts[1], time.Unix(dumpTime, 0))
	other := uint64(1794700000) << 32
	write(t, dir, fmt.Sprintf("dmesg-erst-%d", other+1), "Panic#1 Part1\n"+kmsg(lines[30:]), time.Unix(dumpTime-3600, 0))
	a := NewDir(KindPstore, dir, Options{ModTime: true})
	f, err := a.walk()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range f.dumps {
		var ids []string
		for _, p := range d.parts {
			ids = append(ids, fmt.Sprint(p.id-(p.id>>32<<32)))
		}
		got = append(got, d.reason+":"+strings.Join(ids, ","))
	}
	want := []string{"Panic:1", "Oops:1", "Panic:3,2"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("dumps %q, want %q", got, want)
	}
}

// TestEdges: parts in the wrong order of names, a missing part, a record
// the kernel could not decompress, an empty file, a header without text,
// records of other front ends, a file closed to the user.
func TestEdges(t *testing.T) {
	dir := t.TempDir()
	lines := fatalLines(t)
	parts := splitParts(lines, 3)
	// EFI parts 1 and 3 of a dump; part 2 is gone.
	write(t, dir, fmt.Sprintf("dmesg-efi_pstore-%d", efiID(dumpTime, 1, 2)), "Panic#2 Part1\n"+parts[0], time.Time{})
	write(t, dir, fmt.Sprintf("dmesg-efi_pstore-%d", efiID(dumpTime, 3, 2)), "Panic#2 Part3\n"+parts[2], time.Time{})
	write(t, dir, fmt.Sprintf("dmesg-efi_pstore-%d.enc.z", efiID(dumpTime-99, 1, 1)), "\x78\x9c garbage", time.Time{})
	write(t, dir, "dmesg-ramoops-0", "", time.Time{})
	write(t, dir, "dmesg-ramoops-1", "Oops#1 Part1\n", time.Time{})
	write(t, dir, "pmsg-ramoops-0", "user space text\n", time.Time{})
	write(t, dir, "ftrace-ramoops-0", "CPU:0 ts:1 ...\n", time.Time{})
	closed := write(t, dir, "dmesg-ramoops-2", "Oops#3 Part1\n"+kmsg(lines[:3]), time.Time{})
	if os.Geteuid() != 0 {
		os.Chmod(closed, 0)
		t.Cleanup(func() { os.Chmod(closed, 0o644) })
	}
	l := newLedger(t)
	res := l.run(NewDir(KindPstore, dir, Options{}))
	src := source(t, res, KindPstore)
	if src.Status != model.SourcePartial || len(l.events()) != 1 {
		t.Errorf("source %+v", src)
	}
	notes := []string{"Panic dump in 2 parts, part 2 missing", "1 compressed records the kernel could not decompress (.enc.z): not read", "1 pmsg records: not read", "1 ftrace records: not read"}
	wantNote(t, append(src.Notes, src.Detail), notes...)
	all := strings.Join(src.Notes, "\n")
	if !strings.Contains(all, "holds no kernel log lines") {
		t.Errorf("an empty file and a header without text are not reported:\n%s", all)
	}
	if os.Geteuid() != 0 && !strings.Contains(all, "not readable as this user") {
		t.Errorf("a closed file is not reported:\n%s", all)
	}
	// The order: Part 3 before Part 1.
	f, _ := NewDir(KindPstore, dir, Options{}).walk()
	for _, d := range f.dumps {
		if d.reason == "Panic" && (len(d.parts) != 2 || d.parts[0].no != 3) {
			t.Errorf("order of the parts: %+v", d.parts)
		}
	}
}

// TestUnreadableStore: a store closed to the user is reported as such.
func TestUnreadableStore(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every directory")
	}
	dir := filepath.Join(t.TempDir(), "pstore")
	os.MkdirAll(dir, 0o750)
	os.Chmod(dir, 0)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	rep := NewDir(KindPstore, dir, Options{}).Collect(context.Background(), nil)
	if rep.Status != model.SourceUnreadable || !strings.Contains(rep.Detail, "readable as root") {
		t.Errorf("report %+v", rep)
	}
	empty := t.TempDir()
	if rep := NewDir(KindPstore, empty, Options{}).Collect(context.Background(), nil); rep.Status != model.SourceEmpty {
		t.Errorf("empty store: %+v", rep)
	}
}

// TestKdump: the text log of kdump is the kernel log of the crashed boot,
// dated by its RTC line; the memory dump and the log of the capture kernel
// beside it are not read.
func TestKdump(t *testing.T) {
	root := t.TempDir()
	lines := fatalLines(t)
	// vmcore-dmesg prints "[%5llu.%06llu] " and the text
	// (kexec-tools util_lib/elf_info.c). The RTC line of the boot comes
	// from drivers/rtc/class.c, rtc_hctosys.
	rtc := fmt.Sprintf("[    1.523411] rtc_cmos 00:00: setting system clock to 2026-11-15T03:24:45 UTC (%d)\n", bootStart.Add(1523411*time.Microsecond).Unix())
	text := "[    0.000000] Linux version 7.3.0-ras (builder@example) (gcc) #1 SMP\n" + rtc + strings.Join(lines, "\n") + "\n"
	dump := filepath.Join(root, "127.0.0.1-2026-11-15-03:25:52")
	write(t, dump, "vmcore-dmesg.txt", strings.ReplaceAll(text, "TIME 1794739502", "TIME 0"), time.Unix(dumpTime+120, 0))
	write(t, dump, "vmcore", "ELF", time.Time{})
	write(t, dump, "kexec-dmesg.log", "[    0.000000] Linux version 7.3.0-ras\n", time.Time{})
	deb := filepath.Join(root, "202611150325")
	write(t, deb, "dmesg.202611150325", text, time.Unix(dumpTime+120, 0))
	write(t, deb, "dump.202611150325", "KDUMP", time.Time{})
	l := newLedger(t)
	res := l.run(NewDir(KindKdump, root, Options{}))
	src := source(t, res, KindKdump)
	// Each log adds the record of its crash; the error is one record.
	if src.Events != 4 || res.Events != 3 || len(l.panics()) != 2 {
		t.Errorf("source %+v, %d events added", src, res.Events)
	}
	l.ended(1)
	wantNote(t, src.Notes, "kexec-dmesg.log, the log of the capture kernel: not read", "2 memory dumps (vmcore): not read", "kdump text log")
	ev := l.events()
	if len(ev) != 1 {
		t.Fatalf("%d events", len(ev))
	}
	// The RTC line dates the boot to the second.
	if d := ev[0].ErrorDT.Sub(time.Unix(1794739502, 0)); d < -2*time.Second || d > 2*time.Second {
		t.Errorf("time %s", ev[0].ErrorDT)
	}
	boots, _ := l.Boots(context.Background(), "h1")
	if len(boots) != 1 || boots[0].Kernel != "7.3.0-ras" {
		t.Errorf("boots %+v", boots)
	}
}

// TestJournalBoot: a dump of a boot the journal also holds is tied to the
// journal's boot, and a line both hold is stored once.
func TestJournalBoot(t *testing.T) {
	lines := fatalLines(t)
	const bootID = "0123456789abcdef0123456789abcdef"
	// The journal kept the first lines of the 40 and lost the rest when
	// the guest panicked; journald stamps kernel lines with the kernel's
	// own time (_SOURCE_MONOTONIC_TIMESTAMP).
	var j strings.Builder
	for _, line := range append([]string{"[    0.000000] Linux version 7.3.0-ras (builder@example) (gcc) #1 SMP"}, lines[:20]...) {
		mono, msg := splitStamp(t, line)
		wall := bootStart.Add(mono)
		fmt.Fprintf(&j, `{"MESSAGE":%q,"__REALTIME_TIMESTAMP":"%d","__MONOTONIC_TIMESTAMP":"%d","_SOURCE_MONOTONIC_TIMESTAMP":"%d","_BOOT_ID":%q,"_TRANSPORT":"kernel","_HOSTNAME":"h1"}`+"\n",
			msg, wall.UnixMicro(), mono.Microseconds()+5000, mono.Microseconds(), bootID)
	}
	jf := write(t, t.TempDir(), "journal.json", j.String(), time.Time{})
	dir := t.TempDir()
	efiStore(t, dir)
	l := newLedger(t)
	l.run(klog.NewFile(jf, klog.Options{}), NewDir(KindPstore, dir, Options{ModTime: true}))
	boots, _ := l.Boots(context.Background(), "h1")
	if len(boots) != 1 || boots[0].BootID != bootID {
		t.Errorf("boots %+v, want the journal's", boots)
	}
	ev := l.events()
	if len(ev) != 1 || ev[0].BootID != bootID {
		t.Errorf("events %+v", ev)
	}
}

func splitStamp(t *testing.T, line string) (time.Duration, string) {
	t.Helper()
	i := strings.Index(line, "] ")
	var sec, usec int64
	if _, err := fmt.Sscanf(strings.TrimSpace(line[1:i]), "%d.%d", &sec, &usec); err != nil {
		t.Fatalf("%q: %v", line, err)
	}
	return time.Duration(sec)*time.Second + time.Duration(usec)*time.Microsecond, line[i+2:]
}

// TestSniff: a pstore file is known by its name or its first line, a kdump
// log by its name.
func TestSniff(t *testing.T) {
	for _, tt := range []struct {
		name, head, want string
	}{
		{"dmesg-erst-7300000000000000001", "", KindPstore},
		{"dmesg-efi_pstore-179473950301001", "Panic#1 Part1\n", KindPstore},
		{"console-ramoops-0", "[    0.0] x\n", KindPstore},
		{"saved.txt", "Panic#1 Part1\n<6>[ 1.0] x\n", KindPstore},
		{"saved.txt", "Oops#12 Part3\r\n", KindPstore},
		{"vmcore-dmesg.txt", "[    0.000000] Linux version\n", KindKdump},
		{"dmesg.202611150325", "", KindKdump},
		{"dmesg", "[    0.000000] Linux version\n", ""},
		{"dmesg.txt", "Panic#1\n", ""},
		{"dmesg-erst-x", "", ""},
	} {
		if got := Sniff(tt.name, []byte(tt.head)); got != tt.want {
			t.Errorf("Sniff(%q, %q) = %q, want %q", tt.name, tt.head, got, tt.want)
		}
	}
}

// TestReadAgain: a kept ledger that reads the same crash logs again, with
// the journal of the boots, gains no rows, and its boots end as before.
func TestReadAgain(t *testing.T) {
	root := t.TempDir()
	efiStore(t, filepath.Join(root, "store"))
	console := write(t, root, "console.log", strings.Join(fatalLines(t), "\n")+"\n", time.Time{})
	l := newLedger(t)
	read := func() {
		l.run(klog.NewFile(console, klog.Options{}), NewDir(KindPstore, filepath.Join(root, "store"), Options{ModTime: true}))
	}
	count := func() string {
		var ev, boots, dups int
		db := l.Store
		for _, e := range l.all() {
			ev++
			if e.DupOf != 0 {
				dups++
			}
		}
		bs, _ := db.Boots(context.Background(), "h1")
		boots = len(bs)
		var ends []string
		for _, b := range bs {
			ends = append(ends, b.BootID+"="+b.End+"/"+b.EndRef)
		}
		return fmt.Sprintf("events %d, duplicates %d, boots %d, ends %v", ev, dups, boots, ends)
	}
	read()
	first := count()
	read()
	if again := count(); again != first {
		t.Errorf("read again: %s\nfirst:       %s", again, first)
	}
}
