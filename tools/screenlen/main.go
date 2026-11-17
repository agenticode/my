// Command screenlen counts the lines and characters of the default screens
// of MemorY on the lab captures of testdata/real, in English and Korean.
// It measures how much a reader has to read before a decision; a change of
// wording or layout puts its table before and after into the pull request.
//
//	go build -o /tmp/my ./cmd/my
//	go run ./tools/screenlen -my /tmp/my
//	go run ./tools/screenlen -my /tmp/my -format tsv > after.tsv
//	go run ./tools/screenlen -compare before.tsv after.tsv
//
// A capture with a kept ledger (var/lib/my/ledger.db) is shown through that
// ledger, as "my open LEDGER", because that is what a reader of the case
// sees; preflight always reads the copy (--root). The report counts the
// text of its summary section (s1) and of its findings section (s3), one
// line per block element. A screen that does not apply to a capture (a
// DIMM screen on a host whose findings name no DIMM) is left out.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// captures are the fixed inputs and the DIMM each DIMM screen is about; an
// empty slot means the findings of the capture name no DIMM.
var captures = []struct{ name, slot string }{
	{"6.19.10-ghes", "DIMM 0"},
	{"6.19.10-ghes-multi", "CPU0_B1"},
	{"6.19.10-bert-loop", "DIMM 0"},
	{"6.19.10-ghes-loop-spread", "CPU0_A1"},
	{"7.3.0-mce", ""},
	{"6.19.10-mce-loop", ""},
}

// screens in the order of the table. dimm is true for a screen that needs
// a slot.
var screens = []struct {
	name string
	dimm bool
}{
	{"scan", false}, {"dimm", true}, {"why", false}, {"map", true}, {"timeline", false},
	{"boots", false}, {"inventory", false}, {"board", false}, {"bios", false}, {"preflight", false},
	{"gpu", false}, {"diff", false}, {"report-s1", false}, {"report-s3", false},
}

// Count is the size of one screen.
type Count struct {
	Capture, Screen, Lang string
	Lines, Chars          int
}

func main() {
	my := flag.String("my", "", "the my binary to run")
	root := flag.String("root", "testdata/real", "directory of the captures")
	width := flag.Int("width", 104, "columns of the screens")
	langs := flag.String("langs", "en,ko", "languages")
	only := flag.String("only", "", "screens to count, comma separated (default: all)")
	format := flag.String("format", "table", "table or tsv")
	compare := flag.Bool("compare", false, "compare two tsv files: screenlen -compare BEFORE AFTER")
	flag.Parse()
	if *compare {
		if flag.NArg() != 2 {
			fail(errors.New("-compare needs two tsv files"))
		}
		fail(compareFiles(flag.Arg(0), flag.Arg(1), os.Stdout))
		return
	}
	if *my == "" {
		fail(errors.New("-my BIN is required"))
	}
	tmp, err := os.MkdirTemp("", "screenlen")
	if err != nil {
		fail(err)
	}
	defer os.RemoveAll(tmp)
	want := map[string]bool{}
	for _, s := range strings.Split(*only, ",") {
		if s != "" {
			want[s] = true
		}
	}
	var counts []Count
	for _, c := range captures {
		dir := filepath.Join(*root, c.name)
		if _, err := os.Stat(dir); err != nil {
			fail(err)
		}
		for _, lang := range strings.Split(*langs, ",") {
			for _, s := range screens {
				if (len(want) > 0 && !want[s.name]) || (s.dimm && c.slot == "") {
					continue
				}
				out, ok, err := runScreen(*my, dir, c.slot, s.name, lang, *width, tmp)
				if err != nil {
					fail(fmt.Errorf("%s %s %s: %w", c.name, s.name, lang, err))
				}
				if !ok {
					continue
				}
				n := Count{Capture: c.name, Screen: s.name, Lang: lang}
				n.Lines, n.Chars = measure(out)
				counts = append(counts, n)
			}
		}
	}
	switch *format {
	case "tsv":
		err = writeTSV(os.Stdout, counts)
	default:
		err = writeTable(os.Stdout, counts)
	}
	fail(err)
}

func fail(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "screenlen:", err)
		os.Exit(1)
	}
}

// source returns the arguments that name the case of a capture: its kept
// ledger when it has one, the copy otherwise.
func source(dir string, copyOnly bool) (pre, post []string) {
	ledger := filepath.Join(dir, "var", "lib", "my", "ledger.db")
	if _, err := os.Stat(ledger); err == nil && !copyOnly {
		return []string{"open", ledger}, nil
	}
	return nil, []string{"--root", dir}
}

