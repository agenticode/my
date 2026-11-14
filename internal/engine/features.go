package engine

import (
	"math/bits"
	"sort"
	"time"

	"github.com/agenticode/my/internal/model"
)

// Features are the measurements the rules work on. Each one is computed
// from ledger records and a reference time, and each can be checked by
// counting those records.

// Trend directions.
const (
	// TrendUnknown: the ledger covers too little of the baseline window to
	// compare against.
	TrendUnknown = "unknown"
	// TrendQuiet: no errors in the recent window.
	TrendQuiet = "quiet"
	// TrendNew: errors in the recent window and none in the baseline window.
	TrendNew = "new"
	// TrendRising: the recent rate is at least RiseRatio times the baseline
	// rate, with at least RiseMinRecent recent errors.
	TrendRising = "rising"
	// TrendFalling: the recent rate is at most the baseline rate divided
	// by RiseRatio.
	TrendFalling = "falling"
	// TrendSteady: none of the above.
	TrendSteady = "steady"
)

// Trend compares the errors of the recent window with the baseline window
// before it.
type Trend struct {
	Direction string `json:"direction"`
	// Recent and Prior are the errors in the recent and the baseline window.
	Recent int64 `json:"recent"`
	Prior  int64 `json:"prior"`
	// RecentPerDay and PriorPerDay are the daily rates. The baseline rate
	// is taken over PriorDays, the part of the baseline window the ledger
	// covers.
	RecentPerDay float64 `json:"recent_per_day"`
	PriorPerDay  float64 `json:"prior_per_day"`
	PriorDays    float64 `json:"prior_days"`
	// Ratio is RecentPerDay divided by PriorPerDay, or 0 when the baseline
	// window holds no errors.
	Ratio float64 `json:"ratio,omitzero"`
	// Slope is the least-squares slope of the daily counts over both
	// windows, in errors per day per day.
	Slope float64 `json:"slope"`
}

// Rising reports whether the trend counts as growth: rising against the
// baseline, or new with enough errors.
func (t Trend) Rising(cfg *Config) bool {
	return t.Direction == TrendRising || (t.Direction == TrendNew && t.Recent >= int64(cfg.RiseMinRecent))
}

// Spread says how far the places of a fault have grown.
type Spread struct {
	// Cells is the number of distinct places over the life of the fault.
	Cells int `json:"cells"`
	// NewCells is the number of places that failed for the first time
	// within the spread window, not counting the places of the fault's
	// first day. NewRows and NewColumns count new rows and columns the
	// same way.
	NewCells   int `json:"new_cells"`
	NewRows    int `json:"new_rows,omitzero"`
	NewColumns int `json:"new_columns,omitzero"`
	// Spreading is set when NewCells reaches SpreadMinNew.
	Spreading bool `json:"spreading"`
}

// Storm describes the densest burst of errors.
type Storm struct {
	// Peak is the largest number of errors within one storm window.
	Peak int64 `json:"peak"`
	// At is the start of that window.
	At time.Time `json:"at,omitzero"`
	// Storm is set when Peak reaches StormCount.
	Storm bool `json:"storm"`
}

// DataLines summarises which data lines (DQ) of a device failed, on
// platforms that report failing bits.
type DataLines struct {
	// Known is set when at least one record names failing bits.
	Known bool `json:"known"`
	// Lines is the largest number of distinct data lines seen failing in
	// one device.
	Lines int `json:"lines,omitzero"`
	// Multi is set when more than one data line of one device failed at
	// one address. A single failing cell does not do that.
	Multi bool `json:"multi"`
	// MultiRecords is the number of records that show two or more failing
	// data lines by themselves.
	MultiRecords int `json:"multi_records,omitzero"`
}

// Scrub splits errors by how they were found.
type Scrub struct {
	Patrol  int64 `json:"patrol"`
	Demand  int64 `json:"demand"`
	Other   int64 `json:"other,omitzero"`
	Unknown int64 `json:"unknown,omitzero"`
}

// PatrolShare returns the share of errors the patrol scrubber found, among
// the errors whose records say how they were found. ok is false when no
// record says.
func (s Scrub) PatrolShare() (share float64, ok bool) {
	n := s.Patrol + s.Demand + s.Other
	if n == 0 {
		return 0, false
	}
	return float64(s.Patrol) / float64(n), true
}

