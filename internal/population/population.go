// Package population compares the modules of a board with the population
// the documents of the board's maker give for it: which slots carry the
// modules for a given number of modules, which slot of a channel comes
// first, how the processors are balanced and what may be mixed. A rule set
// (sets.tsv) holds these facts for one board family, each with the page of
// the public document it stands on.
//
// A population finding is no finding of the engine: it has no grade and
// changes no grade, ranking, confidence, check or exit code. The functions
// of this package read their input and nothing else.
package population

import (
	"sort"
	"strings"
)

// Board is what the check reads of a board: the slots as the board drew
// them (internal/explorer), what the ledger knows of the host and of the
// module in each slot.
type Board struct {
	// Maker is the maker whose documents describe the board; Product and
	// BoardName the system product and the board of the records; Family
	// the board family whose row placed the slots; Processor the processor
	// family of explorer.ProcessorFamily.
	Maker, Product, BoardName string
	Family, Processor         string
	// Sockets is the number of processor records; Present says per record
	// whether it holds a processor: 1, 0, or -1 when the record does not say.
	Sockets int
	Present []int
	// Mode is the BIOS memory mode the ledger holds (the states of
	// internal/memras), "" when it holds none.
	Mode string
	// Full is set when the board is placed in full; NotPlaced counts the
	// slots that are not placed from their own names or a family table.
	Full      bool
	NotPlaced int
	Slots     []Slot
}

// Slot is one slot of the board as it was placed.
type Slot struct {
	Name        string
	Socket      int
	Channel     int
	Position    int
	ChannelName string
	Populated   bool
	Module      Module
}

// Module is what the ledger knows of the module in a slot. A zero value is
// not known.
type Module struct {
	Bytes int64
	// Type is RDIMM, LRDIMM or UDIMM as the inventory gives the module type.
	Type  string
	Ranks int
	// Speed is the rated speed in MT/s.
	Speed int
	Part  string
}

// Reasons for no rule.
const (
	NoSet   = "no-set"   // no rule set speaks of the board
	NotFull = "not-full" // the board is not placed in full
)

// Kinds of a note: a line that is no finding.
const (
	Agrees      = "agrees"       // every compared processor holds its modules as the table has them
	Holds       = "holds"        // a rule set without an order: no rule fired
	NotCompared = "not-compared" // the table has no population for this number of modules
	OtherMode   = "other-mode"   // the modules stand as the table of another memory mode has them
	SecondSet   = "second-set"   // they stand in the other set the document allows for this number
)

// Fields a rule reads, for the rules not checked.
const (
	FieldType   = "type"
	FieldRanks  = "ranks"
	FieldSize   = "size"
	FieldSpeed  = "speed"
	FieldPart   = "part"
	FieldStatus = "status" // the processor record says nothing of the processor
	FieldPlaces = "places" // the table names slots the board does not list
	FieldMode   = "mode"   // the document gives no table for the memory mode of the ledger
)

// Result is the population check of one board.
type Result struct {
	// Set is the rule set; NoRule why there is none (NoSet or NotFull),
	// with NotPlaced the slots not placed.
	Set       string `json:"set,omitzero"`
	NoRule    string `json:"no_rule,omitzero"`
	NotPlaced int    `json:"not_placed,omitzero"`
	// Ordered is set when the rule set holds an order; Strength is the
	// strength of the order's wording and Source where it stands.
	Ordered    bool        `json:"ordered,omitzero"`
	Strength   string      `json:"strength,omitzero"`
	Source     []Ref       `json:"source,omitzero"`
	Processors []Processor `json:"processors,omitzero"`
	Findings   []Finding   `json:"findings,omitzero"`
	Notes      []Note      `json:"notes,omitzero"`
	Unchecked  []Unchecked `json:"unchecked,omitzero"`
}

// Processor is one processor with slots: the places of its modules and
// the places the table fills for their number.
type Processor struct {
	Socket  int `json:"socket"`
	Modules int `json:"modules"`
	// Installed and Recommended are slot names; Recommended is left out
	// when the table lists no population for this number.
	Installed   []string `json:"installed"`
	Recommended []string `json:"recommended,omitzero"`
	// Table is the table of the processor for every number of modules it
	// lists, in slot names.
	Table []Row `json:"table,omitzero"`
}

