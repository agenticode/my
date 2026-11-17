package view

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/explorer"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
)

// Board is the board of a host as the Memory Explorer draws it: per row
// one or two sockets, each CPU with the banks of its channels beside it,
// every slot with its finding. Slots the board cannot put on a CPU are set
// aside by their names, as the slot map draws them. The placement is made
// from the slot rows of the ledger when the view is built and is stored
// nowhere; the ledger decides what a slot holds.
type Board struct {
	Host string `json:"host"`
	// System is the maker and product of the host.
	System    string `json:"system,omitzero"`
	Slots     int    `json:"slots"`
	Populated int    `json:"populated"`
	// Bytes is the size of all modules, Size the same as text.
	Bytes int64  `json:"bytes,omitzero"`
	Size  string `json:"size,omitzero"`
	CE    int64  `json:"ce,omitzero"`
	UE    int64  `json:"ue,omitzero"`
	// Badge is the memory type and speed every module shares, if they do.
	Badge string `json:"badge,omitzero"`
	// Sockets is the number of sockets of the board (its processor
	// records), when the ledger knows it.
	Sockets int        `json:"sockets,omitzero"`
	Rows    []BoardRow `json:"rows"`
	// Unplaced are the slots the board sets aside, with the reason.
	Unplaced []BoardStick `json:"unplaced"`
	// Aside draws the set-aside slots and the slots the ledger knows only
	// by a name a source used, as the slot map does.
	Aside   *SlotMap      `json:"aside,omitzero"`
	Counts  BoardCounts   `json:"counts"`
	Reasons []BoardReason `json:"reasons"`
	// Caches names where the cache sizes were read: "lshw", "sysfs", or
	// empty when nothing gave them.
	Caches string `json:"caches,omitzero"`
	// Lines are the lines of data quality of the board (design 4.4).
	Lines []Text `json:"lines"`
}

// BoardRow is one row of the board: a bank, a CPU and a bank, or for two
// sockets a bank, two CPUs and a bank.
type BoardRow struct {
	Parts []BoardPart `json:"parts"`
}

// BoardPart is a bank of channels or a CPU.
type BoardPart struct {
	// Bank is "left" or "right" for a bank, empty for a CPU.
	Bank   string `json:"bank,omitzero"`
	Socket int    `json:"socket"`
	// NoChannels is set when the channels of the socket are not drawn:
	// the slots stand in the order of the inventory.
	NoChannels bool           `json:"no_channels,omitzero"`
	Channels   []BoardChannel `json:"channels,omitzero"`
	CPU        *BoardCPU      `json:"cpu,omitzero"`
}

// BoardChannel is one channel of a bank, its sticks in drawing order.
type BoardChannel struct {
	Channel model.Opt[int] `json:"channel,omitzero"`
	Sticks  []BoardStick   `json:"sticks"`
}

// BoardStick is one slot.
type BoardStick struct {
	Slot string `json:"slot"`
	// State is the grade of the finding of the DIMM in the slot, or
	// "empty", or "unknown" (as in the slot map).
	State   string `json:"state"`
	DIMMUID string `json:"dimm_uid,omitzero"`
	CE      int64  `json:"ce,omitzero"`
	UE      int64  `json:"ue,omitzero"`
	// Size is the size of the module as MemorY writes it everywhere.
	Size   string       `json:"size,omitzero"`
	Bytes  int64        `json:"bytes,omitzero"`
	Module *BoardModule `json:"module,omitzero"`
	// Locator and BankLocator are what the inventory named.
	Locator     string `json:"locator,omitzero"`
	BankLocator string `json:"bank_locator,omitzero"`
	// Place is where the board put it, with the tests that gave socket
	// and channel.
	Place     explorer.Placement `json:"place"`
	SocketBy  string             `json:"socket_by,omitzero"`
	ChannelBy string             `json:"channel_by,omitzero"`
}

// BoardModule is what the ledger knows of the module in a slot.
type BoardModule struct {
	Maker      string         `json:"maker,omitzero"`
	PartNumber string         `json:"part_number,omitzero"`
	Serial     string         `json:"serial,omitzero"`
	Type       string         `json:"type,omitzero"`
	SpeedMTs   model.Opt[int] `json:"speed_mts,omitzero"`
	Configured model.Opt[int] `json:"configured_mts,omitzero"`
	Ranks      model.Opt[int] `json:"ranks,omitzero"`
}

