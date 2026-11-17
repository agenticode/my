// Package bundle reads and writes case bundles: one file that carries the
// records of a case from the host they were collected on to whoever
// analyses them.
//
// A bundle (extension .mycase) is a ZIP archive with four members:
//
//	manifest.json  what the bundle is and what it holds: format version,
//	               the tool that wrote it, the hosts, the subject of the
//	               case, a list of what was collected, and the checksum
//	               of the ledger
//	sources.json   the source inventory: every source a scan looked for,
//	               with what it yielded or why it could not be read
//	ledger.db      the ledger, a SQLite file: events, DIMM identities,
//	               placements, boots, actions, findings
//	README.txt     the same in words, for a person who receives the file
//
// With Options.WithInputs the files the records were read from travel as
// well, under inputs/.
//
// A bundle holds records about memory errors. It never holds the contents
// of memory.
//
// The format is plain so that a sender can check what they send: any ZIP
// tool lists and extracts the members, and the ledger opens with any
// SQLite tool.
package bundle

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // the ledger inside a bundle is a SQLite file

	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/outfile"
	"github.com/agenticode/my/internal/store"
)

// The format.
const (
	// Extension is the file name extension of a bundle.
	Extension = ".mycase"
	// Format names the format in the manifest.
	Format = "mycase"
	// Version is the format version this package writes. A reader accepts
	// bundles up to its own version.
	Version = 1

	manifestName = "manifest.json"
	sourcesName  = "sources.json"
	ledgerName   = "ledger.db"
	readmeName   = "README.txt"
)

// maxLedger bounds the size of the ledger a bundle may hold when it is
// taken out of the archive.
const maxLedger = 64 << 30

// Errors of Open.
var (
	// ErrNotBundle: the file is not a case bundle.
	ErrNotBundle = errors.New("not a case bundle")
	// ErrNewerFormat: the bundle was written in a newer format.
	ErrNewerFormat = errors.New("the bundle was written by a newer version of this tool")
	// ErrDamaged: a member is missing, or the ledger does not match its
	// checksum.
	ErrDamaged = errors.New("the bundle is damaged")
	// ErrInputsAnonymized: the files of an import were asked for in an
	// anonymized bundle. The anonymizer replaces names in the ledger; it
	// cannot vouch for a whole vendor file.
	ErrInputsAnonymized = errors.New("the files that were read cannot travel in an anonymized bundle: the anonymizer cannot vouch for a whole vendor file")
)

// inputsDir is the directory of the bundle that holds the files that were
// read, with Options.WithInputs.
const inputsDir = "inputs/"

// Manifest says what a bundle is and what it holds.
type Manifest struct {
	Format        string    `json:"format"`
	FormatVersion int       `json:"format_version"`
	CreatedAt     time.Time `json:"created_at"`
	// Tool is the version of the tool that wrote the bundle.
	Tool   string     `json:"tool"`
	Ledger LedgerInfo `json:"ledger"`
	// Anonymized is set when host names and network addresses were
	// replaced before the bundle was written.
	Anonymized    bool           `json:"anonymized"`
	Anonymization *Anonymization `json:"anonymization,omitzero"`
	// Subject is the DIMM the case is about, when the sender named one.
	Subject *Subject `json:"subject,omitzero"`
	Hosts   []Host   `json:"hosts"`
	// Collected lists what the ledger holds, table by table.
	Collected []Item `json:"collected"`
	// NotCollected names what a bundle never holds.
	NotCollected []string `json:"not_collected"`
	// Inputs are the files the records were read from, so that a bundle
	// can be matched to the files a sender kept.
	Inputs []Input `json:"inputs,omitzero"`
}

// Input is a file the records of a bundle were read from.
type Input struct {
	// Name is the file as it was given, without the directories above
	// it: "sel.txt", or "dump/redfish/v1/Systems/1.json" for a file of a
	// directory that was given.
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
	// Member is the member of the bundle that holds the file, when it
	// travels in the bundle.
	Member string `json:"member,omitzero"`
}

// InputFile is a file to list in the manifest, and to put into the bundle
// with Options.WithInputs.
type InputFile struct {
	Name string
	Path string
}

// LedgerInfo describes the ledger member.
type LedgerInfo struct {
	File          string `json:"file"`
	SchemaVersion int    `json:"schema_version"`
	Bytes         int64  `json:"bytes"`
	SHA256        string `json:"sha256"`
}

