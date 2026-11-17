package explorer

import "sort"

// Layout is what renderDimmMap draws, as data: the summary line, the
// badge of a memory type and speed that all modules share, and the rows of
// the board. A row holds the bank left of the CPU, the CPU, and the bank
// right of it; with two sockets the second CPU and its bank follow.
type Layout struct {
	Empty   bool        `json:"empty"`
	Summary []string    `json:"summary"`
	Badge   string      `json:"badge"`
	Rows    []LayoutRow `json:"rows"`
	Dimms   []Dimm      `json:"-"`
}

// LayoutRow is one row of sockets.
type LayoutRow struct {
	Socket Num          `json:"socket"`
	Dual   bool         `json:"dual"`
	Parts  []LayoutPart `json:"parts"`
}

// LayoutPart is a bank (Bank is "left" or "right") or a CPU.
type LayoutPart struct {
	Bank     string          `json:"bank,omitempty"`
	Channels []LayoutChannel `json:"channels,omitempty"`
	CPU      *LayoutCPU      `json:"cpu,omitempty"`
}

// LayoutChannel is one channel of a bank, its sticks in drawing order.
type LayoutChannel struct {
	Channel Num           `json:"channel"`
	Socket  Num           `json:"socket"`
	Sticks  []LayoutStick `json:"sticks"`
}

// LayoutStick is one slot as drawn: the index of its slot in the input,
// its classes and the size written on it.
type LayoutStick struct {
	Slot    int      `json:"slot"`
	Locator string   `json:"locator"`
	Classes []string `json:"classes"`
	Cap     string   `json:"cap"`
}

// LayoutCPU is the die of one socket: its name, the count and total size
// of its modules, and its cache levels.
type LayoutCPU struct {
	Socket Num           `json:"socket"`
	Name   string        `json:"name"`
	Sub    []string      `json:"sub"`
	Caches []LayoutCache `json:"caches"`
}

// LayoutCache is one cache level of a CPU.
type LayoutCache struct {
	Class string `json:"class"`
	Label string `json:"label"`
	Size  string `json:"size"`
	Bytes Num    `json:"bytes"`
}

var (
	reNumXfusion = jsre(`^DIMM\d\d(\d)`, "")
	reNumBank    = jsre(`_Dimm(\d+)`, "i")
	reNumDell    = jsre(`^[A-Z](\d+)$`, "i")
	reNumLetter  = jsre(`DIMM[A-Z](\d+)`, "i")
	reNumD       = jsre(`D(\d+)`, "i")
)

// DimmNum is the order of a slot within its channel: the third digit of
// DIMM###, the number of _Dimm# of the bank locator, the number of a name
// like A1, the number after DIMM and a letter, or the number after D.
// getDimmNum, memory.ts 1748-1764.
func DimmNum(d Dimm) Num {
	if m, ok := reNumXfusion.Match(d.Locator); ok {
		return ParseInt(m.Group(1))
	}
	if m, ok := reNumBank.Match(d.BankLocator); ok {
		return ParseInt(m.Group(1))
	}
	if m, ok := reNumDell.Match(d.Locator); ok {
		return ParseInt(m.Group(1))
	}
	if m, ok := reNumLetter.Match(d.Locator); ok {
		return ParseInt(m.Group(1))
	}
	if m, ok := reNumD.Match(d.Locator); ok {
		return ParseInt(m.Group(1))
	}
	return 0
}

// stickClasses are the classes of a stick: populated or empty, error, UE.
// buildDimmSlotHtml, memory.ts 1939-1967.
func stickClasses(d Dimm) []string {
	c := []string{"mb-stick-empty"}
	if d.Populated {
		c[0] = "mb-stick-populated"
	}
	if d.ErrorCount > 0 {
		c = append(c, "mb-stick-error")
	}
	if Or(d.UeCount, 0) > 0 {
		c = append(c, "mb-stick-ue")
	}
	return c
}

// cacheLevels are the cache bars of a CPU. buildCacheHtml, memory.ts 1644-1662.
func cacheLevels(socket Num, caches []CPUCache) []LayoutCache {
	out := []LayoutCache{}
	for _, c := range caches {
		if c.Socket != socket {
			continue
		}
		if c.L1Bytes > 0 {
			out = append(out, LayoutCache{"cache-l1", "L1", FormatBytes(float64(c.L1Bytes)), c.L1Bytes})
		}
		if c.L2Bytes > 0 {
			out = append(out, LayoutCache{"cache-l2", "L2", FormatBytes(float64(c.L2Bytes)), c.L2Bytes})
		}
		if c.L3Bytes > 0 {
			out = append(out, LayoutCache{"cache-l3", "L3", FormatBytes(float64(c.L3Bytes)), c.L3Bytes})
		}
		break
	}
	return out
}

type slotGroup struct {
	key   float64
	slots []int
}

// group puts the slots in groups by a number, in the order the numbers
// first appear; -0 and 0 are one group, as the keys of a Map.
func group(idx []int, key func(int) float64) []slotGroup {
	pos := map[float64]int{}
	var out []slotGroup
	for _, i := range idx {
		k := key(i) + 0
		p, ok := pos[k]
		if !ok {
			p = len(out)
			pos[k] = p
			out = append(out, slotGroup{key: k})
		}
		out[p].slots = append(out[p].slots, i)
	}
	return out
}

