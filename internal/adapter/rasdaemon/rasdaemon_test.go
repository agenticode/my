package rasdaemon

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/decode/edac"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/testutil"
)

var ctx = context.Background()

func some(v int) model.Opt[int] { return model.Some(v) }

func addr(v uint64) model.Opt[uint64] { return model.Some(v) }

var none model.Opt[int]

// collect builds the fixture database and reads it both ways: in place and
// through a snapshot. Both must give the same result.
func collect(t *testing.T, fixture string) (*adapter.Collected, adapter.Report, string) {
	t.Helper()
	path := testutil.BuildSQLite(t, testutil.Testdata(t, "rasdaemon", fixture), "ras-mc_event.db")
	var direct, snap adapter.Collected
	repD := New(path, Options{Direct: true}).Collect(ctx, &direct)
	repS := New(path, Options{TempDir: t.TempDir()}).Collect(ctx, &snap)
	if repD.Status != repS.Status || repD.Records != repS.Records || repD.Skipped != repS.Skipped ||
		len(direct.Events) != len(snap.Events) || len(direct.Actions) != len(snap.Actions) {
		t.Fatalf("direct and snapshot reads differ:\n direct: %+v (%d events)\n snapshot: %+v (%d events)",
			repD, len(direct.Events), repS, len(snap.Events))
	}
	for i := range direct.Events {
		if *direct.Events[i] != *snap.Events[i] {
			t.Fatalf("event %d differs between direct and snapshot read", i)
		}
	}
	return &direct, repD, path
}

