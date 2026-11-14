package engine

import (
	"math/rand/v2"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/synth"
)

// wantMode is the mode the classifier gives a generated fault. A fault that
// comes from the slot or the channel looks like scatter over the whole DIMM
// when one DIMM is looked at alone; the system checks relabel it later.
func wantMode(m model.FaultMode) model.FaultMode {
	if m == model.ModeSystemIssue {
		return model.ModeDIMM
	}
	return m
}

func eqOpt(t *testing.T, what string, got, want model.Opt[int]) {
	t.Helper()
	if want.Valid && got != want {
		t.Errorf("%s = %s, want %s", what, got, want)
	}
}

// TestScenarioTruth classifies every built-in scenario of the generator and
// compares the result with what was generated: the mode of each fault, its
// place, and the number of errors in it.
func TestScenarioTruth(t *testing.T) {
	for _, name := range synth.Names() {
		for seed := uint64(1); seed <= 6; seed++ {
			ds := generate(t, name, seed, nil)
			c := Classify(load(t, ledgerOf(t, ds)), Options{})
			if len(c.Unattributed) != 0 {
				t.Errorf("%s/%d: unattributed errors %+v", name, seed, c.Unattributed)
			}
			truthDIMM := map[string]bool{}
			for _, tr := range ds.Truth {
				uid := uidOf(tr.Serial)
				truthDIMM[uid] = true
				if ds.Scenario.Faults[tr.Fault].StaysWithSlot && len(ds.Scenario.Moves) > 0 {
					continue // the errors are split over two DIMMs; see TestErrorsThatStayInTheSlot
				}
				fs := faultsOf(c, uid)
				if len(fs) == 0 {
					t.Errorf("%s/%d: no fault on the DIMM of %+v", name, seed, tr)
					continue
				}
				f := fs[0]
				t.Run(name, func(t *testing.T) {
					if f.Mode != wantMode(tr.Mode) || f.Estimated || f.Grade != model.GradeA || !f.Recurring {
						t.Fatalf("seed %d: fault = %s\ntruth = %+v", seed, faultString(f), tr)
					}
					eqOpt(t, "rank", f.Rank, tr.Rank)
					eqOpt(t, "device", f.DeviceId, tr.Device)
					eqOpt(t, "bank group", f.BankGroup, tr.BankGroup)
					eqOpt(t, "bank", f.Bank, tr.Bank)
					switch tr.Mode {
					case model.ModeSingleBit:
						eqOpt(t, "row", f.RowMin, tr.Row)
						eqOpt(t, "row", f.RowMax, tr.Row)
						eqOpt(t, "column", f.ColumnMin, tr.Column)
						eqOpt(t, "column", f.ColumnMax, tr.Column)
					case model.ModeRow, model.ModeMultiRow:
						eqOpt(t, "top row", f.TopRow, tr.Row)
						eqOpt(t, "first row", f.RowMin, tr.Row)
						if tr.Mode == model.ModeMultiRow || tr.Spreading {
							eqOpt(t, "last row", f.RowMax, model.Some(tr.Row.V+1))
						} else {
							eqOpt(t, "last row", f.RowMax, tr.Row)
						}
					case model.ModeColumn:
						eqOpt(t, "column", f.ColumnMin, tr.Column)
						eqOpt(t, "column", f.ColumnMax, tr.Column)
					}
					// The DIMM's faults hold every generated error, and stray
					// errors of the DIMM at most. A line fault is all in
					// one fault. Among hundreds of scattered errors chance
					// may line a few up (TestScatterIsNotALine bounds how
					// often); they then form a small fault of their own.
					all, scatter := 0, f.Mode == model.ModeBank || f.Mode == model.ModeDevice || f.Mode == model.ModeDIMM
					for _, o := range fs {
						all += o.ErrorCount
					}
					if all < tr.CE+tr.UE || all > tr.CE+tr.UE+10 || int(f.UE) != tr.UE {
						t.Errorf("seed %d: %d errors (%d uncorrected) in the DIMM's faults, %d generated (%d uncorrected)", seed, all, f.UE, tr.CE+tr.UE, tr.UE)
					}
					if scatter && f.ErrorCount*100 < (tr.CE+tr.UE)*97 || !scatter && f.ErrorCount != tr.CE+tr.UE {
						t.Errorf("seed %d: %d errors in the fault, %d generated", seed, f.ErrorCount, tr.CE+tr.UE)
					}
					if confined := f.Mode != model.ModeDevice && f.Mode != model.ModeDIMM; f.Confined != confined {
						t.Errorf("seed %d: confined = %v for %s", seed, f.Confined, f.Mode)
					}
				})
			}
			// Everything else is stray errors: single places hit once.
			if len(ds.Scenario.Moves) > 0 && ds.Scenario.Faults[0].StaysWithSlot {
				continue
			}
			for _, d := range c.DIMMs {
				fs := d.Faults
				if truthDIMM[d.UID] {
					fs = fs[1:]
				}
				for _, f := range fs {
					if ds.Scenario.Name == "demo" {
						t.Errorf("demo/%d: the demo has no stray errors, found %s", seed, faultString(f))
					}
					line := f.Mode == model.ModeRow || f.Mode == model.ModeColumn
					if truthDIMM[d.UID] && line && f.Shape.Cells <= 8 && modeExtent(d.Faults[0].Mode) >= modeExtent(model.ModeBank) {
						continue // a chance alignment inside scatter
					}
					if f.Mode != model.ModeSingleBit || f.Recurring || f.Confined || f.ErrorCount != 1 {
						t.Errorf("%s/%d: stray error classified as %s", name, seed, faultString(f))
					}
				}
			}
		}
	}
}

