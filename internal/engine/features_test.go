package engine

import (
	"math"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/synth"
)

// The hand-built fixtures of this file use one cell, row or bank of DIMM
// "d1" and a reference time some days after t0.

var cellA = loc{0, 3, 1, 2, 5000, 640}

// observedSince makes the ledger cover the host from the given time on: a
// boot started then.
func (b *builder) observedSince(at time.Time) {
	b.in.Boots = append(b.in.Boots, model.Boot{Host: "h1", BootID: "boot-1", StartedAt: at, StartQuality: model.TimeExact})
}

// spreadOver adds n errors at l, evenly spaced from start (inclusive) to
// end (exclusive).
func (b *builder) spreadOver(l loc, n int, start, end time.Time) {
	step := end.Sub(start) / time.Duration(n)
	for k := range n {
		b.ce("d1", start.Add(time.Duration(k)*step+step/2), l)
	}
}

func analyzeD1(t *testing.T, b *builder, asOf time.Time) *DIMMResult {
	t.Helper()
	res := analyze(t, b.in, Options{AsOf: asOf})
	h := res.Host("h1")
	if h == nil || !h.AsOf.Equal(asOf) {
		t.Fatalf("host result = %+v", h)
	}
	d := h.DIMM("d1")
	if d == nil {
		t.Fatal("no result for d1")
	}
	return d
}

func TestTrend(t *testing.T) {
	asOf := t0.Add(60 * day)
	recentFrom, priorFrom := asOf.Add(-7*day), asOf.Add(-37*day)
	tests := []struct {
		name          string
		prior, recent int
		want          string
		rising        bool
	}{
		{"same rate", 30, 7, TrendSteady, false},
		{"three times the rate", 30, 21, TrendRising, true},
		{"twice the rate exactly", 30, 14, TrendRising, true},
		{"just below twice the rate", 30, 13, TrendSteady, false},
		{"a higher rate on too few errors", 3, 4, TrendSteady, false},
		{"nothing before, a few now", 0, 3, TrendNew, false},
		{"nothing before, many now", 0, 12, TrendNew, true},
		{"nothing recently", 30, 0, TrendQuiet, false},
		{"a fifth of the rate", 60, 3, TrendFalling, false},
		{"no errors at all in the windows", 0, 0, TrendQuiet, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBuilder()
			b.observedSince(t0)
			b.ce("d1", t0.Add(day), cellA) // long before both windows
			if tt.prior > 0 {
				b.spreadOver(cellA, tt.prior, priorFrom, recentFrom)
			}
			if tt.recent > 0 {
				b.spreadOver(cellA, tt.recent, recentFrom, asOf)
			}
			d := analyzeD1(t, b, asOf)
			tr := d.Features.Trend
			if tr.Direction != tt.want || tr.Recent != int64(tt.recent) || tr.Prior != int64(tt.prior) || tr.PriorDays != 30 {
				t.Fatalf("trend = %+v, want %s", tr, tt.want)
			}
			cfg := DefaultConfig()
			if tr.Rising(&cfg) != tt.rising {
				t.Errorf("Rising = %v, want %v", tr.Rising(&cfg), tt.rising)
			}
			if got, want := tr.RecentPerDay, float64(tt.recent)/7; math.Abs(got-want) > 1e-9 {
				t.Errorf("recent rate = %g, want %g", got, want)
			}
			if got, want := tr.PriorPerDay, float64(tt.prior)/30; math.Abs(got-want) > 1e-9 {
				t.Errorf("baseline rate = %g, want %g", got, want)
			}
			if tt.prior > 0 && math.Abs(tr.Ratio-tr.RecentPerDay/tr.PriorPerDay) > 1e-9 || tt.prior == 0 && tr.Ratio != 0 {
				t.Errorf("ratio = %g", tr.Ratio)
			}
			// The fault carries the same trend: it holds all the errors.
			if len(d.Faults) != 1 || d.Faults[0].Trend != tr {
				t.Errorf("fault trend = %+v", d.Faults[0].Trend)
			}
			// The slope has the sign of the change.
			switch {
			case tt.want == TrendRising && tr.Slope <= 0, tt.want == TrendFalling && tr.Slope >= 0:
				t.Errorf("slope = %g for %s", tr.Slope, tt.want)
			}
		})
	}
}

