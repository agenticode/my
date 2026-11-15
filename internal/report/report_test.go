package report

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/fmfm"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/scan"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/synth"
	"github.com/agenticode/my/internal/testledger"
	"github.com/agenticode/my/internal/testutil"
	"github.com/agenticode/my/internal/view"

	_ "modernc.org/sqlite" // to change a ledger by hand
)

func TestMain(m *testing.M) { testledger.Main(m) }

var ctx = context.Background()

// now is the time reports are made at in tests.
var now = time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)

// options are the options of a report in tests: every value that would
// differ from run to run is fixed.
func options(lang i18n.Lang, aud Audience) Options {
	return Options{Lang: lang, Audience: aud, Now: now, Tool: "my test", Case: &CaseInfo{ID: "0123456789ab"}}
}

// buildFrom builds the report of the only host of a ledger.
func buildFrom(t testing.TB, s *store.Store, opts Options) *Document {
	t.Helper()
	c, err := view.Open(ctx, s, opts.Lang, engine.Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Host("")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Build(ctx, c, h, opts)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// build builds the report of a built-in scenario.
func build(t testing.TB, scenario string, fl synth.Flavor, lang i18n.Lang, aud Audience) *Document {
	t.Helper()
	return buildFrom(t, testledger.Open(t, scenario, 1, fl), options(lang, aud))
}

// render writes a report and reads it back as a tree.
func render(t testing.TB, doc *Document) ([]byte, *elem) {
	t.Helper()
	var b bytes.Buffer
	if err := doc.Write(&b); err != nil {
		t.Fatal(err)
	}
	root, err := parseHTML(b.Bytes())
	if err != nil {
		t.Fatalf("%v", err)
	}
	return b.Bytes(), root
}

// outline writes what a reader of a report sees, without how it is
// styled: headings, sentences, table rows, and for every figure its name,
// what its marks say and the labels drawn in it. The golden files of the
// report hold outlines, so a change of the style sheet does not touch
// them and a change of a number, a sentence or a mark does.
func outline(root *elem) string {
	var b strings.Builder
	line := func(depth int, s string) {
		b.WriteString(strings.Repeat("  ", depth))
		b.WriteString(s)
		b.WriteByte('\n')
	}
	name := func(e *elem) string {
		n := e.tag
		if id := e.attrs["id"]; id != "" {
			n += "#" + id
		}
		if c := e.attrs["class"]; c != "" {
			n += "." + strings.Join(strings.Fields(c), ".")
		}
		return n
	}
	var rec func(e *elem, depth int)
	rec = func(e *elem, depth int) {
		switch e.tag {
		case "", "head", "style", "script":
			return
		case "svg":
			if e.attrs["aria-hidden"] == "true" {
				return // a swatch beside its label
			}
			line(depth, name(e)+": "+e.attrs["aria-label"])
			var texts []string
			e.walk(func(x *elem) {
				switch {
				case x == e:
				case x.tag == "title" && x.parent != e:
					line(depth+1, "mark: "+x.content0())
				case x.tag == "text":
					texts = append(texts, x.content0())
				}
			})
			if len(texts) > 0 {
				line(depth+1, "text: "+strings.Join(texts, " | "))
			}
			return
		case "table":
			line(depth, name(e))
			for _, sec := range e.kids {
				for _, tr := range sec.kids {
					var cells []string
					for _, c := range tr.kids {
						cells = append(cells, c.content())
					}
					prefix := "| "
					if tr.hasClass("raw") {
						prefix = "raw: "
					}
					line(depth+1, prefix+strings.Join(cells, " | "))
				}
			}
			return
		case "dl":
			for _, d := range e.kids {
				if len(d.kids) >= 2 {
					line(depth, d.kids[0].content()+" = "+d.kids[1].content())
				}
			}
			return
		case "h1", "h2", "h3", "h4":
			line(depth, strings.Repeat("#", int(e.tag[1]-'0'))+" "+e.content())
			return
		case "pre":
			lines := strings.Split(strings.TrimRight(e.content0(), "\n"), "\n")
			line(depth, fmt.Sprintf("%s: %d lines, the first: %s", name(e), len(lines), lines[0]))
			return
		case "p", "li", "figcaption", "summary", "button":
			if text := e.content(); text != "" {
				line(depth, name(e)+": "+text)
			}
			return
		case "section", "article", "details", "figure", "aside", "footer", "header":
			line(depth, name(e))
			depth++
		}
		for _, k := range e.kids {
			rec(k, depth)
		}
	}
	line(0, "lang: "+root.attrs["lang"])
	line(0, "title: "+root.find("title")[0].content0())
	rec(root.kids[1], 0)
	return b.String()
}

// tempRE finds the directory a test ledger was read from.
var tempRE = regexp.MustCompile(`[^\s|]*/my-testledger-\d+/[^/\s]+/root`)

// TestGolden pins what the report of a case says and draws.
func TestGolden(t *testing.T) {
	for _, tt := range []struct {
		name     string
		scenario string
		flavor   synth.Flavor
		lang     i18n.Lang
		aud      Audience
	}{
		{"demo.customer.en", "demo", "", i18n.EN, Customer},
		{"demo.fa.ko", "demo", "", i18n.KO, FA},
		{"demo-amd.customer.en", "demo", synth.FlavorAMD, i18n.EN, Customer},
		{"system-channel.fa.en", "system-channel", "", i18n.EN, FA},
	} {
		t.Run(tt.name, func(t *testing.T) {
			doc := build(t, tt.scenario, tt.flavor, tt.lang, tt.aud)
			raw, root := render(t, doc)
			for _, bad := range checkHTML(root, raw) {
				t.Error(bad)
			}
			// A ledger read from a copy of a host's files names the
			// directory of the copy, which differs from run to run.
			text := tempRE.ReplaceAllString(outline(root), "ROOT")
			testutil.Golden(t, tt.name+".txt", []byte(text))
		})
	}
}

// TestEveryScenarioIsValid writes the report of every built-in scenario,
// for both audiences and in both languages, and holds each against the
// rules of a valid report. The scenarios cover every fault mode and every
// rule.
func TestEveryScenarioIsValid(t *testing.T) {
	for _, name := range synth.Names() {
		for _, v := range []struct {
			lang i18n.Lang
			aud  Audience
		}{{i18n.EN, Customer}, {i18n.KO, FA}} {
			doc := build(t, name, "", v.lang, v.aud)
			raw, root := render(t, doc)
			for _, bad := range checkHTML(root, raw) {
				t.Errorf("%s %s %s: %s", name, v.lang, v.aud, bad)
			}
			if len(doc.DIMMs) == 0 {
				t.Errorf("%s: the report describes no DIMM", name)
			}
			sections := root.find("section.sec")
			if len(sections) != 8 {
				t.Fatalf("%s: %d sections, want 8", name, len(sections))
			}
			for i, s := range sections {
				if s.attrs["id"] != fmt.Sprintf("s%d", i+1) || len(s.kids) < 2 {
					t.Errorf("%s: section %d is %s with %d parts", name, i+1, s.attrs["id"], len(s.kids))
				}
			}
			if len(raw) > 1<<20 {
				t.Errorf("%s %s %s: the report is %d bytes", name, v.lang, v.aud, len(raw))
			}
		}
	}
	// What the platforms of a host tree record: fewer location fields.
	for _, fl := range []synth.Flavor{synth.FlavorIntel, synth.FlavorAMD, synth.FlavorGHES} {
		doc := build(t, "demo", fl, i18n.EN, FA)
		raw, root := render(t, doc)
		for _, bad := range checkHTML(root, raw) {
			t.Errorf("demo %s: %s", fl, bad)
		}
	}
}

func TestSummary(t *testing.T) {
	doc := build(t, "demo", "", i18n.EN, Customer)
	_, root := render(t, doc)
	sum := root.find("section.sec")[0]
	text := sum.content()
	for _, want := range []string{
		"replace now host r12-n034",
		"CPU0_C1 S/N 3B9C56: replace now (finding, confidence high). 1 uncorrected error, the latest on 2026-11-12 03:14 UTC at rank0 dev7 bg0 bank3 row 0x1a2f col 0x238. Replace the DIMM.",
		"1 more with errors or a finding: CPU1_D1 observe.",
		"Case ID 0123456789ab",
		"Period on record 2026-07-31 to 2026-11-13, 4 boots",
		"Errors CE 1,284, UE 1",
		"Observation grade A (bank, row and column known)",
		"DIMMs 16 in 32 slots, 2 with errors or a finding",
		"customer report, generated 2026-11-13 09:30 UTC by my test",
		"Engine version " + engine.Version,
		"A grade is a finding.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the summary lacks %q:\n%s", want, text)
		}
	}
	if v := sum.find("div.verdict"); len(v) != 1 || !v[0].hasClass("g-replace") {
		t.Errorf("the summary does not carry the grade of the host")
	}
	// The contents list leads to every other section.
	var hrefs []string
	for _, a := range sum.find("nav")[0].find("a") {
		hrefs = append(hrefs, a.attrs["href"])
	}
	if strings.Join(hrefs, " ") != "#s2 #s3 #s4 #s5 #s6 #s7 #s8" {
		t.Errorf("contents = %v", hrefs)
	}

	// A host without errors says so.
	quiet := buildFrom(t, emptyHost(t), options(i18n.EN, Customer))
	raw, root := render(t, quiet)
	for _, bad := range checkHTML(root, raw) {
		t.Error(bad)
	}
	if text := root.find("section.sec")[0].content(); !strings.Contains(text, "No DIMM of this host has errors on record.") || !strings.Contains(text, "ok") {
		t.Errorf("summary of a host without errors:\n%s", text)
	}
	if len(quiet.DIMMs) != 0 || len(quiet.Records) != 0 {
		t.Errorf("a host without errors: %d DIMMs, %d records", len(quiet.DIMMs), len(quiet.Records))
	}
}

