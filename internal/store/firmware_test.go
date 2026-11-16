package store

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
)

// TestMigrateVersion8: a ledger of schema 8 keeps every action of every
// kind it could hold, takes the kinds that came later, and gets the
// firmware tables and the source counters, empty.
func TestMigrateVersion8(t *testing.T) {
	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "v8.db")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migs[:8] {
		if _, err := db.Exec(m.sql); err != nil {
			t.Fatalf("migration %d: %v", m.version, err)
		}
	}
	old := []string{"page-offline", "sppr", "hppr", "sparing", "row-remap", "replacement", "reseat", "disable", "mark-failed"}
	qs := []string{
		`PRAGMA application_id = 1297697863`,
		`PRAGMA user_version = 8`,
		`INSERT INTO meta (key, value) VALUES ('ledger_id', '0123456789abcdef0123456789abcdef')`,
		`INSERT INTO scan (id, host, kind, started_at, tool_version) VALUES (1, 'h1', 'import', '2026-11-01T00:00:00.000000Z', 'v')`,
		`INSERT INTO source (id, scan_id, kind, origin, status, records) VALUES (1, 1, 'redfish', 'dump', 'ok', 3)`,
		`INSERT INTO event (host, "ErrorDT", "ErrorSeverity", source, raw_ref, grade, fingerprint) VALUES ('h1', '2026-11-01T01:00:00.000000Z', 'CE', 'redfish/log', 'L1', 'C', 'e1')`,
	}
	for i, k := range old {
		qs = append(qs, fmt.Sprintf(`INSERT INTO action (host, at, kind, method, result, "Rank", label, source, raw_ref, raw, fingerprint, scan_id)
 VALUES ('h1', '2026-11-01T0%d:00:00.000000Z', '%s', 'bios', 'ok', %d, 'DIMM A%d', 'sel/sel', 'R%d', 'raw %d', 'a%d', 1)`, i, k, i, i, i, i, i))
	}
	for _, q := range qs {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("build version 8 ledger: %v\n%s", err, q)
		}
	}
	db.Close()
	s, err := Open(ctx, p, ReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v, _ := s.Version(ctx); v != SchemaVersion() || v < 9 {
		t.Fatalf("version after migration = %d", v)
	}
	acts, err := s.Actions(ctx, ActionFilter{Host: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(acts) != len(old) {
		t.Fatalf("%d actions after migration, want %d", len(acts), len(old))
	}
	for i, a := range acts {
		if string(a.Kind) != old[i] || a.Method != "bios" || a.Result != model.ResultOK || a.Rank != model.Some(i) ||
			a.Label != fmt.Sprintf("DIMM A%d", i) || a.Raw != fmt.Sprintf("raw %d", i) || a.ScanID != 1 || a.DupOf != 0 || a.WithdrawnBy != 0 {
			t.Errorf("action %d after migration: %+v", i, a)
		}
	}
	srcs, err := s.Sources(ctx, 1)
	if err != nil || len(srcs) != 1 || srcs[0].Records != 3 || srcs[0].Unread != 0 || srcs[0].Faults != 0 {
		t.Fatalf("sources after migration: %+v, %v", srcs, err)
	}
	// The kinds of this version go in without a check of the schema.
	for i, k := range []model.ActionKind{model.ActionPPR, model.ActionDeviceSparing, model.ActionPredictedFailure,
		model.ActionMirrorFailover, model.ActionTrainingFailure} {
		mustUpdate(t, s, func(w *Writer) error {
			_, err := w.InsertAction(&model.Action{Host: "h1", At: time.Date(2026, 11, 2, i, 0, 0, 0, time.UTC), Kind: k,
				Source: "redfish/log", RawRef: "N", Fingerprint: "n" + string(k)})
			return err
		})
	}
	if acts, _ := s.Actions(ctx, ActionFilter{Host: "h1"}); len(acts) != len(old)+5 {
		t.Errorf("%d actions after adding five of the new kinds", len(acts))
	}
	if fw, err := s.FirmwareRecords(ctx, FirmwareFilter{}); err != nil || len(fw) != 0 {
		t.Errorf("firmware records after migration: %+v, %v", fw, err)
	}
}

func firmwareSample(common string) *model.FirmwareRecord {
	return &model.FirmwareRecord{Common: common, Flags: []string{"soft"}, Vendor: "Example", VendorID: "MEM0804",
		Registry: "EXAMPLE", RegistryVersion: "2.9", VendorSeverity: "OK", Origin: model.OriginBMCLog, Log: "Lclog",
		Location: "DIMM.Socket.A1", Args: []string{"A1"}, Text: strings.Repeat("x", 600), EntryKey: "k1",
		Mapping: model.MappingTable, MapRef: "example@1:MEM0804", Basis: "registry"}
}

// TestFirmwareRecords: a firmware record goes into fw_record with the event
// or action it came with, or alone; reading the same records again adds no
// row; a row goes with its event.
func TestFirmwareRecords(t *testing.T) {
	s := newStore(t)
	at := time.Date(2026, 11, 3, 2, 14, 7, 0, time.UTC)
	write := func() {
		mustUpdate(t, s, func(w *Writer) error {
			e := &model.Event{Host: "h1", ErrorDT: at, ErrorSeverity: model.SevCE, Source: "redfish/log", RawRef: "E1",
				Fingerprint: "fe1", Firmware: firmwareSample("ce-threshold")}
			if _, err := w.InsertEvent(e); err != nil {
				return err
			}
			a := &model.Action{Host: "h1", At: at.Add(time.Hour), Kind: model.ActionSPPR, Result: model.ResultOK,
				Source: "redfish/log", RawRef: "A1", Fingerprint: "fa1", Firmware: firmwareSample("ppr-done")}
			if _, err := w.InsertAction(a); err != nil {
				return err
			}
			alone := firmwareSample("presence")
			alone.Host, alone.At, alone.Source, alone.RawRef, alone.Fingerprint, alone.Raw = "h1", at.Add(2*time.Hour), "redfish/log", "P1", "fp1", `{"x":1}`
			_, err := w.InsertFirmware(alone)
			return err
		})
	}
	write()
	write()
	fw, err := s.FirmwareRecords(ctx, FirmwareFilter{Host: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(fw) != 3 {
		t.Fatalf("%d firmware records, want 3: %+v", len(fw), fw)
	}
	if fw[0].Common != "ce-threshold" || fw[0].EventID == 0 || fw[0].ActionID != 0 || fw[0].Source != "redfish/log" || fw[0].RawRef != "E1" ||
		!fw[0].At.Equal(at) || fw[0].Fingerprint != "" || len(fw[0].Text) > model.MaxFirmwareText || fw[0].Repeat != 1 ||
		fw[0].Args[0] != "A1" || fw[0].Flags[0] != "soft" || fw[0].MapRef != "example@1:MEM0804" || fw[0].Basis != "registry" {
		t.Errorf("record of the event: %+v", fw[0])
	}
	if fw[1].Common != "ppr-done" || fw[1].ActionID == 0 || fw[1].EventID != 0 {
		t.Errorf("record of the action: %+v", fw[1])
	}
	if fw[2].Common != "presence" || fw[2].EventID != 0 || fw[2].ActionID != 0 || fw[2].Fingerprint != "fp1" || fw[2].Raw != `{"x":1}` {
		t.Errorf("record alone: %+v", fw[2])
	}
	if alone, _ := s.FirmwareRecords(ctx, FirmwareFilter{Host: "h1", Alone: true}); len(alone) != 1 {
		t.Errorf("alone: %+v", alone)
	}
	if ce, _ := s.FirmwareRecords(ctx, FirmwareFilter{Common: []string{"ce-threshold", "ppr-done"}}); len(ce) != 2 {
		t.Errorf("by common event: %+v", ce)
	}
	// The row goes with its event.
	mustUpdate(t, s, func(w *Writer) error {
		_, err := w.exec(`DELETE FROM event WHERE id = ?`, fw[0].EventID)
		return err
	})
	if left, _ := s.FirmwareRecords(ctx, FirmwareFilter{}); len(left) != 2 {
		t.Errorf("after the event was removed: %+v", left)
	}

	// Content checks.
	for name, f := range map[string]*model.FirmwareRecord{
		"no common event":   {Host: "h1", At: at, Fingerprint: "x"},
		"not a common name": {Host: "h1", At: at, Common: "PPR Done", Fingerprint: "x"},
		"no fingerprint":    {Host: "h1", At: at, Common: "presence"},
		"no time":           {Host: "h1", Common: "presence", Fingerprint: "x"},
		"no host":           {At: at, Common: "presence", Fingerprint: "x"},
	} {
		err := s.Update(ctx, func(w *Writer) error { _, err := w.InsertFirmware(f); return err })
		if !errors.Is(err, ErrInvalidRecord) {
			t.Errorf("%s: %v, want an invalid record", name, err)
		}
	}
}

// TestUnreadRecords: at most 50 per source, text cut at 4 KiB, gone with
// the source.
func TestUnreadRecords(t *testing.T) {
	s := newStore(t)
	src := model.Source{Kind: "redfish", Origin: "dump"}
	mustUpdate(t, s, func(w *Writer) error {
		sc := &model.Scan{Host: "h1", Kind: model.ScanImport, StartedAt: time.Now()}
		if err := w.InsertScan(sc); err != nil {
			return err
		}
		src.ScanID, src.Unread, src.Faults = sc.ID, 70, 2
		if err := w.InsertSource(&src); err != nil {
			return err
		}
		var recs []model.UnreadRecord
		for i := range 70 {
			recs = append(recs, model.UnreadRecord{Ref: fmt.Sprintf("r%d", i), Reason: "not JSON", Text: "DIMM " + strings.Repeat("y", 5000)})
		}
		return w.InsertUnread(src.ID, recs)
	})
	got, err := s.UnreadRecords(ctx, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != model.MaxUnreadKept {
		t.Fatalf("%d unread records kept, want %d", len(got), model.MaxUnreadKept)
	}
	if got[0].Ref != "r0" || got[0].Reason != "not JSON" || len(got[0].Text) > model.MaxUnreadText || !strings.HasSuffix(got[0].Text, "...") {
		t.Errorf("first unread record: ref %q reason %q, %d bytes", got[0].Ref, got[0].Reason, len(got[0].Text))
	}
	if srcs, _ := s.Sources(ctx, src.ScanID); len(srcs) != 1 || srcs[0].Unread != 70 || srcs[0].Faults != 2 {
		t.Errorf("source: %+v", srcs)
	}
	mustUpdate(t, s, func(w *Writer) error { return w.DropScan(src.ScanID, "h1", time.Now()) })
	if got, _ := s.UnreadRecords(ctx, src.ID); len(got) != 0 {
		t.Errorf("%d unread records left after the source was dropped", len(got))
	}
}

// TestRegistriesSeen: one row per registry of a host; a later reading
// replaces it.
func TestRegistriesSeen(t *testing.T) {
	s := newStore(t)
	at := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	for i, mapped := range []int{100, 120} {
		mustUpdate(t, s, func(w *Writer) error {
			return w.PutRegistrySeen(&model.RegistrySeen{Host: "h1", SeenAt: at.Add(time.Duration(i) * time.Hour), Prefix: "EXAMPLE",
				Version: "2.9", Language: "en", Messages: 3000, Memory: 122, Mapped: mapped, Wording: 1, Unmapped: []string{"MEM9999"},
				Source: "dump"})
		})
	}
	mustUpdate(t, s, func(w *Writer) error {
		return w.PutRegistrySeen(&model.RegistrySeen{Host: "h1", SeenAt: at, Prefix: "Platform", Version: "1.5.0", Messages: 40, Memory: 10})
	})
	got, err := s.RegistriesSeen(ctx, "h1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Prefix != "EXAMPLE" || got[0].Mapped != 120 || got[0].Unmapped[0] != "MEM9999" || !got[0].SeenAt.Equal(at.Add(time.Hour)) ||
		got[1].Prefix != "Platform" || got[1].Unmapped != nil {
		t.Errorf("registries: %+v", got)
	}
	err = s.Update(ctx, func(w *Writer) error { return w.PutRegistrySeen(&model.RegistrySeen{Host: "h1", SeenAt: at}) })
	if !errors.Is(err, ErrInvalidRecord) {
		t.Errorf("registry without a prefix: %v", err)
	}
}

// TestActionDuplicatesAreLeftOut: an action that repeats another one is
// read only when asked for.
func TestActionDuplicatesAreLeftOut(t *testing.T) {
	s := newStore(t)
	at := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	var first int64
	mustUpdate(t, s, func(w *Writer) error {
		a := &model.Action{Host: "h1", At: at, Kind: model.ActionPPR, Source: "redfish/log", RawRef: "A", Fingerprint: "a"}
		if _, err := w.InsertAction(a); err != nil {
			return err
		}
		first = a.ID
		b := &model.Action{Host: "h1", At: at, Kind: model.ActionPPR, Source: "sel/sel", RawRef: "B", Fingerprint: "b"}
		if _, err := w.InsertAction(b); err != nil {
			return err
		}
		_, err := w.exec(`UPDATE action SET dup_of = ? WHERE id = ?`, a.ID, b.ID)
		return err
	})
	if acts, _ := s.Actions(ctx, ActionFilter{Host: "h1"}); len(acts) != 1 || acts[0].ID != first {
		t.Errorf("actions: %+v", acts)
	}
	if acts, _ := s.Actions(ctx, ActionFilter{Host: "h1", Duplicates: true}); len(acts) != 2 || acts[1].DupOf != first {
		t.Errorf("actions with duplicates: %+v", acts)
	}
}
