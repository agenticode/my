package explorer

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/testutil"
)

// docSection checks that the text between the markers <!-- NAME --> and
// <!-- /NAME --> of docs/compatibility.md is want; UPDATE_GOLDEN=1
// writes it there.
func docSection(t *testing.T, name, want string) {
	t.Helper()
	path := testutil.RepoRoot(t) + "/docs/compatibility.md"
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	open, end := "<!-- "+name+" -->\n", "<!-- /"+name+" -->"
	i, j := strings.Index(text, open), strings.Index(text, end)
	if i < 0 || j < i {
		t.Fatalf("docs/compatibility.md has no section %s", name)
	}
	if got := text[i+len(open) : j]; got != want {
		if os.Getenv("UPDATE_GOLDEN") != "" {
			if err := os.WriteFile(path, []byte(text[:i+len(open)]+want+text[j:]), 0o644); err != nil {
				t.Fatal(err)
			}
			return
		}
		t.Errorf("the section %s of docs/compatibility.md differs from the code (run with UPDATE_GOLDEN=1 after checking the change)", name)
	}
}

// cell is a value for a table cell of Markdown.
func cell(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

// The tables of the name forms in the docs are those of the code: every
// form in the order it is tried, with examples and what its parts are.
func TestSlotFormsInTheDocs(t *testing.T) {
	var b strings.Builder
	b.WriteString("| Locator form | Examples | Parts |\n| --- | --- | --- |\n")
	for _, f := range locatorForms {
		n, ok := formNotes[f.id]
		if !ok {
			t.Errorf("form %s has no note", f.id)
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", f.id, n[0], n[1])
	}
	b.WriteString("\n| Bank locator form | Examples | Parts |\n| --- | --- | --- |\n")
	for _, f := range bankForms {
		n, ok := formNotes["b:"+f.id]
		if !ok {
			t.Errorf("form %s has no note", f.id)
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", f.id, n[0], n[1])
	}
	if len(formNotes) != len(locatorForms)+len(bankForms) {
		t.Errorf("%d notes for %d forms", len(formNotes), len(locatorForms)+len(bankForms))
	}
	docSection(t, "forms", b.String())
}

var (
	reAlt       = regexp.MustCompile(`^\^?(.*?)\$?$`)
	reAnyPrefix = regexp.MustCompile(`\(\?:\.\* \)\?`)
)

// boardsOf writes the products and boards a family pattern matches, where
// the pattern is a list of names, else the pattern.
func boardsOf(pattern string) string {
	var out []string
	depth := 0
	start := 0
	var alts []string
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++
		case '(':
			depth++
		case ')':
			depth--
		case '|':
			if depth == 0 {
				alts = append(alts, pattern[start:i])
				start = i + 1
			}
		}
	}
	alts = append(alts, pattern[start:])
	for _, a := range alts {
		a = reAlt.FindStringSubmatch(a)[1]
		a = reAnyPrefix.ReplaceAllString(a, "")
		product, board, _ := strings.Cut(a, `\|`)
		clean := func(s string) string {
			s = strings.NewReplacer(`\.`, ".", `\(`, "(", `\)`, ")", `\[`, "[", `\]`, "]", `\-`, "-", `\#`, "#", `\+`, "+", `\/`, "/", `\b`, "").Replace(s)
			return strings.TrimSpace(s)
		}
		p, bd := clean(product), clean(board)
		if strings.ContainsAny(p+bd, `\()|?*^$[`) {
			return "`" + cell(pattern) + "`"
		}
		switch {
		case p != "" && bd != "":
			out = append(out, p+" (board "+bd+")")
		case p != "":
			out = append(out, p)
		case bd != "":
			out = append(out, "board "+bd)
		default:
			out = append(out, "no product and no board")
		}
	}
	seen := map[string]bool{}
	var uniq []string
	for _, o := range out {
		if !seen[o] {
			seen[o] = true
			uniq = append(uniq, o)
		}
	}
	return strings.Join(uniq, ", ")
}

// ruleOf says in words what a family row gives.
func ruleOf(f family) string {
	var w []string
	if v, ok := f.when["S"]; ok {
		w = append(w, strings.Join(v, " or ")+" processor records")
	}
	if v, ok := f.when["slots"]; ok {
		w = append(w, strings.Join(v, " or ")+" memory devices")
	}
	if v, ok := f.when["per"]; ok {
		w = append(w, strings.Join(v, " or ")+" slots a processor")
	}
	if v, ok := f.when["cpu"]; ok {
		w = append(w, "processor "+strings.Join(v, " or "))
	}
	cond := ""
	if len(w) > 0 {
		cond = "; with " + strings.Join(w, ", ")
	}
	chans := ""
	if len(f.channels) > 0 {
		var names []string
		for _, c := range f.channels {
			if strings.HasPrefix(c, "?") {
				c = "unnamed"
			}
			names = append(names, c)
		}
		chans = "; channels " + strings.Join(names, ", ")
	}
	socket := map[string]string{"cpu": "the processor word of the names", "letter": "the letter (A the first processor)", "bank": "the bank locator",
		"one": "the one processor", "hub": "the memory controller of the chipset", "global": "the table"}[f.socket]
	arg := func() string {
		if c, p, ok := strings.Cut(f.arg, ","); ok {
			return fmt.Sprintf("; %s channels of %s slots", c, p)
		}
		return ""
	}
	switch f.kind {
	case "table":
		second := ""
		for s := range f.maps {
			if s >= 0 {
				second = ", another table for the second processor"
			}
		}
		return fmt.Sprintf("slot number to channel and position on %s%s%s%s", socket, second, chans, cond)
	case "global":
		return "slot numbers that run over the processors, to processor, channel and position" + chans + cond
	case "cards":
		return "memory cards, " + f.arg + " to a processor; slot number on the card to channel and position" + cond
	case "names":
		s := "the names place the slots"
		if f.sockmap != nil {
			var m []string
			for k, v := range f.sockmap {
				m = append(m, fmt.Sprintf("%s%d is processor %d", k.w, k.n, v))
			}
			sort.Strings(m)
			s += "; " + strings.Join(m, ", ")
		}
		return s + arg() + cond
	case "hub":
		return "memory on the chipset, the names place the slots" + arg() + cond
	case "labels":
		return fmt.Sprintf("labels of the manual for %d slots, given to empty slots without a name%s", len(strings.Fields(f.rawMap)), cond)
	case "phantoms":
		return "records without a slot on the board: " + strings.ReplaceAll(f.rawMap, ",", ", ")
	case "alias":
		return "a board known by its part number: the rule of `" + f.arg + "`"
	case "aside":
		return "set aside: " + f.arg
	}
	return f.kind
}

// The table of board families in the docs is the table of the code, and
// every document it cites is listed with title, number and pages.
func TestFamiliesInTheDocs(t *testing.T) {
	var b strings.Builder
	b.WriteString("| Family | Maker | Boards | What the table gives | Documents |\n| --- | --- | --- | --- | --- |\n")
	for _, f := range families {
		docs := "none found"
		if len(f.source) > 0 {
			docs = "`" + strings.Join(f.source, "`, `") + "`"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s |\n", f.id, f.maker, boardsOf(f.match), cell(ruleOf(f)), docs)
	}
	docSection(t, "families", b.String())
	var d strings.Builder
	d.WriteString("| Document | Maker | Title | Number or revision | Date | Pages | Address |\n| --- | --- | --- | --- | --- | --- | --- |\n")
	for _, id := range sourceOrder {
		s := sources[id]
		addr := "not recorded"
		if s.URL != "" {
			addr = "<" + s.URL + ">"
		}
		fmt.Fprintf(&d, "| `%s` | %s | %s | %s | %s | %s | %s |\n", s.ID, s.Maker, cell(s.Title), cell(s.Number), cell(s.Date), cell(s.Pages), addr)
	}
	docSection(t, "documents", d.String())
}

// The counts of the public collection in the docs are those of the golden
// of TestPopulationFull.
func TestPopulationCountsInTheDocs(t *testing.T) {
	b, err := os.ReadFile("testdata/population-boards.txt")
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	table := 0
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		if line == "" {
			table++
			continue
		}
		p := strings.Split(line, "\t")
		switch {
		case p[0] == "maker" || p[0] == "reason" || p[0] == "fixes":
			heads := map[string]string{"maker": "| Maker | Dumps | Every slot placed, first version | Every slot placed, first round of fixes | Placed in full | Separate list |\n| --- | ---: | ---: | ---: | ---: | ---: |\n",
				"reason": "\n| Reason | Dumps |\n| --- | ---: |\n",
				"fixes":  "\n| Socket count unknown | Every slot placed | Some placed | Without channels | All set aside | A CPU the dump lacks |\n| --- | ---: | ---: | ---: | ---: | ---: |\n"}
			out.WriteString(heads[p[0]])
		default:
			for i := range p {
				if n, err := strconv.Atoi(p[i]); err == nil {
					p[i] = commas(n)
				} else if table == 1 {
					p[i] = "`" + p[i] + "`"
				}
			}
			out.WriteString("| " + strings.Join(p, " | ") + " |\n")
		}
	}
	docSection(t, "population", out.String())
}
