package engine

import (
	"sort"
	"time"

	"github.com/agenticode/my/internal/model"
)

// Clustering turns the errors of one DIMM into faults and gives each fault
// a Level 1 mode. What a platform reports decides how far that goes:
//
//   - Errors with bank, row and column (grade A) are classified in full.
//   - Errors known by physical address only (grade B) are grouped by
//     address. A repeating address and a run of close addresses are
//     reported as single-bit and row faults, marked as estimated. Anything
//     wider is left undetermined.
//   - Errors known as a count (grade C) form one fault of undetermined mode.
//
// For located errors the steps are:
//
//  1. Lines. Within a bank of one device, errors that share a row or a
//     column form a line, if scatter does not explain the alignment (see
//     crowdChance).
//  2. Row groups. Row lines whose rows are neighbours, and single errors in
//     rows next to a line, form one group: a row fault, or a multi-row fault
//     when the neighbours carry a real share of the errors.
//  3. Scatter. What is left is looked at from the DIMM down. Enough
//     different places within the scatter window make a fault of the
//     smallest unit that holds them: a bank, a device (two or more banks) or
//     the DIMM (two or more devices). A unit that holds nearly all of them
//     is judged on its own first.
//  4. Cells. Every place that remains is a single-bit fault: recurring if
//     it failed again later, a one-off otherwise.

type cellKey struct {
	rank, dev, bg, bank int16
	row, col            int32
}

type devKey struct{ rank, dev int16 }

type bankKey struct{ rank, dev, bg, bank int16 }

func (k cellKey) bankKey() bankKey { return bankKey{k.rank, k.dev, k.bg, k.bank} }

func (k cellKey) less(o cellKey) bool {
	switch {
	case k.rank != o.rank:
		return k.rank < o.rank
	case k.dev != o.dev:
		return k.dev < o.dev
	case k.bg != o.bg:
		return k.bg < o.bg
	case k.bank != o.bank:
		return k.bank < o.bank
	case k.row != o.row:
		return k.row < o.row
	}
	return k.col < o.col
}

// cell is one location with the records that hit it.
type cell struct {
	cellKey
	errors int64
	// first is the time of the earliest dated record, or 0.
	first  int64
	events []int32
}

// space is the size of the address space lines are tested against.
type space struct {
	rows, cols float64
	// trials is the number of banks of the DIMM that are searched for lines.
	trials float64
}

// chance bounds the probability that scatter lines up k of the n cells of
// a bank in one of its bins (rows or columns), in any of the banks searched.
func (sp space) chance(k, n int, bins float64) float64 {
	return min(1, alignChance(k, n, bins)*max(sp.trials, 1))
}

// clusterer classifies the errors of one DIMM.
type clusterer struct {
	in   *Input
	cfg  *Config
	adj  Adjacency
	uid  string
	dimm *model.DIMM
}

func timeOf(micro int64) time.Time { return time.UnixMicro(micro).UTC() }

// classify returns the faults of the DIMM whose records are evs.
func (cl *clusterer) classify(evs []int32) []*Fault {
	var located, byAddr, rest []int32
	for _, i := range evs {
		e := &cl.in.events[i]
		switch {
		case e.located():
			located = append(located, i)
		case e.hasAddr && !e.counter:
			byAddr = append(byAddr, i)
		default:
			rest = append(rest, i)
		}
	}
	var faults []*Fault
	faults = append(faults, cl.classifyLocated(located)...)
	faults = append(faults, cl.classifyByAddress(byAddr)...)
	faults = append(faults, cl.classifyCounts(rest)...)
	for _, f := range faults {
		f.Confidence = modeConfidence(f)
	}
	sortFaults(faults)
	return faults
}

// cells groups located records by location, in a fixed order.
func (cl *clusterer) cells(evs []int32) []*cell {
	byKey := map[cellKey]*cell{}
	var out []*cell
	for _, i := range evs {
		e := &cl.in.events[i]
		k := cellKey{e.rank, e.dev, e.bg, e.bank, e.row, e.col}
		c := byKey[k]
		if c == nil {
			c = &cell{cellKey: k}
			byKey[k] = c
			out = append(out, c)
		}
		c.errors += int64(e.errors)
		c.events = append(c.events, i)
		if !e.relative && (c.first == 0 || e.at < c.first) {
			c.first = e.at
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].cellKey.less(out[j].cellKey) })
	return out
}

