package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/agenticode/my/internal/bundle"
	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/scan"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/term"
	"github.com/agenticode/my/internal/view"
)

// source is where a command takes its ledger from: a case that was opened
// (my open FILE ...), or, when nil, a scan of the host.
type source struct {
	// path is the file the reader named.
	path string
	// ledger is the ledger file that is read: the file itself, or the
	// ledger taken out of a bundle.
	ledger string
	// manifest is set when the case is a bundle.
	manifest *bundle.Manifest
	// cleanup removes what opening the case put on disk.
	cleanup func()
}

// options are the flags every command that shows findings takes.
type options struct {
	name string // "my dimm", for messages
	fs   *flag.FlagSet

	lang   string
	asJSON bool
	ascii  bool
	color  string
	width  int
	host   string
	ledger string
	root   string
	cf     engine.ConfigFlags
	help   bool
}

// newOptions registers the common flags of the command named name.
func newOptions(env *cli.Env, name string, src *source) *options {
	o := &options{name: name, fs: flag.NewFlagSet(name, flag.ContinueOnError)}
	o.fs.SetOutput(io.Discard) // errors and help are written here, in the reader's language
	o.fs.StringVar(&o.lang, "lang", "", "")
	o.fs.BoolVar(&o.asJSON, "json", false, "")
	o.fs.BoolVar(&o.ascii, "ascii", false, "")
	o.fs.StringVar(&o.color, "color", term.ColorAuto, "")
	o.fs.IntVar(&o.width, "width", 0, "")
	o.fs.StringVar(&o.host, "host", "", "")
	if src == nil {
		// A scan can keep its ledger, and can read a copy of a host's files.
		o.fs.StringVar(&o.ledger, "ledger", "", "")
		o.fs.StringVar(&o.root, "root", "", "")
	}
	o.cf.Register(o.fs)
	o.fs.BoolVar(&o.help, "help", false, "")
	o.fs.BoolVar(&o.help, "h", false, "")
	return o
}

// session is one run of a command that shows findings.
type session struct {
	ctx  context.Context
	env  *cli.Env
	o    *options
	lang i18n.Lang
	st   term.Style
	c    *view.Case
	h    *engine.HostResult // nil when the ledger holds several hosts and none was named
	// prefix is how the reader runs a further command on the same data:
	// "my", or "my open FILE".
	prefix string
	// scanned says that the data comes from a scan of this run.
	scanned bool
	// src is the case that was opened, or nil.
	src     *source
	closers []func()
}

func (s *session) close() {
	for i := len(s.closers) - 1; i >= 0; i-- {
		s.closers[i]()
	}
}

// say renders a message of the command catalog.
func say(lang i18n.Lang, code string, kv ...any) string {
	var args map[string]any
	if len(kv) > 0 {
		args = make(map[string]any, len(kv)/2)
		for i := 0; i+1 < len(kv); i += 2 {
			args[kv[i].(string)] = kv[i+1]
		}
	}
	return words.Text(lang, code, args)
}

// usageError reports a mistake in the command line.
func (o *options) usageError(env *cli.Env, lang i18n.Lang, format string, a ...any) int {
	// "my open dimm" and "my dimm" share one help text: "my help dimm".
	help := "my help"
	if f := strings.Fields(o.name); len(f) > 1 {
		help += " " + f[len(f)-1]
	}
	fmt.Fprintf(env.Stderr, "%s: %s\n%s\n", o.name, fmt.Sprintf(format, a...), say(lang, "err.see_help", "cmd", help))
	return cli.ExitUsage
}

// failure reports that the command could not do its work.
func (o *options) failure(env *cli.Env, err error) int {
	fmt.Fprintf(env.Stderr, "%s: %v\n", o.name, err)
	return cli.ExitFailure
}

