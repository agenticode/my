package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/agenticode/my/internal/model"
)

// What a BMC says about a host beyond events, actions and the inventory
// (schema version 6): the readings of the BMC itself, the settings of the
// system firmware, and the memory modules as the BMC describes them.

const bmcColumns = `host, observed_at, address, manager, firmware, bios_version, redfish_version, bios_registry,
 live, complete, tls, auth, clock_offset, last_reset, classes, logs, notes, source, raw_ref, fingerprint, scan_id`

const bmcColumnCount = 21

const biosSettingColumns = `host, observed_at, name, value, has_value, pending, has_pending, value_type, registry,
 display_name, menu_path, read_only, reset_required, allowed, lower_bound, upper_bound, default_value, has_default,
 source, raw_ref, scan_id`

const biosSettingColumnCount = 21

const bmcMemoryColumns = `host, observed_at, label, slot, device_locator, service_label, resource_id, state, health,
 error_correction, operating_speed, allowed_speeds, rank_spare, spare_device, device_width, device_width_source,
 metrics, ce_current, ue_current, ce_lifetime, ue_lifetime, alarm_ce, alarm_ue, current_since,
 source, raw_ref, raw, fingerprint, scan_id`

const bmcMemoryColumnCount = 29

// listText encodes a list for a JSON column. An empty list is "[]".
func listText(v any, empty bool) (string, error) {
	if empty {
		return "[]", nil
	}
	return jsonText(v)
}

// readList decodes a JSON column that holds a list. An empty list, and a
// column that does not hold a list, are read as no list: the rest of the
// row is still what the BMC said.
func readList[T any](text string, into *[]T) {
	var list []T
	if json.Unmarshal([]byte(text), &list) == nil && len(list) > 0 {
		*into = list
	}
}

