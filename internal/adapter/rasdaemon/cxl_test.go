package rasdaemon

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
)

// cxlGot is one record of the CXL tables in a form that is easy to compare.
type cxlGot struct {
	Ref   string
	What  string
	Label string
	At    string
	Extra string
}

func cxlSummary(c *adapter.Collected) []cxlGot {
	var out []cxlGot
	short := func(ref string) string { return ref[strings.LastIndex(ref, "#")+1:] }
	for _, e := range c.Events {
		extra := string(e.Grade)
		if e.ErrorScrubType != "" {
			extra += " " + string(e.ErrorScrubType)
		}
		if e.OsPhysicalAddress.Valid {
			extra += " hpa"
		}
		out = append(out, cxlGot{short(e.RawRef), string(e.ErrorSeverity) + ": " + e.ErrorType, e.Label, e.ErrorDT.Format("01-02 15:04"), extra})
	}
	for _, a := range c.Actions {
		extra := a.Method
		if a.Rank.Valid || a.BankGroup.Valid || a.Bank.Valid || a.Row.Valid {
			extra += " rank " + a.Rank.String() + " bg " + a.BankGroup.String() + " bank " + a.Bank.String() + " row " + a.Row.String()
		}
		out = append(out, cxlGot{short(a.RawRef), "action " + string(a.Kind) + "/" + string(a.Result) + ": " + a.Detail, a.Label, a.At.Format("01-02 15:04"), extra})
	}
	return out
}

func TestCXLTables(t *testing.T) {
	c, rep, _ := collect(t, "cxl-1.0.sql")
	want := []cxlGot{
		{"cxl_dram_event/1", "CE: media ECC error", "cxl:mem0", "11-10 08:00", "A Demand hpa"},
		// An access to an invalid address is no memory error.
		{"cxl_dram_event/2", "Informational: invalid address", "cxl:mem0", "11-10 08:05", "C Demand"},
		// The general media record numbers its event types differently.
		{"cxl_general_media_event/1", "Informational: invalid address", "cxl:mem1", "11-10 09:00", "C Demand"},
		{"cxl_general_media_event/2", "UE: scrub media ECC error", "cxl:mem1", "11-11 02:30", "B Patrol hpa"},
		{"cxl_memory_module_event/1", "Informational: CXL device health status change; health: maintenance needed, hardware replacement needed; life used 97%; temperature 61 C; corrected volatile errors 4012; dirty shutdowns 2", "cxl:mem1", "11-11 02:31", "C"},
		{"cxl_memory_module_event/2", "Informational: CXL device media status change; health: hardware replacement needed; media: all data lost; life used 97%; temperature 60 C; corrected volatile errors 4100; dirty shutdowns 2", "cxl:mem1", "11-12 02:31", "C"},
		{"cxl_memory_module_event/3", "Informational: CXL device temperature change; life used 12%; temperature 78 C", "cxl:mem0", "11-12 03:00", "C"},
		{"cxl_poison_event/1", "Informational: CXL poison list entry at device address 0x7f000, 64 bytes, source internal", "cxl:mem1", "11-11 03:00", "B hpa"},
		{"cxl_poison_event/2", "Informational: CXL poison list entry at device address 0x100000, 128 bytes, source unknown", "cxl:mem1", "11-11 03:00", "C"},
		{"cxl_poison_event/4", "Informational: CXL poison injected at device address 0x40, 64 bytes", "cxl:mem0", "11-12 04:00", "C"},
		{"cxl_poison_event/5", "Informational: CXL poison cleared at device address 0x40, 64 bytes", "cxl:mem0", "11-12 04:01", "C"},
		// The device says it needs to be replaced: one mark, at the first record that says so.
		{"cxl_memory_module_event/1", "action mark-failed/ok: the CXL device reports that it needs to be replaced", "cxl:mem1", "11-11 02:31", "device"},
		{"cxl_memory_sparing_event/1", "action sparing/ok: row sparing, 3 spare resources left", "cxl:mem0", "11-10 08:10", "hard rank 1 bg 1 bank 2 row 6703"},
		{"cxl_memory_sparing_event/3", "action sparing/failed: bank sparing, started by the device, result code 0x1, 0 spare resources left", "cxl:mem1", "11-11 02:35", "soft rank - bg 0 bank 5 row -"},
	}
	if got := cxlSummary(c); !reflect.DeepEqual(got, want) {
		t.Errorf("records differ")
		for _, g := range got {
			t.Logf("  have %+v", g)
		}
	}
	if rep.Status != model.SourcePartial || rep.Skipped != 1 {
		t.Errorf("report = %+v", rep)
	}
	for _, part := range []string{
		"cxl_memory_sparing_event/4: timestamp is empty or unreadable",
		"cxl_poison_event: 1 rows list poison that an earlier row already listed",
		"cxl_memory_sparing_event: 1 rows only report how many spare resources are left",
		"cxl_overflow_event: device mem1 lost 7 records of its failure log between 2026-11-11 02:30:05 +0000 and 2026-11-11 02:39:59 +0000",
		"table cxl_generic_event: 1 rows not read (CXL records of a vendor's own kind)",
		"table cxl_aer_ue_event: 1 rows not read (CXL link errors)",
	} {
		if !hasNote(rep, part) {
			t.Errorf("notes lack %q:\n  %s", part, strings.Join(rep.Notes, "\n  "))
		}
	}
	// Tables that are read do not show up as "not read".
	for _, table := range []string{"cxl_memory_module_event", "cxl_poison_event", "cxl_memory_sparing_event", "cxl_overflow_event"} {
		if hasNote(rep, "table "+table+":") {
			t.Errorf("%s is reported as not read", table)
		}
	}

	// The DRAM event keeps the sub-channel and the nibble.
	d := find(t, c, "cxl_dram_event/1")
	if d.SubChannel != some(1) || d.ErrorVector != "nibble:0x4" || d.Row != some(0x1a2f) || d.Column != some(0x3f8) || d.Decoder != "cxl" {
		t.Errorf("cxl_dram_event/1 = %+v", *d)
	}

	// Each device is a module, named by the kernel and identified by its
	// serial number, seen from its first to its last record.
	type seen struct {
		Slot, Serial, Tech string
		At                 string
	}
	var mods []seen
	for _, m := range c.Modules {
		if !m.Populated || m.Source != Kind {
			t.Errorf("module = %+v", m)
		}
		mods = append(mods, seen{m.Locator, m.DIMM.Serial, m.DIMM.Technology, m.ObservedAt.Format("01-02 15:04")})
	}
	wantMods := []seen{
		{"cxl:mem0", "0x1a2b3c4d5e", "CXL", "11-10 08:00"},
		{"cxl:mem0", "0x1a2b3c4d5e", "CXL", "11-12 04:01"},
		{"cxl:mem1", "0x5678", "CXL", "11-10 09:00"},
		{"cxl:mem1", "0x5678", "CXL", "11-12 03:00"},
	}
	if !reflect.DeepEqual(mods, wantMods) {
		t.Errorf("modules = %+v\n   want %+v", mods, wantMods)
	}
}

