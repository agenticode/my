package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/agenticode/my/internal/buildinfo"
	"github.com/agenticode/my/internal/bundle"
	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
)

// toolName is how this binary names itself in what it writes.
func toolName() string { return "my " + buildinfo.Version + " (" + buildinfo.Revision() + ")" }

// keyFromFile reads the key that makes anonymizing repeatable.
func keyFromFile(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	key := []byte(strings.TrimSpace(string(b)))
	if len(key) < 8 {
		return nil, errors.New("the key is shorter than 8 characters")
	}
	return key, nil
}

// sizeText writes a file size.
func sizeText(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f kB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// runBundle is "my bundle [slot|serial] -o FILE": the records of this host,
// or of the case that was opened, as one file to send.
func runBundle(ctx context.Context, env *cli.Env, args []string, src *source) int {
	o := newOptions(env, cmdName("bundle", src), src)
	out := o.fs.String("o", "", "")
	anonymize := o.fs.Bool("anonymize", false, "")
	keyFile := o.fs.String("key-file", "", "")
	force := o.fs.Bool("force", false, "")
	pos, lang, code, done := o.parse(env, args)
	if done {
		return code
	}
	if o.help {
		return printHelp(env, lang, o.style(env, lang), "bundle", src)
	}
	switch {
	case len(pos) > 1:
		return o.usageError(env, lang, "%s", say(lang, "err.extra", "arg", pos[1]))
	case *out == "":
		return o.usageError(env, lang, "%s", say(lang, "err.bundle_out"))
	case *keyFile != "" && !*anonymize:
		return o.usageError(env, lang, "%s", say(lang, "err.key_without"))
	}
	var key []byte
	if *keyFile != "" {
		var err error
		if key, err = keyFromFile(*keyFile); err != nil {
			return o.usageError(env, lang, "--key-file %s: %v", *keyFile, err)
		}
	}
	if !*force {
		if _, err := os.Stat(*out); err == nil {
			return o.usageError(env, lang, "%s", say(lang, "err.exists", "file", *out))
		}
	}

	s, code := o.start(ctx, env, lang, src)
	if s == nil {
		return code
	}
	defer s.close()
	opts := bundle.Options{Now: clock(env), Tool: toolName(), Anonymize: *anonymize, Key: key, Force: *force}
	var subject *engine.DIMMResult
	if len(pos) == 1 {
		if code, ok := s.needHost(); !ok {
			return code
		}
		if subject, code = s.find(pos[0]); subject == nil {
			return code
		}
		opts.Subject = &bundle.Subject{Host: s.h.Host, Slot: subject.Slot, DIMMUID: subject.UID}
		if subject.DIMM != nil {
			opts.Subject.Serial = subject.DIMM.Serial
		}
	}
	// The findings of this run go into the bundle with the records, so that
	// the receiver can see what the sender saw.
	if s.scanned {
		if err := s.c.Result.Save(ctx, s.c.Store()); err != nil {
			return o.failure(env, err)
		}
	}
	w, err := bundle.Write(ctx, s.c.Store(), *out, opts)
	if err != nil {
		return o.failure(env, err)
	}

	if o.asJSON {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(struct {
			Path     string            `json:"path"`
			Bytes    int64             `json:"bytes"`
			Manifest bundle.Manifest   `json:"manifest"`
			Names    map[string]string `json:"names,omitzero"`
		}{w.Path, w.Bytes, w.Manifest, w.Names}); err != nil {
			return o.failure(env, err)
		}
		return cli.ExitOK
	}
	p := func(format string, a ...any) { fmt.Fprintf(env.Stdout, format+"\n", a...) }
	p("%s", say(lang, "bundle.written", "file", w.Path, "size", sizeText(w.Bytes)))
	for _, h := range s.c.Result.Hosts {
		name := h.Host
		if n, ok := w.Names[name]; ok {
			name = n
		}
		dimms := 0
		var ce, ue int64
		for _, d := range h.DIMMs {
			if d.Present {
				dimms++
			}
			ce, ue = ce+d.Features.CE, ue+d.Features.UE
		}
		p("  %s", say(lang, "bundle.host", "host", name, "dimms", dimms, "ce", ce, "ue", ue, "grade", engine.GradeText(lang, h.Grade)))
	}
	if subject != nil {
		text := subject.Slot
		if subject.DIMM != nil && subject.DIMM.Serial != "" {
			text += " S/N " + subject.DIMM.Serial
		}
		p("  %s", say(lang, "bundle.subject", "dimm", text, "grade", engine.GradeText(lang, subject.Verdict.Grade)))
	}
	if s.h != nil {
		if ov, err := s.c.Overview(ctx, s.h, false); err == nil {
			for _, m := range ov.Missing {
				for i, line := range s.st.Wrap(say(lang, "bundle.missing", "what", m.Text), 4) {
					if i == 0 {
						line = "  " + strings.TrimLeft(line, " ")
					}
					p("%s", line)
				}
			}
			if ov.Root != nil {
				for _, line := range s.st.Wrap(ov.Root.Text, 2) {
					p("%s", line)
				}
			}
		}
	}
	if w.Manifest.Anonymized {
		if n := w.Manifest.Anonymization.Addresses; n > 0 {
			p("  %s", say(lang, "bundle.anonymized", "n", n))
		} else {
			p("  %s", say(lang, "bundle.anon_hosts"))
		}
		names := make([]string, 0, len(w.Names))
		for n := range w.Names {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			p("    %s -> %s", n, w.Names[n])
		}
		for _, line := range s.st.Wrap(say(lang, "bundle.keep_names"), 2) {
			p("%s", line)
		}
	}
	p("%s", say(lang, "bundle.open", "file", w.Path))
	return cli.ExitOK
}

// caseInfo says what a bundle is, at the top of what "my open" prints.
type caseInfo struct {
	CreatedAt  string          `json:"created_at"`
	Tool       string          `json:"tool"`
	Anonymized bool            `json:"anonymized"`
	Subject    *bundle.Subject `json:"subject,omitzero"`
}

// caseLine describes an opened bundle in one line.
func caseLine(lang i18n.Lang, m *bundle.Manifest) string {
	parts := []string{say(lang, "case.bundle", "when", m.CreatedAt.UTC().Format("2006-01-02 15:04")+" UTC", "tool", m.Tool)}
	if s := m.Subject; s != nil && s.Slot != "" {
		text := s.Slot
		if s.Serial != "" {
			text += " S/N " + s.Serial
		}
		parts = append(parts, say(lang, "case.subject", "dimm", text))
	}
	if m.Anonymized {
		parts = append(parts, say(lang, "case.anonymized"))
	}
	return strings.Join(parts, "; ")
}

// storedNote says how the findings stored in a case relate to the ones
// this run computed: another engine version, or thresholds the sender
// changed.
func (s *session) storedNote() string {
	if s.h == nil {
		return ""
	}
	raw, err := s.c.Store().Meta(s.ctx, engine.MetaKey(s.h.Host))
	if err != nil || raw == "" {
		return ""
	}
	var rec struct {
		EngineVersion string            `json:"engine_version"`
		Thresholds    map[string]string `json:"thresholds"`
		Grade         string            `json:"grade"`
	}
	if json.Unmarshal([]byte(raw), &rec) != nil {
		return ""
	}
	var notes []string
	if rec.EngineVersion != "" && rec.EngineVersion != engine.Version {
		notes = append(notes, say(s.lang, "case.engine", "theirs", rec.EngineVersion, "ours", engine.Version))
	}
	if len(rec.Thresholds) > 0 {
		keys := make([]string, 0, len(rec.Thresholds))
		for k := range rec.Thresholds {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var sets []string
		for _, k := range keys {
			sets = append(sets, "--set "+k+"="+rec.Thresholds[k])
		}
		notes = append(notes, say(s.lang, "case.thresholds", "sets", strings.Join(sets, " ")))
	}
	if g := model.VerdictGrade(rec.Grade); g.Valid() && g != s.h.Grade {
		notes = append(notes, say(s.lang, "case.grade", "theirs", engine.GradeText(s.lang, g), "ours", engine.GradeText(s.lang, s.h.Grade)))
	}
	return strings.Join(notes, " ")
}
