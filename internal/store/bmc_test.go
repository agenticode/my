package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
)

func testBMC() *model.BMC {
	return &model.BMC{Host: "h1", ObservedAt: at(10 * time.Hour), Address: "bmc-h1.example.net", Manager: "Example BMC", Firmware: "2.14.0",
		BIOSVersion: "1.4.2", RedfishVersion: "1.17.0", BIOSRegistry: "BiosAttributeRegistryEX.v1_0_0", Live: true, Complete: model.Some(false),
		TLS: "fingerprint", Auth: "session", ClockOffset: model.Some(int64(-42)), LastReset: at(-72 * time.Hour),
		Classes: []model.BMCClass{{Class: "memory", State: "ok"}, {Class: "memory-metrics", State: "unsupported"}},
		Logs: []model.BMCLog{{ID: "SEL", Path: "/redfish/v1/Systems/1/LogServices/SEL", Entries: 120, Memory: 7, First: at(-240 * time.Hour), Last: at(9 * time.Hour),
			Total: model.Some(120), Policy: "WrapsWhenFull", Max: model.Some(1000)}},
		Notes:  []string{"the BMC stopped answering after 37 resources"},
		Source: "redfish/manifest", RawRef: "bmc:bmc-h1.example.net/manifest.json", Fingerprint: "b1"}
}

func testSetting() *model.BIOSSetting {
	return &model.BIOSSetting{Host: "h1", ObservedAt: at(10 * time.Hour), Name: "PatrolScrub", Value: "Enabled", HasValue: true,
		Pending: "Disabled", HasPending: true, Type: "enumeration", Registry: "BiosAttributeRegistryEX.v1_0_0", DisplayName: "Patrol Scrub",
		MenuPath: "./Memory", ReadOnly: model.Some(false), ResetRequired: model.Some(true), Allowed: []string{"Enabled", "Disabled"},
		Default: "Enabled", HasDefault: true, Source: "redfish/bios", RawRef: "bmc:x/redfish/v1/Systems/1/Bios/index.json"}
}

func testMemory() *model.BMCMemory {
	return &model.BMCMemory{Host: "h1", ObservedAt: at(10 * time.Hour), Label: "DIMM_A1", Slot: "", DeviceLocator: "DIMM_A1", ServiceLabel: "DIMM 1",
		ResourceID: "DIMM1", State: "Enabled", Health: "OK", ErrorCorrection: "MultiBitECC", OperatingSpeed: model.Some(2933),
		AllowedSpeeds: []int{2400, 2666, 2933, 3200}, RankSpare: model.Some(false), SpareDevice: model.Some(true),
		DeviceWidth: model.Some(4), DeviceWidthSource: "Oem.Example.DeviceWidth", Metrics: "ok",
		CECurrent: model.Some(int64(0)), UECurrent: model.Some(int64(0)), CELifetime: model.Some(int64(1234)), UELifetime: model.Some(int64(1)),
		AlarmCE: model.Some(true), AlarmUE: model.Some(false), CurrentSince: at(-72 * time.Hour),
		Source: "redfish/memory", RawRef: "bmc:x/redfish/v1/Systems/1/Memory/DIMM1/index.json", Raw: `{"Id":"DIMM1"}`, Fingerprint: "m1"}
}

func TestBMCRecordsRoundTrip(t *testing.T) {
	s := newStore(t)
	b, set, mem := testBMC(), testSetting(), testMemory()
	mustUpdate(t, s, func(w *Writer) error {
		if ok, err := w.InsertBMC(b); err != nil || !ok {
			t.Fatalf("InsertBMC: %v, %v", ok, err)
		}
		if err := w.PutBIOSSetting(set); err != nil {
			t.Fatalf("PutBIOSSetting: %v", err)
		}
		if ok, err := w.InsertBMCMemory(mem); err != nil || !ok {
			t.Fatalf("InsertBMCMemory: %v, %v", ok, err)
		}
		return nil
	})
	bmcs, err := s.BMCs(ctx, "h1")
	if err != nil || len(bmcs) != 1 {
		t.Fatalf("BMCs = %+v, %v", bmcs, err)
	}
	if !reflect.DeepEqual(&bmcs[0], b) {
		t.Errorf("bmc round trip\n got: %+v\nwant: %+v", bmcs[0], *b)
	}
	settings, err := s.BIOSSettings(ctx, "h1")
	if err != nil || len(settings) != 1 {
		t.Fatalf("BIOSSettings = %+v, %v", settings, err)
	}
	set.ID = settings[0].ID
	if !reflect.DeepEqual(&settings[0], set) {
		t.Errorf("setting round trip\n got: %+v\nwant: %+v", settings[0], *set)
	}
	mems, err := s.BMCMemory(ctx, "h1")
	if err != nil || len(mems) != 1 {
		t.Fatalf("BMCMemory = %+v, %v", mems, err)
	}
	if !reflect.DeepEqual(&mems[0], mem) {
		t.Errorf("memory round trip\n got: %+v\nwant: %+v", mems[0], *mem)
	}
	// A count of zero is a reported value.
	if !mems[0].CECurrent.Valid || !mems[0].HasCounts() {
		t.Errorf("a count of zero was read back as not reported")
	}

	// Another host is not listed, and no host means every host.
	if other, _ := s.BMCMemory(ctx, "h2"); len(other) != 0 {
		t.Errorf("records of h1 listed for h2")
	}
	if all, _ := s.BMCs(ctx, ""); len(all) != 1 {
		t.Errorf("BMCs of every host = %d", len(all))
	}
}

