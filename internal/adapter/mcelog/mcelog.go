// Package mcelog reads what "mcelog --client" prints: the error counts the
// mcelog daemon keeps per DIMM.
//
// A host that runs the mcelog daemon in place of rasdaemon has no database
// of error records. The daemon decodes each machine check, counts it for
// the DIMM it names, and answers a client with those counts:
//
//	Memory errors
//	SOCKET 0 CHANNEL 1 DIMM 0
//	DMI_NAME "DIMM_B1" DMI_LOCATION "NODE 0 CHANNEL 1 DIMM 0"
//	corrected memory errors:
//		12 total
//		3 in 24h
//	uncorrected memory errors:
//		0 total
//		0 in 24h
//
// These are counts without a cell address, like the EDAC counters of the
// kernel, and they are read the same way: one reading per DIMM, which the
// ledger turns into grade C events that carry the growth since the reading
// before. The counts live in the memory of the daemon. They start at zero
// when it starts, and the output does not say when that was.
//
// The second number of a block is the rate window of the daemon, by default
// the last 24 hours. It counts the errors since the window was last aged,
// which is less than its length ago: at least that many of the errors are
// that recent.
package mcelog

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
)

// Kind is the adapter name in the source inventory.
const Kind = "mcelog"

// Options tell the adapter what the input itself cannot say.
type Options struct {
	// Now is when the daemon was asked. The output carries no time. Zero
	// means the current time.
	Now time.Time
}

// DefaultTimeout bounds a run of mcelog --client.
const DefaultTimeout = 20 * time.Second

// Limits of what one run reads. A host with 96 DIMMs prints a few
// kilobytes; the page table of the daemon is bounded by its own setting.
const (
	maxInput = 64 << 20
	maxLine  = 64 << 10
)

// Adapter reads one answer of the mcelog daemon.
type Adapter struct {
	origin string
	opts   Options
	read   func(ctx context.Context) (data []byte, status model.SourceStatus, detail string)
	// asked is set when the adapter asks the daemon itself, so that an
	// empty answer is the daemon's and not a file that was saved empty.
	asked bool
}

func (a *Adapter) Kind() string   { return Kind }
func (a *Adapter) Origin() string { return a.origin }

func readAll(r io.Reader) ([]byte, model.SourceStatus, string) {
	data, err := io.ReadAll(io.LimitReader(r, maxInput+1))
	if err != nil {
		st, detail := adapter.StatusForError(err)
		return nil, st, detail
	}
	if len(data) > maxInput {
		return nil, model.SourceError, "larger than 64 MiB: not the output of mcelog --client"
	}
	return data, "", ""
}

// NewFile returns an adapter for a file that holds the output.
func NewFile(path string, opts Options) *Adapter {
	return &Adapter{origin: path, opts: opts, read: func(context.Context) ([]byte, model.SourceStatus, string) {
		f, err := os.Open(path)
		if err != nil {
			st, detail := adapter.StatusForError(err)
			return nil, st, detail
		}
		defer f.Close()
		if fi, err := f.Stat(); err == nil && fi.IsDir() {
			return nil, model.SourceError, "is a directory"
		}
		return readAll(f)
	}}
}

// NewReader returns an adapter for output read from r.
func NewReader(r io.Reader, origin string, opts Options) *Adapter {
	return &Adapter{origin: origin, opts: opts, read: func(context.Context) ([]byte, model.SourceStatus, string) { return readAll(r) }}
}

// NewCommand returns an adapter that runs "prog --client" and reads what it
// prints. An empty prog looks mcelog up in PATH; a zero timeout means
// DefaultTimeout. The client asks the daemon for its counts and changes
// nothing. A host without the program, or without a daemon that answers,
// gets the status absent: there are no counts to read.
func NewCommand(prog string, timeout time.Duration, opts Options) *Adapter {
	name := prog
	if name == "" {
		name = "mcelog"
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Adapter{origin: name + " --client", opts: opts, asked: true, read: func(ctx context.Context) ([]byte, model.SourceStatus, string) {
		path := prog
		if path == "" {
			p, err := exec.LookPath("mcelog")
			if err != nil {
				return nil, model.SourceAbsent, "mcelog is not installed"
			}
			path = p
		}
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, path, "--client")
		var out, errOut limited
		out.max, errOut.max = maxInput, 4<<10
		cmd.Stdout, cmd.Stderr = &out, &errOut
		// A child of the program that keeps its output open must not hold
		// up the scan once the program was stopped.
		cmd.WaitDelay = time.Second
		err := cmd.Run()
		switch {
		case ctx.Err() != nil:
			return nil, model.SourceError, fmt.Sprintf("mcelog --client did not answer within %s", timeout)
		case err != nil && out.buf.Len() == 0:
			var notFound *exec.Error
			if errors.As(err, &notFound) || errors.Is(err, os.ErrNotExist) {
				return nil, model.SourceAbsent, "mcelog is not installed"
			}
			if st, detail := adapter.StatusForError(err); st == model.SourceUnreadable {
				return nil, st, detail
			}
		}
		// The client writes what went wrong to its error output and may
		// still exit with success: both outputs say what happened.
		if msg := clientFailure(errOut.buf.String()); msg != "" && !hasBlocks(out.buf.Bytes()) {
			return failure(msg)
		}
		if err != nil && out.buf.Len() == 0 {
			return nil, model.SourceError, "mcelog --client failed: " + firstLine(errOut.buf.String(), err.Error())
		}
		return out.buf.Bytes(), "", ""
	}}
}