// emptyHost returns a ledger with a host that has modules and no errors.
func emptyHost(t testing.TB) *store.Store {
	t.Helper()
	ds := testledger.Generate(t, "demo", 1, func(sc *synth.Scenario) {
		sc.Faults, sc.NoisePerDIMMDay = nil, 0
		sc.Repairs, sc.Offlines = nil, nil
	})
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "quiet.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := ingest.Run(ctx, s, []adapter.Adapter{ds.Adapter()}, ingest.Options{Kind: model.ScanSynthetic, Now: ds.Scenario.End}); err != nil {
		t.Fatal(err)
	}
	return s
}

// TestFiguresDrawTheViews checks the figures against the data they are
// drawn from: a mark for every slot, every segment, every cell with errors
// and every event.
func TestFiguresDrawTheViews(t *testing.T) {
	doc := build(t, "demo", "", i18n.EN, Customer)
	_, root := render(t, doc)

	// Slot map: one cell per slot, each with the grade of its DIMM.
	sm := root.find("svg.slotmap")
	if len(sm) != 1 {
		t.Fatalf("%d slot maps", len(sm))
	}
	classes := map[string]int{}
	var linked []string
	sm[0].walk(func(e *elem) {
		if !e.hasClass("slot") {
			return
		}
		for _, c := range strings.Fields(e.attrs["class"]) {
			if strings.HasPrefix(c, "g-") {
				classes[c]++
			}
		}
		if e.tag == "a" {
			linked = append(linked, e.attrs["href"])
		}
	})
	if classes["g-replace"] != 1 || classes["g-observe"] != 1 || classes["g-ok"] != 14 || classes["g-empty"] != 16 || len(classes) != 4 {
		t.Errorf("slot map cells: %v", classes)
	}
	if strings.Join(linked, " ") != "#dimm-CPU0_C1 #dimm-CPU1_D1" {
		t.Errorf("cells that lead to a finding: %v", linked)
	}
	// A grade is a mark as well as a colour: the two DIMMs with findings
	// carry one, the quiet ones none.
	if n := len(sm[0].find("g.gl")); n != 2 {
		t.Errorf("%d grade marks in the slot map, want 2", n)
	}

	// Address map: a mark per cell with errors, linked to its records.
	d := doc.DIMMs[0]
	maps := root.find("svg.amap")
	if len(maps) != 2 {
		t.Fatalf("%d address maps, want 2", len(maps))
	}
	points := maps[0].find("a.pt")
	if len(points) != len(d.Map.Refs) || len(points) != 13 {
		t.Errorf("%d linked cells, the map has %d cells with errors", len(points), len(d.Map.Refs))
	}
	ue := 0
	for _, p := range points {
		if len(p.find("rect.ue")) == 1 {
			ue++
			if !strings.Contains(p.attrs["data-label"], "row 0x1a2f, columns 0x220 to 0x23f: 568 errors in 568 records, 1 uncorrected; first 2026-09-24, latest 2026-11-12") {
				t.Errorf("cell of the uncorrected error: %s", p.attrs["data-label"])
			}
			if keys := strings.Fields(p.attrs["data-rec"]); len(keys) != 3 || keys[0] != "e1228" {
				t.Errorf("records of the cell of the uncorrected error: %v", keys)
			}
		}
	}
	if ue != 1 {
		t.Errorf("%d cells marked as uncorrected, want 1", ue)
	}

	// Topology bars: four levels, the widest path leads to the row.
	topo := root.find("svg.topo")[0]
	segs := topo.find("g.seg")
	if len(segs) != 5 {
		t.Errorf("%d segments, want 5 (rank, device, bank, two rows)", len(segs))
	}
	if text := topo.content(); !strings.Contains(text, "0x1a2f") || !strings.Contains(text, "98%") {
		t.Errorf("topology labels: %s", text)
	}

	// Timeline of the DIMM: a column per day with errors, a mark per day
	// with an event.
	tl := root.find("svg.tline")
	if len(tl) != 3 {
		t.Fatalf("%d timelines, want 3 (host, two DIMMs)", len(tl))
	}
	days := 0
	for _, n := range d.Timeline.Series[0].CE {
		if n > 0 {
			days++
		}
	}
	if bars := tl[1].find("rect"); len(tl[1].find("g.bars")[0].kids) != days {
		t.Errorf("%d columns, %d days with errors (%d rects)", len(tl[1].find("g.bars")[0].kids), days, len(bars))
	}
	marks := 0
	for _, g := range d.Timeline.Markers {
		marks += len(g.Days)
	}
	if got := len(tl[1].find("circle.mk")) + len(tl[1].find("rect.mk")); got != marks {
		t.Errorf("%d marks drawn, the timeline has %d days with events", got, marks)
	}
	if n := len(tl[1].find("rect.mk-ue")); n != 1 {
		t.Errorf("%d marks of uncorrected errors, want 1", n)
	}
}

