package main

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/memras"
	"github.com/agenticode/my/internal/term"
	"github.com/agenticode/my/internal/testutil"
	"github.com/agenticode/my/internal/view"
)

// bmcCase makes a case from one of the Redfish fixture trees.
func bmcCase(t *testing.T, tree string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), tree+".mycase")
	code, stdout, stderr := runEnv(fixed, "import", testutil.Testdata(t, "redfish", tree), "-o", file)
	if code != cli.ExitOK {
		t.Fatalf("import of %s: exit code %d\n%s\n%s", tree, code, stdout, stderr)
	}
	return file
}

// The settings check on the three fixture trees, as text in both
// languages. The golden files are what a reader sees.
func TestBIOSScreens(t *testing.T) {
	for _, tree := range []string{"dell-shaped", "hpe-shaped", "lenovo-shaped"} {
		file := bmcCase(t, tree)
		for _, lang := range []string{"en", "ko"} {
			code, stdout, stderr := runEnv(fixed, "open", file, "bios", "--lang", lang)
			if code != cli.ExitOK || stderr != "" {
				t.Fatalf("%s (%s): exit code %d, stderr %q", tree, lang, code, stderr)
			}
			// The first line names the case file, which lies in a
			// directory of this run.
			_, body, _ := strings.Cut(stdout, "\n")
			testutil.Golden(t, "bios-"+tree+"."+lang+".txt", []byte(body))
			for _, line := range strings.Split(body, "\n") {
				if strings.Contains(line, "https://") {
					continue // a URL is not broken
				}
				if term.Width(line) > term.DefaultWidth {
					t.Errorf("%s (%s): line wider than %d columns: %q", tree, lang, term.DefaultWidth, line)
				}
			}
		}
	}
}

func TestBIOSAsJSON(t *testing.T) {
	file := bmcCase(t, "dell-shaped")
	code, stdout, stderr := runEnv(fixed, "open", file, "bios", "--json")
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("exit code %d, stderr %q", code, stderr)
	}
	var e struct {
		View     string
		Version  int
		Host     string
		Grade    *string
		ExitCode int `json:"exit_code"`
		Data     view.BIOS
	}
	if err := json.Unmarshal([]byte(stdout), &e); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	// The exit code is no grade.
	if e.View != "bios" || e.Version != jsonVersion || e.Host != "node-d" || e.Grade != nil || e.ExitCode != 0 {
		t.Errorf("envelope: %+v", e)
	}
	d := e.Data
	if d.None || d.Mismatches != 0 || d.PendingMismatches != 1 || len(d.Rows) != len(memras.Settings) || d.Check == nil || d.Check.Vendor != "Dell" ||
		d.Machine != "Dell Inc. PowerEdge R740" || d.BIOSVersion != "2.12.2" || d.BMC == nil || d.BMC.Firmware != "4.40.00.00" || d.BMC.Live {
		t.Errorf("data: %+v", d)
	}
	row := d.Rows[0]
	if row.Setting != memras.PatrolScrub || row.Status != memras.StatusOK || row.State != "on" || row.Attribute != "MemPatrolScrub" || row.Value != "Standard" ||
		row.Expected != "on" || len(row.Lines) != 3 || row.Lines[1].Code != "line.pending" {
		t.Errorf("first row: %+v", row)
	}
	if it := d.Check.Item(memras.PatrolScrub); it == nil || it.Source == nil || !strings.HasPrefix(it.Source.URL, "https://dl.dell.com/") {
		t.Errorf("source of the mapping: %+v", it)
	}
	testutil.Golden(t, "json/bios.json", []byte(stdout))
}

