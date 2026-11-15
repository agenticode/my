package report

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/view"
)

// figW is the width of every figure in CSS pixels. The figures scale with
// the page; at this width a pixel of a figure is a pixel of the page.
const figW = 704.0

// textWidth estimates the width of a text in the sans face of the report.
// The report cannot measure text, so the estimate errs on the wide side:
// a label is placed inside a mark only when the estimate fits, and a label
// that is not placed is still in the tooltip and in the table of the
// figure.
func textWidth(s string, size float64) float64 {
	w := 0.0
	for _, r := range s {
		switch {
		case r >= 0x1100 && (r <= 0x115f || r >= 0x2e80 && r <= 0xa4cf || r >= 0xac00 && r <= 0xd7a3 || r >= 0xf900 && r <= 0xfaff || r >= 0xff00 && r <= 0xff60):
			w += 1.0
		case r == ' ':
			w += 0.3
		case strings.ContainsRune("iljI.,:;|!'()[]", r):
			w += 0.34
		case r == 'm' || r == 'w' || r == 'M' || r == 'W' || r == '%' || r == '@':
			w += 0.92
		case unicode.IsUpper(r) || unicode.IsDigit(r):
			w += 0.68
		default:
			w += 0.58
		}
	}
	return w * size
}

// monoWidth is the width of a text in the monospace face.
func monoWidth(s string, size float64) float64 {
	n := 0.0
	for _, r := range s {
		if r >= 0x1100 && (r <= 0x115f || r >= 0x2e80 && r <= 0xa4cf || r >= 0xac00 && r <= 0xd7a3) {
			n += 2
			continue
		}
		n++
	}
	return n * size * 0.62
}

// svg starts a figure of the given size. The title names it for readers
// who do not see it.
func svg(class string, w, h float64, title string) *node {
	return el("svg", A{"class", class, "viewBox", "0 0 " + num(w) + " " + num(h), "width", num(w), "height", num(h), "role", "img", "aria-label", title},
		el("title", title))
}

// gradeClass returns the style class of a grade or of a slot state.
func gradeClass(state string) string {
	switch model.VerdictGrade(state) {
	case model.VerdictOK:
		return "g-ok"
	case model.VerdictObserve:
		return "g-observe"
	case model.VerdictConsiderReplacement:
		return "g-consider"
	case model.VerdictReplaceNow:
		return "g-replace"
	case model.VerdictHoldReplacement:
		return "g-hold"
	}
	if state == view.SlotEmpty {
		return "g-empty"
	}
	return "g-unknown"
}

// glyph draws the mark of a grade around the origin: a ring for observe,
// an exclamation mark for consider replacement, a cross for replace now
// and a question mark for hold replacement. A grade is told by its mark
// as well as by its colour, so that the figures read in a grey print and
// with any colour vision. The mark of ok is nothing: quiet DIMMs stay
// quiet.
func glyph(state string) *node {
	g := el("g", A{"class", "gl"})
	switch model.VerdictGrade(state) {
	case model.VerdictObserve:
		g.add(el("circle", A{"r", "2.7"}))
	case model.VerdictConsiderReplacement:
		g.add(el("path", A{"d", "M0 -4.2V1"}), el("circle", A{"class", "dot", "cy", "3.9", "r", "1.15"}))
	case model.VerdictReplaceNow:
		g.add(el("path", A{"d", "M-3 -3L3 3M3 -3L-3 3"}))
	case model.VerdictHoldReplacement:
		g.add(el("path", A{"d", "M-2.6 -2.3A2.6 2.6 0 1 1 1.3 -0.1Q0 0.7 0 1.6"}), el("circle", A{"class", "dot", "cy", "4", "r", "1.15"}))
	default:
		return nil
	}
	return g
}

// icon is the swatch of a grade for use in text: a small square in the
// colour of the grade with its mark.
func icon(state string, size float64) *node {
	s := el("svg", A{"class", classes("ic", gradeClass(state)), "viewBox", "0 0 14 14", "width", num(size), "height", num(size), "aria-hidden", "true"},
		el("rect", A{"class", "cell", "x", "0.5", "y", "0.5", "width", "13", "height", "13", "rx", "3"}))
	if g := glyph(state); g != nil {
		g.add(A{"transform", "translate(7 7)"})
		s.add(g)
	}
	return s
}

// badge is a grade in words with its swatch.
func (doc *Document) badge(grade model.VerdictGrade) *node {
	return el("span", A{"class", "badge"}, icon(string(grade), 14), engine.GradeText(doc.Lang, grade))
}