// Mitigation is a repair or isolation step that names a place of a fault:
// a repair of one of its rows, or a page of it taken offline.
type Mitigation struct {
	ActionID int64              `json:"action_id"`
	Kind     model.ActionKind   `json:"kind"`
	Method   string             `json:"method,omitzero"`
	Result   model.ActionResult `json:"result"`
	At       time.Time          `json:"at"`
	RawRef   string             `json:"raw_ref,omitzero"`
	// Row is the repaired row, for repairs that name one.
	Row model.Opt[int] `json:"row,omitzero"`
	// ErrorsAfter counts the errors of the fault after the step at the
	// place the step names: the repaired row, the page taken offline.
	ErrorsAfter int64 `json:"errors_after"`
	// FaultErrorsAfter counts all errors of the fault after the step.
	FaultErrorsAfter int64 `json:"fault_errors_after"`

	// after are the member records behind ErrorsAfter.
	after []int32
}

// Offline summarises the pages of a DIMM that were taken offline.
type Offline struct {
	// Pages is the number of distinct pages. Attempts that failed do not
	// count; they are in Failed.
	Pages  int `json:"pages"`
	Failed int `json:"failed,omitzero"`
	// Days is the number of days on which a page was taken offline for the
	// first time, and LastNew the last such day.
	Days    int       `json:"days,omitzero"`
	LastNew time.Time `json:"last_new,omitzero"`
}

// Features are the measurements of one DIMM over all its errors.
type Features struct {
	CE int64 `json:"ce"`
	UE int64 `json:"ue"`
	// GradeA, GradeB and GradeC split the errors by observation grade.
	GradeA int64 `json:"grade_a"`
	GradeB int64 `json:"grade_b"`
	GradeC int64 `json:"grade_c"`
	// First and Last bound the dated errors.
	First time.Time `json:"first,omitzero"`
	Last  time.Time `json:"last,omitzero"`
	// Undated counts errors whose records carry seconds since boot and no
	// date. They are in the totals and in no trend.
	Undated int64 `json:"undated,omitzero"`
	// Assumed counts errors attributed to the DIMM although it was only
	// seen in the slot at another time; SlotOnly counts errors of a slot
	// whose DIMM has no known serial number.
	Assumed  int64 `json:"assumed,omitzero"`
	SlotOnly int64 `json:"slot_only,omitzero"`

	Trend Trend     `json:"trend"`
	Storm Storm     `json:"storm"`
	Scrub Scrub     `json:"scrub"`
	Lines DataLines `json:"data_lines"`
	// Repetition is the share of the errors that lie in faults whose place
	// failed again later, from 0 to 1.
	Repetition float64 `json:"repetition"`
	Offline    Offline `json:"offline"`
}

// clock holds the reference time of a host and the windows derived from it.
type clock struct {
	asOf int64
	// from is the earliest time the ledger knows something about the host.
	from       int64
	recentFrom int64
	priorFrom  int64
	spreadFrom int64
}

func newClock(asOf, from int64, cfg *Config) clock {
	c := clock{asOf: asOf, from: from}
	c.recentFrom = asOf - days(cfg.RecentDays)
	c.priorFrom = c.recentFrom - days(cfg.BaselineDays)
	c.spreadFrom = asOf - days(cfg.SpreadDays)
	return c
}

// priorDays returns how many days of the baseline window the ledger covers.
func (c clock) priorDays() float64 {
	start := max(c.priorFrom, c.from)
	if start >= c.recentFrom {
		return 0
	}
	return float64(c.recentFrom-start) / float64(microPerDay)
}

// measurer computes features for the records of one DIMM.
type measurer struct {
	in  *Input
	cfg *Config
	clk clock
}