// parse reads the command line. done is set when the command has nothing
// left to do: help was printed, or the command line was wrong.
func (o *options) parse(env *cli.Env, args []string) (pos []string, lang i18n.Lang, code int, done bool) {
	// "?" asks for the reading guide wherever it stands.
	for i, a := range args {
		if a == "?" || a == "-?" {
			args = append(append([]string{}, args[:i]...), args[i+1:]...)
			o.help = true
			break
		}
	}
	pos, err := cli.Parse(o.fs, args)
	lang = cli.Lang(env)
	if o.lang != "" {
		l, lerr := i18n.ParseLang(o.lang)
		if lerr != nil && err == nil {
			return nil, lang, o.usageError(env, lang, "%v", lerr), true
		}
		if lerr == nil {
			lang = l
		}
	}
	if errors.Is(err, flag.ErrHelp) || o.help {
		return nil, lang, cli.ExitOK, false
	}
	if err != nil {
		return nil, lang, o.usageError(env, lang, "%v", err), true
	}
	switch o.color {
	case term.ColorAuto, term.ColorAlways, term.ColorNever:
	default:
		return nil, lang, o.usageError(env, lang, "%s", say(lang, "err.color", "value", o.color)), true
	}
	if o.width < 0 {
		return nil, lang, o.usageError(env, lang, "%s", say(lang, "err.width", "value", o.width)), true
	}
	return pos, lang, 0, false
}

// style works out how to draw for this run.
func (o *options) style(env *cli.Env, lang i18n.Lang) term.Style {
	st := term.Style{Lang: lang, ASCII: o.ascii, Width: o.width}
	columns, terminal := 0, false
	if f, ok := env.Stdout.(*os.File); ok {
		columns, terminal = term.Size(f)
	}
	return term.Detect(st, o.color, terminal, columns, env.Getenv)
}

// clock returns the time of this run. MY_NOW sets it, so that tests and
// examples give the same output on every day.
func clock(env *cli.Env) time.Time {
	if v := env.Getenv("MY_NOW"); v != "" {
		if t, err := parseTime(v); err == nil {
			return t
		}
	}
	return time.Time{}
}

// start opens the data of a command: the case the reader opened, or a scan
// of the host. It runs the engine and picks the host.
func (o *options) start(ctx context.Context, env *cli.Env, lang i18n.Lang, src *source) (*session, int) {
	s := &session{ctx: ctx, env: env, o: o, lang: lang, st: o.style(env, lang), prefix: "my", src: src}
	cfg, err := o.cf.Config()
	if err != nil {
		return nil, o.usageError(env, lang, "%v", err)
	}
	now := clock(env)
	host := o.host
	var ledger *store.Store
	if src != nil {
		s.prefix = "my open " + src.path
		ledger, err = store.Open(ctx, src.ledger, store.ReadOnly)
		if err != nil {
			return nil, o.failure(env, err)
		}
	} else {
		var scanned string
		ledger, scanned, err = o.scan(ctx, env, s, now)
		if err != nil {
			s.close()
			return nil, o.failure(env, err)
		}
		if host == "" {
			host = scanned
		}
		s.scanned = true
		if o.ledger != "" {
			s.prefix = "my --ledger " + o.ledger
		}
		if o.root != "" {
			s.prefix = "my --root " + o.root
		}
	}
	s.closers = append(s.closers, func() { ledger.Close() })

	s.c, err = view.Open(ctx, ledger, lang, engine.Options{Config: &cfg, Now: now})
	if err != nil {
		s.close()
		return nil, o.failure(env, err)
	}
	s.h, err = s.c.Host(host)
	switch {
	case err == nil:
	case errors.Is(err, view.ErrSeveralHosts):
		// The command decides: the overview lists the hosts.
	case errors.Is(err, view.ErrNoSuchHost):
		s.close()
		return nil, o.usageError(env, lang, "%s", say(lang, "err.no_host", "host", host, "hosts", strings.Join(s.c.HostNames(), ", ")))
	default:
		s.close()
		return nil, o.failure(env, errors.New(say(lang, "err.empty")))
	}
	return s, 0
}

// scan reads the host into a ledger: a temporary one that is removed when
// the command ends, or the file given with --ledger, which is kept and
// added to on every run.
func (o *options) scan(ctx context.Context, env *cli.Env, s *session, now time.Time) (*store.Store, string, error) {
	tmp, err := os.MkdirTemp("", "my-scan-")
	if err != nil {
		return nil, "", err
	}
	s.closers = append(s.closers, func() { os.RemoveAll(tmp) })
	path := filepath.Join(tmp, "ledger.db")
	if o.ledger != "" {
		path = o.ledger
	}
	ledger, err := store.Open(ctx, path, store.Create)
	if err != nil {
		return nil, "", err
	}
	opts := scan.Options{Root: "/", Live: true, Privileged: os.Geteuid() == 0, Now: now, TempDir: tmp}
	if o.root != "" && filepath.Clean(o.root) != "/" {
		// A copy of a host's files: only files are read.
		if fi, err := os.Stat(o.root); err != nil || !fi.IsDir() {
			ledger.Close()
			return nil, "", fmt.Errorf("--root %s: %s", o.root, say(s.lang, "err.not_dir"))
		}
		// The copy says when it was made; the clock of this run does not.
		opts.Root, opts.Live, opts.Now = o.root, false, time.Time{}
	}
	res, err := scan.Run(ctx, ledger, opts)
	if err != nil {
		ledger.Close()
		return nil, "", err
	}
	return ledger, res.Host, nil
}

