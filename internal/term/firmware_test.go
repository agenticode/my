package term

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/fwevent"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/testledger"
	"github.com/agenticode/my/internal/view"
)

// fwLog is a log of a BMC made up for the tests: each record is a common
// event about one module at one time.
type fwLog []fwevent.Record

func (fwLog) Kind() string   { return "redfish" }
func (fwLog) Origin() string { return "made-up BMC log" }

func (l fwLog) Collect(ctx context.Context, sink adapter.Sink) adapter.Report {
	rep := adapter.Report{Kind: "redfish", Origin: "made-up BMC log", Status: model.SourceOK}
	for i := range l {
		r := l[i]
		r.Fingerprint = fmt.Sprintf("fw-test-%d", i)
		r.Source = "redfish/log"
		r.Method = "bmc"
		r.Firmware = model.FirmwareRecord{VendorID: fmt.Sprintf("EXM%04d", i+1), Registry: "ExampleEvent.1.0",
			Mapping: model.MappingTable, MapRef: "example@1:" + string(r.Event), Basis: "registry", Origin: model.OriginBMCLog}
		rep.Records++
		if err := fwevent.Emit(sink, &r); err != nil {
			rep.SinkErr = err
			return rep
		}
	}
	return rep
}

// fwScreen is the crash loop with the records of a BMC about its two
// DIMMs, one record in each of seven boots: the firmware line of the
// boots gets a letter for each, and the timeline the new letters.
func fwScreen(t *testing.T, v variant) *screen {
	t.Helper()
	ds := testledger.Generate(t, "crash-loop", 1, nil)
	at := func(month, day, hour, min int) time.Time {
		return time.Date(2026, time.Month(month), day, hour, min, 0, 0, time.UTC)
	}
	log := fwLog{
		{Event: fwevent.Sparing, At: at(7, 31, 12, 0), Labels: []string{"CPU1_D1"}},
		{Event: fwevent.TrainingFailure, At: at(9, 1, 3, 5), Labels: []string{"CPU0_C1"}},
		{Event: fwevent.PPRDone, At: at(10, 1, 3, 2), Labels: []string{"CPU0_C1"}},
		{Event: fwevent.PredictedFailure, At: at(11, 12, 8, 10), Labels: []string{"CPU0_C1"}},
		{Event: fwevent.Disabled, At: at(11, 12, 8, 45), Labels: []string{"CPU0_C1"}},
		{Event: fwevent.MirrorFailover, At: at(11, 12, 9, 24), Labels: []string{"CPU1_D1"}},
		// A prediction a later record takes back gives no letter.
		{Event: fwevent.PredictedFailure, At: at(11, 12, 9, 30), Labels: []string{"CPU1_D1"}},
		{Event: fwevent.Cleared, Clears: fwevent.PredictedFailure, At: at(11, 12, 9, 40), Labels: []string{"CPU1_D1"}},
	}
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := ingest.Run(ctx, s, []adapter.Adapter{ds.Adapter(), log}, ingest.Options{Kind: model.ScanSynthetic, Now: ds.Scenario.End}); err != nil {
		t.Fatal(err)
	}
	c, err := view.Open(ctx, s, v.st.lang(), engine.Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Host("")
	if err != nil {
		t.Fatal(err)
	}
	return &screen{t: t, c: c, h: h, st: v.st}
}

// TestFirmwareInBoots: the line "firmware" of the boots of a host and of
// a DIMM, and its legend, in each variant.
func TestFirmwareInBoots(t *testing.T) {
	for _, v := range []variant{en, ko, ascii, w60} {
		s := fwScreen(t, v)
		check(t, "fw-boots", v, s.boots("", 0))
		check(t, "fw-boots-dimm", v, s.boots("CPU0_C1", 0))
	}
	s := fwScreen(t, en)
	got := s.boots("", 0)
	folded := strings.Join(strings.Fields(got), " ")
	if !strings.Contains(folded, "firmware: D disabled, V mirror failover, N training failed, A failure predicted, S sparing, P repaired") {
		t.Errorf("the legend lacks the firmware letters:\n%s", got)
	}
	for _, want := range []string{
		" firmware  S       N       P       A       D       V       -       -",
		" CPU0_C1   ·       ·       ▒       X       X       X       X       ·",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the boots lack %q:\n%s", want, got)
		}
	}
	// The legend names only the letters on the screen.
	if strings.Contains(got, "E repair") {
		t.Errorf("the legend names a letter the grid does not use:\n%s", got)
	}
	got = s.boots("CPU0_C1", 0)
	if !strings.Contains(got, " firmware     -  N  P  A  D  -  -  -") {
		t.Errorf("the boots of CPU0_C1 lack its firmware line:\n%s", got)
	}

	// Without firmware records there is no such line.
	if got := open(t, "crash-loop", "", en).boots("", 0); strings.Contains(got, "firmware") {
		t.Errorf("a firmware line without firmware records:\n%s", got)
	}
}

