package view

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/synth"
	"github.com/agenticode/my/internal/testledger"
	"github.com/agenticode/my/internal/testutil"
)

// openLedger opens a ledger file as a case of its one host.
func openLedger(t *testing.T, path string, lang i18n.Lang) (*Case, *engine.HostResult) {
	t.Helper()
	s, err := store.Open(ctx, path, store.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	c, err := Open(ctx, s, lang, engine.Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Host("")
	if err != nil {
		t.Fatal(err)
	}
	return c, h
}

// compare sets the two cases of a pair side by side.
func compare(t *testing.T, p testledger.Pair, lang i18n.Lang, opts CompareOptions) *Comparison {
	t.Helper()
	before, after := p.Paths(t)
	a, ha := openLedger(t, before, lang)
	b, hb := openLedger(t, after, lang)
	cmp, err := Compare(ctx, a, ha, b, hb, opts)
	if err != nil {
		t.Fatal(err)
	}
	return cmp
}

func codes(texts []Text) []string {
	out := make([]string, len(texts))
	for i, t := range texts {
		out[i] = t.Code
	}
	return out
}

func kinds(changes []Change) []string {
	out := make([]string, len(changes))
	for i, c := range changes {
		out[i] = c.Kind
		if c.Kind == ChangeAction {
			out[i] += ":" + string(c.Action)
		}
	}
	return out
}

// TestCompareReadsTheChange runs the comparison over the pairs: for each,
// what changed between the cases and what the counts of the DIMM with the
// fault show.
func TestCompareReadsTheChange(t *testing.T) {
	for _, tt := range []struct {
		pair    string
		changes []string
		// dimm is the slot the DIMM with the fault has in the earlier case.
		dimm    string
		change  string
		to      string
		reading string
		notes   []string
		grade   model.VerdictGrade
	}{
		// Two DIMMs swap slots at the restart.
		{"moved", []string{"moved", "moved"}, "CPU0_C1", ChangeMoved, "CPU0_D1", "df.followed", []string{"df.note.between"}, model.VerdictConsiderReplacement},
		{"moved-scan", []string{"moved", "moved"}, "CPU0_C1", ChangeMoved, "CPU0_D1", "df.followed",
			[]string{"df.note.between", "df.note.inherited", "df.note.window"}, model.VerdictConsiderReplacement},
		{"slot", []string{"moved", "moved"}, "CPU0_C1", ChangeMoved, "CPU0_D1", "df.stayed", []string{"df.note.between"}, model.VerdictHoldReplacement},
		{"slot-scan", []string{"moved", "moved"}, "CPU0_C1", ChangeMoved, "CPU0_D1", "df.stayed",
			[]string{"df.note.between", "df.note.inherited"}, model.VerdictConsiderReplacement},
		// The DIMM goes into a slot that was empty.
		{"to-empty", []string{"moved"}, "CPU0_C1", ChangeMoved, "CPU0_C2", "df.followed_empty", []string{"df.note.between"}, model.VerdictConsiderReplacement},
		// Nothing moves: a reseat, new firmware, a repair.
		{"reseat", []string{"action:reseat"}, "CPU0_C1", "", "CPU0_C1", "df.stopped", []string{"df.note.between"}, model.VerdictHoldReplacement},
		{"firmware", []string{"bios", "kernel"}, "CPU0_E1", "", "CPU0_E1", "df.continues", []string{"df.note.between"}, model.VerdictConsiderReplacement},
		{"repaired", []string{"action:hppr"}, "CPU0_C1", "", "CPU0_C1", "df.stopped", []string{"df.note.between"}, model.VerdictObserve},
		// The DIMM is replaced.
		{"replaced", []string{"removed", "added"}, "CPU1_A1", ChangeRemoved, "", "df.removed_stopped", []string{"df.note.between"}, model.VerdictObserve},
		{"replaced-slot", []string{"removed", "added"}, "CPU0_C1", ChangeRemoved, "", "df.removed_stayed", []string{"df.note.between"}, model.VerdictHoldReplacement},
	} {
		t.Run(tt.pair, func(t *testing.T) {
			cmp := compare(t, testledger.DiffPair(t, tt.pair), i18n.EN, CompareOptions{})
			if got := kinds(cmp.Changes); !slices.Equal(got, tt.changes) {
				t.Errorf("changes = %v, want %v", got, tt.changes)
			}
			if cmp.SinceBy != SinceRestart || !cmp.Since.Equal(testledger.PairChange()) || len(cmp.Restarts) != 1 {
				t.Errorf("the period after begins at %s (%s), %d restarts", cmp.Since, cmp.SinceBy, len(cmp.Restarts))
			}
			if cmp.Grade != tt.grade {
				t.Errorf("grade of the later case = %s, want %s", cmp.Grade, tt.grade)
			}
			var row *ComparedDIMM
			for i := range cmp.DIMMs {
				if cmp.DIMMs[i].SlotBefore == tt.dimm {
					row = &cmp.DIMMs[i]
				}
			}
			if row == nil {
				t.Fatalf("no line for the DIMM of %s", tt.dimm)
			}
			if row.Change != tt.change || row.SlotAfter != tt.to || row.BySlot {
				t.Errorf("the DIMM of %s: change %q to %q, by slot %v", tt.dimm, row.Change, row.SlotAfter, row.BySlot)
			}
			// The errors up to the restart are counted for the DIMM that
			// sat in the slot then, in a column of their own.
			if !cmp.BetweenPlaced || row.Between.Total() == 0 || row.Recent.Total() == 0 || row.Recent.Total() > row.Before.Total() {
				t.Errorf("counts of the DIMM: %+v (between placed: %v)", row, cmp.BetweenPlaced)
			}
			if got := codes(cmp.Readings); !slices.Equal(got, []string{tt.reading}) {
				t.Errorf("readings = %v, want %s", got, tt.reading)
			}
			if got := codes(cmp.Notes); !slices.Equal(got, tt.notes) {
				t.Errorf("notes = %v, want %v", got, tt.notes)
			}
			if !strings.HasPrefix(cmp.Readings[0].Text, row.Name+":") && !strings.HasPrefix(cmp.Readings[0].Text, row.Name+" in "+tt.dimm+":") {
				t.Errorf("the reading does not name the DIMM: %s", cmp.Readings[0].Text)
			}
		})
	}
}

// The numbers behind two readings, by hand.
func TestCompareCounts(t *testing.T) {
	// The errors move with the DIMM: the slot it left is quiet.
	cmp := compare(t, testledger.DiffPair(t, "moved"), i18n.EN, CompareOptions{})
	moved, other := cmp.DIMMs[0], cmp.DIMMs[len(cmp.DIMMs)-1]
	if moved.SlotBefore != "CPU0_C1" || moved.SlotAfter != "CPU0_D1" || other.SlotBefore != "CPU0_D1" || other.SlotAfter != "CPU0_C1" {
		t.Fatalf("lines: %+v, %+v", moved, other)
	}
	if moved.Before.CE != 114 || moved.Recent.CE != 114 || moved.Between.CE != 5 || moved.After.CE != 121 || other.After.Total() != 0 {
		t.Errorf("moved: %+v, the DIMM that came: %+v", moved, other)
	}
	want := "S/N 880018: the errors moved with the DIMM. 114 in CPU0_C1 in the 42 days before, 121 in CPU0_D1 in the 42 days after; CPU0_C1 has 0 with S/N E7A387 in it."
	if cmp.Readings[0].Text != want {
		t.Errorf("reading:\n%s\nwant:\n%s", cmp.Readings[0].Text, want)
	}
	if cmp.AfterDays != 42.88 || cmp.BeforeDays != 42.88 || cmp.ApartDays != 45.5 || cmp.NotListed != 9 {
		t.Errorf("days after %v, before %v, apart %v; %d not listed", cmp.AfterDays, cmp.BeforeDays, cmp.ApartDays, cmp.NotListed)
	}

	// The errors stay with the slot: the DIMM that came has them now.
	cmp = compare(t, testledger.DiffPair(t, "slot"), i18n.KO, CompareOptions{})
	want = "S/N 880018: 오류가 슬롯 CPU0_C1에 남음. 이전 42일 동안 이 DIMM으로 114건, 이후 42일 동안 S/N E7A387 장착 상태로 121건. 이 DIMM은 CPU0_D1에서 0건."
	if cmp.Readings[0].Text != want {
		t.Errorf("reading:\n%s\nwant:\n%s", cmp.Readings[0].Text, want)
	}

	// A scan knows where the DIMMs are now. The errors the slot had with
	// the DIMM that left are counted for the DIMM that came, and the
	// comparison says so.
	cmp = compare(t, testledger.DiffPair(t, "slot-scan"), i18n.EN, CompareOptions{})
	var came *ComparedDIMM
	for i := range cmp.DIMMs {
		if cmp.DIMMs[i].SlotAfter == "CPU0_C1" {
			came = &cmp.DIMMs[i]
		}
	}
	if came == nil || came.Inherited.CE != 114 || came.Before.Total() != 0 || came.After.CE != 121 {
		t.Fatalf("the DIMM that came: %+v", came)
	}
	note := cmp.Notes[len(cmp.Notes)-1].Text
	for _, part := range []string{"CPU0_C1: the later case counts 114 errors from before the earlier case was taken as errors of S/N E7A387", "The earlier case has S/N 880018 there."} {
		if !strings.Contains(note, part) {
			t.Errorf("note lacks %q: %s", part, note)
		}
	}
}

// --all lists every DIMM; without it the DIMMs that are quiet in both
// cases and did not move are counted.
func TestCompareAll(t *testing.T) {
	p := testledger.DiffPair(t, "firmware")
	short, long := compare(t, p, i18n.EN, CompareOptions{}), compare(t, p, i18n.EN, CompareOptions{All: true})
	if long.NotListed != 0 || len(long.DIMMs) != 16 || len(short.DIMMs)+short.NotListed != 16 || short.NotListed == 0 {
		t.Errorf("%d lines and %d not listed; with All %d lines and %d not listed", len(short.DIMMs), short.NotListed, len(long.DIMMs), long.NotListed)
	}
	if short.Before.BIOS != "Example BIOS Inc. 2.4.4 03/11/2026" || short.After.BIOS != "Example BIOS Inc. 2.6.1 09/18/2026" ||
		short.After.Kernel != "5.14.0-611.5.1.el9_7.x86_64" || !short.After.BootedAt.Equal(testledger.PairChange()) {
		t.Errorf("firmware: before %+v, after %+v", short.Before, short.After)
	}
}

// The time of the change, when the reader gives it, replaces the restart.
func TestCompareAt(t *testing.T) {
	p := testledger.DiffPair(t, "moved")
	at := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	cmp := compare(t, p, i18n.EN, CompareOptions{At: at})
	if cmp.SinceBy != SinceGiven || !cmp.Since.Equal(at) || cmp.AfterDays != 24 || !cmp.BetweenPlaced {
		t.Errorf("since %s by %s, %v days", cmp.Since, cmp.SinceBy, cmp.AfterDays)
	}
	if !strings.Contains(cmp.Period.Text, "the time given for the change: 24 days") {
		t.Errorf("period: %s", cmp.Period.Text)
	}

	before, after := p.Paths(t)
	a, ha := openLedger(t, before, i18n.EN)
	b, hb := openLedger(t, after, i18n.EN)
	for _, bad := range []time.Time{ha.AsOf, ha.AsOf.Add(-time.Hour), hb.AsOf.Add(time.Second)} {
		if _, err := Compare(ctx, a, ha, b, hb, CompareOptions{At: bad}); !errors.Is(err, ErrChangeTime) {
			t.Errorf("at %s: %v", bad, err)
		}
	}
	// The cases the wrong way round.
	if _, err := Compare(ctx, b, hb, a, ha, CompareOptions{}); !errors.Is(err, ErrCaseOrder) {
		t.Errorf("later case first: %v", err)
	}
}

// A case beside itself: nothing changed, and there is no time after.
func TestCompareSameCase(t *testing.T) {
	_, after := testledger.DiffPair(t, "moved").Paths(t)
	a, ha := openLedger(t, after, i18n.EN)
	cmp, err := Compare(ctx, a, ha, a, ha, CompareOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(cmp.Changes) != 0 || len(cmp.Restarts) != 0 || len(cmp.Readings) != 0 || cmp.SinceBy != SinceCase || cmp.AfterDays != 0 {
		t.Errorf("a case beside itself: %d changes, %d restarts, readings %v, since by %s", len(cmp.Changes), len(cmp.Restarts), codes(cmp.Readings), cmp.SinceBy)
	}
	if got := codes(cmp.Notes); !slices.Equal(got, []string{"df.note.no_time"}) {
		t.Errorf("notes = %v", got)
	}
	for _, d := range cmp.DIMMs {
		if d.Change != "" || d.SlotBefore != d.SlotAfter || d.GradeBefore != d.GradeAfter || d.After.Total() != 0 {
			t.Errorf("line differs: %+v", d)
		}
	}
}

// Two restarts after the earlier case: the period after begins at the
// last one, and the errors between the two are tied to no DIMM, because
// the cases do not say at which restart the DIMMs were moved.
func TestCompareSeveralRestarts(t *testing.T) {
	second := time.Date(2026, 10, 20, 6, 0, 0, 0, time.UTC)
	p := testledger.DiffPair(t, "moved-scan")
	p.Name = "moved-twice"
	p.Mut = func(sc *synth.Scenario) { sc.Reboots = append(sc.Reboots, synth.Reboot{At: second}) }
	cmp := compare(t, p, i18n.EN, CompareOptions{})
	// A scan knows the boot it runs in. The restart of the move is not in
	// what it read.
	if len(cmp.Restarts) != 1 || !cmp.Since.Equal(second) || cmp.SinceBy != SinceRestart {
		t.Fatalf("restarts %+v, since %s", cmp.Restarts, cmp.Since)
	}
	if got := codes(cmp.Readings); !slices.Equal(got, []string{"df.followed"}) {
		t.Errorf("readings = %v", got)
	}

	// A ledger that was kept has both restarts.
	p = testledger.DiffPair(t, "moved")
	p.Name = "moved-twice-kept"
	p.Mut = func(sc *synth.Scenario) { sc.Reboots = append(sc.Reboots, synth.Reboot{At: second}) }
	cmp = compare(t, p, i18n.EN, CompareOptions{})
	if len(cmp.Restarts) != 2 || !cmp.Since.Equal(second) || cmp.BetweenPlaced {
		t.Fatalf("restarts %+v, since %s, between placed %v", cmp.Restarts, cmp.Since, cmp.BetweenPlaced)
	}
	if got := codes(cmp.Notes); !slices.Equal(got, []string{"df.note.between_unplaced"}) {
		t.Errorf("notes = %v", got)
	}
	for _, d := range cmp.DIMMs {
		if d.Between.Total() != 0 {
			t.Errorf("errors between the restarts are counted for %s", d.Name)
		}
	}
	var between int64
	for _, s := range cmp.Between {
		between += s.Total()
	}
	if between < 40 || !strings.Contains(cmp.Notes[0].Text, "Several restarts lie between the cases") {
		t.Errorf("%d errors between: %s", between, cmp.Notes[0].Text)
	}
	if got := codes(cmp.Readings); !slices.Equal(got, []string{"df.followed"}) {
		t.Errorf("readings = %v", got)
	}
}

// Two cases of two hosts: no DIMM is in both.
func TestCompareOtherHost(t *testing.T) {
	before, _ := testledger.DiffPair(t, "moved").Paths(t)
	_, after := testledger.Pair{Name: "other", Scenario: "column", Seed: 2, Cut: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}.Paths(t)
	a, ha := openLedger(t, before, i18n.EN)
	b, hb := openLedger(t, after, i18n.EN)
	cmp, err := Compare(ctx, a, ha, b, hb, CompareOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cmp.Host != "synth-column" || cmp.HostBefore != "synth-moved-dimm" || !slices.Contains(codes(cmp.Notes), "df.note.other_host") {
		t.Errorf("hosts %q and %q, notes %v", cmp.HostBefore, cmp.Host, codes(cmp.Notes))
	}
	removed, added := 0, 0
	for _, c := range cmp.Changes {
		switch c.Kind {
		case ChangeRemoved:
			removed++
		case ChangeAdded:
			added++
		}
	}
	if removed != 16 || added != 16 {
		t.Errorf("%d removed, %d added", removed, added)
	}
}

// How the DIMMs of two cases are paired.
func TestMatchDIMMs(t *testing.T) {
	dimm := func(uid, serial, slot string, present bool) *engine.DIMMResult {
		d := &engine.DIMMResult{UID: uid, Slot: slot, Present: present, Verdict: &model.Verdict{Grade: model.VerdictOK}}
		if serial != "" {
			d.DIMM = &model.DIMM{UID: uid, Identity: model.IdentitySerial, Serial: serial}
		} else {
			d.DIMM = &model.DIMM{UID: uid, Identity: model.IdentitySlot}
		}
		return d
	}
	a := &engine.HostResult{DIMMs: []*engine.DIMMResult{
		dimm("u1", "AAA111", "A1", true),   // the same module, the same identity
		dimm("u2", "BBB222", "A2", true),   // known under another identity in the later case
		dimm("slot-a3", "", "A3", true),    // no serial number in the earlier case
		dimm("u4", "DDD444", "A4", true),   // another serial number in the slot later
		dimm("u5", "EEE555", "A5", true),   // left the host; the later ledger still has it
		dimm("u6", "FFF666", "A6", false),  // was gone before the earlier case
		dimm("u7", "GGG777", "A7", true),   // no serial number in the later case
		dimm("u8", "HHH888", "A8", true),   // moved to B8
		dimm("slot-a9", "", "A9", true),    // no serial number in either case
		dimm("u10", "JJJ000", "A10", true), // gone, nothing in its slot
		dimm("slot-a11", "", "A11", true),  // its slot holds a DIMM that is matched elsewhere
		dimm("u12", "KKK121", "A12", true), // moved to A11
	}}
	b := &engine.HostResult{DIMMs: []*engine.DIMMResult{
		dimm("u1", "AAA111", "A1", true),
		dimm("other-2", "bbb222", "A2", true),
		dimm("u3", "CCC333", "A3", true),
		dimm("u4x", "XXX444", "A4", true),
		dimm("u5", "EEE555", "A5", false),
		dimm("u5new", "NEW555", "A5", true),
		dimm("u6", "FFF666", "A6", true),
		dimm("slot-a7", "", "A7", true),
		dimm("u8", "HHH888", "B8", true),
		dimm("other-a9", "", "A9", true),
		dimm("u12", "KKK121", "A11", true),
	}}
	pairs, bySlot := matchDIMMs(a, b)
	got := map[string]string{}
	for da, db := range pairs {
		got[da.UID] = db.UID
		if bySlot[da] {
			got[da.UID] += " by slot"
		}
	}
	want := map[string]string{
		"u1": "u1", "u2": "other-2", "slot-a3": "u3 by slot", "u5": "u5", "u7": "slot-a7 by slot", "u8": "u8",
		"slot-a9": "other-a9 by slot", "u12": "u12",
	}
	if len(got) != len(want) {
		t.Errorf("pairs = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s is paired with %q, want %q", k, got[k], v)
		}
	}
}

// The data of the comparison as --json prints it.
func TestCompareGolden(t *testing.T) {
	for _, name := range []string{"moved-scan", "replaced"} {
		cmp := compare(t, testledger.DiffPair(t, name), i18n.EN, CompareOptions{})
		cmp.Before.File, cmp.After.File = "before.mycase", "after.mycase"
		b, err := json.MarshalIndent(cmp, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		testutil.Golden(t, "diff-"+name+".json", append(b, '\n'))
	}
}
