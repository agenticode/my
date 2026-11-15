package term

import (
	"fmt"
	"io"
	"strings"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/view"
)

// Hint is a command the reader can run next, and what it shows.
type Hint struct {
	Command string
	Text    string
}

// page collects the lines of one screen.
type page struct {
	st    Style
	lines []string
}

func (p *page) add(line string) { p.lines = append(p.lines, strings.TrimRight(line, " ")) }
func (p *page) addf(format string, a ...any) {
	p.add(fmt.Sprintf(format, a...))
}
func (p *page) blank() {
	if n := len(p.lines); n > 0 && p.lines[n-1] != "" {
		p.lines = append(p.lines, "")
	}
}

func (p *page) writeTo(w io.Writer) error {
	for len(p.lines) > 0 && p.lines[len(p.lines)-1] == "" {
		p.lines = p.lines[:len(p.lines)-1]
	}
	_, err := io.WriteString(w, strings.Join(p.lines, "\n")+"\n")
	return err
}

// labelled writes text under a label: the label in a column of labelW,
// the text wrapped beside it, further lines indented to the text.
func (p *page) labelled(label string, labelW int, text string, color string) {
	st := p.st
	avail := st.width() - 1 - labelW - 2
	for i, line := range wrap(text, avail) {
		head := strings.Repeat(" ", labelW)
		if i == 0 {
			head = st.dim(padRight(label, labelW))
		}
		p.add(" " + head + "  " + st.paint(color, line))
	}
}

// span writes a period of days in the words of the language.
func (st Style) span(from, to string) string {
	return st.say("span", "from", from, "to", to)
}

// headerFields returns the fields of the first line of a host screen.
func (st Style) headerFields(h *view.Header) []string {
	fields := []string{st.say("ov.host", "name", st.bold(h.Name))}
	if h.System != "" {
		fields = append(fields, h.System)
	}
	if !h.To.IsZero() {
		fields = append(fields, st.span(h.From.UTC().Format("2006-01-02"), h.To.UTC().Format("2006-01-02")))
	}
	if n := h.CE + h.UE; n > 0 {
		fields = append(fields, st.say("ov.errors", "n", n, "ce", h.CE, "ue", h.UE))
		var grades []string
		for _, g := range []struct {
			name string
			n    int64
		}{{"A", h.GradeA}, {"B", h.GradeB}, {"C", h.GradeC}} {
			if g.n == 0 {
				continue
			}
			if g.n == n {
				grades = []string{g.name}
				break
			}
			grades = append(grades, g.name+" "+i18n.Thousands(g.n))
		}
		if len(grades) > 0 {
			fields = append(fields, st.say("ov.obs", "grades", strings.Join(grades, ", ")))
		}
	} else {
		fields = append(fields, st.say("ov.no_errors"))
	}
	return fields
}

