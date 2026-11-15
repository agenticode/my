package klog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/decode/edac"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/testutil"
)

var ctx = context.Background()

func some(v int) model.Opt[int]       { return model.Some(v) }
func addr(v uint64) model.Opt[uint64] { return model.Some(v) }

func utc(y int, mo time.Month, d, h, mi, s int) time.Time {
	return time.Date(y, mo, d, h, mi, s, 0, time.UTC)
}

func collectFile(t *testing.T, name string, opts Options) (*adapter.Collected, adapter.Report, string) {
	t.Helper()
	path := testutil.Testdata(t, "klog", name)
	var c adapter.Collected
	rep := NewFile(path, opts).Collect(ctx, &c)
	if rep.Kind != Kind || rep.Origin != path {
		t.Errorf("report identity: %+v", rep)
	}
	return &c, rep, path
}

func collectText(t *testing.T, text string, opts Options) (*adapter.Collected, adapter.Report) {
	t.Helper()
	var c adapter.Collected
	rep := NewReader(strings.NewReader(text), "test", opts).Collect(ctx, &c)
	return &c, rep
}

// at returns the event or fails; ref is the line part of the raw reference.
func at(t *testing.T, c *adapter.Collected, ref string) *model.Event {
	t.Helper()
	for _, e := range c.Events {
		if strings.HasSuffix(e.RawRef, "#"+ref) {
			return e
		}
	}
	t.Fatalf("no event with raw_ref ending in #%s", ref)
	return nil
}

