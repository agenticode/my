package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/testutil"
)

// The demo case of the population check: my board and my report show the
// marks on DIMM 12 and DIMM 1 of both processors and one P-ORDER-01 line
// per processor, and nothing else; the exit code is the grade of the host.
func TestPopulationDemoCase(t *testing.T) {
	root := testutil.Testdata(t, "explorer", "population-demo")
	for _, lang := range []string{"en", "ko"} {
		code, stdout, stderr := runEnv(nil, "board", "--root", root, "--lang", lang, "--width", "104")
		if code != 0 || stderr != "" {
			t.Fatalf("%s: exit %d, %s", lang, code, stderr)
		}
		if n := strings.Count(stdout, "P-ORDER-01"); n != 2 {
			t.Errorf("%s: %d P-ORDER-01 lines\n%s", lang, n, stdout)
		}
		for _, other := range []string{"P-FIRST-01", "P-CPU-01", "P-BAL-01", "P-MIX-"} {
			if strings.Contains(stdout, other) {
				t.Errorf("%s: the screen names %s\n%s", lang, other, stdout)
			}
		}
	}
	code, stdout, _ := runEnv(nil, "board", "--root", root, "--json")
	var env struct {
		ExitCode int `json:"exit_code"`
		Data     struct {
			Rows []struct {
				Parts []struct {
					Channels []struct {
						Sticks []struct {
							Slot string `json:"slot"`
							Mark string `json:"mark"`
						} `json:"sticks"`
					} `json:"channels"`
				} `json:"parts"`
			} `json:"rows"`
			Population struct {
				Set      string `json:"set"`
				Findings []struct {
					Rule   string `json:"rule"`
					Source struct {
						ID    string `json:"id"`
						Pages string `json:"pages"`
					} `json:"source"`
				} `json:"findings"`
			} `json:"population"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil || code != 0 || env.ExitCode != 0 {
		t.Fatalf("--json: exit %d, %v", code, err)
	}
	marks := map[string]string{}
	for _, r := range env.Data.Rows {
		for _, p := range r.Parts {
			for _, ch := range p.Channels {
				for _, s := range ch.Sticks {
					if s.Mark != "" {
						marks[s.Slot] = s.Mark
					}
				}
			}
		}
	}
	want := map[string]string{"PROC 1 DIMM 12": "+", "PROC 1 DIMM 1": "-", "PROC 2 DIMM 12": "+", "PROC 2 DIMM 1": "-"}
	if len(marks) != len(want) {
		t.Errorf("marks %v", marks)
	}
	for k, v := range want {
		if marks[k] != v {
			t.Errorf("%s: mark %q, want %q", k, marks[k], v)
		}
	}
	p := env.Data.Population
	if p.Set != "hp-gen9-24slot" || len(p.Findings) != 2 || p.Findings[0].Source.ID != "hpe-767927-009" || p.Findings[0].Source.Pages != "48" {
		t.Errorf("population %+v", p)
	}

	out := filepath.Join(t.TempDir(), "demo.html")
	if code, _, stderr := runEnv(nil, "report", "--root", root, "-o", out); code != 0 {
		t.Fatalf("report: exit %d, %s", code, stderr)
	}
	html, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	text := string(html)
	if n := strings.Count(text, `<span class="rule">P-ORDER-01</span>`); n != 2 {
		t.Errorf("report: %d P-ORDER-01 lines", n)
	}
	if strings.Count(text, `class="mb-mark mb-mark-fill"`) != 2+1 || strings.Count(text, `class="mb-mark mb-mark-leave"`) != 2+1 {
		t.Errorf("report: the marks of the board and of its legend are not 2 + 1 each")
	}
}

// my why names the population finding of the slot of a DIMM, and nothing
// for a DIMM no finding names; the exit code stays that of the finding.
func TestPopulationWhyLine(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "x10.mycase")
	in := testutil.Testdata(t, "explorer", "linuxhw-sys-6028r-wtr-fac06e", "dmidecode.txt")
	if code, _, stderr := runEnv(nil, "import", in, "-o", bundle); code != 0 {
		t.Fatalf("import: exit %d, %s", code, stderr)
	}
	for _, lang := range []string{"en", "ko"} {
		code, stdout, _ := runEnv(nil, "open", bundle, "why", "P1-DIMMC1", "--lang", lang, "--width", "80")
		head := map[string]string{"en": "Population: P-ORDER-01", "ko": "장착 구성: P-ORDER-01"}[lang]
		if code != 0 || !strings.Contains(stdout, head) {
			t.Errorf("%s: why P1-DIMMC1: exit %d\n%s", lang, code, stdout)
		}
		code, stdout, _ = runEnv(nil, "open", bundle, "why", "P1-DIMMC1", "--lang", lang, "--width", "80", "--detail")
		if code != 0 || !strings.Contains(stdout, head) {
			t.Errorf("%s: why --detail P1-DIMMC1: exit %d\n%s", lang, code, stdout)
		}
		code, stdout, _ = runEnv(nil, "open", bundle, "why", "P1-DIMMA1", "--lang", lang, "--width", "80")
		if code != 0 || strings.Contains(stdout, "P-ORDER-01") {
			t.Errorf("%s: why P1-DIMMA1: exit %d\n%s", lang, code, stdout)
		}
	}
	code, stdout, _ := runEnv(nil, "open", bundle, "why", "P1-DIMMC1", "--json")
	var env struct {
		Data []struct {
			Slot       string `json:"slot"`
			Population struct {
				Code string `json:"code"`
				Text string `json:"text"`
			} `json:"population"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil || code != 0 || len(env.Data) != 1 || env.Data[0].Population.Code != "pop.why" {
		t.Errorf("--json: exit %d, %v, %+v", code, err, env)
	}
}