// TestRecords checks the appendix: every record a mark or a statement
// names is there with its original text, and nothing else.
func TestRecords(t *testing.T) {
	doc := build(t, "demo", "", i18n.EN, Customer)
	_, root := render(t, doc)
	named := map[string]bool{}
	root.walk(func(e *elem) {
		for _, k := range strings.Fields(e.attrs["data-rec"]) {
			named[k] = true
		}
	})
	if len(named) != len(doc.Records) || len(named) < 20 {
		t.Errorf("%d records are named by marks, the appendix holds %d", len(named), len(doc.Records))
	}
	rows := map[string]*elem{}
	for _, tb := range root.find("tbody.rec") {
		if tb.parent.attrs["id"] == "panel-records" {
			t.Error("the panel holds records before anything was chosen")
		}
		rows[strings.TrimPrefix(tb.attrs["id"], "rec-")] = tb
	}
	cited := 0
	var last time.Time
	for _, r := range doc.Records {
		tb := rows[r.Key]
		if tb == nil || !named[r.Key] {
			t.Errorf("record %s: in the appendix %v, named %v", r.Key, tb != nil, named[r.Key])
			continue
		}
		if r.Raw == "" || len(tb.kids) != 2 || !strings.Contains(tb.kids[1].content(), strings.Join(strings.Fields(r.Raw), " ")) {
			t.Errorf("record %s is listed without its original text", r.Key)
		}
		if r.RawRef == "" || !strings.Contains(tb.kids[0].content(), r.RawRef) {
			t.Errorf("record %s does not say where the original is", r.Key)
		}
		if r.Cited {
			cited++
			if !tb.hasClass("cited") {
				t.Errorf("record %s is cited and not marked for print", r.Key)
			}
		}
		if r.At.Before(last) {
			t.Errorf("record %s is out of time order", r.Key)
		}
		last = r.At
	}
	// Five statements of CPU0_C1 and one of CPU1_D1 cite records: at most
	// four each.
	if cited < 6 || cited > 6*MaxCited {
		t.Errorf("%d cited records", cited)
	}
	// The uncorrected error, the repair and the page offlines are there.
	for _, k := range []string{"e1228", "a1", "a2", "a8"} {
		if rows[k] == nil {
			t.Errorf("record %s is missing", k)
		}
	}
	if text := rows["a1"].content(); !strings.Contains(text, "hard PPR") || !strings.Contains(text, "rank0 dev7 bg0 bank3 row 0x1a2f") {
		t.Errorf("the repair: %s", text)
	}
	if text := root.find("section.sec")[7].content(); !strings.Contains(text, fmt.Sprintf("%d of the 1,293 records of this host", len(doc.Records))) {
		t.Errorf("the appendix does not say how many records it lists")
	}

	// The statements of a finding lead to the records they cite.
	links := root.find("a.recs")
	if len(links) != 5 {
		t.Fatalf("%d statements with records, want 5", len(links))
	}
	if l := links[0]; l.content() != "1 record" || l.attrs["data-rec"] != "e1228" || !strings.HasPrefix(l.attrs["data-label"], "[R-UE-01] 1 uncorrected error") {
		t.Errorf("first statement: %q %v", l.content(), l.attrs)
	}
	if l := links[1]; l.content() != "421 records" || len(strings.Fields(l.attrs["data-rec"])) != MaxCited {
		t.Errorf("second statement: %q %v", l.content(), l.attrs)
	}
}

// TestFMFMLines checks the error records with FMFM field names: every
// line is a JSON object, and the FMFM reader of this tool reads the lines
// back as the same records.
func TestFMFMLines(t *testing.T) {
	doc := build(t, "demo", "", i18n.EN, Customer)
	_, root := render(t, doc)
	pre := root.find("pre.json")
	if len(pre) != 1 {
		t.Fatalf("%d JSON blocks", len(pre))
	}
	lines := strings.Split(strings.TrimSpace(pre[0].content0()), "\n")
	var events []*Record
	for _, r := range doc.Records {
		if r.Event != nil {
			events = append(events, r)
		}
	}
	if len(lines) != len(events) || len(lines) < 10 {
		t.Fatalf("%d lines, %d error records", len(lines), len(events))
	}
	for i, l := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("line %d: %v", i+1, err)
		}
		e := events[i].Event
		if m["HostID"] != e.Host || m["ErrorSeverity"] != string(e.ErrorSeverity) || m["raw_ref"] != e.RawRef || m["grade"] != string(e.Grade) {
			t.Errorf("line %d: %v", i+1, m)
		}
		for _, f := range []string{"id", "fingerprint", "dimm_uid", "scan_id", "raw"} {
			if _, ok := m[f]; ok {
				t.Errorf("line %d carries the ledger field %s", i+1, f)
			}
		}
	}
	// Read back through the FMFM adapter.
	a := fmfm.NewReader(strings.NewReader(pre[0].content0()), "report", fmfm.Options{Now: now})
	var got adapter.Collected
	rep := a.Collect(ctx, &got)
	if len(got.Events) != len(events) {
		t.Fatalf("the FMFM reader finds %d records in %d lines (%+v)", len(got.Events), len(lines), rep)
	}
	sort.SliceStable(got.Events, func(i, j int) bool { return got.Events[i].ErrorDT.Before(got.Events[j].ErrorDT) })
	sort.SliceStable(events, func(i, j int) bool { return events[i].Event.ErrorDT.Before(events[j].Event.ErrorDT) })
	for i, g := range got.Events {
		e := events[i].Event
		if g.Row != e.Row || g.Column != e.Column || g.Bank != e.Bank || g.BankGroup != e.BankGroup || g.DeviceId != e.DeviceId || g.Rank != e.Rank ||
			g.ErrorSeverity != e.ErrorSeverity || g.OsPhysicalAddress != e.OsPhysicalAddress || g.ErrorVector != e.ErrorVector ||
			g.ErrorDT.Sub(e.ErrorDT).Abs() > time.Millisecond {
			t.Errorf("record %d read back as %+v, the ledger has %+v", i, g, e)
			break
		}
	}
}

