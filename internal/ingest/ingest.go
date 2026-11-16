// Package ingest runs input adapters against a ledger and keeps the ledger
// consistent afterwards.
//
// One run reads the sources of one host. Every source gets an entry in the
// source inventory, whether it could be read or not. After the sources are
// in, the ledger passes run in a fixed order: slots and DIMM identities,
// counter events, the slots of events, actions and ECS readings, boots,
// carried records, DIMM attribution, duplicates, boot outcomes, counter
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
	// ReadDeadline ends the reading of sources: a source that is not
	// started by then is recorded as not read, and one that is read at the
	// time is stopped. The passes run all the same. Zero means no
	// deadline.
	ReadDeadline time.Time
	// AfterSource is called after the i-th source is in the ledger, before
	// the next is read. An error ends the run.
	AfterSource func(i int, src model.Source) error
}

// NotReadDetail is the detail of a source that the deadline of a run left
// unread.
const NotReadDetail = "not read: the run reached its deadline"

// Result summarises a run.
type Result struct {
	Host string
	Scan model.Scan
	// Sources is the source inventory of the run, one entry per adapter
	// and one for the slot mapping.
	Sources []model.Source
	// Events, Actions and Samples count the records this run added to the
	// ledger. Samples are GPU memory records and ECS readings.
	Events  int
	Actions int
	Samples int
	// Informational counts the records among Events that are no error:
	// a stop of the host, a panic.
	Informational int
	// Duplicates is the number of records of the host that are marked as
	// duplicates of another record after the run.
	Duplicates int
	// Aligned says what was done with boots whose records carry no exact
	// time (see store.AlignTimes).
	Aligned []store.Alignment
	// Clocks says which rasdaemon records were given the kernel's time
	// (see store.AlignClocks).
	Clocks []store.ClockAlignment
	// BMCClocks says which BMC sources had their times moved by the
	// difference of the BMC's clock (see store.AlignBMCClock).
	BMCClocks []store.BMCClockAlignment
}

// FirmwarePassesKind is the source inventory entry that names a pass over
// the firmware records that did not run.
const FirmwarePassesKind = "fw-passes"

// passFault, when set by a test, makes a firmware pass fail.
var passFault func(name string) error

// passMeaning says what a firmware pass that did not run means for the
// counts.
var passMeaning = map[string]string{
	"AlignBMCClock":       "the times of a BMC whose clock was off are as it stamped them, and its records may not be matched with the host's",
	"FirmwareRepeats":     "an entry that folds repeats counts its repeats once per reading",
	"FirmwareWithdrawals": "a mark that a later record took back still counts as holding",
	"FirmwareDuplicates":  "one error that a BMC logged in two logs, or in a burst, may count more than once",
}

// HostHinter is implemented by adapters that can name the host before any
// record is read.
type HostHinter interface {
	HostHint() string
}

// BoardInventory is implemented by adapters whose modules are an inventory
// of the whole board, the way the SMBIOS tables are: the memory resources
// of a BMC. Such a source is checked against SMBIOS before it is taken. The
// BMC of another machine would add the slots, the DIMMs and the errors of
// that machine to this host.
type BoardInventory interface {
	BoardInventory() bool
}

// errOtherMachine rolls back a source that describes another machine.
var errOtherMachine = errors.New("the source describes another machine")

// errReaderFailed rolls back a source whose reader failed.
var errReaderFailed = errors.New("the reader failed")

// SlotMapKind is the source inventory entry that reports how EDAC positions
// were tied to SMBIOS slots.
const SlotMapKind = "slot-map"

