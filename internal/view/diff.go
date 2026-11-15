package view

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// Comparison sets two cases of one host side by side: the host before
// something was done to it, and after. It answers what a reseat, a slot
// swap or a firmware update is done for: whether the errors went with the
// DIMM, stayed with the slot, went on, or stopped.
//
// The two cases are analysed apart, each by the engine on its own ledger,
// and nothing is merged. What the earlier case holds counts as before.
// From the later case only the errors of a period that surely lies after
// the change count as after: the time since the last restart before the
// later case was taken, because a DIMM cannot change its slot and a BIOS
// cannot change its version without one.
type Comparison struct {
	// Host is the name of the host in the later case, and HostBefore its
	// name in the earlier case when that is another one.
	Host       string    `json:"host"`
	HostBefore string    `json:"host_before,omitzero"`
	Before     CaseState `json:"before"`
	After      CaseState `json:"after"`
	// ApartDays is the time between the two cases in days.
	ApartDays float64 `json:"apart_days"`

	// Since is where the period that counts as after begins, and SinceBy
	// how it was chosen (Since* constants).
	Since   time.Time `json:"since"`
	SinceBy string    `json:"since_by"`
	// AfterDays is the length of that period. BeforeDays is the length of
	// the window of the earlier case it is compared with: as long as the
	// period after, or as much of it as the earlier case covers.
	AfterDays  float64 `json:"after_days"`
	BeforeDays float64 `json:"before_days"`
	// Period says the same as a sentence.
	Period Text `json:"period"`

	// Restarts are the boots of the later case that began after the
	// earlier case was taken, oldest first.
	Restarts []Restart `json:"restarts,omitzero"`
	// Changes lists what differs between the cases: DIMMs that moved, left
	// or came, firmware and kernel versions, and the actions the later
	// case recorded after the earlier one was taken.
	Changes []Change `json:"changes"`
	// DIMMs holds one line per DIMM that has errors, a finding or a change
	// of place; with CompareOptions.All, every DIMM.
	DIMMs []ComparedDIMM `json:"dimms"`
	// NotListed counts the DIMMs that were left out.
	NotListed int `json:"not_listed"`
	// Between counts, per slot, the errors of the later case that lie
	// after the earlier case and before Since. They are on neither side.
	// Errors that name no slot are the last entry, with an empty slot.
	// BetweenPlaced says that the DIMMs were where the earlier case has
	// them during that time: only one restart lies between the cases, or
	// the reader gave the time of the change. The lines of the DIMMs then
	// count these errors too.
	Between       []SlotErrors `json:"between,omitzero"`
	BetweenPlaced bool         `json:"between_placed,omitzero"`
	// Readings say what the counts of a DIMM show.
	Readings []Text `json:"readings,omitzero"`
	Notes    []Text `json:"notes,omitzero"`

	// Grade is the finding for the host in the later case.
	Grade     model.VerdictGrade `json:"grade"`
	GradeText string             `json:"grade_text"`

	// after and before are the lengths behind AfterDays and BeforeDays.
	after, before time.Duration
}

// Spans returns the length of the window before and of the period after.
func (c *Comparison) Spans() (before, after time.Duration) { return c.before, c.after }

// How the start of the period after was chosen.
const (
	// SinceRestart: the last restart before the later case was taken.
	SinceRestart = "restart"
	// SinceGiven: the reader named the time of the change.
	SinceGiven = "given"
	// SinceCase: the time of the earlier case. The later case records no
	// restart after it.
	SinceCase = "case"
)

