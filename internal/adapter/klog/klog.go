// Package klog reads the kernel log: dmesg output in its various time
// formats, syslog files, /dev/kmsg records and journalctl -k -o json.
//
// It extracts EDAC error lines, firmware error records printed by GHES,
// machine check records, page offlining messages, the Xid lines of the
// NVIDIA GPU driver that report on GPU memory, and the boot banner. The
// kernel log is a lossy source: the ring buffer wraps and some messages are
// rate limited. It complements the rasdaemon database and does not replace it.
package klog

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/decode/cper"
	"github.com/agenticode/my/internal/decode/edac"
	"github.com/agenticode/my/internal/decode/mce"
	"github.com/agenticode/my/internal/decode/virt"
	"github.com/agenticode/my/internal/model"
)

// Kind is the adapter name in the source inventory.
const Kind = "klog"

// Options tell the adapter what the input itself cannot say.
type Options struct {
	// CapturedAt is when the log was captured. dmesg output carries only
	// seconds since boot; with a capture time the boot is placed at
	// CapturedAt minus the last stamp and times are marked estimated.
	// Without it such times stay relative to boot.
	CapturedAt time.Time
	// ModTimeAsCapture uses the modification time of a file as CapturedAt
	// when CapturedAt is not set. It is right for a file written by
	// "dmesg > file" and wrong for a file whose time was changed in transit.
	ModTimeAsCapture bool
	// BootTime is the exact start of the boot the end of the input belongs
	// to, when the caller knows it (on a live host, from /proc/stat).
	BootTime time.Time
	// BootID is the kernel boot ID of that boot, when known.
	BootID string
	// Location is the time zone of times that carry none (syslog files,
	// dmesg -T). The default is UTC.
	Location *time.Location
}

// Adapter reads one kernel log.
type Adapter struct {
	origin string
	open   func() (io.ReadCloser, time.Time, error)
	opts   Options
	// path is the file behind the adapter, when it reads one.
	path string
}

// NewFile returns an adapter for a kernel log file.
func NewFile(path string, opts Options) *Adapter {
	return &Adapter{origin: path, path: path, opts: opts, open: func() (io.ReadCloser, time.Time, error) {
		f, err := os.Open(path)
		if err != nil {
			return nil, time.Time{}, err
		}
		fi, err := f.Stat()
		if err != nil {
			f.Close()
			return nil, time.Time{}, err
		}
		if fi.IsDir() {
			f.Close()
			return nil, time.Time{}, fmt.Errorf("%s is a directory", path)
		}
		return f, fi.ModTime(), nil
	}}
}

// NewReader returns an adapter for a kernel log read from r, for example
// the output of a command. origin names it in reports and raw references.
func NewReader(r io.Reader, origin string, opts Options) *Adapter {
	return &Adapter{origin: origin, opts: opts, open: func() (io.ReadCloser, time.Time, error) {
		return io.NopCloser(r), time.Time{}, nil
	}}
}

func (a *Adapter) Kind() string   { return Kind }
func (a *Adapter) Origin() string { return a.origin }

// hintTail is how much of the end of a file is looked at for a host name.
const hintTail = 256 << 10

