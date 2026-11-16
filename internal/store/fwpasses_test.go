package store

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
)

var fwT0 = time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)

// fwEvent is an event of a BMC with its firmware record.
type fwEvent struct {
	ref       string
	at        time.Duration // after fwT0
	sev       model.Severity
	slot      string
	source    string
	log       string
	key       string // entry key
	repeat    int
	mapping   string
	rank      bool
	common    string
	clears    string
	hostEvent bool // a record of the host's own logs, no firmware record
}

func putEvents(t *testing.T, s *Store, list []fwEvent) {
	t.Helper()
	mustUpdate(t, s, func(w *Writer) error {
		for _, f := range list {
			e := &model.Event{Host: "h1", ErrorDT: fwT0.Add(f.at), ErrorSeverity: f.sev, Slot: f.slot, Label: f.slot, Source: f.source,
				RawRef: f.ref, Fingerprint: "fp " + f.ref}
			if f.sev == "" {
				e.ErrorSeverity = model.SevCE
			}
			if f.rank {
				e.Rank = model.Some(1)
			}
			if !f.hostEvent {
				common := f.common
				if common == "" {
					common = map[string]string{"CE": "ce", "UE": "ue", "": "unclassified"}[e.ErrorSeverity.Class()]
				}
				mapping := f.mapping
				if mapping == "" {
					mapping = model.MappingTable
				}
				e.Firmware = &model.FirmwareRecord{Common: common, Clears: f.clears, Log: f.log, EntryKey: f.key, Repeat: f.repeat, Mapping: mapping}
			}
			if _, err := w.InsertEvent(e); err != nil {
				return err
			}
		}
		return nil
	})
}

