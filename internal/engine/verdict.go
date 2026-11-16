package engine

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/agenticode/my/internal/decode/virt"
	"github.com/agenticode/my/internal/model"
)

// judge turns the measurements of the DIMMs into findings.
type judge struct {
	// loop is the view of the boots (recurrence.go).
	loop *loopView
	in   *Input
	cfg  *Config
	sys  *systemView
	now  time.Time

	// extra holds record references added to the evidence of a rule after
	// the statement was made.
	extra map[*DIMMResult]map[string][]model.RecordRef
	// hosts holds what is known per host.
	hosts map[string]*hostFacts
	// ecs names the DIMMs with ECS readings; it is built on first use.
	ecs map[string]bool
	// fwi indexes the firmware records; it is built on first use.
	fwi *fwIndex
}

// hasECS reports whether the ledger holds ECS readings of the DIMM.
func (j *judge) hasECS(uid string) bool {
	if j.ecs == nil {
		j.ecs = map[string]bool{}
		for i := range j.in.ECS {
			if u := j.in.ECS[i].DIMMUID; u != "" {
				j.ecs[u] = true
			}
		}
	}
	return j.ecs[uid]
}

// hostFacts is what the findings of a host share.
type hostFacts struct {
	// sources is the source inventory of the latest scan of the host.
	sources []model.Source
	// errorSource is set when some source of error records was read.
	errorSource bool
	// inventory is set when SMBIOS described the slots of the host, and
	// smbiosSlot names the slots it described.
	inventory  bool
	smbiosSlot map[string]bool
	// unplaced counts errors of the host that name no slot; ambiguous
	// counts, per slot, errors between two DIMMs.
	unplaced  int64
	ambiguous map[string]int64
	boots     []model.Boot
	// virtual names the hypervisor of a host that is a virtual machine, or
	// is empty.
	virtual string
	// bios is what the BIOS settings on record say about how to read the
	// records of the host. bmcSource is set when a BMC is among the
	// sources of the latest scan, hostSource when a source of the host's
	// own error records was read.
	bios       biosFacts
	bmcSource  bool
	hostSource bool
	// live is set when the latest scan of the host read the running host:
	// its latest boot was running then.
	live bool
}

func (j *judge) asOf(d *DIMMResult) int64 { return d.clk.asOf }

// Texts for places and times. They hold no words of one language: field
// names such as rank and row are terms in both.

func dateText(micro int64) string {
	if micro == 0 {
		return "?"
	}
	return timeOf(micro).Format("2006-01-02")
}

func stampText(micro int64) string {
	if micro == 0 {
		return "?"
	}
	return timeOf(micro).Format("2006-01-02 15:04") + " UTC"
}

// bootText writes a time that is only known as seconds since boot.
func bootText(micro int64) string {
	return fmt.Sprintf("boot+%ds", micro/int64(time.Second/time.Microsecond))
}

// actionWhen writes the day of an action, or its offset from boot when the
// source recorded no date.
func actionWhen(at time.Time, q model.TimeQuality) string {
	if q == model.TimeRelative {
		return bootText(at.UnixMicro())
	}
	return dateText(at.UnixMicro())
}

// faultDay writes a day in the life of a fault, or the offset from boot
// when none of its records carries a date.
func faultDay(f *Fault, t time.Time) string {
	if f.Undated > 0 && f.Undated == int64(f.ErrorCount) {
		return bootText(t.UnixMicro())
	}
	return dateText(t.UnixMicro())
}

func placeText(rank, dev, bg, bank model.Opt[int]) []string {
	var parts []string
	add := func(name string, o model.Opt[int]) {
		if o.Valid {
			parts = append(parts, fmt.Sprintf("%s%d", name, o.V))
		}
	}
	add("rank", rank)
	add("dev", dev)
	add("bg", bg)
	add("bank", bank)
	return parts
}

// where names the place of a fault.
func where(f *Fault) string {
	parts := placeText(f.Rank, f.DeviceId, f.BankGroup, f.Bank)
	rng := func(name string, lo, hi model.Opt[int]) {
		switch {
		case !lo.Valid:
		case lo == hi:
			parts = append(parts, fmt.Sprintf("%s %#x", name, lo.V))
		default:
			parts = append(parts, fmt.Sprintf("%s %#x-%#x", name, lo.V, hi.V))
		}
	}
	switch f.Mode {
	case model.ModeSingleBit:
		rng("row", f.RowMin, f.RowMax)
		rng("col", f.ColumnMin, f.ColumnMax)
	case model.ModeRow:
		if f.TopRow.Valid {
			rng("row", f.TopRow, f.TopRow)
		} else {
			rng("row", f.RowMin, f.RowMax)
		}
	case model.ModeMultiRow:
		rng("row", f.RowMin, f.RowMax)
	case model.ModeColumn:
		rng("col", f.ColumnMin, f.ColumnMax)
	}
	if f.Grade != model.GradeA && f.AddressMin.Valid {
		if f.AddressMin == f.AddressMax {
			parts = append(parts, fmt.Sprintf("address %#x", f.AddressMin.V))
		} else {
			parts = append(parts, fmt.Sprintf("address %#x-%#x", f.AddressMin.V, f.AddressMax.V))
		}
	}
	return strings.Join(parts, " ")
}

// where names the place of a fault. A fault that covers several devices
// has no single place: its ranks and devices are listed.
func (j *judge) where(f *Fault) string {
	if w := where(f); w != "" {
		return w
	}
	ranks, devs := map[int16]bool{}, map[int16]bool{}
	for _, i := range f.members {
		e := &j.in.events[i]
		if e.rank >= 0 {
			ranks[e.rank] = true
		}
		if e.dev >= 0 {
			devs[e.dev] = true
		}
	}
	list := func(name string, set map[int16]bool) string {
		if len(set) == 0 {
			return ""
		}
		if len(set) > 6 {
			return fmt.Sprintf("%s x%d", name, len(set))
		}
		vals := make([]int, 0, len(set))
		for v := range set {
			vals = append(vals, int(v))
		}
		sort.Ints(vals)
		parts := make([]string, len(vals))
		for i, v := range vals {
			parts[i] = fmt.Sprint(v)
		}
		return name + strings.Join(parts, ",")
	}
	w := strings.TrimSpace(list("rank", ranks) + " " + list("dev", devs))
	if w == "" {
		return "DIMM"
	}
	return w
}

