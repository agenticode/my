package testledger

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/synth"
)

// Pair describes two cases of one synthetic host: the host as it was at
// the cut, and as it is at the end of the scenario. They are what an
// operator has who took a case, changed something on the host, and took a
// second case later.
type Pair struct {
	// Name keys the pair among the ledgers a test binary shares.
	Name string
	// Scenario and Seed choose the history of the host.
	Scenario string
	Seed     uint64
	// Mut, when not nil, changes the scenario before it runs.
	Mut func(*synth.Scenario)
	// Cut is the time the earlier case was taken at.
	Cut time.Time
	// Later, when not nil, changes the history after the cut: it gets the
	// whole data set once the earlier case has been cut from it. A DIMM
	// that is replaced between the cases is made here.
	Later func(*synth.Dataset)
	// Flavor says how the cases are made. With an empty flavor the data is
	// stored directly, and the later case holds where every DIMM sat
	// during every boot, as a ledger that is kept from scan to scan does.
	// With a flavor each case is a scan of the files a host of that
	// platform holds at that time: it knows where the DIMMs are now and
	// nothing about where they were.
	Flavor synth.Flavor
}

// Cut returns a data set as the host held it at a time: the boots that had
// begun, the records up to then, and the DIMMs where they sat.
func Cut(ds *synth.Dataset, at time.Time) *synth.Dataset {
	sc := *ds.Scenario
	sc.End = at
	out := &synth.Dataset{Scenario: &sc, Slots: ds.Slots, Modules: ds.Modules, Truth: ds.Truth}
	for i, b := range ds.Boots {
		if b.StartedAt.After(at) {
			break
		}
		if b.LastSeen.After(at) {
			b.LastSeen = at
		}
		out.Boots = append(out.Boots, b)
		out.Occupancy = append(out.Occupancy, ds.Occupancy[i])
	}
	for _, e := range ds.Events {
		if !e.ErrorDT.After(at) {
			out.Events = append(out.Events, e)
		}
	}
	for _, a := range ds.Actions {
		if !a.At.After(at) {
			out.Actions = append(out.Actions, a)
		}
	}
	return out
}

// Paths returns the ledger files of the two cases, the earlier one first.
// They are built once per test binary.
func (p Pair) Paths(tb testing.TB) (before, after string) {
	tb.Helper()
	shared.Lock()
	defer shared.Unlock()
	keyA, keyB := "pair-"+p.Name+"-before", "pair-"+p.Name+"-after"
	if a, ok := shared.paths[keyA]; ok {
		return a, shared.paths[keyB]
	}
	if shared.dir == "" {
		dir, err := os.MkdirTemp("", "my-testledger-")
		if err != nil {
			tb.Fatal(err)
		}
		shared.dir, shared.ledgers, shared.paths = dir, map[string]*store.Store{}, map[string]string{}
	}
	ds := Generate(tb, p.Scenario, p.Seed, p.Mut)
	if !p.Cut.After(ds.Scenario.Start) || !p.Cut.Before(ds.Scenario.End) {
		tb.Fatalf("pair %s: the cut %s is outside the scenario (%s to %s)", p.Name, p.Cut, ds.Scenario.Start, ds.Scenario.End)
	}
	early := Cut(ds, p.Cut)
	if p.Later != nil {
		p.Later(ds)
	}
	for _, c := range []struct {
		key string
		ds  *synth.Dataset
		out *string
	}{{keyA, early, &before}, {keyB, ds, &after}} {
		dir := filepath.Join(shared.dir, c.key)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			tb.Fatal(err)
		}
		path, err := build(context.Background(), c.ds, p.Flavor, dir)
		if err != nil {
			tb.Fatalf("ledger of %s: %v", c.key, err)
		}
		shared.paths[c.key], *c.out = path, path
	}
	return before, after
}

