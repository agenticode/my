package redfish

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/memras"
	"github.com/agenticode/my/internal/model"
)

// The Bios resource of a system lists every attribute of the system
// firmware, a few hundred to a few thousand of them: boot order, network
// boot, asset tags, contact names, and among them the settings that bear
// on memory errors. Only those are kept. A ledger travels in a case
// bundle, and a bundle holds no network configuration and no accounts.
//
// An attribute is kept when its name, its display name or its menu path
// has a word of memory in it, and it is never kept when it looks like a
// secret. The attribute registry, when the dump holds it, adds what the
// firmware says about the attribute: its display name, the values it may
// take, whether it can be changed and whether a change needs a reset.

// memoryWords mark an attribute as one about memory, in lower case.
var memoryWords = []string{"mem", "dimm", "ddr", "ecc", "scrub", "ppr", "adddc", "sddc", "spar", "mirror", "refresh",
	"rank", "correctable", "uncorr", "leaky", "bucket", "rowhammer"}

// secretWords mark an attribute that is never kept, in lower case.
var secretWords = []string{"password", "passphrase", "passwd", "secret", "token", "credential", "key", "pin"}

// Limits of what is kept of one Bios resource.
const (
	maxSettings     = 400
	maxSettingValue = 200
	maxAllowed      = 64
)

// ExtraAttributes names attributes that are kept whatever their name is:
// the attributes the built-in settings profile maps for some vendor. It is
// consulted for every attribute of a Bios resource.
var ExtraAttributes = memras.Mapped

// registryEntry is what an attribute registry says about one attribute.
type registryEntry struct {
	name, display, menu, typ string
	readOnly, reset          model.Opt[bool]
	allowed                  []string
	lower, upper             model.Opt[int64]
	def                      string
	hasDef                   bool
}