// TestErrorsThatStayInTheSlot: when DIMMs are swapped and the errors stay
// in the slot, each of the two DIMMs has its share of them, as scatter.
func TestErrorsThatStayInTheSlot(t *testing.T) {
	ds := generate(t, "system-slot", 3, noNoise)
	in := load(t, ledgerOf(t, ds))
	c := Classify(in, Options{})
	if len(c.DIMMs) != 2 {
		t.Fatalf("%d DIMMs with faults, want the two that sat in the slot", len(c.DIMMs))
	}
	total := 0
	for _, d := range c.DIMMs {
		if len(d.Faults) != 1 || d.Faults[0].Mode != model.ModeDIMM || !reflect.DeepEqual(d.Faults[0].Slots, []string{"CPU0_C1"}) {
			t.Errorf("DIMM %s: %s", d.DIMM.Serial, describe(d.Faults))
		}
		total += d.Faults[0].ErrorCount
	}
	if total != ds.Truth[0].CE {
		t.Errorf("%d errors in the two faults, %d generated", total, ds.Truth[0].CE)
	}
}

// TestMovedDIMMKeepsOneFault: a row fault that follows its DIMM to another
// slot stays one fault, because rows and columns are counted inside the DIMM.
func TestMovedDIMMKeepsOneFault(t *testing.T) {
	ds := generate(t, "moved-dimm", 3, noNoise)
	c := Classify(load(t, ledgerOf(t, ds)), Options{})
	fs := faultsOf(c, uidOf(ds.Truth[0].Serial))
	if len(c.DIMMs) != 1 || len(fs) != 1 || fs[0].Mode != model.ModeRow || fs[0].ErrorCount != 240 ||
		!reflect.DeepEqual(fs[0].Slots, []string{"CPU0_C1", "CPU0_D1"}) || fs[0].Slot != "CPU0_D1" {
		t.Errorf("faults:%s", describe(fs))
	}
}

