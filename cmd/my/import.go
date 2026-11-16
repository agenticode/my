package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	_ "time/tzdata" // --tz works on a host without zone files

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/bundle"
	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/scan"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/term"
	"github.com/agenticode/my/internal/view"
)

// input is one file or directory given to "my import", with what it was
// taken for and what its sources yielded.
type input struct {
	Path    string         `json:"path"`
	What    string         `json:"what"`
	Sources []model.Source `json:"sources"`
}

// runImport is "my import <files...>": build a case from files someone
// sent, without the host they came from.
func runImport(ctx context.Context, env *cli.Env, args []string) int {
	// An import has no host to scan: it takes the flags of a stored case,
	// and can keep its ledger like a scan.
	o := newOptions(env, "my import", &source{})
	o.fs.StringVar(&o.ledger, "ledger", "", "")
	out := o.fs.String("o", "", "")
	capturedAt := o.fs.String("captured-at", "", "")
	mtime := o.fs.Bool("mtime", false, "")
	nvidiaQuery := o.fs.String("nvidia-query", "", "")
	tz := o.fs.String("tz", "", "")
	anonymize := o.fs.Bool("anonymize", false, "")
	keyFile := o.fs.String("key-file", "", "")
	force := o.fs.Bool("force", false, "")
	all := o.fs.Bool("all", false, "")
	pos, lang, code, done := o.parse(env, args)
	if done {
		return code
	}
	if o.help {
		return printHelp(env, lang, o.style(env, lang), "import", nil)
	}
	switch {
	case len(pos) == 0:
		return o.usageError(env, lang, "%s", say(lang, "err.import_files"))
	case *keyFile != "" && !*anonymize:
		return o.usageError(env, lang, "%s", say(lang, "err.key_without"))
	case *anonymize && *out == "":
		return o.usageError(env, lang, "%s", say(lang, "err.anonymize_out"))
	}
	now := clock(env)
	// A log that holds the records of several hosts is read for the host
	// the case is about.
	dopts := scan.DetectOptions{Now: now, ModTime: *mtime, Host: o.host, NvidiaQuery: *nvidiaQuery}
	if *capturedAt != "" {
		t, err := parseTime(*capturedAt)
		if err != nil {
			return o.usageError(env, lang, "%s", say(lang, "err.time", "flag", "--captured-at", "value", *capturedAt))
		}
		dopts.CapturedAt = t
	}
	if *tz != "" {
		loc, err := time.LoadLocation(*tz)
		if err != nil {
			return o.usageError(env, lang, "%s", say(lang, "err.tz", "value", *tz))
		}
		dopts.Location = loc
	}
	var key []byte
	if *keyFile != "" {
		var err error
		if key, err = keyFromFile(*keyFile); err != nil {
			return o.usageError(env, lang, "--key-file %s: %v", *keyFile, err)
		}
	}
	toStdout := *out == "-"
	if toStdout {
		if code, is := o.binaryToTerminal(env, lang); is {
			return code
		}
	}
	if code, ok := o.checkTarget(env, lang, *out, *force); !ok {
		return code
	}

	tmp, err := os.MkdirTemp("", "my-import-")
	if err != nil {
		return o.failure(env, err)
	}
	defer os.RemoveAll(tmp)
	dopts.TempDir = tmp

	// Every input is recognised before anything is read: a file that is
	// not understood stops the import, it is never passed over.
	inputs := make([]input, len(pos))
	counts := make([]int, len(pos))
	var adapters []adapter.Adapter
	var unknown []string
	for i, p := range pos {
		as, what, err := scan.Detect(ctx, p, dopts)
		if err != nil {
			unknown = append(unknown, err.Error())
			continue
		}
		inputs[i] = input{Path: p, What: what, Sources: []model.Source{}}
		counts[i] = len(as)
		adapters = append(adapters, as...)
	}
	if len(unknown) > 0 {
		for _, u := range unknown {
			fmt.Fprintf(env.Stderr, "%s: %s\n", o.name, u)
		}
		return cli.ExitFailure
	}

	path := filepath.Join(tmp, "ledger.db")
	if o.ledger != "" {
		path = o.ledger
	}
	ledger, err := store.Open(ctx, path, store.Create)
	if err != nil {
		return o.failure(env, err)
	}
	defer ledger.Close()
	res, err := ingest.Run(ctx, ledger, adapters, ingest.Options{Host: o.host, Kind: model.ScanImport, Now: now})
	if err != nil {
		return o.failure(env, err)
	}
	at := 0
	for i := range inputs {
		inputs[i].Sources = append(inputs[i].Sources, res.Sources[at:at+counts[i]]...)
		at += counts[i]
	}
	// What ingest adds by itself (the slot map) follows the inputs.
	extra := res.Sources[at:]

	cfg, err := o.cf.Config()
	if err != nil {
		return o.usageError(env, lang, "%s", errText(lang, err))
	}
	eopts := engine.Options{Config: &cfg, Now: now}
	analyzer, code, ok := o.analyze(ctx, env, lang, ledger, &eopts, now)
	if !ok {
		return code
	}
	c, err := view.Open(ctx, ledger, lang, eopts)
	if err != nil {
		return o.failure(env, err)
	}
	h, err := c.Host(res.Host)
	if err != nil {
		return o.failure(env, err)
	}
	st := o.style(env, lang)

	summary := func(w io.Writer) {
		for _, in := range inputs {
			fmt.Fprintf(w, "%s: %s\n", in.Path, in.What)
			for _, src := range in.Sources {
				importLine(w, lang, st, src)
			}
		}
		for _, src := range extra {
			importLine(w, lang, st, src)
		}
		fmt.Fprintln(w, say(lang, "import.total", "host", res.Host, "events", res.Events, "actions", res.Actions, "dup", res.Duplicates))
		if analyzer != nil {
			fmt.Fprintln(w, analyzerLine(lang, analyzer))
		}
	}

	if *out != "" {
		if err := c.Result.Save(ctx, ledger); err != nil {
			return o.failure(env, err)
		}
		bopts := bundle.Options{Now: now, Tool: toolName(), Anonymize: *anonymize, Key: key, Force: *force}
		var w *bundle.Written
		// With "-o -" the bundle is the standard output, and what is said
		// about it goes to the standard error.
		says := env.Stdout
		if toStdout {
			says = env.Stderr
			if w, err = bundle.WriteTo(ctx, ledger, env.Stdout, bopts); err == nil {
				w.Path = "-"
			}
		} else {
			w, err = bundle.Write(ctx, ledger, *out, bopts)
		}
		if err != nil {
			return o.failure(env, errors.New(targetText(lang, err)))
		}
		if o.asJSON {
			enc := json.NewEncoder(says)
			enc.SetIndent("", "  ")
			if err := enc.Encode(struct {
				Path     string            `json:"path"`
				Bytes    int64             `json:"bytes"`
				Inputs   []input           `json:"inputs"`
				Manifest bundle.Manifest   `json:"manifest"`
				Names    map[string]string `json:"names,omitzero"`
			}{w.Path, w.Bytes, inputs, w.Manifest, w.Names}); err != nil {
				return o.failure(env, err)
			}
			return cli.ExitOK
		}
		summary(says)
		if toStdout {
			fmt.Fprintln(says, say(lang, "bundle.sent", "size", sizeText(w.Bytes)))
		} else {
			fmt.Fprintln(says, say(lang, "bundle.written", "file", w.Path, "size", sizeText(w.Bytes)))
		}
		fmt.Fprintf(says, "  %s\n", say(lang, "import.finding", "host", h.Host, "grade", findingText(lang, h)))
		if a := w.Manifest.Anonymization; a != nil && a.Users > 0 {
			fmt.Fprintf(says, "  %s\n", say(lang, "bundle.anon_users", "n", a.Users))
		}
		for name, word := range w.Names {
			fmt.Fprintf(says, "    %s -> %s\n", name, word)
		}
		if regularFile(w.Path) {
			fmt.Fprintln(says, say(lang, "bundle.open", "file", w.Path))
		}
		return cli.ExitOK
	}

	// Without a file to write, the import is looked at right away.
	ov, err := c.Overview(ctx, h, *all)
	if err != nil {
		return o.failure(env, err)
	}
	if o.asJSON {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		e := envelope{View: "overview", Version: jsonVersion, Lang: lang, Host: h.Host, Grade: h.Grade, ExitCode: h.Grade.ExitCode(), Data: struct {
			Inputs   []input        `json:"inputs"`
			Overview *view.Overview `json:"overview"`
		}{inputs, ov}}
		if err := enc.Encode(e); err != nil {
			return o.failure(env, err)
		}
		return h.Grade.ExitCode()
	}
	summary(env.Stdout)
	fmt.Fprintln(env.Stdout)
	hints := []term.Hint{{Command: "my import ... -o case.mycase", Text: say(lang, "hint.import")}, {Command: "my help", Text: say(lang, "hint.help")}}
	if err := term.Overview(env.Stdout, ov, st, hints); err != nil {
		return o.failure(env, err)
	}
	return h.Grade.ExitCode()
}