// slotMapFigure draws the slots of the board: one line per socket, one
// group per memory channel. anchors maps a slot to the name of the section
// about its DIMM.
func (doc *Document) slotMapFigure(sm *view.SlotMap, anchors map[string]string, subject string) *node {
	const (
		labelSize = 12.0
		cellSize  = 9.5
		stickH    = 34.0
	)
	rowLabel := func(r *view.SlotRow) string {
		switch {
		case r.Label != "":
			return r.Label
		case r.Socket.Valid:
			return doc.vsay("ov.socket", "n", r.Socket.V)
		}
		return ""
	}
	labelW := 0.0
	maxCell := 0.0
	units := 0.0 // widest row, in sticks and gaps
	for i := range sm.Rows {
		r := &sm.Rows[i]
		labelW = max(labelW, textWidth(rowLabel(r), labelSize))
		u := 0.0
		for c, ch := range r.Channels {
			if c > 0 {
				u += 0.55 // gap between channels
			}
			u += float64(len(ch.Slots)) + 0.18*float64(len(ch.Slots)-1)
			for _, sl := range ch.Slots {
				maxCell = max(maxCell, monoWidth(sl.Label, cellSize))
			}
		}
		units = max(units, u)
	}
	if labelW > 0 {
		labelW += 14
	}
	// A stick is as wide as the room allows, between 9 and 24 pixels.
	stick := 24.0
	if units > 0 {
		stick = min(max((figW-labelW)/units, 9), 24)
	}
	gapIn, gapCh := stick*0.18, stick*0.55
	// Slot names stand below the sticks; names wider than a stick stand
	// upright.
	upright := maxCell > stick+gapIn-1
	band := cellSize + 6
	if upright {
		band = maxCell + 8
	}
	rowH := stickH + band + 12
	h := float64(len(sm.Rows))*rowH + 2
	s := svg("fig slotmap", figW, h, doc.say("fig.slotmap", "host", doc.Overview.Host.Name))

	for i := range sm.Rows {
		r := &sm.Rows[i]
		y := float64(i)*rowH + 10
		if l := rowLabel(r); l != "" {
			s.add(el("text", A{"class", "rowlabel", "x", "0", "y", num(y + stickH/2 + 4)}, l))
		}
		x := labelW
		for c, ch := range r.Channels {
			if c > 0 {
				x += gapCh
			}
			for k := range ch.Slots {
				cell := &ch.Slots[k]
				if k > 0 {
					x += gapIn
				}
				state := doc.vsay("ov.empty")
				switch cell.State {
				case view.SlotUnknown:
					state = doc.vsay("ov.unknown")
				case view.SlotEmpty:
				default:
					state = engine.GradeText(doc.Lang, model.VerdictGrade(cell.State))
				}
				title := doc.say("slot.title", "slot", cell.Slot, "state", state)
				if cell.Serial != "" {
					title += ", " + doc.say("slot.serial", "serial", cell.Serial)
				}
				if cell.CE+cell.UE > 0 {
					title += ", " + doc.vsay("ov.errors", "ce", cell.CE, "ue", cell.UE)
				}
				var g *node
				if a := anchors[cell.Slot]; a != "" {
					g = el("a", A{"href", "#" + a, "class", classes("slot", gradeClass(cell.State)), "aria-label", title})
				} else {
					g = el("g", A{"class", classes("slot", gradeClass(cell.State))})
				}
				g.add(el("title", title),
					el("rect", A{"class", "cell", "x", num(x), "y", num(y), "width", num(stick), "height", num(stickH), "rx", "3"}))
				if gl := glyph(cell.State); gl != nil && stick >= 12 {
					gl.add(A{"transform", "translate(" + num(x+stick/2) + " " + num(y+stickH/2) + ")"})
					g.add(gl)
				}
				if subject != "" && cell.Slot == subject {
					// The subject of the case is pointed at.
					cx := x + stick/2
					g.add(el("path", A{"class", "pointer", "d", "M" + num(cx-4) + " " + num(y-8) + "h8l-4 6z"}))
				}
				if upright {
					tx, ty := x+stick/2+cellSize*0.35, y+stickH+5
					g.add(el("text", A{"class", "celllabel", "x", num(tx), "y", num(ty), "text-anchor", "end", "transform", "rotate(-90 " + num(tx) + " " + num(ty) + ")"}, cell.Label))
				} else {
					g.add(el("text", A{"class", "celllabel", "x", num(x + stick/2), "y", num(y + stickH + cellSize + 3), "text-anchor", "middle"}, cell.Label))
				}
				s.add(g)
				x += stick
			}
		}
	}
	return s
}

// pointerIcon is the mark that points at the subject of a case, as a
// swatch for a legend.
func pointerIcon() *node {
	return el("svg", A{"class", "ic slot", "viewBox", "0 0 14 14", "width", "14", "height", "14", "aria-hidden", "true"},
		el("path", A{"class", "pointer", "d", "M3 4h8l-4 6z"}))
}