// ledgerErrors returns the corrected and uncorrected error totals of a ledger.
func ledgerErrors(t *testing.T, s *store.Store) (ce, ue int64) {
	t.Helper()
	ts, err := s.Totals(ctx, store.EventFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range ts {
		ce, ue = ce+x.CE, ue+x.UE
	}
	return ce, ue
}

// checkComplete verifies that a classification accounts for every error of
// the ledger exactly once: no record in two faults, none dropped.
func checkComplete(t *testing.T, name string, s *store.Store, in *Input, c *Classification) {
	t.Helper()
	wantCE, wantUE := ledgerErrors(t, s)
	var ce, ue int64
	seen := map[int64]bool{}
	for _, d := range c.DIMMs {
		for _, f := range d.Faults {
			ce, ue = ce+f.CE, ue+f.UE
			if int64(f.ErrorCount) != f.CE+f.UE || f.EventCount != len(in.EventIDs(f)) || f.EventCount == 0 {
				t.Errorf("%s: fault counts do not add up: %s", name, faultString(f))
			}
			if f.DIMMUID != d.UID || f.Host == "" || f.EngineVersion != Version || f.FirstSeen.After(f.LastSeen) || !f.Mode.Valid() || !f.Grade.Valid() {
				t.Errorf("%s: incomplete fault: %+v", name, f.Fault)
			}
			for _, id := range in.EventIDs(f) {
				if seen[id] {
					t.Errorf("%s: event %d is in two faults", name, id)
				}
				seen[id] = true
			}
		}
	}
	for _, u := range c.Unattributed {
		ce, ue = ce+u.CE, ue+u.UE
	}
	if ce != wantCE || ue != wantUE {
		t.Errorf("%s: faults hold %d corrected and %d uncorrected errors, the ledger %d and %d", name, ce, ue, wantCE, wantUE)
	}
}

// TestEveryErrorIsInOneFault runs the completeness check over every
// scenario, as stored with full locations and as each platform reports it.
func TestEveryErrorIsInOneFault(t *testing.T) {
	for _, name := range synth.Names() {
		ds := generate(t, name, 11, nil)
		s := ledgerOf(t, ds)
		in := load(t, s)
		checkComplete(t, name, s, in, Classify(in, Options{}))
	}
	for _, name := range []string{"demo", "dimm", "column"} {
		ds := generate(t, name, 11, nil)
		for _, fl := range synth.Flavors {
			s := treeLedger(t, ds, fl)
			in := load(t, s)
			checkComplete(t, name+"/"+string(fl), s, in, Classify(in, Options{}))
		}
	}
}

// TestObservationGradeLimitsTheMode is the guard against reading a mode out
// of addresses that were not decoded. The same histories are read back the
// way three platforms report them:
//
//   - ghes: every field, including the device. Modes as generated.
//   - intel: rank, bank, row and column, no device. Modes as generated,
//     except that scatter over several banks of one rank cannot be pinned to
//     one device.
//   - amd: a physical address and a rank. No fault may carry a mode that
//     needs rows and columns; a repeating address and a run of close
//     addresses are reported as estimates.
func TestObservationGradeLimitsTheMode(t *testing.T) {
	type want struct {
		mode        model.FaultMode
		estimatedBy string
	}
	tests := []struct {
		scenario         string
		ghes, intel, amd want
		// amdAny: at grade B the fault may fall apart into single addresses
		// or show as scatter, depending on how fast its rows appeared. Only
		// the guard applies.
		amdAny bool
	}{
		{scenario: "single-bit", ghes: want{model.ModeSingleBit, ""}, intel: want{model.ModeSingleBit, ""}, amd: want{model.ModeSingleBit, EstimateAddressOnly}},
		{scenario: "row", ghes: want{model.ModeRow, ""}, intel: want{model.ModeRow, ""}, amd: want{model.ModeRow, EstimateAddressOnly}},
		{scenario: "demo", ghes: want{model.ModeRow, ""}, intel: want{model.ModeRow, ""}, amd: want{model.ModeRow, EstimateAddressOnly}},
		// Two neighbouring rows are one run of addresses: a row, estimated.
		{scenario: "multi-row", ghes: want{model.ModeMultiRow, ""}, intel: want{model.ModeMultiRow, ""}, amd: want{model.ModeRow, EstimateAddressOnly}},
		// The rows of a column fault are far apart in the address space.
		{scenario: "column", ghes: want{model.ModeColumn, ""}, intel: want{model.ModeColumn, ""}, amdAny: true},
		{scenario: "bank", ghes: want{model.ModeBank, ""}, intel: want{model.ModeBank, ""}, amd: want{model.ModeUndetermined, ""}},
		{scenario: "device", ghes: want{model.ModeDevice, ""}, intel: want{model.ModeDevice, EstimateDeviceUnknown}, amd: want{model.ModeUndetermined, ""}},
		{scenario: "dimm", ghes: want{model.ModeDIMM, ""}, intel: want{model.ModeDIMM, ""}, amd: want{model.ModeUndetermined, ""}},
	}
	for _, tt := range tests {
		ds := generate(t, tt.scenario, 4, noNoise)
		uid := uidOf(ds.Truth[0].Serial)
		for _, fl := range synth.Flavors {
			t.Run(tt.scenario+"/"+string(fl), func(t *testing.T) {
				c := Classify(load(t, treeLedger(t, ds, fl)), Options{})
				fs := faultsOf(c, uid)
				if len(fs) == 0 {
					t.Fatal("no fault")
				}
				w, grade := tt.ghes, model.GradeA
				switch fl {
				case synth.FlavorIntel:
					w = tt.intel
				case synth.FlavorAMD:
					w, grade = tt.amd, model.GradeB
				}
				for _, f := range fs {
					if f.Grade != grade {
						t.Errorf("grade %s, want %s: %s", f.Grade, grade, faultString(f))
					}
					if f.Estimated != (f.EstimatedBy != "") {
						t.Errorf("estimated flag and reason disagree: %s", faultString(f))
					}
					// The guard itself: without rows and columns a mode is
					// an estimate from addresses, or it is undetermined.
					if f.Grade != model.GradeA && f.Mode != model.ModeUndetermined &&
						(f.EstimatedBy != EstimateAddressOnly || (f.Mode != model.ModeSingleBit && f.Mode != model.ModeRow)) {
						t.Errorf("mode %s from undecoded addresses: %s", f.Mode, faultString(f))
					}
					if f.Grade != model.GradeA && (f.RowMin.Valid || f.ColumnMin.Valid || f.Bank.Valid || f.Shape.Rows != 0 || f.Shape.Columns != 0) {
						t.Errorf("rows or columns on a fault without decoded addresses: %s", faultString(f))
					}
				}
				if fl == synth.FlavorAMD && tt.amdAny {
					return
				}
				if len(fs) != 1 {
					t.Errorf("want one fault:%s", describe(fs))
				}
				if f := fs[0]; f.Mode != w.mode || f.EstimatedBy != w.estimatedBy {
					t.Errorf("fault = %s\nwant mode %s, estimated by %q", faultString(f), w.mode, w.estimatedBy)
				}
				// The device is only named where the platform reports it.
				if dev := fs[0].DeviceId; (fl == synth.FlavorGHES && tt.scenario != "dimm") != dev.Valid {
					t.Errorf("device = %s on %s", dev, fl)
				}
			})
		}
	}
}

// TestSameErrorsWithAndWithoutDecoding feeds one set of errors twice: once
// with bank, row and column, once with the physical address only.
func TestSameErrorsWithAndWithoutDecoding(t *testing.T) {
	type hit struct {
		row, col int
		n        int
	}
	// A column fault: one column, six rows, each failing repeatedly.
	var hits []hit
	for i := range 6 {
		hits = append(hits, hit{row: 1000 + 977*i, col: 512, n: 8})
	}
	build := func(decoded bool) []*Fault {
		b := newBuilder()
		for i, h := range hits {
			for k := range h.n {
				l := loc{rank: 0, dev: -1, bg: 1, bank: 2, row: h.row, col: h.col}
				if !decoded {
					l = loc{rank: 0, dev: -1, bg: -1, bank: -1, row: -1, col: -1}
				}
				e := b.event("d1", t0.Add(time.Duration(i)*day+time.Duration(k)*37*time.Hour), l)
				// Rows are 16 KiB apart in this address map.
				e.OsPhysicalAddress = model.Some(uint64(8<<30) + uint64(h.row)<<14 + uint64(h.col)<<3)
				b.add(e)
			}
		}
		return faultsOf(b.classify(Options{}), "d1")
	}
	decoded := build(true)
	if len(decoded) != 1 || decoded[0].Mode != model.ModeColumn || decoded[0].Estimated || decoded[0].Shape.Rows != 6 || decoded[0].ErrorCount != 48 {
		t.Errorf("decoded:%s", describe(decoded))
	}
	undecoded := build(false)
	if len(undecoded) == 0 {
		t.Fatal("undecoded: no faults")
	}
	n := 0
	for _, f := range undecoded {
		n += f.ErrorCount
		if f.Mode == model.ModeColumn || f.Grade != model.GradeB || (f.Mode != model.ModeUndetermined && !f.Estimated) {
			t.Errorf("undecoded: %s", faultString(f))
		}
	}
	if n != 48 {
		t.Errorf("undecoded: %d errors in faults, want 48", n)
	}
}

// TestCountsOnly: counter growth and records that name a DIMM and nothing
// else have no mode.
func TestCountsOnly(t *testing.T) {
	b := newBuilder()
	for i := range 3 {
		e := b.event("d1", t0.Add(time.Duration(i)*day), loc{-1, -1, -1, -1, -1, -1})
		e.Kind, e.Count, e.WindowStart = model.KindCounter, 40, t0.Add(time.Duration(i-1)*day)
		b.add(e)
	}
	ue := b.event("d1", t0.Add(4*day), loc{-1, -1, -1, -1, -1, -1})
	ue.Kind, ue.ErrorSeverity = model.KindCounter, model.SevUE
	b.add(ue)
	// A record with a rank and no address.
	b.ce("d1", t0.Add(5*day), loc{1, -1, -1, -1, -1, -1})
	// A counter record that located records fully account for adds nothing.
	covered := b.event("d1", t0.Add(6*day), loc{-1, -1, -1, -1, -1, -1})
	covered.Kind, covered.Count, covered.Covered = model.KindCounter, 9, 9
	b.add(covered)

	fs := faultsOf(b.classify(Options{}), "d1")
	if len(fs) != 2 {
		t.Fatalf("faults:%s", describe(fs))
	}
	c, r := fs[0], fs[1]
	if c.Mode != model.ModeUndetermined || c.Grade != model.GradeC || c.CE != 120 || c.UE != 1 || c.EventCount != 4 || c.Confidence != 0 || c.Confined || c.Shape.Cells != 0 {
		t.Errorf("counts: %s", faultString(c))
	}
	if r.Mode != model.ModeUndetermined || r.Grade != model.GradeB || r.CE != 1 || r.Rank != model.Some(1) {
		t.Errorf("rank only: %s", faultString(r))
	}
}

// scatterCells adds n errors at random places of the given banks, one error
// per place, spread over a month.
func scatterCells(b *builder, rng *rand.Rand, uid string, n int, pick func() (rank, dev, bg, bank int)) {
	for i := range n {
		rank, dev, bg, bank := pick()
		b.ce(uid, t0.Add(time.Duration(rng.Int64N(int64(30*day)))), loc{rank, dev, bg, bank, rng.IntN(1 << 16), rng.IntN(256) * 8})
		_ = i
	}
}

// TestScatterIsNotALine is the guard against the birthday effect. Hundreds
// of errors scattered over a bank put several errors on some column, and a
// few on some row, by accident. None of that is a row or a column fault.
func TestScatterIsNotALine(t *testing.T) {
	const trials = 300
	lines := 0
	for seed := range trials {
		rng := rand.New(rand.NewPCG(uint64(seed), 99))
		n := []int{5, 12, 40, 150, 600, 2500}[seed%6]
		b := newBuilder()
		var want model.FaultMode
		switch seed % 3 {
		case 0:
			want = model.ModeBank
			scatterCells(b, rng, "d1", n, func() (int, int, int, int) { return 1, 4, 2, 3 })
		case 1:
			want = model.ModeDevice
			scatterCells(b, rng, "d1", n, func() (int, int, int, int) { return 1, 4, rng.IntN(8), rng.IntN(4) })
		default:
			want = model.ModeDIMM
			scatterCells(b, rng, "d1", n, func() (int, int, int, int) { return rng.IntN(2), rng.IntN(20), rng.IntN(8), rng.IntN(4) })
		}
		fs := faultsOf(b.classify(Options{}), "d1")
		if len(fs) == 0 || fs[0].Mode != want {
			t.Errorf("seed %d, %d errors: want %s first:%s", seed, n, want, describe(fs[:min(len(fs), 4)]))
			continue
		}
		for _, f := range fs[1:] {
			switch f.Mode {
			case model.ModeRow, model.ModeColumn, model.ModeMultiRow:
				lines++
				t.Logf("seed %d, %d errors: chance alignment reported as %s", seed, n, faultString(f))
			}
		}
	}
	// The line test allows a chance alignment with probability below
	// LineChance per DIMM. Far more than that would mean the test is broken.
	if limit := int(3 * DefaultConfig().LineChance * trials); lines > limit {
		t.Errorf("%d of %d scattered DIMMs show a line fault, want at most %d", lines, trials, limit)
	}
}

// TestMixtures checks faults that share a DIMM.
func TestMixtures(t *testing.T) {
	bank := func(row, col int) loc { return loc{0, 7, 1, 2, row, col} }
	// repeat adds n errors at one place, a day and a half apart.
	repeat := func(b *builder, l loc, n int) {
		for k := range n {
			b.ce("d1", t0.Add(time.Duration(k)*36*time.Hour), l)
		}
	}
	rowFault := func(b *builder, row, cols, each int) {
		for c := range cols {
			repeat(b, bank(row, 64+c*160), each)
		}
	}
	check := func(t *testing.T, fs []*Fault, want ...model.FaultMode) {
		t.Helper()
		if !reflect.DeepEqual(modes(fs), want) {
			t.Fatalf("modes = %v, want %v:%s", modes(fs), want, describe(fs))
		}
	}

	t.Run("a row fault inside a scattered bank", func(t *testing.T) {
		b := newBuilder()
		rng := rand.New(rand.NewPCG(5, 5))
		rowFault(b, 0x1a2f, 12, 30)
		scatterCells(b, rng, "d1", 40, func() (int, int, int, int) { return 0, 7, 1, 2 })
		fs := faultsOf(b.classify(Options{}), "d1")
		check(t, fs, model.ModeRow, model.ModeBank)
		if fs[0].ErrorCount != 360 || fs[0].Shape.Columns != 12 || fs[0].TopRow != model.Some(0x1a2f) || fs[1].ErrorCount != 40 {
			t.Errorf("faults:%s", describe(fs))
		}
	})

	t.Run("a row and a column that cross", func(t *testing.T) {
		b := newBuilder()
		rowFault(b, 500, 6, 4)
		for r := range 5 {
			repeat(b, bank(9000+r*700, 64), 3) // column 64 is also on the row
		}
		fs := faultsOf(b.classify(Options{}), "d1")
		check(t, fs, model.ModeRow, model.ModeColumn)
		if fs[0].Shape.Cells != 6 || fs[1].Shape.Cells != 5 || fs[1].ColumnMin != model.Some(64) {
			t.Errorf("faults:%s", describe(fs))
		}
	})

	t.Run("two weak cells in one bank are two faults, not a bank fault", func(t *testing.T) {
		b := newBuilder()
		repeat(b, bank(100, 800), 20)
		repeat(b, bank(40000, 16), 7)
		fs := faultsOf(b.classify(Options{}), "d1")
		check(t, fs, model.ModeSingleBit, model.ModeSingleBit)
		if !fs[0].Recurring || !fs[0].Confined || fs[0].ErrorCount != 20 || fs[1].ErrorCount != 7 {
			t.Errorf("faults:%s", describe(fs))
		}
	})

	t.Run("few stray errors stay apart, many in a month are a fault", func(t *testing.T) {
		at := func(i int) loc { return bank(1000+i*3777, 8*(3+i*29)) }
		b := newBuilder()
		for i := range 4 {
			b.ce("d1", t0.Add(time.Duration(i)*5*day), at(i))
		}
		check(t, faultsOf(b.classify(Options{}), "d1"), model.ModeSingleBit, model.ModeSingleBit, model.ModeSingleBit, model.ModeSingleBit)

		b = newBuilder()
		for i := range 5 {
			b.ce("d1", t0.Add(time.Duration(i)*5*day), at(i))
		}
		fs := faultsOf(b.classify(Options{}), "d1")
		check(t, fs, model.ModeBank)
		if fs[0].Shape.Cells != 5 || fs[0].Bank != model.Some(2) || fs[0].BankGroup != model.Some(1) || fs[0].DeviceId != model.Some(7) {
			t.Errorf("bank fault: %s", faultString(fs[0]))
		}

		// The same five places over ten months are five separate upsets.
		b = newBuilder()
		for i := range 5 {
			b.ce("d1", t0.Add(time.Duration(i)*60*day), at(i))
		}
		check(t, faultsOf(b.classify(Options{}), "d1"), model.ModeSingleBit, model.ModeSingleBit, model.ModeSingleBit, model.ModeSingleBit, model.ModeSingleBit)
	})

	t.Run("a row fault that starts to reach the next row stays a row fault", func(t *testing.T) {
		b := newBuilder()
		rowFault(b, 0x1a2f, 9, 40)
		b.ce("d1", t0.Add(50*day), bank(0x1a30, 64))
		b.ce("d1", t0.Add(51*day), bank(0x1a30, 704))
		b.ce("d1", t0.Add(52*day), bank(0x1a2e, 1344))
		fs := faultsOf(b.classify(Options{}), "d1")
		check(t, fs, model.ModeRow)
		f := fs[0]
		if f.Shape.Rows != 3 || f.RowMin != model.Some(0x1a2e) || f.RowMax != model.Some(0x1a30) || f.TopRow != model.Some(0x1a2f) || f.ErrorCount != 363 {
			t.Errorf("fault: %s", faultString(f))
		}
	})

	t.Run("neighbouring rows with a real share of the errors are a multi-row fault", func(t *testing.T) {
		b := newBuilder()
		rowFault(b, 700, 8, 10)
		rowFault(b, 701, 6, 5)
		fs := faultsOf(b.classify(Options{}), "d1")
		check(t, fs, model.ModeMultiRow)
		if fs[0].Shape.Rows != 2 || fs[0].TopRow != model.Some(700) || fs[0].ErrorCount != 110 {
			t.Errorf("fault: %s", faultString(fs[0]))
		}
		// Rows that are not neighbours are two row faults.
		b = newBuilder()
		rowFault(b, 700, 8, 10)
		rowFault(b, 703, 6, 5)
		check(t, faultsOf(b.classify(Options{}), "d1"), model.ModeRow, model.ModeRow)
	})

	t.Run("single cells in neighbouring rows", func(t *testing.T) {
		b := newBuilder()
		repeat(b, bank(700, 64), 3)
		repeat(b, bank(701, 1280), 3)
		fs := faultsOf(b.classify(Options{}), "d1")
		check(t, fs, model.ModeMultiRow)
		if fs[0].Confidence >= 80 {
			t.Errorf("two cells should not give a confident mode: %s", faultString(fs[0]))
		}
		b = newBuilder()
		repeat(b, bank(700, 64), 3)
		repeat(b, bank(702, 1280), 3)
		check(t, faultsOf(b.classify(Options{}), "d1"), model.ModeSingleBit, model.ModeSingleBit)
	})

	t.Run("one bank holds nearly everything", func(t *testing.T) {
		b := newBuilder()
		rng := rand.New(rand.NewPCG(8, 8))
		scatterCells(b, rng, "d1", 300, func() (int, int, int, int) { return 0, 7, 1, 2 })
		// Three stray errors elsewhere do not make it a device or DIMM fault.
		b.ce("d1", t0.Add(3*day), loc{0, 7, 5, 0, 77, 80})
		b.ce("d1", t0.Add(9*day), loc{1, 2, 0, 1, 9000, 160})
		b.ce("d1", t0.Add(20*day), loc{1, 11, 3, 3, 123, 240})
		fs := faultsOf(b.classify(Options{}), "d1")
		check(t, fs, model.ModeBank, model.ModeSingleBit, model.ModeSingleBit, model.ModeSingleBit)
	})

	t.Run("a device fault next to scatter over the rest of the DIMM", func(t *testing.T) {
		b := newBuilder()
		rng := rand.New(rand.NewPCG(9, 9))
		scatterCells(b, rng, "d1", 900, func() (int, int, int, int) { return 0, 7, rng.IntN(8), rng.IntN(4) })
		scatterCells(b, rng, "d1", 40, func() (int, int, int, int) { return 1, rng.IntN(20), rng.IntN(8), rng.IntN(4) })
		fs := faultsOf(b.classify(Options{}), "d1")
		check(t, fs, model.ModeDevice, model.ModeDIMM)
		if fs[0].DeviceId != model.Some(7) || fs[0].ErrorCount != 900 || fs[1].ErrorCount != 40 {
			t.Errorf("faults:%s", describe(fs))
		}
	})

	t.Run("an uncorrected error belongs to the fault at its place", func(t *testing.T) {
		b := newBuilder()
		rowFault(b, 0x1a2f, 9, 5)
		ue := b.event("d1", t0.Add(40*day), bank(0x1a2f, 64))
		ue.ErrorSeverity = model.SevUE
		b.add(ue)
		fs := faultsOf(b.classify(Options{}), "d1")
		check(t, fs, model.ModeRow)
		if fs[0].UE != 1 || fs[0].CE != 45 || fs[0].ErrorCount != 46 {
			t.Errorf("fault: %s", faultString(fs[0]))
		}
	})

	t.Run("one record that stands for many errors", func(t *testing.T) {
		b := newBuilder()
		e := b.event("d1", t0, bank(5, 8))
		e.Count = 50
		b.add(e)
		fs := faultsOf(b.classify(Options{}), "d1")
		check(t, fs, model.ModeSingleBit)
		if fs[0].ErrorCount != 50 || fs[0].EventCount != 1 || !fs[0].Recurring {
			t.Errorf("fault: %s", faultString(fs[0]))
		}
	})
}

// TestRepetitionNeedsTime: an upset cell can be read several times before
// it is rewritten. Errors close together are one incident, not a lasting
// fault.
func TestRepetitionNeedsTime(t *testing.T) {
	l := loc{0, 3, 0, 1, 4242, 96}
	b := newBuilder()
	for k := range 5 {
		b.ce("d1", t0.Add(time.Duration(k)*10*time.Second), l)
	}
	fs := faultsOf(b.classify(Options{}), "d1")
	if len(fs) != 1 || fs[0].Mode != model.ModeSingleBit || fs[0].Recurring || fs[0].Confined || fs[0].ErrorCount != 5 {
		t.Errorf("five errors in a minute:%s", describe(fs))
	}
	// The same place two days later is a repetition.
	b.ce("d1", t0.Add(2*day), l)
	fs = faultsOf(b.classify(Options{}), "d1")
	if len(fs) != 1 || !fs[0].Recurring || !fs[0].Confined {
		t.Errorf("an error two days later:%s", describe(fs))
	}
	// So are many errors, however close together.
	b = newBuilder()
	for k := range DefaultConfig().RepeatCount {
		b.ce("d1", t0.Add(time.Duration(k)*time.Second), l)
	}
	if fs = faultsOf(b.classify(Options{}), "d1"); len(fs) != 1 || !fs[0].Recurring {
		t.Errorf("a burst at one place:%s", describe(fs))
	}
	// The thresholds are configuration.
	cfg := DefaultConfig()
	cfg.RepeatGapHours = 72
	if fs = faultsOf(b2(l).classify(Options{Config: &cfg}), "d1"); fs[0].Recurring {
		t.Errorf("with a 72 h gap two days are not a repetition:%s", describe(fs))
	}
}

// b2 builds two errors at one place, two days apart.
func b2(l loc) *builder {
	b := newBuilder()
	b.ce("d1", t0, l)
	b.ce("d1", t0.Add(2*day), l)
	return b
}

// TestDeviceNotReported: without the device, scatter over the banks of one
// rank is at least a device fault. Two ranks are two devices at least.
func TestDeviceNotReported(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 3))
	b := newBuilder()
	scatterCells(b, rng, "d1", 200, func() (int, int, int, int) { return 1, -1, rng.IntN(8), rng.IntN(4) })
	fs := faultsOf(b.classify(Options{}), "d1")
	if len(fs) != 1 || fs[0].Mode != model.ModeDevice || fs[0].EstimatedBy != EstimateDeviceUnknown || !fs[0].Estimated ||
		fs[0].DeviceId.Valid || fs[0].Rank != model.Some(1) || fs[0].Confidence > 60 {
		t.Errorf("one rank:%s", describe(fs))
	}
	b = newBuilder()
	scatterCells(b, rng, "d1", 200, func() (int, int, int, int) { return rng.IntN(2), -1, rng.IntN(8), rng.IntN(4) })
	fs = faultsOf(b.classify(Options{}), "d1")
	if len(fs) != 1 || fs[0].Mode != model.ModeDIMM || fs[0].Estimated {
		t.Errorf("two ranks:%s", describe(fs))
	}
	// A rank where only some records name the device counts as one unit:
	// the records without a device may belong to the device that is named.
	b = newBuilder()
	scatterCells(b, rng, "d1", 100, func() (int, int, int, int) { return 0, 5, rng.IntN(8), rng.IntN(4) })
	scatterCells(b, rng, "d1", 100, func() (int, int, int, int) { return 0, -1, rng.IntN(8), rng.IntN(4) })
	fs = faultsOf(b.classify(Options{}), "d1")
	for _, f := range fs {
		if f.Mode == model.ModeDIMM {
			t.Errorf("mixed device reporting gave a DIMM fault:%s", describe(fs))
		}
	}
}

