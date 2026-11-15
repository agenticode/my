package store

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
)

const day = 24 * time.Hour

func bootIDs(t *testing.T, s *Store, host string) []string {
	t.Helper()
	boots, err := s.Boots(ctx, host)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, b := range boots {
		ids = append(ids, b.BootID)
	}
	return ids
}

func upsertBoot(t *testing.T, s *Store, b *model.Boot) string {
	t.Helper()
	var id string
	mustUpdate(t, s, func(w *Writer) (err error) {
		id, err = w.UpsertBoot(b)
		return err
	})
	return id
}

func TestUpsertBootFoldsDerivedIntoKernelID(t *testing.T) {
	s := newStore(t)
	// First the boot is known only from a kernel log without a boot ID.
	derived := upsertBoot(t, s, &model.Boot{Host: "h1", StartedAt: at(2 * time.Second), StartQuality: model.TimeEstimated, Source: "klog"})
	if !strings.HasPrefix(derived, "derived-") {
		t.Fatalf("derived boot id = %q", derived)
	}
	e := ev("klog/edac", "L1", at(time.Hour), 0x1000)
	e.BootID = derived
	act := &model.Action{Host: "h1", At: at(time.Hour), Kind: model.ActionPageOffline, BootID: derived, Source: "klog/offline", Fingerprint: "a"}
	mustUpdate(t, s, func(w *Writer) error {
		if _, err := w.InsertEvent(e); err != nil {
			return err
		}
		_, err := w.InsertAction(act)
		return err
	})

	// Then the journal reports the same boot with its kernel boot ID.
	const kid = "0123456789abcdef0123456789abcdef"
	got := upsertBoot(t, s, &model.Boot{Host: "h1", BootID: kid, StartedAt: at(0), StartQuality: model.TimeExact,
		Kernel: "5.14.0-570.12.1.el9_6.x86_64", Source: "journal"})
	if got != kid {
		t.Fatalf("boot id = %q, want the kernel id", got)
	}
	boots, _ := s.Boots(ctx, "h1")
	if len(boots) != 1 || boots[0].BootID != kid || boots[0].Derived {
		t.Fatalf("boots = %+v, want one row under the kernel id", boots)
	}
	if !boots[0].StartedAt.Equal(at(0)) || boots[0].StartQuality != model.TimeExact || boots[0].Kernel == "" {
		t.Errorf("the exact start and the kernel release should win: %+v", boots[0])
	}
	ge, _ := s.Event(ctx, e.ID)
	acts, _ := s.Actions(ctx, ActionFilter{})
	if ge.BootID != kid || acts[0].BootID != kid {
		t.Errorf("records still point at the derived id: event %q, action %q", ge.BootID, acts[0].BootID)
	}

	// A later derived sighting of the same boot folds into the kernel id row.
	again := upsertBoot(t, s, &model.Boot{Host: "h1", StartedAt: at(5 * time.Second), StartQuality: model.TimeEstimated,
		BIOS: "2.4.4", LastSeen: at(3 * time.Hour)})
	if again != kid {
		t.Errorf("derived sighting got id %q, want %q", again, kid)
	}
	boots, _ = s.Boots(ctx, "h1")
	if len(boots) != 1 || boots[0].BIOS != "2.4.4" || !boots[0].LastSeen.Equal(at(3*time.Hour)) || !boots[0].StartedAt.Equal(at(0)) {
		t.Errorf("merge result = %+v", boots)
	}
}

func TestUpsertBootKeepsDistinctBoots(t *testing.T) {
	s := newStore(t)
	a := upsertBoot(t, s, &model.Boot{Host: "h1", BootID: "aaaa", StartedAt: at(0)})
	// Two kernel boot IDs are two boots, however close in time.
	b := upsertBoot(t, s, &model.Boot{Host: "h1", BootID: "bbbb", StartedAt: at(20 * time.Second)})
	// A derived boot outside the merge window is another boot.
	c := upsertBoot(t, s, &model.Boot{Host: "h1", StartedAt: at(10 * time.Minute)})
	// The same start time on another host is another boot.
	d := upsertBoot(t, s, &model.Boot{Host: "h2", StartedAt: at(0)})
	if a != "aaaa" || b != "bbbb" || c == a || c == b || d == a {
		t.Fatalf("ids = %q %q %q %q", a, b, c, d)
	}
	if got := bootIDs(t, s, "h1"); len(got) != 3 {
		t.Errorf("h1 boots = %v, want 3", got)
	}
	if got := bootIDs(t, s, "h2"); len(got) != 1 {
		t.Errorf("h2 boots = %v, want 1", got)
	}
	// Upserting a known id again does not add a row.
	upsertBoot(t, s, &model.Boot{Host: "h1", BootID: "aaaa", StartedAt: at(0), Kernel: "6.12.0"})
	if got := bootIDs(t, s, "h1"); len(got) != 3 {
		t.Errorf("after repeat: h1 boots = %v, want 3", got)
	}
}

