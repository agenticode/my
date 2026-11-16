// Package memras checks the BIOS settings of a server that bear on memory
// errors: patrol scrub, ADDDC and the memory mode, the logging of corrected
// errors, post package repair, the memory frequency and the refresh rate.
//
// Every server vendor names these settings differently in the attribute
// registry of its BIOS. A profile says which attribute of which vendor is
// which setting, and what each value of the attribute means. Every mapping
// of the built-in profile names the public document it was read from. A
// setting no public document gives an attribute for is left unmapped and
// reported as such: the check never guesses an attribute name.
//
// The check reports. It changes nothing on a BMC or in a BIOS.
package memras

import (
	_ "embed"
	"errors"
	"fmt"
	"github.com/agenticode/my/internal/yamlite"
	"os"
	"sort"
	"strings"
	"sync"
)

// The settings a profile can map, in the order they are reported.
const (
	PatrolScrub     = "patrol-scrub"
	ADDDC           = "adddc"
	MemoryMode      = "memory-mode"
	CELogging       = "ce-logging"
	CEThreshold     = "ce-threshold"
	LeakyBucket     = "leaky-bucket"
	PPR             = "ppr"
	MemoryFrequency = "memory-frequency"
	RefreshRate     = "refresh-rate"
)

// Settings lists the settings in the order they are reported.
var Settings = []string{PatrolScrub, ADDDC, MemoryMode, CELogging, CEThreshold, LeakyBucket, PPR, MemoryFrequency, RefreshRate}

// states are the meanings a value of each setting can have.
var states = map[string][]string{
	PatrolScrub:     {"on", "off"},
	ADDDC:           {"on", "off"},
	MemoryMode:      {"independent", "advanced-ecc", "adddc", "rank-sparing", "mirroring", "fault-resilient"},
	CELogging:       {"on", "off"},
	CEThreshold:     {"on", "off"},
	LeakyBucket:     {"on", "off"},
	PPR:             {"on", "off"},
	MemoryFrequency: {"auto", "limited"},
	RefreshRate:     {"1x", "2x"},
}

// States returns the meanings a value of a setting can have.
func States(setting string) []string { return append([]string{}, states[setting]...) }

func knownSetting(s string) bool { _, ok := states[s]; return ok }

func knownState(setting, state string) bool {
	for _, s := range states[setting] {
		if s == state {
			return true
		}
	}
	return false
}

// Reasons a setting has no mapping for a vendor. A profile may also give a
// reason in words of its own.
const (
	// ReasonNotDocumented: the public documents that were read give no
	// attribute for the setting.
	ReasonNotDocumented = "not-documented"
	// ReasonNoVendor: the profile has no mapping for the vendor of the BIOS.
	ReasonNoVendor = "no-vendor"
)

// FormatVersion is the version of the profile format this package reads.
const FormatVersion = 1

// Profile maps the BIOS attributes of server vendors to settings and says
// what each setting is expected to be.
type Profile struct {
	Name string
	// Expect is the state each setting is expected to have. A setting
	// that is not listed has no expectation and is only reported.
	Expect map[string]string
	// Why says in words why a setting is expected to be what Expect says.
	// The built-in profile leaves it empty: its reasons are in the
	// catalog, in both languages.
	Why     map[string]string
	Vendors []*Vendor
}

// Vendor is the mapping for the BIOS of one server vendor.
type Vendor struct {
	ID   string
	Name string
	// Manufacturer lists words, in lower case, one of which the system
	// manufacturer of a host of this vendor holds.
	Manufacturer []string
	Sources      map[string]Source
	Maps         []*Mapping
	// Unmapped says why a setting has no mapping: a reason code, or words.
	Unmapped map[string]string
}

// Source is a public document a mapping was read from.
type Source struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// Mapping says which attribute is a setting and what its values mean.
type Mapping struct {
	Setting   string
	Attribute string
	// Source is the key of the document in the sources of the vendor.
	Source string
	Values []Value
}

// Value is one value of an attribute.
type Value struct {
	// Match is the value as the BIOS writes it. A "*" stands for any text:
	// "*MHz" is every value that ends in MHz.
	Match string
	// Means is the state of the setting the value stands for.
	Means string
	// Note says more about the value, in English and in Korean.
	Note   string
	NoteKO string
}

