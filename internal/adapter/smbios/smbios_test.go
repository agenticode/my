package smbios

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/testutil"
)

var update = flag.Bool("update", false, "rewrite the binary SMBIOS fixtures under testdata/smbios")

var (
	ctx = context.Background()
	now = time.Date(2026, 11, 13, 5, 0, 0, 0, time.UTC)
)

func some(v int) model.Opt[int] { return model.Some(v) }

func TestDmidecodeMemoryDevices(t *testing.T) {
	path := testutil.Testdata(t, "smbios", "dmidecode-t17-ddr4.txt")
	var c adapter.Collected
	rep := NewDmidecodeFile(path, Options{Now: now}).Collect(ctx, &c)

	if rep.Status != model.SourcePartial || rep.Records != 6 || rep.Skipped != 2 || len(c.Modules) != 4 || len(c.Hosts) != 0 {
		t.Fatalf("report = %+v, %d modules; want partial, 6 records, 2 skipped, 4 modules", rep, len(c.Modules))
	}
	notes := strings.Join(rep.Notes, "\n")
	for _, want := range []string{
		"skipped handle 0x1104: memory device block has no Size field",
		`skipped handle 0x1105: Size "a lot" is unreadable`,
		"6 memory slots, 3 populated",
	} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes lack %q:\n%s", want, notes)
		}
	}

	want := []model.Module{
		{Locator: "A1", Handle: some(0x1100), Populated: true, DIMM: model.DIMM{
			Manufacturer: "00CE00B300CE", PartNumber: "M393A4K40DB3-CWE", Serial: "36B2F1A0", ManufacturerID: "bank 1, 0xCE",
			SizeBytes: 32 << 30, MemoryType: "DDR4", FormFactor: "DIMM", ModuleType: "RDIMM", Technology: "DRAM",
			Ranks: some(2), DataWidth: some(64), TotalWidth: some(72), SpeedMTs: some(3200), ConfiguredSpeedMTs: some(2933)}},
		// An older dmidecode: megabytes, MHz and "Configured Clock Speed".
		{Locator: "A2", Handle: some(0x1101), Populated: true, DIMM: model.DIMM{
			Manufacturer: "Micron Technology", PartNumber: "36ASF4G72PZ-2G9E2", Serial: "2C4A91D7",
			SizeBytes: 32 << 30, MemoryType: "DDR4", FormFactor: "DIMM", ModuleType: "RDIMM",
			Ranks: some(2), DataWidth: some(64), TotalWidth: some(72), SpeedMTs: some(2933), ConfiguredSpeedMTs: some(2933)}},
		// An empty slot: the "NO DIMM" strings are not an identity.
		{Locator: "A3", Handle: some(0x1102)},
		// Firmware placeholders in part number and serial number.
		{Locator: "B1", Handle: some(0x1103), Populated: true, DIMM: model.DIMM{
			Manufacturer: "Example Memory", Serial: "00000000",
			SizeBytes: 64 << 30, MemoryType: "DDR4", FormFactor: "DIMM", ModuleType: "LRDIMM", Technology: "DRAM",
			Ranks: some(4), DataWidth: some(64), TotalWidth: some(72), SpeedMTs: some(3200), ConfiguredSpeedMTs: some(2933)}},
	}
	for i := range want {
		w := want[i]
		w.ObservedAt, w.Source = now, Kind
		w.RawRef = path + "#handle 0x" + strings.ToUpper(strings.TrimPrefix(strings.ToLower(hex4(w.Handle.V)), "0x"))
		if got := *c.Modules[i]; !reflect.DeepEqual(got, w) {
			t.Errorf("module %d\n got: %+v\nwant: %+v", i, got, w)
		}
	}
	// The serial of B1 is a firmware placeholder and must not become an identity.
	if model.UsableSerial(c.Modules[3].DIMM.Serial) {
		t.Error("an all-zero serial number passed as usable")
	}
}