func TestAudience(t *testing.T) {
	customer := build(t, "demo", "", i18n.EN, Customer)
	_, croot := render(t, customer)
	level2 := []Level2{{
		DIMMUID: customer.DIMMs[0].Card.DIMM.UID, Analyzer: "example-analyzer 0.3",
		Lines: []Level2Line{{"physical row", "0x1a2f is next to 0x1a31"}, {"bit line", "BL 412"}},
		Notes: []string{"Signature of a word line defect."},
	}}
	opts := options(i18n.EN, FA)
	opts.Level2 = level2
	fa := buildFrom(t, testledger.Open(t, "demo", 1, ""), opts)
	raw, froot := render(t, fa)
	for _, bad := range checkHTML(froot, raw) {
		t.Error(bad)
	}

	ctext, ftext := croot.content(), froot.content()
	// What the failure analysis report adds: the steps of the confidence,
	// the faults one by one, and the output of the vendor analyzer.
	for _, want := range []string{
		"failure analysis report", "confidence high, 91", "90 the grade rests on R-UE-01", "+5 1 check supports the finding",
		"row fault at rank0 dev7 bg0 bank3 row 0x1a2f: CE 1,231, UE 1; 2026-09-24 to 2026-11-12; rising; failed again later; confined to one place; 2 rows, 9 columns, 1 bank; observation grade A",
		"Vendor analyzer (Level 2)", "from example-analyzer 0.3", "physical row 0x1a2f is next to 0x1a31", "Signature of a word line defect.",
	} {
		if !strings.Contains(ftext, want) {
			t.Errorf("the failure analysis report lacks %q", want)
		}
		if strings.Contains(ctext, want) {
			t.Errorf("the customer report holds %q", want)
		}
	}
	if len(froot.find("div.level2")) != 1 {
		t.Errorf("the Level 2 output belongs to one DIMM")
	}
	// The customer report holds no Level 2 output, even when it is given.
	copts := options(i18n.EN, Customer)
	copts.Level2 = level2
	_, croot = render(t, buildFrom(t, testledger.Open(t, "demo", 1, ""), copts))
	if text := croot.content(); strings.Contains(text, "Level 2") || strings.Contains(text, "example-analyzer") || strings.Contains(text, "BL 412") {
		t.Error("the customer report shows the output of a vendor analyzer")
	}
	// Both say the same about the finding itself.
	for _, want := range []string{"replace now (finding, confidence high", "R-UE-01", "Replace the DIMM. Attach a case bundle of this DIMM to the return."} {
		if !strings.Contains(ctext, want) || !strings.Contains(ftext, want) {
			t.Errorf("one of the reports lacks %q", want)
		}
	}

	if _, err := ParseAudience("FA "); err != nil {
		t.Error(err)
	}
	if _, err := ParseAudience("everyone"); err == nil {
		t.Error("an unknown audience was accepted")
	}
}

