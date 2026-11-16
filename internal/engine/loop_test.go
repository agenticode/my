package engine

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
)

// The fixtures of the boots: the host of channelBuilder (A1, A2 on channel
// 0, B1, B2 on channel 1, all of socket 0) with n boots, one a day from
// t0, each running for an hour. They replace the boot that makes the
// ledger cover the host from t0.

func bootName(k int) string { return fmt.Sprintf("boot-%02d", k) }

func bootStart(k int) time.Time { return t0.Add(time.Duration(k-1) * day) }

func loopBuilder(n int, placed ...string) *builder {
	b := channelBuilder(placed...)
	b.in.Boots = nil
	for k := 1; k <= n; k++ {
		b.in.Boots = append(b.in.Boots, model.Boot{Host: "h1", BootID: bootName(k), StartedAt: bootStart(k),
			StartQuality: model.TimeExact, LastSeen: bootStart(k).Add(time.Hour), BIOS: "2.4.4"})
	}
	return b
}

// ended sets how boots ended.
func (b *builder) ended(end string, ks ...int) {
	for _, k := range ks {
		b.in.Boots[k-1].End = end
	}
}

// ueOf adds a fatal uncorrected error of DIMM uid in a slot, half an hour
// into boot k.
func (b *builder) ueOf(uid, slot string, k int, l loc) *model.Event {
	e := severity(b.in1(uid, slot, bootStart(k).Add(30*time.Minute), l), model.SevFatal, "")
	e.BootID = bootName(k)
	return e
}

// ueIn adds such an error of the DIMM "d"+slot.
func (b *builder) ueIn(slot string, k int, l loc) {
	b.add(b.ueOf("d"+slot, slot, k, l))
}

var (
	loopCellA   = loc{0, 7, 0, 3, 0x1a2f, 0x238}
	loopCellB   = loc{1, 2, 1, 0, 0x3300, 0x40}
	loopNoPlace = loc{-1, -1, -1, -1, -1, -1}
)

func addrOnly(addr uint64) func(*model.Event) {
	return func(e *model.Event) { e.OsPhysicalAddress = model.Some(addr) }
}

