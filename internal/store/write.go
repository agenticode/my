package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/agenticode/my/internal/model"
)

// ErrReadOnly is returned when a write is attempted on a read-only ledger.
var ErrReadOnly = errors.New("ledger is open read-only")

// ErrInvalidRecord marks an error about the content of a record, as opposed
// to a failure of the ledger. A caller importing many records can skip the
// record and go on.
var ErrInvalidRecord = errors.New("invalid record")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidRecord, fmt.Sprintf(format, args...))
}

// Writer applies changes inside one transaction. It is only valid inside the
// function passed to Store.Update.
type Writer struct {
	// ctx is the context of the update: when it ends, the next statement
	// is not run. run is the context the statements run under, which does
	// not end. The SQLite driver starts a goroutine for every statement
	// whose context can end, to interrupt it; an import runs a statement
	// per record, and that goroutine was a sixth of its CPU time. A
	// statement of the writer is short, so stopping between two of them
	// is as good as stopping inside one.
	ctx   context.Context
	run   context.Context
	tx    *sql.Tx
	stmts map[string]*sql.Stmt
}

// Update runs fn in one transaction. If fn returns an error nothing is written.
func (s *Store) Update(ctx context.Context, fn func(w *Writer) error) error {
	if s.mode == ReadOnly {
		return ErrReadOnly
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	w := &Writer{ctx: ctx, run: context.WithoutCancel(ctx), tx: tx, stmts: make(map[string]*sql.Stmt)}
	defer w.close()
	if err := fn(w); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (w *Writer) close() {
	for _, st := range w.stmts {
		st.Close()
	}
	w.stmts = nil
}

func (w *Writer) stmt(query string) (*sql.Stmt, error) {
	if st, ok := w.stmts[query]; ok {
		return st, nil
	}
	if err := w.ctx.Err(); err != nil {
		return nil, err
	}
	st, err := w.tx.PrepareContext(w.run, query)
	if err != nil {
		return nil, err
	}
	w.stmts[query] = st
	return st, nil
}

func (w *Writer) exec(query string, args ...any) (sql.Result, error) {
	if err := w.ctx.Err(); err != nil {
		return nil, err
	}
	st, err := w.stmt(query)
	if err != nil {
		return nil, err
	}
	return st.ExecContext(w.run, args...)
}

func (w *Writer) queryRow(query string, args ...any) *sql.Row {
	st, err := w.stmt(query)
	if err != nil {
		// Surface the error through Scan: that of the prepare, or that
		// of a context that has ended.
		return w.tx.QueryRowContext(w.ctx, query, args...)
	}
	if w.ctx.Err() != nil {
		return w.tx.QueryRowContext(w.ctx, query, args...)
	}
	return st.QueryRowContext(w.run, args...)
}

func optInt(o model.Opt[int]) any {
	if !o.Valid {
		return nil
	}
	return int64(o.V)
}

func optInt64(o model.Opt[int64]) any {
	if !o.Valid {
		return nil
	}
	return o.V
}

// optU64 stores an unsigned 64-bit value as its signed bit pattern.
func optU64(o model.Opt[uint64]) any {
	if !o.Valid {
		return nil
	}
	return int64(o.V)
}

func optBool(o model.Opt[bool]) any {
	if !o.Valid {
		return nil
	}
	if o.V {
		return int64(1)
	}
	return int64(0)
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// eventColumns is the column order shared by event inserts and selects.
const eventColumns = `host, kind, "ErrorDT", "ErrorSeverity", "ErrorScrubType",
 "SocketId", "ChannelId", "ChannelSlotId", "SubChannel", "Rank", "DeviceId", "BankGroup", "Bank", "Row", "Column",
 "ErrorVector", "OsPhysicalAddress", source, raw_ref, grade, boot_id, dimm_uid, dimm_link,
 count, covered, dup_of, dup_fingerprint, time_quality, window_start, utc_offset,
 imc, edac_mc, edac_location, smbios_handle, label, slot, syndrome, error_type, decoder, fingerprint, scan_id,
 carried, read_boot_id, not_before, cause_fingerprint`

const eventColumnCount = 45

var insertEventSQL = `INSERT INTO event (` + eventColumns + `) VALUES (` + placeholders(eventColumnCount) + `)
 ON CONFLICT(fingerprint) DO NOTHING`

// InsertEvent adds an event to the ledger. An event whose fingerprint is
// already present is not added again; inserted is then false and e.ID is set
// to the existing row. Missing defaults (kind, count, grade, time quality)
// are filled in on e.
func (w *Writer) InsertEvent(e *model.Event) (inserted bool, err error) {
	if e.Kind == "" {
		e.Kind = model.KindError
	}
	if e.Count == 0 {
		e.Count = 1
	}
	if e.TimeQuality == "" {
		e.TimeQuality = model.TimeExact
	}
	if e.Grade == "" {
		e.Grade = e.ComputeGrade()
	}
	switch {
	case e.Host == "":
		return false, invalid("event: host is empty")
	case e.Source == "":
		return false, invalid("event: source is empty")
	case e.Fingerprint == "":
		return false, invalid("event: fingerprint is empty")
	case !validTime(e.ErrorDT):
		return false, invalid("event: ErrorDT %v is out of range", e.ErrorDT)
	case !e.ErrorSeverity.Valid():
		return false, invalid("event: unknown ErrorSeverity %q", e.ErrorSeverity)
	case !e.ErrorScrubType.Valid():
		return false, invalid("event: unknown ErrorScrubType %q", e.ErrorScrubType)
	case !e.Grade.Valid():
		return false, invalid("event: unknown grade %q", e.Grade)
	case e.Count < 1:
		return false, invalid("event: count %d is not positive", e.Count)
	case e.Covered < 0 || e.Covered > e.Count:
		return false, invalid("event: covered %d is outside 0..%d", e.Covered, e.Count)
	case e.Kind != model.KindError && e.Kind != model.KindCounter:
		return false, invalid("event: unknown kind %q", e.Kind)
	}
	var dupOf, scanID any
	if e.DupOf != 0 {
		dupOf = e.DupOf
	}
	if e.ScanID != 0 {
		scanID = e.ScanID
	}
	res, err := w.exec(insertEventSQL,
		e.Host, string(e.Kind), ts(e.ErrorDT), string(e.ErrorSeverity), nullStr(string(e.ErrorScrubType)),
		optInt(e.SocketId), optInt(e.ChannelId), optInt(e.ChannelSlotId), optInt(e.SubChannel), optInt(e.Rank),
		optInt(e.DeviceId), optInt(e.BankGroup), optInt(e.Bank), optInt(e.Row), optInt(e.Column),
		e.ErrorVector, optU64(e.OsPhysicalAddress), e.Source, e.RawRef, string(e.Grade),
		nullStr(e.BootID), nullStr(e.DIMMUID), e.DIMMLink,
		e.Count, e.Covered, dupOf, e.DupOfFingerprint, string(e.TimeQuality), ts(e.WindowStart), optInt(e.UTCOffset),
		optInt(e.IMC), optInt(e.EdacMC), e.EdacLocation, optInt(e.SMBIOSHandle), e.Label, e.Slot,
		optU64(e.Syndrome), e.ErrorType, e.Decoder, e.Fingerprint, scanID,
		boolInt(e.Carried), nullStr(e.ReadBootID), ts(e.NotBefore), e.CauseFingerprint)
	if err != nil {
		return false, fmt.Errorf("event %s: %w", e.RawRef, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		if err := w.queryRow(`SELECT id FROM event WHERE fingerprint = ?`, e.Fingerprint).Scan(&e.ID); err != nil {
			return false, fmt.Errorf("event %s: %w", e.RawRef, err)
		}
		return false, nil
	}
	if e.ID, err = res.LastInsertId(); err != nil {
		return false, err
	}
	if e.Raw != "" {
		if _, err := w.exec(`INSERT INTO event_raw (event_id, text) VALUES (?, ?)`, e.ID, e.Raw); err != nil {
			return false, fmt.Errorf("event %s: raw record: %w", e.RawRef, err)
		}
	}
	if e.Firmware != nil {
		firmwareOf(e.Firmware, e.Host, e.ScanID, e.ErrorDT, e.Source, e.RawRef)
		if _, err := w.insertFirmware(e.Firmware, e.ID, 0); err != nil {
			return false, err
		}
	}
	return true, nil
}

// UpsertHost records a host, keeping known attributes when the new record
// leaves them empty.
//
// The kernel log names the machine in one string, vendor and product
// together ("Example Systems R650/0PYXKY"). A source that knows the two
// apart is the better one: a product that is the known vendor and product
// in one string does not replace them.
func (w *Writer) UpsertHost(h *model.Host) error {
	if h.Name == "" {
		return invalid("host: name is empty")
	}
	_, err := w.exec(`INSERT INTO host (name, arch, system_vendor, system_product, board, virtual, sockets, cpu, cpu_signature, cpu_present, first_seen, last_seen)
 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
 ON CONFLICT(name) DO UPDATE SET
  sockets        = CASE WHEN excluded.sockets        > 0  THEN excluded.sockets        ELSE sockets        END,
  cpu            = CASE WHEN excluded.cpu            != '' THEN excluded.cpu            ELSE cpu            END,
  cpu_signature  = CASE WHEN excluded.cpu_signature  != '' THEN excluded.cpu_signature  ELSE cpu_signature  END,
  cpu_present    = CASE WHEN excluded.cpu_present    != '' THEN excluded.cpu_present    ELSE cpu_present    END,
  arch           = CASE WHEN excluded.arch           != '' THEN excluded.arch           ELSE arch           END,
  system_vendor  = CASE WHEN excluded.system_vendor  != '' THEN excluded.system_vendor  ELSE system_vendor  END,
  system_product = CASE
    WHEN excluded.system_product = '' THEN system_product
    WHEN excluded.system_vendor = '' AND system_vendor != '' AND system_product != ''
     AND substr(excluded.system_product, 1, length(system_vendor) + 1 + length(system_product)) = system_vendor || ' ' || system_product
     THEN system_product
    ELSE excluded.system_product END,
  board          = CASE WHEN excluded.board          != '' THEN excluded.board          ELSE board          END,
  virtual        = CASE
    WHEN excluded.virtual = '' THEN virtual
    WHEN excluded.virtual = 'hypervisor' AND virtual != '' THEN virtual
    ELSE excluded.virtual END,
  first_seen = MIN(COALESCE(first_seen, excluded.first_seen), COALESCE(excluded.first_seen, first_seen)),
  last_seen  = MAX(COALESCE(last_seen, excluded.last_seen), COALESCE(excluded.last_seen, last_seen))`,
		h.Name, h.Arch, h.SystemVendor, h.SystemProduct, h.Board, h.Virtual, h.Sockets, h.CPU, h.CPUSignature, h.CPUPresent, ts(h.FirstSeen), ts(h.LastSeen))
	return err
}

// bootMergeWindow is how far apart two start times of the same boot, seen
// through different sources, may be.
const bootMergeWindow = 30 * time.Second

func qualityRank(q model.TimeQuality) int {
	switch q {
	case model.TimeExact:
		return 3
	case model.TimeEstimated:
		return 2
	case model.TimeRelative:
		return 1
	}
	return 0
}

// UpsertBoot records a boot and returns the boot ID it is stored under.
//
// A boot without a kernel boot ID gets a derived ID. When the same boot is
// later seen with its kernel boot ID (or the other way round), the two are
// folded into one row under the kernel ID, and records that referred to the
// derived ID are updated. Two boots are taken to be the same when their
// start times are within 30 seconds and at least one of them is derived.
func (w *Writer) UpsertBoot(b *model.Boot) (string, error) {
	if b.Host == "" {
		return "", invalid("boot: host is empty")
	}
	if !validTime(b.StartedAt) {
		return "", invalid("boot: start time %v is out of range", b.StartedAt)
	}
	if b.StartQuality == "" {
		b.StartQuality = model.TimeExact
	}
	id := b.BootID
	derived := b.Derived || id == ""
	if id == "" {
		id = model.DerivedBootID(b.Host, b.StartedAt)
	}

	cur, err := w.bootByID(b.Host, id)
	if err != nil {
		return "", err
	}
	if cur == nil && b.StartQuality != model.TimeRelative {
		near, err := w.bootNear(b.Host, b.StartedAt)
		if err != nil {
			return "", err
		}
		switch {
		case near == nil:
		case derived:
			// The incoming record has no kernel ID: it describes a boot the
			// ledger already knows.
			cur, id = near, near.BootID
		case near.Derived:
			// The incoming record carries the kernel ID of a boot stored
			// under a derived ID: rename that boot.
			for _, q := range []string{
				`UPDATE boot    SET boot_id = ?, derived = 0 WHERE host = ? AND boot_id = ?`,
				`UPDATE event   SET boot_id = ? WHERE host = ? AND boot_id = ?`,
				`UPDATE action  SET boot_id = ? WHERE host = ? AND boot_id = ?`,
				`UPDATE gpu_mem SET boot_id = ? WHERE host = ? AND boot_id = ?`,
				`UPDATE ecs_sample SET boot_id = ? WHERE host = ? AND boot_id = ?`,
			} {
				if _, err := w.exec(q, id, b.Host, near.BootID); err != nil {
					return "", fmt.Errorf("boot %s: %w", id, err)
				}
			}
			near.BootID, near.Derived = id, false
			cur = near
		}
	}

	if cur == nil {
		// How the boot ended is not taken from the record: BootOutcomes
		// works it out from the records of the ledger.
		_, err := w.exec(`INSERT INTO boot (host, boot_id, derived, started_at, start_quality, last_seen, kernel, bios, bmc, cmdline, source, raw_ref)
 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			b.Host, id, boolInt(derived), ts(b.StartedAt), string(b.StartQuality), ts(b.LastSeen),
			b.Kernel, b.BIOS, b.BMC, b.Cmdline, b.Source, b.RawRef)
		if err != nil {
			return "", fmt.Errorf("boot %s: %w", id, err)
		}
		return id, nil
	}

	// Merge into the known boot: a better start time wins, text fields are
	// filled where they were empty.
	started, quality := cur.StartedAt, cur.StartQuality
	if qualityRank(b.StartQuality) > qualityRank(cur.StartQuality) {
		started, quality = b.StartedAt, b.StartQuality
	}
	last := cur.LastSeen
	if b.LastSeen.After(last) {
		last = b.LastSeen
	}
	pick := func(old, new string) string {
		if old != "" {
			return old
		}
		return new
	}
	_, err = w.exec(`UPDATE boot SET started_at = ?, start_quality = ?, last_seen = ?, kernel = ?, bios = ?, bmc = ?, cmdline = ?, source = ?, raw_ref = ?
 WHERE host = ? AND boot_id = ?`,
		ts(started), string(quality), ts(last),
		pick(cur.Kernel, b.Kernel), pick(cur.BIOS, b.BIOS), pick(cur.BMC, b.BMC), pick(cur.Cmdline, b.Cmdline),
		pick(cur.Source, b.Source), pick(cur.RawRef, b.RawRef), b.Host, id)
	if err != nil {
		return "", fmt.Errorf("boot %s: %w", id, err)
	}
	return id, nil
}

const bootColumns = `host, boot_id, derived, started_at, start_quality, last_seen, kernel, bios, bmc, cmdline, source, raw_ref,
 "end", end_at, end_ref, end_source`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanBoot(r rowScanner) (*model.Boot, error) {
	var b model.Boot
	var derived int64
	var started, last, endAt sql.NullString
	var quality string
	if err := r.Scan(&b.Host, &b.BootID, &derived, &started, &quality, &last, &b.Kernel, &b.BIOS, &b.BMC, &b.Cmdline, &b.Source, &b.RawRef,
		&b.End, &endAt, &b.EndRef, &b.EndSource); err != nil {
		return nil, err
	}
	b.Derived = derived != 0
	b.StartedAt, b.LastSeen, b.EndAt = parseTS(started), parseTS(last), parseTS(endAt)
	b.StartQuality = model.TimeQuality(quality)
	return &b, nil
}

func (w *Writer) bootByID(host, id string) (*model.Boot, error) {
	b, err := scanBoot(w.queryRow(`SELECT `+bootColumns+` FROM boot WHERE host = ? AND boot_id = ?`, host, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return b, err
}

// bootNear returns the boot of host that started closest to t within the
// merge window, ignoring boots whose start is only known relative to boot.
func (w *Writer) bootNear(host string, t time.Time) (*model.Boot, error) {
	rows, err := w.tx.QueryContext(w.ctx, `SELECT `+bootColumns+` FROM boot
 WHERE host = ? AND started_at >= ? AND started_at <= ? AND start_quality NOT IN ('relative', 'unknown')`,
		host, ts(t.Add(-bootMergeWindow)), ts(t.Add(bootMergeWindow)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var best *model.Boot
	var bestDiff time.Duration
	for rows.Next() {
		b, err := scanBoot(rows)
		if err != nil {
			return nil, err
		}
		d := b.StartedAt.Sub(t).Abs()
		if best == nil || d < bestDiff {
			best, bestDiff = b, d
		}
	}
	return best, rows.Err()
}

// UpsertDIMM records a DIMM identity, keeping known attributes when the new
// record leaves them empty.
func (w *Writer) UpsertDIMM(d *model.DIMM) error {
	if d.UID == "" {
		return invalid("dimm: uid is empty")
	}
	if d.Identity == "" {
		d.Identity = model.IdentitySerial
	}
	text := func(col string) string {
		return fmt.Sprintf("%[1]s = CASE WHEN excluded.%[1]s != '' THEN excluded.%[1]s ELSE %[1]s END", col)
	}
	num := func(col string) string {
		return fmt.Sprintf("%[1]s = COALESCE(excluded.%[1]s, %[1]s)", col)
	}
	sets := []string{
		text("manufacturer"), text("part_number"), text("serial"), text("manufacturer_id"),
		num("size_bytes"), text("memory_type"), text("form_factor"), text("module_type"), text("technology"),
		num("ranks"), num("device_width"), num("data_width"), num("total_width"), num("speed_mts"), num("configured_speed_mts"),
		text("mfg_date"),
		"first_seen = MIN(COALESCE(first_seen, excluded.first_seen), COALESCE(excluded.first_seen, first_seen))",
		"last_seen = MAX(COALESCE(last_seen, excluded.last_seen), COALESCE(excluded.last_seen, last_seen))",
	}
	var size any
	if d.SizeBytes > 0 {
		size = d.SizeBytes
	}
	_, err := w.exec(`INSERT INTO dimm (dimm_uid, identity, manufacturer, part_number, serial, manufacturer_id, size_bytes,
 memory_type, form_factor, module_type, technology, ranks, device_width, data_width, total_width, speed_mts, configured_speed_mts,
 mfg_date, first_seen, last_seen) VALUES (`+placeholders(20)+`)
 ON CONFLICT(dimm_uid) DO UPDATE SET `+strings.Join(sets, ", "),
		d.UID, string(d.Identity), d.Manufacturer, d.PartNumber, d.Serial, d.ManufacturerID, size,
		d.MemoryType, d.FormFactor, d.ModuleType, d.Technology, optInt(d.Ranks), optInt(d.DeviceWidth), optInt(d.DataWidth),
		optInt(d.TotalWidth), optInt(d.SpeedMTs), optInt(d.ConfiguredSpeedMTs), d.MfgDate, ts(d.FirstSeen), ts(d.LastSeen))
	if err != nil {
		return fmt.Errorf("dimm %s: %w", d.UID, err)
	}
	return nil
}

// UpsertSlot records a slot of a host, keeping known attributes when the new
// record leaves them empty.
func (w *Writer) UpsertSlot(s *model.Slot) error {
	if s.Host == "" || s.Name == "" {
		return invalid("slot: host and name are required")
	}
	text := func(col string) string {
		return fmt.Sprintf("%[1]s = CASE WHEN excluded.%[1]s != '' THEN excluded.%[1]s ELSE %[1]s END", col)
	}
	num := func(col string) string {
		return fmt.Sprintf("%[1]s = COALESCE(excluded.%[1]s, %[1]s)", col)
	}
	sets := []string{
		text("locator"), text("bank_locator"), num("smbios_handle"), num("socket"), num("imc"), num("channel"), num("position"),
		num("edac_mc"), text("edac_location"), text("edac_label"), text("mapping"), num("populated"),
	}
	_, err := w.exec(`INSERT INTO slot (host, name, locator, bank_locator, smbios_handle, socket, imc, channel, position,
 edac_mc, edac_location, edac_label, mapping, populated) VALUES (`+placeholders(14)+`)
 ON CONFLICT(host, name) DO UPDATE SET `+strings.Join(sets, ", "),
		s.Host, s.Name, s.Locator, s.BankLocator, optInt(s.SMBIOSHandle), optInt(s.Socket), optInt(s.IMC), optInt(s.Channel),
		optInt(s.Position), optInt(s.EdacMC), s.EdacLocation, s.EdacLabel, s.Mapping, optBool(s.Populated))
	if err != nil {
		return fmt.Errorf("slot %s: %w", s.Name, err)
	}
	return nil
}

// ReplaceSlot stores a slot row as given, dropping what the ledger held for
// it before. It is used when the mapping between EDAC positions and SMBIOS
// slots is recomputed: a mapping that no longer holds must not linger.
func (w *Writer) ReplaceSlot(s *model.Slot) error {
	if s.Host == "" || s.Name == "" {
		return invalid("slot: host and name are required")
	}
	_, err := w.exec(`INSERT OR REPLACE INTO slot (host, name, locator, bank_locator, smbios_handle, socket, imc, channel, position,
 edac_mc, edac_location, edac_label, mapping, populated) VALUES (`+placeholders(14)+`)`,
		s.Host, s.Name, s.Locator, s.BankLocator, optInt(s.SMBIOSHandle), optInt(s.Socket), optInt(s.IMC), optInt(s.Channel),
		optInt(s.Position), optInt(s.EdacMC), s.EdacLocation, s.EdacLabel, s.Mapping, optBool(s.Populated))
	if err != nil {
		return fmt.Errorf("slot %s: %w", s.Name, err)
	}
	return nil
}

// SetSlotAlias records that a source calls the slot by the given alias. A
// later observation replaces an earlier one.
func (w *Writer) SetSlotAlias(host, alias, slot string) error {
	if host == "" || alias == "" || slot == "" {
		return invalid("slot alias: host, alias and slot are required")
	}
	_, err := w.exec(`INSERT INTO slot_alias (host, alias, slot) VALUES (?, ?, ?)
 ON CONFLICT(host, alias) DO UPDATE SET slot = excluded.slot`, host, alias, slot)
	return err
}

// ObservePlacement records that the DIMM was seen in the slot at the given
// time. A sighting that agrees with the neighbouring placement of the slot
// extends it; otherwise a new placement starts. Sightings may arrive in any
// order.
func (w *Writer) ObservePlacement(dimmUID, host, slot string, at time.Time, source string) error {
	if dimmUID == "" || host == "" || slot == "" {
		return invalid("placement: dimm, host and slot are required")
	}
	if !validTime(at) {
		return invalid("placement: time %v is out of range", at)
	}
	t := ts(at)
	var id int64
	var uid string
	// The placement in force at the time: the latest one that started at or before it.
	err := w.queryRow(`SELECT id, dimm_uid FROM placement WHERE host = ? AND slot = ? AND first_seen <= ?
 ORDER BY first_seen DESC, id DESC LIMIT 1`, host, slot, t).Scan(&id, &uid)
	switch {
	case err == nil && uid == dimmUID:
		_, err = w.exec(`UPDATE placement SET last_seen = MAX(last_seen, ?) WHERE id = ?`, t, id)
		return err
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return err
	}
	// Otherwise the next placement, if it is the same DIMM, started earlier than known.
	err = w.queryRow(`SELECT id, dimm_uid FROM placement WHERE host = ? AND slot = ? AND first_seen > ?
 ORDER BY first_seen ASC, id ASC LIMIT 1`, host, slot, t).Scan(&id, &uid)
	switch {
	case err == nil && uid == dimmUID:
		_, err = w.exec(`UPDATE placement SET first_seen = ? WHERE id = ?`, t, id)
		return err
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return err
	}
	_, err = w.exec(`INSERT INTO placement (dimm_uid, host, slot, first_seen, last_seen, source) VALUES (?, ?, ?, ?, ?, ?)`,
		dimmUID, host, slot, t, t, source)
	return err
}

// InsertAction adds an action. An action whose fingerprint is already
// present is not added again.
func (w *Writer) InsertAction(a *model.Action) (inserted bool, err error) {
	if a.TimeQuality == "" {
		a.TimeQuality = model.TimeExact
	}
	if a.Result == "" {
		a.Result = model.ResultUnknown
	}
	if a.Count == 0 {
		a.Count = 1
	}
	switch {
	case a.Host == "":
		return false, invalid("action: host is empty")
	case a.Source == "":
		return false, invalid("action: source is empty")
	case a.Fingerprint == "":
		return false, invalid("action: fingerprint is empty")
	case !validTime(a.At):
		return false, invalid("action: time %v is out of range", a.At)
	case !a.Kind.Valid():
		return false, invalid("action: unknown kind %q", a.Kind)
	}
	var scanID any
	if a.ScanID != 0 {
		scanID = a.ScanID
	}
	res, err := w.exec(`INSERT INTO action (host, at, time_quality, kind, method, result, "OsPhysicalAddress", "Rank", "DeviceId",
 "BankGroup", "Bank", "Row", label, slot, dimm_uid, count, boot_id, detail, source, raw_ref, raw, fingerprint, scan_id)
 VALUES (`+placeholders(23)+`) ON CONFLICT(fingerprint) DO NOTHING`,
		a.Host, ts(a.At), string(a.TimeQuality), string(a.Kind), a.Method, string(a.Result), optU64(a.OsPhysicalAddress),
		optInt(a.Rank), optInt(a.DeviceId), optInt(a.BankGroup), optInt(a.Bank), optInt(a.Row), a.Label, a.Slot,
		nullStr(a.DIMMUID), a.Count, nullStr(a.BootID), a.Detail, a.Source, a.RawRef, a.Raw, a.Fingerprint, scanID)
	if err != nil {
		return false, fmt.Errorf("action %s: %w", a.RawRef, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		err := w.queryRow(`SELECT id FROM action WHERE fingerprint = ?`, a.Fingerprint).Scan(&a.ID)
		return false, err
	}
	if a.ID, err = res.LastInsertId(); err != nil {
		return false, err
	}
	if a.Firmware != nil {
		firmwareOf(a.Firmware, a.Host, a.ScanID, a.At, a.Source, a.RawRef)
		if _, err := w.insertFirmware(a.Firmware, 0, a.ID); err != nil {
			return false, err
		}
	}
	return true, nil
}

// InsertCounterSample stores one counter reading. A reading of the same
// counter of the same source at the same time is stored once.
func (w *Writer) InsertCounterSample(host string, c *model.CounterSample) (inserted bool, err error) {
	if host == "" {
		return false, invalid("counter sample: host is empty")
	}
	if !validTime(c.ObservedAt) {
		return false, invalid("counter sample: time %v is out of range", c.ObservedAt)
	}
	if c.CE > math.MaxInt64 || c.UE > math.MaxInt64 {
		return false, invalid("counter sample: a count of %d corrected and %d uncorrected errors is out of range", c.CE, c.UE)
	}
	// A running time beyond a century is no running time, and as a
	// duration it would overflow.
	var resetAt time.Time
	if c.SecondsSinceReset.Valid && c.SecondsSinceReset.V >= 0 && c.SecondsSinceReset.V <= 100*365*24*3600 {
		resetAt = c.ObservedAt.Add(-time.Duration(c.SecondsSinceReset.V) * time.Second)
	}
	// A recent count says something only together with its window.
	recent := func(n model.Opt[uint64], window time.Duration) (count, seconds any) {
		if !n.Valid || window < time.Second || n.V > math.MaxInt64 {
			return nil, nil
		}
		return int64(n.V), int64(window / time.Second)
	}
	rce, rceSec := recent(c.RecentCE, c.RecentCEWindow)
	rue, rueSec := recent(c.RecentUE, c.RecentUEWindow)
	res, err := w.exec(`INSERT OR IGNORE INTO counter_sample (host, observed_at, source, mc, mc_name, dimm_index, location, label, bank_locator,
 "SocketId", "ChannelId", "ChannelSlotId", ce, ue, recent_ce, recent_ce_seconds, recent_ue, recent_ue_seconds,
 reset_at, size_mb, mem_type, dev_type, edac_mode, raw_ref) VALUES (`+placeholders(24)+`)`,
		host, ts(c.ObservedAt), c.Source, c.MC, c.MCName, c.DIMMIndex, c.Location, c.Label, c.BankLocator,
		optInt(c.SocketId), optInt(c.ChannelId), optInt(c.ChannelSlotId), int64(c.CE), int64(c.UE), rce, rceSec, rue, rueSec,
		ts(resetAt), optInt(c.SizeMB), c.MemType, c.DevType, c.EdacMode, c.RawRef)
	if err != nil {
		return false, fmt.Errorf("counter sample %s: %w", c.RawRef, err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// InsertScan records the start of a collection run and sets sc.ID.
func (w *Writer) InsertScan(sc *model.Scan) error {
	if sc.Host == "" || sc.Kind == "" {
		return invalid("scan: host and kind are required")
	}
	if !validTime(sc.StartedAt) {
		return invalid("scan: start time %v is out of range", sc.StartedAt)
	}
	res, err := w.exec(`INSERT INTO scan (host, kind, started_at, finished_at, tool_version, privileged, note) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sc.Host, sc.Kind, ts(sc.StartedAt), ts(sc.FinishedAt), sc.ToolVersion, boolInt(sc.Privileged), sc.Note)
	if err != nil {
		return err
	}
	sc.ID, err = res.LastInsertId()
	return err
}

// FinishScan sets the end time of a scan.
func (w *Writer) FinishScan(id int64, at time.Time) error {
	_, err := w.exec(`UPDATE scan SET finished_at = ? WHERE id = ?`, ts(at), id)
	return err
}

// InsertSource adds an entry to the source inventory of a scan and sets src.ID.
func (w *Writer) InsertSource(src *model.Source) error {
	if src.ScanID == 0 || src.Kind == "" {
		return invalid("source: scan and kind are required")
	}
	if src.Status == "" {
		src.Status = model.SourceOK
	}
	notes := src.Notes
	if notes == nil {
		notes = []string{}
	}
	nb, err := json.Marshal(notes)
	if err != nil {
		return err
	}
	res, err := w.exec(`INSERT INTO source (scan_id, kind, origin, status, detail, records, events, actions, skipped, samples,
 grade_a, grade_b, grade_c, first_ts, last_ts, notes, unread, faults) VALUES (`+placeholders(18)+`)`,
		src.ScanID, src.Kind, src.Origin, string(src.Status), src.Detail, src.Records, src.Events, src.Actions, src.Skipped, src.Samples,
		src.GradeA, src.GradeB, src.GradeC, ts(src.FirstTS), ts(src.LastTS), string(nb), src.Unread, src.Faults)
	if err != nil {
		return fmt.Errorf("source %s %s: %w", src.Kind, src.Origin, err)
	}
	src.ID, err = res.LastInsertId()
	return err
}

// DeleteAnalysis removes the faults and verdicts stored for a host, so that
// a new analysis run can replace them.
func (w *Writer) DeleteAnalysis(host string) error {
	if _, err := w.exec(`DELETE FROM fault WHERE host = ?`, host); err != nil {
		return err
	}
	_, err := w.exec(`DELETE FROM verdict WHERE host = ?`, host)
	return err
}

// InsertFault stores a fault with its member events and sets f.ID.
func (w *Writer) InsertFault(f *model.Fault) error {
	switch {
	case f.Host == "":
		return invalid("fault: host is empty")
	case !f.Mode.Valid():
		return invalid("fault: unknown mode %q", f.Mode)
	case !f.Grade.Valid():
		return invalid("fault: unknown grade %q", f.Grade)
	case !validTime(f.FirstSeen) || !validTime(f.LastSeen):
		return invalid("fault: first and last seen are required")
	}
	res, err := w.exec(`INSERT INTO fault (host, dimm_uid, slot, mode, estimated, grade, "Rank", "DeviceId", "BankGroup", "Bank",
 row_min, row_max, column_min, column_max, address_min, address_max, first_seen, last_seen, event_count, error_count,
 confidence, engine_version) VALUES (`+placeholders(22)+`)`,
		f.Host, nullStr(f.DIMMUID), f.Slot, string(f.Mode), boolInt(f.Estimated), string(f.Grade),
		optInt(f.Rank), optInt(f.DeviceId), optInt(f.BankGroup), optInt(f.Bank),
		optInt(f.RowMin), optInt(f.RowMax), optInt(f.ColumnMin), optInt(f.ColumnMax), optU64(f.AddressMin), optU64(f.AddressMax),
		ts(f.FirstSeen), ts(f.LastSeen), f.EventCount, f.ErrorCount, f.Confidence, f.EngineVersion)
	if err != nil {
		return fmt.Errorf("fault: %w", err)
	}
	if f.ID, err = res.LastInsertId(); err != nil {
		return err
	}
	for _, eid := range f.EventIDs {
		if _, err := w.exec(`INSERT OR IGNORE INTO fault_event (fault_id, event_id) VALUES (?, ?)`, f.ID, eid); err != nil {
			return fmt.Errorf("fault %d: member event %d: %w", f.ID, eid, err)
		}
	}
	return nil
}

func jsonText(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

// InsertVerdict stores a verdict and sets v.ID.
func (w *Writer) InsertVerdict(v *model.Verdict) error {
	switch {
	case v.Host == "":
		return invalid("verdict: host is empty")
	case !v.Grade.Valid():
		return invalid("verdict: unknown grade %q", v.Grade)
	case !validTime(v.ComputedAt):
		return invalid("verdict: computed time is required")
	case v.DIMMUID != "" && v.GPU != "":
		return invalid("verdict: a finding is about a DIMM or about a GPU, not both")
	}
	// Empty lists are stored as [], never as null.
	lists := []any{v.Rules, v.Evidence, v.CounterEvidence, v.Missing, v.ConfidenceFactors}
	if v.Rules == nil {
		lists[0] = []string{}
	}
	if v.Evidence == nil {
		lists[1] = []model.Evidence{}
	}
	if v.CounterEvidence == nil {
		lists[2] = []model.Check{}
	}
	if v.Missing == nil {
		lists[3] = []model.Statement{}
	}
	if v.ConfidenceFactors == nil {
		lists[4] = []model.Factor{}
	}
	var parts [5]string
	for i, x := range lists {
		s, err := jsonText(x)
		if err != nil {
			return fmt.Errorf("verdict: %w", err)
		}
		parts[i] = s
	}
	next := ""
	if v.NextAction.Code != "" || v.NextAction.Text != "" {
		s, err := jsonText(v.NextAction)
		if err != nil {
			return fmt.Errorf("verdict: %w", err)
		}
		next = s
	}
	res, err := w.exec(`INSERT INTO verdict (host, dimm_uid, slot, gpu, grade, rules, evidence, counter_evidence, missing, next_action,
 confidence, confidence_factors, engine_version, computed_at) VALUES (`+placeholders(14)+`)`,
		v.Host, nullStr(v.DIMMUID), v.Slot, v.GPU, string(v.Grade), parts[0], parts[1], parts[2], parts[3], next,
		v.Confidence, parts[4], v.EngineVersion, ts(v.ComputedAt))
	if err != nil {
		return fmt.Errorf("verdict: %w", err)
	}
	v.ID, err = res.LastInsertId()
	return err
}

// SetMeta stores value under key, in the transaction of the Writer.
func (w *Writer) SetMeta(key, value string) error {
	_, err := w.exec(`INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// gpuColumns is the column order shared by GPU memory inserts and selects.
const gpuColumns = `host, observed_at, time_quality, kind, gpu_serial, gpu_uuid, gpu_index, pci_bus, model, xid, xid_text,
 remap_correctable, remap_uncorrectable, remap_pending, remap_failure, bank_max, bank_high, bank_partial, bank_low, bank_none,
 retired_sbe, retired_dbe, retired_pending, ecc_enabled, dram_correctable, dram_uncorrectable, dram_correctable_volatile,
 dram_uncorrectable_volatile, sram_correctable, sram_uncorrectable, sram_correctable_volatile, sram_uncorrectable_volatile,
 sram_threshold_exceeded, boot_id, source, raw_ref, raw, fingerprint, scan_id`

const gpuColumnCount = 39

// InsertGPUMem stores a GPU memory record. A record whose fingerprint is
// already present is not added again; inserted is then false and g.ID is
// set to the existing row.
func (w *Writer) InsertGPUMem(g *model.GPUMem) (inserted bool, err error) {
	if g.TimeQuality == "" {
		g.TimeQuality = model.TimeExact
	}
	switch {
	case g.Host == "":
		return false, invalid("gpu_mem: host is empty")
	case g.Kind != model.GPUSnapshot && g.Kind != model.GPUXID:
		return false, invalid("gpu_mem: unknown kind %q", g.Kind)
	case g.Fingerprint == "" || g.Source == "":
		return false, invalid("gpu_mem: source and fingerprint are required")
	case !validTime(g.ObservedAt):
		return false, invalid("gpu_mem: time %v is out of range", g.ObservedAt)
	case qualityRank(g.TimeQuality) == 0:
		return false, invalid("gpu_mem: unknown time quality %q", g.TimeQuality)
	case g.Kind == model.GPUXID && !g.XID.Valid:
		return false, invalid("gpu_mem: an xid record needs the XID number")
	}
	var scanID any
	if g.ScanID != 0 {
		scanID = g.ScanID
	}
	res, err := w.exec(`INSERT INTO gpu_mem (`+gpuColumns+`) VALUES (`+placeholders(gpuColumnCount)+`) ON CONFLICT(fingerprint) DO NOTHING`,
		g.Host, ts(g.ObservedAt), string(g.TimeQuality), g.Kind, g.GPUSerial, g.GPUUUID, optInt(g.GPUIndex), g.PCIBus, g.Model, optInt(g.XID), g.XIDText,
		optInt(g.RemapCorrectable), optInt(g.RemapUncorrectable), optBool(g.RemapPending), optBool(g.RemapFailure),
		optInt(g.BankMax), optInt(g.BankHigh), optInt(g.BankPartial), optInt(g.BankLow), optInt(g.BankNone),
		optInt(g.RetiredSBE), optInt(g.RetiredDBE), optBool(g.RetiredPending), optBool(g.ECCEnabled),
		optInt(g.DRAMCorrectable), optInt(g.DRAMUncorrectable), optInt(g.DRAMCorrectableVolatile), optInt(g.DRAMUncorrectableVolatile),
		optInt(g.SRAMCorrectable), optInt(g.SRAMUncorrectable), optInt(g.SRAMCorrectableVolatile), optInt(g.SRAMUncorrectableVolatile),
		optBool(g.SRAMThresholdExceeded), nullStr(g.BootID), g.Source, g.RawRef, g.Raw, g.Fingerprint, scanID)
	if err != nil {
		return false, fmt.Errorf("gpu_mem %s: %w", g.RawRef, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		err := w.queryRow(`SELECT id FROM gpu_mem WHERE fingerprint = ?`, g.Fingerprint).Scan(&g.ID)
		return false, err
	}
	g.ID, err = res.LastInsertId()
	return err == nil, err
}

// ecsColumns is the column order shared by ECS inserts and selects.
const ecsColumns = `host, observed_at, time_quality, "SocketId", "ChannelId", "ChannelSlotId", "SubChannel", "Rank", "DeviceId",
 label, slot, dimm_uid, mr14, mr15, mr16, mr17, mr18, mr19, mr20, boot_id, source, raw_ref, raw, fingerprint, scan_id`

const ecsColumnCount = 25

// InsertECS stores a reading of the ECS registers. A reading whose
// fingerprint is already present is not added again; inserted is then
// false and e.ID is set to the existing row.
func (w *Writer) InsertECS(e *model.ECSSample) (inserted bool, err error) {
	if e.TimeQuality == "" {
		e.TimeQuality = model.TimeExact
	}
	switch {
	case e.Host == "":
		return false, invalid("ecs: host is empty")
	case e.Fingerprint == "" || e.Source == "":
		return false, invalid("ecs: source and fingerprint are required")
	case !validTime(e.ObservedAt):
		return false, invalid("ecs: time %v is out of range", e.ObservedAt)
	case qualityRank(e.TimeQuality) == 0:
		return false, invalid("ecs: unknown time quality %q", e.TimeQuality)
	case e.Empty():
		return false, invalid("ecs: no register was read")
	}
	for i, r := range e.Registers() {
		if r.Valid && (r.V < 0 || r.V > 0xFF) {
			return false, invalid("ecs: MR%d value %d is not a byte", 14+i, r.V)
		}
	}
	var scanID any
	if e.ScanID != 0 {
		scanID = e.ScanID
	}
	res, err := w.exec(`INSERT INTO ecs_sample (`+ecsColumns+`) VALUES (`+placeholders(ecsColumnCount)+`) ON CONFLICT(fingerprint) DO NOTHING`,
		e.Host, ts(e.ObservedAt), string(e.TimeQuality), optInt(e.SocketId), optInt(e.ChannelId), optInt(e.ChannelSlotId),
		optInt(e.SubChannel), optInt(e.Rank), optInt(e.DeviceId), e.Label, e.Slot, nullStr(e.DIMMUID),
		optInt(e.MR14), optInt(e.MR15), optInt(e.MR16), optInt(e.MR17), optInt(e.MR18), optInt(e.MR19), optInt(e.MR20),
		nullStr(e.BootID), e.Source, e.RawRef, e.Raw, e.Fingerprint, scanID)
	if err != nil {
		return false, fmt.Errorf("ecs %s: %w", e.RawRef, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		err := w.queryRow(`SELECT id FROM ecs_sample WHERE fingerprint = ?`, e.Fingerprint).Scan(&e.ID)
		return false, err
	}
	e.ID, err = res.LastInsertId()
	return err == nil, err
}

// PutCaptureChecks stores what "my preflight" found during a scan: one row
// per check. Checks the scan already has are replaced.
func (w *Writer) PutCaptureChecks(scanID int64, host string, checks []model.CaptureCheck) error {
	if host == "" {
		return invalid("capture check: host is empty")
	}
	for _, c := range checks {
		switch c.State {
		case "kept", "lost", "none", "unknown", "info":
		default:
			return invalid("capture check %s: unknown state %q", c.Name, c.State)
		}
		if c.Name == "" {
			return invalid("capture check: name is empty")
		}
		detail, err := json.Marshal(c.Detail)
		if err != nil {
			return err
		}
		evidence := c.Evidence
		if evidence == nil {
			evidence = []string{}
		}
		ev, err := json.Marshal(evidence)
		if err != nil {
			return err
		}
		if _, err := w.exec(`INSERT INTO capture_check (scan_id, host, name, state, detail, evidence) VALUES (?, ?, ?, ?, ?, ?)
 ON CONFLICT(scan_id, name) DO UPDATE SET host = excluded.host, state = excluded.state, detail = excluded.detail, evidence = excluded.evidence`,
			scanID, host, c.Name, c.State, string(detail), string(ev)); err != nil {
			return fmt.Errorf("capture check %s: %w", c.Name, err)
		}
	}
	return nil
}

// DropScan removes a scan that added nothing: its row, its source
// inventory, capture checks and readings, and the counter readings of the host taken
// since the scan started, which repeat the readings before. A ledger that
// is read at every boot would otherwise grow by those rows each time.
func (w *Writer) DropScan(scanID int64, host string, since time.Time) error {
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM capture_check WHERE scan_id = ?`, []any{scanID}},
		{`DELETE FROM host_reading WHERE scan_id = ?`, []any{scanID}},
		{`DELETE FROM source WHERE scan_id = ?`, []any{scanID}},
		{`DELETE FROM counter_sample WHERE host = ? AND observed_at >= ?`, []any{host, ts(since)}},
		{`DELETE FROM scan WHERE id = ?`, []any{scanID}},
	} {
		if q.sql == `DELETE FROM scan WHERE id = ?` {
			// Records that name the scan and were there before it keep
			// their row; they lose the reference.
			for _, t := range []string{"event", "action", "gpu_mem", "ecs_sample", "bmc", "bios_setting", "bmc_memory", "fw_record", "registry_seen"} {
				if _, err := w.exec(`UPDATE `+t+` SET scan_id = NULL WHERE scan_id = ?`, scanID); err != nil {
					return fmt.Errorf("drop scan %d: %w", scanID, err)
				}
			}
		}
		if _, err := w.exec(q.sql, q.args...); err != nil {
			return fmt.Errorf("drop scan %d: %w", scanID, err)
		}
	}
	return nil
}

// MaxReadingBytes bounds the text of one reading in the ledger.
const MaxReadingBytes = 4 << 20

// PutReading keeps a reading. A reading of the same host, kind, key and
// time replaces the one before.
func (w *Writer) PutReading(r *model.HostReading) error {
	switch {
	case r.Host == "":
		return invalid("reading: host is empty")
	case r.Kind == "":
		return invalid("reading: kind is empty")
	case r.TakenAt.IsZero():
		return invalid("reading %s: no time", r.Kind)
	case len(r.Text) > MaxReadingBytes:
		return invalid("reading %s: %d bytes, more than %d", r.Kind, len(r.Text), MaxReadingBytes)
	}
	var scan any
	if r.ScanID > 0 {
		scan = r.ScanID
	}
	_, err := w.exec(`INSERT INTO host_reading (host, scan_id, kind, key, taken_at, origin, text, cut) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
 ON CONFLICT(host, kind, key, taken_at) DO UPDATE SET scan_id = excluded.scan_id, origin = excluded.origin, text = excluded.text, cut = excluded.cut`,
		r.Host, scan, r.Kind, r.Key, ts(r.TakenAt), r.Origin, r.Text, r.Cut)
	return err
}
