package cli

import (
	"bytes"
	"context"
	"flag"
	"strings"
	"testing"
)

func testApp() *App {
	return &App{
		Name:     "my",
		Synopsis: "test app",
		Commands: []*Command{
			{Name: "zeta", Summary: "last", Run: func(context.Context, *Env, []string) int { return 7 }},
			{Name: "alpha", Summary: "first", Run: func(_ context.Context, env *Env, args []string) int {
				env.Stdout.Write([]byte(strings.Join(args, ",")))
				return ExitOK
			}},
			{Name: "secret", Summary: "hidden", Hidden: true, Run: func(context.Context, *Env, []string) int { return ExitOK }},
		},
	}
}

func run(args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	env := &Env{Stdout: &out, Stderr: &errb, Getenv: func(string) string { return "" }}
	code = testApp().Run(context.Background(), env, args)
	return code, out.String(), errb.String()
}

func TestRunDispatch(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"no arguments prints usage to stderr", nil, ExitUsage, "", "Usage:"},
		{"help prints usage to stdout", []string{"help"}, ExitOK, "Usage:", ""},
		{"long help flag", []string{"--help"}, ExitOK, "Usage:", ""},
		{"unknown command", []string{"nope"}, ExitUsage, "", `unknown command "nope"`},
		{"unknown flag", []string{"--nope"}, ExitUsage, "", "unknown flag --nope"},
		{"command receives its arguments", []string{"alpha", "a", "b"}, ExitOK, "a,b", ""},
		{"command exit code is returned", []string{"zeta"}, 7, "", ""},
		{"hidden command still runs", []string{"secret"}, ExitOK, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := run(tt.args...)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}
			if !strings.Contains(stdout, tt.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout, tt.wantStdout)
			}
			if !strings.Contains(stderr, tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr, tt.wantStderr)
			}
		})
	}
}

func TestUsageListsVisibleCommandsInOrder(t *testing.T) {
	_, stdout, _ := run("help")
	if strings.Contains(stdout, "secret") {
		t.Errorf("usage lists a hidden command:\n%s", stdout)
	}
	// The order is the order the application gives: the commands follow
	// the questions a reader asks, not the alphabet.
	a, z := strings.Index(stdout, "alpha"), strings.Index(stdout, "zeta")
	if a < 0 || z < 0 || z > a {
		t.Errorf("usage should list zeta before alpha, as declared:\n%s", stdout)
	}
}

func TestDefaultCommand(t *testing.T) {
	app := testApp()
	app.Default = &Command{Name: "scan", Run: func(_ context.Context, env *Env, args []string) int {
		env.Stdout.Write([]byte("default:" + strings.Join(args, ",")))
		return 3
	}}
	app.DefaultSummary = "scan this host"
	for _, tt := range []struct {
		args []string
		want string
		code int
	}{
		{nil, "default:", 3},
		{[]string{"--json"}, "default:--json", 3},
		{[]string{"-lang", "ko"}, "default:-lang,ko", 3},
		{[]string{"alpha", "x"}, "x", ExitOK},
	} {
		var out, errb bytes.Buffer
		env := &Env{Stdout: &out, Stderr: &errb, Getenv: func(string) string { return "" }}
		if code := app.Run(context.Background(), env, tt.args); code != tt.code || out.String() != tt.want {
			t.Errorf("Run(%q) = %d, %q; want %d, %q", tt.args, code, out.String(), tt.code, tt.want)
		}
	}
	var out, errb bytes.Buffer
	env := &Env{Stdout: &out, Stderr: &errb, Getenv: func(string) string { return "" }}
	// A word that is no command is an error, never an argument of the default.
	if code := app.Run(context.Background(), env, []string{"CPU0_C1"}); code != ExitUsage || !strings.Contains(errb.String(), "unknown command") {
		t.Errorf("exit code %d, stderr %q", code, errb.String())
	}
	out.Reset()
	app.Run(context.Background(), env, []string{"help"})
	if !strings.Contains(out.String(), "my [flags]              scan this host") {
		t.Errorf("usage does not describe the default:\n%s", out.String())
	}
}

