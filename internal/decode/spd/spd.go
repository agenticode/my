// Package spd decodes the Serial Presence Detect (SPD) EEPROM of a DDR4 or
// DDR5 memory module: what the module maker wrote about the module (type,
// ranks, device width, die density and package, maker codes, the
// manufacturing week, serial and part number) and the CRC that guards it.
// It also reads the forms such bytes travel in: binary files, hex text of
// hexdump, od, xxd, i2cdump and similar tools, and the text decode-dimms
// prints. It reads bytes and text and does no I/O.
//
// The byte offsets and codings follow JEDEC Standard 21-C Annex L (DDR4)
// and JESD400-5 (DDR5), as the public decoders of illumos (libjedec) and
// i2c-tools (decode-dimms) give them; docs/compatibility.md lists what is
// decoded and what it was checked against. Other memory types are named by
// their key byte and not decoded.
//
// A date is read as two BCD digits of year and week, week 1 to 53. Bytes
// that are no such digits are no date: the decoder makes no second guess.
package spd

import (
	"fmt"
	"math"

	"github.com/agenticode/my/internal/model"
)

// States of one reading. A decode gives one of the first five; the reader
// of a host gives the last three when a read fails before the first byte.
const (
	// StateOK: the bytes hold the layout of the memory type and its base
	// CRC is good.
	StateOK = "ok"
	// StateCRC: the base CRC is bad. The fields are decoded and shown with
	// the two CRC values; nothing of them may enter the ledger's DIMM row.
	StateCRC = "crc"
	// StateShort: fewer bytes than a decoded field needs.
	StateShort = "short"
	// StateBlank: the first 128 bytes are all 0x00 or all 0xFF.
	StateBlank = "blank"
	// StateType: byte 2 names a memory type that is not decoded, or none.
	StateType = "type"
	// StateDenied, StateIO and StateTimeout: the read failed before the
	// first byte (permission, an I/O error, the deadline).
	StateDenied  = "denied"
	StateIO      = "io"
	StateTimeout = "timeout"
)

// States lists the states in the order of design and documentation.
var States = []string{StateOK, StateCRC, StateShort, StateBlank, StateType, StateDenied, StateIO, StateTimeout}

// Read tells whether a reading in state s counts as read: its fields can
// be shown.
func Read(s string) bool { return s == StateOK || s == StateCRC || s == StateShort }

// Key bytes (byte 2) of the memory types that are decoded.
const (
	KeyDDR4 = 0x0C
	KeyDDR5 = 0x12
)

// keyBytes names the memory type of each key byte of the SPD.
var keyBytes = map[byte]string{
	0x01: "FPM DRAM",
	0x02: "EDO",
	0x03: "Pipelined Nibble",
	0x04: "SDRAM",
	0x05: "ROM",
	0x06: "DDR SGRAM",
	0x07: "DDR",
	0x08: "DDR2",
	0x09: "DDR2 FB-DIMM",
	0x0A: "DDR2 FB-DIMM probe",
	0x0B: "DDR3",
	0x0C: "DDR4",
	0x0E: "DDR4E",
	0x0F: "LPDDR3",
	0x10: "LPDDR4",
	0x11: "LPDDR4X",
	0x12: "DDR5",
	0x13: "LPDDR5",
	0x14: "DDR5 NVDIMM-P",
	0x15: "LPDDR5X",
}

// MemoryType names the memory type of a key byte, or "" for a byte that
// names none.
func MemoryType(key byte) string { return keyBytes[key] }

// KeyBytes returns the key bytes that name a memory type, in order.
func KeyBytes() []byte {
	out := make([]byte, 0, len(keyBytes))
	for k := 0; k < 256; k++ {
		if _, ok := keyBytes[byte(k)]; ok {
			out = append(out, byte(k))
		}
	}
	return out
}

// CRC is one CRC of the layout: the bytes it covers, the value stored and
// the value computed.
type CRC struct {
	// From and To are the first and the last byte covered; At is where the
	// value is stored, low byte first.
	From int `json:"from"`
	To   int `json:"to"`
	At   int `json:"at"`
	// State is "ok", "bad", or "missing" when the bytes are not all there.
	State    string `json:"state"`
	Stored   int    `json:"stored"`
	Computed int    `json:"computed"`
}

