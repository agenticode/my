package view

import (
	"context"
	"errors"
	"fmt"
	"math/bits"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// AddressMap shows the errors of one bank of a DIMM by row and column. The
// shape tells the fault mode: a horizontal line is a row fault, a vertical
// line a column fault, a single cell that repeats a single-bit fault, and a
// cloud a fault of the bank or of something wider.
type AddressMap struct {
	Host string   `json:"host"`
	Slot string   `json:"slot"`
	DIMM DIMMInfo `json:"dimm"`
	// Bank is the bank that is drawn. It is unset when no record of the
	// DIMM carries a row and a column.
	Bank *BankRef `json:"bank,omitzero"`
	// Banks lists the banks of the DIMM that have located errors, the one
	// with the most errors first, up to MaxBanksListed of them; BankCount
	// is how many there are.
	Banks     []BankRef `json:"banks"`
	BankCount int       `json:"bank_count"`
	// Located is the number of errors of the DIMM whose records carry row
	// and column, Unlocated the number of the others. Only located errors
	// can be drawn.
	Located   int64 `json:"located"`
	Unlocated int64 `json:"unlocated"`

	// Mode is MapRows when every line is one row, MapBins when a line
	// stands for RowsPerLine rows.
	Mode        string `json:"mode,omitzero"`
	RowsPerLine int    `json:"rows_per_line,omitzero"`
	// ColumnsPerCell is the number of column addresses one cell stands
	// for, Columns the number of column addresses the grid covers.
	ColumnsPerCell int `json:"columns_per_cell,omitzero"`
	Columns        int `json:"columns,omitzero"`
	// Scale holds the lower bounds of the four shades above zero.
	Scale []int64   `json:"scale"`
	Lines []MapLine `json:"lines"`
	// Refs point from the cells that hold errors to the ledger records
	// behind them, line by line.
	Refs []CellRef `json:"refs,omitzero"`
	// Marks point at the columns of column faults.
	Marks []ColumnMark `json:"marks,omitzero"`
	// Faults are the faults that have errors in the bank, the largest
	// first.
	Faults []MapFault `json:"faults"`
	// Stray counts the errors of the bank that belong to none of them.
	Stray int64 `json:"stray,omitzero"`
	// Window is the number of boots of the window of the host; Boots and
	// UEBoots count those of them in which the bank had errors and
	// uncorrected errors.
	Window  int `json:"window,omitzero"`
	Boots   int `json:"boots,omitzero"`
	UEBoots int `json:"ue_boots,omitzero"`
	// ByBoots is set when the cells count boots (my map --boots): Boots
	// and UEBoots of the lines hold, per cell, the boots of the window with
	// errors there.
	ByBoots bool `json:"by_boots,omitzero"`
}

// Modes of an AddressMap.
const (
	MapRows = "rows"
	MapBins = "bins"
)

// MaxBanksListed bounds AddressMap.Banks.
const MaxBanksListed = 8

// MapScale holds the lower bounds of the shades of the heat map: 1 to 2
// errors, 3 to 9, 10 to 99, 100 and more.
var MapScale = []int64{1, 3, 10, 100}

// BankRef names one bank of one device.
type BankRef struct {
	Rank      model.Opt[int] `json:"Rank,omitzero"`
	DeviceId  model.Opt[int] `json:"DeviceId,omitzero"`
	BankGroup model.Opt[int] `json:"BankGroup,omitzero"`
	Bank      model.Opt[int] `json:"Bank,omitzero"`
	// Label names the bank the way the findings do: "rank0 dev7 bg0 bank3".
	Label  string `json:"label"`
	Errors int64  `json:"errors"`
}

// MapLine is one line of the grid.
type MapLine struct {
	// Row is the first row of the line and Rows the number of rows it
	// stands for.
	Row  int `json:"row"`
	Rows int `json:"rows"`
	// Gap is set on a line that stands for rows without errors between two
	// groups of rows; such a line has no cells.
	Gap bool `json:"gap,omitzero"`
	// Cells holds the errors per cell, left to right.
	Cells []int64 `json:"cells,omitzero"`
	// UE lists the cells that hold an uncorrected error.
	UE     []int `json:"ue,omitzero"`
	Errors int64 `json:"errors,omitzero"`
	// Boots and UEBoots hold per cell the boots of the window with errors
	// and with uncorrected errors there, on a map by boots.
	Boots   []int `json:"boots,omitzero"`
	UEBoots []int `json:"ue_boots,omitzero"`
	// Note says what the line shows, where a fault explains it.
	Note *Text `json:"note,omitzero"`
}

// MaxCellUE bounds CellRef.UE.
const MaxCellUE = 3

// CellRef points from one cell of the grid to the ledger records behind
// it: a point of the figure leads back to the original records.
type CellRef struct {
	// Line is the index of the line in AddressMap.Lines, and Cell the
	// index of the cell in that line.
	Line int `json:"line"`
	Cell int `json:"cell"`
	// Errors is the number of errors in the cell, UEs the uncorrected ones
	// among them, and Records the number of ledger records they come from.
	Errors  int64 `json:"errors"`
	UEs     int64 `json:"ues,omitzero"`
	Records int   `json:"records"`
	// First and Last are the IDs of the earliest and the latest record of
	// the cell. From and To are their times; they are unset when the
	// records carry seconds since boot only.
	First int64     `json:"first"`
	Last  int64     `json:"last"`
	From  time.Time `json:"from,omitzero"`
	To    time.Time `json:"to,omitzero"`
	// UE lists the IDs of the records of uncorrected errors, up to
	// MaxCellUE of them, the earliest first.
	UE []int64 `json:"ue,omitzero"`
}

// ColumnMark points at one cell of the column axis.
type ColumnMark struct {
	Cell   int  `json:"cell"`
	Column int  `json:"column"`
	Note   Text `json:"note"`
}

// MapFault describes a fault by what it has in the bank that is drawn.
type MapFault struct {
	Mode      model.FaultMode `json:"mode"`
	ModeText  string          `json:"mode_text"`
	Estimated bool            `json:"estimated,omitzero"`
	Where     string          `json:"where"`
	Errors    int64           `json:"errors"`
	UE        int64           `json:"ue,omitzero"`
	Rows      int             `json:"rows"`
	Columns   int             `json:"columns"`
	// DQ lists the data lines of the device the records name.
	DQ    []int     `json:"dq,omitzero"`
	First time.Time `json:"first,omitzero"`
	Last  time.Time `json:"last,omitzero"`
	Text  Text      `json:"text"`
}

// MapOptions select the bank and the size of the grid.
type MapOptions struct {
	// Rank, Device, BankGroup and Bank narrow the choice of the bank. Among
	// the banks that fit, the one with the most errors is drawn.
	Rank, Device, BankGroup, Bank model.Opt[int]
	// RowFrom and RowTo zoom into a range of rows.
	RowFrom, RowTo model.Opt[int]
	// Cells is the widest grid allowed, in cells; 0 means 64.
	Cells int
	// Boots makes the cells count the boots of the window with errors.
	Boots bool
	// MaxLines is the tallest grid allowed, in lines; 0 means 24.
	MaxLines int
}

// ErrNoSuchBank: no bank with located errors fits the selection.
var ErrNoSuchBank = errors.New("no bank with located errors fits")

// mapMargin is the number of rows drawn above and below a group of rows
// with errors, so that a line can be seen against quiet neighbours.
const mapMargin = 3

// binLines is the number of lines of a map whose rows are binned.
const binLines = 16

type bankKey struct{ rank, dev, bg, bank int32 }

func optI32(o model.Opt[int]) int32 {
	if !o.Valid {
		return -1
	}
	return int32(o.V)
}

func keyOpt(v int32) model.Opt[int] {
	if v < 0 {
		return model.Opt[int]{}
	}
	return model.Some(int(v))
}

func eventBank(e *model.Event) bankKey {
	return bankKey{optI32(e.Rank), optI32(e.DeviceId), optI32(e.BankGroup), optI32(e.Bank)}
}

func (k bankKey) ref(n int64) BankRef {
	r := BankRef{Rank: keyOpt(k.rank), DeviceId: keyOpt(k.dev), BankGroup: keyOpt(k.bg), Bank: keyOpt(k.bank), Errors: n}
	var parts []string
	if k.rank >= 0 {
		parts = append(parts, fmt.Sprintf("rank%d", k.rank))
	}
	if k.dev >= 0 {
		parts = append(parts, fmt.Sprintf("dev%d", k.dev))
	}
	parts = append(parts, bankLabel(r.BankGroup, r.Bank))
	r.Label = strings.Join(parts, " ")
	return r
}

func located(e *model.Event) bool {
	return e.Kind != model.KindCounter && e.Row.Valid && e.Column.Valid && (e.Bank.Valid || e.BankGroup.Valid)
}

func pow2ceil(n int) int {
	if n <= 1 {
		return 1
	}
	return 1 << bits.Len(uint(n-1))
}

func pow2floor(n int) int {
	if n <= 1 {
		return 1
	}
	return 1 << (bits.Len(uint(n)) - 1)
}

// AddressMap builds the address map of one bank of a DIMM.
func (c *Case) AddressMap(ctx context.Context, h *engine.HostResult, d *engine.DIMMResult, opts MapOptions) (*AddressMap, error) {
	m := &AddressMap{Host: h.Host, Slot: d.Slot, DIMM: dimmInfo(d.UID, d.DIMM), Banks: []BankRef{}, Scale: MapScale, Lines: []MapLine{}, Faults: []MapFault{}}

	// Which banks have located errors.
	banks := map[bankKey]int64{}
	for e, err := range c.store.Events(ctx, store.EventFilter{DIMMUID: d.UID}) {
		if err != nil {
			return nil, err
		}
		n := int64(e.Errors())
		switch {
		case n == 0:
		case located(e):
			banks[eventBank(e)] += n
			m.Located += n
		default:
			m.Unlocated += n
		}
	}
	keys := make([]bankKey, 0, len(banks))
	for k := range banks {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if banks[a] != banks[b] {
			return banks[a] > banks[b]
		}
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if a.dev != b.dev {
			return a.dev < b.dev
		}
		if a.bg != b.bg {
			return a.bg < b.bg
		}
		return a.bank < b.bank
	})
	m.BankCount = len(keys)
	for i, k := range keys {
		if i < MaxBanksListed {
			m.Banks = append(m.Banks, k.ref(banks[k]))
		}
	}
	fits := func(k bankKey) bool {
		ok := func(want model.Opt[int], have int32) bool { return !want.Valid || int32(want.V) == have }
		return ok(opts.Rank, k.rank) && ok(opts.Device, k.dev) && ok(opts.BankGroup, k.bg) && ok(opts.Bank, k.bank)
	}
	var chosen *bankKey
	for i := range keys {
		if fits(keys[i]) {
			chosen = &keys[i]
			break
		}
	}
	if chosen == nil {
		if len(keys) == 0 {
			return m, nil // nothing to draw: the map says so
		}
		names := make([]string, len(m.Banks))
		for i, b := range m.Banks {
			names[i] = b.Label
		}
		return nil, fmt.Errorf("%w (banks with errors: %s)", ErrNoSuchBank, strings.Join(names, ", "))
	}
	ref := chosen.ref(banks[*chosen])
	m.Bank = &ref

	// The cells of the chosen bank, and the fault each record belongs to.
	faultOf := map[int64]int{}
	for i, f := range d.Faults {
		for _, id := range c.Result.EventIDs(f) {
			faultOf[id] = i
		}
	}
	type cellKey struct{ row, col int }
	type cellVal struct {
		ce, ue        int64
		refs          cellRefs
		boots, ueBoot map[int]bool
	}
	// The boots of the window, by boot ID.
	window := map[string]int{}
	if h.Recurrence != nil {
		for _, b := range h.Recurrence.Boots {
			window[b.BootID] = b.Index
		}
	}
	m.Window = len(window)
	bankBoots, bankUE := map[int]bool{}, map[int]bool{}
	type faultStat struct {
		errors, ue  int64
		rows, cols  map[int]struct{}
		dq          map[int]struct{}
		first, last time.Time
	}
	cells := map[cellKey]*cellVal{}
	stats := map[int]*faultStat{}
	rowFault := map[int]map[int]int64{} // row -> fault -> errors
	width := 0
	if d.DIMM != nil {
		width = d.DIMM.DeviceWidth.Or(0)
	}
	colMax := 0
	for e, err := range c.store.Events(ctx, store.EventFilter{DIMMUID: d.UID}) {
		if err != nil {
			return nil, err
		}
		n := int64(e.Errors())
		if n == 0 || !located(e) || eventBank(e) != *chosen {
			continue
		}
		k := cellKey{e.Row.V, e.Column.V}
		v := cells[k]
		if v == nil {
			v = &cellVal{}
			cells[k] = v
		}
		ue := e.ErrorSeverity.Uncorrected()
		if ue {
			v.ue += n
		} else {
			v.ce += n
		}
		v.refs.add(e, ue)
		if bi, ok := window[e.BootID]; ok && e.Kind != model.KindCounter {
			if v.boots == nil {
				v.boots, v.ueBoot = map[int]bool{}, map[int]bool{}
			}
			v.boots[bi], bankBoots[bi] = true, true
			if ue {
				v.ueBoot[bi], bankUE[bi] = true, true
			}
		}
		colMax = max(colMax, e.Column.V)

		fi, ok := faultOf[e.ID]
		if !ok {
			m.Stray += n
			continue
		}
		st := stats[fi]
		if st == nil {
			st = &faultStat{rows: map[int]struct{}{}, cols: map[int]struct{}{}, dq: map[int]struct{}{}}
			stats[fi] = st
		}
		st.errors += n
		if ue {
			st.ue += n
		}
		st.rows[e.Row.V], st.cols[e.Column.V] = struct{}{}, struct{}{}
		for _, q := range dataLines(e.ErrorVector, width) {
			st.dq[q] = struct{}{}
		}
		if e.TimeQuality != model.TimeRelative {
			if st.first.IsZero() || e.ErrorDT.Before(st.first) {
				st.first = e.ErrorDT
			}
			if e.ErrorDT.After(st.last) {
				st.last = e.ErrorDT
			}
		}
		if rowFault[e.Row.V] == nil {
			rowFault[e.Row.V] = map[int]int64{}
		}
		rowFault[e.Row.V][fi] += n
	}

	// The grid. Columns are binned to a power of two of cells; the axis
	// covers at least 1024 column addresses, the least any DDR4 or DDR5
	// device has.
	want := opts.Cells
	if want <= 0 {
		want = 64
	}
	m.Columns = pow2ceil(max(colMax+1, 1024))
	nCells := min(pow2floor(want), m.Columns)
	m.ColumnsPerCell = m.Columns / nCells

	rowSet := map[int]struct{}{}
	for k := range cells {
		if opts.RowFrom.Valid && k.row < opts.RowFrom.V || opts.RowTo.Valid && k.row > opts.RowTo.V {
			continue
		}
		rowSet[k.row] = struct{}{}
	}
	rows := make([]int, 0, len(rowSet))
	for r := range rowSet {
		rows = append(rows, r)
	}
	sort.Ints(rows)

	maxLines := opts.MaxLines
	if maxLines <= 0 {
		maxLines = 24
	}
	zoomed := opts.RowFrom.Valid && opts.RowTo.Valid && opts.RowTo.V >= opts.RowFrom.V
	type span struct{ lo, hi int }
	var spans []span
	switch {
	case zoomed && opts.RowTo.V-opts.RowFrom.V < maxLines:
		// A range the reader asked for is drawn as it is, quiet rows too.
		spans = []span{{opts.RowFrom.V, opts.RowTo.V}}
	case len(rows) > 0:
		lo, hi := rows[0], rows[0]
		for _, r := range rows[1:] {
			if r-hi > 2*mapMargin+1 {
				spans = append(spans, span{max(lo-mapMargin, 0), hi + mapMargin})
				lo = r
			}
			hi = r
		}
		spans = append(spans, span{max(lo-mapMargin, 0), hi + mapMargin})
		if opts.RowFrom.Valid {
			spans[0].lo = max(spans[0].lo, opts.RowFrom.V)
		}
		if opts.RowTo.Valid {
			spans[len(spans)-1].hi = min(spans[len(spans)-1].hi, opts.RowTo.V)
		}
	}
	need := len(spans) - 1
	for _, s := range spans {
		need += s.hi - s.lo + 1
	}

	line := func(row, n int) MapLine {
		ln := MapLine{Row: row, Rows: n, Cells: make([]int64, nCells)}
		return ln
	}
	// refs collects, per line and cell of the grid, the records behind it.
	type gridKey struct{ line, cell int }
	refs := map[gridKey]*cellRefs{}
	type bootSets struct{ all, ue map[int]bool }
	gridBoots := map[gridKey]*bootSets{}
	refer := func(line, cell int, v *cellVal) {
		k := gridKey{line, cell}
		r := refs[k]
		if r == nil {
			r = &cellRefs{}
			refs[k] = r
		}
		r.merge(&v.refs)
		if len(v.boots) > 0 {
			bs := gridBoots[k]
			if bs == nil {
				bs = &bootSets{all: map[int]bool{}, ue: map[int]bool{}}
				gridBoots[k] = bs
			}
			for b := range v.boots {
				bs.all[b] = true
			}
			for b := range v.ueBoot {
				bs.ue[b] = true
			}
		}
	}
	fill := func(index int, ln *MapLine) {
		ue := map[int]bool{}
		for k, v := range cells {
			if k.row < ln.Row || k.row >= ln.Row+ln.Rows {
				continue
			}
			i := k.col / m.ColumnsPerCell
			ln.Cells[i] += v.ce + v.ue
			ln.Errors += v.ce + v.ue
			if v.ue > 0 {
				ue[i] = true
			}
			refer(index, i, v)
		}
		for i := range ue {
			ln.UE = append(ln.UE, i)
		}
		sort.Ints(ln.UE)
	}
	switch {
	case len(spans) == 0:
		// The range the reader asked for holds no errors.
		m.Mode = MapRows
		m.RowsPerLine = 1
	case need <= maxLines:
		m.Mode, m.RowsPerLine = MapRows, 1
		byRow := map[int]*MapLine{}
		lineOf := map[int]int{}
		for i, s := range spans {
			if i > 0 {
				prev := spans[i-1].hi
				m.Lines = append(m.Lines, MapLine{Row: prev + 1, Rows: s.lo - prev - 1, Gap: true})
			}
			for r := s.lo; r <= s.hi; r++ {
				m.Lines = append(m.Lines, line(r, 1))
			}
		}
		for i := range m.Lines {
			if !m.Lines[i].Gap {
				byRow[m.Lines[i].Row] = &m.Lines[i]
				lineOf[m.Lines[i].Row] = i
			}
		}
		// One pass over the cells instead of one per line.
		ue := map[cellKey]bool{}
		for k, v := range cells {
			ln := byRow[k.row]
			if ln == nil {
				continue
			}
			i := k.col / m.ColumnsPerCell
			ln.Cells[i] += v.ce + v.ue
			ln.Errors += v.ce + v.ue
			if v.ue > 0 && !ue[cellKey{k.row, i}] {
				ue[cellKey{k.row, i}] = true
				ln.UE = append(ln.UE, i)
			}
			refer(lineOf[k.row], i, v)
		}
		for i := range m.Lines {
			sort.Ints(m.Lines[i].UE)
		}
	default:
		lo, hi := rows[0], rows[len(rows)-1]
		if zoomed {
			lo, hi = opts.RowFrom.V, opts.RowTo.V
		}
		step := pow2ceil((hi - lo + binLines) / binLines)
		lo = lo / step * step
		m.Mode, m.RowsPerLine = MapBins, step
		for r := lo; r <= hi; r += step {
			ln := line(r, step)
			fill(len(m.Lines), &ln)
			m.Lines = append(m.Lines, ln)
		}
	}
	m.Boots, m.UEBoots = len(bankBoots), len(bankUE)
	if opts.Boots {
		m.ByBoots = true
		for i := range m.Lines {
			ln := &m.Lines[i]
			if ln.Gap {
				continue
			}
			ln.Boots, ln.UEBoots = make([]int, len(ln.Cells)), make([]int, len(ln.Cells))
		}
		for k, bs := range gridBoots {
			ln := &m.Lines[k.line]
			ln.Boots[k.cell], ln.UEBoots[k.cell] = len(bs.all), len(bs.ue)
		}
	}
	for k, r := range refs {
		m.Refs = append(m.Refs, r.ref(k.line, k.cell))
	}
	sort.Slice(m.Refs, func(i, j int) bool {
		a, b := m.Refs[i], m.Refs[j]
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Cell < b.Cell
	})

	// What the faults have in this bank, and what the lines show.
	order := make([]int, 0, len(stats))
	for fi := range stats {
		order = append(order, fi)
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := stats[order[i]], stats[order[j]]
		if a.errors != b.errors {
			return a.errors > b.errors
		}
		return order[i] < order[j]
	})
	for _, fi := range order {
		f, st := d.Faults[fi], stats[fi]
		where := f.Where()
		if where == "" {
			where = ref.Label
		}
		mf := MapFault{Mode: f.Mode, ModeText: engine.ModeText(c.Lang, f.Mode), Estimated: f.Estimated, Where: where,
			Errors: st.errors, UE: st.ue, Rows: len(st.rows), Columns: len(st.cols), First: st.first, Last: st.last}
		for q := range st.dq {
			mf.DQ = append(mf.DQ, q)
		}
		sort.Ints(mf.DQ)
		args := []any{"fault", FaultName(c.Lang, f.Mode, f.Estimated), "where", where, "n", st.errors, "rows", len(st.rows), "columns", len(st.cols),
			"first", dayText(st.first), "last", dayText(st.last)}
		code := "map.fault"
		if len(mf.DQ) > 0 {
			code = "map.fault_dq"
			dq := make([]string, len(mf.DQ))
			for i, q := range mf.DQ {
				dq[i] = "DQ" + strconv.Itoa(q)
			}
			args = append(args, "dq", strings.Join(dq, ", "))
		}
		mf.Text = c.say(code, args...)
		m.Faults = append(m.Faults, mf)

		if f.Mode == model.ModeColumn && f.ColumnMin.Valid && f.ColumnMin == f.ColumnMax {
			m.Marks = append(m.Marks, ColumnMark{Cell: f.ColumnMin.V / m.ColumnsPerCell, Column: f.ColumnMin.V,
				Note: c.say("map.note.fault", "fault", FaultName(c.Lang, f.Mode, f.Estimated))})
		}
	}
	if m.Mode == MapRows {
		for i := range m.Lines {
			ln := &m.Lines[i]
			if ln.Gap || ln.Errors == 0 {
				continue
			}
			best, bestN := -1, int64(0)
			for fi, n := range rowFault[ln.Row] {
				if n > bestN || (n == bestN && fi < best) {
					best, bestN = fi, n
				}
			}
			if best < 0 {
				continue
			}
			f := d.Faults[best]
			var note Text
			switch f.Mode {
			case model.ModeSingleBit, model.ModeMultiRow:
				note = c.say("map.note.fault", "fault", FaultName(c.Lang, f.Mode, f.Estimated))
			case model.ModeRow:
				if f.TopRow.Valid && f.TopRow.V != ln.Row {
					note = c.say("map.note.neighbour")
				} else {
					note = c.say("map.note.fault", "fault", FaultName(c.Lang, f.Mode, f.Estimated))
				}
			default:
				continue
			}
			ln.Note = &note
		}
	}
	return m, nil
}

