package testutil

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A fuzz function that the Makefile does not list is never run by "make
// fuzz" or by CI, and one that docs/fuzzing.md does not list is not in
// the table a reader looks things up in. Both lists are held against the
// code.
func TestEveryFuzzTargetIsListed(t *testing.T) {
	root := RepoRoot(t)
	fn := regexp.MustCompile(`(?m)^func (Fuzz[A-Za-z0-9_]+)\(f \*testing\.F\)`)
	var have []string
	for _, top := range []string{"cmd", "internal", "tools"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, "_test.go") {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, filepath.Dir(p))
			for _, m := range fn.FindAllStringSubmatch(string(b), -1) {
				have = append(have, "./"+filepath.ToSlash(rel)+":"+m[1])
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(have)
	if len(have) < 30 {
		t.Fatalf("found %d fuzz functions; the search is broken", len(have))
	}

	makefile, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	listed := regexp.MustCompile(`\./[A-Za-z0-9_/.-]+:Fuzz[A-Za-z0-9_]+`).FindAllString(string(makefile), -1)
	sort.Strings(listed)
	in := func(list []string, s string) bool {
		i := sort.SearchStrings(list, s)
		return i < len(list) && list[i] == s
	}
	for _, target := range have {
		if !in(listed, target) {
			t.Errorf("%s is not in FUZZ_TARGETS of the Makefile", target)
		}
	}
	for _, target := range listed {
		if !in(have, target) {
			t.Errorf("FUZZ_TARGETS of the Makefile names %s, which the code does not have", target)
		}
	}

	doc, err := os.ReadFile(filepath.Join(root, "docs", "fuzzing.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range have {
		pkg, name, _ := strings.Cut(strings.TrimPrefix(target, "./"), ":")
		if !strings.Contains(string(doc), "`"+pkg+"`") || !strings.Contains(string(doc), "`"+name+"`") {
			t.Errorf("docs/fuzzing.md does not list %s of %s", name, pkg)
		}
	}
}
