package yamlite

import (
	"strings"
	"testing"
)

// What the reader makes of the small part of YAML it reads.
func TestYAMLForms(t *testing.T) {
	root, err := Parse([]byte("\xef\xbb\xbfa: 1   # one\nb:\n  - x\n  -   y: 2\n      z: \"q # not a comment\"\n  - \n\"k: 1\": 'v''s'\nurl: https://example.com/a#b\nempty:\nlast: text with: a colon\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(root.Keys, "|"); got != "a|b|k: 1|url|empty|last" {
		t.Fatalf("keys: %s", got)
	}
	b := root.Map["b"]
	if root.Map["a"].Scalar != "1" || b.Kind != List || len(b.List) != 3 || b.List[0].Scalar != "x" || b.List[1].Map["y"].Scalar != "2" ||
		b.List[1].Map["z"].Scalar != "q # not a comment" || b.List[2].Scalar != "" || root.Map["k: 1"].Scalar != "v's" ||
		root.Map["url"].Scalar != "https://example.com/a#b" || root.Map["empty"].Scalar != "" || root.Map["last"].Scalar != "text with: a colon" {
		t.Errorf("tree: %+v", root)
	}
	if root.Map["last"].Line != 10 || b.List[1].Line != 4 {
		t.Errorf("lines: %d, %d", root.Map["last"].Line, b.List[1].Line)
	}
}

// Lines that lie outside the part of YAML the reader takes are refused
// with their number.
func TestRefused(t *testing.T) {
	for _, text := range []string{"a: [1, 2]\n", "a: &x 1\n", "a:\n\tb: 1\n", "a: |\n  x\n", "---\na: 1\n---\nb: 2\n"} {
		if _, err := Parse([]byte(text)); err == nil || !strings.Contains(err.Error(), "line ") {
			t.Errorf("%q: %v", text, err)
		}
	}
}