// BoardCPU is one CPU: the count and size of its modules and its caches.
type BoardCPU struct {
	Socket int          `json:"socket"`
	DIMMs  int          `json:"dimms"`
	Bytes  int64        `json:"bytes,omitzero"`
	Size   string       `json:"size,omitzero"`
	Caches []BoardCache `json:"caches"`
}

// BoardCache is one cache level of a CPU; Size is written by the bytes of
// the explorer (KiB, MiB).
type BoardCache struct {
	Level string `json:"level"`
	Bytes int64  `json:"bytes"`
	Size  string `json:"size"`
}

// BoardCounts counts the slots by how the board drew them.
type BoardCounts struct {
	Placed    int `json:"placed"`
	NoChannel int `json:"no_channel"`
	Unplaced  int `json:"unplaced"`
}

// BoardReason is one reason why slots were drawn without channels or set
// aside, with how many and one example of what the inventory named.
type BoardReason struct {
	State       string `json:"state"`
	Reason      string `json:"reason"`
	Count       int    `json:"count"`
	Locator     string `json:"locator"`
	BankLocator string `json:"bank_locator"`
	Text        Text   `json:"text"`
}

// Reasons of the board itself, beside those of explorer.Place.
const (
	whyFault = "fault" // a reader failed; every slot is set aside
	whyCount = "count" // the text was not read as written
	whyLost  = "lost"  // the arrangement did not hold the slot
)

// boardHost is what the board needs of the host beyond its slots.
type boardHost struct {
	vendor, processor string
	sockets           int
	lshw, cpuCache    string
}

// Readers of the board, replaced by tests that make them fail.
var (
	boardParse  = explorer.ParseDmidecodeMemory
	boardLayout = explorer.Board
)

// Board builds the board of a host.
func (c *Case) Board(ctx context.Context, h *engine.HostResult) (*Board, error) {
	slots, err := c.store.Slots(ctx, h.Host)
	if err != nil {
		return nil, err
	}
	hosts, err := c.store.Hosts(ctx)
	if err != nil {
		return nil, err
	}
	var bh boardHost
	system := ""
	for _, x := range hosts {
		if x.Name == h.Host {
			bh.vendor, bh.processor, bh.sockets = x.SystemVendor, x.CPU, x.Sockets
			system = strings.TrimSpace(x.SystemVendor + " " + x.SystemProduct)
		}
	}
	readings, err := c.store.Readings(ctx, h.Host)
	if err != nil {
		return nil, err
	}
	for _, r := range readings {
		switch r.Kind {
		case model.ReadingLshw:
			bh.lshw = r.Text
		case model.ReadingCPUCache:
			bh.cpuCache = r.Text
		}
	}
	b := boardOf(c.Lang, h, slots, bh)
	b.System = system
	return b, nil
}

// described tells a slot an inventory described: it has a locator or an
// SMBIOS handle.
func described(sl model.Slot) bool { return sl.Locator != "" || sl.SMBIOSHandle.Valid }