// needHost ends a command that draws one host when the ledger holds
// several and none was named.
func (s *session) needHost() (int, bool) {
	if s.h != nil {
		return 0, true
	}
	return s.o.usageError(s.env, s.lang, "%s", say(s.lang, "err.several_hosts", "hosts", strings.Join(s.c.HostNames(), ", "))), false
}

// find resolves the slot or serial number the reader named.
func (s *session) find(ref string) (*engine.DIMMResult, int) {
	d, err := s.c.Find(s.ctx, s.h, ref)
	switch {
	case err == nil:
		return d, 0
	case errors.Is(err, view.ErrEmptySlot):
		return nil, s.o.usageError(s.env, s.lang, "%s", say(s.lang, "err.empty_slot", "slot", ref))
	case errors.Is(err, view.ErrAmbiguousDIMM):
		return nil, s.o.usageError(s.env, s.lang, "%v", err)
	case errors.Is(err, view.ErrNoSuchDIMM):
		var known []string
		for _, x := range s.h.DIMMs {
			if x.Present && view.Listed(x) {
				known = append(known, x.Slot)
			}
		}
		msg := say(s.lang, "err.no_dimm", "ref", ref, "host", s.h.Host)
		if len(known) > 0 {
			msg += " " + say(s.lang, "err.with_findings", "slots", strings.Join(known, ", "))
		}
		return nil, s.o.usageError(s.env, s.lang, "%s", msg)
	}
	return nil, s.o.failure(s.env, err)
}

// envelope is the JSON form of a command's output.
type envelope struct {
	// View names what Data holds: "overview", "hosts", "dimm", "why",
	// "map" or "timeline".
	View string `json:"view"`
	// Version is the version of this JSON form. It changes when a field
	// is renamed or removed; new fields do not change it.
	Version int       `json:"version"`
	Lang    i18n.Lang `json:"lang"`
	Host    string    `json:"host,omitzero"`
	// Grade is the finding the exit code of the command stands for.
	Grade    model.VerdictGrade `json:"grade"`
	ExitCode int                `json:"exit_code"`
	// Case describes the bundle the data comes from, when it comes from one.
	Case *caseInfo `json:"case,omitzero"`
	Data any       `json:"data"`
}

// jsonVersion is the version of the JSON output.
const jsonVersion = 1

// emit writes the output of a command: JSON, or the text draw writes.
func (s *session) emit(name string, grade model.VerdictGrade, data any, draw func(w io.Writer) error) int {
	var err error
	if s.o.asJSON {
		e := envelope{View: name, Version: jsonVersion, Lang: s.lang, Grade: grade, ExitCode: grade.ExitCode(), Data: data}
		if s.h != nil {
			e.Host = s.h.Host
		}
		if s.src != nil && s.src.manifest != nil {
			m := s.src.manifest
			e.Case = &caseInfo{CreatedAt: m.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"), Tool: m.Tool, Anonymized: m.Anonymized, Subject: m.Subject}
		}
		enc := json.NewEncoder(s.env.Stdout)
		enc.SetIndent("", "  ")
		err = enc.Encode(e)
	} else {
		// A case says what it is before anything is drawn from it.
		if s.src != nil && s.src.manifest != nil {
			for _, line := range s.st.Wrap(caseLine(s.lang, s.src.manifest), 0) {
				fmt.Fprintln(s.env.Stdout, line)
			}
		}
		if s.src != nil {
			if note := s.storedNote(); note != "" {
				for _, line := range s.st.Wrap(note, 0) {
					fmt.Fprintln(s.env.Stdout, line)
				}
			}
		}
		err = draw(s.env.Stdout)
	}
	if err != nil {
		return s.o.failure(s.env, err)
	}
	return grade.ExitCode()
}
