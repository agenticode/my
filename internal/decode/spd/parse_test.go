package spd

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/testutil"
)

// hexC writes b as hexdump -C does: 16 bytes a row, a gap after the
// eighth, the ASCII column, "*" for repeated rows and the closing offset.
func hexC(b []byte) string {
	var s strings.Builder
	var prev []byte
	star := false
	for off := 0; off < len(b); off += 16 {
		row := b[off:min(off+16, len(b))]
		if prev != nil && bytes.Equal(row, prev) && len(row) == 16 {
			if !star {
				s.WriteString("*\n")
				star = true
			}
			continue
		}
		star = false
		fmt.Fprintf(&s, "%08x ", off)
		for i, c := range row {
			if i == 8 {
				s.WriteString(" ")
			}
			fmt.Fprintf(&s, " %02x", c)
		}
		s.WriteString("  |")
		for _, c := range row {
			if c < 0x20 || c > 0x7E {
				c = '.'
			}
			s.WriteByte(c)
		}
		s.WriteString("|\n")
		prev = row
	}
	fmt.Fprintf(&s, "%08x\n", len(b))
	return s.String()
}

// i2cdump writes b as i2cdump does for one page: a head line of column
// numbers, then rows "00: ..." with an ASCII column.
func i2cdump(b []byte) string {
	var s strings.Builder
	s.WriteString("     0  1  2  3  4  5  6  7  8  9  a  b  c  d  e  f    0123456789abcdef\n")
	for off := 0; off < len(b); off += 16 {
		fmt.Fprintf(&s, "%02x:", off&0xFF)
		row := b[off:min(off+16, len(b))]
		for _, c := range row {
			fmt.Fprintf(&s, " %02x", c)
		}
		s.WriteString("    ")
		for _, c := range row {
			if c < 0x20 || c > 0x7E {
				c = '?'
			}
			s.WriteByte(c)
		}
		s.WriteString("\n")
	}
	return s.String()
}

func mustParse(t *testing.T, what, text string) *Input {
	t.Helper()
	in, err := Parse([]byte(text))
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	return in
}

func notSPD(t *testing.T, what string, data []byte, looks string) {
	t.Helper()
	in, err := Parse(data)
	var n *NotSPD
	if !errors.As(err, &n) {
		t.Fatalf("%s: read as %+v, %v; want no SPD (%s)", what, in, err, looks)
	}
	if n.What != looks {
		t.Errorf("%s: looks like %s (%v), want %s", what, n.What, n, looks)
	}
}