// cellRefs gathers what a CellRef says about a set of records.
type cellRefs struct {
	errors, ues int64
	records     int
	first, last recordAt
	ue          []recordAt
}

// recordAt is a record and where it stands in time. Records that carry
// seconds since boot only come before the dated ones, as in the ledger.
type recordAt struct {
	id    int64
	at    time.Time
	dated bool
}

func (a recordAt) before(b recordAt) bool {
	if !a.at.Equal(b.at) {
		return a.at.Before(b.at)
	}
	return a.id < b.id
}

func (c *cellRefs) add(e *model.Event, ue bool) {
	n := int64(e.Errors())
	r := recordAt{id: e.ID, at: e.ErrorDT, dated: e.TimeQuality != model.TimeRelative}
	c.errors += n
	if c.records == 0 || r.before(c.first) {
		c.first = r
	}
	if c.records == 0 || c.last.before(r) {
		c.last = r
	}
	c.records++
	if ue {
		c.ues += n
		c.ue = append(c.ue, r)
		c.trim()
	}
}

func (c *cellRefs) merge(o *cellRefs) {
	if o.records == 0 {
		return
	}
	if c.records == 0 || o.first.before(c.first) {
		c.first = o.first
	}
	if c.records == 0 || c.last.before(o.last) {
		c.last = o.last
	}
	c.errors, c.ues, c.records = c.errors+o.errors, c.ues+o.ues, c.records+o.records
	c.ue = append(c.ue, o.ue...)
	c.trim()
}

