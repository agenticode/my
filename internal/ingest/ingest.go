// Package ingest runs input adapters against a ledger and keeps the ledger
// consistent afterwards.
//
// One run reads the sources of one host. Every source gets an entry in the
// source inventory, whether it could be read or not. After the sources are
// in, the ledger passes run in a fixed order: slots and DIMM identities,
// counter events, event slots, boots, DIMM attribution, duplicates, counter
// coverage. Each pass recomputes its result from the ledger, so importing
// sources in another order, or importing a source again, gives the same
// ledger.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/buildinfo"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// Options describe a run.
type Options struct {
	// Host is the name the records are stored under. When empty the name
	// is taken from the first adapter that knows the host name, and is
	// "localhost" when none does.
	Host string
	// Kind is the scan kind: model.ScanLive, model.ScanImport (the
	// default) or model.ScanSynthetic.
	Kind string
	// Now is the time of the run; zero means the current time.
	Now time.Time
	// Privileged records whether the run had root privileges.
	Privileged bool
	// Note is free text stored with the scan.
	Note string
	// Tolerance bounds the time difference between two reports of the same
	// error from different sources. Zero values use the defaults.
	Tolerance store.DuplicateTolerance
}

// Result summarises a run.
type Result struct {
	Host string
	Scan model.Scan
	// Sources is the source inventory of the run, one entry per adapter
	// and one for the slot mapping.
	Sources []model.Source
	// Events and Actions count the records this run added to the ledger.
	Events  int
	Actions int
	// Duplicates is the number of records of the host that are marked as
	// duplicates of another record after the run.
	Duplicates int
}

// HostHinter is implemented by adapters that can name the host before any
// record is read.
type HostHinter interface {
	HostHint() string
}

// SlotMapKind is the source inventory entry that reports how EDAC positions
// were tied to SMBIOS slots.
const SlotMapKind = "slot-map"

