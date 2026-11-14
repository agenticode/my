package engine

import (
	"fmt"
	"sort"
	"strings"

	"github.com/agenticode/my/internal/decode/edac"
	"github.com/agenticode/my/internal/model"
)

// The system checks ask whether the cause of a DIMM's errors lies outside
// the DIMM: in the channel, the slot, the contact, or in the way slots are
// named. They look at more than one DIMM at a time, so they run per host
// before the DIMM rules.
//
// They count errors that are not confined: errors outside faults that stay
// within one cell, row, column or bank and came back there. A fault inside
// a DRAM device confines itself that way. A bad contact or a bad trace
// corrupts whatever address is being read.

// slotUse is what one DIMM showed in one slot.
type slotUse struct {
	d    *DIMMResult
	host string
	slot string
	// all counts every error of the DIMM in the slot, loose the errors that
	// are not confined.
	all, loose int64
	// first and last bound the dated loose errors.
	first, last int64
	// events are the loose records, faults the faults they belong to.
	events []int32
	faults []*Fault
}

type hostSlot struct{ host, slot string }

// span is the time one DIMM spent in one slot, as placements show it.
type span struct {
	uid         string
	first, last int64
}

type channelKey struct {
	host                 string
	socket, imc, channel int
}

// conflict says that records tied to one slot carry a name of another.
type conflict struct {
	slot, other string
	name        string // message code of the kind of name
	value       string
	records     int64
	first, last int64 // event IDs
}

// systemView holds what the system checks need about the hosts.
type systemView struct {
	in  *Input
	cfg *Config

	uses      map[hostSlot][]*slotUse
	usesOf    map[*DIMMResult][]*slotUse
	spans     map[hostSlot][]span
	spansOf   map[string][]placed // by DIMM, in time order
	channel   map[hostSlot]channelKey
	members   map[channelKey][]string
	populated map[hostSlot]bool
	conflicts map[hostSlot][]conflict
	named     map[hostSlot][]conflict // conflicts by the slot that is named
	dimms     map[string]*DIMMResult
	present   map[hostSlot]*DIMMResult

	// fired collects the evidence of the system rules per DIMM.
	fired map[*DIMMResult][]model.Evidence
}

// placed is one stay of a DIMM in a slot.
type placed struct {
	hostSlot
	first, last int64
}

func newSystemView(in *Input, cfg *Config, all []*DIMMResult) *systemView {
	v := &systemView{
		in: in, cfg: cfg,
		uses: map[hostSlot][]*slotUse{}, usesOf: map[*DIMMResult][]*slotUse{},
		spans: map[hostSlot][]span{}, spansOf: map[string][]placed{},
		channel: map[hostSlot]channelKey{}, members: map[channelKey][]string{},
		populated: map[hostSlot]bool{}, conflicts: map[hostSlot][]conflict{}, named: map[hostSlot][]conflict{},
		dimms: map[string]*DIMMResult{}, present: map[hostSlot]*DIMMResult{},
		fired: map[*DIMMResult][]model.Evidence{},
	}
	for _, d := range all {
		v.dimms[d.UID] = d
		if d.Present && d.Slot != "" {
			k := hostSlot{d.Host, d.Slot}
			v.present[k], v.populated[k] = d, true
		}
		v.addUses(d)
	}

	// Who sat where, and when. Neighbouring placements of one DIMM in one
	// slot are one stay.
	ps := append([]model.Placement(nil), in.Placements...)
	sort.SliceStable(ps, func(i, j int) bool {
		if !ps[i].FirstSeen.Equal(ps[j].FirstSeen) {
			return ps[i].FirstSeen.Before(ps[j].FirstSeen)
		}
		return ps[i].ID < ps[j].ID
	})
	for _, p := range ps {
		k := hostSlot{p.Host, p.Slot}
		first, last := p.FirstSeen.UnixMicro(), p.LastSeen.UnixMicro()
		if s := v.spans[k]; len(s) > 0 && s[len(s)-1].uid == p.DIMMUID {
			s[len(s)-1].last = max(s[len(s)-1].last, last)
		} else {
			v.spans[k] = append(v.spans[k], span{p.DIMMUID, first, last})
		}
		if s := v.spansOf[p.DIMMUID]; len(s) > 0 && s[len(s)-1].hostSlot == k {
			s[len(s)-1].last = max(s[len(s)-1].last, last)
		} else {
			v.spansOf[p.DIMMUID] = append(v.spansOf[p.DIMMUID], placed{k, first, last})
		}
	}

	// Channels. A slot row counts channels within the memory controller
	// when it names one, and within the socket otherwise.
	for _, sl := range in.Slots {
		k := hostSlot{sl.Host, sl.Name}
		if sl.Populated.Valid && sl.Populated.V {
			v.populated[k] = true
		}
		if !sl.Socket.Valid || !sl.Channel.Valid {
			continue
		}
		ck := channelKey{sl.Host, sl.Socket.V, sl.IMC.Or(-1), sl.Channel.V}
		v.channel[k] = ck
		v.members[ck] = append(v.members[ck], sl.Name)
	}
	for ck := range v.members {
		sort.Strings(v.members[ck])
	}
	v.findConflicts()
	return v
}