// CRC states.
const (
	CRCGood    = "ok"
	CRCBad     = "bad"
	CRCMissing = "missing"
)

// Gap names a part of the layout the bytes did not reach.
type Gap struct {
	// Part is GapBase, GapModuleCRC, GapRanks or GapMaker.
	Part string `json:"part"`
	// Need is the number of bytes the part needs.
	Need int `json:"need"`
}

// Parts of the layout a short reading can miss.
const (
	// GapBase: the first 128 bytes, which hold everything the decoder reads
	// of the organisation.
	GapBase = "base"
	// GapModuleCRC: the CRC of DDR4 bytes 128-253.
	GapModuleCRC = "module-crc"
	// GapRanks: the bytes of DDR5 ranks, bus widths and the base CRC.
	GapRanks = "ranks"
	// GapMaker: the manufacturing block (maker, location, date, serial,
	// part number).
	GapMaker = "manufacturing"
)

// Notes a decode can carry.
const (
	// NoteModuleCRC: the CRC of DDR4 bytes 128-253 is bad. No byte of that
	// block is decoded.
	NoteModuleCRC = "module-crc"
	// NoteAsymmetric: the ranks are of two kinds; organisation and size are
	// those of the first.
	NoteAsymmetric = "asymmetric"
	// NoteNoDate: the date bytes are no BCD year and week.
	NoteNoDate = "no-date"
	// NotePartNotASCII: the part number holds bytes that are no printable
	// ASCII and is not shown.
	NotePartNotASCII = "part-not-ascii"
)

// Decoded is what the decoder reads from the bytes of one SPD (design
// section 5.6). Fields the bytes do not reach are unset.
type Decoded struct {
	// Length is the number of bytes decoded.
	Length int `json:"length"`
	// State is one of the decode states (StateOK and the others).
	State string `json:"state"`
	// KeyByte is byte 2; MemoryType its name.
	KeyByte    model.Opt[int] `json:"key_byte,omitzero"`
	MemoryType string         `json:"memory_type,omitzero"`
	// Fill is the byte a blank SPD holds (0x00 or 0xFF).
	Fill model.Opt[int] `json:"fill,omitzero"`

	ModuleType string `json:"module_type,omitzero"`
	Hybrid     bool   `json:"hybrid,omitzero"`
	Revision   string `json:"spd_revision,omitzero"`
	// DeviceBytes is the size of the SPD device of a DDR5 module.
	DeviceBytes model.Opt[int] `json:"spd_device_bytes,omitzero"`
	CRCs        []CRC          `json:"crcs,omitzero"`

	// DieMbit is the capacity of one die in megabits.
	DieMbit        model.Opt[int] `json:"die_mbit,omitzero"`
	DiesPerPackage model.Opt[int] `json:"dies_per_package,omitzero"`
	// Package is monolithic, multi-load stack, 3DS, DDP, non-monolithic or
	// reserved.
	Package     string         `json:"package,omitzero"`
	RowBits     model.Opt[int] `json:"row_bits,omitzero"`
	ColumnBits  model.Opt[int] `json:"column_bits,omitzero"`
	DeviceWidth model.Opt[int] `json:"device_width,omitzero"`
	// PackageRanks counts the ranks of packages (per sub-channel on DDR5);
	// LogicalRanks counts the dies of a 3DS stack as ranks.
	PackageRanks model.Opt[int] `json:"package_ranks,omitzero"`
	LogicalRanks model.Opt[int] `json:"logical_ranks,omitzero"`
	// RankMix is symmetric or asymmetric.
	RankMix string `json:"rank_mix,omitzero"`
	// BusWidth is the primary bus width in bits (per sub-channel on DDR5),
	// ExtensionBits the bits beside it (ECC).
	BusWidth      model.Opt[int]   `json:"bus_width,omitzero"`
	ExtensionBits model.Opt[int]   `json:"extension_bits,omitzero"`
	SubChannels   model.Opt[int]   `json:"sub_channels,omitzero"`
	SizeBytes     model.Opt[int64] `json:"size_bytes,omitzero"`
	// Organisation is package ranks and device width, as 2Rx4.
	Organisation string `json:"organisation,omitzero"`
	// TCKps is the minimum cycle time in picoseconds, SpeedMTs the rated
	// speed it gives.
	TCKps    model.Opt[int] `json:"tck_ps,omitzero"`
	SpeedMTs model.Opt[int] `json:"speed_mts,omitzero"`

	// ModuleMaker and DRAMMaker are JEDEC codes in the form of SMBIOS
	// ("bank 1, 0x2C"); the names are those of the table of this package,
	// empty for a code it does not name. A decode-dimms text gives names
	// and no codes.
	ModuleMaker     string `json:"module_maker,omitzero"`
	ModuleMakerName string `json:"module_maker_name,omitzero"`
	// Location is the manufacturing location byte, in the maker's own
	// coding.
	Location string `json:"location,omitzero"`
	// Date is the manufacturing week, 2021-W43; DateBytes the two bytes as
	// stored (0x2143), also when they are no date.
	Date           string `json:"mfg_date,omitzero"`
	DateBytes      string `json:"date_bytes,omitzero"`
	Serial         string `json:"serial,omitzero"`
	PartNumber     string `json:"part_number,omitzero"`
	ModuleRevision string `json:"module_revision,omitzero"`
	DRAMMaker      string `json:"dram_maker,omitzero"`
	DRAMMakerName  string `json:"dram_maker_name,omitzero"`
	DRAMStepping   string `json:"dram_stepping,omitzero"`

	// Missing names the parts of the layout the bytes did not reach.
	Missing []Gap `json:"missing,omitzero"`
	// Notes are remarks (NoteModuleCRC and the others).
	Notes []string `json:"notes,omitzero"`
}

