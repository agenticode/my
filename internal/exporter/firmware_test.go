package exporter

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/redfish"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/testutil"
)

// TestFirmwareMetrics: a case with firmware records: the firmware events
// by common event, the repairs of the new types, and the records a reader
// did not understand.
func TestFirmwareMetrics(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	a := redfish.New(testutil.Testdata(t, "redfish", "standard-log.json"), redfish.Options{Now: now})
	if _, err := ingest.Run(ctx, s, []adapter.Adapter{a}, ingest.Options{Host: "h1", Now: now}); err != nil {
		t.Fatal(err)
	}
	set, _ := hostSet(t, s)
	got, x := text(t, set)
	testutil.Golden(t, "firmware.prom", []byte(got))
	for series, want := range map[string]float64{
		`my_dimm_firmware_events_total{host="h1",slot="DIMM_A4",serial="",event="ppr-scheduled"}`:     1,
		`my_dimm_firmware_events_total{host="h1",slot="DIMM_A5",serial="",event="ppr-failed"}`:        1,
		`my_dimm_firmware_events_total{host="h1",slot="DIMM_A10",serial="",event="training-failure"}`: 1,
		`my_dimm_firmware_events_total{host="h1",slot="",serial="",event="device-sparing"}`:           1,
		`my_dimm_repairs_total{host="h1",slot="DIMM_A5",type="ppr"}`:                                  1,
	} {
		if v, ok := x.Series[series]; !ok || v != want {
			t.Errorf("%s = %v (present %v), want %v", series, v, ok, want)
		}
	}
	// The repair that waits for the next boot is no repair yet.
	if strings.Contains(got, `my_dimm_repairs_total{host="h1",slot="DIMM_A4"`) {
		t.Error("a scheduled repair is counted")
	}
}
