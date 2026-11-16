package main

import (
	"context"
	"errors"
	"io"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/term"
	"github.com/agenticode/my/internal/view"
)

// runGPU is "my gpu [gpu]": the memory of the GPUs of the host as a repair
// budget, for every GPU with memory records or for the one the reader
// names by its index, serial number, UUID or PCI address.
func runGPU(ctx context.Context, env *cli.Env, args []string, src *source) int {
	o := newOptions(env, cmdName("gpu", src), src)
	days := o.fs.Int("days", 0, "")
	pos, lang, code, done := o.parse(env, args)
	if done {
		return code
	}
	if o.help {
		return printHelp(env, lang, o.style(env, lang), "gpu", src)
	}
	if len(pos) > 1 {
		return o.usageError(env, lang, "%s", say(lang, "err.extra", "arg", pos[1]))
	}
	if *days < 0 {
		return o.usageError(env, lang, "%s", say(lang, "err.days", "value", *days))
	}
	s, code := o.start(ctx, env, lang, src)
	if s == nil {
		return code
	}
	defer s.close()
	if code, ok := s.needHost(); !ok {
		return code
	}
	opts := view.GPUOptions{Days: *days}
	if len(pos) == 1 {
		opts.Ref = pos[0]
	}
	v, err := s.c.GPUs(ctx, s.h, opts)
	switch {
	case errors.Is(err, view.ErrNoSuchGPU):
		return o.usageError(env, lang, "%s", say(lang, "err.no_gpu", "ref", opts.Ref, "host", s.h.Host))
	case errors.Is(err, view.ErrAmbiguousDIMM):
		return o.usageError(env, lang, "%s", errText(lang, err))
	case err != nil:
		return o.failure(env, err)
	}
	return s.emit("gpu", v.Grade, v, func(w io.Writer) error { return term.GPUs(w, v, s.st) })
}
