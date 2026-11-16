package fwmap

import (
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/fwevent"
	"github.com/agenticode/my/internal/model"
)

//go:embed tables/*.yaml
var shippedFS embed.FS

// Entry is what a reader knows of one record when it asks what it means.
type Entry struct {
	// MessageID is the message key of a log entry, with its registry and
	// version ("OpenBMC.0.1.MemoryECCCorrectable").
	MessageID string
	// Message is the text of the entry.
	Message string
	Args    []string
	// MemoryOrigin says that the entry is about a memory resource: its
	// OriginOfCondition points at one.
	MemoryOrigin bool
	// IPMI is the state of an IPMI sensor ("memory-00"), for a record read
	// from SEL bytes or from the text of a SEL tool.
	IPMI string
	// Registry is what the BMC's own registry says of the MessageId, when
	// the dump holds that registry.
	Registry *RegistryMessage
}

// RegistryMessage is one message of a registry a BMC serves.
type RegistryMessage struct {
	Text     string
	Severity string
	// NArgs is the NumberOfArgs of the registry; -1 when not given.
	NArgs int
}

// Result is what a record means.
type Result struct {
	Event  fwevent.Name
	Flags  []fwevent.Flag
	Clears fwevent.Name
	// Mapping is how it was found: model.MappingTable, MappingRegistry,
	// MappingWording, MappingIPMI or MappingNone.
	Mapping string
	// Line is the table line that answered; Rule the wording rule. Table
	// is the table of the line, or the table that says that a key without
	// a line is about memory.
	Line  *Line
	Rule  *Rule
	Table *Table
	// Ref names the line or rule, Basis how far it was checked, Vendor the
	// vendor of its table.
	Ref, Basis, Vendor string
	// Registry, Version and Key are the parts of the MessageId.
	Registry, Version, Key string
	// Roles are the roles of the arguments in order; nil when the line
	// names none, or when the registry of the BMC gives another number of
	// arguments than the line knows.
	Roles []string
	// Severity is the vendor's severity the line or the registry names.
	Severity string
	// Memory says that the record is about memory at all; Skipped that a
	// table says it is not.
	Memory  bool
	Skipped bool
}

// Classifier asks the tables and the wording rules what a record means.
type Classifier struct {
	user    []*Table
	shipped []*Table
	wording *Wording
	// Notes say what could not be loaded: a shipped table that failed, the
	// lines of a user table that were left out.
	Notes []string
}

var (
	shippedOnce sync.Once
	shipped     *Classifier
	current     struct {
		sync.Mutex
		c *Classifier
	}
)

// Shipped returns the classifier of the tables this program ships.
func Shipped() *Classifier {
	shippedOnce.Do(func() { shipped = load() })
	return shipped
}

// Current returns the classifier the readers use: the shipped one, or the
// one Use set.
func Current() *Classifier {
	current.Lock()
	defer current.Unlock()
	if current.c != nil {
		return current.c
	}
	return Shipped()
}

// Use makes c the classifier of the readers; nil goes back to the shipped
// one.
func Use(c *Classifier) {
	current.Lock()
	current.c = c
	current.Unlock()
}

// load reads the shipped tables. A table that fails to load is left out
// with a note; the tests keep that from happening.
func load() *Classifier {
	c := &Classifier{}
	names, _ := fs.Glob(shippedFS, "tables/*.yaml")
	sort.Strings(names)
	for _, n := range names {
		data, err := shippedFS.ReadFile(n)
		if err != nil {
			c.Notes = append(c.Notes, fmt.Sprintf("%s: %v", n, err))
			continue
		}
		if strings.HasSuffix(n, "/wording.yaml") {
			w, err := ParseWording(data)
			if err != nil {
				c.Notes = append(c.Notes, fmt.Sprintf("the wording rules were left out: %v", err))
				continue
			}
			c.wording = w
			continue
		}
		t, lineErrs, err := Parse(data)
		if err != nil {
			c.Notes = append(c.Notes, fmt.Sprintf("the table %s was left out: %v", n, err))
			continue
		}
		for _, e := range lineErrs {
			c.Notes = append(c.Notes, fmt.Sprintf("the table %s: a line was left out: %v", t.Name, e))
		}
		c.shipped = append(c.shipped, t)
	}
	return c
}

