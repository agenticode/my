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
// with its state and what was found, and the count of lost checks with the
// command that prints their fixes.
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
	if r.Root != "" {
		p.add(" " + st.bold(say("pf.title.copy", "root", r.Root)))
	} else {
		p.add(" " + st.bold(say("pf.title")))
	}
	p.blank()

	stateW := 0
	for _, s := range []preflight.State{preflight.Kept, preflight.Lost, preflight.None, preflight.Unknown, preflight.Info} {
		stateW = max(stateW, Width(say("pf.state."+string(s))))
	}
	nameW := 0
	live := 0
	for _, c := range r.Checks {
		name := c.Name
		if r.Root != "" && c.Live {
			name += " *"
			live++
		}
		nameW = max(nameW, Width(name))
	}
	avail := st.width() - 1 - stateW - 2 - nameW - 2
	for _, c := range r.Checks {
		name := c.Name
		if r.Root != "" && c.Live {
			name += " *"
		}
		lines := wrap(preflight.Text(lang, c.Detail), avail)
		for i, line := range lines {
			if i == 0 {
				p.add(" " + st.paint(preflightColor(c.State), padRight(say("pf.state."+string(c.State)), stateW)) + "  " + padRight(name, nameW) + "  " + line)
				continue
			}
			p.add(" " + strings.Repeat(" ", stateW+2+nameW+2) + line)
		}
	}
	p.blank()
	if live > 0 {
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
