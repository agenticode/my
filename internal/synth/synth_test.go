package synth

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/edacsysfs"
	"github.com/agenticode/my/internal/adapter/hostinfo"
	"github.com/agenticode/my/internal/adapter/klog"
	"github.com/agenticode/my/internal/adapter/rasdaemon"
	"github.com/agenticode/my/internal/adapter/smbios"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

var ctx = context.Background()

func generate(t *testing.T, name string, seed uint64, mut func(*Scenario)) *Dataset {
	t.Helper()
	sc, err := Named(name, seed)
	if err != nil {
		t.Fatal(err)
	}
	if mut != nil {
		mut(sc)
	}
	ds, err := Generate(sc)
	if err != nil {
		t.Fatalf("generate %s: %v", name, err)
	}
	return ds
}

func noNoise(sc *Scenario) { sc.NoisePerDIMMDay = 0 }

func TestScenariosCoverTheDesign(t *testing.T) {
	modes := map[model.FaultMode]bool{}
	var moves, repairs, offlines, ues, reboots int
	for _, name := range Names() {
		ds := generate(t, name, 1, nil)
		for _, f := range ds.Scenario.Faults {
			modes[f.Mode] = true
			ues += len(f.UE)
		}
		moves += len(ds.Scenario.Moves)
		repairs += len(ds.Scenario.Repairs)
		offlines += len(ds.Scenario.Offlines)
		reboots += len(ds.Scenario.Reboots)
		if len(ds.Events) == 0 || len(ds.Boots) < 2 {
			t.Errorf("%s: %d events, %d boots", name, len(ds.Events), len(ds.Boots))
		}
	}
	for _, m := range model.FaultModes {
		if !modes[m] {
			t.Errorf("no scenario has a fault of mode %q", m)
		}
	}
	if moves == 0 || repairs == 0 || offlines == 0 || ues == 0 || reboots == 0 {
		t.Errorf("scenarios lack a feature: %d moves, %d repairs, %d page offlines, %d uncorrected errors, %d reboots", moves, repairs, offlines, ues, reboots)
	}
	if _, err := Named("no-such-scenario", 1); err == nil || !strings.Contains(err.Error(), "known:") {
		t.Errorf("unknown scenario: err = %v", err)
	}
}

func TestSameSeedSameData(t *testing.T) {
	for _, name := range []string{"demo", "dimm", "system-slot"} {
		a, b := generate(t, name, 42, nil), generate(t, name, 42, nil)
		ja, _ := json.Marshal(struct {
			E []model.Event
			A []model.Action
			M []model.DIMM
			B []model.Boot
		}{a.Events, a.Actions, a.Modules, a.Boots})
		jb, _ := json.Marshal(struct {
			E []model.Event
			A []model.Action
			M []model.DIMM
			B []model.Boot
		}{b.Events, b.Actions, b.Modules, b.Boots})
		if string(ja) != string(jb) {
			t.Errorf("%s: two runs with the same seed differ", name)
		}
		c := generate(t, name, 43, nil)
		if reflect.DeepEqual(a.Modules, c.Modules) {
			t.Errorf("%s: another seed gave the same DIMM serial numbers", name)
		}
	}
}

// shape collects the distinct values of each location field of a slot's events.
type shape struct {
	ranks, devices, banks, rows, cols, dqs, pages map[int]bool
	n                                             int
}

func shapeOf(ds *Dataset, slot string) shape {
	s := shape{map[int]bool{}, map[int]bool{}, map[int]bool{}, map[int]bool{}, map[int]bool{}, map[int]bool{}, map[int]bool{}, 0}
	for i := range ds.Events {
		e := &ds.Events[i]
		if e.Label != slot {
			continue
		}
		s.n++
		s.ranks[e.Rank.V], s.devices[e.DeviceId.V] = true, true
		s.banks[e.BankGroup.V*100+e.Bank.V] = true
		s.rows[e.Row.V], s.cols[e.Column.V] = true, true
		var dq int
		fmt.Sscanf(e.ErrorVector, "dq:0x%x", &dq)
		s.dqs[dq] = true
		s.pages[int(e.OsPhysicalAddress.V>>12)] = true
	}
	return s
}

