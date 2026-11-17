package store

import (
	"bytes"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
)

// TestMigrateVersion11: a ledger of schema 11 with a host, a DIMM and a
// reading keeps them and gets an empty table of SPD readings.
func TestMigrateVersion11(t *testing.T) {
	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "v11.db")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migs[:11] {
		if _, err := db.Exec(m.sql); err != nil {
			t.Fatalf("migration %d: %v", m.version, err)
		}
	}
	for _, q := range []string{
		`PRAGMA application_id = 1297697863`,
		`PRAGMA user_version = 11`,
		`INSERT INTO meta (key, value) VALUES ('ledger_id', '0123456789abcdef0123456789abcdef')`,
		`INSERT INTO host (name, system_vendor, system_product, sockets, cpu, cpu_signature, cpu_present) VALUES ('h1', 'Dell Inc.', 'PowerEdge R640', 2, 'Xeon', '6/85/4', '11')`,
		`INSERT INTO dimm (dimm_uid, identity, manufacturer, part_number, serial) VALUES ('d1', 'serial', 'Example', 'EX1', 'S1')`,
		`INSERT INTO host_reading (host, kind, taken_at, text) VALUES ('h1', 'meminfo', '2026-11-17T00:00:00Z', 'MemTotal: 1 kB')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("build version 11 ledger: %v\n%s", err, q)
		}
	}
	db.Close()
	s, err := Open(ctx, p, ReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v, _ := s.Version(ctx); v != SchemaVersion() || v < 12 {
		t.Fatalf("version after migration = %d", v)
	}
	hosts, err := s.Hosts(ctx)
	if err != nil || len(hosts) != 1 || hosts[0].CPUPresent != "11" {
		t.Fatalf("hosts %+v, %v", hosts, err)
	}
	rs, err := s.Readings(ctx, "h1")
	if err != nil || len(rs) != 1 {
		t.Fatalf("readings %+v, %v", rs, err)
	}
	spds, err := s.SPDReadings(ctx, "h1")
	if err != nil || len(spds) != 0 {
		t.Fatalf("spd readings %+v, %v", spds, err)
	}
}

// TestPutSPD: a reading stored, read back, stored again with the same
// bytes (one row, last_seen and scan moved) and with other bytes (two
// rows); a failed read without bytes; the bounds.
func TestPutSPD(t *testing.T) {
	s, err := Open(ctx, filepath.Join(t.TempDir(), "l.db"), Create)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	t1 := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(24 * time.Hour)
	raw := bytes.Repeat([]byte{0x23, 0x12, 0x0C}, 100)
	put := func(r model.SPDReading) bool {
		t.Helper()
		var added bool
		err := s.Update(ctx, func(w *Writer) error {
			var err error
			added, err = w.PutSPD(&r)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return added
	}
	scan := func(at time.Time) int64 {
		sc := &model.Scan{Host: "h1", Kind: model.ScanLive, StartedAt: at, ToolVersion: "test"}
		if err := s.Update(ctx, func(w *Writer) error { return w.InsertScan(sc) }); err != nil {
			t.Fatal(err)
		}
		return sc.ID
	}
	s1, s2, s3 := scan(t1), scan(t2), scan(t2.Add(time.Hour))
	base := model.SPDReading{Host: "h1", Origin: "/sys/bus/i2c/devices/0-0050/eeprom", Bus: "i2c-0", Address: 0x50, Driver: "ee1004",
		Form: model.SPDFormEEPROM, State: "ok", Raw: raw}
	r := base
	r.ScanID, r.LastSeen = s1, t1
	if !put(r) {
		t.Fatal("the first reading added no row")
	}
	r.ScanID, r.LastSeen = s2, t2
	if put(r) {
		t.Fatal("the same bytes added a row")
	}
	got, err := s.SPDReadings(ctx, "h1")
	if err != nil || len(got) != 1 {
		t.Fatalf("%d rows, %v", len(got), err)
	}
	g := got[0]
	if !g.FirstSeen.Equal(t1) || !g.LastSeen.Equal(t2) || g.ScanID != s2 || !bytes.Equal(g.Raw, raw) || g.SHA256 != SPDSum(raw) ||
		g.Bus != "i2c-0" || g.Address != 0x50 || g.Driver != "ee1004" || g.Form != "eeprom" || g.State != "ok" {
		t.Errorf("row %+v", g)
	}
	// An older reading of the same bytes moves no scan and first_seen
	// back.
	r.ScanID, r.LastSeen = s1, t1.Add(-time.Hour)
	if put(r) {
		t.Fatal("an older reading with the same bytes added a row")
	}
	got, _ = s.SPDReadings(ctx, "h1")
	if !got[0].FirstSeen.Equal(t1.Add(-time.Hour)) || !got[0].LastSeen.Equal(t2) || got[0].ScanID != s2 {
		t.Errorf("after an older reading: %+v", got[0])
	}
	// Other bytes: a second row.
	other := bytes.Clone(raw)
	other[100] ^= 1
	r.ScanID, r.LastSeen, r.Raw = s3, t2.Add(time.Hour), other
	if !put(r) {
		t.Fatal("other bytes added no row")
	}
	// A failed read: no bytes, state io; again, one row.
	f := base
	f.Origin, f.Address, f.Raw, f.State, f.Detail = "/sys/bus/i2c/devices/0-0051/eeprom", 0x51, nil, "io", "errno=EIO at=0"
	f.ScanID, f.LastSeen = s1, t1
	put(f)
	f.ScanID, f.LastSeen = s2, t2
	if put(f) {
		t.Error("the same failed read added a row")
	}
	f.Detail = "errno=ENXIO at=0"
	if !put(f) {
		t.Error("another failure added no row")
	}
	got, _ = s.SPDReadings(ctx, "h1")
	if len(got) != 5-1 {
		t.Fatalf("%d rows", len(got))
	}
	for _, g := range got {
		if g.Origin == f.Origin && g.Raw != nil {
			t.Errorf("a failed read holds bytes: %+v", g)
		}
	}
	// decode-dimms text: no bytes, the fields.
	d := model.SPDReading{Host: "h1", Origin: "dimms.txt", Form: model.SPDFormDecodeDimms, State: "ok", Fields: `{"serial":"4D590001"}`, LastSeen: t1}
	put(d)
	got, _ = s.SPDReadings(ctx, "h1")
	var found bool
	for _, g := range got {
		if g.Form == model.SPDFormDecodeDimms {
			found = g.Fields == d.Fields && g.Raw == nil && g.SHA256 == SPDSum([]byte(d.Fields))
		}
	}
	if !found {
		t.Error("decode-dimms reading not kept")
	}
	// The tie.
	err = s.Update(ctx, func(w *Writer) error { return w.TieSPD(got[0].ID, "d123", model.SPDTieSerial) })
	if err != nil {
		t.Fatal(err)
	}
	got, _ = s.SPDReadings(ctx, "h1")
	if got[0].DIMMUID != "d123" || got[0].Tie != "serial" {
		t.Errorf("tie %+v", got[0])
	}
	for _, bad := range []model.SPDReading{
		{Origin: "x", Form: "eeprom", State: "ok", LastSeen: t1},
		{Host: "h1", Form: "eeprom", State: "ok", LastSeen: t1},
		{Host: "h1", Origin: "x", Form: "paper", State: "ok", LastSeen: t1},
		{Host: "h1", Origin: "x", Form: "eeprom", LastSeen: t1},
		{Host: "h1", Origin: "x", Form: "eeprom", State: "ok"},
		{Host: "h1", Origin: "x", Form: "eeprom", State: "ok", LastSeen: t1, Raw: make([]byte, 2049)},
	} {
		err := s.Update(ctx, func(w *Writer) error { _, err := w.PutSPD(&bad); return err })
		if err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}
