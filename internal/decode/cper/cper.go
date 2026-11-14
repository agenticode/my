// Package cper decodes the UEFI Common Platform Error Record memory error
// section, in the three forms it reaches a Linux host: the binary section,
// the compact binary copy the kernel puts in the extlog trace event, and the
// text the kernel prints for a GHES error.
package cper

import (
	"encoding/binary"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/agenticode/my/internal/model"
)

// Validation bits of the memory error section (UEFI 2.7, N.2.5).
const (
	ValidErrorStatus  = 0x0001
	ValidPA           = 0x0002
	ValidPAMask       = 0x0004
	ValidNode         = 0x0008
	ValidCard         = 0x0010
	ValidModule       = 0x0020
	ValidBank         = 0x0040
	ValidDevice       = 0x0080
	ValidRow          = 0x0100
	ValidColumn       = 0x0200
	ValidBitPosition  = 0x0400
	ValidRequestorID  = 0x0800
	ValidResponderID  = 0x1000
	ValidTargetID     = 0x2000
	ValidErrorType    = 0x4000
	ValidRankNumber   = 0x8000
	ValidCardHandle   = 0x10000
	ValidModuleHandle = 0x20000
	ValidRowExt       = 0x40000
	ValidBankGroup    = 0x80000
	ValidBankAddress  = 0x100000
	ValidChipID       = 0x200000
)

// CPER error severities.
const (
	SevRecoverable = 0
	SevFatal       = 1
	SevCorrected   = 2
	SevInfo        = 3
)

// MemError is a decoded memory error section. Only fields whose validation
// bit was set (or that the text form printed) are set.
type MemError struct {
	ErrorStatus         model.Opt[uint64]
	PhysicalAddress     model.Opt[uint64]
	PhysicalAddressMask model.Opt[uint64]
	Node                model.Opt[int]
	Card                model.Opt[int]
	Module              model.Opt[int]
	Rank                model.Opt[int]
	// Bank is the bank number, or the bank address when BankGroup is set.
	Bank        model.Opt[int]
	BankGroup   model.Opt[int]
	Device      model.Opt[int]
	Row         model.Opt[int]
	Column      model.Opt[int]
	BitPosition model.Opt[int]
	ChipID      model.Opt[int]
	// ErrorType is the memory error type code (see ErrorTypeString).
	ErrorType model.Opt[int]
	// ModuleHandle is the SMBIOS type 17 handle of the DIMM.
	ModuleHandle model.Opt[int]
	// DIMMLocation is the "bank locator, device locator" text the kernel
	// resolved the module handle to, when it printed one.
	DIMMLocation string
}

var errorTypes = []string{
	"unknown", "no error", "single-bit ECC", "multi-bit ECC", "single-symbol chipkill ECC",
	"multi-symbol chipkill ECC", "master abort", "target abort", "parity error", "watchdog timeout",
	"invalid address", "mirror broken", "memory sparing", "scrub corrected error",
	"scrub uncorrected error", "physical memory map-out event",
}

// ErrorTypeString names a memory error type code.
func ErrorTypeString(t int) string {
	if t >= 0 && t < len(errorTypes) {
		return errorTypes[t]
	}
	return fmt.Sprintf("reserved (%d)", t)
}

// ErrorTypeCode returns the code of a memory error type name as the kernel
// or EDAC prints it, compared without regard to case.
func ErrorTypeCode(name string) (int, bool) {
	n := strings.TrimSpace(name)
	for i, s := range errorTypes {
		if strings.EqualFold(n, s) {
			return i, true
		}
	}
	return 0, false
}

// ScrubType tells how an error of the given type was found, when the type
// says so: types 13 and 14 are scrub results.
func ScrubType(errorType model.Opt[int]) model.ScrubType {
	if errorType.Valid && (errorType.V == 13 || errorType.V == 14) {
		return model.ScrubPatrol
	}
	return ""
}

// Severity maps a CPER severity code to the ledger vocabulary.
func Severity(code int) (model.Severity, bool) {
	switch code {
	case SevCorrected:
		return model.SevCE, true
	case SevRecoverable:
		return model.SevRecoverable, true
	case SevFatal:
		return model.SevFatal, true
	case SevInfo:
		return model.SevInformational, true
	}
	return "", false
}

// SeverityFromText maps the severity word the kernel prints.
func SeverityFromText(s string) (model.Severity, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "corrected":
		return model.SevCE, true
	case "recoverable":
		return model.SevRecoverable, true
	case "fatal":
		return model.SevFatal, true
	case "info", "informational":
		return model.SevInformational, true
	}
	return "", false
}

const (
	compactLen    = 55 // struct cper_mem_err_compact, packed
	sectionLen    = 80 // struct cper_sec_mem_err, UEFI 2.3 and later
	sectionOldLen = 73 // UEFI 2.1 and 2.2: ends after error_type
)