func hasNote(rep adapter.Report, sub string) bool {
	for _, n := range rep.Notes {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

func near(a, b time.Time, tol time.Duration) bool { return a.Sub(b).Abs() <= tol }

func TestIntelDmesgWithADXL(t *testing.T) {
	c, rep, path := collectFile(t, "intel-adxl.dmesg", Options{})
	boot := utc(2026, 11, 10, 0, 0, 0) // from the machine check record's own time
	us := func(sec, usec int64) time.Time {
		return boot.Add(time.Duration(sec)*time.Second + time.Duration(usec)*time.Microsecond)
	}

	if rep.Status != model.SourcePartial || rep.Records != 14 || rep.Skipped != 2 {
		t.Errorf("report = %+v, want partial, 14 records, 2 skipped", rep)
	}
	for _, want := range []string{
		"lines read", "1 machine checks are not memory errors",
		"#L14: EDAC line is cut off before the closing parenthesis",
		"#L27: machine check record is incomplete",
	} {
		if !hasNote(rep, want) {
			t.Errorf("notes lack %q: %q", want, rep.Notes)
		}
	}
	if len(c.Events) != 5 || len(c.Actions) != 3 || len(c.Boots) != 1 || len(c.Hosts) != 1 {
		t.Fatalf("%d events, %d actions, %d boots, %d hosts; want 5, 3, 1, 1", len(c.Events), len(c.Actions), len(c.Boots), len(c.Hosts))
	}

	e := at(t, c, "L10")
	want := model.Event{
		Kind: model.KindError, ErrorDT: us(8041, 118302), ErrorSeverity: model.SevCE, ErrorScrubType: model.ScrubDemand,
		SocketId: some(0), ChannelId: some(0), ChannelSlotId: some(0), Rank: some(0), BankGroup: some(0), Bank: some(3),
		Row: some(0x1a2f), Column: some(0x3f8), OsPhysicalAddress: addr(0x1f8bd19c0),
		Source: "klog/edac", RawRef: path + "#L10", Grade: model.GradeA, Count: 1, TimeQuality: model.TimeExact,
		IMC: some(1), EdacMC: some(1), EdacLocation: "0:0", Label: "CPU_SrcID#0_MC#1_Chan#0_DIMM#0",
		ErrorType: "memory read error", Decoder: edac.DecoderADXL, Raw: e.Raw, Fingerprint: e.Fingerprint,
	}
	if *e != want {
		t.Errorf("L10\n got: %+v\nwant: %+v", *e, want)
	}
	if !strings.HasPrefix(e.Raw, "EDAC MC1: 1 CE memory read error on") || strings.Contains(e.Raw, "[ 8041") {
		t.Errorf("raw should be the kernel message without its stamp: %q", e.Raw)
	}
	if e := at(t, c, "L12"); e.Count != 2 || e.Column != some(0x100) || e.OsPhysicalAddress != addr(0x1f8bd1400) {
		t.Errorf("L12: %+v", *e)
	}
	if e := at(t, c, "L17"); e.ErrorSeverity != model.SevRecoverable || e.ErrorScrubType != model.ScrubPatrol || e.Syndrome.Valid ||
		!e.ErrorDT.Equal(us(90011, 554120)) {
		t.Errorf("L17 (uncorrected, found by the scrubber): %+v", *e)
	}
	// Row 0, column 0 and bank 0 are reported values: the event is grade A.
	if e := at(t, c, "L28"); e.Grade != model.GradeA || e.Row != some(0) || e.Column != some(0) || e.ChannelId != some(1) || e.Rank != some(1) {
		t.Errorf("L28: %+v", *e)
	}
	// The machine check record carries its own wall-clock time.
	m := at(t, c, "L21-L23")
	if m.Source != "klog/mce" || !m.ErrorDT.Equal(utc(2026, 11, 11, 1, 8, 20)) || m.TimeQuality != model.TimeExact ||
		m.OsPhysicalAddress != addr(0x2a0000040) || m.ChannelId != some(0) || m.SocketId != some(0) || m.Grade != model.GradeB ||
		m.Decoder != "mce/intel" || strings.Count(m.Raw, "\n") != 2 {
		t.Errorf("machine check event: %+v", *m)
	}

	b := c.Boots[0]
	if !b.StartedAt.Equal(boot) || b.StartQuality != model.TimeExact || !b.Derived || b.BootID != "" ||
		b.Kernel != "5.14.0-570.12.1.el9_6.x86_64" || b.BIOS != "1.5.5 02/10/2022" || !strings.HasPrefix(b.Cmdline, "BOOT_IMAGE=") ||
		!b.LastSeen.Equal(us(90700, 500000)) {
		t.Errorf("boot: %+v", *b)
	}
	if c.Hosts[0].SystemProduct != "Example Systems R650/0PYXKY" {
		t.Errorf("host: %+v", *c.Hosts[0])
	}

	wantActs := []struct {
		at     time.Time
		method string
		result model.ActionResult
		detail string
	}{
		{us(86400, 1), "soft", model.ResultUnknown, "corrected error collector"},
		{us(86400, 210094), "soft", model.ResultFailed, "migration failed 1"},
		{us(90011, 554400), "hard", model.ResultOK, "dirty LRU page: Recovered"},
	}
	for i, w := range wantActs {
		a := c.Actions[i]
		if a.Kind != model.ActionPageOffline || !a.At.Equal(w.at) || a.Method != w.method || a.Result != w.result ||
			!strings.Contains(a.Detail, w.detail) || a.OsPhysicalAddress != addr(0x1f8bd1000) || a.Source != "klog/offline" || a.Fingerprint == "" {
			t.Errorf("action %d: %+v", i, *a)
		}
	}
	for _, e := range c.Events {
		if e.BootID != "" {
			t.Errorf("%s: boot %q; events of a boot placed in time are tied to it by time", e.RawRef, e.BootID)
		}
	}
}

const shortDmesg = `[    0.000000] Linux version 6.1.0-18-amd64 (debian-kernel@lists.debian.org) (gcc-12 (Debian 12.2.0-14) 12.2.0) #1 SMP PREEMPT_DYNAMIC Debian 6.1.76-1 (2024-02-01)
[  100.000000] EDAC MC0: 1 CE memory read error on CPU_SrcID#0_MC#0_Chan#0_DIMM#0 (channel:0 slot:0 page:0x1000 offset:0x40 grain:32 syndrome:0x0 -  err_code:0x0101:0x0090 socket:0 imc:0 rank:0 bg:0 ba:1 row:0x2 col:0x8)
[  200.000000] RAS: Soft-offlining pfn: 0x1000
[ 3600.000000] systemd[1]: Started Daily Cleanup of Temporary Directories.
`

func TestBootRelativeTimes(t *testing.T) {
	captured := utc(2026, 5, 1, 12, 0, 0)

	t.Run("no anchor: times stay relative to boot", func(t *testing.T) {
		c, rep := collectText(t, shortDmesg, Options{})
		if rep.Status != model.SourceOK || len(c.Events) != 1 || len(c.Actions) != 1 || len(c.Boots) != 1 {
			t.Fatalf("report %+v, %d events, %d actions, %d boots", rep, len(c.Events), len(c.Actions), len(c.Boots))
		}
		e, a, b := c.Events[0], c.Actions[0], c.Boots[0]
		if e.TimeQuality != model.TimeRelative || !e.ErrorDT.Equal(time.Unix(100, 0)) ||
			a.TimeQuality != model.TimeRelative || !a.At.Equal(time.Unix(200, 0)) {
			t.Errorf("event %v (%s), action %v (%s); want the epoch plus the stamp, relative", e.ErrorDT, e.TimeQuality, a.At, a.TimeQuality)
		}
		if b.StartQuality != model.TimeRelative || !b.Derived || !strings.HasPrefix(b.BootID, "derived-") || e.BootID != b.BootID || a.BootID != b.BootID {
			t.Errorf("boot %+v, event boot %q: records without a date must stay with their own boot", *b, e.BootID)
		}
		if !hasNote(rep, "2 records have only seconds since boot") {
			t.Errorf("notes = %q", rep.Notes)
		}
		// Another capture without an anchor is another boot.
		c2, _ := collectText(t, strings.Replace(shortDmesg, "6.1.0-18", "6.1.0-21", 1), Options{})
		if c2.Boots[0].BootID == b.BootID {
			t.Error("two unrelated captures share a derived boot ID")
		}
	})

	t.Run("capture time: boot is placed at capture minus the last stamp", func(t *testing.T) {
		c, rep := collectText(t, shortDmesg, Options{CapturedAt: captured})
		e, b := c.Events[0], c.Boots[0]
		wantBoot := captured.Add(-3600 * time.Second)
		if !b.StartedAt.Equal(wantBoot) || b.StartQuality != model.TimeEstimated || b.BootID != "" {
			t.Errorf("boot = %+v, want start %v estimated", *b, wantBoot)
		}
		if !e.ErrorDT.Equal(wantBoot.Add(100*time.Second)) || e.TimeQuality != model.TimeEstimated || e.BootID != "" {
			t.Errorf("event at %v (%s)", e.ErrorDT, e.TimeQuality)
		}
		if !hasNote(rep, "2 records have times estimated") {
			t.Errorf("notes = %q", rep.Notes)
		}
	})

	t.Run("boot time from the caller is exact", func(t *testing.T) {
		boot := utc(2026, 5, 1, 9, 0, 0)
		c, _ := collectText(t, shortDmesg, Options{BootTime: boot, BootID: "0123456789abcdef0123456789abcdef", CapturedAt: captured})
		e, b := c.Events[0], c.Boots[0]
		if !b.StartedAt.Equal(boot) || b.StartQuality != model.TimeExact || b.BootID != "0123456789abcdef0123456789abcdef" || b.Derived {
			t.Errorf("boot = %+v", *b)
		}
		if !e.ErrorDT.Equal(boot.Add(100*time.Second)) || e.TimeQuality != model.TimeExact {
			t.Errorf("event at %v (%s)", e.ErrorDT, e.TimeQuality)
		}
	})

	t.Run("file time is used only when asked for", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "dmesg.txt")
		if err := os.WriteFile(path, []byte(shortDmesg), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, captured, captured); err != nil {
			t.Fatal(err)
		}
		var off, on adapter.Collected
		NewFile(path, Options{}).Collect(ctx, &off)
		NewFile(path, Options{ModTimeAsCapture: true}).Collect(ctx, &on)
		if off.Events[0].TimeQuality != model.TimeRelative {
			t.Errorf("default: quality %s, want relative", off.Events[0].TimeQuality)
		}
		if on.Events[0].TimeQuality != model.TimeEstimated || !on.Events[0].ErrorDT.Equal(captured.Add(-3500*time.Second)) {
			t.Errorf("with the file time: %v (%s)", on.Events[0].ErrorDT, on.Events[0].TimeQuality)
		}
	})

	t.Run("the RTC line of the boot anchors it when nothing else does", func(t *testing.T) {
		log := strings.Replace(shortDmesg, "[  100.000000] EDAC",
			"[    1.500000] rtc_cmos 00:02: setting system clock to 2026-05-01T09:00:01 UTC (1777626001)\n[  100.000000] EDAC", 1)
		c, _ := collectText(t, log, Options{})
		b := c.Boots[0]
		want := time.Unix(1777626001, 0).Add(-1500 * time.Millisecond)
		if !b.StartedAt.Equal(want) || b.StartQuality != model.TimeEstimated {
			t.Errorf("boot = %v (%s), want %v estimated", b.StartedAt, b.StartQuality, want)
		}
	})
}

