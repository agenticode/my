package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/agenticode/my/internal/model"
)

// Alignment says what AlignTimes did with the records of one boot whose
// times were not known exactly.
type Alignment struct {
	BootID string
	// Records is the number of error records of the boot that lack an
	// exact time, Matched how many of them agree with a dated record of
	// another source once the boot is moved by Offset.
	Records int
	Matched int
	// Offset is what was added to the times of the boot.
	Offset time.Duration
	// StartedAt is the start of the boot after the move.
	StartedAt time.Time
	// Aligned is set when the boot was moved. When it is not, dated
	// records of the same errors exist and no single offset explains them:
	// the records of the boot may then be counted twice.
	Aligned bool
	// MergedInto is the boot the records now belong to, when the moved
	// boot turned out to be a boot the ledger already knew.
	MergedInto string
}

// alignment limits.
const (
	// alignSample is how many undated records of a boot are compared.
	alignSample = 4000
	// alignPairs bounds the comparisons for one boot.
	alignPairs = 4_000_000
	// alignMinMatched and alignMinShare say when an offset is believed: at
	// least this many records, and this share of the records that had
	// anything to be compared with, must agree on it.
	alignMinMatched = 3
	alignMinShare   = 0.3
	// alignSameBoot is how close the moved start of a boot must be to the
	// start of a known boot to be that boot.
	alignSameBoot = 3 * time.Minute
)

