package report

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/view"
)

//go:embed figures.css
var figureSheet string

// Figure is one figure of a report as a picture of its own: an SVG
// document that carries its style, a heading, its legend and the notes
// the report prints below it. It is for the places a whole report does
// not fit in.
type Figure struct {
	// Name names the figure, for a file name: "slotmap", "timeline",
	// "CPU0_C1-map".
	Name string
	SVG  []byte
}

// legendItem is one entry of the legend of a figure on its own.
type legendItem struct {
	// swatch draws the mark of the entry in a box of 14 by 14 pixels at
	// the origin.
	swatch []*node
	label  string
}

// gradeItem is the legend entry of a grade or a slot state.
func gradeItem(state, label string) legendItem {
	g := el("g", A{"class", gradeClass(state)}, el("rect", A{"class", "cell", "x", "0.5", "y", "0.5", "width", "13", "height", "13", "rx", "3"}))
	if gl := glyph(state); gl != nil {
		gl.add(A{"transform", "translate(7 7)"})
		g.add(gl)
	}
	return legendItem{[]*node{g}, label}
}

// shadeItem is the legend entry of a shade of the address map.
func shadeItem(class, label string, ue bool) legendItem {
	sw := []*node{el("rect", A{"class", class, "x", "1", "y", "1", "width", "12", "height", "12", "rx", "2"})}
	if ue {
		sw = append(sw, el("path", A{"class", "uex", "d", "M4.4 4.4L9.6 9.6M9.6 4.4L4.4 9.6"}))
	}
	return legendItem{sw, label}
}

// plain turns the links of a figure into plain groups: a figure on its
// own has no appendix to lead to. What a mark says stays as its title.
func plain(n *node) *node {
	out := &node{tag: n.tag, text: n.text, raw: n.raw}
	if n.tag == "a" {
		out.tag = "g"
	}
	for _, a := range n.attrs {
		switch a.name {
		case "href", "data-rec", "data-label", "aria-label":
			if n.tag == "a" {
				continue
			}
		}
		out.attrs = append(out.attrs, a)
	}
	for _, k := range n.kids {
		out.kids = append(out.kids, plain(k))
	}
	return out
}

// wrapText breaks a note into lines of at most width pixels, between
// words.
func wrapText(text string, size, width float64) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		if line != "" && textWidth(line+" "+word, size) > width {
			lines = append(lines, line)
			line = word
			continue
		}
		if line != "" {
			line += " "
		}
		line += word
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// standalone writes a figure of the report as a document of its own.
func standalone(name, heading string, legend []legendItem, fig *node, notes []string) (Figure, error) {
	const (
		pad      = 16.0
		legendH  = 20.0
		noteSize = 11.0
		noteH    = 16.0
	)
	w := figW + 2*pad
	y := pad
	root := el("svg", A{"xmlns", "http://www.w3.org/2000/svg", "class", "fig", "role", "img", "aria-label", heading},
		el("title", heading), el("style", rawText(figureSheet)), el("rect", A{"class", "bg", "width", num(w)}))
	bg := root.kids[2]
	root.add(el("text", A{"class", "figtitle", "x", num(pad), "y", num(y + 12)}, heading))
	y += 26

	// The legend: entries side by side, on a new line where the width ends.
	x := pad
	for i, it := range legend {
		lw := 20 + textWidth(it.label, 11) + 18
		if i > 0 && x+lw > w-pad {
			x, y = pad, y+legendH
		}
		g := el("g", A{"transform", "translate(" + num(x) + " " + num(y) + ")"})
		g.add(it.swatch)
		g.add(el("text", A{"class", "figlegend", "x", "20", "y", "11"}, it.label))
		root.add(g)
		x += lw
	}
	if len(legend) > 0 {
		y += legendH + 6
	}

	// The figure itself, without its own title: the heading says it.
	body := el("g", A{"transform", "translate(" + num(pad) + " " + num(y) + ")"})
	for _, k := range fig.kids {
		if k.tag == "title" {
			continue
		}
		body.add(plain(k))
	}
	root.add(body)
	var fh float64
	if _, err := fmt.Sscanf(fig.attr("height"), "%g", &fh); err != nil {
		return Figure{}, fmt.Errorf("report: figure %s has no height", name)
	}
	y += fh + 8

	for _, note := range notes {
		for _, line := range wrapText(note, noteSize, figW) {
			root.add(el("text", A{"class", "fignote", "x", num(pad), "y", num(y + 11)}, line))
			y += noteH
		}
		y += 2
	}
	h := y + pad - 4
	root.add(A{"viewBox", "0 0 " + num(w) + " " + num(h), "width", num(w), "height", num(h)})
	bg.add(A{"height", num(h)})

	var b strings.Builder
	if err := root.write(&b, false); err != nil {
		return Figure{}, err
	}
	b.WriteByte('\n')
	return Figure{Name: name, SVG: []byte(b.String())}, nil
}

