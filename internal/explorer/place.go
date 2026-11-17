package explorer

import "sort"

// The checks of the board. They decide how a slot is drawn from what the
// readers returned: on its CPU and channel, on its CPU without channels,
// or set aside by its name. They change no reader.

// Placement states.
const (
	Placed    = "placed"
	NoChannel = "no-channel"
	Unplaced  = "unplaced"
	// NotASlot is a memory device that is no slot of the board: the
	// board leaves it out.
	NotASlot = "not-a-slot"
)

// Reasons of a placement that is not Placed.
const (
	WhyNone            = "none"             // no test named a socket or a channel
	WhyRange           = "range"            // a number too large to be a socket or a channel
	WhyNegative        = "negative"         // a socket below 0, or one of the test that gave it
	WhyAssumed         = "assumed"          // no test named the socket, on a board of several
	WhyRun             = "run"              // the sockets do not run from 0 or 1 without gaps, or are more than the board has
	WhyCrowd           = "crowd"            // more than 3 slots in one channel
	WhyNegativeChannel = "negative-channel" // a channel below 0
	WhyUnknownForm     = "unknown-form"     // a name no form of the name table reads
	WhyNoTable         = "no-table"         // a slot number without a channel, on a board without a table of its family
)

// Placement is where the board draws one slot.
type Placement struct {
	State   string `json:"state"`
	Reason  string `json:"reason,omitempty"`
	Socket  int    `json:"socket"`
	Channel int    `json:"channel"`
	// Position is the place of the slot in its channel.
	Position int `json:"position,omitempty"`
	// Assumed is set when no test named the socket.
	Assumed bool `json:"assumed,omitempty"`
}

// MaxBoardSockets is the most sockets a board is drawn with.
const MaxBoardSockets = 8

// Place puts each slot on the board or aside. sockets is the number of
// sockets of the board (its processor records), 0 when not known;
// packages is the number of CPU packages seen elsewhere (cache readings),
// which stands in for the board's when that is not known, for the slots
// whose socket no test named. Slots the pass of the second round read are
// checked as PlaceBoard checks them, without what the reader found of the
// board beside them.
func Place(dimms []Dimm, sockets, packages int) []Placement {
	f := Facts{Sockets: sockets, Packages: packages}
	for _, d := range dimms {
		if d.PlaceBy != "" || d.Aside != "" || d.NotSlot != "" {
			f.Pass = true
		}
	}
	return PlaceBoard(dimms, f)
}

// PlaceBoard puts each slot on the board or aside, with what the reader
// found of the board: the checks of the first round for slots its readers
// placed, and for slots the pass of the second round placed the checks of
// the second round, which hold a board to the processors it has, to the
// channels of its processor and to the slots a channel of its platform
// holds.
func PlaceBoard(dimms []Dimm, f Facts) []Placement {
	if f.Pass {
		return placeNamed(dimms, f)
	}
	return placeFirst(dimms, f.Sockets, f.Packages)
}

