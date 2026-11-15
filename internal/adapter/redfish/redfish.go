// Package redfish reads what a BMC serves over Redfish, from files: log
// entries (LogEntry), memory modules (Memory) and the system they belong to
// (ComputerSystem). It takes one JSON file, or a directory tree of them as
// a mockup recorder or a crawl of the service leaves it.
//
// Log entries become error events and actions. Memory resources become the
// inventory: the slots of the board, the identity of the DIMM in each, and
// the mark the BMC puts on a module it reports as failed or disabled.
// Vendor extensions (Oem) are not interpreted; the whole resource is kept
// as the raw record.
//
// The adapter does not talk to a BMC. It reads what was saved.
package redfish

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
)

// Kind is the adapter name in the source inventory.
const Kind = "redfish"

// Options tell the adapter what the input itself cannot say.
type Options struct {
	// Now is when the dump was made. Memory resources carry no time: the
	// state they show is recorded as seen at this time. Zero means the
	// current time.
	Now time.Time
	// ModTimeAsObserved uses the latest modification time of the files as
	// the time of the dump when Now is not set. It is right for files
	// saved from the BMC and left alone since.
	ModTimeAsObserved bool
}

// Limits of what one run reads.
const (
	maxFile  = 64 << 20
	maxTotal = 512 << 20
	maxFiles = 50000
	maxRaw   = 8 << 10
	maxDepth = 64
)

// document is one file of the dump.
type document struct {
	name string
	data []byte
}

// Adapter reads one Redfish dump.
type Adapter struct {
	origin string
	opts   Options
	load   func(ctx context.Context) (docs []document, modTime time.Time, notes []string, err error)
}

// New returns an adapter for a dump at path: one JSON file, or a directory
// whose files are read whatever their names are, as long as they hold JSON.
func New(path string, opts Options) *Adapter {
	return &Adapter{origin: path, opts: opts, load: func(ctx context.Context) ([]document, time.Time, []string, error) {
		fi, err := os.Stat(path)
		if err != nil {
			return nil, time.Time{}, nil, err
		}
		if !fi.IsDir() {
			f, err := os.Open(path)
			if err != nil {
				return nil, time.Time{}, nil, err
			}
			defer f.Close()
			data, err := readLimited(f, maxFile)
			if err != nil {
				return nil, time.Time{}, nil, err
			}
			return []document{{name: "", data: data}}, fi.ModTime(), nil, nil
		}
		return loadTree(ctx, os.DirFS(path))
	}}
}

// NewFS returns an adapter for a dump that is a directory tree in fsys.
// origin names it in reports and raw references.
func NewFS(fsys fs.FS, origin string, opts Options) *Adapter {
	return &Adapter{origin: origin, opts: opts, load: func(ctx context.Context) ([]document, time.Time, []string, error) {
		return loadTree(ctx, fsys)
	}}
}

// NewReader returns an adapter for one JSON document read from r.
func NewReader(r io.Reader, origin string, opts Options) *Adapter {
	return &Adapter{origin: origin, opts: opts, load: func(context.Context) ([]document, time.Time, []string, error) {
		data, err := readLimited(r, maxFile)
		if err != nil {
			return nil, time.Time{}, nil, err
		}
		return []document{{name: "", data: data}}, time.Time{}, nil, nil
	}}
}

func (a *Adapter) Kind() string   { return Kind }
func (a *Adapter) Origin() string { return a.origin }

var errTooLarge = errors.New("larger than 64 MiB")

func readLimited(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errTooLarge
	}
	return data, nil
}

// looksLikeJSON reports whether a file starts like a JSON document.
func looksLikeJSON(head []byte) bool {
	head = bytes.TrimLeft(bytes.TrimPrefix(head, []byte("\xef\xbb\xbf")), " \t\r\n")
	return len(head) > 0 && (head[0] == '{' || head[0] == '[')
}