// Run reads the adapters into the ledger. A source that cannot be read does
// not fail the run: it is recorded in the source inventory with the reason.
// Run returns an error only when the ledger itself fails.
func Run(ctx context.Context, s *store.Store, adapters []adapter.Adapter, opts Options) (*Result, error) {
	host := strings.TrimSpace(opts.Host)
	if host == "" {
		for _, a := range adapters {
			if h, ok := a.(HostHinter); ok {
				if n := strings.TrimSpace(h.HostHint()); n != "" {
					host = n
					break
				}
			}
		}
	}
	if host == "" {
		host = "localhost"
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	kind := opts.Kind
	if kind == "" {
		kind = model.ScanImport
	}

	res := &Result{Host: host}
	res.Scan = model.Scan{Host: host, Kind: kind, StartedAt: now, ToolVersion: buildinfo.Version, Privileged: opts.Privileged, Note: opts.Note}
	err := s.Update(ctx, func(w *store.Writer) error {
		if err := w.InsertScan(&res.Scan); err != nil {
			return err
		}
		return w.UpsertHost(&model.Host{Name: host, FirstSeen: now, LastSeen: now})
	})
	if err != nil {
		return nil, fmt.Errorf("ingest: %w", err)
	}

	var modules []*model.Module
	for _, a := range adapters {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		src := model.Source{ScanID: res.Scan.ID, Kind: a.Kind(), Origin: a.Origin()}
		sk := &sink{host: host, scanID: res.Scan.ID, src: &src}
		var rep adapter.Report
		err := s.Update(ctx, func(w *store.Writer) error {
			sk.w = w
			rep = a.Collect(ctx, sk)
			// A record the ledger could not take rolls back the whole
			// source: half a source would look like a complete one.
			return rep.SinkErr
		})
		if err != nil {
			return nil, fmt.Errorf("ingest %s %s: %w", a.Kind(), a.Origin(), err)
		}
		src.Status, src.Detail = rep.Status, rep.Detail
		src.Records = rep.Records
		src.Skipped = rep.Skipped + sk.tally.Total()
		src.Notes = append(src.Notes, rep.Notes...)
		src.Notes = append(src.Notes, sk.tally.Notes()...)
		if sk.known > 0 {
			src.Notes = append(src.Notes, fmt.Sprintf("%d records were already in the ledger", sk.known))
		}
		if sk.tally.Total() > 0 && src.Status == model.SourceOK {
			src.Status = model.SourcePartial
		}
		res.Events += sk.newEvents
		res.Actions += sk.newActions
		modules = append(modules, sk.modules...)
		if err := s.Update(ctx, func(w *store.Writer) error { return w.InsertSource(&src) }); err != nil {
			return nil, fmt.Errorf("ingest: %w", err)
		}
		res.Sources = append(res.Sources, src)
	}

	// Slots, DIMM identities and placements from what this run observed,
	// then the mapping between EDAC positions and SMBIOS slots from
	// everything the ledger knows.
	if err := s.Update(ctx, func(w *store.Writer) error { return recordInventory(w, host, modules) }); err != nil {
		return nil, fmt.Errorf("ingest: inventory: %w", err)
	}
	slots, err := remapSlots(ctx, s, host)
	if err != nil {
		return nil, fmt.Errorf("ingest: slots: %w", err)
	}
	if len(slots.notes) > 0 {
		src := model.Source{ScanID: res.Scan.ID, Kind: SlotMapKind, Origin: host, Status: model.SourceOK,
			Records: slots.mapped + slots.unmapped, Skipped: slots.unmapped, Notes: slots.notes}
		if slots.unmapped > 0 {
			src.Status = model.SourcePartial
			src.Detail = "EDAC positions and SMBIOS slots could not all be matched"
		}
		if err := s.Update(ctx, func(w *store.Writer) error { return w.InsertSource(&src) }); err != nil {
			return nil, fmt.Errorf("ingest: %w", err)
		}
		res.Sources = append(res.Sources, src)
	}

	steps := []struct {
		name string
		fn   func() error
	}{
		{"counter events", func() error { return s.DeriveCounterEvents(ctx, host) }},
		{"event slots", func() error { return resolveEventSlots(ctx, s, host) }},
		{"boots", func() error { return s.LinkBoots(ctx, host) }},
		{"DIMM attribution", func() error { return s.LinkDIMMs(ctx, host) }},
		{"duplicates", func() (err error) {
			res.Duplicates, err = s.MarkDuplicates(ctx, host, opts.Tolerance)
			return err
		}},
		{"counter coverage", func() error { return s.ReconcileCounters(ctx, host) }},
		{"orphans", func() error { return s.PruneOrphans(ctx, host) }},
	}
	for _, st := range steps {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := st.fn(); err != nil {
			return nil, fmt.Errorf("ingest: %s: %w", st.name, err)
		}
	}

	finished := time.Now().UTC()
	if !opts.Now.IsZero() {
		// A run with a fixed clock (tests, synthetic data) ends when it began.
		finished = now
	}
	if err := s.Update(ctx, func(w *store.Writer) error { return w.FinishScan(res.Scan.ID, finished) }); err != nil {
		return nil, fmt.Errorf("ingest: %w", err)
	}
	res.Scan.FinishedAt = finished
	return res, nil
}

// sink writes the records of one adapter into the ledger.
type sink struct {
	w      *store.Writer
	host   string
	scanID int64
	src    *model.Source
	tally  adapter.Tally

	known      int
	newEvents  int
	newActions int
	modules    []*model.Module
}

// refuse handles an error of the ledger about one record. A record with
// unusable content is skipped and noted; any other error stops the source.
func (k *sink) refuse(ref string, err error) error {
	if errors.Is(err, store.ErrInvalidRecord) {
		k.tally.Skip(ref, strings.TrimPrefix(err.Error(), store.ErrInvalidRecord.Error()+": "))
		return nil
	}
	return err
}

func (k *sink) seen(t time.Time) {
	if k.src.FirstTS.IsZero() || t.Before(k.src.FirstTS) {
		k.src.FirstTS = t
	}
	if t.After(k.src.LastTS) {
		k.src.LastTS = t
	}
}

func (k *sink) Event(e *model.Event) error {
	e.Host, e.ScanID = k.host, k.scanID
	// Two hosts can hold the same record identity (row 1 of a rasdaemon
	// database, stored in the same second), so the host is part of it.
	e.Fingerprint = model.Fingerprint(k.host, e.Fingerprint)
	if e.DupOfFingerprint != "" {
		e.DupOfFingerprint = model.Fingerprint(k.host, e.DupOfFingerprint)
	}
	inserted, err := k.w.InsertEvent(e)
	if err != nil {
		return k.refuse(e.RawRef, err)
	}
	k.src.Events++
	switch e.Grade {
	case model.GradeA:
		k.src.GradeA++
	case model.GradeB:
		k.src.GradeB++
	default:
		k.src.GradeC++
	}
	k.seen(e.ErrorDT)
	if inserted {
		k.newEvents++
	} else {
		k.known++
	}
	return nil
}

func (k *sink) Action(a *model.Action) error {
	a.Host, a.ScanID = k.host, k.scanID
	a.Fingerprint = model.Fingerprint(k.host, a.Fingerprint)
	inserted, err := k.w.InsertAction(a)
	if err != nil {
		return k.refuse(a.RawRef, err)
	}
	k.src.Actions++
	k.seen(a.At)
	if inserted {
		k.newActions++
	} else {
		k.known++
	}
	return nil
}

func (k *sink) Boot(b *model.Boot) error {
	b.Host = k.host
	if _, err := k.w.UpsertBoot(b); err != nil {
		return k.refuse(b.RawRef, err)
	}
	return nil
}

func (k *sink) Host(h *model.Host) error {
	h.Name = k.host
	return k.refuse(k.src.Origin, k.w.UpsertHost(h))
}

func (k *sink) Module(m *model.Module) error {
	k.modules = append(k.modules, m)
	return nil
}

func (k *sink) Counter(c *model.CounterSample) error {
	if _, err := k.w.InsertCounterSample(k.host, c); err != nil {
		return k.refuse(c.RawRef, err)
	}
	return nil
}
