// Package i18n renders messages in the languages MemorY speaks: English
// and Korean.
//
// A message is a template per language. Templates name their arguments:
//
//	{n}             the value of argument n
//	{n|row|rows}    "row" when argument n is 1, "rows" otherwise
//
// Numbers are written with thousands separators. A message is identified by
// a code, so that the party that states something (the analysis engine) can
// hand over a code with arguments and the reader's language is chosen where
// the sentence is shown.
//
// An argument that is a string starting with "@" names another message of
// the same catalog, which is put in its place: "@kind.hppr" becomes
// "hard PPR" in English and "hPPR" in Korean. That keeps arguments free of
// words in one language.
package i18n

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Lang is a language MemorY can write in.
type Lang string

const (
	EN Lang = "en"
	KO Lang = "ko"
)

// Langs lists the supported languages.
var Langs = []Lang{EN, KO}

// ParseLang returns the language named by s: "en" or "ko", in any case, with
// an optional region ("ko_KR.UTF-8", "en-US").
func ParseLang(s string) (Lang, error) {
	name := strings.ToLower(strings.TrimSpace(s))
	if i := strings.IndexAny(name, "_-."); i > 0 {
		name = name[:i]
	}
	for _, l := range Langs {
		if name == string(l) {
			return l, nil
		}
	}
	return "", &LangError{Name: s}
}

// LangError is the error of ParseLang: Name is no supported language.
type LangError struct {
	Name string
}

func (e *LangError) Error() string {
	return fmt.Sprintf("unknown language %q (supported: en, ko)", e.Name)
}

// Message is one sentence in every supported language.
type Message struct {
	EN, KO string
}

// Catalog maps message codes to messages.
type Catalog map[string]Message

// Has reports whether the catalog knows the code.
func (c Catalog) Has(code string) bool {
	_, ok := c[code]
	return ok
}

// Codes returns the codes of the catalog in order.
func (c Catalog) Codes() []string {
	out := make([]string, 0, len(c))
	for k := range c {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Text renders the message with the given code in the given language. A
// language without a template falls back to English. An unknown code is
// returned as it is, so that a missing message is visible and nothing is
// lost.
func (c Catalog) Text(lang Lang, code string, args map[string]any) string {
	m, ok := c[code]
	if !ok {
		return code
	}
	tmpl := m.EN
	if lang == KO && m.KO != "" {
		tmpl = m.KO
	}
	return Format(tmpl, c.resolve(lang, args))
}

// resolve replaces arguments that name a message ("@code") by the text of
// that message. References do not nest.
func (c Catalog) resolve(lang Lang, args map[string]any) map[string]any {
	var out map[string]any
	for k, v := range args {
		s, ok := v.(string)
		if !ok || !strings.HasPrefix(s, "@") {
			continue
		}
		m, known := c[s[1:]]
		if !known {
			continue
		}
		if out == nil {
			out = make(map[string]any, len(args))
			for k2, v2 := range args {
				out[k2] = v2
			}
		}
		out[k] = m.EN
		if lang == KO && m.KO != "" {
			out[k] = m.KO
		}
	}
	if out == nil {
		return args
	}
	return out
}

// Format fills a template with arguments. An argument the template names
// and args does not hold is written as "?".
func Format(tmpl string, args map[string]any) string {
	var b strings.Builder
	for {
		open := strings.IndexByte(tmpl, '{')
		if open < 0 {
			break
		}
		end := strings.IndexByte(tmpl[open:], '}')
		if end < 0 {
			break
		}
		b.WriteString(tmpl[:open])
		field := tmpl[open+1 : open+end]
		tmpl = tmpl[open+end+1:]

		name, forms, plural := strings.Cut(field, "|")
		v, ok := args[name]
		switch {
		case !ok:
			b.WriteString("?")
		case plural:
			one, other, _ := strings.Cut(forms, "|")
			if n, isNum := number(v); isNum && n == 1 {
				b.WriteString(one)
			} else {
				b.WriteString(other)
			}
		default:
			b.WriteString(Value(v))
		}
	}
	b.WriteString(tmpl)
	return b.String()
}

// Placeholders returns the argument names a template uses, in order of
// first use.
func Placeholders(tmpl string) []string {
	var out []string
	seen := map[string]bool{}
	for {
		open := strings.IndexByte(tmpl, '{')
		if open < 0 {
			break
		}
		end := strings.IndexByte(tmpl[open:], '}')
		if end < 0 {
			break
		}
		name, _, _ := strings.Cut(tmpl[open+1:open+end], "|")
		tmpl = tmpl[open+end+1:]
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

func number(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case float32:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}

// Value formats one argument: whole numbers with thousands separators,
// other numbers with one decimal, lists joined by commas.
func Value(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []string:
		return strings.Join(x, ", ")
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = Value(e)
		}
		return strings.Join(parts, ", ")
	case nil:
		return "?"
	}
	if n, ok := number(v); ok {
		if n == math.Trunc(n) && math.Abs(n) < 1e15 {
			return Thousands(int64(n))
		}
		return strconv.FormatFloat(n, 'f', 1, 64)
	}
	return fmt.Sprint(v)
}

// Thousands writes n with a comma between groups of three digits.
func Thousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := ""
	if n < 0 {
		neg, s = "-", s[1:]
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return neg + s
}
