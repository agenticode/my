package scan

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/klog"
	"github.com/agenticode/my/internal/adapter/rasdaemon"
	"github.com/agenticode/my/internal/adapter/redfish"
	"github.com/agenticode/my/internal/adapter/sel"
	"github.com/agenticode/my/internal/adapter/smbios"
)

// DetectOptions carry what a file cannot say about itself.
type DetectOptions struct {
	// Now is the time of the import. Inventories that carry no time of
	// their own (dmidecode output, the memory resources of a Redfish dump)
	// are recorded as seen at this time. Zero means the current time.
	Now time.Time
	// CapturedAt is when a kernel log that only has seconds since boot was
	// captured. Without it the times of such a log stay relative to boot.
	CapturedAt time.Time
	// ModTime uses the modification time of a file as the time it was
	// captured, where the file has no time of its own and neither Now nor
	// CapturedAt is set. It is right for files that were saved and left
	// alone since.
	ModTime bool
	// Location is the time zone of times that carry none: syslog files,
	// dmesg -T, and the text forms of a BMC event log. Nil means UTC.
	Location *time.Location
	// TempDir is where a private copy of a rasdaemon database of a host
	// tree is made; empty means the system temporary directory.
	TempDir string
}

// What Detect calls the inputs it knows.
const (
	WhatHostTree  = "copy of a host's files"
	WhatRasdaemon = "rasdaemon database"
	WhatKernelLog = "kernel log"
	WhatJournal   = "kernel log (journal export)"
	WhatDmidecode = "dmidecode output"
	WhatSEL       = "BMC event log (SEL)"
	WhatRedfish   = "Redfish dump"
)

// ErrUnknownInput is returned by Detect, wrapped with what the input looks
// like, for a file or directory that is none of the inputs.
var ErrUnknownInput = errors.New("not an input this tool reads")

// sniffSize is how much of a file is looked at to tell what it is.
const sniffSize = 256 << 10

// maxBinary bounds a binary file that is read whole to tell what it is. A
// full event log of a BMC is 1 MiB.
const maxBinary = 16 << 20

// Detect returns the adapters that read what is at path, and what it took
// the input for. The input is recognised by its content, not by its name:
//
//   - a directory with proc, sys or var below it is a copy of a host's
//     files and is read like a host;
//   - a directory with Redfish resources in JSON files is a Redfish dump;
//   - a file is a rasdaemon database, a kernel log in one of its forms, the
//     output of dmidecode, a BMC event log, or a Redfish resource.
//
// A file compressed with gzip is read through, except a database.
// Nothing is read beyond what is needed to recognise the input; the
// adapters read it when they are run.
func Detect(ctx context.Context, path string, opts DetectOptions) (adapters []adapter.Adapter, what string, err error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, "", err
	}
	if fi.IsDir() {
		return detectDir(ctx, path, opts)
	}
	if !fi.Mode().IsRegular() {
		return nil, "", fmt.Errorf("%s: %w: not a regular file", path, ErrUnknownInput)
	}
	head, zipped, err := readHead(path)
	if err != nil {
		return nil, "", err
	}
	a, what, err := detectFile(path, fi, head, zipped, opts)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", path, err)
	}
	return []adapter.Adapter{a}, what, nil
}

// hostTreeMarks are paths that only a copy of a host's files has.
var hostTreeMarks = []string{"proc", "sys", "var/log", "var/lib/rasdaemon", "sos_commands", "usr/local/var/lib/rasdaemon"}

func detectDir(ctx context.Context, dir string, opts DetectOptions) ([]adapter.Adapter, string, error) {
	for _, m := range hostTreeMarks {
		if fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(m))); err == nil && fi.IsDir() {
			return Sources(ctx, Options{Root: dir, Now: opts.Now, Location: opts.Location, TempDir: opts.TempDir}), WhatHostTree, nil
		}
	}
	// A Redfish dump: some JSON file below the directory is a resource.
	const maxLooked = 400
	looked, found := 0, false
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		if looked++; looked > maxLooked {
			return fs.SkipAll
		}
		head, _, err := readHead(p)
		if err == nil && redfish.Sniff(head) {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	if found {
		return []adapter.Adapter{redfish.New(dir, redfish.Options{Now: opts.Now, ModTimeAsObserved: opts.ModTime})}, WhatRedfish, nil
	}
	return nil, "", fmt.Errorf("%s: %w: a directory that is neither a copy of a host's files (no proc, sys or var below it) nor a Redfish dump", dir, ErrUnknownInput)
}

