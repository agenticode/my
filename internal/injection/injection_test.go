package injection

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

var ctx = context.Background()

const mcePlan = `# Injection plan: what was put into the host, where and how many times.
format: 1
scenario: mce
summary: "machine checks in a nested guest: 7 corrected and 3 uncorrected memory errors"
method: "mce-inject module, records handed to the machine check poller and exception handler"
kernel: 7.3.0-ras
rasdaemon: 0.8.0
cpu: "Skylake-SP (family 6 model 85) emulated by QEMU without hardware virtualization"
injections:
  - id: ce-1
    time: 2026-11-15T05:14:11.820Z
    inject: error
    severity: CE
    scrub: Demand
    count: 1
    address: 0x106b2c040
    socket: 0
    channel: 0
    note: "machine check bank 7, status 0x9c00004000010090 (corrected memory read error, channel 0)"
  - id: ce-3
    time: 2026-11-15T05:14:14.900Z
    inject: error
    severity: CE
    count: 2          # twice on one page
    address: 0x106b28100
    channel: 0
  - id: ce-3-offline
    time: 2026-11-15T05:14:14.900Z
    inject: page-offline
    how: soft
    count: 1
    address: 0x106b28000
    note: "by the corrected error collector at the second error of ce-3"
  - id: row
    inject: error
    severity: Recoverable
    scrub: Patrol
    rank: 1
    device: 7
    bank_group: 1
    bank: 2
    row: 0x1a2f
    column: 0x238
    bit: 5
    label: "DIMM 0"
`

func TestRead(t *testing.T) {
	p, err := Read(strings.NewReader(mcePlan))
	if err != nil {
		t.Fatal(err)
	}
	if p.Scenario != "mce" || !strings.HasPrefix(p.Summary, "machine checks in a nested guest: 7") || !strings.HasPrefix(p.Method, "mce-inject module,") {
		t.Errorf("head: %+v", p)
	}
	if want := map[string]string{"kernel": "7.3.0-ras", "rasdaemon": "0.8.0",
		"cpu": "Skylake-SP (family 6 model 85) emulated by QEMU without hardware virtualization"}; !reflect.DeepEqual(p.Meta, want) {
		t.Errorf("meta = %v", p.Meta)
	}
	if len(p.Injections) != 4 {
		t.Fatalf("%d injections", len(p.Injections))
	}
	some := func(v int) model.Opt[int] { return model.Some(v) }
	want := []Injection{
		{ID: "ce-1", Time: time.Date(2026, 11, 15, 5, 14, 11, 820e6, time.UTC), Inject: InjectError, Severity: model.SevCE, Scrub: model.ScrubDemand,
			Count: 1, Address: model.Some(uint64(0x106b2c040)), Socket: some(0), Channel: some(0), Line: 10,
			Note: "machine check bank 7, status 0x9c00004000010090 (corrected memory read error, channel 0)"},
		{ID: "ce-3", Time: time.Date(2026, 11, 15, 5, 14, 14, 900e6, time.UTC), Inject: InjectError, Severity: model.SevCE, Count: 2,
			Address: model.Some(uint64(0x106b28100)), Channel: some(0), Line: 20},
		{ID: "ce-3-offline", Time: time.Date(2026, 11, 15, 5, 14, 14, 900e6, time.UTC), Inject: InjectOffline, How: "soft", Count: 1,
			Address: model.Some(uint64(0x106b28000)), Line: 27, Note: "by the corrected error collector at the second error of ce-3"},
		{ID: "row", Inject: InjectError, Severity: model.SevRecoverable, Scrub: model.ScrubPatrol, Count: 1, Rank: some(1), Device: some(7),
			BankGroup: some(1), Bank: some(2), Row: some(0x1a2f), Column: some(0x238), Bit: some(5), Label: "DIMM 0", Line: 34},
	}
	for i := range want {
		if !reflect.DeepEqual(p.Injections[i], want[i]) {
			t.Errorf("injection %d\n got  %+v\n want %+v", i, p.Injections[i], want[i])
		}
	}

	// A plan without injections: the baseline of a host.
	p, err = Read(strings.NewReader("format: 1\nscenario: baseline\nsummary: nothing\nmethod: none\ninjections: []\n"))
	if err != nil || len(p.Injections) != 0 || p.Scenario != "baseline" {
		t.Errorf("empty plan: %+v, %v", p, err)
	}
	// Quotes and escapes; Windows line ends.
	p, err = Read(strings.NewReader("format: 1\r\nscenario: \"a \\\"b\\\" \\\\ c\"   # why\r\ninjections: []\r\n"))
	if err != nil || p.Scenario != `a "b" \ c` {
		t.Errorf("quoted scenario: %+v, %v", p, err)
	}
}

