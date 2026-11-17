package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/agenticode/my/internal/model"
)

// TestMigrateVersion10: a ledger of schema 10 with a host keeps it and gets
// the signature and the populated flags of its processor records, unknown;
// a later source that knows them fills them, and one that does not keeps
// them.
func TestMigrateVersion10(t *testing.T) {
	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "v10.db")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migs[:10] {
		if _, err := db.Exec(m.sql); err != nil {
			t.Fatalf("migration %d: %v", m.version, err)
		}
	}
	for _, q := range []string{
		`PRAGMA application_id = 1297697863`,
		`PRAGMA user_version = 10`,
		`INSERT INTO meta (key, value) VALUES ('ledger_id', '0123456789abcdef0123456789abcdef')`,
		`INSERT INTO host (name, system_vendor, system_product, board, sockets, cpu) VALUES ('h1', 'Dell Inc.', 'PowerEdge R640', 'Dell Inc. 0W23H8', 2, 'Intel(R) Xeon(R) Gold 6130 CPU @ 2.10GHz')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("build version 10 ledger: %v\n%s", err, q)
		}
	}
	db.Close()
	s, err := Open(ctx, p, ReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v, _ := s.Version(ctx); v != SchemaVersion() || v < 11 {
		t.Fatalf("version after migration = %d", v)
	}
	hosts, err := s.Hosts(ctx)
	if err != nil || len(hosts) != 1 || hosts[0].Sockets != 2 || hosts[0].Board != "Dell Inc. 0W23H8" || hosts[0].CPUSignature != "" || hosts[0].CPUPresent != "" {
		t.Fatalf("hosts %+v, %v", hosts, err)
	}
	err = s.Update(ctx, func(w *Writer) error {
		if err := w.UpsertHost(&model.Host{Name: "h1", Sockets: 2, CPU: "Intel(R) Xeon(R) Gold 6130 CPU @ 2.10GHz", CPUSignature: "6/85/4", CPUPresent: "10"}); err != nil {
			return err
		}
		return w.UpsertHost(&model.Host{Name: "h1", SystemVendor: "Dell Inc."})
	})
	if err != nil {
		t.Fatal(err)
	}
	hosts, _ = s.Hosts(ctx)
	if hosts[0].CPUSignature != "6/85/4" || hosts[0].CPUPresent != "10" || hosts[0].Sockets != 2 {
		t.Errorf("host %+v", hosts[0])
	}
}
