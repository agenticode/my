package term

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/explorer"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/view"
)

// Board draws the board of a host: per socket a strip of two-column cells
// in the arrangement of the Memory Explorer, the channel numbers above,
// the slot names below with what they share printed once at the left, and
// a row of marks for the errors on record. Slots the board sets aside
// follow, drawn as the slot map draws them.
func Board(w io.Writer, b *view.Board, st Style) error {
	p := &page{st: st}
	head := []string{st.say("ov.host", "name", b.Host)}
	if b.System != "" {
		head = append(head, b.System)
	}
	head = append(head, st.say("board.slots", "populated", b.Populated, "slots", b.Slots))
	if b.Size != "" {
		head[len(head)-1] += ", " + b.Size
	}
	if b.Badge != "" {
		head = append(head, b.Badge)
	}
	if b.CE+b.UE > 0 {
		head = append(head, st.say("ov.errors", "ce", b.CE, "ue", b.UE))
	}
	for _, line := range flow(head, "  ", st.width()) {
		p.add(line)
	}
	errors := b.CE+b.UE > 0
	shown := map[string]bool{}
	marks := false
	for _, r := range b.Rows {
		p.blank()
		for _, seg := range st.boardSegments(r.Parts) {
			for _, line := range st.strip(seg, errors) {
				p.add(line)
			}
			p.blank()
		}
		for _, part := range r.Parts {
			for _, ch := range part.Channels {
				for _, s := range ch.Sticks {
					shown[s.State] = true
					shown["named"] = shown["named"] || s.Label != ""
					marks = marks || s.CE+s.UE > 0
				}
			}
		}
	}
	if len(b.Rows) > 0 {
		st.boardLegend(p, shown, marks)
		st.populationLines(p, b)
	}
	if b.Aside != nil && len(b.Aside.Rows) > 0 {
		p.blank()
		why := ""
		if len(b.Reasons) > 0 {
			why = b.Reasons[0].Text.Text
		}
		for _, l := range wrap(st.say("board.aside", "n", b.Aside.Slots)+" "+why, st.width()-1) {
			p.add(" " + l)
		}
		p.blank()
		st.slotMap(p, b.Aside)
	}
	if st.Detail {
		st.boardDetail(p, b)
	}
	return p.writeTo(w)
}

// stripPart is a part of a strip: the sticks of a channel or a CPU.
type stripPart struct {
	cpu     *view.BoardCPU
	channel model.Opt[int]
	name    string // the vendor's name of the channel
	sticks  []view.BoardStick
	// bankEnd is set on the last channel of a bank.
	bankEnd bool
}

// boardSegments lays the parts of a row out in lines that fit the width:
// both sockets on one line, else one socket with its bank per line, and a
// bank that still does not fit is cut at a channel.
func (st Style) boardSegments(parts []view.BoardPart) [][]stripPart {
	flat := func(ps []view.BoardPart) []stripPart {
		var out []stripPart
		for _, p := range ps {
			if p.CPU != nil {
				out = append(out, stripPart{cpu: p.CPU})
				continue
			}
			for i, ch := range p.Channels {
				out = append(out, stripPart{channel: ch.Channel, name: ch.Name, sticks: ch.Sticks, bankEnd: i == len(p.Channels)-1})
			}
		}
		return out
	}
	all := flat(parts)
	if st.stripWidth(all, 1, 2) <= st.width() || st.stripWidth(all, minGap(all), 1) <= st.width() {
		return [][]stripPart{all}
	}
	var halves [][]stripPart
	if len(parts) == 4 {
		halves = [][]stripPart{flat(parts[:2]), flat(parts[2:])}
	} else {
		halves = [][]stripPart{all}
	}
	var out [][]stripPart
	for _, h := range halves {
		if st.stripWidth(h, minGap(h), 1) <= st.width() {
			out = append(out, h)
			continue
		}
		// Cut at a channel into as few lines as hold it.
		var cur []stripPart
		for _, sp := range h {
			next := append(append([]stripPart{}, cur...), sp)
			if len(cur) > 0 && st.stripWidth(next, minGap(next), 1) > st.width() {
				out = append(out, cur)
				cur = nil
			}
			cur = append(cur, sp)
		}
		if len(cur) > 0 {
			out = append(out, cur)
		}
	}
	return out
}

// minGap is the least gap between the cells of a channel: none when every
// name is narrower than a cell, else one, so that names do not run into
// each other.
func minGap(seg []stripPart) int {
	prefix, cell := stripLabels(seg)
	for _, sp := range seg {
		for _, s := range sp.sticks {
			if Width(strings.TrimPrefix(stickName(s), prefix)) >= cell {
				return 1
			}
		}
	}
	return 0
}

