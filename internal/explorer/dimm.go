package explorer

// Carried over from the Memory Explorer of aterm by its author. See NOTICE.

import "strings"

// Bounds of what the readers of slots return. A reader stops one entry
// past its bound, so that a caller sees that the bound was reached.
const (
	MaxSlots  = 4096 // slots of one text
	MaxBlocks = 4096 // cache or bank blocks of one lshw text
	MaxNodes  = 1024 // NUMA nodes
)

// Dimm is one memory slot as the reference describes it (DimmSlotInfo),
// with the names of the tests that gave its socket and its channel.
type Dimm struct {
	Locator         string `json:"locator"`
	BankLocator     string `json:"bankLocator"`
	Size            string `json:"size"`
	SizeBytes       Num    `json:"sizeBytes"`
	Type            string `json:"type"`
	Speed           string `json:"speed"`
	ConfiguredSpeed string `json:"configuredSpeed"`
	Manufacturer    string `json:"manufacturer"`
	SerialNumber    string `json:"serialNumber"`
	PartNumber      string `json:"partNumber"`
	Rank            string `json:"rank"`
	Channel         Num    `json:"channel"`
	Socket          Num    `json:"socket"`
	Populated       bool   `json:"populated"`
	CeCount         Num    `json:"ceCount"`
	UeCount         Num    `json:"ueCount"`
	ErrorCount      Num    `json:"errorCount"`

	// SocketBy and ChannelBy name the test that set Socket and Channel;
	// empty when none did (then the number is 0). The reference has no
	// such fields: the comparison with it leaves them out.
	SocketBy  string `json:"socketBy,omitempty"`
	ChannelBy string `json:"channelBy,omitempty"`
}

// Names of the tests of the readers of slots.
const (
	ByPNodeChannelDimm = "p-node-channel-dimm" // bank locator P0_Node0_Channel0_Dimm0
	ByNodeChannelDimm  = "node-channel-dimm"   // bank locator _Node0_Channel0_Dimm0
	ByDimmDigits       = "dimm-digits"         // DIMM000: socket, channel, slot
	ByLetterNumber     = "letter-number"       // A1: socket from the letter
	ByProc             = "proc"                // PROC 1 (from 1)
	ByCPU              = "cpu"                 // CPU0 (from 0)
	ByPOrNode          = "p-or-node"           // P1 or Node 1 (from 1)
	ByP                = "p"                   // lshw: P1 at the start (from 1)
	ByNode             = "node"                // lshw: Node 0 (from 0); channel: Node 0
	ByCD               = "c-d"                 // _C0D1
	ByCHDimm           = "ch-dimm"             // _CH0_DIMM1
	ByBankChannel      = "bank-channel"        // bank locator Channel 0
	ByBankNode         = "bank-node"           // bank locator Node 0
	ByDimmLetter       = "dimm-letter"         // DIMMA1
	ByChannelLetter    = "channel-letter"      // ChannelA or _A
	ByBankChannelChar  = "bank-channel-char"   // bank locator CHANNEL A or CH 0
	ByLetterNumberPass = "letter-number-pass"  // channel from the number of A1, second pass
	ByProcDimmPass     = "proc-dimm-pass"      // channel from DIMM n of PROC m DIMM n, second pass
)

