package redfish

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/decode/ipmi"
	"github.com/agenticode/my/internal/model"
)

// A log entry says what happened in one of several vocabularies. The ones
// read here:
//
//   - entries of type SEL about a Memory sensor: the event bytes in
//     MessageId, as the schema defines for this entry type, or the text of
//     the IPMI state in Message;
//   - the OpenBMC message registry (OpenBMC.x.y.MemoryECCCorrectable and
//     its neighbours), whose arguments are socket, channel, DIMM and rank;
//   - message IDs of the form MEMnnnn, where MEM0000 to MEM0010 follow the
//     states of the IPMI memory sensor one by one;
//   - messages that spell out an IPMI memory state or one of two widely
//     used vendor texts about a memory module.
//
// Every state is mapped through the IPMI memory sensor vocabulary, so an
// event reads the same whether it came from a SEL dump or from Redfish.
// An entry that is about memory and fits none of these is counted and
// named in the report.

var (
	selDataRE = regexp.MustCompile(`^0[xX][0-9a-fA-F]{8}$`)
	openbmcRE = regexp.MustCompile(`^OpenBMC\.\d+\.\d+(?:\.\d+)?\.(\w+)$`)
	memIDRE   = regexp.MustCompile(`(?:^|\.)MEM(\d{4})$`)
	// "(Processor 1, Memory Module 4)" in the message of a vendor log.
	procModuleRE = regexp.MustCompile(`\(Processor (\d{1,2}), Memory Module (\d{1,3})\)`)
	locationRE   = regexp.MustCompile(`(?i)\bat location(?:\(s\))? ([^.;]+)`)
	memoryWordRE = regexp.MustCompile(`(?i)\b(?:memory|dimm\w*|ecc)\b`)
)

// openbmcEvents maps message names of the OpenBMC registry to states.
var openbmcEvents = map[string]ipmi.MemoryEvent{
	"MemoryECCCorrectable":          ipmi.MemCorrectable,
	"MemoryECCUncorrectable":        ipmi.MemUncorrectable,
	"MemoryParityCommandAndAddress": ipmi.MemParity,
	"MemoryParityNotKnown":          ipmi.MemParity,
	"SparingRedundancyDegraded":     ipmi.MemSpare,
	"MemoryThermTrip":               ipmi.MemOvertemp,
}

// openbmcCleared are messages that say a state ended.
var openbmcCleared = map[string]bool{"SparingRedundancyFull": true, "MirroringRedundancyFull": true}

// memIDEvents maps MEMnnnn message IDs to states.
var memIDEvents = map[string]ipmi.MemoryEvent{
	"0000": ipmi.MemCorrectable, "0001": ipmi.MemUncorrectable, "0002": ipmi.MemParity, "0003": ipmi.MemScrubFailed,
	"0004": ipmi.MemDeviceDisabled, "0005": ipmi.MemLogLimit, "0006": ipmi.MemPresence, "0007": ipmi.MemConfigError,
	"0008": ipmi.MemSpare, "0009": ipmi.MemThrottled, "0010": ipmi.MemOvertemp,
	// The corrected error rate of a module passed a limit.
	"0701": ipmi.MemCorrectable, "0702": ipmi.MemCorrectable,
}

// memIDCleared are MEMnnnn messages that say a module is fine again.
var memIDCleared = map[string]bool{"0016": true, "0700": true}