func TestRuleLoopPlace(t *testing.T) {
	t.Run("one cell in three boots that each stopped fires R-LOOP-01 (grade A)", func(t *testing.T) {
		b := loopBuilder(3, "A1", "A2", "B1")
		for k := 1; k <= 3; k++ {
			b.ueIn("A1", k, loopCellA)
		}
		b.ended(model.BootStop, 1, 2, 3)
		h := hostOf(t, b)
		v := h.DIMM("dA1").Verdict
		wantVerdict(t, v, model.VerdictReplaceNow, RuleUE, RuleLoopPlace)
		e := evidenceOf(t, v, RuleLoopPlace)
		if want := "uncorrected errors at rank0 dev7 bg0 bank3 row 0x1a2f col 0x238 in 3 of the last 3 boots: each of these boots ended in a stop"; e.Text != want {
			t.Errorf("text = %q", e.Text)
		}
		if got := Text(i18n.KO, e.Statement); got != "최근 부팅 3회 중 3회 rank0 dev7 bg0 bank3 row 0x1a2f col 0x238에서 UE: 해당 부팅 모두 중단으로 끝남" {
			t.Errorf("Korean text = %q", got)
		}
		if e.Total != 3 {
			t.Errorf("%d records behind the statement", e.Total)
		}
		if !has(checkCodes(v), "supports check.loop_alone") || v.NextAction.Code != "next.loop_place" {
			t.Errorf("checks %q, next %q", checkCodes(v), v.NextAction.Code)
		}
		r := h.Recurrence
		if r == nil || r.Window() != 3 || r.Stops != 3 || r.Streak != 3 || r.Latest != 3 || r.Where != RuleLoopPlace {
			t.Fatalf("recurrence %+v", r)
		}
		for _, bs := range r.Boots {
			if bs.StopSlot != "A1" || bs.RanSeconds != 3600 {
				t.Errorf("boot %d: stop slot %q, ran %d s", bs.Index, bs.StopSlot, bs.RanSeconds)
			}
		}
		db := r.DIMM("dA1")
		if db == nil || db.BootsWithUE != 3 || db.StopBoots != 3 || len(db.Places) != 1 || db.Places[0].Level != PlaceCell {
			t.Fatalf("boots of dA1: %+v", db)
		}
		var lines []string
		for _, st := range r.Statements {
			lines = append(lines, Text(i18n.EN, st))
		}
		want := []string{
			"3 of the last 3 boots ended in a stop, the latest on 2026-10-03 01:00 UTC, 60 min after its boot",
			"3 stops with an uncorrected error of A1 (R-LOOP-01)",
		}
		if !equalStrings(lines, want) {
			t.Errorf("stops lines = %q", lines)
		}
		// The other DIMMs are not involved.
		wantVerdict(t, h.DIMM("dA2").Verdict, model.VerdictOK)
	})

	t.Run("one address in two boots fires it (grade B); a change of the BIOS between them speaks against it", func(t *testing.T) {
		b := loopBuilder(3, "A1", "A2")
		b.add(withEvent(b.ueOf("dA1", "A1", 1, loopNoPlace), addrOnly(0x12345640)))
		b.add(withEvent(b.ueOf("dA1", "A1", 3, loopNoPlace), addrOnly(0x12345640)))
		b.ended(model.BootStop, 1)
		v := verdictOf(t, b, asOf, "dA1")
		wantVerdict(t, v, model.VerdictReplaceNow, RuleUE, RuleLoopPlace)
		if e := evidenceOf(t, v, RuleLoopPlace); e.Text != "uncorrected errors at 0x12345640 in 2 of the last 3 boots: some of these boots ended in a stop" {
			t.Errorf("text = %q", e.Text)
		}
		if has(checkCodes(v), "weakens check.loop_changed") {
			t.Errorf("checks %q with one BIOS", checkCodes(v))
		}
		b.in.Boots[1].BIOS, b.in.Boots[2].BIOS = "2.5.1", "2.5.1"
		v = verdictOf(t, b, asOf, "dA1")
		if !has(checkCodes(v), "weakens check.loop_changed") {
			t.Errorf("checks %q after a BIOS update", checkCodes(v))
		}
	})

	t.Run("one boot is not a loop", func(t *testing.T) {
		b := loopBuilder(3, "A1", "A2")
		b.ueIn("A1", 2, loopCellA)
		b.ended(model.BootStop, 2)
		h := hostOf(t, b)
		wantVerdict(t, h.DIMM("dA1").Verdict, model.VerdictReplaceNow, RuleUE)
		r := h.Recurrence
		if r.Where != "dimm" || r.Stops != 1 || r.Streak != 0 || len(r.Statements) != 2 || r.Statements[1].Code != "loop.where_one" {
			t.Errorf("recurrence: where %q, stops %d, streak %d, statements %q", r.Where, r.Stops, r.Streak, r.Statements)
		}
	})

	t.Run("a threshold of three boots needs three", func(t *testing.T) {
		b := loopBuilder(3, "A1")
		b.ueIn("A1", 1, loopCellA)
		b.ueIn("A1", 2, loopCellA)
		cfg := DefaultConfig()
		cfg.LoopMinBoots = 3
		v := analyze(t, b.in, Options{AsOf: asOf, Config: &cfg}).Host("h1").DIMM("dA1").Verdict
		wantVerdict(t, v, model.VerdictReplaceNow, RuleUE)
	})
}

func withEvent(e *model.Event, mut func(*model.Event)) *model.Event {
	mut(e)
	return e
}

