package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/preflight"
	pt "github.com/agenticode/my/internal/preflight/preflighttest"
	"github.com/agenticode/my/internal/testutil"
)

// livePreflight points "my preflight" at a made tree as if it were the
// running host, with a fixed process list and free space.
func livePreflight(t *testing.T, f pt.Files) string {
	t.Helper()
	root := pt.Tree(t, f)
	old, oldOS := preflightProbe, hostOS
	preflightProbe = func(o *preflight.Options) {
		o.Root = root
		o.Processes = func() ([]string, error) { return []string{"systemd", "sshd"}, nil }
		o.FreeBytes = func(string) (uint64, error) { return 40 << 30, nil }
	}
	hostOS = "linux"
	t.Cleanup(func() { preflightProbe, hostOS = old, oldOS })
	return root
}

func TestPreflightScreens(t *testing.T) {
	fixed := map[string]string{"MY_NOW": "2026-11-16T03:12:00Z"}
	for _, tt := range []struct {
		golden string
		files  pt.Files
		args   []string
		code   int
	}{
		{"preflight-kept.en.txt", pt.Kept(), nil, 0},
		{"preflight-bare.en.txt", pt.Bare(), nil, exitLost},
		{"preflight-bare.ko.txt", pt.Bare(), []string{"--lang", "ko"}, exitLost},
		{"preflight-bare.ascii.txt", pt.Bare(), []string{"--ascii"}, exitLost},
		{"preflight-bare.narrow.txt", pt.Bare(), []string{"--width", "60"}, exitLost},
		{"preflight-kept.script.sh", pt.Kept(), []string{"--script"}, 0},
		{"preflight-bare.script.sh", pt.Bare(), []string{"--script"}, exitLost},
		{"preflight-bare.script.ko.sh", pt.Bare(), []string{"--script", "--lang", "ko"}, exitLost},
		{"preflight-fedora.script.sh", pt.Kept().With(pt.Files{"proc/cmdline": "ro quiet\n", "sys/module/pstore/parameters/backend": "(null)\n", "sys/kernel/kexec_crash_loaded": "0\n"}), []string{"--script"}, exitLost},
		{"json/preflight.json", pt.Bare(), []string{"--json"}, exitLost},
	} {
		t.Run(tt.golden, func(t *testing.T) {
			livePreflight(t, tt.files)
			code, out, errOut := runEnv(fixed, append([]string{"preflight"}, tt.args...)...)
			if code != tt.code || errOut != "" {
				t.Fatalf("exit code %d (want %d), stderr %q", code, tt.code, errOut)
			}
			testutil.Golden(t, tt.golden, []byte(out))
		})
	}
}

// TestPreflightCopy reads a copy of a host's files with --root: the title
// names the copy, and the checks that need the running host are marked.
func TestPreflightCopy(t *testing.T) {
	root := pt.Tree(t, pt.Bare())
	code, out, errOut := run("preflight", "--root", root)
	if code != exitLost || errOut != "" {
		t.Fatalf("exit code %d, stderr %q", code, errOut)
	}
	out = strings.ReplaceAll(out, root, "/COPY")
	testutil.Golden(t, "preflight-copy.en.txt", []byte(out))
	// This copy holds the files of every check: no check waits for the
	// running host, and none is marked.
	if strings.Contains(out, " *") || strings.Contains(out, "which a copy does not hold") || !strings.Contains(out, "my preflight --script --root /COPY") {
		t.Errorf("copy screen:\n%s", out)
	}
	// A copy without them: the checks of the running host share a line.
	_, out, _ = run("preflight", "--root", filepath.Join("..", "..", "testdata", "real", "6.19.10-mce-loop"))
	if flat := strings.Join(strings.Fields(out), " "); !strings.Contains(flat, "unknown 10 checks read the running host, which a copy does not hold: journal, pstore-backend,") ||
		strings.Contains(out, "unknown  journal") {
		t.Errorf("copy screen of a capture:\n%s", out)
	}
	// With --detail every check has its line, those of the running host
	// marked.
	_, out, _ = run("preflight", "--root", root, "--detail")
	if !strings.Contains(out, "harvest *") || !strings.Contains(out, "1 check reads the running host and is marked *") {
		t.Errorf("copy screen with --detail:\n%s", out)
	}
}

func TestPreflightJSON(t *testing.T) {
	livePreflight(t, pt.Bare())
	_, out, _ := run("preflight", "--json")
	var e struct {
		View     string `json:"view"`
		ExitCode int    `json:"exit_code"`
		Data     preflight.Report
	}
	if err := json.Unmarshal([]byte(out), &e); err != nil {
		t.Fatal(err)
	}
	if e.View != "preflight" || e.ExitCode != exitLost || len(e.Data.Checks) != len(preflight.Names) || e.Data.Lost == 0 {
		t.Errorf("envelope %+v", e)
	}
	for _, c := range e.Data.Checks {
		if c.Detail.Code == "" || c.Detail.Text == "" {
			t.Errorf("%s: statement without code or text", c.Name)
		}
	}
}

func TestPreflightCommandLine(t *testing.T) {
	livePreflight(t, pt.Bare())
	for _, tt := range []struct {
		args []string
		code int
		err  string
	}{
		{[]string{"preflight", "--script", "--json"}, cli.ExitUsage, "--script and --json"},
		{[]string{"preflight", "extra"}, cli.ExitUsage, `unexpected argument "extra"`},
		{[]string{"preflight", "--root", "/does/not/exist"}, cli.ExitUsage, "not a directory"},
		{[]string{"preflight", "--lang", "xx"}, cli.ExitUsage, "unknown language"},
		{[]string{"preflight", "--color", "red"}, cli.ExitUsage, "--color red"},
		{[]string{"preflight", "--width", "-3"}, cli.ExitUsage, "--width -3"},
		{[]string{"preflight", "--nope"}, cli.ExitUsage, "unknown flag"},
	} {
		code, _, errOut := run(tt.args...)
		if code != tt.code || !strings.Contains(errOut, tt.err) {
			t.Errorf("%v: exit %d, stderr %q", tt.args, code, errOut)
		}
	}
	hostOS = "darwin"
	if code, _, errOut := run("preflight"); code != cli.ExitFailure || !strings.Contains(errOut, "not a Linux host") {
		t.Errorf("on darwin: exit %d, stderr %q", code, errOut)
	}
}

func TestPreflightHelp(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.EN, i18n.KO} {
		code, out, errOut := run("help", "preflight", "--lang", string(lang))
		if code != cli.ExitOK || errOut != "" {
			t.Fatalf("exit %d, stderr %q", code, errOut)
		}
		for _, flag := range []string{"--script", "--root", "--ledger", "--lang", "--json", "--ascii", "--color", "--width"} {
			if !strings.Contains(out, flag) {
				t.Errorf("help (%s) lacks %s", lang, flag)
			}
		}
		if lang == i18n.KO && !strings.Contains(out, "사용법:") {
			t.Errorf("not Korean:\n%s", out)
		}
		for _, line := range strings.Split(out, "\n") {
			if w := len([]rune(line)); w > 120 {
				t.Errorf("help line too wide: %q", line)
			}
		}
	}
	_, out, _ := run("help")
	if !strings.Contains(out, "preflight ") {
		t.Errorf("the command list lacks preflight:\n%s", out)
	}
}
