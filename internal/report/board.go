package report

import (
	"strconv"
	"strings"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/explorer"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/view"
)

// The board of section 2: the CPUs with the banks of their channels, in
// the arrangement and the proportions of the Memory Explorer, drawn in the
// palette of the report. Every stick names a card in #mx-cards, which the
// script shows beside the pointer; without script the title of the stick
// says the same in one line.

// Proportions of the board at scale 1, in pixels.
const (
	bStickW   = 18.0
	bStickH   = 56.0
	bNotchW   = 8.0
	bNotchH   = 3.0
	bGapIn    = 2.0
	bGapCh    = 6.0
	bBus      = 24.0
	bInterCPU = 32.0
	bCPUW     = 92.0
	// bText is the size of the text of the board; it does not scale.
	bText = 9.5
	// bMinScale is the least a row of two sockets is shrunk to; below it
	// each socket takes a row of its own.
	bMinScale = 0.7
)

// boardDrawn tells a board that has a CPU to draw.
func boardDrawn(b *view.Board) bool {
	return b != nil && b.Counts.Placed+b.Counts.NoChannel > 0 && len(b.Rows) > 0
}

// boardWidth is the width of the parts of a row at scale 1.
func boardWidth(parts []view.BoardPart) float64 {
	w := 0.0
	for i, p := range parts {
		if i > 0 {
			if p.CPU != nil && parts[i-1].CPU != nil {
				w += bInterCPU
			} else {
				w += bBus
			}
		}
		if p.CPU != nil {
			w += bCPUW
			continue
		}
		w += bankWidth(p)
	}
	return w
}

func bankWidth(p view.BoardPart) float64 {
	w := 0.0
	for c, ch := range p.Channels {
		if c > 0 {
			w += bGapCh
		}
		w += float64(len(ch.Sticks))*bStickW + float64(max(len(ch.Sticks)-1, 0))*bGapIn
	}
	return w
}

// boardLines are the rows of the board as drawn: a row of two sockets
// that would shrink below bMinScale is drawn as two rows.
func boardLines(b *view.Board) [][]view.BoardPart {
	var out [][]view.BoardPart
	for _, r := range b.Rows {
		if figW/boardWidth(r.Parts) < bMinScale && len(r.Parts) == 4 {
			out = append(out, r.Parts[:2], r.Parts[2:])
			continue
		}
		out = append(out, r.Parts)
	}
	return out
}

// cardID is the id of the card of a stick or a CPU.
func cardID(prefix string, n int) string { return "mx-" + prefix + "-" + strconv.Itoa(n) }

// boardFigure draws the board. anchors maps a slot to the section of its
// finding; cards gives the id of the card of each slot.
func (doc *Document) boardFigure(b *view.Board, anchors map[string]string, subject string, cards map[string]string) *node {
	lines := boardLines(b)
	widest := 0.0
	for _, ln := range lines {
		widest = max(widest, boardWidth(ln))
	}
	s := min(1.0, figW/widest)
	// Name band below the sticks: names stand upright.
	nameW := 0.0
	cpuLevels := 0
	for _, ln := range lines {
		for _, p := range ln {
			if p.CPU != nil {
				cpuLevels = max(cpuLevels, len(p.CPU.Caches))
			}
			for _, ch := range p.Channels {
				for _, st := range ch.Sticks {
					nameW = max(nameW, monoWidth(st.Slot, bText))
				}
			}
		}
	}
	stickH := bStickH * s
	if stickH < 40 {
		stickH = 40
	}
	cpuH := 18 + 2*13 + float64(cpuLevels)*13 + 8
	bankH := stickH + 6 + nameW + 6 + bText + 4
	rowH := max(bankH, cpuH) + 18
	w := min(figW, widest*s)
	h := rowH*float64(len(lines)) + 10
	fig := svg("fig slotmap board", w, h, doc.say("fig.board", "host", doc.Overview.Host.Name))
	cpuN := 0
	for i, ln := range lines {
		y := 12 + float64(i)*rowH
		x := (w - boardWidth(ln)*s) / 2
		row := el("g", A{"class", "mb-socket-row"})
		for k, p := range ln {
			if k > 0 {
				gap := bBus
				if p.CPU != nil && ln[k-1].CPU != nil {
					gap = bInterCPU
				}
				mid := y + min(stickH, cpuH)/2
				row.add(el("path", A{"class", "mb-bus", "d", "M" + num(x) + " " + num(mid) + "h" + num(gap*s)}))
				x += gap * s
			}
			if p.CPU != nil {
				row.add(doc.cpuDie(p.CPU, x, y, bCPUW*s, cpuH, cardID("cpu", cpuN), b.Caches))
				cpuN++
				x += bCPUW * s
				continue
			}
			bank := el("g", A{"class", "mb-bank"})
			for c, ch := range p.Channels {
				if c > 0 {
					x += bGapCh * s
				}
				group := el("g", A{"class", "mb-ch-group"})
				x0 := x
				for j, st := range ch.Sticks {
					if j > 0 {
						x += bGapIn * s
					}
					group.add(doc.stick(st, x, y, bStickW*s, stickH, nameW, anchors[st.Slot], cards[st.Slot], subject == st.Slot))
					x += bStickW * s
				}
				if ch.Channel.Valid {
					group.add(el("text", A{"class", "mb-ch-label", "x", num((x0 + x) / 2), "y", num(y + stickH + 6 + nameW + 6 + bText), "text-anchor", "middle"},
						doc.say("board.ch", "n", ch.Channel.V)))
				}
				bank.add(group)
			}
			row.add(bank)
		}
		fig.add(row)
	}
	return fig
}

