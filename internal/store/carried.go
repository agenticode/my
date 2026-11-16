package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/agenticode/my/internal/model"
)

// QualityUnknown is the start quality of a boot that is known to have been
// there and whose start is not on record: the boot before the one that
// read a carried record, when the ledger holds no earlier boot.
const QualityUnknown = "unknown"

// LinkCarried puts every carried record into the boot it belongs to.
//
// A carried record was kept by hardware or firmware over a reset and
// logged by a later boot: the contents of a machine check bank that the
// next boot polled, a record of the firmware's boot error region. Its
// source dates it to the boot that read it. The error happened in the boot
// before, at a time after that boot started and before the reading boot
// started. The pass sets, for each carried record:
//
//   - read_boot_id: the boot that read it (the boot the record had before
//     the first run of the pass);
//   - boot_id: the boot before that one, by start time;
//   - "ErrorDT": the last time the boot before was seen, which is the
//     latest time on record for it, else one second before the reading
//     boot started; time_quality 'estimated';
//   - not_before: the start of the boot before.
//
// When the ledger holds no earlier boot, the pass adds one with start
// quality 'unknown' one second before the reading boot, and removes it
// again once a real boot before is on record. It runs after LinkBoots and
// computes everything from the rows: running it again changes nothing.
func (s *Store) LinkCarried(ctx context.Context, host string) error {
	type boot struct {
		id             string
		start, last    time.Time
		quality        string
		placeholderFor string
	}
	var boots []boot
	byID := map[string]*boot{}
	rows, err := s.db.QueryContext(ctx, `SELECT boot_id, started_at, last_seen, start_quality, raw_ref FROM boot WHERE host = ? ORDER BY started_at, boot_id`, host)
	if err != nil {
		return fmt.Errorf("link carried: %w", err)
	}
	for rows.Next() {
		var b boot
		var start, last sql.NullString
		var ref string
		if err := rows.Scan(&b.id, &start, &last, &b.quality, &ref); err != nil {
			rows.Close()
			return err
		}
		b.start, b.last = parseTS(start), parseTS(last)
		if b.quality == QualityUnknown {
			b.placeholderFor = ref
		}
		boots = append(boots, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("link carried: %w", err)
	}
	for i := range boots {
		byID[boots[i].id] = &boots[i]
	}

	type carried struct {
		id               int64
		bootID, readBoot string
	}
	var recs []carried
	rows, err = s.db.QueryContext(ctx, `SELECT id, COALESCE(boot_id, ''), COALESCE(read_boot_id, '') FROM event
 WHERE host = ? AND carried = 1 AND time_quality != 'relative' ORDER BY id`, host)
	if err != nil {
		return fmt.Errorf("link carried: %w", err)
	}
	for rows.Next() {
		var c carried
		if err := rows.Scan(&c.id, &c.bootID, &c.readBoot); err != nil {
			rows.Close()
			return err
		}
		recs = append(recs, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("link carried: %w", err)
	}
	if len(recs) == 0 {
		return s.prunePlaceholders(ctx, host)
	}

	return s.Update(ctx, func(w *Writer) error {
		for _, c := range recs {
			read := c.readBoot
			if read == "" {
				read = c.bootID
			}
			rb := byID[read]
			if rb == nil || rb.quality == string(model.TimeRelative) || rb.quality == QualityUnknown {
				continue
			}
			// The boot before: the latest that started before the reading
			// boot and whose start is on record.
			var prev *boot
			for i := range boots {
				b := &boots[i]
				if b.quality == string(model.TimeRelative) || b.quality == QualityUnknown || !b.start.Before(rb.start) {
					continue
				}
				prev = b
			}
			var target string
			var at, notBefore time.Time
			if prev != nil {
				target, notBefore = prev.id, prev.start
				at = prev.last
				if !at.After(prev.start) || !at.Before(rb.start) {
					at = rb.start.Add(-time.Second)
				}
			} else {
				target = placeholderID(host, rb.id)
				at = rb.start.Add(-time.Second)
				if byID[target] == nil {
					if _, err := w.exec(`INSERT INTO boot (host, boot_id, derived, started_at, start_quality, last_seen, source, raw_ref)
 VALUES (?, ?, 1, ?, ?, ?, 'carried', ?)`, host, target, ts(at), QualityUnknown, ts(at), rb.id); err != nil {
						return fmt.Errorf("link carried: %w", err)
					}
					b := boot{id: target, start: at, last: at, quality: QualityUnknown, placeholderFor: rb.id}
					boots = append(boots, b)
					byID = map[string]*boot{}
					for i := range boots {
						byID[boots[i].id] = &boots[i]
					}
				}
			}
			if _, err := w.exec(`UPDATE event SET read_boot_id = ?, boot_id = ?, "ErrorDT" = ?, not_before = ?, time_quality = 'estimated' WHERE id = ?`,
				read, target, ts(at), ts(notBefore), c.id); err != nil {
				return fmt.Errorf("link carried: %w", err)
			}
		}
		return w.prunePlaceholders(host)
	})
}

// placeholderID is the ID of the boot LinkCarried puts before a reading
// boot when the ledger holds no earlier one. It is a derived ID, so that
// an anonymized bundle hashes it like the others.
func placeholderID(host, readBoot string) string {
	return "derived-" + model.Fingerprint("boot-before", host, readBoot)[:16]
}

func (s *Store) prunePlaceholders(ctx context.Context, host string) error {
	return s.Update(ctx, func(w *Writer) error { return w.prunePlaceholders(host) })
}

// prunePlaceholders removes the boots of unknown start that no record
// belongs to any more.
func (w *Writer) prunePlaceholders(host string) error {
	_, err := w.exec(`DELETE FROM boot WHERE host = ? AND start_quality = ?
 AND NOT EXISTS (SELECT 1 FROM event e WHERE e.host = boot.host AND e.boot_id = boot.boot_id)
 AND NOT EXISTS (SELECT 1 FROM action a WHERE a.host = boot.host AND a.boot_id = boot.boot_id)`, host, QualityUnknown)
	if err != nil {
		return fmt.Errorf("link carried: %w", err)
	}
	return nil
}

// stopRecords returns, per boot, the records that show that a hardware
// error stopped it: carried records of uncorrected errors and fatal boot
// error blocks, which the next boot read, and the records of the kernel's
// own panic lines about a hardware error.
func (s *Store) stopRecords(ctx context.Context, host string) (map[string]endRecord, error) {
	out := map[string]endRecord{}
	rows, err := s.db.QueryContext(ctx, `SELECT id, boot_id, source, "ErrorSeverity", error_type FROM event
 WHERE host = ? AND carried = 1 AND boot_id IS NOT NULL AND kind = 'error'
 AND ("ErrorSeverity" IN ('UE', 'Fatal', 'Recoverable') OR ("ErrorSeverity" = 'Informational' AND error_type = ?))
 ORDER BY id`, host, model.ErrorTypeFatalBlock)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r endRecord
		var boot, sev, et string
		if err := rows.Scan(&r.id, &boot, &r.source, &sev, &et); err != nil {
			return nil, err
		}
		if _, ok := out[boot]; !ok {
			r.end = model.BootStop
			out[boot] = r
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// The kernel's panic lines of a fatal machine check or a fatal firmware
	// error are read by the kernel log reader; their record is
	// kernelStops'. They are later in the boot than anything carried.
	ks, err := s.kernelStops(ctx, host)
	if err != nil {
		return nil, err
	}
	for boot, r := range ks {
		if cur, ok := out[boot]; !ok || cur.at.IsZero() {
			out[boot] = r
		}
	}
	return out, nil
}

// kernelStops returns the stops the kernel itself printed, per boot: the
// panic lines of a fatal machine check ("Kernel panic - not syncing:
// Fatal machine check") or of a fatal firmware error. The kernel log
// reader does not keep them as records yet; until it does there are none.
func (s *Store) kernelStops(ctx context.Context, host string) (map[string]endRecord, error) {
	return map[string]endRecord{}, nil
}

// endRecord is the record that shows how a boot ended.
type endRecord struct {
	id     int64
	end    string
	source string
	at     time.Time
}

// BootOutcomes sets how each boot of a host ended, from the records:
//
//   - stop, when a hardware error stopped it: a carried record of an
//     uncorrected error or a fatal boot error block that the next boot
//     read, or the kernel's panic lines about a hardware error;
//   - panic, when a crash log shows a panic and no record shows a
//     hardware error: a pstore dump written for a panic, a kdump log;
//   - nothing otherwise. A boot that ended cleanly and one that lost power
//     look the same in the records.
//
// end_ref names the record ("event:<id>"), end_source its source. It runs
// after the duplicates and computes everything from the rows.
func (s *Store) BootOutcomes(ctx context.Context, host string) error {
	stops, err := s.stopRecords(ctx, host)
	if err != nil {
		return fmt.Errorf("boot outcomes: %w", err)
	}
	panics := map[string]endRecord{}
	rows, err := s.db.QueryContext(ctx, `SELECT id, boot_id, source, "ErrorDT" FROM event
 WHERE host = ? AND boot_id IS NOT NULL AND "ErrorSeverity" = 'Informational' AND error_type = ? ORDER BY "ErrorDT", id`, host, model.ErrorTypePanic)
	if err != nil {
		return fmt.Errorf("boot outcomes: %w", err)
	}
	for rows.Next() {
		var r endRecord
		var boot, at string
		if err := rows.Scan(&r.id, &boot, &r.source, &at); err != nil {
			rows.Close()
			return err
		}
		r.end, r.at = model.BootPanic, parseTS(sql.NullString{String: at, Valid: true})
		if _, ok := panics[boot]; !ok {
			panics[boot] = r
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("boot outcomes: %w", err)
	}

	type current struct {
		end, ref, source string
		at               time.Time
	}
	have := map[string]current{}
	var ids []string
	rows, err = s.db.QueryContext(ctx, `SELECT boot_id, "end", end_at, end_ref, end_source FROM boot WHERE host = ?`, host)
	if err != nil {
		return fmt.Errorf("boot outcomes: %w", err)
	}
	for rows.Next() {
		var id string
		var c current
		var at sql.NullString
		if err := rows.Scan(&id, &c.end, &at, &c.ref, &c.source); err != nil {
			rows.Close()
			return err
		}
		c.at = parseTS(at)
		have[id] = c
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("boot outcomes: %w", err)
	}
	sort.Strings(ids)
	return s.Update(ctx, func(w *Writer) error {
		for _, id := range ids {
			var want current
			r, ok := stops[id]
			if !ok {
				r, ok = panics[id]
			}
			if ok {
				want = current{end: r.end, ref: "event:" + strconv.FormatInt(r.id, 10), source: r.source, at: r.at}
			}
			if cur := have[id]; cur.end == want.end && cur.ref == want.ref && cur.source == want.source && cur.at.Equal(want.at) {
				continue
			}
			if _, err := w.exec(`UPDATE boot SET "end" = ?, end_at = ?, end_ref = ?, end_source = ? WHERE host = ? AND boot_id = ?`,
				want.end, ts(want.at), want.ref, want.source, host, id); err != nil {
				return fmt.Errorf("boot outcomes: %w", err)
			}
		}
		return nil
	})
}

// markCarried matches carried records with the other reports of the same
// error. Their time is a bound, so the time tolerance of MarkDuplicates is
// replaced by the boot: two records are one error when they belong to the
// same boot, have the same severity class and the same cache line address,
// or the same slot when neither has an address. Without an address on
// either side, a carried uncorrected record is the one uncorrected record
// of its boot when that boot has exactly one. The pairs:
//
//   - the panic text of the boot (pstore, kdump, a console, the journal)
//     and the bank record the next boot read: the record with the exact
//     time is counted at equal grade;
//   - a boot error record of the firmware and the machine check or
//     firmware record of the boot before: the best grade is counted;
//   - the region of boot error records and the lines the kernel printed
//     of it (two carried records of one reading boot): the region;
//   - a record of a BMC log for the same slot anywhere in the boot.
//
// Records of the same source may pair when one of them is carried: the
// kernel log holds both the panic text and the polled bank record.
func (s *Store) markCarried(ctx context.Context, host string) error {
	type rec struct {
		id                         int64
		boot, readBoot, src, class string
		slot                       string
		addr                       sql.NullInt64
		grade                      model.Grade
		carried                    bool
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, boot_id, COALESCE(read_boot_id, ''), source, "ErrorSeverity", slot, "OsPhysicalAddress", grade, carried
 FROM event WHERE host = ? AND kind = 'error' AND dup_of IS NULL AND boot_id IS NOT NULL
 AND boot_id IN (SELECT boot_id FROM event WHERE host = ? AND carried = 1 AND dup_of IS NULL AND boot_id IS NOT NULL)
 ORDER BY id`, host, host)
	if err != nil {
		return err
	}
	byBoot := map[string][]*rec{}
	var order []string
	for rows.Next() {
		var r rec
		var sev, grade string
		var carried int64
		if err := rows.Scan(&r.id, &r.boot, &r.readBoot, &r.src, &sev, &r.slot, &r.addr, &grade, &carried); err != nil {
			rows.Close()
			return err
		}
		r.class, r.grade, r.carried = model.Severity(sev).Class(), model.Grade(grade), carried != 0
		if r.class == "" {
			continue
		}
		if _, ok := byBoot[r.boot]; !ok {
			order = append(order, r.boot)
		}
		byBoot[r.boot] = append(byBoot[r.boot], &r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	// better reports whether a should be counted rather than b.
	better := func(a, b *rec) bool {
		switch {
		case gradeRank(a.grade) != gradeRank(b.grade):
			return gradeRank(a.grade) > gradeRank(b.grade)
		case a.carried != b.carried:
			return !a.carried // the exact time
		case (a.src == "bert") != (b.src == "bert"):
			return a.src == "bert" // the region over the printed lines
		}
		return a.id < b.id
	}
	type pair struct{ dup, primary int64 }
	var pairs []pair
	for _, boot := range order {
		recs := byBoot[boot]
		gone := map[int64]bool{}
		// The uncorrected records of the boot that are not carried.
		var ue []*rec
		for _, r := range recs {
			if !r.carried && r.class == "UE" && !bmcSource(r.src) {
				ue = append(ue, r)
			}
		}
		for _, c := range recs {
			if !c.carried || gone[c.id] {
				continue
			}
			var match *rec
			for _, r := range recs {
				if r == c || gone[r.id] || r.class != c.class {
					continue
				}
				if r.carried && (r.readBoot != c.readBoot || reporter(r.src) == reporter(c.src)) {
					continue
				}
				same := false
				switch {
				case bmcSource(r.src):
					same = c.slot != "" && r.slot == c.slot
				case c.addr.Valid && r.addr.Valid:
					same = uint64(c.addr.Int64)>>6 == uint64(r.addr.Int64)>>6
				case c.addr.Valid || r.addr.Valid:
				case c.slot != "" && r.slot == c.slot:
					same = true
				default:
					same = c.class == "UE" && len(ue) == 1 && ue[0] == r
				}
				if !same {
					continue
				}
				if match == nil || better(r, match) {
					match = r
				}
			}
			if match == nil {
				continue
			}
			keep, drop := c, match
			if better(match, c) {
				keep, drop = match, c
			}
			gone[drop.id] = true
			pairs = append(pairs, pair{drop.id, keep.id})
		}
	}
	if len(pairs) == 0 {
		return nil
	}
	return s.Update(ctx, func(w *Writer) error {
		for _, p := range pairs {
			if _, err := w.exec(`UPDATE event SET dup_of = ? WHERE id = ?`, p.primary, p.dup); err != nil {
				return err
			}
		}
		// Records that pointed at a record that is now a duplicate point
		// at the one that stays.
		_, err := w.exec(`UPDATE event SET dup_of = (SELECT p.dup_of FROM event p WHERE p.id = event.dup_of)
 WHERE host = ? AND dup_of IN (SELECT id FROM event WHERE host = ? AND dup_of IS NOT NULL)`, host, host)
		return err
	})
}
