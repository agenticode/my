package report

import (
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/agenticode/my/internal/explorer"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/view"
)

// Section 7, the Memory Explorer: the memory of the host as the kernel
// counted it when the scan ran, in the tabs of the explorer. It holds no
// finding. A report of a case without readings has no such section, and
// the sections after it keep the numbers they had.

// hasExplorer tells a report with the explorer section.
func (doc *Document) hasExplorer() bool { return !doc.Explorer.Empty() }

// secQuality and secAppendix are the numbers of the sections of data
// quality and of the appendix.
func (doc *Document) secQuality() int {
	if doc.hasExplorer() {
		return 8
	}
	return 7
}

func (doc *Document) secAppendix() int { return doc.secQuality() + 1 }

// secWord is the heading word of a section number.
func (doc *Document) secWord(n int) string {
	if doc.hasExplorer() {
		switch n {
		case 7:
			return doc.say("sec.explorer")
		case 8:
			return doc.say("sec.7")
		case 9:
			return doc.say("sec.8")
		}
	}
	return doc.say("sec." + strconv.Itoa(n))
}

// mxTab is one tab of the explorer.
type mxTab struct {
	id, word string
	body     []any
}

// explorerSection draws section 7.
func (doc *Document) explorerSection() *node {
	e := doc.Explorer
	s := doc.section(7)
	s.add(el("p", A{"class", "lead"}, doc.say("mx.lead")))
	var tabs []mxTab
	if sys := e.System; sys != nil {
		tabs = append(tabs, mxTab{"mx-overview", "mx.tab.overview", doc.mxOverview(e)})
	}
	if len(e.Slabs) > 0 {
		tabs = append(tabs, mxTab{"mx-slab", "mx.tab.slab", doc.mxSlab(e)})
	}
	if e.Page != nil {
		tabs = append(tabs, mxTab{"mx-page", "mx.tab.page", doc.mxPage(e)})
	}
	if len(e.Numa) > 0 {
		tabs = append(tabs, mxTab{"mx-numa", "mx.tab.numa", doc.mxNuma(e)})
	}
	if e.Mglru != nil {
		tabs = append(tabs, mxTab{"mx-mglru", "mx.tab.mglru", doc.mxMglru(e)})
	}
	nav := el("ul", A{"class", "mx-tabs"})
	for _, t := range tabs {
		nav.add(el("li", el("a", A{"href", "#" + t.id}, doc.say(t.word))))
	}
	s.add(el("nav", A{"aria-label", doc.say("mx.tabs")}, nav))
	panels := el("div", A{"class", "mx-panels"})
	for _, t := range tabs {
		p := el("div", A{"id", t.id, "class", "mx-panel"}, el("h3", doc.say(t.word)))
		p.add(t.body...)
		panels.add(p)
	}
	return s.add(panels)
}

func mib(n explorer.Num) string { return explorer.FormatBytes(float64(n)) }

// mxOverview is the tab Overview: the cards, the bar of the memory, and
// in a report for failure analysis the hints of the explorer.
func (doc *Document) mxOverview(e *view.Explorer) []any {
	sys := e.System
	cards := el("dl", A{"class", "mx-kpis"})
	card := func(key, value, level string) {
		cls := "mx-kpi"
		if level != "" {
			cls += " kpi-" + level
		}
		cards.add(el("div", A{"class", cls}, el("dt", doc.say("mx.kpi."+key)), el("dd", value)))
	}
	card("total", mib(sys.TotalBytes), "")
	card("used", mib(sys.UsedBytes), "")
	card("available", mib(sys.AvailableBytes), view.KPILevel("available", sys))
	card("cache", mib(sys.CachedBytes+sys.BuffersBytes), "")
	card("slab", mib(sys.SlabBytes)+" ("+doc.say("mx.kpi.slab_ru", "r", mib(sys.SreclaimableBytes), "u", mib(sys.SunreclaimBytes))+")", "")
	swap := doc.say("mx.kpi.no_swap")
	if sys.SwapTotalBytes != 0 {
		swap = mib(sys.SwapUsedBytes)
	}
	card("swap", swap, view.KPILevel("swap", sys))
	if slices.Contains(e.Missing, model.ReadingPressure) {
		card("psi", doc.say("mx.kpi.not_read"), "")
	} else {
		card("psi", explorer.ToFixed(float64(sys.PsiSomeAvg10), 1)+" / "+explorer.ToFixed(float64(sys.PsiFullAvg10), 1), view.KPILevel("pressure", sys))
	}
	card("dirty", mib(sys.DirtyBytes)+" / "+mib(sys.WritebackBytes), "")
	out := []any{cards, doc.memoryBar(sys)}
	if doc.Audience == FA {
		ul := el("ul", A{"class", "mx-hints"})
		for _, h := range e.Hints {
			ul.add(el("li", A{"class", "hint-" + h.Level}, doc.say("mx.hint."+h.Code)))
		}
		out = append(out, el("p", A{"class", "how"}, doc.say("mx.hints_are")), ul)
	}
	return out
}