// spaceOf estimates how many rows and columns a bank of the DIMM has, from
// the largest values seen. Columns are counted at the granularity the
// platform reports them (memory controllers report the start of a burst).
// Both numbers have a floor below which no server DIMM lies, so that a few
// errors do not make the address space look small.
func spaceOf(cells []*cell) space {
	maxRow, maxCol, step := 0, 0, 0
	for _, c := range cells {
		maxRow = max(maxRow, int(c.row))
		maxCol = max(maxCol, int(c.col))
		step = gcd(step, int(c.col))
	}
	if step == 0 {
		step = 1
	}
	return space{
		rows: float64(max(ceilPow2(maxRow+1), 1<<14)),
		cols: float64(max(ceilPow2(maxCol+1)/step, 1<<7)),
	}
}

func members(cells []*cell) []int32 {
	var out []int32
	for _, c := range cells {
		out = append(out, c.events...)
	}
	return out
}

func (cl *clusterer) classifyLocated(evs []int32) []*Fault {
	if len(evs) == 0 {
		return nil
	}
	cells := cl.cells(evs)
	sp := spaceOf(cells)

	// cells is sorted, so the cells of a bank are contiguous.
	var banks [][]*cell
	for start := 0; start < len(cells); {
		end := start
		for end < len(cells) && cells[end].bankKey() == cells[start].bankKey() {
			end++
		}
		banks = append(banks, cells[start:end])
		start = end
	}
	// Every bank with two or more cells is one more place where chance can
	// line cells up. A DIMM with errors scattered over a hundred banks will
	// show an accidental pair somewhere, so the bound is taken over all of
	// them.
	for _, b := range banks {
		if len(b) >= 2 {
			sp.trials++
		}
	}

	var faults []*Fault
	var loose []*cell
	for _, b := range banks {
		fs, rest := cl.classifyBank(b, sp)
		faults = append(faults, fs...)
		loose = append(loose, rest...)
	}
	fs, rest := cl.scatter(levelDIMM, loose)
	faults = append(faults, fs...)
	for _, c := range rest {
		f := cl.newFault(model.ModeSingleBit, model.GradeA, append([]int32(nil), c.events...))
		f.Confined = f.Recurring
		faults = append(faults, f)
	}
	return faults
}

// line is a row or a column of a bank with the cells on it.
type line struct {
	isRow bool
	index int32
	cells []*cell
	// chance is the probability bound under which scatter explains it.
	chance float64
}

func (l *line) better(o *line) bool {
	switch {
	case o == nil:
		return true
	case l.chance != o.chance:
		return l.chance < o.chance
	case len(l.cells) != len(o.cells):
		return len(l.cells) > len(o.cells)
	case l.isRow != o.isRow:
		return l.isRow
	}
	return l.index < o.index
}

// extractLines takes the rows and columns of one bank that hold more cells
// than scatter of the bank's cells explains, strongest first. It returns
// the lines and the cells on none of them.
func (cl *clusterer) extractLines(cells []*cell, sp space) (rows, cols []*line, loose []*cell) {
	rest := append([]*cell(nil), cells...)
	for len(rest) >= 2 {
		byRow, byCol := map[int32][]*cell{}, map[int32][]*cell{}
		for _, c := range rest {
			byRow[c.row] = append(byRow[c.row], c)
			byCol[c.col] = append(byCol[c.col], c)
		}
		var best *line
		consider := func(isRow bool, idx int32, cs []*cell, bins float64) {
			if len(cs) < 2 {
				return
			}
			l := &line{isRow: isRow, index: idx, cells: cs, chance: sp.chance(len(cs), len(rest), bins)}
			if l.better(best) {
				best = l
			}
		}
		for idx, cs := range byRow {
			consider(true, idx, cs, sp.rows)
		}
		for idx, cs := range byCol {
			consider(false, idx, cs, sp.cols)
		}
		if best == nil || best.chance >= cl.cfg.LineChance {
			break
		}
		if best.isRow {
			rows = append(rows, best)
		} else {
			cols = append(cols, best)
		}
		taken := map[*cell]bool{}
		for _, c := range best.cells {
			taken[c] = true
		}
		kept := rest[:0]
		for _, c := range rest {
			if !taken[c] {
				kept = append(kept, c)
			}
		}
		rest = kept
	}
	return rows, cols, rest
}

