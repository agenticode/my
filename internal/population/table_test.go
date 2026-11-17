package population

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/explorer"
	"github.com/agenticode/my/internal/testutil"
)

// collectionBoard is one slot list of the public collection
// (testdata/explorer/population): its family and, per socket, the places
// the board gives its slots.
type collectionBoard struct {
	list            int
	family          string
	product, board  string
	sockets         int
	places          map[int]map[Place]bool
	placedInFull    bool
	slotsPerProcess map[int]int
}

// readCollection reads the slot lists and the board family of each.
func readCollection(t *testing.T) []collectionBoard {
	t.Helper()
	root := testutil.Testdata(t, "explorer", "population")
	fams := map[int]string{}
	b, err := os.ReadFile(filepath.Join(root, "dumps.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		p := strings.Split(line, "\t")
		if len(p) != 3 || strings.HasPrefix(line, "#") || p[0] == "dump" {
			continue
		}
		n, _ := strconv.Atoi(p[1])
		if p[2] != "" {
			fams[n] = p[2]
		}
	}
	f, err := os.Open(filepath.Join(root, "slots.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	byList := map[int]*collectionBoard{}
	var order []int
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		p := strings.Split(sc.Text(), "\t")
		if len(p) != 23 || strings.HasPrefix(p[0], "#") || p[0] == "list" {
			continue
		}
		n, _ := strconv.Atoi(p[0])
		cb := byList[n]
		if cb == nil {
			s, _ := strconv.Atoi(p[2])
			cb = &collectionBoard{list: n, family: fams[n], product: p[4], board: p[5], sockets: s, places: map[int]map[Place]bool{},
				placedInFull: true, slotsPerProcess: map[int]int{}}
			byList[n] = cb
			order = append(order, n)
		}
		if p[15] == "not-a-slot" {
			continue
		}
		if p[15] != "placed" {
			cb.placedInFull = false
			continue
		}
		sock := p[17]
		if sock == "hub" {
			sock = "0" // memory on the chipset: the board draws socket 0
		}
		s, e1 := strconv.Atoi(sock)
		c, e2 := strconv.Atoi(p[18])
		pos, e3 := strconv.Atoi(p[20])
		if e1 != nil || e2 != nil || e3 != nil {
			continue
		}
		if cb.places[s] == nil {
			cb.places[s] = map[Place]bool{}
		}
		cb.places[s][Place{c, pos}] = true
		cb.slotsPerProcess[s]++
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	out := make([]collectionBoard, 0, len(order))
	for _, n := range order {
		out = append(out, *byList[n])
	}
	return out
}

// procPlaces are the places of the Nth processor (from 1) of a board.
func (cb collectionBoard) procPlaces(n int) map[Place]bool {
	var socks []int
	for s := range cb.places {
		socks = append(socks, s)
	}
	sort.Ints(socks)
	if n < 1 || n > len(socks) {
		return nil
	}
	return cb.places[socks[n-1]]
}

// TestSetsFitTheirBoards: every place of every table of a rule set exists
// on a board of its family in the public collection, for the processor the
// table is about.
func TestSetsFitTheirBoards(t *testing.T) {
	boards := readCollection(t)
	byFamily := map[string][]collectionBoard{}
	for _, b := range boards {
		if b.family != "" {
			byFamily[b.family] = append(byFamily[b.family], b)
		}
	}
	for _, s := range Sets() {
		fams := s.When["family"]
		if len(fams) == 0 {
			t.Errorf("%s: a rule set without a board family", s.ID)
			continue
		}
		var of []collectionBoard
		for _, f := range fams {
			of = append(of, byFamily[f]...)
		}
		if len(of) == 0 {
			t.Errorf("%s: no board of the collection has its family", s.ID)
			continue
		}
		for _, tables := range [][]Table{s.Order, s.Alt} {
			for _, tb := range tables {
				proc := max(tb.Proc, 1)
				fits := false
				var missing []string
				for _, b := range of {
					have := b.procPlaces(proc)
					var miss []string
					for _, p := range tb.Places() {
						if !have[p] {
							miss = append(miss, p.String())
						}
					}
					if len(miss) == 0 {
						fits = true
						break
					}
					if missing == nil || len(miss) < len(missing) {
						missing = miss
					}
				}
				if !fits {
					t.Errorf("%s: no board of its family has the places %v (processor %d)", s.ID, missing, proc)
				}
			}
		}
	}
}

// TestSetsCiteKnownPages: every page a rule set cites is of a document of
// sources.tsv, every order has its strength and every rule its page.
func TestSetsCiteKnownPages(t *testing.T) {
	if len(Sets()) < 200 {
		t.Fatalf("only %d rule sets", len(Sets()))
	}
	for _, s := range Sets() {
		refs := append([]Ref{}, s.Source...)
		for _, id := range Rules {
			refs = append(refs, s.Rules[id].Source...)
		}
		for _, r := range refs {
			if _, ok := explorer.SourceByID(r.ID); !ok {
				t.Errorf("%s: %s is not in sources.tsv", s.ID, r.ID)
			}
		}
		if len(s.Order) > 0 && (s.Strength == "" || len(s.Source) == 0) {
			t.Errorf("%s: an order without its strength or page", s.ID)
		}
		for id, r := range s.Rules {
			if r.Strength != Required && r.Strength != Recommended || len(r.Source) == 0 {
				t.Errorf("%s: %s without its strength or page", s.ID, id)
			}
		}
		for _, tb := range append(append([]Table{}, s.Order...), s.Alt...) {
			for n, ps := range tb.Sets {
				if len(ps) != n {
					t.Errorf("%s: a row of %d with %d places", s.ID, n, len(ps))
				}
			}
			if dup := duplicate(tb.Seq); dup != "" {
				t.Errorf("%s: %s twice in an order", s.ID, dup)
			}
		}
	}
}

// TestOneSetPerBoard: no two rule sets match one board of the collection,
// unless the first of them is the narrower (it holds more conditions).
func TestOneSetPerBoard(t *testing.T) {
	sets := Sets()
	matched := map[string]int{}
	for _, cb := range readCollection(t) {
		b := Board{Maker: "", Product: cb.product, BoardName: cb.board, Family: cb.family, Sockets: cb.sockets}
		var ids []int
		for i := range sets {
			s := sets[i]
			s.Maker = "*" // the maker is that of the family row, which already matched
			if s.matches(&b, cb.slotsPerProcess) {
				ids = append(ids, i)
			}
		}
		if len(ids) > 1 && len(sets[ids[0]].When) <= len(sets[ids[1]].When) {
			t.Errorf("list %d: %s and %s both match", cb.list, sets[ids[0]].ID, sets[ids[1]].ID)
		}
		if len(ids) > 0 {
			matched[sets[ids[0]].ID]++
		}
	}
	for _, s := range sets {
		if matched[s.ID] == 0 {
			t.Errorf("%s matches no board of the collection", s.ID)
		}
	}
}

// docSection checks the text between <!-- name --> and <!-- /name --> of
// docs/rules.md; UPDATE_GOLDEN=1 writes it.
func docSection(t *testing.T, name, want string) {
	t.Helper()
	path := filepath.Join(testutil.RepoRoot(t), "docs", "rules.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	open, end := "<!-- "+name+" -->\n", "<!-- /"+name+" -->"
	i, j := strings.Index(text, open), strings.Index(text, end)
	if i < 0 || j < i {
		t.Fatalf("docs/rules.md has no section %s", name)
	}
	if got := text[i+len(open) : j]; got != want {
		if os.Getenv("UPDATE_GOLDEN") != "" {
			if err := os.WriteFile(path, []byte(text[:i+len(open)]+want+text[j:]), 0o644); err != nil {
				t.Fatal(err)
			}
			return
		}
		t.Errorf("the section %s of docs/rules.md differs from the code (run with UPDATE_GOLDEN=1 after checking the change)", name)
	}
}

func cell(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

// tableText says what a table gives: a fill order of n slots, or the
// numbers of modules it lists.
func tableText(t Table) string {
	var s string
	if t.Seq != nil {
		s = fmt.Sprintf("order of %d slots", len(t.Seq))
	} else {
		var ns []string
		for _, n := range t.Counts(nil) {
			ns = append(ns, strconv.Itoa(n))
		}
		s = strings.Join(ns, ", ") + " modules"
	}
	if t.Proc > 0 {
		s = fmt.Sprintf("processor %d: %s", t.Proc, s)
	}
	if t.Mode != "" {
		s += " (" + t.Mode + ")"
	}
	return s
}

func short(st string) string {
	if st == Required {
		return "required"
	}
	return "recommended"
}

// The rule sets and their documents in docs/rules.md are those of the
// tables of the code.
func TestSetsInTheDocs(t *testing.T) {
	var b strings.Builder
	b.WriteString("| Rule set | First slot | Table | Order | Other tables | Rules | Documents |\n| --- | --- | --- | --- | --- | --- | --- |\n")
	cited := map[string]map[string]bool{}
	var docs []string
	cite := func(refs []Ref) []string {
		var out []string
		for _, r := range refs {
			if cited[r.ID] == nil {
				cited[r.ID] = map[string]bool{}
				docs = append(docs, r.ID)
			}
			cited[r.ID][r.Pages] = true
			out = append(out, "`"+r.ID+"` "+r.Pages)
		}
		return out
	}
	for _, s := range Sets() {
		first := map[string]string{"0": "position 0", "last": "last position", "": "-"}[s.First]
		table, order, alt := "-", "-", "-"
		var tt []string
		for _, x := range s.Order {
			tt = append(tt, tableText(x))
		}
		if len(tt) > 0 {
			table, order = strings.Join(tt, "; "), short(s.Strength)
		}
		var at []string
		for _, x := range s.Alt {
			at = append(at, tableText(x))
		}
		if len(at) > 0 {
			alt = strings.Join(at, "; ")
		}
		var rules []string
		var refs []string
		refs = append(refs, cite(s.Source)...)
		for _, id := range Rules {
			r, ok := s.Rules[id]
			if !ok {
				continue
			}
			rules = append(rules, id+" "+short(r.Strength))
			refs = append(refs, cite(r.Source)...)
		}
		seen := map[string]bool{}
		var uniq []string
		for _, r := range refs {
			if !seen[r] {
				seen[r] = true
				uniq = append(uniq, r)
			}
		}
		rs := "-"
		if len(rules) > 0 {
			rs = strings.Join(rules, ", ")
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s | %s | %s |\n", s.ID, first, table, order, alt, rs, strings.Join(uniq, ", "))
	}
	docSection(t, "population-sets", b.String())

	var d strings.Builder
	d.WriteString("| Document | Maker | Title | Number or revision | Date | Pages cited | Address |\n| --- | --- | --- | --- | --- | --- | --- |\n")
	sort.Strings(docs)
	for _, id := range docs {
		s, _ := explorer.SourceByID(id)
		var pages []string
		for p := range cited[id] {
			pages = append(pages, p)
		}
		sort.Slice(pages, func(a, b int) bool { return pageKey(pages[a]) < pageKey(pages[b]) })
		addr := "not recorded"
		if s.URL != "" {
			addr = "<" + s.URL + ">"
		}
		fmt.Fprintf(&d, "| `%s` | %s | %s | %s | %s | %s | %s |\n", id, s.Maker, cell(s.Title), cell(s.Number), cell(s.Date),
			strings.ReplaceAll(strings.Join(pages, ", "), "topic", "the whole page"), addr)
	}
	docSection(t, "population-documents", d.String())
}

var reLead = regexp.MustCompile(`^\d+`)

func pageKey(p string) string {
	m := reLead.FindString(p)
	return fmt.Sprintf("%08s%s", m, p)
}
