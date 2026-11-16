package term

import (
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/view"
)

// Boots draws the boots of a host, DIMMs by boots, or with a DIMM set its
// places by boots. show limits the number of boots drawn (0: as many as
// fit, the latest first to stay).
func Boots(w io.Writer, b *view.Boots, st Style, show int, hints []Hint) error {
	p := &page{st: st}
	head := []string{st.say("ov.host", "name", st.bold(b.Host))}
	if b.System != "" {
		head = append(head, b.System)
	}
	if n := len(b.Boots); n > 0 {
		head = append(head, st.say("boots.count", "n", n),
			st.span(b.Boots[0].StartedAt.UTC().Format("2006-01-02"), b.Boots[n-1].StartedAt.UTC().Format("2006-01-02")))
	}
	if b.DIMM != nil {
		head = []string{st.bold(b.Slot)}
		if b.DIMM.Kind != "" {
			head = append(head, b.DIMM.Kind)
		}
		if b.DIMM.Manufacturer != "" || b.DIMM.PartNumber != "" {
			head = append(head, strings.TrimSpace(b.DIMM.Manufacturer+" "+b.DIMM.PartNumber))
		}
		if b.DIMM.Serial != "" {
			head = append(head, "S/N "+b.DIMM.Serial)
		}
	}
	for _, line := range flow(head, "  ", st.width()) {
		p.add(line)
	}
	if len(b.Boots) == 0 {
		p.add(" " + st.say("boots.none"))
		return p.writeTo(w)
	}
	if b.DIMM == nil {
		st.stopsLines(p, b.Stops)
		p.blank()
		st.bootsGrid(p, b, show)
	} else {
		p.blank()
		if len(b.Rows) == 0 {
			p.add(" " + st.say("boots.dimm_none", "n", len(b.Boots)))
		} else {
			st.placesGrid(p, b, show, view.MaxPlaces)
		}
		if len(b.Rules) > 0 {
			p.blank()
			for _, e := range b.Rules {
				for i, l := range wrap(e.Text.Text, st.width()-3-len(e.Rule)) {
					if i == 0 {
						l = padRight(l, st.width()-3-len(e.Rule)) + " " + e.Rule
					}
					p.add(" " + l)
				}
			}
		}
	}
	if len(hints) > 0 {
		p.blank()
		fields := make([]string, len(hints))
		for i, h := range hints {
			fields[i] = st.bold(h.Command) + ": " + h.Text
		}
		for _, line := range flow(fields, "   ", st.width()-1) {
			p.add(" " + line)
		}
	}
	return p.writeTo(w)
}

// stopsLines writes the stops lines of a host: how many boots of the
// window stopped, and where the uncorrected errors lie.
func (st Style) stopsLines(p *page, s *view.Stops) {
	if s == nil {
		return
	}
	label := st.say("boots.stops")
	lw := Width(label)
	for i, line := range s.Lines {
		lead := strings.Repeat(" ", lw)
		if i == 0 {
			lead = st.paint(cRed, label)
		}
		for k, l := range wrap(line.Text, st.width()-lw-2) {
			if k > 0 {
				lead = strings.Repeat(" ", lw)
			}
			p.add(" " + lead + " " + l)
		}
	}
}

// bootGlyph is the letter of a cell: what a DIMM or a place showed in one
// boot.
func (st Style) bootGlyph(c view.BootCell) string {
	g := st.g()
	switch {
	case c.Stopped:
		return st.paint(cRed, g.ue)
	case c.UE > 0:
		return st.paint(cRed, "U")
	case c.CE >= 100:
		return st.paint(heatColor(3), g.shade[3])
	case c.CE >= 10:
		return st.paint(heatColor(2), g.shade[2])
	case c.CE > 0:
		return st.paint(heatColor(1), g.shade[1])
	case c.Absent:
		return "-"
	case !c.Known:
		return "?"
	}
	return st.dim(g.shade[0])
}

// endWord is how a boot ended, in the grid of a host.
func (st Style) endWord(end string) string {
	switch end {
	case model.BootStop:
		return st.paint(cRed, st.say("boots.end.stop"))
	case model.BootPanic:
		return st.say("boots.end.panic")
	case "running":
		return st.say("boots.end.running")
	}
	return "-"
}

// endLetter is how a boot ended, in the grid of one DIMM.
func (st Style) endLetter(end string) string {
	switch end {
	case model.BootStop:
		// H, halted: S is sparing in the timeline.
		return st.paint(cRed, "H")
	case model.BootPanic:
		return "p"
	case "running":
		return "r"
	}
	return "-"
}