// runScreen runs one screen and returns what it printed. ok is false when
// the screen does not apply to the capture.
func runScreen(my, dir, slot, screen, lang string, width int, tmp string) ([]byte, bool, error) {
	common := []string{"--lang", lang, "--color", "never", "--width", strconv.Itoa(width)}
	pre, post := source(dir, screen == "preflight")
	var args []string
	switch screen {
	case "dimm", "map":
		args = append(append(pre, screen, slot), post...)
	case "why":
		args = append(pre, screen)
		if slot != "" {
			args = append(args, slot)
		}
		args = append(args, post...)
	case "diff":
		// The kept ledger against the copy as one case: what the copy
		// adds to the ledger. A capture without a kept ledger has no
		// second case.
		if len(pre) == 0 {
			return nil, false, nil
		}
		bundle := filepath.Join(tmp, filepath.Base(dir)+".mycase")
		if _, err := os.Stat(bundle); err != nil {
			if _, err := run(my, "bundle", "-o", bundle, "--root", dir); err != nil {
				return nil, false, err
			}
		}
		args = []string{"diff", pre[1], bundle}
	case "report-s1", "report-s3":
		file := filepath.Join(tmp, filepath.Base(dir)+"."+lang+".html")
		if _, err := os.Stat(file); err != nil {
			rargs := append(append(append([]string{}, pre...), "report", "-o", file, "--lang", lang), post...)
			if _, err := run(my, rargs...); err != nil {
				return nil, false, err
			}
		}
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, false, err
		}
		id := strings.TrimPrefix(screen, "report-")
		text, found := sectionText(string(b), id)
		return []byte(text), found, nil
	case "scan":
		// "my open FILE" with no command is the overview.
		args = pre
		if len(pre) == 0 {
			args = []string{"scan"}
		}
		args = append(args, post...)
	default:
		args = append(append(pre, screen), post...)
	}
	out, err := run(my, append(args, common...)...)
	return out, true, err
}

