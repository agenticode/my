package report

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/agenticode/my/internal/decode/spd"
	"github.com/agenticode/my/internal/view"
)

// The section Module SPD: what each module says about itself in its SPD
// EEPROM, as read at the scan, before the Memory Explorer. It holds no
// finding and changes no grade. A report of a case without an SPD reading
// has no such section, and the sections after it keep their numbers; when
// the scan looked for the SPD and read none, the reason stands in the
// section on data quality.

// spdSheet styles the section. It goes into a report that has the section
// only, so that the report of a case without an SPD reading is the same
// file it was before the section existed.
//
//go:embed spd.css
var spdSheet string

func (doc *Document) spdStyle() string {
	if !doc.hasSPD() {
		return ""
	}
	return spdSheet
}

// hasSPD tells a report with the section Module SPD.
func (doc *Document) hasSPD() bool { return doc.SPD != nil && doc.SPD.Read > 0 }

// spdSay renders a message of the SPD views.
func (doc *Document) spdSay(code string, kv ...any) string {
	var args map[string]any
	if len(kv) > 0 {
		args = make(map[string]any, len(kv)/2)
		for i := 0; i+1 < len(kv); i += 2 {
			args[kv[i].(string)] = kv[i+1]
		}
	}
	return view.SPDMessages.Text(doc.Lang, code, args)
}

// spdPart is the section Module SPD when the case holds a reading, else
// nothing.
func (doc *Document) spdPart() any {
	if !doc.hasSPD() {
		return nil
	}
	return doc.spdSection()
}