// slotLegend lists what the cells of the slot map stand for. With subject
// set it also says what the pointer above a cell means.
func (doc *Document) slotLegend(sm *view.SlotMap, subject bool) *node {
	ul := el("ul", A{"class", "legend"})
	for _, g := range []model.VerdictGrade{model.VerdictReplaceNow, model.VerdictConsiderReplacement, model.VerdictObserve, model.VerdictOK, model.VerdictHoldReplacement} {
		ul.add(el("li", icon(string(g), 14), engine.GradeText(doc.Lang, g)))
	}
	empty, unknown := false, false
	for _, r := range sm.Rows {
		for _, ch := range r.Channels {
			for _, sl := range ch.Slots {
				empty = empty || sl.State == view.SlotEmpty
				unknown = unknown || sl.State == view.SlotUnknown
			}
		}
	}
	if empty {
		ul.add(el("li", icon(view.SlotEmpty, 14), doc.vsay("ov.empty")))
	}
	if unknown {
		ul.add(el("li", icon(view.SlotUnknown, 14), doc.vsay("ov.unknown")))
	}
	if subject {
		ul.add(el("li", pointerIcon(), doc.say("sum.subject")))
	}
	return ul
}

// sparkline draws a few values as small columns on their own scale.
func sparkline(values []int64, title string) *node {
	const (
		barW = 5.0
		gap  = 1.4
		h    = 18.0
	)
	w := float64(len(values))*(barW+gap) - gap
	s := svg("spark", w, h, title)
	var top int64
	for _, v := range values {
		top = max(top, v)
	}
	for i, v := range values {
		x := float64(i) * (barW + gap)
		if v == 0 || top == 0 {
			// A step without errors still shows as a step.
			s.add(el("rect", A{"class", "zero", "x", num(x), "y", num(h - 1.5), "width", num(barW), "height", "1.5"}))
			continue
		}
		bh := max(float64(v)/float64(top)*(h-1), 2.5)
		s.add(el("rect", A{"class", "bar", "x", num(x), "y", num(h - bh), "width", num(barW), "height", num(bh), "rx", "1"}))
	}
	return s
}

// percent writes a share as a whole percentage.
func percent(share float64) string {
	switch {
	case share <= 0:
		return "0%"
	case share < 0.005:
		return "<1%"
	}
	return fmt.Sprintf("%.0f%%", math.Min(share*100, 100))
}

// segmentLabel names a segment of the topology bars.
func (doc *Document) segmentLabel(sg *view.Segment) string {
	if sg.Others > 0 {
		return doc.say("topo.others", "n", sg.Others)
	}
	return sg.Label
}

// topologyFigure draws the topology bars of a DIMM: four bars, each
// splitting all errors of the DIMM at one level.
func (doc *Document) topologyFigure(t *view.Topology, slot string) *node {
	const (
		labelW = 54.0
		barH   = 22.0
		gap    = 5.0
		size   = 11.0
	)
	w := figW - labelW
	h := float64(len(t.Levels))*(barH+gap) - gap
	s := svg("fig topo", figW, h, doc.say("fig.topology", "slot", slot))
	for li := range t.Levels {
		lv := &t.Levels[li]
		y := float64(li) * (barH + gap)
		s.add(el("text", A{"class", "rowlabel", "x", "0", "y", num(y + barH/2 + 4)}, lv.Name))
		if !lv.Known {
			// Nothing in the records says which branch: one segment says so.
			text := doc.vsay("topo.unreported")
			g := el("g", A{"class", "seg dim"}, el("title", lv.Name+": "+text),
				el("rect", A{"x", num(labelW), "y", num(y), "width", num(w), "height", num(barH), "rx", "4"}))
			if textWidth(text, size)+16 <= w {
				g.add(el("text", A{"class", "seglabel", "x", num(labelW + 8), "y", num(y + barH/2 + 4)}, text))
			}
			s.add(g)
			continue
		}
		cum := 0.0
		for i := range lv.Segments {
			sg := &lv.Segments[i]
			x0 := labelW + cum*w
			cum += sg.Share
			x1 := labelW + cum*w
			if i == len(lv.Segments)-1 {
				x1 = labelW + w
			}
			label := doc.segmentLabel(sg)
			title := doc.say("topo.seg", "level", lv.Name, "label", label, "n", sg.Errors, "share", percent(sg.Share))
			cls := "seg"
			if sg.Unknown || sg.Others > 0 {
				cls = "seg dim"
			}
			g := el("g", A{"class", cls}, el("title", title))
			// Neighbours are kept apart by a gap in the colour of the page.
			sw := x1 - x0
			pad := 1.0
			if sw < 3 {
				pad = 0
			}
			if sw-2*pad < 0.4 {
				continue // too narrow to draw; the table of the figure lists it
			}
			g.add(el("rect", A{"x", num(x0 + pad), "y", num(y), "width", num(sw - 2*pad), "height", num(barH), "rx", num(min(4, (sw-2*pad)/2))}))
			pct := percent(sg.Share)
			lw, pw := monoWidth(label, size), textWidth(pct, size)
			switch {
			case lw+pw+30 <= sw:
				g.add(el("text", A{"class", "seglabel", "x", num(x0 + 8), "y", num(y + barH/2 + 4)}, label),
					el("text", A{"class", "segshare", "x", num(x1 - 8), "y", num(y + barH/2 + 4), "text-anchor", "end"}, pct))
			case lw+16 <= sw:
				g.add(el("text", A{"class", "seglabel", "x", num(x0 + 8), "y", num(y + barH/2 + 4)}, label))
			}
			s.add(g)
		}
	}
	return s
}

