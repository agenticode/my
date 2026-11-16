package view

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// source is a made-up source of a run.
type source struct {
	kind   string
	status model.SourceStatus
	fn     func(sink adapter.Sink) error
}

func (f *source) Kind() string   { return f.kind }
func (f *source) Origin() string { return "test:" + f.kind }
func (f *source) Collect(_ context.Context, sink adapter.Sink) adapter.Report {
	rep := adapter.Report{Kind: f.kind, Origin: f.Origin(), Status: f.status}
	if rep.Status == "" {
		rep.Status = model.SourceOK
	}
	if f.fn != nil {
		if err := f.fn(sink); err != nil {
			rep.Status, rep.SinkErr = model.SourceError, err
		}
	}
	return rep
}

// The host of these tests: two DIMMs in A1 and A2, as a BMC lists them.
var (
	sideBoot = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	sideRead = time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
)

// osError is a corrected error rasdaemon recorded for a slot.
func osError(ref, slot string, at time.Time) *model.Event {
	return &model.Event{ErrorDT: at, ErrorSeverity: model.SevCE, Source: "rasdaemon/mc_event", RawRef: ref, Fingerprint: ref, Label: slot}
}

// bmcEntry is an entry of a log of the BMC about a slot.
func bmcEntry(ref, slot string, at time.Time, text string) *model.Event {
	return &model.Event{ErrorDT: at, ErrorSeverity: model.SevCE, ErrorType: text, Source: "redfish/log", RawRef: ref, Fingerprint: ref, Label: slot}
}

// osSide is what the host recorded: a boot, and the given errors.
func osSide(events ...*model.Event) adapter.Adapter {
	return &source{kind: "rasdaemon", fn: func(sink adapter.Sink) error {
		if err := sink.Boot(&model.Boot{BootID: "boot-1", StartedAt: sideBoot, StartQuality: model.TimeExact, Source: "rasdaemon"}); err != nil {
			return err
		}
		for _, e := range events {
			if err := sink.Event(e); err != nil {
				return err
			}
		}
		return nil
	}}
}

// bmcSide is what a BMC said: the two modules with their widths, a BMC
// record with the given logs, settings, readings and entries.
type bmcSide struct {
	widths   [2]int
	logs     []model.BMCLog
	offset   model.Opt[int64]
	classes  []model.BMCClass
	settings map[string]string
	readings []model.BMCMemory
	entries  []*model.Event
}

func (b bmcSide) adapter() adapter.Adapter {
	return &source{kind: "redfish", fn: func(sink adapter.Sink) error {
		if err := sink.Host(&model.Host{SystemVendor: "Dell Inc.", SystemProduct: "PowerEdge R740"}); err != nil {
			return err
		}
		for i, name := range []string{"A1", "A2"} {
			m := &model.Module{ObservedAt: sideRead, Locator: name, Populated: true, Source: "redfish", RawRef: "dump#" + name, Aliases: []string{"DIMM " + name}}
			m.DIMM = model.DIMM{Manufacturer: "Example Memory", PartNumber: "EXM32G29R2X4-B", Serial: "S000" + name, SizeBytes: 32 << 30, Ranks: model.Some(2)}
			if w := b.widths[i]; w > 0 {
				m.DIMM.DeviceWidth = model.Some(w)
			}
			if err := sink.Module(m); err != nil {
				return err
			}
		}
		if err := sink.BMC(&model.BMC{ObservedAt: sideRead, Address: "10.0.0.5", Firmware: "4.40", BIOSVersion: "2.12", Live: true, ClockOffset: b.offset,
			Classes: b.classes, Logs: b.logs, Source: "redfish/manifest", Fingerprint: "bmc"}); err != nil {
			return err
		}
		for name, value := range b.settings {
			if err := sink.BIOSSetting(&model.BIOSSetting{ObservedAt: sideRead, Name: name, Value: value, HasValue: true, Source: "redfish/bios"}); err != nil {
				return err
			}
		}
		for i := range b.readings {
			m := b.readings[i]
			m.ObservedAt, m.Source, m.RawRef, m.Fingerprint = sideRead, "redfish/memory", "dump#m"+m.Label, "reading "+m.Label
			if err := sink.BMCMemory(&m); err != nil {
				return err
			}
		}
		for _, e := range b.entries {
			if err := sink.Event(e); err != nil {
				return err
			}
		}
		return nil
	}}
}