// TestHostileText puts markup into every text a host can supply and
// checks that none of it becomes markup of the report.
func TestHostileText(t *testing.T) {
	const evil = `<script>alert(1)</script><img src=x onerror=alert(2)>"'&amp;</pre></style>]]>`
	ds := testledger.Generate(t, "demo", 1, nil)
	path := filepath.Join(t.TempDir(), "evil.db")
	s, err := store.Open(ctx, path, store.Create)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := ingest.Run(ctx, s, []adapter.Adapter{ds.Adapter()}, ingest.Options{Kind: model.ScanSynthetic, Now: ds.Scenario.End}); err != nil {
		t.Fatal(err)
	}
	// Rewrite what sources supply: record texts, names, labels. Control
	// characters and bytes that are not UTF-8 are part of the attack.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`UPDATE event_raw SET text = text || ?`,
		`UPDATE event SET raw_ref = raw_ref || ?, source = source || ?, label = ?`,
		`UPDATE action SET raw = raw || ?, detail = ?, method = ?`,
		`UPDATE dimm SET manufacturer = ?, part_number = ?`,
		`UPDATE host SET system_vendor = ?, system_product = ?`,
		`UPDATE source SET origin = ?, detail = ?`,
	} {
		n := strings.Count(stmt, "?")
		args := make([]any, n)
		for i := range args {
			args[i] = evil + "\x01\x1b[31m\xff\r\n"
		}
		if _, err := db.Exec(stmt, args...); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	// The remarks of a source are a JSON list in the ledger.
	notes, _ := json.Marshal([]string{evil})
	if _, err := db.Exec(`UPDATE source SET notes = ?`, string(notes)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err = store.Open(ctx, path, store.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	opts := options(i18n.EN, FA)
	opts.Tool = "my " + evil
	opts.Case.ID = evil
	doc := buildFrom(t, s, opts)
	doc.DIMMs[0].Level2 = []Level2{{Analyzer: evil, Lines: []Level2Line{{evil, evil}}, Notes: []string{evil}}}
	raw, root := render(t, doc)
	for _, bad := range checkHTML(root, raw) {
		t.Error(bad)
	}
	// The texts are in the report as text, and the only script is the
	// report's own.
	text := root.content()
	if n := strings.Count(text, "<script>alert(1)</script>"); n < 20 {
		t.Errorf("the hostile text is shown %d times; it should be shown wherever it was put", n)
	}
	if n := len(root.find("script")); n != 1 {
		t.Errorf("%d script elements", n)
	}
	if bytes.Contains(raw, []byte("<img")) || bytes.Contains(raw, []byte("<script>alert")) || bytes.Contains(raw, []byte("onerror=alert(2)>\"")) {
		t.Error("hostile markup is in the report unescaped")
	}
	if bytes.ContainsAny(raw, "\x01\x1b\r") || !bytes.Contains(raw, []byte("\uFFFD")) {
		t.Error("control characters or invalid bytes are in the report, or were dropped without a trace")
	}
}

// TestSelfContained checks what makes a report one file that opens
// anywhere: one style sheet, one script, both inline and free of markup
// characters, and a declared language and colour scheme.
func TestSelfContained(t *testing.T) {
	raw, root := render(t, build(t, "demo", "", i18n.KO, Customer))
	if root.attrs["lang"] != "ko" {
		t.Errorf("lang = %q", root.attrs["lang"])
	}
	for _, text := range []string{styleSheet, script} {
		if strings.ContainsAny(text, "<&") {
			t.Error("the style sheet or the script holds '<' or '&': the report would not be well-formed")
		}
	}
	// The line breaking rules for Korean text.
	for _, rule := range []string{
		"html { word-break: keep-all; overflow-wrap: break-word; text-wrap-style: pretty; }",
		"h1, h2, h3, h4, caption, figcaption, legend, summary, th { text-wrap-style: balance; }",
		"p, li, dd, td { text-wrap-style: pretty; }",
		"@media print",
		"print-color-adjust: exact",
		"@page { size: A4;",
	} {
		if !strings.Contains(styleSheet, rule) {
			t.Errorf("the style sheet lacks %q", rule)
		}
	}
	if strings.Contains(styleSheet, "break-all") || strings.Contains(styleSheet, "word-wrap") {
		t.Error("the style sheet breaks Korean words")
	}
	metas := map[string]string{}
	for _, m := range root.find("meta") {
		metas[m.attrs["name"]] = m.attrs["content"]
	}
	if metas["color-scheme"] != "light" || metas["viewport"] == "" || metas["generator"] != "my test" {
		t.Errorf("meta = %v", metas)
	}
	// A number and its unit stay together.
	if !bytes.Contains(raw, []byte("64\u00a0GB")) {
		t.Error("a number and its unit are not bound")
	}
	// The same case gives the same bytes.
	again, _ := render(t, build(t, "demo", "", i18n.KO, Customer))
	if !bytes.Equal(raw, again) {
		t.Error("two reports of one case differ")
	}
}

// TestLimits checks that what a report leaves out is said where it is
// left out.
func TestLimits(t *testing.T) {
	// A bank fault covers more cells than lead to their records.
	doc := build(t, "bank", "", i18n.EN, Customer)
	_, root := render(t, doc)
	d := doc.DIMMs[0]
	if len(d.Map.Refs) <= MaxLinkedCells || len(d.Linked) != MaxLinkedCells {
		t.Fatalf("%d cells with errors, %d linked", len(d.Map.Refs), len(d.Linked))
	}
	amap := root.find("svg.amap")[0]
	if n := len(amap.find("a.pt")); n != MaxLinkedCells {
		t.Errorf("%d cells lead to records, want %d", n, MaxLinkedCells)
	}
	text := root.content()
	want := fmt.Sprintf("The %d cells with the most errors lead to their records; the other %d do not.", MaxLinkedCells, len(d.Map.Refs)-MaxLinkedCells)
	if !strings.Contains(text, want) {
		t.Errorf("the report does not say %q", want)
	}
	if want := fmt.Sprintf("The table lists the %d cells with the most errors of %d.", MaxCellRows, len(d.Map.Refs)); !strings.Contains(text, want) {
		t.Errorf("the report does not say %q", want)
	}
	// Every cell with errors still says what it holds.
	cells := 0
	amap.walk(func(e *elem) {
		if e.tag == "title" && e.parent != amap && strings.Contains(e.content0(), "columns") {
			cells++
		}
	})
	if cells != len(d.Map.Refs) {
		t.Errorf("%d cells describe themselves, %d hold errors", cells, len(d.Map.Refs))
	}

	// DIMMs with a stray error have their finding and no figures.
	doc = build(t, "system-channel", "", i18n.EN, Customer)
	_, root = render(t, doc)
	detailed := doc.detailed()
	if len(doc.DIMMs) != 14 || len(detailed) != 2 {
		t.Fatalf("%d DIMMs, %d with figures", len(doc.DIMMs), len(detailed))
	}
	if n := len(root.find("article.card")); n != 14 {
		t.Errorf("%d finding cards, want 14", n)
	}
	if n := len(root.find("svg.amap")); n != 2 {
		t.Errorf("%d address maps, want 2", n)
	}
	note := "12 more DIMMs have fewer than 10 errors and the finding \"observe\": their finding is in section 3, and this section leaves them out."
	if n := strings.Count(root.content(), note); n != 3 {
		t.Errorf("the note about the DIMMs without figures appears %d times, want 3 (sections 4, 5, 6)", n)
	}
	// With AllFigures every DIMM with errors is drawn.
	opts := options(i18n.EN, Customer)
	opts.AllFigures = true
	all := buildFrom(t, testledger.Open(t, "system-channel", 1, ""), opts)
	if len(all.detailed()) != 14 {
		t.Errorf("AllFigures: %d DIMMs with figures", len(all.detailed()))
	}
}

// TestChecks checks section 6: what speaks for a cause outside the DIMM,
// and what a person has to test.
func TestChecks(t *testing.T) {
	_, root := render(t, build(t, "demo", "", i18n.EN, Customer))
	sec := root.find("section.sec")[5]
	text := sec.content()
	for _, want := range []string{
		"open no other DIMM on this channel: there is nothing to compare with",
		"supports the other 15 DIMMs of this host have no unconfined errors",
		"Tests for a person Swap the DIMM with one from another channel, or reseat it, and scan again after some days.",
		"The records gave no check for this finding. Tests for a person No test is open.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the checks lack %q:\n%s", want, text)
		}
	}
	// Two open checks name the same test: it is listed once.
	if n := strings.Count(text, "Swap the DIMM with one from another channel"); n != 1 {
		t.Errorf("the swap test is listed %d times", n)
	}

	// A hold finding: the rule that fired is the first row, and its next
	// step the first test.
	_, root = render(t, build(t, "system-channel", "", i18n.EN, Customer))
	text = root.find("section.sec")[5].content()
	for _, want := range []string{
		"A cause outside the DIMM is suspected. Settle that before a DIMM is pulled.",
		"R-SYS-01 CPU0_B1 and CPU0_B2 share one channel",
		"Tests for a person Do not pull a DIMM yet. Check the channel first",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the checks of a hold finding lack %q:\n%s", want, text)
		}
	}
}

