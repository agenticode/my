package explorer

// This file is MemorY's own: the pass that reads the names of every slot
// of a board and decides socket, channel and position (the fixes of the
// second round, design of the board). It runs after the readers of the
// first round when FixNameForms is on, and replaces what they decided. Each
// stage is a fix of its own; a stage that is off decides nothing, and the
// slots it would have decided stay without that part.

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Ways a place was found (Dimm.PlaceBy).
const (
	ByName      = "name"       // the processor word of the locator
	ByBank      = "bank"       // the processor word of the bank locator
	ByNodeOf    = "node"       // a Node of the bank locator
	ByMate      = "mate"       // slots of one channel letter share a processor
	ByLetters   = "letters"    // channel letters split over the processors in order
	ByOneSocket = "one-socket" // one processor record
	ByHub       = "hub"        // memory on the chipset
	ByLabel     = "label"      // an empty slot without a name, named by the manual
)

// Slots that are no slot of the board (Dimm.NotSlot).
const (
	NotSlotFlash   = "flash"            // a chip of the board the firmware lists as a memory device
	NotSlotPhantom = "no-slot-on-board" // a record the manual of the board has no slot for
)

// Facts are what the readers found of the board beside its slots: the
// processor records, the processor and its channels, the family of the
// board, and what the checks of the board need to know.
type Facts struct {
	// Pass is set when the names were read by the pass of the second round.
	Pass bool `json:"pass,omitempty"`
	// Sockets is the number of processor records, 0 when not known;
	// Packages the CPU packages another reading saw (the caches).
	Sockets  int `json:"sockets,omitempty"`
	Packages int `json:"packages,omitempty"`
	// Present says per processor record whether it holds a processor:
	// 1 populated, 0 not, -1 not known.
	Present []int `json:"present,omitempty"`
	// Processor is the processor of the board as the table knows it.
	Processor Processor `json:"processor"`
	// Channels and PerChannel are the channel count of a processor and the
	// most slots of a channel the checks hold the board to; ChannelsBy says
	// where they come from: "processor" or "family:<id>".
	Channels   int    `json:"channels,omitempty"`
	PerChannel int    `json:"perChannel,omitempty"`
	ChannelsBy string `json:"channelsBy,omitempty"`
	// Family is the board family whose row acted; Alias the row of a board
	// known by its part number that sent it there.
	Family string `json:"family,omitempty"`
	Alias  string `json:"alias,omitempty"`
	// Aside is the reason of a family row that sets every slot aside.
	Aside string `json:"aside,omitempty"`
	// Hub is set for memory on the chipset.
	Hub bool `json:"hub,omitempty"`
	// Maker is the maker whose documents describe the board.
	Maker string `json:"maker,omitempty"`
	// Letter says what the letter of A1 and the digit of 1A were taken
	// for: "letter" (the channel), "digit" (the digit is the channel) or
	// "label".
	Letter string `json:"letter,omitempty"`
	// Unnamed counts the empty slots without a name; Labelled those of
	// them named by the manual; Phantoms the records the manual has no
	// slot for; PhantomsPopulated those that hold a module.
	Unnamed           int `json:"unnamed,omitempty"`
	Labelled          int `json:"labelled,omitempty"`
	Phantoms          int `json:"phantoms,omitempty"`
	PhantomsPopulated int `json:"phantomsPopulated,omitempty"`
	// Duplicates counts the slot names that stand twice.
	Duplicates int `json:"duplicates,omitempty"`
	// BankDisagrees is set when a bank locator names another channel than
	// its locator.
	BankDisagrees bool `json:"bankDisagrees,omitempty"`
	// Nodes is set when the nodes of the bank locators name nothing: their
	// count is no multiple of the processor records.
	Nodes string `json:"nodes,omitempty"`
}

// The placement of one slot inside the pass.
type passSlot struct {
	live          bool
	placed        bool
	why           string
	socket        optInt
	hub           bool
	key           chanKey
	hasKey        bool
	chname        string
	poskey        int
	channel, pos  int
	idx           string
	by            string
	label         string // the label of the manual of an unnamed slot
	chBy          string // where the channel came from: name, bank or family:<id>
	familyNamedCh bool   // the family table named the channel
}

// chanKey is a channel of a socket before it gets its number: a memory
// controller and a letter or a number.
type chanKey struct {
	mc    int
	s     string
	n     int
	isNum bool
}

