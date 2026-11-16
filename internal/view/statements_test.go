package view

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
)

// wordPrefixes are the codes of the engine catalog that are words inside
// a statement (grades, units, names of fault modes and sources, the
// conditions of rules) or errors of the thresholds, not statements.
var wordPrefixes = []string{"grade.", "label.", "confidence.", "config.", "word.", "status.", "unit.", "mode.", "kind.", "name.", "rule."}

// StatementCodes lists every statement the engine can make: the codes of
// its catalog that are not words.
func statementCodes() []string {
	var out []string
	for _, code := range engine.Messages.Codes() {
		word := false
		for _, p := range wordPrefixes {
			word = word || strings.HasPrefix(code, p)
		}
		if !word {
			out = append(out, code)
		}
	}
	sort.Strings(out)
	return out
}

// TestEveryStatementReachesTheCard: whatever statement the engine puts in
// a finding, in any of its parts, the card carries it with its code, and
// --json and --detail draw the card. The short screens leave statements
// out; this is where they stay.
func TestEveryStatementReachesTheCard(t *testing.T) {
	codes := statementCodes()
	if len(codes) < 100 {
		t.Fatalf("%d statement codes: the list of word prefixes takes too much", len(codes))
	}
	v := &model.Verdict{Grade: model.VerdictReplaceNow, Rules: []string{"R-UE-01"}, NextAction: model.Statement{Code: codes[0]}}
	for _, code := range codes {
		st := model.Statement{Code: code}
		v.Evidence = append(v.Evidence, model.Evidence{Rule: "R-UE-01", Statement: st})
		v.CounterEvidence = append(v.CounterEvidence, model.Check{Effect: "open", Statement: st})
		v.Missing = append(v.Missing, st)
		v.ConfidenceFactors = append(v.ConfidenceFactors, model.Factor{Delta: 1, Statement: st})
	}
	for _, lang := range []i18n.Lang{i18n.EN, i18n.KO} {
		c := &Case{Lang: lang}
		card := &Card{}
		c.finding(card, v)
		if len(card.Evidence) != len(codes) || len(card.Checks) != len(codes) || len(card.Missing) != len(codes) || len(card.Factors) != len(codes) || card.Next == nil {
			t.Fatalf("%s: the card holds %d, %d, %d, %d statements of %d", lang, len(card.Evidence), len(card.Checks), len(card.Missing), len(card.Factors), len(codes))
		}
		b, err := json.Marshal(card)
		if err != nil {
			t.Fatal(err)
		}
		for _, code := range codes {
			if n := strings.Count(string(b), `"code":"`+code+`"`); n < 4 {
				t.Errorf("%s: %s is %d times in the JSON of the card, want 4", lang, code, n)
			}
		}
		for i := range card.Evidence {
			if card.Evidence[i].Text.Text == "" || card.Evidence[i].Text.Text == codes[i] {
				t.Errorf("%s: %s has no text", lang, codes[i])
			}
		}
	}
}