// limited is a writer that keeps the first max bytes.
type limited struct {
	buf bytes.Buffer
	max int
}

func (l *limited) Write(p []byte) (int, error) {
	if room := l.max - l.buf.Len(); room > 0 {
		l.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

func firstLine(s, fallback string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			if len(l) > 200 {
				l = l[:200] + "..."
			}
			return l
		}
	}
	return fallback
}

// clientFailure returns the line with which the client or the daemon says
// that the question was not answered, or "".
func clientFailure(text string) string {
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		low := strings.ToLower(l)
		if strings.Contains(low, "client connect") || strings.Contains(low, "client socket") || strings.Contains(low, "client command write") ||
			strings.Contains(low, "client read") || low == "permission denied" {
			return l
		}
	}
	return ""
}

// failure names the status of an answer that holds no counts, from the
// line that says why.
func failure(msg string) ([]byte, model.SourceStatus, string) {
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "permission denied"), strings.Contains(low, "operation not permitted"):
		return nil, model.SourceUnreadable, "the mcelog daemon answers root only (" + msg + "); readable as root"
	case strings.Contains(low, "no such file"), strings.Contains(low, "connection refused"):
		return nil, model.SourceAbsent, "no mcelog daemon is running on this host (" + msg + "): there are no counts to read"
	}
	return nil, model.SourceError, "mcelog --client failed: " + msg
}

var (
	blockRE = regexp.MustCompile(`^SOCKET (\d+) CHANNEL (\d+|any) DIMM (\d+|any)$`)
	totalRE = regexp.MustCompile(`^(\d+) total$`)
	// The window line is "<count> in <length><unit>". The unit is one of d,
	// h, m and s and is missing when the setting of the daemon has none.
	windowRE = regexp.MustCompile(`^(\d+) in (\d+)([dhms]?)$`)
	pageRE   = regexp.MustCompile(`^([0-9a-fA-F]+): total (\d+) seen "([^"]*)" (online|offline-failed|offline)( triggered)?$`)
)

const (
	headDIMMs = "Memory errors"
	headPages = "Per page corrected memory statistics:"
	headCE    = "corrected memory errors:"
	headUE    = "uncorrected memory errors:"
)

// Sniff reports whether data looks like the output of mcelog --client. It
// is used to pick the adapter for a file by its content.
func Sniff(data []byte) bool {
	head := data
	if len(head) > 64<<10 {
		head = head[:64<<10]
	}
	return hasBlocks(head)
}

// hasBlocks reports whether text holds a DIMM block or a line of the page
// table.
func hasBlocks(text []byte) bool {
	for _, raw := range bytes.Split(text, []byte("\n")) {
		line := clean(string(raw))
		if blockRE.MatchString(line) || pageRE.MatchString(line) {
			return true
		}
	}
	return false
}

// clean takes the line end, the indentation and control characters off a
// line. The daemon prints the unit of a window as one character, which is a
// NUL byte when the setting has no unit.
func clean(line string) string {
	line = strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' || r == 0x7f {
			return -1
		}
		return r
	}, line)
	return strings.TrimSpace(line)
}

// count is one error count of a block.
type count struct {
	total model.Opt[uint64]
	// recent is the number of the window line and window its length.
	recent model.Opt[uint64]
	window time.Duration
}

// block is one DIMM block of the output. A channel or DIMM of -1 stands for
// "any": the daemon could not tell which.
type block struct {
	socket, channel, dimm int
	name, location        string
	ce, ue                count
	line                  int
}