// TestGPU checks the report of a host whose finding is about GPU memory:
// the summary leads with the GPU, and its card leads to the records.
func TestGPU(t *testing.T) {
	as, _, err := scan.Detect(ctx, testutil.Testdata(t, "nvidia", "nvidia-smi-q.txt"), scan.DetectOptions{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "gpu.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := ingest.Run(ctx, s, as, ingest.Options{Kind: model.ScanImport, Now: now, Host: "gpu-host"}); err != nil {
		t.Fatal(err)
	}
	doc := buildFrom(t, s, options(i18n.EN, Customer))
	raw, root := render(t, doc)
	for _, bad := range checkHTML(root, raw) {
		t.Error(bad)
	}
	if len(doc.GPUs) != 1 || len(doc.DIMMs) != 0 || doc.GPUs[0].Grade != model.VerdictReplaceNow || doc.GPUs[0].Next == nil {
		t.Fatalf("GPUs %+v, %d DIMMs", doc.GPUs, len(doc.DIMMs))
	}
	g := doc.GPUs[0]
	sum := root.find("section.sec")[0].content()
	for _, want := range []string{
		"replace now host gpu-host",
		"GPU " + g.Key + ": replace now. The driver reports a row remapping failure as of 2026-11-13 09:30 UTC. Return the GPU for repair or replace it: its row remapping has failed.",
		"GPU memory 1 GPU with a finding",
	} {
		if !strings.Contains(sum, want) {
			t.Errorf("the summary lacks %q:\n%s", want, sum)
		}
	}
	if strings.Contains(sum, "No DIMM of this host has errors on record") || strings.Contains(sum, "DIMMs 0") {
		t.Errorf("the summary of a GPU finding speaks of DIMMs:\n%s", sum)
	}
	cards := root.find("article.card")
	if len(cards) != 1 || cards[0].attrs["id"] != g.Anchor || !strings.HasPrefix(g.Anchor, "gpu-") || !cards[0].hasClass("g-replace") {
		t.Fatalf("GPU card: %d cards, anchor %q", len(cards), g.Anchor)
	}
	links := cards[0].find("a.recs")
	if len(links) != 1 || !strings.HasPrefix(links[0].attrs["data-rec"], "g") || len(doc.Records) != 1 || doc.Records[0].Raw == "" || doc.Records[0].Kind != model.RecordGPU {
		t.Errorf("the GPU finding does not lead to its record: %d links, records %+v", len(links), doc.Records)
	}
	// The slot map section lists the GPU and leads to its card.
	if a := root.find("section.sec")[1].find("a"); len(a) != 1 || a[0].attrs["href"] != "#"+g.Anchor {
		t.Errorf("section 2 does not lead to the GPU finding")
	}
}

// TestSubject checks that the DIMM a case is about leads the report.
func TestSubject(t *testing.T) {
	s := testledger.Open(t, "demo", 1, "")
	c, err := view.Open(ctx, s, i18n.EN, engine.Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	h, _ := c.Host("")
	quiet, err := c.Find(ctx, h, "CPU0_A1")
	if err != nil {
		t.Fatal(err)
	}
	other, _ := c.Find(ctx, h, "CPU1_D1")
	for _, tt := range []struct {
		uid   string
		lead  string
		dimms int
	}{
		{"", "CPU0_C1 S/N 3B9C56: replace now", 2},
		{other.UID, "CPU1_D1 S/N 2AEB41: observe", 2},
		// A DIMM without errors that a case names is described too.
		{quiet.UID, "CPU0_A1 S/N " + quiet.DIMM.Serial + ": ok", 3},
	} {
		opts := options(i18n.EN, Customer)
		opts.Case.SubjectUID = tt.uid
		doc := buildFrom(t, s, opts)
		raw, root := render(t, doc)
		for _, bad := range checkHTML(root, raw) {
			t.Error(bad)
		}
		if text := root.find("p.conclusion")[0].content(); !strings.HasPrefix(text, tt.lead) || len(doc.DIMMs) != tt.dimms {
			t.Errorf("subject %q: %d DIMMs, conclusion %q", tt.uid, len(doc.DIMMs), text)
		}
		subjects := 0
		for _, d := range doc.DIMMs {
			if d.Subject {
				subjects++
				if !d.Detail {
					t.Errorf("the subject has no figures")
				}
			}
		}
		if (tt.uid != "") != (subjects == 1) {
			t.Errorf("subject %q: %d DIMMs marked", tt.uid, subjects)
		}
		if tt.uid != "" && len(root.find("path.pointer")) != 1 {
			t.Errorf("subject %q is not pointed at in the slot map", tt.uid)
		}
	}
}

func TestBuildRefusesAnotherLanguage(t *testing.T) {
	c, err := view.Open(ctx, testledger.Open(t, "demo", 1, ""), i18n.EN, engine.Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	h, _ := c.Host("")
	if _, err := Build(ctx, c, h, Options{Lang: i18n.KO}); err == nil {
		t.Error("a Korean report was built from sentences in English")
	}
	// Defaults: the language of the case, the customer audience, the
	// identity of the ledger.
	doc, err := Build(ctx, c, h, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Lang != i18n.EN || doc.Audience != Customer || len(doc.CaseID) != 12 || doc.Generated.IsZero() {
		t.Errorf("defaults: %s %s %q %v", doc.Lang, doc.Audience, doc.CaseID, doc.Generated)
	}
}

// phraseRE finds wording the texts of this project do not use.
var phraseRE = regexp.MustCompile(`(?i)not just|seamless|robust|powerful|leverage|unlock|—|–|·|이 아니라|가 아니라|을 넘어|를 넘어`)

func TestWords(t *testing.T) {
	for _, code := range Words.Codes() {
		m := Words[code]
		if m.EN == "" || m.KO == "" {
			t.Errorf("%s lacks a language", code)
		}
		en, ko := i18n.Placeholders(m.EN), i18n.Placeholders(m.KO)
		sort.Strings(en)
		sort.Strings(ko)
		// Korean needs no word for one and many, so it may leave out an
		// argument English only uses to choose between the two.
		for _, name := range ko {
			if !contains(en, name) {
				t.Errorf("%s: the Korean text uses {%s}, the English one does not", code, name)
			}
		}
		for _, name := range en {
			if !contains(ko, name) && !strings.Contains(m.EN, "{"+name+"|") {
				t.Errorf("%s: the Korean text leaves out {%s}", code, name)
			}
		}
		for _, text := range []string{m.EN, m.KO} {
			if found := phraseRE.FindString(text); found != "" {
				t.Errorf("%s: %q in %q", code, found, text)
			}
		}
	}
	// Every check that names a test names one that exists.
	for check, todo := range todoOf {
		if !Words.Has(todo) || !engine.Messages.Has(check) {
			t.Errorf("%s -> %s: unknown code", check, todo)
		}
	}
	for next, todo := range todoOfNext {
		if !Words.Has(todo) || !engine.Messages.Has(next) {
			t.Errorf("%s -> %s: unknown code", next, todo)
		}
	}
	// A code the report asks for and the catalog lacks would be printed
	// as the code itself.
	src, err := os.ReadFile("render.go")
	if err != nil {
		t.Fatal(err)
	}
	more, _ := os.ReadFile("figures.go")
	build, _ := os.ReadFile("build.go")
	src = append(append(src, more...), build...)
	for _, m := range regexp.MustCompile(`doc\.say\("([a-z0-9_.]+)"`).FindAllSubmatch(src, -1) {
		if code := string(m[1]); !Words.Has(code) && !strings.HasSuffix(code, ".") {
			t.Errorf("the report asks for %q, which the catalog lacks", code)
		}
	}
	for _, m := range regexp.MustCompile(`doc\.vsay\("([a-z0-9_.-]+)"`).FindAllSubmatch(src, -1) {
		if code := string(m[1]); !view.Messages.Has(code) && !strings.HasSuffix(code, ".") {
			t.Errorf("the report asks package view for %q, which its catalog lacks", code)
		}
	}
	// And a text nothing asks for is dead weight.
	for _, code := range Words.Codes() {
		prefix := code
		if i := strings.LastIndexByte(code, '.'); i >= 0 {
			prefix = code[:i+1]
		}
		if !bytes.Contains(src, []byte(`"`+code+`"`)) && !bytes.Contains(src, []byte(`"`+prefix+`"`)) && !strings.HasPrefix(code, "todo.") {
			t.Errorf("nothing asks for %q", code)
		}
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestDOM(t *testing.T) {
	// Text and attribute values are escaped; an element is closed.
	n := el("p", A{"class", "a b", "title", `"<&>`}, "x < y & z", el("br"), el("span"), nil, (*node)(nil), []*node{txt("t")})
	var b strings.Builder
	if err := n.write(&b, false); err != nil {
		t.Fatal(err)
	}
	if got, want := b.String(), `<p class="a b" title="&quot;&lt;&amp;&gt;">x &lt; y &amp; z<br /><span></span>t</p>`; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	// An attribute is there once, and an empty one not at all.
	n = el("td", A{"class", "r"}).add(A{"class", "r ue", "id", ""})
	if len(n.attrs) != 1 || n.attr("class") != "r ue" {
		t.Errorf("attributes = %v", n.attrs)
	}
	// Inside a figure an element without content closes itself.
	b.Reset()
	if err := el("svg", el("rect", A{"x", "1"}), el("g")).write(&b, false); err != nil {
		t.Fatal(err)
	}
	if got, want := b.String(), `<svg><rect x="1"/><g/></svg>`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	// What cannot be written is refused or replaced.
	b.Reset()
	if err := el("style", rawText("a < b")).write(&b, false); err == nil {
		t.Error("a style sheet with '<' was written")
	}
	if err := el("br", "x").write(&b, false); err == nil {
		t.Error("a line break with content was written")
	}
	if err := el("script", "text").write(&b, false); err == nil {
		t.Error("a script with escaped text was written")
	}
	if got := clean("a\x00b\x1bc\r\nd\re\xffg\tz"); got != "a\uFFFDb\uFFFDc\nd\ne\uFFFDg\tz" {
		t.Errorf("clean = %q", got)
	}
	// A date stays whole, with the word before it and the comma after
	// it; a last word of one or two letters is tied to the one before.
	parts := rich(" seen on 2026-11-12 03:14 UTC and 2026-11-13, at row 5 ")
	var got []string
	for _, p := range parts {
		if p.tag == "span" && p.attr("class") == "nw" {
			got = append(got, "["+p.kids[0].text+"]")
		} else {
			got = append(got, p.text)
		}
	}
	if want := " |[seen on 2026-11-12 03:14 UTC]| |[and 2026-11-13,]| at row\u00a05| "; strings.Join(got, "|") != want {
		t.Errorf("rich text:\n got  %q\n want %q", strings.Join(got, "|"), want)
	}
	for in, want := range map[string]string{"a": "a", "one two": "one two", "one two 3": "one two\u00a03", "하나 둘 재발": "하나 둘\u00a0재발", "하나 둘 세어본다": "하나 둘 세어본다"} {
		if tie(in) != want {
			t.Errorf("tie(%q) = %q", in, tie(in))
		}
	}
	for in, want := range map[float64]string{1: "1", 1.5: "1.5", 0.126: "0.13", -0.001: "0", 10: "10", 2.50: "2.5"} {
		if got := num(in); got != want {
			t.Errorf("num(%v) = %s, want %s", in, got, want)
		}
	}
	if bind("DDR5 RDIMM 64 GB 2Rx4") != "DDR5 RDIMM 64\u00a0GB 2Rx4" {
		t.Error("bind")
	}
	used := map[string]bool{}
	if a, b := anchor("CPU0 C1/a", "", used), anchor("CPU0_C1_a", "", used); a != "dimm-CPU0_C1_a" || b != "dimm-CPU0_C1_a-2" {
		t.Errorf("anchors %s, %s", a, b)
	}
}

func TestFigureHelpers(t *testing.T) {
	for in, want := range map[float64]float64{0: 4, 3: 4, 4: 4, 5: 5, 7: 10, 73: 100, 100: 100, 101: 200, 350: 400, 450: 500, 0.5: 4} {
		if got := niceTop(in); got != want {
			t.Errorf("niceTop(%v) = %v, want %v", in, got, want)
		}
	}
	for in, want := range map[float64]string{0: "0%", 0.001: "<1%", 0.5: "50%", 0.999: "100%", 1.2: "100%"} {
		if got := percent(in); got != want {
			t.Errorf("percent(%v) = %s, want %s", in, got, want)
		}
	}
	if heatLevel(0, view.MapScale) != 0 || heatLevel(2, view.MapScale) != 1 || heatLevel(9, view.MapScale) != 2 || heatLevel(99, view.MapScale) != 3 || heatLevel(1e6, view.MapScale) != 4 {
		t.Error("heat levels")
	}
	if scaleText(view.MapScale, 0) != "1-2" || scaleText(view.MapScale, 3) != "100+" {
		t.Error("scale texts")
	}
	// Hangul is as wide as it is tall; the estimate errs on the wide side.
	if textWidth("슬롯", 10) != 20 || textWidth("ab", 10) < 10 || monoWidth("0x1a2f", 10) < 36 {
		t.Error("text widths")
	}
}

// TestEveryFixtureInput builds a report from every input the adapters have
// a fixture for: kernel logs with and without dates, rasdaemon databases
// of several versions, BMC event logs, Redfish dumps, nvidia-smi output
// and FMFM logs. The ledgers these give are not the tidy ones of the
// synthetic scenarios: errors without a DIMM, slots without a board
// position, GPUs, records dated by seconds since boot. A report must hold
// for each.
func TestEveryFixtureInput(t *testing.T) {
	root := testutil.Testdata(t)
	var inputs []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		switch {
		case rel == "redfish/bmc-tree":
			inputs = append(inputs, p)
			return filepath.SkipDir
		case d.IsDir() && (rel == "edac-sysfs" || rel == "dcbrain" || rel == "real" || strings.HasPrefix(rel, "smbios/tables-")):
			return filepath.SkipDir
		case d.IsDir(), strings.HasSuffix(rel, ".hex"), strings.HasSuffix(rel, ".md"):
			return nil
		case strings.HasSuffix(rel, ".sql"):
			p = testutil.BuildSQLite(t, p, strings.TrimSuffix(filepath.Base(p), ".sql")+".db")
		}
		inputs = append(inputs, p)
		return nil
	})
	if err != nil || len(inputs) < 30 {
		t.Fatalf("%d inputs, %v", len(inputs), err)
	}
	reports := 0
	for _, in := range inputs {
		name := filepath.Base(in)
		as, _, err := scan.Detect(ctx, in, scan.DetectOptions{Now: now, TempDir: t.TempDir()})
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		s, err := store.Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), store.Create)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ingest.Run(ctx, s, as, ingest.Options{Kind: model.ScanImport, Now: now, Host: "fixture-host"}); err != nil {
			t.Errorf("%s: %v", name, err)
			s.Close()
			continue
		}
		for _, lang := range []i18n.Lang{i18n.EN, i18n.KO} {
			c, err := view.Open(ctx, s, lang, engine.Options{Now: now})
			if err != nil {
				t.Errorf("%s: %v", name, err)
				continue
			}
			for _, h := range c.Result.Hosts {
				opts := options(lang, FA)
				opts.AllFigures = true
				doc, err := Build(ctx, c, h, opts)
				if err != nil {
					t.Errorf("%s, host %s: %v", name, h.Host, err)
					continue
				}
				raw, root := render(t, doc)
				for _, bad := range checkHTML(root, raw) {
					t.Errorf("%s, host %s, %s: %s", name, h.Host, lang, bad)
				}
				// A sentence that lost an argument shows a question mark
				// in its place, and a text that lost its code shows the code.
				text := root.content()
				for _, code := range append(Words.Codes(), "card.", "tl.", "map.", "ov.") {
					if strings.Contains(text, " "+code+" ") && strings.Contains(code, ".") && len(code) > 6 {
						t.Errorf("%s: the report prints the code %q", name, code)
					}
				}
				reports++
			}
		}
		s.Close()
	}
	if reports < 60 {
		t.Errorf("%d reports built", reports)
	}
}

// copyLedger returns a private, writable copy of the ledger of a
// built-in scenario.
func copyLedger(t testing.TB, scenario string) string {
	t.Helper()
	data, err := os.ReadFile(testledger.Path(t, scenario, 1, ""))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), scenario+".db")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestLongHistory checks a case that covers years and holds more repairs
// than a report lists: the timeline shares a column among several days,
// and every list that is cut says so.
func TestLongHistory(t *testing.T) {
	// A bank fault touches many pages, so many of them can be taken
	// offline.
	ds := testledger.Generate(t, "bank", 1, func(sc *synth.Scenario) {
		sc.Start = time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
		sc.Reboots = []synth.Reboot{{At: time.Date(2025, 3, 1, 3, 0, 0, 0, time.UTC)}, {At: time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC)}}
		sc.Faults[0].Start = time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC)
		sc.Faults[0].Events = 3000
		sc.Offlines = []synth.Offline{{At: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC), Fault: 0, Pages: 150}}
	})
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "long.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := ingest.Run(ctx, s, []adapter.Adapter{ds.Adapter()}, ingest.Options{Kind: model.ScanSynthetic, Now: ds.Scenario.End}); err != nil {
		t.Fatal(err)
	}
	doc := buildFrom(t, s, options(i18n.EN, Customer))
	raw, root := render(t, doc)
	for _, bad := range checkHTML(root, raw) {
		t.Error(bad)
	}
	d := doc.DIMMs[0]
	if d.Timeline.Days < 800 || d.ActionCount < 100 || len(d.Actions) != MaxActions {
		t.Fatalf("%d days, %d actions, %d listed", d.Timeline.Days, d.ActionCount, len(d.Actions))
	}
	tl := root.find("svg.tline")
	text := tl[len(tl)-1].content()
	for _, want := range []string{"one column = 3 days", "2025-01", "2025-07", "2026-01", "2024-05-30", "2026-11-12", "reboot 2025-03-01 2026-10-01", "page offline 2026-11-02"} {
		if !strings.Contains(text, want) {
			t.Errorf("the timeline of %d days lacks %q: %s", d.Timeline.Days, want, text)
		}
	}
	// A column stands for several days and says which.
	var title string
	tl[len(tl)-1].walk(func(e *elem) {
		if e.tag == "title" && strings.Contains(e.content0(), " to ") && title == "" && e.parent.tag == "rect" {
			title = e.content0()
		}
	})
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2} to \d{4}-\d{2}-\d{2}: [\d,]+ corrected errors?$`).MatchString(title) {
		t.Errorf("a column of several days says %q", title)
	}
	// No column is narrower than two pixels.
	for _, r := range tl[len(tl)-1].find("g.bars")[0].kids {
		if w := r.attrs["width"]; w < "2" {
			t.Errorf("a column %s pixels wide", w)
			break
		}
	}
	all := root.content()
	for _, want := range []string{
		fmt.Sprintf("The latest %d of %d steps are listed.", MaxActions, d.ActionCount),
		fmt.Sprintf("page offline: the latest %d of %d lead to their records", view.MaxMarks, d.ActionCount),
	} {
		if !strings.Contains(all, want) {
			t.Errorf("the report does not say %q", want)
		}
	}
}

// TestOtherStates checks what a report says about states the synthetic
// scenarios do not have: a DIMM that was taken out, sources that could
// not be read, thresholds that were changed, and a case from a bundle.
func TestOtherStates(t *testing.T) {
	path := copyLedger(t, "demo")
	s, err := store.Open(ctx, path, store.ReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	// Another DIMM is seen in the slot of the failing one: that one was
	// replaced.
	uid := model.DIMMUID("Example Memory", "EXM64G48R2X4-A", "NEW00001")
	err = s.Update(ctx, func(w *store.Writer) error {
		if err := w.UpsertDIMM(&model.DIMM{UID: uid, Manufacturer: "Example Memory", PartNumber: "EXM64G48R2X4-A", Serial: "NEW00001"}); err != nil {
			return err
		}
		return w.ObservePlacement(uid, "r12-n034", "CPU0_C1", time.Date(2026, 11, 13, 6, 0, 0, 0, time.UTC), "test")
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// The scan ran as a normal user and could not read a source.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`UPDATE scan SET kind = 'live', privileged = 0`,
		`INSERT INTO source (scan_id, kind, origin, status, detail) SELECT id, 'smbios', '/sys/firmware/dmi/tables', 'unreadable', 'permission denied; the SMBIOS tables are readable as root' FROM scan`,
		`INSERT INTO source (scan_id, kind, origin, status, detail) SELECT id, 'rasdaemon', '/var/lib/rasdaemon/ras-mc_event.db', 'absent', 'not present' FROM scan`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	db.Close()
	s, err = store.Open(ctx, path, store.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	cfg := engine.DefaultConfig()
	if err := cfg.Set("rule.row.min_errors", "12"); err != nil {
		t.Fatal(err)
	}
	c, err := view.Open(ctx, s, i18n.EN, engine.Options{Now: now, Config: &cfg})
	if err != nil {
		t.Fatal(err)
	}
	h, _ := c.Host("")
	opts := options(i18n.EN, Customer)
	opts.Case = &CaseInfo{ID: "abcdef012345", Bundle: true, Created: now.Add(-time.Hour), Tool: "my 0.9.0 (0123abcd)", Anonymized: true}
	doc, err := Build(ctx, c, h, opts)
	if err != nil {
		t.Fatal(err)
	}
	raw, root := render(t, doc)
	for _, bad := range checkHTML(root, raw) {
		t.Error(bad)
	}
	text := root.content()
	for _, want := range []string{
		// The DIMM that was taken out keeps its finding and says where it is.
		"CPU0_C1 S/N 3B9C56: replace now", "The DIMM is no longer in the host.", "replace now (no longer in the host)",
		"in this slot since 2026-07-31; 0 slot changes; no longer in the host",
		// The case.
		"Case ID abcdef012345 the first 12 digits of the SHA-256 checksum of the ledger in the bundle",
		"Case bundle written 2026-11-13 08:30 UTC by my 0.9.0 (0123abcd); host names and network addresses were replaced before it was sent",
		"Collected scan of the host without root privileges, 2026-11-13 00:00 UTC",
		"Engine version " + engine.Version + "; thresholds changed from the defaults: rule.row.min_errors=12",
		// What was not read.
		"smbios: not readable (permission denied; the SMBIOS tables are readable as root)",
		"rasdaemon: not present",
		"This scan ran without root privileges. As root it also reads the SMBIOS tables (slot names and DIMM serial numbers).",
		"not readable (permission denied; the SMBIOS tables are readable as root)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the report lacks %q", want)
		}
	}
	// The slot shows the DIMM that is in it now, and does not lead to the
	// finding of the one that was taken out.
	var cell *elem
	root.find("svg.slotmap")[0].walk(func(e *elem) {
		if e.hasClass("slot") && strings.HasPrefix(e.find("title")[0].content0(), "CPU0_C1:") {
			cell = e
		}
	})
	if cell == nil || cell.tag != "g" || !cell.hasClass("g-ok") || !strings.Contains(cell.find("title")[0].content0(), "NEW00001") {
		t.Errorf("the slot of the replaced DIMM: %+v", cell)
	}
}