// RanText writes how long a boot ran, in the unit that fits it.
func RanText(seconds int64) string {
	switch {
	case seconds <= 0:
		return "?"
	case seconds >= 48*3600:
		return strconv.FormatInt(seconds/86400, 10) + " d"
	case seconds >= 2*3600:
		return strconv.FormatInt(seconds/3600, 10) + " h"
	case seconds >= 120:
		return strconv.FormatInt(seconds/60, 10) + " min"
	}
	return strconv.FormatInt(seconds, 10) + " s"
}

// shown returns the index of the first boot drawn: the latest boots that
// fit cols columns, or show of them.
func shown(n, cols, show int) int {
	k := min(n, max(cols, 1))
	if show > 0 {
		k = min(k, show)
	}
	return n - k
}

// bootsGrid draws the DIMMs of a host by boots.
func (st Style) bootsGrid(p *page, b *view.Boots, show int) {
	heads := []string{st.say("boots.boot"), st.say("boots.started"), st.say("boots.ran"), st.say("boots.ended")}
	noDIMM := st.say("boots.no_dimm")
	lw := 0
	for _, h := range heads {
		lw = max(lw, Width(h))
	}
	for _, r := range b.Rows {
		if r.Level == view.RowNoDIMM {
			lw = max(lw, Width(noDIMM))
		} else {
			lw = max(lw, Width(r.Slot))
		}
	}
	// A column fits the widest of its texts and one space.
	cw := 6
	for _, bc := range b.Boots {
		cw = max(cw, Width(RanText(bc.RanSeconds)), Width(st.say("boots.end.stop")), Width(st.say("boots.end.panic")),
			Width(st.say("boots.end.running")))
	}
	cw++
	from := shown(len(b.Boots), (st.width()-2-lw-2)/cw, show)
	cols := b.Boots[from:]
	line := func(label string, cells []string) {
		var s strings.Builder
		s.WriteString(" " + padRight(label, lw) + "  ")
		for _, c := range cells {
			s.WriteString(padRight(c, cw))
		}
		p.add(s.String())
	}
	var idx, day, clock, ran, ended []string
	for _, bc := range cols {
		idx = append(idx, strconv.Itoa(bc.Index))
		if bc.StartQuality == "unknown" {
			day, clock = append(day, "?"), append(clock, "")
		} else {
			t := bc.StartedAt.UTC()
			day, clock = append(day, t.Format("01-02")), append(clock, t.Format("15:04"))
		}
		ran = append(ran, RanText(bc.RanSeconds))
		ended = append(ended, st.endWord(bc.End))
	}
	line(heads[0], idx)
	line(heads[1], day)
	line("", clock)
	line(heads[2], ran)
	line(heads[3], ended)
	p.blank()
	for _, r := range b.Rows {
		label := r.Slot
		if r.Level == view.RowNoDIMM {
			label = noDIMM
		}
		var cells []string
		for _, c := range r.Cells[from:] {
			cells = append(cells, st.bootGlyph(c))
		}
		line(label, cells)
	}
	p.blank()
	if from > 0 {
		st.para(p, st.say("boots.left_out", "n", from))
	}
	if b.Quiet > 0 {
		st.para(p, st.say("boots.quiet", "n", b.Quiet))
	}
	st.bootsLegend(p, true)
}

// para writes a sentence wrapped to the width, one space in.
func (st Style) para(p *page, text string) {
	for _, l := range wrap(text, st.width()-2) {
		p.add(" " + l)
	}
}

// bootsLegend explains the letters of a grid.
func (st Style) bootsLegend(p *page, words bool) {
	g := st.g()
	for _, l := range wrap(st.say("boots.legend", "g0", g.shade[0], "g1", g.shade[1], "g2", g.shade[2], "g3", g.shade[3], "ue", g.ue), st.width()-2) {
		p.add(" " + st.dim(l))
	}
	code := "boots.legend_letters"
	if words {
		code = "boots.legend_words"
	}
	for _, l := range wrap(st.say(code), st.width()-2) {
		p.add(" " + st.dim(l))
	}
}