func TestFaultModesHaveTheirShape(t *testing.T) {
	tests := []struct {
		scenario, slot string
		check          func(t *testing.T, s shape)
	}{
		{"single-bit", "CPU1_D1", func(t *testing.T, s shape) {
			if len(s.ranks) != 1 || len(s.devices) != 1 || len(s.banks) != 1 || len(s.rows) != 1 || len(s.cols) != 1 || len(s.dqs) != 1 || len(s.pages) != 1 {
				t.Errorf("single bit must repeat one location: %d devices, %d banks, %d rows, %d columns, %d pages", len(s.devices), len(s.banks), len(s.rows), len(s.cols), len(s.pages))
			}
		}},
		{"row", "CPU0_C1", func(t *testing.T, s shape) {
			if len(s.devices) != 1 || len(s.banks) != 1 || len(s.rows) != 1 || len(s.cols) < 2 {
				t.Errorf("row: %d devices, %d banks, %d rows, %d columns", len(s.devices), len(s.banks), len(s.rows), len(s.cols))
			}
			// A row covers several pages, which is why per-page counting misses it.
			if len(s.pages) < 2 {
				t.Errorf("row fault touches %d page", len(s.pages))
			}
		}},
		{"multi-row", "CPU0_C1", func(t *testing.T, s shape) {
			var rows []int
			for r := range s.rows {
				rows = append(rows, r)
			}
			sort.Ints(rows)
			if len(s.banks) != 1 || len(rows) != 2 || rows[1]-rows[0] != 1 || len(s.cols) < 2 {
				t.Errorf("multi-row: %d banks, rows %v, %d columns", len(s.banks), rows, len(s.cols))
			}
		}},
		{"column", "CPU0_E1", func(t *testing.T, s shape) {
			if len(s.devices) != 1 || len(s.banks) != 1 || len(s.cols) != 1 || len(s.rows) < 2 {
				t.Errorf("column: %d devices, %d banks, %d columns, %d rows", len(s.devices), len(s.banks), len(s.cols), len(s.rows))
			}
		}},
		{"bank", "CPU1_A1", func(t *testing.T, s shape) {
			if len(s.devices) != 1 || len(s.banks) != 1 || len(s.rows) < 50 || len(s.cols) < 50 {
				t.Errorf("bank: %d devices, %d banks, %d rows, %d columns", len(s.devices), len(s.banks), len(s.rows), len(s.cols))
			}
		}},
		{"device", "CPU1_G1", func(t *testing.T, s shape) {
			if len(s.ranks) != 1 || len(s.devices) != 1 || len(s.banks) < 2 {
				t.Errorf("device: %d ranks, %d devices, %d banks", len(s.ranks), len(s.devices), len(s.banks))
			}
		}},
		{"dimm", "CPU0_H1", func(t *testing.T, s shape) {
			if len(s.devices) < 2 || len(s.banks) < 2 {
				t.Errorf("DIMM: %d devices, %d banks", len(s.devices), len(s.banks))
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.scenario, func(t *testing.T) {
			for seed := uint64(1); seed <= 5; seed++ {
				ds := generate(t, tt.scenario, seed, noNoise)
				s := shapeOf(ds, tt.slot)
				if s.n != ds.Truth[0].CE+ds.Truth[0].UE || s.n == 0 {
					t.Fatalf("seed %d: %d events in the slot, truth says %d", seed, s.n, ds.Truth[0].CE+ds.Truth[0].UE)
				}
				tt.check(t, s)
			}
		})
	}
}

func TestEventsAreWellFormed(t *testing.T) {
	for _, name := range Names() {
		ds := generate(t, name, 7, nil)
		h := ds.Scenario.Host
		g := h.Geometry
		seen := map[string]bool{}
		addrOf := map[string]uint64{}
		var prev time.Time
		for i := range ds.Events {
			e := &ds.Events[i]
			if e.ErrorDT.Before(prev) {
				t.Fatalf("%s: events are not in time order at %d", name, i)
			}
			prev = e.ErrorDT
			if e.ErrorDT.Before(ds.Scenario.Start) || e.ErrorDT.After(ds.Scenario.End) {
				t.Errorf("%s: event %d at %v is outside the scenario", name, i, e.ErrorDT)
			}
			if e.Rank.V >= g.Ranks || e.DeviceId.V >= g.Devices || e.BankGroup.V >= g.BankGroups || e.Bank.V >= g.Banks ||
				e.Row.V >= g.Rows || e.Column.V >= g.Columns || e.Column.V%g.ColumnStep != 0 {
				t.Errorf("%s: event %d has a location outside the geometry: %+v", name, i, e)
			}
			if e.ComputeGrade() != model.GradeA || !e.ErrorSeverity.Valid() || e.Fingerprint == "" || seen[e.Fingerprint] || e.RawRef == "" ||
				!strings.HasPrefix(e.Raw, "EDAC MC") || e.BootID == "" || e.DIMMUID == "" {
				t.Errorf("%s: event %d is incomplete: %+v", name, i, e)
			}
			seen[e.Fingerprint] = true
			// One location in one slot is one address, and it is a
			// cache line address.
			key := fmt.Sprintf("%s/%d/%d/%d/%d/%d", e.Label, e.Rank.V, e.BankGroup.V, e.Bank.V, e.Row.V, e.Column.V)
			if a, ok := addrOf[key]; ok && a != e.OsPhysicalAddress.V {
				t.Errorf("%s: location %s has two addresses", name, key)
			}
			addrOf[key] = e.OsPhysicalAddress.V
			if e.OsPhysicalAddress.V%64 != 0 || e.OsPhysicalAddress.V < 4<<30 {
				t.Errorf("%s: address %#x", name, e.OsPhysicalAddress.V)
			}
		}
		// Addresses of different slots never overlap.
		slotOf := map[uint64]string{}
		for i := range ds.Events {
			e := &ds.Events[i]
			base := (e.OsPhysicalAddress.V - 4<<30) / uint64(g.SizeBytes())
			if s, ok := slotOf[base]; ok && s != e.Label {
				t.Errorf("%s: slots %s and %s share an address range", name, s, e.Label)
			}
			slotOf[base] = e.Label
		}
	}
}

func TestDemoMatchesTheDesign(t *testing.T) {
	ds := generate(t, "demo", 1, nil)
	if ds.Scenario.Host.Name != "r12-n034" || len(ds.Boots) != 4 || len(ds.Events) != 1285 {
		t.Fatalf("host %s, %d boots, %d events; want r12-n034, 4, 1285", ds.Scenario.Host.Name, len(ds.Boots), len(ds.Events))
	}
	row := ds.Truth[0]
	if row.Slot != "CPU0_C1" || row.CE != 1231 || row.UE != 1 || row.Row != model.Some(0x1a2f) || row.Device != model.Some(7) || row.Bank != model.Some(3) {
		t.Errorf("row fault truth = %+v", row)
	}
	if bit := ds.Truth[1]; bit.Slot != "CPU1_D1" || bit.CE != 53 || bit.UE != 0 {
		t.Errorf("single-bit truth = %+v", bit)
	}
	kinds := map[string]int{}
	for _, a := range ds.Actions {
		kinds[string(a.Kind)+"/"+a.Method+"/"+string(a.Result)]++
		if a.Slot != "CPU0_C1" {
			t.Errorf("action on %s: %+v", a.Slot, a)
		}
	}
	want := map[string]int{"hppr/bios/ok": 1, "page-offline/soft/ok": 6, "page-offline/hard/ok": 1}
	if !reflect.DeepEqual(kinds, want) {
		t.Errorf("actions = %v, want %v", kinds, want)
	}
	// The fault goes on after the repair: the row comes back.
	repair := time.Date(2026, 10, 28, 1, 30, 40, 0, time.UTC)
	after := 0
	var ue *model.Event
	for i := range ds.Events {
		e := &ds.Events[i]
		if e.Label == "CPU0_C1" && e.ErrorDT.After(repair) && e.Row.V == 0x1a2f {
			after++
		}
		if e.ErrorSeverity == model.SevUE {
			ue = e
		}
	}
	if after == 0 {
		t.Error("no errors in the repaired row after the repair")
	}
	if ue == nil || !ue.ErrorDT.Equal(time.Date(2026, 11, 12, 3, 14, 0, 0, time.UTC)) || ue.Row.V != 0x1a2f || ue.DeviceId.V != 7 || ue.ErrorScrubType != model.ScrubDemand {
		t.Errorf("uncorrected error = %+v", ue)
	}
	// The row fault names data lines 2 and 3 of its device.
	if !strings.HasSuffix(ue.ErrorVector, "0xc") {
		t.Errorf("error vector of the row fault = %q, want data lines 2 and 3", ue.ErrorVector)
	}
}

func TestMovesAndRepairs(t *testing.T) {
	moveAt := time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC)

	t.Run("errors follow a moved DIMM", func(t *testing.T) {
		ds := generate(t, "moved-dimm", 3, noNoise)
		uids := map[string]bool{}
		for i := range ds.Events {
			e := &ds.Events[i]
			want := "CPU0_C1"
			if !e.ErrorDT.Before(moveAt) {
				want = "CPU0_D1"
			}
			if e.Label != want {
				t.Fatalf("event at %v is in %s, want %s", e.ErrorDT, e.Label, want)
			}
			uids[e.DIMMUID] = true
		}
		if len(uids) != 1 {
			t.Errorf("%d DIMM identities, want 1: the fault belongs to the DIMM", len(uids))
		}
	})
	t.Run("errors of a slot stay when the DIMM is swapped", func(t *testing.T) {
		ds := generate(t, "system-slot", 3, noNoise)
		before, after := map[string]bool{}, map[string]bool{}
		for i := range ds.Events {
			e := &ds.Events[i]
			if e.Label != "CPU0_C1" {
				t.Fatalf("event in %s, want CPU0_C1", e.Label)
			}
			if e.ErrorDT.Before(moveAt) {
				before[e.DIMMUID] = true
			} else {
				after[e.DIMMUID] = true
			}
		}
		if len(before) != 1 || len(after) != 1 || reflect.DeepEqual(before, after) {
			t.Errorf("DIMMs before %v and after %v the swap: want one each, and different", before, after)
		}
	})
	t.Run("two DIMMs of one channel", func(t *testing.T) {
		ds := generate(t, "system-channel", 3, noNoise)
		n := map[string]int{}
		for i := range ds.Events {
			n[ds.Events[i].Label]++
		}
		if n["CPU0_B1"] != 180 || n["CPU0_B2"] != 160 || len(n) != 2 {
			t.Errorf("events per slot = %v", n)
		}
	})
	t.Run("a repaired fault that is set to stop stays quiet", func(t *testing.T) {
		ds := generate(t, "repaired", 3, noNoise)
		for i := range ds.Events {
			if !ds.Events[i].ErrorDT.Before(moveAt) {
				t.Fatalf("event at %v, after the repair", ds.Events[i].ErrorDT)
			}
		}
		if len(ds.Actions) != 1 || ds.Actions[0].Kind != model.ActionHPPR || ds.Actions[0].Row != ds.Truth[0].Row || ds.Truth[0].CE >= 150 {
			t.Errorf("actions = %+v, truth = %+v", ds.Actions, ds.Truth[0])
		}
	})
}

// TestRuleScenarios: the scenarios that exist to make single rules fire
// carry the actions those rules read.
func TestRuleScenarios(t *testing.T) {
	reboot := time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC)
	kinds := func(ds *Dataset) map[model.ActionKind]int {
		n := map[model.ActionKind]int{}
		for _, a := range ds.Actions {
			n[a.Kind]++
			if a.Slot == "" || a.Label != a.Slot || a.BootID == "" || a.Fingerprint == "" {
				t.Errorf("%s: incomplete action %+v", ds.Scenario.Name, a)
			}
		}
		return n
	}
	lastEvent := func(ds *Dataset) time.Time {
		return ds.Events[len(ds.Events)-1].ErrorDT
	}

	t.Run("reseated", func(t *testing.T) {
		ds := generate(t, "reseated", 3, noNoise)
		if n := kinds(ds); len(ds.Actions) != 1 || n[model.ActionReseat] != 1 || !ds.Actions[0].At.Equal(reboot) || ds.Actions[0].Slot != "CPU0_C1" {
			t.Errorf("actions = %+v", ds.Actions)
		}
		// The errors end at the reseat.
		if ds.Truth[0].CE != 120 || !lastEvent(ds).Before(reboot) {
			t.Errorf("%d errors, the last at %v", ds.Truth[0].CE, lastEvent(ds))
		}
	})
	t.Run("fw-disabled", func(t *testing.T) {
		ds := generate(t, "fw-disabled", 3, noNoise)
		a := ds.Actions[0]
		if n := kinds(ds); len(ds.Actions) != 1 || n[model.ActionDisable] != 1 || a.Method != "bios" || a.Detail == "" || !a.At.After(reboot) || a.Slot != "CPU1_A1" {
			t.Errorf("actions = %+v", ds.Actions)
		}
		if !lastEvent(ds).Before(reboot) {
			t.Errorf("an error at %v, after the DIMM was taken out of use", lastEvent(ds))
		}
	})
	t.Run("repair-failed", func(t *testing.T) {
		ds := generate(t, "repair-failed", 3, noNoise)
		a := ds.Actions[0]
		if n := kinds(ds); len(ds.Actions) != 1 || n[model.ActionHPPR] != 1 || a.Result != model.ResultFailed || a.Row != ds.Truth[0].Row {
			t.Errorf("actions = %+v", ds.Actions)
		}
		// The fault goes on after the failed repair.
		if !lastEvent(ds).After(reboot.Add(30*24*time.Hour)) || ds.Truth[0].CE != 400 {
			t.Errorf("%d errors, the last at %v", ds.Truth[0].CE, lastEvent(ds))
		}
	})
	t.Run("offline-growing", func(t *testing.T) {
		ds := generate(t, "offline-growing", 3, noNoise)
		pages, days := map[uint64]bool{}, map[string]bool{}
		for _, a := range ds.Actions {
			if a.Kind != model.ActionPageOffline || a.Result != model.ResultOK || !a.OsPhysicalAddress.Valid {
				t.Errorf("action %+v", a)
			}
			if pages[a.OsPhysicalAddress.V] {
				t.Errorf("page %#x is taken offline twice", a.OsPhysicalAddress.V)
			}
			pages[a.OsPhysicalAddress.V] = true
			days[a.At.Format("2006-01-02")] = true
		}
		if len(pages) != 6 || len(days) != 3 {
			t.Errorf("%d pages on %d days, want 6 on 3", len(pages), len(days))
		}
	})
	t.Run("sparing names a rank and a device, no row", func(t *testing.T) {
		ds := generate(t, "device", 3, func(sc *Scenario) {
			sc.Repairs = []Repair{{At: sc.Reboots[1].At, Kind: model.ActionSparing, Fault: 0}}
		})
		var sp *model.Action
		for i := range ds.Actions {
			if ds.Actions[i].Kind == model.ActionSparing {
				sp = &ds.Actions[i]
			}
		}
		if sp == nil || sp.Rank != ds.Truth[0].Rank || sp.DeviceId != ds.Truth[0].Device || sp.Row.Valid || sp.Bank.Valid {
			t.Errorf("sparing action = %+v", sp)
		}
	})
}

