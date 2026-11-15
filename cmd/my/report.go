package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/outfile"
	"github.com/agenticode/my/internal/report"
)

// runReport is "my report [FILE] -o OUT": the findings of a case as one
// HTML file. FILE is a case bundle or a ledger; without it the host is
// scanned, as the other commands do. With --figures the figures of the
// report are also written as SVG files of their own.
func runReport(ctx context.Context, env *cli.Env, args []string, src *source) int {
	o := newOptions(env, cmdName("report", src), src)
	out := o.fs.String("o", "", "")
	audience := o.fs.String("audience", string(report.Customer), "")
	force := o.fs.Bool("force", false, "")
	all := o.fs.Bool("all", false, "")
	figures := o.fs.String("figures", "", "")
	pos, lang, code, done := o.parse(env, args)
	if done {
		return code
	}
	if o.help {
		return printHelp(env, lang, o.style(env, lang), "report", src)
	}
	aud, err := report.ParseAudience(*audience)
	switch {
	case err != nil:
		return o.usageError(env, lang, "%s", say(lang, "err.audience", "value", *audience))
	case src != nil && len(pos) > 0:
		return o.usageError(env, lang, "%s", say(lang, "err.extra", "arg", pos[0]))
	case len(pos) > 1:
		return o.usageError(env, lang, "%s", say(lang, "err.extra", "arg", pos[1]))
	case *out == "" && *figures == "":
		return o.usageError(env, lang, "%s", say(lang, "err.report_out"))
	}
	if len(pos) == 1 {
		// A case file: nothing is scanned.
		if o.ledger != "" || o.root != "" {
			return o.usageError(env, lang, "%s", say(lang, "err.report_scan"))
		}
		c, err := openCase(ctx, pos[0])
		if err != nil {
			return o.failure(env, err)
		}
		defer c.cleanup()
		src = c
	}
	toStdout := *out == "-"
	if code, ok := o.checkTarget(env, lang, *out, *force); !ok {
		return code
	}
	if *figures != "" {
		if fi, err := os.Stat(*figures); err == nil && !fi.IsDir() {
			return o.usageError(env, lang, "--figures %s: %s", *figures, say(lang, "err.not_dir"))
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
	opts := report.Options{Lang: lang, Audience: aud, Now: clock(env), Tool: toolName(), AllFigures: *all}
	if src != nil && src.manifest != nil {
		m := src.manifest
		info := &report.CaseInfo{ID: m.Ledger.SHA256, Bundle: true, Created: m.CreatedAt, Tool: m.Tool, Anonymized: m.Anonymized}
		if len(info.ID) > 12 {
			info.ID = info.ID[:12]
		}
		if sub := m.Subject; sub != nil {
			// The subject is named by its ledger identity, or by its slot
			// where the identity was replaced with the host name.
			if d := s.h.DIMM(sub.DIMMUID); d != nil && sub.DIMMUID != "" {
				info.SubjectUID = d.UID
			} else if sub.Slot != "" {
				if d, err := s.c.Find(ctx, s.h, sub.Slot); err == nil {
					info.SubjectUID = d.UID
				}
			}
		}
		opts.Case = info
	}
	doc, err := report.Build(ctx, s.c, s.h, opts)
	if err != nil {
		return o.failure(env, err)
	}
	// The figures first: a name that is taken stops the run before
	// anything is written.
	var figs []report.Figure
	if *figures != "" {
		if figs, err = doc.Figures(); err != nil {
			return o.failure(env, err)
		}
		if _, err := os.Stat(*figures); err == nil {
			for _, f := range figs {
				if code, ok := o.checkTarget(env, lang, filepath.Join(*figures, f.Name+".svg"), *force); !ok {
					return code
				}
			}
		}
		if len(figs) > 0 {
			// The directory is made for the figures; a host without any gets none.
			if err := os.MkdirAll(*figures, 0o777); err != nil {
				return o.failure(env, err)
			}
		}
		for _, f := range figs {
			if err := writeWhole(filepath.Join(*figures, f.Name+".svg"), f.SVG, *force); err != nil {
				return o.failure(env, errors.New(targetText(lang, err)))
			}
		}
	}
	var buf bytes.Buffer
	if *out != "" {
		if err := doc.Write(&buf); err != nil {
			return o.failure(env, err)
		}
	}
	if toStdout {
		if _, err := env.Stdout.Write(buf.Bytes()); err != nil {
			return o.failure(env, err)
		}
		return cli.ExitOK
	}
	if *out != "" {
		if err := writeWhole(*out, buf.Bytes(), *force); err != nil {
			return o.failure(env, errors.New(targetText(lang, err)))
		}
	}

	if o.asJSON {
		type dimm struct {
			Slot   string             `json:"slot"`
			Serial string             `json:"serial,omitzero"`
			Grade  model.VerdictGrade `json:"grade"`
		}
		res := struct {
			Path     string             `json:"path"`
			Bytes    int                `json:"bytes"`
			Host     string             `json:"host"`
			Grade    model.VerdictGrade `json:"grade"`
			Audience report.Audience    `json:"audience"`
			Lang     string             `json:"lang"`
			CaseID   string             `json:"case_id"`
			DIMMs    []dimm             `json:"dimms"`
			Records  int                `json:"records"`
			// Figures are the files written with --figures.
			Figures []string `json:"figures,omitzero"`
		}{*out, buf.Len(), s.h.Host, s.h.Grade, aud, string(lang), doc.CaseID, []dimm{}, len(doc.Records), nil}
		for _, d := range doc.DIMMs {
			res.DIMMs = append(res.DIMMs, dimm{d.Card.Slot, d.Card.DIMM.Serial, d.Card.Grade})
		}
		for _, f := range figs {
			res.Figures = append(res.Figures, filepath.Join(*figures, f.Name+".svg"))
		}
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			return o.failure(env, err)
		}
		return cli.ExitOK
	}
	p := func(format string, a ...any) { fmt.Fprintf(env.Stdout, format+"\n", a...) }
	if *out != "" {
		p("%s", say(lang, "report.written", "file", *out, "size", sizeText(int64(buf.Len()))))
	}
	switch {
	case len(figs) > 0:
		p("%s", say(lang, "report.figures", "dir", *figures, "n", len(figs)))
	case *figures != "":
		p("%s", say(lang, "report.no_figures"))
	}
	p("  %s", say(lang, "import.finding", "host", s.h.Host, "grade", findingText(lang, s.h)))
	if *out == "" {
		return cli.ExitOK
	}
	p("  %s", say(lang, "report.holds", "dimms", len(doc.DIMMs), "records", len(doc.Records), "audience", report.Words.Text(lang, "audience."+string(aud), nil)))
	if note := s.storedNote(); src != nil && note != "" {
		for _, line := range s.st.Wrap(note, 2) {
			p("%s", line)
		}
	}
	p("%s", say(lang, "report.open"))
	return cli.ExitOK
}

// writeWhole writes a file of the report: beside its target and moved into
// place, so that a file that exists is whole.
func writeWhole(path string, data []byte, force bool) error {
	_, err := outfile.Write(path, force, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
	return err
}
