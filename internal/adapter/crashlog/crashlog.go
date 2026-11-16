// Package crashlog reads the kernel log of a boot that crashed, from the
// places where it outlives the crash: the dumps of the persistent store
// (pstore) below /sys/fs/pstore, their archive by systemd-pstore below
// /var/lib/systemd/pstore, and the text log kdump writes below /var/crash.
//
// A panic hands the end of the kernel log to the backend of pstore
// (fs/pstore/platform.c, pstore_dump) in parts of the size the backend
// takes, each with a header line "<reason>#<count> Part<n>". Part 1 holds
// the newest text. The next boot shows each part as a file
// (fs/pstore/inode.c, pstore_mkfile: "<type>-<backend>-<id>"). This
// package puts the parts of a dump in order, Part N first, takes the
// header lines out and hands the text to the kernel log reader
// (internal/adapter/klog), so that the records in it are read as the
// kernel log of the boot that crashed. A line that the journal holds as
// well has the same stamp and text there and is stored once.
//
// It only reads: a file of /sys/fs/pstore is never removed (removing it
// erases the record in the backend, fs/pstore/inode.c, pstore_unlink).
package crashlog

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/klog"
	"github.com/agenticode/my/internal/model"
)

// Kinds of source in the inventory.
const (
	KindPstore = "pstore"
	KindKdump  = "kdump"
)

// Where a host keeps them, below its root.
const (
	// StoreDir is the pstore file system (fs/pstore/inode.c), mounted
	// by systemd at boot.
	StoreDir = "sys/fs/pstore"
	// ArchiveDir is where systemd-pstore copies the files with
	// Storage=external (src/pstore/pstore.c, arg_archivedir).
	ArchiveDir = "var/lib/systemd/pstore"
	// KdumpDir is the default dump path of kdump-utils (kdump.sh,
	// KDUMP_PATH) and of Debian's kdump-tools (KDUMP_COREDIR).
	KdumpDir = "var/crash"
)

// Limits of what is read. A pstore record is a few KiB (the backend's
// buffer, 10240 bytes of kernel log by default: fs/pstore/Kconfig,
// PSTORE_DEFAULT_KMSG_BYTES); a kdump text log holds the whole ring
// buffer of the kernel.
const (
	maxPart  = 16 << 20
	maxDump  = 64 << 20
	maxDepth = 3
)

// Options say what the files themselves cannot.
type Options struct {
	// ModTime takes the modification time of a file as the time of its
	// record where nothing better tells it. On a live host the files of
	// /sys/fs/pstore carry the record time (pstore_mkfile sets it from
	// the backend); a copy of them may not.
	ModTime bool
	// TempDir is handed to the kernel log reader.
	TempDir string
}

// Adapter reads the crash logs below one place.
type Adapter struct {
	kind   string
	path   string
	origin string
	opts   Options
}

// NewDir returns an adapter for the crash logs below a directory: the
// pstore file system, the archive of systemd-pstore, the dump path of
// kdump, or a copy of any of them. kind names it in the inventory.
func NewDir(kind, dir string, opts Options) *Adapter {
	return &Adapter{kind: kind, path: dir, origin: dir, opts: opts}
}

// NewFile returns an adapter for one file: a pstore file, a dmesg.txt of
// systemd-pstore, or the text log of kdump.
func NewFile(kind, path string, opts Options) *Adapter {
	return &Adapter{kind: kind, path: path, origin: path, opts: opts}
}

func (a *Adapter) Kind() string   { return a.kind }
func (a *Adapter) Origin() string { return a.origin }

// File names of the pstore file system: "<type>-<backend>-<id>", and
// ".enc.z" when the kernel could not decompress the record.
var nameRE = regexp.MustCompile(`^(dmesg|console|pmsg|ftrace|mce|rtas|powerpc-[a-z]+)-([A-Za-z0-9_]+)-(\d{1,20})(\.enc\.z)?$`)

// headerRE is the header pstore_dump writes above each part:
// "%s#%d Part%u\n" with the reason of kmsg_dump_reason_str
// (kernel/printk/printk.c).
var headerRE = regexp.MustCompile(`^(Panic|Oops|Emergency|Shutdown|Unknown|NMI)#(\d{1,9}) Part(\d{1,6})\s*$`)

