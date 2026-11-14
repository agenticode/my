package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/agenticode/my/internal/model"
)

// The functions in this file keep the ledger consistent after new records
// arrive. Each one recomputes its result from the rows in the ledger, so
// the outcome does not depend on the order in which sources were imported
// and running a pass twice changes nothing.

// LinkBoots sets the boot of every event, action, GPU memory record and ECS
// reading of a host from its time: a record belongs to the latest boot that
// started at or before it. Records before the first known boot keep an
// empty boot. Records whose time is only known relative to boot keep the
// boot their source gave them.
func (s *Store) LinkBoots(ctx context.Context, host string) error {
	return s.Update(ctx, func(w *Writer) error {
		for _, q := range []string{
			`UPDATE event SET boot_id = (
   SELECT b.boot_id FROM boot b
   WHERE b.host = event.host AND b.start_quality != 'relative' AND b.started_at <= event."ErrorDT"
   ORDER BY b.started_at DESC LIMIT 1)
 WHERE host = ? AND time_quality != 'relative'`,
			`UPDATE action SET boot_id = (
   SELECT b.boot_id FROM boot b
   WHERE b.host = action.host AND b.start_quality != 'relative' AND b.started_at <= action.at
   ORDER BY b.started_at DESC LIMIT 1)
 WHERE host = ? AND time_quality != 'relative'`,
			`UPDATE gpu_mem SET boot_id = (
   SELECT b.boot_id FROM boot b
   WHERE b.host = gpu_mem.host AND b.start_quality != 'relative' AND b.started_at <= gpu_mem.observed_at
   ORDER BY b.started_at DESC LIMIT 1)
 WHERE host = ? AND time_quality != 'relative'`,
			`UPDATE ecs_sample SET boot_id = (
   SELECT b.boot_id FROM boot b
   WHERE b.host = ecs_sample.host AND b.start_quality != 'relative' AND b.started_at <= ecs_sample.observed_at
   ORDER BY b.started_at DESC LIMIT 1)
 WHERE host = ? AND time_quality != 'relative'`,
		} {
			if _, err := w.exec(q, host); err != nil {
				return fmt.Errorf("link boots: %w", err)
			}
		}
		return nil
	})
}

// LocationKey is one distinct way events of a host name the place of an
// error. A host has few of them, however many events it has, so slots are
// resolved per key.
type LocationKey struct {
	EdacMC        model.Opt[int]
	EdacLocation  string
	SMBIOSHandle  model.Opt[int]
	Label         string
	SocketId      model.Opt[int]
	IMC           model.Opt[int]
	ChannelId     model.Opt[int]
	ChannelSlotId model.Opt[int]
	// Records is the number of events that use the key.
	Records int64
}