// heatLevel returns the shade of a count on the scale of the address map.
func heatLevel(n int64, scale []int64) int {
	level := 0
	for i, lo := range scale {
		if n >= lo {
			level = i + 1
		}
	}
	return min(level, 4)
}

// scaleText names the counts a shade of the address map stands for.
func scaleText(scale []int64, i int) string {
	lo := scale[i]
	if i+1 < len(scale) {
		if hi := scale[i+1] - 1; hi > lo {
			return fmt.Sprintf("%d-%d", lo, hi)
		}
		return fmt.Sprint(lo)
	}
	return fmt.Sprintf("%d+", lo)
}

// swatch is a small square of one shade of the address map.
func swatch(class string, ue bool) *node {
	s := el("svg", A{"class", "ic", "viewBox", "0 0 12 12", "width", "12", "height", "12", "aria-hidden", "true"},
		el("rect", A{"class", class, "width", "12", "height", "12", "rx", "2"}))
	if ue {
		s.add(el("path", A{"class", "uex", "d", "M3.4 3.4L8.6 8.6M8.6 3.4L3.4 8.6"}))
	}
	return s
}

// mapLegend lists the shades of the address map.
func (doc *Document) mapLegend(m *view.AddressMap) *node {
	ul := el("ul", A{"class", "legend"})
	ul.add(el("li", swatch("ht0", false), "0"))
	for i := range m.Scale {
		ul.add(el("li", swatch(fmt.Sprintf("ht%d", min(i+1, 4)), false), scaleText(m.Scale, i)))
	}
	ul.add(el("li", swatch("ue", true), doc.vsay("map.ue")))
	return ul
}

// cellRange names the rows and columns a cell of the address map covers.
func (doc *Document) cellRange(m *view.AddressMap, ln *view.MapLine, cell int) string {
	from, to := cell*m.ColumnsPerCell, (cell+1)*m.ColumnsPerCell-1
	if ln.Rows > 1 {
		return doc.say("map.celln", "row", fmt.Sprintf("%#x", ln.Row), "rowto", fmt.Sprintf("%#x", ln.Row+ln.Rows-1),
			"from", fmt.Sprintf("%#x", from), "to", fmt.Sprintf("%#x", to))
	}
	return doc.say("map.cell1", "row", fmt.Sprintf("%#x", ln.Row), "from", fmt.Sprintf("%#x", from), "to", fmt.Sprintf("%#x", to))
}

// cellText says what a cell of the address map holds.
func (doc *Document) cellText(m *view.AddressMap, r *view.CellRef) string {
	ln := &m.Lines[r.Line]
	text := doc.cellRange(m, ln, r.Cell) + ": "
	if r.UEs > 0 {
		text += doc.say("map.count_ue", "n", r.Errors, "records", r.Records, "ue", r.UEs)
	} else {
		text += doc.say("map.count", "n", r.Errors, "records", r.Records)
	}
	if !r.From.IsZero() && !r.To.IsZero() {
		text += "; " + doc.say("map.span", "from", day(r.From), "to", day(r.To))
	}
	return text
}

// cellKeys lists the records a cell leads to: its uncorrected errors, its
// first and its latest record.
func cellKeys(r *view.CellRef) []string {
	var out []string
	seen := map[int64]bool{}
	add := func(id int64) {
		if id != 0 && !seen[id] {
			seen[id] = true
			out = append(out, key(model.RecordEvent, id))
		}
	}
	for _, id := range r.UE {
		add(id)
	}
	add(r.First)
	add(r.Last)
	return out
}

// link wraps a mark in a link to the records behind it. The link works
// without the script of the report: it leads to the first of the records
// in the appendix. With the script the records open beside the figure.
func link(keys []string, label string, kids ...*node) *node {
	a := el("a", A{"href", "#rec-" + keys[0], "data-rec", strings.Join(keys, " "), "data-label", label, "aria-label", label}, el("title", label))
	a.add(kids)
	return a
}

