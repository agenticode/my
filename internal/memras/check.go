package memras

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agenticode/my/internal/model"
)

// What became of one setting in a check.
const (
	// StatusOK: the setting is what the profile expects.
	StatusOK = "ok"
	// StatusMismatch: the setting is something else than expected, and
	// this BIOS could be set as expected.
	StatusMismatch = "mismatch"
	// StatusNoFit: the registry of this BIOS allows no value that means
	// what the profile expects. It is no mismatch: the expectation does
	// not fit this BIOS version.
	StatusNoFit = "no-fit"
	// StatusReported: the setting is known and the profile expects
	// nothing of it.
	StatusReported = "reported"
	// StatusUnknownValue: the attribute has a value the profile does not
	// know.
	StatusUnknownValue = "unknown-value"
	// StatusAbsent: the profile maps an attribute for the setting and the
	// BIOS settings on record do not list it.
	StatusAbsent = "absent"
	// StatusUnmapped: the profile has no attribute for the setting on
	// this BIOS.
	StatusUnmapped = "unmapped"
)

// Module is what the check needs to know of one DIMM.
type Module struct {
	Slot string
	// Width is the width of the DRAM devices in bits, 0 when unknown.
	Width int
	// Ranks is the number of ranks, 0 when unknown.
	Ranks int
	// Channel names the memory channel of the slot ("0/1/2" for socket,
	// controller and channel), "" when the place is unknown.
	Channel string
	// Operating and Rated are the speed in use and the speed the module
	// is rated for, 0 when unknown.
	Operating, Rated int
}

// Input is what a check works on.
type Input struct {
	// Manufacturer is the system manufacturer of the host.
	Manufacturer string
	// Settings are the BIOS settings of one reading.
	Settings []model.BIOSSetting
	// Modules are the DIMMs of the host.
	Modules []Module
}

// Item is the outcome for one setting.
type Item struct {
	Setting string `json:"setting"`
	Status  string `json:"status"`
	// Attribute is the BIOS attribute the profile maps, Value its value
	// in effect, Pending the value that waits for a reboot.
	Attribute  string `json:"attribute,omitzero"`
	Value      string `json:"value,omitzero"`
	Pending    string `json:"pending,omitzero"`
	HasPending bool   `json:"has_pending,omitzero"`
	// State is what the value means, PendingState what the pending value
	// means; "" for a value the profile does not know.
	State        string `json:"state,omitzero"`
	PendingState string `json:"pending_state,omitzero"`
	// Note says more about the value, from the profile.
	Note   string `json:"note,omitzero"`
	NoteKO string `json:"-"`
	// Expected is the state the profile expects, "" when it expects none.
	Expected string `json:"expected,omitzero"`
	// Why is the reason the profile gives for its expectation, in words.
	Why string `json:"why,omitzero"`
	// What the registry of the BIOS says about the attribute.
	DisplayName   string          `json:"display_name,omitzero"`
	Allowed       []string        `json:"allowed,omitzero"`
	ReadOnly      model.Opt[bool] `json:"read_only,omitzero"`
	ResetRequired model.Opt[bool] `json:"reset_required,omitzero"`
	// Registry is set when the registry of the BIOS was read.
	Registry bool `json:"registry"`
	// Reason says why a setting is unmapped: a reason code of this
	// package, or the words of a profile.
	Reason string `json:"reason,omitzero"`
	// Source is the public document the mapping was read from.
	Source *Source `json:"source,omitzero"`
	// Notes are remarks that follow from the DIMMs of the host, as
	// statements of the catalog of this package.
	Notes []model.Statement `json:"notes,omitzero"`
}

// Related is a BIOS setting on record that the profile maps to nothing.
type Related struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitzero"`
	Value       string `json:"value"`
	HasValue    bool   `json:"has_value"`
	Pending     string `json:"pending,omitzero"`
	HasPending  bool   `json:"has_pending,omitzero"`
}

// Slow is a DIMM that runs below the speed it is rated for.
type Slow struct {
	Slot      string `json:"slot"`
	Operating int    `json:"operating"`
	Rated     int    `json:"rated"`
}

