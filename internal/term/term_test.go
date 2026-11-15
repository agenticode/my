package term

import (
	"bytes"
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/synth"
	"github.com/agenticode/my/internal/testledger"
	"github.com/agenticode/my/internal/testutil"
	"github.com/agenticode/my/internal/view"
)

func TestMain(m *testing.M) { testledger.Main(m) }

var ctx = context.Background()

var now = time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)

// variant is one way of drawing: language, glyphs, colour, width.
type variant struct {
	name string
	st   Style
}

var (
	en    = variant{"en", Style{Lang: i18n.EN}}
	ko    = variant{"ko", Style{Lang: i18n.KO}}
	ascii = variant{"ascii", Style{Lang: i18n.EN, ASCII: true}}
	color = variant{"color", Style{Lang: i18n.EN, Color: true}}
	w60   = variant{"w60", Style{Lang: i18n.EN, Width: 60}}
	w140  = variant{"w140", Style{Lang: i18n.EN, Width: 140}}
	// The three every figure is pinned in.
	all = []variant{en, ko, ascii}
)

var ansi = regexp.MustCompile("\x1b\\[[0-9;]*m")

// screen is the case of a scenario in the language of a variant.
type screen struct {
	t  *testing.T
	c  *view.Case
	h  *engine.HostResult
	st Style
}