// HostHint returns the host name the latest lines of a log file carry, or
// "" when the log names no host: syslog files and journal exports name the
// host on every line, dmesg output does not. The first lines of a boot are
// filed under "localhost", so the end of the file is what tells.
func (a *Adapter) HostHint() string {
	if a.path == "" {
		return ""
	}
	f, err := os.Open(a.path)
	if err != nil {
		return ""
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	off := max(fi.Size()-hintTail, 0)
	buf := make([]byte, fi.Size()-off)
	if _, err := io.ReadFull(io.NewSectionReader(f, off, int64(len(buf))), buf); err != nil {
		return ""
	}
	lines := strings.Split(string(buf), "\n")
	if off > 0 && len(lines) > 0 {
		lines = lines[1:] // the first line of the tail is cut
	}
	loc := a.opts.Location
	if loc == nil {
		loc = time.UTC
	}
	fr := framer{loc: loc, ref: a.opts.CapturedAt}
	fallback := ""
	// Forward, so that the framer learns what kind of file this is; the
	// last name that is a name wins.
	name := ""
	for i, raw := range lines {
		raw = strings.TrimRight(raw, "\r")
		var l line
		if strings.HasPrefix(strings.TrimLeft(raw, " \t"), `{"`) {
			var err error
			if l, err = frameJSON(i+1, raw); err != nil {
				continue
			}
		} else {
			var ok bool
			if l, ok = fr.frame(i+1, raw); !ok {
				continue
			}
		}
		switch {
		case l.host == "":
		case unnamedHost(l.host):
			fallback = l.host
		default:
			name = l.host
		}
	}
	if name == "" {
		return fallback
	}
	return name
}

// maxLine bounds the length of one input line.
const maxLine = 1 << 20

// Collect reads the log into sink.
func (a *Adapter) Collect(ctx context.Context, sink adapter.Sink) adapter.Report {
	rep := adapter.Report{Kind: Kind, Origin: a.origin}
	rc, mtime, err := a.open()
	if err != nil {
		rep.Status, rep.Detail = adapter.StatusForError(err)
		return rep
	}
	defer rc.Close()
	opts := a.opts
	if opts.CapturedAt.IsZero() && opts.ModTimeAsCapture {
		opts.CapturedAt = mtime
	}
	if opts.Location == nil {
		opts.Location = time.UTC
	}
	p := &parser{ctx: ctx, sink: sink, rep: &rep, origin: a.origin, opts: opts, ghes: map[string]*ghesBlock{}}
	p.fr = framer{loc: opts.Location, ref: opts.CapturedAt}

	if err := p.run(rc); err != nil {
		var se sinkError
		if errors.As(err, &se) {
			rep.Status, rep.Detail, rep.SinkErr = model.SourceError, "ledger rejected a record: "+se.err.Error(), se.err
		} else {
			rep.Status, rep.Detail = adapter.StatusForError(err)
		}
		return rep
	}

	rep.Records = p.records
	rep.Skipped = p.tally.Total()
	rep.Notes = append(rep.Notes, fmt.Sprintf("%d lines read", p.lineNo))
	if note := p.coverage(); note != "" {
		rep.Notes = append(rep.Notes, note)
	}
	if p.notMemory > 0 {
		rep.Notes = append(rep.Notes, fmt.Sprintf("%d machine checks are not memory errors and were left out", p.notMemory))
	}
	if p.otherSections > 0 {
		rep.Notes = append(rep.Notes, fmt.Sprintf("%d firmware error sections are not memory errors and were left out", p.otherSections))
	}
	if p.relative > 0 {
		rep.Notes = append(rep.Notes, fmt.Sprintf("%d records have only seconds since boot: no capture time or boot time was given, so their dates are unknown", p.relative))
	}
	if p.estimated > 0 {
		rep.Notes = append(rep.Notes, fmt.Sprintf("%d records have times estimated from seconds since boot and the capture time", p.estimated))
	}
	if p.bigPages != "" {
		rep.Notes = append(rep.Notes, p.bigPages)
	}
	if p.injected > 0 {
		rep.Notes = append(rep.Notes, fmt.Sprintf("%d page offlines were asked for by a program (madvise or the hwpoison injector): they follow no error the hardware reported", p.injected))
	}
	for _, inj := range injectorMarks {
		if p.injectors[inj.name] {
			rep.Notes = append(rep.Notes, fmt.Sprintf("%s was set up in a boot of this log: records of that boot may come from a test", inj.name))
		}
	}
	if note := otherXIDNote(p.otherXID); note != "" {
		rep.Notes = append(rep.Notes, note)
	}
	rep.Notes = append(rep.Notes, p.tally.Notes()...)
	switch {
	case rep.Records == 0:
		rep.Status = model.SourceEmpty
	case rep.Skipped > 0:
		rep.Status = model.SourcePartial
	default:
		rep.Status = model.SourceOK
	}
	return rep
}

// stampOf returns the time a line of the input carries: a wall-clock time,
// seconds since boot, or both.
func (p *parser) stampOf(raw string) (l line, ok bool) {
	if strings.HasPrefix(strings.TrimLeft(raw, " \t"), `{"`) {
		l, err := frameJSON(0, raw)
		return l, err == nil && (!l.wall.IsZero() || l.hasMono)
	}
	fr := p.fr // a copy: the framer keeps what it learned about the file
	l, ok = fr.frame(0, raw)
	return l, ok && (!l.wall.IsZero() || l.hasMono)
}

// coverage says when the first and the last line of the input were
// written. A finding that says "no errors" speaks of the time from the
// first line on and of no time before it: a journal that kept two weeks
// says nothing about the months before, and a ring buffer that has wrapped
// nothing about the start of the boot. The last line is not the end of
// what a log of a running host covers: a kernel that has been silent for
// a month has a last line of a month ago.
func (p *parser) coverage() string {
	first, ok1 := p.stampOf(p.firstRaw)
	last, ok2 := p.stampOf(p.lastRaw)
	if !ok1 || !ok2 {
		return ""
	}
	const layout = "2006-01-02 15:04"
	wall := func(l line) time.Time {
		switch {
		case !l.wall.IsZero():
			return l.wall
		case l.hasMono && !p.opts.BootTime.IsZero() && p.segs <= 1:
			// One boot, and the caller knows when it started.
			return p.opts.BootTime.Add(l.mono)
		}
		return time.Time{}
	}
	from, to := wall(first), wall(last)
	var note string
	switch {
	case !from.IsZero() && !to.IsZero():
		note = fmt.Sprintf("its lines run from %s to %s UTC", from.UTC().Format(layout), to.UTC().Format(layout))
	case first.hasMono && last.hasMono && p.segs <= 1:
		note = fmt.Sprintf("its lines run from %s to %s after boot", sinceBoot(first.mono), sinceBoot(last.mono))
	default:
		return ""
	}
	// A log of one boot that does not start with the boot has lost its
	// beginning: the ring buffer wrapped, or the file was cut.
	if p.segs <= 1 && first.hasMono && first.mono > lateStart {
		note += fmt.Sprintf("; the first is from %s after boot, and what the kernel said before that is not in it", sinceBoot(first.mono))
	}
	return note
}

// machineName tidies the name of the machine in the DMI line of the kernel,
// which is "vendor product/board". A board that only repeats the product,
// as virtual machines and some servers report it, is left out.
func machineName(s string) string {
	s = strings.TrimSpace(s)
	i := strings.LastIndexByte(s, '/')
	if i <= 0 || i == len(s)-1 {
		return strings.TrimSuffix(s, "/")
	}
	if board := s[i+1:]; strings.HasSuffix(s[:i], " "+board) || s[:i] == board {
		return s[:i]
	}
	return s
}

// lateStart is how long after boot the first line of a kernel log may be
// stamped before the log counts as cut. The first lines of a boot carry
// stamps of a few seconds at most.
const lateStart = time.Minute

// sinceBoot writes a time since boot in the unit that fits it.
func sinceBoot(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%.1f days", d.Hours()/24)
	case d >= 2*time.Hour:
		return fmt.Sprintf("%.1f hours", d.Hours())
	case d >= 2*time.Minute:
		return fmt.Sprintf("%.0f minutes", d.Minutes())
	}
	return fmt.Sprintf("%.0f s", d.Seconds())
}

type sinkError struct{ err error }

func (e sinkError) Error() string { return e.err.Error() }

// segment is one boot within the input.
type segment struct {
	index  int
	bootID string // kernel boot ID, from the journal

	lastMono time.Duration
	hasMono  bool
	lastWall time.Time

	// start is the boot time, known once a line carries both a wall-clock
	// time and seconds since boot.
	start        time.Time
	startQuality model.TimeQuality
	// mceStart and rtcStart are boot times derived from a machine check
	// record's own wall-clock time and from the RTC line of the boot.
	mceStart time.Time
	rtcStart time.Time

	banner  string
	kernel  string
	bios    string
	system  string
	cmdline string

	pageShift uint
	records   int

	// gpus maps a PCI address to the GPU the driver named there in this
	// boot; lastGPU is the address of the latest such line, which the
	// serial number line that follows belongs to.
	gpus    map[string]*gpuIdent
	lastGPU string

	// injecting maps a page frame number to the line that announced its
	// offline as asked for by a program.
	injecting map[uint64]injection
}

// injection is an announced software injection of a memory failure.
type injection struct {
	n   int    // line number
	how string // "madvise" or "the hwpoison injector"
}

