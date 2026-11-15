package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/term"
	"github.com/agenticode/my/internal/view"
)

// A command that shows findings runs on the host (src nil: it scans) or on
// a case that was opened (my open FILE command ...).
type viewFunc func(ctx context.Context, env *cli.Env, args []string, src *source) int

// viewCommands are the commands that show findings, by name.
var viewCommands = map[string]viewFunc{
	"dimm":     runDIMM,
	"why":      runWhy,
	"map":      runMap,
	"timeline": runTimeline,
	"gpu":      runGPU,
	"bios":     runBIOS,
	"verify":   runVerify,
	"export":   runExport,
	"bundle":   runBundle,
	"report":   runReport,
}

func live(fn viewFunc) func(context.Context, *cli.Env, []string) int {
	return func(ctx context.Context, env *cli.Env, args []string) int { return fn(ctx, env, args, nil) }
}

// cmdName returns the name a command goes by in messages.
func cmdName(name string, src *source) string {
	switch {
	case src != nil && name == "":
		return "my open"
	case src != nil:
		return "my open " + name
	case name == "":
		return "my"
	}
	return "my " + name
}

// runOverview is the command without a name: the slot map and the ranking
// of this host, or of the case that was opened.
func runOverview(ctx context.Context, env *cli.Env, args []string, src *source) int {
	o := newOptions(env, cmdName("", src), src)
	all := o.fs.Bool("all", false, "")
	pos, lang, code, done := o.parse(env, args)
	if done {
		return code
	}
	if o.help {
		return printHelp(env, lang, o.style(env, lang), "overview", src)
	}
	if len(pos) > 0 {
		// "my --root DIR why": the flags were read as those of a scan, and
		// the command stands where no argument belongs.
		if src == nil && app().Lookup(pos[0]) != nil {
			return o.usageError(env, lang, "%s", say(lang, "err.command_first", "cmd", pos[0]))
		}
		return o.usageError(env, lang, "%s", say(lang, "err.extra", "arg", pos[0]))
	}
	s, code := o.start(ctx, env, lang, src)
	if s == nil {
		return code
	}
	defer s.close()

	if s.h == nil {
		// Several hosts: list them, with the finding for each.
		hosts := s.c.Hosts()
		return s.emit("hosts", s.c.Result.Grade(), hosts, func(w io.Writer) error { return term.Hosts(w, hosts, s.st) })
	}
	ov, err := s.c.Overview(ctx, s.h, *all)
	if err != nil {
		return o.failure(env, err)
	}
	return s.emit("overview", s.h.Grade, ov, func(w io.Writer) error { return term.Overview(w, ov, s.st, s.hints(ov)) })
}

// hints names the commands that go on from the overview.
func (s *session) hints(ov *view.Overview) []term.Hint {
	var out []term.Hint
	// The DIMM to look at next: the subject of the case when it has one,
	// else the DIMM at the top of the ranking.
	slot := ""
	if len(ov.Ranking) > 0 {
		slot = ov.Ranking[0].Slot
	}
	if s.src != nil && s.src.manifest != nil && s.src.manifest.Subject != nil && s.src.manifest.Subject.Slot != "" {
		slot = s.src.manifest.Subject.Slot
	}
	if slot != "" {
		out = append(out,
			term.Hint{Command: s.prefix + " dimm " + slot, Text: say(s.lang, "hint.dimm")},
			term.Hint{Command: s.prefix + " why " + slot, Text: say(s.lang, "hint.why")})
	} else if ov.HostFinding != nil {
		// No DIMM to point at: the finding of the host is what to read.
		out = append(out, term.Hint{Command: s.prefix + " why", Text: say(s.lang, "hint.why")})
	}
	if s.src == nil {
		bundle := "my bundle -o case.mycase"
		if s.o.sf.set() {
			// The bundle of this host of the fleet, not of the machine the
			// command runs on.
			bundle = s.prefix + " bundle -o case.mycase"
		}
		out = append(out, term.Hint{Command: bundle, Text: say(s.lang, "hint.bundle")})
	}
	out = append(out, term.Hint{Command: "my help", Text: say(s.lang, "hint.help")})
	return out
}

// runDIMM is "my dimm <slot|serial>": the examination of one DIMM.
func runDIMM(ctx context.Context, env *cli.Env, args []string, src *source) int {
	o := newOptions(env, cmdName("dimm", src), src)
	pos, lang, code, done := o.parse(env, args)
	if done {
		return code
	}
	if o.help {
		return printHelp(env, lang, o.style(env, lang), "dimm", src)
	}
	if len(pos) != 1 {
		return o.usageError(env, lang, "%s", say(lang, "err.one_dimm"))
	}
	s, code := o.start(ctx, env, lang, src)
	if s == nil {
		return code
	}
	defer s.close()
	if code, ok := s.needHost(); !ok {
		return code
	}
	d, code := s.find(pos[0])
	if d == nil {
		return code
	}
	x, err := s.c.Examine(ctx, s.h, d, s.st.MapCells())
	if err != nil {
		return o.failure(env, err)
	}
	return s.emit("dimm", d.Verdict.Grade, x, func(w io.Writer) error { return term.Examination(w, x, s.st) })
}

