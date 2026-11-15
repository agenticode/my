package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
)

// alignFixture is a host whose boot started at at(bootAt). rasdaemon dated
// its errors; a kernel log saved with dmesg has the same errors with
// seconds since boot only. The daemon stamps a record a little after the
// kernel printed it.
type alignFixture struct {
	s      *Store
	bootID string
	dated  []*model.Event
	loose  []*model.Event
}

const bootAt = 40 * day

// errorsAt lists the errors of the boot: seconds since boot and address.
var errorsAt = []struct {
	mono time.Duration
	addr uint64
}{
	{90 * time.Second, 0x1000}, {95 * time.Second, 0x1040}, {20 * time.Minute, 0x1000}, {3 * time.Hour, 0x2000},
	{3*time.Hour + 2*time.Second, 0x1000}, {26 * time.Hour, 0x1040}, {50 * time.Hour, 0x2000}, {51 * time.Hour, 0x9000},
}

func newAlignFixture(t *testing.T, quality model.TimeQuality, wrongBy time.Duration) *alignFixture {
	t.Helper()
	f := &alignFixture{s: newStore(t)}
	// What the kernel log thinks the boot started at: the epoch for a log
	// without dates, or a start worked out from a capture time that is off.
	start := time.Unix(0, 0).UTC()
	if quality == model.TimeEstimated {
		start = at(bootAt).Add(wrongBy)
	}
	f.bootID = upsertBoot(t, f.s, &model.Boot{Host: "h1", StartedAt: start, StartQuality: quality, Derived: true,
		LastSeen: start.Add(51 * time.Hour), Kernel: "5.14.0", Source: "klog"})
	for i, e := range errorsAt {
		d := ev("rasdaemon/mc_event", fmt.Sprintf("db#%d", i), at(bootAt).Add(e.mono).Add(400*time.Millisecond).Truncate(time.Second), e.addr)
		l := ev("klog/edac", fmt.Sprintf("dmesg#L%d", i), start.Add(e.mono).Add(123456*time.Microsecond), e.addr)
		l.TimeQuality, l.BootID = quality, f.bootID
		f.dated, f.loose = append(f.dated, d), append(f.loose, l)
	}
	insert(t, f.s, f.dated...)
	insert(t, f.s, f.loose...)
	return f
}

func (f *alignFixture) event(t *testing.T, e *model.Event) *model.Event {
	t.Helper()
	got, err := f.s.Event(ctx, e.ID)
	if err != nil || got == nil {
		t.Fatalf("event %d: %v", e.ID, err)
	}
	return got
}

