// Package engine is the analysis engine of MemorY. It reads the event
// ledger and gives one finding per DIMM.
//
// Classify groups the errors of a DIMM into faults and gives each fault a
// Level 1 mode, as far as the observation grade of its records allows.
//
// Analyze builds on that. It measures every fault and DIMM (trend, spread,
// storms, failing data lines, what happened after repairs), ranks the DIMM
// against its own past and against the DIMMs of its part number, and
// applies the rules. A finding holds a grade, the IDs of the rules that
// fired, the evidence with the ledger records behind it, checks against
// other explanations, the data that was missing and the next step.
//
// Run does this for a ledger, and Result.Save stores faults and findings
// in it.
//
// The engine uses rules only. Every step can be stated in one line and
// traced to the ledger records it rests on. It reads; it never changes a
// host and takes no repair or isolation step.
package engine

import (
	"sort"
	"time"

	"github.com/agenticode/my/internal/model"
)

// Version identifies the behaviour of the engine. It changes whenever a
// change can give a different fault or finding for the same ledger. Stored
// faults and findings carry the version that produced them.
const Version = "1"

// Options select how the engine runs. The zero value uses the defaults.
type Options struct {
	// Config holds the thresholds; nil means DefaultConfig.
	Config *Config
	// Adjacency says which rows are physical neighbours; nil means
	// LogicalAdjacency.
	Adjacency Adjacency
	// PartBaseline supplies reference error rates per part number, the
	// third baseline layer; nil means there is none.
	PartBaseline PartBaseline
	// Host limits the result to one host. The whole ledger is still read:
	// a DIMM keeps the history it has from other hosts.
	Host string
	// AsOf sets the reference time and leaves out every record after it.
	// The zero value uses, per host, the latest time the ledger knows
	// something about the host.
	AsOf time.Time
	// ObservedFrom says that every host was watched from this time on:
	// a host without records before its first error had no errors then.
	// The zero value takes the earliest record of each host as the start
	// of what is known about it. A replay of a fleet log sets it to the
	// start of the log; without it a fault cannot be called new until the
	// host has a week of records.
	ObservedFrom time.Time
	// Now is stored with findings as the time they were computed; the zero
	// value means the current time. It does not change any finding.
	Now time.Time
}

func (o Options) config() *Config {
	if o.Config != nil {
		c := *o.Config
		return &c
	}
	c := DefaultConfig()
	return &c
}

func (o Options) adjacency() Adjacency {
	if o.Adjacency != nil {
		return o.Adjacency
	}
	return LogicalAdjacency{}
}

// DIMMFaults holds the faults of one DIMM.
type DIMMFaults struct {
	// UID is the DIMM identity the errors are attributed to.
	UID string
	// DIMM describes the module, when the ledger has a record of it.
	DIMM *model.DIMM
	// Faults are ordered by error count, largest first.
	Faults []*Fault
}

// Unattributed counts errors the ledger could not tie to a DIMM. They take
// part in no fault; a reader must be told that they exist.
type Unattributed struct {
	Host string `json:"host"`
	// Slot is the slot the errors were reported for, or empty when the
	// records name no place at all.
	Slot string `json:"slot,omitzero"`
	// Ambiguous is set when the slot held different DIMMs before and after
	// the errors and the time of the swap is unknown.
	Ambiguous bool  `json:"ambiguous,omitzero"`
	CE        int64 `json:"ce"`
	UE        int64 `json:"ue"`
	// Records is the number of ledger records behind the counts.
	Records int `json:"records"`
}

// Classification is the result of clustering a ledger.
type Classification struct {
	// DIMMs is ordered by DIMM identity.
	DIMMs []DIMMFaults
	// Unattributed is ordered by host and slot.
	Unattributed []Unattributed
}

// Classify groups the errors of every DIMM in the input into faults and
// assigns each fault a Level 1 mode, as far as the observation grade of its
// records allows.
func Classify(in *Input, opts Options) *Classification {
	cfg, adj := opts.config(), opts.adjacency()

	byDIMM := in.byDIMM()
	type looseKey struct {
		host, slot int32
		ambiguous  bool
	}
	loose := map[looseKey]*Unattributed{}
	for i := range in.events {
		e := &in.events[i]
		if e.dimm >= 0 {
			continue
		}
		k := looseKey{e.host, e.slot, e.link == linkAmbiguous}
		u := loose[k]
		if u == nil {
			u = &Unattributed{Host: in.hostNames[e.host], Ambiguous: k.ambiguous}
			if e.slot >= 0 {
				u.Slot = in.slots[e.slot].name
			}
			loose[k] = u
		}
		u.Records++
		if e.sev == sevCE {
			u.CE += int64(e.errors)
		} else {
			u.UE += int64(e.errors)
		}
	}

	info := map[string]*model.DIMM{}
	for i := range in.DIMMs {
		info[in.DIMMs[i].UID] = &in.DIMMs[i]
	}
	res := &Classification{}
	for d, evs := range byDIMM {
		if len(evs) == 0 {
			continue
		}
		uid := in.dimms[d]
		cl := &clusterer{in: in, cfg: cfg, adj: adj, uid: uid, dimm: info[uid]}
		res.DIMMs = append(res.DIMMs, DIMMFaults{UID: uid, DIMM: info[uid], Faults: cl.classify(evs)})
	}
	sort.Slice(res.DIMMs, func(i, j int) bool { return res.DIMMs[i].UID < res.DIMMs[j].UID })
	for _, u := range loose {
		res.Unattributed = append(res.Unattributed, *u)
	}
	sort.Slice(res.Unattributed, func(i, j int) bool {
		a, b := res.Unattributed[i], res.Unattributed[j]
		if a.Host != b.Host {
			return a.Host < b.Host
		}
		if a.Slot != b.Slot {
			return a.Slot < b.Slot
		}
		return !a.Ambiguous && b.Ambiguous
	})
	return res
}

// byDIMM returns, per DIMM index, the records attributed to the DIMM.
func (in *Input) byDIMM() [][]int32 {
	out := make([][]int32, len(in.dimms))
	for i := range in.events {
		if d := in.events[i].dimm; d >= 0 {
			out[d] = append(out[d], int32(i))
		}
	}
	return out
}

// EventIDs returns the ledger IDs of the member records of a fault, in time
// order.
func (in *Input) EventIDs(f *Fault) []int64 {
	out := make([]int64, len(f.members))
	for i, m := range f.members {
		out[i] = in.events[m].id
	}
	return out
}
