package report

import (
	_ "embed"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/view"
)

//go:embed style.css
var styleSheet string

//go:embed script.js
var script string

// Write writes the report as one HTML document.
func (doc *Document) Write(w io.Writer) error {
	root, err := doc.html()
	if err != nil {
		return err
	}
	return writeDocument(w, root)
}

// unitRE finds a number and the unit that follows it.
var unitRE = regexp.MustCompile(`(\d) (GB|MB|kB|TB|B)\b`)

// bind keeps a number and its unit on one line.
func bind(s string) string { return unitRE.ReplaceAllString(s, "$1 $2") }

// upperFirst starts a sentence with a capital letter where the script has
// one.
func upperFirst(s string) string {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return s
	}
	return string(s[0]-'a'+'A') + s[1:]
}

// sentence ends a statement with a full stop.
func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasSuffix(s, ".") {
		return s
	}
	return s + "."
}

func (doc *Document) html() (*node, error) {
	host := doc.Overview.Host
	head := el("head",
		el("meta", A{"charset", "utf-8"}),
		el("meta", A{"name", "viewport", "content", "width=device-width, initial-scale=1"}),
		el("meta", A{"name", "color-scheme", "content", "light"}),
		el("meta", A{"name", "generator", "content", doc.Tool}),
		el("title", doc.say("title.page", "host", host.Name, "grade", host.GradeText)),
		el("style", rawText(styleSheet)),
	)
	sheet := el("div", A{"class", "sheet"},
		el("header", A{"class", "top"},
			el("p", A{"class", "kicker"},
				el("span", A{"class", "product"}, "MemorY"), " ",
				el("span", doc.say("audience."+string(doc.Audience))), " ",
				el("span", doc.say("generated", "when", minute(doc.Generated)))),
			el("h1", doc.say("title"), " ", el("span", A{"class", "h1host"}, doc.vsay("ov.host", "name", host.Name)))),
	)
	appendix, err := doc.appendix()
	if err != nil {
		return nil, err
	}
	sheet.add(el("main",
		doc.summary(), doc.slotMap(), doc.findings(), doc.where(), doc.timelines(), doc.checks(), doc.quality(), appendix),
		el("footer", el("p", doc.say("footer")), el("p", A{"class", "mono"}, doc.Tool)))
	body := el("body", sheet, doc.panel(), el("script", rawText(script)))
	return el("html", A{"lang", string(doc.Lang)}, head, body), nil
}

// section starts one of the eight sections of the report.
func (doc *Document) section(n int) *node {
	return el("section", A{"id", "s" + strconv.Itoa(n), "class", "sec"},
		el("h2", el("span", A{"class", "num"}, strconv.Itoa(n)), " "+doc.say("sec."+strconv.Itoa(n))))
}

// how is a line that says how to read a figure.
func how(text string) *node { return el("p", A{"class", "how"}, text) }

// conclusion says in a few sentences what the report found: the finding
// for the subject of the case, or for the DIMM or GPU with the worst
// finding, what it rests on and what follows; then what else has a
// finding.
func (doc *Document) conclusion() []*node {
	var dimm *DIMM
	for _, d := range doc.DIMMs {
		if d.Subject {
			dimm = d
		}
	}
	if dimm == nil && len(doc.DIMMs) > 0 {
		dimm = doc.DIMMs[0]
	}
	var gpu *GPU
	if len(doc.GPUs) > 0 {
		gpu = doc.GPUs[0]
	}
	// A GPU leads when no DIMM has anything on record, or when its
	// finding is the worse one and the case names no subject.
	if gpu != nil && dimm != nil && (dimm.Subject || gpu.Grade.ExitCode() <= dimm.Card.Grade.ExitCode()) {
		gpu = nil
	}
	if gpu != nil {
		dimm = nil
	}
	var out []*node
	p := el("p", A{"class", "conclusion"})
	finish := func(evidence []view.Evidence, next *view.Text) {
		if len(evidence) > 0 {
			p.add(R(sentence(upperFirst(evidence[0].Text.Text)) + " "))
		}
		if next != nil {
			p.add(R(sentence(next.Text)))
		}
		out = append(out, p)
	}
	switch {
	case dimm != nil:
		c := dimm.Card
		p.add(el("strong", dimmName(c)), ": "+c.GradeText+" ("+c.Label+", "+doc.vsay("card.confidence", "level", c.ConfidenceText)+"). ")
		finish(c.Evidence, c.Next)
		if !c.Present {
			out = append(out, el("p", doc.say("sum.gone")))
		}
	case gpu != nil:
		p.add(el("strong", "GPU "+gpu.Key), ": "+gpu.GradeText+". ")
		finish(gpu.Evidence, gpu.Next)
	default:
		return []*node{el("p", A{"class", "conclusion"}, doc.say("sum.none"))}
	}
	var others []string
	for _, o := range doc.DIMMs {
		if o != dimm {
			others = append(others, o.Card.Slot+" "+o.Card.GradeText)
		}
	}
	for _, o := range doc.GPUs {
		if o != gpu {
			others = append(others, "GPU "+o.Key+" "+o.GradeText)
		}
	}
	if len(others) > 0 {
		out = append(out, el("p", doc.say("sum.others", "n", len(others), "list", strings.Join(others, ", "))))
	}
	return out
}

// dimmName names a DIMM by its slot and serial number.
func dimmName(c *view.Card) string {
	if c.DIMM.Serial != "" {
		return c.Slot + " S/N " + c.DIMM.Serial
	}
	return c.Slot
}

// obsText says how much of the location of the errors the records of a
// host or a DIMM carry.
func (doc *Document) obsText(a, b, c int64) string {
	total := a + b + c
	var parts []string
	for _, g := range []struct {
		name string
		n    int64
	}{{"A", a}, {"B", b}, {"C", c}} {
		if g.n == 0 {
			continue
		}
		if g.n == total {
			return g.name + " (" + doc.say("obs."+g.name) + ")"
		}
		parts = append(parts, doc.say("obs.share", "grade", g.name, "n", g.n, "what", doc.say("obs."+g.name)))
	}
	if len(parts) == 0 {
		return doc.vsay("ov.no_errors")
	}
	return strings.Join(parts, "; ")
}

