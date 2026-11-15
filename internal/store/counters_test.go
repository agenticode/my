package store

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
)

// daemonSample is a reading of a counter that a program keeps per socket,
// channel and DIMM, as the mcelog daemon does.
func daemonSample(when time.Duration, ce, ue uint64) *model.CounterSample {
	return &model.CounterSample{
		ObservedAt: at(when), MC: 0, DIMMIndex: 0x201, Label: "CPU0_C1", BankLocator: "NODE 0 CHANNEL 1 DIMM 0",
		SocketId: model.Some(0), ChannelId: model.Some(1), ChannelSlotId: model.Some(0),
		CE: ce, UE: ue, Source: "mcelog", RawRef: fmt.Sprintf("client#L2@%s", when),
	}
}

// counterEvent is what a test compares of a counter event.
type counterEvent struct {
	Source    string
	At, Start time.Duration
	Sev       model.Severity
	Count     int
}

// counterEvents lists the counter events of the ledger. A window without a
// known start has Start -1.
func counterEvents(t *testing.T, s *Store) []counterEvent {
	t.Helper()
	list, err := s.EventList(ctx, EventFilter{Kinds: []model.EventKind{model.KindCounter}})
	if err != nil {
		t.Fatal(err)
	}
	var out []counterEvent
	for _, e := range list {
		start := time.Duration(-1)
		if !e.WindowStart.IsZero() {
			start = e.WindowStart.Sub(t0)
		}
		out = append(out, counterEvent{e.Source, e.ErrorDT.Sub(t0), start, e.ErrorSeverity, e.Count})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case a.Source != b.Source:
			return a.Source < b.Source
		case a.At != b.At:
			return a.At < b.At
		case a.Sev != b.Sev:
			return a.Sev < b.Sev
		}
		return a.Start < b.Start
	})
	return out
}

func addSamples(t *testing.T, s *Store, samples ...*model.CounterSample) {
	t.Helper()
	mustUpdate(t, s, func(w *Writer) error {
		for _, c := range samples {
			if _, err := w.InsertCounterSample("h1", c); err != nil {
				return err
			}
		}
		return nil
	})
	if err := s.DeriveCounterEvents(ctx, "h1"); err != nil {
		t.Fatalf("DeriveCounterEvents: %v", err)
	}
}

// Two sources read in the same second are two readings, and the growth of
// each is counted against its own earlier readings.
func TestCounterSourcesStayApart(t *testing.T) {
	s := newStore(t)
	edac := func(when time.Duration, ce uint64) *model.CounterSample {
		return &model.CounterSample{ObservedAt: at(when), MC: 0, DIMMIndex: 0x201, Location: "channel:1 slot:0", Label: "CPU0_C1",
			CE: ce, SecondsSinceReset: model.Some(int64(when / time.Second)), Source: "edac-sysfs", RawRef: "mc0/dimm513"}
	}
	// The same controller and index numbers in both sources, on purpose.
	addSamples(t, s, edac(10*time.Hour, 50), daemonSample(10*time.Hour, 40, 0), edac(20*time.Hour, 53), daemonSample(20*time.Hour, 45, 1))

	want := []counterEvent{
		{"edac-sysfs/counter", 10 * time.Hour, 0, model.SevCE, 50},
		{"edac-sysfs/counter", 20 * time.Hour, 10 * time.Hour, model.SevCE, 3},
		{"mcelog/counter", 10 * time.Hour, -1, model.SevCE, 40}, // the daemon does not say when it started
		{"mcelog/counter", 20 * time.Hour, 10 * time.Hour, model.SevCE, 5},
		{"mcelog/counter", 20 * time.Hour, 10 * time.Hour, model.SevUE, 1},
	}
	if got := counterEvents(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("counter events\n got: %+v\nwant: %+v", got, want)
	}

	// An EDAC counter carries its EDAC position, a counter placed by
	// numbers carries the numbers; neither carries the other.
	list, _ := s.EventList(ctx, EventFilter{Kinds: []model.EventKind{model.KindCounter}})
	for _, e := range list {
		placed := e.SocketId.Valid && e.ChannelId.Valid && e.ChannelSlotId.Valid
		positioned := e.EdacMC.Valid && e.EdacLocation != ""
		if (e.Source == "mcelog/counter") != placed || (e.Source == "edac-sysfs/counter") != positioned || placed == positioned {
			t.Errorf("%s: socket %v channel %v dimm %v, EDAC mc %v location %q", e.Source, e.SocketId, e.ChannelId, e.ChannelSlotId, e.EdacMC, e.EdacLocation)
		}
		if e.Label != "CPU0_C1" || e.Grade != model.GradeC {
			t.Errorf("%s: label %q grade %s", e.Source, e.Label, e.Grade)
		}
	}

	// The EDAC objects of the host are the EDAC readings alone.
	latest, err := s.LatestCounterSamples(ctx, "h1")
	if err != nil || len(latest) != 1 || latest[0].Source != "edac-sysfs" || latest[0].CE != 53 {
		t.Errorf("LatestCounterSamples = %+v, %v; want the EDAC reading of 20 h", latest, err)
	}
}