func TestSyslogFileWithTwoBoots(t *testing.T) {
	c, rep, _ := collectFile(t, "intel-skx.messages", Options{CapturedAt: utc(2023, 3, 1, 0, 0, 0)})

	if rep.Status != model.SourceOK || rep.Records != 7 || rep.Skipped != 0 || rep.HostHint != "r07-n011" {
		t.Errorf("report = %+v, want ok, 7 records, host r07-n011", rep)
	}
	if len(c.Events) != 3 || len(c.Actions) != 1 || len(c.Boots) != 2 {
		t.Fatalf("%d events, %d actions, %d boots; want 3, 1, 2", len(c.Events), len(c.Actions), len(c.Boots))
	}
	// The year comes from the capture time, row and column are bare hexadecimal.
	e := at(t, c, "L2")
	if !e.ErrorDT.Equal(utc(2023, 2, 27, 0, 0, 3)) || e.TimeQuality != model.TimeExact || e.Row != some(0x1b8b) || e.Column != some(0x338) ||
		e.Rank != some(1) || e.BankGroup != some(1) || e.Bank != some(3) || e.ChannelId != some(1) || e.ChannelSlotId != some(0) ||
		e.Decoder != edac.DecoderSKX || e.Grade != model.GradeA {
		t.Errorf("L2: %+v", *e)
	}
	if e := at(t, c, "L6"); e.ErrorSeverity != model.SevRecoverable || e.ErrorScrubType != model.ScrubPatrol || !e.ErrorDT.Equal(utc(2023, 2, 27, 3, 14, 7)) {
		t.Errorf("L6: %+v", *e)
	}
	if e := at(t, c, "L13"); e.Count != 2 || e.Column != some(0x348) || !e.ErrorDT.Equal(utc(2023, 2, 27, 6, 25, 14)) {
		t.Errorf("L13: %+v", *e)
	}
	if a := c.Actions[0]; a.Method != "hard" || a.Result != model.ResultOK || !a.At.Equal(utc(2023, 2, 27, 3, 14, 7)) {
		t.Errorf("action: %+v", *a)
	}
	// Stamps starting over mark the second boot.
	b1, b2 := c.Boots[0], c.Boots[1]
	if !near(b1.StartedAt, utc(2023, 2, 13, 0, 0, 0), time.Second) || b1.Kernel != "" || b1.StartQuality != model.TimeExact {
		t.Errorf("first boot: %+v", *b1)
	}
	if !near(b2.StartedAt, utc(2023, 2, 27, 6, 10, 0), time.Second) || b2.Kernel != "4.18.0-553.el8_10.x86_64" || b2.BIOS != "3.8b 01/27/2023" {
		t.Errorf("second boot: %+v", *b2)
	}
}