func open(t *testing.T, scenario string, fl synth.Flavor, v variant) *screen {
	t.Helper()
	c, err := view.Open(ctx, testledger.Open(t, scenario, 1, fl), v.st.lang(), engine.Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Host("")
	if err != nil {
		t.Fatal(err)
	}
	return &screen{t: t, c: c, h: h, st: v.st}
}

func (s *screen) dimm(ref string) *engine.DIMMResult {
	s.t.Helper()
	d, err := s.c.Find(ctx, s.h, ref)
	if err != nil {
		s.t.Fatal(err)
	}
	return d
}

func (s *screen) overview() string {
	s.t.Helper()
	ov, err := s.c.Overview(ctx, s.h, false)
	if err != nil {
		s.t.Fatal(err)
	}
	hints := []Hint{{"my dimm CPU0_C1", "examination"}, {"my why CPU0_C1", "reasons"}, {"my bundle -o case.mycase", "case bundle"}, {"my help", "how to read the figures"}}
	var b bytes.Buffer
	if err := Overview(&b, ov, s.st, hints); err != nil {
		s.t.Fatal(err)
	}
	return b.String()
}

func (s *screen) examination(ref string) string {
	s.t.Helper()
	x, err := s.c.Examine(ctx, s.h, s.dimm(ref), s.st.MapCells())
	if err != nil {
		s.t.Fatal(err)
	}
	var b bytes.Buffer
	if err := Examination(&b, x, s.st); err != nil {
		s.t.Fatal(err)
	}
	return b.String()
}

func (s *screen) why(ref string) string {
	s.t.Helper()
	card, err := s.c.Card(ctx, s.h, s.dimm(ref))
	if err != nil {
		s.t.Fatal(err)
	}
	var b bytes.Buffer
	if err := Why(&b, []*view.Card{card}, s.st); err != nil {
		s.t.Fatal(err)
	}
	return b.String()
}

func (s *screen) oneline(ref string) string {
	s.t.Helper()
	card, err := s.c.Card(ctx, s.h, s.dimm(ref))
	if err != nil {
		s.t.Fatal(err)
	}
	return Oneline(card, s.st) + "\n"
}

func (s *screen) addressMap(ref string, opts view.MapOptions) string {
	s.t.Helper()
	opts.Cells = s.st.MapCells()
	m, err := s.c.AddressMap(ctx, s.h, s.dimm(ref), opts)
	if err != nil {
		s.t.Fatal(err)
	}
	var b bytes.Buffer
	if err := AddressMap(&b, m, s.st); err != nil {
		s.t.Fatal(err)
	}
	return b.String()
}

func (s *screen) timeline(ref string) string {
	s.t.Helper()
	var d *engine.DIMMResult
	if ref != "" {
		d = s.dimm(ref)
	}
	tl, err := s.c.Timeline(ctx, s.h, d, view.TimelineOptions{})
	if err != nil {
		s.t.Fatal(err)
	}
	var b bytes.Buffer
	if err := Timeline(&b, tl, s.st); err != nil {
		s.t.Fatal(err)
	}
	return b.String()
}

// check compares a screen with its golden file and tests what holds for
// every screen: no line is wider than the style allows, plain output holds
// no escape sequences, and ASCII output of English text is ASCII.
func check(t *testing.T, name string, v variant, got string) {
	t.Helper()
	inspect(t, name, v, got)
	testutil.Golden(t, name+"."+v.name+".txt", []byte(got))
}

// inspect tests what holds for every screen.
func inspect(t *testing.T, name string, v variant, got string) {
	t.Helper()
	if strings.TrimSpace(got) == "" {
		t.Errorf("%s: nothing was drawn", name)
	}
	plain := ansi.ReplaceAllString(got, "")
	if !v.st.Color && plain != got {
		t.Errorf("%s: escape sequences in output without colour", name)
	}
	if v.st.Color && plain == got {
		t.Errorf("%s: no colour in coloured output", name)
	}
	for i, line := range strings.Split(plain, "\n") {
		if w := Width(line); w > v.st.width() && !oneLong(line) {
			t.Errorf("%s line %d is %d columns wide, the style allows %d:\n%s", name, i+1, w, v.st.width(), line)
		}
		if line != strings.TrimRight(line, " ") {
			t.Errorf("%s line %d ends in spaces", name, i+1)
		}
	}
	if v.st.ASCII && v.st.lang() == i18n.EN {
		for _, r := range plain {
			if r > 0x7e {
				t.Errorf("%s: %q in ASCII output", name, r)
				break
			}
		}
	}
}

// oneLong reports whether a line is too wide only because it holds one
// word that cannot be broken, such as the path of an original record.
func oneLong(line string) bool {
	for _, w := range strings.Fields(line) {
		if Width(w) > 40 {
			return true
		}
	}
	return false
}

func TestOverview(t *testing.T) {
	for _, v := range []variant{en, ko, ascii, color, w60} {
		check(t, "demo-overview", v, open(t, "demo", "", v).overview())
	}
	// Two DIMMs per channel, and a hold finding: two bands, "??" cells.
	got := open(t, "system-channel", "", en).overview()
	check(t, "system-channel-overview", en, got)
	if !strings.Contains(got, "??") || !strings.Contains(got, "A2   B2") {
		t.Errorf("the slot map lacks the hold cells or the second band:\n%s", got)
	}
	// A host tree as an AMD platform records it: grade B, more sources.
	check(t, "demo-amd-overview", en, open(t, "demo", synth.FlavorAMD, en).overview())
}

func TestExamination(t *testing.T) {
	for _, v := range []variant{en, ko, ascii, color, w60, w140} {
		check(t, "demo-dimm", v, open(t, "demo", "", v).examination("CPU0_C1"))
	}
	// A bank fault: folded rows in the topology, binned rows in the map.
	check(t, "bank-dimm", en, open(t, "bank", "", en).examination("CPU1_A1"))
	// Addresses only: the lower levels are not reported and no map exists.
	got := open(t, "demo", synth.FlavorAMD, en).examination("CPU0_C1")
	check(t, "demo-amd-dimm", en, got)
	if !strings.Contains(got, "not reported by this platform") || !strings.Contains(got, "no address map to draw") {
		t.Errorf("the screen does not say what the platform leaves out:\n%s", got)
	}
	// A DIMM without errors: the card and nothing else.
	got = open(t, "demo", "", en).examination("CPU0_A1")
	check(t, "demo-dimm-quiet", en, got)
	if strings.Contains(got, "where the errors sit") || strings.Contains(got, "errors per day") {
		t.Errorf("figures drawn for a DIMM without errors:\n%s", got)
	}
}

func TestWhy(t *testing.T) {
	for _, v := range all {
		check(t, "demo-why", v, open(t, "demo", "", v).why("CPU0_C1"))
	}
	check(t, "system-channel-why", en, open(t, "system-channel", "", en).why("CPU0_B1"))
	// The one-line form is not wrapped: it is pasted into a ticket.
	for _, v := range all {
		testutil.Golden(t, "demo-oneline."+v.name+".txt", []byte(open(t, "demo", "", v).oneline("CPU0_C1")))
	}
	// The one-line form is one line and names host, slot, grade and rule.
	s := open(t, "demo", "", en)
	card, err := s.c.Card(ctx, s.h, s.dimm("CPU1_D1"))
	if err != nil {
		t.Fatal(err)
	}
	line := Oneline(card, s.st)
	for _, want := range []string{"r12-n034 CPU1_D1 S/N 2AEB41: observe (finding, confidence medium; R-SOFT-01).", "No action."} {
		if !strings.Contains(line, want) {
			t.Errorf("one line lacks %q: %s", want, line)
		}
	}
	if strings.Contains(line, "\n") {
		t.Errorf("more than one line: %q", line)
	}
}

func TestAddressMap(t *testing.T) {
	for _, v := range []variant{en, ko, ascii, color} {
		check(t, "demo-map", v, open(t, "demo", "", v).addressMap("CPU0_C1", view.MapOptions{}))
	}
	// A column fault: a vertical line with a mark under its column.
	got := open(t, "column", "", en).addressMap("CPU0_E1", view.MapOptions{})
	check(t, "column-map", en, got)
	if !strings.Contains(got, "^ col 0x110: column fault") {
		t.Errorf("the column is not marked:\n%s", got)
	}
	// Rows far apart: the quiet rows between them are one line.
	s := open(t, "system-channel", "", en)
	got = s.addressMap("CPU0_B1", view.MapOptions{})
	check(t, "system-channel-map", en, got)
	check(t, "demo-map", w60, open(t, "demo", "", w60).addressMap("CPU0_C1", view.MapOptions{}))
}

func TestTimeline(t *testing.T) {
	for _, v := range []variant{en, ko, ascii, w60, w140} {
		check(t, "demo-timeline", v, open(t, "demo", "", v).timeline("CPU0_C1"))
		check(t, "demo-timeline-host", v, open(t, "demo", "", v).timeline(""))
	}
	got := open(t, "moved-dimm", "", en).timeline("CPU0_D1")
	check(t, "moved-dimm-timeline", en, got)
	if !strings.Contains(got, "moved from CPU0_C1 to CPU0_D1") {
		t.Errorf("the slot change is not on the timeline:\n%s", got)
	}
	check(t, "system-channel-timeline-host", en, open(t, "system-channel", "", en).timeline(""))
}

func TestTimelineOfAQuietHost(t *testing.T) {
	// A host nothing is on record about: one day, no errors, no events.
	tl := &view.Timeline{Host: "h", From: now, To: now, Days: 1, Series: []view.Series{}, Markers: []view.MarkerGroup{},
		Notes: []view.Text{{Text: "0 corrected and 0 uncorrected errors from 2026-11-13 to 2026-11-13"}}}
	var b bytes.Buffer
	if err := Timeline(&b, tl, en.st); err != nil {
		t.Fatal(err)
	}
	if got, want := b.String(), "host h  2026-11-13 to 2026-11-13  1 day\n\n no errors and no events in this period\n"; got != want {
		t.Errorf("timeline of a quiet host:\n%q\nwant\n%q", got, want)
	}
}

func TestHosts(t *testing.T) {
	s := open(t, "demo", "", en)
	rows := append(s.c.Hosts(), view.HostRow{Name: "r12-n035", Grade: "ok", GradeText: "ok", DIMMs: 16})
	for _, v := range all {
		var b bytes.Buffer
		c := open(t, "demo", "", v)
		rows[0] = c.c.Hosts()[0]
		if err := Hosts(&b, rows, v.st); err != nil {
			t.Fatal(err)
		}
		check(t, "hosts", v, b.String())
	}
}

func TestWidth(t *testing.T) {
	for s, want := range map[string]int{
		"": 0, "abc": 3, "즉시 교체": 9, "row 고장": 8, "░▒▓█": 4, "│└─": 3, "e\u0301": 1, "ＡＢ": 4, "\x1b[1;31mUE\x1b[0m 1": 4,
	} {
		if got := Width(s); got != want {
			t.Errorf("Width(%q) = %d, want %d", s, got, want)
		}
	}
	if got := padRight("교체", 6); got != "교체  " {
		t.Errorf("padRight = %q", got)
	}
	if got := padLeft("교체", 6); got != "  교체" {
		t.Errorf("padLeft = %q", got)
	}
	if got := padRight("abcdef", 3); got != "abcdef" {
		t.Errorf("padRight never cuts: %q", got)
	}
}

func TestTruncate(t *testing.T) {
	for _, tt := range []struct {
		s    string
		w    int
		want string
	}{
		{"abcdef", 6, "abcdef"}, {"abcdef", 4, "abc…"}, {"교체 고려", 5, "교체…"}, {"abcdef", 1, "."}, {"abc def", 5, "abc…"},
	} {
		if got := truncate(tt.s, tt.w, "…"); got != tt.want || Width(got) > tt.w {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.s, tt.w, got, tt.want)
		}
	}
}