// TestFirmwareOnTheTimeline: the letters A, N and V, and the text of a
// mark taken back.
func TestFirmwareOnTheTimeline(t *testing.T) {
	for _, v := range []variant{en, ko, ascii} {
		check(t, "fw-timeline", v, fwScreen(t, v).timeline("CPU1_D1"))
	}
	s := fwScreen(t, en)
	c0 := s.timeline("CPU0_C1")
	for _, want := range []string{"memory training failed", "failure predicted by firmware", "taken out of use by firmware"} {
		if !strings.Contains(c0, want) {
			t.Errorf("the timeline of CPU0_C1 lacks %q:\n%s", want, c0)
		}
	}
	tl, err := s.c.Timeline(ctx, s.h, s.dimm("CPU1_D1"), view.TimelineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	letters := map[string]string{}
	for _, g := range tl.Markers {
		letters[g.Letter] = g.Text.Text
	}
	if !strings.Contains(letters["V"], "mirror") || !strings.Contains(letters["A"], "taken back by a later record") || letters["S"] == "" {
		t.Errorf("markers of CPU1_D1: %v", letters)
	}
	// The strip of boots under the timeline of a DIMM gives the letters of
	// that DIMM: the prediction taken back gives none.
	var strip []string
	for _, bc := range tl.Boots.Boots {
		strip = append(strip, bc.Firmware)
	}
	if strings.Join(strip, ",") != "S,,,,,V,," {
		t.Errorf("the strip of CPU1_D1 has the firmware letters %q", strip)
	}
	if len(tl.Firmware) != 4 {
		t.Errorf("CPU1_D1 has %d firmware records: %+v", len(tl.Firmware), tl.Firmware)
	}
	for _, f := range tl.Firmware {
		if f.Event == string(fwevent.PredictedFailure) && !f.Withdrawn {
			t.Errorf("the prediction taken back is not withdrawn: %+v", f)
		}
	}
}

// TestFirmwareInTheInventory: the line "firmware:" under a DIMM with the
// marks of its firmware, newest first, without the one taken back.
func TestFirmwareInTheInventory(t *testing.T) {
	inv := func(s *screen) string {
		t.Helper()
		v, err := s.c.Inventory(ctx, s.h, view.InventoryOptions{})
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		if err := Inventory(&b, v, s.st); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	// The table of the inventory is wider than 60 columns at any width.
	for _, v := range []variant{en, ko, ascii} {
		check(t, "fw-inventory", v, inv(fwScreen(t, v)))
	}
	got := inv(fwScreen(t, en))
	for _, want := range []string{"firmware: disabled 11-12, PFA 11-12 +2", "firmware: mirror 11-12, sparing 07-31"} {
		if !strings.Contains(got, want) {
			t.Errorf("the inventory lacks %q:\n%s", want, got)
		}
	}
	if got := inv(open(t, "crash-loop", "", en)); strings.Contains(got, "firmware:") {
		t.Errorf("a firmware line without firmware records:\n%s", got)
	}
}