// EventLocations returns the distinct location keys of the events of a host.
func (s *Store) EventLocations(ctx context.Context, host string) ([]LocationKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT edac_mc, edac_location, smbios_handle, label, "SocketId", imc, "ChannelId", "ChannelSlotId", COUNT(*)
 FROM event WHERE host = ?
 GROUP BY edac_mc, edac_location, smbios_handle, label, "SocketId", imc, "ChannelId", "ChannelSlotId"
 ORDER BY edac_mc, edac_location, label, "SocketId", imc, "ChannelId", "ChannelSlotId"`, host)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LocationKey
	for rows.Next() {
		var k LocationKey
		var mc, handle, socket, imc, ch, pos sql.NullInt64
		if err := rows.Scan(&mc, &k.EdacLocation, &handle, &k.Label, &socket, &imc, &ch, &pos, &k.Records); err != nil {
			return nil, err
		}
		k.EdacMC, k.SMBIOSHandle = toOptInt(mc), toOptInt(handle)
		k.SocketId, k.IMC, k.ChannelId, k.ChannelSlotId = toOptInt(socket), toOptInt(imc), toOptInt(ch), toOptInt(pos)
		out = append(out, k)
	}
	return out, rows.Err()
}

// SetEventSlot sets the slot of all events of a host that use the location key.
func (w *Writer) SetEventSlot(host string, k LocationKey, slot string) error {
	_, err := w.exec(`UPDATE event SET slot = ?
 WHERE host = ? AND edac_mc IS ? AND edac_location = ? AND smbios_handle IS ? AND label = ?
   AND "SocketId" IS ? AND imc IS ? AND "ChannelId" IS ? AND "ChannelSlotId" IS ? AND slot != ?`,
		slot, host, optInt(k.EdacMC), k.EdacLocation, optInt(k.SMBIOSHandle), k.Label,
		optInt(k.SocketId), optInt(k.IMC), optInt(k.ChannelId), optInt(k.ChannelSlotId), slot)
	return err
}

// ECSLocations returns the distinct ways the ECS readings of a host name
// the module they were read from. Only the label and the socket, channel
// and slot numbers are set in the keys.
func (s *Store) ECSLocations(ctx context.Context, host string) ([]LocationKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT label, "SocketId", "ChannelId", "ChannelSlotId", COUNT(*)
 FROM ecs_sample WHERE host = ?
 GROUP BY label, "SocketId", "ChannelId", "ChannelSlotId"
 ORDER BY label, "SocketId", "ChannelId", "ChannelSlotId"`, host)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LocationKey
	for rows.Next() {
		var k LocationKey
		var socket, ch, pos sql.NullInt64
		if err := rows.Scan(&k.Label, &socket, &ch, &pos, &k.Records); err != nil {
			return nil, err
		}
		k.SocketId, k.ChannelId, k.ChannelSlotId = toOptInt(socket), toOptInt(ch), toOptInt(pos)
		out = append(out, k)
	}
	return out, rows.Err()
}

// SetECSSlot sets the slot of all ECS readings of a host that use the
// location key.
func (w *Writer) SetECSSlot(host string, k LocationKey, slot string) error {
	_, err := w.exec(`UPDATE ecs_sample SET slot = ?
 WHERE host = ? AND label = ? AND "SocketId" IS ? AND "ChannelId" IS ? AND "ChannelSlotId" IS ? AND slot != ?`,
		slot, host, k.Label, optInt(k.SocketId), optInt(k.ChannelId), optInt(k.ChannelSlotId), slot)
	return err
}