func TestWrapAndFlow(t *testing.T) {
	got := wrap("the quick brown fox jumps over the lazy dog", 16)
	if strings.Join(got, "|") != "the quick brown|fox jumps over|the lazy dog" {
		t.Errorf("wrap = %q", got)
	}
	// Korean breaks between eojeol, counted in columns.
	got = wrap("같은 채널의 다른 DIMM 오류 0건", 14)
	if strings.Join(got, "|") != "같은 채널의|다른 DIMM 오류|0건" {
		t.Errorf("wrap = %q", got)
	}
	// A word longer than the line stays whole.
	got = wrap("see /var/lib/rasdaemon/ras-mc_event.db#mc_event:12 there", 10)
	if strings.Join(got, "|") != "see|/var/lib/rasdaemon/ras-mc_event.db#mc_event:12|there" {
		t.Errorf("wrap = %q", got)
	}
	if got := wrap("", 10); len(got) != 1 || got[0] != "" {
		t.Errorf("wrap of nothing = %q", got)
	}
	if got := flow([]string{"aa", "", "bbb", "cc"}, "  ", 8); strings.Join(got, "|") != "aa  bbb|cc" {
		t.Errorf("flow = %q", got)
	}
	// Colour takes no columns: coloured fields break where plain ones do.
	red := func(s string) string { return "\x1b[31m" + s + "\x1b[0m" }
	if got := flow([]string{red("aa"), red("bbb"), "cc"}, "  ", 8); len(got) != 2 || got[0] != red("aa")+"  "+red("bbb") || got[1] != "cc" {
		t.Errorf("flow of coloured fields = %q", got)
	}
}

