package engine

import (
	"testing"
	"time"

	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
)

// Every rule has a test that makes it fire and tests that come close and
// must not. The fixtures are built by hand around a reference time 60 days
// after t0, with the host observed since t0.

var asOf = t0.Add(60 * day)

func ruleBuilder() *builder {
	b := newBuilder()
	b.observedSince(t0)
	return b
}

func severity(e *model.Event, sev model.Severity, scrub model.ScrubType) *model.Event {
	e.ErrorSeverity, e.ErrorScrubType = sev, scrub
	return e
}

// evidenceOf returns the first evidence of a rule.
func evidenceOf(t *testing.T, v *model.Verdict, rule string) model.Evidence {
	t.Helper()
	for _, e := range v.Evidence {
		if e.Rule == rule {
			return e
		}
	}
	t.Fatalf("no evidence of %s in %q", rule, codes(v))
	return model.Evidence{}
}

func TestRuleUncorrected(t *testing.T) {
	t.Run("an uncorrected error fires R-UE-01", func(t *testing.T) {
		b := ruleBuilder()
		ue := severity(b.event("d1", t0.Add(50*day), cellA), model.SevUE, model.ScrubDemand)
		b.add(ue)
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictReplaceNow, RuleUE)
		e := evidenceOf(t, v, RuleUE)
		if e.Code != "ue.located" || e.Total != 1 || len(e.Records) != 1 || e.Records[0] != (model.RecordRef{Kind: model.RecordEvent, ID: ue.ID}) {
			t.Errorf("evidence = %+v", e)
		}
		if want := "1 uncorrected error, the latest on 2026-11-20 00:00 UTC at rank0 dev3 bg1 bank2 row 0x1388 col 0x280"; e.Text != want {
			t.Errorf("text = %q\nwant   %q", e.Text, want)
		}
		if got := Text(i18n.KO, e.Statement); got != "UE 1건, 최근 2026-11-20 00:00 UTC, rank0 dev3 bg1 bank2 row 0x1388 col 0x280" {
			t.Errorf("Korean text = %q", got)
		}
		if v.NextAction.Code != "next.replace" || v.Confidence < 75 {
			t.Errorf("next %q, confidence %d", v.NextAction.Code, v.Confidence)
		}
	})

	t.Run("corrected errors alone do not", func(t *testing.T) {
		b := ruleBuilder()
		b.spreadOver(cellA, 40, t0.Add(10*day), asOf)
		wantVerdict(t, verdictOf(t, b, asOf, "d1"), model.VerdictObserve, RuleSoft)
	})

	t.Run("an uncorrected error of another DIMM, or after the reference time, does not", func(t *testing.T) {
		b := ruleBuilder()
		b.ce("d1", t0.Add(40*day), cellA)
		b.add(severity(b.event("d2", t0.Add(50*day), cellA), model.SevUE, model.ScrubDemand))
		b.add(severity(b.event("d1", asOf.Add(day), cellA), model.SevFatal, model.ScrubDemand))
		wantVerdict(t, verdictOf(t, b, asOf, "d1"), model.VerdictObserve, RuleSoft)
		wantVerdict(t, verdictOf(t, b, asOf, "d2"), model.VerdictReplaceNow, RuleUE)
	})

	t.Run("an uncorrected error known from a counter fires, with less confidence", func(t *testing.T) {
		b := ruleBuilder()
		e := severity(b.event("d1", t0.Add(50*day), loc{-1, -1, -1, -1, -1, -1}), model.SevUE, "")
		e.Kind, e.Count, e.WindowStart = model.KindCounter, 2, t0.Add(49*day)
		b.add(e)
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictReplaceNow, RuleUE)
		if e := evidenceOf(t, v, RuleUE); e.Code != "ue.unlocated" || e.Args["n"] != int64(2) {
			t.Errorf("evidence = %+v", e)
		}
		located := ruleBuilder()
		located.add(severity(located.event("d1", t0.Add(50*day), cellA), model.SevUE, model.ScrubDemand))
		if lv := verdictOf(t, located, asOf, "d1"); v.Confidence >= lv.Confidence {
			t.Errorf("confidence %d with a count, %d with a located record", v.Confidence, lv.Confidence)
		}
	})

	t.Run("an uncorrected error the patrol scrubber found fires R-SCRUB-01", func(t *testing.T) {
		b := ruleBuilder()
		b.add(severity(b.event("d1", t0.Add(50*day), cellA), model.SevRecoverable, model.ScrubPatrol))
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictConsiderReplacement, RuleScrub)
		if e := evidenceOf(t, v, RuleScrub); e.Code != "scrub.located" {
			t.Errorf("evidence = %+v", e)
		}
		if v.NextAction.Code != "next.plan" {
			t.Errorf("next = %q", v.NextAction.Code)
		}
	})

	t.Run("a corrected error the patrol scrubber found does not", func(t *testing.T) {
		b := ruleBuilder()
		b.add(severity(b.event("d1", t0.Add(50*day), cellA), model.SevCE, model.ScrubPatrol))
		wantVerdict(t, verdictOf(t, b, asOf, "d1"), model.VerdictObserve, RuleSoft)
	})

	t.Run("a machine check panic is R-UE-01 whoever found it", func(t *testing.T) {
		b := ruleBuilder()
		b.add(severity(b.event("d1", t0.Add(50*day), cellA), model.SevFatal, model.ScrubPatrol))
		wantVerdict(t, verdictOf(t, b, asOf, "d1"), model.VerdictReplaceNow, RuleUE)
	})

	t.Run("both kinds fire both rules and the higher grade wins", func(t *testing.T) {
		b := ruleBuilder()
		b.add(severity(b.event("d1", t0.Add(40*day), cellA), model.SevRecoverable, model.ScrubPatrol))
		b.add(severity(b.event("d1", t0.Add(50*day), cellA), model.SevUE, model.ScrubDemand))
		b.add(severity(b.event("d1", t0.Add(51*day), cellA), model.SevUE, ""))
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictReplaceNow, RuleUE, RuleScrub)
		if e := evidenceOf(t, v, RuleUE); e.Args["n"] != int64(2) || e.Total != 2 {
			t.Errorf("R-UE-01 evidence = %+v", e)
		}
	})
}

