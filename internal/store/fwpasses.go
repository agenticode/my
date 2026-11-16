package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/agenticode/my/internal/model"
)

// The passes over firmware records (design V1, section 7). Each computes
// its result from the rows on every run, so a ledger read again, or with
// its sources in another order, comes out the same.

// FirmwareWindow is how far apart two records of a BMC are stamped and
// still report one error: the burst of one log, and the copies of one
// error in two logs. The firmware writes them while it handles the error.
const FirmwareWindow = 5 * time.Second

// fwRow is an event or an action with its firmware record.
type fwRow struct {
	id          int64
	at          time.Time
	quality     string
	class       string // CE, UE, or "" for informational events
	kind        string // the action kind
	result      string
	slot, label string
	source      string
	grade       string
	place       int // how much of the place the record names
	fingerprint string
	common      string
	clears      string
	log         string
	mapping     string
	entryKey    string
	repeat      int
	count       int
}

// where names the module of a row: its slot, else its label.
func (r *fwRow) where() string {
	if r.slot != "" {
		return "s:" + r.slot
	}
	if r.label != "" {
		return "l:" + model.NormalizeLabel(r.label)
	}
	return ""
}

func placeScore(vals ...sql.NullInt64) int {
	n := 0
	for _, v := range vals {
		if v.Valid {
			n++
		}
	}
	return n
}

// firmwareEvents returns the events of a host that came with a firmware
// record, in time order.
func (w *Writer) firmwareEvents(host string) ([]*fwRow, error) {
	rows, err := w.tx.QueryContext(w.ctx, `SELECT e.id, e."ErrorDT", e.time_quality, e."ErrorSeverity", e.slot, e.label, e.source, e.grade,
 e."SocketId", e."ChannelId", e."ChannelSlotId", e."Rank", e."DeviceId", e."Bank", e."Row", e."Column", e."OsPhysicalAddress",
 e.fingerprint, e.count, f.common, f.clears, f.log, f.mapping, f.entry_key, f.repeat
 FROM event e JOIN fw_record f ON f.event_id = e.id WHERE e.host = ? AND e.kind = 'error' ORDER BY e."ErrorDT", e.id`, host)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*fwRow
	for rows.Next() {
		r := &fwRow{}
		var at, sev string
		var s, ch, pos, rank, dev, bank, row, col, addr sql.NullInt64
		if err := rows.Scan(&r.id, &at, &r.quality, &sev, &r.slot, &r.label, &r.source, &r.grade, &s, &ch, &pos, &rank, &dev, &bank,
			&row, &col, &addr, &r.fingerprint, &r.count, &r.common, &r.clears, &r.log, &r.mapping, &r.entryKey, &r.repeat); err != nil {
			return nil, err
		}
		r.at = parseTS(sql.NullString{String: at, Valid: true})
		r.class = model.Severity(sev).Class()
		r.place = placeScore(s, ch, pos, rank, dev, bank, row, col, addr)
		out = append(out, r)
	}
	return out, rows.Err()
}

// firmwareActions returns the actions of a host that came with a firmware
// record, in time order.
func (w *Writer) firmwareActions(host string) ([]*fwRow, error) {
	rows, err := w.tx.QueryContext(w.ctx, `SELECT a.id, a.at, a.time_quality, a.kind, a.result, a.slot, a.label, a.source,
 a."Rank", a."DeviceId", a."BankGroup", a."Bank", a."Row", a."OsPhysicalAddress", a.fingerprint,
 f.common, f.log, f.mapping, f.entry_key, f.repeat
 FROM action a JOIN fw_record f ON f.action_id = a.id WHERE a.host = ? ORDER BY a.at, a.id`, host)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*fwRow
	for rows.Next() {
		r := &fwRow{}
		var at string
		var rank, dev, bg, bank, row, addr sql.NullInt64
		if err := rows.Scan(&r.id, &at, &r.quality, &r.kind, &r.result, &r.slot, &r.label, &r.source, &rank, &dev, &bg, &bank, &row, &addr,
			&r.fingerprint, &r.common, &r.log, &r.mapping, &r.entryKey, &r.repeat); err != nil {
			return nil, err
		}
		r.at = parseTS(sql.NullString{String: at, Valid: true})
		r.place = placeScore(rank, dev, bg, bank, row, addr)
		out = append(out, r)
	}
	return out, rows.Err()
}