// Anonymization says what was replaced. It does not say by what: the
// mapping is shown to the sender and is not part of the bundle.
type Anonymization struct {
	Hosts     int `json:"hosts"`
	Addresses int `json:"addresses"`
	// Users counts the names of home directories that were replaced in
	// the paths of the files that were read.
	Users int `json:"users,omitzero"`
	// Keyed is set when the sender gave a key: bundles made with the same
	// key replace a name by the same word.
	Keyed bool `json:"keyed"`
}

// Subject names the DIMM a case is about.
type Subject struct {
	Host    string `json:"host"`
	Slot    string `json:"slot,omitzero"`
	Serial  string `json:"serial,omitzero"`
	DIMMUID string `json:"dimm_uid,omitzero"`
}

// Host summarises one host of the ledger.
type Host struct {
	Name    string    `json:"name"`
	DIMMs   int       `json:"dimms"`
	CE      int64     `json:"ce"`
	UE      int64     `json:"ue"`
	Records int64     `json:"records"`
	First   time.Time `json:"first,omitzero"`
	Last    time.Time `json:"last,omitzero"`
	// Boots is the number of boots of the host on record, Stops those of
	// them that a fatal hardware error ended, and Panics those that ended
	// in another panic.
	Boots  int `json:"boots,omitzero"`
	Stops  int `json:"stops,omitzero"`
	Panics int `json:"panics,omitzero"`
	// Sources are the kinds of the sources that gave records of the host:
	// "klog", "rasdaemon", "pstore", "kdump", "bert" and the others.
	Sources []string `json:"sources,omitzero"`
}

// Item is one kind of record the ledger holds.
type Item struct {
	Table string `json:"table"`
	Rows  int64  `json:"rows"`
	// Holds says what a row of the table is.
	Holds string `json:"holds"`
}

// Sources is the content of sources.json.
type Sources struct {
	Scans []ScanSources `json:"scans"`
}

// ScanSources is one collection run with its source inventory.
type ScanSources struct {
	model.Scan
	Sources []model.Source `json:"sources"`
}

// tables says what each table of the ledger holds, for the manifest. A
// table that is not listed here is listed in the manifest with its name.
var tables = map[string]string{
	"event":          "memory error records: time, severity, location fields (socket, channel, slot, rank, device, bank, row, column, physical address), source and a reference to the original record",
	"event_raw":      "the original text of each error record: the kernel log lines or the database row it was read from",
	"action":         "repair and isolation steps the host recorded: pages taken offline, post package repair, sparing, modules disabled by firmware",
	"dimm":           "DIMM identities: manufacturer, part number, serial number, size, type",
	"slot":           "memory slots of the board with the names sources use for them",
	"slot_alias":     "other names of the slots",
	"placement":      "which DIMM was seen in which slot, and when",
	"boot":           "boots of the host: time, kernel release, BIOS version, kernel command line, and how a boot ended when a record shows it",
	"capture_check":  "what a reset right after a fatal memory error would have lost on the host when it was scanned, check by check, with the files that were read",
	"host":           "the host: name, architecture, system vendor and product",
	"scan":           "collection runs: when, as which kind, by which tool version, whether as root",
	"source":         "the source inventory: every source that was looked for and what it yielded",
	"counter_sample": "readings of the per-DIMM error counters",
	"fault":          "faults the analysis engine made out",
	"fault_event":    "which error records belong to which fault",
	"verdict":        "findings of the analysis engine with their evidence",
	"gpu_mem":        "GPU memory records: row remapping state, ECC counters, XID events",
	"ecs_sample":     "readings of the DDR5 error check and scrub registers",
	"bmc":            "readings of a BMC: its firmware, the BIOS version, which resources it gave, the period its logs cover",
	"bios_setting":   "settings of the system firmware that bear on memory errors, as a BMC reports them, with the pending values",
	"bmc_memory":     "memory modules as a BMC describes them: names, state, speed, and the BMC's own error counts",
	"fw_record":      "what the BIOS or a BMC said about memory, in the vendor's words: the common event it was read as, message ID, registry, severity, place, arguments, text",
	"unread_record":  "records a reader could not use and whose text names memory, with the reason, at most 50 per source",
	"registry_seen":  "message registries a BMC published, with the number of memory messages this version classes",
	"host_reading":   "counters of the kernel about the memory of the host when a scan ran: memory use, slab cache sizes, page counters, NUMA node sizes, cache sizes of the CPUs; what the scan saw of the path to the SPD",
	"spd_reading":    "the SPD of the memory modules as read, 512 or 1,024 bytes per module, with the manufacturing week, serial and part number",
	"meta":           "ledger identity, schema history and what the findings were computed with",
}

