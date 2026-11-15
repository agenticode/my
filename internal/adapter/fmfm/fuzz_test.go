package fmfm

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
	for _, name := range []string{"runtime.json", "boot.json", "crash.json", "combined.jsonl"} {
		f.Add(testutil.ReadTestdata(f, "fmfm", name))
	}
	f.Add([]byte(`{"ErrorDT":"9999:12:31:23:59:60:999","ErrorSeverity":"fatal","Row":"0xffffffffffffffff","OsPhysicalAddress":-1,"EcsModeRegisters":{"MR14":{"x":1}}}`))
	f.Add([]byte(`[{"CurrentBootDT":1e400,"InstalledMemory":[{"SocketId":"x","ChannelId":[],"Locator":" "}],"TrainingEventLogs":[7,{"Step":{}}]}]`))
	f.Add([]byte(`{"OsCrashDT":"2026-11-12T03:14:07Z","FatalError":[1],"SocketId":0,"ChannelId":0,"ChannelSlotId":99999999999}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var c adapter.Collected
		rep := NewReader(bytes.NewReader(data), "fuzz", Options{Now: now}).Collect(ctx, &c)
		if rep.Status == "" || rep.Kind != Kind {
			t.Fatalf("report without status or kind: %+v", rep)
		}
		usable := rep.Status == model.SourceOK || rep.Status == model.SourcePartial
		if n := len(c.Events) + len(c.ECSs) + len(c.Modules) + len(c.Boots); n > 0 && !usable {
			t.Errorf("%d records from a source with status %s", n, rep.Status)
		}
		okTime := func(at time.Time) bool { y := at.UTC().Year(); return !at.IsZero() && y >= 1970 && y <= 9999 }
		for _, e := range c.Events {
			if !e.ErrorSeverity.Valid() || !e.ErrorScrubType.Valid() || !e.Grade.Valid() || e.Fingerprint == "" || e.Source == "" ||
				e.RawRef == "" || e.Count != 1 || !okTime(e.ErrorDT) {
				t.Errorf("event the ledger would refuse: %+v", e)
			}
		}
		for _, s := range c.ECSs {
			if s.Empty() || s.Fingerprint == "" || s.Source == "" || !okTime(s.ObservedAt) {
				t.Errorf("reading the ledger would refuse: %+v", s)
			}
			for i, reg := range s.Registers() {
				if reg.Valid && (reg.V < 0 || reg.V > 0xFF) {
					t.Errorf("MR%d = %d", 14+i, reg.V)
				}
			}
		}
		for _, m := range c.Modules {
			if m.Locator == "" || !okTime(m.ObservedAt) {
				t.Errorf("module without a slot name or time: %+v", m)
			}
		}
		for _, b := range c.Boots {
			if !okTime(b.StartedAt) {
				t.Errorf("boot without a time: %+v", b)
			}
		}
	})
}
