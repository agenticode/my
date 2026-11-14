package edac

import "testing"

// The decoders take text from logs and databases that this tool does not
// control. They must return, never panic, whatever the text is.
func FuzzParseDetail(f *testing.F) {
	for _, s := range []string{
		" err_code:0x0101:0x0091  SystemAddress:0x1f8bd19c0 ProcessorSocketId:0x0 MemoryControllerId:0x1 ChannelId:0x0 DimmSlotId:0x0 DimmRankId:0x1 Row:0x1a2f Column:0x3f8 Bank:0x3 BankGroup:0x1",
		" err_code:0101:0090 socket:0 imc:1 rank:1 bg:0 ba:2 row:1000 col:10",
		" OVERFLOW recoverable err_code:0x0001:0x00c1 socket:0 imc:0 rank:0 bg:0 ba:0 row:0x2 col:0x8",
		" area:DRAM err_code:0001:0090 socket:1 ha:0 channel_mask:4 rank:2",
		"APEI location: node:0 card:1 module:0 rank:0 bank:3 row:6703 col:1016 bit_pos:5 DIMM location:NODE 1 DIMM_A1 status(0x0000000000000400): Storage error in DRAM memory",
		"APEI location: node:0 card:0 module:1 DIMM location: not present. DMI handle: 0x0012 ",
		"", "row:", "Row:0x", "socket:99999999999999999999 imc:-1",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		d := ParseDetail(s)
		for _, o := range []struct {
			name string
			v    int
			ok   bool
		}{{"socket", d.Socket.V, d.Socket.Valid}, {"row", d.Row.V, d.Row.Valid}, {"column", d.Column.V, d.Column.Valid}, {"bank", d.Bank.V, d.Bank.Valid}} {
			if o.ok && o.v < 0 {
				t.Errorf("%s = %d from %q", o.name, o.v, s)
			}
		}
		_ = ParseLayers(s).Key()
		_ = ParseLabel(s)
	})
}