// summary is section 1: the conclusion, the grade, the identity of the
// case and how much the records say.
func (doc *Document) summary() *node {
	s := doc.section(1)
	host := doc.Overview.Host
	verdict := el("div", A{"class", classes("verdict", gradeClass(string(host.Grade)))},
		el("p", A{"class", "grade"}, icon(string(host.Grade), 26),
			el("strong", host.GradeText), " ",
			el("span", A{"class", "sub"}, doc.vsay("ov.host", "name", host.Name))))
	verdict.add(doc.conclusion())
	s.add(verdict)

	facts := el("dl", A{"class", "facts"})
	fact := func(code string, value ...any) {
		facts.add(el("div", el("dt", doc.say(code)), el("dd", value...)))
	}
	caseNote := doc.say("val.case_id")
	if doc.Case.Bundle {
		caseNote = doc.say("val.case_file")
	}
	fact("fact.case", el("span", A{"class", "id"}, doc.CaseID), " ", el("span", A{"class", "sub"}, caseNote))
	fact("fact.host", host.Name)
	if host.System != "" {
		fact("fact.system", host.System)
	}
	if !host.To.IsZero() {
		fact("fact.period", R(doc.say("val.period", "from", day(host.From), "to", day(host.To), "boots", host.Boots)))
	}
	if host.CE+host.UE > 0 {
		fact("fact.errors", doc.vsay("ov.errors", "ce", host.CE, "ue", host.UE))
	} else {
		fact("fact.errors", doc.vsay("ov.no_errors"))
	}
	if host.CE+host.UE > 0 {
		fact("fact.obs", doc.obsText(host.GradeA, host.GradeB, host.GradeC))
	}
	if sm := doc.Overview.SlotMap; sm.Slots > 0 {
		fact("fact.dimms", doc.say("val.dimms", "dimms", doc.Present, "slots", sm.Slots, "flagged", len(doc.DIMMs)))
	} else if doc.Present > 0 {
		fact("fact.dimms", doc.say("val.dimms_only", "dimms", doc.Present, "flagged", len(doc.DIMMs)))
	}
	if len(doc.GPUs) > 0 {
		fact("fact.gpus", doc.say("val.gpus", "n", len(doc.GPUs)))
	}
	if sc := host.Scan; sc != nil {
		what := doc.say("scan." + sc.Kind)
		if sc.Kind == model.ScanLive {
			what = doc.say("scan.live_user")
			if sc.Privileged {
				what = doc.say("scan.live_root")
			}
		}
		fact("fact.collected", R(doc.say("val.collected", "what", what, "when", minute(sc.At))))
	}
	if doc.Case.Bundle {
		text := doc.say("val.bundle", "when", minute(doc.Case.Created), "tool", doc.Case.Tool)
		if doc.Case.Anonymized {
			text += "; " + doc.say("val.anonymized")
		}
		fact("fact.bundle", R(text))
	}
	fact("fact.report", R(doc.say("val.report", "audience", doc.say("audience."+string(doc.Audience)), "when", minute(doc.Generated), "tool", doc.Tool)))
	if len(doc.Thresholds) > 0 {
		sets := make([]string, len(doc.Thresholds))
		for i, kv := range doc.Thresholds {
			sets[i] = kv[0] + "=" + kv[1]
		}
		fact("fact.engine", doc.say("val.engine_set", "version", doc.EngineVersion, "sets", strings.Join(sets, ", ")))
	} else {
		fact("fact.engine", doc.say("val.engine", "version", doc.EngineVersion))
	}
	s.add(facts, el("p", A{"class", "how"}, doc.say("sum.finding")))

	toc := el("ol", A{"class", "toc"})
	for n := 2; n <= 8; n++ {
		toc.add(el("li", el("a", A{"href", "#s" + strconv.Itoa(n)}, el("span", A{"class", "num"}, strconv.Itoa(n)), " "+doc.say("sec."+strconv.Itoa(n)))))
	}
	s.add(el("nav", A{"aria-label", doc.say("contents")}, toc))
	return s
}

// anchors maps the slot of each described DIMM to its name in links.
func (doc *Document) anchors() map[string]string {
	out := map[string]string{}
	for _, d := range doc.DIMMs {
		if d.Card.Present && d.Card.Slot != "" {
			out[d.Card.Slot] = d.Anchor
		}
	}
	return out
}

// tableHead builds a table with its head row and no body yet. A column
// whose head starts with ">" holds numbers and is set to the right.
func tableHead(class string, heads ...string) *node {
	tr := el("tr")
	for _, h := range heads {
		if strings.HasPrefix(h, ">") {
			tr.add(el("th", A{"class", "r", "scope", "col"}, h[1:]))
		} else {
			tr.add(el("th", A{"scope", "col"}, h))
		}
	}
	return el("table", A{"class", class}, el("thead", tr))
}

// table builds a table with a head row and returns it with its body.
func table(class string, heads ...string) (*node, *node) {
	t, body := tableHead(class, heads...), el("tbody")
	return t.add(body), body
}

// numCell is a cell that holds a number.
func numCell(n int64) *node { return el("td", A{"class", "r"}, i18n.Thousands(n)) }

