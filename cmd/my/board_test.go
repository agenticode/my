package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/testutil"
)

// my board exits with the grade of the host and writes the view board.
func TestBoardCommand(t *testing.T) {
	root := testutil.Testdata(t, "real", "6.19.10-ghes-multi")
	code, stdout, stderr := runEnv(nil, "board", "--root", root, "--lang", "en", "--width", "80")
	if code != 2 || stderr != "" || !strings.Contains(stdout, "══╡CPU 0╞══") {
		t.Fatalf("exit %d, %s\n%s", code, stderr, stdout)
	}
	code, stdout, _ = runEnv(nil, "board", "--root", root, "--json")
	var env struct {
		View     string `json:"view"`
		Grade    string `json:"grade"`
		ExitCode int    `json:"exit_code"`
		Data     struct {
			Counts struct{ Placed int } `json:"counts"`
			Rows   []json.RawMessage    `json:"rows"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil || code != 2 || env.View != "board" || env.ExitCode != 2 || env.Data.Counts.Placed != 6 || len(env.Data.Rows) != 1 {
		t.Errorf("--json: exit %d, %+v, %v", code, env, err)
	}
	code, stdout, _ = runEnv(nil, "open", testutil.Testdata(t, "real", "6.19.10-ghes-multi", "my", "case.mycase"), "board", "--lang", "ko")
	if code != 2 || !strings.Contains(stdout, "CPU 0") {
		t.Errorf("my open FILE board: exit %d\n%s", code, stdout)
	}
}
