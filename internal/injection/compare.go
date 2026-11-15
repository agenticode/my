package injection

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// Difference is a field in which a record of the ledger disagrees with
// the plan.
type Difference struct {
	Field string
	Want  string
	Got   string
	// Record names the record: its source and raw reference.
	Record string
}

func (d Difference) String() string {
	return fmt.Sprintf("%s: injected %s, recorded %s (%s)", d.Field, d.Want, d.Got, d.Record)
}

// Check is the comparison of one injection with the ledger.
type Check struct {
	Injection *Injection
	// Found is what the ledger holds for the injection: errors, each
	// counted once however many sources report it, or pages offline.
	Found int
	// Sources counts the records that match, by source. A record that the
	// ledger marked as a second report of another is counted here too.
	Sources map[string]int
	// Differences are the fields a matching record reports differently.
	Differences []Difference
	// Unreported are the fields of the plan that no matching record
	// carries: the platform did not expose them.
	Unreported []string
	// Grades are the observation grades of the matching error records.
	Grades map[model.Grade]int
}

// OK reports whether the ledger holds what was injected: the right number
// of errors or pages, and no field that was decoded differently. Fields the
// platform does not report are not held against it.
func (c *Check) OK() bool {
	return c.Found == c.Injection.Count && len(c.Differences) == 0
}

// Result is the comparison of a plan with the ledger of one host.
type Result struct {
	Host   string
	Checks []Check
	// UnplannedErrors and UnplannedOfflines count what the ledger holds
	// beyond the plan: errors (counted once) and pages offline that match
	// no injection. Unplanned lists them, at most 50.
	UnplannedErrors   int
	UnplannedOfflines int
	Unplanned         []string
}

// OK reports whether every injection was found as planned. Records beyond
// the plan do not count against it: a host may have errors of its own.
func (r *Result) OK() bool {
	for i := range r.Checks {
		if !r.Checks[i].OK() {
			return false
		}
	}
	return true
}

// Failed returns the checks that are not OK.
func (r *Result) Failed() []*Check {
	var out []*Check
	for i := range r.Checks {
		if !r.Checks[i].OK() {
			out = append(out, &r.Checks[i])
		}
	}
	return out
}

// earlier is how long before its stated time a record may be dated and
// still belong to an injection. Sources stamp records with clocks that
// differ by seconds; a record from another day is another event at the
// same address.
const earlier = 10 * time.Minute

