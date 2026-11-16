package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMeasure(t *testing.T) {
	lines, chars := measure([]byte("판정  즉시 교체\nab  \n\n\n"))
	if lines != 2 || chars != 11 {
		t.Errorf("measure = %d lines, %d chars; want 2, 11", lines, chars)
	}
	if lines, chars := measure([]byte("\n \n")); lines != 0 || chars != 0 {
		t.Errorf("empty screen = %d lines, %d chars", lines, chars)
	}
}

func TestSectionText(t *testing.T) {
	doc := `<section id="s1" class="sec"><h2>1 Summary</h2><style>p{}</style><p>a &amp; b</p>` +
		`<svg><text>figure</text></svg><dl><dt>Host</dt><dd>lab</dd></dl>` +
		`<section id="where" class="dimm"><p>inner</p></section></section>` +
		`<section id="s2" class="sec"><p>next</p></section>`
	got, ok := sectionText(doc, "s1")
	want := "1 Summary\na & b\nHost\nlab\ninner\n"
	if !ok || got != want {
		t.Errorf("sectionText = %q, %v; want %q", got, ok, want)
	}
	if _, ok := sectionText(doc, "s9"); ok {
		t.Error("a missing section was found")
	}
}

func TestCompare(t *testing.T) {
	dir := t.TempDir()
	before := filepath.Join(dir, "before.tsv")
	after := filepath.Join(dir, "after.tsv")
	head := "capture\tscreen\tlang\tlines\tchars\n"
	if err := os.WriteFile(before, []byte(head+"c1\twhy\ten\t34\t2000\nc1\twhy\tko\t30\t1500\nc1\tscan\ten\t20\t900\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(after, []byte(head+"c1\twhy\ten\t12\t700\nc1\twhy\tko\t11\t600\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := compareFiles(before, after, &b); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"| scan | c1 | 20 | - | - | - |", "| why | c1 | 34 | 12 | 30 | 11 |"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("compare lacks %q:\n%s", want, b.String())
		}
	}
	if strings.Index(b.String(), "| scan |") > strings.Index(b.String(), "| why |") {
		t.Errorf("rows are not in the order of the screens:\n%s", b.String())
	}
}