func TestJournalJSONFromAMDHost(t *testing.T) {
	c, rep, _ := collectFile(t, "amd.journal.json", Options{})
	const bootA, bootB = "6f1c0a8e2b7d4f0e9a3b5c7d9e1f2a3b", "0d9e8f7a6b5c4d3e2f1a0b9c8d7e6f5a"

	if rep.Status != model.SourcePartial || rep.Records != 11 || rep.Skipped != 1 || rep.HostHint != "epyc-a17" {
		t.Errorf("report = %+v, want partial, 11 records, 1 skipped, host epyc-a17", rep)
	}
	if !hasNote(rep, "#L20: journal entry is not valid JSON") || !hasNote(rep, "1 machine checks are not memory errors") {
		t.Errorf("notes = %q", rep.Notes)
	}
	if len(c.Events) != 4 || len(c.Actions) != 2 || len(c.Boots) != 2 {
		t.Fatalf("%d events, %d actions, %d boots; want 4, 2, 2", len(c.Events), len(c.Actions), len(c.Boots))
	}

	// amd64_edac prints its line in the middle of the machine check decode.
	ed := at(t, c, "L10")
	if ed.Source != "klog/edac" || ed.ChannelId != some(1) || ed.ChannelSlotId != some(1) || ed.Rank != some(0) || ed.EdacLocation != "2:1" ||
		ed.OsPhysicalAddress != addr(0x7f5d2c9c0) || ed.Syndrome != model.Some(uint64(4)) || ed.Decoder != edac.DecoderCSROW ||
		ed.Grade != model.GradeB || ed.BootID != bootA || !near(ed.ErrorDT, utc(2024, 6, 4, 3, 10, 41), time.Second) {
		t.Errorf("EDAC line: %+v", *ed)
	}
	mc := at(t, c, "L5-L11")
	if mc.Source != "klog/mce" || mc.DupOfFingerprint != ed.Fingerprint || mc.OsPhysicalAddress.Valid || mc.ChannelId != some(1) ||
		mc.ChannelSlotId != some(1) || mc.Rank != some(0) || mc.Decoder != "mce/amd" || mc.ErrorSeverity != model.SevCE {
		t.Errorf("machine check: %+v", *mc)
	}
	// A message that is not valid UTF-8 arrives as an array of byte values.
	b := at(t, c, "L18")
	if b.Label != "P0_DIMM_�" || b.ChannelId != some(3) || b.ChannelSlotId != some(2) || b.Rank != some(1) ||
		b.OsPhysicalAddress != addr(0x1a3c001c0) || b.Syndrome != model.Some(uint64(0x2a01)) {
		t.Errorf("byte array message: %+v", *b)
	}
	ue := at(t, c, "L23")
	if ue.ErrorSeverity != model.SevUE || ue.BootID != bootB || ue.Grade != model.GradeB {
		t.Errorf("uncorrected event of the second boot: %+v", *ue)
	}
	for _, e := range c.Events {
		if strings.Contains(e.Raw, "rasdaemon:") {
			t.Errorf("a message of another program was read as a kernel message: %q", e.Raw)
		}
	}

	b1, b2 := c.Boots[0], c.Boots[1]
	// The first messages were read by journald 4.5 s after boot; the boot
	// time must not be late by that much.
	if b1.BootID != bootA || b1.Derived || !b1.StartedAt.Equal(utc(2024, 6, 1, 0, 0, 0)) || b1.Kernel != "5.14.0-427.13.1.el9_4.x86_64" ||
		b1.BIOS != "1.6 12/21/2023" || b1.StartQuality != model.TimeExact {
		t.Errorf("first boot: %+v", *b1)
	}
	if b2.BootID != bootB || !b2.StartedAt.Equal(utc(2024, 6, 5, 12, 0, 0)) {
		t.Errorf("second boot: %+v", *b2)
	}
	if a := c.Actions[0]; a.Method != "soft" || a.BootID != bootB || a.OsPhysicalAddress != addr(0x7f5d2c000) {
		t.Errorf("first action: %+v", *a)
	}
	if a := c.Actions[1]; a.Method != "hard" || a.Result != model.ResultOK {
		t.Errorf("second action: %+v", *a)
	}
}