// kdump text logs: vmcore-dmesg.txt of kdump-utils (kdump.sh,
// save_vmcore_dmesg_fs, also vmcore-dmesg-incomplete.txt when the tool
// failed half way) and dmesg.<stamp> of Debian's kdump-tools
// (kdump-config, KDUMP_DMESGFILE).
var kdumpRE = regexp.MustCompile(`^(vmcore-dmesg\.txt|vmcore-dmesg-incomplete\.txt|dmesg\.\d{12})$`)

// part is one file of a dump.
type part struct {
	path    string
	name    string
	typ     string // dmesg, console, ...
	backend string
	id      uint64
	hasID   bool
	encz    bool
	mtime   time.Time

	// From the header line.
	reason string
	count  int
	no     int
	header bool
}

// dump is the text of one crashed boot: the parts of one pstore record in
// reading order, or one text log.
type dump struct {
	kind   string
	name   string // how the inventory names it
	origin string // the klog origin of its lines
	parts  []*part
	reason string
	time   time.Time
	// whole is a text that holds several parts with their headers: a
	// dmesg.txt that systemd-pstore put together.
	whole   bool
	missing []int
	stream  bool // read straight from the file (kdump)
}

// found is what a walk of a place gives.
type found struct {
	dumps     []*dump
	notes     []string
	unreadPar int // files that could not be read
	errs      []string
}

// Collect reads every crash log below the place into sink.
func (a *Adapter) Collect(ctx context.Context, sink adapter.Sink) adapter.Report {
	rep := adapter.Report{Kind: a.kind, Origin: a.origin}
	fi, err := os.Stat(a.path)
	if err != nil {
		rep.Status, rep.Detail = adapter.StatusForError(err)
		return rep
	}
	var f found
	if fi.IsDir() {
		f, err = a.walk()
	} else {
		f, err = a.one(fi)
	}
	if err != nil {
		rep.Status, rep.Detail = adapter.StatusForError(err)
		if rep.Status == model.SourceUnreadable && a.kind == KindPstore {
			rep.Detail += "; the pstore file system is readable as root"
		}
		return rep
	}
	rep.Notes = append(rep.Notes, f.notes...)
	var tally adapter.Tally
	for _, e := range f.errs {
		tally.Skip(a.origin, e)
	}
	read := 0
	for _, d := range f.dumps {
		if err := ctx.Err(); err != nil {
			rep.Status, rep.Detail = model.SourceError, err.Error()
			return rep
		}
		r, ok := a.read(ctx, d, sink, &tally)
		if r.SinkErr != nil {
			rep.Status, rep.Detail, rep.SinkErr = model.SourceError, r.Detail, r.SinkErr
			return rep
		}
		if !ok {
			continue
		}
		read++
		rep.Records += r.Records
		rep.Skipped += r.Skipped
		if rep.HostHint == "" {
			rep.HostHint = r.HostHint
		}
		rep.Notes = append(rep.Notes, a.summary(d, r))
	}
	rep.Skipped += tally.Total()
	rep.Notes = append(rep.Notes, tally.Notes()...)
	switch {
	case read == 0 && len(f.dumps) == 0 && tally.Total() == 0:
		rep.Status = model.SourceEmpty
		if a.kind == KindKdump {
			rep.Detail = "no kdump text log"
		} else {
			rep.Detail = "no dump of the kernel log"
		}
	case read == 0 && tally.Total() > 0:
		rep.Status = model.SourceUnreadable
		rep.Detail = "no dump could be read"
	case rep.Skipped > 0:
		rep.Status = model.SourcePartial
	default:
		rep.Status = model.SourceOK
	}
	return rep
}