func TestReadRejects(t *testing.T) {
	head := "format: 1\nscenario: s\n"
	entry := "injections:\n  - id: a\n    inject: error\n    severity: CE\n    address: 0x1000\n"
	for _, tt := range []struct{ name, in, want string }{
		{"empty", "", `no "format" line`},
		{"another format", "format: 2\nscenario: s\ninjections: []\n", "plan format 2 is not known"},
		{"format not a number", "format: one\n", "line 1: format"},
		{"no scenario", "format: 1\ninjections: []\n", "names no scenario"},
		{"no list", head, `no "injections" list`},
		{"not key and value", head + "just words\n", `line 3: expected "key: value"`},
		{"no space after the colon", head + "kernel:7.3\n", "line 3: a space must follow the colon"},
		{"field name", head + "Kernel Version: 7\n", "line 3"},
		{"tab", head + "injections:\n\t- id: a\n", "line 4: a tab"},
		{"indented outside the list", head + "  kernel: 7.3\n", "line 3: an indented line outside the list"},
		{"field before an entry", head + "injections:\n    id: a\n", `line 4: a field before the first "- "`},
		{"fields not lined up", head + "injections:\n  - id: a\n      inject: error\n", "line 5: the fields of an injection must line up"},
		{"unknown field", head + "injections:\n  - id: a\n    colour: red\n", `line 5: "colour" is not a field`},
		{"field twice", head + entry + "    address: 0x2000\n", `line 8: "address" is given twice`},
		{"list twice", head + "injections: []\ninjections: []\n", `line 4: "injections" is given twice`},
		{"flow list", head + "injections: [a, b]\n", "line 3"},
		{"meta twice", head + "kernel: a\nkernel: b\ninjections: []\n", `line 4: "kernel" is given twice`},
		{"open quote", head + "summary: \"never ends\ninjections: []\n", "line 3: summary: the closing quote is missing"},
		{"text after quote", head + "summary: \"a\" b\ninjections: []\n", "line 3: summary: text after the closing quote"},
		{"special characters unquoted", head + "summary: {a: b}\ninjections: []\n", "line 3: summary: a value with special characters"},
		{"no id", head + "injections:\n  - inject: error\n    severity: CE\n    address: 1\n", "line 4: injection \"\": it has no id"},
		{"id twice", head + entry + "  - id: a\n    inject: error\n    severity: CE\n    address: 0x1000\n", `line 8: injection "a" is already defined on line 4`},
		{"no kind", head + "injections:\n  - id: a\n    address: 1\n", "it does not say what was injected"},
		{"unknown kind", head + "injections:\n  - id: a\n    inject: fire\n", `inject "fire" is neither`},
		{"severity", head + "injections:\n  - id: a\n    inject: error\n    severity: bad\n    address: 1\n", `severity "bad" is none of`},
		{"scrub", head + "injections:\n  - id: a\n    inject: error\n    severity: CE\n    scrub: Weekly\n    address: 1\n", `scrub "Weekly" is none of`},
		{"error without a place", head + "injections:\n  - id: a\n    inject: error\n    severity: CE\n", "an error needs an address, a row, a label or a device"},
		{"offline without address", head + "injections:\n  - id: a\n    inject: page-offline\n    how: soft\n", "needs the address of the page"},
		{"offline how", head + "injections:\n  - id: a\n    inject: page-offline\n    how: gently\n    address: 0x1000\n", `how "gently" is neither hard nor soft`},
		{"offline count", head + "injections:\n  - id: a\n    inject: page-offline\n    how: soft\n    count: 2\n    address: 0x1000\n", "count is 0 or 1"},
		{"offline with severity", head + "injections:\n  - id: a\n    inject: page-offline\n    how: soft\n    severity: CE\n    address: 0x1000\n", "severity and scrub belong to an error"},
		{"how on an error", head + entry + "    how: hard\n", `"how" belongs to a page offline`},
		{"number", head + "injections:\n  - id: a\n    inject: error\n    severity: CE\n    row: many\n", `line 7: row: "many" is not a number`},
		{"negative", head + "injections:\n  - id: a\n    inject: error\n    severity: CE\n    count: -1\n    address: 1\n", "line 7: count"},
		{"time", head + "injections:\n  - id: a\n    time: yesterday\n", `line 5: time "yesterday" is not in the form`},
		{"dpa without device", head + "injections:\n  - id: a\n    inject: error\n    severity: CE\n    dpa: 0x1000\n", "needs the device"},
		{"empty entry", head + "injections:\n  -\n", "line 4: an empty list entry"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Read(strings.NewReader(tt.in))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want one with %q", err, tt.want)
			}
		})
	}
	if _, err := ReadFile(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Error("a missing file was read")
	}
	if _, err := Read(strings.NewReader("format: 1\nscenario: s\nsummary: " + strings.Repeat("x", maxLine+10) + "\n")); err == nil ||
		!strings.Contains(err.Error(), "longer than") {
		t.Errorf("a line that is too long: %v", err)
	}
}