// readHead returns the start of a file, decompressed when the file is
// gzip-compressed. Binary files up to maxBinary are returned whole.
func readHead(path string) (head []byte, zipped bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	var r io.Reader = f
	magic := make([]byte, 2)
	if n, _ := io.ReadFull(f, magic); n == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return nil, false, err
		}
		zr, err := gzip.NewReader(f)
		if err != nil {
			return nil, false, fmt.Errorf("%s: gzip: %w", path, err)
		}
		defer zr.Close()
		r, zipped = zr, true
	} else if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, false, err
	}
	head, err = io.ReadAll(io.LimitReader(r, sniffSize))
	if err != nil && len(head) == 0 {
		return nil, zipped, fmt.Errorf("%s: %w", path, err)
	}
	if isBinary(head) && len(head) == sniffSize {
		// A binary event log has to be seen whole to be recognised.
		rest, _ := io.ReadAll(io.LimitReader(r, maxBinary-sniffSize+1))
		head = append(head, rest...)
	}
	return head, zipped, nil
}

func isBinary(data []byte) bool {
	if bytes.IndexByte(data, 0) >= 0 {
		return true
	}
	// The end of a head may cut a character in two.
	tail := data
	if len(tail) > 4 {
		tail = tail[:len(tail)-4]
	}
	return !utf8.Valid(tail)
}

const (
	sqliteMagic = "SQLite format 3\x00"
	// ledgerAppID is the application ID of a MemorY ledger ("MYLG").
	ledgerAppID = 0x4D594C47
)

var (
	dmidecodeRE = regexp.MustCompile(`(?m)^(?:# dmidecode \d|Handle 0x[0-9A-Fa-f]{4}, DMI type \d+, \d+ bytes)`)
	// The shapes a line of a kernel log has: a stamp in brackets with or
	// without a priority, a record of /dev/kmsg, the date dmesg -T prints,
	// and a line of a syslog file with either kind of time.
	klogREs = []*regexp.Regexp{
		regexp.MustCompile(`(?m)^(?:<\d{1,3}>)?\[\s*\d+\.\d{6}\] `),
		regexp.MustCompile(`(?m)^\d{1,3},\d+,\d+,[^;\n]*;`),
		regexp.MustCompile(`(?m)^\[[A-Z][a-z]{2} [A-Z][a-z]{2} [ \d]\d \d\d:\d\d:\d\d \d{4}\] `),
		regexp.MustCompile(`(?m)^[A-Z][a-z]{2} [ \d]\d \d\d:\d\d:\d\d \S+ [^\s:\[]+(?:\[\d+\])?: `),
		regexp.MustCompile(`(?m)^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\S* \S+ [^\s:\[]+(?:\[\d+\])?: `),
		regexp.MustCompile(`(?m)^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d[,.]\d+[+-]\d\d:?\d\d `),
	}
)

