package engine

import (
	"testing"
	"time"

	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// The system rules look at more than one DIMM. Their fixtures use a host
// with two channels of two slots each: A1 and A2 on channel 0, B1 and B2
// on channel 1.

func channelBuilder(placed ...string) *builder {
	b := ruleBuilder()
	b.slot("A1", 0, 0)
	b.slot("A2", 0, 1)
	b.slot("B1", 1, 0)
	b.slot("B2", 1, 1)
	for _, s := range placed {
		b.place("d"+s, s, t0, asOf)
	}
	return b
}

func modesOf(t *testing.T, b *builder, uid string) []model.FaultMode {
	t.Helper()
	return modes(analyze(t, b.in, Options{AsOf: asOf}).Host("h1").DIMM(uid).Faults)
}

func TestRuleChannel(t *testing.T) {
	t.Run("unconfined errors on two DIMMs of a channel fire R-SYS-01 for both", func(t *testing.T) {
		b := channelBuilder("A1", "A2", "B1")
		b.scatter("dA1", "A1", 40, t0.Add(10*day), t0.Add(50*day))
		b.scatter("dA2", "A2", 30, t0.Add(12*day), t0.Add(55*day))
		res := analyze(t, b.in, Options{AsOf: asOf})
		h := res.Host("h1")
		for _, uid := range []string{"dA1", "dA2"} {
			d := h.DIMM(uid)
			wantVerdict(t, d.Verdict, model.VerdictHoldReplacement, RuleChannel)
			if len(d.Faults) != 1 || d.Faults[0].Mode != model.ModeSystemIssue {
				t.Errorf("%s: faults:%s", uid, describe(d.Faults))
			}
			if d.Verdict.NextAction.Code != "next.channel" {
				t.Errorf("%s: next = %q", uid, d.Verdict.NextAction.Code)
			}
		}
		e := evidenceOf(t, h.DIMM("dA1").Verdict, RuleChannel)
		if want := "A1 and A2 share one channel (socket 0 channel 0) and both have errors that are not confined to one place: 40 here, 30 there, between 2026-10-11 and 2026-11-24"; e.Text != want {
			t.Errorf("text = %q", e.Text)
		}
		if got := Text(i18n.KO, e.Statement); got != "같은 채널(socket 0 channel 0)의 A1에 40건, A2에 30건: 한 위치에 국한되지 않은 오류, 2026-10-11부터 2026-11-24까지" {
			t.Errorf("Korean text = %q", got)
		}
		if e.Total != 40 || len(e.Records) != 20 {
			t.Errorf("%d records, %d listed", e.Total, len(e.Records))
		}
		// The DIMM on the other channel is not involved, and the host
		// shows the hold first.
		wantVerdict(t, h.DIMM("dB1").Verdict, model.VerdictOK)
		if h.Grade != model.VerdictHoldReplacement || res.ExitCode() != 4 || h.DIMMs[0].Verdict.Grade != model.VerdictHoldReplacement {
			t.Errorf("host grade %s, exit code %d, first DIMM %s", h.Grade, res.ExitCode(), h.DIMMs[0].Slot)
		}
	})

	t.Run("hold replacement outranks an uncorrected error", func(t *testing.T) {
		b := channelBuilder("A1", "A2")
		b.scatter("dA1", "A1", 40, t0.Add(10*day), t0.Add(50*day))
		b.scatter("dA2", "A2", 30, t0.Add(12*day), t0.Add(55*day))
		b.add(severity(b.in1("dA1", "A1", t0.Add(30*day), loc{0, 4, 2, 1, 999, 64}), model.SevUE, model.ScrubDemand))
		v := verdictOf(t, b, asOf, "dA1")
		wantVerdict(t, v, model.VerdictHoldReplacement, RuleChannel, RuleUE)
		if v.Evidence[0].Rule != RuleChannel || v.NextAction.Code != "next.channel" {
			t.Errorf("evidence %q, next %q", codes(v), v.NextAction.Code)
		}
	})

	t.Run("counts without locations on both DIMMs fire it: counts are not confined", func(t *testing.T) {
		b := channelBuilder("A1", "A2")
		for i, uid := range []string{"dA1", "dA2"} {
			e := b.in1(uid, []string{"A1", "A2"}[i], t0.Add(40*day), loc{-1, -1, -1, -1, -1, -1})
			e.Kind, e.Count, e.WindowStart = model.KindCounter, 50, t0.Add(30*day)
			b.add(e)
		}
		v := verdictOf(t, b, asOf, "dA1")
		wantVerdict(t, v, model.VerdictHoldReplacement, RuleChannel)
		if !has(missingCodes(v), "missing.location") {
			t.Errorf("missing = %q", missingCodes(v))
		}
	})

	t.Run("a neighbour whose errors stay in one cell does not", func(t *testing.T) {
		b := channelBuilder("A1", "A2")
		b.scatter("dA1", "A1", 40, t0.Add(10*day), t0.Add(50*day))
		for k := range 30 {
			b.add(b.in1("dA2", "A2", t0.Add(time.Duration(12+k)*day), cellA))
		}
		v := verdictOf(t, b, asOf, "dA1")
		wantVerdict(t, v, model.VerdictConsiderReplacement, RuleRow)
		if !has(checkCodes(v), "supports check.neighbors_confined") {
			t.Errorf("checks = %q", checkCodes(v))
		}
		if got := modesOf(t, b, "dA1"); len(got) != 1 || got[0] != model.ModeDIMM {
			t.Errorf("modes = %v", got)
		}
		wantVerdict(t, verdictOf(t, b, asOf, "dA2"), model.VerdictObserve, RuleSoft)
	})

	t.Run("a neighbour with a few stray errors does not", func(t *testing.T) {
		b := channelBuilder("A1", "A2")
		b.scatter("dA1", "A1", 40, t0.Add(10*day), t0.Add(50*day))
		b.scatter("dA2", "A2", 4, t0.Add(12*day), t0.Add(55*day))
		v := verdictOf(t, b, asOf, "dA1")
		wantVerdict(t, v, model.VerdictConsiderReplacement, RuleRow)
		if !has(checkCodes(v), "open check.neighbors_few") {
			t.Errorf("checks = %q", checkCodes(v))
		}
		// The threshold is configuration.
		cfg := DefaultConfig()
		cfg.SystemMinErrors = 4
		d := analyze(t, b.in, Options{AsOf: asOf, Config: &cfg}).Host("h1").DIMM("dA1")
		wantVerdict(t, d.Verdict, model.VerdictHoldReplacement, RuleChannel)
	})

	t.Run("errors in different periods do not", func(t *testing.T) {
		b := channelBuilder("A1", "A2")
		b.scatter("dA1", "A1", 40, t0.Add(1*day), t0.Add(10*day))
		b.scatter("dA2", "A2", 30, t0.Add(40*day), t0.Add(50*day))
		for _, uid := range []string{"dA1", "dA2"} {
			if v := verdictOf(t, b, asOf, uid); has(v.Rules, RuleChannel) {
				t.Errorf("%s: rules = %v", uid, v.Rules)
			}
		}
	})

	t.Run("DIMMs on different channels do not", func(t *testing.T) {
		b := channelBuilder("A1", "B1")
		b.scatter("dA1", "A1", 40, t0.Add(10*day), t0.Add(50*day))
		b.scatter("dB1", "B1", 30, t0.Add(12*day), t0.Add(55*day))
		v := verdictOf(t, b, asOf, "dA1")
		wantVerdict(t, v, model.VerdictConsiderReplacement, RuleRow)
		// A2 is populated according to the slot table and has no errors.
		if !has(checkCodes(v), "supports check.neighbors_clean") {
			t.Errorf("checks = %q", checkCodes(v))
		}
	})

	t.Run("the same channel number on another socket is another channel", func(t *testing.T) {
		b := channelBuilder("A1")
		b.in.Slots = append(b.in.Slots, model.Slot{Host: "h1", Name: "X1", Socket: model.Some(1), Channel: model.Some(0), Position: model.Some(0), Populated: model.Some(true)})
		b.place("dX1", "X1", t0, asOf)
		b.scatter("dA1", "A1", 40, t0.Add(10*day), t0.Add(50*day))
		b.scatter("dX1", "X1", 30, t0.Add(12*day), t0.Add(55*day))
		if v := verdictOf(t, b, asOf, "dA1"); has(v.Rules, RuleChannel) {
			t.Errorf("rules = %v", v.Rules)
		}
	})

	t.Run("without channel information the neighbours cannot be checked", func(t *testing.T) {
		b := ruleBuilder()
		b.place("dA1", "A1", t0, asOf)
		b.place("dA2", "A2", t0, asOf)
		b.scatter("dA1", "A1", 40, t0.Add(10*day), t0.Add(50*day))
		b.scatter("dA2", "A2", 30, t0.Add(12*day), t0.Add(55*day))
		v := verdictOf(t, b, asOf, "dA1")
		wantVerdict(t, v, model.VerdictConsiderReplacement, RuleRow)
		if !has(checkCodes(v), "open check.channel_unknown") {
			t.Errorf("checks = %q", checkCodes(v))
		}
	})

	t.Run("a fault inside the DIMM speaks against the channel", func(t *testing.T) {
		b := channelBuilder("A1", "A2")
		b.scatter("dA1", "A1", 40, t0.Add(10*day), t0.Add(50*day))
		b.scatter("dA2", "A2", 30, t0.Add(12*day), t0.Add(55*day))
		for k := range 24 {
			b.add(b.in1("dA1", "A1", t0.Add(time.Duration(10+k)*day), loc{1, 9, 6, 2, 4242, 64 * (1 + k%4)}))
		}
		res := analyze(t, b.in, Options{AsOf: asOf})
		d := res.Host("h1").DIMM("dA1")
		// The hold stays; the row fault keeps its mode and its own rule.
		if d.Verdict.Grade != model.VerdictHoldReplacement || d.Verdict.Rules[0] != RuleChannel || !has(checkCodes(d.Verdict), "weakens check.confined") {
			t.Errorf("finding = %s %v, checks %q", d.Verdict.Grade, d.Verdict.Rules, checkCodes(d.Verdict))
		}
		var row, system int
		for _, f := range d.Faults {
			switch f.Mode {
			case model.ModeRow:
				row++
			case model.ModeSystemIssue:
				system++
			}
		}
		if row != 1 || system != 1 {
			t.Errorf("faults:%s", describe(d.Faults))
		}
	})

	t.Run("errors on many DIMMs of a host are mentioned as a possible common cause", func(t *testing.T) {
		b := channelBuilder("A1", "B1")
		b.in.Slots = append(b.in.Slots, model.Slot{Host: "h1", Name: "C1", Socket: model.Some(0), Channel: model.Some(2), Position: model.Some(0), Populated: model.Some(true)})
		b.place("dC1", "C1", t0, asOf)
		for i, s := range []string{"A1", "B1", "C1"} {
			b.scatter("d"+s, s, 30+i, t0.Add(10*day), t0.Add(50*day))
		}
		v := verdictOf(t, b, asOf, "dA1")
		wantVerdict(t, v, model.VerdictConsiderReplacement, RuleRow)
		var hw *model.Check
		for i := range v.CounterEvidence {
			if v.CounterEvidence[i].Code == "check.host_wide" {
				hw = &v.CounterEvidence[i]
			}
		}
		if hw == nil || hw.Effect != model.EffectWeakens || hw.Text != "2 other DIMMs of this host have unconfined errors in the same period (B1, C1): a common cause is possible" {
			t.Errorf("checks = %q", checkCodes(v))
		}
		// With two DIMMs only, the others being silent supports the finding.
		b = channelBuilder("A1", "B1")
		b.scatter("dA1", "A1", 30, t0.Add(10*day), t0.Add(50*day))
		if v := verdictOf(t, b, asOf, "dA1"); !has(checkCodes(v), "supports check.host_clean") || has(checkCodes(v), "weakens check.host_wide") {
			t.Errorf("checks = %q", checkCodes(v))
		}
	})
}

// swapBuilder builds a host on which the DIMM of slot A1 was changed on day
// 21: "old" sat there before, "new" after. Where each of them was at other
// times is up to the caller.
func swapBuilder() *builder {
	b := ruleBuilder()
	b.slot("A1", 0, 0)
	b.slot("B1", 1, 0)
	b.place("old", "A1", t0, t0.Add(20*day))
	b.place("new", "A1", t0.Add(21*day), asOf)
	return b
}

func TestRuleSlotKeepsErrors(t *testing.T) {
	t.Run("errors that stay in the slot after a swap fire R-SYS-02 for both DIMMs", func(t *testing.T) {
		b := swapBuilder()
		b.place("new", "B1", t0, t0.Add(20*day)) // the two DIMMs were swapped
		b.place("old", "B1", t0.Add(21*day), asOf)
		b.scatter("old", "A1", 20, t0.Add(5*day), t0.Add(19*day))
		b.scatter("new", "A1", 25, t0.Add(22*day), t0.Add(55*day))
		res := analyze(t, b.in, Options{AsOf: asOf})
		h := res.Host("h1")
		newD, oldD := h.DIMM("new"), h.DIMM("old")
		wantVerdict(t, newD.Verdict, model.VerdictHoldReplacement, RuleSlot)
		wantVerdict(t, oldD.Verdict, model.VerdictHoldReplacement, RuleSlot)
		e := evidenceOf(t, newD.Verdict, RuleSlot)
		if want := "the errors stayed in slot A1 when its DIMM was changed on 2026-10-22: 20 with DIMM SN-old before, 25 with DIMM SN-new after"; e.Text != want {
			t.Errorf("text = %q", e.Text)
		}
		if got := Text(i18n.KO, e.Statement); got != "2026-10-22 DIMM을 바꾼 뒤에도 슬롯 A1에 오류 지속: 바꾸기 전 SN-old 20건, 바꾼 뒤 SN-new 25건" {
			t.Errorf("Korean text = %q", got)
		}
		// The DIMM that left is silent in its new slot, which is stated.
		if got := codes(oldD.Verdict); !equalStrings(got, []string{"R-SYS-02 sys.stays", "R-SYS-02 sys.left_quiet"}) {
			t.Errorf("evidence of the DIMM that left = %q", got)
		}
		if oldD.Slot != "B1" || !oldD.Present || oldD.Verdict.NextAction.Code != "next.slot" || oldD.Verdict.NextAction.Args["slot"] != "A1" {
			t.Errorf("DIMM that left: slot %s, next %+v", oldD.Slot, oldD.Verdict.NextAction)
		}
		for _, d := range []*DIMMResult{newD, oldD} {
			if d.Faults[0].Mode != model.ModeSystemIssue {
				t.Errorf("%s: faults:%s", d.UID, describe(d.Faults))
			}
		}
		if h.Grade != model.VerdictHoldReplacement {
			t.Errorf("host grade = %s", h.Grade)
		}
	})

	t.Run("a replacement that fails in the same slot fires it; the returned DIMM is listed as gone", func(t *testing.T) {
		b := swapBuilder()
		b.scatter("old", "A1", 20, t0.Add(5*day), t0.Add(19*day))
		b.scatter("new", "A1", 25, t0.Add(22*day), t0.Add(55*day))
		h := analyze(t, b.in, Options{AsOf: asOf}).Host("h1")
		wantVerdict(t, h.DIMM("new").Verdict, model.VerdictHoldReplacement, RuleSlot)
		old := h.DIMM("old")
		wantVerdict(t, old.Verdict, model.VerdictHoldReplacement, RuleSlot)
		if old.Present || old.Verdict.NextAction.Code != "next.removed" || len(old.Verdict.Evidence) != 1 {
			t.Errorf("returned DIMM: present %v, next %q, evidence %q", old.Present, old.Verdict.NextAction.Code, codes(old.Verdict))
		}
	})

	t.Run("errors that follow the DIMM to its new slot do not", func(t *testing.T) {
		b := swapBuilder()
		b.place("old", "B1", t0.Add(21*day), asOf)
		b.scatter("old", "A1", 20, t0.Add(5*day), t0.Add(19*day))
		b.scatter("old", "B1", 20, t0.Add(22*day), t0.Add(55*day))
		b.scatter("new", "A1", 25, t0.Add(22*day), t0.Add(55*day))
		for _, uid := range []string{"old", "new"} {
			if v := verdictOf(t, b, asOf, uid); has(v.Rules, RuleSlot) {
				t.Errorf("%s: rules = %v", uid, v.Rules)
			}
		}
		v := verdictOf(t, b, asOf, "old")
		wantVerdict(t, v, model.VerdictConsiderReplacement, RuleRow)
		var followed *model.Check
		for i := range v.CounterEvidence {
			if v.CounterEvidence[i].Code == "check.followed" {
				followed = &v.CounterEvidence[i]
			}
		}
		if followed == nil || followed.Effect != model.EffectSupports || followed.Text != "the errors followed the DIMM from A1 to B1: 20 before the move, 20 after" {
			t.Errorf("checks = %q", checkCodes(v))
		}
	})

	t.Run("a DIMM that brought its errors along does not", func(t *testing.T) {
		b := swapBuilder()
		b.place("new", "B1", t0, t0.Add(20*day))
		b.scatter("old", "A1", 20, t0.Add(5*day), t0.Add(19*day))
		b.scatter("new", "B1", 10, t0.Add(5*day), t0.Add(19*day))
		b.scatter("new", "A1", 25, t0.Add(22*day), t0.Add(55*day))
		for _, uid := range []string{"old", "new"} {
			if v := verdictOf(t, b, asOf, uid); has(v.Rules, RuleSlot) {
				t.Errorf("%s: rules = %v", uid, v.Rules)
			}
		}
	})

	t.Run("faults inside the two DIMMs do not", func(t *testing.T) {
		b := swapBuilder()
		for k := range 20 {
			b.add(b.in1("old", "A1", t0.Add(time.Duration(k)*day), loc{0, 7, 0, 3, 0x1a2f, 64 * (1 + k%4)}))
			b.add(b.in1("new", "A1", t0.Add(time.Duration(22+k)*day), cellA))
		}
		for _, uid := range []string{"old", "new"} {
			if v := verdictOf(t, b, asOf, uid); has(v.Rules, RuleSlot) {
				t.Errorf("%s: rules = %v", uid, v.Rules)
			}
		}
	})

	t.Run("too few errors on one side do not", func(t *testing.T) {
		b := swapBuilder()
		b.scatter("old", "A1", 4, t0.Add(5*day), t0.Add(19*day))
		b.scatter("new", "A1", 25, t0.Add(22*day), t0.Add(55*day))
		for _, uid := range []string{"old", "new"} {
			if v := verdictOf(t, b, asOf, uid); has(v.Rules, RuleSlot) {
				t.Errorf("%s: rules = %v", uid, v.Rules)
			}
		}
	})
}

func TestRuleReseat(t *testing.T) {
	t.Run("errors that stop at a reseat fire R-SYS-03", func(t *testing.T) {
		b := ruleBuilder()
		b.scatter("d1", "S-d1", 20, t0.Add(5*day), t0.Add(25*day))
		a := b.act("d1", model.ActionReseat, t0.Add(30*day), nil)
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictHoldReplacement, RuleReseat)
		e := evidenceOf(t, v, RuleReseat)
		if want := "20 errors before the reseat of 2026-10-31, none in the 30 days since"; e.Text != want {
			t.Errorf("text = %q", e.Text)
		}
		if got := Text(i18n.KO, e.Statement); got != "2026-10-31 reseat 전 20건, 이후 30일 동안 0건" {
			t.Errorf("Korean text = %q", got)
		}
		if e.Records[0] != (model.RecordRef{Kind: model.RecordAction, ID: a.ID, RawRef: a.RawRef}) || e.Total != 21 {
			t.Errorf("records: %d, first %+v", e.Total, e.Records[0])
		}
		if v.NextAction.Code != "next.contact" || !has(checkCodes(v), "open check.no_patrol") {
			t.Errorf("next %q, checks %q", v.NextAction.Code, checkCodes(v))
		}
		// Errors that a contact explains are a system issue.
		if got := modesOf(t, b, "d1"); len(got) != 1 || got[0] != model.ModeSystemIssue {
			t.Errorf("modes = %v", got)
		}
	})

	t.Run("errors that stop when the DIMM is moved fire it: a move reseats the DIMM", func(t *testing.T) {
		b := ruleBuilder()
		b.slot("A1", 0, 0)
		b.slot("B1", 1, 0)
		b.place("mover", "A1", t0, t0.Add(20*day))
		b.place("mover", "B1", t0.Add(21*day), asOf)
		b.place("other", "A1", t0.Add(21*day), asOf)
		b.scatter("mover", "A1", 20, t0.Add(5*day), t0.Add(19*day))
		v := verdictOf(t, b, asOf, "mover")
		wantVerdict(t, v, model.VerdictHoldReplacement, RuleReseat)
		e := evidenceOf(t, v, RuleReseat)
		if want := "20 errors in A1 before the move to B1 on 2026-10-22, none in either slot in the 39 days since"; e.Code != "sys.moved_quiet" || e.Text != want {
			t.Errorf("evidence = %q (%s)", e.Text, e.Code)
		}
		if got := Text(i18n.KO, e.Statement); got != "2026-10-22 슬롯 이동(A1 → B1) 전 20건, 이후 39일 동안 두 슬롯 모두 0건" {
			t.Errorf("Korean text = %q", got)
		}
		wantVerdict(t, verdictOf(t, b, asOf, "other"), model.VerdictOK)
	})

	t.Run("a fault inside the DIMM before the reseat speaks against the contact", func(t *testing.T) {
		b := ruleBuilder()
		b.rowFault("d1", 4, 5, t0.Add(5*day), t0.Add(25*day))
		b.act("d1", model.ActionReseat, t0.Add(30*day), nil)
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictHoldReplacement, RuleReseat)
		if !has(checkCodes(v), "weakens check.confined") {
			t.Errorf("checks = %q", checkCodes(v))
		}
		// The row fault stays a row fault.
		if got := modesOf(t, b, "d1"); len(got) != 1 || got[0] != model.ModeRow {
			t.Errorf("modes = %v", got)
		}
	})

	t.Run("a stray error after the reseat does not undo it", func(t *testing.T) {
		b := ruleBuilder()
		b.scatter("d1", "S-d1", 20, t0.Add(5*day), t0.Add(25*day))
		b.act("d1", model.ActionReseat, t0.Add(30*day), nil)
		b.ce("d1", t0.Add(40*day), cellA)
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictHoldReplacement, RuleReseat)
		if e := evidenceOf(t, v, RuleReseat); e.Code != "sys.reseat_few" || e.Text != "20 errors before the reseat of 2026-10-31, 1 in the 30 days since" {
			t.Errorf("evidence = %q (%s)", e.Text, e.Code)
		}
	})

	for _, tt := range []struct {
		name  string
		build func(b *builder)
	}{
		{"errors that go on after the reseat", func(b *builder) {
			b.scatter("d1", "S-d1", 20, t0.Add(5*day), t0.Add(25*day))
			b.act("d1", model.ActionReseat, t0.Add(30*day), nil)
			b.scatter("d1", "S-d1", 5, t0.Add(40*day), t0.Add(50*day))
		}},
		{"errors after the reseat that are no small part of those before", func(b *builder) {
			b.scatter("d1", "S-d1", 20, t0.Add(5*day), t0.Add(25*day))
			b.act("d1", model.ActionReseat, t0.Add(30*day), nil)
			b.scatter("d1", "S-d1", 3, t0.Add(40*day), t0.Add(50*day))
		}},
		{"a reseat three days ago", func(b *builder) {
			b.scatter("d1", "S-d1", 20, t0.Add(5*day), t0.Add(25*day))
			b.act("d1", model.ActionReseat, asOf.Add(-3*day), nil)
		}},
		{"too few errors before the reseat", func(b *builder) {
			b.scatter("d1", "S-d1", 4, t0.Add(5*day), t0.Add(25*day))
			b.act("d1", model.ActionReseat, t0.Add(30*day), nil)
		}},
		{"a reseat that is on record as failed", func(b *builder) {
			b.scatter("d1", "S-d1", 20, t0.Add(5*day), t0.Add(25*day))
			b.act("d1", model.ActionReseat, t0.Add(30*day), func(a *model.Action) { a.Result = model.ResultFailed })
		}},
		{"no reseat and no move", func(b *builder) {
			b.scatter("d1", "S-d1", 20, t0.Add(5*day), t0.Add(25*day))
		}},
	} {
		t.Run(tt.name+" do not fire it", func(t *testing.T) {
			b := ruleBuilder()
			tt.build(b)
			if v := verdictOf(t, b, asOf, "d1"); has(v.Rules, RuleReseat) || v.Grade == model.VerdictHoldReplacement {
				t.Errorf("finding = %s %v", v.Grade, v.Rules)
			}
		})
	}

	t.Run("a move after which the old slot keeps failing is R-SYS-02", func(t *testing.T) {
		b := swapBuilder()
		b.place("old", "B1", t0.Add(21*day), asOf)
		b.scatter("old", "A1", 20, t0.Add(5*day), t0.Add(19*day))
		b.scatter("new", "A1", 25, t0.Add(22*day), t0.Add(55*day))
		wantVerdict(t, verdictOf(t, b, asOf, "old"), model.VerdictHoldReplacement, RuleSlot)
	})
}

