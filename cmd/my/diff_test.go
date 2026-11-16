package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/testledger"
	"github.com/agenticode/my/internal/testutil"
)

// pairDir puts the two cases of a pair into an empty directory under the
// names given and makes it the working directory, so that a screen names
// the files the same way on every machine.
func pairDir(t *testing.T, pair, before, after string) {
	t.Helper()
	a, b := testledger.DiffPair(t, pair).Paths(t)
	dir := t.TempDir()
	for from, to := range map[string]string{a: before, b: after} {
		if err := copyFile(from, filepath.Join(dir, to)); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
}

// The command prints what the renderer draws for the same two cases, and
// exits with the grade of the host in the later one.
func TestDiffCommand(t *testing.T) {
	// Before the working directory changes: the root is found from it.
	root := testutil.RepoRoot(t)
	golden := func(name string) string {
		b, err := os.ReadFile(filepath.Join(root, "internal", "term", "testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	for _, tt := range []struct {
		pair, golden string
		args         []string
		code         int
	}{
		{"moved-scan", "diff-moved-scan.en.txt", nil, 2},
		{"moved-scan", "diff-moved-scan.ko.txt", []string{"--lang", "ko"}, 2},
		{"moved-scan", "diff-moved-scan.w60.txt", []string{"--width=60"}, 2},
		{"slot", "diff-slot.ascii.txt", []string{"--ascii"}, 4},
		{"replaced", "diff-replaced.en.txt", nil, 1},
		{"to-empty", "diff-all.en.txt", []string{"--all", "--at", "2026-10-03"}, 2},
	} {
		pairDir(t, tt.pair, "before.mycase", "after.mycase")
		code, out, errOut := run(append([]string{"diff", "before.mycase", "after.mycase"}, tt.args...)...)
		if code != tt.code || errOut != "" {
			t.Errorf("%s %v: exit code %d, stderr %q", tt.pair, tt.args, code, errOut)
		}
		if out != golden(tt.golden) {
			t.Errorf("my diff %v differs from %s:\n%s", tt.args, tt.golden, out)
		}
	}
}

func TestDiffJSON(t *testing.T) {
	var code int
	var out, errOut string
	// In a test of its own: the golden file is found from the directory
	// of the package, which is the working directory again afterwards.
	t.Run("run", func(t *testing.T) {
		pairDir(t, "moved-scan", "before.mycase", "after.mycase")
		code, out, errOut = run("diff", "before.mycase", "after.mycase", "--json")
	})
	if code != 2 || errOut != "" {
		t.Fatalf("exit code %d, stderr %q", code, errOut)
	}
	var e struct {
		View     string `json:"view"`
		Host     string `json:"host"`
		Grade    string `json:"grade"`
		ExitCode int    `json:"exit_code"`
		Data     struct {
			Before struct {
				File string `json:"file"`
			} `json:"before"`
			SinceBy string `json:"since_by"`
			Changes []struct {
				Kind string `json:"kind"`
			} `json:"changes"`
			Readings []struct {
				Code string `json:"code"`
			} `json:"readings"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &e); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if e.View != "diff" || e.Host != "synth-moved-dimm" || e.Grade != "consider-replacement" || e.ExitCode != 2 ||
		e.Data.Before.File != "before.mycase" || e.Data.SinceBy != "restart" || len(e.Data.Changes) != 2 ||
		len(e.Data.Readings) != 1 || e.Data.Readings[0].Code != "df.followed" {
		t.Errorf("envelope = %+v", e)
	}
	testutil.Golden(t, "json/diff.json", []byte(out))
}

// Two bundles whose host names were replaced, each with a key of its own:
// the DIMMs are still paired by their serial numbers.
func TestDiffBundles(t *testing.T) {
	pairDir(t, "moved-scan", "before.db", "after.db")
	for _, name := range []string{"before", "after"} {
		if code, out, errOut := runEnv(fixed, "open", name+".db", "bundle", "-o", name+".mycase", "--anonymize"); code != cli.ExitOK {
			t.Fatalf("bundle of %s: exit code %d\n%s%s", name, code, out, errOut)
		}
	}
	code, out, errOut := run("diff", "before.mycase", "after.mycase")
	if code != 2 || errOut != "" {
		t.Fatalf("exit code %d, stderr %q\n%s", code, errOut, out)
	}
	flat := strings.Join(strings.Fields(out), " ")
	wantLines(t, flat,
		"before.mycase: case bundle of 2026-11-13 09:30 UTC",
		"after.mycase: case bundle of 2026-11-13 09:30 UTC",
		"host names and network addresses were replaced",
		"moved S/N 880018, CPU0_C1 → CPU0_D1",
		"S/N 880018: the errors moved with the DIMM.",
		"The cases name the host differently: host-",
	)
	if strings.Contains(out, "synth-moved-dimm") || strings.Contains(out, "may be of two hosts") {
		t.Errorf("the screen of two anonymized bundles:\n%s", out)
	}
	_, out, _ = run("diff", "before.mycase", "after.db", "--json")
	if !strings.Contains(out, `"anonymized": true`) || strings.Count(out, `"anonymized"`) != 1 {
		t.Errorf("a bundle beside a ledger, as JSON:\n%s", out)
	}
}

// A vendor analyzer is asked about each case.
func TestDiffWithAnalyzer(t *testing.T) {
	pairDir(t, "moved-scan", "before.db", "after.db")
	exe := analyzer(t, "level2-only")
	code, out, errOut := run("diff", "before.db", "after.db", "--plugin", exe)
	if code != 2 || errOut != "" {
		t.Fatalf("exit code %d, stderr %q\n%s", code, errOut, out)
	}
	if !strings.HasPrefix(out, "vendor analyzer test analyzer 0.1") || strings.Count(out, "vendor analyzer") != 1 {
		t.Errorf("the screen names the analyzer %d times:\n%s", strings.Count(out, "vendor analyzer"), out)
	}
}

func TestDiffCommandLine(t *testing.T) {
	pairDir(t, "moved", "before.db", "after.db")
	if err := os.WriteFile("notes.txt", []byte("no case\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		args []string
		code int
		want string
	}{
		{nil, cli.ExitUsage, "my diff: name two cases, the earlier one first: my diff before.mycase after.mycase"},
		{[]string{"before.db"}, cli.ExitUsage, "name two cases, the earlier one first"},
		{[]string{"before.db", "after.db", "third.db"}, cli.ExitUsage, `unexpected argument "third.db"`},
		{[]string{"after.db", "before.db"}, cli.ExitUsage,
			"my diff: before.db (2026-09-28 12:00 UTC) is older than after.db (2026-11-13 00:00 UTC): name the earlier case first"},
		{[]string{"before.db", "after.db", "--at", "soon"}, cli.ExitUsage, "--at soon: want a date (2026-11-12) or a time"},
		{[]string{"before.db", "after.db", "--at", "2026-09-01"}, cli.ExitUsage,
			"--at 2026-09-01: want a time after the earlier case (2026-09-28 12:00 UTC) and up to the later one (2026-11-13 00:00 UTC)"},
		{[]string{"before.db", "after.db", "--at", "2026-12-01"}, cli.ExitUsage, "--at 2026-12-01: want a time after the earlier case"},
		{[]string{"before.db", "after.db", "--host", "elsewhere"}, cli.ExitUsage, "no host named elsewhere in the ledger (hosts: synth-moved-dimm)"},
		{[]string{"before.db", "after.db", "--ledger", "x.db"}, cli.ExitUsage, "unknown flag --ledger"},
		{[]string{"before.db", "missing.db"}, cli.ExitFailure, "my diff: open missing.db: no such file or directory"},
		{[]string{"notes.txt", "after.db"}, cli.ExitFailure, "my diff: notes.txt is neither a MemorY ledger nor a case bundle"},
		{[]string{"after.db", "before.db", "--lang", "ko"}, cli.ExitUsage, "앞선 케이스를 먼저 지정해야 함"},
	} {
		code, out, errOut := run(append([]string{"diff"}, tt.args...)...)
		if code != tt.code || out != "" || !strings.Contains(errOut, tt.want) {
			t.Errorf("my diff %v: exit code %d, stdout %q, stderr %q; want %d and %q", tt.args, code, out, errOut, tt.code, tt.want)
		}
	}
	// A wrong command line says where the usage is.
	if _, _, errOut := run("diff"); !strings.Contains(errOut, `run "my help diff" for usage`) {
		t.Errorf("stderr: %s", errOut)
	}
	// The files are read and nothing else: no file is written beside them.
	entries, err := os.ReadDir(".")
	if err != nil || len(entries) != 3 {
		t.Errorf("the directory holds %d files after the runs (%v)", len(entries), err)
	}
}

// The page about comparing quotes the output of a real run.
func TestDiffDocShowsRealOutput(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "docs", "diff.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		pair, cmd string
		code      int
	}{
		{"moved-scan", "my diff before.db after.db", 2},
		{"slot", "my diff before.db after.db --lang ko", 4},
	} {
		start := strings.Index(string(doc), "```\n$ "+tt.cmd+"\n")
		if start < 0 {
			t.Fatalf("docs/diff.md does not quote %q", tt.cmd)
		}
		block := string(doc)[start+len("```\n$ "+tt.cmd+"\n"):]
		end := strings.Index(block, "```")
		if end < 0 {
			t.Fatal("the quoted block does not end")
		}
		pairDir(t, tt.pair, "before.db", "after.db")
		code, out, errOut := run(strings.Fields(tt.cmd)[1:]...)
		if code != tt.code || errOut != "" || out != block[:end] {
			t.Errorf("exit code %d, stderr %q; docs/diff.md quotes\n%s\nthe command prints\n%s", code, errOut, block[:end], out)
		}
	}
}