// BaseCRC returns the CRC of the first block, the one that decides the
// state.
func (d *Decoded) BaseCRC() (CRC, bool) {
	if len(d.CRCs) == 0 {
		return CRC{}, false
	}
	return d.CRCs[0], true
}

// HasSerial reports whether the decode reached the serial number.
func (d *Decoded) HasSerial() bool { return d.Serial != "" }

func bits(v byte, hi, lo uint) int { return int(v>>lo) & (1<<(hi-lo+1) - 1) }

func bcd(v byte) (int, bool) {
	hi, lo := int(v>>4), int(v&15)
	if hi > 9 || lo > 9 {
		return 0, false
	}
	return hi*10 + lo, true
}

// DateCode returns the manufacturing week of the two date bytes, as
// 2021-W43, or "" when the bytes are no BCD year and week 1 to 53.
func DateCode(year, week byte) string {
	y, ok1 := bcd(year)
	w, ok2 := bcd(week)
	if !ok1 || !ok2 || w < 1 || w > 53 {
		return ""
	}
	return fmt.Sprintf("%04d-W%02d", 2000+y, w)
}

// MakerCode returns the JEDEC code of a continuation byte (count with
// parity in bit 7) and a code byte, in the form SMBIOS uses: "bank 1,
// 0xCE". Bytes that are both 0x00 or both 0xFF are no code.
func MakerCode(count, code byte) string {
	if count == 0 && code == 0 || count == 0xFF && code == 0xFF {
		return ""
	}
	return fmt.Sprintf("bank %d, 0x%02X", int(count&0x7F)+1, code)
}

// partNumber reads the ASCII part number: cut at the first NUL, padding
// removed. Bytes that are no printable ASCII make no part number.
func partNumber(b []byte) (string, bool) {
	for i, c := range b {
		if c == 0 {
			b = b[:i]
			break
		}
	}
	for _, c := range b {
		if c < 0x20 || c > 0x7E {
			return "", false
		}
	}
	s := string(b)
	for len(s) > 0 && s[len(s)-1] == ' ' {
		s = s[:len(s)-1]
	}
	for len(s) > 0 && s[0] == ' ' {
		s = s[1:]
	}
	return s, true
}