// slotMap is section 2: the slot map and the DIMMs ranked by risk.
func (doc *Document) slotMap() *node {
	s := doc.section(2)
	ov := doc.Overview
	sm := &ov.SlotMap
	if len(sm.Rows) == 0 {
		s.add(el("p", doc.vsay("ov.no_slots")))
	} else {
		subject := ""
		for _, d := range doc.DIMMs {
			if d.Subject && d.Card.Present {
				subject = d.Card.Slot
			}
		}
		s.add(el("figure", doc.slotMapFigure(sm, doc.anchors(), subject), doc.slotLegend(sm),
			el("figcaption", doc.vsay("ov.slots", "slots", sm.Slots, "dimms", sm.Populated))),
			how(doc.say("cap.slotmap")))
	}

	if len(ov.Ranking) == 0 {
		s.add(el("p", doc.vsay("ov.none")))
	} else {
		t, body := table("ranking", ">"+doc.say("rank.n"), doc.say("rank.slot"), doc.say("rank.serial"), ">CE", ">UE", doc.say("rank.mode"),
			doc.say("rank.trend", "days", view.TrendDays), doc.say("rank.finding"), doc.say("rank.rules"))
		anchors := doc.anchors()
		for _, r := range ov.Ranking {
			slot := el("td", A{"class", "id"})
			if a := anchors[r.Slot]; a != "" && r.Present {
				slot.add(el("a", A{"href", "#" + a}, r.Slot))
			} else {
				slot.add(r.Slot)
			}
			serial := r.DIMM.Serial
			if serial == "" {
				serial = "-"
			}
			mode := r.ModeText
			switch {
			case mode == "":
				mode = "-"
			case r.Estimated:
				mode += " (" + engine.ModeText(doc.Lang, "estimated") + ")"
			}
			vals := make([]string, len(r.Trend))
			for i, v := range r.Trend {
				vals[i] = i18n.Thousands(v)
			}
			trend := el("td", A{"class", "trend"},
				sparkline(r.Trend, doc.say("trend.title", "days", view.TrendDays, "values", strings.Join(vals, ", "))),
				doc.vsay("dir."+r.Direction))
			finding := el("td", doc.badge(r.Grade))
			if !r.Present {
				finding.add(" (" + doc.vsay("ov.gone") + ")")
			}
			ue := numCell(r.UE)
			if r.UE > 0 {
				ue.add(A{"class", "r uecount"})
			}
			body.add(el("tr", numCell(int64(r.N)), slot, el("td", A{"class", "id"}, serial), numCell(r.CE), ue,
				el("td", A{"class", "nw"}, mode), trend, finding, el("td", doc.ruleChips(r.Rules))))
		}
		s.add(scroll(t), how(doc.say("cap.ranking", "days", view.TrendDays, "step", view.TrendDays/view.TrendBuckets)))
	}
	if ov.Quiet > 0 {
		s.add(el("p", doc.vsay("ov.quiet", "n", ov.Quiet)))
	}
	if ov.Unattributed > 0 {
		s.add(el("p", doc.vsay("ov.loose", "n", ov.Unattributed)))
	}
	if len(doc.GPUs) > 0 {
		t, body := table("ranking", "GPU", doc.say("rank.finding"), doc.say("rank.rules"))
		for _, g := range doc.GPUs {
			body.add(el("tr", el("td", A{"class", "id"}, el("a", A{"href", "#" + g.Anchor}, g.Key)), el("td", doc.badge(g.Grade)), el("td", doc.ruleChips(g.Rules))))
		}
		s.add(el("h3", doc.say("gpu.title")), scroll(t))
	}
	return s
}

// ruleChips writes rule IDs; each says what the rule fires on.
func (doc *Document) ruleChips(ids []string) []*node {
	var out []*node
	for i, id := range ids {
		if i > 0 {
			out = append(out, txt(" "))
		}
		chip := el("span", A{"class", "rule"}, id)
		if r, ok := engine.RuleByID(id); ok {
			chip.add(A{"title", doc.esay(r.Condition)})
		}
		out = append(out, chip)
	}
	return out
}