// importLine writes what one source of an import yielded.
func importLine(w io.Writer, lang i18n.Lang, st term.Style, src model.Source) {
	status := view.Messages.Text(lang, "status.ok", nil)
	if src.Status != model.SourceOK {
		status = engine.Messages.Text(lang, "status."+string(src.Status), nil)
	}
	line := fmt.Sprintf("  %s: %s", src.Kind, status)
	var parts []string
	if src.Records > 0 {
		parts = append(parts, say(lang, "import.records", "n", src.Records))
	}
	if src.Events > 0 {
		grade := ""
		if g := src.BestGrade(); g != "" {
			grade = string(g)
		}
		parts = append(parts, say(lang, "import.events", "n", src.Events, "grade", grade))
	}
	if src.Actions > 0 {
		parts = append(parts, say(lang, "import.actions", "n", src.Actions))
	}
	if src.Skipped > 0 {
		parts = append(parts, say(lang, "import.skipped", "n", src.Skipped))
	}
	if len(parts) > 0 {
		line += ", " + strings.Join(parts, ", ")
	}
	fmt.Fprintln(w, line)
	// What went wrong, or what the reader has to know, in the words of the
	// source.
	notes := src.Notes
	if src.Detail != "" {
		notes = append([]string{src.Detail}, notes...)
	}
	for _, n := range notes {
		if src.Status == model.SourceOK && src.Kind != ingest.TimeAlignKind && !strings.Contains(n, "skipped") &&
			!strings.Contains(n, "seconds since boot") && !strings.Contains(n, "estimated") {
			continue
		}
		for _, l := range st.Wrap(n, 6) {
			fmt.Fprintln(w, l)
		}
	}
}
