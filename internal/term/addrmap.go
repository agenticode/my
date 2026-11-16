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
func rowLabel(row int) string { return fmt.Sprintf("%#x", row) }

// addressMap draws the heat map of one bank.
func (st Style) addressMap(p *page, m *view.AddressMap) {
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
	for _, line := range flow([]string{st.bold(m.Bank.Label), st.say("map.axes"), cellNote}, "  ", st.width()-1) {
		p.add(" " + line)
	}

	// The scale: what each shade stands for.
	legend := []string{st.paint(heatColor(0), g.shade[0]) + " 0"}
	if m.ByBoots {
		legend = nil
		for _, l := range wrap(st.say("map.by_boots", "of", m.Window, "g0", g.shade[0], "ue", g.ue), st.width()-1) {
			p.add(" " + l)
		}
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
		legend = append(legend, st.paint(heatColor(i+1), g.shade[min(i+1, 4)])+" "+text)
	}
	if !m.ByBoots {
		legend = append(legend, st.paint(cBoldRed, g.ue)+" "+st.say("map.ue"))
	}
	for _, line := range flow(legend, "   ", st.width()-1) {
		p.add(" " + line)
	}
	p.blank()

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

	p.blank()
	for _, f := range m.Faults {
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
	if m.BankCount > 1 {
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
	if m.Window > 0 && m.Boots >= 2 {
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
	st.addressMap(p, m)
	return p.writeTo(w)
}
