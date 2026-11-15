package ingest

import (
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
)

// What a BMC says beyond events reaches the ledger under the host of the
// run, and its modules are tied to the slots its inventory named.
func TestBMCRecords(t *testing.T) {
	s := newStore(t)
	when := time.Date(2026, 11, 16, 3, 0, 0, 0, time.UTC)
	dump := &fake{kind: "redfish", fn: func(sink adapter.Sink) error {
		for _, name := range []string{"DIMM_A1", "DIMM_A2"} {
			m := &model.Module{ObservedAt: when, Locator: name, Populated: true, Source: "redfish", RawRef: "dump#" + name}
			m.DIMM.Manufacturer, m.DIMM.PartNumber, m.DIMM.Serial = "Example", "EX-32G", "SN-"+name
			if err := sink.Module(m); err != nil {
				return err
			}
		}
		if err := sink.BMC(&model.BMC{ObservedAt: when, Address: "10.0.0.5", Firmware: "2.14.0", Source: "redfish/manifest", Fingerprint: "bmc"}); err != nil {
			return err
		}
		if err := sink.BIOSSetting(&model.BIOSSetting{ObservedAt: when, Name: "PatrolScrub", Value: "Enabled", HasValue: true, Source: "redfish/bios"}); err != nil {
			return err
		}
		// The BMC writes the name of the first slot with a space, the
		// second as the inventory does, and names a third no inventory has.
		for label, ce := range map[string]int64{"DIMM A1": 7, "DIMM_A2": 0, "DIMM_Z9": 1} {
			if err := sink.BMCMemory(&model.BMCMemory{ObservedAt: when, Label: label, Metrics: "ok", CELifetime: model.Some(ce),
				Source: "redfish/memory", RawRef: "dump#" + label, Fingerprint: "mem " + label}); err != nil {
				return err
			}
		}
		return nil
	}}
	res, err := Run(ctx, s, []adapter.Adapter{dump}, Options{Host: "node-a", Now: when})
	if err != nil {
		t.Fatal(err)
	}
	if res.Samples != 5 || res.Sources[0].Samples != 5 || res.Sources[0].Status != model.SourceOK {
		t.Errorf("samples = %d, source %+v", res.Samples, res.Sources[0])
	}
	bmcs, err := s.BMCs(ctx, "node-a")
	if err != nil || len(bmcs) != 1 || bmcs[0].Host != "node-a" || bmcs[0].ScanID != res.Scan.ID || bmcs[0].Address != "10.0.0.5" {
		t.Errorf("bmc = %+v, %v", bmcs, err)
	}
	settings, err := s.BIOSSettings(ctx, "node-a")
	if err != nil || len(settings) != 1 || settings[0].Host != "node-a" || settings[0].Value != "Enabled" {
		t.Errorf("settings = %+v, %v", settings, err)
	}
	mems, err := s.BMCMemory(ctx, "node-a")
	if err != nil || len(mems) != 3 {
		t.Fatalf("memory = %+v, %v", mems, err)
	}
	slots := map[string]string{}
	for _, m := range mems {
		slots[m.Label] = m.Slot
	}
	if slots["DIMM A1"] != "DIMM_A1" || slots["DIMM_A2"] != "DIMM_A2" || slots["DIMM_Z9"] != "" {
		t.Errorf("slots = %v", slots)
	}

	// The same dump again adds nothing.
	res, err = Run(ctx, s, []adapter.Adapter{dump}, Options{Host: "node-a", Now: when})
	if err != nil {
		t.Fatal(err)
	}
	// The setting is put again; the BMC and the modules are known.
	if res.Samples != 1 {
		t.Errorf("second run added %d samples", res.Samples)
	}
	if mems, _ := s.BMCMemory(ctx, "node-a"); len(mems) != 3 {
		t.Errorf("memory after the second run = %d rows", len(mems))
	}
	if settings, _ := s.BIOSSettings(ctx, "node-a"); len(settings) != 1 {
		t.Errorf("settings after the second run = %d rows", len(settings))
	}

	// A record the ledger cannot take is skipped and named, and the
	// source is read on.
	bad := &fake{kind: "redfish", fn: func(sink adapter.Sink) error {
		if err := sink.BMCMemory(&model.BMCMemory{ObservedAt: when, Label: "", Source: "redfish/memory", RawRef: "dump#nameless", Fingerprint: "x"}); err != nil {
			return err
		}
		return sink.BIOSSetting(&model.BIOSSetting{ObservedAt: when, Name: "MemTest", Value: "Disabled", HasValue: true, Source: "redfish/bios"})
	}}
	res, err = Run(ctx, s, []adapter.Adapter{bad}, Options{Host: "node-a", Now: when})
	if err != nil {
		t.Fatal(err)
	}
	if src := res.Sources[0]; src.Status != model.SourcePartial || src.Skipped != 1 || src.Samples != 1 {
		t.Errorf("source with a record that is refused: %+v", src)
	}
}
