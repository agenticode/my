// Package dimmspd reads the SPD EEPROM of the memory modules: from the
// eeprom files the kernel drivers ee1004 (DDR4) and spd5118 (DDR5) give on
// a running host, from the files a collection of "my collect host" wrote,
// and from dump files given to an import. What it saw of the path to the
// SPD is kept as a probe, and when nothing could be read the source says
// why (Reason). It holds no error record: the source is ok, empty or
// absent and changes no finding.
//
// Package internal/decode/spd decodes the bytes.
package dimmspd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/decode/spd"
	"github.com/agenticode/my/internal/model"
)

// Kind is the source kind.
const Kind = "spd"

// Files of a collection.
const (
	ProbeFile = "spd-probe.txt"
	Dir       = "spd"
)

// DebugVirtual reports whether MY_DEBUG holds spd-vm: the probe then runs
// on a virtual machine, for the lab.
func DebugVirtual() bool {
	for _, w := range strings.Split(os.Getenv("MY_DEBUG"), ",") {
		if strings.TrimSpace(w) == "spd-vm" {
			return true
		}
	}
	return false
}

// Live reads the SPD of the running host (or of the root a test made).
type Live struct {
	fsys   FS
	origin string
	opts   LiveOptions
	// MemoryType is the memory type SMBIOS gives, for the reason; "" when
	// not known.
	MemoryType string
}

// NewLive returns the reader of the host whose root is fsys; origin is
// the path of that root ("/").
func NewLive(fsys FS, origin string, opts LiveOptions, memoryType string) *Live {
	if origin == "" {
		origin = "/"
	}
	return &Live{fsys: fsys, origin: origin, opts: opts, MemoryType: memoryType}
}

// Kind is the source kind.
func (a *Live) Kind() string { return Kind }

// Origin is where the SPD devices are listed.
func (a *Live) Origin() string { return path.Join(a.origin, "sys/bus/i2c/devices") }

// Collect probes the host, reads every SPD it can, and keeps the probe and
// the readings.
func (a *Live) Collect(ctx context.Context, sink adapter.Sink) adapter.Report {
	now := a.opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	p, devs := ProbeHost(ctx, a.fsys, a.opts)
	var readings []*model.SPDReading
	for _, d := range devs {
		if d.Path == "" {
			continue
		}
		readings = append(readings, &model.SPDReading{Origin: path.Join(a.origin, d.Path), Bus: d.Bus, Address: d.Addr, Driver: d.Client.Driver,
			Form: model.SPDFormEEPROM, State: d.Client.State, Detail: detail(d.Client), Raw: d.Raw, LastSeen: now})
	}
	return keep(sink, a.Origin(), path.Join(a.origin, "sys/bus/i2c/devices"), now, p, readings, a.MemoryType)
}

// detail writes the facts of a read as key=value words.
func detail(c ProbeClient) string {
	var w []string
	if c.Errno != "" {
		w = append(w, "errno="+c.Errno, "at="+strconv.Itoa(c.At))
	}
	if c.EEPROM >= 0 && c.Read < c.EEPROM && c.Errno == "" {
		w = append(w, "size="+strconv.Itoa(c.EEPROM))
	}
	if c.File != "" {
		w = append(w, "file="+c.File)
	}
	return strings.Join(w, " ")
}

// keep gives the probe and the readings to the sink and makes the report.
func keep(sink adapter.Sink, origin, probeOrigin string, now time.Time, p *Probe, readings []*model.SPDReading, memoryType string) adapter.Report {
	rep := adapter.Report{Kind: Kind, Origin: origin}
	if p != nil {
		if err := sink.Reading(&model.HostReading{Kind: model.ReadingSPDProbe, TakenAt: now, Origin: probeOrigin, Text: p.Text()}); err != nil {
			rep.Status, rep.Detail, rep.SinkErr = model.SourceError, "ledger rejected a record: "+err.Error(), err
			return rep
		}
	}
	var tally adapter.Tally
	read := 0
	for _, r := range readings {
		r := r
		err := adapter.Each(&tally, r.Origin, "", func() error {
			if err := sink.SPD(r); err != nil {
				return err
			}
			if spd.Read(r.State) {
				read++
			} else {
				rep.Notes = append(rep.Notes, fmt.Sprintf("%s: %s", r.Origin, stateNote(r)))
			}
			return nil
		})
		if err != nil {
			rep.Status, rep.Detail, rep.SinkErr = model.SourceError, "ledger rejected a record: "+err.Error(), err
			return rep
		}
	}
	rep.AddTally(&tally)
	rep.Records = len(readings)
	if p != nil && len(p.Clients) > rep.Records {
		rep.Records = len(p.Clients)
	}
	rep.Skipped = rep.Records - read
	switch {
	case read > 0:
		rep.Status = model.SourceOK
	case p != nil:
		code := Reason(p, memoryType)
		rep.Detail = Short(p, code)
		rep.Status = model.SourceAbsent
		if len(p.Clients) > 0 {
			rep.Status = model.SourceEmpty
		}
	default:
		rep.Status = model.SourceEmpty
		rep.Detail = fileShort(readings)
	}
	sort.Strings(rep.Notes)
	return rep
}

