// Package eventstream writes the records of a host as a stream of JSON
// lines in one schema, my-events/1, for a collector that is not MemorY: a
// log pipeline, a ticket system, a database. Every event and action of the
// host is one line, oldest first, the records of the host's own logs and
// those of its BIOS and BMC alike; a firmware record that made neither is
// a line of its own. Duplicates are in the stream with dup_of set, so a
// consumer counts by the same rule as the ledger.
//
// The stream goes to a writer the caller opens; this package sends
// nothing anywhere.
package eventstream

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// Schema names the form of a line.
const Schema = "my-events/1"

// Kinds of record a line stands for.
const (
	RecordEvent    = "event"
	RecordAction   = "action"
	RecordFirmware = "firmware"
)

// OriginOS is the origin of a record of the host's own logs.
const OriginOS = "os"

// Options select what a stream holds.
type Options struct {
	// Since leaves out records before it. Records whose time is relative
	// to an unknown boot have no date and are left out too.
	Since time.Time
	// Raw adds the raw text of each record.
	Raw bool
}

// Line is one record. Fields that are zero, false or empty are left out.
type Line struct {
	Schema      string `json:"schema"`
	Host        string `json:"host"`
	Time        string `json:"time,omitzero"`
	TimeQuality string `json:"time_quality"`
	// UptimeSeconds is the time of a record that only knows the seconds
	// since its boot.
	UptimeSeconds int64    `json:"uptime_seconds,omitzero"`
	Record        string   `json:"record"`
	Event         string   `json:"event"`
	Flags         []string `json:"flags,omitzero"`
	Severity      string   `json:"severity,omitzero"`
	Count         int      `json:"count,omitzero"`
	Action        string   `json:"action,omitzero"`
	Result        string   `json:"result,omitzero"`
	Text          string   `json:"text,omitzero"`
	Slot          string   `json:"slot,omitzero"`
	Location      string   `json:"location,omitzero"`
	DIMM          *DIMM    `json:"dimm,omitzero"`
	Place         *Place   `json:"place,omitzero"`
	Origin        string   `json:"origin"`
	Source        string   `json:"source"`
	Log           string   `json:"log,omitzero"`
	Vendor        *Vendor  `json:"vendor,omitzero"`
	Mapping       *Mapping `json:"mapping,omitzero"`
	Fingerprint   string   `json:"fingerprint"`
	DupOf         string   `json:"dup_of,omitzero"`
	WithdrawnBy   string   `json:"withdrawn_by,omitzero"`
	BootID        string   `json:"boot_id,omitzero"`
	RawRef        string   `json:"raw_ref,omitzero"`
	Raw           string   `json:"raw,omitzero"`
}

// DIMM is the module a record is tied to.
type DIMM struct {
	Serial       string `json:"serial,omitzero"`
	Part         string `json:"part,omitzero"`
	Manufacturer string `json:"manufacturer,omitzero"`
}

// Place is where in the memory a record puts its error or step.
type Place struct {
	Socket      *int    `json:"socket,omitzero"`
	Channel     *int    `json:"channel,omitzero"`
	ChannelSlot *int    `json:"channel_slot,omitzero"`
	Rank        *int    `json:"rank,omitzero"`
	Device      *int    `json:"device,omitzero"`
	BankGroup   *int    `json:"bank_group,omitzero"`
	Bank        *int    `json:"bank,omitzero"`
	Row         *int    `json:"row,omitzero"`
	Column      *int    `json:"column,omitzero"`
	Address     *uint64 `json:"address,omitzero"`
}

// Vendor is what the BIOS or the BMC said, in its own terms.
type Vendor struct {
	Name            string   `json:"name,omitzero"`
	ID              string   `json:"id,omitzero"`
	Registry        string   `json:"registry,omitzero"`
	RegistryVersion string   `json:"registry_version,omitzero"`
	Severity        string   `json:"severity,omitzero"`
	Message         string   `json:"message,omitzero"`
	Args            []string `json:"args,omitzero"`
}

// Mapping says how the common event of a firmware record was found.
type Mapping struct {
	By    string `json:"by"`
	Ref   string `json:"ref,omitzero"`
	Basis string `json:"basis,omitzero"`
}

