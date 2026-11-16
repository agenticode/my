package engine

import (
	"testing"

	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// evidenceAll returns the evidence of a rule in its order.
func evidenceAll(v *model.Verdict, rule string) []model.Evidence {
	var out []model.Evidence
	for _, e := range v.Evidence {
		if e.Rule == rule {
			out = append(out, e)
		}
	}
	return out
}

func TestUncorrectedErrorThatStoppedTheHost(t *testing.T) {
	t.Run("the finding of the DIMM says it", func(t *testing.T) {
		b := ruleBuilder()
		first := severity(b.event("d1", t0.Add(40*day), cellA), model.SevUE, model.ScrubDemand)
		fatal := severity(b.event("d1", t0.Add(50*day), cellA), model.SevFatal, model.ScrubDemand)
		b.add(first)
		b.add(fatal)
		b.in.Stops = []store.Stop{{Host: "h1", BootID: "b1", EventID: fatal.ID, StopID: 900, Source: "klog/stop"}}
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictReplaceNow, RuleUE)
		ev := evidenceAll(v, RuleUE)
		if len(ev) != 2 || ev[0].Code != "ue.located" || ev[1].Code != "ue.stopped" {
			t.Fatalf("evidence %+v", ev)
		}
		st := ev[1]
		if st.Text != "the uncorrected error of 2026-11-20 00:00 UTC stopped the host" || st.Total != 2 || len(st.Records) != 2 ||
			st.Records[0] != (model.RecordRef{Kind: model.RecordEvent, ID: fatal.ID}) || st.Records[1] != (model.RecordRef{Kind: model.RecordEvent, ID: 900}) {
			t.Errorf("statement %+v", st)
		}
		if got := Text(i18n.KO, st.Statement); got != "2026-11-20 00:00 UTC의 UE로 호스트가 중단됨" {
			t.Errorf("Korean %q", got)
		}
	})

	t.Run("several stops", func(t *testing.T) {
		b := ruleBuilder()
		x := severity(b.event("d1", t0.Add(40*day), cellA), model.SevFatal, model.ScrubDemand)
		y := severity(b.event("d1", t0.Add(50*day), cellA), model.SevFatal, model.ScrubDemand)
		b.add(x)
		b.add(y)
		// The second boot stopped and the next boot showed it: no record of
		// the kernel's lines.
		b.in.Stops = []store.Stop{{Host: "h1", BootID: "b1", EventID: x.ID, StopID: 900}, {Host: "h1", BootID: "b2", EventID: y.ID}}
		ev := evidenceAll(verdictOf(t, b, asOf, "d1"), RuleUE)
		if len(ev) != 2 || ev[1].Code != "ue.stopped_many" || ev[1].Text != "2 of these uncorrected errors stopped the host, the latest on 2026-11-20 00:00 UTC" || ev[1].Total != 3 {
			t.Errorf("evidence %+v", ev)
		}
	})

	t.Run("a stop no error record shows says nothing about the DIMM", func(t *testing.T) {
		b := ruleBuilder()
		ue := severity(b.event("d1", t0.Add(50*day), cellA), model.SevUE, model.ScrubDemand)
		b.add(ue)
		b.in.Stops = []store.Stop{{Host: "h1", BootID: "b1", StopID: 900}}
		if ev := evidenceAll(verdictOf(t, b, asOf, "d1"), RuleUE); len(ev) != 1 {
			t.Errorf("evidence %+v", ev)
		}
	})

	t.Run("the host finding says it", func(t *testing.T) {
		b := ruleBuilder()
		e := b.loose(t0.Add(10*day), model.SevFatal, 0x12345040)
		b.add(e)
		b.in.Stops = []store.Stop{{Host: "h1", BootID: "b1", EventID: e.ID, StopID: 900}}
		ev := evidenceAll(hostOf(t, b).Loose, RuleHostUE)
		if len(ev) != 2 || ev[1].Code != "ue.stopped" || ev[1].Text != "the uncorrected error of 2026-10-11 00:00 UTC stopped the host" {
			t.Errorf("evidence %+v", ev)
		}
	})
}