// classifyBank finds the line faults of one bank of one device. It returns
// them and the cells that belong to none.
func (cl *clusterer) classifyBank(cells []*cell, sp space) ([]*Fault, []*cell) {
	rows, cols, loose := cl.extractLines(cells, sp)
	var faults []*Fault
	for _, l := range cols {
		f := cl.newFault(model.ModeColumn, model.GradeA, members(l.cells))
		f.Confined = f.Recurring
		faults = append(faults, f)
	}

	// Row groups: rows are nodes, neighbouring rows are joined.
	type node struct {
		row   int32
		line  *line
		loose []*cell
		group int
	}
	nodes := map[int32]*node{}
	var order []*node
	get := func(row int32) *node {
		n := nodes[row]
		if n == nil {
			n = &node{row: row, group: len(order)}
			nodes[row] = n
			order = append(order, n)
		}
		return n
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].index < rows[j].index })
	for _, l := range rows {
		get(l.index).line = l
	}
	for _, c := range loose {
		get(c.row).loose = append(get(c.row).loose, c)
	}
	// Two loose cells in neighbouring rows are only joined when chance
	// does not put loose cells next to each other: among n cells scattered
	// over R rows about n*(n-1)/R pairs are neighbours by accident.
	n := float64(len(loose))
	looseJoin := n*(n-1)/sp.rows*max(sp.trials, 1) < cl.cfg.LineChance

	parent := make([]int, len(order))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		if parent[i] != i {
			parent[i] = find(parent[i])
		}
		return parent[i]
	}
	k := cells[0].cellKey
	site := Site{DIMM: cl.dimm, Rank: optOf(k.rank), DeviceId: optOf(k.dev), BankGroup: optOf(k.bg), Bank: optOf(k.bank)}
	for _, n := range order {
		for _, r := range cl.adj.NeighborRows(site, int(n.row)) {
			if r < 0 || r > 1<<31-1 {
				continue
			}
			m := nodes[int32(r)]
			if m == nil || m == n {
				continue
			}
			if n.line == nil && m.line == nil && !looseJoin {
				continue
			}
			if a, b := find(n.group), find(m.group); a != b {
				parent[max(a, b)] = min(a, b)
			}
		}
	}
	groups := map[int][]*node{}
	var roots []int
	for _, n := range order {
		r := find(n.group)
		if _, ok := groups[r]; !ok {
			roots = append(roots, r)
		}
		groups[r] = append(groups[r], n)
	}
	sort.Ints(roots)

	var left []*cell
	for _, r := range roots {
		g := groups[r]
		hasLine := false
		for _, n := range g {
			hasLine = hasLine || n.line != nil
		}
		if !hasLine && len(g) < 2 {
			left = append(left, g[0].loose...)
			continue
		}
		var total, top int64
		var topRow int32
		var cs []*cell
		for _, n := range g {
			var sum int64
			if n.line != nil {
				cs = append(cs, n.line.cells...)
			}
			cs = append(cs, n.loose...)
			for _, c := range n.loose {
				sum += c.errors
			}
			if n.line != nil {
				for _, c := range n.line.cells {
					sum += c.errors
				}
			}
			total += sum
			if sum > top || (sum == top && n.row < topRow) {
				top, topRow = sum, n.row
			}
		}
		mode := model.ModeRow
		if len(g) >= 2 && float64(total-top) >= cl.cfg.MultiRowShare*float64(total) {
			mode = model.ModeMultiRow
		}
		f := cl.newFault(mode, model.GradeA, members(cs))
		f.TopRow = model.Some(int(topRow))
		f.Confined = f.Recurring
		faults = append(faults, f)
	}
	return faults, left
}

func optOf(v int16) model.Opt[int] {
	if v < 0 {
		return model.Opt[int]{}
	}
	return model.Some(int(v))
}

// Levels of the scatter search.
const (
	levelDIMM = iota
	levelDevice
	levelBank
)