// injectionReach is how many lines after its announcement the offline of
// an injected page is expected. The kernel prints the two within a few
// lines of each other.
const injectionReach = 200

// pending is a record of the current boot whose time cannot be resolved
// before the end of the boot is seen.
type pending struct {
	ev      *model.Event
	act     *model.Action
	gpu     *model.GPUMem
	mono    time.Duration
	hasMono bool
}

// gpuIdent is what the driver printed about the GPU at a PCI address.
type gpuIdent struct{ uuid, serial string }

type ghesBlock struct {
	first line
	lastN int
	body  []string
	raw   []string
}

type mceBlock struct {
	first                         line
	raw                           []string
	bank                          int
	status, addr, misc, synd, ipd uint64
}

type amdBlock struct {
	first                    line
	raw                      []string
	lastN                    int
	hasHead                  bool
	bank                     int
	status, addr, ipid, synd uint64
	ext                      string
	edacFP                   string
}

type parser struct {
	ctx    context.Context
	sink   adapter.Sink
	rep    *adapter.Report
	origin string
	opts   Options
	fr     framer
	tally  adapter.Tally

	lineNo  int
	records int
	// firstRaw and lastRaw are the first and the last line of the input,
	// kept to say what time the input covers.
	firstRaw, lastRaw string
	// seen counts the records of each stamp and text, to tell equal
	// lines apart.
	seen          map[string]int
	injected      int
	injectors     map[string]bool
	otherXID      map[int]int
	notMemory     int
	otherSections int
	relative      int
	estimated     int
	bigPages      string

	seg     *segment
	segs    int
	pend    []pending
	ghes    map[string]*ghesBlock
	mce     *mceBlock
	amd     *amdBlock
	lastGH  *tie
	hostDMI string
	// hypervisor is what the kernel of the latest boot in the log says it
	// runs under ("Hypervisor detected: KVM"), or "".
	hypervisor string
}

// tie remembers a record a following line may be a second report of.
type tie struct {
	fp    string
	n     int
	class string
}

func (p *parser) run(r io.Reader) error {
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		raw, tooLong, err := readLine(br)
		if err != nil && err != io.EOF {
			return err
		}
		if raw != "" || err == nil {
			p.lineNo++
			if !tooLong && strings.TrimSpace(raw) != "" {
				if p.firstRaw == "" {
					p.firstRaw = raw
				}
				p.lastRaw = raw
			}
			if tooLong {
				// A line that is not read is counted like a record that
				// was looked at: what is skipped is part of the count.
				p.records++
				p.tally.Skip(fmt.Sprintf("%s#L%d", p.origin, p.lineNo), "line is longer than 1 MiB")
			} else if herr := p.handle(raw); herr != nil {
				return herr
			}
			if p.lineNo%4096 == 0 {
				if cerr := p.ctx.Err(); cerr != nil {
					return cerr
				}
			}
		}
		if err == io.EOF {
			break
		}
	}
	if err := p.ctx.Err(); err != nil {
		return err
	}
	return p.closeSegment(true)
}

// readLine returns the next line without its line end. A line longer than
// maxLine is consumed and reported as too long.
func readLine(br *bufio.Reader) (s string, tooLong bool, err error) {
	var buf []byte
	for {
		chunk, e := br.ReadSlice('\n')
		if len(buf)+len(chunk) <= maxLine {
			buf = append(buf, chunk...)
		} else {
			tooLong = true
		}
		if e == bufio.ErrBufferFull {
			continue
		}
		return strings.TrimRight(string(buf), "\r\n"), tooLong, e
	}
}

// quickMono finds the seconds-since-boot stamp of a text line without
// parsing the whole line. It is applied to every line, to follow the end of
// each boot and to notice when the stamps start over.
func quickMono(raw string) (time.Duration, bool) {
	s := raw
	switch {
	case len(s) > 0 && s[0] == '[':
	case len(s) > 0 && s[0] == '<':
		i := strings.IndexByte(s, '>')
		if i < 0 || i > 4 {
			return 0, false
		}
		s = s[i+1:]
	case len(s) > 0 && s[0] >= '0' && s[0] <= '9' && strings.IndexByte(s, ';') > 0 && strings.Count(s[:strings.IndexByte(s, ';')], ",") >= 3:
		// /dev/kmsg: priority,sequence,microseconds,flags;message
		f := strings.SplitN(s[:strings.IndexByte(s, ';')], ",", 4)
		us, err := strconv.ParseInt(f[2], 10, 64)
		if err != nil || us < 0 || us > maxUptimeSeconds*1e6 {
			return 0, false
		}
		return time.Duration(us) * time.Microsecond, true
	default:
		i := strings.Index(s, " kernel: [")
		if i < 0 || i > 96 {
			return 0, false
		}
		s = s[i+9:]
	}
	if len(s) == 0 || s[0] != '[' {
		return 0, false
	}
	j := strings.IndexByte(s, ']')
	if j < 9 || j > 24 {
		return 0, false
	}
	num := strings.TrimLeft(s[1:j], " ")
	dot := strings.IndexByte(num, '.')
	if dot <= 0 || len(num)-dot-1 != 6 {
		return 0, false
	}
	sec, err1 := strconv.ParseInt(num[:dot], 10, 64)
	usec, err2 := strconv.ParseInt(num[dot+1:], 10, 64)
	if err1 != nil || err2 != nil || sec < 0 || sec > maxUptimeSeconds {
		return 0, false
	}
	return time.Duration(sec)*time.Second + time.Duration(usec)*time.Microsecond, true
}

func (p *parser) newSegment() {
	p.segs++
	p.seg = &segment{index: p.segs, pageShift: 12}
}

// trackMono follows the stamps of the current boot. Stamps that start over
// mean the input continues with the next boot.
func (p *parser) trackMono(mono time.Duration) error {
	if p.seg == nil {
		p.newSegment()
	}
	if p.seg.hasMono && mono+time.Second < p.seg.lastMono {
		if err := p.closeSegment(false); err != nil {
			return err
		}
		p.newSegment()
	}
	if !p.seg.hasMono || mono > p.seg.lastMono {
		p.seg.lastMono = mono
	}
	p.seg.hasMono = true
	return nil
}

