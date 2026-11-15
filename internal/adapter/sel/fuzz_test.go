package sel

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
	for _, name := range []string{"ipmitool-elist.txt", "ipmitool-list.txt", "ipmitool-list-v.txt", "ipmitool-elist.csv", "ipmi-sel.txt", "sel.json",
		"ipmitool-elist-supermicro.txt", "ipmitool-elist-dell.txt", "ipmitool-elist-zone.txt", "ipmitool-elist-ampm.txt"} {
		f.Add(testutil.ReadTestdata(f, "sel", name))
	}
	f.Add(rawFixture(f))
	f.Add([]byte("   1 |  Pre-Init  |0000000042| Memory | Spare | Deasserted\n"))
	f.Add([]byte(`[{"raw":"0100 02 ff 2e f5 6a 33 00 04 0c 53 6f 30 ff 03"},{"time":"99999999999999999999"}]`))
	f.Add([]byte("SEL Record ID : 1\n Record Type : 02\n Timestamp : S+ 70/366 23:59:60\n Sensor Type : Memory\n Event Type : Sensor-specific Discrete\n Event Data : 3fffff\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		var c adapter.Collected
		rep := NewReader(bytes.NewReader(data), "fuzz", Options{}).Collect(ctx, &c)
		if rep.Status == "" || rep.Kind != Kind {
			t.Fatalf("report without status or kind: %+v", rep)
		}
		if rep.Skipped > rep.Records {
			t.Errorf("%d of %d records skipped", rep.Skipped, rep.Records)
		}
		usable := rep.Status == model.SourceOK || rep.Status == model.SourcePartial
		if n := len(c.Events) + len(c.Actions); n > 0 && !usable {
			t.Errorf("%d records from a source with status %s", n, rep.Status)
		}
		okTime := func(at time.Time) bool { y := at.UTC().Year(); return !at.IsZero() && y >= 1970 && y <= 9999 }
		for _, e := range c.Events {
			if !e.ErrorSeverity.Valid() || !e.ErrorScrubType.Valid() || e.Grade != model.GradeC || e.Fingerprint == "" || e.Source == "" ||
				e.RawRef == "" || e.Count != 1 || !okTime(e.ErrorDT) || e.ErrorType == "" {
				t.Errorf("event the ledger would refuse: %+v", e)
			}
		}
		for _, a := range c.Actions {
			if !a.Kind.Valid() || a.Fingerprint == "" || a.Source == "" || a.RawRef == "" || !okTime(a.At) {
				t.Errorf("action the ledger would refuse: %+v", a)
			}
		}
		// The same input read with a zone option gives the same records at
		// other times.
		var z adapter.Collected
		NewReader(bytes.NewReader(data), "fuzz", Options{Location: time.FixedZone("x", -7*3600)}).Collect(ctx, &z)
		if len(z.Events) != len(c.Events) || len(z.Actions) != len(c.Actions) {
			// A time near the ends of the range can fall outside it in another zone.
			if abs(len(z.Events)-len(c.Events))+abs(len(z.Actions)-len(c.Actions)) > rep.Records {
				t.Errorf("zone option changed the records: %d/%d events, %d/%d actions", len(z.Events), len(c.Events), len(z.Actions), len(c.Actions))
			}
		}
	})
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
