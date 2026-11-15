package ingest

import (
	"slices"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/redfish"
	"github.com/agenticode/my/internal/adapter/sel"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/testutil"
)

// findings runs the engine on a ledger and returns the finding per slot.
func findings(t *testing.T, res *engine.Result, host string) map[string]*model.Verdict {
	t.Helper()
	h := res.Host(host)
	if h == nil {
		t.Fatalf("no result for host %s", host)
	}
	out := map[string]*model.Verdict{}
	for _, d := range h.DIMMs {
		out[d.Slot] = d.Verdict
	}
	return out
}

// TestBMCDumpReachesTheRules reads what a BMC serves and nothing else: no
// host log, no SMBIOS. The module the BMC reports as failed and out of use
// must come out with the rule about firmware marks, tied to its serial
// number.
func TestBMCDumpReachesTheRules(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	dump := redfish.New(testutil.Testdata(t, "redfish", "bmc-tree"), redfish.Options{Now: now})
	run, err := Run(ctx, s, []adapter.Adapter{dump}, Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	// The host name comes from the system resource of the dump.
	if run.Host != "r12-n034" {
		t.Errorf("host = %q, want the name in the dump", run.Host)
	}
	src := run.Sources[0]
	if src.Kind != "redfish" || src.Status != model.SourceOK || src.Events != 7 || src.Actions != 3 || src.GradeB != 3 || src.GradeC != 4 {
		t.Errorf("source inventory entry = %+v", src)
	}

	res, err := engine.Run(ctx, s, engine.Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := findings(t, res, "r12-n034")
	b1 := got["DIMM_B1"]
	if b1 == nil {
		t.Fatalf("no finding for DIMM_B1; slots: %v", got)
	}
	for _, rule := range []string{engine.RuleFirmware, engine.RuleUE, engine.RuleRepairGone} {
		if !slices.Contains(b1.Rules, rule) {
			t.Errorf("DIMM_B1: rules %v lack %s", b1.Rules, rule)
		}
	}
	if b1.Grade != model.VerdictReplaceNow {
		t.Errorf("DIMM_B1: grade %s", b1.Grade)
	}
	d, err := s.DIMM(ctx, b1.DIMMUID)
	if err != nil || d == nil || d.Serial != "80A1C3" || d.MemoryType != "DDR5" {
		t.Errorf("DIMM of the finding = %+v, %v", d, err)
	}
	// The other modules carry no firmware mark.
	for _, slot := range []string{"DIMM_A1", "DIMM_C1"} {
		v := got[slot]
		if v == nil {
			t.Errorf("no finding for %s", slot)
			continue
		}
		if slices.Contains(v.Rules, engine.RuleFirmware) || v.Grade == model.VerdictReplaceNow {
			t.Errorf("%s: grade %s, rules %v", slot, v.Grade, v.Rules)
		}
	}
}

// TestSELDisableReachesTheRules does the same for a SEL dump: the module
// the firmware took out of use after an uncorrected error.
func TestSELDisableReachesTheRules(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	log := sel.NewFile(testutil.Testdata(t, "sel", "ipmitool-elist.txt"), sel.Options{})
	if _, err := Run(ctx, s, []adapter.Adapter{log}, Options{Host: "h1", Now: now}); err != nil {
		t.Fatal(err)
	}
	res, err := engine.Run(ctx, s, engine.Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := findings(t, res, "h1")
	c1 := got["DIMM_C1"]
	if c1 == nil || !slices.Contains(c1.Rules, engine.RuleFirmware) || !slices.Contains(c1.Rules, engine.RuleUE) || c1.Grade != model.VerdictReplaceNow {
		t.Errorf("DIMM_C1 = %+v", c1)
	}
	// DIMM_G1 was taken out of use and came back: no firmware mark stands.
	if g1 := got["DIMM_G1"]; g1 != nil && slices.Contains(g1.Rules, engine.RuleFirmware) {
		t.Errorf("DIMM_G1: rules %v", g1.Rules)
	}
	// The spare that took over for DIMM_F1 is a repair resource used up.
	if f1 := got["DIMM_F1"]; f1 == nil || !slices.Contains(f1.Rules, engine.RuleRepairGone) {
		t.Errorf("DIMM_F1 = %+v", f1)
	}
}
