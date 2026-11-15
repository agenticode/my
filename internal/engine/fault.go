package engine

import (
	"sort"

	"github.com/agenticode/my/internal/model"
)

// Reasons a fault mode is an estimate.
const (
	// EstimateAddressOnly: the errors are known by physical address only,
	// and the mode was inferred from how close the addresses are.
	EstimateAddressOnly = "address-only"
	// EstimateDeviceUnknown: the platform does not report the DRAM device,
	// so the errors may come from more than one device. The mode is the
	// smallest extent the data allows.
	EstimateDeviceUnknown = "device-unknown"
)

// Shape counts the distinct places a fault touched.
type Shape struct {
	// Cells is the number of distinct locations: bank, row and column for
	// located errors, cache line addresses for errors known by address.
	Cells int `json:"cells"`
	// Rows and Columns count distinct rows and columns within their bank.
	Rows    int `json:"rows,omitzero"`
	Columns int `json:"columns,omitzero"`
	Banks   int `json:"banks,omitzero"`
	// Devices counts distinct DRAM devices, where the platform reports them.
	Devices int `json:"devices,omitzero"`
	Ranks   int `json:"ranks,omitzero"`
	// Pages counts distinct 4 KiB pages, where addresses are known.
	Pages int `json:"pages,omitzero"`
}

// Fault is a group of errors of one DIMM that share a cause, as far as
// their locations show, with what the engine measured about the group.
type Fault struct {
	model.Fault

	// EstimatedBy says why the mode is an estimate (Estimate* constants),
	// or is empty.
	EstimatedBy string `json:"estimated_by,omitzero"`
	// Recurring is set when the place failed again after the repeat gap:
	// the cause did not go away with one correction.
	Recurring bool `json:"recurring"`
	// Confined is set when the errors stay within one cell, row, column or
	// bank of one device and came back there. A cause outside the DIMM
	// (slot, channel, board) does not confine itself that way.
	Confined bool `json:"confined"`
	// Shape counts the distinct places involved.
	Shape Shape `json:"shape"`
	// CE and UE are the numbers of corrected and uncorrected errors.
	CE int64 `json:"ce"`
	UE int64 `json:"ue"`
	// TopRow is the row with the most errors of a row or multi-row fault.
	TopRow model.Opt[int] `json:"top_row,omitzero"`
	// Slots lists the slots the errors were reported in, oldest first. A
	// DIMM that was moved has more than one.
	Slots []string `json:"slots,omitzero"`

	// The measurements below are filled in by Analyze; Classify leaves
	// them empty.

	// Trend compares the recent errors of the fault with the baseline
	// window before them.
	Trend Trend `json:"trend"`
	// Spread says how many of the fault's places are new.
	Spread Spread `json:"spread"`
	// Storm is the densest burst of the fault's errors.
	Storm Storm `json:"storm"`
	// Lines summarises the failing data lines, where records name them.
	Lines DataLines `json:"data_lines"`
	// Scrub splits the errors by how they were found.
	Scrub Scrub `json:"scrub"`
	// Mitigations are the repairs and page offlines that name a place of
	// the fault, oldest first.
	Mitigations []Mitigation `json:"mitigations,omitzero"`
	// Undated counts errors known by seconds since boot only.
	Undated int64 `json:"undated,omitzero"`

	// members are the indexes of the member records in Input.events, in
	// time order.
	members []int32
}

// Where names the place of the fault in the words the findings use, for
// example "rank0 dev7 bg0 bank3 row 0x1a2f". It is empty for a fault that
// covers several banks of several devices: such a fault has no single place.
func (f *Fault) Where() string { return where(f) }

// modeExtent orders modes by how much of a DIMM they cover.
func modeExtent(m model.FaultMode) int {
	for i, x := range model.FaultModes {
		if x == m {
			return i + 1
		}
	}
	return 0
}

type shapeCounter struct {
	cells, rows, cols map[cellKey]struct{}
	banks             map[bankKey]struct{}
	devs              map[devKey]struct{}
	ranks             map[int16]struct{}
	pages, lines      map[addrKey]struct{}
}

type addrKey struct {
	slot int32
	addr uint64
}