func TestAlignTimesDatesALogWithoutDates(t *testing.T) {
	f := newAlignFixture(t, model.TimeRelative, 0)
	// A page taken offline during the boot, known by seconds since boot.
	act := &model.Action{Host: "h1", At: time.Unix(0, 0).UTC().Add(3 * time.Hour), TimeQuality: model.TimeRelative, Kind: model.ActionPageOffline,
		BootID: f.bootID, Source: "klog/offline", RawRef: "dmesg#L9", Fingerprint: "a1", Result: model.ResultOK}
	// An error only the kernel log has, without an address.
	extra := ev("klog/edac", "dmesg#L99", time.Unix(0, 0).UTC().Add(4*time.Hour), 0)
	extra.OsPhysicalAddress, extra.TimeQuality, extra.BootID = model.Opt[uint64]{}, model.TimeRelative, f.bootID
	mustUpdate(t, f.s, func(w *Writer) error {
		if _, err := w.InsertAction(act); err != nil {
			return err
		}
		_, err := w.InsertEvent(extra)
		return err
	})

	got, err := f.s.AlignTimes(ctx, "h1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Aligned || got[0].Records != len(errorsAt)+1 || got[0].Matched != len(errorsAt) || got[0].BootID != f.bootID {
		t.Fatalf("alignment = %+v", got)
	}
	// The boot is found to the second.
	if d := got[0].StartedAt.Sub(at(bootAt)).Abs(); d > time.Second {
		t.Errorf("boot start = %v, want %v", got[0].StartedAt, at(bootAt))
	}
	for i, l := range f.loose {
		e := f.event(t, l)
		want := at(bootAt).Add(errorsAt[i].mono)
		if e.TimeQuality != model.TimeEstimated || e.ErrorDT.Sub(want).Abs() > 2*time.Second {
			t.Errorf("record %d: %s at %v, want about %v", i, e.TimeQuality, e.ErrorDT, want)
		}
		// The fraction of the second is the kernel's own and stays.
		if e.ErrorDT.Nanosecond() != 123456000 {
			t.Errorf("record %d: fraction %d ns", i, e.ErrorDT.Nanosecond())
		}
	}
	// Records without an address move with their boot, and so do actions.
	if e := f.event(t, extra); e.TimeQuality != model.TimeEstimated || e.ErrorDT.Sub(at(bootAt).Add(4*time.Hour)).Abs() > 2*time.Second {
		t.Errorf("record without an address: %s at %v", e.TimeQuality, e.ErrorDT)
	}
	actions, _ := f.s.Actions(ctx, ActionFilter{Host: "h1"})
	if len(actions) != 1 || actions[0].TimeQuality != model.TimeEstimated || actions[0].At.Sub(at(bootAt).Add(3*time.Hour)).Abs() > 2*time.Second {
		t.Errorf("action = %+v", actions)
	}
	boots, _ := f.s.Boots(ctx, "h1")
	if len(boots) != 1 || boots[0].StartQuality != model.TimeEstimated || boots[0].StartedAt.Sub(at(bootAt)).Abs() > time.Second ||
		boots[0].LastSeen.Sub(at(bootAt).Add(51*time.Hour)).Abs() > time.Second || boots[0].Kernel != "5.14.0" {
		t.Errorf("boot = %+v", boots)
	}

	// Now the two sources are seen to report the same errors.
	n, err := f.s.MarkDuplicates(ctx, "h1", DuplicateTolerance{})
	if err != nil || n != len(errorsAt) {
		t.Errorf("%d duplicates marked, want %d (%v)", n, len(errorsAt), err)
	}
	totals, _ := f.s.Totals(ctx, EventFilter{Host: "h1"})
	var ce int64
	for _, x := range totals {
		ce += x.CE
	}
	if ce != int64(len(errorsAt))+1 {
		t.Errorf("CE = %d, want %d: the errors both sources report, once, and the one only the log has", ce, len(errorsAt)+1)
	}

	// A second pass finds everything in place.
	again, err := f.s.AlignTimes(ctx, "h1")
	if err != nil || len(again) != 0 {
		t.Errorf("second pass = %+v, %v", again, err)
	}
	if e := f.event(t, f.loose[0]); e.ErrorDT.Sub(at(bootAt).Add(errorsAt[0].mono)).Abs() > 2*time.Second {
		t.Errorf("the second pass moved a record to %v", e.ErrorDT)
	}
}

func TestAlignTimesCorrectsAWrongCaptureTime(t *testing.T) {
	// The log was dated from the time of its file, which was touched six
	// days after the capture.
	f := newAlignFixture(t, model.TimeEstimated, 6*day+17*time.Minute)
	got, err := f.s.AlignTimes(ctx, "h1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Aligned || got[0].Offset.Round(time.Minute) != -(6*day+17*time.Minute) {
		t.Fatalf("alignment = %+v", got)
	}
	for i, l := range f.loose {
		if e := f.event(t, l); e.ErrorDT.Sub(at(bootAt).Add(errorsAt[i].mono)).Abs() > 2*time.Second {
			t.Errorf("record %d at %v", i, e.ErrorDT)
		}
	}
	if n, _ := f.s.MarkDuplicates(ctx, "h1", DuplicateTolerance{}); n != len(errorsAt) {
		t.Errorf("%d duplicates marked, want %d", n, len(errorsAt))
	}
	// A log whose estimate was right is left alone.
	f = newAlignFixture(t, model.TimeEstimated, 0)
	if got, err := f.s.AlignTimes(ctx, "h1"); err != nil || len(got) != 0 {
		t.Errorf("a log that needs no move: %+v, %v", got, err)
	}
}

func TestAlignTimesJoinsTheBootOtherSourcesKnow(t *testing.T) {
	f := newAlignFixture(t, model.TimeRelative, 0)
	// The journal knows this boot by its kernel boot ID and exact start.
	const kid = "0123456789abcdef0123456789abcdef"
	upsertBoot(t, f.s, &model.Boot{Host: "h1", BootID: kid, StartedAt: at(bootAt).Add(20 * time.Second), StartQuality: model.TimeExact, Source: "journal"})
	got, err := f.s.AlignTimes(ctx, "h1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Aligned || got[0].MergedInto != kid {
		t.Fatalf("alignment = %+v", got)
	}
	if ids := bootIDs(t, f.s, "h1"); len(ids) != 1 || ids[0] != kid {
		t.Errorf("boots = %v", ids)
	}
	for _, l := range f.loose {
		if e := f.event(t, l); e.BootID != kid {
			t.Errorf("record belongs to boot %q", e.BootID)
		}
	}
}

