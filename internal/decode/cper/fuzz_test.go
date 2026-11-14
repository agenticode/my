package cper

import "testing"

func FuzzParseText(f *testing.F) {
	for _, s := range []string{
		"node:0 card:1 module:0 rank:1 bank_group:1 bank_address:3 device:7 row:6703 column:1016 bit_position:5 ",
		"node: 0\ncard: 1\nerror_type: 2, single-bit ECC\nphysical_address: 0x0000000b5689dc40",
		"error_status: Storage error in DRAM memory (0x0000000000000400)\nDIMM location: not present. DMI handle: 0x001a ",
		"DIMM location: ", "row:99999999999999999999", "physical_address: 0x", "",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		m, _ := ParseText(s)
		if m.Row.Valid && m.Row.V < 0 || m.Column.Valid && m.Column.V < 0 {
			t.Errorf("negative location from %q: %+v", s, m)
		}
	})
}

func FuzzParseBinary(f *testing.F) {
	f.Add(make([]byte, 55))
	f.Add(make([]byte, 80))
	f.Add(make([]byte, 73))
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	f.Fuzz(func(t *testing.T, b []byte) {
		if m, err := ParseCompact(b); err == nil && m.Row.Valid && m.Row.V > 0x3ffff {
			t.Errorf("row %#x exceeds 18 bits", m.Row.V)
		}
		if m, err := ParseSection(b); err == nil && m.ErrorType.Valid && m.ErrorType.V > 255 {
			t.Errorf("error type %d", m.ErrorType.V)
		}
	})
}