func (p *parser) handle(raw string) error {
	trimmed := strings.TrimLeft(raw, " \t")
	if strings.HasPrefix(trimmed, `{"`) {
		if !candidate(raw) {
			return nil
		}
		l, err := frameJSON(p.lineNo, raw)
		if err != nil {
			p.records++
			p.tally.Skip(p.ref(&l), "journal entry is not valid JSON")
			return nil
		}
		if !l.kernel {
			return nil
		}
		if p.seg == nil || (l.bootID != "" && p.seg.bootID != l.bootID) {
			if p.seg != nil {
				if err := p.closeSegment(false); err != nil {
					return err
				}
			}
			p.newSegment()
			p.seg.bootID = l.bootID
		}
		if l.hasMono && (!p.seg.hasMono || l.mono > p.seg.lastMono) {
			p.seg.lastMono, p.seg.hasMono = l.mono, true
		}
		return p.dispatch(&l)
	}

	if mono, ok := quickMono(raw); ok {
		if err := p.trackMono(mono); err != nil {
			return err
		}
	}
	if !candidate(raw) {
		return nil
	}
	l, ok := p.fr.frame(p.lineNo, raw)
	if !ok || !l.kernel {
		return nil
	}
	if p.seg == nil {
		p.newSegment()
	}
	return p.dispatch(&l)
}

func (p *parser) ref(l *line) string {
	return fmt.Sprintf("%s#L%d", p.origin, l.n)
}

func (p *parser) refRange(first *line, lastN int) string {
	if lastN > first.n {
		return fmt.Sprintf("%s#L%d-L%d", p.origin, first.n, lastN)
	}
	return p.ref(first)
}

// fingerprint identifies a record by the kernel's own stamp and text, so
// that the same line read from dmesg and from the journal is one record.
//
// The same text with the same stamp again is another record: a cell that
// fails six times within a second gives six equal lines, and the forms
// that date a line to the second (dmesg -T, syslog) give them equal stamps
// as well. Such lines are told apart by their order.
func (p *parser) fingerprint(class string, l *line, extra string) string {
	var fp string
	switch {
	case l.hasMono:
		fp = model.Fingerprint(Kind, class, "m", strconv.FormatInt(int64(l.mono/time.Microsecond), 10), l.msg, extra)
	case !l.wall.IsZero():
		fp = model.Fingerprint(Kind, class, "w", strconv.FormatInt(l.wall.Unix(), 10), l.msg, extra)
	default:
		return model.Fingerprint(Kind, class, "n", p.origin, strconv.Itoa(l.n), l.msg, extra)
	}
	if p.seen == nil {
		p.seen = map[string]int{}
	}
	n := p.seen[fp]
	p.seen[fp] = n + 1
	if n > 0 {
		return model.Fingerprint(fp, "again", strconv.Itoa(n))
	}
	return fp
}

func (p *parser) dispatch(l *line) error {
	// The name of the host is the one its latest lines carry. The first
	// messages of a boot are logged before the host has its name: the
	// journal files them under "localhost".
	if l.host != "" && (p.rep.HostHint == "" || unnamedHost(p.rep.HostHint) || !unnamedHost(l.host)) {
		p.rep.HostHint = l.host
	}
	seg := p.seg
	if !l.wall.IsZero() {
		if l.wall.After(seg.lastWall) {
			seg.lastWall = l.wall
		}
		// Messages printed before the log daemon started all carry the
		// time it read them, which is later than boot plus their stamp.
		// The earliest estimate over all lines is the boot time.
		start := l.bootStart
		if start.IsZero() && l.hasMono {
			start = l.wall.Add(-l.mono)
		}
		if !start.IsZero() && (seg.startQuality == "" || start.Before(seg.start)) {
			seg.start, seg.startQuality = start, model.TimeExact
		}
	}
	if err := p.expire(l.n); err != nil {
		return err
	}

	msg := l.msg
	if m := ghesTagRE.FindStringSubmatch(msg); m != nil {
		return p.ghesLine(l, m[1], m[2])
	}
	if i := strings.Index(msg, hwErrMark); i >= 0 {
		return p.hwLine(l, strings.TrimSpace(msg[i+len(hwErrMark):]))
	}
	if isEDACLine(msg) {
		// The EDAC line of a firmware error follows its GHES block.
		if err := p.flushGHES(); err != nil {
			return err
		}
		if err := p.flushMCE(); err != nil {
			return err
		}
		return p.edacLine(l)
	}
	// Any other record ends an open machine check block.
	if err := p.flushMCE(); err != nil {
		return err
	}
	if err := p.flushAMD(); err != nil {
		return err
	}

	if strings.Contains(msg, "NVRM: ") {
		return p.nvrmLine(l)
	}

	if m := linuxVersionRE.FindStringSubmatch(msg); m != nil {
		// A second banner in formats without seconds since boot is the
		// only sign of a reboot.
		if seg.banner != "" && p.seg.bootID == "" {
			if err := p.closeSegment(false); err != nil {
				return err
			}
			p.newSegment()
			seg = p.seg
			if !l.wall.IsZero() {
				seg.lastWall = l.wall
				if l.hasMono {
					seg.start, seg.startQuality = l.wall.Add(-l.mono), model.TimeExact
				}
			}
			if l.hasMono {
				seg.lastMono, seg.hasMono = l.mono, true
			}
		}
		seg.banner, seg.kernel = msg, m[1]
		// What the kernel of an earlier boot ran under says nothing about
		// this one: a disk can move from a virtual machine to a physical one.
		p.hypervisor = ""
		// What the GPU driver said before this boot about which GPU sits
		// where does not carry over.
		seg.gpus, seg.lastGPU = nil, ""
		if !l.wall.IsZero() && !l.hasMono && seg.startQuality == "" {
			// The banner is the first message of a boot.
			seg.start, seg.startQuality = l.wall, model.TimeExact
		}
		if has64KPages(m[1]) {
			seg.pageShift = 16
		}
		p.records++
		seg.records++
		return nil
	}
	if v := virt.FromKernelLine(msg); v != "" {
		p.hypervisor = v
		return nil
	}
	if m := dmiRE.FindStringSubmatch(msg); m != nil {
		seg.system, seg.bios = machineName(m[1]), m[2]
		p.hostDMI = seg.system
		p.records++
		return nil
	}
	if m := cmdlineRE.FindStringSubmatch(msg); m != nil {
		if seg.cmdline == "" {
			seg.cmdline = m[1]
		}
		p.records++
		return nil
	}
	if m := rtcRE.FindStringSubmatch(msg); m != nil && l.hasMono {
		if unix, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			seg.rtcStart = time.Unix(unix, 0).UTC().Add(-l.mono)
		}
		return nil
	}
	if m := injectRE.FindStringSubmatch(msg); m != nil {
		if pfn, err := strconv.ParseUint(strings.TrimPrefix(m[2], "0x"), 16, 44); err == nil {
			if seg.injecting == nil {
				seg.injecting = map[uint64]injection{}
			}
			how := "madvise"
			if m[1] == "at" {
				how = "the hwpoison injector"
			}
			seg.injecting[pfn] = injection{n: l.n, how: how}
		}
		return nil
	}
	for _, inj := range injectorMarks {
		if strings.Contains(msg, inj.mark) {
			if p.injectors == nil {
				p.injectors = map[string]bool{}
			}
			p.injectors[inj.name] = true
			return nil
		}
	}
	if o, ok := parseOffline(msg); ok {
		p.records++
		seg.records++
		if inj, ok := seg.injecting[o.pfn]; ok && o.method == "hard" {
			delete(seg.injecting, o.pfn)
			if l.n-inj.n <= injectionReach {
				o.detail += "; asked for through " + inj.how
				p.injected++
			}
		}
		a := &model.Action{
			Kind: model.ActionPageOffline, Method: o.method, Result: o.result,
			OsPhysicalAddress: model.Some(o.pfn << seg.pageShift), Detail: o.detail,
			Source: Kind + "/offline", RawRef: p.ref(l), Raw: l.msg,
			Fingerprint: p.fingerprint("offline", l, ""),
		}
		return p.emit(nil, a, l)
	}
	return nil
}

