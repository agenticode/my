package engine

import (
	"sort"
	"strings"
	"time"

	"github.com/agenticode/my/internal/fwevent"
	"github.com/agenticode/my/internal/model"
)

// What the BIOS and the BMC say about a module beyond its errors: a
// prediction of its failure, a failed training, a mirror that took over, a
// repair and its result. The rules read the actions made of firmware
// records, and the firmware records themselves for the vendor's message ID
// and for how far the mapping of the message was checked.

// fwIndex holds the firmware records of the input by the event or action
// they came with, and per DIMM.
type fwIndex struct {
	byAction map[int64]*model.FirmwareRecord
	byEvent  map[int64]*model.FirmwareRecord
	byDIMM   map[string][]*model.FirmwareRecord
	byHost   map[string][]*model.FirmwareRecord
}

func (j *judge) fw() *fwIndex {
	if j.fwi != nil {
		return j.fwi
	}
	x := &fwIndex{byAction: map[int64]*model.FirmwareRecord{}, byEvent: map[int64]*model.FirmwareRecord{},
		byDIMM: map[string][]*model.FirmwareRecord{}, byHost: map[string][]*model.FirmwareRecord{}}
	for i := range j.in.Firmware {
		f := &j.in.Firmware[i]
		if f.ActionID != 0 {
			x.byAction[f.ActionID] = f
		}
		if f.EventID != 0 {
			x.byEvent[f.EventID] = f
		}
		if f.DIMMUID != "" {
			x.byDIMM[f.DIMMUID] = append(x.byDIMM[f.DIMMUID], f)
		}
		x.byHost[f.Host] = append(x.byHost[f.Host], f)
	}
	j.fwi = x
	return x
}

// idArg is the vendor's message ID of a record as the statements write it:
// " (MEM0804)", or nothing.
func idArg(f *model.FirmwareRecord) string {
	if f == nil || f.VendorID == "" {
		return ""
	}
	return " (" + f.VendorID + ")"
}

// isMark reports whether an action is a mark firmware put on a module.
func isMark(k model.ActionKind) bool {
	switch k {
	case model.ActionDisable, model.ActionMarkFailed, model.ActionPredictedFailure, model.ActionTrainingFailure:
		return true
	}
	return false
}

// pprKind reports whether an action is a post package repair.
func pprKind(k model.ActionKind) bool {
	return k == model.ActionSPPR || k == model.ActionHPPR || k == model.ActionPPR
}

// firmwareMarks speaks about the marks and steps of firmware beyond the
// ones R-FW-01 and R-REP-01 read since the first versions: a prediction
// (R-FW-02), a failed training (R-FW-03, or R-FW-01 when it followed the
// module into a second slot), a mirror that took over (R-FW-01), a repair
// that waits for the next boot.
func (j *judge) firmwareMarks(fs *findings, who func(*model.Action) string) {
	x := j.fw()
	d := fs.d
	var pfa, mirror *model.Action
	var trains, pending []*model.Action
	for _, a := range d.actions {
		if a.WithdrawnBy != 0 {
			continue
		}
		switch {
		case a.Kind == model.ActionPredictedFailure:
			if pfa == nil {
				pfa = a
			}
		case a.Kind == model.ActionMirrorFailover && a.Result != model.ResultFailed:
			mirror = a
		case a.Kind == model.ActionTrainingFailure:
			trains = append(trains, a)
		case pprKind(a.Kind) && a.Result == model.ResultPending:
			pending = append(pending, a)
		}
	}
	if mirror != nil {
		fs.add(RuleFirmware, statement("fw.mirror", "when", actionWhen(mirror.At, mirror.TimeQuality), "id", idArg(x.byAction[mirror.ID])),
			[]model.RecordRef{actionRef(mirror.ID, mirror.RawRef)}, 1)
	}
	if pfa != nil {
		fs.add(RuleFWPredict, statement("fw.pfa", "who", who(pfa), "when", actionWhen(pfa.At, pfa.TimeQuality), "id", idArg(x.byAction[pfa.ID])),
			[]model.RecordRef{actionRef(pfa.ID, pfa.RawRef)}, 1)
		// Errors at one cell only: a repair of the row may be enough.
		cells, others := 0, 0
		for _, f := range d.Faults {
			switch {
			case f.Mode == model.ModeSingleBit && f.Grade == model.GradeA:
				cells++
			case f.Mode != model.ModeSystemIssue:
				others++
			}
		}
		if cells > 0 && others == 0 {
			fs.checks = append(fs.checks, model.Check{Effect: model.EffectWeakens, Statement: statement("check.pfa_cell")})
		}
	}
	if len(trains) > 0 {
		j.training(fs, trains)
	}
	for _, p := range pending {
		done := false
		for _, a := range d.actions {
			if pprKind(a.Kind) && a.Result != model.ResultPending && a.At.After(p.At) {
				done = true
			}
		}
		if !done {
			fs.checks = append(fs.checks, model.Check{Effect: model.EffectOpen, Statement: statement("check.ppr_pending",
				"kind", kindArg(p.Kind), "when", actionWhen(p.At, p.TimeQuality))})
		}
	}
}

