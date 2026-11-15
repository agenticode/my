// Package sel reads the IPMI system event log (SEL) of a BMC from a dump:
// the text ipmitool prints for "sel list" and "sel elist", with and without
// -v and -c, the default output of FreeIPMI's ipmi-sel, the binary file
// "ipmitool sel writeraw" saves, and JSON lists of log entries.
//
// A SEL holds what the BIOS and the BMC reported about memory: corrected
// and uncorrected errors, a module taken out of use, a spare taking over.
// It is the record that survives when the operating system did not live to
// write its own, and the only one there is when the host cannot be reached.
// It names a module at best and never an address, so its events have the
// lowest observation grade.
package sel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/decode/ipmi"
	"github.com/agenticode/my/internal/model"
)

// Kind is the adapter name in the source inventory.
const Kind = "sel"

// Options tell the adapter what the input itself cannot say.
type Options struct {
	// Location is the time zone of the times in a text dump. ipmitool
	// prints them in the zone of the machine it ran on, and only newer
	// versions name that zone. When Location is nil a zone the dump names
	// is used if its abbreviation is unambiguous, and UTC otherwise; the
	// report says which. The timestamps of a binary dump are the BMC's own
	// clock and are read as UTC.
	Location *time.Location
}

// Adapter reads one SEL dump.
type Adapter struct {
	origin string
	open   func() (io.ReadCloser, error)
	opts   Options
}

// NewFile returns an adapter for a SEL dump in a file.
func NewFile(path string, opts Options) *Adapter {
	return &Adapter{origin: path, opts: opts, open: func() (io.ReadCloser, error) {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		if fi, err := f.Stat(); err == nil && fi.IsDir() {
			f.Close()
			return nil, fmt.Errorf("%s is a directory", path)
		}
		return f, nil
	}}
}

// NewReader returns an adapter for a SEL dump read from r, for example the
// output of a command. origin names it in reports and raw references.
func NewReader(r io.Reader, origin string, opts Options) *Adapter {
	return &Adapter{origin: origin, opts: opts, open: func() (io.ReadCloser, error) { return io.NopCloser(r), nil }}
}

func (a *Adapter) Kind() string   { return Kind }
func (a *Adapter) Origin() string { return a.origin }

// maxInput bounds the size of a dump. A SEL holds a few thousand records.
const maxInput = 64 << 20

// Input forms.
const (
	formList    = "list"     // ipmitool sel list, sel elist, with or without -c
	formVerbose = "verbose"  // ipmitool sel list -v
	formFree    = "ipmi-sel" // FreeIPMI
	formRaw     = "raw"      // ipmitool sel writeraw
	formJSON    = "json"
)

// entry is one SEL record in the form all inputs are brought to.
type entry struct {
	ref string
	raw string
	// id is the record ID as hexadecimal digits without leading zeros.
	id string

	hasTime bool
	t       time.Time
	// relative is set when the time counts from the start of the BMC.
	relative bool
	// wall is the time as the dump printed it, for the fingerprint.
	wall string

	// other is set for records that are not standard events: OEM records
	// and the kernel panic record.
	other string

	sensorType string // the sensor type as printed, "" when only the code is known
	typeCode   int    // the sensor type code, -1 when only the name is known
	sensorName string
	sensorNum  int // -1 when unknown
	desc       string
	deassert   bool
	extra      string // further fields of the line

	// rec is set when the event bytes are known.
	rec       *ipmi.Record
	generator string
}

