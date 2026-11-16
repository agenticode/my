package view

import (
	"context"
	"sort"
	"time"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/model"
)

// Boots is the view "boots": the latest boots of a host, how each of them
// ended, and per DIMM, or per place of one DIMM, what it showed in each
// boot. A fatal error ends its boot; on a hard fault every boot ends at the
// same place, and the days of a timeline hide such a loop of short boots.
type Boots struct {
	Host   string `json:"host"`
	System string `json:"system,omitzero"`
	// Slot and DIMM are set on the view of one DIMM: its places by boots.
	Slot string    `json:"slot,omitzero"`
	DIMM *DIMMInfo `json:"dimm,omitzero"`
	// OnRecord is the number of boots of the host on record. Boots holds
	// the latest of them, at most loop.window_boots, oldest first.
	OnRecord int       `json:"boots_on_record"`
	Boots    []BootCol `json:"boots"`
	// Rows are the DIMMs with errors in these boots, and a row for the
	// uncorrected errors that name no DIMM; on the view of one DIMM, its
	// places.
	Rows []BootRow `json:"rows"`
	// Others sums the places of a DIMM beyond the rows listed.
	Others *BootRow `json:"others,omitzero"`
	// Quiet is the number of DIMMs of the host without errors in these
	// boots.
	Quiet int `json:"quiet,omitzero"`
	// Stops is the summary of the stops of the window, nil without one.
	Stops *Stops `json:"stops,omitzero"`
	// Rules are the statements of the R-LOOP rules on the finding of the
	// DIMM.
	Rules []Evidence `json:"rules,omitzero"`
}

// BootCol is one boot of the window.
type BootCol struct {
	Index        int       `json:"index"`
	BootID       string    `json:"boot_id"`
	StartedAt    time.Time `json:"started_at"`
	StartQuality string    `json:"start_quality"`
	RanSeconds   int64     `json:"ran_seconds,omitzero"`
	// End is "stop", "panic", "running", or "" when no record tells.
	End       string `json:"end"`
	EndRecord string `json:"end_record,omitzero"`
	// StopSlot is the slot of the one DIMM with an uncorrected error in a
	// boot that stopped.
	StopSlot string `json:"stop_slot,omitzero"`
}

// Levels of a BootRow beside the place levels of the engine.
const (
	// RowDIMM is the row of a DIMM in the view of a host.
	RowDIMM = "dimm"
	// RowNoDIMM is the row of the uncorrected errors that name no DIMM.
	RowNoDIMM = "no-dimm"
)

// BootRow is one DIMM, or one place of a DIMM, boot by boot.
type BootRow struct {
	// Level is "dimm" or "no-dimm" in the view of a host, and the level
	// of the place in the view of one DIMM: "cell", "address" or
	// "dimm-only" (engine.PlaceCell, PlaceAddress, PlaceDIMM).
	Level  string `json:"level"`
	UID    string `json:"dimm_uid,omitzero"`
	Slot   string `json:"slot,omitzero"`
	Serial string `json:"serial,omitzero"`
	Place  string `json:"place,omitzero"`
	// Cells holds one cell per boot of the window, in the order of Boots.
	Cells           []BootCell `json:"cells"`
	BootsWithErrors int        `json:"boots_with_errors"`
	BootsWithUE     int        `json:"boots_with_ue"`
	CE              int64      `json:"ce"`
	UE              int64      `json:"ue"`
	// StopBoots counts the boots that stopped with an uncorrected error
	// of this DIMM.
	StopBoots int `json:"stop_boots,omitzero"`
	// Places counts the places a row of Others stands for.
	Places int `json:"places,omitzero"`
}

// BootCell is what a row showed in one boot.
type BootCell struct {
	Boot int   `json:"boot"`
	CE   int64 `json:"ce,omitzero"`
	UE   int64 `json:"ue,omitzero"`
	// Stopped: the boot ended in a stop and the row had an uncorrected
	// error in it.
	Stopped bool `json:"stopped,omitzero"`
	// Known is false where the boot stopped and no record of it names a
	// DIMM: an empty cell does not clear this DIMM then.
	Known bool `json:"known"`
	// Absent: the DIMM was not in the host during the boot.
	Absent bool `json:"absent,omitzero"`
}

