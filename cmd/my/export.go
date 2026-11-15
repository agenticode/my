package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/agenticode/my/internal/buildinfo"
	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/fmfmlog"
	"github.com/agenticode/my/internal/outfile"
)

// exportFormats are the forms "my export" writes.
var exportFormats = []string{"fmfm"}

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
	case pos[0] != "fmfm":
		return o.usageError(env, lang, "%s", say(lang, "err.export_unknown", "format", pos[0]))
	case len(pos) > 1:
		return o.usageError(env, lang, "%s", say(lang, "err.extra", "arg", pos[1]))
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
