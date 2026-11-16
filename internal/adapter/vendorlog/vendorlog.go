// Package vendorlog reads logs that a vendor's tool printed and someone
// saved: the output of racadm getsel and racadm lclog view of a Dell iDRAC.
// Each format has a file of its own here; each turns its records into the
// common events of package fwevent through the mapping tables of package
// fwmap, as the Redfish reader does with the same messages.
//
//	racadm getsel        Record:, Date/Time:, Source:, Severity:,
//	                     Description: and a dashed line per record. The
//	                     description is the sentence of the message
//	                     without its ID: the table of the vendor has
//	                     patterns for the sentences about memory.
//	racadm lclog view    SeqNumber =, Message ID =, Category =, AgentID =,
//	                     Severity =, Timestamp =, Message =,
//	                     Message Arg n =, FQDD = per record.
//
// Neither prints a time zone. A time is read in the zone the import names
// (--tz), else as UTC, and the report says so.
package vendorlog

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/fwevent"
	"github.com/agenticode/my/internal/fwmap"
	"github.com/agenticode/my/internal/model"
)

// Kind is the adapter name in the source inventory; a source is named
// "vendorlog/<format>".
const Kind = "vendorlog"

// Formats this package reads.
const (
	FormatGetSEL = "racadm-getsel"
	FormatLCLog  = "racadm-lclog"
)

// Limits of one input. A full SEL of an iDRAC holds a few thousand
// records, a Lifecycle log some ten thousand.
const (
	maxInput = 64 << 20
	maxLine  = 64 << 10
	// maxRecordLines bounds the lines of one record: a record that does
	// not end is not read past them.
	maxRecordLines = 64
)

// Options tell the adapter what the input itself cannot say.
type Options struct {
	// Location is the time zone of the times, which carry none. Nil means
	// UTC, with a note.
	Location *time.Location
}

// Adapter reads one saved tool output.
type Adapter struct {
	origin string
	opts   Options
	open   func() (io.ReadCloser, error)
}

func (a *Adapter) Kind() string   { return Kind }
func (a *Adapter) Origin() string { return a.origin }

// NewFile reads a saved output from a file.
func NewFile(path string, opts Options) *Adapter {
	return &Adapter{origin: path, opts: opts, open: func() (io.ReadCloser, error) { return os.Open(path) }}
}

// NewReader reads a saved output from r, named origin.
func NewReader(r io.Reader, origin string, opts Options) *Adapter {
	return &Adapter{origin: origin, opts: opts, open: func() (io.ReadCloser, error) { return io.NopCloser(r), nil }}
}

// FormatOf tells the format of the start of an input, or "".
func FormatOf(data []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 4096), maxLine)
	var record, dateTime, seq, msgID bool
	for n := 0; sc.Scan() && n < 200; n++ {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "Record:"):
			record = true
		case record && strings.HasPrefix(line, "Date/Time:"):
			dateTime = true
		case record && dateTime && strings.HasPrefix(line, "Description:"):
			return FormatGetSEL
		case strings.HasPrefix(line, "SeqNumber") && strings.Contains(line, "="):
			seq = true
		case seq && strings.HasPrefix(line, "Message ID") && strings.Contains(line, "="):
			msgID = true
		case seq && msgID && strings.HasPrefix(line, "Timestamp") && strings.Contains(line, "="):
			return FormatLCLog
		}
	}
	return ""
}

// Sniff reports whether data is a format of this package.
func Sniff(data []byte) bool { return FormatOf(data) != "" }

// entry is one record of a saved output.
type entry struct {
	ref, raw  string
	n         int
	stamp     string
	id        string
	args      []string
	message   string
	severity  string
	fqdd      string
	agent     string
	malformed string
}

