package store

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
)

var ctx = context.Background()

// t0 is the reference time of the tests.
var t0 = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

func at(d time.Duration) time.Time { return t0.Add(d) }

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), Create)
	if err != nil {
		t.Fatalf("create ledger: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func mustUpdate(t *testing.T, s *Store, fn func(w *Writer) error) {
	t.Helper()
	if err := s.Update(ctx, fn); err != nil {
		t.Fatalf("update: %v", err)
	}
}

// ev builds a located corrected error for tests.
func ev(source, ref string, when time.Time, addr uint64) *model.Event {
	return &model.Event{
		Host: "h1", ErrorDT: when, ErrorSeverity: model.SevCE, Source: source, RawRef: ref,
		OsPhysicalAddress: model.Some(addr), Label: "CPU0_C1", Slot: "CPU0_C1",
		Fingerprint: model.Fingerprint(source, ref),
	}
}

func insert(t *testing.T, s *Store, events ...*model.Event) {
	t.Helper()
	mustUpdate(t, s, func(w *Writer) error {
		for _, e := range events {
			if _, err := w.InsertEvent(e); err != nil {
				return err
			}
		}
		return nil
	})
}

func TestCreateAndReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case with %20 ? and #.db")
	s, err := Open(ctx, path, Create)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	v, err := s.Version(ctx)
	if err != nil || v != SchemaVersion() || v < 1 {
		t.Fatalf("version = %d, %v; want %d", v, err, SchemaVersion())
	}
	id, err := s.Meta(ctx, "ledger_id")
	if err != nil || len(id) != 32 {
		t.Fatalf("ledger_id = %q, %v", id, err)
	}
	insert(t, s, ev("rasdaemon/mc_event", "mc_event/1", at(0), 0x1000))
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// A closed ledger is one plain file: no WAL sidecars.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("directory holds %v, want only the ledger file", names)
	}

	// It opens read-only from a directory that cannot be written.
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	ro, err := Open(ctx, path, ReadOnly)
	if err != nil {
		t.Fatalf("open read-only: %v", err)
	}
	defer ro.Close()
	events, err := ro.EventList(ctx, EventFilter{})
	if err != nil || len(events) != 1 {
		t.Fatalf("read-only events = %d, %v", len(events), err)
	}
	if got, _ := ro.Meta(ctx, "ledger_id"); got != id {
		t.Errorf("ledger_id changed across reopen: %q vs %q", got, id)
	}
	if err := ro.Update(ctx, func(*Writer) error { return nil }); !errors.Is(err, ErrReadOnly) {
		t.Errorf("update on read-only ledger: err = %v, want ErrReadOnly", err)
	}
	if err := ro.LinkBoots(ctx, "h1"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("link on read-only ledger: err = %v, want ErrReadOnly", err)
	}
}