// ActionLabels returns the distinct labels the actions of a host name their
// target by.
func (s *Store) ActionLabels(ctx context.Context, host string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT label FROM action WHERE host = ? AND label != '' ORDER BY label`, host)
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

// SetActionSlot sets the slot of all actions of a host that name their
// target by the given label.
func (w *Writer) SetActionSlot(host, label, slot string) error {
	_, err := w.exec(`UPDATE action SET slot = ? WHERE host = ? AND label = ? AND slot != ?`, slot, host, label, slot)
	return err
}

// LinkDIMMs attributes the events, actions and ECS readings of a host to
// DIMM identities. Records must have their slot set first (SetEventSlot,
// SetActionSlot, SetECSSlot) and boots must be known for swaps to be placed
// in time.
//
// For each slot the placements say which DIMM was seen there and when:
//   - an event inside a placement belongs to that DIMM ("observed");
//   - an event before the first or after the last placement belongs to the
//     nearest one ("assumed");
//   - between two placements of different DIMMs the swap happened at a boot.
//     With one boot in the gap the events are split there. With several, the
//     events between the first and the last of them stay without a DIMM
//     ("ambiguous"), as do all events in the gap when no boot is known;
//   - a slot without any placement gets a placeholder identity ("slot").
//
// Page offline actions carry an address and no slot. They take the slot of
// an event on the same page.
func (s *Store) LinkDIMMs(ctx context.Context, host string) error {
	type placement struct {
		uid         string
		first, last time.Time
	}
	bySlot := map[string][]placement{}
	pl, err := s.Placements(ctx, host, "")
	if err != nil {
		return err
	}
	for _, p := range pl {
		bySlot[p.Slot] = append(bySlot[p.Slot], placement{p.DIMMUID, p.FirstSeen, p.LastSeen})
	}
	boots, err := s.Boots(ctx, host)
	if err != nil {
		return err
	}
	var bootTimes []time.Time
	for _, b := range boots {
		if b.StartQuality != model.TimeRelative {
			bootTimes = append(bootTimes, b.StartedAt)
		}
	}

	// Slots named by events or actions.
	slotSet := map[string]bool{}
	for _, q := range []string{
		`SELECT DISTINCT slot FROM event WHERE host = ? AND slot != ''`,
		`SELECT DISTINCT slot FROM action WHERE host = ? AND slot != ''`,
		`SELECT DISTINCT slot FROM ecs_sample WHERE host = ? AND slot != ''`,
	} {
		rows, err := s.db.QueryContext(ctx, q, host)
		if err != nil {
			return err
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return err
			}
			slotSet[name] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	slots := make([]string, 0, len(slotSet))
	for name := range slotSet {
		slots = append(slots, name)
	}
	sort.Strings(slots)

	return s.Update(ctx, func(w *Writer) error {
		// Page offline actions take the slot of an event on the same 4 KiB page.
		if _, err := w.exec(`UPDATE action SET slot = COALESCE((
   SELECT e.slot FROM event e
   WHERE e.host = action.host AND e.slot != '' AND e."OsPhysicalAddress" IS NOT NULL
     AND (e."OsPhysicalAddress" >> 12) = (action."OsPhysicalAddress" >> 12)
   ORDER BY e."ErrorDT" <= action.at DESC, e."ErrorDT" DESC LIMIT 1), '')
 WHERE host = ? AND "OsPhysicalAddress" IS NOT NULL AND label = '' AND kind = 'page-offline'`, host); err != nil {
			return fmt.Errorf("link dimms: action slots: %w", err)
		}
		rows, err := w.tx.QueryContext(w.ctx, `SELECT DISTINCT slot FROM action WHERE host = ? AND slot != ''`, host)
		if err != nil {
			return err
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return err
			}
			if !slotSet[name] {
				slotSet[name] = true
				slots = append(slots, name)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		sort.Strings(slots)

		// Records without a slot have no DIMM.
		for _, q := range []string{
			`UPDATE event  SET dimm_uid = NULL, dimm_link = '' WHERE host = ? AND slot = '' AND (dimm_uid IS NOT NULL OR dimm_link != '')`,
			`UPDATE action SET dimm_uid = NULL WHERE host = ? AND slot = '' AND dimm_uid IS NOT NULL`,
			`UPDATE ecs_sample SET dimm_uid = NULL WHERE host = ? AND slot = '' AND dimm_uid IS NOT NULL`,
		} {
			if _, err := w.exec(q, host); err != nil {
				return err
			}
		}

		assign := func(slot, uid, link string, from, to time.Time) error {
			var u any
			if uid != "" {
				u = uid
			}
			cond, args := ``, []any{u, link, host, slot}
			acond, aargs := ``, []any{u, host, slot}
			ccond, cargs := ``, []any{u, host, slot}
			if !from.IsZero() {
				cond, args = cond+` AND "ErrorDT" >= ?`, append(args, ts(from))
				acond, aargs = acond+` AND at >= ?`, append(aargs, ts(from))
				ccond, cargs = ccond+` AND observed_at >= ?`, append(cargs, ts(from))
			}
			if !to.IsZero() {
				cond, args = cond+` AND "ErrorDT" < ?`, append(args, ts(to))
				acond, aargs = acond+` AND at < ?`, append(aargs, ts(to))
				ccond, cargs = ccond+` AND observed_at < ?`, append(cargs, ts(to))
			}
			if _, err := w.exec(`UPDATE event SET dimm_uid = ?, dimm_link = ? WHERE host = ? AND slot = ?`+cond, args...); err != nil {
				return err
			}
			if _, err := w.exec(`UPDATE action SET dimm_uid = ? WHERE host = ? AND slot = ?`+acond, aargs...); err != nil {
				return err
			}
			_, err := w.exec(`UPDATE ecs_sample SET dimm_uid = ? WHERE host = ? AND slot = ?`+ccond, cargs...)
			return err
		}

		for _, slot := range slots {
			ps := bySlot[slot]
			if len(ps) == 0 {
				uid := model.SlotUID(host, slot)
				if err := w.UpsertDIMM(&model.DIMM{UID: uid, Identity: model.IdentitySlot}); err != nil {
					return err
				}
				if err := assign(slot, uid, model.LinkSlot, time.Time{}, time.Time{}); err != nil {
					return fmt.Errorf("link dimms: slot %s: %w", slot, err)
				}
				continue
			}
			// Merge neighbouring placements of the same DIMM into one span.
			var spans []placement
			for _, p := range ps {
				if n := len(spans); n > 0 && spans[n-1].uid == p.uid {
					if p.last.After(spans[n-1].last) {
						spans[n-1].last = p.last
					}
					continue
				}
				spans = append(spans, p)
			}
			// Before the first span and after the last one: assumed.
			if err := assign(slot, spans[0].uid, model.LinkAssumed, time.Time{}, spans[0].first); err != nil {
				return err
			}
			for i, sp := range spans {
				// Inside the span, both ends included.
				if err := assign(slot, sp.uid, model.LinkObserved, sp.first, sp.last.Add(time.Microsecond)); err != nil {
					return err
				}
				if i == len(spans)-1 {
					if err := assign(slot, sp.uid, model.LinkAssumed, sp.last.Add(time.Microsecond), time.Time{}); err != nil {
						return err
					}
					break
				}
				next := spans[i+1]
				gapFrom, gapTo := sp.last.Add(time.Microsecond), next.first
				var cuts []time.Time
				for _, bt := range bootTimes {
					if bt.After(sp.last) && !bt.After(next.first) {
						cuts = append(cuts, bt)
					}
				}
				switch len(cuts) {
				case 0:
					if err := assign(slot, "", model.LinkAmbiguous, gapFrom, gapTo); err != nil {
						return err
					}
				default:
					first, last := cuts[0], cuts[len(cuts)-1]
					if err := assign(slot, sp.uid, model.LinkAssumed, gapFrom, first); err != nil {
						return err
					}
					if last.After(first) {
						if err := assign(slot, "", model.LinkAmbiguous, first, last); err != nil {
							return err
						}
					}
					if err := assign(slot, next.uid, model.LinkAssumed, last, gapTo); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
}

// DuplicateTolerance bounds how far apart two reports of the same error may
// be in time when they come from different sources.
type DuplicateTolerance struct {
	// Exact applies when both records carry wall-clock times.
	Exact time.Duration
	// Estimated applies when either time was derived from seconds since boot.
	Estimated time.Duration
}

// DefaultDuplicateTolerance is used when a zero tolerance is passed.
var DefaultDuplicateTolerance = DuplicateTolerance{Exact: 5 * time.Second, Estimated: 2 * time.Minute}

func sourceRank(source string) int {
	switch {
	case strings.HasPrefix(source, "rasdaemon/"):
		return 3
	case strings.HasPrefix(source, "klog/"):
		return 2
	}
	return 1
}

func gradeRank(g model.Grade) int {
	switch g {
	case model.GradeA:
		return 3
	case model.GradeB:
		return 2
	}
	return 1
}

// MarkDuplicates finds records of a host that report the same error and
// keeps one of each group in the totals.
//
// Two kinds of duplicates exist. An adapter may declare that two of its own
// records describe one error (a machine check record and the EDAC record
// decoded from it); those declarations are applied as given. Records from
// different sources are matched here: same severity class, same cache line
// address (or same slot when neither has an address), times within the
// tolerance, and at most one record per source in a group. The record with
// the best observation grade stays; the others point at it through dup_of.
//
// It returns the number of records marked as duplicates.
func (s *Store) MarkDuplicates(ctx context.Context, host string, tol DuplicateTolerance) (int, error) {
	if tol.Exact <= 0 {
		tol.Exact = DefaultDuplicateTolerance.Exact
	}
	if tol.Estimated <= 0 {
		tol.Estimated = DefaultDuplicateTolerance.Estimated
	}
	maxTol := max(tol.Exact, tol.Estimated)

	// Start from the adapters' own declarations.
	err := s.Update(ctx, func(w *Writer) error {
		if _, err := w.exec(`UPDATE event SET dup_of = NULL WHERE host = ? AND dup_of IS NOT NULL`, host); err != nil {
			return err
		}
		_, err := w.exec(`UPDATE event SET dup_of = (
   SELECT p.id FROM event p WHERE p.fingerprint = event.dup_fingerprint AND p.id != event.id)
 WHERE host = ? AND dup_fingerprint != ''`, host)
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("mark duplicates: %w", err)
	}

	type member struct {
		id     int64
		source string
		grade  model.Grade
	}
	type cluster struct {
		members   []member
		first     time.Time
		estimated bool
	}
	open := map[string][]*cluster{}
	updates := map[int64]int64{} // duplicate id -> primary id

	closeCluster := func(c *cluster) {
		if len(c.members) < 2 {
			return
		}
		best := 0
		for i, m := range c.members {
			b := c.members[best]
			switch {
			case gradeRank(m.grade) != gradeRank(b.grade):
				if gradeRank(m.grade) > gradeRank(b.grade) {
					best = i
				}
			case sourceRank(m.source) != sourceRank(b.source):
				if sourceRank(m.source) > sourceRank(b.source) {
					best = i
				}
			case m.id < b.id:
				best = i
			}
		}
		for i, m := range c.members {
			if i != best {
				updates[m.id] = c.members[best].id
			}
		}
	}

	rows, err := s.db.QueryContext(ctx, `SELECT id, "ErrorDT", "ErrorSeverity", "OsPhysicalAddress", slot, label, source, grade, time_quality
 FROM event WHERE host = ? AND kind = 'error' AND dup_of IS NULL AND time_quality != 'relative'
 ORDER BY "ErrorDT", id`, host)
	if err != nil {
		return 0, fmt.Errorf("mark duplicates: %w", err)
	}
	for rows.Next() {
		var (
			id                                  int64
			dt, sev, slot, label, source, grade string
			quality                             string
			addr                                sql.NullInt64
		)
		if err := rows.Scan(&id, &dt, &sev, &addr, &slot, &label, &source, &grade, &quality); err != nil {
			rows.Close()
			return 0, err
		}
		class := model.Severity(sev).Class()
		if class == "" {
			continue
		}
		var key string
		switch {
		case addr.Valid:
			key = fmt.Sprintf("%s|a%x", class, uint64(addr.Int64)>>6)
		case slot != "":
			key = class + "|s" + slot
		case label != "":
			key = class + "|l" + model.NormalizeLabel(label)
		default:
			continue // nothing to match on
		}
		t := parseTS(sql.NullString{String: dt, Valid: true})
		est := model.TimeQuality(quality) != model.TimeExact

		// Drop clusters that are too old to take another member.
		cs := open[key]
		keep := cs[:0]
		for _, c := range cs {
			if t.Sub(c.first) > maxTol {
				closeCluster(c)
				continue
			}
			keep = append(keep, c)
		}
		var joined bool
		for _, c := range keep {
			limit := tol.Exact
			if est || c.estimated {
				limit = tol.Estimated
			}
			if t.Sub(c.first) > limit {
				continue
			}
			has := false
			for _, m := range c.members {
				if m.source == source {
					has = true
					break
				}
			}
			if has {
				continue
			}
			c.members = append(c.members, member{id, source, model.Grade(grade)})
			c.estimated = c.estimated || est
			joined = true
			break
		}
		if !joined {
			keep = append(keep, &cluster{members: []member{{id, source, model.Grade(grade)}}, first: t, estimated: est})
		}
		open[key] = keep
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("mark duplicates: %w", err)
	}
	for _, cs := range open {
		for _, c := range cs {
			closeCluster(c)
		}
	}

	err = s.Update(ctx, func(w *Writer) error {
		for dup, primary := range updates {
			if _, err := w.exec(`UPDATE event SET dup_of = ? WHERE id = ?`, primary, dup); err != nil {
				return err
			}
		}
		// A record declared as a duplicate of a record that is itself a
		// duplicate must point at the record that stays.
		for range 8 {
			res, err := w.exec(`UPDATE event SET dup_of = (SELECT p.dup_of FROM event p WHERE p.id = event.dup_of)
 WHERE host = ? AND dup_of IN (SELECT id FROM event WHERE host = ? AND dup_of IS NOT NULL)`, host, host)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return nil
			}
		}
		return errors.New("duplicate chain does not settle")
	})
	if err != nil {
		return 0, fmt.Errorf("mark duplicates: %w", err)
	}
	var n int
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM event WHERE host = ? AND dup_of IS NOT NULL`, host).Scan(&n)
	return n, err
}

