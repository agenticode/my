package term

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/view"
)

// MapCells returns the number of cells per line of an address map that
// fits the style: the largest power of two that leaves room for the row
// labels, at most 128.
func (st Style) MapCells() int {
	avail := st.width() - 1 - mapLabelWidth - 2
	cells := 16
	for cells*2 <= avail && cells < 128 {
		cells *= 2
	}
	return cells
}

// mapLabelWidth is the width of the row labels of an address map: a row
// address of up to five hexadecimal digits.
const mapLabelWidth = 8

// rowLabel writes a row address.
// ExamMap is how the examination of a DIMM draws its address map: in
// short with one quiet row around the rows with errors and at most eight
// lines of binned rows, with Detail as "my map" draws it.
func (st Style) ExamMap() view.MapOptions {
	o := view.MapOptions{Cells: st.MapCells(), MaxLines: 16}
	if !st.Detail {
		o.Margin, o.Bins = 1, 8
	}
	return o
}

func rowLabel(row int) string { return fmt.Sprintf("%#x", row) }

// addressMap draws the heat map of one bank.
//
// In brief, as part of the examination of a DIMM, it leaves out what the
// card above already says: the faults of the bank and the boots it had
// errors in.
func (st Style) addressMap(p *page, m *view.AddressMap, brief bool) {
	g := st.g()
	if m.Bank == nil {
		p.add(" " + st.say("map.none"))
		if m.Unlocated > 0 {
			p.add(" " + st.say("map.unlocated", "n", m.Unlocated))
		}
		return
	}

	cellNote := st.say("map.cell", "n", m.ColumnsPerCell)
	if m.Mode == view.MapBins {
		cellNote = st.say("map.cell_bins", "n", m.ColumnsPerCell, "rows", m.RowsPerLine)
	}
	head := flow([]string{st.bold(m.Bank.Label), st.say("map.axes"), cellNote}, "  ", st.width()-1)

	// The scale: what each shade the grid shows stands for.
	used := map[int]bool{}
	anyUE := false
	for _, ln := range m.Lines {
		if ln.Gap {
			continue
		}
		ue := map[int]bool{}
		for _, i := range ln.UE {
			ue[i] = true
		}
		anyUE = anyUE || len(ln.UE) > 0
		for i, n := range ln.Cells {
			if !ue[i] {
				used[heatLevel(n, m.Scale)] = true
			}
		}
	}
	var legend []string
	if used[0] {
		legend = append(legend, st.paint(heatColor(0), g.shade[0])+" 0")
	}
	var sentence []string
	if m.ByBoots {
		legend = nil
		sentence = wrap(st.say("map.by_boots", "of", m.Window, "g0", g.shade[0], "ue", g.ue), st.width()-1)
	}
	for i, lo := range m.Scale {
		text := fmt.Sprintf("%d+", lo)
		if i+1 < len(m.Scale) {
			if hi := m.Scale[i+1] - 1; hi > lo {
				text = fmt.Sprintf("%d-%d", lo, hi)
			} else {
				text = fmt.Sprint(lo)
			}
		}
		if m.ByBoots {
			break
		}
		if used[heatLevel(lo, m.Scale)] {
			legend = append(legend, st.paint(heatColor(i+1), g.shade[min(i+1, 4)])+" "+text)
		}
	}
	if !m.ByBoots && anyUE {
		legend = append(legend, st.paint(cBoldRed, g.ue)+" "+st.say("map.ue"))
	}
	// The scale stands beside the head when there is room.
	keys := flow(legend, "   ", st.width()-1)
	if n := len(head); n > 0 && len(keys) == 1 && Width(head[n-1])+4+Width(keys[0]) <= st.width()-1 {
		head[n-1] += "    " + keys[0]
		keys = nil
	}
	for _, line := range append(append(head, sentence...), keys...) {
		p.add(" " + line)
	}
	if !brief {
		p.blank()
	}

	if len(m.Lines) == 0 {
		p.add(" " + st.say("map.no_rows"))
	} else {
		cells := 0
		for _, ln := range m.Lines {
			cells = max(cells, len(ln.Cells))
		}
		// The column axis: its first and its last address above the grid.
		lo, hi := "0x000", fmt.Sprintf("%#x", m.Columns-1)
		axis := lo + strings.Repeat(" ", max(cells-Width(lo)-Width(hi), 1)) + hi
		head := padRight("row", mapLabelWidth) + "  " + axis
		if 1+Width(head)+10 <= st.width() {
			head += "  (column)"
		}
		p.add(" " + st.dim(head))
		noteRoom := st.width() - 1 - mapLabelWidth - 2 - cells - 2
		for _, ln := range m.Lines {
			if ln.Gap {
				p.add(" " + st.dim(padRight(g.gap, mapLabelWidth)+"  "+st.say("map.gap", "n", ln.Rows)))
				continue
			}
			ue := map[int]bool{}
			for _, i := range ln.UE {
				ue[i] = true
			}
			var b strings.Builder
			// Runs of one shade are coloured together to keep the output short.
			run, runColor := "", ""
			flush := func() {
				if run != "" {
					b.WriteString(st.paint(runColor, run))
					run = ""
				}
			}
			for i, n := range ln.Cells {
				glyph, color := g.shade[heatLevel(n, m.Scale)], heatColor(heatLevel(n, m.Scale))
				if ue[i] {
					glyph, color = g.ue, cBoldRed
				}
				if m.ByBoots && i < len(ln.Boots) {
					// The number of boots with an error in the cell.
					switch b, u := ln.Boots[i], ln.UEBoots[i]; {
					case u >= 2:
						glyph, color = g.ue, cBoldRed
					case u == 1:
						glyph, color = strings.ToLower(g.ue), cRed
					case b == 0:
						glyph, color = g.shade[0], heatColor(0)
					case b > 9:
						glyph, color = "+", heatColor(3)
					default:
						glyph, color = strconv.Itoa(b), heatColor(min(b, 3))
					}
				}
				if color != runColor {
					flush()
					runColor = color
				}
				run += glyph
			}
			flush()
			line := " " + padRight(rowLabel(ln.Row), mapLabelWidth) + "  " + b.String()
			if ln.Note != nil {
				note := g.note + " " + ln.Note.Text
				if Width(note) <= noteRoom {
					line += "  " + note
				}
			}
			p.add(line)
		}
		for _, mk := range m.Marks {
			p.add(" " + strings.Repeat(" ", mapLabelWidth+2+mk.Cell) + g.caret + " " + fmt.Sprintf("col %#x", mk.Column) + ": " + mk.Note.Text)
		}
	}

	if !brief {
		p.blank()
	}
	for _, f := range m.Faults {
		if brief {
			break
		}
		for i, l := range wrap(f.Text.Text, st.width()-3) {
			if i == 0 {
				p.add(" " + l)
			} else {
				p.add("   " + l)
			}
		}
	}
	if m.Stray > 0 {
		p.add(" " + st.say("map.stray", "n", m.Stray))
	}
	if m.BankCount > 1 && !brief {
		// In brief the topology bars above name the other branches.
		var others []string
		for _, b := range m.Banks {
			if b.Label != m.Bank.Label {
				others = append(others, fmt.Sprintf("%s (%s)", b.Label, i18n.Thousands(b.Errors)))
			}
		}
		text := st.say("map.others", "list", strings.Join(others, ", "))
		if rest := m.BankCount - len(m.Banks); rest > 0 {
			text += " " + st.say("map.more", "n", rest)
		}
		for i, l := range wrap(text, st.width()-3) {
			if i == 0 {
				p.add(" " + l)
			} else {
				p.add("   " + l)
			}
		}
	}
	if m.Unlocated > 0 {
		p.add(" " + st.say("map.unlocated", "n", m.Unlocated))
	}
	if m.Window > 0 && m.Boots >= 2 && !brief {
		p.add(" " + st.say("map.boots", "n", m.Boots, "of", m.Window, "ue", m.UEBoots))
	}
}

// AddressMap draws the address map of one bank of a DIMM.
func AddressMap(w io.Writer, m *view.AddressMap, st Style) error {
	p := &page{st: st}
	head := []string{st.bold(m.Slot)}
	if m.DIMM.Serial != "" {
		head = append(head, "S/N "+m.DIMM.Serial)
	}
	head = append(head, m.DIMM.Kind)
	for _, line := range flow(head, "  ", st.width()) {
		p.add(line)
	}
	p.blank()
	st.addressMap(p, m, false)
	return p.writeTo(w)
}
