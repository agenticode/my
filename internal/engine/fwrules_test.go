package engine

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
)

// fwAct adds an action that came from a BMC log, with its firmware record.
func (b *builder) fwAct(uid string, kind model.ActionKind, result model.ActionResult, at time.Time, common, id string, mut func(*model.Action)) *model.Action {
	a := b.act(uid, kind, at, func(a *model.Action) {
		a.Source, a.Result, a.Method = "redfish/log", result, "bmc"
		if mut != nil {
			mut(a)
		}
	})
	b.in.Firmware = append(b.in.Firmware, model.FirmwareRecord{ActionID: a.ID, Host: "h1", At: at, Common: common, VendorID: id,
		Mapping: model.MappingTable, MapRef: "dell@1:" + id, Basis: "registry", DIMMUID: uid, Slot: a.Slot})
	return a
}

func hasCheck(v *model.Verdict, code string) bool {
	for _, c := range v.CounterEvidence {
		if c.Code == code {
			return true
		}
	}
	return false
}

func TestRuleFirmwarePrediction(t *testing.T) {
	t.Run("a prediction fires R-FW-02", func(t *testing.T) {
		b := ruleBuilder()
		b.fwAct("d1", model.ActionPredictedFailure, model.ResultOK, t0.Add(40*day), "predicted-failure", "MEM1234", nil)
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictConsiderReplacement, RuleFWPredict)
		e := evidenceOf(t, v, RuleFWPredict)
		if e.Code != "fw.pfa" || !strings.Contains(e.Text, "(MEM1234)") || !strings.Contains(e.Text, "the BMC predicts a failure") {
			t.Errorf("evidence %+v", e)
		}
		if ko := Text(i18n.KO, e.Statement); !strings.Contains(ko, "고장을 예측") {
			t.Errorf("Korean %q", ko)
		}
		if v.NextAction.Code != "next.fw_pfa" || !hasCheck(v, "check.fw_unchecked") {
			t.Errorf("next %q, checks %v", v.NextAction.Code, codes(v))
		}
	})
	t.Run("a prediction taken back fires nothing", func(t *testing.T) {
		b := ruleBuilder()
		b.fwAct("d1", model.ActionPredictedFailure, model.ResultOK, t0.Add(40*day), "predicted-failure", "MEM1234", func(a *model.Action) { a.WithdrawnBy = 99 })
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictOK)
	})
	t.Run("errors at one cell weaken it", func(t *testing.T) {
		b := ruleBuilder()
		b.fwAct("d1", model.ActionPredictedFailure, model.ResultOK, t0.Add(40*day), "predicted-failure", "", nil)
		for i := range 3 {
			b.ce("d1", t0.Add(time.Duration(10+i)*day), cellA)
		}
		v := verdictOf(t, b, asOf, "d1")
		if !hasCheck(v, "check.pfa_cell") {
			t.Errorf("checks %v", codes(v))
		}
	})
}

