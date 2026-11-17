package spdhost

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/agenticode/my/internal/adapter/dimmspd"
	"github.com/agenticode/my/internal/decode/spd"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/synth"
)

// The demo case of the SPD (testdata/spd/demo): a made host of 8 slots,
// written as a copy of a host's files from the synthetic generator, with
// findings on three DIMMs, and the SPD of each module: the public copy
// ddr5-rdimm-64g-2rx4 with the serial number of the slot's DIMM and one of
// two weeks written. Both lie outside the CRC, so every file decodes with
// a good CRC. SMBIOS agrees with the SPD: DDR5 RDIMM, 64 GB, the part
// number of the dump, and the maker as the JEDEC code alone, so that no
// maker string stands beside a code it does not fit.

// DemoHost is the name of the host of the demo case.
const DemoHost = "r07-n112"

// DemoPart is the part number of the public dump the demo case is built
// from.
const DemoPart = "MTC40F2046S1RC48BA1"

// demoWeeks are the weeks of the modules: the early week for the three
// DIMMs with a finding and two without one.
var demoWeeks = map[string][2]int{
	"CPU0_A1": {2022, 43}, "CPU0_B1": {2022, 43}, "CPU0_C1": {2022, 43}, "CPU0_D1": {2023, 8},
	"CPU1_A1": {2022, 43}, "CPU1_B1": {2022, 43}, "CPU1_C1": {2023, 8}, "CPU1_D1": {2023, 8},
}

// DemoScenario is the scenario of the demo case.
func DemoScenario() (*synth.Scenario, error) {
	sc, err := synth.Named("demo", 7)
	if err != nil {
		return nil, err
	}
	sc.Name = "spd-demo"
	sc.Host.Name = DemoHost
	sc.Host.IMCs, sc.Host.ChannelsPerIMC, sc.Host.SlotsPerChannel = 2, 2, 1
	sc.Host.SerialDigits = 8
	sc.Faults = []synth.Fault{
		sc.Faults[0],
		{Mode: model.ModeBank, Slot: "CPU0_B1", Start: sc.Faults[0].Start, Events: 400, Trend: synth.Rising, PatrolShare: 0.2},
		{Mode: model.ModeSingleBit, Slot: "CPU1_A1", Events: 53, Trend: synth.Flat, PatrolShare: 0.3},
	}
	return sc, nil
}

// WriteDemo writes the demo case below dir, which must not exist.
func WriteDemo(dir string, base []byte) error {
	sc, err := DemoScenario()
	if err != nil {
		return err
	}
	ds, err := synth.Generate(sc)
	if err != nil {
		return err
	}
	for i := range ds.Modules {
		m := &ds.Modules[i]
		m.Manufacturer, m.PartNumber, m.ManufacturerID = "", DemoPart, "bank 1, 0x2C"
		m.UID = model.DIMMUID(m.Manufacturer, m.PartNumber, m.Serial)
	}
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("%s exists", dir)
	}
	if err := ds.WriteHostTree(dir, synth.FlavorIntel); err != nil {
		return err
	}
	// The DIMM of each slot at the end of the scenario.
	last := len(ds.Occupancy) - 1
	type slot struct{ name, serial string }
	var slots []slot
	for _, s := range ds.Slots {
		if idx := ds.Occupancy[last][s.Index]; idx >= 0 {
			slots = append(slots, slot{s.Name, ds.Modules[idx].Serial})
		}
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i].name < slots[j].name })
	if len(slots) != 8 {
		return fmt.Errorf("%d populated slots, want 8", len(slots))
	}
	if err := os.MkdirAll(filepath.Join(dir, dimmspd.Dir), 0o755); err != nil {
		return err
	}
	var probe strings.Builder
	probe.WriteString("spd-probe 1\ni2c present\nadapter i2c-0 \"SMBus I801 adapter at 0000:00:1f.4\"\n" +
		"pci 0000:00:1f.4 id=8086:1bc9 driver=i801_smbus\ndriver i801_smbus registered\ndriver ee1004 module\ndriver spd5118 registered\n")
	for i, s := range slots {
		w, ok := demoWeeks[s.name]
		if !ok {
			return fmt.Errorf("no week for %s", s.name)
		}
		sn, err := spd.SetSerial(base, s.serial)
		if err != nil {
			return err
		}
		dt, err := spd.SetDate(base, w[0], w[1])
		if err != nil {
			return err
		}
		b := spd.Build(base, sn, dt)
		id := fmt.Sprintf("0-00%02x", 0x50+i)
		if err := os.WriteFile(filepath.Join(dir, dimmspd.Dir, id+".bin"), b, 0o644); err != nil {
			return err
		}
		fmt.Fprintf(&probe, "client %s name=spd5118 driver=spd5118 eeprom=1024 read=1024 state=%s\n", id, spd.Decode(b).State)
	}
	probe.WriteString("bmc present\n")
	return os.WriteFile(filepath.Join(dir, dimmspd.ProbeFile), []byte(probe.String()), 0o644)
}