// NotCollected is what a bundle never holds.
var NotCollected = []string{
	"contents of memory, of files or of processes",
	"user accounts, credentials or keys",
	"network configuration",
	"kernel log lines that are not about memory errors, page offlining, machine checks or the boot banner",
}

// NotCollectedWithInputs is what a bundle that carries the files that
// were read never holds: those files hold whatever their writer put in.
var NotCollectedWithInputs = []string{
	"contents of memory or of processes",
	"files other than those listed under inputs in the manifest",
}

// Options describe a bundle to write.
type Options struct {
	// Now is the creation time; zero means the current time.
	Now time.Time
	// Tool is the version of the tool, for the manifest.
	Tool string
	// Subject is the DIMM the case is about, if the sender named one.
	Subject *Subject
	// Anonymize replaces host names and network addresses.
	Anonymize bool
	// Key makes the replacement repeatable: the same key gives the same
	// words for the same names. Without it a random key is used and
	// thrown away.
	Key []byte
	// Force replaces a file that exists.
	Force bool
	// TempDir is where the copy of the ledger is prepared; empty means the
	// system temporary directory.
	TempDir string
	// Inputs are the files the records were read from: the manifest lists
	// each with its size and checksum.
	Inputs []InputFile
	// WithInputs puts those files into the bundle. A vendor log holds
	// logins, addresses and user names, which a bundle otherwise never
	// holds; it cannot go with Anonymize.
	WithInputs bool
}

// Written describes a bundle that was written.
type Written struct {
	Path     string
	Bytes    int64
	Manifest Manifest
	// Names maps each host name that was replaced to the word that took
	// its place. It is for the sender; it is not in the bundle.
	Names map[string]string
}

// Write writes the ledger as a bundle to path. The file is written beside
// its target and moved into place, so a bundle found under the name is
// whole; a device or a pipe under the name is written to as it is.
func Write(ctx context.Context, s *store.Store, path string, opts Options) (*Written, error) {
	// Before the ledger is copied: a name that is taken, or a directory
	// that is not there, needs no work to find out.
	if err := outfile.Check(path, opts.Force); err != nil {
		return nil, err
	}
	w, emit, cleanup, err := prepare(ctx, s, opts)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	n, err := outfile.Write(path, opts.Force, emit)
	if err != nil {
		return nil, err
	}
	w.Path, w.Bytes = path, n
	return w, nil
}

// WriteTo writes the ledger as a bundle to out: a pipe to another host, or
// the standard output of a command run over ssh.
func WriteTo(ctx context.Context, s *store.Store, out io.Writer, opts Options) (*Written, error) {
	w, emit, cleanup, err := prepare(ctx, s, opts)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	c := &countWriter{w: out}
	if err := emit(c); err != nil {
		return nil, err
	}
	w.Bytes = c.n
	return w, nil
}

