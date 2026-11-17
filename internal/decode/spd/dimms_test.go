package spd

import (
	"strings"
	"testing"

	"github.com/agenticode/my/internal/testutil"
)

// TestDecodeDimmsText reads what decode-dimms 4.4 printed for the four
// DDR4 copies (testdata/spd/illumos/decode-dimms-4.4.txt) and holds it to
// this package's decode of the same bytes: size, ranks, device width,
// date, serial, part number and both CRCs. Where decode-dimms reads date
// bytes that are no BCD as binary numbers, this package gives no date.
func TestDecodeDimmsText(t *testing.T) {
	text := string(testutil.ReadTestdata(t, "spd", "illumos", "decode-dimms-4.4.txt"))
	if !IsDecodeDimms(text) {
		t.Fatal("not recognised")
	}
	in, err := Parse([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if in.Form != FormDecodeDimms || len(in.Modules) != 4 {
		t.Fatalf("form %s, %d modules", in.Form, len(in.Modules))
	}
	names := []string{"ddr4-rdimm-64g-2rx4", "ddr4-lrdimm-128g-3ds", "ddr4-udimm-32g-2rx8", "ddr4-sodimm-16g-2rx8"}
	for i, m := range in.Modules {
		name := names[i]
		if m.Path != name+".hexC" || m.Cut {
			t.Errorf("module %d: path %q cut %v", i, m.Path, m.Cut)
		}
		d := Decode(dump(t, name))
		got := m.Decoded()
		if got.State != StateOK || m.MemoryType != "DDR4" {
			t.Errorf("%s: state %s type %q", name, got.State, m.MemoryType)
		}
		if got.SizeBytes != d.SizeBytes || got.PackageRanks != d.PackageRanks || got.DeviceWidth != d.DeviceWidth ||
			got.Serial != d.Serial || got.PartNumber != d.PartNumber || got.Organisation != d.Organisation || got.SpeedMTs != d.SpeedMTs {
			t.Errorf("%s: decode-dimms %+v\nthis package %+v", name, got, d)
		}
		if len(m.CRCs) != 2 || m.CRCs[0].Range != "0-125" || m.CRCs[1].Range != "128-253" || !m.CRCs[0].OK || !m.CRCs[1].OK {
			t.Errorf("%s: CRC lines %+v", name, m.CRCs)
		}
		for j, c := range m.CRCs {
			if !strings.Contains(c.Text, strings.ToUpper(hex4(d.CRCs[j].Computed))) {
				t.Errorf("%s: CRC line %q, computed 0x%04X", name, c.Text, d.CRCs[j].Computed)
			}
		}
		switch name {
		case "ddr4-udimm-32g-2rx8":
			// Date bytes DA AD: no date on either side.
			if got.Date != "" || d.Date != "" || m.Date != "0xDAAD" {
				t.Errorf("%s: date %q / %q", name, m.Date, d.Date)
			}
		case "ddr4-sodimm-16g-2rx8":
			// Date bytes 29 1D: decode-dimms prints 2041-W29, read as binary;
			// this package gives no date.
			if m.Date != "2041-W29" || d.Date != "" {
				t.Errorf("%s: date %q / %q", name, m.Date, d.Date)
			}
		default:
			if got.Date != d.Date || d.Date == "" {
				t.Errorf("%s: date %q / %q", name, got.Date, d.Date)
			}
		}
	}
	if in.Modules[0].Maker != "Micron Technology" || in.Modules[1].Maker != "Samsung" || in.Modules[0].Location != "0x06" {
		t.Errorf("makers %q %q location %q", in.Modules[0].Maker, in.Modules[1].Maker, in.Modules[0].Location)
	}
}

func hex4(v int) string {
	const digits = "0123456789ABCDEF"
	return string([]byte{digits[v>>12&15], digits[v>>8&15], digits[v>>4&15], digits[v&15]})
}

// TestDecodeDimmsCut reads a text that ends inside a module, a bad CRC
// line on two lines, and the forms that are not read.
func TestDecodeDimmsCut(t *testing.T) {
	text := string(testutil.ReadTestdata(t, "spd", "illumos", "decode-dimms-4.4.txt"))
	last := strings.Index(text, "Decoding EEPROM: ddr4-sodimm-16g-2rx8.hexC")
	cut := text[:last+strings.Index(text[last:], "---=== Memory Characteristics")]
	mods, err := ReadDecodeDimms(cut)
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 4 || !mods[3].Cut || mods[2].Cut {
		t.Fatalf("%d modules, last cut %v", len(mods), mods[len(mods)-1].Cut)
	}
	if d := mods[3].Decoded(); d.State != StateShort || d.Serial != "" || d.MemoryType != "DDR4" {
		t.Errorf("cut module: state %s serial %q type %q", d.State, d.Serial, d.MemoryType)
	}
	early, err := ReadDecodeDimms(text[:last+60])
	if err != nil || early[3].Decoded().State != StateShort {
		t.Errorf("a module cut before its memory type: %v", err)
	}
	bad := strings.Replace(text, "EEPROM CRC of bytes 0-125                        OK (0xA3FD)",
		"EEPROM CRC of bytes 0-125                        Bad\n                                                 (found 0x0000, calculated 0xA3FD)", 1)
	mods, err = ReadDecodeDimms(bad)
	if err != nil {
		t.Fatal(err)
	}
	if c := mods[0].CRCs[0]; c.OK || c.Text != "Bad (found 0x0000, calculated 0xA3FD)" || mods[0].Decoded().State != StateCRC {
		t.Errorf("bad CRC: %+v", c)
	}
	side := "Decoding EEPROM                                  /sys/a  /sys/b\n---=== SPD EEPROM Information ===---\n"
	if _, err := ReadDecodeDimms(side); err == nil {
		t.Error("side by side read")
	}
	if _, err := ReadDecodeDimms("# decode-dimms version 4.4\n\nNumber of SDRAM DIMMs detected and decoded: 0\n"); err == nil {
		t.Error("a text without a module read")
	}
	ddr3 := "Decoding EEPROM: /sys/bus/i2c/drivers/at24/0-0050\n---=== SPD EEPROM Information ===---\nFundamental Memory type                          DDR3 SDRAM\n\nNumber of SDRAM DIMMs detected and decoded: 1\n"
	mods, err = ReadDecodeDimms(ddr3)
	if err != nil || len(mods) != 1 || mods[0].Decoded().State != StateType || mods[0].Path != "/sys/bus/i2c/drivers/at24/0-0050" {
		t.Errorf("DDR3: %+v %v", mods, err)
	}
}
