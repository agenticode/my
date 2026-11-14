// Package synth generates synthetic memory error histories: a host, its
// DIMMs, boots, faults of each Level 1 mode, repair actions, slot moves and
// uncorrected errors. The output drives tests and demos without hardware.
//
// Everything is derived from a seed, so a scenario always produces the same
// data. Manufacturer, part and serial numbers are made up.
package synth

import (
	"fmt"
	"sort"
	"time"

	"github.com/agenticode/my/internal/model"
)

// Geometry is the internal layout of the synthetic DIMMs.
type Geometry struct {
	Ranks      int
	Devices    int // DRAM devices per rank
	BankGroups int
	Banks      int // banks per bank group
	Rows       int
	Columns    int
	// ColumnStep is the granularity of reported columns. Memory controllers
	// report the start of the cache line, 8 column addresses wide.
	ColumnStep  int
	DeviceWidth int // data lines per device
}

// DDR5x4 is a 64 GiB DDR5 RDIMM with two ranks of x4 16 Gbit devices:
// 8 bank groups of 4 banks, 65536 rows and 2048 columns per bank.
var DDR5x4 = Geometry{Ranks: 2, Devices: 20, BankGroups: 8, Banks: 4, Rows: 1 << 16, Columns: 1 << 11, ColumnStep: 8, DeviceWidth: 4}

// RankBytes is the capacity of one rank: 8 bytes per column address.
func (g Geometry) RankBytes() int64 {
	return int64(g.BankGroups) * int64(g.Banks) * int64(g.Rows) * int64(g.Columns) * 8
}

// SizeBytes is the capacity of the DIMM.
func (g Geometry) SizeBytes() int64 { return int64(g.Ranks) * g.RankBytes() }

// Host describes the synthetic machine.
type Host struct {
	Name    string
	Sockets int
	// IMCs is the number of memory controllers per socket, ChannelsPerIMC
	// the channels of each, SlotsPerChannel the DIMM slots of each channel.
	IMCs            int
	ChannelsPerIMC  int
	SlotsPerChannel int
	// PopulateAll fills every slot. Otherwise only the first slot of each
	// channel holds a DIMM.
	PopulateAll bool
	Geometry    Geometry

	SystemVendor  string
	SystemProduct string
	Kernel        string
	BIOS          string
}

// DefaultHost is a two-socket machine with 8 channels per socket and one
// DIMM per channel.
func DefaultHost(name string) Host {
	return Host{
		Name: name, Sockets: 2, IMCs: 4, ChannelsPerIMC: 2, SlotsPerChannel: 2, Geometry: DDR5x4,
		SystemVendor: "Example Systems", SystemProduct: "R760", Kernel: "5.14.0-570.12.1.el9_6.x86_64", BIOS: "Example BIOS Inc. 2.4.4 03/11/2026",
	}
}

// SlotName returns the board name of a slot: CPU<socket>_<channel letter><position>.
// The channel letter counts across the controllers of the socket.
func (h Host) SlotName(socket, imc, channel, position int) string {
	return fmt.Sprintf("CPU%d_%c%d", socket, 'A'+rune(imc*h.ChannelsPerIMC+channel), position+1)
}

// Trend shapes how the events of a fault are spread over its active time.
type Trend int

const (
	// Flat spreads events evenly.
	Flat Trend = iota
	// Rising puts more events towards the end.
	Rising
	// Burst puts most events into a short storm near the end.
	Burst
)

// Fault is one fault to synthesize.
type Fault struct {
	// Mode is the Level 1 fault mode the events are shaped as.
	Mode model.FaultMode
	// Slot names the DIMM slot the fault starts in.
	Slot string
	// Start and End bound the activity; zero values mean the scenario span.
	Start, End time.Time
	// Events is the number of corrected error events.
	Events int
	Trend  Trend
	// PatrolShare is the share of events found by the patrol scrubber.
	PatrolShare float64
	// UE lists times of uncorrected errors at the fault location.
	UE []time.Time
	// StaysWithSlot makes the errors stay in the slot when the DIMM is
	// moved, as a fault of the slot, channel or board does. Otherwise they
	// follow the DIMM.
	StaysWithSlot bool
	// StopAfterRepair ends the fault at the first successful repair of its
	// row. Without it the fault goes on after the repair.
	StopAfterRepair bool
	// SpreadToNextRow is the share of events in the last third of a row
	// fault that land in the next row: a row fault that starts to spread.
	SpreadToNextRow float64

	// Location, chosen from the seed where not set.
	Rank, Device, BankGroup, Bank, Row, Column model.Opt[int]
}