// eventWhere names the place of one record, or returns "" when the record
// carries none.
func eventWhere(e *event) string {
	parts := placeText(optOf(e.rank), optOf(e.dev), optOf(e.bg), optOf(e.bank))
	if e.row >= 0 {
		parts = append(parts, fmt.Sprintf("row %#x", e.row))
	}
	if e.col >= 0 {
		parts = append(parts, fmt.Sprintf("col %#x", e.col))
	}
	if e.row < 0 && e.hasAddr {
		parts = append(parts, fmt.Sprintf("address %#x", e.addr))
	}
	return strings.Join(parts, " ")
}

// actionWhere names the place an action was aimed at, or returns "".
func actionWhere(a *model.Action) string {
	parts := placeText(a.Rank, a.DeviceId, a.BankGroup, a.Bank)
	if a.Row.Valid {
		parts = append(parts, fmt.Sprintf("row %#x", a.Row.V))
	}
	if len(parts) == 0 && a.OsPhysicalAddress.Valid {
		parts = append(parts, fmt.Sprintf("page %#x", a.OsPhysicalAddress.V>>12))
	}
	return strings.Join(parts, " ")
}

func modeArg(f *Fault) string { return "@mode." + string(f.Mode) }

func kindArg(k model.ActionKind) string { return "@kind." + string(k) }

// eventRefs returns references to the given records in time order. When
// there are more than MaxEvidenceRecords, the first and the last of them
// are kept; total is the number of records.
func (j *judge) eventRefs(idx []int32) (refs []model.RecordRef, total int) {
	if len(idx) == 0 {
		return nil, 0
	}
	evs := j.in.events
	sorted := append([]int32(nil), idx...)
	sort.Slice(sorted, func(a, b int) bool {
		x, y := &evs[sorted[a]], &evs[sorted[b]]
		if x.relative != y.relative {
			return x.relative
		}
		if x.at != y.at {
			return x.at < y.at
		}
		return x.id < y.id
	})
	limit := max(j.cfg.MaxEvidenceRecords, 1)
	if len(sorted) > limit {
		head := (limit + 1) / 2
		sorted = append(sorted[:head:head], sorted[len(sorted)-(limit-head):]...)
	}
	refs = make([]model.RecordRef, len(sorted))
	for i, m := range sorted {
		refs[i] = model.RecordRef{Kind: model.RecordEvent, ID: evs[m].id}
	}
	return refs, len(idx)
}

func (j *judge) addRecord(d *DIMMResult, rule string, ref model.RecordRef) {
	if j.extra[d] == nil {
		j.extra[d] = map[string][]model.RecordRef{}
	}
	j.extra[d][rule] = append(j.extra[d][rule], ref)
}

func actionRef(id int64, rawRef string) model.RecordRef {
	return model.RecordRef{Kind: model.RecordAction, ID: id, RawRef: rawRef}
}

// basis notes what a rule's evidence rests on, for the confidence.
type basis struct {
	gradeB, gradeC, estimated bool
}

// findings holds the pieces of a verdict while it is put together.
type findings struct {
	d        *DIMMResult
	evidence []model.Evidence
	checks   []model.Check
	basis    map[string]*basis
	// covered marks the faults a rule other than R-SOFT-01 spoke about.
	covered map[*Fault]bool
	// quietShort are faults that were repaired and are silent for less
	// than QuietDays.
	quietShort []model.Check
	// unlocated is set when R-SOFT-01 spoke about errors without rows and
	// columns.
	unlocated bool
	// fwWording is set when the firmware record that decided the grade was
	// classed by its wording.
	fwWording bool
}

func (fs *findings) add(rule string, st model.Statement, refs []model.RecordRef, total int) {
	fs.evidence = append(fs.evidence, model.Evidence{Rule: rule, Statement: st, Records: refs, Total: total})
}

func (fs *findings) note(rule string, f *Fault) {
	b := fs.basis[rule]
	if b == nil {
		b = &basis{}
		fs.basis[rule] = b
	}
	switch {
	case f.Grade == model.GradeC:
		b.gradeC = true
	case f.Grade == model.GradeB:
		b.gradeB = true
	}
	if f.Estimated {
		b.estimated = true
	}
}

func (fs *findings) has(rule string) bool {
	for _, e := range fs.evidence {
		if e.Rule == rule {
			return true
		}
	}
	return false
}

// uncorrected is R-UE-01 and R-SCRUB-01: uncorrected errors, split by
// whether the patrol scrubber found them before anything read the data.
func (j *judge) uncorrected(fs *findings) {
	evs := j.in.events
	var demand, patrol []int32
	for _, i := range fs.d.events {
		e := &evs[i]
		if e.sev == sevCE || e.at > fs.d.clk.asOf && !e.relative {
			continue
		}
		if e.scrub == scrubPatrol && e.sev != sevFatal {
			patrol = append(patrol, i)
		} else {
			demand = append(demand, i)
		}
	}
	state := func(rule, located, unlocated string, idx []int32) {
		if len(idx) == 0 {
			return
		}
		var n int64
		last := &evs[idx[0]]
		counted := true
		for _, i := range idx {
			e := &evs[i]
			n += int64(e.errors)
			if !e.relative && (last.relative || e.at >= last.at) {
				last = e
			}
			counted = counted && e.counter
		}
		refs, total := j.eventRefs(idx)
		when := stampText(last.at)
		if last.relative {
			when = bootText(last.at)
		}
		if w := eventWhere(last); w != "" {
			fs.add(rule, statement(located, "n", n, "when", when, "where", w), refs, total)
		} else {
			fs.add(rule, statement(unlocated, "n", n, "when", when), refs, total)
		}
		// R-LOOP-01 and R-LOOP-02 say already that the boots of these
		// errors stopped.
		if st, refs, total, ok := j.stopped(idx); ok && !fs.has(RuleLoopPlace) && !fs.has(RuleLoopDIMM) {
			fs.add(rule, st, refs, total)
		}
		if counted {
			if fs.basis[rule] == nil {
				fs.basis[rule] = &basis{}
			}
			fs.basis[rule].gradeC = true
		}
	}
	state(RuleUE, "ue.located", "ue.unlocated", demand)
	state(RuleScrub, "scrub.located", "scrub.unlocated", patrol)
}