// WithUser returns a classifier that asks the user tables before the
// shipped ones. A user table named like a shipped one holds where the
// shipped one holds, unless it says otherwise; its lines replace the
// shipped lines of the same key and add the keys the shipped one lacks.
// Lines that cannot be read are left out and named in the notes.
func (c *Classifier) WithUser(files map[string][]byte) (*Classifier, error) {
	out := &Classifier{shipped: c.shipped, wording: c.wording, Notes: slices.Clone(c.Notes)}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		t, lineErrs, err := Parse(files[name])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		for _, e := range lineErrs {
			out.Notes = append(out.Notes, fmt.Sprintf("%s: a line was left out: %v", name, e))
		}
		t.User = true
		if base := out.Table(t.Name); base != nil && len(t.Applies.Prefixes) == 0 && !t.Applies.AnyRegistry && !t.Applies.IPMI {
			t.Applies = base.Applies
		}
		out.user = append(out.user, t)
	}
	return out, nil
}

// Tables returns the shipped tables in name order, and Wording the
// wording rules.
func (c *Classifier) Tables() []*Table  { return c.shipped }
func (c *Classifier) Wording() *Wording { return c.wording }

// Table returns the shipped table of the name, or nil.
func (c *Classifier) Table(name string) *Table {
	for _, t := range c.shipped {
		if t.Name == name {
			return t
		}
	}
	return nil
}

// SplitMessageID splits "OpenBMC.0.1.MemoryECCCorrectable" into the
// registry, its version and the key. An ID without a registry is a key
// alone.
func SplitMessageID(id string) (registry, version, key string) {
	parts := strings.Split(strings.TrimSpace(id), ".")
	if len(parts) < 2 {
		return "", "", strings.TrimSpace(id)
	}
	key = parts[len(parts)-1]
	registry = parts[0]
	var v []string
	for _, p := range parts[1 : len(parts)-1] {
		v = append(v, p)
	}
	return registry, strings.Join(v, "."), key
}

// memoryText tells a text that names memory, a DIMM or ECC.
var memoryText = regexp.MustCompile(`(?i)\bmemory\b|\bdimm\w*|\becc\b`)

// Classify says what a record means, in the order of section 4.4 of the
// design: the state of an IPMI sensor; a line of a user table, then of a
// shipped table; the wording of the BMC's own registry text; the wording
// of the entry's text; unclassified when it is about memory.
func (c *Classifier) Classify(e Entry) (r Result) {
	return adapter.Guard(func() Result { return c.classify(e) })
}

