package term

import (
	"io"
	"strings"
	"time"

	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/view"
)

// restartsShown is how many restarts between two cases are named.
const restartsShown = 6

// stamp writes a time to the minute.
func stamp(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") }

// errorCount writes a number of errors the way the ranking does: corrected
// and uncorrected apart, and a plain 0 for none.
func errorCount(e view.ErrorCount) string {
	var parts []string
	if e.CE > 0 {
		parts = append(parts, i18n.Thousands(e.CE)+" CE")
	}
	if e.UE > 0 {
		parts = append(parts, i18n.Thousands(e.UE)+" UE")
	}
	if len(parts) == 0 {
		return "0"
	}
	return strings.Join(parts, " ")
}

// spanHead writes a length of time for a column head: 43D, 36H, 50M.
func (st Style) spanHead(d time.Duration) string {
	unit, n := view.SpanOf(d)
	return st.say("df.span."+unit, "n", n)
}

// changeText writes one change without its kind.
func (st Style) changeText(c *view.Change) string {
	arrow := " " + st.g().arrow + " "
	switch c.Kind {
	case view.ChangeMoved:
		return st.say("df.moved", "dimm", c.DIMM, "from", c.From, "to", c.To, "arrow", st.g().arrow)
	case view.ChangeRemoved:
		return st.say("df.was_in", "dimm", c.DIMM, "slot", c.From)
	case view.ChangeAdded:
		return st.say("df.now_in", "dimm", c.DIMM, "slot", c.To)
	case view.ChangeAction:
		code := "df.action"
		if c.Slot == "" {
			code += "_host"
		}
		if c.N > 1 || !c.First.Equal(c.Last) {
			return st.say(code+"_n", "kind", c.ActionText, "slot", c.Slot, "n", c.N, "from", stamp(c.First), "to", stamp(c.Last))
		}
		return st.say(code, "kind", c.ActionText, "slot", c.Slot, "when", stamp(c.First))
	}
	return c.From + arrow + c.To
}

// Comparison draws two cases of one host side by side: what was changed
// between them, the errors of every DIMM before and after, and what the
// counts show.
func Comparison(w io.Writer, c *view.Comparison, st Style) error {
	p := &page{st: st}
	arrow := st.g().arrow

	// The two cases, and the finding for the host in each.
	side := func(s *view.CaseState) string {
		if s.File == "" {
			return stamp(s.AsOf)
		}
		return s.File + " " + stamp(s.AsOf)
	}
	finding := st.paint(gradeColor(string(c.After.Grade)), c.After.GradeText)
	if c.Before.Grade != c.After.Grade {
		finding = st.paint(gradeColor(string(c.Before.Grade)), c.Before.GradeText) + " " + arrow + " " + finding
	}
	head := []string{
		st.say("ov.host", "name", st.bold(c.Host)),
		side(&c.Before),
		arrow + " " + side(&c.After),
		st.say("df.apart", "span", st.apart(c)),
		st.say("ov.finding", "grade", finding),
	}
	for _, line := range flow(head, "  ", st.width()) {
		p.add(line)
	}
	for _, line := range st.Wrap(c.Period.Text, 0) {
		p.add(st.dim(line))
	}
	p.blank()

	labels := []string{st.say("df.changes"), st.say("df.reading"), st.say("df.notes")}
	lw := 0
	for _, l := range labels {
		lw = max(lw, Width(l))
	}

	// What differs.
	type entry struct{ kind, text string }
	var entries []entry
	for i := range c.Changes {
		ch := &c.Changes[i]
		entries = append(entries, entry{st.say("df.kind." + ch.Kind), st.changeText(ch)})
	}
	if n := len(c.Restarts); n > 0 {
		var times []string
		for i, r := range c.Restarts {
			if i == restartsShown {
				break
			}
			times = append(times, stamp(r.At))
		}
		text := strings.Join(times, ", ")
		if n > restartsShown {
			text = st.say("df.more_restarts", "times", text, "n", n-restartsShown)
		}
		entries = append(entries, entry{st.say("df.kind.restart"), st.say("df.restarts", "n", n, "times", text)})
	}
	label := st.say("df.changes")
	if len(entries) == 0 {
		p.labelled(label, lw, st.say("df.no_changes"), "")
	}
	// The kind of a change in a column of its own, the text beside it.
	kw := 0
	for _, e := range entries {
		kw = max(kw, Width(e.kind))
	}
	for _, e := range entries {
		for i, line := range wrap(e.text, st.width()-1-lw-2-kw-2) {
			head, kind := strings.Repeat(" ", lw), strings.Repeat(" ", kw)
			if i == 0 {
				head, kind = st.dim(padRight(label, lw)), padRight(e.kind, kw)
			}
			p.add(" " + head + "  " + kind + "  " + line)
		}
		label = ""
	}
	p.blank()

	// One line per DIMM.
	if len(c.DIMMs) > 0 {
		before, after := c.Spans()
		dimm := &column{head: st.say("col.dimm")}
		slot := &column{head: st.say("col.slot")}
		all := &column{head: st.say("col.df_before"), right: true}
		recent := &column{head: st.say("col.df_recent", "span", st.spanHead(before)), right: true}
		gap := &column{head: st.say("col.df_between"), right: true}
		since := &column{head: st.say("col.df_after", "span", st.spanHead(after)), right: true}
		grade := &column{head: st.say("col.finding")}
		// later holds the finding of the later case alone and place the
		// slot the DIMM is in now, for a terminal too narrow for both
		// states. The changes above name the moves.
		later := &column{head: st.say("col.finding")}
		place := &column{head: st.say("col.slot")}
		const none = "-"
		or := func(s string) string {
			if s == "" {
				return none
			}
			return s
		}
		for i := range c.DIMMs {
			d := &c.DIMMs[i]
			dimm.cells = append(dimm.cells, cell{text: d.Name})
			where := d.SlotAfter
			if d.SlotBefore != d.SlotAfter {
				where = or(d.SlotBefore) + " " + arrow + " " + or(d.SlotAfter)
			}
			slot.cells = append(slot.cells, cell{text: where})
			if d.SlotAfter == "" {
				place.cells = append(place.cells, cell{text: d.SlotBefore + " " + arrow + " " + none})
			} else {
				place.cells = append(place.cells, cell{text: d.SlotAfter})
			}
			was, now := errorCount(d.Before), errorCount(d.After)
			last, mid := errorCount(d.Recent), errorCount(d.Between)
			if d.SlotBefore == "" {
				was, last, mid = none, none, none
			}
			if d.SlotAfter == "" {
				now = none
			}
			all.cells = append(all.cells, cell{text: was})
			recent.cells = append(recent.cells, cell{text: last})
			gap.cells = append(gap.cells, cell{text: mid})
			since.cells = append(since.cells, cell{text: now})
			text, color := d.GradeAfterText, gradeColor(string(d.GradeAfter))
			switch {
			case d.GradeAfter == "":
				text, color = d.GradeBeforeText+" "+arrow+" "+none, gradeColor(string(d.GradeBefore))
			case d.GradeBefore != d.GradeAfter:
				text = or(d.GradeBeforeText) + " " + arrow + " " + text
			}
			grade.cells = append(grade.cells, cell{text: text, color: color})
			later.cells = append(later.cells, cell{text: or(d.GradeAfterText), color: gradeColor(string(d.GradeAfter))})
		}
		// The errors between the earlier case and the period after have
		// a column when they can be tied to the DIMMs. What goes first
		// when the terminal is narrow: the total of the earlier case,
		// which "my open" shows, then that column, which a note repeats.
		wide := func() bool { return dimm.width()+slot.width()+since.width()+grade.width()+8 > st.width() }
		if wide() {
			grade = later
		}
		if wide() {
			slot = place
		}
		cols := []*column{dimm, slot, all, recent, since, grade}
		if c.BetweenPlaced {
			cols = []*column{dimm, slot, all, recent, gap, since, grade}
		}
		st.table(p, cols, []*column{all, gap, recent})
	} else {
		p.add(" " + st.say("df.no_dimms"))
	}
	if c.NotListed > 0 {
		for _, line := range st.Wrap(st.say("df.not_listed", "n", c.NotListed), 1) {
			p.add(st.dim(line))
		}
	}

	for _, block := range []struct {
		label string
		texts []view.Text
		color string
	}{{st.say("df.reading"), c.Readings, ""}, {st.say("df.notes"), c.Notes, cDim}} {
		if len(block.texts) == 0 {
			continue
		}
		p.blank()
		label := block.label
		for _, t := range block.texts {
			p.labelled(label, lw, t.Text, block.color)
			label = ""
		}
	}
	return p.writeTo(w)
}

// apart writes the time between the two cases.
func (st Style) apart(c *view.Comparison) string {
	unit, n := view.SpanOf(c.After.AsOf.Sub(c.Before.AsOf))
	return st.say("df."+unit, "n", n)
}