func hex4(v int) string {
	const digits = "0123456789abcdef"
	return "0x" + string([]byte{digits[v>>12&15], digits[v>>8&15], digits[v>>4&15], digits[v&15]})
}

func TestDmidecodeFullOutputOfAVirtualMachine(t *testing.T) {
	var c adapter.Collected
	rep := NewDmidecodeFile(testutil.Testdata(t, "smbios", "dmidecode-full-vm.txt"), Options{Now: now}).Collect(ctx, &c)
	if rep.Status != model.SourceOK || rep.Records != 1 || len(c.Modules) != 1 || len(c.Hosts) != 1 {
		t.Fatalf("report = %+v, %d modules, %d hosts", rep, len(c.Modules), len(c.Hosts))
	}
	m := c.Modules[0]
	if m.Locator != "DIMM 0" || !m.Populated || m.DIMM.SizeBytes != 1<<30 || m.DIMM.Manufacturer != "QEMU" || m.DIMM.Serial != "" ||
		m.DIMM.PartNumber != "" || m.DIMM.MemoryType != "RAM" || m.DIMM.Ranks.Valid || m.DIMM.SpeedMTs.Valid || m.DIMM.TotalWidth.Valid {
		t.Errorf("module: %+v", *m)
	}
	if h := c.Hosts[0]; h.SystemVendor != "Example Cloud" || h.SystemProduct != "Standard PC (Q35 + ICH9, 2009)" {
		t.Errorf("host: %+v", *h)
	}
}

func TestDmidecodeInputsWithoutMemoryDevices(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		status model.SourceStatus
		detail string
	}{
		{"empty", "", model.SourceError, "no dmidecode structures found"},
		{"not dmidecode output", "EDAC MC0: 1 CE memory read error\n", model.SourceError, "no dmidecode structures found"},
		{"run without privilege", "# dmidecode 3.5\n/sys/firmware/dmi/tables/smbios_entry_point: Permission denied\nScanning /dev/mem for entry point.\n/dev/mem: Permission denied\n",
			model.SourceUnreadable, "run it as root"},
		{"other types only", "Handle 0x0000, DMI type 0, 24 bytes\nBIOS Information\n\tVendor: Example\n\n", model.SourceEmpty, "no memory device (type 17) records"},
		{"header cut off", "Handle 0x1100, DMI type 17, 84 bytes\nMemory Device\n\tLocator: A1\n\tSize: 32", model.SourcePartial, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c adapter.Collected
			rep := NewDmidecodeReader(strings.NewReader(tt.in), "stdin", Options{Now: now}).Collect(ctx, &c)
			if rep.Status != tt.status || !strings.Contains(rep.Detail, tt.detail) {
				t.Errorf("report = %+v, want %s with %q", rep, tt.status, tt.detail)
			}
		})
	}
	var c adapter.Collected
	if rep := NewDmidecodeFile(filepath.Join(t.TempDir(), "absent.txt"), Options{}).Collect(ctx, &c); rep.Status != model.SourceAbsent {
		t.Errorf("missing file: %+v", rep)
	}
	if rep := NewDmidecodeFile(t.TempDir(), Options{}).Collect(ctx, &c); rep.Status != model.SourceError {
		t.Errorf("directory: %+v", rep)
	}
}