// hasWord reports whether s holds one of the words.
func hasWord(s string, words []string) bool {
	s = strings.ToLower(s)
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// about says how an attribute is about memory: 2 when its name says so or
// a profile maps it, 1 when only its display name or its menu does, 0 when
// it is not, or when it looks like a secret.
func about(name string, e *registryEntry) int {
	if hasWord(name, secretWords) || (e != nil && strings.EqualFold(e.typ, "Password")) {
		return 0
	}
	if hasWord(name, memoryWords) || (ExtraAttributes != nil && ExtraAttributes(name)) {
		return 2
	}
	if e != nil && (hasWord(e.display, memoryWords) || hasWord(e.menu, memoryWords)) {
		return 1
	}
	return 0
}

// scalar writes the value of an attribute as text. ok is false for a value
// that is no single value; has is false for null.
func scalar(v any) (text, typ string, has, ok bool) {
	switch x := v.(type) {
	case nil:
		return "", "", false, true
	case string:
		return x, "string", true, true
	case bool:
		return strconv.FormatBool(x), "boolean", true, true
	case json.Number:
		return x.String(), "integer", true, true
	}
	return "", "", false, false
}

func optBool(o object, key string) model.Opt[bool] {
	if b, ok := o[key].(bool); ok {
		return model.Some(b)
	}
	return model.Opt[bool]{}
}

func optInt64(o object, key string) model.Opt[int64] {
	if n, ok := integer(o, key); ok {
		return model.Some(n)
	}
	return model.Opt[int64]{}
}

// registryEntries reads the attributes of a registry by name.
func registryEntries(reg object) map[string]*registryEntry {
	list, _ := child(reg, "RegistryEntries")["Attributes"].([]any)
	out := make(map[string]*registryEntry, len(list))
	for _, item := range list {
		a, ok := item.(object)
		if !ok {
			continue
		}
		name := str(a, "AttributeName")
		if name == "" {
			continue
		}
		e := &registryEntry{name: name, display: str(a, "DisplayName"), menu: str(a, "MenuPath"), typ: str(a, "Type"),
			readOnly: optBool(a, "ReadOnly"), reset: optBool(a, "ResetRequired"),
			lower: optInt64(a, "LowerBound"), upper: optInt64(a, "UpperBound")}
		if values, ok := a["Value"].([]any); ok {
			for _, v := range values {
				vo, ok := v.(object)
				if !ok || len(e.allowed) == maxAllowed {
					continue
				}
				if n, _ := vo["ValueName"].(string); n != "" && len(n) <= maxSettingValue {
					e.allowed = append(e.allowed, n)
				}
			}
		}
		if text, _, has, ok := scalar(a["DefaultValue"]); ok && has && len(text) <= maxSettingValue {
			e.def, e.hasDef = text, true
		}
		out[name] = e
	}
	return out
}

// biosResources picks the Bios resource with the settings in effect and
// the one with the pending settings. The first names the second in its
// "@Redfish.Settings"; without that, the resource whose URI ends in
// Settings, Pending or SD below another Bios resource is the pending one.
func (r *run) biosResources() (current, pending *resource) {
	if len(r.bios) == 0 {
		return nil, nil
	}
	byID := map[string]*resource{}
	for i := range r.bios {
		byID[strings.TrimRight(str(r.bios[i].obj, "@odata.id"), "/")] = &r.bios[i]
	}
	isPending := map[*resource]bool{}
	for i := range r.bios {
		link := strings.TrimRight(str(child(child(r.bios[i].obj, "@Redfish.Settings"), "SettingsObject"), "@odata.id"), "/")
		if p, ok := byID[link]; ok && link != "" && p != &r.bios[i] {
			if current == nil {
				current, pending = &r.bios[i], p
			}
			isPending[p] = true
		}
	}
	if current != nil {
		return current, pending
	}
	// No link: go by the names.
	for i := range r.bios {
		id := strings.TrimRight(str(r.bios[i].obj, "@odata.id"), "/")
		switch strings.ToLower(lastSegment(id)) {
		case "settings", "pending", "sd":
			if parent, ok := byID[id[:max(0, strings.LastIndex(id, "/"))]]; ok {
				if current == nil {
					current, pending = parent, &r.bios[i]
				}
				isPending[&r.bios[i]] = true
			}
		}
	}
	if current != nil {
		return current, pending
	}
	for i := range r.bios {
		if !isPending[&r.bios[i]] {
			return &r.bios[i], nil
		}
	}
	return &r.bios[0], nil
}

// registryFor returns the attribute registry the Bios resource names, or
// the only one of the dump.
func (r *run) registryFor(bios *resource) (*resource, string) {
	want := strings.ToLower(str(bios.obj, "AttributeRegistry"))
	for i := range r.registries {
		id := strings.ToLower(str(r.registries[i].obj, "Id"))
		if id != "" && want != "" && (id == want || strings.HasPrefix(want, id+".") || strings.HasPrefix(id, want+".")) {
			return &r.registries[i], str(r.registries[i].obj, "Id")
		}
	}
	if len(r.registries) == 1 {
		name := str(r.registries[0].obj, "Id")
		if name == "" {
			name = str(bios.obj, "AttributeRegistry")
		}
		return &r.registries[0], name
	}
	return nil, ""
}

// settings turns the Bios resources of the dump into the settings that
// bear on memory.
func (r *run) settings(sink adapter.Sink) error {
	current, pending := r.biosResources()
	if current == nil {
		return nil
	}
	if n := len(r.bios); n > 2 || (n == 2 && pending == nil) {
		r.rep.Notes = append(r.rep.Notes, fmt.Sprintf("%d Bios resources are in the dump; the settings of %s are read", n, current.id))
	}
	attrs := child(current.obj, "Attributes")
	if attrs == nil {
		r.tally.Skip(current.ref, "Bios resource without Attributes")
		return nil
	}
	var entries map[string]*registryEntry
	reg, regName := r.registryFor(current)
	if reg != nil {
		entries = registryEntries(reg.obj)
	}
	var waiting object
	if pending != nil {
		waiting = child(pending.obj, "Attributes")
	}

	names := make([]string, 0, len(attrs))
	for name := range attrs {
		names = append(names, name)
	}
	sort.Strings(names)
	type kept struct {
		name string
		rank int
	}
	var keep []kept
	odd := 0
	for _, name := range names {
		if rank := about(name, entries[name]); rank > 0 {
			keep = append(keep, kept{name, rank})
		}
	}
	// When there are more than the bound, the ones whose own name is about
	// memory come first.
	left := 0
	if len(keep) > maxSettings {
		sort.SliceStable(keep, func(i, j int) bool { return keep[i].rank > keep[j].rank })
		left = len(keep) - maxSettings
		keep = keep[:maxSettings]
	}
	changed := 0
	for _, k := range keep {
		text, typ, has, ok := scalar(attrs[k.name])
		if !ok || len(text) > maxSettingValue {
			odd++
			continue
		}
		set := &model.BIOSSetting{ObservedAt: r.now, Name: k.name, Value: text, HasValue: has, Type: typ, Source: Kind + "/bios", RawRef: current.ref}
		if e := entries[k.name]; e != nil {
			set.Registry, set.DisplayName, set.MenuPath = regName, e.display, e.menu
			set.ReadOnly, set.ResetRequired, set.Allowed = e.readOnly, e.reset, e.allowed
			set.LowerBound, set.UpperBound, set.Default, set.HasDefault = e.lower, e.upper, e.def, e.hasDef
			if e.typ != "" {
				set.Type = strings.ToLower(e.typ)
			}
		}
		if waiting != nil {
			if v, listed := waiting[k.name]; listed {
				if ptext, _, phas, pok := scalar(v); pok && phas && len(ptext) <= maxSettingValue && (ptext != text || !has) {
					set.Pending, set.HasPending = ptext, true
					changed++
				}
			}
		}
		if err := sink.BIOSSetting(set); err != nil {
			return err
		}
	}

	add := func(format string, a ...any) { r.rep.Notes = append(r.rep.Notes, fmt.Sprintf(format, a...)) }
	add("%d BIOS attributes read, %d kept as settings that bear on memory", len(attrs), len(keep)-odd)
	switch {
	case reg != nil:
		add("attribute registry %s read: %d attributes described", regName, len(entries))
	case str(current.obj, "AttributeRegistry") != "":
		add("the attribute registry %s is not in the dump: display names, allowed values and whether a setting can be changed are not known", str(current.obj, "AttributeRegistry"))
	}
	switch {
	case pending == nil:
		add("the dump holds no pending BIOS settings: a change that waits for a reboot would not show")
	case changed > 0:
		add("%d memory settings have a pending value that differs: it takes effect after a reboot", changed)
	}
	if left > 0 {
		add("%d more attributes look like memory settings and were left out: the limit is %d", left, maxSettings)
	}
	if odd > 0 {
		add("%d memory attributes hold no single value, or a text longer than %d characters, and were left out", odd, maxSettingValue)
	}
	return nil
}
