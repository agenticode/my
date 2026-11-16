package synth

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"time"

	"github.com/agenticode/my/internal/model"
)

// FleetOptions describe a made-up fleet: many hosts with faults of every
// mode, for runs that measure how the tool behaves on large inputs.
type FleetOptions struct {
	// Seed makes the fleet repeatable: the same options give the same
	// scenarios.
	Seed uint64
	// Hosts is the number of hosts.
	Hosts int
	// Events is the number of corrected error events in the fleet. The
	// result holds about as many: noise and rounding add a few.
	Events int
	// Days is the length of the history of every host; zero means 180.
	Days int
}

// fleetEnd is the day every history of a fleet ends at. It is fixed so
// that the same options give the same data on any day.
var fleetEnd = day(2026, 11, 13)

// modeShares is how the faults of a fleet divide into modes. Faults of one
// cell are the most frequent kind in the field studies of DRAM errors and
// faults of whole devices and DIMMs the rarest; the shares follow that
// order and are otherwise a choice.
var modeShares = []struct {
	mode  model.FaultMode
	share float64
}{
	{model.ModeSingleBit, 0.45},
	{model.ModeRow, 0.20},
	{model.ModeColumn, 0.12},
	{model.ModeBank, 0.08},
	{model.ModeDevice, 0.06},
	{model.ModeMultiRow, 0.04},
	{model.ModeDIMM, 0.03},
	{model.ModeSystemIssue, 0.02},
}

// hostShapes are the machines of a fleet with their shares: two sockets
// with one DIMM per channel, two sockets fully populated, one socket.
var hostShapes = []struct {
	sockets int
	all     bool
	product string
	share   float64
}{
	{2, false, "R760", 0.60},
	{2, true, "R860", 0.25},
	{1, false, "R360", 0.15},
}

var fleetKernels = []string{"5.14.0-570.12.1.el9_6.x86_64", "5.14.0-427.13.1.el9_4.x86_64", "6.8.0-45-generic", "6.12.0-55.9.1.el10_0.x86_64"}