// Overview draws the first screen of a host: the slot map and the ranking.
func Overview(w io.Writer, ov *view.Overview, st Style, hints []Hint) error {
	p := &page{st: st}
	for _, line := range flow(st.headerFields(&ov.Host), "  ", st.width()) {
		p.add(line)
	}
	p.add(st.say("ov.finding", "grade", st.paint(gradeColor(string(ov.Host.Grade)), ov.Host.GradeText)))
	p.blank()

	st.slotMap(p, &ov.SlotMap)
	p.blank()
	st.ranking(p, ov)

	// What else the reader has to know about the host.
	p.blank()
	labels := []string{st.say("ov.sources"), st.say("card.missing")}
	lw := 0
	for _, l := range labels {
		lw = max(lw, Width(l))
	}
	if n := ov.Quiet; n > 0 {
		p.add(" " + st.say("ov.quiet", "n", n))
	}
	for _, g := range ov.GPUs {
		line := fmt.Sprintf(" GPU %s  %s  %s", g.GPU, st.paint(gradeColor(string(g.Grade)), g.GradeText), strings.Join(g.Rules, " "))
		p.add(line)
		for _, e := range g.Evidence {
			for _, l := range wrap(e.Text, st.width()-5) {
				p.add("     " + l)
			}
		}
	}
	if ov.Unattributed > 0 {
		p.add(" " + st.say("ov.loose", "n", ov.Unattributed))
	}
	var read []string
	for _, s := range ov.Sources {
		switch s.Status {
		case model.SourceOK, model.SourcePartial:
			read = append(read, st.say("ov.source", "kind", s.Kind, "n", s.Records))
		case model.SourceEmpty:
			read = append(read, st.say("ov.source_empty", "kind", s.Kind))
		}
	}
	if len(read) > 0 {
		p.labelled(labels[0], lw, strings.Join(read, ", "), "")
	}
	label := labels[1]
	for _, m := range ov.Missing {
		p.labelled(label, lw, m.Text, "")
		label = ""
	}
	if ov.Root != nil {
		p.labelled(label, lw, ov.Root.Text, "")
	}

	if len(hints) > 0 {
		p.blank()
		fields := make([]string, len(hints))
		for i, h := range hints {
			fields[i] = st.bold(h.Command) + ": " + h.Text
		}
		// Colour codes take no columns: measure the plain text.
		plain := make([]string, len(hints))
		for i, h := range hints {
			plain[i] = h.Command + ": " + h.Text
		}
		for _, idx := range flowIndex(plain, "   ", st.width()-1) {
			p.add(" " + strings.Join(fields[idx[0]:idx[1]], "   "))
		}
	}
	return p.writeTo(w)
}

// flowIndex is flow for fields that are drawn with colour: it returns the
// index range of the fields of each line.
func flowIndex(plain []string, gap string, w int) [][2]int {
	var out [][2]int
	start, n := 0, 0
	gw := Width(gap)
	for i, f := range plain {
		fw := Width(f)
		switch {
		case i == start:
			n = fw
		case n+gw+fw > w:
			out = append(out, [2]int{start, i})
			start, n = i, fw
		default:
			n += gw + fw
		}
	}
	if start < len(plain) {
		out = append(out, [2]int{start, len(plain)})
	}
	return out
}

// slotMap draws the slots of the board: one line per socket, one column
// per channel, and a band of lines per position on the channel.
func (st Style) slotMap(p *page, sm *view.SlotMap) {
	if len(sm.Rows) == 0 {
		p.add(" " + st.say("ov.no_slots"))
		return
	}
	type bandRow struct {
		label string
		cells []*view.SlotCell
	}
	positions := 0
	for _, r := range sm.Rows {
		for _, ch := range r.Channels {
			positions = max(positions, len(ch.Slots))
		}
	}
	rowLabel := func(r *view.SlotRow) string {
		switch {
		case r.Label != "":
			return r.Label
		case r.Socket.Valid:
			return st.say("ov.socket", "n", r.Socket.V)
		}
		return ""
	}
	var bands [][]bandRow
	hidden := 0
	emptyDrawn := false
	for k := range positions {
		var band []bandRow
		occupied, slots := false, 0
		for i := range sm.Rows {
			r := &sm.Rows[i]
			br := bandRow{label: rowLabel(r)}
			any := false
			for c := range r.Channels {
				var cell *view.SlotCell
				if k < len(r.Channels[c].Slots) {
					cell = &r.Channels[c].Slots[k]
					any = true
					slots++
					if cell.State != view.SlotEmpty && cell.State != view.SlotUnknown {
						occupied = true
					}
				}
				br.cells = append(br.cells, cell)
			}
			if any {
				band = append(band, br)
			}
		}
		// A band without a single module says nothing about any DIMM.
		if !occupied {
			hidden += slots
			continue
		}
		for _, br := range band {
			for _, c := range br.cells {
				if c != nil && (c.State == view.SlotEmpty || c.State == view.SlotUnknown) {
					emptyDrawn = true
				}
			}
		}
		bands = append(bands, band)
	}

	lw, cw := 0, 2
	for _, band := range bands {
		for _, br := range band {
			lw = max(lw, Width(br.label))
			for _, c := range br.cells {
				if c != nil {
					cw = max(cw, Width(c.Label))
				}
			}
		}
	}
	const gap = 3
	perLine := max((st.width()-1-lw-gap)/(cw+gap), 1)
	for _, band := range bands {
		cols := 0
		for _, br := range band {
			cols = max(cols, len(br.cells))
		}
		for from := 0; from < cols; from += perLine {
			to := min(from+perLine, cols)
			prev := ""
			for _, br := range band {
				var head, body strings.Builder
				for c := from; c < to && c < len(br.cells); c++ {
					cell := br.cells[c]
					label, glyph := "", "  "
					if cell != nil {
						label = cell.Label
						glyph = st.paint(gradeColor(cell.State), st.gradeGlyph(cell.State))
					}
					head.WriteString(strings.Repeat(" ", gap) + padRight(label, cw))
					body.WriteString(strings.Repeat(" ", gap) + glyph + strings.Repeat(" ", cw-2))
				}
				// Rows that name their columns alike share one head line.
				if h := head.String(); h != prev {
					p.add(" " + strings.Repeat(" ", lw) + st.dim(h))
					prev = h
				}
				p.add(" " + padRight(br.label, lw) + body.String())
			}
		}
	}

	p.blank()
	legend := []string{}
	for _, g := range []model.VerdictGrade{model.VerdictReplaceNow, model.VerdictConsiderReplacement, model.VerdictObserve, model.VerdictOK, model.VerdictHoldReplacement} {
		legend = append(legend, st.paint(gradeColor(string(g)), st.gradeGlyph(string(g)))+" "+engine.GradeText(st.lang(), g))
	}
	if emptyDrawn {
		legend = append(legend, st.paint(gradeColor(view.SlotEmpty), st.gradeGlyph(view.SlotEmpty))+" "+st.say("ov.empty"))
	}
	for _, line := range flow(legend, "   ", st.width()-1) {
		p.add(" " + line)
	}
	note := st.say("ov.slots", "slots", sm.Slots, "dimms", sm.Populated)
	if hidden > 0 {
		note += " " + st.say("ov.hidden", "n", hidden)
	}
	p.add(" " + st.dim(note))
}