// Unset fields stay unset: a BMC that reports no count, no speed and no
// registry is not read back as zeros.
func TestBMCRecordsKeepWhatIsNotReported(t *testing.T) {
	s := newStore(t)
	mem := &model.BMCMemory{Host: "h1", ObservedAt: at(0), Label: "DIMM 4", Metrics: "not offered", Source: "redfish/memory", Fingerprint: "m4"}
	set := &model.BIOSSetting{Host: "h1", ObservedAt: at(0), Name: "MemTest", Source: "redfish/bios"}
	b := &model.BMC{Host: "h1", ObservedAt: at(0), Source: "redfish", Fingerprint: "b0"}
	mustUpdate(t, s, func(w *Writer) error {
		if _, err := w.InsertBMCMemory(mem); err != nil {
			return err
		}
		if _, err := w.InsertBMC(b); err != nil {
			return err
		}
		return w.PutBIOSSetting(set)
	})
	mems, _ := s.BMCMemory(ctx, "h1")
	if len(mems) != 1 || mems[0].HasCounts() || mems[0].OperatingSpeed.Valid || mems[0].AlarmCE.Valid || mems[0].AllowedSpeeds != nil || !mems[0].CurrentSince.IsZero() {
		t.Errorf("memory = %+v", mems)
	}
	settings, _ := s.BIOSSettings(ctx, "h1")
	if len(settings) != 1 || settings[0].HasValue || settings[0].HasPending || settings[0].ReadOnly.Valid || settings[0].Allowed != nil || settings[0].LowerBound.Valid {
		t.Errorf("setting = %+v", settings)
	}
	bmcs, _ := s.BMCs(ctx, "h1")
	if len(bmcs) != 1 || bmcs[0].Live || bmcs[0].Complete.Valid || bmcs[0].ClockOffset.Valid || !bmcs[0].LastReset.IsZero() || bmcs[0].Classes != nil {
		t.Errorf("bmc = %+v", bmcs)
	}
}

func TestBMCRecordsReadAgain(t *testing.T) {
	s := newStore(t)
	mustUpdate(t, s, func(w *Writer) error {
		for i := 0; i < 2; i++ {
			b, mem := testBMC(), testMemory()
			okB, err := w.InsertBMC(b)
			if err != nil {
				return err
			}
			okM, err := w.InsertBMCMemory(mem)
			if err != nil {
				return err
			}
			if okB != (i == 0) || okM != (i == 0) || b.ID == 0 || mem.ID == 0 {
				t.Errorf("round %d: inserted %v, %v, ids %d, %d", i, okB, okM, b.ID, mem.ID)
			}
		}
		// A setting read again replaces the one that is there.
		set := testSetting()
		if err := w.PutBIOSSetting(set); err != nil {
			return err
		}
		set.Value, set.Pending, set.HasPending = "Disabled", "", false
		return w.PutBIOSSetting(set)
	})
	settings, _ := s.BIOSSettings(ctx, "h1")
	if len(settings) != 1 || settings[0].Value != "Disabled" || settings[0].HasPending {
		t.Errorf("settings = %+v", settings)
	}
	// The same setting at a later time is a second reading.
	mustUpdate(t, s, func(w *Writer) error {
		set := testSetting()
		set.ObservedAt = at(20 * time.Hour)
		return w.PutBIOSSetting(set)
	})
	if settings, _ = s.BIOSSettings(ctx, "h1"); len(settings) != 2 || !settings[0].ObservedAt.Before(settings[1].ObservedAt) {
		t.Errorf("settings over time = %+v", settings)
	}

	// A module is tied to a slot by the name its source uses.
	labels, err := s.BMCMemoryLabels(ctx, "h1")
	if err != nil || !reflect.DeepEqual(labels, []string{"DIMM_A1"}) {
		t.Fatalf("labels = %v, %v", labels, err)
	}
	mustUpdate(t, s, func(w *Writer) error { return w.SetBMCMemorySlot("h1", "DIMM_A1", "CPU0_A1") })
	if mems, _ := s.BMCMemory(ctx, "h1"); len(mems) != 1 || mems[0].Slot != "CPU0_A1" {
		t.Errorf("memory after the slot was set = %+v", mems)
	}
}