func (k chanKey) order() string {
	if k.isNum {
		return fmt.Sprintf("%06d\x00%06d", k.mc, k.n)
	}
	return fmt.Sprintf("%06d\x00%s", k.mc, k.s)
}

// pass is the state of the pass over one board.
type pass struct {
	dimms   []Dimm
	hints   textHints
	toks    []tokens
	slots   []passSlot
	live    []int
	facts   *Facts
	proc    Processor
	maker   string
	sockmap map[word]int // which processor a word of the names is (a family row)
	S       int
}

var flashSlotTypes = map[string]bool{"FLASH": true, "ROM": true, "EPROM": true, "FEPROM": true, "EEPROM": true}

// placeNames runs the pass over the slots of a text and returns the facts of
// the board.
func placeNames(dimms []Dimm, hints textHints) Facts {
	f := Facts{Pass: true, Sockets: len(hints.cpus)}
	for _, c := range hints.cpus {
		f.Present = append(f.Present, c.status)
	}
	_, proc := boardProcessor(hints.cpus)
	p := &pass{dimms: dimms, hints: hints, facts: &f, proc: proc, S: len(hints.cpus), maker: makerOf(hints)}
	f.Processor, f.Maker = proc, p.maker
	p.proc.Hub = false // memory on the chipset is not read yet
	p.slots = make([]passSlot, len(dimms))
	p.toks = make([]tokens, len(dimms))
	for i := range dimms {
		d := &dimms[i]
		d.Socket, d.Channel, d.SocketBy, d.ChannelBy = 0, 0, "", ""
		d.Position, d.ChannelName, d.PlaceBy, d.Family, d.Idx, d.NotSlot, d.Unnamed, d.Label, d.Aside = 0, "", "", "", "", "", false, "", ""
	}
	phantoms := p.phantoms()
	for i := range dimms {
		d := &dimms[i]
		switch {
		case flashSlotTypes[strings.ToUpper(strings.TrimSpace(d.Type))]:
			d.NotSlot = NotSlotFlash
		case len(phantoms) > 0 && phantoms[squash(d.Locator)]:
			if d.Populated {
				f.PhantomsPopulated++
				p.live = append(p.live, i)
			} else {
				d.NotSlot = NotSlotPhantom
				f.Phantoms++
			}
		default:
			p.live = append(p.live, i)
		}
	}
	for _, i := range p.live {
		p.slots[i].live = true
		p.toks[i] = readNames(dimms[i].passLocator, dimms[i].BankLocator)
	}
	if len(p.live) == 0 {
		return f
	}
	p.unnamed()
	if !p.applyFamily() {
		p.generic()
	}
	p.rank()
	p.write()
	// Names that stand twice.
	seen := map[[2]string]bool{}
	for _, i := range p.live {
		if p.toks[i].lform == "" {
			continue
		}
		k := [2]string{strings.TrimSpace(dimms[i].Locator), strings.TrimSpace(dimms[i].BankLocator)}
		if seen[k] {
			f.Duplicates++
		}
		seen[k] = true
	}
	f.Hub = p.proc.Hub
	// The channel count and the slots of a channel the checks hold the
	// board to: of the family row where it gives them, else of the
	// processor (which a family row of kind names may have set).
	if f.Channels == 0 && p.proc.Channels > 0 {
		f.Channels = p.proc.Channels
		if f.ChannelsBy == "" {
			f.ChannelsBy = "processor"
		}
	}
	if f.PerChannel == 0 {
		f.PerChannel = p.proc.PerChannel
	}
	return f
}