// AlignTimes gives the records of a boot that carry only seconds since
// boot, or times estimated from a capture time, the dates other sources
// recorded for the same errors.
//
// A kernel log saved with dmesg has no dates. The rasdaemon database of
// the same host holds the same errors with dates. Every pair of records
// with the same cache line address and severity class votes for the
// difference of its two times; the true start of the boot gets one vote
// per error both sources saw, while wrong pairings scatter. When enough
// records agree, the boot and everything in it is moved by that offset and
// its times are marked estimated. The records can then be recognised as
// duplicates of the dated ones, and take part in trends.
//
// Without that, the errors of such a log are counted on top of the dated
// records of the same errors.
//
// The pass changes nothing when it is run again, and nothing for boots no
// other source has dated records of.
func (s *Store) AlignTimes(ctx context.Context, host string) ([]Alignment, error) {
	// The boots that hold records without an exact time. A record that
	// carries seconds since boot keeps the boot its source gave it; one
	// with an estimated time belongs to the boot LinkBoots found for it.
	type boot struct {
		id      string
		known   bool // the ledger has a row for the boot
		start   time.Time
		quality string
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT COALESCE(e.boot_id, ''), b.started_at, COALESCE(b.start_quality, '')
 FROM event e LEFT JOIN boot b ON b.host = e.host AND b.boot_id = e.boot_id
 WHERE e.host = ? AND e.time_quality IN ('relative', 'estimated') AND e.kind = 'error' AND e.dup_of IS NULL
 ORDER BY 1`, host)
	if err != nil {
		return nil, fmt.Errorf("align times: %w", err)
	}
	var boots []boot
	for rows.Next() {
		var b boot
		var at sql.NullString
		if err := rows.Scan(&b.id, &at, &b.quality); err != nil {
			rows.Close()
			return nil, err
		}
		b.start, b.known = parseTS(at), b.quality != ""
		boots = append(boots, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(boots) == 0 {
		return nil, nil
	}

	type lineKey struct {
		line  uint64
		class string
	}
	var out []Alignment
	for _, b := range boots {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// The records of the boot that can be compared: they name an address.
		type floating struct {
			key    lineKey
			at     int64 // seconds
			source string
		}
		var recs []floating
		total := 0
		rows, err := s.db.QueryContext(ctx, `SELECT "ErrorDT", "ErrorSeverity", "OsPhysicalAddress", source FROM event
 WHERE host = ? AND COALESCE(boot_id, '') = ? AND time_quality IN ('relative', 'estimated') AND kind = 'error' AND dup_of IS NULL
 ORDER BY "ErrorDT", id`, host, b.id)
		if err != nil {
			return nil, fmt.Errorf("align times: %w", err)
		}
		for rows.Next() {
			var dt, sev, source string
			var addr sql.NullInt64
			if err := rows.Scan(&dt, &sev, &addr, &source); err != nil {
				rows.Close()
				return nil, err
			}
			class := model.Severity(sev).Class()
			if class == "" {
				continue
			}
			total++
			if addr.Valid {
				t := parseTS(sql.NullString{String: dt, Valid: true})
				recs = append(recs, floating{lineKey{uint64(addr.Int64) >> 6, class}, t.Unix(), source})
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		if len(recs) == 0 {
			continue
		}
		// An even sample keeps a long log affordable.
		if len(recs) > alignSample {
			step := float64(len(recs)) / alignSample
			sample := make([]floating, 0, alignSample)
			for i := 0; i < alignSample; i++ {
				sample = append(sample, recs[int(float64(i)*step)])
			}
			recs = sample
		}
		want := map[lineKey]bool{}
		sources := map[string]bool{}
		for _, r := range recs {
			want[r.key] = true
			sources[r.source] = true
		}

		// Dated records of other sources at the same addresses.
		anchors := map[lineKey][]int64{}
		rows, err = s.db.QueryContext(ctx, `SELECT "ErrorDT", "ErrorSeverity", "OsPhysicalAddress", source FROM event
 WHERE host = ? AND time_quality = 'exact' AND kind = 'error' AND "OsPhysicalAddress" IS NOT NULL`, host)
		if err != nil {
			return nil, fmt.Errorf("align times: %w", err)
		}
		for rows.Next() {
			var dt, sev, source string
			var addr int64
			if err := rows.Scan(&dt, &sev, &addr, &source); err != nil {
				rows.Close()
				return nil, err
			}
			k := lineKey{uint64(addr) >> 6, model.Severity(sev).Class()}
			if !want[k] || sources[source] {
				continue
			}
			anchors[k] = append(anchors[k], parseTS(sql.NullString{String: dt, Valid: true}).Unix())
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		if len(anchors) == 0 {
			continue // no other source knows these errors: nothing to align with
		}

		// Every pair votes for the difference of its times.
		votes := map[int64]int{}
		comparable, pairs := 0, 0
		for _, r := range recs {
			as := anchors[r.key]
			if len(as) == 0 {
				continue
			}
			comparable++
			seen := map[int64]bool{}
			for _, a := range as {
				if d := a - r.at; !seen[d] {
					// One record gives one vote per offset, however many
					// dated records share its address and second.
					seen[d] = true
					votes[d]++
				}
				pairs++
			}
			if pairs > alignPairs {
				break
			}
		}
		// The offset with the most agreement, counting the seconds beside
		// it: a daemon stamps a record a moment after the kernel does.
		window := func(d int64) int { return votes[d-1] + votes[d] + votes[d+1] }
		var best int64
		bestN, secondN := 0, 0
		keys := make([]int64, 0, len(votes))
		for d := range votes {
			keys = append(keys, d)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		for _, d := range keys {
			if n := window(d); n > bestN || (n == bestN && votes[d] > votes[best]) {
				best, bestN = d, n
			}
		}
		for _, d := range keys {
			if d < best-2 || d > best+2 {
				secondN = max(secondN, window(d))
			}
		}
		al := Alignment{BootID: b.id, Records: total, Matched: bestN, StartedAt: b.start}
		if bestN < alignMinMatched || float64(bestN) < alignMinShare*float64(comparable) || bestN < 2*secondN {
			// Dated records of these errors exist and do not settle on one
			// offset. Say so: the records stay where they are.
			out = append(out, al)
			continue
		}
		if best >= -1 && best <= 1 {
			continue // already where the dated records are
		}
		al.Aligned, al.Offset = true, time.Duration(best)*time.Second
		floatingBoot := b.known && (b.quality == "relative" || b.quality == "estimated")
		if floatingBoot {
			al.StartedAt = b.start.Add(al.Offset)
			// A boot that now starts where a known boot starts is that boot.
			var known sql.NullString
			err = s.db.QueryRowContext(ctx, `SELECT boot_id FROM boot
 WHERE host = ? AND boot_id != ? AND start_quality = 'exact' AND started_at BETWEEN ? AND ?
 ORDER BY abs(julianday(started_at) - julianday(?)) LIMIT 1`,
				host, b.id, ts(al.StartedAt.Add(-alignSameBoot)), ts(al.StartedAt.Add(alignSameBoot)), ts(al.StartedAt)).Scan(&known)
			if err != nil && err != sql.ErrNoRows {
				return nil, fmt.Errorf("align times: %w", err)
			}
			al.MergedInto = known.String
		}

		shift := fmt.Sprintf("%+d seconds", best)
		err = s.Update(ctx, func(w *Writer) error {
			// The fraction of the second stays as it is: the offset is whole
			// seconds, and the ledger form of a time has a fixed width.
			moved := func(col string) string {
				return `strftime('%Y-%m-%dT%H:%M:%S', ` + col + `, ?) || substr(` + col + `, 20)`
			}
			target := any(b.id)
			switch {
			case al.MergedInto != "":
				target = al.MergedInto
			case b.id == "":
				target = nil
			}
			for _, q := range []struct {
				table, col string
			}{{"event", `"ErrorDT"`}, {"action", "at"}, {"gpu_mem", "observed_at"}, {"ecs_sample", "observed_at"}} {
				if _, err := w.exec(`UPDATE `+q.table+` SET `+q.col+` = `+moved(q.col)+`, time_quality = 'estimated', boot_id = ?
 WHERE host = ? AND COALESCE(boot_id, '') = ? AND time_quality IN ('relative', 'estimated')`, shift, target, host, b.id); err != nil {
					return err
				}
			}
			switch {
			case !floatingBoot:
				return nil
			case al.MergedInto != "":
				_, err := w.exec(`DELETE FROM boot WHERE host = ? AND boot_id = ?`, host, b.id)
				return err
			}
			_, err := w.exec(`UPDATE boot SET started_at = `+moved("started_at")+`,
 last_seen = CASE WHEN last_seen IS NULL THEN NULL ELSE `+moved("last_seen")+` END, start_quality = 'estimated'
 WHERE host = ? AND boot_id = ?`, shift, shift, host, b.id)
			return err
		})
		if err != nil {
			return nil, fmt.Errorf("align times: %w", err)
		}
		out = append(out, al)
	}
	return out, nil
}
