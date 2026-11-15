// Package ipmi decodes the part of the IPMI system event log (SEL) that
// describes memory: the 16-byte event record, the events of the Memory
// sensor type (IPMI 2.0, table 42-3, sensor type 0Ch), and the texts the
// common tools print for them.
//
// A BMC logs memory events as the BIOS or the BMC itself reports them.
// Such a record says that something happened to a module and, at best,
// which module. It carries no address, so everything it yields has the
// lowest observation grade.
package ipmi

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"github.com/agenticode/my/internal/model"
)

// Sensor type and event type codes used here.
const (
	SensorMemory            = 0x0C
	SensorEventLogging      = 0x10
	EventTypeSensorSpecific = 0x6F
)

// RecordSize is the size of one SEL record.
const RecordSize = 16

// Record types.
const (
	RecordSystemEvent = 0x02 // the standard event record
	recordOEMFirst    = 0xC0 // C0h-DFh: OEM record with a timestamp
	recordOEMNoTime   = 0xE0 // E0h-FFh: OEM record without one
)

// Timestamps below this value count seconds since the BMC started: its
// clock was not set yet. FFFFFFFFh marks a time that was not recorded.
const (
	TimeInitDone    = 0x20000000
	TimeUnspecified = 0xFFFFFFFF
)

// Record is one SEL record.
type Record struct {
	ID   uint16
	Type uint8
	// Timestamp is in seconds, as the BMC's clock had it. It is not set on
	// OEM records without a timestamp.
	Timestamp uint32
	HasTime   bool

	// The fields of a system event record (type 02h).
	GeneratorID  uint16
	EvMRev       uint8
	SensorType   uint8
	SensorNumber uint8
	EventType    uint8 // the event/reading type code, without the direction bit
	Deassert     bool
	Data         [3]byte
}

// ParseRecord decodes one 16-byte SEL record.
func ParseRecord(b []byte) (Record, error) {
	var r Record
	if len(b) != RecordSize {
		return r, fmt.Errorf("SEL record is %d bytes, want %d", len(b), RecordSize)
	}
	r.ID = binary.LittleEndian.Uint16(b[0:2])
	r.Type = b[2]
	if r.Type < recordOEMNoTime {
		r.Timestamp = binary.LittleEndian.Uint32(b[3:7])
		r.HasTime = r.Timestamp != TimeUnspecified
	}
	if r.Type != RecordSystemEvent {
		return r, nil
	}
	r.GeneratorID = binary.LittleEndian.Uint16(b[7:9])
	r.EvMRev = b[9]
	r.SensorType = b[10]
	r.SensorNumber = b[11]
	r.EventType = b[12] & 0x7F
	r.Deassert = b[12]&0x80 != 0
	copy(r.Data[:], b[13:16])
	return r, nil
}

// SystemEvent reports whether the record is a standard event record. OEM
// records have a layout only their vendor knows.
func (r Record) SystemEvent() bool { return r.Type == RecordSystemEvent }

// Offset returns the event offset: which of the states of the sensor the
// event is about (event data 1, bits 3:0).
func (r Record) Offset() int { return int(r.Data[0] & 0x0F) }

// ModuleID returns the memory module the event is about, counted relative
// to the entity of the sensor, when event data 3 holds it in the form the
// specification defines (event data 1 bits 5:4 are 11b). Many platforms
// put a code of their own into the byte instead; that is not interpreted.
func (r Record) ModuleID() model.Opt[int] {
	if r.Data[0]>>4&0x03 != 0x03 {
		return model.Opt[int]{}
	}
	return model.Some(int(r.Data[2]))
}

// Generator names who logged the record: "bios" for system firmware (the
// BIOS and its SMI handler), "bmc" for the BMC, "software" for system
// management or other software, and "" for a generator ID of zero, which
// tools print when they do not know it.
func Generator(id uint16) string {
	low := uint8(id)
	switch {
	case id == 0:
		return ""
	case low&0x01 == 0:
		// An IPMB slave address: a management controller. 20h is the BMC.
		return "bmc"
	}
	switch sw := low >> 1; {
	case sw <= 0x1F:
		return "bios" // 00h-0Fh BIOS, 10h-1Fh SMI handler
	default:
		return "software"
	}
}