// placesGrid draws the places of one DIMM by boots, at most limit of them.
func (st Style) placesGrid(p *page, b *view.Boots, show, limit int) {
	heads := []string{st.say("boots.boot"), st.say("boots.ended")}
	place := func(r view.BootRow) string {
		switch {
		case r.Level == "others":
			return st.say("boots.others", "n", r.Places)
		case r.Level == "dimm-only" || r.Place == "":
			return st.say("boots.dimm_only")
		}
		return r.Place
	}
	rows := b.Rows
	var others *view.BootRow
	if len(rows) > limit {
		// The rest is summed in one row.
		o := view.BootRow{Level: "others", Cells: make([]view.BootCell, len(b.Boots))}
		for i := range o.Cells {
			o.Cells[i] = view.BootCell{Boot: i + 1, Known: true}
		}
		for _, r := range rows[limit:] {
			o.Places++
			o.CE += r.CE
			o.UE += r.UE
			for k, c := range r.Cells {
				oc := &o.Cells[k]
				oc.CE, oc.UE, oc.Stopped = oc.CE+c.CE, oc.UE+c.UE, oc.Stopped || c.Stopped
			}
		}
		if b.Others != nil {
			o.Places += b.Others.Places
			o.CE += b.Others.CE
			o.UE += b.Others.UE
			for k, c := range b.Others.Cells {
				oc := &o.Cells[k]
				oc.CE, oc.UE, oc.Stopped = oc.CE+c.CE, oc.UE+c.UE, oc.Stopped || c.Stopped
			}
		}
		for _, c := range o.Cells {
			if c.CE+c.UE > 0 {
				o.BootsWithErrors++
			}
		}
		rows, others = rows[:limit], &o
	} else if b.Others != nil {
		others = b.Others
	}
	if others != nil {
		rows = append(append([]view.BootRow{}, rows...), *others)
	}
	// The cells of one row of a bank stand under a line that names the
	// row: "rank0 dev7 bg0 bank3 row 0x1a2f", then "  col 0x238".
	type entry struct {
		head  string
		label string
		row   view.BootRow
	}
	var entries []entry
	heads2 := map[string]int{}
	var order []string
	for _, r := range rows {
		label := place(r)
		head := ""
		if r.Level == "cell" {
			if i := strings.LastIndex(label, " col "); i > 0 {
				head, label = label[:i], "  "+label[i+1:]
			}
		}
		if _, ok := heads2[head]; !ok {
			heads2[head] = len(order)
			order = append(order, head)
		}
		entries = append(entries, entry{head, label, r})
	}
	// Groups keep the order of their first row; rows without a group
	// (addresses, the DIMM, the others) keep theirs at the end.
	sort.SliceStable(entries, func(a, b int) bool {
		x, y := entries[a].head, entries[b].head
		if (x == "") != (y == "") {
			return y == ""
		}
		return heads2[x] < heads2[y]
	})
	lw := 0
	for _, h := range heads {
		lw = max(lw, Width(h))
	}
	for _, e := range entries {
		lw = max(lw, Width(e.label))
	}
	lw = min(lw, 46)
	n := len(b.Boots)
	ofText := func(k int) string { return st.say("boots.of", "n", k, "of", n) }
	bw := max(Width(st.say("boots.boots")), Width(ofText(n)))
	right := bw + 2 + 16
	cw := max(len(strconv.Itoa(b.Boots[n-1].Index))+1, 3)
	from := shown(n, (st.width()-2-lw-2-right)/cw, show)
	line := func(label string, cells []string, tail string) {
		var s strings.Builder
		s.WriteString(" " + padRight(label, lw) + "  ")
		for _, c := range cells {
			s.WriteString(padRight(c, cw))
		}
		if tail != "" {
			s.WriteString(" " + tail)
		}
		p.add(s.String())
	}
	var idx, ended []string
	for _, bc := range b.Boots[from:] {
		idx = append(idx, strconv.Itoa(bc.Index))
		ended = append(ended, st.endLetter(bc.End))
	}
	line(heads[0], idx, padRight(st.say("boots.boots"), bw)+"  "+st.say("boots.errors_head"))
	line(heads[1], ended, "")
	head := ""
	for _, e := range entries {
		if e.head != "" && e.head != head {
			p.add(" " + e.head)
		}
		head = e.head
		r := e.row
		label := truncate(e.label, lw, st.g().ellipsis)
		var cells []string
		for _, c := range r.Cells[from:] {
			cells = append(cells, st.bootGlyph(c))
		}
		var counts []string
		if r.CE > 0 {
			counts = append(counts, "CE "+i18n.Thousands(r.CE))
		}
		if r.UE > 0 {
			counts = append(counts, "UE "+i18n.Thousands(r.UE))
		}
		line(label, cells, padRight(ofText(r.BootsWithErrors), bw)+"  "+strings.Join(counts, ", "))
	}
	p.blank()
	if from > 0 {
		st.para(p, st.say("boots.left_out", "n", from))
	}
	st.bootsLegend(p, false)
}
