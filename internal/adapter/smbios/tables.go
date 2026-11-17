package smbios

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"github.com/agenticode/my/internal/model"
)

// Structure is one SMBIOS structure: the formatted area and its strings.
type Structure struct {
	Type   byte
	Handle uint16
	// Data is the formatted area including the four header bytes.
	Data []byte
	// Strings is the string set that follows the formatted area; string
	// number n of the specification is Strings[n-1].
	Strings []string
}

// str returns the string a string-number field at offset off refers to.
func (s *Structure) str(off int) string {
	if off >= len(s.Data) {
		return ""
	}
	n := int(s.Data[off])
	if n == 0 || n > len(s.Strings) {
		return ""
	}
	return strings.TrimSpace(s.Strings[n-1])
}

func (s *Structure) u8(off int) (byte, bool) {
	if off+1 > len(s.Data) {
		return 0, false
	}
	return s.Data[off], true
}

func (s *Structure) u16(off int) (uint16, bool) {
	if off+2 > len(s.Data) {
		return 0, false
	}
	return binary.LittleEndian.Uint16(s.Data[off:]), true
}

func (s *Structure) u32(off int) (uint32, bool) {
	if off+4 > len(s.Data) {
		return 0, false
	}
	return binary.LittleEndian.Uint32(s.Data[off:]), true
}

// maxStructures bounds a table walk; no real table comes near it.
const maxStructures = 20000

// ParseTable walks an SMBIOS structure table, the content of
// /sys/firmware/dmi/tables/DMI. It returns the structures it could read
// and, if the table is damaged, an error saying where the walk stopped.
func ParseTable(b []byte) ([]Structure, error) {
	var out []Structure
	off := 0
	for off < len(b) {
		if len(out) >= maxStructures {
			return out, fmt.Errorf("more than %d structures; the table is not plausible", maxStructures)
		}
		if off+4 > len(b) {
			return out, fmt.Errorf("structure header at offset %d runs past the end of the table", off)
		}
		length := int(b[off+1])
		if length < 4 {
			return out, fmt.Errorf("structure at offset %d has length %d, below the 4-byte header", off, length)
		}
		if off+length > len(b) {
			return out, fmt.Errorf("structure at offset %d (type %d, %d bytes) runs past the end of the table", off, b[off], length)
		}
		s := Structure{Type: b[off], Handle: binary.LittleEndian.Uint16(b[off+2:]), Data: b[off : off+length]}
		// The string set: NUL-terminated strings, closed by one more NUL.
		// A structure without strings is followed by two NULs.
		p := off + length
		for {
			end := p
			for end < len(b) && b[end] != 0 {
				end++
			}
			if end >= len(b) {
				return out, fmt.Errorf("strings of the structure at offset %d (type %d) are not terminated", off, s.Type)
			}
			if end == p {
				p++ // the closing NUL
				// A structure without strings has two NULs; a table
				// with only one there is tolerated.
				if len(s.Strings) == 0 && p < len(b) && b[p] == 0 {
					p++
				}
				break
			}
			s.Strings = append(s.Strings, string(b[p:end]))
			p = end + 1
		}
		out = append(out, s)
		off = p
		if s.Type == 127 { // end-of-table
			break
		}
	}
	return out, nil
}

// EntryPoint is what the SMBIOS entry point structure says about the table.
type EntryPoint struct {
	Major, Minor, Docrev int
	// Bits is 32 for the "_SM_" entry point and 64 for "_SM3_".
	Bits int
}

// ParseEntryPoint reads /sys/firmware/dmi/tables/smbios_entry_point.
func ParseEntryPoint(b []byte) (EntryPoint, error) {
	switch {
	case len(b) >= 24 && string(b[:5]) == "_SM3_":
		return EntryPoint{Major: int(b[7]), Minor: int(b[8]), Docrev: int(b[9]), Bits: 64}, nil
	case len(b) >= 31 && string(b[:4]) == "_SM_":
		return EntryPoint{Major: int(b[6]), Minor: int(b[7]), Bits: 32}, nil
	}
	return EntryPoint{}, errors.New("no SMBIOS entry point anchor (_SM_ or _SM3_)")
}

