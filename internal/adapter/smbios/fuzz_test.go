package smbios

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/agenticode/my/internal/adapter"
)

// A table is firmware data. Lengths and string numbers in it cannot be
// trusted: walking it must not read out of bounds or loop.
func FuzzTables(f *testing.F) {
	f.Add(ddr5Table())
	f.Add([]byte{17, 92, 0, 0x11})
	f.Add([]byte{17, 4, 0, 0, 0, 0})
	f.Add([]byte{127, 4, 0xfe, 0xff, 0, 0})
	f.Fuzz(func(t *testing.T, table []byte) {
		var c adapter.Collected
		rep := NewTables(fstest.MapFS{"DMI": {Data: table}}, ".", "fuzz", Options{}).Collect(context.Background(), &c)
		if rep.Status == "" {
			t.Error("no status")
		}
		if len(c.Modules) > len(table)/4+1 {
			t.Errorf("%d modules from %d bytes", len(c.Modules), len(table))
		}
	})
}

// The entry point says which version of the specification the table
// follows. It is read for a remark only, and must not keep a table from
// being read whatever it holds.
func FuzzEntryPoint(f *testing.F) {
	f.Add(EntryPoint64(3, 7, 4096))
	f.Add(append([]byte("_SM_"), make([]byte, 27)...))
	f.Add([]byte("_SM3_"))
	f.Add([]byte{})
	table := ddr5Table()
	f.Fuzz(func(t *testing.T, ep []byte) {
		e, err := ParseEntryPoint(ep)
		if err == nil && (e.Bits != 32 && e.Bits != 64 || e.Major < 0 || e.Major > 255 || e.Minor < 0 || e.Minor > 255) {
			t.Errorf("entry point %+v from %x", e, ep)
		}
		var c adapter.Collected
		rep := NewTables(fstest.MapFS{"DMI": {Data: table}, "smbios_entry_point": {Data: ep}}, ".", "fuzz", Options{}).Collect(context.Background(), &c)
		if rep.Status != "ok" || len(c.Modules) != 3 || len(rep.Notes) > 2 {
			t.Errorf("the table was read with %+v, %d modules", rep, len(c.Modules))
		}
	})
}

func FuzzDmidecode(f *testing.F) {
	f.Add("Handle 0x1100, DMI type 17, 84 bytes\nMemory Device\n\tSize: 32 GB\n\tLocator: A1\n\tSerial Number: 36B2F1A0\n\n")
	f.Add("Handle 0x1100, DMI type 17, 84 bytes\n\tSize: 99999999999999999999 TB\n")
	f.Add("\tSize: 1 GB\nHandle 0xZZ, DMI type 17")
	f.Fuzz(func(t *testing.T, text string) {
		var c adapter.Collected
		rep := NewDmidecodeReader(strings.NewReader(text), "fuzz", Options{}).Collect(context.Background(), &c)
		if rep.Status == "" {
			t.Error("no status")
		}
		for _, m := range c.Modules {
			if m.DIMM.SizeBytes < 0 {
				t.Errorf("negative size from %q", text)
			}
		}
	})
}