// dense reports whether enough of the cells were first seen within one
// scatter window.
func (cl *clusterer) dense(cells []*cell) bool {
	n := cl.cfg.ScatterMinCells
	if n < 2 {
		n = 2
	}
	if len(cells) < n {
		return false
	}
	ts := make([]int64, len(cells))
	for i, c := range cells {
		ts[i] = c.first
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i] < ts[j] })
	w := cl.cfg.scatterWindow()
	for i := 0; i+n-1 < len(ts); i++ {
		if ts[i+n-1]-ts[i] <= w {
			return true
		}
	}
	return false
}

// scatter looks for faults among cells that are on no line. It returns the
// faults and the cells that stay on their own.
func (cl *clusterer) scatter(level int, cells []*cell) ([]*Fault, []*cell) {
	if len(cells) == 0 {
		return nil, nil
	}
	if level == levelBank {
		if !cl.dense(cells) {
			return nil, cells
		}
		f := cl.newFault(model.ModeBank, model.GradeA, members(cells))
		f.Confined = f.Recurring
		return []*Fault{f}, nil
	}

	// Children: devices of the DIMM, or banks of a device. A rank in which
	// some records lack the device is one child: its records cannot be told
	// apart by device.
	type child struct {
		key   bankKey
		cells []*cell
	}
	noDev := map[int16]bool{}
	if level == levelDIMM {
		for _, c := range cells {
			if c.dev < 0 {
				noDev[c.rank] = true
			}
		}
	}
	keyOf := func(c *cell) bankKey {
		if level == levelDIMM {
			if noDev[c.rank] {
				return bankKey{rank: c.rank, dev: -1}
			}
			return bankKey{rank: c.rank, dev: c.dev}
		}
		return c.bankKey()
	}
	idx := map[bankKey]int{}
	var children []*child
	for _, c := range cells {
		k := keyOf(c)
		i, ok := idx[k]
		if !ok {
			i = len(children)
			idx[k] = i
			children = append(children, &child{key: k})
		}
		children[i].cells = append(children[i].cells, c)
	}
	largest := 0
	for i, ch := range children {
		if len(ch.cells) > len(children[largest].cells) {
			largest = i
		}
	}
	if len(children) == 1 {
		return cl.scatter(level+1, cells)
	}
	outside := len(cells) - len(children[largest].cells)
	if cl.dense(cells) && float64(outside) >= cl.cfg.WiderShare*float64(len(cells)) {
		mode := model.ModeDIMM
		if level == levelDevice {
			mode = model.ModeDevice
		}
		f := cl.newFault(mode, model.GradeA, members(cells))
		if level == levelDevice && cells[0].dev < 0 {
			// Several banks of one rank, device not reported: at least a
			// device fault, possibly wider.
			f.Estimated, f.EstimatedBy = true, EstimateDeviceUnknown
		}
		return []*Fault{f}, nil
	}
	// One child holds nearly everything, or the whole is too thin: judge
	// the largest child on its own and the others together.
	var others []*cell
	for i, ch := range children {
		if i != largest {
			others = append(others, ch.cells...)
		}
	}
	f1, l1 := cl.scatter(level+1, children[largest].cells)
	f2, l2 := cl.scatter(level, others)
	return append(f1, f2...), append(l1, l2...)
}

// addrCell is one cache line address with the records that hit it.
type addrCell struct {
	slot   int32
	line   uint64 // address >> 6
	events []int32
}

