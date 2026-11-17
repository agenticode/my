package explorer

import (
	"regexp"
	"strings"
)

// Bounds of what the readers of slots return. A reader stops one entry
// past its bound, so that a caller sees that the bound was reached.
const (
	MaxSlots  = 4096 // slots of one text
	MaxBlocks = 4096 // cache or bank blocks of one lshw text
	MaxNodes  = 1024 // NUMA nodes
)

// Dimm is one memory slot as the explorer describes it, with the names of
// the tests that gave its socket and its channel.
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
	// empty when none did (then the number is 0). The want files of the
	// tests hold no such fields.
	SocketBy  string `json:"socketBy,omitempty"`
	ChannelBy string `json:"channelBy,omitempty"`

	// What the pass of the second round decides beside socket and channel
	// (FixNameForms). The want files hold none of these.
	// Position is the place of the slot in its channel, from 0.
	Position Num `json:"position,omitempty"`
	// ChannelName is the vendor's name of the channel ("A", "2", "1.0").
	ChannelName string `json:"channelName,omitempty"`
	// PlaceBy says how the place was found: name, bank, node, mate,
	// letters, one-socket, hub, family:<id>; empty for a slot not placed.
	PlaceBy string `json:"placeBy,omitempty"`
	// Family is the board family whose row acted.
	Family string `json:"family,omitempty"`
	// Idx is the DIMM index a bank locator spells, "" when none does.
	Idx string `json:"idx,omitempty"`
	// NotSlot says why a memory device is no slot of the board: flash, or
	// no-slot-on-board for a record the manual has no slot for.
	NotSlot string `json:"notSlot,omitempty"`
	// Unnamed is set for an empty slot the firmware gives no name; Label is
	// the slot label of the manual it takes, when a family row lists them.
	Unnamed bool   `json:"unnamed,omitempty"`
	Label   string `json:"label,omitempty"`
	// Aside is why the pass placed no slot: no-name, unknown-form,
	// no-socket, no-channel, or the reason of a family row.
	Aside string `json:"aside,omitempty"`

	// What the second passes need of the first.
	rawSocket, rawNode, rawChannel Num
	dellNumber                     Num
	hasDell, notLetter             bool
	// passLocator is the name the pass of the second round reads as the
	// locator: the Locator field, or the slot string of lshw.
	passLocator string
}

