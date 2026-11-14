// Package rasdaemon reads the SQLite database rasdaemon keeps
// (ras-mc_event.db). It understands the tables of rasdaemon 0.6 to 1.0 and
// reads columns by name, because a database that was upgraded in place has
// its newer columns appended in a different order than a fresh one.
package rasdaemon

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/decode/cper"
	"github.com/agenticode/my/internal/decode/edac"
	"github.com/agenticode/my/internal/decode/mce"
	"github.com/agenticode/my/internal/model"
)

// Kind is the adapter name in the source inventory.
const Kind = "rasdaemon"

// DefaultPath is where rasdaemon keeps its database unless configured otherwise.
const DefaultPath = "/var/lib/rasdaemon/ras-mc_event.db"

// Options control how the database is opened.
type Options struct {
	// Direct reads the file in place. The default is to read a private
	// snapshot copy, which takes no SQLite lock on a file rasdaemon may be
	// writing to. Use Direct for a file nothing writes to, such as a
	// database a customer sent.
	Direct bool
	// TempDir is where the snapshot copy is made; empty means the system
	// temporary directory. The copy is removed when Collect returns.
	TempDir string
}

// Adapter reads one rasdaemon database.
type Adapter struct {
	path string
	opts Options
}

// New returns an adapter for the database at path.
func New(path string, opts Options) *Adapter { return &Adapter{path: path, opts: opts} }

func (a *Adapter) Kind() string   { return Kind }
func (a *Adapter) Origin() string { return a.path }

// tableUse says what the adapter does with a rasdaemon table.
var tableUse = map[string]string{
	"mc_event":                "events",
	"mce_record":              "events",
	"extlog_event":            "events",
	"cxl_dram_event":          "events",
	"cxl_general_media_event": "events",
	"memory_failure_event":    "actions",
	// Known rasdaemon tables that do not describe memory errors.
	"aer_event":               "PCIe errors",
	"non_standard_event":      "vendor-specific records",
	"arm_event":               "ARM processor errors",
	"devlink_event":           "network device health",
	"disk_errors":             "block device errors",
	"signal_event":            "signals",
	"cxl_poison_event":        "CXL poison list records",
	"cxl_aer_ue_event":        "CXL link errors",
	"cxl_aer_ce_event":        "CXL link errors",
	"cxl_overflow_event":      "CXL event log overflow",
	"cxl_generic_event":       "CXL generic records",
	"cxl_memory_module_event": "CXL device health records",
}