// Collect reads the dump into sink.
func (a *Adapter) Collect(ctx context.Context, sink adapter.Sink) adapter.Report {
	rep := adapter.Report{Kind: Kind, Origin: a.origin}
	if err := ctx.Err(); err != nil {
		rep.Status, rep.Detail = model.SourceError, err.Error()
		return rep
	}
	rc, err := a.open()
	if err != nil {
		rep.Status, rep.Detail = adapter.StatusForError(err)
		return rep
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxInput+1))
	if err != nil {
		rep.Status, rep.Detail = adapter.StatusForError(err)
		return rep
	}
	if len(data) > maxInput {
		rep.Status, rep.Detail = model.SourceError, "larger than 64 MiB: not a SEL dump"
		return rep
	}

	r := &reader{ctx: ctx, origin: a.origin, opts: a.opts, rep: &rep}
	form, why := sniff(data)
	if form == "" {
		rep.Status, rep.Detail = model.SourceError, why
		return rep
	}
	switch form {
	case formRaw:
		r.readRaw(data)
	case formJSON:
		if err := r.readJSON(data); err != nil {
			rep.Status, rep.Detail = model.SourceError, "not readable as JSON: "+err.Error()
			return rep
		}
	default:
		r.readText(string(data), form)
	}
	rep.Records = len(r.entries)
	rep.Notes = append(rep.Notes, fmt.Sprintf("%d records read (%s)", len(r.entries), formName[form]))

	if err := r.emit(sink, form); err != nil {
		var stop stopped
		if errors.As(err, &stop) {
			rep.Status, rep.Detail = model.SourceError, stop.err.Error()
		} else {
			rep.Status, rep.Detail, rep.SinkErr = model.SourceError, "ledger rejected a record: "+err.Error(), err
		}
		return rep
	}
	rep.Notes = append(rep.Notes, r.notes()...)
	rep.Skipped = r.tally.Total()
	rep.Notes = append(rep.Notes, r.tally.Notes()...)
	switch {
	case rep.Records == 0:
		rep.Status, rep.Detail = model.SourceEmpty, "no SEL records"
	case r.memory == 0:
		rep.Status, rep.Detail = model.SourceEmpty, fmt.Sprintf("none of the %d records is from a memory sensor", rep.Records)
	case rep.Skipped > 0:
		rep.Status = model.SourcePartial
	default:
		rep.Status = model.SourceOK
	}
	return rep
}

var formName = map[string]string{
	formList:    "ipmitool sel list",
	formVerbose: "ipmitool sel list -v",
	formFree:    "ipmi-sel",
	formRaw:     "ipmitool sel writeraw",
	formJSON:    "JSON",
}

// Sniff reports whether data looks like a SEL dump this adapter reads. It
// is used to pick the adapter for a file by its content.
func Sniff(data []byte) bool {
	form, _ := sniff(data)
	return form != ""
}

// sniff tells the input forms apart. The second result says why the input
// is none of them.
func sniff(data []byte) (form, why string) {
	trimmed := bytes.TrimLeft(data, " \t\r\n\xef\xbb\xbf")
	if len(trimmed) == 0 {
		// An empty SEL prints nothing, or a line that says so.
		return formList, ""
	}
	if isBinary(data) {
		if len(data)%ipmi.RecordSize == 0 && plausibleRaw(data) {
			return formRaw, ""
		}
		return "", "binary data that is not a file of 16-byte SEL records"
	}
	if looksLikeJSON(trimmed) {
		return formJSON, ""
	}
	head := string(trimmed)
	if len(head) > 16<<10 {
		head = head[:16<<10]
	}
	lines := strings.Split(head, "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		switch {
		case l == "":
			continue
		case strings.HasPrefix(l, "SEL Record ID"):
			return formVerbose, ""
		case strings.HasPrefix(l, "ID") && strings.Contains(l, "| Date") && strings.Contains(l, "| Event"):
			return formFree, ""
		case isSaveLine(l):
			return "", `a file written by "ipmitool sel save" holds the events without their times; use "sel elist", "sel list -v" or "sel writeraw"`
		case strings.Contains(l, "SEL has no entries"):
			return formList, ""
		}
		if _, ok := splitListLine(l); ok {
			return formList, ""
		}
	}
	return "", "no SEL records found: the input is none of the forms this reader knows"
}

// isBinary reports whether data is not text. Every record of a binary dump
// has a type byte that is a control character or no valid UTF-8.
func isBinary(data []byte) bool {
	if !utf8.Valid(data) {
		return true
	}
	for _, c := range data {
		if c < 0x20 && c != '\t' && c != '\n' && c != '\r' && c != '\f' {
			return true
		}
	}
	return false
}

// looksLikeJSON tells a JSON document from text that happens to start with
// a bracket, as every line of a kernel log does.
func looksLikeJSON(trimmed []byte) bool {
	switch trimmed[0] {
	case '{':
		return true
	case '[':
		rest := bytes.TrimLeft(trimmed[1:], " \t\r\n")
		if len(rest) > 0 && (rest[0] == '{' || rest[0] == ']' || rest[0] == '"') {
			return true
		}
		return json.Valid(trimmed)
	}
	return false
}

