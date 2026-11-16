package engine

import (
	"fmt"
	"github.com/agenticode/my/internal/i18n"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agenticode/my/internal/model"
)

// Recurrence across boots. A fatal memory error ends its boot; on a hard
// fault every boot ends the same way. The engine looks at the latest
// loop.window_boots boots of a host and asks, per DIMM, in which boots it
// had errors and at which places, and per host, which boots ended in a
// stop and on which DIMM.

// BootSpan is one boot of the window.
type BootSpan struct {
	// Index counts the boots of the window from 1, oldest first.
	Index        int       `json:"index"`
	BootID       string    `json:"boot_id"`
	StartedAt    time.Time `json:"started_at"`
	StartQuality string    `json:"start_quality"`
	// RanSeconds is how long the boot ran: from its start to the last
	// time it was seen.
	RanSeconds int64 `json:"ran_seconds,omitzero"`
	// End is "stop", "panic", "running" for the latest boot of a host
	// that was scanned while it ran, or "" when nothing tells.
	End string `json:"end"`
	// EndRecord names the record that shows how the boot ended.
	EndRecord string `json:"end_record,omitzero"`
	// StopSlot names the slot of the DIMM whose uncorrected error was in
	// a boot that stopped, when exactly one DIMM had one.
	StopSlot string `json:"stop_slot,omitzero"`
	last     time.Time
	endAt    time.Time
	bios     string
}

// BootCell is what one DIMM, or one place of it, showed in one boot.
type BootCell struct {
	Boot int   `json:"boot"`
	CE   int64 `json:"ce,omitzero"`
	UE   int64 `json:"ue,omitzero"`
	// Stopped says that the boot ended in a stop and that this DIMM had an
	// uncorrected error in it.
	Stopped bool `json:"stopped,omitzero"`
}

// Place levels: what the records of a place say about where it is.
const (
	PlaceCell    = "cell"      // rank, device, bank group, bank, row and column (grade A)
	PlaceAddress = "address"   // a cache line of the physical address (grade B)
	PlaceDIMM    = "dimm-only" // the module only (grade C)
	// The lines R-LOOP-01 also takes as one place: a row or a column of
	// a bank (grade A), and addresses within cluster.row_window_bytes,
	// taken to be one row (grade B, estimated).
	PlaceRow     = "row"
	PlaceColumn  = "column"
	PlaceNearRow = "address-row"
)

// placeRank orders the levels of a place from the finest.
func placeRank(level string) int {
	switch level {
	case PlaceCell:
		return 0
	case PlaceRow, PlaceColumn:
		return 1
	case PlaceAddress:
		return 2
	case PlaceNearRow:
		return 3
	}
	return 4
}

// PlaceBoots is one place of a DIMM boot by boot.
type PlaceBoots struct {
	Level string `json:"level"`
	// Place names the place: "rank0 dev7 bg0 bank3 row 0x1a2f col 0x238",
	// an address, or "" for the module.
	Place           string     `json:"place"`
	Cells           []BootCell `json:"cells"`
	BootsWithErrors int        `json:"boots_with_errors"`
	BootsWithUE     int        `json:"boots_with_ue"`
	CE              int64      `json:"ce"`
	UE              int64      `json:"ue"`
	events          []int32
	ueBoots         []int
	// from and to bound the addresses of an address row.
	from, to uint64
}

// DIMMBoots is one DIMM boot by boot.
type DIMMBoots struct {
	UID             string       `json:"dimm_uid"`
	Slot            string       `json:"slot"`
	Cells           []BootCell   `json:"cells"`
	BootsWithErrors int          `json:"boots_with_errors"`
	BootsWithUE     int          `json:"boots_with_ue"`
	StopBoots       int          `json:"stop_boots"`
	Places          []PlaceBoots `json:"places,omitzero"`
	d               *DIMMResult
	ueBoots         map[int]bool
	// lines are the rows, columns and address rows of the uncorrected
	// errors, for R-LOOP-01.
	lines []PlaceBoots
}

