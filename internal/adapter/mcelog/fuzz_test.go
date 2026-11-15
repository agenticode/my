package mcelog

import (
	"bytes"
	"testing"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/testutil"
)

// FuzzCollect feeds arbitrary input to the reader. Whatever the input, the
// reader must return a report, and every reading it emits must be one the
// ledger accepts.
func FuzzCollect(f *testing.F) {
	for _, name := range []string{"client-paper.txt", "client-x3850.txt"} {
		f.Add(testutil.ReadTestdata(f, "mcelog", name))
	}
	f.Add([]byte("Memory errors\nSOCKET 0 CHANNEL 1 DIMM 0\nDMI_NAME \"A1\" DMI_LOCATION \"NODE 0 CHANNEL 1 DIMM 0\"\ncorrected memory errors:\n\t4294967295 total\n\t7 in 3600\x00\nuncorrected memory errors:\n\t1 total\n\t1 in 2d\n"))
	f.Add([]byte("SOCKET 65535 CHANNEL any DIMM any\ncorrected memory errors:\n\t1 total\n\nSOCKET 65535 CHANNEL any DIMM 3\nDMI_LOCATION \"\"\nuncorrected memory errors:\n\t9 total\n"))
	f.Add([]byte("Per page corrected memory statistics:\nffffffffff000: total 3 seen \"1 in 24h\" offline-failed triggered\n\ndone\n"))
	f.Add([]byte("mcelog: client connect: No such file or directory\n"))
	f.Add([]byte("SOCKET 0 CHANNEL 0 DIMM 0\nDMI_NAME \"\nDMI_NAME \"\" DMI_LOCATION\ncorrected memory errors:\n\t1 in 0s\n\t-1 total\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		var c adapter.Collected
		rep := NewReader(bytes.NewReader(data), "fuzz", Options{Now: now}).Collect(ctx, &c)
		if rep.Status == "" || rep.Kind != Kind {
			t.Fatalf("report without status or kind: %+v", rep)
		}
		if rep.Skipped > rep.Records {
			t.Errorf("%d of %d records skipped", rep.Skipped, rep.Records)
		}
		if n := len(c.Counters); n > 0 && rep.Status != model.SourceOK && rep.Status != model.SourcePartial {
			t.Errorf("%d readings from a source with status %s", n, rep.Status)
		}
		if len(c.Events)+len(c.Actions)+len(c.Modules)+len(c.GPUs)+len(c.ECSs) != 0 {
			t.Errorf("records other than counter readings")
		}
		seen := map[[2]int]bool{}
		for _, s := range c.Counters {
			key := [2]int{s.MC, s.DIMMIndex}
			if seen[key] {
				t.Errorf("two readings of one counter: %+v", s)
			}
			seen[key] = true
			if !s.ObservedAt.Equal(now) || s.Source != Kind || s.RawRef == "" || !s.Placed() || s.MC != s.SocketId.V || s.MC < 0 {
				t.Errorf("reading the ledger would refuse or misplace: %+v", s)
			}
			if s.CE > 1<<32 || s.UE > 1<<32 {
				t.Errorf("count beyond what the daemon can hold: %+v", s)
			}
			for _, r := range []struct {
				n      model.Opt[uint64]
				window int64
			}{{s.RecentCE, int64(s.RecentCEWindow)}, {s.RecentUE, int64(s.RecentUEWindow)}} {
				if r.n.Valid != (r.window > 0) {
					t.Errorf("recent count and window do not go together: %+v", s)
				}
			}
			if s.DIMMIndex == -1 && (s.ChannelId.Valid || s.ChannelSlotId.Valid || s.Label != "" || s.RecentCE.Valid) {
				t.Errorf("the rest of a socket names a place: %+v", s)
			}
		}
	})
}
