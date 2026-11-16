package term

import (
	"fmt"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/view"
)

// TestDetailDrawsEveryStatement: the card with --detail draws every
// statement of every part, and the short card shows a few of them and
// counts the rest: shown and counted add up to what the card holds.
func TestDetailDrawsEveryStatement(t *testing.T) {
	var tokens []string
	text := func(part string, i int) view.Text {
		tok := fmt.Sprintf("%s%02d", part, i)
		tokens = append(tokens, tok)
		return view.Text{Code: "x." + tok, Text: tok}
	}
	card := &view.Card{Host: "h", Slot: "A1", Grade: model.VerdictReplaceNow, GradeText: "replace now", Label: "finding",
		ConfidenceText: "high", Rules: []string{"R-UE-01", "R-ROW-03"}, Present: true}
	for i := 0; i < 6; i++ {
		rule := "R-UE-01"
		if i%2 == 1 {
			rule = "R-ROW-03"
		}
		card.Evidence = append(card.Evidence, view.Evidence{Rule: rule, Text: text("ev", i)})
	}
	for i, effect := range []string{"open", "supports", "weakens", "open", "supports"} {
		card.Checks = append(card.Checks, view.Check{Effect: effect, EffectText: effect, Text: text("ck", i)})
	}
	for i := 0; i < 3; i++ {
		card.Missing = append(card.Missing, text("ms", i))
		card.Baseline = append(card.Baseline, text("bl", i))
		card.Factors = append(card.Factors, view.Factor{Delta: i, Text: text("cf", i)})
	}
	next := text("nx", 0)
	card.Next = &next

	for _, lang := range []i18n.Lang{i18n.EN, i18n.KO} {
		var b strings.Builder
		if err := Why(&b, []*view.Card{card}, Style{Lang: lang, Detail: true}); err != nil {
			t.Fatal(err)
		}
		for _, tok := range tokens {
			if !strings.Contains(b.String(), tok) {
				t.Errorf("%s: --detail lacks %s:\n%s", lang, tok, b.String())
			}
		}
		b.Reset()
		if err := Why(&b, []*view.Card{card}, Style{Lang: lang}); err != nil {
			t.Fatal(err)
		}
		short := b.String()
		shown := 0
		for _, tok := range tokens {
			if strings.HasPrefix(tok, "ev") || strings.HasPrefix(tok, "ck") {
				if strings.Contains(short, tok) {
					shown++
				}
			}
		}
		brief := card.Brief()
		// The lead, the lines below it, the counted rest and the open
		// checks are all the statements and checks of the card.
		if shown != 1+len(brief.Lines) || 1+len(brief.Lines)+brief.More+brief.Open != len(card.Evidence)+len(card.Checks) ||
			brief.Missing != len(card.Missing) || len(brief.Lines) != view.BriefLines {
			t.Errorf("%s: %d shown, brief %+v\n%s", lang, shown, brief, short)
		}
		// The check that weakens the finding comes first.
		if !strings.Contains(short, "weakens: ck02") || strings.Index(short, "ck02") > strings.Index(short, "ev01") {
			t.Errorf("%s: the weakening check is not first:\n%s", lang, short)
		}
		if !strings.Contains(short, ": --detail") {
			t.Errorf("%s: the short card does not name --detail:\n%s", lang, short)
		}
	}
}