// Speed says which DIMMs run below their rated speed and what the data
// shows that could be the reason. The reasons are candidates.
type Speed struct {
	Slow []Slow `json:"slow"`
	// Known is the number of DIMMs whose two speeds are known.
	Known int `json:"known"`
	// Candidates are statements of the catalog of this package.
	Candidates []model.Statement `json:"candidates,omitzero"`
}

// Result is a check of the BIOS settings of one host.
type Result struct {
	Profile string `json:"profile"`
	// Vendor is the vendor of the profile the BIOS was taken for, and By
	// how: "manufacturer" or "attributes". Both are empty when the
	// profile has no mapping for this BIOS.
	Vendor string `json:"vendor,omitzero"`
	By     string `json:"vendor_by,omitzero"`
	// Manufacturer is the system manufacturer of the host.
	Manufacturer string `json:"manufacturer,omitzero"`
	// ObservedAt is the time of the reading, Registry the attribute
	// registry it refers to.
	ObservedAt time.Time `json:"observed_at,omitzero"`
	Registry   string    `json:"registry,omitzero"`
	// Settings is the number of BIOS settings on record.
	Settings int    `json:"settings"`
	Items    []Item `json:"items"`
	// Related are the settings on record the profile maps to nothing.
	Related []Related `json:"related,omitzero"`
	Speed   *Speed    `json:"speed,omitzero"`
	// Mismatches counts the settings that are not as expected.
	Mismatches int `json:"mismatches"`
	// Sources are the documents the mappings in use were read from.
	Sources []Source `json:"sources,omitzero"`
}

// Item returns the outcome for a setting, or nil.
func (r *Result) Item(setting string) *Item {
	for i := range r.Items {
		if r.Items[i].Setting == setting {
			return &r.Items[i]
		}
	}
	return nil
}

// The documents the rules about the device width rest on.
var (
	// SourceWidth is where the limits of ADDDC and of device correction
	// by device width are stated.
	SourceWidth = Source{
		Title: "Memory Errors and Dell EMC PowerEdge YX4X Server Memory RAS Features, Revision 1.0, 2020-01-03",
		URL:   "https://dl.dell.com/manuals/common/dellemc_poweredge_yx4x_memoryras_v1_0.pdf",
	}
	// SourceAMDC is AMD's statement on device correction with x4 DIMMs.
	SourceAMDC = Source{
		Title: "AMD, Advanced Memory Device Correction (AMDC) for Servers, 2020",
		URL:   "https://www.amd.com/content/dam/amd/en/documents/epyc-business-docs/white-papers/advanced-memory-device-correction.pdf",
	}
)

func statement(code string, kv ...any) model.Statement {
	args := make(map[string]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		args[kv[i].(string)] = kv[i+1]
	}
	return model.Statement{Code: code, Args: args}
}

// allowsState reports whether the registry allows a value that means the
// state. known is false when the registry lists no values to go by.
func allowsState(m *Mapping, allowed []string, state string) (allows, known bool) {
	if len(allowed) == 0 {
		return false, false
	}
	for _, a := range allowed {
		if v := m.lookup(a); v != nil && v.Means == state {
			return true, true
		}
	}
	return false, true
}