func find(t *testing.T, c *adapter.Collected, ref string) *model.Event {
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

func utc(y int, mo time.Month, d, h, mi, s int) time.Time {
	return time.Date(y, mo, d, h, mi, s, 0, time.UTC)
}

func TestIntelSKXFromRasdaemon06(t *testing.T) {
	c, rep, path := collect(t, "intel-skx-0.6.sql")

	if rep.Status != model.SourcePartial || rep.Records != 14 || rep.Skipped != 3 {
		t.Errorf("report = %+v, want partial, 14 records, 3 skipped", rep)
	}
	for _, want := range []string{
		"mce_record: 1 machine checks are not memory errors",
		"table aer_event: 2 rows not read (PCIe errors)",
		"mc_event/6: timestamp is empty or unreadable",
		`mc_event/7: unknown error type "Unknown"`,
		"mce_record/6: machine check status register is not valid",
	} {
		if !hasNote(rep, want) {
			t.Errorf("notes lack %q: %q", want, rep.Notes)
		}
	}
	if len(c.Events) != 10 || len(c.Actions) != 0 {
		t.Fatalf("%d events, %d actions; want 10, 0", len(c.Events), len(c.Actions))
	}

	// The 4.18 driver prints row and column as bare hexadecimal.
	e := find(t, c, "mc_event/1")
	want := model.Event{
		Kind: model.KindError, ErrorDT: utc(2023, 2, 27, 6, 36, 26), ErrorSeverity: model.SevCE, ErrorScrubType: model.ScrubDemand,
		SocketId: some(0), ChannelId: some(1), ChannelSlotId: some(0), Rank: some(1), BankGroup: some(1), Bank: some(3),
		Row: some(0x1b8b), Column: some(0x338), OsPhysicalAddress: addr(0x1f8bd19c0),
		Source: "rasdaemon/mc_event", RawRef: path + "#mc_event/1", Grade: model.GradeA, Count: 1,
		TimeQuality: model.TimeExact, UTCOffset: some(8 * 3600), IMC: some(0), EdacMC: some(0), EdacLocation: "1:0",
		Label: "CPU_SrcID#0_MC#0_Chan#1_DIMM#0", ErrorType: "memory read error", Decoder: edac.DecoderSKX,
		Raw: e.Raw, Fingerprint: e.Fingerprint,
	}
	if *e != want {
		t.Errorf("mc_event/1\n got: %+v\nwant: %+v", *e, want)
	}
	if !strings.Contains(e.Raw, `"driver_detail":" err_code:0101:0091 socket:0 imc:0 rank:1 bg:1 ba:3 row:1b8b col:338"`) ||
		!strings.Contains(e.Raw, `"id":1`) {
		t.Errorf("raw record does not hold the original row: %s", e.Raw)
	}

	if e := find(t, c, "mc_event/3"); e.Count != 3 || e.Column != some(0x200) {
		t.Errorf("mc_event/3: count %d column %v, want 3 and 0x200", e.Count, e.Column)
	}
	if e := find(t, c, "mc_event/4"); e.ErrorSeverity != model.SevRecoverable || e.ErrorScrubType != model.ScrubPatrol || e.Grade != model.GradeA {
		t.Errorf("mc_event/4: %s %s %s, want Recoverable Patrol A", e.ErrorSeverity, e.ErrorScrubType, e.Grade)
	}
	if e := find(t, c, "mc_event/5"); e.SocketId != some(1) || e.IMC != some(1) || e.ChannelId != some(2) || e.ChannelSlotId != some(1) ||
		e.Row != some(0x1000) || e.Column != some(0x10) || e.UTCOffset != some(0) || e.EdacMC != some(3) || e.EdacLocation != "2:1" {
		t.Errorf("mc_event/5: %+v", *e)
	}
	if e := find(t, c, "mc_event/8"); e.ErrorSeverity != model.SevInformational || e.Label != "" || e.Grade != model.GradeC ||
		e.OsPhysicalAddress.Valid || e.EdacLocation != "" {
		t.Errorf("mc_event/8 (informational, no DIMM): %+v", *e)
	}

	// Machine checks are tied to the EDAC record of the same error, also
	// when two errors share a second.
	for mceRef, mcRef := range map[string]string{"mce_record/1": "mc_event/1", "mce_record/2": "mc_event/2", "mce_record/3": "mc_event/4"} {
		m, mc := find(t, c, mceRef), find(t, c, mcRef)
		if m.DupOfFingerprint != mc.Fingerprint {
			t.Errorf("%s is not declared a duplicate of %s", mceRef, mcRef)
		}
	}
	if m := find(t, c, "mce_record/3"); m.ErrorSeverity != model.SevRecoverable || m.ErrorScrubType != model.ScrubPatrol {
		t.Errorf("mce_record/3 (SRAO): %s %s", m.ErrorSeverity, m.ErrorScrubType)
	}
	// A memory machine check without an EDAC record stands on its own.
	m := find(t, c, "mce_record/4")
	if m.DupOfFingerprint != "" || m.Grade != model.GradeB || m.OsPhysicalAddress != addr(0x2a0000040) || m.ChannelId != some(0) ||
		m.SocketId != some(0) || m.Decoder != "mce/intel" || m.ErrorSeverity != model.SevCE || m.ErrorScrubType != model.ScrubDemand {
		t.Errorf("mce_record/4: %+v", *m)
	}
	for _, e := range c.Events {
		if strings.HasSuffix(e.RawRef, "mce_record/5") {
			t.Error("a cache error was turned into a memory event")
		}
	}
}

func TestAMDFromUpgradedDatabase(t *testing.T) {
	c, rep, _ := collect(t, "amd-upgraded-0.8.sql")

	if rep.Status != model.SourcePartial || rep.Records != 17 || rep.Skipped != 1 {
		t.Errorf("report = %+v, want partial, 17 records, 1 skipped", rep)
	}
	for _, want := range []string{
		"mce_record: 1 machine checks are not memory errors",
		"table aer_event: 1 rows not read", "table cxl_aer_ce_event: 1 rows not read", "table disk_errors: 1 rows not read",
		`memory_failure_event/4: page frame number "n/a" is unreadable`,
	} {
		if !hasNote(rep, want) {
			t.Errorf("notes lack %q: %q", want, rep.Notes)
		}
	}
	if len(c.Events) != 12 || len(c.Actions) != 3 {
		t.Fatalf("%d events, %d actions; want 12, 3", len(c.Events), len(c.Actions))
	}

	// amd64_edac: channel and DIMM from the chip-select layers, no row or column.
	e := find(t, c, "mc_event/1")
	if e.ChannelId != some(1) || e.ChannelSlotId != some(1) || e.Rank != some(0) || e.Row.Valid || e.Column.Valid ||
		e.OsPhysicalAddress != addr(0x7f5d2c9c0) || e.Syndrome != model.Some(uint64(4)) || e.EdacLocation != "2:1" ||
		e.Decoder != edac.DecoderCSROW || e.Grade != model.GradeB || e.UTCOffset != some(-5*3600) ||
		!e.ErrorDT.Equal(utc(2024, 6, 4, 3, 10, 41)) || e.ErrorScrubType != "" {
		t.Errorf("mc_event/1: %+v", *e)
	}
	// A label set by the administrator says nothing about the position.
	if e := find(t, c, "mc_event/3"); e.Label != "P0_DIMM_F1" || e.ChannelId.Valid || e.EdacLocation != "5:3" || e.Grade != model.GradeB {
		t.Errorf("mc_event/3: %+v", *e)
	}
	if e := find(t, c, "mc_event/4"); e.ErrorSeverity != model.SevRecoverable {
		t.Errorf("deferred error severity = %s, want Recoverable", e.ErrorSeverity)
	}
	if e := find(t, c, "mc_event/5"); e.Grade != model.GradeC || e.Label != "" || e.OsPhysicalAddress.Valid ||
		e.ErrorType != "Failed to map error addr to a csrow" {
		t.Errorf("mc_event/5 (address not mapped): %+v", *e)
	}
	if e := find(t, c, "mc_event/6"); e.ErrorSeverity != model.SevFatal || e.EdacMC != some(1) {
		t.Errorf("mc_event/6: %+v", *e)
	}

	// The machine check address of the AMD memory controller is not a
	// system address: it must not be stored as one, and the match with the
	// EDAC record goes by channel and chip select.
	for mceRef, mcRef := range map[string]string{"mce_record/1": "mc_event/1", "mce_record/2": "mc_event/2"} {
		m, mc := find(t, c, mceRef), find(t, c, mcRef)
		if m.OsPhysicalAddress.Valid {
			t.Errorf("%s: normalized address stored as OsPhysicalAddress", mceRef)
		}
		if m.DupOfFingerprint != mc.Fingerprint {
			t.Errorf("%s is not declared a duplicate of %s", mceRef, mcRef)
		}
		if m.ChannelId != some(1) || m.ChannelSlotId != some(1) || m.Rank != some(0) || m.Grade != model.GradeB || m.Decoder != "mce/amd" {
			t.Errorf("%s: %+v", mceRef, *m)
		}
	}
	if m := find(t, c, "mce_record/3"); m.DupOfFingerprint != "" || m.ChannelId != some(3) || m.ChannelSlotId != some(0) || m.Rank != some(1) ||
		m.ErrorType != "DRAM ECC error" {
		t.Errorf("mce_record/3: %+v", *m)
	}

	// Pages taken offline by the kernel.
	want := []struct {
		addr   uint64
		result model.ActionResult
		detail string
	}{
		{0x7f5d2c000, model.ResultOK, "dirty LRU page: Recovered"},
		{0x7f5d2d000, model.ResultPending, "free buddy page: Delayed"},
		{0x9e0040000, model.ResultFailed, "kernel slab page: Failed"},
	}
	for i, w := range want {
		a := c.Actions[i]
		if a.Kind != model.ActionPageOffline || a.Method != "hard" || a.OsPhysicalAddress != addr(w.addr) || a.Result != w.result ||
			a.Detail != w.detail || a.Source != "rasdaemon/memory_failure_event" || a.Fingerprint == "" || a.Raw == "" {
			t.Errorf("action %d: %+v", i, *a)
		}
	}

	// CXL DRAM events carry the full location when the device reported it.
	d := find(t, c, "cxl_dram_event/1")
	if d.Grade != model.GradeA || d.BankGroup != some(1) || d.Bank != some(2) || d.Row != some(0x1a2f) || d.Column != some(0x3f8) ||
		d.ChannelId != some(0) || d.Rank != some(1) || d.ErrorVector != "nibble:0x4" || d.OsPhysicalAddress != addr(0x2c0018680) ||
		d.ErrorSeverity != model.SevCE || d.ErrorScrubType != model.ScrubDemand || d.Label != "cxl:mem0" || d.Decoder != "cxl" {
		t.Errorf("cxl_dram_event/1: %+v", *d)
	}
	// Zeros in every location column mean "not reported", not row 0.
	d = find(t, c, "cxl_dram_event/2")
	if d.Grade != model.GradeB || d.Row.Valid || d.Bank.Valid || d.ErrorSeverity != model.SevUE || d.ErrorScrubType != model.ScrubPatrol {
		t.Errorf("cxl_dram_event/2: %+v", *d)
	}
	g := find(t, c, "cxl_general_media_event/1")
	if g.OsPhysicalAddress.Valid || g.ChannelId != some(1) || g.Rank != some(0) || g.DeviceId != some(3) || g.Label != "cxl:mem1" {
		t.Errorf("cxl_general_media_event/1: %+v", *g)
	}
}

func TestFirmwareFirstFromRasdaemon10(t *testing.T) {
	c, rep, _ := collect(t, "ghes-1.0.sql")

	if rep.Status != model.SourceOK || rep.Records != 11 || rep.Skipped != 0 {
		t.Errorf("report = %+v, want ok, 11 records, none skipped", rep)
	}
	if !hasNote(rep, "extlog_event: 1 records have a CPER blob that could not be decoded") || !hasNote(rep, "table signal_event: 1 rows not read") {
		t.Errorf("notes = %q", rep.Notes)
	}
	if len(c.Events) != 10 || len(c.Actions) != 1 {
		t.Fatalf("%d events, %d actions; want 10, 1", len(c.Events), len(c.Actions))
	}

	// ghes_edac repeats the firmware's fields in decimal.
	e := find(t, c, "mc_event/1")
	if e.Grade != model.GradeA || e.SocketId != some(0) || e.ChannelId != some(1) || e.ChannelSlotId != some(0) || e.Rank != some(1) ||
		e.DeviceId != some(7) || e.BankGroup != some(1) || e.Bank != some(3) || e.Row != some(6703) || e.Column != some(1016) ||
		e.ErrorVector != "bit:5" || e.Label != "NODE 1 CPU1_DIMM_B1" || e.Decoder != edac.DecoderGHES || e.EdacLocation != "9" ||
		e.ErrorScrubType != "" || e.OsPhysicalAddress != addr(0xb5689dc40) || !e.ErrorDT.Equal(utc(2026, 10, 2, 0, 15, 30)) {
		t.Errorf("mc_event/1: %+v", *e)
	}
	if e := find(t, c, "mc_event/2"); e.ErrorScrubType != model.ScrubPatrol || e.Column != some(1040) {
		t.Errorf("mc_event/2 (scrub corrected error): %+v", *e)
	}
	if e := find(t, c, "mc_event/3"); e.Grade != model.GradeB || e.SMBIOSHandle != some(0x1a) || e.Label != "" || e.SocketId != some(1) ||
		e.ChannelSlotId != some(1) || e.Row.Valid {
		t.Errorf("mc_event/3 (module only): %+v", *e)
	}
	if e := find(t, c, "mc_event/4"); e.Grade != model.GradeC || e.Count != 2 {
		t.Errorf("mc_event/4 (nothing but a count): %+v", *e)
	}
	if e := find(t, c, "mc_event/5"); e.ErrorSeverity != model.SevUE || e.ErrorScrubType != model.ScrubPatrol {
		t.Errorf("mc_event/5: %s %s, want UE Patrol", e.ErrorSeverity, e.ErrorScrubType)
	}
	if e := find(t, c, "mc_event/6"); e.ErrorSeverity != model.SevFatal || e.ErrorVector != "" {
		t.Errorf("mc_event/6: %+v", *e)
	}

	// The extlog table holds the firmware record in compact binary form.
	x := find(t, c, "extlog_event/1")
	if x.Grade != model.GradeA || x.SocketId != some(0) || x.ChannelId != some(1) || x.ChannelSlotId != some(0) || x.Rank != some(1) ||
		x.DeviceId != some(7) || x.BankGroup != some(1) || x.Bank != some(3) || x.Row != some(6703) || x.Column != some(1016) ||
		x.ErrorVector != "bit:5" || x.SMBIOSHandle != some(0x1100) || x.Label != "CPU1_DIMM_B1" || x.ErrorSeverity != model.SevCE ||
		x.ErrorType != "single-bit ECC" || x.OsPhysicalAddress != addr(0xb5689dc40) || x.Decoder != "cper" || x.Count != 1 {
		t.Errorf("extlog_event/1: %+v", *x)
	}
	if x := find(t, c, "extlog_event/2"); x.Grade != model.GradeB || x.ErrorSeverity != model.SevRecoverable || x.ErrorType != "multi-bit ECC" ||
		x.SocketId != some(1) || x.ChannelId != some(2) || x.ChannelSlotId != some(1) || x.SMBIOSHandle != some(0x1a) || x.Row.Valid {
		t.Errorf("extlog_event/2: %+v", *x)
	}
	// A truncated blob costs the location, not the record.
	if x := find(t, c, "extlog_event/3"); x.Grade != model.GradeB || x.SocketId.Valid || x.OsPhysicalAddress != addr(0x12f4a6080) {
		t.Errorf("extlog_event/3: %+v", *x)
	}
	// Error type 255 and an address of all ones mean "not provided".
	if x := find(t, c, "extlog_event/4"); x.Grade != model.GradeC || x.OsPhysicalAddress.Valid || x.ErrorType != "" || x.SMBIOSHandle != some(0x1a) {
		t.Errorf("extlog_event/4: %+v", *x)
	}
	if a := c.Actions[0]; a.OsPhysicalAddress != addr(0xb5689d000) || a.Result != model.ResultOK {
		t.Errorf("action: %+v", *a)
	}
}

// The database of a host whose clock was set back by 10 seconds after
// rasdaemon had started. rasdaemon's times are 9 to 10 seconds ahead of the
// times the kernel gave the same machine checks; the kernel log dates the
// errors by the kernel's clock. Read with rasdaemon's times, every error of
// such a boot is counted twice next to its kernel log line.
func TestRecordsCarryTheKernelsTime(t *testing.T) {
	c, rep, _ := collect(t, "clock-step-0.8.sql")
	if rep.Status != model.SourceOK || rep.Records != 13 || len(c.Events) != 10 || len(c.Actions) != 3 {
		t.Fatalf("report = %+v, %d events, %d actions; want ok, 13 records, 10 events, 3 actions", rep, len(c.Events), len(c.Actions))
	}
	if !hasNote(rep, "rasdaemon stamped its records 9 to 10 s ahead of the kernel's clock") || !hasNote(rep, "13 records are dated by the kernel's time") {
		t.Errorf("notes = %q", rep.Notes)
	}
	// walltime of the rows: 1794720280 is 2026-11-15 05:24:40 UTC.
	wall := []int64{1794720280, 1794720281, 1794720283, 1794720284, 1794720286, 1794720287, 1794720288, 1794720290, 1794720292, 1794720293}
	stamp := []time.Time{utc(2026, 11, 15, 5, 24, 50), utc(2026, 11, 15, 5, 24, 51), utc(2026, 11, 15, 5, 24, 53), utc(2026, 11, 15, 5, 24, 54),
		utc(2026, 11, 15, 5, 24, 56), utc(2026, 11, 15, 5, 24, 57), utc(2026, 11, 15, 5, 24, 58), utc(2026, 11, 15, 5, 25, 0),
		utc(2026, 11, 15, 5, 25, 1), utc(2026, 11, 15, 5, 25, 3)}
	for i, e := range c.Events {
		if !e.ErrorDT.Equal(time.Unix(wall[i], 0)) || e.TimeQuality != model.TimeExact {
			t.Errorf("event %d is dated %s (%s), want the kernel's %s", i+1, e.ErrorDT, e.TimeQuality, time.Unix(wall[i], 0).UTC())
		}
		// The fingerprint names the row by rasdaemon's time, as before.
		if want := fingerprint("mce_record", int64(i+1), stamp[i]); e.Fingerprint != want {
			t.Errorf("event %d: fingerprint changed", i+1)
		}
	}
	if e := c.Events[0]; e.ErrorSeverity != model.SevCE || e.OsPhysicalAddress != addr(0x104161040) || e.ChannelId != some(0) ||
		e.ErrorScrubType != model.ScrubDemand || e.Grade != model.GradeB {
		t.Errorf("first corrected error: %+v", *e)
	}
	if e := c.Events[7]; e.ErrorSeverity != model.SevRecoverable || e.ErrorScrubType != model.ScrubPatrol {
		t.Errorf("scrub error: %+v", *e)
	}
	if e := c.Events[9]; e.ErrorSeverity != model.SevRecoverable || e.OsPhysicalAddress != addr(0x103a33340) {
		t.Errorf("data load error: %+v", *e)
	}
	// The pages the kernel gave up follow the machine check next to them.
	for i, want := range []time.Time{utc(2026, 11, 15, 5, 24, 50), utc(2026, 11, 15, 5, 24, 52), utc(2026, 11, 15, 5, 24, 53)} {
		if a := c.Actions[i]; !a.At.Equal(want) || a.Kind != model.ActionPageOffline || a.Result != model.ResultOK {
			t.Errorf("action %d: %+v, want at %s", i+1, *a, want)
		}
	}
}

// What the correction leaves alone, and how far it reaches.
func TestClockCorrectionLimits(t *testing.T) {
	const schema = `
CREATE TABLE mc_event (id INTEGER PRIMARY KEY, timestamp TEXT, err_count INTEGER, err_type TEXT, err_msg TEXT, label TEXT, mc INTEGER, top_layer INTEGER, middle_layer INTEGER, lower_layer INTEGER, address INTEGER, grain INTEGER, syndrome INTEGER, driver_detail TEXT);
CREATE TABLE mce_record (id INTEGER PRIMARY KEY, timestamp TEXT, mcgcap INTEGER, mcgstatus INTEGER, status INTEGER, addr INTEGER, misc INTEGER, ip INTEGER, tsc INTEGER, walltime INTEGER, cpu INTEGER, cpuid INTEGER, apicid INTEGER, socketid INTEGER, cs INTEGER, bank INTEGER, cpuvendor INTEGER, bank_name TEXT, error_msg TEXT, mcgstatus_msg TEXT, mcistatus_msg TEXT, mcastatus_msg TEXT, user_action TEXT, mc_location TEXT);
CREATE TABLE memory_failure_event (id INTEGER PRIMARY KEY, timestamp TEXT, pfn TEXT, page_type TEXT, action_result TEXT);
`
	// 1794720000 is 2026-11-15 05:20:00 UTC.
	mce := func(id int, stamp string, wall int64) string {
		return "INSERT INTO mce_record (id, timestamp, status, addr, misc, walltime, bank, cpuvendor, socketid) VALUES (" +
			strings.Join([]string{itoa(id), "'" + stamp + "'", "0x9c00004000010090", "0x12345" + itoa(id) + "40", "0x86", itoa64(wall), "7", "0", "0"}, ", ") + ");\n"
	}
	mc := func(id int, stamp string, address string) string {
		return "INSERT INTO mc_event (id, timestamp, err_count, err_type, err_msg, label, mc, top_layer, middle_layer, lower_layer, address, grain, syndrome, driver_detail) VALUES (" +
			itoa(id) + ", '" + stamp + "', 1, 'Corrected', 'memory read error', 'CPU_SrcID#0_MC#0_Chan#0_DIMM#0', 0, 0, 0, -1, " + address + ", 32, 0, '');\n"
	}
	tests := []struct {
		name   string
		rows   string
		events map[string]time.Time // raw_ref suffix -> wanted time
		note   string               // "" = no note about the clock
	}{
		{
			name: "one second apart is how rasdaemon rounds, not a clock that is off",
			rows: mce(1, "2026-11-15 05:20:10 +0000", 1794720011) + mc(1, "2026-11-15 05:20:10 +0000", "0x12345140"),
			events: map[string]time.Time{
				"mce_record/1": utc(2026, 11, 15, 5, 20, 10), "mc_event/1": utc(2026, 11, 15, 5, 20, 10),
			},
		},
		{
			name: "the EDAC record of a machine check moves with it",
			rows: mce(1, "2026-11-15 05:20:10 +0000", 1794720000) + mc(1, "2026-11-15 05:20:10 +0000", "0x12345140"),
			events: map[string]time.Time{
				"mce_record/1": utc(2026, 11, 15, 5, 20, 0), "mc_event/1": utc(2026, 11, 15, 5, 20, 0),
			},
			note: "10 s ahead of the kernel's clock",
		},
		{
			name: "a clock that ran behind",
			rows: mce(1, "2026-11-15 05:19:15 +0000", 1794720000) + mc(1, "2026-11-15 05:19:16 +0000", "0x12345140"),
			events: map[string]time.Time{
				"mce_record/1": utc(2026, 11, 15, 5, 20, 0), "mc_event/1": utc(2026, 11, 15, 5, 20, 1),
			},
			note: "45 s behind the kernel's clock",
		},
		{
			name: "a record ten minutes from any machine check keeps rasdaemon's time",
			rows: mce(1, "2026-11-15 05:20:10 +0000", 1794720000) + mc(1, "2026-11-15 05:30:11 +0000", "0x99999000") +
				mc(2, "2026-11-15 05:30:10 +0000", "0x88888000"),
			events: map[string]time.Time{
				"mce_record/1": utc(2026, 11, 15, 5, 20, 0), "mc_event/1": utc(2026, 11, 15, 5, 30, 11), "mc_event/2": utc(2026, 11, 15, 5, 30, 0),
			},
			note: "2 records are dated by the kernel's time",
		},
		{
			name: "a kernel time before 2000 is a clock that was never set",
			rows: mce(1, "2026-11-15 05:20:10 +0000", 86400) + mce(2, "2026-11-15 05:20:20 +0000", 0),
			events: map[string]time.Time{
				"mce_record/1": utc(2026, 11, 15, 5, 20, 10), "mce_record/2": utc(2026, 11, 15, 5, 20, 20),
			},
		},
		{
			name: "rasdaemon restarted with the right clock: each record follows the machine check nearest to it",
			rows: mce(1, "2026-11-15 05:20:10 +0000", 1794720000) + mce(2, "2026-11-15 05:24:00 +0000", 1794720240) +
				mc(1, "2026-11-15 05:20:40 +0000", "0x77777000") + mc(2, "2026-11-15 05:23:30 +0000", "0x66666000"),
			events: map[string]time.Time{
				"mce_record/1": utc(2026, 11, 15, 5, 20, 0), "mce_record/2": utc(2026, 11, 15, 5, 24, 0),
				"mc_event/1": utc(2026, 11, 15, 5, 20, 30), "mc_event/2": utc(2026, 11, 15, 5, 23, 30),
			},
			note: "10 s ahead of the kernel's clock",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script := filepath.Join(t.TempDir(), "db.sql")
			if err := os.WriteFile(script, []byte(schema+tt.rows), 0o600); err != nil {
				t.Fatal(err)
			}
			var c adapter.Collected
			rep := New(testutil.BuildSQLite(t, script, "ras-mc_event.db"), Options{Direct: true}).Collect(ctx, &c)
			if rep.Status != model.SourceOK || len(c.Events) != len(tt.events) {
				t.Fatalf("report = %+v, %d events; want ok and %d events", rep, len(c.Events), len(tt.events))
			}
			for ref, want := range tt.events {
				if e := find(t, &c, ref); !e.ErrorDT.Equal(want) {
					t.Errorf("%s is dated %s, want %s", ref, e.ErrorDT.UTC().Format(time.RFC3339), want.Format(time.RFC3339))
				}
			}
			switch {
			case tt.note == "" && hasNote(rep, "the kernel's clock"):
				t.Errorf("unexpected note about the clock: %q", rep.Notes)
			case tt.note != "" && !hasNote(rep, tt.note):
				t.Errorf("notes = %q, want one with %q", rep.Notes, tt.note)
			}
		})
	}

	// A machine check and its EDAC record stay one error: they are matched
	// by the time rasdaemon gave both.
	script := filepath.Join(t.TempDir(), "db.sql")
	rows := mce(1, "2026-11-15 05:20:10 +0000", 1794720000) + mc(1, "2026-11-15 05:20:10 +0000", "0x12345140")
	if err := os.WriteFile(script, []byte(schema+rows), 0o600); err != nil {
		t.Fatal(err)
	}
	var c adapter.Collected
	New(testutil.BuildSQLite(t, script, "ras-mc_event.db"), Options{Direct: true}).Collect(ctx, &c)
	if m, e := find(t, &c, "mce_record/1"), find(t, &c, "mc_event/1"); m.DupOfFingerprint != e.Fingerprint || e.Fingerprint == "" {
		t.Errorf("the machine check record does not name its EDAC record: %q vs %q", m.DupOfFingerprint, e.Fingerprint)
	}

	for _, tt := range []struct {
		lo, hi int64
		want   string
	}{{10, 10, "10 s ahead of"}, {9, 10, "9 to 10 s ahead of"}, {-45, -45, "45 s behind"}, {-45, -40, "40 to 45 s behind"},
		{-5, 10, "between 5 s behind and 10 s ahead of"}} {
		if got := clockSpan(tt.lo, tt.hi); got != tt.want {
			t.Errorf("clockSpan(%d, %d) = %q, want %q", tt.lo, tt.hi, got, tt.want)
		}
	}
}

