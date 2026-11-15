package synth

import (
	"reflect"
	"sort"
	"testing"

	"github.com/agenticode/my/internal/model"
)

// A fleet is repeatable, holds about the events that were asked for, every
// mode of fault, and scenarios the generator accepts.
func TestFleet(t *testing.T) {
	o := FleetOptions{Seed: 3, Hosts: 300, Events: 60000}
	fleet, err := Fleet(o)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Fleet(o)
	if err != nil || !reflect.DeepEqual(fleet, again) {
		t.Fatalf("the same options gave another fleet (%v)", err)
	}
	other, _ := Fleet(FleetOptions{Seed: 4, Hosts: 300, Events: 60000})
	if reflect.DeepEqual(fleet, other) {
		t.Error("another seed gave the same fleet")
	}
	if len(fleet) != o.Hosts {
		t.Fatalf("%d hosts", len(fleet))
	}

	names, seeds := map[string]bool{}, map[uint64]bool{}
	modes := map[model.FaultMode]int{}
	var perFault []int
	asked, faultless := 0, 0
	for _, sc := range fleet {
		if names[sc.Host.Name] || seeds[sc.Seed] || names[sc.Name] {
			t.Errorf("host %s or its seed %d is there twice", sc.Host.Name, sc.Seed)
		}
		names[sc.Host.Name], names[sc.Name], seeds[sc.Seed] = true, true, true
		if len(sc.Faults) == 0 {
			faultless++
		}
		slots := map[string]bool{}
		for _, f := range sc.Faults {
			modes[f.Mode]++
			asked += f.Events
			perFault = append(perFault, f.Events)
			if f.Events < 1 || slots[f.Slot] || !f.Start.Before(sc.End) || f.Start.Before(sc.Start) {
				t.Errorf("%s: fault %+v", sc.Host.Name, f)
			}
			slots[f.Slot] = true
		}
	}
	for _, m := range model.FaultModes {
		if modes[m] == 0 {
			t.Errorf("no fault of mode %s among %d", m, len(perFault))
		}
	}
	if modes[model.ModeSingleBit] <= modes[model.ModeRow] || modes[model.ModeRow] <= modes[model.ModeDIMM] {
		t.Errorf("faults by mode: %v", modes)
	}
	if faultless < o.Hosts/10 || faultless > o.Hosts/3 {
		t.Errorf("%d of %d hosts have no fault", faultless, o.Hosts)
	}
	if asked < o.Events*98/100 || asked > o.Events*102/100 {
		t.Errorf("the faults ask for %d events, the fleet for %d", asked, o.Events)
	}
	// A heavy tail: the largest tenth of the faults holds most events, and
	// no fault more than two fifths of them.
	sort.Sort(sort.Reverse(sort.IntSlice(perFault)))
	top := 0
	for _, n := range perFault[:len(perFault)/10] {
		top += n
	}
	if top < asked/2 || perFault[0] > asked*2/5+1 {
		t.Errorf("the largest tenth of the faults holds %d of %d events, the largest fault %d", top, asked, perFault[0])
	}

	// Every scenario runs, and the hosts hold about what was asked for.
	events, actions := 0, 0
	serials := map[string]string{}
	for _, sc := range fleet {
		ds, err := Generate(sc)
		if err != nil {
			t.Fatalf("%s: %v", sc.Name, err)
		}
		events += len(ds.Events)
		actions += len(ds.Actions)
		for _, m := range ds.Modules {
			if host, dup := serials[m.Serial]; dup {
				t.Errorf("serial number %s is in %s and in %s", m.Serial, host, sc.Host.Name)
			}
			serials[m.Serial] = sc.Host.Name
		}
	}
	if events < o.Events*95/100 || events > o.Events*110/100 {
		t.Errorf("%d events generated, %d asked for", events, o.Events)
	}
	if actions == 0 {
		t.Error("a fleet of 300 hosts without a repair, an offlined page or an uncorrected error")
	}
}

// One host is the host of a storm: all events, a handful of faults.
func TestFleetOfOneHost(t *testing.T) {
	fleet, err := Fleet(FleetOptions{Seed: 1, Hosts: 1, Events: 5000})
	if err != nil || len(fleet) != 1 || len(fleet[0].Faults) != 4 {
		t.Fatalf("%v, %d hosts", err, len(fleet))
	}
	ds, err := Generate(fleet[0])
	if err != nil {
		t.Fatal(err)
	}
	if n := len(ds.Events); n < 5000 || n > 5100 {
		t.Errorf("%d events", n)
	}
	// A tree of it can be written.
	if err := ds.WriteHostTree(t.TempDir(), FlavorIntel); err != nil {
		t.Fatal(err)
	}
}

func TestFleetOptions(t *testing.T) {
	for _, o := range []FleetOptions{{Hosts: 0, Events: 10}, {Hosts: 3, Events: 0}, {Hosts: 3, Events: 10, Days: 5}} {
		if _, err := Fleet(o); err == nil {
			t.Errorf("%+v was accepted", o)
		}
	}
	// Few hosts and few events still make a fleet with a fault.
	for seed := range uint64(40) {
		fleet, err := Fleet(FleetOptions{Seed: seed, Hosts: 2, Events: 3})
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, sc := range fleet {
			if _, err := Generate(sc); err != nil {
				t.Fatalf("seed %d: %v", seed, err)
			}
			n += len(sc.Faults)
		}
		if n == 0 {
			t.Errorf("seed %d: a fleet without a fault", seed)
		}
	}
}
