package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
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
	// Records is the number of events that use the key, Declared the
	// number among them that their adapter declared to be a second report
	// of another record.
	Records  int64
	Declared int64
}

// EventLocations returns the distinct location keys of the events of a host.
func (s *Store) EventLocations(ctx context.Context, host string) ([]LocationKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT edac_mc, edac_location, smbios_handle, label, "SocketId", imc, "ChannelId", "ChannelSlotId", COUNT(*),
 COALESCE(SUM(dup_fingerprint != ''), 0)
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
		if err := rows.Scan(&mc, &k.EdacLocation, &handle, &k.Label, &socket, &imc, &ch, &pos, &k.Records, &k.Declared); err != nil {
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
	// BMC applies between a record of a BMC log and a record of the host's
	// own logs. The two were stamped by different clocks.
	BMC time.Duration
}

// DefaultDuplicateTolerance is used when a zero tolerance is passed.
var DefaultDuplicateTolerance = DuplicateTolerance{Exact: 5 * time.Second, Estimated: 2 * time.Minute, BMC: 2 * time.Minute}

// bmcSource reports whether a record comes from a log a BMC keeps. Such a
// record names a module at best and never an address.
func bmcSource(source string) bool {
	return strings.HasPrefix(source, "sel/") || strings.HasPrefix(source, "redfish/")
}

// reporter names what a record came through, for the rule that one error
// is at most one record of each. It is the source, except for the CXL
// tables of rasdaemon: a device reports an error as a DRAM event record or
// as a general media event record, never as both, so records of these
// tables are different errors however close they are in time.
func reporter(source string) string {
	if strings.HasPrefix(source, "rasdaemon/cxl_") {
		return "rasdaemon/cxl"
	}
	return source
}

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
// tolerance, and at most one record per source in a group. A record an
// adapter declared to be a second report counts as the record of its
// source in the group of the record it repeats. The CXL tables of
// rasdaemon count as one source. The record with the best observation
// grade stays; the others point at it through dup_of.
//
// A record of a BMC log is matched once more, because it has no address to
// match on: it is a duplicate when the host's own logs hold a record of the
// same severity class for the same slot within the BMC tolerance. Firmware
// logs such a record when it handles an error the operating system also
// reports, and often one for a whole series of corrected errors. A BMC
// record without a counterpart stays: the error that took the host down is
// in no other log.
//
// It returns the number of records marked as duplicates.
func (s *Store) MarkDuplicates(ctx context.Context, host string, tol DuplicateTolerance) (int, error) {
	if tol.Exact <= 0 {
		tol.Exact = DefaultDuplicateTolerance.Exact
	}
	if tol.Estimated <= 0 {
		tol.Estimated = DefaultDuplicateTolerance.Estimated
	}
	if tol.BMC <= 0 {
		tol.BMC = DefaultDuplicateTolerance.BMC
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
		// from holds the reporters the group has a record of: those of
		// its members, and those of the records an adapter declared to
		// be second reports of a member. Such a record is not a member,
		// and it is its source's report of the error all the same.
		from map[string]bool
	}

	// What the adapters declared: the sources of the second reports of
	// each record.
	declared := map[int64][]string{}
	drows, err := s.db.QueryContext(ctx, `SELECT dup_of, source FROM event WHERE host = ? AND dup_of IS NOT NULL`, host)
	if err != nil {
		return 0, fmt.Errorf("mark duplicates: %w", err)
	}
	for drows.Next() {
		var id int64
		var source string
		if err := drows.Scan(&id, &source); err != nil {
			drows.Close()
			return 0, err
		}
		declared[id] = append(declared[id], source)
	}
	drows.Close()
	if err := drows.Err(); err != nil {
		return 0, fmt.Errorf("mark duplicates: %w", err)
	}
	join := func(c *cluster, m member) {
		c.members = append(c.members, m)
		if c.from == nil {
			c.from = map[string]bool{}
		}
		c.from[reporter(m.source)] = true
		for _, src := range declared[m.id] {
			c.from[reporter(src)] = true
		}
	}
	open := map[string][]*cluster{}
	// What was found: pairs of a duplicate and the record that stays. A
	// host in an error storm has a pair for every second record, so they
	// are kept as a list; marked says which records of a BMC log are among
	// the duplicates, the only ones that are asked about again.
	type pair struct{ dup, primary int64 }
	var updates []pair
	marked := map[int64]bool{}

	// Records by severity class and slot, for the BMC match: the ones from
	// the host's own logs in time order, and the ones from a BMC log. The
	// host's own records are kept only when a BMC log has records at all.
	type stamped struct {
		id int64
		t  time.Time
	}
	hostLog := map[string][]stamped{}
	bmcLog := map[string][]stamped{}
	var withBMC bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM event WHERE host = ? AND kind = 'error' AND dup_of IS NULL
 AND (source LIKE 'sel/%' OR source LIKE 'redfish/%'))`, host).Scan(&withBMC); err != nil {
		return 0, fmt.Errorf("mark duplicates: %w", err)
	}

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
				updates = append(updates, pair{m.id, c.members[best].id})
				if bmcSource(m.source) {
					marked[m.id] = true
				}
			}
		}
	}

	rows, err := s.db.QueryContext(ctx, `SELECT id, "ErrorDT", "ErrorSeverity", "OsPhysicalAddress", slot, label, source, grade, time_quality
 FROM event WHERE host = ? AND kind = 'error' AND dup_of IS NULL AND time_quality != 'relative'
 ORDER BY "ErrorDT", id`, host)
	if err != nil {
		return 0, fmt.Errorf("mark duplicates: %w", err)
	}
	seen := 0
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
		if slot != "" && withBMC {
			k := class + "|" + slot
			if bmcSource(source) {
				bmcLog[k] = append(bmcLog[k], stamped{id, t})
			} else {
				hostLog[k] = append(hostLog[k], stamped{id, t})
			}
		}

		// The rows come in the order of their times. A group that is too
		// old for this row is too old for every row after it: such groups
		// are closed and dropped now and then, also those whose place no
		// later row names. Without that a group stays in memory for every
		// cache line a host ever reported an error in.
		if seen++; seen%8192 == 0 {
			for k, cs := range open {
				keep := cs[:0]
				for _, c := range cs {
					if t.Sub(c.first) > maxTol {
						closeCluster(c)
						continue
					}
					keep = append(keep, c)
				}
				if len(keep) == 0 {
					delete(open, k)
				} else {
					open[k] = keep
				}
			}
		}

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
			if c.from[reporter(source)] {
				continue
			}
			// A record that brings a second report of a source the group
			// already has is another error as well.
			clash := false
			for _, src := range declared[id] {
				if c.from[reporter(src)] {
					clash = true
					break
				}
			}
			if clash {
				continue
			}
			join(c, member{id, source, model.Grade(grade)})
			c.estimated = c.estimated || est
			joined = true
			break
		}
		if !joined {
			c := &cluster{first: t, estimated: est}
			join(c, member{id, source, model.Grade(grade)})
			keep = append(keep, c)
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

	// BMC records that are still counted: the nearest record of the host's
	// own logs for the same slot, if one is near enough, reports the same
	// error with more detail.
	for k, recs := range bmcLog {
		host := hostLog[k]
		if len(host) == 0 {
			continue
		}
		for _, b := range recs {
			if marked[b.id] {
				continue
			}
			i := sort.Search(len(host), func(i int) bool { return !host[i].t.Before(b.t) })
			best, bestDiff := int64(0), tol.BMC+1
			for _, j := range []int{i - 1, i} {
				if j < 0 || j >= len(host) {
					continue
				}
				if d := host[j].t.Sub(b.t).Abs(); d < bestDiff {
					best, bestDiff = host[j].id, d
				}
			}
			if best != 0 {
				updates = append(updates, pair{b.id, best})
			}
		}
	}

	err = s.Update(ctx, func(w *Writer) error {
		for _, u := range updates {
			if _, err := w.exec(`UPDATE event SET dup_of = ? WHERE id = ?`, u.primary, u.dup); err != nil {
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
// previous reading of the same counter of the same source, or since the
// counter started when there is no earlier reading in the same counting
// period. A reading that says how many of its errors are recent gives two
// events per severity: the recent errors within their window, and the rest.
// The set of counter events is rebuilt from all readings, so importing an
// older reading later gives the same ledger as importing it first.
func (s *Store) DeriveCounterEvents(ctx context.Context, host string) error {
	type sample struct {
		at, resetAt               time.Time
		source                    string
		mc, dimm                  int
		location, label           string
		socket, channel, position model.Opt[int]
		ce, ue                    uint64
		recentCE, recentUE        model.Opt[uint64]
		ceWindow, ueWindow        time.Duration
		rawRef                    string
	}
	rows, err := s.db.QueryContext(ctx, `SELECT observed_at, reset_at, source, mc, dimm_index, location, label, "SocketId", "ChannelId", "ChannelSlotId",
 ce, ue, recent_ce, recent_ce_seconds, recent_ue, recent_ue_seconds, raw_ref
 FROM counter_sample WHERE host = ? ORDER BY source, mc, dimm_index, observed_at`, host)
	if err != nil {
		return fmt.Errorf("derive counter events: %w", err)
	}
	var samples []sample
	for rows.Next() {
		var sm sample
		var at, reset sql.NullString
		var ce, ue int64
		var socket, channel, position, rce, rceSec, rue, rueSec sql.NullInt64
		if err := rows.Scan(&at, &reset, &sm.source, &sm.mc, &sm.dimm, &sm.location, &sm.label, &socket, &channel, &position,
			&ce, &ue, &rce, &rceSec, &rue, &rueSec, &sm.rawRef); err != nil {
			rows.Close()
			return err
		}
		sm.at, sm.resetAt, sm.ce, sm.ue = parseTS(at), parseTS(reset), uint64(ce), uint64(ue)
		sm.socket, sm.channel, sm.position = toOptInt(socket), toOptInt(channel), toOptInt(position)
		if rce.Valid && rceSec.Valid && rce.Int64 >= 0 && rceSec.Int64 > 0 {
			sm.recentCE, sm.ceWindow = model.Some(uint64(rce.Int64)), time.Duration(rceSec.Int64)*time.Second
		}
		if rue.Valid && rueSec.Valid && rue.Int64 >= 0 && rueSec.Int64 > 0 {
			sm.recentUE, sm.ueWindow = model.Some(uint64(rue.Int64)), time.Duration(rueSec.Int64)*time.Second
		}
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
		if prev != nil && (prev.source != cur.source || prev.mc != cur.mc || prev.dimm != cur.dimm) {
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
				// A counter cannot have started before the boot it runs in.
				windowStart = bootStart(cur.at)
			}
		}
		src := cur.source
		if src == "" {
			src = "counter"
		}
		emit := func(n uint64, sev model.Severity, from time.Time, part string) {
			if n > math.MaxInt32 {
				n = math.MaxInt32
			}
			e := &model.Event{
				Host: host, Kind: model.KindCounter, ErrorDT: cur.at, ErrorSeverity: sev,
				Source: src + "/counter", RawRef: cur.rawRef, Grade: model.GradeC,
				Count: int(n), TimeQuality: model.TimeExact, WindowStart: from, Label: cur.label,
			}
			if cur.socket.Valid {
				e.SocketId, e.ChannelId, e.ChannelSlotId = cur.socket, cur.channel, cur.position
			} else {
				e.EdacMC, e.EdacLocation = model.Some(cur.mc), cur.location
			}
			id := []string{"counter", host, cur.source, fmt.Sprint(cur.mc), fmt.Sprint(cur.dimm), cur.at.UTC().Format(timeLayout), string(sev)}
			if part != "" {
				id = append(id, part)
			}
			e.Fingerprint = model.Fingerprint(id...)
			want = append(want, e)
		}
		for _, d := range []struct {
			n      uint64
			sev    model.Severity
			recent model.Opt[uint64]
			window time.Duration
		}{{dce, model.SevCE, cur.recentCE, cur.ceWindow}, {due, model.SevUE, cur.recentUE, cur.ueWindow}} {
			rest := d.n
			// The recent errors are told apart where their window is
			// narrower than the one the growth was counted over.
			if from := cur.at.Add(-d.window); d.recent.Valid && d.recent.V > 0 && d.window > 0 && (windowStart.IsZero() || from.After(windowStart)) {
				recent := min(d.recent.V, rest)
				if recent > 0 {
					emit(recent, d.sev, from, "recent")
				}
				rest -= recent
			}
			if rest > 0 {
				emit(rest, d.sev, windowStart, "")
			}
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

// counterRank orders the sources of counter events for ReconcileCounters:
// the counters of the kernel first, the counts other programs keep after
// them in the order of their names.
func counterRank(source string) int {
	if source == "edac-sysfs/counter" {
		return 0
	}
	return 1
}

// ReconcileCounters sets, for every counter event of a host, how many of its
// errors the ledger already counts: the located error records of the same
// severity class on the same slot inside the counting window, and what the
// counters of another source count beyond those. Totals count only the
// remainder, so a host that has both a rasdaemon database and EDAC counters
// is not counted twice, a host that has the counters of two sources neither,
// and errors that only a counter saw are still counted.
//
// The sources are taken in a fixed order, the counters of the kernel first.
// What an earlier source counts beyond the located records covers the
// events of a later source whose windows overlap its own, each error once
// per later source. Two events of one source whose windows lie inside one
// another (the recent errors of a reading and the rest of it, or two rank
// counters of one DIMM) share the located records of the common window:
// what covers the narrower event does not cover the wider one again.
func (s *Store) ReconcileCounters(ctx context.Context, host string) error {
	type counter struct {
		id         int64
		count      int
		sev        model.Severity
		slot       string
		source     string
		mc, socket sql.NullInt64
		start, end time.Time
		// located is how many of the errors located records account for,
		// spare how many the event counts beyond everything before it, and
		// taken how many of those a later source has been covered by.
		located, spare int
		taken          map[string]int
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, count, "ErrorSeverity", slot, source, edac_mc, "SocketId", window_start, "ErrorDT"
 FROM event WHERE host = ? AND kind = 'counter' ORDER BY "ErrorDT", id`, host)
	if err != nil {
		return fmt.Errorf("reconcile counters: %w", err)
	}
	var counters []*counter
	for rows.Next() {
		c := &counter{taken: map[string]int{}}
		var sev string
		var start, end sql.NullString
		if err := rows.Scan(&c.id, &c.count, &sev, &c.slot, &c.source, &c.mc, &c.socket, &start, &end); err != nil {
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
	// Earlier sources first; within a source the narrower window first, so
	// that what it took from the located records is known when the wider
	// window around it is looked at.
	length := func(c *counter) time.Duration {
		if c.start.IsZero() {
			return time.Duration(math.MaxInt64)
		}
		return c.end.Sub(c.start)
	}
	sort.SliceStable(counters, func(i, j int) bool {
		a, b := counters[i], counters[j]
		if ra, rb := counterRank(a.source), counterRank(b.source); ra != rb {
			return ra < rb
		}
		if a.source != b.source {
			return a.source < b.source
		}
		return length(a) < length(b)
	})
	// same reports whether two events count errors of the same place and
	// severity class. Errors that are tied to no slot are told apart by
	// their memory controller or socket within one source; across sources
	// the two numberings do not compare, and such errors are one pool.
	same := func(a, b *counter) bool {
		if a.sev.Uncorrected() != b.sev.Uncorrected() || a.slot != b.slot {
			return false
		}
		return a.slot != "" || a.source != b.source || (a.mc == b.mc && a.socket == b.socket)
	}
	// overlaps reports whether the windows of two events share some time.
	overlaps := func(a, b *counter) bool {
		return (a.start.IsZero() || a.start.Before(b.end)) && (b.start.IsZero() || b.start.Before(a.end))
	}
	// within reports whether the window of a lies inside the window of b.
	within := func(a, b *counter) bool {
		return !a.end.After(b.end) && (b.start.IsZero() || (!a.start.IsZero() && !a.start.Before(b.start)))
	}
	return s.Update(ctx, func(w *Writer) error {
		for i, c := range counters {
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
			switch {
			case c.slot != "":
				q, args = q+` AND slot = ?`, append(args, c.slot)
			case c.socket.Valid:
				// What a source that counts per socket could not place.
				q, args = q+` AND slot = '' AND "SocketId" IS ?`, append(args, c.socket)
			default:
				// Controller-level counter of errors EDAC could not place.
				q, args = q+` AND slot = '' AND edac_mc IS ?`, append(args, c.mc)
			}
			var located int
			if err := w.queryRow(q, args...).Scan(&located); err != nil {
				return fmt.Errorf("reconcile counters: %w", err)
			}
			for _, d := range counters[:i] {
				if d.source == c.source && same(c, d) && within(d, c) {
					located -= d.located
				}
			}
			c.located = max(min(located, c.count), 0)
			need := c.count - c.located
			for _, d := range counters[:i] {
				if need == 0 {
					break
				}
				if d.source == c.source || !same(c, d) || !overlaps(c, d) {
					continue
				}
				take := min(need, d.spare-d.taken[c.source])
				if take > 0 {
					d.taken[c.source] += take
					need -= take
				}
			}
			c.spare = need
			if _, err := w.exec(`UPDATE event SET covered = ? WHERE id = ?`, c.count-need, c.id); err != nil {
				return fmt.Errorf("reconcile counters: %w", err)
			}
		}
		return nil
	})
}