func TestSpark(t *testing.T) {
	st := Style{}
	// Zero is the lowest glyph; one error among thousands is still above it.
	if got := st.spark([]float64{0, 1, 5000}); got != "▁▂█" {
		t.Errorf("spark = %q", got)
	}
	if got := st.spark([]float64{0, 0, 0}); got != "▁▁▁" {
		t.Errorf("spark of nothing = %q", got)
	}
	if got := (Style{ASCII: true}).spark([]float64{0, 1, 2, 3, 4, 5, 6}); got != "_.:-=+#" && len(got) != 7 {
		t.Errorf("ASCII spark = %q", got)
	}
	if got := st.spark(nil); got != "" {
		t.Errorf("spark(nil) = %q", got)
	}
}

func TestStyleWidth(t *testing.T) {
	for in, want := range map[int]int{0: DefaultWidth, 10: MinWidth, 80: 80, 1000: MaxWidth} {
		if got := (Style{Width: in}).width(); got != want {
			t.Errorf("width(%d) = %d, want %d", in, got, want)
		}
	}
	for width, want := range map[int]int{60: 32, 80: 64, 100: 64, 140: 128, 200: 128} {
		if got := (Style{Width: width}).MapCells(); got != want {
			t.Errorf("MapCells at %d columns = %d, want %d", width, got, want)
		}
	}
}