func scanRef(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func toOptInt64(n sql.NullInt64) model.Opt[int64] {
	if !n.Valid {
		return model.Opt[int64]{}
	}
	return model.Some(n.Int64)
}

// InsertBMC stores a reading of a management controller. A reading whose
// fingerprint is already present is not added again; inserted is then
// false and b.ID is set to the existing row.
func (w *Writer) InsertBMC(b *model.BMC) (inserted bool, err error) {
	switch {
	case b.Host == "":
		return false, invalid("bmc: host is empty")
	case b.Fingerprint == "" || b.Source == "":
		return false, invalid("bmc: source and fingerprint are required")
	case !validTime(b.ObservedAt):
		return false, invalid("bmc: time %v is out of range", b.ObservedAt)
	case !b.LastReset.IsZero() && !validTime(b.LastReset):
		return false, invalid("bmc: reset time %v is out of range", b.LastReset)
	}
	classes, err := listText(b.Classes, len(b.Classes) == 0)
	if err != nil {
		return false, err
	}
	logs, err := listText(b.Logs, len(b.Logs) == 0)
	if err != nil {
		return false, err
	}
	notes, err := listText(b.Notes, len(b.Notes) == 0)
	if err != nil {
		return false, err
	}
	res, err := w.exec(`INSERT INTO bmc (`+bmcColumns+`) VALUES (`+placeholders(bmcColumnCount)+`) ON CONFLICT(fingerprint) DO NOTHING`,
		b.Host, ts(b.ObservedAt), b.Address, b.Manager, b.Firmware, b.BIOSVersion, b.RedfishVersion, b.BIOSRegistry,
		boolInt(b.Live), optBool(b.Complete), b.TLS, b.Auth, optInt64(b.ClockOffset), ts(b.LastReset), classes, logs, notes,
		b.Source, b.RawRef, b.Fingerprint, scanRef(b.ScanID))
	if err != nil {
		return false, fmt.Errorf("bmc %s: %w", b.RawRef, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		err := w.queryRow(`SELECT id FROM bmc WHERE fingerprint = ?`, b.Fingerprint).Scan(&b.ID)
		return false, err
	}
	b.ID, err = res.LastInsertId()
	return err == nil, err
}

// PutBIOSSetting stores a setting of the system firmware. A setting of the
// same host, time and name replaces the one that is there: the same
// reading read again says the same.
func (w *Writer) PutBIOSSetting(s *model.BIOSSetting) error {
	switch {
	case s.Host == "":
		return invalid("bios setting: host is empty")
	case s.Name == "" || s.Source == "":
		return invalid("bios setting: name and source are required")
	case !validTime(s.ObservedAt):
		return invalid("bios setting: time %v is out of range", s.ObservedAt)
	case len(s.Name) > 512 || len(s.Value) > 4096 || len(s.Pending) > 4096:
		return invalid("bios setting %s: name or value is longer than a setting", s.Name[:min(len(s.Name), 64)])
	}
	allowed, err := listText(s.Allowed, len(s.Allowed) == 0)
	if err != nil {
		return err
	}
	_, err = w.exec(`INSERT INTO bios_setting (`+biosSettingColumns+`) VALUES (`+placeholders(biosSettingColumnCount)+`)
 ON CONFLICT(host, observed_at, name) DO UPDATE SET value = excluded.value, has_value = excluded.has_value,
 pending = excluded.pending, has_pending = excluded.has_pending, value_type = excluded.value_type,
 registry = excluded.registry, display_name = excluded.display_name, menu_path = excluded.menu_path,
 read_only = excluded.read_only, reset_required = excluded.reset_required, allowed = excluded.allowed,
 lower_bound = excluded.lower_bound, upper_bound = excluded.upper_bound, default_value = excluded.default_value,
 has_default = excluded.has_default, source = excluded.source, raw_ref = excluded.raw_ref, scan_id = excluded.scan_id`,
		s.Host, ts(s.ObservedAt), s.Name, s.Value, boolInt(s.HasValue), s.Pending, boolInt(s.HasPending), s.Type, s.Registry,
		s.DisplayName, s.MenuPath, optBool(s.ReadOnly), optBool(s.ResetRequired), allowed, optInt64(s.LowerBound), optInt64(s.UpperBound),
		s.Default, boolInt(s.HasDefault), s.Source, s.RawRef, scanRef(s.ScanID))
	if err != nil {
		return fmt.Errorf("bios setting %s: %w", s.Name, err)
	}
	return nil
}

// InsertBMCMemory stores a memory module as a BMC describes it. A record
// whose fingerprint is already present is not added again; inserted is
// then false and m.ID is set to the existing row.
func (w *Writer) InsertBMCMemory(m *model.BMCMemory) (inserted bool, err error) {
	switch {
	case m.Host == "":
		return false, invalid("bmc memory: host is empty")
	case m.Fingerprint == "" || m.Source == "":
		return false, invalid("bmc memory: source and fingerprint are required")
	case m.Label == "":
		return false, invalid("bmc memory: the module names no slot")
	case !validTime(m.ObservedAt):
		return false, invalid("bmc memory: time %v is out of range", m.ObservedAt)
	case !m.CurrentSince.IsZero() && !validTime(m.CurrentSince):
		return false, invalid("bmc memory: time %v is out of range", m.CurrentSince)
	}
	for _, c := range []model.Opt[int64]{m.CECurrent, m.UECurrent, m.CELifetime, m.UELifetime} {
		if c.Valid && c.V < 0 {
			return false, invalid("bmc memory %s: a count below zero", m.Label)
		}
	}
	speeds, err := listText(m.AllowedSpeeds, len(m.AllowedSpeeds) == 0)
	if err != nil {
		return false, err
	}
	res, err := w.exec(`INSERT INTO bmc_memory (`+bmcMemoryColumns+`) VALUES (`+placeholders(bmcMemoryColumnCount)+`) ON CONFLICT(fingerprint) DO NOTHING`,
		m.Host, ts(m.ObservedAt), m.Label, m.Slot, m.DeviceLocator, m.ServiceLabel, m.ResourceID, m.State, m.Health,
		m.ErrorCorrection, optInt(m.OperatingSpeed), speeds, optBool(m.RankSpare), optBool(m.SpareDevice), optInt(m.DeviceWidth), m.DeviceWidthSource,
		m.Metrics, optInt64(m.CECurrent), optInt64(m.UECurrent), optInt64(m.CELifetime), optInt64(m.UELifetime),
		optBool(m.AlarmCE), optBool(m.AlarmUE), ts(m.CurrentSince), m.Source, m.RawRef, m.Raw, m.Fingerprint, scanRef(m.ScanID))
	if err != nil {
		return false, fmt.Errorf("bmc memory %s: %w", m.RawRef, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		err := w.queryRow(`SELECT id FROM bmc_memory WHERE fingerprint = ?`, m.Fingerprint).Scan(&m.ID)
		return false, err
	}
	m.ID, err = res.LastInsertId()
	return err == nil, err
}

// SetBMCMemorySlot ties the BMC's records of a module to a slot of the
// ledger: every record of the host whose source calls the slot label.
func (w *Writer) SetBMCMemorySlot(host, label, slot string) error {
	_, err := w.exec(`UPDATE bmc_memory SET slot = ? WHERE host = ? AND label = ?`, slot, host, label)
	return err
}

// BMCMemoryLabels returns the names the BMC records of a host use for
// slots, in order.
func (s *Store) BMCMemoryLabels(ctx context.Context, host string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT label FROM bmc_memory WHERE host = ? ORDER BY label`, host)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// BMCs returns the readings of management controllers in time order. An
// empty host does not restrict.
func (s *Store) BMCs(ctx context.Context, host string) ([]model.BMC, error) {
	q := `SELECT id, ` + bmcColumns + ` FROM bmc`
	var args []any
	if host != "" {
		q, args = q+` WHERE host = ?`, append(args, host)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY observed_at, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.BMC
	for rows.Next() {
		var b model.BMC
		var observed, reset sql.NullString
		var live int64
		var complete, offset, scanID sql.NullInt64
		var classes, logs, notes string
		if err := rows.Scan(&b.ID, &b.Host, &observed, &b.Address, &b.Manager, &b.Firmware, &b.BIOSVersion, &b.RedfishVersion, &b.BIOSRegistry,
			&live, &complete, &b.TLS, &b.Auth, &offset, &reset, &classes, &logs, &notes, &b.Source, &b.RawRef, &b.Fingerprint, &scanID); err != nil {
			return nil, err
		}
		b.ObservedAt, b.LastReset = parseTS(observed), parseTS(reset)
		b.Live, b.Complete, b.ClockOffset, b.ScanID = live != 0, toOptBool(complete), toOptInt64(offset), scanID.Int64
		readList(classes, &b.Classes)
		readList(logs, &b.Logs)
		readList(notes, &b.Notes)
		out = append(out, b)
	}
	return out, rows.Err()
}

// BIOSSettings returns the settings of the system firmware of a host, in
// the order of time and name. An empty host does not restrict.
func (s *Store) BIOSSettings(ctx context.Context, host string) ([]model.BIOSSetting, error) {
	q := `SELECT id, ` + biosSettingColumns + ` FROM bios_setting`
	var args []any
	if host != "" {
		q, args = q+` WHERE host = ?`, append(args, host)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY host, observed_at, name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.BIOSSetting
	for rows.Next() {
		var b model.BIOSSetting
		var observed sql.NullString
		var hasValue, hasPending, hasDefault int64
		var readOnly, reset, lower, upper, scanID sql.NullInt64
		var allowed string
		if err := rows.Scan(&b.ID, &b.Host, &observed, &b.Name, &b.Value, &hasValue, &b.Pending, &hasPending, &b.Type, &b.Registry,
			&b.DisplayName, &b.MenuPath, &readOnly, &reset, &allowed, &lower, &upper, &b.Default, &hasDefault,
			&b.Source, &b.RawRef, &scanID); err != nil {
			return nil, err
		}
		b.ObservedAt = parseTS(observed)
		b.HasValue, b.HasPending, b.HasDefault = hasValue != 0, hasPending != 0, hasDefault != 0
		b.ReadOnly, b.ResetRequired = toOptBool(readOnly), toOptBool(reset)
		b.LowerBound, b.UpperBound, b.ScanID = toOptInt64(lower), toOptInt64(upper), scanID.Int64
		readList(allowed, &b.Allowed)
		out = append(out, b)
	}
	return out, rows.Err()
}

// BMCMemory returns the memory modules as BMCs describe them, in the
// order of time and slot. An empty host does not restrict.
func (s *Store) BMCMemory(ctx context.Context, host string) ([]model.BMCMemory, error) {
	q := `SELECT id, ` + bmcMemoryColumns + ` FROM bmc_memory`
	var args []any
	if host != "" {
		q, args = q+` WHERE host = ?`, append(args, host)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY host, observed_at, label, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.BMCMemory
	for rows.Next() {
		var m model.BMCMemory
		var observed, since sql.NullString
		var speed, rankSpare, spareDevice, width, ceCur, ueCur, ceLife, ueLife, alarmCE, alarmUE, scanID sql.NullInt64
		var speeds string
		if err := rows.Scan(&m.ID, &m.Host, &observed, &m.Label, &m.Slot, &m.DeviceLocator, &m.ServiceLabel, &m.ResourceID, &m.State, &m.Health,
			&m.ErrorCorrection, &speed, &speeds, &rankSpare, &spareDevice, &width, &m.DeviceWidthSource,
			&m.Metrics, &ceCur, &ueCur, &ceLife, &ueLife, &alarmCE, &alarmUE, &since,
			&m.Source, &m.RawRef, &m.Raw, &m.Fingerprint, &scanID); err != nil {
			return nil, err
		}
		m.ObservedAt, m.CurrentSince = parseTS(observed), parseTS(since)
		m.OperatingSpeed, m.DeviceWidth = toOptInt(speed), toOptInt(width)
		m.RankSpare, m.SpareDevice = toOptBool(rankSpare), toOptBool(spareDevice)
		m.CECurrent, m.UECurrent, m.CELifetime, m.UELifetime = toOptInt64(ceCur), toOptInt64(ueCur), toOptInt64(ceLife), toOptInt64(ueLife)
		m.AlarmCE, m.AlarmUE, m.ScanID = toOptBool(alarmCE), toOptBool(alarmUE), scanID.Int64
		readList(speeds, &m.AllowedSpeeds)
		out = append(out, m)
	}
	return out, rows.Err()
}
