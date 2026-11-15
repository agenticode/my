package engine

import (
	"math/rand/v2"
	"testing"
)

// extractLinesBySearch is the plain form of extractLines: every round it
// groups the remaining cells by row and by column, rates every group and
// takes the best. extractLines must return what this returns.
func (cl *clusterer) extractLinesBySearch(cells []*cell, sp space) (rows, cols []*line, loose []*cell) {
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

// randomBank makes the cells of one bank: lines of several lengths, lines
// that cross, and scatter, in the sorted order classifyLocated works with.
func randomBank(rng *rand.Rand) []*cell {
	rowSpan, colSpan := 1<<(4+rng.IntN(14)), 8<<rng.IntN(8)
	seen := map[[2]int32]bool{}
	var cells []*cell
	add := func(row, col int) {
		k := [2]int32{int32(row), int32(col)}
		if !seen[k] {
			seen[k] = true
			cells = append(cells, &cell{cellKey: cellKey{rank: 0, dev: -1, bg: -1, bank: 3, row: k[0], col: k[1]}, errors: 1})
		}
	}
	for i, n := 0, rng.IntN(6); i < n; i++ { // rows
		row := rng.IntN(rowSpan)
		for j, m := 0, 2+rng.IntN(12); j < m; j++ {
			add(row, rng.IntN(colSpan/8)*8)
		}
	}
	for i, n := 0, rng.IntN(6); i < n; i++ { // columns
		col := rng.IntN(colSpan/8) * 8
		for j, m := 0, 2+rng.IntN(12); j < m; j++ {
			add(rng.IntN(rowSpan), col)
		}
	}
	for i, n := 0, rng.IntN(40); i < n; i++ { // scatter
		add(rng.IntN(rowSpan), rng.IntN(colSpan/8)*8)
	}
	sortCells(cells)
	return cells
}

func sortCells(cells []*cell) {
	for i := 1; i < len(cells); i++ {
		for j := i; j > 0 && cells[j].cellKey.less(cells[j-1].cellKey); j-- {
			cells[j], cells[j-1] = cells[j-1], cells[j]
		}
	}
}

func sameLines(t *testing.T, what string, got, want []*line) bool {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: %d lines, want %d", what, len(got), len(want))
		return false
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.isRow != w.isRow || g.index != w.index || g.chance != w.chance || len(g.cells) != len(w.cells) {
			t.Errorf("%s %d: row %v index %d chance %g with %d cells, want row %v index %d chance %g with %d cells",
				what, i, g.isRow, g.index, g.chance, len(g.cells), w.isRow, w.index, w.chance, len(w.cells))
			return false
		}
		for j := range w.cells {
			if g.cells[j] != w.cells[j] {
				t.Errorf("%s %d: cell %d differs", what, i, j)
				return false
			}
		}
	}
	return true
}

// TestExtractLinesMatchesSearch compares extractLines with the plain
// search on random banks: the same lines in the same order with the same
// cells, and the same cells left over.
func TestExtractLinesMatchesSearch(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))
	cfg := DefaultConfig()
	lines := 0
	for n := 0; n < 3000; n++ {
		cells := randomBank(rng)
		sp := spaceOf(cells)
		sp.trials = float64(1 + rng.IntN(4))
		c := cfg
		if n%5 == 0 {
			// A loose threshold takes many more lines, also weak ones.
			c.LineChance = 0.5
		}
		cl := &clusterer{cfg: &c}
		rows, cols, loose := cl.extractLines(cells, sp)
		wantRows, wantCols, wantLoose := cl.extractLinesBySearch(cells, sp)
		ok := sameLines(t, "rows", rows, wantRows) && sameLines(t, "columns", cols, wantCols)
		if len(loose) != len(wantLoose) {
			t.Errorf("%d cells left over, want %d", len(loose), len(wantLoose))
			ok = false
		}
		for i := range wantLoose {
			if ok && loose[i] != wantLoose[i] {
				t.Errorf("cell %d left over differs", i)
				ok = false
			}
		}
		if !ok {
			t.Fatalf("bank %d with %d cells", n, len(cells))
		}
		lines += len(rows) + len(cols)
	}
	if lines < 3000 {
		t.Errorf("the random banks gave %d lines in all; the comparison says little", lines)
	}
	// Banks without cells and with one cell.
	cl := &clusterer{cfg: &cfg}
	if rows, cols, loose := cl.extractLines(nil, space{rows: 1 << 17, cols: 128}); rows != nil || cols != nil || loose != nil {
		t.Errorf("empty bank: %v %v %v", rows, cols, loose)
	}
	one := []*cell{{cellKey: cellKey{row: 5, col: 8}}}
	if rows, cols, loose := cl.extractLines(one, space{rows: 1 << 17, cols: 128}); rows != nil || cols != nil || len(loose) != 1 || loose[0] != one[0] {
		t.Errorf("bank with one cell: %v %v %v", rows, cols, loose)
	}
}

// BenchmarkExtractLines measures a bank with many lines: 400 rows of 30
// cells each and 2,000 scattered cells.
func BenchmarkExtractLines(b *testing.B) {
	rng := rand.New(rand.NewPCG(1, 2))
	var cells []*cell
	seen := map[[2]int32]bool{}
	add := func(row, col int) {
		k := [2]int32{int32(row), int32(col)}
		if !seen[k] {
			seen[k] = true
			cells = append(cells, &cell{cellKey: cellKey{dev: -1, bg: -1, row: k[0], col: k[1]}, errors: 1})
		}
	}
	for i := 0; i < 400; i++ {
		row := rng.IntN(1 << 17)
		for j := 0; j < 30; j++ {
			add(row, rng.IntN(128)*8)
		}
	}
	for i := 0; i < 2000; i++ {
		add(rng.IntN(1<<17), rng.IntN(128)*8)
	}
	cfg := DefaultConfig()
	cl := &clusterer{cfg: &cfg}
	sp := spaceOf(cells)
	sp.trials = 1
	b.Run("heaps", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			cl.extractLines(cells, sp)
		}
	})
	b.Run("search", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			cl.extractLinesBySearch(cells, sp)
		}
	})
}
