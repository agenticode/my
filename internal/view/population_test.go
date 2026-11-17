package view

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/population"
	"github.com/agenticode/my/internal/testutil"
)

// A rule set written by hand for the tests, for the board family of the
// HPE ProLiant DL360 Gen9: the order A to L of each processor and two
// rules. The rows of internal/population/sets.tsv are checked there.
const viewSets = `id	maker	match	when	first	order	strength	alt	rules	source
made-hp-gen9	hp	.	family=hp-gen9-24slot	0	seq: 0.0 1.0 2.0 3.0 0.1 1.1 2.1 3.1 0.2 1.2 2.2 3.2	req	-	P-BAL-01=rec hpe-767927-009:48; P-MIX-01=req hpe-767927-009:41,47	hpe-767927-009:48 hpe-768830-401a:79
`

// withSets runs the population check of the views with the rule sets of
// text while the test runs.
func withSets(t *testing.T, text string) {
	t.Helper()
	sets, err := population.ParseSets(text)
	if err != nil {
		t.Fatal(err)
	}
	old := populationCheck
	populationCheck = func(b population.Board) population.Result { return population.CheckWith(b, sets) }
	t.Cleanup(func() { populationCheck = old })
}

// bare is a slot name of populationBoard without the number it appends.
func bare(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i], _, _ = strings.Cut(n, "#")
	}
	return out
}

// marks are the marks of the sticks of a board, by slot.
func marks(b *Board) map[string]string {
	out := map[string]string{}
	for _, r := range b.Rows {
		for _, p := range r.Parts {
			for _, ch := range p.Channels {
				for _, s := range ch.Sticks {
					if s.Mark != "" {
						out[bare([]string{s.Slot})[0]] = s.Mark
					}
				}
			}
		}
	}
	return out
}

// The demo dump of the collection: one module per processor in DIMM 1,
// where the table fills DIMM 12 first.
func TestPopulationOfTheBoard(t *testing.T) {
	withSets(t, viewSets)
	for _, lang := range []i18n.Lang{i18n.EN, i18n.KO} {
		b := populationBoard(t, lang, "1579B6FEB060")
		p := b.Population
		if p == nil || p.Set != "made-hp-gen9" || p.NoRule != "" || len(p.Findings) != 2 {
			t.Fatalf("%s: population %+v", lang, p)
		}
		for i, f := range p.Findings {
			proc := []string{"PROC 1", "PROC 2"}[i]
			if f.Rule != population.Order || f.Socket != i || !reflect.DeepEqual(bare(f.Fill), []string{proc + " DIMM 12"}) ||
				!reflect.DeepEqual(bare(f.Leave), []string{proc + " DIMM 1"}) || f.Strength != population.Required {
				t.Errorf("%s: finding %+v", lang, f)
			}
			if f.Source.ID != "hpe-767927-009" || f.Source.Pages != "48" || !strings.HasPrefix(f.Source.Cite, "HPE ProLiant DL360 Gen9 Server User Guide, 767927-009") ||
				!strings.HasSuffix(f.Source.Cite, ", p. 48") {
				t.Errorf("%s: source %+v", lang, f.Source)
			}
			if f.Text.Text == "" || strings.Contains(f.Text.Text, "{") || strings.Contains(f.Text.Text, "?") || f.StrengthText.Text == "" {
				t.Errorf("%s: text %q, strength %q", lang, f.Text.Text, f.StrengthText.Text)
			}
		}
		if lang == i18n.EN {
			if got := p.Findings[0].Text.Text; got != "CPU 0: 1 module stands in PROC 1 DIMM 1#0. For 1 module the table fills PROC 1 DIMM 12#11." {
				t.Errorf("text %q", got)
			}
		}
		want := map[string]string{"PROC 1 DIMM 12": "+", "PROC 1 DIMM 1": "-", "PROC 2 DIMM 12": "+", "PROC 2 DIMM 1": "-"}
		if got := marks(b); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: marks %v", lang, got)
		}
	}
	// A board whose modules stand as the table has them: one line, no mark.
	b := populationBoard(t, i18n.EN, "062C3461F315")
	if p := b.Population; p == nil || len(p.Findings) != 0 || len(marks(b)) != 0 {
		t.Errorf("062C3461F315: %+v", p)
	} else {
		for _, n := range p.Notes {
			if n.Text.Text == "" || strings.Contains(n.Text.Text, "{") {
				t.Errorf("note %+v", n)
			}
		}
	}
}

