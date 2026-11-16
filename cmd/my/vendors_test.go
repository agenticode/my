package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/fwmap"
	"github.com/agenticode/my/internal/term"
	"github.com/agenticode/my/internal/testutil"
)

// TestVendorsScreens pins the three forms of "my vendors" in both
// languages and as JSON.
func TestVendorsScreens(t *testing.T) {
	for _, tt := range []struct {
		args   []string
		golden string
	}{
		{[]string{"vendors"}, "vendors.en.txt"},
		{[]string{"vendors", "--lang", "ko"}, "vendors.ko.txt"},
		{[]string{"vendors", "dmtf"}, "vendors-dmtf.en.txt"},
		{[]string{"vendors", "dmtf", "--lang", "ko"}, "vendors-dmtf.ko.txt"},
		{[]string{"vendors", "--json"}, "json/vendors.json"},
		{[]string{"vendors", "dell", "--json"}, "json/vendors-dell.json"},
	} {
		code, out, errOut := run(tt.args...)
		if code != cli.ExitOK || errOut != "" {
			t.Errorf("%v: exit code %d, stderr %q", tt.args, code, errOut)
			continue
		}
		testutil.Golden(t, tt.golden, []byte(out))
	}
	code, out, _ := run("vendors", "--print", "ipmi")
	want, _ := fwmap.ShippedText("ipmi")
	if code != cli.ExitOK || out != string(want) {
		t.Errorf("--print ipmi: exit code %d, %d bytes", code, len(out))
	}
	for _, args := range [][]string{{"vendors", "hpe"}, {"vendors", "--print", "../x"}, {"vendors", "--seen"}, {"vendors", "a", "b"}} {
		if code, _, errOut := run(args...); code != cli.ExitUsage || errOut == "" {
			t.Errorf("%v: exit code %d, stderr %q", args, code, errOut)
		}
	}
}

// TestEventMap: a user table names a message no shipped table has, and
// "my vendors --seen" lists the IDs nothing classes.
func TestEventMap(t *testing.T) {
	dir := t.TempDir()
	log := testutil.Testdata(t, "redfish", "registry-log.json")
	plain := filepath.Join(dir, "plain.db")
	if code, _, errOut := runEnv(fixed, "import", log, "--ledger", plain); code > 4 {
		t.Fatalf("import: exit code %d, stderr %q", code, errOut)
	}
	code, out, errOut := runEnv(fixed, "open", plain, "vendors", "--seen")
	if code != cli.ExitOK || !strings.Contains(out, "MEM9999") || !strings.Contains(out, "no message registry of a BMC is on record") {
		t.Fatalf("--seen: exit code %d, stderr %q\n%s", code, errOut, out)
	}

	table := filepath.Join(dir, "mine.yaml")
	if err := os.WriteFile(table, []byte(`format: 1
table: dell
version: 1
vendor: Dell
basis: documents
key: message-id
sources:
  notes:
    title: "service notes"
    url: https://wiki.example.com/mem9999
messages:
  - id: MEM9999
    event: ppr-done
    args: location
    source: notes
  - id: MEM9998
    event: ppr-done
    source: elsewhere
`), 0o644); err != nil {
		t.Fatal(err)
	}
	mapped := filepath.Join(dir, "mapped.db")
	code, out, errOut = runEnv(fixed, "import", log, "--ledger", mapped, "--event-map", table)
	if code > 4 || !strings.Contains(errOut, "MEM9998") || !strings.Contains(errOut, "elsewhere") {
		t.Fatalf("import with --event-map: exit code %d, stderr %q\n%s", code, errOut, out)
	}
	if code, out, _ := runEnv(fixed, "open", mapped, "vendors", "--seen"); code != cli.ExitOK || strings.Contains(out, "MEM9999") {
		t.Errorf("--seen after the user table: exit code %d\n%s", code, out)
	}
	// The next command asks the shipped tables again.
	if code, out, _ := run("vendors", "dell", "--json"); code != cli.ExitOK || strings.Contains(out, "MEM9999") {
		t.Errorf("a user table outlived its command")
	}
	if code, _, errOut := runEnv(fixed, "import", log, "--event-map", filepath.Join(dir, "none.yaml")); code != cli.ExitUsage || !strings.Contains(errOut, "none.yaml") {
		t.Errorf("a missing table: exit code %d, stderr %q", code, errOut)
	}
}

// TestVendorsHelp keeps the help of "my vendors" and its flags the same:
// the command scans nothing and takes no flag that points a scan
// somewhere, and its help names no such flag.
func TestVendorsHelp(t *testing.T) {
	o, _, _ := vendorsOptions(nil)
	takes := map[string]bool{}
	o.fs.VisitAll(func(f *flag.Flag) {
		if f.Name != "h" && f.Name != "help" {
			takes["--"+f.Name] = true
		}
	})
	for _, lang := range []string{"en", "ko"} {
		code, out, errOut := run("help", "vendors", "--lang", lang)
		if code != cli.ExitOK || errOut != "" {
			t.Fatalf("help vendors (%s): exit code %d, stderr %q", lang, code, errOut)
		}
		named := map[string]bool{}
		for _, f := range flagRE.FindAllString(out, -1) {
			named[f] = true
			if !takes[f] {
				t.Errorf("help of vendors (%s) names %s, which the command does not take", lang, f)
			}
		}
		for f := range takes {
			if !named[f] {
				t.Errorf("help of vendors (%s) lacks %s", lang, f)
			}
		}
		for _, line := range strings.Split(out, "\n") {
			if term.Width(line) > term.DefaultWidth {
				t.Errorf("help of vendors (%s): line wider than %d columns: %q", lang, term.DefaultWidth, line)
			}
		}
	}
	for _, f := range []string{"--root", "--bmc", "--plugin", "--set"} {
		if code, _, errOut := run("vendors", f, "x"); code != cli.ExitUsage || !strings.Contains(errOut, "unknown flag "+f) {
			t.Errorf("my vendors %s x: exit code %d, stderr %q", f, code, errOut)
		}
	}
}