// counted runs the passes and returns the refs of the events that count,
// with their counts.
func counted(t *testing.T, s *Store) string {
	t.Helper()
	runFirmwarePasses(t, s)
	evs, err := s.EventList(ctx, EventFilter{Host: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		if e.ErrorSeverity.Class() == "" {
			continue
		}
		out = append(out, fmt.Sprintf("%s:%d", e.RawRef, e.Count))
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

func runFirmwarePasses(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.AlignBMCClock(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	if err := s.FirmwareRepeats(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	if err := s.FirmwareWithdrawals(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FirmwareDuplicates(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkDuplicates(ctx, "h1", DuplicateTolerance{}); err != nil {
		t.Fatal(err)
	}
}

const sec = time.Second

func TestFirmwareDuplicateRules(t *testing.T) {
	for _, tt := range []struct {
		name string
		evs  []fwEvent
		want string
	}{
		{"the same entry read twice", []fwEvent{
			{ref: "a", source: "redfish/log", log: "Sel", key: "k1", repeat: 1},
			{ref: "b", source: "redfish/log", log: "Sel", key: "k1", repeat: 1, at: sec},
		}, "a:1"},
		{"two entries with keys of their own", []fwEvent{
			{ref: "a", source: "redfish/log", log: "Sel", key: "k1", repeat: 1, slot: "A1"},
			{ref: "b", source: "redfish/log", log: "Sel", key: "k2", repeat: 1, slot: "A1", at: sec},
		}, "a:1 b:1"},
		{"four uncorrected records of one log in three seconds", []fwEvent{
			{ref: "a", sev: model.SevUE, slot: "A1", source: "redfish/log", log: "Sel"},
			{ref: "b", sev: model.SevUE, slot: "A1", source: "redfish/log", log: "Sel", at: sec},
			{ref: "c", sev: model.SevUE, slot: "A1", source: "redfish/log", log: "Sel", at: 2 * sec},
			{ref: "d", sev: model.SevUE, slot: "A1", source: "redfish/log", log: "Sel", at: 3 * sec},
		}, "a:1"},
		{"uncorrected records of one log six seconds apart", []fwEvent{
			{ref: "a", sev: model.SevUE, slot: "A1", source: "redfish/log", log: "Sel"},
			{ref: "b", sev: model.SevUE, slot: "A1", source: "redfish/log", log: "Sel", at: 6 * sec},
		}, "a:1 b:1"},
		{"corrected records of one log are each a report", []fwEvent{
			{ref: "a", slot: "A1", source: "redfish/log", log: "Sel"},
			{ref: "b", slot: "A1", source: "redfish/log", log: "Sel", at: sec},
		}, "a:1 b:1"},
		{"two logs of one BMC", []fwEvent{
			{ref: "a", slot: "A1", source: "redfish/log", log: "Sel"},
			{ref: "b", slot: "A1", source: "redfish/log", log: "Lclog", at: 2 * sec},
		}, "a:1"},
		{"two logs, the table over the wording", []fwEvent{
			{ref: "a", slot: "A1", source: "redfish/log", log: "Sel", mapping: model.MappingWording},
			{ref: "b", slot: "A1", source: "redfish/log", log: "Lclog", at: 2 * sec},
		}, "b:1"},
		{"two logs, the one with more of the place", []fwEvent{
			{ref: "a", slot: "A1", source: "redfish/log", log: "Sel"},
			{ref: "b", slot: "A1", source: "redfish/log", log: "Lclog", at: 2 * sec, rank: true},
		}, "b:1"},
		{"the same log read by two readers", []fwEvent{
			{ref: "a", slot: "A1", source: "redfish/log", log: "Sel"},
			{ref: "b", slot: "A1", source: "sel/list", log: "ipmitool sel list", at: sec},
		}, "a:1"},
		{"two logs, other modules", []fwEvent{
			{ref: "a", slot: "A1", source: "redfish/log", log: "Sel"},
			{ref: "b", slot: "A2", source: "redfish/log", log: "Lclog", at: sec},
		}, "a:1 b:1"},
		{"two logs, other classes", []fwEvent{
			{ref: "a", slot: "A1", source: "redfish/log", log: "Sel"},
			{ref: "b", sev: model.SevUE, slot: "A1", source: "redfish/log", log: "Lclog", at: sec},
		}, "a:1 b:1"},
		{"two logs, six seconds apart", []fwEvent{
			{ref: "a", slot: "A1", source: "redfish/log", log: "Sel"},
			{ref: "b", slot: "A1", source: "redfish/log", log: "Lclog", at: 6 * sec},
		}, "a:1 b:1"},
		{"a BMC record and a host record", []fwEvent{
			{ref: "bmc", slot: "A1", source: "redfish/log", log: "Sel", at: 30 * sec},
			{ref: "host", slot: "A1", source: "klog/edac", hostEvent: true},
		}, "host:1"},
		{"an entry whose repeat grows over three readings", []fwEvent{
			{ref: "r1", slot: "A1", source: "redfish/log", log: "IML", key: "k9", repeat: 1},
			{ref: "r3", slot: "A1", source: "redfish/log", log: "IML", key: "k9", repeat: 3, at: 3600 * sec},
			{ref: "r6", slot: "A1", source: "redfish/log", log: "IML", key: "k9", repeat: 6, at: 7200 * sec},
		}, "r1:1 r3:2 r6:3"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t)
			putEvents(t, s, tt.evs)
			if got := counted(t, s); got != tt.want {
				t.Errorf("counted %s, want %s", got, tt.want)
			}
			// Run twice: the same.
			if got := counted(t, s); got != tt.want {
				t.Errorf("second run: %s, want %s", got, tt.want)
			}
			// The rows in the other order: the same.
			r := newStore(t)
			rev := append([]fwEvent(nil), tt.evs...)
			for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
				rev[i], rev[j] = rev[j], rev[i]
			}
			putEvents(t, r, rev)
			if got := counted(t, r); got != tt.want {
				t.Errorf("rows in the other order: %s, want %s", got, tt.want)
			}
		})
	}
}

func putAction(t *testing.T, s *Store, ref string, at time.Duration, kind model.ActionKind, slot, source, log string, rank bool) {
	t.Helper()
	mustUpdate(t, s, func(w *Writer) error {
		a := &model.Action{Host: "h1", At: fwT0.Add(at), Kind: kind, Result: model.ResultOK, Slot: slot, Label: slot, Source: source,
			RawRef: ref, Fingerprint: "fp " + ref, Firmware: &model.FirmwareRecord{Common: "x", Log: log}}
		if rank {
			a.Rank = model.Some(0)
		}
		_, err := w.InsertAction(a)
		return err
	})
}

func liveActions(t *testing.T, s *Store) string {
	t.Helper()
	runFirmwarePasses(t, s)
	acts, err := s.Actions(ctx, ActionFilter{Host: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, a := range acts {
		w := ""
		if a.WithdrawnBy != 0 {
			w = "(withdrawn)"
		}
		out = append(out, a.RawRef+w)
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

func TestFirmwareActionDuplicates(t *testing.T) {
	s := newStore(t)
	putAction(t, s, "lc", 0, model.ActionPPR, "A1", "redfish/log", "Lclog", false)
	putAction(t, s, "sel", 2*sec, model.ActionPPR, "A1", "redfish/log", "Sel", true)
	putAction(t, s, "other", 3*sec, model.ActionPPR, "A2", "redfish/log", "Sel", false)
	putAction(t, s, "late", 10*sec, model.ActionPPR, "A1", "redfish/log", "Sel", false)
	putAction(t, s, "kind", sec, model.ActionDisable, "A1", "redfish/log", "Sel", false)
	if got := liveActions(t, s); got != "kind late other sel" {
		t.Errorf("actions that hold: %s", got)
	}
	if all, _ := s.Actions(ctx, ActionFilter{Host: "h1", Duplicates: true}); len(all) != 5 {
		t.Errorf("%d actions with duplicates", len(all))
	}
}

// TestFirmwareWithdrawals: a mark is withdrawn by a later "cleared" of the
// same module, also one of a later dump; not by one before it, nor of
// another module.
func TestFirmwareWithdrawals(t *testing.T) {
	s := newStore(t)
	putAction(t, s, "off", 0, model.ActionDisable, "A1", "sel/list", "SEL", false)
	putAction(t, s, "pfa", 0, model.ActionPredictedFailure, "A2", "redfish/log", "Sel", false)
	putAction(t, s, "off3", 0, model.ActionDisable, "A3", "sel/list", "SEL", false)
	putEvents(t, s, []fwEvent{
		{ref: "back-before", sev: model.SevInformational, slot: "A3", source: "sel/list", log: "SEL", common: "cleared", clears: "disabled", at: -sec},
		{ref: "back-other", sev: model.SevInformational, slot: "A9", source: "sel/list", log: "SEL", common: "cleared", clears: "disabled", at: sec},
	})
	if got := liveActions(t, s); got != "off off3 pfa" {
		t.Errorf("before the later dump: %s", got)
	}
	// A later dump.
	putEvents(t, s, []fwEvent{
		{ref: "back", sev: model.SevInformational, slot: "A1", source: "sel/list", log: "SEL", common: "cleared", clears: "disabled", at: 3600 * sec},
		{ref: "pfa-gone", sev: model.SevInformational, slot: "A2", source: "redfish/log", log: "Sel", common: "cleared", clears: "predicted-failure", at: 7200 * sec},
	})
	if got := liveActions(t, s); got != "off(withdrawn) off3 pfa(withdrawn)" {
		t.Errorf("after the later dump: %s", got)
	}
}

// TestAlignBMCClock: the clock of a BMC is moved when 3 pairs agree on a
// difference between 60 s and 15 h, and not otherwise.
func TestAlignBMCClock(t *testing.T) {
	pairs := func(n int, off time.Duration, zero int) []fwEvent {
		var list []fwEvent
		for i := range n {
			at := time.Duration(i) * 6 * time.Hour
			list = append(list, fwEvent{ref: fmt.Sprintf("host%d", i), slot: "A1", source: "klog/edac", hostEvent: true, at: at},
				fwEvent{ref: fmt.Sprintf("bmc%d", i), slot: "A1", source: "redfish/log", log: "Sel", at: at + off + time.Duration(i)*time.Second/2})
		}
		for i := range zero {
			at := time.Duration(100+i) * 6 * time.Hour
			list = append(list, fwEvent{ref: fmt.Sprintf("hz%d", i), slot: "A1", source: "klog/edac", hostEvent: true, at: at},
				fwEvent{ref: fmt.Sprintf("bz%d", i), slot: "A1", source: "redfish/log", log: "Sel", at: at})
		}
		return list
	}
	for _, tt := range []struct {
		name  string
		evs   []fwEvent
		moved time.Duration
	}{
		{"three pairs that agree", pairs(3, 2*time.Hour, 0), -2 * time.Hour},
		{"a time zone of nine hours", pairs(4, 9*time.Hour, 0), -9 * time.Hour},
		{"two pairs", pairs(2, 2*time.Hour, 0), 0},
		{"pairs that agree at zero too", pairs(3, 2*time.Hour, 2), 0},
		{"a difference of twenty hours", pairs(3, 20*time.Hour, 0), 0},
		{"thirty seconds", pairs(3, 30*time.Second, 0), 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t)
			putEvents(t, s, tt.evs)
			al, err := s.AlignBMCClock(ctx, "h1")
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case tt.moved == 0 && len(al) != 0:
				t.Errorf("moved: %+v", al)
			case tt.moved != 0 && (len(al) != 1 || (al[0].Offset-tt.moved).Abs() > 2*time.Second || al[0].Matched < 3):
				t.Errorf("alignment %+v, want %v", al, tt.moved)
			}
			if tt.moved != 0 {
				evs, _ := s.EventList(ctx, EventFilter{Host: "h1", WithDuplicates: true})
				for _, e := range evs {
					if e.Source == "redfish/log" && (e.TimeQuality != model.TimeEstimated || e.ErrorDT.Sub(fwT0) > time.Minute+time.Duration(len(tt.evs))*6*time.Hour) {
						t.Errorf("%s: %s %s", e.RawRef, e.ErrorDT, e.TimeQuality)
					}
				}
				// Run again: nothing more moves.
				if again, _ := s.AlignBMCClock(ctx, "h1"); len(again) != 0 {
					t.Errorf("second run moved: %+v", again)
				}
			}
		})
	}
}