// Recurrence is what the boots of the window of a host show.
type Recurrence struct {
	Boots []BootSpan `json:"boots"`
	// OnRecord is the number of boots of the host on record; the window
	// holds the latest of them.
	OnRecord int `json:"boots_on_record"`
	// Stops counts the boots of the window that a hardware error stopped,
	// Panics those that ended in another panic, and Streak the latest
	// boots in a row that ended in a stop.
	Stops  int `json:"stops"`
	Panics int `json:"panics,omitzero"`
	Streak int `json:"streak,omitzero"`
	// Latest is the index of the latest boot that ended in a stop, or 0.
	Latest int `json:"latest_stop,omitzero"`
	// Where says where the uncorrected errors of the window lie: the rule
	// that fired (R-LOOP-01, R-LOOP-02, R-LOOP-03), "dimm" for a single
	// DIMM below the rules, "none" when no record names a DIMM, or "".
	Where string `json:"where,omitzero"`
	// Statements say it in sentences, for the stops line of a view.
	Statements []model.Statement `json:"statements,omitzero"`
	DIMMs      []*DIMMBoots      `json:"dimms,omitzero"`
	// Loose are the places of uncorrected errors that name no DIMM, boot
	// by boot.
	Loose []PlaceBoots `json:"loose,omitzero"`
	// What R-LOOP-03 says for the host finding.
	spread []model.Statement
}

// Window returns the number of boots in the window.
func (r *Recurrence) Window() int { return len(r.Boots) }

// DIMM returns the boots of one DIMM, or nil.
func (r *Recurrence) DIMM(uid string) *DIMMBoots {
	if r == nil {
		return nil
	}
	for _, d := range r.DIMMs {
		if d.UID == uid {
			return d
		}
	}
	return nil
}

// loopView computes the recurrence of every host and the evidence of the
// R-LOOP rules.
type loopView struct {
	fired  map[*DIMMResult][]model.Evidence
	checks map[*DIMMResult][]model.Check
	missed map[*DIMMResult][]model.Statement
	byHost map[string]*Recurrence
	// faultBoots counts, per fault, the boots of the window it had errors
	// in.
	faultBoots map[*Fault]int
}

// placeKey identifies a place of a DIMM at the level its records give.
type placeKey struct {
	level                string
	rank, dev, bg, bank  int16
	row, col             int32
	line                 uint64
	hasRank, hasDev, hBG bool
}

func placeOf(e *event) placeKey {
	switch {
	case e.located():
		return placeKey{level: PlaceCell, rank: e.rank, dev: e.dev, bg: e.bg, bank: e.bank, row: e.row, col: e.col}
	case e.hasAddr:
		return placeKey{level: PlaceAddress, line: e.addr >> 6}
	}
	return placeKey{level: PlaceDIMM}
}

func (k placeKey) text() string {
	switch k.level {
	case PlaceCell:
		var parts []string
		if k.rank >= 0 {
			parts = append(parts, fmt.Sprintf("rank%d", k.rank))
		}
		if k.dev >= 0 {
			parts = append(parts, fmt.Sprintf("dev%d", k.dev))
		}
		if k.bg >= 0 {
			parts = append(parts, fmt.Sprintf("bg%d", k.bg))
		}
		if k.bank >= 0 {
			parts = append(parts, fmt.Sprintf("bank%d", k.bank))
		}
		parts = append(parts, fmt.Sprintf("row 0x%x col 0x%x", k.row, k.col))
		return strings.Join(parts, " ")
	case PlaceAddress:
		return fmt.Sprintf("0x%x", k.line<<6)
	}
	return ""
}

// newLoopView looks at the boots of every host.
func newLoopView(j *judge, hosts map[string]*HostResult, clocks map[string]clock) *loopView {
	v := &loopView{fired: map[*DIMMResult][]model.Evidence{}, checks: map[*DIMMResult][]model.Check{},
		missed: map[*DIMMResult][]model.Statement{}, byHost: map[string]*Recurrence{}, faultBoots: map[*Fault]int{}}
	names := make([]string, 0, len(hosts))
	for name := range hosts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		v.host(j, name, hosts[name], clocks[name])
	}
	return v
}

