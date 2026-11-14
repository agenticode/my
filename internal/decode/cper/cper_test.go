package cper

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/model"
)

func some(v int) model.Opt[int] { return model.Some(v) }

// compact builds a struct cper_mem_err_compact.
func compact(valid uint64, node, card, module, bank, device, row, column, bitPos, rank, handle uint16, extended uint8) []byte {
	b := make([]byte, compactLen)
	le := binary.LittleEndian
	le.PutUint64(b[0:], valid)
	for i, v := range []uint16{node, card, module, bank, device, row, column, bitPos} {
		le.PutUint16(b[8+2*i:], v)
	}
	le.PutUint16(b[48:], rank)
	le.PutUint16(b[52:], handle)
	b[54] = extended
	return b
}

func TestParseCompact(t *testing.T) {
	t.Run("all location fields", func(t *testing.T) {
		valid := uint64(ValidNode | ValidCard | ValidModule | ValidRankNumber | ValidBankGroup | ValidBankAddress |
			ValidDevice | ValidRow | ValidRowExt | ValidColumn | ValidBitPosition | ValidModuleHandle | ValidChipID)
		// bank group 1 in the high byte, bank address 3 in the low byte;
		// row 0x1a2f with extension bits 17:16 = 2; chip ID 5 in bits 7:5.
		m, err := ParseCompact(compact(valid, 0, 1, 0, 0x0103, 7, 0x1a2f, 1016, 12, 1, 0x1100, 2|5<<5))
		if err != nil {
			t.Fatal(err)
		}
		want := MemError{Node: some(0), Card: some(1), Module: some(0), Rank: some(1), BankGroup: some(1), Bank: some(3),
			Device: some(7), Row: some(0x21a2f), Column: some(1016), BitPosition: some(12), ChipID: some(5), ModuleHandle: some(0x1100)}
		if m != want {
			t.Errorf("\n got: %+v\nwant: %+v", m, want)
		}
	})
	t.Run("only what the validation bits allow", func(t *testing.T) {
		// Firmware filled every field and marked only bank and row valid.
		m, err := ParseCompact(compact(ValidBank|ValidRow, 9, 9, 9, 3, 9, 100, 9, 9, 9, 9, 3))
		if err != nil {
			t.Fatal(err)
		}
		if want := (MemError{Bank: some(3), Row: some(100)}); m != want {
			t.Errorf("\n got: %+v\nwant: %+v", m, want)
		}
	})
	t.Run("too short", func(t *testing.T) {
		if _, err := ParseCompact(make([]byte, 54)); err == nil || !strings.Contains(err.Error(), "54 bytes") {
			t.Errorf("err = %v", err)
		}
		if _, err := ParseCompact(nil); err == nil {
			t.Error("nil input accepted")
		}
	})
}

func section(n int, valid uint64) []byte {
	b := make([]byte, n)
	le := binary.LittleEndian
	le.PutUint64(b[0:], valid)
	le.PutUint64(b[8:], 0x400)               // error status: storage error in DRAM
	le.PutUint64(b[16:], 0xb5689dc40)        // physical address
	le.PutUint64(b[24:], 0xffffffffffffffc0) // mask
	for i, v := range []uint16{0, 1, 0, 3, 7, 6703, 1016, 5} {
		le.PutUint16(b[32+2*i:], v)
	}
	b[72] = 2 // single-bit ECC
	if n >= sectionLen {
		le.PutUint16(b[74:], 1)      // rank
		le.PutUint16(b[78:], 0x1100) // module handle
	}
	return b
}

