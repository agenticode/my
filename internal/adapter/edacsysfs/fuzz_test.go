package edacsysfs

import (
	"math"
	"testing"
	"testing/fstest"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
)

// FuzzCollect reads a counter tree whose files hold arbitrary text. The
// tree of a live host is written by the kernel; the tree of a copy
// (--root, an sos report) is whatever was put there. Whatever the files
// hold, every sample the adapter hands on is one the ledger takes, and a
// file that cannot be used is counted as skipped.
func FuzzCollect(f *testing.F) {
	f.Add("Intel_10nm Socket#0 IMC#0", "90700", "4", "1", "CPU_SrcID#0_MC#0_Chan#0_DIMM#0", "channel 0 slot 0 ", "32768", "0", "Registered-DDR4", uint8(0))
	f.Add("F19h_M10h", "270700", "2", "0", "mc#0csrow#2channel#1", "csrow 2 channel 1 ", "65536", "1", "Registered-DDR5", uint8(1))
	f.Add("", "18446744073709551615", "18446744073709551615", "9223372036854775808", "", "", "18446744073709551615", "-1", "", uint8(2))
	f.Add("x", "9223372036854775807", "9223372036854775807", "0", "a\x00b", "channel 99999999999999999999 slot -1", "1099511627776", "x", "\xff", uint8(4))
	f.Add("x", " 12 ", "+3", "0x10", "\n", "0:0", "", "", "", uint8(8))
	f.Add("x", "1", "1", "1", "label", "channel:0 slot:1", "8192", "1", "Unbuffered-DDR3", uint8(16))
	f.Add("x", "1", "1", "1", "label", "0", "8192", "1", "t", uint8(32))
	f.Add("x", "1", "1", "1", "label", "0", "8192", "1", "t", uint8(64))

	f.Fuzz(func(t *testing.T, mcName, since, ce, ue, label, location, size, noinfo, memType string, shape uint8) {
		// The shape bits choose what the tree looks like around the files.
		dimm := "dimm0"
		if shape&1 != 0 {
			dimm = "rank25"
		}
		mc := "mc0"
		if shape&2 != 0 {
			mc = "mc99999999999999999999"
		}
		tree := fstest.MapFS{
			"mc/" + mc + "/mc_name":                        {Data: []byte(mcName)},
			"mc/" + mc + "/seconds_since_reset":            {Data: []byte(since)},
			"mc/" + mc + "/ce_noinfo_count":                {Data: []byte(noinfo)},
			"mc/" + mc + "/ue_noinfo_count":                {Data: []byte(ue)},
			"mc/" + mc + "/" + dimm + "/dimm_ce_count":     {Data: []byte(ce)},
			"mc/" + mc + "/" + dimm + "/dimm_ue_count":     {Data: []byte(ue)},
			"mc/" + mc + "/" + dimm + "/dimm_label":        {Data: []byte(label)},
			"mc/" + mc + "/" + dimm + "/dimm_location":     {Data: []byte(location)},
			"mc/" + mc + "/" + dimm + "/size":              {Data: []byte(size)},
			"mc/" + mc + "/" + dimm + "/dimm_mem_type":     {Data: []byte(memType)},
			"mc/" + mc + "/" + dimm + "/dimm_dev_type":     {Data: []byte(memType)},
			"mc/" + mc + "/" + dimm + "/dimm_edac_mode":    {Data: []byte(label)},
			"mc/power/runtime_status":                      {Data: []byte("unsupported\n")},
			"mc/" + mc + "/power/autosuspend_delay_ms":     {Data: []byte(since)},
			"mc/" + mc + "/" + dimm + "/power/control":     {Data: []byte("auto\n")},
			"mc/" + mc + "/" + dimm + "/dimm_ce_count.bak": {Data: []byte(ce)},
		}
		if shape&4 != 0 {
			// A second controller with the layout of old kernels only.
			tree["mc/mc1/csrow0/ce_count"] = &fstest.MapFile{Data: []byte(ce)}
			tree["mc/mc1/mc_name"] = &fstest.MapFile{Data: []byte(mcName)}
		}
		if shape&8 != 0 {
			delete(tree, "mc/"+mc+"/"+dimm+"/dimm_ue_count")
		}
		if shape&16 != 0 {
			// A second DIMM whose counters are the same files.
			tree["mc/"+mc+"/dimm1/dimm_ce_count"] = &fstest.MapFile{Data: []byte(ue)}
			tree["mc/"+mc+"/dimm1/dimm_ue_count"] = &fstest.MapFile{Data: []byte(ce)}
		}
		if shape&32 != 0 {
			delete(tree, "mc/"+mc+"/seconds_since_reset")
			delete(tree, "mc/"+mc+"/ue_noinfo_count")
		}
		if shape&64 != 0 {
			// A file where a controller directory is expected.
			tree = fstest.MapFS{"mc/mc0": {Data: []byte(ce)}, "mc/mc2/dimm0/dimm_ce_count": {Data: []byte(ce)}, "mc/mc2/dimm0/dimm_ue_count": {Data: []byte(ue)}}
		}

		var c adapter.Collected
		rep := New(tree, "mc", "fuzz", Options{Now: now}).Collect(ctx, &c)
		if rep.Status == "" || rep.Kind != Kind {
			t.Fatalf("report without status or kind: %+v", rep)
		}
		if rep.SinkErr != nil {
			t.Fatalf("the collecting sink takes every record, yet: %v", rep.SinkErr)
		}
		if rep.Skipped > rep.Records {
			t.Errorf("%d of %d records skipped", rep.Skipped, rep.Records)
		}
		if len(c.Counters)+rep.Skipped != rep.Records {
			t.Errorf("%d records: %d samples and %d skipped", rep.Records, len(c.Counters), rep.Skipped)
		}
		if n := len(c.Counters); n > 0 && rep.Status != model.SourceOK && rep.Status != model.SourcePartial {
			t.Errorf("%d samples from a source with status %s", n, rep.Status)
		}
		for _, s := range c.Counters {
			if !s.ObservedAt.Equal(now) || s.Source != Kind || s.RawRef == "" {
				t.Errorf("incomplete sample: %+v", *s)
			}
			// What the ledger refuses, or would misread.
			if s.CE > math.MaxInt64 || s.UE > math.MaxInt64 {
				t.Errorf("counts the ledger refuses: %+v", *s)
			}
			if v := s.SecondsSinceReset; v.Valid && (v.V < 0 || v.V > maxSinceReset) {
				t.Errorf("counters that run for %d seconds: %+v", v.V, *s)
			}
			if s.MC < 0 || s.DIMMIndex < -1 {
				t.Errorf("negative index: %+v", *s)
			}
			if v := s.SizeMB; v.Valid && v.V < 0 {
				t.Errorf("negative size: %+v", *s)
			}
			for _, text := range []string{s.MCName, s.Label, s.Location, s.MemType, s.DevType, s.EdacMode} {
				if len(text) > maxText {
					t.Errorf("a text of %d bytes", len(text))
				}
			}
		}
	})
}