func TestBMCRecordsValidation(t *testing.T) {
	s := newStore(t)
	refused := func(name string, fn func(w *Writer) error) {
		t.Helper()
		if err := s.Update(ctx, fn); !errors.Is(err, ErrInvalidRecord) {
			t.Errorf("%s: err = %v, want ErrInvalidRecord", name, err)
		}
	}
	for name, change := range map[string]func(*model.BMC){
		"no host":        func(b *model.BMC) { b.Host = "" },
		"no fingerprint": func(b *model.BMC) { b.Fingerprint = "" },
		"no source":      func(b *model.BMC) { b.Source = "" },
		"no time":        func(b *model.BMC) { b.ObservedAt = time.Time{} },
		"reset in 1601":  func(b *model.BMC) { b.LastReset = time.Date(1601, 1, 1, 0, 0, 0, 0, time.UTC) },
	} {
		b := testBMC()
		change(b)
		refused("bmc: "+name, func(w *Writer) error { _, err := w.InsertBMC(b); return err })
	}
	for name, change := range map[string]func(*model.BIOSSetting){
		"no host":    func(b *model.BIOSSetting) { b.Host = "" },
		"no name":    func(b *model.BIOSSetting) { b.Name = "" },
		"no source":  func(b *model.BIOSSetting) { b.Source = "" },
		"no time":    func(b *model.BIOSSetting) { b.ObservedAt = time.Time{} },
		"long value": func(b *model.BIOSSetting) { b.Value = strings.Repeat("x", 5000) },
		"long name":  func(b *model.BIOSSetting) { b.Name = strings.Repeat("n", 600) },
	} {
		b := testSetting()
		change(b)
		refused("setting: "+name, func(w *Writer) error { return w.PutBIOSSetting(b) })
	}
	for name, change := range map[string]func(*model.BMCMemory){
		"no host":        func(m *model.BMCMemory) { m.Host = "" },
		"no label":       func(m *model.BMCMemory) { m.Label = "" },
		"no fingerprint": func(m *model.BMCMemory) { m.Fingerprint = "" },
		"no time":        func(m *model.BMCMemory) { m.ObservedAt = time.Time{} },
		"negative count": func(m *model.BMCMemory) { m.CELifetime = model.Some(int64(-1)) },
		"bad since":      func(m *model.BMCMemory) { m.CurrentSince = time.Date(1601, 1, 1, 0, 0, 0, 0, time.UTC) },
	} {
		m := testMemory()
		change(m)
		refused("memory: "+name, func(w *Writer) error { _, err := w.InsertBMCMemory(m); return err })
	}
	for _, table := range []string{"bmc", "bios_setting", "bmc_memory"} {
		var n int
		if err := s.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s holds %d rows after refused records (%v)", table, n, err)
		}
	}
}

// A ledger of schema version 5 gets the three tables and keeps its rows.
func TestMigrateVersion5(t *testing.T) {
	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "v5.db")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		migs[0].sql, migs[1].sql, migs[2].sql, migs[3].sql, migs[4].sql,
		`PRAGMA application_id = 1297697863`,
		`PRAGMA user_version = 5`,
		`INSERT INTO host (name, first_seen, last_seen) VALUES ('h1', '2026-11-01T00:00:00.000000Z', '2026-11-01T00:00:00.000000Z')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("build version 5 ledger: %v\n%s", err, q)
		}
	}
	db.Close()

	if _, err := Open(ctx, p, ReadOnly); !errors.Is(err, ErrNeedsMigration) {
		t.Fatalf("read-only open of a version 5 ledger: err = %v, want ErrNeedsMigration", err)
	}
	s, err := Open(ctx, p, ReadWrite)
	if err != nil {
		t.Fatalf("open for writing: %v", err)
	}
	defer s.Close()
	if v, _ := s.Version(ctx); v != SchemaVersion() || v < 6 {
		t.Fatalf("version after migration = %d", v)
	}
	if hosts, err := s.Hosts(ctx); err != nil || len(hosts) != 1 {
		t.Errorf("hosts after migration = %v, %v", hosts, err)
	}
	mustUpdate(t, s, func(w *Writer) error {
		if _, err := w.InsertBMC(testBMC()); err != nil {
			return err
		}
		if _, err := w.InsertBMCMemory(testMemory()); err != nil {
			return err
		}
		return w.PutBIOSSetting(testSetting())
	})
	if mems, err := s.BMCMemory(ctx, "h1"); err != nil || len(mems) != 1 {
		t.Errorf("memory after migration = %v, %v", mems, err)
	}
}