// placeFirst holds the checks of the first round.
func placeFirst(dimms []Dimm, sockets, packages int) []Placement {
	p := make([]Placement, len(dimms))
	un := func(i int, why string) { p[i] = Placement{State: Unplaced, Reason: why} }
	for i, d := range dimms {
		p[i] = Placement{State: Placed, Assumed: d.SocketBy == ""}
		s, ok1 := d.Socket.Int()
		c, ok2 := d.Channel.Int()
		switch {
		case d.NotSlot != "":
			p[i] = Placement{State: NotASlot, Reason: d.NotSlot}
		case d.Aside != "":
			// The pass of the second round placed no slot here.
			switch d.Aside {
			case "no-channel":
				p[i] = Placement{State: NoChannel, Reason: WhyNoTable, Socket: s}
			case "no-socket":
				p[i] = Placement{State: Unplaced, Reason: WhyAssumed, Assumed: true}
			case "no-name":
				un(i, WhyNone)
			default:
				un(i, d.Aside)
			}
		case d.SocketBy == "" && d.ChannelBy == "":
			un(i, WhyNone)
		case !ok1 || !ok2:
			un(i, WhyRange)
		default:
			p[i].Socket, p[i].Channel, p[i].Position = s, c, int(d.Position)
		}
	}
	bad := map[string]bool{}
	for i, d := range dimms {
		if p[i].State == Placed && p[i].Socket < 0 {
			bad[d.SocketBy] = true
		}
	}
	for i, d := range dimms {
		if p[i].State == Placed && (p[i].Socket < 0 || d.SocketBy != "" && bad[d.SocketBy]) {
			un(i, WhyNegative)
		}
	}
	board := sockets
	if board == 0 {
		board = packages
	}
	for i := range dimms {
		if p[i].State == Placed && p[i].Assumed && board > 1 {
			un(i, WhyAssumed)
		}
	}
	seen := map[int]bool{}
	var socks []int
	for i := range dimms {
		if p[i].State == Placed && !seen[p[i].Socket] {
			seen[p[i].Socket] = true
			socks = append(socks, p[i].Socket)
		}
	}
	sort.Ints(socks)
	n := len(socks)
	if n > 0 && !((socks[0] == 0 || socks[0] == 1) && socks[n-1]-socks[0] == n-1 && n <= MaxBoardSockets && (sockets == 0 || n <= sockets)) {
		for i := range dimms {
			if p[i].State == Placed {
				un(i, WhyRun)
			}
		}
	}
	type key struct{ s, c int }
	per := map[key]int{}
	for i := range dimms {
		if p[i].State == Placed {
			per[key{p[i].Socket, p[i].Channel}]++
		}
	}
	dropped := map[int]string{}
	for k, n := range per {
		if n > 3 {
			dropped[k.s] = WhyCrowd
		}
	}
	for i := range dimms {
		if p[i].State == Placed && p[i].Channel < 0 && dropped[p[i].Socket] == "" {
			dropped[p[i].Socket] = WhyNegativeChannel
		}
	}
	for i := range dimms {
		why := dropped[p[i].Socket]
		if p[i].State != Placed || why == "" {
			continue
		}
		if p[i].Assumed {
			p[i] = Placement{State: Unplaced, Reason: why, Assumed: true}
			continue
		}
		p[i].State, p[i].Reason, p[i].Channel = NoChannel, why, 0
	}
	return p
}

// Reasons of the checks of the second round.
const (
	WhySamePlace    = "same-place"    // two slots name one place
	WhyMoreChannels = "more-channels" // a socket shows more channels than its processor has
)

// placeNamed places the slots the pass of the second round read and checks
// the board: the sockets run from 0 without a gap and are no more than the
// processor records and 8; no two slots share a place; no socket has more
// channels than its processor and no channel more slots than its platform
// holds. An empty slot named by the manual is drawn, and counts in none of
// the checks.
func placeNamed(dimms []Dimm, f Facts) []Placement {
	p := make([]Placement, len(dimms))
	for i, d := range dimms {
		s, _ := d.Socket.Int()
		switch {
		case d.NotSlot != "":
			p[i] = Placement{State: NotASlot, Reason: d.NotSlot}
		case d.PlaceBy != "":
			c, _ := d.Channel.Int()
			pos, _ := d.Position.Int()
			p[i] = Placement{State: Placed, Socket: s, Channel: c, Position: pos}
		case d.Aside == "no-channel":
			p[i] = Placement{State: NoChannel, Reason: WhyNoTable, Socket: s}
		case d.Aside == "no-socket":
			p[i] = Placement{State: Unplaced, Reason: WhyAssumed, Assumed: true}
		case d.Aside == "no-name" || d.Aside == "":
			p[i] = Placement{State: Unplaced, Reason: WhyNone}
		default:
			p[i] = Placement{State: Unplaced, Reason: d.Aside}
		}
	}
	var drawn, placed []int
	for i := range p {
		if p[i].State == Placed {
			drawn = append(drawn, i)
			if dimms[i].Label == "" {
				placed = append(placed, i)
			}
		}
	}
	if len(placed) == 0 {
		return p
	}
	aside := func(idx []int, why string) {
		for _, i := range idx {
			p[i] = Placement{State: Unplaced, Reason: why}
		}
	}
	// Sockets: from 0 without a gap, no more than the processor records,
	// at most 8. A slot drawn without channels stands on its socket too.
	if !f.Hub {
		seen := map[int]bool{}
		for _, i := range placed {
			seen[p[i].Socket] = true
		}
		for i := range p {
			if p[i].State == NoChannel {
				seen[p[i].Socket] = true
			}
		}
		n := len(seen)
		ok := n <= MaxBoardSockets && (f.Sockets == 0 || n <= f.Sockets)
		for s := range seen {
			if s < 0 || s >= n {
				ok = false
			}
		}
		if !ok {
			var all []int
			for i := range p {
				if p[i].State == Placed || p[i].State == NoChannel {
					all = append(all, i)
				}
			}
			aside(all, WhyRun)
			return p
		}
	}
	type place struct{ s, c, pos int }
	at := map[place]bool{}
	for _, i := range placed {
		k := place{p[i].Socket, p[i].Channel, p[i].Position}
		if at[k] {
			aside(drawn, WhySamePlace)
			return p
		}
		at[k] = true
	}
	channels, limit := f.Channels, f.PerChannel
	if limit == 0 {
		limit = 3
	}
	if channels == 0 {
		channels = 1 << 30 // no channel count: the board is drawn as the names say
	}
	bySocket := map[int][]int{}
	var socks []int
	for _, i := range placed {
		if _, ok := bySocket[p[i].Socket]; !ok {
			socks = append(socks, p[i].Socket)
		}
		bySocket[p[i].Socket] = append(bySocket[p[i].Socket], i)
	}
	sort.Ints(socks)
	for _, s := range socks {
		per := map[int]int{}
		most := 0
		for _, i := range bySocket[s] {
			per[p[i].Channel]++
			most = max(most, per[p[i].Channel])
		}
		why := ""
		switch {
		case len(per) > channels:
			why = WhyMoreChannels
		case most > limit:
			why = WhyCrowd
		}
		if why == "" {
			continue
		}
		for _, i := range drawn {
			if p[i].Socket == s {
				p[i] = Placement{State: NoChannel, Reason: why, Socket: s}
			}
		}
	}
	return p
}