// Row is one row of a table: the slots for N modules.
type Row struct {
	N     int      `json:"n"`
	Slots []string `json:"slots"`
}

// Finding is one population finding.
type Finding struct {
	Rule     string `json:"rule"`
	Strength string `json:"strength"`
	// Socket is the processor the finding is about, -1 for the system;
	// Channel the vendor's name of the channel of a channel rule.
	Socket  int    `json:"socket"`
	Channel string `json:"channel,omitzero"`
	// Fill and Leave are the empty slots the table fills and the modules
	// in slots it leaves empty (P-ORDER-01).
	Fill  []string `json:"fill,omitzero"`
	Leave []string `json:"leave,omitzero"`
	// Slots are the slots the finding names; First the slot of the
	// channel the table fills first.
	Slots []string `json:"slots,omitzero"`
	First string   `json:"first,omitzero"`
	// What differs: the modules per processor (P-BAL-01), the module types
	// (P-MIX-01), the ranks and bytes of Slots (P-MIX-02, P-MIX-03), the
	// rated speeds (P-MIX-04), the part numbers in the channel (P-MIX-05).
	Counts []int    `json:"counts,omitzero"`
	Types  []string `json:"types,omitzero"`
	Ranks  []int    `json:"ranks,omitzero"`
	Bytes  []int64  `json:"bytes,omitzero"`
	Speeds []int    `json:"speeds,omitzero"`
	Parts  int      `json:"parts,omitzero"`
	Source []Ref    `json:"source"`
}

// Note is a line of the check that is no finding.
type Note struct {
	Kind   string `json:"kind"`
	Socket int    `json:"socket"`
	// Modules is the number of modules the line speaks of; Counts the
	// numbers the table lists (NotCompared); Mode the memory mode of the
	// table the modules stand as (OtherMode).
	Modules int    `json:"modules"`
	Counts  []int  `json:"counts,omitzero"`
	Mode    string `json:"mode,omitzero"`
	Source  []Ref  `json:"source,omitzero"`
}

// Unchecked is a rule that was not checked: the field it needs and how
// many modules (or processors, slots) lack it.
type Unchecked struct {
	Rule  string `json:"rule"`
	Field string `json:"field"`
	N     int    `json:"n"`
	// Mode is the memory mode of the ledger (FieldMode).
	Mode string `json:"mode,omitzero"`
}

// Check compares the board with the embedded rule sets.
func Check(b Board) Result { return CheckWith(b, sets) }

// CheckWith compares the board with the first of the rule sets that
// speaks of it.
func CheckWith(b Board, sets []Set) Result {
	var r Result
	if !b.Full {
		r.NoRule, r.NotPlaced = NotFull, b.NotPlaced
		return r
	}
	per := map[int]int{}
	for _, s := range b.Slots {
		per[s.Socket]++
	}
	var set *Set
	for i := range sets {
		if sets[i].matches(&b, per) {
			set = &sets[i]
			break
		}
	}
	if set == nil {
		r.NoRule = NoSet
		return r
	}
	c := checker{b: &b, set: set, r: &r}
	c.run()
	return r
}

type checker struct {
	b   *Board
	set *Set
	r   *Result
	// socks are the sockets with slots, in order; slots their slots.
	socks []int
	slots map[int][]int
}