func TestRuleLoopDIMM(t *testing.T) {
	t.Run("the DIMM only, in two boots (grade C)", func(t *testing.T) {
		b := loopBuilder(2, "A1", "A2")
		b.ueIn("A1", 1, loopNoPlace)
		b.ueIn("A1", 2, loopNoPlace)
		b.ended(model.BootStop, 1, 2)
		v := verdictOf(t, b, asOf, "dA1")
		wantVerdict(t, v, model.VerdictReplaceNow, RuleUE, RuleLoopDIMM)
		if e := evidenceOf(t, v, RuleLoopDIMM); e.Code != "loop.dimm_only" || e.Text != "uncorrected errors in 2 of the last 2 boots, at no place that repeats: each of these boots ended in a stop" {
			t.Errorf("statement %s: %q", e.Code, e.Text)
		}
		if !has(checkCodes(v), "open check.loop_not_ruled_out") || v.NextAction.Code != "next.loop_dimm" {
			t.Errorf("checks %q, next %q", checkCodes(v), v.NextAction.Code)
		}
	})

	t.Run("two cells in two boots", func(t *testing.T) {
		b := loopBuilder(4, "A1", "A2")
		b.ueIn("A1", 2, loopCellA)
		b.ueIn("A1", 4, loopCellB)
		v := verdictOf(t, b, asOf, "dA1")
		wantVerdict(t, v, model.VerdictReplaceNow, RuleUE, RuleLoopDIMM)
		e := evidenceOf(t, v, RuleLoopDIMM)
		if e.Text != "uncorrected errors in 2 of the last 4 boots, at 2 different places: none of these boots is on record as a stop" {
			t.Errorf("text = %q", e.Text)
		}
		if got := Text(i18n.KO, e.Statement); got != "최근 부팅 4회 중 2회 UE, 위치는 2곳으로 매번 다름: 해당 부팅 중 중단 기록 없음" {
			t.Errorf("Korean text = %q", got)
		}
	})

	t.Run("two boots on two DIMMs fire no rule; each names the other", func(t *testing.T) {
		b := loopBuilder(3, "A1", "B1")
		b.ueIn("A1", 1, loopCellA)
		b.ueIn("B1", 3, loopCellB)
		b.ended(model.BootStop, 1, 3)
		h := hostOf(t, b)
		for _, x := range [][2]string{{"dA1", "B1"}, {"dB1", "A1"}} {
			v := h.DIMM(x[0]).Verdict
			wantVerdict(t, v, model.VerdictReplaceNow, RuleUE)
			found := false
			for _, c := range v.CounterEvidence {
				if c.Code == "check.loop_other" && c.Effect == model.EffectWeakens && c.Args["slot"] == x[1] {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: checks %q", x[0], checkCodes(v))
			}
		}
		r := h.Recurrence
		if r.Where != "dimm" || r.Statements[len(r.Statements)-1].Text != "uncorrected errors on 2 DIMMs (A1, B1): too few boots each for a rule of the boots" {
			t.Errorf("where %q, statements %q", r.Where, r.Statements)
		}
		if r.Boots[0].StopSlot != "A1" || r.Boots[2].StopSlot != "B1" || r.Streak != 1 {
			t.Errorf("boots %+v", r.Boots)
		}
	})
}

func TestRuleLoopSpread(t *testing.T) {
	t.Run("two DIMMs of one channel, two boots each", func(t *testing.T) {
		b := loopBuilder(4, "A1", "A2", "B1")
		b.ueIn("A1", 1, loopCellA)
		b.ueIn("A2", 2, loopCellB)
		b.ueIn("A1", 3, loopCellB)
		b.ueIn("A2", 4, loopCellA)
		b.ended(model.BootStop, 1, 2, 3, 4)
		h := hostOf(t, b)
		for _, uid := range []string{"dA1", "dA2"} {
			v := h.DIMM(uid).Verdict
			wantVerdict(t, v, model.VerdictHoldReplacement, RuleLoopSpread, RuleUE, RuleLoopDIMM)
			var texts []string
			for _, e := range v.Evidence {
				if e.Rule == RuleLoopSpread {
					texts = append(texts, e.Text)
				}
			}
			want := []string{"uncorrected errors in 4 of the last 4 boots, spread over 2 DIMMs", "these DIMMs are all on one channel (socket 0 channel 0)"}
			if !equalStrings(texts, want) || v.NextAction.Code != "next.loop_spread" {
				t.Errorf("%s: R-LOOP-03 says %q, next %q", uid, texts, v.NextAction.Code)
			}
		}
		wantVerdict(t, h.DIMM("dB1").Verdict, model.VerdictOK)
		r := h.Recurrence
		if r.Where != RuleLoopSpread || r.Statements[1].Text != "the uncorrected errors are spread over 2 DIMMs (A1, A2), R-LOOP-03" {
			t.Errorf("where %q, statements %q", r.Where, r.Statements)
		}
		if h.Grade != model.VerdictHoldReplacement {
			t.Errorf("host grade %s", h.Grade)
		}
	})

	t.Run("three DIMMs of one socket, one boot each", func(t *testing.T) {
		b := loopBuilder(3, "A1", "A2", "B1")
		b.ueIn("A1", 1, loopCellA)
		b.ueIn("B1", 2, loopCellA)
		b.ueIn("A2", 3, loopCellA)
		h := hostOf(t, b)
		for _, uid := range []string{"dA1", "dA2", "dB1"} {
			v := h.DIMM(uid).Verdict
			wantVerdict(t, v, model.VerdictHoldReplacement, RuleLoopSpread, RuleUE)
			if !has(codes(v), RuleLoopSpread+" loop.shared_socket") {
				t.Errorf("%s: %q", uid, codes(v))
			}
			// Three DIMMs spread: no check of one other DIMM.
			if has(checkCodes(v), "weakens check.loop_other") {
				t.Errorf("%s: checks %q", uid, checkCodes(v))
			}
		}
	})

	t.Run("DIMMs on two sockets, and DIMMs of no known channel", func(t *testing.T) {
		b := loopBuilder(3, "A1", "B1")
		b.in.Slots = append(b.in.Slots, model.Slot{Host: "h1", Name: "C1", Locator: "C1", Socket: model.Some(1), Channel: model.Some(0),
			Position: model.Some(0), Populated: model.Some(true)})
		b.place("dC1", "C1", t0, asOf)
		b.ueIn("A1", 1, loopCellA)
		b.ueIn("B1", 2, loopCellA)
		b.ueIn("C1", 3, loopCellA)
		v := verdictOf(t, b, asOf, "dC1")
		if !has(codes(v), RuleLoopSpread+" loop.shared_sockets") {
			t.Errorf("%q", codes(v))
		}

		b = loopBuilder(3)
		for _, s := range []string{"X1", "X2", "X3"} {
			b.in.Slots = append(b.in.Slots, model.Slot{Host: "h1", Name: s, Locator: s, Populated: model.Some(true)})
			b.place("d"+s, s, t0, asOf)
		}
		for k, s := range []string{"X1", "X2", "X3"} {
			b.ueIn(s, k+1, loopCellA)
		}
		if v := verdictOf(t, b, asOf, "dX2"); !has(codes(v), RuleLoopSpread+" loop.shared_unknown") {
			t.Errorf("%q", codes(v))
		}
	})

	t.Run("a share above the threshold is no spread", func(t *testing.T) {
		b := loopBuilder(3, "A1", "A2")
		b.ueIn("A1", 1, loopCellA)
		b.ueIn("A1", 2, loopCellA)
		b.ueIn("A2", 3, loopCellA)
		h := hostOf(t, b)
		wantVerdict(t, h.DIMM("dA1").Verdict, model.VerdictReplaceNow, RuleUE, RuleLoopPlace)
		wantVerdict(t, h.DIMM("dA2").Verdict, model.VerdictReplaceNow, RuleUE)
		// The error of dA2 was in a boot of its own: in the boots of dA1
		// no other DIMM had one.
		if !has(checkCodes(h.DIMM("dA1").Verdict), "supports check.loop_alone") {
			t.Errorf("checks %q", checkCodes(h.DIMM("dA1").Verdict))
		}
	})

	t.Run("a confined fault of its own from before speaks against the spread", func(t *testing.T) {
		b := loopBuilder(3, "A1", "A2", "B1")
		b.rowFault("dA1", 4, 3, t0.Add(-20*day), t0.Add(-10*day))
		b.ueIn("A1", 1, loopCellB)
		b.ueIn("A2", 2, loopCellB)
		b.ueIn("B1", 3, loopCellB)
		v := verdictOf(t, b, asOf, "dA1")
		if !has(checkCodes(v), "weakens check.loop_own_fault") {
			t.Errorf("checks %q", checkCodes(v))
		}
	})
}

func TestLoopWindow(t *testing.T) {
	t.Run("the window holds the latest 16 boots", func(t *testing.T) {
		b := loopBuilder(20, "A1")
		b.ueIn("A1", 1, loopCellA)
		b.ueIn("A1", 2, loopCellA)
		h := hostOf(t, b)
		wantVerdict(t, h.DIMM("dA1").Verdict, model.VerdictReplaceNow, RuleUE)
		r := h.Recurrence
		if r.Window() != 16 || r.OnRecord != 20 || r.Boots[0].BootID != bootName(5) || r.Boots[15].Index != 16 || r.DIMM("dA1") != nil {
			t.Errorf("window %d of %d, first %s", r.Window(), r.OnRecord, r.Boots[0].BootID)
		}

		b = loopBuilder(20, "A1")
		b.ueIn("A1", 19, loopCellA)
		b.ueIn("A1", 20, loopCellA)
		v := verdictOf(t, b, asOf, "dA1")
		if e := evidenceOf(t, v, RuleLoopPlace); e.Args["of"] != 16 || e.Args["n"] != 2 {
			t.Errorf("args %v", e.Args)
		}
	})

	t.Run("boots after the reference time and boots of no known start are left out", func(t *testing.T) {
		b := loopBuilder(3, "A1")
		b.in.Boots = append(b.in.Boots, model.Boot{Host: "h1", BootID: "later", StartedAt: asOf.Add(day), StartQuality: model.TimeExact},
			model.Boot{Host: "h1", BootID: "relative", StartedAt: time.Unix(5, 0), StartQuality: model.TimeRelative})
		if r := hostOf(t, b).Recurrence; r.Window() != 3 {
			t.Errorf("window %d", r.Window())
		}
	})

	t.Run("a stop streak counts the latest boots; a running boot is passed over", func(t *testing.T) {
		b := loopBuilder(5, "A1")
		b.ended(model.BootStop, 1, 3, 4)
		b.ended(model.BootPanic, 2)
		b.in.Scans = append(b.in.Scans, model.Scan{ID: 1, Host: "h1", Kind: model.ScanLive, StartedAt: bootStart(5).Add(time.Minute)})
		r := hostOf(t, b).Recurrence
		if r.Stops != 3 || r.Panics != 1 || r.Streak != 2 || r.Latest != 4 || r.Boots[4].End != "running" {
			t.Errorf("stops %d, panics %d, streak %d, latest %d, last end %q", r.Stops, r.Panics, r.Streak, r.Latest, r.Boots[4].End)
		}
		if r.Where != "none" || r.Statements[len(r.Statements)-1].Code != "loop.where_none" {
			t.Errorf("where %q, statements %q", r.Where, r.Statements)
		}
	})
}

func TestLoopDIMMThatLeft(t *testing.T) {
	// dOld sat in A1 for the first two boots and stopped both; dNew took
	// its place.
	b := loopBuilder(4, "A2")
	b.place("dOld", "A1", t0, bootStart(2).Add(2*time.Hour))
	b.place("dNew", "A1", bootStart(3), asOf)
	b.add(b.ueOf("dOld", "A1", 1, loopCellA))
	b.add(b.ueOf("dOld", "A1", 2, loopCellA))
	b.ended(model.BootStop, 1, 2)
	h := hostOf(t, b)
	old := h.DIMM("dOld")
	if old == nil || old.Present {
		t.Fatalf("dOld: %+v", old)
	}
	wantVerdict(t, old.Verdict, model.VerdictReplaceNow, RuleUE, RuleLoopPlace)
	if old.Verdict.NextAction.Code != "next.removed" {
		t.Errorf("next %q", old.Verdict.NextAction.Code)
	}
	wantVerdict(t, h.DIMM("dNew").Verdict, model.VerdictOK)
	if h.Grade == model.VerdictReplaceNow || h.Recurrence.Streak != 0 {
		t.Errorf("host grade %s, streak %d", h.Grade, h.Recurrence.Streak)
	}
}

func TestLoopFaultBoots(t *testing.T) {
	b := loopBuilder(4, "A1")
	for k := 1; k <= 4; k++ {
		for c := range 3 {
			e := b.in1("dA1", "A1", bootStart(k).Add(time.Duration(10+c)*time.Minute), loc{0, 7, 0, 3, 0x1a2f, 64 * (1 + c)})
			e.BootID = bootName(k)
			b.add(e)
		}
	}
	v := verdictOf(t, b, asOf, "dA1")
	found := false
	for _, e := range v.Evidence {
		if e.Code == "loop.fault_boots" {
			found = true
			if e.Args["n"] != 4 || e.Args["of"] != 4 {
				t.Errorf("args %v", e.Args)
			}
		}
	}
	if !found {
		t.Errorf("evidence %q", codes(v))
	}
}

func TestLoopHostFinding(t *testing.T) {
	t.Run("an address of no DIMM in two boots", func(t *testing.T) {
		b := loopBuilder(3)
		for _, k := range []int{1, 3} {
			e := b.loose(bootStart(k).Add(time.Minute), model.SevFatal, 0x7f001040)
			e.BootID = bootName(k)
			b.add(e)
		}
		b.ended(model.BootStop, 1, 3)
		h := hostOf(t, b)
		if h.Loose == nil {
			t.Fatal("no host finding")
		}
		if !has(codes(h.Loose), RuleHostUE+" host.loop_address") {
			t.Errorf("evidence %q", codes(h.Loose))
		}
		for _, x := range h.Loose.Evidence {
			if x.Code == "host.loop_address" && x.Text != "uncorrected errors at the address 0x7f001040 in 2 of the last 3 boots" {
				t.Errorf("text = %q", x.Text)
			}
		}
		if h.Recurrence.Where != "none" || h.Recurrence.Loose[0].BootsWithUE != 2 {
			t.Errorf("where %q, loose %+v", h.Recurrence.Where, h.Recurrence.Loose)
		}
	})

	t.Run("stops that name no DIMM list what a reset loses", func(t *testing.T) {
		b := loopBuilder(2)
		e := b.loose(bootStart(1).Add(time.Minute), model.SevFatal, 0)
		e.BootID = bootName(1)
		b.add(e)
		b.ended(model.BootStop, 1)
		b.in.Captures = []model.CaptureCheck{{Host: "h1", Name: "pstore-backend", State: "lost"}, {Host: "h1", Name: "journal", State: "kept"},
			{Host: "h1", Name: "mce-bootlog", State: "lost"}, {Host: "h2", Name: "kdump", State: "lost"}}
		h := hostOf(t, b)
		var got []string
		for _, m := range h.Loose.Missing {
			if m.Code == "missing.capture_lost" {
				got = append(got, m.Text, Text(i18n.KO, m))
			}
		}
		want := []string{"my preflight found what a reset loses on this host: pstore-backend, mce-bootlog; a stop there may leave no record",
			"my preflight 결과 이 호스트에서 reset 때 잃는 항목: pstore-backend, mce-bootlog. 중단이 기록 없이 지나갈 수 있음"}
		if !equalStrings(got, want) {
			t.Errorf("missing = %q", got)
		}
	})
}

// TestLoopInputOrder: the order in which boots and records reach the
// engine does not change what it finds.
func TestLoopInputOrder(t *testing.T) {
	build := func(rng *rand.Rand) (*Input, *builder) {
		b := loopBuilder(8, "A1", "A2", "B1", "B2")
		slots := []string{"A1", "A2", "B1", "B2"}
		places := []loc{loopCellA, loopCellB, loopNoPlace}
		var evs []*model.Event
		for k := 1; k <= 8; k++ {
			if k%3 == 0 {
				continue
			}
			s := slots[(k*7)%4]
			evs = append(evs, b.ueOf("d"+s, s, k, places[k%3]))
			e := b.in1("d"+s, s, bootStart(k).Add(40*time.Minute), loopCellA)
			e.BootID = bootName(k)
			evs = append(evs, e)
		}
		b.ended(model.BootStop, 1, 2, 4, 5, 7, 8)
		b.ended(model.BootPanic, 3)
		if rng != nil {
			rng.Shuffle(len(evs), func(i, j int) { evs[i], evs[j] = evs[j], evs[i] })
			rng.Shuffle(len(b.in.Boots), func(i, j int) { b.in.Boots[i], b.in.Boots[j] = b.in.Boots[j], b.in.Boots[i] })
		}
		for _, e := range evs {
			b.add(e)
		}
		return b.in, b
	}
	in, _ := build(nil)
	want, err := json.Marshal(analyze(t, in, Options{AsOf: asOf}).Host("h1"))
	if err != nil {
		t.Fatal(err)
	}
	for seed := range 20 {
		in, _ := build(rand.New(rand.NewPCG(uint64(seed), 3)))
		got, _ := json.Marshal(analyze(t, in, Options{AsOf: asOf}).Host("h1"))
		if string(got) != string(want) {
			t.Fatalf("seed %d: the result depends on the order of the input", seed)
		}
	}
}