func stateNote(r *model.SPDReading) string {
	if r.Detail != "" {
		return r.State + " (" + r.Detail + ")"
	}
	return r.State
}

// fileShort is the detail of a dump source whose files gave no SPD.
func fileShort(readings []*model.SPDReading) string {
	for _, code := range []string{spd.StateType, spd.StateBlank} {
		all := len(readings) > 0
		for _, r := range readings {
			all = all && r.State == code
		}
		if all && code == spd.StateType {
			return Short(nil, ReasonNotDecoded)
		}
		if all {
			return Short(nil, ReasonBlank)
		}
	}
	return Short(nil, ReasonReadFailed)
}

// Copy reads the SPD a collection of "my collect host" holds: its probe
// text (spd-probe.txt) and the files below spd/, nothing under sys. A
// person may put dumps of any form there too.
type Copy struct {
	root       string
	now        time.Time
	memoryType string
}

// Holds tells whether a copy of a host's files below root holds a probe or
// SPD files.
func Holds(root string) bool {
	if fi, err := os.Stat(filepath.Join(root, ProbeFile)); err == nil && !fi.IsDir() {
		return true
	}
	ents, err := os.ReadDir(filepath.Join(root, Dir))
	return err == nil && len(ents) > 0
}

// NewCopy returns the reader of the SPD of a copy of a host's files.
func NewCopy(root string, now time.Time, memoryType string) *Copy {
	return &Copy{root: root, now: now, memoryType: memoryType}
}

// Kind is the source kind.
func (a *Copy) Kind() string { return Kind }

// Origin is the directory of the SPD files.
func (a *Copy) Origin() string { return filepath.Join(a.root, Dir) }

var binName = regexp.MustCompile(`^(\d+)-00([0-9a-fA-F]{2})\.bin$`)

// maxCopyFiles bounds the files of spd/ that are read.
const maxCopyFiles = 64

// Collect reads the probe and the files.
func (a *Copy) Collect(ctx context.Context, sink adapter.Sink) adapter.Report {
	now := a.now
	if now.IsZero() {
		now = time.Now()
	}
	var p *Probe
	probePath := filepath.Join(a.root, ProbeFile)
	if b, err := adapter.ReadFile(os.DirFS(a.root), ProbeFile, 64<<10); err == nil {
		p = ParseProbe(string(b))
	} else if !errors.Is(err, fs.ErrNotExist) {
		_, d := adapter.StatusForError(err)
		return adapter.Report{Kind: Kind, Origin: a.Origin(), Status: model.SourceEmpty, Detail: d}
	}
	clients := map[string]ProbeClient{}
	if p != nil {
		for _, c := range p.Clients {
			clients[c.ID] = c
		}
	}
	ents, _ := os.ReadDir(filepath.Join(a.root, Dir))
	var readings []*model.SPDReading
	var notes []string
	seen := map[string]bool{}
	for i, e := range ents {
		if ctx.Err() != nil {
			break
		}
		if i >= maxCopyFiles {
			notes = append(notes, fmt.Sprintf("%d files of %s were left out: at most %d are read", len(ents)-maxCopyFiles, Dir, maxCopyFiles))
			break
		}
		if e.IsDir() || !e.Type().IsRegular() {
			continue
		}
		file := filepath.Join(a.root, Dir, e.Name())
		if m := binName.FindStringSubmatch(e.Name()); m != nil {
			id := m[1] + "-00" + strings.ToLower(m[2])
			if c, ok := clients[id]; ok {
				seen[id] = true
				raw, err := adapter.ReadFile(os.DirFS(filepath.Join(a.root, Dir)), e.Name(), model.MaxSPDBytes)
				if err != nil {
					notes = append(notes, e.Name()+": "+err.Error())
					continue
				}
				addr, _ := strconv.ParseInt(m[2], 16, 32)
				r := &model.SPDReading{Origin: file, Bus: "i2c-" + m[1], Address: int(addr), Driver: c.Driver, Form: model.SPDFormEEPROM,
					State: c.State, Detail: detail(c), Raw: raw, LastSeen: now}
				if len(raw) > 0 || spd.Read(c.State) {
					r.State = spd.Decode(raw).State
				}
				if len(raw) == 0 {
					r.Raw = nil
				}
				readings = append(readings, r)
				continue
			}
		}
		rs, err := FileReadings(file, now)
		if err != nil {
			notes = append(notes, e.Name()+": "+err.Error())
			continue
		}
		readings = append(readings, rs...)
	}
	// A client the probe read and whose file is missing still has its
	// state.
	if p != nil {
		for _, c := range p.Clients {
			if seen[c.ID] || c.State == ClientUnbound || c.State == ClientNoFile {
				continue
			}
			if c.State == spd.StateDenied || c.State == spd.StateIO || c.State == spd.StateTimeout {
				bus, addr := busOf(c.ID)
				readings = append(readings, &model.SPDReading{Origin: filepath.Join(a.root, Dir, c.ID+".bin"), Bus: bus, Address: addr,
					Driver: c.Driver, Form: model.SPDFormEEPROM, State: c.State, Detail: detail(c), LastSeen: now})
			}
		}
	}
	rep := keep(sink, a.Origin(), probePath, now, p, readings, a.memoryType)
	rep.Notes = append(rep.Notes, notes...)
	return rep
}