// summary says in one note what a dump was and what came of it.
func (a *Adapter) summary(d *dump, r adapter.Report) string {
	var b strings.Builder
	b.WriteString(d.name)
	b.WriteString(": ")
	switch {
	case d.kind == KindKdump:
		b.WriteString("kdump text log")
	case d.reason != "":
		fmt.Fprintf(&b, "%s dump", d.reason)
		if n := len(d.parts); n > 1 || d.whole {
			fmt.Fprintf(&b, " in %d %s", max(n, 1), plural(max(n, 1), "part", "parts"))
		}
	default:
		b.WriteString("console log")
	}
	if len(d.missing) > 0 {
		fmt.Fprintf(&b, ", %s %s missing", plural(len(d.missing), "part", "parts"), joinInts(d.missing))
	}
	if !d.time.IsZero() {
		fmt.Fprintf(&b, ", written %s UTC", d.time.UTC().Format("2006-01-02 15:04:05"))
	}
	for _, n := range r.Notes {
		if strings.HasSuffix(n, " lines read") || strings.HasPrefix(n, "its lines run") {
			b.WriteString("; ")
			b.WriteString(n)
		}
	}
	return b.String()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func joinInts(v []int) string {
	s := make([]string, len(v))
	for i, n := range v {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, ", ")
}

// one makes the dump of a single file given to an import.
func (a *Adapter) one(fi fs.FileInfo) (found, error) {
	var f found
	name := filepath.Base(a.path)
	if kdumpRE.MatchString(name) || a.kind == KindKdump {
		f.dumps = append(f.dumps, a.kdumpLog(a.path, fi))
		return f, nil
	}
	p, ok := a.part(a.path, fi)
	if !ok {
		// A dump under another name: a dmesg.txt, or a pstore file that
		// was renamed when it was copied.
		p = &part{path: a.path, name: name, typ: "dmesg", mtime: fi.ModTime()}
		if name == "dmesg.txt" {
			return a.wholeDumps(p, &f), nil
		}
	}
	if err := p.readHeader(); err != nil {
		return f, err
	}
	switch {
	case p.encz:
		f.notes = append(f.notes, name+": compressed, and the kernel could not decompress it; not read")
	case p.typ == "dmesg" && p.header:
		f.dumps = append(f.dumps, a.group([]*part{p}, &f)...)
	case p.typ == "dmesg" || p.typ == "console":
		f.dumps = append(f.dumps, &dump{kind: KindPstore, name: name, origin: a.path, parts: []*part{p}, time: a.timeOf(p)})
	default:
		f.notes = append(f.notes, fmt.Sprintf("%s: a %s record; not read", name, p.typ))
	}
	return f, nil
}

// walk finds the crash logs below a directory, at most three levels down:
// the pstore file system keeps its files at the top, systemd-pstore files
// them below <timestamp>/<count> (EFI) or <record id> (ERST), and kdump
// writes one directory per dump.
func (a *Adapter) walk() (found, error) {
	var f found
	var parts []*part
	var wholes []*part
	dirsWithParts := map[string]bool{}
	skipped := map[string]int{}
	root := filepath.Clean(a.path)
	// The top directory itself has to be readable: what is closed to the
	// user is the answer.
	if _, err := os.ReadDir(root); err != nil {
		return f, err
	}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p != root {
				f.errs = append(f.errs, fmt.Sprintf("%s: %s", rel(root, p), errText(err)))
				return nil
			}
			return err
		}
		depth := strings.Count(rel(root, p), string(filepath.Separator))
		if d.IsDir() {
			if p != root && depth >= maxDepth {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		name := d.Name()
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		switch {
		case kdumpRE.MatchString(name):
			f.dumps = append(f.dumps, a.kdumpLog(p, fi))
		case name == "dmesg.txt":
			wholes = append(wholes, &part{path: p, name: name, typ: "dmesg", mtime: fi.ModTime()})
		case name == "kexec-dmesg.log":
			// The log of the capture kernel itself: it would enter the
			// ledger as a boot of a few seconds.
			skipped["kexec-dmesg.log, the log of the capture kernel"]++
		case strings.HasPrefix(name, "vmcore") || strings.HasPrefix(name, "dump."):
			skipped["memory dumps (vmcore)"]++
		default:
			pt, ok := a.part(p, fi)
			if !ok {
				return nil
			}
			switch {
			case pt.encz:
				skipped["compressed records the kernel could not decompress (.enc.z)"]++
			case pt.typ == "pmsg" || pt.typ == "ftrace" || pt.typ == "mce" || pt.typ == "rtas" || strings.HasPrefix(pt.typ, "powerpc"):
				skipped[pt.typ+" records"]++
			default:
				parts = append(parts, pt)
				dirsWithParts[filepath.Dir(p)] = true
			}
		}
		return nil
	})
	if err != nil {
		return f, err
	}
	// Read the headers; a file closed to this user is skipped and noted.
	var readable []*part
	for _, p := range parts {
		if err := p.readHeader(); err != nil {
			f.errs = append(f.errs, fmt.Sprintf("%s: %s", rel(root, p.path), errText(err)))
			continue
		}
		readable = append(readable, p)
	}
	var dmesg, other []*part
	for _, p := range readable {
		if p.typ == "dmesg" {
			dmesg = append(dmesg, p)
		} else {
			other = append(other, p)
		}
	}
	f.dumps = append(f.dumps, a.group(dmesg, &f)...)
	for _, p := range other {
		f.dumps = append(f.dumps, &dump{kind: KindPstore, name: rel(root, p.path), origin: p.path, parts: []*part{p}, time: a.timeOf(p)})
	}
	for _, w := range wholes {
		if dirsWithParts[filepath.Dir(w.path)] {
			// systemd-pstore wrote it from the parts beside it.
			skipped["dmesg.txt files that repeat the parts beside them"]++
			continue
		}
		a.wholeDumps(w, &f)
	}
	keys := make([]string, 0, len(skipped))
	for k := range skipped {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		f.notes = append(f.notes, fmt.Sprintf("%d %s: not read", skipped[k], k))
	}
	for _, d := range f.dumps {
		if d.name == "" {
			d.name = rel(root, d.origin)
		} else if filepath.IsAbs(d.name) {
			d.name = rel(root, d.name)
		}
	}
	sort.SliceStable(f.dumps, func(i, j int) bool { return f.dumps[i].name < f.dumps[j].name })
	return f, nil
}