func TestRuleFirmware(t *testing.T) {
	t.Run("a DIMM the BIOS took out of use fires R-FW-01", func(t *testing.T) {
		b := ruleBuilder()
		a := b.act("d1", model.ActionDisable, t0.Add(30*day), func(a *model.Action) { a.Method, a.Detail = "bios", "DIMM disabled after training failure" })
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictReplaceNow, RuleFirmware)
		e := evidenceOf(t, v, RuleFirmware)
		if e.Code != "fw.disabled_detail" || len(e.Records) != 1 || e.Records[0] != (model.RecordRef{Kind: model.RecordAction, ID: a.ID, RawRef: a.RawRef}) {
			t.Errorf("evidence = %+v", e)
		}
		if want := "the BIOS took the DIMM out of use on 2026-10-31: DIMM disabled after training failure"; e.Text != want {
			t.Errorf("text = %q", e.Text)
		}
		if got := Text(i18n.KO, e.Statement); got != "2026-10-31 BIOS가 DIMM 사용 중지: DIMM disabled after training failure" {
			t.Errorf("Korean text = %q", got)
		}
	})

	t.Run("a DIMM the BMC reports as failed fires R-FW-01", func(t *testing.T) {
		b := ruleBuilder()
		b.act("d1", model.ActionMarkFailed, t0.Add(30*day), func(a *model.Action) { a.Method = "bmc" })
		b.act("d1", model.ActionMarkFailed, t0.Add(40*day), func(a *model.Action) { a.Method = "bmc" })
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictReplaceNow, RuleFirmware)
		// Reported since the first mark.
		if e := evidenceOf(t, v, RuleFirmware); e.Code != "fw.failed" || e.Args["when"] != "2026-10-31" || e.Args["who"] != "@word.bmc" {
			t.Errorf("evidence = %+v", e)
		}
	})

	t.Run("other actions, and marks on another DIMM, do not", func(t *testing.T) {
		b := ruleBuilder()
		b.ce("d1", t0.Add(30*day), cellA)
		b.act("d1", model.ActionReseat, t0.Add(31*day), nil)
		b.act("d1", model.ActionReplacement, t0.Add(32*day), nil)
		b.act("d2", model.ActionDisable, t0.Add(33*day), nil)
		b.act("d1", model.ActionDisable, asOf.Add(day), nil) // after the reference time
		wantVerdict(t, verdictOf(t, b, asOf, "d1"), model.VerdictObserve, RuleSoft)
		wantVerdict(t, verdictOf(t, b, asOf, "d2"), model.VerdictReplaceNow, RuleFirmware)
	})
}

