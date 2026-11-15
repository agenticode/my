package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
)

// clockHost is a host with one boot, its errors in the rasdaemon database
// and in the kernel log. rasdaemon's clock is ahead of the kernel's by
// ahead.
type clockHost struct {
	s      *Store
	daemon []*model.Event
	kernel []*model.Event
	page   *model.Action
}

// clockErrors lists errors of the boot: time since its start and address.
var clockErrors = []struct {
	after time.Duration
	addr  uint64
}{
	{2 * time.Minute, 0x100010140}, {2*time.Minute + 3*time.Second, 0x100118140}, {5 * time.Minute, 0x100238180},
	{5*time.Minute + 3*time.Second, 0x100010140}, {40 * time.Minute, 0x1002a0140}, {3 * time.Hour, 0x100010140},
	{3*time.Hour + 4*time.Second, 0x1003f0180}, {9 * time.Hour, 0x100118140},
}

func newClockHost(t *testing.T, boot string, start time.Time, ahead time.Duration, n int) *clockHost {
	t.Helper()
	h := &clockHost{s: newStore(t)}
	h.add(t, boot, start, ahead, n)
	return h
}

// add stores the first n errors of a boot in both sources.
func (h *clockHost) add(t *testing.T, boot string, start time.Time, ahead time.Duration, n int) {
	t.Helper()
	h.addRange(t, boot, start, ahead, 0, n)
}

// addRange stores the errors from..to-1 of a boot in both sources.
func (h *clockHost) addRange(t *testing.T, boot string, start time.Time, ahead time.Duration, from, to int) {
	t.Helper()
	for i, e := range clockErrors[:to] {
		if i < from {
			continue
		}
		when := start.Add(e.after)
		// The kernel log has the time to the microsecond; rasdaemon
		// counts in whole seconds of its own clock.
		k := ev("klog/edac", fmt.Sprintf("%s#L%d", boot, i), when.Add(321*time.Millisecond), e.addr)
		d := ev("rasdaemon/mc_event", fmt.Sprintf("%s#mc/%d", boot, i), when.Add(ahead).Truncate(time.Second), e.addr)
		k.BootID, d.BootID = boot, boot
		k.Grade, d.Grade = model.GradeA, model.GradeA
		h.kernel, h.daemon = append(h.kernel, k), append(h.daemon, d)
		insert(t, h.s, k, d)
	}
}