// boardOf builds the board from the slot rows of the ledger.
func boardOf(lang i18n.Lang, h *engine.HostResult, slots []model.Slot, bh boardHost) *Board {
	b := &Board{Host: h.Host, Sockets: bh.sockets, Rows: []BoardRow{}, Unplaced: []BoardStick{}, Reasons: []BoardReason{}, Lines: []Text{}}
	var desc, rest []model.Slot
	for _, sl := range slots {
		if described(sl) {
			desc = append(desc, sl)
		} else {
			rest = append(rest, sl)
		}
	}
	sort.SliceStable(desc, func(i, j int) bool {
		a, c := desc[i].SMBIOSHandle, desc[j].SMBIOSHandle
		if a.Valid != c.Valid {
			return a.Valid
		}
		if a.Valid && a.V != c.V {
			return a.V < c.V
		}
		return naturalLess(desc[i].Name, desc[j].Name)
	})

	// The sticks, one per slot, in the order of the text.
	sticks := make([]BoardStick, 0, len(desc))
	for _, sl := range desc {
		sticks = append(sticks, stickOf(h, sl))
	}
	host := explorer.SlotHost{Vendor: bh.vendor, Processor: bh.processor}
	text := make([]explorer.SlotText, len(desc))
	for i, sl := range desc {
		text[i] = explorer.SlotText{Locator: sl.Locator, BankLocator: sl.BankLocator, Populated: sticks[i].State != SlotEmpty && sticks[i].State != SlotUnknown}
		if d := h.InSlot(sl.Name); d != nil && d.DIMM != nil {
			text[i].SizeMB = d.DIMM.SizeBytes >> 20
		}
	}
	var dimms []explorer.Dimm
	fault := ""
	if len(desc) > 0 {
		out := adapter.Guard(func() *[]explorer.Dimm {
			d := boardParse(explorer.WriteSlotsOf(host, text), nil, nil)
			return &d
		})
		switch {
		case out == nil:
			fault = whyFault
		case len(*out) != len(desc):
			fault = whyCount
		default:
			dimms = *out
		}
	} else if bh.lshw != "" {
		dimms, sticks = lshwSlots(h, bh.lshw, rest)
		if len(sticks) > 0 {
			rest = untied(rest, sticks)
		}
	}
	caches, source := boardCaches(bh)
	b.Caches = source

	places := make([]explorer.Placement, len(sticks))
	if fault != "" {
		for i := range places {
			places[i] = explorer.Placement{State: explorer.Unplaced, Reason: fault}
		}
	} else if len(dimms) > 0 {
		p := adapter.Guard(func() []explorer.Placement { return explorer.Place(dimms, bh.sockets, len(caches)) })
		if len(p) != len(dimms) {
			for i := range places {
				places[i] = explorer.Placement{State: explorer.Unplaced, Reason: whyFault}
			}
		} else {
			copy(places, p)
		}
		for i := range sticks {
			sticks[i].SocketBy, sticks[i].ChannelBy = dimms[i].SocketBy, dimms[i].ChannelBy
		}
	}
	for i := range sticks {
		sticks[i].Place = places[i]
	}
	held := arrange(b, sticks, dimms, places, caches)
	for i := range sticks {
		if places[i].State != explorer.Unplaced && !held[i] {
			sticks[i].Place = explorer.Placement{State: explorer.Unplaced, Reason: whyLost}
		}
	}

	// Counts, reasons and the slots set aside.
	byReason := map[string]*BoardReason{}
	var reasonOrder []string
	var aside []model.Slot
	for i, st := range sticks {
		switch st.Place.State {
		case explorer.Placed:
			b.Counts.Placed++
		case explorer.NoChannel:
			b.Counts.NoChannel++
		default:
			b.Counts.Unplaced++
			b.Unplaced = append(b.Unplaced, st)
			if i < len(desc) {
				aside = append(aside, desc[i])
			}
		}
		if st.Place.State == explorer.Placed {
			continue
		}
		k := st.Place.State + "/" + st.Place.Reason
		r := byReason[k]
		if r == nil {
			r = &BoardReason{State: st.Place.State, Reason: st.Place.Reason, Locator: st.Locator, BankLocator: st.BankLocator}
			byReason[k] = r
			reasonOrder = append(reasonOrder, k)
		}
		r.Count++
	}
	for _, k := range reasonOrder {
		r := byReason[k]
		code := "board.why." + r.Reason
		if r.State == explorer.NoChannel {
			code = "board.nochannel." + r.Reason
		}
		r.Text = boardSay(lang, code, "n", r.Count, "locator", r.Locator, "bank", r.BankLocator)
		b.Reasons = append(b.Reasons, *r)
	}
	aside = append(aside, rest...)
	if len(aside) > 0 {
		sm := slotMap(h, aside)
		b.Aside = &sm
	}

	// The summary, from the ledger.
	b.Slots = len(slots)
	types := map[string]bool{}
	for _, sl := range slots {
		st := stickOf(h, sl)
		if st.State != SlotEmpty && st.State != SlotUnknown {
			b.Populated++
			b.Bytes += st.Bytes
			if st.Module != nil {
				types[strings.TrimSpace(st.Module.Type+" "+speedText(st.Module))] = true
			}
		}
		b.CE += st.CE
		b.UE += st.UE
	}
	if b.Bytes > 0 {
		b.Size = sizeText(b.Bytes)
	}
	if len(types) == 1 {
		for t := range types {
			b.Badge = t
		}
	}
	b.Lines = boardLines(lang, b)
	return b
}