var widths = map[int]int{0: 4, 1: 8, 2: 16, 3: 32}
var busWidths = map[int]int{0: 8, 1: 16, 2: 32, 3: 64}

// Module types of byte 3, bits 3-0.
var ddr4Modules = map[int]string{1: "RDIMM", 2: "UDIMM", 3: "SODIMM", 4: "LRDIMM", 5: "Mini-RDIMM", 6: "Mini-UDIMM",
	8: "72b-SO-RDIMM", 9: "72b-SO-UDIMM", 12: "16b-SO-DIMM", 13: "32b-SO-DIMM"}
var ddr5Modules = map[int]string{1: "RDIMM", 2: "UDIMM", 3: "SODIMM", 4: "LRDIMM", 5: "CUDIMM", 6: "CSODIMM", 7: "MRDIMM",
	8: "CAMM2", 9: "SOCAMM2", 10: "DDIMM", 11: "solder down"}

// ModuleTypes returns the module type names of a memory type by code, for
// documentation and tests.
func ModuleTypes(memoryType string) map[int]string {
	out := map[int]string{}
	src := ddr4Modules
	if memoryType == "DDR5" {
		src = ddr5Modules
	}
	for k, v := range src {
		out[k] = v
	}
	return out
}

func moduleType(table map[int]string, v byte) string {
	if s, ok := table[int(v&15)]; ok {
		return s
	}
	return fmt.Sprintf("type 0x%X", v&15)
}

// Die capacity in megabits: DDR4 byte 4 bits 3-0, DDR5 byte 4 bits 4-0
// (in gigabits there).
var ddr4Dies = map[int]int{0: 256, 1: 512, 2: 1024, 3: 2048, 4: 4096, 5: 8192, 6: 16384, 7: 32768, 8: 12288, 9: 24576}
var ddr5Dies = map[int]int{1: 4, 2: 8, 3: 12, 4: 16, 5: 24, 6: 32, 7: 48, 8: 64}

// DDR5 byte 4 bits 7-5: dies per package and the kind of package.
var ddr5Stacks = map[int]struct {
	n    int
	kind string
}{0: {1, "monolithic"}, 1: {2, "DDP"}, 2: {2, "3DS"}, 3: {4, "3DS"}, 4: {8, "3DS"}, 5: {16, "3DS"}}

func crcOf(b []byte, from, to, at int) CRC {
	c := CRC{From: from, To: to, At: at}
	if len(b) < at+2 {
		c.State = CRCMissing
		return c
	}
	c.Computed = int(CRC16(b[from : to+1]))
	c.Stored = int(b[at]) | int(b[at+1])<<8
	c.State = CRCBad
	if c.Computed == c.Stored {
		c.State = CRCGood
	}
	return c
}

// Decode reads the bytes of one SPD. It takes any bytes and never panics;
// bytes beyond 4,096 are not looked at.
func Decode(b []byte) Decoded {
	if len(b) > MaxBinary {
		b = b[:MaxBinary]
	}
	d := Decoded{Length: len(b)}
	if len(b) > 0 && blank(b) {
		d.State, d.Fill = StateBlank, model.Some(int(b[0]))
		return d
	}
	if len(b) < 3 {
		d.State = StateShort
		d.Missing = []Gap{{GapBase, 128}}
		return d
	}
	d.KeyByte = model.Some(int(b[2]))
	d.MemoryType = keyBytes[b[2]]
	switch b[2] {
	case KeyDDR4:
		decodeDDR4(b, &d)
	case KeyDDR5:
		decodeDDR5(b, &d)
	default:
		d.State = StateType
		return d
	}
	d.State = StateOK
	switch {
	case len(d.CRCs) > 0 && d.CRCs[0].State == CRCBad:
		d.State = StateCRC
	case len(d.Missing) > 0:
		d.State = StateShort
	}
	return d
}

// blank reports whether the first 128 bytes (or all, when fewer) are all
// 0x00 or all 0xFF.
func blank(b []byte) bool {
	n := min(len(b), 128)
	for i := 1; i < n; i++ {
		if b[i] != b[0] {
			return false
		}
	}
	return b[0] == 0x00 || b[0] == 0xFF
}

