package engine

import (
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/synth"
)

// TestCatalogIsComplete: every message exists in both languages and uses
// the same arguments in both.
func TestCatalogIsComplete(t *testing.T) {
	for _, code := range Messages.Codes() {
		m := Messages[code]
		if strings.TrimSpace(m.EN) == "" || strings.TrimSpace(m.KO) == "" {
			t.Errorf("%s: a language is missing: %+v", code, m)
			continue
		}
		en, ko := i18n.Placeholders(m.EN), i18n.Placeholders(m.KO)
		sort.Strings(en)
		sort.Strings(ko)
		if !reflect.DeepEqual(en, ko) {
			t.Errorf("%s: arguments differ: en %v, ko %v", code, en, ko)
		}
	}
	for _, r := range Rules {
		if !Messages.Has(r.Condition) || r.Condition != "rule."+r.ID || !r.Grade.Valid() {
			t.Errorf("rule %s: condition %q, grade %q", r.ID, r.Condition, r.Grade)
		}
	}
	for _, g := range []model.VerdictGrade{model.VerdictOK, model.VerdictObserve, model.VerdictConsiderReplacement, model.VerdictReplaceNow, model.VerdictHoldReplacement} {
		if GradeText(i18n.EN, g) == "grade."+string(g) || GradeText(i18n.KO, g) == GradeText(i18n.EN, g) {
			t.Errorf("grade %s: %q / %q", g, GradeText(i18n.EN, g), GradeText(i18n.KO, g))
		}
	}
	for _, m := range append([]model.FaultMode{model.ModeUndetermined}, model.FaultModes...) {
		if ModeText(i18n.EN, m) == "mode."+string(m) || ModeText(i18n.KO, m) == "mode."+string(m) {
			t.Errorf("mode %s has no name", m)
		}
	}
	for _, k := range []model.ActionKind{model.ActionPageOffline, model.ActionSPPR, model.ActionHPPR, model.ActionSparing, model.ActionRowRemap,
		model.ActionReseat, model.ActionReplacement, model.ActionDisable, model.ActionMarkFailed} {
		if !Messages.Has("kind." + string(k)) {
			t.Errorf("action kind %s has no name", k)
		}
	}
	for _, st := range []model.SourceStatus{model.SourcePartial, model.SourceEmpty, model.SourceAbsent, model.SourceUnreadable, model.SourceError} {
		if !Messages.Has("status." + string(st)) {
			t.Errorf("source status %s has no name", st)
		}
	}
	// The grades of the design, in Korean.
	want := map[model.VerdictGrade]string{model.VerdictOK: "정상", model.VerdictObserve: "관찰", model.VerdictConsiderReplacement: "교체 고려",
		model.VerdictReplaceNow: "즉시 교체", model.VerdictHoldReplacement: "교체 보류"}
	for g, ko := range want {
		if got := GradeText(i18n.KO, g); got != ko {
			t.Errorf("grade %s in Korean = %q, want %q", g, got, ko)
		}
	}
	if Messages.Text(i18n.KO, "label.finding", nil) != "소견" || Messages.Text(i18n.EN, "label.finding", nil) != model.VerdictLabel {
		t.Error("a verdict is called a finding")
	}
}

// TestCatalogWording: a grade is a finding. No sentence of the engine calls
// it a recommendation, and none uses the filler the project rules out.
func TestCatalogWording(t *testing.T) {
	banned := []string{
		"recommend", "advis", "must be replaced", "seamless", "robust", "powerful", "leverage", "not just", "—",
		"권고", "권장", "추천", "아니라", "넘어", "매끄럽", "견고", "강력", "최적", "·",
	}
	for _, code := range Messages.Codes() {
		m := Messages[code]
		for _, w := range banned {
			if strings.Contains(strings.ToLower(m.EN), w) || strings.Contains(m.KO, w) {
				t.Errorf("%s contains %q: %q / %q", code, w, m.EN, m.KO)
			}
		}
		// Korean comparison particle after a word ("A보다 B").
		if regexp.MustCompile(`[가-힣A-Za-z0-9)]보다 `).MatchString(m.KO) {
			t.Errorf("%s compares with 보다: %q", code, m.KO)
		}
	}
}

// statements returns every statement of a finding.
func statements(v *model.Verdict) []model.Statement {
	var out []model.Statement
	for _, e := range v.Evidence {
		out = append(out, e.Statement)
	}
	for _, c := range v.CounterEvidence {
		out = append(out, c.Statement)
	}
	out = append(out, v.Missing...)
	if v.NextAction.Code != "" {
		out = append(out, v.NextAction)
	}
	for _, f := range v.ConfidenceFactors {
		out = append(out, f.Statement)
	}
	return out
}

