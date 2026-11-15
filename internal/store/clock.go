package store

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/agenticode/my/internal/model"
)

// ClockAlignment says what AlignClocks did with the rasdaemon records of
// one boot.
type ClockAlignment struct {
	BootID string
	// Records is the number of rasdaemon records, errors and actions, that
	// were moved. Matched is how many of the errors the kernel log holds at
	// that distance.
	Records int
	Matched int
	// Offset is what was added to the times rasdaemon gave. It is negative
	// when rasdaemon's clock ran ahead of the kernel's.
	Offset time.Duration
}

// Limits of the clock alignment, in seconds where they are times.
const (
	// clockWindow is how far apart the two records of one error are looked
	// for. A clock that was set by a whole time zone is still found.
	clockWindow = 15 * 3600
	// clockNear is how far apart they may be when fewer than
	// alignMinMatched errors agree: one error in both sources is taken for
	// the same error only at the distance of a clock that was set at boot,
	// not hours apart.
	clockNear = 120
	// clockJitter is how far the records of several errors scatter around
	// one difference: rasdaemon counts in whole seconds.
	clockJitter = 1
	// clockSame is how far apart the two records of one error may be for
	// the clocks to agree. Records that close are taken for one error
	// anyway (DefaultDuplicateTolerance), and an emulated host shows a
	// second or two between a kernel log line and its trace event without
	// any clock having been set.
	clockSame = clockJitter + 1
	// clockSample and clockPairs bound the comparisons for one boot.
	clockSample = 4000
	clockPairs  = 2_000_000
)

// AlignClocks gives rasdaemon records the kernel's time where the kernel
// log of the same boot shows that rasdaemon's clock was off.
//
// rasdaemon dates a record by the uptime of its trace event plus the
// difference between wall clock and uptime that it measured when it
// started. A clock that is set after that, as NTP does at boot when the
// hardware clock was off, leaves every record of that run wrong by the
// step. The kernel log dates the same errors by the kernel's clock. Two
// records more than a few seconds apart are not taken for one error, so
// every error of such a run is counted twice.
//
// The rasdaemon adapter corrects this by itself where machine check
// records carry the kernel's time. This pass covers the hosts that have
// none: ARM servers, and firmware-first error handling.
//
// Two records are compared when they are of the same boot, cache line (or
// slot, where neither has an address) and severity class. The pass looks
// for the difference in time that explains the rasdaemon records:
//
//   - One difference at which every comparable record of the boot has a
//     kernel log record moves the whole boot. An error that repeats also
//     pairs with its other occurrences. No such pairing holds for the
//     first and the last record alike.
//   - Failing that, the records that have a kernel log record at their own
//     time are set aside together with it, one to one: an earlier run of
//     this pass moved them, or rasdaemon was started again after the clock
//     was set. The rest are moved when they agree among themselves, or when
//     at least three of them agree and no other difference comes close.
//     rasdaemon's clock is off for a whole run of the daemon, so in the
//     kernel's time they must all lie before the records that were set
//     aside, or all after them. A record nothing can be said about, and a
//     page offline, goes with the record nearest to it in time.
//
// One or two errors are believed when the difference is at most two
// minutes. A larger one needs three. Records up to two seconds apart are
// left where they are.
//
// The pass keeps no state. What it moved lies at the kernel's time
// afterwards, so a second run changes nothing, and a later scan moves only
// the records it added.
func (s *Store) AlignClocks(ctx context.Context, host string) ([]ClockAlignment, error) {
	fail := func(err error) ([]ClockAlignment, error) { return nil, fmt.Errorf("align clocks: %w", err) }
	daemon, err := s.clockRecords(ctx, host, "rasdaemon")
	if err != nil {
		return fail(err)
	}
	if len(daemon) == 0 {
		return nil, nil
	}
	kernel, err := s.clockRecords(ctx, host, "klog")
	if err != nil {
		return fail(err)
	}
	if len(kernel) == 0 {
		return nil, nil
	}

	boots := map[string]*clockBoot{}
	boot := func(id string) *clockBoot {
		b := boots[id]
		if b == nil {
			b = &clockBoot{anchors: map[string][]int64{}}
			boots[id] = b
		}
		return b
	}
	for _, k := range kernel {
		if k.key != "" {
			b := boot(k.boot)
			b.anchors[k.key] = append(b.anchors[k.key], k.at)
		}
	}
	for _, r := range daemon {
		b := boot(r.boot)
		b.recs = append(b.recs, r)
	}
	// The page offlines rasdaemon recorded go with its errors.
	rows, err := s.db.QueryContext(ctx, `SELECT id, at, COALESCE(boot_id, '') FROM action
 WHERE host = ? AND time_quality = 'exact' AND source LIKE 'rasdaemon/%' ORDER BY at, id`, host)
	if err != nil {
		return fail(err)
	}
	for rows.Next() {
		var a clockRec
		var at string
		if err := rows.Scan(&a.id, &at, &a.boot); err != nil {
			rows.Close()
			return fail(err)
		}
		a.at = parseTS(sql.NullString{String: at, Valid: true}).Unix()
		if b := boots[a.boot]; b != nil {
			b.actions = append(b.actions, a)
		}
	}
	if err := rows.Close(); err != nil {
		return fail(err)
	}
	if err := rows.Err(); err != nil {
		return fail(err)
	}

	ids := make([]string, 0, len(boots))
	for id := range boots {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []ClockAlignment
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		b := boots[id]
		if len(b.recs) == 0 || len(b.anchors) == 0 {
			continue
		}
		offset, matched, marks := b.solve()
		if matched == 0 {
			continue
		}
		// Records the kernel log has no word on go with the nearest one
		// it has.
		type decided struct {
			at    int64
			moves bool
		}
		var known []decided
		for i, m := range marks {
			if m != clockUnknown {
				known = append(known, decided{b.recs[i].at, m == clockMoves})
			}
		}
		moves := func(at int64) bool {
			i := sort.Search(len(known), func(i int) bool { return known[i].at >= at })
			switch {
			case i == len(known):
				return known[i-1].moves
			case i > 0 && at-known[i-1].at <= known[i].at-at:
				return known[i-1].moves
			}
			return known[i].moves
		}
		var events, actions []int64
		for i, r := range b.recs {
			if marks[i] == clockMoves || (marks[i] == clockUnknown && moves(r.at)) {
				events = append(events, r.id)
			}
		}
		for _, a := range b.actions {
			if moves(a.at) {
				actions = append(actions, a.id)
			}
		}
		err := s.Update(ctx, func(w *Writer) error {
			if err := w.shiftTimes("event", `"ErrorDT"`, events, offset); err != nil {
				return err
			}
			return w.shiftTimes("action", "at", actions, offset)
		})
		if err != nil {
			return fail(err)
		}
		out = append(out, ClockAlignment{BootID: id, Records: len(events) + len(actions), Matched: matched, Offset: time.Duration(offset) * time.Second})
	}
	return out, nil
}

