package term

import (
	"io"
	"strings"

	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/view"
)

// Widest a maker and a part number are drawn in the table of the
// inventory. The line below a module has the serial number, and --json
// has every field in full.
const (
	maxMakerWidth = 18
	maxPartWidth  = 22
)

// Inventory draws the DIMMs of a host one per line, and below the table
// what the operating system and the BMC each recorded.
func Inventory(w io.Writer, v *view.Inventory, st Style) error {
	p := &page{st: st}
	fields := []string{st.say("ov.host", "name", st.bold(v.Host))}
	if v.Machine != "" {
		fields = append(fields, v.Machine)
	}
	if v.BIOSVersion != "" {
		fields = append(fields, "BIOS "+v.BIOSVersion)
	}
	if b := v.BMC; b != nil && b.Firmware != "" {
		fields = append(fields, "BMC "+b.Firmware)
	}
	for _, line := range flow(fields, "  ", st.width()) {
		p.add(line)
	}
	for _, t := range v.Lead {
		for _, line := range st.Wrap(t.Text, 0) {
			p.add(line)
		}
	}

	if len(v.Rows) > 0 {
		p.blank()
		inventoryTable(p, v, st)
	}
	if len(v.Legend)+len(v.Notes) > 0 {
		p.blank()
		for _, t := range append(append([]view.Text{}, v.Legend...), v.Notes...) {
			for _, l := range st.Wrap(t.Text, 1) {
				p.add(st.dim(l))
			}
		}
	}
	if s := v.Sides; s != nil {
		p.blank()
		for _, t := range s.Lead {
			for _, l := range st.Wrap(t.Text, 1) {
				p.add(l)
			}
		}
		item := func(text string) {
			for i, l := range st.Wrap("- "+text, 3) {
				if i > 0 {
					l = "  " + l
				}
				p.add(l)
			}
		}
		for _, r := range s.Rows {
			item(r.Text.Text)
			if r.Count != nil && r.Count.Text.Text != r.Text.Text {
				item(r.Count.Text.Text)
			}
		}
		for _, g := range s.Candidates {
			p.blank()
			for _, l := range st.Wrap(g.Head.Text, 1) {
				p.add(l)
			}
			for _, t := range g.Items {
				item(t.Text)
			}
		}
	}
	return p.writeTo(w)
}

// inventoryTable draws the table of the modules. The RAS column moves to
// the line below a module when the table is wider than the terminal.
func inventoryTable(p *page, v *view.Inventory, st Style) {
	c := v.Columns
	count := func(n model.Opt[int64]) string {
		if !n.Valid {
			return "-"
		}
		return i18n.Thousands(n.V)
	}
	cell := func(s string) string {
		if s == "" {
			return "-"
		}
		return s
	}
	type line struct {
		slot, serial, maker, part, size, org, speed, ras, os, bmc string
		row                                                       *view.InventoryRow
	}
	lines := make([]line, len(v.Rows))
	wSlot, wSerial, wMaker, wPart := Width(c.Slot), Width(c.Serial), Width(c.Maker), Width(c.Part)
	wSize, wOrg, wSpeed, wRAS := Width(c.Size), Width(c.Org), Width(c.Speed), Width(c.RAS)
	wOS, wBMC := Width(c.CEOS), Width(c.CEBMC)
	for i := range v.Rows {
		r := &v.Rows[i]
		l := line{slot: r.Slot, row: r}
		if r.Populated {
			l.serial = cell(r.Serial)
			l.maker = cell(truncate(r.Maker, maxMakerWidth, st.g().ellipsis))
			l.part = cell(truncate(r.PartNumber, maxPartWidth, st.g().ellipsis))
			l.size, l.org, l.speed, l.ras = cell(r.Size), cell(r.Org), cell(r.Speed), cell(r.RAS)
			l.os, l.bmc = count(r.CEOS), count(r.CEBMC)
		} else {
			l.serial = c.Empty
		}
		lines[i] = l
		wSlot, wSerial, wMaker, wPart = max(wSlot, Width(l.slot)), max(wSerial, Width(l.serial)), max(wMaker, Width(l.maker)), max(wPart, Width(l.part))
		wSize, wOrg, wSpeed, wRAS = max(wSize, Width(l.size)), max(wOrg, Width(l.org)), max(wSpeed, Width(l.speed)), max(wRAS, Width(l.ras))
		wOS, wBMC = max(wOS, Width(l.os)), max(wBMC, Width(l.bmc))
	}
	// The RAS column is drawn when the modules differ in it. What holds for
	// every module alike stands once above the table.
	fixed := func() int {
		return 1 + wSlot + 2 + wSerial + 2 + wMaker + 2 + wPart + 2 + wSize + 2 + wOrg + 2 + wSpeed + 2 + wOS + 2 + wBMC
	}
	// A table wider than the terminal gives up width of the maker first and
	// of the part number next.
	for _, w := range []*int{&wMaker, &wPart} {
		if over := fixed() - st.width(); over > 0 {
			*w = max(*w-over, 8)
		}
	}
	differ := false
	for _, l := range lines {
		differ = differ || (l.row.Populated && l.row.RAS != v.RASCommon)
	}
	rasBelow := differ && fixed()+2+wRAS > st.width()
	draw := func(l line, head bool) string {
		out := " " + padRight(l.slot, wSlot) + "  " + padRight(l.serial, wSerial) + "  " + padRight(truncate(l.maker, wMaker, st.g().ellipsis), wMaker) + "  " +
			padRight(truncate(l.part, wPart, st.g().ellipsis), wPart) + "  " + padLeft(l.size, wSize) + "  " + padRight(l.org, wOrg) + "  " + padRight(l.speed, wSpeed)
		if differ && !rasBelow {
			out += "  " + padRight(l.ras, wRAS)
		}
		out += "  " + padLeft(l.os, wOS) + "  " + padLeft(l.bmc, wBMC)
		if head {
			return st.dim(out)
		}
		return strings.TrimRight(out, " ")
	}
	p.add(draw(line{slot: c.Slot, serial: c.Serial, maker: c.Maker, part: c.Part, size: c.Size, org: c.Org, speed: c.Speed, ras: c.RAS, os: c.CEOS, bmc: c.CEBMC}, true))
	indent := 1 + wSlot + 2
	for _, l := range lines {
		p.add(draw(l, false))
		if rasBelow && l.row.RAS != "" {
			for _, s := range st.Wrap(c.RAS+": "+l.row.RAS, indent) {
				p.add(st.dim(s))
			}
		}
		for _, t := range l.row.Lines {
			for _, s := range st.Wrap(t.Text, indent) {
				p.add(st.dim(s))
			}
		}
	}
}
