package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/redfish"
	"github.com/agenticode/my/internal/bundle"
	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/outfile"
	"github.com/agenticode/my/internal/plugin"
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
	// sf points a scan at the store of a fleet instead of at this host.
	sf fleetFlags
	// bmc names the BMC of the host, to be asked in the same run.
	bmc bmcFlags
	// pf names a vendor analyzer to run before the engine.
	pf   pluginFlags
	cf   engine.ConfigFlags
	help bool
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
		o.sf.register(o.fs)
		o.bmc.registerScan(o.fs)
	}
	o.cf.Register(o.fs)
	o.pf.register(o)
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
	src *source
	// analyzer is the response of the vendor analyzer, when one was asked
	// for with --plugin.
	analyzer *plugin.Result
	closers  []func()
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
	if o.ledger != "" {
		// Before anything is read: a ledger that is kept is a database
		// file, and a device or a missing directory cannot hold one.
		if err := outfile.CheckLedger(o.ledger); err != nil {
			return nil, lang, o.usageError(env, lang, "--ledger %s", targetText(lang, err)), true
		}
	}
	return pos, lang, 0, false
}

// targetText says in the reader's language why a file cannot be written.
func targetText(lang i18n.Lang, err error) string {
	var pe *outfile.PathError
	if !errors.As(err, &pe) {
		return err.Error()
	}
	for _, m := range []struct {
		err  error
		code string
	}{
		{outfile.ErrExists, "err.exists"},
		{outfile.ErrNoDir, "err.no_dir"},
		{outfile.ErrIsDir, "err.is_dir"},
		{outfile.ErrNotFile, "err.not_file"},
		{outfile.ErrDenied, "err.dir_denied"},
		{outfile.ErrDangling, "err.dangling"},
	} {
		if errors.Is(pe.Err, m.err) {
			return say(lang, m.code, "file", pe.Path)
		}
	}
	return err.Error()
}

// checkTarget ends a command whose output file cannot be written, before
// the host is scanned for it: a name that is taken, a directory that is
// not there. "-" is the standard output and needs no check.
func (o *options) checkTarget(env *cli.Env, lang i18n.Lang, path string, force bool) (code int, ok bool) {
	if path == "" || path == "-" {
		return 0, true
	}
	if err := outfile.Check(path, force); err != nil {
		return o.usageError(env, lang, "%s", targetText(lang, err)), false
	}
	return 0, true
}

// binaryToTerminal ends a command that was asked to write a file to the
// standard output while that is a terminal.
func (o *options) binaryToTerminal(env *cli.Env, lang i18n.Lang) (code int, is bool) {
	if f, ok := env.Stdout.(*os.File); ok {
		if _, terminal := term.Size(f); terminal {
			return o.usageError(env, lang, "%s", say(lang, "err.terminal")), true
		}
	}
	return 0, false
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
			var u usage
			if errors.As(err, &u) {
				return nil, o.usageError(env, lang, "%s", u.msg)
			}
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
		if o.sf.set() {
			s.prefix = "my " + o.sf.args(o.host)
		}
	}
	s.closers = append(s.closers, func() { ledger.Close() })

	eopts := engine.Options{Config: &cfg, Now: now}
	var ok bool
	var code int
	if s.analyzer, code, ok = o.analyze(ctx, env, lang, ledger, &eopts, now); !ok {
		s.close()
		return nil, code
	}
	s.c, err = view.Open(ctx, ledger, lang, eopts)
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
	if flag := o.bmc.alone(); flag != "" {
		return nil, "", usage{say(s.lang, "err.bmc_alone", "flag", flag)}
	}
	if o.sf.set() {
		if o.bmc.bmc != "" {
			return nil, "", usage{say(s.lang, "err.bmc_fleet")}
		}
		return o.scanFleet(ctx, env, s.lang, path, now)
	}
	if o.sf.since != "" {
		return nil, "", usage{say(s.lang, "err.since_alone")}
	}
	var bmc func() (adapter.Adapter, error)
	if o.bmc.bmc != "" {
		bmc = func() (adapter.Adapter, error) { return o.readBMC(ctx, env, s.lang, tmp, now) }
	}
	return scanInto(ctx, s.lang, path, o.root, tmp, now, bmc)
}