func speedText(m *BoardModule) string {
	s := m.Configured
	if !s.Valid {
		s = m.SpeedMTs
	}
	if !s.Valid {
		return ""
	}
	return fmt.Sprintf("%d MT/s", s.V)
}

// stickOf fills a stick from the ledger: the finding of the DIMM in the
// slot, its counts and its module.
func stickOf(h *engine.HostResult, sl model.Slot) BoardStick {
	st := BoardStick{Slot: sl.Name, Locator: sl.Locator, BankLocator: sl.BankLocator, State: SlotUnknown}
	switch d := h.InSlot(sl.Name); {
	case d != nil && !(sl.Populated.Valid && !sl.Populated.V):
		st.State = string(d.Verdict.Grade)
		st.CE, st.UE = d.Features.CE, d.Features.UE
		st.DIMMUID = d.UID
		if m := d.DIMM; m != nil {
			st.Bytes = m.SizeBytes
			if m.SizeBytes > 0 {
				st.Size = sizeText(m.SizeBytes)
			}
			st.Module = &BoardModule{Maker: m.Manufacturer, PartNumber: m.PartNumber, Serial: m.Serial,
				Type: strings.TrimSpace(m.MemoryType + " " + m.ModuleType), SpeedMTs: m.SpeedMTs, Configured: m.ConfiguredSpeedMTs, Ranks: m.Ranks}
		}
	case sl.Populated.Valid && sl.Populated.V:
		st.State = string(model.VerdictOK)
	case sl.Populated.Valid:
		st.State = SlotEmpty
	}
	return st
}

// lshwSlots reads the banks of lshw text when no inventory described a
// slot, and ties each to a ledger slot of the same name.
func lshwSlots(h *engine.HostResult, lshw string, slots []model.Slot) ([]explorer.Dimm, []BoardStick) {
	out := adapter.Guard(func() *[]explorer.Dimm {
		d := explorer.ParseLshwDimms(lshw, nil, nil)
		return &d
	})
	if out == nil || len(*out) > explorer.MaxSlots {
		return nil, nil
	}
	byName := map[string]model.Slot{}
	for _, sl := range slots {
		byName[model.NormalizeLabel(sl.Name)] = sl
	}
	var sticks []BoardStick
	for _, d := range *out {
		sl, ok := byName[model.NormalizeLabel(d.Locator)]
		if !ok {
			sl = model.Slot{Name: d.Locator, Locator: d.Locator, Populated: model.Some(d.Populated)}
		}
		st := stickOf(h, sl)
		st.Locator = d.Locator
		sticks = append(sticks, st)
	}
	return *out, sticks
}

// untied are the slots no lshw bank was tied to.
func untied(slots []model.Slot, sticks []BoardStick) []model.Slot {
	tied := map[string]bool{}
	for _, st := range sticks {
		tied[st.Slot] = true
	}
	var out []model.Slot
	for _, sl := range slots {
		if !tied[sl.Name] {
			out = append(out, sl)
		}
	}
	return out
}

// boardCaches reads the cache sizes: lshw, then sysfs when sysfs shows more
// packages.
func boardCaches(bh boardHost) ([]explorer.CPUCache, string) {
	var caches []explorer.CPUCache
	source := ""
	if bh.lshw != "" {
		caches = adapter.Guard(func() []explorer.CPUCache { return explorer.ParseLshwCacheHierarchy(bh.lshw) })
		if len(caches) > 0 {
			source = "lshw"
		}
	}
	if bh.cpuCache != "" {
		if sys := adapter.Guard(func() []explorer.CPUCache { return explorer.ParseSysfsCacheInfo(bh.cpuCache) }); len(sys) > len(caches) {
			caches, source = sys, "sysfs"
		}
	}
	return caches, source
}