// TestInputForms reads each input form back to the bytes of a public copy,
// and a text of each form that just misses.
func TestInputForms(t *testing.T) {
	d4, d5 := dump(t, "ddr4-rdimm-64g-2rx4"), dump(t, "ddr5-rdimm-64g-2rx4")
	forms := func(name string) []byte {
		return testutil.ReadTestdata(t, "spd", "illumos", "forms", name)
	}
	t.Run("tools", func(t *testing.T) {
		for _, c := range []struct {
			file   string
			base   []byte
			layout string
		}{
			{"ddr4-rdimm-64g-2rx4.hexdump-C.txt", d4, LayoutBytes},
			{"ddr4-rdimm-64g-2rx4.od.txt", d4, LayoutBytes},
			{"ddr4-rdimm-64g-2rx4.xxd.txt", d4, LayoutWords},
			{"ddr4-rdimm-64g-2rx4.hexdump.txt", d4, LayoutWordsSwapped},
			{"ddr5-rdimm-64g-2rx4.hexdump-C.txt", d5, LayoutBytes},
			{"ddr5-rdimm-64g-2rx4.od.txt", d5, LayoutBytes},
			{"ddr5-rdimm-64g-2rx4.xxd.txt", d5, LayoutWords},
			{"ddr5-rdimm-64g-2rx4.hexdump.txt", d5, LayoutWordsSwapped},
		} {
			text := forms(c.file)
			if !bytes.Contains(text, []byte("\n*\n")) && !strings.Contains(c.file, "xxd") {
				t.Fatalf("%s holds no repeated rows", c.file)
			}
			in := mustParse(t, c.file, string(text))
			if !bytes.Equal(in.Bytes, c.base) || in.Layout != c.layout || in.Gap != -1 || in.Back != -1 {
				t.Errorf("%s: %d bytes, layout %s, gap %d, back %d; want %d bytes, %s", c.file, len(in.Bytes), in.Layout, in.Gap, in.Back, len(c.base), c.layout)
			}
			// Cut before the closing line: the repeated rows at the end
			// have nothing to repeat up to, and the bytes end early.
			cut := string(text[:bytes.LastIndex(bytes.TrimRight(text, "\n"), []byte("\n"))+1])
			if in := mustParse(t, c.file+" without its last line", cut); len(in.Bytes) >= len(c.base) && strings.Contains(cut[len(cut)-3:], "*") {
				t.Errorf("%s without the closing offset: %d bytes", c.file, len(in.Bytes))
			}
		}
	})
	t.Run("hexdump -C", func(t *testing.T) {
		in := mustParse(t, "hexdump -C", hexC(d4))
		if !bytes.Equal(in.Bytes, d4) {
			t.Fatal("bytes differ")
		}
		// A row of 15 bytes in the middle: the offsets leave a byte out.
		lines := strings.Split(hexC(d4), "\n")
		lines[1] = lines[1][:strings.LastIndex(lines[1][:58], " ")] + "  |x|"
		in = mustParse(t, "a short row", strings.Join(lines, "\n"))
		if in.Gap != 31 || len(in.Bytes) != 31 {
			t.Errorf("a short row: gap %d, %d bytes", in.Gap, len(in.Bytes))
		}
		if d := Decode(in.Bytes); d.State != StateShort {
			t.Errorf("state %s", d.State)
		}
	})
	t.Run("od", func(t *testing.T) {
		text := string(forms("ddr4-rdimm-64g-2rx4.od.txt"))
		in := mustParse(t, "od", text)
		if !bytes.Equal(in.Bytes, d4) {
			t.Fatal("bytes differ")
		}
		// The closing offset sets where the last "*" ends.
		if !strings.HasSuffix(text, "*\n000200\n") {
			t.Fatalf("od text ends %q", text[len(text)-20:])
		}
		in = mustParse(t, "od with a later end", strings.TrimSuffix(text, "000200\n")+"000300\n")
		if len(in.Bytes) != 0x300 {
			t.Errorf("a closing offset of 0x300: %d bytes", len(in.Bytes))
		}
	})
	t.Run("i2cdump", func(t *testing.T) {
		in := mustParse(t, "i2cdump", i2cdump(d4[:256]))
		if !bytes.Equal(in.Bytes, d4[:256]) || in.Layout != LayoutBytes {
			t.Fatalf("i2cdump: %d bytes %s", len(in.Bytes), in.Layout)
		}
		// The ASCII column of a row could pass for bytes ("12 34"): the
		// row ends after 16 bytes.
		text := strings.Replace(i2cdump(d4[:256]), "    #", "    12 34 #", 1)
		if in := mustParse(t, "i2cdump with hex in the ASCII column", text); !bytes.Equal(in.Bytes, d4[:256]) {
			t.Error("the ASCII column was read as bytes")
		}
		if d := Decode(in.Bytes); d.State != StateShort || d.HasSerial() {
			t.Errorf("one page of DDR4: %s", d.State)
		}
	})
	t.Run("eeprog prefix", func(t *testing.T) {
		var s strings.Builder
		for off := 0; off < len(d4); off += 16 {
			fmt.Fprintf(&s, "0000 %02x:", off)
			for _, c := range d4[off : off+16] {
				fmt.Fprintf(&s, " %02x", c)
			}
			s.WriteString("\n")
		}
		in := mustParse(t, "rows with a leading 0000", s.String())
		if !bytes.Equal(in.Bytes, d4) {
			t.Errorf("%d bytes, gap %d back %d", len(in.Bytes), in.Gap, in.Back)
		}
		// Without the prefix: offsets of two and three digits with a colon.
		if in := mustParse(t, "offsets with a colon", strings.ReplaceAll(s.String(), "0000 ", "")); !bytes.Equal(in.Bytes, d4) {
			t.Errorf("offsets with a colon: %d bytes, back %d", len(in.Bytes), in.Back)
		}
		// A prefix and a row of four-digit offset and groups: the reading
		// without the prefix starts at 0 and holds more bytes.
		words := "0000 2312 0c01 8631 0008 0060 0003 080b 8000\n"
		if in, _ := ReadHex(words); in == nil || in.Layout != LayoutWords && in.Layout != LayoutWordsSwapped || len(in.Bytes) != 16 {
			t.Errorf("a row of groups after 0000: %+v", in)
		}
	})
	t.Run("words in both orders", func(t *testing.T) {
		for _, b := range [][]byte{d4, d5} {
			var file, swapped strings.Builder
			for off := 0; off < len(b); off += 16 {
				fmt.Fprintf(&file, "%08x:", off)
				fmt.Fprintf(&swapped, "%07x", off)
				for i := off; i < off+16; i += 2 {
					fmt.Fprintf(&file, " %02x%02x", b[i], b[i+1])
					fmt.Fprintf(&swapped, " %02x%02x", b[i+1], b[i])
				}
				file.WriteString("  ................\n")
				swapped.WriteString("\n")
			}
			fmt.Fprintf(&swapped, "%07x\n", len(b))
			if in := mustParse(t, "file order", file.String()); !bytes.Equal(in.Bytes, b) || in.Layout != LayoutWords {
				t.Errorf("file order: %s", in.Layout)
			}
			if in := mustParse(t, "swapped", swapped.String()); !bytes.Equal(in.Bytes, b) || in.Layout != LayoutWordsSwapped {
				t.Errorf("swapped: %s", in.Layout)
			}
		}
		// Neither CRC good: the order with a memory type at byte 2 is taken,
		// and the decode says crc.
		bad := bytes.Clone(d4)
		bad[4] ^= 1
		var s strings.Builder
		for off := 0; off < len(bad); off += 16 {
			fmt.Fprintf(&s, "%07x", off)
			for i := off; i < off+16; i += 2 {
				fmt.Fprintf(&s, " %02x%02x", bad[i+1], bad[i])
			}
			s.WriteString("\n")
		}
		in := mustParse(t, "bad CRC", s.String())
		if !bytes.Equal(in.Bytes, bad) || in.Layout != LayoutWordsSwapped || Decode(in.Bytes).State != StateCRC {
			t.Errorf("bad CRC in words: %s %s", in.Layout, Decode(in.Bytes).State)
		}
	})
	t.Run("plain", func(t *testing.T) {
		var spaced, runs strings.Builder
		for off := 0; off < len(d5); off += 16 {
			for i, c := range d5[off : off+16] {
				if i > 0 {
					spaced.WriteString(" ")
				}
				fmt.Fprintf(&spaced, "%02X", c)
			}
			spaced.WriteString("\n")
		}
		for off := 0; off < len(d5); off += 30 {
			runs.WriteString(hexString(d5[off:min(off+30, len(d5))]) + "\n")
		}
		// The first byte of the DDR5 dump, 30, could pass for an offset: a
		// two-digit offset needs a colon.
		if in := mustParse(t, "plain bytes", spaced.String()); !bytes.Equal(in.Bytes, d5) || in.Layout != LayoutPlain {
			t.Errorf("plain bytes: %d %s", len(in.Bytes), in.Layout)
		}
		if in := mustParse(t, "runs of hex digits", runs.String()); !bytes.Equal(in.Bytes, d5) || in.Layout != LayoutPlain {
			t.Errorf("runs: %d %s", len(in.Bytes), in.Layout)
		}
		// Lines of eight digits could pass for offsets alone.
		var eights strings.Builder
		for off := 0; off < len(d5); off += 4 {
			eights.WriteString(hexString(d5[off:off+4]) + "\n")
		}
		if in := mustParse(t, "runs of eight digits", eights.String()); !bytes.Equal(in.Bytes, d5) {
			t.Errorf("runs of eight digits: %d bytes", len(in.Bytes))
		}
	})
	t.Run("offset column", func(t *testing.T) {
		var s strings.Builder
		s.WriteString("   00 01 02 03 04 05 06 07 08 09 0A 0B 0C 0D 0E 0F\n")
		for off := 0; off < len(d4); off += 16 {
			fmt.Fprintf(&s, "%02X", off&0xF0)
			for _, c := range d4[off : off+16] {
				fmt.Fprintf(&s, " %02X", c)
			}
			s.WriteString("\n")
		}
		if in := mustParse(t, "a table with a two-digit offset column", s.String()); !bytes.Equal(in.Bytes, d4) {
			t.Errorf("offset column: %d bytes", len(in.Bytes))
		}
		// A table whose first column does not count is all bytes.
		x := strings.Replace(s.String(), "\n10 ", "\n11 ", 1)
		if in := mustParse(t, "a column that does not count", x); len(in.Bytes) == len(d4) {
			t.Errorf("a column that does not count was taken for offsets")
		}
	})
	t.Run("CRLF and comments", func(t *testing.T) {
		text := "# a dump\r\n" + strings.ReplaceAll(hexC(d5), "\n", "\r\n")
		if in := mustParse(t, "CRLF", text); !bytes.Equal(in.Bytes, d5) {
			t.Errorf("CRLF: %d bytes", len(in.Bytes))
		}
	})
	t.Run("gap", func(t *testing.T) {
		lines := strings.Split(string(forms("ddr4-rdimm-64g-2rx4.hexdump-C.txt")), "\n")
		var kept []string
		for _, l := range lines {
			if !strings.HasPrefix(l, "00000150") {
				kept = append(kept, l)
			}
		}
		in := mustParse(t, "a gap", strings.Join(kept, "\n"))
		if in.Gap != 0x150 || len(in.Bytes) != 0x150 {
			t.Errorf("gap %d, %d bytes", in.Gap, len(in.Bytes))
		}
		d := Decode(in.Bytes)
		if d.State != StateShort || d.HasSerial() {
			t.Errorf("state %s serial %q", d.State, d.Serial)
		}
		// The first row is not at offset 0.
		notSPD(t, "no start", []byte(strings.Join(lines[2:], "\n")), LooksHexNoStart)
	})
	t.Run("two dumps in one text", func(t *testing.T) {
		in := mustParse(t, "two dumps", hexC(d4)+hexC(d5))
		if !bytes.Equal(in.Bytes, d4) || in.Back != 0 {
			t.Errorf("%d bytes, back %d", len(in.Bytes), in.Back)
		}
	})
	t.Run("bounds", func(t *testing.T) {
		notSPD(t, "a text over the bound", []byte(strings.Repeat(hexC(d4), 80)), LooksBigText)
		big := bytes.Repeat(d4, 9)
		notSPD(t, "hex of more than 4,096 bytes", []byte(hexC(big)), LooksHexBig)
		notSPD(t, "binary of more than 4,096 bytes", big, LooksBig)
		notSPD(t, "binary of 100 bytes", d4[:100], LooksSmall)
		notSPD(t, "blank", bytes.Repeat([]byte{0xFF}, 512), LooksBlank)
		notSPD(t, "no memory type", append([]byte{0x23, 0x12, 0x0D}, make([]byte, 509)...), LooksNoType)
		notSPD(t, "hex without a memory type", []byte(hexC(append([]byte{0x23, 0x11, 0x0D}, d4[3:]...))), LooksHexNoType)
		notSPD(t, "empty", nil, LooksEmpty)
		notSPD(t, "prose", []byte("This is a note about a module.\nIt has no bytes.\n"), LooksText)
		notSPD(t, "HTML", []byte("<html><body><table>\n---=== SPD EEPROM Information ===---\n</table>"), LooksHTML)
	})
	t.Run("gzip", func(t *testing.T) {
		var z bytes.Buffer
		w := gzip.NewWriter(&z)
		w.Write([]byte(hexC(d4)))
		w.Close()
		in, err := Parse(z.Bytes())
		if err != nil || !bytes.Equal(in.Bytes, d4) || !in.Gzip {
			t.Errorf("gzip: %v", err)
		}
		notSPD(t, "a broken gzip file", []byte{0x1F, 0x8B, 0, 0}, LooksGzip)
	})
	t.Run("binary", func(t *testing.T) {
		for name := range publicFiles {
			b := dump(t, name)
			if in, err := Parse(b); err != nil || in.Form != FormBinary {
				t.Errorf("%s: %v", name, err)
			}
		}
		// A file of the illumos tests read with its file name.
		in, err := Parse(testutil.ReadTestdata(t, "spd", filepath.FromSlash("illumos/M386AAK40B40-CWD70.spd")))
		if err != nil || in.Form != FormHex {
			t.Errorf("illumos hex: %v", err)
		}
	})
}

func hexString(b []byte) string {
	var s strings.Builder
	for _, c := range b {
		fmt.Fprintf(&s, "%02x", c)
	}
	return s.String()
}