// matches reports whether a value of the BIOS is this value.
func (v *Value) matches(value string) bool {
	if !strings.Contains(v.Match, "*") {
		return v.Match == value
	}
	parts := strings.Split(v.Match, "*")
	if !strings.HasPrefix(value, parts[0]) {
		return false
	}
	rest := value[len(parts[0]):]
	for i, p := range parts[1:] {
		last := i == len(parts)-2
		switch {
		case last && p == "":
			return true
		case last:
			return strings.HasSuffix(rest, p) && len(rest) >= len(p)
		}
		j := strings.Index(rest, p)
		if j < 0 {
			return false
		}
		rest = rest[j+len(p):]
	}
	return rest == ""
}

// lookup finds the value of a mapping a value of the BIOS is. An exact
// value wins over one with a "*".
func (m *Mapping) lookup(value string) *Value {
	var wild *Value
	for i := range m.Values {
		v := &m.Values[i]
		if !v.matches(value) {
			continue
		}
		if !strings.Contains(v.Match, "*") {
			return v
		}
		if wild == nil {
			wild = v
		}
	}
	return wild
}

// Mapped reports whether the profile maps an attribute name of some
// vendor.
func (p *Profile) Mapped(attribute string) bool {
	for _, v := range p.Vendors {
		for _, m := range v.Maps {
			if m.Attribute == attribute {
				return true
			}
		}
	}
	return false
}

// vendorFor picks the vendor a host's BIOS belongs to: by the system
// manufacturer, or, when that is unknown or fits none, the one vendor
// whose attributes the BIOS has. by says which of the two it was.
func (p *Profile) vendorFor(manufacturer string, has func(attribute string) bool) (v *Vendor, by string) {
	low := strings.ToLower(manufacturer)
	if strings.TrimSpace(low) != "" {
		for _, v := range p.Vendors {
			for _, w := range v.Manufacturer {
				if w != "" && strings.Contains(low, w) {
					return v, "manufacturer"
				}
			}
		}
	}
	var best *Vendor
	bestN, tie := 0, false
	for _, v := range p.Vendors {
		n := 0
		seen := map[string]bool{}
		for _, m := range v.Maps {
			if !seen[m.Attribute] && has(m.Attribute) {
				seen[m.Attribute] = true
				n++
			}
		}
		switch {
		case n > bestN:
			best, bestN, tie = v, n, false
		case n == bestN && n > 0:
			tie = true
		}
	}
	if best != nil && !tie {
		return best, "attributes"
	}
	return nil, ""
}

//go:embed builtin.yaml
var builtinYAML []byte

var (
	builtinOnce sync.Once
	builtin     *Profile
	builtinErr  error
)

// Builtin returns the profile that is part of the binary.
func Builtin() *Profile {
	builtinOnce.Do(func() { builtin, builtinErr = Parse(builtinYAML) })
	if builtinErr != nil {
		// The file is part of the binary and a test reads it.
		panic("memras: the built-in profile does not parse: " + builtinErr.Error())
	}
	return builtin
}

// BuiltinText returns the built-in profile as it is written, to start a
// profile of one's own from.
func BuiltinText() []byte { return append([]byte{}, builtinYAML...) }

// Mapped reports whether the built-in profile maps an attribute name.
func Mapped(attribute string) bool { return Builtin().Mapped(attribute) }