// addUses records what the DIMM showed in each slot it has errors in.
func (v *systemView) addUses(d *DIMMResult) {
	evs := v.in.events
	by := map[int32]*slotUse{}
	get := func(slot int32) *slotUse {
		u := by[slot]
		if u == nil {
			sk := v.in.slots[slot]
			u = &slotUse{d: d, host: v.in.hostNames[sk.host], slot: sk.name}
			by[slot] = u
		}
		return u
	}
	for _, f := range d.Faults {
		seen := map[int32]bool{}
		for _, i := range f.members {
			e := &evs[i]
			if e.slot < 0 {
				continue
			}
			u := get(e.slot)
			u.all += int64(e.errors)
			if f.Confined {
				continue
			}
			u.loose += int64(e.errors)
			u.events = append(u.events, i)
			if !seen[e.slot] {
				seen[e.slot] = true
				u.faults = append(u.faults, f)
			}
			if !e.relative {
				if u.first == 0 || e.at < u.first {
					u.first = e.at
				}
				u.last = max(u.last, e.at)
			}
		}
	}
	slots := make([]int32, 0, len(by))
	for s := range by {
		slots = append(slots, s)
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i] < slots[j] })
	for _, s := range slots {
		u := by[s]
		k := hostSlot{u.host, u.slot}
		v.uses[k] = append(v.uses[k], u)
		v.usesOf[d] = append(v.usesOf[d], u)
	}
}