// Stops sums up the boots of the window that a fatal hardware error
// ended.
type Stops struct {
	Stops  int `json:"stops"`
	Window int `json:"window"`
	Streak int `json:"streak,omitzero"`
	Panics int `json:"panics,omitzero"`
	// Where is the rule that says where the uncorrected errors lie
	// (R-LOOP-01, R-LOOP-02, R-LOOP-03), "dimm" below the rules, "none"
	// when no record names a DIMM.
	Where string `json:"where,omitzero"`
	// Lines are the stops lines: how many boots stopped, and where.
	Lines []Text `json:"lines"`
}

// MaxPlaces is the number of places of a DIMM the view lists before the
// rest is summed in one row.
const MaxPlaces = 12

// stops returns the summary of the stops of a host, or nil.
func (c *Case) stops(h *engine.HostResult) *Stops {
	r := h.Recurrence
	if r == nil || r.Stops == 0 {
		return nil
	}
	s := &Stops{Stops: r.Stops, Window: r.Window(), Streak: r.Streak, Panics: r.Panics, Where: r.Where, Lines: []Text{}}
	for _, st := range r.Statements {
		s.Lines = append(s.Lines, c.statement(st))
	}
	return s
}

// StopsOf returns the summary of the stops of a host, or nil when no boot
// of its window ended in a stop.
func (c *Case) StopsOf(h *engine.HostResult) *Stops { return c.stops(h) }

// BootsView builds the view of the boots of a host, or with d set the
// places of one DIMM boot by boot.
func (c *Case) BootsView(ctx context.Context, h *engine.HostResult, d *engine.DIMMResult) (*Boots, error) {
	b := &Boots{Host: h.Host, Boots: []BootCol{}, Rows: []BootRow{}}
	hdr, _, _, err := c.header(ctx, h)
	if err != nil {
		return nil, err
	}
	b.System = hdr.System
	r := h.Recurrence
	if r == nil {
		if d != nil {
			info := dimmInfo(d.UID, d.DIMM)
			b.Slot, b.DIMM = d.Slot, &info
		}
		return b, nil
	}
	b.OnRecord = r.OnRecord
	for _, bs := range r.Boots {
		b.Boots = append(b.Boots, BootCol{Index: bs.Index, BootID: bs.BootID, StartedAt: bs.StartedAt, StartQuality: bs.StartQuality,
			RanSeconds: bs.RanSeconds, End: bs.End, EndRecord: bs.EndRecord, StopSlot: bs.StopSlot})
	}
	b.Stops = c.stops(h)
	// A boot that stopped with no record naming a DIMM: an empty cell of a
	// DIMM says nothing about it.
	unnamed := map[int]bool{}
	for _, bs := range r.Boots {
		if bs.End == model.BootStop {
			unnamed[bs.Index] = true
		}
	}
	for _, db := range r.DIMMs {
		for _, cell := range db.Cells {
			if cell.UE > 0 {
				delete(unnamed, cell.Boot)
			}
		}
	}

	if d != nil {
		info := dimmInfo(d.UID, d.DIMM)
		b.Slot, b.DIMM = d.Slot, &info
		db := r.DIMM(d.UID)
		if db == nil {
			return b, nil
		}
		absent, err := c.absent(ctx, h, d, r)
		if err != nil {
			return nil, err
		}
		for i, pb := range db.Places {
			row := BootRow{Level: pb.Level, Place: pb.Place, BootsWithErrors: pb.BootsWithErrors, BootsWithUE: pb.BootsWithUE,
				CE: pb.CE, UE: pb.UE, Cells: fullCells(len(r.Boots), pb.Cells, unnamed, absent)}
			if i < MaxPlaces {
				b.Rows = append(b.Rows, row)
				continue
			}
			if b.Others == nil {
				b.Others = &BootRow{Level: "others", Cells: fullCells(len(r.Boots), nil, unnamed, absent)}
			}
			o := b.Others
			o.Places++
			o.CE += row.CE
			o.UE += row.UE
			for k, cell := range row.Cells {
				oc := &o.Cells[k]
				oc.CE += cell.CE
				oc.UE += cell.UE
				oc.Stopped = oc.Stopped || cell.Stopped
			}
		}
		if o := b.Others; o != nil {
			for _, cell := range o.Cells {
				if cell.CE+cell.UE > 0 {
					o.BootsWithErrors++
				}
				if cell.UE > 0 {
					o.BootsWithUE++
				}
			}
		}
		for _, e := range d.Verdict.Evidence {
			switch e.Rule {
			case engine.RuleLoopPlace, engine.RuleLoopDIMM, engine.RuleLoopSpread:
				b.Rules = append(b.Rules, Evidence{Rule: e.Rule, Text: c.statement(e.Statement)})
			}
		}
		return b, nil
	}

	inWindow := map[string]bool{}
	for _, db := range r.DIMMs {
		inWindow[db.UID] = true
		row := BootRow{Level: RowDIMM, UID: db.UID, Slot: db.Slot, BootsWithErrors: db.BootsWithErrors, BootsWithUE: db.BootsWithUE,
			StopBoots: db.StopBoots}
		var dr *engine.DIMMResult
		for _, x := range h.DIMMs {
			if x.UID == db.UID {
				dr = x
			}
		}
		var absent map[int]bool
		if dr != nil {
			if dr.DIMM != nil {
				row.Serial = dr.DIMM.Serial
			}
			if absent, err = c.absent(ctx, h, dr, r); err != nil {
				return nil, err
			}
		}
		row.Cells = fullCells(len(r.Boots), db.Cells, unnamed, absent)
		for _, cell := range db.Cells {
			row.CE += cell.CE
			row.UE += cell.UE
		}
		b.Rows = append(b.Rows, row)
	}
	for _, x := range h.DIMMs {
		if Listed(x) && !inWindow[x.UID] {
			b.Quiet++
		}
	}
	if len(r.Loose) > 0 {
		row := BootRow{Level: RowNoDIMM}
		var cells []engine.BootCell
		byBoot := map[int]*engine.BootCell{}
		for _, pb := range r.Loose {
			row.UE += pb.UE
			for _, cell := range pb.Cells {
				bc := byBoot[cell.Boot]
				if bc == nil {
					bc = &engine.BootCell{Boot: cell.Boot}
					byBoot[cell.Boot] = bc
				}
				bc.UE += cell.UE
				bc.Stopped = bc.Stopped || cell.Stopped
			}
		}
		keys := make([]int, 0, len(byBoot))
		for k := range byBoot {
			keys = append(keys, k)
		}
		sort.Ints(keys)
		for _, k := range keys {
			cells = append(cells, *byBoot[k])
		}
		row.Cells = fullCells(len(r.Boots), cells, nil, nil)
		row.BootsWithErrors, row.BootsWithUE = len(cells), len(cells)
		b.Rows = append(b.Rows, row)
	}
	return b, nil
}

