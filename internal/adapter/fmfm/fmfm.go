// Package fmfm reads the logs the OCP Fleet Memory Fault Management (FMFM)
// framework asks a fleet operator to keep, as JSON: the boot log (firmware,
// installed memory, boot time, training failures), the runtime error log
// (one record per error, with the DDR5 ECS registers and the DIMM
// temperature) and the crash log.
//
// The ledger's error event carries the field names of the runtime log
// (ErrorDT, ErrorSeverity, SocketId, Row and so on), so a runtime record
// goes in as it is. The framework document names these fields; the JSON
// schema that fixes how they are nested is not public. This reader
// therefore looks for the fields by name, wherever in a document they are:
// in one object, nested in objects of a record, in a list of records, or
// in several documents one after the other.
package fmfm

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
	"sync"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
)

// Kind is the adapter name in the source inventory.
const Kind = "fmfm"

// Options tell the adapter what the input itself cannot say.
type Options struct {
	// HostID names the host whose records are wanted from a log that
	// holds several. The comparison ignores case. When it is empty, or no
	// record names it, the host of the first record that names one is
	// read; a name no record uses is then the caller's own name for that
	// host. Records of other hosts are left out, and reported as skipped
	// unless the caller asked for the host that was read.
	HostID string
	// Now is the time of the import. Installed memory of a boot record
	// without a boot time is recorded as seen then. Zero means the current
	// time.
	Now time.Time
	// Location is the time zone of times that carry none. Nil means UTC.
	Location *time.Location
}

// Limits of what one run reads.
const (
	maxInput = 256 << 20
	maxRaw   = 8 << 10
	maxDepth = 32
)

// Adapter reads one FMFM log file.
type Adapter struct {
	origin string
	opts   Options
	open   func() (io.ReadCloser, error)

	// The input is read once: the host name is asked for before the
	// records are, and a reader can only be read once.
	once sync.Once
	data []byte
	err  error
}

// NewFile returns an adapter for a log file.
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

// NewReader returns an adapter for a log read from r.
func NewReader(r io.Reader, origin string, opts Options) *Adapter {
	return &Adapter{origin: origin, opts: opts, open: func() (io.ReadCloser, error) { return io.NopCloser(r), nil }}
}

func (a *Adapter) Kind() string   { return Kind }
func (a *Adapter) Origin() string { return a.origin }

var errTooLarge = errors.New("larger than 256 MiB")

func (a *Adapter) read() ([]byte, error) {
	a.once.Do(func() {
		rc, err := a.open()
		if err != nil {
			a.err = err
			return
		}
		defer rc.Close()
		data, err := io.ReadAll(io.LimitReader(rc, maxInput+1))
		switch {
		case err != nil:
			a.err = err
		case len(data) > maxInput:
			a.err = errTooLarge
		default:
			a.data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
		}
	})
	return a.data, a.err
}

// recordMarks are the field names that make a JSON object a record of one
// of the three logs.
var (
	runtimeMarks = []string{"errordt", "ecsmoderegisters"}
	bootMarks    = []string{"currentbootdt", "installedmemory", "installedfirmware", "systemmemoryconfig", "primordialbootdt", "trainingeventlogs"}
	crashMarks   = []string{"systemcrashdt", "oscrashdt"}
)

// Sniff reports whether data looks like an FMFM log. It is used to pick
// the adapter for a file by its content.
func Sniff(data []byte) bool {
	head := bytes.TrimLeft(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), " \t\r\n")
	if len(head) == 0 || (head[0] != '{' && head[0] != '[') {
		return false
	}
	if len(head) > 256<<10 {
		head = head[:256<<10]
	}
	for _, name := range []string{`"ErrorDT"`, `"EcsModeRegisters"`, `"CurrentBootDT"`, `"InstalledMemory"`, `"SystemCrashDT"`, `"OsCrashDT"`} {
		if bytes.Contains(head, []byte(name)) {
			return true
		}
	}
	return false
}