// Names of the tests of the readers of slots.
const (
	ByPNodeChannelDimm = "p-node-channel-dimm"    // bank locator P0_Node0_Channel0_Dimm0
	ByNodeChannelDimm  = "node-channel-dimm"      // bank locator _Node0_Channel0_Dimm0
	ByDimmDigits       = "dimm-digits"            // DIMM000: socket, channel, slot
	ByLetterNumber     = "letter-number"          // A1: socket from the letter
	ByProc             = "proc"                   // PROC 1 (from 1)
	ByCPU              = "cpu"                    // CPU0 (from 0)
	ByPOrNode          = "p-or-node"              // P1 or Node 1 (from 1)
	ByP                = "p"                      // lshw: P1 at the start (from 1)
	ByNode             = "node"                   // lshw: Node 0 (from 0); channel: Node 0
	ByCD               = "c-d"                    // _C0D1
	ByCHDimm           = "ch-dimm"                // _CH0_DIMM1
	ByBankChannel      = "bank-channel"           // bank locator Channel 0
	ByBankNode         = "bank-node"              // bank locator Node 0
	ByDimmLetter       = "dimm-letter"            // DIMMA1
	ByChannelLetter    = "channel-letter"         // ChannelA or _A
	ByBankChannelChar  = "bank-channel-char"      // bank locator CHANNEL A or CH 0
	ByLetterNumberPass = "letter-number-pass"     // channel from the number of A1, second pass
	ByCPUSpaced        = "cpu-space"              // CPU 1 (from 1, or from 0 when a CPU 0 is there)
	ByPLetter          = "dimm-p-letter"          // DIMM_P0_A0: socket 0, channel A
	ByDellDimm         = "dimm-underscore-letter" // DIMM_A1 of a Dell system: socket A
	ByProcDimmPass     = "proc-dimm-pass"         // channel from DIMM n of PROC m DIMM n, second pass
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
	reDmiSizeBinary   = jsre(`(\d+)\s*(MiB|GiB|TiB|MB|GB|TB)`, "i")
	reDimmPLetter     = jsre(`^DIMM_P(\d+)_([A-Z])\d+$`, "i")
	reDellDimm        = jsre(`^DIMM_([A-Z])(\d+)$`, "i")
	reCPUSpace        = jsre(`CPU\s+(\d+)`, "i")
	reBankChanChar    = jsre(`(?:Channel\s*|CHAN\s+|CH\s*)([A-Z0-9])`, "i")
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

// fieldGetters reads "Key: value": the first
// line that starts with the key, white space before it allowed, and the
// value after any white space, the end of the line included.
type fieldGetters struct {
	carried, fixed map[string]*jsRegexp
}

// hspace is the white space of JavaScript without the line terminators.
const hspace = `[\t\v\f \x{00A0}\x{1680}\x{2000}-\x{200A}\x{202F}\x{205F}\x{3000}\x{FEFF}]`

func newFields(keys ...string) fieldGetters {
	g := fieldGetters{carried: map[string]*jsRegexp{}, fixed: map[string]*jsRegexp{}}
	for _, k := range keys {
		g.carried[k] = jsre(`^\s*`+k+`:\s*(.+)`, "m")
		// With FixEmptyField the value is the rest of the line, empty or not.
		g.fixed[k] = &jsRegexp{re: regexp.MustCompile(`(?m:^)` + hspace + `*` + regexp.QuoteMeta(k) + `:` + hspace + `*(` + dotClass + `*)`), multiline: true}
	}
	return g
}

func (g fieldGetters) get(block, key string) string {
	re := g.carried[key]
	if on(FixEmptyField) {
		re = g.fixed[key]
	}
	m, ok := re.Match(block)
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

// Counts are EDAC counts by the name "dimm<N>".
type Counts map[string]Num

// ParseEdacCounts reads the lines "dimm<N>:<count>" made of the EDAC
// tree; a later line of the same name replaces the earlier one.
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
func ParseDmidecodeMemory(raw string, ce, ue Counts) []Dimm {
	d, _ := ReadSlots(raw, ce, ue)
	return d
}

// ReadSlots reads the Memory Device blocks of dmidecode text and what the
// text says of the board beside them.
func ReadSlots(raw string, ce, ue Counts) ([]Dimm, Facts) {
	raw = lineEnds(raw)
	if Trim(raw) == "" {
		return nil, Facts{}
	}
	hints := hintsOf(raw)
	var dimms []Dimm
	dimmIndex := 0
	seen := map[string]bool{}
	sections := strings.Split(raw, "Memory Device\n")
	for si, section := range sections {
		if !strings.Contains(section, "Locator:") {
			continue
		}
		if len(dimms) > MaxSlots {
			break
		}
		if on(FixRepeatedTable) && si > 0 {
			if k, ok := deviceKey(sections[si-1], section); ok {
				if seen[k] {
					continue
				}
				seen[k] = true
			}
		}
		get := func(k string) string { return dmiFields.get(section, k) }
		sizeStr := get("Size")
		populated := sizeStr != "" && sizeStr != "No Module Installed" && sizeStr != "Not Installed"
		var sizeBytes Num
		if populated {
			re := reDmiSize
			if on(FixBinarySizes) {
				re = reDmiSizeBinary
			}
			if m, ok := re.Match(sizeStr); ok {
				sizeBytes = sizeOf(ParseInt(m.Group(1)), m.Group(2))
			}
		}
		locator := get("Locator")
		bank := get("Bank Locator")

		d := Dimm{Locator: locator, BankLocator: bank, passLocator: locator}
		placeDmidecode(&d, locator, bank, hints)

		ceCount := ce.get("dimm" + itoa(dimmIndex))
		ueCount := ue.get("dimm" + itoa(dimmIndex))
		cfg := get("Configured Memory Speed")
		if cfg == "" {
			cfg = get("Configured Clock Speed")
		}
		d.Size, d.SizeBytes, d.Populated = sizeStr, sizeBytes, populated
		d.Type, d.Speed, d.ConfiguredSpeed = orUnknown(get("Type")), orUnknown(get("Speed")), orUnknown(cfg)
		d.Manufacturer, d.SerialNumber = orUnknown(get("Manufacturer")), orUnknown(get("Serial Number"))
		d.PartNumber, d.Rank = orUnknown(get("Part Number")), orUnknown(get("Rank"))
		d.CeCount, d.UeCount, d.ErrorCount = ceCount, ueCount, ceCount+ueCount
		dimms = append(dimms, d)
		dimmIndex++
	}
	secondPasses(dimms, hints)
	f := Facts{Sockets: len(hints.cpus)}
	if on(FixNameForms) && len(dimms) <= MaxSlots {
		f = placeNames(dimms, hints)
	}
	return dimms, f
}

// deviceKey is the handle line and the fields of a memory device, the text
// a repeated record repeats; ok is false when no handle line stands before
// it.
func deviceKey(before, section string) (string, bool) {
	before = strings.TrimSuffix(before, "\n")
	line := before[strings.LastIndexByte(before, '\n')+1:]
	if !reHandleLine.MatchString(line) || !strings.Contains(line, "DMI type 17,") {
		return "", false
	}
	if i := strings.Index(section, "\nHandle "); i >= 0 {
		section = section[:i]
	}
	return line + "\n" + section, true
}

// sizeOf is a size with its unit in bytes; 0 for a unit it does not know.
func sizeOf(val Num, unit string) Num {
	v := float64(val)
	switch upperASCII(unit) {
	case "MB", "MIB":
		return Num(v * 1024 * 1024)
	case "GB", "GIB":
		return Num(v * 1024 * 1024 * 1024)
	case "TB", "TIB":
		return Num(v * 1024 * 1024 * 1024 * 1024)
	}
	return 0
}

// letterChannel is the channel of a letter or digit: A is 0, 7 is 7.
func letterChannel(s string) Num {
	ch := upperASCII(s)
	if ch >= "0" && ch <= "9" {
		return ParseInt(ch)
	}
	return Num(ch[0]) - 65
}

// channelLetter finds the channel letter of a locator such as
// ChannelA-DIMM0 or CPU0_DIMM_A1. With FixDimmWord a letter that begins
// the word DIMM or DIM is passed over.
func channelLetter(locator string) (jsMatch, bool) {
	if !on(FixDimmWord) {
		return reChannelLetter.Match(locator)
	}
	for _, m := range reChannelLetter.All(locator) {
		rest := strings.ToUpper(locator[m.End():])
		if strings.EqualFold(m.Group(1), "D") && m.Group(2) == "" && strings.HasPrefix(rest, "IM") {
			continue
		}
		return m, true
	}
	return jsMatch{}, false
}

// placeDmidecode sets the socket and the channel of a slot from its
// locator and bank locator, with the names of the tests that fired.
func placeDmidecode(d *Dimm, locator, bank string, hints textHints) {
	pNode, isPNode := reBankPNode.Match(bank)
	var nodeCh jsMatch
	isNodeCh := false
	if !isPNode {
		nodeCh, isNodeCh = reBankNodeChannel.Match(bank)
	}
	dimmNum, isDimmNum := reDimmNumeric.Match(locator)
	var pLetter jsMatch
	isPLetter := false
	if on(FixPLetter) {
		pLetter, isPLetter = reDimmPLetter.Match(locator)
	}
	switch {
	case isPNode:
		d.Socket = ParseInt(pNode.Group(1))
		d.rawNode, d.rawChannel = ParseInt(pNode.Group(2)), ParseInt(pNode.Group(3))
		d.Channel = Num(float64(d.rawNode)*4 + float64(d.rawChannel))
		d.SocketBy, d.ChannelBy = ByPNodeChannelDimm, ByPNodeChannelDimm
		return
	case isNodeCh:
		d.Socket, d.Channel = ParseInt(nodeCh.Group(1)), ParseInt(nodeCh.Group(2))
		d.SocketBy, d.ChannelBy = ByNodeChannelDimm, ByNodeChannelDimm
		return
	case isDimmNum:
		d.Socket, d.Channel = ParseInt(dimmNum.Group(1)), ParseInt(dimmNum.Group(2))
		d.SocketBy, d.ChannelBy = ByDimmDigits, ByDimmDigits
		return
	case isPLetter:
		d.Socket, d.Channel = ParseInt(pLetter.Group(1)), letterChannel(pLetter.Group(2))
		d.SocketBy, d.ChannelBy = ByPLetter, ByPLetter
		return
	}
	dell, isDell := reDell.Match(locator)
	var dellDimm jsMatch
	isDellDimm := false
	if on(FixDellDimm) && !isDell && hints.dell() {
		dellDimm, isDellDimm = reDellDimm.Match(locator)
	}
	cpu, isCPU := reCPU.Match(locator)
	if !isCPU {
		cpu, isCPU = reCPU.Match(bank)
	}
	var cpuSp jsMatch
	isCPUSp := false
	if on(FixCPUWithSpace) && !isCPU {
		if cpuSp, isCPUSp = reCPUSpace.Match(locator); !isCPUSp {
			cpuSp, isCPUSp = reCPUSpace.Match(bank)
		}
	}
	pn, isPN := rePNode.Match(locator)
	if !isPN {
		pn, isPN = rePNode.Match(bank)
	}
	proc, isProc := reProc.Match(locator)
	switch {
	case isDell:
		d.Socket, d.SocketBy = Num(upperASCII(dell.Group(1))[0])-65, ByLetterNumber
	case isDellDimm:
		d.Socket, d.SocketBy = Num(upperASCII(dellDimm.Group(1))[0])-65, ByDellDimm
		d.dellNumber, d.hasDell = ParseInt(dellDimm.Group(2)), true
	case isProc:
		d.Socket, d.SocketBy = ParseInt(proc.Group(1))-1, ByProc
	case isCPU:
		d.Socket, d.SocketBy = ParseInt(cpu.Group(1)), ByCPU
	case isCPUSp:
		d.rawSocket = ParseInt(cpuSp.Group(1))
		d.Socket, d.SocketBy = d.rawSocket-1, ByCPUSpaced
	case isPN:
		d.rawSocket = ParseInt(pn.Group(1))
		d.Socket, d.SocketBy = d.rawSocket-1, ByPOrNode
	}
	if m, ok := reCxDx.Match(locator); ok {
		d.Channel, d.ChannelBy = ParseInt(m.Group(1)), ByCD
		return
	}
	if m, ok := reCHxDIMMx.Match(locator); ok {
		d.Channel, d.ChannelBy = ParseInt(m.Group(1)), ByCHDimm
		return
	}
	if m, ok := reBankChannelNum.Match(bank); ok {
		d.Channel, d.ChannelBy = ParseInt(m.Group(1)), ByBankChannel
		return
	}
	letters := func() bool {
		if m, ok := reDimmLetter.Match(locator); ok {
			d.Channel, d.ChannelBy = letterChannel(m.Group(1)), ByDimmLetter
			return true
		}
		if m, ok := channelLetter(locator); ok {
			d.Channel, d.ChannelBy = letterChannel(m.Group(1)), ByChannelLetter
			return true
		}
		return false
	}
	if on(FixLetterBeforeNode) && letters() {
		return
	}
	if m, ok := reBankNodeNum.Match(bank); ok {
		d.Channel, d.ChannelBy = ParseInt(m.Group(1)), ByBankNode
		return
	}
	if !on(FixLetterBeforeNode) && letters() {
		return
	}
	re := reBankChannelChar
	if on(FixChanWord) {
		re = reBankChanChar
	}
	if m, ok := re.Match(bank); ok {
		d.Channel, d.ChannelBy = letterChannel(m.Group(1)), ByBankChannelChar
	}
}

// secondPasses gives the slots named like A1 and like PROC 1 DIMM 9 their
// channels: the number of the slot, counted from 1, modulo half the slots
// of its socket. Then the fixes that look at all the slots of a text.
func secondPasses(dimms []Dimm, hints textHints) {
	isLetter := func(d *Dimm) bool {
		return reDellLocator.Test(d.Locator) || d.hasDell
	}
	if on(FixLettersFromA) {
		// A letter is a socket only when the letters of the text start at A.
		low := byte('Z' + 1)
		for i := range dimms {
			if d := &dimms[i]; d.SocketBy == ByLetterNumber || d.SocketBy == ByDellDimm {
				if c := upperASCII(d.Locator)[firstLetter(d)]; c < low {
					low = c
				}
			}
		}
		if low != 'A' && low <= 'Z' {
			for i := range dimms {
				if d := &dimms[i]; d.SocketBy == ByLetterNumber || d.SocketBy == ByDellDimm {
					d.Socket, d.SocketBy, d.hasDell, d.notLetter = 0, "", false, true
				}
			}
		}
	}
	channelsOf := func(n int) float64 { return maxf(1, ceil(float64(n)/2)) }
	if on(FixDellChannels) {
		if c, ok := ProcessorChannels(hints.processor); ok {
			channelsOf = func(int) float64 { return float64(c) }
		}
	}
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
			from := 1.0
			if by == ByLetterNumberPass {
				numChannels = channelsOf(len(group))
				if on(FixLettersFromA) {
					for _, d := range group {
						if n, ok := number(d); ok && n == 0 {
							from = 0
						}
					}
				}
			}
			for _, d := range group {
				if slot, ok := number(d); ok {
					d.Channel = Num(Mod(float64(slot)-from, numChannels))
					d.ChannelBy = by
				}
			}
		}
	}
	pass(func(d *Dimm) bool { return isLetter(d) && !d.notLetter },
		func(d *Dimm) (Num, bool) {
			if d.hasDell {
				return d.dellNumber, true
			}
			return ParseInt(d.Locator[1:]), true
		}, ByLetterNumberPass)
	pass(func(d *Dimm) bool { return reHPLocator.Test(d.Locator) },
		func(d *Dimm) (Num, bool) {
			m, ok := reDimmNumber.Match(d.Locator)
			if !ok {
				return 0, false
			}
			return ParseInt(m.Group(1)), true
		}, ByProcDimmPass)
	if on(FixNumberingBase) {
		zero := map[string]bool{}
		for _, d := range dimms {
			if counted(d.SocketBy) && d.rawSocket == 0 {
				zero[d.SocketBy] = true
			}
		}
		for i := range dimms {
			if d := &dimms[i]; counted(d.SocketBy) && zero[d.SocketBy] {
				d.Socket = d.rawSocket
			}
		}
	}
	if on(FixSupermicroNode) {
		first := map[float64]float64{}
		for _, d := range dimms {
			if d.ChannelBy != ByPNodeChannelDimm {
				continue
			}
			s := float64(d.Socket) + 0
			if n, ok := first[s]; !ok || float64(d.rawNode) < n {
				first[s] = float64(d.rawNode)
			}
		}
		for i := range dimms {
			if d := &dimms[i]; d.ChannelBy == ByPNodeChannelDimm {
				d.Channel = Num((float64(d.rawNode)-first[float64(d.Socket)+0])*4 + float64(d.rawChannel))
			}
		}
	}
}