func (c *checker) run() {
	b, set, r := c.b, c.set, c.r
	r.Set, r.Ordered = set.ID, len(set.Order) > 0
	if r.Ordered {
		r.Strength, r.Source = set.Strength, set.Source
	}
	c.slots = map[int][]int{}
	for i, s := range b.Slots {
		if _, ok := c.slots[s.Socket]; !ok {
			c.socks = append(c.socks, s.Socket)
		}
		c.slots[s.Socket] = append(c.slots[s.Socket], i)
	}
	sort.Ints(c.socks)
	compared, agreed, modules, inCompared := 0, 0, 0, 0
	for k, sock := range c.socks {
		p, cmp, ok := c.order(k+1, sock)
		if p != nil {
			r.Processors = append(r.Processors, *p)
			modules += p.Modules
		}
		if cmp {
			compared++
			inCompared += p.Modules
			if ok {
				agreed++
			}
		}
	}
	c.cpu()
	c.balance()
	c.mixType()
	c.channels()
	c.mixSpeed()
	if len(r.Findings) == 0 && len(r.Notes) == 0 {
		switch {
		case r.Ordered && compared > 0 && agreed == compared:
			r.Notes = append(r.Notes, Note{Kind: Agrees, Socket: -1, Modules: inCompared, Source: set.Source})
		case !r.Ordered && modules > 0:
			r.Notes = append(r.Notes, Note{Kind: Holds, Socket: -1, Modules: modules, Source: c.ruleSources()})
		}
	}
}

// ruleSources are the pages of the rules of the set, each document once.
func (c *checker) ruleSources() []Ref {
	var out []Ref
	seen := map[string]bool{}
	for _, id := range Rules {
		for _, ref := range c.set.Rules[id].Source {
			if !seen[ref.ID+":"+ref.Pages] {
				seen[ref.ID+":"+ref.Pages] = true
				out = append(out, ref)
			}
		}
	}
	return out
}

// place is the place of a slot on its processor.
func (c *checker) place(i int) Place {
	s := c.b.Slots[i]
	return Place{s.Channel, s.Position}
}

// names are the slot names of places of a socket, in the order given.
func (c *checker) names(sock int, ps []Place) []string {
	at := map[Place]string{}
	for _, i := range c.slots[sock] {
		at[c.place(i)] = c.b.Slots[i].Name
	}
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		if n, ok := at[p]; ok {
			out = append(out, n)
		}
	}
	return out
}

// order holds a processor (proc from 1) to the order of the rule set. It
// returns the processor, whether its modules were compared with a table,
// and whether they stand as that table has them.
func (c *checker) order(proc, sock int) (*Processor, bool, bool) {
	have := map[Place]bool{}
	var inst []Place
	for _, i := range c.slots[sock] {
		have[c.place(i)] = true
		if c.b.Slots[i].Populated {
			inst = append(inst, c.place(i))
		}
	}
	n := len(inst)
	if n == 0 {
		return nil, false, false
	}
	sortPlaces(inst)
	p := &Processor{Socket: sock, Modules: n, Installed: c.names(sock, inst)}
	if len(c.set.Order) == 0 {
		return p, false, false
	}
	t, others, ok := c.tables(proc)
	if !ok {
		c.unchecked(Order, FieldMode, 1)
		return p, false, false
	}
	if t == nil {
		return p, false, false
	}
	for _, k := range t.Counts(have) {
		if ps, ok := t.At(k, have); ok {
			p.Table = append(p.Table, Row{N: k, Slots: c.names(sock, ps)})
		}
	}
	want, listed := t.At(n, have)
	if !listed {
		c.r.Notes = append(c.r.Notes, Note{Kind: NotCompared, Socket: sock, Modules: n, Counts: t.Counts(have)})
		return p, false, false
	}
	missing := 0
	for _, w := range want {
		if !have[w] {
			missing++
		}
	}
	if missing > 0 {
		c.unchecked(Order, FieldPlaces, missing)
		return p, false, false
	}
	p.Recommended = c.names(sock, want)
	if samePlaces(want, inst) {
		return p, true, true
	}
	for _, o := range others {
		if ps, ok := o.At(n, have); ok && samePlaces(ps, inst) {
			note := Note{Kind: SecondSet, Socket: sock, Modules: n, Source: c.set.Source}
			if o.Mode != "" && o.Mode != t.Mode {
				note.Kind, note.Mode = OtherMode, o.Mode
			}
			c.r.Notes = append(c.r.Notes, note)
			return p, true, true
		}
	}
	in := map[Place]bool{}
	for _, x := range inst {
		in[x] = true
	}
	wanted := map[Place]bool{}
	var fill, leave []Place
	for _, w := range want {
		wanted[w] = true
		if !in[w] {
			fill = append(fill, w)
		}
	}
	for _, x := range inst {
		if !wanted[x] {
			leave = append(leave, x)
		}
	}
	c.r.Findings = append(c.r.Findings, Finding{Rule: Order, Strength: c.set.Strength, Socket: sock, Fill: c.names(sock, fill),
		Leave: c.names(sock, leave), Slots: append(c.names(sock, fill), c.names(sock, leave)...), Source: c.set.Source})
	return p, true, false
}