// stopped says which of the uncorrected errors idx stopped the host. The
// statement comes after the one that counts them, under the same rule. Its
// records are the errors and the kernel's lines of the stop.
func (j *judge) stopped(idx []int32) (model.Statement, []model.RecordRef, int, bool) {
	stops := j.in.stopsByEvent()
	if len(stops) == 0 {
		return model.Statement{}, nil, 0, false
	}
	evs := j.in.events
	var hit []int32
	for _, i := range idx {
		if _, ok := stops[evs[i].id]; ok {
			hit = append(hit, i)
		}
	}
	if len(hit) == 0 {
		return model.Statement{}, nil, 0, false
	}
	last := &evs[hit[0]]
	for _, i := range hit {
		if e := &evs[i]; !e.relative && (last.relative || e.at >= last.at) {
			last = e
		}
	}
	when := stampText(last.at)
	if last.relative {
		when = bootText(last.at)
	}
	refs, total := j.eventRefs(hit)
	for _, i := range hit {
		if st := stops[evs[i].id]; st.StopID != 0 {
			refs = append(refs, model.RecordRef{Kind: model.RecordEvent, ID: st.StopID})
			total++
		}
	}
	if len(hit) == 1 {
		return statement("ue.stopped", "when", when), refs, total, true
	}
	return statement("ue.stopped_many", "n", len(hit), "when", when), refs, total, true
}

// firmware is R-FW-01 and R-REP-01: what firmware says about the module,
// and repairs that have nothing left to give.
func (j *judge) firmware(fs *findings) {
	who := func(a *model.Action) string {
		switch strings.ToLower(a.Method) {
		case "bios":
			return "@word.bios"
		case "bmc":
			return "@word.bmc"
		}
		return "@word.firmware"
	}
	var disabled, failed, sparing *model.Action
	for _, a := range fs.d.actions {
		// A mark a later record took back is history: it holds no more.
		if a.WithdrawnBy != 0 {
			continue
		}
		if a.Result == model.ResultFailed {
			if j.failedRepair(fs, a, who) {
				continue
			}
			if repairKind(a.Kind) && a.Kind != model.ActionSparing && a.Kind != model.ActionDeviceSparing {
				refs := []model.RecordRef{actionRef(a.ID, a.RawRef)}
				if w := actionWhere(a); w != "" {
					fs.add(RuleRepairGone, statement("rep.failed", "kind", kindArg(a.Kind), "where", w, "when", actionWhen(a.At, a.TimeQuality)), refs, 1)
				} else {
					fs.add(RuleRepairGone, statement("rep.failed_unlocated", "kind", kindArg(a.Kind), "when", actionWhen(a.At, a.TimeQuality)), refs, 1)
				}
			}
			continue
		}
		switch a.Kind {
		case model.ActionDisable:
			disabled = a // the latest
		case model.ActionMarkFailed:
			if failed == nil {
				failed = a // since the first
			}
		case model.ActionSparing:
			// Sparing that waits for its copy has not taken the spare yet.
			if sparing == nil && a.Result != model.ResultPending {
				sparing = a
			}
		}
	}
	mark := func(a *model.Action, code string) {
		if a == nil {
			return
		}
		refs := []model.RecordRef{actionRef(a.ID, a.RawRef)}
		if a.Detail != "" {
			fs.add(RuleFirmware, statement(code+"_detail", "who", who(a), "when", actionWhen(a.At, a.TimeQuality), "detail", a.Detail), refs, 1)
		} else {
			fs.add(RuleFirmware, statement(code, "who", who(a), "when", actionWhen(a.At, a.TimeQuality)), refs, 1)
		}
	}
	mark(disabled, "fw.disabled")
	mark(failed, "fw.failed")
	if sparing != nil {
		fs.add(RuleRepairGone, statement("rep.sparing", "when", actionWhen(sparing.At, sparing.TimeQuality)), []model.RecordRef{actionRef(sparing.ID, sparing.RawRef)}, 1)
	}
	j.firmwareMarks(fs, who)
}

// wider reports whether a mode covers more than one cell: the faults
// R-ROW-03 is about.
func wider(m model.FaultMode) bool {
	switch m {
	case model.ModeRow, model.ModeMultiRow, model.ModeColumn, model.ModeBank, model.ModeDevice, model.ModeDIMM:
		return true
	}
	return false
}

// area reports whether a mode covers a bank or more.
func area(m model.FaultMode) bool {
	return m == model.ModeBank || m == model.ModeDevice || m == model.ModeDIMM
}

// growing is R-ROW-03: a fault wider than a cell whose errors are rising or
// came in a recent burst or whose places are spreading, and any fault that
// covers a bank or more.
func (j *judge) growing(fs *findings) {
	cfg, evs := j.cfg, j.in.events
	for _, f := range fs.d.Faults {
		if !wider(f.Mode) || f.ErrorCount < cfg.RowMinErrors {
			continue
		}
		fired := false
		if f.Trend.Rising(cfg) {
			var recent []int32
			for _, i := range f.members {
				if e := &evs[i]; !e.relative && e.at > fs.d.clk.recentFrom && e.at <= fs.d.clk.asOf {
					recent = append(recent, i)
				}
			}
			refs, total := j.eventRefs(recent)
			if f.Trend.Direction == TrendNew {
				fs.add(RuleRow, statement("row.new", "mode", modeArg(f), "where", j.where(f), "recent", f.Trend.Recent,
					"days", cfg.RecentDays, "base", cfg.BaselineDays), refs, total)
			} else {
				fs.add(RuleRow, statement("row.rising", "mode", modeArg(f), "where", j.where(f), "recent", f.Trend.Recent,
					"days", cfg.RecentDays, "base", cfg.BaselineDays, "ratio", fmt.Sprintf("%.1f", f.Trend.Ratio)), refs, total)
			}
			fired = true
		}
		if f.Spread.Spreading {
			refs, total := j.eventRefs(f.members)
			switch f.Mode {
			case model.ModeRow, model.ModeMultiRow, model.ModeColumn:
				if f.Grade == model.GradeA {
					fs.add(RuleRow, statement("row.spreading_line", "mode", modeArg(f), "where", j.where(f), "new", f.Spread.NewCells,
						"days", cfg.SpreadDays, "columns", f.Shape.Columns, "rows", f.Shape.Rows), refs, total)
					break
				}
				fallthrough
			default:
				fs.add(RuleRow, statement("row.spreading_area", "mode", modeArg(f), "where", j.where(f), "new", f.Spread.NewCells,
					"days", cfg.SpreadDays, "cells", f.Spread.Cells), refs, total)
			}
			fired = true
		}
		// A burst counts while it is recent. One that lies further back is
		// said with the errors of the DIMM and raises no finding: on a
		// fleet log, servers whose fault had a burst weeks before failed no
		// more often than servers without one (docs/evaluation.md).
		if f.Storm.Recent {
			var recent []int32
			for _, i := range f.members {
				if e := &evs[i]; !e.relative && e.at > fs.d.clk.recentFrom && e.at <= fs.d.clk.asOf {
					recent = append(recent, i)
				}
			}
			refs, total := j.eventRefs(recent)
			fs.add(RuleRow, statement("row.storm", "mode", modeArg(f), "where", j.where(f), "peak", f.Storm.RecentPeak,
				"minutes", cfg.StormMinutes, "when", stampText(f.Storm.RecentAt.UnixMicro())), refs, total)
			fired = true
		}
		// A fault that covers a bank or more does not have to grow. No row
		// repair reaches it, and on a fleet log servers with such a fault
		// got a failure ticket as often without growth as the servers this
		// rule names for a growing fault (docs/evaluation.md).
		if !fired && area(f.Mode) {
			refs, total := j.eventRefs(f.members)
			fs.add(RuleRow, statement("row.area", "mode", modeArg(f), "where", j.where(f), "n", f.ErrorCount,
				"cells", f.Shape.Cells, "from", faultDay(f, f.FirstSeen)), refs, total)
			fired = true
		}
		if !fired {
			continue
		}
		if f.Lines.Multi {
			fs.add(RuleRow, statement("row.multi_dq", "where", j.where(f), "lines", f.Lines.Lines), nil, 0)
		}
		fs.covered[f] = true
		fs.note(RuleRow, f)
	}
}