// TestTrendNeedsHistory: a ledger that covers a few days cannot tell a new
// fault from one that was always there.
func TestTrendNeedsHistory(t *testing.T) {
	asOf := t0.Add(60 * day)
	for _, tt := range []struct {
		covered   float64 // days of history before the reference time
		want      string
		priorDays float64
	}{
		{5, TrendUnknown, 0},
		{10, TrendUnknown, 3},
		{13.9, TrendUnknown, 6.9},
		{14, TrendNew, 7},
		{20, TrendNew, 13},
		{100, TrendNew, 30},
	} {
		b := newBuilder()
		b.observedSince(asOf.Add(-time.Duration(tt.covered * float64(day))))
		b.spreadOver(cellA, 20, asOf.Add(-3*day), asOf)
		tr := analyzeD1(t, b, asOf).Features.Trend
		if tr.Direction != tt.want || math.Abs(tr.PriorDays-tt.priorDays) > 1e-6 || tr.Recent != 20 {
			t.Errorf("%g days of history: trend = %+v, want %s over %g days", tt.covered, tr, tt.want, tt.priorDays)
		}
	}
	// The baseline rate is taken over the days the ledger covers.
	b := newBuilder()
	b.observedSince(asOf.Add(-17 * day))
	b.spreadOver(cellA, 20, asOf.Add(-17*day), asOf.Add(-7*day))
	b.spreadOver(cellA, 7, asOf.Add(-7*day), asOf)
	if tr := analyzeD1(t, b, asOf).Features.Trend; tr.PriorDays != 10 || tr.PriorPerDay != 2 || tr.Direction != TrendFalling {
		t.Errorf("trend = %+v, want 2 per day over 10 days", tr)
	}
	// Without a boot record the first error bounds the history.
	b = newBuilder()
	b.spreadOver(cellA, 12, asOf.Add(-2*day), asOf)
	d := analyzeD1(t, b, asOf)
	if d.Features.Trend.Direction != TrendUnknown || !d.clk.equalFrom(asOf.Add(-2*day+2*time.Hour)) {
		t.Errorf("trend = %+v", d.Features.Trend)
	}
}

// TestObservedFrom: a caller that knows the host was watched before its
// first record can say so, and a burst of errors is then new.
func TestObservedFrom(t *testing.T) {
	asOf := t0.Add(60 * day)
	b := newBuilder()
	b.spreadOver(cellA, 12, asOf.Add(-2*day), asOf)
	since := asOf.Add(-20 * day)
	h := analyze(t, b.in, Options{AsOf: asOf, ObservedFrom: since}).Host("h1")
	d := h.DIMM("d1")
	cfg := DefaultConfig()
	if tr := d.Features.Trend; tr.Direction != TrendNew || tr.PriorDays != 13 || tr.Recent != 12 || !tr.Rising(&cfg) {
		t.Errorf("trend = %+v, want new over 13 days", tr)
	}
	if !h.ObservedFrom.Equal(since) || !d.clk.equalFrom(since) {
		t.Errorf("observed from %s, want %s", h.ObservedFrom, since)
	}
	// A time after the first record changes nothing: the record is known.
	h = analyze(t, b.in, Options{AsOf: asOf, ObservedFrom: asOf.Add(-day)}).Host("h1")
	if d := h.DIMM("d1"); d.Features.Trend.Direction != TrendUnknown || !d.clk.equalFrom(asOf.Add(-2*day+2*time.Hour)) {
		t.Errorf("trend = %+v, observed from %s", d.Features.Trend, h.ObservedFrom)
	}
	// It also holds without a reference time given by the caller.
	h = analyze(t, b.in, Options{ObservedFrom: since}).Host("h1")
	if !h.ObservedFrom.Equal(since) || h.DIMM("d1").Features.Trend.Direction != TrendNew {
		t.Errorf("without AsOf: observed from %s, trend %+v", h.ObservedFrom, h.DIMM("d1").Features.Trend)
	}
	// A host the ledger has no dated record of has no reference time, and
	// the start of the watch gives it none.
	empty := NewInput()
	empty.Hosts = []model.Host{{Name: "h9"}}
	if h := analyze(t, empty, Options{ObservedFrom: since}).Host("h9"); !h.AsOf.IsZero() || !h.ObservedFrom.IsZero() {
		t.Errorf("host without records: as of %s, observed from %s", h.AsOf, h.ObservedFrom)
	}
}

func (c clock) equalFrom(t time.Time) bool { return c.from == t.UnixMicro() }