func rel(root, p string) string {
	r, err := filepath.Rel(root, p)
	if err != nil || r == "." {
		return filepath.Base(p)
	}
	return r
}

func errText(err error) string {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return "not readable as this user"
	case errors.Is(err, fs.ErrNotExist):
		return "gone while it was read"
	}
	var tl *adapter.TooLargeError
	if errors.As(err, &tl) {
		return fmt.Sprintf("larger than %d bytes", tl.Limit)
	}
	return err.Error()
}

// part parses the name of a pstore file.
func (a *Adapter) part(path string, fi fs.FileInfo) (*part, bool) {
	m := nameRE.FindStringSubmatch(filepath.Base(path))
	if m == nil {
		return nil, false
	}
	p := &part{path: path, name: filepath.Base(path), typ: m[1], backend: m[2], encz: m[4] != "", mtime: fi.ModTime()}
	if id, err := strconv.ParseUint(m[3], 10, 64); err == nil {
		p.id, p.hasID = id, true
	}
	return p, true
}

// readHeader reads the first line of a part and takes what its header says.
func (p *part) readHeader() error {
	f, err := os.Open(p.path)
	if err != nil {
		return err
	}
	defer f.Close()
	line, err := bufio.NewReaderSize(f, 512).ReadString('\n')
	if err != nil && err != io.EOF {
		return err
	}
	if m := headerRE.FindStringSubmatch(strings.TrimRight(line, "\r\n")); m != nil {
		p.reason = m[1]
		p.count, _ = strconv.Atoi(m[2])
		p.no, _ = strconv.Atoi(m[3])
		p.header = true
	}
	return nil
}

// efiTime returns the time an EFI record id holds: efi-pstore.c,
// generic_id, makes it (timestamp * 100 + part) * 1000 + count.
func efiTime(id uint64) (ts uint64, part, count int) {
	return id / 100000, int(id / 1000 % 100), int(id % 1000)
}

func efiBackend(b string) bool { return b == "efi_pstore" || b == "efi" }