// A reading that says how many of its errors are recent gives the recent
// errors a window of their own.
func TestCounterRecentErrors(t *testing.T) {
	day := 24 * time.Hour
	recent := func(c *model.CounterSample, ce, ue uint64) *model.CounterSample {
		c.RecentCE, c.RecentCEWindow = model.Some(ce), day
		c.RecentUE, c.RecentUEWindow = model.Some(ue), day
		return c
	}
	for _, tc := range []struct {
		name    string
		boot    time.Duration // start of the boot, or -1 for none on record
		samples []*model.CounterSample
		want    []counterEvent
	}{
		{
			name: "first reading, boot known", boot: 0,
			samples: []*model.CounterSample{recent(daemonSample(100*time.Hour, 500, 2), 37, 0)},
			want: []counterEvent{
				{"mcelog/counter", 100 * time.Hour, 0, model.SevCE, 463},
				{"mcelog/counter", 100 * time.Hour, 76 * time.Hour, model.SevCE, 37},
				{"mcelog/counter", 100 * time.Hour, 0, model.SevUE, 2},
			},
		},
		{
			name: "first reading, no boot on record", boot: -1,
			samples: []*model.CounterSample{recent(daemonSample(100*time.Hour, 500, 0), 37, 0)},
			want: []counterEvent{
				{"mcelog/counter", 100 * time.Hour, -1, model.SevCE, 463},
				{"mcelog/counter", 100 * time.Hour, 76 * time.Hour, model.SevCE, 37},
			},
		},
		{
			name: "all errors are recent", boot: 0,
			samples: []*model.CounterSample{recent(daemonSample(100*time.Hour, 5, 1), 5, 1)},
			want: []counterEvent{
				{"mcelog/counter", 100 * time.Hour, 76 * time.Hour, model.SevCE, 5},
				{"mcelog/counter", 100 * time.Hour, 76 * time.Hour, model.SevUE, 1},
			},
		},
		{
			name: "boot younger than the window", boot: 90 * time.Hour,
			samples: []*model.CounterSample{recent(daemonSample(100*time.Hour, 12, 0), 7, 0)},
			// Everything happened in the 10 hours since the boot: the
			// 24 hours say nothing more.
			want: []counterEvent{{"mcelog/counter", 100 * time.Hour, 90 * time.Hour, model.SevCE, 12}},
		},
		{
			name: "second reading two days later", boot: 0,
			samples: []*model.CounterSample{
				recent(daemonSample(100*time.Hour, 500, 0), 37, 0),
				recent(daemonSample(148*time.Hour, 530, 0), 10, 0),
			},
			want: []counterEvent{
				{"mcelog/counter", 100 * time.Hour, 0, model.SevCE, 463},
				{"mcelog/counter", 100 * time.Hour, 76 * time.Hour, model.SevCE, 37},
				{"mcelog/counter", 148 * time.Hour, 100 * time.Hour, model.SevCE, 20},
				{"mcelog/counter", 148 * time.Hour, 124 * time.Hour, model.SevCE, 10},
			},
		},
		{
			name: "second reading within the window", boot: 0,
			samples: []*model.CounterSample{
				recent(daemonSample(100*time.Hour, 500, 0), 37, 0),
				recent(daemonSample(110*time.Hour, 504, 0), 41, 0),
			},
			// The growth of ten hours is narrower than the 24 hours.
			want: []counterEvent{
				{"mcelog/counter", 100 * time.Hour, 0, model.SevCE, 463},
				{"mcelog/counter", 100 * time.Hour, 76 * time.Hour, model.SevCE, 37},
				{"mcelog/counter", 110 * time.Hour, 100 * time.Hour, model.SevCE, 4},
			},
		},
		{
			name: "more recent errors than the growth", boot: 0,
			samples: []*model.CounterSample{
				daemonSample(100*time.Hour, 500, 0),
				recent(daemonSample(148*time.Hour, 503, 0), 9, 0), // inconsistent: 9 recent of 3 new
			},
			want: []counterEvent{
				{"mcelog/counter", 100 * time.Hour, 0, model.SevCE, 500},
				{"mcelog/counter", 148 * time.Hour, 124 * time.Hour, model.SevCE, 3},
			},
		},
		{
			name: "the daemon restarted", boot: 0,
			samples: []*model.CounterSample{
				recent(daemonSample(100*time.Hour, 500, 0), 0, 0),
				recent(daemonSample(148*time.Hour, 4, 0), 1, 0),
			},
			// The count went down: it started over, at a time not on record.
			want: []counterEvent{
				{"mcelog/counter", 100 * time.Hour, 0, model.SevCE, 500},
				{"mcelog/counter", 148 * time.Hour, 0, model.SevCE, 3},
				{"mcelog/counter", 148 * time.Hour, 124 * time.Hour, model.SevCE, 1},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, order := range [][]int{{0, 1}, {1, 0}} {
				s := newStore(t)
				if tc.boot >= 0 {
					mustUpdate(t, s, func(w *Writer) error {
						_, err := w.UpsertBoot(&model.Boot{Host: "h1", BootID: "b1", StartedAt: at(tc.boot), StartQuality: model.TimeExact, Source: "hostinfo"})
						return err
					})
				}
				for _, i := range order {
					if i < len(tc.samples) {
						addSamples(t, s, tc.samples[i])
					}
				}
				if got := counterEvents(t, s); !reflect.DeepEqual(got, tc.want) {
					t.Errorf("order %v\n got: %+v\nwant: %+v", order, got, tc.want)
				}
				if err := s.DeriveCounterEvents(ctx, "h1"); err != nil {
					t.Fatal(err)
				}
				if got := counterEvents(t, s); !reflect.DeepEqual(got, tc.want) {
					t.Errorf("order %v, after a second run\n got: %+v\nwant: %+v", order, got, tc.want)
				}
			}
		})
	}
}