// Write writes the records of a host to w and returns how many lines it
// wrote.
func Write(ctx context.Context, w io.Writer, s *store.Store, host string, opts Options) (int, error) {
	// Everything but the events is read first: the events are read as a
	// stream, and the ledger serves one query at a time.
	dimms := map[string]*DIMM{}
	list, err := s.DIMMs(ctx)
	if err != nil {
		return 0, err
	}
	for _, d := range list {
		if d.Serial != "" || d.PartNumber != "" || d.Manufacturer != "" {
			dimms[d.UID] = &DIMM{Serial: d.Serial, Part: d.PartNumber, Manufacturer: d.Manufacturer}
		}
	}
	fws, err := s.FirmwareRecords(ctx, store.FirmwareFilter{Host: host})
	if err != nil {
		return 0, err
	}
	byEvent, byAction := map[int64]*model.FirmwareRecord{}, map[int64]*model.FirmwareRecord{}
	var alone []*model.FirmwareRecord
	for i := range fws {
		f := &fws[i]
		switch {
		case f.EventID != 0:
			byEvent[f.EventID] = f
		case f.ActionID != 0:
			byAction[f.ActionID] = f
		case opts.Since.IsZero() || !f.At.Before(opts.Since):
			alone = append(alone, f)
		}
	}
	sort.SliceStable(alone, func(i, j int) bool { return alone[i].At.Before(alone[j].At) })
	acts, err := s.Actions(ctx, store.ActionFilter{Host: host, From: opts.Since, Duplicates: true})
	if err != nil {
		return 0, err
	}
	links, err := s.Links(ctx, host)
	if err != nil {
		return 0, err
	}

	bw := bufio.NewWriter(w)
	enc := json.NewEncoder(bw)
	enc.SetEscapeHTML(false)
	n := 0
	put := func(l *Line) error {
		if n%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		n++
		return enc.Encode(l)
	}
	// The three lists are merged by time: actions and firmware records
	// alone go before the first event that is later than they are.
	ai, fi := 0, 0
	before := func(t time.Time, quality model.TimeQuality) error {
		for {
			var next *Line
			switch {
			case ai < len(acts) && (fi >= len(alone) || !alone[fi].At.Before(acts[ai].At)) && earlier(acts[ai].At, acts[ai].TimeQuality, t, quality):
				a := &acts[ai]
				next = ActionLine(host, a, byAction[a.ID], dimms[a.DIMMUID], links, opts.Raw)
				ai++
			case fi < len(alone) && earlier(alone[fi].At, model.TimeExact, t, quality):
				next = FirmwareLine(host, alone[fi], opts.Raw)
				fi++
			default:
				return nil
			}
			if next != nil {
				if err := put(next); err != nil {
					return err
				}
			}
		}
	}
	f := store.EventFilter{Host: host, From: opts.Since, WithDuplicates: true, WithRaw: opts.Raw}
	for e, err := range s.Events(ctx, f) {
		if err != nil {
			return n, err
		}
		if !opts.Since.IsZero() && e.TimeQuality == model.TimeRelative {
			continue
		}
		if err := before(e.ErrorDT, e.TimeQuality); err != nil {
			return n, err
		}
		if err := put(EventLine(host, e, byEvent[e.ID], dimms[e.DIMMUID], links, opts.Raw)); err != nil {
			return n, err
		}
	}
	if err := before(time.Time{}, ""); err != nil {
		return n, err
	}
	return n, bw.Flush()
}

// earlier says whether a record at t goes before one at limit. A zero
// limit lets every record go. Relative times count from the start of the
// epoch; they come first, as the ledger orders them.
func earlier(t time.Time, q model.TimeQuality, limit time.Time, lq model.TimeQuality) bool {
	if limit.IsZero() && lq == "" {
		return true
	}
	return !t.After(limit)
}

// EventLine makes the line of an event.
func EventLine(host string, e *model.Event, fw *model.FirmwareRecord, d *DIMM, links store.RecordLinks, raw bool) *Line {
	l := &Line{Schema: Schema, Host: host, Record: RecordEvent, Severity: string(e.ErrorSeverity), Count: e.Count, Text: e.ErrorType,
		Slot: e.Slot, Location: e.Label, DIMM: d, Source: e.Source, Fingerprint: e.Fingerprint, DupOf: links.EventDup[e.ID],
		BootID: e.BootID, RawRef: e.RawRef}
	setTime(l, e.ErrorDT, e.TimeQuality)
	if l.Location == "" {
		l.Location = e.EdacLocation
	}
	l.Event = hostEvent(e)
	p := Place{Socket: opt(e.SocketId), Channel: opt(e.ChannelId), ChannelSlot: opt(e.ChannelSlotId), Rank: opt(e.Rank),
		Device: opt(e.DeviceId), BankGroup: opt(e.BankGroup), Bank: opt(e.Bank), Row: opt(e.Row), Column: opt(e.Column),
		Address: opt(e.OsPhysicalAddress)}
	if p != (Place{}) {
		l.Place = &p
	}
	if raw {
		l.Raw = e.Raw
	}
	firmware(l, fw, raw)
	if l.Origin == "" {
		l.Origin = originOf(e.Source)
	}
	return l
}