// countWriter counts what passes through it.
type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// prepare makes the content of a bundle: a copy of the ledger, anonymized
// when asked, and what describes it. emit writes the archive; cleanup
// removes the copy.
func prepare(ctx context.Context, s *store.Store, opts Options) (w *Written, emit func(io.Writer) error, cleanup func(), err error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC().Truncate(time.Second)
	if opts.WithInputs && opts.Anonymize {
		return nil, nil, nil, ErrInputsAnonymized
	}

	tmp, err := os.MkdirTemp(opts.TempDir, "my-bundle-")
	if err != nil {
		return nil, nil, nil, err
	}
	// An error return sets cleanup to nil: the copy is removed through a
	// function of its own.
	remove := func() { os.RemoveAll(tmp) }
	cleanup = remove
	defer func() {
		if err != nil {
			remove()
		}
	}()
	ledger := filepath.Join(tmp, ledgerName)
	if err = s.Snapshot(ctx, ledger); err != nil {
		return nil, nil, nil, err
	}

	w = &Written{}
	m := &w.Manifest
	m.Format, m.FormatVersion, m.CreatedAt, m.Tool = Format, Version, now, opts.Tool
	m.NotCollected = NotCollected
	if opts.WithInputs && len(opts.Inputs) > 0 {
		m.NotCollected = NotCollectedWithInputs
	}
	if opts.Subject != nil {
		sub := *opts.Subject
		m.Subject = &sub
	}
	var anon *anonymizer
	if opts.Anonymize {
		a, aerr := newAnonymizer(opts.Key)
		if aerr != nil {
			return nil, nil, nil, aerr
		}
		if aerr := a.run(ctx, ledger); aerr != nil {
			return nil, nil, nil, fmt.Errorf("anonymize: %w", aerr)
		}
		anon = a
		m.Anonymized = true
		m.Anonymization = &Anonymization{Hosts: len(a.names), Addresses: len(a.addresses), Users: len(a.users), Keyed: len(opts.Key) > 0}
		w.Names = a.names
		if m.Subject != nil {
			m.Subject.Host = a.host(m.Subject.Host)
		}
	}

	// Describe the copy: it is what the bundle holds.
	c, err := store.Open(ctx, ledger, store.ReadOnly)
	if err != nil {
		return nil, nil, nil, err
	}
	src, err := describe(ctx, c, m)
	cerr := c.Close()
	if err == nil {
		err = cerr
	}
	if err != nil {
		return nil, nil, nil, err
	}
	sum, size, err := checksum(ledger)
	if err != nil {
		return nil, nil, nil, err
	}
	m.Ledger = LedgerInfo{File: ledgerName, SchemaVersion: m.Ledger.SchemaVersion, Bytes: size, SHA256: sum}
	travel, err := listInputs(ctx, m, opts, anon)
	if err != nil {
		return nil, nil, nil, err
	}

	manifest, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, nil, nil, err
	}
	sources, err := json.MarshalIndent(src, "", "  ")
	if err != nil {
		return nil, nil, nil, err
	}
	text := readme(m)

	emit = func(out io.Writer) error {
		lf, err := os.Open(ledger)
		if err != nil {
			return err
		}
		defer lf.Close()
		zw := zip.NewWriter(out)
		for _, member := range []struct {
			name string
			r    io.Reader
		}{
			{manifestName, bytes.NewReader(append(manifest, '\n'))},
			{sourcesName, bytes.NewReader(append(sources, '\n'))},
			{readmeName, strings.NewReader(text)},
			{ledgerName, lf},
		} {
			f, err := zw.CreateHeader(&zip.FileHeader{Name: member.name, Method: zip.Deflate, Modified: now})
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, member.r); err != nil {
				return err
			}
		}
		for _, in := range travel {
			if err := addInput(zw, in, now); err != nil {
				return err
			}
		}
		return zw.Close()
	}
	return w, emit, cleanup, nil
}

// describe fills the manifest from the ledger and returns the source
// inventory.
func describe(ctx context.Context, s *store.Store, m *Manifest) (*Sources, error) {
	v, err := s.Version(ctx)
	if err != nil {
		return nil, err
	}
	m.Ledger.SchemaVersion = v

	hosts, err := s.Hosts(ctx)
	if err != nil {
		return nil, err
	}
	m.Hosts = []Host{}
	for _, h := range hosts {
		row := Host{Name: h.Name}
		totals, err := s.Totals(ctx, store.EventFilter{Host: h.Name})
		if err != nil {
			return nil, err
		}
		for _, t := range totals {
			row.CE += t.CE
			row.UE += t.UE
			row.Records += t.Records
			if !t.First.IsZero() && t.First.Year() > 1970 && (row.First.IsZero() || t.First.Before(row.First)) {
				row.First = t.First
			}
			if t.Last.After(row.Last) {
				row.Last = t.Last
			}
		}
		occupants, err := s.SlotOccupants(ctx, h.Name)
		if err != nil {
			return nil, err
		}
		row.DIMMs = len(occupants)
		boots, err := s.Boots(ctx, h.Name)
		if err != nil {
			return nil, err
		}
		for _, b := range boots {
			if b.StartQuality == store.QualityUnknown {
				continue // a boot the ledger put before a record it read
			}
			row.Boots++
			switch b.End {
			case model.BootStop:
				row.Stops++
			case model.BootPanic:
				row.Panics++
			}
		}
		m.Hosts = append(m.Hosts, row)
	}

	counts, err := tableRows(ctx, s.Path())
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	m.Collected = []Item{}
	for _, name := range names {
		holds, ok := tables[name]
		if !ok {
			holds = "table " + name
		}
		m.Collected = append(m.Collected, Item{Table: name, Rows: counts[name], Holds: holds})
	}

	scans, err := s.Scans(ctx)
	if err != nil {
		return nil, err
	}
	src := &Sources{Scans: []ScanSources{}}
	for _, sc := range scans {
		list, err := s.Sources(ctx, sc.ID)
		if err != nil {
			return nil, err
		}
		if list == nil {
			list = []model.Source{}
		}
		src.Scans = append(src.Scans, ScanSources{Scan: sc, Sources: list})
		for i := range m.Hosts {
			h := &m.Hosts[i]
			if h.Name != sc.Host {
				continue
			}
			for _, x := range list {
				if x.Records > 0 && !slices.Contains(h.Sources, x.Kind) {
					h.Sources = append(h.Sources, x.Kind)
				}
			}
			sort.Strings(h.Sources)
		}
	}
	return src, nil
}

