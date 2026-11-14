package i18n

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestFormat(t *testing.T) {
	args := map[string]any{"n": 1231, "one": 1, "zero": 0, "row": "0x1a2f", "ratio": 2.34, "whole": 3.0, "list": []string{"CPU0_B1", "CPU0_B2"}, "neg": int64(-45000)}
	tests := []struct{ tmpl, want string }{
		{"plain text", "plain text"},
		{"{n} corrected errors in row {row}", "1,231 corrected errors in row 0x1a2f"},
		{"{one} {one|error|errors}, {n} {n|error|errors}, {zero} {zero|error|errors}", "1 error, 1,231 errors, 0 errors"},
		{"{ratio} times, {whole} days", "2.3 times, 3 days"},
		{"slots {list}", "slots CPU0_B1, CPU0_B2"},
		{"{neg}", "-45,000"},
		{"{missing} and {missing|a|b}", "? and ?"},
		{"{row|a|b}", "b"}, // not a number: the other form
		{"an open brace { stays", "an open brace { stays"},
		{"같은 row에서 CE {n}건, column {one}개", "같은 row에서 CE 1,231건, column 1개"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := Format(tt.tmpl, args); got != tt.want {
			t.Errorf("Format(%q) = %q, want %q", tt.tmpl, got, tt.want)
		}
	}
}

// TestArgsSurviveJSON: arguments are stored as JSON with a finding. Read
// back, numbers are float64 and lists are []any; the text must not change.
func TestArgsSurviveJSON(t *testing.T) {
	args := map[string]any{"n": 1231, "one": int64(1), "list": []string{"a", "b"}, "s": "0x1a2f"}
	const tmpl = "{n} {n|error|errors}, {one} {one|place|places}: {list} at {s}"
	want := Format(tmpl, args)
	b, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if got := Format(tmpl, back); got != want || want != "1,231 errors, 1 place: a, b at 0x1a2f" {
		t.Errorf("after JSON: %q, before: %q", got, want)
	}
}

func TestCatalog(t *testing.T) {
	c := Catalog{
		"ue.count": {EN: "{n} uncorrected {n|error|errors}", KO: "UE {n}건"},
		"en.only":  {EN: "only English"},
	}
	if got := c.Text(EN, "ue.count", map[string]any{"n": 1}); got != "1 uncorrected error" {
		t.Errorf("en: %q", got)
	}
	if got := c.Text(KO, "ue.count", map[string]any{"n": 2}); got != "UE 2건" {
		t.Errorf("ko: %q", got)
	}
	if got := c.Text(KO, "en.only", nil); got != "only English" {
		t.Errorf("fallback to English: %q", got)
	}
	if got := c.Text(EN, "no.such.code", nil); got != "no.such.code" {
		t.Errorf("unknown code: %q", got)
	}
	// An argument can name another message of the catalog.
	c["kind.hppr"] = Message{EN: "hard PPR", KO: "hPPR"}
	c["rep"] = Message{EN: "{kind} on {slot}", KO: "{slot} {kind}"}
	args := map[string]any{"kind": "@kind.hppr", "slot": "@not.a.code"}
	if en, ko := c.Text(EN, "rep", args), c.Text(KO, "rep", args); en != "hard PPR on @not.a.code" || ko != "@not.a.code hPPR" {
		t.Errorf("message references: %q, %q", en, ko)
	}
	if args["kind"] != "@kind.hppr" {
		t.Error("Text changed the caller's arguments")
	}
	delete(c, "kind.hppr")
	delete(c, "rep")
	if !c.Has("ue.count") || c.Has("x") || !reflect.DeepEqual(c.Codes(), []string{"en.only", "ue.count"}) {
		t.Errorf("Has or Codes: %v", c.Codes())
	}
}

func TestPlaceholders(t *testing.T) {
	got := Placeholders("{n} {n|error|errors} in {slot}, {when}; { not closed")
	if want := []string{"n", "slot", "when"}; !reflect.DeepEqual(got, want) {
		t.Errorf("placeholders = %v, want %v", got, want)
	}
	if got := Placeholders("none"); got != nil {
		t.Errorf("placeholders of plain text = %v", got)
	}
}

func TestParseLang(t *testing.T) {
	for in, want := range map[string]Lang{"en": EN, "ko": KO, "KO": KO, " en ": EN, "ko_KR.UTF-8": KO, "en-US": EN} {
		if got, err := ParseLang(in); err != nil || got != want {
			t.Errorf("ParseLang(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "kr", "korean", "C", "ja"} {
		if _, err := ParseLang(in); err == nil {
			t.Errorf("ParseLang(%q) returned no error", in)
		}
	}
}

func TestValueAndThousands(t *testing.T) {
	for in, want := range map[int64]string{0: "0", 7: "7", 999: "999", 1000: "1,000", 1234567: "1,234,567", -1000: "-1,000", -999: "-999"} {
		if got := Thousands(in); got != want {
			t.Errorf("Thousands(%d) = %q, want %q", in, got, want)
		}
	}
	if Value(nil) != "?" || Value(true) != "true" || Value(uint64(5000)) != "5,000" || Value([]any{1.0, "x"}) != "1, x" || Value(float32(0.5)) != "0.5" {
		t.Error("Value")
	}
}