func TestFirmwareFirstOnKernel6(t *testing.T) {
	captured := utc(2026, 10, 1, 3, 0, 0)
	c, rep, _ := collectFile(t, "ghes-6.x.dmesg", Options{CapturedAt: captured})
	boot := captured.Add(-(9000*time.Second + 12*time.Microsecond))

	if rep.Status != model.SourceOK || rep.Records != 9 || rep.Skipped != 0 {
		t.Errorf("report = %+v, want ok, 9 records", rep)
	}
	if !hasNote(rep, "2 firmware error sections are not memory errors") {
		t.Errorf("notes = %q", rep.Notes)
	}
	if len(c.Events) != 5 || len(c.Actions) != 1 || len(c.Boots) != 1 {
		t.Fatalf("%d events, %d actions, %d boots; want 5, 1, 1", len(c.Events), len(c.Actions), len(c.Boots))
	}

	g := at(t, c, "L6-L17")
	if g.Source != "klog/ghes" || g.Grade != model.GradeA || g.ErrorSeverity != model.SevCE || g.SocketId != some(0) || g.ChannelId != some(1) ||
		g.ChannelSlotId != some(0) || g.Rank != some(1) || g.BankGroup != some(1) || g.Bank != some(3) || g.DeviceId != some(7) ||
		g.Row != some(6703) || g.Column != some(1016) || g.ErrorVector != "bit:5" || g.OsPhysicalAddress != addr(0xb5689dc40) ||
		g.Label != "NODE 1 CPU1_DIMM_B1" || g.ErrorType != "single-bit ECC" || g.Decoder != "cper" || g.TimeQuality != model.TimeEstimated ||
		!g.ErrorDT.Equal(boot.Add(3600*time.Second+100001*time.Microsecond)) || strings.Count(g.Raw, "\n") != 11 {
		t.Errorf("GHES block: %+v", *g)
	}
	// ghes_edac reports the same error right after the block.
	ed := at(t, c, "L18")
	if ed.DupOfFingerprint != g.Fingerprint || ed.Decoder != edac.DecoderGHES || ed.Row != some(6703) || ed.Column != some(1016) ||
		ed.DeviceId != some(7) || ed.OsPhysicalAddress != addr(0xb5689dc40) || ed.Label != "NODE 1 CPU1_DIMM_B1" || ed.EdacLocation != "" {
		t.Errorf("ghes_edac line: %+v", *ed)
	}
	// A ghes_edac line without a block before it (the block print is rate limited) stands on its own.
	if e := at(t, c, "L19"); e.DupOfFingerprint != "" || e.ErrorScrubType != model.ScrubPatrol || e.Column != some(1040) {
		t.Errorf("L19: %+v", *e)
	}
	g2 := at(t, c, "L20-L31")
	if g2.ErrorSeverity != model.SevRecoverable || g2.ErrorScrubType != model.ScrubPatrol || g2.Grade != model.GradeB || g2.SocketId != some(1) ||
		g2.ChannelId != some(2) || g2.ChannelSlotId != some(1) || g2.SMBIOSHandle != some(0x1a) || g2.Row.Valid || g2.Label != "" {
		t.Errorf("second GHES block: %+v", *g2)
	}
	if e := at(t, c, "L32"); e.DupOfFingerprint != g2.Fingerprint || e.ErrorSeverity != model.SevUE || e.SMBIOSHandle != some(0x1a) || e.Label != "" {
		t.Errorf("L32: %+v", *e)
	}
	if a := c.Actions[0]; a.Result != model.ResultPending || a.OsPhysicalAddress != addr(0x12f4a6000) || a.TimeQuality != model.TimeEstimated {
		t.Errorf("action: %+v", *a)
	}
	if b := c.Boots[0]; !b.StartedAt.Equal(boot) || b.StartQuality != model.TimeEstimated || b.Kernel != "6.12.0-55.9.1.el10_0.aarch64" ||
		!strings.HasPrefix(b.Cmdline, "BOOT_IMAGE=") {
		t.Errorf("boot: %+v", *b)
	}
}