var unfilled = regexp.MustCompile(`[{}@?]`)

// checkStatements renders every statement of a result in both languages
// and fails on a code the catalog does not know and on an argument that
// was not filled in. It returns the codes it saw.
func checkStatements(t *testing.T, name string, res *Result, seen map[string]bool) {
	t.Helper()
	res.verdicts(func(v *model.Verdict) {
		for _, st := range statements(v) {
			seen[st.Code] = true
			if !Messages.Has(st.Code) {
				t.Errorf("%s: unknown message code %q", name, st.Code)
				continue
			}
			en, ko := Text(i18n.EN, st), Text(i18n.KO, st)
			if en != st.Text {
				t.Errorf("%s: %s: stored text %q, rendered %q", name, st.Code, st.Text, en)
			}
			for _, s := range []string{en, ko} {
				// A question mark stands for an argument that is missing.
				if unfilled.MatchString(s) || strings.TrimSpace(s) == "" {
					t.Errorf("%s: %s: argument not filled in: %q (args %v)", name, st.Code, s, st.Args)
				}
			}
			// Stored as JSON and read back, the sentence is the same.
			b, err := json.Marshal(st)
			if err != nil {
				t.Fatal(err)
			}
			var back model.Statement
			if err := json.Unmarshal(b, &back); err != nil {
				t.Fatal(err)
			}
			if Text(i18n.EN, back) != en || Text(i18n.KO, back) != ko {
				t.Errorf("%s: %s changes when stored: %q -> %q", name, st.Code, ko, Text(i18n.KO, back))
			}
		}
	})
}

// TestStatementsRender renders the findings of every scenario, on every
// platform view, in both languages.
func TestStatementsRender(t *testing.T) {
	seen := map[string]bool{}
	for _, name := range synth.Names() {
		ds := generate(t, name, 3, nil)
		checkStatements(t, name, analyze(t, fullInput(ds), Options{}), seen)
		for _, fl := range synth.Flavors {
			checkStatements(t, name+"/"+string(fl), analyze(t, platformInput(ds, fl), Options{}), seen)
		}
	}
	for _, fl := range synth.Flavors {
		res, err := Run(ctx, sharedLedger(t, "demo", 1, fl), Options{Now: computedAt})
		if err != nil {
			t.Fatal(err)
		}
		checkStatements(t, "ledger/"+string(fl), res, seen)
	}
	if len(seen) < 25 {
		t.Errorf("only %d different messages in all scenarios: %v", len(seen), seen)
	}
}

// TestEveryMessageIsStated: the catalog holds no sentence the engine never
// states, and the engine states no sentence the catalog lacks.
func TestEveryMessageIsStated(t *testing.T) {
	var src strings.Builder
	for _, f := range []string{"verdict.go", "system.go", "analyze.go", "hostfinding.go", "recurrence.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src.Write(b)
	}
	code := src.String()
	stated := regexp.MustCompile(`statement\("([a-z_.0-9-]+)"`).FindAllStringSubmatch(code, -1)
	for _, m := range stated {
		if !Messages.Has(m[1]) {
			t.Errorf("the engine states %q, which the catalog lacks", m[1])
		}
	}
	for _, c := range Messages.Codes() {
		family, _, _ := strings.Cut(c, ".")
		switch family {
		case "grade", "label", "confidence", "mode", "word", "kind", "name", "status", "rule":
			continue // looked up by name, not stated
		}
		// A code is stated as a literal, or built from a literal prefix
		// (code+"_detail").
		base := strings.TrimSuffix(c, "_detail")
		if !strings.Contains(code, `"`+c+`"`) && !strings.Contains(code, `"`+base+`"`) {
			t.Errorf("the catalog holds %q, which the engine never states", c)
		}
	}
}

func TestTextFallsBack(t *testing.T) {
	// A statement of a later engine, whose code this one does not know,
	// keeps the English text it was stored with.
	st := model.Statement{Code: "future.rule", Args: map[string]any{"n": 3}, Text: "3 things a later engine found"}
	for _, l := range i18n.Langs {
		if got := Text(l, st); got != st.Text {
			t.Errorf("%s: %q", l, got)
		}
	}
	if st := statement("ue.unlocated", "n", 2, "when", "2026-11-12 03:14 UTC"); st.Text != "2 uncorrected errors, the latest on 2026-11-12 03:14 UTC; the records carry no location" ||
		Text(i18n.KO, st) != "UE 2건, 최근 2026-11-12 03:14 UTC, 위치 정보 없음" {
		t.Errorf("statement = %+v", st)
	}
	if st := statement("missing.ecs"); st.Args != nil || st.Text != "no ECS counters" {
		t.Errorf("statement without arguments = %+v", st)
	}
}