func detectFile(path string, fi fs.FileInfo, head []byte, zipped bool, opts DetectOptions) (adapter.Adapter, string, error) {
	// The time a file without a time of its own was captured.
	seen := opts.Now
	captured := opts.CapturedAt
	if opts.ModTime {
		if seen.IsZero() {
			seen = fi.ModTime()
		}
		if captured.IsZero() {
			captured = fi.ModTime()
		}
	}
	// A compressed file is read through a reader that opens it when the
	// adapter runs.
	stream := func() io.Reader { return &gzipFile{path: path} }

	trimmed := bytes.TrimLeft(bytes.TrimPrefix(head, []byte("\xef\xbb\xbf")), " \t\r\n")
	switch {
	case len(trimmed) == 0:
		return nil, "", fmt.Errorf("%w: the file is empty", ErrUnknownInput)

	case bytes.HasPrefix(head, []byte(sqliteMagic)):
		if len(head) >= 72 && binary.BigEndian.Uint32(head[68:72]) == ledgerAppID {
			return nil, "", fmt.Errorf("%w: a MemorY ledger is the result of an import, not an input; show it with \"my open\"", ErrUnknownInput)
		}
		if zipped {
			return nil, "", fmt.Errorf("%w: a compressed database; unpack it first", ErrUnknownInput)
		}
		// A file nothing writes to any more is read in place.
		return rasdaemon.New(path, rasdaemon.Options{Direct: true, TempDir: opts.TempDir}), WhatRasdaemon, nil

	case bytes.HasPrefix(head, []byte("PK\x03\x04")):
		return nil, "", fmt.Errorf("%w: a zip archive; a case bundle is shown with \"my open\", any other archive has to be unpacked", ErrUnknownInput)

	case isBinary(head):
		if sel.Sniff(head) {
			if zipped {
				return sel.NewReader(stream(), path, sel.Options{Location: opts.Location}), WhatSEL, nil
			}
			return sel.NewFile(path, sel.Options{Location: opts.Location}), WhatSEL, nil
		}
		return nil, "", fmt.Errorf("%w: binary data of no known kind", ErrUnknownInput)
	}

	kopts := klog.Options{CapturedAt: captured, Location: opts.Location}
	text := trimmed
	var a adapter.Adapter
	var what string
	switch {
	case text[0] == '{' && bytes.Contains(text, []byte(`"MESSAGE"`)) &&
		(bytes.Contains(text, []byte(`"_BOOT_ID"`)) || bytes.Contains(text, []byte(`"__REALTIME_TIMESTAMP"`)) || bytes.Contains(text, []byte(`"_TRANSPORT"`))):
		what = WhatJournal
		if zipped {
			a = klog.NewReader(stream(), path, kopts)
		} else {
			a = klog.NewFile(path, kopts)
		}

	case redfish.Sniff(text):
		what = WhatRedfish
		if zipped {
			a = redfish.NewReader(stream(), path, redfish.Options{Now: seen})
		} else {
			a = redfish.New(path, redfish.Options{Now: opts.Now, ModTimeAsObserved: opts.ModTime})
		}

	case looksLikeJSON(text):
		// JSON that is no journal and no Redfish resource: a list of event
		// log entries names sensors.
		if !bytes.Contains(bytes.ToLower(text), []byte("sensor")) {
			return nil, "", fmt.Errorf("%w: JSON that is neither a journal export, a Redfish resource nor a list of event log entries", ErrUnknownInput)
		}
		what = WhatSEL
		if zipped {
			a = sel.NewReader(stream(), path, sel.Options{Location: opts.Location})
		} else {
			a = sel.NewFile(path, sel.Options{Location: opts.Location})
		}

	case dmidecodeRE.Match(text):
		what = WhatDmidecode
		if zipped {
			a = smbios.NewDmidecodeReader(stream(), path, smbios.Options{Now: seen})
		} else {
			a = smbios.NewDmidecodeFile(path, smbios.Options{Now: seen})
		}

	case sel.Sniff(text):
		what = WhatSEL
		if zipped {
			a = sel.NewReader(stream(), path, sel.Options{Location: opts.Location})
		} else {
			a = sel.NewFile(path, sel.Options{Location: opts.Location})
		}

	case looksLikeKernelLog(text):
		what = WhatKernelLog
		if zipped {
			a = klog.NewReader(stream(), path, kopts)
		} else {
			a = klog.NewFile(path, kopts)
		}

	default:
		first, _, _ := strings.Cut(string(text[:min(len(text), 80)]), "\n")
		return nil, "", fmt.Errorf("%w: text of no known kind (it starts %q)", ErrUnknownInput, strings.TrimSpace(first))
	}
	return a, what, nil
}

// looksLikeJSON tells a JSON document from text that starts with a
// bracket, as every line of dmesg output does.
func looksLikeJSON(text []byte) bool {
	switch text[0] {
	case '{':
		return true
	case '[':
		rest := bytes.TrimLeft(text[1:], " \t\r\n")
		if len(rest) > 0 && (rest[0] == '{' || rest[0] == ']' || rest[0] == '"') {
			return true
		}
		return json.Valid(text)
	}
	return false
}

// looksLikeKernelLog reports whether text has lines in one of the shapes
// of a kernel log.
func looksLikeKernelLog(text []byte) bool {
	for _, re := range klogREs {
		if re.Match(text) {
			return true
		}
	}
	return false
}

// gzipFile reads a gzip-compressed file. It opens the file on the first
// read, so that an adapter that is never run opens nothing, and closes it
// when the end is reached or reading fails.
type gzipFile struct {
	path string
	f    *os.File
	zr   *gzip.Reader
	err  error
}

func (g *gzipFile) Read(p []byte) (int, error) {
	if g.err != nil {
		return 0, g.err
	}
	if g.f == nil {
		f, err := os.Open(g.path)
		if err != nil {
			g.err = err
			return 0, err
		}
		zr, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			g.err = fmt.Errorf("gzip: %w", err)
			return 0, g.err
		}
		g.f, g.zr = f, zr
	}
	n, err := g.zr.Read(p)
	if err != nil {
		g.zr.Close()
		g.f.Close()
		g.err = err
	}
	return n, err
}