// TestRecordsWithoutDateAreInNoTrend: seconds since boot cannot be placed
// in a window. The errors count; the trend, the storm and the spread leave
// them out.
func TestRecordsWithoutDateAreInNoTrend(t *testing.T) {
	asOf := t0.Add(60 * day)
	b := newBuilder()
	b.observedSince(t0)
	b.spreadOver(cellA, 7, asOf.Add(-7*day), asOf)
	for k := range 200 {
		e := b.event("d1", time.Unix(int64(1000+k), 0).UTC(), cellA)
		e.TimeQuality = model.TimeRelative
		b.add(e)
	}
	d := analyzeD1(t, b, asOf)
	ft := d.Features
	if ft.CE != 207 || ft.Undated != 200 || ft.Trend.Recent != 7 || ft.Trend.Prior != 0 || ft.Storm.Peak != 1 || d.Faults[0].Undated != 200 {
		t.Errorf("features = %+v", ft)
	}
	if !ft.First.Equal(asOf.Add(-7*day+12*time.Hour)) || !ft.Last.Equal(asOf.Add(-12*time.Hour)) {
		t.Errorf("first %v, last %v", ft.First, ft.Last)
	}
}

func TestSpread(t *testing.T) {
	row := func(col int) loc { return loc{0, 3, 1, 2, 5000, col} }
	build := func() *builder {
		b := newBuilder()
		b.observedSince(t0)
		// Day 0: three columns of one row, which fail again on the
		// following days.
		for c := range 3 {
			for k := range 4 {
				b.ce("d1", t0.Add(time.Duration(k)*2*day+time.Duration(c)*time.Hour), row(64*(c+1)))
			}
		}
		// Day 40 and 41: two more columns, and one cell in the next row.
		b.ce("d1", t0.Add(40*day), row(1024))
		b.ce("d1", t0.Add(41*day), row(1088))
		b.ce("d1", t0.Add(41*day+time.Hour), loc{0, 3, 1, 2, 5001, 64})
		return b
	}
	d := analyzeD1(t, build(), t0.Add(45*day))
	if len(d.Faults) != 1 || d.Faults[0].Mode != model.ModeRow {
		t.Fatalf("faults:%s", describe(d.Faults))
	}
	want := Spread{Cells: 6, NewCells: 3, NewRows: 1, NewColumns: 2, Spreading: true}
	if got := d.Faults[0].Spread; got != want {
		t.Errorf("spread = %+v, want %+v", got, want)
	}
	// Thirty days later the same places are not new any more.
	d = analyzeD1(t, build(), t0.Add(75*day))
	if got := d.Faults[0].Spread; got != (Spread{Cells: 6}) {
		t.Errorf("spread after 75 days = %+v", got)
	}
	// One new place is below the default of two.
	b := build()
	d = analyzeD1(t, b, t0.Add(40*day+time.Minute))
	if got := d.Faults[0].Spread; got.NewCells != 1 || got.Spreading {
		t.Errorf("spread with one new place = %+v", got)
	}
	cfg := DefaultConfig()
	cfg.SpreadMinNew = 1
	if got := analyze(t, b.in, Options{AsOf: t0.Add(40*day + time.Minute), Config: &cfg}).Host("h1").DIMM("d1").Faults[0].Spread; !got.Spreading {
		t.Errorf("spread with SpreadMinNew = 1: %+v", got)
	}

	// The places a fault shows on its first day are what it is, not growth.
	b = newBuilder()
	b.observedSince(t0)
	for c := range 9 {
		for k := range 3 {
			b.ce("d1", t0.Add(50*day+time.Duration(c+k*9)*time.Hour), row(64*(c+1)))
		}
	}
	d = analyzeD1(t, b, t0.Add(55*day))
	if got := d.Faults[0].Spread; got.Cells != 9 || got.NewCells != 0 || got.Spreading {
		t.Errorf("a fault that came with nine columns: spread = %+v", got)
	}

	// Errors known by address only: the places are cache line addresses.
	b = newBuilder()
	b.observedSince(t0)
	for k := range 5 {
		e := b.event("d1", t0.Add(time.Duration(k*10)*day), loc{0, -1, -1, -1, -1, -1})
		e.OsPhysicalAddress = model.Some(uint64(0x2_0000_0000) + uint64(k)*4096)
		b.add(e)
	}
	d = analyzeD1(t, b, t0.Add(45*day))
	if f := d.Faults[0]; f.Mode != model.ModeRow || !f.Estimated || f.Spread.Cells != 5 || f.Spread.NewCells != 3 || !f.Spread.Spreading {
		t.Errorf("address-only fault: %s spread %+v", faultString(f), f.Spread)
	}
}