// Fleet returns one scenario per host. The error events are divided among
// the faults of the fleet with a heavy tail, as error counts in a real
// fleet are: most faults have a few events and a few faults have most of
// them. With one host the events go to a handful of faults of that host,
// the history of a machine in an error storm.
//
// Every host has its own seed, so that no two hold a DIMM with the same
// serial number.
func Fleet(o FleetOptions) ([]*Scenario, error) {
	if o.Hosts < 1 || o.Events < 1 {
		return nil, fmt.Errorf("a fleet has at least one host and one event (asked for %d hosts, %d events)", o.Hosts, o.Events)
	}
	days := o.Days
	if days <= 0 {
		days = 180
	}
	if days < 14 {
		return nil, fmt.Errorf("a history of %d days is too short: a fleet needs 14", days)
	}
	rng := rand.New(rand.NewPCG(o.Seed, o.Seed^0xda942042e4dd58b5))
	start := fleetEnd.AddDate(0, 0, -days)
	span := fleetEnd.Sub(start)
	at := func(share float64) time.Time {
		return start.Add(time.Duration(share * float64(span))).Truncate(time.Minute)
	}

	out := make([]*Scenario, o.Hosts)
	type place struct{ host, fault int }
	var faults []place
	for i := range out {
		shape := hostShapes[len(hostShapes)-1]
		u := rng.Float64()
		for _, s := range hostShapes {
			if u < s.share {
				shape = s
				break
			}
			u -= s.share
		}
		name := fmt.Sprintf("node-%04d", i+1)
		h := DefaultHost(name)
		h.Sockets, h.PopulateAll, h.SystemProduct = shape.sockets, shape.all, shape.product
		h.Kernel = fleetKernels[rng.IntN(len(fleetKernels))]
		h.SerialDigits = 10
		sc := &Scenario{
			Name: fmt.Sprintf("fleet-%d-%04d", o.Seed, i+1), Seed: o.Seed*1_000_003 + uint64(i) + 1, Host: h,
			Start: start, End: fleetEnd, NoisePerDIMMDay: 0.001 + 0.004*rng.Float64(),
		}
		// Up to three reboots, each at its own minute.
		for range rng.IntN(4) {
			sc.Reboots = append(sc.Reboots, Reboot{At: at(0.05 + 0.9*rng.Float64())})
		}
		sort.Slice(sc.Reboots, func(a, b int) bool { return sc.Reboots[a].At.Before(sc.Reboots[b].At) })
		for j := len(sc.Reboots) - 1; j > 0; j-- {
			if !sc.Reboots[j].At.After(sc.Reboots[j-1].At) {
				sc.Reboots = append(sc.Reboots[:j], sc.Reboots[j+1:]...)
			}
		}

		// One host in five has no fault, only stray errors. The others
		// have one fault, some two or three. A single host is the host of
		// a storm and has four.
		n := 0
		switch u := rng.Float64(); {
		case o.Hosts == 1:
			n = 4
		case u < 0.20:
			n = 0
		case u < 0.80:
			n = 1
		case u < 0.95:
			n = 2
		default:
			n = 3
		}
		used := map[string]bool{}
		for range n {
			f := Fault{Trend: Flat, PatrolShare: 0.6 * rng.Float64()}
			u := rng.Float64()
			f.Mode = modeShares[len(modeShares)-1].mode
			for _, m := range modeShares {
				if u < m.share {
					f.Mode = m.mode
					break
				}
				u -= m.share
			}
			// A populated slot no other fault of the host sits in.
			for try := 0; try < 64 && (f.Slot == "" || used[f.Slot]); try++ {
				f.Slot = h.SlotName(rng.IntN(h.Sockets), rng.IntN(h.IMCs), rng.IntN(h.ChannelsPerIMC), 0)
			}
			used[f.Slot] = true
			f.Start = at(0.85 * rng.Float64())
			switch u := rng.Float64(); {
			case u < 0.30:
				f.Trend = Rising
			case u < 0.50:
				f.Trend = Burst
			}
			f.StaysWithSlot = f.Mode == model.ModeSystemIssue
			sc.Faults = append(sc.Faults, f)
			faults = append(faults, place{i, len(sc.Faults) - 1})
		}
		out[i] = sc
	}
	if len(faults) == 0 {
		// A small fleet can come out without a fault: the events need one.
		h := out[0].Host
		out[0].Faults = []Fault{{Mode: model.ModeRow, Slot: h.SlotName(0, 0, 0, 0), Start: at(0.2), Trend: Rising, PatrolShare: 0.3}}
		faults = append(faults, place{0, 0})
	}

	// The events: a weight per fault from a Pareto distribution of shape
	// 1, whose largest values are a large part of any sample, cut off so
	// that no fault holds more than two fifths of the fleet, then scaled
	// to the number asked for.
	weights := make([]float64, len(faults))
	sum := 0.0
	for i := range weights {
		weights[i] = 1 / math.Max(1-rng.Float64(), 1e-9)
		sum += weights[i]
	}
	for range 8 {
		limit, over := 0.4*sum, false
		for i, w := range weights {
			if len(weights) > 2 && w > limit {
				sum -= w - limit
				weights[i] = limit
				over = true
			}
		}
		if !over {
			break
		}
	}
	for i, p := range faults {
		sc := out[p.host]
		f := &sc.Faults[p.fault]
		f.Events = max(1, int(weights[i]/sum*float64(o.Events)+0.5))

		// What happens around a fault with enough errors to matter.
		if f.Events < 40 {
			continue
		}
		life := fleetEnd.Sub(f.Start)
		late := func(share float64) time.Time {
			return f.Start.Add(time.Duration(share * float64(life))).Truncate(time.Second)
		}
		switch u := rng.Float64(); {
		case u < 0.04:
			f.UE = []time.Time{late(0.7 + 0.29*rng.Float64())}
		case u < 0.06:
			f.PatrolUE = []time.Time{late(0.7 + 0.29*rng.Float64())}
		}
		// A row is repaired at a reboot that follows the start of its
		// fault, and half of the repairs end the errors.
		if f.Mode == model.ModeRow && rng.Float64() < 0.25 {
			for _, r := range sc.Reboots {
				if r.At.After(f.Start.Add(24 * time.Hour)) {
					f.StopAfterRepair = rng.Float64() < 0.5
					sc.Repairs = append(sc.Repairs, Repair{At: r.At, Kind: model.ActionHPPR, Fault: p.fault})
					break
				}
			}
		}
		// Pages with many corrected errors are taken offline. A fault
		// that bursts at its end, or that a repair ended early, may have
		// no error before that time.
		if f.Trend != Burst && !f.StopAfterRepair && f.Mode != model.ModeSystemIssue && rng.Float64() < 0.15 {
			sc.Offlines = append(sc.Offlines, Offline{At: late(0.8), Fault: p.fault, Pages: 1 + rng.IntN(4)})
		}
	}
	return out, nil
}