// ddr5Table is a two-socket DDR5 host: two populated slots and an empty one.
func ddr5Table() []byte {
	var b Builder
	b.AddSystem(0x0100, "Example Systems", "R760")
	b.AddBaseboard(0x0200, "Example Systems", "0VDRRW")
	b.AddMemoryDevice(MemoryDevice{Handle: 0x1100, Locator: "CPU0_C1", BankLocator: "P0_Node0_Channel2_Dimm0", SizeMB: 65536,
		MemoryType: TypeDDR5, FormFactor: FormDIMM, TypeDetail: DetailRDIMM, SpeedMTs: 4800, ConfSpeedMTs: 4800,
		Manufacturer: "Example Memory", Serial: "80A1C3", AssetTag: "CPU0_C1_AssetTag", PartNumber: "EXM64G48R2X4-A",
		Ranks: 2, TotalWidth: 80, DataWidth: 64, Technology: TechnologyDRAM, ManufacturerID: 0x0B85})
	b.AddMemoryDevice(MemoryDevice{Handle: 0x1101, Locator: "CPU0_C2", BankLocator: "P0_Node0_Channel2_Dimm1",
		MemoryType: 0x02, FormFactor: 0x02, Manufacturer: "NO DIMM", Serial: "NO DIMM", PartNumber: "NO DIMM"})
	b.AddMemoryDevice(MemoryDevice{Handle: 0x1102, Locator: "CPU1_D1", BankLocator: "P1_Node1_Channel3_Dimm0", SizeMB: 16384,
		MemoryType: TypeDDR5, FormFactor: FormDIMM, TypeDetail: DetailRDIMM, SpeedMTs: 5600, ConfSpeedMTs: 4800,
		Manufacturer: "Example Memory", Serial: "80A1F7", PartNumber: "EXM16G56R1X8-B",
		Ranks: 1, TotalWidth: 80, DataWidth: 64, Technology: TechnologyDRAM})
	return b.Bytes()
}

func TestRawTables(t *testing.T) {
	table := ddr5Table()
	entry := EntryPoint64(3, 5, len(table))
	dir := testutil.Testdata(t, "smbios", "tables-ddr5")
	if *update {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, data := range map[string][]byte{"DMI": table, "smbios_entry_point": entry} {
			if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	// The committed fixture is what the builder produces.
	for name, data := range map[string][]byte{"DMI": table, "smbios_entry_point": entry} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("fixture missing (run go test -update): %v", err)
		}
		if !bytes.Equal(got, data) {
			t.Errorf("testdata/smbios/tables-ddr5/%s differs from the builder output (run go test -update)", name)
		}
	}

	var c adapter.Collected
	rep := NewTables(os.DirFS(dir), ".", "/sys/firmware/dmi/tables", Options{Now: now}).Collect(ctx, &c)
	if rep.Status != model.SourceOK || rep.Records != 3 || len(c.Modules) != 3 || len(c.Hosts) != 1 {
		t.Fatalf("report = %+v, %d modules, %d hosts", rep, len(c.Modules), len(c.Hosts))
	}
	if n := strings.Join(rep.Notes, "\n"); !strings.Contains(n, "SMBIOS 3.5") || !strings.Contains(n, "3 memory slots, 2 populated") {
		t.Errorf("notes = %q", rep.Notes)
	}
	want := []model.Module{
		{ObservedAt: now, Locator: "CPU0_C1", BankLocator: "P0_Node0_Channel2_Dimm0", Handle: some(0x1100), Populated: true,
			Source: Kind, RawRef: "/sys/firmware/dmi/tables#handle 0x1100", DIMM: model.DIMM{
				Manufacturer: "Example Memory", PartNumber: "EXM64G48R2X4-A", Serial: "80A1C3", ManufacturerID: "bank 6, 0x0B",
				SizeBytes: 64 << 30, MemoryType: "DDR5", FormFactor: "DIMM", ModuleType: "RDIMM", Technology: "DRAM",
				Ranks: some(2), DataWidth: some(64), TotalWidth: some(80), SpeedMTs: some(4800), ConfiguredSpeedMTs: some(4800)}},
		{ObservedAt: now, Locator: "CPU0_C2", BankLocator: "P0_Node0_Channel2_Dimm1", Handle: some(0x1101),
			Source: Kind, RawRef: "/sys/firmware/dmi/tables#handle 0x1101"},
		{ObservedAt: now, Locator: "CPU1_D1", BankLocator: "P1_Node1_Channel3_Dimm0", Handle: some(0x1102), Populated: true,
			Source: Kind, RawRef: "/sys/firmware/dmi/tables#handle 0x1102", DIMM: model.DIMM{
				Manufacturer: "Example Memory", PartNumber: "EXM16G56R1X8-B", Serial: "80A1F7",
				SizeBytes: 16 << 30, MemoryType: "DDR5", FormFactor: "DIMM", ModuleType: "RDIMM", Technology: "DRAM",
				Ranks: some(1), DataWidth: some(64), TotalWidth: some(80), SpeedMTs: some(5600), ConfiguredSpeedMTs: some(4800)}},
	}
	for i := range want {
		if got := *c.Modules[i]; !reflect.DeepEqual(got, want[i]) {
			t.Errorf("module %d\n got: %+v\nwant: %+v", i, got, want[i])
		}
	}
	if h := c.Hosts[0]; h.SystemVendor != "Example Systems" || h.SystemProduct != "R760" || h.Board != "Example Systems 0VDRRW" {
		t.Errorf("host: %+v", *h)
	}
}