// ReadFile reads a profile from a file.
func ReadFile(path string) (*Profile, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if fi.IsDir() {
		return nil, fmt.Errorf("%s is a directory", path)
	}
	if fi.Size() > yamlite.MaxBytes {
		return nil, fmt.Errorf("%s: larger than %d bytes", path, yamlite.MaxBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	p, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}

// Decoding of the tree of a profile file. Every key is checked: a key
// that is not known is a mistake in the file, not something to pass over.

// Parse reads a profile.
func Parse(data []byte) (*Profile, error) {
	root, err := yamlite.Parse(data)
	if err != nil {
		return nil, err
	}
	if err := root.Mapping("profile", "format", "name", "expect", "why", "vendors"); err != nil {
		return nil, err
	}
	p := &Profile{Expect: map[string]string{}, Why: map[string]string{}}
	f, ok := root.Map["format"]
	if !ok {
		return nil, errors.New("no \"format: 1\" line: not a memory RAS settings profile")
	}
	if v, err := f.Text("format"); err != nil {
		return nil, err
	} else if v != "1" {
		return nil, yamlite.ErrAt(f.Line, "format %s: this version reads format %d", v, FormatVersion)
	}
	if n, ok := root.Map["name"]; ok {
		if p.Name, err = n.Text("name"); err != nil {
			return nil, err
		}
	}
	if n, ok := root.Map["expect"]; ok {
		if err := n.Mapping("expect", Settings...); err != nil {
			return nil, err
		}
		for _, k := range n.Keys {
			v, err := n.Map[k].Text("expect " + k)
			if err != nil {
				return nil, err
			}
			if !knownState(k, v) {
				return nil, yamlite.ErrAt(n.Map[k].Line, "expect %s: %q is not one of %s", k, v, strings.Join(states[k], ", "))
			}
			p.Expect[k] = v
		}
	}
	if n, ok := root.Map["why"]; ok {
		if err := n.Mapping("why", Settings...); err != nil {
			return nil, err
		}
		for _, k := range n.Keys {
			if p.Why[k], err = n.Map[k].Text("why " + k); err != nil {
				return nil, err
			}
		}
	}
	vendors := []*yamlite.Node{}
	if n, ok := root.Map["vendors"]; ok {
		if vendors, err = n.Items("vendors"); err != nil {
			return nil, err
		}
	}
	ids := map[string]bool{}
	for _, vn := range vendors {
		v, err := parseVendor(vn)
		if err != nil {
			return nil, err
		}
		if ids[v.ID] {
			return nil, yamlite.ErrAt(vn.Line, "the vendor %q is given twice", v.ID)
		}
		ids[v.ID] = true
		p.Vendors = append(p.Vendors, v)
	}
	return p, nil
}

func parseVendor(n *yamlite.Node) (*Vendor, error) {
	if err := n.Mapping("vendor", "id", "name", "manufacturer", "sources", "settings", "unmapped"); err != nil {
		return nil, err
	}
	v := &Vendor{Sources: map[string]Source{}, Unmapped: map[string]string{}}
	var err error
	for _, key := range []string{"id", "name"} {
		kn, ok := n.Map[key]
		if !ok {
			return nil, yamlite.ErrAt(n.Line, "vendor: no %q", key)
		}
		text, err := kn.Text("vendor " + key)
		if err != nil {
			return nil, err
		}
		if text == "" {
			return nil, yamlite.ErrAt(kn.Line, "vendor: %q is empty", key)
		}
		if key == "id" {
			v.ID = text
		} else {
			v.Name = text
		}
	}
	if mn, ok := n.Map["manufacturer"]; ok {
		list, err := mn.Items("manufacturer")
		if err != nil {
			return nil, err
		}
		for _, it := range list {
			w, err := it.Text("manufacturer")
			if err != nil {
				return nil, err
			}
			if w = strings.ToLower(strings.TrimSpace(w)); w != "" {
				v.Manufacturer = append(v.Manufacturer, w)
			}
		}
	}
	if sn, ok := n.Map["sources"]; ok {
		if err := sn.Mapping("sources"); err != nil {
			return nil, err
		}
		for _, key := range sn.Keys {
			one := sn.Map[key]
			if err := one.Mapping("source "+key, "title", "url"); err != nil {
				return nil, err
			}
			var src Source
			if t, ok := one.Map["title"]; ok {
				if src.Title, err = t.Text("title"); err != nil {
					return nil, err
				}
			}
			u, ok := one.Map["url"]
			if !ok {
				return nil, yamlite.ErrAt(one.Line, "source %s: no \"url\": every mapping names the public document it comes from", key)
			}
			if src.URL, err = u.Text("url"); err != nil {
				return nil, err
			}
			if !strings.HasPrefix(src.URL, "https://") && !strings.HasPrefix(src.URL, "http://") {
				return nil, yamlite.ErrAt(u.Line, "source %s: %q is no URL of a public document", key, clip(src.URL, 60))
			}
			v.Sources[key] = src
		}
	}
	if sn, ok := n.Map["settings"]; ok {
		list, err := sn.Items("settings")
		if err != nil {
			return nil, err
		}
		for _, it := range list {
			m, err := parseMapping(it, v)
			if err != nil {
				return nil, err
			}
			for _, other := range v.Maps {
				if other.Setting == m.Setting {
					return nil, yamlite.ErrAt(it.Line, "vendor %s: the setting %s is mapped twice", v.ID, m.Setting)
				}
			}
			v.Maps = append(v.Maps, m)
		}
	}
	if un, ok := n.Map["unmapped"]; ok {
		if err := un.Mapping("unmapped", Settings...); err != nil {
			return nil, err
		}
		for _, key := range un.Keys {
			for _, m := range v.Maps {
				if m.Setting == key {
					return nil, yamlite.ErrAt(un.Map[key].Line, "vendor %s: the setting %s is mapped and listed as unmapped", v.ID, key)
				}
			}
			if v.Unmapped[key], err = un.Map[key].Text("unmapped " + key); err != nil {
				return nil, err
			}
		}
	}
	return v, nil
}

func parseMapping(n *yamlite.Node, v *Vendor) (*Mapping, error) {
	if err := n.Mapping("setting", "setting", "attribute", "source", "values"); err != nil {
		return nil, err
	}
	m := &Mapping{}
	get := func(key string) (string, error) {
		kn, ok := n.Map[key]
		if !ok {
			return "", yamlite.ErrAt(n.Line, "vendor %s: a mapping without %q", v.ID, key)
		}
		text, err := kn.Text(key)
		if err == nil && text == "" {
			err = yamlite.ErrAt(kn.Line, "vendor %s: %q is empty", v.ID, key)
		}
		return text, err
	}
	var err error
	if m.Setting, err = get("setting"); err != nil {
		return nil, err
	}
	if !knownSetting(m.Setting) {
		return nil, yamlite.ErrAt(n.Map["setting"].Line, "unknown setting %q (known: %s)", m.Setting, strings.Join(Settings, ", "))
	}
	if m.Attribute, err = get("attribute"); err != nil {
		return nil, err
	}
	if m.Source, err = get("source"); err != nil {
		return nil, err
	}
	if _, ok := v.Sources[m.Source]; !ok {
		return nil, yamlite.ErrAt(n.Map["source"].Line, "vendor %s: the mapping of %s names the source %q, which the vendor does not list", v.ID, m.Attribute, m.Source)
	}
	vn, ok := n.Map["values"]
	if !ok {
		return nil, yamlite.ErrAt(n.Line, "vendor %s: the mapping of %s has no values", v.ID, m.Attribute)
	}
	list, err := vn.Items("values")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, it := range list {
		if err := it.Mapping("value", "value", "means", "note", "note_ko"); err != nil {
			return nil, err
		}
		var val Value
		for key, into := range map[string]*string{"value": &val.Match, "means": &val.Means, "note": &val.Note, "note_ko": &val.NoteKO} {
			if kn, ok := it.Map[key]; ok {
				if *into, err = kn.Text(key); err != nil {
					return nil, err
				}
			}
		}
		if _, ok := it.Map["value"]; !ok {
			return nil, yamlite.ErrAt(it.Line, "vendor %s, %s: a value without \"value\"", v.ID, m.Attribute)
		}
		if !knownState(m.Setting, val.Means) {
			return nil, yamlite.ErrAt(it.Line, "vendor %s, %s: value %q means %q, which is not one of %s", v.ID, m.Attribute, val.Match, val.Means,
				strings.Join(states[m.Setting], ", "))
		}
		if seen[val.Match] {
			return nil, yamlite.ErrAt(it.Line, "vendor %s, %s: the value %q is given twice", v.ID, m.Attribute, val.Match)
		}
		seen[val.Match] = true
		m.Values = append(m.Values, val)
	}
	if len(m.Values) == 0 {
		return nil, yamlite.ErrAt(vn.Line, "vendor %s: the mapping of %s has no values", v.ID, m.Attribute)
	}
	return m, nil
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// SourceList returns the documents the profile cites, each once, in the
// order of vendors and keys.
func (p *Profile) SourceList() []Source {
	var out []Source
	seen := map[string]bool{}
	for _, v := range p.Vendors {
		keys := make([]string, 0, len(v.Sources))
		for k := range v.Sources {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if s := v.Sources[k]; !seen[s.URL] {
				seen[s.URL] = true
				out = append(out, s)
			}
		}
	}
	return out
}
