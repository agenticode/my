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
)

// Placement is where the board draws one slot.
type Placement struct {
	State   string `json:"state"`
	Reason  string `json:"reason,omitempty"`
	Socket  int    `json:"socket"`
	Channel int    `json:"channel"`
	// Assumed is set when no test named the socket.
	Assumed bool `json:"assumed,omitempty"`
}

// MaxBoardSockets is the most sockets a board is drawn with.
const MaxBoardSockets = 8

// Place puts each slot on the board or aside. sockets is the number of
// sockets of the board (its processor records), 0 when not known;
// packages is the number of CPU packages seen elsewhere (cache readings),
// which stands in for the board's when that is not known, for the slots
// whose socket no test named.
func Place(dimms []Dimm, sockets, packages int) []Placement {
	p := make([]Placement, len(dimms))
	un := func(i int, why string) { p[i] = Placement{State: Unplaced, Reason: why} }
	for i, d := range dimms {
		p[i] = Placement{State: Placed, Assumed: d.SocketBy == ""}
		s, ok1 := d.Socket.Int()
		c, ok2 := d.Channel.Int()
		switch {
		case d.SocketBy == "" && d.ChannelBy == "":
			un(i, WhyNone)
		case !ok1 || !ok2:
			un(i, WhyRange)
		default:
			p[i].Socket, p[i].Channel = s, c
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