// Patterns of parseDmidecodeMemory and parseLshwDimms.
var (
	reBankPNode       = jsre(`^P(\d+)_Node(\d+)_Channel(\d+)_Dimm(\d+)`, "i")
	reBankNodeChannel = jsre(`_Node(\d+)_Channel(\d+)_Dimm(\d+)`, "i")
	reDimmNumeric     = jsre(`^DIMM(\d)(\d)(\d)`, "")
	reDell            = jsre(`^([A-Z])(\d+)$`, "i")
	reCPU             = jsre(`CPU(\d+)`, "i")
	rePNode           = jsre(`(?:^P|Node\s*)(\d+)`, "i")
	reProc            = jsre(`PROC\s*(\d+)`, "i")
	reCxDx            = jsre(`[_]C(\d+)D(\d+)`, "i")
	reCHxDIMMx        = jsre(`[_]CH(\d+)[_]DIMM(\d+)`, "i")
	reBankChannelNum  = jsre(`Channel\s*(\d+)`, "i")
	reBankNodeNum     = jsre(`Node\s*(\d+)`, "i")
	reDimmLetter      = jsre(`DIMM([A-Z])(\d*)`, "i")
	reChannelLetter   = jsre(`(?:Channel\s*|_)([A-Z])(\d*)`, "i")
	reBankChannelChar = jsre(`(?:Channel\s*|CH\s*)([A-Z0-9])`, "i")
	reDmiSize         = jsre(`(\d+)\s*(MB|GB|TB)`, "i")
	reDellLocator     = jsre(`^[A-Z]\d+$`, "i")
	reHPLocator       = jsre(`PROC\s*\d+\s*DIMM\s*\d+`, "i")
	reDimmNumber      = jsre(`DIMM\s*(\d+)`, "i")

	reLshwDimmSlot      = jsre(`DIMM(\d)(\d)(\d)`, "")
	reLshwP             = jsre(`^P(\d+)`, "i")
	reLshwCD            = jsre(`_C(\d+)D(\d+)`, "i")
	reLshwCHDimm        = jsre(`_CH(\d+)_DIMM(\d+)`, "i")
	reLshwDimmLetter    = jsre(`DIMM([A-Z])`, "i")
	reLshwChannelLetter = jsre(`Channel\s*([A-Z])`, "i")
	reLshwSize          = jsre(`(\d+)\s*(MiB|GiB|TiB|MB|GB|TB)`, "i")
	reMHz               = jsre(`(\d+)\s*MHz`, "")
	reDDR               = jsre(`(DDR\d)`, "i")
	reEdacLine          = jsre(`^(dimm\d+):(\d+)`, "")

	reMemoryStart = jsre(`\*-memory(?::\d+)?`, "")
	reMemoryEnd   = jsre(`\*-memory(?::\d+)?\s+UNCLAIMED`, "")
	reBankStart   = jsre(`\*-bank:\d+`, "")
	reBankEnd     = jsre(`\*-bank:|\*-memory:`, "")
)

// fieldGetters reads "Key: value" as the get() of the reference: the first
// line that starts with the key, white space before it allowed, and the
// value after any white space, the end of the line included.
type fieldGetters map[string]*jsRegexp

func newFields(keys ...string) fieldGetters {
	g := fieldGetters{}
	for _, k := range keys {
		g[k] = jsre(`^\s*`+k+`:\s*(.+)`, "m")
	}
	return g
}

func (g fieldGetters) get(block, key string) string {
	m, ok := g[key].Match(block)
	if !ok {
		return ""
	}
	return Trim(m.Group(1))
}

var dmiFields = newFields("Size", "Locator", "Bank Locator", "Type", "Speed", "Configured Memory Speed",
	"Configured Clock Speed", "Manufacturer", "Serial Number", "Part Number", "Rank")

var lshwFields = newFields("slot", "size", "description", "vendor", "serial", "product")

// orUnknown is value || 'Unknown'.
func orUnknown(s string) string {
	if s == "" {
		return "Unknown"
	}
	return s
}

// Counts are the EDAC counts of the reference: "dimm<N>" to a count.
type Counts map[string]Num

// ParseEdacCounts reads the lines "dimm<N>:<count>" the reference makes of
// the EDAC tree; a later line of the same name replaces the earlier one.
// The EDAC lines of collectMemoryMap, window.ts 849-862.
func ParseEdacCounts(raw string) Counts {
	c := Counts{}
	for _, line := range Lines(raw) {
		if m, ok := reEdacLine.Match(line); ok {
			c[m.Group(1)] = ParseInt(m.Group(2))
		}
	}
	return c
}

func (c Counts) get(key string) Num { return Or(c[key], 0) }