func TestStorm(t *testing.T) {
	asOf := t0.Add(10 * day)
	storm := func(b *builder) Storm { return analyzeD1(t, b, asOf).Features.Storm }

	b := newBuilder()
	b.spreadOver(cellA, 150, t0.Add(day), t0.Add(day+30*time.Minute))
	if s := storm(b); s.Peak != 150 || !s.Storm || !s.At.Equal(t0.Add(day+6*time.Second)) {
		t.Errorf("150 errors in half an hour: %+v", s)
	}
	b = newBuilder()
	b.spreadOver(cellA, 150, t0.Add(day), t0.Add(day+3*time.Hour))
	if s := storm(b); s.Peak < 49 || s.Peak > 52 || s.Storm {
		t.Errorf("150 errors in three hours: %+v", s)
	}
	// One record that stands for many errors.
	b = newBuilder()
	e := b.event("d1", t0.Add(day), cellA)
	e.Count = 500
	b.add(e)
	if s := storm(b); s.Peak != 500 || !s.Storm {
		t.Errorf("one record of 500: %+v", s)
	}
	// Counter growth is spread over its counting window.
	counter := func(n int, window time.Duration) *builder {
		b := newBuilder()
		e := b.event("d1", t0.Add(2*day), loc{-1, -1, -1, -1, -1, -1})
		e.Kind, e.Count, e.WindowStart = model.KindCounter, n, t0.Add(2*day-window)
		b.add(e)
		return b
	}
	if s := storm(counter(1200, 24*time.Hour)); s.Peak != 50 || s.Storm {
		t.Errorf("1200 errors counted over a day: %+v", s)
	}
	if s := storm(counter(1200, 30*time.Minute)); s.Peak != 1200 || !s.Storm {
		t.Errorf("1200 errors counted over half an hour: %+v", s)
	}
	// The threshold is configuration.
	cfg := DefaultConfig()
	cfg.StormCount, cfg.StormMinutes = 40, 60
	b = newBuilder()
	b.spreadOver(cellA, 150, t0.Add(day), t0.Add(day+3*time.Hour))
	if s := analyze(t, b.in, Options{AsOf: asOf, Config: &cfg}).Host("h1").DIMM("d1").Features.Storm; !s.Storm {
		t.Errorf("with a threshold of 40: %+v", s)
	}
	if s := storm(newBuilderWith("d1")); s != (Storm{}) {
		t.Errorf("no dated errors: %+v", s)
	}
}

// newBuilderWith returns a builder that knows the DIMM through a placement
// and has no errors for it.
func newBuilderWith(uid string) *builder {
	b := newBuilder()
	b.in.host("h1")
	b.in.DIMMs = append(b.in.DIMMs, model.DIMM{UID: uid, Identity: model.IdentitySerial, PartNumber: "PN-1", Manufacturer: "M"})
	b.in.Placements = append(b.in.Placements, model.Placement{ID: 1, DIMMUID: uid, Host: "h1", Slot: "S-" + uid, FirstSeen: t0, LastSeen: t0.Add(5 * day)})
	return b
}

