package smbios

import (
	"context"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/testutil"
)

// The host keeps the name of the first populated processor record that has
// one, the CPUID signature of that record and one populated flag per record:
// from raw tables and from the full text of dmidecode. Text of dmidecode -t
// memory has no processor record and leaves all three unknown.
func TestProcessorRecords(t *testing.T) {
	var b Builder
	b.AddSystem(0x100, "Example Systems", "E2")
	b.AddProcessorRecord(0x400, Processor{Version: "Not Specified", Maker: "Intel", Family: 0xB3})
	// Family 6, model 0x55 (85), stepping 7: ext model 5, base model 5.
	b.AddProcessorRecord(0x401, Processor{Version: "Intel(R) Xeon(R) Gold 6130 CPU @ 2.10GHz", Maker: "Intel", Family: 0xB3, EAX: 0x00050657, Populated: true})
	b.AddProcessorRecord(0x402, Processor{Version: "AMD EPYC 7302 16-Core Processor", Maker: "AMD", Family: 0x6B, EAX: 0x00830F10, Populated: true})
	b.AddMemoryDevice(MemoryDevice{Handle: 0x1100, Locator: "A1", SizeMB: 16384})
	h := hostOf(t, NewTables(fstest.MapFS{"DMI": {Data: b.Bytes()}}, ".", "t", Options{}))
	if h.Sockets != 3 || h.CPU != "Intel(R) Xeon(R) Gold 6130 CPU @ 2.10GHz" || h.CPUSignature != "6/85/7" || h.CPUPresent != "011" {
		t.Errorf("raw tables: %d sockets, cpu %q, signature %q, present %q", h.Sockets, h.CPU, h.CPUSignature, h.CPUPresent)
	}
	if p := processorOf(&Structure{Type: 4, Data: append(make([]byte, 8), 0x10, 0x0F, 0x83, 0x00, 0, 0, 0, 0)}); p.signature != "" {
		t.Errorf("a record without a family code has the signature %q", p.signature)
	}

	text := strings.Join([]string{
		"Handle 0x0400, DMI type 4, 48 bytes", "Processor Information", "\tSocket Designation: CPU1", "\tStatus: Unpopulated",
		"\tVersion: Not Specified", "",
		"Handle 0x0401, DMI type 4, 48 bytes", "Processor Information", "\tSocket Designation: CPU2",
		"\tSignature: Family 23, Model 49, Stepping 0", "\tVersion: AMD EPYC 7302 16-Core Processor", "\tStatus: Populated, Enabled", "",
		"Handle 0x1100, DMI type 17, 40 bytes", "Memory Device", "\tSize: 16 GB", "\tLocator: A1", "",
	}, "\n")
	if h := hostOf(t, NewDmidecodeReader(strings.NewReader(text), "t", Options{})); h.CPU != "AMD EPYC 7302 16-Core Processor" || h.CPUSignature != "23/49/0" || h.CPUPresent != "01" {
		t.Errorf("text, first socket empty: cpu %q, signature %q, present %q", h.CPU, h.CPUSignature, h.CPUPresent)
	}
	full := testutil.Testdata(t, "explorer", "netbox-dell-poweredge-m630", "dmidecode.txt")
	if h := hostOf(t, NewDmidecodeFile(full, Options{})); h.CPUSignature != "6/63/2" || h.CPUPresent != "11" {
		t.Errorf("full text: signature %q, present %q", h.CPUSignature, h.CPUPresent)
	}
	f, err := os.Open(testutil.Testdata(t, "smbios", "dmidecode-t17-ddr4.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if h := hostOf(t, NewDmidecodeReader(f, "t17", Options{})); h.CPU != "" || h.CPUSignature != "" || h.CPUPresent != "" {
		t.Errorf("-t memory text: cpu %q, signature %q, present %q", h.CPU, h.CPUSignature, h.CPUPresent)
	}
}

// A memory device of type Flash, ROM, EPROM, FEPROM or EEPROM is a chip of
// the board: it makes no slot, counts as no record and leaves one note.
func TestFlashDevices(t *testing.T) {
	var b Builder
	b.AddMemoryDevice(MemoryDevice{Handle: 0x1100, Locator: "DIMMA1", SizeMB: 16384, MemoryType: TypeDDR4})
	b.AddMemoryDevice(MemoryDevice{Handle: 0x1101, Locator: "BIOS", BankLocator: "ROM0", SizeMB: 16, MemoryType: 0x09})
	check := func(name string, a adapter.Adapter) {
		var c adapter.Collected
		rep := a.Collect(context.Background(), &c)
		notes := 0
		for _, n := range rep.Notes {
			if strings.Contains(n, "no memory slot") {
				notes++
			}
		}
		if len(c.Modules) != 1 || c.Modules[0].Locator != "DIMMA1" || rep.Records != 1 || notes != 1 || rep.Status != "ok" {
			t.Errorf("%s: %d modules, %d records, %d flash notes, status %s, notes %q", name, len(c.Modules), rep.Records, notes, rep.Status, rep.Notes)
		}
	}
	check("raw tables", NewTables(fstest.MapFS{"DMI": {Data: b.Bytes()}}, ".", "t", Options{}))
	text := strings.Join([]string{
		"Handle 0x1100, DMI type 17, 40 bytes", "Memory Device", "\tSize: 16 GB", "\tLocator: DIMMA1", "\tType: DDR4", "",
		"Handle 0x1101, DMI type 17, 40 bytes", "Memory Device", "\tSize: 16 MB", "\tLocator: BIOS", "\tBank Locator: ROM0", "\tType: Flash", "",
	}, "\n")
	check("text", NewDmidecodeReader(strings.NewReader(text), "t", Options{}))
}

// A text that holds its table twice gives each record once: the public
// dump of an ASUS TS100-E7-PI4 lists 4 memory devices and 1 processor
// record two times.
func TestRepeatedTable(t *testing.T) {
	var c adapter.Collected
	rep := NewDmidecodeFile(testutil.Testdata(t, "explorer", "linuxhw-ts100-e7-pi4-e88885", "dmidecode.txt"), Options{}).Collect(context.Background(), &c)
	if len(c.Modules) != 4 || rep.Records != 4 || len(c.Hosts) != 1 || c.Hosts[0].Sockets != 1 || c.Hosts[0].CPUPresent != "1" {
		t.Errorf("%d modules, %d records, hosts %+v", len(c.Modules), rep.Records, c.Hosts)
	}
}
