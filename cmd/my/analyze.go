package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/term"
	"github.com/agenticode/my/internal/view"
)

// analyzeCommand runs the analysis engine over a ledger and prints the
// finding for every DIMM. Its exit code is the grade of the worst finding.
// It is hidden from the command list: the commands that scan a host and
// draw the findings build on the same engine, and this one exists for
// scripts, tests and development.
func analyzeCommand() *cli.Command {
	return &cli.Command{
		Name:    "analyze",
		Summary: "run the analysis engine over a ledger and print the findings",
		Hidden:  true,
		Run:     runAnalyze,
	}
}

// labels are the words of the plain-text output.
var labels = i18n.Catalog{
	"host":       {EN: "host {host}, as of {when}, engine {version}", KO: "호스트 {host}, 기준 시각 {when}, 엔진 {version}"},
	"host.grade": {EN: "host finding: {grade}", KO: "호스트 소견: {grade}"},
	"none":       {EN: "no host in the ledger", KO: "원장에 호스트 없음"},
	"no.host":    {EN: "no host named {host} in the ledger", KO: "원장에 {host} 호스트 없음"},
	"quiet":      {EN: "{n} {n|DIMM|DIMMs} without errors", KO: "오류 없는 DIMM {n}개"},
	"gone":       {EN: "no longer in the host", KO: "호스트에서 빠짐"},
	"confidence": {EN: "confidence {level}, {n}", KO: "신뢰도 {level}, {n}"},
	"loose":      {EN: "{n} {n|error|errors} of this host {n|is|are} tied to no DIMM", KO: "이 호스트의 오류 {n}건은 어느 DIMM에도 묶이지 않음"},
	"saved":      {EN: "faults and findings stored in {file}", KO: "고장과 소견을 {file}에 저장"},
}

func runAnalyze(ctx context.Context, env *cli.Env, args []string) int {
	fl := flag.NewFlagSet("my analyze", flag.ContinueOnError)
	fl.SetOutput(env.Stderr)
	ledger := fl.String("ledger", "", "ledger `file` to analyse")
	host := fl.String("host", "", "analyse one host of the ledger, by `name`")
	asOf := fl.String("as-of", "", "analyse the ledger as it was at `time` (2006-01-02 or RFC 3339); later records are left out")
	lang := fl.String("lang", "en", "language of the output: en or ko")
	asJSON := fl.Bool("json", false, "print the result as JSON")
	all := fl.Bool("all", false, "also list the DIMMs without errors")
	save := fl.Bool("save", false, "store the faults and findings in the ledger")
	rules := fl.Bool("rules", false, "list the rules and exit")
	thresholds := fl.Bool("thresholds", false, "list the thresholds with their keys and defaults and exit")
	var cf engine.ConfigFlags
	cf.Register(fl)
	fl.Usage = func() {
		fmt.Fprintf(env.Stderr, "Usage: my analyze -ledger file [-host name] [-as-of time] [-config file] [-set key=value] [-lang en|ko] [-json] [-all] [-save]\n"+
			"       my analyze -rules | -thresholds\n\n"+
			"Runs the analysis engine over a ledger and prints the finding for every DIMM.\n"+
			"Exit code: 0 ok, 1 observe, 2 consider replacement, 3 replace now, 4 hold replacement.\n\n")
		fl.PrintDefaults()
	}
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return cli.ExitOK
		}
		return cli.ExitUsage
	}
	usage := func(format string, a ...any) int {
		fmt.Fprintf(env.Stderr, "my analyze: "+format+"\n", a...)
		return cli.ExitUsage
	}
	if fl.NArg() > 0 {
		return usage("unexpected argument %q", fl.Arg(0))
	}
	lg, err := i18n.ParseLang(*lang)
	if err != nil {
		return usage("%v", err)
	}
	cfg, err := cf.Config()
	if err != nil {
		return usage("%v", err)
	}
	switch {
	case *rules:
		printRules(env.Stdout, lg)
		return cli.ExitOK
	case *thresholds:
		printThresholds(env.Stdout, &cfg)
		return cli.ExitOK
	case *ledger == "":
		return usage("give the ledger with -ledger")
	}
	opts := engine.Options{Config: &cfg, Host: *host}
	if *asOf != "" {
		t, err := parseTime(*asOf)
		if err != nil {
			return usage("-as-of %s: want a date (2006-01-02) or a time (2006-01-02T15:04:05Z)", *asOf)
		}
		opts.AsOf = t
	}

	fail := func(err error) int {
		fmt.Fprintf(env.Stderr, "my analyze: %v\n", err)
		return cli.ExitFailure
	}
	// The ledger is only opened for writing when the result is stored.
	mode := store.ReadOnly
	if *save {
		mode = store.ReadWrite
	}
	s, err := store.Open(ctx, *ledger, mode)
	if err != nil {
		return fail(err)
	}
	defer s.Close()
	res, err := engine.Run(ctx, s, opts)
	if err != nil {
		return fail(err)
	}
	if *host != "" && len(res.Hosts) == 0 {
		return fail(errors.New(labels.Text(i18n.EN, "no.host", map[string]any{"host": *host})))
	}
	if *save {
		if err := res.Save(ctx, s); err != nil {
			return fail(err)
		}
	}
	if *asJSON {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			return fail(err)
		}
	} else {
		printResult(env.Stdout, lg, res, *all)
		if *save {
			fmt.Fprintln(env.Stdout, labels.Text(lg, "saved", map[string]any{"file": s.Path()}))
		}
	}
	if err := s.Close(); err != nil {
		return fail(err)
	}
	return res.ExitCode()
}