func TestDataLines(t *testing.T) {
	asOf := t0.Add(10 * day)
	vec := func(b *builder, l loc, at time.Duration, v string) {
		e := b.event("d1", t0.Add(at), l)
		e.ErrorVector = v
		b.add(e)
	}
	lines := func(b *builder) DataLines { return analyzeD1(t, b, asOf).Features.Lines }

	// No record names failing bits.
	b := newBuilder()
	b.ce("d1", t0, cellA)
	if got := lines(b); got != (DataLines{}) {
		t.Errorf("no vectors: %+v", got)
	}
	// One data line, again and again.
	b = newBuilder()
	for k := range 5 {
		vec(b, cellA, time.Duration(k)*day, "dq:0x4")
	}
	if got := lines(b); got != (DataLines{Known: true, Lines: 1}) {
		t.Errorf("one line: %+v", got)
	}
	// Two data lines in one record.
	b = newBuilder()
	vec(b, cellA, 0, "dq:0xc")
	vec(b, cellA, day, "dq:0xc")
	if got := lines(b); got != (DataLines{Known: true, Lines: 2, Multi: true, MultiRecords: 2}) {
		t.Errorf("two lines per record: %+v", got)
	}
	// Two data lines at one address, in separate records.
	b = newBuilder()
	vec(b, cellA, 0, "dq:0x1")
	vec(b, cellA, day, "dq:0x8")
	if got := lines(b); got != (DataLines{Known: true, Lines: 2, Multi: true}) {
		t.Errorf("two lines at one address: %+v", got)
	}
	// Different data lines at different addresses of one device: several
	// lines, and no address with more than one.
	b = newBuilder()
	vec(b, cellA, 0, "dq:0x1")
	vec(b, loc{0, 3, 1, 2, 9000, 64}, day, "dq:0x2")
	vec(b, loc{0, 3, 1, 2, 9500, 64}, 2*day, "dq:0x4")
	if got := lines(b); got != (DataLines{Known: true, Lines: 3}) {
		t.Errorf("three lines at three addresses: %+v", got)
	}
	// The same line of two devices is one line per device.
	b = newBuilder()
	vec(b, cellA, 0, "dq:0x1")
	vec(b, loc{0, 9, 1, 2, 5000, 640}, day, "dq:0x1")
	if got := lines(b); got != (DataLines{Known: true, Lines: 1}) {
		t.Errorf("one line in two devices: %+v", got)
	}
	// Bit positions with the device named: two positions in one device at
	// one address.
	b = newBuilder()
	vec(b, cellA, 0, "bit:12")
	vec(b, cellA, day, "bit:14")
	if got := lines(b); got != (DataLines{Known: true, Lines: 2, Multi: true}) {
		t.Errorf("two bits of one device: %+v", got)
	}
	// Bit positions without the device: the device width tells which
	// device a position falls into.
	noDev := loc{0, -1, 1, 2, 5000, 640}
	b = newBuilder()
	b.in.DIMMs = []model.DIMM{{UID: "d1", DeviceWidth: model.Some(4)}}
	vec(b, noDev, 0, "bit:12")   // device 3
	vec(b, noDev, day, "bit:14") // device 3
	vec(b, noDev, 2*day, "bit:40")
	if got := lines(b); got != (DataLines{Known: true, Lines: 2, Multi: true}) {
		t.Errorf("bits with a device width: %+v", got)
	}
	// Without device and width nothing can be said about lines.
	b = newBuilder()
	vec(b, noDev, 0, "bit:12")
	vec(b, noDev, day, "bit:14")
	if got := lines(b); got != (DataLines{Known: true}) {
		t.Errorf("bits without device and width: %+v", got)
	}
}

func TestScrubSplit(t *testing.T) {
	b := newBuilder()
	add := func(n int, st model.ScrubType) {
		for k := range n {
			e := b.event("d1", t0.Add(time.Duration(k)*time.Hour), cellA)
			e.ErrorScrubType = st
			b.add(e)
		}
	}
	add(6, model.ScrubPatrol)
	add(3, model.ScrubDemand)
	add(1, model.ScrubOther)
	add(5, "")
	d := analyzeD1(t, b, t0.Add(5*day))
	want := Scrub{Patrol: 6, Demand: 3, Other: 1, Unknown: 5}
	if d.Features.Scrub != want || d.Faults[0].Scrub != want {
		t.Errorf("scrub = %+v", d.Features.Scrub)
	}
	if share, ok := want.PatrolShare(); !ok || share != 0.4 {
		t.Errorf("patrol share = %g, %v; want 6 of 15", share, ok)
	}
	if _, ok := (Scrub{Unknown: 9}).PatrolShare(); ok {
		t.Error("a share without any record that says how the error was found")
	}
}

// action builds an action on DIMM d1 in its slot.
func action(id int64, kind model.ActionKind, at time.Time) model.Action {
	return model.Action{ID: id, Host: "h1", At: at, Kind: kind, Result: model.ResultOK, Slot: "S-d1", DIMMUID: "d1",
		Source: "test", RawRef: "action#" + string(rune('0'+id))}
}