// Reasons a board stands in the separate list: its own records do not tell
// the place of every slot.
const (
	ListUnnamed          = "unnamed-empty"                    // empty slots the firmware names not
	ListDuplicates       = "duplicated-names"                 // one name for two slots
	ListAbsent           = "absent-processor"                 // modules on a socket whose processor record says not installed
	ListProcessorUnknown = "processor-unknown"                // no channel count of the processor to check against
	ListRecordsMissing   = "processor-records-missing"        // the slots need more processors than the records name
	ListPhantom          = "module-in-a-slot-the-board-lacks" // a module in a record the manual has no slot for
	ListNoDevice         = "no-memory-device"                 // no memory device at all
	ListNotPlaced        = "not-placed"                       // none of the above
)

// Completeness says whether a board is placed in full: every slot of it on
// a socket, a channel and a position from its own names or from the table
// of its family, every check holding, the channel count known, no module on
// an absent processor. When it is not, Reason is why, in the words of the
// separate list.
type Completeness struct {
	Full   bool   `json:"full"`
	Reason string `json:"reason,omitempty"`
	// Absent counts the modules on sockets whose processor record says the
	// processor is not installed.
	Absent int `json:"absent,omitempty"`
	// Unchecked is set when no channel count is known to check the board
	// against.
	Unchecked bool `json:"unchecked,omitempty"`
}

// Complete judges a board from its slots, what the reader found of it and
// how it was placed.
func Complete(d []Dimm, f Facts, p []Placement) Completeness {
	var c Completeness
	live, placed, own := 0, 0, 0
	for i := range p {
		if p[i].State == NotASlot {
			continue
		}
		live++
		if p[i].State != Placed {
			continue
		}
		placed++
		if d[i].Label == "" {
			own++
		}
		s := p[i].Socket
		if d[i].Populated && !f.Hub && s >= 0 && s < len(f.Present) && f.Present[s] == 0 {
			c.Absent++
		}
	}
	c.Unchecked = placed > 0 && f.Channels == 0
	switch {
	case live == 0:
		c.Reason = ListNoDevice
	case f.PhantomsPopulated > 0:
		c.Reason = ListPhantom
	case f.Unnamed > 0:
		c.Reason = ListUnnamed
	case own == live && c.Absent > 0:
		c.Reason = ListAbsent
	case own == live && c.Unchecked:
		c.Reason = ListProcessorUnknown
	case own == live:
		c.Full = true
	case f.Duplicates > 0:
		c.Reason = ListDuplicates
	case f.Aside != "":
		c.Reason = f.Aside
	case !f.Named:
		// The records name no product and no board: no document can be
		// found for the board.
		c.Reason = "board-unknown"
	default:
		c.Reason = ListNotPlaced
		for i := range p {
			if p[i].Reason == WhyRun {
				c.Reason = ListRecordsMissing
			}
		}
	}
	return c
}