// TimeAlignKind is the source inventory entry that reports what was done to
// the times of records: records without a date that were dated by the
// records of another source, and rasdaemon records that were given the
// kernel's time.
const TimeAlignKind = "time-align"

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

	// The serial numbers of the DIMMs SMBIOS lists for the host: those the
	// ledger has, and those of this run as they are read. They are what an
	// inventory of the whole board is checked against.
	var board map[string]bool
	for _, a := range adapters {
		if b, ok := a.(BoardInventory); ok && b.BoardInventory() {
			if board, err = boardSerials(ctx, s, host); err != nil {
				return nil, fmt.Errorf("ingest: %w", err)
			}
			break
		}
	}

	var modules []*model.Module
	for i, a := range adapters {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		src := model.Source{ScanID: res.Scan.ID, Kind: a.Kind(), Origin: a.Origin()}
		readCtx, cancel := ctx, context.CancelFunc(func() {})
		if !opts.ReadDeadline.IsZero() {
			if !time.Now().Before(opts.ReadDeadline) {
				src.Status, src.Detail = model.SourceError, NotReadDetail
				if err := s.Update(ctx, func(w *store.Writer) error { return w.InsertSource(&src) }); err != nil {
					return nil, fmt.Errorf("ingest: %w", err)
				}
				res.Sources = append(res.Sources, src)
				continue
			}
			readCtx, cancel = context.WithDeadline(ctx, opts.ReadDeadline)
		}
		whole, _ := a.(BoardInventory)
		// What a BMC says about the machine fills what the host does not
		// say about itself.
		sk := &sink{host: host, scanID: res.Scan.ID, src: &src, fill: whole != nil && whole.BoardInventory()}
		var rep adapter.Report
		listed := 0
		err := s.Update(ctx, func(w *store.Writer) error {
			sk.w = w
			rep = adapter.Safely(readCtx, a, sk)
			// A record the ledger could not take rolls back the whole
			// source: half a source would look like a complete one.
			if rep.SinkErr != nil {
				return rep.SinkErr
			}
			// So does a reader that failed: what it gave before is not
			// known to be right.
			if rep.Failed {
				return errReaderFailed
			}
			if sk.fill {
				if listed = otherMachine(sk.modules, board); listed > 0 {
					return errOtherMachine
				}
			}
			return nil
		})
		if errors.Is(err, errOtherMachine) {
			// Nothing of it is in the ledger. The inventory says why.
			src = model.Source{ScanID: res.Scan.ID, Kind: a.Kind(), Origin: a.Origin()}
			sk = &sink{host: host, scanID: res.Scan.ID, src: &src}
			rep = adapter.Report{Status: model.SourceError, Detail: fmt.Sprintf(
				"describes another machine: none of the %d DIMM serial numbers it lists is among the %d SMBIOS lists for this host. Nothing of it was added",
				listed, len(board))}
			err = nil
		}
		if errors.Is(err, errReaderFailed) {
			// Nothing of it is in the ledger. The report says why.
			src = model.Source{ScanID: res.Scan.ID, Kind: a.Kind(), Origin: a.Origin()}
			sk = &sink{host: host, scanID: res.Scan.ID, src: &src}
			err = nil
		}
		cancel()
		if err != nil {
			return nil, fmt.Errorf("ingest %s %s: %w", a.Kind(), a.Origin(), err)
		}
		for _, m := range sk.modules {
			if k := serialKey(m.DIMM.Serial); k != "" && m.Handle.Valid && m.Populated && board != nil {
				board[k] = true
			}
		}
		src.Status, src.Detail = rep.Status, rep.Detail
		src.Records = rep.Records
		src.Skipped = rep.Skipped + sk.tally.Total()
		src.Unread, src.Faults = rep.Unread, rep.Faults
		src.Notes = append(src.Notes, rep.Notes...)
		src.Notes = append(src.Notes, sk.tally.Notes()...)
		if sk.known > 0 {
			src.Notes = append(src.Notes, fmt.Sprintf("%d records were already in the ledger", sk.known))
		}
		if (sk.tally.Total() > 0 || src.Unread > 0 || src.Faults > 0) && src.Status == model.SourceOK {
			src.Status = model.SourcePartial
		}
		res.Events += sk.newEvents
		res.Informational += sk.newInformational
		res.Actions += sk.newActions
		res.Samples += sk.newSamples
		modules = append(modules, sk.modules...)
		if err := s.Update(ctx, func(w *store.Writer) error {
			if err := w.InsertSource(&src); err != nil {
				return err
			}
			if len(rep.UnreadRecords) == 0 {
				return nil
			}
			return w.InsertUnread(src.ID, rep.UnreadRecords)
		}); err != nil {
			return nil, fmt.Errorf("ingest: %w", err)
		}
		res.Sources = append(res.Sources, src)
		if opts.AfterSource != nil {
			if err := opts.AfterSource(i, src); err != nil {
				return nil, err
			}
		}
	}

	// Slots, DIMM identities and placements from what this run observed,
	// then the mapping between EDAC positions and SMBIOS slots from
	// everything the ledger knows.
	taken, err := inventoryLabels(ctx, s, host)
	if err != nil {
		return nil, fmt.Errorf("ingest: inventory: %w", err)
	}
	if err := s.Update(ctx, func(w *store.Writer) error { return recordInventory(w, host, modules, taken) }); err != nil {
		return nil, fmt.Errorf("ingest: inventory: %w", err)
	}
	// Two inventories of one board (SMBIOS and the BMC) are joined before
	// EDAC positions are tied to slots, so that they are tied to the slots
	// that remain.
	joined, err := joinInventories(ctx, s, host)
	if err != nil {
		return nil, fmt.Errorf("ingest: inventories: %w", err)
	}
	slots, err := remapSlots(ctx, s, host)
	if err != nil {
		return nil, fmt.Errorf("ingest: slots: %w", err)
	}
	slots.notes = append(joined.notes(), slots.notes...)
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

	// The passes over firmware records each run in a transaction of their
	// own. One that fails is rolled back and named in the inventory, and
	// the run goes on: the counts are then those without it.
	var failedPasses []string
	fwPass := func(name string, fn func() error) {
		err := func() (err error) {
			if !adapter.DebugPanics() {
				defer func() {
					if v := recover(); v != nil {
						err = fmt.Errorf("%v", v)
					}
				}()
			}
			if passFault != nil {
				if err := passFault(name); err != nil {
					return err
				}
			}
			return fn()
		}()
		if err != nil {
			failedPasses = append(failedPasses, fmt.Sprintf("%s did not run (%v): %s", name, err, passMeaning[name]))
		}
	}
	steps := []struct {
		name string
		fn   func() error
	}{
		{"counter events", func() error { return s.DeriveCounterEvents(ctx, host) }},
		{"event slots", func() error { return resolveEventSlots(ctx, s, host) }},
		{"action slots", func() error { return resolveActionSlots(ctx, s, host) }},
		{"ECS slots", func() error { return resolveECSSlots(ctx, s, host) }},
		{"BMC memory slots", func() error { return resolveBMCMemorySlots(ctx, s, host) }},
		{"boots", func() error { return s.LinkBoots(ctx, host) }},
		{"clocks", func() (err error) {
			// Before the records without a date are dated by the others.
			if res.Clocks, err = s.AlignClocks(ctx, host); err != nil {
				return err
			}
			// The clock of a BMC, against the host's records.
			fwPass("AlignBMCClock", func() (err error) {
				res.BMCClocks, err = s.AlignBMCClock(ctx, host)
				return err
			})
			if len(res.Clocks) == 0 && len(res.BMCClocks) == 0 {
				return nil
			}
			return s.LinkBoots(ctx, host)
		}},
		{"times", func() (err error) {
			if res.Aligned, err = s.AlignTimes(ctx, host); err != nil {
				return err
			}
			// Records that got a date belong to the boot of that date.
			return s.LinkBoots(ctx, host)
		}},
		// Records kept over a reset belong to the boot before the one
		// that read them.
		{"carried records", func() error { return s.LinkCarried(ctx, host) }},
		{"DIMM attribution", func() error { return s.LinkDIMMs(ctx, host) }},
		// What firmware records say once: an entry read twice, a burst, the
		// same error in two logs of a BMC; repeats; marks taken back.
		{"firmware records", func() error {
			fwPass("FirmwareRepeats", func() error { return s.FirmwareRepeats(ctx, host) })
			fwPass("FirmwareWithdrawals", func() error { return s.FirmwareWithdrawals(ctx, host) })
			fwPass("FirmwareDuplicates", func() (err error) {
				_, err = s.FirmwareDuplicates(ctx, host)
				return err
			})
			return nil
		}},
		{"duplicates", func() (err error) {
			res.Duplicates, err = s.MarkDuplicates(ctx, host, opts.Tolerance)
			return err
		}},
		{"boot outcomes", func() error { return s.BootOutcomes(ctx, host) }},
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

	if len(failedPasses) > 0 {
		src := model.Source{ScanID: res.Scan.ID, Kind: FirmwarePassesKind, Origin: host, Status: model.SourcePartial,
			Detail: "a pass over the firmware records did not run; the counts are those without it", Notes: failedPasses}
		if err := s.Update(ctx, func(w *store.Writer) error { return w.InsertSource(&src) }); err != nil {
			return nil, fmt.Errorf("ingest: %w", err)
		}
		res.Sources = append(res.Sources, src)
	}

	// What was done to times is part of the inventory: it changes how
	// records are counted.
	if len(res.Aligned) > 0 || len(res.Clocks) > 0 || len(res.BMCClocks) > 0 {
		src := model.Source{ScanID: res.Scan.ID, Kind: TimeAlignKind, Origin: host, Status: model.SourceOK}
		for _, c := range res.Clocks {
			src.Records += c.Records
			src.Notes = append(src.Notes, clockNote(c))
		}
		for _, c := range res.BMCClocks {
			src.Records += c.Records
			src.Notes = append(src.Notes, bmcClockNote(c))
		}
		for _, a := range res.Aligned {
			src.Records += a.Records
			if a.Aligned {
				note := fmt.Sprintf("the times of %d records were set by %d errors another source reports with a date", a.Records, a.Matched)
				if !a.StartedAt.IsZero() && a.StartedAt.Year() > 1970 {
					note += fmt.Sprintf(": their boot started %s UTC", a.StartedAt.UTC().Format("2006-01-02 15:04:05"))
				}
				src.Notes = append(src.Notes, note)
				continue
			}
			src.Status = model.SourcePartial
			src.Skipped += a.Records
			src.Detail = "records without a date could not be matched with the dated records of the same errors; they may be counted twice"
			src.Notes = append(src.Notes, fmt.Sprintf("%d records of one boot carry no date; dated records of the same errors exist and do not settle on one boot time", a.Records))
		}
		if err := s.Update(ctx, func(w *store.Writer) error { return w.InsertSource(&src) }); err != nil {
			return nil, fmt.Errorf("ingest: %w", err)
		}
		res.Sources = append(res.Sources, src)
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

// bmcClockNote says in a sentence what store.AlignBMCClock did.
func bmcClockNote(c store.BMCClockAlignment) string {
	side, by := "ahead of", -c.Offset
	if c.Offset > 0 {
		side, by = "behind", c.Offset
	}
	h, m := int(by.Hours()), int(by.Minutes())%60
	return fmt.Sprintf("the clock of the BMC (%s) was %d h %02d min %s the host's, by %d errors both report; %d records were moved",
		c.Source, h, m, side, c.Matched, c.Records)
}

// clockNote says in a sentence what store.AlignClocks did with a boot.
func clockNote(c store.ClockAlignment) string {
	side, by := "ahead of", -c.Offset
	if c.Offset > 0 {
		side, by = "behind", c.Offset
	}
	return fmt.Sprintf("rasdaemon stamped %d records of one boot %s %s the kernel's clock: they are dated by the kernel's time, which the kernel log gives for %d of the errors",
		c.Records, by, side, c.Matched)
}

// sink writes the records of one adapter into the ledger.
type sink struct {
	w      *store.Writer
	host   string
	scanID int64
	src    *model.Source
	tally  adapter.Tally

	// fill says that the facts of the source about the host only fill
	// what is not known.
	fill bool

	known            int
	newEvents        int
	newInformational int
	newActions       int
	newSamples       int
	modules          []*model.Module
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
	if e.CauseFingerprint != "" {
		e.CauseFingerprint = model.Fingerprint(k.host, e.CauseFingerprint)
	}
	inserted, err := k.w.InsertEvent(e)
	if err != nil {
		return k.refuse(e.RawRef, err)
	}
	k.src.Events++
	if e.ErrorSeverity == model.SevInformational {
		k.src.Informational++
		if inserted {
			k.newInformational++
		}
	}
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

func (k *sink) GPU(g *model.GPUMem) error {
	g.Host, g.ScanID = k.host, k.scanID
	g.Fingerprint = model.Fingerprint(k.host, g.Fingerprint)
	inserted, err := k.w.InsertGPUMem(g)
	if err != nil {
		return k.refuse(g.RawRef, err)
	}
	k.sample(g.ObservedAt, g.TimeQuality, inserted)
	return nil
}

func (k *sink) ECS(e *model.ECSSample) error {
	e.Host, e.ScanID = k.host, k.scanID
	e.Fingerprint = model.Fingerprint(k.host, e.Fingerprint)
	inserted, err := k.w.InsertECS(e)
	if err != nil {
		return k.refuse(e.RawRef, err)
	}
	k.sample(e.ObservedAt, e.TimeQuality, inserted)
	return nil
}

func (k *sink) BMC(b *model.BMC) error {
	b.Host, b.ScanID = k.host, k.scanID
	b.Fingerprint = model.Fingerprint(k.host, b.Fingerprint)
	inserted, err := k.w.InsertBMC(b)
	if err != nil {
		return k.refuse(b.RawRef, err)
	}
	k.sample(b.ObservedAt, model.TimeExact, inserted)
	return nil
}

func (k *sink) BIOSSetting(s *model.BIOSSetting) error {
	s.Host, s.ScanID = k.host, k.scanID
	if err := k.w.PutBIOSSetting(s); err != nil {
		return k.refuse(s.RawRef, err)
	}
	k.sample(s.ObservedAt, model.TimeExact, true)
	return nil
}

func (k *sink) BMCMemory(m *model.BMCMemory) error {
	m.Host, m.ScanID = k.host, k.scanID
	m.Fingerprint = model.Fingerprint(k.host, m.Fingerprint)
	inserted, err := k.w.InsertBMCMemory(m)
	if err != nil {
		return k.refuse(m.RawRef, err)
	}
	k.sample(m.ObservedAt, model.TimeExact, inserted)
	return nil
}

// Firmware keeps a firmware record that makes neither an event nor an
// action. It is counted as a record of the source, in no total.
func (k *sink) Firmware(f *model.FirmwareRecord) error {
	f.Host, f.ScanID = k.host, k.scanID
	f.Fingerprint = model.Fingerprint(k.host, f.Fingerprint)
	inserted, err := k.w.InsertFirmware(f)
	if err != nil {
		return k.refuse(f.RawRef, err)
	}
	k.sample(f.At, model.TimeExact, inserted)
	return nil
}

// Registry records a message registry a BMC published.
func (k *sink) Registry(r *model.RegistrySeen) error {
	r.Host, r.ScanID = k.host, k.scanID
	if r.Source == "" {
		r.Source = k.src.Origin
	}
	return k.refuse(r.Prefix, k.w.PutRegistrySeen(r))
}

// sample counts a reading that is neither an event nor an action: a GPU
// memory record, an ECS reading, or what a BMC says about itself, the BIOS
// settings and a memory module.
func (k *sink) sample(t time.Time, q model.TimeQuality, inserted bool) {
	k.src.Samples++
	if q != model.TimeRelative {
		// A time counted from the epoch says nothing about the period the
		// source covers.
		k.seen(t)
	}
	if inserted {
		k.newSamples++
	} else {
		k.known++
	}
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
	if k.fill {
		return k.refuse(k.src.Origin, k.w.FillHost(h))
	}
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