func TestRuleRepairResources(t *testing.T) {
	row := func(a *model.Action) {
		a.Rank, a.DeviceId, a.BankGroup, a.Bank, a.Row = model.Some(0), model.Some(7), model.Some(0), model.Some(3), model.Some(0x1a2f)
	}
	t.Run("a failed PPR fires R-REP-01", func(t *testing.T) {
		b := ruleBuilder()
		b.rowFault("d1", 4, 5, t0.Add(10*day), t0.Add(30*day))
		b.act("d1", model.ActionHPPR, t0.Add(31*day), func(a *model.Action) { row(a); a.Result = model.ResultFailed })
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictReplaceNow, RuleRepairGone)
		e := evidenceOf(t, v, RuleRepairGone)
		if want := "the hard PPR of rank0 dev7 bg0 bank3 row 0x1a2f failed on 2026-11-01"; e.Code != "rep.failed" || e.Text != want {
			t.Errorf("evidence = %q (%s)", e.Text, e.Code)
		}
		if got := Text(i18n.KO, e.Statement); got != "2026-11-01 rank0 dev7 bg0 bank3 row 0x1a2f hPPR 실패" {
			t.Errorf("Korean text = %q", got)
		}
	})

	t.Run("a failed repair that names no place fires too", func(t *testing.T) {
		b := ruleBuilder()
		b.act("d1", model.ActionSPPR, t0.Add(31*day), func(a *model.Action) { a.Result = model.ResultFailed })
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictReplaceNow, RuleRepairGone)
		if e := evidenceOf(t, v, RuleRepairGone); e.Code != "rep.failed_unlocated" {
			t.Errorf("evidence = %+v", e)
		}
	})

	t.Run("sparing in use fires R-REP-01", func(t *testing.T) {
		b := ruleBuilder()
		b.act("d1", model.ActionSparing, t0.Add(31*day), func(a *model.Action) { a.Rank = model.Some(1) })
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictReplaceNow, RuleRepairGone)
		if e := evidenceOf(t, v, RuleRepairGone); e.Code != "rep.sparing" || e.Args["when"] != "2026-11-01" {
			t.Errorf("evidence = %+v", e)
		}
	})

	t.Run("a repair that worked, a failed page offline and failed sparing do not", func(t *testing.T) {
		b := ruleBuilder()
		b.rowFault("d1", 4, 5, t0.Add(10*day), t0.Add(30*day))
		b.act("d1", model.ActionHPPR, t0.Add(31*day), row)
		b.act("d1", model.ActionPageOffline, t0.Add(32*day), func(a *model.Action) {
			a.OsPhysicalAddress, a.Result = model.Some(uint64(0x1000)), model.ResultFailed
		})
		b.act("d1", model.ActionSparing, t0.Add(33*day), func(a *model.Action) { a.Result = model.ResultFailed })
		v := verdictOf(t, b, asOf, "d1")
		if has(v.Rules, RuleRepairGone) {
			t.Errorf("rules = %v", v.Rules)
		}
		wantVerdict(t, v, model.VerdictObserve, RuleQuiet)
	})

	t.Run("a GPU whose row remap failed", func(t *testing.T) {
		b := ruleBuilder()
		b.in.host("h1")
		at := t0.Add(40 * day)
		b.in.GPUs = []model.GPUMem{
			{ID: 1, Host: "h1", ObservedAt: at.Add(-time.Hour), Kind: model.GPUXID, GPUSerial: "1320921000977", GPUIndex: model.Some(5), XID: model.Some(48), RawRef: "kmsg#1"},
			{ID: 2, Host: "h1", ObservedAt: at, Kind: model.GPUXID, GPUSerial: "1320921000977", GPUIndex: model.Some(5), XID: model.Some(64), RawRef: "kmsg#2"},
			{ID: 3, Host: "h1", ObservedAt: at.Add(time.Hour), Kind: model.GPUSnapshot, GPUSerial: "1320921000977", GPUIndex: model.Some(5),
				RemapFailure: model.Some(true), RawRef: "nvidia-smi#5"},
			// A GPU with double-bit errors that were remapped: no rule of
			// this engine speaks about it.
			{ID: 4, Host: "h1", ObservedAt: at, Kind: model.GPUXID, GPUSerial: "1320921000412", GPUIndex: model.Some(3), XID: model.Some(48), RawRef: "kmsg#3"},
			{ID: 5, Host: "h1", ObservedAt: at.Add(time.Hour), Kind: model.GPUSnapshot, GPUSerial: "1320921000412", GPUIndex: model.Some(3),
				RemapFailure: model.Some(false), RemapUncorrectable: model.Some(2), RawRef: "nvidia-smi#3"},
			// A GPU known by UUID only.
			{ID: 6, Host: "h1", ObservedAt: at, Kind: model.GPUXID, GPUUUID: "GPU-7a1c", XID: model.Some(64), RawRef: "kmsg#4"},
		}
		res := analyze(t, b.in, Options{AsOf: asOf})
		h := res.Host("h1")
		if len(h.GPUs) != 2 || h.GPUs[0].Key != "1320921000977" || h.GPUs[1].Key != "GPU-7a1c" || h.GPUs[0].Index != model.Some(5) {
			t.Fatalf("GPU results = %+v", h.GPUs)
		}
		v := h.GPUs[0].Verdict
		if v.Grade != model.VerdictReplaceNow || !equalStrings(v.Rules, []string{RuleRepairGone}) || v.GPU != "1320921000977" || v.DIMMUID != "" ||
			v.Label != model.VerdictLabel || v.NextAction.Code != "next.gpu_replace" || v.Confidence != 85 {
			t.Errorf("GPU finding = %+v", v)
		}
		if got := codes(v); !equalStrings(got, []string{"R-REP-01 gpu.xid64", "R-REP-01 gpu.remap_failure"}) {
			t.Errorf("evidence = %q", got)
		}
		if r := v.Evidence[0].Records; len(r) != 1 || r[0] != (model.RecordRef{Kind: model.RecordGPU, ID: 2, RawRef: "kmsg#2"}) {
			t.Errorf("records = %+v", r)
		}
		// The host is as bad as its worst GPU.
		if h.Grade != model.VerdictReplaceNow || res.ExitCode() != 3 {
			t.Errorf("host grade %s, exit code %d", h.Grade, res.ExitCode())
		}
	})
}

