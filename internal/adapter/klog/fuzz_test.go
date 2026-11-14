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
	f.Add("9,9,99999999999999999999,-;EDAC MC0: 1 UE on a (b page:0xffffffffffffffff offset:0xfff grain:9)\n", false)
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
		for _, e := range c.Events {
			if e.Fingerprint == "" || !e.ErrorSeverity.Valid() || !e.Grade.Valid() || e.Count < 1 {
				t.Errorf("malformed event from fuzz input: %+v", e)
			}
		}
	})
}
