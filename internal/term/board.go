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
					marks = marks || s.CE+s.UE > 0
				}
			}
		}
	}
	if len(b.Rows) > 0 {
		st.boardLegend(p, shown, marks)
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
				out = append(out, stripPart{channel: ch.Channel, sticks: ch.Sticks, bankEnd: i == len(p.Channels)-1})
			}
		}
		return out
	}
	all := flat(parts)
	if st.stripWidth(all, 1, 2) <= st.width() || st.stripWidth(all, 0, 1) <= st.width() {
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
		if st.stripWidth(h, 0, 1) <= st.width() {
			out = append(out, h)
			continue
		}
		// Cut at a channel into as few lines as hold it.
		var cur []stripPart
		for _, sp := range h {
			if len(cur) > 0 && st.stripWidth(append(append([]stripPart{}, cur...), sp), 0, 1) > st.width() {
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

// stripLabels are the shared start of the slot names of a strip and the
// width of a cell.
func stripLabels(seg []stripPart) (prefix string, cell int) {
	var names []string
	for _, sp := range seg {
		for _, s := range sp.sticks {
			names = append(names, s.Slot)
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
	if st.ASCII {
		return "==[CPU " + strconv.Itoa(cpu.Socket) + "]=="
	}
	return "══╡CPU " + strconv.Itoa(cpu.Socket) + "╞══"
}

func (st Style) labelWidth(prefix string, errors bool) int {
	w := max(Width(st.say("board.ch")), Width(prefix))
	if errors {
		w = max(w, Width(st.say("board.err")))
	}
	return 1 + w + 2
}

// stripWidth is the width of a strip with the given gaps.
func (st Style) stripWidth(seg []stripPart, gapIn, gapCh int) int {
	prefix, cell := stripLabels(seg)
	w := st.labelWidth(prefix, true)
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
		gapIn, gapCh = 0, 1
	}
	prefix, cell := stripLabels(seg)
	lw := st.labelWidth(prefix, errors)
	var ch, mid, names, errs strings.Builder
	pad := func(b *strings.Builder, s string, w int) { b.WriteString(padRight(s, w)) }
	pad(&ch, " "+st.say("board.ch"), lw)
	pad(&mid, "", lw)
	pad(&names, " "+prefix, lw)
	pad(&errs, " "+st.say("board.err"), lw)
	anyChannel := false
	for i, sp := range seg {
		if i > 0 {
			for _, b := range []*strings.Builder{&ch, &mid, &names, &errs} {
				b.WriteString(strings.Repeat(" ", gapCh))
			}
		}
		if sp.cpu != nil {
			box := st.cpuBox(sp.cpu)
			bw := Width(box)
			ch.WriteString(strings.Repeat(" ", bw))
			mid.WriteString(st.bold(box))
			names.WriteString(strings.Repeat(" ", bw))
			errs.WriteString(strings.Repeat(" ", bw))
			continue
		}
		span := len(sp.sticks)*cell + max(len(sp.sticks)-1, 0)*gapIn
		label := ""
		if sp.channel.Valid {
			label = strconv.Itoa(sp.channel.V)
			anyChannel = true
		}
		left := (span - Width(label) + 1) / 2
		ch.WriteString(strings.Repeat(" ", max(left, 0)) + padRight(label, span-max(left, 0)))
		for j, s := range sp.sticks {
			if j > 0 {
				for _, b := range []*strings.Builder{&mid, &names, &errs} {
					b.WriteString(strings.Repeat(" ", gapIn))
				}
			}
			glyph := st.paint(gradeColor(s.State), st.gradeGlyph(s.State))
			mid.WriteString(glyph + strings.Repeat(" ", cell-2))
			name := strings.TrimPrefix(s.Slot, prefix)
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
	out = append(out, mid.String(), names.String())
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
	for _, l := range flow(legend, "   ", st.width()-1) {
		p.add(" " + l)
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
	add := func(s view.BoardStick) {
		place := st.say("board.place_aside")
		switch s.Place.State {
		case explorer.Placed:
			place = st.say("board.place", "cpu", s.Place.Socket, "ch", s.Place.Channel)
		case explorer.NoChannel:
			place = st.say("board.place_nochannel", "cpu", s.Place.Socket)
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
		rows = append(rows, []string{s.Slot, place, s.Size, part, serial, strconv.FormatInt(s.CE, 10), strconv.FormatInt(s.UE, 10), state})
	}
	for _, r := range b.Rows {
		for _, part := range r.Parts {
			for _, ch := range part.Channels {
				for _, s := range ch.Sticks {
					add(s)
				}
			}
		}
	}
	for _, s := range b.Unplaced {
		add(s)
	}
	p.blank()
	for _, line := range table(rows, st.width()) {
		p.add(line)
	}
	if len(b.Lines) > 0 {
		p.blank()
		for _, t := range b.Lines {
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
