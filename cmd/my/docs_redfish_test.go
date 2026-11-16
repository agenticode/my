package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/testutil"
)

// redfishCases makes the case files docs/redfish.md quotes, in dir:
// dell-shaped.mycase is "my import" of the Dell-shaped tree of
// testdata/redfish, and host-bmc.mycase is a bundle of the demo host made
// with --bmc against the stand-in BMC of the tests.
func redfishCases(t *testing.T, dir string) {
	t.Helper()
	data, err := os.ReadFile(bmcCase(t, "dell-shaped"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dell-shaped.mycase"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	root := hostTree(t)
	alone, _ := overview(t, fixed, "--root", root)
	srv := bmcOf(t, alone)
	code, stdout, stderr := runEnv(account(), "bundle", "--root", root, "--bmc", srv.URL, "--insecure", "-o", filepath.Join(dir, "host-bmc.mycase"))
	if code != cli.ExitOK {
		t.Fatalf("bundle with --bmc: exit code %d\n%s\n%s", code, stdout, stderr)
	}
}

// TestRedfishGuideShowsRealOutput keeps the screens docs/redfish.md quotes
// equal to what the commands print for the cases the page names. Set
// MY_DUMP_CASES to a directory to keep the case files.
func TestRedfishGuideShowsRealOutput(t *testing.T) {
	const page = "docs/redfish.md"
	text, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), page))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if keep := os.Getenv("MY_DUMP_CASES"); keep != "" {
		dir = keep
	}
	redfishCases(t, dir)
	qs := quotes(t, string(text))
	if len(qs) != 4 {
		t.Errorf("%s quotes %d commands, want 4", page, len(qs))
	}
	t.Chdir(dir)
	for _, q := range qs {
		_, out, errOut := runEnv(fixed, strings.Fields(q.cmd)[1:]...)
		if errOut != "" {
			t.Errorf("%s: %s: stderr %q", page, q.cmd, errOut)
		}
		got := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
		switch i := missing(q.want, got); {
		case i < 0:
		case i == len(q.want):
			t.Errorf("%s: %q prints more than the page quotes; the first line after the quote is %q", page, q.cmd, got[len(q.want)])
		default:
			t.Errorf("%s: the output of %q does not have line %d of the quote at its place:\n %q", page, q.cmd, i+1, q.want[i])
		}
	}
}