// The two reasons for no rule, each said in one line.
func TestPopulationNoRule(t *testing.T) {
	withSets(t, viewSets)
	for _, lang := range []i18n.Lang{i18n.EN, i18n.KO} {
		// A Dell PowerEdge R340, placed in full: no rule set of the test.
		b := populationBoard(t, lang, "0013715F7C60")
		if p := b.Population; p == nil || p.NoRule != population.NoSet || p.Text == nil || !strings.Contains(p.Text.Text, "PowerEdge R340") || len(marks(b)) != 0 {
			t.Errorf("%s: no set %+v", lang, p)
		}
		// A Supermicro X8DTH with modules on an absent processor: not
		// placed in full.
		b = populationBoard(t, lang, "35A3317203E1")
		if p := b.Population; p == nil || p.NoRule != population.NotFull || p.Text == nil || len(marks(b)) != 0 {
			t.Errorf("%s: not full %+v", lang, p)
		}
		// A Dell PowerEdge R910: slots below the board.
		b = populationBoard(t, lang, "10A6C2EC2170")
		if p := b.Population; p == nil || p.NoRule != population.NotFull || p.Text == nil || p.Text.Code != "pop.not_full" || strings.Contains(p.Text.Text, "{") {
			t.Errorf("%s: not placed %+v", lang, p)
		}
	}
}

// A fault of the check leaves the board as it was, without marks and with
// one line of data quality.
func TestPopulationFault(t *testing.T) {
	withSets(t, viewSets)
	want := populationBoard(t, i18n.EN, "1579B6FEB060")
	populationCheck = func(population.Board) population.Result { panic("made-up fault") }
	got := populationBoard(t, i18n.EN, "1579B6FEB060")
	if got.Population != nil || len(marks(got)) != 0 {
		t.Fatalf("population %+v marks %v", got.Population, marks(got))
	}
	if n := len(got.Lines); n != len(want.Lines)+1 || got.Lines[n-1].Code != "pop.fault" {
		t.Fatalf("lines %+v", got.Lines)
	}
	got.Lines = got.Lines[:len(got.Lines)-1]
	want.Population = nil
	for ri := range want.Rows {
		for pi := range want.Rows[ri].Parts {
			for ci := range want.Rows[ri].Parts[pi].Channels {
				for si := range want.Rows[ri].Parts[pi].Channels[ci].Sticks {
					want.Rows[ri].Parts[pi].Channels[ci].Sticks[si].Mark = ""
				}
			}
		}
	}
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(want)
	if string(a) != string(b) {
		t.Errorf("the fault changed the board:\n%s\n%s", a, b)
	}
}