func TestParseSection(t *testing.T) {
	all := uint64(ValidErrorStatus | ValidPA | ValidPAMask | ValidNode | ValidCard | ValidModule | ValidBank | ValidDevice |
		ValidRow | ValidColumn | ValidBitPosition | ValidErrorType | ValidRankNumber | ValidModuleHandle)
	t.Run("current layout", func(t *testing.T) {
		m, err := ParseSection(section(sectionLen, all))
		if err != nil {
			t.Fatal(err)
		}
		want := MemError{
			ErrorStatus: model.Some(uint64(0x400)), PhysicalAddress: model.Some(uint64(0xb5689dc40)),
			PhysicalAddressMask: model.Some(uint64(0xffffffffffffffc0)),
			Node:                some(0), Card: some(1), Module: some(0), Rank: some(1), Bank: some(3), Device: some(7),
			Row: some(6703), Column: some(1016), BitPosition: some(5), ErrorType: some(2), ModuleHandle: some(0x1100),
		}
		if m != want {
			t.Errorf("\n got: %+v\nwant: %+v", m, want)
		}
	})
	t.Run("UEFI 2.1 layout ignores validation bits for fields it does not have", func(t *testing.T) {
		m, err := ParseSection(section(sectionOldLen, all))
		if err != nil {
			t.Fatal(err)
		}
		if m.Rank.Valid || m.ModuleHandle.Valid {
			t.Errorf("rank %v and handle %v come from beyond the short structure", m.Rank, m.ModuleHandle)
		}
		if m.Row != some(6703) || m.ErrorType != some(2) || !m.PhysicalAddress.Valid {
			t.Errorf("fields inside the short structure were lost: %+v", m)
		}
	})
	t.Run("too short", func(t *testing.T) {
		if _, err := ParseSection(make([]byte, 72)); err == nil {
			t.Error("72-byte section accepted")
		}
	})
}

func TestParseText(t *testing.T) {
	want := MemError{Node: some(0), Card: some(1), Module: some(0), Rank: some(1), Bank: some(3), Device: some(7),
		Row: some(6703), Column: some(1016), BitPosition: some(5)}
	tests := []struct {
		name string
		text string
		want MemError
	}{
		{"6.x: one line, no space", "node:0 card:1 module:0 rank:1 bank:3 device:7 row:6703 column:1016 bit_position:5 ", want},
		{"4.x and 5.x: one line, space after colon", "node: 0 card: 1 module: 0 rank: 1 bank: 3 device: 7 row: 6703 column: 1016 bit_position: 5 ", want},
		{"3.x: one field per line", "node: 0\ncard: 1\nmodule: 0\nrank: 1\nbank: 3\ndevice: 7\nrow: 6703\ncolumn: 1016\nbit_position: 5", want},
		{
			"full block of a 6.x kernel",
			"section_type: memory error\n error_status: Storage error in DRAM memory (0x0000000000000400)\nphysical_address: 0x0000000b5689dc40\n" +
				"physical_address_mask: 0xffffffffffffffc0\nnode:0 card:1 module:0 rank:1 bank_group:1 bank_address:3 device:7 row:6703 column:1016 bit_position:5 chip_id:2 \n" +
				"error_type: 2, single-bit ECC\nDIMM location: CPU1 DIMM_B1 ",
			MemError{ErrorStatus: model.Some(uint64(0x400)), PhysicalAddress: model.Some(uint64(0xb5689dc40)),
				PhysicalAddressMask: model.Some(uint64(0xffffffffffffffc0)),
				Node:                some(0), Card: some(1), Module: some(0), Rank: some(1), BankGroup: some(1), Bank: some(3), Device: some(7),
				Row: some(6703), Column: some(1016), BitPosition: some(5), ChipID: some(2), ErrorType: some(2),
				DIMMLocation: "CPU1 DIMM_B1"},
		},
		{
			"older block: status as a bare number, DMI handle only",
			"error_status: 0x0000000000000400\nphysical_address: 0x00000000b5689d000\nnode: 1 module: 2 \nerror_type: 3, multi-bit ECC\n" +
				"DIMM location: not present. DMI handle: 0x001a ",
			MemError{ErrorStatus: model.Some(uint64(0x400)), PhysicalAddress: model.Some(uint64(0xb5689d000)),
				Node: some(1), Module: some(2), ErrorType: some(3), ModuleHandle: some(0x1a)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseText(tt.text)
			if !ok {
				t.Fatal("no location field found")
			}
			if got != tt.want {
				t.Errorf("\n got: %+v\nwant: %+v", got, tt.want)
			}
		})
	}
	if _, ok := ParseText("section_type: PCIe error\nport_type: 4, root port"); ok {
		t.Error("a non-memory section yielded location fields")
	}
	// A negative or non-numeric value is not a location.
	if m, _ := ParseText("node:x card:-1 row:12"); m.Node.Valid || m.Card.Valid || m.Row != some(12) {
		t.Errorf("malformed values: %+v", m)
	}
}

