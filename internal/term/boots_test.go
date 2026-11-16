package term

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/synth"
	"github.com/agenticode/my/internal/testledger"
	"github.com/agenticode/my/internal/view"
)

func (s *screen) boots(ref string, show int) string {
	s.t.Helper()
	var d *engine.DIMMResult
	if ref != "" {
		d = s.dimm(ref)
	}
	b, err := s.c.BootsView(ctx, s.h, d)
	if err != nil {
		s.t.Fatal(err)
	}
	hints := []Hint{{"my boots CPU0_C1", "the places of this DIMM boot by boot"}}
	var out bytes.Buffer
	if err := Boots(&out, b, s.st, show, hints); err != nil {
		s.t.Fatal(err)
	}
	return out.String()
}

// The crash-loop scenario: a cell of CPU0_C1 gives a fatal error in four
// boots in a row, each read back by the next boot.
func TestBoots(t *testing.T) {
	for _, v := range []variant{en, ko, ascii, color, w60} {
		check(t, "loop-boots", v, open(t, "crash-loop", "", v).boots("", 0))
		check(t, "loop-boots-dimm", v, open(t, "crash-loop", "", v).boots("CPU0_C1", 0))
	}
	got := open(t, "crash-loop", "", en).boots("", 0)
	for _, want := range []string{
		"stops: CPU0_C1 had an uncorrected error in 4 of the boots that stopped (R-LOOP-01)",
		"ended    -       -       -       STOP    STOP    STOP    STOP    -",
		"CPU0_C1  ·       ·       ▒       X       X       X       X       ·",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the boots lack %q:\n%s", want, got)
		}
	}
	// The grid shows how many boots stopped: the line that counts them
	// comes with --detail.
	if strings.Contains(got, "4 of the last 8 boots") {
		t.Errorf("the short boots count the stops:\n%s", got)
	}
	s := open(t, "crash-loop", "", en)
	s.st.Detail = true
	if got := s.boots("", 0); !strings.Contains(got, "stops: 4 of the last 8 boots ended in a fatal hardware error, the latest on 2026-11-12 10:10 UTC") {
		t.Errorf("--detail lacks the count of the stops:\n%s", got)
	}
	// More boots than fit: the latest stay, a line says how many are left
	// out; --boots N draws N of them.
	got = open(t, "crash-loop", "", w60).boots("", 0)
	if !strings.Contains(got, "2 earlier boots are left out") || !strings.Contains(got, " boot     3       4") {
		t.Errorf("at 60 columns:\n%s", got)
	}
	got = open(t, "crash-loop", "", en).boots("", 3)
	check(t, "loop-boots-three", en, got)
	if !strings.Contains(got, "5 earlier boots are left out") || !strings.Contains(got, " 6       7       8") {
		t.Errorf("three boots:\n%s", got)
	}
	// The places of a DIMM: the cell under its row, the rule below.
	got = open(t, "crash-loop", "", en).boots("CPU0_C1", 0)
	for _, want := range []string{"rank0 dev7 bg0 bank3 row 0x1a2f\n   col 0x238  ·  ·  ▒  X  X  X  X  ·   5 of 8  CE 60, UE 4", "R-LOOP-01"} {
		if !strings.Contains(got, want) {
			t.Errorf("the places lack %q:\n%s", want, got)
		}
	}
	// A host without stops: the grid and no stops line.
	got = open(t, "demo", "", en).boots("", 0)
	check(t, "demo-boots", en, got)
	if strings.Contains(got, "stops:") || !strings.Contains(got, "CPU0_C1  ·       ░       ▓       U") {
		t.Errorf("demo:\n%s", got)
	}
}