// stick draws one slot: its module with the grade of its finding, the
// notch red when the module had an uncorrected error, the size upright on
// it and the slot name upright below it.
func (doc *Document) stick(st view.BoardStick, x, y, w, h, nameW float64, anchor, card string, subject bool) *node {
	title := doc.stickTitle(st)
	cls := classes("slot", "mb-dimm-stick", gradeClass(st.State))
	attrs := A{"class", cls, "aria-label", title}
	if card != "" {
		attrs = append(attrs, "data-card", card)
	}
	var g *node
	if anchor != "" {
		g = el("a", append(A{"href", "#" + anchor}, attrs...))
	} else {
		g = el("g", attrs)
	}
	g.add(el("title", title),
		el("rect", A{"class", "cell", "x", num(x), "y", num(y), "width", num(w), "height", num(h), "rx", "2"}))
	if st.State != view.SlotEmpty && st.State != view.SlotUnknown {
		notch := "mb-stick-notch"
		if st.UE > 0 {
			notch += " ue"
		}
		g.add(el("rect", A{"class", notch, "x", num(x + (w-bNotchW*w/bStickW)/2), "y", num(y - bNotchH), "width", num(bNotchW * w / bStickW), "height", num(bNotchH)}))
	}
	if gl := glyph(st.State); gl != nil && w >= 10 {
		gl.add(A{"transform", "translate(" + num(x+w/2) + " " + num(y+9) + ")"})
		g.add(gl)
	}
	if st.Size != "" && monoWidth(st.Size, bText) <= h-22 {
		tx, ty := x+w/2+bText*0.35, y+h-4
		g.add(el("text", A{"class", "mb-stick-cap", "x", num(tx), "y", num(ty), "transform", "rotate(-90 " + num(tx) + " " + num(ty) + ")"}, st.Size))
	}
	tx, ty := x+w/2+bText*0.35, y+h+6
	g.add(el("text", A{"class", "celllabel", "x", num(tx), "y", num(ty), "text-anchor", "end", "transform", "rotate(-90 " + num(tx) + " " + num(ty) + ")"}, st.Slot))
	if subject {
		cx := x + w/2
		g.add(el("path", A{"class", "pointer", "d", "M" + num(cx-4) + " " + num(y-12) + "h8l-4 6z"}))
	}
	return g
}

// stickTitle is what a stick says in one line.
func (doc *Document) stickTitle(st view.BoardStick) string {
	state := doc.vsay("ov.empty")
	switch st.State {
	case view.SlotUnknown:
		state = doc.vsay("ov.unknown")
	case view.SlotEmpty:
	default:
		state = engine.GradeText(doc.Lang, model.VerdictGrade(st.State))
	}
	t := doc.say("slot.title", "slot", st.Slot, "state", state)
	if st.Size != "" {
		t += ", " + st.Size
	}
	if st.Module != nil && st.Module.Serial != "" {
		t += ", " + doc.say("slot.serial", "serial", st.Module.Serial)
	}
	if st.CE+st.UE > 0 {
		t += ", " + doc.vsay("ov.errors", "ce", st.CE, "ue", st.UE)
	}
	return t
}

// cpuDie draws a CPU: its name, the count and size of its modules, and a
// bar per cache level.
func (doc *Document) cpuDie(cpu *view.BoardCPU, x, y, w, h float64, card, source string) *node {
	title := doc.say("board.cpu", "n", cpu.Socket)
	g := el("g", A{"class", "mb-cpu-die", "data-card", card, "aria-label", title, "role", "img"},
		el("title", title),
		el("rect", A{"class", "mb-die", "x", num(x), "y", num(y), "width", num(w), "height", num(h), "rx", "4"}),
		el("text", A{"class", "mb-cpu-name", "x", num(x + w/2), "y", num(y + 16), "text-anchor", "middle"}, title),
		el("text", A{"class", "mb-cpu-sub", "x", num(x + w/2), "y", num(y + 29), "text-anchor", "middle"}, doc.say("board.dimms", "n", cpu.DIMMs)))
	if cpu.Size != "" {
		g.add(el("text", A{"class", "mb-cpu-sub", "x", num(x + w/2), "y", num(y + 42), "text-anchor", "middle"}, cpu.Size))
	}
	for i, c := range cpu.Caches {
		cy := y + 48 + float64(i)*13
		g.add(el("rect", A{"class", "mb-cache-bar cache-" + strings.ToLower(c.Level), "x", num(x + 6), "y", num(cy), "width", num(w - 12), "height", "11", "rx", "2"}),
			el("text", A{"class", "mb-cache-label", "x", num(x + 9), "y", num(cy + 8.5)}, c.Level+" "+c.Size))
	}
	return g
}