// fields decodes the location members shared by both binary layouts.
type rawFields struct {
	valid                                         uint64
	node, card, module, bank, device, row, column uint16
	bitPos, rank, moduleHandle                    uint16
	extended                                      uint8
}

func (f rawFields) apply(m *MemError) {
	set := func(bit uint64, dst *model.Opt[int], v int) {
		if f.valid&bit != 0 {
			*dst = model.Some(v)
		}
	}
	set(ValidNode, &m.Node, int(f.node))
	set(ValidCard, &m.Card, int(f.card))
	set(ValidModule, &m.Module, int(f.module))
	set(ValidRankNumber, &m.Rank, int(f.rank))
	set(ValidDevice, &m.Device, int(f.device))
	set(ValidColumn, &m.Column, int(f.column))
	set(ValidBitPosition, &m.BitPosition, int(f.bitPos))
	set(ValidModuleHandle, &m.ModuleHandle, int(f.moduleHandle))
	switch {
	case f.valid&(ValidBankGroup|ValidBankAddress) != 0:
		// Bank group in the high byte, bank address in the low byte.
		set(ValidBankGroup, &m.BankGroup, int(f.bank>>8))
		set(ValidBankAddress, &m.Bank, int(f.bank&0xff))
	case f.valid&ValidBank != 0:
		m.Bank = model.Some(int(f.bank))
	}
	if f.valid&(ValidRow|ValidRowExt) != 0 {
		row := int(f.row)
		if f.valid&ValidRowExt != 0 {
			row |= int(f.extended&0x3) << 16 // row bits 17:16
		}
		m.Row = model.Some(row)
	}
	set(ValidChipID, &m.ChipID, int(f.extended>>5))
}

// ParseCompact decodes struct cper_mem_err_compact, the form the kernel
// stores in the extlog_mem_event trace event and rasdaemon keeps in the
// cper_data column. The compact form has no address, status or error type;
// rasdaemon stores those in their own columns.
func ParseCompact(b []byte) (MemError, error) {
	var m MemError
	if len(b) < compactLen {
		return m, fmt.Errorf("compact memory error record is %d bytes, want at least %d", len(b), compactLen)
	}
	le := binary.LittleEndian
	f := rawFields{
		valid:        le.Uint64(b[0:]),
		node:         le.Uint16(b[8:]),
		card:         le.Uint16(b[10:]),
		module:       le.Uint16(b[12:]),
		bank:         le.Uint16(b[14:]),
		device:       le.Uint16(b[16:]),
		row:          le.Uint16(b[18:]),
		column:       le.Uint16(b[20:]),
		bitPos:       le.Uint16(b[22:]),
		rank:         le.Uint16(b[48:]),
		moduleHandle: le.Uint16(b[52:]),
		extended:     b[54],
	}
	f.apply(&m)
	return m, nil
}

// ParseSection decodes struct cper_sec_mem_err, the memory error section of
// a CPER record. Sections from UEFI 2.1 and 2.2 firmware are shorter and end
// after the error type.
func ParseSection(b []byte) (MemError, error) {
	var m MemError
	if len(b) < sectionOldLen {
		return m, fmt.Errorf("memory error section is %d bytes, want at least %d", len(b), sectionOldLen)
	}
	le := binary.LittleEndian
	f := rawFields{
		valid:  le.Uint64(b[0:]),
		node:   le.Uint16(b[32:]),
		card:   le.Uint16(b[34:]),
		module: le.Uint16(b[36:]),
		bank:   le.Uint16(b[38:]),
		device: le.Uint16(b[40:]),
		row:    le.Uint16(b[42:]),
		column: le.Uint16(b[44:]),
		bitPos: le.Uint16(b[46:]),
	}
	if len(b) >= sectionLen {
		f.extended = b[73]
		f.rank = le.Uint16(b[74:])
		f.moduleHandle = le.Uint16(b[78:])
	} else {
		// The short layout has no fields behind the error type; validation
		// bits that claim otherwise are firmware bugs.
		f.valid &= ValidRankNumber - 1
	}
	if f.valid&ValidErrorStatus != 0 {
		m.ErrorStatus = model.Some(le.Uint64(b[8:]))
	}
	if f.valid&ValidPA != 0 {
		m.PhysicalAddress = model.Some(le.Uint64(b[16:]))
	}
	if f.valid&ValidPAMask != 0 {
		m.PhysicalAddressMask = model.Some(le.Uint64(b[24:]))
	}
	if f.valid&ValidErrorType != 0 {
		m.ErrorType = model.Some(int(b[72]))
	}
	f.apply(&m)
	return m, nil
}