// fold writes a field name without case, spaces, underscores and dashes.
func fold(k string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(k) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// object is a JSON object with folded field names.
type object map[string]any

func folded(m map[string]any) object {
	o := make(object, len(m))
	for k, v := range m {
		if fk := fold(k); fk != "" {
			if _, dup := o[fk]; !dup {
				o[fk] = v
			}
		}
	}
	return o
}

func (o object) has(names []string) bool {
	for _, n := range names {
		if _, ok := o[n]; ok {
			return true
		}
	}
	return false
}

// record is one record found in the input.
type record struct {
	obj object
	// flat holds the scalar fields of the record and of the objects
	// nested in it, by folded name. A field of the record itself wins
	// over a nested one of the same name.
	flat map[string]any
	raw  string
	ref  string
}

// HostHint returns the host the log is about: the name the caller gave,
// or the HostID of the first record that has one.
func (a *Adapter) HostHint() string {
	if a.opts.HostID != "" {
		return a.opts.HostID
	}
	data, err := a.read()
	if err != nil {
		return ""
	}
	r := &run{origin: a.origin}
	r.find(data)
	r.pickHost()
	return r.host
}

// pickHost decides whose records are read: the host the caller asked for
// when the log names it, and the first host the log names otherwise.
func (r *run) pickHost() {
	r.host, r.asked = "", false
	for i := range r.records {
		h := text(r.records[i].flat["hostid"])
		if h == "" {
			continue
		}
		if r.host == "" {
			r.host = h
		}
		if r.opts.HostID != "" && strings.EqualFold(h, r.opts.HostID) {
			r.host, r.asked = h, true
			return
		}
	}
}

// run carries the state of one Collect call.
type run struct {
	ctx    context.Context
	origin string
	opts   Options
	rep    *adapter.Report
	tally  adapter.Tally
	sink   adapter.Sink
	now    time.Time

	records []record
	badDocs []string
	// parts counts the entries of the lists inside boot records (installed
	// memory, training events): each is a record of its own in the count
	// of the report. skipped holds the references of the records and
	// entries that could not be used, each once, however many reasons
	// were found for it.
	parts   int
	skipped map[string]bool

	// host is the HostID whose records are read, as the log writes it;
	// empty when no record names a host. asked says that the caller named
	// this host.
	host       string
	asked      bool
	otherHosts map[string]int
	seen       map[string]int // content of a record -> how often it was seen

	// placed are the modules the boot records of the log list by socket,
	// channel and slot number, in the order of the log. They are reported
	// at the end, when it is known what else the log says about them.
	placed []listedModule
	// ecsPlaces are the places ECS registers were read from.
	ecsPlaces map[[3]int]bool
	clashes   int
	temps     int
	unread    map[string]int
}

// listedModule is a module a boot record lists by numbers.
type listedModule struct {
	place  [3]int
	module model.Module
	// named: the entry has a slot name of its own.
	named bool
	// clash: another entry of the same boot record has the same numbers,
	// so the numbers do not say which of the two a record means.
	clash bool
}

// Collect reads the log into sink.
func (a *Adapter) Collect(ctx context.Context, sink adapter.Sink) adapter.Report {
	rep := adapter.Report{Kind: Kind, Origin: a.origin}
	if err := ctx.Err(); err != nil {
		rep.Status, rep.Detail = model.SourceError, err.Error()
		return rep
	}
	data, err := a.read()
	if err != nil {
		rep.Status, rep.Detail = adapter.StatusForError(err)
		return rep
	}
	r := &run{ctx: ctx, origin: a.origin, opts: a.opts, rep: &rep, sink: sink,
		otherHosts: map[string]int{}, seen: map[string]int{}, ecsPlaces: map[[3]int]bool{}, unread: map[string]int{}}
	r.now = a.opts.Now.UTC()
	if a.opts.Now.IsZero() {
		r.now = time.Now().UTC()
	}
	docs := r.find(data)
	for _, bad := range r.badDocs {
		r.skip(a.origin, bad)
	}
	// A document that cannot be read counts as a record that was looked
	// at, like the records and the entries of their lists.
	count := func() { rep.Records, rep.Skipped = len(r.records)+r.parts+len(r.badDocs), len(r.skipped) }
	count()
	if len(r.records) == 0 {
		rep.Notes = append(rep.Notes, r.tally.Notes()...)
		switch {
		case len(bytes.TrimSpace(data)) == 0:
			rep.Status, rep.Detail = model.SourceEmpty, "the file is empty"
		case docs == 0:
			rep.Status, rep.Detail = model.SourceError, "not readable as JSON"
			if len(r.badDocs) > 0 {
				rep.Detail = r.badDocs[0]
			}
		default:
			rep.Status, rep.Detail = model.SourceEmpty, "no FMFM boot, runtime or crash record found: none of the fields that mark one is in the document"
		}
		return rep
	}

	if err := r.emit(); err != nil {
		var stop stopped
		if errors.As(err, &stop) {
			rep.Status, rep.Detail = model.SourceError, stop.err.Error()
		} else {
			rep.Status, rep.Detail, rep.SinkErr = model.SourceError, "ledger rejected a record: "+err.Error(), err
		}
		return rep
	}
	rep.HostHint = r.host
	if a.opts.HostID != "" {
		rep.HostHint = a.opts.HostID
	}
	count()
	rep.Notes = append(rep.Notes, r.notes()...)
	rep.Notes = append(rep.Notes, r.tally.Notes()...)
	if rep.Skipped > 0 {
		rep.Status = model.SourcePartial
	} else {
		rep.Status = model.SourceOK
	}
	return rep
}

// skip notes that the record or list entry at ref could not be used. The
// reason goes into the notes of the report every time; the record counts
// as skipped once.
func (r *run) skip(ref, reason string) {
	r.tally.Skip(ref, reason)
	if r.skipped == nil {
		r.skipped = map[string]bool{}
	}
	r.skipped[ref] = true
}

// stopped marks the end of a read because its context ended.
type stopped struct{ err error }

func (s stopped) Error() string { return s.err.Error() }

// find parses the input, one JSON document or several one after the
// other, and collects the records in it. It returns the number of
// documents that could be read.
func (r *run) find(data []byte) (docs int) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	for {
		var v any
		err := dec.Decode(&v)
		if err == io.EOF {
			break
		}
		if err != nil {
			r.badDocs = append(r.badDocs, "not readable as JSON: "+err.Error())
			break
		}
		docs++
		r.walk(v, fmt.Sprintf("document %d", docs), 0)
	}
	return docs
}

