package term

import (
	"io"
	"strings"

	"github.com/agenticode/my/internal/memras"
	"github.com/agenticode/my/internal/view"
)

// statusColor is the colour of what became of a setting: red for a
// setting that is not as expected, and none for the rest. A setting the
// profile cannot judge is not a finding.
func statusColor(status string) string {
	switch status {
	case memras.StatusMismatch:
		return cRed
	case memras.StatusOK:
		return cGreen
	}
	return ""
}

// BIOS draws the check of the BIOS memory RAS settings of a host: one
// line per setting, and below each what waits for a reboot and what the
// DIMMs of the host mean for it.
func BIOS(w io.Writer, v *view.BIOS, st Style) error {
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
	if v.None {
		return p.writeTo(w)
	}
	p.blank()

	// The table. The attribute and its value stand last: they are as long
	// as the BIOS makes them.
	c := v.Columns
	wSetting, wState, wExpected, wCheck := Width(c.Setting), Width(c.State), Width(c.Expected), Width(c.Check)
	for _, r := range v.Rows {
		wSetting = max(wSetting, Width(r.Title))
		wState = max(wState, Width(r.StateText))
		wExpected = max(wExpected, Width(r.ExpectedText))
		wCheck = max(wCheck, Width(r.StatusText))
	}
	p.add(st.dim(" " + padRight(c.Setting, wSetting) + "  " + padRight(c.State, wState) + "  " + padRight(c.Expected, wExpected) + "  " +
		padRight(c.Check, wCheck) + "  " + c.Value))
	indent := 1 + wSetting + 2
	for _, r := range v.Rows {
		value := ""
		if r.Attribute != "" {
			value = r.Attribute
			if r.Value != "" {
				value += " = " + r.Value
			}
		}
		expected := r.ExpectedText
		if expected == "" {
			expected = "-"
		}
		state := r.StateText
		if state == "" {
			state = "-"
		}
		check := st.paint(statusColor(r.Status), r.StatusText) + strings.Repeat(" ", max(0, wCheck-Width(r.StatusText)))
		line := " " + padRight(r.Title, wSetting) + "  " + padRight(state, wState) + "  " + padRight(expected, wExpected) + "  " + check
		// An attribute and value too long for the line stand below it.
		used := 1 + wSetting + 2 + wState + 2 + wExpected + 2 + wCheck + 2
		if used+Width(value) <= st.width() {
			p.add(line + "  " + value)
		} else {
			p.add(line)
			p.add(strings.Repeat(" ", indent) + value)
		}
		for _, t := range r.Lines {
			for _, l := range st.Wrap(t.Text, indent) {
				p.add(st.dim(l))
			}
		}
	}

	if len(v.Unmapped) > 0 {
		p.blank()
		for _, t := range v.Unmapped {
			for _, l := range st.Wrap(t.Text, 1) {
				p.add(l)
			}
		}
	}
	if len(v.Speed) > 0 {
		p.blank()
		for i, t := range v.Speed {
			text, indent := t.Text, 1
			if i >= 2 {
				text, indent = "- "+text, 3
			}
			for j, l := range st.Wrap(text, indent) {
				if i >= 2 && j > 0 {
					l = "  " + l
				}
				p.add(l)
			}
		}
	}
	if len(v.Related) > 0 {
		p.blank()
		var parts []string
		for _, r := range v.Related {
			part := r.Name
			if r.HasValue {
				part += " = " + r.Value
			}
			if r.HasPending {
				part += " (" + st.g().arrow + " " + r.Pending + ")"
			}
			parts = append(parts, part)
		}
		for _, l := range st.Wrap(v.RelatedHead.Text, 1) {
			p.add(l)
		}
		for _, l := range flow(parts, ", ", st.width()-3) {
			p.add("   " + st.dim(l))
		}
	}
	if len(v.Sources) > 0 {
		p.blank()
		for _, l := range st.Wrap(v.SourcesHead.Text, 1) {
			p.add(l)
		}
		for _, s := range v.Sources {
			for _, l := range st.Wrap(s.Title, 3) {
				p.add(l)
			}
			p.add("     " + st.dim(s.URL))
		}
	}
	p.blank()
	for i, t := range v.Summary {
		color := ""
		if i == 0 && v.Mismatches > 0 {
			color = cRed
		}
		for _, l := range st.Wrap(t.Text, 1) {
			p.add(st.paint(color, strings.TrimRight(l, " ")))
		}
	}
	return p.writeTo(w)
}
