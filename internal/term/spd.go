package term

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/agenticode/my/internal/decode/spd"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/view"
)

// sayS renders a message of the SPD catalog.
func (st Style) sayS(code string, kv ...any) string {
	var args map[string]any
	if len(kv) > 0 {
		args = make(map[string]any, len(kv)/2)
		for i := 0; i+1 < len(kv); i += 2 {
			args[kv[i].(string)] = kv[i+1]
		}
	}
	return view.SPDMessages.Text(st.lang(), code, args)
}

// SPD draws what each memory module of a host says about itself in its
// SPD: the host, what all modules share, one row per module, the
// differences to SMBIOS, and the table of date codes. When nothing was
// read it says why and what the reader can do.
func SPD(w io.Writer, v *view.SPD, st Style) error {
	p := &page{st: st}
	fields := []string{st.say("ov.host", "name", st.bold(v.Host))}
	if v.System != "" {
		fields = append(fields, v.System)
	}
	fields = append(fields, v.Head.Text)
	for _, line := range flow(fields, "  ", st.width()) {
		p.add(line)
	}
	if r := v.Reason; r != nil {
		for _, t := range append(append([]view.Text{r.Text}, r.Facts...), stepOf(r)...) {
			for _, l := range st.Wrap(t.Text, 1) {
				p.add(l)
			}
		}
		return p.writeTo(w)
	}
	for _, l := range flow(sharedFields(v.Shared, st), "  ", st.width()) {
		p.add(l)
	}
	if len(v.Modules) > 0 {
		p.blank()
		st.moduleTable(p, v.Modules, v.Shared, false)
	}
	if len(v.Notes) > 0 {
		p.blank()
		for _, t := range v.Notes {
			for _, l := range st.Wrap(t.Text, 1) {
				p.add(st.dim(l))
			}
		}
	}
	if v.Lots != nil && len(v.Lots.Rows) > 0 {
		p.blank()
		p.add(" " + st.bold(st.sayS("lots.title")))
		st.lotsTable(p, v.Lots, len(v.Lots.ByOrganisation) > 1)
	}
	return p.writeTo(w)
}

func stepOf(r *view.SPDReason) []view.Text {
	if r.Step == nil {
		return nil
	}
	return []view.Text{*r.Step}
}

// sharedFields are what every module has in common: "16 x Micron
// Technology (JEDEC bank 1, 0x2C)", "MTC40F2046S1RC48BA1", "DDR5 RDIMM 64
// GB 2Rx4", "16 Gb dies".
func sharedFields(s view.SPDShared, st Style) []string {
	if s.Count == 0 {
		return nil
	}
	var fields []string
	maker := ""
	switch {
	case s.MakerID != "" && spd.MakerName(s.MakerID) != "":
		maker = spd.MakerLabel(s.MakerID)
	case s.MakerID != "" && s.Maker != "":
		maker = s.Maker + " (JEDEC " + s.MakerID + ")"
	case s.MakerID != "":
		maker = s.MakerID
	default:
		maker = s.Maker
	}
	if maker != "" {
		fields = append(fields, maker)
	}
	if s.PartNumber != "" {
		fields = append(fields, s.PartNumber)
	}
	if s.MemoryType != "" {
		kind := strings.TrimSpace(s.MemoryType + " " + s.ModuleType)
		if s.SizeBytes > 0 {
			kind += " " + view.SizeText(s.SizeBytes)
		}
		if s.Org != "" {
			kind += " " + s.Org
		}
		fields = append(fields, kind)
	}
	if s.DieMbit > 0 {
		fields = append(fields, st.sayS("spd.dies", "die", view.DieText(s.DieMbit)))
	}
	if len(fields) == 0 {
		return nil
	}
	// The count stands before the first field: "16 x Micron Technology".
	fields[0] = fmt.Sprintf("%d x %s", s.Count, fields[0])
	return fields
}