// ParseDmidecodeMemory reads the Memory Device blocks of dmidecode text.
// parseDmidecodeMemory, window.ts 906-1097.
func ParseDmidecodeMemory(raw string, ce, ue Counts) []Dimm {
	if Trim(raw) == "" {
		return nil
	}
	var dimms []Dimm
	dimmIndex := 0
	for _, section := range strings.Split(raw, "Memory Device\n") {
		if !strings.Contains(section, "Locator:") {
			continue
		}
		if len(dimms) > MaxSlots {
			break
		}
		get := func(k string) string { return dmiFields.get(section, k) }
		sizeStr := get("Size")
		populated := sizeStr != "" && sizeStr != "No Module Installed" && sizeStr != "Not Installed"
		var sizeBytes Num
		if populated {
			if m, ok := reDmiSize.Match(sizeStr); ok {
				val := float64(ParseInt(m.Group(1)))
				switch upperASCII(m.Group(2)) {
				case "MB":
					sizeBytes = Num(val * 1024 * 1024)
				case "GB":
					sizeBytes = Num(val * 1024 * 1024 * 1024)
				case "TB":
					sizeBytes = Num(val * 1024 * 1024 * 1024 * 1024)
				}
			}
		}
		locator := get("Locator")
		bank := get("Bank Locator")

		var socket, channel Num
		var socketBy, channelBy string
		pNode, isPNode := reBankPNode.Match(bank)
		var nodeCh jsMatch
		isNodeCh := false
		if !isPNode {
			nodeCh, isNodeCh = reBankNodeChannel.Match(bank)
		}
		dimmNum, isDimmNum := reDimmNumeric.Match(locator)
		switch {
		case isPNode:
			socket = ParseInt(pNode.Group(1))
			node := float64(ParseInt(pNode.Group(2)))
			ch := float64(ParseInt(pNode.Group(3)))
			channel = Num(node*4 + ch)
			socketBy, channelBy = ByPNodeChannelDimm, ByPNodeChannelDimm
		case isNodeCh:
			socket = ParseInt(nodeCh.Group(1))
			channel = ParseInt(nodeCh.Group(2))
			socketBy, channelBy = ByNodeChannelDimm, ByNodeChannelDimm
		case isDimmNum:
			socket = ParseInt(dimmNum.Group(1))
			channel = ParseInt(dimmNum.Group(2))
			socketBy, channelBy = ByDimmDigits, ByDimmDigits
		default:
			dell, isDell := reDell.Match(locator)
			cpu, isCPU := reCPU.Match(locator)
			if !isCPU {
				cpu, isCPU = reCPU.Match(bank)
			}
			pn, isPN := rePNode.Match(locator)
			if !isPN {
				pn, isPN = rePNode.Match(bank)
			}
			proc, isProc := reProc.Match(locator)
			switch {
			case isDell:
				socket = Num(upperASCII(dell.Group(1))[0]) - 65
				socketBy = ByLetterNumber
			case isProc:
				socket = ParseInt(proc.Group(1)) - 1
				socketBy = ByProc
			case isCPU:
				socket = ParseInt(cpu.Group(1))
				socketBy = ByCPU
			case isPN:
				socket = ParseInt(pn.Group(1)) - 1
				socketBy = ByPOrNode
			}
			if m, ok := reCxDx.Match(locator); ok {
				channel, channelBy = ParseInt(m.Group(1)), ByCD
			} else if m, ok := reCHxDIMMx.Match(locator); ok {
				channel, channelBy = ParseInt(m.Group(1)), ByCHDimm
			} else if m, ok := reBankChannelNum.Match(bank); ok {
				channel, channelBy = ParseInt(m.Group(1)), ByBankChannel
			} else if m, ok := reBankNodeNum.Match(bank); ok {
				channel, channelBy = ParseInt(m.Group(1)), ByBankNode
			} else {
				m, ok := reDimmLetter.Match(locator)
				by := ByDimmLetter
				if !ok {
					m, ok = reChannelLetter.Match(locator)
					by = ByChannelLetter
				}
				if !ok {
					m, ok = reBankChannelChar.Match(bank)
					by = ByBankChannelChar
				}
				if ok {
					ch := upperASCII(m.Group(1))
					if ch >= "0" && ch <= "9" {
						channel = ParseInt(ch)
					} else {
						channel = Num(ch[0]) - 65
					}
					channelBy = by
				}
			}
		}

		ceCount := ce.get("dimm" + itoa(dimmIndex))
		ueCount := ue.get("dimm" + itoa(dimmIndex))
		cfg := get("Configured Memory Speed")
		if cfg == "" {
			cfg = get("Configured Clock Speed")
		}
		dimms = append(dimms, Dimm{
			Locator:         locator,
			BankLocator:     bank,
			Size:            sizeStr,
			SizeBytes:       sizeBytes,
			Type:            orUnknown(get("Type")),
			Speed:           orUnknown(get("Speed")),
			ConfiguredSpeed: orUnknown(cfg),
			Manufacturer:    orUnknown(get("Manufacturer")),
			SerialNumber:    orUnknown(get("Serial Number")),
			PartNumber:      orUnknown(get("Part Number")),
			Rank:            orUnknown(get("Rank")),
			Channel:         channel,
			Socket:          socket,
			Populated:       populated,
			CeCount:         ceCount,
			UeCount:         ueCount,
			ErrorCount:      ceCount + ueCount,
			SocketBy:        socketBy,
			ChannelBy:       channelBy,
		})
		dimmIndex++
	}
	secondPasses(dimms)
	return dimms
}

