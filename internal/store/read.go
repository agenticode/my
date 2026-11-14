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
	)
	dest := []any{
		&e.ID, &e.Host, &kind, &dt, &sev, &scrub,
		&socket, &channel, &chSlot, &subCh, &rank, &dev, &bg, &bank, &row, &col,
		&e.ErrorVector, &addr, &e.Source, &e.RawRef, &grade, &bootID, &dimmUID, &e.DIMMLink,
		&e.Count, &e.Covered, &dupOf, &e.DupOfFingerprint, &quality, &windowStart, &utcOff,
		&imc, &edacMC, &e.EdacLocation, &handle, &e.Label, &e.Slot, &syndrome, &e.ErrorType, &e.Decoder, &e.Fingerprint, &scanID,
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
	rows, err := s.db.QueryContext(ctx, `SELECT name, arch, system_vendor, system_product, board, first_seen, last_seen FROM host ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Host
	for rows.Next() {
		var h model.Host
		var first, last sql.NullString
		if err := rows.Scan(&h.Name, &h.Arch, &h.SystemVendor, &h.SystemProduct, &h.Board, &first, &last); err != nil {
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
	q := `SELECT id, host, at, time_quality, kind, method, result, "OsPhysicalAddress", "Rank", "DeviceId", "BankGroup", "Bank", "Row",
 label, slot, dimm_uid, count, boot_id, detail, source, raw_ref, raw, fingerprint, scan_id FROM action`
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
		var addr, rank, dev, bg, bank, row, scanID sql.NullInt64
		var dimmUID, bootID sql.NullString
		if err := rows.Scan(&a.ID, &a.Host, &at, &quality, &kind, &a.Method, &result, &addr, &rank, &dev, &bg, &bank, &row,
			&a.Label, &a.Slot, &dimmUID, &a.Count, &bootID, &a.Detail, &a.Source, &a.RawRef, &a.Raw, &a.Fingerprint, &scanID); err != nil {
			return nil, err
		}
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
	q, args := `SELECT id, scan_id, kind, origin, status, detail, records, events, actions, skipped, grade_a, grade_b, grade_c, first_ts, last_ts, notes FROM source`, []any{}
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
			&src.Skipped, &src.GradeA, &src.GradeB, &src.GradeC, &first, &last, &notes); err != nil {
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
	rows, err := s.db.QueryContext(ctx, `SELECT id, host, dimm_uid, slot, grade, rules, evidence, counter_evidence, missing, next_action,
 confidence, engine_version, computed_at FROM verdict WHERE host = ? ORDER BY slot, id`, host)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Verdict
	for rows.Next() {
		var v model.Verdict
		var dimmUID, computed sql.NullString
		var grade, rules, evidence, counter, missing string
		if err := rows.Scan(&v.ID, &v.Host, &dimmUID, &v.Slot, &grade, &rules, &evidence, &counter, &missing, &v.NextAction,
			&v.Confidence, &v.EngineVersion, &computed); err != nil {
			return nil, err
		}
		v.DIMMUID, v.Grade, v.ComputedAt = dimmUID.String, model.VerdictGrade(grade), parseTS(computed)
		for _, p := range []struct {
			text string
			dst  any
		}{{rules, &v.Rules}, {evidence, &v.Evidence}, {counter, &v.CounterEvidence}, {missing, &v.Missing}} {
			if err := json.Unmarshal([]byte(p.text), p.dst); err != nil {
				return nil, fmt.Errorf("verdict %d: %w", v.ID, err)
			}
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// GPUMem returns the GPU memory records of a host in time order.
func (s *Store) GPUMem(ctx context.Context, host string) ([]model.GPUMem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, host, observed_at, kind, gpu_serial, gpu_uuid, gpu_index, pci_bus, model, xid, xid_text,
 remap_correctable, remap_uncorrectable, remap_pending, remap_failure, bank_max, bank_high, bank_partial, bank_low, bank_none,
 retired_sbe, retired_dbe, retired_pending, boot_id, source, raw_ref, raw, fingerprint FROM gpu_mem WHERE host = ? ORDER BY observed_at, id`, host)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.GPUMem
	for rows.Next() {
		var g model.GPUMem
		var observed, bootID sql.NullString
		var idx, xid, rc, ru, rp, rf, bmax, bhigh, bpart, blow, bnone, sbe, dbe, rpend sql.NullInt64
		if err := rows.Scan(&g.ID, &g.Host, &observed, &g.Kind, &g.GPUSerial, &g.GPUUUID, &idx, &g.PCIBus, &g.Model, &xid, &g.XIDText,
			&rc, &ru, &rp, &rf, &bmax, &bhigh, &bpart, &blow, &bnone, &sbe, &dbe, &rpend, &bootID, &g.Source, &g.RawRef, &g.Raw, &g.Fingerprint); err != nil {
			return nil, err
		}
		g.ObservedAt, g.BootID = parseTS(observed), bootID.String
		g.GPUIndex, g.XID = toOptInt(idx), toOptInt(xid)
		g.RemapCorrectable, g.RemapUncorrectable, g.RemapPending, g.RemapFailure = toOptInt(rc), toOptInt(ru), toOptBool(rp), toOptBool(rf)
		g.BankMax, g.BankHigh, g.BankPartial, g.BankLow, g.BankNone = toOptInt(bmax), toOptInt(bhigh), toOptInt(bpart), toOptInt(blow), toOptInt(bnone)
		g.RetiredSBE, g.RetiredDBE, g.RetiredPending = toOptInt(sbe), toOptInt(dbe), toOptBool(rpend)
		out = append(out, g)
	}
	return out, rows.Err()
}
