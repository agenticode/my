// Package smbios reads the memory device records of SMBIOS (type 17): which
// slots the board has, and the manufacturer, part number and serial number
// of the DIMM in each. It accepts the raw tables the kernel exposes under
// /sys/firmware/dmi/tables and the text output of dmidecode.
//
// The raw tables are readable by root only on most systems. Without them
// the ledger has no DIMM identities and falls back to slot placeholders;
// the report says so.
package smbios

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
)

// Kind is the adapter name in the source inventory.
const Kind = "smbios"

// TablesDir is the location of the raw tables below the file system root.
const TablesDir = "sys/firmware/dmi/tables"

// Options control a reading.
type Options struct {
	// Now is the time of the observation; zero means the current time.
	Now time.Time
}

// Adapter reads SMBIOS memory device records from one source.
type Adapter struct {
	origin string
	opts   Options
	read   func(ctx context.Context, a *Adapter, sink adapter.Sink, rep *adapter.Report)
}

func (a *Adapter) Kind() string   { return Kind }
func (a *Adapter) Origin() string { return a.origin }

// Collect reads the source into sink.
func (a *Adapter) Collect(ctx context.Context, sink adapter.Sink) adapter.Report {
	rep := adapter.Report{Kind: Kind, Origin: a.origin}
	if err := ctx.Err(); err != nil {
		rep.Status, rep.Detail = model.SourceError, err.Error()
		return rep
	}
	a.read(ctx, a, sink, &rep)
	return rep
}

func (a *Adapter) now() time.Time {
	if a.opts.Now.IsZero() {
		return time.Now().UTC()
	}
	return a.opts.Now.UTC()
}

// NewTables returns an adapter for the raw SMBIOS tables in directory dir
// of fsys: the files DMI and smbios_entry_point.
func NewTables(fsys fs.FS, dir, origin string, opts Options) *Adapter {
	return &Adapter{origin: origin, opts: opts, read: func(ctx context.Context, a *Adapter, sink adapter.Sink, rep *adapter.Report) {
		table, err := fs.ReadFile(fsys, path.Join(dir, "DMI"))
		if err != nil {
			rep.Status, rep.Detail = adapter.StatusForError(err)
			switch rep.Status {
			case model.SourceUnreadable:
				rep.Detail = "permission denied; the SMBIOS tables are readable as root. Without them DIMM serial numbers are unknown"
			case model.SourceAbsent:
				rep.Detail = "no SMBIOS tables exposed by the kernel"
			}
			return
		}
		if ep, err := fs.ReadFile(fsys, path.Join(dir, "smbios_entry_point")); err == nil {
			if e, err := ParseEntryPoint(ep); err == nil {
				rep.Notes = append(rep.Notes, fmt.Sprintf("SMBIOS %d.%d", e.Major, e.Minor))
			}
		}
		structs, perr := ParseTable(table)
		a.emit(sink, rep, structs)
		if perr != nil && rep.SinkErr == nil {
			rep.Notes = append(rep.Notes, "the table is damaged: "+perr.Error())
			if rep.Records == 0 {
				rep.Status, rep.Detail = model.SourceError, "SMBIOS table is damaged: "+perr.Error()
			} else {
				rep.Status = model.SourcePartial
			}
		}
	}}
}

// NewRoot returns an adapter for the raw tables of the system whose file
// system root is root.
func NewRoot(root string, opts Options) *Adapter {
	return NewTables(os.DirFS(root), TablesDir, filepath.Join(root, filepath.FromSlash(TablesDir)), opts)
}

// emit sends the memory devices and the system identity of a parsed table.
func (a *Adapter) emit(sink adapter.Sink, rep *adapter.Report, structs []Structure) {
	now := a.now()
	var host model.Host
	populated := 0
	for i := range structs {
		s := &structs[i]
		switch s.Type {
		case 1:
			host.SystemVendor, host.SystemProduct = clean(s.str(0x04)), clean(s.str(0x05))
		case 2:
			if host.Board == "" {
				host.Board = strings.TrimSpace(clean(s.str(0x04)) + " " + clean(s.str(0x05)))
			}
		case 17:
			rep.Records++
			m := memoryDevice(s)
			if len(s.Data) < 0x15 {
				rep.Skipped++
				rep.Notes = append(rep.Notes, fmt.Sprintf("skipped handle 0x%04X: memory device structure is %d bytes, too short to hold a locator", s.Handle, len(s.Data)))
				continue
			}
			m.ObservedAt, m.Source = now, Kind
			m.RawRef = fmt.Sprintf("%s#handle 0x%04X", a.origin, s.Handle)
			if m.Populated {
				populated++
			}
			if err := sink.Module(m); err != nil {
				rep.Status, rep.Detail, rep.SinkErr = model.SourceError, "ledger rejected a record: "+err.Error(), err
				return
			}
		}
	}
	if host != (model.Host{}) {
		if err := sink.Host(&host); err != nil {
			rep.Status, rep.Detail, rep.SinkErr = model.SourceError, "ledger rejected a record: "+err.Error(), err
			return
		}
	}
	finish(rep, populated)
}

