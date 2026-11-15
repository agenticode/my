package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/agenticode/my/internal/adapter/rasdaemon"
	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/exporter"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/serve"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/view"
)

// How a server that reads a store keeps up with it.
const (
	// fleetFullEvery is how often the hosts of the store are listed in
	// full. The scans in between ask only for rows above the row numbers
	// seen so far. The full listing finds what that misses: rows that were
	// removed, and a row that became visible below a number already seen.
	fleetFullEvery = 12
	// fleetMaxAge is how old the findings for a host get before the host
	// is read again although it has no new row. Findings are as of the
	// time they are made: a fault that was rising is quiet a week later.
	fleetMaxAge = 6 * time.Hour
)

// fleetSource reads the store of a fleet for the server. A scan lists the
// hosts that have new rows and reads those; what it found for the other
// hosts stays. Reading every host on every scan would send the whole
// store over the network each time.
//
// A host is read into a ledger in a temporary directory that is removed
// when the findings are made. Between scans the source keeps the metrics
// of every host and nothing else.
type fleetSource struct {
	env      *cli.Env
	lang     i18n.Lang
	cfg      engine.Config
	flags    fleetFlags
	only     string // the host named with --host, or ""
	interval time.Duration
	// clock gives the time of a scan.
	clock func() time.Time

	st    *rasdaemon.Store
	scans int
	// seen is the highest row number per table that a listing has shown.
	seen map[string]int64
	// known is what the listings say about each host: its rows and its
	// highest row numbers. hosts is what the last read of each host found,
	// and pending the hosts that have rows which were not read yet.
	known   map[string]*rasdaemon.StoreHost
	hosts   map[string]*fleetHost
	pending map[string]bool
	unnamed int64
}

// fleetHost is what the last read of a host found.
type fleetHost struct {
	set    *exporter.Set
	grade  model.VerdictGrade
	readAt time.Time
}

func newFleetSource(env *cli.Env, lang i18n.Lang, cfg engine.Config, flags fleetFlags, only string, interval time.Duration) *fleetSource {
	fixed := clock(env)
	return &fleetSource{env: env, lang: lang, cfg: cfg, flags: flags, only: only, interval: interval,
		clock: func() time.Time {
			if !fixed.IsZero() {
				return fixed
			}
			return time.Now()
		},
		known: map[string]*rasdaemon.StoreHost{}, hosts: map[string]*fleetHost{}, pending: map[string]bool{}}
}

// close closes the connection to the store.
func (f *fleetSource) close() {
	if f.st != nil {
		f.st.Close()
	}
}

// wanted reports whether the server shows a host.
func (f *fleetSource) wanted(name string) bool {
	return name != "" && (f.only == "" || strings.EqualFold(name, f.only))
}

// Scan brings the findings up to date with the store.
func (f *fleetSource) Scan(ctx context.Context) (serve.State, error) {
	now := f.clock()
	if f.st == nil {
		st, _, err := f.flags.open(ctx, f.env, f.lang, now)
		if err != nil {
			return nil, err
		}
		f.st = st
	}
	// "30d" counts back from this scan, whenever the server was started.
	since, err := parseSince(f.flags.since, now)
	if err != nil {
		return nil, err
	}
	opts := rasdaemon.ReadOptions{Since: since}

	f.scans++
	full := f.seen == nil || f.scans%fleetFullEvery == 0
	var after map[string]int64
	if !full {
		after = f.seen
	}
	listed, err := f.st.Hosts(ctx, after)
	if err != nil {
		return nil, err
	}
	if f.seen == nil {
		f.seen = map[string]int64{}
	}
	if full {
		f.unnamed = 0
	}
	present := map[string]bool{}
	for i := range listed {
		h := &listed[i]
		for table, last := range h.Last {
			f.seen[table] = max(f.seen[table], last)
		}
		if h.Name == "" {
			f.unnamed += h.Rows
			continue
		}
		if !f.wanted(h.Name) {
			continue
		}
		present[h.Name] = true
		old := f.known[h.Name]
		switch {
		case full:
			// The whole of the host: read it when it is not what it was.
			if old == nil || old.Rows != h.Rows || !reflect.DeepEqual(old.Last, h.Last) {
				f.pending[h.Name] = true
			}
			f.known[h.Name] = h
		case old == nil:
			f.pending[h.Name] = true
			f.known[h.Name] = h
		default:
			// Rows above the marks: the host has something new.
			f.pending[h.Name] = true
			old.Rows += h.Rows
			for table, last := range h.Last {
				old.Last[table] = max(old.Last[table], last)
			}
		}
	}
	if full {
		// A host whose rows are gone from the store is gone from the fleet.
		for name := range f.known {
			if !present[name] {
				delete(f.known, name)
				delete(f.hosts, name)
				delete(f.pending, name)
			}
		}
		if f.only != "" && len(f.known) == 0 {
			var names []string
			for _, h := range listed {
				if h.Name != "" {
					names = append(names, h.Name)
				}
			}
			return nil, errors.New(say(f.lang, "err.fleet_host", "host", f.only, "store", f.st.Origin(), "n", len(names), "hosts", hostList(names)))
		}
	}

	// Hosts without new rows whose findings have aged: a share of them per
	// scan, the oldest first, so that every host is read again within
	// fleetMaxAge and no scan reads them all.
	var aged []string
	for name, h := range f.hosts {
		if !f.pending[name] && now.Sub(h.readAt) >= fleetMaxAge {
			aged = append(aged, name)
		}
	}
	sort.Slice(aged, func(i, j int) bool {
		a, b := f.hosts[aged[i]], f.hosts[aged[j]]
		if !a.readAt.Equal(b.readAt) {
			return a.readAt.Before(b.readAt)
		}
		return aged[i] < aged[j]
	})
	budget := 1 + int(float64(len(f.hosts))*float64(f.interval)/float64(fleetMaxAge))
	for i, name := range aged {
		if i >= budget {
			break
		}
		f.pending[name] = true
	}

	names := make([]string, 0, len(f.pending))
	for name := range f.pending {
		names = append(names, name)
	}
	sort.Strings(names)
	read := 0
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		h, err := f.read(ctx, name, opts, now)
		if err != nil {
			// The host stays pending and is read by the next scan.
			return nil, err
		}
		f.hosts[name] = h
		delete(f.pending, name)
		read++
	}

	st := &fleetState{src: f, opts: opts, set: &exporter.Set{}, origin: f.st.Origin(), read: read, unnamed: f.unnamed, grades: map[model.VerdictGrade]int{}}
	for name, h := range f.hosts {
		st.names = append(st.names, name)
		st.set.Merge(h.set)
		st.grades[h.grade]++
	}
	sort.Strings(st.names)
	return st, nil
}