// secondPasses gives the slots named like A1 and like PROC 1 DIMM 9 their
// channels: the number of the slot, counted from 1, modulo half the slots
// of its socket. The two second passes of parseDmidecodeMemory and
// parseLshwDimms, window.ts 1059-1094 and 1442-1478.
func secondPasses(dimms []Dimm) {
	pass := func(match func(d *Dimm) bool, number func(d *Dimm) (Num, bool), by string) {
		groups := map[float64][]*Dimm{}
		var order []float64
		for i := range dimms {
			d := &dimms[i]
			if !match(d) {
				continue
			}
			key := float64(d.Socket) + 0 // -0 and 0 are one key, as in a Map
			if _, ok := groups[key]; !ok {
				order = append(order, key)
			}
			groups[key] = append(groups[key], d)
		}
		for _, key := range order {
			group := groups[key]
			numChannels := maxf(1, ceil(float64(len(group))/2))
			for _, d := range group {
				if slot, ok := number(d); ok {
					d.Channel = Num(Mod(float64(slot)-1, numChannels))
					d.ChannelBy = by
				}
			}
		}
	}
	pass(func(d *Dimm) bool { return reDellLocator.Test(d.Locator) },
		func(d *Dimm) (Num, bool) { return ParseInt(d.Locator[1:]), true }, ByLetterNumberPass)
	pass(func(d *Dimm) bool { return reHPLocator.Test(d.Locator) },
		func(d *Dimm) (Num, bool) {
			m, ok := reDimmNumber.Match(d.Locator)
			if !ok {
				return 0, false
			}
			return ParseInt(m.Group(1)), true
		}, ByProcDimmPass)
}

// lshwMemorySection is the text from the first *-memory node to the next
// *-memory node that lshw marks UNCLAIMED, or to the end.
func lshwMemorySection(raw string) (string, bool) {
	m, ok := reMemoryStart.Match(raw)
	if !ok {
		return "", false
	}
	end := reMemoryEnd.Index(raw, m.End())
	if end < 0 {
		end = len(raw)
	}
	return raw[m.Start():end], true
}

// blocks cuts text into the blocks that start where start matches and end
// before the next place where end matches, or at the end of the text: the
// global search with a look-ahead of the reference. At most max+1 blocks.
func blocks(text string, start, end *jsRegexp, max int) []string {
	var out []string
	pos := 0
	for pos <= len(text) && len(out) <= max {
		loc := start.re.FindStringIndex(text[pos:])
		if loc == nil {
			break
		}
		from := pos + loc[0]
		to := end.Index(text, pos+loc[1])
		if to < 0 {
			to = len(text)
		}
		out = append(out, text[from:to])
		pos = to
	}
	return out
}