// trim keeps the earliest MaxCellUE records of uncorrected errors.
func (c *cellRefs) trim() {
	sort.Slice(c.ue, func(i, j int) bool { return c.ue[i].before(c.ue[j]) })
	if len(c.ue) > MaxCellUE {
		c.ue = c.ue[:MaxCellUE]
	}
}

func (c *cellRefs) ref(line, cell int) CellRef {
	r := CellRef{Line: line, Cell: cell, Errors: c.errors, UEs: c.ues, Records: c.records, First: c.first.id, Last: c.last.id}
	if c.first.dated {
		r.From = c.first.at
	}
	if c.last.dated {
		r.To = c.last.at
	}
	for _, u := range c.ue {
		r.UE = append(r.UE, u.id)
	}
	return r
}

// FaultName names a fault by its mode: "row fault", "system issue".
func FaultName(lang i18n.Lang, mode model.FaultMode, estimated bool) string {
	switch {
	case mode == model.ModeSystemIssue:
		return Messages.Text(lang, "fault.system", nil)
	case mode == model.ModeUndetermined:
		return Messages.Text(lang, "fault.undetermined", nil)
	case estimated:
		return Messages.Text(lang, "fault.estimated", map[string]any{"mode": engine.ModeText(lang, mode)})
	}
	return Messages.Text(lang, "fault.name", map[string]any{"mode": engine.ModeText(lang, mode)})
}

// dayText writes a day, or "?" for a time that is not known.
func dayText(t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	return t.UTC().Format("2006-01-02")
}

// dataLines returns the data lines (DQ) of the device an error vector
// names. A "dq:" mask names them directly. A "bit:" position counts within
// the data word; it names a data line when the width of the devices is
// known.
func dataLines(vector string, width int) []int {
	name, val, ok := strings.Cut(vector, ":")
	if !ok {
		return nil
	}
	switch name {
	case "dq":
		mask, err := strconv.ParseUint(strings.TrimPrefix(val, "0x"), 16, 32)
		if err != nil {
			return nil
		}
		var out []int
		for i := 0; mask != 0; i, mask = i+1, mask>>1 {
			if mask&1 != 0 {
				out = append(out, i)
			}
		}
		return out
	case "bit":
		n, err := strconv.Atoi(val)
		if err != nil || n < 0 || width <= 0 {
			return nil
		}
		return []int{n % width}
	}
	return nil
}
