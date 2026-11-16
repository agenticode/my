package preflight

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/agenticode/my/internal/i18n"
)

// Script writes the fixes of the lost checks as a shell script, with what
// each costs, where that is written down and how to undo it in comments
// above its commands. MemorY never runs it: a person reads it and runs what
// they choose.
func Script(w io.Writer, rep *Report, lang i18n.Lang, now time.Time) error {
	bw := bufio.NewWriter(w)
	say := func(code string, kv ...any) string {
		return Text(lang, statement(code, kv...))
	}
	comment := func(s string) {
		for _, line := range strings.Split(s, "\n") {
			fmt.Fprintf(bw, "# %s\n", line)
		}
	}
	if now.IsZero() {
		now = time.Now()
	}
	fmt.Fprintln(bw, "#!/bin/sh")
	comment(say("pf.script.head", "host", rep.Host, "time", now.UTC().Format("2006-01-02 15:04")))
	comment(say("pf.script.read"))
	n := 0
	for _, c := range rep.Checks {
		if c.State != Lost || c.Fix == nil {
			continue
		}
		if n == 0 {
			fmt.Fprintln(bw, "set -e")
		}
		n++
		f := c.Fix
		fmt.Fprintln(bw)
		comment(c.Name + ": " + Text(lang, c.Detail))
		comment(Text(lang, f.What))
		comment(say("pf.script.cost", "text", Text(lang, f.Cost)))
		comment(say("pf.script.source", "text", f.Source))
		for _, u := range f.Undo {
			comment(say("pf.script.undo", "text", u))
		}
		if f.Reboot {
			comment(say("pf.script.reboot"))
		}
		if len(f.Commands) == 0 {
			switch {
			case f.Param != "" && f.Remove:
				comment(say("pf.script.unparam", "param", f.Param))
			case f.Param != "":
				comment(say("pf.script.param", "param", f.Param))
			default:
				comment(say("pf.script.manual"))
			}
			continue
		}
		for _, cmd := range f.Commands {
			fmt.Fprintln(bw, cmd)
		}
	}
	if n == 0 {
		fmt.Fprintln(bw)
		comment(say("pf.script.none"))
	}
	return bw.Flush()
}