// offline is R-OFF-02: pages keep being taken offline.
func (j *judge) offline(fs *findings) {
	o, cfg := fs.d.Features.Offline, j.cfg
	if fs.d.clk.zero() {
		return // no dated record: "recently" has no meaning
	}
	if o.Pages < cfg.OfflineMinPages || o.Days < cfg.OfflineMinDays || o.LastNew.IsZero() ||
		fs.d.clk.asOf-o.LastNew.UnixMicro() > days(cfg.OfflineRecentDays) {
		return
	}
	var refs []model.RecordRef
	total := 0
	for _, a := range fs.d.actions {
		if a.Kind == model.ActionPageOffline && a.Result != model.ResultFailed {
			total++
			if len(refs) < cfg.MaxEvidenceRecords {
				refs = append(refs, actionRef(a.ID, a.RawRef))
			}
		}
	}
	fs.add(RuleOffline, statement("off.growing", "pages", o.Pages, "days", o.Days, "when", dateText(o.LastNew.UnixMicro())), refs, total)
}

// repaired is R-REP-02 and R-QUIET-01: what happened at the place of a
// repair or a page offline afterwards.
func (j *judge) repaired(fs *findings) {
	cfg := j.cfg
	// Repairs that name the module only, with the faults they stand for.
	type moduleRepair struct {
		m      *Mitigation
		before int64
		after  int64
	}
	var modules []*moduleRepair
	byAction := map[int64]*moduleRepair{}
	clockChecked, kindChecked := false, false
	repairChecks := func(m *Mitigation, after []int32) {
		if m.Kind == model.ActionPPR && !kindChecked {
			kindChecked = true
			fs.checks = append(fs.checks, model.Check{Effect: model.EffectOpen, Statement: statement("check.ppr_kind")})
		}
		// A repair dated by a BMC whose clock was not compared, and
		// errors of the host less than 15 hours later.
		if m.BMCClock && !clockChecked && len(after) > 0 {
			first := j.in.events[after[0]].at
			for _, i := range after {
				first = min(first, j.in.events[i].at)
			}
			if first-m.At.UnixMicro() < 15*3600*1_000_000 {
				clockChecked = true
				fs.checks = append(fs.checks, model.Check{Effect: model.EffectOpen, Statement: statement("check.bmc_clock")})
			}
		}
	}
	for _, f := range fs.d.Faults {
		var last *Mitigation
		for i := range f.Mitigations {
			m := &f.Mitigations[i]
			if m.Result == model.ResultFailed {
				continue
			}
			if m.ModuleOnly {
				mr := byAction[m.ActionID]
				if mr == nil {
					mr = &moduleRepair{m: m}
					byAction[m.ActionID] = mr
					modules = append(modules, mr)
				}
				mr.before += int64(f.ErrorCount) - m.FaultErrorsAfter
				mr.after += m.FaultErrorsAfter
				if m.ErrorsAfter > 0 && repairKind(m.Kind) {
					refs, total := j.eventRefs(m.after)
					refs = append([]model.RecordRef{actionRef(m.ActionID, m.RawRef)}, refs...)
					if f.Grade == model.GradeC || j.where(f) == "" {
						fs.add(RuleRecurred, statement("rep.recurred_dimm_c", "n", m.ErrorsAfter, "kind", kindArg(m.Kind),
							"when", dateText(m.At.UnixMicro())), refs, total+1)
					} else {
						fs.add(RuleRecurred, statement("rep.recurred_dimm", "n", m.ErrorsAfter, "where", j.where(f), "kind", kindArg(m.Kind),
							"when", dateText(m.At.UnixMicro())), refs, total+1)
					}
					fs.covered[f] = true
					fs.note(RuleRecurred, f)
					repairChecks(m, m.after)
				}
				continue
			}
			if last == nil || !m.At.Before(last.At) {
				last = m
			}
			if !repairKind(m.Kind) || m.ErrorsAfter < 1 {
				continue
			}
			refs, total := j.eventRefs(m.after)
			refs = append([]model.RecordRef{actionRef(m.ActionID, m.RawRef)}, refs...)
			place := m.Where
			if place == "" {
				place = j.where(f)
			}
			repairChecks(m, m.after)
			fs.add(RuleRecurred, statement("rep.recurred", "n", m.ErrorsAfter, "where", place, "kind", kindArg(m.Kind),
				"when", dateText(m.At.UnixMicro())), refs, total+1)
			fs.covered[f] = true
			fs.note(RuleRecurred, f)
			// A soft PPR does not survive a power cycle.
			if m.Kind == model.ActionSPPR && len(m.after) > 0 {
				first := j.in.events[m.after[0]].at
				for _, i := range m.after {
					first = min(first, j.in.events[i].at)
				}
				for _, b := range j.hosts[fs.d.Host].boots {
					if at := b.StartedAt.UnixMicro(); b.StartQuality != model.TimeRelative && at > m.At.UnixMicro() && at <= first {
						fs.checks = append(fs.checks, model.Check{Effect: model.EffectWeakens,
							Statement: statement("check.sppr_volatile", "when", dateText(at))})
						break
					}
				}
			}
		}
		if last == nil || !f.Recurring || last.FaultErrorsAfter != 0 || fs.d.clk.zero() {
			continue
		}
		since := fs.d.clk.asOf - last.At.UnixMicro()
		if since < days(cfg.QuietDays) {
			fs.quietShort = append(fs.quietShort, model.Check{Effect: model.EffectOpen, Statement: statement("check.quiet_short",
				"days", since/microPerDay, "kind", kindArg(last.Kind), "when", dateText(last.At.UnixMicro()), "need", cfg.QuietDays)})
			continue
		}
		refs, total := j.eventRefs(f.members)
		refs = append([]model.RecordRef{actionRef(last.ActionID, last.RawRef)}, refs...)
		fs.add(RuleQuiet, statement("quiet.after", "mode", modeArg(f), "where", j.where(f), "n", f.ErrorCount,
			"last", faultDay(f, f.LastSeen), "days", since/microPerDay, "kind", kindArg(last.Kind), "when", dateText(last.At.UnixMicro())), refs, total+1)
		fs.covered[f] = true
		fs.note(RuleQuiet, f)
	}
	// A repair that names the module only: quiet when no fault that had
	// errors before it had one since, for rule.quiet.days.
	for _, mr := range modules {
		m := mr.m
		if mr.after != 0 || !repairKind(m.Kind) || fs.d.clk.zero() || fs.has(RuleRecurred) {
			continue
		}
		since := fs.d.clk.asOf - m.At.UnixMicro()
		if since < days(cfg.QuietDays) {
			fs.quietShort = append(fs.quietShort, model.Check{Effect: model.EffectOpen, Statement: statement("check.quiet_short",
				"days", since/microPerDay, "kind", kindArg(m.Kind), "when", dateText(m.At.UnixMicro()), "need", cfg.QuietDays)})
			continue
		}
		fs.add(RuleQuiet, statement("quiet.after_dimm", "days", since/microPerDay, "kind", kindArg(m.Kind), "when", dateText(m.At.UnixMicro()),
			"n", mr.before), []model.RecordRef{actionRef(m.ActionID, m.RawRef)}, 1)
		for _, f := range fs.d.Faults {
			for i := range f.Mitigations {
				if f.Mitigations[i].ActionID == m.ActionID {
					fs.covered[f] = true
					fs.note(RuleQuiet, f)
				}
			}
		}
	}
}

