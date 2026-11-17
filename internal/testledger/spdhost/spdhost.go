// Package spdhost makes hosts with the SPD of their modules for the tests
// of the views and commands that show it: the demo scenario of the
// synthetic generator written as a copy of a host's files, with
// spd-probe.txt and one file spd/<bus>-<address>.bin per DIMM, each built
// from the public DDR5 copy under testdata/spd with the serial of the
// slot's DIMM, its part number and a week written.
package spdhost

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/adapter/dimmspd"
	"github.com/agenticode/my/internal/decode/spd"
	"github.com/agenticode/my/internal/scan"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/synth"
	"github.com/agenticode/my/internal/testledger"
	"github.com/agenticode/my/internal/testutil"
)

// Setup says what SPD a made host holds.
type Setup struct {
	Seed uint64
	Host string
	// Date gives the year and week of a slot; 0 writes bytes that are no
	// date. Skip leaves a slot without a file.
	Date func(slot string) (int, int)
	Skip func(slot string) bool
	// Change changes the bytes of a slot after they are made.
	Change func(slot string, b []byte) []byte
	// Serial changes the serial written for a slot.
	Serial func(slot, serial string) string
	// Part is the part number written; empty writes the part number of
	// the generator's DIMMs.
	Part string
	// NoSMBIOS removes the SMBIOS of the copy.
	NoSMBIOS bool
	// Probe replaces the probe text; the files are then written as they
	// are made and the probe lists none of them unless it says so.
	Probe string
}

// DIMM is a slot of a made host and the serial number SMBIOS gives.
type DIMM struct{ Slot, Serial string }

// GeneratorPart is the part number of the generator's DIMMs.
const GeneratorPart = "EXM64G48R2X4-A"

// Host writes the files of the demo scenario as a copy of a host below a
// new temporary directory, and returns it with its slots and serial
// numbers.
func Host(tb testing.TB, seed uint64) (string, []DIMM) {
	tb.Helper()
	if seed == 0 {
		seed = 1
	}
	ds := testledger.Generate(tb, "demo", seed, func(sc *synth.Scenario) { sc.Host.SerialDigits = 8 })
	root := filepath.Join(tb.TempDir(), "root")
	if err := ds.WriteHostTree(root, synth.FlavorIntel); err != nil {
		tb.Fatal(err)
	}
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(tb.TempDir(), "first.db"), store.Create)
	if err != nil {
		tb.Fatal(err)
	}
	defer s.Close()
	res, err := scan.Run(ctx, s, scan.Options{Root: root, TempDir: tb.TempDir()})
	if err != nil {
		tb.Fatal(err)
	}
	ps, _ := s.Placements(ctx, res.Host, "")
	dimms, _ := s.PlacedDIMMs(ctx, res.Host)
	var out []DIMM
	for _, p := range ps {
		out = append(out, DIMM{p.Slot, dimms[p.DIMMUID].Serial})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
	return root, out
}

// Bytes builds an SPD from the public DDR5 copy with a serial, a part
// number and a week (year 0 writes bytes that are no date).
func Bytes(tb testing.TB, serial, part string, year, week int) []byte {
	tb.Helper()
	base := testutil.ReadTestdata(tb, "spd", "illumos", "ddr5-rdimm-64g-2rx4.bin")
	var ch []spd.Change
	if serial != "" {
		c, err := spd.SetSerial(base, serial)
		if err != nil {
			tb.Fatal(err)
		}
		ch = append(ch, c)
	}
	if year > 0 {
		c, err := spd.SetDate(base, year, week)
		if err != nil {
			tb.Fatal(err)
		}
		ch = append(ch, c)
	} else {
		ch = append(ch, spd.Change{Offset: 515, Bytes: []byte{0xDA, 0xAD}})
	}
	if part != "" {
		ch = append(ch, spd.Change{Offset: 521, Bytes: []byte(fmt.Sprintf("%-30s", part))})
	}
	return spd.Build(base, ch...)
}

// Write writes the SPD files and the probe of a made host.
func Write(tb testing.TB, root string, dimms []DIMM, o Setup) {
	tb.Helper()
	var probe strings.Builder
	probe.WriteString("spd-probe 1\ni2c present\nadapter i2c-0 \"SMBus I801 adapter at 0000:00:1f.4\"\nadapter i2c-1 \"SMBus I801 adapter at 0000:00:1f.4\"\n")
	if err := os.MkdirAll(filepath.Join(root, dimmspd.Dir), 0o755); err != nil {
		tb.Fatal(err)
	}
	part := o.Part
	if part == "" {
		part = GeneratorPart
	}
	for i, d := range dimms {
		if o.Skip != nil && o.Skip(d.Slot) {
			continue
		}
		y, w := 2022, 43
		if o.Date != nil {
			y, w = o.Date(d.Slot)
		}
		sn := d.Serial
		if o.Serial != nil {
			sn = o.Serial(d.Slot, sn)
		}
		b := Bytes(tb, sn, part, y, w)
		if o.Change != nil {
			b = o.Change(d.Slot, b)
		}
		id := fmt.Sprintf("%d-00%02x", i/8, 0x50+i%8)
		if err := os.WriteFile(filepath.Join(root, dimmspd.Dir, id+".bin"), b, 0o644); err != nil {
			tb.Fatal(err)
		}
		fmt.Fprintf(&probe, "client %s name=spd5118 driver=spd5118 eeprom=1024 read=%d state=%s\n", id, len(b), spd.Decode(b).State)
	}
	text := probe.String()
	if o.Probe != "" {
		text = o.Probe
	}
	if err := os.WriteFile(filepath.Join(root, dimmspd.ProbeFile), []byte(text), 0o644); err != nil {
		tb.Fatal(err)
	}
	if o.NoSMBIOS {
		os.RemoveAll(filepath.Join(root, "sys", "firmware", "dmi"))
		os.Remove(filepath.Join(root, "dmidecode.txt"))
	}
}

// Root makes a host with its SPD and returns the root of its files.
func Root(tb testing.TB, o Setup) string {
	tb.Helper()
	root, dimms := Host(tb, o.Seed)
	Write(tb, root, dimms, o)
	return root
}

// Scan makes a host with its SPD and scans it into the ledger.
func Scan(tb testing.TB, s *store.Store, o Setup) {
	tb.Helper()
	root := Root(tb, o)
	if _, err := scan.Run(context.Background(), s, scan.Options{Root: root, TempDir: tb.TempDir(), Host: o.Host}); err != nil {
		tb.Fatal(err)
	}
}