var memoryTypes = map[byte]string{
	0x01: "Other", 0x02: "Unknown", 0x03: "DRAM", 0x04: "EDRAM", 0x05: "VRAM", 0x06: "SRAM", 0x07: "RAM", 0x08: "ROM",
	0x09: "Flash", 0x0A: "EEPROM", 0x0B: "FEPROM", 0x0C: "EPROM", 0x0D: "CDRAM", 0x0E: "3DRAM", 0x0F: "SDRAM", 0x10: "SGRAM",
	0x11: "RDRAM", 0x12: "DDR", 0x13: "DDR2", 0x14: "DDR2 FB-DIMM", 0x18: "DDR3", 0x19: "FBD2", 0x1A: "DDR4", 0x1B: "LPDDR",
	0x1C: "LPDDR2", 0x1D: "LPDDR3", 0x1E: "LPDDR4", 0x1F: "Logical non-volatile device", 0x20: "HBM", 0x21: "HBM2",
	0x22: "DDR5", 0x23: "LPDDR5", 0x24: "HBM3",
}

var formFactors = map[byte]string{
	0x01: "Other", 0x02: "Unknown", 0x03: "SIMM", 0x04: "SIP", 0x05: "Chip", 0x06: "DIP", 0x07: "ZIP",
	0x08: "Proprietary Card", 0x09: "DIMM", 0x0A: "TSOP", 0x0B: "Row Of Chips", 0x0C: "RIMM", 0x0D: "SODIMM",
	0x0E: "SRIMM", 0x0F: "FB-DIMM", 0x10: "Die",
}

var technologies = map[byte]string{
	0x01: "Other", 0x02: "Unknown", 0x03: "DRAM", 0x04: "NVDIMM-N", 0x05: "NVDIMM-F", 0x06: "NVDIMM-P",
	0x07: "Intel Optane persistent memory",
}

// Type detail bits of the memory device structure.
const (
	detailRegistered = 1 << 13
	detailUnbuffered = 1 << 14
	detailLRDIMM     = 1 << 15
)

// moduleType names the module construction from the type detail field.
func moduleType(detail uint16) string {
	switch {
	case detail&detailLRDIMM != 0:
		return "LRDIMM"
	case detail&detailRegistered != 0:
		return "RDIMM"
	case detail&detailUnbuffered != 0:
		return "UDIMM"
	}
	return ""
}

// placeholder reports whether a firmware string says "nothing here".
func placeholder(s string) bool { return model.Placeholder(s) }

func clean(s string) string {
	if placeholder(s) {
		return ""
	}
	return strings.TrimSpace(s)
}

// memoryDevice decodes a type 17 structure. Fields beyond the length of the
// structure (older SMBIOS versions) stay unset.
func memoryDevice(s *Structure) *model.Module {
	m := &model.Module{Handle: model.Some(int(s.Handle))}
	m.Locator = strings.TrimSpace(s.str(0x10))
	m.BankLocator = clean(s.str(0x11))
	d := &m.DIMM

	if size, ok := s.u16(0x0C); ok {
		switch {
		case size == 0:
			// No module installed.
		case size == 0xFFFF:
			m.Populated = true // installed, size unknown
		case size == 0x7FFF:
			m.Populated = true
			if ext, ok := s.u32(0x1C); ok {
				d.SizeBytes = int64(ext&0x7FFFFFFF) << 20
			}
		case size&0x8000 != 0:
			m.Populated = true
			d.SizeBytes = int64(size&0x7FFF) << 10
		default:
			m.Populated = true
			d.SizeBytes = int64(size) << 20
		}
	}
	if !m.Populated {
		return m
	}
	if v, ok := s.u16(0x08); ok && v != 0 && v != 0xFFFF {
		d.TotalWidth = model.Some(int(v))
	}
	if v, ok := s.u16(0x0A); ok && v != 0 && v != 0xFFFF {
		d.DataWidth = model.Some(int(v))
	}
	if v, ok := s.u8(0x0E); ok {
		d.FormFactor = clean(formFactors[v])
	}
	if v, ok := s.u8(0x12); ok {
		d.MemoryType = clean(memoryTypes[v])
	}
	if v, ok := s.u16(0x13); ok {
		d.ModuleType = moduleType(v)
	}
	speed := func(off, ext int) model.Opt[int] {
		v, ok := s.u16(off)
		switch {
		case !ok || v == 0:
			return model.Opt[int]{}
		case v == 0xFFFF:
			if e, ok := s.u32(ext); ok && e&0x7FFFFFFF != 0 {
				return model.Some(int(e & 0x7FFFFFFF))
			}
			return model.Opt[int]{}
		}
		return model.Some(int(v))
	}
	d.SpeedMTs = speed(0x15, 0x54)
	d.ConfiguredSpeedMTs = speed(0x20, 0x58)
	d.Manufacturer = clean(s.str(0x17))
	d.Serial = clean(s.str(0x18))
	d.PartNumber = clean(s.str(0x1A))
	if v, ok := s.u8(0x1B); ok && v&0x0F != 0 {
		d.Ranks = model.Some(int(v & 0x0F))
	}
	if v, ok := s.u8(0x28); ok {
		d.Technology = clean(technologies[v])
	}
	if v, ok := s.u16(0x2C); ok && v != 0 {
		// JEDEC JEP-106: continuation count in the low byte (bit 7 is
		// parity), the ID code in the high byte.
		d.ManufacturerID = fmt.Sprintf("bank %d, 0x%02X", (v&0x7F)+1, v>>8)
	}
	return m
}

