package engine

import (
	"sort"
	"strings"

	"github.com/agenticode/my/internal/model"
)

// A finding is about a DIMM because a decision is about a DIMM. Some
// records name none: machine checks of a host whose memory controller has
// no EDAC driver, firmware records without a location, pages the kernel
// took offline by address. They are in no fault and in the finding of no
// DIMM. Left out of every finding, they would leave a host with
// uncorrected errors at "ok".
//
// The host finding speaks about them. It is one finding per host, next to
// those of its DIMMs and GPUs, and counts for the grade of the host:
//
//	R-HOST-01  hold replacement  uncorrected errors that name no DIMM
//	R-HOST-02  observe           corrected errors or page offlines that name no DIMM
//
// Hold replacement is the grade for "check before a DIMM is pulled", and
// that is what such records ask for: they prove that memory failed and do
// not say which module.

// askedMark is what the kernel log adapter writes into the detail of a page
// offline that a program asked for (madvise, the hwpoison injector).
const askedMark = "asked for through"

// loose builds the finding about the errors and page offlines of a host
// that the records tie to no DIMM. It returns nil when there are none.
func (j *judge) loose(h *HostResult, clk clock) *model.Verdict {
	evs := j.in.events
	var ue, ce []int32
	var noPlace int64
	ambiguous := map[string]int64{}
	for i := range evs {
		e := &evs[i]
		if e.dimm >= 0 || j.in.hostNames[e.host] != h.Host || (!e.relative && e.at > clk.asOf) {
			continue
		}
		if e.sev == sevCE {
			ce = append(ce, int32(i))
		} else {
			ue = append(ue, int32(i))
		}
		switch {
		case e.slot < 0:
			noPlace += int64(e.errors)
		case e.link == linkAmbiguous:
			ambiguous[j.in.slots[e.slot].name] += int64(e.errors)
		}
	}
	var offlines []*model.Action
	for i := range j.in.Actions {
		if a := &j.in.Actions[i]; a.Host == h.Host && a.Kind == model.ActionPageOffline && a.DIMMUID == "" {
			offlines = append(offlines, a)
		}
	}
	off := (&measurer{in: j.in, cfg: j.cfg, clk: clk}).offline(offlines)
	h.LoosePages = off.Pages
	// R-LOOP-03 is set on every DIMM involved; the host finding carries
	// the summary.
	var spread []model.Statement
	if r := h.Recurrence; r != nil {
		spread = r.spread
	}
	// Firmware records about memory that name no DIMM: a mirror that lost
	// its redundancy, a sparing mode of the system.
	unnamedN, unnamedKinds := j.unnamed(h.Host)
	if len(ue) == 0 && len(ce) == 0 && off.Pages == 0 && len(spread) == 0 && unnamedN == 0 {
		return nil
	}

	var evidence []model.Evidence
	add := func(rule string, st model.Statement, refs []model.RecordRef, total int) {
		evidence = append(evidence, model.Evidence{Rule: rule, Statement: st, Records: refs, Total: total})
	}
	var undated int64
	if len(ue) > 0 {
		var n int64
		last := &evs[ue[0]]
		for _, i := range ue {
			e := &evs[i]
			n += int64(e.errors)
			if !e.relative && (last.relative || e.at >= last.at) {
				last = e
			}
			if e.relative {
				undated += int64(e.errors)
			}
		}
		when := stampText(last.at)
		if last.relative {
			when = bootText(last.at)
		}
		refs, total := j.eventRefs(ue)
		if w := eventWhere(last); w != "" {
			add(RuleHostUE, statement("host.ue_located", "n", n, "when", when, "where", w), refs, total)
		} else {
			add(RuleHostUE, statement("host.ue", "n", n, "when", when), refs, total)
		}
		if st, refs, total, ok := j.stopped(ue); ok {
			add(RuleHostUE, st, refs, total)
		}
		// An address that had uncorrected errors in several boots.
		if r := h.Recurrence; r != nil && r.Window() >= 2 {
			for _, pb := range r.Loose {
				if pb.Level == PlaceAddress && pb.BootsWithUE >= j.cfg.LoopMinBoots {
					refs, total := j.eventRefs(pb.events)
					add(RuleHostUE, statement("host.loop_address", "where", pb.Place, "n", pb.BootsWithUE, "of", r.Window()), refs, total)
				}
			}
		}
	}
	if len(ce) > 0 {
		var n int64
		var first, last int64
		for _, i := range ce {
			e := &evs[i]
			n += int64(e.errors)
			if e.relative {
				undated += int64(e.errors)
				continue
			}
			if first == 0 || e.at < first {
				first = e.at
			}
			last = max(last, e.at)
		}
		refs, total := j.eventRefs(ce)
		switch {
		case first == 0:
			add(RuleHostCE, statement("host.ce_undated", "n", n), refs, total)
		case dateText(first) == dateText(last):
			add(RuleHostCE, statement("host.ce_day", "n", n, "from", dateText(first)), refs, total)
		default:
			add(RuleHostCE, statement("host.ce", "n", n, "from", dateText(first), "to", dateText(last)), refs, total)
		}
	}
	asked := map[uint64]bool{}
	if off.Pages > 0 {
		var refs []model.RecordRef
		total := 0
		for _, a := range offlines {
			if a.Result == model.ResultFailed || (a.At.UnixMicro() > clk.asOf && a.TimeQuality != model.TimeRelative) {
				continue
			}
			total++
			if len(refs) < j.cfg.MaxEvidenceRecords {
				refs = append(refs, actionRef(a.ID, a.RawRef))
			}
			if a.OsPhysicalAddress.Valid && strings.Contains(a.Detail, askedMark) {
				asked[a.OsPhysicalAddress.V>>12] = true
			}
		}
		if off.LastNew.IsZero() {
			add(RuleHostCE, statement("host.pages_undated", "pages", off.Pages), refs, total)
		} else {
			add(RuleHostCE, statement("host.pages", "pages", off.Pages, "when", dateText(off.LastNew.UnixMicro())), refs, total)
		}
	}

	if unnamedN > 0 {
		add(RuleHostCE, statement("host.fw_unnamed", "n", unnamedN, "kinds", strings.Join(unnamedKinds, ", ")), nil, unnamedN)
	}
	for _, st := range spread {
		add(RuleLoopSpread, st, nil, 0)
	}
	sort.SliceStable(evidence, func(a, b int) bool { return rulePosition(evidence[a].Rule) < rulePosition(evidence[b].Rule) })

	v := &model.Verdict{Host: h.Host, Grade: model.VerdictOK, Label: model.VerdictLabel, Evidence: evidence,
		EngineVersion: Version, ComputedAt: j.now}
	for _, e := range evidence {
		if len(v.Rules) == 0 || v.Rules[len(v.Rules)-1] != e.Rule {
			v.Rules = append(v.Rules, e.Rule)
		}
		if r, _ := RuleByID(e.Rule); worse(r.Grade, v.Grade) {
			v.Grade = r.Grade
		}
	}

	// Why the records name no DIMM, and what speaks against reading them
	// as failing hardware.
	check := func(effect string, st model.Statement) {
		v.CounterEvidence = append(v.CounterEvidence, model.Check{Effect: effect, Statement: st})
	}
	if noPlace > 0 {
		check(model.EffectOpen, statement("check.host_no_place", "n", noPlace))
	}
	for _, slot := range sortedKeys(ambiguous) {
		check(model.EffectOpen, statement("check.host_ambiguous", "n", ambiguous[slot], "slot", slot))
	}
	if n := len(asked); n > 0 {
		check(model.EffectWeakens, statement("check.host_asked", "n", n))
	}

	hf := j.hosts[h.Host]
	miss := func(code string, kv ...any) { v.Missing = append(v.Missing, statement(code, kv...)) }
	edac := false
	for _, src := range hf.sources {
		if src.Kind == "edac-sysfs" && (src.Status == model.SourceEmpty || src.Status == model.SourceAbsent) {
			edac = true
		}
		switch src.Status {
		case model.SourcePartial, model.SourceUnreadable, model.SourceError:
		case model.SourceAbsent:
			if !errorSourceKinds[src.Kind] && src.Kind != "smbios" {
				continue
			}
		default:
			continue
		}
		if src.Detail != "" && src.Status != model.SourceAbsent {
			miss("missing.source_detail", "kind", src.Kind, "status", "@status."+string(src.Status), "detail", src.Detail)
		} else {
			miss("missing.source", "kind", src.Kind, "status", "@status."+string(src.Status))
		}
	}
	switch {
	case hf.virtual != "":
		// A guest has no memory controller to have a driver for.
		v.Missing = append(v.Missing, VirtualStatement(hf.virtual))
	case edac:
		miss("missing.no_edac")
	}
	if undated > 0 {
		miss("missing.dates", "n", undated)
	}
	// Boots stopped and no record of them names a DIMM: what my preflight
	// found a reset loses on the host.
	if r := h.Recurrence; r != nil && r.Stops > 0 && r.Where == "none" {
		if lost := lostCaptures(j.in, h.Host); len(lost) > 0 {
			miss("missing.capture_lost", "checks", lost)
		}
	}

	switch hold := v.Grade == model.VerdictHoldReplacement; {
	case len(spread) > 0:
		v.NextAction = statement("next.loop_spread")
	case hold && hf.virtual != "":
		v.NextAction = statement("next.host_ue_vm")
	case hold:
		v.NextAction = statement("next.host_ue")
	case hf.virtual != "":
		v.NextAction = statement("next.host_ce_vm")
	default:
		v.NextAction = statement("next.host_ce")
	}

	lead := v.Rules[0]
	v.Confidence = baseConfidence[lead]
	v.ConfidenceFactors = []model.Factor{{Statement: statement("conf.rule", "rule", lead), Delta: v.Confidence}}
	step := func(delta int, st model.Statement) {
		v.Confidence += delta
		v.ConfidenceFactors = append(v.ConfidenceFactors, model.Factor{Statement: st, Delta: delta})
	}
	if n := len(asked); n > 0 && len(ue) == 0 && len(ce) == 0 && n >= off.Pages {
		// Every page was taken offline because a program asked for it.
		step(-30, statement("conf.weakens", "n", 1))
	}
	unread := 0
	for _, m := range v.Missing {
		if m.Code == "missing.source" || m.Code == "missing.source_detail" {
			unread++
		}
	}
	if unread > 0 {
		step(-min(5*unread, 15), statement("conf.sources", "n", unread))
	}
	v.Confidence = min(max(v.Confidence, 5), 95)
	return v
}

// sortedKeys returns the keys of m in order.
func sortedKeys(m map[string]int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
