package cli

import (
	"bytes"
	"context"
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

func TestUsageListsVisibleCommandsSorted(t *testing.T) {
	_, stdout, _ := run("help")
	if strings.Contains(stdout, "secret") {
		t.Errorf("usage lists a hidden command:\n%s", stdout)
	}
	a, z := strings.Index(stdout, "alpha"), strings.Index(stdout, "zeta")
	if a < 0 || z < 0 || a > z {
		t.Errorf("usage should list alpha before zeta:\n%s", stdout)
	}
}