// stickName is the name a stick is drawn by: the slot, or for an empty
// slot the firmware gives no name, the label of the board's manual with a
// star.
func stickName(s view.BoardStick) string {
	if s.Label != "" {
		return s.Label + "*"
	}
	return s.Slot
}

// stripLabels are the shared start of the slot names of a strip and the
// width of a cell.
func stripLabels(seg []stripPart) (prefix string, cell int) {
	var names []string
	for _, sp := range seg {
		for _, s := range sp.sticks {
			names = append(names, stickName(s))
		}
	}
	prefix = commonStart(names)
	cell = 2
	for _, n := range names {
		cell = max(cell, Width(strings.TrimPrefix(n, prefix)))
	}
	return prefix, cell
}

// commonStart is what the names share up to their last separator.
func commonStart(names []string) string {
	if len(names) < 2 {
		return ""
	}
	p := names[0]
	for _, n := range names[1:] {
		for !strings.HasPrefix(n, p) {
			p = p[:len(p)-1]
		}
	}
	i := strings.LastIndexAny(p, "_-. ")
	if i < 0 {
		return ""
	}
	return p[:i+1]
}

func (st Style) cpuBox(cpu *view.BoardCPU) string {
	name := "CPU " + strconv.Itoa(cpu.Socket)
	if cpu.Controller {
		name = st.say("board.chipset")
	}
	if st.ASCII {
		return "==[" + name + "]=="
	}
	return "══╡" + name + "╞══"
}

func (st Style) labelWidth(prefix string, errors, marks bool) int {
	w := max(Width(st.say("board.ch")), Width(prefix))
	if errors {
		w = max(w, Width(st.say("board.err")))
	}
	if marks {
		w = max(w, Width(st.say("board.markrow")))
	}
	return 1 + w + 2
}

// segMarked tells a strip whose sticks carry marks of the population
// check.
func segMarked(seg []stripPart) bool {
	for _, sp := range seg {
		for _, s := range sp.sticks {
			if s.Mark != "" {
				return true
			}
		}
	}
	return false
}

// stripWidth is the width of a strip with the given gaps.
func (st Style) stripWidth(seg []stripPart, gapIn, gapCh int) int {
	prefix, cell := stripLabels(seg)
	w := st.labelWidth(prefix, true, segMarked(seg))
	for i, sp := range seg {
		if i > 0 {
			w += gapCh
		}
		if sp.cpu != nil {
			w += Width(st.cpuBox(sp.cpu))
			continue
		}
		w += len(sp.sticks)*cell + max(len(sp.sticks)-1, 0)*gapIn
	}
	return w
}

