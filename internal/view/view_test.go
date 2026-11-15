package view

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/scan"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/synth"
	"github.com/agenticode/my/internal/testledger"
	"github.com/agenticode/my/internal/testutil"
)

func TestMain(m *testing.M) { testledger.Main(m) }

var ctx = context.Background()

// now is the time findings are stamped with in tests.
var now = time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)

// open returns the case of a built-in scenario and its only host.
func open(t *testing.T, scenario string, fl synth.Flavor, lang i18n.Lang) (*Case, *engine.HostResult) {
	t.Helper()
	c, err := Open(ctx, testledger.Open(t, scenario, 1, fl), lang, engine.Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Host("")
	if err != nil {
		t.Fatal(err)
	}
	return c, h
}

func find(t *testing.T, c *Case, h *engine.HostResult, ref string) *engine.DIMMResult {
	t.Helper()
	d, err := c.Find(ctx, h, ref)
	if err != nil {
		t.Fatalf("find %q: %v", ref, err)
	}
	return d
}

// golden compares the JSON form of a view with a golden file: the JSON is
// what --json prints, so its field names are an interface.
func golden(t *testing.T, name string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	testutil.Golden(t, name, append(b, '\n'))
}

func TestFind(t *testing.T) {
	c, h := open(t, "demo", "", i18n.EN)
	tests := []struct {
		ref  string
		slot string
		err  error
	}{
		{"CPU0_C1", "CPU0_C1", nil},
		{"cpu0_c1", "CPU0_C1", nil},
		{" CPU1_D1 ", "CPU1_D1", nil},
		{"3B9C56", "CPU0_C1", nil},    // serial number
		{"3b9c56", "CPU0_C1", nil},    // in any case
		{"..9C56", "CPU0_C1", nil},    // its end, as the ranking prints it
		{"9C5", "", ErrNoSuchDIMM},    // too short to be a serial number
		{"CPU0_C2", "", ErrEmptySlot}, // a slot without a module
		{"CPU9_Z9", "", ErrNoSuchDIMM},
		{"", "", ErrNoSuchDIMM},
	}
	for _, tt := range tests {
		d, err := c.Find(ctx, h, tt.ref)
		switch {
		case tt.err != nil:
			if !errors.Is(err, tt.err) {
				t.Errorf("Find(%q): error %v, want %v", tt.ref, err, tt.err)
			}
		case err != nil:
			t.Errorf("Find(%q): %v", tt.ref, err)
		case d.Slot != tt.slot:
			t.Errorf("Find(%q) = %s, want %s", tt.ref, d.Slot, tt.slot)
		}
	}
	// The ledger identity selects a DIMM as well.
	uid := find(t, c, h, "CPU0_C1").UID
	if d := find(t, c, h, uid); d.Slot != "CPU0_C1" {
		t.Errorf("Find(%q) = %s", uid, d.Slot)
	}
}

func TestFindByEDACLabel(t *testing.T) {
	// A host tree has EDAC labels beside the SMBIOS locators.
	c, h := open(t, "demo", synth.FlavorIntel, i18n.EN)
	aliases, err := c.Store().SlotAliases(ctx, h.Host)
	if err != nil {
		t.Fatal(err)
	}
	var label string
	for alias, slot := range aliases {
		if slot == "CPU0_C1" && strings.HasPrefix(alias, "label:") && alias != "label:CPU0_C1" {
			label = strings.TrimPrefix(alias, "label:")
		}
	}
	if label == "" {
		t.Skip("the ledger has no EDAC label alias for CPU0_C1")
	}
	if d := find(t, c, h, label); d.Slot != "CPU0_C1" {
		t.Errorf("Find(%q) = %s, want CPU0_C1", label, d.Slot)
	}
}

func TestHostSelection(t *testing.T) {
	c, h := open(t, "demo", "", i18n.EN)
	if h.Host != "r12-n034" {
		t.Fatalf("host = %q", h.Host)
	}
	if got, err := c.Host("R12-N034"); err != nil || got != h {
		t.Errorf("Host in another case: %v", err)
	}
	if _, err := c.Host("nope"); !errors.Is(err, ErrNoSuchHost) {
		t.Errorf("unknown host: %v", err)
	}
	rows := c.Hosts()
	if len(rows) != 1 || rows[0].Grade != model.VerdictReplaceNow || rows[0].Worst != "CPU0_C1" || rows[0].DIMMs != 16 || rows[0].Findings != 2 {
		t.Errorf("Hosts() = %+v", rows)
	}
}

func TestOverviewDemo(t *testing.T) {
	c, h := open(t, "demo", "", i18n.EN)
	ov, err := c.Overview(ctx, h, false)
	if err != nil {
		t.Fatal(err)
	}
	hd := ov.Host
	if hd.CE != 1284 || hd.UE != 1 || hd.GradeA != 1285 || hd.GradeB+hd.GradeC != 0 || hd.Grade != model.VerdictReplaceNow || hd.Boots != 4 {
		t.Errorf("header = %+v", hd)
	}
	sm := ov.SlotMap
	if sm.Slots != 32 || sm.Populated != 16 || len(sm.Rows) != 2 {
		t.Fatalf("slot map: %d slots, %d populated, %d rows", sm.Slots, sm.Populated, len(sm.Rows))
	}
	states := map[string]string{}
	for _, r := range sm.Rows {
		if len(r.Channels) != 8 {
			t.Errorf("row %s has %d channels, want 8", r.Label, len(r.Channels))
		}
		for _, ch := range r.Channels {
			if len(ch.Slots) != 2 {
				t.Errorf("a channel of %s has %d slots, want 2", r.Label, len(ch.Slots))
			}
			for _, cell := range ch.Slots {
				// Row label and cell label together are the slot name.
				if r.Label+"_"+cell.Label != cell.Slot {
					t.Errorf("labels %q + %q do not give %q", r.Label, cell.Label, cell.Slot)
				}
				states[cell.Slot] = cell.State
			}
		}
	}
	for slot, want := range map[string]string{"CPU0_C1": "replace-now", "CPU1_D1": "observe", "CPU0_A1": "ok", "CPU0_C2": SlotEmpty} {
		if states[slot] != want {
			t.Errorf("state of %s = %q, want %q", slot, states[slot], want)
		}
	}
	if len(ov.Ranking) != 2 || ov.Quiet != 14 {
		t.Fatalf("ranking has %d rows, %d quiet", len(ov.Ranking), ov.Quiet)
	}
	r := ov.Ranking[0]
	if r.Slot != "CPU0_C1" || r.CE != 1231 || r.UE != 1 || r.Mode != model.ModeRow || r.Direction != engine.TrendRising || len(r.Trend) != TrendBuckets {
		t.Errorf("first row = %+v", r)
	}
	// The trend buckets hold the errors of the last 30 days: the same days
	// a timeline of 30 days draws.
	d := find(t, c, h, "CPU0_C1")
	var sum int64
	for _, n := range r.Trend {
		sum += n
	}
	month, err := c.Timeline(ctx, h, d, TimelineOptions{Days: TrendDays})
	if err != nil {
		t.Fatal(err)
	}
	if sum != month.CE+month.UE || sum == 0 {
		t.Errorf("trend buckets sum to %d; the last %d days hold %d errors", sum, TrendDays, month.CE+month.UE)
	}
	// With all set every DIMM is listed.
	all, err := c.Overview(ctx, h, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Ranking) != 16 || all.Quiet != 0 {
		t.Errorf("with all: %d rows, %d quiet", len(all.Ranking), all.Quiet)
	}
	golden(t, "demo-overview.json", ov)
}

func TestOverviewSaysWhatWasNotRead(t *testing.T) {
	c, h := open(t, "demo", synth.FlavorAMD, i18n.EN)
	ov, err := c.Overview(ctx, h, false)
	if err != nil {
		t.Fatal(err)
	}
	if ov.Host.GradeB != 1285 || ov.Host.GradeA != 0 {
		t.Errorf("grades = A %d, B %d", ov.Host.GradeA, ov.Host.GradeB)
	}
	kinds := map[string]bool{}
	for _, s := range ov.Sources {
		kinds[s.Kind] = true
	}
	for _, k := range []string{"hostinfo", "rasdaemon", "klog", "edac-sysfs", "smbios"} {
		if !kinds[k] {
			t.Errorf("source inventory lacks %s: %+v", k, ov.Sources)
		}
	}
	if ov.Ranking[0].Mode != model.ModeRow || !ov.Ranking[0].Estimated {
		t.Errorf("mode on a platform without rows: %+v", ov.Ranking[0])
	}
}

func TestSlotNames(t *testing.T) {
	for _, tt := range []struct {
		names []string
		want  string
	}{
		{[]string{"CPU0_A1", "CPU0_B1", "CPU0_H2"}, "CPU0_"},
		{[]string{"CPU0_C1", "CPU0_C2"}, "CPU0_"},            // never inside a word
		{[]string{"DIMM 10", "DIMM 11", "DIMM 12"}, "DIMM "}, // never inside a number
		{[]string{"P1-DIMMA1", "P1-DIMMB1"}, "P1-"},
		{[]string{"A1", "A2", "A12"}, ""},
		{[]string{"CPU0_A1"}, "CPU0_"},
		{[]string{"DIMM_", "DIMM_A"}, ""}, // nothing would be left of a name
		{nil, ""},
	} {
		if got := commonPrefix(tt.names); got != tt.want {
			t.Errorf("commonPrefix(%q) = %q, want %q", tt.names, got, tt.want)
		}
	}
	if namePrefix("CPU0_C1") != "CPU0_" || namePrefix("A1") != "" || namePrefix("DIMM_") != "" {
		t.Error("namePrefix")
	}
	for _, tt := range []struct {
		a, b string
		less bool
	}{{"A2", "A10", true}, {"A10", "A2", false}, {"A1", "B1", true}, {"DIMM 9", "DIMM 10", true}, {"A1", "A1", false}, {"A01", "A1", false}, {"A", "A1", true}} {
		if got := naturalLess(tt.a, tt.b); got != tt.less {
			t.Errorf("naturalLess(%q, %q) = %v", tt.a, tt.b, got)
		}
	}
}

func TestSlotMapWithoutTopology(t *testing.T) {
	// Slots the ledger knows by name only are grouped by what the names
	// share; two groups with one label keep their whole names.
	h := &engine.HostResult{Host: "h"}
	slots := []model.Slot{
		{Host: "h", Name: "CPU1_B1", Populated: model.Some(true)},
		{Host: "h", Name: "CPU0_A10", Populated: model.Some(true)},
		{Host: "h", Name: "CPU0_A2", Populated: model.Some(false)},
		{Host: "h", Name: "A1"},
	}
	sm := slotMap(h, slots)
	if len(sm.Rows) != 2 || sm.Populated != 2 {
		t.Fatalf("rows = %+v", sm.Rows)
	}
	got := map[string][]string{}
	for _, r := range sm.Rows {
		for _, ch := range r.Channels {
			for _, cell := range ch.Slots {
				got[r.Label] = append(got[r.Label], cell.Label+"="+cell.State)
			}
		}
	}
	// CPU1_B1 is alone under its prefix: it keeps its whole name and joins
	// the row of the names that share nothing.
	want := map[string][]string{"": {"A1=unknown", "CPU1_B1=ok"}, "CPU0": {"A2=empty", "A10=ok"}}
	for label, cells := range want {
		if strings.Join(got[label], " ") != strings.Join(cells, " ") {
			t.Errorf("row %q = %v, want %v", label, got[label], cells)
		}
	}

	// Sockets whose slot names share one prefix cannot be told apart by it.
	slots = []model.Slot{
		{Host: "h", Name: "DIMM_A1", Socket: model.Some(0), Channel: model.Some(0), Position: model.Some(0)},
		{Host: "h", Name: "DIMM_A2", Socket: model.Some(0), Channel: model.Some(0), Position: model.Some(1)},
		{Host: "h", Name: "DIMM_J1", Socket: model.Some(1), Channel: model.Some(0), Position: model.Some(0)},
		{Host: "h", Name: "DIMM_J2", Socket: model.Some(1), Channel: model.Some(0), Position: model.Some(1)},
	}
	sm = slotMap(h, slots)
	for _, r := range sm.Rows {
		if r.Label != "" || len(r.Channels) != 1 || len(r.Channels[0].Slots) != 2 || r.Channels[0].Slots[0].Label != r.Channels[0].Slots[0].Slot {
			t.Errorf("row of socket %v = %+v", r.Socket, r)
		}
	}
}

func TestCard(t *testing.T) {
	c, h := open(t, "demo", "", i18n.EN)
	card, err := c.Card(ctx, h, find(t, c, h, "CPU0_C1"))
	if err != nil {
		t.Fatal(err)
	}
	if card.Grade != model.VerdictReplaceNow || card.ConfidenceLevel != "high" || strings.Join(card.Rules, " ") != "R-UE-01 R-ROW-03 R-REP-02" {
		t.Errorf("card = %+v", card)
	}
	if len(card.Evidence) != 5 || card.Evidence[0].Rule != "R-UE-01" || card.Evidence[0].Records != 1 || card.Evidence[0].First == "" {
		t.Errorf("evidence = %+v", card.Evidence)
	}
	if len(card.Checks) != 3 || card.Next == nil || len(card.Baseline) != 2 || len(card.Factors) == 0 || len(card.Faults) != 1 {
		t.Errorf("checks %d, baseline %d, factors %d, faults %d", len(card.Checks), len(card.Baseline), len(card.Factors), len(card.Faults))
	}
	if card.Moves != 0 || card.Since.IsZero() || !card.Present {
		t.Errorf("since %v, moves %d", card.Since, card.Moves)
	}
	golden(t, "demo-card.json", card)

	// The same card in Korean: codes and numbers stay, the sentences change.
	ko, hk := open(t, "demo", "", i18n.KO)
	kcard, err := ko.Card(ctx, hk, find(t, ko, hk, "CPU0_C1"))
	if err != nil {
		t.Fatal(err)
	}
	if kcard.GradeText != "즉시 교체" || kcard.Label != "소견" || kcard.Evidence[0].Code != card.Evidence[0].Code || kcard.Evidence[0].Text.Text == card.Evidence[0].Text.Text {
		t.Errorf("Korean card: %+v", kcard.Evidence[0])
	}
	if !strings.Contains(kcard.Baseline[0].Text, "백분위") {
		t.Errorf("Korean baseline: %q", kcard.Baseline[0].Text)
	}

	// A DIMM without errors has a card too: it says that nothing is known.
	quiet, err := c.Card(ctx, h, find(t, c, h, "CPU0_A1"))
	if err != nil {
		t.Fatal(err)
	}
	if quiet.Grade != model.VerdictOK || len(quiet.Evidence) != 0 || quiet.Evidence == nil || quiet.Faults == nil {
		t.Errorf("card of a quiet DIMM: %+v", quiet)
	}
}

func TestCardCountsSlotChanges(t *testing.T) {
	c, h := open(t, "moved-dimm", "", i18n.EN)
	card, err := c.Card(ctx, h, find(t, c, h, "CPU0_D1"))
	if err != nil {
		t.Fatal(err)
	}
	if card.Moves != 1 || card.Since.Format("2006-01-02") != "2026-10-01" {
		t.Errorf("moves = %d, since = %v; the DIMM moved to CPU0_D1 on 2026-10-01", card.Moves, card.Since)
	}
}

func TestTopology(t *testing.T) {
	c, h := open(t, "demo", "", i18n.EN)
	d := find(t, c, h, "CPU0_C1")
	topo, err := c.Topology(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if topo.Total != d.Features.CE+d.Features.UE || len(topo.Levels) != 4 {
		t.Fatalf("total %d, %d levels", topo.Total, len(topo.Levels))
	}
	want := []struct{ name, first string }{{LevelRank, "rank0"}, {LevelDevice, "dev7"}, {LevelBank, "bg0 bank3"}, {LevelRow, "0x1a2f"}}
	for i, lv := range topo.Levels {
		if lv.Name != want[i].name || !lv.Known || lv.Segments[0].Label != want[i].first {
			t.Errorf("level %d = %+v", i, lv)
		}
		// Every level splits all errors of the DIMM.
		var sum int64
		var share float64
		for _, s := range lv.Segments {
			sum += s.Errors
			share += s.Share
			if i == 0 && s.Parent != -1 {
				t.Errorf("top level segment has parent %d", s.Parent)
			}
			if i > 0 && (s.Parent < 0 || s.Parent >= len(topo.Levels[i-1].Segments)) {
				t.Errorf("level %s: parent %d out of range", lv.Name, s.Parent)
			}
		}
		if sum != topo.Total || math.Abs(share-1) > 1e-9 {
			t.Errorf("level %s sums to %d errors, share %.3f", lv.Name, sum, share)
		}
	}
	if rows := topo.Levels[3].Segments; len(rows) != 2 || rows[1].Label != "0x1a30" || rows[0].Share < 0.95 {
		t.Errorf("rows = %+v", rows)
	}
	golden(t, "demo-topology.json", topo)

	// A bank fault touches hundreds of rows: the small ones are folded.
	c, h = open(t, "bank", "", i18n.EN)
	topo, err = c.Topology(ctx, h.DIMMs[0])
	if err != nil {
		t.Fatal(err)
	}
	rows := topo.Levels[3].Segments
	if len(rows) != maxBranches+1 || rows[maxBranches].Others < 100 || rows[maxBranches].Label != "" {
		t.Errorf("row level of a bank fault has %d segments, the last %+v", len(rows), rows[len(rows)-1])
	}

	// A platform that reports addresses only: the levels below the rank
	// are not known, and say so.
	c, h = open(t, "demo", synth.FlavorAMD, i18n.EN)
	topo, err = c.Topology(ctx, find(t, c, h, "CPU0_C1"))
	if err != nil {
		t.Fatal(err)
	}
	if !topo.Levels[0].Known || topo.Levels[1].Known || topo.Levels[2].Known || topo.Levels[3].Known {
		t.Errorf("known levels on an address-only platform: %v %v %v %v", topo.Levels[0].Known, topo.Levels[1].Known, topo.Levels[2].Known, topo.Levels[3].Known)
	}
	if s := topo.Levels[3].Segments; len(s) != 1 || !s[0].Unknown || s[0].Label != "?" {
		t.Errorf("row level = %+v", s)
	}

	// A DIMM without errors has an empty topology.
	c, h = open(t, "demo", "", i18n.EN)
	topo, err = c.Topology(ctx, find(t, c, h, "CPU0_A1"))
	if err != nil || topo.Total != 0 || len(topo.Levels) != 4 || topo.Levels[0].Segments == nil {
		t.Errorf("topology of a quiet DIMM: %+v, %v", topo, err)
	}
}

func TestAddressMapRows(t *testing.T) {
	c, h := open(t, "demo", "", i18n.EN)
	d := find(t, c, h, "CPU0_C1")
	m, err := c.AddressMap(ctx, h, d, MapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if m.Bank == nil || m.Bank.Label != "rank0 dev7 bg0 bank3" || m.Mode != MapRows || m.RowsPerLine != 1 || m.Columns != 2048 || m.ColumnsPerCell != 32 {
		t.Fatalf("map = %+v", m)
	}
	if m.Located != 1232 || m.Unlocated != 0 || m.BankCount != 1 {
		t.Errorf("located %d, unlocated %d, banks %d", m.Located, m.Unlocated, m.BankCount)
	}
	// Three quiet rows above and below the two rows of the fault.
	if len(m.Lines) != 8 || m.Lines[0].Row != 0x1a2c || m.Lines[7].Row != 0x1a33 {
		t.Fatalf("lines from %#x to %#x, %d of them", m.Lines[0].Row, m.Lines[len(m.Lines)-1].Row, len(m.Lines))
	}
	var total int64
	for _, ln := range m.Lines {
		total += ln.Errors
		var sum int64
		for _, n := range ln.Cells {
			sum += n
		}
		if sum != ln.Errors || len(ln.Cells) != 64 {
			t.Errorf("row %#x: cells sum to %d, line says %d, %d cells", ln.Row, sum, ln.Errors, len(ln.Cells))
		}
	}
	if total != 1232 {
		t.Errorf("the lines hold %d errors, want 1,232", total)
	}
	top, next := m.Lines[3], m.Lines[4]
	if top.Row != 0x1a2f || top.Note == nil || top.Note.Code != "map.note.fault" || len(top.UE) != 1 || top.UE[0] != 0x238/32 {
		t.Errorf("row of the fault = %+v", top)
	}
	if next.Note == nil || next.Note.Code != "map.note.neighbour" || len(next.UE) != 0 {
		t.Errorf("row next to it = %+v", next)
	}
	if len(m.Faults) != 1 || m.Faults[0].Mode != model.ModeRow || m.Faults[0].Rows != 2 || m.Faults[0].Columns != 9 || m.Faults[0].UE != 1 || len(m.Faults[0].DQ) != 2 {
		t.Errorf("faults = %+v", m.Faults)
	}
	golden(t, "demo-map.json", m)

	// A narrower grid bins more columns into a cell.
	narrow, err := c.AddressMap(ctx, h, d, MapOptions{Cells: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(narrow.Lines[0].Cells) != 16 || narrow.ColumnsPerCell != 128 {
		t.Errorf("20 cells allowed: %d cells of %d columns", len(narrow.Lines[0].Cells), narrow.ColumnsPerCell)
	}

	// Zooming into rows the reader names draws them as they are.
	zoom, err := c.AddressMap(ctx, h, d, MapOptions{RowFrom: model.Some(0x1a2f), RowTo: model.Some(0x1a30)})
	if err != nil {
		t.Fatal(err)
	}
	if len(zoom.Lines) != 2 || zoom.Lines[0].Row != 0x1a2f || zoom.Lines[1].Errors == 0 {
		t.Errorf("zoomed lines = %+v", zoom.Lines)
	}
	empty, err := c.AddressMap(ctx, h, d, MapOptions{RowFrom: model.Some(0), RowTo: model.Some(7)})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Lines) != 8 || empty.Lines[0].Errors != 0 {
		t.Errorf("a quiet range has %d lines", len(empty.Lines))
	}
}

func TestAddressMapBankSelection(t *testing.T) {
	c, h := open(t, "demo", "", i18n.EN)
	d := find(t, c, h, "CPU0_C1")
	for _, opts := range []MapOptions{
		{Bank: model.Some(3)},
		{BankGroup: model.Some(0), Bank: model.Some(3)},
		{Rank: model.Some(0), Device: model.Some(7)},
	} {
		m, err := c.AddressMap(ctx, h, d, opts)
		if err != nil || m.Bank == nil || m.Bank.Label != "rank0 dev7 bg0 bank3" {
			t.Errorf("selection %+v: %v", opts, err)
		}
	}
	_, err := c.AddressMap(ctx, h, d, MapOptions{Bank: model.Some(2)})
	if !errors.Is(err, ErrNoSuchBank) || !strings.Contains(err.Error(), "rank0 dev7 bg0 bank3") {
		t.Errorf("a bank without errors: %v", err)
	}
}

func TestAddressMapShapes(t *testing.T) {
	// A bank fault is a cloud over the whole bank: rows are binned.
	c, h := open(t, "bank", "", i18n.EN)
	m, err := c.AddressMap(ctx, h, h.DIMMs[0], MapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if m.Mode != MapBins || m.RowsPerLine != 4096 || len(m.Lines) != 16 || m.Faults[0].Mode != model.ModeBank {
		t.Errorf("bank fault: mode %s, %d rows per line, %d lines", m.Mode, m.RowsPerLine, len(m.Lines))
	}
	filled := 0
	for _, ln := range m.Lines {
		for _, n := range ln.Cells {
			if n > 0 {
				filled++
			}
		}
		if ln.Note != nil {
			t.Errorf("a binned line carries a note: %+v", ln.Note)
		}
	}
	if filled < 300 {
		t.Errorf("a bank fault fills %d cells; it should cover the grid", filled)
	}

	// A column fault is a vertical line, and the column is marked.
	c, h = open(t, "column", "", i18n.EN)
	m, err = c.AddressMap(ctx, h, h.DIMMs[0], MapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Marks) != 1 || m.Faults[0].Mode != model.ModeColumn {
		t.Fatalf("column fault: marks %+v", m.Marks)
	}
	for _, ln := range m.Lines {
		for i, n := range ln.Cells {
			if n > 0 && i != m.Marks[0].Cell {
				t.Errorf("row %#x has errors in cell %d; the column fault is in cell %d", ln.Row, i, m.Marks[0].Cell)
			}
		}
	}

	// Two groups of rows far apart: the rows between them are one line.
	c, h = open(t, "repaired", "", i18n.EN)
	d := find(t, c, h, "CPU0_C1")
	m, err = c.AddressMap(ctx, h, d, MapOptions{MaxLines: 40})
	if err != nil {
		t.Fatal(err)
	}
	if m.Mode != MapRows {
		t.Fatalf("mode = %s", m.Mode)
	}

	// A platform without rows and columns: nothing to draw, and the map
	// says how many errors that leaves out.
	c, h = open(t, "demo", synth.FlavorAMD, i18n.EN)
	m, err = c.AddressMap(ctx, h, find(t, c, h, "CPU0_C1"), MapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if m.Bank != nil || m.Unlocated != 1232 || m.Located != 0 || len(m.Lines) != 0 {
		t.Errorf("address-only platform: %+v", m)
	}
}

func TestAddressMapGap(t *testing.T) {
	// Noise puts single errors into rows far from each other.
	c, h := open(t, "system-channel", "", i18n.EN)
	d := find(t, c, h, "CPU1_A1")
	m, err := c.AddressMap(ctx, h, d, MapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if m.Bank == nil || m.Mode != MapRows || len(m.Lines) != 2*mapMargin+1 {
		t.Fatalf("one error: mode %s, %d lines", m.Mode, len(m.Lines))
	}
	if ln := m.Lines[mapMargin]; ln.Errors != 1 || ln.Note == nil {
		t.Errorf("line of the error = %+v", ln)
	}
}

func TestDataLines(t *testing.T) {
	for _, tt := range []struct {
		vec   string
		width int
		want  string
	}{
		{"dq:0xc", 4, "2 3"}, {"dq:0x1", 0, "0"}, {"bit:30", 4, "2"}, {"bit:30", 0, ""}, {"bit:x", 4, ""},
		{"nibble:0x3", 4, ""}, {"", 4, ""}, {"dq:zz", 4, ""},
	} {
		var got []string
		for _, q := range dataLines(tt.vec, tt.width) {
			got = append(got, string(rune('0'+q)))
		}
		if strings.Join(got, " ") != tt.want {
			t.Errorf("dataLines(%q, %d) = %v, want %q", tt.vec, tt.width, got, tt.want)
		}
	}
}

func TestTimeline(t *testing.T) {
	c, h := open(t, "demo", "", i18n.EN)
	d := find(t, c, h, "CPU0_C1")
	tl, err := c.Timeline(ctx, h, d, TimelineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if tl.From.Format("2006-01-02") != "2026-07-31" || tl.To.Format("2006-01-02") != "2026-11-12" || tl.Days != 105 {
		t.Errorf("period %v to %v, %d days", tl.From, tl.To, tl.Days)
	}
	if len(tl.Series) != 1 || len(tl.Series[0].CE) != 105 || tl.Series[0].Total != 1231 || tl.CE != 1231 || tl.UE != 1 {
		t.Errorf("series = %+v", tl.Series)
	}
	groups := map[string]MarkerGroup{}
	for _, g := range tl.Markers {
		groups[g.Kind] = g
	}
	if g := groups[MarkBoot]; g.Count != 3 || len(g.Days) != 3 || g.Letter != "B" || g.Text.Code != "tl.boot.many" {
		t.Errorf("boots = %+v", g)
	}
	if g := groups[MarkUE]; g.Count != 1 || g.Text.Code != "tl.ue.one_how" || !strings.Contains(g.Text.Text, "2026-11-12") {
		t.Errorf("uncorrected errors = %+v", g)
	}
	if g := groups[string(model.ActionHPPR)]; g.Count != 1 || g.Letter != "P" || !strings.Contains(g.Text.Text, "hard PPR") {
		t.Errorf("repairs = %+v", g)
	}
	if g := groups[string(model.ActionPageOffline)]; g.Count != 7 || g.Letter != "O" {
		t.Errorf("page offlines = %+v", g)
	}
	// The errors since the latest boot are what a counter would show.
	if tl.SinceBoot == nil || tl.SinceBoot.At.Format("2006-01-02") != "2026-10-28" || tl.SinceBoot.CE >= tl.CE || tl.SinceBoot.UE != 1 {
		t.Errorf("since boot = %+v", tl.SinceBoot)
	}
	golden(t, "demo-timeline.json", tl)

	// The last 30 days only.
	short, err := c.Timeline(ctx, h, d, TimelineOptions{Days: 30})
	if err != nil {
		t.Fatal(err)
	}
	if short.Days != 30 || short.From.Format("2006-01-02") != "2026-10-14" || len(short.Series[0].CE) != 30 || short.CE >= tl.CE {
		t.Errorf("30 days: %d days from %v, CE %d", short.Days, short.From, short.CE)
	}

	// The host: one row per DIMM with errors, and the markers say which
	// slot they are about.
	host, err := c.Timeline(ctx, h, nil, TimelineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(host.Series) != 2 || host.Series[0].Label != "CPU0_C1" || host.Series[1].Label != "CPU1_D1" || host.CE != 1284 || host.Slot != "" {
		t.Errorf("host series = %+v", host.Series)
	}
	for _, g := range host.Markers {
		if g.Kind == MarkUE && !strings.HasPrefix(g.Text.Text, "CPU0_C1: ") {
			t.Errorf("host marker does not name the slot: %q", g.Text.Text)
		}
	}
}

func TestTimelineCountsAnOfflineOnce(t *testing.T) {
	// On a host tree the page the kernel took offline after the uncorrected
	// error is in the kernel log and in the rasdaemon database: two action
	// records of one event. The timeline draws events.
	c, h := open(t, "demo", synth.FlavorIntel, i18n.EN)
	d := find(t, c, h, "CPU0_C1")
	tl, err := c.Timeline(ctx, h, d, TimelineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	actions, err := c.Store().Actions(ctx, store.ActionFilter{DIMMUID: d.UID, Kinds: []model.ActionKind{model.ActionPageOffline}})
	if err != nil {
		t.Fatal(err)
	}
	// The same history stored directly has every offline once.
	direct, hd := open(t, "demo", "", i18n.EN)
	want, err := direct.Timeline(ctx, hd, find(t, direct, hd, "CPU0_C1"), TimelineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	count := func(tl *Timeline) int {
		for _, g := range tl.Markers {
			if g.Kind == string(model.ActionPageOffline) {
				return g.Count
			}
		}
		return 0
	}
	if got := count(tl); got != count(want) || got != 7 || len(actions) != 8 {
		t.Errorf("timeline counts %d offlines from %d action records; the history has %d", got, len(actions), count(want))
	}
}

func TestTimelineShowsSlotChange(t *testing.T) {
	c, h := open(t, "moved-dimm", "", i18n.EN)
	tl, err := c.Timeline(ctx, h, find(t, c, h, "CPU0_D1"), TimelineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var move *MarkerGroup
	for i := range tl.Markers {
		if tl.Markers[i].Kind == MarkMove {
			move = &tl.Markers[i]
		}
	}
	if move == nil || move.Letter != "M" || !strings.Contains(move.Text.Text, "from CPU0_C1 to CPU0_D1") {
		t.Fatalf("markers = %+v", tl.Markers)
	}
	// The errors follow the DIMM: its row covers both slots.
	if tl.Series[0].Total != 240 {
		t.Errorf("the DIMM has %d errors on its timeline, want 240", tl.Series[0].Total)
	}
}

func TestTimelineSaysWhatFirmwareDid(t *testing.T) {
	c, h := open(t, "fw-disabled", "", i18n.EN)
	tl, err := c.Timeline(ctx, h, h.DIMMs[0], TimelineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range tl.Markers {
		if g.Kind == string(model.ActionDisable) {
			if g.Letter != "D" || g.Text.Text != "2026-10-01 taken out of use by firmware" {
				t.Errorf("marker = %+v", g)
			}
			return
		}
	}
	t.Errorf("no marker for the module firmware took out of use: %+v", tl.Markers)
}

func TestTimelineFoldsManyDIMMs(t *testing.T) {
	c, h := open(t, "system-channel", "", i18n.EN)
	tl, err := c.Timeline(ctx, h, nil, TimelineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tl.Series) != MaxSeries+1 {
		t.Fatalf("%d rows, want %d and one for the rest", len(tl.Series), MaxSeries)
	}
	var sum int64
	for _, s := range tl.Series {
		sum += s.Total
	}
	if sum != tl.CE || !strings.Contains(tl.Series[MaxSeries].Label, "more") {
		t.Errorf("rows sum to %d, period has %d; last row %q", sum, tl.CE, tl.Series[MaxSeries].Label)
	}
}

func TestShortSerial(t *testing.T) {
	for in, want := range map[string]string{"": "", "3B9C56": "3B9C56", "12345678": "12345678", "H0KE00080A1C3": "..80A1C3"} {
		if got := ShortSerial(in); got != want {
			t.Errorf("ShortSerial(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestMessages checks the catalog: both languages present, the same
// arguments on both sides.
func TestMessages(t *testing.T) {
	for _, code := range Messages.Codes() {
		m := Messages[code]
		if m.EN == "" || m.KO == "" {
			t.Errorf("%s: a language is missing", code)
			continue
		}
		en, ko := i18n.Placeholders(m.EN), i18n.Placeholders(m.KO)
		set := map[string]bool{}
		for _, p := range en {
			set[p] = true
		}
		for _, p := range ko {
			if !set[p] {
				t.Errorf("%s: Korean uses {%s}, English does not", code, p)
			}
			delete(set, p)
		}
		for p := range set {
			t.Errorf("%s: English uses {%s}, Korean does not", code, p)
		}
	}
}

// TestOverviewSaysWhatRootWouldAdd scans a live host as a user who may not
// read the SMBIOS tables: the overview names the source, why it could not
// be read, and what a scan as root would add.
func TestOverviewSaysWhatRootWouldAdd(t *testing.T) {
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
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Live, without a journal and without a ring buffer: the log file is read.
	if _, err := scan.Run(ctx, s, scan.Options{Root: root, Live: true, Now: ds.Scenario.End, Journalctl: "-", TempDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	c, err := Open(ctx, s, i18n.EN, engine.Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Host("")
	if err != nil {
		t.Fatal(err)
	}
	ov, err := c.Overview(ctx, h, false)
	if err != nil {
		t.Fatal(err)
	}
	if ov.Host.Scan == nil || ov.Host.Scan.Kind != model.ScanLive || ov.Host.Scan.Privileged {
		t.Fatalf("scan = %+v", ov.Host.Scan)
	}
	if len(ov.Missing) != 1 || !strings.Contains(ov.Missing[0].Text, "smbios: not readable (permission denied; the SMBIOS tables are readable as root") {
		t.Errorf("missing = %+v", ov.Missing)
	}
	if ov.Root == nil || ov.Root.Text != "This scan ran without root privileges. As root it also reads the SMBIOS tables (slot names and DIMM serial numbers)." {
		t.Errorf("root = %+v", ov.Root)
	}
	// The errors are found and graded all the same, by slot instead of by
	// serial number, and the finding says what that costs.
	if h.Grade != model.VerdictReplaceNow || len(ov.Ranking) != 2 || ov.Ranking[0].DIMM.Serial != "" {
		t.Errorf("grade %s, ranking %+v", h.Grade, ov.Ranking)
	}
	card, err := c.Card(ctx, h, h.DIMMs[0])
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range card.Missing {
		found = found || strings.Contains(m.Text, "smbios: not readable")
	}
	if !found {
		t.Errorf("the finding does not name the source it lacked: %+v", card.Missing)
	}

	// The same in Korean.
	k, err := Open(ctx, s, i18n.KO, engine.Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	hk, _ := k.Host("")
	kov, err := k.Overview(ctx, hk, false)
	if err != nil || kov.Root == nil || !strings.Contains(kov.Root.Text, "root로 실행하면 추가로 읽는 것: SMBIOS 테이블(슬롯 이름과 DIMM 시리얼).") {
		t.Errorf("Korean root line = %+v, %v", kov.Root, err)
	}
}

// TestRecordReferences checks that a point of a figure leads back to the
// ledger records behind it: the cells of an address map, the events of a
// timeline and the statements of a finding.
func TestRecordReferences(t *testing.T) {
	c, h := open(t, "demo", "", i18n.EN)
	d := find(t, c, h, "CPU0_C1")
	s := c.Store()

	// Address map, one row per line: every cell with errors has a
	// reference, and the references add up to the errors of the grid.
	m, err := c.AddressMap(ctx, h, d, MapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	checkRefs := func(m *AddressMap) {
		t.Helper()
		type key struct{ line, cell int }
		seen := map[key]bool{}
		var errs int64
		records := 0
		for i, r := range m.Refs {
			if i > 0 && (m.Refs[i-1].Line > r.Line || (m.Refs[i-1].Line == r.Line && m.Refs[i-1].Cell >= r.Cell)) {
				t.Errorf("references out of order at %d: %+v", i, r)
			}
			ln := m.Lines[r.Line]
			if ln.Gap || ln.Cells[r.Cell] != r.Errors || r.Records == 0 || r.First == 0 || r.Last == 0 || len(r.UE) > MaxCellUE {
				t.Errorf("reference %+v does not match line %+v", r, ln)
			}
			if r.From.After(r.To) || (r.Records == 1 && r.First != r.Last) {
				t.Errorf("reference %+v: first and last disagree", r)
			}
			seen[key{r.Line, r.Cell}] = true
			errs += r.Errors
			records += r.Records
			// The records are in the cell they are said to be in.
			for _, id := range append([]int64{r.First, r.Last}, r.UE...) {
				e, err := s.Event(ctx, id)
				if err != nil || e == nil {
					t.Fatalf("record %d: %v", id, err)
				}
				if e.Row.V < ln.Row || e.Row.V >= ln.Row+ln.Rows || e.Column.V/m.ColumnsPerCell != r.Cell {
					t.Errorf("record %d (row %#x col %#x) is not in line %#x cell %d", id, e.Row.V, e.Column.V, ln.Row, r.Cell)
				}
			}
			for _, id := range r.UE {
				if e, _ := s.Event(ctx, id); e == nil || !e.ErrorSeverity.Uncorrected() {
					t.Errorf("record %d is listed as an uncorrected error", id)
				}
			}
			if (len(r.UE) > 0) != (r.UEs > 0) {
				t.Errorf("reference %+v: uncorrected errors and their records disagree", r)
			}
		}
		for li, ln := range m.Lines {
			for ci, n := range ln.Cells {
				if (n > 0) != seen[key{li, ci}] {
					t.Errorf("line %d cell %d holds %d errors, reference %v", li, ci, n, seen[key{li, ci}])
				}
			}
		}
		if errs != m.Located-otherBanks(m) || records == 0 {
			t.Errorf("references cover %d errors in %d records, the bank holds %d", errs, records, m.Located-otherBanks(m))
		}
	}
	checkRefs(m)
	var ue *CellRef
	for i := range m.Refs {
		if m.Refs[i].UEs > 0 {
			ue = &m.Refs[i]
		}
	}
	if ue == nil || ue.Line != 3 || ue.Cell != 0x238/32 || ue.UEs != 1 || len(ue.UE) != 1 || ue.From.IsZero() || !ue.To.After(ue.From) {
		t.Errorf("the cell of the uncorrected error = %+v", ue)
	}

	// Lines that stand for many rows.
	cb, hb := open(t, "bank", "", i18n.EN)
	mb, err := cb.AddressMap(ctx, hb, hb.DIMMs[0], MapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if mb.Mode != MapBins || len(mb.Refs) < 300 {
		t.Fatalf("bank fault: mode %s, %d references", mb.Mode, len(mb.Refs))
	}
	s = cb.Store()
	checkRefs(mb)
	s = c.Store()

	// Timeline: every event is there with the record it comes from.
	tl, err := c.Timeline(ctx, h, d, TimelineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range tl.Markers {
		if len(g.Marks) == 0 || len(g.Marks) > MaxMarks {
			t.Errorf("%s: %d marks", g.Kind, len(g.Marks))
		}
		n := 0
		days := map[int]bool{}
		for i, mk := range g.Marks {
			if i > 0 && mk.At.Before(g.Marks[i-1].At) {
				t.Errorf("%s: marks out of order", g.Kind)
			}
			if mk.Text.Text == "" || mk.Count < 1 || mk.Day < 0 || mk.Day >= tl.Days {
				t.Errorf("%s: mark %+v", g.Kind, mk)
			}
			n += mk.Count
			days[mk.Day] = true
			switch g.Kind {
			case MarkBoot, MarkMove:
				if mk.Record != nil {
					t.Errorf("%s: a boot or a move has no original record: %+v", g.Kind, mk.Record)
				}
			case MarkUE:
				e, _ := s.Event(ctx, mk.Record.ID)
				if mk.Record.Kind != model.RecordEvent || e == nil || !e.ErrorSeverity.Uncorrected() || e.RawRef != mk.Record.RawRef {
					t.Errorf("UE mark %+v", mk.Record)
				}
			default:
				if mk.Record == nil || mk.Record.Kind != model.RecordAction || mk.Record.ID == 0 || mk.Record.RawRef == "" {
					t.Errorf("%s: mark without its action: %+v", g.Kind, mk.Record)
				}
			}
		}
		if n != g.Count || len(days) != len(g.Days) {
			t.Errorf("%s: marks add up to %d on %d days, the group says %d on %d", g.Kind, n, len(days), g.Count, len(g.Days))
		}
	}

	// A finding: the statements cite records, the first and the last of
	// them as the card prints them.
	card, err := c.Card(ctx, h, d)
	if err != nil {
		t.Fatal(err)
	}
	cited := 0
	for _, e := range card.Evidence {
		if e.Records == 0 {
			if len(e.Cited) != 0 {
				t.Errorf("%s cites records and counts none", e.Code)
			}
			continue
		}
		if len(e.Cited) == 0 || len(e.Cited) > e.Records || e.Cited[0].RawRef != e.First || e.Cited[len(e.Cited)-1].RawRef != e.Last {
			t.Errorf("%s: %d records, cited %+v, first %q, last %q", e.Code, e.Records, e.Cited, e.First, e.Last)
		}
		cited += len(e.Cited)
	}
	if cited < 4 {
		t.Errorf("the finding cites %d records", cited)
	}
}

// otherBanks returns the located errors of a DIMM outside the bank that
// is drawn.
func otherBanks(m *AddressMap) int64 {
	var n int64
	for _, b := range m.Banks {
		if b.Label != m.Bank.Label {
			n += b.Errors
		}
	}
	return n
}

// TestTimelineKeepsTheLatestMarks checks the bound on the events a marker
// group lists one by one.
func TestTimelineKeepsTheLatestMarks(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(ctx, filepath.Join(dir, "many.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	const pages = MaxMarks + 40
	err = s.Update(ctx, func(w *store.Writer) error {
		if err := w.UpsertHost(&model.Host{Name: "h"}); err != nil {
			return err
		}
		for i := range pages {
			at := start.Add(time.Duration(i) * time.Hour)
			a := &model.Action{Host: "h", At: at, TimeQuality: model.TimeExact, Kind: model.ActionPageOffline, Result: model.ResultOK,
				OsPhysicalAddress: model.Some(uint64(0x1000 * (i + 1))), Slot: "A1", Source: "test", RawRef: "t#" + at.Format(time.RFC3339),
				Fingerprint: model.Fingerprint("page", at.String())}
			if _, err := w.InsertAction(a); err != nil {
				return err
			}
			e := &model.Event{Host: "h", Kind: model.KindError, ErrorDT: at, ErrorSeverity: model.SevCE, Slot: "A1", Count: 1,
				Source: "test", RawRef: "e#" + at.Format(time.RFC3339), Grade: model.GradeC, TimeQuality: model.TimeExact,
				Fingerprint: model.Fingerprint("event", at.String())}
			if _, err := w.InsertEvent(e); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	c, err := Open(ctx, s, i18n.EN, engine.Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Host("")
	if err != nil {
		t.Fatal(err)
	}
	tl, err := c.Timeline(ctx, h, nil, TimelineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var g *MarkerGroup
	for i := range tl.Markers {
		if tl.Markers[i].Kind == string(model.ActionPageOffline) {
			g = &tl.Markers[i]
		}
	}
	if g == nil || g.Count != pages || len(g.Marks) != MaxMarks {
		t.Fatalf("page offlines: %+v", g)
	}
	last := start.Add((pages - 1) * time.Hour)
	if !g.Marks[MaxMarks-1].At.Equal(last) || !g.Marks[0].At.Equal(last.Add(-(MaxMarks-1)*time.Hour)) {
		t.Errorf("the marks run from %v to %v, want the latest %d up to %v", g.Marks[0].At, g.Marks[MaxMarks-1].At, MaxMarks, last)
	}
}

// TestCaseInAnotherLanguage: the same case gives the same card in another
// language without the engine running again, and the card is the one a
// case opened in that language gives.
func TestCaseInAnotherLanguage(t *testing.T) {
	en, h := open(t, "demo", "", i18n.EN)
	ko := en.In(i18n.KO)
	if ko == en || ko.Result != en.Result || ko.Store() != en.Store() || ko.Lang != i18n.KO || en.Lang != i18n.EN {
		t.Fatalf("In(ko): result shared %v, lang %s, the original is now %s", ko.Result == en.Result, ko.Lang, en.Lang)
	}
	if en.In(i18n.EN) != en || en.In("") != en {
		t.Error("In with the language of the case, or with none, returns another case")
	}
	direct, hk := open(t, "demo", "", i18n.KO)
	want, err := direct.Card(ctx, hk, find(t, direct, hk, "CPU0_C1"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := ko.Card(ctx, h, find(t, ko, h, "CPU0_C1"))
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(want)
	b, _ := json.Marshal(got)
	if string(a) != string(b) || got.GradeText != "즉시 교체" {
		t.Errorf("the card of In(ko) differs from the card of a case opened in Korean:\n%s\n%s", b, a)
	}
}

// TestHostsWorstFirst: a ledger that holds a fleet lists the hosts to look
// at first: hold replacement, replace now, consider replacement, observe.
func TestHostsWorstFirst(t *testing.T) {
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "fleet.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Stored in an order that is neither by name nor by grade. Each host
	// gets its own seed: with one seed the hosts would hold DIMMs with the
	// same serial numbers, which the ledger takes for the same DIMMs.
	for i, name := range []string{"single-bit", "offline-growing", "demo", "system-channel", "row"} {
		ds := testledger.Generate(t, name, uint64(i+1), nil)
		if _, err := ingest.Run(ctx, s, []adapter.Adapter{ds.Adapter()}, ingest.Options{Kind: model.ScanSynthetic, Now: ds.Scenario.End}); err != nil {
			t.Fatal(err)
		}
	}
	c, err := Open(ctx, s, i18n.EN, engine.Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	rows := c.Hosts()
	var got []string
	for i, r := range rows {
		got = append(got, r.Name+" "+string(r.Grade))
		if i > 0 {
			prev := rows[i-1]
			if prev.Grade.ExitCode() < r.Grade.ExitCode() ||
				(prev.Grade == r.Grade && (prev.UE < r.UE || (prev.UE == r.UE && prev.CE < r.CE))) {
				t.Errorf("%s (%s, UE %d, CE %d) is listed before %s (%s, UE %d, CE %d)", prev.Name, prev.Grade, prev.UE, prev.CE, r.Name, r.Grade, r.UE, r.CE)
			}
		}
	}
	if len(rows) != 5 || rows[0].Grade != model.VerdictHoldReplacement || rows[0].Name != "synth-system-channel" || rows[1].Name != "r12-n034" ||
		rows[len(rows)-1].Grade != model.VerdictObserve {
		t.Errorf("hosts: %q", got)
	}
}