func busOf(id string) (string, int) {
	m := clientName.FindStringSubmatch(id)
	if m == nil {
		return "", 0
	}
	addr, _ := strconv.ParseInt(m[2], 16, 32)
	return "i2c-" + m[1], int(addr)
}

// FileReadings reads a dump file in any form of package spd: one reading
// for bytes, one per module for decode-dimms output. A file that is no
// SPD in a form the package reads is taken as raw bytes when it is binary
// and up to 2,048 bytes long, as an eeprom file of a collection is; any
// other gives the error.
func FileReadings(file string, now time.Time) ([]*model.SPDReading, error) {
	b, err := adapter.ReadFile(os.DirFS(filepath.Dir(file)), filepath.Base(file), spd.MaxText)
	if err != nil {
		return nil, err
	}
	in, err := spd.Parse(b)
	if err != nil {
		if spd.IsText(b) || len(b) > model.MaxSPDBytes || len(b) == 0 {
			return nil, err
		}
		in = &spd.Input{Form: spd.FormBinary, Bytes: b, Gap: -1, Back: -1}
	}
	return InputReadings(file, in, now)
}

// InputReadings makes the readings of an input that was read.
func InputReadings(origin string, in *spd.Input, now time.Time) ([]*model.SPDReading, error) {
	switch in.Form {
	case spd.FormDecodeDimms:
		var out []*model.SPDReading
		for i, m := range in.Modules {
			fields, err := json.Marshal(m)
			if err != nil {
				return nil, err
			}
			o := origin
			if len(in.Modules) > 1 {
				o = origin + "#" + strconv.Itoa(i+1)
			}
			out = append(out, &model.SPDReading{Origin: o, Form: model.SPDFormDecodeDimms, State: m.Decoded().State, Fields: string(fields), LastSeen: now})
		}
		return out, nil
	}
	raw := in.Bytes
	if len(raw) > model.MaxSPDBytes {
		raw = raw[:model.MaxSPDBytes]
	}
	form := model.SPDFormBinary
	var w []string
	if in.Form == spd.FormHex {
		form = model.SPDFormHex
		w = append(w, "layout="+in.Layout)
		if in.Gap >= 0 {
			w = append(w, "gap="+strconv.Itoa(in.Gap))
		}
		if in.Back >= 0 {
			w = append(w, "back="+strconv.Itoa(in.Back))
		}
	}
	if in.Gzip {
		w = append(w, "gzip=1")
	}
	return []*model.SPDReading{{Origin: origin, Form: form, State: spd.Decode(raw).State, Detail: strings.Join(w, " "), Raw: raw, LastSeen: now}}, nil
}

// File reads one dump file given to an import.
type File struct {
	path string
	now  time.Time
}

// NewFile returns the reader of a dump file; now is the time its reading
// is recorded at.
func NewFile(path string, now time.Time) *File { return &File{path: path, now: now} }

// Kind is the source kind.
func (a *File) Kind() string { return Kind }

// Origin is the file.
func (a *File) Origin() string { return a.path }

// Collect reads the file.
func (a *File) Collect(ctx context.Context, sink adapter.Sink) adapter.Report {
	now := a.now
	if now.IsZero() {
		now = time.Now()
	}
	rs, err := FileReadings(a.path, now)
	if err != nil {
		// The source is ok, empty or absent and no other: a file that
		// cannot be read gave no SPD.
		st, d := adapter.StatusForError(err)
		if st != model.SourceAbsent {
			st = model.SourceEmpty
		}
		return adapter.Report{Kind: Kind, Origin: a.path, Status: st, Detail: d}
	}
	return keep(sink, a.path, "", now, nil, rs, "")
}