func (v *loopView) host(j *judge, name string, h *HostResult, clk clock) {
	in, cfg := j.in, j.cfg
	hi, ok := in.hostIdx[name]
	if !ok {
		return
	}
	var boots []model.Boot
	for _, b := range in.Boots {
		if b.Host != name || b.StartQuality == model.TimeRelative {
			continue
		}
		if clk.asOf > 0 && b.StartedAt.UnixMicro() > clk.asOf {
			continue
		}
		boots = append(boots, b)
	}
	sort.SliceStable(boots, func(a, b int) bool {
		if !boots[a].StartedAt.Equal(boots[b].StartedAt) {
			return boots[a].StartedAt.Before(boots[b].StartedAt)
		}
		return boots[a].BootID < boots[b].BootID
	})
	r := &Recurrence{OnRecord: len(boots), Boots: []BootSpan{}}
	if len(boots) > cfg.LoopWindowBoots {
		boots = boots[len(boots)-cfg.LoopWindowBoots:]
	}
	pos := map[int32]int{} // boot index of Input -> position in the window
	for i, b := range boots {
		span := BootSpan{Index: i + 1, BootID: b.BootID, StartedAt: b.StartedAt, StartQuality: string(b.StartQuality),
			End: b.End, EndRecord: b.EndRef, last: b.LastSeen, endAt: b.EndAt, bios: b.BIOS}
		// How long the boot ran: until the record of its end, or until
		// it was last seen.
		until := b.LastSeen
		if b.End != "" && !b.EndAt.IsZero() && b.EndAt.After(b.StartedAt) {
			until = b.EndAt
		}
		if !until.IsZero() && until.After(b.StartedAt) && b.StartQuality != "unknown" {
			span.RanSeconds = int64(until.Sub(b.StartedAt) / time.Second)
		}
		if bi, ok := in.bootIdx[bootKey{hi, b.BootID}]; ok {
			pos[bi] = i
		}
		r.Boots = append(r.Boots, span)
	}
	if len(r.Boots) > 0 && r.Boots[len(r.Boots)-1].End == "" && j.hosts[name] != nil && j.hosts[name].live {
		r.Boots[len(r.Boots)-1].End = "running"
	}

	// Per DIMM and boot: the errors, and the places at the finest level
	// the records give.
	evs := in.events
	for _, d := range h.DIMMs {
		db := &DIMMBoots{UID: d.UID, Slot: d.Slot, d: d, ueBoots: map[int]bool{}}
		cells := map[int]*BootCell{}
		places := map[placeKey]*PlaceBoots{}
		pcells := map[placeKey]map[int]*BootCell{}
		for _, i := range d.events {
			e := &evs[i]
			p, ok := pos[e.boot]
			if !ok || e.counter {
				continue
			}
			c := cells[p]
			if c == nil {
				c = &BootCell{Boot: p + 1}
				cells[p] = c
			}
			k := placeOf(e)
			pb := places[k]
			if pb == nil {
				pb = &PlaceBoots{Level: k.level, Place: k.text()}
				places[k] = pb
				pcells[k] = map[int]*BootCell{}
			}
			pc := pcells[k][p]
			if pc == nil {
				pc = &BootCell{Boot: p + 1}
				pcells[k][p] = pc
			}
			pb.events = append(pb.events, i)
			if e.sev == sevCE {
				c.CE += int64(e.errors)
				pc.CE += int64(e.errors)
				pb.CE += int64(e.errors)
			} else {
				c.UE += int64(e.errors)
				pc.UE += int64(e.errors)
				pb.UE += int64(e.errors)
				db.ueBoots[p] = true
			}
		}
		if len(cells) == 0 {
			continue
		}
		for _, p := range bootKeys(cells) {
			c := cells[p]
			if c.UE > 0 && r.Boots[p].End == model.BootStop {
				c.Stopped = true
				db.StopBoots++
			}
			db.Cells = append(db.Cells, *c)
		}
		db.BootsWithErrors = len(db.Cells)
		db.BootsWithUE = len(db.ueBoots)
		for k, pb := range places {
			for _, p := range bootKeys(pcells[k]) {
				c := *pcells[k][p]
				c.Stopped = c.UE > 0 && r.Boots[p].End == model.BootStop
				pb.Cells = append(pb.Cells, c)
				if c.UE > 0 {
					pb.BootsWithUE++
					pb.ueBoots = append(pb.ueBoots, p)
				}
			}
			pb.BootsWithErrors = len(pb.Cells)
			db.Places = append(db.Places, *pb)
		}
		sort.SliceStable(db.Places, func(a, b int) bool {
			x, y := &db.Places[a], &db.Places[b]
			if x.BootsWithErrors != y.BootsWithErrors {
				return x.BootsWithErrors > y.BootsWithErrors
			}
			if x.CE+x.UE != y.CE+y.UE {
				return x.CE+x.UE > y.CE+y.UE
			}
			return x.Place < y.Place
		})
		db.lines = lines(evs, d.events, pos, j.cfg.RowWindowBytes)
		r.DIMMs = append(r.DIMMs, db)
		// The boots each fault of the DIMM had errors in.
		for _, f := range d.Faults {
			seen := map[int]bool{}
			for _, m := range f.members {
				if p, ok := pos[evs[m].boot]; ok {
					seen[p] = true
				}
			}
			v.faultBoots[f] = len(seen)
		}
	}
	sort.SliceStable(r.DIMMs, func(a, b int) bool {
		x, y := r.DIMMs[a], r.DIMMs[b]
		if x.BootsWithUE != y.BootsWithUE {
			return x.BootsWithUE > y.BootsWithUE
		}
		if x.BootsWithErrors != y.BootsWithErrors {
			return x.BootsWithErrors > y.BootsWithErrors
		}
		if x.Slot != y.Slot {
			return x.Slot < y.Slot
		}
		return x.UID < y.UID
	})

	// Uncorrected errors that name no DIMM, by address and boot.
	loose := map[placeKey]*PlaceBoots{}
	looseBoots := map[placeKey]map[int]bool{}
	for i := range evs {
		e := &evs[i]
		if e.host != hi || e.dimm >= 0 || e.sev == sevCE || e.counter {
			continue
		}
		p, ok := pos[e.boot]
		if !ok {
			continue
		}
		k := placeOf(e)
		if loose[k] == nil {
			loose[k] = &PlaceBoots{Level: k.level, Place: k.text()}
			looseBoots[k] = map[int]bool{}
		}
		loose[k].UE += int64(e.errors)
		loose[k].events = append(loose[k].events, int32(i))
		looseBoots[k][p] = true
	}
	for k, pb := range loose {
		for _, p := range bootKeys(looseBoots[k]) {
			pb.Cells = append(pb.Cells, BootCell{Boot: p + 1, UE: 1, Stopped: r.Boots[p].End == model.BootStop})
			pb.ueBoots = append(pb.ueBoots, p)
		}
		pb.BootsWithErrors, pb.BootsWithUE = len(pb.Cells), len(pb.Cells)
		r.Loose = append(r.Loose, *pb)
	}
	sort.SliceStable(r.Loose, func(a, b int) bool {
		if r.Loose[a].BootsWithUE != r.Loose[b].BootsWithUE {
			return r.Loose[a].BootsWithUE > r.Loose[b].BootsWithUE
		}
		return r.Loose[a].Place < r.Loose[b].Place
	})

	// Which DIMM each stop was on: the one DIMM with an uncorrected error
	// in the boot.
	for p := range r.Boots {
		b := &r.Boots[p]
		switch b.End {
		case model.BootStop:
			r.Stops++
			r.Latest = b.Index
		case model.BootPanic:
			r.Panics++
		}
		if b.End != model.BootStop {
			continue
		}
		var on []string
		for _, db := range r.DIMMs {
			if db.ueBoots[p] {
				on = append(on, db.Slot)
			}
		}
		if len(on) == 1 {
			b.StopSlot = on[0]
		}
	}
	for p := len(r.Boots) - 1; p >= 0; p-- {
		switch r.Boots[p].End {
		case "running":
			continue
		case model.BootStop:
			r.Streak++
			continue
		}
		break
	}
	v.byHost[name] = r
	h.Recurrence = r
	v.rules(j, name, r)
}