// tables are the table a processor is held to and the tables that stand
// beside it. Without the memory mode in the ledger that is the order and
// every alt; with it the tables of that mode alone. ok is false when the
// ledger holds a mode the rule set has no table for.
func (c *checker) tables(proc int) (*Table, []Table, bool) {
	order := forProc(c.set.Order, proc)
	alts := altsFor(c.set.Alt, proc)
	tagged := false
	for _, t := range append(append([]Table{}, c.set.Order...), c.set.Alt...) {
		if t.Mode != "" {
			tagged = true
		}
	}
	mode := c.b.Mode
	if mode == "" || !tagged {
		return order, alts, true
	}
	// A table without a mode is a second set of the order's mode.
	of := func(t Table) string {
		if t.Mode == "" && order != nil {
			return order.Mode
		}
		return t.Mode
	}
	var main *Table
	if order != nil && order.Mode == mode {
		main = order
	}
	var same []Table
	for i, t := range alts {
		if of(t) != mode {
			continue
		}
		if main == nil && t.Mode == mode {
			main = &alts[i]
			continue
		}
		same = append(same, t)
	}
	if main == nil {
		return nil, nil, false
	}
	return main, same, true
}

func (c *checker) unchecked(rule, field string, n int) {
	for i, u := range c.r.Unchecked {
		if u.Rule == rule && u.Field == field {
			c.r.Unchecked[i].N += n
			return
		}
	}
	u := Unchecked{Rule: rule, Field: field, N: n}
	if field == FieldMode {
		u.Mode = c.b.Mode
	}
	c.r.Unchecked = append(c.r.Unchecked, u)
}

func (c *checker) finding(rule string, f Finding) {
	r := c.set.Rules[rule]
	f.Rule, f.Strength, f.Source = rule, r.Strength, r.Source
	c.r.Findings = append(c.r.Findings, f)
}

// modules counts the populated slots of a socket.
func (c *checker) modules(sock int) int {
	n := 0
	for _, i := range c.slots[sock] {
		if c.b.Slots[i].Populated {
			n++
		}
	}
	return n
}

// installed says whether a socket holds a processor: 1 yes, 0 no, -1 not
// known. A socket with modules holds one.
func (c *checker) installed(sock int) int {
	if c.modules(sock) > 0 {
		return 1
	}
	if sock >= 0 && sock < len(c.b.Present) {
		return c.b.Present[sock]
	}
	return -1
}

// cpu is P-CPU-01: an installed processor without a module.
func (c *checker) cpu() {
	if _, ok := c.set.Rules[CPU]; !ok {
		return
	}
	unknown := 0
	for _, sock := range c.socks {
		switch c.installed(sock) {
		case 1:
			if c.modules(sock) == 0 {
				c.finding(CPU, Finding{Socket: sock})
			}
		case -1:
			unknown++
		}
	}
	if unknown > 0 {
		c.unchecked(CPU, FieldStatus, unknown)
	}
}

// balance is P-BAL-01: the installed processors hold different numbers of
// modules.
func (c *checker) balance() {
	if _, ok := c.set.Rules[Balance]; !ok {
		return
	}
	var counts []int
	unknown := 0
	for _, sock := range c.socks {
		switch c.installed(sock) {
		case 1:
			counts = append(counts, c.modules(sock))
		case -1:
			unknown++
		}
	}
	for _, n := range counts {
		if n != counts[0] {
			c.finding(Balance, Finding{Socket: -1, Counts: counts})
			return
		}
	}
	if unknown > 0 && len(counts) > 0 {
		c.unchecked(Balance, FieldStatus, unknown)
	}
}