// mappingBuilder builds a host with two slots that EDAC and SMBIOS both
// know, and a DIMM with errors in CPU0_E1.
func mappingBuilder(p store.EventPlace) *builder {
	b := ruleBuilder()
	b.in.host("h1")
	b.in.Slots = []model.Slot{
		{Host: "h1", Name: "CPU0_C1", Locator: "CPU0_C1", SMBIOSHandle: model.Some(0x1104), Socket: model.Some(0), IMC: model.Some(1), Channel: model.Some(0),
			Position: model.Some(0), EdacMC: model.Some(1), EdacLocation: "0:0", EdacLabel: "CPU_SrcID#0_MC#1_Chan#0_DIMM#0", Populated: model.Some(true)},
		{Host: "h1", Name: "CPU0_E1", Locator: "CPU0_E1", SMBIOSHandle: model.Some(0x1108), Socket: model.Some(0), IMC: model.Some(2), Channel: model.Some(0),
			Position: model.Some(0), EdacMC: model.Some(2), EdacLocation: "0:0", EdacLabel: "CPU_SrcID#0_MC#2_Chan#0_DIMM#0", Populated: model.Some(true)},
	}
	b.place("dC1", "CPU0_C1", t0, asOf)
	b.place("dE1", "CPU0_E1", t0, asOf)
	for k := range 12 {
		b.add(b.in1("dE1", "CPU0_E1", t0.Add(time.Duration(10+k)*day), cellA))
	}
	p.Host, p.Slot, p.Records, p.FirstID, p.LastID = "h1", "CPU0_E1", 12, 1, 12
	b.in.Places = []store.EventPlace{p}
	return b
}