// document is the output taken apart.
type document struct {
	blocks []block
	// pages, offline and failed count the lines of the page table.
	pages, offline, failed int
	sawDIMMs, sawPages     bool
	tally                  adapter.Tally
}

func parseCount(s string) (uint64, bool) {
	// The daemon counts in 32 bits.
	v, err := strconv.ParseUint(s, 10, 32)
	return v, err == nil
}

// parseDMI reads `DMI_NAME "x" DMI_LOCATION "y"`, of which either part may
// be missing. The daemon prints the strings of the BIOS as they are.
func parseDMI(line string) (name, location string, ok bool) {
	const nameKey, locKey = `DMI_NAME "`, `DMI_LOCATION "`
	rest := line
	if strings.HasPrefix(rest, nameKey) {
		rest = rest[len(nameKey):]
		// The name ends where the location begins, or at the last quote.
		end := strings.Index(rest, `" `+locKey)
		if end < 0 {
			if !strings.HasSuffix(rest, `"`) {
				return "", "", false
			}
			return strings.TrimSpace(rest[:len(rest)-1]), "", true
		}
		name, rest = strings.TrimSpace(rest[:end]), rest[end+2:]
	}
	if !strings.HasPrefix(rest, locKey) || !strings.HasSuffix(rest, `"`) || len(rest) < len(locKey)+1 {
		return "", "", false
	}
	return name, strings.TrimSpace(rest[len(locKey) : len(rest)-1]), true
}

// parse takes the output apart. Lines it does not know are counted, with
// their place, so that nothing is passed over in silence.
func parse(data []byte, origin string) document {
	var doc document
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 4096), maxLine)
	var cur *block
	var sect *count
	inPages := false
	n := 0
	for sc.Scan() {
		n++
		line := clean(sc.Text())
		ref := fmt.Sprintf("%s#L%d", origin, n)
		switch {
		case line == "":
			continue
		case line == headDIMMs:
			doc.sawDIMMs, inPages, cur, sect = true, false, nil, nil
			continue
		case line == headPages:
			doc.sawPages, inPages, cur, sect = true, true, nil, nil
			continue
		case line == "done" || line == "pong":
			// The end of an answer, when the answer was taken off the socket.
			continue
		}
		if m := blockRE.FindStringSubmatch(line); m != nil {
			num := func(s string) int {
				if s == "any" {
					return -1
				}
				v, err := strconv.ParseUint(s, 10, 16)
				if err != nil {
					return -2
				}
				return int(v)
			}
			b := block{socket: num(m[1]), channel: num(m[2]), dimm: num(m[3]), line: n}
			inPages, sect = false, nil
			if b.socket < 0 || b.channel == -2 || b.dimm == -2 {
				doc.tally.Skip(ref, "a socket, channel or DIMM number that is out of range")
				cur = nil
				continue
			}
			doc.blocks = append(doc.blocks, b)
			cur = &doc.blocks[len(doc.blocks)-1]
			continue
		}
		if m := pageRE.FindStringSubmatch(line); m != nil && (inPages || cur == nil) {
			doc.sawPages = true
			doc.pages++
			switch m[4] {
			case "offline":
				doc.offline++
			case "offline-failed":
				doc.failed++
			}
			continue
		}
		if cur == nil {
			doc.tally.Skip(ref, "a line that belongs to no DIMM block")
			continue
		}
		switch {
		case strings.HasPrefix(line, "DMI_NAME ") || strings.HasPrefix(line, "DMI_LOCATION "):
			name, loc, ok := parseDMI(line)
			if !ok {
				doc.tally.Skip(ref, "a DMI line that is not readable")
				continue
			}
			cur.name, cur.location = name, loc
		case line == headCE:
			sect = &cur.ce
		case line == headUE:
			sect = &cur.ue
		case sect == nil:
			doc.tally.Skip(ref, "a line of a DIMM block that is not known")
		default:
			if m := totalRE.FindStringSubmatch(line); m != nil {
				if v, ok := parseCount(m[1]); ok {
					sect.total = model.Some(v)
				} else {
					doc.tally.Skip(ref, "a count that is out of range")
				}
				continue
			}
			if m := windowRE.FindStringSubmatch(line); m != nil {
				v, ok := parseCount(m[1])
				length, err := strconv.ParseUint(m[2], 10, 31)
				unit := time.Second
				switch m[3] {
				case "d":
					unit = 24 * time.Hour
				case "h":
					unit = time.Hour
				case "m":
					unit = time.Minute
				}
				if !ok || err != nil || length == 0 || length > uint64((100*365*24*time.Hour)/unit) {
					doc.tally.Skip(ref, "a rate window that is out of range")
					continue
				}
				sect.recent, sect.window = model.Some(v), time.Duration(length)*unit
				continue
			}
			if line == "not enabled" {
				continue // the daemon keeps no rate window for this count
			}
			// What follows a line that is not known may belong to something
			// that is not known either: it is not taken for this count.
			sect = nil
			doc.tally.Skip(ref, "a line of a DIMM block that is not known")
		}
	}
	if err := sc.Err(); err != nil {
		doc.tally.Skip(fmt.Sprintf("%s#L%d", origin, n+1), "a line longer than 64 KiB: the rest was not read")
	}
	return doc
}