// clockRec is a record in the terms the comparison needs.
type clockRec struct {
	id   int64
	at   int64  // Unix seconds
	key  string // the error it reports; empty when it cannot be compared
	boot string
}

// clockRecords returns the dated events of one reporter in time order.
func (s *Store) clockRecords(ctx context.Context, host, reporter string) ([]clockRec, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, "ErrorDT", kind, "ErrorSeverity", "OsPhysicalAddress", slot, COALESCE(boot_id, '') FROM event
 WHERE host = ? AND time_quality = 'exact' AND source LIKE ? ORDER BY "ErrorDT", id`, host, reporter+"/%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []clockRec
	for rows.Next() {
		var r clockRec
		var dt, kind, sev, slot string
		var addr sql.NullInt64
		if err := rows.Scan(&r.id, &dt, &kind, &sev, &addr, &slot, &r.boot); err != nil {
			return nil, err
		}
		r.at = parseTS(sql.NullString{String: dt, Valid: true}).Unix()
		if class := model.Severity(sev).Class(); kind == string(model.KindError) && class != "" {
			switch {
			case addr.Valid:
				r.key = fmt.Sprintf("%s|a%x", class, uint64(addr.Int64)>>6)
			case slot != "":
				r.key = class + "|s" + slot
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// shiftTimes adds seconds to the times of the rows with the given ids.
func (w *Writer) shiftTimes(table, col string, ids []int64, seconds int64) error {
	// The fraction of the second stays as it is: the offset is whole
	// seconds, and the ledger form of a time has a fixed width.
	q := `UPDATE ` + table + ` SET ` + col + ` = strftime('%Y-%m-%dT%H:%M:%S', ` + col + `, ?) || substr(` + col + `, 20) WHERE id IN (`
	shift := fmt.Sprintf("%+d seconds", seconds)
	for len(ids) > 0 {
		n := min(len(ids), 400)
		args := make([]any, 0, n+1)
		args = append(args, shift)
		for _, id := range ids[:n] {
			args = append(args, id)
		}
		if _, err := w.exec(q+placeholders(n)+`)`, args...); err != nil {
			return err
		}
		ids = ids[n:]
	}
	return nil
}

// clockBoot holds what the two sources recorded in one boot.
type clockBoot struct {
	recs    []clockRec         // rasdaemon's events in time order
	actions []clockRec         // rasdaemon's actions in time order
	anchors map[string][]int64 // times of the kernel log records by error, ascending
}

// What the kernel log says about the time of a rasdaemon record.
const (
	clockUnknown = iota // nothing
	clockStays          // a kernel log record of its error lies at its own time
	clockMoves          // one lies at the offset of the boot
)

// solve finds how far rasdaemon's clock was from the kernel's in the boot.
// It returns the seconds to add to rasdaemon's times, how many records the
// kernel log holds at that distance, and what is known of each record.
// matched is zero when the clocks agree or nothing settles the question.
func (b *clockBoot) solve() (offset int64, matched int, marks []uint8) {
	marks = make([]uint8, len(b.recs))
	near := func(d int64) bool { return d >= -clockNear && d <= clockNear }

	// The whole boot at once.
	var comparable, counts []int
	for i, r := range b.recs {
		if r.key == "" {
			continue
		}
		if lo, hi := clockSpan(b.anchors[r.key], r.at); hi > lo {
			comparable, counts = append(comparable, i), append(counts, hi-lo)
		}
	}
	if len(comparable) == 0 {
		return 0, 0, marks
	}
	var all clockVote
	for _, j := range clockPick(counts) {
		r := b.recs[comparable[j]]
		all.add(r.at, b.anchors[r.key], nil)
	}
	if d, agree, ok := all.unanimous(); ok {
		if agree || (len(comparable) < alignMinMatched && !near(d)) {
			return 0, 0, marks // the clocks agree, or too few errors say they do not
		}
		for _, i := range comparable {
			marks[i] = clockMoves
		}
		return d, len(comparable), marks
	}

	// A part of it. Each record takes the kernel log record at its own
	// time, if one is left: both lists are in time order, so the earliest
	// that fits is the right one to take.
	byKey := map[string][]int{}
	for _, i := range comparable {
		byKey[b.recs[i].key] = append(byKey[b.recs[i].key], i)
	}
	taken := map[string][]bool{}
	free := map[string][]int{} // free[key][j]: kernel log records before j that nothing took
	for key, idx := range byKey {
		as := b.anchors[key]
		tk := make([]bool, len(as))
		j := 0
		for _, i := range idx {
			at := b.recs[i].at
			for j < len(as) && as[j] < at-clockSame {
				j++
			}
			if j < len(as) && as[j] <= at+clockSame {
				tk[j], marks[i] = true, clockStays
				j++
			}
		}
		fr := make([]int, len(as)+1)
		for j, t := range tk {
			fr[j+1] = fr[j]
			if !t {
				fr[j+1]++
			}
		}
		taken[key], free[key] = tk, fr
	}
	var voters []int
	counts = counts[:0]
	for _, i := range comparable {
		r := b.recs[i]
		if marks[i] == clockStays {
			continue
		}
		lo, hi := clockSpan(b.anchors[r.key], r.at)
		if free[r.key][hi] > free[r.key][lo] {
			voters, counts = append(voters, i), append(counts, hi-lo)
		}
	}
	var rest clockVote
	for _, j := range clockPick(counts) {
		r := b.recs[voters[j]]
		rest.add(r.at, b.anchors[r.key], taken[r.key])
	}
	d, _, ok := rest.unanimous()
	if ok {
		ok = len(voters) >= alignMinMatched || near(d)
	}
	if !ok {
		d, ok = rest.dominant()
	}
	if !ok {
		return 0, 0, marks
	}
	for _, i := range voters {
		r := b.recs[i]
		as, tk := b.anchors[r.key], taken[r.key]
		j := sort.Search(len(as), func(j int) bool { return as[j] >= r.at+d-clockJitter })
		for ; j < len(as) && as[j] <= r.at+d+clockJitter; j++ {
			if !tk[j] {
				marks[i] = clockMoves
				matched++
				break
			}
		}
	}
	// In the kernel's time the records that move lie before all that
	// stay, or after them. Where they are mixed, a record and a kernel log
	// line of two errors at one address were taken for one error.
	const unset = math.MinInt64
	firstMove, lastMove, firstStay, lastStay := int64(unset), int64(unset), int64(unset), int64(unset)
	span := func(first, last *int64, at int64) {
		if *first == unset || at < *first {
			*first = at
		}
		if *last == unset || at > *last {
			*last = at
		}
	}
	for i, m := range marks {
		switch m {
		case clockMoves:
			span(&firstMove, &lastMove, b.recs[i].at+d)
		case clockStays:
			span(&firstStay, &lastStay, b.recs[i].at)
		}
	}
	if matched == 0 || (firstStay != unset && lastMove > firstStay && lastStay > firstMove) {
		return 0, 0, marks
	}
	return d, matched, marks
}

// clockSpan returns the part of as, which is ascending, that lies within
// the window around at.
func clockSpan(as []int64, at int64) (lo, hi int) {
	lo = sort.Search(len(as), func(i int) bool { return as[i] >= at-clockWindow })
	hi = sort.Search(len(as), func(i int) bool { return as[i] > at+clockWindow })
	return lo, hi
}

// clockPick chooses the records that vote, given how many kernel log
// records each is compared with: all of them when that stays within the
// limits, else an even selection that keeps the first and the last.
func clockPick(counts []int) []int {
	n, total := len(counts), 0
	for _, c := range counts {
		total += c
	}
	k := min(n, clockSample)
	if total > clockPairs {
		k = min(k, int(float64(n)*clockPairs/float64(total)))
	}
	k = max(k, min(n, alignMinMatched))
	out := make([]int, 0, k)
	for j := 0; j < k; j++ {
		if k == n {
			out = append(out, j)
			continue
		}
		out = append(out, int(math.Round(float64(j)*float64(n-1)/float64(k-1))))
	}
	return out
}

// clockVote counts which differences in time a set of records agree on.
type clockVote struct {
	exact map[int64]int // records with a kernel log record exactly that far away
	near  map[int64]int // records with one within clockJitter of it
	n     int           // records with any
}

// add counts the kernel log records of the error of one record: as holds
// their times in ascending order, skip marks those to leave out.
func (v *clockVote) add(at int64, as []int64, skip []bool) {
	if v.exact == nil {
		v.exact, v.near = map[int64]int{}, map[int64]int{}
	}
	lo, hi := clockSpan(as, at)
	// A record counts once for a difference, however many kernel log
	// records lie there.
	lastExact, lastNear := int64(math.MinInt64), int64(math.MinInt64)
	seen := false
	for i := lo; i < hi; i++ {
		if skip != nil && skip[i] {
			continue
		}
		d := as[i] - at
		seen = true
		if d != lastExact {
			v.exact[d]++
			lastExact = d
		}
		for x := max(d-clockJitter, lastNear+1); x <= d+clockJitter; x++ {
			v.near[x]++
			lastNear = x
		}
	}
	if seen {
		v.n++
	}
}

// unanimous returns the difference every record agrees with, when there is
// one and only one. agree says that every record has a kernel log record
// within clockSame of its own time: the clocks do not differ.
func (v *clockVote) unanimous() (d int64, agree, ok bool) {
	var full []int64
	for d, n := range v.near {
		if n == v.n {
			full = append(full, d)
		}
	}
	if len(full) == 0 {
		return 0, false, false
	}
	sort.Slice(full, func(i, j int) bool { return full[i] < full[j] })
	// rasdaemon cuts the second off, so one difference shows as two
	// neighbouring values. A wider range is more than one answer.
	lo, hi := full[0], full[len(full)-1]
	if hi-lo > 2*clockJitter+2 {
		return 0, false, false
	}
	// The middle of the range; of two middles the one more records have
	// exactly. An error that repeats every second widens the range on
	// both sides.
	off := func(d int64) int64 { return max(2*d-lo-hi, lo+hi-2*d) }
	best := full[0]
	for _, d := range full[1:] {
		if off(d) < off(best) || (off(d) == off(best) && v.exact[d] > v.exact[best]) {
			best = d
		}
	}
	return best, lo <= clockJitter && hi >= -clockJitter, true
}

// dominant returns the difference most records agree with, when enough of
// them do and no other difference has half as many.
func (v *clockVote) dominant() (int64, bool) {
	keys := make([]int64, 0, len(v.near))
	for d := range v.near {
		keys = append(keys, d)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	var best int64
	bestN, second := 0, 0
	for _, d := range keys {
		if n := v.near[d]; n > bestN || (n == bestN && v.exact[d] > v.exact[best]) {
			best, bestN = d, n
		}
	}
	for _, d := range keys {
		if d < best-2*clockJitter-1 || d > best+2*clockJitter+1 {
			second = max(second, v.near[d])
		}
	}
	return best, bestN >= alignMinMatched && float64(bestN) >= alignMinShare*float64(v.n) && bestN >= 2*second
}