// strip draws one line of the board: channel numbers, cells, names and
// marks.
func (st Style) strip(seg []stripPart, errors bool) []string {
	gapIn, gapCh := 1, 2
	if st.stripWidth(seg, gapIn, gapCh) > st.width() {
		gapIn, gapCh = minGap(seg), 1
	}
	prefix, cell := stripLabels(seg)
	marked := segMarked(seg)
	lw := st.labelWidth(prefix, errors, marked)
	var ch, mid, marks, names, errs, chnames strings.Builder
	pad := func(b *strings.Builder, s string, w int) { b.WriteString(padRight(s, w)) }
	pad(&ch, " "+st.say("board.ch"), lw)
	pad(&mid, "", lw)
	pad(&marks, " "+st.say("board.markrow"), lw)
	pad(&names, " "+prefix, lw)
	pad(&errs, " "+st.say("board.err"), lw)
	pad(&chnames, " "+st.say("board.chname"), lw)
	anyChannel, anyName := false, false
	centre := func(b *strings.Builder, label string, span int) {
		left := (span - Width(label) + 1) / 2
		b.WriteString(strings.Repeat(" ", max(left, 0)) + padRight(label, span-max(left, 0)))
	}
	for i, sp := range seg {
		if i > 0 {
			for _, b := range []*strings.Builder{&ch, &mid, &marks, &names, &errs, &chnames} {
				b.WriteString(strings.Repeat(" ", gapCh))
			}
		}
		if sp.cpu != nil {
			box := st.cpuBox(sp.cpu)
			bw := Width(box)
			ch.WriteString(strings.Repeat(" ", bw))
			mid.WriteString(st.bold(box))
			marks.WriteString(strings.Repeat(" ", bw))
			names.WriteString(strings.Repeat(" ", bw))
			errs.WriteString(strings.Repeat(" ", bw))
			chnames.WriteString(strings.Repeat(" ", bw))
			continue
		}
		span := len(sp.sticks)*cell + max(len(sp.sticks)-1, 0)*gapIn
		label := ""
		if sp.channel.Valid {
			label = strconv.Itoa(sp.channel.V)
			anyChannel = true
			// The vendor's name of the channel, where it says more than
			// the number.
			if sp.name != "" && sp.name != label {
				anyName = true
			}
		}
		centre(&ch, label, span)
		name := ""
		if sp.channel.Valid {
			name = sp.name
		}
		centre(&chnames, truncate(name, span, ""), span)
		for j, s := range sp.sticks {
			if j > 0 {
				for _, b := range []*strings.Builder{&mid, &marks, &names, &errs} {
					b.WriteString(strings.Repeat(" ", gapIn))
				}
			}
			glyph := st.paint(gradeColor(s.State), st.gradeGlyph(s.State))
			mid.WriteString(glyph + strings.Repeat(" ", cell-2))
			// The mark of the population check stands under the cell.
			pad(&marks, s.Mark, cell)
			name := strings.TrimPrefix(stickName(s), prefix)
			if s.State != view.SlotEmpty && s.State != view.SlotUnknown && s.State != string(model.VerdictOK) {
				name = st.paint(gradeColor(s.State), st.bold(name))
			}
			pad(&names, name, cell)
			mark := ""
			if s.CE > 0 {
				mark += "c"
			}
			if s.UE > 0 {
				mark += "U"
			}
			pad(&errs, mark, cell)
		}
	}
	var out []string
	if anyChannel {
		out = append(out, ch.String())
	}
	out = append(out, mid.String())
	if marked {
		out = append(out, marks.String())
	}
	out = append(out, names.String())
	if anyName {
		out = append(out, strings.TrimRight(chnames.String(), " "))
	}
	if errors && strings.TrimSpace(errs.String()) != strings.TrimSpace(st.say("board.err")) {
		out = append(out, errs.String())
	}
	return out
}

// boardLegend names what the strips draw: the grades shown, empty and
// unknown slots, and the marks.
func (st Style) boardLegend(p *page, shown map[string]bool, marks bool) {
	var legend []string
	for _, g := range []model.VerdictGrade{model.VerdictReplaceNow, model.VerdictConsiderReplacement, model.VerdictObserve, model.VerdictOK, model.VerdictHoldReplacement} {
		if shown[string(g)] {
			legend = append(legend, st.paint(gradeColor(string(g)), st.gradeGlyph(string(g)))+" "+engine.GradeText(st.lang(), g))
		}
	}
	if shown[view.SlotEmpty] {
		legend = append(legend, st.paint(gradeColor(view.SlotEmpty), st.gradeGlyph(view.SlotEmpty))+" "+st.say("ov.empty"))
	}
	if shown[view.SlotUnknown] {
		legend = append(legend, st.gradeGlyph(view.SlotUnknown)+" "+st.say("ov.unknown"))
	}
	if marks {
		legend = append(legend, st.say("board.marks"))
	}
	if shown["named"] {
		legend = append(legend, st.say("board.named"))
	}
	for _, l := range flow(legend, "   ", st.width()-1) {
		p.add(" " + l)
	}
}

// populationLines are the legend line of the marks and one line per
// population finding; with --detail each finding names its document and
// page, and the lines that are no finding and the rules not checked
// follow.
func (st Style) populationLines(p *page, b *view.Board) {
	pop := b.Population
	if pop == nil {
		return
	}
	marked := false
	for _, r := range b.Rows {
		for _, part := range r.Parts {
			for _, ch := range part.Channels {
				for _, s := range ch.Sticks {
					marked = marked || s.Mark != ""
				}
			}
		}
	}
	if marked {
		legend := []string{"+ " + st.say("pop.legend.plus"), "- " + st.say("pop.legend.minus")}
		for _, l := range flow(legend, "   ", st.width()-1) {
			p.add(" " + l)
		}
	}
	notes := st.Detail && (len(pop.Notes) > 0 || len(pop.Unchecked) > 0)
	if len(pop.Findings) == 0 && !notes {
		return
	}
	p.blank()
	if st.Detail {
		p.add(" " + st.bold(st.say("pop.title")))
	}
	lw := 0
	for _, f := range pop.Findings {
		lw = max(lw, Width(f.Rule))
	}
	for _, f := range pop.Findings {
		p.labelled(f.Rule, lw, f.Text.Text, "")
		if st.Detail {
			p.labelled("", lw, f.StrengthText.Text+", "+f.Source.Cite, cDim)
		}
	}
	if !st.Detail {
		return
	}
	for _, n := range pop.Notes {
		for i, l := range wrap(n.Text.Text, st.width()-3) {
			if i == 0 {
				p.add(" - " + l)
			} else {
				p.add("   " + l)
			}
		}
	}
	for _, u := range pop.Unchecked {
		for i, l := range wrap(u.Text.Text, st.width()-3) {
			if i == 0 {
				p.add(" - " + st.dim(l))
			} else {
				p.add("   " + st.dim(l))
			}
		}
	}
}

