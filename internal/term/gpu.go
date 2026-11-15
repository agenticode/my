package term

import (
	"fmt"
	"io"
	"strings"

	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/view"
)

// GPUs draws the memory of the GPUs of a host as a repair budget: one line
// per GPU, a bar of its banks by the spare rows they have left, and below
// that what each finding rests on.
func GPUs(w io.Writer, v *view.GPUs, st Style) error {
	p := &page{st: st}
	if len(v.GPUs) == 0 {
		p.add(st.say("ov.host", "name", st.bold(v.Host)))
		p.blank()
		for _, line := range st.Wrap(st.say("gpu.none"), 1) {
			p.add(line)
		}
		st.gpuNotes(p, v.Notes)
		return p.writeTo(w)
	}
	fields := []string{st.say("ov.host", "name", st.bold(v.Host)), st.say("gpu.count", "n", len(v.GPUs))}
	if !v.AsOf.IsZero() {
		fields = append(fields, st.say("gpu.window", "days", v.Days, "to", v.AsOf.UTC().Format("2006-01-02")))
	}
	for _, line := range flow(fields, "  ", st.width()) {
		p.add(line)
	}
	fired := false
	for i := range v.GPUs {
		fired = fired || len(v.GPUs[i].Rules) > 0
	}
	if fired {
		p.add(st.say("gpu.finding", "grade", st.paint(gradeColor(string(v.Grade)), v.GradeText)))
	} else {
		// A state of the budget is no finding, and "ok" would read as one.
		p.add(st.say("gpu.no_finding"))
	}
	p.blank()
	st.gpuTable(p, v)
	p.blank()
	st.gpuBars(p, v)
	for i := range v.GPUs {
		st.gpuDetail(p, &v.GPUs[i], v.Detail)
	}
	st.gpuNotes(p, v.Notes)
	return p.writeTo(w)
}

func (st Style) gpuNotes(p *page, notes []view.Text) {
	if len(notes) == 0 {
		return
	}
	p.blank()
	label := st.say("card.missing")
	for _, n := range notes {
		p.labelled(label, Width(st.say("card.missing")), n.Text, "")
		label = ""
	}
}

// gpuName is how a line names a GPU: by its index when it has one.
func gpuName(g *view.GPU) string {
	if g.Index.Valid {
		return fmt.Sprintf("GPU %d", g.Index.V)
	}
	return "GPU"
}

// gpuIdentity is the short identity of a GPU in the list: the end of its
// serial number, else the start of its UUID, else its PCI address.
func gpuIdentity(g *view.GPU) string {
	switch {
	case g.Serial != "":
		return view.ShortSerial(g.Serial)
	case g.UUID != "":
		u := strings.TrimPrefix(g.UUID, "GPU-")
		if len(u) > 8 {
			u = u[:8]
		}
		return u
	case g.PCIBus != "":
		return g.PCIBus
	}
	return "-"
}

func (st Style) flagText(b model.Opt[bool]) (string, string) {
	switch {
	case !b.Valid:
		return "-", ""
	case b.V:
		return st.say("gpu.yes"), cBoldRed
	}
	return st.say("gpu.no"), ""
}

func countText(n model.Opt[int]) string {
	if !n.Valid {
		return "-"
	}
	return i18n.Thousands(int64(n.V))
}

// gpuStateColor returns the colour of a budget state.
func gpuStateColor(state string) string {
	switch state {
	case view.GPUFailed:
		return cBoldRed
	case view.GPUPending, view.GPULow:
		return cYellow
	case view.GPURepaired, view.GPUErrors:
		return cCyan
	}
	return cGreen
}

