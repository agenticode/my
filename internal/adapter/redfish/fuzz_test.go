package redfish

import (
	"bytes"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/testutil"
)

// FuzzCollect feeds arbitrary input to the reader. Whatever the input, the
// reader must return a report, and every record it emits must be one the
// ledger accepts.
func FuzzCollect(f *testing.F) {
	for _, name := range []string{"sel-log.json", "registry-log.json", "vendor-text-log.json", "memory-expanded.json",
		"bmc-tree/redfish/v1/Systems/system/index.json", "bmc-tree/redfish/v1/Systems/system/Memory/dimm1/index.json",
		"bmc-tree/redfish/v1/Systems/system/LogServices/EventLog/Entries/index.json"} {
		f.Add(testutil.ReadTestdata(f, "redfish", name))
	}
	f.Add([]byte(`{"EntryType":"SEL","SensorType":"Memory","Created":"9999-12-31T23:59:59-12:00","MessageId":"0xef04ffff","Links":{"OriginOfCondition":{"@odata.id":"/Memory/%zz"}}}`))
	f.Add([]byte(`[{"DeviceLocator":"","CapacityMiB":1e99,"MemoryLocation":{"Socket":-1,"Channel":"2","Slot":70000},"Status":{"Health":"Critical","Conditions":[1,{"Message":7}]}}]`))
	f.Add([]byte(`{"@odata.type":"#LogEntry.v1.LogEntry","MessageId":"OpenBMC.0.1.MemoryParityNotKnown","MessageArgs":[null,{},"x"],"Created":"2026-01-01T00:00:00Z"}`))
	// BIOS settings with their registry, the error counts of a module,
	// and the manifest of a collection.
	for _, name := range []string{"dell-shaped/redfish/v1/Systems/System.Embedded.1/Bios/index.json",
		"dell-shaped/redfish/v1/Systems/System.Embedded.1/Bios/BiosRegistry/index.json",
		"dell-shaped/redfish/v1/Systems/System.Embedded.1/Memory/DIMM.Socket.A1/MemoryMetrics/index.json",
		"lenovo-shaped/redfish/v1/Systems/1/Bios/Pending/index.json"} {
		f.Add(testutil.ReadTestdata(f, "redfish", name))
	}
	f.Add([]byte(`[{"@odata.id":"/s/1/Bios","AttributeRegistry":"R","Attributes":{"MemA":1e400,"MemB":{"x":[]},"MemC":null,"DimmD":"\u0000"},"@Redfish.Settings":{"SettingsObject":{"@odata.id":"/s/1/Bios"}}},` +
		`{"Id":"R","RegistryEntries":{"Attributes":[{"AttributeName":"MemA","Type":7,"Value":[1,{"ValueName":2}],"LowerBound":"x","ReadOnly":"no"},7,{"AttributeName":""}]}}]`))
	f.Add([]byte(`[{"@odata.id":"/m/1","DeviceLocator":"A1","CapacityMiB":4096,"SerialNumber":"S1","AllowedSpeedsMHz":[-1,"x",1e99],"Metrics":{"@odata.id":"/m/1/MemoryMetrics"},"IsRankSpareEnabled":"yes"},` +
		`{"@odata.id":"/m/1/MemoryMetrics","@odata.type":"#MemoryMetrics.v1_7_0.MemoryMetrics","LifeTime":{"CorrectableECCErrorCount":-5,"UncorrectableECCErrorCount":1e30},"CurrentPeriod":7,"HealthData":{"AlarmTrips":[]}}]`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var c adapter.Collected
		rep := NewReader(bytes.NewReader(data), "fuzz", Options{Now: now}).Collect(ctx, &c)
		if rep.Status == "" || rep.Kind != Kind {
			t.Fatalf("report without status or kind: %+v", rep)
		}
		usable := rep.Status == model.SourceOK || rep.Status == model.SourcePartial
		if n := len(c.Events) + len(c.Actions) + len(c.Modules); n > 0 && !usable {
			t.Errorf("%d records from a source with status %s", n, rep.Status)
		}
		okTime := func(at time.Time) bool { y := at.UTC().Year(); return !at.IsZero() && y >= 1970 && y <= 9999 }
		for _, e := range c.Events {
			if !e.ErrorSeverity.Valid() || !e.ErrorScrubType.Valid() || !e.Grade.Valid() || e.Fingerprint == "" || e.Source == "" ||
				e.RawRef == "" || e.Count != 1 || !okTime(e.ErrorDT) || e.ErrorType == "" {
				t.Errorf("event the ledger would refuse: %+v", e)
			}
		}
		for _, a := range c.Actions {
			if !a.Kind.Valid() || a.Fingerprint == "" || a.Source == "" || a.RawRef == "" || !okTime(a.At) {
				t.Errorf("action the ledger would refuse: %+v", a)
			}
		}
		if n := len(c.BMCs) + len(c.Settings) + len(c.Memory); n > 0 && !usable {
			t.Errorf("%d records about a BMC from a source with status %s", n, rep.Status)
		}
		for _, b := range c.BMCs {
			if b.Fingerprint == "" || b.Source == "" || !okTime(b.ObservedAt) || (!b.LastReset.IsZero() && !okTime(b.LastReset)) {
				t.Errorf("BMC record the ledger would refuse: %+v", b)
			}
		}
		for _, s := range c.Settings {
			if s.Name == "" || len(s.Name) > 512 || len(s.Value) > maxSettingValue || len(s.Pending) > maxSettingValue || s.Source == "" || !okTime(s.ObservedAt) {
				t.Errorf("setting the ledger would refuse: %+v", s)
			}
		}
		for _, m := range c.Memory {
			if m.Label == "" || m.Fingerprint == "" || m.Source == "" || !okTime(m.ObservedAt) || (!m.CurrentSince.IsZero() && !okTime(m.CurrentSince)) {
				t.Errorf("module reading the ledger would refuse: %+v", m)
			}
			for _, n := range []model.Opt[int64]{m.CECurrent, m.UECurrent, m.CELifetime, m.UELifetime} {
				if n.Valid && n.V < 0 {
					t.Errorf("a count below zero: %+v", m)
				}
			}
		}
		for _, m := range c.Modules {
			if m.Locator == "" || !okTime(m.ObservedAt) || m.DIMM.SizeBytes < 0 {
				t.Errorf("module without a slot name or time: %+v", m)
			}
			if m.Socket.Valid != m.Channel.Valid || m.Socket.Valid != m.Position.Valid {
				t.Errorf("module with part of a place: %+v", m)
			}
		}
	})
}
