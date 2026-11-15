package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/term"
	"github.com/agenticode/my/internal/view"
)

// runDiff is "my diff BEFORE AFTER": two stored cases of one host side by
// side, the host before something was done to it and after. Each case is
// read and analysed on its own; nothing is merged and no file is changed.
// The exit code is the grade of the host in the later case.
func runDiff(ctx context.Context, env *cli.Env, args []string) int {
	o := newOptions(env, "my diff", &source{})
	at := o.fs.String("at", "", "")
	all := o.fs.Bool("all", false, "")
	pos, lang, code, done := o.parse(env, args)
	if done {
		return code
	}
	if o.help {
		return printHelp(env, lang, o.style(env, lang), "diff", &source{})
	}
	switch {
	case len(pos) < 2:
		return o.usageError(env, lang, "%s", say(lang, "err.diff_files"))
	case len(pos) > 2:
		return o.usageError(env, lang, "%s", say(lang, "err.extra", "arg", pos[2]))
	}
	var changed time.Time
	if *at != "" {
		t, err := parseTime(*at)
		if err != nil {
			return o.usageError(env, lang, "%s", say(lang, "err.time", "flag", "--at", "value", *at))
		}
		changed = t
	}

	var sessions [2]*session
	for i, path := range pos {
		src, err := openCase(ctx, path)
		if err != nil {
			fmt.Fprintf(env.Stderr, "%s: %v\n", o.name, err)
			return cli.ExitFailure
		}
		defer src.cleanup()
		s, code := o.start(ctx, env, lang, src)
		if s == nil {
			return code
		}
		defer s.close()
		if s.h == nil {
			return o.usageError(env, lang, "%s: %s", path, say(lang, "err.several_hosts", "hosts", strings.Join(s.c.HostNames(), ", ")))
		}
		sessions[i] = s
	}
	a, b := sessions[0], sessions[1]

	cmp, err := view.Compare(ctx, a.c, a.h, b.c, b.h, view.CompareOptions{At: changed, All: *all})
	stamp := func(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") }
	switch {
	case errors.Is(err, view.ErrCaseOrder):
		return o.usageError(env, lang, "%s", say(lang, "err.diff_order",
			"first", pos[0], "first_at", stamp(a.h.AsOf), "second", pos[1], "second_at", stamp(b.h.AsOf)))
	case errors.Is(err, view.ErrChangeTime):
		return o.usageError(env, lang, "%s", say(lang, "err.diff_at",
			"value", *at, "first_at", stamp(a.h.AsOf), "second_at", stamp(b.h.AsOf)))
	case err != nil:
		return o.failure(env, err)
	}
	for i, st := range []*view.CaseState{&cmp.Before, &cmp.After} {
		st.File = pos[i]
		if m := sessions[i].src.manifest; m != nil {
			st.Anonymized = m.Anonymized
		}
	}

	exit := cmp.Grade.ExitCode()
	if o.asJSON {
		e := newEnvelope("diff", lang, cmp.Host, cmp.Grade, cmp)
		e.Analyzer = b.analyzerInfo()
		err = writeJSON(env.Stdout, e)
	} else {
		// Each case that is a bundle says what it is.
		for i, s := range sessions {
			if m := s.src.manifest; m != nil {
				for _, line := range b.st.Wrap(pos[i]+": "+caseLine(lang, m), 0) {
					fmt.Fprintln(env.Stdout, line)
				}
			}
		}
		b.analyzerLines(env.Stdout)
		err = term.Comparison(env.Stdout, cmp, b.st)
	}
	if err != nil {
		return o.failure(env, err)
	}
	return exit
}