// gpuTable draws one line per GPU.
func (st Style) gpuTable(p *page, v *view.GPUs) {
	idx := &column{head: st.say("col.gpu"), right: true}
	serial := &column{head: st.say("col.serial")}
	xid := &column{head: st.say("col.xid", "days", v.Days)}
	remap := &column{head: st.say("col.remapped")}
	pending := &column{head: st.say("col.pending")}
	failed := &column{head: st.say("col.failed")}
	banks := &column{head: st.say("col.banks")}
	state := &column{head: st.say("col.state")}
	finding := &column{head: st.say("col.finding")}
	for i := range v.GPUs {
		g := &v.GPUs[i]
		it := "-"
		if g.Index.Valid {
			it = fmt.Sprint(g.Index.V)
		}
		idx.cells = append(idx.cells, cell{text: it})
		serial.cells = append(serial.cells, cell{text: gpuIdentity(g)})
		var xs []string
		for _, x := range g.XIDs {
			xs = append(xs, fmt.Sprintf("%dx%d", x.XID, x.Count))
		}
		xt := strings.Join(xs, " ")
		if xt == "" {
			xt = "-"
		}
		xid.cells = append(xid.cells, cell{text: xt})
		rt := "-"
		switch {
		case g.RemapCorrectable.Valid || g.RemapUncorrectable.Valid:
			rt = countText(g.RemapCorrectable) + " / " + countText(g.RemapUncorrectable)
		case g.RetiredSBE.Valid || g.RetiredDBE.Valid:
			// A GPU that retires pages has no rows to remap.
			rt = countText(g.RetiredSBE) + " / " + countText(g.RetiredDBE) + " " + st.say("gpu.pages")
		}
		remap.cells = append(remap.cells, cell{text: rt})
		pt, pc := st.flagText(g.RemapPending)
		if pc != "" {
			pc = cYellow
		}
		pending.cells = append(pending.cells, cell{text: pt, color: pc})
		ft, fc := st.flagText(g.RemapFailed)
		failed.cells = append(failed.cells, cell{text: ft, color: fc})
		bt := "-"
		if b := g.Banks; b != nil {
			bt = fmt.Sprintf("%d / %d / %d / %d / %d", b.Max, b.High, b.Partial, b.Low, b.None)
		}
		banks.cells = append(banks.cells, cell{text: bt})
		state.cells = append(state.cells, cell{text: g.StateText, color: gpuStateColor(g.State)})
		// The finding with its rule, as one cell: a grade without the rule
		// that gave it says half.
		gt, gc := "-", ""
		if len(g.Rules) > 0 {
			gt, gc = g.GradeText+" "+strings.Join(g.Rules, " "), gradeColor(string(g.Grade))
		}
		finding.cells = append(finding.cells, cell{text: gt, color: gc})
	}
	// What goes first when the terminal is narrow: the banks, which the
	// bars below show, then the columns the state already sums up.
	st.table(p, []*column{idx, serial, xid, remap, pending, failed, banks, state, finding}, []*column{banks, serial, xid, pending, failed, remap})
}

// gpuBars draws, per GPU, its banks by the spare rows they have left. The
// buckets run from max to none; the glyph of a bucket gets denser as its
// banks have fewer spare rows, so a bar of the lightest glyph is a budget
// that is untouched.
func (st Style) gpuBars(p *page, v *view.GPUs) {
	g := st.g()
	names := []string{"max", "high", "partial", "low", "none"}
	any := false
	lw := 0
	for i := range v.GPUs {
		if v.GPUs[i].Banks != nil {
			any = true
			lw = max(lw, Width(gpuName(&v.GPUs[i])))
		}
	}
	if !any {
		p.add(" " + st.dim(st.say("gpu.bars.none")))
		return
	}
	p.add(" " + st.say("gpu.bars"))
	for i := range v.GPUs {
		x := &v.GPUs[i]
		b := x.Banks
		if b == nil {
			continue
		}
		counts := []int{b.Max, b.High, b.Partial, b.Low, b.None}
		total := b.Total()
		tail := "  " + st.say("gpu.banks", "n", total)
		width := st.width() - 1 - lw - 2 - Width(tail)
		// A bucket that holds banks is drawn at least wide enough for its
		// name and count; the rest of the bar is shared by size.
		labels := make([]string, len(counts))
		need := 0
		for k, c := range counts {
			if c > 0 {
				labels[k] = fmt.Sprintf("%s %d", names[k], c)
				need += Width(labels[k]) + 3
			}
		}
		spare := max(width-need-1, 0)
		var bar strings.Builder
		bar.WriteString(g.sep)
		for k, c := range counts {
			if c == 0 || total == 0 {
				continue
			}
			fill := spare * c / total
			seg := labels[k] + " " + strings.Repeat(g.shade[k], fill+1)
			bar.WriteString(st.paint(heatColor(k), seg) + g.sep)
		}
		p.add(" " + padRight(gpuName(x), lw) + "  " + bar.String() + st.dim(tail))
	}
	legend := make([]string, len(names))
	for k, name := range names {
		legend[k] = st.paint(heatColor(k), g.shade[k]) + " " + name
	}
	for _, line := range flow(legend, "   ", st.width()-1) {
		p.add(" " + st.dim(line))
	}
}