func TestRuleGrowingFault(t *testing.T) {
	recentFrom, priorFrom := asOf.Add(-7*day), asOf.Add(-37*day)

	t.Run("a row fault whose errors are rising fires R-ROW-03", func(t *testing.T) {
		b := ruleBuilder()
		b.rowFault("d1", 4, 5, priorFrom, recentFrom) // 20 errors in 30 days
		b.rowFault("d1", 4, 5, recentFrom, asOf)      // 20 errors in 7 days
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictConsiderReplacement, RuleRow)
		e := evidenceOf(t, v, RuleRow)
		if want := "row fault at rank0 dev7 bg0 bank3 row 0x1a2f: 20 errors in the last 7 days, 4.3 times the daily rate of the 30 days before"; e.Code != "row.rising" || e.Text != want {
			t.Errorf("evidence = %q (%s)", e.Text, e.Code)
		}
		if got := Text(i18n.KO, e.Statement); got != "row 고장(rank0 dev7 bg0 bank3 row 0x1a2f): 최근 7일 20건, 그 앞 30일 일평균의 4.3배" {
			t.Errorf("Korean text = %q", got)
		}
		// The records are the errors of the recent window.
		if e.Total != 20 || len(e.Records) != 20 {
			t.Errorf("%d of %d records listed", len(e.Records), e.Total)
		}
		if v.NextAction.Code != "next.plan" || !has(checkCodes(v), "open check.no_cross_test") {
			t.Errorf("next %q, checks %q", v.NextAction.Code, checkCodes(v))
		}
	})

	t.Run("a row fault that reaches new columns fires R-ROW-03", func(t *testing.T) {
		b := ruleBuilder()
		b.rowFault("d1", 3, 10, t0.Add(day), asOf) // steady: 0.5 errors a day
		b.ce("d1", asOf.Add(-10*day), loc{0, 7, 0, 3, 0x1a2f, 1024})
		b.ce("d1", asOf.Add(-9*day), loc{0, 7, 0, 3, 0x1a2f, 1088})
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictConsiderReplacement, RuleRow)
		e := evidenceOf(t, v, RuleRow)
		if want := "row fault at rank0 dev7 bg0 bank3 row 0x1a2f: 2 new places in the last 30 days; it now covers 5 columns in 1 row"; e.Code != "row.spreading_line" || e.Text != want {
			t.Errorf("evidence = %q (%s)", e.Text, e.Code)
		}
	})

	t.Run("a fault that appeared this week with many errors fires R-ROW-03", func(t *testing.T) {
		b := ruleBuilder()
		b.rowFault("d1", 4, 5, asOf.Add(-3*day), asOf)
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictConsiderReplacement, RuleRow)
		if e := evidenceOf(t, v, RuleRow); e.Code != "row.new" || e.Args["recent"] != int64(20) {
			t.Errorf("evidence = %+v", e)
		}
	})

	t.Run("a recent storm on a row fault fires R-ROW-03", func(t *testing.T) {
		b := ruleBuilder()
		// A steady rate before and within the recent window, at the same
		// columns: not rising, not spreading. Then 150 errors in half an
		// hour two days before the reference time.
		b.rowFault("d1", 3, 220, priorFrom, recentFrom)
		b.rowFault("d1", 3, 50, asOf.Add(-2*day), asOf.Add(-2*day+30*time.Minute))
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictConsiderReplacement, RuleRow)
		if got := codes(v); !equalStrings(got, []string{"R-ROW-03 row.storm"}) {
			t.Errorf("evidence = %q", got)
		}
		if e := evidenceOf(t, v, RuleRow); e.Args["peak"] != int64(150) || e.Total != 150 || e.Args["when"] != stampText(asOf.Add(-2*day+6*time.Second).UnixMicro()) {
			t.Errorf("evidence = %+v", e)
		}
	})

	t.Run("a storm that lies further back raises no finding", func(t *testing.T) {
		b := ruleBuilder()
		b.rowFault("d1", 3, 20, t0.Add(day), t0.Add(2*day)) // long ago: not rising, not spreading
		b.rowFault("d1", 3, 50, t0.Add(3*day), t0.Add(3*day+30*time.Minute))
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictObserve, RuleSoft)
		// The burst is still said, with the errors of the DIMM.
		if got := codes(v); !equalStrings(got, []string{"R-SOFT-01 soft.fault", "R-SOFT-01 soft.storm"}) {
			t.Errorf("evidence = %q", got)
		}
		// Eight days back is outside the recent window of seven days.
		b = ruleBuilder()
		b.rowFault("d1", 3, 220, priorFrom, recentFrom)
		b.rowFault("d1", 3, 50, asOf.Add(-8*day), asOf.Add(-8*day+30*time.Minute))
		wantVerdict(t, verdictOf(t, b, asOf, "d1"), model.VerdictObserve, RuleSoft)
	})

	// bankFault puts n errors at n places of one bank within n hours.
	bankFault := func(b *builder, n int, start time.Time) {
		for k := range n {
			b.ce("d1", start.Add(time.Duration(k)*time.Hour), loc{0, 7, 0, 3, 1000 + k*977, 8 * ((k * 13) % 250)})
		}
	}

	t.Run("a fault that covers a bank fires R-ROW-03 without growth", func(t *testing.T) {
		b := ruleBuilder()
		// 30 errors at 30 places 52 days back: no error in the recent
		// window, no new place in the last 30 days, no burst.
		bankFault(b, 30, t0.Add(8*day))
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictConsiderReplacement, RuleRow)
		if got := codes(v); !equalStrings(got, []string{"R-ROW-03 row.area"}) {
			t.Errorf("evidence = %q", got)
		}
		if e := evidenceOf(t, v, RuleRow); e.Args["mode"] != "@mode.bank" || e.Args["n"] != 30 || e.Args["cells"] != 30 || e.Args["from"] != "2026-10-09" || e.Total != 30 {
			t.Errorf("evidence = %+v", e)
		}
	})

	t.Run("a bank fault that grows is stated by its growth", func(t *testing.T) {
		b := ruleBuilder()
		bankFault(b, 30, asOf.Add(-3*day))
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictConsiderReplacement, RuleRow)
		for _, c := range codes(v) {
			if c == "R-ROW-03 row.area" {
				t.Errorf("a growing fault is also stated as an area: %q", codes(v))
			}
		}
	})

	t.Run("a bank fault below the error minimum stays with R-SOFT-01", func(t *testing.T) {
		b := ruleBuilder()
		bankFault(b, 6, t0.Add(8*day))
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictObserve, RuleSoft)
		if got := codes(v); !equalStrings(got, []string{"R-SOFT-01 soft.few"}) {
			t.Errorf("evidence = %q", got)
		}
	})

	t.Run("more than one failing data line is stated with it", func(t *testing.T) {
		b := ruleBuilder()
		for k := range 20 {
			e := b.event("d1", recentFrom.Add(time.Hour+time.Duration(k)*8*time.Hour), loc{0, 7, 0, 3, 0x1a2f, 64 * (1 + k%4)})
			e.ErrorVector = "dq:0xc"
			b.add(e)
		}
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictConsiderReplacement, RuleRow)
		if got := codes(v); !equalStrings(got, []string{"R-ROW-03 row.new", "R-ROW-03 row.multi_dq"}) {
			t.Errorf("evidence = %q", got)
		}
	})

	t.Run("it applies to every mode wider than a cell", func(t *testing.T) {
		for _, tt := range []struct {
			mode model.FaultMode
			at   func(k int) loc
		}{
			{model.ModeColumn, func(k int) loc { return loc{0, 7, 0, 3, 100 + (k%6)*900, 512} }},
			{model.ModeMultiRow, func(k int) loc { return loc{0, 7, 0, 3, 700 + k%2, 64 * (1 + k%5)} }},
			{model.ModeBank, func(k int) loc { return loc{0, 7, 0, 3, 50 + k*811, 8 * (k * 37 % 250)} }},
			{model.ModeDevice, func(k int) loc { return loc{0, 7, k % 8, k % 4, 50 + k*811, 8 * (k * 37 % 250)} }},
			{model.ModeDIMM, func(k int) loc { return loc{k % 2, k % 20, k % 8, k % 4, 50 + k*811, 8 * (k * 37 % 250)} }},
		} {
			b := ruleBuilder()
			for k := range 40 {
				b.ce("d1", recentFrom.Add(time.Duration(k)*4*time.Hour), tt.at(k))
			}
			h := analyze(t, b.in, Options{AsOf: asOf}).Host("h1")
			d := h.DIMM("d1")
			if d.Faults[0].Mode != tt.mode {
				t.Errorf("fixture for %s was classified as %s", tt.mode, d.Faults[0].Mode)
				continue
			}
			wantVerdict(t, d.Verdict, model.VerdictConsiderReplacement, RuleRow)
		}
	})

	t.Run("one cell does not, however fast it fails", func(t *testing.T) {
		b := ruleBuilder()
		b.spreadOver(cellA, 10, priorFrom, recentFrom)
		b.spreadOver(cellA, 300, recentFrom, asOf)
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictObserve, RuleSoft)
		if e := evidenceOf(t, v, RuleSoft); e.Code != "soft.cell" {
			t.Errorf("evidence = %+v", e)
		}
	})

	t.Run("a storm on one cell is stated and stays at observe", func(t *testing.T) {
		b := ruleBuilder()
		b.spreadOver(cellA, 20, t0.Add(day), t0.Add(20*day))
		b.spreadOver(cellA, 150, t0.Add(30*day), t0.Add(30*day+30*time.Minute))
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictObserve, RuleSoft)
		if got := codes(v); !equalStrings(got, []string{"R-SOFT-01 soft.cell", "R-SOFT-01 soft.storm"}) {
			t.Fatalf("evidence = %q", got)
		}
		if e := v.Evidence[1]; e.Text != "a burst of 150 errors within 60 minutes from 2026-10-31 00:00 UTC" ||
			Text(i18n.KO, e.Statement) != "2026-10-31 00:00 UTC부터 60분 동안 150건 폭주" {
			t.Errorf("storm = %q / %q", e.Text, Text(i18n.KO, e.Statement))
		}
	})

	t.Run("a steady row fault without new places does not", func(t *testing.T) {
		b := ruleBuilder()
		b.rowFault("d1", 4, 15, t0.Add(day), asOf)
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictObserve, RuleSoft)
		if e := evidenceOf(t, v, RuleSoft); e.Code != "soft.fault" {
			t.Errorf("evidence = %+v", e)
		}
	})

	t.Run("a row fault with too few errors does not", func(t *testing.T) {
		b := ruleBuilder()
		b.rowFault("d1", 3, 3, asOf.Add(-20*day), asOf) // 9 errors, new columns within the window
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictObserve, RuleSoft)
		if e := evidenceOf(t, v, RuleSoft); e.Code != "soft.few" || e.Args["min"] != 10 {
			t.Errorf("evidence = %+v", e)
		}
		// With a lower threshold it does.
		cfg := DefaultConfig()
		cfg.RowMinErrors = 9
		d := analyze(t, b.in, Options{AsOf: asOf, Config: &cfg}).Host("h1").DIMM("d1")
		wantVerdict(t, d.Verdict, model.VerdictConsiderReplacement, RuleRow)
	})

	t.Run("rising counts without rows and columns do not: the mode is unknown", func(t *testing.T) {
		b := ruleBuilder()
		counter := func(n int, at time.Time) {
			e := b.event("d1", at, loc{-1, -1, -1, -1, -1, -1})
			e.Kind, e.Count, e.WindowStart = model.KindCounter, n, at.Add(-day)
			b.add(e)
		}
		for k := range 30 {
			counter(2, priorFrom.Add(time.Duration(k)*day+time.Hour))
		}
		for k := range 7 {
			counter(40, recentFrom.Add(time.Duration(k)*day+time.Hour))
		}
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictObserve, RuleSoft)
		if e := evidenceOf(t, v, RuleSoft); e.Code != "soft.unlocated_rising" || e.Args["n"] != 340 || e.Args["recent"] != int64(280) {
			t.Errorf("evidence = %+v", e)
		}
		if v.NextAction.Code != "next.locate" || !has(missingCodes(v), "missing.location") {
			t.Errorf("next %q, missing %q", v.NextAction.Code, missingCodes(v))
		}
	})

	t.Run("a row inferred from addresses fires as an estimate", func(t *testing.T) {
		b := ruleBuilder()
		for k := range 24 {
			e := b.event("d1", recentFrom.Add(time.Duration(k)*6*time.Hour), loc{0, -1, -1, -1, -1, -1})
			e.OsPhysicalAddress = model.Some(uint64(0x4_0000_0000) + uint64(k%6)*512)
			b.add(e)
		}
		h := analyze(t, b.in, Options{AsOf: asOf}).Host("h1")
		d := h.DIMM("d1")
		if f := d.Faults[0]; f.Mode != model.ModeRow || !f.Estimated {
			t.Fatalf("fault = %s", faultString(f))
		}
		v := d.Verdict
		wantVerdict(t, v, model.VerdictConsiderReplacement, RuleRow)
		if !has(checkCodes(v), "weakens check.estimated") || !has(missingCodes(v), "missing.rowcol") {
			t.Errorf("checks %q, missing %q", checkCodes(v), missingCodes(v))
		}
		estimated := false
		for _, f := range v.ConfidenceFactors {
			estimated = estimated || f.Code == "conf.estimated"
		}
		if !estimated || v.Confidence >= 75 {
			t.Errorf("confidence %d with factors %+v", v.Confidence, v.ConfidenceFactors)
		}
	})
}