// scrambled is an adjacency in which the physical neighbours of a row are
// eight rows away, as a stand-in for a vendor layout.
type scrambled struct{ sites []Site }

func (s *scrambled) NeighborRows(site Site, row int) []int {
	s.sites = append(s.sites, site)
	return []int{row - 8, row + 8}
}

// TestAdjacencyHook: which rows are neighbours is the vendor's knowledge.
func TestAdjacencyHook(t *testing.T) {
	build := func(second int) *builder {
		b := newBuilder()
		for _, row := range []int{4096, second} {
			for c := range 6 {
				for k := range 5 {
					b.ce("d1", t0.Add(time.Duration(k)*30*time.Hour), loc{1, 9, 2, 1, row, 80 * (c + 1)})
				}
			}
		}
		b.in.DIMMs = []model.DIMM{{UID: "d1", PartNumber: "PN-1"}}
		return b
	}
	// Logical neighbours.
	if fs := faultsOf(build(4097).classify(Options{}), "d1"); len(fs) != 1 || fs[0].Mode != model.ModeMultiRow {
		t.Errorf("default, rows 4096 and 4097:%s", describe(fs))
	}
	if fs := faultsOf(build(4104).classify(Options{}), "d1"); len(fs) != 2 || fs[0].Mode != model.ModeRow || fs[1].Mode != model.ModeRow {
		t.Errorf("default, rows 4096 and 4104:%s", describe(fs))
	}
	// With the vendor layout it is the other way round.
	adj := &scrambled{}
	if fs := faultsOf(build(4104).classify(Options{Adjacency: adj}), "d1"); len(fs) != 1 || fs[0].Mode != model.ModeMultiRow || fs[0].Shape.Rows != 2 {
		t.Errorf("vendor layout, rows 4096 and 4104:%s", describe(fs))
	}
	if fs := faultsOf(build(4097).classify(Options{Adjacency: adj}), "d1"); len(fs) != 2 {
		t.Errorf("vendor layout, rows 4096 and 4097:%s", describe(fs))
	}
	// The hook is told which bank of which module it is asked about.
	if len(adj.sites) == 0 {
		t.Fatal("the adjacency was not asked")
	}
	want := Site{DIMM: adj.sites[0].DIMM, Rank: model.Some(1), DeviceId: model.Some(9), BankGroup: model.Some(2), Bank: model.Some(1)}
	if adj.sites[0] != want || want.DIMM == nil || want.DIMM.PartNumber != "PN-1" {
		t.Errorf("site = %+v", adj.sites[0])
	}
	// The default has no row below zero.
	if n := (LogicalAdjacency{}).NeighborRows(Site{}, 0); !reflect.DeepEqual(n, []int{1}) {
		t.Errorf("neighbours of row 0 = %v", n)
	}
}

