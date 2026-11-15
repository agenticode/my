package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/testutil"
)

// TestReadmeShowsRealOutput runs the commands the README quotes and
// compares what they print with the quoted text. The README claims to show
// real output; this keeps the claim true.
func TestReadmeShowsRealOutput(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	// The README names the ledger as it is created in the working directory.
	t.Chdir(t.TempDir())
	if code, _, errOut := run("synth", "-scenario", "demo", "-ledger", "demo.db"); code != cli.ExitOK {
		t.Fatalf("synth: exit code %d: %s", code, errOut)
	}
	for _, cmd := range []string{
		"my open demo.db",
		"my open demo.db dimm CPU0_C1",
		"my open demo.db why CPU0_C1 --oneline",
	} {
		start := strings.Index(string(readme), "```\n$ "+cmd+"\n")
		if start < 0 {
			t.Errorf("the README does not quote %q", cmd)
			continue
		}
		block := string(readme)[start+len("```\n$ "+cmd+"\n"):]
		end := strings.Index(block, "\n```")
		if end < 0 {
			t.Errorf("the block of %q does not end", cmd)
			continue
		}
		want := block[:end] + "\n"
		_, got, errOut := run(strings.Fields(cmd)[1:]...)
		if errOut != "" {
			t.Errorf("%s: stderr %q", cmd, errOut)
		}
		if got != want {
			w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
			for i := 0; i < len(w) || i < len(g); i++ {
				var wl, gl string
				if i < len(w) {
					wl = w[i]
				}
				if i < len(g) {
					gl = g[i]
				}
				if wl != gl {
					t.Errorf("README, output of %q, line %d:\n README %q\n actual %q", cmd, i+1, wl, gl)
					break
				}
			}
		}
	}
}