func TestRawTablesOfOlderAndDamagedFirmware(t *testing.T) {
	read := func(table []byte) (*adapter.Collected, adapter.Report) {
		var c adapter.Collected
		rep := NewTables(fstest.MapFS{"DMI": {Data: table}}, ".", "tables", Options{Now: now}).Collect(ctx, &c)
		return &c, rep
	}

	t.Run("SMBIOS 2.6 structure: fields behind the attributes byte do not exist", func(t *testing.T) {
		full := ddr5Table()
		structs, err := ParseTable(full)
		if err != nil {
			t.Fatal(err)
		}
		var b Builder
		s := structs[2] // CPU0_C1
		b.Add(17, s.Handle, s.Data[4:0x1C], s.Strings...)
		c, rep := read(b.Bytes())
		if rep.Status != model.SourceOK || len(c.Modules) != 1 {
			t.Fatalf("report = %+v", rep)
		}
		d := c.Modules[0].DIMM
		// 65536 MB needs the extended size field, which a 2.6 structure lacks.
		if d.SizeBytes != 0 || d.ConfiguredSpeedMTs.Valid || d.Technology != "" || d.ManufacturerID != "" {
			t.Errorf("fields beyond the structure were read: %+v", d)
		}
		if d.Serial != "80A1C3" || d.Ranks != some(2) || d.SpeedMTs != some(4800) || !c.Modules[0].Populated {
			t.Errorf("fields inside the structure were lost: %+v", d)
		}
	})

	t.Run("structure too short for a locator", func(t *testing.T) {
		var b Builder
		b.Add(17, 0x1100, make([]byte, 12))
		_, rep := read(b.Bytes())
		if rep.Status != model.SourcePartial || rep.Skipped != 1 || !strings.Contains(strings.Join(rep.Notes, "\n"), "too short to hold a locator") {
			t.Errorf("report = %+v", rep)
		}
	})

	t.Run("table cut off in the middle", func(t *testing.T) {
		full := ddr5Table()
		c, rep := read(full[:len(full)-40])
		if rep.Status != model.SourcePartial || len(c.Modules) != 2 || !strings.Contains(strings.Join(rep.Notes, "\n"), "the table is damaged") {
			t.Errorf("report = %+v, %d modules", rep, len(c.Modules))
		}
	})

	t.Run("not a table", func(t *testing.T) {
		for name, data := range map[string][]byte{
			"length below header": {17, 2, 0, 0, 0, 0},
			"text":                []byte("Handle 0x1100, DMI type 17, 84 bytes"),
			"three bytes":         {17, 92, 0},
		} {
			c, rep := read(data)
			if rep.Status != model.SourceError || len(c.Modules) != 0 || !strings.Contains(rep.Detail, "damaged") {
				t.Errorf("%s: report = %+v", name, rep)
			}
		}
	})

	t.Run("empty table", func(t *testing.T) {
		_, rep := read(nil)
		if rep.Status != model.SourceEmpty {
			t.Errorf("report = %+v", rep)
		}
	})

	t.Run("string number beyond the string set", func(t *testing.T) {
		var b Builder
		d := make([]byte, 88)
		d[0x0C-4], d[0x0D-4] = 0x00, 0x40 // 16384 MB
		d[0x10-4] = 9                     // locator: string 9 of 1
		b.Add(17, 0x1100, d, "only one string")
		c, rep := read(b.Bytes())
		if rep.Status != model.SourceOK || c.Modules[0].Locator != "" || c.Modules[0].DIMM.SizeBytes != 16<<30 {
			t.Errorf("report = %+v, module %+v", rep, *c.Modules[0])
		}
	})
}