// fullCells turns the cells of the boots with errors into one cell per
// boot of the window.
func fullCells(n int, cells []engine.BootCell, unnamed, absent map[int]bool) []BootCell {
	out := make([]BootCell, n)
	for i := range out {
		out[i] = BootCell{Boot: i + 1, Known: !unnamed[i+1], Absent: absent[i+1]}
	}
	for _, c := range cells {
		if c.Boot < 1 || c.Boot > n {
			continue
		}
		out[c.Boot-1] = BootCell{Boot: c.Boot, CE: c.CE, UE: c.UE, Stopped: c.Stopped, Known: true}
	}
	return out
}

// absent returns the boots of the window during which the DIMM was not in
// the host: boots after the last time a DIMM that left was seen, and boots
// before the first time a DIMM was seen in a slot that held another one
// before it.
func (c *Case) absent(ctx context.Context, h *engine.HostResult, d *engine.DIMMResult, r *engine.Recurrence) (map[int]bool, error) {
	places, err := c.store.Placements(ctx, h.Host, "")
	if err != nil {
		return nil, err
	}
	var first, last time.Time
	slots := map[string]bool{}
	for _, p := range places {
		if p.DIMMUID != d.UID {
			continue
		}
		slots[p.Slot] = true
		if first.IsZero() || p.FirstSeen.Before(first) {
			first = p.FirstSeen
		}
		if p.LastSeen.After(last) {
			last = p.LastSeen
		}
	}
	if first.IsZero() {
		return nil, nil
	}
	// Another DIMM held one of its slots before it.
	var other time.Time
	for _, p := range places {
		if p.DIMMUID != d.UID && slots[p.Slot] && p.FirstSeen.Before(first) && p.LastSeen.After(other) {
			other = p.LastSeen
		}
	}
	out := map[int]bool{}
	for i, bs := range r.Boots {
		end := bs.StartedAt
		if i+1 < len(r.Boots) {
			end = r.Boots[i+1].StartedAt
		}
		switch {
		case !d.Present && bs.StartedAt.After(last):
			out[bs.Index] = true
		case !other.IsZero() && end.Before(first) && !bs.StartedAt.After(other):
			out[bs.Index] = true
		}
	}
	return out, nil
}
