package report

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/view"
)

// The part "Boots" of the slot map section: the latest boots of the host,
// how each ended, and per DIMM what it showed in each boot; then, per DIMM
// with errors in two or more boots, its places boot by boot. Every cell
// carries a letter or a number, so that nothing is told by colour alone,
// and a table below each figure holds the same data for print and for a
// screen reader.

// showBoots says whether the report has a part on the boots: two or more
// boots in the window, and a stop or a DIMM with errors in two of them.
func (doc *Document) showBoots() bool {
	b := doc.Boots
	if b == nil || len(b.Boots) < 2 {
		return false
	}
	if b.Stops != nil {
		return true
	}
	for _, r := range b.Rows {
		if r.BootsWithErrors >= 2 {
			return true
		}
	}
	return false
}

func (doc *Document) bootsPart() []*node {
	if !doc.showBoots() {
		return nil
	}
	b := doc.Boots
	out := []*node{el("h3", A{"id", "boots"}, doc.say("boots.title"))}
	if s := b.Stops; s != nil {
		p := el("p", A{"class", "stops"}, el("strong", doc.vsay("boots.stops")))
		for i, l := range s.Lines {
			if i > 0 {
				p.add(el("br"))
			}
			p.add(" " + sentence(l.Text))
		}
		out = append(out, p)
	}
	if len(b.Rows) > 0 {
		out = append(out, el("figure", doc.bootsFigure(b, doc.say("fig.boots", "host", b.Host, "n", len(b.Boots))),
			el("figcaption", doc.bootsLegend())), scroll(doc.bootsTable(b)))
	}
	out = append(out, how(doc.say("cap.boots")))
	for _, d := range doc.DIMMs {
		if d.Boots == nil || len(d.Boots.Rows) == 0 {
			continue
		}
		out = append(out, el("h4", doc.say("boots.dimm", "slot", d.Card.Slot)),
			el("figure", doc.bootsFigure(d.Boots, doc.say("fig.boots_dimm", "slot", d.Card.Slot, "n", len(d.Boots.Boots)))),
			scroll(doc.bootsTable(d.Boots)))
		for _, e := range d.Boots.Rules {
			out = append(out, el("p", el("span", A{"class", "rules"}, doc.ruleChips([]string{e.Rule})), " "+sentence(e.Text.Text)))
		}
	}
	return out
}

// bootRows are the rows of a grid: the row of a DIMM, of the records that
// name no DIMM, or of a place of one DIMM, and the sum of the places
// beyond the first.
func (doc *Document) bootRows(b *view.Boots) ([]view.BootRow, []string) {
	rows := append([]view.BootRow{}, b.Rows...)
	if b.Others != nil {
		rows = append(rows, *b.Others)
	}
	labels := make([]string, len(rows))
	for i, r := range rows {
		switch {
		case r.Level == view.RowNoDIMM:
			labels[i] = doc.vsay("boots.no_dimm")
		case r.Level == view.RowDIMM:
			labels[i] = r.Slot
		case r.Level == "others":
			labels[i] = doc.vsay("boots.others", "n", r.Places)
		case r.Place == "":
			labels[i] = doc.vsay("boots.dimm_only")
		default:
			labels[i] = r.Place
		}
	}
	return rows, labels
}

// bootCell returns the class, the mark and the words of one cell.
func (doc *Document) bootCell(c view.BootCell) (class, mark, words string) {
	switch {
	case c.Stopped:
		return "bt-stop", "X", doc.say("boots.cell_stop", "ue", c.UE, "ce", c.CE)
	case c.UE > 0:
		return "bt-ue", "U", doc.say("boots.cell_ue", "ue", c.UE, "ce", c.CE)
	case c.CE > 0:
		level := 1
		switch {
		case c.CE >= 100:
			level = 3
		case c.CE >= 10:
			level = 2
		}
		mark = strconv.FormatInt(c.CE, 10)
		if c.CE > 99 {
			mark = "99+"
		}
		return fmt.Sprintf("bt-ce%d", level), mark, doc.say("boots.cell_ce", "ce", c.CE)
	case c.Absent:
		return "bt-absent", "-", doc.say("boots.cell_absent")
	case !c.Known:
		return "bt-unknown", "?", doc.say("boots.cell_unknown")
	}
	return "bt-none", "·", doc.say("boots.cell_none")
}

// endMark is how a boot ended, in one letter, and in words.
func (doc *Document) endMark(end string) (string, string) {
	switch end {
	case model.BootStop:
		return "S", doc.say("boots.end_stop")
	case model.BootPanic:
		return "p", doc.say("boots.end_panic")
	case "running":
		return "r", doc.say("boots.end_running")
	}
	return "-", doc.say("boots.end_none")
}

