package cper

import "encoding/binary"

// MemSection builds a platform memory error section (struct
// cper_sec_mem_err, 80 bytes) with the fields of m that are set. It is the
// inverse of ParseSection, for tests and for the error regions the lab
// makes by hand.
func MemSection(m MemError) []byte {
	b := make([]byte, sectionLen)
	le := binary.LittleEndian
	var valid uint64
	put16 := func(off int, bit uint64, v int) {
		le.PutUint16(b[off:], uint16(v))
		valid |= bit
	}
	if m.ErrorStatus.Valid {
		le.PutUint64(b[8:], m.ErrorStatus.V)
		valid |= ValidErrorStatus
	}
	if m.PhysicalAddress.Valid {
		le.PutUint64(b[16:], m.PhysicalAddress.V)
		valid |= ValidPA
	}
	if m.PhysicalAddressMask.Valid {
		le.PutUint64(b[24:], m.PhysicalAddressMask.V)
		valid |= ValidPAMask
	}
	if m.Node.Valid {
		put16(32, ValidNode, m.Node.V)
	}
	if m.Card.Valid {
		put16(34, ValidCard, m.Card.V)
	}
	if m.Module.Valid {
		put16(36, ValidModule, m.Module.V)
	}
	bank := 0
	if m.Bank.Valid {
		bank = m.Bank.V
		if m.BankGroup.Valid {
			valid |= ValidBankAddress
		} else {
			valid |= ValidBank
		}
	}
	if m.BankGroup.Valid {
		bank |= m.BankGroup.V << 8
		valid |= ValidBankGroup
	}
	le.PutUint16(b[38:], uint16(bank))
	if m.Device.Valid {
		put16(40, ValidDevice, m.Device.V)
	}
	if m.Row.Valid {
		put16(42, ValidRow, m.Row.V&0xffff)
		if m.Row.V > 0xffff {
			b[73] |= byte(m.Row.V>>16) & 0x3
			valid |= ValidRowExt
		}
	}
	if m.Column.Valid {
		put16(44, ValidColumn, m.Column.V)
	}
	if m.BitPosition.Valid {
		put16(46, ValidBitPosition, m.BitPosition.V)
	}
	if m.ErrorType.Valid {
		b[72] = byte(m.ErrorType.V)
		valid |= ValidErrorType
	}
	if m.Rank.Valid {
		put16(74, ValidRankNumber, m.Rank.V)
	}
	if m.ModuleHandle.Valid {
		put16(78, ValidModuleHandle, m.ModuleHandle.V)
	}
	le.PutUint64(b[0:], valid)
	return b
}

// Entry is a Generic Error Data Entry to build.
type Entry struct {
	SectionType [16]byte
	Severity    int
	// V300 gives the entry revision 0x300 with the time stamp field.
	V300 bool
	// Timestamp is BCD-coded as the entry holds it; zero leaves it out.
	Timestamp [8]byte
	FRUText   string
	Data      []byte
}

// StatusBlockBytes builds a Generic Error Status Block with its entries
// and no raw data.
func StatusBlockBytes(blockStatus uint32, severity int, entries ...Entry) []byte {
	le := binary.LittleEndian
	var data []byte
	for _, e := range entries {
		head := make([]byte, entryLen)
		rev := uint16(0x201)
		if e.V300 {
			head = make([]byte, entryV300Len)
			rev = 0x300
		}
		copy(head[0:16], e.SectionType[:])
		le.PutUint32(head[16:], uint32(e.Severity))
		le.PutUint16(head[20:], rev)
		var valid byte
		if e.FRUText != "" {
			copy(head[44:64], e.FRUText)
			valid |= entryValidFRUText
		}
		if e.V300 && e.Timestamp != [8]byte{} {
			copy(head[64:72], e.Timestamp[:])
			valid |= entryValidTimestamp
		}
		head[22] = valid
		le.PutUint32(head[24:], uint32(len(e.Data)))
		data = append(data, head...)
		data = append(data, e.Data...)
	}
	b := make([]byte, statusLen, statusLen+len(data))
	le.PutUint32(b[0:], blockStatus)
	le.PutUint32(b[12:], uint32(len(data)))
	le.PutUint32(b[16:], uint32(severity))
	return append(b, data...)
}