func (h *clockHost) ce(t *testing.T) int64 {
	t.Helper()
	if _, err := h.s.MarkDuplicates(ctx, "h1", DuplicateTolerance{}); err != nil {
		t.Fatal(err)
	}
	totals, err := h.s.Totals(ctx, EventFilter{Host: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	var n int64
	for _, x := range totals {
		n += x.CE
	}
	return n
}

// rasdaemon's clock ran 10 seconds ahead of the kernel's for a whole boot,
// as on the lab host after NTP set the clock back. The database has no
// machine check record that would say so. The kernel log has the same
// errors.
func TestAlignClocks(t *testing.T) {
	start := at(40 * day)
	h := newClockHost(t, "b1", start, 10*time.Second, len(clockErrors))
	// A page the kernel gave up, recorded by rasdaemon in the same boot.
	h.page = &model.Action{Host: "h1", At: start.Add(3*time.Hour + 10*time.Second), Kind: model.ActionPageOffline, Method: "hard",
		OsPhysicalAddress: model.Some(uint64(0x100010000)), Source: "rasdaemon/memory_failure_event", RawRef: "mf/1", BootID: "b1",
		Fingerprint: model.Fingerprint("mf", "1")}
	mustUpdate(t, h.s, func(w *Writer) error { _, err := w.InsertAction(h.page); return err })

	// As read: every error is ten seconds from its other record and counts twice.
	if got := h.ce(t); got != 16 {
		t.Fatalf("before: CE %d, want 16 (each of the 8 errors twice)", got)
	}
	got, err := h.s.AlignClocks(ctx, "h1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].BootID != "b1" || got[0].Offset != -10*time.Second || got[0].Matched != 8 || got[0].Records != 9 {
		t.Fatalf("alignment = %+v, want boot b1 moved by -10s on 8 errors, 9 records", got)
	}
	for i, d := range h.daemon {
		e, err := h.s.Event(ctx, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		want := start.Add(clockErrors[i].after)
		if !e.ErrorDT.Equal(want) || e.TimeQuality != model.TimeExact {
			t.Errorf("rasdaemon record %d is dated %s (%s), want %s", i, e.ErrorDT.Format(time.RFC3339), e.TimeQuality, want.Format(time.RFC3339))
		}
	}
	actions, err := h.s.Actions(ctx, ActionFilter{Host: "h1"})
	if err != nil || len(actions) != 1 || !actions[0].At.Equal(start.Add(3*time.Hour)) {
		t.Errorf("the page offline was not moved with the errors: %+v, %v", actions, err)
	}
	// The kernel log is left alone.
	for i, k := range h.kernel {
		if e, _ := h.s.Event(ctx, k.ID); !e.ErrorDT.Equal(k.ErrorDT) {
			t.Errorf("kernel log record %d was moved", i)
		}
	}
	if got := h.ce(t); got != 8 {
		t.Errorf("after: CE %d, want 8", got)
	}
	// A second run finds the clocks in agreement.
	if again, err := h.s.AlignClocks(ctx, "h1"); err != nil || len(again) != 0 {
		t.Errorf("second run: %+v, %v", again, err)
	}
}

func TestAlignClocksCases(t *testing.T) {
	start := at(40 * day)
	sec := time.Second
	tests := []struct {
		name  string
		ahead time.Duration
		n     int
		moved time.Duration // zero: nothing is moved
	}{
		{"clocks agree", 0, 8, 0},
		{"a second apart is how rasdaemon rounds", sec, 8, 0},
		{"two seconds apart are one error anyway", -2 * sec, 8, 0},
		{"three seconds apart", 3 * sec, 8, -3 * sec},
		{"rasdaemon behind", -45 * sec, 8, 45 * sec},
		{"one error in both sources, seconds apart", 7 * sec, 1, -7 * sec},
		{"two errors that agree", 100 * sec, 2, -100 * sec},
		{"one error ten minutes apart is not enough", 10 * time.Minute, 1, 0},
		{"two errors ten minutes apart are not enough", 10 * time.Minute, 2, 0},
		{"three errors that agree, a time zone apart", 9 * time.Hour, 5, -9 * time.Hour},
		{"beyond any time zone", 20 * time.Hour, 8, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newClockHost(t, "b1", start, tt.ahead, tt.n)
			got, err := h.s.AlignClocks(ctx, "h1")
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case tt.moved == 0 && len(got) != 0:
				t.Errorf("moved: %+v", got)
			case tt.moved != 0 && (len(got) != 1 || got[0].Offset != tt.moved || got[0].Records != tt.n):
				t.Errorf("alignment = %+v, want %d records moved by %s", got, tt.n, tt.moved)
			}
			want := int64(tt.n)
			if tt.moved == 0 && tt.ahead.Abs() > 5*sec {
				want = 2 * int64(tt.n) // nothing settles it: both records of each error stay
			}
			if ce := h.ce(t); ce != want {
				t.Errorf("CE %d, want %d", ce, want)
			}
		})
	}

	t.Run("each boot has its own clock error", func(t *testing.T) {
		h := newClockHost(t, "b1", start, 10*sec, 5)
		h.add(t, "b2", start.Add(3*day), 0, 5)
		h.add(t, "b3", start.Add(6*day), -7*sec, 5)
		got, err := h.s.AlignClocks(ctx, "h1")
		if err != nil || len(got) != 2 || got[0].BootID != "b1" || got[0].Offset != -10*sec || got[1].BootID != "b3" || got[1].Offset != 7*sec {
			t.Fatalf("alignment = %+v, %v", got, err)
		}
		if ce := h.ce(t); ce != 15 {
			t.Errorf("CE %d, want 15", ce)
		}
	})

	t.Run("errors that point two ways settle nothing", func(t *testing.T) {
		h := newClockHost(t, "b1", start, 10*sec, 1)
		// A second error whose two records are 40 seconds apart.
		k := ev("klog/edac", "L9", start.Add(time.Hour), 0x555000)
		d := ev("rasdaemon/mc_event", "mc/9", start.Add(time.Hour+40*sec), 0x555000)
		k.BootID, d.BootID = "b1", "b1"
		insert(t, h.s, k, d)
		if got, err := h.s.AlignClocks(ctx, "h1"); err != nil || len(got) != 0 {
			t.Errorf("alignment = %+v, %v", got, err)
		}
	})

	t.Run("records without an address agree by their slot", func(t *testing.T) {
		s := newStore(t)
		for i := 0; i < 4; i++ {
			when := start.Add(time.Duration(i) * 20 * time.Minute)
			k := &model.Event{Host: "h1", ErrorDT: when, ErrorSeverity: model.SevCE, Source: "klog/ghes", RawRef: fmt.Sprintf("L%d", i),
				Slot: "DIMM 0", Label: "DIMM 0", BootID: "b1", Fingerprint: model.Fingerprint("k", fmt.Sprint(i))}
			d := &model.Event{Host: "h1", ErrorDT: when.Add(12 * sec), ErrorSeverity: model.SevCE, Source: "rasdaemon/mc_event", RawRef: fmt.Sprintf("mc/%d", i),
				Slot: "DIMM 0", Label: "DIMM 0", BootID: "b1", Fingerprint: model.Fingerprint("d", fmt.Sprint(i))}
			insert(t, s, k, d)
		}
		got, err := s.AlignClocks(ctx, "h1")
		if err != nil || len(got) != 1 || got[0].Offset != -12*sec || got[0].Matched != 4 {
			t.Errorf("alignment = %+v, %v", got, err)
		}
	})

	// The kernel's time of every rasdaemon record of the host, in the
	// order of clockErrors.
	kernelTimes := func(t *testing.T, h *clockHost, want func(i int) time.Duration) {
		t.Helper()
		for i, d := range h.daemon {
			e, err := h.s.Event(ctx, d.ID)
			if err != nil {
				t.Fatal(err)
			}
			if w := start.Add(clockErrors[i].after + want(i)); !e.ErrorDT.Equal(w) {
				t.Errorf("rasdaemon record %d is dated %s, want %s", i, e.ErrorDT.Format(time.RFC3339), w.Format(time.RFC3339))
			}
		}
	}
	none := func(int) time.Duration { return 0 }

	t.Run("a later scan adds records of the same boot", func(t *testing.T) {
		h := newClockHost(t, "b1", start, 10*sec, 5)
		if got, err := h.s.AlignClocks(ctx, "h1"); err != nil || len(got) != 1 || got[0].Records != 5 {
			t.Fatalf("first scan: %+v, %v", got, err)
		}
		// One more error, at an address that failed before.
		h.addRange(t, "b1", start, 10*sec, 5, 6)
		got, err := h.s.AlignClocks(ctx, "h1")
		if err != nil || len(got) != 1 || got[0].Offset != -10*sec || got[0].Records != 1 || got[0].Matched != 1 {
			t.Fatalf("second scan: %+v, %v", got, err)
		}
		// Two more.
		h.addRange(t, "b1", start, 10*sec, 6, 8)
		got, err = h.s.AlignClocks(ctx, "h1")
		if err != nil || len(got) != 1 || got[0].Offset != -10*sec || got[0].Records != 2 || got[0].Matched != 2 {
			t.Fatalf("third scan: %+v, %v", got, err)
		}
		kernelTimes(t, h, none)
		if ce := h.ce(t); ce != 8 {
			t.Errorf("CE %d, want 8", ce)
		}
		if again, err := h.s.AlignClocks(ctx, "h1"); err != nil || len(again) != 0 {
			t.Errorf("fourth scan: %+v, %v", again, err)
		}
	})

	t.Run("rasdaemon was started again after the clock was set", func(t *testing.T) {
		// Five errors while its clock was ahead, three after the restart.
		h := newClockHost(t, "b1", start, 10*sec, 5)
		h.addRange(t, "b1", start, 0, 5, 8)
		// Two errors the kernel log has lost, one in each run, and a page
		// offline in each.
		lost := []*model.Event{
			ev("rasdaemon/mc_event", "mc/lost1", start.Add(30*time.Minute+10*sec), 0x777000),
			ev("rasdaemon/mc_event", "mc/lost2", start.Add(8*time.Hour), 0x778000),
		}
		for _, e := range lost {
			e.BootID = "b1"
		}
		insert(t, h.s, lost...)
		pages := []*model.Action{
			{At: start.Add(41*time.Minute + 10*sec), RawRef: "mf/1"},
			{At: start.Add(3*time.Hour + 5*sec), RawRef: "mf/2"},
		}
		for _, a := range pages {
			a.Host, a.Kind, a.Method, a.Source, a.BootID = "h1", model.ActionPageOffline, "hard", "rasdaemon/memory_failure_event", "b1"
			a.Fingerprint = model.Fingerprint("mf", a.RawRef)
			mustUpdate(t, h.s, func(w *Writer) error { _, err := w.InsertAction(a); return err })
		}
		got, err := h.s.AlignClocks(ctx, "h1")
		if err != nil || len(got) != 1 || got[0].Offset != -10*sec || got[0].Matched != 5 || got[0].Records != 7 {
			t.Fatalf("alignment = %+v, %v, want 5 errors, the lost one next to them and a page offline moved by -10s", got, err)
		}
		kernelTimes(t, h, none)
		for i, want := range []time.Time{start.Add(30 * time.Minute), start.Add(8 * time.Hour)} {
			if e, _ := h.s.Event(ctx, lost[i].ID); !e.ErrorDT.Equal(want) {
				t.Errorf("lost record %d is dated %s, want %s", i, e.ErrorDT.Format(time.RFC3339), want.Format(time.RFC3339))
			}
		}
		actions, err := h.s.Actions(ctx, ActionFilter{Host: "h1"})
		if err != nil || len(actions) != 2 {
			t.Fatalf("actions: %+v, %v", actions, err)
		}
		for _, a := range actions {
			want := start.Add(41 * time.Minute)
			if a.RawRef == "mf/2" {
				want = start.Add(3*time.Hour + 5*sec)
			}
			if !a.At.Equal(want) {
				t.Errorf("page offline %s is dated %s, want %s", a.RawRef, a.At.Format(time.RFC3339), want.Format(time.RFC3339))
			}
		}
		if ce := h.ce(t); ce != 10 {
			t.Errorf("CE %d, want 10", ce)
		}
	})

	t.Run("the kernel log lost lines", func(t *testing.T) {
		h := newClockHost(t, "b1", start, 10*sec, 8)
		// The line of an error whose address failed three times, and the
		// line of one that failed once.
		mustUpdate(t, h.s, func(w *Writer) error {
			_, err := w.exec(`DELETE FROM event WHERE id IN (?, ?)`, h.kernel[3].ID, h.kernel[6].ID)
			return err
		})
		got, err := h.s.AlignClocks(ctx, "h1")
		if err != nil || len(got) != 1 || got[0].Offset != -10*sec || got[0].Matched != 6 || got[0].Records != 8 {
			t.Fatalf("alignment = %+v, %v, want all 8 records moved on 6 errors", got, err)
		}
		kernelTimes(t, h, none)
		if ce := h.ce(t); ce != 8 {
			t.Errorf("CE %d, want 8", ce)
		}
	})

	t.Run("one cell fails hundreds of times", func(t *testing.T) {
		s := newStore(t)
		const n = 300
		var daemon []*model.Event
		for i := 0; i < n; i++ {
			when := start.Add(time.Duration(i) * 1300 * time.Millisecond)
			k := ev("klog/edac", fmt.Sprintf("L%d", i), when.Add(321*time.Millisecond), 0x100010140)
			d := ev("rasdaemon/mc_event", fmt.Sprintf("mc/%d", i), when.Add(10*sec).Truncate(sec), 0x100010140)
			k.BootID, d.BootID = "b1", "b1"
			daemon = append(daemon, d)
			insert(t, s, k, d)
		}
		// The cell fails about once a second, so a record has a kernel log
		// line at every difference; only the first and the last record
		// say which one it is, to the second that rasdaemon cuts off.
		got, err := s.AlignClocks(ctx, "h1")
		if err != nil || len(got) != 1 || (got[0].Offset != -10*sec && got[0].Offset != -9*sec) || got[0].Records != n || got[0].Matched != n {
			t.Fatalf("alignment = %+v, %v", got, err)
		}
		for i, d := range daemon {
			e, _ := s.Event(ctx, d.ID)
			want := start.Add(time.Duration(i) * 1300 * time.Millisecond)
			if off := e.ErrorDT.Sub(want); off < -sec || off > sec {
				t.Fatalf("record %d is dated %s, want %s", i, e.ErrorDT.Format(time.RFC3339), want.Format(time.RFC3339))
			}
		}
		h := &clockHost{s: s}
		if ce := h.ce(t); ce != n {
			t.Errorf("CE %d, want %d", ce, n)
		}
		if again, err := s.AlignClocks(ctx, "h1"); err != nil || len(again) != 0 {
			t.Errorf("second run: %+v, %v", again, err)
		}
	})

	t.Run("the kernel log has two lines for an error", func(t *testing.T) {
		h := newClockHost(t, "b1", start, 10*sec, 5)
		twin := func(from, to int) {
			for i := from; i < to; i++ {
				k := ev("klog/ghes", fmt.Sprintf("G%d", i), h.kernel[i].ErrorDT, clockErrors[i].addr)
				k.BootID = "b1"
				insert(t, h.s, k)
			}
		}
		twin(0, 5)
		if got, err := h.s.AlignClocks(ctx, "h1"); err != nil || len(got) != 1 || got[0].Offset != -10*sec || got[0].Records != 5 {
			t.Fatalf("first scan: %+v, %v", got, err)
		}
		h.addRange(t, "b1", start, 10*sec, 5, 8)
		twin(5, 8)
		if got, err := h.s.AlignClocks(ctx, "h1"); err != nil || len(got) != 1 || got[0].Offset != -10*sec || got[0].Records != 3 {
			t.Fatalf("second scan: %+v, %v", got, err)
		}
		kernelTimes(t, h, none)
	})

	t.Run("a kernel log that holds the end of the boot only", func(t *testing.T) {
		// Three cells fail every ten minutes. The database has ten
		// rounds, the kernel log the last two. The clocks agree: an early
		// record is not the late line of the same cell.
		s := newStore(t)
		for round := 0; round < 10; round++ {
			for cell := 0; cell < 3; cell++ {
				when := start.Add(time.Duration(round)*10*time.Minute + time.Duration(cell)*7*sec)
				addr := uint64(0x200000000 + cell*0x10000)
				d := ev("rasdaemon/mc_event", fmt.Sprintf("mc/%d.%d", round, cell), when, addr)
				d.BootID = "b1"
				insert(t, s, d)
				if round >= 8 {
					k := ev("klog/edac", fmt.Sprintf("L%d.%d", round, cell), when.Add(200*time.Millisecond), addr)
					k.BootID = "b1"
					insert(t, s, k)
				}
			}
		}
		if got, err := s.AlignClocks(ctx, "h1"); err != nil || len(got) != 0 {
			t.Errorf("alignment = %+v, %v", got, err)
		}
		h := &clockHost{s: s}
		if ce := h.ce(t); ce != 30 {
			t.Errorf("CE %d, want 30", ce)
		}
	})

	t.Run("two errors at one address, each source lost one", func(t *testing.T) {
		// The clocks agree for the eight errors around them. The record
		// of the first and the line of the second, 40 seconds later, are
		// two errors.
		h := newClockHost(t, "b1", start, 0, 8)
		d := ev("rasdaemon/mc_event", "mc/first", start.Add(time.Hour), 0x555000)
		k := ev("klog/edac", "L/second", start.Add(time.Hour+40*sec), 0x555000)
		d.BootID, k.BootID = "b1", "b1"
		insert(t, h.s, d, k)
		if got, err := h.s.AlignClocks(ctx, "h1"); err != nil || len(got) != 0 {
			t.Errorf("alignment = %+v, %v", got, err)
		}
		if ce := h.ce(t); ce != 10 {
			t.Errorf("CE %d, want 10", ce)
		}
	})

	t.Run("a database alone, and a kernel log alone", func(t *testing.T) {
		s := newStore(t)
		insert(t, s, ev("rasdaemon/mc_event", "mc/1", start, 0x1000))
		if got, err := s.AlignClocks(ctx, "h1"); err != nil || len(got) != 0 {
			t.Errorf("database alone: %+v, %v", got, err)
		}
		s = newStore(t)
		insert(t, s, ev("klog/edac", "L1", start, 0x1000))
		if got, err := s.AlignClocks(ctx, "h1"); err != nil || len(got) != 0 {
			t.Errorf("kernel log alone: %+v, %v", got, err)
		}
		// Another host is not looked at.
		if got, err := s.AlignClocks(ctx, "other"); err != nil || len(got) != 0 {
			t.Errorf("other host: %+v, %v", got, err)
		}
	})
}

// A cell that fails thousands of times gives more pairs of records than
// the pass compares: a selection of the records decides, and it keeps the
// first and the last, which alone say what the difference is.
func TestClockSolveSelects(t *testing.T) {
	const n = 5000
	b := &clockBoot{anchors: map[string][]int64{}}
	for i := 0; i < n; i++ {
		at := int64(1_800_000_000 + 2*i)
		b.anchors["CE|a40"] = append(b.anchors["CE|a40"], at)
		b.recs = append(b.recs, clockRec{id: int64(i + 1), at: at + 10, key: "CE|a40"})
	}
	// A record the kernel log has nothing of goes with the others.
	b.recs = append(b.recs, clockRec{id: n + 1, at: b.recs[n-1].at + 60, key: "CE|a80"})
	offset, matched, marks := b.solve()
	if offset != -10 || matched != n {
		t.Fatalf("offset %d on %d records, want -10 on %d", offset, matched, n)
	}
	for i, m := range marks[:n] {
		if m != clockMoves {
			t.Fatalf("record %d: mark %d", i, m)
		}
	}
	if marks[n] != clockUnknown {
		t.Errorf("the record without a kernel log line: mark %d", marks[n])
	}

	counts := make([]int, 10)
	for i := range counts {
		counts[i] = clockPairs / 2
	}
	if got := clockPick(counts); len(got) != 3 || got[0] != 0 || got[1] <= 0 || got[1] >= 9 || got[2] != 9 {
		t.Errorf("selection of 10 records over the limit = %v, want the first, one between and the last", got)
	}
	if got := clockPick([]int{5, 5}); len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Errorf("selection of 2 records = %v", got)
	}
	if got := clockPick(nil); len(got) != 0 {
		t.Errorf("selection of no records = %v", got)
	}
}