func itoa(v int) string     { return itoa64(int64(v)) }
func itoa64(v int64) string { return strconv.FormatInt(v, 10) }

func TestSnapshotLeavesTheSourceAlone(t *testing.T) {
	path := testutil.BuildSQLite(t, testutil.Testdata(t, "rasdaemon", "intel-skx-0.6.sql"), "ras-mc_event.db")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fiBefore, _ := os.Stat(path)
	tmp := t.TempDir()
	var c adapter.Collected
	rep := New(path, Options{TempDir: tmp}).Collect(ctx, &c)
	if rep.Status != model.SourcePartial {
		t.Fatalf("status = %s (%s)", rep.Status, rep.Detail)
	}
	after, _ := os.ReadFile(path)
	fiAfter, _ := os.Stat(path)
	if !bytes.Equal(before, after) || !fiBefore.ModTime().Equal(fiAfter.ModTime()) {
		t.Error("the source database was modified")
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("files appeared next to the source database: %v", entries)
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("snapshot was not removed: %v", left)
	}
	// Events point at the real path, not at the snapshot.
	if !strings.HasPrefix(c.Events[0].RawRef, path+"#") {
		t.Errorf("raw_ref = %q", c.Events[0].RawRef)
	}
}

func TestSnapshotOfADatabaseInTheMiddleOfAWrite(t *testing.T) {
	path := testutil.BuildSQLite(t, testutil.Testdata(t, "rasdaemon", "ghes-1.0.sql"), "ras-mc_event.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// A writer with a small page cache has to spill pages to the database
	// file before it commits; the rollback journal is what makes that safe.
	if _, err := conn.ExecContext(ctx, `PRAGMA cache_size = 2`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	for i := 100; i < 3100; i++ {
		if _, err := conn.ExecContext(ctx, `INSERT INTO mc_event VALUES (?, '2026-10-07 00:00:00 +0900', 1, 'Corrected', 'single-bit ECC',
 'unknown memory', 0, -1, -1, -1, ?, 6, 0, 'APEI location: node:0 card:1 module:0 status(0x0000000000000400): Storage error in DRAM memory')`,
			i, 0x1000000+i*64); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(path + "-journal"); err != nil {
		t.Skipf("the writer did not spill to disk, nothing to test: %v", err)
	}

	var c adapter.Collected
	rep := New(path, Options{TempDir: t.TempDir()}).Collect(ctx, &c)
	if rep.Status != model.SourceOK {
		t.Fatalf("status = %s (%s)", rep.Status, rep.Detail)
	}
	// Only the committed rows are visible; the unfinished transaction was
	// rolled back in the copy.
	if len(c.Events) != 10 {
		t.Errorf("%d events, want the 10 committed ones", len(c.Events))
	}
	// The writer was not disturbed and can commit.
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		t.Fatalf("writer could not commit after the snapshot: %v", err)
	}
	var again adapter.Collected
	if rep := New(path, Options{TempDir: t.TempDir()}).Collect(ctx, &again); rep.Status != model.SourceOK || len(again.Events) != 3010 {
		t.Errorf("after the commit: status %s, %d events, want 3010", rep.Status, len(again.Events))
	}
}

func TestSourcesThatCannotBeRead(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	good, err := os.ReadFile(testutil.BuildSQLite(t, testutil.Testdata(t, "rasdaemon", "intel-skx-0.6.sql"), "good.db"))
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "other.db")
	odb, _ := sql.Open("sqlite", other)
	if _, err := odb.Exec(`CREATE TABLE inventory (id INTEGER PRIMARY KEY, name TEXT); INSERT INTO inventory VALUES (1, 'x')`); err != nil {
		t.Fatal(err)
	}
	odb.Close()
	empty := filepath.Join(dir, "empty.db")
	edb, _ := sql.Open("sqlite", empty)
	if _, err := edb.Exec(`CREATE TABLE mc_event (id INTEGER PRIMARY KEY, timestamp TEXT, err_count INTEGER, err_type TEXT, err_msg TEXT, label TEXT, mc INTEGER, top_layer INTEGER, middle_layer INTEGER, lower_layer INTEGER, address INTEGER, grain INTEGER, syndrome INTEGER, driver_detail TEXT)`); err != nil {
		t.Fatal(err)
	}
	edb.Close()

	tests := []struct {
		name   string
		path   string
		status model.SourceStatus
		detail string
	}{
		{"missing file", filepath.Join(dir, "absent.db"), model.SourceAbsent, "not present"},
		{"directory", dir, model.SourceError, "is a directory"},
		{"text file", write("dmesg.txt", []byte(strings.Repeat("EDAC MC0: 1 CE memory read error\n", 300))), model.SourceError, "not a readable SQLite database"},
		{"zero-length file", write("zero.db", nil), model.SourceError, "no rasdaemon tables found"},
		{"another SQLite database", other, model.SourceError, "no rasdaemon tables found (tables present: inventory)"},
		{"rasdaemon database without rows", empty, model.SourceEmpty, ""},
		{"truncated database", write("truncated.db", good[:len(good)/2+100]), model.SourceError, ""},
		{"damaged header", write("damaged.db", append([]byte("SQLite format 3\x00garbage-garbage-garbage"), good[40:]...)), model.SourceError, ""},
	}
	for _, tt := range tests {
		for _, direct := range []bool{true, false} {
			t.Run(tt.name, func(t *testing.T) {
				var c adapter.Collected
				rep := New(tt.path, Options{Direct: direct, TempDir: t.TempDir()}).Collect(ctx, &c)
				if rep.Status != tt.status {
					t.Errorf("direct=%v: status = %s (%s), want %s", direct, rep.Status, rep.Detail, tt.status)
				}
				if !strings.Contains(rep.Detail, tt.detail) {
					t.Errorf("direct=%v: detail = %q, want it to contain %q", direct, rep.Detail, tt.detail)
				}
				if rep.Status != model.SourceEmpty && rep.Status != model.SourceOK && rep.Detail == "" {
					t.Errorf("direct=%v: a failed source must say why", direct)
				}
				if rep.Kind != Kind || rep.Origin != tt.path {
					t.Errorf("report identity: %+v", rep)
				}
			})
		}
	}

	t.Run("permission denied", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root reads everything")
		}
		p := write("secret.db", good)
		if err := os.Chmod(p, 0); err != nil {
			t.Fatal(err)
		}
		var c adapter.Collected
		rep := New(p, Options{TempDir: t.TempDir()}).Collect(ctx, &c)
		if rep.Status != model.SourceUnreadable || !strings.Contains(rep.Detail, "readable as root") {
			t.Errorf("report = %+v", rep)
		}
	})
}

