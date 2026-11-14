package i18n

import (
	"strings"
	"testing"
)

// FuzzFormat feeds arbitrary templates to Format. It must not panic, must
// keep text without placeholders as it is, and must name the same
// arguments as Placeholders.
func FuzzFormat(f *testing.F) {
	f.Add("{n} uncorrected {n|error|errors} at {where}", "n", "rank0 row 0x1a2f")
	f.Add("UE {n}건, 최근 {when}", "when", "2026-11-12")
	f.Add("{", "a", "b")
	f.Add("}{}{|}{||}{a|b}{a|b|c|d}", "a", "1")
	f.Add("plain", "", "")
	f.Fuzz(func(t *testing.T, tmpl, name, value string) {
		args := map[string]any{name: value, "n": 2}
		out := Format(tmpl, args)
		if !strings.Contains(tmpl, "{") && out != tmpl {
			t.Errorf("Format(%q) = %q", tmpl, out)
		}
		names := Placeholders(tmpl)
		if len(names) == 0 && strings.Contains(out, "?") && !strings.Contains(tmpl, "?") && !strings.Contains(value, "?") {
			t.Errorf("Format(%q) = %q: a missing argument without a placeholder", tmpl, out)
		}
		c := Catalog{"m": {EN: tmpl, KO: tmpl}}
		if got := c.Text(KO, "m", args); got != c.Text(EN, "m", args) {
			t.Errorf("the same template gives %q in Korean", got)
		}
	})
}
