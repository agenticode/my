package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/bundle"
	"github.com/agenticode/my/internal/cli"
)

// A file that cannot be written is reported before the host is scanned,
// and in the words of the reader: the directory that is missing, not the
// name of a file the tool would have made on the way.
func TestOutputIsCheckedBeforeTheScan(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "absent")
	// A scan of this root fails. When the output is checked first, that
	// failure is never reached.
	noRoot := filepath.Join(dir, "no-such-root")
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"bundle", []string{"bundle", "-o", filepath.Join(missing, "case.mycase"), "--root", noRoot}, "the directory " + missing + " does not exist"},
		{"report", []string{"report", "-o", filepath.Join(missing, "r.html"), "--root", noRoot}, "the directory " + missing + " does not exist"},
		{"import", []string{"import", filepath.Join("..", "..", "testdata", "klog", "intel-skx.messages"), "-o", filepath.Join(missing, "case.mycase")}, "the directory " + missing + " does not exist"},
		{"bundle onto a directory", []string{"bundle", "-o", dir, "--root", noRoot, "--force"}, dir + " is a directory; name a file"},
		{"report onto a directory", []string{"report", "-o", dir, "--root", noRoot}, dir + " is a directory; name a file"},
		{"bundle in Korean", []string{"bundle", "-o", filepath.Join(missing, "case.mycase"), "--root", noRoot, "--lang", "ko"}, missing + " 디렉터리가 없음"},
		{"ledger on a device", []string{"--ledger", os.DevNull, "--root", noRoot}, "--ledger " + os.DevNull + " is not a regular file"},
		{"ledger in a missing directory", []string{"why", "--ledger", filepath.Join(missing, "host.db"), "--root", noRoot}, "--ledger the directory " + missing + " does not exist"},
	} {
		code, stdout, stderr := runEnv(fixed, tc.args...)
		if code != cli.ExitUsage || stdout != "" || !strings.Contains(stderr, tc.want) {
			t.Errorf("%s: exit code %d, stdout %q, stderr %q; want exit %d and %q", tc.name, code, stdout, stderr, cli.ExitUsage, tc.want)
		}
		if strings.Contains(stderr, ".part") || strings.Contains(stderr, ".my-") {
			t.Errorf("%s: the message names a file of the tool: %q", tc.name, stderr)
		}
	}
	if got, _ := os.ReadDir(dir); len(got) != 0 {
		t.Errorf("files were made on the way: %v", got)
	}
}

// "-o -" sends the bundle to the standard output and everything said about
// it to the standard error: "ssh HOST sudo my bundle -o - > case.mycase"
// leaves nothing on the host.
func TestBundleToStandardOutput(t *testing.T) {
	root := hostTree(t)
	dir := t.TempDir()
	for _, tc := range []struct {
		name string
		args []string
		says string
	}{
		{"bundle", []string{"bundle", "CPU0_C1", "-o", "-", "--root", root}, "  host r12-n034: 16 DIMMs, CE 1,284, UE 1, finding: replace now"},
		{"import", []string{"import", root, "-o", "-"}, "  host r12-n034, finding: replace now"},
	} {
		code, stdout, stderr := runEnv(fixed, tc.args...)
		if code != cli.ExitOK {
			t.Fatalf("%s: exit code %d, stderr %q", tc.name, code, stderr)
		}
		file := filepath.Join(dir, tc.name+".mycase")
		if err := os.WriteFile(file, []byte(stdout), 0o600); err != nil {
			t.Fatal(err)
		}
		m, err := bundle.ReadManifest(file)
		if err != nil {
			t.Fatalf("%s: the standard output is not a bundle: %v", tc.name, err)
		}
		if len(m.Hosts) != 1 || m.Hosts[0].Name != "r12-n034" {
			t.Errorf("%s: manifest hosts %+v", tc.name, m.Hosts)
		}
		wantLines(t, stderr, "case bundle written to the standard output (", tc.says)
		if strings.Contains(stderr, "look at it") {
			t.Errorf("%s: names a file to open where there is none:\n%s", tc.name, stderr)
		}
		// The receiver opens what arrived.
		if code, out, errOut := runEnv(fixed, "open", file, "why", "--oneline"); code != 3 || !strings.Contains(out, "replace now") {
			t.Errorf("%s: my open of what arrived: exit code %d, %q, %q", tc.name, code, out, errOut)
		}
	}
}

// A device under the name of the output is written to and stays what it
// is. Moved onto it, a file would take /dev/null away from the host.
func TestDeviceIsWrittenToNotReplaced(t *testing.T) {
	before, err := os.Stat(os.DevNull)
	if err != nil || before.Mode()&fs.ModeDevice == 0 {
		t.Skip("no null device")
	}
	root := hostTree(t)
	for _, args := range [][]string{
		{"bundle", "-o", os.DevNull, "--root", root},
		{"bundle", "-o", os.DevNull, "--root", root, "--force"},
		{"report", "-o", os.DevNull, "--root", root, "--force"},
		{"import", root, "-o", os.DevNull, "--force"},
	} {
		code, stdout, stderr := runEnv(fixed, args...)
		if code != cli.ExitOK {
			t.Errorf("%v: exit code %d, stderr %q", args, code, stderr)
		}
		if strings.Contains(stdout, "look at it") {
			t.Errorf("%v: names a device as the file to open:\n%s", args, stdout)
		}
		after, err := os.Stat(os.DevNull)
		if err != nil || after.Mode()&fs.ModeDevice == 0 || !os.SameFile(before, after) {
			t.Fatalf("%v: %s is no longer the device it was", args, os.DevNull)
		}
	}
}

// A host with nothing to draw gets no figures, no empty directory, and a
// line that says so.
func TestFiguresOfAHostWithNothingToDraw(t *testing.T) {
	root := t.TempDir()
	write := func(rel, text string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("proc/sys/kernel/hostname", "quiet\n")
	write("var/log/messages", "Nov 12 03:14:00 quiet kernel: Linux version 5.14.0-570.el9.x86_64 (gcc)\n")
	dir := filepath.Join(t.TempDir(), "figures")
	code, stdout, stderr := runEnv(fixed, "report", "--figures", dir, "--root", root)
	if code != cli.ExitOK || !strings.Contains(stdout, "no figure written") {
		t.Errorf("exit code %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Errorf("an empty directory %s was made", dir)
	}
}