// Collect reads the database into sink.
func (a *Adapter) Collect(ctx context.Context, sink adapter.Sink) adapter.Report {
	rep := adapter.Report{Kind: Kind, Origin: a.path}
	fail := func(err error) adapter.Report {
		rep.Status, rep.Detail = adapter.StatusForError(err)
		return rep
	}
	fi, err := os.Stat(a.path)
	if err != nil {
		return fail(err)
	}
	if fi.IsDir() {
		rep.Status, rep.Detail = model.SourceError, "is a directory"
		return rep
	}

	dbPath, params := a.path, "mode=ro&immutable=1"
	if !a.opts.Direct {
		snap, err := takeSnapshot(ctx, a.path, a.opts.TempDir)
		if err != nil {
			return fail(err)
		}
		defer snap.remove()
		dbPath = snap.db
		if snap.journal {
			// The copy carries an unfinished transaction; SQLite must be
			// allowed to roll it back in the copy.
			params = "mode=rw"
		}
		if snap.unstable {
			rep.Notes = append(rep.Notes, "the database was being written during every copy attempt; the snapshot may miss the latest records")
		}
	}
	esc := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(dbPath)
	db, err := sql.Open("sqlite", "file:"+esc+"?"+params+"&_pragma=busy_timeout(2000)")
	if err != nil {
		return fail(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(2)

	var check string
	if err := db.QueryRowContext(ctx, `PRAGMA quick_check(1)`).Scan(&check); err != nil {
		rep.Status = model.SourceError
		rep.Detail = "not a readable SQLite database: " + err.Error()
		return rep
	}
	if check != "ok" {
		rep.Status = model.SourceError
		rep.Detail = "database fails the SQLite integrity check: " + check
		return rep
	}

	tables, err := listTables(ctx, db)
	if err != nil {
		return fail(err)
	}
	known := 0
	for _, t := range tables {
		if _, ok := tableUse[t]; ok {
			known++
		}
	}
	if known == 0 {
		rep.Status = model.SourceError
		rep.Detail = "no rasdaemon tables found"
		if len(tables) > 0 {
			rep.Detail += " (tables present: " + strings.Join(tables, ", ") + ")"
		}
		return rep
	}

	r := &reader{ctx: ctx, db: db, sink: sink, path: a.path, rep: &rep}
	// mc_event first: machine check records are matched against it.
	order := []struct {
		table string
		fn    func(*row) error
	}{
		{"mc_event", r.mcEvent},
		{"mce_record", r.mceRecord},
		{"extlog_event", r.extlogEvent},
		{"cxl_dram_event", r.cxlDRAM},
		{"cxl_general_media_event", r.cxlGeneralMedia},
		{"memory_failure_event", r.memoryFailure},
	}
	has := map[string]bool{}
	for _, t := range tables {
		has[t] = true
	}
	for _, step := range order {
		if !has[step.table] {
			continue
		}
		if err := r.scan(step.table, step.fn); err != nil {
			var se sinkError
			if errors.As(err, &se) {
				rep.Status, rep.Detail, rep.SinkErr = model.SourceError, "ledger rejected a record: "+se.err.Error(), se.err
				return rep
			}
			rep.Status = model.SourceError
			rep.Detail = fmt.Sprintf("table %s: %v", step.table, err)
			return rep
		}
	}
	if r.notMemory > 0 {
		rep.Notes = append(rep.Notes, fmt.Sprintf("mce_record: %d machine checks are not memory errors and were left out", r.notMemory))
	}
	if r.badCPER > 0 {
		rep.Notes = append(rep.Notes, fmt.Sprintf("extlog_event: %d records have a CPER blob that could not be decoded; they were kept without location", r.badCPER))
	}
	// Say what is in the database and was not interpreted.
	for _, t := range tables {
		use, ok := tableUse[t]
		if ok && (use == "events" || use == "actions") {
			continue
		}
		var n int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM "`+strings.ReplaceAll(t, `"`, `""`)+`"`).Scan(&n); err != nil || n == 0 {
			continue
		}
		if ok {
			rep.Notes = append(rep.Notes, fmt.Sprintf("table %s: %d rows not read (%s)", t, n, use))
		} else {
			rep.Notes = append(rep.Notes, fmt.Sprintf("table %s: %d rows not read (unknown table)", t, n))
		}
	}

	rep.Skipped = r.tally.Total()
	rep.Notes = append(rep.Notes, r.tally.Notes()...)
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

func listTables(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// sinkError marks an error returned by the Sink, as opposed to one of the
// database.
type sinkError struct{ err error }

func (e sinkError) Error() string { return e.err.Error() }

// row is one database row with access by column name. SQLite columns are
// dynamically typed, so every accessor accepts what it finds.
type row struct {
	table string
	names []string
	index map[string]int
	vals  []any
}

func (r *row) get(name string) any {
	i, ok := r.index[name]
	if !ok {
		return nil
	}
	return r.vals[i]
}

// int returns an integer column. Text that holds a number is accepted.
func (r *row) int(name string) (int64, bool) {
	switch v := r.get(name).(type) {
	case int64:
		return v, true
	case float64:
		if v == float64(int64(v)) {
			return int64(v), true
		}
	case string:
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 0, 64); err == nil {
			return n, true
		}
	case []byte:
		if n, err := strconv.ParseInt(strings.TrimSpace(string(v)), 0, 64); err == nil {
			return n, true
		}
	}
	return 0, false
}

func (r *row) str(name string) string {
	switch v := r.get(name).(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64)
	}
	return ""
}

func (r *row) blob(name string) []byte {
	switch v := r.get(name).(type) {
	case []byte:
		return v
	case string:
		return []byte(v)
	}
	return nil
}

// raw renders the row as JSON in column order, leaving out NULLs. Blobs
// are written as hexadecimal.
func (r *row) raw() string {
	var b strings.Builder
	b.WriteByte('{')
	first := true
	for i, name := range r.names {
		v := r.vals[i]
		if v == nil {
			continue
		}
		if !first {
			b.WriteByte(',')
		}
		first = false
		k, _ := json.Marshal(name)
		b.Write(k)
		b.WriteByte(':')
		switch x := v.(type) {
		case []byte:
			j, _ := json.Marshal("0x" + hex.EncodeToString(x))
			b.Write(j)
		default:
			j, err := json.Marshal(x)
			if err != nil {
				j = []byte(`"?"`)
			}
			b.Write(j)
		}
	}
	b.WriteByte('}')
	return b.String()
}

// reader carries the state of one Collect call.
type reader struct {
	ctx  context.Context
	db   *sql.DB
	sink adapter.Sink
	path string
	rep  *adapter.Report

	tally     adapter.Tally
	notMemory int
	badCPER   int
	// mcIndex lists the mc_event rows by time, to find the EDAC record of
	// a machine check.
	mcIndex []mcEntry
	sorted  bool
}

type mcEntry struct {
	unix     int64
	id       int64
	addrLine uint64 // address >> 6, valid when hasAddr
	hasAddr  bool
	top, mid int
	used     bool
}

func (r *reader) scan(table string, fn func(*row) error) error {
	rows, err := r.db.QueryContext(r.ctx, `SELECT * FROM "`+table+`" ORDER BY rowid`)
	if err != nil {
		return err
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		return err
	}
	rw := &row{table: table, names: names, index: make(map[string]int, len(names))}
	for i, n := range names {
		rw.index[n] = i
	}
	ptrs := make([]any, len(names))
	for rows.Next() {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		rw.vals = make([]any, len(names))
		for i := range rw.vals {
			ptrs[i] = &rw.vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		r.rep.Records++
		if err := fn(rw); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (r *reader) ref(rw *row) (string, int64) {
	id, _ := rw.int("id")
	return fmt.Sprintf("%s#%s/%d", r.path, rw.table, id), id
}

// parseTimestamp reads the time text rasdaemon writes with
// strftime("%Y-%m-%d %H:%M:%S %z").
func parseTimestamp(s string) (time.Time, model.Opt[int], bool) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse("2006-01-02 15:04:05 -0700", s); err == nil {
		_, off := t.Zone()
		return t.UTC(), model.Some(off), true
	}
	// A timestamp without an offset is taken as UTC.
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t.UTC(), model.Opt[int]{}, true
	}
	return time.Time{}, model.Opt[int]{}, false
}

// base fills the fields every event of this adapter shares. It reports
// false, after noting the reason, when the row has no usable time.
func (r *reader) base(rw *row, e *model.Event) bool {
	ref, id := r.ref(rw)
	t, off, ok := parseTimestamp(rw.str("timestamp"))
	if !ok {
		r.tally.Skip(ref, "timestamp is empty or unreadable")
		return false
	}
	e.Kind = model.KindError
	e.ErrorDT, e.UTCOffset, e.TimeQuality = t, off, model.TimeExact
	e.Source = Kind + "/" + rw.table
	e.RawRef = ref
	e.Raw = rw.raw()
	e.Count = 1
	e.Fingerprint = fingerprint(rw.table, id, t)
	return true
}

func fingerprint(table string, id int64, t time.Time) string {
	return model.Fingerprint(Kind, table, strconv.FormatInt(id, 10), strconv.FormatInt(t.Unix(), 10))
}

func (r *reader) emit(e *model.Event) error {
	e.Grade = e.ComputeGrade()
	if err := r.sink.Event(e); err != nil {
		return sinkError{err}
	}
	return nil
}

func optFromInt(v int64, ok bool) model.Opt[int] {
	if !ok || v < 0 || v > 1<<31 {
		return model.Opt[int]{}
	}
	return model.Some(int(v))
}

// mcEvent turns one EDAC memory controller event into a ledger event.
func (r *reader) mcEvent(rw *row) error {
	var e model.Event
	if !r.base(rw, &e) {
		return nil
	}
	detail := edac.ParseDetail(rw.str("driver_detail"))
	sev, ok := edac.Severity(rw.str("err_type"), detail.Recoverable)
	if !ok {
		r.tally.Skip(e.RawRef, fmt.Sprintf("unknown error type %q", rw.str("err_type")))
		return nil
	}
	e.ErrorSeverity = sev
	msg := rw.str("err_msg")
	e.ErrorType = msg
	e.ErrorScrubType = edac.ScrubType(msg)
	if n, ok := rw.int("err_count"); ok && n > 1 && n < 1<<31 {
		e.Count = int(n)
	}
	if mc, ok := rw.int("mc"); ok {
		e.EdacMC = optFromInt(mc, true)
	}
	top, _ := rw.int("top_layer")
	mid, _ := rw.int("middle_layer")
	low, _ := rw.int("lower_layer")
	if _, ok := rw.int("top_layer"); !ok {
		top = -1
	}
	if _, ok := rw.int("middle_layer"); !ok {
		mid = -1
	}
	if _, ok := rw.int("lower_layer"); !ok {
		low = -1
	}
	layers := edac.LayersFromIndices(int(top), int(mid), int(low))
	e.EdacLocation = layers.Key()

	addr, hasAddr := rw.int("address")
	if hasAddr && addr > 0 {
		e.OsPhysicalAddress = model.Some(uint64(addr))
	} else {
		hasAddr = false
	}
	if syn, ok := rw.int("syndrome"); ok && syn != 0 {
		e.Syndrome = model.Some(uint64(syn))
	}

	label := rw.str("label")
	detail.Apply(&e)
	lbl := edac.ParseLabel(label)
	lbl.Apply(&e)
	switch {
	case detail.Decoder == edac.DecoderGHES:
		// The GHES detail names the DIMM by its DMI locator; the type of
		// the error tells whether the scrubber found it.
		if code, ok := cper.ErrorTypeCode(msg); ok && e.ErrorScrubType == "" {
			e.ErrorScrubType = cper.ScrubType(model.Some(code))
		}
		if detail.DIMMLocation != "" {
			label = detail.DIMMLocation
		}
	case strings.HasPrefix(lbl.Kind, "intel") || detail.Decoder == edac.DecoderSKX || detail.Decoder == edac.DecoderADXL || detail.Decoder == edac.DecoderSB:
		// The Intel drivers' layers are channel and slot.
		if len(layers.Index) >= 2 {
			if !e.ChannelId.Valid {
				e.ChannelId = model.Some(layers.Index[0])
			}
			if !e.ChannelSlotId.Valid {
				e.ChannelSlotId = model.Some(layers.Index[1])
			}
		}
	}
	if !edac.PlaceholderLabel(label) {
		e.Label = label
	}

	if err := r.emit(&e); err != nil {
		return err
	}
	_, id := r.ref(rw)
	ent := mcEntry{unix: e.ErrorDT.Unix(), id: id, hasAddr: hasAddr, top: int(top), mid: int(mid)}
	if hasAddr {
		ent.addrLine = uint64(addr) >> 6
	}
	r.mcIndex = append(r.mcIndex, ent)
	r.sorted = false
	return nil
}

// findMC returns the unused mc_event entry that reports the same error as
// a machine check at time unix: same cache line, or same chip select and
// channel when the addresses are of different kinds.
func (r *reader) findMC(unix int64, addrLine uint64, hasAddr bool, channel, csrow model.Opt[int]) *mcEntry {
	if !r.sorted {
		sort.SliceStable(r.mcIndex, func(i, j int) bool { return r.mcIndex[i].unix < r.mcIndex[j].unix })
		r.sorted = true
	}
	lo := sort.Search(len(r.mcIndex), func(i int) bool { return r.mcIndex[i].unix >= unix-1 })
	var byLayer *mcEntry
	for i := lo; i < len(r.mcIndex) && r.mcIndex[i].unix <= unix+1; i++ {
		ent := &r.mcIndex[i]
		if ent.used {
			continue
		}
		if hasAddr && ent.hasAddr && ent.addrLine == addrLine {
			return ent
		}
		if byLayer == nil && channel.Valid && csrow.Valid && ent.top == csrow.V && ent.mid == channel.V {
			byLayer = ent
		}
	}
	return byLayer
}

// mceRecord turns a machine check record into a ledger event if it reports
// a memory error.
func (r *reader) mceRecord(rw *row) error {
	ref, _ := r.ref(rw)
	status, _ := rw.int("status")
	if !mce.Valid(uint64(status)) {
		r.tally.Skip(ref, "machine check status register is not valid")
		return nil
	}
	vendor, _ := rw.int("cpuvendor")
	bank, _ := rw.int("bank")
	addr, _ := rw.int("addr")
	misc, _ := rw.int("misc")
	ipid, _ := rw.int("ipid")
	synd, _ := rw.int("synd")
	dec := mce.Decode(mce.Record{Vendor: int(vendor), Bank: int(bank), Status: uint64(status), Addr: uint64(addr),
		Misc: uint64(misc), IPID: uint64(ipid), Synd: uint64(synd)})
	errorMsg, statusMsg := rw.str("error_msg"), rw.str("mcastatus_msg")
	if !dec.Memory && !mce.IsMemoryText(errorMsg, statusMsg) {
		r.notMemory++
		return nil
	}

	var e model.Event
	if !r.base(rw, &e) {
		return nil
	}
	e.ErrorSeverity = dec.Severity
	e.ErrorScrubType = dec.Scrub
	text := strings.ToLower(errorMsg + " " + statusMsg)
	if e.ErrorScrubType == "" {
		switch {
		case strings.Contains(text, "scrub"):
			e.ErrorScrubType = model.ScrubPatrol
		case strings.Contains(text, "memory read") || strings.Contains(text, "rd_channel"):
			e.ErrorScrubType = model.ScrubDemand
		}
	}
	e.ErrorType = dec.ErrorType
	if e.ErrorType == "" {
		e.ErrorType = strings.TrimSuffix(strings.TrimSpace(statusMsg), ".")
	}
	amd := vendor == mce.VendorAMD || vendor == mce.VendorHygon
	e.Decoder = "mce/intel"
	if amd {
		e.Decoder = "mce/amd"
	}
	hasAddr := dec.Address.Valid && addr != 0
	// The address of an AMD memory controller bank is normalized to the
	// controller, not a system physical address. It stays in the raw record.
	if hasAddr && !amd {
		e.OsPhysicalAddress = model.Some(uint64(addr))
	}
	if sock, ok := rw.int("socketid"); ok {
		e.SocketId = optFromInt(sock, true)
	}
	loc := mce.ParseLocation(rw.str("mc_location"))
	var channel, csrow model.Opt[int]
	if v, ok := loc["memory_channel"]; ok {
		channel = model.Some(v)
	} else if dec.Channel.Valid {
		channel = dec.Channel
	}
	if v, ok := loc["csrow"]; ok {
		csrow = model.Some(v)
	} else if dec.ChipSelect.Valid {
		csrow = dec.ChipSelect
	}
	e.ChannelId = channel
	if csrow.Valid {
		// Two chip selects per DIMM, one per rank.
		e.ChannelSlotId, e.Rank = model.Some(csrow.V>>1), model.Some(csrow.V&1)
	} else if v, ok := loc["rank"]; ok {
		e.Rank = model.Some(v)
	}

	if ent := r.findMC(e.ErrorDT.Unix(), uint64(addr)>>6, hasAddr, channel, csrow); ent != nil {
		ent.used = true
		e.DupOfFingerprint = fingerprint("mc_event", ent.id, time.Unix(ent.unix, 0))
	}
	return r.emit(&e)
}

// extlogEvent turns a firmware error record logged through the extended
// error log into a ledger event.
func (r *reader) extlogEvent(rw *row) error {
	var e model.Event
	if !r.base(rw, &e) {
		return nil
	}
	sevCode, _ := rw.int("severity")
	sev, ok := cper.Severity(int(sevCode))
	if !ok {
		r.tally.Skip(e.RawRef, fmt.Sprintf("unknown CPER severity %d", sevCode))
		return nil
	}
	e.ErrorSeverity = sev
	e.Decoder = "cper"
	var m cper.MemError
	if blob := rw.blob("cper_data"); len(blob) > 0 {
		var err error
		if m, err = cper.ParseCompact(blob); err != nil {
			r.badCPER++
			m = cper.MemError{}
		}
	}
	// 255 marks an error type the firmware did not provide.
	if et, ok := rw.int("etype"); ok && et >= 0 && et < 255 {
		m.ErrorType = model.Some(int(et))
	}
	// An address of all ones marks an address the firmware did not provide.
	if addr, ok := rw.int("address"); ok && addr > 0 {
		m.PhysicalAddress = model.Some(uint64(addr))
	}
	m.Apply(&e)
	if fru := strings.TrimSpace(rw.str("fru_text")); fru != "" {
		e.Label = fru
	}
	return r.emit(&e)
}

// cxlCommon fills what the two CXL media event tables share. Location
// columns are only meaningful when the device marked them valid, and
// rasdaemon stores zero otherwise without keeping the validity flags.
func (r *reader) cxlCommon(rw *row, e *model.Event) bool {
	if !r.base(rw, e) {
		return false
	}
	e.Decoder = "cxl"
	desc, _ := rw.int("descriptor")
	typ, _ := rw.int("type")
	trans, _ := rw.int("transaction_type")
	e.ErrorSeverity = model.SevCE
	if desc&1 != 0 {
		e.ErrorSeverity = model.SevUE
	}
	switch {
	case typ == 1 || trans == 5: // scrub media ECC error, internal media scrub
		e.ErrorScrubType = model.ScrubPatrol
	case trans == 1 || trans == 2: // host read, host write
		e.ErrorScrubType = model.ScrubDemand
	default:
		e.ErrorScrubType = model.ScrubOther
	}
	switch typ {
	case 0:
		e.ErrorType = "media ECC error"
	case 1:
		e.ErrorType = "scrub media ECC error"
	case 2:
		e.ErrorType = "invalid address"
	case 3:
		e.ErrorType = "data path error"
	}
	// The host physical address is all ones when the device address is not
	// mapped into a region.
	if hpa, ok := rw.int("hpa"); ok && hpa > 0 {
		e.OsPhysicalAddress = model.Some(uint64(hpa))
	}
	if dev := rw.str("memdev"); dev != "" {
		e.Label = "cxl:" + dev
	}
	return true
}

func (r *reader) cxlDRAM(rw *row) error {
	var e model.Event
	if !r.cxlCommon(rw, &e) {
		return nil
	}
	bg, _ := rw.int("bank_group")
	bank, _ := rw.int("bank")
	rowN, _ := rw.int("row")
	col, _ := rw.int("column")
	nibble, _ := rw.int("nibble_mask")
	// A DRAM location of all zeros with no correction mask is taken as
	// "not reported": the validity flags are not in the database.
	if bg != 0 || bank != 0 || rowN != 0 || col != 0 || len(rw.blob("cor_mask")) > 0 {
		e.BankGroup, e.Bank = optFromInt(bg, true), optFromInt(bank, true)
		e.Row, e.Column = optFromInt(rowN, true), optFromInt(col, true)
		if ch, ok := rw.int("channel"); ok {
			e.ChannelId = optFromInt(ch, true)
		}
		if rk, ok := rw.int("rank"); ok {
			e.Rank = optFromInt(rk, true)
		}
		if sc, ok := rw.int("sub_channel"); ok {
			e.SubChannel = optFromInt(sc, true)
		}
		if nibble != 0 {
			e.ErrorVector = fmt.Sprintf("nibble:0x%x", nibble)
		}
	}
	return r.emit(&e)
}

func (r *reader) cxlGeneralMedia(rw *row) error {
	var e model.Event
	if !r.cxlCommon(rw, &e) {
		return nil
	}
	ch, _ := rw.int("channel")
	rk, _ := rw.int("rank")
	dev, _ := rw.int("device")
	if ch != 0 || rk != 0 || dev != 0 {
		e.ChannelId, e.Rank, e.DeviceId = optFromInt(ch, true), optFromInt(rk, true), optFromInt(dev, true)
	}
	return r.emit(&e)
}

// memoryFailure turns a page the kernel poisoned after an uncorrected error
// into a page offline action.
func (r *reader) memoryFailure(rw *row) error {
	ref, id := r.ref(rw)
	t, _, ok := parseTimestamp(rw.str("timestamp"))
	if !ok {
		r.tally.Skip(ref, "timestamp is empty or unreadable")
		return nil
	}
	pfnText := strings.TrimSpace(rw.str("pfn"))
	pfn, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(pfnText), "0x"), 16, 52)
	if err != nil {
		r.tally.Skip(ref, fmt.Sprintf("page frame number %q is unreadable", pfnText))
		return nil
	}
	a := &model.Action{
		At: t, TimeQuality: model.TimeExact, Kind: model.ActionPageOffline, Method: "hard",
		OsPhysicalAddress: model.Some(pfn << 12),
		Detail:            strings.TrimSpace(rw.str("page_type") + ": " + rw.str("action_result")),
		Source:            Kind + "/" + rw.table, RawRef: ref, Raw: rw.raw(),
		Fingerprint: fingerprint(rw.table, id, t),
	}
	switch strings.ToLower(strings.TrimSpace(rw.str("action_result"))) {
	case "recovered":
		a.Result = model.ResultOK
	case "delayed":
		a.Result = model.ResultPending
	case "failed", "ignored":
		a.Result = model.ResultFailed
	default:
		a.Result = model.ResultUnknown
	}
	if err := r.sink.Action(a); err != nil {
		return sinkError{err}
	}
	return nil
}