// Builder assembles an SMBIOS structure table. The synthetic data generator
// uses it to write the tables a host would expose, and the tests use it to
// build tables with known content.
type Builder struct {
	buf []byte
}

// Add appends a structure. data is the formatted area after the four
// header bytes; fields that hold a string number refer to strs, counting
// from 1.
func (b *Builder) Add(typ byte, handle uint16, data []byte, strs ...string) {
	b.buf = append(b.buf, typ, byte(4+len(data)), byte(handle), byte(handle>>8))
	b.buf = append(b.buf, data...)
	for _, s := range strs {
		b.buf = append(b.buf, s...)
		b.buf = append(b.buf, 0)
	}
	if len(strs) == 0 {
		b.buf = append(b.buf, 0)
	}
	b.buf = append(b.buf, 0)
}

// Bytes returns the table closed by an end-of-table structure.
func (b *Builder) Bytes() []byte {
	out := append([]byte(nil), b.buf...)
	return append(out, 127, 4, 0xFE, 0xFF, 0, 0)
}

// EntryPoint64 returns a 64-bit entry point ("_SM3_") for a table of the
// given length and SMBIOS version.
func EntryPoint64(major, minor byte, tableLen int) []byte {
	ep := make([]byte, 24)
	copy(ep, "_SM3_")
	ep[6] = 24 // entry point length
	ep[7], ep[8], ep[9] = major, minor, 0
	ep[10] = 1 // entry point revision
	binary.LittleEndian.PutUint32(ep[12:], uint32(tableLen))
	var sum byte
	for _, v := range ep {
		sum += v
	}
	ep[5] = -sum
	return ep
}

// MemoryDevice describes a type 17 structure for Builder.AddMemoryDevice.
type MemoryDevice struct {
	Handle       uint16
	Locator      string
	BankLocator  string
	SizeMB       int // 0 means no module installed
	MemoryType   byte
	FormFactor   byte
	TypeDetail   uint16
	SpeedMTs     int
	ConfSpeedMTs int
	Manufacturer string
	Serial       string
	AssetTag     string
	PartNumber   string
	Ranks        int
	TotalWidth   int
	DataWidth    int
	Technology   byte
	// ManufacturerID is the raw JEDEC code word: continuation count in the
	// low byte, ID code in the high byte.
	ManufacturerID uint16
}

// Memory type and form factor codes used when building tables.
const (
	TypeDDR4       = 0x1A
	TypeDDR5       = 0x22
	FormDIMM       = 0x09
	DetailRDIMM    = detailRegistered | 1<<7 // registered, synchronous
	DetailLRDIMM   = detailLRDIMM | 1<<7
	DetailUDIMM    = detailUnbuffered | 1<<7
	TechnologyDRAM = 0x03
)