// Every texts of the population reads in both languages: a made-up result
// with every kind of finding, note and rule not checked.
func TestPopulationTexts(t *testing.T) {
	ref := []population.Ref{{ID: "hpe-767927-009", Pages: "48"}}
	r := population.Result{Set: "x", Ordered: true, Strength: population.Required, Source: ref,
		Processors: []population.Processor{{Socket: 0, Modules: 1, Installed: []string{"A2"}, Recommended: []string{"A1"}}},
		Findings: []population.Finding{
			{Rule: population.Order, Strength: population.Required, Socket: 0, Fill: []string{"A1"}, Leave: []string{"A2"}, Slots: []string{"A1", "A2"}, Source: ref},
			{Rule: population.First, Strength: population.Recommended, Socket: 0, Channel: "A", Slots: []string{"A2", "A1"}, First: "A1", Source: ref},
			{Rule: population.CPU, Strength: population.Required, Socket: 1, Source: ref},
			{Rule: population.Balance, Strength: population.Recommended, Socket: -1, Counts: []int{1, 0}, Source: ref},
			{Rule: population.MixType, Strength: population.Required, Socket: -1, Types: []string{"RDIMM", "LRDIMM"}, Source: ref},
			{Rule: population.MixRank, Strength: population.Required, Socket: 0, Channel: "A", Slots: []string{"A1", "A2"}, First: "A1", Ranks: []int{1, 2}, Source: ref},
			{Rule: population.MixSize, Strength: population.Required, Socket: 0, Channel: "A", Slots: []string{"A1", "A2"}, First: "A1", Bytes: []int64{8 << 30, 16 << 30}, Source: ref},
			{Rule: population.MixSpeed, Strength: population.Recommended, Socket: -1, Speeds: []int{2133, 2400}, Source: ref},
			{Rule: population.MixPart, Strength: population.Required, Socket: 0, Channel: "A", Slots: []string{"A1", "A2"}, Parts: 2, Source: ref},
		},
		Notes: []population.Note{
			{Kind: population.Agrees, Socket: -1, Modules: 2, Source: ref},
			{Kind: population.Holds, Socket: -1, Modules: 1, Source: ref},
			{Kind: population.NotCompared, Socket: 0, Modules: 4, Counts: []int{3, 6}},
			{Kind: population.NotCompared, Socket: 0, Modules: 13, Counts: []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}},
			{Kind: population.OtherMode, Socket: 0, Modules: 2, Mode: "advanced-ecc", Source: ref},
			{Kind: population.SecondSet, Socket: 0, Modules: 1, Source: ref},
		},
		Unchecked: []population.Unchecked{
			{Rule: population.MixType, Field: population.FieldType, N: 2},
			{Rule: population.MixRank, Field: population.FieldRanks, N: 1},
			{Rule: population.MixSize, Field: population.FieldSize, N: 3},
			{Rule: population.MixSpeed, Field: population.FieldSpeed, N: 2},
			{Rule: population.MixPart, Field: population.FieldPart, N: 2},
			{Rule: population.CPU, Field: population.FieldStatus, N: 1},
			{Rule: population.Order, Field: population.FieldPlaces, N: 2},
			{Rule: population.Order, Field: population.FieldMode, N: 1, Mode: "mirroring"},
		},
	}
	for _, lang := range []i18n.Lang{i18n.EN, i18n.KO} {
		p := populationView(lang, r, boardHost{product: "ProLiant DL360 Gen9"})
		var texts []Text
		for _, f := range p.Findings {
			texts = append(texts, f.Text, f.StrengthText)
			if f.Text.Code == "pop.rule" {
				t.Errorf("%s: %s has no sentence", lang, f.Rule)
			}
		}
		for _, n := range p.Notes {
			texts = append(texts, n.Text)
		}
		for _, u := range p.Unchecked {
			texts = append(texts, u.Text)
		}
		for _, no := range []population.Result{{NoRule: population.NoSet}, {NoRule: population.NotFull, NotPlaced: 3}, {NoRule: population.NotFull}} {
			texts = append(texts, *populationView(lang, no, boardHost{vendor: "HPE", product: "ProLiant DL360 Gen9", board: "ProLiant DL360 Gen9"}).Text)
		}
		for _, tx := range texts {
			if tx.Text == "" || strings.ContainsAny(tx.Text, "{}") || strings.Contains(tx.Text, "?") || strings.Contains(tx.Text, "@pop") {
				t.Errorf("%s: %s reads %q", lang, tx.Code, tx.Text)
			}
		}
		if lang == i18n.EN {
			for _, w := range []string{"1 to 12 modules", "3 and 6 modules", "RDIMM and LRDIMM", "2133 MT/s and 2400 MT/s", "8 GB", "1 rank and"} {
				found := false
				for _, tx := range texts {
					found = found || strings.Contains(tx.Text, w)
				}
				if !found {
					t.Errorf("no text says %q", w)
				}
			}
		}
	}
}

// TestPopulationFieldsInTheDocs: every field of the view population and
// the mark of a stick are named in docs/data-model.md.
func TestPopulationFieldsInTheDocs(t *testing.T) {
	page, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "docs", "data-model.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(page)
	start := strings.Index(doc, "### The view `population`")
	if start < 0 {
		t.Fatal("docs/data-model.md has no section on the view population")
	}
	section := doc[start:]
	if end := strings.Index(section[4:], "\n### "); end >= 0 {
		section = section[:end+4]
	}
	keys := map[string]bool{}
	var walk func(t reflect.Type)
	walk = func(ty reflect.Type) {
		for ty.Kind() == reflect.Pointer || ty.Kind() == reflect.Slice {
			ty = ty.Elem()
		}
		if ty.Kind() != reflect.Struct || ty == reflect.TypeOf(Text{}) {
			return
		}
		for i := 0; i < ty.NumField(); i++ {
			f := ty.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "" || name == "-" {
				continue
			}
			keys[name] = true
			walk(f.Type)
		}
	}
	walk(reflect.TypeOf(Population{}))
	var names []string
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		if !regexp.MustCompile("`" + regexp.QuoteMeta(k) + "(?:\\[\\])?`").MatchString(section) {
			t.Errorf("docs/data-model.md does not name the field %q of the view population", k)
		}
	}
	board := doc[strings.Index(doc, "### The view `board`"):]
	if !strings.Contains(board[:strings.Index(board, "### The view `population`")], "`mark`") {
		t.Error("docs/data-model.md does not name the mark of a stick")
	}
}
