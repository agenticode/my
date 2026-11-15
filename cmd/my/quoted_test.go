package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/testutil"
)

// quote is a command a page shows with its output: a fenced block whose
// first line is "$ my ...".
type quote struct {
	cmd  string
	want []string
}

// elided stands in a quote for lines that were left out.
const elided = "[...]"

// quotes returns the commands a page shows with their output.
func quotes(t *testing.T, page string) []quote {
	t.Helper()
	const open = "```\n$ "
	var out []quote
	for rest := page; ; {
		i := strings.Index(rest, open+"my ")
		if i < 0 {
			return out
		}
		rest = rest[i+len(open):]
		end := strings.Index(rest, "\n```")
		if end < 0 {
			t.Fatalf("a block that starts with %q does not end", strings.SplitN(rest, "\n", 2)[0])
		}
		cmd, body, _ := strings.Cut(rest[:end], "\n")
		out = append(out, quote{cmd: cmd, want: strings.Split(body, "\n")})
		rest = rest[end:]
	}
}

// missing compares the lines of a quote with what the command printed and
// returns the first quoted line the output does not have at its place, or
// -1. Lines between two elisions may stand anywhere after what came before
// them; the other lines stand at the start and at the end of the output.
func missing(want, got []string) int {
	at, floating := 0, false
	for i := 0; i < len(want); {
		if want[i] == elided {
			floating = true
			i++
			continue
		}
		n := 0
		for i+n < len(want) && want[i+n] != elided {
			n++
		}
		seg := want[i : i+n]
		last := i+n == len(want)
		match := func(from int) int {
			for k, line := range seg {
				if from+k >= len(got) || got[from+k] != line {
					return k
				}
			}
			return -1
		}
		switch {
		case !floating:
			if k := match(at); k >= 0 {
				return i + k
			}
		case last:
			// The end of the quote is the end of the output.
			at = len(got) - n
			if at < 0 {
				return i
			}
			if k := match(at); k >= 0 {
				return i + k
			}
		default:
			found := false
			for ; at+n <= len(got); at++ {
				if match(at) < 0 {
					found = true
					break
				}
			}
			if !found {
				return i
			}
		}
		at += n
		i += n
		floating = false
	}
	if !floating && at != len(got) {
		return len(want)
	}
	return -1
}

// checkQuotes runs every command a page of the repository quotes, from the
// top of the repository as the page writes them, and compares what it
// prints with the quoted text. It returns the commands it ran.
func checkQuotes(t *testing.T, page string, env map[string]string) []string {
	t.Helper()
	root := testutil.RepoRoot(t)
	text, err := os.ReadFile(filepath.Join(root, page))
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	var ran []string
	for _, q := range quotes(t, string(text)) {
		ran = append(ran, q.cmd)
		_, out, errOut := runEnv(env, strings.Fields(q.cmd)[1:]...)
		if errOut != "" {
			t.Errorf("%s: %s: stderr %q", page, q.cmd, errOut)
		}
		got := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
		i := missing(q.want, got)
		switch {
		case i < 0:
		case i == len(q.want):
			t.Errorf("%s: %q prints more than the page quotes; the first line after the quote is %q", page, q.cmd, got[len(q.want)])
		default:
			t.Errorf("%s: the output of %q does not have line %d of the quote at its place:\n %q", page, q.cmd, i+1, q.want[i])
		}
	}
	return ran
}

// TestQuoteComparison checks the comparison itself.
func TestQuoteComparison(t *testing.T) {
	got := []string{"a", "b", "c", "d", "e"}
	for _, tt := range []struct {
		name string
		want []string
		at   int
	}{
		{"whole", []string{"a", "b", "c", "d", "e"}, -1},
		{"changed line", []string{"a", "x", "c", "d", "e"}, 1},
		{"cut short", []string{"a", "b", "c"}, 3},
		{"too long", []string{"a", "b", "c", "d", "e", "f"}, 5},
		{"head", []string{"a", "b", elided}, -1},
		{"tail", []string{elided, "d", "e"}, -1},
		{"tail that is not the end", []string{elided, "c", "d"}, 1},
		{"middle", []string{elided, "b", "c", elided}, -1},
		{"head, middle and tail", []string{"a", elided, "c", elided, "e"}, -1},
		{"out of order", []string{elided, "d", elided, "b", elided}, 3},
		{"not there", []string{"a", elided, "x", elided}, 2},
	} {
		if at := missing(tt.want, got); at != tt.at {
			t.Errorf("%s: missing = %d, want %d", tt.name, at, tt.at)
		}
	}
}

// TestGuidesShowRealOutput keeps the screens the guides quote equal to
// what the commands print for the example case.
func TestGuidesShowRealOutput(t *testing.T) {
	for page, n := range map[string]int{
		"docs/reading-guide.md": 5,
	} {
		t.Run(page, func(t *testing.T) {
			if ran := checkQuotes(t, page, nil); len(ran) != n {
				t.Errorf("%s quotes %d commands, want %d: %q", page, len(ran), n, ran)
			}
		})
	}
}