// runWhy is "my why [slot|serial]": the finding and what it rests on, for
// one DIMM or for every DIMM that has one.
func runWhy(ctx context.Context, env *cli.Env, args []string, src *source) int {
	o := newOptions(env, cmdName("why", src), src)
	oneline := o.fs.Bool("oneline", false, "")
	all := o.fs.Bool("all", false, "")
	pos, lang, code, done := o.parse(env, args)
	if done {
		return code
	}
	if o.help {
		return printHelp(env, lang, o.style(env, lang), "why", src)
	}
	if len(pos) > 1 {
		return o.usageError(env, lang, "%s", say(lang, "err.extra", "arg", pos[1]))
	}
	s, code := o.start(ctx, env, lang, src)
	if s == nil {
		return code
	}
	defer s.close()
	if code, ok := s.needHost(); !ok {
		return code
	}

	grade := s.h.Grade
	var d *engine.DIMMResult
	if len(pos) == 1 {
		if d, code = s.find(pos[0]); d == nil {
			return code
		}
		grade = d.Verdict.Grade
	}
	cards, err := whyCards(ctx, s.c, s.h, d, *all)
	if err != nil {
		return o.failure(env, err)
	}
	return s.emit("why", grade, cards, func(w io.Writer) error {
		if !*oneline {
			return term.Why(w, cards, s.st)
		}
		for _, c := range cards {
			if _, err := fmt.Fprintln(w, term.Oneline(c, s.st)); err != nil {
				return err
			}
		}
		if len(cards) == 0 {
			_, err := fmt.Fprintln(w, s.h.Host+": "+view.Messages.Text(s.lang, "ov.none", nil))
			return err
		}
		return nil
	})
}

// parseBank reads the value of --bank: a bank address ("3"), or a bank
// group and a bank address ("0/3").
func parseBank(v string) (bg, bank model.Opt[int], err error) {
	num := func(s string) (model.Opt[int], error) {
		n, err := strconv.ParseInt(strings.TrimSpace(s), 0, 32)
		if err != nil || n < 0 {
			return model.Opt[int]{}, fmt.Errorf("%q", v)
		}
		return model.Some(int(n)), nil
	}
	if g, b, ok := strings.Cut(v, "/"); ok {
		if bg, err = num(g); err != nil {
			return
		}
		bank, err = num(b)
		return
	}
	bank, err = num(v)
	return
}

// parseRows reads the value of --rows: "0x1a00-0x1aff", or one row.
func parseRows(v string) (from, to model.Opt[int], err error) {
	num := func(s string) (model.Opt[int], error) {
		n, err := strconv.ParseInt(strings.TrimSpace(s), 0, 32)
		if err != nil || n < 0 {
			return model.Opt[int]{}, fmt.Errorf("%q", v)
		}
		return model.Some(int(n)), nil
	}
	a, b, ok := strings.Cut(v, "-")
	if !ok {
		b = a
	}
	if from, err = num(a); err != nil {
		return
	}
	if to, err = num(b); err != nil {
		return
	}
	if to.V < from.V {
		err = fmt.Errorf("%q", v)
	}
	return
}

// runMap is "my map <slot|serial>": the address map of one bank.
func runMap(ctx context.Context, env *cli.Env, args []string, src *source) int {
	o := newOptions(env, cmdName("map", src), src)
	bank := o.fs.String("bank", "", "")
	rank := o.fs.Int("rank", -1, "")
	device := o.fs.Int("device", -1, "")
	rows := o.fs.String("rows", "", "")
	pos, lang, code, done := o.parse(env, args)
	if done {
		return code
	}
	if o.help {
		return printHelp(env, lang, o.style(env, lang), "map", src)
	}
	if len(pos) != 1 {
		return o.usageError(env, lang, "%s", say(lang, "err.one_dimm"))
	}
	var mo view.MapOptions
	var err error
	if *bank != "" {
		if mo.BankGroup, mo.Bank, err = parseBank(*bank); err != nil {
			return o.usageError(env, lang, "%s", say(lang, "err.bank", "value", *bank))
		}
	}
	if *rows != "" {
		if mo.RowFrom, mo.RowTo, err = parseRows(*rows); err != nil {
			return o.usageError(env, lang, "%s", say(lang, "err.rows", "value", *rows))
		}
	}
	if *rank >= 0 {
		mo.Rank = model.Some(*rank)
	}
	if *device >= 0 {
		mo.Device = model.Some(*device)
	}
	s, code := o.start(ctx, env, lang, src)
	if s == nil {
		return code
	}
	defer s.close()
	if code, ok := s.needHost(); !ok {
		return code
	}
	d, code := s.find(pos[0])
	if d == nil {
		return code
	}
	mo.Cells = s.st.MapCells()
	m, err := s.c.AddressMap(ctx, s.h, d, mo)
	if err != nil {
		// A bank without errors is a wrong choice, not a failure.
		return o.usageError(env, lang, "%v", err)
	}
	return s.emit("map", d.Verdict.Grade, m, func(w io.Writer) error { return term.AddressMap(w, m, s.st) })
}

// runTimeline is "my timeline [slot|serial]": errors per day and events,
// for one DIMM or for the host.
func runTimeline(ctx context.Context, env *cli.Env, args []string, src *source) int {
	o := newOptions(env, cmdName("timeline", src), src)
	days := o.fs.Int("days", 0, "")
	pos, lang, code, done := o.parse(env, args)
	if done {
		return code
	}
	if o.help {
		return printHelp(env, lang, o.style(env, lang), "timeline", src)
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
	grade := s.h.Grade
	var d *engine.DIMMResult
	if len(pos) == 1 {
		if d, code = s.find(pos[0]); d == nil {
			return code
		}
		grade = d.Verdict.Grade
	}
	tl, err := s.c.Timeline(ctx, s.h, d, view.TimelineOptions{Days: *days})
	if err != nil {
		return o.failure(env, err)
	}
	return s.emit("timeline", grade, tl, func(w io.Writer) error { return term.Timeline(w, tl, s.st) })
}