// CaseState describes one of the two cases.
type CaseState struct {
	// File is the file the case was read from, and Anonymized says that it
	// is a bundle whose host names were replaced. The caller sets both.
	File       string `json:"file,omitzero"`
	Anonymized bool   `json:"anonymized,omitzero"`
	// AsOf is the latest time the case knows something about the host.
	AsOf      time.Time          `json:"as_of"`
	Grade     model.VerdictGrade `json:"grade"`
	GradeText string             `json:"grade_text"`
	// DIMMs is the number of DIMMs in the host, CE and UE their errors.
	DIMMs int   `json:"dimms"`
	CE    int64 `json:"ce"`
	UE    int64 `json:"ue"`
	// BootedAt is the start of the boot the case was taken in, with what
	// that boot recorded about kernel and firmware.
	BootedAt time.Time `json:"booted_at,omitzero"`
	Kernel   string    `json:"kernel,omitzero"`
	BIOS     string    `json:"bios,omitzero"`
	BMC      string    `json:"bmc,omitzero"`
}

// Restart is a boot between the two cases.
type Restart struct {
	At     time.Time `json:"at"`
	Kernel string    `json:"kernel,omitzero"`
	BIOS   string    `json:"bios,omitzero"`
}

// Kinds of change.
const (
	ChangeMoved   = "moved"
	ChangeRemoved = "removed"
	ChangeAdded   = "added"
	ChangeBIOS    = "bios"
	ChangeBMC     = "bmc"
	ChangeKernel  = "kernel"
	ChangeAction  = "action"
)

// Change is one thing that differs between the cases.
type Change struct {
	// Kind is one of the Change* constants.
	Kind string `json:"kind"`
	// DIMM names the module of a change of place.
	DIMM string `json:"dimm,omitzero"`
	// From and To are the slots of a move, the slot a DIMM left (From) or
	// came into (To), or the two versions of firmware or kernel.
	From string `json:"from,omitzero"`
	To   string `json:"to,omitzero"`
	// Action, with its text, is the kind of a recorded action; Slot is
	// where it was taken, N how many units (pages, rows) it covers, and
	// First and Last the times of the records.
	Action     model.ActionKind `json:"action,omitzero"`
	ActionText string           `json:"action_text,omitzero"`
	Slot       string           `json:"slot,omitzero"`
	N          int              `json:"n,omitzero"`
	First      time.Time        `json:"first,omitzero"`
	Last       time.Time        `json:"last,omitzero"`
}

// ErrorCount is a number of corrected and uncorrected errors.
type ErrorCount struct {
	CE int64 `json:"ce"`
	UE int64 `json:"ue"`
}

// Total returns the number of errors.
func (e ErrorCount) Total() int64 { return e.CE + e.UE }

func (e *ErrorCount) add(o ErrorCount) { e.CE += o.CE; e.UE += o.UE }

// SlotErrors is a number of errors recorded in one slot.
type SlotErrors struct {
	Slot string `json:"slot"`
	ErrorCount
}

// ComparedDIMM is one DIMM in the two cases.
type ComparedDIMM struct {
	DIMM DIMMInfo `json:"dimm"`
	// Name is how the lines about the DIMM name it: by the end of its
	// serial number, or by its slot when no serial number is known.
	Name string `json:"name"`
	// SlotBefore and SlotAfter are the slots the DIMM is in. An empty one
	// says that the DIMM is not in the host in that case.
	SlotBefore string `json:"slot_before,omitzero"`
	SlotAfter  string `json:"slot_after,omitzero"`
	// Change is ChangeMoved, ChangeRemoved or ChangeAdded, or empty for a
	// DIMM that is in the same slot in both cases.
	Change string `json:"change,omitzero"`
	// BySlot is set when one of the cases has no serial number for the
	// DIMM: the two were then taken to be one DIMM because they are in the
	// same slot.
	BySlot bool `json:"by_slot,omitzero"`
	// Before counts every error of the DIMM in the earlier case, Recent
	// those of the window before the earlier case was taken, and After
	// the errors of the period after in the later case. Between counts
	// the errors of the slot the DIMM had in the earlier case from then
	// to the start of the period after, when Comparison.BetweenPlaced is
	// set.
	Before  ErrorCount `json:"before"`
	Recent  ErrorCount `json:"recent"`
	Between ErrorCount `json:"between,omitzero"`
	After   ErrorCount `json:"after"`
	// GradeBefore and GradeAfter are the findings of the two cases. One is
	// empty when the DIMM is not in the host in that case.
	GradeBefore     model.VerdictGrade `json:"grade_before,omitzero"`
	GradeBeforeText string             `json:"grade_before_text,omitzero"`
	GradeAfter      model.VerdictGrade `json:"grade_after,omitzero"`
	GradeAfterText  string             `json:"grade_after_text,omitzero"`
	// Inherited counts errors the later case ties to this DIMM although,
	// by the earlier case, another DIMM was in the slot when they were
	// recorded.
	Inherited ErrorCount `json:"inherited,omitzero"`
}

