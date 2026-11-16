package ingest

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/fwevent"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// passSource gives every firmware pass something to do: one error in two
// logs, a mark taken back, an entry read with two repeat counts.
func passSource(at time.Time) adapter.Adapter {
	return &fake{kind: "redfish", fn: func(sink adapter.Sink) error {
		recs := []*fwevent.Record{
			{Event: fwevent.CE, At: at, Labels: []string{"DIMM_A1"}, Fingerprint: "sel 1",
				Firmware: model.FirmwareRecord{Log: "Sel", Mapping: model.MappingTable}},
			{Event: fwevent.CE, At: at.Add(time.Second), Labels: []string{"DIMM_A1"}, Fingerprint: "lc 1",
				Firmware: model.FirmwareRecord{Log: "Lclog", Mapping: model.MappingTable}},
			{Event: fwevent.Disabled, At: at.Add(time.Hour), Labels: []string{"DIMM_A2"}, Fingerprint: "off",
				Firmware: model.FirmwareRecord{Log: "Sel", Mapping: model.MappingTable}},
			{Event: fwevent.Cleared, Clears: fwevent.Disabled, At: at.Add(2 * time.Hour), Labels: []string{"DIMM_A2"}, Fingerprint: "on",
				Firmware: model.FirmwareRecord{Log: "Sel", Mapping: model.MappingTable}},
			{Event: fwevent.CE, At: at.Add(3 * time.Hour), Labels: []string{"DIMM_A3"}, Fingerprint: "iml r2",
				Firmware: model.FirmwareRecord{Log: "IML", EntryKey: "e7", Repeat: 2, Mapping: model.MappingWording}},
			{Event: fwevent.CE, At: at.Add(4 * time.Hour), Labels: []string{"DIMM_A3"}, Fingerprint: "iml r5",
				Firmware: model.FirmwareRecord{Log: "IML", EntryKey: "e7", Repeat: 5, Mapping: model.MappingWording}},
		}
		for _, r := range recs {
			r.Source, r.RawRef = "redfish/log", r.Fingerprint
			if err := fwevent.Emit(sink, r); err != nil {
				return err
			}
		}
		return nil
	}}
}

// ledgerState is what the passes change: counts, duplicates, withdrawals.
func passState(t *testing.T, s *store.Store) string {
	t.Helper()
	evs, err := s.EventList(ctx, store.EventFilter{Host: "node-a", WithDuplicates: true})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		out = append(out, fmt.Sprintf("%s count %d dup %v %s", e.RawRef, e.Count, e.DupOf != 0, e.ErrorDT.Format(time.RFC3339)))
	}
	acts, _ := s.Actions(ctx, store.ActionFilter{Host: "node-a", Duplicates: true})
	for _, a := range acts {
		out = append(out, fmt.Sprintf("%s withdrawn %v dup %v", a.RawRef, a.WithdrawnBy != 0, a.DupOf != 0))
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

// TestPassFault: a firmware pass that fails, or panics, is rolled back and
// named in the inventory; the ledger is the ledger without that pass, and
// the run goes on.
func TestPassFault(t *testing.T) {
	at := time.Date(2026, 11, 3, 2, 0, 0, 0, time.UTC)
	t.Cleanup(func() { passFault = nil })
	run := func(fault func(string) error) (*store.Store, *Result) {
		passFault = fault
		s := newStore(t)
		res := mustRun(t, s, at.Add(24*time.Hour), passSource(at))
		return s, res
	}
	all, _ := run(nil)
	full := passState(t, all)
	if !strings.Contains(full, "lc 1 count 1 dup true") || !strings.Contains(full, "off withdrawn true") || !strings.Contains(full, "iml r5 count 3") {
		t.Fatalf("the passes did not do their work:\n%s", full)
	}
	for _, name := range []string{"AlignBMCClock", "FirmwareRepeats", "FirmwareWithdrawals", "FirmwareDuplicates"} {
		for _, how := range []string{"error", "panic"} {
			t.Run(name+" "+how, func(t *testing.T) {
				without, _ := run(func(n string) error {
					if n == name {
						return errSkipPass
					}
					return nil
				})
				failed, res := run(func(n string) error {
					if n != name {
						return nil
					}
					if how == "panic" {
						panic("a fault for the test")
					}
					return errors.New("a fault for the test")
				})
				if got, want := passState(t, failed), passState(t, without); got != want {
					t.Errorf("ledger:\n%s\nwant the ledger without the pass:\n%s", got, want)
				}
				var entry *model.Source
				for i := range res.Sources {
					if res.Sources[i].Kind == FirmwarePassesKind {
						entry = &res.Sources[i]
					}
				}
				if entry == nil || !strings.Contains(strings.Join(entry.Notes, "\n"), name+" did not run (") || entry.Status != model.SourcePartial {
					t.Errorf("inventory: %+v", entry)
				}
			})
		}
	}
}