// decoded is what a log entry says once its vocabulary is read.
type decoded struct {
	ev       ipmi.MemoryEvent
	deassert bool
	decoder  string
	// labels are the modules the entry names.
	labels []string
	// Places by number, when the vocabulary has them.
	socket, channel, position, rank model.Opt[int]
	generator                       string
	text                            string // the state in the entry's own words, for states this reader has no class for
	// cleared is set for a message that says a state ended.
	cleared bool
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

// args returns the message arguments of an entry as text.
func args(o object) []string {
	list, _ := o["MessageArgs"].([]any)
	out := make([]string, 0, len(list))
	for _, a := range list {
		out = append(out, strings.TrimSpace(fmt.Sprint(a)))
	}
	return out
}

// channelIndex reads a channel given as a number or as a letter.
func channelIndex(s string) model.Opt[int] {
	if len(s) == 1 && s[0] >= 'A' && s[0] <= 'Z' {
		return model.Some(int(s[0] - 'A'))
	}
	if len(s) == 1 && s[0] >= 'a' && s[0] <= 'z' {
		return model.Some(int(s[0] - 'a'))
	}
	return smallInt(s)
}

func smallInt(s string) model.Opt[int] {
	n := 0
	if s == "" || len(s) > 4 {
		return model.Opt[int]{}
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return model.Opt[int]{}
		}
		n = n*10 + int(s[i]-'0')
	}
	return model.Some(n)
}

// decode reads what a log entry says. memory reports whether the entry is
// about memory at all; an entry that is, and that no vocabulary explains,
// comes back with the state MemUnknown.
func (r *run) decode(o object) (d decoded, memory bool) {
	d.ev = ipmi.MemUnknown
	entryType, sensorType := str(o, "EntryType"), str(o, "SensorType")
	msgID, msg := str(o, "MessageId"), str(o, "Message")
	origin, originIsMemory := r.memoryOrigin(originOf(o))
	if originIsMemory {
		d.labels = []string{origin}
	}
	if code := str(o, "EntryCode"); strings.HasPrefix(strings.ToLower(code), "deassert") {
		d.deassert = true
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
		if selDataRE.MatchString(msgID) {
			et, deassert, data, err := ipmi.ParseEventData(msgID)
			if err == nil {
				rec := ipmi.Record{Type: ipmi.RecordSystemEvent, SensorType: ipmi.SensorMemory, EventType: et, Deassert: deassert, Data: data}
				if n, ok := integer(o, "SensorNumber"); ok && n >= 0 && n <= 0xFF {
					rec.SensorNumber = uint8(n)
				}
				d.ev = ipmi.MemoryEventOf(rec)
				d.deassert = d.deassert || deassert
				if id := rec.ModuleID(); id.Valid && len(d.labels) == 0 {
					d.labels = []string{fmt.Sprintf("sensor 0x%02x module %d", rec.SensorNumber, id.V)}
				}
			}
		}
		if d.ev == ipmi.MemUnknown {
			if d.ev, _ = ipmi.MemoryEventFromText(msg); d.ev == ipmi.MemUnknown {
				d.ev, _ = ipmi.MemoryEventInText(msg)
			}
		}
		if strings.Contains(strings.ToLower(msg), "deassert") {
			d.deassert = true
		}
		// The module in the text wins over a module number: it is a name.
		if names := ipmi.ModuleNames(msg); len(names) > 0 && !originIsMemory {
			d.labels = names
		}
		d.text = msg
		return d, true
	}

	if m := openbmcRE.FindStringSubmatch(msgID); m != nil {
		ev, known := openbmcEvents[m[1]]
		if !known && !openbmcCleared[m[1]] && !strings.HasPrefix(m[1], "Memory") && !strings.HasPrefix(m[1], "Mirroring") {
			return d, false
		}
		d.decoder, d.text = "redfish/openbmc", msg
		d.cleared = openbmcCleared[m[1]]
		if known {
			d.ev = ev
		}
		// Socket, channel and DIMM lead the arguments of these messages;
		// the parity messages say whether channel and DIMM are known.
		if a := args(o); len(a) >= 3 && m[1] != "MemoryThermTrip" {
			chOK, dimmOK := true, true
			if strings.HasPrefix(m[1], "MemoryParity") && len(a) >= 5 {
				chOK, dimmOK = a[3] != "0", a[4] != "0"
			}
			d.socket = smallInt(a[0])
			if chOK {
				d.channel = channelIndex(a[1])
			}
			if chOK && dimmOK {
				d.position = smallInt(a[2])
			}
			if len(a) >= 4 && (m[1] == "MemoryECCCorrectable" || m[1] == "MemoryECCUncorrectable") {
				d.rank = smallInt(a[3])
			}
		}
		return d, true
	}

	if m := memIDRE.FindStringSubmatch(msgID); m != nil {
		d.decoder, d.text = "redfish/registry", msg
		if ev, known := memIDEvents[m[1]]; known {
			d.ev = ev
		}
		d.cleared = memIDCleared[m[1]]
		if !originIsMemory {
			d.labels = locations(o, msg)
		}
		return d, true
	}

	// Plain text: the name of an IPMI memory state, or a vendor text that
	// names the module as "(Processor 1, Memory Module 4)".
	pm := procModuleRE.FindStringSubmatch(msg)
	ev, _ := ipmi.MemoryEventInText(msg)
	if ev == ipmi.MemUnknown && pm != nil {
		if low := strings.ToLower(msg); strings.Contains(low, "corrected memory error threshold exceeded") {
			ev = ipmi.MemCorrectable
		}
	}
	if ev != ipmi.MemUnknown || pm != nil {
		d.ev, d.decoder, d.text = ev, "redfish/text", msg
		switch names := ipmi.ModuleNames(msg); {
		case originIsMemory:
		case len(names) > 0:
			d.labels = names
		case pm != nil:
			// The boards that write this text name the slot "PROC 1 DIMM 4".
			d.labels = []string{fmt.Sprintf("PROC %s DIMM %s", pm[1], pm[2])}
		}
		return d, true
	}

	if originIsMemory || memoryWordRE.MatchString(msg) || strings.Contains(strings.ToLower(msgID), "memory") {
		d.text = msg
		return d, true
	}
	return d, false
}

