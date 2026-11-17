package smbios

import (
	"context"
	"os"
	"testing"
	"testing/fstest"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/testutil"
)

func hostOf(t *testing.T, a adapter.Adapter) model.Host {
	t.Helper()
	var c adapter.Collected
	a.Collect(context.Background(), &c)
	if len(c.Hosts) == 0 {
		return model.Host{}
	}
	return *c.Hosts[0]
}

// The number of processor records is the number of sockets of the board:
// from raw tables, from the full text of dmidecode, and unknown from the
// text of dmidecode -t memory, which has no processor record.
func TestSocketsOfTheBoard(t *testing.T) {
	var b Builder
	b.AddSystem(0x100, "Example Systems", "E2")
	b.AddProcessor(0x400, "AMD EPYC 9254 24-Core Processor")
	b.AddProcessor(0x401, "AMD EPYC 9254 24-Core Processor")
	b.AddMemoryDevice(MemoryDevice{Handle: 0x1100, Locator: "A1", SizeMB: 16384})
	h := hostOf(t, NewTables(fstest.MapFS{"DMI": {Data: b.Bytes()}}, ".", "t", Options{}))
	if h.Sockets != 2 || h.CPU != "AMD EPYC 9254 24-Core Processor" {
		t.Errorf("raw tables: %d sockets, cpu %q", h.Sockets, h.CPU)
	}
	full := testutil.Testdata(t, "explorer", "netbox-dell-poweredge-m630", "dmidecode.txt")
	if h := hostOf(t, NewDmidecodeFile(full, Options{})); h.Sockets != 2 || h.CPU != "Intel(R) Xeon(R) CPU E5-2683 v3 @ 2.00GHz" {
		t.Errorf("full text: %d sockets, cpu %q", h.Sockets, h.CPU)
	}
	f, err := os.Open(testutil.Testdata(t, "smbios", "dmidecode-t17-ddr4.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if h := hostOf(t, NewDmidecodeReader(f, "t17", Options{})); h.Sockets != 0 || h.CPU != "" {
		t.Errorf("-t memory text: %d sockets, cpu %q", h.Sockets, h.CPU)
	}
}