// DeriveCounterEvents turns the counter readings of a host into counter
// events: one per reading and severity, carrying the growth since the
// previous reading of the same counter, or since the counter started when
// there is no earlier reading in the same counting period. The set of
// counter events is rebuilt from all readings, so importing an older reading
// later gives the same ledger as importing it first.
func (s *Store) DeriveCounterEvents(ctx context.Context, host string) error {
	type sample struct {
		at, resetAt     time.Time
		mc, dimm        int
		location, label string
		ce, ue          uint64
		source, rawRef  string
	}
	rows, err := s.db.QueryContext(ctx, `SELECT observed_at, reset_at, mc, dimm_index, location, label, ce, ue, source, raw_ref
 FROM counter_sample WHERE host = ? ORDER BY mc, dimm_index, observed_at`, host)
	if err != nil {
		return fmt.Errorf("derive counter events: %w", err)
	}
	var samples []sample
	for rows.Next() {
		var sm sample
		var at, reset sql.NullString
		var ce, ue int64
		if err := rows.Scan(&at, &reset, &sm.mc, &sm.dimm, &sm.location, &sm.label, &ce, &ue, &sm.source, &sm.rawRef); err != nil {
			rows.Close()
			return err
		}
		sm.at, sm.resetAt, sm.ce, sm.ue = parseTS(at), parseTS(reset), uint64(ce), uint64(ue)
		samples = append(samples, sm)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	boots, err := s.Boots(ctx, host)
	if err != nil {
		return err
	}
	bootStart := func(t time.Time) time.Time {
		var start time.Time
		for _, b := range boots {
			if b.StartQuality != model.TimeRelative && !b.StartedAt.After(t) {
				start = b.StartedAt
			}
		}
		return start
	}

	var want []*model.Event
	var prev *sample
	for i := range samples {
		cur := &samples[i]
		if prev != nil && (prev.mc != cur.mc || prev.dimm != cur.dimm) {
			prev = nil
		}
		// The previous reading belongs to the same counting period when the
		// counters were not reset in between and did not go backwards.
		samePeriod := prev != nil && cur.ce >= prev.ce && cur.ue >= prev.ue &&
			(cur.resetAt.IsZero() || !prev.at.Before(cur.resetAt))
		var dce, due uint64
		var windowStart time.Time
		if samePeriod {
			dce, due, windowStart = cur.ce-prev.ce, cur.ue-prev.ue, prev.at
		} else {
			dce, due = cur.ce, cur.ue
			windowStart = cur.resetAt
			if windowStart.IsZero() {
				windowStart = bootStart(cur.at)
			}
		}
		for _, d := range []struct {
			n   uint64
			sev model.Severity
		}{{dce, model.SevCE}, {due, model.SevUE}} {
			if d.n == 0 {
				continue
			}
			src := cur.source
			if src == "" {
				src = "counter"
			}
			want = append(want, &model.Event{
				Host: host, Kind: model.KindCounter, ErrorDT: cur.at, ErrorSeverity: d.sev,
				Source: src + "/counter", RawRef: cur.rawRef, Grade: model.GradeC,
				Count: int(d.n), TimeQuality: model.TimeExact, WindowStart: windowStart,
				EdacMC: model.Some(cur.mc), EdacLocation: cur.location, Label: cur.label,
				Fingerprint: model.Fingerprint("counter", host, fmt.Sprint(cur.mc), fmt.Sprint(cur.dimm), cur.at.UTC().Format(timeLayout), string(d.sev)),
			})
		}
		prev = cur
	}

	return s.Update(ctx, func(w *Writer) error {
		keep := map[string]bool{}
		for _, e := range want {
			keep[e.Fingerprint] = true
			inserted, err := w.InsertEvent(e)
			if err != nil {
				return fmt.Errorf("derive counter events: %w", err)
			}
			if !inserted {
				// The reading is known; its delta may have changed because
				// an earlier reading arrived later.
				if _, err := w.exec(`UPDATE event SET count = ?, covered = MIN(covered, ?), window_start = ? WHERE id = ?`,
					e.Count, e.Count, ts(e.WindowStart), e.ID); err != nil {
					return err
				}
			}
		}
		rows, err := w.tx.QueryContext(w.ctx, `SELECT id, fingerprint FROM event WHERE host = ? AND kind = 'counter'`, host)
		if err != nil {
			return err
		}
		var stale []int64
		for rows.Next() {
			var id int64
			var fp string
			if err := rows.Scan(&id, &fp); err != nil {
				rows.Close()
				return err
			}
			if !keep[fp] {
				stale = append(stale, id)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, id := range stale {
			if _, err := w.exec(`DELETE FROM event WHERE id = ?`, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// ReconcileCounters sets, for every counter event of a host, how many of its
// errors located error records already account for: the errors of the same
// severity class on the same slot inside the counting window. Totals count
// only the remainder, so a host that has both a rasdaemon database and EDAC
// counters is not counted twice, and errors that only the counter saw are
// still counted.
func (s *Store) ReconcileCounters(ctx context.Context, host string) error {
	type counter struct {
		id         int64
		count      int
		sev        model.Severity
		slot       string
		mc         sql.NullInt64
		start, end time.Time
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, count, "ErrorSeverity", slot, edac_mc, window_start, "ErrorDT"
 FROM event WHERE host = ? AND kind = 'counter' ORDER BY "ErrorDT", id`, host)
	if err != nil {
		return fmt.Errorf("reconcile counters: %w", err)
	}
	var counters []counter
	for rows.Next() {
		var c counter
		var sev string
		var start, end sql.NullString
		if err := rows.Scan(&c.id, &c.count, &sev, &c.slot, &c.mc, &start, &end); err != nil {
			rows.Close()
			return err
		}
		c.sev, c.start, c.end = model.Severity(sev), parseTS(start), parseTS(end)
		counters = append(counters, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	return s.Update(ctx, func(w *Writer) error {
		for _, c := range counters {
			sevs := `('CE')`
			if c.sev.Uncorrected() {
				sevs = `('Recoverable', 'UE', 'Fatal')`
			}
			q := `SELECT COALESCE(SUM(count), 0) FROM event
 WHERE host = ? AND kind = 'error' AND dup_of IS NULL AND "ErrorSeverity" IN ` + sevs + ` AND "ErrorDT" <= ?`
			args := []any{host, ts(c.end)}
			if !c.start.IsZero() {
				q, args = q+` AND "ErrorDT" > ?`, append(args, ts(c.start))
			}
			if c.slot != "" {
				q, args = q+` AND slot = ?`, append(args, c.slot)
			} else {
				// Controller-level counter of errors EDAC could not place.
				q, args = q+` AND slot = '' AND edac_mc IS ?`, append(args, c.mc)
			}
			var located int
			if err := w.queryRow(q, args...).Scan(&located); err != nil {
				return fmt.Errorf("reconcile counters: %w", err)
			}
			covered := min(located, c.count)
			if _, err := w.exec(`UPDATE event SET covered = ? WHERE id = ?`, covered, c.id); err != nil {
				return fmt.Errorf("reconcile counters: %w", err)
			}
		}
		return nil
	})
}