func dashed(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// spdMaker names the maker of a module: the name with its JEDEC code, the
// code alone, or the name a source gave.
func spdMaker(m view.SPDModule) string {
	d := m.SPD
	switch {
	case d.ModuleMaker != "" && spd.MakerName(d.ModuleMaker) != "":
		return spd.MakerLabel(d.ModuleMaker)
	case d.ModuleMaker != "" && m.MakerName != "":
		return m.MakerName + " (JEDEC " + d.ModuleMaker + ")"
	case d.ModuleMaker != "":
		return d.ModuleMaker
	}
	return m.MakerName
}

func (doc *Document) spdSection() *node {
	v := doc.SPD
	s := doc.section(doc.secSPD())
	s.add(el("p", A{"class", "lead"}, doc.say("spd.lead")))
	s.add(el("p", v.Head.Text))
	if sh := v.Shared; sh.Count > 0 {
		var parts []string
		if sh.MakerID != "" || sh.Maker != "" {
			parts = append(parts, spdMaker(view.SPDModule{MakerName: sh.Maker, SPD: spd.Decoded{ModuleMaker: sh.MakerID}}))
		}
		if sh.PartNumber != "" {
			parts = append(parts, sh.PartNumber)
		}
		if sh.MemoryType != "" {
			kind := strings.TrimSpace(sh.MemoryType + " " + sh.ModuleType)
			if sh.SizeBytes > 0 {
				kind += " " + view.SizeText(sh.SizeBytes)
			}
			if sh.Org != "" {
				kind += " " + sh.Org
			}
			parts = append(parts, kind)
		}
		if sh.DieMbit > 0 {
			parts = append(parts, doc.spdSay("spd.dies", "die", view.DieText(sh.DieMbit)))
		}
		if len(parts) > 0 {
			s.add(el("p", A{"class", "spd-shared"}, fmt.Sprintf("%d x ", sh.Count)+strings.Join(parts, ", ")))
		}
	}
	t, body := table("spd", doc.spdSay("spd.col.slot"), doc.spdSay("spd.col.date"), doc.spdSay("spd.col.loc"), doc.spdSay("spd.col.serial"),
		doc.spdSay("spd.col.org"), doc.spdSay("spd.col.part"), doc.spdSay("spd.col.maker"), doc.spdSay("spd.col.finding"))
	var lines []string
	for _, m := range v.Modules {
		d := m.SPD
		row := el("tr", el("td", A{"class", "id"}, m.Place()))
		if spd.Read(m.State) && d.HasSerial() {
			date := d.Date
			if date == "" {
				date = d.DateBytes
			}
			row.add(el("td", A{"class", "mono"}, dashed(date)), el("td", A{"class", "mono"}, dashed(d.Location)), el("td", A{"class", "mono"}, dashed(d.Serial)),
				el("td", dashed(d.Organisation)), el("td", A{"class", "mono"}, dashed(d.PartNumber)), el("td", dashed(spdMaker(m))), el("td", dashed(m.GradeText)))
		} else {
			text := "-"
			if m.StateText != nil {
				text = m.StateText.Text
			}
			row.add(el("td", A{"colspan", "7"}, text))
		}
		body.add(row)
		if m.StateText != nil && spd.Read(m.State) && d.HasSerial() {
			lines = append(lines, m.Place()+": "+m.StateText.Text)
		}
		for _, x := range m.Differences {
			lines = append(lines, x.Text)
		}
	}
	s.add(el("div", A{"class", "scroll"}, t))
	for _, n := range v.Notes {
		lines = append(lines, n.Text)
	}
	if len(lines) > 0 {
		ul := el("ul", A{"class", "spd-lines"})
		for _, l := range lines {
			ul.add(el("li", l))
		}
		s.add(ul)
	}
	if l := v.Lots; l != nil && len(l.Rows) > 0 {
		s.add(el("h3", doc.spdSay("lots.title")))
		s.add(doc.lotsTable(l))
	}
	if doc.Audience == FA {
		s.add(el("h3", doc.say("spd.fa")))
		for _, m := range v.Modules {
			s.add(doc.spdDetail(m))
		}
	}
	return s
}

// lotsTable draws the table of date codes, its lines and the counts by
// organisation.
func (doc *Document) lotsTable(l *view.Lots) *node {
	box := el("div", A{"class", "spd-lots"})
	heads := []string{doc.spdSay("lots.col.maker"), doc.spdSay("lots.col.part"), doc.spdSay("lots.col.date"), ">" + doc.spdSay("lots.col.dimms")}
	if l.Findings {
		where := doc.spdSay("lots.col.slots")
		if len(l.Hosts) > 1 {
			where = doc.spdSay("lots.col.hosts")
		}
		heads = append(heads, ">"+doc.spdSay("lots.col.finding"), ">"+doc.spdSay("lots.col.ue"), where)
	}
	t, body := table("lots", heads...)
	for _, r := range l.Rows {
		date := r.MfgDate
		switch {
		case date == "":
			date = doc.spdSay("lots.no_date")
		case r.DateSource == "bmc":
			date = doc.spdSay("lots.from_bmc", "date", date)
		}
		row := el("tr", el("td", dashed(view.LotMaker(r))), el("td", A{"class", "mono"}, dashed(r.PartNumber)), el("td", A{"class", "mono"}, date), numCell(int64(r.DIMMs)))
		if l.Findings {
			var where []string
			if len(l.Hosts) > 1 {
				for _, h := range r.Hosts {
					if h.WithFinding > 0 {
						where = append(where, doc.spdSay("lots.host_count", "host", h.Host, "n", h.WithFinding))
					}
				}
			} else {
				where = r.Slots
			}
			row.add(numCell(int64(r.WithFinding)), numCell(int64(r.WithUE)), el("td", strings.Join(where, " ")))
		}
		body.add(row)
	}
	box.add(el("div", A{"class", "scroll"}, t))
	if l.Why != nil {
		box.add(el("p", l.Why.Text))
	}
	for _, x := range l.Lines {
		box.add(el("p", x.Text))
	}
	if len(l.ByOrganisation) > 1 {
		heads := []string{doc.spdSay("lots.col.org"), ">" + doc.spdSay("lots.col.dimms")}
		if l.Findings {
			heads = append(heads, ">"+doc.spdSay("lots.col.finding"), ">"+doc.spdSay("lots.col.ue"))
		}
		t, body := table("lots-org", heads...)
		for _, o := range l.ByOrganisation {
			row := el("tr", el("td", o.Organisation), numCell(int64(o.DIMMs)))
			if l.Findings {
				row.add(numCell(int64(o.WithFinding)), numCell(int64(o.WithUE)))
			}
			body.add(row)
		}
		box.add(el("h4", doc.spdSay("lots.title_org")), el("div", A{"class", "scroll"}, t))
	}
	return box
}

// spdDetail is what the report for failure analysis adds per module: the
// fields of my spd --detail and the bytes as a hex dump.
func (doc *Document) spdDetail(m view.SPDModule) *node {
	d := m.SPD
	dl := el("dl", A{"class", "spd-fields"})
	field := func(k, v string) {
		if v != "" {
			dl.add(el("div", el("dt", k), el("dd", v)))
		}
	}
	field(doc.say("spd.f.origin"), m.Origin)
	field(doc.say("spd.f.state"), m.State+" "+strings.TrimSpace(m.Detail))
	if d.Revision != "" {
		field(doc.say("spd.f.revision"), d.Revision)
	}
	for _, c := range d.CRCs {
		if c.State != spd.CRCMissing {
			field(fmt.Sprintf("CRC %d-%d", c.From, c.To), fmt.Sprintf("0x%04X / 0x%04X (%s)", c.Stored, c.Computed, c.State))
		}
	}
	if d.DiesPerPackage.Valid {
		field(doc.say("spd.f.package"), doc.spdSay("spd.package", "n", d.DiesPerPackage.V, "kind", d.Package))
	}
	if d.DieMbit.Valid {
		field(doc.say("spd.f.die"), view.DieText(d.DieMbit.V))
	}
	if d.DRAMMaker != "" {
		field(doc.say("spd.f.dram"), spd.MakerLabel(d.DRAMMaker))
	} else if d.DRAMMakerName != "" {
		field(doc.say("spd.f.dram"), d.DRAMMakerName)
	}
	field(doc.say("spd.f.stepping"), d.DRAMStepping)
	field(doc.say("spd.f.module_revision"), d.ModuleRevision)
	if m.Bus != "" {
		field(doc.say("spd.f.bus"), fmt.Sprintf("%s 0x%02x %s", m.Bus, m.Address, m.Driver))
	}
	switch m.Tie {
	case "serial":
		field(doc.say("spd.f.tie"), doc.spdSay("spd.tie.serial"))
	case "serial-reversed":
		field(doc.say("spd.f.tie"), doc.spdSay("spd.tie.reversed"))
	}
	box := el("div", A{"class", "spd-module keep"}, el("h4", m.Place()), dl)
	if raw := doc.spdRaw[m.Origin]; len(raw) > 0 {
		box.add(details(doc.say("spd.hex", "n", len(raw)), el("pre", A{"class", "hex"}, hexDump(raw))))
	}
	return box
}

// hexDump writes bytes as rows of an offset and 16 bytes.
func hexDump(b []byte) string {
	var s strings.Builder
	for off := 0; off < len(b); off += 16 {
		fmt.Fprintf(&s, "%04x:", off)
		for _, c := range b[off:min(off+16, len(b))] {
			fmt.Fprintf(&s, " %02x", c)
		}
		s.WriteString("\n")
	}
	return s.String()
}

// spdQuality is the paragraph of data quality when the scan looked for the
// SPD and read none: the reason, what was read beside it and the step.
func (doc *Document) spdQuality() []any {
	v := doc.SPD
	if v == nil || v.Read > 0 || !v.Looked || v.Reason == nil {
		return nil
	}
	text := v.Reason.Text.Text
	for _, f := range v.Reason.Facts {
		text += " " + f.Text
	}
	if v.Reason.Step != nil {
		text += " " + v.Reason.Step.Text
	}
	return []any{el("h3", doc.say("sec.spd")), el("p", A{"class", "spd-reason"}, v.Head.Text+". "+text)}
}