func decodeDDR4(b []byte, d *Decoded) {
	if len(b) < 128 {
		d.Missing = append(d.Missing, Gap{GapBase, 128})
		return
	}
	d.Revision = fmt.Sprintf("%d.%d", b[1]>>4, b[1]&15)
	d.ModuleType = moduleType(ddr4Modules, b[3])
	d.Hybrid = b[3]&0x80 != 0
	d.CRCs = append(d.CRCs, crcOf(b, 0, 125, 126))
	mbit, okDie := ddr4Dies[bits(b[4], 3, 0)]
	if okDie {
		d.DieMbit = model.Some(mbit)
	}
	d.RowBits = model.Some(12 + bits(b[5], 5, 3))
	d.ColumnBits = model.Some(9 + bits(b[5], 2, 0))
	mono := bits(b[6], 7, 7) == 0
	dies := 1 + bits(b[6], 6, 4)
	load := bits(b[6], 1, 0)
	d.DiesPerPackage = model.Some(dies)
	switch {
	case mono:
		d.Package = "monolithic"
	case load == 0:
		d.Package = "non-monolithic"
	case load == 1:
		d.Package = "multi-load stack"
	case load == 2:
		d.Package = "3DS"
	default:
		d.Package = "reserved"
	}
	width, okWidth := widths[bits(b[12], 2, 0)]
	pranks := 1 + bits(b[12], 5, 3)
	if okWidth {
		d.DeviceWidth = model.Some(width)
	}
	d.PackageRanks = model.Some(pranks)
	d.RankMix = "symmetric"
	if bits(b[12], 6, 6) == 1 {
		d.RankMix = "asymmetric"
		d.Notes = append(d.Notes, NoteAsymmetric)
	}
	pri, okBus := busWidths[bits(b[13], 2, 0)]
	if okBus {
		d.BusWidth = model.Some(pri)
	}
	switch bits(b[13], 4, 3) {
	case 0:
		d.ExtensionBits = model.Some(0)
	case 1:
		d.ExtensionBits = model.Some(8)
	}
	logical := pranks
	if !mono && load == 2 {
		logical = pranks * dies
	}
	d.LogicalRanks = model.Some(logical)
	if okDie && okBus && okWidth {
		d.SizeBytes = model.Some(int64(mbit) * (1 << 20) / 8 * int64(pri) / int64(width) * int64(logical))
	}
	if okWidth {
		d.Organisation = fmt.Sprintf("%dRx%d", pranks, width)
	}
	fine := int(int8(b[125]))
	tck := int(b[18])*125 + fine
	d.TCKps = model.Some(tck)
	if tck > 0 {
		step := 400.0 / 3
		d.SpeedMTs = model.Some(int(math.Round(2_000_000/float64(tck)/step) * step))
	}
	c := crcOf(b, 128, 253, 254)
	d.CRCs = append(d.CRCs, c)
	switch c.State {
	case CRCMissing:
		d.Missing = append(d.Missing, Gap{GapModuleCRC, 256})
	case CRCBad:
		d.Notes = append(d.Notes, NoteModuleCRC)
	}
	if len(b) < 353 {
		d.Missing = append(d.Missing, Gap{GapMaker, 353})
		return
	}
	maker(b, 320, d)
}

