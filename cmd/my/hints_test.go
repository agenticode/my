package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/term"
)

// TestHintsRun: every command a screen names as the next step runs as it
// is printed. The hints of a scan keep the flags that pointed it at a
// copy or a ledger, after the command, and a slot name with a space is
// quoted for the shell.
func TestHintsRun(t *testing.T) {
	dir := t.TempDir()
	real := func(name string) string { return filepath.Join("..", "..", "testdata", "real", name) }
	ledger := filepath.Join(dir, "kept.db")
	total, ran := 0, 0
	for _, start := range [][]string{
		{"--root", real("6.19.10-ghes-loop-spread")},
		{"boots", "--root", real("6.19.10-ghes-loop-spread")},
		{"boots", "CPU0_A1", "--root", real("6.19.10-ghes-loop-spread")},
		{"--root", real("6.19.10-ghes")}, // the slot is "DIMM 0"
		{"boots", "--root", real("6.19.10-mce-loop")},
		{"--root", real("7.3.0-mce"), "--ledger", ledger},
		{"open", filepath.Join("..", "..", "examples", "demo.mycase")},
		{"open", filepath.Join("..", "..", "examples", "crash-loop.mycase"), "boots"},
	} {
		var hints []term.Hint
		shownHints = func(h []term.Hint) { hints = append(hints, h...) }
		code, _, stderr := runEnv(nil, start...)
		shownHints = nil
		if code < 0 || code > 4 {
			t.Errorf("%v: exit code %d\n%s", start, code, stderr)
			continue
		}
		total += len(hints)
		for _, h := range hints {
			args, ok := shellWords(h.Command)
			if !ok || len(args) == 0 || args[0] != "my" {
				t.Errorf("%v: hint %q is no command line", start, h.Command)
				continue
			}
			args = args[1:]
			if len(args) > 0 && args[0] == "help" {
				continue
			}
			for i, a := range args {
				if a == "case.mycase" {
					args[i] = filepath.Join(dir, "case.mycase")
				}
			}
			if strings.HasPrefix(h.Command, "my open /var/lib/my") {
				continue // the kept ledger of this machine
			}
			if hasArg(args, "bundle") {
				args = append(args, "--force")
			}
			ran++
			if code, _, stderr := runEnv(nil, args...); code == cli.ExitUsage || code == cli.ExitFailure {
				t.Errorf("%v: hint %q: exit code %d\n%s", start, h.Command, code, stderr)
			}
		}
	}
	// One hint per screen at most, and every one runs.
	if total < 7 || ran < 6 {
		t.Errorf("%d hints shown, %d run", total, ran)
	}
	// The quoting of a name with a space.
	if got := shellWord("DIMM 0"); got != "'DIMM 0'" {
		t.Errorf("shellWord: %q", got)
	}
	if got := shellWord("it's"); got != `'it'\''s'` {
		t.Errorf("shellWord: %q", got)
	}
	if w, ok := shellWords(shellWord("it's") + " " + shellWord("DIMM 0")); !ok || len(w) != 2 || w[0] != "it's" || w[1] != "DIMM 0" {
		t.Errorf("shellWords: %q", w)
	}
}

func hasArg(args []string, a string) bool {
	for _, x := range args {
		if x == a {
			return true
		}
	}
	return false
}

// shellWords splits a command line the way a POSIX shell splits words
// that are plain, in single quotes or escaped with a backslash, the forms
// shellWord writes.
func shellWords(line string) ([]string, bool) {
	var out []string
	var cur strings.Builder
	quoted, escaped, word := false, false, false
	for _, r := range line {
		switch {
		case quoted:
			if r == '\'' {
				quoted = false
			} else {
				cur.WriteRune(r)
			}
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped, word = true, true
		case r == '\'':
			quoted, word = true, true
		case r == ' ':
			if word {
				out = append(out, cur.String())
				cur.Reset()
				word = false
			}
		default:
			cur.WriteRune(r)
			word = true
		}
	}
	if quoted || escaped {
		return nil, false
	}
	if word {
		out = append(out, cur.String())
	}
	return out, true
}