func TestOpenErrors(t *testing.T) {
	dir := t.TempDir()

	t.Run("missing file", func(t *testing.T) {
		for _, m := range []Mode{ReadOnly, ReadWrite} {
			if _, err := Open(ctx, filepath.Join(dir, "absent.db"), m); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("mode %d: err = %v, want not exist", m, err)
			}
		}
	})

	t.Run("directory", func(t *testing.T) {
		if _, err := Open(ctx, dir, Create); err == nil || !strings.Contains(err.Error(), "is a directory") {
			t.Errorf("err = %v, want a directory error", err)
		}
	})

	t.Run("not a database", func(t *testing.T) {
		p := filepath.Join(dir, "text.db")
		if err := os.WriteFile(p, []byte(strings.Repeat("EDAC MC0: 1 CE memory read error\n", 200)), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, m := range []Mode{ReadOnly, ReadWrite, Create} {
			if _, err := Open(ctx, p, m); !errors.Is(err, ErrNotLedger) {
				t.Errorf("mode %d: err = %v, want ErrNotLedger", m, err)
			}
		}
	})

	t.Run("another SQLite database", func(t *testing.T) {
		p := filepath.Join(dir, "ras-mc_event.db")
		db, err := sql.Open("sqlite", p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`CREATE TABLE mc_event (id INTEGER PRIMARY KEY, timestamp TEXT)`); err != nil {
			t.Fatal(err)
		}
		db.Close()
		if _, err := Open(ctx, p, ReadWrite); !errors.Is(err, ErrNotLedger) {
			t.Errorf("err = %v, want ErrNotLedger", err)
		}
		// The foreign database must be left untouched.
		db, _ = sql.Open("sqlite", p)
		defer db.Close()
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM sqlite_master`).Scan(&n); err != nil || n != 1 {
			t.Errorf("foreign database changed: %d objects, %v", n, err)
		}
	})

	t.Run("newer schema", func(t *testing.T) {
		p := filepath.Join(dir, "newer.db")
		s, err := Open(ctx, p, Create)
		if err != nil {
			t.Fatal(err)
		}
		s.Close()
		db, _ := sql.Open("sqlite", p)
		if _, err := db.Exec(`PRAGMA user_version = 999`); err != nil {
			t.Fatal(err)
		}
		db.Close()
		for _, m := range []Mode{ReadOnly, ReadWrite} {
			if _, err := Open(ctx, p, m); !errors.Is(err, ErrNewerSchema) {
				t.Errorf("mode %d: err = %v, want ErrNewerSchema", m, err)
			}
		}
	})

	t.Run("older schema", func(t *testing.T) {
		p := filepath.Join(dir, "older.db")
		db, _ := sql.Open("sqlite", p)
		if _, err := db.Exec(`PRAGMA application_id = 1297697863`); err != nil { // "MYLG"
			t.Fatal(err)
		}
		db.Close()
		if _, err := Open(ctx, p, ReadOnly); !errors.Is(err, ErrNeedsMigration) {
			t.Fatalf("read-only: err = %v, want ErrNeedsMigration", err)
		}
		s, err := Open(ctx, p, ReadWrite)
		if err != nil {
			t.Fatalf("read-write should migrate: %v", err)
		}
		defer s.Close()
		if v, _ := s.Version(ctx); v != SchemaVersion() {
			t.Errorf("version after migration = %d, want %d", v, SchemaVersion())
		}
		if applied, _ := s.Meta(ctx, "migration.0001"); applied == "" {
			t.Error("migration 0001 is not recorded in meta")
		}
	})
}

func TestMigrationsAreContiguous(t *testing.T) {
	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range migs {
		if m.version != i+1 {
			t.Errorf("migration %d has version %d", i+1, m.version)
		}
		if strings.TrimSpace(m.sql) == "" {
			t.Errorf("migration %d is empty", m.version)
		}
	}
}

func TestSchemaHasDesignTables(t *testing.T) {
	s := newStore(t)
	for _, table := range []string{"event", "dimm", "placement", "boot", "action", "fault", "verdict", "gpu_mem"} {
		var n int
		if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s: found %d, %v", table, n, err)
		}
	}
	// The event table carries the FMFM field names as column names.
	rows, err := s.db.Query(`SELECT name FROM pragma_table_info('event')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var c string
		rows.Scan(&c)
		cols[c] = true
	}
	for _, c := range []string{
		"ErrorDT", "ErrorSeverity", "ErrorScrubType", "SocketId", "ChannelId", "ChannelSlotId", "Rank", "DeviceId",
		"BankGroup", "Bank", "Row", "Column", "ErrorVector", "OsPhysicalAddress", "source", "raw_ref", "grade", "boot_id", "dimm_uid",
	} {
		if !cols[c] {
			t.Errorf("event table lacks column %q", c)
		}
	}
}

func TestUpdateRollsBackOnError(t *testing.T) {
	s := newStore(t)
	boom := errors.New("boom")
	err := s.Update(ctx, func(w *Writer) error {
		if _, err := w.InsertEvent(ev("klog/edac", "L1", at(0), 0x1000)); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if events, _ := s.EventList(ctx, EventFilter{}); len(events) != 0 {
		t.Errorf("%d events survived a failed update", len(events))
	}
}

func TestMeta(t *testing.T) {
	s := newStore(t)
	if v, err := s.Meta(ctx, "absent"); err != nil || v != "" {
		t.Errorf("absent key = %q, %v", v, err)
	}
	if err := s.SetMeta(ctx, "case", "A"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMeta(ctx, "case", "B"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Meta(ctx, "case"); v != "B" {
		t.Errorf("case = %q, want B", v)
	}
}
