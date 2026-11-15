package main

import (
	"context"
	"io"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/injection"
	"github.com/agenticode/my/internal/term"
)

// exitDiffers is the exit code of "my verify" when the records of the
// host are not what the plan says.
const exitDiffers = 1

// runVerify is "my verify --expect PLAN": the comparison of an injection
// plan with what the sources of the host recorded. Its exit code says
// whether every injection was found as planned; it is no grade.
func runVerify(ctx context.Context, env *cli.Env, args []string, src *source) int {
	o := newOptions(env, cmdName("verify", src), src)
	expect := o.fs.String("expect", "", "")
	pos, lang, code, done := o.parse(env, args)
	if done {
		return code
	}
	if o.help {
		return printHelp(env, lang, o.style(env, lang), "verify", src)
	}
	switch {
	case len(pos) > 0:
		return o.usageError(env, lang, "%s", say(lang, "err.extra", "arg", pos[0]))
	case *expect == "":
		return o.usageError(env, lang, "%s", say(lang, "err.expect"))
	}
	// The plan first: a plan that cannot be read needs no scan.
	plan, err := injection.ReadFile(*expect)
	if err != nil {
		return o.usageError(env, lang, "--expect %v", err)
	}
	s, code := o.start(ctx, env, lang, src)
	if s == nil {
		return code
	}
	defer s.close()
	if code, ok := s.needHost(); !ok {
		return code
	}
	v, err := s.c.Verify(ctx, s.h, plan)
	if err != nil {
		return o.failure(env, err)
	}
	exit := cli.ExitOK
	if !v.OK {
		exit = exitDiffers
	}
	return s.emitCode("verify", "", exit, v, func(w io.Writer) error { return term.Verification(w, v, s.st) })
}