// offlines adds page offline actions of d1: n pages starting at the given
// page number, at the given time.
func offlines(b *builder, first, n int, at time.Time, result model.ActionResult) {
	for k := range n {
		b.act("d1", model.ActionPageOffline, at.Add(time.Duration(k)*time.Second), func(a *model.Action) {
			a.OsPhysicalAddress, a.Result = model.Some(uint64(first+k)<<12), result
		})
	}
}

func TestRuleOfflineGrowth(t *testing.T) {
	t.Run("pages taken offline on several days, the last recently, fire R-OFF-02", func(t *testing.T) {
		b := ruleBuilder()
		offlines(b, 100, 2, asOf.Add(-20*day), model.ResultOK)
		offlines(b, 200, 1, asOf.Add(-2*day), model.ResultUnknown) // the kernel log does not say how it ended
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictConsiderReplacement, RuleOffline)
		e := evidenceOf(t, v, RuleOffline)
		if want := "3 pages were taken offline on 2 different days, the last on 2026-11-28"; e.Text != want || e.Total != 3 || len(e.Records) != 3 || e.Records[0].Kind != model.RecordAction {
			t.Errorf("evidence = %q, %d records: %+v", e.Text, e.Total, e.Records)
		}
	})

	for _, tt := range []struct {
		name  string
		build func(b *builder)
	}{
		{"all pages on one day", func(b *builder) { offlines(b, 100, 6, asOf.Add(-2*day), model.ResultOK) }},
		{"the same page again on another day", func(b *builder) {
			offlines(b, 100, 3, asOf.Add(-20*day), model.ResultOK)
			offlines(b, 100, 3, asOf.Add(-2*day), model.ResultOK)
		}},
		{"the last page long ago", func(b *builder) {
			offlines(b, 100, 2, asOf.Add(-30*day), model.ResultOK)
			offlines(b, 200, 2, asOf.Add(-20*day), model.ResultOK)
		}},
		{"fewer than three pages", func(b *builder) {
			offlines(b, 100, 1, asOf.Add(-20*day), model.ResultOK)
			offlines(b, 200, 1, asOf.Add(-2*day), model.ResultOK)
		}},
		{"attempts that failed", func(b *builder) {
			offlines(b, 100, 2, asOf.Add(-20*day), model.ResultOK)
			offlines(b, 200, 3, asOf.Add(-2*day), model.ResultFailed)
		}},
	} {
		t.Run(tt.name+" do not", func(t *testing.T) {
			b := ruleBuilder()
			tt.build(b)
			if v := verdictOf(t, b, asOf, "d1"); has(v.Rules, RuleOffline) || v.Grade != model.VerdictOK {
				t.Errorf("finding = %s %v", v.Grade, v.Rules)
			}
		})
	}

	t.Run("the thresholds are configuration", func(t *testing.T) {
		b := ruleBuilder()
		offlines(b, 100, 6, asOf.Add(-2*day), model.ResultOK)
		cfg := DefaultConfig()
		cfg.OfflineMinDays = 1
		v := analyze(t, b.in, Options{AsOf: asOf, Config: &cfg}).Host("h1").DIMM("d1").Verdict
		wantVerdict(t, v, model.VerdictConsiderReplacement, RuleOffline)
	})
}