func (c *Classifier) classify(e Entry) (r Result) {
	r.Mapping = model.MappingNone
	if e.IPMI != "" {
		for _, t := range c.ordered() {
			if !t.Applies.IPMI {
				continue
			}
			if l := pick(t.lookup(e.IPMI), e, len(e.Args)); l != nil {
				r.fromLine(l, e)
				r.Mapping = model.MappingIPMI
				return r
			}
		}
	}
	if e.MessageID != "" {
		r.Registry, r.Version, r.Key = SplitMessageID(e.MessageID)
		nargs := len(e.Args)
		if e.Registry != nil && e.Registry.NArgs >= 0 {
			nargs = e.Registry.NArgs
		}
		for _, t := range c.forRegistry(r.Registry) {
			if t.skips(r.Key) {
				r.Skipped = true
				return r
			}
			if l := pick(t.lookup(r.Key), e, nargs); l != nil {
				r.fromLine(l, e)
				r.Mapping = model.MappingTable
				// A line for another number of arguments, or a registry of
				// the BMC that gives another count than the roles name: the
				// roles are not used.
				if l.NArgs != 0 && l.NArgs != nargs {
					r.Roles = nil
				}
				if e.Registry != nil && e.Registry.NArgs >= 0 && len(l.Args) > 0 && e.Registry.NArgs != len(l.Args) {
					r.Roles = nil
				}
				if r.Severity == "" && e.Registry != nil {
					r.Severity = e.Registry.Severity
				}
				return r
			}
			if t.memoryKey(r.Key) && r.Table == nil {
				r.Table, r.Vendor, r.Basis = t, t.Vendor, t.Basis
			}
		}
	}
	key := strings.ToLower(e.MessageID)
	r.Memory = e.MemoryOrigin || r.Table != nil || memoryText.MatchString(e.Message) || strings.Contains(key, "memory") ||
		(e.Registry != nil && memoryText.MatchString(e.Registry.Text))
	if !r.Memory {
		return r
	}
	if c.wording != nil {
		if e.Registry != nil && e.Registry.Text != "" {
			if ev, clears, flags, rule, ok := c.wording.Read(e.Registry.Text); ok {
				r.fromRule(ev, clears, flags, rule, model.MappingRegistry, c.wording.Version)
				r.Severity = e.Registry.Severity
				return r
			}
		}
		if ev, clears, flags, rule, ok := c.wording.Read(e.Message); ok {
			r.fromRule(ev, clears, flags, rule, model.MappingWording, c.wording.Version)
			return r
		}
	}
	r.Event = fwevent.Unclassified
	return r
}

func (r *Result) fromLine(l *Line, e Entry) {
	r.Event, r.Flags, r.Clears, r.Line, r.Table = l.Event, l.Flags, l.Clears, l, l.table
	r.Ref, r.Basis, r.Vendor, r.Severity = l.Ref(), l.BasisOf(), l.table.Vendor, l.Severity
	r.Roles = l.Args
	r.Memory = true
}

func (r *Result) fromRule(ev, clears fwevent.Name, flags []fwevent.Flag, rule *Rule, mapping string, version int) {
	r.Event, r.Clears, r.Flags, r.Rule, r.Mapping = ev, clears, flags, rule, mapping
	if rule != nil {
		r.Ref = rule.Ref(version)
	}
}

// pick chooses the line of a key that holds for the entry: a line whose
// pattern fits the first argument, then the line of the number of
// arguments, then a line for any entry. When every line names another
// number of arguments, the first one holds without its roles.
func pick(lines []*Line, e Entry, nargs int) *Line {
	var fallback *Line
	for _, l := range lines {
		if l.Origin == "memory" && !e.MemoryOrigin {
			continue
		}
		if l.ArgsLike != nil {
			if len(e.Args) == 0 || !l.ArgsLike.MatchString(e.Args[0]) {
				continue
			}
			return l
		}
		if l.NArgs != 0 && l.NArgs != nargs {
			if fallback == nil {
				fallback = l
			}
			continue
		}
		return l
	}
	return fallback
}

// memoryKey reports whether a key is one the table says is about memory.
func (t *Table) memoryKey(key string) bool {
	for _, p := range t.MemoryKeys {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// skips reports whether a key is one the table says is not about memory.
func (t *Table) skips(key string) bool {
	for _, p := range t.Skip {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// ordered returns the tables in the order they are asked: user, shipped.
func (c *Classifier) ordered() []*Table {
	return append(slices.Clone(c.user), c.shipped...)
}

// forRegistry returns the tables that hold for a registry prefix, in the
// order they are asked: user tables that name it, shipped tables that name
// it, user and shipped tables that hold for any registry.
func (c *Classifier) forRegistry(prefix string) []*Table {
	var named, any []*Table
	for _, t := range c.ordered() {
		switch {
		case slices.Contains(t.Applies.Prefixes, prefix):
			named = append(named, t)
		case t.Applies.AnyRegistry:
			any = append(any, t)
		}
	}
	return append(named, any...)
}
