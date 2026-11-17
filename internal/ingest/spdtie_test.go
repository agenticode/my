package ingest

import (
	"testing"
	"testing/fstest"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/smbios"
	"github.com/agenticode/my/internal/decode/spd"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/testutil"
)

// spdBoard is SMBIOS of a host of eight DDR5 slots with the given serial
// numbers ("" for an empty slot). The maker of slot 7 differs, so that
// its DIMM is another identity even with the serial of another slot.
func spdBoard(serials ...string) fstest.MapFS {
	var b smbios.Builder
	b.AddSystem(0x0100, "Example Systems", "R760")
	for i, sn := range serials {
		md := smbios.MemoryDevice{Handle: uint16(0x1100 + i), Locator: "DIMM" + string(rune('A'+i)), BankLocator: "P0",
			MemoryType: 0x22, FormFactor: smbios.FormDIMM, TypeDetail: smbios.DetailRDIMM, SpeedMTs: 4800,
			Manufacturer: "Example Memory", Serial: sn, PartNumber: "EXM64G48R2X4"}
		if sn != "" {
			md.SizeMB = 65536
			md.Ranks = 2
		}
		if i == 6 {
			md.Manufacturer = "Other Memory"
		}
		b.AddMemoryDevice(md)
	}
	return fstest.MapFS{"DMI": {Data: b.Bytes()}}
}

// spdOf builds the SPD of a slot from the public DDR5 copy: its serial and
// a date written, the CRC written again.
func spdOf(t *testing.T, serial string, year, week int) []byte {
	t.Helper()
	base := testutil.ReadTestdata(t, "spd", "illumos", "ddr5-rdimm-64g-2rx4.bin")
	s, err := spd.SetSerial(base, serial)
	if err != nil {
		t.Fatal(err)
	}
	d, err := spd.SetDate(base, year, week)
	if err != nil {
		t.Fatal(err)
	}
	return spd.Build(base, s, d)
}

type spdRow struct {
	origin string
	raw    []byte
	state  string
}

func spdAdapter(rows []spdRow) adapter.Adapter {
	return &fake{kind: "spd", fn: func(sink adapter.Sink) error {
		for _, r := range rows {
			st := r.state
			if st == "" {
				st = spd.Decode(r.raw).State
			}
			if err := sink.SPD(&model.SPDReading{Origin: r.origin, Form: model.SPDFormEEPROM, State: st, Raw: r.raw, LastSeen: intelNow}); err != nil {
				return err
			}
		}
		return nil
	}}
}

func ties(t *testing.T, s *store.Store, host string) map[string]string {
	t.Helper()
	rs, err := s.SPDReadings(ctx, host)
	if err != nil {
		t.Fatal(err)
	}
	dimms, err := s.PlacedDIMMs(ctx, host)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, r := range rs {
		if r.DIMMUID == "" {
			out[r.Origin] = "-"
			continue
		}
		out[r.Origin] = dimms[r.DIMMUID].Serial + " " + r.Tie
	}
	return out
}

func TestTieSPD(t *testing.T) {
	s := newStore(t)
	board := spdBoard("4D590011", "1200594D", "4D590013", "4D590014", "4D590015", "00000000", "4D590013", "4D590018")
	bad := spdOf(t, "4D590018", 2022, 43)
	bad[234] ^= 0x08 // the base CRC no longer fits
	rows := []spdRow{
		{"a", spdOf(t, "4D590011", 2022, 43), ""},    // equal serial
		{"b", spdOf(t, "4D590011", 2023, 8)[:0], ""}, // no bytes: tied to nothing
		{"c", spdOf(t, "4D590012", 2023, 8), ""},     // the reverse of slot B
		{"d", spdOf(t, "4D590013", 2022, 43), ""},    // two DIMMs carry it
		{"e", spdOf(t, "4D590014", 2022, 43), ""},    // two readings carry it
		{"f", spdOf(t, "4D590014", 2022, 43), ""},
		{"g", spdOf(t, "00000000", 2022, 43), ""}, // placeholder
		{"h", bad, ""}, // crc
		{"i", spdOf(t, "4D590015", 2022, 43)[:128], ""}, // the first page: no serial
	}
	rows[1].state = spd.StateIO
	sm := smbios.NewTables(board, ".", "/sys/firmware/dmi/tables", smbios.Options{Now: intelNow})
	if _, err := Run(ctx, s, []adapter.Adapter{sm, spdAdapter(rows)}, Options{Host: "h1", Kind: model.ScanLive, Now: intelNow}); err != nil {
		t.Fatal(err)
	}
	got := ties(t, s, "h1")
	want := map[string]string{
		"a": "4D590011 serial", "b": "-", "c": "1200594D serial-reversed", "d": "-", "e": "-", "f": "-", "g": "-",
		"h": "4D590018 serial", "i": "-",
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("reading %s: tie %q, want %q", k, got[k], w)
		}
	}
	dimms, _ := s.PlacedDIMMs(ctx, "h1")
	bySerial := map[string]model.DIMM{}
	for _, d := range dimms {
		if d.Manufacturer == "Example Memory" {
			bySerial[d.Serial] = d
		}
	}
	// The DIMM row: date and width of the tied ok reading; the SMBIOS
	// fields kept (part number, maker, size, ranks).
	a := bySerial["4D590011"]
	if a.MfgDate != "2022-W43" || !a.DeviceWidth.Valid || a.DeviceWidth.V != 4 || a.PartNumber != "EXM64G48R2X4" ||
		a.Manufacturer != "Example Memory" || a.SizeBytes != 64<<30 || a.Ranks.V != 2 || a.ManufacturerID != "bank 1, 0x2C" {
		t.Errorf("DIMM of slot A: %+v", a)
	}
	if c := bySerial["1200594D"]; c.MfgDate != "2023-W08" {
		t.Errorf("DIMM tied with the reversed serial: %+v", c)
	}
	// Nothing from a reading of state crc, nor from one without a tie.
	for _, sn := range []string{"4D590018", "4D590014", "4D590015"} {
		if d := bySerial[sn]; d.MfgDate != "" || d.DeviceWidth.Valid {
			t.Errorf("DIMM %s took fields: %+v", sn, d)
		}
	}
}