// signature is a comparable summary of a classification.
func signature(in *Input, c *Classification) []string {
	var out []string
	for _, d := range c.DIMMs {
		for _, f := range d.Faults {
			ids := in.EventIDs(f)
			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
			s := d.UID + " " + faultString(f)
			for _, id := range ids {
				s += " " + time.Duration(id).String()
			}
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// TestOrderDoesNotMatter: the result depends on the records, not on the
// order they arrive in, and two runs give the same result.
func TestOrderDoesNotMatter(t *testing.T) {
	for _, name := range []string{"demo", "dimm", "system-channel", "multi-row"} {
		ds := generate(t, name, 21, nil)
		s := ledgerOf(t, ds)
		evs, err := s.EventList(ctx, store.EventFilter{})
		if err != nil {
			t.Fatal(err)
		}
		build := func(seed uint64) (*Input, *Classification) {
			in := NewInput()
			in.DIMMs, _ = s.DIMMs(ctx)
			order := rand.New(rand.NewPCG(seed, 1)).Perm(len(evs))
			if seed == 0 {
				sort.Ints(order)
			}
			for _, i := range order {
				in.Add(evs[i])
			}
			return in, Classify(in, Options{})
		}
		in0, c0 := build(0)
		want := signature(in0, c0)
		for seed := uint64(1); seed <= 3; seed++ {
			in, c := build(seed)
			if got := signature(in, c); !reflect.DeepEqual(got, want) {
				t.Errorf("%s: order %d gives another result\n got: %.300v\nwant: %.300v", name, seed, got, want)
			}
		}
		// Faults of a DIMM are ordered by error count.
		for _, d := range c0.DIMMs {
			for i := 1; i < len(d.Faults); i++ {
				if d.Faults[i].ErrorCount > d.Faults[i-1].ErrorCount {
					t.Errorf("%s: faults are not ordered by error count:%s", name, describe(d.Faults))
				}
			}
		}
	}
}

// TestUnattributedErrorsAreReported: errors the ledger could not tie to a
// DIMM are in no fault, and the result says how many there are.
func TestUnattributedErrorsAreReported(t *testing.T) {
	b := newBuilder()
	b.ce("d1", t0, loc{0, 1, 0, 0, 10, 8})
	for i := range 3 {
		e := b.event("", t0.Add(time.Duration(i)*time.Hour), loc{0, 1, 0, 0, 10, 8})
		e.DIMMLink, e.Slot = model.LinkAmbiguous, "CPU0_C1"
		b.add(e)
	}
	ue := b.event("", t0, loc{-1, -1, -1, -1, -1, -1})
	ue.DIMMLink, ue.Slot, ue.ErrorSeverity = "", "", model.SevFatal
	b.add(ue)
	// Records that count nothing are not kept at all.
	info := b.event("d1", t0, loc{0, 1, 0, 0, 10, 8})
	info.ErrorSeverity = model.SevInformational
	b.add(info)
	dup := b.event("d1", t0, loc{0, 1, 0, 0, 10, 8})
	dup.DupOf = 1
	b.add(dup)
	if b.in.Events() != 5 {
		t.Errorf("%d records kept, want 5", b.in.Events())
	}

	c := b.classify(Options{})
	want := []Unattributed{
		{Host: "h1", Slot: "", UE: 1, Records: 1},
		{Host: "h1", Slot: "CPU0_C1", Ambiguous: true, CE: 3, Records: 3},
	}
	if !reflect.DeepEqual(c.Unattributed, want) {
		t.Errorf("unattributed = %+v\nwant %+v", c.Unattributed, want)
	}
	if fs := faultsOf(c, "d1"); len(c.DIMMs) != 1 || len(fs) != 1 || fs[0].ErrorCount != 1 {
		t.Errorf("faults:%s", describe(fs))
	}
}

// TestTimesSinceBootOnly: records that carry seconds since boot and no date
// are classified by place. They cannot show that a place failed again later.
func TestTimesSinceBootOnly(t *testing.T) {
	b := newBuilder()
	epoch := time.Unix(0, 0).UTC()
	for c := range 4 {
		for k := range 2 {
			e := b.event("d1", epoch.Add(time.Duration(100+c*50+k)*time.Second), loc{0, -1, 0, 3, 777, 64 * (c + 1)})
			e.TimeQuality = model.TimeRelative
			b.add(e)
		}
	}
	fs := faultsOf(b.classify(Options{}), "d1")
	if len(fs) != 1 || fs[0].Mode != model.ModeRow || fs[0].Recurring || fs[0].ErrorCount != 8 {
		t.Fatalf("faults:%s", describe(fs))
	}
	if !fs[0].FirstSeen.Equal(epoch.Add(100*time.Second)) || !fs[0].LastSeen.Equal(epoch.Add(251*time.Second)) {
		t.Errorf("first %v, last %v: want the offsets since boot", fs[0].FirstSeen, fs[0].LastSeen)
	}
	// A dated record decides first and last seen.
	b.ce("d1", t0, loc{0, -1, 0, 3, 777, 64})
	fs = faultsOf(b.classify(Options{}), "d1")
	if len(fs) != 1 || !fs[0].FirstSeen.Equal(t0) || !fs[0].LastSeen.Equal(t0) {
		t.Errorf("with a dated record: first %v, last %v", fs[0].FirstSeen, fs[0].LastSeen)
	}
}

// TestAddressOnlyFollowsTheSlot: physical addresses belong to the slot. A
// DIMM that moved shows two address ranges, which are looked at apart.
func TestAddressOnlyFollowsTheSlot(t *testing.T) {
	b := newBuilder()
	b.in.DIMMs = []model.DIMM{{UID: "d1", SizeBytes: 64 << 30}}
	for i, slot := range []string{"CPU0_C1", "CPU0_D1"} {
		base := uint64(8<<30) + uint64(i)*(64<<30)
		for k := range 6 {
			e := b.event("d1", t0.Add(time.Duration(i*20+k*2)*day), loc{0, -1, -1, -1, -1, -1})
			e.Slot = slot
			e.OsPhysicalAddress = model.Some(base + 0x12340000 + uint64(k%3)*1024)
			b.add(e)
		}
	}
	fs := faultsOf(b.classify(Options{}), "d1")
	if len(fs) != 2 {
		t.Fatalf("faults:%s", describe(fs))
	}
	for i, f := range fs {
		if f.Mode != model.ModeRow || f.EstimatedBy != EstimateAddressOnly || f.ErrorCount != 6 || f.Shape.Cells != 3 || f.Shape.Pages != 1 ||
			!f.AddressMin.Valid || f.AddressMax.V-f.AddressMin.V != 2048 || len(f.Slots) != 1 {
			t.Errorf("fault %d: %s %v", i, faultString(f), f.Slots)
		}
	}
	// An address that repeats is a single-bit fault, estimated.
	b = newBuilder()
	for k := range 4 {
		e := b.event("d1", t0.Add(time.Duration(k)*3*day), loc{-1, -1, -1, -1, -1, -1})
		e.OsPhysicalAddress = model.Some(uint64(0x7_1234_5678))
		b.add(e)
	}
	fs = faultsOf(b.classify(Options{}), "d1")
	if len(fs) != 1 || fs[0].Mode != model.ModeSingleBit || !fs[0].Estimated || !fs[0].Recurring || fs[0].Grade != model.GradeB || fs[0].Rank.Valid {
		t.Errorf("repeating address:%s", describe(fs))
	}
}

// TestTailProbabilities checks the tails against values computed by
// summation, and the two chance bounds built on them.
func TestTailProbabilities(t *testing.T) {
	near := func(got, want float64) bool {
		d := got - want
		return d <= want*0.01+1e-18 && d >= -want*0.01-1e-18
	}
	poisson := []struct {
		k      int
		lambda float64
		want   float64
	}{
		{0, 1, 1}, {1, 0, 0}, {5, 0, 0},
		{2, 1, 0.2642411177}, {5, 2, 0.0526530173}, {12, 2.34, 6.5885e-6},
		{2, 0.001, 4.99667e-7}, {3, 1e-4, 1.66654e-13},
		{3, 50, 1}, // at or below the mean the tail is reported as 1
	}
	for _, tt := range poisson {
		if got := poissonTail(tt.k, tt.lambda); !near(got, tt.want) {
			t.Errorf("poissonTail(%d, %g) = %g, want %g", tt.k, tt.lambda, got, tt.want)
		}
	}
	binom := []struct {
		k, n int
		p    float64
		want float64
	}{
		{0, 5, 0.5, 1}, {6, 5, 0.5, 0}, {2, 2, 0.25, 0.0625}, {2, 3, 0.1, 0.028},
		{3, 10, 0.1, 0.0701908264}, {5, 5, 0.5, 0.03125}, {2, 600, 1.0 / 65536, 4.1639e-5},
		{1, 10, 0.5, 1}, {3, 4, 1, 1},
	}
	for _, tt := range binom {
		if got := binomTail(tt.k, tt.n, tt.p); !near(got, tt.want) {
			t.Errorf("binomTail(%d, %d, %g) = %g, want %g", tt.k, tt.n, tt.p, got, tt.want)
		}
	}
	// Two cells that share one of 256 columns: 1/256 by chance.
	if got := alignChance(2, 2, 256); !near(got, 1.0/256) {
		t.Errorf("alignChance(2, 2, 256) = %g", got)
	}
	// Among 600 cells some column of 256 holds 8 with near certainty.
	if got := alignChance(8, 600, 256); got < 0.5 {
		t.Errorf("alignChance(8, 600, 256) = %g", got)
	}
	if got := alignChance(12, 12, 65536); got > 1e-40 {
		t.Errorf("twelve cells on one row of 65536: chance = %g", got)
	}
	// crowdChance: with nothing else around, a group is beyond chance.
	if got := crowdChance(2, 0, 65536); got != 0 {
		t.Errorf("crowdChance without background = %g", got)
	}
	if got := crowdChance(2, 600, 256); got != 1 {
		t.Errorf("two of 600 in one of 256 bins: chance = %g, want 1", got)
	}
	if ceilPow2(1) != 1 || ceilPow2(1025) != 2048 || gcd(0, 24) != 24 || gcd(24, 40) != 8 {
		t.Error("ceilPow2 or gcd")
	}
}