// trend compares the recent window with the baseline window for the
// records idx.
func (m *measurer) trend(idx []int32) Trend {
	c := m.clk
	t := Trend{Direction: TrendUnknown, PriorDays: c.priorDays()}
	start := max(c.priorFrom, c.from)
	nDays := int((c.asOf - start + microPerDay - 1) / microPerDay)
	var daily []float64
	if nDays > 0 {
		daily = make([]float64, nDays)
	}
	for _, i := range idx {
		e := &m.in.events[i]
		if e.relative || e.at > c.asOf || e.at <= c.priorFrom {
			continue
		}
		if e.at > c.recentFrom {
			t.Recent += int64(e.errors)
		} else {
			t.Prior += int64(e.errors)
		}
		if e.at > start && nDays > 0 {
			// Days are counted back from the reference time.
			d := nDays - 1 - int((c.asOf-e.at)/microPerDay)
			if d >= 0 {
				daily[d] += float64(e.errors)
			}
		}
	}
	t.RecentPerDay = float64(t.Recent) / m.cfg.RecentDays
	if t.PriorDays > 0 {
		t.PriorPerDay = float64(t.Prior) / t.PriorDays
	}
	t.Slope = slope(daily)
	if t.PriorDays < m.cfg.MinBaselineDays {
		return t
	}
	if t.Prior > 0 {
		t.Ratio = t.RecentPerDay / t.PriorPerDay
	}
	switch {
	case t.Recent == 0:
		t.Direction = TrendQuiet
	case t.Prior == 0:
		t.Direction = TrendNew
	case t.Ratio >= m.cfg.RiseRatio && t.Recent >= int64(m.cfg.RiseMinRecent):
		t.Direction = TrendRising
	case t.Ratio <= 1/m.cfg.RiseRatio:
		t.Direction = TrendFalling
	default:
		t.Direction = TrendSteady
	}
	return t
}

// slope returns the least-squares slope of y over 0, 1, 2, ...
func slope(y []float64) float64 {
	n := float64(len(y))
	if n < 3 {
		return 0
	}
	var sx, sy, sxx, sxy float64
	for i, v := range y {
		x := float64(i)
		sx, sy, sxx, sxy = sx+x, sy+v, sxx+x*x, sxy+x*v
	}
	return (n*sxy - sx*sy) / (n*sxx - sx*sx)
}

// storm finds the busiest window among the records idx, which must be in
// time order. A counter record is spread evenly over its counting window.
func (m *measurer) storm(idx []int32) Storm {
	w := int64(m.cfg.StormMinutes * float64(time.Minute/time.Microsecond))
	if w <= 0 {
		return Storm{}
	}
	type point struct {
		at int64
		n  float64
	}
	var pts []point
	for _, i := range idx {
		e := &m.in.events[i]
		if e.relative {
			continue
		}
		n := float64(e.errors)
		if e.counter && e.from > 0 && e.at-e.from > w {
			n *= float64(w) / float64(e.at-e.from)
		}
		pts = append(pts, point{e.at, n})
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].at < pts[j].at })
	var best, sum float64
	var at int64
	lo := 0
	for hi := range pts {
		sum += pts[hi].n
		for pts[hi].at-pts[lo].at > w {
			sum -= pts[lo].n
			lo++
		}
		if sum > best {
			best, at = sum, pts[lo].at
		}
	}
	s := Storm{Peak: int64(best + 0.5)}
	if s.Peak > 0 {
		s.At = timeOf(at)
	}
	s.Storm = s.Peak >= int64(m.cfg.StormCount) && m.cfg.StormCount > 0
	return s
}

// spread counts the places of a fault and how many of them are new.
func (m *measurer) spread(f *Fault) Spread {
	evs := m.in.events
	// When each place, row and column failed for the first time.
	cell, row, col := map[cellKey]int64{}, map[cellKey]int64{}, map[cellKey]int64{}
	line := map[addrKey]int64{}
	note := func(mp map[cellKey]int64, k cellKey, at int64) {
		if old, ok := mp[k]; !ok || at < old {
			mp[k] = at
		}
	}
	located := f.Grade == model.GradeA
	var start int64
	dated := false
	for _, i := range f.members {
		e := &evs[i]
		if e.relative || e.at > m.clk.asOf {
			continue
		}
		if !dated || e.at < start {
			start, dated = e.at, true
		}
		switch {
		case located && e.located():
			k := cellKey{e.rank, e.dev, e.bg, e.bank, e.row, e.col}
			note(cell, k, e.at)
			k.col = -1
			note(row, k, e.at)
			k.row, k.col = -1, e.col
			note(col, k, e.at)
		case !located && e.hasAddr:
			// Without rows and columns a place is a cache line address
			// of the slot.
			k := addrKey{e.slot, e.addr >> 6}
			if old, ok := line[k]; !ok || e.at < old {
				line[k] = e.at
			}
		}
	}
	s := Spread{Cells: len(cell) + len(line)}
	if !dated {
		return s
	}
	// A place is new when it first failed inside the spread window and
	// after the first day of the fault.
	cut := max(m.clk.spreadFrom, start+microPerDay)
	count := func(mp map[cellKey]int64) (n int) {
		for _, at := range mp {
			if at > cut {
				n++
			}
		}
		return n
	}
	s.NewCells, s.NewRows, s.NewColumns = count(cell), count(row), count(col)
	for _, at := range line {
		if at > cut {
			s.NewCells++
		}
	}
	s.Spreading = m.cfg.SpreadMinNew > 0 && s.NewCells >= m.cfg.SpreadMinNew
	return s
}