// isSaveLine recognises a line of "ipmitool sel save":
// 0x04 0x0c 0x53 0x6f 0xa0 0xff 0x01 # Memory #0x53 Correctable ECC
func isSaveLine(l string) bool {
	f := strings.Fields(l)
	if len(f) < 8 || f[7] != "#" {
		return false
	}
	for _, x := range f[:7] {
		if len(x) != 4 || !strings.HasPrefix(x, "0x") {
			return false
		}
	}
	return true
}

// plausibleRaw checks that every 16-byte record of a binary file has a
// record type the specification defines.
func plausibleRaw(data []byte) bool {
	for off := 0; off+ipmi.RecordSize <= len(data); off += ipmi.RecordSize {
		if t := data[off+2]; t != ipmi.RecordSystemEvent && t < 0xC0 {
			return false
		}
	}
	return len(data) > 0
}

// reader carries the state of one Collect call.
type reader struct {
	ctx    context.Context
	origin string
	opts   Options
	rep    *adapter.Report
	tally  adapter.Tally

	entries []entry
	clock   clock

	memory        int // records of memory sensors
	otherSensors  int
	otherRecords  map[string]int
	cleared       int
	presence      int
	unclassified  map[string]int
	multiDIMM     int
	cancelled     int
	logLimit      []time.Time
	logCleared    []time.Time
	ceLoggingOff  int
	unnamedModule int
}

func (r *reader) refLine(n int) string { return fmt.Sprintf("%s#L%d", r.origin, n) }

// readRaw decodes a file of 16-byte records.
func (r *reader) readRaw(data []byte) {
	for i, off := 0, 0; off+ipmi.RecordSize <= len(data); i, off = i+1, off+ipmi.RecordSize {
		chunk := data[off : off+ipmi.RecordSize]
		rec, err := ipmi.ParseRecord(chunk)
		e := entry{ref: fmt.Sprintf("%s#record %d", r.origin, i+1), raw: fmt.Sprintf("% x", chunk), sensorNum: -1, typeCode: -1}
		if err != nil {
			r.entries = append(r.entries, e)
			continue
		}
		e.id = fmt.Sprintf("%x", rec.ID)
		if rec.HasTime {
			e.hasTime = true
			e.t = time.Unix(int64(rec.Timestamp), 0).UTC()
			e.relative = rec.Timestamp < ipmi.TimeInitDone
			e.wall = e.t.Format("20060102150405")
		}
		if !rec.SystemEvent() {
			e.other = fmt.Sprintf("OEM record %02x", rec.Type)
		} else {
			e.rec = &rec
			e.typeCode, e.sensorNum = int(rec.SensorType), int(rec.SensorNumber)
			e.deassert = rec.Deassert
			e.generator = ipmi.Generator(rec.GeneratorID)
		}
		r.entries = append(r.entries, e)
	}
}

