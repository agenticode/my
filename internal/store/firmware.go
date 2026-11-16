package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/agenticode/my/internal/model"
)

// Firmware records: what the BIOS and the BMC said about memory, kept in
// the vendor's words beside the event or action made from it (fw_record),
// the records a reader could not use (unread_record), and the message
// registries a BMC published (registry_seen).

// cut shortens s to at most n bytes without splitting a UTF-8 sequence and
// marks the cut.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	const mark = "..."
	end := n - len(mark)
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + mark
}

// commonName reports whether s has the form of a common event name:
// lower-case words joined by dashes.
func commonName(s string) bool {
	if s == "" || len(s) > 40 || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < 'a' || c > 'z') && c != '-' {
			return false
		}
	}
	return true
}

const fwColumns = `host, scan_id, event_id, action_id, at, common, flags, clears, vendor, vendor_id, registry, registry_version,
 vendor_severity, origin, log, location, args, text, repeat, last_at, resolved, entry_key, mapping, map_ref, basis,
 source, raw_ref, fingerprint, raw`

const fwColumnCount = 29

// insertFirmware writes the fw_record row of fw. eventID or actionID ties
// it to its record; with neither, fw must carry a fingerprint and its raw
// text, and a row with the same fingerprint is not added again.
func (w *Writer) insertFirmware(fw *model.FirmwareRecord, eventID, actionID int64) (inserted bool, err error) {
	if fw.Repeat == 0 {
		fw.Repeat = 1
	}
	if fw.Mapping == "" {
		fw.Mapping = model.MappingNone
	}
	switch {
	case fw.Host == "":
		return false, invalid("firmware record: host is empty")
	case !commonName(fw.Common):
		return false, invalid("firmware record: %q is not a common event", fw.Common)
	case fw.Clears != "" && !commonName(fw.Clears):
		return false, invalid("firmware record: %q is not a common event", fw.Clears)
	case !validTime(fw.At):
		return false, invalid("firmware record: time %v is out of range", fw.At)
	case fw.Repeat < 1:
		return false, invalid("firmware record: repeat %d is not positive", fw.Repeat)
	case eventID == 0 && actionID == 0 && fw.Fingerprint == "":
		return false, invalid("firmware record: a record without an event or action needs a fingerprint")
	}
	args := fw.Args
	if args == nil {
		args = []string{}
	}
	ab, err := json.Marshal(args)
	if err != nil {
		return false, err
	}
	fw.Text = cut(fw.Text, model.MaxFirmwareText)
	var scanID, evID, actID, fp any
	if fw.ScanID != 0 {
		scanID = fw.ScanID
	}
	if eventID != 0 {
		evID = eventID
	}
	if actionID != 0 {
		actID = actionID
	}
	if eventID == 0 && actionID == 0 {
		fp = fw.Fingerprint
	}
	res, err := w.exec(`INSERT INTO fw_record (`+fwColumns+`) VALUES (`+placeholders(fwColumnCount)+`) ON CONFLICT(fingerprint) DO NOTHING`,
		fw.Host, scanID, evID, actID, ts(fw.At), fw.Common, strings.Join(fw.Flags, ","), fw.Clears, fw.Vendor, fw.VendorID,
		fw.Registry, fw.RegistryVersion, fw.VendorSeverity, fw.Origin, fw.Log, fw.Location, string(ab), fw.Text, fw.Repeat,
		ts(fw.LastAt), boolInt(fw.Resolved), fw.EntryKey, fw.Mapping, fw.MapRef, fw.Basis, fw.Source, fw.RawRef, fp, fw.Raw)
	if err != nil {
		return false, fmt.Errorf("firmware record %s: %w", fw.RawRef, err)
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	fw.EventID, fw.ActionID = eventID, actionID
	fw.ID, err = res.LastInsertId()
	return err == nil, err
}

// InsertFirmware adds a firmware record that makes neither an event nor an
// action: a module detected, a configuration in effect. It needs a
// fingerprint; a record whose fingerprint is present is not added again.
func (w *Writer) InsertFirmware(fw *model.FirmwareRecord) (inserted bool, err error) {
	if fw.Fingerprint == "" {
		return false, invalid("firmware record: fingerprint is empty")
	}
	return w.insertFirmware(fw, 0, 0)
}

// InsertUnread keeps records a reader of the source could not use. At most
// model.MaxUnreadKept are kept per source, each text cut at
// model.MaxUnreadText bytes.
func (w *Writer) InsertUnread(sourceID int64, recs []model.UnreadRecord) error {
	if sourceID == 0 {
		return invalid("unread record: source is required")
	}
	var have int
	if err := w.queryRow(`SELECT count(*) FROM unread_record WHERE source_id = ?`, sourceID).Scan(&have); err != nil {
		return err
	}
	for i := range recs {
		if have >= model.MaxUnreadKept {
			break
		}
		r := &recs[i]
		res, err := w.exec(`INSERT INTO unread_record (source_id, ref, reason, text) VALUES (?, ?, ?, ?)`,
			sourceID, cut(r.Ref, 512), cut(r.Reason, 512), cut(r.Text, model.MaxUnreadText))
		if err != nil {
			return fmt.Errorf("unread record %s: %w", r.Ref, err)
		}
		r.SourceID = sourceID
		if r.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		have++
	}
	return nil
}

// PutRegistrySeen records a message registry a BMC of the host published.
// The row of the same prefix, version and language is replaced: the
// latest reading says how much of it this version classes.
func (w *Writer) PutRegistrySeen(r *model.RegistrySeen) error {
	switch {
	case r.Host == "":
		return invalid("registry: host is empty")
	case strings.TrimSpace(r.Prefix) == "":
		return invalid("registry: prefix is empty")
	case !validTime(r.SeenAt):
		return invalid("registry: time %v is out of range", r.SeenAt)
	case r.Messages < 0 || r.Memory < 0 || r.Mapped < 0 || r.Wording < 0:
		return invalid("registry %s: a count is negative", r.Prefix)
	}
	unmapped := r.Unmapped
	if len(unmapped) > model.MaxRegistryUnmapped {
		unmapped = unmapped[:model.MaxRegistryUnmapped]
	}
	if unmapped == nil {
		unmapped = []string{}
	}
	ub, err := json.Marshal(unmapped)
	if err != nil {
		return err
	}
	var scanID any
	if r.ScanID != 0 {
		scanID = r.ScanID
	}
	_, err = w.exec(`INSERT INTO registry_seen (host, scan_id, seen_at, prefix, version, owner, language, messages, memory, mapped,
 wording, unmapped, source) VALUES (`+placeholders(13)+`)
 ON CONFLICT(host, prefix, version, language) DO UPDATE SET scan_id = excluded.scan_id, seen_at = excluded.seen_at,
 owner = excluded.owner, messages = excluded.messages, memory = excluded.memory, mapped = excluded.mapped,
 wording = excluded.wording, unmapped = excluded.unmapped, source = excluded.source`,
		r.Host, scanID, ts(r.SeenAt), r.Prefix, r.Version, r.Owner, r.Language, r.Messages, r.Memory, r.Mapped, r.Wording,
		string(ub), r.Source)
	if err != nil {
		return fmt.Errorf("registry %s: %w", r.Prefix, err)
	}
	return nil
}

// FirmwareFilter selects firmware records. Zero fields do not restrict.
type FirmwareFilter struct {
	Host   string
	Common []string
	// Alone selects the records that have neither an event nor an action.
	Alone bool
}

// FirmwareRecords returns the firmware records selected by f in time order.
func (s *Store) FirmwareRecords(ctx context.Context, f FirmwareFilter) ([]model.FirmwareRecord, error) {
	var conds []string
	var args []any
	if f.Host != "" {
		conds, args = append(conds, `host = ?`), append(args, f.Host)
	}
	if len(f.Common) > 0 {
		conds = append(conds, `common IN (`+placeholders(len(f.Common))+`)`)
		for _, c := range f.Common {
			args = append(args, c)
		}
	}
	if f.Alone {
		conds = append(conds, `event_id IS NULL AND f.action_id IS NULL`)
	}
	cols := "f.id"
	for _, c := range strings.Split(fwColumns, ",") {
		cols += ", f." + strings.TrimSpace(c)
	}
	q := `SELECT ` + cols + `, COALESCE(e.dimm_uid, a.dimm_uid, ''), COALESCE(e.slot, a.slot, '') FROM fw_record f
 LEFT JOIN event e ON e.id = f.event_id LEFT JOIN action a ON a.id = f.action_id`
	for i, c := range conds {
		conds[i] = "f." + c
	}
	if len(conds) > 0 {
		q += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY f.at, f.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.FirmwareRecord
	for rows.Next() {
		var fw model.FirmwareRecord
		var scanID, evID, actID sql.NullInt64
		var at, flags, argsText string
		var lastAt, fp sql.NullString
		var resolved int64
		if err := rows.Scan(&fw.ID, &fw.Host, &scanID, &evID, &actID, &at, &fw.Common, &flags, &fw.Clears, &fw.Vendor, &fw.VendorID,
			&fw.Registry, &fw.RegistryVersion, &fw.VendorSeverity, &fw.Origin, &fw.Log, &fw.Location, &argsText, &fw.Text,
			&fw.Repeat, &lastAt, &resolved, &fw.EntryKey, &fw.Mapping, &fw.MapRef, &fw.Basis, &fw.Source, &fw.RawRef, &fp,
			&fw.Raw, &fw.DIMMUID, &fw.Slot); err != nil {
			return nil, err
		}
		fw.ScanID, fw.EventID, fw.ActionID = scanID.Int64, evID.Int64, actID.Int64
		fw.At, fw.LastAt = parseTS(sql.NullString{String: at, Valid: true}), parseTS(lastAt)
		fw.Resolved, fw.Fingerprint = resolved != 0, fp.String
		if flags != "" {
			fw.Flags = strings.Split(flags, ",")
		}
		if err := json.Unmarshal([]byte(argsText), &fw.Args); err != nil {
			return nil, fmt.Errorf("firmware record %d: args: %w", fw.ID, err)
		}
		if len(fw.Args) == 0 {
			fw.Args = nil
		}
		out = append(out, fw)
	}
	return out, rows.Err()
}

// UnreadRecords returns the unread records kept for the given sources, in
// the order they were read.
func (s *Store) UnreadRecords(ctx context.Context, sourceIDs ...int64) ([]model.UnreadRecord, error) {
	if len(sourceIDs) == 0 {
		return nil, nil
	}
	args := make([]any, len(sourceIDs))
	for i, id := range sourceIDs {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, source_id, ref, reason, text FROM unread_record WHERE source_id IN (`+
		placeholders(len(args))+`) ORDER BY source_id, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.UnreadRecord
	for rows.Next() {
		var r model.UnreadRecord
		if err := rows.Scan(&r.ID, &r.SourceID, &r.Ref, &r.Reason, &r.Text); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RegistriesSeen returns the message registries the BMCs of a host
// published, by prefix and version.
func (s *Store) RegistriesSeen(ctx context.Context, host string) ([]model.RegistrySeen, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, host, scan_id, seen_at, prefix, version, owner, language, messages, memory, mapped,
 wording, unmapped, source FROM registry_seen WHERE host = ? ORDER BY prefix, version, language`, host)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.RegistrySeen
	for rows.Next() {
		var r model.RegistrySeen
		var scanID sql.NullInt64
		var seen, unmapped string
		if err := rows.Scan(&r.ID, &r.Host, &scanID, &seen, &r.Prefix, &r.Version, &r.Owner, &r.Language, &r.Messages, &r.Memory,
			&r.Mapped, &r.Wording, &unmapped, &r.Source); err != nil {
			return nil, err
		}
		r.ScanID, r.SeenAt = scanID.Int64, parseTS(sql.NullString{String: seen, Valid: true})
		if err := json.Unmarshal([]byte(unmapped), &r.Unmapped); err != nil {
			return nil, fmt.Errorf("registry %s: unmapped: %w", r.Prefix, err)
		}
		if len(r.Unmapped) == 0 {
			r.Unmapped = nil
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// firmwareOf copies the identity of a record onto the firmware record that
// goes with it.
func firmwareOf(fw *model.FirmwareRecord, host string, scanID int64, at time.Time, source, rawRef string) {
	fw.Host, fw.ScanID, fw.At = host, scanID, at
	if fw.Source == "" {
		fw.Source = source
	}
	if fw.RawRef == "" {
		fw.RawRef = rawRef
	}
}
