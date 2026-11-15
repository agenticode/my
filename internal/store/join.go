package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/agenticode/my/internal/model"
)

// Two inventories of one board (the SMBIOS tables of the host and the memory
// resources of its BMC) can record one DIMM under two identities and one
// slot under two names. The functions here make one of the two. Deciding
// that two records mean the same thing is left to the caller.

// PlacedDIMMs returns the DIMMs that have a placement on a host, by
// identity.
func (s *Store) PlacedDIMMs(ctx context.Context, host string) (map[string]model.DIMM, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+dimmColumns+` FROM dimm
 WHERE dimm_uid IN (SELECT dimm_uid FROM placement WHERE host = ?)`, host)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]model.DIMM{}
	for rows.Next() {
		d, err := scanDIMM(rows)
		if err != nil {
			return nil, err
		}
		out[d.UID] = *d
	}
	return out, rows.Err()
}

// placementsWhere reads placements inside the transaction.
func (w *Writer) placementsWhere(cond string, args ...any) ([]model.Placement, error) {
	rows, err := w.tx.QueryContext(w.ctx, `SELECT id, dimm_uid, host, slot, first_seen, last_seen, source FROM placement WHERE `+
		cond+` ORDER BY first_seen, id`, args...)
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

// ReplacePlacement removes a placement and records its first and its last
// sighting again, as sightings of the given DIMM in the given slot. They
// extend a placement of that DIMM in that slot when there is one.
func (w *Writer) ReplacePlacement(p model.Placement, dimmUID, slot string) error {
	if _, err := w.exec(`DELETE FROM placement WHERE id = ?`, p.ID); err != nil {
		return err
	}
	if err := w.ObservePlacement(dimmUID, p.Host, slot, p.FirstSeen, p.Source); err != nil {
		return err
	}
	if p.LastSeen.After(p.FirstSeen) {
		return w.ObservePlacement(dimmUID, p.Host, slot, p.LastSeen, p.Source)
	}
	return nil
}

// AdoptDIMM makes what a host recorded under one DIMM identity records of
// another: the placements, and the DIMM named in events, actions, ECS
// readings, faults and findings. The first identity is removed when no
// record of any host refers to it any more.
func (w *Writer) AdoptDIMM(host, from, to string) error {
	if host == "" || from == "" || to == "" || from == to {
		return invalid("adopt dimm: host and two different identities are required")
	}
	placements, err := w.placementsWhere(`host = ? AND dimm_uid = ?`, host, from)
	if err != nil {
		return fmt.Errorf("adopt dimm %s: %w", from, err)
	}
	for _, p := range placements {
		if err := w.ReplacePlacement(p, to, p.Slot); err != nil {
			return fmt.Errorf("adopt dimm %s: %w", from, err)
		}
	}
	for _, table := range []string{"event", "action", "ecs_sample", "fault", "verdict"} {
		if _, err := w.exec(`UPDATE `+table+` SET dimm_uid = ? WHERE host = ? AND dimm_uid = ?`, to, host, from); err != nil {
			return fmt.Errorf("adopt dimm %s: %s: %w", from, table, err)
		}
	}
	return w.dropUnusedDIMM(from)
}

// dropUnusedDIMM removes a DIMM identity no record refers to.
func (w *Writer) dropUnusedDIMM(uid string) error {
	_, err := w.exec(`DELETE FROM dimm WHERE dimm_uid = ?
 AND NOT EXISTS (SELECT 1 FROM placement p WHERE p.dimm_uid = dimm.dimm_uid)
 AND NOT EXISTS (SELECT 1 FROM event e WHERE e.dimm_uid = dimm.dimm_uid)
 AND NOT EXISTS (SELECT 1 FROM action x WHERE x.dimm_uid = dimm.dimm_uid)
 AND NOT EXISTS (SELECT 1 FROM ecs_sample c WHERE c.dimm_uid = dimm.dimm_uid)
 AND NOT EXISTS (SELECT 1 FROM fault f WHERE f.dimm_uid = dimm.dimm_uid)
 AND NOT EXISTS (SELECT 1 FROM verdict v WHERE v.dimm_uid = dimm.dimm_uid)`, uid)
	if err != nil {
		return fmt.Errorf("drop dimm %s: %w", uid, err)
	}
	return nil
}

// MoveSlot makes what a host recorded for one slot records of another slot
// and removes the first: its names, its placements, and the slot named in
// events, actions, ECS readings, faults, findings and BMC readings. The
// second slot has to exist.
func (w *Writer) MoveSlot(host, from, to string) error {
	if host == "" || from == "" || to == "" || from == to {
		return invalid("move slot: host and two different slots are required")
	}
	var n int
	if err := w.queryRow(`SELECT COUNT(*) FROM slot WHERE host = ? AND name = ?`, host, to).Scan(&n); err != nil {
		return fmt.Errorf("move slot %s: %w", from, err)
	}
	if n == 0 {
		return invalid("move slot: %s has no slot %q", host, to)
	}
	placements, err := w.placementsWhere(`host = ? AND slot = ?`, host, from)
	if err != nil {
		return fmt.Errorf("move slot %s: %w", from, err)
	}
	for _, p := range placements {
		if err := w.ReplacePlacement(p, p.DIMMUID, to); err != nil {
			return fmt.Errorf("move slot %s: %w", from, err)
		}
	}
	for _, table := range []string{"slot_alias", "event", "action", "ecs_sample", "fault", "verdict", "bmc_memory"} {
		if _, err := w.exec(`UPDATE `+table+` SET slot = ? WHERE host = ? AND slot = ?`, to, host, from); err != nil {
			return fmt.Errorf("move slot %s: %s: %w", from, table, err)
		}
	}
	if _, err := w.exec(`DELETE FROM slot WHERE host = ? AND name = ?`, host, from); err != nil {
		return fmt.Errorf("move slot %s: %w", from, err)
	}
	return nil
}

// DropSlot removes a slot nothing but its own names refers to: no
// placement, event, action, ECS reading or BMC reading. It reports whether
// the slot was removed.
func (w *Writer) DropSlot(host, name string) (bool, error) {
	res, err := w.exec(`DELETE FROM slot WHERE host = ? AND name = ?
 AND NOT EXISTS (SELECT 1 FROM placement p WHERE p.host = slot.host AND p.slot = slot.name)
 AND NOT EXISTS (SELECT 1 FROM event e WHERE e.host = slot.host AND e.slot = slot.name)
 AND NOT EXISTS (SELECT 1 FROM action x WHERE x.host = slot.host AND x.slot = slot.name)
 AND NOT EXISTS (SELECT 1 FROM ecs_sample c WHERE c.host = slot.host AND c.slot = slot.name)
 AND NOT EXISTS (SELECT 1 FROM bmc_memory b WHERE b.host = slot.host AND b.slot = slot.name)`, host, name)
	if err != nil {
		return false, fmt.Errorf("drop slot %s: %w", name, err)
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	if _, err := w.exec(`DELETE FROM slot_alias WHERE host = ? AND slot = ?`, host, name); err != nil {
		return false, fmt.Errorf("drop slot %s: %w", name, err)
	}
	return true, nil
}
