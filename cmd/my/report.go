package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/report"
)

// runReport is "my report [FILE] -o OUT": the findings of a case as one
// HTML file. FILE is a case bundle or a ledger; without it the host is
// scanned, as the other commands do.
func runReport(ctx context.Context, env *cli.Env, args []string, src *source) int {
	o := newOptions(env, cmdName("report", src), src)
	out := o.fs.String("o", "", "")
	audience := o.fs.String("audience", string(report.Customer), "")
	force := o.fs.Bool("force", false, "")
	all := o.fs.Bool("all", false, "")
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
	case *out == "":
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
	if !toStdout && !*force {
		if _, err := os.Stat(*out); err == nil {
			return o.usageError(env, lang, "%s", say(lang, "err.exists", "file", *out))
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
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		return o.failure(env, err)
	}
	if toStdout {
		if _, err := env.Stdout.Write(buf.Bytes()); err != nil {
			return o.failure(env, err)
		}
		return cli.ExitOK
	}
	if err := writeWhole(*out, buf.Bytes()); err != nil {
		return o.failure(env, err)
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
		}{*out, buf.Len(), s.h.Host, s.h.Grade, aud, string(lang), doc.CaseID, []dimm{}, len(doc.Records)}
		for _, d := range doc.DIMMs {
			res.DIMMs = append(res.DIMMs, dimm{d.Card.Slot, d.Card.DIMM.Serial, d.Card.Grade})
		}
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			return o.failure(env, err)
		}
		return cli.ExitOK
	}
	p := func(format string, a ...any) { fmt.Fprintf(env.Stdout, format+"\n", a...) }
	p("%s", say(lang, "report.written", "file", *out, "size", sizeText(int64(buf.Len()))))
	p("  %s", say(lang, "import.finding", "host", s.h.Host, "grade", engine.GradeText(lang, s.h.Grade)))
	p("  %s", say(lang, "report.holds", "dimms", len(doc.DIMMs), "records", len(doc.Records), "audience", report.Words.Text(lang, "audience."+string(aud), nil)))
	if note := s.storedNote(); src != nil && note != "" {
		for _, line := range s.st.Wrap(note, 2) {
			p("%s", line)
		}
	}
	p("%s", say(lang, "report.open"))
	return cli.ExitOK
}

// writeWhole writes a file beside its target and moves it into place, so
// that a file that exists is whole.
func writeWhole(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".my-report-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(0o644); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