func TestFirmwareFirstOnKernel418With64KPages(t *testing.T) {
	c, rep, _ := collectFile(t, "ghes-4.18.dmesg-T", Options{})

	if rep.Status != model.SourceOK || rep.Records != 9 || rep.Skipped != 0 {
		t.Errorf("report = %+v, want ok, 9 records", rep)
	}
	if len(c.Events) != 5 || len(c.Actions) != 1 || len(c.Boots) != 2 {
		t.Fatalf("%d events, %d actions, %d boots; want 5, 1, 2", len(c.Events), len(c.Actions), len(c.Boots))
	}
	// 4.x prints the location with a space after each colon, in decimal.
	g := at(t, c, "L4-L14")
	if g.Grade != model.GradeA || g.Row != some(6703) || g.Column != some(1016) || g.Bank != some(3) || g.BankGroup.Valid || g.DeviceId != some(7) ||
		g.OsPhysicalAddress != addr(0xb5689dc40) || g.Label != "NODE 1 DIMM_B1" || !g.ErrorDT.Equal(utc(2023, 2, 27, 14, 36, 26)) || g.TimeQuality != model.TimeExact {
		t.Errorf("GHES block: %+v", *g)
	}
	// ghes_edac of 4.x puts the fields inside the parentheses only, with
	// col and bit_pos, and the kernel splits the address at 64 KiB.
	ed := at(t, c, "L15")
	if ed.DupOfFingerprint != g.Fingerprint || ed.Decoder != edac.DecoderGHES || ed.Row != some(6703) || ed.Column != some(1016) ||
		ed.ErrorVector != "bit:5" || ed.OsPhysicalAddress != addr(0xb5689dc40) || ed.Label != "NODE 1 DIMM_B1" || ed.DeviceId.Valid {
		t.Errorf("ghes_edac line: %+v", *ed)
	}
	if e := at(t, c, "L16"); e.DupOfFingerprint != "" || e.OsPhysicalAddress != addr(0xb5689dc40) {
		t.Errorf("L16 (a later error at the same address): %+v", *e)
	}
	if e := at(t, c, "L17"); e.Label != "" || e.SMBIOSHandle != some(0x1a) || e.Row != some(12) || e.Column != some(8) || e.ErrorVector != "bit:0" ||
		e.OsPhysicalAddress != addr(0xb50000040) {
		t.Errorf("L17: %+v", *e)
	}
	if e := at(t, c, "L19"); e.ErrorSeverity != model.SevUE || !e.ErrorDT.Equal(utc(2023, 2, 28, 9, 30, 0)) {
		t.Errorf("L19: %+v", *e)
	}
	if a := c.Actions[0]; a.OsPhysicalAddress != addr(0xb56890000) {
		t.Errorf("page offline address with 64 KiB pages = %v", a.OsPhysicalAddress)
	}
	// Without stamps a second banner is the only sign of a reboot.
	if !c.Boots[0].StartedAt.Equal(utc(2023, 2, 13, 0, 0, 0)) || !c.Boots[1].StartedAt.Equal(utc(2023, 2, 28, 8, 0, 0)) ||
		c.Boots[0].BIOS != "F21 03/02/2022" || c.Boots[1].Kernel != "4.18.0-553.el8_10.aarch64" {
		t.Errorf("boots: %+v %+v", *c.Boots[0], *c.Boots[1])
	}
	// The zone of times without one is the caller's to say.
	seoul := time.FixedZone("KST", 9*3600)
	c2, _, _ := collectFile(t, "ghes-4.18.dmesg-T", Options{Location: seoul})
	if e := at(t, c2, "L4-L14"); !e.ErrorDT.Equal(utc(2023, 2, 27, 5, 36, 26)) {
		t.Errorf("with Location UTC+9: %v", e.ErrorDT)
	}
}

func TestKmsgRecords(t *testing.T) {
	boot := utc(2026, 9, 1, 0, 0, 0)
	c, rep, _ := collectFile(t, "kmsg.txt", Options{BootTime: boot, BootID: "feedfacefeedfacefeedfacefeedface"})
	if rep.Status != model.SourceOK || rep.Records != 5 || len(c.Events) != 1 || len(c.Actions) != 1 || len(c.Boots) != 1 {
		t.Fatalf("report %+v, %d events, %d actions, %d boots", rep, len(c.Events), len(c.Actions), len(c.Boots))
	}
	e := c.Events[0]
	if !e.ErrorDT.Equal(boot.Add(8041118302*time.Microsecond)) || e.TimeQuality != model.TimeExact || e.Decoder != edac.DecoderSKX ||
		e.SocketId != some(0) || e.IMC != some(1) || e.ChannelId != some(0) || e.ChannelSlotId != some(0) || e.Row != some(0x1a2f) ||
		e.Column != some(0x3f8) || e.Bank != some(3) || e.Grade != model.GradeA {
		t.Errorf("event: %+v", *e)
	}
	if b := c.Boots[0]; b.BootID != "feedfacefeedfacefeedfacefeedface" || b.Derived || b.Kernel != "6.8.0-45-generic" || b.BIOS != "2.1 12/05/2023" {
		t.Errorf("boot: %+v", *b)
	}
}