// lines summarises the failing data lines among the records idx.
func (m *measurer) lines(idx []int32, width int) DataLines {
	evs := m.in.events
	var d DataLines
	// Per device and per address: the data lines seen failing.
	perDev := map[devKey]map[uint32]struct{}{}
	perCell := map[cellKey]map[uint32]struct{}{}
	add := func(e *event, dev int16, line uint32) {
		k := devKey{e.rank, dev}
		if perDev[k] == nil {
			perDev[k] = map[uint32]struct{}{}
		}
		perDev[k][line] = struct{}{}
		if e.located() {
			ck := cellKey{e.rank, dev, e.bg, e.bank, e.row, e.col}
			if perCell[ck] == nil {
				perCell[ck] = map[uint32]struct{}{}
			}
			perCell[ck][line] = struct{}{}
		}
	}
	for _, i := range idx {
		e := &evs[i]
		switch e.vecKind {
		case vecDQ:
			// A mask of the data lines of the device the record names.
			d.Known = true
			if bits.OnesCount32(e.vec) >= 2 {
				d.MultiRecords++
				d.Multi = true
			}
			for b := range 32 {
				if e.vec&(1<<b) != 0 {
					add(e, e.dev, uint32(b))
				}
			}
		case vecBit:
			// A bit position within the data word. Two positions belong
			// to one device when the record names the device, or when the
			// device width tells which device a position falls into.
			d.Known = true
			switch {
			case e.dev >= 0:
				add(e, e.dev, e.vec)
			case width > 0:
				add(e, int16(e.vec/uint32(width)), e.vec)
			}
		}
	}
	for _, lines := range perDev {
		d.Lines = max(d.Lines, len(lines))
	}
	for _, lines := range perCell {
		if len(lines) >= 2 {
			d.Multi = true
		}
	}
	return d
}

func (m *measurer) scrub(idx []int32) Scrub {
	var s Scrub
	for _, i := range idx {
		e := &m.in.events[i]
		n := int64(e.errors)
		switch e.scrub {
		case scrubPatrol:
			s.Patrol += n
		case scrubDemand:
			s.Demand += n
		case scrubOther:
			s.Other += n
		default:
			s.Unknown += n
		}
	}
	return s
}

// repairKinds are the actions that repair a place inside a DIMM.
func repairKind(k model.ActionKind) bool {
	switch k {
	case model.ActionSPPR, model.ActionHPPR, model.ActionRowRemap, model.ActionSparing:
		return true
	}
	return false
}

