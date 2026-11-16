package cper

import (
	"encoding/binary"
	"fmt"
	"time"
)

// Generic Error Status Blocks, the frame firmware puts error records in
// for the operating system: one after the other in the Boot Error Region
// of the BERT table, one per error source in the HEST. The layouts are
// those of the ACPI specification ("Generic Error Status Block", "Generic
// Error Data Entry") as Linux declares them in include/acpi/actbl1.h:
//
//	struct acpi_hest_generic_status      20 bytes: block_status,
//	    raw_data_offset, raw_data_length, data_length, error_severity
//	struct acpi_hest_generic_data        64 bytes: section_type (GUID,
//	    0-15), error_severity (16), revision (20), validation_bits (22),
//	    flags (23), error_data_length (24), fru_id (28-43), fru_text
//	    (44-63)
//	struct acpi_hest_generic_data_v300   72 bytes: the same and a
//	    time_stamp (64-71), for a revision of 0x300 and above
//	    (include/acpi/ghes.h, acpi_hest_get_version: revision >> 8;
//	    acpi_hest_get_size)
//
// The lab showed the kernel taking a region of this layout and refusing
// one with a header 8 bytes longer ("Invalid error record",
// cper_estatus_check in drivers/firmware/efi/cper.c).

const (
	statusLen    = 20
	entryLen     = 64
	entryV300Len = 72
)

// Validation bits of a Generic Error Data Entry (actbl1.h,
// ACPI_HEST_GEN_VALID_*).
const (
	entryValidFRUID     = 1
	entryValidFRUText   = 1 << 1
	entryValidTimestamp = 1 << 2
)

// SectionPlatformMemory is the section type of a platform memory error,
// CPER_SEC_PLATFORM_MEM (include/linux/cper.h), as it lies in memory: a
// GUID whose first three fields are little endian.
var SectionPlatformMemory = [16]byte{0x14, 0x11, 0xbc, 0xa5, 0x64, 0x6f, 0xde, 0x4e, 0xb8, 0x63, 0x3e, 0x83, 0xed, 0x7c, 0x83, 0xb1}

// StatusBlock is one Generic Error Status Block.
type StatusBlock struct {
	// Offset is where the block starts in the region.
	Offset int
	// Len is the length of the block, raw data included (cper_estatus_len
	// in drivers/acpi/apei/apei-internal.h).
	Len int
	// BlockStatus is 0 once the kernel has printed the block: bert.c,
	// bert_print_all, clears it after each record.
	BlockStatus uint32
	// Severity is the CPER severity of the block.
	Severity int
	Entries  []DataEntry
	// Bytes are the bytes of the block with the block status left out,
	// so that a block reads the same before and after the kernel cleared
	// that field.
	Bytes []byte
}

// DataEntry is one Generic Error Data Entry with its section.
type DataEntry struct {
	SectionType [16]byte
	Severity    int
	Revision    uint16
	FRUText     string
	// Time is the time stamp of a revision 0x300 entry that marks it
	// valid, else zero.
	Time time.Time
	// Precise says that the firmware marked the time stamp as precise.
	Precise bool
	// Data is the section.
	Data []byte
}

// Memory reports whether the entry is a platform memory error section.
func (e DataEntry) Memory() bool { return e.SectionType == SectionPlatformMemory }

