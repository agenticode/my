package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/cli"
)

// What a command prints on a terminal holds text from its inputs: here a
// slot name of a dmidecode file and a label of a kernel log, both with an
// escape sequence that sets the title of the window and clears the screen,
// and a manufacturer with one that hides text. Before, the commands sent
// such text to the terminal as it was. On a terminal the sequences are
// shown, not sent; the colour codes of the figures go through.
func TestTextForATerminalIsMadeSafe(t *testing.T) {
	dir := t.TempDir()
	dmi := filepath.Join(dir, "dmidecode.txt")
	dmesg := filepath.Join(dir, "dmesg.txt")
	bundle := filepath.Join(dir, "case.mycase")
	write := func(path, text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const evil = "\x1b]0;owned\x07\x1b[2J"
	write(dmi, "# dmidecode 3.5\nHandle 0x1100, DMI type 17, 84 bytes\nMemory Device\n\tSize: 32 GB\n\tLocator: DIMM_A1"+evil+
		"\n\tBank Locator: BANK 0\n\tType: DDR4\n\tManufacturer: Example\x1b[8m Memory\n\tSerial Number: 36B2F1A0\n\tPart Number: EXM32G\n\n")
	write(dmesg, "[    0.000000] Linux version 6.1.0 (b@e) #1 SMP\n[  100.000000] EDAC MC0: 1 CE memory read error on DIMM_A1"+evil+
		" (channel:0 slot:0 page:0x1000 offset:0x40 grain:32 syndrome:0x0)\n")
	if code, _, stderr := run("import", dmi, dmesg, "-o", bundle); code != cli.ExitOK {
		t.Fatalf("import: exit %d, %s", code, stderr)
	}

	// The command writes to a pipe that stands for the terminal.
	onTerminal := func(args ...string) (out, errOut string) {
		t.Helper()
		var streams [2]string
		var files [2]*os.File
		done := make(chan struct{}, 2)
		for i := range files {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			files[i] = w
			go func() {
				b, _ := io.ReadAll(r)
				streams[i] = string(b)
				r.Close()
				done <- struct{}{}
			}()
		}
		env := &cli.Env{Stdout: files[0], Stderr: files[1], Getenv: func(string) string { return "" }}
		safeStreams(env, func(*os.File) bool { return true })
		app().Run(context.Background(), env, args)
		for _, f := range files {
			f.Close()
		}
		<-done
		<-done
		return streams[0], streams[1]
	}
	shown := false
	own := regexp.MustCompile(`\x1b\[(?:0|1|2|31|32|33|35|36)(?:;(?:0|1|2|31|32|33|35|36))*m`)
	for _, args := range [][]string{
		{"open", bundle, "--all", "--color", "always"},
		{"open", bundle, "dimm", "36B2F1A0", "--color", "always"},
		{"open", bundle, "why", "36B2F1A0"},
		{"open", bundle, "why", "36B2F1A0", "--oneline"},
		{"open", bundle, "timeline", "--color", "always"},
		{"open", bundle, "dimm", "no-such-dimm" + evil},
		{"open", filepath.Join(dir, "none"+evil+".mycase")},
		{"import", dmi, dmesg},
	} {
		out, errOut := onTerminal(args...)
		for name, text := range map[string]string{"standard output": out, "standard error": errOut} {
			rest := own.ReplaceAllString(text, "")
			if i := strings.IndexAny(rest, "\x1b\x07\r\x00\x7f"); i >= 0 {
				t.Errorf("my %s: a control character reaches the terminal on the %s: %q", strings.Join(args[:3], " "), name, rest[max(0, i-40):min(len(rest), i+40)])
			}
		}
		// Where the command prints the name at all, the sequence in it
		// is shown.
		_, plain, plainErr := run(args...)
		if printed := strings.Contains(plain+plainErr, evil); printed != strings.Contains(out+errOut, "^[]0;owned^G^[[2J") {
			t.Errorf("my %s: the name is printed (%v) and the sequence is not shown:\n%s%s", strings.Join(args[:3], " "), printed, out, errOut)
		}
		shown = shown || strings.Contains(out+errOut, "^[]0;owned^G^[[2J")
		if args[len(args)-1] == "always" && !own.MatchString(out) {
			t.Errorf("my %s: the colours of the figure are gone", strings.Join(args[:3], " "))
		}
	}

	if !shown {
		t.Error("no command printed the name with the sequence: the test checks nothing")
	}

	// Output that is no terminal is left as it is: the same command into
	// a buffer holds the text of the input byte for byte.
	if _, out, _ := run("open", bundle, "--all"); !strings.Contains(out, "DIMM_A1"+evil) {
		t.Errorf("output that goes to no terminal was changed:\n%q", out)
	}
	// So is a stream that is a file, and a buffer is no file.
	f, err := os.Create(filepath.Join(dir, "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var buf bytes.Buffer
	env := &cli.Env{Stdout: f, Stderr: &buf}
	safeStreams(env, isTerminal)
	if env.Stdout != io.Writer(f) || env.Stderr != io.Writer(&buf) || fileOf(env.Stdout) != f || fileOf(env.Stderr) != nil {
		t.Error("streams that are no terminal were wrapped")
	}
	safeStreams(env, func(*os.File) bool { return true })
	if s, ok := env.Stdout.(*screen); !ok || fileOf(s) != f || env.Stderr != io.Writer(&buf) {
		t.Errorf("a stream that is a terminal: %T", env.Stdout)
	}
}