// group puts the dmesg parts into dumps.
//
//   - EFI variables: the id holds the time of the dump, the part and the
//     count (efiTime); the parts of a dump share time and count.
//   - ERST: every record of a boot gets the next id (cper_next_record_id
//     in drivers/firmware/efi/cper.c: seeded with the time of its first
//     use, then counted up), and pstore_dump writes Part 1 first, so the
//     parts of one dump have ids that follow each other. The header tells
//     two dumps of one boot apart.
//   - Other backends (ramoops keeps one part per dump): a file is a dump.
//
// Within a dump the parts are read from the highest part number down:
// Part 1 holds the newest text, and so the text runs in the order it was
// written. systemd-pstore rebuilds dmesg.txt the same way.
func (a *Adapter) group(parts []*part, f *found) []*dump {
	type key struct {
		dir, backend string
		a, b         uint64
		reason       string
	}
	groups := map[key][]*part{}
	var order []key
	add := func(k key, p *part) {
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], p)
	}
	var erst []*part
	for _, p := range parts {
		dir := filepath.Dir(p.path)
		switch {
		case efiBackend(p.backend) && p.hasID:
			ts, _, count := efiTime(p.id)
			add(key{dir: dir, backend: p.backend, a: ts, b: uint64(count)}, p)
		case p.backend == "erst" && p.hasID:
			erst = append(erst, p)
		default:
			add(key{dir: dir, backend: p.backend, a: p.id, b: uint64(len(order))}, p)
		}
	}
	// ERST: runs of ids that follow each other, cut where the header
	// starts another dump.
	sort.Slice(erst, func(i, j int) bool {
		if di, dj := filepath.Dir(erst[i].path), filepath.Dir(erst[j].path); di != dj {
			return di < dj
		}
		return erst[i].id < erst[j].id
	})
	var start *part
	var prev *part
	for _, p := range erst {
		if prev == nil || filepath.Dir(p.path) != filepath.Dir(prev.path) || p.id != prev.id+1 ||
			(p.header && prev.header && (p.count != prev.count || p.reason != prev.reason || p.no != prev.no+1)) {
			start = p
		}
		add(key{dir: filepath.Dir(p.path), backend: "erst", a: start.id}, p)
		prev = p
	}

	var out []*dump
	for _, k := range order {
		ps := groups[k]
		sort.SliceStable(ps, func(i, j int) bool {
			ni, nj := ps[i].no, ps[j].no
			if !ps[i].header || !ps[j].header {
				// Without headers the id stands for the part.
				if efiBackend(ps[i].backend) {
					_, ni, _ = efiTime(ps[i].id)
					_, nj, _ = efiTime(ps[j].id)
				} else {
					return ps[i].id > ps[j].id
				}
			}
			return ni > nj
		})
		// Part 1 names the dump: it is the newest text and the id the
		// kernel gave first.
		first := ps[len(ps)-1]
		d := &dump{kind: KindPstore, name: first.path, origin: first.path, parts: ps, reason: first.reason}
		if len(ps) > 1 {
			d.origin = fmt.Sprintf("%s+%d", first.path, len(ps)-1)
		}
		if first.reason == "" {
			for _, p := range ps {
				if p.reason != "" {
					d.reason = p.reason
					break
				}
			}
		}
		top := 0
		have := map[int]bool{}
		for _, p := range ps {
			n := p.no
			if !p.header && efiBackend(p.backend) && p.hasID {
				_, n, _ = efiTime(p.id)
			}
			have[n] = true
			top = max(top, n)
		}
		for n := 1; n <= top; n++ {
			if !have[n] {
				d.missing = append(d.missing, n)
			}
		}
		d.time = a.timeOf(first)
		out = append(out, d)
	}
	return out
}

// timeOf returns the time of the record of a part: the time an EFI id
// holds, else the modification time when the caller says it is the
// record time (the live pstore file system, pstore_mkfile).
func (a *Adapter) timeOf(p *part) time.Time {
	if efiBackend(p.backend) && p.hasID {
		if ts, _, _ := efiTime(p.id); ts > 0 && ts < 1<<34 {
			return time.Unix(int64(ts), 0).UTC()
		}
	}
	if a.opts.ModTime && !p.mtime.IsZero() {
		return p.mtime.UTC()
	}
	return time.Time{}
}

// wholeDumps reads a dmesg.txt that systemd-pstore put together: the parts
// with their headers, Part N first. The EFI archive names its directory
// after the time of the dump (src/pstore/pstore.c, process_dmesg_files:
// the record id without its last five digits).
func (a *Adapter) wholeDumps(w *part, f *found) found {
	d := &dump{kind: KindPstore, name: w.path, origin: w.path, parts: []*part{w}, whole: true}
	// <archive>/<timestamp>/<count>/dmesg.txt
	if count := filepath.Base(filepath.Dir(w.path)); len(count) == 3 {
		if ts, err := strconv.ParseUint(filepath.Base(filepath.Dir(filepath.Dir(w.path))), 10, 64); err == nil && ts > 0 && ts < 1<<34 {
			d.time = time.Unix(int64(ts), 0).UTC()
		}
	}
	if d.time.IsZero() && a.opts.ModTime {
		d.time = w.mtime.UTC()
	}
	if err := w.readHeader(); err == nil {
		d.reason = w.reason
	}
	f.dumps = append(f.dumps, d)
	return *f
}