// boardDetail adds per socket its modules, size and caches, a table of the
// modules, and the lines of data quality of the board.
func (st Style) boardDetail(p *page, b *view.Board) {
	p.blank()
	for _, r := range b.Rows {
		for _, part := range r.Parts {
			if c := part.CPU; c != nil {
				line := fmt.Sprintf(" CPU %d  %s", c.Socket, st.say("board.dimms", "n", c.DIMMs))
				if c.Controller {
					line = " " + st.say("board.chipset") + "  " + st.say("board.dimms", "n", c.DIMMs)
				}
				if c.Size != "" {
					line += ", " + c.Size
				}
				for _, cache := range c.Caches {
					line += "  " + cache.Level + " " + cache.Size
				}
				p.add(line)
			}
		}
	}
	rows := [][]string{{st.say("board.col.slot"), st.say("board.col.place"), st.say("board.col.size"), st.say("board.col.part"),
		st.say("board.col.serial"), "CE", "UE", st.say("board.col.finding")}}
	add := func(s view.BoardStick, chName string) {
		place := st.say("board.place_aside")
		switch s.Place.State {
		case explorer.Placed:
			ch := strconv.Itoa(s.Place.Channel)
			if chName != "" && chName != ch {
				ch += " " + chName
			}
			place = st.say("board.place", "cpu", s.Place.Socket, "ch", ch)
			if b.Hub {
				place = st.say("board.place_hub", "ch", ch)
			}
		case explorer.NoChannel:
			place = st.say("board.place_nochannel", "cpu", s.Place.Socket)
			if b.Hub {
				place = st.say("board.chipset")
			}
		}
		slot := s.Slot
		if s.Label != "" {
			slot = s.Label + "*"
		}
		part, serial := "", ""
		if s.Module != nil {
			part, serial = s.Module.PartNumber, s.Module.Serial
		}
		state := st.say("ov.empty")
		switch s.State {
		case view.SlotEmpty:
		case view.SlotUnknown:
			state = st.say("ov.unknown")
		default:
			state = engine.GradeText(st.lang(), model.VerdictGrade(s.State))
		}
		rows = append(rows, []string{slot, place, s.Size, part, serial, strconv.FormatInt(s.CE, 10), strconv.FormatInt(s.UE, 10), state})
	}
	for _, r := range b.Rows {
		for _, part := range r.Parts {
			for _, ch := range part.Channels {
				for _, s := range ch.Sticks {
					add(s, ch.Name)
				}
			}
		}
	}
	for _, s := range b.Unplaced {
		add(s, "")
	}
	p.blank()
	for _, line := range table(rows, st.width()) {
		p.add(line)
	}
	lines := b.Lines
	if pop := b.Population; pop != nil && pop.Text != nil {
		// Why the board got no population check, once.
		lines = append(append([]view.Text{}, lines...), *pop.Text)
	}
	if len(lines) > 0 {
		p.blank()
		for _, t := range lines {
			for i, l := range wrap(t.Text, st.width()-3) {
				if i == 0 {
					p.add(" - " + l)
				} else {
					p.add("   " + l)
				}
			}
		}
	}
}

// table lays rows out in columns, two spaces apart, cut to the width.
func table(rows [][]string, width int) []string {
	if len(rows) == 0 {
		return nil
	}
	ws := make([]int, len(rows[0]))
	for _, r := range rows {
		for i, c := range r {
			ws[i] = max(ws[i], Width(c))
		}
	}
	var out []string
	for _, r := range rows {
		var b strings.Builder
		b.WriteString(" ")
		for i, c := range r {
			if i > 0 {
				b.WriteString("  ")
			}
			if i == len(r)-1 {
				b.WriteString(c)
			} else {
				b.WriteString(padRight(c, ws[i]))
			}
		}
		line := strings.TrimRight(b.String(), " ")
		if Width(line) > width {
			line = truncate(line, width, "")
		}
		out = append(out, line)
	}
	return out
}