func TestMitigations(t *testing.T) {
	row := func(r, col int) loc { return loc{0, -1, 1, 2, r, col} } // the platform does not report the device
	asOf := t0.Add(40 * day)
	build := func() *builder {
		b := newBuilder()
		b.observedSince(t0)
		// A row fault in row 5000: errors before and after day 10.
		for k := range 20 {
			e := b.event("d1", t0.Add(time.Duration(k)*day), row(5000, 64*(1+k%4)))
			e.OsPhysicalAddress = model.Some(uint64(0x1_0000_0000) + uint64(k%4)*4096)
			b.add(e)
		}
		// An unrelated weak cell in another bank.
		for k := range 6 {
			e := b.event("d1", t0.Add(time.Duration(k)*2*day), loc{1, -1, 0, 0, 77, 8})
			e.OsPhysicalAddress = model.Some(uint64(0x3_0000_0000))
			b.add(e)
		}
		return b
	}

	b := build()
	ppr := action(1, model.ActionHPPR, t0.Add(10*day+time.Hour))
	ppr.Rank, ppr.DeviceId, ppr.BankGroup, ppr.Bank, ppr.Row = model.Some(0), model.Some(7), model.Some(1), model.Some(2), model.Some(5000)
	other := action(2, model.ActionSPPR, t0.Add(11*day))
	other.Rank, other.BankGroup, other.Bank, other.Row = model.Some(0), model.Some(1), model.Some(2), model.Some(4999) // another row
	noPlace := action(3, model.ActionHPPR, t0.Add(12*day))                                                             // names no place
	off := action(4, model.ActionPageOffline, t0.Add(15*day+time.Hour))
	off.OsPhysicalAddress = model.Some(uint64(0x1_0000_0000) + 4096)
	offFailed := action(5, model.ActionPageOffline, t0.Add(16*day))
	offFailed.OsPhysicalAddress, offFailed.Result = model.Some(uint64(0x1_0000_0000)), model.ResultFailed
	offElsewhere := action(6, model.ActionPageOffline, t0.Add(17*day))
	offElsewhere.OsPhysicalAddress = model.Some(uint64(0x9_0000_0000)) // a page with no errors
	late := action(7, model.ActionHPPR, asOf.Add(day))                 // after the reference time
	late.Row = model.Some(5000)
	b.in.Actions = []model.Action{ppr, other, noPlace, off, offFailed, offElsewhere, late}

	d := analyzeD1(t, b, asOf)
	if len(d.Faults) != 2 || d.Faults[0].Mode != model.ModeRow || d.Faults[1].Mode != model.ModeSingleBit {
		t.Fatalf("faults:%s", describe(d.Faults))
	}
	ms := d.Faults[0].Mitigations
	if len(ms) != 3 {
		t.Fatalf("mitigations of the row fault = %+v", ms)
	}
	// The repair of row 5000: nine errors on days 11 to 19 came after it.
	if m := ms[0]; m.ActionID != 1 || m.Kind != model.ActionHPPR || m.Row != model.Some(5000) || m.ErrorsAfter != 9 || m.FaultErrorsAfter != 9 ||
		!m.At.Equal(ppr.At) || m.RawRef != ppr.RawRef || len(m.after) != 9 {
		t.Errorf("repair = %+v", m)
	}
	// The page taken offline on day 15: errors of days 16 to 19, one of
	// them (day 17, column 2) on that page.
	if m := ms[1]; m.ActionID != 4 || m.Kind != model.ActionPageOffline || m.ErrorsAfter != 1 || m.FaultErrorsAfter != 4 || m.Row.Valid {
		t.Errorf("page offline = %+v", m)
	}
	// A failed attempt still names a place of the fault.
	if m := ms[2]; m.ActionID != 5 || m.Result != model.ResultFailed {
		t.Errorf("failed page offline = %+v", m)
	}
	if len(d.Faults[1].Mitigations) != 0 {
		t.Errorf("mitigations of the unrelated cell = %+v", d.Faults[1].Mitigations)
	}
	// The DIMM's pages: two taken offline on two days, one attempt failed.
	wantOff := Offline{Pages: 2, Failed: 1, Days: 2, LastNew: offElsewhere.At}
	if d.Features.Offline != wantOff {
		t.Errorf("offline = %+v, want %+v", d.Features.Offline, wantOff)
	}

	// A repair that names a rank only covers every fault of that rank.
	b = build()
	sparing := action(1, model.ActionSparing, t0.Add(10*day+time.Hour))
	sparing.Rank = model.Some(1)
	b.in.Actions = []model.Action{sparing}
	d = analyzeD1(t, b, asOf)
	if len(d.Faults[0].Mitigations) != 0 || len(d.Faults[1].Mitigations) != 1 || d.Faults[1].Mitigations[0].ErrorsAfter != 0 {
		t.Errorf("sparing of rank 1: row fault %+v, cell %+v", d.Faults[0].Mitigations, d.Faults[1].Mitigations)
	}
	if d.Features.Offline != (Offline{}) {
		t.Errorf("offline without page offlines = %+v", d.Features.Offline)
	}
}

// TestRepetition: the share of a DIMM's errors that lie in faults whose
// place failed again.
func TestRepetition(t *testing.T) {
	b := newBuilder()
	b.spreadOver(cellA, 30, t0, t0.Add(30*day))
	for i := range 10 {
		b.ce("d1", t0.Add(time.Duration(i)*40*day), loc{1, i, i % 8, i % 4, 100 + i*5000, 8 * i})
	}
	d := analyzeD1(t, b, t0.Add(400*day))
	if d.Features.Repetition != 0.75 || len(d.Faults) != 11 {
		t.Errorf("repetition = %g with %d faults", d.Features.Repetition, len(d.Faults))
	}
}