// run runs my and returns standard output and standard error together, as
// a terminal shows them. An exit code below 64 is the grade of what was
// shown and no failure.
func run(my string, args ...string) ([]byte, error) {
	cmd := exec.Command(my, args...)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() < 64 {
		err = nil
	}
	if err != nil {
		return nil, fmt.Errorf("my %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return out, nil
}

// measure counts the lines and the characters of a screen; trailing empty
// lines do not count.
func measure(out []byte) (lines, chars int) {
	s := strings.TrimRight(string(out), "\n ")
	if s == "" {
		return 0, 0
	}
	for _, l := range strings.Split(s, "\n") {
		lines++
		chars += utf8.RuneCountInString(strings.TrimRight(l, " "))
	}
	return lines, chars
}

var (
	reHidden = regexp.MustCompile(`(?s)<(style|script|svg|template)\b.*?</(style|script|svg|template)>`)
	reBlock  = regexp.MustCompile(`</(p|li|h1|h2|h3|h4|tr|dt|dd|div|figcaption|summary|caption)>|<br\s*/?>`)
	reTag    = regexp.MustCompile(`<[^>]+>`)
	reSpace  = regexp.MustCompile(`[ \t\r]+`)
)

// sectionText returns the text of the section with the given id of a
// report, one line per block element.
func sectionText(doc, id string) (string, bool) {
	start := strings.Index(doc, `<section id="`+id+`"`)
	if start < 0 {
		return "", false
	}
	rest := doc[start:]
	// Sections nest (a DIMM's section inside a numbered one): the section
	// ends where the next numbered one starts.
	end := len(rest)
	if i := regexp.MustCompile(`<section id="s\d+"`).FindStringIndex(rest[1:]); i != nil {
		end = i[0] + 1
	}
	s := reHidden.ReplaceAllString(rest[:end], " ")
	s = reBlock.ReplaceAllString(s, "\n")
	s = html.UnescapeString(reTag.ReplaceAllString(s, " "))
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(reSpace.ReplaceAllString(l, " ")); l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n") + "\n", true
}

func writeTSV(w io.Writer, counts []Count) error {
	bw := bufio.NewWriter(w)
	fmt.Fprintln(bw, "capture\tscreen\tlang\tlines\tchars")
	for _, c := range counts {
		fmt.Fprintf(bw, "%s\t%s\t%s\t%d\t%d\n", c.Capture, c.Screen, c.Lang, c.Lines, c.Chars)
	}
	return bw.Flush()
}

// writeTable prints one row per screen and capture with the lines of each
// language, and the sums per screen.
func writeTable(w io.Writer, counts []Count) error {
	bw := bufio.NewWriter(w)
	fmt.Fprintf(bw, "%-26s %-10s %9s %9s\n", "capture", "screen", "en l/ch", "ko l/ch")
	type key struct{ capture, screen string }
	rows := map[key]map[string]Count{}
	var order []key
	for _, c := range counts {
		k := key{c.Capture, c.Screen}
		if rows[k] == nil {
			rows[k] = map[string]Count{}
			order = append(order, k)
		}
		rows[k][c.Lang] = c
	}
	cell := func(c Count, ok bool) string {
		if !ok {
			return "-"
		}
		return fmt.Sprintf("%d/%d", c.Lines, c.Chars)
	}
	for _, k := range order {
		en, okEN := rows[k]["en"]
		ko, okKO := rows[k]["ko"]
		fmt.Fprintf(bw, "%-26s %-10s %9s %9s\n", k.capture, k.screen, cell(en, okEN), cell(ko, okKO))
	}
	return bw.Flush()
}

// compareFiles prints the lines of each screen before and after, summed
// over the captures, and per capture.
func compareFiles(before, after string, w io.Writer) error {
	a, err := readTSV(before)
	if err != nil {
		return err
	}
	b, err := readTSV(after)
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(w)
	fmt.Fprintln(bw, "| screen | capture | en before | en after | ko before | ko after |")
	fmt.Fprintln(bw, "| --- | --- | ---: | ---: | ---: | ---: |")
	var keys []string
	seen := map[string]bool{}
	for _, m := range []map[string]Count{a, b} {
		for k := range m {
			sk := k[:strings.LastIndex(k, "\t")]
			if !seen[sk] {
				seen[sk] = true
				keys = append(keys, sk)
			}
		}
	}
	order := map[string]int{}
	for i, s := range screens {
		order[s.name] = i
	}
	sort.Slice(keys, func(i, j int) bool {
		si, ci := split2(keys[i])
		sj, cj := split2(keys[j])
		if si != sj {
			return order[si] < order[sj]
		}
		return ci < cj
	})
	val := func(m map[string]Count, k string) string {
		c, ok := m[k]
		if !ok {
			return "-"
		}
		return strconv.Itoa(c.Lines)
	}
	// The sum of a screen over the captures both files have.
	sum := func(m, other map[string]Count, screen, lang string) string {
		n, any := 0, false
		for k, c := range m {
			if c.Screen == screen && c.Lang == lang {
				if _, ok := other[k]; ok {
					n += c.Lines
					any = true
				}
			}
		}
		if !any {
			return "-"
		}
		return strconv.Itoa(n)
	}
	for _, k := range keys {
		s, c := split2(k)
		fmt.Fprintf(bw, "| %s | %s | %s | %s | %s | %s |\n", s, c,
			val(a, k+"\ten"), val(b, k+"\ten"), val(a, k+"\tko"), val(b, k+"\tko"))
	}
	fmt.Fprintln(bw)
	fmt.Fprintln(bw, "| screen | en before | en after | ko before | ko after |")
	fmt.Fprintln(bw, "| --- | ---: | ---: | ---: | ---: |")
	for _, sc := range screens {
		row := []string{sum(a, b, sc.name, "en"), sum(b, a, sc.name, "en"), sum(a, b, sc.name, "ko"), sum(b, a, sc.name, "ko")}
		if row[0] == "-" && row[2] == "-" {
			continue
		}
		fmt.Fprintf(bw, "| %s | %s | %s | %s | %s |\n", sc.name, row[0], row[1], row[2], row[3])
	}
	return bw.Flush()
}

func split2(k string) (screen, capture string) {
	i := strings.Index(k, "\t")
	return k[:i], k[i+1:]
}

// readTSV reads the output of -format tsv, keyed "screen\tcapture\tlang".
func readTSV(name string) (map[string]Count, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]Count{}
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		if line == 1 {
			continue
		}
		f := strings.Split(sc.Text(), "\t")
		if len(f) != 5 {
			return nil, fmt.Errorf("%s:%d: want 5 fields, have %d", name, line, len(f))
		}
		lines, err1 := strconv.Atoi(f[3])
		chars, err2 := strconv.Atoi(f[4])
		if err := errors.Join(err1, err2); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", name, line, err)
		}
		out[f[1]+"\t"+f[0]+"\t"+f[2]] = Count{Capture: f[0], Screen: f[1], Lang: f[2], Lines: lines, Chars: chars}
	}
	return out, sc.Err()
}
