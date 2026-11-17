package dimmspd

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/testutil"
)

// docSection checks that the text between the markers <!-- NAME --> and
// <!-- /NAME --> of docs/compatibility.md is want; UPDATE_GOLDEN=1
// writes it there.
func docSection(t *testing.T, name, want string) {
	t.Helper()
	path := testutil.RepoRoot(t) + "/docs/compatibility.md"
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	open, end := "<!-- "+name+" -->\n", "<!-- /"+name+" -->"
	i, j := strings.Index(text, open), strings.Index(text, end)
	if i < 0 || j < i {
		t.Fatalf("docs/compatibility.md has no section %s", name)
	}
	if got := text[i+len(open) : j]; got != want {
		if os.Getenv(testutil.UpdateGoldenEnv) != "" {
			if err := os.WriteFile(path, []byte(text[:i+len(open)]+want+text[j:]), 0o644); err != nil {
				t.Fatal(err)
			}
			return
		}
		t.Errorf("the section %s of docs/compatibility.md differs from the code (run with UPDATE_GOLDEN=1 after checking the change)", name)
	}
}

// The table of reasons of the docs is that of the code: every code in the
// order Reason tests it, what was read to tell it, the short text.
func TestReasonsInTheDocs(t *testing.T) {
	var b strings.Builder
	b.WriteString("| Code | Read to tell | Short text |\n| --- | --- | --- |\n")
	example := map[string]*Probe{
		ReasonSMBusNotLoaded:     {PCI: []ProbePCI{{ID: "8086:a323", Driver: "-"}}},
		ReasonSMBusNotBuilt:      {PCI: []ProbePCI{{ID: "8086:a323", Driver: "-"}}},
		ReasonSPDDriverNotLoaded: {Clients: []ProbeClient{{Name: "ee1004"}}},
		ReasonSPDDriverNotBuilt:  {Clients: []ProbeClient{{Name: "ee1004"}}},
	}
	for _, code := range Reasons {
		ev, ok := Evidence[code]
		if !ok {
			t.Errorf("%s: no evidence", code)
		}
		p := example[code]
		if p == nil {
			p = &Probe{}
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", code, ev, Short(p, code))
	}
	docSection(t, "spd-reasons", b.String())
}