// kdumpLog makes the dump of a text log of kdump: one file, read as it is.
func (a *Adapter) kdumpLog(path string, fi fs.FileInfo) *dump {
	d := &dump{kind: KindKdump, name: path, origin: path, parts: []*part{{path: path, name: filepath.Base(path), mtime: fi.ModTime()}}, stream: true}
	// The file is written by the capture kernel after the crash, so its
	// time is later than the crash by the time that took: the last choice
	// for the start of the boot.
	d.time = fi.ModTime().UTC()
	return d
}

// read hands the text of a dump to the kernel log reader.
func (a *Adapter) read(ctx context.Context, d *dump, sink adapter.Sink, tally *adapter.Tally) (adapter.Report, bool) {
	var text []byte
	var err error
	if d.stream {
		text, err = head(d.parts[0].path, maxDump)
	} else {
		text, err = d.text()
	}
	if err != nil {
		tally.Skip(d.origin, errText(err))
		return adapter.Report{}, false
	}
	if len(bytes.TrimSpace(text)) == 0 {
		tally.Skip(d.origin, "holds no kernel log lines")
		return adapter.Report{}, false
	}
	kopts := klog.Options{Location: time.UTC, TempDir: a.opts.TempDir}
	// The start of the crashed boot. The kernel log reader takes it from
	// a machine check record's own TIME field, then from a capture time,
	// then from the RTC line of the boot. The time of the record is a
	// capture time of a log that ends near the crash, and it is given only
	// when the text has neither of the two others.
	if !anchored(text) && !d.time.IsZero() {
		kopts.CapturedAt = d.time
	}
	bs := &bootSink{Sink: sink}
	var r adapter.Report
	if d.stream {
		r = klog.NewFile(d.parts[0].path, kopts).Collect(ctx, bs)
	} else {
		r = klog.NewReader(bytes.NewReader(text), d.origin, kopts).Collect(ctx, bs)
	}
	if r.SinkErr != nil {
		return r, false
	}
	if r.Status == model.SourceUnreadable || r.Status == model.SourceError {
		tally.Skip(d.origin, r.Detail)
		return r, false
	}
	if d.kind == KindKdump || d.reason == "Panic" {
		if err := a.panicRecord(d, text, bs.last, sink); err != nil {
			r.Status, r.Detail, r.SinkErr = model.SourceError, "ledger rejected a record: "+err.Error(), err
			return r, false
		}
	}
	return r, true
}

// bootSink passes every record on and keeps the last boot the kernel log
// reader made of the text.
type bootSink struct {
	adapter.Sink
	last *model.Boot
}

func (b *bootSink) Boot(x *model.Boot) error {
	c := *x
	b.last = &c
	return b.Sink.Boot(x)
}

// panicRecord stores the fact that the boot of a dump ended in a panic:
// a pstore dump written for a panic (the header says "Panic"), or a kdump
// text log, which the capture kernel writes after a crash. The record is
// Informational: it is no error and counts in no total. It carries no
// text of the panic, whose message can be about anything on the host; the
// ledger keeps the kernel lines about memory errors and machine checks
// only. A hardware error that the text shows is a stop of the boot, which
// outranks it (store.BootOutcomes).
func (a *Adapter) panicRecord(d *dump, text []byte, boot *model.Boot, sink adapter.Sink) error {
	e := &model.Event{
		Kind: model.KindError, ErrorSeverity: model.SevInformational, ErrorType: model.ErrorTypePanic,
		Source: d.kind + "/panic", RawRef: d.origin, Count: 1, Grade: model.GradeC,
		// The same dump read from two places is one record.
		Fingerprint: model.Fingerprint("crashlog-panic", d.kind, model.Fingerprint(string(text))),
	}
	switch {
	case boot != nil && boot.StartQuality == model.TimeRelative:
		// The boot is not placed in time: the record stays in it.
		e.ErrorDT, e.TimeQuality, e.BootID = boot.LastSeen, model.TimeRelative, boot.BootID
	case boot != nil && !boot.LastSeen.IsZero():
		e.ErrorDT, e.TimeQuality = boot.LastSeen, model.TimeEstimated
	case !d.time.IsZero():
		e.ErrorDT, e.TimeQuality = d.time, model.TimeEstimated
	default:
		// Nothing dates the dump.
		return nil
	}
	if e.ErrorDT.IsZero() {
		return nil
	}
	return sink.Event(e)
}

