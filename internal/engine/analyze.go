package engine

import (
	"sort"
	"time"

	"github.com/agenticode/my/internal/model"
)

// Result is what the engine found in a ledger.
type Result struct {
	// EngineVersion is the Version of the engine that produced the result.
	EngineVersion string `json:"engine_version"`
	// Config holds the thresholds the result was computed with.
	Config Config `json:"config"`
	// Hosts is ordered by host name.
	Hosts []*HostResult `json:"hosts"`

	in *Input
}

// HostResult holds the findings for one host.
type HostResult struct {
	Host string `json:"host"`
	// AsOf is the reference time: the latest time the ledger knows
	// something about the host, or the time the caller asked for. "Recent"
	// and "quiet since" are counted back from it, so a case bundle gives
	// the same result whenever it is opened.
	AsOf time.Time `json:"as_of"`
	// ObservedFrom is the earliest time the ledger knows something about
	// the host. It bounds how much history a trend can rest on.
	ObservedFrom time.Time `json:"observed_from,omitzero"`
	// DIMMs holds every DIMM whose latest known place is this host, with
	// or without errors, ordered by error count and slot.
	DIMMs []*DIMMResult `json:"dimms"`
	// Unattributed counts errors of the host that could not be tied to a
	// DIMM. They are in no fault.
	Unattributed []Unattributed `json:"unattributed,omitzero"`
}

// DIMMResult holds the findings for one DIMM.
type DIMMResult struct {
	// UID is the DIMM identity of the ledger.
	UID string `json:"dimm_uid"`
	// DIMM describes the module, or is nil when the ledger has no record
	// of it.
	DIMM *model.DIMM `json:"dimm,omitzero"`
	// Host and Slot are the latest known place of the DIMM.
	Host string `json:"host"`
	Slot string `json:"slot,omitzero"`
	// Present is set when the DIMM is the latest module seen in that slot.
	// A DIMM that was replaced keeps its history and is not present.
	Present bool `json:"present"`

	// Faults are ordered by error count, largest first.
	Faults   []*Fault `json:"faults,omitzero"`
	Features Features `json:"features"`
	Baseline Baseline `json:"baseline"`

	// events are the records of the DIMM in Input.events.
	events []int32
	// actions are the actions attributed to the DIMM, oldest first.
	actions []*model.Action
	// windowErrors is the number of errors in the recent and the baseline
	// window, for the peer comparison.
	windowErrors int64
	clk          clock
}

// until returns a view of the input as the ledger would have looked at the
// given time: later records are left out.
func (in *Input) until(t time.Time) *Input {
	at := t.UnixMicro()
	out := *in
	out.events = nil
	for i := range in.events {
		if e := &in.events[i]; e.relative || e.at <= at {
			out.events = append(out.events, *e)
		}
	}
	out.Actions = nil
	for _, a := range in.Actions {
		if !a.At.After(t) {
			out.Actions = append(out.Actions, a)
		}
	}
	out.Placements = nil
	for _, p := range in.Placements {
		if p.FirstSeen.After(t) {
			continue
		}
		if p.LastSeen.After(t) {
			p.LastSeen = t
		}
		out.Placements = append(out.Placements, p)
	}
	out.Boots = nil
	for _, b := range in.Boots {
		if b.StartedAt.After(t) && b.StartQuality != model.TimeRelative {
			continue
		}
		if b.LastSeen.After(t) {
			b.LastSeen = t
		}
		out.Boots = append(out.Boots, b)
	}
	keep := map[int64]bool{}
	out.Scans = nil
	for _, sc := range in.Scans {
		if !sc.StartedAt.After(t) {
			keep[sc.ID] = true
			out.Scans = append(out.Scans, sc)
		}
	}
	out.Sources = nil
	for _, src := range in.Sources {
		if keep[src.ScanID] {
			out.Sources = append(out.Sources, src)
		}
	}
	out.GPUs = nil
	for _, g := range in.GPUs {
		if !g.ObservedAt.After(t) {
			out.GPUs = append(out.GPUs, g)
		}
	}
	return &out
}