func TestGlyphsCarryTheGrade(t *testing.T) {
	// A grade must be readable without colour: every grade has its own
	// glyph, in both glyph sets, and the heat map has five distinct shades.
	for _, st := range []Style{{}, {ASCII: true}} {
		seen := map[string]string{}
		for _, state := range []string{"ok", "observe", "consider-replacement", "replace-now", "hold-replacement", view.SlotEmpty} {
			g := st.gradeGlyph(state)
			if other, dup := seen[g]; dup {
				t.Errorf("ASCII=%v: %s and %s share the glyph %q", st.ASCII, state, other, g)
			}
			if Width(g) != 2 {
				t.Errorf("ASCII=%v: glyph %q of %s is not two columns wide", st.ASCII, g, state)
			}
			seen[g] = state
		}
		shades := map[string]bool{}
		for _, s := range st.g().shade {
			shades[s] = true
		}
		if len(shades) != 5 {
			t.Errorf("ASCII=%v: the heat map has %d distinct shades", st.ASCII, len(shades))
		}
	}
	for n, want := range map[int64]int{0: 0, 1: 1, 2: 1, 3: 2, 9: 2, 10: 3, 99: 3, 100: 4, 1 << 40: 4} {
		if got := heatLevel(n, view.MapScale); got != want {
			t.Errorf("heatLevel(%d) = %d, want %d", n, got, want)
		}
	}
}

func TestTopologyLayout(t *testing.T) {
	st := Style{}
	lv := &view.Level{Name: view.LevelRow, Known: true}
	// One wide branch, then branches too narrow to draw, then a fold.
	shares := []float64{0.90, 0.004, 0.004, 0.004, 0.088}
	for i, s := range shares {
		seg := view.Segment{Label: "r", Parent: 0, Share: s}
		if i == len(shares)-1 {
			seg.Label, seg.Others = "", 40
		}
		lv.Segments = append(lv.Segments, seg)
	}
	for _, width := range []int{20, 57, 92, 180} {
		segs := st.topoLayout(lv, width)
		at := 0
		for _, s := range segs {
			if s.start != at || s.end-s.start < 2 {
				t.Errorf("width %d: segment %+v does not follow column %d or is too narrow", width, s, at)
			}
			at = s.end
		}
		if at != width {
			t.Errorf("width %d: the segments end at column %d", width, at)
		}
		line := st.topoLine(segs, true)
		if Width(line) != width+1 {
			t.Errorf("width %d: the line is %d columns wide", width, Width(line))
		}
	}
	// The narrow branches joined the fold: "+43" stands for 40 + 3.
	segs := st.topoLayout(lv, 92)
	if len(segs) != 2 || segs[1].label != "+43" {
		t.Errorf("segments = %+v", segs)
	}
	if got := percent(0.004); got != "<1%" {
		t.Errorf("percent(0.004) = %q", got)
	}
	if percent(0) != "0%" || percent(0.999) != "100%" || percent(0.5) != "50%" {
		t.Error("percent")
	}
}

func TestTableDropsColumnsThatDoNotFit(t *testing.T) {
	wide := open(t, "demo", "", en).overview()
	narrow := open(t, "demo", "", w60).overview()
	if !strings.Contains(wide, "RULES") || !strings.Contains(wide, "TREND 30D") {
		t.Errorf("columns missing at 100 columns:\n%s", wide)
	}
	if strings.Contains(narrow, "RULES") || !strings.Contains(narrow, "FINDING") || !strings.Contains(narrow, "CPU0_C1") {
		t.Errorf("at 60 columns the table should drop the rules and keep the finding:\n%s", narrow)
	}
}