// addressMapFigure draws the errors of one bank by row and column.
func (doc *Document) addressMapFigure(d *DIMM) *node {
	m := d.Map
	const (
		labelW = 58.0
		noteW  = 112.0
		rowH   = 16.0
		cellH  = 15.0
		axisH  = 22.0
		size   = 10.5
		textY  = 11.5 // baseline of a text within a row
	)
	cells := 0
	for _, ln := range m.Lines {
		cells = max(cells, len(ln.Cells))
	}
	cells = max(cells, 1)
	pitch := (figW - labelW - noteW) / float64(cells)
	gridW := pitch * float64(cells)
	marks := len(m.Marks)
	h := axisH + float64(len(m.Lines))*rowH + 4 + float64(marks)*14
	s := svg("fig amap", figW, h, doc.say("fig.map", "slot", d.Card.Slot, "bank", m.Bank.Label))

	// The column axis: the first and the last address, and the quarters
	// between them where there is room.
	s.add(el("text", A{"class", "axistitle", "x", "0", "y", "12"}, doc.say("tv.row")))
	digits := len(fmt.Sprintf("%x", max(m.Columns-1, 1)))
	hex := func(v int) string { return fmt.Sprintf("0x%0*x", digits, v) }
	last := hex(m.Columns - 1)
	s.add(el("text", A{"class", "axis", "x", num(labelW), "y", "12"}, hex(0)),
		el("text", A{"class", "axis", "x", num(labelW + gridW), "y", "12", "text-anchor", "end"}, last))
	if gridW >= 5*monoWidth(last, size)+40 {
		for q := 1; q <= 3; q++ {
			s.add(el("text", A{"class", "axis", "x", num(labelW + gridW*float64(q)/4), "y", "12", "text-anchor", "middle"}, hex(m.Columns*q/4)))
		}
	}
	s.add(el("text", A{"class", "axistitle", "x", num(labelW + gridW + 8), "y", "12"}, doc.say("tv.columns")))
	s.add(el("path", A{"class", "ax", "d", "M" + num(labelW) + " " + num(axisH-4.5) + "H" + num(labelW+gridW)}))

	refs := map[[2]int]int{}
	for i, r := range m.Refs {
		refs[[2]int{r.Line, r.Cell}] = i
	}
	for li := range m.Lines {
		ln := &m.Lines[li]
		y := axisH + float64(li)*rowH
		if ln.Gap {
			// Rows without errors between two groups of rows are left out
			// and counted.
			cx := labelW - 22
			s.add(el("path", A{"class", "gapdots", "d", fmt.Sprintf("M%s %sv.01M%s %sv.01M%s %sv.01", num(cx), num(y+3.5), num(cx), num(y+7.5), num(cx), num(y+11.5))}),
				el("text", A{"class", "gapnote", "x", num(labelW), "y", num(y + textY)}, doc.vsay("map.gap", "n", ln.Rows)))
			continue
		}
		s.add(el("text", A{"class", "rowaddr", "x", num(labelW - 8), "y", num(y + textY), "text-anchor", "end"}, fmt.Sprintf("%#x", ln.Row)))
		ue := map[int]bool{}
		for _, i := range ln.UE {
			ue[i] = true
		}
		// Cells without errors are drawn as runs: one mark for many cells.
		var quiet strings.Builder
		runFrom := -1
		flush := func(to int) {
			if runFrom < 0 {
				return
			}
			for c := runFrom; c < to; c++ {
				fmt.Fprintf(&quiet, "M%s %sh%sv%sh-%sz", num(labelW+float64(c)*pitch), num(y), num(pitch-1), num(cellH), num(pitch-1))
			}
			runFrom = -1
		}
		for c, n := range ln.Cells {
			if n == 0 {
				if runFrom < 0 {
					runFrom = c
				}
				continue
			}
			flush(c)
			x := labelW + float64(c)*pitch
			cls := fmt.Sprintf("ht%d", heatLevel(n, m.Scale))
			var mark []*node
			if ue[c] {
				mark = append(mark, el("rect", A{"class", "ue", "x", num(x), "y", num(y), "width", num(pitch - 1), "height", num(cellH)}),
					el("path", A{"class", "uex", "d", fmt.Sprintf("M%s %sL%s %sM%s %sL%s %s",
						num(x+1.7), num(y+4.6), num(x+pitch-2.7), num(y+cellH-4.6), num(x+pitch-2.7), num(y+4.6), num(x+1.7), num(y+cellH-4.6))}))
			} else {
				mark = append(mark, el("rect", A{"class", cls, "x", num(x), "y", num(y), "width", num(pitch - 1), "height", num(cellH)}))
			}
			ri, ok := refs[[2]int{li, c}]
			switch {
			case ok && d.Linked[ri]:
				s.add(link(cellKeys(&m.Refs[ri]), doc.cellText(m, &m.Refs[ri]), mark...).add(A{"class", "pt"}))
			case ok:
				s.add(el("g", el("title", doc.cellText(m, &m.Refs[ri])), mark))
			default:
				s.add(el("g", mark))
			}
		}
		flush(len(ln.Cells))
		if quiet.Len() > 0 {
			s.add(el("path", A{"class", "ht0", "d", quiet.String()}))
		}
		if ln.Note != nil && textWidth(ln.Note.Text, size)+20 <= noteW {
			nx := labelW + gridW + 8
			s.add(el("path", A{"class", "notearrow", "d", fmt.Sprintf("M%s %sh-8m0 0l3.5 -3m-3.5 3l3.5 3", num(nx+9), num(y+cellH/2))}),
				el("text", A{"class", "note", "x", num(nx + 14), "y", num(y + textY)}, ln.Note.Text))
		}
	}
	// A column fault is pointed at from below the grid.
	base := axisH + float64(len(m.Lines))*rowH + 2
	for i, mk := range m.Marks {
		cx := labelW + (float64(mk.Cell)+0.5)*pitch - 0.5
		y := base + float64(i)*14
		text := doc.say("map.column", "col", fmt.Sprintf("%#x", mk.Column), "note", mk.Note.Text)
		t := el("text", A{"class", "note", "x", num(cx + 8), "y", num(y + 10)}, text)
		if cx+8+textWidth(text, size) > figW {
			t = el("text", A{"class", "note", "x", num(cx - 8), "y", num(y + 10), "text-anchor", "end"}, text)
		}
		s.add(el("path", A{"class", "caret", "d", fmt.Sprintf("M%s %sl4 6h-8z", num(cx), num(y+2))}), t)
	}
	return s
}