// populated are the indexes of the slots that hold a module, in the order
// of the board.
func (c *checker) populated() []int {
	var out []int
	for _, sock := range c.socks {
		for _, i := range c.slots[sock] {
			if c.b.Slots[i].Populated {
				out = append(out, i)
			}
		}
	}
	return out
}

// TypeClass is the class of a module type the documents speak of:
// LRDIMM, RDIMM or UDIMM, "" when the type says none of them.
func TypeClass(t string) string {
	u := strings.ToUpper(t)
	switch {
	case strings.Contains(u, "LRDIMM"):
		return "LRDIMM"
	case strings.Contains(u, "RDIMM"):
		return "RDIMM"
	case strings.Contains(u, "UDIMM"), strings.Contains(u, "SO-DIMM"), strings.Contains(u, "SODIMM"):
		return "UDIMM"
	}
	return ""
}

// mixType is P-MIX-01: module types that may not stand together.
func (c *checker) mixType() {
	if _, ok := c.set.Rules[MixType]; !ok {
		return
	}
	seen := map[string]bool{}
	var types []string
	unknown := 0
	for _, i := range c.populated() {
		t := TypeClass(c.b.Slots[i].Module.Type)
		switch {
		case t == "":
			unknown++
		case !seen[t]:
			seen[t] = true
			types = append(types, t)
		}
	}
	if len(types) > 1 {
		sort.Slice(types, func(a, b int) bool { return typeOrder(types[a]) < typeOrder(types[b]) })
		c.finding(MixType, Finding{Socket: -1, Types: types})
		return
	}
	if unknown > 0 {
		c.unchecked(MixType, FieldType, unknown)
	}
}

func typeOrder(t string) int { return strings.Index("RDIMM LRDIMM UDIMM", t) }

// mixSpeed is P-MIX-04: modules of different rated speed.
func (c *checker) mixSpeed() {
	if _, ok := c.set.Rules[MixSpeed]; !ok {
		return
	}
	seen := map[int]bool{}
	var speeds []int
	unknown := 0
	for _, i := range c.populated() {
		v := c.b.Slots[i].Module.Speed
		switch {
		case v <= 0:
			unknown++
		case !seen[v]:
			seen[v] = true
			speeds = append(speeds, v)
		}
	}
	if len(speeds) > 1 {
		sort.Ints(speeds)
		c.finding(MixSpeed, Finding{Socket: -1, Speeds: speeds})
		return
	}
	if unknown > 0 {
		c.unchecked(MixSpeed, FieldSpeed, unknown)
	}
}

