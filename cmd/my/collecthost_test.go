package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/scan"
	"github.com/agenticode/my/internal/testutil"
)

// hostRoot copies the 6-DIMM capture as the root of a host, with its
// kernel log as records of /dev/kmsg and stand-in programs that print what
// the host would print.
func hostRoot(t *testing.T) (root, bin, log string) {
	t.Helper()
	capture := testutil.Testdata(t, "real", "6.19.10-ghes-multi")
	root = filepath.Join(t.TempDir(), "host")
	for _, rel := range []string{"proc", "sys", "var"} {
		copyTree(t, filepath.Join(capture, rel), filepath.Join(root, rel))
	}
	bin = t.TempDir()
	log = filepath.Join(t.TempDir(), "ran")
	prog := func(name, script string) string {
		p := filepath.Join(bin, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\necho \""+name+" $*\" >> "+log+"\n"+script), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	collectHostPrograms = map[string]string{
		"dmidecode": prog("dmidecode", "cat "+filepath.Join(capture, "dmidecode.txt")+"\n"),
		"lshw":      prog("lshw", "printf '  *-memory\\n       description: System memory\\n'\n"),
		"numactl":   prog("numactl", "printf 'available: 1 nodes (0)\\nnode 0 size: 98304 MB\\nnode 0 free: 90000 MB\\n'\n"),
		"numastat":  prog("numastat", "printf '                           node0\\nnuma_hit                 1234\\n'\n"),
		"sar":       prog("sar", "echo 'Cannot open /var/log/sa/sa17: No such file or directory' >&2; exit 1\n"),
		"mcelog":    "-",
		"sudo":      prog("sudo", "shift; exec \"$@\"\n"),
	}
	collectHostRoot = root
	dmesg, err := os.ReadFile(filepath.Join(capture, "dmesg"))
	if err != nil {
		t.Fatal(err)
	}
	// The root keeps the kernel log where a scan of it reads one.
	if err := os.MkdirAll(filepath.Join(root, "var", "log"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "var", "log", "dmesg"), dmesg, 0o644); err != nil {
		t.Fatal(err)
	}
	collectHostKmsg = func() ([]byte, error) { return kmsgOf(dmesg), nil }
	t.Cleanup(func() { collectHostRoot, collectHostPrograms, collectHostKmsg = "/", nil, nil })
	return root, bin, log
}

var reDmesgLine = regexp.MustCompile(`^\[\s*(\d+)\.(\d{6})\] (.*)$`)

// kmsgOf writes dmesg lines as records of /dev/kmsg.
func kmsgOf(dmesg []byte) []byte {
	var b bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(dmesg))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	seq := 0
	for sc.Scan() {
		m := reDmesgLine.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		s, _ := strconv.ParseInt(m[1], 10, 64)
		us, _ := strconv.ParseInt(m[2], 10, 64)
		fmt.Fprintf(&b, "6,%d,%d,-;%s\n", seq, s*1e6+us, m[3])
		seq++
	}
	return b.Bytes()
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		dst := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func hostTreeFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			fi, _ := d.Info()
			out = append(out, fmt.Sprintf("%s %d %s", filepath.ToSlash(rel), fi.Size(), fi.ModTime()))
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func TestCollectHost(t *testing.T) {
	root, _, log := hostRoot(t)
	before := hostTreeFiles(t, root)
	out := filepath.Join(t.TempDir(), "coll")
	env := map[string]string{"MY_NOW": "2026-11-17T05:00:00Z"}
	code, stdout, stderr := runEnv(env, "collect", "host", "-o", out, "--lang", "en")
	// sar printed nothing and failed: the collection is written and partial.
	if code != exitPartial {
		t.Fatalf("exit %d, %s", code, stderr)
	}
	testutil.Golden(t, "collect-host-en.txt", []byte(strings.ReplaceAll(stdout, out, "OUT")))
	if !slicesEqual(before, hostTreeFiles(t, root)) {
		t.Error("the host's files changed")
	}
	var names []string
	for _, f := range hostTreeFiles(t, out) {
		names = append(names, strings.Fields(f)[0])
	}
	testutil.Golden(t, "collect-host-tree.txt", []byte(strings.Join(names, "\n")+"\n"))
	var cj scan.CollectResult
	b, err := os.ReadFile(filepath.Join(out, "collect.json"))
	if err != nil || json.Unmarshal(b, &cj) != nil || len(cj.Entries) == 0 {
		t.Fatalf("collect.json: %v", err)
	}
	ran, _ := os.ReadFile(log)
	if strings.Contains(string(ran), "sudo") {
		t.Errorf("sudo ran without --sudo: %s", ran)
	}
	// A scan of the collection equals a scan of the root.
	_, a, _ := runEnv(env, "--root", root, "--json")
	_, c, _ := runEnv(env, "--root", out, "--json")
	pick := regexp.MustCompile(`"(grade|exit_code|ce|ue|slots|populated)": ?("[^"]*"|\d+)`)
	if got, want := pick.FindAllString(c, -1), pick.FindAllString(a, -1); len(want) == 0 || !slicesEqual(got, want) {
		t.Errorf("scan of the collection %v, of the root %v", got, want)
	}

	// Korean, and --sudo: exactly dmidecode and lshw run through sudo.
	os.Remove(log)
	out2 := filepath.Join(t.TempDir(), "coll2")
	code, stdout, _ = runEnv(env, "collect", "host", "-o", out2, "--sudo", "--lang", "ko")
	if code != exitPartial {
		t.Errorf("exit %d", code)
	}
	testutil.Golden(t, "collect-host-ko.txt", []byte(strings.ReplaceAll(stdout, out2, "OUT")))
	ran, _ = os.ReadFile(log)
	var viaSudo []string
	for _, l := range strings.Split(strings.TrimSpace(string(ran)), "\n") {
		if strings.HasPrefix(l, "sudo ") {
			viaSudo = append(viaSudo, l)
		}
	}
	if want := []string{"sudo -n " + collectHostPrograms["dmidecode"] + " -t memory", "sudo -n " + collectHostPrograms["lshw"] + " -c memory -c processor"}; !slicesEqual(viaSudo, want) {
		t.Errorf("through sudo: %q, want %q", viaSudo, want)
	}
}

// Every step goes as asked: exit 0 when nothing was denied, timed out or
// failed; -o must be new or empty.
func TestCollectHostExit(t *testing.T) {
	hostRoot(t)
	collectHostPrograms["sar"] = "-"
	out := filepath.Join(t.TempDir(), "c")
	if code, _, stderr := runEnv(nil, "collect", "host", "-o", out); code != 0 {
		t.Errorf("exit %d: %s", code, stderr)
	}
	if code, _, stderr := runEnv(nil, "collect", "host", "-o", out); code != 64 || !strings.Contains(stderr, "not empty") {
		t.Errorf("into a full directory: exit %d, %s", code, stderr)
	}
	if code, _, _ := runEnv(nil, "collect", "redfish", "--sudo", "--bmc", "x", "-o", out+"x"); code != 64 {
		t.Errorf("--sudo with redfish: exit %d", code)
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