// nvrmLine handles a line of the NVIDIA GPU driver: an Xid error, or the
// identity of the GPU at a PCI address.
func (p *parser) nvrmLine(l *line) error {
	seg := p.seg
	msg := l.msg
	if m := gpuAtRE.FindStringSubmatch(msg); m != nil {
		if seg.gpus == nil {
			seg.gpus = map[string]*gpuIdent{}
		}
		seg.lastGPU = pciAddress(m[1])
		seg.gpus[seg.lastGPU] = &gpuIdent{uuid: m[2]}
		return nil
	}
	if m := gpuSerialRE.FindStringSubmatch(msg); m != nil {
		if id := seg.gpus[seg.lastGPU]; id != nil && model.UsableSerial(m[1]) {
			id.serial = m[1]
		}
		return nil
	}
	if !strings.Contains(msg, "NVRM: Xid") {
		return nil
	}
	p.records++
	seg.records++
	m := xidRE.FindStringSubmatch(msg)
	if m == nil {
		p.tally.Skip(p.ref(l), "Xid line is unreadable")
		return nil
	}
	code, err := strconv.Atoi(m[2])
	if err != nil {
		p.tally.Skip(p.ref(l), "Xid line is unreadable")
		return nil
	}
	if _, memory := MemoryXIDs[code]; !memory {
		if p.otherXID == nil {
			p.otherXID = map[int]int{}
		}
		p.otherXID[code]++
		return nil
	}
	g := &model.GPUMem{
		Kind: model.GPUXID, XID: model.Some(code), XIDText: strings.TrimSpace(xidProcRE.ReplaceAllString(m[3], "")),
		PCIBus: pciAddress(m[1]), Source: Kind + "/xid", RawRef: p.ref(l), Raw: l.msg,
		Fingerprint: p.fingerprint("xid", l, ""),
	}
	if id := seg.gpus[g.PCIBus]; id != nil {
		g.GPUUUID, g.GPUSerial = id.uuid, id.serial
	}
	switch {
	case !l.wall.IsZero():
		return p.sendGPU(g, l.wall, model.TimeExact, seg.bootID)
	case l.hasMono && seg.startQuality != "":
		return p.sendGPU(g, seg.start.Add(l.mono), seg.startQuality, seg.bootID)
	}
	p.pend = append(p.pend, pending{gpu: g, mono: l.mono, hasMono: l.hasMono})
	return nil
}

func (p *parser) sendGPU(g *model.GPUMem, t time.Time, q model.TimeQuality, bootID string) error {
	if y := t.UTC().Year(); y < 1970 || y > 9999 {
		p.tally.Skip(g.RawRef, "time is out of range")
		return nil
	}
	switch q {
	case model.TimeRelative:
		p.relative++
	case model.TimeEstimated:
		p.estimated++
	}
	g.ObservedAt, g.TimeQuality, g.BootID = t, q, bootID
	if err := p.sink.GPU(g); err != nil {
		return sinkError{err}
	}
	return nil
}

// otherXIDNote says which Xid records were left out because they are not
// about GPU memory.
func otherXIDNote(counts map[int]int) string {
	if len(counts) == 0 {
		return ""
	}
	codes := make([]int, 0, len(counts))
	total := 0
	for c, n := range counts {
		codes = append(codes, c)
		total += n
	}
	sort.Slice(codes, func(i, j int) bool {
		if counts[codes[i]] != counts[codes[j]] {
			return counts[codes[i]] > counts[codes[j]]
		}
		return codes[i] < codes[j]
	})
	var parts []string
	for i, c := range codes {
		if i == 8 {
			parts = append(parts, "...")
			break
		}
		parts = append(parts, fmt.Sprintf("Xid %d x%d", c, counts[c]))
	}
	return fmt.Sprintf("%d Xid records are not about GPU memory and were left out (%s)", total, strings.Join(parts, ", "))
}

// unnamedHost reports whether a host name in a log line is the name a
// system has before it was given one.
func unnamedHost(name string) bool {
	switch strings.ToLower(name) {
	case "localhost", "localhost.localdomain", "(none)":
		return true
	}
	return false
}

// has64KPages reports whether a kernel release is known to be built with
// 64 KiB pages: kernels whose name says so, and the aarch64 kernels of
// Enterprise Linux 7 and 8.
func has64KPages(release string) bool {
	if strings.Contains(release, "64k") {
		return true
	}
	return strings.HasSuffix(release, ".aarch64") && (strings.Contains(release, ".el8") || strings.Contains(release, ".el7"))
}

// expire closes multi-line records that were not continued for a while.
func (p *parser) expire(n int) error {
	for seq, b := range p.ghes {
		if n-b.lastN > 50 {
			delete(p.ghes, seq)
			if err := p.ghesEvents(b); err != nil {
				return err
			}
		}
	}
	if p.amd != nil && n-p.amd.lastN > 12 {
		return p.flushAMD()
	}
	return nil
}