// Reboot is a boot of the host during the scenario.
type Reboot struct {
	At     time.Time
	Kernel string // empty keeps the host's kernel
	BIOS   string // empty keeps the host's BIOS
}

// Move swaps the DIMMs of two slots at a reboot.
type Move struct {
	At   time.Time // must be the time of a Reboot
	A, B string
}

// Repair is a repair action on the row of a fault.
type Repair struct {
	At     time.Time
	Kind   model.ActionKind // ActionSPPR, ActionHPPR, ActionSparing
	Fault  int              // index into Scenario.Faults
	Failed bool
}

// Offline takes pages of a fault offline.
type Offline struct {
	At    time.Time
	Fault int // index into Scenario.Faults
	// Pages is the number of pages; the pages with the most errors so far
	// are taken.
	Pages int
}

// Scenario is a complete script for the generator.
type Scenario struct {
	Name  string
	Seed  uint64
	Host  Host
	Start time.Time
	End   time.Time
	// Reboots after the boot that is already running at Start.
	Reboots  []Reboot
	Faults   []Fault
	Moves    []Move
	Repairs  []Repair
	Offlines []Offline
	// NoisePerDIMMDay is the rate of unrelated single corrected errors per
	// DIMM and day.
	NoisePerDIMMDay float64
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func clock(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, time.UTC)
}

// base returns the frame every named scenario shares: the default host, a
// span of fifteen weeks and two reboots.
func base(name string, seed uint64) *Scenario {
	return &Scenario{
		Name: name, Seed: seed, Host: DefaultHost("synth-" + name),
		Start: day(2026, 8, 2), End: day(2026, 11, 13),
		Reboots:         []Reboot{{At: clock(2026, 9, 1, 3, 0)}, {At: clock(2026, 10, 1, 3, 0)}},
		NoisePerDIMMDay: 0.004,
	}
}