// emit turns the entries into ledger records.
func (r *reader) emit(sink adapter.Sink, form string) error {
	type out struct {
		ev   *model.Event
		act  *model.Action
		e    *entry
		name ipmi.MemoryEvent
	}
	var outs []out
	source := Kind + "/" + form

	for i := range r.entries {
		if i%1024 == 0 {
			if err := r.ctx.Err(); err != nil {
				return stopped{err}
			}
		}
		e := &r.entries[i]
		if e.other != "" {
			if r.otherRecords == nil {
				r.otherRecords = map[string]int{}
			}
			r.otherRecords[e.other]++
			continue
		}
		isMemory := e.typeCode == ipmi.SensorMemory || (e.typeCode < 0 && sensorTypeIs(e.sensorType, "Memory"))
		isLogging := e.typeCode == ipmi.SensorEventLogging || (e.typeCode < 0 && sensorTypeIs(e.sensorType, "Event Logging Disabled"))
		if isLogging {
			r.logging(e)
		}
		if !isMemory {
			r.otherSensors++
			continue
		}
		r.memory++
		if !e.hasTime {
			r.tally.Skip(e.ref, "memory sensor record has no readable time")
			continue
		}

		// What happened: from the event bytes when they are known, from the
		// description otherwise.
		ev, rest := ipmi.MemUnknown, ""
		switch {
		case e.rec != nil && e.rec.EventType == ipmi.EventTypeSensorSpecific:
			ev = ipmi.MemoryEventOf(*e.rec)
			_, rest = ipmi.MemoryEventFromText(e.desc)
		case e.rec == nil:
			ev, rest = ipmi.MemoryEventFromText(e.desc)
		}
		labels := r.labels(e, rest)
		fp := model.Fingerprint(Kind, e.id, e.wall, fmt.Sprint(int(ev)), strings.ToLower(strings.TrimSpace(e.desc)), fmt.Sprint(e.deassert))
		info := func(text string) *model.Event {
			return &model.Event{Kind: model.KindError, ErrorSeverity: model.SevInformational, ErrorType: text, Count: 1,
				Source: source, Decoder: "ipmi/sel", RawRef: e.ref, Raw: e.raw, Fingerprint: fp}
		}

		if ev == ipmi.MemUnknown {
			// A memory sensor said something this reader has no class for.
			text := strings.TrimSpace(e.desc)
			if text == "" && e.rec != nil {
				text = fmt.Sprintf("event type %02xh, data %02x%02x%02x", e.rec.EventType, e.rec.Data[0], e.rec.Data[1], e.rec.Data[2])
			}
			if text == "" {
				r.tally.Skip(e.ref, "memory sensor record has no description")
				continue
			}
			if e.deassert {
				text += " (deasserted)"
			}
			if r.unclassified == nil {
				r.unclassified = map[string]int{}
			}
			r.unclassified[text]++
			x := info(text)
			if len(labels) == 1 {
				x.Label = labels[0]
			}
			outs = append(outs, out{ev: x, e: e, name: ev})
			continue
		}

		m := ev.Meaning()
		if e.deassert {
			if ev == ipmi.MemDeviceDisabled {
				// The module is in use again. A disable this log recorded
				// before for the same module no longer holds.
				for j := len(outs) - 1; j >= 0; j-- {
					o := &outs[j]
					if o.act == nil || o.act.Kind != model.ActionDisable || !sameModule(o.e, e, o.act.Label, labels) {
						continue
					}
					was := info(ev.String() + " (enabled again later)")
					was.RawRef, was.Raw, was.Label, was.Fingerprint = o.act.RawRef, o.act.Raw, o.act.Label, o.act.Fingerprint
					o.ev, o.act = was, nil
					r.cancelled++
				}
				x := info(ev.String() + " (deasserted: in use again)")
				if len(labels) == 1 {
					x.Label = labels[0]
				}
				outs = append(outs, out{ev: x, e: e, name: ev})
				continue
			}
			r.cleared++
			continue
		}

		switch m.Class {
		case ipmi.ClassNone:
			r.presence++
		case ipmi.ClassError, ipmi.ClassInfo:
			x := info(ev.String())
			x.ErrorSeverity, x.ErrorScrubType = m.Severity, m.Scrub
			switch len(labels) {
			case 1:
				x.Label = labels[0]
			case 0:
				if m.Class == ipmi.ClassError {
					r.unnamedModule++
				}
			default:
				// One record that names several modules is one error on
				// one of them.
				x.ErrorType += " on one of " + strings.Join(labels, ", ")
				r.multiDIMM++
			}
			if ev == ipmi.MemLogLimit {
				r.logLimit = append(r.logLimit, e.t)
			}
			outs = append(outs, out{ev: x, e: e, name: ev})
		case ipmi.ClassAction:
			targets := labels
			if len(targets) == 0 {
				targets = []string{""}
				r.unnamedModule++
			}
			for n, label := range targets {
				a := &model.Action{Kind: m.Action, Method: e.generator, Result: m.Result, Label: label, Detail: ev.String(),
					Source: source, RawRef: e.ref, Raw: e.raw, Fingerprint: model.Fingerprint(fp, fmt.Sprint(n))}
				outs = append(outs, out{act: a, e: e, name: ev})
			}
		}
	}

	for i := range outs {
		o := &outs[i]
		q := model.TimeExact
		if o.e.relative {
			q = model.TimeRelative
		}
		var err error
		switch {
		case o.ev != nil:
			o.ev.ErrorDT, o.ev.TimeQuality = o.e.t, q
			o.ev.Grade = o.ev.ComputeGrade()
			err = sink.Event(o.ev)
		case o.act != nil:
			o.act.At, o.act.TimeQuality = o.e.t, q
			err = sink.Action(o.act)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// sameModule reports whether two records are about the same module: the
// same label, or the same sensor when neither names a module.
func sameModule(a, b *entry, aLabel string, bLabels []string) bool {
	if aLabel != "" || len(bLabels) > 0 {
		return len(bLabels) == 1 && model.NormalizeLabel(aLabel) == model.NormalizeLabel(bLabels[0])
	}
	if a.sensorNum >= 0 && b.sensorNum >= 0 {
		return a.sensorNum == b.sensorNum
	}
	return strings.EqualFold(a.sensorName, b.sensorName)
}

// logging notes what records of the event logging sensor say about the
// log itself.
func (r *reader) logging(e *entry) {
	text := strings.ToLower(e.desc)
	offset := -1
	if e.rec != nil && e.rec.EventType == ipmi.EventTypeSensorSpecific {
		offset = e.rec.Offset()
	}
	switch {
	case offset == 0 || strings.Contains(text, "correctable memory error logging disabled"):
		if !e.deassert {
			r.ceLoggingOff++
		}
	case offset == 2 || strings.Contains(text, "log area reset") || strings.Contains(text, "log area cleared"):
		if e.hasTime && !e.relative {
			r.logCleared = append(r.logCleared, e.t)
		}
	}
}

// notes returns the remarks of the run for the report.
func (r *reader) notes() []string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	if r.otherSensors > 0 {
		add("%d records of other sensors were left out", r.otherSensors)
	}
	for i, k := range sortedKeys(r.otherRecords) {
		if i == maxKinds {
			add("%d more kinds of record with a vendor format were not read", len(r.otherRecords)-maxKinds)
			break
		}
		add("%d records of kind %q have a vendor format and were not read", r.otherRecords[k], k)
	}
	if r.presence > 0 {
		add("%d presence records were left out", r.presence)
	}
	if r.cleared > 0 {
		add("%d records only say that a state ended and were left out", r.cleared)
	}
	if r.cancelled > 0 {
		add("%d disable records were followed by the module coming back into use; they are kept as information, not as a disabled module", r.cancelled)
	}
	for i, k := range sortedKeys(r.unclassified) {
		if i == maxKinds {
			add("memory sensor records with %d more texts this reader has no class for are kept as information", len(r.unclassified)-maxKinds)
			break
		}
		add("%d memory sensor records say %q, which this reader has no class for; they are kept as information", r.unclassified[k], k)
	}
	if r.multiDIMM > 0 {
		add("%d error records name more than one module; they are counted once and tied to none of them", r.multiDIMM)
	}
	if r.unnamedModule > 0 {
		add("%d memory records do not name a module: the dump shows no per-module sensor name and no module in the event text", r.unnamedModule)
	}
	if n := len(r.logLimit); n > 0 {
		add("the firmware stopped logging corrected errors %d time(s), first on %s: corrected errors after that are missing from this log",
			n, r.logLimit[0].Format("2006-01-02 15:04 UTC"))
	}
	if r.ceLoggingOff > 0 {
		add("%d records say that logging of corrected memory errors was switched off: later corrected errors are missing from this log", r.ceLoggingOff)
	}
	if n := len(r.logCleared); n > 0 {
		last := r.logCleared[0]
		for _, t := range r.logCleared {
			if t.After(last) {
				last = t
			}
		}
		add("the log was cleared, last on %s: records before that are gone", last.Format("2006-01-02 15:04 UTC"))
	}
	out = append(out, r.clock.notes(r.opts.Location)...)
	return out
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sensorTypeIs reports whether the printed sensor type is name.
func sensorTypeIs(printed, name string) bool {
	return strings.EqualFold(strings.TrimSpace(printed), name)
}

// maxKinds bounds the number of notes about one sort of leftover.
const maxKinds = 8

// stopped marks the end of a read because its context ended.
type stopped struct{ err error }

func (s stopped) Error() string { return s.err.Error() }

// errNoRecords is returned by the JSON reader for a document without a
// list of entries.
var errNoRecords = errors.New("no list of log entries found")
