package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/exporter"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/serve"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/term"
	"github.com/agenticode/my/internal/view"
)

// serveOptions are the flags of "my serve".
type serveOptions struct {
	*options
	metrics  bool
	listen   string
	interval string
}

func newServeOptions() *serveOptions {
	// The flags of the figures (--json, --ascii, --color, --width) have no
	// meaning for a server and are not registered.
	o := &serveOptions{options: &options{name: "my serve", fs: flag.NewFlagSet("my serve", flag.ContinueOnError), color: term.ColorAuto}}
	o.fs.SetOutput(io.Discard)
	o.fs.BoolVar(&o.metrics, "metrics", false, "")
	o.fs.StringVar(&o.listen, "listen", serve.DefaultListen, "")
	o.fs.StringVar(&o.interval, "interval", serve.DefaultInterval.String(), "")
	o.fs.StringVar(&o.root, "root", "", "")
	o.sf.register(o.fs)
	o.fs.StringVar(&o.host, "host", "", "")
	o.fs.StringVar(&o.lang, "lang", "", "")
	o.cf.Register(o.fs)
	o.fs.BoolVar(&o.help, "help", false, "")
	o.fs.BoolVar(&o.help, "h", false, "")
	return o
}

// minInterval is the shortest pause between two scans the command takes.
// A scan reads every source of the host again.
const minInterval = time.Second

// listenAddress checks the value of --listen: a host and a port, a colon
// and a port, or a port alone.
func listenAddress(v string) (string, error) {
	if _, err := strconv.ParseUint(v, 10, 16); err == nil {
		v = ":" + v
	}
	_, port, err := net.SplitHostPort(v)
	if err != nil {
		return "", err
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return "", fmt.Errorf("port %q", port)
	}
	return v, nil
}

// runServe is "my serve": it scans this host again and again and answers
// HTTP requests from the latest scan, until it is told to stop.
func runServe(ctx context.Context, env *cli.Env, args []string) int {
	o := newServeOptions()
	pos, lang, code, done := o.parse(env, args)
	if done {
		return code
	}
	if o.help {
		return printHelp(env, lang, o.style(env, lang), "serve", nil)
	}
	if len(pos) > 0 {
		return o.usageError(env, lang, "%s", say(lang, "err.extra", "arg", pos[0]))
	}
	listen, err := listenAddress(o.listen)
	if err != nil {
		return o.usageError(env, lang, "%s", say(lang, "err.listen", "value", o.listen))
	}
	interval, err := time.ParseDuration(o.interval)
	if err != nil || interval < minInterval {
		return o.usageError(env, lang, "%s", say(lang, "err.interval", "value", o.interval))
	}
	cfg, err := o.cf.Config()
	if err != nil {
		return o.usageError(env, lang, "%s", errText(lang, err))
	}
	if o.root != "" && filepath.Clean(o.root) != "/" {
		if fi, err := os.Stat(o.root); err != nil || !fi.IsDir() {
			return o.usageError(env, lang, "--root %s: %s", o.root, say(lang, "err.not_dir"))
		}
	}

	log := &serveLog{w: env.Stderr}
	var src serve.Source
	switch {
	case o.sf.set() && o.root != "":
		return o.usageError(env, lang, "%s", say(lang, "err.fleet_root"))
	case o.sf.set():
		// One server for the fleet: it reads the store the rasdaemons of
		// the hosts write to.
		if _, err := parseSince(o.sf.since, time.Now()); err != nil {
			return o.usageError(env, lang, "%s", say(lang, "err.since", "value", o.sf.since))
		}
		fleet := newFleetSource(env, lang, cfg, o.sf, o.host, interval)
		defer fleet.close()
		src = fleet
	case o.sf.since != "":
		return o.usageError(env, lang, "%s", say(lang, "err.since_alone"))
	case o.host != "":
		return o.usageError(env, lang, "%s", say(lang, "err.host_alone"))
	default:
		src = &hostSource{lang: lang, cfg: cfg, root: o.root, now: clock(env)}
	}
	// A scan is logged when it says something the log does not say yet:
	// the first one, one that found something else than the one before,
	// one that failed, and the first that works again.
	said, unnamed := "", int64(0)
	srv := serve.New(src, serve.Options{
		Listen: listen, Metrics: o.metrics, Interval: interval,
		OnListen: func(addr net.Addr) {
			log.print(say(lang, "serve.listening", "url", "http://"+addr.String()))
			if !o.metrics {
				log.print(say(lang, "serve.no_metrics"))
			}
		},
		OnScan: func(st serve.State, took time.Duration, err error) {
			if err != nil {
				log.print(say(lang, "serve.scan_failed", "err", err.Error()))
				said = ""
				return
			}
			if found := st.(interface{ summary(i18n.Lang) string }).summary(lang); found != said {
				log.print(say(lang, "serve.scan", "found", found, "took", took.Round(time.Millisecond).String()))
				said = found
			}
			// Rows of a store that name no host are in no finding: say so
			// when their number changes.
			if fleet, ok := st.(*fleetState); ok && fleet.unnamed != unnamed {
				if unnamed = fleet.unnamed; unnamed > 0 {
					log.print(say(lang, "fleet.unnamed", "n", unnamed))
				}
			}
		},
	})
	if err := srv.Run(ctx); err != nil {
		return o.failure(env, err)
	}
	log.print(say(lang, "serve.stopped"))
	return cli.ExitOK
}

