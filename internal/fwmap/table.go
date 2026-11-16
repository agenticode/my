// Package fwmap maps the messages of every BMC and BIOS vendor to the
// common events of package fwevent. A table per vendor or standard holds
// facts: message IDs, the role of each argument, the vendor's severity,
// the common event, and per line the source it was read from. It holds no
// sentence of a vendor. The classifier asks the tables, then the wording
// rules, in a fixed order.
package fwmap

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/agenticode/my/internal/fwevent"
	"github.com/agenticode/my/internal/yamlite"
)

// Bases: how far a table, or a line, was checked.
const (
	BasisCapture   = "capture"
	BasisSample    = "sample"
	BasisRegistry  = "registry"
	BasisDocuments = "documents"
)

// Unchecked reports whether a basis is short of a capture of a real
// machine: such a mapping is "not checked on real hardware".
func Unchecked(basis string) bool { return basis != BasisCapture }

// MaxPattern is the size of the largest text pattern of a table.
const MaxPattern = 256

// Roles of the arguments of a message.
var roles = []string{"location", "locations", "socket", "channel", "position", "rank", "subrank", "device", "bank", "row",
	"column", "address", "count", "serial", "part", "board", "code", "channel-valid", "position-valid", "-"}

// Key extractors and location forms a table can name.
var (
	extractors = []string{"message-id", "event-id", "text", "ipmi"}
	forms      = []string{"plain", "openbmc"}
)

// Source is a document or file the lines of a table rest on.
type Source struct {
	Title string
	URL   string
	Read  string
}

// Applies says when a table is tried. Conditions are alternatives.
type Applies struct {
	// Prefixes are registry prefixes of a MessageId.
	Prefixes []string
	// AnyRegistry: the table holds for a MessageId of any registry, after
	// the tables that name its prefix. A fork of a registry renames the
	// prefix and keeps the names.
	AnyRegistry bool
	// IPMI: the table holds for the states of IPMI sensors.
	IPMI bool
}

// Line maps one message, or one state, to a common event.
type Line struct {
	ID string
	// NArgs, when not zero, says that the line holds for entries with this
	// many arguments.
	NArgs int
	// ArgsLike, when set, holds only when the first argument matches.
	ArgsLike *regexp.Regexp
	// Origin "memory": the line holds only when the entry is about a
	// memory resource.
	Origin string
	Event  fwevent.Name
	Flags  []fwevent.Flag
	Clears fwevent.Name
	Args   []string
	// Words are the words of the ledger for the record, when the common
	// event says less than the message: "parity error".
	Words    string
	Severity string
	Sources  []string
	Basis    string
	// Line is the line of the file the entry starts on.
	Line  int
	table *Table
}

// Ref names the line: table, version and key, as "dmtf@1:MemoryParityError".
func (l *Line) Ref() string { return fmt.Sprintf("%s@%d:%s", l.table.Name, l.table.Version, l.ID) }

// Table returns the table of the line.
func (l *Line) Table() *Table { return l.table }

// BasisOf returns the basis of the line: its own, or the table's.
func (l *Line) BasisOf() string {
	if l.Basis != "" {
		return l.Basis
	}
	return l.table.Basis
}

// Table is a mapping table.
type Table struct {
	Name     string
	Version  int
	Vendor   string
	Basis    string
	Applies  Applies
	Key      string
	Location string
	Sources  map[string]Source
	// SourceOrder keeps the order of the sources of the file.
	SourceOrder []string
	Skip        []string
	// MemoryKeys are prefixes of keys that are about memory whether or
	// not the table has a line for them: such a record without a line is
	// unclassified, never left out.
	MemoryKeys []string
	Lines      []*Line
	// User: the table was given with --event-map.
	User bool

	index map[string][]*Line
}

// lookup returns the lines of a key, with variants by argument count and
// by the first argument first.
func (t *Table) lookup(key string) []*Line { return t.index[key] }