// rules fires R-LOOP-01, R-LOOP-02 and R-LOOP-03 for the DIMMs of a host,
// and adds the checks of the boots.
func (v *loopView) rules(j *judge, host string, r *Recurrence) {
	cfg := j.cfg
	window := len(r.Boots)
	if window < 2 {
		return
	}
	// The boots with an uncorrected error tied to a DIMM, and which DIMMs.
	ueBoots := map[int][]*DIMMBoots{}
	for _, db := range r.DIMMs {
		for p := range db.ueBoots {
			ueBoots[p] = append(ueBoots[p], db)
		}
	}
	loop := map[*DIMMBoots]string{}
	tops := map[*DIMMBoots]*PlaceBoots{}
	for _, db := range r.DIMMs {
		if db.BootsWithUE < cfg.LoopMinBoots {
			continue
		}
		stopped := "@word.loop_none_stopped"
		if db.StopBoots == db.BootsWithUE {
			stopped = "@word.loop_each_stopped"
		} else if db.StopBoots > 0 {
			stopped = "@word.loop_some_stopped"
		}
		// The place with the most boots of uncorrected errors: a cell or
		// an address, or a row, a column or an address row they lie in;
		// the finer one of two with as many boots.
		var top *PlaceBoots
		consider := func(p *PlaceBoots) {
			if p.Level == PlaceDIMM {
				return
			}
			if top == nil || p.BootsWithUE > top.BootsWithUE || (p.BootsWithUE == top.BootsWithUE && placeRank(p.Level) < placeRank(top.Level)) {
				top = p
			}
		}
		for i := range db.Places {
			consider(&db.Places[i])
		}
		for i := range db.lines {
			consider(&db.lines[i])
		}
		if top != nil && top.BootsWithUE >= cfg.LoopMinBoots {
			st := statement("loop.place", "where", top.Place, "n", top.BootsWithUE, "of", window, "stopped", stopped)
			if top.Level == PlaceNearRow {
				st = statement("loop.place_near", "from", fmt.Sprintf("0x%x", top.from), "to", fmt.Sprintf("0x%x", top.to),
					"n", top.BootsWithUE, "of", window, "stopped", stopped)
			}
			v.fire(j, db.d, RuleLoopPlace, st, top.events)
			loop[db], tops[db] = RuleLoopPlace, top
			continue
		}
		var all []int32
		places := map[string]bool{}
		for _, p := range db.Places {
			if p.BootsWithUE > 0 {
				all = append(all, p.events...)
				if p.Level != PlaceDIMM {
					places[p.Place] = true
				}
			}
		}
		switch len(places) {
		case 0:
			v.fire(j, db.d, RuleLoopDIMM, statement("loop.dimm_only", "n", db.BootsWithUE, "of", window, "stopped", stopped), all)
		case 1:
			v.fire(j, db.d, RuleLoopDIMM, statement("loop.dimm_partly", "n", db.BootsWithUE, "of", window, "stopped", stopped), all)
		default:
			v.fire(j, db.d, RuleLoopDIMM, statement("loop.dimm_places", "n", db.BootsWithUE, "of", window, "places", len(places), "stopped", stopped), all)
		}
		loop[db] = RuleLoopDIMM
	}

	// R-LOOP-03: uncorrected errors in several boots, at several DIMMs,
	// none of which has more than its share of the boots.
	spread := false
	if len(ueBoots) >= cfg.LoopScatterBoots {
		most := 0
		for _, db := range r.DIMMs {
			most = max(most, db.BootsWithUE)
		}
		if float64(most) <= cfg.LoopScatterShare*float64(len(ueBoots)) {
			spread = true
			var involved []*DIMMBoots
			for _, db := range r.DIMMs {
				if db.BootsWithUE > 0 {
					involved = append(involved, db)
				}
			}
			shared := sharedPlace(j, host, involved)
			slots := make([]string, len(involved))
			for i, db := range involved {
				slots[i] = db.Slot
			}
			r.spread = []model.Statement{
				statement("loop.spread_host", "n", len(ueBoots), "of", window, "dimms", len(involved), "slots", slots, "most", most), shared}
			for _, db := range involved {
				var all []int32
				for _, p := range db.Places {
					if p.BootsWithUE > 0 {
						all = append(all, p.events...)
					}
				}
				v.fire(j, db.d, RuleLoopSpread, statement("loop.spread", "n", len(ueBoots), "of", window, "dimms", len(involved), "most", most), all)
				v.fire(j, db.d, RuleLoopSpread, shared, nil)
				// A DIMM with a confined fault of its own from before the
				// first boot of the spread speaks against it.
				first := firstBoot(ueBoots)
				for _, f := range db.d.Faults {
					if f.Confined && first < len(r.Boots) && f.FirstSeen.Before(r.Boots[first].StartedAt) {
						v.check(db.d, model.EffectWeakens, statement("check.loop_own_fault", "slot", db.Slot, "where", f.Where()))
						break
					}
				}
			}
			r.Where = RuleLoopSpread
		}
	}

	// Which rule says where the uncorrected errors are.
	if !spread {
		for _, db := range r.DIMMs {
			if rule, ok := loop[db]; ok && (r.Where == "" || rule == RuleLoopPlace) {
				r.Where = rule
			}
		}
	}
	if r.Where == "" && len(ueBoots) > 0 {
		r.Where = "dimm"
	}
	if r.Where == "" && r.Stops > 0 {
		r.Where = "none"
	}

	for _, db := range r.DIMMs {
		rule, fired := loop[db]
		switch {
		case fired && rule == RuleLoopPlace:
			top := tops[db]
			// No other DIMM had an uncorrected error in these boots.
			alone := true
			for _, p := range top.ueBoots {
				if len(ueBoots[p]) > 1 {
					alone = false
				}
			}
			if alone {
				v.check(db.d, model.EffectSupports, statement("check.loop_alone"))
			}
			switch top.Level {
			case PlaceCell, PlaceRow, PlaceColumn:
				// A contact or a trace corrupts whatever address is read.
				v.check(db.d, model.EffectSupports, statement("check.loop_confined"))
			case PlaceAddress, PlaceNearRow:
				// An address is a place only as long as the map from
				// addresses to cells stays the same.
				if v.changedBetween(j, r, db.d.Host, top.ueBoots) {
					v.check(db.d, model.EffectWeakens, statement("check.loop_changed"))
				}
			}
		case fired && rule == RuleLoopDIMM:
			v.check(db.d, model.EffectOpen, statement("check.loop_not_ruled_out"))
		case !fired && !spread && db.BootsWithUE == 1:
			// Two boots on two DIMMs: no rule, and each says the other.
			for p := range db.ueBoots {
				for _, q := range bootKeys(ueBoots) {
					if q == p {
						continue
					}
					for _, o := range ueBoots[q] {
						if o != db {
							v.check(db.d, model.EffectWeakens, statement("check.loop_other", "slot", o.Slot, "boot", q+1, "of", window))
						}
					}
				}
			}
		}
	}

	// The stops line, in sentences.
	if r.Stops > 0 {
		latest := r.Boots[r.Latest-1]
		// The time of the record that shows the stop, or the last time
		// the boot was seen, and how long the boot ran until then.
		when := latest.endAt
		if when.IsZero() || when.Before(latest.StartedAt) {
			when = latest.last
		}
		if when.IsZero() {
			when = latest.StartedAt
		}
		if s := int64(when.Sub(latest.StartedAt) / time.Second); s > 0 && latest.StartQuality != "unknown" {
			n, unit := Duration(s)
			r.Statements = append(r.Statements, statement("loop.stops", "n", r.Stops, "of", window, "when", stampText(when.UnixMicro()), "ran_n", n, "ran_unit", "@"+unit))
		} else {
			r.Statements = append(r.Statements, statement("loop.stops_when", "n", r.Stops, "of", window, "when", stampText(when.UnixMicro())))
		}
		switch r.Where {
		case RuleLoopPlace, RuleLoopDIMM:
			for _, db := range r.DIMMs {
				if loop[db] != "" {
					r.Statements = append(r.Statements, statement("loop.where_dimm", "slot", db.Slot, "n", db.StopBoots, "rule", loop[db]))
					break
				}
			}
		case RuleLoopSpread:
			var slots []string
			for _, db := range r.DIMMs {
				if db.BootsWithUE > 0 {
					slots = append(slots, db.Slot)
				}
			}
			r.Statements = append(r.Statements, statement("loop.where_spread", "dimms", len(slots), "slots", slots))
		case "dimm":
			var few []*DIMMBoots
			for _, db := range r.DIMMs {
				if db.BootsWithUE > 0 {
					few = append(few, db)
				}
			}
			if len(few) == 1 {
				r.Statements = append(r.Statements, statement("loop.where_one", "slot", few[0].Slot, "n", few[0].BootsWithUE))
			} else {
				slots := make([]string, len(few))
				for i, db := range few {
					slots[i] = db.Slot
				}
				r.Statements = append(r.Statements, statement("loop.where_few", "dimms", len(few), "slots", slots))
			}
		default:
			r.Statements = append(r.Statements, statement("loop.where_none"))
		}
	}
}

