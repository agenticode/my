package population

import (
	_ "embed"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

//go:embed sets.tsv
var setsTSV string

// Set is one rule set: a row of sets.tsv.
type Set struct {
	ID    string
	Maker string
	// Match is the pattern over "system product|board" as the table
	// writes it; When the conditions of the row.
	Match string
	re    *regexp.Regexp
	When  map[string][]string
	// First is the slot of a channel the documents fill first: "0" (the
	// position 0), "last" (the highest position), or "" when they do not
	// say.
	First string
	// Order holds the fill order of the default memory mode, one table per
	// processor that differs; Strength is the strength of its wording and
	// Source where it stands.
	Order    []Table
	Strength string
	Source   []Ref
	// Alt holds the tables of the other memory modes and the second sets
	// a document allows for one count.
	Alt []Table
	// Rules are the rules beside the order the documents state, by id.
	Rules map[string]Rule
}

// Table is one table of a rule set.
type Table struct {
	// Proc is the processor the table is about, from 1 in the order of the
	// sockets of the board; 0 when it is the table of every processor that
	// has none of its own.
	Proc int
	// Mode is the memory mode the table is written for (the states of
	// internal/memras: independent, advanced-ecc, ...), "" when the
	// document gives one table for every mode.
	Mode string
	// Seq is one fill order: n modules stand in its first n places that
	// exist on the board. Sets are the places per number of modules.
	Seq  []Place
	Sets map[int][]Place
}

// Place is a slot of a processor: its channel, counted from 0 among the
// channels of the processor, and its position in the channel from 0.
type Place struct{ Channel, Position int }

func (p Place) String() string { return strconv.Itoa(p.Channel) + "." + strconv.Itoa(p.Position) }

// Rule is a rule a rule set states: the strength of the document's
// wording and the pages it stands on.
type Rule struct {
	Strength string
	Source   []Ref
}

// Ref is a page of a document of internal/explorer/sources.tsv.
type Ref struct {
	ID    string `json:"id"`
	Pages string `json:"pages"`
}

// Strengths, as the documents word a rule.
const (
	Required    = "required"
	Recommended = "recommended"
)

// The rule ids (docs/rules.md, "Population rules").
const (
	Order    = "P-ORDER-01"
	First    = "P-FIRST-01"
	CPU      = "P-CPU-01"
	Balance  = "P-BAL-01"
	MixType  = "P-MIX-01"
	MixRank  = "P-MIX-02"
	MixSize  = "P-MIX-03"
	MixSpeed = "P-MIX-04"
	MixPart  = "P-MIX-05"
)

// Rules are the rule ids in the order the screens list them.
var Rules = []string{Order, First, CPU, Balance, MixType, MixRank, MixSize, MixSpeed, MixPart}

var sets []Set

func init() {
	var err error
	if sets, err = ParseSets(setsTSV); err != nil {
		panic(err)
	}
}

// Sets are the rule sets of sets.tsv, in table order.
func Sets() []Set { return sets }

// setColumns are the columns of sets.tsv.
var setColumns = []string{"id", "maker", "match", "when", "first", "order", "strength", "alt", "rules", "source"}

// ParseSets reads rule sets in the form of sets.tsv: a header line naming
// the columns, lines starting with # are comments, "-" stands for an
// empty field.
func ParseSets(text string) ([]Set, error) {
	var out []Set
	ids := map[string]bool{}
	header := false
	for n, line := range strings.Split(text, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if !header {
			if strings.Join(f, " ") != strings.Join(setColumns, " ") {
				return nil, fmt.Errorf("sets: line %d: the header is not %s", n+1, strings.Join(setColumns, " "))
			}
			header = true
			continue
		}
		if len(f) != len(setColumns) {
			return nil, fmt.Errorf("sets: line %d: %d fields, want %d", n+1, len(f), len(setColumns))
		}
		for i := range f {
			if f[i] = strings.TrimSpace(f[i]); f[i] == "-" {
				f[i] = ""
			}
		}
		s, err := parseSet(f)
		if err != nil {
			return nil, fmt.Errorf("sets: line %d (%s): %w", n+1, f[0], err)
		}
		if ids[s.ID] {
			return nil, fmt.Errorf("sets: line %d: the id %s stands twice", n+1, s.ID)
		}
		ids[s.ID] = true
		out = append(out, s)
	}
	return out, nil
}

func parseSet(f []string) (Set, error) {
	s := Set{ID: f[0], Maker: f[1], Match: f[2], When: map[string][]string{}, First: f[4], Rules: map[string]Rule{}}
	if s.ID == "" || s.Maker == "" || s.Match == "" {
		return s, fmt.Errorf("id, maker and match are needed")
	}
	re, err := regexp.Compile("(?i)" + s.Match)
	if err != nil {
		return s, fmt.Errorf("match: %w", err)
	}
	s.re = re
	for _, c := range strings.Split(f[3], ";") {
		if c = strings.TrimSpace(c); c == "" {
			continue
		}
		k, v, ok := strings.Cut(c, "=")
		if !ok || (k != "family" && k != "S" && k != "cpu" && k != "per") || v == "" {
			return s, fmt.Errorf("when: %q", c)
		}
		s.When[k] = strings.Split(v, "|")
	}
	switch s.First {
	case "", "0", "last":
	default:
		return s, fmt.Errorf("first: %q", s.First)
	}
	if s.Order, err = parseTables(f[5]); err != nil {
		return s, fmt.Errorf("order: %w", err)
	}
	if s.Alt, err = parseTables(f[7]); err != nil {
		return s, fmt.Errorf("alt: %w", err)
	}
	for _, a := range s.Alt {
		for _, t := range s.Order {
			if a.Mode != "" && t.Mode == "" {
				return s, fmt.Errorf("alt: a table of a memory mode needs the mode of the order")
			}
		}
	}
	seen := map[int]bool{}
	for _, t := range s.Order {
		if seen[t.Proc] {
			return s, fmt.Errorf("order: two tables of processor %d", t.Proc)
		}
		seen[t.Proc] = true
	}
	if len(s.Order) > 0 {
		if s.Strength, err = strength(f[6]); err != nil {
			return s, fmt.Errorf("strength: %w", err)
		}
		if s.Source, err = parseRefs(strings.Fields(f[9])); err != nil || len(s.Source) == 0 {
			return s, fmt.Errorf("source: the order needs a page of a document")
		}
	} else if f[6] != "" || f[9] != "" || len(s.Alt) > 0 {
		return s, fmt.Errorf("strength, source and alt go with an order")
	}
	for _, e := range strings.Split(f[8], ";") {
		if e = strings.TrimSpace(e); e == "" {
			continue
		}
		w := strings.Fields(e)
		id, st, ok := strings.Cut(w[0], "=")
		if !ok || !knownRule(id) || id == Order {
			return s, fmt.Errorf("rules: %q", w[0])
		}
		if _, dup := s.Rules[id]; dup {
			return s, fmt.Errorf("rules: %s stands twice", id)
		}
		r := Rule{}
		if r.Strength, err = strength(st); err != nil {
			return s, fmt.Errorf("rules: %s: %w", id, err)
		}
		if r.Source, err = parseRefs(w[1:]); err != nil || len(r.Source) == 0 {
			return s, fmt.Errorf("rules: %s needs a page of a document", id)
		}
		if (id == First || id == MixRank || id == MixSize) && s.First == "" {
			return s, fmt.Errorf("rules: %s needs the first slot of a channel", id)
		}
		s.Rules[id] = r
	}
	if len(s.Order) == 0 && len(s.Rules) == 0 {
		return s, fmt.Errorf("no order and no rule")
	}
	return s, nil
}

func knownRule(id string) bool {
	for _, r := range Rules {
		if r == id {
			return true
		}
	}
	return false
}

func strength(s string) (string, error) {
	switch s {
	case "req":
		return Required, nil
	case "rec":
		return Recommended, nil
	}
	return "", fmt.Errorf("%q is neither req nor rec", s)
}

var reRef = regexp.MustCompile(`^([a-z0-9][a-z0-9.+_-]*):([0-9][0-9A-Za-z,.-]*)$`)

func parseRefs(words []string) ([]Ref, error) {
	var out []Ref
	for _, w := range words {
		m := reRef.FindStringSubmatch(w)
		if m == nil {
			return nil, fmt.Errorf("source %q", w)
		}
		out = append(out, Ref{ID: m[1], Pages: m[2]})
	}
	return out, nil
}

// parseTables reads tables separated by "||": each "[pN: ][mode=M: ]"
// and "seq: c.p c.p ..." or "sets: n: c.p ...; n: c.p ...".
func parseTables(text string) ([]Table, error) {
	var out []Table
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	for _, part := range strings.Split(text, "||") {
		part = strings.TrimSpace(part)
		t := Table{}
		if strings.HasPrefix(part, "p") {
			h, rest, ok := strings.Cut(part, ":")
			n, err := strconv.Atoi(h[1:])
			if !ok || err != nil || n < 1 {
				return nil, fmt.Errorf("%q", part)
			}
			t.Proc, part = n, strings.TrimSpace(rest)
		}
		if strings.HasPrefix(part, "mode=") {
			h, rest, ok := strings.Cut(part, ":")
			if !ok || len(h) == len("mode=") {
				return nil, fmt.Errorf("%q", part)
			}
			t.Mode, part = h[len("mode="):], strings.TrimSpace(rest)
		}
		switch {
		case strings.HasPrefix(part, "seq:"):
			ps, err := parsePlaces(part[len("seq:"):])
			if err != nil || len(ps) == 0 {
				return nil, fmt.Errorf("seq %q", part)
			}
			if dup := duplicate(ps); dup != "" {
				return nil, fmt.Errorf("seq: %s stands twice", dup)
			}
			t.Seq = ps
		case strings.HasPrefix(part, "sets:"):
			t.Sets = map[int][]Place{}
			for _, e := range strings.Split(part[len("sets:"):], ";") {
				if e = strings.TrimSpace(e); e == "" {
					continue
				}
				h, rest, ok := strings.Cut(e, ":")
				n, err := strconv.Atoi(strings.TrimSpace(h))
				if !ok || err != nil || n < 1 {
					return nil, fmt.Errorf("sets %q", e)
				}
				ps, err := parsePlaces(rest)
				if err != nil {
					return nil, err
				}
				if len(ps) != n {
					return nil, fmt.Errorf("sets: the row of %d has %d places", n, len(ps))
				}
				if dup := duplicate(ps); dup != "" {
					return nil, fmt.Errorf("sets: the row of %d holds %s twice", n, dup)
				}
				if _, again := t.Sets[n]; again {
					return nil, fmt.Errorf("sets: two rows of %d", n)
				}
				t.Sets[n] = ps
			}
			if len(t.Sets) == 0 {
				return nil, fmt.Errorf("sets %q", part)
			}
		default:
			return nil, fmt.Errorf("%q is neither seq nor sets", part)
		}
		out = append(out, t)
	}
	return out, nil
}

func parsePlaces(text string) ([]Place, error) {
	var out []Place
	for _, w := range strings.Fields(text) {
		c, p, ok := strings.Cut(w, ".")
		ci, err1 := strconv.Atoi(c)
		pi, err2 := strconv.Atoi(p)
		if !ok || err1 != nil || err2 != nil || ci < 0 || pi < 0 {
			return nil, fmt.Errorf("place %q", w)
		}
		out = append(out, Place{ci, pi})
	}
	return out, nil
}

func duplicate(ps []Place) string {
	seen := map[Place]bool{}
	for _, p := range ps {
		if seen[p] {
			return p.String()
		}
		seen[p] = true
	}
	return ""
}

// Counts are the numbers of modules a table lists, for a processor whose
// slots are have: every row of sets, and for a seq 1 to the places of
// the order that exist.
func (t Table) Counts(have map[Place]bool) []int {
	if t.Seq != nil {
		n := 0
		for _, p := range t.Seq {
			if have[p] {
				n++
			}
		}
		out := make([]int, n)
		for i := range out {
			out[i] = i + 1
		}
		return out
	}
	var out []int
	for n := range t.Sets {
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}

// At is the places the table fills for n modules on a processor whose
// slots are have, and whether it lists n.
func (t Table) At(n int, have map[Place]bool) ([]Place, bool) {
	if t.Seq != nil {
		var out []Place
		for _, p := range t.Seq {
			if len(out) == n {
				break
			}
			if have[p] {
				out = append(out, p)
			}
		}
		return out, len(out) == n
	}
	ps, ok := t.Sets[n]
	return ps, ok
}

// Places are every place the table names.
func (t Table) Places() []Place {
	if t.Seq != nil {
		return t.Seq
	}
	var out []Place
	seen := map[Place]bool{}
	for _, n := range t.Counts(nil) {
		for _, p := range t.Sets[n] {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// forProc is the table of a processor (from 1) among tables: its own, else
// the one of every processor.
func forProc(tables []Table, proc int) *Table {
	var all *Table
	for i := range tables {
		switch tables[i].Proc {
		case proc:
			return &tables[i]
		case 0:
			if all == nil {
				all = &tables[i]
			}
		}
	}
	return all
}

// altsFor are the tables of alt a processor reads: its own and those of
// every processor.
func altsFor(tables []Table, proc int) []Table {
	var out []Table
	for _, t := range tables {
		if t.Proc == proc || t.Proc == 0 {
			out = append(out, t)
		}
	}
	return out
}

// matches tells whether a rule set speaks of a board.
func (s *Set) matches(b *Board, perSocket map[int]int) bool {
	if s.Maker != "*" && s.Maker != b.Maker {
		return false
	}
	if !s.re.MatchString(b.Product + "|" + b.BoardName) {
		return false
	}
	in := func(k, v string) bool {
		vals, ok := s.When[k]
		if !ok {
			return true
		}
		for _, x := range vals {
			if x == v {
				return true
			}
		}
		return false
	}
	if !in("family", b.Family) || !in("cpu", b.Processor) || !in("S", strconv.Itoa(b.Sockets)) {
		return false
	}
	if _, ok := s.When["per"]; ok {
		for _, n := range perSocket {
			if !in("per", strconv.Itoa(n)) {
				return false
			}
		}
	}
	return true
}