// boardCards are the cards of the sticks and the CPUs: per slot the rows
// of the module, its counts, where the board put it and its finding; per
// CPU its modules and caches.
func (doc *Document) boardCards(b *view.Board) (*node, map[string]string) {
	box := el("div", A{"id", "mx-cards", "hidden", "hidden"})
	ids := map[string]string{}
	n := 0
	row := func(dl *node, key, val string) {
		if val != "" {
			dl.add(el("div", el("dt", doc.say(key)), el("dd", val)))
		}
	}
	optInt := func(o model.Opt[int], unit string) string {
		if !o.Valid {
			return ""
		}
		return strconv.Itoa(o.V) + unit
	}
	cpuN := 0
	for _, ln := range boardLines(b) {
		for _, p := range ln {
			if p.CPU != nil {
				dl := el("dl", A{"id", cardID("cpu", cpuN), "class", "mx-card"})
				dl.add(el("div", A{"class", "mx-card-title"}, el("dt", doc.say("board.cpu", "n", p.CPU.Socket)), el("dd", "")))
				row(dl, "card.dimms", strconv.Itoa(p.CPU.DIMMs))
				row(dl, "card.memory", p.CPU.Size)
				for _, c := range p.CPU.Caches {
					dl.add(el("div", el("dt", doc.say("card.cache", "level", c.Level)), el("dd", c.Size)))
				}
				if len(p.CPU.Caches) > 0 && b.Caches != "" {
					row(dl, "card.cache_from", b.Caches)
				}
				box.add(dl)
				cpuN++
				continue
			}
			for _, ch := range p.Channels {
				for _, st := range ch.Sticks {
					id := cardID("slot", n)
					n++
					ids[st.Slot] = id
					dl := el("dl", A{"id", id, "class", "mx-card"})
					dl.add(el("div", A{"class", "mx-card-title"}, el("dt", st.Slot), el("dd", "")))
					row(dl, "card.locator", st.Locator)
					row(dl, "card.size", st.Size)
					if m := st.Module; m != nil {
						row(dl, "card.type", m.Type)
						row(dl, "card.speed", optInt(m.SpeedMTs, " MT/s"))
						row(dl, "card.configured", optInt(m.Configured, " MT/s"))
						row(dl, "card.maker", m.Maker)
						row(dl, "card.part", m.PartNumber)
						row(dl, "card.serial", m.Serial)
						row(dl, "card.ranks", optInt(m.Ranks, ""))
					}
					row(dl, "card.ce", strconv.FormatInt(st.CE, 10))
					row(dl, "card.ue", strconv.FormatInt(st.UE, 10))
					row(dl, "card.bank", st.BankLocator)
					row(dl, "card.place", doc.placeText(st.Place))
					row(dl, "card.finding", doc.stateText(st.State))
					box.add(dl)
				}
			}
		}
	}
	return box, ids
}

func (doc *Document) stateText(state string) string {
	switch state {
	case view.SlotEmpty:
		return doc.vsay("ov.empty")
	case view.SlotUnknown:
		return doc.vsay("ov.unknown")
	}
	return engine.GradeText(doc.Lang, model.VerdictGrade(state))
}

func (doc *Document) placeText(p explorer.Placement) string {
	switch p.State {
	case explorer.Placed:
		return doc.say("board.place", "cpu", p.Socket, "ch", p.Channel)
	case explorer.NoChannel:
		return doc.say("board.place_nochannel", "cpu", p.Socket)
	}
	return doc.say("board.place_aside")
}

// boardStates is the slot map of the sticks of the board, for its legend.
func boardStates(b *view.Board) *view.SlotMap {
	sm := &view.SlotMap{Rows: []view.SlotRow{{}}}
	for _, r := range b.Rows {
		for _, p := range r.Parts {
			for _, ch := range p.Channels {
				var cells []view.SlotCell
				for _, st := range ch.Sticks {
					cells = append(cells, view.SlotCell{Slot: st.Slot, State: st.State})
				}
				sm.Rows[0].Channels = append(sm.Rows[0].Channels, view.SlotChannel{Slots: cells})
			}
		}
	}
	return sm
}

// boardSummary is the line above the board: slots that hold a module, the
// size of all, the errors, and the type and speed all modules share.
func (doc *Document) boardSummary(b *view.Board) *node {
	p := el("p", A{"class", "board-summary"}, doc.say("board.summary", "populated", b.Populated, "slots", b.Slots))
	if b.Size != "" {
		p.add(", ", el("span", A{"class", "nw"}, doc.say("board.total", "size", b.Size)))
	}
	if b.CE+b.UE > 0 {
		p.add(", ", el("span", A{"class", "nw"}, doc.vsay("ov.errors", "ce", b.CE, "ue", b.UE)))
	}
	if b.Badge != "" {
		p.add(" ", el("span", A{"class", "mb-ddr-badge"}, b.Badge))
	}
	return p
}

// boardAside is the line before the slots set aside.
func (doc *Document) boardAside(b *view.Board) string {
	return doc.say("board.aside", "n", b.Aside.Slots)
}