// newFault builds a fault from its member records.
func (cl *clusterer) newFault(mode model.FaultMode, grade model.Grade, members []int32) *Fault {
	evs := cl.in.events
	sort.Slice(members, func(i, j int) bool {
		a, b := &evs[members[i]], &evs[members[j]]
		if a.relative != b.relative {
			return a.relative // records without a date sort first
		}
		if a.at != b.at {
			return a.at < b.at
		}
		return a.id < b.id
	})
	f := &Fault{members: members}
	f.Mode, f.Grade, f.EngineVersion = mode, grade, Version
	f.DIMMUID = cl.uid
	f.EventCount = len(members)

	sc := shapeCounter{
		cells: map[cellKey]struct{}{}, rows: map[cellKey]struct{}{}, cols: map[cellKey]struct{}{},
		banks: map[bankKey]struct{}{}, devs: map[devKey]struct{}{}, ranks: map[int16]struct{}{},
		pages: map[addrKey]struct{}{}, lines: map[addrKey]struct{}{},
	}
	var first, last int64
	var dated bool
	uniform := func(cur *int16, set *bool, v int16) {
		switch {
		case !*set:
			*cur, *set = v, true
		case *cur != v:
			*cur = -1
		}
	}
	var rank, dev, bg, bank int16
	var rankSet, devSet, bgSet, bankSet bool
	rowMin, rowMax, colMin, colMax := int32(-1), int32(-1), int32(-1), int32(-1)
	var addrMin, addrMax uint64
	var hasAddr bool
	seenSlot := map[int32]bool{}
	located := grade == model.GradeA

	for _, m := range members {
		e := &evs[m]
		f.ErrorCount += int(e.errors)
		if e.sev == sevCE {
			f.CE += int64(e.errors)
		} else {
			f.UE += int64(e.errors)
		}
		if !e.relative {
			if !dated || e.at < first {
				first = e.at
			}
			if !dated || e.at > last {
				last = e.at
			}
			dated = true
		}
		if e.slot >= 0 && !seenSlot[e.slot] {
			seenSlot[e.slot] = true
			f.Slots = append(f.Slots, cl.in.slots[e.slot].name)
		}
		if e.slot >= 0 {
			// The latest record names the place the fault is at now.
			f.Slot, f.Host = cl.in.slots[e.slot].name, cl.in.hostNames[e.host]
		} else if f.Host == "" {
			f.Host = cl.in.hostNames[e.host]
		}
		uniform(&rank, &rankSet, e.rank)
		uniform(&dev, &devSet, e.dev)
		uniform(&bg, &bgSet, e.bg)
		uniform(&bank, &bankSet, e.bank)
		if e.rank >= 0 {
			sc.ranks[e.rank] = struct{}{}
		}
		if e.dev >= 0 {
			sc.devs[devKey{e.rank, e.dev}] = struct{}{}
		}
		if e.hasAddr {
			if !hasAddr || e.addr < addrMin {
				addrMin = e.addr
			}
			if !hasAddr || e.addr > addrMax {
				addrMax = e.addr
			}
			hasAddr = true
			sc.pages[addrKey{e.slot, e.addr >> 12}] = struct{}{}
			sc.lines[addrKey{e.slot, e.addr >> 6}] = struct{}{}
		}
		if located && e.located() {
			k := cellKey{e.rank, e.dev, e.bg, e.bank, e.row, e.col}
			sc.cells[k] = struct{}{}
			k.col = -1
			sc.rows[k] = struct{}{}
			k.row, k.col = -1, e.col
			sc.cols[k] = struct{}{}
			sc.banks[bankKey{e.rank, e.dev, e.bg, e.bank}] = struct{}{}
			if rowMin < 0 || e.row < rowMin {
				rowMin = e.row
			}
			if e.row > rowMax {
				rowMax = e.row
			}
			if colMin < 0 || e.col < colMin {
				colMin = e.col
			}
			if e.col > colMax {
				colMax = e.col
			}
		}
	}
	if !dated && len(members) > 0 {
		// Only seconds since boot are known. The fault keeps those offsets;
		// a reader has to show them as offsets.
		first, last = evs[members[0]].at, evs[members[len(members)-1]].at
	}
	f.FirstSeen, f.LastSeen = timeOf(first), timeOf(last)

	opt := func(v int16, set bool) model.Opt[int] {
		if !set || v < 0 {
			return model.Opt[int]{}
		}
		return model.Some(int(v))
	}
	f.Rank, f.DeviceId = opt(rank, rankSet), opt(dev, devSet)
	if hasAddr {
		f.AddressMin, f.AddressMax = model.Some(addrMin), model.Some(addrMax)
	}
	f.Shape = Shape{Ranks: len(sc.ranks), Devices: len(sc.devs), Pages: len(sc.pages)}
	if located {
		f.Shape.Cells, f.Shape.Rows, f.Shape.Columns, f.Shape.Banks = len(sc.cells), len(sc.rows), len(sc.cols), len(sc.banks)
		if len(sc.banks) == 1 {
			// Rows and columns only mean something inside one bank.
			f.BankGroup, f.Bank = opt(bg, bgSet), opt(bank, bankSet)
			f.RowMin, f.RowMax = model.Some(int(rowMin)), model.Some(int(rowMax))
			f.ColumnMin, f.ColumnMax = model.Some(int(colMin)), model.Some(int(colMax))
		}
	} else {
		f.Shape.Cells = len(sc.lines)
	}
	gap := cl.cfg.repeatGap()
	f.Recurring = f.ErrorCount >= 2 && ((dated && last-first >= gap) || f.ErrorCount >= cl.cfg.RepeatCount)
	return f
}

// sortFaults puts the faults of a DIMM in a fixed order: most errors first,
// then the wider mode, then the older fault.
func sortFaults(fs []*Fault) {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if a.ErrorCount != b.ErrorCount {
			return a.ErrorCount > b.ErrorCount
		}
		if x, y := modeExtent(a.Mode), modeExtent(b.Mode); x != y {
			return x > y
		}
		if !a.FirstSeen.Equal(b.FirstSeen) {
			return a.FirstSeen.Before(b.FirstSeen)
		}
		return a.members[0] < b.members[0]
	})
}