// loadTree reads every JSON file below the root of fsys.
func loadTree(ctx context.Context, fsys fs.FS) (docs []document, modTime time.Time, notes []string, err error) {
	var total int64
	var notJSON, unreadable, tooLarge int
	walkErr := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			if p == "." {
				return err
			}
			unreadable++
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		if len(docs) >= maxFiles {
			return fmt.Errorf("more than %d files: not a dump of one BMC", maxFiles)
		}
		f, err := fsys.Open(p)
		if err != nil {
			unreadable++
			return nil
		}
		defer f.Close()
		head := make([]byte, 512)
		n, _ := io.ReadFull(f, head)
		if !looksLikeJSON(head[:n]) {
			notJSON++
			return nil
		}
		rest, err := readLimited(f, maxFile)
		if errors.Is(err, errTooLarge) {
			tooLarge++
			return nil
		}
		if err != nil {
			unreadable++
			return nil
		}
		total += int64(n + len(rest))
		if total > maxTotal {
			return errors.New("more than 512 MiB of JSON: not a dump of one BMC")
		}
		if fi, err := d.Info(); err == nil && fi.ModTime().After(modTime) {
			modTime = fi.ModTime()
		}
		docs = append(docs, document{name: p, data: append(head[:n:n], rest...)})
		return nil
	})
	if walkErr != nil {
		return nil, time.Time{}, nil, walkErr
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].name < docs[j].name })
	if notJSON > 0 {
		notes = append(notes, fmt.Sprintf("%d files that hold no JSON were left out", notJSON))
	}
	if unreadable > 0 {
		notes = append(notes, fmt.Sprintf("%d files or directories could not be read", unreadable))
	}
	if tooLarge > 0 {
		notes = append(notes, fmt.Sprintf("%d files are larger than 64 MiB and were left out", tooLarge))
	}
	return docs, modTime, notes, nil
}

// Sniff reports whether data looks like a Redfish resource or a collection
// of them. It is used to pick the adapter for a file by its content.
func Sniff(data []byte) bool {
	if !looksLikeJSON(data) {
		return false
	}
	head := data
	if len(head) > 64<<10 {
		head = head[:64<<10]
	}
	for _, mark := range []string{`"@odata.type"`, `"@odata.id"`, `"@odata.context"`} {
		if bytes.Contains(head, []byte(mark)) {
			return true
		}
	}
	return false
}

// object is a JSON object.
type object = map[string]any

// resource is a Redfish resource found in the dump.
type resource struct {
	obj  object
	id   string // the @odata.id, or a position in the file
	ref  string // the raw reference
	file string
}

// str returns a string property, trimmed. Other types give "".
func str(o object, key string) string {
	s, _ := o[key].(string)
	return strings.TrimSpace(s)
}

// integer returns a number property that holds a whole number. A number
// given as a string is accepted.
func integer(o object, key string) (int64, bool) {
	switch v := o[key].(type) {
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return n, true
		}
		if f, err := v.Float64(); err == nil && f == float64(int64(f)) {
			return int64(f), true
		}
	case string:
		var n json.Number = json.Number(strings.TrimSpace(v))
		if i, err := n.Int64(); err == nil {
			return i, true
		}
	}
	return 0, false
}

func child(o object, key string) object {
	c, _ := o[key].(object)
	return c
}

// odataType returns the resource type of an object: "LogEntry" for
// "#LogEntry.v1_15_0.LogEntry".
func odataType(o object) string {
	t := strings.TrimPrefix(str(o, "@odata.type"), "#")
	if i := strings.IndexByte(t, '.'); i > 0 {
		return t[:i]
	}
	return t
}

