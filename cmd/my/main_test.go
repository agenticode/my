package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/cli"
)

func TestVersionCommand(t *testing.T) {
	var out, errb bytes.Buffer
	env := &cli.Env{Stdout: &out, Stderr: &errb, Getenv: func(string) string { return "" }}
	if code := app().Run(context.Background(), env, []string{"version"}); code != cli.ExitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, errb.String())
	}
	if !strings.HasPrefix(out.String(), "my ") {
		t.Errorf("version output = %q, want it to start with %q", out.String(), "my ")
	}
}