// walk looks for records in a JSON value.
func (r *run) walk(v any, pos string, depth int) {
	if depth > maxDepth {
		return
	}
	switch x := v.(type) {
	case []any:
		for i, item := range x {
			r.walk(item, fmt.Sprintf("%s[%d]", pos, i), depth+1)
		}
	case map[string]any:
		o := folded(x)
		if o.has(runtimeMarks) || o.has(bootMarks) || o.has(crashMarks) {
			rec := record{obj: o, flat: map[string]any{}, ref: r.origin + "#" + pos}
			flatten(o, rec.flat, 0)
			if b, err := json.Marshal(x); err == nil {
				rec.raw = string(b)
				if len(rec.raw) > maxRaw {
					rec.raw = rec.raw[:maxRaw] + "..."
				}
			}
			r.records = append(r.records, rec)
			return
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			switch x[k].(type) {
			case map[string]any, []any:
				r.walk(x[k], pos+"/"+k, depth+1)
			}
		}
	}
}

// flatten collects the scalar fields of an object and of the objects
// nested in it. Lists are left where they are: they hold records of their
// own kind (installed memory, training events, register values).
func flatten(o object, into map[string]any, depth int) {
	var nested []object
	for k, v := range o {
		switch x := v.(type) {
		case map[string]any:
			nested = append(nested, folded(x))
		case []any:
		case nil:
		default:
			if _, dup := into[k]; !dup || depth == 0 {
				into[k] = v
			}
		}
	}
	if depth >= 4 {
		return
	}
	for _, n := range nested {
		sub := map[string]any{}
		flatten(n, sub, depth+1)
		for k, v := range sub {
			if _, dup := into[k]; !dup {
				into[k] = v
			}
		}
	}
}

// text returns a scalar as text: a string as it is, a number in its
// written form.
func text(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case json.Number:
		return x.String()
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	return ""
}

func (r *run) notes() []string {
	var out []string
	add := func(format string, a ...any) { out = append(out, fmt.Sprintf(format, a...)) }
	if len(r.otherHosts) > 0 {
		names := make([]string, 0, len(r.otherHosts))
		total := 0
		for h, n := range r.otherHosts {
			names = append(names, h)
			total += n
		}
		sort.Strings(names)
		if len(names) > 5 {
			names = append(names[:5], "...")
		}
		which := r.host
		if !r.asked {
			which += ", the first host the log names"
		}
		add("records of other hosts left out: %d (%s); this run read the records of %s", total, strings.Join(names, ", "), which)
	}
	if r.clashes > 0 {
		add("entries of the installed memory that have the socket, channel and slot numbers of another entry of their boot record: %d; these numbers place no records", r.clashes)
	}
	if r.temps > 0 {
		add("records with a DIMM temperature: %d; the temperature is kept in the raw record and not stored as a series", r.temps)
	}
	if len(r.unread) > 0 {
		names := make([]string, 0, len(r.unread))
		for n := range r.unread {
			names = append(names, n)
		}
		sort.Strings(names)
		add("fields that were not read: %s", strings.Join(names, ", "))
	}
	return out
}
