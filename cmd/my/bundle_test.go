package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/bundle"
	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/synth"
	"github.com/agenticode/my/internal/testledger"
)

// hostTree writes the files of the demo host and returns the root.
func hostTree(t *testing.T) string {
	t.Helper()
	ds := testledger.Generate(t, "demo", 1, nil)
	root := filepath.Join(t.TempDir(), "root")
	if err := ds.WriteHostTree(root, synth.FlavorIntel); err != nil {
		t.Fatal(err)
	}
	return root
}

// fixed is an environment with a fixed clock.
var fixed = map[string]string{"MY_NOW": "2026-11-13T09:30:00Z"}

// TestBundleRoundTrip is the round trip of a case: the operator writes a
// bundle on the host, the receiver opens it and sees what the operator saw.
func TestBundleRoundTrip(t *testing.T) {
	root := hostTree(t)
	file := filepath.Join(t.TempDir(), "case.mycase")
	code, out, errOut := runEnv(fixed, "bundle", "CPU0_C1", "-o", file, "--root", root)
	if code != cli.ExitOK || errOut != "" {
		t.Fatalf("exit code %d, stderr %q", code, errOut)
	}
	wantLines(t, out,
		"case bundle written: "+file+" (",
		"  host r12-n034: 16 DIMMs, CE 1,284, UE 1, finding: replace now",
		"  subject of the case: CPU0_C1 S/N 3B9C56, finding: replace now",
		"look at it: my open "+file,
	)
	m, err := bundle.ReadManifest(file)
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject == nil || m.Subject.Slot != "CPU0_C1" || m.Subject.Serial != "3B9C56" || m.Anonymized || !strings.HasPrefix(m.Tool, "my ") ||
		m.CreatedAt.Format("2006-01-02 15:04") != "2026-11-13 09:30" {
		t.Errorf("manifest = %+v", m)
	}
	srcs, err := bundle.ReadSources(file)
	if err != nil || len(srcs.Scans) != 1 || len(srcs.Scans[0].Sources) < 5 {
		t.Errorf("source inventory = %+v, %v", srcs, err)
	}

	// The same screens as on the host. Only the first line (what the case
	// is) and the way further commands are spelled differ.
	strip := func(s, prefix string, opened bool) string {
		s = strings.ReplaceAll(s, prefix, "MY")
		if opened {
			s = s[strings.Index(s, "\n")+1:] // the line that says what the case is
		}
		// The lines of further commands differ: a scan puts its flags after
		// the command, and names "my bundle" on a host only.
		var keep []string
		for _, line := range strings.Split(s, "\n") {
			if !strings.HasPrefix(line, " my ") && !strings.HasPrefix(line, " MY ") {
				keep = append(keep, line)
			}
		}
		return strings.Join(keep, "\n")
	}
	for _, args := range [][]string{nil, {"dimm", "CPU0_C1"}, {"why", "CPU1_D1"}, {"map", "CPU0_C1", "--ascii"}, {"timeline"}, {"dimm", "CPU0_C1", "--lang", "ko"}} {
		_, onHost, _ := runEnv(fixed, append(append([]string{}, args...), "--root", root)...)
		code, opened, errOut := runEnv(fixed, append([]string{"open", file}, args...)...)
		if errOut != "" || code == cli.ExitFailure || code == cli.ExitUsage {
			t.Errorf("my open FILE %v: exit code %d, stderr %q", args, code, errOut)
		}
		if a, b := strip(onHost, "my --root "+root, false), strip(opened, "my open "+file, true); a != b {
			t.Errorf("my open FILE %v differs from the screen on the host:\n--- host\n%s\n--- case\n%s", args, a, b)
		}
		first := strings.SplitN(opened, "\n", 2)[0]
		if !strings.Contains(first, "2026-11-13 09:30 UTC") || !strings.Contains(first, "CPU0_C1 S/N 3B9C56") || !(strings.HasPrefix(first, "case bundle of ") || strings.Contains(first, "케이스 번들")) {
			t.Errorf("my open FILE %v does not say what the case is:\n%s", args, opened)
		}
	}
	// The next commands point at the subject of the case, also when another
	// DIMM ranks above it.
	second := filepath.Join(t.TempDir(), "second.mycase")
	if code, _, errOut := runEnv(fixed, "bundle", "CPU1_D1", "-o", second, "--root", root); code != cli.ExitOK {
		t.Fatalf("exit code %d, stderr %q", code, errOut)
	}
	_, out, _ = runEnv(fixed, "open", second)
	wantLines(t, out, "subject CPU1_D1 S/N 2AEB41", "my open "+second+" dimm CPU1_D1: examination of this DIMM")

	// --json carries what the case is.
	_, out, _ = runEnv(fixed, "open", file, "why", "CPU0_C1", "--json")
	var e struct {
		Case *caseInfo `json:"case"`
	}
	if err := json.Unmarshal([]byte(out), &e); err != nil || e.Case == nil || e.Case.Subject == nil || e.Case.Subject.Slot != "CPU0_C1" || e.Case.CreatedAt != "2026-11-13T09:30:00Z" {
		t.Errorf("case in JSON: %+v, %v", e.Case, err)
	}

	// A bundle that exists is not replaced without --force.
	code, _, errOut = runEnv(fixed, "bundle", "-o", file, "--root", root)
	if code != cli.ExitUsage || !strings.Contains(errOut, "exists; --force replaces it") {
		t.Errorf("second bundle: exit code %d, stderr %q", code, errOut)
	}
	if code, _, errOut = runEnv(fixed, "bundle", "-o", file, "--root", root, "--force"); code != cli.ExitOK {
		t.Errorf("--force: exit code %d, stderr %q", code, errOut)
	}
	if m, _ := bundle.ReadManifest(file); m.Subject != nil {
		t.Errorf("a bundle without a DIMM has a subject: %+v", m.Subject)
	}
}