func TestHelpOfACommand(t *testing.T) {
	app := testApp()
	app.SynopsisKO = "시험용 앱"
	app.Commands[1].SummaryKO = "첫째"
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"help", "alpha"}, "--help"},
		{[]string{"help", "alpha", "--lang", "ko"}, "--help,--lang,ko"},
		{[]string{"help", "--lang=ko", "alpha"}, "--help,--lang=ko"},
		{[]string{"help", "alpha", "--ascii", "--lang", "ko"}, "--help,--ascii,--lang,ko"},
	} {
		var out, errb bytes.Buffer
		env := &Env{Stdout: &out, Stderr: &errb, Getenv: func(string) string { return "" }}
		if code := app.Run(context.Background(), env, tt.args); code != ExitOK || out.String() != tt.want {
			t.Errorf("Run(%q) = %d, %q; want %q", tt.args, code, out.String(), tt.want)
		}
	}
	var out, errb bytes.Buffer
	env := &Env{Stdout: &out, Stderr: &errb, Getenv: func(string) string { return "" }}
	if code := app.Run(context.Background(), env, []string{"help", "nope"}); code != ExitUsage || !strings.Contains(errb.String(), `unknown command "nope"`) {
		t.Errorf("help of an unknown command: %d, %q", code, errb.String())
	}
	// The usage in Korean, by flag and by locale.
	for _, e := range []*Env{
		{Stdout: &out, Stderr: &errb, Getenv: func(string) string { return "" }},
		{Stdout: &out, Stderr: &errb, Getenv: func(k string) string { return map[string]string{"LANG": "ko_KR.UTF-8"}[k] }},
	} {
		out.Reset()
		args := []string{"help", "--lang", "ko"}
		if e.Getenv("LANG") != "" {
			args = []string{"?"}
		}
		app.Run(context.Background(), e, args)
		for _, want := range []string{"시험용 앱", "사용법:", "첫째", "last"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("Korean usage lacks %q:\n%s", want, out.String())
			}
		}
	}
}

func TestLang(t *testing.T) {
	for _, tt := range []struct {
		env  map[string]string
		want string
	}{
		{nil, "en"},
		{map[string]string{"LANG": "ko_KR.UTF-8"}, "ko"},
		{map[string]string{"LANG": "ko_KR.UTF-8", "LC_ALL": "C"}, "en"},
		{map[string]string{"LANG": "de_DE.UTF-8"}, "en"},
		{map[string]string{"LANG": "en_US.UTF-8", "MY_LANG": "ko"}, "ko"},
		{map[string]string{"LANG": "ko_KR.UTF-8", "MY_LANG": "xx"}, "ko"},
		{map[string]string{"LC_MESSAGES": "ko_KR", "LANG": "en_US"}, "ko"},
	} {
		env := &Env{Getenv: func(k string) string { return tt.env[k] }}
		if got := Lang(env); string(got) != tt.want {
			t.Errorf("Lang(%v) = %s, want %s", tt.env, got, tt.want)
		}
	}
}

func TestParse(t *testing.T) {
	for _, tt := range []struct {
		args []string
		pos  string
		bank string
		json bool
		err  bool
	}{
		{[]string{"CPU0_C1", "--bank", "3"}, "CPU0_C1", "3", false, false},
		{[]string{"--bank=3", "CPU0_C1", "--json"}, "CPU0_C1", "3", true, false},
		{[]string{"-json", "a", "b", "-bank", "0/3", "c"}, "a,b,c", "0/3", true, false},
		{[]string{"a", "--", "--json", "-x"}, "a,--json,-x", "", false, false},
		{[]string{"--", "a"}, "a", "", false, false},
		{nil, "", "", false, false},
		{[]string{"a", "--nope"}, "", "", false, true},
		{[]string{"a", "--bank"}, "", "", false, true},
	} {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.SetOutput(new(bytes.Buffer))
		bank := fs.String("bank", "", "")
		asJSON := fs.Bool("json", false, "")
		pos, err := Parse(fs, tt.args)
		if (err != nil) != tt.err {
			t.Errorf("Parse(%q): error %v", tt.args, err)
			continue
		}
		if err == nil && (strings.Join(pos, ",") != tt.pos || *bank != tt.bank || *asJSON != tt.json) {
			t.Errorf("Parse(%q) = %q, bank %q, json %v", tt.args, pos, *bank, *asJSON)
		}
	}
	for arg, want := range map[string]bool{"-h": true, "--help": true, "?": true, "-help": true, "help": false, "-x": false, "": false} {
		if IsHelp(arg) != want {
			t.Errorf("IsHelp(%q) = %v", arg, !want)
		}
	}
}
