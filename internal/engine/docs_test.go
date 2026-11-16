package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/testutil"
)

// TestRulesPage keeps docs/rules.md in step with the engine: every rule
// has its section under the heading of its grade, every threshold its row
// with the default and the meaning the tool prints, and the page names no
// rule and no threshold the engine does not have.
func TestRulesPage(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "docs", "rules.md"))
	if err != nil {
		t.Fatal(err)
	}
	page := string(b)

	// The heading of a grade is its name with a capital letter.
	heading := func(id string) string {
		at := strings.Index(page, "\n### "+id+"\n")
		if at < 0 {
			return ""
		}
		before := page[:at]
		h := strings.LastIndex(before, "\n## ")
		if h < 0 {
			return ""
		}
		line := before[h+4:]
		return strings.ToLower(line[:strings.IndexByte(line, '\n')])
	}
	for _, r := range Rules {
		want := GradeText(i18n.EN, r.Grade)
		if got := heading(r.ID); got != want {
			t.Errorf("docs/rules.md: %s stands under %q, its grade is %q", r.ID, got, want)
		}
	}
	for _, id := range regexp.MustCompile(`R-[A-Z]+-\d\d`).FindAllString(page, -1) {
		if _, ok := RuleByID(id); !ok {
			t.Errorf("docs/rules.md names %s, which is no rule", id)
		}
	}

	params := Params()
	for _, p := range params {
		row := fmt.Sprintf("| `%s` | %s | %s |", p.Key, p.Default, p.Help)
		if !strings.Contains(page, row) {
			t.Errorf("docs/rules.md lacks the row of a threshold:\n%s", row)
		}
	}
	known := map[string]bool{}
	for _, p := range params {
		known[p.Key] = true
	}
	for _, m := range regexp.MustCompile("`((?:cluster|trend|spread|storm|baseline|rule|check|evidence|loop)\\.[a-z_.]+)`").FindAllStringSubmatch(page, -1) {
		if !known[m[1]] {
			t.Errorf("docs/rules.md names the threshold %s, which the engine does not have", m[1])
		}
	}

	// Where the confidence of a finding starts, per rule.
	for id, start := range baseConfidence {
		if !strings.Contains(page, fmt.Sprintf("| %s | %d |", id, start)) {
			t.Errorf("docs/rules.md: the confidence of %s starts at %d", id, start)
		}
	}
	if len(baseConfidence) != len(Rules) {
		t.Errorf("%d rules have a start of the confidence, %d rules exist", len(baseConfidence), len(Rules))
	}
}
