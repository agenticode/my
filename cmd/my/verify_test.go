package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/testcapture"
	"github.com/agenticode/my/internal/testutil"
)

// Every capture of a real host verifies against the plan of its
// injection: read as a copy of the host's files, and as the case bundle
// that was written on the host.
func TestVerifyCaptures(t *testing.T) {
	entries, err := os.ReadDir(testutil.Testdata(t, "real"))
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := testcapture.Dir(t, e.Name())
		plan := filepath.Join(dir, "plan.yaml")
		if _, err := os.Stat(plan); err != nil {
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			var forms [][]string
			// A capture of a loop holds the kept ledger of my harvest:
			// that is what knows every boot of the plan. A copy of the host
			// holds the files of the last boot, which internal/labcase
			// checks with what they can tell.
			if kept := filepath.Join(dir, "var", "lib", "my", "ledger.db"); fileExists(kept) {
				forms = append(forms, []string{"open", kept, "verify", "--expect", plan})
			} else if _, err := os.Stat(filepath.Join(dir, "proc")); err == nil {
				forms = append(forms, []string{"verify", "--root", dir, "--expect", plan})
			}
			if b := filepath.Join(dir, "my", "case.mycase"); fileExists(b) {
				forms = append(forms, []string{"open", b, "verify", "--expect", plan})
			}
			for _, args := range forms {
				code, out, errOut := run(args...)
				if code != cli.ExitOK || errOut != "" || strings.Contains(out, "what differs") ||
					!(strings.Contains(out, "found as planned") || strings.Contains(out, "the plan lists no injection")) {
					t.Errorf("%v: exit code %d, stderr %q\n%s", args, code, errOut, out)
				}
				checked++
			}
		})
	}
	if checked < 15 {
		t.Errorf("%d captures verified, want the copies and bundles under testdata/real", checked)
	}
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

func TestVerifyCommand(t *testing.T) {
	ledger, _ := testcapture.Ledger(t, testcapture.GHES)
	good, bad := testcapture.PlanPath(t, testcapture.GHES), testcapture.MismatchedPlan(t)

	code, out, errOut := run("open", ledger, "verify", "--expect", good)
	if code != cli.ExitOK || errOut != "" {
		t.Fatalf("exit code %d, stderr %q\n%s", code, errOut, out)
	}
	if want := string(testutil.ReadTestdata(t, "..", "internal", "term", "testdata", "verify-ok.en.txt")); out != want {
		t.Errorf("the command and the renderer differ:\n%s", out)
	}

	// A plan the records do not fit: exit code 1, and what differs.
	code, out, errOut = run("open", ledger, "verify", "--expect", bad)
	if code != exitDiffers || errOut != "" {
		t.Fatalf("exit code %d, stderr %q\n%s", code, errOut, out)
	}
	if want := string(testutil.ReadTestdata(t, "..", "internal", "term", "testdata", "verify-mismatch.en.txt")); out != want {
		t.Errorf("the command and the renderer differ:\n%s", out)
	}
	_, out, _ = run("open", ledger, "verify", "--expect", bad, "--lang", "ko", "--ascii")
	wantLines(t, out, "주입 25건 중 4건이 계획과 다름", "디코딩 불일치", "row-2: row: 주입 0x1a30, 기록 0x1a2f, 기록 3건(")

	// As JSON: the exit code is in the envelope, and no grade, because
	// the result of a verification is none.
	code, out, _ = run("open", ledger, "verify", "--expect", bad, "--json")
	var e struct {
		View     string  `json:"view"`
		Grade    *string `json:"grade"`
		ExitCode int     `json:"exit_code"`
		Host     string  `json:"host"`
		Data     struct {
			OK     bool `json:"ok"`
			Failed int  `json:"failed"`
			Checks []struct {
				ID, Result  string
				Differences []struct {
					Field, Want, Got string
					Records          []string
				}
			} `json:"checks"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &e); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if code != exitDiffers || e.View != "verify" || e.Grade != nil || e.ExitCode != exitDiffers || e.Host != "lab-arm" || e.Data.OK || e.Data.Failed != 4 || len(e.Data.Checks) != 25 {
		t.Errorf("envelope: exit code %d, %+v", code, e)
	}
	if c := e.Data.Checks[1]; c.ID != "row-2" || c.Result != "decode" || len(c.Differences) != 2 || c.Differences[0].Field != "row" || len(c.Differences[0].Records) != 3 {
		t.Errorf("row-2: %+v", c)
	}
	testutil.Golden(t, "json/verify.json", []byte(out))
}

func TestVerifyCommandLine(t *testing.T) {
	ledger, _ := testcapture.Ledger(t, testcapture.GHES)
	dir := t.TempDir()
	write := func(name, text string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	broken := write("broken.yaml", "format: 1\nscenario: x\ninjections:\n  - id: a\n    inject: error\n    severity: Loud\n    address: 0x1000\n")
	other := write("other.yaml", "format: 2\nscenario: x\ninjections: []\n")
	notPlan := write("notes.txt", "these are my notes\n")
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"verify"}, "name the injection plan with --expect, for example --expect plan.yaml"},
		{[]string{"verify", "plan.yaml"}, `unexpected argument "plan.yaml"`},
		{[]string{"verify", "--expect", filepath.Join(dir, "none.yaml")}, "none.yaml: no such file or directory"},
		{[]string{"verify", "--expect", broken}, `broken.yaml: line 4: injection "a": severity "Loud" is none of Informational, CE, Recoverable, UE, Fatal`},
		{[]string{"verify", "--expect", other}, "plan format 2 is not known to this version (it reads format 1)"},
		{[]string{"verify", "--expect", notPlan}, `notes.txt: line 1: expected "key: value"`},
	} {
		code, out, errOut := run(append([]string{"open", ledger}, tt.args...)...)
		if code != cli.ExitUsage || out != "" || !strings.Contains(errOut, tt.want) || !strings.Contains(errOut, `run "my help verify" for usage`) {
			t.Errorf("%v: exit code %d, stdout %q, stderr %q", tt.args, code, out, errOut)
		}
	}
	// Korean.
	_, _, errOut := run("open", ledger, "verify", "--lang", "ko")
	if !strings.Contains(errOut, "주입 계획을 --expect로 지정해야 함") {
		t.Errorf("stderr %q", errOut)
	}
}

// The page about verifying quotes the output of a real run on a capture.
func TestVerifyDocShowsRealOutput(t *testing.T) {
	root := testutil.RepoRoot(t)
	doc, err := os.ReadFile(filepath.Join(root, "docs", "verify.md"))
	if err != nil {
		t.Fatal(err)
	}
	const cmd = "my verify --root testdata/real/7.3.0-mce --expect testdata/real/7.3.0-mce/plan.yaml"
	start := strings.Index(string(doc), "```\n$ "+cmd+"\n")
	if start < 0 {
		t.Fatalf("docs/verify.md does not quote %q", cmd)
	}
	block := string(doc)[start+len("```\n$ "+cmd+"\n"):]
	end := strings.Index(block, "```")
	if end < 0 {
		t.Fatal("the quoted block does not end")
	}
	// The page names the capture as it lies in the repository.
	t.Chdir(root)
	code, out, errOut := run(strings.Fields(cmd)[1:]...)
	if code != cli.ExitOK || errOut != "" || out != block[:end] {
		t.Errorf("exit code %d, stderr %q; docs/verify.md quotes\n%s\nthe command prints\n%s", code, errOut, block[:end], out)
	}
}