// Compare looks up every injection of the plan in the ledger of host.
//
// An error is found by its address: records of the same cache line and
// severity class, dated no earlier than ten minutes before the injection.
// A record belongs to one injection: several injections at one address are
// served in the order of their times, and each takes as many errors as it
// put in.
// An error the plan gives no address for is found by its row, column and
// bank. One that has neither is found by its label among the records
// without a row, as long as channel, rank, device and bank do not
// contradict the plan. A page offline is found by its page and by how the
// page was taken offline.
func Compare(ctx context.Context, s *store.Store, host string, p *Plan) (*Result, error) {
	events, err := s.EventList(ctx, store.EventFilter{Host: host, Kinds: []model.EventKind{model.KindError}, WithDuplicates: true})
	if err != nil {
		return nil, fmt.Errorf("compare: %w", err)
	}
	actions, err := s.Actions(ctx, store.ActionFilter{Host: host})
	if err != nil {
		return nil, fmt.Errorf("compare: %w", err)
	}
	res := &Result{Host: host}
	usedEvent := make([]bool, len(events))
	usedAction := make([]bool, len(actions))

	// Second reports follow the record they repeat.
	primary := map[int64]int{} // event ID -> index of the record that counts
	for j, e := range events {
		if e.DupOf == 0 {
			primary[e.ID] = j
		}
	}
	seconds := map[int][]int{}
	for j, e := range events {
		if e.DupOf != 0 {
			if pj, ok := primary[e.DupOf]; ok {
				seconds[pj] = append(seconds[pj], j)
			}
		}
	}

	// Injections are served in the order of their times, so that several
	// at one address each get the records of their own moment: a record
	// belongs to one injection, and an injection takes no more errors
	// than it put in. What is left over is beyond the plan.
	order := make([]int, len(p.Injections))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		ta, tb := p.Injections[order[a]].Time, p.Injections[order[b]].Time
		return !ta.IsZero() && !tb.IsZero() && ta.Before(tb)
	})
	res.Checks = make([]Check, len(p.Injections))
	for _, i := range order {
		in := &p.Injections[i]
		c := Check{Injection: in, Sources: map[string]int{}}
		switch in.Inject {
		case InjectError:
			c.Grades = map[model.Grade]int{}
			reported := map[string]bool{}
			take := func(j int) {
				e := events[j]
				usedEvent[j] = true
				c.Sources[e.Source]++
				c.Grades[e.Grade]++
				for _, f := range in.fields() {
					got, ok := f.of(e)
					if !ok {
						continue
					}
					reported[f.name] = true
					if got != f.want {
						c.Differences = append(c.Differences, Difference{Field: f.name, Want: f.want, Got: got, Record: e.Source + " " + e.RawRef})
					}
				}
			}
			for j, e := range events {
				if c.Found >= max(in.Count, 1) {
					break
				}
				if usedEvent[j] || e.DupOf != 0 || !in.matchesEvent(e) {
					continue
				}
				c.Found += max(e.Count, 1)
				take(j)
				for _, k := range seconds[j] {
					take(k)
				}
			}
			if len(c.Sources) > 0 {
				for _, f := range in.fields() {
					if !reported[f.name] {
						c.Unreported = append(c.Unreported, f.name)
					}
				}
			}
		case InjectOffline:
			page := in.Address.V >> 12
			for j := range actions {
				a := &actions[j]
				if a.Kind != model.ActionPageOffline || !a.OsPhysicalAddress.Valid || a.OsPhysicalAddress.V>>12 != page {
					continue
				}
				if !in.Time.IsZero() && a.TimeQuality != model.TimeRelative && a.At.Before(in.Time.Add(-earlier)) {
					continue
				}
				usedAction[j] = true
				c.Sources[a.Source]++
				if a.Method != in.How {
					c.Differences = append(c.Differences, Difference{Field: "how", Want: in.How, Got: a.Method, Record: a.Source + " " + a.RawRef})
				}
			}
			if len(c.Sources) > 0 {
				c.Found = 1
			}
		}
		res.Checks[i] = c
	}
	// A second report whose first report was claimed is accounted for.
	for j, e := range events {
		if e.DupOf != 0 {
			if pj, ok := primary[e.DupOf]; ok && usedEvent[pj] {
				usedEvent[j] = true
			}
		}
	}

	note := func(s string) {
		if len(res.Unplanned) < 50 {
			res.Unplanned = append(res.Unplanned, s)
		}
	}
	for j, e := range events {
		if usedEvent[j] || e.DupOf != 0 || e.ErrorSeverity.Class() == "" {
			continue
		}
		res.UnplannedErrors += max(e.Count, 1)
		note(fmt.Sprintf("%s %s %s", e.ErrorSeverity, e.Source, e.RawRef))
	}
	pages := map[uint64]bool{}
	for j := range actions {
		a := &actions[j]
		if usedAction[j] || a.Kind != model.ActionPageOffline {
			continue
		}
		if a.OsPhysicalAddress.Valid {
			if pages[a.OsPhysicalAddress.V>>12] {
				continue
			}
			pages[a.OsPhysicalAddress.V>>12] = true
		}
		res.UnplannedOfflines++
		note(fmt.Sprintf("page offline (%s) %s %s", a.Method, a.Source, a.RawRef))
	}
	sort.Strings(res.Unplanned)
	return res, nil
}

// matchesEvent reports whether a record of the ledger reports this error.
func (in *Injection) matchesEvent(e *model.Event) bool {
	if e.ErrorSeverity.Class() != in.Severity.Class() {
		return false
	}
	if !in.Time.IsZero() && e.TimeQuality != model.TimeRelative && e.ErrorDT.Before(in.Time.Add(-earlier)) {
		return false
	}
	switch {
	case in.Address.Valid:
		return e.OsPhysicalAddress.Valid && e.OsPhysicalAddress.V>>6 == in.Address.V>>6
	case in.Row.Valid:
		same := func(want, got model.Opt[int]) bool { return !want.Valid || (got.Valid && got.V == want.V) }
		return same(in.Row, e.Row) && same(in.Column, e.Column) && same(in.Bank, e.Bank) && same(in.BankGroup, e.BankGroup) &&
			same(in.Device, e.DeviceId) && same(in.Rank, e.Rank)
	case in.place() != "":
		// By the DIMM or device alone many errors look alike: what the
		// plan says about the place inside it must not be contradicted.
		same := func(want, got model.Opt[int]) bool { return !want.Valid || !got.Valid || got.V == want.V }
		return (model.NormalizeLabel(e.Label) == model.NormalizeLabel(in.place()) || e.Slot == in.place()) &&
			same(in.Channel, e.ChannelId) && same(in.Rank, e.Rank) && same(in.Device, e.DeviceId) &&
			same(in.BankGroup, e.BankGroup) && same(in.Bank, e.Bank) && !e.Row.Valid
	}
	return false
}

