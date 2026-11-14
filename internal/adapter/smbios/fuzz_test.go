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