// findConflicts compares the names error records carry with the slots the
// records were tied to. A record is tied to a slot through one of its
// names; when another name of the same record belongs to a different slot,
// the sources disagree about where the error is.
func (v *systemView) findConflicts() {
	type index struct {
		label  map[string][]string
		handle map[int]string
		edac   map[string]string
		topo   map[[4]int]string
	}
	byHost := map[string]*index{}
	for _, sl := range v.in.Slots {
		ix := byHost[sl.Host]
		if ix == nil {
			ix = &index{label: map[string][]string{}, handle: map[int]string{}, edac: map[string]string{}, topo: map[[4]int]string{}}
			byHost[sl.Host] = ix
		}
		for _, n := range []string{sl.Name, sl.Locator, sl.EdacLabel, strings.TrimSpace(sl.BankLocator + " " + sl.Locator)} {
			if l := model.NormalizeLabel(n); l != "" && !edac.PlaceholderLabel(n) {
				ix.label[l] = append(ix.label[l], sl.Name)
			}
		}
		if sl.SMBIOSHandle.Valid {
			ix.handle[sl.SMBIOSHandle.V] = sl.Name
		}
		if sl.EdacMC.Valid && sl.EdacLocation != "" {
			ix.edac[fmt.Sprintf("%d:%s", sl.EdacMC.V, sl.EdacLocation)] = sl.Name
		}
		if sl.Socket.Valid && sl.IMC.Valid && sl.Channel.Valid && sl.Position.Valid {
			ix.topo[[4]int{sl.Socket.V, sl.IMC.V, sl.Channel.V, sl.Position.V}] = sl.Name
		}
	}
	for _, p := range v.in.Places {
		ix := byHost[p.Host]
		if ix == nil || p.Slot == "" {
			continue
		}
		add := func(other, name, value string) {
			c := conflict{slot: p.Slot, other: other, name: name, value: value, records: p.Records, first: p.FirstID, last: p.LastID}
			k := hostSlot{p.Host, p.Slot}
			v.conflicts[k] = append(v.conflicts[k], c)
			o := hostSlot{p.Host, other}
			v.named[o] = append(v.named[o], c)
		}
		if l := model.NormalizeLabel(p.Label); l != "" && !edac.PlaceholderLabel(p.Label) {
			if owners := ix.label[l]; len(owners) > 0 && !contains(owners, p.Slot) {
				add(owners[0], "@name.label", strings.TrimSpace(p.Label))
			}
		}
		if p.SMBIOSHandle.Valid {
			if o, ok := ix.handle[p.SMBIOSHandle.V]; ok && o != p.Slot {
				add(o, "@name.handle", fmt.Sprintf("0x%04X", p.SMBIOSHandle.V))
			}
		}
		if p.EdacMC.Valid && p.EdacLocation != "" {
			if o, ok := ix.edac[fmt.Sprintf("%d:%s", p.EdacMC.V, p.EdacLocation)]; ok && o != p.Slot {
				add(o, "@name.edac", fmt.Sprintf("mc%d %s", p.EdacMC.V, p.EdacLocation))
			}
		}
		if p.SocketId.Valid && p.IMC.Valid && p.ChannelId.Valid && p.ChannelSlotId.Valid {
			if o, ok := ix.topo[[4]int{p.SocketId.V, p.IMC.V, p.ChannelId.V, p.ChannelSlotId.V}]; ok && o != p.Slot {
				add(o, "@name.topology", fmt.Sprintf("%d/%d/%d/%d", p.SocketId.V, p.IMC.V, p.ChannelId.V, p.ChannelSlotId.V))
			}
		}
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// channelText names a channel the way slot rows describe it.
func channelText(ck channelKey) string {
	if ck.imc >= 0 {
		return fmt.Sprintf("socket %d controller %d channel %d", ck.socket, ck.imc, ck.channel)
	}
	return fmt.Sprintf("socket %d channel %d", ck.socket, ck.channel)
}

// dimmName names a DIMM in a sentence: by its serial number when known.
func dimmName(d *DIMMResult, uid string) string {
	if d != nil && d.DIMM != nil && d.DIMM.Serial != "" {
		return d.DIMM.Serial
	}
	if len(uid) > 8 {
		return uid[:8]
	}
	return uid
}

// slotActivity sums the loose errors all DIMMs showed in a slot.
type slotActivity struct {
	loose, all  int64
	first, last int64
	uses        []*slotUse
}

func (v *systemView) activity(k hostSlot) slotActivity {
	var a slotActivity
	for _, u := range v.uses[k] {
		a.all += u.all
		a.loose += u.loose
		if u.loose == 0 {
			continue
		}
		a.uses = append(a.uses, u)
		if u.first != 0 && (a.first == 0 || u.first < a.first) {
			a.first = u.first
		}
		a.last = max(a.last, u.last)
	}
	return a
}

// run evaluates the system rules for every host of the view.
func (v *systemView) run(j *judge) {
	v.channels(j)
	v.slotsThatKeepErrors(j)
	v.reseats(j)
	v.mappings(j)

	// A fault whose errors a system rule explains is a system issue: the
	// channel, the slot or the contact, not a place inside the DIMM.
	for d, evs := range v.fired {
		for _, ev := range evs {
			if ev.Rule != RuleChannel && ev.Rule != RuleSlot && ev.Rule != RuleReseat {
				continue
			}
			for _, f := range d.Faults {
				if !f.Confined {
					f.Mode = model.ModeSystemIssue
				}
			}
		}
	}
}

// channels is R-SYS-01: two or more slots of one channel show unconfined
// errors in the same period.
func (v *systemView) channels(j *judge) {
	keys := make([]channelKey, 0, len(v.members))
	for ck := range v.members {
		keys = append(keys, ck)
	}
	sort.Slice(keys, func(a, b int) bool {
		x, y := keys[a], keys[b]
		if x.host != y.host {
			return x.host < y.host
		}
		if x.socket != y.socket {
			return x.socket < y.socket
		}
		if x.imc != y.imc {
			return x.imc < y.imc
		}
		return x.channel < y.channel
	})
	slack := days(v.cfg.SystemOverlapDays)
	for _, ck := range keys {
		slots := v.members[ck]
		if len(slots) < 2 {
			continue
		}
		acts := map[string]slotActivity{}
		var active []string
		for _, s := range slots {
			a := v.activity(hostSlot{ck.host, s})
			// Errors without a date cannot be put into a period.
			if a.loose >= int64(v.cfg.SystemMinErrors) && a.first != 0 {
				acts[s] = a
				active = append(active, s)
			}
		}
		for _, s := range active {
			a := acts[s]
			var others []string
			var m int64
			from, to := a.first, a.last
			for _, o := range active {
				b := acts[o]
				if o == s || a.first > b.last+slack || b.first > a.last+slack {
					continue
				}
				others = append(others, o)
				m += b.loose
				if b.first != 0 && (from == 0 || b.first < from) {
					from = b.first
				}
				to = max(to, b.last)
			}
			if len(others) == 0 {
				continue
			}
			for _, u := range a.uses {
				st := statement("sys.channel", "slot", s, "others", others, "channel", channelText(ck),
					"n", u.loose, "m", m, "from", dateText(from), "to", dateText(to))
				v.fire(j, u.d, RuleChannel, st, u.events)
			}
		}
	}
}

// errorsOf counts the errors of a DIMM in a time range, optionally outside
// one slot. A zero bound is open.
func (v *systemView) errorsOf(d *DIMMResult, after, before int64, except hostSlot) (n int64) {
	if d == nil {
		return 0
	}
	for _, i := range d.events {
		e := &v.in.events[i]
		if e.relative || (after != 0 && e.at <= after) || (before != 0 && e.at >= before) {
			continue
		}
		if except.slot != "" && e.slot >= 0 {
			sk := v.in.slots[e.slot]
			if sk.name == except.slot && v.in.hostNames[sk.host] == except.host {
				continue
			}
		}
		n += int64(e.errors)
	}
	return n
}

// slotsThatKeepErrors is R-SYS-02: a slot shows unconfined errors with one
// DIMM, the DIMM is changed, and the slot shows them with the next DIMM.
func (v *systemView) slotsThatKeepErrors(j *judge) {
	keys := make([]hostSlot, 0, len(v.spans))
	for k := range v.spans {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool {
		if keys[a].host != keys[b].host {
			return keys[a].host < keys[b].host
		}
		return keys[a].slot < keys[b].slot
	})
	need := int64(v.cfg.SystemMinErrors)
	for _, k := range keys {
		spans := v.spans[k]
		use := map[string]*slotUse{}
		for _, u := range v.uses[k] {
			use[u.d.UID] = u
		}
		for i := 1; i < len(spans); i++ {
			a, b := spans[i-1], spans[i]
			ua, ub := use[a.uid], use[b.uid]
			if ua == nil || ub == nil || ua.loose < need || ub.loose < need {
				continue
			}
			da, db := v.dimms[a.uid], v.dimms[b.uid]
			// The errors must belong to the slot: the DIMM that left has
			// none elsewhere afterwards, and the DIMM that came brought
			// none with it.
			if v.errorsOf(da, a.last, 0, k) >= need || v.errorsOf(db, 0, b.first, k) >= need {
				continue
			}
			st := statement("sys.stays", "slot", k.slot, "when", dateText(b.first), "old", dimmName(da, a.uid), "new", dimmName(db, b.uid),
				"before", ua.loose, "after", ub.loose)
			v.fire(j, db, RuleSlot, st, ub.events)
			v.fire(j, da, RuleSlot, st, ua.events)
			// Where the DIMM that left is now, if it was seen again.
			if da != nil && da.Present && (da.Slot != k.slot || da.Host != k.host) {
				if since := j.asOf(da) - b.first; since > 0 && v.errorsOf(da, a.last, 0, k) == 0 {
					v.fire(j, da, RuleSlot, statement("sys.left_quiet", "slot", k.slot, "old", dimmName(da, a.uid), "now", da.Slot,
						"days", since/microPerDay), nil)
				}
			}
		}
	}
}

// reseats is R-SYS-03: the errors of a DIMM stopped when it was reseated,
// or when it was moved to another slot, which reseats it too.
func (v *systemView) reseats(j *judge) {
	need := int64(v.cfg.SystemMinErrors)
	quiet := days(v.cfg.QuietDays)
	uids := make([]string, 0, len(v.dimms))
	for uid := range v.dimms {
		uids = append(uids, uid)
	}
	sort.Strings(uids)
	for _, uid := range uids {
		d := v.dimms[uid]
		if len(d.events) == 0 {
			continue
		}
		asOf := j.asOf(d)
		// A reseat on record.
		var reseat *model.Action
		for _, a := range d.actions {
			if a.Kind == model.ActionReseat && a.Result != model.ResultFailed && a.TimeQuality != model.TimeRelative {
				reseat = a
			}
		}
		if reseat != nil {
			at := reseat.At.UnixMicro()
			before, after := v.errorsOf(d, 0, at, hostSlot{}), v.errorsOf(d, at, 0, hostSlot{})
			if before >= need && stopped(before, after, need) && asOf-at >= quiet {
				st := statement("sys.reseat", "n", before, "when", dateText(at), "days", (asOf-at)/microPerDay)
				if after > 0 {
					st = statement("sys.reseat_few", "n", before, "when", dateText(at), "days", (asOf-at)/microPerDay, "after", after)
				}
				v.fire(j, d, RuleReseat, st, d.events)
				j.addRecord(d, RuleReseat, model.RecordRef{Kind: model.RecordAction, ID: reseat.ID, RawRef: reseat.RawRef})
			}
			continue
		}
		// A move to another slot.
		stays := v.spansOf[uid]
		if len(stays) < 2 {
			continue
		}
		prev, cur := stays[len(stays)-2], stays[len(stays)-1]
		before, after := v.errorsOf(d, 0, cur.first, hostSlot{}), v.errorsOf(d, prev.last, 0, hostSlot{})
		if before < need || !stopped(before, after, need) || asOf-cur.first < quiet {
			continue
		}
		// If the old slot went on failing with its next DIMM, the slot is
		// the cause and R-SYS-02 says so.
		var left int64
		for _, u := range v.uses[prev.hostSlot] {
			if u.d != d {
				left += u.loose
			}
		}
		if left >= need {
			continue
		}
		st := statement("sys.moved_quiet", "n", before, "from", prev.slot, "to", cur.slot, "when", dateText(cur.first), "days", (asOf-cur.first)/microPerDay)
		if after > 0 {
			st = statement("sys.moved_few", "n", before, "from", prev.slot, "to", cur.slot, "when", dateText(cur.first), "days", (asOf-cur.first)/microPerDay, "after", after)
		}
		v.fire(j, d, RuleReseat, st, d.events)
	}
}

// stopped reports whether errors came to an end: none afterwards, or a few
// stray ones that are fewer than the system rules compare and at most a
// tenth of what there was before.
func stopped(before, after, need int64) bool {
	return after == 0 || (after < need && after*10 <= before)
}

// mappings is R-MAP-01: records tied to a slot carry a name of another slot.
func (v *systemView) mappings(j *judge) {
	keys := make([]hostSlot, 0, len(v.conflicts))
	for k := range v.conflicts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool {
		if keys[a].host != keys[b].host {
			return keys[a].host < keys[b].host
		}
		return keys[a].slot < keys[b].slot
	})
	for _, k := range keys {
		for _, c := range v.conflicts[k] {
			st := statement("map.conflict", "n", c.records, "slot", c.slot, "name", c.name, "value", c.value, "other", c.other)
			for _, u := range v.uses[k] {
				if u.all == 0 {
					continue
				}
				v.fired[u.d] = append(v.fired[u.d], model.Evidence{Rule: RuleMapping, Statement: st, Total: int(c.records),
					Records: firstLast(c.first, c.last)})
			}
		}
	}
}

func firstLast(first, last int64) []model.RecordRef {
	out := []model.RecordRef{{Kind: model.RecordEvent, ID: first}}
	if last != first {
		out = append(out, model.RecordRef{Kind: model.RecordEvent, ID: last})
	}
	return out
}

func (v *systemView) fire(j *judge, d *DIMMResult, rule string, st model.Statement, events []int32) {
	if d == nil {
		return
	}
	ev := model.Evidence{Rule: rule, Statement: st}
	ev.Records, ev.Total = j.eventRefs(events)
	v.fired[d] = append(v.fired[d], ev)
}

// neighbours returns the other populated slots of the channel of a slot,
// and whether the channel is known.
func (v *systemView) neighbours(k hostSlot) (slots []string, known bool) {
	ck, ok := v.channel[k]
	if !ok {
		return nil, false
	}
	for _, s := range v.members[ck] {
		if s != k.slot && v.populated[hostSlot{k.host, s}] {
			slots = append(slots, s)
		}
	}
	return slots, true
}