// changedBetween reports whether the BIOS version or the set of DIMMs of
// the host changed between the first and the last of the given boots: one
// address may then be two places.
func (v *loopView) changedBetween(j *judge, r *Recurrence, host string, boots []int) bool {
	first, last := -1, -1
	for _, p := range boots {
		if first < 0 || p < first {
			first = p
		}
		last = max(last, p)
	}
	if first < 0 || first == last {
		return false
	}
	bios := ""
	for p := first; p <= last; p++ {
		b := r.Boots[p].bios
		if b == "" {
			continue
		}
		if bios != "" && b != bios {
			return true
		}
		bios = b
	}
	from, to := r.Boots[first].StartedAt, r.Boots[last].StartedAt
	for _, pl := range j.in.Placements {
		if pl.Host == host && pl.FirstSeen.After(from) && pl.FirstSeen.Before(to) {
			return true
		}
	}
	return false
}

// sharedPlace says what the DIMMs of a spread share: one channel, one
// socket, or several sockets.
func sharedPlace(j *judge, host string, dimms []*DIMMBoots) model.Statement {
	sockets := map[int]bool{}
	channels := map[channelKey]bool{}
	known := true
	for _, db := range dimms {
		ck, ok := j.sys.channel[hostSlot{host, db.Slot}]
		if !ok {
			known = false
			continue
		}
		sockets[ck.socket] = true
		channels[ck] = true
	}
	switch {
	case !known || len(sockets) == 0:
		return statement("loop.shared_unknown")
	case len(channels) == 1:
		for ck := range channels {
			return statement("loop.shared_channel", "channel", channelText(ck))
		}
	case len(sockets) == 1:
		for s := range sockets {
			return statement("loop.shared_socket", "socket", s)
		}
	}
	return statement("loop.shared_sockets", "sockets", len(sockets))
}