// trendArrow returns the glyph of a trend direction.
func (st Style) trendArrow(direction string) string {
	g := st.g()
	switch direction {
	case engine.TrendRising, engine.TrendNew:
		return g.up
	case engine.TrendSteady:
		return g.flat
	case engine.TrendFalling, engine.TrendQuiet:
		return g.down
	}
	return g.unknown
}

// cell is one cell of a table: its text and the colour it is drawn in.
type cell struct {
	text  string
	color string
}

// column is one column of a table.
type column struct {
	head  string
	right bool
	cells []cell
}

func (c *column) width() int {
	w := Width(c.head)
	for _, x := range c.cells {
		w = max(w, Width(x.text))
	}
	return w
}

// table draws columns side by side, dropping columns from the end of
// optional until the table fits the width.
func (st Style) table(p *page, cols []*column, optional []*column) {
	total := func() int {
		n := 0
		for _, c := range cols {
			n += c.width() + 2
		}
		return n
	}
	for total() > st.width() && len(optional) > 0 {
		drop := optional[0]
		optional = optional[1:]
		for i, c := range cols {
			if c == drop {
				cols = append(cols[:i], cols[i+1:]...)
				break
			}
		}
	}
	if len(cols) == 0 {
		return
	}
	var head strings.Builder
	for _, c := range cols {
		w := c.width()
		if c.right {
			head.WriteString(" " + padLeft(c.head, w) + " ")
		} else {
			head.WriteString(" " + padRight(c.head, w) + " ")
		}
	}
	p.add(st.dim(strings.TrimRight(head.String(), " ")))
	for r := range cols[0].cells {
		var line strings.Builder
		for _, c := range cols {
			w := c.width()
			x := c.cells[r]
			text := padRight(x.text, w)
			if c.right {
				text = padLeft(x.text, w)
			}
			// Pad first, colour afterwards: escape codes take no columns.
			if x.color != "" {
				trimmed := strings.TrimRight(text, " ")
				text = st.paint(x.color, trimmed) + text[len(trimmed):]
			}
			line.WriteString(" " + text + " ")
		}
		p.add(line.String())
	}
}

