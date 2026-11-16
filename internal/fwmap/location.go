package fwmap

import (
	"strconv"
	"strings"

	"github.com/agenticode/my/internal/decode/ipmi"
	"github.com/agenticode/my/internal/model"
)

// Place is where a record says it happened, from its arguments.
type Place struct {
	// Labels are the modules the record names, as candidate slot names.
	Labels []string
	// Location is the place as the vendor wrote it, unchanged.
	Location                                                   string
	Socket, Channel, Position, Rank, Device, Bank, Row, Column model.Opt[int]
	Address                                                    model.Opt[uint64]
	// Count is the number of errors an argument gives; zero when none.
	Count  int
	Serial string
	Part   string
	// Args are the arguments, at most adapter.MaxArgs.
	Args []string
}

// PlaceOf reads the arguments of a record by the roles a line gives them,
// in the location form of the table.
func PlaceOf(form string, roles, args []string) Place {
	if len(args) > 32 {
		args = args[:32]
	}
	p := Place{Args: args}
	channelValid, positionValid := true, true
	for i, role := range roles {
		if i >= len(args) {
			break
		}
		a := strings.TrimSpace(args[i])
		switch role {
		case "location", "locations":
			if p.Location == "" {
				p.Location = a
			}
			p.Labels = append(p.Labels, SplitLocations(a)...)
		case "socket":
			p.Socket = smallInt(a)
		case "channel":
			p.Channel = channelIndex(a)
		case "position":
			p.Position = smallInt(a)
		case "rank":
			p.Rank = smallInt(a)
		case "device":
			p.Device = smallInt(a)
		case "bank":
			p.Bank = smallInt(a)
		case "row":
			p.Row = number(a)
		case "column":
			p.Column = number(a)
		case "address":
			if v, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(a), "0x"), 16, 64); err == nil {
				p.Address = model.Some(v)
			}
		case "count":
			if v, err := strconv.Atoi(a); err == nil && v > 0 && v < 1<<30 {
				p.Count = v
			}
		case "serial":
			p.Serial = a
		case "part":
			p.Part = a
		case "channel-valid":
			channelValid = a != "0"
		case "position-valid":
			positionValid = a != "0"
		}
	}
	// The form of the OpenBMC registry says when channel and DIMM are not
	// known: a parity error names them only when they are.
	if !channelValid {
		p.Channel, p.Position = model.Opt[int]{}, model.Opt[int]{}
	}
	if !positionValid {
		p.Position = model.Opt[int]{}
	}
	if p.Location == "" && p.Socket.Valid {
		var parts []string
		for _, r := range []struct {
			name string
			v    model.Opt[int]
		}{{"socket", p.Socket}, {"channel", p.Channel}, {"position", p.Position}, {"rank", p.Rank}} {
			if r.v.Valid {
				parts = append(parts, r.name+" "+strconv.Itoa(r.v.V))
			}
		}
		p.Location = strings.Join(parts, ", ")
	}
	return p
}

// SplitLocations splits "DIMM_A1, DIMM_A2" and keeps what looks like the
// name of a slot: a DIMM name, a processor with a module number, or a
// short designator such as "A1".
func SplitLocations(s string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' }) {
		p = strings.TrimSpace(p)
		switch names := ipmi.ModuleNames(p); {
		case p == "":
		case ipmi.IsProcessorModule(p):
			out = append(out, strings.Join(strings.Fields(p), " "))
		case len(names) > 0:
			out = append(out, names...)
		case len(p) <= 6 && !strings.ContainsAny(p, " ()") && strings.ContainsAny(p, "0123456789"):
			out = append(out, p)
		}
	}
	return out
}

// channelIndex reads a channel given as a number or as a letter.
func channelIndex(s string) model.Opt[int] {
	if len(s) == 1 && s[0] >= 'A' && s[0] <= 'Z' {
		return model.Some(int(s[0] - 'A'))
	}
	if len(s) == 1 && s[0] >= 'a' && s[0] <= 'z' {
		return model.Some(int(s[0] - 'a'))
	}
	return smallInt(s)
}

func smallInt(s string) model.Opt[int] {
	n := 0
	if s == "" || len(s) > 4 {
		return model.Opt[int]{}
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return model.Opt[int]{}
		}
		n = n*10 + int(s[i]-'0')
	}
	return model.Some(n)
}

// number reads a row or a column, in decimal or with 0x in hexadecimal.
func number(s string) model.Opt[int] {
	base := 10
	if l := strings.ToLower(s); strings.HasPrefix(l, "0x") {
		s, base = s[2:], 16
	}
	v, err := strconv.ParseInt(s, base, 32)
	if err != nil || v < 0 {
		return model.Opt[int]{}
	}
	return model.Some(int(v))
}