// channels holds the rules of one channel: P-FIRST-01, P-MIX-02, P-MIX-03
// and P-MIX-05.
func (c *checker) channels() {
	rules := c.set.Rules
	_, first := rules[First]
	_, rank := rules[MixRank]
	_, size := rules[MixSize]
	_, part := rules[MixPart]
	if !first && !rank && !size && !part {
		return
	}
	var noRank, noSize, noPart int
	for _, sock := range c.socks {
		byCh := map[int][]int{}
		var chs []int
		for _, i := range c.slots[sock] {
			ch := c.b.Slots[i].Channel
			if _, ok := byCh[ch]; !ok {
				chs = append(chs, ch)
			}
			byCh[ch] = append(byCh[ch], i)
		}
		sort.Ints(chs)
		for _, ch := range chs {
			// The slots of the channel in the order the table fills them.
			idx := byCh[ch]
			sort.SliceStable(idx, func(a, b int) bool {
				pa, pb := c.b.Slots[idx[a]].Position, c.b.Slots[idx[b]].Position
				if c.set.First == "last" {
					return pa > pb
				}
				return pa < pb
			})
			name := c.b.Slots[idx[0]].ChannelName
			head := c.b.Slots[idx[0]].Name
			var mods []int
			for _, i := range idx {
				if c.b.Slots[i].Populated {
					mods = append(mods, i)
				}
			}
			if first && len(mods) > 0 && !c.b.Slots[idx[0]].Populated {
				c.finding(First, Finding{Socket: sock, Channel: name, Slots: []string{c.b.Slots[mods[0]].Name, head}, First: head})
			}
			if len(mods) < 2 {
				continue
			}
			if rank {
				if v, ok := c.rising(mods, func(m Module) int64 { return int64(m.Ranks) }); ok {
					if v >= 0 {
						a, b := c.b.Slots[mods[v-1]], c.b.Slots[mods[v]]
						c.finding(MixRank, Finding{Socket: sock, Channel: name, Slots: []string{a.Name, b.Name}, First: head,
							Ranks: []int{a.Module.Ranks, b.Module.Ranks}})
					}
				} else {
					noRank += c.lacking(mods, func(m Module) bool { return m.Ranks <= 0 })
				}
			}
			if size {
				if v, ok := c.rising(mods, func(m Module) int64 { return m.Bytes }); ok {
					if v >= 0 {
						a, b := c.b.Slots[mods[v-1]], c.b.Slots[mods[v]]
						c.finding(MixSize, Finding{Socket: sock, Channel: name, Slots: []string{a.Name, b.Name}, First: head,
							Bytes: []int64{a.Module.Bytes, b.Module.Bytes}})
					}
				} else {
					noSize += c.lacking(mods, func(m Module) bool { return m.Bytes <= 0 })
				}
			}
			if part {
				distinct := map[string]bool{}
				lack := c.lacking(mods, func(m Module) bool { return strings.TrimSpace(m.Part) == "" })
				for _, i := range mods {
					if p := strings.TrimSpace(c.b.Slots[i].Module.Part); p != "" {
						distinct[p] = true
					}
				}
				var names []string
				for _, i := range mods {
					names = append(names, c.b.Slots[i].Name)
				}
				switch {
				case len(distinct) > 1:
					c.finding(MixPart, Finding{Socket: sock, Channel: name, Slots: names, Parts: len(distinct)})
				case lack > 0:
					noPart += lack
				}
			}
		}
	}
	c.notChecked(MixRank, FieldRanks, noRank)
	c.notChecked(MixSize, FieldSize, noSize)
	c.notChecked(MixPart, FieldPart, noPart)
}

// notChecked lists a rule as not checked when it found nothing and
// modules lacked its field.
func (c *checker) notChecked(rule, field string, n int) {
	if n == 0 {
		return
	}
	for _, f := range c.r.Findings {
		if f.Rule == rule {
			return
		}
	}
	c.unchecked(rule, field, n)
}

// rising finds in modules in fill order the first one whose value is
// above the one before it: its index, or -1 when there is none; ok is
// false when a module lacks the value.
func (c *checker) rising(mods []int, v func(Module) int64) (int, bool) {
	for _, i := range mods {
		if v(c.b.Slots[i].Module) <= 0 {
			return 0, false
		}
	}
	for k := 1; k < len(mods); k++ {
		if v(c.b.Slots[mods[k]].Module) > v(c.b.Slots[mods[k-1]].Module) {
			return k, true
		}
	}
	return -1, true
}

func (c *checker) lacking(mods []int, lacks func(Module) bool) int {
	n := 0
	for _, i := range mods {
		if lacks(c.b.Slots[i].Module) {
			n++
		}
	}
	return n
}

func sortPlaces(ps []Place) {
	sort.Slice(ps, func(a, b int) bool {
		if ps[a].Channel != ps[b].Channel {
			return ps[a].Channel < ps[b].Channel
		}
		return ps[a].Position < ps[b].Position
	})
}

func samePlaces(a, b []Place) bool {
	if len(a) != len(b) {
		return false
	}
	in := map[Place]bool{}
	for _, p := range a {
		in[p] = true
	}
	for _, p := range b {
		if !in[p] {
			return false
		}
	}
	return true
}

// Marks are the marks of the board: "+" on an empty slot the table fills
// for the number of modules, "-" on a module in a slot it leaves empty.
func (r Result) Marks() map[string]string {
	out := map[string]string{}
	for _, f := range r.Findings {
		if f.Rule != Order {
			continue
		}
		for _, s := range f.Fill {
			out[s] = "+"
		}
		for _, s := range f.Leave {
			out[s] = "-"
		}
	}
	return out
}