// ranking draws the DIMMs with errors or a finding, the worst first.
func (st Style) ranking(p *page, ov *view.Overview) {
	if len(ov.Ranking) == 0 {
		p.add(" " + st.say("ov.none"))
		return
	}
	n := &column{head: "#", right: true}
	slot := &column{head: st.say("col.slot")}
	serial := &column{head: st.say("col.serial")}
	ce := &column{head: "CE", right: true}
	ue := &column{head: "UE", right: true}
	mode := &column{head: st.say("col.mode")}
	trend := &column{head: st.say("col.trend", "days", view.TrendDays)}
	finding := &column{head: st.say("col.finding")}
	rules := &column{head: st.say("col.rules")}
	for _, r := range ov.Ranking {
		n.cells = append(n.cells, cell{text: fmt.Sprint(r.N)})
		slot.cells = append(slot.cells, cell{text: r.Slot})
		s := view.ShortSerial(r.DIMM.Serial)
		if s == "" {
			s = "-"
		}
		serial.cells = append(serial.cells, cell{text: s})
		ce.cells = append(ce.cells, cell{text: i18n.Thousands(r.CE)})
		ueColor := ""
		if r.UE > 0 {
			ueColor = cBoldRed
		}
		ue.cells = append(ue.cells, cell{text: i18n.Thousands(r.UE), color: ueColor})
		m := r.ModeText
		switch {
		case m == "":
			m = "-"
		case r.Estimated:
			m += " (" + engine.ModeText(st.lang(), "estimated") + ")"
		}
		mode.cells = append(mode.cells, cell{text: m})
		values := make([]float64, len(r.Trend))
		for i, v := range r.Trend {
			values[i] = float64(v)
		}
		trend.cells = append(trend.cells, cell{text: st.spark(values) + " " + st.trendArrow(r.Direction)})
		g := r.GradeText
		if !r.Present {
			g += " (" + st.say("ov.gone") + ")"
		}
		finding.cells = append(finding.cells, cell{text: g, color: gradeColor(string(r.Grade))})
		rules.cells = append(rules.cells, cell{text: strings.Join(r.Rules, " ")})
	}
	st.table(p, []*column{n, slot, serial, ce, ue, mode, trend, finding, rules}, []*column{rules, trend, serial, mode})
}

// Hosts draws the hosts of a ledger that holds several.
func Hosts(w io.Writer, hosts []view.HostRow, st Style) error {
	p := &page{st: st}
	p.add(st.say("hosts.title", "n", len(hosts)))
	p.blank()
	name := &column{head: st.say("col.host")}
	finding := &column{head: st.say("col.finding")}
	dimms := &column{head: "DIMM", right: true}
	found := &column{head: st.say("col.findings"), right: true}
	ce := &column{head: "CE", right: true}
	ue := &column{head: "UE", right: true}
	worst := &column{head: st.say("col.worst")}
	until := &column{head: st.say("col.until")}
	for _, h := range hosts {
		name.cells = append(name.cells, cell{text: h.Name})
		finding.cells = append(finding.cells, cell{text: h.GradeText, color: gradeColor(string(h.Grade))})
		dimms.cells = append(dimms.cells, cell{text: fmt.Sprint(h.DIMMs)})
		found.cells = append(found.cells, cell{text: fmt.Sprint(h.Findings)})
		ce.cells = append(ce.cells, cell{text: i18n.Thousands(h.CE)})
		ue.cells = append(ue.cells, cell{text: i18n.Thousands(h.UE)})
		wt := h.Worst
		if wt == "" {
			wt = "-"
		}
		worst.cells = append(worst.cells, cell{text: wt})
		ut := "-"
		if !h.To.IsZero() {
			ut = h.To.UTC().Format("2006-01-02")
		}
		until.cells = append(until.cells, cell{text: ut})
	}
	st.table(p, []*column{name, finding, dimms, found, ce, ue, worst, until}, []*column{until, worst})
	return p.writeTo(w)
}
