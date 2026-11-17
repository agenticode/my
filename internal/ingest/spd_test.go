package ingest

import (
	"testing"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
)

// An SPD reading an adapter gives is kept with the host and the scan of
// the run, counted as a sample of its source; a second run with the same
// bytes keeps one row.
func TestSPDThroughTheSink(t *testing.T) {
	s := newStore(t)
	spd := &fake{kind: "spd", fn: func(sink adapter.Sink) error {
		return sink.SPD(&model.SPDReading{Origin: "/sys/bus/i2c/devices/0-0050/eeprom", Bus: "i2c-0", Address: 0x50, Driver: "ee1004",
			Form: model.SPDFormEEPROM, State: "ok", Raw: []byte{0x23, 0x12, 0x0C}, LastSeen: intelNow})
	}}
	var scans []int64
	for range 2 {
		res, err := Run(ctx, s, []adapter.Adapter{spd}, Options{Host: "r12-n034", Kind: model.ScanLive, Now: intelNow})
		if err != nil {
			t.Fatal(err)
		}
		scans = append(scans, res.Scan.ID)
		if len(res.Sources) != 1 || res.Sources[0].Samples != 1 {
			t.Fatalf("sources %+v", res.Sources)
		}
	}
	got, err := s.SPDReadings(ctx, "r12-n034")
	if err != nil || len(got) != 1 || got[0].Host != "r12-n034" || got[0].ScanID != scans[1] {
		t.Fatalf("readings %+v, %v (scans %v)", got, err, scans)
	}
}