// memoryBar draws the memory of the host as one bar of parts.
func (doc *Document) memoryBar(sys *explorer.SystemSnapshot) *node {
	const w, h = figW, 26.0
	total := float64(sys.TotalBytes)
	fig := svg("fig mx-bar", w, h+2, doc.say("mx.bar", "total", mib(sys.TotalBytes)))
	legend := el("ul", A{"class", "legend"})
	x := 0.0
	for _, seg := range view.Segments(sys) {
		sw := w * float64(seg.Bytes) / total
		label := doc.say("mx.seg."+seg.Code) + " " + explorer.FormatBytes(float64(seg.Bytes)) + " (" + explorer.ToFixed(explorer.Pct(float64(seg.Bytes), total), 1) + "%)"
		fig.add(el("g", A{"class", "mx-seg seg-" + seg.Code}, el("title", label),
			el("rect", A{"x", num(x), "y", "1", "width", num(max(sw, 0.5)), "height", num(h)})))
		legend.add(el("li", el("span", A{"class", "mx-dot seg-" + seg.Code}), label))
		x += sw
	}
	return el("figure", fig, legend)
}

// mxSlab is the tab Slab: the caches that take the most memory.
func (doc *Document) mxSlab(e *view.Explorer) []any {
	t, body := table("mx-slab", doc.say("mx.slab.name"), ">"+doc.say("mx.slab.active"), ">"+doc.say("mx.slab.objects"), ">"+doc.say("mx.slab.size"), ">"+doc.say("mx.slab.total"))
	for _, s := range e.Slabs {
		body.add(el("tr", el("td", A{"class", "mono"}, s.Name), numCell(int64(s.ActiveObjs)), numCell(int64(s.TotalObjs)),
			el("td", A{"class", "r"}, explorer.FormatBytes(float64(s.ObjSize))), el("td", A{"class", "r"}, explorer.FormatBytes(float64(s.TotalSize)))))
	}
	return []any{how(doc.say("mx.slab.how")), t}
}

// sarMetrics are the four charts of the tab Page.
var sarMetrics = []struct{ metric, class string }{{"fault/s", "c-fault"}, {"pgfree/s", "c-pgfree"}, {"pgscank/s", "c-pgscank"}, {"pgscand/s", "c-pgscand"}}

// mxPage is the tab Page: the page counters of /proc/vmstat and the
// paging table of sar -B in four charts.
func (doc *Document) mxPage(e *view.Explorer) []any {
	var out []any
	keys := e.Page.Vmstat.Keys()
	sort.Strings(keys)
	if len(keys) > 0 {
		t, body := table("mx-vmstat", doc.say("mx.page.counter"), ">"+doc.say("mx.page.value"))
		for _, k := range keys {
			v, _ := e.Page.Vmstat.Get(k)
			cell := el("td", A{"class", "r"}, explorer.FormatNumber(float64(v)))
			if lv := view.CounterLevel(float64(v)); lv != "" {
				cell = el("td", A{"class", "r v-" + lv}, explorer.FormatNumber(float64(v)))
			}
			body.add(el("tr", el("td", A{"class", "mono"}, k), cell))
		}
		out = append(out, how(doc.say("mx.page.how")), t)
	}
	if len(e.Page.SarB) == 0 {
		return append(out, el("p", doc.say("mx.page.no_sar")))
	}
	for _, m := range sarMetrics {
		out = append(out, doc.sarChart(e.Page.SarB, m.metric, m.class))
	}
	return out
}