// kindOf says which of the resources this adapter reads an object is. An
// object without a type is recognised by the properties only that kind of
// resource has, because some tools strip the type when they save a log.
func kindOf(o object) string {
	switch t := odataType(o); t {
	case "LogEntry", "Memory", "ComputerSystem", "MemoryMetrics":
		return t
	case "":
		_, hasEntryType := o["EntryType"]
		_, hasCreated := o["Created"]
		_, hasMessage := o["Message"]
		if hasEntryType && (hasCreated || hasMessage) {
			return "LogEntry"
		}
		_, hasLocator := o["DeviceLocator"]
		_, hasLocation := o["MemoryLocation"]
		_, hasCapacity := o["CapacityMiB"]
		_, hasType := o["MemoryDeviceType"]
		if (hasLocator || hasLocation) && (hasCapacity || hasType) {
			return "Memory"
		}
	}
	return ""
}

// run carries the state of one Collect call.
type run struct {
	ctx    context.Context
	origin string
	rep    *adapter.Report
	tally  adapter.Tally
	now    time.Time

	seen    map[string]bool
	entries []resource
	modules []resource
	systems []resource
	metrics int
	badDocs int

	// locators maps the URI of a memory resource to the name of its slot,
	// places the socket, channel and slot numbers of one to that name.
	locators map[string]string
	places   map[[3]int]string
}

// Collect reads the dump into sink.
func (a *Adapter) Collect(ctx context.Context, sink adapter.Sink) adapter.Report {
	rep := adapter.Report{Kind: Kind, Origin: a.origin}
	if err := ctx.Err(); err != nil {
		rep.Status, rep.Detail = model.SourceError, err.Error()
		return rep
	}
	docs, modTime, notes, err := a.load(ctx)
	if err != nil {
		rep.Status, rep.Detail = adapter.StatusForError(err)
		return rep
	}
	rep.Notes = append(rep.Notes, notes...)
	r := &run{ctx: ctx, origin: a.origin, rep: &rep, seen: map[string]bool{}, locators: map[string]string{}, places: map[[3]int]string{}}
	switch {
	case !a.opts.Now.IsZero():
		r.now = a.opts.Now.UTC()
	case a.opts.ModTimeAsObserved && !modTime.IsZero():
		r.now = modTime.UTC()
	default:
		r.now = time.Now().UTC()
	}

	for i := range docs {
		if err := ctx.Err(); err != nil {
			rep.Status, rep.Detail = model.SourceError, err.Error()
			return rep
		}
		r.scan(&docs[i])
	}
	if len(docs) > 1 || r.badDocs > 0 {
		rep.Notes = append(rep.Notes, fmt.Sprintf("%d JSON files read", len(docs)))
	}
	rep.Records = len(r.entries) + len(r.modules) + len(r.systems)

	err = r.emit(sink)
	if err != nil {
		var stop stopped
		if errors.As(err, &stop) {
			rep.Status, rep.Detail = model.SourceError, stop.err.Error()
		} else {
			rep.Status, rep.Detail, rep.SinkErr = model.SourceError, "ledger rejected a record: "+err.Error(), err
		}
		return rep
	}
	rep.Skipped = r.tally.Total()
	rep.Notes = append(rep.Notes, r.tally.Notes()...)
	switch {
	case len(docs) == 0:
		rep.Status, rep.Detail = model.SourceEmpty, "no JSON files"
	case rep.Records == 0 && r.badDocs == len(docs):
		rep.Status, rep.Detail = model.SourceError, "no file could be read as JSON"
	case rep.Records == 0:
		rep.Status, rep.Detail = model.SourceEmpty, "no log entries, memory resources or systems found"
	case rep.Skipped > 0:
		rep.Status = model.SourcePartial
	default:
		rep.Status = model.SourceOK
	}
	return rep
}

// stopped marks the end of a read because its context ended.
type stopped struct{ err error }

func (s stopped) Error() string { return s.err.Error() }

