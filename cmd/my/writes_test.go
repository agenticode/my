package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/testutil"
)

// listing returns every path below dir with its size and time, and the
// hash of the whole, to tell whether a run changed anything there.
func listing(t *testing.T, dir string) (string, []string) {
	t.Helper()
	var names []string
	h := sha256.New()
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		if rel == "." {
			return nil
		}
		names = append(names, rel)
		if fi, err := d.Info(); err == nil {
			h.Write([]byte(rel + fi.ModTime().String() + fi.Mode().String()))
			if !d.IsDir() {
				h.Write([]byte{byte(fi.Size())})
			}
		}
		return nil
	})
	sort.Strings(names)
	return hex.EncodeToString(h.Sum(nil)), names
}

// TestCommandsWriteOnlyWhatTheyAreAsked: every command but "my harvest"
// writes the files it was named and nothing else: no file in the working
// directory, none left in the temporary directory, no change to what it
// reads. "my harvest install" is the one command that writes elsewhere
// (TestHarvestInstall).
func TestCommandsWriteOnlyWhatTheyAreAsked(t *testing.T) {
	repo := testutil.RepoRoot(t)
	capture := filepath.Join(repo, "testdata", "real", "7.3.0-mce-print-all")
	demoLedger := demo(t)
	tmp, cwd, outDir := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Chdir(cwd)
	out := func(name string) string { return filepath.Join(outDir, name) }
	inputs := []string{capture}
	before := map[string]string{}
	for _, in := range inputs {
		before[in], _ = listing(t, in)
	}
	ledgerBefore, _ := listing(t, filepath.Dir(demoLedger))
	for _, tt := range []struct {
		args    []string
		outputs []string
	}{
		{[]string{"--root", capture}, nil},
		{[]string{"why", "--root", capture}, nil},
		{[]string{"timeline", "--root", capture, "--json"}, nil},
		{[]string{"inventory", "--root", capture}, nil},
		{[]string{"preflight", "--root", capture}, nil},
		{[]string{"preflight", "--root", capture, "--script"}, nil},
		{[]string{"--root", capture, "--ledger", out("kept.db")}, []string{"kept.db"}},
		{[]string{"bundle", "--root", capture, "-o", out("case.mycase")}, []string{"case.mycase"}},
		{[]string{"report", "--root", capture, "-o", out("report.html")}, []string{"report.html"}},
		{[]string{"import", filepath.Join(capture, "dmesg"), "-o", out("import.mycase")}, []string{"import.mycase"}},
		{[]string{"import", filepath.Join(capture, "dmesg")}, nil},
		{[]string{"open", out("case.mycase")}, nil},
		{[]string{"open", demoLedger, "why"}, nil},
		{[]string{"export", "fmfm", "--root", capture, "-o", out("fmfm.json")}, []string{"fmfm.json"}},
		{[]string{"version"}, nil},
		{[]string{"help", "harvest"}, nil},
	} {
		_, wantBefore := listing(t, outDir)
		code, _, errOut := run(tt.args...)
		if code > 4 {
			t.Errorf("%v: exit %d: %s", tt.args, code, errOut)
		}
		if _, names := listing(t, tmp); len(names) != 0 {
			t.Errorf("%v left in the temporary directory: %q", tt.args, names)
		}
		if _, names := listing(t, cwd); len(names) != 0 {
			t.Errorf("%v wrote to the working directory: %q", tt.args, names)
		}
		_, after := listing(t, outDir)
		want := append(append([]string{}, wantBefore...), tt.outputs...)
		sort.Strings(want)
		want = uniq(want)
		if strings.Join(after, " ") != strings.Join(want, " ") {
			t.Errorf("%v: output directory holds %q, want %q", tt.args, after, want)
		}
		for in, h := range before {
			if now, _ := listing(t, in); now != h {
				t.Errorf("%v changed %s", tt.args, in)
			}
		}
		if now, _ := listing(t, filepath.Dir(demoLedger)); now != ledgerBefore {
			t.Errorf("%v changed the ledger it opened", tt.args)
		}
	}
}

func uniq(s []string) []string {
	var out []string
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

var _ = os.Getenv