// Location text. The kernel has printed the location in three ways:
//
//	node: 0                 one field per line (3.x kernels)
//	node: 0 card: 1 ...     one line, a space after each colon (4.x, 5.x)
//	node:0 card:1 ...       one line, no space (6.x), also used by ghes_edac
//
// Numbers are decimal in all of them. ghes_edac abbreviates column and
// bit_position to col and bit_pos.
var (
	locField  = regexp.MustCompile(`\b(node|card|module|rank|bank_group|bank_address|bank|device|row|column|col|bit_position|bit_pos|chip_id|chipID): ?(-?\d+)\b`)
	dmiHandle = regexp.MustCompile(`DMI handle: ?0x([0-9a-fA-F]{1,4})`)
	// The DIMM location text runs to the end of the line or to the next
	// field ghes_edac appends after it.
	dimmLoc = regexp.MustCompile(`DIMM location: ?(.*?)\s*(?:status\(|requestorID:|responderID:|targetID:|chipID:|page:0x|$)`)
	hexFld  = regexp.MustCompile(`\b(physical_address|physical_address_mask): ?0x([0-9a-fA-F]+)`)
	statFld = regexp.MustCompile(`(?:error_status: .*\(0x([0-9a-fA-F]+)\)|error_status: 0x([0-9a-fA-F]+)|status\(0x([0-9a-fA-F]+)\))`)
	typeFld = regexp.MustCompile(`\berror_type: ?(\d+)`)
)

// ParseText collects the memory error fields from kernel text: one line, or
// several lines of a GHES block joined with newlines. Fields that do not
// appear stay unset. It reports whether any location field was found.
func ParseText(s string) (MemError, bool) {
	var m MemError
	found := false
	for _, kv := range locField.FindAllStringSubmatch(s, -1) {
		n, err := strconv.Atoi(kv[2])
		if err != nil || n < 0 {
			continue
		}
		v := model.Some(n)
		found = true
		switch kv[1] {
		case "node":
			m.Node = v
		case "card":
			m.Card = v
		case "module":
			m.Module = v
		case "rank":
			m.Rank = v
		case "bank_group":
			m.BankGroup = v
		case "bank_address", "bank":
			m.Bank = v
		case "device":
			m.Device = v
		case "row":
			m.Row = v
		case "column", "col":
			m.Column = v
		case "bit_position", "bit_pos":
			m.BitPosition = v
		case "chip_id", "chipID":
			m.ChipID = v
		}
	}
	for _, kv := range hexFld.FindAllStringSubmatch(s, -1) {
		n, err := strconv.ParseUint(kv[2], 16, 64)
		if err != nil {
			continue
		}
		if kv[1] == "physical_address" {
			m.PhysicalAddress = model.Some(n)
		} else {
			m.PhysicalAddressMask = model.Some(n)
		}
	}
	if st := statFld.FindStringSubmatch(s); st != nil {
		for _, h := range st[1:] {
			if h == "" {
				continue
			}
			if n, err := strconv.ParseUint(h, 16, 64); err == nil {
				m.ErrorStatus = model.Some(n)
			}
		}
	}
	if t := typeFld.FindStringSubmatch(s); t != nil {
		if n, err := strconv.Atoi(t[1]); err == nil {
			m.ErrorType = model.Some(n)
		}
	}
	if h := dmiHandle.FindStringSubmatch(s); h != nil {
		if n, err := strconv.ParseUint(h[1], 16, 16); err == nil {
			m.ModuleHandle = model.Some(int(n))
			found = true
		}
	}
	if d := dimmLoc.FindStringSubmatch(s); d != nil {
		loc := strings.TrimSpace(d[1])
		if loc != "" && !strings.HasPrefix(loc, "not present") {
			m.DIMMLocation = loc
			found = true
		}
	}
	return m, found
}

// Apply copies the decoded location into an event. Node, card and module
// are firmware numbering; they are stored as SocketId, ChannelId and
// ChannelSlotId, which is how firmware on current server platforms uses
// them, and the event's Decoder says so.
func (m *MemError) Apply(e *model.Event) {
	copyInt := func(dst *model.Opt[int], src model.Opt[int]) {
		if src.Valid {
			*dst = src
		}
	}
	copyInt(&e.SocketId, m.Node)
	copyInt(&e.ChannelId, m.Card)
	copyInt(&e.ChannelSlotId, m.Module)
	copyInt(&e.Rank, m.Rank)
	copyInt(&e.DeviceId, m.Device)
	copyInt(&e.BankGroup, m.BankGroup)
	copyInt(&e.Bank, m.Bank)
	copyInt(&e.Row, m.Row)
	copyInt(&e.Column, m.Column)
	copyInt(&e.SMBIOSHandle, m.ModuleHandle)
	if m.PhysicalAddress.Valid {
		e.OsPhysicalAddress = m.PhysicalAddress
	}
	if m.BitPosition.Valid && e.ErrorVector == "" {
		e.ErrorVector = fmt.Sprintf("bit:%d", m.BitPosition.V)
	}
	if m.ErrorType.Valid {
		if e.ErrorType == "" {
			e.ErrorType = ErrorTypeString(m.ErrorType.V)
		}
		if e.ErrorScrubType == "" {
			e.ErrorScrubType = ScrubType(m.ErrorType)
		}
	}
	if m.DIMMLocation != "" && e.Label == "" {
		e.Label = m.DIMMLocation
	}
}