func TestInvalidScenarios(t *testing.T) {
	bad := map[string]func(*Scenario){
		"no name":                  func(s *Scenario) { s.Name = "" },
		"end before start":         func(s *Scenario) { s.End = s.Start },
		"reboot outside the span":  func(s *Scenario) { s.Reboots = append(s.Reboots, Reboot{At: s.End.Add(time.Hour)}) },
		"move not at a reboot":     func(s *Scenario) { s.Moves = []Move{{At: s.Start.Add(time.Hour), A: "CPU0_A1", B: "CPU0_B1"}} },
		"move of an unknown slot":  func(s *Scenario) { s.Moves = []Move{{At: s.Reboots[0].At, A: "CPU0_A1", B: "CPU9_Z9"}} },
		"fault in an unknown slot": func(s *Scenario) { s.Faults[0].Slot = "DIMM_X" },
		"fault in an empty slot":   func(s *Scenario) { s.Faults[0].Slot = "CPU0_C2" },
		"fault without a mode":     func(s *Scenario) { s.Faults[0].Mode = "" },
		"repair of no fault":       func(s *Scenario) { s.Repairs = []Repair{{At: s.Reboots[0].At, Kind: model.ActionHPPR, Fault: 9}} },
		"repair of a bank fault": func(s *Scenario) {
			s.Faults[0].Mode = model.ModeBank
			s.Repairs = []Repair{{At: s.Reboots[0].At, Kind: model.ActionHPPR, Fault: 0}}
		},
		"repair with a wrong kind":  func(s *Scenario) { s.Repairs = []Repair{{At: s.Reboots[0].At, Kind: model.ActionReseat, Fault: 0}} },
		"offline before any error":  func(s *Scenario) { s.Offlines = []Offline{{At: s.Start, Fault: 0, Pages: 3}} },
		"reseat not at a reboot":    func(s *Scenario) { s.Reseats = []Reseat{{At: s.Start.Add(time.Hour), Slot: "CPU0_C1"}} },
		"reseat of an unknown slot": func(s *Scenario) { s.Reseats = []Reseat{{At: s.Reboots[0].At, Slot: "DIMM_X"}} },
		"mark with a wrong kind": func(s *Scenario) {
			s.Marks = []Mark{{At: s.Reboots[0].At, Slot: "CPU0_C1", Kind: model.ActionPageOffline}}
		},
		"mark outside the span": func(s *Scenario) {
			s.Marks = []Mark{{At: s.End.Add(time.Hour), Slot: "CPU0_C1", Kind: model.ActionDisable}}
		},
		"mark of an unknown slot": func(s *Scenario) {
			s.Marks = []Mark{{At: s.Reboots[0].At, Slot: "DIMM_X", Kind: model.ActionMarkFailed}}
		},
		"sparing of a fault without a rank": func(s *Scenario) {
			s.Faults[0].Mode = model.ModeDIMM
			s.Repairs = []Repair{{At: s.Reboots[0].At, Kind: model.ActionSparing, Fault: 0}}
		},
		"host without sockets":  func(s *Scenario) { s.Host.Sockets = 0 },
		"geometry without rows": func(s *Scenario) { s.Host.Geometry.Rows = 0 },
	}
	for name, mut := range bad {
		sc, _ := Named("row", 1)
		mut(sc)
		if _, err := Generate(sc); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

type total struct {
	Slot, Serial string
	CE, UE       int64
}

func totals(t *testing.T, s *store.Store, host string) []total {
	t.Helper()
	ts, err := s.Totals(ctx, store.EventFilter{Host: host})
	if err != nil {
		t.Fatal(err)
	}
	var out []total
	for _, x := range ts {
		tt := total{Slot: x.Slot, CE: x.CE, UE: x.UE}
		if d, _ := s.DIMM(ctx, x.DIMMUID); d != nil {
			tt.Serial = d.Serial
		}
		out = append(out, tt)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Slot != out[j].Slot {
			return out[i].Slot < out[j].Slot
		}
		return out[i].Serial < out[j].Serial
	})
	return out
}

func TestLedgerFromDataset(t *testing.T) {
	t.Run("demo", func(t *testing.T) {
		ds := generate(t, "demo", 1, nil)
		s := newStore(t)
		res, err := ingest.Run(ctx, s, []adapter.Adapter{ds.Adapter()}, ingest.Options{Kind: model.ScanSynthetic, Now: ds.Scenario.End})
		if err != nil {
			t.Fatal(err)
		}
		if res.Host != "r12-n034" || res.Events != 1285 || res.Actions != 8 || res.Duplicates != 0 {
			t.Errorf("result = %+v", res)
		}
		want := []total{
			{Slot: "CPU0_C1", Serial: ds.Truth[0].Serial, CE: 1231, UE: 1},
			{Slot: "CPU1_D1", Serial: ds.Truth[1].Serial, CE: 53},
		}
		if got := totals(t, s, "r12-n034"); !reflect.DeepEqual(got, want) {
			t.Errorf("totals\n got: %+v\nwant: %+v", got, want)
		}
		boots, _ := s.Boots(ctx, "r12-n034")
		slots, _ := s.Slots(ctx, "r12-n034")
		if len(boots) != 4 || len(slots) != 32 {
			t.Errorf("%d boots, %d slots; want 4 and 32", len(boots), len(slots))
		}
		// Every event is in a boot and tied to the DIMM that was seen in
		// its slot during that boot.
		evs, _ := s.EventList(ctx, store.EventFilter{Host: "r12-n034"})
		for _, e := range evs {
			if e.BootID == "" || e.DIMMLink == "" || e.DIMMLink == model.LinkAmbiguous || e.Grade != model.GradeA {
				t.Fatalf("event %s: boot %q link %q grade %s", e.RawRef, e.BootID, e.DIMMLink, e.Grade)
			}
		}
		acts, _ := s.Actions(ctx, store.ActionFilter{Host: "r12-n034"})
		for _, a := range acts {
			if a.Slot != "CPU0_C1" || a.DIMMUID == "" {
				t.Errorf("action %s %s: slot %q dimm %q", a.Kind, a.Method, a.Slot, a.DIMMUID)
			}
		}
	})

	t.Run("a moved DIMM keeps its history", func(t *testing.T) {
		ds := generate(t, "moved-dimm", 5, noNoise)
		s := newStore(t)
		if _, err := ingest.Run(ctx, s, []adapter.Adapter{ds.Adapter()}, ingest.Options{Now: ds.Scenario.End}); err != nil {
			t.Fatal(err)
		}
		host := ds.Scenario.Host.Name
		got := totals(t, s, host)
		if len(got) != 2 || got[0].Slot != "CPU0_C1" || got[1].Slot != "CPU0_D1" || got[0].Serial != got[1].Serial ||
			got[0].Serial != ds.Truth[0].Serial || got[0].CE+got[1].CE != 240 {
			t.Errorf("totals = %+v, want the 240 errors of one DIMM split over two slots", got)
		}
		uid := model.DIMMUID("Example Memory", "EXM64G48R2X4-A", ds.Truth[0].Serial)
		pl, _ := s.Placements(ctx, host, uid)
		if len(pl) != 2 || pl[0].Slot != "CPU0_C1" || pl[1].Slot != "CPU0_D1" {
			t.Errorf("placements of the moved DIMM = %+v", pl)
		}
	})

	t.Run("errors that stay in a slot are split between its DIMMs", func(t *testing.T) {
		ds := generate(t, "system-slot", 5, noNoise)
		s := newStore(t)
		if _, err := ingest.Run(ctx, s, []adapter.Adapter{ds.Adapter()}, ingest.Options{Now: ds.Scenario.End}); err != nil {
			t.Fatal(err)
		}
		got := totals(t, s, ds.Scenario.Host.Name)
		if len(got) != 2 || got[0].Slot != "CPU0_C1" || got[1].Slot != "CPU0_C1" || got[0].Serial == got[1].Serial || got[0].CE+got[1].CE != 240 {
			t.Errorf("totals = %+v, want one slot with two DIMMs", got)
		}
	})
}

// readTree imports a host tree the way a scan of that host would.
func readTree(t *testing.T, root string, at time.Time) (*store.Store, *ingest.Result) {
	t.Helper()
	hi := hostinfo.NewRoot(root, hostinfo.Options{Now: at})
	info, _ := hi.Read()
	s := newStore(t)
	res, err := ingest.Run(ctx, s, []adapter.Adapter{
		hi,
		rasdaemon.New(filepath.Join(root, "var", "lib", "rasdaemon", "ras-mc_event.db"), rasdaemon.Options{TempDir: t.TempDir()}),
		klog.NewFile(filepath.Join(root, "var", "log", "dmesg"), klog.Options{BootTime: info.BootTime, BootID: info.BootID}),
		edacsysfs.NewRoot(root, edacsysfs.Options{Now: at}),
		smbios.NewRoot(root, smbios.Options{Now: at}),
	}, ingest.Options{Kind: model.ScanLive, Now: at})
	if err != nil {
		t.Fatal(err)
	}
	return s, res
}

func TestHostTreeRoundTrip(t *testing.T) {
	ds := generate(t, "demo", 1, nil)
	last := ds.Boots[len(ds.Boots)-1]
	inLastBoot := 0
	for i := range ds.Events {
		if ds.Events[i].BootID == last.BootID {
			inLastBoot++
		}
	}
	for _, fl := range Flavors {
		t.Run(string(fl), func(t *testing.T) {
			root := t.TempDir()
			if err := ds.WriteHostTree(root, fl); err != nil {
				t.Fatal(err)
			}
			s, res := readTree(t, root, ds.Scenario.End)
			if res.Host != "r12-n034" {
				t.Errorf("host = %q", res.Host)
			}
			for _, src := range res.Sources {
				if src.Status != model.SourceOK {
					t.Errorf("source %s: %s %s %q", src.Kind, src.Status, src.Detail, src.Notes)
				}
			}
			// Every EDAC position is tied to its SMBIOS slot, so errors
			// land on serial numbers.
			var mapNote string
			for _, src := range res.Sources {
				if src.Kind == ingest.SlotMapKind {
					mapNote = strings.Join(src.Notes, " ")
				}
			}
			if !strings.Contains(mapNote, "EDAC DIMM positions are tied to SMBIOS slots") {
				t.Errorf("slot map = %q", mapNote)
			}
			// The ledger counts every error once although the database,
			// the kernel log and the counters all report the last boot.
			want := []total{
				{Slot: "CPU0_C1", Serial: ds.Truth[0].Serial, CE: 1231, UE: 1},
				{Slot: "CPU1_D1", Serial: ds.Truth[1].Serial, CE: 53},
			}
			if got := totals(t, s, "r12-n034"); !reflect.DeepEqual(got, want) {
				t.Errorf("totals\n got: %+v\nwant: %+v", got, want)
			}
			if res.Duplicates != inLastBoot {
				t.Errorf("%d duplicates, want %d: the kernel log lines of the last boot", res.Duplicates, inLastBoot)
			}

			// What survives of the location depends on the platform. The
			// row fault of the demo leaks into the next row.
			rowOK := func(r model.Opt[int]) bool { return r == model.Some(0x1a2f) || r == model.Some(0x1a30) }
			evs, _ := s.EventList(ctx, store.EventFilter{Host: "r12-n034", Slot: "CPU0_C1", Kinds: []model.EventKind{model.KindError}})
			if len(evs) != 1232 {
				t.Fatalf("%d located events on CPU0_C1", len(evs))
			}
			for _, e := range evs {
				switch fl {
				case FlavorIntel:
					if e.Grade != model.GradeA || !rowOK(e.Row) || e.Bank != model.Some(3) || e.DeviceId.Valid {
						t.Fatalf("intel: %+v", e)
					}
				case FlavorAMD:
					if e.Grade != model.GradeB || e.Row.Valid || !e.OsPhysicalAddress.Valid || e.Rank != model.Some(0) {
						t.Fatalf("amd: %+v", e)
					}
				case FlavorGHES:
					if e.Grade != model.GradeA || !rowOK(e.Row) || e.DeviceId != model.Some(7) || e.ErrorVector == "" {
						t.Fatalf("ghes: %+v", e)
					}
				}
			}
			// The uncorrected error and its page offline arrive through two sources.
			ue, _ := s.EventList(ctx, store.EventFilter{Host: "r12-n034", Kinds: []model.EventKind{model.KindError},
				Severities: []model.Severity{model.SevUE, model.SevRecoverable, model.SevFatal}})
			if len(ue) != 1 || !ue[0].ErrorDT.Equal(time.Date(2026, 11, 12, 3, 14, 0, 0, time.UTC)) {
				t.Errorf("uncorrected events = %+v", ue)
			}
			boots, _ := s.Boots(ctx, "r12-n034")
			if len(boots) != 1 || boots[0].BootID != last.BootID || boots[0].Derived || !boots[0].StartedAt.Equal(last.StartedAt) {
				t.Errorf("boots = %+v", boots)
			}
			d, _ := s.DIMM(ctx, model.DIMMUID("Example Memory", "EXM64G48R2X4-A", ds.Truth[0].Serial))
			if d == nil || d.SizeBytes != 64<<30 || d.MemoryType != "DDR5" || d.Organization() != "2Rx4" {
				t.Errorf("DIMM = %+v", d)
			}
		})
	}
	if err := ds.WriteHostTree(t.TempDir(), "sparc"); err == nil {
		t.Error("unknown flavor accepted")
	}
}