// sel is the log of a BMC that was read with ipmitool: entries and nothing
// about the BMC itself.
func selSide(entries ...*model.Event) adapter.Adapter {
	return &source{kind: "sel", fn: func(sink adapter.Sink) error {
		for _, e := range entries {
			e.Source = "sel/elist"
			if err := sink.Event(e); err != nil {
				return err
			}
		}
		return nil
	}}
}

// inventoryOf runs the sources into a ledger and builds the inventory.
func inventoryOf(t *testing.T, lang i18n.Lang, sources ...adapter.Adapter) *Inventory {
	t.Helper()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := ingest.Run(ctx, s, sources, ingest.Options{Host: "node-a", Now: sideRead}); err != nil {
		t.Fatal(err)
	}
	c, err := Open(ctx, s, lang, engine.Options{Now: sideRead})
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Host("")
	if err != nil {
		t.Fatal(err)
	}
	v, err := c.Inventory(ctx, h, InventoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// said returns the sentences of the comparison as one text.
func said(s *Sides) string {
	var b strings.Builder
	for _, t := range s.Lead {
		b.WriteString(t.Text + "\n")
	}
	for _, r := range s.Rows {
		b.WriteString(r.Text.Text + "\n")
		if r.Count != nil {
			b.WriteString(r.Count.Text.Text + "\n")
		}
	}
	for _, g := range s.Candidates {
		b.WriteString(g.Head.Text + "\n")
		for _, t := range g.Items {
			b.WriteString("- " + t.Text + "\n")
		}
	}
	return b.String()
}

func wantSaid(t *testing.T, s *Sides, parts ...string) {
	t.Helper()
	text := said(s)
	for _, p := range parts {
		if !strings.Contains(text, p) {
			t.Errorf("the comparison does not say %q:\n%s", p, text)
		}
	}
}

func day(n int) time.Time { return sideBoot.Add(time.Duration(n) * 24 * time.Hour) }

// Errors the OS has and the BMC log lacks: what the BIOS settings and the
// logs on record say that can explain it is listed, as candidates.
func TestSidesOSOnly(t *testing.T) {
	bmc := bmcSide{
		settings: map[string]string{"CorrEccSmi": "Disabled", "CECriticalSEL": "Enabled", "MemPatrolScrub": "Standard"},
		logs: []model.BMCLog{
			{ID: "Sel", Path: "/redfish/v1/Managers/1/LogServices/Sel", Entries: 1024, Total: model.Some(1024), Max: model.Some(1024), Policy: "WrapsWhenFull",
				First: day(2), Last: day(8)},
			{ID: "Lclog", Path: "/redfish/v1/Managers/1/LogServices/Lclog", Entries: 50, Total: model.Some(300), Truncated: true, First: day(3), Last: day(8)},
		},
		entries: []*model.Event{bmcEntry("sel/1", "A2", day(4), "correctable ECC logging limit reached")},
	}
	v := inventoryOf(t, i18n.EN, osSide(osError("mc/1", "A1", day(1)), osError("mc/2", "A1", day(5)), osError("mc/3", "A1", day(6))), bmc.adapter())
	s := v.Sides
	// The period both cover begins with the oldest entry of the BMC logs
	// and ends when the BMC was read. The error of day 1 lies before it.
	if !s.Common || !s.From.Equal(day(2)) || !s.To.Equal(sideRead) {
		t.Fatalf("period %s to %s (common %v)", s.From, s.To, s.Common)
	}
	if len(s.Rows) != 2 || s.Rows[0].Slot != "A1" || s.Rows[0].Outcome != SidesOSOnly || s.Rows[0].OS != 2 || s.Rows[0].BMC != 0 ||
		s.Rows[1].Slot != "A2" || s.Rows[1].Outcome != SidesBMCOnly || s.Rows[1].BMC != 1 {
		t.Fatalf("rows = %+v", s.Rows)
	}
	wantSaid(t, s,
		"OS and BMC side by side, 2026-11-03 00:00 UTC to 2026-11-10 00:00 UTC. The records of the operating system begin 2026-11-01 00:00 UTC, the logs of the BMC 2026-11-03 00:00 UTC.",
		"A1: the OS has 2 corrected errors, the logs of the BMC have none",
		"Errors the OS has and the logs of the BMC lack. Candidates for the reason, none established:",
		"- the BIOS does not log corrected errors to the BMC: CorrEccSmi = Disabled",
		"- the BIOS logs an event to the BMC when corrected errors cross a threshold (CECriticalSEL = Enabled); errors below the threshold leave no entry",
		"- the firmware stopped logging corrected errors on 2026-11-05 00:00 UTC: the logging limit was reached",
		"- the log Sel is full (1,024 entries) and overwrites its oldest entries",
		"- 50 of the 300 entries of the log Lclog were read",
		"Entries of the BMC the OS has no record of. Candidates for the reason, none established:",
		"- the firmware can handle corrected errors itself and log them to the BMC without reporting them to the operating system (firmware-first handling)")
	if strings.Contains(said(s), "no such setting of this BIOS is on record") {
		t.Errorf("a threshold setting is on record and the comparison says none is:\n%s", said(s))
	}
	// The table counts over the same period.
	a1 := v.Rows[0]
	if a1.Slot != "A1" || a1.CEOS.V != 2 || a1.CEBMC.V != 0 || len(a1.BMCNames) != 0 {
		t.Errorf("A1 = %+v", a1)
	}
	if got := v.Legend[1].Text; !strings.Contains(got, "both from 2026-11-03 00:00 UTC to 2026-11-10 00:00 UTC, the period both sides cover") {
		t.Errorf("legend = %q", got)
	}
}

// Entries of the BMC the OS has no record of, on a host without rasdaemon
// records and with a kernel log that was denied.
func TestSidesBMCOnly(t *testing.T) {
	bmc := bmcSide{
		logs:    []model.BMCLog{{ID: "Sel", Entries: 3, First: day(2), Last: day(6)}},
		offset:  model.Some[int64](-290),
		entries: []*model.Event{bmcEntry("sel/1", "A2", day(4), "correctable ECC"), bmcEntry("sel/2", "A2", day(6), "correctable ECC")},
	}
	edac := &source{kind: "edac-sysfs", fn: func(sink adapter.Sink) error {
		return sink.Boot(&model.Boot{BootID: "boot-1", StartedAt: sideBoot, StartQuality: model.TimeExact, Source: "hostinfo"})
	}}
	v := inventoryOf(t, i18n.EN, &source{kind: "rasdaemon", status: model.SourceAbsent}, &source{kind: "klog", status: model.SourceUnreadable}, edac, bmc.adapter())
	s := v.Sides
	if len(s.Rows) != 1 || s.Rows[0].Outcome != SidesBMCOnly || s.Rows[0].BMC != 2 || s.Rows[0].OS != 0 {
		t.Fatalf("rows = %+v", s.Rows)
	}
	wantSaid(t, s,
		"The clock of the BMC was 5 minutes behind the machine that read it.",
		"A2: the logs of the BMC have 2 entries about corrected errors, the OS has no record",
		"- the firmware can handle corrected errors itself",
		"- rasdaemon keeps no records on this host: the OS side is the kernel log and the EDAC counters",
		"- the source klog of the operating system was not read (")
	if len(s.OS.Sources) != 1 || s.OS.Sources[0] != "edac-sysfs" || len(s.BMC.Sources) != 1 {
		t.Errorf("sources: OS %v, BMC %v", s.OS.Sources, s.BMC.Sources)
	}
	// The newest entries stand below the module.
	a2 := v.Rows[1]
	if len(a2.Events) != 2 || !a2.Events[1].At.Equal(day(6)) || a2.CEBMC.V != 2 || a2.CEOS.V != 0 {
		t.Errorf("A2 = %+v", a2)
	}
}

// Both sides have records: an entry of the BMC that stands at the time of
// an OS record is the BMC's report of that error. The BMC's count since
// the reset is set against what the OS recorded since then.
func TestSidesBoth(t *testing.T) {
	reset := day(3)
	bmc := bmcSide{
		logs: []model.BMCLog{{ID: "Sel", Entries: 2, First: day(2), Last: day(6)}},
		entries: []*model.Event{
			bmcEntry("sel/1", "A1", day(5).Add(40*time.Second), "correctable ECC"), // the BMC's report of mc/2
			bmcEntry("sel/2", "A1", day(6), "correctable ECC"),
		},
		readings: []model.BMCMemory{
			{Label: "A1", Metrics: "ok", CECurrent: model.Some[int64](2), CELifetime: model.Some[int64](40), CurrentSince: reset},
			{Label: "A2", Metrics: "ok", CECurrent: model.Some[int64](0), CELifetime: model.Some[int64](0), CurrentSince: reset},
		},
	}
	v := inventoryOf(t, i18n.EN, osSide(osError("mc/1", "A1", day(1)), osError("mc/2", "A1", day(5)), osError("mc/3", "A1", day(7))), bmc.adapter())
	s := v.Sides
	if len(s.Rows) != 1 {
		t.Fatalf("rows = %+v", s.Rows)
	}
	r := s.Rows[0]
	if r.Outcome != SidesBoth || r.OS != 2 || r.BMC != 2 || r.Matched != 1 || r.Count == nil || r.Count.BMC != 2 || r.Count.OS != 2 || !r.Count.Since.Equal(reset) {
		t.Fatalf("row = %+v, count %+v", r, r.Count)
	}
	wantSaid(t, s,
		"A1: the OS has 2 corrected errors, the logs of the BMC 2 entries; 1 of the entries stands at the time of an OS record",
		"A1: since the reset of 2026-11-04 00:00 UTC the BMC counts 2 corrected errors, and the OS has as many",
		"The lifetime count of the BMC covers the life of a module")
	if len(s.Candidates) != 0 {
		t.Errorf("candidates where nothing differs: %+v", s.Candidates)
	}
	a1 := v.Rows[0]
	if a1.BMCCount == nil || a1.BMCCount.CELifetime.V != 40 || len(a1.Lines) != 3 ||
		a1.Lines[0].Text != "corrected errors the BMC counts: 40 over the life of the module, 2 since the reset of 2026-11-04 00:00 UTC" ||
		a1.Lines[2].Text != "BMC log 2026-11-07 00:00 UTC: correctable ECC" {
		t.Errorf("A1 = %+v", a1)
	}
	// A module the BMC counted nothing for has no line about counts.
	for _, l := range v.Rows[1].Lines {
		if strings.Contains(l.Text, "counts") {
			t.Errorf("A2 has a line about counts: %q", l.Text)
		}
	}
}

// Two sides that cover no common period are not compared over one: each
// count is of everything the side has.
func TestSidesApart(t *testing.T) {
	bmc := bmcSide{
		logs:    []model.BMCLog{{ID: "Sel", Entries: 1, First: day(40), Last: day(40)}},
		entries: []*model.Event{bmcEntry("sel/1", "A1", day(40), "correctable ECC")},
	}
	// The BMC was read on day 9, and its log begins on day 40 by its own
	// clock.
	v := inventoryOf(t, i18n.EN, osSide(osError("mc/1", "A1", day(1))), bmc.adapter())
	s := v.Sides
	if s.Common || !s.From.IsZero() {
		t.Fatalf("period %s to %s", s.From, s.To)
	}
	wantSaid(t, s, "cover no common period: the counts are those of everything each side has")
	if a1 := v.Rows[0]; a1.CEOS.V != 1 || a1.CEBMC.V != 1 {
		t.Errorf("A1 = %+v", a1)
	}
	if got := v.Legend[1].Text; !strings.Contains(got, "the two cover no common period") {
		t.Errorf("legend = %q", got)
	}
}

// A log of the BMC that was read with ipmitool is the BMC side as well;
// there is no record of the BMC itself then.
func TestSidesWithASELListing(t *testing.T) {
	// The BMC that was read over Redfish offers no count per module.
	redfish := bmcSide{readings: []model.BMCMemory{{Label: "A1", Metrics: "not offered"}, {Label: "A2", Metrics: "not offered"}}}
	v := inventoryOf(t, i18n.EN, osSide(osError("mc/1", "A1", day(5))), redfish.adapter(),
		selSide(bmcEntry("sel/1", "A1", day(2), "correctable ECC"), bmcEntry("sel/2", "A1", day(5).Add(time.Minute), "correctable ECC")))
	s := v.Sides
	if !s.Common || !s.From.Equal(day(2)) || len(s.BMC.Sources) != 2 {
		t.Fatalf("period from %s (common %v), BMC sources %v", s.From, s.Common, s.BMC.Sources)
	}
	if len(s.Rows) != 1 || s.Rows[0].Outcome != SidesBoth || s.Rows[0].BMC != 2 || s.Rows[0].Matched != 1 {
		t.Errorf("rows = %+v", s.Rows)
	}
	wantSaid(t, s, "The BMC gives no error count per module (memory metrics: not offered).")
}

// The RAS column says per module what a setting does for it when the
// modules differ in what decides it: ADDDC acts on x4 devices.
func TestInventoryRAS(t *testing.T) {
	settings := map[string]string{"MemPatrolScrub": "Standard", "AdddcSetting": "Enabled", "PostPackageRepair": "HardPPR", "MemOpMode": "OptimizerMode"}
	mixed := inventoryOf(t, i18n.EN, bmcSide{widths: [2]int{4, 8}, settings: settings}.adapter())
	if mixed.RASCommon != "" || mixed.Rows[0].RAS != "patrol scrub, ADDDC, PPR" || mixed.Rows[1].RAS != "patrol scrub, ADDDC (no effect on x8), PPR" {
		t.Errorf("RAS: common %q, rows %q and %q", mixed.RASCommon, mixed.Rows[0].RAS, mixed.Rows[1].RAS)
	}
	if got := mixed.Rows[1].Settings[1]; got.Setting != "adddc" || got.State != "on" || got.Effect != "none" {
		t.Errorf("ADDDC on the x8 module = %+v", got)
	}
	if mixed.Rows[0].Org != "2Rx4" || mixed.Rows[1].Org != "2Rx8" || len(mixed.Notes) != 0 {
		t.Errorf("organization %q and %q, notes %v", mixed.Rows[0].Org, mixed.Rows[1].Org, mixed.Notes)
	}
	if !strings.Contains(mixed.Legend[0].Text, "RAS lists the memory RAS settings") {
		t.Errorf("legend = %q", mixed.Legend[0].Text)
	}

	// Modules alike: the settings stand once above the table.
	alike := inventoryOf(t, i18n.KO, bmcSide{settings: settings}.adapter())
	if alike.RASCommon != "패트롤 스크럽, ADDDC (폭 모름), PPR" || alike.Rows[0].Org != "2R" {
		t.Errorf("RAS common %q, organization %q", alike.RASCommon, alike.Rows[0].Org)
	}
	var lead []string
	for _, l := range alike.Lead {
		lead = append(lead, l.Text)
	}
	if text := strings.Join(lead, "\n"); !strings.Contains(text, "모든 DIMM에 적용 중인 BIOS 메모리 RAS 설정: 패트롤 스크럽, ADDDC (폭 모름), PPR") {
		t.Errorf("lead = %s", text)
	}
	if len(alike.Notes) != 1 || alike.Notes[0].Code != "inv.note_width" {
		t.Errorf("notes = %+v", alike.Notes)
	}

	// A mode that is no ADDDC, and nothing on: the lead says so.
	mirror := inventoryOf(t, i18n.EN, bmcSide{settings: map[string]string{"MemOpMode": "MirrorMode", "MemPatrolScrub": "Disabled"}}.adapter())
	if mirror.RASCommon != "mirroring" {
		t.Errorf("RAS common = %q", mirror.RASCommon)
	}
	off := inventoryOf(t, i18n.EN, bmcSide{settings: map[string]string{"MemPatrolScrub": "Disabled"}}.adapter())
	if off.RASCommon != "" || !strings.Contains(fmt.Sprint(off.Lead), "none of the memory RAS settings the profile reads is on") {
		t.Errorf("RAS common %q, lead %v", off.RASCommon, off.Lead)
	}

	// No settings: whether the BMC gave its BIOS settings and none of them
	// is about memory, or the BIOS was not read.
	for _, tt := range []struct {
		classes []model.BMCClass
		code    string
	}{
		{[]model.BMCClass{{Class: "bios", State: "ok"}}, "inv.ras_none_memory"},
		{[]model.BMCClass{{Class: "bios", State: "timeout"}}, "inv.ras_none"},
		{nil, "inv.ras_none"},
	} {
		v := inventoryOf(t, i18n.EN, bmcSide{classes: tt.classes}.adapter())
		found := false
		for _, l := range v.Lead {
			found = found || l.Code == tt.code
		}
		if !found {
			t.Errorf("classes %v: lead %v, want %s", tt.classes, v.Lead, tt.code)
		}
	}
}

// A clock that is days off is told in days.
func TestSidesClockOffDays(t *testing.T) {
	v := inventoryOf(t, i18n.EN, bmcSide{offset: model.Some[int64](-4266*86400 - 3600)}.adapter())
	wantSaid(t, v.Sides, "The clock of the BMC was 4,266 days behind the machine that read it; it may never have been set.")
	v = inventoryOf(t, i18n.EN, bmcSide{offset: model.Some[int64](2*86400 + 7200)}.adapter())
	wantSaid(t, v.Sides, "The clock of the BMC was 2 days ahead of the machine that read it")
}

// Every message of the inventory exists in both languages.
func TestInventoryMessagesAreComplete(t *testing.T) {
	for code, m := range InventoryMessages {
		if strings.TrimSpace(m.EN) == "" || strings.TrimSpace(m.KO) == "" {
			t.Errorf("%s lacks a language: %+v", code, m)
		}
	}
}