// byKey sorts groups by their number, as sort((a, b) => a[0] - b[0]).
func byKey(g []slotGroup) {
	sort.SliceStable(g, func(i, j int) bool { return g[i].key-g[j].key < 0 })
}

// Board arranges slots as the explorer draws them: one socket with half
// its channels left of the CPU, mirrored so that channel 0 is next to it,
// and the rest right of it; two sockets or more as [bank] [CPU 0] [CPU 1]
// [bank], the channels of the first mirrored. Sockets beyond the second
// are not drawn. renderDimmMap, memory.ts 1664-1852.
func Board(dimms []Dimm, caches []CPUCache) Layout {
	l := Layout{Dimms: dimms, Rows: []LayoutRow{}}
	if len(dimms) == 0 {
		l.Empty = true
		return l
	}
	all := make([]int, len(dimms))
	for i := range dimms {
		all[i] = i
	}
	sockets := group(all, func(i int) float64 { return float64(dimms[i].Socket) })
	var populated int
	var total, ce, ue Num
	var types, speeds []string
	seen := map[string]bool{}
	seenSpeed := map[string]bool{}
	for _, d := range dimms {
		ce += Or(d.CeCount, 0)
		ue += Or(d.UeCount, 0)
		if !d.Populated {
			continue
		}
		populated++
		total += d.SizeBytes
		if d.Type != "" && !seen[d.Type] {
			seen[d.Type] = true
			types = append(types, d.Type)
		}
		sp := d.ConfiguredSpeed
		if sp == "" {
			sp = d.Speed
		}
		if sp != "" && !seenSpeed[sp] {
			seenSpeed[sp] = true
			speeds = append(speeds, sp)
		}
	}
	if len(types) == 1 {
		l.Badge = types[0]
		if len(speeds) == 1 {
			l.Badge += " · " + speeds[0]
		}
	}
	l.Summary = []string{itoa(populated) + " / " + itoa(len(dimms)) + " slots populated", "Total: " + FormatBytes(float64(total))}
	if ce > 0 {
		l.Summary = append(l.Summary, "CE: "+NumString(float64(ce)))
	}
	if ue > 0 {
		l.Summary = append(l.Summary, "UE: "+NumString(float64(ue)))
	}
	byKey(sockets)

	stick := func(i int) LayoutStick {
		d := dimms[i]
		cap := ""
		if d.Populated {
			cap = FormatBytes(float64(d.SizeBytes))
		}
		return LayoutStick{Slot: i, Locator: d.Locator, Classes: stickClasses(d), Cap: cap}
	}
	bank := func(side string, chs []slotGroup, socket Num) LayoutPart {
		p := LayoutPart{Bank: side, Channels: []LayoutChannel{}}
		for _, ch := range chs {
			c := LayoutChannel{Channel: Num(ch.key), Socket: socket, Sticks: []LayoutStick{}}
			for _, i := range ch.slots {
				c.Sticks = append(c.Sticks, stick(i))
			}
			p.Channels = append(p.Channels, c)
		}
		return p
	}
	cpu := func(socket Num, slots []int) LayoutPart {
		pop := 0
		var size Num
		for _, i := range slots {
			if dimms[i].Populated {
				pop++
				size += dimms[i].SizeBytes
			}
		}
		return LayoutPart{CPU: &LayoutCPU{Socket: socket, Name: "CPU " + NumString(float64(socket)),
			Sub: []string{itoa(pop) + " DIMMs", FormatBytes(float64(size))}, Caches: cacheLevels(socket, caches)}}
	}
	channels := func(slots []int) []slotGroup {
		g := group(slots, func(i int) float64 { return float64(dimms[i].Channel) })
		byKey(g)
		return g
	}
	sortSticks := func(g []slotGroup, desc bool) {
		for _, ch := range g {
			s := ch.slots
			sort.SliceStable(s, func(a, b int) bool {
				na, nb := DimmNum(dimms[s[a]]), DimmNum(dimms[s[b]])
				if desc {
					return nb-na < 0
				}
				return na-nb < 0
			})
		}
	}
	if len(sockets) >= 2 {
		s0, s1 := sockets[0], sockets[1]
		ch0 := channels(s0.slots)
		reverse(ch0)
		sortSticks(ch0, true)
		ch1 := channels(s1.slots)
		sortSticks(ch1, false)
		id0, id1 := Num(s0.key), Num(s1.key)
		l.Rows = append(l.Rows, LayoutRow{Socket: id0, Dual: true, Parts: []LayoutPart{
			bank("left", ch0, id0), cpu(id0, s0.slots), cpu(id1, s1.slots), bank("right", ch1, id1)}})
		return l
	}
	s := sockets[0]
	id := Num(s.key)
	chs := channels(s.slots)
	half := int(ceil(float64(len(chs)) / 2))
	left := append([]slotGroup(nil), chs[:half]...)
	reverse(left)
	sortSticks(left, true)
	right := chs[half:]
	sortSticks(right, false)
	l.Rows = append(l.Rows, LayoutRow{Socket: id, Parts: []LayoutPart{bank("left", left, id), cpu(id, s.slots), bank("right", right, id)}})
	return l
}

func reverse(g []slotGroup) {
	for i, j := 0, len(g)-1; i < j; i, j = i+1, j-1 {
		g[i], g[j] = g[j], g[i]
	}
}