// niceTop returns a round number at or above v for the top of an axis:
// 1, 2 or 5 times a power of ten, and at least 4 so that the axis has
// whole steps.
func niceTop(v float64) float64 {
	if v <= 4 {
		return 4
	}
	p := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 2, 4, 5, 10} {
		if v <= m*p {
			return m * p
		}
	}
	return 10 * p
}

// markClass returns the style of a marker kind on the timeline.
func markClass(kind string) string {
	switch kind {
	case view.MarkUE:
		return "mk-ue"
	case view.MarkBoot, view.MarkMove:
		return "mk-ctx"
	}
	return "mk-act"
}

// guided says whether a line runs up from the marks of a kind: from a
// boot, a repair, a reseat, a replacement and a slot change, the events
// the days before and after are compared across.
func guided(kind string) bool {
	switch kind {
	case view.MarkUE, string(model.ActionPageOffline), string(model.ActionDisable), string(model.ActionMarkFailed):
		return false
	}
	return true
}

// laneLabel names the lane of a marker kind.
func (doc *Document) laneLabel(kind string) string {
	switch kind {
	case view.MarkBoot:
		return doc.say("lane.boot")
	case view.MarkUE:
		return doc.say("lane.ue")
	case view.MarkMove:
		return doc.say("lane.move")
	}
	return doc.kindText(kind)
}