// builders holds the named scenarios.
var builders = map[string]func(seed uint64) *Scenario{
	"single-bit": func(seed uint64) *Scenario {
		s := base("single-bit", seed)
		s.Faults = []Fault{{Mode: model.ModeSingleBit, Slot: "CPU1_D1", Events: 53, Trend: Flat, PatrolShare: 0.3}}
		return s
	},
	"row": func(seed uint64) *Scenario {
		s := base("row", seed)
		s.Faults = []Fault{{Mode: model.ModeRow, Slot: "CPU0_C1", Start: day(2026, 9, 21), Events: 400, Trend: Rising, PatrolShare: 0.2}}
		return s
	},
	"multi-row": func(seed uint64) *Scenario {
		s := base("multi-row", seed)
		s.Faults = []Fault{{Mode: model.ModeMultiRow, Slot: "CPU0_C1", Start: day(2026, 9, 10), Events: 300, Trend: Rising, PatrolShare: 0.2}}
		return s
	},
	"column": func(seed uint64) *Scenario {
		s := base("column", seed)
		s.Faults = []Fault{{Mode: model.ModeColumn, Slot: "CPU0_E1", Start: day(2026, 8, 20), Events: 250, Trend: Flat, PatrolShare: 0.5}}
		return s
	},
	"bank": func(seed uint64) *Scenario {
		s := base("bank", seed)
		s.Faults = []Fault{{Mode: model.ModeBank, Slot: "CPU1_A1", Start: day(2026, 9, 1), Events: 600, Trend: Rising, PatrolShare: 0.4}}
		return s
	},
	"device": func(seed uint64) *Scenario {
		s := base("device", seed)
		s.Faults = []Fault{{Mode: model.ModeDevice, Slot: "CPU1_G1", Start: day(2026, 9, 15), Events: 900, Trend: Burst, PatrolShare: 0.4,
			UE: []time.Time{clock(2026, 11, 10, 14, 2)}}}
		return s
	},
	"dimm": func(seed uint64) *Scenario {
		s := base("dimm", seed)
		s.Faults = []Fault{{Mode: model.ModeDIMM, Slot: "CPU0_H1", Start: day(2026, 8, 10), Events: 1200, Trend: Rising, PatrolShare: 0.4}}
		return s
	},
	// Two DIMMs on one channel show errors: the cause is likely not a DIMM.
	"system-channel": func(seed uint64) *Scenario {
		s := base("system-channel", seed)
		s.Host.PopulateAll = true
		s.Faults = []Fault{
			{Mode: model.ModeSystemIssue, Slot: "CPU0_B1", Start: day(2026, 9, 5), Events: 180, Trend: Flat, StaysWithSlot: true},
			{Mode: model.ModeSystemIssue, Slot: "CPU0_B2", Start: day(2026, 9, 5), Events: 160, Trend: Flat, StaysWithSlot: true},
		}
		return s
	},
	// The DIMM is swapped with a neighbour and the errors stay in the slot.
	"system-slot": func(seed uint64) *Scenario {
		s := base("system-slot", seed)
		s.Faults = []Fault{{Mode: model.ModeSystemIssue, Slot: "CPU0_C1", Start: day(2026, 8, 20), Events: 240, Trend: Flat, StaysWithSlot: true}}
		s.Moves = []Move{{At: clock(2026, 10, 1, 3, 0), A: "CPU0_C1", B: "CPU0_D1"}}
		return s
	},
	// The DIMM is swapped with a neighbour and the errors follow it.
	"moved-dimm": func(seed uint64) *Scenario {
		s := base("moved-dimm", seed)
		s.Faults = []Fault{{Mode: model.ModeRow, Slot: "CPU0_C1", Start: day(2026, 8, 20), Events: 240, Trend: Flat, PatrolShare: 0.2}}
		s.Moves = []Move{{At: clock(2026, 10, 1, 3, 0), A: "CPU0_C1", B: "CPU0_D1"}}
		return s
	},
	// A row fault that is repaired at boot and stays quiet afterwards.
	"repaired": func(seed uint64) *Scenario {
		s := base("repaired", seed)
		s.Faults = []Fault{{Mode: model.ModeRow, Slot: "CPU0_C1", Start: day(2026, 8, 20), Events: 150, Trend: Flat, PatrolShare: 0.2, StopAfterRepair: true}}
		s.Repairs = []Repair{{At: clock(2026, 10, 1, 3, 0), Kind: model.ActionHPPR, Fault: 0}}
		return s
	},
	// The case of the design mockups: a row fault that grows, is repaired
	// at a boot, comes back, has pages taken offline and ends in an
	// uncorrected error, next to a DIMM with a harmless single-bit fault.
	"demo": func(seed uint64) *Scenario {
		s := base("demo", seed)
		s.Host.Name = "r12-n034"
		s.Reboots = []Reboot{{At: clock(2026, 8, 26, 2, 0)}, {At: clock(2026, 9, 30, 2, 0)}, {At: clock(2026, 10, 28, 1, 30)}}
		s.Faults = []Fault{
			{Mode: model.ModeRow, Slot: "CPU0_C1", Start: day(2026, 9, 21), Events: 1231, Trend: Rising, PatrolShare: 0.15,
				SpreadToNextRow: 0.04,
				UE:              []time.Time{clock(2026, 11, 12, 3, 14)},
				Rank:            model.Some(0), Device: model.Some(7), BankGroup: model.Some(0), Bank: model.Some(3), Row: model.Some(0x1a2f)},
			{Mode: model.ModeSingleBit, Slot: "CPU1_D1", Events: 53, Trend: Flat, PatrolShare: 0.3},
		}
		s.Repairs = []Repair{{At: clock(2026, 10, 28, 1, 30), Kind: model.ActionHPPR, Fault: 0}}
		s.Offlines = []Offline{{At: clock(2026, 11, 11, 9, 0), Fault: 0, Pages: 6}}
		s.NoisePerDIMMDay = 0
		return s
	},
}

// Names returns the names of the built-in scenarios.
func Names() []string {
	out := make([]string, 0, len(builders))
	for n := range builders {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Named returns a built-in scenario.
func Named(name string, seed uint64) (*Scenario, error) {
	b, ok := builders[name]
	if !ok {
		return nil, fmt.Errorf("unknown scenario %q (known: %v)", name, Names())
	}
	return b(seed), nil
}