// A width EDAC gave is replaced by that of the SPD; a date a BMC gave is
// replaced where a reading is tied and kept where none is.
func TestSPDReplacesWidth(t *testing.T) {
	s := newStore(t)
	sm := smbios.NewTables(spdBoard("4D590011", "4D590012"), ".", "/sys/firmware/dmi/tables", smbios.Options{Now: intelNow})
	if _, err := Run(ctx, s, []adapter.Adapter{sm}, Options{Host: "h1", Kind: model.ScanLive, Now: intelNow}); err != nil {
		t.Fatal(err)
	}
	dimms, _ := s.PlacedDIMMs(ctx, "h1")
	err := s.Update(ctx, func(w *store.Writer) error {
		for _, d := range dimms {
			if err := w.UpsertDIMM(&model.DIMM{UID: d.UID, Identity: d.Identity, DeviceWidth: model.Some(8), MfgDate: "2020-W01"}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, s, []adapter.Adapter{sm, spdAdapter([]spdRow{{"a", spdOf(t, "4D590011", 2022, 43), ""}})}, Options{Host: "h1", Kind: model.ScanLive, Now: intelNow}); err != nil {
		t.Fatal(err)
	}
	dimms, _ = s.PlacedDIMMs(ctx, "h1")
	for _, d := range dimms {
		switch d.Serial {
		case "4D590011":
			if d.DeviceWidth.V != 4 || d.MfgDate != "2022-W43" {
				t.Errorf("tied: %+v", d)
			}
		case "4D590012":
			if d.DeviceWidth.V != 8 || d.MfgDate != "2020-W01" {
				t.Errorf("untied: %+v", d)
			}
		}
	}
}

// A host without SMBIOS serial numbers ties nothing.
func TestTieSPDWithoutSMBIOS(t *testing.T) {
	s := newStore(t)
	if _, err := Run(ctx, s, []adapter.Adapter{spdAdapter([]spdRow{{"a", spdOf(t, "4D590011", 2022, 43), ""}})}, Options{Host: "h1", Kind: model.ScanLive, Now: intelNow}); err != nil {
		t.Fatal(err)
	}
	if got := ties(t, s, "h1"); got["a"] != "-" {
		t.Errorf("tie without SMBIOS: %v", got)
	}
}

// A decode-dimms reading gives its date only when it is no later than the
// reading.
func TestPlausibleDate(t *testing.T) {
	r := model.SPDReading{Form: model.SPDFormDecodeDimms, LastSeen: intelNow}
	if !plausibleDate(r, "2021-W43") || plausibleDate(r, "2041-W29") || plausibleDate(r, "0xDAAD") {
		t.Error("dates of decode-dimms text")
	}
	if !plausibleDate(model.SPDReading{Form: model.SPDFormEEPROM}, "2041-W29") {
		t.Error("a BCD date of bytes")
	}
	if k, rev := spdSerialKeys("4D590011"); k != "4D590011" || rev != "1100594D" {
		t.Errorf("keys %q %q", k, rev)
	}
	if k, _ := spdSerialKeys("FFFFFFFF"); k != "" {
		t.Error("a placeholder serial has a key")
	}
}