// timelineFigure draws errors per day with the events of the period in
// lanes below. A timeline with one row gets an axis of counts; a timeline
// with several rows draws each on its own scale and prints its peak.
func (doc *Document) timelineFigure(tl *view.Timeline, what string) *node {
	const (
		size   = 10.5
		rightW = 92.0
		laneH  = 17.0
		axisH  = 20.0
	)
	single := len(tl.Series) <= 1
	labelW := 30.0
	for _, sr := range tl.Series {
		if !single {
			labelW = max(labelW, monoWidth(sr.Label, 11)+12)
		}
	}
	for _, g := range tl.Markers {
		labelW = max(labelW, textWidth(doc.laneLabel(g.Kind), size)+12)
	}
	labelW = min(labelW, 170)
	plotW := figW - labelW - rightW
	if single {
		plotW = figW - labelW - 14
	}
	// One column per day; a long period shares a column among several
	// days, so that a column is at least 2 pixels wide.
	per := max(int(math.Ceil(float64(tl.Days)/(plotW/2))), 1)
	cols := (tl.Days + per - 1) / per
	pitch := plotW / float64(cols)
	barW := pitch
	if pitch >= 3 {
		barW = pitch - 1
	}
	xOf := func(day int) float64 { return labelW + (float64(day/per)+0.5)*pitch }

	rowH := 26.0
	top := 4.0
	if single {
		rowH, top = 104, 16
	}
	rows := max(len(tl.Series), 1)
	plotH := float64(rows) * rowH
	lanesY := top + plotH + axisH
	h := lanesY + float64(len(tl.Markers))*laneH + 4
	s := svg("fig tline", figW, h, doc.say("fig.timeline", "what", what, "from", day(tl.From), "to", day(tl.To)))

	bins := func(sr *view.Series) []float64 {
		out := make([]float64, cols)
		for c := range out {
			var sum int64
			n := 0
			for d := c * per; d < min((c+1)*per, tl.Days) && d < len(sr.CE); d++ {
				sum += sr.CE[d]
				n++
			}
			if n > 0 {
				out[c] = float64(sum) / float64(n)
			}
		}
		return out
	}
	binTitle := func(sr *view.Series, c int, v float64) string {
		from := tl.From.AddDate(0, 0, c*per)
		text := doc.say("tl.day", "day", day(from), "n", int64(math.Round(v)))
		if per > 1 {
			to := tl.From.AddDate(0, 0, min((c+1)*per, tl.Days)-1)
			text = doc.say("tl.days", "from", day(from), "to", day(to), "n", int64(math.Round(v*float64(per))))
		}
		if !single {
			// Among several rows a column says which row it belongs to.
			text = sr.Label + ", " + text
		}
		return text
	}

	for i := range rows {
		y0 := top + float64(i)*rowH
		area := rowH - 6
		base := y0 + area
		var sr *view.Series
		if i < len(tl.Series) {
			sr = &tl.Series[i]
		}
		peak := 0.0
		var vals []float64
		if sr != nil {
			vals = bins(sr)
			for _, v := range vals {
				peak = max(peak, v)
			}
		}
		axisTop := peak
		if single {
			// An axis of counts: three lines at round numbers.
			axisTop = niceTop(peak)
			s.add(el("text", A{"class", "axistitle", "x", num(labelW), "y", "10"}, doc.say("tl.axis")))
			if per > 1 {
				s.add(el("text", A{"class", "axistitle", "x", num(labelW + plotW), "y", "10", "text-anchor", "end"}, doc.say("tl.binned", "n", per)))
			}
			for k := 0; k <= 2; k++ {
				v := axisTop * float64(k) / 2
				gy := base - area*float64(k)/2
				cls := "grid"
				if k == 0 {
					cls = "ax"
				}
				s.add(el("path", A{"class", cls, "d", "M" + num(labelW) + " " + num(gy+0.5) + "H" + num(labelW+plotW)}),
					el("text", A{"class", "tick", "x", num(labelW - 6), "y", num(gy + 3.5), "text-anchor", "end"}, i18n.Thousands(int64(v))))
			}
		} else {
			s.add(el("path", A{"class", "ax", "d", "M" + num(labelW) + " " + num(base+0.5) + "H" + num(labelW+plotW)}))
			if sr != nil {
				s.add(el("text", A{"class", "serieslabel", "x", num(labelW - 8), "y", num(base - 3), "text-anchor", "end"}, sr.Label))
				if sr.Peak > 0 {
					s.add(el("text", A{"class", "peak", "x", num(labelW + plotW + 8), "y", num(base - 3)}, doc.vsay("tl.peak", "n", sr.Peak)))
				}
			}
		}
		if sr == nil || axisTop == 0 {
			continue
		}
		g := el("g", A{"class", "bars"})
		for c, v := range vals {
			if v == 0 {
				continue
			}
			bh := max(v/axisTop*area, 1.5)
			x := labelW + float64(c)*pitch
			g.add(el("rect", A{"x", num(x), "y", num(base - bh), "width", num(barW), "height", num(bh)}, el("title", binTitle(sr, c, v))))
		}
		s.add(g)
	}

	// The time axis: the first day, the first of each month, the last day.
	axisY := top + plotH
	layout := "01-02"
	if tl.Days > 366 {
		layout = "2006-01"
	}
	type tick struct {
		x     float64
		text  string
		align string
	}
	var ticks []tick
	taken := func(x0, x1 float64) bool {
		for _, t := range ticks {
			tw := monoWidth(t.text, size)
			a, b := t.x, t.x+tw
			switch t.align {
			case "end":
				a, b = t.x-tw, t.x
			case "middle":
				a, b = t.x-tw/2, t.x+tw/2
			}
			if x0 < b+8 && x1 > a-8 {
				return true
			}
		}
		return false
	}
	first, lastDay := tl.From.Format(layout), tl.To.Format(layout)
	if tl.Days > 366 {
		first, lastDay = day(tl.From), day(tl.To)
	}
	ticks = append(ticks, tick{labelW, first, "start"})
	if x := labelW + plotW; !taken(x-monoWidth(lastDay, size), x) {
		ticks = append(ticks, tick{x, lastDay, "end"})
	}
	for d := tl.From.AddDate(0, 0, 1); !d.After(tl.To); d = d.AddDate(0, 0, 1) {
		if d.Day() != 1 || (tl.Days > 366 && d.Month() != time.January && d.Month() != time.July) {
			continue
		}
		off := int(d.Sub(tl.From).Hours() / 24)
		x := labelW + float64(off)/float64(per)*pitch
		text := d.Format(layout)
		tw := monoWidth(text, size)
		if !taken(x-tw/2, x+tw/2) {
			ticks = append(ticks, tick{x, text, "middle"})
			s.add(el("path", A{"class", "ax", "d", "M" + num(x) + " " + num(axisY) + "v4"}))
		}
	}
	for _, t := range ticks {
		s.add(el("text", A{"class", "tick", "x", num(t.x), "y", num(axisY + 14), "text-anchor", t.align}, t.text))
	}

	// The lanes: one per kind of event, a mark per day with an event.
	for gi := range tl.Markers {
		g := &tl.Markers[gi]
		cy := lanesY + float64(gi)*laneH + laneH/2
		s.add(el("text", A{"class", "lanelabel", "x", num(labelW - 8), "y", num(cy + 3.5), "text-anchor", "end"}, doc.laneLabel(g.Kind)),
			el("path", A{"class", "lane", "d", "M" + num(labelW) + " " + num(cy) + "H" + num(labelW+plotW)}))
		byDay := map[int][]*view.Mark{}
		for i := range g.Marks {
			mk := &g.Marks[i]
			byDay[mk.Day] = append(byDay[mk.Day], mk)
		}
		// Marks that share a column are one mark.
		type spot struct {
			x     float64
			days  []int
			marks []*view.Mark
		}
		var spots []*spot
		for _, d := range g.Days {
			x := xOf(d)
			if n := len(spots); n > 0 && spots[n-1].x == x {
				spots[n-1].days = append(spots[n-1].days, d)
				spots[n-1].marks = append(spots[n-1].marks, byDay[d]...)
				continue
			}
			spots = append(spots, &spot{x: x, days: []int{d}, marks: byDay[d]})
		}
		if guided(g.Kind) && len(spots) <= 12 {
			for _, sp := range spots {
				s.add(el("path", A{"class", "guide", "d", "M" + num(sp.x) + " " + num(top) + "V" + num(cy)}))
			}
		}
		for si, sp := range spots {
			// What the mark says, and the records it leads to.
			label := doc.laneLabel(g.Kind) + ", " + day(tl.From.AddDate(0, 0, sp.days[0]))
			if len(sp.marks) > 0 {
				label = sp.marks[0].Text.Text
				if n := len(sp.marks) - 1; n > 0 {
					label = doc.say("tl.more", "text", label, "n", n)
				}
			}
			var keys []string
			for _, mk := range sp.marks {
				if mk.Record == nil || len(keys) >= 8 {
					continue
				}
				// Only the latest events of a kind lead to their records.
				if k := refKey(*mk.Record); doc.record(k) != nil {
					keys = append(keys, k)
				}
			}
			var shape []*node
			if g.Kind == view.MarkUE {
				shape = []*node{
					el("rect", A{"class", "mk mk-ue", "x", num(sp.x - 4.5), "y", num(cy - 4.5), "width", "9", "height", "9", "rx", "2"}),
					el("path", A{"class", "uex", "d", fmt.Sprintf("M%s %sl4.4 4.4m0 -4.4l-4.4 4.4", num(sp.x-2.2), num(cy-2.2))}),
				}
			} else {
				shape = []*node{el("circle", A{"class", "mk " + markClass(g.Kind), "cx", num(sp.x), "cy", num(cy), "r", "4"})}
			}
			if len(keys) > 0 {
				s.add(link(keys, label, shape...).add(A{"class", "pt"}))
			} else {
				s.add(el("g", el("title", label), shape))
			}
			// A lane with few marks says the day beside each.
			if len(spots) <= 3 {
				// Month and day; with the year when the period covers
				// more than one.
				text := tl.From.AddDate(0, 0, sp.days[0]).Format("01-02")
				if tl.Days > 366 {
					text = day(tl.From.AddDate(0, 0, sp.days[0]))
				}
				tw := monoWidth(text, size)
				next := labelW + plotW + 4
				if si+1 < len(spots) {
					next = spots[si+1].x - 6
				}
				prev := labelW
				if si > 0 {
					prev = spots[si-1].x + 6 + tw + 8
				}
				switch {
				case sp.x+8+tw <= next:
					s.add(el("text", A{"class", "tick", "x", num(sp.x + 8), "y", num(cy + 3.5)}, text))
				case sp.x-8-tw >= prev:
					s.add(el("text", A{"class", "tick", "x", num(sp.x - 8), "y", num(cy + 3.5), "text-anchor", "end"}, text))
				}
			}
		}
	}
	return s
}

// gradeBar draws the errors of a host split by observation grade as one
// thin bar.
func (doc *Document) gradeBar(a, b, c int64) *node {
	const h = 8.0
	total := float64(a + b + c)
	s := svg("fig obsbar", figW, h, doc.say("fig.grades", "host", doc.Overview.Host.Name))
	x := 0.0
	parts := []struct {
		n   int64
		cls string
	}{{a, "oa"}, {b, "ob"}, {c, "oc"}}
	drawn := 0
	for _, p := range parts {
		if p.n > 0 {
			drawn++
		}
	}
	for _, p := range parts {
		if p.n == 0 || total == 0 {
			continue
		}
		w := float64(p.n) / total * figW
		pad := 1.0
		if drawn == 1 || w < 4 {
			pad = 0
		}
		s.add(el("rect", A{"class", p.cls, "x", num(x + pad), "y", "0", "width", num(max(w-2*pad, 1)), "height", num(h), "rx", "3"}))
		x += w
	}
	return s
}

// sortedCells returns the indexes of the cell references of a map, the
// cells with the most errors first.
func sortedCells(m *view.AddressMap) []int {
	order := make([]int, len(m.Refs))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return m.Refs[order[a]].Errors > m.Refs[order[b]].Errors })
	return order
}