// ActionLine makes the line of an action.
func ActionLine(host string, a *model.Action, fw *model.FirmwareRecord, d *DIMM, links store.RecordLinks, raw bool) *Line {
	l := &Line{Schema: Schema, Host: host, Record: RecordAction, Event: string(a.Kind), Count: a.Count, Action: string(a.Kind),
		Result: string(a.Result), Text: a.Detail, Slot: a.Slot, Location: a.Label, DIMM: d, Source: a.Source,
		Fingerprint: a.Fingerprint, DupOf: links.ActionDup[a.ID], WithdrawnBy: links.Withdrawn[a.ID], BootID: a.BootID, RawRef: a.RawRef}
	setTime(l, a.At, a.TimeQuality)
	p := Place{Rank: opt(a.Rank), Device: opt(a.DeviceId), BankGroup: opt(a.BankGroup), Bank: opt(a.Bank), Row: opt(a.Row),
		Address: opt(a.OsPhysicalAddress)}
	if p != (Place{}) {
		l.Place = &p
	}
	if raw {
		l.Raw = a.Raw
	}
	firmware(l, fw, raw)
	if l.Origin == "" {
		l.Origin = originOf(a.Source)
	}
	return l
}

// FirmwareLine makes the line of a firmware record that made neither an
// event nor an action: a module seen, a mode in effect, a log cleared.
func FirmwareLine(host string, f *model.FirmwareRecord, raw bool) *Line {
	l := &Line{Schema: Schema, Host: host, Record: RecordFirmware, Slot: f.Slot, Source: f.Source, Fingerprint: f.Fingerprint,
		RawRef: f.RawRef}
	setTime(l, f.At, model.TimeExact)
	firmware(l, f, raw)
	if l.Origin == "" {
		l.Origin = originOf(f.Source)
	}
	return l
}

// firmware adds to a line what the vendor said: the common event takes the
// place of the one the ledger record gives.
func firmware(l *Line, f *model.FirmwareRecord, raw bool) {
	if f == nil {
		return
	}
	if f.Common != "" {
		l.Event = f.Common
	}
	l.Flags = f.Flags
	l.Origin, l.Log = f.Origin, f.Log
	if f.Location != "" {
		l.Location = f.Location
	}
	v := Vendor{Name: f.Vendor, ID: f.VendorID, Registry: f.Registry, RegistryVersion: f.RegistryVersion, Severity: f.VendorSeverity,
		Message: f.Text, Args: f.Args}
	if v.Name != "" || v.ID != "" || v.Registry != "" || v.Message != "" || v.Severity != "" || len(v.Args) > 0 {
		l.Vendor = &v
	}
	if f.Mapping != "" {
		l.Mapping = &Mapping{By: f.Mapping, Ref: f.MapRef, Basis: f.Basis}
	}
	if raw && l.Raw == "" {
		l.Raw = f.Raw
	}
}

// hostEvent names the event of a record of the ledger by its kind,
// severity and type: ce, ue, scrub-ce, scrub-ue, counter, stop, panic,
// informational.
func hostEvent(e *model.Event) string {
	switch {
	case e.Kind == model.KindCounter:
		return "counter"
	case e.ErrorType == model.ErrorTypeStop:
		return "stop"
	case e.ErrorType == model.ErrorTypePanic && e.ErrorSeverity == model.SevInformational:
		return "panic"
	}
	scrub := ""
	if e.ErrorScrubType == model.ScrubPatrol {
		scrub = "scrub-"
	}
	switch e.ErrorSeverity.Class() {
	case "CE":
		return scrub + "ce"
	case "UE":
		return scrub + "ue"
	}
	return "informational"
}

// originOf names where a record without a firmware record comes from: the
// logs of a BMC, or those of the host.
func originOf(source string) string {
	kind, _, _ := strings.Cut(source, "/")
	switch kind {
	case "redfish", "sel", "vendorlog":
		return model.OriginBMCLog
	}
	return OriginOS
}

// setTime sets the time of a line: a date, or the seconds since boot of a
// record that knows no date.
func setTime(l *Line, t time.Time, q model.TimeQuality) {
	if q == "" {
		q = model.TimeExact
	}
	l.TimeQuality = string(q)
	if q == model.TimeRelative {
		l.UptimeSeconds = t.Unix()
		return
	}
	if !t.IsZero() {
		l.Time = t.UTC().Format(time.RFC3339Nano)
	}
}

func opt[T any](o model.Opt[T]) *T {
	if !o.Valid {
		return nil
	}
	v := o.V
	return &v
}
