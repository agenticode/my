package redfish

import (
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/decode/ipmi"
	"github.com/agenticode/my/internal/fwevent"
	"github.com/agenticode/my/internal/fwmap"
	"github.com/agenticode/my/internal/model"
)

// A log entry says what happened in one of the vocabularies of package
// fwmap: the event bytes of an entry of type SEL (in MessageId, with or
// without 0x) or of a message that carries a SEL record, the name of a
// state of the IPMI memory sensor in the text of such an entry, a message
// of a registry a table maps (the DMTF standard, OpenBMC under any prefix,
// a vendor's), or words the wording rules know. Every entry about memory
// is read as a common event of package fwevent, and fwevent says what it
// is in the ledger, so an event reads the same whatever vocabulary it came
// in. An entry that is about memory and fits nothing is kept as
// information and named in the report.

var (
	selDataRE = regexp.MustCompile(`^(?:0[xX])?[0-9a-fA-F]{8}$`)
	// "(Processor 1, Memory Module 4)" in the message of a vendor log.
	procModuleRE = regexp.MustCompile(`\(Processor (\d{1,2}), Memory Module (\d{1,3})\)`)
	locationRE   = regexp.MustCompile(`(?i)\bat location(?:\(s\))? ([^.;]+)`)
	hexDigitsRE  = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)
)

// crossLogWindow is how far apart the entries of two logs of one BMC may
// be stamped and still report one error. The firmware writes both while it
// handles the error.
const crossLogWindow = 5 * time.Second

// decoded is what a log entry says once its vocabulary is read.
type decoded struct {
	res fwmap.Result
	// state is the IPMI state name, for an entry read through the IPMI
	// memory sensor.
	state    string
	deassert bool
	decoder  string
	// labels are the modules the entry names.
	labels    []string
	place     fwmap.Place
	generator string
	text      string // the entry's own words
}

