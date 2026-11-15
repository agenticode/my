package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/agenticode/my/internal/buildinfo"
	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/plugin"
	"github.com/agenticode/my/internal/report"
	"github.com/agenticode/my/internal/store"
)

// pluginFlags are the flags that name a vendor analyzer.
type pluginFlags struct {
	path    string
	timeout string
	logs    int
}

func (p *pluginFlags) register(o *options) {
	o.fs.StringVar(&p.path, "plugin", "", "")
	o.fs.StringVar(&p.timeout, "plugin-timeout", "", "")
	o.fs.IntVar(&p.logs, "plugin-logs", 0, "")
}

// analyzerInfo names the vendor analyzer a run used, in the JSON output.
type analyzerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Vendor  string `json:"vendor,omitzero"`
	// Notes are remarks about its response.
	Notes []string `json:"notes,omitzero"`
}

// analyze runs the vendor analyzer the reader named and hands what it said
// about rows and baselines to the engine. Without --plugin it does
// nothing. A run that asked for an analyzer and cannot have it stops: a
// finding that lacks what was asked for is not what the reader takes it
// for.
func (o *options) analyze(ctx context.Context, env *cli.Env, lang i18n.Lang, ledger *store.Store, eopts *engine.Options, now time.Time) (*plugin.Result, int, bool) {
	p := &o.pf
	switch {
	case p.path == "" && (p.timeout != "" || p.logs != 0):
		return nil, o.usageError(env, lang, "%s", say(lang, "err.plugin_alone")), false
	case p.path == "":
		return nil, 0, true
	case p.logs < 0:
		return nil, o.usageError(env, lang, "%s", say(lang, "err.plugin_logs", "value", p.logs)), false
	}
	opts := plugin.Options{Path: p.path}
	if p.timeout != "" {
		d, err := time.ParseDuration(p.timeout)
		if err != nil || d < 100*time.Millisecond {
			return nil, o.usageError(env, lang, "%s", say(lang, "err.plugin_timeout", "value", p.timeout)), false
		}
		opts.Timeout = d
	}
	req, err := plugin.BuildRequest(ctx, ledger, plugin.RequestOptions{Tool: "MemorY", Version: buildinfo.Version, Now: now, PreviousLogs: p.logs})
	if err != nil {
		return nil, o.failure(env, err), false
	}
	res, err := plugin.Run(ctx, opts, req)
	if err != nil {
		var perr *plugin.Error
		if errors.As(err, &perr) {
			// The reason is the analyzer's own or about its response, in
			// English; what to do about it is in the reader's language.
			err = fmt.Errorf("%w\n%s", err, say(lang, "plugin.failed"))
		}
		return nil, o.failure(env, err), false
	}
	eopts.Adjacency, eopts.PartBaseline = res.Adjacency(), res.PartBaseline()
	return res, 0, true
}

// analyzerLine says which vendor analyzer a run used.
func analyzerLine(lang i18n.Lang, res *plugin.Result) string {
	name := res.Analyzer.Name + " " + res.Analyzer.Version
	if res.Analyzer.Vendor != "" {
		name += " (" + res.Analyzer.Vendor + ")"
	}
	return say(lang, "plugin.used", "name", name, "dimms", len(res.DIMMs))
}

// level2 returns what the analyzer said beyond the public classification,
// in the form a report for failure analysis takes.
func level2(res *plugin.Result) []report.Level2 {
	if res == nil {
		return nil
	}
	var out []report.Level2
	name := res.Analyzer.Name + " " + res.Analyzer.Version
	for id, d := range res.DIMMs {
		if d.Level2 == nil || (len(d.Level2.Lines) == 0 && len(d.Level2.Notes) == 0) {
			continue
		}
		l := report.Level2{DIMMUID: id, Analyzer: name, Notes: d.Level2.Notes}
		for _, line := range d.Level2.Lines {
			l.Lines = append(l.Lines, report.Level2Line{Label: line.Label, Value: line.Value})
		}
		out = append(out, l)
	}
	return out
}

// analyzerInfo names the vendor analyzer of the run for the JSON envelope,
// or returns nil when none took part.
func (s *session) analyzerInfo() *analyzerInfo {
	a := s.analyzer
	if a == nil {
		return nil
	}
	return &analyzerInfo{Name: a.Analyzer.Name, Version: a.Analyzer.Version, Vendor: a.Analyzer.Vendor, Notes: a.Notes}
}

// analyzerLines says on a screen that a vendor analyzer took part, with
// its remarks: it can change which rows count as one fault.
func (s *session) analyzerLines(w io.Writer) {
	a := s.analyzer
	if a == nil {
		return
	}
	for _, line := range s.st.Wrap(analyzerLine(s.lang, a), 0) {
		fmt.Fprintln(w, line)
	}
	for _, n := range a.Notes {
		for _, line := range s.st.Wrap(say(s.lang, "plugin.note", "note", n), 2) {
			fmt.Fprintln(w, line)
		}
	}
}