func (p *parser) ghesLine(l *line, seq, body string) error {
	b := p.ghes[seq]
	header := strings.HasPrefix(body, "Hardware error from APEI")
	if b != nil && header {
		delete(p.ghes, seq)
		if err := p.ghesEvents(b); err != nil {
			return err
		}
		b = nil
	}
	if b == nil {
		b = &ghesBlock{first: *l}
		p.ghes[seq] = b
	}
	b.lastN = l.n
	b.raw = append(b.raw, l.msg)
	if !header {
		b.body = append(b.body, body)
	}
	return nil
}

func (p *parser) flushGHES() error {
	for seq, b := range p.ghes {
		delete(p.ghes, seq)
		if err := p.ghesEvents(b); err != nil {
			return err
		}
	}
	return nil
}

// ghesEvents turns the memory error sections of a GHES block into events.
func (p *parser) ghesEvents(b *ghesBlock) error {
	eventSev, sections := splitGHES(b.body)
	for i := range sections {
		s := &sections[i]
		m, found := s.memError()
		if s.typ != "memory error" && !(s.typ == "" && found) {
			p.otherSections++
			continue
		}
		p.records++
		p.seg.records++
		ref := p.refRange(&b.first, b.lastN)
		sevText := s.severity
		if sevText == "" {
			sevText = eventSev
		}
		sev, ok := cper.SeverityFromText(sevText)
		if !ok {
			p.tally.Skip(ref, fmt.Sprintf("firmware error record has no readable severity (%q)", sevText))
			continue
		}
		e := &model.Event{
			Kind: model.KindError, ErrorSeverity: sev, Source: Kind + "/ghes", Decoder: "cper", Count: 1,
			RawRef: ref, Raw: strings.Join(b.raw, "\n"),
			Fingerprint: p.fingerprint("ghes", &b.first, strconv.Itoa(i)),
		}
		m.Apply(e)
		if e.Label == "" {
			e.Label = s.fruText
		}
		p.lastGH = &tie{fp: e.Fingerprint, n: b.lastN, class: sev.Class()}
		if err := p.emit(e, nil, &b.first); err != nil {
			return err
		}
	}
	return nil
}

func (p *parser) edacLine(l *line) error {
	p.records++
	p.seg.records++
	rec, why := parseEDACLine(l.msg)
	if why != "" {
		p.tally.Skip(p.ref(l), why)
		return nil
	}
	d := edac.ParseDetail(rec.detail)
	layers := edac.ParseLayers(rec.location)
	if d.Decoder == "" && len(layers.Index) == 0 && rec.location != "" {
		// Up to 5.x ghes_edac prints the firmware's fields only inside the
		// parentheses; the detail holds the status alone.
		d = edac.ParseDetail("APEI location: " + rec.location + " " + rec.detail)
	}
	sev, _ := edac.Severity(rec.kind, d.Recoverable)
	e := &model.Event{
		Kind: model.KindError, ErrorSeverity: sev, ErrorScrubType: edac.ScrubType(rec.msg), ErrorType: rec.msg,
		Source: Kind + "/edac", Count: max(rec.count, 1), EdacMC: model.Some(rec.mc),
		RawRef: p.ref(l), Raw: l.msg, Fingerprint: p.fingerprint("edac", l, ""),
		Syndrome: rec.syndrome,
	}
	if rec.syndrome.Valid && rec.syndrome.V == 0 {
		e.Syndrome = model.Opt[uint64]{}
	}
	e.EdacLocation = layers.Key()

	// The kernel splits the address at its page size. Offsets of 4096 and
	// more only occur with larger pages.
	seg := p.seg
	if rec.offset >= 1<<12 && seg.pageShift == 12 {
		seg.pageShift = 16
		p.bigPages = fmt.Sprintf("line %d has a page offset above 4 KiB: the kernel uses 64 KiB pages; addresses of EDAC lines before it in the same boot were computed for 4 KiB pages", l.n)
	}
	if rec.page>>(63-seg.pageShift) != 0 || rec.offset>>16 != 0 {
		p.tally.Skip(p.ref(l), "EDAC line page or offset is out of range")
		return nil
	}
	if rec.page != 0 || rec.offset != 0 {
		e.OsPhysicalAddress = model.Some(rec.page<<seg.pageShift | rec.offset)
	}

	d.Apply(e)
	label := rec.label
	edac.ParseLabel(label).Apply(e)
	if ch, slot := layers.Named("channel"), layers.Named("slot"); ch.Valid && slot.Valid {
		if !e.ChannelId.Valid {
			e.ChannelId = ch
		}
		if !e.ChannelSlotId.Valid {
			e.ChannelSlotId = slot
		}
	}
	if cs := layers.Named("csrow"); cs.Valid {
		// Chip-select based drivers: two chip selects per DIMM, one per rank.
		if ch := layers.Named("channel"); ch.Valid && !e.ChannelId.Valid {
			e.ChannelId = ch
		}
		if !e.ChannelSlotId.Valid {
			e.ChannelSlotId = model.Some(cs.V >> 1)
		}
		if !e.Rank.Valid {
			e.Rank = model.Some(cs.V & 1)
		}
		if e.Decoder == "" {
			e.Decoder = edac.DecoderCSROW
		}
	}
	if d.Decoder == edac.DecoderGHES {
		if code, ok := cper.ErrorTypeCode(rec.msg); ok && e.ErrorScrubType == "" {
			e.ErrorScrubType = cper.ScrubType(model.Some(code))
		}
		if d.DIMMLocation != "" {
			label = d.DIMMLocation
		}
		// ghes_edac reports the error the GHES block just before it printed.
		if t := p.lastGH; t != nil && l.n-t.n <= 20 && t.class == sev.Class() {
			e.DupOfFingerprint = t.fp
		}
		p.lastGH = nil
	}
	if !edac.PlaceholderLabel(label) {
		e.Label = label
	}
	if p.amd != nil && p.amd.edacFP == "" {
		p.amd.edacFP = e.Fingerprint
		p.amd.lastN = l.n
	}
	return p.emit(e, nil, l)
}

