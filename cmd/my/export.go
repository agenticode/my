package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/agenticode/my/internal/buildinfo"
	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/eventstream"
	"github.com/agenticode/my/internal/fmfmlog"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/outfile"
)

// exportFormats are the forms "my export" writes.
var exportFormats = []string{"fmfm", "events", "labels"}

// runExport is "my export fmfm": the findings of a host as an analysis
// log in the form of the FMFM framework. The log goes to standard output,
// or with -o into a file. It holds findings and suggestions for a person
// and never an action descriptor.
func runExport(ctx context.Context, env *cli.Env, args []string, src *source) int {
	o := newOptions(env, cmdName("export", src), src)
	out := o.fs.String("o", "", "")
	records := o.fs.Bool("records", false, "")
	all := o.fs.Bool("all", false, "")
	force := o.fs.Bool("force", false, "")
	// A scan of this host already takes --since for the store of a fleet;
	// for the stream it also cuts the records.
	since, ownSince := &o.sf.since, false
	if o.fs.Lookup("since") == nil {
		since, ownSince = o.fs.String("since", "", ""), true
	}
	raw := o.fs.Bool("raw", false, "")
	var lf labelFlags
	o.fs.StringVar(&lf.names, "names", "", "")
	o.fs.StringVar(&lf.vendor, "vendor", "", "")
	o.fs.StringVar(&lf.model, "model", "", "")
	o.fs.StringVar(&lf.product, "product", "", "")
	pos, lang, code, done := o.parse(env, args)
	if done {
		return code
	}
	if o.help {
		return printHelp(env, lang, o.style(env, lang), "export", src)
	}
	switch {
	case len(pos) == 0:
		return o.usageError(env, lang, "%s", say(lang, "err.export_format"))
	case pos[0] != "fmfm" && pos[0] != "labels" && pos[0] != "events":
		return o.usageError(env, lang, "%s", say(lang, "err.export_unknown", "format", pos[0]))
	case len(pos) > 1:
		return o.usageError(env, lang, "%s", say(lang, "err.extra", "arg", pos[1]))
	}
	if pos[0] != "events" {
		for name, set := range map[string]bool{"--since": ownSince && *since != "", "--raw": *raw} {
			if set {
				return o.usageError(env, lang, "%s", say(lang, "err.events_flag", "flag", name))
			}
		}
	}
	if pos[0] == "events" {
		cut := *since
		at, err := parseSince(cut, clock(env))
		if err != nil {
			return o.usageError(env, lang, "%s", say(lang, "err.since", "value", cut))
		}
		if !o.sf.set() {
			// Without a fleet the flag is the cut of the stream alone.
			o.sf.since = ""
		}
		return exportEvents(ctx, env, o, lang, src, *out, *force, eventstream.Options{Since: at, Raw: *raw})
	}
	if pos[0] == "labels" {
		// The DIMM label file of rasdaemon, written from the slot names.
		return exportLabels(ctx, env, o, lang, src, lf, *out, *force)
	}
	toFile := *out != "" && *out != "-"
	if toFile {
		// A file that cannot be written is found before the host is scanned.
		if code, ok := o.checkTarget(env, lang, *out, *force); !ok {
			return code
		}
	}
	s, code := o.start(ctx, env, lang, src)
	if s == nil {
		return code
	}
	defer s.close()
	if code, ok := s.needHost(); !ok {
		return code
	}
	bopts := fmfmlog.BuildOptions{Lang: lang, Now: clock(env), Tool: "MemorY", Version: buildinfo.Version, All: *all, Records: *records}
	if a := s.analyzer; a != nil {
		bopts.VendorAnalyzer = &fmfmlog.VendorAnalyzer{Name: a.Analyzer.Name, Version: a.Analyzer.Version, Vendor: a.Analyzer.Vendor}
	}
	// The log is written as it is read from the ledger: with --records it
	// holds every error record of the host.
	var doc *fmfmlog.Document
	var written int
	var failed error
	write := func(w io.Writer) error {
		doc, written, failed = fmfmlog.Stream(ctx, w, s.c.Store(), s.h, bopts)
		return failed
	}
	if !toFile {
		// The log is the output: it is JSON with or without --json.
		if err := write(env.Stdout); err != nil {
			return o.failure(env, err)
		}
		return cli.ExitOK
	}
	size, err := outfile.Write(*out, *force, write)
	switch {
	case failed != nil:
		// The log could not be made; the target is as it was.
		return o.failure(env, failed)
	case err != nil:
		return o.failure(env, errors.New(targetText(lang, err)))
	}
	if o.asJSON {
		res := struct {
			Path    string `json:"path"`
			Bytes   int    `json:"bytes"`
			Format  string `json:"format"`
			Host    string `json:"host"`
			DIMMs   int    `json:"dimms"`
			Records int    `json:"records"`
		}{*out, int(size), fmfmlog.Format, s.h.Host, len(doc.AnalysisLog), written}
		if err := writeJSON(env.Stdout, res); err != nil {
			return o.failure(env, err)
		}
		return cli.ExitOK
	}
	fmt.Fprintln(env.Stdout, say(lang, "export.written", "file", *out, "size", sizeText(size)))
	fmt.Fprintln(env.Stdout, "  "+say(lang, "export.holds", "host", s.h.Host, "dimms", len(doc.AnalysisLog), "records", written,
		"grade", engine.GradeText(lang, s.h.Grade)))
	fmt.Fprintln(env.Stdout, "  "+say(lang, "export.no_cpad"))
	return cli.ExitOK
}

// exportEvents is "my export events": every record of the host as one line
// of JSON in the schema my-events/1, to standard output or with -o into a
// file.
func exportEvents(ctx context.Context, env *cli.Env, o *options, lang i18n.Lang, src *source, out string, force bool, opts eventstream.Options) int {
	toFile := out != "" && out != "-"
	if toFile {
		if code, ok := o.checkTarget(env, lang, out, force); !ok {
			return code
		}
	}
	s, code := o.start(ctx, env, lang, src)
	if s == nil {
		return code
	}
	defer s.close()
	if code, ok := s.needHost(); !ok {
		return code
	}
	var written int
	var failed error
	write := func(w io.Writer) error {
		written, failed = eventstream.Write(ctx, w, s.c.Store(), s.h.Host, opts)
		return failed
	}
	if !toFile {
		// The stream is the output, with or without --json.
		if err := write(env.Stdout); err != nil {
			return o.failure(env, err)
		}
		return cli.ExitOK
	}
	size, err := outfile.Write(out, force, write)
	switch {
	case failed != nil:
		return o.failure(env, failed)
	case err != nil:
		return o.failure(env, errors.New(targetText(lang, err)))
	}
	if o.asJSON {
		res := struct {
			Path    string `json:"path"`
			Bytes   int    `json:"bytes"`
			Format  string `json:"format"`
			Host    string `json:"host"`
			Records int    `json:"records"`
		}{out, int(size), eventstream.Schema, s.h.Host, written}
		if err := writeJSON(env.Stdout, res); err != nil {
			return o.failure(env, err)
		}
		return cli.ExitOK
	}
	fmt.Fprintln(env.Stdout, say(lang, "events.written", "file", out, "size", sizeText(size)))
	fmt.Fprintln(env.Stdout, "  "+say(lang, "events.holds", "host", s.h.Host, "n", written, "schema", eventstream.Schema))
	return cli.ExitOK
}