// scanInto scans the host, or the copy of a host's files below root, into
// the ledger at path, which is created when it is not there. It returns
// the ledger and the name of the host. bmc, when it is not nil, asks the
// BMC of the host; it is called once the host is known to be worth
// scanning, and what it returns is read in the same run.
func scanInto(ctx context.Context, lang i18n.Lang, path, root, tmp string, now time.Time, bmc func() (adapter.Adapter, error)) (*store.Store, string, error) {
	opts := scan.Options{Root: "/", Live: true, Privileged: os.Geteuid() == 0, Now: now, TempDir: tmp}
	if root != "" && filepath.Clean(root) != "/" {
		// A copy of a host's files: only files are read.
		if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
			return nil, "", usage{fmt.Sprintf("--root %s: %s", root, say(lang, "err.not_dir"))}
		}
		// The copy says when it was made; the clock of this run does not.
		opts.Root, opts.Live, opts.Now = root, false, time.Time{}
	}
	if opts.Live && hostOS != "linux" {
		// The sources of a scan are what the Linux kernel and its tools
		// keep. Elsewhere there is nothing to read, and an overview that
		// says "no errors on record" would be about nothing.
		return nil, "", errors.New(say(lang, "err.not_linux", "os", hostOS))
	}
	if !opts.Live && !scan.HoldsErrorSource(&opts) {
		// The directory holds nothing a scan reads errors from: it is no
		// copy of a host. The ledger is left alone; the scan runs into a
		// scratch file, which takes no time, to name what it looked for.
		scratch, err := store.Open(ctx, filepath.Join(tmp, "nothing.db"), store.Create)
		if err != nil {
			return nil, "", err
		}
		res, err := scan.Run(ctx, scratch, opts)
		scratch.Close()
		if err != nil {
			return nil, "", err
		}
		return nil, "", errors.New(blindText(lang, &opts, res.Sources))
	}
	if bmc != nil {
		a, err := bmc()
		if err != nil {
			return nil, "", err
		}
		opts.Extra = append(opts.Extra, a)
	}
	_, statErr := os.Stat(path)
	made := errors.Is(statErr, fs.ErrNotExist)
	ledger, err := store.Open(ctx, path, store.Create)
	if err != nil {
		return nil, "", err
	}
	res, err := scan.Run(ctx, ledger, opts)
	if err != nil {
		ledger.Close()
		return nil, "", err
	}
	for _, src := range res.Sources {
		// The answers of the BMC were refused: they are those of another
		// machine, or they could not be read. The reader asked for them,
		// so the command ends here and says why. What the host itself
		// gave stays in a ledger that is kept.
		if bmc != nil && src.Kind == redfish.Kind && strings.HasPrefix(src.Origin, bmcOrigin) && src.Status == model.SourceError {
			ledger.Close()
			if made {
				for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
					os.Remove(path + suffix)
				}
			}
			return nil, "", errors.New(say(lang, "err.bmc_refused", "bmc", strings.TrimPrefix(src.Origin, bmcOrigin), "why", src.Detail))
		}
	}
	if scan.Blind(res.Sources) {
		// Nothing that could have shown an error was read. The scan ends
		// here: its overview would read "no errors on record". A ledger
		// this run made for it holds nothing worth keeping.
		ledger.Close()
		if made {
			for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
				os.Remove(path + suffix)
			}
		}
		return nil, "", errors.New(blindText(lang, &opts, res.Sources))
	}
	return ledger, res.Host, nil
}

// hostOS is the operating system this binary runs on. A test sets it to
// run a scan of a made-up host on any system.
var hostOS = runtime.GOOS

// blindText says that a scan read no source of error records: where it
// looked, what it found there, and what would let it read.
func blindText(lang i18n.Lang, opts *scan.Options, sources []model.Source) string {
	var b strings.Builder
	switch {
	case !opts.Live:
		b.WriteString(say(lang, "err.blind_root", "dir", opts.Root))
	case opts.Privileged:
		b.WriteString(say(lang, "err.blind_live"))
	default:
		b.WriteString(say(lang, "err.blind_user"))
	}
	for _, src := range sources {
		switch src.Kind {
		case "hostinfo", "smbios", ingest.SlotMapKind, ingest.TimeAlignKind:
			continue // they describe the machine and hold no error
		}
		line := "\n  " + src.Kind + ": " + engine.Messages.Text(lang, "status."+string(src.Status), nil)
		if src.Detail != "" && src.Detail != "not present" {
			line += " (" + src.Detail + ")"
		}
		b.WriteString(line)
	}
	if opts.Live && !opts.Privileged {
		b.WriteString("\n" + say(lang, "err.blind_sudo"))
	}
	return b.String()
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
	d, msg, err := findDIMM(s.ctx, s.c, s.h, s.lang, ref)
	switch {
	case d != nil:
		return d, 0
	case msg != "":
		return nil, s.o.usageError(s.env, s.lang, "%s", msg)
	}
	return nil, s.o.failure(s.env, err)
}