// hwLine handles a "[Hardware Error]: ..." line that is not part of a GHES
// block: a machine check record, or its decode by mce_amd.
func (p *parser) hwLine(l *line, body string) error {
	if m := mceHeadRE.FindStringSubmatch(body); m != nil {
		if err := p.flushMCE(); err != nil {
			return err
		}
		if err := p.flushAMD(); err != nil {
			return err
		}
		b := &mceBlock{first: *l, raw: []string{l.msg}}
		b.bank, _ = strconv.Atoi(m[3])
		b.status, _ = strconv.ParseUint(m[4], 16, 64)
		p.mce = b
		return nil
	}
	if p.mce != nil {
		switch {
		case strings.HasPrefix(body, "RIP"):
			p.mce.raw = append(p.mce.raw, l.msg)
			return nil
		case mceTSCRE.MatchString(body):
			p.mce.raw = append(p.mce.raw, l.msg)
			for _, kv := range mceKVRE.FindAllStringSubmatch(body, -1) {
				v, err := strconv.ParseUint(kv[2], 16, 64)
				if err != nil {
					continue
				}
				switch kv[1] {
				case "ADDR":
					p.mce.addr = v
				case "MISC":
					p.mce.misc = v
				case "SYND":
					p.mce.synd = v
				case "IPID":
					p.mce.ipd = v
				}
			}
			return nil
		}
		if m := mceProcRE.FindStringSubmatch(body); m != nil {
			b := p.mce
			p.mce = nil
			b.raw = append(b.raw, l.msg)
			vendor, _ := strconv.Atoi(m[1])
			unix, _ := strconv.ParseInt(m[3], 10, 64)
			socket, _ := strconv.Atoi(m[4])
			return p.mceEvent(b, l.n, vendor, unix, model.Some(socket))
		}
	}

	// AMD decode block.
	switch {
	case isAMDStatusText(body):
		if err := p.flushAMD(); err != nil {
			return err
		}
		p.amd = &amdBlock{first: *l, raw: []string{l.msg}, lastN: l.n}
		return nil
	case amdHeadRE.MatchString(body):
		m := amdHeadRE.FindStringSubmatch(body)
		if p.amd == nil || p.amd.hasHead {
			if err := p.flushAMD(); err != nil {
				return err
			}
			p.amd = &amdBlock{first: *l}
		}
		p.amd.hasHead = true
		p.amd.bank, _ = strconv.Atoi(m[5])
		p.amd.status, _ = strconv.ParseUint(m[6], 16, 64)
	case p.amd == nil:
		return nil // "Machine check events logged" and other notices
	case amdAddrRE.MatchString(body):
		p.amd.addr, _ = strconv.ParseUint(amdAddrRE.FindStringSubmatch(body)[1], 16, 64)
	case amdIPIDRE.MatchString(body):
		m := amdIPIDRE.FindStringSubmatch(body)
		p.amd.ipid, _ = strconv.ParseUint(m[1], 16, 64)
		if m[2] != "" {
			p.amd.synd, _ = strconv.ParseUint(m[2], 16, 64)
		}
	case amdExtRE.MatchString(body):
		m := amdExtRE.FindStringSubmatch(body)
		p.amd.ext = strings.TrimSpace(m[1] + ": " + m[3])
	}
	p.amd.raw = append(p.amd.raw, l.msg)
	p.amd.lastN = l.n
	return nil
}

func (p *parser) flushMCE() error {
	if p.mce == nil {
		return nil
	}
	b := p.mce
	p.mce = nil
	p.records++
	p.tally.Skip(p.ref(&b.first), "machine check record is incomplete")
	return nil
}

// mceEvent turns a complete machine check record into an event if it
// reports a memory error. The record carries its own wall-clock time.
func (p *parser) mceEvent(b *mceBlock, lastN, vendor int, unix int64, socket model.Opt[int]) error {
	p.records++
	p.seg.records++
	ref := p.refRange(&b.first, lastN)
	if !mce.Valid(b.status) {
		p.tally.Skip(ref, "machine check status register is not valid")
		return nil
	}
	dec := mce.Decode(mce.Record{Vendor: vendor, Bank: b.bank, Status: b.status, Addr: b.addr, Misc: b.misc, IPID: b.ipd, Synd: b.synd})
	if !dec.Memory {
		p.notMemory++
		return nil
	}
	amd := vendor == mce.VendorAMD || vendor == mce.VendorHygon
	e := &model.Event{
		Kind: model.KindError, ErrorSeverity: dec.Severity, ErrorScrubType: dec.Scrub, ErrorType: dec.ErrorType,
		Source: Kind + "/mce", Decoder: "mce/intel", Count: 1, SocketId: socket, ChannelId: dec.Channel,
		RawRef: ref, Raw: strings.Join(b.raw, "\n"), Fingerprint: p.fingerprint("mce", &b.first, ""),
	}
	if amd {
		e.Decoder = "mce/amd"
		if dec.ChipSelect.Valid {
			e.ChannelSlotId, e.Rank = model.Some(dec.ChipSelect.V>>1), model.Some(dec.ChipSelect.V&1)
		}
	} else if dec.Address.Valid && b.addr != 0 {
		e.OsPhysicalAddress = model.Some(b.addr)
	}
	first := b.first
	if unix > 0 {
		// The record's own time is exact, and tells when the boot started.
		first.wall = time.Unix(unix, 0).UTC()
		if first.hasMono && p.seg.startQuality == "" && p.seg.mceStart.IsZero() {
			p.seg.mceStart = first.wall.Add(-first.mono)
		}
	}
	return p.emit(e, nil, &first)
}

func (p *parser) flushAMD() error {
	if p.amd == nil {
		return nil
	}
	b := p.amd
	p.amd = nil
	if !b.hasHead {
		return nil
	}
	p.records++
	p.seg.records++
	ref := p.refRange(&b.first, b.lastN)
	if !mce.Valid(b.status) {
		p.tally.Skip(ref, "machine check status register is not valid")
		return nil
	}
	dec := mce.Decode(mce.Record{Vendor: mce.VendorAMD, Bank: b.bank, Status: b.status, Addr: b.addr, IPID: b.ipid, Synd: b.synd})
	if !dec.Memory && !strings.Contains(strings.ToLower(b.ext), "dram ecc") {
		p.notMemory++
		return nil
	}
	e := &model.Event{
		Kind: model.KindError, ErrorSeverity: dec.Severity, ErrorScrubType: dec.Scrub, ErrorType: "DRAM ECC error",
		Source: Kind + "/mce", Decoder: "mce/amd", Count: 1, ChannelId: dec.Channel,
		RawRef: ref, Raw: strings.Join(b.raw, "\n"), Fingerprint: p.fingerprint("mce", &b.first, ""),
		// amd64_edac printed its line for this error inside the block.
		DupOfFingerprint: b.edacFP,
	}
	if dec.ChipSelect.Valid {
		e.ChannelSlotId, e.Rank = model.Some(dec.ChipSelect.V>>1), model.Some(dec.ChipSelect.V&1)
	}
	return p.emit(e, nil, &b.first)
}