// openLedger opens a ledger file with plain SQL access.
func openLedger(path, mode string) (*sql.DB, error) {
	esc := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(path)
	db, err := sql.Open("sqlite", "file:"+esc+"?mode="+mode+"&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// tableNames lists the tables of a ledger.
func tableNames(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

func quote(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

// tableRows counts the rows of every table of a ledger.
func tableRows(ctx context.Context, path string) (map[string]int64, error) {
	db, err := openLedger(path, "ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	names, err := tableNames(ctx, db)
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for _, name := range names {
		var n int64
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM `+quote(name)).Scan(&n); err != nil {
			return nil, err
		}
		out[name] = n
	}
	return out, nil
}

func checksum(path string) (sum string, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	size, err = io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), size, err
}

// IsBundle reports whether the file at path starts like a ZIP archive.
func IsBundle(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 4)
	_, err = io.ReadFull(f, head)
	return err == nil && bytes.Equal(head, []byte("PK\x03\x04"))
}

// member returns the named member of an archive.
func member(zr *zip.Reader, name string) *zip.File {
	for _, f := range zr.File {
		if f.Name == name {
			return f
		}
	}
	return nil
}

func readMember(zr *zip.Reader, name string, limit int64) ([]byte, error) {
	f := member(zr, name)
	if f == nil {
		return nil, fmt.Errorf("%w: no %s", ErrDamaged, name)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrDamaged, name, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrDamaged, name, err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%w: %s is larger than %d bytes", ErrDamaged, name, limit)
	}
	return b, nil
}

// readManifest reads and checks the manifest of an open archive.
func readManifest(zr *zip.Reader) (*Manifest, error) {
	if member(zr, manifestName) == nil {
		return nil, ErrNotBundle
	}
	b, err := readMember(zr, manifestName, 16<<20)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrDamaged, manifestName, err)
	}
	switch {
	case m.Format != Format:
		return nil, ErrNotBundle
	case m.FormatVersion > Version:
		return nil, fmt.Errorf("%w (format %d, this tool reads up to %d)", ErrNewerFormat, m.FormatVersion, Version)
	case m.FormatVersion < 1:
		return nil, fmt.Errorf("%w: format version %d", ErrDamaged, m.FormatVersion)
	}
	return &m, nil
}

// ReadManifest returns the manifest of the bundle at path without taking
// the ledger out.
func ReadManifest(path string) (*Manifest, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		if errors.Is(err, zip.ErrFormat) {
			return nil, fmt.Errorf("%s: %w", path, ErrNotBundle)
		}
		return nil, err
	}
	defer zr.Close()
	m, err := readManifest(&zr.Reader)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// ReadSources returns the source inventory of the bundle at path.
func ReadSources(path string) (*Sources, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	b, err := readMember(&zr.Reader, sourcesName, 256<<20)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var s Sources
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s: %w: %s: %v", path, ErrDamaged, sourcesName, err)
	}
	return &s, nil
}

// Case is a bundle that was opened: its manifest, and its ledger taken out
// into a private directory.
type Case struct {
	Manifest Manifest
	// Ledger is the path of the ledger file. It belongs to the Case and
	// is removed by Close.
	Ledger string
	dir    string
}

// Close removes what Open put on disk.
func (c *Case) Close() error {
	if c == nil || c.dir == "" {
		return nil
	}
	return os.RemoveAll(c.dir)
}

// Open takes the ledger out of the bundle at path and checks it against
// the manifest. The bundle is not changed. tmpDir is where the ledger is
// put; empty means the system temporary directory.
func Open(ctx context.Context, path, tmpDir string) (*Case, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		if errors.Is(err, zip.ErrFormat) {
			return nil, fmt.Errorf("%s: %w", path, ErrNotBundle)
		}
		return nil, err
	}
	defer zr.Close()
	m, err := readManifest(&zr.Reader)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	name := m.Ledger.File
	if name == "" {
		name = ledgerName
	}
	lf := member(&zr.Reader, name)
	if lf == nil {
		return nil, fmt.Errorf("%s: %w: no %s", path, ErrDamaged, name)
	}
	if lf.UncompressedSize64 > maxLedger {
		return nil, fmt.Errorf("%s: %w: the ledger is larger than %d bytes", path, ErrDamaged, int64(maxLedger))
	}

	dir, err := os.MkdirTemp(tmpDir, "my-case-")
	if err != nil {
		return nil, err
	}
	c := &Case{Manifest: *m, Ledger: filepath.Join(dir, ledgerName), dir: dir}
	fail := func(err error) (*Case, error) {
		c.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	rc, err := lf.Open()
	if err != nil {
		return fail(fmt.Errorf("%w: %v", ErrDamaged, err))
	}
	out, err := os.OpenFile(c.Ledger, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		rc.Close()
		return fail(err)
	}
	h := sha256.New()
	// The size in the archive header is a claim: stop at the bound whatever
	// it says.
	n, err := copyContext(ctx, io.MultiWriter(out, h), io.LimitReader(rc, maxLedger+1))
	rc.Close()
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil && ctx.Err() != nil:
		return fail(ctx.Err())
	case err != nil:
		return fail(fmt.Errorf("%w: %s: %v", ErrDamaged, name, err))
	case n > maxLedger:
		return fail(fmt.Errorf("%w: the ledger is larger than %d bytes", ErrDamaged, int64(maxLedger)))
	}
	if sum := hex.EncodeToString(h.Sum(nil)); m.Ledger.SHA256 != "" && sum != m.Ledger.SHA256 {
		return fail(fmt.Errorf("%w: the ledger does not match the checksum in the manifest", ErrDamaged))
	}

	// A ledger of an older schema is brought up to date in the copy.
	s, err := store.Open(ctx, c.Ledger, store.ReadOnly)
	if errors.Is(err, store.ErrNeedsMigration) {
		s, err = store.Open(ctx, c.Ledger, store.ReadWrite)
	}
	if err != nil {
		return fail(err)
	}
	if err := s.Close(); err != nil {
		return fail(err)
	}
	return c, nil
}

// copyContext copies until the reader ends or the context is cancelled.
func copyContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, 1<<20)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return total, werr
			}
			total += int64(n)
		}
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}

// traveller is a file that goes into the bundle, with the size and
// checksum the manifest gives for it.
type traveller struct {
	path, member string
	bytes        int64
	sum          string
}

// listInputs lists the files the records were read from in the manifest
// and returns those that travel in the bundle. A name goes through the
// anonymizer of the bundle when there is one.
func listInputs(ctx context.Context, m *Manifest, opts Options, anon *anonymizer) ([]traveller, error) {
	var travel []traveller
	taken := map[string]bool{}
	for _, in := range opts.Inputs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sum, size, err := checksum(in.Path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", in.Name, err)
		}
		name := inputName(in.Name)
		if anon != nil {
			name = anon.text(name)
		}
		// Two files of the same name stay apart.
		base := name
		for i := 2; taken[name]; i++ {
			name = fmt.Sprintf("%s-%d", base, i)
		}
		taken[name] = true
		row := Input{Name: name, Bytes: size, SHA256: sum}
		if opts.WithInputs {
			row.Member = inputsDir + name
			travel = append(travel, traveller{path: in.Path, member: row.Member, bytes: size, sum: sum})
		}
		m.Inputs = append(m.Inputs, row)
	}
	return travel, nil
}

// inputName makes a name of the manifest and of a member: slashes only,
// no directory above the file, no element that climbs.
func inputName(name string) string {
	var parts []string
	for _, p := range strings.Split(filepath.ToSlash(name), "/") {
		switch p {
		case "", ".", "..":
			continue
		}
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		return "input"
	}
	return strings.Join(parts, "/")
}

// addInput writes a file into the bundle, and fails when it is no longer
// the file the manifest describes.
func addInput(zw *zip.Writer, in traveller, now time.Time) error {
	f, err := os.Open(in.path)
	if err != nil {
		return err
	}
	defer f.Close()
	w, err := zw.CreateHeader(&zip.FileHeader{Name: in.member, Method: zip.Deflate, Modified: now})
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(w, h), f)
	if err != nil {
		return err
	}
	if n != in.bytes || hex.EncodeToString(h.Sum(nil)) != in.sum {
		return fmt.Errorf("%s changed while the bundle was written", in.member)
	}
	return nil
}
