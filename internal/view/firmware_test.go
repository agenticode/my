package view

import (
	"testing"
	"time"

	"github.com/agenticode/my/internal/fwevent"
)

// TestMarksText: the marks of a DIMM in a few words, newest first, two of
// them and the number of the rest; marks taken back, steps reported twice
// and records that are no mark are left out.
func TestMarksText(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 11, d, 12, 0, 0, 0, time.UTC) }
	rec := func(ev fwevent.Name, d int, flags ...string) FirmwareRecord {
		return FirmwareRecord{At: day(d), Event: string(ev), Flags: flags}
	}
	for _, tt := range []struct {
		list []FirmwareRecord
		want string
	}{
		{nil, ""},
		{[]FirmwareRecord{rec(fwevent.CE, 1), rec(fwevent.CEThreshold, 2)}, ""},
		{[]FirmwareRecord{rec(fwevent.PPRDone, 3)}, "PPR ok 11-03"},
		{[]FirmwareRecord{rec(fwevent.PPRDone, 3), rec(fwevent.PredictedFailure, 9)}, "PFA 11-09, PPR ok 11-03"},
		{[]FirmwareRecord{rec(fwevent.TrainingFailure, 1), rec(fwevent.PPRFailed, 2), rec(fwevent.PPRDone, 3), rec(fwevent.PredictedFailure, 9)},
			"PFA 11-09, PPR ok 11-03 +2"},
		{[]FirmwareRecord{rec(fwevent.DeviceSparing, 4, "adddc"), rec(fwevent.DeviceSparing, 5, "sddc"), rec(fwevent.DeviceSparing, 6)},
			"device sparing 11-06, SDDC 11-05 +1"},
		{[]FirmwareRecord{rec(fwevent.PPRScheduled, 1), rec(fwevent.MirrorFailover, 2)}, "mirror 11-02, PPR pending 11-01"},
		{[]FirmwareRecord{{At: day(4), Event: string(fwevent.PredictedFailure), Withdrawn: true}, rec(fwevent.Disabled, 2)}, "disabled 11-02"},
		{[]FirmwareRecord{{At: day(4), Event: string(fwevent.Sparing), Duplicate: true}, rec(fwevent.SparingFailed, 2)}, "sparing failed 11-02"},
	} {
		if got := marksText(tt.list); got != tt.want {
			t.Errorf("%+v: %q, want %q", tt.list, got, tt.want)
		}
	}
}

// TestBootLetter: the record of a boot that weighs most gives the letter.
func TestBootLetter(t *testing.T) {
	rec := func(ev fwevent.Name) FirmwareRecord { return FirmwareRecord{Event: string(ev)} }
	for _, tt := range []struct {
		list []FirmwareRecord
		want string
	}{
		{nil, ""},
		{[]FirmwareRecord{rec(fwevent.CE), rec(fwevent.Cleared)}, ""},
		{[]FirmwareRecord{rec(fwevent.PPRDone), rec(fwevent.Sparing)}, "S"},
		{[]FirmwareRecord{rec(fwevent.PredictedFailure), rec(fwevent.PPRFailed)}, "E"},
		{[]FirmwareRecord{rec(fwevent.SparingFailed), rec(fwevent.TrainingFailure)}, "N"},
		{[]FirmwareRecord{rec(fwevent.TrainingFailure), rec(fwevent.MirrorFailover)}, "V"},
		{[]FirmwareRecord{rec(fwevent.MirrorFailover), rec(fwevent.Disabled)}, "D"},
		{[]FirmwareRecord{rec(fwevent.DeviceSparing)}, "S"},
		{[]FirmwareRecord{{Event: string(fwevent.Disabled), Withdrawn: true}, rec(fwevent.PPRDone)}, "P"},
		{[]FirmwareRecord{{Event: string(fwevent.Disabled), Duplicate: true}}, ""},
	} {
		if got := bootLetter(tt.list); got != tt.want {
			t.Errorf("%+v: %q, want %q", tt.list, got, tt.want)
		}
	}
}

// TestFirmwareBoots: a record belongs to the boot that ran at its time; a
// record before the first boot drawn belongs to none.
func TestFirmwareBoots(t *testing.T) {
	at := func(d int) time.Time { return time.Date(2026, 11, d, 0, 0, 0, 0, time.UTC) }
	// Out of order, as no caller promises an order.
	cols := []BootCol{{Index: 2, StartedAt: at(5)}, {Index: 1, StartedAt: at(3)}, {Index: 3, StartedAt: at(8)}}
	firmwareBoots(cols, []FirmwareRecord{
		{At: at(1), Event: string(fwevent.Disabled)},
		{At: at(4), Event: string(fwevent.PPRDone)},
		{At: at(5), Event: string(fwevent.PredictedFailure)},
		{At: at(9), Event: string(fwevent.TrainingFailure)},
	})
	if cols[0].Firmware != "A" || cols[1].Firmware != "P" || cols[2].Firmware != "N" {
		t.Errorf("letters %q %q %q", cols[0].Firmware, cols[1].Firmware, cols[2].Firmware)
	}
	firmwareBoots(nil, []FirmwareRecord{{At: at(1)}})
}