// emit sends a record on if its time is known, and holds it until the end
// of the boot otherwise.
func (p *parser) emit(e *model.Event, a *model.Action, l *line) error {
	seg := p.seg
	switch {
	case !l.wall.IsZero():
		return p.send(e, a, l.wall, model.TimeExact, seg.bootID)
	case l.hasMono && seg.startQuality != "":
		return p.send(e, a, seg.start.Add(l.mono), seg.startQuality, seg.bootID)
	}
	p.pend = append(p.pend, pending{ev: e, act: a, mono: l.mono, hasMono: l.hasMono})
	return nil
}

func (p *parser) send(e *model.Event, a *model.Action, t time.Time, q model.TimeQuality, bootID string) error {
	if y := t.UTC().Year(); y < 1970 || y > 9999 {
		ref := ""
		if e != nil {
			ref = e.RawRef
		} else {
			ref = a.RawRef
		}
		p.tally.Skip(ref, "time is out of range")
		return nil
	}
	switch q {
	case model.TimeRelative:
		p.relative++
	case model.TimeEstimated:
		p.estimated++
	}
	var err error
	if e != nil {
		e.ErrorDT, e.TimeQuality, e.BootID = t, q, bootID
		e.Grade = e.ComputeGrade()
		err = p.sink.Event(e)
	} else {
		a.At, a.TimeQuality, a.BootID = t, q, bootID
		err = p.sink.Action(a)
	}
	if err != nil {
		return sinkError{err}
	}
	return nil
}

// closeSegment ends the current boot: it finishes open multi-line records,
// settles the boot time, sends the records that were waiting for it and
// reports the boot. last is set for the boot at the end of the input, the
// only one the capture time and the caller's boot time apply to.
func (p *parser) closeSegment(last bool) error {
	seg := p.seg
	if seg == nil {
		return nil
	}
	if err := p.flushGHES(); err != nil {
		return err
	}
	if err := p.flushMCE(); err != nil {
		return err
	}
	if err := p.flushAMD(); err != nil {
		return err
	}
	p.lastGH = nil

	bootID := seg.bootID
	derived := false
	if seg.startQuality == "" {
		switch {
		case last && !p.opts.BootTime.IsZero():
			seg.start, seg.startQuality = p.opts.BootTime.UTC(), model.TimeExact
		case !seg.mceStart.IsZero():
			seg.start, seg.startQuality = seg.mceStart, model.TimeExact
		case last && !p.opts.CapturedAt.IsZero() && seg.hasMono:
			seg.start, seg.startQuality = p.opts.CapturedAt.UTC().Add(-seg.lastMono), model.TimeEstimated
		case !seg.rtcStart.IsZero():
			seg.start, seg.startQuality = seg.rtcStart, model.TimeEstimated
		default:
			// Nothing anchors this boot in time. Its records count from
			// the epoch and stay together under an ID of their own.
			seg.start, seg.startQuality = time.Unix(0, 0).UTC(), model.TimeRelative
			bootID = "derived-" + model.Fingerprint("klog-boot", p.origin, strconv.Itoa(seg.index), seg.banner)[:16]
			derived = true
		}
	}
	if last && bootID == "" && p.opts.BootID != "" {
		bootID = p.opts.BootID
	}
	eventBoot := bootID
	if !derived && seg.bootID == "" {
		// Events of a boot placed in time are tied to it by time.
		eventBoot = ""
	}

	for _, pd := range p.pend {
		t := seg.start
		if pd.hasMono {
			t = seg.start.Add(pd.mono)
		}
		q := seg.startQuality
		if !pd.hasMono && q != model.TimeRelative {
			// A message without any stamp: all that is known is the boot.
			q = model.TimeEstimated
		}
		if pd.gpu != nil {
			if err := p.sendGPU(pd.gpu, t, q, eventBoot); err != nil {
				return err
			}
			continue
		}
		if err := p.send(pd.ev, pd.act, t, q, eventBoot); err != nil {
			return err
		}
	}
	p.pend = nil

	if y := seg.start.UTC().Year(); (seg.records > 0 || seg.banner != "") && (y < 1970 || y > 9999) {
		// Stamps since boot that do not fit the dates of the log put the
		// start of the boot before 1970 or after 9999. The ledger stores
		// no such time: the boot is left out, its records keep the times
		// they have.
		p.records++
		p.tally.Skip(p.origin, "start of a boot is out of range: the seconds since boot of its lines do not fit their dates")
	} else if seg.records > 0 || seg.banner != "" {
		b := &model.Boot{
			BootID: bootID, Derived: derived || bootID == "", StartedAt: seg.start, StartQuality: seg.startQuality,
			Kernel: seg.kernel, BIOS: seg.bios, Cmdline: seg.cmdline, Source: Kind, RawRef: p.origin,
		}
		switch {
		case !seg.lastWall.IsZero():
			b.LastSeen = seg.lastWall
		case seg.hasMono:
			b.LastSeen = seg.start.Add(seg.lastMono)
		}
		if err := p.sink.Boot(b); err != nil {
			return sinkError{err}
		}
	}
	if last && (p.hostDMI != "" || p.hypervisor != "") {
		// What the latest boot of the log says about the machine: its
		// name, and whether it is a virtual machine. The kernel names the
		// hypervisor itself on x86; elsewhere the name of the machine
		// tells where it is that of a virtual one.
		h := &model.Host{SystemProduct: p.hostDMI, Virtual: p.hypervisor}
		if h.Virtual == "" {
			h.Virtual = virt.FromName(p.hostDMI)
		}
		if err := p.sink.Host(h); err != nil {
			return sinkError{err}
		}
	}
	p.seg = nil
	return nil
}