// Collect reads the output into sink.
func (a *Adapter) Collect(ctx context.Context, sink adapter.Sink) adapter.Report {
	rep := adapter.Report{Kind: Kind, Origin: a.origin, Status: model.SourceOK}
	f, err := a.open()
	if err != nil {
		rep.Status, rep.Detail = model.SourceUnreadable, err.Error()
		if errors.Is(err, os.ErrNotExist) {
			rep.Status = model.SourceAbsent
		}
		return rep
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxInput+1))
	if err != nil {
		rep.Status, rep.Detail = model.SourceError, err.Error()
		return rep
	}
	if len(data) > maxInput {
		rep.Status, rep.Detail = model.SourceError, fmt.Sprintf("larger than %d MiB", maxInput>>20)
		return rep
	}
	format := FormatOf(data)
	if format == "" {
		rep.Status, rep.Detail = model.SourceError, "neither racadm getsel nor racadm lclog view output"
		return rep
	}
	what := map[string]string{FormatGetSEL: "racadm getsel output", FormatLCLog: "racadm lclog view output"}[format]
	var entries []entry
	switch format {
	case FormatGetSEL:
		entries = splitGetSEL(data, a.origin)
	case FormatLCLog:
		entries = splitLCLog(data, a.origin)
	}
	r := &run{ctx: ctx, sink: sink, format: format, opts: a.opts, unclassified: map[string]int{}}
	err = r.read(entries)
	rep.Records = len(entries)
	rep.AddTally(&r.tally)
	if err != nil {
		var se sinkErr
		if errors.As(err, &se) {
			rep.SinkErr = se.err
		}
		rep.Status, rep.Detail = model.SourceError, err.Error()
		return rep
	}
	rep.Notes = append(append(rep.Notes, what), r.notes()...)
	if r.memory == 0 {
		rep.Notes = append(rep.Notes, "no record about memory")
	}
	return rep
}

type sinkErr struct{ err error }

func (e sinkErr) Error() string { return e.err.Error() }

// run reads the records of one output.
type run struct {
	ctx    context.Context
	sink   adapter.Sink
	format string
	opts   Options
	tally  adapter.Tally

	memory, notMemory, cleared, presence int
	unclassified                         map[string]int
	noZone                               bool
}

func (r *run) read(entries []entry) error {
	source := Kind + "/" + r.format
	for i := range entries {
		if i%1024 == 0 {
			if err := r.ctx.Err(); err != nil {
				return err
			}
		}
		if i == adapter.MaxRecords {
			r.tally.Skip(entries[i].ref, fmt.Sprintf("%d records after the first %d were not read", len(entries)-i, adapter.MaxRecords))
			break
		}
		e := &entries[i]
		if err := adapter.Each(&r.tally, e.ref, e.raw, func() error { return r.one(e, source) }); err != nil {
			return err
		}
	}
	return nil
}