// arrange draws the sockets that hold placed slots, two sockets to a row
// in the order of their numbers, as the explorer arranges them. A socket
// drawn without channels gets one channel per slot in the order of the
// inventory. It returns which sticks the board holds.
func arrange(b *Board, sticks []BoardStick, dimms []explorer.Dimm, places []explorer.Placement, caches []explorer.CPUCache) map[int]bool {
	held := map[int]bool{}
	bySocket := map[int][]int{}
	noch := map[int]bool{}
	for i, p := range places {
		if p.State == explorer.Unplaced || i >= len(dimms) {
			continue
		}
		bySocket[p.Socket] = append(bySocket[p.Socket], i)
		if p.State == explorer.NoChannel {
			noch[p.Socket] = true
		}
	}
	var socks []int
	for s := range bySocket {
		socks = append(socks, s)
	}
	sort.Ints(socks)
	cacheOf := func(s int) []BoardCache {
		out := []BoardCache{}
		for _, c := range caches {
			if n, ok := c.Socket.Int(); ok && n == s {
				for _, l := range []struct {
					name string
					v    explorer.Num
				}{{"L1", c.L1Bytes}, {"L2", c.L2Bytes}, {"L3", c.L3Bytes}} {
					if l.v > 0 {
						out = append(out, BoardCache{Level: l.name, Bytes: int64(l.v), Size: explorer.FormatBytes(float64(l.v))})
					}
				}
				break
			}
		}
		return out
	}
	for k := 0; k < len(socks); k += 2 {
		pair := socks[k:min(k+2, len(socks))]
		var in []explorer.Dimm
		var idx []int
		for _, s := range pair {
			for j, i := range bySocket[s] {
				d := dimms[i]
				d.Socket, d.Channel = explorer.Num(s), explorer.Num(places[i].Channel)
				if noch[s] {
					d.Channel = explorer.Num(j)
				}
				in = append(in, d)
				idx = append(idx, i)
			}
		}
		l := adapter.Guard(func() *explorer.Layout {
			x := boardLayout(in, nil)
			return &x
		})
		if l == nil {
			continue
		}
		for _, r := range l.Rows {
			row := BoardRow{Parts: []BoardPart{}}
			for _, p := range r.Parts {
				if p.CPU != nil {
					s, _ := p.CPU.Socket.Int()
					cpu := &BoardCPU{Socket: s, Caches: cacheOf(s)}
					for _, i := range bySocket[s] {
						if st := sticks[i]; st.State != SlotEmpty && st.State != SlotUnknown {
							cpu.DIMMs++
							cpu.Bytes += st.Bytes
						}
					}
					if cpu.Bytes > 0 {
						cpu.Size = sizeText(cpu.Bytes)
					}
					row.Parts = append(row.Parts, BoardPart{Socket: s, CPU: cpu})
					continue
				}
				s := 0
				part := BoardPart{Bank: p.Bank, Channels: []BoardChannel{}}
				for _, ch := range p.Channels {
					s, _ = ch.Socket.Int()
					bc := BoardChannel{Sticks: []BoardStick{}}
					if n, ok := ch.Channel.Int(); ok && !noch[s] {
						bc.Channel = model.Some(n)
					}
					for _, st := range ch.Sticks {
						i := idx[st.Slot]
						held[i] = true
						bc.Sticks = append(bc.Sticks, sticks[i])
					}
					part.Channels = append(part.Channels, bc)
				}
				part.Socket, part.NoChannels = s, noch[s]
				if len(part.Channels) > 0 {
					part.Socket, _ = p.Channels[0].Socket.Int()
					part.NoChannels = noch[part.Socket]
				}
				row.Parts = append(row.Parts, part)
			}
			b.Rows = append(b.Rows, row)
		}
	}
	return held
}

// boardLines are the lines of data quality of the board: what was placed,
// drawn without channels and set aside, and where the caches came from.
func boardLines(lang i18n.Lang, b *Board) []Text {
	var out []Text
	if n := b.Counts.Placed + b.Counts.NoChannel + b.Counts.Unplaced; n > 0 {
		out = append(out, boardSay(lang, "board.counts", "placed", b.Counts.Placed, "nochannel", b.Counts.NoChannel, "unplaced", b.Counts.Unplaced))
	}
	for _, r := range b.Reasons {
		out = append(out, r.Text)
	}
	switch b.Caches {
	case "lshw":
		out = append(out, boardSay(lang, "board.caches.lshw"))
	case "sysfs":
		out = append(out, boardSay(lang, "board.caches.sysfs"))
	}
	return out
}

func boardSay(lang i18n.Lang, code string, kv ...any) Text {
	t := Text{Code: code}
	if len(kv) > 0 {
		t.Args = make(map[string]any, len(kv)/2)
		for i := 0; i+1 < len(kv); i += 2 {
			t.Args[kv[i].(string)] = kv[i+1]
		}
	}
	t.Text = BoardMessages.Text(lang, code, t.Args)
	return t
}