func decodeDDR5(b []byte, d *Decoded) {
	if len(b) < 128 {
		d.Missing = append(d.Missing, Gap{GapBase, 128})
		return
	}
	switch bits(b[0], 6, 4) {
	case 1:
		d.DeviceBytes = model.Some(256)
	case 2:
		d.DeviceBytes = model.Some(512)
	case 3:
		d.DeviceBytes = model.Some(1024)
	case 4:
		d.DeviceBytes = model.Some(2048)
	}
	d.Revision = fmt.Sprintf("%d.%d", b[1]>>4, b[1]&15)
	d.ModuleType = moduleType(ddr5Modules, b[3])
	d.Hybrid = b[3]&0x80 != 0
	gbit, okDie := ddr5Dies[bits(b[4], 4, 0)]
	if okDie {
		d.DieMbit = model.Some(gbit * 1024)
	}
	stack, okStack := ddr5Stacks[bits(b[4], 7, 5)]
	if okStack {
		d.DiesPerPackage = model.Some(stack.n)
		d.Package = stack.kind
	} else {
		d.Package = "reserved"
	}
	d.RowBits = model.Some(16 + bits(b[5], 4, 0))
	d.ColumnBits = model.Some(10 + bits(b[5], 7, 5))
	width, okWidth := widths[bits(b[6], 7, 5)]
	if okWidth {
		d.DeviceWidth = model.Some(width)
	}
	tck := int(b[20]) | int(b[21])<<8
	d.TCKps = model.Some(tck)
	if tck > 0 {
		d.SpeedMTs = model.Some(int(math.Round(2_000_000/float64(tck)/400) * 400))
	}
	c := crcOf(b, 0, 509, 510)
	d.CRCs = append(d.CRCs, c)
	if c.State == CRCMissing {
		d.Missing = append(d.Missing, Gap{GapRanks, 512}, Gap{GapMaker, 555})
	} else {
		pranks := 1 + bits(b[234], 5, 3)
		d.PackageRanks = model.Some(pranks)
		d.RankMix = "symmetric"
		if bits(b[234], 6, 6) == 1 {
			d.RankMix = "asymmetric"
			d.Notes = append(d.Notes, NoteAsymmetric)
		}
		pri, okBus := busWidths[bits(b[235], 2, 0)]
		if okBus {
			d.BusWidth = model.Some(pri)
		}
		switch bits(b[235], 4, 3) {
		case 0:
			d.ExtensionBits = model.Some(0)
		case 1:
			d.ExtensionBits = model.Some(4)
		case 2:
			d.ExtensionBits = model.Some(8)
		}
		sub := 1 + bits(b[235], 7, 5)
		d.SubChannels = model.Some(sub)
		logical := pranks
		if okStack && stack.kind == "3DS" {
			logical = pranks * stack.n
		}
		d.LogicalRanks = model.Some(logical)
		if okDie && okStack && okBus && okWidth {
			d.SizeBytes = model.Some(int64(sub) * int64(pri/width) * int64(stack.n) * (int64(gbit) << 30) / 8 * int64(pranks))
		}
		if okWidth {
			d.Organisation = fmt.Sprintf("%dRx%d", pranks, width)
		}
	}
	if len(b) < 555 {
		if c.State != CRCMissing {
			d.Missing = append(d.Missing, Gap{GapMaker, 555})
		}
		return
	}
	maker(b, 512, d)
}

// maker decodes the manufacturing block that starts at off: the same
// layout on DDR4 (320) and DDR5 (512), with a part number of 20 or 30
// characters.
func maker(b []byte, off int, d *Decoded) {
	partLen, dram := 20, 350
	if off == 512 {
		partLen, dram = 30, 552
	}
	d.ModuleMaker = MakerCode(b[off], b[off+1])
	d.ModuleMakerName = MakerName(d.ModuleMaker)
	d.Location = fmt.Sprintf("0x%02X", b[off+2])
	d.DateBytes = fmt.Sprintf("0x%02X%02X", b[off+3], b[off+4])
	d.Date = DateCode(b[off+3], b[off+4])
	if d.Date == "" {
		d.Notes = append(d.Notes, NoteNoDate)
	}
	d.Serial = fmt.Sprintf("%02X%02X%02X%02X", b[off+5], b[off+6], b[off+7], b[off+8])
	p, ok := partNumber(b[off+9 : off+9+partLen])
	d.PartNumber = p
	if !ok {
		d.Notes = append(d.Notes, NotePartNotASCII)
	}
	rev := off + 9 + partLen
	d.ModuleRevision = fmt.Sprintf("0x%02X", b[rev])
	d.DRAMMaker = MakerCode(b[dram], b[dram+1])
	d.DRAMMakerName = MakerName(d.DRAMMaker)
	d.DRAMStepping = fmt.Sprintf("0x%02X", b[dram+2])
}
