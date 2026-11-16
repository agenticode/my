package klog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
)

// A kernel log is whatever a machine in trouble managed to write. The
// adapter must get through any of it and never emit a record the ledger
// would refuse for its time.
func FuzzCollect(f *testing.F) {
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "testdata", "klog"))
	if err != nil {
		f.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		f.Fatal(err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(root, e.Name()))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(string(b), true)
	}
	f.Add("[18446744073709551615.999999] EDAC MC0: 1 CE x on y (page:0x0 offset:0x0 grain:1 syndrome:0x0)\n", false)
	f.Add("{1}[Hardware Error]:  Error 0, type: corrected\n{1}[Hardware Error]:   section_type: memory error\n", true)
	// What the next boot prints of the boot before: the boot error region
	// and a record left in a bank (carried.go).
	f.Add(bertLines, false)
	f.Add(mceText(4.3, true, 0xbe00000000010090, 0, 1794798147)+later(60), false)
	f.Add("9,9,99999999999999999999,-;EDAC MC0: 1 UE on a (b page:0xffffffffffffffff offset:0xfff grain:9)\n", false)
	f.Add("[9999999999.000000] EDAC MC0: 1 CE x on y (page:0x1 offset:0x0 grain:1 syndrome:0x0)\n", true)
	f.Add(`{"MESSAGE":"EDAC MC0: 1 CE x on y (page:0x1 offset:0x0 grain:1 syndrome:0x0)","_TRANSPORT":"kernel","__REALTIME_TIMESTAMP":"9223372036854775807"}`+"\n", false)
	f.Add("[99999999999.000000] NVRM: Xid (PCI:0000:3b:00): 9999, pid=1, name=x, text\n[1.000000] NVRM: GPU Board Serial Number: 1\n", true)
	f.Fuzz(func(t *testing.T, text string, anchored bool) {
		opts := Options{}
		if anchored {
			opts.CapturedAt = time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
		}
		var c adapter.Collected
		rep := NewReader(strings.NewReader(text), "fuzz", opts).Collect(context.Background(), &c)
		if rep.Status == "" {
			t.Error("no status")
		}
		if rep.Skipped > rep.Records {
			t.Errorf("%d of %d records skipped", rep.Skipped, rep.Records)
		}
		for _, e := range c.Events {
			if y := e.ErrorDT.UTC().Year(); y < 1970 || y > 9999 {
				t.Errorf("event time %v is out of range", e.ErrorDT)
			}
			if e.Fingerprint == "" || !e.ErrorSeverity.Valid() || !e.Grade.Valid() || e.Count < 1 {
				t.Errorf("malformed event from fuzz input: %+v", e)
			}
		}
		for _, b := range c.Boots {
			if y := b.StartedAt.UTC().Year(); b.StartedAt.IsZero() || y < 1970 || y > 9999 {
				t.Errorf("boot at %v is out of range", b.StartedAt)
			}
		}
		for _, g := range c.GPUs {
			if y := g.ObservedAt.UTC().Year(); y < 1970 || y > 9999 {
				t.Errorf("Xid time %v is out of range", g.ObservedAt)
			}
			if _, memory := MemoryXIDs[g.XID.V]; g.Fingerprint == "" || !g.XID.Valid || !memory || g.PCIBus == "" || g.TimeQuality == "" {
				t.Errorf("malformed Xid record from fuzz input: %+v", g)
			}
		}
	})
}
