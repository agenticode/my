package term

import (
	"io"
	"strings"

	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/preflight"
)

// preflightColor returns the colour of a state. The state is always
// written as a word; the colour repeats it.
func preflightColor(s preflight.State) string {
	switch s {
	case preflight.Kept:
		return cGreen
	case preflight.Lost:
		return cBoldRed
	case preflight.Unknown:
		return cYellow
	case preflight.Info:
		return cCyan
	}
	return cDim
}

// Preflight draws what a reset would leave on a host: one line per check
// with its state and what was found, the lost ones first, and the count of
// lost checks with the command that prints their fixes. On a copy of a
// host's files the checks that need the running host share one line;
// st.Detail lists them one by one, in the order of the report.
func Preflight(w io.Writer, r *preflight.Report, st Style) error {
	p := &page{st: st}
	lang := st.lang()
	say := func(code string, kv ...any) string {
		var args map[string]any
		if len(kv) > 0 {
			args = make(map[string]any, len(kv)/2)
			for i := 0; i+1 < len(kv); i += 2 {
				args[kv[i].(string)] = kv[i+1]
			}
		}
		return preflight.Messages.Text(lang, code, args)
	}
	head := []string{say("pf.head.host", "name", st.bold(r.Host))}
	if r.Kernel != "" {
		head = append(head, say("pf.head.kernel", "release", r.Kernel))
	}
	if r.Arch != "" {
		head = append(head, r.Arch)
	}
	if r.CPU != "" {
		head = append(head, r.CPU)
	}
	if !r.Booted.IsZero() {
		head = append(head, say("pf.head.booted", "time", r.Booted.UTC().Format("2006-01-02 15:04")))
	}
	for _, line := range flow(head, "  ", st.width()) {
		p.add(line)
	}
	p.blank()
	title := say("pf.title")
	if r.Root != "" {
		title = say("pf.title.copy", "root", r.Root)
	}
	for _, l := range wrap(title, st.width()-1) {
		p.add(" " + st.bold(l))
	}
	p.blank()

	// In short: the lost checks first, the rest in order, and the checks
	// that a copy cannot answer by name in one line.
	checks := r.Checks
	var needLive []string
	if !st.Detail {
		var lost, rest []preflight.Check
		for _, c := range r.Checks {
			switch {
			case r.Root != "" && c.Live && c.State == preflight.Unknown:
				needLive = append(needLive, c.Name)
			case c.State == preflight.Lost:
				lost = append(lost, c)
			default:
				rest = append(rest, c)
			}
		}
		checks = append(lost, rest...)
	}
	// In short, checks with the same state and the same finding share a
	// line that names them all, where the first of them stands.
	type entry struct {
		c     preflight.Check
		names []string
	}
	var entries []entry
	at := map[string]int{}
	for _, c := range checks {
		key := string(c.State) + "\x00" + preflight.Text(lang, c.Detail)
		if i, ok := at[key]; ok && !st.Detail {
			entries[i].names = append(entries[i].names, c.Name)
			continue
		}
		at[key] = len(entries)
		entries = append(entries, entry{c: c, names: []string{c.Name}})
	}

	stateW := 0
	for _, s := range []preflight.State{preflight.Kept, preflight.Lost, preflight.None, preflight.Unknown, preflight.Info} {
		stateW = max(stateW, Width(say("pf.state."+string(s))))
	}
	mark := func(c preflight.Check) string {
		if st.Detail && r.Root != "" && c.Live {
			return c.Name + " *"
		}
		return c.Name
	}
	nameW := 0
	live := 0
	for _, c := range r.Checks {
		if r.Root != "" && c.Live {
			live++
		}
	}
	for _, e := range entries {
		if len(e.names) == 1 {
			nameW = max(nameW, Width(mark(e.c)))
		}
	}
	avail := st.width() - 1 - stateW - 2 - nameW - 2
	for _, e := range entries {
		c := e.c
		if len(e.names) > 1 {
			// The names and the finding they share, across both columns.
			text := strings.Join(e.names, ", ") + ": " + preflight.Text(lang, c.Detail)
			for i, line := range wrap(text, st.width()-1-stateW-2) {
				head := strings.Repeat(" ", stateW)
				if i == 0 {
					head = st.paint(preflightColor(c.State), padRight(say("pf.state."+string(c.State)), stateW))
				}
				p.add(" " + head + "  " + line)
			}
			continue
		}
		name := mark(c)
		lines := wrap(preflight.Text(lang, c.Detail), avail)
		for i, line := range lines {
			if i == 0 {
				p.add(" " + st.paint(preflightColor(c.State), padRight(say("pf.state."+string(c.State)), stateW)) + "  " + padRight(name, nameW) + "  " + line)
				continue
			}
			p.add(" " + strings.Repeat(" ", stateW+2+nameW+2) + line)
		}
	}
	if len(needLive) > 0 {
		text := say("pf.copy.group", "n", len(needLive), "names", strings.Join(needLive, ", "))
		for i, line := range wrap(text, st.width()-1-stateW-2) {
			head := strings.Repeat(" ", stateW)
			if i == 0 {
				head = st.paint(preflightColor(preflight.Unknown), padRight(say("pf.state.unknown"), stateW))
			}
			p.add(" " + head + "  " + line)
		}
	}
	p.blank()
	if live > 0 && st.Detail {
		for _, line := range st.Wrap(say("pf.copy.note", "n", live), 1) {
			p.add(st.dim(line))
		}
	}
	if r.Lost == 0 {
		for _, line := range st.Wrap(say("pf.summary.none"), 1) {
			p.add(line)
		}
	} else {
		cmd := "my preflight --script"
		if r.Root != "" {
			cmd += " --root " + r.Root
		}
		if lang == i18n.KO {
			cmd += " --lang ko"
		}
		for _, line := range st.Wrap(say("pf.summary", "lost", r.Lost, "n", len(r.Checks)), 1) {
			p.add(line)
		}
		p.add("   " + cmd)
	}
	return p.writeTo(w)
}
