package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/cli"
)

// TestErrorsInTheReadersLanguage: a mistake on the command line and a file
// that cannot be read are said in the language of the reader, with the
// flag as the help texts write it and without the system call that failed.
func TestErrorsInTheReadersLanguage(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "absent")
	text := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(text, []byte("# notes\nnothing here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join("..", "..", "testdata", "real", "7.3.0-mce")
	for _, tt := range []struct {
		args []string
		code int
		en   string // the first line of the error in English
		help string // the help named below it, if any
	}{
		{[]string{"nope"}, cli.ExitUsage, `my: unknown command "nope"`, ""},
		{[]string{"--frob"}, cli.ExitUsage, "my: unknown flag --frob", "my help"},
		{[]string{"--width", "abc"}, cli.ExitUsage, "my: --width abc: not a number", "my help"},
		{[]string{"dimm", "--root"}, cli.ExitUsage, "my dimm: --root needs a value", "my help dimm"},
		{[]string{"--lang", "xx"}, cli.ExitUsage, `my: unknown language "xx" (supported: en, ko)`, "my help"},
		{[]string{"--root", root, "--set", "nope=1"}, cli.ExitUsage, `my: --set: unknown threshold "nope"`, "my help"},
		{[]string{"--root", root, "--set", "storm.count=0"}, cli.ExitUsage, "my: --set: storm.count: 0 is below the minimum of 1", "my help"},
		{[]string{"--root", root, "--config", missing}, cli.ExitUsage, "my: --config " + missing + ": no such file or directory", "my help"},
		{[]string{"open", missing}, cli.ExitFailure, "my open: " + missing + ": no such file or directory", ""},
		{[]string{"open", text}, cli.ExitFailure, "my open: " + text + " is neither a MemorY ledger nor a case bundle", ""},
		{[]string{"import", missing}, cli.ExitFailure, "my import: " + missing + ": no such file or directory", ""},
		{[]string{"import", text}, cli.ExitFailure, "my import: " + text + `: not an input this tool reads: text of no known kind (it starts "# notes")`, ""},
		{[]string{"bios", "--root", root, "--profile", missing}, cli.ExitUsage, "my bios: --profile " + missing + ": no such file or directory", "my help bios"},
		{[]string{"--fleet", "oracle"}, cli.ExitUsage, `my: --fleet: unknown backend "oracle": want mysql or postgresql`, "my help"},
		{[]string{"harvest", "foo"}, cli.ExitUsage, `my harvest foo: unknown subcommand "foo": my harvest [install|uninstall|status]`, "my help harvest"},
		{[]string{"harvest", "status", "--frob"}, cli.ExitUsage, "my harvest status: unknown flag --frob", "my help harvest"},
		{[]string{"collect", "redfish", "--bmc", "https://127.0.0.1:9", "--ca-file", missing, "-o", filepath.Join(dir, "c")}, cli.ExitUsage,
			"my collect: --ca-file " + missing + ": no such file or directory", "my help collect"},
		{[]string{"collect", "redfish", "--bmc", "https://127.0.0.1:9", "--ca-file", text, "-o", filepath.Join(dir, "c")}, cli.ExitUsage,
			"my collect: --ca-file " + text + ": no certificate in PEM form found", "my help collect"},
		{[]string{"collect", "redfish", "--bmc", "https://127.0.0.1:9", "--session", "-o", filepath.Join(dir, "c")}, cli.ExitUsage,
			"my collect: --session logs in with an account: name it with --user or MY_REDFISH_USER", "my help collect"},
		{[]string{"collect", "redfish", "--bmc", "https://127.0.0.1:9", "-o", text}, cli.ExitUsage,
			"my collect: -o " + text + ": it is a file; a collection is a directory, and a case bundle has a name that ends in .mycase", "my help collect"},
	} {
		code, stdout, stderr := runEnv(nil, tt.args...)
		first, rest, _ := strings.Cut(stderr, "\n")
		if code != tt.code || stdout != "" || first != tt.en {
			t.Errorf("%v: exit code %d, stdout %q, first line %q, want %d and %q", tt.args, code, stdout, first, tt.code, tt.en)
		}
		if tt.help != "" && !strings.HasPrefix(rest, `run "`+tt.help+`" for usage`) {
			t.Errorf("%v: names the help as %q, want %q", tt.args, rest, tt.help)
		}
		// In Korean the sentence is Korean; what stays are names, paths
		// and values.
		code, _, stderr = runEnv(map[string]string{"MY_LANG": "ko"}, tt.args...)
		first, _, _ = strings.Cut(stderr, "\n")
		if code != tt.code || !hangul.MatchString(first) || english.MatchString(first) {
			t.Errorf("%v in Korean: exit code %d, %q", tt.args, code, first)
		}
	}
	// A --lang after the mistake counts: the parse stops at the mistake.
	for _, args := range [][]string{{"--frobnicate", "--lang", "ko"}, {"dimm", "--width", "x", "--lang=ko"}, {"harvest", "status", "--frob", "--lang", "ko"}} {
		code, _, stderr := runEnv(nil, args...)
		if first, _, _ := strings.Cut(stderr, "\n"); code != cli.ExitUsage || !hangul.MatchString(first) {
			t.Errorf("%v: exit code %d, %q", args, code, first)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "c")); err == nil {
		t.Error("a command line that was refused left a directory behind")
	}
}

var (
	hangul = regexp.MustCompile(`[가-힣]`)
	// Words of the English messages that a Korean one must not carry.
	english = regexp.MustCompile(`\b(unknown|no such|not a|needs|want|flag provided|invalid value|stat|open \S+:|neither|is a directory|it is)\b`)
)