func TestAlignTimesLeavesAloneWhatItCannotSettle(t *testing.T) {
	// Nothing dated to compare with: a log without dates stays as it is,
	// and the pass has nothing to report.
	s := newStore(t)
	id := upsertBoot(t, s, &model.Boot{Host: "h1", StartedAt: time.Unix(0, 0).UTC(), StartQuality: model.TimeRelative, Derived: true, Source: "klog"})
	l := ev("klog/edac", "dmesg#L1", time.Unix(0, 0).UTC().Add(time.Hour), 0x1000)
	l.TimeQuality, l.BootID = model.TimeRelative, id
	insert(t, s, l)
	if got, err := s.AlignTimes(ctx, "h1"); err != nil || len(got) != 0 {
		t.Errorf("no dated records: %+v, %v", got, err)
	}
	// Dated records of the same source do not count: a source is not
	// compared with itself.
	d := ev("klog/edac", "syslog#L1", at(bootAt).Add(time.Hour), 0x1000)
	insert(t, s, d)
	if got, err := s.AlignTimes(ctx, "h1"); err != nil || len(got) != 0 {
		t.Errorf("dated records of the same source: %+v, %v", got, err)
	}

	// One cell that fails every hour, on the hour: every hour is as good a
	// boot time as the next. The pass says that it could not decide, and
	// moves nothing.
	s = newStore(t)
	id = upsertBoot(t, s, &model.Boot{Host: "h1", StartedAt: time.Unix(0, 0).UTC(), StartQuality: model.TimeRelative, Derived: true, Source: "klog"})
	for i := range 6 {
		d := ev("rasdaemon/mc_event", fmt.Sprintf("db#%d", i), at(bootAt).Add(time.Duration(i)*time.Hour), 0x1000)
		l := ev("klog/edac", fmt.Sprintf("dmesg#L%d", i), time.Unix(0, 0).UTC().Add(time.Duration(i)*time.Hour), 0x1000)
		l.TimeQuality, l.BootID = model.TimeRelative, id
		insert(t, s, d, l)
		if i == 2 {
			// Half of the log is missing from the database.
			break
		}
	}
	for i := 3; i < 6; i++ {
		d := ev("rasdaemon/mc_event", fmt.Sprintf("db#%d", i), at(bootAt).Add(time.Duration(i)*time.Hour), 0x1000)
		insert(t, s, d)
	}
	got, err := s.AlignTimes(ctx, "h1")
	if err != nil || len(got) != 1 || got[0].Aligned || got[0].Records != 3 {
		t.Fatalf("an ambiguous log: %+v, %v", got, err)
	}
	events, _ := s.EventList(ctx, EventFilter{Host: "h1"})
	relative := 0
	for _, e := range events {
		if e.TimeQuality == model.TimeRelative {
			relative++
		}
	}
	if relative != 3 {
		t.Errorf("%d records still carry seconds since boot, want 3", relative)
	}

	// Two records that agree are not enough to move a boot.
	s = newStore(t)
	id = upsertBoot(t, s, &model.Boot{Host: "h1", StartedAt: time.Unix(0, 0).UTC(), StartQuality: model.TimeRelative, Derived: true, Source: "klog"})
	for i, mono := range []time.Duration{time.Minute, 7 * time.Hour} {
		d := ev("rasdaemon/mc_event", fmt.Sprintf("db#%d", i), at(bootAt).Add(mono), uint64(0x1000+i*0x1000))
		l := ev("klog/edac", fmt.Sprintf("dmesg#L%d", i), time.Unix(0, 0).UTC().Add(mono), uint64(0x1000+i*0x1000))
		l.TimeQuality, l.BootID = model.TimeRelative, id
		insert(t, s, d, l)
	}
	if got, err := s.AlignTimes(ctx, "h1"); err != nil || len(got) != 1 || got[0].Aligned {
		t.Errorf("two matching records: %+v, %v", got, err)
	}
}
