package term

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/agenticode/my/internal/view"
)

// verifyColor returns the colour of a check result.
func verifyColor(result string) string {
	switch result {
	case view.VerifyOK:
		return cGreen
	case view.VerifyDecode:
		return cYellow
	}
	return cBoldRed
}

// Verification draws the comparison of an injection plan with the records
// of a host: the result in one line, one line per injection, and below
// that every difference with the record that shows it.
func Verification(w io.Writer, v *view.Verification, st Style) error {
	p := &page{st: st}
	head := []string{st.say("ov.host", "name", st.bold(v.Host)), st.say("vf.plan", "scenario", v.Plan.Scenario)}
	for _, line := range flow(head, "  ", st.width()) {
		p.add(line)
	}
	color := cGreen
	if !v.OK {
		color = cBoldRed
	}
	for _, line := range st.Wrap(v.Summary.Text, 0) {
		p.add(st.paint(color, line))
	}
	if v.Plan.Summary != "" {
		for _, line := range st.Wrap(v.Plan.Summary, 0) {
			p.add(st.dim(line))
		}
	}
	p.blank()

	if len(v.Checks) > 0 {
		n := &column{head: "#", right: true}
		id := &column{head: st.say("col.id")}
		what := &column{head: st.say("col.injected")}
		where := &column{head: st.say("col.where")}
		planned := &column{head: st.say("col.planned"), right: true}
		found := &column{head: st.say("col.found"), right: true}
		sources := &column{head: st.say("col.sources")}
		result := &column{head: st.say("col.result")}
		for i := range v.Checks {
			c := &v.Checks[i]
			n.cells = append(n.cells, cell{text: fmt.Sprint(c.N)})
			id.cells = append(id.cells, cell{text: c.ID})
			what.cells = append(what.cells, cell{text: c.What})
			where.cells = append(where.cells, cell{text: truncate(c.Where, 34, st.g().ellipsis)})
			planned.cells = append(planned.cells, cell{text: fmt.Sprint(c.Planned)})
			found.cells = append(found.cells, cell{text: fmt.Sprint(c.Found)})
			var src []string
			for _, s := range c.Sources {
				// The number of records, where a source has more than one.
				if s.Records > 1 {
					src = append(src, fmt.Sprintf("%s x%d", s.Source, s.Records))
				} else {
					src = append(src, s.Source)
				}
			}
			names := strings.Join(src, ", ")
			if names == "" {
				names = "-"
			}
			sources.cells = append(sources.cells, cell{text: names})
			result.cells = append(result.cells, cell{text: c.ResultText, color: verifyColor(c.Result)})
		}
		// What goes first when the terminal is narrow: the sources, which
		// the line below the list sums up, then the place, which is in the
		// plan and in the lines about what differs.
		st.table(p, []*column{n, id, what, where, planned, found, sources, result}, []*column{sources, where, n, what})
	}

	labels := []string{st.say("vf.mismatches"), st.say("vf.unreported"), st.say("vf.beyond"), st.say("vf.method"), st.say("vf.sources")}
	lw := 0
	for _, l := range labels {
		lw = max(lw, Width(l))
	}

	// Every check that failed, with what differs.
	label := st.say("vf.mismatches")
	say := func(text string) {
		if label != "" {
			p.blank()
		}
		p.labelled(label, lw, text, "")
		label = ""
	}
	for i := range v.Checks {
		c := &v.Checks[i]
		switch c.Result {
		case view.VerifyOK:
			continue
		case view.VerifyMissing:
			say(c.ID + ": " + st.say("vf.no_record", "what", c.What, "where", c.Where))
		case view.VerifyCount:
			say(c.ID + ": " + st.say("vf.fewer", "found", c.Found, "planned", c.Planned, "what", c.What, "where", c.Where))
		case view.VerifyUnexpected:
			say(c.ID + ": " + st.say("vf.stayed", "where", c.Where))
		}
		for _, d := range c.Differences {
			say(c.ID + ": " + d.Text)
		}
	}

	if len(v.Sources) > 0 {
		p.blank()
		var parts []string
		for _, s := range v.Sources {
			parts = append(parts, st.say("vf.sources.text", "source", s.Source, "n", s.Records))
		}
		p.labelled(st.say("vf.sources"), lw, strings.Join(parts, ", "), "")
		p.labelled("", lw, st.say("vf.sources.note"), cDim)
	}
	if len(v.Unreported) > 0 {
		p.blank()
		var parts []string
		for _, f := range v.Unreported {
			parts = append(parts, st.say("vf.unreported.text", "field", f.Field, "n", f.Injections))
		}
		p.labelled(st.say("vf.unreported"), lw, strings.Join(parts, ", "), "")
		p.labelled("", lw, st.say("vf.unreported.note"), cDim)
	}
	if v.UnplannedErrors+v.UnplannedOfflines > 0 {
		p.blank()
		p.labelled(st.say("vf.beyond"), lw, st.say("vf.beyond.text", "errors", v.UnplannedErrors, "pages", v.UnplannedOfflines), "")
		const show = 6
		for i, u := range v.Unplanned {
			if i == show {
				p.labelled("", lw, st.say("vf.more", "n", v.UnplannedErrors+v.UnplannedOfflines-show), cDim)
				break
			}
			p.labelled("", lw, u, cDim)
		}
	}
	if v.Plan.Method != "" || len(v.Plan.Meta) > 0 {
		p.blank()
		var parts []string
		if v.Plan.Method != "" {
			parts = append(parts, v.Plan.Method)
		}
		keys := make([]string, 0, len(v.Plan.Meta))
		for k := range v.Plan.Meta {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			parts = append(parts, k+" "+v.Plan.Meta[k])
		}
		p.labelled(st.say("vf.method"), lw, strings.Join(parts, "; "), cDim)
	}
	return p.writeTo(w)
}