// ParseStatusBlocks walks the Generic Error Status Blocks of an error
// region. It does not stop at a block status of zero, because the kernel
// clears it in a region it has printed; it goes on while the data length of
// a block is not zero and its entries add up to that length as
// cper_estatus_check (drivers/firmware/efi/cper.c) requires, and stops at
// the first block that does not. Every length is checked against what is
// left of the region before it is used. The second result says why the
// walk stopped before the end of the region, or is empty.
func ParseStatusBlocks(region []byte) ([]StatusBlock, string) {
	le := binary.LittleEndian
	var out []StatusBlock
	off := 0
	for len(region)-off >= statusLen {
		b := region[off:]
		status := le.Uint32(b[0:])
		rawOff, rawLen := le.Uint32(b[4:]), le.Uint32(b[8:])
		dataLen := le.Uint32(b[12:])
		sev := le.Uint32(b[16:])
		if dataLen == 0 {
			// The end of the records: the rest of the region is empty.
			return out, ""
		}
		// cper_estatus_check_header.
		if dataLen < entryLen {
			return out, fmt.Sprintf("block at offset %d: data length %d is shorter than one entry", off, dataLen)
		}
		if uint64(dataLen) > uint64(len(b)-statusLen) {
			return out, fmt.Sprintf("block at offset %d: data length %d runs past the end of the region", off, dataLen)
		}
		blockLen := uint64(statusLen) + uint64(dataLen)
		if rawLen != 0 {
			if uint64(rawOff) < uint64(statusLen)+uint64(dataLen) {
				return out, fmt.Sprintf("block at offset %d: raw data at %d overlaps the entries", off, rawOff)
			}
			blockLen = uint64(rawOff) + uint64(rawLen)
		}
		if blockLen > uint64(len(b)) {
			return out, fmt.Sprintf("block at offset %d: length %d runs past the end of the region", off, blockLen)
		}
		entries, err := parseEntries(b[statusLen : statusLen+dataLen])
		if err != "" {
			return out, fmt.Sprintf("block at offset %d: %s", off, err)
		}
		raw := make([]byte, blockLen)
		copy(raw, b[:blockLen])
		copy(raw[0:4], []byte{0, 0, 0, 0})
		out = append(out, StatusBlock{Offset: off, Len: int(blockLen), BlockStatus: status, Severity: int(sev), Entries: entries, Bytes: raw})
		off += int(blockLen)
	}
	return out, ""
}

// parseEntries splits the data of a block into its entries; they have to
// fill it exactly.
func parseEntries(data []byte) ([]DataEntry, string) {
	le := binary.LittleEndian
	var out []DataEntry
	for off := 0; off < len(data); {
		rest := data[off:]
		if len(rest) < entryLen {
			return nil, fmt.Sprintf("entry at %d: %d bytes left, an entry has %d", off, len(rest), entryLen)
		}
		var e DataEntry
		copy(e.SectionType[:], rest[0:16])
		e.Severity = int(le.Uint32(rest[16:]))
		e.Revision = le.Uint16(rest[20:])
		valid := rest[22]
		errLen := le.Uint32(rest[24:])
		head := entryLen
		if e.Revision>>8 >= 3 {
			head = entryV300Len
			if len(rest) < head {
				return nil, fmt.Sprintf("entry at %d: %d bytes left, an entry of revision %#x has %d", off, len(rest), e.Revision, head)
			}
		}
		if uint64(errLen) > uint64(len(rest)-head) {
			return nil, fmt.Sprintf("entry at %d: section length %d runs past the block", off, errLen)
		}
		if valid&entryValidFRUText != 0 {
			e.FRUText = cString(rest[44:64])
		}
		if head == entryV300Len && valid&entryValidTimestamp != 0 {
			e.Time, e.Precise = timestamp(rest[64:72])
		}
		e.Data = append([]byte(nil), rest[head:head+int(errLen)]...)
		out = append(out, e)
		off += head + int(errLen)
	}
	return out, ""
}

// timestamp decodes the time stamp of an entry: binary coded decimal
// seconds, minutes, hours, a flag byte (bit 0: precise), day, month, year
// and century, as cper_print_tstamp reads it. A time stamp that is no
// valid date gives the zero time.
func timestamp(b []byte) (time.Time, bool) {
	bcd := func(v byte) (int, bool) {
		hi, lo := int(v>>4), int(v&0x0f)
		return hi*10 + lo, hi < 10 && lo < 10
	}
	var vals [7]int
	for i, idx := range []int{0, 1, 2, 4, 5, 6, 7} {
		v, ok := bcd(b[idx])
		if !ok {
			return time.Time{}, false
		}
		vals[i] = v
	}
	sec, min, hour, day, mon, year, century := vals[0], vals[1], vals[2], vals[3], vals[4], vals[5], vals[6]
	if mon < 1 || mon > 12 || day < 1 || day > 31 || hour > 23 || min > 59 || sec > 60 {
		return time.Time{}, false
	}
	t := time.Date(century*100+year, time.Month(mon), day, hour, min, sec, 0, time.UTC)
	if t.Day() != day || t.Year() < 1970 {
		return time.Time{}, false
	}
	return t, b[3]&1 != 0
}

func cString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			b = b[:i]
			break
		}
	}
	out := make([]rune, 0, len(b))
	for _, c := range b {
		if c >= 0x20 && c < 0x7f {
			out = append(out, rune(c))
		}
	}
	return string(out)
}