// AddMemoryDevice appends a type 17 structure in the SMBIOS 3.3 layout
// (92 bytes).
func (b *Builder) AddMemoryDevice(m MemoryDevice) {
	d := make([]byte, 92-4)
	le := binary.LittleEndian
	put16 := func(off int, v uint16) { le.PutUint16(d[off-4:], v) }
	put32 := func(off int, v uint32) { le.PutUint32(d[off-4:], v) }
	var strs []string
	str := func(off int, s string) {
		if s == "" {
			return
		}
		strs = append(strs, s)
		d[off-4] = byte(len(strs))
	}
	put16(0x04, 0x1000) // physical memory array handle
	put16(0x06, 0xFFFE) // no error information
	width := func(v int) uint16 {
		if v == 0 {
			return 0xFFFF
		}
		return uint16(v)
	}
	put16(0x08, width(m.TotalWidth))
	put16(0x0A, width(m.DataWidth))
	switch {
	case m.SizeMB == 0:
	case m.SizeMB >= 0x7FFF:
		put16(0x0C, 0x7FFF)
		put32(0x1C, uint32(m.SizeMB))
	default:
		put16(0x0C, uint16(m.SizeMB))
	}
	d[0x0E-4] = m.FormFactor
	str(0x10, m.Locator)
	str(0x11, m.BankLocator)
	d[0x12-4] = m.MemoryType
	put16(0x13, m.TypeDetail)
	put16(0x15, uint16(m.SpeedMTs))
	str(0x17, m.Manufacturer)
	str(0x18, m.Serial)
	str(0x19, m.AssetTag)
	str(0x1A, m.PartNumber)
	d[0x1B-4] = byte(m.Ranks & 0x0F)
	put16(0x20, uint16(m.ConfSpeedMTs))
	d[0x28-4] = m.Technology
	put16(0x2C, m.ManufacturerID)
	b.Add(17, m.Handle, d, strs...)
}

// AddSystem appends a type 1 (system information) structure.
func (b *Builder) AddSystem(handle uint16, manufacturer, product string) {
	d := make([]byte, 27-4)
	var strs []string
	if manufacturer != "" {
		strs = append(strs, manufacturer)
		d[0x04-4] = byte(len(strs))
	}
	if product != "" {
		strs = append(strs, product)
		d[0x05-4] = byte(len(strs))
	}
	b.Add(1, handle, d, strs...)
}

// AddBaseboard appends a type 2 (baseboard information) structure.
func (b *Builder) AddBaseboard(handle uint16, manufacturer, product string) {
	d := make([]byte, 15-4)
	var strs []string
	if manufacturer != "" {
		strs = append(strs, manufacturer)
		d[0x04-4] = byte(len(strs))
	}
	if product != "" {
		strs = append(strs, product)
		d[0x05-4] = byte(len(strs))
	}
	b.Add(2, handle, d, strs...)
}

// AddProcessor appends a type 4 (processor information) structure with
// its version string.
func (b *Builder) AddProcessor(handle uint16, version string) {
	d := make([]byte, 48-4)
	var strs []string
	if version != "" {
		strs = append(strs, version)
		d[0x10-4] = byte(len(strs))
	}
	b.Add(4, handle, d, strs...)
}

// Processor describes a type 4 structure for Builder.AddProcessorRecord.
type Processor struct {
	Version, Maker string
	// Family is the processor family code (0xB3 Xeon, 0x6B AMD EPYC).
	Family byte
	// EAX is the first word of the processor ID: the CPUID signature.
	EAX       uint32
	Populated bool
}

// AddProcessorRecord appends a type 4 structure with maker, family,
// signature, version and the populated bit of its status.
func (b *Builder) AddProcessorRecord(handle uint16, p Processor) {
	d := make([]byte, 48-4)
	var strs []string
	str := func(off int, s string) {
		if s == "" {
			return
		}
		strs = append(strs, s)
		d[off-4] = byte(len(strs))
	}
	d[0x05-4] = 0x03 // central processor
	d[0x06-4] = p.Family
	str(0x07, p.Maker)
	binary.LittleEndian.PutUint32(d[0x08-4:], p.EAX)
	str(0x10, p.Version)
	if p.Populated {
		d[0x18-4] = 0x41 // socket populated, CPU enabled
	}
	b.Add(4, handle, d, strs...)
}