// TestFeaturesOfTheScenarios checks the measurements on generated
// histories whose course is known.
func TestFeaturesOfTheScenarios(t *testing.T) {
	primary := func(name string) (*DIMMResult, *Fault) {
		ds := generate(t, name, 1, noNoise)
		res := analyze(t, inputOf(ds), Options{AsOf: ds.Scenario.End})
		d := res.Hosts[0].DIMM(uidOf(ds.Truth[0].Serial))
		if d == nil || len(d.Faults) == 0 {
			t.Fatalf("%s: no result", name)
		}
		return d, d.Faults[0]
	}

	// The demo: a row fault that grows, is repaired, comes back, has pages
	// taken offline and ends in an uncorrected error.
	ds := generate(t, "demo", 1, nil)
	in := inputOf(ds)
	in.Actions = append(in.Actions, ds.Actions...)
	for i := range in.Actions {
		in.Actions[i].ID = int64(i + 1)
		in.Actions[i].DIMMUID = uidOf(ds.Truth[0].Serial)
	}
	in.Boots = append(in.Boots, ds.Boots...)
	res := analyze(t, in, Options{AsOf: ds.Scenario.End})
	d := res.Hosts[0].DIMM(uidOf(ds.Truth[0].Serial))
	f := d.Faults[0]
	if f.Trend.Direction != TrendRising || !f.Spread.Spreading || f.Spread.NewColumns == 0 || f.Spread.NewRows != 1 {
		t.Errorf("demo: trend %+v spread %+v", f.Trend, f.Spread)
	}
	if !f.Lines.Multi || f.Lines.Lines != 2 || f.Lines.MultiRecords != 1232 {
		t.Errorf("demo: data lines %+v, want two lines in every record", f.Lines)
	}
	if d.Features.UE != 1 || d.Features.CE != 1231 || d.Features.Repetition != 1 || d.Features.Offline.Pages != 6 || d.Features.Offline.Days != 1 {
		t.Errorf("demo: features %+v", d.Features)
	}
	var repair *Mitigation
	offlines := 0
	for i := range f.Mitigations {
		switch f.Mitigations[i].Kind {
		case model.ActionHPPR:
			repair = &f.Mitigations[i]
		case model.ActionPageOffline:
			offlines++
		}
	}
	if repair == nil || repair.Row != model.Some(0x1a2f) || repair.ErrorsAfter < 500 || repair.ErrorsAfter > repair.FaultErrorsAfter || offlines != 7 {
		t.Errorf("demo: mitigations %+v", f.Mitigations)
	}
	if share, ok := d.Features.Scrub.PatrolShare(); !ok || share < 0.1 || share > 0.2 {
		t.Errorf("demo: patrol share %g, generated with 0.15", share)
	}
	// The neighbour with the weak cell: one place, no growth.
	n := res.Hosts[0].DIMM(uidOf(ds.Truth[1].Serial))
	if nf := n.Faults[0]; nf.Spread.Spreading || nf.Spread.Cells != 1 || nf.Trend.Rising(&res.Config) || nf.Lines.Multi || len(nf.Mitigations) != 0 {
		t.Errorf("demo neighbour: trend %+v spread %+v lines %+v", nf.Trend, nf.Spread, nf.Lines)
	}

	// Generated with a rising rate.
	for _, name := range []string{"row", "multi-row", "bank"} {
		if _, f := primary(name); !f.Trend.Rising(&res.Config) || f.Trend.Slope <= 0 || !f.Spread.Spreading {
			t.Errorf("%s: trend %+v spread %+v", name, f.Trend, f.Spread)
		}
	}
	// Generated with a flat rate: not rising, and still reaching new places.
	for _, name := range []string{"column", "moved-dimm"} {
		if _, f := primary(name); f.Trend.Rising(&res.Config) || f.Trend.Direction == TrendUnknown || !f.Spread.Spreading {
			t.Errorf("%s: trend %+v spread %+v", name, f.Trend, f.Spread)
		}
	}
	// One cell at a flat rate.
	if _, f := primary("single-bit"); f.Trend.Rising(&res.Config) || f.Spread.Spreading || f.Spread.Cells != 1 {
		t.Errorf("single-bit: trend %+v spread %+v", f.Trend, f.Spread)
	}
	// A storm at the end of the history.
	if d, f := primary("device"); !f.Trend.Rising(&res.Config) || f.Trend.Ratio < 10 || d.Features.Storm.Peak < 20 || d.Features.UE != 1 {
		t.Errorf("device: trend %+v storm %+v", f.Trend, d.Features.Storm)
	}
}