// lineKey is a row or a column of a bank.
type lineKey struct {
	level               string
	rank, dev, bg, bank int16
	n                   int32
}

func (k lineKey) text() string {
	var parts []string
	for _, f := range []struct {
		name string
		v    int16
	}{{"rank", k.rank}, {"dev", k.dev}, {"bg", k.bg}, {"bank", k.bank}} {
		if f.v >= 0 {
			parts = append(parts, fmt.Sprintf("%s%d", f.name, f.v))
		}
	}
	if k.level == PlaceRow {
		parts = append(parts, fmt.Sprintf("row 0x%x", k.n))
	} else {
		parts = append(parts, fmt.Sprintf("col 0x%x", k.n))
	}
	return strings.Join(parts, " ")
}

// lines groups the uncorrected errors of a DIMM in the window by row and
// by column of a bank (grade A), and by addresses that lie within window
// bytes of the lowest of them (grade B, one row estimated as in
// cluster.row_window_bytes), with the boots each group had errors in.
func lines(evs []event, idx []int32, pos map[int32]int, window uint64) []PlaceBoots {
	type member struct {
		i int32
		p int
	}
	groups := map[lineKey][]member{}
	type addrMember struct {
		addr uint64
		member
	}
	var addrs []addrMember
	for _, i := range idx {
		e := &evs[i]
		p, ok := pos[e.boot]
		if !ok || e.counter || e.sev == sevCE {
			continue
		}
		switch {
		case e.located():
			groups[lineKey{PlaceRow, e.rank, e.dev, e.bg, e.bank, e.row}] = append(groups[lineKey{PlaceRow, e.rank, e.dev, e.bg, e.bank, e.row}], member{i, p})
			groups[lineKey{PlaceColumn, e.rank, e.dev, e.bg, e.bank, e.col}] = append(groups[lineKey{PlaceColumn, e.rank, e.dev, e.bg, e.bank, e.col}], member{i, p})
		case e.hasAddr:
			addrs = append(addrs, addrMember{e.addr, member{i, p}})
		}
	}
	group := func(level, place string, ms []member) PlaceBoots {
		pb := PlaceBoots{Level: level, Place: place}
		cells := map[int]*BootCell{}
		for _, m := range ms {
			c := cells[m.p]
			if c == nil {
				c = &BootCell{Boot: m.p + 1}
				cells[m.p] = c
			}
			c.UE += int64(evs[m.i].errors)
			pb.UE += int64(evs[m.i].errors)
			pb.events = append(pb.events, m.i)
		}
		for _, p := range bootKeys(cells) {
			pb.Cells = append(pb.Cells, *cells[p])
			pb.ueBoots = append(pb.ueBoots, p)
		}
		pb.BootsWithErrors, pb.BootsWithUE = len(pb.Cells), len(pb.Cells)
		return pb
	}
	var out []PlaceBoots
	for k, ms := range groups {
		out = append(out, group(k.level, k.text(), ms))
	}
	sort.Slice(addrs, func(a, b int) bool {
		if addrs[a].addr != addrs[b].addr {
			return addrs[a].addr < addrs[b].addr
		}
		return addrs[a].i < addrs[b].i
	})
	for start := 0; start < len(addrs); {
		end := start
		for end+1 < len(addrs) && addrs[end+1].addr-addrs[start].addr < window {
			end++
		}
		// One cache line is the address place itself.
		if addrs[end].addr>>6 != addrs[start].addr>>6 {
			ms := make([]member, 0, end-start+1)
			for _, a := range addrs[start : end+1] {
				ms = append(ms, a.member)
			}
			pb := group(PlaceNearRow, fmt.Sprintf("0x%x-0x%x", addrs[start].addr, addrs[end].addr), ms)
			pb.from, pb.to = addrs[start].addr, addrs[end].addr
			out = append(out, pb)
		}
		start = end + 1
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Level != out[b].Level {
			return out[a].Level < out[b].Level
		}
		return out[a].Place < out[b].Place
	})
	return out
}