// maxSoftStatements caps the statements R-SOFT-01 makes about single
// faults. Further faults are summed up in one statement.
const maxSoftStatements = 4

// soft is R-SOFT-01: corrected errors that no other rule spoke about, when
// nothing points at a growing fault.
func (j *judge) soft(fs *findings) {
	for _, e := range fs.evidence {
		if r, _ := RuleByID(e.Rule); r.Grade == model.VerdictReplaceNow || r.Grade == model.VerdictConsiderReplacement {
			return
		}
	}
	cfg, d := j.cfg, fs.d
	var strays []int32
	var strayErrors int64
	strayPlaces := 0
	var from, to int64
	stated := 0
	var moreFaults int
	var moreErrors int64
	for _, f := range d.Faults {
		if f.CE == 0 || fs.covered[f] || f.Mode == model.ModeSystemIssue {
			continue
		}
		first := faultDay(f, f.FirstSeen)
		var st model.Statement
		switch {
		case f.Mode == model.ModeSingleBit && !f.Recurring:
			strays = append(strays, f.members...)
			strayErrors += int64(f.ErrorCount)
			strayPlaces++
			// Stray errors without a date take no part in the period.
			if f.Undated != int64(f.ErrorCount) {
				if at := f.FirstSeen.UnixMicro(); from == 0 || at < from {
					from = at
				}
				to = max(to, f.LastSeen.UnixMicro())
			}
			fs.note(RuleSoft, f)
			continue
		case f.Mode == model.ModeUndetermined:
			fs.unlocated = true
			if f.Trend.Direction == TrendRising {
				st = statement("soft.unlocated_rising", "n", f.ErrorCount, "recent", f.Trend.Recent, "days", cfg.RecentDays,
					"ratio", fmt.Sprintf("%.1f", f.Trend.Ratio))
			} else {
				st = statement("soft.unlocated", "n", f.ErrorCount)
			}
		case f.Mode == model.ModeSingleBit:
			st = statement("soft.cell", "where", j.where(f), "n", f.ErrorCount, "from", first, "recent", f.Trend.Recent, "days", cfg.RecentDays)
		case f.ErrorCount < cfg.RowMinErrors:
			st = statement("soft.few", "mode", modeArg(f), "where", j.where(f), "n", f.ErrorCount, "from", first, "min", cfg.RowMinErrors)
		default:
			st = statement("soft.fault", "mode", modeArg(f), "where", j.where(f), "n", f.ErrorCount, "from", first, "days", cfg.SpreadDays)
		}
		fs.note(RuleSoft, f)
		if stated >= maxSoftStatements {
			moreFaults++
			moreErrors += int64(f.ErrorCount)
			continue
		}
		stated++
		refs, total := j.eventRefs(f.members)
		fs.add(RuleSoft, st, refs, total)
	}
	if moreFaults > 0 {
		fs.add(RuleSoft, statement("soft.more", "faults", moreFaults, "n", moreErrors), nil, 0)
	}
	// A burst does not change the grade of errors that stay in one cell,
	// but it is said: a storm of corrected errors costs the host time.
	if st := d.Features.Storm; st.Storm && (stated > 0 || strayPlaces > 0) {
		fs.add(RuleSoft, statement("soft.storm", "peak", st.Peak, "minutes", cfg.StormMinutes, "when", stampText(st.At.UnixMicro())), nil, 0)
	}
	if strayPlaces > 0 {
		refs, total := j.eventRefs(strays)
		switch {
		case from == 0:
			fs.add(RuleSoft, statement("soft.stray_undated", "n", strayErrors, "places", strayPlaces), refs, total)
		case dateText(from) == dateText(to):
			fs.add(RuleSoft, statement("soft.stray_once", "n", strayErrors, "places", strayPlaces, "from", dateText(from)), refs, total)
		default:
			fs.add(RuleSoft, statement("soft.stray", "n", strayErrors, "places", strayPlaces, "from", dateText(from), "to", dateText(to)), refs, total)
		}
	}
}

// looseOf sums the unconfined errors of a DIMM over all slots, with the
// period they span.
func (j *judge) looseOf(d *DIMMResult) (n, first, last int64) {
	for _, u := range j.sys.usesOf[d] {
		n += u.loose
		if u.loose > 0 && u.first != 0 && (first == 0 || u.first < first) {
			first = u.first
		}
		last = max(last, u.last)
	}
	return n, first, last
}