// Reading the database again, with the poison list read out once more
// since, gives the same poison records.
func TestCXLPoisonKeepsOneRecordPerRange(t *testing.T) {
	a, _, _ := collect(t, "cxl-1.0.sql")
	b, _, _ := collect(t, "cxl-1.0.sql")
	fp := func(c *adapter.Collected, ref string) string { return find(t, c, ref).Fingerprint }
	if fp(a, "cxl_poison_event/1") != fp(b, "cxl_poison_event/1") || fp(a, "cxl_poison_event/1") == fp(a, "cxl_poison_event/2") {
		t.Error("fingerprints of poison list entries")
	}
	// Injection and clearing are events of their own.
	if fp(a, "cxl_poison_event/4") == fp(a, "cxl_poison_event/5") {
		t.Error("injection and clearing share a fingerprint")
	}
	var at time.Time
	for _, e := range a.Events {
		if strings.HasSuffix(e.RawRef, "cxl_poison_event/1") {
			at = e.ErrorDT
		}
	}
	if !at.Equal(time.Date(2026, 11, 11, 3, 0, 0, 0, time.UTC)) {
		t.Errorf("the poison entry has the time %v, want that of its first listing", at)
	}
}

func TestCXLNames(t *testing.T) {
	// The two media records share most names and differ in their numbers.
	if nameOf(cxlDRAMTypes, 1) != "scrub media ECC error" || nameOf(cxlMediaTypes, 1) != "invalid address" ||
		nameOf(cxlMediaTypes, 4) != "scrub media ECC error" || nameOf(cxlDRAMTypes, 2) != "invalid address" {
		t.Error("event type names")
	}
	for _, c := range []struct {
		names []string
		code  int64
		want  string
	}{
		{cxlDRAMTypes, 0, "media ECC error"}, {cxlDRAMTypes, 7, "event type 7"}, {cxlDRAMTypes, -1, "event type -1"},
		{cxlSparingKinds, 3, "rank"}, {cxlMediaStatus, 3, "all data lost"}, {cxlModuleEvents, 8, "power management fault"},
	} {
		if got := nameOf(c.names, c.code); got != c.want {
			t.Errorf("nameOf(%d) = %q, want %q", c.code, got, c.want)
		}
	}
}