// serveLog writes the log of the server: one line per event, with the
// time. Scans and requests run on different goroutines.
type serveLog struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *serveLog) print(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.w, "%s %s\n", time.Now().UTC().Format("2006-01-02T15:04:05Z"), line)
}

// hostSource scans this host, or a copy of a host's files, for the server.
// It keeps nothing between scans: every scan reads the sources into a new
// ledger in a temporary directory, and the ledger of the scan before is
// removed when its state is closed. A ledger that is added to on every
// scan would grow by a scan entry, the source inventory and a reading of
// every counter each time.
type hostSource struct {
	lang i18n.Lang
	cfg  engine.Config
	root string
	now  time.Time
}

func (h *hostSource) Scan(ctx context.Context) (serve.State, error) {
	tmp, err := os.MkdirTemp("", "my-serve-")
	if err != nil {
		return nil, err
	}
	st := &hostState{tmp: tmp, lang: h.lang}
	ok := false
	defer func() {
		if !ok {
			st.Close()
		}
	}()
	var host string
	if st.ledger, host, err = scanInto(ctx, h.lang, filepath.Join(tmp, "ledger.db"), h.root, tmp, h.now, nil); err != nil {
		return nil, err
	}
	if st.c, err = view.Open(ctx, st.ledger, h.lang, engine.Options{Config: &h.cfg, Now: h.now}); err != nil {
		return nil, err
	}
	if st.h, err = st.c.Host(host); err != nil {
		return nil, errors.New(say(h.lang, "err.empty"))
	}
	if st.set, err = exporter.Host(ctx, st.ledger, st.h); err != nil {
		return nil, err
	}
	ok = true
	return st, nil
}

// hostState is one scan of the host: its ledger and what the engine found.
type hostState struct {
	tmp    string
	lang   i18n.Lang
	ledger *store.Store
	c      *view.Case
	h      *engine.HostResult
	set    *exporter.Set
}

func (s *hostState) Metrics() *exporter.Set { return s.set }

// DIMM answers /api/dimm with what "my why --json" prints for the same
// DIMM: the same data in the same envelope.
func (s *hostState) DIMM(ctx context.Context, q serve.Query) (any, error) {
	lang := s.lang
	if q.Lang != "" {
		l, err := i18n.ParseLang(q.Lang)
		if err != nil {
			return nil, &serve.Error{Status: http.StatusBadRequest, Message: err.Error()}
		}
		lang = l
	}
	return whyDocument(ctx, s.c.In(lang), s.h, lang, q)
}

// whyDocument builds the answer to /api/dimm for one host.
func whyDocument(ctx context.Context, c *view.Case, h *engine.HostResult, lang i18n.Lang, q serve.Query) (any, error) {
	if q.Host != "" && !strings.EqualFold(q.Host, h.Host) {
		return nil, &serve.Error{Status: http.StatusNotFound, Message: say(lang, "err.no_host", "host", q.Host, "hosts", h.Host)}
	}
	grade := h.Grade
	var d *engine.DIMMResult
	if q.Ref != "" {
		var msg string
		var err error
		d, msg, err = findDIMM(ctx, c, h, lang, q.Ref)
		switch {
		case d != nil:
		case msg == "":
			return nil, err
		case errors.Is(err, view.ErrAmbiguousDIMM):
			// The name fits several DIMMs: the request has to say more.
			return nil, &serve.Error{Status: http.StatusBadRequest, Message: msg}
		default:
			return nil, &serve.Error{Status: http.StatusNotFound, Message: msg}
		}
		grade = d.Verdict.Grade
	}
	cards, err := whyCards(ctx, c, h, d, q.All)
	if err != nil {
		return nil, err
	}
	return newEnvelope("why", lang, h.Host, grade, cards), nil
}

func (s *hostState) Close() {
	if s.ledger != nil {
		s.ledger.Close()
	}
	os.RemoveAll(s.tmp)
}

// summary says in one line what the scan found, for the log.
func (s *hostState) summary(lang i18n.Lang) string {
	dimms, listed := 0, 0
	for _, d := range s.h.DIMMs {
		if !d.Present {
			continue
		}
		dimms++
		if view.Listed(d) {
			listed++
		}
	}
	return say(lang, "serve.found", "host", s.h.Host, "grade", engine.GradeText(lang, s.h.Grade), "dimms", dimms, "listed", listed)
}
