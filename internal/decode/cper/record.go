package cper

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// A whole CPER record (UEFI specification, appendix N): a header, the
// section descriptors, the sections. A Redfish log entry can carry one in
// DiagnosticData; only its memory sections are of use here.

const (
	recordHeaderLen = 128
	descriptorLen   = 72
	maxSections     = 64
)

// platformMemoryGUID is the section type of the platform memory error
// section, A5BC1114-6F64-4EDE-B863-3E83ED7C83B1, in the byte order of the
// record.
var platformMemoryGUID = [16]byte{0x14, 0x11, 0xbc, 0xa5, 0x64, 0x6f, 0xde, 0x4e, 0xb8, 0x63, 0x3e, 0x83, 0xed, 0x7c, 0x83, 0xb1}

// RecordSection is one section of a CPER record.
type RecordSection struct {
	// Memory is set when the section is a platform memory error section.
	Memory bool
	// Severity is the section's severity code (0 recoverable, 1 fatal, 2
	// corrected, 3 informational).
	Severity int
	Body     []byte
}

// ParseRecord splits a CPER record into its sections. The sections are
// taken as they lie in the record; a descriptor that points outside it is
// an error.
func ParseRecord(b []byte) ([]RecordSection, error) {
	le := binary.LittleEndian
	if len(b) < recordHeaderLen || string(b[0:4]) != "CPER" {
		return nil, errors.New("not a CPER record: no CPER signature")
	}
	if le.Uint32(b[6:]) != 0xFFFFFFFF {
		return nil, errors.New("not a CPER record: the end of the signature is wrong")
	}
	count := int(le.Uint16(b[10:]))
	length := le.Uint32(b[20:])
	if uint64(length) > uint64(len(b)) {
		return nil, fmt.Errorf("the record says it is %d bytes long and holds %d", length, len(b))
	}
	if count > maxSections {
		return nil, fmt.Errorf("the record names %d sections, more than %d", count, maxSections)
	}
	if recordHeaderLen+count*descriptorLen > int(length) {
		return nil, fmt.Errorf("%d section descriptors do not fit in %d bytes", count, length)
	}
	out := make([]RecordSection, 0, count)
	for i := range count {
		d := b[recordHeaderLen+i*descriptorLen:]
		off, n := le.Uint32(d[0:]), le.Uint32(d[4:])
		if uint64(off)+uint64(n) > uint64(length) {
			return nil, fmt.Errorf("section %d lies outside the record", i)
		}
		var guid [16]byte
		copy(guid[:], d[16:32])
		out = append(out, RecordSection{Memory: guid == platformMemoryGUID, Severity: int(le.Uint32(d[48:])), Body: b[off : off+n]})
	}
	return out, nil
}

// IsMemorySectionType reports whether a section type GUID in its text form
// is the platform memory error section.
func IsMemorySectionType(guid string) bool {
	switch guid {
	case "A5BC1114-6F64-4EDE-B863-3E83ED7C83B1", "a5bc1114-6f64-4ede-b863-3e83ed7c83b1":
		return true
	}
	return false
}
