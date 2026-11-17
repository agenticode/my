package spd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/testutil"
)

// publicFiles maps each row of expectedPublic to its copy.
var publicFiles = map[string]string{
	"ddr4-rdimm-64g-2rx4":   "illumos/ddr4-rdimm-64g-2rx4.bin",
	"ddr4-lrdimm-128g-3ds":  "illumos/ddr4-lrdimm-128g-3ds.bin",
	"ddr4-udimm-32g-2rx8":   "illumos/ddr4-udimm-32g-2rx8.bin",
	"ddr4-sodimm-16g-2rx8":  "illumos/ddr4-sodimm-16g-2rx8.bin",
	"ddr5-rdimm-64g-2rx4":   "illumos/ddr5-rdimm-64g-2rx4.bin",
	"ddr5-rdimm-16g-1rx8":   "illumos/ddr5-rdimm-16g-1rx8.bin",
	"ddr5-udimm-16g-1rx8-a": "ubihazard/ddr5-udimm-16g-1rx8-a.bin",
	"ddr5-udimm-16g-1rx8-b": "ubihazard/ddr5-udimm-16g-1rx8-b.bin",
	"ddr3-sodimm":           "rigred/ddr3-sodimm.bin",
}

func dump(t testing.TB, name string) []byte {
	t.Helper()
	return testutil.ReadTestdata(t, "spd", filepath.FromSlash(publicFiles[name]))
}

// answers reads a tab-separated answer table into rows of column -> value.
func answers(t *testing.T, text string) []map[string]string {
	t.Helper()
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	cols := strings.Split(lines[0], "\t")
	var out []map[string]string
	for _, l := range lines[1:] {
		f := strings.Split(l, "\t")
		if len(f) != len(cols) {
			t.Fatalf("row %q has %d fields, the head %d", f[0], len(f), len(cols))
		}
		r := map[string]string{}
		for i, c := range cols {
			r[c] = f[i]
		}
		out = append(out, r)
	}
	return out
}

func optS[T any](o model.Opt[T]) string {
	if !o.Valid {
		return "-"
	}
	return fmt.Sprint(o.V)
}