// FirmwareRepeats gives each reading of an entry that folds repeats the
// count of the occurrences it added: an entry read with 3 repeats and
// later with 5 counts 3 and 2, so the entry counts 5 however often it was
// read (section 7.3).
func (s *Store) FirmwareRepeats(ctx context.Context, host string) error {
	return s.Update(ctx, func(w *Writer) error {
		rows, err := w.firmwareEvents(host)
		if err != nil {
			return err
		}
		byKey := map[string][]*fwRow{}
		for _, r := range rows {
			if r.entryKey != "" && r.class != "" {
				byKey[r.entryKey] = append(byKey[r.entryKey], r)
			}
		}
		for _, list := range byKey {
			sort.SliceStable(list, func(i, j int) bool {
				if list[i].repeat != list[j].repeat {
					return list[i].repeat < list[j].repeat
				}
				return list[i].id < list[j].id
			})
			prev := 0
			for _, r := range list {
				if r.repeat <= prev {
					// The same reading twice: one stays (FirmwareDuplicates).
					continue
				}
				n := max(r.repeat-prev, 1)
				if len(list) > 1 || r.repeat > 1 {
					if _, err := w.exec(`UPDATE event SET count = ?, covered = MIN(covered, ?) WHERE id = ?`, n, n, r.id); err != nil {
						return err
					}
				}
				prev = r.repeat
			}
		}
		return nil
	})
}

// marks are the action kinds a "cleared" record can take back.
var withdrawable = map[string]string{
	string(model.ActionDisable):          "disabled",
	string(model.ActionPredictedFailure): "predicted-failure",
}

// FirmwareWithdrawals sets withdrawn_by on a mark that a later record
// takes back: a module disabled and then in use again, a prediction that
// was cleared, for the same module (section 7.4). It is set again from
// the rows on every run, so a later dump withdraws a mark of an earlier one.
func (s *Store) FirmwareWithdrawals(ctx context.Context, host string) error {
	return s.Update(ctx, func(w *Writer) error {
		if _, err := w.exec(`UPDATE action SET withdrawn_by = NULL WHERE host = ? AND withdrawn_by IS NOT NULL`, host); err != nil {
			return err
		}
		events, err := w.firmwareEvents(host)
		if err != nil {
			return err
		}
		actions, err := w.firmwareActions(host)
		if err != nil {
			return err
		}
		for _, a := range actions {
			want, ok := withdrawable[a.kind]
			if !ok || a.where() == "" {
				continue
			}
			for _, e := range events {
				if e.common != "cleared" || e.clears != want || e.where() != a.where() || !e.at.After(a.at) {
					continue
				}
				if _, err := w.exec(`UPDATE action SET withdrawn_by = ? WHERE id = ?`, e.id, a.id); err != nil {
					return err
				}
				break
			}
		}
		return nil
	})
}