func repairRow(a *model.Action) {
	a.Rank, a.BankGroup, a.Bank, a.Row = model.Some(0), model.Some(0), model.Some(3), model.Some(0x1a2f)
}

func TestRuleRecurrenceAfterRepair(t *testing.T) {
	t.Run("errors in the repaired row after the repair fire R-REP-02", func(t *testing.T) {
		b := ruleBuilder()
		b.rowFault("d1", 3, 20, t0.Add(day), asOf) // steady, before and after the repair
		a := b.act("d1", model.ActionHPPR, t0.Add(30*day), repairRow)
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictConsiderReplacement, RuleRecurred)
		e := evidenceOf(t, v, RuleRecurred)
		if want := "31 errors at rank0 bg0 bank3 row 0x1a2f after the hard PPR of 2026-10-31"; e.Text != want {
			t.Errorf("text = %q", e.Text)
		}
		if got := Text(i18n.KO, e.Statement); got != "2026-10-31 hPPR 뒤 rank0 bg0 bank3 row 0x1a2f에서 31건 재발" {
			t.Errorf("Korean text = %q", got)
		}
		// The repair comes first among the records, then the errors after it.
		if e.Total != 32 || e.Records[0] != (model.RecordRef{Kind: model.RecordAction, ID: a.ID, RawRef: a.RawRef}) || len(e.Records) != 21 {
			t.Errorf("%d records, %d listed, first %+v", e.Total, len(e.Records), e.Records[0])
		}
	})

	t.Run("errors in the spared rank after sparing fire it too, next to R-REP-01", func(t *testing.T) {
		b := ruleBuilder()
		b.rowFault("d1", 3, 20, t0.Add(day), asOf)
		b.act("d1", model.ActionSparing, t0.Add(30*day), func(a *model.Action) { a.Rank = model.Some(0) })
		wantVerdict(t, verdictOf(t, b, asOf, "d1"), model.VerdictReplaceNow, RuleRepairGone, RuleRecurred)
	})

	t.Run("memory sparing that leaves spares is a repair of its row", func(t *testing.T) {
		// A CXL device took a spare for the row and has spares left: errors
		// in the row after it fire R-REP-02, and nothing is used up.
		b := ruleBuilder()
		b.rowFault("d1", 3, 20, t0.Add(day), asOf)
		b.act("d1", model.ActionMemorySparing, t0.Add(30*day), repairRow)
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictConsiderReplacement, RuleRecurred)
		if has(v.Rules, RuleRepairGone) {
			t.Errorf("rules = %v", v.Rules)
		}
		e := evidenceOf(t, v, RuleRecurred)
		if want := "31 errors at rank0 bg0 bank3 row 0x1a2f after the memory sparing of 2026-10-31"; e.Text != want {
			t.Errorf("text = %q", e.Text)
		}

		// The same row quiet since: no rule about the repair yet, and a
		// check counts the days of quiet.
		b = ruleBuilder()
		b.rowFault("d1", 3, 10, t0.Add(day), t0.Add(29*day))
		b.act("d1", model.ActionMemorySparing, asOf.Add(-2*day), repairRow)
		v = verdictOf(t, b, asOf, "d1")
		if has(v.Rules, RuleRepairGone) || has(v.Rules, RuleRecurred) {
			t.Errorf("rules = %v", v.Rules)
		}
		if !has(checkCodes(v), "open check.quiet_short") {
			t.Errorf("checks = %v", checkCodes(v))
		}
	})

	t.Run("errors elsewhere after the repair do not", func(t *testing.T) {
		b := ruleBuilder()
		b.rowFault("d1", 3, 10, t0.Add(day), t0.Add(29*day))
		b.act("d1", model.ActionHPPR, t0.Add(30*day), repairRow)
		// A weak cell in another bank goes on failing.
		b.spreadOver(loc{1, 2, 4, 1, 77, 8}, 20, t0.Add(day), asOf)
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictObserve, RuleSoft, RuleQuiet)
	})

	t.Run("a page taken offline is not a repair", func(t *testing.T) {
		b := ruleBuilder()
		for k := range 30 {
			e := b.event("d1", t0.Add(time.Duration(k)*2*day), cellA)
			e.OsPhysicalAddress = model.Some(uint64(0x5000))
			b.add(e)
		}
		b.act("d1", model.ActionPageOffline, t0.Add(20*day+time.Hour), func(a *model.Action) { a.OsPhysicalAddress = model.Some(uint64(0x5000)) })
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictObserve, RuleSoft)
	})

	t.Run("a repair that failed is R-REP-01, not a recurrence", func(t *testing.T) {
		b := ruleBuilder()
		b.rowFault("d1", 3, 20, t0.Add(day), asOf)
		b.act("d1", model.ActionHPPR, t0.Add(30*day), func(a *model.Action) { repairRow(a); a.Result = model.ResultFailed })
		wantVerdict(t, verdictOf(t, b, asOf, "d1"), model.VerdictReplaceNow, RuleRepairGone)
	})

	t.Run("a soft PPR is lost at a reboot", func(t *testing.T) {
		b := ruleBuilder()
		b.rowFault("d1", 3, 10, t0.Add(day), t0.Add(29*day))
		b.act("d1", model.ActionSPPR, t0.Add(30*day), repairRow)
		b.in.Boots = append(b.in.Boots, model.Boot{Host: "h1", BootID: "boot-2", StartedAt: t0.Add(35 * day), StartQuality: model.TimeExact})
		b.rowFault("d1", 3, 4, t0.Add(40*day), asOf)
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictConsiderReplacement, RuleRecurred)
		if !has(checkCodes(v), "weakens check.sppr_volatile") {
			t.Errorf("checks = %q", checkCodes(v))
		}
		// Without the reboot the repair should have held.
		b.in.Boots = b.in.Boots[:1]
		if v := verdictOf(t, b, asOf, "d1"); has(checkCodes(v), "weakens check.sppr_volatile") {
			t.Errorf("checks without a reboot = %q", checkCodes(v))
		}
	})

	t.Run("a repair that names no place cannot show a recurrence", func(t *testing.T) {
		b := ruleBuilder()
		b.rowFault("d1", 3, 20, t0.Add(day), asOf)
		b.act("d1", model.ActionHPPR, t0.Add(30*day), nil)
		v := verdictOf(t, b, asOf, "d1")
		if has(v.Rules, RuleRecurred) || !has(checkCodes(v), "open check.repair_unlocated") {
			t.Errorf("rules %v, checks %q", v.Rules, checkCodes(v))
		}
	})
}