func finish(rep *adapter.Report, populated int) {
	switch {
	case rep.Records == 0:
		rep.Status, rep.Detail = model.SourceEmpty, "no memory device (type 17) records"
	case rep.Skipped > 0:
		rep.Status = model.SourcePartial
	default:
		rep.Status = model.SourceOK
	}
	if rep.Records > 0 {
		rep.Notes = append(rep.Notes, fmt.Sprintf("%d memory slots, %d populated", rep.Records, populated))
	}
}

// NewDmidecodeFile returns an adapter for a file holding the output of
// dmidecode (all types, or -t 17, or -t memory).
func NewDmidecodeFile(path string, opts Options) *Adapter {
	return newDmidecode(path, opts, func() (io.ReadCloser, error) {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		if fi, err := f.Stat(); err == nil && fi.IsDir() {
			f.Close()
			return nil, fmt.Errorf("%s is a directory", path)
		}
		return f, nil
	})
}

// NewDmidecodeReader returns an adapter for dmidecode output read from r.
func NewDmidecodeReader(r io.Reader, origin string, opts Options) *Adapter {
	return newDmidecode(origin, opts, func() (io.ReadCloser, error) { return io.NopCloser(r), nil })
}

func newDmidecode(origin string, opts Options, open func() (io.ReadCloser, error)) *Adapter {
	return &Adapter{origin: origin, opts: opts, read: func(ctx context.Context, a *Adapter, sink adapter.Sink, rep *adapter.Report) {
		rc, err := open()
		if err != nil {
			rep.Status, rep.Detail = adapter.StatusForError(err)
			return
		}
		defer rc.Close()
		a.dmidecode(rc, sink, rep)
	}}
}

var (
	handleRE = regexp.MustCompile(`^Handle 0x([0-9A-Fa-f]+), DMI type (\d+), (\d+) bytes`)
	sizeRE   = regexp.MustCompile(`^(\d+) ?(kB|KB|MB|GB|TB)$`)
	numRE    = regexp.MustCompile(`^(\d+)`)
	mfgIDRE  = regexp.MustCompile(`^Bank (\d+), Hex 0x([0-9A-Fa-f]{2})$`)
)