func TestBundleAnonymize(t *testing.T) {
	root := hostTree(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "anon.mycase")
	code, out, errOut := runEnv(fixed, "bundle", "-o", file, "--root", root, "--anonymize")
	if code != cli.ExitOK || errOut != "" {
		t.Fatalf("exit code %d, stderr %q", code, errOut)
	}
	// The operator is told the new name; the bundle does not hold the old.
	var word string
	for _, line := range strings.Split(out, "\n") {
		if name, w, ok := strings.Cut(strings.TrimSpace(line), " -> "); ok && name == "r12-n034" {
			word = w
		}
	}
	if !strings.HasPrefix(word, "host-") || !strings.Contains(out, "host names replaced") || !strings.Contains(out, "Keep these lines") {
		t.Fatalf("output:\n%s", out)
	}
	if strings.Contains(out, "host r12-n034:") {
		t.Errorf("the summary names the host by its old name:\n%s", out)
	}
	code, opened, _ := runEnv(fixed, "open", file)
	if code != 3 || strings.Contains(opened, "r12-n034") || !strings.Contains(opened, "host "+word) ||
		!strings.Contains(strings.Join(strings.Fields(opened), " "), "host names and network addresses were replaced") {
		t.Errorf("the opened case:\n%s", opened)
	}
	// The serial numbers stay: a return is about them.
	if !strings.Contains(opened, "3B9C56") {
		t.Errorf("the serial number is gone:\n%s", opened)
	}

	// A key file makes two bundles agree on the new name.
	key := filepath.Join(dir, "key")
	os.WriteFile(key, []byte("a key of this site\n"), 0o600)
	words := map[string]bool{}
	for _, name := range []string{"k1.mycase", "k2.mycase"} {
		code, out, errOut := runEnv(fixed, "bundle", "-o", filepath.Join(dir, name), "--root", root, "--anonymize", "--key-file", key, "--json")
		if code != cli.ExitOK {
			t.Fatalf("exit code %d, stderr %q", code, errOut)
		}
		var res struct {
			Names    map[string]string `json:"names"`
			Manifest bundle.Manifest   `json:"manifest"`
		}
		if err := json.Unmarshal([]byte(out), &res); err != nil || !res.Manifest.Anonymized || !res.Manifest.Anonymization.Keyed {
			t.Fatalf("--json: %v\n%s", err, out)
		}
		words[res.Names["r12-n034"]] = true
	}
	if len(words) != 1 || words[word] {
		t.Errorf("words with one key: %v; without a key: %s", words, word)
	}

	// A case that was received can be passed on without the names.
	plain := filepath.Join(dir, "plain.mycase")
	if code, _, errOut := runEnv(fixed, "bundle", "-o", plain, "--root", root); code != cli.ExitOK {
		t.Fatalf("exit code %d, stderr %q", code, errOut)
	}
	again := filepath.Join(dir, "again.mycase")
	code, out, errOut = runEnv(fixed, "open", plain, "bundle", "-o", again, "--anonymize")
	if code != cli.ExitOK || !strings.Contains(out, "r12-n034 -> host-") {
		t.Fatalf("re-bundling: exit code %d, stderr %q\n%s", code, errOut, out)
	}
	if _, opened, _ := runEnv(fixed, "open", again, "why", "--oneline"); strings.Contains(opened, "r12-n034") || !strings.Contains(opened, "replace now") {
		t.Errorf("the re-bundled case:\n%s", opened)
	}
}