// check runs the tests of a finding against other explanations.
func (j *judge) check(fs *findings, grade model.VerdictGrade) {
	d, cfg, sys := fs.d, j.cfg, j.sys
	add := func(effect string, st model.Statement) {
		fs.checks = append(fs.checks, model.Check{Statement: st, Effect: effect})
	}
	errors := d.Features.CE + d.Features.UE
	k := hostSlot{d.Host, d.Slot}
	need := int64(cfg.SystemMinErrors)

	// Records tied to another slot that carry a name of this one.
	for _, c := range sys.named[k] {
		add(model.EffectOpen, statement("check.named_by_other", "n", c.records, "slot", c.slot, "name", c.name))
	}
	if errors == 0 {
		return
	}

	// A finding that blames the DIMM is tested against a cause outside
	// it. A finding that asks for nothing only lists what speaks against it.
	blames := grade == model.VerdictReplaceNow || grade == model.VerdictConsiderReplacement

	// The other DIMMs of the channel.
	if d.Present && !fs.has(RuleChannel) {
		switch others, known := sys.neighbours(k); {
		case !known:
			if blames {
				add(model.EffectOpen, statement("check.channel_unknown"))
			}
		case len(others) == 0:
			if blames {
				add(model.EffectOpen, statement("check.channel_alone"))
			}
		default:
			var clean []string
			_, first, last := j.looseOf(d)
			for _, o := range others {
				a := sys.activity(hostSlot{d.Host, o})
				switch {
				case a.all == 0:
					clean = append(clean, o)
				case a.loose == 0:
					if blames {
						add(model.EffectSupports, statement("check.neighbors_confined", "slot", o, "n", a.all))
					}
				case a.loose < need:
					if blames {
						add(model.EffectOpen, statement("check.neighbors_few", "slot", o, "n", a.loose, "min", need))
					}
				case first == 0 || (a.first <= last+days(cfg.SystemOverlapDays) && first <= a.last+days(cfg.SystemOverlapDays)):
					add(model.EffectWeakens, statement("check.neighbors_active", "slot", o, "n", a.loose))
				}
			}
			if blames && len(clean) > 0 && len(clean) == len(others) {
				add(model.EffectSupports, statement("check.neighbors_clean", "n", len(clean), "slots", clean))
			}
		}
	}

	// Did the errors follow the DIMM to another slot?
	if !fs.has(RuleSlot) && !fs.has(RuleReseat) {
		stays := sys.spansOf[d.UID]
		moved := false
		if len(stays) >= 2 {
			prev, cur := stays[len(stays)-2], stays[len(stays)-1]
			before, after := sys.errorsOf(d, 0, cur.first, hostSlot{}), sys.errorsOf(d, prev.last, 0, hostSlot{})
			if before > 0 && after > 0 {
				add(model.EffectSupports, statement("check.followed", "from", prev.slot, "to", cur.slot, "before", before, "after", after))
				moved = true
			}
		}
		reseated := false
		for _, a := range d.actions {
			reseated = reseated || a.Kind == model.ActionReseat
		}
		if !moved && !reseated && blames {
			add(model.EffectOpen, statement("check.no_cross_test"))
		}
	}

	// The other DIMMs of the host: unconfined errors on several of them
	// in the same period point at something they share.
	{
		n, first, last := j.looseOf(d)
		var others []string
		quiet := 0
		for _, o := range j.sys.dimms {
			if o == d || o.Host != d.Host || !o.Present {
				continue
			}
			m, f, l := j.looseOf(o)
			switch {
			case m == 0:
				quiet++
			case n >= need && m >= need && first != 0 && f != 0 && f <= last+days(cfg.SystemOverlapDays) && first <= l+days(cfg.SystemOverlapDays):
				others = append(others, o.Slot)
			}
		}
		switch {
		case len(others)+1 >= cfg.HostWideMinDIMMs:
			sort.Strings(others)
			add(model.EffectWeakens, statement("check.host_wide", "n", len(others), "slots", others))
		case blames && quiet > 0 && len(others) == 0:
			total := 0
			for _, o := range j.sys.dimms {
				if o != d && o.Host == d.Host && o.Present {
					total++
				}
			}
			if quiet == total {
				add(model.EffectSupports, statement("check.host_clean", "n", quiet))
			}
		}
	}

	// Did the errors start after the BIOS or the kernel changed?
	if !d.Features.First.IsZero() {
		first := d.Features.First.UnixMicro()
		boots := j.hosts[d.Host].boots
		for i := len(boots) - 1; i >= 1; i-- {
			b, p := boots[i], boots[i-1]
			at := b.StartedAt.UnixMicro()
			if b.StartQuality == model.TimeRelative || at > first {
				continue
			}
			covered := float64(at-d.clk.from) / float64(microPerDay)
			var what, old, new string
			switch {
			case b.BIOS != "" && p.BIOS != "" && b.BIOS != p.BIOS:
				what, old, new = "@word.bios-version", p.BIOS, b.BIOS
			case b.Kernel != "" && p.Kernel != "" && b.Kernel != p.Kernel:
				what, old, new = "@word.kernel", p.Kernel, b.Kernel
			}
			// Only the boot the errors started in counts, and only with
			// enough history before it to say there were none.
			if what != "" && covered >= cfg.MinBaselineDays && (i == len(boots)-1 || boots[i+1].StartedAt.UnixMicro() > first) {
				add(model.EffectWeakens, statement("check.after_change", "what", what, "when", dateText(at), "old", old, "new", new,
					"days", (first-at)/microPerDay, "covered", int64(covered)))
			}
			break
		}
	}

	// Errors at times when no inventory shows the DIMM in the slot.
	if d.Features.Assumed*2 >= errors && d.Features.Assumed > 0 && blames {
		add(model.EffectOpen, statement("check.assumed", "n", d.Features.Assumed))
	}

	// What the mode of the largest fault rests on.
	if len(d.Faults) > 0 {
		switch d.Faults[0].EstimatedBy {
		case EstimateAddressOnly:
			if d.Faults[0].Mode == model.ModeRow {
				add(model.EffectWeakens, statement("check.estimated"))
			}
		case EstimateDeviceUnknown:
			add(model.EffectOpen, statement("check.device_unknown"))
		}
	}

	// A cause outside the DIMM does not confine errors to one row.
	if grade == model.VerdictHoldReplacement && (fs.has(RuleChannel) || fs.has(RuleSlot) || fs.has(RuleReseat)) {
		for _, f := range d.Faults {
			if f.Confined && f.ErrorCount >= cfg.RowMinErrors {
				add(model.EffectWeakens, statement("check.confined", "where", j.where(f), "mode", modeArg(f)))
				break
			}
		}
	}

	// Silence after a repair.
	fs.checks = append(fs.checks, fs.quietShort...)
	if fs.has(RuleQuiet) || fs.has(RuleReseat) {
		if _, ok := d.Features.Scrub.PatrolShare(); !ok || d.Features.Scrub.Patrol == 0 {
			if b := j.hosts[d.Host].bios; b.patrolOff {
				// The BIOS says why the scrubber found nothing.
				add(model.EffectOpen, statement("check.patrol_off", "attribute", b.patrolAttr, "value", b.patrolValue))
			} else {
				add(model.EffectOpen, statement("check.no_patrol"))
			}
		}
	}

	// Repairs that name no place.
	unlocated := 0
	for _, a := range d.actions {
		if repairKind(a.Kind) && a.Result != model.ResultFailed && actionWhere(a) == "" && a.Kind != model.ActionSparing {
			unlocated++
		}
	}
	if unlocated > 0 {
		add(model.EffectOpen, statement("check.repair_unlocated", "n", unlocated))
	}
}