// Check checks the BIOS settings of a host against the profile.
func (p *Profile) Check(in Input) *Result {
	res := &Result{Profile: p.Name, Manufacturer: in.Manufacturer, Settings: len(in.Settings), Items: []Item{}}
	byName := make(map[string]*model.BIOSSetting, len(in.Settings))
	for i := range in.Settings {
		s := &in.Settings[i]
		byName[s.Name] = s
		if s.ObservedAt.After(res.ObservedAt) {
			res.ObservedAt = s.ObservedAt
		}
		if s.Registry != "" {
			res.Registry = s.Registry
		}
	}
	vendor, by := p.vendorFor(in.Manufacturer, func(a string) bool { return byName[a] != nil })
	if vendor != nil {
		res.Vendor, res.By = vendor.Name, by
	}
	used := map[string]bool{}
	cited := map[string]bool{}
	cite := func(s Source) {
		if !cited[s.URL] {
			cited[s.URL] = true
			res.Sources = append(res.Sources, s)
		}
	}

	for _, setting := range Settings {
		it := Item{Setting: setting, Expected: p.Expect[setting], Why: p.Why[setting]}
		var m *Mapping
		if vendor != nil {
			for _, x := range vendor.Maps {
				if x.Setting == setting {
					m = x
				}
			}
		}
		switch {
		case vendor == nil:
			it.Status, it.Reason = StatusUnmapped, ReasonNoVendor
		case m == nil:
			it.Status, it.Reason = StatusUnmapped, vendor.Unmapped[setting]
			if it.Reason == "" {
				it.Reason = ReasonNotDocumented
			}
		default:
			it.Attribute = m.Attribute
			if src, ok := vendor.Sources[m.Source]; ok {
				it.Source = &src
			}
			s := byName[m.Attribute]
			if s == nil || !s.HasValue {
				it.Status = StatusAbsent
				break
			}
			used[m.Attribute] = true
			if it.Source != nil {
				cite(*it.Source)
			}
			it.Value, it.Pending, it.HasPending = s.Value, s.Pending, s.HasPending
			it.DisplayName, it.Allowed, it.ReadOnly, it.ResetRequired = s.DisplayName, s.Allowed, s.ReadOnly, s.ResetRequired
			it.Registry = s.Registry != ""
			v := m.lookup(s.Value)
			if s.HasPending {
				if pv := m.lookup(s.Pending); pv != nil {
					it.PendingState = pv.Means
				}
			}
			if v == nil {
				it.Status = StatusUnknownValue
				break
			}
			it.State, it.Note, it.NoteKO = v.Means, v.Note, v.NoteKO
			switch {
			case it.Expected == "":
				it.Status = StatusReported
			case it.State == it.Expected:
				it.Status = StatusOK
			default:
				it.Status = StatusMismatch
				// A BIOS whose registry allows no value with the expected
				// meaning cannot be set as expected.
				if allows, known := allowsState(m, s.Allowed, it.Expected); known && !allows {
					it.Status = StatusNoFit
				}
			}
		}
		if it.Status == StatusMismatch {
			res.Mismatches++
		}
		it.Notes = widthNotes(&it, in.Modules)
		if len(it.Notes) > 0 {
			cite(SourceWidth)
			if it.Setting == MemoryMode {
				cite(SourceAMDC)
			}
		}
		res.Items = append(res.Items, it)
	}

	// What is on record and maps to nothing: a reader may know what it is.
	for i := range in.Settings {
		s := &in.Settings[i]
		if used[s.Name] {
			continue
		}
		res.Related = append(res.Related, Related{Name: s.Name, DisplayName: s.DisplayName, Value: s.Value, HasValue: s.HasValue,
			Pending: s.Pending, HasPending: s.HasPending})
	}
	sort.Slice(res.Related, func(i, j int) bool { return res.Related[i].Name < res.Related[j].Name })
	res.Speed = speedOf(in.Modules, res.Item(MemoryFrequency))
	return res
}

// slots joins slot names for a statement, the first few of many.
func slots(names []string) string {
	sort.Strings(names)
	if len(names) > 8 {
		return strings.Join(names[:8], ", ") + ", ..."
	}
	return strings.Join(names, ", ")
}