// open reads one host of the store into a new ledger and runs the engine
// on it. The caller calls done when it has what it needs.
func (f *fleetSource) open(ctx context.Context, host string, opts rasdaemon.ReadOptions, now time.Time) (c *view.Case, h *engine.HostResult, done func(), err error) {
	tmp, err := os.MkdirTemp("", "my-fleet-")
	if err != nil {
		return nil, nil, nil, err
	}
	var ledger *store.Store
	cleanup := func() {
		if ledger != nil {
			ledger.Close()
		}
		os.RemoveAll(tmp)
	}
	defer func() {
		if err != nil {
			cleanup()
		}
	}()
	if ledger, err = store.Open(ctx, filepath.Join(tmp, "ledger.db"), store.Create); err != nil {
		return nil, nil, nil, err
	}
	if err = readStoreHost(ctx, f.st, ledger, host, opts, now); err != nil {
		return nil, nil, nil, err
	}
	if c, err = view.Open(ctx, ledger, f.lang, engine.Options{Config: &f.cfg, Now: now}); err != nil {
		return nil, nil, nil, err
	}
	if h, err = c.Host(host); err != nil {
		return nil, nil, nil, err
	}
	return c, h, cleanup, nil
}

// read makes the findings for one host.
func (f *fleetSource) read(ctx context.Context, host string, opts rasdaemon.ReadOptions, now time.Time) (*fleetHost, error) {
	c, h, done, err := f.open(ctx, host, opts, now)
	if err != nil {
		return nil, err
	}
	defer done()
	set, err := exporter.Host(ctx, c.Store(), h)
	if err != nil {
		return nil, err
	}
	return &fleetHost{set: set, grade: h.Grade, readAt: now}, nil
}

// fleetState is what a scan of the store found: the metrics of every
// host. The detail about a DIMM is read from the store when it is asked
// for.
type fleetState struct {
	src    *fleetSource
	opts   rasdaemon.ReadOptions
	set    *exporter.Set
	origin string
	// names are the hosts, in order; grades counts them by finding.
	names  []string
	grades map[model.VerdictGrade]int
	// read is the number of hosts this scan read.
	read    int
	unnamed int64
}

func (s *fleetState) Metrics() *exporter.Set { return s.set }
func (s *fleetState) Close()                 {}

// DIMM answers /api/dimm for one host of the fleet, which the request
// names with ?host=. The rows of the host are read for the answer, so it
// is as of the request.
func (s *fleetState) DIMM(ctx context.Context, q serve.Query) (any, error) {
	lang := s.src.lang
	if q.Lang != "" {
		l, err := i18n.ParseLang(q.Lang)
		if err != nil {
			return nil, &serve.Error{Status: http.StatusBadRequest, Message: err.Error()}
		}
		lang = l
	}
	host := ""
	switch {
	case q.Host == "" && len(s.names) == 1:
		host = s.names[0]
	case q.Host == "":
		return nil, &serve.Error{Status: http.StatusBadRequest, Message: say(lang, "err.fleet_api_host", "n", len(s.names), "hosts", hostList(s.names))}
	default:
		for _, n := range s.names {
			if strings.EqualFold(n, q.Host) {
				host = n
			}
		}
		if host == "" {
			return nil, &serve.Error{Status: http.StatusNotFound, Message: say(lang, "err.fleet_host", "host", q.Host, "store", s.origin, "n", len(s.names), "hosts", hostList(s.names))}
		}
	}
	c, h, done, err := s.src.open(ctx, host, s.opts, s.src.clock())
	if err != nil {
		return nil, err
	}
	defer done()
	return whyDocument(ctx, c.In(lang), h, lang, serve.Query{Ref: q.Ref, All: q.All})
}

// summary says in one line what the scan found, for the log.
func (s *fleetState) summary(lang i18n.Lang) string {
	var parts []string
	for _, g := range []model.VerdictGrade{model.VerdictHoldReplacement, model.VerdictReplaceNow, model.VerdictConsiderReplacement, model.VerdictObserve, model.VerdictOK} {
		if n := s.grades[g]; n > 0 {
			parts = append(parts, say(lang, "serve.fleet_grade", "n", n, "grade", engine.GradeText(lang, g)))
		}
	}
	return say(lang, "serve.fleet", "store", s.origin, "n", len(s.names), "grades", strings.Join(parts, ", "))
}