// training speaks about failed trainings of the module: R-FW-01 when they
// followed it into a second slot, R-FW-03 when it has no error of its
// cells on record.
func (j *judge) training(fs *findings, trains []*model.Action) {
	d := fs.d
	x := j.fw()
	sort.SliceStable(trains, func(a, b int) bool { return trains[a].At.Before(trains[b].At) })
	latest := trains[len(trains)-1]
	var refs []model.RecordRef
	slots := map[string]bool{}
	var slotOrder []string
	boots := map[string]bool{}
	for _, a := range trains {
		refs = append(refs, actionRef(a.ID, a.RawRef))
		if a.Slot != "" && !slots[a.Slot] {
			slots[a.Slot] = true
			slotOrder = append(slotOrder, a.Slot)
		}
		key := a.BootID
		if key == "" {
			key = a.At.UTC().Format("2006-01-02")
		}
		boots[key] = true
	}
	if len(slotOrder) >= 2 {
		fs.add(RuleFirmware, statement("fw.train_followed", "a", slotOrder[0], "b", slotOrder[1]), refs, len(refs))
		return
	}
	// The module has shown no error of its own cells: no uncorrected error
	// and no fault inside it located on record.
	for _, i := range d.events {
		if j.in.events[i].sev != sevCE {
			return
		}
	}
	for _, f := range d.Faults {
		if f.Grade == model.GradeA || f.Grade == model.GradeB {
			return
		}
	}
	if len(boots) >= 2 {
		fs.add(RuleFWTrain, statement("fw.train_boots", "n", len(boots), "when", actionWhen(latest.At, latest.TimeQuality)), refs, len(refs))
	} else {
		fs.add(RuleFWTrain, statement("fw.train", "when", actionWhen(latest.At, latest.TimeQuality), "id", idArg(x.byAction[latest.ID])), refs, len(refs))
	}
	// A reseat or a slot change since: the hold is checked.
	moved := false
	for _, a := range j.in.Actions {
		if a.Host == d.Host && a.Slot == latest.Slot && (a.Kind == model.ActionReseat || a.Kind == model.ActionReplacement) && a.At.After(latest.At) {
			moved = true
		}
	}
	if !moved {
		fs.checks = append(fs.checks, model.Check{Effect: model.EffectOpen, Statement: statement("check.train_reseat")})
	}
	// The slot failed training with another module before.
	for i := range j.in.Actions {
		a := &j.in.Actions[i]
		if a.Kind != model.ActionTrainingFailure || a.Host != d.Host || a.Slot != latest.Slot || a.DIMMUID == "" || a.DIMMUID == d.UID || a.WithdrawnBy != 0 {
			continue
		}
		serial := a.DIMMUID
		for k := range j.in.DIMMs {
			if j.in.DIMMs[k].UID == a.DIMMUID && j.in.DIMMs[k].Serial != "" {
				serial = j.in.DIMMs[k].Serial
			}
		}
		fs.checks = append(fs.checks, model.Check{Effect: model.EffectSupports, Statement: statement("check.train_slot",
			"serial", serial, "when", actionWhen(a.At, a.TimeQuality))})
		break
	}
}

// failedRepair speaks about a step of firmware that failed: a repair, or
// sparing that has no spare left. It reports whether it spoke.
func (j *judge) failedRepair(fs *findings, a *model.Action, who func(*model.Action) string) bool {
	f := j.fw().byAction[a.ID]
	if f == nil {
		return false
	}
	refs := []model.RecordRef{actionRef(a.ID, a.RawRef)}
	switch {
	case a.Kind == model.ActionSparing || a.Kind == model.ActionDeviceSparing:
		fs.add(RuleRepairGone, statement("rep.no_spare", "who", who(a), "when", actionWhen(a.At, a.TimeQuality), "id", idArg(f)), refs, 1)
		return true
	case pprKind(a.Kind) && actionWhere(a) == "":
		fs.add(RuleRepairGone, statement("rep.failed_fw", "kind", kindArg(a.Kind), "when", actionWhen(a.At, a.TimeQuality), "id", idArg(f)), refs, 1)
		return true
	}
	return false
}

