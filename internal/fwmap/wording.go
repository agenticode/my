package fwmap

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"github.com/agenticode/my/internal/fwevent"
	"github.com/agenticode/my/internal/yamlite"
)

// Wording is the table of wording rules: what a text about memory says
// when no table has its message. A rule is a set of word groups that must
// all occur, a set that must not, and the common event. A text that fits
// rules of two families says two things and stays unclassified. Words that
// end a state turn the result into "cleared".
type Wording struct {
	Version int
	Rules   []*Rule
	ends    *regexp.Regexp
}

// Rule is one wording rule.
type Rule struct {
	Event fwevent.Name
	Flags []fwevent.Flag
	All   []*regexp.Regexp
	None  []*regexp.Regexp
	Line  int
}

// Ref names the rule: "wording@1:12" for the rule on line 12.
func (r *Rule) Ref(version int) string { return fmt.Sprintf("wording@%d:%d", version, r.Line) }

// ParseWording reads the table of wording rules.
func ParseWording(data []byte) (*Wording, error) {
	root, err := yamlite.Parse(data)
	if err != nil {
		return nil, err
	}
	if err := root.Mapping("wording", "format", "table", "version", "ends", "rules"); err != nil {
		return nil, err
	}
	if f := root.Map["format"]; f == nil || f.Scalar != "1" {
		return nil, errors.New(`no "format: 1" line: not a table of wording rules`)
	}
	if t := root.Map["table"]; t == nil || t.Scalar != "wording" {
		return nil, errors.New(`the table of wording rules is named "wording"`)
	}
	w := &Wording{}
	if v := root.Map["version"]; v != nil {
		w.Version, _ = strconv.Atoi(v.Scalar)
	}
	if w.Version < 1 {
		return nil, errors.New("wording: version is not a whole number of at least 1")
	}
	compile := func(s string, line int) (*regexp.Regexp, error) {
		if len(s) > MaxPattern {
			return nil, yamlite.ErrAt(line, "pattern longer than %d bytes", MaxPattern)
		}
		re, err := regexp.Compile("(?i)" + s)
		if err != nil {
			return nil, yamlite.ErrAt(line, "pattern %q: %v", s, err)
		}
		return re, nil
	}
	if e := root.Map["ends"]; e != nil {
		if w.ends, err = compile(e.Scalar, e.Line); err != nil {
			return nil, err
		}
	}
	items, err := root.Map["rules"].Items("rules")
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if err := it.Mapping("rule", "event", "flags", "all", "none"); err != nil {
			return nil, err
		}
		r := &Rule{Line: it.Line}
		if e := it.Map["event"]; e != nil {
			r.Event = fwevent.Name(e.Scalar)
		}
		if !r.Event.Valid() {
			return nil, yamlite.ErrAt(it.Line, "rule: %q is not a common event", r.Event)
		}
		if f := it.Map["flags"]; f != nil {
			for _, s := range list(f.Scalar) {
				if !fwevent.Flag(s).Valid() {
					return nil, yamlite.ErrAt(it.Line, "rule: %q is not a flag", s)
				}
				r.Flags = append(r.Flags, fwevent.Flag(s))
			}
		}
		for key, dst := range map[string]*[]*regexp.Regexp{"all": &r.All, "none": &r.None} {
			n := it.Map[key]
			if n == nil {
				continue
			}
			groups, err := texts(n, key)
			if err != nil {
				return nil, err
			}
			for _, g := range groups {
				re, err := compile(g, n.Line)
				if err != nil {
					return nil, err
				}
				*dst = append(*dst, re)
			}
		}
		if len(r.All) == 0 {
			return nil, yamlite.ErrAt(it.Line, "rule %s: no word group that must occur", r.Event)
		}
		w.Rules = append(w.Rules, r)
	}
	return w, nil
}

// fits reports whether text fits the rule.
func (r *Rule) fits(text string) bool {
	for _, re := range r.All {
		if !re.MatchString(text) {
			return false
		}
	}
	for _, re := range r.None {
		if re.MatchString(text) {
			return false
		}
	}
	return true
}

// Read classes a text by the rules. It returns the rule that decided, or
// nil with ok set when rules of two families fit (the text stays
// unclassified), and nil, false when no rule fits.
func (w *Wording) Read(text string) (event fwevent.Name, clears fwevent.Name, flags []fwevent.Flag, rule *Rule, ok bool) {
	if len(text) > 4<<10 {
		text = text[:4<<10]
	}
	var first *Rule
	for _, r := range w.Rules {
		if !r.fits(text) {
			continue
		}
		if first == nil {
			first = r
			continue
		}
		if r.Event.Family() != first.Event.Family() {
			return fwevent.Unclassified, "", nil, nil, true
		}
	}
	if first == nil {
		return "", "", nil, nil, false
	}
	if w.ends != nil && w.ends.MatchString(text) && first.Event != fwevent.Cleared {
		return fwevent.Cleared, first.Event, nil, first, true
	}
	return first.Event, "", first.Flags, first, true
}
