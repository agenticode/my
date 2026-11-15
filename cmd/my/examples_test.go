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
	// The same in Korean, under examples/ko.
	if err := os.Mkdir(filepath.Join(tmp, "ko"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := runEnv(fixed, "report", file, "-o", filepath.Join(tmp, "ko", "demo-report.html"), "--figures", filepath.Join(tmp, "ko", "figures"), "--lang", "ko"); code != cli.ExitOK {
		t.Fatalf("my report examples/demo.mycase --lang ko: exit code %d, stderr %q", code, errOut)
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
	for _, sub := range []string{"", "ko"} {
		same(filepath.Join(sub, "demo-report.html"))
		got, want := list(filepath.Join(tmp, sub, "figures")), list(filepath.Join(dir, sub, "figures"))
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("examples/%s holds %v, the command writes %v (run \"make examples\")", filepath.Join(sub, "figures"), want, got)
		}
		for _, name := range got {
			same(filepath.Join(sub, "figures", name))
		}
	}
	// The Korean report is in Korean.
	if b, err := os.ReadFile(filepath.Join(dir, "ko", "demo-report.html")); err != nil || !bytes.Contains(b, []byte(`<html lang="ko">`)) || !bytes.Contains(b, []byte("즉시 교체")) {
		t.Errorf("examples/ko/demo-report.html is not a Korean report (%v)", err)
	}

	// Each README shows figures of the example in its language; they exist.
	for name, prefix := range map[string]string{"README.md": "](examples/", "README.ko.md": "](examples/ko/"} {
		readme, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), name))
		if err != nil {
			t.Fatal(err)
		}
		shown := 0
		for _, line := range strings.Split(string(readme), "\n") {
			for {
				i := strings.Index(line, "](examples/")
				if i < 0 {
					break
				}
				path := line[i+2:]
				end := strings.IndexByte(path, ')')
				if end < 0 {
					break
				}
				path, line = path[:end], path[end:]
				if _, err := os.Stat(filepath.Join(testutil.RepoRoot(t), filepath.FromSlash(path))); err != nil {
					t.Errorf("%s points at %s: %v", name, path, err)
				}
				if strings.HasPrefix("]("+path, prefix) && strings.HasSuffix(path, ".svg") {
					shown++
				}
			}
		}
		if shown < 3 {
			t.Errorf("%s shows %d figures of the example in its language", name, shown)
		}
	}
}