// one reads one record.
func (r *run) one(e *entry, source string) error {
	if e.malformed != "" {
		return adapter.Unreadable("%s", e.malformed)
	}
	ask := fwmap.Entry{Message: e.message, Args: e.args, Manufacturer: "Dell"}
	if e.id != "" {
		ask.MessageID = e.id
	}
	res := fwmap.Current().Classify(ask)
	if res.Skipped || !res.Memory {
		r.notMemory++
		return nil
	}
	r.memory++
	at, err := r.time(e.stamp)
	if err != nil {
		r.tally.Unread(e.ref, "record about memory has no readable time", e.raw)
		return nil
	}

	// The place: the arguments by the roles of the line, the words a
	// pattern found, or the FQDD of the record.
	form := "dell"
	if res.Table != nil {
		form = res.Table.Location
	}
	p := fwmap.PlaceOf(form, res.Roles, e.args)
	labels := p.Labels
	if loc := strings.TrimSpace(res.Text["location"]); loc != "" && len(labels) == 0 {
		labels = fwmap.DellLocations(loc)
		p.Location = loc
	}
	if len(labels) == 0 && strings.HasPrefix(strings.ToUpper(e.fqdd), "DIMM.") {
		labels = fwmap.DellLocations(e.fqdd)
		p.Location = e.fqdd
	}

	ev, clears := res.Event, res.Clears
	if !ev.Valid() {
		ev = fwevent.Unclassified
	}
	text := fwevent.Text(ev, clears)
	if res.Line != nil && res.Line.Words != "" {
		text = res.Line.Words
	}
	switch ev {
	case fwevent.Cleared:
		r.cleared++
	case fwevent.Presence:
		r.presence++
	case fwevent.Unclassified:
		key := e.id
		if key == "" {
			key = e.message
			if len(key) > 60 {
				key = key[:60] + "..."
			}
		}
		r.unclassified[key]++
		text = e.message
		if len(text) > 200 {
			text = text[:200] + "..."
		}
		if text == "" {
			r.tally.Unread(e.ref, "record about memory has no message", e.raw)
			return nil
		}
	}
	vendorID := e.id
	if vendorID == "" {
		vendorID = res.Key
	}
	_, _, key := fwmap.SplitMessageID(vendorID)
	log := "SEL"
	if r.format == FormatLCLog {
		log = "Lclog"
	}
	origin := model.OriginToolExport
	if e.agent == "SEL" {
		// A Lifecycle log record that copies a SEL event.
		origin = model.OriginSEL
	}
	location := p.Location
	if location == "" && len(labels) > 0 {
		location = strings.Join(labels, ", ")
	}
	fp := model.Fingerprint(source, fmt.Sprint(e.n), e.stamp, e.id, e.message)
	rec := &fwevent.Record{Event: ev, Flags: res.Flags, Clears: clears, At: at, Count: p.Count, Labels: labels,
		SocketId: p.Socket, ChannelId: p.Channel, ChannelSlotId: p.Position, Rank: p.Rank, Address: p.Address,
		Decoder: source, Method: "bmc", Text: text, Source: source, RawRef: e.ref, Raw: e.raw, Fingerprint: fp,
		Firmware: model.FirmwareRecord{Vendor: res.Vendor, VendorID: key, VendorSeverity: e.severity, Origin: origin, Log: log,
			Location: location, Args: p.Args, Text: e.message, EntryKey: model.Fingerprint(source, fmt.Sprint(e.n), e.stamp),
			Mapping: res.Mapping, MapRef: res.Ref, Basis: res.Basis}}
	if err := fwevent.Emit(r.sink, rec); err != nil {
		if errors.Is(err, fwevent.ErrRecord) {
			return adapter.Unreadable("the record could not be read as a firmware record: %v", err)
		}
		return sinkErr{err}
	}
	return nil
}

// time reads a time of the output in the zone of the options.
func (r *run) time(s string) (time.Time, error) {
	loc := r.opts.Location
	if loc == nil {
		loc, r.noZone = time.UTC, true
	}
	for _, layout := range []string{"01/02/2006 15:04:05", "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
		if t, err := time.ParseInLocation(layout, strings.TrimSpace(s), loc); err == nil {
			if t.Year() < 2000 || t.Year() > 2100 {
				return time.Time{}, errors.New("the year is out of range")
			}
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("%q is no time of the form MM/DD/YYYY HH:MM:SS or YYYY-MM-DD HH:MM:SS", s)
}

// notes says what the report of the source needs to say.
func (r *run) notes() []string {
	var out []string
	if r.memory > 0 {
		out = append(out, fmt.Sprintf("%d %s about memory", r.memory, plural(r.memory, "record", "records")))
	}
	if r.notMemory > 0 {
		out = append(out, fmt.Sprintf("%d %s not about memory left out", r.notMemory, plural(r.notMemory, "record", "records")))
	}
	if r.noZone && r.memory > 0 {
		out = append(out, "the times carry no zone and were read as UTC; --tz names the zone of the iDRAC")
	}
	if len(r.unclassified) > 0 {
		var parts []string
		for k, n := range r.unclassified {
			parts = append(parts, fmt.Sprintf("%s (%d)", k, n))
		}
		sortStrings(parts)
		if len(parts) > 8 {
			parts = append(parts[:8], fmt.Sprintf("and %d more", len(parts)-8))
		}
		out = append(out, "about memory without a class: "+strings.Join(parts, ", "))
	}
	return out
}

func plural(n int, one, other string) string {
	if n == 1 {
		return one
	}
	return other
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// lines splits an input into lines, each at most maxLine bytes.
func lines(data []byte) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), maxLine)
	for sc.Scan() {
		out = append(out, strings.TrimRight(sc.Text(), "\r"))
	}
	return out
}

// dashes says whether a line is a separator of records.
func dashes(line string) bool {
	t := strings.TrimSpace(line)
	return len(t) >= 3 && strings.Trim(t, "-") == ""
}