// sarChart draws one column of sar -B over time in the geometry of the
// explorer: 800 by 220, the scale 10 % over the largest value.
func (doc *Document) sarChart(rows []explorer.SarRow, metric, class string) *node {
	const width, height = 800.0, 220.0
	const top, right, bottom, left = 20.0, 30.0, 50.0, 60.0
	cw, ch := width-left-right, height-top-bottom
	if _, ok := rows[0].Values.Get(metric); !ok {
		return el("p", doc.say("mx.page.no_metric", "metric", metric))
	}
	maxV := 0.0
	for _, r := range rows {
		v, _ := r.Values.Get(metric)
		maxV = max(maxV, float64(v))
	}
	maxV = math.Ceil(maxV * 1.1)
	if maxV == 0 {
		maxV = 10
	}
	fig := svg("fig mx-chart "+class, width, height, doc.say("mx.page.chart", "metric", metric))
	for _, ratio := range []float64{0, 0.25, 0.5, 0.75, 1} {
		y := top + ratio*ch
		fig.add(el("path", A{"class", "grid", "d", "M" + num(left) + " " + num(y) + "H" + num(width-right)}),
			el("text", A{"class", "tick", "x", num(left - 8), "y", num(y + 4), "text-anchor", "end"}, explorer.ToFixed(maxV*(1-ratio), 1)))
	}
	n := len(rows)
	pos := func(i int) (float64, float64) {
		v, _ := rows[i].Values.Get(metric)
		x := left + float64(i)/float64(max(n-1, 1))*cw
		return x, top + ch - float64(v)/maxV*ch
	}
	var d strings.Builder
	for i := range rows {
		x, y := pos(i)
		if i == 0 {
			d.WriteString("M" + num(x) + " " + num(y))
		} else {
			d.WriteString("L" + num(x) + " " + num(y))
		}
	}
	fig.add(el("path", A{"class", "mx-line", "d", d.String()}))
	step := max(1, (n+9)/10)
	for i, r := range rows {
		x, y := pos(i)
		v, _ := r.Values.Get(metric)
		fig.add(el("g", A{"class", "pt"}, el("title", r.Time+" "+metric+" "+explorer.NumString(float64(v))),
			el("circle", A{"class", "mx-dot-pt", "cx", num(x), "cy", num(y), "r", "2.5"})))
		if i%step == 0 || i == n-1 {
			fig.add(el("text", A{"class", "tick", "x", num(x), "y", num(height - 8), "text-anchor", "middle"}, r.Time))
		}
	}
	return el("figure", fig, el("figcaption", metric))
}