// recordsLink is the count of the records behind a statement; it leads to
// them.
func (doc *Document) recordsLink(e *view.Evidence) *node {
	if e.Records == 0 {
		return nil
	}
	text := doc.vsay("card.records", "n", e.Records)
	var keys []string
	for _, r := range cited(e) {
		if k := refKey(r); doc.record(k) != nil {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return el("span", A{"class", "recs"}, text)
	}
	return el("a", A{"class", "recs", "href", "#rec-" + keys[0], "data-rec", strings.Join(keys, " "), "data-label", "[" + e.Rule + "] " + e.Text.Text}, text)
}

// dimmHead is the line that says which DIMM a part of the report is
// about.
func (doc *Document) dimmHead(d *DIMM, tag string) *node {
	c := d.Card
	h := el(tag, el("span", A{"class", "slotname"}, c.Slot))
	var meta []string
	if c.DIMM.Kind != "" {
		meta = append(meta, bind(c.DIMM.Kind))
	}
	if p := strings.TrimSpace(c.DIMM.Manufacturer + " " + c.DIMM.PartNumber); p != "" {
		meta = append(meta, p)
	}
	if c.DIMM.Serial != "" {
		meta = append(meta, "S/N "+c.DIMM.Serial)
	} else {
		meta = append(meta, doc.vsay("dimm.no_serial"))
	}
	for _, m := range meta {
		h.add(" ", el("span", A{"class", "meta"}, m))
	}
	return h
}

// findings is section 3: the finding card of every DIMM that has errors
// or a finding.
func (doc *Document) findings() *node {
	s := doc.section(3)
	if len(doc.DIMMs) == 0 && len(doc.GPUs) == 0 {
		s.add(el("p", doc.vsay("ov.none")))
		return s
	}
	fired := map[string]bool{}
	for _, d := range doc.DIMMs {
		c := d.Card
		art := el("article", A{"id", d.Anchor, "class", classes("card", gradeClass(string(c.Grade)))}, doc.dimmHead(d, "h3"))
		var facts []string
		if !c.Since.IsZero() {
			facts = append(facts, doc.vsay("dimm.since", "when", day(c.Since)))
		}
		facts = append(facts, doc.vsay("dimm.moves", "n", c.Moves))
		if !c.Present {
			facts = append(facts, doc.vsay("ov.gone"))
		}
		if d.Subject {
			facts = append(facts, doc.say("sum.subject"))
		}
		art.add(el("p", A{"class", "sub"}, R(strings.Join(facts, "; "))))

		dl := el("dl", A{"class", "finding"})
		row := func(code string, kids ...any) {
			dl.add(el("div", el("dt", doc.vsay(code)), el("dd", kids...)))
		}
		conf := doc.vsay("card.confidence", "level", c.ConfidenceText)
		if doc.Audience == FA {
			conf += fmt.Sprintf(", %d", c.Confidence)
		}
		row("card.finding", doc.badge(c.Grade), " ("+c.Label+", "+conf+") ", el("span", A{"class", "rules"}, doc.ruleChips(c.Rules)))
		for _, id := range c.Rules {
			fired[id] = true
		}
		if len(c.Evidence) > 0 {
			ul := el("ul")
			for i := range c.Evidence {
				e := &c.Evidence[i]
				ul.add(el("li", el("span", A{"class", "rule"}, e.Rule), R(" "+e.Text.Text+" "), doc.recordsLink(e)))
			}
			row("card.evidence", ul)
		}
		if len(c.Checks) > 0 {
			ul := el("ul")
			for _, k := range c.Checks {
				ul.add(el("li", el("span", A{"class", "tag " + k.Effect}, k.EffectText), R(" "+k.Text.Text)))
			}
			row("card.checks", ul)
		}
		list := func(code string, items []view.Text) {
			if len(items) == 0 {
				return
			}
			ul := el("ul")
			for _, it := range items {
				ul.add(el("li", R(it.Text)))
			}
			row(code, ul)
		}
		list("card.missing", c.Missing)
		if d.Detail {
			list("card.baseline", c.Baseline)
		}
		if c.Next != nil {
			row("card.next", el("strong", R(c.Next.Text)))
		}
		// A DIMM without figures has a short card: what was seen and what
		// follows.
		if doc.Audience == FA && d.Detail {
			if len(c.Factors) > 0 {
				ol := el("ul", A{"class", "factors"})
				for i, f := range c.Factors {
					delta := fmt.Sprintf("%+d", f.Delta)
					if i == 0 {
						delta = strconv.Itoa(f.Delta)
					}
					ol.add(el("li", el("span", A{"class", "delta"}, delta), R(" "+f.Text.Text)))
				}
				row("card.factors", ol)
			}
			if len(c.Faults) > 0 {
				row("card.faults", doc.faultList(c))
			}
		}
		art.add(dl)
		for _, l := range d.Level2 {
			art.add(doc.level2(l))
		}
		if d.Detail {
			art.add(el("p", A{"class", "links"},
				el("a", A{"href", "#where-" + d.Anchor}, doc.say("card.to_where")), " ",
				el("a", A{"href", "#time-" + d.Anchor}, doc.say("card.to_time")), " ",
				el("a", A{"href", "#check-" + d.Anchor}, doc.say("card.to_check"))))
		}
		s.add(art)
	}

	if len(doc.GPUs) > 0 {
		s.add(el("h3", doc.say("gpu.title")))
		for _, g := range doc.GPUs {
			art := el("article", A{"id", g.Anchor, "class", classes("card", gradeClass(string(g.Grade)))},
				el("h4", el("span", A{"class", "slotname"}, "GPU "+g.Key)))
			dl := el("dl", A{"class", "finding"})
			dl.add(el("div", el("dt", doc.vsay("card.finding")), el("dd", doc.badge(g.Grade), " ", el("span", A{"class", "rules"}, doc.ruleChips(g.Rules)))))
			if len(g.Evidence) > 0 {
				ul := el("ul")
				for i := range g.Evidence {
					e := &g.Evidence[i]
					ul.add(el("li", el("span", A{"class", "rule"}, e.Rule), R(" "+e.Text.Text+" "), doc.recordsLink(e)))
				}
				dl.add(el("div", el("dt", doc.vsay("card.evidence")), el("dd", ul)))
			}
			if g.Next != nil {
				dl.add(el("div", el("dt", doc.vsay("card.next")), el("dd", el("strong", R(g.Next.Text)))))
			}
			for _, id := range g.Rules {
				fired[id] = true
			}
			s.add(art.add(dl))
		}
	}
	s.add(how(doc.say("cap.card")))

	// The rules the findings name, with what each fires on.
	if len(fired) > 0 {
		t, body := table("rules", doc.say("rules.id"), doc.say("rules.grade"), doc.say("rules.when"))
		for _, r := range engine.Rules {
			if fired[r.ID] {
				body.add(el("tr", el("td", el("span", A{"class", "rule"}, r.ID)), el("td", doc.badge(r.Grade)), el("td", doc.esay(r.Condition))))
			}
		}
		s.add(el("h3", doc.say("rules.title")), scroll(t))
	}
	return s
}

// faultList lists the faults of a DIMM one by one: mode and place, counts,
// period, trend, extent and observation grade.
func (doc *Document) faultList(c *view.Card) *node {
	ul := el("ul")
	for _, f := range c.Faults {
		name := view.FaultName(doc.Lang, f.Mode, f.Estimated)
		if f.Where != "" {
			name = doc.vsay("card.fault", "fault", name, "where", f.Where)
		}
		parts := []string{name + ": " + doc.vsay("ov.errors", "ce", f.CE, "ue", f.UE)}
		if !f.First.IsZero() && f.First.Year() > 1970 {
			parts = append(parts, doc.vsay("span", "from", day(f.First), "to", day(f.Last)))
		}
		parts = append(parts, doc.vsay("dir."+f.Direction))
		if f.Recurring {
			parts = append(parts, doc.say("fault.recur"))
		}
		if f.Confined {
			parts = append(parts, doc.say("fault.confined"))
		}
		if f.Rows+f.Columns+f.Banks > 0 {
			parts = append(parts, doc.say("fault.cells", "rows", f.Rows, "columns", f.Columns, "banks", f.Banks))
		}
		parts = append(parts, doc.vsay("ov.obs", "grades", string(f.Grade)))
		ul.add(el("li", R(strings.Join(parts, "; "))))
	}
	return ul
}

// level2 shows what a vendor analyzer said about a DIMM.
func (doc *Document) level2(l Level2) *node {
	box := el("div", A{"class", "level2"}, el("h4", doc.say("l2.title")), el("p", A{"class", "sub"}, doc.say("l2.by", "analyzer", l.Analyzer)+". "+doc.say("l2.audience")))
	if len(l.Lines) > 0 {
		dl := el("dl", A{"class", "finding"})
		for _, ln := range l.Lines {
			dl.add(el("div", el("dt", ln.Label), el("dd", ln.Value)))
		}
		box.add(dl)
	}
	for _, n := range l.Notes {
		box.add(el("p", n))
	}
	return box
}

// keepRows is the number of rows up to which a table is kept on one page
// in print.
const keepRows = 12

// scroll wraps a table: on a narrow screen it scrolls sideways within the
// wrapper, and in print a short table stays on one page.
func scroll(t *node) *node {
	rows := 0
	for _, sec := range t.kids {
		rows += len(sec.kids)
	}
	if rows <= keepRows {
		return el("div", A{"class", "scroll keep"}, t)
	}
	return el("div", A{"class", "scroll"}, t)
}

// details is a part of the report that opens on demand: the table that
// holds the same as a figure.
func details(summary string, kids ...any) *node {
	return el("details", el("summary", summary), el("div", A{"class", "scroll"}).add(kids...))
}

// where is section 4: for each DIMM, the branch its errors sit on and the
// address map of the bank with the most errors.
func (doc *Document) where() *node {
	s := doc.section(4)
	drawn, maps := 0, 0
	s.add(doc.restNote())
	for _, d := range doc.detailed() {
		sub := el("section", A{"id", "where-" + d.Anchor, "class", "dimm"}, doc.dimmHead(d, "h3"))
		if d.Card.CE+d.Card.UE == 0 {
			sub.add(el("p", doc.vsay("ov.no_errors")))
			s.add(sub)
			continue
		}
		drawn++
		if t := d.Topology; t != nil && t.Total > 0 {
			tv, body := table("tv", doc.say("tv.level"), doc.say("tv.branch"), ">"+doc.say("tv.errors"), ">"+doc.say("tv.share"))
			for li := range t.Levels {
				lv := &t.Levels[li]
				for i := range lv.Segments {
					sg := &lv.Segments[i]
					body.add(el("tr", el("td", lv.Name), el("td", A{"class", "id"}, doc.segmentLabel(sg)), numCell(sg.Errors), el("td", A{"class", "r"}, percent(sg.Share))))
				}
			}
			sub.add(el("figure", el("figcaption", doc.vsay("topo.title")), doc.topologyFigure(t, d.Card.Slot)),
				details(doc.say("tv.title"), tv))
		}
		m := d.Map
		switch {
		case m == nil || m.Bank == nil:
			sub.add(el("p", doc.vsay("map.none")))
		default:
			cellNote := doc.vsay("map.cell", "n", m.ColumnsPerCell)
			if m.Mode == view.MapBins {
				cellNote = doc.vsay("map.cell_bins", "n", m.ColumnsPerCell, "rows", m.RowsPerLine)
			}
			fig := el("figure", el("figcaption", el("span", A{"class", "mono"}, m.Bank.Label), ", "+doc.vsay("map.axes")+", "+cellNote), doc.mapLegend(m))
			if len(m.Lines) == 0 {
				fig.add(el("p", doc.vsay("map.no_rows")))
			} else {
				fig.add(doc.addressMapFigure(d))
				maps++
			}
			sub.add(fig)
			notes := el("ul", A{"class", "notes"})
			for _, f := range m.Faults {
				notes.add(el("li", R(f.Text.Text)))
			}
			if m.Stray > 0 {
				notes.add(el("li", doc.vsay("map.stray", "n", m.Stray)))
			}
			if m.BankCount > 1 {
				var others []string
				for _, b := range m.Banks {
					if b.Label != m.Bank.Label {
						others = append(others, fmt.Sprintf("%s (%s)", b.Label, i18n.Thousands(b.Errors)))
					}
				}
				text := doc.vsay("map.others", "list", strings.Join(others, ", "))
				if rest := m.BankCount - len(m.Banks); rest > 0 {
					text += " " + doc.vsay("map.more", "n", rest)
				}
				notes.add(el("li", text))
			}
			if linked := len(d.Linked); linked < len(m.Refs) {
				notes.add(el("li", doc.say("map.linked", "n", linked, "rest", len(m.Refs)-linked)))
			}
			if len(notes.kids) > 0 {
				sub.add(notes)
			}
			sub.add(doc.cellTable(d))
		}
		if m != nil && m.Unlocated > 0 {
			sub.add(el("p", doc.vsay("map.unlocated", "n", m.Unlocated)))
		}
		s.add(sub)
	}
	if drawn > 0 {
		s.add(how(doc.say("cap.topology")))
		if maps > 0 {
			s.add(how(doc.say("cap.map")))
		}
	} else if len(doc.DIMMs) == 0 {
		s.add(el("p", doc.vsay("ov.none")))
	}
	return s
}

// restNote says which DIMMs the sections with figures leave out.
func (doc *Document) restNote() *node {
	rest := len(doc.DIMMs) - len(doc.detailed())
	if rest == 0 {
		return nil
	}
	return el("p", A{"class", "sub"}, doc.say("detail.rest", "n", rest, "min", DetailMinErrors, "grade", engine.GradeText(doc.Lang, model.VerdictObserve)))
}

// cellTable lists the cells of an address map that hold errors: the same
// as the figure, as a table.
func (doc *Document) cellTable(d *DIMM) *node {
	m := d.Map
	if len(m.Refs) == 0 {
		return nil
	}
	t, body := table("tv", doc.say("tv.row"), doc.say("tv.columns"), ">"+doc.say("tv.errors"), ">"+doc.say("tv.ue"), ">"+doc.say("tv.records"),
		doc.say("tv.first"), doc.say("tv.latest"))
	order := sortedCells(m)
	for n, i := range order {
		if n >= MaxCellRows {
			break
		}
		r := &m.Refs[i]
		ln := &m.Lines[r.Line]
		rows := fmt.Sprintf("%#x", ln.Row)
		if ln.Rows > 1 {
			rows += fmt.Sprintf(" - %#x", ln.Row+ln.Rows-1)
		}
		cols := fmt.Sprintf("%#x - %#x", r.Cell*m.ColumnsPerCell, (r.Cell+1)*m.ColumnsPerCell-1)
		rec := func(id int64, at string) *node {
			if k := key(model.RecordEvent, id); d.Linked[i] && doc.record(k) != nil {
				return el("td", A{"class", "nw"}, el("a", A{"href", "#rec-" + k, "data-rec", k, "data-label", doc.cellText(m, r)}, at))
			}
			return el("td", A{"class", "nw"}, at)
		}
		body.add(el("tr", el("td", A{"class", "id"}, rows), el("td", A{"class", "id"}, cols), numCell(r.Errors), numCell(r.UEs), numCell(int64(r.Records)),
			rec(r.First, day(r.From)), rec(r.Last, day(r.To))))
	}
	var more *node
	if len(order) > MaxCellRows {
		more = el("p", A{"class", "sub"}, doc.say("tv.more", "n", MaxCellRows, "total", len(order)))
	}
	return details(doc.say("tv.title"), t, more)
}

// timelines is section 5: errors per day with the events of the host, for
// the host and for each DIMM, and the repairs on record.
func (doc *Document) timelines() *node {
	s := doc.section(5)
	host := doc.Overview.Host.Name
	events := func(tl *view.Timeline) *node {
		ul := el("ul", A{"class", "notes"})
		for i := range tl.Markers {
			g := &tl.Markers[i]
			ul.add(el("li", R(g.Text.Text)))
			// A boot and a slot change have no record to lead to.
			if n := len(linkedMarks(g)); n > 0 && n < g.Events {
				ul.add(el("li", A{"class", "sub"}, doc.say("tl.older", "kind", doc.laneLabel(g.Kind), "shown", n, "n", g.Events)))
			}
		}
		for _, n := range tl.Notes {
			ul.add(el("li", R(n.Text)))
		}
		if len(ul.kids) == 0 {
			return nil
		}
		return ul
	}
	draw := func(tl *view.Timeline, what string, caption *node) []*node {
		switch {
		case tl == nil || tl.Days == 0:
			out := []*node{el("p", doc.vsay("tl.none"))}
			if tl != nil {
				for _, n := range tl.Notes {
					out = append(out, el("p", n.Text))
				}
			}
			return out
		case len(tl.Series) == 0 && len(tl.Markers) == 0:
			return []*node{el("p", doc.vsay("tl.quiet"))}
		}
		return []*node{el("figure", caption, doc.timelineFigure(tl, what)), events(tl), doc.dayTable(tl)}
	}

	// The host: every DIMM with errors, a row each. A host with one such
	// DIMM has nothing to add to the timeline of that DIMM.
	detailed := doc.detailed()
	hostRow := len(detailed) != 1 || len(doc.Timeline.Series) > 1
	if hostRow {
		s.add(el("section", A{"class", "dimm"}, el("h3", doc.say("tl.host", "host", host)),
			draw(doc.Timeline, doc.vsay("ov.host", "name", host), el("figcaption", doc.vsay("tl.title")))))
		if len(doc.Timeline.Series) > 1 {
			s.add(how(doc.say("cap.series")))
		}
	}
	s.add(doc.restNote())
	for _, d := range detailed {
		sub := el("section", A{"id", "time-" + d.Anchor, "class", "dimm"}, doc.dimmHead(d, "h3"))
		if d.Card.CE+d.Card.UE == 0 && d.ActionCount == 0 {
			sub.add(el("p", doc.vsay("ov.no_errors")))
			s.add(sub)
			continue
		}
		sub.add(draw(d.Timeline, d.Card.Slot, el("figcaption", doc.vsay("tl.title"))))
		if len(d.Actions) > 0 {
			t, body := table("actions", doc.say("act.when"), doc.say("act.step"), doc.say("act.target"), doc.say("act.result"), doc.say("act.source"))
			for i := range d.Actions {
				a := &d.Actions[i]
				step := doc.kindText(string(a.Kind))
				if a.Method != "" {
					step += " (" + a.Method + ")"
				}
				target := doc.actionWhere(a)
				if a.Count > 1 {
					target = strings.TrimSpace(target + " " + doc.say("act.pages", "n", a.Count))
				}
				if target == "" {
					target = "-"
				}
				k := key(model.RecordAction, a.ID)
				when := el("td", A{"class", "id"})
				if doc.record(k) != nil {
					when.add(el("a", A{"href", "#rec-" + k, "data-rec", k, "data-label", step + ", " + doc.timeText(a.At, a.TimeQuality)}, doc.timeText(a.At, a.TimeQuality)))
				} else {
					when.add(doc.timeText(a.At, a.TimeQuality))
				}
				body.add(el("tr", when, el("td", step), el("td", A{"class", "mono"}, target), el("td", A{"class", "nw"}, doc.say("res."+string(a.Result))), el("td", A{"class", "id"}, a.Source)))
			}
			sub.add(el("h4", doc.say("act.title")), scroll(t))
			if d.ActionCount > len(d.Actions) {
				sub.add(el("p", A{"class", "sub"}, doc.say("act.shown", "shown", len(d.Actions), "n", d.ActionCount)))
			}
		}
		s.add(sub)
	}
	if len(detailed) > 0 || hostRow {
		s.add(how(doc.say("cap.timeline")))
	}
	return s
}

// dayTable lists the days of a timeline that have errors or events: the
// same as the figure, as a table.
func (doc *Document) dayTable(tl *view.Timeline) *node {
	heads := []string{doc.say("tv.day")}
	for _, sr := range tl.Series {
		heads = append(heads, ">"+sr.Label)
	}
	heads = append(heads, doc.say("tv.what"))
	t, body := table("tv", heads...)
	events := map[int][]string{}
	for _, g := range tl.Markers {
		for _, d := range g.Days {
			events[d] = append(events[d], doc.laneLabel(g.Kind))
		}
	}
	rows := 0
	for d := 0; d < tl.Days; d++ {
		any := len(events[d]) > 0
		for _, sr := range tl.Series {
			any = any || (d < len(sr.CE) && sr.CE[d] > 0)
		}
		if !any {
			continue
		}
		tr := el("tr", el("td", A{"class", "id"}, day(tl.From.AddDate(0, 0, d))))
		for _, sr := range tl.Series {
			var n int64
			if d < len(sr.CE) {
				n = sr.CE[d]
			}
			tr.add(numCell(n))
		}
		tr.add(el("td", A{"class", "nw"}, strings.Join(events[d], ", ")))
		body.add(tr)
		rows++
	}
	if rows == 0 {
		return nil
	}
	return details(doc.say("tv.title"), t)
}

// checks is section 6: what was tested against a wrong diagnosis, and
// what a person has to test.
func (doc *Document) checks() *node {
	s := doc.section(6)
	if len(doc.DIMMs) == 0 {
		s.add(el("p", doc.vsay("ov.none")))
		return s
	}
	s.add(how(doc.say("cap.checks")), doc.restNote())
	for _, d := range doc.detailed() {
		c := d.Card
		sub := el("section", A{"id", "check-" + d.Anchor, "class", "dimm"},
			el("h3", el("span", A{"class", "slotname"}, c.Slot), " ", el("span", A{"class", "meta"}, doc.badge(c.Grade))))
		if c.Grade == model.VerdictHoldReplacement {
			sub.add(el("p", A{"class", "hold"}, doc.say("chk.hold")))
		}
		// What speaks for a cause outside the DIMM: the hold rules that
		// fired. Then the checks of the finding.
		t, body := table("checks", doc.say("chk.result"), doc.say("chk.check"))
		rows := 0
		for i := range c.Evidence {
			e := &c.Evidence[i]
			if r, ok := engine.RuleByID(e.Rule); ok && r.Grade == model.VerdictHoldReplacement {
				body.add(el("tr", el("td", doc.ruleChips([]string{e.Rule})), el("td", R(e.Text.Text+" "), doc.recordsLink(e))))
				rows++
			}
		}
		var todos []string
		seen := map[string]bool{}
		if c.Grade == model.VerdictHoldReplacement && c.Next != nil {
			// The next step of a hold finding is the first test, and it
			// stands for the test a check would name.
			todos = append(todos, c.Next.Text)
			seen[todoOfNext[c.Next.Code]] = true
		}
		for _, k := range c.Checks {
			body.add(el("tr", el("td", el("span", A{"class", "tag " + k.Effect}, k.EffectText)), el("td", R(k.Text.Text))))
			rows++
			if code := todoOf[k.Code]; code != "" && k.Effect != model.EffectSupports && !seen[code] {
				seen[code] = true
				todos = append(todos, doc.say(code))
			}
		}
		sub.add(el("h4", doc.say("chk.auto")))
		if rows == 0 {
			sub.add(el("p", doc.say("chk.none")))
		} else {
			sub.add(scroll(t))
		}
		sub.add(el("h4", doc.say("chk.todo")))
		if len(todos) == 0 {
			sub.add(el("p", doc.say("chk.todo_none")))
		} else {
			ul := el("ul", A{"class", "todo"})
			for _, text := range todos {
				ul.add(el("li", R(text)))
			}
			sub.add(ul)
		}
		s.add(sub)
	}
	return s
}

// quality is section 7: the sources, what each yielded, and how much of
// the location of an error the records carry.
func (doc *Document) quality() *node {
	s := doc.section(7)
	s.add(how(doc.say("cap.quality")))
	host := doc.Overview.Host

	if host.CE+host.UE > 0 {
		legend := el("ul", A{"class", "legend"})
		for _, g := range []struct {
			name, cls string
			n         int64
		}{{"A", "oa", host.GradeA}, {"B", "ob", host.GradeB}, {"C", "oc", host.GradeC}} {
			legend.add(el("li", swatch(g.cls, false), doc.say("obs.share", "grade", g.name, "n", g.n, "what", doc.say("obs."+g.name))))
		}
		s.add(el("h3", doc.say("q.grades")), el("figure", doc.gradeBar(host.GradeA, host.GradeB, host.GradeC), legend))
		if len(doc.DIMMs) > 0 {
			t, body := table("grades", "DIMM", ">A", ">B", ">C")
			for _, d := range doc.DIMMs {
				body.add(el("tr", el("td", A{"class", "id"}, d.Card.Slot), numCell(d.GradeA), numCell(d.GradeB), numCell(d.GradeC)))
			}
			s.add(details(doc.say("q.dimms"), t))
		}
	}

	s.add(el("h3", doc.say("q.sources")))
	srcs := doc.Overview.Sources
	if len(srcs) == 0 {
		s.add(el("p", doc.say("q.no_sources")))
	} else {
		t, body := table("sources", doc.say("q.source"), doc.say("q.origin"), doc.say("q.status"), ">"+doc.say("q.records"), ">"+doc.say("q.events"),
			">"+doc.say("q.actions"), ">"+doc.say("q.skipped"), doc.say("q.grade"), doc.say("q.span"))
		for _, src := range srcs {
			grade := string(src.Grade)
			if grade == "" {
				grade = "-"
			}
			span := "-"
			if !src.First.IsZero() && src.First.Year() > 1970 {
				span = doc.vsay("span", "from", day(src.First), "to", day(src.Last))
			}
			status := src.StatusText
			if src.Detail != "" && src.Status != model.SourceOK {
				status += " (" + src.Detail + ")"
			}
			body.add(el("tr", el("td", A{"class", "id"}, src.Kind), el("td", A{"class", "mono"}, src.Origin), el("td", status),
				numCell(int64(src.Records)), numCell(int64(src.Events)), numCell(int64(src.Actions)), numCell(int64(src.Skipped)),
				el("td", grade), el("td", R(span))))
		}
		s.add(scroll(t))
	}

	s.add(el("h3", doc.say("q.missing")))
	if len(doc.Overview.Missing) == 0 && doc.Overview.Root == nil {
		s.add(el("p", doc.say("q.none")))
	} else {
		ul := el("ul", A{"class", "notes"})
		for _, m := range doc.Overview.Missing {
			ul.add(el("li", R(m.Text)))
		}
		if r := doc.Overview.Root; r != nil {
			ul.add(el("li", r.Text))
		}
		s.add(ul)
	}

	notes := el("ul", A{"class", "notes"})
	for _, src := range srcs {
		for _, n := range src.Notes {
			notes.add(el("li", el("span", A{"class", "mono"}, src.Kind), ": "+n))
		}
	}
	if doc.Duplicates > 0 {
		notes.add(el("li", doc.say("q.dup", "n", doc.Duplicates)))
	}
	if doc.Undated > 0 {
		notes.add(el("li", doc.say("q.undated", "n", doc.Undated)))
	}
	if n := doc.Overview.Unattributed; n > 0 {
		notes.add(el("li", doc.say("q.loose", "n", n)))
	}
	if doc.NoSerial > 0 {
		notes.add(el("li", doc.say("q.no_serial", "n", doc.NoSerial)))
	}
	if len(notes.kids) > 0 {
		s.add(el("h3", doc.say("q.notes")), notes)
	}
	return s
}

// recordHeads are the column heads of the table of records.
func (doc *Document) recordHeads() []string {
	return []string{doc.say("rec.id"), doc.say("rec.time"), doc.say("rec.what"), doc.say("rec.slot"), doc.say("rec.where"),
		">" + doc.say("rec.n"), doc.say("rec.grade"), doc.say("rec.source")}
}

// recordRows writes one record as a group of two rows: its fields, and
// its original text.
func (doc *Document) recordRows(r *Record) *node {
	when := doc.timeText(r.At, r.TimeQuality)
	timeCell := el("td", A{"class", "id"}, when)
	if r.TimeQuality == model.TimeEstimated {
		timeCell.add(A{"title", doc.say("rec.estimated")}, " ~")
	}
	what := el("td", r.What)
	if r.Cited {
		what.add(" ", el("span", A{"class", "cite", "title", doc.say("rec.cited")}, "*"))
	}
	grade := string(r.Grade)
	if grade == "" {
		grade = "-"
	}
	orDash := func(s string) string {
		if s == "" {
			return "-"
		}
		return s
	}
	raw := r.Raw
	rawCell := el("td", A{"colspan", "8"})
	if raw == "" {
		rawCell.add(el("p", A{"class", "sub"}, doc.say("rec.no_raw")))
	} else {
		rawCell.add(el("pre", raw))
	}
	return el("tbody", A{"id", "rec-" + r.Key, "class", classes("rec", r.Class, map[bool]string{true: "cited"}[r.Cited])},
		el("tr",
			el("td", A{"class", "id"}, el("a", A{"class", "recid", "href", "#rec-" + r.Key}, r.Key)),
			timeCell, what.add(A{"class", "nw"}), el("td", A{"class", "id"}, orDash(r.Slot)), el("td", A{"class", "mono"}, orDash(r.Where)),
			numCell(int64(r.Count)), el("td", grade),
			el("td", A{"class", "mono"}, r.Source, el("br"), r.RawRef)),
		el("tr", A{"class", "raw"}, rawCell))
}

// appendix is section 8: the records the report points at, with their
// original text, and the error records with FMFM field names.
func (doc *Document) appendix() (*node, error) {
	s := doc.section(8)
	s.add(el("h3", A{"id", "records"}, doc.say("app.records")), how(doc.say("cap.records")+" "+doc.say("cap.cited")))
	if len(doc.Records) == 0 {
		s.add(el("p", doc.say("rec.none")))
	} else {
		// Each record is a row group of its own: its fields, and its
		// original text.
		t := tableHead("records", doc.recordHeads()...)
		for _, r := range doc.Records {
			t.add(doc.recordRows(r))
		}
		s.add(el("p", A{"class", "sub"}, doc.say("app.listed", "n", len(doc.Records), "total", doc.TotalRecords)), scroll(t))
	}

	lines, n, err := doc.fmfmLines()
	if err != nil {
		return nil, err
	}
	s.add(el("h3", A{"id", "fmfm"}, doc.say("app.fmfm")))
	if n == 0 {
		s.add(el("p", doc.say("app.fmfm_none")))
	} else {
		s.add(how(doc.say("cap.fmfm")), el("pre", A{"class", "json", "tabindex", "0"}, lines))
	}
	return s, nil
}

// panel is where the records of a point open, beside the figure the
// reader is looking at. It is filled by the script; without the script a
// point leads to its record in the appendix.
func (doc *Document) panel() *node {
	t := tableHead("records", doc.recordHeads()...).add(A{"id", "panel-records"})
	return el("aside", A{"id", "panel", "class", "panel", "hidden", "hidden", "aria-label", doc.say("panel.title")},
		el("div", A{"class", "panel-top"},
			el("p", A{"id", "panel-label", "class", "panel-what"}),
			el("button", A{"id", "panel-close", "type", "button", "title", doc.say("panel.hint")}, doc.say("panel.close"))),
		scroll(t))
}
