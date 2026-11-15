package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/bundle"
	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/scan"
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

// A scan that read nothing that could have shown an error does not answer
// "no errors on record". It ends with what it looked for and exit code 70:
// a directory that holds nothing of a host, a system that is not Linux, a
// user who may read no source.
func TestScanThatCanReadNothing(t *testing.T) {
	empty := t.TempDir()
	dir := t.TempDir()
	keep := filepath.Join(dir, "host.db")
	for _, args := range [][]string{
		{"--root", empty},
		{"why", "--root", empty},
		{"timeline", "--root", empty, "--json"},
		{"bundle", "--root", empty, "-o", filepath.Join(dir, "case.mycase")},
		{"report", "--root", empty, "-o", filepath.Join(dir, "r.html")},
		{"--root", empty, "--ledger", keep},
	} {
		code, stdout, stderr := runEnv(fixed, args...)
		if code != cli.ExitFailure || stdout != "" {
			t.Errorf("%v: exit code %d, stdout %q", args, code, stdout)
		}
		wantLines(t, stderr, empty+" holds no source of error records of a host, so there is nothing to scan:",
			"  rasdaemon: not present", "  klog: not present (no kernel log file found", "  edac-sysfs: not present")
		if strings.Contains(stderr, "hostinfo") || strings.Contains(stderr, "smbios") || strings.Contains(stderr, "sudo") {
			t.Errorf("%v: the message lists what holds no error, or names root for a copy:\n%s", args, stderr)
		}
	}
	// Nothing was written, and no ledger was left for a scan of nothing.
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("files left by scans that read nothing: %v", left)
	}
	// In Korean.
	_, _, stderr := runEnv(fixed, "--root", empty, "--lang", "ko")
	wantLines(t, stderr, empty+"에 호스트의 오류 기록 소스가 없어 스캔할 것이 없음:", "  rasdaemon: 없음")

	// A ledger that was there before is kept as it was found: readable.
	root := hostTree(t)
	if code, _, stderr := runEnv(fixed, "--root", root, "--ledger", keep); code != 3 {
		t.Fatalf("scan into a ledger: exit code %d, %s", code, stderr)
	}
	if code, _, _ := runEnv(fixed, "--root", empty, "--ledger", keep); code != cli.ExitFailure {
		t.Errorf("a scan of nothing into a ledger: exit code %d", code)
	}
	if code, out, stderr := runEnv(fixed, "open", keep, "why", "--oneline"); code != 3 || !strings.Contains(out, "replace now") {
		t.Errorf("the ledger after a scan of nothing: exit code %d, %q, %q", code, out, stderr)
	}

	// A system that is not Linux: the scan of this host is not tried.
	saved := hostOS
	defer func() { hostOS = saved }()
	hostOS = "plan9"
	code, stdout, stderr := runEnv(fixed)
	if code != cli.ExitFailure || stdout != "" || !strings.Contains(stderr, "my: this is not a Linux host (plan9).") || !strings.Contains(stderr, `"my open FILE"`) {
		t.Errorf("not Linux: exit code %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	// A copy of a host's files is read on any system.
	if code, _, stderr := runEnv(fixed, "--root", root); code != 3 {
		t.Errorf("a copy of a host on another system: exit code %d, %s", code, stderr)
	}
	hostOS = saved

	// What the message says on a live host, by who ran the scan.
	sources := []model.Source{
		{Kind: "hostinfo", Status: model.SourceOK},
		{Kind: "rasdaemon", Status: model.SourceAbsent, Detail: "no rasdaemon database"},
		{Kind: "klog", Status: model.SourceUnreadable, Detail: "the journal shows this user no kernel messages"},
		{Kind: "edac-sysfs", Status: model.SourceEmpty, Detail: "no memory controller is registered"},
		{Kind: "smbios", Status: model.SourceUnreadable, Detail: "permission denied"},
	}
	user := blindText(i18n.EN, &scan.Options{Root: "/", Live: true}, sources)
	wantLines(t, user, "no source of error records can be read as this user, so the scan cannot say whether this host has memory errors:",
		"\n  rasdaemon: not present (no rasdaemon database)", "\n  klog: not readable (the journal shows this user no kernel messages)",
		"\n  edac-sysfs: no records (no memory controller is registered)", "\nAs root the scan reads the kernel log and the rasdaemon database: sudo my")
	if strings.Contains(user, "smbios") || strings.Contains(user, "hostinfo") {
		t.Errorf("the message lists a source that holds no error:\n%s", user)
	}
	if asRoot := blindText(i18n.EN, &scan.Options{Root: "/", Live: true, Privileged: true}, sources); strings.Contains(asRoot, "sudo") ||
		!strings.HasPrefix(asRoot, "no source of error records can be read on this host, so the scan cannot say whether it has memory errors:") {
		t.Errorf("as root:\n%s", asRoot)
	}
	if ko := blindText(i18n.KO, &scan.Options{Root: "/", Live: true}, sources); !strings.Contains(ko, "이 사용자로는 오류 기록 소스를 하나도 읽지 못해") ||
		!strings.Contains(ko, "klog: 읽지 못함") || !strings.Contains(ko, "sudo my") {
		t.Errorf("Korean:\n%s", ko)
	}
}