// scan parses one file and collects the resources in it.
func (r *run) scan(d *document) {
	name := r.origin
	if d.name != "" {
		name = r.origin + "/" + d.name
	}
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(d.data, []byte("\xef\xbb\xbf"))))
	dec.UseNumber()
	values := 0
	for {
		var v any
		err := dec.Decode(&v)
		if err == io.EOF {
			break
		}
		if err != nil {
			r.badDocs++
			r.tally.Skip(name, "not readable as JSON: "+err.Error())
			return
		}
		values++
		r.walk(v, name, d.name, fmt.Sprintf("value %d", values), 0)
	}
}

// walk looks for resources in a JSON value. Collections, expanded
// members, arrays of resources and maps of them are all walked through.
func (r *run) walk(v any, name, file, pos string, depth int) {
	if depth > maxDepth {
		return
	}
	switch x := v.(type) {
	case []any:
		for i, item := range x {
			r.walk(item, name, file, fmt.Sprintf("%s[%d]", pos, i), depth+1)
		}
	case object:
		if kind := kindOf(x); kind != "" {
			id := str(x, "@odata.id")
			key := kind + " " + id
			if id == "" {
				id = pos
				key = kind + " " + name + " " + pos
			}
			if kind == "MemoryMetrics" {
				if !r.seen[key] {
					r.seen[key] = true
					r.metrics++
				}
				return
			}
			if !r.seen[key] {
				r.seen[key] = true
				res := resource{obj: x, id: id, ref: name + "#" + id, file: file}
				switch kind {
				case "LogEntry":
					r.entries = append(r.entries, res)
				case "Memory":
					r.modules = append(r.modules, res)
				case "ComputerSystem":
					r.systems = append(r.systems, res)
				}
			}
			if kind != "ComputerSystem" {
				return // nothing this adapter reads is nested in these
			}
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			switch x[k].(type) {
			case object, []any:
				r.walk(x[k], name, file, pos+"/"+k, depth+1)
			}
		}
	}
}

// raw renders a resource as compact JSON for the raw record.
func raw(o object) string {
	b, err := json.Marshal(o)
	if err != nil {
		return ""
	}
	if len(b) > maxRaw {
		return string(b[:maxRaw]) + "..."
	}
	return string(b)
}

// lastSegment returns the last path segment of a URI, unescaped.
func lastSegment(uri string) string {
	uri = strings.TrimRight(uri, "/")
	seg := path.Base(uri)
	if u, err := url.PathUnescape(seg); err == nil {
		seg = u
	}
	return seg
}

// emit turns the resources into ledger records: systems and modules first,
// because log entries point at memory resources.
func (r *run) emit(sink adapter.Sink) error {
	for i := range r.systems {
		if err := r.system(sink, &r.systems[i]); err != nil {
			return err
		}
	}
	populated := 0
	marks := map[string]int{}
	for i := range r.modules {
		ok, err := r.module(sink, &r.modules[i], marks)
		if err != nil {
			return err
		}
		if ok {
			populated++
		}
	}
	if n := len(r.modules); n > 0 {
		r.rep.Notes = append(r.rep.Notes, fmt.Sprintf("%d memory slots, %d populated", n, populated))
	}
	for _, k := range []string{"disabled", "failed", "warning"} {
		if n := marks[k]; n > 0 {
			r.rep.Notes = append(r.rep.Notes, fmt.Sprintf("%d modules are reported as %s by the BMC", n, k))
		}
	}
	if r.metrics > 0 {
		r.rep.Notes = append(r.rep.Notes, fmt.Sprintf("%d MemoryMetrics resources were not read: their error counts are not in the ledger", r.metrics))
	}
	return r.logs(sink)
}

// system records the identity of the machine.
func (r *run) system(sink adapter.Sink, res *resource) error {
	o := res.obj
	if h := str(o, "HostName"); h != "" && r.rep.HostHint == "" {
		r.rep.HostHint = h
	}
	host := model.Host{SystemVendor: str(o, "Manufacturer"), SystemProduct: str(o, "Model"), FirstSeen: r.now, LastSeen: r.now}
	if host.SystemVendor == "" && host.SystemProduct == "" {
		return nil
	}
	return sink.Host(&host)
}