func TestRuleFirmwareTraining(t *testing.T) {
	t.Run("a training failure without errors holds", func(t *testing.T) {
		b := ruleBuilder()
		b.fwAct("d1", model.ActionTrainingFailure, model.ResultOK, t0.Add(40*day), "training-failure", "MEM0700", nil)
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictHoldReplacement, RuleFWTrain)
		if v.NextAction.Code != "next.fw_train" || !hasCheck(v, "check.train_reseat") {
			t.Errorf("next %q, checks %v", v.NextAction.Code, codes(v))
		}
	})
	t.Run("in two boots", func(t *testing.T) {
		b := ruleBuilder()
		b.fwAct("d1", model.ActionTrainingFailure, model.ResultOK, t0.Add(40*day), "training-failure", "", nil)
		b.fwAct("d1", model.ActionTrainingFailure, model.ResultOK, t0.Add(45*day), "training-failure", "", nil)
		v := verdictOf(t, b, asOf, "d1")
		if e := evidenceOf(t, v, RuleFWTrain); e.Code != "fw.train_boots" {
			t.Errorf("evidence %+v", e)
		}
	})
	t.Run("an uncorrected error: no hold", func(t *testing.T) {
		b := ruleBuilder()
		b.fwAct("d1", model.ActionTrainingFailure, model.ResultOK, t0.Add(40*day), "training-failure", "", nil)
		b.add(severity(b.event("d1", t0.Add(30*day), cellA), model.SevUE, model.ScrubDemand))
		v := verdictOf(t, b, asOf, "d1")
		if slices.Contains(v.Rules, RuleFWTrain) {
			t.Errorf("rules %v", v.Rules)
		}
	})
	t.Run("a located fault: no hold", func(t *testing.T) {
		b := ruleBuilder()
		b.fwAct("d1", model.ActionTrainingFailure, model.ResultOK, t0.Add(40*day), "training-failure", "", nil)
		b.ce("d1", t0.Add(30*day), cellA)
		v := verdictOf(t, b, asOf, "d1")
		if slices.Contains(v.Rules, RuleFWTrain) {
			t.Errorf("rules %v", v.Rules)
		}
	})
	t.Run("the failure followed the module into a second slot", func(t *testing.T) {
		b := ruleBuilder()
		b.fwAct("d1", model.ActionTrainingFailure, model.ResultOK, t0.Add(40*day), "training-failure", "", nil)
		b.fwAct("d1", model.ActionTrainingFailure, model.ResultOK, t0.Add(45*day), "training-failure", "", func(a *model.Action) { a.Slot = "S-other" })
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictReplaceNow, RuleFirmware)
		if e := evidenceOf(t, v, RuleFirmware); e.Code != "fw.train_followed" {
			t.Errorf("evidence %+v", e)
		}
	})
	t.Run("another module failed training in the slot", func(t *testing.T) {
		b := ruleBuilder()
		b.fwAct("d1", model.ActionTrainingFailure, model.ResultOK, t0.Add(40*day), "training-failure", "", nil)
		b.fwAct("d0", model.ActionTrainingFailure, model.ResultOK, t0.Add(10*day), "training-failure", "", func(a *model.Action) { a.Slot = "S-d1" })
		v := verdictOf(t, b, asOf, "d1")
		if !hasCheck(v, "check.train_slot") {
			t.Errorf("checks %v", codes(v))
		}
	})
}

func TestRuleFirmwareSteps(t *testing.T) {
	t.Run("the mirror failed over: R-FW-01", func(t *testing.T) {
		b := ruleBuilder()
		b.fwAct("d1", model.ActionMirrorFailover, model.ResultOK, t0.Add(40*day), "mirror-failover", "MEM8000", nil)
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictReplaceNow, RuleFirmware)
	})
	t.Run("a repair that failed: R-REP-01", func(t *testing.T) {
		b := ruleBuilder()
		b.fwAct("d1", model.ActionPPR, model.ResultFailed, t0.Add(40*day), "ppr-failed", "MEM0805", nil)
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictReplaceNow, RuleRepairGone)
		if e := evidenceOf(t, v, RuleRepairGone); e.Code != "rep.failed_fw" || !strings.Contains(e.Text, "(MEM0805)") {
			t.Errorf("evidence %+v", e)
		}
	})
	t.Run("no spare is left: R-REP-01", func(t *testing.T) {
		b := ruleBuilder()
		b.fwAct("d1", model.ActionDeviceSparing, model.ResultFailed, t0.Add(40*day), "sparing-failed", "", nil)
		v := verdictOf(t, b, asOf, "d1")
		if e := evidenceOf(t, v, RuleRepairGone); e.Code != "rep.no_spare" {
			t.Errorf("evidence %+v", e)
		}
	})
	t.Run("device sparing in use fires no rule", func(t *testing.T) {
		b := ruleBuilder()
		b.fwAct("d1", model.ActionDeviceSparing, model.ResultOK, t0.Add(40*day), "device-sparing", "", nil)
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictOK)
	})
	t.Run("a repair that waits for the next boot", func(t *testing.T) {
		b := ruleBuilder()
		b.fwAct("d1", model.ActionPPR, model.ResultPending, t0.Add(40*day), "ppr-scheduled", "", nil)
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictOK)
		if !hasCheck(v, "check.ppr_pending") {
			t.Errorf("checks %v", codes(v))
		}
		// Then done: no open check.
		b.fwAct("d1", model.ActionPPR, model.ResultOK, t0.Add(41*day), "ppr-done", "", nil)
		if v := verdictOf(t, b, asOf, "d1"); hasCheck(v, "check.ppr_pending") {
			t.Errorf("checks after the repair ran: %v", codes(v))
		}
	})
}