// ParseLshwDimms reads the banks of the memory node of lshw text.
// parseLshwDimms, window.ts 1313-1481.
func ParseLshwDimms(raw string, ce, ue Counts) []Dimm {
	if Trim(raw) == "" {
		return nil
	}
	section, ok := lshwMemorySection(raw)
	if !ok {
		return nil
	}
	var dimms []Dimm
	for dimmIndex, block := range blocks(section, reBankStart, reBankEnd, MaxSlots) {
		get := func(k string) string { return lshwFields.get(block, k) }
		slotStr := get("slot")
		sizeStr := get("size")
		desc := get("description")
		populated := sizeStr != "" && !strings.Contains(desc, "[empty]")
		var sizeBytes Num
		if populated {
			if m, ok := reLshwSize.Match(sizeStr); ok {
				val := float64(ParseInt(m.Group(1)))
				switch upperASCII(m.Group(2)) {
				case "MIB", "MB":
					sizeBytes = Num(val * 1024 * 1024)
				case "GIB", "GB":
					sizeBytes = Num(val * 1024 * 1024 * 1024)
				case "TIB", "TB":
					sizeBytes = Num(val * 1024 * 1024 * 1024 * 1024)
				}
			}
		}
		var socket, channel Num
		var socketBy, channelBy string
		if m, ok := reLshwDimmSlot.Match(slotStr); ok {
			socket, channel = ParseInt(m.Group(1)), ParseInt(m.Group(2))
			socketBy, channelBy = ByDimmDigits, ByDimmDigits
		} else {
			dell, isDell := reDell.Match(slotStr)
			hp, isHP := reProc.Match(slotStr)
			cpu, isCPU := reCPU.Match(slotStr)
			p, isP := reLshwP.Match(slotStr)
			node, isNode := reBankNodeNum.Match(slotStr)
			switch {
			case isDell:
				socket, socketBy = Num(upperASCII(dell.Group(1))[0])-65, ByLetterNumber
			case isHP:
				socket, socketBy = ParseInt(hp.Group(1))-1, ByProc
			case isCPU:
				socket, socketBy = ParseInt(cpu.Group(1)), ByCPU
			case isP:
				socket, socketBy = ParseInt(p.Group(1))-1, ByP
			case isNode:
				socket, socketBy = ParseInt(node.Group(1)), ByNode
			}
			if !isDell {
				if m, ok := reLshwCD.Match(slotStr); ok {
					channel, channelBy = ParseInt(m.Group(1)), ByCD
				} else if m, ok := reLshwCHDimm.Match(slotStr); ok {
					channel, channelBy = ParseInt(m.Group(1)), ByCHDimm
				} else if isNode {
					channel, channelBy = ParseInt(node.Group(1)), ByNode
				} else if m, ok := reLshwDimmLetter.Match(slotStr); ok {
					channel, channelBy = Num(upperASCII(m.Group(1))[0])-65, ByDimmLetter
				} else if m, ok := reLshwChannelLetter.Match(slotStr); ok {
					channel, channelBy = Num(upperASCII(m.Group(1))[0])-65, ByChannelLetter
				}
			}
		}
		speed := "Unknown"
		if m, ok := reMHz.Match(desc); ok {
			speed = m.Group(1) + " MT/s"
		}
		typ := "Unknown"
		if m, ok := reDDR.Match(desc); ok {
			typ = m.Group(1)
		}
		ceCount := ce.get("dimm" + itoa(dimmIndex))
		ueCount := ue.get("dimm" + itoa(dimmIndex))
		locator := slotStr
		if locator == "" {
			locator = "Bank " + itoa(dimmIndex)
		}
		size := "No Module Installed"
		if populated {
			size = sizeStr
		}
		dimms = append(dimms, Dimm{
			Locator: locator, Size: size, SizeBytes: sizeBytes, Type: typ, Speed: speed, ConfiguredSpeed: speed,
			Manufacturer: orUnknown(get("vendor")), SerialNumber: orUnknown(get("serial")),
			PartNumber: orUnknown(get("product")), Rank: "Unknown", Channel: channel, Socket: socket,
			Populated: populated, CeCount: ceCount, UeCount: ueCount, ErrorCount: ceCount + ueCount,
			SocketBy: socketBy, ChannelBy: channelBy,
		})
	}
	secondPasses(dimms)
	return dimms
}