// CompareOptions select what a comparison shows.
type CompareOptions struct {
	// At is the time the change was made, when the reader knows it. The
	// period after then begins there.
	At time.Time
	// All lists every DIMM.
	All bool
}

// Errors of a comparison.
var (
	// ErrCaseOrder: the case named second is older than the first.
	ErrCaseOrder = errors.New("the second case is older than the first")
	// ErrChangeTime: the time given for the change is not after the
	// earlier case and up to the later one.
	ErrChangeTime = errors.New("the time of the change does not lie between the two cases")
)

// errorSums holds the errors of a period of one host.
type errorSums struct {
	byDIMM  map[string]ErrorCount
	bySlot  map[string]ErrorCount
	byPlace map[[2]string]ErrorCount // DIMM and slot
	loose   ErrorCount               // tied to no DIMM
	noSlot  ErrorCount               // tied to no slot
}

// firstDate is before every dated record. Records that only know seconds
// since boot are kept as times of 1970 and are left out by it.
var firstDate = time.Date(1971, 1, 1, 0, 0, 0, 0, time.UTC)

// errorsIn sums the dated errors of a host from a time (inclusive) to a
// time (exclusive). A zero end is open.
func (c *Case) errorsIn(ctx context.Context, host string, from, to time.Time) (*errorSums, error) {
	if from.Before(firstDate) {
		from = firstDate
	}
	sums := &errorSums{byDIMM: map[string]ErrorCount{}, bySlot: map[string]ErrorCount{}, byPlace: map[[2]string]ErrorCount{}}
	totals, err := c.store.Totals(ctx, store.EventFilter{Host: host, From: from, To: to})
	if err != nil {
		return nil, err
	}
	for _, t := range totals {
		n := ErrorCount{CE: t.CE, UE: t.UE}
		if t.Slot != "" {
			s := sums.bySlot[t.Slot]
			s.add(n)
			sums.bySlot[t.Slot] = s
		} else {
			sums.noSlot.add(n)
		}
		if t.DIMMUID == "" {
			sums.loose.add(n)
			continue
		}
		d := sums.byDIMM[t.DIMMUID]
		d.add(n)
		sums.byDIMM[t.DIMMUID] = d
		k := [2]string{t.DIMMUID, t.Slot}
		p := sums.byPlace[k]
		p.add(n)
		sums.byPlace[k] = p
	}
	return sums, nil
}