func TestTablesAbsentAndUnreadable(t *testing.T) {
	var c adapter.Collected
	rep := NewRoot(t.TempDir(), Options{}).Collect(ctx, &c)
	if rep.Status != model.SourceAbsent || !strings.Contains(rep.Detail, "no SMBIOS tables") {
		t.Errorf("absent: %+v", rep)
	}

	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	root := t.TempDir()
	dir := filepath.Join(root, filepath.FromSlash(TablesDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The kernel creates the file with mode 0400, owned by root.
	if err := os.WriteFile(filepath.Join(dir, "DMI"), ddr5Table(), 0o000); err != nil {
		t.Fatal(err)
	}
	a := NewRoot(root, Options{})
	rep = a.Collect(ctx, &c)
	if rep.Status != model.SourceUnreadable || !strings.Contains(rep.Detail, "readable as root") || !strings.Contains(rep.Detail, "serial numbers are unknown") {
		t.Errorf("unreadable: %+v", rep)
	}
	if a.Origin() != dir {
		t.Errorf("origin = %q, want %q", a.Origin(), dir)
	}
}

func TestParseEntryPoint(t *testing.T) {
	ep, err := ParseEntryPoint(EntryPoint64(3, 5, 1234))
	if err != nil || ep.Major != 3 || ep.Minor != 5 || ep.Bits != 64 {
		t.Errorf("64-bit: %+v, %v", ep, err)
	}
	old := make([]byte, 31)
	copy(old, "_SM_")
	old[6], old[7] = 2, 8
	ep, err = ParseEntryPoint(old)
	if err != nil || ep.Major != 2 || ep.Minor != 8 || ep.Bits != 32 {
		t.Errorf("32-bit: %+v, %v", ep, err)
	}
	for _, bad := range [][]byte{nil, []byte("_SM3_"), []byte("garbage-garbage-garbage-garbage-")} {
		if _, err := ParseEntryPoint(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	// The checksum of the generated entry point is valid.
	var sum byte
	for _, v := range EntryPoint64(3, 5, 1234) {
		sum += v
	}
	if sum != 0 {
		t.Errorf("entry point checksum = %d, want 0", sum)
	}
}

type failingSink struct {
	adapter.Collected
	err error
}

func (f *failingSink) Module(*model.Module) error { return f.err }

func TestSinkErrorAndCancel(t *testing.T) {
	boom := errors.New("ledger is full")
	fsys := fstest.MapFS{"DMI": {Data: ddr5Table()}}
	rep := NewTables(fsys, ".", "tables", Options{}).Collect(ctx, &failingSink{err: boom})
	if rep.Status != model.SourceError || !errors.Is(rep.SinkErr, boom) {
		t.Errorf("tables: %+v", rep)
	}
	text := string(testutil.ReadTestdata(t, "smbios", "dmidecode-t17-ddr4.txt"))
	rep = NewDmidecodeReader(strings.NewReader(text), "stdin", Options{}).Collect(ctx, &failingSink{err: boom})
	if rep.Status != model.SourceError || !errors.Is(rep.SinkErr, boom) {
		t.Errorf("dmidecode: %+v", rep)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	var c adapter.Collected
	if rep := NewTables(fsys, ".", "tables", Options{}).Collect(cctx, &c); rep.Status != model.SourceError || len(c.Modules) != 0 {
		t.Errorf("cancelled: %+v", rep)
	}
}