// squash is a name in capitals without anything but letters and digits.
func squash(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// write puts the result of the pass into the slots.
func (p *pass) write() {
	for _, i := range p.live {
		s, d := &p.slots[i], &p.dimms[i]
		d.Unnamed = p.toks[i].unnamed() || s.label != ""
		if s.label != "" {
			d.Label = s.label
		}
		if !s.placed {
			// A socket without a channel is drawn without channels; how
			// the place was found is said only of a placed slot.
			if s.socket.ok && !s.hub {
				d.Socket, d.SocketBy = Num(s.socket.v), s.by
			}
			d.Family, d.Aside = p.facts.Family, s.why
			continue
		}
		d.SocketBy, d.ChannelBy, d.PlaceBy = s.by, s.chBy, s.by
		if s.hub {
			d.Socket = 0
		} else {
			d.Socket = Num(s.socket.v)
		}
		d.Channel, d.Position, d.ChannelName, d.Idx = Num(s.channel), Num(s.pos), s.chname, s.idx
		d.Family = p.facts.Family
	}
}

// Makers whose documents describe the board.
var (
	makerPrefixes = []struct{ prefix, maker string }{
		{"dell", "dell"}, {"hpe", "hp"}, {"hewlett", "hp"}, {"hp", "hp"}, {"compaq", "hp"}, {"supermicro", "supermicro"},
		{"super micro", "supermicro"}, {"huawei", "huawei"}, {"xfusion", "huawei"}, {"lenovo", "lenovo"}, {"ibm", "ibm"},
		{"intel", "intel"}, {"inspur", "inspur"}, {"giga", "gigabyte"}, {"asus", "asus"}, {"asrock", "asrock"},
		{"quanta", "quanta"}, {"qct", "quanta"}, {"sugon", "sugon"}, {"fujitsu", "fujitsu"}, {"fsc", "fujitsu"},
		{"cisco", "cisco"}, {"tyan", "tyan"}, {"oracle", "sun"}, {"sun ", "sun"}, {"sun", "sun"}, {"nec", "nec"}, {"msi", "msi"},
		{"micro-star", "msi"},
	}
	boardMakers  = map[string]bool{"supermicro": true, "dell": true, "intel": true, "tyan": true, "asus": true, "asrock": true, "gigabyte": true, "quanta": true, "msi": true, "hp": true, "lenovo": true, "ibm": true, "fujitsu": true, "sun": true}
	systemMakers = map[string]bool{"dell": true, "hp": true, "lenovo": true, "ibm": true, "fujitsu": true, "cisco": true, "sun": true, "huawei": true, "inspur": true, "nec": true}
	reDellPart   = regexp.MustCompile(`(?:^| )0[0-9A-Z]{5}$`)
)

func normMaker(v string) string {
	l := strings.ToLower(strings.TrimSpace(v))
	for _, m := range makerPrefixes {
		if strings.HasPrefix(l, m.prefix) {
			return m.maker
		}
	}
	return ""
}

// makerOf is the maker whose documents describe the board: a system maker
// that builds its own boards, else the maker the board string starts with,
// else Dell for a PowerEdge product or for a board named by a Dell part
// number (0 and five letters or digits), else whatever is named.
func makerOf(h textHints) string {
	s, b := normMaker(h.sysVendor), normMaker(h.board)
	dellPart := reDellPart.MatchString(h.board)
	if s == "cisco" && (dellPart || strings.HasPrefix(h.product, "PowerEdge")) {
		return "dell"
	}
	if systemMakers[s] {
		return s
	}
	if boardMakers[b] {
		return b
	}
	if strings.HasPrefix(h.product, "PowerEdge") || dellPart {
		return "dell"
	}
	if s != "" {
		return s
	}
	return b
}

// spelledBanks are the bank locator forms that spell channel and slot.
var spelledBanks = map[string]bool{"p-node-channel-dimm": true, "p-channel-dimm": true, "node-channel-dimm": true,
	"node-channel-dimm-words": true, "socket-channel-dimm": true}

// socketOf turns a processor word and its number into a socket: from the
// row of the family that names the words, else from 0 for the P of a bank
// locator and the first digit of DIMM012, from 1 for PROC, and from 0 when
// a slot of the board carries 0 with the word, else from 1.
func (p *pass) socketOf(w word, zero map[string]bool) int {
	if s, ok := p.sockmap[word{w.w, w.n, true}]; ok {
		return s
	}
	switch w.w {
	case "BP", "DIGIT":
		return w.n
	case "PROC":
		return w.n - 1
	}
	if zero[w.w] {
		return w.n
	}
	return w.n - 1
}

// zeros are the processor words of the board that carry a 0.
func (p *pass) zeros() map[string]bool {
	z := map[string]bool{}
	for _, i := range p.live {
		t := p.toks[i]
		for _, w := range []word{t.cpu, t.bcpu} {
			if w.ok && w.n == 0 {
				z[w.w] = true
			}
		}
	}
	return z
}

// sockets decides the socket of every slot (the fix socket-rules): the
// processor word, a Node of the bank locator, one processor record, the
// processor of the slots that share a channel letter, and the channel
// letters split over the processors in order.
func (p *pass) sockets() {
	if !stage(FixSocketRules) {
		return
	}
	set := func(i, s int, by string) { p.slots[i].socket, p.slots[i].by = some(s), by }
	has := func(i int) bool { return p.slots[i].socket.ok }
	zero := p.zeros()
	for _, i := range p.live {
		t := p.toks[i]
		switch {
		case t.cpu.ok:
			set(i, p.socketOf(t.cpu, zero), ByName)
		case t.bcpu.ok:
			set(i, p.socketOf(t.bcpu, zero), ByBank)
		}
	}
	nodeSet := map[int]bool{}
	for _, i := range p.live {
		if t := p.toks[i]; t.node.ok {
			nodeSet[t.node.v] = true
		}
	}
	var nodes []int
	for n := range nodeSet {
		nodes = append(nodes, n)
	}
	sort.Ints(nodes)
	index := func(n int) int { return sort.SearchInts(nodes, n) }
	S := p.S
	if len(nodes) > 0 && S >= 1 {
		switch {
		case len(nodes) <= S:
			for _, i := range p.live {
				if t := p.toks[i]; !has(i) && t.node.ok {
					n := t.node.v
					switch {
					case len(nodes) == S:
						set(i, index(n), ByNodeOf)
					case nodeSet[0]:
						set(i, n, ByNodeOf)
					default:
						set(i, n-1, ByNodeOf)
					}
				}
			}
		case len(nodes)%S == 0:
			per := len(nodes) / S
			for _, i := range p.live {
				if t := &p.toks[i]; t.node.ok {
					t.mc = some(index(t.node.v) % per)
					if !has(i) {
						set(i, index(t.node.v)/per, ByNodeOf)
					}
				}
			}
		default:
			p.facts.Nodes = fmt.Sprintf("%d nodes on %d sockets", len(nodes), S)
		}
	}
	if S == 1 {
		for _, i := range p.live {
			if !has(i) {
				set(i, 0, ByOneSocket)
			}
		}
	}
	// Slots that share a channel letter share the socket.
	mate := map[string]map[int]bool{}
	for _, i := range p.live {
		if t := p.toks[i]; has(i) && t.chl != "" {
			if mate[t.chl] == nil {
				mate[t.chl] = map[int]bool{}
			}
			mate[t.chl][p.slots[i].socket.v] = true
		}
	}
	for _, i := range p.live {
		if t := p.toks[i]; !has(i) && t.chl != "" && len(mate[t.chl]) == 1 {
			for s := range mate[t.chl] {
				set(i, s, ByMate)
			}
		}
	}
	// Channel letters that run over the processors: the letters of the
	// board, in order, split evenly over its sockets, when no slot says
	// otherwise.
	var rest []int
	for _, i := range p.live {
		if !has(i) && p.toks[i].chl != "" {
			rest = append(rest, i)
		}
	}
	C := p.proc.Channels
	if len(rest) > 0 && S > 1 && C > 0 {
		set1 := map[string]bool{}
		for _, i := range p.live {
			if t := p.toks[i]; t.chl != "" && !t.cpu.ok {
				set1[t.chl] = true
			}
		}
		var letters []string
		for l := range set1 {
			letters = append(letters, l)
		}
		sort.Strings(letters)
		if len(letters)%S == 0 && len(letters)/S <= C {
			k := len(letters) / S
			want := map[string]int{}
			for j, l := range letters {
				want[l] = j / k
			}
			agree := true
			for _, i := range p.live {
				if t := p.toks[i]; has(i) && t.chl != "" && !t.cpu.ok && p.slots[i].socket.v != want[t.chl] {
					agree = false
				}
			}
			if agree {
				for _, i := range rest {
					set(i, want[p.toks[i].chl], ByLetters)
				}
			}
		}
	}
}

// letterRole is what the letter of A1 and of 1A is on this board (the fix
// letter-meaning). Where slots carry such a name beside a bank locator that
// spells channel and slot, the pairing decides: the letter is the channel
// when letters and bank channels pair one to one; for 1A the digit is the
// channel when digits and bank channels pair one to one; else the name is a
// label. A bank locator P1_BANK2 groups the slots of one channel. Without
// such a bank locator the letter is the channel, except for 1A on
// Supermicro boards, where the digit is the channel and the letter the slot.
func (p *pass) letterRole(spelled map[int]bool) string {
	var both, dls []int
	for _, i := range p.live {
		t := p.toks[i]
		if (t.ln || t.dl) && spelled[i] {
			both = append(both, i)
		}
		if t.dl {
			dls = append(dls, i)
		}
	}
	if len(dls) > 0 {
		allBank := true
		for _, i := range dls {
			if p.toks[i].bform != "p-bank" {
				allBank = false
			}
		}
		if allBank {
			type g struct {
				w word
				b optInt
			}
			groups := map[g][]int{}
			var order []g
			for _, i := range dls {
				t := p.toks[i]
				k := g{t.bcpu, t.bank}
				if _, ok := groups[k]; !ok {
					order = append(order, k)
				}
				groups[k] = append(groups[k], i)
			}
			same := func(f func(t tokens) string) bool {
				for _, k := range order {
					v := groups[k]
					if len(v) <= 1 {
						return false
					}
					vals := map[string]bool{}
					for _, i := range v {
						vals[f(p.toks[i])] = true
					}
					if len(vals) != 1 {
						return false
					}
				}
				return true
			}
			if same(func(t tokens) string { return t.dlL }) {
				return "letter"
			}
			if same(func(t tokens) string { return strconv.Itoa(t.dlN) }) {
				return "digit"
			}
		}
	}
	if len(both) == 0 {
		if p.maker == "supermicro" && len(dls) > 0 {
			return "digit"
		}
		return "letter"
	}
	type bankKey struct {
		w   word
		n   optInt
		chn optInt
	}
	type valKey struct {
		w word
		n optInt
		v string
	}
	oneToOne := func(val func(t tokens) string) bool {
		a2b := map[valKey]map[bankKey]bool{}
		b2a := map[bankKey]map[string]bool{}
		for _, i := range both {
			t := p.toks[i]
			k := bankKey{t.bcpu, t.node, t.chn}
			v := val(t)
			a := valKey{t.bcpu, t.node, v}
			if a2b[a] == nil {
				a2b[a] = map[bankKey]bool{}
			}
			a2b[a][k] = true
			if b2a[k] == nil {
				b2a[k] = map[string]bool{}
			}
			b2a[k][v] = true
		}
		for _, x := range a2b {
			if len(x) != 1 {
				return false
			}
		}
		for _, x := range b2a {
			if len(x) != 1 {
				return false
			}
		}
		return true
	}
	if oneToOne(func(t tokens) string {
		if t.ln {
			return t.lnL
		}
		return t.dlL
	}) {
		return "letter"
	}
	allDL := true
	for _, i := range both {
		if !p.toks[i].dl {
			allDL = false
		}
	}
	if allDL && oneToOne(func(t tokens) string { return strconv.Itoa(t.dlN) }) {
		return "digit"
	}
	return "label"
}

// generic places the slots whose names spell the place.
func (p *pass) generic() {
	spelled := map[int]bool{}
	for _, i := range p.live {
		if spelledBanks[p.toks[i].bform] {
			spelled[i] = true
		}
	}
	if stage(FixLetterMeaning) {
		role := p.letterRole(spelled)
		for _, i := range p.live {
			if t := p.toks[i]; t.ln || t.dl {
				p.facts.Letter = role
				break
			}
		}
		for _, i := range p.live {
			t := &p.toks[i]
			var kind, L string
			var dg int
			switch {
			case t.dl:
				kind, L, dg = "dl", t.dlL, t.dlN
			case t.ln:
				kind, L, dg = "ln", t.lnL, t.lnN
			default:
				continue
			}
			t.dl, t.ln = false, false
			if spelled[i] {
				t.bchn, t.bpos = t.chn, t.pos
				t.chn, t.pos = optInt{}, optInt{}
			} else if t.pos.ok {
				t.bpos, t.pos = t.pos, optInt{}
			}
			switch {
			case role == "letter":
				t.chl, t.pos, t.chBank = L, some(dg), false
			case role == "digit" && kind == "dl":
				t.chn, t.pos, t.chname, t.chBank = some(dg), some(int(L[0])-65), strconv.Itoa(dg), false
			case spelled[i]:
				t.chn, t.pos, t.chBank = t.bchn, t.bpos, true
			case t.chl != "":
				t.pos = some(dg)
			}
		}
	}
	p.sockets()
	for _, i := range p.live {
		t, s := p.toks[i], &p.slots[i]
		switch {
		case t.unnamed() && s.label == "":
			s.why = "no-name"
			continue
		case t.lform == "?":
			s.why = "unknown-form"
			continue
		case !s.socket.ok:
			s.why = "no-socket"
			continue
		}
		idx := ""
		if spelled[i] {
			if t.bpos.ok {
				idx = strconv.Itoa(t.bpos.v)
			} else if t.pos.ok {
				idx = strconv.Itoa(t.pos.v)
			}
		}
		var key chanKey
		hasKey := false
		pk := 0
		name := ""
		switch {
		case t.chl != "":
			key, hasKey = chanKey{s: t.chl}, true
			if strings.HasPrefix(t.lform, "controller-channel") {
				key.mc = t.mc.v
			}
			switch {
			case t.pos.ok:
				pk = t.pos.v
			case t.num.ok:
				pk = t.num.v
			}
		case t.chn.ok:
			mc := 0
			if t.mc.ok {
				mc = t.mc.v
			}
			if t.node.ok && t.bcpu.ok {
				mc = t.node.v
			}
			key, hasKey = chanKey{mc: mc, n: t.chn.v, isNum: true}, true
			name = t.chname
			if t.pos.ok {
				pk = t.pos.v
			}
		}
		if !hasKey {
			s.why = "no-channel"
			continue
		}
		s.placed, s.key, s.hasKey, s.chname, s.poskey, s.idx = true, key, true, name, pk, idx
		s.chBy = ByName
		if t.chBank {
			s.chBy = ByBank
		}
	}
	// A bank locator that disagrees with a locator that spells the
	// channel: the locator stands.
	type sk struct {
		s int
		l string
	}
	seen := map[sk]map[[2]optInt]bool{}
	for _, i := range p.live {
		t := p.toks[i]
		if p.slots[i].placed && t.chl != "" && spelled[i] {
			k := sk{p.slots[i].socket.v, t.chl}
			if seen[k] == nil {
				seen[k] = map[[2]optInt]bool{}
			}
			ch := t.chn
			if t.bchn.ok {
				ch = t.bchn
			}
			seen[k][[2]optInt{t.node, ch}] = true
		}
	}
	for _, v := range seen {
		if len(v) > 1 {
			p.facts.BankDisagrees = true
		}
	}
}

// rank numbers the channels of each socket and the slots of each channel
// (the fix channel-rank): the place of a channel among the channels of its
// socket that have slots, and of a slot among the slots of its channel,
// both from 0. Without the fix the channel is its letter or number as the
// name gives it and the position the slot number of the name.
func (p *pass) rank() {
	bySock := map[int][]int{}
	var socks []int
	for _, i := range p.live {
		s := &p.slots[i]
		if !s.placed || !s.hasKey {
			continue
		}
		k := s.socket.v
		if s.hub {
			k = -1
		}
		if _, ok := bySock[k]; !ok {
			socks = append(socks, k)
		}
		bySock[k] = append(bySock[k], i)
	}
	for _, sock := range socks {
		idx := bySock[sock]
		keySet := map[chanKey]bool{}
		for _, i := range idx {
			keySet[p.slots[i].key] = true
		}
		var keys []chanKey
		for k := range keySet {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(a, b int) bool { return keys[a].order() < keys[b].order() })
		mcs := map[int]bool{}
		for _, k := range keys {
			mcs[k.mc] = true
		}
		several := len(mcs) > 1
		for _, i := range idx {
			s := &p.slots[i]
			switch {
			case s.chname == "?":
				s.chname = ""
			case s.chname == "":
				c := s.key.s
				if s.key.isNum {
					c = strconv.Itoa(s.key.n)
				}
				switch {
				case several && s.key.isNum:
					s.chname = fmt.Sprintf("%d.%s", s.key.mc, c)
				case several:
					s.chname = fmt.Sprintf("%d%s", s.key.mc, c)
				default:
					s.chname = c
				}
			}
			if !stage(FixChannelRank) {
				s.channel, s.pos = rawChannel(s.key), s.poskey
				continue
			}
			for j, k := range keys {
				if k == s.key {
					s.channel = j
				}
			}
		}
		if !stage(FixChannelRank) {
			continue
		}
		for _, k := range keys {
			var members []int
			pks := map[int]bool{}
			for _, i := range idx {
				if p.slots[i].key == k {
					members = append(members, i)
					pks[p.slots[i].poskey] = true
				}
			}
			var sorted []int
			for v := range pks {
				sorted = append(sorted, v)
			}
			sort.Ints(sorted)
			for _, i := range members {
				p.slots[i].pos = sort.SearchInts(sorted, p.slots[i].poskey)
			}
		}
	}
}

// rawChannel is the channel a name gives before the channels are ranked:
// the number, or the place of the letter from A.
func rawChannel(k chanKey) int {
	if k.isNum {
		return k.n
	}
	if n, err := strconv.Atoi(k.s); err == nil {
		return n
	}
	if len(k.s) == 1 && k.s[0] >= 'A' && k.s[0] <= 'Z' {
		return int(k.s[0] - 'A')
	}
	return 0
}