// parseTime reads the time of a log entry. A time without a zone is taken
// as UTC.
func parseTime(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			if y := t.UTC().Year(); y < 1970 || y > 9999 {
				return time.Time{}, false
			}
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// originOf returns the URI of the resource a log entry is about.
func originOf(o object) string {
	if links := child(o, "Links"); links != nil {
		if oc := child(links, "OriginOfCondition"); oc != nil {
			return strings.TrimRight(str(oc, "@odata.id"), "/")
		}
	}
	if oc := child(o, "OriginOfCondition"); oc != nil {
		return strings.TrimRight(str(oc, "@odata.id"), "/")
	}
	return ""
}

// memoryOrigin reports whether a URI names a memory resource, and the name
// of its slot: the device locator when the dump holds the resource, and
// the last segment of the URI otherwise.
func (r *run) memoryOrigin(uri string) (label string, ok bool) {
	if uri == "" {
		return "", false
	}
	if l, known := r.locators[uri]; known {
		return l, true
	}
	i := strings.LastIndex(uri, "/Memory/")
	if i < 0 || strings.Contains(uri[i+len("/Memory/"):], "/") {
		return "", false
	}
	return lastSegment(uri), true
}

// args returns the message arguments of an entry as text, at most
// adapter.MaxArgs of them.
func args(o object) []string {
	list, _ := o["MessageArgs"].([]any)
	if len(list) > adapter.MaxArgs {
		list = list[:adapter.MaxArgs]
	}
	out := make([]string, 0, len(list))
	for _, a := range list {
		out = append(out, strings.TrimSpace(fmt.Sprint(a)))
	}
	return out
}

// ipmiState returns what a state of the IPMI memory sensor means.
func ipmiState(ev ipmi.MemoryEvent) fwmap.Result {
	if ev == ipmi.MemUnknown {
		return fwmap.Result{Event: fwevent.Unclassified, Mapping: model.MappingNone, Memory: true}
	}
	res := fwmap.Current().Classify(fwmap.Entry{IPMI: fmt.Sprintf("memory-%02x", int(ev))})
	if !res.Event.Valid() {
		return fwmap.Result{Event: fwevent.Unclassified, Mapping: model.MappingNone, Memory: true}
	}
	return res
}

// decode reads what a log entry says. memory reports whether the entry is
// about memory at all.
func (r *run) decode(o object) (d decoded, memory bool) {
	entryType, sensorType := str(o, "EntryType"), str(o, "SensorType")
	msgID, msg := str(o, "MessageId"), str(o, "Message")
	d.text = msg
	origin, originIsMemory := r.memoryOrigin(originOf(o))
	if originIsMemory {
		d.labels = []string{origin}
	}
	if code := str(o, "EntryCode"); strings.HasPrefix(strings.ToLower(code), "deassert") {
		d.deassert = true
	}

	// A record of the IPMI memory sensor: an entry of type SEL, or a
	// message that carries the bytes of a SEL record.
	sel := func(rec ipmi.Record) {
		ev := ipmi.MemoryEventOf(rec)
		d.deassert = d.deassert || rec.Deassert
		if id := rec.ModuleID(); id.Valid && len(d.labels) == 0 {
			d.labels = []string{fmt.Sprintf("sensor 0x%02x module %d", rec.SensorNumber, id.V)}
		}
		d.res, d.decoder = ipmiState(ev), "redfish/sel"
		if ev != ipmi.MemUnknown {
			d.state = ev.String()
		}
	}
	if _, _, key := fwmap.SplitMessageID(msgID); key == "SELEntryAdded" {
		a := args(o)
		if len(a) != 1 {
			return d, false
		}
		hexText := strings.Join(strings.Fields(strings.TrimPrefix(strings.TrimPrefix(a[0], "0x"), "0X")), "")
		if !hexDigitsRE.MatchString(hexText) {
			return d, false
		}
		b, _ := hex.DecodeString(hexText)
		rec, err := ipmi.ParseRecord(b)
		if err != nil || !rec.SystemEvent() || rec.SensorType != ipmi.SensorMemory {
			return d, false
		}
		sel(rec)
		return d, true
	}

	switch {
	case strings.EqualFold(entryType, "SEL") || sensorType != "":
		if !strings.EqualFold(sensorType, "Memory") {
			return d, false
		}
		d.decoder = "redfish/sel"
		if g := str(o, "GeneratorId"); g != "" {
			var id uint16
			if _, err := fmt.Sscanf(strings.ToLower(g), "0x%x", &id); err == nil {
				d.generator = ipmi.Generator(id)
			}
		}
		read := false
		if selDataRE.MatchString(msgID) {
			if et, deassert, data, err := ipmi.ParseEventData(msgID); err == nil {
				rec := ipmi.Record{Type: ipmi.RecordSystemEvent, SensorType: ipmi.SensorMemory, EventType: et, Deassert: deassert, Data: data}
				if n, ok := integer(o, "SensorNumber"); ok && n >= 0 && n <= 0xFF {
					rec.SensorNumber = uint8(n)
				}
				if ipmi.MemoryEventOf(rec) != ipmi.MemUnknown {
					generator := d.generator
					sel(rec)
					d.generator = generator
					read = true
				}
			}
		}
		if !read {
			ev, _ := ipmi.MemoryEventFromText(msg)
			if ev == ipmi.MemUnknown {
				ev, _ = ipmi.MemoryEventInText(msg)
			}
			d.res = ipmiState(ev)
			if ev != ipmi.MemUnknown {
				d.state = ev.String()
			}
		}
		if strings.Contains(strings.ToLower(msg), "deassert") {
			d.deassert = true
		}
		// The module in the text wins over a module number: it is a name.
		if names := ipmi.ModuleNames(msg); len(names) > 0 && !originIsMemory {
			d.labels = names
		}
		return d, true
	}

	d.res = fwmap.Current().Classify(fwmap.Entry{MessageID: msgID, Message: msg, Args: args(o), MemoryOrigin: originIsMemory})
	if d.res.Skipped || !d.res.Memory {
		return d, false
	}
	form := "plain"
	switch {
	case d.res.Table != nil && d.res.Table.Name == "openbmc":
		d.decoder = "redfish/openbmc"
	case d.res.Table != nil:
		d.decoder = "redfish/registry"
	case d.res.Mapping == model.MappingWording || d.res.Mapping == model.MappingRegistry:
		d.decoder = "redfish/text"
	}
	if d.res.Table != nil {
		form = d.res.Table.Location
	}
	a := args(o)
	d.place = fwmap.PlaceOf(form, d.res.Roles, a)
	if originIsMemory {
		return d, true
	}
	d.labels = d.place.Labels
	if len(d.labels) == 0 && d.res.Roles == nil && d.res.Table != nil {
		// A message of a table without the roles of its arguments: the
		// arguments that are names of slots.
		for _, x := range a {
			d.labels = append(d.labels, fwmap.SplitLocations(x)...)
		}
	}
	if len(d.labels) == 0 && !d.place.Socket.Valid {
		if d.labels = textLocations(msg); len(d.labels) > 0 && d.decoder == "" {
			d.decoder = "redfish/text"
		}
	}
	return d, true
}

// textLocations returns the modules a message names in its text: after
// "at location", by a DIMM name, or as "(Processor 1, Memory Module 4)".
func textLocations(msg string) []string {
	if m := locationRE.FindStringSubmatch(msg); m != nil {
		if out := fwmap.SplitLocations(m[1]); len(out) > 0 {
			return out
		}
	}
	if names := ipmi.ModuleNames(msg); len(names) > 0 {
		return names
	}
	if pm := procModuleRE.FindStringSubmatch(msg); pm != nil {
		// The boards that write this text name the slot "PROC 1 DIMM 4".
		return []string{fmt.Sprintf("PROC %s DIMM %s", pm[1], pm[2])}
	}
	return nil
}

// logs turns the log entries into events and actions.
func (r *run) logs(sink adapter.Sink) error {
	type out struct {
		ev    *model.Event
		act   *model.Action
		alone *model.FirmwareRecord
		// log is the log the entry stands in.
		log string
	}
	var outs []out
	var notMemory, cxl, cleared, presence, withdrawn, multi, unnamed int
	unclassified := map[string]int{}
	var logLimit []time.Time

	for i := range r.entries {
		if i%1024 == 0 {
			if err := r.ctx.Err(); err != nil {
				return stopped{err}
			}
		}
		if i == adapter.MaxRecords {
			// The rest is counted and left out; the dump is read in part.
			r.tally.Skip(r.entries[i].ref, fmt.Sprintf("%d log entries after the first %d were not read", len(r.entries)-i, adapter.MaxRecords))
			break
		}
		res := &r.entries[i]
		if err := adapter.Each(&r.tally, res.ref, str(res.obj, "MessageId")+" "+str(res.obj, "Message"), func() error {
			o := res.obj
			// Every entry counts for the period its log covers.
			r.cover(res)
			if strings.EqualFold(str(o, "EntryType"), "CXL") {
				cxl++
				return nil
			}
			d, memory := r.decode(o)
			if !memory {
				notMemory++
				return nil
			}
			r.coverMemory(res)
			// A place given by numbers is the slot the same dump describes
			// with those numbers.
			p := d.place
			if len(d.labels) == 0 && p.Socket.Valid && p.Channel.Valid && p.Position.Valid {
				if name, ok := r.places[[3]int{p.Socket.V, p.Channel.V, p.Position.V}]; ok {
					d.labels = []string{name}
				}
			}
			created, at, ok := entryTime(o)
			if !ok {
				r.tally.Unread(res.ref, "log entry about memory has no readable time", raw(o))
				return nil
			}
			msgID := str(o, "MessageId")
			fp := model.Fingerprint(Kind, "log", res.id, created, msgID, str(o, "Message"))
			rawText := raw(o)

			ev, clears := d.res.Event, d.res.Clears
			if d.deassert && ev != fwevent.Cleared && ev != fwevent.Unclassified {
				if ev == fwevent.Disabled {
					// The module is in use again: a disable this dump
					// recorded before for the same module no longer holds.
					for j := len(outs) - 1; j >= 0; j-- {
						a := outs[j].act
						if a == nil || a.Kind != model.ActionDisable || len(d.labels) != 1 || model.NormalizeLabel(a.Label) != model.NormalizeLabel(d.labels[0]) {
							continue
						}
						was := &model.Event{Kind: model.KindError, ErrorDT: a.At, TimeQuality: model.TimeExact, ErrorSeverity: model.SevInformational,
							ErrorType: d.state + " (enabled again later)", Count: 1, Label: a.Label, Source: Kind + "/log", Decoder: d.decoder,
							RawRef: a.RawRef, Raw: a.Raw, Fingerprint: a.Fingerprint, Firmware: a.Firmware}
						outs[j] = out{ev: was}
						withdrawn++
					}
				}
				ev, clears = fwevent.Cleared, ev
			}
			text := fwevent.Text(ev, clears)
			if d.res.Line != nil && d.res.Line.Words != "" && ev == d.res.Event {
				text = d.res.Line.Words
			}
			switch {
			case d.state != "" && ev == fwevent.Cleared && clears == fwevent.Disabled:
				text = d.state + " (deasserted: in use again)"
			case d.state != "" && ev != fwevent.Cleared:
				text = d.state
			}
			switch ev {
			case fwevent.Presence:
				presence++
			case fwevent.Cleared:
				cleared++
			case fwevent.Unclassified:
				// About memory, in words this reader has no class for.
				key := msgID
				if key == "" || selDataRE.MatchString(key) {
					key = d.text
					if len(key) > 60 {
						key = key[:60] + "..."
					}
				}
				unclassified[key]++
				text = d.text
				if text == "" {
					text = msgID
				}
				if text == "" {
					r.tally.Unread(res.ref, "log entry about memory has no message", rawText)
					return nil
				}
				if len(text) > 200 {
					text = text[:200] + "..."
				}
			}
			method := d.generator
			if method == "" && d.decoder != "redfish/sel" {
				method = "bmc" // the BMC's own log
			}
			registry, version, key := fwmap.SplitMessageID(msgID)
			if d.decoder == "redfish/sel" {
				registry, version = "", ""
			}
			location := p.Location
			if location == "" && len(d.labels) > 0 {
				location = strings.Join(d.labels, ", ")
			}
			severity := str(o, "Severity")
			if severity == "" {
				severity = d.res.Severity
			}
			rec := &fwevent.Record{Event: ev, Flags: d.res.Flags, Clears: clears, At: at, Count: p.Count, Labels: d.labels,
				SocketId: p.Socket, ChannelId: p.Channel, ChannelSlotId: p.Position, Rank: p.Rank, DeviceId: p.Device, Bank: p.Bank,
				Row: p.Row, Column: p.Column, Address: p.Address, Decoder: d.decoder, Method: method, Text: text,
				Source: Kind + "/log", RawRef: res.ref, Raw: rawText, Fingerprint: fp,
				Firmware: model.FirmwareRecord{Vendor: d.res.Vendor, VendorID: key, Registry: registry, RegistryVersion: version,
					VendorSeverity: severity, Origin: model.OriginBMCLog, Log: lastSegment(logPath(res)), Location: location,
					Args: p.Args, Text: d.text, EntryKey: model.Fingerprint(logPath(res), res.id, created), Mapping: d.res.Mapping,
					MapRef: d.res.Ref, Basis: d.res.Basis}}
			lo, err := rec.Ledger()
			if err != nil {
				return adapter.Unreadable("the entry could not be read as a firmware record: %v", err)
			}
			for _, e := range lo.Events {
				named := e.Label != "" || (e.SocketId.Valid && e.ChannelId.Valid && e.ChannelSlotId.Valid)
				switch {
				case len(d.labels) > 1 && e.ErrorSeverity.Class() != "":
					multi++
				case !named && e.ErrorSeverity.Class() != "":
					unnamed++
				}
				if ev == fwevent.CELogLimit {
					logLimit = append(logLimit, at)
				}
				o := out{ev: e}
				if e.ErrorSeverity.Class() != "" {
					o.log = logPath(res)
				}
				outs = append(outs, o)
			}
			if len(lo.Actions) > 0 && len(d.labels) == 0 {
				unnamed++
			}
			for _, a := range lo.Actions {
				outs = append(outs, out{act: a})
			}
			if lo.Alone != nil {
				outs = append(outs, out{alone: lo.Alone})
			}
			return nil
		}); err != nil {
			return err
		}
	}

	// A BMC can keep one error in two of its logs: the system event log and
	// the log of the manager. The entry of the second log is declared a
	// second report of the first, so the error is counted once. Two
	// entries are one error when they stand in different logs, name the
	// same module, are of the same severity and lie within crossLogWindow
	// of each other; an entry stands for at most one entry of each other
	// log.
	type errorKey struct {
		where    string
		severity model.Severity
	}
	type first struct {
		at   time.Time
		fp   string
		logs map[string]bool
	}
	firsts := map[errorKey][]*first{}
	twice := 0
	for _, o := range outs {
		e := o.ev
		if e == nil || o.log == "" || e.ErrorSeverity.Class() == "" {
			continue
		}
		k := errorKey{severity: e.ErrorSeverity}
		switch {
		case e.Label != "":
			k.where = "label:" + model.NormalizeLabel(e.Label)
		case e.SocketId.Valid && e.ChannelId.Valid && e.ChannelSlotId.Valid:
			k.where = fmt.Sprintf("place:%d:%d:%d", e.SocketId.V, e.ChannelId.V, e.ChannelSlotId.V)
		default:
			continue // names no module: nothing says it is the same error
		}
		var same *first
		for _, f := range firsts[k] {
			if !f.logs[o.log] && e.ErrorDT.Sub(f.at).Abs() <= crossLogWindow {
				same = f
				break
			}
		}
		if same == nil {
			firsts[k] = append(firsts[k], &first{at: e.ErrorDT, fp: e.Fingerprint, logs: map[string]bool{o.log: true}})
			continue
		}
		same.logs[o.log] = true
		e.DupOfFingerprint = same.fp
		twice++
	}

	for _, o := range outs {
		var err error
		switch {
		case o.ev != nil:
			o.ev.Grade = o.ev.ComputeGrade()
			err = sink.Event(o.ev)
		case o.act != nil:
			err = sink.Action(o.act)
		case o.alone != nil:
			err = sink.Firmware(o.alone)
		}
		if err != nil {
			return err
		}
	}

	add := func(format string, a ...any) { r.rep.Notes = append(r.rep.Notes, fmt.Sprintf(format, a...)) }
	if n := len(r.entries); n > 0 {
		add("%d log entries read", n)
	}
	if notMemory > 0 {
		add("%d log entries are not about memory and were left out", notMemory)
	}
	if cxl > 0 {
		add("%d log entries of type CXL were not read: the event record in them is not decoded", cxl)
	}
	if presence > 0 {
		add("%d presence entries are kept apart and counted in no total", presence)
	}
	if cleared > 0 {
		add("%d entries only say that a state ended; they are kept as information", cleared)
	}
	if withdrawn > 0 {
		add("%d disable entries were followed by the module coming back into use; they are kept as information, not as a disabled module", withdrawn)
	}
	keys := make([]string, 0, len(unclassified))
	for k := range unclassified {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		if i == 8 {
			add("entries about memory with %d more message IDs this reader has no class for are kept as information", len(keys)-8)
			break
		}
		add("%d entries about memory say %q, which this reader has no class for; they are kept as information", unclassified[k], k)
	}
	if multi > 0 {
		add("%d error entries name more than one module; they are counted once and tied to none of them", multi)
	}
	if twice > 0 {
		add("%d error entries repeat an entry of another log of the same BMC: each such error is counted once", twice)
	}
	if unnamed > 0 {
		add("%d memory entries do not name a module", unnamed)
	}
	if n := len(logLimit); n > 0 {
		add("the firmware stopped logging corrected errors %d time(s), first on %s: corrected errors after that are missing from this log",
			n, logLimit[0].Format("2006-01-02 15:04 UTC"))
	}
	return nil
}