// Figures returns the figures of the report as pictures of their own: the
// slot map, the timeline of the host and, for every DIMM the report draws
// figures for, its topology bars, address map and timeline.
func (doc *Document) Figures() ([]Figure, error) {
	var out []Figure
	add := func(name, heading string, legend []legendItem, fig *node, notes []string) error {
		f, err := standalone(name, heading, legend, fig, notes)
		if err != nil {
			return err
		}
		out = append(out, f)
		return nil
	}
	host := doc.Overview.Host.Name

	if sm := &doc.Overview.SlotMap; len(sm.Rows) > 0 {
		subject := ""
		for _, d := range doc.DIMMs {
			if d.Subject && d.Card.Present {
				subject = d.Card.Slot
			}
		}
		var legend []legendItem
		for _, g := range []model.VerdictGrade{model.VerdictReplaceNow, model.VerdictConsiderReplacement, model.VerdictObserve, model.VerdictOK, model.VerdictHoldReplacement} {
			legend = append(legend, gradeItem(string(g), engine.GradeText(doc.Lang, g)))
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
			legend = append(legend, gradeItem(view.SlotEmpty, doc.vsay("ov.empty")))
		}
		if unknown {
			legend = append(legend, gradeItem(view.SlotUnknown, doc.say("slot.unknown")))
		}
		if subject != "" {
			legend = append(legend, legendItem{[]*node{el("g", A{"class", "slot"}, el("path", A{"class", "pointer", "d", "M3 4h8l-4 6z"}))}, doc.say("sum.subject")})
		}
		notes := []string{doc.vsay("ov.slots", "slots", sm.Slots, "dimms", sm.Populated)}
		if err := add("slotmap", doc.say("fig.slotmap", "host", host), legend, doc.slotMapFigure(sm, nil, subject), notes); err != nil {
			return nil, err
		}
	}

	timelineNotes := func(tl *view.Timeline) []string {
		var notes []string
		for _, g := range tl.Markers {
			notes = append(notes, g.Text.Text)
		}
		for _, n := range tl.Notes {
			notes = append(notes, n.Text)
		}
		return notes
	}
	drawn := func(tl *view.Timeline) bool {
		return tl != nil && tl.Days > 0 && (len(tl.Series) > 0 || len(tl.Markers) > 0)
	}
	if tl := doc.Timeline; drawn(tl) {
		if err := add("timeline", doc.say("tl.host", "host", host)+", "+doc.vsay("span", "from", day(tl.From), "to", day(tl.To)), nil,
			doc.timelineFigure(tl, doc.vsay("ov.host", "name", host)), timelineNotes(tl)); err != nil {
			return nil, err
		}
	}

	for _, d := range doc.detailed() {
		c := d.Card
		if c.CE+c.UE == 0 {
			continue
		}
		base := strings.TrimPrefix(d.Anchor, "dimm-")
		who := dimmName(c)
		if t := d.Topology; t != nil && t.Total > 0 {
			if err := add(base+"-topology", who+": "+doc.vsay("topo.title"), nil, doc.topologyFigure(t, c.Slot), nil); err != nil {
				return nil, err
			}
		}
		if m := d.Map; m != nil && m.Bank != nil && len(m.Lines) > 0 {
			cellNote := doc.vsay("map.cell", "n", m.ColumnsPerCell)
			if m.Mode == view.MapBins {
				cellNote = doc.vsay("map.cell_bins", "n", m.ColumnsPerCell, "rows", m.RowsPerLine)
			}
			legend := []legendItem{shadeItem("ht0", "0", false)}
			for i := range m.Scale {
				legend = append(legend, shadeItem(fmt.Sprintf("ht%d", min(i+1, 4)), scaleText(m.Scale, i), false))
			}
			legend = append(legend, shadeItem("ue", doc.vsay("map.ue"), true))
			var notes []string
			for _, f := range m.Faults {
				notes = append(notes, f.Text.Text)
			}
			// On its own the map leads nowhere: no cell is a link.
			alone := *d
			alone.Linked = nil
			if err := add(base+"-map", who+": "+m.Bank.Label+", "+doc.vsay("map.axes")+", "+cellNote, legend, doc.addressMapFigure(&alone), notes); err != nil {
				return nil, err
			}
		}
		if tl := d.Timeline; drawn(tl) {
			if err := add(base+"-timeline", who+": "+doc.vsay("tl.title")+", "+doc.vsay("span", "from", day(tl.From), "to", day(tl.To)), nil,
				doc.timelineFigure(tl, c.Slot), timelineNotes(tl)); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}
