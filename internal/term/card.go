package term

import (
	"fmt"
	"io"
	"strings"

	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/view"
)

// dimmHead draws the line that says which DIMM a screen is about, or, for
// the host finding, that it is about what names no DIMM.
func (st Style) dimmHead(p *page, c *view.Card) {
	if c.Scope == view.ScopeHost {
		fields := []string{st.bold(st.say("card.host", "host", c.Host)), st.say("ov.host_finding"), st.looseCounts(c)}
		for _, line := range flow(fields, "  ", st.width()) {
			p.add(line)
		}
		return
	}
	fields := []string{st.bold(c.Slot)}
	add := func(s string) { fields = append(fields, s) }
	add(c.DIMM.Kind)
	add(strings.TrimSpace(c.DIMM.Manufacturer + " " + c.DIMM.PartNumber))
	if c.DIMM.Serial != "" {
		add("S/N " + c.DIMM.Serial)
	} else {
		add(st.say("dimm.no_serial"))
	}
	if !c.Since.IsZero() {
		add(st.say("dimm.since", "when", c.Since.UTC().Format("2006-01-02")))
	}
	add(st.say("dimm.moves", "n", c.Moves))
	if !c.Present {
		add(st.say("ov.gone"))
	}
	// Fields flow to the next line where the width ends; an empty field
	// takes no room.
	for _, line := range flow(fields, "  ", st.width()) {
		p.add(line)
	}
}

// card draws the finding of a DIMM. With detail it adds the records each
// statement rests on, the steps of the confidence and the faults.
func (st Style) card(p *page, c *view.Card, detail bool) {
	labels := map[string]string{}
	lw := 0
	for _, code := range []string{"card.finding", "card.evidence", "card.checks", "card.missing", "card.baseline", "card.next", "card.factors", "card.faults"} {
		labels[code] = st.say(code)
		if detail || (code != "card.factors" && code != "card.faults") {
			lw = max(lw, Width(labels[code]))
		}
	}

	// The finding and, at the right edge, the rules that fired.
	conf := st.say("card.confidence", "level", c.ConfidenceText)
	if detail {
		conf += fmt.Sprintf(", %d", c.Confidence)
	}
	tail := " (" + c.Label + ", " + conf + ")"
	rules := strings.Join(c.Rules, " ")
	used := 1 + lw + 2 + Width(c.GradeText) + Width(tail)
	line := " " + st.dim(padRight(labels["card.finding"], lw)) + "  " + st.paint(gradeColor(string(c.Grade)), c.GradeText) + tail
	switch {
	case rules == "":
		p.add(line)
	case used+2+Width(rules) <= st.width():
		p.add(line + strings.Repeat(" ", st.width()-used-Width(rules)) + rules)
	default:
		p.add(line)
		p.add(" " + strings.Repeat(" ", lw) + "  " + rules)
	}

	label := labels["card.evidence"]
	for _, e := range c.Evidence {
		p.labelled(label, lw, "["+e.Rule+"] "+e.Text.Text, "")
		label = ""
		if detail && e.Records > 0 {
			ref := st.say("card.records", "n", e.Records)
			switch {
			case e.First != "" && e.First != e.Last && e.Last != "":
				ref += ": " + st.say("card.first") + " " + e.First + ", " + st.say("card.last") + " " + e.Last
			case e.First != "":
				ref += ": " + e.First
			}
			p.labelled("", lw, ref, cDim)
		}
	}
	label = labels["card.checks"]
	for _, k := range c.Checks {
		p.labelled(label, lw, k.EffectText+": "+k.Text.Text, "")
		label = ""
	}
	label = labels["card.missing"]
	for _, m := range c.Missing {
		p.labelled(label, lw, m.Text, "")
		label = ""
	}
	label = labels["card.baseline"]
	for _, b := range c.Baseline {
		p.labelled(label, lw, b.Text, "")
		label = ""
	}
	if c.Next != nil {
		p.labelled(labels["card.next"], lw, c.Next.Text, "")
	}
	if !detail {
		return
	}
	label = labels["card.factors"]
	for i, f := range c.Factors {
		delta := fmt.Sprintf("%+d", f.Delta)
		if i == 0 {
			delta = fmt.Sprintf("%d", f.Delta)
		}
		p.labelled(label, lw, padLeft(delta, 3)+"  "+f.Text.Text, "")
		label = ""
	}
	label = labels["card.faults"]
	for _, f := range c.Faults {
		text := view.FaultName(st.lang(), f.Mode, f.Estimated)
		if f.Where != "" {
			text = st.say("card.fault", "fault", text, "where", f.Where)
		}
		text += fmt.Sprintf(": CE %s, UE %s", i18n.Thousands(f.CE), i18n.Thousands(f.UE))
		if !f.First.IsZero() && f.First.Year() > 1970 {
			text += ", " + st.span(f.First.UTC().Format("2006-01-02"), f.Last.UTC().Format("2006-01-02"))
		}
		text += ", " + st.say("dir."+f.Direction)
		p.labelled(label, lw, text, "")
		label = ""
	}
}

// Why draws the finding of one or more DIMMs with everything it rests on.
func Why(w io.Writer, cards []*view.Card, st Style) error {
	p := &page{st: st}
	for i, c := range cards {
		if i > 0 {
			p.blank()
		}
		st.dimmHead(p, c)
		p.blank()
		st.card(p, c, true)
	}
	if len(cards) == 0 {
		p.add(st.say("ov.none"))
	}
	return p.writeTo(w)
}

// Oneline returns the finding of a DIMM as one line for a ticket: host,
// slot and serial number, the grade with the rules behind it, the first
// statement of the evidence and the next step.
func Oneline(c *view.Card, st Style) string {
	var b strings.Builder
	b.WriteString(c.Host)
	if c.Scope != view.ScopeHost {
		// The host finding names no DIMM: its first statement says what
		// it is about.
		b.WriteString(" " + c.Slot)
	}
	if c.DIMM.Serial != "" {
		b.WriteString(" S/N " + c.DIMM.Serial)
	}
	b.WriteString(": " + c.GradeText + " (" + c.Label + ", " + st.say("card.confidence", "level", c.ConfidenceText))
	if len(c.Rules) > 0 {
		b.WriteString("; " + strings.Join(c.Rules, ", "))
	}
	b.WriteString(")")
	sentence := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		b.WriteString(" " + s)
		if !strings.HasSuffix(s, ".") {
			b.WriteString(".")
		}
	}
	b.WriteString(".")
	if len(c.Evidence) > 0 {
		sentence(upperFirst(c.Evidence[0].Text.Text))
	}
	if c.Next != nil {
		sentence(c.Next.Text)
	}
	return b.String()
}

// upperFirst starts a sentence with a capital letter where the script has
// one.
func upperFirst(s string) string {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return s
	}
	return string(s[0]-'a'+'A') + s[1:]
}
