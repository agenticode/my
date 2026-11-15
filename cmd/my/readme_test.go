package main

import (
	"slices"
	"testing"
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
