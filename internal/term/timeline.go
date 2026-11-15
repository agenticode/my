package term

import (
	"io"
	"sort"
	"strings"
	"time"

	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/view"
)

// timeline draws errors per day with the events of the period below.
func (st Style) timeline(p *page, tl *view.Timeline) {
	g := st.g()
	if tl.Days == 0 {
		p.add(" " + st.say("tl.none"))
		for _, n := range tl.Notes {
			p.add(" " + n.Text)
		}
		return
	}
	events := st.say("tl.events")
	lw := max(Width(events), 8)
	for _, s := range tl.Series {
		lw = max(lw, Width(s.Label))
	}
	// The chart takes the left part of the line. The right part holds the
	// peak of each row and the texts of the events.
	room := min(max(st.width()*42/100, 30), 60)
	chart := max(st.width()-1-lw-2-room-1, 10)
	per := (tl.Days + chart - 1) / chart
	cols := (tl.Days + per - 1) / per

	// The time axis: the first day, the first of each month, the last day.
	layout := "01-02"
	if tl.Days > 366 {
		layout = "2006-01"
	}
	axis := []rune(strings.Repeat(" ", cols))
	place := func(col int, text string, force bool) {
		if col < 0 || col+len(text) > len(axis) {
			return
		}
		lo, hi := max(col-1, 0), min(col+len(text)+1, len(axis))
		if !force {
			for _, r := range axis[lo:hi] {
				if r != ' ' {
					return
				}
			}
		}
		copy(axis[col:], []rune(text))
	}
	first, last := tl.From.Format("01-02"), tl.To.Format("01-02")
	if tl.Days > 366 {
		first, last = tl.From.Format("2006-01-02"), tl.To.Format("2006-01-02")
	}
	if len(first)+len(last)+1 <= cols {
		place(0, first, true)
		place(cols-len(last), last, true)
	} else if len(last) <= cols {
		place(cols-len(last), last, true)
	}
	for d := tl.From.AddDate(0, 0, 1); !d.After(tl.To); d = d.AddDate(0, 0, 1) {
		if d.Day() != 1 || (tl.Days > 366 && d.Month() != time.January && d.Month() != time.July) {
			continue
		}
		place(int(d.Sub(tl.From).Hours()/24)/per, d.Format(layout), false)
	}
	p.add(" " + strings.Repeat(" ", lw) + "  " + st.dim(string(axis)))

	for _, s := range tl.Series {
		bins := make([]float64, cols)
		for c := range bins {
			var sum int64
			n := 0
			for d := c * per; d < min((c+1)*per, tl.Days); d++ {
				sum += s.CE[d]
				n++
			}
			if n > 0 {
				bins[c] = float64(sum) / float64(n)
			}
		}
		line := " " + padRight(s.Label, lw) + "  " + st.spark(bins)
		if s.Peak > 0 {
			line += "  " + st.dim(st.say("tl.peak", "n", s.Peak))
		}
		p.add(line)
	}
	if len(tl.Series) == 0 {
		p.add(" " + padRight("CE", lw) + "  " + st.spark(make([]float64, cols)))
	}

	// Markers: one letter per event day, moved aside where two fall on one
	// column.
	if len(tl.Markers) > 0 {
		// A letter keeps its place in time: letters that want the same
		// column line up in the order of their days.
		type mark struct{ day, group, col int }
		var marks []mark
		for gi, grp := range tl.Markers {
			last := -1
			for _, d := range grp.Days {
				if c := min(d/per, cols-1); c != last {
					marks = append(marks, mark{day: d, group: gi, col: c})
					last = c
				}
			}
		}
		sort.SliceStable(marks, func(i, j int) bool { return marks[i].day < marks[j].day })
		if len(marks) > cols {
			marks = marks[len(marks)-cols:] // more letters than columns: the latest stay
		}
		for i := 1; i < len(marks); i++ {
			marks[i].col = max(marks[i].col, marks[i-1].col+1)
		}
		for i := len(marks) - 1; i >= 0; i-- {
			limit := cols - 1
			if i+1 < len(marks) {
				limit = marks[i+1].col - 1
			}
			marks[i].col = min(marks[i].col, limit)
		}
		taken := map[int]int{} // column -> group
		placed := make([][]int, len(tl.Markers))
		for _, mk := range marks {
			taken[mk.col] = mk.group
			placed[mk.group] = append(placed[mk.group], mk.col)
		}
		row := []rune(strings.Repeat(" ", cols))
		maxCol := 0
		for c, gi := range taken {
			row[c] = []rune(tl.Markers[gi].Letter)[0]
			maxCol = max(maxCol, c)
		}
		p.add(" " + st.dim(padRight(events, lw)) + "  " + strings.TrimRight(string(row), " "))

		// The texts hang from the letters. The group that ends furthest
		// right comes first; reboots come last, as the line that runs
		// under everything.
		order := make([]int, 0, len(tl.Markers))
		for gi := range tl.Markers {
			if len(placed[gi]) > 0 {
				order = append(order, gi)
			}
		}
		sort.SliceStable(order, func(i, j int) bool {
			a, b := order[i], order[j]
			if x, y := tl.Markers[a].Kind == view.MarkBoot, tl.Markers[b].Kind == view.MarkBoot; x != y {
				return y
			}
			return placed[a][len(placed[a])-1] > placed[b][len(placed[b])-1]
		})
		textRoom := max(st.width()-1-lw-2-maxCol-3, 20)
		for i, gi := range order {
			rest := map[int]bool{}
			for _, gj := range order[i+1:] {
				for _, c := range placed[gj] {
					rest[c] = true
				}
			}
			own := map[int]bool{}
			for _, c := range placed[gi] {
				own[c] = true
			}
			left := placed[gi][0]
			var b, cont strings.Builder
			for x := 0; x <= maxCol; x++ {
				switch {
				case x < left && rest[x]:
					b.WriteString(g.vert)
				case x < left:
					b.WriteString(" ")
				case own[x] && x == left:
					b.WriteString(g.corner)
				case own[x]:
					b.WriteString(g.joint)
				case rest[x]:
					b.WriteString(g.cross)
				default:
					b.WriteString(g.horiz)
				}
				if rest[x] {
					cont.WriteString(g.vert)
				} else {
					cont.WriteString(" ")
				}
			}
			for k, text := range wrap(tl.Markers[gi].Text.Text, textRoom) {
				lead := b.String()
				if k > 0 {
					lead = cont.String()
				}
				p.add(" " + strings.Repeat(" ", lw) + "  " + st.dim(lead) + " " + text)
			}
		}
	}

	p.blank()
	for _, n := range tl.Notes {
		for i, l := range wrap(n.Text, st.width()-3) {
			if i == 0 {
				p.add(" " + l)
			} else {
				p.add("   " + l)
			}
		}
	}
}

// Timeline draws the timeline of a host or of one DIMM.
func Timeline(w io.Writer, tl *view.Timeline, st Style) error {
	p := &page{st: st}
	head := []string{st.say("ov.host", "name", st.bold(tl.Host))}
	add := func(s string) { head = append(head, s) }
	if tl.Slot != "" {
		add(tl.Slot)
		if tl.DIMM != nil && tl.DIMM.Serial != "" {
			add("S/N " + tl.DIMM.Serial)
		}
	}
	if tl.Days > 0 {
		add(st.span(tl.From.Format("2006-01-02"), tl.To.Format("2006-01-02")))
		add(st.say("tl.days", "n", i18n.Thousands(int64(tl.Days))))
	}
	for _, line := range flow(head, "  ", st.width()) {
		p.add(line)
	}
	p.blank()
	st.timeline(p, tl)
	return p.writeTo(w)
}