// mxNuma is the tab NUMA: a bar per node, the counters of numastat with
// their shares, and the locality of each node.
func (doc *Document) mxNuma(e *view.Explorer) []any {
	var out []any
	t := e.NumaAll
	if t != nil && t.Spread > 30 {
		out = append(out, el("p", A{"class", "mx-warn"}, doc.say("mx.numa.spread", "points", explorer.ToFixed(t.Spread, 0))))
	}
	const w, rowH = figW, 22.0
	labelW := 70.0
	fig := svg("fig mx-numa", w, rowH*float64(len(e.Numa))+4, doc.say("mx.numa.bars"))
	for i, n := range e.Numa {
		y := 2 + float64(i)*rowH
		used := explorer.Pct(float64(n.UsedBytes), float64(n.TotalBytes))
		bw := (w - labelW - 210) * explorer.Clamp(used, 0, 100) / 100
		label := doc.say("mx.numa.node", "n", explorer.NumString(float64(n.NodeID)))
		value := mib(n.UsedBytes) + " / " + mib(n.TotalBytes) + " (" + explorer.ToFixed(used, 1) + "%)"
		fig.add(el("text", A{"class", "rowlabel", "x", "0", "y", num(y + 14)}, label),
			el("rect", A{"class", "lane", "x", num(labelW), "y", num(y + 3), "width", num(w - labelW - 210), "height", "14"}),
			el("g", A{"class", "mx-node n-" + view.NodeLevel(used)}, el("title", label+" "+value),
				el("rect", A{"x", num(labelW), "y", num(y + 3), "width", num(max(bw, 0.5)), "height", "14"})),
			el("text", A{"class", "tick", "x", num(w - 204), "y", num(y + 14)}, value))
	}
	out = append(out, el("figure", fig))
	if t == nil || !t.HasCounts {
		return out
	}
	heads := []string{doc.say("mx.numa.counter")}
	for _, n := range e.Numa {
		heads = append(heads, ">"+doc.say("mx.numa.node", "n", explorer.NumString(float64(n.NodeID))))
	}
	heads = append(heads, ">"+doc.say("mx.numa.total"), doc.say("mx.numa.assess"))
	tbl, body := table("mx-numastat", heads...)
	row := func(name string, get func(explorer.NumaNode) explorer.Num, assess string) {
		tr := el("tr", el("td", A{"class", "mono"}, name))
		var sum float64
		for _, n := range e.Numa {
			v := float64(get(n))
			sum += v
			tr.add(el("td", A{"class", "r"}, explorer.FormatNumber(v)))
		}
		tr.add(el("td", A{"class", "r"}, explorer.FormatNumber(sum)))
		if assess == "" {
			tr.add(el("td"))
		} else {
			tr.add(el("td", A{"class", "assess-" + strings.SplitN(assess, " ", 2)[0]}, strings.SplitN(assess, " ", 2)[1]))
		}
		body.add(tr)
	}
	pctText := func(v float64) string { return explorer.ToFixed(v, 1) + "%" }
	row("numa_hit", func(n explorer.NumaNode) explorer.Num { return n.NumaHit }, view.Assess(t.HitRate, 95, 80, true)+" "+doc.say("mx.numa.hit", "pct", pctText(t.HitRate)))
	row("numa_miss", func(n explorer.NumaNode) explorer.Num { return n.NumaMiss }, view.Assess(t.MissRate, 5, 20, false)+" "+doc.say("mx.numa.miss", "pct", pctText(t.MissRate)))
	foreign := "ok"
	if t.Foreign != 0 {
		foreign = "warn"
	}
	row("numa_foreign", func(n explorer.NumaNode) explorer.Num { return n.NumaForeign }, foreign+" "+i18n.Thousands(t.Foreign))
	row("interleave_hit", func(n explorer.NumaNode) explorer.Num { return n.InterleaveHit }, "")
	row("local_node", func(n explorer.NumaNode) explorer.Num { return n.LocalNode }, view.Assess(t.Locality, 90, 70, true)+" "+doc.say("mx.numa.local", "pct", pctText(t.Locality)))
	row("other_node", func(n explorer.NumaNode) explorer.Num { return n.OtherNode }, view.Assess(t.Remote, 10, 30, false)+" "+doc.say("mx.numa.remote", "pct", pctText(t.Remote)))
	out = append(out, tbl)
	// The locality of each node.
	loc := el("ul", A{"class", "mx-locality"})
	for _, n := range e.Numa {
		total := float64(n.LocalNode + n.OtherNode)
		l := 100.0
		if total > 0 {
			l = explorer.Pct(float64(n.LocalNode), total)
		}
		loc.add(el("li", A{"class", "assess-" + view.Assess(l, 90, 70, true)}, doc.say("mx.numa.node_local", "n", explorer.NumString(float64(n.NodeID)), "pct", pctText(l))))
	}
	return append(out, loc, how(doc.say("mx.numa.how")))
}

// mxMglru is the tab MGLRU: whether the multi-generational LRU is on.
func (doc *Document) mxMglru(e *view.Explorer) []any {
	word := "mx.mglru.off"
	if e.Mglru.Enabled {
		word = "mx.mglru.on"
	}
	return []any{el("p", doc.say(word, "value", e.Mglru.EnabledValue))}
}