// locations returns the modules a registry message names: its arguments
// when they are DIMM names, and the text after "at location" otherwise.
func locations(o object, msg string) []string {
	var out []string
	for _, a := range args(o) {
		out = append(out, splitLocations(a)...)
	}
	if len(out) == 0 {
		if m := locationRE.FindStringSubmatch(msg); m != nil {
			out = splitLocations(m[1])
		}
	}
	if len(out) == 0 {
		out = ipmi.ModuleNames(msg)
	}
	return out
}

// splitLocations splits "DIMM_A1, DIMM_A2" and keeps what looks like the
// name of a slot: a DIMM name, a processor with a module number, or a
// short designator such as "A1".
func splitLocations(s string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' }) {
		p = strings.TrimSpace(p)
		switch names := ipmi.ModuleNames(p); {
		case p == "":
		case ipmi.IsProcessorModule(p):
			out = append(out, strings.Join(strings.Fields(p), " "))
		case len(names) > 0:
			out = append(out, names...)
		case len(p) <= 6 && !strings.ContainsAny(p, " ()") && strings.ContainsAny(p, "0123456789"):
			out = append(out, p)
		}
	}
	return out
}

// logs turns the log entries into events and actions.
func (r *run) logs(sink adapter.Sink) error {
	type out struct {
		ev  *model.Event
		act *model.Action
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
		res := &r.entries[i]
		o := res.obj
		// Every entry counts for the period its log covers.
		r.cover(res)
		if strings.EqualFold(str(o, "EntryType"), "CXL") {
			cxl++
			continue
		}
		d, memory := r.decode(o)
		if !memory {
			notMemory++
			continue
		}
		r.coverMemory(res)
		// A place given by numbers is the slot the same dump describes
		// with those numbers.
		if len(d.labels) == 0 && d.socket.Valid && d.channel.Valid && d.position.Valid {
			if name, ok := r.places[[3]int{d.socket.V, d.channel.V, d.position.V}]; ok {
				d.labels = []string{name}
			}
		}
		created, at, ok := entryTime(o)
		if !ok {
			r.tally.Skip(res.ref, "log entry about memory has no readable time")
			continue
		}
		fp := model.Fingerprint(Kind, "log", res.id, created, str(o, "MessageId"), str(o, "Message"))
		rawText := raw(o)
		event := func(sev model.Severity, text string) *model.Event {
			e := &model.Event{Kind: model.KindError, ErrorDT: at, TimeQuality: model.TimeExact, ErrorSeverity: sev, ErrorType: text, Count: 1,
				SocketId: d.socket, ChannelId: d.channel, ChannelSlotId: d.position, Rank: d.rank,
				Source: Kind + "/log", Decoder: d.decoder, RawRef: res.ref, Raw: rawText, Fingerprint: fp}
			if len(d.labels) == 1 {
				e.Label = d.labels[0]
			}
			return e
		}

		if d.cleared {
			cleared++
			continue
		}
		if d.ev == ipmi.MemUnknown {
			// About memory, in words this reader has no class for.
			key := str(o, "MessageId")
			if key == "" || selDataRE.MatchString(key) {
				key = d.text
				if len(key) > 60 {
					key = key[:60] + "..."
				}
			}
			unclassified[key]++
			text := d.text
			if text == "" {
				text = str(o, "MessageId")
			}
			if text == "" {
				r.tally.Skip(res.ref, "log entry about memory has no message")
				continue
			}
			if len(text) > 200 {
				text = text[:200] + "..."
			}
			outs = append(outs, out{ev: event(model.SevInformational, text)})
			continue
		}

		m := d.ev.Meaning()
		if d.deassert {
			if d.ev == ipmi.MemDeviceDisabled {
				for j := len(outs) - 1; j >= 0; j-- {
					a := outs[j].act
					if a == nil || a.Kind != model.ActionDisable || len(d.labels) != 1 || model.NormalizeLabel(a.Label) != model.NormalizeLabel(d.labels[0]) {
						continue
					}
					was := event(model.SevInformational, d.ev.String()+" (enabled again later)")
					was.ErrorDT, was.RawRef, was.Raw, was.Label, was.Fingerprint = a.At, a.RawRef, a.Raw, a.Label, a.Fingerprint
					outs[j] = out{ev: was}
					withdrawn++
				}
				outs = append(outs, out{ev: event(model.SevInformational, d.ev.String()+" (deasserted: in use again)")})
				continue
			}
			cleared++
			continue
		}
		switch m.Class {
		case ipmi.ClassNone:
			presence++
		case ipmi.ClassError, ipmi.ClassInfo:
			e := event(m.Severity, d.ev.String())
			e.ErrorScrubType = m.Scrub
			named := len(d.labels) > 0 || (d.socket.Valid && d.channel.Valid && d.position.Valid)
			switch {
			case len(d.labels) > 1:
				e.ErrorType += " on one of " + strings.Join(d.labels, ", ")
				multi++
			case !named && m.Class == ipmi.ClassError:
				unnamed++
			}
			if d.ev == ipmi.MemLogLimit {
				logLimit = append(logLimit, at)
			}
			outs = append(outs, out{ev: e})
		case ipmi.ClassAction:
			targets := d.labels
			if len(targets) == 0 {
				targets = []string{""}
				unnamed++
			}
			method := d.generator
			if method == "" && d.decoder != "redfish/sel" {
				method = "bmc" // the BMC's own log
			}
			for n, label := range targets {
				outs = append(outs, out{act: &model.Action{At: at, TimeQuality: model.TimeExact, Kind: m.Action, Method: method, Result: m.Result,
					Label: label, Detail: d.ev.String(), Source: Kind + "/log", RawRef: res.ref, Raw: rawText,
					Fingerprint: model.Fingerprint(fp, fmt.Sprint(n))}})
			}
		}
	}

	for _, o := range outs {
		var err error
		if o.ev != nil {
			o.ev.Grade = o.ev.ComputeGrade()
			err = sink.Event(o.ev)
		} else {
			err = sink.Action(o.act)
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
		add("%d presence entries were left out", presence)
	}
	if cleared > 0 {
		add("%d entries only say that a state ended and were left out", cleared)
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
	if unnamed > 0 {
		add("%d memory entries do not name a module", unnamed)
	}
	if n := len(logLimit); n > 0 {
		add("the firmware stopped logging corrected errors %d time(s), first on %s: corrected errors after that are missing from this log",
			n, logLimit[0].Format("2006-01-02 15:04 UTC"))
	}
	return nil
}