// errorSourceKinds are the adapters that deliver error records.
var errorSourceKinds = map[string]bool{"rasdaemon": true, "klog": true, "edac-sysfs": true, "synth": true}

// missing lists the data the finding did not have.
func (j *judge) missing(fs *findings, grade model.VerdictGrade) []model.Statement {
	d, hf := fs.d, j.hosts[fs.d.Host]
	var out []model.Statement
	add := func(code string, kv ...any) { out = append(out, statement(code, kv...)) }

	// Sources that could not be read, or only in part.
	for _, src := range hf.sources {
		switch src.Status {
		case model.SourcePartial, model.SourceUnreadable, model.SourceError:
		case model.SourceAbsent:
			if !errorSourceKinds[src.Kind] && src.Kind != "smbios" {
				continue
			}
		default:
			continue
		}
		// The detail of a source that is not there only repeats that.
		if src.Detail != "" && src.Status != model.SourceAbsent {
			add("missing.source_detail", "kind", src.Kind, "status", "@status."+string(src.Status), "detail", src.Detail)
		} else {
			add("missing.source", "kind", src.Kind, "status", "@status."+string(src.Status))
		}
	}
	if !hf.errorSource {
		add("missing.no_errors")
	}
	if hf.virtual != "" {
		out = append(out, VirtualStatement(hf.virtual))
	}
	switch {
	case d.DIMM != nil && d.DIMM.Identity == model.IdentitySerial:
	case !hf.inventory:
		add("missing.inventory")
	case hf.smbiosSlot[d.Slot]:
		// SMBIOS describes the slot and gives no usable serial number.
		add("missing.serial")
	default:
		// The slot is known through EDAC or an error record only.
		add("missing.unmapped")
	}
	if hf.unplaced > 0 {
		add("missing.unplaced", "n", hf.unplaced)
	}
	if n := hf.ambiguous[d.Slot]; n > 0 && d.Slot != "" {
		add("missing.ambiguous", "n", n)
	}

	ft := d.Features
	// What the BIOS settings say about the log of a BMC. Where the BMC is
	// the only witness, a DIMM without records rests on it too.
	if b := hf.bios; hf.bmcSource && (ft.CE+ft.UE > 0 || !hf.hostSource) {
		switch {
		case b.ceOff:
			add("missing.bmc_ce_off", "attribute", b.ceAttr, "value", b.ceValue)
		case b.ceThreshold:
			add("missing.bmc_threshold", "attribute", b.thresholdAttr, "value", b.thresholdValue)
		}
	}
	if ft.CE+ft.UE == 0 {
		return out
	}
	if ft.GradeB > 0 {
		add("missing.rowcol", "n", ft.GradeB)
	}
	if ft.GradeC > 0 {
		add("missing.location", "n", ft.GradeC)
	}
	if ft.GradeA > 0 {
		device := false
		for _, f := range d.Faults {
			device = device || f.Shape.Devices > 0
		}
		if !device {
			add("missing.device")
		}
		if !ft.Lines.Known {
			add("missing.bits")
		}
	}
	if _, ok := ft.Scrub.PatrolShare(); !ok {
		add("missing.scrub")
	}
	if b := hf.bios; b.patrolOff {
		add("missing.patrol_off", "attribute", b.patrolAttr, "value", b.patrolValue)
	}
	if ft.Trend.Direction == TrendUnknown && !d.clk.zero() {
		add("missing.history", "days", int64(ft.Trend.PriorDays), "need", j.cfg.MinBaselineDays)
	}
	if ft.Undated > 0 {
		add("missing.dates", "n", ft.Undated)
	}
	// What would say more about the DIMM, named where a finding asks for
	// more than watching: the ECS counts of a DDR5 DIMM, unless the ledger
	// has readings of them, and a temperature history, which no input
	// stores yet.
	if grade != model.VerdictOK && grade != model.VerdictObserve {
		if d.DIMM != nil && strings.EqualFold(d.DIMM.MemoryType, "DDR5") && !j.hasECS(d.UID) {
			add("missing.ecs")
		}
		add("missing.temperature")
	}
	return out
}

// VirtualStatement says that a host is a virtual machine and what that
// means for its records. virtual names the hypervisor, or is the word for
// one that is known to be there and not known by name.
func VirtualStatement(virtual string) model.Statement {
	if virtual == virt.Unnamed {
		return statement("missing.virtual", "what", "@word.hypervisor")
	}
	return statement("missing.virtual", "what", virtual)
}

func (c clock) zero() bool { return c.asOf == 0 }

// next names the step that follows from the finding.
func (j *judge) next(fs *findings, grade model.VerdictGrade) model.Statement {
	d := fs.d
	if grade == model.VerdictOK {
		return model.Statement{}
	}
	if !d.Present {
		return statement("next.removed")
	}
	switch grade {
	case model.VerdictHoldReplacement:
		switch {
		case fs.has(RuleFWTrain) && !fs.has(RuleLoopSpread) && !fs.has(RuleMapping) && !fs.has(RuleSlot) && !fs.has(RuleChannel):
			return statement("next.fw_train")
		case fs.has(RuleLoopSpread):
			return statement("next.loop_spread")
		case fs.has(RuleMapping):
			return statement("next.mapping")
		case fs.has(RuleSlot):
			slot := d.Slot
			for _, e := range fs.evidence {
				if s, ok := e.Args["slot"].(string); ok && e.Rule == RuleSlot {
					slot = s
					break
				}
			}
			return statement("next.slot", "slot", slot)
		case fs.has(RuleChannel):
			return statement("next.channel")
		}
		return statement("next.contact")
	case model.VerdictReplaceNow:
		switch {
		case fs.has(RuleLoopPlace):
			return statement("next.loop_place")
		case fs.has(RuleLoopDIMM):
			return statement("next.loop_dimm")
		}
		return statement("next.replace")
	case model.VerdictConsiderReplacement:
		if fs.has(RuleFWPredict) && !fs.has(RuleRow) && !fs.has(RuleRecurred) && !fs.has(RuleScrub) && !fs.has(RuleOffline) {
			return statement("next.fw_pfa")
		}
		return statement("next.plan")
	}
	if fs.unlocated {
		return statement("next.locate")
	}
	return statement("next.observe")
}