// MemoryEvent is a state of the Memory sensor type, by its offset.
type MemoryEvent int

// The states of the Memory sensor type (sensor type 0Ch).
const (
	MemCorrectable    MemoryEvent = 0x00 // correctable ECC or other correctable memory error
	MemUncorrectable  MemoryEvent = 0x01 // uncorrectable ECC or other uncorrectable memory error
	MemParity         MemoryEvent = 0x02
	MemScrubFailed    MemoryEvent = 0x03 // stuck bit
	MemDeviceDisabled MemoryEvent = 0x04
	MemLogLimit       MemoryEvent = 0x05 // correctable error logging limit reached
	MemPresence       MemoryEvent = 0x06
	MemConfigError    MemoryEvent = 0x07
	MemSpare          MemoryEvent = 0x08
	MemThrottled      MemoryEvent = 0x09
	MemOvertemp       MemoryEvent = 0x0A

	// MemUnknown is an offset the specification does not define, or a text
	// that names none of the states.
	MemUnknown MemoryEvent = -1
)

var memoryNames = map[MemoryEvent]string{
	MemCorrectable:    "correctable ECC",
	MemUncorrectable:  "uncorrectable ECC",
	MemParity:         "parity error",
	MemScrubFailed:    "memory scrub failed (stuck bit)",
	MemDeviceDisabled: "memory device disabled",
	MemLogLimit:       "correctable ECC logging limit reached",
	MemPresence:       "presence detected",
	MemConfigError:    "configuration error",
	MemSpare:          "spare",
	MemThrottled:      "memory automatically throttled",
	MemOvertemp:       "critical overtemperature",
}

// String returns the name of the state.
func (m MemoryEvent) String() string {
	if n, ok := memoryNames[m]; ok {
		return n
	}
	return "unknown memory event"
}

// MemoryEventOf returns the state a memory sensor record reports, or
// MemUnknown for a record that is not a sensor-specific event of a memory
// sensor or has an offset the specification does not define.
func MemoryEventOf(r Record) MemoryEvent {
	if !r.SystemEvent() || r.SensorType != SensorMemory || r.EventType != EventTypeSensorSpecific {
		return MemUnknown
	}
	m := MemoryEvent(r.Offset())
	if _, ok := memoryNames[m]; !ok {
		return MemUnknown
	}
	return m
}

// memoryTexts are the descriptions tools print for the states: ipmitool,
// FreeIPMI and the wording of the specification itself. Longer texts come
// first so that "uncorrectable ECC" is not read as "correctable ECC", and
// the logging limit not as a correctable error.
var memoryTexts = []struct {
	text string
	ev   MemoryEvent
}{
	{"correctable ecc / other correctable memory error logging limit reached", MemLogLimit},
	{"correctable memory error logging limit reached", MemLogLimit},
	{"correctable ecc logging limit reached", MemLogLimit},
	{"uncorrectable ecc / other uncorrectable memory error", MemUncorrectable},
	{"uncorrectable memory error", MemUncorrectable},
	{"uncorrectable ecc", MemUncorrectable},
	{"correctable ecc / other correctable memory error", MemCorrectable},
	{"correctable memory error", MemCorrectable},
	{"correctable ecc", MemCorrectable},
	{"memory scrub failed", MemScrubFailed},
	{"memory device disabled", MemDeviceDisabled},
	{"memory automatically throttled", MemThrottled},
	{"critical overtemperature", MemOvertemp},
	{"configuration error", MemConfigError},
	{"presence detected", MemPresence},
	{"throttled", MemThrottled},
	{"parity", MemParity},
	{"spare", MemSpare},
}