// Replace puts another module into a slot from a time on: the module that
// was there leaves the host. The errors recorded in the slot stay as they
// are, so a fault that stays with the slot goes on with the new module;
// DropErrors takes the later errors of a fault that left with the module.
func Replace(ds *synth.Dataset, slot string, at time.Time, serial string) {
	index := -1
	for _, s := range ds.Slots {
		if s.Name == slot {
			index = s.Index
		}
	}
	if index < 0 {
		panic("testledger: no slot " + slot)
	}
	for i, b := range ds.Boots {
		if b.StartedAt.Before(at) {
			continue
		}
		old := ds.Occupancy[i][index]
		if old < 0 {
			panic("testledger: slot " + slot + " is empty")
		}
		m := ds.Modules[old]
		m.Serial = serial
		m.UID = model.DIMMUID(m.Manufacturer, m.PartNumber, m.Serial)
		ds.Modules = append(ds.Modules, m)
		for j := i; j < len(ds.Boots); j++ {
			row := append([]int(nil), ds.Occupancy[j]...)
			row[index] = len(ds.Modules) - 1
			ds.Occupancy[j] = row
		}
		return
	}
	panic("testledger: no boot at or after the replacement")
}

// DropErrors removes the errors recorded in a slot from a time on.
func DropErrors(ds *synth.Dataset, slot string, from time.Time) {
	kept := ds.Events[:0:0]
	for _, e := range ds.Events {
		if e.Label == slot && !e.ErrorDT.Before(from) {
			continue
		}
		kept = append(kept, e)
	}
	ds.Events = kept
}

// The pairs the comparison of two cases is tested on. In each the earlier
// case is taken on 2026-09-28 and something is done to the host at the
// restart of 2026-10-01 03:00.
var (
	pairCut    = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	pairChange = time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC)
)

// PairChange is the time of the restart at which the pairs change the host.
func PairChange() time.Time { return pairChange }

// DiffPair returns one of the pairs by name:
//
//	moved          two DIMMs swap slots and the errors go with the DIMM
//	moved-scan     the same, each case a scan of the host's files
//	slot           two DIMMs swap slots and the errors stay in the slot
//	slot-scan      the same, each case a scan of the host's files
//	to-empty       a DIMM moves to a slot that was empty
//	reseat         a DIMM is reseated and its errors stop
//	firmware       BIOS and kernel are updated and the errors go on
//	repaired       a row is repaired at the restart and stays quiet
//	replaced       a DIMM is replaced and the slot is quiet afterwards
//	replaced-slot  a DIMM is replaced and the errors stay in the slot
func DiffPair(tb testing.TB, name string) Pair {
	tb.Helper()
	p := Pair{Name: name, Seed: 1, Cut: pairCut}
	switch name {
	case "moved":
		p.Scenario = "moved-dimm"
	case "moved-scan":
		p.Scenario, p.Flavor = "moved-dimm", synth.FlavorIntel
	case "slot":
		p.Scenario = "system-slot"
	case "slot-scan":
		p.Scenario, p.Flavor = "system-slot", synth.FlavorIntel
	case "to-empty":
		p.Scenario = "moved-dimm"
		p.Mut = func(sc *synth.Scenario) { sc.Moves = []synth.Move{{At: pairChange, A: "CPU0_C1", B: "CPU0_C2"}} }
	case "reseat":
		p.Scenario = "reseated"
	case "firmware":
		p.Scenario = "column"
		p.Mut = func(sc *synth.Scenario) {
			sc.Reboots[1].BIOS = "Example BIOS Inc. 2.6.1 09/18/2026"
			sc.Reboots[1].Kernel = "5.14.0-611.5.1.el9_7.x86_64"
		}
	case "repaired":
		p.Scenario = "repaired"
	case "replaced":
		p.Scenario = "bank"
		p.Later = func(ds *synth.Dataset) {
			Replace(ds, "CPU1_A1", pairChange, "5A11C3")
			DropErrors(ds, "CPU1_A1", pairChange)
		}
	case "replaced-slot":
		p.Scenario = "system-slot"
		p.Mut = func(sc *synth.Scenario) { sc.Moves = nil }
		p.Later = func(ds *synth.Dataset) { Replace(ds, "CPU0_C1", pairChange, "5A11C3") }
	default:
		tb.Fatalf("no pair named %q", name)
	}
	return p
}