func TestErrorTypesAndSeverity(t *testing.T) {
	if got := ErrorTypeString(2); got != "single-bit ECC" {
		t.Errorf("type 2 = %q", got)
	}
	if got := ErrorTypeString(99); got != "reserved (99)" {
		t.Errorf("type 99 = %q", got)
	}
	// ghes_edac and the kernel print the same type with different capitals.
	for _, name := range []string{"Single-bit ECC", "single-bit ECC", " SINGLE-BIT ECC "} {
		if code, ok := ErrorTypeCode(name); !ok || code != 2 {
			t.Errorf("ErrorTypeCode(%q) = %d, %v", name, code, ok)
		}
	}
	if _, ok := ErrorTypeCode("memory read error"); ok {
		t.Error("an EDAC driver message is not a CPER error type")
	}
	if ScrubType(some(13)) != model.ScrubPatrol || ScrubType(some(14)) != model.ScrubPatrol || ScrubType(some(2)) != "" || ScrubType(model.Opt[int]{}) != "" {
		t.Error("scrub type from error type")
	}
	for code, want := range map[int]model.Severity{0: model.SevRecoverable, 1: model.SevFatal, 2: model.SevCE, 3: model.SevInformational} {
		if got, ok := Severity(code); !ok || got != want {
			t.Errorf("Severity(%d) = %q, %v; want %q", code, got, ok, want)
		}
	}
	if _, ok := Severity(7); ok {
		t.Error("severity 7 accepted")
	}
	for text, want := range map[string]model.Severity{"corrected": model.SevCE, "recoverable": model.SevRecoverable, "fatal": model.SevFatal, "info": model.SevInformational} {
		if got, ok := SeverityFromText(text); !ok || got != want {
			t.Errorf("SeverityFromText(%q) = %q, %v", text, got, ok)
		}
	}
	if _, ok := SeverityFromText("warning"); ok {
		t.Error("unknown severity word accepted")
	}
}

func TestApply(t *testing.T) {
	m := MemError{Node: some(0), Card: some(1), Module: some(0), Rank: some(1), BankGroup: some(1), Bank: some(3), Device: some(7),
		Row: some(6703), Column: some(1016), BitPosition: some(5), ErrorType: some(13), ModuleHandle: some(0x1100),
		PhysicalAddress: model.Some(uint64(0xb5689dc40)), DIMMLocation: "CPU1 DIMM_B1"}
	var e model.Event
	m.Apply(&e)
	if e.SocketId != some(0) || e.ChannelId != some(1) || e.ChannelSlotId != some(0) || e.Rank != some(1) || e.DeviceId != some(7) ||
		e.BankGroup != some(1) || e.Bank != some(3) || e.Row != some(6703) || e.Column != some(1016) {
		t.Errorf("location: %+v", e)
	}
	if e.ErrorVector != "bit:5" || e.SMBIOSHandle != some(0x1100) || e.Label != "CPU1 DIMM_B1" ||
		e.OsPhysicalAddress != model.Some(uint64(0xb5689dc40)) || e.ErrorType != "scrub corrected error" || e.ErrorScrubType != model.ScrubPatrol {
		t.Errorf("other fields: %+v", e)
	}
	if e.ComputeGrade() != model.GradeA {
		t.Errorf("grade = %s, want A", e.ComputeGrade())
	}
	// A record with an address and no location is grade B.
	var b model.Event
	(&MemError{PhysicalAddress: model.Some(uint64(0x1000)), Node: some(0)}).Apply(&b)
	if b.ComputeGrade() != model.GradeB {
		t.Errorf("grade = %s, want B", b.ComputeGrade())
	}
}
