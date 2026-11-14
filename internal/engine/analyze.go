package engine

import (
	"fmt"
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
	// Grade is the finding for the host: the grade of its worst DIMM or
	// GPU. Hold replacement outranks the other grades. DIMMs that are no
	// longer in the host do not count.
	Grade model.VerdictGrade `json:"grade"`
	// DIMMs holds every DIMM whose latest known place is this host, with
	// or without errors, ordered by grade, error count and slot.
	DIMMs []*DIMMResult `json:"dimms"`
	// GPUs holds the findings about GPU memory. A GPU is listed when a
	// rule fired for it.
	GPUs []*GPUResult `json:"gpus,omitzero"`
	// Unattributed counts errors of the host that could not be tied to a
	// DIMM. They are in no fault.
	Unattributed []Unattributed `json:"unattributed,omitzero"`
}

// GPUResult holds the finding about the memory of one GPU.
type GPUResult struct {
	// Key identifies the GPU: its serial number, or its UUID.
	Key     string         `json:"gpu"`
	Index   model.Opt[int] `json:"index,omitzero"`
	Verdict *model.Verdict `json:"verdict"`
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

	// Verdict is the finding for the DIMM.
	Verdict *model.Verdict `json:"verdict"`
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
// DIMM into faults, measures the faults and the DIMMs, ranks each DIMM
// against its own past and its peers, and applies the rules. The result
// holds one finding per DIMM.
//
// It returns an error when a threshold of the configuration is outside its
// bounds.
func Analyze(in *Input, opts Options) (*Result, error) {
	cfg := opts.config()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("engine: thresholds: %w", err)
	}
	if !opts.AsOf.IsZero() {
		in = in.until(opts.AsOf)
	}
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

	// Findings. The system rules look at several DIMMs at once and run
	// first; they can turn faults into system issues.
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	jd := &judge{in: in, cfg: cfg, now: now.UTC().Truncate(time.Microsecond), extra: map[*DIMMResult]map[string][]model.RecordRef{}, hosts: map[string]*hostFacts{}}
	for name, h := range hosts {
		jd.hosts[name] = newHostFacts(in, name, h)
	}
	jd.sys = newSystemView(in, cfg, all)
	jd.sys.run(jd)
	for _, d := range all {
		d.Verdict = jd.verdict(d)
	}
	for name, h := range hosts {
		h.GPUs = jd.gpus(name)
	}

	for name, h := range hosts {
		if opts.Host != "" && name != opts.Host {
			continue
		}
		sort.SliceStable(h.DIMMs, func(i, j int) bool {
			a, b := h.DIMMs[i], h.DIMMs[j]
			if x, y := gradeRank(a.Verdict.Grade), gradeRank(b.Verdict.Grade); x != y {
				return x > y
			}
			if x, y := a.Features.CE+a.Features.UE, b.Features.CE+b.Features.UE; x != y {
				return x > y
			}
			if a.Slot != b.Slot {
				return a.Slot < b.Slot
			}
			return a.UID < b.UID
		})
		h.Grade = model.VerdictOK
		for _, d := range h.DIMMs {
			if d.Present && worse(d.Verdict.Grade, h.Grade) {
				h.Grade = d.Verdict.Grade
			}
		}
		for _, g := range h.GPUs {
			if worse(g.Verdict.Grade, h.Grade) {
				h.Grade = g.Verdict.Grade
			}
		}
		res.Hosts = append(res.Hosts, h)
	}
	sort.Slice(res.Hosts, func(i, j int) bool { return res.Hosts[i].Host < res.Hosts[j].Host })
	return res, nil
}