// place returns the name the ledger has for the DIMM or device of an
// injection: its label, or for a CXL memory device the kernel's name of it
// with the prefix the ledger gives such devices.
func (in *Injection) place() string {
	if in.Label == "" && in.DeviceName != "" {
		return "cxl:" + in.DeviceName
	}
	return in.Label
}

// field is one thing a plan says about an error, with how a record says it.
type field struct {
	name string
	want string
	of   func(e *model.Event) (string, bool)
}

// fields lists what the plan says about an error beyond its address.
func (in *Injection) fields() []field {
	var out []field
	num := func(name string, want model.Opt[int], get func(e *model.Event) model.Opt[int], hex bool) {
		if !want.Valid {
			return
		}
		show := func(v int) string {
			if hex {
				return "0x" + strconv.FormatInt(int64(v), 16)
			}
			return strconv.Itoa(v)
		}
		out = append(out, field{name, show(want.V), func(e *model.Event) (string, bool) {
			if got := get(e); got.Valid {
				return show(got.V), true
			}
			return "", false
		}})
	}
	out = append(out, field{"severity", severityWord(in.Severity), func(e *model.Event) (string, bool) { return severityWord(e.ErrorSeverity), true }})
	if in.Scrub != "" {
		out = append(out, field{"scrub", string(in.Scrub), func(e *model.Event) (string, bool) { return string(e.ErrorScrubType), e.ErrorScrubType != "" }})
	}
	num("socket", in.Socket, func(e *model.Event) model.Opt[int] { return e.SocketId }, false)
	num("channel", in.Channel, func(e *model.Event) model.Opt[int] { return e.ChannelId }, false)
	num("slot", in.Slot, func(e *model.Event) model.Opt[int] { return e.ChannelSlotId }, false)
	num("rank", in.Rank, func(e *model.Event) model.Opt[int] { return e.Rank }, false)
	num("device", in.Device, func(e *model.Event) model.Opt[int] { return e.DeviceId }, false)
	num("bank_group", in.BankGroup, func(e *model.Event) model.Opt[int] { return e.BankGroup }, false)
	num("bank", in.Bank, func(e *model.Event) model.Opt[int] { return e.Bank }, false)
	num("row", in.Row, func(e *model.Event) model.Opt[int] { return e.Row }, true)
	num("column", in.Column, func(e *model.Event) model.Opt[int] { return e.Column }, true)
	if in.Bit.Valid {
		out = append(out, field{"bit", strconv.Itoa(in.Bit.V), func(e *model.Event) (string, bool) {
			if rest, ok := strings.CutPrefix(e.ErrorVector, "bit:"); ok {
				return rest, true
			}
			return "", false
		}})
	}
	if in.place() != "" {
		// The DIMM is right when the record names it, when the ledger tied
		// the record to the slot of that name, or when the label of the
		// record ends in the name: ghes_edac puts the bank locator before
		// the device locator ("BANK 0 DIMM 0" for "DIMM 0").
		want := model.NormalizeLabel(in.place())
		out = append(out, field{"label", want, func(e *model.Event) (string, bool) {
			label, slot := model.NormalizeLabel(e.Label), model.NormalizeLabel(e.Slot)
			switch {
			case label == want || slot == want || strings.HasSuffix(label, " "+want):
				return want, true
			case slot != "":
				return slot, true
			case label != "":
				return label, true
			}
			return "", false
		}})
	}
	return out
}

// severityWord names a severity as far as every source can tell it. EDAC
// has one word for an uncorrected error the system survived, where a
// firmware record and a machine check say "recoverable".
func severityWord(s model.Severity) string {
	switch s {
	case model.SevCE:
		return "corrected"
	case model.SevRecoverable, model.SevUE:
		return "uncorrected"
	case model.SevFatal:
		return "fatal"
	}
	return strings.ToLower(string(s))
}