// index keeps the counters of one socket apart in the ledger: channel and
// DIMM number, each with room for "any".
func index(channel, dimm int) int {
	return (channel+1)<<16 | (dimm + 1)
}

// Collect reads the output into sink.
func (a *Adapter) Collect(ctx context.Context, sink adapter.Sink) adapter.Report {
	rep := adapter.Report{Kind: Kind, Origin: a.origin}
	if err := ctx.Err(); err != nil {
		rep.Status, rep.Detail = model.SourceError, err.Error()
		return rep
	}
	data, status, detail := a.read(ctx)
	if status != "" {
		rep.Status, rep.Detail = status, detail
		return rep
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	doc := parse(data, a.origin)
	if len(doc.blocks) == 0 {
		text := string(data)
		switch msg := clientFailure(text); {
		case msg != "":
			_, rep.Status, rep.Detail = failure(msg)
		case len(bytes.TrimSpace(data)) == 0:
			// The daemon prints nothing as long as it has no DIMM in its
			// table: it runs and has seen no memory error. That is what
			// mcelog 201 answers on a host without errors.
			rep.Status, rep.Detail = model.SourceEmpty, "no output: the daemon lists no DIMM, or its answer was not saved"
			if a.asked {
				rep.Detail = "the daemon answered and lists no DIMM: it has counted no memory error since it started"
			}
		case doc.sawDIMMs || doc.sawPages:
			rep.Status, rep.Detail = model.SourceEmpty, "the daemon lists no DIMM"
			rep.Records, rep.Skipped = doc.tally.Total(), doc.tally.Total()
			rep.Notes = append(rep.Notes, pageNote(&doc)...)
			rep.Notes = append(rep.Notes, doc.tally.Notes()...)
		default:
			rep.Status, rep.Detail = model.SourceError, "not the output of mcelog --client: "+firstLine(text, "no text")
		}
		return rep
	}
	// A record is a DIMM block, or a line that could not be read.
	rep.Skipped = doc.tally.Total()
	rep.Records = len(doc.blocks) + rep.Skipped

	now := a.opts.Now.UTC()
	if a.opts.Now.IsZero() {
		now = time.Now().UTC()
	}

	// A block for a whole socket holds every memory error of the socket,
	// those of its DIMMs among them. What is left after the blocks of its
	// DIMMs and channels are the errors the daemon could not place.
	type sums struct{ ce, ue uint64 }
	placed := map[int]sums{}
	seen := map[[3]int]int{}
	var dimms, withErrors, named int
	var ceTotal, ueTotal uint64
	for i := range doc.blocks {
		b := &doc.blocks[i]
		key := [3]int{b.socket, b.channel, b.dimm}
		if first, dup := seen[key]; dup {
			// The block was counted as a record; now it is a skipped one.
			rep.Skipped++
			doc.tally.Skip(fmt.Sprintf("%s#L%d", a.origin, b.line), fmt.Sprintf("a second block for the DIMM of line %d", first))
			b.socket = -1 // left out below
			continue
		}
		seen[key] = b.line
		if b.channel == -1 && b.dimm == -1 {
			continue
		}
		s := placed[b.socket]
		s.ce, s.ue = s.ce+b.ce.total.Or(0), s.ue+b.ue.total.Or(0)
		placed[b.socket] = s
	}

	var unplaced uint64
	sockets, partial := 0, 0
	for i := range doc.blocks {
		b := &doc.blocks[i]
		if b.socket < 0 {
			continue
		}
		c := &model.CounterSample{
			ObservedAt: now, MC: b.socket, MCName: "mcelog socket " + strconv.Itoa(b.socket), DIMMIndex: index(b.channel, b.dimm),
			Label: b.name, BankLocator: b.location, SocketId: model.Some(b.socket),
			CE: b.ce.total.Or(0), UE: b.ue.total.Or(0),
			Source: Kind, RawRef: fmt.Sprintf("%s#L%d", a.origin, b.line),
		}
		if b.channel >= 0 {
			c.ChannelId = model.Some(b.channel)
		}
		if b.dimm >= 0 {
			c.ChannelSlotId = model.Some(b.dimm)
		}
		switch {
		case b.channel == -1 && b.dimm == -1:
			// The rest of the socket. The rate window of the socket ages by
			// itself and says nothing about the rest.
			sockets++
			s := placed[b.socket]
			c.DIMMIndex, c.Label, c.BankLocator = -1, "", ""
			c.CE, c.UE = minus(c.CE, s.ce), minus(c.UE, s.ue)
			unplaced += c.CE + c.UE
		default:
			if b.channel == -1 || b.dimm == -1 {
				// The daemon knew the channel and not the DIMM on it, or
				// the other way round: the errors belong to no one DIMM.
				partial++
				unplaced += c.CE + c.UE
			} else {
				dimms++
				if c.CE+c.UE > 0 {
					withErrors++
				}
				if b.name != "" {
					named++
				}
				ceTotal, ueTotal = ceTotal+c.CE, ueTotal+c.UE
			}
			c.RecentCE, c.RecentCEWindow = b.ce.recent, b.ce.window
			c.RecentUE, c.RecentUEWindow = b.ue.recent, b.ue.window
		}
		if err := sink.Counter(c); err != nil {
			rep.Status, rep.Detail, rep.SinkErr = model.SourceError, "ledger rejected a record: "+err.Error(), err
			return rep
		}
	}

	rep.Notes = append(rep.Notes, fmt.Sprintf("%d DIMMs listed, %d with errors: %d corrected and %d uncorrected errors since the daemon started; the output does not say when that was",
		dimms, withErrors, ceTotal, ueTotal))
	if dimms > named {
		rep.Notes = append(rep.Notes, fmt.Sprintf("%d of the DIMMs have no DMI name: they are kept under their socket, channel and DIMM numbers", dimms-named))
	}
	if sockets+partial > 0 {
		rep.Notes = append(rep.Notes, fmt.Sprintf("%d errors are counted for a socket or a channel and for no DIMM: the daemon could not tell which DIMM they belong to", unplaced))
	}
	if w := windows(doc.blocks); w != "" {
		rep.Notes = append(rep.Notes, "rate window of the daemon: "+w)
	}
	rep.Notes = append(rep.Notes, pageNote(&doc)...)
	rep.Notes = append(rep.Notes, doc.tally.Notes()...)
	if rep.Skipped > 0 {
		rep.Status = model.SourcePartial
	} else {
		rep.Status = model.SourceOK
	}
	return rep
}

func minus(a, b uint64) uint64 {
	if b >= a {
		return 0
	}
	return a - b
}

// windows names the rate windows the blocks use, shortest first.
func windows(blocks []block) string {
	set := map[time.Duration]bool{}
	for i := range blocks {
		for _, c := range []*count{&blocks[i].ce, &blocks[i].ue} {
			if c.recent.Valid {
				set[c.window] = true
			}
		}
	}
	var list []time.Duration
	for w := range set {
		list = append(list, w)
	}
	sort.Slice(list, func(i, j int) bool { return list[i] < list[j] })
	var out []string
	for _, w := range list {
		switch {
		case w%(24*time.Hour) == 0 && w > 24*time.Hour:
			out = append(out, fmt.Sprintf("%dd", w/(24*time.Hour)))
		case w%time.Hour == 0:
			out = append(out, fmt.Sprintf("%dh", w/time.Hour))
		case w%time.Minute == 0:
			out = append(out, fmt.Sprintf("%dm", w/time.Minute))
		default:
			out = append(out, fmt.Sprintf("%ds", w/time.Second))
		}
	}
	return strings.Join(out, ", ")
}

// pageNote says what the page table of the daemon holds. Its lines are a
// state without a time: which pages are offline now. The offlining itself
// is on record, with its time, in the kernel log.
func pageNote(doc *document) []string {
	if doc.pages == 0 {
		return nil
	}
	return []string{fmt.Sprintf("the page table of the daemon lists %d pages with corrected errors, %d offline and %d where offlining failed; it is not read into the ledger: it has no times, and the kernel log records each offlining with its time",
		doc.pages, doc.offline, doc.failed)}
}
