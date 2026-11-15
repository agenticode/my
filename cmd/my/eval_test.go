package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/eval"
	"github.com/agenticode/my/internal/testutil"
)

func TestEvalIsHidden(t *testing.T) {
	if _, usage, _ := run("help"); strings.Contains(usage, "eval") {
		t.Errorf("the command list shows the hidden eval command:\n%s", usage)
	}
	code, _, errOut := run("eval", "-h")
	if code != cli.ExitOK || !strings.Contains(errOut, "Usage: my eval -data dir|zip") || !strings.Contains(errOut, "-cutoffs") {
		t.Errorf("exit code %d, help:\n%s", code, errOut)
	}
}

func TestEvalGenerateAndRun(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fleet")
	code, out, errOut := run("eval", "-generate", dir, "-seed", "3", "-servers", "150")
	if code != cli.ExitOK || !strings.Contains(out, " servers and ") || !strings.Contains(out, dir) {
		t.Fatalf("exit code %d, stdout %q, stderr %q", code, out, errOut)
	}
	for _, f := range []string{"mcelog.csv", "inventory.csv", "trouble_tickets.csv"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatal(err)
		}
	}

	scratch := t.TempDir()
	code, out, errOut = run("eval", "-data", dir, "-scratch", scratch)
	if code != cli.ExitOK {
		t.Fatalf("exit code %d, stderr %s", code, errOut)
	}
	for _, want := range []string{
		"MemorY evaluation on the dcbrain DRAM error dataset",
		"thresholds: defaults",
		"servers with errors: 150;",
		"Findings and what followed, held-out period (cut-off days 150, 180, 210)",
		"Finding for a failed server at times before its ticket",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// Progress goes to standard error and can be switched off.
	if !strings.Contains(errOut, "my eval: ") {
		t.Errorf("no progress on standard error: %q", errOut)
	}
	if _, _, quiet := run("eval", "-data", dir, "-scratch", scratch, "-quiet"); quiet != "" {
		t.Errorf("-quiet still prints: %q", quiet)
	}
	if left, _ := os.ReadDir(scratch); len(left) != 0 {
		t.Errorf("temporary files were left behind: %v", left)
	}

	// JSON, other cut-offs, a changed threshold, degraded records.
	code, out, errOut = run("eval", "-data", dir, "-scratch", scratch, "-quiet", "-format", "json", "-cutoffs", "100, 50", "-horizon", "20",
		"-split", "90", "-leads", "1s,2h", "-set", "storm.count=50", "-degrade", "b", "-workers", "1")
	if code != cli.ExitOK {
		t.Fatalf("exit code %d, stderr %s", code, errOut)
	}
	var rep eval.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if len(rep.ByCut) != 2 || rep.ByCut[0].Day != 50 || rep.ByCut[0].HeldOut || !rep.ByCut[1].HeldOut || rep.HorizonDays != 20 ||
		len(rep.Leads) != 2 || rep.Degrade != "B" || len(rep.Thresholds) != 1 || rep.Thresholds[0] != [2]string{"storm.count", "50"} {
		t.Errorf("report = %+v", rep)
	}
	if rep.Census.GradeErrors[0] != 0 || rep.Census.GradeErrors[1] != rep.Dataset.Rows {
		t.Errorf("errors by grade after degrading: %v", rep.Census.GradeErrors)
	}

	// Markdown.
	code, out, _ = run("eval", "-data", dir, "-scratch", scratch, "-quiet", "-format", "md", "-spool", filepath.Join(scratch, "spool"))
	if code != cli.ExitOK || !strings.Contains(out, "### Dataset") || !strings.Contains(out, "| --- | ---: |") {
		t.Errorf("exit code %d, Markdown:\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(scratch, "spool", "spool.json")); err != nil {
		t.Errorf("-spool did not keep the spool: %v", err)
	}
}

func TestEvalExtract(t *testing.T) {
	data := testutil.Testdata(t, "dcbrain")
	ledger := filepath.Join(t.TempDir(), "s0002.db")
	code, out, errOut := run("eval", "-data", data, "-extract", "s0002", "-ledger", ledger)
	if code != cli.ExitOK {
		t.Fatalf("exit code %d, stderr %s", code, errOut)
	}
	for _, want := range []string{"dcbrain dcbrain#s0002: ok, 88 rows, 88 records", "inventory: 24 DIMMs of DRAM model B1", "failure ticket at 0001-03-11 03:14:07", "my analyze -ledger"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// The ledger is one the other commands read: the day before the
	// ticket the row fault asks for a replacement.
	code, out, errOut = run("analyze", "-ledger", ledger, "-as-of", "2001-03-12")
	if code != 2 || !strings.Contains(out, "DIMM0") || !strings.Contains(out, "R-ROW-03") {
		t.Errorf("analyze: exit code %d, stderr %s, stdout:\n%s", code, errOut, out)
	}

	if code, _, errOut = run("eval", "-data", data, "-extract", "nobody", "-ledger", filepath.Join(t.TempDir(), "x.db")); code != cli.ExitFailure || !strings.Contains(errOut, "no record of server nobody") {
		t.Errorf("unknown server: exit code %d, stderr %s", code, errOut)
	}
}

func TestEvalUsageErrors(t *testing.T) {
	data := testutil.Testdata(t, "dcbrain")
	cases := []struct {
		args []string
		code int
		want string
	}{
		{[]string{"eval"}, cli.ExitUsage, "give the dataset with -data"},
		{[]string{"eval", "extra"}, cli.ExitUsage, `unexpected argument "extra"`},
		{[]string{"eval", "-nope"}, cli.ExitUsage, "flag provided but not defined"},
		{[]string{"eval", "-data", filepath.Join(t.TempDir(), "missing")}, cli.ExitFailure, "no such file"},
		{[]string{"eval", "-data", t.TempDir()}, cli.ExitFailure, "no error table"},
		{[]string{"eval", "-data", data, "-format", "yaml"}, cli.ExitUsage, "want text, md or json"},
		{[]string{"eval", "-data", data, "-cutoffs", "60,x"}, cli.ExitUsage, "is not a day number"},
		{[]string{"eval", "-data", data, "-cutoffs", "60,70"}, cli.ExitUsage, "closer than the outcome window"},
		{[]string{"eval", "-data", data, "-cutoffs", "400"}, cli.ExitUsage, "outside 1 to 243"},
		{[]string{"eval", "-data", data, "-leads", "soon"}, cli.ExitUsage, "is not a duration"},
		{[]string{"eval", "-data", data, "-leads", "1ms"}, cli.ExitUsage, "shorter than a second"},
		{[]string{"eval", "-data", data, "-degrade", "A"}, cli.ExitUsage, "want B or C"},
		{[]string{"eval", "-data", data, "-horizon", "0"}, cli.ExitUsage, "at least 1 day"},
		{[]string{"eval", "-data", data, "-split", "0"}, cli.ExitUsage, "want a day"},
		{[]string{"eval", "-data", data, "-max-records", "0"}, cli.ExitUsage, "at least 1"},
		{[]string{"eval", "-data", data, "-set", "nope=1"}, cli.ExitUsage, "unknown threshold"},
		{[]string{"eval", "-data", data, "-extract", "s0001"}, cli.ExitUsage, "needs the ledger"},
		{[]string{"eval", "-data", data, "-ledger", "x.db"}, cli.ExitUsage, "-ledger goes with -extract"},
		{[]string{"eval", "-data", data, "-server", "nobody", "-quiet"}, cli.ExitFailure, `no row of server "nobody"`},
		{[]string{"eval", "-generate", t.TempDir(), "-data", data}, cli.ExitUsage, "-generate goes with neither"},
		{[]string{"eval", "-generate", t.TempDir(), "-servers", "0"}, cli.ExitUsage, "want 1 to 1000000"},
	}
	for _, c := range cases {
		code, _, errOut := run(c.args...)
		if code != c.code || !strings.Contains(errOut, c.want) {
			t.Errorf("my %s: exit code %d, stderr %q; want %d and %q", strings.Join(c.args, " "), code, errOut, c.code, c.want)
		}
	}
}

// docExcerpts returns the parts of an evaluation report that
// docs/evaluation.md quotes: the findings of the development period up to
// the first line about precision, and the findings before the tickets for
// an uncorrectable error.
func docExcerpts(report string) []string {
	lines := strings.Split(report, "\n")
	cut := func(first string, last func(string) bool) string {
		for i, l := range lines {
			if !strings.HasPrefix(l, first) {
				continue
			}
			for j := i + 1; j < len(lines); j++ {
				if last(lines[j]) {
					return strings.Join(lines[i:j+1], "\n") + "\n"
				}
			}
		}
		return ""
	}
	return []string{
		cut("Findings and what followed, development period", func(l string) bool {
			return strings.HasPrefix(l, "  consider replacement, against a ticket of type 1, 2 or 3")
		}),
		cut("  Servers with a ticket of failure type 1 ", func(l string) bool { return strings.HasPrefix(l, "    30 days ") }),
	}
}

// TestEvalDocExcerpt keeps the report excerpts of docs/evaluation.md equal
// to what the command prints for the made-up fleet the page names.
// "make golden" rewrites them after an intended change.
func TestEvalDocExcerpt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fleet")
	if code, _, errOut := run("eval", "-generate", dir); code != cli.ExitOK {
		t.Fatalf("exit code %d, stderr %s", code, errOut)
	}
	code, out, errOut := run("eval", "-data", dir, "-quiet", "-scratch", t.TempDir())
	if code != cli.ExitOK {
		t.Fatalf("exit code %d, stderr %s", code, errOut)
	}
	want := docExcerpts(out)
	for i, w := range want {
		if w == "" {
			t.Fatalf("the report has no part %d to quote:\n%s", i, out)
		}
	}
	path := filepath.Join(testutil.RepoRoot(t), "docs", "evaluation.md")
	doc, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The quoted parts are the fenced blocks that start like a part.
	text := string(doc)
	starts := []string{"```\nFindings and what followed, development period", "```\n  Servers with a ticket of failure type 1 "}
	changed := false
	for i, start := range starts {
		at := strings.Index(text, start)
		if at < 0 {
			t.Fatalf("%s has no block that starts with %q", path, start)
		}
		body := at + len("```\n")
		end := strings.Index(text[body:], "```")
		if end < 0 {
			t.Fatalf("%s: the block that starts with %q is not closed", path, start)
		}
		if got := text[body : body+end]; got != want[i] {
			if os.Getenv("UPDATE_GOLDEN") == "" {
				t.Errorf("%s quotes a report that the command no longer prints (run \"make golden\"):\n--- quoted\n%s--- printed\n%s", path, got, want[i])
				continue
			}
			text = text[:body] + want[i] + text[body+end:]
			changed = true
		}
	}
	if changed {
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
