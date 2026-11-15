package ipmi

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/model"
)

func record(t *testing.T, hexBytes string) Record {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(hexBytes, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	r, err := ParseRecord(b)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestParseRecord(t *testing.T) {
	// Record 0x0102, system event, 2026-11-12 03:14:07 UTC, logged by the SMI
	// handler (33h), memory sensor 53h, sensor-specific, asserted, data 30 ff 03.
	r := record(t, "0201 02 ff2ef56a 3300 04 0c 53 6f 30 ff 03")
	want := Record{ID: 0x0102, Type: RecordSystemEvent, Timestamp: 1794453247, HasTime: true, GeneratorID: 0x0033, EvMRev: 4,
		SensorType: SensorMemory, SensorNumber: 0x53, EventType: EventTypeSensorSpecific, Data: [3]byte{0x30, 0xff, 0x03}}
	if r != want {
		t.Errorf("record = %+v\nwant %+v", r, want)
	}
	if !r.SystemEvent() || r.Offset() != 0 || r.ModuleID() != model.Some(3) || MemoryEventOf(r) != MemCorrectable {
		t.Errorf("offset %d, module %v, event %v", r.Offset(), r.ModuleID(), MemoryEventOf(r))
	}

	// The direction bit.
	d := record(t, "0300 02 ff2ef56a 0100 04 0c 53 ef 34 ff 02")
	if !d.Deassert || d.EventType != EventTypeSensorSpecific || MemoryEventOf(d) != MemDeviceDisabled {
		t.Errorf("deasserted record = %+v", d)
	}

	// A time that was not recorded, and one counted from the start of the BMC.
	if u := record(t, "0400 02 ffffffff 3300 04 0c 53 6f 30 ff 03"); u.HasTime {
		t.Error("an unspecified timestamp counts as a time")
	}
	if p := record(t, "0500 02 2a000000 3300 04 0c 53 6f 30 ff 03"); !p.HasTime || p.Timestamp != 42 || p.Timestamp >= TimeInitDone {
		t.Errorf("pre-init record = %+v", p)
	}

	// OEM records: one with a timestamp, one without. Their bytes are not
	// read as event fields.
	o := record(t, "0600 c1 6130f56a 570100 0a1b2c3d4e5f")
	if o.SystemEvent() || !o.HasTime || o.SensorType != 0 || MemoryEventOf(o) != MemUnknown {
		t.Errorf("timestamped OEM record = %+v", o)
	}
	if n := record(t, "0700 e0 0102030405060708090a0b0c0d"); n.SystemEvent() || n.HasTime || n.Timestamp != 0 {
		t.Errorf("OEM record without timestamp = %+v", n)
	}

	for _, n := range []int{0, 15, 17} {
		if _, err := ParseRecord(make([]byte, n)); err == nil {
			t.Errorf("a record of %d bytes was accepted", n)
		}
	}
}

func TestModuleID(t *testing.T) {
	for data1, want := range map[byte]model.Opt[int]{
		0x30: model.Some(7), // sensor-specific extension code in byte 3
		0xF0: model.Some(7),
		0xA0: {}, // OEM code in byte 3
		0x00: {}, // byte 3 unspecified
		0x10: {}, // reserved
	} {
		r := Record{Type: RecordSystemEvent, SensorType: SensorMemory, EventType: EventTypeSensorSpecific, Data: [3]byte{data1, 0xff, 7}}
		if got := r.ModuleID(); got != want {
			t.Errorf("event data 1 %#02x: module = %v, want %v", data1, got, want)
		}
	}
}

func TestMemoryEventOf(t *testing.T) {
	base := Record{Type: RecordSystemEvent, SensorType: SensorMemory, EventType: EventTypeSensorSpecific}
	for offset := 0; offset <= 0x0A; offset++ {
		r := base
		r.Data[0] = 0xA0 | byte(offset)
		if got := MemoryEventOf(r); int(got) != offset {
			t.Errorf("offset %d decoded as %v", offset, got)
		}
		if MemoryEvent(offset).String() == "unknown memory event" {
			t.Errorf("offset %d has no name", offset)
		}
	}
	r := base
	r.Data[0] = 0x0B // an offset the specification leaves undefined
	if MemoryEventOf(r) != MemUnknown {
		t.Error("offset 0Bh was decoded")
	}
	r = base
	r.SensorType = 0x01 // a temperature sensor
	if MemoryEventOf(r) != MemUnknown {
		t.Error("a temperature record was decoded as a memory event")
	}
	r = base
	r.EventType = 0x0B // a generic event type on a memory sensor: redundancy
	if MemoryEventOf(r) != MemUnknown {
		t.Error("a generic event of a memory sensor was decoded as sensor-specific")
	}
	if MemUnknown.String() != "unknown memory event" || MemUnknown.Meaning().Class != ClassNone {
		t.Errorf("unknown event: %q, %+v", MemUnknown.String(), MemUnknown.Meaning())
	}
}

func TestMemoryEventFromText(t *testing.T) {
	for text, want := range map[string]struct {
		ev   MemoryEvent
		rest string
	}{
		// ipmitool
		"Correctable ECC":                       {MemCorrectable, ""},
		"Uncorrectable ECC":                     {MemUncorrectable, ""},
		"Parity":                                {MemParity, ""},
		"Memory Scrub Failed":                   {MemScrubFailed, ""},
		"Memory Device Disabled":                {MemDeviceDisabled, ""},
		"Correctable ECC logging limit reached": {MemLogLimit, ""},
		"Presence Detected":                     {MemPresence, ""},
		"Configuration Error":                   {MemConfigError, ""},
		"Spare":                                 {MemSpare, ""},
		"Throttled":                             {MemThrottled, ""},
		"Critical Overtemperature":              {MemOvertemp, ""},
		// ipmitool with what it decoded from vendor bytes
		"Correctable ECC (@DIMMB1(CPU1))":    {MemCorrectable, "(@DIMMB1(CPU1))"},
		"Uncorrectable ECC ( DIMMA1,DIMMA2)": {MemUncorrectable, "( DIMMA1,DIMMA2)"},
		// FreeIPMI
		"Correctable memory error":                       {MemCorrectable, ""},
		"Uncorrectable memory error":                     {MemUncorrectable, ""},
		"Correctable memory error logging limit reached": {MemLogLimit, ""},
		"Presence detected":                              {MemPresence, ""},
		"Configuration error":                            {MemConfigError, ""},
		"Memory Automatically Throttled":                 {MemThrottled, ""},
		// the wording of the specification
		"Correctable ECC / other correctable memory error":                       {MemCorrectable, ""},
		"Uncorrectable ECC / other uncorrectable memory error":                   {MemUncorrectable, ""},
		"Correctable ECC / other correctable memory error logging limit reached": {MemLogLimit, ""},
		"Memory Scrub Failed (stuck bit)":                                        {MemScrubFailed, "(stuck bit)"},
		"  correctable ecc  ":                                                    {MemCorrectable, ""},
		"UNCORRECTABLE ECC @DIMMA1":                                              {MemUncorrectable, "@DIMMA1"},
		"Spare. Memory module/device ID 2":                                       {MemSpare, ". Memory module/device ID 2"},
		// not states of the memory sensor
		"Predictive Failure Asserted": {MemUnknown, ""},
		"Redundancy Lost":             {MemUnknown, ""},
		"Spared":                      {MemUnknown, ""},
		"Parity2":                     {MemUnknown, ""},
		"":                            {MemUnknown, ""},
	} {
		ev, rest := MemoryEventFromText(text)
		if ev != want.ev || rest != want.rest {
			t.Errorf("%q = %v, %q; want %v, %q", text, ev, rest, want.ev, want.rest)
		}
	}
}

func TestMeaning(t *testing.T) {
	for ev, want := range map[MemoryEvent]Meaning{
		MemCorrectable:    {Class: ClassError, Severity: model.SevCE},
		MemUncorrectable:  {Class: ClassError, Severity: model.SevUE},
		MemParity:         {Class: ClassError, Severity: model.SevUE},
		MemScrubFailed:    {Class: ClassError, Severity: model.SevCE, Scrub: model.ScrubPatrol},
		MemDeviceDisabled: {Class: ClassAction, Action: model.ActionDisable, Result: model.ResultOK},
		MemSpare:          {Class: ClassAction, Action: model.ActionSparing, Result: model.ResultOK},
		MemLogLimit:       {Class: ClassInfo, Severity: model.SevInformational},
		MemConfigError:    {Class: ClassInfo, Severity: model.SevInformational},
		MemThrottled:      {Class: ClassInfo, Severity: model.SevInformational},
		MemOvertemp:       {Class: ClassInfo, Severity: model.SevInformational},
		MemPresence:       {Class: ClassNone},
	} {
		got := ev.Meaning()
		if got != want {
			t.Errorf("%v = %+v, want %+v", ev, got, want)
		}
		if got.Class == ClassAction && !got.Action.Valid() {
			t.Errorf("%v: action kind %q is not one the ledger knows", ev, got.Action)
		}
		if got.Class == ClassError && !got.Severity.Valid() {
			t.Errorf("%v: severity %q", ev, got.Severity)
		}
	}
}

func TestGenerator(t *testing.T) {
	for id, want := range map[uint16]string{
		0x0020: "bmc",      // the BMC's own address
		0x0001: "bios",     // software ID 00h
		0x0033: "bios",     // software ID 19h: the SMI handler
		0x003F: "bios",     // software ID 1Fh, the last of the SMI handler range
		0x0041: "software", // software ID 20h: system management software
		0x0061: "software",
		0x002C: "bmc", // another controller on the IPMB
		0x0000: "",
		0x0133: "bios", // the second byte is the channel
	} {
		if got := Generator(id); got != want {
			t.Errorf("generator %#04x = %q, want %q", id, got, want)
		}
	}
}

func TestParseEventData(t *testing.T) {
	et, deassert, data, err := ParseEventData("0x6fa0ff01")
	if err != nil || et != EventTypeSensorSpecific || deassert || data != [3]byte{0xa0, 0xff, 0x01} {
		t.Errorf("0x6fa0ff01 = %#x, %v, %x, %v", et, deassert, data, err)
	}
	et, deassert, data, err = ParseEventData("0XEF34FF02")
	if err != nil || et != EventTypeSensorSpecific || !deassert || data != [3]byte{0x34, 0xff, 0x02} {
		t.Errorf("0XEF34FF02 = %#x, %v, %x, %v", et, deassert, data, err)
	}
	// Six digits: the event data as ipmitool prints it.
	et, deassert, data, err = ParseEventData(" a1ffff ")
	if err != nil || et != EventTypeSensorSpecific || deassert || data != [3]byte{0xa1, 0xff, 0xff} {
		t.Errorf("a1ffff = %#x, %v, %x, %v", et, deassert, data, err)
	}
	for _, bad := range []string{"", "0x", "0x6fa0ff0", "0x6fa0ff0102", "0x6fa0zz01", "CPU 1 DIMM 3", "MEM0001"} {
		if _, _, _, err := ParseEventData(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func FuzzParseRecord(f *testing.F) {
	f.Add([]byte{0x01, 0x00, 0x02, 0xff, 0x2e, 0xf5, 0x6a, 0x33, 0x00, 0x04, 0x0c, 0x53, 0x6f, 0x30, 0xff, 0x03}, "Correctable ECC (@DIMMB1(CPU1))", "0x6fa0ff01")
	f.Add([]byte{0x07, 0x00, 0xe0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13}, "Spare", "a1ffff")
	f.Add([]byte{}, "", "")
	f.Fuzz(func(t *testing.T, b []byte, text, data string) {
		if r, err := ParseRecord(b); err == nil {
			ev := MemoryEventOf(r)
			_ = ev.String()
			_ = ev.Meaning()
			_ = r.ModuleID()
			_ = Generator(r.GeneratorID)
			if r.Offset() < 0 || r.Offset() > 15 {
				t.Errorf("offset %d", r.Offset())
			}
		}
		ev, rest := MemoryEventFromText(text)
		if ev == MemUnknown && rest != "" {
			t.Errorf("unknown event with rest %q", rest)
		}
		if len(rest) > len(text) {
			t.Errorf("rest %q is longer than the text %q", rest, text)
		}
		_, _, _, _ = ParseEventData(data)
	})
}