// hostSpan returns, per host, the latest and the earliest time the ledger
// knows something about it.
func (in *Input) hostSpans() (latest, earliest map[string]int64) {
	latest, earliest = map[string]int64{}, map[string]int64{}
	seen := func(host string, t time.Time, bound bool) {
		if t.IsZero() || t.Year() < 1971 {
			return // unset, or seconds since boot counted from the epoch
		}
		at := t.UnixMicro()
		if at > latest[host] {
			latest[host] = at
		}
		if bound {
			if old, ok := earliest[host]; !ok || at < old {
				earliest[host] = at
			}
		}
	}
	for i := range in.events {
		e := &in.events[i]
		if !e.relative {
			seen(in.hostNames[e.host], timeOf(e.at), true)
		}
	}
	for _, a := range in.Actions {
		if a.TimeQuality != model.TimeRelative {
			seen(a.Host, a.At, true)
		}
	}
	for _, b := range in.Boots {
		if b.StartQuality != model.TimeRelative {
			seen(b.Host, b.StartedAt, true)
			seen(b.Host, b.LastSeen, false)
		}
	}
	for _, p := range in.Placements {
		seen(p.Host, p.LastSeen, false)
	}
	for _, sc := range in.Scans {
		// A live scan looked at the host at that moment. An import says
		// when files were read, not when the host was looked at.
		if sc.Kind == model.ScanLive || sc.Kind == model.ScanSynthetic {
			seen(sc.Host, sc.StartedAt, false)
			seen(sc.Host, sc.FinishedAt, false)
		}
	}
	return latest, earliest
}