// baseConfidence is where the confidence of a finding starts, by the rule
// that sets the grade. A recorded uncorrected error is a fact; a trend is a
// reading of counts.
var baseConfidence = map[string]int{
	RuleUE: 90, RuleFirmware: 90, RuleRepairGone: 85,
	RuleScrub: 80, RuleRecurred: 80, RuleRow: 75, RuleOffline: 70,
	RuleQuiet: 70, RuleSoft: 65,
	RuleSlot: 80, RuleMapping: 75, RuleChannel: 65, RuleReseat: 60,
	RuleHostUE: 85, RuleHostCE: 70,
	RuleLoopPlace: 90, RuleLoopDIMM: 80, RuleLoopSpread: 70,
	RuleFWPredict: 75, RuleFWTrain: 65,
}

// confidence rates the finding from 0 to 100 and lists the steps.
func (j *judge) confidence(fs *findings, rules []string, missing []model.Statement) (int, []model.Factor) {
	d, hf := fs.d, j.hosts[fs.d.Host]
	var factors []model.Factor
	total := 0
	step := func(delta int, st model.Statement) {
		if delta == 0 {
			return
		}
		total += delta
		factors = append(factors, model.Factor{Statement: st, Delta: delta})
	}
	if len(rules) == 0 {
		step(85, statement("conf.ok"))
		if !hf.errorSource {
			step(-45, statement("conf.no_error_source"))
		}
	} else {
		lead := rules[0]
		step(baseConfidence[lead], statement("conf.rule", "rule", lead))
		if b := fs.basis[lead]; b != nil {
			switch {
			case b.gradeC:
				step(-15, statement("conf.grade_c"))
			case b.gradeB && b.estimated:
				step(-15, statement("conf.estimated"))
			case b.gradeB:
				step(-10, statement("conf.grade_b"))
			case b.estimated:
				step(-10, statement("conf.estimated"))
			}
		}
		if d.DIMM == nil || d.DIMM.Identity != model.IdentitySerial {
			step(-10, statement("conf.no_serial"))
		}
		if errors := d.Features.CE + d.Features.UE; errors > 0 && d.Features.Assumed*2 >= errors {
			step(-5, statement("conf.assumed"))
		}
		var supports, weakens, open int
		for _, c := range fs.checks {
			switch c.Effect {
			case model.EffectSupports:
				supports++
			case model.EffectWeakens:
				weakens++
			case model.EffectOpen:
				open++
			}
		}
		if supports > 0 {
			step(min(5*supports, 10), statement("conf.supports", "n", supports))
		}
		if weakens > 0 {
			step(-min(10*weakens, 30), statement("conf.weakens", "n", weakens))
		}
		if open > 0 {
			step(-min(2*open, 6), statement("conf.open", "n", open))
		}
	}
	if fs.fwWording {
		step(-5, statement("conf.fw_wording"))
	}
	unread := 0
	for _, m := range missing {
		if m.Code == "missing.source" || m.Code == "missing.source_detail" {
			unread++
		}
	}
	if unread > 0 {
		step(-min(5*unread, 15), statement("conf.sources", "n", unread))
	}
	return min(max(total, 5), 95), factors
}

// verdict puts the finding of one DIMM together.
func (j *judge) verdict(d *DIMMResult) *model.Verdict {
	fs := &findings{d: d, basis: map[string]*basis{}, covered: map[*Fault]bool{}}
	// The system rules ran before; their evidence comes first, then that
	// of the boots.
	fs.evidence = append(fs.evidence, j.sys.fired[d]...)
	if j.loop != nil {
		fs.evidence = append(fs.evidence, j.loop.fired[d]...)
	}
	for i := range fs.evidence {
		if refs := j.extra[d][fs.evidence[i].Rule]; len(refs) > 0 {
			fs.evidence[i].Records = append(refs, fs.evidence[i].Records...)
			fs.evidence[i].Total += len(refs)
		}
	}
	for _, f := range d.Faults {
		switch {
		case f.Mode == model.ModeSystemIssue:
			fs.covered[f] = true
			for _, r := range []string{RuleChannel, RuleSlot, RuleReseat} {
				if fs.has(r) {
					fs.note(r, f)
				}
			}
		case fs.has(RuleReseat):
			// Every error of the DIMM is from before the reseat; the rule
			// spoke about all of them.
			fs.covered[f] = true
			fs.note(RuleReseat, f)
		}
	}
	j.uncorrected(fs)
	j.firmware(fs)
	j.growing(fs)
	j.offline(fs)
	j.repaired(fs)
	j.soft(fs)
	j.thresholds(fs)
	j.faultBoots(fs)

	sort.SliceStable(fs.evidence, func(a, b int) bool {
		return rulePosition(fs.evidence[a].Rule) < rulePosition(fs.evidence[b].Rule)
	})
	v := &model.Verdict{Host: d.Host, DIMMUID: d.UID, Slot: d.Slot, Grade: model.VerdictOK, Label: model.VerdictLabel,
		Evidence: fs.evidence, EngineVersion: Version, ComputedAt: j.now}
	for _, e := range fs.evidence {
		if len(v.Rules) == 0 || v.Rules[len(v.Rules)-1] != e.Rule {
			v.Rules = append(v.Rules, e.Rule)
		}
		if r, _ := RuleByID(e.Rule); worse(r.Grade, v.Grade) {
			v.Grade = r.Grade
		}
	}
	j.check(fs, v.Grade)
	fs.fwWording = j.fwChecks(fs, v.Grade)
	if j.loop != nil {
		fs.checks = append(fs.checks, j.loop.checks[d]...)
	}
	v.CounterEvidence = fs.checks
	v.Missing = append(append(j.missing(fs, v.Grade), j.loopMissing(fs)...), j.fwMissing(fs)...)
	v.NextAction = j.next(fs, v.Grade)
	v.Confidence, v.ConfidenceFactors = j.confidence(fs, v.Rules, v.Missing)
	return v
}