// FirmwareDuplicates marks the records of a BMC that report one error or
// one step a second time (section 7.2):
//
//   - the same entry read twice: an equal entry key and repeat;
//   - a burst of one log: uncorrected records that name the same module
//     within FirmwareWindow;
//   - two logs or readers of one BMC: records of the same module and
//     severity class within FirmwareWindow; the one a table classed stays
//     over one classed by its wording, then the one with more of the
//     place;
//   - actions of the same kind and result for the same module within
//     FirmwareWindow from two logs or readers.
//
// An error is declared through dup_fingerprint, which the duplicate pass
// of all sources (MarkDuplicates) takes as an adapter's declaration; an
// action gets dup_of. Corrected records of one log are each a report of
// their own and are not folded.
func (s *Store) FirmwareDuplicates(ctx context.Context, host string) (int, error) {
	n := 0
	err := s.Update(ctx, func(w *Writer) error {
		if _, err := w.exec(`UPDATE event SET dup_fingerprint = '' WHERE host = ? AND dup_fingerprint != ''
 AND id IN (SELECT event_id FROM fw_record WHERE host = ? AND event_id IS NOT NULL)`, host, host); err != nil {
			return err
		}
		if _, err := w.exec(`UPDATE action SET dup_of = NULL WHERE host = ? AND dup_of IS NOT NULL`, host); err != nil {
			return err
		}
		events, err := w.firmwareEvents(host)
		if err != nil {
			return err
		}
		dup := map[int64]*fwRow{} // a duplicate and the record that stays
		better := func(a, b *fwRow) bool {
			ra, rb := mappingRank(a.mapping), mappingRank(b.mapping)
			if ra != rb {
				return ra > rb
			}
			if a.place != b.place {
				return a.place > b.place
			}
			return a.id < b.id
		}
		// The same entry read twice.
		seenKey := map[string]*fwRow{}
		for _, e := range events {
			if e.entryKey == "" {
				continue
			}
			k := fmt.Sprintf("%s|%d|%s", e.entryKey, e.repeat, e.common)
			if first, ok := seenKey[k]; ok {
				dup[e.id] = first
				continue
			}
			seenKey[k] = e
		}
		// A burst of uncorrected records in one log.
		for i, e := range events {
			if dup[e.id] != nil || e.class != "UE" || e.where() == "" {
				continue
			}
			for _, f := range events[i+1:] {
				if f.at.Sub(e.at) > FirmwareWindow {
					break
				}
				if dup[f.id] == nil && f.class == "UE" && f.where() == e.where() && f.source == e.source && f.log == e.log {
					dup[f.id] = e
				}
			}
		}
		// Two logs or readers of one BMC. A record stands for at most one
		// record of each other log.
		for i, e := range events {
			if dup[e.id] != nil || e.class == "" || e.where() == "" {
				continue
			}
			taken := map[string]bool{}
			for _, f := range events[i+1:] {
				if f.at.Sub(e.at) > FirmwareWindow {
					break
				}
				other := f.source + "|" + f.log
				if dup[f.id] != nil || f.class != e.class || f.where() != e.where() || (f.source == e.source && f.log == e.log) || taken[other] {
					continue
				}
				taken[other] = true
				keep, drop := e, f
				if better(f, e) {
					keep, drop = f, e
				}
				dup[drop.id] = keep
				if drop == e {
					break
				}
			}
		}
		ids := make([]int64, 0, len(dup))
		for id := range dup {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			keep := dup[id]
			for dup[keep.id] != nil {
				keep = dup[keep.id]
			}
			if _, err := w.exec(`UPDATE event SET dup_fingerprint = ? WHERE id = ?`, keep.fingerprint, id); err != nil {
				return err
			}
			n++
		}

		actions, err := w.firmwareActions(host)
		if err != nil {
			return err
		}
		dupA := map[int64]*fwRow{}
		for i, a := range actions {
			if dupA[a.id] != nil || a.where() == "" {
				continue
			}
			taken := map[string]bool{}
			for _, b := range actions[i+1:] {
				if b.at.Sub(a.at) > FirmwareWindow {
					break
				}
				same := b.kind == a.kind && b.result == a.result && b.where() == a.where()
				twice := a.entryKey != "" && a.entryKey == b.entryKey && a.repeat == b.repeat
				other := b.source + "|" + b.log
				if dupA[b.id] != nil || !same || (b.source == a.source && b.log == a.log && !twice) || taken[other] {
					continue
				}
				taken[other] = true
				keep, drop := a, b
				if b.place > a.place {
					keep, drop = b, a
				}
				dupA[drop.id] = keep
				if drop == a {
					break
				}
			}
		}
		for id, keep := range dupA {
			for dupA[keep.id] != nil {
				keep = dupA[keep.id]
			}
			if _, err := w.exec(`UPDATE action SET dup_of = ? WHERE id = ?`, keep.id, id); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("firmware duplicates: %w", err)
	}
	return n, nil
}

// mappingRank orders the ways a common event was found: a table first.
func mappingRank(m string) int {
	switch m {
	case model.MappingTable, model.MappingIPMI:
		return 3
	case model.MappingCPER:
		return 2
	case model.MappingRegistry, model.MappingWording:
		return 1
	}
	return 0
}

// BMCClockAlignment says what AlignBMCClock did with the records of one
// source of a BMC.
type BMCClockAlignment struct {
	Source  string
	Offset  time.Duration
	Records int
	Matched int
}

// Limits of the clock of a BMC.
const (
	// bmcClockAgree is how close the differences of the pairs of one error
	// have to be.
	bmcClockAgree = 2 * time.Second
	// bmcClockMin and bmcClockMax bound a difference that is taken for a
	// clock that is off.
	bmcClockMin = 60 * time.Second
	bmcClockMax = 15 * time.Hour
	// bmcClockPairs is how many pairs have to agree.
	bmcClockPairs = 3
	// bmcClockPerSlot bounds the records of one slot and class on each
	// side that are paired.
	bmcClockPerSlot = 256
)

// AlignBMCClock moves the times of the records of a BMC source when its
// clock was off (section 7.5): at least 3 pairs of a BMC record and a host
// record of the same slot and severity class agree on one difference
// within 2 s, the difference is between 60 s and 15 hours, and fewer than
// 2 pairs agree at zero. The moved records are marked estimated, which
// leaves them out of a later alignment; their stamp as the BMC wrote it
// stays in the raw text.
func (s *Store) AlignBMCClock(ctx context.Context, host string) ([]BMCClockAlignment, error) {
	var out []BMCClockAlignment
	err := s.Update(ctx, func(w *Writer) error {
		type rec struct {
			at     time.Time
			key    string
			source string
		}
		rows, err := w.tx.QueryContext(w.ctx, `SELECT "ErrorDT", "ErrorSeverity", slot, source, time_quality FROM event
 WHERE host = ? AND kind = 'error' AND slot != '' AND carried = 0 AND time_quality = 'exact'`, host)
		if err != nil {
			return err
		}
		var bmc, hostRecs []rec
		perKey := map[string]int{}
		for rows.Next() {
			var at, sev, slot, source, quality string
			if err := rows.Scan(&at, &sev, &slot, &source, &quality); err != nil {
				rows.Close()
				return err
			}
			class := model.Severity(sev).Class()
			if class == "" {
				continue
			}
			r := rec{parseTS(sql.NullString{String: at, Valid: true}), class + "|" + slot, source}
			// A few hundred records of a slot on each side say what the
			// clock did; a storm of them would only make the pairs many.
			if bmcSource(source) {
				if perKey[r.key+"|bmc"]++; perKey[r.key+"|bmc"] <= bmcClockPerSlot {
					bmc = append(bmc, r)
				}
			} else if perKey[r.key+"|host"]++; perKey[r.key+"|host"] <= bmcClockPerSlot {
				hostRecs = append(hostRecs, r)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		bySource := map[string][]time.Duration{}
		for _, b := range bmc {
			for _, h := range hostRecs {
				if h.key != b.key {
					continue
				}
				d := b.at.Sub(h.at)
				if d.Abs() <= bmcClockMax+bmcClockAgree {
					bySource[b.source] = append(bySource[b.source], d)
				}
			}
		}
		sources := make([]string, 0, len(bySource))
		for src := range bySource {
			sources = append(sources, src)
		}
		sort.Strings(sources)
		for _, src := range sources {
			diffs := bySource[src]
			sort.Slice(diffs, func(i, j int) bool { return diffs[i] < diffs[j] })
			zero := 0
			for _, d := range diffs {
				if d.Abs() <= bmcClockAgree {
					zero++
				}
			}
			if zero >= 2 {
				continue
			}
			// The largest group of differences that agree within 2 s.
			best, bestN := time.Duration(0), 0
			for i := range diffs {
				j := i
				for j < len(diffs) && diffs[j]-diffs[i] <= bmcClockAgree {
					j++
				}
				if j-i > bestN {
					bestN, best = j-i, diffs[i+(j-i)/2]
				}
			}
			if bestN < bmcClockPairs || best.Abs() < bmcClockMin || best.Abs() > bmcClockMax {
				continue
			}
			best = best.Round(time.Second)
			// The BMC's clock ran ahead by best: its records move back.
			moved := 0
			for _, table := range []struct{ name, col string }{{"event", `"ErrorDT"`}, {"action", "at"}} {
				rows, err := w.tx.QueryContext(w.ctx, `SELECT id, `+table.col+` FROM `+table.name+` WHERE host = ? AND source = ? AND time_quality = 'exact'`, host, src)
				if err != nil {
					return err
				}
				type stamp struct {
					id int64
					t  time.Time
				}
				var list []stamp
				for rows.Next() {
					var id int64
					var t string
					if err := rows.Scan(&id, &t); err != nil {
						rows.Close()
						return err
					}
					list = append(list, stamp{id, parseTS(sql.NullString{String: t, Valid: true})})
				}
				rows.Close()
				if err := rows.Err(); err != nil {
					return err
				}
				for _, st := range list {
					if _, err := w.exec(`UPDATE `+table.name+` SET `+table.col+` = ?, time_quality = 'estimated' WHERE id = ?`, ts(st.t.Add(-best)), st.id); err != nil {
						return err
					}
					moved++
				}
			}
			out = append(out, BMCClockAlignment{Source: src, Offset: -best, Records: moved, Matched: bestN})
		}
		return nil
	})
	return out, err
}