// classifyByAddress groups records that are known by physical address only.
// Addresses belong to the slot, so a DIMM that was moved is looked at slot
// by slot.
func (cl *clusterer) classifyByAddress(evs []int32) []*Fault {
	if len(evs) == 0 {
		return nil
	}
	byKey := map[addrKey]*addrCell{}
	var cells []*addrCell
	for _, i := range evs {
		e := &cl.in.events[i]
		k := addrKey{e.slot, e.addr >> 6}
		c := byKey[k]
		if c == nil {
			c = &addrCell{slot: e.slot, line: k.addr}
			byKey[k] = c
			cells = append(cells, c)
		}
		c.events = append(c.events, i)
	}
	sort.Slice(cells, func(i, j int) bool {
		if cells[i].slot != cells[j].slot {
			return cells[i].slot < cells[j].slot
		}
		return cells[i].line < cells[j].line
	})

	window := cl.cfg.RowWindowBytes
	if window < 64 {
		window = 64
	}

	var faults []*Fault
	var loose []*addrCell
	for start := 0; start < len(cells); {
		end := start
		for end < len(cells) && cells[end].slot == cells[start].slot {
			end++
		}
		slotCells := cells[start:end]
		start = end
		// A run is tested against scatter of the slot's other addresses
		// over the address range all of them span.
		bins := float64((slotCells[len(slotCells)-1].line-slotCells[0].line)<<6)/float64(window) + 1
		// Runs: consecutive addresses no further apart than the window.
		for i := 0; i < len(slotCells); {
			j := i + 1
			for j < len(slotCells) && (slotCells[j].line-slotCells[j-1].line)<<6 <= window {
				j++
			}
			run := slotCells[i:j]
			i = j
			if len(run) >= 2 && crowdChance(len(run), len(slotCells)-len(run), bins) < cl.cfg.LineChance {
				var ms []int32
				for _, c := range run {
					ms = append(ms, c.events...)
				}
				f := cl.newFault(model.ModeRow, model.GradeB, ms)
				f.Estimated, f.EstimatedBy = true, EstimateAddressOnly
				f.Confined = f.Recurring
				faults = append(faults, f)
				continue
			}
			loose = append(loose, run...)
		}
	}

	// What is left: many addresses in a short time are a fault whose extent
	// the addresses do not show; few are single addresses.
	n := max(cl.cfg.ScatterMinCells, 2)
	dense := false
	if len(loose) >= n {
		ts := make([]int64, 0, len(loose))
		for _, c := range loose {
			first := int64(0)
			for _, i := range c.events {
				if e := &cl.in.events[i]; !e.relative && (first == 0 || e.at < first) {
					first = e.at
				}
			}
			ts = append(ts, first)
		}
		sort.Slice(ts, func(i, j int) bool { return ts[i] < ts[j] })
		w := cl.cfg.scatterWindow()
		for i := 0; i+n-1 < len(ts); i++ {
			if ts[i+n-1]-ts[i] <= w {
				dense = true
				break
			}
		}
	}
	if dense {
		var ms []int32
		for _, c := range loose {
			ms = append(ms, c.events...)
		}
		faults = append(faults, cl.newFault(model.ModeUndetermined, model.GradeB, ms))
		return faults
	}
	for _, c := range loose {
		f := cl.newFault(model.ModeSingleBit, model.GradeB, append([]int32(nil), c.events...))
		f.Estimated, f.EstimatedBy = true, EstimateAddressOnly
		f.Confined = f.Recurring
		faults = append(faults, f)
	}
	return faults
}

// classifyCounts handles records without a usable location: counter
// growth, and error records that name a DIMM or a rank and nothing more.
// They form one fault of undetermined mode per observation grade.
func (cl *clusterer) classifyCounts(evs []int32) []*Fault {
	var withRank, counts []int32
	for _, i := range evs {
		if e := &cl.in.events[i]; e.rank >= 0 && !e.counter {
			withRank = append(withRank, i)
		} else {
			counts = append(counts, i)
		}
	}
	var faults []*Fault
	if len(withRank) > 0 {
		faults = append(faults, cl.newFault(model.ModeUndetermined, model.GradeB, withRank))
	}
	if len(counts) > 0 {
		faults = append(faults, cl.newFault(model.ModeUndetermined, model.GradeC, counts))
	}
	return faults
}

// modeConfidence rates how far the mode of a fault can be trusted, from 0
// to 100. It reflects the observation grade and how much the fault rests on.
func modeConfidence(f *Fault) int {
	if f.Mode == model.ModeUndetermined {
		return 0
	}
	c := 90
	switch {
	case f.Grade == model.GradeB:
		c = 50
	case f.Estimated:
		c = 60
	}
	switch f.Mode {
	case model.ModeSingleBit:
		if !f.Recurring {
			c -= 20 // one sighting: the place is known, a lasting fault is not
		}
	case model.ModeRow, model.ModeColumn, model.ModeMultiRow:
		switch {
		case f.Shape.Cells <= 2:
			c -= 20
		case f.Shape.Cells == 3:
			c -= 10
		}
	default:
		if f.Shape.Cells < 10 {
			c -= 10
		}
	}
	return min(max(c, 5), 95)
}