func TestRuleQuietAfterRepair(t *testing.T) {
	t.Run("a fault that is silent since its repair fires R-QUIET-01", func(t *testing.T) {
		b := ruleBuilder()
		b.rowFault("d1", 3, 10, t0.Add(day), t0.Add(29*day))
		b.act("d1", model.ActionHPPR, t0.Add(30*day), repairRow)
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictObserve, RuleQuiet)
		e := evidenceOf(t, v, RuleQuiet)
		if want := "row fault at rank0 dev7 bg0 bank3 row 0x1a2f: 30 errors until 2026-10-29, none in the 30 days since the hard PPR of 2026-10-31"; e.Text != want {
			t.Errorf("text = %q", e.Text)
		}
		if got := Text(i18n.KO, e.Statement); got != "row 고장(rank0 dev7 bg0 bank3 row 0x1a2f): 2026-10-29까지 30건, 2026-10-31 hPPR 뒤 30일 동안 0건" {
			t.Errorf("Korean text = %q", got)
		}
		// No record says the patrol scrubber reads this memory.
		if v.NextAction.Code != "next.observe" || !has(checkCodes(v), "open check.no_patrol") {
			t.Errorf("next %q, checks %q", v.NextAction.Code, checkCodes(v))
		}
	})

	t.Run("a cell whose page was taken offline and that is silent since fires it", func(t *testing.T) {
		b := ruleBuilder()
		for k := range 12 {
			e := severity(b.event("d1", t0.Add(time.Duration(k)*2*day), cellA), model.SevCE, model.ScrubPatrol)
			e.OsPhysicalAddress = model.Some(uint64(0x5040))
			b.add(e)
		}
		b.act("d1", model.ActionPageOffline, t0.Add(25*day), func(a *model.Action) { a.OsPhysicalAddress = model.Some(uint64(0x5000)) })
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictObserve, RuleQuiet)
		if e := evidenceOf(t, v, RuleQuiet); e.Args["kind"] != "@kind.page-offline" || e.Args["mode"] != "@mode.single-bit" {
			t.Errorf("evidence = %+v", e)
		}
		// The scrubber found errors here before: silence means something.
		if has(checkCodes(v), "open check.no_patrol") {
			t.Errorf("checks = %q", checkCodes(v))
		}
	})

	t.Run("a few days of silence are not enough", func(t *testing.T) {
		b := ruleBuilder()
		b.rowFault("d1", 3, 10, t0.Add(day), asOf.Add(-4*day))
		b.act("d1", model.ActionHPPR, asOf.Add(-3*day), repairRow)
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictObserve, RuleSoft)
		if !has(checkCodes(v), "open check.quiet_short") {
			t.Errorf("checks = %q", checkCodes(v))
		}
		cfg := DefaultConfig()
		cfg.QuietDays = 2
		d := analyze(t, b.in, Options{AsOf: asOf, Config: &cfg}).Host("h1").DIMM("d1")
		wantVerdict(t, d.Verdict, model.VerdictObserve, RuleQuiet)
	})

	t.Run("silence without a repair or page offline is not R-QUIET-01", func(t *testing.T) {
		b := ruleBuilder()
		b.rowFault("d1", 3, 10, t0.Add(day), t0.Add(29*day))
		wantVerdict(t, verdictOf(t, b, asOf, "d1"), model.VerdictObserve, RuleSoft)
	})

	t.Run("a single upset that was repaired is not a place that failed repeatedly", func(t *testing.T) {
		b := ruleBuilder()
		b.ce("d1", t0.Add(day), loc{0, 7, 0, 3, 0x1a2f, 64})
		b.act("d1", model.ActionHPPR, t0.Add(2*day), repairRow)
		wantVerdict(t, verdictOf(t, b, asOf, "d1"), model.VerdictObserve, RuleSoft)
	})
}