func parseTime(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot read %q as a time", s)
}

func printRules(w io.Writer, lg i18n.Lang) {
	for _, r := range engine.Rules {
		fmt.Fprintf(w, "%-10s  %-20s  %s\n", r.ID, engine.GradeText(lg, r.Grade), engine.Messages.Text(lg, r.Condition, nil))
	}
}

func printThresholds(w io.Writer, cfg *engine.Config) {
	for _, p := range engine.Params() {
		value, _ := cfg.Get(p.Key)
		note := ""
		if value != p.Default {
			note = fmt.Sprintf(" (default %s)", p.Default)
		}
		fmt.Fprintf(w, "%-28s = %s%s\n    %s\n", p.Key, value, note, p.Help)
	}
}

// printResult writes the findings as plain text: one block per DIMM that
// has a finding other than ok, worst first.
func printResult(w io.Writer, lg i18n.Lang, res *engine.Result, all bool) {
	say := func(code string, kv ...any) string {
		args := map[string]any{}
		for i := 0; i+1 < len(kv); i += 2 {
			args[kv[i].(string)] = kv[i+1]
		}
		return labels.Text(lg, code, args)
	}
	if len(res.Hosts) == 0 {
		fmt.Fprintln(w, say("none"))
		return
	}
	// Labels are padded to the same width in display columns; a Korean
	// syllable takes two.
	width := 0
	// The words of the card are the ones the screens use.
	card := func(code string) string { return view.Messages.Text(lg, "card."+code, nil) }
	for _, code := range []string{"finding", "evidence", "checks", "missing", "next"} {
		width = max(width, term.Width(card(code)))
	}
	line := func(label, text string) {
		fmt.Fprintf(w, "  %s%s  %s\n", label, strings.Repeat(" ", width-term.Width(label)), text)
	}
	for i, h := range res.Hosts {
		if i > 0 {
			fmt.Fprintln(w)
		}
		when := "-"
		if !h.AsOf.IsZero() {
			when = h.AsOf.UTC().Format("2006-01-02 15:04") + " UTC"
		}
		fmt.Fprintln(w, say("host", "host", h.Host, "when", when, "version", res.EngineVersion))
		fmt.Fprintln(w, say("host.grade", "grade", engine.GradeText(lg, h.Grade)))
		quiet := 0
		for _, d := range h.DIMMs {
			v := d.Verdict
			if v.Grade == model.VerdictOK && len(v.CounterEvidence) == 0 && !all {
				quiet++
				continue
			}
			fmt.Fprintln(w)
			head := d.Slot
			if d.DIMM != nil && d.DIMM.Serial != "" {
				head += "  S/N " + d.DIMM.Serial
			}
			head += fmt.Sprintf("  CE %s  UE %s", i18n.Thousands(d.Features.CE), i18n.Thousands(d.Features.UE))
			if len(d.Faults) > 0 {
				f := d.Faults[0]
				head += "  " + engine.ModeText(lg, f.Mode)
				if f.Estimated {
					head += " (" + engine.ModeText(lg, "estimated") + ")"
				}
			}
			if !d.Present {
				head += "  (" + say("gone") + ")"
			}
			fmt.Fprintln(w, head)
			conf := say("confidence", "level", engine.Messages.Text(lg, "confidence."+engine.ConfidenceLevel(v.Confidence), nil), "n", v.Confidence)
			line(card("finding"), fmt.Sprintf("%s (%s, %s)  %s", engine.GradeText(lg, v.Grade), engine.Messages.Text(lg, "label.finding", nil), conf, strings.Join(v.Rules, " ")))
			label := card("evidence")
			for _, e := range v.Evidence {
				line(label, fmt.Sprintf("[%s] %s", e.Rule, engine.Text(lg, e.Statement)))
				label = ""
			}
			label = card("checks")
			for _, c := range v.CounterEvidence {
				line(label, fmt.Sprintf("%s: %s", view.Messages.Text(lg, "effect."+c.Effect, nil), engine.Text(lg, c.Statement)))
				label = ""
			}
			label = card("missing")
			for _, m := range v.Missing {
				line(label, engine.Text(lg, m))
				label = ""
			}
			if v.NextAction.Code != "" {
				line(card("next"), engine.Text(lg, v.NextAction))
			}
		}
		for _, g := range h.GPUs {
			v := g.Verdict
			fmt.Fprintf(w, "\nGPU %s\n", g.Key)
			line(card("finding"), fmt.Sprintf("%s (%s)  %s", engine.GradeText(lg, v.Grade), engine.Messages.Text(lg, "label.finding", nil), strings.Join(v.Rules, " ")))
			label := card("evidence")
			for _, e := range v.Evidence {
				line(label, fmt.Sprintf("[%s] %s", e.Rule, engine.Text(lg, e.Statement)))
				label = ""
			}
			line(card("next"), engine.Text(lg, v.NextAction))
		}
		if v := h.Loose; v != nil {
			// The host finding: about what the records tie to no DIMM.
			fmt.Fprintf(w, "\n%s\n", view.Messages.Text(lg, "ov.host_finding", nil))
			line(card("finding"), fmt.Sprintf("%s (%s)  %s", engine.GradeText(lg, v.Grade), engine.Messages.Text(lg, "label.finding", nil), strings.Join(v.Rules, " ")))
			label := card("evidence")
			for _, e := range v.Evidence {
				line(label, fmt.Sprintf("[%s] %s", e.Rule, engine.Text(lg, e.Statement)))
				label = ""
			}
			label = card("checks")
			for _, k := range v.CounterEvidence {
				line(label, view.Messages.Text(lg, "effect."+k.Effect, nil)+": "+engine.Text(lg, k.Statement))
				label = ""
			}
			label = card("missing")
			for _, m := range v.Missing {
				line(label, engine.Text(lg, m))
				label = ""
			}
			line(card("next"), engine.Text(lg, v.NextAction))
		}
		if quiet > 0 {
			fmt.Fprintf(w, "\n%s\n", say("quiet", "n", quiet))
		}
		var loose int64
		for _, u := range h.Unattributed {
			loose += u.CE + u.UE
		}
		if loose > 0 {
			fmt.Fprintln(w, say("loose", "n", loose))
		}
	}
}
