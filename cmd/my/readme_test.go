package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/testutil"
)

// TestReadmeShowsRealOutput runs the commands the two READMEs quote and
// compares what they print with the quoted text. A README claims to show
// real output; this keeps the claim true. The Korean one shows what a
// reader with a Korean locale gets.
func TestReadmeShowsRealOutput(t *testing.T) {
	want := []string{
		"my open examples/demo.mycase",
		"my open examples/demo.mycase dimm CPU0_C1",
		"my open examples/demo.mycase why CPU0_C1 --oneline",
	}
	for _, tt := range []struct {
		page string
		env  map[string]string
	}{
		{"README.md", nil},
		{"README.ko.md", map[string]string{"LANG": "ko_KR.UTF-8"}},
	} {
		t.Run(tt.page, func(t *testing.T) {
			if ran := checkQuotes(t, tt.page, tt.env); !slices.Equal(ran, want) {
				t.Errorf("%s quotes the commands %q, want %q", tt.page, ran, want)
			}
		})
	}
}

// TestReadmeListsTheCommands holds the commands tables of the two READMEs
// to the command list: every command that is not hidden has a row, and so
// has each way of "my collect".
func TestReadmeListsTheCommands(t *testing.T) {
	names := []string{"collect host", "collect redfish"}
	for _, c := range app().Commands {
		if !c.Hidden && c.Name != "collect" && c.Name != "version" {
			names = append(names, c.Name)
		}
	}
	for _, page := range []string{"README.md", "README.ko.md"} {
		text, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), page))
		if err != nil {
			t.Fatal(err)
		}
		var rows []string
		for _, line := range strings.Split(string(text), "\n") {
			if strings.HasPrefix(line, "| `my") {
				rows = append(rows, line)
			}
		}
		for _, n := range names {
			if !slices.ContainsFunc(rows, func(r string) bool { return strings.Contains(r, "`my "+n) }) {
				t.Errorf("the commands table of %s has no row for my %s", page, n)
			}
		}
	}
}