func TestBIOSProfiles(t *testing.T) {
	file := bmcCase(t, "dell-shaped")

	// The built-in profile, as it is written.
	code, stdout, stderr := run("bios", "--print-profile")
	if code != cli.ExitOK || stderr != "" || stdout != string(memras.BuiltinText()) {
		t.Errorf("--print-profile: exit code %d, stderr %q, %d bytes", code, stderr, len(stdout))
	}

	// A profile of one's own: it expects another refresh rate and a memory
	// frequency without a limit, and nothing of patrol scrub.
	own := strings.Replace(stdout, "expect:\n  patrol-scrub: on\n  ce-logging: on\n", "expect:\n  refresh-rate: 2x\n  memory-frequency: auto\n  ppr: on\nwhy:\n  refresh-rate: our hot aisle\n", 1)
	own = strings.Replace(own, "name: built-in", "name: fleet-a", 1)
	path := filepath.Join(t.TempDir(), "fleet-a.yaml")
	if err := os.WriteFile(path, []byte(own), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runEnv(fixed, "open", file, "bios", "--profile", path)
	if code != exitDiffers || stderr != "" {
		t.Fatalf("own profile: exit code %d, stderr %q\n%s", code, stderr, stdout)
	}
	flat := strings.Join(strings.Fields(stdout), " ")
	wantLines(t, flat,
		"profile fleet-a, mapping for Dell (by the system manufacturer)",
		"patrol scrub on - reported MemPatrolScrub = Standard",
		"memory frequency limited not limited not as expected MemFrequency = 2666MHz",
		"refresh rate 1x 2x not as expected MemRefreshRate = 1x",
		"expected 2x: our hot aisle",
		// The attribute the profile maps for PPR is not in this BIOS: an
		// open question, not a setting that is wrong.
		"post package repair - on not in this BIOS PostPackageRepair the settings of this BIOS on record do not list the attribute",
		"2 of 2 expectations of the profile are not met")

	var e struct {
		ExitCode int `json:"exit_code"`
		Data     view.BIOS
	}
	code, stdout, _ = runEnv(fixed, "open", file, "bios", "--profile", path, "--json")
	if err := json.Unmarshal([]byte(stdout), &e); err != nil || code != exitDiffers || e.ExitCode != exitDiffers || e.Data.Mismatches != 2 {
		t.Errorf("own profile as JSON: exit code %d, %v, mismatches %d", code, err, e.Data.Mismatches)
	}

	// Mistakes.
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(bad, []byte("format: 1\nexpect:\n  patrol-scrub: maybe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"open", file, "bios", "--profile", filepath.Join(t.TempDir(), "missing.yaml")}, "--profile"},
		{[]string{"open", file, "bios", "--profile", bad}, "line 3: expect patrol-scrub: \"maybe\" is not one of on, off"},
		{[]string{"open", file, "bios", "extra"}, `unexpected argument "extra"`},
	} {
		code, stdout, stderr := runEnv(fixed, tt.args...)
		if code != cli.ExitUsage || stdout != "" || !strings.Contains(stderr, tt.want) {
			t.Errorf("%v: exit code %d, stdout %q, stderr %q", tt.args, code, stdout, stderr)
		}
	}
}

// A ledger without BIOS settings: the command says where they come from,
// and its exit code is not that of a setting that is wrong.
func TestBIOSWithoutSettings(t *testing.T) {
	code, stdout, stderr := runEnv(fixed, "open", demo(t), "bios")
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("exit code %d, stderr %q", code, stderr)
	}
	wantLines(t, strings.Join(strings.Fields(stdout), " "), "No BIOS settings are on record for this host.", "my collect redfish --bmc ADDRESS -o DIR")
	if strings.Contains(stdout, "SETTING") {
		t.Errorf("a table without settings:\n%s", stdout)
	}
	code, stdout, _ = runEnv(fixed, "open", demo(t), "bios", "--json")
	var e struct {
		Data view.BIOS
	}
	if err := json.Unmarshal([]byte(stdout), &e); err != nil || code != cli.ExitOK || !e.Data.None || e.Data.Check == nil || e.Data.Check.Settings != 0 {
		t.Errorf("as JSON: exit code %d, %v, %+v", code, err, e.Data)
	}
	// On a copy of a host's files too.
	code, stdout, _ = runEnv(fixed, "bios", "--root", hostTree(t), "--lang", "ko")
	if code != cli.ExitOK || !strings.Contains(stdout, "BIOS 설정 기록이 없습니다") {
		t.Errorf("--root: exit code %d\n%s", code, stdout)
	}
}

func TestBIOSHelp(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.EN, i18n.KO} {
		code, out, errOut := run("help", "bios", "--lang", string(lang))
		if code != cli.ExitOK || errOut != "" {
			t.Fatalf("exit code %d, stderr %q", code, errOut)
		}
		flat := strings.Join(strings.Fields(out), " ")
		if lang == i18n.EN {
			wantLines(t, flat, "Usage: my bios [--profile FILE] [flags]", "The command changes no setting.",
				"the expectation does not fit this BIOS version; that is said and is no mismatch",
				"The exit code is 0 when every expectation is met", "my open case.mycase bios")
			if strings.Contains(flat, "Exit code: the grade of what was shown") {
				t.Errorf("the help speaks of grades:\n%s", out)
			}
		} else if !strings.Contains(out, "사용법:") || !strings.Contains(out, "옵션:") || !strings.Contains(out, "어떤 설정도 바꾸지 않습니다") {
			t.Errorf("not Korean:\n%s", out)
		}
		o := newOptions(&cli.Env{Getenv: func(string) string { return "" }}, "my", nil)
		o.fs.String("profile", "", "")
		o.fs.Bool("print-profile", false, "")
		known := map[string]bool{"--help": true}
		o.fs.VisitAll(func(f *flag.Flag) {
			if f.Name == "h" || f.Name == "help" {
				return
			}
			known["--"+f.Name] = true
			if !strings.Contains(out, "--"+f.Name) {
				t.Errorf("help of bios (%s) lacks --%s", lang, f.Name)
			}
		})
		for _, f := range flagRE.FindAllString(out, -1) {
			if !known[f] {
				t.Errorf("help of bios (%s) names %s, which the command does not take", lang, f)
			}
		}
		for _, line := range strings.Split(out, "\n") {
			if term.Width(line) > term.DefaultWidth {
				t.Errorf("help of bios (%s): line wider than %d columns: %q", lang, term.DefaultWidth, line)
			}
		}
	}
	if _, out, _ := run("help"); !strings.Contains(out, "bios ") {
		t.Errorf("the command list lacks bios:\n%s", out)
	}
	for code, m := range biosWords {
		if m.EN == "" || m.KO == "" {
			t.Errorf("%s lacks a language", code)
		}
	}
}