func TestBundleKeepsWhatTheSenderSaw(t *testing.T) {
	root := hostTree(t)
	file := filepath.Join(t.TempDir(), "tuned.mycase")
	// The sender ran with a threshold of their own.
	if code, _, errOut := runEnv(fixed, "bundle", "-o", file, "--root", root, "--set", "trend.recent_days=3"); code != cli.ExitOK {
		t.Fatalf("exit code %d, stderr %q", code, errOut)
	}
	_, out, _ := runEnv(fixed, "open", file)
	if !strings.Contains(strings.Join(strings.Fields(out), " "), "The sender changed thresholds. To see what they saw, add: --set trend.recent_days=3") {
		t.Errorf("the opened case does not name the sender's thresholds:\n%s", out)
	}
	// With the same threshold the note about a different finding is gone.
	_, out, _ = runEnv(fixed, "open", file, "--set", "trend.recent_days=3")
	if strings.Contains(out, "The sender's host finding was") {
		t.Errorf("same thresholds, yet a different finding is reported:\n%s", out)
	}
}

func TestBundleMistakes(t *testing.T) {
	root := hostTree(t)
	dir := t.TempDir()
	short := filepath.Join(dir, "short")
	os.WriteFile(short, []byte("abc\n"), 0o600)
	for _, tt := range []struct {
		name string
		args []string
		code int
		want string
	}{
		{"no file named", []string{"bundle", "--root", root}, cli.ExitUsage, "name the file to write with -o"},
		{"two DIMMs", []string{"bundle", "A", "B", "-o", filepath.Join(dir, "x"), "--root", root}, cli.ExitUsage, `unexpected argument "B"`},
		{"unknown DIMM", []string{"bundle", "CPU9_Z9", "-o", filepath.Join(dir, "x"), "--root", root}, cli.ExitUsage, `no slot or DIMM "CPU9_Z9"`},
		{"a key without anonymizing", []string{"bundle", "-o", filepath.Join(dir, "x"), "--key-file", short, "--root", root}, cli.ExitUsage, "--key-file goes with --anonymize"},
		{"a key that is too short", []string{"bundle", "-o", filepath.Join(dir, "x"), "--anonymize", "--key-file", short, "--root", root}, cli.ExitUsage, "shorter than 8 characters"},
		{"a key file that is missing", []string{"bundle", "-o", filepath.Join(dir, "x"), "--anonymize", "--key-file", filepath.Join(dir, "none"), "--root", root}, cli.ExitUsage, "no such file"},
		{"a directory that is missing", []string{"bundle", "-o", filepath.Join(dir, "none", "x.mycase"), "--root", root}, cli.ExitUsage, "the directory " + filepath.Join(dir, "none") + " does not exist"},
	} {
		code, out, errOut := runEnv(fixed, tt.args...)
		if code != tt.code || !strings.Contains(errOut, tt.want) || out != "" {
			t.Errorf("%s: exit code %d, stdout %q, stderr %q; want %d and %q", tt.name, code, out, errOut, tt.code, tt.want)
		}
	}
	// A failed bundle leaves no file behind.
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("%d files in the output directory, want only the key file", len(entries))
	}
	// A damaged bundle is refused with the reason.
	bad := filepath.Join(dir, "bad.mycase")
	os.WriteFile(bad, []byte("PK\x03\x04 not really"), 0o644)
	code, _, errOut := run("open", bad)
	if code != cli.ExitFailure || !strings.Contains(errOut, "not a case bundle") {
		t.Errorf("a damaged bundle: exit code %d, stderr %q", code, errOut)
	}
}
