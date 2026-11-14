package dcbrain

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
)

// Kind is the adapter name in the source inventory.
const Kind = "dcbrain"

// DefaultMaxRows bounds the rows of one server the adapter holds in
// memory. A few servers of the dataset have tens of millions of rows.
const DefaultMaxRows = 4_000_000

// Options control how a server is read.
type Options struct {
	// Base is the real time day 0 of the dataset calendar is placed at;
	// the zero value means DefaultBase.
	Base time.Time
	// MaxRows bounds the rows read for the server; zero means
	// DefaultMaxRows. Rows beyond it are left out and the report says so.
	MaxRows int
}

func (o Options) base() time.Time {
	if o.Base.IsZero() {
		return DefaultBase
	}
	return o.Base.UTC()
}

// Adapter reads the records of one server of the dataset, so that the
// server can be imported into a ledger like any other host.
type Adapter struct {
	ds   *Dataset
	sid  string
	opts Options
}

// NewServer returns an adapter for the server with the given ID.
func NewServer(ds *Dataset, sid string, opts Options) *Adapter {
	return &Adapter{ds: ds, sid: sid, opts: opts}
}

func (a *Adapter) Kind() string   { return Kind }
func (a *Adapter) Origin() string { return a.ds.Path + "#" + a.sid }

// HostHint names the host: the server ID.
func (a *Adapter) HostHint() string { return a.sid }

// Collect reads the rows of the server into sink. Rows that are equal in
// every column are stored as one record with a count.
func (a *Adapter) Collect(ctx context.Context, sink adapter.Sink) adapter.Report {
	rep := adapter.Report{Kind: Kind, Origin: a.Origin(), HostHint: a.sid}
	base := a.opts.base()
	limit := a.opts.MaxRows
	if limit <= 0 {
		limit = DefaultMaxRows
	}
	fail := func(err error) adapter.Report {
		rep.Status, rep.Detail = adapter.StatusForError(err)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			rep.Status, rep.Detail = model.SourceError, "reading was interrupted"
		}
		return rep
	}

	rc, err := a.ds.OpenTable(TableErrors)
	if err != nil {
		return fail(err)
	}
	var recs []Rec
	over := 0
	name := a.ds.Name(TableErrors)
	tr, err := ReadErrors(ctx, rc, name, func(r *Row) error {
		if r.SID != a.sid {
			return nil
		}
		if len(recs) >= limit {
			over++
			return nil
		}
		recs = append(recs, r.Rec())
		return nil
	})
	rc.Close()
	if err != nil {
		return fail(err)
	}
	total := len(recs) + over
	rep.Records = total
	if tr.Skipped > 0 {
		rep.Notes = append(rep.Notes, fmt.Sprintf("%d lines of %s could not be read; they may belong to any server", tr.Skipped, name))
		rep.Notes = append(rep.Notes, tr.Notes...)
	}

	if err := a.inventory(sink, &rep, base); err != nil {
		rep.SinkErr = err
		return fail(err)
	}

	recs = Merge(recs, 1)
	var e model.Event
	for i := range recs {
		rec := &recs[i]
		Fill(&e, "", rec, base)
		e.RawRef = name + ":" + strconv.FormatUint(uint64(rec.Line), 10)
		e.Raw = rawText(a.sid, rec)
		e.Fingerprint = model.Fingerprint(Kind, a.sid, e.Raw)
		ev := e
		if err := sink.Event(&ev); err != nil {
			rep.SinkErr = err
			return fail(err)
		}
	}
	a.tickets(&rep)

	rep.Notes = append(rep.Notes, fmt.Sprintf("dates are the dataset's own calendar with its first day placed at %s; they are not the dates the errors happened on", base.Format("2006-01-02")))
	switch {
	case over > 0:
		rep.Status = model.SourcePartial
		rep.Skipped += over
		rep.Detail = fmt.Sprintf("the server has %d rows; the first %d were read", total, limit)
	case rep.Records == 0:
		rep.Status = model.SourceEmpty
		rep.Detail = "no rows of server " + a.sid
	default:
		rep.Status = model.SourceOK
	}
	return rep
}

// inventory passes on what the inventory table says about the server.
func (a *Adapter) inventory(sink adapter.Sink, rep *adapter.Report, base time.Time) error {
	if !a.ds.Has(TableInventory) {
		rep.Notes = append(rep.Notes, "no inventory table next to the error table")
		return nil
	}
	rc, err := a.ds.OpenTable(TableInventory)
	if err != nil {
		rep.Notes = append(rep.Notes, "inventory table: "+err.Error())
		return nil
	}
	defer rc.Close()
	servers, _, err := ReadInventory(rc, a.ds.Name(TableInventory))
	if err != nil {
		rep.Notes = append(rep.Notes, "inventory table: "+err.Error())
		return nil
	}
	for _, s := range servers {
		if s.SID != a.sid {
			continue
		}
		rep.Notes = append(rep.Notes, fmt.Sprintf("inventory: %d DIMMs of DRAM model %s, server manufacturer %s (codes of the dataset; no serial numbers)",
			s.DIMMs, s.DRAMModel, s.Manufacturer))
		return sink.Host(&model.Host{SystemVendor: s.Manufacturer, FirstSeen: base, LastSeen: base})
	}
	rep.Notes = append(rep.Notes, "the inventory table does not list the server")
	return nil
}

// tickets notes the failure tickets of the server. A ticket is what
// happened to the server, not a record the server wrote, so it is not
// stored as an error.
func (a *Adapter) tickets(rep *adapter.Report) {
	if !a.ds.Has(TableTickets) {
		return
	}
	rc, err := a.ds.OpenTable(TableTickets)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			rep.Notes = append(rep.Notes, "ticket table: "+err.Error())
		}
		return
	}
	defer rc.Close()
	tickets, _, err := ReadTickets(rc, a.ds.Name(TableTickets))
	if err != nil {
		rep.Notes = append(rep.Notes, "ticket table: "+err.Error())
		return
	}
	for _, t := range tickets {
		if t.SID == a.sid {
			rep.Notes = append(rep.Notes, fmt.Sprintf("failure ticket at %s on the dataset calendar (failure_type %d, %s); it is not stored as an error record",
				t.At, int(t.Type), t.Type))
		}
	}
}