// Parse reads a table. A table that cannot be read as a whole is an error;
// lines that cannot be read are left out and returned as errors of their
// own, with their line numbers, so that the other lines hold.
func Parse(data []byte) (*Table, []error, error) {
	root, err := yamlite.Parse(data)
	if err != nil {
		return nil, nil, err
	}
	if err := root.Mapping("table", "format", "table", "version", "vendor", "basis", "applies", "key", "location", "sources",
		"skip", "memory-keys", "messages"); err != nil {
		return nil, nil, err
	}
	get := func(k string) string {
		if n, ok := root.Map[k]; ok && n.Kind == yamlite.Scalar {
			return n.Scalar
		}
		return ""
	}
	if get("format") != "1" {
		return nil, nil, errors.New(`no "format: 1" line: not a mapping table of firmware messages`)
	}
	t := &Table{Name: get("table"), Vendor: get("vendor"), Basis: get("basis"), Key: get("key"), Location: get("location"),
		Sources: map[string]Source{}, index: map[string][]*Line{}}
	if !nameRE.MatchString(t.Name) {
		return nil, nil, fmt.Errorf("table: %q is no table name (lower-case letters, digits and dashes)", t.Name)
	}
	if t.Version, err = strconv.Atoi(get("version")); err != nil || t.Version < 1 {
		return nil, nil, fmt.Errorf("table %s: version %q is not a whole number of at least 1", t.Name, get("version"))
	}
	if !slices.Contains([]string{BasisCapture, BasisSample, BasisRegistry, BasisDocuments}, t.Basis) {
		return nil, nil, fmt.Errorf("table %s: basis %q is none of capture, sample, registry, documents", t.Name, t.Basis)
	}
	if !slices.Contains(extractors, t.Key) {
		return nil, nil, fmt.Errorf("table %s: key %q is none of %s", t.Name, t.Key, strings.Join(extractors, ", "))
	}
	if t.Location == "" {
		t.Location = "plain"
	}
	if !slices.Contains(forms, t.Location) {
		return nil, nil, fmt.Errorf("table %s: location %q is none of %s", t.Name, t.Location, strings.Join(forms, ", "))
	}
	if a, ok := root.Map["applies"]; ok {
		if err := a.Mapping("applies", "message-id-prefix", "bare-id", "any-registry", "ipmi"); err != nil {
			return nil, nil, err
		}
		if p, ok := a.Map["message-id-prefix"]; ok {
			if t.Applies.Prefixes, err = texts(p, "message-id-prefix"); err != nil {
				return nil, nil, err
			}
		}
		t.Applies.AnyRegistry = a.Map["any-registry"] != nil && a.Map["any-registry"].Scalar == "yes"
		if a.Map["bare-id"] != nil && a.Map["bare-id"].Scalar == "yes" {
			// The table also holds for a MessageId that names no registry.
			t.Applies.Prefixes = append(t.Applies.Prefixes, "")
		}
		t.Applies.IPMI = a.Map["ipmi"] != nil && a.Map["ipmi"].Scalar == "yes"
	}
	if s, ok := root.Map["sources"]; ok {
		if err := s.Mapping("sources"); err != nil {
			return nil, nil, err
		}
		for _, k := range s.Keys {
			n := s.Map[k]
			if err := n.Mapping("source "+k, "title", "url", "read"); err != nil {
				return nil, nil, err
			}
			src := Source{}
			for f, dst := range map[string]*string{"title": &src.Title, "url": &src.URL, "read": &src.Read} {
				if v, ok := n.Map[f]; ok {
					*dst = v.Scalar
				}
			}
			if src.Title == "" || src.URL == "" {
				return nil, nil, yamlite.ErrAt(n.Line, "source %s: a source needs a title and a URL", k)
			}
			t.Sources[k] = src
			t.SourceOrder = append(t.SourceOrder, k)
		}
	}
	if s, ok := root.Map["skip"]; ok {
		if t.Skip, err = texts(s, "skip"); err != nil {
			return nil, nil, err
		}
	}
	if s, ok := root.Map["memory-keys"]; ok {
		if t.MemoryKeys, err = texts(s, "memory-keys"); err != nil {
			return nil, nil, err
		}
	}
	var lineErrs []error
	if m, ok := root.Map["messages"]; ok {
		items, err := m.Items("messages")
		if err != nil {
			return nil, nil, err
		}
		for _, it := range items {
			l, err := t.parseLine(it)
			if err != nil {
				lineErrs = append(lineErrs, err)
				continue
			}
			t.Lines = append(t.Lines, l)
		}
	}
	// One line per key, argument count and first-argument pattern.
	seen := map[string]int{}
	var kept []*Line
	for _, l := range t.Lines {
		like := ""
		if l.ArgsLike != nil {
			like = l.ArgsLike.String()
		}
		k := fmt.Sprintf("%s\x00%d\x00%s", l.ID, l.NArgs, like)
		if first, dup := seen[k]; dup {
			lineErrs = append(lineErrs, yamlite.ErrAt(l.Line, "message %s: given twice (first on line %d)", l.ID, first))
			continue
		}
		seen[k] = l.Line
		kept = append(kept, l)
	}
	t.Lines = kept
	for _, l := range t.Lines {
		t.index[l.ID] = append(t.index[l.ID], l)
	}
	for k := range t.index {
		// Lines with a pattern on the first argument, then lines for a
		// number of arguments, then the line for any entry.
		slices.SortStableFunc(t.index[k], func(a, b *Line) int {
			rank := func(l *Line) int {
				switch {
				case l.ArgsLike != nil:
					return 0
				case l.NArgs != 0:
					return 1
				}
				return 2
			}
			return rank(a) - rank(b)
		})
	}
	return t, lineErrs, nil
}

var nameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

func texts(n *yamlite.Node, what string) ([]string, error) {
	if n.Kind == yamlite.Scalar {
		if n.Scalar == "" {
			return nil, nil
		}
		return []string{n.Scalar}, nil
	}
	items, err := n.Items(what)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		s, err := it.Text(what)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// list splits "a, b, c".
func list(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (t *Table) parseLine(n *yamlite.Node) (*Line, error) {
	if err := n.Mapping("message", "id", "nargs", "args-like", "origin", "event", "flags", "clears", "args", "words", "severity",
		"source", "basis"); err != nil {
		return nil, err
	}
	get := func(k string) string {
		if v, ok := n.Map[k]; ok && v.Kind == yamlite.Scalar {
			return v.Scalar
		}
		return ""
	}
	l := &Line{ID: get("id"), Origin: get("origin"), Event: fwevent.Name(get("event")), Clears: fwevent.Name(get("clears")),
		Args: list(get("args")), Words: get("words"), Severity: get("severity"), Sources: list(get("source")), Basis: get("basis"), Line: n.Line, table: t}
	at := func(format string, a ...any) error {
		return yamlite.ErrAt(n.Line, "message %s: %s", l.ID, fmt.Sprintf(format, a...))
	}
	if l.ID == "" || len(l.ID) > 128 {
		return nil, at("a line needs an id of at most 128 bytes")
	}
	if s := get("nargs"); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil || v < 0 || v > 32 {
			return nil, at("nargs %q is not a number from 0 to 32", s)
		}
		l.NArgs = v
	}
	if p := get("args-like"); p != "" {
		if len(p) > MaxPattern {
			return nil, at("the pattern is longer than %d bytes", MaxPattern)
		}
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, at("the pattern does not compile: %v", err)
		}
		l.ArgsLike = re
	}
	if l.Origin != "" && l.Origin != "memory" {
		return nil, at("origin %q is not memory", l.Origin)
	}
	if !l.Event.Valid() {
		return nil, at("%q is not a common event", l.Event)
	}
	if l.Clears != "" && (!l.Clears.Valid() || l.Event != fwevent.Cleared) {
		return nil, at("clears %q needs the event cleared and a common event", l.Clears)
	}
	for _, f := range list(get("flags")) {
		if !fwevent.Flag(f).Valid() {
			return nil, at("%q is not a flag", f)
		}
		l.Flags = append(l.Flags, fwevent.Flag(f))
	}
	for _, r := range l.Args {
		if !slices.Contains(roles, r) {
			return nil, at("argument role %q is none of %s", r, strings.Join(roles, ", "))
		}
	}
	if len(l.Sources) == 0 {
		return nil, at("a line needs the source it was read from")
	}
	for _, s := range l.Sources {
		if _, ok := t.Sources[s]; !ok {
			return nil, at("source %q is not among the sources of the table", s)
		}
	}
	if l.Basis != "" && !slices.Contains([]string{BasisCapture, BasisSample, BasisRegistry, BasisDocuments}, l.Basis) {
		return nil, at("basis %q is none of capture, sample, registry, documents", l.Basis)
	}
	return l, nil
}