// The same kernel message read from dmesg, from /dev/kmsg and from the
// journal must be one record in the ledger.
func TestFingerprintIsTheSameAcrossFormats(t *testing.T) {
	const msg = `EDAC MC0: 1 CE memory read error on DIMM_A1 (channel:0 slot:0 page:0x1000 offset:0x40 grain:32 syndrome:0x0)`
	inputs := map[string]string{
		"dmesg":        "[ 1234.567890] " + msg + "\n",
		"dmesg raw":    "<4>[ 1234.567890] " + msg + "\n",
		"kmsg":         "4,99,1234567890,-;" + msg + "\n",
		"syslog":       "Nov 12 03:14:07 host kernel: [ 1234.567890] " + msg + "\n",
		"syslog iso":   "2026-11-12T03:14:07.123456+09:00 host kernel: [ 1234.567890] " + msg + "\n",
		"journal json": `{"MESSAGE":"` + msg + `","_TRANSPORT":"kernel","_BOOT_ID":"aa","__REALTIME_TIMESTAMP":"1794420847000000","__MONOTONIC_TIMESTAMP":"1234568000","_SOURCE_MONOTONIC_TIMESTAMP":"1234567890"}` + "\n",
	}
	var want string
	for name, in := range inputs {
		c, rep := collectText(t, in, Options{CapturedAt: utc(2026, 11, 13, 0, 0, 0)})
		if len(c.Events) != 1 {
			t.Fatalf("%s: %d events (%+v)", name, len(c.Events), rep)
		}
		if want == "" {
			want = c.Events[0].Fingerprint
		}
		if c.Events[0].Fingerprint != want {
			t.Errorf("%s: fingerprint differs from the other formats", name)
		}
	}
	// Another stamp or another address is another record.
	c, _ := collectText(t, "[ 1234.567891] "+msg+"\n", Options{})
	if c.Events[0].Fingerprint == want {
		t.Error("a different stamp gave the same fingerprint")
	}
}

func TestInputsThatAreNotKernelLogs(t *testing.T) {
	dir := t.TempDir()
	t.Run("missing file", func(t *testing.T) {
		var c adapter.Collected
		rep := NewFile(filepath.Join(dir, "absent"), Options{}).Collect(ctx, &c)
		if rep.Status != model.SourceAbsent {
			t.Errorf("report = %+v", rep)
		}
	})
	t.Run("directory", func(t *testing.T) {
		var c adapter.Collected
		rep := NewFile(dir, Options{}).Collect(ctx, &c)
		if rep.Status != model.SourceError || !strings.Contains(rep.Detail, "is a directory") {
			t.Errorf("report = %+v", rep)
		}
	})
	t.Run("empty input", func(t *testing.T) {
		c, rep := collectText(t, "", Options{})
		if rep.Status != model.SourceEmpty || len(c.Events) != 0 || len(c.Boots) != 0 {
			t.Errorf("report = %+v", rep)
		}
	})
	t.Run("binary data", func(t *testing.T) {
		junk := make([]byte, 300000)
		for i := range junk {
			junk[i] = byte(i*7 + i/3)
		}
		c, rep := collectText(t, string(junk), Options{})
		if rep.Status != model.SourceEmpty || len(c.Events) != 0 {
			t.Errorf("report = %+v, %d events", rep, len(c.Events))
		}
	})
	t.Run("a log without memory errors", func(t *testing.T) {
		c, rep := collectText(t, "[    0.000000] Linux version 6.1.0\n[    1.000000] usb 1-1: new high-speed USB device\n", Options{})
		if rep.Status != model.SourceOK || rep.Records != 1 || len(c.Events) != 0 || len(c.Boots) != 1 {
			t.Errorf("report = %+v, %d events, %d boots", rep, len(c.Events), len(c.Boots))
		}
	})
	t.Run("a line longer than 1 MiB is skipped, the rest is read", func(t *testing.T) {
		long := "[    5.000000] EDAC MC0: 1 CE " + strings.Repeat("x", 2<<20) + "\n"
		c, rep := collectText(t, long+shortDmesg, Options{})
		if rep.Status != model.SourcePartial || rep.Skipped != 1 || len(c.Events) != 1 || !hasNote(rep, "#L1: line is longer than 1 MiB") {
			t.Errorf("report = %+v, %d events", rep, len(c.Events))
		}
	})
	t.Run("no line end at the end of the input", func(t *testing.T) {
		c, _ := collectText(t, strings.TrimRight(shortDmesg, "\n"), Options{})
		if len(c.Events) != 1 || len(c.Actions) != 1 {
			t.Errorf("%d events, %d actions", len(c.Events), len(c.Actions))
		}
	})
	t.Run("Windows line ends", func(t *testing.T) {
		c, _ := collectText(t, strings.ReplaceAll(shortDmesg, "\n", "\r\n"), Options{})
		if len(c.Events) != 1 || c.Events[0].Column != some(8) {
			t.Errorf("events = %+v", c.Events)
		}
	})
}

