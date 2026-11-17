package explorer

import (
	"reflect"
	"testing"
)

// WriteSlotsOf writes what the ledger keeps of a host so that the reader
// takes every hint back: system maker and product, the board string, one
// processor record per socket with name, signature and status.
func TestWriteSlotsOfHints(t *testing.T) {
	slots := []SlotText{{Locator: "A1", Populated: true, SizeMB: 16384, Type: "DDR4"}, {Locator: "BIOS", BankLocator: "ROM0", Populated: true, SizeMB: 16, Type: "Flash"}}
	for _, c := range []struct {
		name string
		host SlotHost
		cpus []cpuRecord
	}{
		{"every hint", SlotHost{Vendor: "Example Systems", Product: "E2 Server", Board: "Example Systems 0X1Y2Z", Processor: "Intel(R) Xeon(R) CPU E5-2630 v4 @ 2.20GHz",
			Signature: "6/79/1", Sockets: 2, Present: "10"},
			[]cpuRecord{{"Intel(R) Xeon(R) CPU E5-2630 v4 @ 2.20GHz", "6/79/1", 1}, {"Intel(R) Xeon(R) CPU E5-2630 v4 @ 2.20GHz", "6/79/1", 0}}},
		{"a ledger before the populated flags", SlotHost{Vendor: "Example Systems", Processor: "AMD EPYC 7302 16-Core Processor", Sockets: 1},
			[]cpuRecord{{"AMD EPYC 7302 16-Core Processor", "", -1}}},
		{"a name without a socket count", SlotHost{Processor: "AMD EPYC 7302 16-Core Processor"}, nil},
		{"nothing known", SlotHost{}, nil},
	} {
		text := WriteSlotsOf(c.host, slots)
		h := hintsOf(text)
		if h.sysVendor != c.host.Vendor || h.product != c.host.Product || h.board != c.host.Board || !reflect.DeepEqual(h.cpus, c.cpus) {
			t.Errorf("%s: read back %q %q %q %+v", c.name, h.sysVendor, h.product, h.board, h.cpus)
		}
		if c.cpus == nil && h.cpuName != c.host.Processor {
			t.Errorf("%s: the name of the processor read back %q", c.name, h.cpuName)
		}
		if h.vendor != c.host.Vendor || h.processor != c.host.Processor {
			t.Errorf("%s: the hints of the first round read %q %q", c.name, h.vendor, h.processor)
		}
		d := ParseDmidecodeMemory(text, nil, nil)
		if len(d) != 2 || d[0].Type != "DDR4" || d[1].Type != "Flash" {
			t.Errorf("%s: slots %+v", c.name, d)
		}
	}
}

// The reader takes the hints of a dump as the ledger keeps them: the
// placeholders firmware writes are no names, the board is the first board
// record that names something, and a text without processor records has
// none.
func TestHintsOfADump(t *testing.T) {
	text := "Handle 0x0001, DMI type 1, 27 bytes\nSystem Information\n\tManufacturer: To Be Filled By O.E.M.\n\tProduct Name: Example R1\n\n" +
		"Handle 0x0002, DMI type 2, 15 bytes\nBase Board Information\n\tManufacturer: Not Specified\n\tProduct Name: Not Specified\n\n" +
		"Handle 0x0003, DMI type 2, 15 bytes\nBase Board Information\n\tManufacturer: Example\n\tProduct Name: 0X1Y2Z\n\n" +
		"Handle 0x0004, DMI type 4, 48 bytes\nProcessor Information\n\tSignature: Type 0, Family 6, Model 85, Stepping 4\n\tVersion: Intel(R) Xeon(R) Gold 6130 CPU @ 2.10GHz\n\tStatus: Populated, Enabled\n\n" +
		"Handle 0x0005, DMI type 4, 48 bytes\nProcessor Information\n\tVersion: Not Specified\n\tStatus: Unpopulated\n\n"
	h := hintsOf(text)
	want := []cpuRecord{{"Intel(R) Xeon(R) Gold 6130 CPU @ 2.10GHz", "6/85/4", 1}, {"", "", 0}}
	if h.sysVendor != "" || h.product != "Example R1" || h.board != "Example 0X1Y2Z" || !reflect.DeepEqual(h.cpus, want) {
		t.Errorf("read %q %q %q %+v", h.sysVendor, h.product, h.board, h.cpus)
	}
	if h := hintsOf(slotsOf("A1", "A2")); h.cpus != nil || h.board != "" {
		t.Errorf("memory text: %+v", h)
	}
}