func TestLooselyTypedRows(t *testing.T) {
	// SQLite does not enforce column types. Numbers stored as text, NULL
	// layers and a negative address must not stop the read.
	dir := t.TempDir()
	path := filepath.Join(dir, "odd.db")
	db, _ := sql.Open("sqlite", path)
	if _, err := db.Exec(`
CREATE TABLE mc_event (id INTEGER PRIMARY KEY, timestamp TEXT, err_count, err_type TEXT, err_msg TEXT, label TEXT, mc, top_layer, middle_layer, lower_layer, address, grain, syndrome, driver_detail TEXT, extra_column TEXT);
INSERT INTO mc_event VALUES (1, '2025-01-01 00:00:00 +0000', '2', 'Corrected', NULL, NULL, '0', NULL, NULL, NULL, '0x1000', NULL, NULL, NULL, 'x');
INSERT INTO mc_event VALUES (2, '2025-01-01 00:00:01', NULL, 'Corrected', 'memory read error', 'DIMM_A1', 0, 255, 255, 255, -5, 0, 0, '', NULL);
INSERT INTO mc_event VALUES (3, 'yesterday', 1, 'Corrected', '', '', 0, 0, 0, -1, 0, 0, 0, '', NULL);
INSERT INTO mc_event VALUES (4, 20250101, 1.5, 'Corrected', X'00FF', 'DIMM_A1', 2.0, 0, 1, -1, 4096.0, 0, 0, ' err_code:zz socket:x imc:y', NULL);
`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	var c adapter.Collected
	rep := New(path, Options{Direct: true}).Collect(ctx, &c)
	if rep.Status != model.SourcePartial || rep.Records != 4 || rep.Skipped != 2 || len(c.Events) != 2 {
		t.Fatalf("report = %+v with %d events; want partial, 4 records, 2 skipped, 2 events", rep, len(c.Events))
	}
	a := c.Events[0]
	if a.Count != 2 || a.OsPhysicalAddress != addr(0x1000) || a.EdacMC != some(0) || a.EdacLocation != "" || a.Label != "" || a.Grade != model.GradeB {
		t.Errorf("row 1: %+v", *a)
	}
	b := c.Events[1]
	if b.Count != 1 || b.OsPhysicalAddress.Valid || b.UTCOffset != none || b.Label != "DIMM_A1" || b.EdacLocation != "" || b.Grade != model.GradeC ||
		!b.ErrorDT.Equal(utc(2025, 1, 1, 0, 0, 1)) {
		t.Errorf("row 2: %+v", *b)
	}
}

type failingSink struct {
	adapter.Collected
	err error
}

func (f *failingSink) Event(*model.Event) error { return f.err }

func TestSinkErrorStopsTheRead(t *testing.T) {
	path := testutil.BuildSQLite(t, testutil.Testdata(t, "rasdaemon", "intel-skx-0.6.sql"), "ras-mc_event.db")
	boom := errors.New("ledger is full")
	rep := New(path, Options{Direct: true}).Collect(ctx, &failingSink{err: boom})
	if rep.Status != model.SourceError || !errors.Is(rep.SinkErr, boom) || !strings.Contains(rep.Detail, "ledger is full") {
		t.Errorf("report = %+v", rep)
	}
}

func TestCancelledContext(t *testing.T) {
	path := testutil.BuildSQLite(t, testutil.Testdata(t, "rasdaemon", "intel-skx-0.6.sql"), "ras-mc_event.db")
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	for _, direct := range []bool{true, false} {
		var c adapter.Collected
		rep := New(path, Options{Direct: direct, TempDir: t.TempDir()}).Collect(cctx, &c)
		if rep.Status != model.SourceError || len(c.Events) != 0 {
			t.Errorf("direct=%v: report = %+v, %d events", direct, rep, len(c.Events))
		}
	}
}

func TestParseTimestamp(t *testing.T) {
	tests := []struct {
		in   string
		want time.Time
		off  model.Opt[int]
		ok   bool
	}{
		{"2023-02-27 14:36:26 +0800", utc(2023, 2, 27, 6, 36, 26), some(28800), true},
		{"2024-06-03 22:10:41 -0500", utc(2024, 6, 4, 3, 10, 41), some(-18000), true},
		{"2026-10-02 09:15:30 +0000", utc(2026, 10, 2, 9, 15, 30), some(0), true},
		{" 2026-10-02 09:15:30 +0530 ", utc(2026, 10, 2, 3, 45, 30), some(19800), true},
		{"2026-10-02 09:15:30", utc(2026, 10, 2, 9, 15, 30), none, true},
		{"", time.Time{}, none, false},
		{"2026-13-02 09:15:30 +0000", time.Time{}, none, false},
		{"1700000000", time.Time{}, none, false},
	}
	for _, tt := range tests {
		got, off, ok := parseTimestamp(tt.in)
		if ok != tt.ok || !got.Equal(tt.want) || off != tt.off {
			t.Errorf("parseTimestamp(%q) = %v, %v, %v; want %v, %v, %v", tt.in, got, off, ok, tt.want, tt.off, tt.ok)
		}
	}
}