// Analyze runs the engine over the input: it groups the errors of every
// DIMM into faults, measures the faults and the DIMMs, and ranks each DIMM
// against its own past and its peers.
func Analyze(in *Input, opts Options) *Result {
	if !opts.AsOf.IsZero() {
		in = in.until(opts.AsOf)
	}
	cfg := opts.config()
	res := &Result{EngineVersion: Version, Config: *cfg, in: in}
	cls := Classify(in, opts)

	latest, earliest := in.hostSpans()
	clocks := map[string]clock{}
	hosts := map[string]*HostResult{}
	host := func(name string) *HostResult {
		if h, ok := hosts[name]; ok {
			return h
		}
		asOf, from := latest[name], earliest[name]
		if !opts.AsOf.IsZero() {
			asOf = opts.AsOf.UnixMicro()
		}
		if from == 0 || from > asOf {
			from = asOf
		}
		h := &HostResult{Host: name}
		if asOf > 0 {
			h.AsOf, h.ObservedFrom = timeOf(asOf), timeOf(from)
		}
		clocks[name] = newClock(asOf, from, cfg)
		hosts[name] = h
		return h
	}
	for _, h := range in.Hosts {
		host(h.Name)
	}
	for _, name := range in.hostNames {
		host(name)
	}

	// Where each DIMM was seen last, and which DIMM each slot held last.
	type place struct {
		host, slot string
		at         int64
		id         int64
	}
	later := func(a, b place) bool { return a.at > b.at || (a.at == b.at && a.id > b.id) }
	lastOfDIMM := map[string]place{}
	type hostSlot struct{ host, slot string }
	lastInSlot := map[hostSlot]string{}
	lastInSlotAt := map[hostSlot]place{}
	for _, p := range in.Placements {
		pl := place{p.Host, p.Slot, p.LastSeen.UnixMicro(), p.ID}
		if old, ok := lastOfDIMM[p.DIMMUID]; !ok || later(pl, old) {
			lastOfDIMM[p.DIMMUID] = pl
		}
		k := hostSlot{p.Host, p.Slot}
		if old, ok := lastInSlotAt[k]; !ok || later(pl, old) {
			lastInSlotAt[k], lastInSlot[k] = pl, p.DIMMUID
		}
	}
	empty := map[hostSlot]bool{}
	for _, sl := range in.Slots {
		if sl.Populated.Valid && !sl.Populated.V {
			empty[hostSlot{sl.Host, sl.Name}] = true
		}
	}

	info := map[string]*model.DIMM{}
	for i := range in.DIMMs {
		info[in.DIMMs[i].UID] = &in.DIMMs[i]
	}
	faults := map[string][]*Fault{}
	for _, d := range cls.DIMMs {
		faults[d.UID] = d.Faults
	}
	events := in.byDIMM()
	actions := map[string][]*model.Action{}
	for i := range in.Actions {
		if a := &in.Actions[i]; a.DIMMUID != "" {
			actions[a.DIMMUID] = append(actions[a.DIMMUID], a)
		}
	}

	// Subjects: every DIMM with errors, actions or a place in a host.
	uids := map[string]bool{}
	for uid := range lastOfDIMM {
		uids[uid] = true
	}
	for uid := range faults {
		uids[uid] = true
	}
	for uid := range actions {
		uids[uid] = true
	}
	var all []*DIMMResult
	for uid := range uids {
		d := &DIMMResult{UID: uid, DIMM: info[uid], Faults: faults[uid], actions: actions[uid]}
		if i, ok := in.dimmIdx[uid]; ok {
			d.events = events[i]
		}
		sort.SliceStable(d.actions, func(i, j int) bool { return d.actions[i].At.Before(d.actions[j].At) })
		switch pl, ok := lastOfDIMM[uid]; {
		case ok:
			d.Host, d.Slot = pl.host, pl.slot
			k := hostSlot{pl.host, pl.slot}
			d.Present = lastInSlot[k] == uid && !empty[k]
		case len(d.Faults) > 0:
			// No inventory names the DIMM: it is known through its errors.
			var last *Fault
			for _, f := range d.Faults {
				if last == nil || f.LastSeen.After(last.LastSeen) {
					last = f
				}
			}
			d.Host, d.Slot, d.Present = last.Host, last.Slot, true
		case len(d.actions) > 0:
			a := d.actions[len(d.actions)-1]
			d.Host, d.Slot, d.Present = a.Host, a.Slot, true
		}
		h := host(d.Host)
		d.clk = clocks[d.Host]
		m := &measurer{in: in, cfg: cfg, clk: d.clk}
		width := 0
		if d.DIMM != nil {
			width = d.DIMM.DeviceWidth.Or(0)
		}
		for _, f := range d.Faults {
			m.measureFault(f, d.actions, width)
		}
		d.Features = m.measureDIMM(d.events, d.Faults, d.actions, width)
		d.Baseline.Own = m.ownBaseline(d.events, d.Features.Trend)
		d.windowErrors = m.windowErrors(d.events)
		h.DIMMs = append(h.DIMMs, d)
		all = append(all, d)
	}
	peerBaselines(all, cfg, opts.PartBaseline)

	for _, u := range cls.Unattributed {
		h := host(u.Host)
		h.Unattributed = append(h.Unattributed, u)
	}
	for name, h := range hosts {
		if opts.Host != "" && name != opts.Host {
			continue
		}
		sort.SliceStable(h.DIMMs, func(i, j int) bool {
			a, b := h.DIMMs[i], h.DIMMs[j]
			if x, y := a.Features.CE+a.Features.UE, b.Features.CE+b.Features.UE; x != y {
				return x > y
			}
			if a.Slot != b.Slot {
				return a.Slot < b.Slot
			}
			return a.UID < b.UID
		})
		res.Hosts = append(res.Hosts, h)
	}
	sort.Slice(res.Hosts, func(i, j int) bool { return res.Hosts[i].Host < res.Hosts[j].Host })
	return res
}

// Host returns the result of the named host, or nil.
func (r *Result) Host(name string) *HostResult {
	for _, h := range r.Hosts {
		if h.Host == name {
			return h
		}
	}
	return nil
}

// DIMM returns the result of the DIMM with the given identity, or nil.
func (h *HostResult) DIMM(uid string) *DIMMResult {
	for _, d := range h.DIMMs {
		if d.UID == uid {
			return d
		}
	}
	return nil
}

// InSlot returns the result of the DIMM that is present in the named slot,
// or nil.
func (h *HostResult) InSlot(slot string) *DIMMResult {
	for _, d := range h.DIMMs {
		if d.Present && d.Slot == slot {
			return d
		}
	}
	return nil
}
