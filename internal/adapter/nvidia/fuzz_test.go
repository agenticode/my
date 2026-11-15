package nvidia

import (
	"bytes"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/testutil"
)

// FuzzCollect feeds arbitrary input to the reader. Whatever the input, the
// reader must return a report, and every snapshot it emits must be one the
// ledger accepts.
func FuzzCollect(f *testing.F) {
	for _, name := range []string{"nvidia-smi-q.txt", "nvidia-smi-q.xml", "nvidia-smi-q-legacy.txt"} {
		f.Add(testutil.ReadTestdata(f, "nvidia", name))
	}
	f.Add([]byte("Timestamp : Mon Jan  1 00:00:00 1601\nGPU 00000000:3B:00.0\n    Remapped Rows\n        Pending : Yes\n      Odd indent : 1\n                    Deep : 2\n"))
	f.Add([]byte(`<nvidia_smi_log><gpu id="FFFFFFFF:FF:1F.7"><gpu><uuid>GPU-1</uuid></gpu><ecc_errors><volatile><dram_correctable>99999999999999999999</dram_correctable></volatile></ecc_errors></gpu></nvidia_smi_log>`))
	f.Add([]byte("<nvidia_smi_log><timestamp>x</timestamp>" + string(bytes.Repeat([]byte("<a>"), 40)) + "</nvidia_smi_log>"))

	now := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, data []byte) {
		var c adapter.Collected
		rep := NewReader(bytes.NewReader(data), "fuzz", Options{Now: now}).Collect(ctx, &c)
		if rep.Status == "" || rep.Kind != Kind {
			t.Fatalf("report without status or kind: %+v", rep)
		}
		if rep.Skipped > rep.Records {
			t.Errorf("%d of %d records skipped", rep.Skipped, rep.Records)
		}
		if n := len(c.GPUs); n > 0 && rep.Status != model.SourceOK && rep.Status != model.SourcePartial {
			t.Errorf("%d snapshots from a source with status %s", n, rep.Status)
		}
		seen := map[string]bool{}
		for _, g := range c.GPUs {
			y := g.ObservedAt.UTC().Year()
			if g.Kind != model.GPUSnapshot || g.Fingerprint == "" || g.Source != Kind || g.RawRef == "" || y < 1970 || y > 9999 ||
				g.TimeQuality != model.TimeExact || !g.GPUIndex.Valid || (g.GPUUUID == "" && g.GPUSerial == "" && g.PCIBus == "") {
				t.Errorf("snapshot the ledger would refuse: %+v", g)
			}
			for _, n := range []model.Opt[int]{g.DRAMCorrectable, g.DRAMUncorrectable, g.SRAMCorrectable, g.SRAMUncorrectable, g.RemapCorrectable,
				g.RemapUncorrectable, g.BankMax, g.BankNone, g.RetiredSBE, g.RetiredDBE} {
				if n.Valid && n.V < 0 {
					t.Errorf("negative count in %+v", g)
				}
			}
			seen[g.Fingerprint] = true
		}
		_ = seen
	})
}
