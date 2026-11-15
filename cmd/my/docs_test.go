package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"

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

// anchor returns the name GitHub gives the heading of a Markdown page:
// lower case, spaces as hyphens, punctuation dropped.
func anchor(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(heading)) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// anchors returns the names of the headings of a Markdown page. Lines
// inside a fenced block are no headings.
func anchors(page string) map[string]bool {
	out := map[string]bool{}
	fenced := false
	for _, line := range strings.Split(page, "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
			continue
		}
		if fenced || !strings.HasPrefix(line, "#") {
			continue
		}
		text := strings.TrimLeft(line, "#")
		if !strings.HasPrefix(text, " ") {
			continue
		}
		// Code marks are no part of the name.
		out[anchor(strings.ReplaceAll(text, "`", ""))] = true
	}
	return out
}

var linkRE = regexp.MustCompile(`\]\(([^)\s]+)\)`)

// TestLinksOfThePages follows every link between the Markdown pages of
// the repository: the file a link names exists, and so does the heading
// it points at.
func TestLinksOfThePages(t *testing.T) {
	root := testutil.RepoRoot(t)
	var pages []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "dist", "bin", "node_modules":
				return filepath.SkipDir
			}
			// Copies of hosts and captures hold files of other origin.
			if rel, _ := filepath.Rel(root, path); strings.HasPrefix(filepath.ToSlash(rel), "testdata/") && strings.Count(filepath.ToSlash(rel), "/") >= 2 {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".md") {
			pages = append(pages, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < 10 {
		t.Fatalf("found %d Markdown pages below %s", len(pages), root)
	}
	text := map[string]string{}
	read := func(path string) (string, bool) {
		if s, ok := text[path]; ok {
			return s, true
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return "", false
		}
		text[path] = string(b)
		return string(b), true
	}
	links := 0
	for _, page := range pages {
		body, _ := read(page)
		rel, _ := filepath.Rel(root, page)
		fenced := false
		for n, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(line, "```") {
				fenced = !fenced
			}
			if fenced {
				continue
			}
			for _, m := range linkRE.FindAllStringSubmatch(line, -1) {
				target := m[1]
				if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
					continue
				}
				links++
				file, frag, _ := strings.Cut(target, "#")
				dest := page
				if file != "" {
					dest = filepath.Join(filepath.Dir(page), filepath.FromSlash(file))
					if _, err := os.Stat(dest); err != nil {
						t.Errorf("%s:%d: the link to %s leads nowhere", rel, n+1, target)
						continue
					}
				}
				if frag == "" || !strings.HasSuffix(dest, ".md") {
					continue
				}
				if s, ok := read(dest); !ok || !anchors(s)[frag] {
					t.Errorf("%s:%d: the link to %s names a heading that is not there", rel, n+1, target)
				}
			}
		}
	}
	if links < 40 {
		t.Errorf("followed %d links; the pages hold more", links)
	}
}

// TestLayoutOfTheSourceTree keeps the layout table of CONTRIBUTING.md
// complete: every package directory below internal/ and every directory
// below tools/ has a row.
func TestLayoutOfTheSourceTree(t *testing.T) {
	root := testutil.RepoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "CONTRIBUTING.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, table, ok := strings.Cut(string(b), "\n## Layout\n")
	if !ok {
		t.Fatal("CONTRIBUTING.md has no section \"Layout\"")
	}
	for _, parent := range []string{"internal", "tools"} {
		entries, err := os.ReadDir(filepath.Join(root, parent))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if dir := parent + "/" + e.Name(); !strings.Contains(table, "`"+dir+"`") {
				t.Errorf("CONTRIBUTING.md: the layout table has no row for %s", dir)
			}
		}
	}
}
