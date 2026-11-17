package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/agenticode/my/internal/model"
)

// The SPD of the memory modules (schema version 12): one row per SPD as a
// scan or an import got it. The bytes are kept as read; the fields are
// decoded when a view is built.

// spdForms are the forms a reading may have.
var spdForms = map[string]bool{model.SPDFormEEPROM: true, model.SPDFormBinary: true, model.SPDFormHex: true, model.SPDFormDecodeDimms: true}

// SPDSum returns the SHA-256 of the bytes of a reading, or of the text of
// decode-dimms output, as stored in the ledger.
func SPDSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// PutSPD keeps an SPD reading. A reading whose bytes, state and detail are
// those of the latest row of the same host and origin moves that row's
// last_seen and scan and adds no row; any other reading adds one. It
// reports whether a row was added.
func (w *Writer) PutSPD(r *model.SPDReading) (bool, error) {
	switch {
	case r.Host == "":
		return false, invalid("spd reading: host is empty")
	case r.Origin == "":
		return false, invalid("spd reading: origin is empty")
	case !spdForms[r.Form]:
		return false, invalid("spd reading %s: form %q", r.Origin, r.Form)
	case r.State == "":
		return false, invalid("spd reading %s: no state", r.Origin)
	case len(r.Raw) > model.MaxSPDBytes:
		return false, invalid("spd reading %s: %d bytes, more than %d", r.Origin, len(r.Raw), model.MaxSPDBytes)
	case len(r.Fields) > 64<<10:
		return false, invalid("spd reading %s: fields of %d bytes", r.Origin, len(r.Fields))
	case r.LastSeen.IsZero():
		return false, invalid("spd reading %s: no time", r.Origin)
	}
	if r.FirstSeen.IsZero() || r.FirstSeen.After(r.LastSeen) {
		r.FirstSeen = r.LastSeen
	}
	if r.SHA256 == "" {
		if r.Form == model.SPDFormDecodeDimms {
			r.SHA256 = SPDSum([]byte(r.Fields))
		} else {
			r.SHA256 = SPDSum(r.Raw)
		}
	}
	var scan any
	if r.ScanID > 0 {
		scan = r.ScanID
	}
	var (
		id                  int64
		sum, state, detail  string
		lastSeen, firstSeen sql.NullString
		dimmUID, tie        string
	)
	err := w.queryRow(`SELECT id, sha256, state, detail, first_seen, last_seen, dimm_uid, tie FROM spd_reading
 WHERE host = ? AND origin = ? ORDER BY last_seen DESC, id DESC LIMIT 1`, r.Host, r.Origin).
		Scan(&id, &sum, &state, &detail, &firstSeen, &lastSeen, &dimmUID, &tie)
	switch {
	case err == nil && sum == r.SHA256 && state == r.State && detail == r.Detail:
		_, err := w.exec(`UPDATE spd_reading SET last_seen = MAX(last_seen, ?), first_seen = MIN(first_seen, ?),
 scan_id = CASE WHEN ? >= last_seen THEN COALESCE(?, scan_id) ELSE scan_id END WHERE id = ?`,
			ts(r.LastSeen), ts(r.FirstSeen), ts(r.LastSeen), scan, id)
		if err != nil {
			return false, fmt.Errorf("spd reading %s: %w", r.Origin, err)
		}
		r.ID, r.DIMMUID, r.Tie = id, dimmUID, tie
		return false, nil
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return false, fmt.Errorf("spd reading %s: %w", r.Origin, err)
	}
	var raw any
	if r.Raw != nil {
		raw = r.Raw
	}
	res, err := w.exec(`INSERT INTO spd_reading (host, scan_id, origin, bus, address, driver, form, state, detail, raw, fields, sha256, first_seen, last_seen, dimm_uid, tie)
 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Host, scan, r.Origin, r.Bus, r.Address, r.Driver, r.Form, r.State, r.Detail, raw, r.Fields, r.SHA256,
		ts(r.FirstSeen), ts(r.LastSeen), r.DIMMUID, r.Tie)
	if err != nil {
		return false, fmt.Errorf("spd reading %s: %w", r.Origin, err)
	}
	r.ID, _ = res.LastInsertId()
	return true, nil
}

// TieSPD sets the DIMM of an SPD reading and the evidence; an empty DIMM
// unties it.
func (w *Writer) TieSPD(id int64, dimmUID, tie string) error {
	if dimmUID == "" {
		tie = ""
	}
	_, err := w.exec(`UPDATE spd_reading SET dimm_uid = ?, tie = ? WHERE id = ?`, dimmUID, tie, id)
	return err
}

const spdColumns = `id, host, COALESCE(scan_id, 0), origin, bus, address, driver, form, state, detail, raw, fields, sha256, first_seen, last_seen, dimm_uid, tie`

func scanSPD(rows *sql.Rows) ([]model.SPDReading, error) {
	defer rows.Close()
	var out []model.SPDReading
	for rows.Next() {
		var r model.SPDReading
		var first, last sql.NullString
		if err := rows.Scan(&r.ID, &r.Host, &r.ScanID, &r.Origin, &r.Bus, &r.Address, &r.Driver, &r.Form, &r.State, &r.Detail,
			&r.Raw, &r.Fields, &r.SHA256, &first, &last, &r.DIMMUID, &r.Tie); err != nil {
			return nil, err
		}
		r.FirstSeen, r.LastSeen = parseTS(first), parseTS(last)
		out = append(out, r)
	}
	return out, rows.Err()
}

// SPDReadings returns every SPD reading of a host, by origin and time.
func (s *Store) SPDReadings(ctx context.Context, host string) ([]model.SPDReading, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+spdColumns+` FROM spd_reading WHERE host = ? ORDER BY origin, last_seen, id`, host)
	if err != nil {
		return nil, err
	}
	return scanSPD(rows)
}

// SPDReadings returns every SPD reading of a host inside a write, for the
// join.
func (w *Writer) SPDReadings(host string) ([]model.SPDReading, error) {
	rows, err := w.tx.QueryContext(w.ctx, `SELECT `+spdColumns+` FROM spd_reading WHERE host = ? ORDER BY origin, last_seen, id`, host)
	if err != nil {
		return nil, err
	}
	return scanSPD(rows)
}

// SetDIMMFromSPD writes what a tied SPD reading says into the row of a
// DIMM: the manufacturing week and the device width replace what the row
// holds (no inventory has the first, and the second comes to EDAC and a
// BMC from the same EEPROM by way of firmware); the other fields of fill
// are written only where the row is empty. An empty date or an unset
// width leaves the column as it is.
func (w *Writer) SetDIMMFromSPD(uid, mfgDate string, width model.Opt[int], fill model.DIMM) error {
	if uid == "" {
		return invalid("dimm: uid is empty")
	}
	var size any
	if fill.SizeBytes > 0 {
		size = fill.SizeBytes
	}
	_, err := w.exec(`UPDATE dimm SET
  mfg_date        = CASE WHEN ? != '' THEN ? ELSE mfg_date END,
  device_width    = COALESCE(?, device_width),
  size_bytes      = COALESCE(size_bytes, ?),
  memory_type     = CASE WHEN memory_type = '' THEN ? ELSE memory_type END,
  module_type     = CASE WHEN module_type = '' THEN ? ELSE module_type END,
  ranks           = COALESCE(ranks, ?),
  speed_mts       = COALESCE(speed_mts, ?),
  manufacturer_id = CASE WHEN manufacturer_id = '' THEN ? ELSE manufacturer_id END
 WHERE dimm_uid = ?`,
		mfgDate, mfgDate, optInt(width), size, fill.MemoryType, fill.ModuleType, optInt(fill.Ranks), optInt(fill.SpeedMTs), fill.ManufacturerID, uid)
	if err != nil {
		return fmt.Errorf("dimm %s: %w", uid, err)
	}
	return nil
}