// TestRepairOfTheModule covers the four rows of the example table of the
// design: errors before a repair that names the module only, and after.
func TestRepairOfTheModule(t *testing.T) {
	repair := t0.Add(30 * day)
	before := func(b *builder) {
		for i := range 12 {
			b.ce("d1", repair.Add(-time.Duration(i+1)*day), cellA)
		}
	}
	t.Run("errors after the repair: R-REP-02", func(t *testing.T) {
		b := ruleBuilder()
		before(b)
		b.fwAct("d1", model.ActionPPR, model.ResultOK, repair, "ppr-done", "MEM0804", nil)
		for i := range 12 {
			b.ce("d1", repair.Add(time.Duration(i+1)*day), cellA)
		}
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictConsiderReplacement, RuleRecurred)
		e := evidenceOf(t, v, RuleRecurred)
		if e.Code != "rep.recurred_dimm" || !strings.Contains(e.Text, "12 errors") || !hasCheck(v, "check.ppr_kind") {
			t.Errorf("evidence %+v, checks %v", e, codes(v))
		}
	})
	t.Run("quiet since the repair: R-QUIET-01", func(t *testing.T) {
		b := ruleBuilder()
		before(b)
		b.fwAct("d1", model.ActionHPPR, model.ResultOK, repair, "ppr-done", "", nil)
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictObserve, RuleQuiet)
		if e := evidenceOf(t, v, RuleQuiet); e.Code != "quiet.after_dimm" {
			t.Errorf("evidence %+v", e)
		}
	})
	t.Run("quiet for fewer days than needed", func(t *testing.T) {
		b := ruleBuilder()
		before(b)
		b.fwAct("d1", model.ActionHPPR, model.ResultOK, asOf.Add(-3*day), "ppr-done", "", nil)
		v := verdictOf(t, b, asOf, "d1")
		if slices.Contains(v.Rules, RuleQuiet) || !hasCheck(v, "check.quiet_short") {
			t.Errorf("rules %v, checks %v", v.Rules, codes(v))
		}
	})
	t.Run("a fault first seen after the repair is no recurrence", func(t *testing.T) {
		b := ruleBuilder()
		b.fwAct("d1", model.ActionHPPR, model.ResultOK, repair, "ppr-done", "", nil)
		for i := range 3 {
			b.ce("d1", repair.Add(time.Duration(i+1)*day), cellA)
		}
		v := verdictOf(t, b, asOf, "d1")
		if slices.Contains(v.Rules, RuleRecurred) {
			t.Errorf("rules %v", v.Rules)
		}
	})
	t.Run("a repair dated by a BMC, errors hours later", func(t *testing.T) {
		b := ruleBuilder()
		before(b)
		b.fwAct("d1", model.ActionHPPR, model.ResultOK, repair, "ppr-done", "", nil)
		b.ce("d1", repair.Add(3*time.Hour), cellA)
		v := verdictOf(t, b, asOf, "d1")
		if !hasCheck(v, "check.bmc_clock") {
			t.Errorf("checks %v", codes(v))
		}
	})
}

// TestFirmwareMissing: records about the DIMM no table classes, and a log
// of the BMC that was cleared and one that is full.
func TestFirmwareMissing(t *testing.T) {
	b := ruleBuilder()
	b.ce("d1", t0.Add(20*day), cellA)
	b.in.Firmware = append(b.in.Firmware,
		model.FirmwareRecord{Host: "h1", At: t0.Add(21 * day), Common: "unclassified", DIMMUID: "d1", EventID: 1000},
		model.FirmwareRecord{Host: "h1", At: t0.Add(22 * day), Common: "log-cleared", Fingerprint: "c"},
		model.FirmwareRecord{Host: "h1", At: t0.Add(22*day + time.Hour), Common: "log-full", Fingerprint: "f"},
		model.FirmwareRecord{Host: "h1", At: t0.Add(23 * day), Common: "mirror-lost", EventID: 1001})
	res := analyze(t, b.in, Options{AsOf: asOf})
	v := res.Host("h1").DIMM("d1").Verdict
	var missing []string
	for _, m := range v.Missing {
		missing = append(missing, m.Code)
	}
	if !slices.Contains(missing, "missing.fw_unclassified") || !slices.Contains(missing, "missing.bmc_log_cleared") ||
		!slices.Contains(missing, "missing.bmc_log_full") {
		t.Errorf("missing %v", missing)
	}
	hf := res.Host("h1").Loose
	if hf == nil || !slices.Contains(hf.Rules, RuleHostCE) {
		t.Fatalf("host finding %+v", hf)
	}
	found := false
	for _, e := range hf.Evidence {
		found = found || e.Code == "host.fw_unnamed" && strings.Contains(e.Text, "mirror-lost")
	}
	if !found {
		t.Errorf("host finding evidence %+v", hf.Evidence)
	}
}
