package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"
	"time"

	"github.com/agenticode/my/internal/model"
)

func toOptInt(n sql.NullInt64) model.Opt[int] {
	if !n.Valid {
		return model.Opt[int]{}
	}
	return model.Some(int(n.Int64))
}

func toOptU64(n sql.NullInt64) model.Opt[uint64] {
	if !n.Valid {
		return model.Opt[uint64]{}
	}
	return model.Some(uint64(n.Int64))
}

func toOptBool(n sql.NullInt64) model.Opt[bool] {
	if !n.Valid {
		return model.Opt[bool]{}
	}
	return model.Some(n.Int64 != 0)
}

// EventFilter selects events. Zero fields do not restrict.
type EventFilter struct {
	Host    string
	DIMMUID string
	Slot    string
	// From is inclusive, To exclusive.
	From, To   time.Time
	Severities []model.Severity
	Kinds      []model.EventKind
	// WithDuplicates includes records marked as duplicates of another record.
	WithDuplicates bool
	// WithRaw loads the original record text into Event.Raw.
	WithRaw bool
	// Limit caps the number of events returned; 0 means no cap.
	Limit int
}

func (f *EventFilter) where() (string, []any) {
	var conds []string
	var args []any
	add := func(c string, a ...any) {
		conds = append(conds, c)
		args = append(args, a...)
	}
	if f.Host != "" {
		add(`event.host = ?`, f.Host)
	}
	if f.DIMMUID != "" {
		add(`event.dimm_uid = ?`, f.DIMMUID)
	}
	if f.Slot != "" {
		add(`event.slot = ?`, f.Slot)
	}
	if !f.From.IsZero() {
		add(`event."ErrorDT" >= ?`, ts(f.From))
	}
	if !f.To.IsZero() {
		add(`event."ErrorDT" < ?`, ts(f.To))
	}
	if len(f.Severities) > 0 {
		var a []any
		for _, s := range f.Severities {
			a = append(a, string(s))
		}
		add(`event."ErrorSeverity" IN (`+placeholders(len(a))+`)`, a...)
	}
	if len(f.Kinds) > 0 {
		var a []any
		for _, k := range f.Kinds {
			a = append(a, string(k))
		}
		add(`event.kind IN (`+placeholders(len(a))+`)`, a...)
	}
	if !f.WithDuplicates {
		add(`event.dup_of IS NULL`)
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

func scanEvent(r rowScanner, withRaw bool) (*model.Event, error) {
	var e model.Event
	var (
		kind, dt, sev, grade, quality                                 string
		scrub, bootID, dimmUID, windowStart, raw                      sql.NullString
		socket, channel, chSlot, subCh, rank, dev, bg, bank, row, col sql.NullInt64
		addr, dupOf, utcOff, imc, edacMC, handle, syndrome, scanID    sql.NullInt64
		carried                                                       int64
		readBoot, notBefore                                           sql.NullString
	)
	dest := []any{
		&e.ID, &e.Host, &kind, &dt, &sev, &scrub,
		&socket, &channel, &chSlot, &subCh, &rank, &dev, &bg, &bank, &row, &col,
		&e.ErrorVector, &addr, &e.Source, &e.RawRef, &grade, &bootID, &dimmUID, &e.DIMMLink,
		&e.Count, &e.Covered, &dupOf, &e.DupOfFingerprint, &quality, &windowStart, &utcOff,
		&imc, &edacMC, &e.EdacLocation, &handle, &e.Label, &e.Slot, &syndrome, &e.ErrorType, &e.Decoder, &e.Fingerprint, &scanID,
		&carried, &readBoot, &notBefore, &e.CauseFingerprint,
	}
	if withRaw {
		dest = append(dest, &raw)
	}
	if err := r.Scan(dest...); err != nil {
		return nil, err
	}
	e.Kind = model.EventKind(kind)
	e.ErrorDT = parseTS(sql.NullString{String: dt, Valid: true})
	e.ErrorSeverity = model.Severity(sev)
	e.ErrorScrubType = model.ScrubType(scrub.String)
	e.SocketId, e.ChannelId, e.ChannelSlotId, e.SubChannel = toOptInt(socket), toOptInt(channel), toOptInt(chSlot), toOptInt(subCh)
	e.Rank, e.DeviceId, e.BankGroup, e.Bank = toOptInt(rank), toOptInt(dev), toOptInt(bg), toOptInt(bank)
	e.Row, e.Column = toOptInt(row), toOptInt(col)
	e.OsPhysicalAddress = toOptU64(addr)
	e.Grade = model.Grade(grade)
	e.BootID, e.DIMMUID = bootID.String, dimmUID.String
	e.DupOf = dupOf.Int64
	e.TimeQuality = model.TimeQuality(quality)
	e.WindowStart = parseTS(windowStart)
	e.UTCOffset = toOptInt(utcOff)
	e.IMC, e.EdacMC, e.SMBIOSHandle = toOptInt(imc), toOptInt(edacMC), toOptInt(handle)
	e.Syndrome = toOptU64(syndrome)
	e.ScanID = scanID.Int64
	e.Carried, e.ReadBootID, e.NotBefore = carried != 0, readBoot.String, parseTS(notBefore)
	e.Raw = raw.String
	return &e, nil
}

// Events streams the events selected by f in time order. Iteration stops at
// the first error, which is yielded as the second value.
func (s *Store) Events(ctx context.Context, f EventFilter) iter.Seq2[*model.Event, error] {
	return func(yield func(*model.Event, error) bool) {
		where, args := f.where()
		q := `SELECT event.id, ` + eventColumns
		if f.WithRaw {
			q += `, event_raw.text FROM event LEFT JOIN event_raw ON event_raw.event_id = event.id`
		} else {
			q += ` FROM event`
		}
		q += where + ` ORDER BY event."ErrorDT", event.id`
		if f.Limit > 0 {
			q += fmt.Sprintf(" LIMIT %d", f.Limit)
		}
		rows, err := s.db.QueryContext(ctx, q, args...)
		if err != nil {
			yield(nil, err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanEvent(rows, f.WithRaw)
			if err != nil {
				yield(nil, err)
				return
			}
			if !yield(e, nil) {
				return
			}
		}
		if err := rows.Err(); err != nil {
			yield(nil, err)
		}
	}
}

// EventList returns the events selected by f as a slice.
func (s *Store) EventList(ctx context.Context, f EventFilter) ([]*model.Event, error) {
	var out []*model.Event
	for e, err := range s.Events(ctx, f) {
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// Event returns one event with its original record text, or nil when the ID
// is unknown.
func (s *Store) Event(ctx context.Context, id int64) (*model.Event, error) {
	row := s.db.QueryRowContext(ctx, `SELECT event.id, `+eventColumns+`, event_raw.text
 FROM event LEFT JOIN event_raw ON event_raw.event_id = event.id WHERE event.id = ?`, id)
	e, err := scanEvent(row, true)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return e, err
}

// Totals is the error count of one DIMM in one slot, computed from the ledger.
type Totals struct {
	Host string
	// DIMMUID is empty for errors that could not be tied to a DIMM.
	DIMMUID string
	Slot    string
	// CE and UE count errors: SUM(count - covered) over records that are
	// not duplicates.
	CE, UE int64
	// Records is the number of ledger records behind the counts.
	Records     int64
	First, Last time.Time
	// ErrorsA, ErrorsB and ErrorsC split CE+UE by observation grade.
	ErrorsA, ErrorsB, ErrorsC int64
}

// Totals returns error counts per host, DIMM and slot. Duplicates and
// informational records never count, whatever the filter says.
func (s *Store) Totals(ctx context.Context, f EventFilter) ([]Totals, error) {
	f.WithDuplicates = false
	where, args := f.where()
	rows, err := s.db.QueryContext(ctx, `SELECT host, COALESCE(dimm_uid, ''), slot,
  COALESCE(SUM(CASE WHEN "ErrorSeverity" = 'CE' THEN count - covered ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN "ErrorSeverity" IN ('Recoverable', 'UE', 'Fatal') THEN count - covered ELSE 0 END), 0),
  COUNT(*), MIN("ErrorDT"), MAX("ErrorDT"),
  COALESCE(SUM(CASE WHEN grade = 'A' THEN count - covered ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN grade = 'B' THEN count - covered ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN grade = 'C' THEN count - covered ELSE 0 END), 0)
 FROM event`+where+` AND "ErrorSeverity" != 'Informational'
 GROUP BY host, COALESCE(dimm_uid, ''), slot
 ORDER BY host, slot, 2`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Totals
	for rows.Next() {
		var t Totals
		var first, last sql.NullString
		if err := rows.Scan(&t.Host, &t.DIMMUID, &t.Slot, &t.CE, &t.UE, &t.Records, &first, &last, &t.ErrorsA, &t.ErrorsB, &t.ErrorsC); err != nil {
			return nil, err
		}
		t.First, t.Last = parseTS(first), parseTS(last)
		out = append(out, t)
	}
	return out, rows.Err()
}

// DayCount is the number of errors on one calendar day.
type DayCount struct {
	Day    string // YYYY-MM-DD
	CE, UE int64
}

// Daily returns error counts per day for the events selected by f. Days are
// cut at UTC midnight shifted by offsetSeconds, so a caller can bucket by
// the local day of the host. A counter record counts on the day its window
// ends.
func (s *Store) Daily(ctx context.Context, f EventFilter, offsetSeconds int) ([]DayCount, error) {
	f.WithDuplicates = false
	where, args := f.where()
	day := `substr("ErrorDT", 1, 10)`
	if offsetSeconds != 0 {
		day = fmt.Sprintf(`strftime('%%Y-%%m-%%d', "ErrorDT", '%+d seconds')`, offsetSeconds)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+day+` AS day,
  COALESCE(SUM(CASE WHEN "ErrorSeverity" = 'CE' THEN count - covered ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN "ErrorSeverity" IN ('Recoverable', 'UE', 'Fatal') THEN count - covered ELSE 0 END), 0)
 FROM event`+where+` AND "ErrorSeverity" != 'Informational' GROUP BY day ORDER BY day`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DayCount
	for rows.Next() {
		var d DayCount
		if err := rows.Scan(&d.Day, &d.CE, &d.UE); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Hosts returns the hosts in the ledger.
func (s *Store) Hosts(ctx context.Context) ([]model.Host, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, arch, system_vendor, system_product, board, virtual, first_seen, last_seen FROM host ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Host
	for rows.Next() {
		var h model.Host
		var first, last sql.NullString
		if err := rows.Scan(&h.Name, &h.Arch, &h.SystemVendor, &h.SystemProduct, &h.Board, &h.Virtual, &first, &last); err != nil {
			return nil, err
		}
		h.FirstSeen, h.LastSeen = parseTS(first), parseTS(last)
		out = append(out, h)
	}
	return out, rows.Err()
}

// Boots returns the boots of a host in start order. An empty host selects all.
func (s *Store) Boots(ctx context.Context, host string) ([]model.Boot, error) {
	q, args := `SELECT `+bootColumns+` FROM boot`, []any{}
	if host != "" {
		q, args = q+` WHERE host = ?`, append(args, host)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY host, started_at, boot_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Boot
	for rows.Next() {
		b, err := scanBoot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

const dimmColumns = `dimm_uid, identity, manufacturer, part_number, serial, manufacturer_id, size_bytes, memory_type, form_factor,
 module_type, technology, ranks, device_width, data_width, total_width, speed_mts, configured_speed_mts, mfg_date, first_seen, last_seen`

func scanDIMM(r rowScanner) (*model.DIMM, error) {
	var d model.DIMM
	var identity string
	var size, ranks, devW, dataW, totalW, speed, cfgSpeed sql.NullInt64
	var first, last sql.NullString
	if err := r.Scan(&d.UID, &identity, &d.Manufacturer, &d.PartNumber, &d.Serial, &d.ManufacturerID, &size, &d.MemoryType,
		&d.FormFactor, &d.ModuleType, &d.Technology, &ranks, &devW, &dataW, &totalW, &speed, &cfgSpeed, &d.MfgDate, &first, &last); err != nil {
		return nil, err
	}
	d.Identity = model.IdentityKind(identity)
	d.SizeBytes = size.Int64
	d.Ranks, d.DeviceWidth, d.DataWidth, d.TotalWidth = toOptInt(ranks), toOptInt(devW), toOptInt(dataW), toOptInt(totalW)
	d.SpeedMTs, d.ConfiguredSpeedMTs = toOptInt(speed), toOptInt(cfgSpeed)
	d.FirstSeen, d.LastSeen = parseTS(first), parseTS(last)
	return &d, nil
}

// DIMMs returns all DIMM identities in the ledger.
func (s *Store) DIMMs(ctx context.Context) ([]model.DIMM, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+dimmColumns+` FROM dimm ORDER BY dimm_uid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.DIMM
	for rows.Next() {
		d, err := scanDIMM(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// DIMM returns one DIMM identity, or nil when the UID is unknown.
func (s *Store) DIMM(ctx context.Context, uid string) (*model.DIMM, error) {
	d, err := scanDIMM(s.db.QueryRowContext(ctx, `SELECT `+dimmColumns+` FROM dimm WHERE dimm_uid = ?`, uid))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return d, err
}

const slotColumns = `host, name, locator, bank_locator, smbios_handle, socket, imc, channel, position, edac_mc, edac_location, edac_label, mapping, populated`

func scanSlot(r rowScanner) (*model.Slot, error) {
	var sl model.Slot
	var handle, socket, imc, channel, position, edacMC, populated sql.NullInt64
	if err := r.Scan(&sl.Host, &sl.Name, &sl.Locator, &sl.BankLocator, &handle, &socket, &imc, &channel, &position,
		&edacMC, &sl.EdacLocation, &sl.EdacLabel, &sl.Mapping, &populated); err != nil {
		return nil, err
	}
	sl.SMBIOSHandle, sl.Socket, sl.IMC, sl.Channel, sl.Position = toOptInt(handle), toOptInt(socket), toOptInt(imc), toOptInt(channel), toOptInt(position)
	sl.EdacMC, sl.Populated = toOptInt(edacMC), toOptBool(populated)
	return &sl, nil
}

// Slots returns the slots of a host, ordered by socket, controller, channel
// and position, with slots of unknown position last.
func (s *Store) Slots(ctx context.Context, host string) ([]model.Slot, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+slotColumns+` FROM slot WHERE host = ?
 ORDER BY socket IS NULL, socket, imc IS NULL, imc, channel IS NULL, channel, position IS NULL, position, name`, host)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Slot
	for rows.Next() {
		sl, err := scanSlot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sl)
	}
	return out, rows.Err()
}

// LatestCounterSamples returns the per-DIMM samples of the most recent
// reading of the EDAC counters of a host: the EDAC DIMM objects that
// existed then. Counters of sources that place a DIMM by numbers are not
// EDAC objects and are left out.
func (s *Store) LatestCounterSamples(ctx context.Context, host string) ([]model.CounterSample, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT observed_at, mc, mc_name, dimm_index, location, label, ce, ue, size_mb, mem_type, dev_type, edac_mode, source, raw_ref
 FROM counter_sample
 WHERE host = ? AND dimm_index >= 0 AND "SocketId" IS NULL
   AND observed_at = (SELECT MAX(observed_at) FROM counter_sample WHERE host = ? AND dimm_index >= 0 AND "SocketId" IS NULL)
 ORDER BY mc, dimm_index`, host, host)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.CounterSample
	for rows.Next() {
		var c model.CounterSample
		var at sql.NullString
		var ce, ue int64
		var size sql.NullInt64
		if err := rows.Scan(&at, &c.MC, &c.MCName, &c.DIMMIndex, &c.Location, &c.Label, &ce, &ue, &size, &c.MemType, &c.DevType, &c.EdacMode, &c.Source, &c.RawRef); err != nil {
			return nil, err
		}
		c.ObservedAt, c.CE, c.UE, c.SizeMB = parseTS(at), uint64(ce), uint64(ue), toOptInt(size)
		out = append(out, c)
	}
	return out, rows.Err()
}

// SlotOccupants returns, for each slot of a host that ever held a DIMM, the
// DIMM of its most recent placement.
func (s *Store) SlotOccupants(ctx context.Context, host string) (map[string]model.DIMM, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.slot, `+qualify("dimm", dimmColumns)+` FROM placement p
 JOIN dimm ON dimm.dimm_uid = p.dimm_uid
 WHERE p.host = ? AND p.id = (
   SELECT q.id FROM placement q WHERE q.host = p.host AND q.slot = p.slot ORDER BY q.last_seen DESC, q.id DESC LIMIT 1)`, host)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]model.DIMM{}
	for rows.Next() {
		var slot string
		d, err := scanDIMM(prefixScanner{rows, &slot})
		if err != nil {
			return nil, err
		}
		out[slot] = *d
	}
	return out, rows.Err()
}

// qualify prefixes every column of a comma-separated list with a table name.
func qualify(table, columns string) string {
	parts := strings.Split(columns, ",")
	for i, c := range parts {
		parts[i] = table + "." + strings.TrimSpace(c)
	}
	return strings.Join(parts, ", ")
}

// prefixScanner scans one leading column before the columns its user asks for.
type prefixScanner struct {
	r     rowScanner
	first any
}

func (p prefixScanner) Scan(dest ...any) error {
	return p.r.Scan(append([]any{p.first}, dest...)...)
}

// PruneOrphans removes rows nothing refers to any more after slots were
// re-mapped: slots that came from an EDAC label or an event and have no
// alias, event, action or placement left, and placeholder DIMM identities
// no record uses.
func (s *Store) PruneOrphans(ctx context.Context, host string) error {
	return s.Update(ctx, func(w *Writer) error {
		if _, err := w.exec(`DELETE FROM slot WHERE host = ? AND locator = '' AND smbios_handle IS NULL
 AND NOT EXISTS (SELECT 1 FROM slot_alias a WHERE a.host = slot.host AND a.slot = slot.name)
 AND NOT EXISTS (SELECT 1 FROM event e WHERE e.host = slot.host AND e.slot = slot.name)
 AND NOT EXISTS (SELECT 1 FROM action x WHERE x.host = slot.host AND x.slot = slot.name)
 AND NOT EXISTS (SELECT 1 FROM ecs_sample c WHERE c.host = slot.host AND c.slot = slot.name)
 AND NOT EXISTS (SELECT 1 FROM placement p WHERE p.host = slot.host AND p.slot = slot.name)`, host); err != nil {
			return fmt.Errorf("prune slots: %w", err)
		}
		if _, err := w.exec(`DELETE FROM dimm WHERE identity = 'slot'
 AND NOT EXISTS (SELECT 1 FROM event e WHERE e.dimm_uid = dimm.dimm_uid)
 AND NOT EXISTS (SELECT 1 FROM action x WHERE x.dimm_uid = dimm.dimm_uid)
 AND NOT EXISTS (SELECT 1 FROM ecs_sample c WHERE c.dimm_uid = dimm.dimm_uid)
 AND NOT EXISTS (SELECT 1 FROM placement p WHERE p.dimm_uid = dimm.dimm_uid)
 AND NOT EXISTS (SELECT 1 FROM fault f WHERE f.dimm_uid = dimm.dimm_uid)
 AND NOT EXISTS (SELECT 1 FROM verdict v WHERE v.dimm_uid = dimm.dimm_uid)`); err != nil {
			return fmt.Errorf("prune placeholder DIMMs: %w", err)
		}
		return nil
	})
}

// SlotAliases returns the alias table of a host: alias to slot name.
func (s *Store) SlotAliases(ctx context.Context, host string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT alias, slot FROM slot_alias WHERE host = ?`, host)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var a, sl string
		if err := rows.Scan(&a, &sl); err != nil {
			return nil, err
		}
		out[a] = sl
	}
	return out, rows.Err()
}

// Placements returns placements in time order. Empty arguments do not restrict.
func (s *Store) Placements(ctx context.Context, host, dimmUID string) ([]model.Placement, error) {
	q := `SELECT id, dimm_uid, host, slot, first_seen, last_seen, source FROM placement`
	var conds []string
	var args []any
	if host != "" {
		conds, args = append(conds, `host = ?`), append(args, host)
	}
	if dimmUID != "" {
		conds, args = append(conds, `dimm_uid = ?`), append(args, dimmUID)
	}
	if len(conds) > 0 {
		q += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY host, slot, first_seen, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Placement
	for rows.Next() {
		var p model.Placement
		var first, last sql.NullString
		if err := rows.Scan(&p.ID, &p.DIMMUID, &p.Host, &p.Slot, &first, &last, &p.Source); err != nil {
			return nil, err
		}
		p.FirstSeen, p.LastSeen = parseTS(first), parseTS(last)
		out = append(out, p)
	}
	return out, rows.Err()
}

// ActionFilter selects actions. Zero fields do not restrict.
type ActionFilter struct {
	Host     string
	DIMMUID  string
	Slot     string
	From, To time.Time
	Kinds    []model.ActionKind
	// Duplicates includes the actions that repeat another action (dup_of
	// set). They are left out by default.
	Duplicates bool
}

// Actions returns the actions selected by f in time order.
func (s *Store) Actions(ctx context.Context, f ActionFilter) ([]model.Action, error) {
	var conds []string
	var args []any
	add := func(c string, a ...any) {
		conds = append(conds, c)
		args = append(args, a...)
	}
	if f.Host != "" {
		add(`host = ?`, f.Host)
	}
	if f.DIMMUID != "" {
		add(`dimm_uid = ?`, f.DIMMUID)
	}
	if f.Slot != "" {
		add(`slot = ?`, f.Slot)
	}
	if !f.From.IsZero() {
		add(`at >= ?`, ts(f.From))
	}
	if !f.To.IsZero() {
		add(`at < ?`, ts(f.To))
	}
	if len(f.Kinds) > 0 {
		var a []any
		for _, k := range f.Kinds {
			a = append(a, string(k))
		}
		add(`kind IN (`+placeholders(len(a))+`)`, a...)
	}
	if !f.Duplicates {
		add(`dup_of IS NULL`)
	}
	q := `SELECT id, host, at, time_quality, kind, method, result, "OsPhysicalAddress", "Rank", "DeviceId", "BankGroup", "Bank", "Row",
 label, slot, dimm_uid, count, boot_id, detail, source, raw_ref, raw, fingerprint, scan_id, dup_of, withdrawn_by FROM action`
	if len(conds) > 0 {
		q += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY at, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Action
	for rows.Next() {
		var a model.Action
		var at, quality, kind, result string
		var addr, rank, dev, bg, bank, row, scanID, dupOf, withdrawn sql.NullInt64
		var dimmUID, bootID sql.NullString
		if err := rows.Scan(&a.ID, &a.Host, &at, &quality, &kind, &a.Method, &result, &addr, &rank, &dev, &bg, &bank, &row,
			&a.Label, &a.Slot, &dimmUID, &a.Count, &bootID, &a.Detail, &a.Source, &a.RawRef, &a.Raw, &a.Fingerprint, &scanID,
			&dupOf, &withdrawn); err != nil {
			return nil, err
		}
		a.DupOf, a.WithdrawnBy = dupOf.Int64, withdrawn.Int64
		a.At = parseTS(sql.NullString{String: at, Valid: true})
		a.TimeQuality, a.Kind, a.Result = model.TimeQuality(quality), model.ActionKind(kind), model.ActionResult(result)
		a.OsPhysicalAddress = toOptU64(addr)
		a.Rank, a.DeviceId, a.BankGroup, a.Bank, a.Row = toOptInt(rank), toOptInt(dev), toOptInt(bg), toOptInt(bank), toOptInt(row)
		a.DIMMUID, a.BootID, a.ScanID = dimmUID.String, bootID.String, scanID.Int64
		out = append(out, a)
	}
	return out, rows.Err()
}

// Scans returns all collection runs in start order.
func (s *Store) Scans(ctx context.Context) ([]model.Scan, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, host, kind, started_at, finished_at, tool_version, privileged, note FROM scan ORDER BY started_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Scan
	for rows.Next() {
		var sc model.Scan
		var started, finished sql.NullString
		var priv int64
		if err := rows.Scan(&sc.ID, &sc.Host, &sc.Kind, &started, &finished, &sc.ToolVersion, &priv, &sc.Note); err != nil {
			return nil, err
		}
		sc.StartedAt, sc.FinishedAt, sc.Privileged = parseTS(started), parseTS(finished), priv != 0
		out = append(out, sc)
	}
	return out, rows.Err()
}

// Sources returns the source inventory of a scan; scanID 0 selects all scans.
func (s *Store) Sources(ctx context.Context, scanID int64) ([]model.Source, error) {
	q, args := `SELECT id, scan_id, kind, origin, status, detail, records, events, actions, skipped, samples, grade_a, grade_b, grade_c, first_ts, last_ts, notes,
 unread, faults FROM source`, []any{}
	if scanID != 0 {
		q, args = q+` WHERE scan_id = ?`, append(args, scanID)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY scan_id, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Source
	for rows.Next() {
		var src model.Source
		var status, notes string
		var first, last sql.NullString
		if err := rows.Scan(&src.ID, &src.ScanID, &src.Kind, &src.Origin, &status, &src.Detail, &src.Records, &src.Events, &src.Actions,
			&src.Skipped, &src.Samples, &src.GradeA, &src.GradeB, &src.GradeC, &first, &last, &notes, &src.Unread, &src.Faults); err != nil {
			return nil, err
		}
		src.Status = model.SourceStatus(status)
		src.FirstTS, src.LastTS = parseTS(first), parseTS(last)
		if err := json.Unmarshal([]byte(notes), &src.Notes); err != nil {
			return nil, fmt.Errorf("source %d: notes: %w", src.ID, err)
		}
		if len(src.Notes) == 0 {
			src.Notes = nil
		}
		out = append(out, src)
	}
	return out, rows.Err()
}

// SourceGrades returns the best observation grade among the errors of a
// host, per source name of their records ("rasdaemon/mc_event",
// "edac-sysfs/counter"). Informational records are no errors and are left
// out. The source inventory counts what an adapter produced; this counts
// what is in the ledger, which includes the errors that were derived from
// counter readings.
func (s *Store) SourceGrades(ctx context.Context, host string) (map[string]model.Grade, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT source, MAX(CASE grade WHEN 'A' THEN 3 WHEN 'B' THEN 2 ELSE 1 END)
 FROM event WHERE host = ? AND "ErrorSeverity" != 'Informational' GROUP BY source`, host)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]model.Grade{}
	for rows.Next() {
		var source string
		var rank int
		if err := rows.Scan(&source, &rank); err != nil {
			return nil, err
		}
		out[source] = []model.Grade{model.GradeC, model.GradeC, model.GradeB, model.GradeA}[min(max(rank, 0), 3)]
	}
	return out, rows.Err()
}

// Faults returns the stored faults of a host with their member event IDs.
func (s *Store) Faults(ctx context.Context, host string) ([]model.Fault, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, host, dimm_uid, slot, mode, estimated, grade, "Rank", "DeviceId", "BankGroup", "Bank",
 row_min, row_max, column_min, column_max, address_min, address_max, first_seen, last_seen, event_count, error_count, confidence, engine_version
 FROM fault WHERE host = ? ORDER BY slot, first_seen, id`, host)
	if err != nil {
		return nil, err
	}
	var out []model.Fault
	for rows.Next() {
		var f model.Fault
		var dimmUID sql.NullString
		var mode, grade string
		var est int64
		var rank, dev, bg, bank, rmin, rmax, cmin, cmax, amin, amax sql.NullInt64
		var first, last sql.NullString
		if err := rows.Scan(&f.ID, &f.Host, &dimmUID, &f.Slot, &mode, &est, &grade, &rank, &dev, &bg, &bank,
			&rmin, &rmax, &cmin, &cmax, &amin, &amax, &first, &last, &f.EventCount, &f.ErrorCount, &f.Confidence, &f.EngineVersion); err != nil {
			rows.Close()
			return nil, err
		}
		f.DIMMUID, f.Mode, f.Estimated, f.Grade = dimmUID.String, model.FaultMode(mode), est != 0, model.Grade(grade)
		f.Rank, f.DeviceId, f.BankGroup, f.Bank = toOptInt(rank), toOptInt(dev), toOptInt(bg), toOptInt(bank)
		f.RowMin, f.RowMax, f.ColumnMin, f.ColumnMax = toOptInt(rmin), toOptInt(rmax), toOptInt(cmin), toOptInt(cmax)
		f.AddressMin, f.AddressMax = toOptU64(amin), toOptU64(amax)
		f.FirstSeen, f.LastSeen = parseTS(first), parseTS(last)
		out = append(out, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		ids, err := s.db.QueryContext(ctx, `SELECT event_id FROM fault_event WHERE fault_id = ? ORDER BY event_id`, out[i].ID)
		if err != nil {
			return nil, err
		}
		for ids.Next() {
			var id int64
			if err := ids.Scan(&id); err != nil {
				ids.Close()
				return nil, err
			}
			out[i].EventIDs = append(out[i].EventIDs, id)
		}
		ids.Close()
		if err := ids.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Verdicts returns the stored verdicts of a host.
func (s *Store) Verdicts(ctx context.Context, host string) ([]model.Verdict, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, host, dimm_uid, slot, gpu, grade, rules, evidence, counter_evidence, missing, next_action,
 confidence, confidence_factors, engine_version, computed_at FROM verdict WHERE host = ? ORDER BY slot, gpu, id`, host)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Verdict
	for rows.Next() {
		v := model.Verdict{Label: model.VerdictLabel}
		var dimmUID, computed sql.NullString
		var grade, rules, evidence, counter, missing, next, factors string
		if err := rows.Scan(&v.ID, &v.Host, &dimmUID, &v.Slot, &v.GPU, &grade, &rules, &evidence, &counter, &missing, &next,
			&v.Confidence, &factors, &v.EngineVersion, &computed); err != nil {
			return nil, err
		}
		v.DIMMUID, v.Grade, v.ComputedAt = dimmUID.String, model.VerdictGrade(grade), parseTS(computed)
		parts := []struct {
			text string
			dst  any
		}{{rules, &v.Rules}, {evidence, &v.Evidence}, {counter, &v.CounterEvidence}, {missing, &v.Missing}, {factors, &v.ConfidenceFactors}}
		if next != "" {
			parts = append(parts, struct {
				text string
				dst  any
			}{next, &v.NextAction})
		}
		for _, p := range parts {
			if err := json.Unmarshal([]byte(p.text), p.dst); err != nil {
				return nil, fmt.Errorf("verdict %d: %w", v.ID, err)
			}
		}
		// An empty list reads back as nil, whatever was stored.
		if len(v.Rules) == 0 {
			v.Rules = nil
		}
		if len(v.Evidence) == 0 {
			v.Evidence = nil
		}
		if len(v.CounterEvidence) == 0 {
			v.CounterEvidence = nil
		}
		if len(v.Missing) == 0 {
			v.Missing = nil
		}
		if len(v.ConfidenceFactors) == 0 {
			v.ConfidenceFactors = nil
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// EventRawRefs returns the raw_ref of the events with the given IDs: where
// the original record of each can be found.
func (s *Store) EventRawRefs(ctx context.Context, ids []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(ids))
	const batch = 500
	for start := 0; start < len(ids); start += batch {
		chunk := ids[start:min(start+batch, len(ids))]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := s.db.QueryContext(ctx, `SELECT id, raw_ref FROM event WHERE id IN (`+placeholders(len(chunk))+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			var ref string
			if err := rows.Scan(&id, &ref); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = ref
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// EventPlace is one distinct way the events of a host name the place of an
// error, together with the slot those events were resolved to. Comparing
// the names with the slot shows records whose names point to different
// slots.
type EventPlace struct {
	Host string
	LocationKey
	// Slot is the slot the events were tied to.
	Slot string
	// FirstID and LastID are the IDs of the first and the last event that
	// use the names; LocationKey.Records says how many there are.
	FirstID, LastID int64
}

// EventPlaces returns the distinct combinations of location names and
// resolved slot among the error records of a host that count. An empty host
// selects all hosts.
func (s *Store) EventPlaces(ctx context.Context, host string) ([]EventPlace, error) {
	q := `SELECT host, edac_mc, edac_location, smbios_handle, label, "SocketId", imc, "ChannelId", "ChannelSlotId", slot, COUNT(*), MIN(id), MAX(id)
 FROM event WHERE dup_of IS NULL AND kind = 'error' AND slot != ''`
	var args []any
	if host != "" {
		q, args = q+` AND host = ?`, append(args, host)
	}
	rows, err := s.db.QueryContext(ctx, q+`
 GROUP BY host, edac_mc, edac_location, smbios_handle, label, "SocketId", imc, "ChannelId", "ChannelSlotId", slot
 ORDER BY host, slot, edac_mc, edac_location, label, "SocketId", imc, "ChannelId", "ChannelSlotId"`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventPlace
	for rows.Next() {
		var p EventPlace
		var mc, handle, socket, imc, ch, pos sql.NullInt64
		if err := rows.Scan(&p.Host, &mc, &p.EdacLocation, &handle, &p.Label, &socket, &imc, &ch, &pos, &p.Slot, &p.Records, &p.FirstID, &p.LastID); err != nil {
			return nil, err
		}
		p.EdacMC, p.SMBIOSHandle = toOptInt(mc), toOptInt(handle)
		p.SocketId, p.IMC, p.ChannelId, p.ChannelSlotId = toOptInt(socket), toOptInt(imc), toOptInt(ch), toOptInt(pos)
		out = append(out, p)
	}
	return out, rows.Err()
}

// GPUMem returns the GPU memory records of a host in time order.
func (s *Store) GPUMem(ctx context.Context, host string) ([]model.GPUMem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, `+gpuColumns+` FROM gpu_mem WHERE host = ? ORDER BY observed_at, id`, host)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.GPUMem
	for rows.Next() {
		var g model.GPUMem
		var observed, bootID sql.NullString
		var quality string
		var idx, xid, rc, ru, rp, rf, bmax, bhigh, bpart, blow, bnone, sbe, dbe, rpend sql.NullInt64
		var ecc, dc, du, dcv, duv, sc, su, scv, suv, sth, scanID sql.NullInt64
		if err := rows.Scan(&g.ID, &g.Host, &observed, &quality, &g.Kind, &g.GPUSerial, &g.GPUUUID, &idx, &g.PCIBus, &g.Model, &xid, &g.XIDText,
			&rc, &ru, &rp, &rf, &bmax, &bhigh, &bpart, &blow, &bnone, &sbe, &dbe, &rpend,
			&ecc, &dc, &du, &dcv, &duv, &sc, &su, &scv, &suv, &sth,
			&bootID, &g.Source, &g.RawRef, &g.Raw, &g.Fingerprint, &scanID); err != nil {
			return nil, err
		}
		g.ObservedAt, g.BootID, g.TimeQuality, g.ScanID = parseTS(observed), bootID.String, model.TimeQuality(quality), scanID.Int64
		g.GPUIndex, g.XID = toOptInt(idx), toOptInt(xid)
		g.RemapCorrectable, g.RemapUncorrectable, g.RemapPending, g.RemapFailure = toOptInt(rc), toOptInt(ru), toOptBool(rp), toOptBool(rf)
		g.BankMax, g.BankHigh, g.BankPartial, g.BankLow, g.BankNone = toOptInt(bmax), toOptInt(bhigh), toOptInt(bpart), toOptInt(blow), toOptInt(bnone)
		g.RetiredSBE, g.RetiredDBE, g.RetiredPending = toOptInt(sbe), toOptInt(dbe), toOptBool(rpend)
		g.ECCEnabled, g.SRAMThresholdExceeded = toOptBool(ecc), toOptBool(sth)
		g.DRAMCorrectable, g.DRAMUncorrectable = toOptInt(dc), toOptInt(du)
		g.DRAMCorrectableVolatile, g.DRAMUncorrectableVolatile = toOptInt(dcv), toOptInt(duv)
		g.SRAMCorrectable, g.SRAMUncorrectable = toOptInt(sc), toOptInt(su)
		g.SRAMCorrectableVolatile, g.SRAMUncorrectableVolatile = toOptInt(scv), toOptInt(suv)
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	completeGPUs(out)
	return out, nil
}

// completeGPUs fills in the identity of GPU memory records that name their
// GPU by less than other records of the host do. An XID line of the kernel
// log names a GPU by its PCI address, and by its UUID only when the driver
// printed it in the same boot; a snapshot names it by serial number, UUID
// and PCI address. The rows in the ledger stay as the sources gave them:
// the identity is completed on reading, so a snapshot imported later
// completes records that were imported before it.
//
// A UUID is taken from a record with the same PCI address, of the same
// boot when there is one and nearest in time otherwise. Serial number,
// index and model follow the UUID, which does not change over the life of
// a GPU.
func completeGPUs(recs []model.GPUMem) {
	byBus := map[string][]int{}
	for i := range recs {
		if r := &recs[i]; r.PCIBus != "" && r.GPUUUID != "" {
			byBus[r.PCIBus] = append(byBus[r.PCIBus], i)
		}
	}
	for i := range recs {
		r := &recs[i]
		if r.GPUUUID != "" || r.PCIBus == "" {
			continue
		}
		best, bestDiff, sameBoot := -1, time.Duration(0), false
		for _, j := range byBus[r.PCIBus] {
			c := &recs[j]
			boot := r.BootID != "" && c.BootID == r.BootID
			if !boot && (r.TimeQuality == model.TimeRelative || c.TimeQuality == model.TimeRelative) {
				continue // no common clock
			}
			d := c.ObservedAt.Sub(r.ObservedAt).Abs()
			switch {
			case best < 0, boot && !sameBoot, boot == sameBoot && d < bestDiff:
				best, bestDiff, sameBoot = j, d, boot
			}
		}
		if best >= 0 {
			r.GPUUUID = recs[best].GPUUUID
		}
	}

	// The latest record that knows more about a UUID describes its GPU.
	type ident struct {
		serial, model string
		index         model.Opt[int]
	}
	byUUID := map[string]*ident{}
	for i := range recs {
		r := &recs[i]
		if r.GPUUUID == "" {
			continue
		}
		id := byUUID[r.GPUUUID]
		if id == nil {
			id = &ident{}
			byUUID[r.GPUUUID] = id
		}
		if r.GPUSerial != "" {
			id.serial = r.GPUSerial
		}
		if r.Model != "" {
			id.model = r.Model
		}
		if r.GPUIndex.Valid {
			id.index = r.GPUIndex
		}
	}
	for i := range recs {
		r := &recs[i]
		id := byUUID[r.GPUUUID]
		if id == nil {
			continue
		}
		if r.GPUSerial == "" {
			r.GPUSerial = id.serial
		}
		if r.Model == "" {
			r.Model = id.model
		}
		if !r.GPUIndex.Valid {
			r.GPUIndex = id.index
		}
	}
}

// ECSFilter selects ECS readings. Empty fields do not restrict.
type ECSFilter struct {
	Host    string
	Slot    string
	DIMMUID string
}

// ECS returns readings of the ECS registers in time order.
func (s *Store) ECS(ctx context.Context, f ECSFilter) ([]model.ECSSample, error) {
	var conds []string
	var args []any
	if f.Host != "" {
		conds, args = append(conds, `host = ?`), append(args, f.Host)
	}
	if f.Slot != "" {
		conds, args = append(conds, `slot = ?`), append(args, f.Slot)
	}
	if f.DIMMUID != "" {
		conds, args = append(conds, `dimm_uid = ?`), append(args, f.DIMMUID)
	}
	q := `SELECT id, ` + ecsColumns + ` FROM ecs_sample`
	if len(conds) > 0 {
		q += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY observed_at, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ECSSample
	for rows.Next() {
		var e model.ECSSample
		var observed, dimmUID, bootID sql.NullString
		var quality string
		var socket, ch, pos, sub, rank, dev, scanID sql.NullInt64
		var mr [7]sql.NullInt64
		if err := rows.Scan(&e.ID, &e.Host, &observed, &quality, &socket, &ch, &pos, &sub, &rank, &dev,
			&e.Label, &e.Slot, &dimmUID, &mr[0], &mr[1], &mr[2], &mr[3], &mr[4], &mr[5], &mr[6],
			&bootID, &e.Source, &e.RawRef, &e.Raw, &e.Fingerprint, &scanID); err != nil {
			return nil, err
		}
		e.ObservedAt, e.TimeQuality = parseTS(observed), model.TimeQuality(quality)
		e.SocketId, e.ChannelId, e.ChannelSlotId = toOptInt(socket), toOptInt(ch), toOptInt(pos)
		e.SubChannel, e.Rank, e.DeviceId = toOptInt(sub), toOptInt(rank), toOptInt(dev)
		e.DIMMUID, e.BootID, e.ScanID = dimmUID.String, bootID.String, scanID.Int64
		e.MR14, e.MR15, e.MR16, e.MR17 = toOptInt(mr[0]), toOptInt(mr[1]), toOptInt(mr[2]), toOptInt(mr[3])
		e.MR18, e.MR19, e.MR20 = toOptInt(mr[4]), toOptInt(mr[5]), toOptInt(mr[6])
		out = append(out, e)
	}
	return out, rows.Err()
}

// CaptureChecks returns what "my preflight" found during the latest scan
// of the host that stored its checks, in the order of the checks; nil when
// no scan did.
func (s *Store) CaptureChecks(ctx context.Context, host string) ([]model.CaptureCheck, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT scan_id, host, name, state, detail, evidence FROM capture_check
 WHERE host = ? AND scan_id = (SELECT MAX(scan_id) FROM capture_check WHERE host = ?) ORDER BY rowid`, host, host)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.CaptureCheck
	for rows.Next() {
		var c model.CaptureCheck
		var detail, evidence string
		if err := rows.Scan(&c.ScanID, &c.Host, &c.Name, &c.State, &detail, &evidence); err != nil {
			return nil, err
		}
		if detail != "" {
			if err := json.Unmarshal([]byte(detail), &c.Detail); err != nil {
				c.Detail = model.Statement{Text: detail}
			}
		}
		_ = json.Unmarshal([]byte(evidence), &c.Evidence)
		out = append(out, c)
	}
	return out, rows.Err()
}

// recordTables are the tables that hold what sources read; a run that adds
// to none of them added nothing.
var recordTables = []string{"event", "action", "boot", "placement", "gpu_mem", "ecs_sample", "bmc", "bmc_memory", "bios_setting", "fw_record"}

// RecordCounts returns the number of rows of each table that holds what
// sources read, for all hosts.
func (s *Store) RecordCounts(ctx context.Context) (map[string]int64, error) {
	out := map[string]int64{}
	for _, t := range recordTables {
		var n int64
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM `+t).Scan(&n); err != nil {
			return nil, err
		}
		out[t] = n
	}
	return out, nil
}