// gpuDetail writes what the list has no room for: the finding of a GPU
// with what it rests on, the step that follows, and notes. With detail set
// it also lists the counts behind the budget and the latest Xid records.
func (st Style) gpuDetail(p *page, g *view.GPU, detail bool) {
	if !detail && len(g.Evidence) == 0 && g.Next == nil && len(g.Notes) == 0 {
		return
	}
	p.blank()
	head := []string{st.bold(gpuName(g))}
	if g.Serial != "" {
		head = append(head, "S/N "+g.Serial)
	}
	if g.UUID != "" && (detail || g.Serial == "") {
		head = append(head, g.UUID)
	}
	if g.Model != "" {
		head = append(head, g.Model)
	}
	if g.PCIBus != "" {
		head = append(head, "PCI "+g.PCIBus)
	}
	if !g.ReadAt.IsZero() {
		head = append(head, st.say("gpu.read", "when", g.ReadAt.UTC().Format("2006-01-02 15:04 UTC")))
	}
	for _, line := range flow(head, "  ", st.width()-1) {
		p.add(" " + line)
	}
	labels := []string{st.say("card.finding"), st.say("card.evidence"), st.say("card.next"), st.say("gpu.note"), st.say("gpu.state")}
	if detail {
		labels = append(labels, st.say("gpu.rows"), st.say("gpu.pages"), st.say("gpu.ecc"), st.say("gpu.xid"))
	}
	lw := 0
	for _, l := range labels {
		lw = max(lw, Width(l))
	}
	lw += 2
	put := func(label, text, color string) { p.labelled("  "+label, lw, text, color) }

	put(st.say("gpu.state"), g.StateText, gpuStateColor(g.State))
	if len(g.Rules) > 0 {
		put(st.say("card.finding"), g.GradeText+"  "+strings.Join(g.Rules, " "), gradeColor(string(g.Grade)))
	}
	label := st.say("card.evidence")
	for _, e := range g.Evidence {
		put(label, e.Text, "")
		label = ""
	}
	if detail {
		unknown := st.say("gpu.unknown")
		or := func(n model.Opt[int]) string {
			if !n.Valid {
				return unknown
			}
			return i18n.Thousands(int64(n.V))
		}
		if g.RemapCorrectable.Valid || g.RemapUncorrectable.Valid {
			put(st.say("gpu.rows"), st.say("gpu.rows.text", "ce", or(g.RemapCorrectable), "ue", or(g.RemapUncorrectable)), "")
		}
		if g.RetiredSBE.Valid || g.RetiredDBE.Valid {
			put(st.say("gpu.pages"), st.say("gpu.pages.text", "sbe", or(g.RetiredSBE), "dbe", or(g.RetiredDBE)), "")
		}
		if g.DRAMCorrectable.Valid || g.DRAMUncorrectable.Valid || g.DRAMCorrectableVolatile.Valid || g.DRAMUncorrectableVolatile.Valid {
			put(st.say("gpu.ecc"), st.say("gpu.ecc.text", "ce", or(g.DRAMCorrectable), "ue", or(g.DRAMUncorrectable),
				"vce", or(g.DRAMCorrectableVolatile), "vue", or(g.DRAMUncorrectableVolatile)), "")
		}
		label = st.say("gpu.xid")
		for _, e := range g.Events {
			when := e.At.UTC().Format("2006-01-02 15:04 UTC")
			if e.TimeQuality == model.TimeRelative {
				when = fmt.Sprintf("boot+%ds", e.At.Unix())
			}
			text := fmt.Sprintf("%s  %d", when, e.XID)
			if e.Text != "" {
				text += "  " + e.Text
			}
			put(label, text, "")
			label = ""
		}
		if more := g.XIDTotal - len(g.Events); more > 0 {
			put(label, st.say("gpu.xid.more", "n", more), "")
		}
	}
	if g.Next != nil {
		put(st.say("card.next"), g.Next.Text, "")
	}
	label = st.say("gpu.note")
	for _, n := range g.Notes {
		put(label, n.Text, "")
		label = ""
	}
}