func TestRuleMapping(t *testing.T) {
	// The records were tied to CPU0_E1 through their EDAC position.
	position := store.LocationKey{EdacMC: model.Some(2), EdacLocation: "0:0"}

	t.Run("records tied to one slot that carry the label of another fire R-MAP-01", func(t *testing.T) {
		p := position
		p.Label = "CPU0_C1"
		b := mappingBuilder(store.EventPlace{LocationKey: p})
		h := analyze(t, b.in, Options{AsOf: asOf}).Host("h1")
		v := h.DIMM("dE1").Verdict
		wantVerdict(t, v, model.VerdictHoldReplacement, RuleMapping, RuleSoft)
		e := evidenceOf(t, v, RuleMapping)
		if want := "12 error records tied to CPU0_E1 carry a label (CPU0_C1) that belongs to CPU0_C1"; e.Text != want {
			t.Errorf("text = %q", e.Text)
		}
		if got := Text(i18n.KO, e.Statement); got != "CPU0_E1에 묶인 오류 기록 12건에 CPU0_C1의 라벨(CPU0_C1) 포함" {
			t.Errorf("Korean text = %q", got)
		}
		if e.Total != 12 || len(e.Records) != 2 || e.Records[0].ID != 1 || e.Records[1].ID != 12 || v.NextAction.Code != "next.mapping" {
			t.Errorf("records %+v, next %q", e.Records, v.NextAction.Code)
		}
		// The DIMM of the slot that is named has no finding of its own; it
		// is told that some of those errors may be its own.
		c := h.DIMM("dC1").Verdict
		wantVerdict(t, c, model.VerdictOK)
		if got := checkCodes(c); !equalStrings(got, []string{"open check.named_by_other"}) ||
			c.CounterEvidence[0].Text != "12 error records tied to CPU0_E1 carry a label of this slot: some errors of CPU0_E1 may belong here" {
			t.Errorf("checks of the named slot = %q %q", got, c.CounterEvidence[0].Text)
		}
		if h.Grade != model.VerdictHoldReplacement {
			t.Errorf("host grade = %s", h.Grade)
		}
	})

	for _, tt := range []struct {
		name string
		key  store.LocationKey
		code string
	}{
		{"an SMBIOS handle", store.LocationKey{EdacMC: model.Some(2), EdacLocation: "0:0", SMBIOSHandle: model.Some(0x1104)}, "@name.handle"},
		{"an EDAC position", store.LocationKey{Label: "CPU0_E1", EdacMC: model.Some(1), EdacLocation: "0:0"}, "@name.edac"},
		{"socket and channel numbers", store.LocationKey{Label: "CPU0_E1", SocketId: model.Some(0), IMC: model.Some(1), ChannelId: model.Some(0), ChannelSlotId: model.Some(0)}, "@name.topology"},
	} {
		t.Run(tt.name+" of another slot fires it", func(t *testing.T) {
			b := mappingBuilder(store.EventPlace{LocationKey: tt.key})
			v := verdictOf(t, b, asOf, "dE1")
			if v.Grade != model.VerdictHoldReplacement || v.Rules[0] != RuleMapping {
				t.Fatalf("finding = %s %v", v.Grade, v.Rules)
			}
			if e := evidenceOf(t, v, RuleMapping); e.Args["name"] != tt.code || e.Args["other"] != "CPU0_C1" {
				t.Errorf("evidence = %+v", e)
			}
		})
	}

	for _, tt := range []struct {
		name string
		key  store.LocationKey
	}{
		{"names that all belong to the slot", store.LocationKey{Label: "cpu0_e1", EdacMC: model.Some(2), EdacLocation: "0:0", SMBIOSHandle: model.Some(0x1108),
			SocketId: model.Some(0), IMC: model.Some(2), ChannelId: model.Some(0), ChannelSlotId: model.Some(0)}},
		{"the default label of the slot's EDAC position", store.LocationKey{Label: "CPU_SrcID#0_MC#2_Chan#0_DIMM#0", EdacMC: model.Some(2), EdacLocation: "0:0"}},
		{"a label that says EDAC could not place the error", store.LocationKey{Label: "unknown memory", EdacMC: model.Some(2), EdacLocation: "0:0"}},
		{"a label no slot is called", store.LocationKey{Label: "DIMM_X9", EdacMC: model.Some(2), EdacLocation: "0:0"}},
		{"numbers no slot has", store.LocationKey{EdacMC: model.Some(2), EdacLocation: "0:0", SocketId: model.Some(3), IMC: model.Some(0), ChannelId: model.Some(5), ChannelSlotId: model.Some(1)}},
	} {
		t.Run(tt.name+" do not fire it", func(t *testing.T) {
			b := mappingBuilder(store.EventPlace{LocationKey: tt.key})
			wantVerdict(t, verdictOf(t, b, asOf, "dE1"), model.VerdictObserve, RuleSoft)
		})
	}

	t.Run("it is found in a ledger", func(t *testing.T) {
		s := newStore(t)
		err := s.Update(ctx, func(w *store.Writer) error {
			for _, sl := range mappingBuilder(store.EventPlace{}).in.Slots {
				if err := w.UpsertSlot(&sl); err != nil {
					return err
				}
			}
			if err := w.UpsertDIMM(&model.DIMM{UID: "dE1", Identity: model.IdentitySerial, Serial: "E1E1E1"}); err != nil {
				return err
			}
			for k := range 12 {
				// rasdaemon rows: the layers say mc2 0:0, the label says CPU0_C1.
				e := &model.Event{Host: "h1", ErrorDT: t0.Add(time.Duration(10+k) * day), ErrorSeverity: model.SevCE, Source: "rasdaemon/mc_event",
					RawRef: "mc_event/" + string(rune('a'+k)), Fingerprint: "e" + string(rune('a'+k)), Label: "CPU0_C1", EdacMC: model.Some(2), EdacLocation: "0:0",
					Slot: "CPU0_E1", DIMMUID: "dE1", DIMMLink: model.LinkObserved, Rank: model.Some(0), Bank: model.Some(2), Row: model.Some(5000), Column: model.Some(640)}
				if _, err := w.InsertEvent(e); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		res, err := Run(ctx, s, Options{Now: computedAt})
		if err != nil {
			t.Fatal(err)
		}
		v := res.Host("h1").DIMM("dE1").Verdict
		wantVerdict(t, v, model.VerdictHoldReplacement, RuleMapping, RuleSoft)
		// Run looked up where the original records are.
		if r := evidenceOf(t, v, RuleMapping).Records; len(r) != 2 || r[0].RawRef != "mc_event/a" || r[1].RawRef != "mc_event/l" {
			t.Errorf("records = %+v", r)
		}
	})
}

// TestChannelsWithinAController: where slot rows name the memory
// controller, channels are counted within it.
func TestChannelsWithinAController(t *testing.T) {
	b := ruleBuilder()
	b.in.host("h1")
	for _, s := range []struct {
		name          string
		imc, position int
	}{{"X1", 2, 0}, {"X2", 2, 1}, {"Y1", 3, 0}} {
		b.in.Slots = append(b.in.Slots, model.Slot{Host: "h1", Name: s.name, Socket: model.Some(0), IMC: model.Some(s.imc), Channel: model.Some(0),
			Position: model.Some(s.position), Populated: model.Some(true)})
		b.place("d"+s.name, s.name, t0, asOf)
	}
	b.scatter("dX1", "X1", 20, t0.Add(10*day), t0.Add(40*day))
	b.scatter("dY1", "Y1", 20, t0.Add(10*day), t0.Add(40*day))
	// Channel 0 of controller 2 and channel 0 of controller 3 are two channels.
	if v := verdictOf(t, b, asOf, "dX1"); has(v.Rules, RuleChannel) {
		t.Errorf("rules = %v", v.Rules)
	}
	b.scatter("dX2", "X2", 20, t0.Add(10*day), t0.Add(40*day))
	v := verdictOf(t, b, asOf, "dX1")
	wantVerdict(t, v, model.VerdictHoldReplacement, RuleChannel)
	if e := evidenceOf(t, v, RuleChannel); e.Args["channel"] != "socket 0 controller 2 channel 0" {
		t.Errorf("channel = %v", e.Args["channel"])
	}
	wantVerdict(t, verdictOf(t, b, asOf, "dY1"), model.VerdictConsiderReplacement, RuleRow)
}

func TestDIMMName(t *testing.T) {
	d := &DIMMResult{DIMM: &model.DIMM{Serial: "80A1C3"}}
	if got := dimmName(d, "d878e1a15b6e30a44057"); got != "80A1C3" {
		t.Errorf("with a serial number: %q", got)
	}
	// Without a serial number the start of the ledger identity stands in.
	for _, d := range []*DIMMResult{nil, {}, {DIMM: &model.DIMM{}}} {
		if got := dimmName(d, "s878e1a15b6e30a44057"); got != "s878e1a1" {
			t.Errorf("without a serial number: %q", got)
		}
	}
	if got := dimmName(nil, "old"); got != "old" {
		t.Errorf("short identity: %q", got)
	}
}

// TestMappingConflictsOfTwoSlots: every slot whose records name another
// slot is flagged, in a fixed order.
func TestMappingConflictsOfTwoSlots(t *testing.T) {
	b := mappingBuilder(store.EventPlace{LocationKey: store.LocationKey{Label: "CPU0_C1", EdacMC: model.Some(2), EdacLocation: "0:0"}})
	for k := range 6 {
		b.add(b.in1("dC1", "CPU0_C1", t0.Add(time.Duration(20+k)*day), cellA))
	}
	b.in.Places = append(b.in.Places, store.EventPlace{Host: "h1", Slot: "CPU0_C1", FirstID: 13, LastID: 18,
		LocationKey: store.LocationKey{Label: "CPU0_E1", EdacMC: model.Some(1), EdacLocation: "0:0", Records: 6}})
	h := analyze(t, b.in, Options{AsOf: asOf}).Host("h1")
	for _, uid := range []string{"dC1", "dE1"} {
		v := h.DIMM(uid).Verdict
		if v.Grade != model.VerdictHoldReplacement || v.Rules[0] != RuleMapping || !has(checkCodes(v), "open check.named_by_other") {
			t.Errorf("%s: %s %v, checks %q", uid, v.Grade, v.Rules, checkCodes(v))
		}
	}
}
