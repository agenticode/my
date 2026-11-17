package report

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/explorer"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/scan"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/testutil"
	"github.com/agenticode/my/internal/view"
)

// populationReport imports one dmidecode text and builds its report for
// an audience in both languages, each checked for validity.
func populationReport(t *testing.T, fixture string, aud Audience) ([]*elem, []*Document) {
	t.Helper()
	in := testutil.Testdata(t, "explorer", fixture, "dmidecode.txt")
	as, _, err := scan.Detect(ctx, in, scan.DetectOptions{Now: now, TempDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := ingest.Run(ctx, s, as, ingest.Options{Kind: model.ScanImport, Now: now, Host: "fixture-host"}); err != nil {
		t.Fatal(err)
	}
	var roots []*elem
	var docs []*Document
	for _, lang := range []i18n.Lang{i18n.EN, i18n.KO} {
		c, err := view.Open(ctx, s, lang, engine.Options{Now: now})
		if err != nil {
			t.Fatal(err)
		}
		doc, err := Build(ctx, c, c.Result.Hosts[0], options(lang, aud))
		if err != nil {
			t.Fatal(err)
		}
		raw, root := render(t, doc)
		for _, bad := range checkHTML(root, raw) {
			t.Errorf("%s %s: %s", fixture, lang, bad)
		}
		roots = append(roots, root)
		docs = append(docs, doc)
	}
	return roots, docs
}

// pick is e.find with "a > b" for the children b of the elements a.
func pick(e *elem, sel string) []*elem {
	outer, inner, ok := strings.Cut(sel, " > ")
	if !ok {
		return e.find(sel)
	}
	tag, class, _ := strings.Cut(inner, ".")
	var out []*elem
	for _, x := range e.find(outer) {
		for _, k := range x.kids {
			if k.tag == tag && (class == "" || k.hasClass(class)) {
				out = append(out, k)
			}
		}
	}
	return out
}

// texts are the text of the elements below e that match a selector.
func texts(e *elem, sel string) []string {
	var out []string
	for _, x := range pick(e, sel) {
		out = append(out, x.content())
	}
	return out
}

// The marks, their legend line and the block under the board, on boards
// whose modules stand elsewhere than the table has them, as it has them,
// without a rule set and not placed in full.
func TestPopulationInTheReport(t *testing.T) {
	for _, c := range []struct {
		fixture      string
		plus, minus  int
		block, lines int
		quality      string // a word of the line of data quality, for no rule
	}{
		// Intel M50CYP2SB: four modules per processor in A1, B1, E1, F1
		// where the table fills A1, C1, E1, G1.
		{"linuxhw-m50cyp2sbstd-d50fc4", 4, 4, 1, 2, ""},
		// Supermicro X10DRW: A1 and C1 where the table fills A1 and B1.
		{"linuxhw-sys-6028r-wtr-fac06e", 2, 2, 1, 2, ""},
		// HP BL460c Gen9: the modules stand as the table has them.
		{"netbox-hp-bl460c-gen9", 0, 0, 1, 1, ""},
		// Supermicro SYS-6018R: no rule set covers the board.
		{"netbox-sm-sys-6018r", 0, 0, 0, 0, "6018R"},
		// Supermicro SSG-6028R: the board is not placed in full.
		{"netbox-sm-ssg-6028r", 0, 0, 0, 0, "6028R"},
	} {
		roots, docs := populationReport(t, c.fixture, Customer)
		for i, root := range roots {
			lang := []string{"en", "ko"}[i]
			name := c.fixture + " " + lang
			plus, minus := 0, 0
			for _, b := range root.find("svg.board") {
				plus, minus = plus+len(b.find("path.mb-mark-fill")), minus+len(b.find("path.mb-mark-leave"))
			}
			if plus != c.plus || minus != c.minus {
				t.Errorf("%s: %d plus and %d minus marks, want %d and %d", name, plus, minus, c.plus, c.minus)
			}
			legends := root.find("ul.mark-legend")
			if (plus+minus > 0) != (len(legends) == 1) || len(legends) > 1 {
				t.Errorf("%s: %d legend lines of the marks for %d marks", name, len(legends), plus+minus)
			}
			if len(legends) == 1 && len(legends[0].find("li")) != 2 {
				t.Errorf("%s: the legend of the marks has %d items", name, len(legends[0].find("li")))
			}
			blocks := root.find("div.population")
			if len(blocks) != c.block {
				t.Fatalf("%s: %d population blocks, want %d", name, len(blocks), c.block)
			}
			if c.block == 1 {
				lines := pick(blocks[0], "ul.pop-lines > li")
				if len(lines) != c.lines {
					t.Errorf("%s: %d lines in the block, want %d", name, len(lines), c.lines)
				}
				title := texts(blocks[0], "h3")
				want := map[string]string{"en": "Population", "ko": "장착 구성"}[lang]
				if len(title) != 1 || title[0] != want {
					t.Errorf("%s: the block is titled %v", name, title)
				}
				// Default: no modules, no table, no rules not checked.
				if n := len(blocks[0].find("ul.pop-modules")) + len(blocks[0].find("table.pop-table")) + len(blocks[0].find("ul.pop-unchecked")); n != 0 {
					t.Errorf("%s: the default report holds %d parts of the detail", name, n)
				}
			}
			// Every finding line names a document of sources.tsv.
			p := docs[i].Board.Population
			for _, f := range p.Findings {
				if _, ok := explorer.SourceByID(f.Source.ID); !ok || f.Source.Cite == "" {
					t.Errorf("%s: %s cites %q", name, f.Rule, f.Source.ID)
				}
				found := false
				for _, l := range texts(root, "ul.pop-lines > li") {
					found = found || strings.Contains(l, f.Source.Cite) && strings.Contains(l, f.Rule)
				}
				if !found {
					t.Errorf("%s: no line names %s with %q", name, f.Rule, f.Source.Cite)
				}
			}
			dq := strings.Join(texts(root, "ul.dq-board > li"), "\n")
			if c.quality != "" {
				if p == nil || p.Text == nil || !strings.Contains(dq, p.Text.Text) || strings.Count(dq, p.Text.Text) != 1 {
					t.Errorf("%s: the data quality lines lack the line of no rule:\n%s", name, dq)
				}
			} else if p != nil && p.Text != nil {
				t.Errorf("%s: a line of no rule on a board with a rule set", name)
			}
		}
	}
}

// --audience fa adds the modules each finding names, the vendor's table of
// every processor and the rules not checked.
func TestPopulationDetailForFA(t *testing.T) {
	roots, docs := populationReport(t, "linuxhw-sys-6028r-wtr-fac06e", FA)
	for i, root := range roots {
		blocks := root.find("div.population")
		if len(blocks) != 1 {
			t.Fatalf("%d blocks", len(blocks))
		}
		b := blocks[0]
		if n := len(b.find("ul.pop-modules")); n != len(docs[i].Board.Population.Findings) {
			t.Errorf("%d lists of modules for %d findings", n, len(docs[i].Board.Population.Findings))
		}
		if n := len(b.find("table.pop-table")); n != 2 {
			t.Errorf("%d tables of the vendor, want one per processor", n)
		}
		if n := len(pick(b, "ul.pop-unchecked > li")); n != len(docs[i].Board.Population.Unchecked) || n == 0 {
			t.Errorf("%d rules not checked", n)
		}
		for _, m := range texts(b, "ul.pop-modules > li") {
			if !strings.Contains(m, ": ") || !strings.Contains(m, "GB") {
				t.Errorf("module line %q", m)
			}
		}
	}
}

// The marks add no text to the board, and the text of the block is no
// smaller than the text of the board (9.5 px at the width of a figure).
func TestPopulationTextSize(t *testing.T) {
	roots, _ := populationReport(t, "linuxhw-m50cyp2sbstd-d50fc4", FA)
	for _, root := range roots {
		for _, b := range root.find("svg.board") {
			for _, m := range append(b.find("path.mb-mark-fill"), b.find("path.mb-mark-leave")...) {
				for _, k := range m.kids {
					if k.tag != "title" {
						t.Errorf("a mark holds <%s>", k.tag)
					}
				}
			}
		}
	}
	re := regexp.MustCompile(`(?m)^\.(pop-[a-z-]+|mark-legend[^{]*)\s*\{[^}]*font-size: ([0-9.]+)rem`)
	rules := re.FindAllStringSubmatch(styleSheet, -1)
	if len(rules) < 2 {
		t.Fatalf("found %d rules of the block with a font size", len(rules))
	}
	for _, m := range rules {
		if px, _ := strconv.ParseFloat(m[2], 64); px*16 < 9.5 {
			t.Errorf(".%s: font-size %srem is below 9.5 px", m[1], m[2])
		}
	}
}