func TestRuleSoft(t *testing.T) {
	t.Run("stray errors at places that did not fail again fire R-SOFT-01", func(t *testing.T) {
		b := ruleBuilder()
		for i := range 3 {
			b.ce("d1", t0.Add(time.Duration(10+i*15)*day), loc{i % 2, 3 + i*5, i * 3, i, 1000 + i*7000, 64 * (i + 2)})
		}
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictObserve, RuleSoft)
		e := evidenceOf(t, v, RuleSoft)
		if want := "3 corrected errors at 3 places between 2026-10-11 and 2026-11-10; no place failed again"; e.Code != "soft.stray" || e.Text != want {
			t.Errorf("evidence = %q (%s)", e.Text, e.Code)
		}
		if got := Text(i18n.KO, e.Statement); got != "2026-10-11부터 2026-11-10까지 CE 3건, 위치 3곳, 반복된 위치 없음" {
			t.Errorf("Korean text = %q", got)
		}
		if v.NextAction.Code != "next.observe" || len(v.CounterEvidence) != 0 {
			t.Errorf("next %q, checks %q", v.NextAction.Code, checkCodes(v))
		}
	})

	t.Run("one stray error", func(t *testing.T) {
		b := ruleBuilder()
		b.ce("d1", t0.Add(10*day), cellA)
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictObserve, RuleSoft)
		if e := evidenceOf(t, v, RuleSoft); e.Text != "1 corrected error at 1 place on 2026-10-11; no place failed again" {
			t.Errorf("text = %q", e.Text)
		}
	})

	t.Run("a DIMM without errors is ok", func(t *testing.T) {
		b := newBuilderWith("d1")
		b.observedSince(t0)
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictOK)
		if len(v.Evidence) != 0 || len(v.CounterEvidence) != 0 || v.NextAction.Code != "" || v.Confidence != 85 {
			t.Errorf("finding = %+v", v)
		}
	})

	t.Run("it is not added to a finding that already asks for a replacement", func(t *testing.T) {
		b := ruleBuilder()
		b.rowFault("d1", 4, 5, asOf.Add(-3*day), asOf)
		b.ce("d1", t0.Add(5*day), loc{1, 2, 4, 1, 77, 8}) // a stray error
		wantVerdict(t, verdictOf(t, b, asOf, "d1"), model.VerdictConsiderReplacement, RuleRow)
		b.add(severity(b.event("d1", t0.Add(6*day), cellA), model.SevUE, model.ScrubDemand))
		wantVerdict(t, verdictOf(t, b, asOf, "d1"), model.VerdictReplaceNow, RuleUE, RuleRow)
	})

	t.Run("it stands next to R-QUIET-01 for the faults that rule does not cover", func(t *testing.T) {
		b := ruleBuilder()
		b.rowFault("d1", 3, 10, t0.Add(day), t0.Add(29*day))
		b.act("d1", model.ActionHPPR, t0.Add(30*day), repairRow)
		b.spreadOver(loc{1, 2, 4, 1, 77, 8}, 20, t0.Add(day), asOf)
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictObserve, RuleSoft, RuleQuiet)
		if got := codes(v); !equalStrings(got, []string{"R-SOFT-01 soft.cell", "R-QUIET-01 quiet.after"}) {
			t.Errorf("evidence = %q", got)
		}
	})

	t.Run("many small faults are summed up", func(t *testing.T) {
		b := ruleBuilder()
		// Seven weak cells, first seen months apart: no scatter fault.
		for i := range 7 {
			b.spreadOver(loc{i % 2, i, i, i % 4, 500 + i*8000, 64 * (i + 1)}, 3+i, t0.Add(-time.Duration(i)*50*day), asOf)
		}
		h := analyze(t, b.in, Options{AsOf: asOf}).Host("h1")
		d := h.DIMM("d1")
		if len(d.Faults) != 7 {
			t.Fatalf("faults:%s", describe(d.Faults))
		}
		v := d.Verdict
		wantVerdict(t, v, model.VerdictObserve, RuleSoft)
		got := codes(v)
		if len(got) != 5 || got[4] != "R-SOFT-01 soft.more" || got[0] != "R-SOFT-01 soft.cell" {
			t.Fatalf("evidence = %q", got)
		}
		if e := v.Evidence[4]; e.Args["faults"] != 3 || e.Args["n"] != int64(3+4+5) {
			t.Errorf("summary = %+v", e)
		}
	})

	t.Run("counts without locations", func(t *testing.T) {
		b := ruleBuilder()
		e := b.event("d1", t0.Add(20*day), loc{-1, -1, -1, -1, -1, -1})
		e.Kind, e.Count, e.WindowStart = model.KindCounter, 57, t0.Add(19*day)
		b.add(e)
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictObserve, RuleSoft)
		if e := evidenceOf(t, v, RuleSoft); e.Code != "soft.unlocated" || e.Text != "57 errors whose records carry no row or column: the fault mode cannot be determined" {
			t.Errorf("evidence = %q (%s)", e.Text, e.Code)
		}
		if v.NextAction.Code != "next.locate" {
			t.Errorf("next = %q", v.NextAction.Code)
		}
	})
}