// text puts the parts of a dump together without their header lines.
func (d *dump) text() ([]byte, error) {
	var out bytes.Buffer
	for _, p := range d.parts {
		b, err := head(p.path, maxPart)
		if err != nil {
			return nil, err
		}
		if out.Len()+len(b) > maxDump {
			return nil, &adapter.TooLargeError{Name: p.path, Limit: maxDump}
		}
		// Records of some backends end in zero bytes; the text ends there.
		if i := bytes.IndexByte(b, 0); i >= 0 {
			b = b[:i]
		}
		for _, line := range bytes.SplitAfter(b, []byte("\n")) {
			if headerRE.Match(bytes.TrimRight(line, "\r\n")) {
				continue
			}
			out.Write(line)
		}
		if out.Len() > 0 && out.Bytes()[out.Len()-1] != '\n' {
			out.WriteByte('\n')
		}
	}
	return out.Bytes(), nil
}

// head reads at most limit bytes of a file and fails beyond.
func head(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, &adapter.TooLargeError{Name: path, Limit: limit}
	}
	return b, nil
}

// The lines the kernel log reader takes the start of a boot from: the
// record of __print_mce ("PROCESSOR %u:%x TIME %llu SOCKET %u APIC %x",
// arch/x86/kernel/cpu/mce/core.c) and the RTC line of
// drivers/rtc/class.c, rtc_hctosys ("setting system clock to %ptR UTC
// (%lld)").
var (
	mceTimeRE = regexp.MustCompile(`PROCESSOR \d+:[0-9a-fA-F]+ TIME [1-9]\d* SOCKET \d+`)
	rtcRE     = regexp.MustCompile(`setting system clock to \S+(?: \S+)? UTC \(\d{9,11}\)`)
)

// anchored reports whether a text tells the start of its boot itself.
func anchored(text []byte) bool {
	return mceTimeRE.Match(text) || rtcRE.Match(text)
}

// Sniff tells a crash log by its name or its first line: a pstore file
// ("dmesg-erst-7300000000000000001"), a file that starts with the header
// of a pstore dump, or a text log of kdump. It returns the kind, or "".
func Sniff(name string, head []byte) string {
	base := filepath.Base(name)
	switch {
	case kdumpRE.MatchString(base):
		return KindKdump
	case nameRE.MatchString(base):
		return KindPstore
	}
	line, _, _ := bytes.Cut(head, []byte("\n"))
	if headerRE.Match(bytes.TrimRight(line, "\r")) {
		return KindPstore
	}
	return ""
}

// Kinds returns the kinds of crash log below a directory, at most three
// levels down: pstore files or a dmesg.txt of systemd-pstore, and text logs
// of kdump.
func Kinds(dir string) []string {
	var pstore, kdump bool
	root := filepath.Clean(dir)
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && strings.Count(rel(root, p), string(filepath.Separator)) >= maxDepth {
				return fs.SkipDir
			}
			return nil
		}
		switch name := d.Name(); {
		case kdumpRE.MatchString(name):
			kdump = true
		case nameRE.MatchString(name):
			pstore = true
		case name == "dmesg.txt":
			if f, err := os.Open(p); err == nil {
				h := make([]byte, 128)
				n, _ := io.ReadFull(f, h)
				f.Close()
				if Sniff(name, h[:n]) == KindPstore {
					pstore = true
				}
			}
		}
		if pstore && kdump {
			return fs.SkipAll
		}
		return nil
	})
	var out []string
	if pstore {
		out = append(out, KindPstore)
	}
	if kdump {
		out = append(out, KindKdump)
	}
	return out
}