// thresholds adds what firmware said about the corrected errors of the
// module to the rule that speaks about them: its own limit was passed. It
// fires no rule by itself: a count without a place decides no
// replacement, and a prediction has a rule of its own.
func (j *judge) thresholds(fs *findings) {
	var rule string
	for _, r := range []string{RuleRow, RuleSoft} {
		if fs.has(r) {
			rule = r
			break
		}
	}
	if rule == "" {
		return
	}
	var list []*model.FirmwareRecord
	for _, f := range j.fw().byDIMM[fs.d.UID] {
		if f.Common == string(fwevent.CEThreshold) && f.EventID != 0 {
			list = append(list, f)
		}
	}
	if len(list) == 0 {
		return
	}
	last := list[len(list)-1]
	var refs []model.RecordRef
	for _, f := range list {
		refs = append(refs, model.RecordRef{Kind: model.RecordEvent, ID: f.EventID, RawRef: f.RawRef})
	}
	fs.add(rule, statement("fw.threshold", "n", len(list), "when", actionWhen(last.At, model.TimeExact), "id", idArg(last)), refs, len(refs))
}

// fwChecks says how far the record that decided the grade can be trusted:
// classed by a table that was not checked on real hardware, or by its
// wording only. It reports whether the record was classed by wording.
func (j *judge) fwChecks(fs *findings, grade model.VerdictGrade) (wording bool) {
	x := j.fw()
	for _, e := range fs.evidence {
		if r, _ := RuleByID(e.Rule); r.Grade != grade {
			continue
		}
		for _, ref := range e.Records {
			var f *model.FirmwareRecord
			switch ref.Kind {
			case model.RecordAction:
				f = x.byAction[ref.ID]
			case model.RecordEvent:
				f = x.byEvent[ref.ID]
			}
			if f == nil {
				continue
			}
			id := f.VendorID
			if id == "" {
				id = f.Common
			}
			switch {
			case f.Mapping == model.MappingWording || f.Mapping == model.MappingRegistry:
				fs.checks = append(fs.checks, model.Check{Effect: model.EffectOpen, Statement: statement("check.fw_wording", "id", id)})
				return true
			case f.MapRef != "" && f.Basis != "capture":
				table, _, _ := strings.Cut(f.MapRef, ":")
				fs.checks = append(fs.checks, model.Check{Effect: model.EffectOpen, Statement: statement("check.fw_unchecked",
					"id", id, "table", table, "basis", f.Basis)})
				return false
			}
			return false
		}
	}
	return false
}

// fwMissing lists what firmware records say is missing: records about the
// module in words this version has no class for, and a log of the BMC that
// was cleared or is full.
func (j *judge) fwMissing(fs *findings) []model.Statement {
	x := j.fw()
	var out []model.Statement
	n := 0
	for _, f := range x.byDIMM[fs.d.UID] {
		if f.Common == string(fwevent.Unclassified) {
			n++
		}
	}
	if n > 0 {
		out = append(out, statement("missing.fw_unclassified", "n", n))
	}
	var cleared, full time.Time
	for _, f := range x.byHost[fs.d.Host] {
		switch f.Common {
		case string(fwevent.LogCleared):
			if f.At.After(cleared) {
				cleared = f.At
			}
		case string(fwevent.LogFull):
			if full.IsZero() || f.At.Before(full) {
				full = f.At
			}
		}
	}
	if !cleared.IsZero() {
		out = append(out, statement("missing.bmc_log_cleared", "when", actionWhen(cleared, model.TimeExact)))
	}
	if !full.IsZero() {
		out = append(out, statement("missing.bmc_log_full", "when", actionWhen(full, model.TimeExact)))
	}
	return out
}

// unnamedCommons are the common events that, naming no DIMM, still say
// something about the memory of the host.
var unnamedCommons = map[string]bool{
	string(fwevent.MirrorLost): true, string(fwevent.MirrorFailover): true, string(fwevent.Sparing): true,
	string(fwevent.DeviceSparing): true, string(fwevent.SparingFailed): true, string(fwevent.PredictedFailure): true,
	string(fwevent.Disabled): true, string(fwevent.TrainingFailure): true, string(fwevent.PPRDone): true,
	string(fwevent.PPRFailed): true, string(fwevent.ConfigError): true,
}

// unnamed returns the firmware records of a host about memory that name no
// DIMM, and their common events.
func (j *judge) unnamed(host string) (n int, kinds []string) {
	seen := map[string]bool{}
	for _, f := range j.fw().byHost[host] {
		if f.DIMMUID != "" || f.Slot != "" || !unnamedCommons[f.Common] {
			continue
		}
		n++
		if !seen[f.Common] {
			seen[f.Common] = true
			kinds = append(kinds, f.Common)
		}
	}
	sort.Strings(kinds)
	return n, kinds
}