// dmidecode parses the text dmidecode prints: one block per structure, a
// "Handle ..." line, a title line, then tab-indented "Key: Value" lines.
func (a *Adapter) dmidecode(r io.Reader, sink adapter.Sink, rep *adapter.Report) {
	now := a.now()
	var host model.Host
	populated := 0
	var (
		typ    = -1
		handle int
		fields map[string]string
		sawAny bool
		denied bool
	)
	flush := func() error {
		defer func() { typ, fields = -1, nil }()
		switch typ {
		case 1:
			host.SystemVendor, host.SystemProduct = clean(fields["Manufacturer"]), clean(fields["Product Name"])
		case 2:
			if host.Board == "" {
				host.Board = strings.TrimSpace(clean(fields["Manufacturer"]) + " " + clean(fields["Product Name"]))
			}
		case 17:
			rep.Records++
			m, why := moduleFromText(fields)
			if why != "" {
				rep.Skipped++
				rep.Notes = append(rep.Notes, fmt.Sprintf("skipped handle 0x%04X: %s", handle, why))
				return nil
			}
			m.Handle = model.Some(handle)
			m.ObservedAt, m.Source = now, Kind
			m.RawRef = fmt.Sprintf("%s#handle 0x%04X", a.origin, handle)
			if m.Populated {
				populated++
			}
			return sink.Module(m)
		}
		return nil
	}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.Contains(line, "Permission denied") || strings.Contains(line, "Can't read memory from /dev/mem") {
			denied = true
		}
		if m := handleRE.FindStringSubmatch(line); m != nil {
			if err := flush(); err != nil {
				rep.Status, rep.Detail, rep.SinkErr = model.SourceError, "ledger rejected a record: "+err.Error(), err
				return
			}
			h, _ := strconv.ParseInt(m[1], 16, 32)
			t, _ := strconv.Atoi(m[2])
			handle, typ, fields, sawAny = int(h), t, map[string]string{}, true
			continue
		}
		if fields == nil || !strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "\t\t") {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		if _, dup := fields[k]; !dup {
			fields[k] = strings.TrimSpace(v)
		}
	}
	if err := flush(); err != nil {
		rep.Status, rep.Detail, rep.SinkErr = model.SourceError, "ledger rejected a record: "+err.Error(), err
		return
	}
	if err := sc.Err(); err != nil {
		rep.Status, rep.Detail = model.SourceError, err.Error()
		return
	}
	if host != (model.Host{}) {
		if err := sink.Host(&host); err != nil {
			rep.Status, rep.Detail, rep.SinkErr = model.SourceError, "ledger rejected a record: "+err.Error(), err
			return
		}
	}
	finish(rep, populated)
	if rep.Records == 0 {
		switch {
		case denied:
			rep.Status, rep.Detail = model.SourceUnreadable, "dmidecode was run without the privilege to read the tables; run it as root"
		case !sawAny:
			rep.Status, rep.Detail = model.SourceError, "no dmidecode structures found in the input"
		}
	}
}

func parseSize(s string) (bytes int64, populated, ok bool) {
	s = strings.TrimSpace(s)
	switch strings.ToLower(s) {
	case "no module installed":
		return 0, false, true
	case "unknown":
		return 0, true, true
	}
	m := sizeRE.FindStringSubmatch(s)
	if m == nil {
		return 0, false, false
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, false, false
	}
	shift := map[string]uint{"kB": 10, "KB": 10, "MB": 20, "GB": 30, "TB": 40}[m[2]]
	return n << shift, true, true
}

func leadingInt(s string) model.Opt[int] {
	m := numRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return model.Opt[int]{}
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n == 0 {
		return model.Opt[int]{}
	}
	return model.Some(n)
}

// moduleFromText builds a module from the fields of a "Memory Device" block.
func moduleFromText(f map[string]string) (*model.Module, string) {
	size, hasSize := f["Size"]
	if !hasSize {
		return nil, "memory device block has no Size field"
	}
	bytes, populated, ok := parseSize(size)
	if !ok {
		return nil, fmt.Sprintf("Size %q is unreadable", size)
	}
	m := &model.Module{Locator: strings.TrimSpace(f["Locator"]), BankLocator: clean(f["Bank Locator"]), Populated: populated}
	if !populated {
		return m, ""
	}
	d := &m.DIMM
	d.SizeBytes = bytes
	d.Manufacturer, d.PartNumber, d.Serial = clean(f["Manufacturer"]), clean(f["Part Number"]), clean(f["Serial Number"])
	d.MemoryType, d.FormFactor, d.Technology = clean(f["Type"]), clean(f["Form Factor"]), clean(f["Memory Technology"])
	detail := f["Type Detail"]
	switch {
	case strings.Contains(detail, "LRDIMM"):
		d.ModuleType = "LRDIMM"
	case strings.Contains(detail, "Registered"):
		d.ModuleType = "RDIMM"
	case strings.Contains(detail, "Unbuffered"):
		d.ModuleType = "UDIMM"
	}
	d.Ranks = leadingInt(f["Rank"])
	d.TotalWidth, d.DataWidth = leadingInt(f["Total Width"]), leadingInt(f["Data Width"])
	d.SpeedMTs = leadingInt(f["Speed"])
	// dmidecode before 3.2 calls the configured speed a clock speed.
	d.ConfiguredSpeedMTs = leadingInt(f["Configured Memory Speed"])
	if !d.ConfiguredSpeedMTs.Valid {
		d.ConfiguredSpeedMTs = leadingInt(f["Configured Clock Speed"])
	}
	if id := mfgIDRE.FindStringSubmatch(f["Module Manufacturer ID"]); id != nil {
		d.ManufacturerID = fmt.Sprintf("bank %s, 0x%s", id[1], strings.ToUpper(id[2]))
	}
	return m, ""
}
