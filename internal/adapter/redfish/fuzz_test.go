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