func firstBoot(boots map[int][]*DIMMBoots) int {
	first := -1
	for p := range boots {
		if first < 0 || p < first {
			first = p
		}
	}
	return first
}

func (v *loopView) fire(j *judge, d *DIMMResult, rule string, st model.Statement, events []int32) {
	if d == nil {
		return
	}
	ev := model.Evidence{Rule: rule, Statement: st}
	if len(events) > 0 {
		ev.Records, ev.Total = j.eventRefs(events)
	}
	v.fired[d] = append(v.fired[d], ev)
}

func (v *loopView) check(d *DIMMResult, effect string, st model.Statement) {
	v.checks[d] = append(v.checks[d], model.Check{Statement: st, Effect: effect})
}

// faultBootsStatement says in how many boots of the window a fault had
// errors, when it had them in two or more: a fault of corrected errors
// that comes back boot after boot.
func (v *loopView) faultBootsStatement(f *Fault, host string) (model.Statement, bool) {
	r := v.byHost[host]
	n := v.faultBoots[f]
	if r == nil || n < 2 {
		return model.Statement{}, false
	}
	return statement("loop.fault_boots", "where", orNone(f.Where()), "n", n, "of", len(r.Boots)), true
}

// loopMissing says, on a finding of R-LOOP-02, what my preflight found a
// reset loses on the host: the records that would have named the place.
func (j *judge) loopMissing(fs *findings) []model.Statement {
	if !fs.has(RuleLoopDIMM) {
		return nil
	}
	if lost := lostCaptures(j.in, fs.d.Host); len(lost) > 0 {
		return []model.Statement{statement("missing.capture_lost", "checks", lost)}
	}
	return nil
}

