package plugin

import (
	"fmt"
	"slices"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/model"
)

// rowKey names one row of one bank of one DIMM. A place field the records
// do not carry is -1.
type rowKey struct {
	dimm                      string
	rank, device, group, bank int
	row                       int
}

// adjacency holds the rows an analyzer named neighbours for.
type adjacency struct {
	rows map[rowKey][]int
	// reverse holds, for a row, the rows that name it as a neighbour.
	reverse map[rowKey][]int
}

func place(o *int) int {
	if o == nil {
		return -1
	}
	return *o
}

func opt(o model.Opt[int]) int {
	if !o.Valid {
		return -1
	}
	return o.V
}

// add checks the rows of one DIMM and takes them in.
func (a *adjacency) add(dimm, where string, rows []AdjacentRows) error {
	if len(rows) > MaxRows {
		return fmt.Errorf("%s.AdjacentRows has %d rows; the limit is %d", where, len(rows), MaxRows)
	}
	for j := range rows {
		r := &rows[j]
		at := fmt.Sprintf("%s.AdjacentRows[%d]", where, j)
		for _, f := range []struct {
			name string
			v    *int
		}{{"Rank", r.Rank}, {"DeviceId", r.DeviceId}, {"BankGroup", r.BankGroup}, {"Bank", r.Bank}} {
			if f.v != nil && (*f.v < 0 || *f.v > 1<<16) {
				return fmt.Errorf("%s.%s is %d", at, f.name, *f.v)
			}
		}
		if r.Row < 0 || r.Row > MaxRow {
			return fmt.Errorf("%s.Row is %d", at, r.Row)
		}
		if len(r.Neighbors) > MaxNeighbors {
			return fmt.Errorf("%s has %d neighbours; the limit is %d", at, len(r.Neighbors), MaxNeighbors)
		}
		key := rowKey{dimm, place(r.Rank), place(r.DeviceId), place(r.BankGroup), place(r.Bank), r.Row}
		if _, dup := a.rows[key]; dup {
			return fmt.Errorf("%s names row %d a second time", at, r.Row)
		}
		seen := map[int]bool{}
		for _, n := range r.Neighbors {
			switch {
			case n < 0 || n > MaxRow:
				return fmt.Errorf("%s names the neighbour %d", at, n)
			case n == r.Row:
				return fmt.Errorf("%s names row %d its own neighbour", at, n)
			case seen[n]:
				return fmt.Errorf("%s names the neighbour %d twice", at, n)
			}
			seen[n] = true
		}
		a.rows[key] = slices.Clone(r.Neighbors)
		for _, n := range r.Neighbors {
			back := key
			back.row = n
			a.reverse[back] = append(a.reverse[back], r.Row)
		}
	}
	return nil
}

// symmetric checks that two rows the analyzer both answered for agree: if
// one names the other, the other names it.
func (a *adjacency) symmetric() error {
	for key, neighbours := range a.rows {
		for _, n := range neighbours {
			other := key
			other.row = n
			if back, answered := a.rows[other]; answered && !slices.Contains(back, key.row) {
				return fmt.Errorf("AdjacentRows of the DIMM %q: row %d names row %d a neighbour, and row %d does not name row %d", key.dimm, key.row, n, n, key.row)
			}
		}
	}
	return nil
}

// NeighborRows answers the engine. A row the analyzer answered for has the
// neighbours it named. Any other row has the rows numbered one below and
// one above it, less those the analyzer answered for without naming it,
// plus the rows that name it: the relation stays symmetric.
func (a *adjacency) NeighborRows(site engine.Site, row int) []int {
	logical := engine.LogicalAdjacency{}.NeighborRows(site, row)
	if site.DIMM == nil || len(a.rows) == 0 {
		return logical
	}
	key := rowKey{site.DIMM.UID, opt(site.Rank), opt(site.DeviceId), opt(site.BankGroup), opt(site.Bank), row}
	if n, ok := a.rows[key]; ok {
		return slices.Clone(n)
	}
	var out []int
	for _, n := range logical {
		other := key
		other.row = n
		if back, answered := a.rows[other]; answered && !slices.Contains(back, row) {
			continue
		}
		out = append(out, n)
	}
	for _, n := range a.reverse[key] {
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// Adjacency returns what the analyzer said about neighbouring rows, in the
// form the engine asks for. Without such an answer it is the logical
// adjacency of the engine.
func (r *Result) Adjacency() engine.Adjacency {
	if r == nil || r.adjacent == nil || len(r.adjacent.rows) == 0 {
		return engine.LogicalAdjacency{}
	}
	return r.adjacent
}

// baseline answers the engine's question about a part number from what
// the analyzer said about each DIMM.
type baseline map[string]float64

func (b baseline) Percentile(d *model.DIMM, _ int64, _ float64) (float64, bool) {
	if d == nil {
		return 0, false
	}
	p, ok := b[d.UID]
	return p, ok
}

// PartBaseline returns the percentiles of the analyzer in the form the
// engine asks for, or nil when the analyzer gave none.
func (r *Result) PartBaseline() engine.PartBaseline {
	if r == nil {
		return nil
	}
	b := baseline{}
	for id, d := range r.DIMMs {
		if d.Baseline != nil {
			b[id] = d.Baseline.Percentile
		}
	}
	if len(b) == 0 {
		return nil
	}
	return b
}