// newHostFacts collects what the findings of a host share.
func newHostFacts(in *Input, name string, h *HostResult) *hostFacts {
	hf := &hostFacts{ambiguous: map[string]int64{}, smbiosSlot: map[string]bool{}}
	var latest *model.Scan
	scans := 0
	ids := map[int64]bool{}
	for i := range in.Scans {
		sc := &in.Scans[i]
		if sc.Host != name {
			continue
		}
		scans++
		ids[sc.ID] = true
		if latest == nil || sc.StartedAt.After(latest.StartedAt) || (sc.StartedAt.Equal(latest.StartedAt) && sc.ID > latest.ID) {
			latest = sc
		}
	}
	for _, src := range in.Sources {
		if !ids[src.ScanID] {
			continue
		}
		if latest != nil && src.ScanID == latest.ID {
			hf.sources = append(hf.sources, src)
		}
		if errorSourceKinds[src.Kind] {
			switch src.Status {
			case model.SourceOK, model.SourcePartial, model.SourceEmpty:
				hf.errorSource = true
			}
		}
	}
	// Without a scan on record nothing is known about what was read, and
	// errors in the ledger came from some source.
	if scans == 0 {
		hf.errorSource = true
	}
	for i := range in.events {
		if in.hostNames[in.events[i].host] == name {
			hf.errorSource = true
			break
		}
	}
	for _, sl := range in.Slots {
		if sl.Host == name && (sl.Locator != "" || sl.SMBIOSHandle.Valid) {
			hf.inventory = true
			hf.smbiosSlot[sl.Name] = true
		}
	}
	for _, p := range in.Placements {
		if p.Host == name {
			hf.inventory = true
		}
	}
	for _, u := range h.Unattributed {
		switch {
		case u.Slot == "":
			hf.unplaced += u.CE + u.UE
		case u.Ambiguous:
			hf.ambiguous[u.Slot] += u.CE + u.UE
		}
	}
	for _, b := range in.Boots {
		if b.Host == name && b.StartQuality != model.TimeRelative {
			hf.boots = append(hf.boots, b)
		}
	}
	sort.SliceStable(hf.boots, func(i, j int) bool { return hf.boots[i].StartedAt.Before(hf.boots[j].StartedAt) })
	return hf
}

// gpus applies R-REP-01 to the GPUs of a host: a row remap that failed
// leaves nothing to repair with.
func (j *judge) gpus(host string) []*GPUResult {
	type gpu struct {
		key      string
		index    model.Opt[int]
		xid64    []*model.GPUMem
		snapshot *model.GPUMem
	}
	by := map[string]*gpu{}
	var keys []string
	for i := range j.in.GPUs {
		r := &j.in.GPUs[i]
		if r.Host != host {
			continue
		}
		key := r.GPUSerial
		if key == "" {
			key = r.GPUUUID
		}
		if key == "" && r.PCIBus != "" {
			key = r.PCIBus
		}
		if key == "" && r.GPUIndex.Valid {
			key = fmt.Sprintf("GPU %d", r.GPUIndex.V)
		}
		if key == "" {
			continue
		}
		g := by[key]
		if g == nil {
			g = &gpu{key: key}
			by[key] = g
			keys = append(keys, key)
		}
		if r.GPUIndex.Valid {
			g.index = r.GPUIndex
		}
		switch {
		case r.Kind == model.GPUXID && r.XID.Valid && r.XID.V == 64:
			g.xid64 = append(g.xid64, r)
		case r.Kind == model.GPUSnapshot && (g.snapshot == nil || r.ObservedAt.After(g.snapshot.ObservedAt)):
			g.snapshot = r
		}
	}
	sort.Strings(keys)
	var out []*GPUResult
	for _, key := range keys {
		g := by[key]
		var ev []model.Evidence
		if n := len(g.xid64); n > 0 {
			last := g.xid64[n-1]
			e := model.Evidence{Rule: RuleRepairGone, Statement: statement("gpu.xid64", "when", stampText(last.ObservedAt.UnixMicro())), Total: n}
			for _, r := range g.xid64 {
				if len(e.Records) < j.cfg.MaxEvidenceRecords {
					e.Records = append(e.Records, model.RecordRef{Kind: model.RecordGPU, ID: r.ID, RawRef: r.RawRef})
				}
			}
			ev = append(ev, e)
		}
		if s := g.snapshot; s != nil && s.RemapFailure.Valid && s.RemapFailure.V {
			ev = append(ev, model.Evidence{Rule: RuleRepairGone, Statement: statement("gpu.remap_failure", "when", stampText(s.ObservedAt.UnixMicro())), Total: 1,
				Records: []model.RecordRef{{Kind: model.RecordGPU, ID: s.ID, RawRef: s.RawRef}}})
		}
		if len(ev) == 0 {
			continue
		}
		v := &model.Verdict{Host: host, GPU: key, Grade: model.VerdictReplaceNow, Label: model.VerdictLabel, Rules: []string{RuleRepairGone},
			Evidence: ev, NextAction: statement("next.gpu_replace"), EngineVersion: Version, ComputedAt: j.now}
		v.Confidence = baseConfidence[RuleRepairGone]
		v.ConfidenceFactors = []model.Factor{{Statement: statement("conf.rule", "rule", RuleRepairGone), Delta: v.Confidence}}
		out = append(out, &GPUResult{Key: key, Index: g.index, Verdict: v})
	}
	return out
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