func TestUpsertBootRelativeCapturesStayApart(t *testing.T) {
	s := newStore(t)
	epoch := time.Unix(0, 0).UTC()
	// Two kernel logs without any wall-clock anchor both start at the epoch.
	a := upsertBoot(t, s, &model.Boot{Host: "h1", BootID: "derived-capture-a", Derived: true, StartedAt: epoch, StartQuality: model.TimeRelative})
	b := upsertBoot(t, s, &model.Boot{Host: "h1", BootID: "derived-capture-b", Derived: true, StartedAt: epoch, StartQuality: model.TimeRelative})
	if a == b {
		t.Fatalf("relative captures were merged into %q", a)
	}
	if got := bootIDs(t, s, "h1"); len(got) != 2 {
		t.Errorf("boots = %v, want 2", got)
	}
}

func TestUpsertBootValidation(t *testing.T) {
	s := newStore(t)
	for name, b := range map[string]*model.Boot{
		"no host": {StartedAt: at(0)},
		"no time": {Host: "h1"},
	} {
		if err := s.Update(ctx, func(w *Writer) error { _, err := w.UpsertBoot(b); return err }); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestObservePlacement(t *testing.T) {
	s := newStore(t)
	mustUpdate(t, s, func(w *Writer) error {
		for _, uid := range []string{"dX", "dY"} {
			if err := w.UpsertDIMM(&model.DIMM{UID: uid, Identity: model.IdentitySerial}); err != nil {
				return err
			}
		}
		return nil
	})
	see := func(uid string, d time.Duration) {
		t.Helper()
		mustUpdate(t, s, func(w *Writer) error { return w.ObservePlacement(uid, "h1", "CPU0_C1", at(d), "smbios") })
	}
	type span struct {
		uid         string
		first, last time.Duration
	}
	check := func(step string, want ...span) {
		t.Helper()
		pl, err := s.Placements(ctx, "h1", "")
		if err != nil {
			t.Fatal(err)
		}
		var got []span
		for _, p := range pl {
			got = append(got, span{p.DIMMUID, p.FirstSeen.Sub(t0), p.LastSeen.Sub(t0)})
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: placements = %+v, want %+v", step, got, want)
		}
	}

	see("dX", 10*day)
	check("first sighting", span{"dX", 10 * day, 10 * day})
	see("dX", 20*day)
	check("same DIMM later extends", span{"dX", 10 * day, 20 * day})
	see("dY", 30*day)
	check("another DIMM starts a placement", span{"dX", 10 * day, 20 * day}, span{"dY", 30 * day, 30 * day})
	see("dX", 5*day)
	check("an earlier sighting moves the start back", span{"dX", 5 * day, 20 * day}, span{"dY", 30 * day, 30 * day})
	see("dX", 25*day)
	check("a sighting in the gap extends the earlier placement", span{"dX", 5 * day, 25 * day}, span{"dY", 30 * day, 30 * day})
	see("dY", 28*day)
	check("a sighting just before the next placement moves its start", span{"dX", 5 * day, 25 * day}, span{"dY", 28 * day, 30 * day})
	see("dX", 40*day)
	check("the first DIMM coming back is a third placement",
		span{"dX", 5 * day, 25 * day}, span{"dY", 28 * day, 30 * day}, span{"dX", 40 * day, 40 * day})
	see("dX", 15*day)
	check("a sighting inside a placement changes nothing",
		span{"dX", 5 * day, 25 * day}, span{"dY", 28 * day, 30 * day}, span{"dX", 40 * day, 40 * day})

	byDIMM, _ := s.Placements(ctx, "", "dY")
	if len(byDIMM) != 1 || byDIMM[0].Slot != "CPU0_C1" {
		t.Errorf("placements of dY = %+v", byDIMM)
	}
}

func TestLinkBoots(t *testing.T) {
	s := newStore(t)
	upsertBoot(t, s, &model.Boot{Host: "h1", BootID: "b1", StartedAt: at(0)})
	upsertBoot(t, s, &model.Boot{Host: "h1", BootID: "b2", StartedAt: at(2 * day)})
	upsertBoot(t, s, &model.Boot{Host: "h2", BootID: "other-host", StartedAt: at(-10 * day)})
	upsertBoot(t, s, &model.Boot{Host: "h1", BootID: "derived-rel", Derived: true, StartedAt: time.Unix(0, 0), StartQuality: model.TimeRelative})

	before := ev("rasdaemon/mc_event", "before", at(-time.Hour), 0x1000)
	in1 := ev("rasdaemon/mc_event", "in1", at(time.Hour), 0x1000)
	edge := ev("rasdaemon/mc_event", "edge", at(2*day), 0x1000)
	in2 := ev("rasdaemon/mc_event", "in2", at(3*day), 0x1000)
	stale := ev("rasdaemon/mc_event", "stale", at(3*day), 0x2000)
	stale.BootID = "b1" // a wrong boot from an earlier state of the ledger
	rel := ev("klog/edac", "rel", time.Unix(1234, 0), 0x1000)
	rel.TimeQuality, rel.BootID = model.TimeRelative, "derived-rel"
	insert(t, s, before, in1, edge, in2, stale, rel)
	act := &model.Action{Host: "h1", At: at(2*day + time.Hour), Kind: model.ActionPageOffline, Source: "klog/offline", Fingerprint: "a"}
	mustUpdate(t, s, func(w *Writer) error { _, err := w.InsertAction(act); return err })

	if err := s.LinkBoots(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	want := map[int64]string{before.ID: "", in1.ID: "b1", edge.ID: "b2", in2.ID: "b2", stale.ID: "b2", rel.ID: "derived-rel"}
	for id, boot := range want {
		e, _ := s.Event(ctx, id)
		if e.BootID != boot {
			t.Errorf("event %s: boot = %q, want %q", e.RawRef, e.BootID, boot)
		}
	}
	acts, _ := s.Actions(ctx, ActionFilter{Host: "h1"})
	if acts[0].BootID != "b2" {
		t.Errorf("action boot = %q, want b2", acts[0].BootID)
	}
}

func TestEventLocationsAndSetEventSlot(t *testing.T) {
	s := newStore(t)
	a := ev("rasdaemon/mc_event", "1", at(0), 0x1000)
	a.Slot, a.Label, a.EdacMC, a.EdacLocation = "", "CPU_SrcID#0_MC#1_Chan#0_DIMM#0", model.Some(1), "channel:0 slot:0"
	b := ev("rasdaemon/mc_event", "2", at(time.Hour), 0x2000)
	b.Slot, b.Label, b.EdacMC, b.EdacLocation = "", a.Label, a.EdacMC, a.EdacLocation
	c := ev("klog/ghes", "3", at(2*time.Hour), 0x3000)
	c.Slot, c.Label, c.SMBIOSHandle = "", "unknown memory", model.Some(0x1100)
	d := ev("rasdaemon/mce_record", "4", at(3*time.Hour), 0x4000)
	d.Slot, d.Label = "", ""
	insert(t, s, a, b, c, d)

	keys, err := s.EventLocations(ctx, "h1")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 3 {
		t.Fatalf("location keys = %+v, want 3 distinct", keys)
	}
	var edac *LocationKey
	for i := range keys {
		if keys[i].EdacMC.Valid {
			edac = &keys[i]
		}
	}
	if edac == nil || edac.Records != 2 {
		t.Fatalf("EDAC key = %+v, want one key used by 2 events", edac)
	}
	mustUpdate(t, s, func(w *Writer) error { return w.SetEventSlot("h1", *edac, "CPU0_C1") })
	for id, want := range map[int64]string{a.ID: "CPU0_C1", b.ID: "CPU0_C1", c.ID: "", d.ID: ""} {
		if e, _ := s.Event(ctx, id); e.Slot != want {
			t.Errorf("event %s: slot = %q, want %q", e.RawRef, e.Slot, want)
		}
	}
}

// linkFixture is a slot that held DIMM X, then DIMM Y, with events around the swap.
type linkFixture struct {
	s      *Store
	events map[string]*model.Event
}

func newLinkFixture(t *testing.T, bootDays ...int) *linkFixture {
	t.Helper()
	s := newStore(t)
	mustUpdate(t, s, func(w *Writer) error {
		for _, uid := range []string{"dX", "dY"} {
			if err := w.UpsertDIMM(&model.DIMM{UID: uid, Identity: model.IdentitySerial}); err != nil {
				return err
			}
		}
		for _, p := range []struct {
			uid  string
			days []int
		}{{"dX", []int{1, 3}}, {"dY", []int{10, 12}}} {
			for _, d := range p.days {
				if err := w.ObservePlacement(p.uid, "h1", "CPU0_C1", at(time.Duration(d)*day), "smbios"); err != nil {
					return err
				}
			}
		}
		for _, d := range bootDays {
			if _, err := w.UpsertBoot(&model.Boot{Host: "h1", BootID: fmt.Sprintf("boot-day-%d", d), StartedAt: at(time.Duration(d) * day)}); err != nil {
				return err
			}
		}
		return nil
	})
	f := &linkFixture{s: s, events: map[string]*model.Event{}}
	for _, d := range []int{0, 2, 4, 6, 8, 11, 20} {
		name := fmt.Sprintf("day%d", d)
		e := ev("rasdaemon/mc_event", name, at(time.Duration(d)*day), uint64(0x1000*(d+1)))
		f.events[name] = e
		insert(t, s, e)
	}
	if err := s.LinkDIMMs(ctx, "h1"); err != nil {
		t.Fatalf("LinkDIMMs: %v", err)
	}
	return f
}

func (f *linkFixture) check(t *testing.T, want map[string][2]string) {
	t.Helper()
	for name, w := range want {
		e, err := f.s.Event(ctx, f.events[name].ID)
		if err != nil {
			t.Fatal(err)
		}
		if e.DIMMUID != w[0] || e.DIMMLink != w[1] {
			t.Errorf("%s: dimm = %q (%s), want %q (%s)", name, e.DIMMUID, e.DIMMLink, w[0], w[1])
		}
	}
}

func TestLinkDIMMsAcrossASwap(t *testing.T) {
	t.Run("one boot between the placements", func(t *testing.T) {
		f := newLinkFixture(t, 5)
		f.check(t, map[string][2]string{
			"day0":  {"dX", model.LinkAssumed},  // before the DIMM was first seen
			"day2":  {"dX", model.LinkObserved}, // inside the first placement
			"day4":  {"dX", model.LinkAssumed},  // after the last sighting, before the boot
			"day6":  {"dY", model.LinkAssumed},  // after the boot, before the new DIMM was seen
			"day8":  {"dY", model.LinkAssumed},
			"day11": {"dY", model.LinkObserved},
			"day20": {"dY", model.LinkAssumed},
		})
	})
	t.Run("two boots between the placements", func(t *testing.T) {
		f := newLinkFixture(t, 5, 7)
		f.check(t, map[string][2]string{
			"day4": {"dX", model.LinkAssumed},
			"day6": {"", model.LinkAmbiguous}, // the swap was at one of the two boots
			"day8": {"dY", model.LinkAssumed},
		})
	})
	t.Run("no boot known between the placements", func(t *testing.T) {
		f := newLinkFixture(t)
		f.check(t, map[string][2]string{
			"day2":  {"dX", model.LinkObserved},
			"day4":  {"", model.LinkAmbiguous},
			"day6":  {"", model.LinkAmbiguous},
			"day8":  {"", model.LinkAmbiguous},
			"day11": {"dY", model.LinkObserved},
		})
	})
	t.Run("running the pass again changes nothing", func(t *testing.T) {
		f := newLinkFixture(t, 5)
		before, _ := f.s.EventList(ctx, EventFilter{})
		if err := f.s.LinkDIMMs(ctx, "h1"); err != nil {
			t.Fatal(err)
		}
		after, _ := f.s.EventList(ctx, EventFilter{})
		if !reflect.DeepEqual(before, after) {
			t.Error("second LinkDIMMs run changed events")
		}
	})
}

func TestLinkDIMMsWithoutIdentity(t *testing.T) {
	s := newStore(t)
	inSlot := ev("rasdaemon/mc_event", "1", at(0), 0x1000)
	inSlot.Slot = "mc#0csrow#2channel#1"
	noSlot := ev("rasdaemon/mce_record", "2", at(time.Hour), 0x2000)
	noSlot.Slot, noSlot.Label = "", ""
	insert(t, s, inSlot, noSlot)
	if err := s.LinkDIMMs(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	e, _ := s.Event(ctx, inSlot.ID)
	wantUID := model.SlotUID("h1", "mc#0csrow#2channel#1")
	if e.DIMMUID != wantUID || e.DIMMLink != model.LinkSlot {
		t.Errorf("slot without identity: dimm = %q (%s), want %q (slot)", e.DIMMUID, e.DIMMLink, wantUID)
	}
	d, _ := s.DIMM(ctx, wantUID)
	if d == nil || d.Identity != model.IdentitySlot {
		t.Errorf("placeholder DIMM = %+v", d)
	}
	e, _ = s.Event(ctx, noSlot.ID)
	if e.DIMMUID != "" || e.DIMMLink != "" {
		t.Errorf("event without a slot got a DIMM: %q (%s)", e.DIMMUID, e.DIMMLink)
	}
}

func TestLinkDIMMsGivesPageOfflineActionsTheSlotOfThePage(t *testing.T) {
	s := newStore(t)
	mustUpdate(t, s, func(w *Writer) error {
		if err := w.UpsertDIMM(&model.DIMM{UID: "dX", Identity: model.IdentitySerial}); err != nil {
			return err
		}
		return w.ObservePlacement("dX", "h1", "CPU0_C1", at(0), "smbios")
	})
	insert(t, s, ev("rasdaemon/mc_event", "1", at(0), 0x1f8bd19c0))
	onPage := &model.Action{Host: "h1", At: at(time.Hour), Kind: model.ActionPageOffline, Method: "soft",
		OsPhysicalAddress: model.Some(uint64(0x1f8bd1000)), Source: "klog/offline", Fingerprint: "a1"}
	elsewhere := &model.Action{Host: "h1", At: at(time.Hour), Kind: model.ActionPageOffline, Method: "soft",
		OsPhysicalAddress: model.Some(uint64(0x7777000)), Source: "klog/offline", Fingerprint: "a2"}
	mustUpdate(t, s, func(w *Writer) error {
		for _, a := range []*model.Action{onPage, elsewhere} {
			if _, err := w.InsertAction(a); err != nil {
				return err
			}
		}
		return nil
	})
	if err := s.LinkDIMMs(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	acts, _ := s.Actions(ctx, ActionFilter{Host: "h1"})
	sort.Slice(acts, func(i, j int) bool { return acts[i].Fingerprint < acts[j].Fingerprint })
	if acts[0].Slot != "CPU0_C1" || acts[0].DIMMUID != "dX" {
		t.Errorf("action on a page with errors: slot %q dimm %q, want CPU0_C1 dX", acts[0].Slot, acts[0].DIMMUID)
	}
	if acts[1].Slot != "" || acts[1].DIMMUID != "" {
		t.Errorf("action on a page without errors got slot %q dimm %q", acts[1].Slot, acts[1].DIMMUID)
	}
	byDIMM, _ := s.Actions(ctx, ActionFilter{DIMMUID: "dX"})
	if len(byDIMM) != 1 {
		t.Errorf("actions of dX = %d, want 1", len(byDIMM))
	}
}

func dupOf(t *testing.T, s *Store, e *model.Event) int64 {
	t.Helper()
	got, err := s.Event(ctx, e.ID)
	if err != nil || got == nil {
		t.Fatalf("event %d: %v", e.ID, err)
	}
	return got.DupOf
}

func TestMarkDuplicatesAcrossSources(t *testing.T) {
	s := newStore(t)
	const addr = 0x1f8bd19c0
	graded := func(e *model.Event, g model.Grade) *model.Event { e.Grade = g; return e }

	// The same error in the rasdaemon database and in the kernel log.
	ras := graded(ev("rasdaemon/mc_event", "mc/1", at(0), addr), model.GradeA)
	klog := graded(ev("klog/edac", "L1", at(time.Second), addr), model.GradeA)
	// A machine check record with less detail than the kernel log line of the same error.
	mce := graded(ev("rasdaemon/mce_record", "mce/1", at(time.Hour), 0x2000), model.GradeB)
	klogA := graded(ev("klog/edac", "L2", at(time.Hour+2*time.Second), 0x2010), model.GradeA) // same cache line
	// Two real errors at the same address in the same second, each logged twice.
	r2a := graded(ev("rasdaemon/mc_event", "mc/2", at(2*time.Hour), 0x3000), model.GradeA)
	r2b := graded(ev("rasdaemon/mc_event", "mc/3", at(2*time.Hour), 0x3000), model.GradeA)
	k2a := graded(ev("klog/edac", "L3", at(2*time.Hour), 0x3000), model.GradeA)
	k2b := graded(ev("klog/edac", "L4", at(2*time.Hour), 0x3000), model.GradeA)
	// Same address, too far apart in time.
	r3 := graded(ev("rasdaemon/mc_event", "mc/4", at(3*time.Hour), 0x4000), model.GradeA)
	k3 := graded(ev("klog/edac", "L5", at(3*time.Hour+10*time.Second), 0x4000), model.GradeA)
	// A kernel log time estimated from seconds since boot may be further off.
	r4 := graded(ev("rasdaemon/mc_event", "mc/5", at(4*time.Hour), 0x5000), model.GradeA)
	k4 := graded(ev("klog/edac", "L6", at(4*time.Hour+time.Minute), 0x5000), model.GradeA)
	k4.TimeQuality = model.TimeEstimated
	// A corrected and an uncorrected error at one address are two errors.
	r5 := graded(ev("rasdaemon/mc_event", "mc/6", at(5*time.Hour), 0x6000), model.GradeA)
	k5 := graded(ev("klog/edac", "L7", at(5*time.Hour), 0x6000), model.GradeA)
	k5.ErrorSeverity = model.SevUE
	// Without addresses the slot is the key.
	r6 := ev("rasdaemon/mc_event", "mc/7", at(6*time.Hour), 0)
	r6.OsPhysicalAddress = model.None[uint64]()
	k6 := ev("klog/edac", "L8", at(6*time.Hour+time.Second), 0)
	k6.OsPhysicalAddress = model.None[uint64]()
	// Another host is another machine.
	other := graded(ev("klog/edac", "L9", at(0), addr), model.GradeA)
	other.Host = "h2"

	insert(t, s, ras, klog, mce, klogA, r2a, r2b, k2a, k2b, r3, k3, r4, k4, r5, k5, r6, k6, other)
	n, err := s.MarkDuplicates(ctx, "h1", DuplicateTolerance{})
	if err != nil {
		t.Fatal(err)
	}
	if n != 6 {
		t.Errorf("MarkDuplicates marked %d records, want 6", n)
	}

	tests := []struct {
		name string
		e    *model.Event
		want int64
	}{
		{"database record stays", ras, 0},
		{"kernel log copy points at the database record", klog, ras.ID},
		{"record with less detail points at the better one", mce, klogA.ID},
		{"better record stays", klogA, 0},
		{"first of two real errors stays", r2a, 0},
		{"second of two real errors stays", r2b, 0},
		{"first kernel log copy", k2a, r2a.ID},
		{"second kernel log copy", k2b, r2b.ID},
		{"beyond tolerance: database record", r3, 0},
		{"beyond tolerance: kernel log record", k3, 0},
		{"estimated time within the wider tolerance", k4, r4.ID},
		{"different severity class: corrected", r5, 0},
		{"different severity class: uncorrected", k5, 0},
		{"no address: database record", r6, 0},
		{"no address: kernel log copy matched by slot", k6, r6.ID},
		{"other host untouched", other, 0},
	}
	for _, tt := range tests {
		if got := dupOf(t, s, tt.e); got != tt.want {
			t.Errorf("%s: dup_of = %d, want %d", tt.name, got, tt.want)
		}
	}

	// Each error counts once: 11 records on h1 remain after 6 duplicates.
	totals, _ := s.Totals(ctx, EventFilter{Host: "h1"})
	var ce, ue int64
	for _, tt := range totals {
		ce, ue = ce+tt.CE, ue+tt.UE
	}
	if ce != 9 || ue != 1 {
		t.Errorf("totals after dedupe: CE %d UE %d, want 9 and 1", ce, ue)
	}

	// The pass recomputes from the ledger: a second run gives the same marks.
	before, _ := s.EventList(ctx, EventFilter{WithDuplicates: true})
	if n2, err := s.MarkDuplicates(ctx, "h1", DuplicateTolerance{}); err != nil || n2 != n {
		t.Fatalf("second run: %d, %v", n2, err)
	}
	after, _ := s.EventList(ctx, EventFilter{WithDuplicates: true})
	if !reflect.DeepEqual(before, after) {
		t.Error("second MarkDuplicates run changed the ledger")
	}
}

// Records of a CXL memory device as rasdaemon 1.0.0 stored them in the lab
// (testdata/real/7.3.0-cxl): corrected errors with a DRAM location in one
// table and, seconds later, corrected errors without one in another. The
// device has no address in the host's address space, so the slot is all
// they have in common. A device reports an error in one of the two forms:
// these are eleven errors, not eight.
func TestMarkDuplicatesKeepsTheCXLTablesApart(t *testing.T) {
	s := newStore(t)
	cxl := func(table, ref string, when time.Time, sev model.Severity, grade model.Grade) *model.Event {
		return &model.Event{Host: "h1", ErrorDT: when, ErrorSeverity: sev, Source: "rasdaemon/" + table, RawRef: ref,
			Label: "cxl:mem0", Slot: "cxl:mem0", Grade: grade, Fingerprint: model.Fingerprint(table, ref)}
	}
	var all, media []*model.Event
	for i := 0; i < 8; i++ {
		all = append(all, cxl("cxl_dram_event", "dram/"+string(rune('1'+i)), at(time.Duration(i)*time.Second), model.SevCE, model.GradeA))
	}
	all = append(all, cxl("cxl_dram_event", "dram/9", at(9*time.Second), model.SevUE, model.GradeA))
	for i := 0; i < 3; i++ {
		e := cxl("cxl_general_media_event", "media/"+string(rune('1'+i)), at(time.Duration(10+i)*time.Second), model.SevCE, model.GradeB)
		media = append(media, e)
		all = append(all, e)
	}
	// The kernel log of another host form: the same device named by a
	// line of the kernel is a second report of the error next to it.
	line := cxl("x", "L1", at(11*time.Second), model.SevCE, model.GradeC)
	line.Source = "klog/edac"
	all = append(all, line)
	insert(t, s, all...)
	n, err := s.MarkDuplicates(ctx, "h1", DuplicateTolerance{})
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range media {
		if got := dupOf(t, s, e); got != 0 {
			t.Errorf("general media record %d is taken for a second report of record %d", i+1, got)
		}
	}
	if got := dupOf(t, s, line); got == 0 || n != 1 {
		t.Errorf("the kernel log line stands alone (dup_of %d, %d duplicates): another source may still report the same error", got, n)
	}
	totals, err := s.Totals(ctx, EventFilter{Host: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	var ce, ue int64
	for _, x := range totals {
		ce += x.CE
		ue += x.UE
	}
	if ce != 11 || ue != 1 {
		t.Errorf("CE %d, UE %d; the device reported 11 and 1", ce, ue)
	}
	for src, want := range map[string]string{"rasdaemon/cxl_dram_event": "rasdaemon/cxl", "rasdaemon/cxl_poison_event": "rasdaemon/cxl",
		"rasdaemon/mc_event": "rasdaemon/mc_event", "klog/edac": "klog/edac"} {
		if got := reporter(src); got != want {
			t.Errorf("reporter(%q) = %q, want %q", src, got, want)
		}
	}
}

func TestMarkDuplicatesDeclaredByAdapter(t *testing.T) {
	s := newStore(t)
	// rasdaemon stored one error twice: as a decoded EDAC record and as the
	// raw machine check. Their addresses differ (system vs normalized), so
	// only the adapter can tell they are one error.
	mc := ev("rasdaemon/mc_event", "mc/1", at(0), 0x7f5d2c9c0)
	mce := ev("rasdaemon/mce_record", "mce/1", at(0), 0x1d2c9c0)
	mce.DupOfFingerprint = mc.Fingerprint
	// The kernel log has the EDAC line of the same error, with more detail.
	klog := ev("klog/edac", "L1", at(time.Second), 0x7f5d2c9c0)
	mc.Grade, mce.Grade, klog.Grade = model.GradeB, model.GradeB, model.GradeA
	// A declaration that names a record the ledger does not hold is ignored.
	orphan := ev("rasdaemon/mce_record", "mce/2", at(time.Hour), 0x9000)
	orphan.DupOfFingerprint = "no-such-record"
	insert(t, s, mce, mc, klog, orphan) // the duplicate is inserted before its primary

	if _, err := s.MarkDuplicates(ctx, "h1", DuplicateTolerance{}); err != nil {
		t.Fatal(err)
	}
	if got := dupOf(t, s, klog); got != 0 {
		t.Errorf("the grade A record should stay, dup_of = %d", got)
	}
	if got := dupOf(t, s, mc); got != klog.ID {
		t.Errorf("mc_event dup_of = %d, want %d", got, klog.ID)
	}
	// The machine check was declared a duplicate of mc_event, which is now a
	// duplicate itself: it must point at the record that stays.
	if got := dupOf(t, s, mce); got != klog.ID {
		t.Errorf("mce_record dup_of = %d, want %d (the surviving record)", got, klog.ID)
	}
	if got := dupOf(t, s, orphan); got != 0 {
		t.Errorf("orphan declaration: dup_of = %d, want 0", got)
	}
	totals, _ := s.Totals(ctx, EventFilter{Host: "h1"})
	var ce int64
	for _, tt := range totals {
		ce += tt.CE
	}
	if ce != 2 {
		t.Errorf("CE total = %d, want 2 (one error seen three ways, plus the orphan)", ce)
	}
}

func counterFixture(t *testing.T, order []int) *Store {
	t.Helper()
	s := newStore(t)
	samples := []*model.CounterSample{
		// Counters started at t0 (10 h before the first reading).
		{ObservedAt: at(10 * time.Hour), MC: 0, MCName: "Intel_10nm Socket#0 IMC#0", DIMMIndex: 2, Location: "channel:1 slot:0",
			Label: "CPU0_C1", CE: 50, UE: 0, SecondsSinceReset: model.Some(int64(36000)), Source: "edac-sysfs", RawRef: "mc0/dimm2@10h"},
		{ObservedAt: at(20 * time.Hour), MC: 0, DIMMIndex: 2, Location: "channel:1 slot:0", Label: "CPU0_C1",
			CE: 53, UE: 1, SecondsSinceReset: model.Some(int64(72000)), Source: "edac-sysfs", RawRef: "mc0/dimm2@20h"},
		// A reboot at 39 h reset the counters.
		{ObservedAt: at(40 * time.Hour), MC: 0, DIMMIndex: 2, Location: "channel:1 slot:0", Label: "CPU0_C1",
			CE: 2, UE: 0, SecondsSinceReset: model.Some(int64(3600)), Source: "edac-sysfs", RawRef: "mc0/dimm2@40h"},
		// Another DIMM with nothing to report.
		{ObservedAt: at(10 * time.Hour), MC: 0, DIMMIndex: 3, Location: "channel:1 slot:1", Label: "CPU0_C2",
			SecondsSinceReset: model.Some(int64(36000)), Source: "edac-sysfs", RawRef: "mc0/dimm3@10h"},
	}
	for _, i := range order {
		mustUpdate(t, s, func(w *Writer) error {
			_, err := w.InsertCounterSample("h1", samples[i])
			return err
		})
		if err := s.DeriveCounterEvents(ctx, "h1"); err != nil {
			t.Fatalf("DeriveCounterEvents: %v", err)
		}
	}
	return s
}

type counterRow struct {
	At, Start time.Duration
	Sev       model.Severity
	Count     int
}

func counterRows(t *testing.T, s *Store) []counterRow {
	t.Helper()
	list, err := s.EventList(ctx, EventFilter{Kinds: []model.EventKind{model.KindCounter}})
	if err != nil {
		t.Fatal(err)
	}
	var out []counterRow
	for _, e := range list {
		if e.Grade != model.GradeC || e.Label != "CPU0_C1" || e.EdacLocation != "channel:1 slot:0" || !e.EdacMC.Valid {
			t.Errorf("counter event fields: %+v", e)
		}
		out = append(out, counterRow{e.ErrorDT.Sub(t0), e.WindowStart.Sub(t0), e.ErrorSeverity, e.Count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].At != out[j].At {
			return out[i].At < out[j].At
		}
		return out[i].Sev < out[j].Sev
	})
	return out
}

func TestDeriveCounterEvents(t *testing.T) {
	want := []counterRow{
		{10 * time.Hour, 0, model.SevCE, 50},             // since the counters started
		{20 * time.Hour, 10 * time.Hour, model.SevCE, 3}, // growth since the previous reading
		{20 * time.Hour, 10 * time.Hour, model.SevUE, 1},
		{40 * time.Hour, 39 * time.Hour, model.SevCE, 2}, // after a reset the count starts over
	}
	for name, order := range map[string][]int{
		"in time order":   {0, 1, 2, 3},
		"newest first":    {2, 1, 0, 3},
		"middle one last": {0, 2, 3, 1},
	} {
		t.Run(name, func(t *testing.T) {
			s := counterFixture(t, order)
			if got := counterRows(t, s); !reflect.DeepEqual(got, want) {
				t.Errorf("counter events\n got: %+v\nwant: %+v", got, want)
			}
			// Deriving again must not add or change anything.
			if err := s.DeriveCounterEvents(ctx, "h1"); err != nil {
				t.Fatal(err)
			}
			if got := counterRows(t, s); !reflect.DeepEqual(got, want) {
				t.Errorf("after a second run\n got: %+v\nwant: %+v", got, want)
			}
		})
	}
}

func TestInsertCounterSampleIsIdempotent(t *testing.T) {
	s := newStore(t)
	c := &model.CounterSample{ObservedAt: at(time.Hour), MC: 0, DIMMIndex: 1, CE: 4, Source: "edac-sysfs"}
	var first, second bool
	mustUpdate(t, s, func(w *Writer) (err error) {
		if first, err = w.InsertCounterSample("h1", c); err != nil {
			return err
		}
		second, err = w.InsertCounterSample("h1", c)
		return err
	})
	if !first || second {
		t.Errorf("inserted = %v, %v; want true, false", first, second)
	}
}

func TestReconcileCounters(t *testing.T) {
	s := counterFixture(t, []int{0, 1, 2, 3})
	// rasdaemon logged 47 of the 50 errors of the first window, all 3 of the
	// second, none of the third. One of the first window is also in the
	// kernel log and must not be subtracted twice.
	var located []*model.Event
	for i := range 47 {
		e := ev("rasdaemon/mc_event", fmt.Sprintf("mc/%d", i), at(time.Hour+time.Duration(i)*time.Minute), uint64(0x10000+i*64))
		located = append(located, e)
	}
	dup := ev("klog/edac", "L1", at(time.Hour), 0x10000)
	located = append(located, dup)
	for i := range 3 {
		located = append(located, ev("rasdaemon/mc_event", fmt.Sprintf("mc/w2-%d", i), at(15*time.Hour+time.Duration(i)*time.Minute), uint64(0x20000+i*64)))
	}
	// An uncorrected error in the second window, and errors that must not
	// count: another slot, and exactly at the start of the window.
	ue := ev("rasdaemon/mc_event", "mc/ue", at(16*time.Hour), 0x30000)
	ue.ErrorSeverity = model.SevUE
	otherSlot := ev("rasdaemon/mc_event", "mc/other", at(15*time.Hour), 0x40000)
	otherSlot.Slot, otherSlot.Label = "CPU0_C2", "CPU0_C2"
	located = append(located, ue, otherSlot)
	insert(t, s, located...)

	// Counter events carry the EDAC label; resolve it to the slot name.
	keys, _ := s.EventLocations(ctx, "h1")
	mustUpdate(t, s, func(w *Writer) error {
		for _, k := range keys {
			if k.Label == "CPU0_C1" {
				if err := w.SetEventSlot("h1", k, "CPU0_C1"); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if _, err := s.MarkDuplicates(ctx, "h1", DuplicateTolerance{}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileCounters(ctx, "h1"); err != nil {
		t.Fatal(err)
	}

	list, _ := s.EventList(ctx, EventFilter{Kinds: []model.EventKind{model.KindCounter}})
	got := map[string]int{}
	for _, e := range list {
		got[fmt.Sprintf("%s@%s", e.ErrorSeverity, e.ErrorDT.Sub(t0))] = e.Covered
	}
	want := map[string]int{"CE@10h0m0s": 47, "CE@20h0m0s": 3, "UE@20h0m0s": 1, "CE@40h0m0s": 0}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("covered = %v, want %v", got, want)
	}

	// Totals for the slot equal what the counters saw: 50 + 3 + 2 corrected
	// and 1 uncorrected, although only 50 corrected errors were logged.
	totals, _ := s.Totals(ctx, EventFilter{Host: "h1", Slot: "CPU0_C1"})
	var ce, ue2 int64
	for _, tt := range totals {
		ce, ue2 = ce+tt.CE, ue2+tt.UE
	}
	if ce != 55 || ue2 != 1 {
		t.Errorf("slot totals: CE %d UE %d, want 55 and 1", ce, ue2)
	}

	// Reconciling again gives the same result.
	if err := s.ReconcileCounters(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	again, _ := s.Totals(ctx, EventFilter{Host: "h1", Slot: "CPU0_C1"})
	if !reflect.DeepEqual(totals, again) {
		t.Error("second ReconcileCounters run changed the totals")
	}
}