// ledger builds a ledger with the given records of one host.
func ledger(t *testing.T, events []*model.Event, actions []*model.Action) *store.Store {
	t.Helper()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	at := time.Date(2026, 11, 15, 5, 14, 12, 0, time.UTC)
	err = s.Update(ctx, func(w *store.Writer) error {
		if err := w.UpsertHost(&model.Host{Name: "h", FirstSeen: at, LastSeen: at}); err != nil {
			return err
		}
		for i, e := range events {
			e.Host, e.Kind, e.TimeQuality = "h", model.KindError, model.TimeExact
			if e.ErrorDT.IsZero() {
				e.ErrorDT = at.Add(time.Duration(i) * time.Second)
			}
			e.RawRef = "test#" + string(rune('a'+i))
			e.Fingerprint = model.Fingerprint("test", "event", e.RawRef)
			e.Grade = e.ComputeGrade()
			if _, err := w.InsertEvent(e); err != nil {
				return err
			}
		}
		for i, a := range actions {
			a.Host, a.Kind = "h", model.ActionPageOffline
			if a.At.IsZero() {
				a.At = at.Add(time.Duration(i) * time.Second)
			}
			a.RawRef = "test#action" + string(rune('a'+i))
			a.Fingerprint = model.Fingerprint("test", "action", a.RawRef)
			if _, err := w.InsertAction(a); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkDuplicates(ctx, "h", store.DuplicateTolerance{}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCompare(t *testing.T) {
	p, err := Read(strings.NewReader(mcePlan))
	if err != nil {
		t.Fatal(err)
	}
	some := func(v int) model.Opt[int] { return model.Some(v) }
	addr := func(v uint64) model.Opt[uint64] { return model.Some(v) }
	at := time.Date(2026, 11, 15, 5, 14, 12, 0, time.UTC)

	s := ledger(t, []*model.Event{
		// ce-1 from two sources: one error.
		{ErrorSeverity: model.SevCE, ErrorScrubType: model.ScrubDemand, OsPhysicalAddress: addr(0x106b2c040), SocketId: some(0), ChannelId: some(0),
			Source: "rasdaemon/mce_record", ErrorDT: at},
		{ErrorSeverity: model.SevCE, ErrorScrubType: model.ScrubDemand, OsPhysicalAddress: addr(0x106b2c040), SocketId: some(0),
			Source: "klog/mce", ErrorDT: at},
		// ce-3: two errors, the second one decoded onto another channel.
		{ErrorSeverity: model.SevCE, OsPhysicalAddress: addr(0x106b28100), ChannelId: some(0), Source: "rasdaemon/mce_record", ErrorDT: at.Add(3 * time.Second)},
		{ErrorSeverity: model.SevCE, OsPhysicalAddress: addr(0x106b28100), ChannelId: some(1), Source: "rasdaemon/mce_record", ErrorDT: at.Add(20 * time.Second)},
		// The row error: no address in the plan, found by its place. The record knows no bit.
		{ErrorSeverity: model.SevRecoverable, ErrorScrubType: model.ScrubPatrol, Rank: some(1), DeviceId: some(7), BankGroup: some(1), Bank: some(2),
			Row: some(0x1a2f), Column: some(0x238), Label: "dimm 0", Source: "rasdaemon/mc_event"},
		// An error nobody planned, and the same cache line long before the injection.
		{ErrorSeverity: model.SevCE, OsPhysicalAddress: addr(0x999000), Source: "rasdaemon/mce_record"},
		{ErrorSeverity: model.SevCE, OsPhysicalAddress: addr(0x106b2c040), Source: "rasdaemon/mce_record", ErrorDT: at.Add(-24 * time.Hour)},
		// An uncorrected error at the address of a corrected injection is another error.
		{ErrorSeverity: model.SevUE, OsPhysicalAddress: addr(0x106b2c040), Source: "rasdaemon/mce_record", ErrorDT: at.Add(40 * time.Second)},
	}, []*model.Action{
		// The page of ce-3, reported by two sources, and a page nobody planned.
		{Method: "soft", OsPhysicalAddress: addr(0x106b28000), Source: "klog/offline"},
		{Method: "soft", OsPhysicalAddress: addr(0x106b28000), Source: "rasdaemon/memory_failure_event"},
		{Method: "hard", OsPhysicalAddress: addr(0x777000), Source: "klog/offline"},
	})
	res, err := Compare(ctx, s, "h", p)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Checks) != 4 || res.Host != "h" {
		t.Fatalf("%d checks", len(res.Checks))
	}
	by := map[string]*Check{}
	for i := range res.Checks {
		by[res.Checks[i].Injection.ID] = &res.Checks[i]
	}

	c := by["ce-1"]
	if !c.OK() || c.Found != 1 || !reflect.DeepEqual(c.Sources, map[string]int{"rasdaemon/mce_record": 1, "klog/mce": 1}) ||
		len(c.Unreported) != 0 || c.Grades[model.GradeB] != 2 {
		t.Errorf("ce-1: %+v", *c)
	}
	c = by["ce-3"]
	if c.OK() || c.Found != 2 || len(c.Differences) != 1 || c.Differences[0].Field != "channel" || c.Differences[0].Want != "0" ||
		c.Differences[0].Got != "1" || !strings.Contains(c.Differences[0].String(), "channel: injected 0, recorded 1 (rasdaemon/mce_record test#d)") {
		t.Errorf("ce-3: %+v", *c)
	}
	c = by["ce-3-offline"]
	if !c.OK() || c.Found != 1 || !reflect.DeepEqual(c.Sources, map[string]int{"klog/offline": 1, "rasdaemon/memory_failure_event": 1}) {
		t.Errorf("ce-3-offline: %+v", *c)
	}
	c = by["row"]
	if !c.OK() || c.Found != 1 || !reflect.DeepEqual(c.Unreported, []string{"bit"}) || c.Grades[model.GradeA] != 1 {
		t.Errorf("row: %+v", *c)
	}
	if res.OK() || len(res.Failed()) != 1 || res.Failed()[0].Injection.ID != "ce-3" {
		t.Errorf("failed checks: %+v", res.Failed())
	}
	if res.UnplannedErrors != 3 || res.UnplannedOfflines != 1 || len(res.Unplanned) != 4 {
		t.Errorf("unplanned: %d errors, %d offlines, %v", res.UnplannedErrors, res.UnplannedOfflines, res.Unplanned)
	}

	// Nothing recorded: every injection is missing, and a page that should
	// stay in use is as planned.
	p2, err := Read(strings.NewReader("format: 1\nscenario: s\ninjections:\n" +
		"  - id: lost\n    inject: error\n    severity: CE\n    address: 0x5000\n" +
		"  - id: stays\n    inject: page-offline\n    how: soft\n    count: 0\n    address: 0x6000\n" +
		"  - id: hard\n    inject: page-offline\n    how: hard\n    address: 0x106b28000\n"))
	if err != nil {
		t.Fatal(err)
	}
	res, err = Compare(ctx, s, "h", p2)
	if err != nil {
		t.Fatal(err)
	}
	if c := res.Checks[0]; c.OK() || c.Found != 0 || len(c.Sources) != 0 || len(c.Unreported) != 0 {
		t.Errorf("lost: %+v", c)
	}
	if c := res.Checks[1]; !c.OK() || c.Found != 0 {
		t.Errorf("stays: %+v", c)
	}
	// The page is offline, and not the way the plan says.
	if c := res.Checks[2]; c.OK() || c.Found != 1 || len(c.Differences) != 2 || c.Differences[0].Field != "how" || c.Differences[0].Got != "soft" {
		t.Errorf("hard: %+v", c)
	}
	// Errors without an address and without a row are found by their
	// label: the records of the device that have no row and do not
	// contradict the place the plan gives.
	p3, err := Read(strings.NewReader("format: 1\nscenario: s\ninjections:\n" +
		"  - id: media\n    inject: error\n    severity: CE\n    count: 2\n    dpa: 0x2001000\n    device_name: mem0\n    channel: 1\n    rank: 0\n    device: 32\n"))
	if err != nil {
		t.Fatal(err)
	}
	s3 := ledger(t, []*model.Event{
		{ErrorSeverity: model.SevCE, Label: "cxl:mem0", ChannelId: some(1), Rank: some(0), DeviceId: some(32), Source: "rasdaemon/cxl_general_media_event"},
		{ErrorSeverity: model.SevCE, Label: "cxl:mem0", ChannelId: some(1), Rank: some(0), DeviceId: some(32), Source: "rasdaemon/cxl_general_media_event"},
		// Another rank of the same device, and an error of it with a row: other errors.
		{ErrorSeverity: model.SevCE, Label: "cxl:mem0", ChannelId: some(1), Rank: some(1), Source: "rasdaemon/cxl_general_media_event"},
		{ErrorSeverity: model.SevCE, Label: "cxl:mem0", ChannelId: some(1), Rank: some(0), Row: some(7), Column: some(1), Bank: some(1), BankGroup: some(0),
			Source: "rasdaemon/cxl_dram_event", ErrorDT: at.Add(time.Minute)},
	}, nil)
	res, err = Compare(ctx, s3, "h", p3)
	if err != nil {
		t.Fatal(err)
	}
	if c := res.Checks[0]; !c.OK() || c.Found != 2 || res.UnplannedErrors != 2 {
		t.Errorf("media: %+v, %d unplanned", c, res.UnplannedErrors)
	}

	// Several injections at one address: each takes the records of its
	// own moment and no more than it put in. A record too many is beyond
	// the plan.
	p4, err := Read(strings.NewReader("format: 1\nscenario: s\ninjections:\n" +
		"  - id: late\n    time: 2026-11-15T05:20:00Z\n    inject: error\n    severity: Recoverable\n    count: 1\n    address: 0x7000\n    label: \"DIMM 0\"\n" +
		"  - id: early\n    time: 2026-11-15T05:14:12Z\n    inject: error\n    severity: Recoverable\n    count: 2\n    address: 0x7000\n    label: \"DIMM 0\"\n" +
		"  - id: fatal\n    inject: error\n    severity: Fatal\n    address: 0x8000\n"))
	if err != nil {
		t.Fatal(err)
	}
	s4 := ledger(t, []*model.Event{
		// EDAC says "uncorrected" where firmware said "recoverable"; its
		// label adds the bank, and the ledger tied it to the slot.
		{ErrorSeverity: model.SevUE, OsPhysicalAddress: addr(0x7000), Label: "BANK 0 DIMM 0", Slot: "DIMM 0", Source: "rasdaemon/mc_event", ErrorDT: at},
		{ErrorSeverity: model.SevRecoverable, OsPhysicalAddress: addr(0x7000), Label: "DIMM 0", Source: "klog/ghes", ErrorDT: at.Add(time.Second)},
		{ErrorSeverity: model.SevUE, OsPhysicalAddress: addr(0x7000), Label: "BANK 0 DIMM 0", Slot: "DIMM 0", Source: "rasdaemon/mc_event", ErrorDT: at.Add(time.Minute)},
		{ErrorSeverity: model.SevUE, OsPhysicalAddress: addr(0x7000), Label: "BANK 0 DIMM 0", Slot: "DIMM 0", Source: "rasdaemon/mc_event", ErrorDT: at.Add(6 * time.Minute)},
		{ErrorSeverity: model.SevUE, OsPhysicalAddress: addr(0x7000), Label: "BANK 1 DIMM 3", Slot: "DIMM 3", Source: "rasdaemon/mc_event", ErrorDT: at.Add(7 * time.Minute)},
		// The plan says fatal; a record that says the system survived differs.
		{ErrorSeverity: model.SevUE, OsPhysicalAddress: addr(0x8000), Source: "rasdaemon/mc_event", ErrorDT: at},
	}, nil)
	res, err = Compare(ctx, s4, "h", p4)
	if err != nil {
		t.Fatal(err)
	}
	if c := res.Checks[1]; c.Injection.ID != "early" || !c.OK() || c.Found != 2 || c.Sources["rasdaemon/mc_event"] != 2 || c.Sources["klog/ghes"] != 1 {
		t.Errorf("early: %+v", c)
	}
	if c := res.Checks[0]; c.Injection.ID != "late" || !c.OK() || c.Found != 1 || len(c.Sources) != 1 {
		t.Errorf("late: %+v", c)
	}
	if c := res.Checks[2]; c.OK() || c.Found != 1 || len(c.Differences) != 1 || c.Differences[0].Field != "severity" ||
		c.Differences[0].Want != "fatal" || c.Differences[0].Got != "uncorrected" {
		t.Errorf("fatal: %+v", c)
	}
	// The fifth record at the address is one error too many, on another DIMM.
	if res.UnplannedErrors != 1 || len(res.Unplanned) != 1 || !strings.Contains(res.Unplanned[0], "test#e") {
		t.Errorf("unplanned: %d, %v", res.UnplannedErrors, res.Unplanned)
	}

	// Another host has none of it.
	res, err = Compare(ctx, s, "other", p)
	if err != nil || res.OK() || res.Checks[0].Found != 0 {
		t.Errorf("other host: %+v, %v", res, err)
	}
}

func FuzzRead(f *testing.F) {
	f.Add(mcePlan)
	f.Add("format: 1\nscenario: s\ninjections: []\n")
	f.Add("format: 1\nscenario: \"a\\\"\n  - id: x\n")
	f.Fuzz(func(t *testing.T, in string) {
		p, err := Read(strings.NewReader(in))
		if err == nil && (p == nil || p.Scenario == "") {
			t.Errorf("a plan without a scenario was accepted")
		}
	})
}