func TestDetect(t *testing.T) {
	env := func(kv map[string]string) func(string) string {
		return func(k string) string { return kv[k] }
	}
	for _, tt := range []struct {
		name     string
		st       Style
		color    string
		terminal bool
		columns  int
		env      map[string]string
		want     Style
	}{
		{"a terminal gets colour and its width", Style{}, ColorAuto, true, 132, nil, Style{Color: true, Width: 132}},
		{"a pipe gets neither", Style{}, ColorAuto, false, 0, nil, Style{}},
		{"NO_COLOR turns colour off", Style{}, ColorAuto, true, 80, map[string]string{"NO_COLOR": "1"}, Style{Width: 80}},
		{"a dumb terminal has no colour", Style{}, ColorAuto, true, 80, map[string]string{"TERM": "dumb"}, Style{Width: 80}},
		{"always wins over NO_COLOR and a pipe", Style{}, ColorAlways, false, 0, map[string]string{"NO_COLOR": "1"}, Style{Color: true}},
		{"never wins over a terminal", Style{}, ColorNever, true, 80, nil, Style{Width: 80}},
		{"a width that was asked for stays", Style{Width: 70}, ColorAuto, true, 132, nil, Style{Color: true, Width: 70}},
		{"COLUMNS sets the width of a pipe", Style{}, ColorAuto, false, 0, map[string]string{"COLUMNS": "90"}, Style{Width: 90}},
		{"the C locale gets ASCII", Style{}, ColorAuto, false, 0, map[string]string{"LANG": "C"}, Style{ASCII: true}},
		{"LC_ALL decides before LANG", Style{}, ColorAuto, false, 0, map[string]string{"LANG": "C", "LC_ALL": "en_US.UTF-8"}, Style{}},
		{"a legacy Korean character set gets ASCII glyphs", Style{}, ColorAuto, false, 0, map[string]string{"LANG": "ko_KR.eucKR"}, Style{ASCII: true}},
		{"utf8 without the dash", Style{}, ColorAuto, false, 0, map[string]string{"LC_CTYPE": "en_US.utf8"}, Style{}},
		{"ASCII that was asked for stays", Style{ASCII: true}, ColorAuto, false, 0, map[string]string{"LANG": "ko_KR.UTF-8"}, Style{ASCII: true}},
	} {
		if got := Detect(tt.st, tt.color, tt.terminal, tt.columns, env(tt.env)); got != tt.want {
			t.Errorf("%s: %+v, want %+v", tt.name, got, tt.want)
		}
	}
	// A file is not a terminal.
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, terminal := Size(f); terminal {
		t.Error("a file was taken for a terminal")
	}
	if _, terminal := Size(nil); terminal {
		t.Error("no file was taken for a terminal")
	}
}

// TestEveryScenarioDraws draws every screen for every DIMM with a finding
// in every built-in scenario, in three styles. The scenarios cover every
// fault mode and every rule; the golden files pin a few of them, this test
// makes sure that none of the others breaks a figure: nothing fails, no
// line is wider than the style allows, and ASCII stays ASCII.
func TestEveryScenarioDraws(t *testing.T) {
	styles := []variant{en, {"ko-w60", Style{Lang: i18n.KO, Width: 60}}, {"ascii-w140", Style{Lang: i18n.EN, ASCII: true, Width: 140}}}
	for _, name := range synth.Names() {
		for _, v := range styles {
			s := open(t, name, "", v)
			inspect(t, name+" overview", v, s.overview())
			inspect(t, name+" timeline", v, s.timeline(""))
			listed := 0
			for _, d := range s.h.DIMMs {
				if !view.Listed(d) {
					continue
				}
				// Every finding once; of the DIMMs that only have a stray
				// error, a few are enough.
				if listed++; listed > 4 {
					break
				}
				ref := d.UID
				inspect(t, name+" dimm "+d.Slot, v, s.examination(ref))
				inspect(t, name+" why "+d.Slot, v, s.why(ref))
				inspect(t, name+" map "+d.Slot, v, s.addressMap(ref, view.MapOptions{}))
				inspect(t, name+" timeline "+d.Slot, v, s.timeline(ref))
			}
			if listed == 0 {
				t.Errorf("scenario %s has no DIMM with a finding", name)
			}
		}
	}
}
