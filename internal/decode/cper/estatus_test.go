package cper

import (
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
)

var memRow = MemError{
	ErrorStatus: model.Some(uint64(0x400)), PhysicalAddress: model.Some(uint64(0x1a2f3c40)),
	Node: model.Some(0), Card: model.Some(2), Module: model.Some(1), Rank: model.Some(0), Device: model.Some(7),
	BankGroup: model.Some(1), Bank: model.Some(3), Row: model.Some(0x1a2f), Column: model.Some(0x238),
	ErrorType: model.Some(3), ModuleHandle: model.Some(0x1100),
}

func TestMemSectionRoundTrip(t *testing.T) {
	got, err := ParseSection(MemSection(memRow))
	if err != nil {
		t.Fatal(err)
	}
	if got != memRow {
		t.Errorf("round trip:\n got %+v\nwant %+v", got, memRow)
	}
	big := memRow
	big.Row = model.Some(0x3abcd)
	if got, _ := ParseSection(MemSection(big)); got.Row != big.Row {
		t.Errorf("row with extension: %+v", got.Row)
	}
}

// A region of several blocks: both entry revisions, a time stamp, a
// cleared block status, a section that is no memory error, and the empty
// rest of the region.
func TestParseStatusBlocks(t *testing.T) {
	var proc [16]byte
	proc[0] = 0x42
	ts := [8]byte{0x07, 0x14, 0x03, 0x01, 0x16, 0x11, 0x26, 0x20} // 2026-11-16 03:14:07, precise
	region := StatusBlockBytes(1, SevFatal, Entry{SectionType: SectionPlatformMemory, Severity: SevFatal, Data: MemSection(memRow)})
	region = append(region, StatusBlockBytes(0, SevCorrected,
		Entry{SectionType: SectionPlatformMemory, Severity: SevCorrected, V300: true, Timestamp: ts, FRUText: "DIMM_A1", Data: MemSection(memRow)},
		Entry{SectionType: proc, Severity: SevCorrected, Data: make([]byte, 40)})...)
	region = append(region, make([]byte, 200)...)
	blocks, stop := ParseStatusBlocks(region)
	if stop != "" || len(blocks) != 2 {
		t.Fatalf("blocks %d, stop %q", len(blocks), stop)
	}
	b0, b1 := blocks[0], blocks[1]
	if b0.BlockStatus != 1 || b0.Severity != SevFatal || len(b0.Entries) != 1 || !b0.Entries[0].Memory() || !b0.Entries[0].Time.IsZero() {
		t.Errorf("block 0: %+v", b0)
	}
	if b1.Offset != b0.Len || b1.BlockStatus != 0 || len(b1.Entries) != 2 || b1.Entries[1].Memory() {
		t.Errorf("block 1: %+v", b1)
	}
	e := b1.Entries[0]
	if !e.Time.Equal(time.Date(2026, 11, 16, 3, 14, 7, 0, time.UTC)) || !e.Precise || e.FRUText != "DIMM_A1" || e.Revision != 0x300 {
		t.Errorf("entry v300: %+v", e)
	}
	if m, _ := ParseSection(e.Data); m != memRow {
		t.Errorf("section of the v300 entry: %+v", m)
	}
	// The block status is not part of the bytes of a block.
	again, _ := ParseStatusBlocks(append(StatusBlockBytes(0, SevFatal, Entry{SectionType: SectionPlatformMemory, Severity: SevFatal, Data: MemSection(memRow)}), region[b0.Len:]...))
	if string(again[0].Bytes) != string(b0.Bytes) {
		t.Error("the bytes of a block change with its block status")
	}
}

// Lengths that do not fit stop the walk, with the blocks before kept.
func TestParseStatusBlocksBounds(t *testing.T) {
	good := StatusBlockBytes(1, SevFatal, Entry{SectionType: SectionPlatformMemory, Severity: SevFatal, Data: MemSection(memRow)})
	le := binary.LittleEndian
	mut := func(f func(b []byte)) []byte {
		b := append([]byte(nil), good...)
		f(b)
		return append(append([]byte(nil), good...), b...)
	}
	for _, tt := range []struct {
		name   string
		region []byte
		blocks int
		stop   string
	}{
		{"empty", nil, 0, ""},
		{"zeros", make([]byte, 64), 0, ""},
		{"header only", good[:statusLen], 0, "runs past the end"},
		{"data length too short", mut(func(b []byte) { le.PutUint32(b[12:], 10) }), 1, "shorter than one entry"},
		{"data length too long", mut(func(b []byte) { le.PutUint32(b[12:], 1<<30) }), 1, "runs past the end"},
		{"section length too long", mut(func(b []byte) { le.PutUint32(b[statusLen+24:], 1<<31) }), 1, "runs past the block"},
		{"entries short of the data length", mut(func(b []byte) { le.PutUint32(b[12:], le.Uint32(b[12:])+entryLen-1) }), 1, "runs past the end"},
		{"raw data inside the entries", mut(func(b []byte) { le.PutUint32(b[4:], 8); le.PutUint32(b[8:], 4) }), 1, "overlaps"},
		{"raw data past the end", mut(func(b []byte) { le.PutUint32(b[4:], uint32(len(good))); le.PutUint32(b[8:], 1<<20) }), 1, "runs past the end"},
		{"truncated", good[:len(good)-1], 0, "runs past the end"},
	} {
		blocks, stop := ParseStatusBlocks(tt.region)
		if len(blocks) != tt.blocks || !strings.Contains(stop, tt.stop) || (tt.stop == "" && stop != "") {
			t.Errorf("%s: %d blocks, stop %q", tt.name, len(blocks), stop)
		}
	}
}

