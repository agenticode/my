package dcbrain

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/testutil"
)

// importServer imports one server of the fixture into a new ledger and
// analyses it as of the given day of the dataset calendar.
func importServer(t *testing.T, sid string, asOfDay int) (*ingest.Result, *engine.HostResult) {
	t.Helper()
	ctx := context.Background()
	d, err := Open(testutil.Testdata(t, "dcbrain"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	asOf := StampOfDay(asOfDay).Time(DefaultBase)
	res, err := ingest.Run(ctx, s, []adapter.Adapter{NewServer(d, sid, Options{})}, ingest.Options{Now: asOf})
	if err != nil {
		t.Fatal(err)
	}
	out, err := engine.Run(ctx, s, engine.Options{AsOf: asOf, Now: asOf})
	if err != nil {
		t.Fatal(err)
	}
	h := out.Host(sid)
	if h == nil {
		t.Fatalf("no result for host %s; hosts: %+v", sid, out.Hosts)
	}
	return res, h
}

func TestImportIntoLedger(t *testing.T) {
	// s0002 has a growing row fault on DIMM0 until day 70.
	res, h := importServer(t, "s0002", 71)
	if res.Host != "s0002" || len(res.Sources) != 1 || res.Sources[0].Status != model.SourceOK || res.Sources[0].Kind != Kind {
		t.Fatalf("ingest result: %+v", res)
	}
	if src := res.Sources[0]; src.GradeA == 0 || src.GradeB != 0 || src.GradeC != 0 {
		t.Errorf("grades of the source: A %d, B %d, C %d", src.GradeA, src.GradeB, src.GradeC)
	}
	d := h.InSlot("DIMM0")
	if d == nil {
		t.Fatalf("no DIMM in slot DIMM0: %+v", h.DIMMs)
	}
	if d.UID != model.SlotUID("s0002", "DIMM0") || d.Features.SlotOnly == 0 {
		t.Errorf("DIMM identity %s, slot-only errors %d; want the slot placeholder", d.UID, d.Features.SlotOnly)
	}
	if len(d.Faults) == 0 || d.Faults[0].Mode != model.ModeRow || d.Faults[0].Grade != model.GradeA {
		t.Fatalf("faults = %+v", d.Faults)
	}
	f := d.Faults[0]
	if !f.Rank.Valid || f.Rank.V != 1 || !f.Bank.Valid || f.Bank.V != 9 || f.DeviceId.Valid || !f.TopRow.Valid || f.TopRow.V != 0x1a2f {
		t.Errorf("place of the fault: rank %v bank %v device %v row %v", f.Rank, f.Bank, f.DeviceId, f.TopRow)
	}
	if d.Features.Scrub.Patrol == 0 || d.Features.Scrub.Demand == 0 {
		t.Errorf("scrub split = %+v", d.Features.Scrub)
	}
	if d.Verdict.Grade != model.VerdictConsiderReplacement || len(d.Verdict.Rules) == 0 || d.Verdict.Rules[0] != engine.RuleRow {
		t.Errorf("finding = %s %v", d.Verdict.Grade, d.Verdict.Rules)
	}
	// The finding says what the dataset lacks.
	missing := map[string]bool{}
	for _, m := range d.Verdict.Missing {
		missing[m.Code] = true
	}
	for _, code := range []string{"missing.inventory", "missing.device", "missing.bits"} {
		if !missing[code] {
			t.Errorf("the finding does not list %s as missing: %+v", code, d.Verdict.Missing)
		}
	}

	// s0006 has scattered errors on both DIMMs of one channel.
	_, h = importServer(t, "s0006", 207)
	for _, slot := range []string{"DIMM4", "DIMM5"} {
		d := h.InSlot(slot)
		if d == nil {
			t.Fatalf("no DIMM in slot %s", slot)
		}
		if d.Verdict.Grade != model.VerdictHoldReplacement || d.Verdict.Rules[0] != engine.RuleChannel {
			t.Errorf("%s: finding = %s %v, want hold replacement by %s", slot, d.Verdict.Grade, d.Verdict.Rules, engine.RuleChannel)
		}
	}

	// Importing the same server again adds nothing.
	ctx := context.Background()
	ds, _ := Open(testutil.Testdata(t, "dcbrain"))
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	first, err := ingest.Run(ctx, s, []adapter.Adapter{NewServer(ds, "s0008", Options{})}, ingest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := ingest.Run(ctx, s, []adapter.Adapter{NewServer(ds, "s0008", Options{})}, ingest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Events != 5 || second.Events != 0 {
		t.Errorf("events added: %d, then %d; want 5, then 0", first.Events, second.Events)
	}
	tot, err := s.Totals(ctx, store.EventFilter{Host: "s0008"})
	if err != nil {
		t.Fatal(err)
	}
	var ce int64
	for _, x := range tot {
		ce += x.CE
	}
	if ce != 9 {
		t.Errorf("the ledger counts %d corrected errors of s0008, want 9", ce)
	}
}
