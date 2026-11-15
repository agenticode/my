package main

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/testutil"
)

// TestExamples keeps the files under examples/ true: the example case
// opens and shows the host the README shows, and the report and the
// figures beside it are what "my report" writes from that case today.
// After an intended change, "make examples" writes them again.
func TestExamples(t *testing.T) {
	dir := filepath.Join(testutil.RepoRoot(t), "examples")
	file := filepath.Join(dir, "demo.mycase")

	code, out, errOut := runEnv(fixed, "open", file)
	if code != 3 || errOut != "" {
		t.Fatalf("my open examples/demo.mycase: exit code %d, stderr %q", code, errOut)
	}
	wantLines(t, out,
		"case bundle of 2026-11-13 09:30 UTC, written by my dev; subject CPU0_C1 S/N 3B9C56",
		"host r12-n034  Example Systems R760  2026-07-31 to 2026-11-13  CE 1,284, UE 1  observation grade A",
		" 1  CPU0_C1  3B9C56  1,231   1  row         ▃▄▄▄▅▅▆▆▇█ ↑  replace now  R-UE-01 R-ROW-03 R-REP-02",
		" 2  CPU1_D1  2AEB41     53   0  single-bit  ▆▁▄█▃▄▃▄▄▁ ↓  observe      R-SOFT-01",
	)

	tmp := t.TempDir()
	report, figures := filepath.Join(tmp, "demo-report.html"), filepath.Join(tmp, "figures")
	if code, _, errOut := runEnv(fixed, "report", file, "-o", report, "--figures", figures); code != cli.ExitOK {
		t.Fatalf("my report examples/demo.mycase: exit code %d, stderr %q", code, errOut)
	}
	same := func(name string) {
		t.Helper()
		got, err := os.ReadFile(filepath.Join(tmp, name))
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Errorf("examples/%s: %v (run \"make examples\")", name, err)
			return
		}
		if bytes.Equal(got, want) {
			return
		}
		// Point at the first line that differs.
		g, w := strings.Split(string(got), "\n"), strings.Split(string(want), "\n")
		for i := 0; i < len(g) || i < len(w); i++ {
			var gl, wl string
			if i < len(g) {
				gl = g[i]
			}
			if i < len(w) {
				wl = w[i]
			}
			if gl != wl {
				at := 0
				for at < len(gl) && at < len(wl) && gl[at] == wl[at] {
					at++
				}
				cut := func(s string) string { return s[max(at-40, 0):min(at+80, len(s))] }
				t.Errorf("examples/%s is not what the command writes (run \"make examples\" and review the diff); line %d:\n examples %q\n command  %q", name, i+1, cut(wl), cut(gl))
				return
			}
		}
	}
	same("demo-report.html")

	list := func(d string) []string {
		entries, err := os.ReadDir(d)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		return names
	}
	got, want := list(figures), list(filepath.Join(dir, "figures"))
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("examples/figures holds %v, the command writes %v (run \"make examples\")", want, got)
	}
	for _, name := range got {
		same(filepath.Join("figures", name))
	}

	// The README shows figures of the example; they exist.
	readme, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	shown := 0
	for _, line := range strings.Split(string(readme), "\n") {
		i := strings.Index(line, "](examples/")
		if i < 0 {
			continue
		}
		path := line[i+2:]
		path = path[:strings.IndexByte(path, ')')]
		if _, err := os.Stat(filepath.Join(testutil.RepoRoot(t), filepath.FromSlash(path))); err != nil {
			t.Errorf("the README points at %s: %v", path, err)
		}
		shown++
	}
	if shown < 4 {
		t.Errorf("the README points at %d files of the example", shown)
	}
}