// TestFeaturesOnPlatforms: what a platform does not report is not measured.
func TestFeaturesOnPlatforms(t *testing.T) {
	ds := generate(t, "demo", 1, nil)
	uid := uidOf(ds.Truth[0].Serial)
	for _, fl := range synth.Flavors {
		res := analyze(t, load(t, sharedLedger(t, "demo", 1, fl)), Options{})
		d := res.Hosts[0].DIMM(uid)
		if d == nil || len(d.Faults) != 1 {
			t.Fatalf("%s: no result", fl)
		}
		f, ft := d.Faults[0], d.Features
		// Counts, trend and page offlines do not depend on the platform.
		if ft.CE != 1231 || ft.UE != 1 || ft.Trend.Direction != TrendRising || ft.Trend.Recent != 421 || ft.Offline.Pages != 6 || !f.Spread.Spreading {
			t.Errorf("%s: features %+v spread %+v", fl, ft, f.Spread)
		}
		wantGrades := [3]int64{1232, 0, 0}
		if fl == synth.FlavorAMD {
			wantGrades = [3]int64{0, 1232, 0}
		}
		if got := [3]int64{ft.GradeA, ft.GradeB, ft.GradeC}; got != wantGrades {
			t.Errorf("%s: errors by grade = %v, want %v", fl, got, wantGrades)
		}
		// Failing bits are only reported through CPER.
		if known := fl == synth.FlavorGHES; ft.Lines.Known != known {
			t.Errorf("%s: data lines %+v", fl, ft.Lines)
		}
		// The Intel error type says how every error was found. Firmware
		// reports mark the scrubber's finds and say nothing about the
		// rest. The AMD records say nothing.
		share, ok := ft.Scrub.PatrolShare()
		switch fl {
		case synth.FlavorIntel:
			if !ok || ft.Scrub != (Scrub{Patrol: 193, Demand: 1039}) {
				t.Errorf("%s: scrub %+v", fl, ft.Scrub)
			}
		case synth.FlavorGHES:
			if !ok || ft.Scrub != (Scrub{Patrol: 193, Unknown: 1039}) || share > 0.16 {
				t.Errorf("%s: scrub %+v, share %g", fl, ft.Scrub, share)
			}
		default:
			if ok || ft.Scrub != (Scrub{Unknown: 1232}) {
				t.Errorf("%s: scrub %+v", fl, ft.Scrub)
			}
		}
		// The kernel log and the memory failure table report page offlines
		// with addresses, which tie them to the fault on every platform.
		if len(f.Mitigations) < 7 {
			t.Errorf("%s: %d mitigations", fl, len(f.Mitigations))
		}
		// All history is one scan of one boot: the DIMM was seen in the
		// slot at scan time only, so earlier errors are assumed to be its.
		if ft.Assumed != 1232 {
			t.Errorf("%s: %d errors attributed by assumption", fl, ft.Assumed)
		}
	}
}

func TestSlope(t *testing.T) {
	if s := slope([]float64{1, 2, 3, 4, 5}); math.Abs(s-1) > 1e-12 {
		t.Errorf("slope of 1..5 = %g", s)
	}
	if s := slope([]float64{9, 6, 3, 0}); math.Abs(s+3) > 1e-12 {
		t.Errorf("slope of 9,6,3,0 = %g", s)
	}
	if s := slope([]float64{4, 4, 4}); s != 0 {
		t.Errorf("slope of a flat series = %g", s)
	}
	if s := slope([]float64{1, 100}); s != 0 {
		t.Errorf("slope of two points = %g, want 0: too short to tell", s)
	}
}

func TestMidRank(t *testing.T) {
	tests := []struct {
		values []float64
		x      float64
		want   float64
	}{
		{nil, 3, 0},
		{[]float64{0, 0, 0, 0}, 0, 50},
		{[]float64{0, 0, 0, 0}, 1, 100},
		{[]float64{1, 2, 3, 4}, 0, 0},
		{[]float64{1, 2, 3, 4}, 3, 62.5},
		{[]float64{0, 0, 0, 5}, 5, 87.5},
	}
	for _, tt := range tests {
		if got := midRank(tt.values, tt.x); got != tt.want {
			t.Errorf("midRank(%v, %g) = %g, want %g", tt.values, tt.x, got, tt.want)
		}
	}
}