// TestBootsInOtherScreens: the stops lines and the stops row of the
// overview, the boots block of a DIMM, the boots column of the topology,
// the line under the address map and the map by boots, and the boots
// strip of the timeline.
func TestBootsInOtherScreens(t *testing.T) {
	for _, v := range all {
		check(t, "loop-overview", v, open(t, "crash-loop", "", v).overview())
		check(t, "loop-dimm", v, open(t, "crash-loop", "", v).examination("CPU0_C1"))
	}
	ov := open(t, "crash-loop", "", en).overview()
	for _, want := range []string{" stops: 4 of the last 8 boots", " CPU0    ░░   ░░   ██", " stops   ·    ·    4    ·"} {
		if !strings.Contains(ov, want) {
			t.Errorf("the overview lacks %q:\n%s", want, ov)
		}
	}
	// In short the boots block carries the count of boots; the topology
	// of one place is not drawn.
	x := open(t, "crash-loop", "", en).examination("CPU0_C1")
	if !strings.Contains(x, " boots of this DIMM\n") || !strings.Contains(x, "  5 of 8  CE 60, UE 4") || strings.Contains(x, "the latest boots") ||
		strings.Contains(x, "where the errors sit") {
		t.Errorf("the examination:\n%s", x)
	}
	x = open(t, "crash-loop", "", en).detail().examination("CPU0_C1")
	if !strings.Contains(x, " boots of this DIMM\n") || !strings.Contains(x, "|  5 of 8") || strings.Contains(x, "the latest boots") {
		t.Errorf("the examination with detail:\n%s", x)
	}
	// The demo has errors in three of its four boots and no stop.
	x = open(t, "demo", "", en).detail().examination("CPU0_C1")
	if !strings.Contains(x, "boots\n rank    |rank0") || !strings.Contains(x, "|  3 of 4") || !strings.Contains(x, "errors in 3 of the last 4 boots, uncorrected in 1") {
		t.Errorf("the demo examination:\n%s", x)
	}
	for _, v := range []variant{en, ko, ascii} {
		check(t, "demo-map-boots", v, open(t, "demo", "", v).addressMap("CPU0_C1", view.MapOptions{Boots: true}))
	}
	m := open(t, "demo", "", en).addressMap("CPU0_C1", view.MapOptions{Boots: true})
	if !strings.Contains(m, "value = boots with an error there, of the last 4") || !strings.Contains(m, "x·····2·1··2") {
		t.Errorf("the map by boots:\n%s", m)
	}
	check(t, "loop-timeline", en, open(t, "crash-loop", "", en).timeline(""))
	tl := open(t, "crash-loop", "", en).timeline("CPU1_D1")
	if !strings.Contains(tl, "the latest boots\n") || strings.Contains(tl, "CPU0_C1  ") {
		t.Errorf("the timeline of CPU1_D1 draws other DIMMs in its boots:\n%s", tl)
	}
	// No stop, no strip.
	if tl := open(t, "demo", "", en).timeline(""); strings.Contains(tl, "the latest boots\n") {
		t.Errorf("a strip without a stop:\n%s", tl)
	}
}

// TestOneBoot: a ledger of a host with one boot shows no boots block, no
// boots column and no line under the map.
func TestOneBoot(t *testing.T) {
	ds := testledger.Generate(t, "single-bit", 1, func(sc *synth.Scenario) { sc.Reboots = nil })
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := ingest.Run(ctx, s, []adapter.Adapter{ds.Adapter()}, ingest.Options{Kind: model.ScanSynthetic, Now: ds.Scenario.End}); err != nil {
		t.Fatal(err)
	}
	c, err := view.Open(ctx, s, i18n.EN, engine.Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Host("")
	if err != nil {
		t.Fatal(err)
	}
	sc := &screen{t: t, c: c, h: h, st: en.st}
	x := sc.examination("CPU1_D1")
	if strings.Contains(x, "boots of this DIMM") || strings.Contains(x, " of 1") || strings.Contains(x, "the latest boots") {
		t.Errorf("one boot:\n%s", x)
	}
	if b := sc.boots("", 0); !strings.Contains(b, "1 boot ") && !strings.Contains(b, "1 boot\n") {
		t.Errorf("the boots of one boot:\n%s", b)
	}
}

// TestBootsLegendWithoutCells: a grid of boots without a DIMM row has no
// cell to explain; the legend starts with how the boots ended, without a
// separator in front.
func TestBootsLegendWithoutCells(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.EN, i18n.KO} {
		st := Style{Lang: lang}
		p := &page{st: st}
		st.bootsLegend(p, &gridKeys{ends: map[string]bool{"": true}}, true)
		if len(p.lines) != 1 || strings.HasPrefix(strings.TrimSpace(p.lines[0]), ";") {
			t.Errorf("%s: legend %q", lang, p.lines)
		}
		p = &page{st: st}
		st.bootsLegend(p, &gridKeys{}, true)
		if len(p.lines) != 0 {
			t.Errorf("%s: a legend of nothing: %q", lang, p.lines)
		}
	}
}
