package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
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

	// A closed ledger answers with errors and can be closed again.
	if _, err := s.Hosts(ctx); err == nil {
		t.Error("read on a closed ledger returned no error")
	}
	if err := s.Update(ctx, func(*Writer) error { return nil }); err == nil {
		t.Error("write on a closed ledger returned no error")
	}
	if err := s.Close(); err != nil {
		t.Errorf("second close: %v", err)
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
	for _, table := range []string{"event", "dimm", "placement", "boot", "action", "fault", "verdict", "gpu_mem", "ecs_sample"} {
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

// TestMigrateVersion1 opens a ledger written with schema version 1 and
// checks that what it held is still there afterwards.
func TestMigrateVersion1(t *testing.T) {
	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "v1.db")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	// A version 1 ledger: the first migration and nothing after it.
	for _, q := range []string{
		migs[0].sql,
		`PRAGMA application_id = 1297697863`,
		`PRAGMA user_version = 1`,
		`INSERT INTO meta (key, value) VALUES ('ledger_id', '0123456789abcdef0123456789abcdef')`,
		`INSERT INTO dimm (dimm_uid, identity, serial) VALUES ('dA', 'serial', '80A1C3')`,
		`INSERT INTO event (host, "ErrorDT", "ErrorSeverity", source, raw_ref, grade, fingerprint) VALUES ('h1', '2026-11-01T00:00:00.000000Z', 'CE', 'klog/edac', 'L1', 'C', 'e1')`,
		`INSERT INTO action (host, at, kind, method, result, "OsPhysicalAddress", "Rank", "DeviceId", "BankGroup", "Bank", "Row", label, slot, dimm_uid, count, boot_id, detail, source, raw_ref, raw, fingerprint)
		 VALUES ('h1', '2026-11-01T01:00:00.000000Z', 'hppr', 'bios', 'ok', 4096, 0, 7, 0, 3, 6703, 'CPU0_C1', 'CPU0_C1', 'dA', 2, 'boot-1', 'repair at boot', 'synth', 'ppr/1', 'raw text', 'a1')`,
		`INSERT INTO fault (host, dimm_uid, mode, grade, first_seen, last_seen) VALUES ('h1', 'dA', 'row', 'A', '2026-11-01T00:00:00.000000Z', '2026-11-01T00:00:00.000000Z')`,
		`INSERT INTO fault_event (fault_id, event_id) VALUES (1, 1)`,
		`INSERT INTO verdict (host, dimm_uid, grade, evidence, counter_evidence, next_action, computed_at)
		 VALUES ('h1', 'dA', 'observe', '[{"rule":"R-UE-01","text":"1 UE"}]', '["plain text"]', 'plain text', '2026-11-01T02:00:00.000000Z')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("build version 1 ledger: %v\n%s", err, q)
		}
	}
	db.Close()

	if _, err := Open(ctx, p, ReadOnly); !errors.Is(err, ErrNeedsMigration) {
		t.Fatalf("read-only open of a version 1 ledger: err = %v, want ErrNeedsMigration", err)
	}
	s, err := Open(ctx, p, ReadWrite)
	if err != nil {
		t.Fatalf("open for writing: %v", err)
	}
	defer s.Close()
	if v, _ := s.Version(ctx); v != SchemaVersion() || v < 2 {
		t.Fatalf("version after migration = %d", v)
	}
	if id, _ := s.Meta(ctx, "ledger_id"); id != "0123456789abcdef0123456789abcdef" {
		t.Errorf("ledger_id = %q", id)
	}
	if applied, _ := s.Meta(ctx, "migration.0002"); applied == "" {
		t.Error("migration 0002 is not recorded in meta")
	}
	// The action is there with every column.
	acts, err := s.Actions(ctx, ActionFilter{})
	want := model.Action{ID: 1, Host: "h1", At: time.Date(2026, 11, 1, 1, 0, 0, 0, time.UTC), TimeQuality: model.TimeExact, Kind: model.ActionHPPR,
		Method: "bios", Result: model.ResultOK, OsPhysicalAddress: model.Some(uint64(4096)), Rank: model.Some(0), DeviceId: model.Some(7),
		BankGroup: model.Some(0), Bank: model.Some(3), Row: model.Some(6703), Label: "CPU0_C1", Slot: "CPU0_C1", DIMMUID: "dA", Count: 2,
		BootID: "boot-1", Detail: "repair at boot", Source: "synth", RawRef: "ppr/1", Raw: "raw text", Fingerprint: "a1"}
	if err != nil || len(acts) != 1 || !reflect.DeepEqual(acts[0], want) {
		t.Errorf("actions after migration = %+v, %v\nwant %+v", acts, err, want)
	}
	// Its indexes and its fingerprint constraint were rebuilt with the table.
	var idx int
	s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND tbl_name = 'action' AND name IN ('action_time', 'action_dimm')`).Scan(&idx)
	if idx != 2 {
		t.Errorf("%d of 2 action indexes after migration", idx)
	}
	var left int
	s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'action_v2'`).Scan(&left)
	if left != 0 {
		t.Error("the temporary table of the migration is still there")
	}
	mustUpdate(t, s, func(w *Writer) error {
		again, err := w.InsertAction(&model.Action{Host: "h1", At: at(0), Kind: model.ActionHPPR, Source: "synth", Fingerprint: "a1"})
		if err != nil || again {
			return fmt.Errorf("known fingerprint inserted again: %v, %v", again, err)
		}
		// The new kinds are accepted.
		_, err = w.InsertAction(&model.Action{Host: "h1", At: at(0), Kind: model.ActionDisable, Method: "bios", Slot: "CPU0_C1", Source: "test", RawRef: "x", Fingerprint: "a2"})
		return err
	})
	// Events stay; faults and findings of the earlier engine are dropped.
	evs, _ := s.EventList(ctx, EventFilter{})
	faults, _ := s.Faults(ctx, "h1")
	verdicts, err := s.Verdicts(ctx, "h1")
	if len(evs) != 1 || len(faults) != 0 || len(verdicts) != 0 || err != nil {
		t.Errorf("%d events, %d faults, %d verdicts, %v; want 1, 0, 0", len(evs), len(faults), len(verdicts), err)
	}
	var members int
	s.db.QueryRow(`SELECT count(*) FROM fault_event`).Scan(&members)
	if members != 0 {
		t.Errorf("%d fault_event rows left behind", members)
	}
	// A ledger that was migrated opens read-only from then on.
	s.Close()
	ro, err := Open(ctx, p, ReadOnly)
	if err != nil {
		t.Fatalf("read-only open after migration: %v", err)
	}
	ro.Close()
}

func TestWriterSetMeta(t *testing.T) {
	s := newStore(t)
	mustUpdate(t, s, func(w *Writer) error {
		if err := w.SetMeta("analysis.h1", "first"); err != nil {
			return err
		}
		return w.SetMeta("analysis.h1", "second")
	})
	if v, _ := s.Meta(ctx, "analysis.h1"); v != "second" {
		t.Errorf("meta = %q", v)
	}
	// It is part of the transaction.
	s.Update(ctx, func(w *Writer) error {
		w.SetMeta("analysis.h1", "third")
		return errors.New("roll back")
	})
	if v, _ := s.Meta(ctx, "analysis.h1"); v != "second" {
		t.Errorf("meta after a rolled back update = %q", v)
	}
}

func TestSnapshot(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, filepath.Join(dir, "ledger.db"), Create)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetMeta(ctx, "note", "kept"); err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(dir, "copy.db")
	if err := s.Snapshot(ctx, copyPath); err != nil {
		t.Fatal(err)
	}
	// The copy must not replace a file that is there.
	if err := s.Snapshot(ctx, copyPath); !errors.Is(err, fs.ErrExist) {
		t.Errorf("second snapshot to the same file: %v", err)
	}
	s.Close()

	// The copy is a ledger of its own, and a read-only ledger can be copied.
	c, err := Open(ctx, copyPath, ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if v, _ := c.Meta(ctx, "note"); v != "kept" {
		t.Errorf("meta of the copy = %q", v)
	}
	if err := c.Snapshot(ctx, filepath.Join(dir, "copy2.db")); err != nil {
		t.Errorf("snapshot of a read-only ledger: %v", err)
	}
}

func TestUpsertHostKeepsVendorAndProductApart(t *testing.T) {
	s := newStore(t)
	host := func() model.Host {
		t.Helper()
		hosts, err := s.Hosts(ctx)
		if err != nil || len(hosts) != 1 {
			t.Fatalf("hosts = %+v, %v", hosts, err)
		}
		return hosts[0]
	}
	up := func(h model.Host) {
		t.Helper()
		h.Name = "h1"
		mustUpdate(t, s, func(w *Writer) error { return w.UpsertHost(&h) })
	}
	// /sys/class/dmi knows vendor and product; the kernel log then names
	// the machine in one string. The two stay apart.
	up(model.Host{SystemVendor: "Microsoft Corporation", SystemProduct: "Virtual Machine"})
	up(model.Host{SystemProduct: "Microsoft Corporation Virtual Machine/Virtual Machine"})
	if h := host(); h.SystemVendor != "Microsoft Corporation" || h.SystemProduct != "Virtual Machine" {
		t.Errorf("host = %+v", h)
	}
	// Another product is another product.
	up(model.Host{SystemProduct: "Example Systems R650/0PYXKY"})
	if h := host(); h.SystemProduct != "Example Systems R650/0PYXKY" {
		t.Errorf("host = %+v", h)
	}
	// A source that names vendor and product replaces what is there.
	up(model.Host{SystemVendor: "Example Systems", SystemProduct: "R650"})
	if h := host(); h.SystemVendor != "Example Systems" || h.SystemProduct != "R650" {
		t.Errorf("host = %+v", h)
	}
	up(model.Host{SystemProduct: "Example Systems R650/0PYXKY"})
	if h := host(); h.SystemProduct != "R650" {
		t.Errorf("host = %+v", h)
	}
}

// TestUpsertHostVirtual: what is known about the hypervisor of a host stays
// when a later source knows nothing or less, and a name replaces the word
// for a hypervisor that is not known by name.
func TestUpsertHostVirtual(t *testing.T) {
	s := newStore(t)
	virtual := func() string {
		t.Helper()
		hosts, err := s.Hosts(ctx)
		if err != nil || len(hosts) != 1 {
			t.Fatalf("hosts = %+v, %v", hosts, err)
		}
		return hosts[0].Virtual
	}
	up := func(v string) {
		t.Helper()
		mustUpdate(t, s, func(w *Writer) error { return w.UpsertHost(&model.Host{Name: "h1", Virtual: v}) })
	}
	for _, step := range []struct{ set, want string }{
		{"", ""},
		{"hypervisor", "hypervisor"}, // the flag of the CPU
		{"KVM", "KVM"},               // the kernel log names it
		{"hypervisor", "KVM"},        // the flag again: the name stays
		{"", "KVM"},                  // a source that says nothing
		{"VMware", "VMware"},         // another name replaces it
	} {
		up(step.set)
		if got := virtual(); got != step.want {
			t.Errorf("after %q: Virtual = %q, want %q", step.set, got, step.want)
		}
	}
}

// TestSourceGrades checks the best grade per source name of the errors of
// a host: counter records count, informational records and other hosts do
// not.
func TestSourceGrades(t *testing.T) {
	s := newStore(t)
	a := ev("rasdaemon/mc_event", "r1", at(0), 0x1000)
	a.Grade = model.GradeA
	b := ev("rasdaemon/mc_event", "r2", at(time.Minute), 0x2000)
	b.Grade = model.GradeB
	k := ev("klog/edac", "k1", at(2*time.Minute), 0x3000)
	k.Grade = model.GradeB
	c := &model.Event{Host: "h1", Kind: model.KindCounter, ErrorDT: at(time.Hour), WindowStart: at(0), ErrorSeverity: model.SevCE,
		Source: "edac-sysfs/counter", RawRef: "c1", Count: 4, Grade: model.GradeC, Slot: "CPU0_C1", Fingerprint: model.Fingerprint("c1")}
	info := ev("sel/list", "i1", at(3*time.Minute), 0)
	info.ErrorSeverity, info.Grade, info.OsPhysicalAddress = model.SevInformational, model.GradeA, model.Opt[uint64]{}
	other := ev("fmfm/runtime", "o1", at(4*time.Minute), 0x4000)
	other.Host, other.Grade = "h2", model.GradeA
	insert(t, s, a, b, k, c, info, other)

	got, err := s.SourceGrades(ctx, "h1")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]model.Grade{"rasdaemon/mc_event": model.GradeA, "klog/edac": model.GradeB, "edac-sysfs/counter": model.GradeC}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SourceGrades = %v, want %v", got, want)
	}
	if got, err := s.SourceGrades(ctx, "h9"); err != nil || len(got) != 0 {
		t.Errorf("unknown host: %v, %v", got, err)
	}
}