// makerOf names the maker of a module.
func makerOf(m view.SPDModule) string {
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

// moduleTable draws one row per module. The columns of organisation, part
// number and maker are drawn when the modules differ in them; what they
// share stands once above. A reading that is not ok has its state on the
// line below.
func (st Style) moduleTable(p *page, mods []view.SPDModule, shared view.SPDShared, files bool) {
	cell := func(s string) string {
		if s == "" {
			return "-"
		}
		return s
	}
	type row struct {
		place, date, loc, serial, org, part, maker, finding string
		m                                                   *view.SPDModule
	}
	head := row{place: st.sayS("spd.col.slot"), date: st.sayS("spd.col.date"), loc: st.sayS("spd.col.loc"), serial: st.sayS("spd.col.serial"),
		org: st.sayS("spd.col.org"), part: st.sayS("spd.col.part"), maker: st.sayS("spd.col.maker"), finding: st.sayS("spd.col.finding")}
	if files {
		head.place = st.sayS("spd.col.file")
	}
	var rows []row
	orgs, parts, makers := map[string]bool{}, map[string]bool{}, map[string]bool{}
	tied := false
	for i := range mods {
		m := &mods[i]
		d := m.SPD
		r := row{place: m.Place(), m: m}
		if files {
			r.place = filepath.Base(m.Origin)
		}
		if spd.Read(m.State) {
			date := d.Date
			if date == "" && d.DateBytes != "" {
				date = d.DateBytes
			}
			r.date, r.loc, r.serial, r.org, r.part, r.maker = cell(date), cell(d.Location), cell(d.Serial), cell(d.Organisation), cell(d.PartNumber), cell(makerOf(*m))
			// A field the bytes did not reach makes no difference between
			// the modules.
			for _, x := range []struct {
				v string
				m map[string]bool
			}{{r.org, orgs}, {r.part, parts}, {r.maker, makers}} {
				if x.v != "-" {
					x.m[x.v] = true
				}
			}
		} else {
			r.date, r.loc, r.serial, r.org, r.part, r.maker = "-", "-", "-", "-", "-", "-"
		}
		r.finding = cell(m.GradeText)
		tied = tied || m.Slot != ""
		rows = append(rows, r)
	}
	if !tied && !files {
		head.place = st.sayS("spd.col.place")
	}
	showOrg := len(orgs) > 1 || shared.Org == "" && len(orgs) > 0
	showPart := len(parts) > 1 || shared.PartNumber == "" && len(parts) > 0
	showMaker := len(makers) > 1 || shared.MakerID == "" && shared.Maker == "" && len(makers) > 0
	widths := func(get func(row) string) int {
		n := 0
		for _, r := range append([]row{head}, rows...) {
			n = max(n, Width(get(r)))
		}
		return n
	}
	wPlace, wDate, wLoc, wSerial := widths(func(r row) string { return r.place }), widths(func(r row) string { return r.date }),
		widths(func(r row) string { return r.loc }), widths(func(r row) string { return r.serial })
	wOrg, wPart, wMaker := widths(func(r row) string { return r.org }), widths(func(r row) string { return r.part }), widths(func(r row) string { return r.maker })
	total := func() int {
		n := 1 + wPlace + 2 + wDate + 2 + wLoc + 2 + wSerial
		if showOrg {
			n += 2 + wOrg
		}
		if showPart {
			n += 2 + wPart
		}
		if showMaker {
			n += 2 + wMaker
		}
		if tied {
			n += 2 + widths(func(r row) string { return r.finding })
		}
		return n
	}
	// A table wider than the terminal gives up the maker first, then the
	// width of the part number.
	if over := total() - st.width(); over > 0 && showMaker {
		wMaker = max(wMaker-over, 8)
	}
	if over := total() - st.width(); over > 0 && showPart {
		wPart = max(wPart-over, 8)
	}
	draw := func(r row, h bool) string {
		out := " " + padRight(r.place, wPlace) + "  " + padRight(r.date, wDate) + "  " + padRight(r.loc, wLoc) + "  " + padRight(r.serial, wSerial)
		if showOrg {
			out += "  " + padRight(r.org, wOrg)
		}
		if showPart {
			out += "  " + padRight(truncate(r.part, wPart, st.g().ellipsis), wPart)
		}
		if showMaker {
			out += "  " + padRight(truncate(r.maker, wMaker, st.g().ellipsis), wMaker)
		}
		if tied {
			f := r.finding
			if !h && r.m.Grade != "" {
				f = st.paint(gradeColor(string(r.m.Grade)), f)
			}
			out += "  " + f
		}
		if h {
			return st.dim(out)
		}
		return strings.TrimRight(out, " ")
	}
	p.add(draw(head, true))
	indent := 1 + wPlace + 2
	for _, r := range rows {
		var below []string
		switch {
		case r.m.StateText != nil && (!spd.Read(r.m.State) || !r.m.SPD.HasSerial()):
			// A reading without fields: its state stands in their place.
			lines := st.Wrap(r.m.StateText.Text, indent)
			lines[0] = " " + padRight(r.place, wPlace) + "  " + strings.TrimLeft(lines[0], " ")
			for _, l := range lines {
				p.add(strings.TrimRight(l, " "))
			}
		default:
			p.add(draw(r, false))
			if r.m.StateText != nil {
				below = append(below, r.m.StateText.Text)
			}
		}
		for _, t := range r.m.Differences {
			below = append(below, t.Text)
		}
		if st.Detail {
			below = append(below, st.moduleDetail(r.m)...)
		}
		for _, b := range below {
			for _, l := range st.Wrap(b, indent) {
				p.add(st.dim(l))
			}
		}
	}
}

// moduleDetail is what --detail adds to a module: the SPD revision and
// where the bytes came from, the CRCs, the package, the DRAM maker and
// stepping, the module revision, bus, address and driver, and the tie.
func (st Style) moduleDetail(m *view.SPDModule) []string {
	d := m.SPD
	var out []string
	if d.Revision != "" {
		out = append(out, st.sayS("spd.detail.revision", "rev", d.Revision, "bytes", m.Bytes, "origin", m.Origin))
	}
	if m.Form != "eeprom" {
		detail := m.Detail
		if detail == "" {
			detail = "-"
		}
		out = append(out, st.sayS("spd.detail.form", "form", m.Form, "detail", detail))
	}
	for _, c := range d.CRCs {
		if c.State == spd.CRCMissing {
			continue
		}
		out = append(out, st.sayS("spd.detail.crc", "from", c.From, "to", c.To, "stored", fmt.Sprintf("0x%04X", c.Stored), "computed", fmt.Sprintf("0x%04X", c.Computed), "state", c.State))
	}
	if d.DiesPerPackage.Valid && d.Package != "" {
		out = append(out, st.sayS("spd.package", "n", d.DiesPerPackage.V, "kind", d.Package))
	}
	if d.DRAMStepping != "" || d.DRAMMaker != "" {
		maker := d.DRAMMaker
		if d.DRAMMakerName != "" && maker != "" {
			maker = spd.MakerLabel(maker)
		} else if maker == "" {
			maker = cellText(d.DRAMMakerName)
		}
		out = append(out, st.sayS("spd.detail.dram", "maker", maker, "stepping", cellText(d.DRAMStepping), "rev", cellText(d.ModuleRevision)))
	}
	if m.Bus != "" {
		out = append(out, st.sayS("spd.detail.bus", "bus", m.Bus, "addr", fmt.Sprintf("%02x", m.Address), "driver", cellText(m.Driver)))
	}
	switch m.Tie {
	case "serial":
		out = append(out, st.sayS("spd.detail.tie", "tie", st.sayS("spd.tie.serial")))
	case "serial-reversed":
		out = append(out, st.sayS("spd.detail.tie", "tie", st.sayS("spd.tie.reversed")))
	}
	return out
}

func cellText(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// Lots draws the table of date codes of the view lots.
func Lots(w io.Writer, l *view.Lots, st Style) error {
	p := &page{st: st}
	head := st.bold(st.sayS("lots.title"))
	if len(l.Hosts) > 0 {
		head += "  " + st.sayS("lots.hosts", "n", len(l.Hosts), "hosts", strings.Join(l.Hosts, ", "))
	}
	p.add(head)
	if len(l.Rows) == 0 {
		p.add(" " + st.sayS("lots.none"))
		return p.writeTo(w)
	}
	st.lotsTable(p, l, true)
	return p.writeTo(w)
}

// lotsTable draws the rows of date codes, the lines below them and, when
// asked, the counts by organisation. The maker and the part number are
// columns when the rows differ in them and stand once above otherwise.
func (st Style) lotsTable(p *page, l *view.Lots, orgs bool) {
	makers, parts := map[string]bool{}, map[string]bool{}
	for _, r := range l.Rows {
		makers[view.LotMaker(r)], parts[r.PartNumber] = true, true
	}
	showMaker, showPart := len(makers) > 1, len(parts) > 1
	if !showMaker || !showPart {
		var fields []string
		if !showMaker {
			for m := range makers {
				fields = append(fields, m)
			}
		}
		if !showPart {
			for pn := range parts {
				fields = append(fields, pn)
			}
		}
		for _, line := range flow(fields, "  ", st.width()-1) {
			p.add(" " + st.dim(line))
		}
	}
	several := len(l.Hosts) > 1
	type row struct{ maker, part, date, dimms, finding, ue, where string }
	head := row{maker: st.sayS("lots.col.maker"), part: st.sayS("lots.col.part"), date: st.sayS("lots.col.date"), dimms: st.sayS("lots.col.dimms"),
		finding: st.sayS("lots.col.finding"), ue: st.sayS("lots.col.ue"), where: st.sayS("lots.col.slots")}
	if several {
		head.where = st.sayS("lots.col.hosts")
	}
	var rows []row
	for _, r := range l.Rows {
		date := r.MfgDate
		switch {
		case date == "":
			date = st.sayS("lots.no_date")
		case r.DateSource == "bmc":
			date = st.sayS("lots.from_bmc", "date", date)
		}
		x := row{maker: view.LotMaker(r), part: r.PartNumber, date: date, dimms: i18n.Thousands(int64(r.DIMMs)),
			finding: i18n.Thousands(int64(r.WithFinding)), ue: i18n.Thousands(int64(r.WithUE))}
		if several {
			var hs []string
			for _, h := range r.Hosts {
				if h.WithFinding > 0 {
					hs = append(hs, st.sayS("lots.host_count", "host", h.Host, "n", h.WithFinding))
				}
			}
			x.where = strings.Join(hs, ", ")
		} else {
			x.where = strings.Join(r.Slots, " ")
		}
		rows = append(rows, x)
	}
	w := func(get func(row) string) int {
		n := 0
		for _, r := range append([]row{head}, rows...) {
			n = max(n, Width(get(r)))
		}
		return n
	}
	wMaker, wPart, wDate := w(func(r row) string { return r.maker }), w(func(r row) string { return r.part }), w(func(r row) string { return r.date })
	wDimms, wFind, wUE := w(func(r row) string { return r.dimms }), w(func(r row) string { return r.finding }), w(func(r row) string { return r.ue })
	fixed := func() int {
		n := 1 + wDate + 2 + wDimms
		if showMaker {
			n += wMaker + 2
		}
		if showPart {
			n += wPart + 2
		}
		if l.Findings {
			n += 2 + wFind + 2 + wUE + 2
		}
		return n
	}
	// The head of the last column is the widest thing it holds at first;
	// the slots and hosts flow below their column when they do not fit.
	need := func() int {
		if l.Findings {
			return fixed() + Width(head.where)
		}
		return fixed()
	}
	if over := need() - st.width(); over > 0 && showMaker {
		wMaker = max(wMaker-over, 8)
	}
	if over := need() - st.width(); over > 0 && showPart {
		wPart = max(wPart-over, 8)
	}
	draw := func(r row, h bool) string {
		out := " "
		if showMaker {
			out += padRight(truncate(r.maker, wMaker, st.g().ellipsis), wMaker) + "  "
		}
		if showPart {
			out += padRight(truncate(r.part, wPart, st.g().ellipsis), wPart) + "  "
		}
		out += padRight(r.date, wDate) + "  " + padLeft(r.dimms, wDimms)
		if l.Findings {
			out += "  " + padLeft(r.finding, wFind) + "  " + padLeft(r.ue, wUE) + "  " + r.where
		}
		if h {
			return st.dim(out)
		}
		return strings.TrimRight(out, " ")
	}
	p.add(draw(head, true))
	indent := fixed()
	for _, r := range rows {
		line := draw(r, false)
		if Width(line) <= st.width() {
			p.add(line)
			continue
		}
		// The slots or hosts flow onto the lines below their column.
		where := r.where
		r.where = ""
		p.add(draw(r, false))
		for _, l := range st.Wrap(where, indent) {
			p.add(l)
		}
	}
	if l.Why != nil {
		for _, line := range st.Wrap(l.Why.Text, 1) {
			p.add(st.dim(line))
		}
	}
	for _, t := range l.Lines {
		for _, line := range st.Wrap(t.Text, 1) {
			p.add(line)
		}
	}
	if orgs && len(l.ByOrganisation) > 0 {
		p.blank()
		p.add(" " + st.bold(st.sayS("lots.title_org")))
		wOrg := Width(st.sayS("lots.col.org"))
		for _, o := range l.ByOrganisation {
			wOrg = max(wOrg, Width(o.Organisation))
		}
		line := func(org, dimms, find, ue string, h bool) string {
			out := " " + padRight(org, wOrg) + "  " + padLeft(dimms, wDimms)
			if l.Findings {
				out += "  " + padLeft(find, wFind) + "  " + padLeft(ue, wUE)
			}
			if h {
				return st.dim(out)
			}
			return strings.TrimRight(out, " ")
		}
		p.add(line(st.sayS("lots.col.org"), head.dimms, head.finding, head.ue, true))
		for _, o := range l.ByOrganisation {
			p.add(line(o.Organisation, i18n.Thousands(int64(o.DIMMs)), i18n.Thousands(int64(o.WithFinding)), i18n.Thousands(int64(o.WithUE)), false))
		}
	}
}

// SPDFiles draws the decode of dump files: one row per module, a line for
// a file that is no SPD, and the table of date codes over the files.
func SPDFiles(w io.Writer, f *view.SPDFiles, st Style) error {
	p := &page{st: st}
	p.add(f.Head.Text)
	var mods []view.SPDModule
	for _, file := range f.Files {
		if file.NotSPD != nil {
			for _, l := range st.Wrap(file.Path+": "+file.NotSPD.Text, 1) {
				p.add(l)
			}
			continue
		}
		mods = append(mods, file.Modules...)
	}
	if len(mods) > 0 {
		shared := view.SharedOf(mods)
		for _, l := range flow(sharedFields(shared, st), "  ", st.width()) {
			p.add(l)
		}
		p.blank()
		st.moduleTable(p, mods, shared, true)
	}
	if f.Lots != nil && len(f.Lots.Rows) > 0 {
		p.blank()
		p.add(" " + st.bold(st.sayS("lots.title")))
		st.lotsTable(p, f.Lots, len(f.Lots.ByOrganisation) > 1)
	}
	return p.writeTo(w)
}
