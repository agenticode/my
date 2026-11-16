package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
)

// TestMigrateVersion6 opens a ledger of schema 6 with a boot and an event
// and checks that they are there afterwards, with the new columns empty.
func TestMigrateVersion6(t *testing.T) {
	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "v6.db")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migs[:6] {
		if _, err := db.Exec(m.sql); err != nil {
			t.Fatalf("migration %d: %v", m.version, err)
		}
	}
	for _, q := range []string{
		`PRAGMA application_id = 1297697863`,
		`PRAGMA user_version = 6`,
		`INSERT INTO meta (key, value) VALUES ('ledger_id', '0123456789abcdef0123456789abcdef')`,
		`INSERT INTO boot (host, boot_id, started_at, last_seen) VALUES ('h1', 'b1', '2026-11-01T00:00:00.000000Z', '2026-11-01T05:00:00.000000Z')`,
		`INSERT INTO event (host, "ErrorDT", "ErrorSeverity", source, raw_ref, grade, boot_id, fingerprint) VALUES ('h1', '2026-11-01T01:00:00.000000Z', 'UE', 'klog/mce', 'L1', 'B', 'b1', 'e1')`,
		`INSERT INTO scan (host, kind, started_at) VALUES ('h1', 'live', '2026-11-01T05:00:00.000000Z')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("build version 6 ledger: %v\n%s", err, q)
		}
	}
	db.Close()
	s, err := Open(ctx, p, ReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v, _ := s.Version(ctx); v != SchemaVersion() || v < 7 {
		t.Fatalf("version after migration = %d", v)
	}
	ev, err := s.EventList(ctx, EventFilter{})
	if err != nil || len(ev) != 1 || ev[0].Carried || ev[0].ReadBootID != "" || !ev[0].NotBefore.IsZero() || ev[0].BootID != "b1" {
		t.Fatalf("events after migration: %+v, %v", ev, err)
	}
	boots, err := s.Boots(ctx, "h1")
	if err != nil || len(boots) != 1 || boots[0].End != "" || boots[0].EndRef != "" || !boots[0].LastSeen.Equal(time.Date(2026, 11, 1, 5, 0, 0, 0, time.UTC)) {
		t.Fatalf("boots after migration: %+v, %v", boots, err)
	}
	if checks, err := s.CaptureChecks(ctx, "h1"); err != nil || checks != nil {
		t.Errorf("capture checks of a migrated ledger: %+v, %v", checks, err)
	}
}

// Times of the boots of the tests below: A from t0 to an hour later, B two
// hours after t0.
var (
	bootA = model.Boot{Host: "h1", BootID: "a", StartedAt: at(0), LastSeen: at(time.Hour), StartQuality: model.TimeExact}
	bootB = model.Boot{Host: "h1", BootID: "b", StartedAt: at(2 * time.Hour), LastSeen: at(3 * time.Hour), StartQuality: model.TimeExact}
	bootC = model.Boot{Host: "h1", BootID: "c", StartedAt: at(4 * time.Hour), LastSeen: at(5 * time.Hour), StartQuality: model.TimeExact}
)

func addBoots(t *testing.T, s *Store, boots ...model.Boot) {
	t.Helper()
	mustUpdate(t, s, func(w *Writer) error {
		for _, b := range boots {
			b := b
			if _, err := w.UpsertBoot(&b); err != nil {
				return err
			}
		}
		return nil
	})
}

// ev is an event of host h1 with the given fingerprint, time and source.
func cev(fp string, dt time.Time, sev model.Severity, source string) *model.Event {
	return &model.Event{Host: "h1", Kind: model.KindError, ErrorDT: dt, ErrorSeverity: sev, Source: source, RawRef: fp, Fingerprint: fp}
}

func addEvents(t *testing.T, s *Store, evs ...*model.Event) {
	t.Helper()
	mustUpdate(t, s, func(w *Writer) error {
		for _, e := range evs {
			if _, err := w.InsertEvent(e); err != nil {
				return err
			}
		}
		return nil
	})
}

func eventByFP(t *testing.T, s *Store, fp string) *model.Event {
	t.Helper()
	evs, err := s.EventList(ctx, EventFilter{WithDuplicates: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Fingerprint == fp {
			return e
		}
	}
	t.Fatalf("no event %s", fp)
	return nil
}

// link runs the passes that place carried records, as ingest runs them.
func link(t *testing.T, s *Store) {
	t.Helper()
	if err := s.LinkBoots(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	if err := s.LinkCarried(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
}

// TestLinkCarried: a record the next boot read belongs to the boot
// before, with the time bound of that boot; the pass changes nothing when
// it runs again.
func TestLinkCarried(t *testing.T) {
	s := newStore(t)
	addBoots(t, s, bootA, bootB)
	c := cev("carried", at(2*time.Hour+30*time.Second), model.SevFatal, "klog/mce")
	c.Carried, c.TimeQuality, c.OsPhysicalAddress = true, model.TimeEstimated, model.Some(uint64(0x12345040))
	addEvents(t, s, c)
	for i := range 3 {
		link(t, s)
		got := eventByFP(t, s, "carried")
		if got.BootID != "a" || got.ReadBootID != "b" || !got.ErrorDT.Equal(at(time.Hour)) || !got.NotBefore.Equal(at(0)) || got.TimeQuality != model.TimeEstimated {
			t.Fatalf("run %d: %+v", i, got)
		}
	}
	boots, _ := s.Boots(ctx, "h1")
	if len(boots) != 2 {
		t.Errorf("boots %+v", boots)
	}
}

// TestLinkCarriedWithoutAnEarlierBoot: the pass puts a boot of unknown
// start before the reading boot, keeps one when it runs again, and moves
// the record to the real boot before once that is on record.
func TestLinkCarriedWithoutAnEarlierBoot(t *testing.T) {
	s := newStore(t)
	addBoots(t, s, bootB)
	c := cev("carried", at(2*time.Hour+30*time.Second), model.SevFatal, "klog/mce")
	c.Carried = true
	addEvents(t, s, c)
	var placeholder string
	for i := range 2 {
		link(t, s)
		got := eventByFP(t, s, "carried")
		boots, _ := s.Boots(ctx, "h1")
		if len(boots) != 2 || boots[0].StartQuality != QualityUnknown || got.BootID != boots[0].BootID || got.ReadBootID != "b" ||
			!got.ErrorDT.Equal(at(2*time.Hour-time.Second)) || !got.NotBefore.IsZero() {
			t.Fatalf("run %d: event %+v, boots %+v", i, got, boots)
		}
		if placeholder != "" && placeholder != boots[0].BootID {
			t.Fatalf("the boot before changed its ID: %s, then %s", placeholder, boots[0].BootID)
		}
		placeholder = boots[0].BootID
	}
	// A record of exact time just before the reading boot is not given to
	// the boot of unknown start.
	addEvents(t, s, cev("near", at(2*time.Hour-500*time.Millisecond), model.SevCE, "klog/edac"))
	link(t, s)
	if got := eventByFP(t, s, "near"); got.BootID != "" {
		t.Errorf("a record of exact time went to the boot of unknown start: %+v", got)
	}
	// The journal of the boot before is read later.
	addBoots(t, s, bootA)
	link(t, s)
	got := eventByFP(t, s, "carried")
	boots, _ := s.Boots(ctx, "h1")
	if got.BootID != "a" || len(boots) != 2 || boots[0].BootID != "a" {
		t.Errorf("after the boot before was read: event %+v, boots %+v", got, boots)
	}
}

// TestCarriedDuplicates: the pairs of section "One error through several
// survivors" of the design, case by case.
func TestCarriedDuplicates(t *testing.T) {
	addr := model.Some(uint64(0x12345040))
	other := model.Some(uint64(0x7700000))
	carried := func(fp string, sev model.Severity, src string, g model.Grade, a model.Opt[uint64], slot string) *model.Event {
		e := cev(fp, at(2*time.Hour+30*time.Second), sev, src)
		e.Carried, e.Grade, e.OsPhysicalAddress, e.Slot = true, g, a, slot
		return e
	}
	exact := func(fp string, dt time.Duration, sev model.Severity, src string, g model.Grade, a model.Opt[uint64], slot string) *model.Event {
		e := cev(fp, at(dt), sev, src)
		e.Grade, e.OsPhysicalAddress, e.Slot = g, a, slot
		return e
	}
	for _, tt := range []struct {
		name string
		evs  []*model.Event
		// dups maps a fingerprint to the fingerprint of the record it
		// duplicates; every other record stays counted.
		dups map[string]string
	}{
		{"panic text and the bank record of the next boot: the exact one is counted",
			[]*model.Event{exact("text", 50*time.Minute, model.SevFatal, "klog/mce", model.GradeB, addr, ""), carried("bank", model.SevFatal, "klog/mce", model.GradeB, addr, "")},
			map[string]string{"bank": "text"}},
		{"another cache line is another error",
			[]*model.Event{exact("text", 50*time.Minute, model.SevFatal, "klog/mce", model.GradeB, other, ""), carried("bank", model.SevFatal, "klog/mce", model.GradeB, addr, "")},
			nil},
		{"another severity class is another error",
			[]*model.Event{exact("text", 50*time.Minute, model.SevCE, "klog/mce", model.GradeB, addr, ""), carried("bank", model.SevFatal, "klog/mce", model.GradeB, addr, "")},
			nil},
		{"another boot is another error",
			[]*model.Event{exact("text", 2*time.Hour+10*time.Minute, model.SevFatal, "klog/mce", model.GradeB, addr, ""), carried("bank", model.SevFatal, "klog/mce", model.GradeB, addr, "")},
			nil},
		{"no address on either side, and one uncorrected record in the boot",
			[]*model.Event{exact("text", 50*time.Minute, model.SevFatal, "klog/ghes", model.GradeC, model.Opt[uint64]{}, ""), carried("bank", model.SevFatal, "klog/mce", model.GradeC, model.Opt[uint64]{}, "")},
			map[string]string{"bank": "text"}},
		{"no address on either side, and two uncorrected records in the boot",
			[]*model.Event{exact("t1", 40*time.Minute, model.SevFatal, "klog/ghes", model.GradeC, model.Opt[uint64]{}, ""),
				exact("t2", 50*time.Minute, model.SevUE, "rasdaemon/mce_record", model.GradeC, model.Opt[uint64]{}, ""),
				carried("bank", model.SevFatal, "klog/mce", model.GradeC, model.Opt[uint64]{}, "")},
			nil},
		{"a boot error record and the machine check of the boot before: the best grade",
			[]*model.Event{exact("mce", 50*time.Minute, model.SevFatal, "klog/mce", model.GradeB, addr, ""), carried("bert", model.SevFatal, "bert", model.GradeA, addr, "")},
			map[string]string{"mce": "bert"}},
		{"same slot when neither has an address",
			[]*model.Event{exact("ghes", 50*time.Minute, model.SevUE, "klog/ghes", model.GradeC, model.Opt[uint64]{}, "CPU0_C1"), carried("bert", model.SevUE, "bert", model.GradeC, model.Opt[uint64]{}, "CPU0_C1")},
			map[string]string{"bert": "ghes"}},
		{"the region and the lines the kernel printed of it: the region",
			[]*model.Event{carried("lines", model.SevFatal, "klog/bert", model.GradeA, addr, ""), carried("region", model.SevFatal, "bert", model.GradeA, addr, "")},
			map[string]string{"lines": "region"}},
		{"a record of a BMC log anywhere in the boot",
			[]*model.Event{exact("sel", 5*time.Minute, model.SevUE, "sel/memory", model.GradeC, model.Opt[uint64]{}, "CPU0_C1"), carried("bank", model.SevUE, "klog/mce", model.GradeB, addr, "CPU0_C1")},
			map[string]string{"sel": "bank"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t)
			addBoots(t, s, bootA, bootB)
			addEvents(t, s, tt.evs...)
			link(t, s)
			for range 2 {
				if _, err := s.MarkDuplicates(ctx, "h1", DuplicateTolerance{}); err != nil {
					t.Fatal(err)
				}
				for _, e := range tt.evs {
					got := eventByFP(t, s, e.Fingerprint)
					want := int64(0)
					if p, ok := tt.dups[e.Fingerprint]; ok {
						want = eventByFP(t, s, p).ID
					}
					if got.DupOf != want {
						t.Errorf("%s: dup_of %d, want %d", e.Fingerprint, got.DupOf, want)
					}
				}
			}
		})
	}
}

// TestBootOutcomes: how a boot ended, per evidence, and the same again
// when the pass runs again.
func TestBootOutcomes(t *testing.T) {
	s := newStore(t)
	addBoots(t, s, bootA, bootB, bootC, model.Boot{Host: "h1", BootID: "d", StartedAt: at(6 * time.Hour), StartQuality: model.TimeExact})
	// A: an uncorrected record left in a bank, read in B.
	bank := cev("bank", at(2*time.Hour+30*time.Second), model.SevFatal, "klog/mce")
	bank.Carried = true
	// B: a pstore dump of a panic without a hardware error.
	panicB := cev("panic-b", at(2*time.Hour+50*time.Minute), model.SevInformational, "pstore/panic")
	panicB.ErrorType = model.ErrorTypePanic
	// C: a corrected record carried over (no stop), and a fatal block of
	// the boot error region without a memory error, read in D.
	ce := cev("ce", at(6*time.Hour+time.Minute), model.SevCE, "bert")
	ce.Carried = true
	block := cev("block", at(6*time.Hour+time.Minute), model.SevInformational, "bert")
	block.Carried, block.ErrorType = true, model.ErrorTypeFatalBlock
	addEvents(t, s, bank, panicB)
	run := func() {
		link(t, s)
		if _, err := s.MarkDuplicates(ctx, "h1", DuplicateTolerance{}); err != nil {
			t.Fatal(err)
		}
		if err := s.BootOutcomes(ctx, "h1"); err != nil {
			t.Fatal(err)
		}
	}
	ends := func() map[string]model.Boot {
		boots, _ := s.Boots(ctx, "h1")
		out := map[string]model.Boot{}
		for _, b := range boots {
			out[b.BootID] = b
		}
		return out
	}
	run()
	got := ends()
	if b := got["a"]; b.End != model.BootStop || b.EndRef != "event:"+itoa(eventByFP(t, s, "bank").ID) || b.EndSource != "klog/mce" {
		t.Errorf("A: %+v", b)
	}
	if b := got["b"]; b.End != model.BootPanic || b.EndSource != "pstore/panic" || !b.EndAt.Equal(at(2*time.Hour+50*time.Minute)) {
		t.Errorf("B: %+v", b)
	}
	if got["c"].End != "" || got["d"].End != "" {
		t.Errorf("C, D: %+v %+v", got["c"], got["d"])
	}
	addEvents(t, s, ce)
	run()
	if got := ends(); got["c"].End != "" {
		t.Errorf("a carried corrected record stops C: %+v", got["c"])
	}
	addEvents(t, s, block)
	run()
	first := ends()
	if b := first["c"]; b.End != model.BootStop || b.EndSource != "bert" {
		t.Errorf("C after the fatal block: %+v", b)
	}
	run()
	if again := ends(); !sameEnds(first, again) {
		t.Errorf("a second run changed the outcomes:\n%+v\n%+v", first, again)
	}
	// A stop outranks a panic of the same boot.
	stopB := cev("bank-b", at(4*time.Hour+30*time.Second), model.SevUE, "klog/mce")
	stopB.Carried = true
	addEvents(t, s, stopB)
	run()
	if b := ends()["b"]; b.End != model.BootStop {
		t.Errorf("B with a stop and a panic: %+v", b)
	}
}

func sameEnds(a, b map[string]model.Boot) bool {
	if len(a) != len(b) {
		return false
	}
	for k, x := range a {
		y := b[k]
		if x.End != y.End || x.EndRef != y.EndRef || x.EndSource != y.EndSource || !x.EndAt.Equal(y.EndAt) {
			return false
		}
	}
	return true
}

func itoa(n int64) string {
	b := []byte{}
	if n == 0 {
		return "0"
	}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestCaptureChecks: the checks of a scan are stored and the latest scan's
// are read back.
func TestCaptureChecks(t *testing.T) {
	s := newStore(t)
	var first, second model.Scan
	mustUpdate(t, s, func(w *Writer) error {
		first = model.Scan{Host: "h1", Kind: model.ScanLive, StartedAt: at(0)}
		second = model.Scan{Host: "h1", Kind: model.ScanLive, StartedAt: at(time.Hour)}
		if err := w.InsertScan(&first); err != nil {
			return err
		}
		if err := w.InsertScan(&second); err != nil {
			return err
		}
		if err := w.PutCaptureChecks(first.ID, "h1", []model.CaptureCheck{{Name: "journal", State: "lost"}}); err != nil {
			return err
		}
		return w.PutCaptureChecks(second.ID, "h1", []model.CaptureCheck{
			{Name: "journal", State: "kept", Detail: model.Statement{Code: "pf.journal.disk", Args: map[string]any{"interval": "5min"}, Text: "on disk"}, Evidence: []string{"/var/log/journal: a directory"}},
			{Name: "pstore-backend", State: "lost"},
		})
	})
	got, err := s.CaptureChecks(ctx, "h1")
	if err != nil || len(got) != 2 || got[0].Name != "journal" || got[0].State != "kept" || got[0].Detail.Code != "pf.journal.disk" ||
		got[0].Detail.Args["interval"] != "5min" || len(got[0].Evidence) != 1 || got[1].Name != "pstore-backend" || got[0].ScanID != second.ID {
		t.Errorf("checks %+v, %v", got, err)
	}
	err = s.Update(ctx, func(w *Writer) error {
		return w.PutCaptureChecks(second.ID, "h1", []model.CaptureCheck{{Name: "x", State: "maybe"}})
	})
	if err == nil {
		t.Error("a check of unknown state was stored")
	}
}