// counted tells the socket tests whose numbers may count from 0 or 1.
func counted(by string) bool { return by == ByPOrNode || by == ByCPUSpaced || by == ByP }

// firstLetter is the offset of the letter that names the socket.
func firstLetter(d *Dimm) int {
	if d.SocketBy == ByDellDimm {
		return len("DIMM_")
	}
	return 0
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
// global search with a look-ahead of JavaScript. At most max+1 blocks.
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
func ParseLshwDimms(raw string, ce, ue Counts) []Dimm {
	d, _ := ParseLshwDimmsOf(SlotHost{}, raw, ce, ue)
	return d
}

// ParseLshwDimmsOf reads the banks of the memory node of lshw text, with
// what the ledger knows of the host as the hints of the pass of the second
// round, which reads the slot string as the locator of a slot without a
// bank locator.
func ParseLshwDimmsOf(host SlotHost, raw string, ce, ue Counts) ([]Dimm, Facts) {
	dimms := parseLshwDimms(raw, ce, ue)
	h := hintsOf(WriteSlotsOf(host, nil))
	f := Facts{Sockets: len(h.cpus)}
	if on(FixNameForms) && len(dimms) > 0 && len(dimms) <= MaxSlots {
		f = placeNames(dimms, h)
	}
	return dimms, f
}

func parseLshwDimms(raw string, ce, ue Counts) []Dimm {
	raw = lineEnds(raw)
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
		var socket, channel, rawSocket Num
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
				rawSocket = ParseInt(p.Group(1))
				socket, socketBy = rawSocket-1, ByP
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
			SocketBy: socketBy, ChannelBy: channelBy, rawSocket: rawSocket, passLocator: slotStr,
		})
	}
	secondPasses(dimms, textHints{})
	return dimms
}