// lostCaptures returns the checks of preflight that were lost on a host,
// for the "missing" lines of a finding about a host that stopped and has
// no located record.
func lostCaptures(in *Input, host string) []string {
	var out []string
	for _, c := range in.Captures {
		if c.Host == host && c.State == "lost" {
			out = append(out, c.Name)
		}
	}
	return out
}

func bootKeys[V any](m map[int]V) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}

// orNone turns a place of no text into a word for a statement argument.
func orNone(s string) string {
	if s == "" {
		return "@word.loop_no_place"
	}
	return s
}

// Duration returns a running time in the unit that fits it: the number and
// the code of the unit in Messages ("unit.min").
func Duration(seconds int64) (n int64, unit string) {
	switch {
	case seconds >= 48*3600:
		return seconds / 86400, "unit.d"
	case seconds >= 2*3600:
		return seconds / 3600, "unit.h"
	case seconds >= 120:
		return seconds / 60, "unit.min"
	}
	return seconds, "unit.s"
}

// DurationText writes a time span in the unit that fits it: "3 min" in
// English, "3분" in Korean.
func DurationText(lang i18n.Lang, seconds int64) string {
	n, unit := Duration(seconds)
	if lang == i18n.KO {
		return i18n.Thousands(n) + Messages.Text(lang, unit, nil)
	}
	return i18n.Thousands(n) + " " + Messages.Text(lang, unit, nil)
}

// faultBoots adds to R-SOFT-01 or R-ROW-03 in how many boots the fault
// they speak about had errors, when it had them in two or more boots.
func (j *judge) faultBoots(fs *findings) {
	if j.loop == nil {
		return
	}
	rule := ""
	switch {
	case fs.has(RuleRow):
		rule = RuleRow
	case fs.has(RuleSoft):
		rule = RuleSoft
	default:
		return
	}
	var best *Fault
	for _, f := range fs.d.Faults {
		if j.loop.faultBoots[f] >= 2 && (best == nil || j.loop.faultBoots[f] > j.loop.faultBoots[best]) {
			best = f
		}
	}
	if best == nil {
		return
	}
	if st, ok := j.loop.faultBootsStatement(best, fs.d.Host); ok {
		fs.add(rule, st, nil, 0)
	}
}
