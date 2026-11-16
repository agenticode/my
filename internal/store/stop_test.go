package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
)

func stopEvent(fp string, dt time.Time, cause string) *model.Event {
	e := cev(fp, dt, model.SevInformational, "klog/stop")
	e.ErrorType, e.CauseFingerprint = model.ErrorTypeStop, cause
	return e
}

// TestKernelStops: the kernel's lines of a stop end their boot. The record
// that shows it is the error record before the lines when there is one,
// the counted one when that record is a second report of another.
func TestKernelStops(t *testing.T) {
	s := newStore(t)
	addBoots(t, s, bootA, bootB, bootC, model.Boot{Host: "h1", BootID: "d", StartedAt: at(6 * time.Hour), StartQuality: model.TimeExact})
	// A: a fatal machine check and the panic after it.
	mce := cev("mce", at(30*time.Minute), model.SevFatal, "klog/mce")
	stopA := stopEvent("stop-a", at(30*time.Minute+9*time.Millisecond), "mce")
	// B: a panic on a machine check that no record shows, and another
	// panic of the same boot read from a crash log.
	stopB := stopEvent("stop-b", at(2*time.Hour+10*time.Minute), "")
	panicB := cev("panic-b", at(2*time.Hour+9*time.Minute), model.SevInformational, "pstore/panic")
	panicB.ErrorType = model.ErrorTypePanic
	// C: a firmware error record, the EDAC line of the same error, and the
	// panic tied to the EDAC line. A cause that is not in the ledger.
	ghes := cev("ghes", at(4*time.Hour+time.Minute), model.SevFatal, "klog/ghes")
	edac := cev("edac", at(4*time.Hour+time.Minute), model.SevUE, "klog/edac")
	edac.DupOfFingerprint = "ghes"
	stopC := stopEvent("stop-c", at(4*time.Hour+time.Minute+time.Millisecond), "edac")
	stopD := stopEvent("stop-d", at(6*time.Hour+time.Minute), "lost")
	addEvents(t, s, mce, stopA, stopB, panicB, ghes, edac, stopC, stopD)
	run := func() {
		link(t, s)
		if _, err := s.MarkDuplicates(ctx, "h1", DuplicateTolerance{}); err != nil {
			t.Fatal(err)
		}
		if err := s.BootOutcomes(ctx, "h1"); err != nil {
			t.Fatal(err)
		}
	}
	run()
	boots, err := s.Boots(ctx, "h1")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]model.Boot{}
	for _, b := range boots {
		got[b.BootID] = b
	}
	id := func(fp string) int64 { return eventByFP(t, s, fp).ID }
	want := map[string]struct {
		ref string
		at  time.Time
	}{
		"a": {"event:" + itoa(id("mce")), at(30*time.Minute + 9*time.Millisecond)},
		"b": {"event:" + itoa(id("stop-b")), at(2*time.Hour + 10*time.Minute)},
		"c": {"event:" + itoa(id("edac")), at(4*time.Hour + time.Minute + time.Millisecond)},
		"d": {"event:" + itoa(id("stop-d")), at(6*time.Hour + time.Minute)},
	}
	for boot, w := range want {
		b := got[boot]
		if b.End != model.BootStop || b.EndRef != w.ref || b.EndSource != "klog/stop" || !b.EndAt.Equal(w.at) {
			t.Errorf("%s: end %q ref %q (want %q) source %q at %v", boot, b.End, b.EndRef, w.ref, b.EndSource, b.EndAt)
		}
	}
	first := got
	run()
	boots, _ = s.Boots(ctx, "h1")
	again := map[string]model.Boot{}
	for _, b := range boots {
		again[b.BootID] = b
	}
	if !sameEnds(first, again) {
		t.Errorf("a second run changed the outcomes")
	}

	stops, err := s.Stops(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		boot          string
		event, stopID int64
	}
	wantStops := []row{{"a", id("mce"), id("stop-a")}, {"b", 0, id("stop-b")}, {"c", id("ghes"), id("stop-c")}, {"d", 0, id("stop-d")}}
	if len(stops) != len(wantStops) {
		t.Fatalf("stops %+v", stops)
	}
	for i, w := range wantStops {
		st := stops[i]
		if st.Host != "h1" || st.BootID != w.boot || st.EventID != w.event || st.StopID != w.stopID || st.StopRef != eventByFP(t, s, map[string]string{"a": "stop-a", "b": "stop-b", "c": "stop-c", "d": "stop-d"}[w.boot]).RawRef {
			t.Errorf("stop %d: %+v, want %+v", i, st, w)
		}
	}
	if one, err := s.Stops(ctx, "h2"); err != nil || len(one) != 0 {
		t.Errorf("stops of another host: %+v, %v", one, err)
	}
}

// TestMigrateVersion7: a ledger of schema 7 gets the column of the cause
// of a stop, empty for the records it holds.
func TestMigrateVersion7(t *testing.T) {
	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "v7.db")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migs[:7] {
		if _, err := db.Exec(m.sql); err != nil {
			t.Fatalf("migration %d: %v", m.version, err)
		}
	}
	for _, q := range []string{
		`PRAGMA application_id = 1297697863`,
		`PRAGMA user_version = 7`,
		`INSERT INTO meta (key, value) VALUES ('ledger_id', '0123456789abcdef0123456789abcdef')`,
		`INSERT INTO event (host, "ErrorDT", "ErrorSeverity", source, raw_ref, grade, fingerprint) VALUES ('h1', '2026-11-01T01:00:00.000000Z', 'UE', 'klog/mce', 'L1', 'B', 'e1')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("build version 7 ledger: %v\n%s", err, q)
		}
	}
	db.Close()
	s, err := Open(ctx, p, ReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v, _ := s.Version(ctx); v != SchemaVersion() || v < 8 {
		t.Fatalf("version after migration = %d", v)
	}
	ev, err := s.EventList(ctx, EventFilter{})
	if err != nil || len(ev) != 1 || ev[0].CauseFingerprint != "" {
		t.Fatalf("events after migration: %+v, %v", ev, err)
	}
}