func str(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func crcText(d Decoded, i int) string {
	if i >= len(d.CRCs) {
		return "-"
	}
	c := d.CRCs[i]
	switch c.State {
	case CRCGood:
		return "ok"
	case CRCMissing:
		return "not in the dump"
	}
	return fmt.Sprintf("bad (stored 0x%04X, computed 0x%04X)", c.Stored, c.Computed)
}

// columns gives the fields of a decode under the column names of the
// answer tables.
func columns(d Decoded) map[string]string {
	c := map[string]string{
		"bytes":            strconv.Itoa(d.Length),
		"type":             str(d.MemoryType),
		"module_type":      str(d.ModuleType),
		"spd_revision":     str(d.Revision),
		"crc_base":         crcText(d, 0),
		"crc_module":       crcText(d, 1),
		"date":             str(d.Date),
		"location":         str(d.Location),
		"serial":           str(d.Serial),
		"part":             str(d.PartNumber),
		"revision":         str(d.ModuleRevision),
		"module_maker":     str(d.ModuleMaker),
		"dram_maker":       str(d.DRAMMaker),
		"dram_stepping":    str(d.DRAMStepping),
		"dies_per_package": optS(d.DiesPerPackage),
		"package":          str(d.Package),
		"device_width":     optS(d.DeviceWidth),
		"package_ranks":    optS(d.PackageRanks),
		"logical_ranks":    optS(d.LogicalRanks),
		"rank_mix":         str(d.RankMix),
		"bus_width":        optS(d.BusWidth),
		"ecc_bits":         optS(d.ExtensionBits),
		"subchannels":      optS(d.SubChannels),
		"size_bytes":       optS(d.SizeBytes),
		"organisation":     str(d.Organisation),
		"speed_mts":        optS(d.SpeedMTs),
	}
	c["date_raw"] = "-"
	if len(d.DateBytes) == 6 {
		c["date_raw"] = d.DateBytes[2:4] + " " + d.DateBytes[4:6]
	}
	c["die_density"] = "-"
	if d.DieMbit.Valid {
		c["die_density"] = fmt.Sprintf("%d Gb", d.DieMbit.V/1024)
	}
	if d.MemoryType == "DDR5" {
		c["crc_module"] = "-"
	}
	return c
}

// compare holds a decode to one row of an answer table.
func compare(t *testing.T, name string, d Decoded, want map[string]string) {
	t.Helper()
	got := columns(d)
	var keys []string
	for k := range got {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if w, ok := want[k]; ok && got[k] != w {
			t.Errorf("%s: %s = %q, want %q", name, k, got[k], w)
		}
	}
	// The short column holds a sentence; the decode names the parts the
	// bytes did not reach.
	if short := want["short"]; short != "" {
		if (short != "-") != (len(d.Missing) > 0) {
			t.Errorf("%s: missing parts %v, want short %q", name, d.Missing, short)
		}
		if short != "-" && !strings.Contains(short, strconv.Itoa(d.Length)) {
			t.Errorf("%s: short %q does not name the length %d", name, short, d.Length)
		}
	}
}

func TestPublicDumps(t *testing.T) {
	rows := answers(t, expectedPublic)
	if len(rows) != len(publicFiles) {
		t.Fatalf("%d rows, %d files", len(rows), len(publicFiles))
	}
	for _, want := range rows {
		name := want["name"]
		b := dump(t, name)
		in, err := Parse(b)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if in.Form != FormBinary || !bytes.Equal(in.Bytes, b) {
			t.Fatalf("%s: read as %s with %d bytes", name, in.Form, len(in.Bytes))
		}
		d := Decode(in.Bytes)
		compare(t, name, d, want)
		wantState := StateOK
		if want["type"] == "DDR3" {
			wantState = StateType
		}
		if d.State != wantState {
			t.Errorf("%s: state %s, want %s", name, d.State, wantState)
		}
	}
}

// TestVerbatimHex reads the two hex files of the illumos tests and holds
// their bytes to the rows of the copies made of them.
func TestVerbatimHex(t *testing.T) {
	rows := map[string]map[string]string{}
	for _, r := range answers(t, expectedPublic) {
		rows[r["name"]] = r
	}
	for file, name := range map[string]string{
		"illumos/M386AAK40B40-CWD70.spd": "ddr4-lrdimm-128g-3ds",
		"illumos/AQD-D5V16GR48-SB.spd":   "ddr5-rdimm-16g-1rx8",
	} {
		in, err := Parse(testutil.ReadTestdata(t, "spd", filepath.FromSlash(file)))
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if in.Form != FormHex || in.Layout != LayoutBytes || in.Gap != -1 || in.Back != -1 {
			t.Fatalf("%s: form %s layout %s gap %d back %d", file, in.Form, in.Layout, in.Gap, in.Back)
		}
		if !bytes.Equal(in.Bytes, dump(t, name)) {
			t.Fatalf("%s: bytes differ from the copy %s", file, name)
		}
		compare(t, file, Decode(in.Bytes), rows[name])
	}
}

// synthetic builds the dumps of expectedSynthetic from the public copies,
// by the recipe of the row's note.
func synthetic(t testing.TB) map[string][]byte {
	d4, d5 := dump(t, "ddr4-rdimm-64g-2rx4"), dump(t, "ddr5-rdimm-64g-2rx4")
	flip := func(b []byte, off int, x byte) []byte {
		c := bytes.Clone(b)
		c[off] ^= x
		return c
	}
	return map[string][]byte{
		"ddr4-rdimm-1rx8":       Build(d4, Change{12, []byte{0x01}}),
		"ddr4-rdimm-asymmetric": Build(d4, Change{12, []byte{0x48}}),
		"ddr4-rdimm-ddp":        Build(d4, Change{6, []byte{0x91}}),
		"ddr4-bad-base-crc":     flip(d4, 4, 0x01),
		"ddr4-bad-module-crc":   flip(d4, 200, 0xFF),
		"ddr4-first-page":       d4[:256],
		"ddr4-first-half-page":  d4[:128],
		"ddr5-rdimm-3ds-4h":     Build(d5, Change{4, []byte{d5[4]&0x1F | 3<<5}}),
		"ddr5-rdimm-ddp":        Build(d5, Change{4, []byte{d5[4]&0x1F | 1<<5}}),
		"ddr5-first-page":       d5[:128],
		"ddr5-half":             d5[:512],
		"ddr5-bad-crc":          flip(d5, 234, 0x08),
		"blank-00":              make([]byte, 512),
		"blank-ff":              bytes.Repeat([]byte{0xFF}, 512),
	}
}

// TestSynthetic holds the decoder to the synthetic dumps (built from the
// public copies, seen by no second decoder).
func TestSynthetic(t *testing.T) {
	dumps := synthetic(t)
	rows := answers(t, expectedSynthetic)
	if len(rows) != 14 || len(dumps) != 14 {
		t.Fatalf("%d rows, %d dumps", len(rows), len(dumps))
	}
	states := map[string]string{
		"ddr4-bad-base-crc": StateCRC, "ddr5-bad-crc": StateCRC,
		"ddr4-first-page": StateShort, "ddr4-first-half-page": StateShort, "ddr5-first-page": StateShort, "ddr5-half": StateShort,
		"blank-00": StateBlank, "blank-ff": StateBlank,
	}
	for _, want := range rows {
		name := want["name"]
		b, ok := dumps[name]
		if !ok {
			t.Fatalf("no recipe for %s", name)
		}
		d := Decode(b)
		compare(t, name, d, want)
		ws := states[name]
		if ws == "" {
			ws = StateOK
		}
		if d.State != ws {
			t.Errorf("%s: state %s, want %s", name, d.State, ws)
		}
		if r := want["reason"]; r != "-" {
			if !d.Fill.Valid || !strings.Contains(r, fmt.Sprintf("0x%02X", d.Fill.V)) {
				t.Errorf("%s: fill %v, want %q", name, d.Fill, r)
			}
		}
	}
	if d := Decode(dumps["ddr4-bad-module-crc"]); !hasNote(d, NoteModuleCRC) || d.State != StateOK {
		t.Errorf("a bad CRC of bytes 128-253 is a note: state %s notes %v", d.State, d.Notes)
	}
	for _, n := range []string{"ddr4-rdimm-asymmetric"} {
		if d := Decode(dumps[n]); !hasNote(d, NoteAsymmetric) {
			t.Errorf("%s: notes %v", n, d.Notes)
		}
	}
	if d := Decode(dumps["ddr5-first-page"]); d.HasSerial() || !d.DeviceWidth.Valid || !d.DieMbit.Valid {
		t.Errorf("the first 128 bytes of DDR5 give width and die and no serial: %+v", d)
	}
}

func hasNote(d Decoded, n string) bool {
	for _, x := range d.Notes {
		if x == n {
			return true
		}
	}
	return false
}

// TestCRC computes every stored CRC of the public dumps (8 base CRCs and
// the 4 of DDR4 bytes 128-253) and finds each changed byte.
func TestCRC(t *testing.T) {
	if got := CRC16([]byte("123456789")); got != 0x31C3 {
		t.Fatalf("CRC16 of the check string is 0x%04X, want 0x31C3 (CRC-16/XMODEM)", got)
	}
	n := 0
	for name := range publicFiles {
		b := dump(t, name)
		if b[2] != KeyDDR4 && b[2] != KeyDDR5 {
			continue
		}
		d := Decode(b)
		for _, c := range d.CRCs {
			if c.State != CRCGood {
				t.Errorf("%s: CRC of bytes %d-%d is %s", name, c.From, c.To, c.State)
			}
			n++
			// One changed byte inside the range makes the CRC bad.
			for _, off := range []int{c.From, (c.From + c.To) / 2, c.To} {
				x := bytes.Clone(b)
				x[off] ^= 0x10
				if got := crcOf(x, c.From, c.To, c.At); got.State != CRCBad {
					t.Errorf("%s: byte %d changed, CRC %d-%d still %s", name, off, c.From, c.To, got.State)
				}
			}
			// A changed byte of the stored value too.
			x := bytes.Clone(b)
			x[c.At] ^= 0x01
			if got := crcOf(x, c.From, c.To, c.At); got.State != CRCBad {
				t.Errorf("%s: stored CRC changed, still %s", name, got.State)
			}
		}
	}
	if n != 12 {
		t.Errorf("%d CRCs in the public dumps, want 12", n)
	}
}

func TestDate(t *testing.T) {
	cases := []struct {
		y, w byte
		want string
	}{
		{0x21, 0x43, "2021-W43"},
		{0x22, 0x01, "2022-W01"},
		{0x09, 0x53, "2009-W53"},
		{0x23, 0x00, ""}, // week 0
		{0x23, 0x54, ""}, // week 54
		{0xDA, 0xAD, ""}, // no BCD digits
		{0x29, 0x1D, ""}, // week no BCD: decode-dimms reads it as binary, this decoder does not
		{0x1A, 0x10, ""}, // year no BCD
		{0x00, 0x00, ""}, // nothing written
		{0xFF, 0xFF, ""}, // erased
		{0x99, 0x52, "2099-W52"},
	}
	for _, c := range cases {
		if got := DateCode(c.y, c.w); got != c.want {
			t.Errorf("DateCode(0x%02X, 0x%02X) = %q, want %q", c.y, c.w, got, c.want)
		}
	}
	b := Build(dump(t, "ddr4-rdimm-64g-2rx4"), Change{323, []byte{0x23, 0x54}})
	if d := Decode(b); d.Date != "" || d.DateBytes != "0x2354" || !hasNote(d, NoteNoDate) || d.State != StateOK {
		t.Errorf("week 54: date %q bytes %q notes %v state %s", d.Date, d.DateBytes, d.Notes, d.State)
	}
}

func TestSerialPlaceholders(t *testing.T) {
	for _, s := range []string{"00000000", "FFFFFFFF"} {
		v, _ := hex.DecodeString(s)
		b := Build(dump(t, "ddr5-rdimm-64g-2rx4"), Change{517, v})
		d := Decode(b)
		if d.Serial != s {
			t.Errorf("serial bytes %s read as %q", s, d.Serial)
		}
		if model.UsableSerial(d.Serial) {
			t.Errorf("serial %s counts as a serial number", s)
		}
	}
	if !model.UsableSerial(Decode(dump(t, "ddr5-rdimm-64g-2rx4")).Serial) {
		t.Error("the serial of a public copy does not count")
	}
}

func TestPartNumber(t *testing.T) {
	base := dump(t, "ddr4-rdimm-64g-2rx4")
	part := func(p []byte) Decoded {
		field := bytes.Repeat([]byte{' '}, 20)
		copy(field, p)
		return Decode(Build(base, Change{329, field}))
	}
	if d := part([]byte("ABC\x00DEF")); d.PartNumber != "ABC" || hasNote(d, NotePartNotASCII) {
		t.Errorf("a NUL ends the part number: %q %v", d.PartNumber, d.Notes)
	}
	if d := part([]byte("ABC\x7FDEF")); d.PartNumber != "" || !hasNote(d, NotePartNotASCII) {
		t.Errorf("0x7F: %q %v", d.PartNumber, d.Notes)
	}
	if d := part([]byte("AB\xC3\xA9")); d.PartNumber != "" || !hasNote(d, NotePartNotASCII) {
		t.Errorf("bytes above 0x7E: %q %v", d.PartNumber, d.Notes)
	}
	if d := part([]byte("  M393A4K40DB3  ")); d.PartNumber != "M393A4K40DB3" {
		t.Errorf("padding: %q", d.PartNumber)
	}
}

func TestModuleTypes(t *testing.T) {
	d4, d5 := dump(t, "ddr4-rdimm-64g-2rx4"), dump(t, "ddr5-rdimm-64g-2rx4")
	want4 := map[int]string{1: "RDIMM", 2: "UDIMM", 3: "SODIMM", 4: "LRDIMM", 5: "Mini-RDIMM", 6: "Mini-UDIMM",
		8: "72b-SO-RDIMM", 9: "72b-SO-UDIMM", 12: "16b-SO-DIMM", 13: "32b-SO-DIMM"}
	want5 := map[int]string{1: "RDIMM", 2: "UDIMM", 3: "SODIMM", 4: "LRDIMM", 5: "CUDIMM", 6: "CSODIMM", 7: "MRDIMM",
		8: "CAMM2", 9: "SOCAMM2", 10: "DDIMM", 11: "solder down"}
	for code := 0; code < 16; code++ {
		for _, c := range []struct {
			base []byte
			want map[int]string
		}{{d4, want4}, {d5, want5}} {
			w, ok := c.want[code]
			if !ok {
				w = fmt.Sprintf("type 0x%X", code)
			}
			d := Decode(Build(c.base, Change{3, []byte{byte(code)}}))
			if d.ModuleType != w || d.Hybrid {
				t.Errorf("%s code %d: %q hybrid %v, want %q", d.MemoryType, code, d.ModuleType, d.Hybrid, w)
			}
			if d := Decode(Build(c.base, Change{3, []byte{byte(code) | 0x80}})); !d.Hybrid {
				t.Errorf("%s code %d with bit 7: not hybrid", d.MemoryType, code)
			}
		}
	}
	if len(ModuleTypes("DDR4")) != len(want4) || len(ModuleTypes("DDR5")) != len(want5) {
		t.Error("ModuleTypes differs from the codes")
	}
}

func TestKeyBytes(t *testing.T) {
	base := dump(t, "ddr4-rdimm-64g-2rx4")
	named := 0
	for k := 0; k < 256; k++ {
		b := bytes.Clone(base)
		b[2] = byte(k)
		d := Decode(b)
		name := MemoryType(byte(k))
		if name != "" {
			named++
		}
		switch k {
		case KeyDDR4:
			if d.State != StateOK || d.MemoryType != "DDR4" {
				t.Errorf("key 0x%02X: %s %q", k, d.State, d.MemoryType)
			}
		case KeyDDR5:
			if d.MemoryType != "DDR5" || d.State == StateType {
				t.Errorf("key 0x%02X: %s %q", k, d.State, d.MemoryType)
			}
		default:
			if d.State != StateType || d.MemoryType != name || d.KeyByte.V != k {
				t.Errorf("key 0x%02X: state %s type %q, want type %q", k, d.State, d.MemoryType, name)
			}
		}
	}
	if named != len(KeyBytes()) || named != 20 {
		t.Errorf("%d key bytes named", named)
	}
	if MemoryType(0x0B) != "DDR3" || MemoryType(0x0D) != "" || MemoryType(0x00) != "" {
		t.Error("key byte names")
	}
}

// TestBuilder: a build without a change gives the same bytes; one with a
// change writes good CRCs.
func TestBuilder(t *testing.T) {
	for name := range publicFiles {
		b := dump(t, name)
		if got := Build(b); !bytes.Equal(got, b) {
			t.Errorf("%s: a build without a change differs", name)
		}
	}
	b := dump(t, "ddr5-rdimm-64g-2rx4")
	s, err := SetSerial(b, "4D590011")
	if err != nil {
		t.Fatal(err)
	}
	dt, err := SetDate(b, 2023, 8)
	if err != nil {
		t.Fatal(err)
	}
	d := Decode(Build(b, s, dt))
	if d.State != StateOK || d.Serial != "4D590011" || d.Date != "2023-W08" {
		t.Errorf("built: %s %q %q", d.State, d.Serial, d.Date)
	}
	if _, err := SetDate(b, 1999, 1); err == nil {
		t.Error("year 1999 written")
	}
	if _, err := SetSerial(b, "XYZ"); err == nil {
		t.Error("serial XYZ written")
	}
	if _, err := SetSerial(dump(t, "ddr3-sodimm"), "4D590011"); err == nil {
		t.Error("serial written into DDR3")
	}
	x := Build(dump(t, "ddr4-rdimm-64g-2rx4"), Change{4, []byte{0x87}}, Change{200, []byte{0}})
	if d := Decode(x); d.State != StateOK || hasNote(d, NoteModuleCRC) || d.DieMbit.V != 32768 {
		t.Errorf("both DDR4 CRCs written again: %s %v %v", d.State, d.Notes, d.DieMbit)
	}
}

// TestEveryLength decodes each length of each public copy from 0 to 2,048
// bytes without a panic, and a prefix never gets a state that claims more
// than its bytes.
func TestEveryLength(t *testing.T) {
	for name := range publicFiles {
		full := dump(t, name)
		for n := 0; n <= 2048; n++ {
			var b []byte
			if n <= len(full) {
				b = full[:n]
			} else {
				b = append(bytes.Clone(full), make([]byte, n-len(full))...)
			}
			d := Decode(b)
			if d.Length != n {
				t.Fatalf("%s[:%d]: length %d", name, n, d.Length)
			}
			if d.State == StateOK && n < len(full) && full[2] == KeyDDR5 && n < 555 {
				t.Fatalf("%s[:%d]: ok without the manufacturing block", name, n)
			}
			if d.HasSerial() && (full[2] == KeyDDR4 && n < 353 || full[2] == KeyDDR5 && n < 555) {
				t.Fatalf("%s[:%d]: a serial past the end", name, n)
			}
		}
	}
	for _, b := range [][]byte{nil, {0x23}, {0x23, 0x12}, {0, 0, 0x0C}, {0xFF, 0xFF, 0x12}} {
		_ = Decode(b)
	}
}

func TestMakers(t *testing.T) {
	rows, err := Makers()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 60 {
		t.Fatalf("%d rows, want 60", len(rows))
	}
	form := regexp.MustCompile(`^bank \d+, 0x[0-9A-F]{2}$`)
	for _, m := range rows {
		if !form.MatchString(m.Code) || m.Source != "dd il" {
			t.Errorf("row %+v", m)
		}
	}
	for code, want := range map[string]string{
		"bank 1, 0x2C":  "Micron Technology",
		"bank 1, 0xCE":  "Samsung",
		"bank 2, 0x7A":  "Apacer Technology",
		"bank 11, 0xC8": "Advantech",
		"bank 5, 0xCB":  "A-DATA Technology",
		"bank 5, 0xEF":  "Team Group",
		"bank 7, 0x32":  "Montage Technology",
	} {
		if got := MakerName(code); got != want {
			t.Errorf("%s: %q, want %q", code, got, want)
		}
	}
	if got := MakerLabel("bank 1, 0xCE"); got != "Samsung (JEDEC bank 1, 0xCE)" {
		t.Errorf("label %q", got)
	}
	if got := MakerLabel("bank 16, 0x01"); got != "bank 16, 0x01" {
		t.Errorf("an unknown code: %q", got)
	}
	if MakerLabel("") != "" {
		t.Error("an empty code has a label")
	}
	d := Decode(dump(t, "ddr4-rdimm-64g-2rx4"))
	if d.ModuleMakerName != "Micron Technology" || d.DRAMMakerName != "Micron Technology" {
		t.Errorf("names of a decode: %q %q", d.ModuleMakerName, d.DRAMMakerName)
	}
	// Bank 1, 0xAD is checked through the table: its name is written in
	// makers.tsv and nowhere else, so the test reads it from there.
	name := ""
	for _, line := range strings.Split(makersTSV, "\n") {
		if f := strings.Split(line, "\t"); len(f) == 4 && f[0] == "1" && f[1] == "0xAD" {
			name = f[2]
		}
	}
	if name == "" || MakerName("bank 1, 0xAD") != name {
		t.Errorf("bank 1, 0xAD: %q, row %q", MakerName("bank 1, 0xAD"), name)
	}
	if got := MakerLabel("bank 1, 0xAD"); got != name+" (JEDEC bank 1, 0xAD)" {
		t.Errorf("label of bank 1, 0xAD: %q", got)
	}
	// The UDIMM copies with the DRAM maker code of their source, bytes
	// 552-553 = 80 AD, which lie outside the CRC.
	for _, f := range []string{"ddr5-udimm-16g-1rx8-a", "ddr5-udimm-16g-1rx8-b"} {
		b := dump(t, f)
		b[552], b[553] = 0x80, 0xAD
		d := Decode(b)
		if d.State != StateOK || d.DRAMMaker != "bank 1, 0xAD" || d.DRAMMakerName != name {
			t.Errorf("%s with its DRAM maker code: %s %q %q", f, d.State, d.DRAMMaker, d.DRAMMakerName)
		}
	}
	for _, bad := range []string{
		"bank\tcode\tname\tsource\n1\t0xCE\tA\tdd il\n1\t0xCE\tB\tdd il\n", // twice
		"bank\tcode\tname\tsource\n1\t0x4E\tA\tdd il\n",                    // even parity
		"bank\tcode\tname\tsource\n1\t0xce\tA\tdd il\n",                    // lower case
		"bank\tcode\tname\tsource\n1\t0xCE\t\tdd il\n",                     // no name
		"bank\tcode\tname\n1\t0xCE\tA\n",                                   // head
		"bank\tcode\tname\tsource\nx\t0xCE\tA\tdd il\n",                    // bank
	} {
		if _, err := parseMakers(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

// TestTestdataHashes checks every file of the test dumps against the
// SHA-256 of testdata/spd/README.md, and that the README lists every file
// but the licence texts.
func TestTestdataHashes(t *testing.T) {
	root := testutil.Testdata(t, "spd")
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	rowRe := regexp.MustCompile("(?m)^\\| `([^`]+)` \\|.*\\| `([0-9a-f]{64})` \\|$")
	listed := map[string]string{}
	for _, m := range rowRe.FindAllStringSubmatch(string(readme), -1) {
		listed[m[1]] = m[2]
	}
	if len(listed) < 20 {
		t.Fatalf("%d files listed", len(listed))
	}
	seen := 0
	for _, dir := range []string{"illumos", "ubihazard", "rigred"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, e os.DirEntry, err error) error {
			if err != nil || e.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			if strings.Contains(filepath.Base(rel), "LICENSE") {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(b)
			want, ok := listed[rel]
			switch {
			case !ok:
				t.Errorf("%s is not listed in testdata/spd/README.md", rel)
			case hex.EncodeToString(sum[:]) != want:
				t.Errorf("%s: SHA-256 %x, the README says %s", rel, sum, want)
			}
			seen++
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if seen != len(listed) {
		t.Errorf("%d files, %d listed", seen, len(listed))
	}
}

// TestFetchedDumps decodes the dumps tools/spd-fetch.sh downloads into
// MY_SPD_DIR: each has a good CRC and a date. Skipped without the variable.
func TestFetchedDumps(t *testing.T) {
	dir := os.Getenv("MY_SPD_DIR")
	if dir == "" {
		t.Skip("MY_SPD_DIR is not set (tools/spd-fetch.sh <dir> fills one)")
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.bin"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no dumps in %s: %v", dir, err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		in, err := Parse(b)
		if err != nil {
			t.Errorf("%s: %v", filepath.Base(f), err)
			continue
		}
		d := Decode(in.Bytes)
		if d.State != StateOK || d.Date == "" || d.Organisation == "" || !d.SizeBytes.Valid {
			t.Errorf("%s: state %s date %q organisation %q size %v", filepath.Base(f), d.State, d.Date, d.Organisation, d.SizeBytes)
		}
	}
	t.Logf("%d dumps decoded", len(files))
}