// MemoryEventFromText returns the state a description of a memory sensor
// event names, and the text after the name: tools append what they decoded
// from vendor bytes, such as the DIMM. A description that starts with none
// of the known names gives MemUnknown.
func MemoryEventFromText(desc string) (ev MemoryEvent, rest string) {
	d := strings.TrimSpace(desc)
	low := strings.ToLower(d)
	for _, t := range memoryTexts {
		if !strings.HasPrefix(low, t.text) {
			continue
		}
		rest = d[len(t.text):]
		// The name must end there: "Spare" is a state, "Spared" is not.
		if rest != "" && isWordByte(rest[0]) {
			continue
		}
		return t.ev, strings.TrimSpace(rest)
	}
	return MemUnknown, ""
}

func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// Class says what a memory event is to the ledger.
type Class int

const (
	// ClassNone: not an error and not an action; presence, for example.
	ClassNone Class = iota
	// ClassError: an error event.
	ClassError
	// ClassAction: the firmware did something to the module.
	ClassAction
	// ClassInfo: worth a mark on the timeline, counted in no total.
	ClassInfo
)

// Meaning is how an asserted memory event enters the ledger.
type Meaning struct {
	Class    Class
	Severity model.Severity   // of an error or an informational event
	Scrub    model.ScrubType  // set when the state itself says how the error was found
	Action   model.ActionKind // of an action
	Result   model.ActionResult
}

// Meaning returns how the state enters the ledger when it is asserted.
//
// A correctable error record stands for at least one corrected error: most
// platforms log one record when a count of errors passes a limit, not one
// per error. A parity error and an uncorrectable ECC error both left data
// uncorrected. A scrub that failed on a stuck bit found an error that
// reads still correct, so it counts as a corrected error found by the
// scrubber. A disabled device and a spare taking over are things the
// firmware did. A configuration error is kept as information: it can be a
// population rule as well as a module that failed training.
func (m MemoryEvent) Meaning() Meaning {
	switch m {
	case MemCorrectable:
		return Meaning{Class: ClassError, Severity: model.SevCE}
	case MemUncorrectable, MemParity:
		return Meaning{Class: ClassError, Severity: model.SevUE}
	case MemScrubFailed:
		return Meaning{Class: ClassError, Severity: model.SevCE, Scrub: model.ScrubPatrol}
	case MemDeviceDisabled:
		return Meaning{Class: ClassAction, Action: model.ActionDisable, Result: model.ResultOK}
	case MemSpare:
		return Meaning{Class: ClassAction, Action: model.ActionSparing, Result: model.ResultOK}
	case MemLogLimit, MemConfigError, MemThrottled, MemOvertemp:
		return Meaning{Class: ClassInfo, Severity: model.SevInformational}
	}
	return Meaning{Class: ClassNone}
}

// ParseEventData reads the event direction and data of a Redfish log entry
// of type SEL: "0xNNaabbcc", where NN is the event direction and type byte
// and aa, bb, cc are event data 1 to 3. It also accepts the six digits of
// the event data alone, as ipmitool prints them, for a sensor-specific
// event that was asserted.
func ParseEventData(s string) (eventType uint8, deassert bool, data [3]byte, err error) {
	h := strings.TrimSpace(s)
	h = strings.TrimPrefix(strings.TrimPrefix(h, "0x"), "0X")
	if len(h) != 8 && len(h) != 6 {
		return 0, false, data, errors.New("event data is neither 8 nor 6 hex digits")
	}
	var b [4]byte
	for i := 0; i < len(h)/2; i++ {
		hi, ok1 := unhex(h[2*i])
		lo, ok2 := unhex(h[2*i+1])
		if !ok1 || !ok2 {
			return 0, false, data, errors.New("event data is not hexadecimal")
		}
		b[i] = hi<<4 | lo
	}
	if len(h) == 6 {
		return EventTypeSensorSpecific, false, [3]byte{b[0], b[1], b[2]}, nil
	}
	return b[0] & 0x7F, b[0]&0x80 != 0, [3]byte{b[1], b[2], b[3]}, nil
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}
