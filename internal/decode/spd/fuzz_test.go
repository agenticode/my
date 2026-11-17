package spd

import (
	"strings"
	"testing"

	"github.com/agenticode/my/internal/testutil"
)

func seeds(f *testing.F) [][]byte {
	var out [][]byte
	for name := range publicFiles {
		out = append(out, dump(f, name))
	}
	for _, n := range []string{"M386AAK40B40-CWD70.spd", "AQD-D5V16GR48-SB.spd", "decode-dimms-4.4.txt"} {
		out = append(out, testutil.ReadTestdata(f, "spd", "illumos", n))
	}
	for _, n := range []string{"ddr4-rdimm-64g-2rx4.hexdump-C.txt", "ddr4-rdimm-64g-2rx4.hexdump.txt", "ddr5-rdimm-64g-2rx4.od.txt", "ddr5-rdimm-64g-2rx4.xxd.txt"} {
		out = append(out, testutil.ReadTestdata(f, "spd", "illumos", "forms", n))
	}
	return out
}

// FuzzParse feeds any bytes to the reader of input forms and to the
// decoder. Neither panics; an input taken for an SPD has at most 4,096
// bytes, and a decode never reaches past them.
func FuzzParse(f *testing.F) {
	for _, s := range seeds(f) {
		f.Add(s)
	}
	f.Add([]byte{0x23, 0x12, 0x0C})
	f.Add([]byte{0x1F, 0x8B, 0x08, 0x00})
	f.Fuzz(func(t *testing.T, data []byte) {
		in, err := Parse(data)
		if err == nil {
			if len(in.Bytes) > MaxBinary {
				t.Fatalf("%d bytes taken", len(in.Bytes))
			}
			for _, m := range in.Modules {
				_ = m.Decoded()
			}
		}
		d := Decode(data)
		if d.Length > MaxBinary || d.Length > len(data) {
			t.Fatalf("length %d of %d bytes", d.Length, len(data))
		}
		if d.State == "" {
			t.Fatal("no state")
		}
	})
}

// FuzzReadHex feeds any text to the hex reader. It does not panic and
// takes no more than 4,096 bytes; offsets it reports lie within them.
func FuzzReadHex(f *testing.F) {
	for _, s := range seeds(f) {
		f.Add(string(s))
	}
	f.Add("0000: 23 12\n*\n0200\n")
	f.Add("0000 00: 23 12 0c\n0000 10: 00\n")
	f.Add("*\n00000000  23 12\n*\n")
	f.Fuzz(func(t *testing.T, text string) {
		in, err := ReadHex(text)
		if err != nil {
			return
		}
		if len(in.Bytes) > MaxBinary {
			t.Fatalf("%d bytes", len(in.Bytes))
		}
		if in.Gap > len(in.Bytes) || in.Back > MaxBinary {
			t.Fatalf("gap %d back %d of %d bytes", in.Gap, in.Back, len(in.Bytes))
		}
	})
}

// FuzzDecodeDimms feeds any text to the reader of decode-dimms output. It
// does not panic, and every module it gives turns into a decode.
func FuzzDecodeDimms(f *testing.F) {
	for _, s := range seeds(f) {
		if strings.Contains(string(s), dimmsSeparator) {
			f.Add(string(s))
		}
	}
	f.Add("Decoding EEPROM: x\n---=== SPD EEPROM Information ===---\nSize   999999999999999999 GB\nRanks   99999999999\n")
	f.Fuzz(func(t *testing.T, text string) {
		mods, err := ReadDecodeDimms(text)
		if err != nil {
			return
		}
		for _, m := range mods {
			if d := m.Decoded(); d.State == "" {
				t.Fatal("no state")
			}
			if m.SizeBytes.Valid && m.SizeBytes.V < 0 {
				t.Fatalf("size %d", m.SizeBytes.V)
			}
		}
	})
}