// slotTotals returns the corrected and uncorrected totals of a slot.
func slotTotals(t *testing.T, s *Store, slot string) (ce, ue int64) {
	t.Helper()
	totals, err := s.Totals(ctx, EventFilter{Host: "h1", Slot: slot})
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range totals {
		ce, ue = ce+tt.CE, ue+tt.UE
	}
	return ce, ue
}

// reconcile ties the counter events to the slot their label names and
// runs the passes that count.
func reconcile(t *testing.T, s *Store) {
	t.Helper()
	keys, err := s.EventLocations(ctx, "h1")
	if err != nil {
		t.Fatal(err)
	}
	mustUpdate(t, s, func(w *Writer) error {
		for _, k := range keys {
			if k.Label != "" {
				if err := w.SetEventSlot("h1", k, k.Label); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if _, err := s.MarkDuplicates(ctx, "h1", DuplicateTolerance{}); err != nil {
		t.Fatal(err)
	}
	for range 2 { // a second run must change nothing
		if err := s.ReconcileCounters(ctx, "h1"); err != nil {
			t.Fatal(err)
		}
	}
}

// An error that located records, the EDAC counters and the counts of a
// daemon all report is counted once, and errors that only one of them saw
// are still counted.
func TestReconcileCounterSources(t *testing.T) {
	day := 24 * time.Hour
	edac := func(when time.Duration, ce, ue uint64) *model.CounterSample {
		return &model.CounterSample{ObservedAt: at(when), MC: 0, DIMMIndex: 2, Location: "channel:1 slot:0", Label: "CPU0_C1",
			CE: ce, UE: ue, SecondsSinceReset: model.Some(int64(when / time.Second)), Source: "edac-sysfs", RawRef: fmt.Sprintf("mc0/dimm2@%s", when)}
	}
	located := func(n int, from time.Duration) []*model.Event {
		var out []*model.Event
		for i := range n {
			out = append(out, ev("rasdaemon/mc_event", fmt.Sprintf("mc/%s/%d", from, i), at(from+time.Duration(i)*time.Minute), uint64(0x10000+i*64)))
		}
		return out
	}
	withRecent := func(c *model.CounterSample, ce uint64) *model.CounterSample {
		c.RecentCE, c.RecentCEWindow = model.Some(ce), day
		return c
	}
	for _, tc := range []struct {
		name           string
		samples        []*model.CounterSample
		events         []*model.Event
		wantCE, wantUE int64
	}{
		{
			name:    "the daemon alone",
			samples: []*model.CounterSample{daemonSample(100*time.Hour, 10, 1)},
			wantCE:  10, wantUE: 1,
		},
		{
			name:    "the daemon and located records of the same errors",
			samples: []*model.CounterSample{daemonSample(100*time.Hour, 10, 0)},
			events:  located(10, 50*time.Hour),
			wantCE:  10,
		},
		{
			name:    "the daemon saw more than was logged",
			samples: []*model.CounterSample{daemonSample(100*time.Hour, 10, 0)},
			events:  located(4, 50*time.Hour),
			wantCE:  10,
		},
		{
			name:    "both counters, the same errors",
			samples: []*model.CounterSample{edac(100*time.Hour, 10, 1), daemonSample(100*time.Hour, 10, 1)},
			wantCE:  10, wantUE: 1,
		},
		{
			name:    "both counters and located records",
			samples: []*model.CounterSample{edac(100*time.Hour, 10, 0), daemonSample(100*time.Hour, 10, 0)},
			events:  located(3, 50*time.Hour),
			wantCE:  10,
		},
		{
			name:    "the daemon counted more than the kernel",
			samples: []*model.CounterSample{edac(100*time.Hour, 10, 0), daemonSample(100*time.Hour, 12, 0)},
			events:  located(3, 50*time.Hour),
			wantCE:  12,
		},
		{
			name:    "the daemon counted fewer than the kernel",
			samples: []*model.CounterSample{edac(100*time.Hour, 10, 0), daemonSample(100*time.Hour, 8, 0)},
			wantCE:  10,
		},
		{
			name: "two readings of the daemon against one of the kernel",
			samples: []*model.CounterSample{
				daemonSample(50*time.Hour, 10, 0), daemonSample(100*time.Hour, 20, 0), edac(100*time.Hour, 12, 0),
			},
			// The kernel lost eight: its twelve cover twelve of the twenty.
			wantCE: 20,
		},
		{
			name:    "recent errors, all located",
			samples: []*model.CounterSample{withRecent(daemonSample(100*time.Hour, 10, 0), 3)},
			events:  located(3, 90*time.Hour),
			// The three located errors are the three recent ones, not three
			// of the seven before them as well.
			wantCE: 10,
		},
		{
			name:    "recent errors, older ones located",
			samples: []*model.CounterSample{withRecent(daemonSample(100*time.Hour, 10, 0), 3)},
			events:  located(3, 20*time.Hour),
			wantCE:  10,
		},
		{
			name:    "recent errors with both counters",
			samples: []*model.CounterSample{edac(100*time.Hour, 10, 0), withRecent(daemonSample(100*time.Hour, 10, 0), 3)},
			events:  located(3, 90*time.Hour),
			wantCE:  10,
		},
		{
			name: "the kernel was read later than the daemon",
			samples: []*model.CounterSample{
				daemonSample(60*time.Hour, 10, 0), edac(100*time.Hour, 10, 0),
			},
			wantCE: 10,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			mustUpdate(t, s, func(w *Writer) error {
				_, err := w.UpsertBoot(&model.Boot{Host: "h1", BootID: "b1", StartedAt: at(0), StartQuality: model.TimeExact, Source: "hostinfo"})
				return err
			})
			addSamples(t, s, tc.samples...)
			insert(t, s, tc.events...)
			reconcile(t, s)
			if ce, ue := slotTotals(t, s, "CPU0_C1"); ce != tc.wantCE || ue != tc.wantUE {
				list, _ := s.EventList(ctx, EventFilter{Kinds: []model.EventKind{model.KindCounter}})
				for _, e := range list {
					t.Logf("%s count %d covered %d window %s to %s", e.Source, e.Count, e.Covered, e.WindowStart.Sub(t0), e.ErrorDT.Sub(t0))
				}
				t.Errorf("slot totals: CE %d UE %d, want %d and %d", ce, ue, tc.wantCE, tc.wantUE)
			}
		})
	}
}

// Two rank counters of one DIMM share the located records of their slot.
func TestReconcileRankCounters(t *testing.T) {
	s := newStore(t)
	rank := func(idx int, ce uint64) *model.CounterSample {
		return &model.CounterSample{ObservedAt: at(100 * time.Hour), MC: 0, DIMMIndex: idx, Location: fmt.Sprintf("csrow:%d channel:0", idx), Label: "CPU0_C1",
			CE: ce, SecondsSinceReset: model.Some(int64(360000)), Source: "edac-sysfs", RawRef: fmt.Sprintf("mc0/rank%d", idx)}
	}
	addSamples(t, s, rank(0, 6), rank(1, 4))
	var events []*model.Event
	for i := range 6 {
		events = append(events, ev("rasdaemon/mc_event", fmt.Sprintf("mc/%d", i), at(50*time.Hour+time.Duration(i)*time.Minute), uint64(0x10000+i*64)))
	}
	insert(t, s, events...)
	reconcile(t, s)
	// Ten errors in the counters, six of them located: ten in all.
	if ce, _ := slotTotals(t, s, "CPU0_C1"); ce != 10 {
		t.Errorf("slot total = %d, want 10", ce)
	}
}

// Errors a source could not place are counted against the located records
// that name no slot: by socket for a source that counts per socket.
func TestReconcileUnplacedCounters(t *testing.T) {
	s := newStore(t)
	sock := &model.CounterSample{ObservedAt: at(100 * time.Hour), MC: 1, DIMMIndex: -1, SocketId: model.Some(1), CE: 5, Source: "mcelog", RawRef: "client#socket1"}
	addSamples(t, s, sock)
	var events []*model.Event
	for i, socket := range []int{1, 1, 0} {
		e := ev("klog/mce", fmt.Sprintf("m/%d", i), at(50*time.Hour+time.Duration(i)*time.Minute), uint64(0x10000+i*64))
		e.Label, e.Slot, e.SocketId = "", "", model.Some(socket)
		events = append(events, e)
	}
	insert(t, s, events...)
	reconcile(t, s)
	list, _ := s.EventList(ctx, EventFilter{Kinds: []model.EventKind{model.KindCounter}})
	if len(list) != 1 || list[0].Covered != 2 || list[0].Count != 5 || !list[0].SocketId.Valid || list[0].EdacMC.Valid {
		t.Errorf("counter event = %+v; want 5 errors of socket 1, 2 of them located", list)
	}
}

// TestMigrateVersion3 opens a ledger written with schema version 3 and
// checks that its counter readings are kept and that a second source can
// be added beside them.
func TestMigrateVersion3(t *testing.T) {
	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "v3.db")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		migs[0].sql, migs[1].sql, migs[2].sql,
		`PRAGMA application_id = 1297697863`,
		`PRAGMA user_version = 3`,
		`INSERT INTO host (name, first_seen, last_seen) VALUES ('h1', '2026-11-01T00:00:00.000000Z', '2026-11-01T00:00:00.000000Z')`,
		`INSERT INTO counter_sample (host, observed_at, mc, mc_name, dimm_index, location, label, ce, ue, reset_at, size_mb, mem_type, dev_type, edac_mode, source, raw_ref)
		 VALUES ('h1', '2026-11-01T10:00:00.000000Z', 0, 'skx_edac', 2, 'channel:1 slot:0', 'CPU0_C1', 50, 1, '2026-11-01T00:00:00.000000Z', 65536, 'Registered-DDR4', 'x4', 'SECDED', 'edac-sysfs', 'mc0/dimm2'),
		        ('h1', '2026-11-01T10:00:00.000000Z', 0, 'skx_edac', 3, 'channel:1 slot:1', 'CPU0_C2', 0, 0, '2026-11-01T00:00:00.000000Z', 65536, 'Registered-DDR4', 'x4', 'SECDED', 'edac-sysfs', 'mc0/dimm3')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("build version 3 ledger: %v\n%s", err, q)
		}
	}
	db.Close()

	if _, err := Open(ctx, p, ReadOnly); !errors.Is(err, ErrNeedsMigration) {
		t.Fatalf("read-only open of a version 3 ledger: err = %v, want ErrNeedsMigration", err)
	}
	s, err := Open(ctx, p, ReadWrite)
	if err != nil {
		t.Fatalf("open for writing: %v", err)
	}
	defer s.Close()
	if v, _ := s.Version(ctx); v != SchemaVersion() || v < 4 {
		t.Fatalf("version after migration = %d", v)
	}
	latest, err := s.LatestCounterSamples(ctx, "h1")
	if err != nil || len(latest) != 2 {
		t.Fatalf("readings after migration = %+v, %v", latest, err)
	}
	want := model.CounterSample{ObservedAt: time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC), MC: 0, MCName: "skx_edac", DIMMIndex: 2,
		Location: "channel:1 slot:0", Label: "CPU0_C1", CE: 50, UE: 1, SizeMB: model.Some(65536), MemType: "Registered-DDR4", DevType: "x4", EdacMode: "SECDED",
		Source: "edac-sysfs", RawRef: "mc0/dimm2"}
	if !reflect.DeepEqual(latest[0], want) {
		t.Errorf("reading after migration\n got: %+v\nwant: %+v", latest[0], want)
	}

	// The same reading again is known; the same numbers from another
	// source are a reading of their own.
	var again, other bool
	mustUpdate(t, s, func(w *Writer) (err error) {
		c := want
		c.SecondsSinceReset = model.Some(int64(36000))
		if again, err = w.InsertCounterSample("h1", &c); err != nil {
			return err
		}
		d := daemonSample(10*time.Hour, 40, 0)
		d.DIMMIndex = 2
		other, err = w.InsertCounterSample("h1", d)
		return err
	})
	if again || !other {
		t.Errorf("inserted: the same reading %v, the reading of another source %v; want false, true", again, other)
	}
	if err := s.DeriveCounterEvents(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	got := counterEvents(t, s)
	wantEvents := []counterEvent{
		{"edac-sysfs/counter", 10 * time.Hour, 0, model.SevCE, 50},
		{"edac-sysfs/counter", 10 * time.Hour, 0, model.SevUE, 1},
		{"mcelog/counter", 10 * time.Hour, -1, model.SevCE, 40},
	}
	if !reflect.DeepEqual(got, wantEvents) {
		t.Errorf("counter events after migration\n got: %+v\nwant: %+v", got, wantEvents)
	}
}

// A recent count without a window, and a count that does not fit, are not
// stored as such.
func TestCounterSampleBounds(t *testing.T) {
	s := newStore(t)
	c := daemonSample(10*time.Hour, 5, 0)
	c.RecentCE = model.Some(uint64(3)) // no window
	addSamples(t, s, c)
	if got := counterEvents(t, s); len(got) != 1 || got[0].Count != 5 {
		t.Errorf("a recent count without a window split the reading: %+v", got)
	}
	big := daemonSample(11*time.Hour, 1<<63, 0)
	err := s.Update(ctx, func(w *Writer) error {
		_, err := w.InsertCounterSample("h1", big)
		return err
	})
	if !errors.Is(err, ErrInvalidRecord) {
		t.Errorf("a count beyond the range: err = %v, want ErrInvalidRecord", err)
	}
}

// The time the counters started is the time of the reading less the
// seconds they have been running. A number of seconds that is no running
// time (it came from a file of a copied host tree) overflowed as a
// duration and gave a reset time that was never the case: such a reading
// has no reset time.
func TestRunningTimeThatIsNone(t *testing.T) {
	s := newStore(t)
	sample := func(index int, since int64) *model.CounterSample {
		return &model.CounterSample{ObservedAt: at(10 * time.Hour), MC: 0, DIMMIndex: index, CE: 1,
			SecondsSinceReset: model.Some(since), Source: "edac-sysfs", RawRef: fmt.Sprintf("mc0/dimm%d", index)}
	}
	mustUpdate(t, s, func(w *Writer) error {
		for i, since := range []int64{36000, 100 * 365 * 24 * 3600, 100*365*24*3600 + 1, 1 << 62, -5} {
			if _, err := w.InsertCounterSample("h1", sample(i, since)); err != nil {
				return err
			}
		}
		return nil
	})
	rows, err := s.db.QueryContext(ctx, `SELECT dimm_index, reset_at FROM counter_sample ORDER BY dimm_index`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var index int
		var reset sql.NullString
		if err := rows.Scan(&index, &reset); err != nil {
			t.Fatal(err)
		}
		got = append(got, reset.String)
	}
	century := at(10 * time.Hour).Add(-100 * 365 * 24 * time.Hour).UTC().Format(timeLayout)
	want := []string{at(0).UTC().Format(timeLayout), century, "", "", ""}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reset times %q, want %q", got, want)
	}
}
