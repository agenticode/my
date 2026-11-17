package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
)

// TestMigrateVersion9: a ledger of schema 9 with a host keeps it, gets the
// socket count and processor name of the host (unknown), and the table of
// readings, empty.
func TestMigrateVersion9(t *testing.T) {
	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "v9.db")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migs[:9] {
		if _, err := db.Exec(m.sql); err != nil {
			t.Fatalf("migration %d: %v", m.version, err)
		}
	}
	for _, q := range []string{
		`PRAGMA application_id = 1297697863`,
		`PRAGMA user_version = 9`,
		`INSERT INTO meta (key, value) VALUES ('ledger_id', '0123456789abcdef0123456789abcdef')`,
		`INSERT INTO host (name, system_vendor, system_product) VALUES ('h1', 'Dell Inc.', 'PowerEdge R6615')`,
		`INSERT INTO scan (id, host, kind, started_at, tool_version) VALUES (1, 'h1', 'live', '2026-11-01T00:00:00.000000Z', 'v')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("build version 9 ledger: %v\n%s", err, q)
		}
	}
	db.Close()
	s, err := Open(ctx, p, ReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v, _ := s.Version(ctx); v != SchemaVersion() || v < 10 {
		t.Fatalf("version after migration = %d", v)
	}
	hosts, err := s.Hosts(ctx)
	if err != nil || len(hosts) != 1 || hosts[0].SystemVendor != "Dell Inc." || hosts[0].Sockets != 0 || hosts[0].CPU != "" {
		t.Fatalf("hosts %+v, %v", hosts, err)
	}
	at := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	err = s.Update(ctx, func(w *Writer) error {
		if err := w.UpsertHost(&model.Host{Name: "h1", Sockets: 2, CPU: "AMD EPYC 9254 24-Core Processor"}); err != nil {
			return err
		}
		// A later source without processor records keeps what was known.
		if err := w.UpsertHost(&model.Host{Name: "h1", SystemVendor: "Dell Inc."}); err != nil {
			return err
		}
		for _, r := range []model.HostReading{
			{Host: "h1", ScanID: 1, Kind: model.ReadingMeminfo, TakenAt: at, Origin: "/proc/meminfo", Text: "MemTotal: 1 kB\n"},
			{Host: "h1", Kind: model.ReadingMeminfo, TakenAt: at.Add(time.Hour), Text: "MemTotal: 2 kB\n"},
			{Host: "h1", Kind: model.ReadingNumastat, TakenAt: at, Text: "x", Cut: true},
		} {
			if err := w.PutReading(&r); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	hosts, _ = s.Hosts(ctx)
	if hosts[0].Sockets != 2 || hosts[0].CPU != "AMD EPYC 9254 24-Core Processor" {
		t.Errorf("host %+v", hosts[0])
	}
	rs, err := s.Readings(ctx, "h1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 2 || rs[0].Kind != model.ReadingMeminfo || rs[0].Text != "MemTotal: 2 kB\n" || !rs[1].Cut {
		t.Errorf("readings %+v", rs)
	}
	err = s.Update(ctx, func(w *Writer) error {
		return w.PutReading(&model.HostReading{Host: "h1", Kind: "x", TakenAt: at, Text: string(make([]byte, MaxReadingBytes+1))})
	})
	if err == nil {
		t.Error("a reading over the bound was kept")
	}
}
