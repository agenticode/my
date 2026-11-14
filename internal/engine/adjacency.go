package engine

import "github.com/agenticode/my/internal/model"

// Site names the bank a row belongs to, for adjacency questions.
type Site struct {
	// DIMM is the module, or nil when the ledger has no identity for it.
	DIMM *model.DIMM
	// Rank, DeviceId, BankGroup and Bank are unset where the platform did
	// not report them.
	Rank, DeviceId, BankGroup, Bank model.Opt[int]
}

// Adjacency says which rows of a bank lie next to each other inside the
// DRAM device.
//
// Error records carry logical row addresses. Vendors scramble, mirror and
// remap rows inside the device, so two rows with neighbouring numbers may be
// far apart physically and the other way round. A vendor analyzer that
// knows the internal layout supplies its own Adjacency; without one the
// engine uses LogicalAdjacency.
type Adjacency interface {
	// NeighborRows returns the rows that are physical neighbours of row.
	// The relation must be symmetric: if b is a neighbour of a, a is a
	// neighbour of b.
	NeighborRows(site Site, row int) []int
}

// LogicalAdjacency treats rows whose numbers differ by one as neighbours.
type LogicalAdjacency struct{}

// NeighborRows returns row-1 and row+1.
func (LogicalAdjacency) NeighborRows(_ Site, row int) []int {
	if row <= 0 {
		return []int{row + 1}
	}
	return []int{row - 1, row + 1}
}
