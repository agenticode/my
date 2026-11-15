package plugin

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/fmfmlog"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// RequestOptions say what a request holds.
type RequestOptions struct {
	// Tool and Version name the program that asks.
	Tool, Version string
	// Now is the time of the request; zero means the current time.
	Now time.Time
	// PreviousLogs is how many records before the newest one are sent per
	// DIMM; zero means DefaultPreviousLogs.
	PreviousLogs int
}

// timeLayout is how times are written: UTC with milliseconds.
const timeLayout = "2006-01-02T15:04:05.000Z07:00"

// BuildRequest writes the request for the DIMMs of a ledger that have
// error records: for each the newest record and the records before it.
// An analyzer keeps nothing between two runs; the ledger is its memory.
//
// Errors that are tied to no DIMM are not sent: there is nothing to
// analyse them for.
func BuildRequest(ctx context.Context, s *store.Store, opts RequestOptions) (*Request, error) {
	n := opts.PreviousLogs
	if n <= 0 {
		n = DefaultPreviousLogs
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	req := &Request{
		Contract: Contract, Core: Core{Name: opts.Tool, Version: opts.Version, EngineVersion: engine.Version},
		RequestDT: now.UTC().Format(timeLayout), PreviousLogs: n, Dimms: []RequestDIMM{},
	}

	type logs struct {
		records    []fmfmlog.Runtime
		earlier    int
		ce, ue     int64
		host, slot string
	}
	by := map[string]*logs{}
	var order []string
	for e, err := range s.Events(ctx, store.EventFilter{}) {
		if err != nil {
			return nil, fmt.Errorf("analyzer request: %w", err)
		}
		errs := e.Errors()
		if errs <= 0 || e.DIMMUID == "" {
			continue
		}
		l := by[e.DIMMUID]
		if l == nil {
			l = &logs{}
			by[e.DIMMUID] = l
			order = append(order, e.DIMMUID)
		}
		if e.ErrorSeverity.Uncorrected() {
			l.ue += int64(errs)
		} else {
			l.ce += int64(errs)
		}
		// The place of the newest record is the place of the DIMM.
		l.host, l.slot = e.Host, e.Slot
		r := fmfmlog.RuntimeOf(e)
		r.Count = 0
		if errs != 1 {
			r.Count = errs
		}
		l.records = append(l.records, r)
		// Keep the newest n+1; trim in steps, not on every record.
		if len(l.records) > 2*(n+1) {
			drop := len(l.records) - (n + 1)
			l.earlier += drop
			l.records = append(l.records[:0], l.records[drop:]...)
		}
	}
	if len(by) == 0 {
		return req, nil
	}

	dimms, err := s.DIMMs(ctx)
	if err != nil {
		return nil, fmt.Errorf("analyzer request: DIMMs: %w", err)
	}
	identity := map[string]*model.DIMM{}
	for i := range dimms {
		identity[dimms[i].UID] = &dimms[i]
	}
	slots := map[string]map[string]model.Slot{}
	sort.Strings(order)
	for _, uid := range order {
		l := by[uid]
		if drop := len(l.records) - (n + 1); drop > 0 {
			l.earlier += drop
			l.records = l.records[drop:]
		}
		d := RequestDIMM{
			DimmId: uid, Location: fmfmlog.Location{HostID: l.host, Slot: l.slot}, Corrected: l.ce, Uncorrected: l.ue,
			NewLog: l.records[len(l.records)-1], PreviousLogs: l.records[:len(l.records)-1], EarlierLogs: l.earlier,
		}
		if d.PreviousLogs == nil {
			d.PreviousLogs = []fmfmlog.Runtime{}
		}
		if id := identity[uid]; id != nil {
			if id.Identity == model.IdentitySerial {
				d.DimmSerialNumber = id.Serial
			}
			d.Manufacturer, d.PartNumber, d.MemoryType, d.ModuleType, d.SizeBytes = id.Manufacturer, id.PartNumber, id.MemoryType, id.ModuleType, id.SizeBytes
			if id.Ranks.Valid {
				v := id.Ranks.V
				d.Ranks = &v
			}
			if id.DeviceWidth.Valid {
				v := id.DeviceWidth.V
				d.DeviceWidth = &v
			}
		}
		if _, ok := slots[l.host]; !ok {
			list, err := s.Slots(ctx, l.host)
			if err != nil {
				return nil, fmt.Errorf("analyzer request: slots of %s: %w", l.host, err)
			}
			m := map[string]model.Slot{}
			for _, sl := range list {
				m[sl.Name] = sl
			}
			slots[l.host] = m
		}
		if sl, ok := slots[l.host][l.slot]; ok {
			for _, f := range []struct {
				v   model.Opt[int]
				dst **int
			}{{sl.Socket, &d.Location.SocketId}, {sl.Channel, &d.Location.ChannelId}, {sl.Position, &d.Location.ChannelSlotId}} {
				if f.v.Valid {
					v := f.v.V
					*f.dst = &v
				}
			}
		}
		req.Dimms = append(req.Dimms, d)
	}
	return req, nil
}