func (doc *Document) bootsFigure(b *view.Boots, title string) *node {
	const (
		size  = 10.5
		rowH  = 18.0
		cellH = 16.0
		headH = 36.0
	)
	rows, labels := doc.bootRows(b)
	labelW := 0.0
	for _, l := range labels {
		labelW = max(labelW, monoWidth(l, size))
	}
	labelW = min(labelW+12, figW/2)
	n := len(b.Boots)
	pitch := min((figW-labelW)/float64(max(n, 1)), 34)
	h := headH + float64(len(rows))*rowH + 4
	s := svg("fig boots", figW, h, title)
	for i, bc := range b.Boots {
		x := labelW + float64(i)*pitch
		letter, words := doc.endMark(bc.End)
		t := doc.say("boots.col", "n", bc.Index, "when", minute(bc.StartedAt), "ran", ranText(bc.RanSeconds), "end", words)
		cls := "tick"
		if bc.End == model.BootStop {
			cls = "tick stopmark"
		}
		s.add(el("g", el("title", t),
			el("text", A{"class", "tick", "x", num(x + pitch/2), "y", "12", "text-anchor", "middle"}, strconv.Itoa(bc.Index)),
			el("text", A{"class", cls, "x", num(x + pitch/2), "y", "28", "text-anchor", "middle"}, letter)))
	}
	for ri, r := range rows {
		y := headH + float64(ri)*rowH
		label := labels[ri]
		if monoWidth(label, size) > labelW-12 {
			for len(label) > 4 && monoWidth(label+"…", size) > labelW-12 {
				label = label[:len(label)-1]
			}
			label += "…"
		}
		s.add(el("text", A{"class", "rowaddr", "x", num(labelW - 8), "y", num(y + 12), "text-anchor", "end"}, label))
		for i, c := range r.Cells {
			if i >= n {
				break
			}
			x := labelW + float64(i)*pitch
			cls, mark, words := doc.bootCell(c)
			s.add(el("g", A{"class", cls},
				el("title", doc.say("boots.cell", "row", labels[ri], "n", c.Boot, "what", words)),
				el("rect", A{"x", num(x + 1), "y", num(y), "width", num(pitch - 2), "height", num(cellH), "rx", "2"}),
				el("text", A{"class", "celllabel", "x", num(x + pitch/2), "y", num(y + 11.5), "text-anchor", "middle"}, mark)))
		}
	}
	return s
}

// bootsLegend says what the marks of a grid stand for.
func (doc *Document) bootsLegend() string {
	return doc.say("boots.legend")
}

// bootsTable holds the data of a grid: one row per line of the grid, one
// column per boot.
func (doc *Document) bootsTable(b *view.Boots) *node {
	heads := []string{doc.say("boots.head_row")}
	for _, bc := range b.Boots {
		_, words := doc.endMark(bc.End)
		heads = append(heads, ">"+doc.say("boots.head_boot", "n", bc.Index, "end", words))
	}
	t, body := table("boots", heads...)
	start := el("tr", el("th", A{"scope", "row"}, doc.say("boots.head_started")))
	ran := el("tr", el("th", A{"scope", "row"}, doc.say("boots.head_ran")))
	for _, bc := range b.Boots {
		start.add(el("td", A{"class", "r nw"}, minute(bc.StartedAt)))
		ran.add(el("td", A{"class", "r nw"}, ranText(bc.RanSeconds)))
	}
	body.add(start, ran)
	rows, labels := doc.bootRows(b)
	for ri, r := range rows {
		tr := el("tr", el("th", A{"scope", "row", "class", "id"}, labels[ri]))
		for _, c := range r.Cells {
			var parts []string
			if c.CE > 0 {
				parts = append(parts, "CE "+i18n.Thousands(c.CE))
			}
			if c.UE > 0 {
				parts = append(parts, "UE "+i18n.Thousands(c.UE))
			}
			if c.Stopped {
				parts = append(parts, doc.say("boots.end_stop"))
			}
			switch {
			case len(parts) > 0:
			case c.Absent:
				parts = append(parts, doc.say("boots.cell_absent"))
			case !c.Known:
				parts = append(parts, "?")
			}
			tr.add(el("td", A{"class", "r nw"}, strings.Join(parts, ", ")))
		}
		body.add(tr)
	}
	return t
}

// ranText writes how long a boot ran.
func ranText(seconds int64) string {
	switch {
	case seconds <= 0:
		return "?"
	case seconds >= 48*3600:
		return strconv.FormatInt(seconds/86400, 10) + " d"
	case seconds >= 2*3600:
		return strconv.FormatInt(seconds/3600, 10) + " h"
	case seconds >= 120:
		return strconv.FormatInt(seconds/60, 10) + " min"
	}
	return strconv.FormatInt(seconds, 10) + " s"
}