// datedBoots returns the boots of a host that began at a known time up to
// the given one, oldest first.
func (c *Case) datedBoots(ctx context.Context, host string, until time.Time) ([]model.Boot, error) {
	all, err := c.store.Boots(ctx, host)
	if err != nil {
		return nil, err
	}
	var out []model.Boot
	for _, b := range all {
		if b.StartQuality == model.TimeRelative || b.StartedAt.Before(firstDate) || b.StartedAt.After(until) {
			continue
		}
		out = append(out, b)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out, nil
}

// caseState describes a host as its case holds it.
func (c *Case) caseState(h *engine.HostResult, boots []model.Boot) CaseState {
	st := CaseState{AsOf: h.AsOf, Grade: h.Grade, GradeText: engine.GradeText(c.Lang, h.Grade)}
	for _, d := range h.DIMMs {
		if d.Present {
			st.DIMMs++
			st.CE += d.Features.CE
			st.UE += d.Features.UE
		}
	}
	if n := len(boots); n > 0 {
		b := boots[n-1]
		st.BootedAt, st.Kernel, st.BIOS, st.BMC = b.StartedAt, b.Kernel, b.BIOS, b.BMC
	}
	return st
}

// serialKnown reports whether the ledger knows the DIMM by its serial
// number.
func serialKnown(d *engine.DIMMResult) bool {
	return d.DIMM != nil && d.DIMM.Identity == model.IdentitySerial && d.DIMM.Serial != ""
}

// dimmLabel names a DIMM in a line: by the end of its serial number, or by
// its slot when no serial number is known.
func dimmLabel(info DIMMInfo, slot string) string {
	if info.Serial != "" {
		return "S/N " + ShortSerial(info.Serial)
	}
	return slot
}

// days writes a length of time as days with two decimals.
func days(d time.Duration) float64 {
	return math.Round(float64(d)/float64(24*time.Hour)*100) / 100
}

// SpanOf splits a length of time into a unit and a whole number of it:
// "days" from two days on, "hours" from two hours on, "minutes" below that.
func SpanOf(d time.Duration) (unit string, n int) {
	switch {
	case d >= 48*time.Hour:
		return "days", int(d / (24 * time.Hour))
	case d >= 2*time.Hour:
		return "hours", int(d / time.Hour)
	}
	return "minutes", int(d / time.Minute)
}

// spanText writes a length of time in the words of the language.
func (c *Case) spanText(d time.Duration) string {
	unit, n := SpanOf(d)
	return c.say("df."+unit, "n", n).Text
}

// minuteText writes a time to the minute.
func minuteText(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") }

// matchDIMMs pairs the DIMMs that are in the host in the earlier case with
// DIMMs of the later case. bySlot holds the pairs that rest on the slot.
func matchDIMMs(ha, hb *engine.HostResult) (pairs map[*engine.DIMMResult]*engine.DIMMResult, bySlot map[*engine.DIMMResult]bool) {
	pairs, bySlot = map[*engine.DIMMResult]*engine.DIMMResult{}, map[*engine.DIMMResult]bool{}
	used := map[*engine.DIMMResult]bool{}
	take := func(da, db *engine.DIMMResult) {
		pairs[da], used[db] = db, true
	}
	// The identity of the ledger: manufacturer, part number and serial
	// number. A DIMM that left the host can still be in the later ledger.
	byUID := map[string]*engine.DIMMResult{}
	for _, db := range hb.DIMMs {
		if serialKnown(db) && (byUID[db.UID] == nil || db.Present) {
			byUID[db.UID] = db
		}
	}
	for _, da := range ha.DIMMs {
		if !da.Present || !serialKnown(da) {
			continue
		}
		if db := byUID[da.UID]; db != nil && !used[db] {
			take(da, db)
		}
	}
	// The serial number alone, where the two cases know different things
	// about a module (one read the part number and the other did not).
	bySerial := map[string][]*engine.DIMMResult{}
	for _, db := range hb.DIMMs {
		if db.Present && !used[db] && serialKnown(db) {
			k := model.NormalizeLabel(db.DIMM.Serial)
			bySerial[k] = append(bySerial[k], db)
		}
	}
	for _, da := range ha.DIMMs {
		if !da.Present || pairs[da] != nil || !serialKnown(da) {
			continue
		}
		if found := bySerial[model.NormalizeLabel(da.DIMM.Serial)]; len(found) == 1 && !used[found[0]] {
			take(da, found[0])
		}
	}
	// The slot, where one of the cases has no serial number for what is
	// in it. Two different serial numbers in one slot are two DIMMs.
	inSlot := map[string]*engine.DIMMResult{}
	for _, db := range hb.DIMMs {
		if db.Present && !used[db] && db.Slot != "" {
			inSlot[db.Slot] = db
		}
	}
	for _, da := range ha.DIMMs {
		if !da.Present || pairs[da] != nil || da.Slot == "" {
			continue
		}
		if db := inSlot[da.Slot]; db != nil && !used[db] && (!serialKnown(da) || !serialKnown(db)) {
			take(da, db)
			bySlot[da] = true
		}
	}
	return pairs, bySlot
}

// Compare sets the case of a host (b, hb) beside an earlier case of the
// same host (a, ha). The sentences are in the language of the later case.
func Compare(ctx context.Context, a *Case, ha *engine.HostResult, b *Case, hb *engine.HostResult, opts CompareOptions) (*Comparison, error) {
	ta, tb := ha.AsOf, hb.AsOf
	if tb.Before(ta) {
		return nil, fmt.Errorf("%w: %s and %s", ErrCaseOrder, minuteText(ta), minuteText(tb))
	}
	cmp := &Comparison{Host: hb.Host, Grade: hb.Grade, GradeText: engine.GradeText(b.Lang, hb.Grade), Changes: []Change{}, DIMMs: []ComparedDIMM{}}
	if ha.Host != hb.Host {
		cmp.HostBefore = ha.Host
	}
	bootsA, err := a.datedBoots(ctx, ha.Host, ta)
	if err != nil {
		return nil, err
	}
	bootsB, err := b.datedBoots(ctx, hb.Host, tb)
	if err != nil {
		return nil, err
	}
	cmp.Before, cmp.After = a.caseState(ha, bootsA), b.caseState(hb, bootsB)
	cmp.ApartDays = days(tb.Sub(ta))

	// The period that counts as after.
	for _, bt := range bootsB {
		if bt.StartedAt.After(ta) {
			cmp.Restarts = append(cmp.Restarts, Restart{At: bt.StartedAt, Kernel: bt.Kernel, BIOS: bt.BIOS})
		}
	}
	from := ta.Add(time.Microsecond) // what the earlier case holds is before
	switch {
	case !opts.At.IsZero():
		if !opts.At.After(ta) || opts.At.After(tb) {
			return nil, fmt.Errorf("%w: %s is not after %s and up to %s", ErrChangeTime, minuteText(opts.At), minuteText(ta), minuteText(tb))
		}
		cmp.Since, cmp.SinceBy, from = opts.At, SinceGiven, opts.At
	case len(cmp.Restarts) > 0:
		cmp.Since, cmp.SinceBy = cmp.Restarts[len(cmp.Restarts)-1].At, SinceRestart
		from = cmp.Since
	default:
		cmp.Since, cmp.SinceBy = ta, SinceCase
	}
	after := tb.Sub(cmp.Since)
	before := after
	if known := ta.Sub(ha.ObservedFrom); !ha.ObservedFrom.IsZero() && known >= 0 && known < before {
		before = known
	}
	cmp.after, cmp.before = after, before
	cmp.AfterDays, cmp.BeforeDays = days(after), days(before)
	aspan, bspan := b.spanText(after), b.spanText(before)
	cmp.Period = b.say("df.since."+cmp.SinceBy, "since", minuteText(cmp.Since), "span", aspan)

	// The errors of the periods.
	recent := &errorSums{}
	if before > 0 {
		if recent, err = a.errorsIn(ctx, ha.Host, ta.Add(-before), time.Time{}); err != nil {
			return nil, err
		}
	}
	since, err := b.errorsIn(ctx, hb.Host, from, time.Time{})
	if err != nil {
		return nil, err
	}
	earlier, err := b.errorsIn(ctx, hb.Host, firstDate, ta.Add(time.Microsecond))
	if err != nil {
		return nil, err
	}
	between := &errorSums{}
	if cmp.Since.After(ta) {
		if between, err = b.errorsIn(ctx, hb.Host, ta.Add(time.Microsecond), from); err != nil {
			return nil, err
		}
		for slot, n := range between.bySlot {
			if n.Total() > 0 {
				cmp.Between = append(cmp.Between, SlotErrors{Slot: slot, ErrorCount: n})
			}
		}
		sort.Slice(cmp.Between, func(i, j int) bool { return cmp.Between[i].Slot < cmp.Between[j].Slot })
		// Until the first restart after it, the host is as the earlier
		// case has it. Between two restarts nobody looked.
		cmp.BetweenPlaced = len(cmp.Between) > 0 && (cmp.SinceBy == SinceGiven || len(cmp.Restarts) == 1)
		// Errors that name no slot stand last, under an empty name.
		if between.noSlot.Total() > 0 {
			cmp.Between = append(cmp.Between, SlotErrors{ErrorCount: between.noSlot})
		}
	}

	// One line per DIMM: those of the later case in its order, the worst
	// first, then the DIMMs that are gone.
	pairs, bySlot := matchDIMMs(ha, hb)
	earlierOf := map[*engine.DIMMResult]*engine.DIMMResult{}
	for da, db := range pairs {
		earlierOf[db] = da
	}
	inSlotBefore := map[string]*engine.DIMMResult{}
	for _, da := range ha.DIMMs {
		if da.Present && da.Slot != "" {
			inSlotBefore[da.Slot] = da
		}
	}
	grade := func(c *Case, d *engine.DIMMResult) (model.VerdictGrade, string) {
		if d.Verdict == nil {
			return "", ""
		}
		return d.Verdict.Grade, engine.GradeText(c.Lang, d.Verdict.Grade)
	}
	fillBefore := func(row *ComparedDIMM, da *engine.DIMMResult) {
		row.SlotBefore = da.Slot
		row.Before = ErrorCount{CE: da.Features.CE, UE: da.Features.UE}
		row.Recent = recent.byDIMM[da.UID]
		if cmp.BetweenPlaced && da.Slot != "" {
			row.Between = between.bySlot[da.Slot]
		}
		row.GradeBefore, row.GradeBeforeText = grade(a, da)
	}
	var rows []ComparedDIMM
	for _, db := range hb.DIMMs {
		if !db.Present {
			continue
		}
		row := ComparedDIMM{DIMM: dimmInfo(db.UID, db.DIMM), SlotAfter: db.Slot, After: since.byDIMM[db.UID]}
		row.GradeAfter, row.GradeAfterText = grade(b, db)
		da := earlierOf[db]
		switch {
		case da == nil:
			row.Change = ChangeAdded
		case da.Slot != db.Slot:
			row.Change = ChangeMoved
		}
		if da != nil {
			fillBefore(&row, da)
			row.BySlot = bySlot[da]
			if row.BySlot && !serialKnown(db) && serialKnown(da) {
				row.DIMM = dimmInfo(da.UID, da.DIMM)
			}
		}
		// Errors of the slot from the time of the earlier case, which has
		// another DIMM there.
		if other := inSlotBefore[db.Slot]; row.Change != "" && other != nil && other != da {
			row.Inherited = earlier.byPlace[[2]string{db.UID, db.Slot}]
		}
		row.Name = dimmLabel(row.DIMM, db.Slot)
		rows = append(rows, row)
	}
	for _, da := range ha.DIMMs {
		if !da.Present {
			continue
		}
		if db := pairs[da]; db != nil && db.Present {
			continue
		}
		row := ComparedDIMM{DIMM: dimmInfo(da.UID, da.DIMM), Change: ChangeRemoved}
		fillBefore(&row, da)
		row.Name = dimmLabel(row.DIMM, da.Slot)
		rows = append(rows, row)
	}

	// What differs: the DIMMs that moved, then those that left and those
	// that came.
	for _, kind := range []string{ChangeMoved, ChangeRemoved, ChangeAdded} {
		for i := range rows {
			if r := &rows[i]; r.Change == kind {
				cmp.Changes = append(cmp.Changes, Change{Kind: kind, DIMM: r.Name, From: r.SlotBefore, To: r.SlotAfter})
			}
		}
	}
	for _, f := range []struct{ kind, was, is string }{
		{ChangeBIOS, cmp.Before.BIOS, cmp.After.BIOS},
		{ChangeBMC, cmp.Before.BMC, cmp.After.BMC},
		{ChangeKernel, cmp.Before.Kernel, cmp.After.Kernel},
	} {
		if f.was != "" && f.is != "" && f.was != f.is {
			cmp.Changes = append(cmp.Changes, Change{Kind: f.kind, From: f.was, To: f.is})
		}
	}
	actions, err := b.store.Actions(ctx, store.ActionFilter{Host: hb.Host, From: ta.Add(time.Microsecond)})
	if err != nil {
		return nil, err
	}
	cmp.Changes = append(cmp.Changes, b.recorded(actions)...)

	for i := range rows {
		r := &rows[i]
		if opts.All || r.Change != "" || r.Before.Total()+r.Recent.Total()+r.Between.Total()+r.After.Total() > 0 ||
			(r.GradeBefore != "" && r.GradeBefore != model.VerdictOK) || (r.GradeAfter != "" && r.GradeAfter != model.VerdictOK) {
			cmp.DIMMs = append(cmp.DIMMs, *r)
		} else {
			cmp.NotListed++
		}
	}

	// What the counts show.
	need := int64(b.Result.Config.SystemMinErrors)
	nowIn := map[string]*ComparedDIMM{}
	for i := range rows {
		if rows[i].SlotAfter != "" {
			nowIn[rows[i].SlotAfter] = &rows[i]
		}
	}
	stopped := false
	for i := range rows {
		r := &rows[i]
		// Without a period after there is nothing to read.
		if r.Before.Total() < need || after <= 0 {
			continue
		}
		t := b.reading(r, nowIn[r.SlotBefore], since.bySlot[r.SlotBefore].Total(), need, aspan, bspan)
		cmp.Readings = append(cmp.Readings, t)
		stopped = stopped || strings.Contains(t.Code, "stopped")
	}

	// What a reader has to know to take the lines above as they are.
	note := func(code string, kv ...any) { cmp.Notes = append(cmp.Notes, b.say(code, kv...)) }
	known := func(h *engine.HostResult) (n int) {
		for _, d := range h.DIMMs {
			if d.Present && serialKnown(d) {
				n++
			}
		}
		return n
	}
	switch {
	case len(pairs) == len(bySlot) && known(ha) > 0 && known(hb) > 0:
		note("df.note.other_host")
	case cmp.HostBefore != "":
		note("df.note.names", "before", ha.Host, "after", hb.Host)
	}
	if after <= 0 {
		note("df.note.no_time")
	}
	if len(bootsB) == 0 {
		note("df.note.no_boots")
	}
	var slots []string
	for i := range rows {
		if rows[i].BySlot {
			slots = append(slots, rows[i].SlotAfter)
		}
	}
	if len(slots) > 0 {
		sort.Strings(slots)
		note("df.note.by_slot", "slots", strings.Join(slots, ", "))
	}
	if len(cmp.Between) > 0 {
		var n int64
		var parts []string
		for _, s := range cmp.Between {
			n += s.Total()
			name := s.Slot
			if name == "" {
				name = b.say("df.no_slot").Text
			}
			parts = append(parts, fmt.Sprintf("%s %d", name, s.Total()))
		}
		code := "df.note.between"
		if !cmp.BetweenPlaced {
			code += "_unplaced"
		}
		if n > 0 {
			note(code, "n", n, "since", minuteText(cmp.Since), "slots", strings.Join(parts, ", "))
		}
		// Errors in a slot the earlier case has empty: a DIMM was put
		// there before the time the reader gave for the change.
		if cmp.SinceBy == SinceGiven {
			for _, s := range cmp.Between {
				if s.Slot != "" && inSlotBefore[s.Slot] == nil {
					note("df.note.at_late", "slot", s.Slot, "n", s.Total(), "since", minuteText(cmp.Since))
				}
			}
		}
	}
	var undated int64
	for _, d := range hb.DIMMs {
		undated += d.Features.Undated
	}
	if undated > 0 {
		note("df.note.undated", "n", undated)
	}
	if n := since.loose.Total(); n > 0 {
		note("df.note.loose", "n", n, "since", minuteText(cmp.Since))
	}
	for i := range rows {
		r := &rows[i]
		if n := r.Inherited.Total(); n > 0 {
			other := inSlotBefore[r.SlotAfter]
			note("df.note.inherited", "n", n, "slot", r.SlotAfter, "dimm", r.Name,
				"other", dimmLabel(dimmInfo(other.UID, other.DIMM), other.Slot), "grade", r.GradeAfterText)
		}
	}
	if quiet := b.Result.Config.QuietDays; stopped && cmp.AfterDays < quiet {
		note("df.note.short", "span", aspan, "days", quiet)
	}
	if before < after {
		note("df.note.window", "bspan", bspan, "aspan", aspan)
	}
	return cmp, nil
}

// recorded lists the actions of a case as changes: one line per kind and
// slot, with the number of units and the time of the first and the last.
func (c *Case) recorded(actions []model.Action) []Change {
	type key struct {
		kind model.ActionKind
		slot string
	}
	var order []key
	sums := map[key]*Change{}
	for _, a := range actionEvents(actions) {
		k := key{a.Kind, a.Slot}
		ch := sums[k]
		if ch == nil {
			ch = &Change{Kind: ChangeAction, Action: a.Kind, ActionText: c.kindText(string(a.Kind)), Slot: a.Slot, First: a.At, Last: a.At}
			sums[k] = ch
			order = append(order, k)
		}
		ch.N += max(a.Count, 1)
		if a.At.Before(ch.First) {
			ch.First = a.At
		}
		if a.At.After(ch.Last) {
			ch.Last = a.At
		}
	}
	out := make([]Change, 0, len(order))
	for _, k := range order {
		out = append(out, *sums[k])
	}
	return out
}

// reading says what the counts of a DIMM show. now is the DIMM that is in
// the slot the DIMM had in the earlier case, or nil when that slot is
// empty; old counts the errors of that slot in the period after.
func (c *Case) reading(r *ComparedDIMM, now *ComparedDIMM, old, need int64, aspan, bspan string) Text {
	rec, aft := r.Recent.Total(), r.After.Total()
	args := []any{"dimm", r.Name, "rec", rec, "aft", aft, "old", old, "from", r.SlotBefore, "to", r.SlotAfter,
		"slot", r.SlotBefore, "aspan", aspan, "bspan", bspan}
	other := ""
	if now != nil {
		other = now.Name
	}
	args = append(args, "other", other)
	say := func(code string) Text { return c.say(code, args...) }
	many := func(n int64) bool { return n >= need }

	switch r.Change {
	case ChangeMoved:
		// The slot it left is empty: only the DIMM can show errors.
		empty := ""
		if now == nil {
			empty = "_empty"
		}
		switch {
		case many(aft) && many(old):
			return say("df.both")
		case many(aft):
			return say("df.followed" + empty)
		case many(old):
			return say("df.stayed")
		case !many(rec):
			return say("df.was_quiet")
		case aft+old == 0:
			return say("df.moved_stopped" + empty)
		}
		return say("df.moved_few" + empty)
	case ChangeRemoved:
		switch {
		case now == nil:
			return say("df.removed_empty")
		case many(old):
			return say("df.removed_stayed")
		case !many(rec):
			return say("df.removed_was_quiet")
		case old == 0:
			return say("df.removed_stopped")
		}
		return say("df.removed_few")
	}
	switch {
	case many(aft):
		return say("df.continues")
	case !many(rec):
		return say("df.was_quiet")
	case aft == 0:
		return say("df.stopped")
	}
	return say("df.few")
}