func TestTimestamp(t *testing.T) {
	for _, tt := range []struct {
		b    [8]byte
		want time.Time
	}{
		{[8]byte{0x59, 0x59, 0x23, 0, 0x31, 0x12, 0x69, 0x19}, time.Time{}}, // before 1970
		{[8]byte{0x00, 0x00, 0x00, 0, 0x30, 0x02, 0x26, 0x20}, time.Time{}}, // 30 February
		{[8]byte{0x0a, 0x00, 0x00, 0, 0x01, 0x01, 0x26, 0x20}, time.Time{}}, // not BCD
		{[8]byte{0x00, 0x00, 0x00, 0, 0x01, 0x01, 0x26, 0x20}, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
	} {
		if got, _ := timestamp(tt.b[:]); !got.Equal(tt.want) {
			t.Errorf("%x: %s, want %s", tt.b, got, tt.want)
		}
	}
}

func FuzzParseStatusBlocks(f *testing.F) {
	f.Add(StatusBlockBytes(1, SevFatal, Entry{SectionType: SectionPlatformMemory, Severity: SevFatal, Data: MemSection(memRow)}))
	f.Add(StatusBlockBytes(0, SevCorrected, Entry{SectionType: SectionPlatformMemory, V300: true, Timestamp: [8]byte{1, 2, 3, 1, 4, 5, 6, 0x20}, Data: MemSection(memRow)}))
	f.Fuzz(func(t *testing.T, region []byte) {
		blocks, _ := ParseStatusBlocks(region)
		end := 0
		for _, b := range blocks {
			if b.Offset != end || b.Len < statusLen || b.Offset+b.Len > len(region) || len(b.Bytes) != b.Len {
				t.Fatalf("block %+v in a region of %d bytes", b, len(region))
			}
			end = b.Offset + b.Len
			for _, e := range b.Entries {
				if e.Memory() {
					ParseSection(e.Data)
				}
			}
		}
	})
}

// TestEntryLayout reads entries laid out by hand from struct
// acpi_hest_generic_data and acpi_hest_generic_data_v300
// (include/acpi/actbl1.h), not with the builder of this package: the FRU
// text at byte 44, the time stamp of revision 0x300 at byte 64, the section
// after 64 or 72 bytes. The builder once put both 8 bytes later, and the
// kernel refused such a region in the lab.
func TestEntryLayout(t *testing.T) {
	le := binary.LittleEndian
	section := MemSection(MemError{PhysicalAddress: model.Some(uint64(0x12345040))})
	entry := func(rev uint16, valid byte, stamp []byte) []byte {
		head := make([]byte, 64)
		copy(head[0:16], SectionPlatformMemory[:])
		le.PutUint32(head[16:], uint32(SevFatal))
		le.PutUint16(head[20:], rev)
		head[22] = valid
		le.PutUint32(head[24:], uint32(len(section)))
		copy(head[44:64], "CPU0_C1")
		head = append(head, stamp...)
		return append(head, section...)
	}
	stamp := []byte{0x07, 0x14, 0x03, 0x01, 0x16, 0x11, 0x26, 0x20} // 2026-11-16 03:14:07, precise
	for _, tt := range []struct {
		name  string
		entry []byte
		time  bool
	}{
		{"revision 0x201", entry(0x201, entryValidFRUText, nil), false},
		{"revision 0x300", entry(0x300, entryValidFRUText|entryValidTimestamp, stamp), true},
	} {
		block := make([]byte, statusLen)
		le.PutUint32(block[0:], 1)
		le.PutUint32(block[12:], uint32(len(tt.entry)))
		le.PutUint32(block[16:], uint32(SevFatal))
		blocks, err := ParseStatusBlocks(append(block, tt.entry...))
		if err != "" || len(blocks) != 1 || len(blocks[0].Entries) != 1 {
			t.Fatalf("%s: %v, %+v", tt.name, err, blocks)
		}
		e := blocks[0].Entries[0]
		if e.FRUText != "CPU0_C1" || len(e.Data) != len(section) || (tt.time && !e.Time.Equal(time.Date(2026, 11, 16, 3, 14, 7, 0, time.UTC))) {
			t.Errorf("%s: FRU %q, %d bytes of section, time %v", tt.name, e.FRUText, len(e.Data), e.Time)
		}
	}
}
