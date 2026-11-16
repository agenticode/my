package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// briefCases are the cases the tests of the short screens run on: the
// example case and lab captures with a finding of every kind of scope.
func briefCases(t *testing.T) [][]string {
	real := func(name string) []string {
		return []string{"--root", filepath.Join("..", "..", "testdata", "real", name)}
	}
	return [][]string{
		{"open", demo(t)},
		real("6.19.10-ghes"),
		real("6.19.10-ghes-multi"),
		real("6.19.10-ghes-loop-spread"),
		real("7.3.0-mce"),
		real("6.19.10-mce-loop"),
	}
}

// briefRun runs a command on a case: "open FILE" goes in front of it, the
// flags of a scan after it.
func briefRun(t *testing.T, c []string, args ...string) string {
	t.Helper()
	var all []string
	if c[0] == "open" {
		all = append(append(all, c...), args...)
	} else {
		all = append(append(all, args...), c...)
	}
	code, out, errOut := run(all...)
	if code < 0 || code > 4 || errOut != "" {
		t.Fatalf("my %s: exit code %d, stderr %q", strings.Join(all, " "), code, errOut)
	}
	return out
}

type jsonText struct {
	Code string `json:"code"`
	Text string `json:"text"`
}

type jsonCard struct {
	Slot     string `json:"slot"`
	Evidence []struct {
		Rule string `json:"rule"`
		jsonText
	} `json:"evidence"`
	Checks []struct {
		Effect     string `json:"effect"`
		EffectText string `json:"effect_text"`
		jsonText
	} `json:"checks"`
	Missing  []jsonText `json:"missing"`
	Baseline []jsonText `json:"baseline"`
	Next     *jsonText  `json:"next"`
	Factors  []jsonText `json:"confidence_factors"`
}

// statements returns the sentences of a card with the code of each.
func (c jsonCard) statements() []jsonText {
	var out []jsonText
	for _, e := range c.Evidence {
		out = append(out, e.jsonText)
	}
	for _, k := range c.Checks {
		out = append(out, k.jsonText)
	}
	out = append(append(append(out, c.Missing...), c.Baseline...), c.Factors...)
	if c.Next != nil {
		out = append(out, *c.Next)
	}
	return out
}

func whyCardsJSON(t *testing.T, c []string, lang string) []jsonCard {
	t.Helper()
	var e struct {
		Data []jsonCard `json:"data"`
	}
	if err := json.Unmarshal([]byte(briefRun(t, c, "why", "--all", "--json", "--lang", lang)), &e); err != nil {
		t.Fatal(err)
	}
	return e.Data
}

// flat joins the words of a text with single spaces, so that a sentence
// wrapped over lines is found as it is.
func flat(s string) string { return strings.Join(strings.Fields(s), " ") }

// TestDetailHoldsEveryStatement: what the short card leaves out is not
// lost. Every statement of every finding, as --json gives it, is in the
// text of "my why --detail", in both languages; and the short card counts
// what it leaves out.
func TestDetailHoldsEveryStatement(t *testing.T) {
	for _, c := range briefCases(t) {
		for _, lang := range []string{"en", "ko"} {
			cards := whyCardsJSON(t, c, lang)
			if len(cards) == 0 {
				t.Errorf("%v: no finding", c)
			}
			detail := flat(briefRun(t, c, "why", "--all", "--detail", "--lang", lang, "--width", "200"))
			short := briefRun(t, c, "why", "--all", "--lang", lang, "--width", "200")
			for _, card := range cards {
				for _, s := range card.statements() {
					if s.Code == "" || !strings.Contains(detail, flat(s.Text)) {
						t.Errorf("%v %s %s: statement %s %q is not in --detail", c, lang, card.Slot, s.Code, s.Text)
					}
				}
			}
			if n := strings.Count(short, ": --detail\n"); n != len(cards) {
				t.Errorf("%v %s: %d cards, %d lines that name --detail:\n%s", c, lang, len(cards), n, short)
			}
		}
	}
}

// TestBriefParity: the short card has the same lines in English and in
// Korean, and each line carries the same statement in both.
func TestBriefParity(t *testing.T) {
	for _, c := range briefCases(t) {
		en := strings.Split(briefRun(t, c, "why", "--all", "--width", "400"), "\n")
		ko := strings.Split(briefRun(t, c, "why", "--all", "--lang", "ko", "--width", "400"), "\n")
		if len(en) != len(ko) {
			t.Errorf("%v: %d lines in English, %d in Korean:\n%s\n%s", c, len(en), len(ko), strings.Join(en, "\n"), strings.Join(ko, "\n"))
			continue
		}
		cardsEN, cardsKO := whyCardsJSON(t, c, "en"), whyCardsJSON(t, c, "ko")
		if len(cardsEN) != len(cardsKO) {
			t.Fatalf("%v: %d cards in English, %d in Korean", c, len(cardsEN), len(cardsKO))
		}
		// The line of each statement, searched within the lines of its
		// card: from the line of its decision to the next card's.
		lineOf := func(lines []string, from, to int, text string) int {
			for i := from; i < to && i < len(lines); i++ {
				if strings.Contains(lines[i], text) {
					return i
				}
			}
			return -1
		}
		heads := func(lines []string, cards []jsonCard) []int {
			out, from := []int{}, 0
			for _, card := range cards {
				at := from
				if card.Slot != "" {
					at = lineOf(lines, from, len(lines), card.Slot+"  ")
				}
				out = append(out, at)
				from = at + 1
			}
			return append(out, len(lines))
		}
		he, hk := heads(en, cardsEN), heads(ko, cardsKO)
		for i := range cardsEN {
			se, sk := cardsEN[i].statements(), cardsKO[i].statements()
			if he[i] != hk[i] || he[i] < 0 {
				t.Errorf("%v %s: the card starts on line %d in English, %d in Korean", c, cardsEN[i].Slot, he[i], hk[i])
				continue
			}
			if len(se) != len(sk) {
				t.Errorf("%v %s: %d statements in English, %d in Korean", c, cardsEN[i].Slot, len(se), len(sk))
				continue
			}
			for k := range se {
				if se[k].Code != sk[k].Code {
					t.Errorf("%v %s: statement %d is %s in English, %s in Korean", c, cardsEN[i].Slot, k, se[k].Code, sk[k].Code)
					continue
				}
				a, b := lineOf(en, he[i], he[i+1], se[k].Text), lineOf(ko, hk[i], hk[i+1], sk[k].Text)
				if a != b {
					t.Errorf("%v %s: %s on line %d in English, %d in Korean", c, cardsEN[i].Slot, se[k].Code, a, b)
				}
			}
		}
	}
}