func TestValuesOutOfRangeAreSkipped(t *testing.T) {
	const tail = " EDAC MC0: 1 CE memory read error on DIMM_A1 (channel:0 slot:0 page:%s offset:0x40 grain:32 syndrome:0x0)\n"
	tests := []struct {
		name   string
		in     string
		events int
		note   string
	}{
		{"a page number that is not an address", "[  100.000000]" + strings.Replace(tail, "%s", "0xffffffffffffffff", 1), 0, "page or offset is out of range"},
		{"an offset beyond any page size", "[  100.000000] EDAC MC0: 1 CE on DIMM_A1 (channel:0 slot:0 page:0x1000 offset:0x123456 grain:32 syndrome:0x0)\n", 0, "page or offset is out of range"},
		// A stamp of 300 years is not an uptime: the line is read as a bare
		// message, without a time.
		{"a stamp that is not an uptime", "[9999999999.000000]" + strings.Replace(tail, "%s", "0x1000", 1), 1, ""},
		{"a journal time beyond year 9999", `{"MESSAGE":"EDAC MC0: 1 CE on DIMM_A1 (channel:0 slot:0 page:0x1000 offset:0x40 grain:32 syndrome:0x0)","_TRANSPORT":"kernel","__REALTIME_TIMESTAMP":"9223372036854775807"}` + "\n", 0, "time is out of range"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, rep := collectText(t, tt.in, Options{CapturedAt: utc(2026, 11, 13, 0, 0, 0)})
			if len(c.Events) != tt.events || (tt.note != "" && !hasNote(rep, tt.note)) {
				t.Errorf("%d events, notes %q; want %d events and a note with %q", len(c.Events), rep.Notes, tt.events, tt.note)
			}
			for _, e := range c.Events {
				if y := e.ErrorDT.Year(); y < 1970 || y > 9999 {
					t.Errorf("event time %v", e.ErrorDT)
				}
			}
		})
	}
}

type failingSink struct {
	adapter.Collected
	err error
}

func (f *failingSink) Event(*model.Event) error { return f.err }

func TestSinkErrorAndCancel(t *testing.T) {
	boom := errors.New("ledger is full")
	rep := NewReader(strings.NewReader(shortDmesg), "test", Options{CapturedAt: utc(2026, 1, 1, 0, 0, 0)}).Collect(ctx, &failingSink{err: boom})
	if rep.Status != model.SourceError || !errors.Is(rep.SinkErr, boom) {
		t.Errorf("report = %+v", rep)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	var c adapter.Collected
	rep = NewReader(strings.NewReader(shortDmesg), "test", Options{}).Collect(cctx, &c)
	if rep.Status != model.SourceError || len(c.Events) != 0 {
		t.Errorf("cancelled: report = %+v, %d events", rep, len(c.Events))
	}
}

// TestOtherDMILinesAreNotTheMachine reads the boot banner of a kernel that
// prints more than one line starting with "DMI:". Linux 6.8 and later say
// how many memory slots are populated, right after the line that
// identifies the machine. Seen on a real host: the second line replaced
// the name of the machine.
func TestOtherDMILinesAreNotTheMachine(t *testing.T) {
	c, rep := collectText(t, strings.Join([]string{
		"[    0.000000] Linux version 6.11.0-1018-azure (buildd@lcy02-amd64-086) (x86_64-linux-gnu-gcc-13 (Ubuntu 13.3.0-6ubuntu2~24.04) 13.3.0) #18~24.04.1-Ubuntu SMP",
		"[    0.000000] Command line: BOOT_IMAGE=/boot/vmlinuz-6.11.0-1018-azure root=/dev/sda1 ro console=tty1 console=ttyS0",
		"[    0.016483] DMI: Microsoft Corporation Virtual Machine/Virtual Machine, BIOS Hyper-V UEFI Release v4.1 08/23/2024",
		"[    0.016493] DMI: Memory slots populated: 2/2",
		"",
	}, "\n"), Options{BootTime: time.Date(2026, 11, 15, 4, 0, 0, 0, time.UTC)})
	if rep.Status != model.SourceOK {
		t.Fatalf("report = %+v", rep)
	}
	if len(c.Hosts) != 1 || c.Hosts[0].SystemProduct != "Microsoft Corporation Virtual Machine/Virtual Machine" {
		t.Errorf("hosts = %+v", c.Hosts)
	}
	if len(c.Boots) != 1 || c.Boots[0].BIOS != "Hyper-V UEFI Release v4.1 08/23/2024" || c.Boots[0].Kernel != "6.11.0-1018-azure" {
		t.Errorf("boots = %+v", c.Boots)
	}
}