// findDIMM resolves a slot or a serial number in a host. When the name
// fits no DIMM, or more than one, it returns the error of view.Case.Find
// together with a message that says the same in the reader's language. An
// error without a message means that the ledger could not be read.
func findDIMM(ctx context.Context, c *view.Case, h *engine.HostResult, lang i18n.Lang, ref string) (*engine.DIMMResult, string, error) {
	d, err := c.Find(ctx, h, ref)
	switch {
	case err == nil:
		return d, "", nil
	case errors.Is(err, view.ErrEmptySlot):
		return nil, say(lang, "err.empty_slot", "slot", ref), err
	case errors.Is(err, view.ErrAmbiguousDIMM):
		return nil, err.Error(), err
	case errors.Is(err, view.ErrNoSuchDIMM):
		var known []string
		for _, x := range h.DIMMs {
			if x.Present && view.Listed(x) {
				known = append(known, x.Slot)
			}
		}
		msg := say(lang, "err.no_dimm", "ref", ref, "host", h.Host)
		if len(known) > 0 {
			msg += " " + say(lang, "err.with_findings", "slots", strings.Join(known, ", "))
		}
		return nil, msg, err
	}
	return nil, "", err
}

// whyCards builds what "my why" shows: the finding card of one DIMM, or,
// when d is nil, of every DIMM of the host that has errors or a finding
// (with all set, of every DIMM), the worst first, and the card of the host
// finding when the host has one.
func whyCards(ctx context.Context, c *view.Case, h *engine.HostResult, d *engine.DIMMResult, all bool) ([]*view.Card, error) {
	cards := []*view.Card{}
	if d != nil {
		card, err := c.Card(ctx, h, d)
		if err != nil {
			return nil, err
		}
		return append(cards, card), nil
	}
	for _, x := range h.DIMMs {
		if !all && !view.Listed(x) {
			continue
		}
		card, err := c.Card(ctx, h, x)
		if err != nil {
			return nil, err
		}
		cards = append(cards, card)
	}
	// The host finding, about what names no DIMM. It leads where it sets
	// the grade of the host and follows the DIMMs otherwise.
	hc, err := c.HostCard(ctx, h)
	if err != nil {
		return nil, err
	}
	if hc != nil {
		leads := len(cards) == 0 || (hc.Grade.ExitCode() > cards[0].Grade.ExitCode() &&
			!(hc.Grade == model.VerdictHoldReplacement && cards[0].Grade == model.VerdictReplaceNow))
		if leads {
			cards = append([]*view.Card{hc}, cards...)
		} else {
			cards = append(cards, hc)
		}
	}
	return cards, nil
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
	// Grade is the finding the exit code of the command stands for. A
	// command whose exit code is no grade (verify) leaves it out.
	Grade    model.VerdictGrade `json:"grade,omitzero"`
	ExitCode int                `json:"exit_code"`
	// Case describes the bundle the data comes from, when it comes from one.
	Case *caseInfo `json:"case,omitzero"`
	// Analyzer names the vendor analyzer the run used (--plugin).
	Analyzer *analyzerInfo `json:"analyzer,omitzero"`
	Data     any           `json:"data"`
}

// jsonVersion is the version of the JSON output.
const jsonVersion = 1

// newEnvelope wraps the data of a view for output as JSON.
func newEnvelope(name string, lang i18n.Lang, host string, grade model.VerdictGrade, data any) envelope {
	return envelope{View: name, Version: jsonVersion, Lang: lang, Host: host, Grade: grade, ExitCode: grade.ExitCode(), Data: data}
}

// writeJSON writes a document the way --json prints it.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// emit writes the output of a command that shows findings: JSON, or the
// text draw writes. The exit code is the grade.
func (s *session) emit(name string, grade model.VerdictGrade, data any, draw func(w io.Writer) error) int {
	return s.emitCode(name, grade, grade.ExitCode(), data, draw)
}

// emitCode writes the output of a command and returns its exit code. A
// command whose exit code is no grade passes an empty grade.
func (s *session) emitCode(name string, grade model.VerdictGrade, code int, data any, draw func(w io.Writer) error) int {
	var err error
	if s.o.asJSON {
		host := ""
		if s.h != nil {
			host = s.h.Host
		}
		e := newEnvelope(name, s.lang, host, grade, data)
		e.ExitCode = code
		e.Analyzer = s.analyzerInfo()
		if s.src != nil && s.src.manifest != nil {
			m := s.src.manifest
			e.Case = &caseInfo{CreatedAt: m.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"), Tool: m.Tool, Anonymized: m.Anonymized, Subject: m.Subject}
		}
		err = writeJSON(s.env.Stdout, e)
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
		s.analyzerLines(s.env.Stdout)
		err = draw(s.env.Stdout)
	}
	if err != nil {
		return s.o.failure(s.env, err)
	}
	return code
}
