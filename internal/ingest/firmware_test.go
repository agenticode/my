package ingest

import (
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/fwevent"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// firmwareFake emits one firmware record of each kind the ledger keeps
// apart: an error, a step, a mark, and records that make neither.
func firmwareFake(at time.Time) adapter.Adapter {
	return &fake{kind: "redfish", fn: func(sink adapter.Sink) error {
		for i, n := range []fwevent.Name{fwevent.CEThreshold, fwevent.PPRDone, fwevent.PredictedFailure, fwevent.Presence,
			fwevent.ConfigState, fwevent.LogCleared} {
			r := &fwevent.Record{Event: n, At: at.Add(time.Duration(i) * time.Minute), Labels: []string{"DIMM A1"},
				Source: "redfish/log", RawRef: "entry " + string(n), Raw: `{"MessageId":"x"}`, Fingerprint: "fp " + string(n),
				Firmware: model.FirmwareRecord{Vendor: "Example", VendorID: "X" + string(n), Origin: model.OriginBMCLog,
					Mapping: model.MappingTable, MapRef: "example@1:X", Basis: "registry"}}
			if err := fwevent.Emit(sink, r); err != nil {
				return err
			}
		}
		return nil
	}}
}

// TestFirmwareRecordsInTheLedger: a presence and a configuration record
// give a firmware record row and no event; the others give their event
// or action with the row beside it; a second reading adds nothing.
func TestFirmwareRecordsInTheLedger(t *testing.T) {
	s := newStore(t)
	at := time.Date(2026, 11, 3, 2, 0, 0, 0, time.UTC)
	res := mustRun(t, s, at.Add(time.Hour), firmwareFake(at))
	if res.Events != 1 || res.Actions != 2 {
		t.Errorf("first run: %d events, %d actions", res.Events, res.Actions)
	}
	src := res.Sources[0]
	if src.Events != 1 || src.Actions != 2 || src.Samples != 3 {
		t.Errorf("source: %+v", src)
	}
	fw, err := s.FirmwareRecords(ctx, store.FirmwareFilter{Host: "node-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(fw) != 6 {
		t.Fatalf("%d firmware records, want 6", len(fw))
	}
	alone := 0
	for _, f := range fw {
		if f.EventID == 0 && f.ActionID == 0 {
			alone++
			switch f.Common {
			case "presence", "config-state", "log-cleared":
			default:
				t.Errorf("%s has no event or action", f.Common)
			}
		}
		if f.ScanID != res.Scan.ID || f.Host != "node-a" || f.Basis != "registry" {
			t.Errorf("firmware record %+v", f)
		}
	}
	if alone != 3 {
		t.Errorf("%d records alone, want 3", alone)
	}
	ev, _ := s.EventList(ctx, store.EventFilter{Host: "node-a"})
	if len(ev) != 1 || ev[0].ErrorSeverity != model.SevCE {
		t.Errorf("events: %+v", ev)
	}

	again := mustRun(t, s, at.Add(2*time.Hour), firmwareFake(at))
	if again.Events != 0 || again.Actions != 0 || again.Samples != 0 {
		t.Errorf("second run added %d events, %d actions, %d samples", again.Events, again.Actions, again.Samples)
	}
	if fw2, _ := s.FirmwareRecords(ctx, store.FirmwareFilter{}); len(fw2) != 6 {
		t.Errorf("%d firmware records after a second reading, want 6", len(fw2))
	}
	acts, _ := s.Actions(ctx, store.ActionFilter{Host: "node-a"})
	if len(acts) != 2 || acts[0].Kind != model.ActionPPR || acts[1].Kind != model.ActionPredictedFailure {
		t.Errorf("actions: %+v", acts)
	}
	counts, _ := s.RecordCounts(ctx)
	if counts["fw_record"] != 6 {
		t.Errorf("record counts: %v", counts)
	}
}