// mitigations finds the actions that name a place of the fault and counts
// the errors that came after each.
func (m *measurer) mitigations(f *Fault, actions []*model.Action) []Mitigation {
	if len(actions) == 0 {
		return nil
	}
	evs := m.in.events
	var out []Mitigation
	for _, a := range actions {
		at := a.At.UnixMicro()
		if at > m.clk.asOf {
			continue
		}
		var match func(e *event) bool
		switch {
		case a.Kind == model.ActionPageOffline && a.OsPhysicalAddress.Valid:
			host, ok := m.in.hostIdx[a.Host]
			if !ok {
				continue
			}
			slot, ok := m.in.slotIdx[slotKey{host, a.Slot}]
			if !ok {
				continue
			}
			page := a.OsPhysicalAddress.V >> 12
			match = func(e *event) bool { return e.hasAddr && e.slot == slot && e.addr>>12 == page }
		case repairKind(a.Kind) && (a.Row.Valid || a.Bank.Valid || a.BankGroup.Valid || a.DeviceId.Valid || a.Rank.Valid):
			// Every field the repair names must agree; a field the record
			// does not report (the device on many platforms) is not held
			// against it.
			same := func(want model.Opt[int], got int32) bool { return !want.Valid || got < 0 || int32(want.V) == got }
			match = func(e *event) bool {
				if a.Row.Valid && (e.row < 0 || int32(a.Row.V) != e.row) {
					return false
				}
				return same(a.Rank, int32(e.rank)) && same(a.DeviceId, int32(e.dev)) && same(a.BankGroup, int32(e.bg)) && same(a.Bank, int32(e.bank))
			}
		default:
			continue
		}
		mt := Mitigation{ActionID: a.ID, Kind: a.Kind, Method: a.Method, Result: a.Result, At: a.At, RawRef: a.RawRef}
		if repairKind(a.Kind) {
			mt.Row = a.Row
		}
		hit := false
		for _, i := range f.members {
			e := &evs[i]
			if e.at > m.clk.asOf {
				continue
			}
			named := match(e)
			hit = hit || named
			if e.relative || e.at <= at {
				continue
			}
			mt.FaultErrorsAfter += int64(e.errors)
			if named {
				mt.ErrorsAfter += int64(e.errors)
				mt.after = append(mt.after, i)
			}
		}
		if hit {
			out = append(out, mt)
		}
	}
	return out
}

// offline summarises the page offline actions of a DIMM.
func (m *measurer) offline(actions []*model.Action) Offline {
	var o Offline
	type page struct {
		slot string
		pfn  uint64
	}
	first := map[page]int64{}
	for _, a := range actions {
		if a.Kind != model.ActionPageOffline || a.At.UnixMicro() > m.clk.asOf {
			continue
		}
		if a.Result == model.ResultFailed {
			o.Failed++
			continue
		}
		if !a.OsPhysicalAddress.Valid {
			continue
		}
		k := page{a.Slot, a.OsPhysicalAddress.V >> 12}
		if at := a.At.UnixMicro(); first[k] == 0 || at < first[k] {
			first[k] = at
		}
	}
	o.Pages = len(first)
	daySet := map[int64]bool{}
	var last int64
	for _, at := range first {
		daySet[at/microPerDay] = true
		last = max(last, at)
	}
	o.Days = len(daySet)
	if last > 0 {
		o.LastNew = timeOf(last)
	}
	return o
}

// measureFault fills in the features of a fault.
func (m *measurer) measureFault(f *Fault, actions []*model.Action, width int) {
	f.Trend = m.trend(f.members)
	f.Storm = m.storm(f.members)
	f.Spread = m.spread(f)
	f.Lines = m.lines(f.members, width)
	f.Scrub = m.scrub(f.members)
	f.Mitigations = m.mitigations(f, actions)
	for _, i := range f.members {
		if e := &m.in.events[i]; e.relative {
			f.Undated += int64(e.errors)
		}
	}
}

// measureDIMM computes the features of a DIMM from all its records, which
// must be in time order, and its faults.
func (m *measurer) measureDIMM(idx []int32, faults []*Fault, actions []*model.Action, width int) Features {
	evs := m.in.events
	var ft Features
	var first, last int64
	var total, recurring int64
	for _, i := range idx {
		e := &evs[i]
		n := int64(e.errors)
		if e.sev == sevCE {
			ft.CE += n
		} else {
			ft.UE += n
		}
		switch {
		case e.located():
			ft.GradeA += n
		case e.counter || (!e.hasAddr && e.rank < 0):
			ft.GradeC += n
		default:
			ft.GradeB += n
		}
		switch e.link {
		case linkAssumed:
			ft.Assumed += n
		case linkSlot:
			ft.SlotOnly += n
		}
		if e.relative {
			ft.Undated += n
			continue
		}
		if first == 0 || e.at < first {
			first = e.at
		}
		last = max(last, e.at)
	}
	if first > 0 {
		ft.First, ft.Last = timeOf(first), timeOf(last)
	}
	for _, f := range faults {
		total += int64(f.ErrorCount)
		if f.Recurring {
			recurring += int64(f.ErrorCount)
		}
	}
	if total > 0 {
		ft.Repetition = float64(recurring) / float64(total)
	}
	ft.Trend = m.trend(idx)
	ft.Storm = m.storm(idx)
	ft.Scrub = m.scrub(idx)
	ft.Lines = m.lines(idx, width)
	ft.Offline = m.offline(actions)
	return ft
}