// widthNotes says what the width of the DRAM devices of the host means
// for ADDDC and for device correction.
func widthNotes(it *Item, modules []Module) []model.Statement {
	if len(modules) == 0 {
		return nil
	}
	var x8, unknown []string
	for _, m := range modules {
		switch {
		case m.Width == 8:
			x8 = append(x8, m.Slot)
		case m.Width == 0:
			unknown = append(unknown, m.Slot)
		}
	}
	var out []model.Statement
	switch {
	case it.Setting == ADDDC && it.State == "on":
		if len(x8) > 0 {
			out = append(out, statement("note.adddc_x8", "n", len(x8), "total", len(modules), "slots", slots(x8)))
		}
		if len(unknown) > 0 {
			out = append(out, statement("note.adddc_width_unknown", "n", len(unknown), "total", len(modules)))
		}
		// ADDDC needs two or more ranks on a channel.
		ranks := map[string]int{}
		known := true
		for _, m := range modules {
			if m.Channel == "" || m.Ranks == 0 {
				known = false
				break
			}
			ranks[m.Channel] += m.Ranks
		}
		if known {
			var single []string
			for _, m := range modules {
				if ranks[m.Channel] < 2 {
					single = append(single, m.Slot)
				}
			}
			if len(single) > 0 {
				out = append(out, statement("note.adddc_one_rank", "n", len(single), "slots", slots(single)))
			}
		}
	case it.Setting == MemoryMode && (it.State == "advanced-ecc" || it.State == "adddc" || it.State == "independent"):
		if len(x8) > 0 {
			out = append(out, statement("note.sddc_x8", "n", len(x8), "total", len(modules), "slots", slots(x8)))
		}
	}
	return out
}

// speedOf lists the DIMMs that run below their rated speed, with what the
// data shows that could be the reason.
func speedOf(modules []Module, frequency *Item) *Speed {
	sp := &Speed{Slow: []Slow{}}
	lowestRated, sameOperating, operating := 0, true, 0
	perChannel := map[string]int{}
	for _, m := range modules {
		if m.Channel != "" {
			perChannel[m.Channel]++
		}
		if m.Rated > 0 && (lowestRated == 0 || m.Rated < lowestRated) {
			lowestRated = m.Rated
		}
		if m.Operating == 0 || m.Rated == 0 {
			continue
		}
		sp.Known++
		if operating == 0 {
			operating = m.Operating
		} else if m.Operating != operating {
			sameOperating = false
		}
		if m.Operating < m.Rated {
			sp.Slow = append(sp.Slow, Slow{Slot: m.Slot, Operating: m.Operating, Rated: m.Rated})
		}
	}
	if sp.Known == 0 {
		return nil
	}
	if len(sp.Slow) == 0 {
		return sp
	}
	sort.Slice(sp.Slow, func(i, j int) bool { return sp.Slow[i].Slot < sp.Slow[j].Slot })
	// The BIOS setting, when it limits the frequency.
	if frequency != nil && frequency.State == "limited" {
		sp.Candidates = append(sp.Candidates, statement("speed.bios", "attribute", frequency.Attribute, "value", frequency.Value))
	}
	// Two or more DIMMs on a channel.
	var shared []string
	for _, m := range modules {
		if m.Channel != "" && perChannel[m.Channel] > 1 && m.Operating > 0 && m.Operating < m.Rated {
			shared = append(shared, m.Slot)
		}
	}
	if len(shared) > 0 {
		sp.Candidates = append(sp.Candidates, statement("speed.per_channel", "n", len(shared), "slots", slots(shared)))
	}
	// A module that is rated lower than the others.
	mixed := false
	for _, s := range sp.Slow {
		if lowestRated > 0 && lowestRated < s.Rated && s.Operating <= lowestRated {
			mixed = true
		}
	}
	if mixed {
		sp.Candidates = append(sp.Candidates, statement("speed.mixed", "rated", strconv.Itoa(lowestRated)))
	}
	// What is left when every DIMM runs at one speed: the processor or the
	// board. The ledger holds nothing to check that against.
	if sameOperating {
		sp.Candidates = append(sp.Candidates, statement("speed.platform", "speed", strconv.Itoa(operating)))
	}
	return sp
}

// Latest returns the settings of the newest reading among settings of
// several readings.
func Latest(settings []model.BIOSSetting) []model.BIOSSetting {
	var newest time.Time
	for i := range settings {
		if settings[i].ObservedAt.After(newest) {
			newest = settings[i].ObservedAt
		}
	}
	var out []model.BIOSSetting
	for i := range settings {
		if settings[i].ObservedAt.Equal(newest) {
			out = append(out, settings[i])
		}
	}
	return out
}
