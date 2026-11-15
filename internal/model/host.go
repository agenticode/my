package model

import "time"

// Host describes a machine whose records are in the ledger.
type Host struct {
	Name          string    `json:"host"`
	Arch          string    `json:"arch,omitzero"`
	SystemVendor  string    `json:"system_vendor,omitzero"`
	SystemProduct string    `json:"system_product,omitzero"`
	Board         string    `json:"board,omitzero"`
	FirstSeen     time.Time `json:"first_seen,omitzero"`
	LastSeen      time.Time `json:"last_seen,omitzero"`
}

// Boot is one boot of a host.
type Boot struct {
	Host string `json:"host"`
	// BootID is the kernel boot ID (32 hex digits) when a source recorded
	// it. Otherwise it is derived from host and start time and Derived is set.
	BootID  string `json:"boot_id"`
	Derived bool   `json:"derived,omitzero"`

	StartedAt    time.Time   `json:"started_at"`
	StartQuality TimeQuality `json:"start_quality"`
	// LastSeen is the latest time known to belong to this boot.
	LastSeen time.Time `json:"last_seen,omitzero"`

	Kernel  string `json:"kernel,omitzero"`  // kernel release, e.g. 5.14.0-570.el9.x86_64
	BIOS    string `json:"bios,omitzero"`    // BIOS vendor, version and date as reported
	BMC     string `json:"bmc,omitzero"`     // BMC firmware version
	Cmdline string `json:"cmdline,omitzero"` // kernel command line

	Source string `json:"source,omitzero"`
	RawRef string `json:"raw_ref,omitzero"`
}

// DerivedBootID returns the boot ID used when no kernel boot ID is known.
func DerivedBootID(host string, startedAt time.Time) string {
	return "derived-" + Fingerprint("boot", host, startedAt.UTC().Format("2006-01-02T15:04:05"))[:16]
}

// CounterSample is one reading of a per-DIMM error counter: of the EDAC
// counters of the kernel, or of the counts another program keeps, such as
// the mcelog daemon. Counters restart at zero, with the boot or with the
// program that keeps them, so a sample is only meaningful together with the
// time the count started.
//
// A sample names its DIMM in one of two ways. An EDAC counter has a memory
// controller, an index and a layer position. A counter of a source that
// knows no EDAC position has the numbers of socket, channel and DIMM
// (SocketId set); MC and DIMMIndex then only keep the counters of the
// source apart.
type CounterSample struct {
	ObservedAt time.Time
	// MC is the EDAC memory controller index, or the socket for a counter
	// placed by numbers.
	MC     int
	MCName string
	// DIMMIndex is the index of the dimmN or rankN directory, or for a
	// counter placed by numbers an index its source chose. It is -1 for
	// errors the source could not tie to a DIMM: the controller-level
	// counters of EDAC, the rest of a socket total.
	DIMMIndex int
	// Location is the EDAC layer position, for example "channel:0 slot:1".
	Location string
	Label    string
	CE, UE   uint64
	// SecondsSinceReset is how long the counters have been running.
	SecondsSinceReset Opt[int64]

	// SocketId, ChannelId and ChannelSlotId place a counter by numbers. A
	// counter of a whole channel has no ChannelSlotId, one of a whole
	// socket only SocketId.
	SocketId, ChannelId, ChannelSlotId Opt[int]
	// BankLocator is the SMBIOS bank locator the source gives for the DIMM.
	BankLocator string
	// RecentCE and RecentUE say how many of the errors counted in CE and UE
	// fell into the time RecentCEWindow and RecentUEWindow before the
	// reading, for a source that keeps a rate window beside its totals. The
	// number is a lower bound: such a window may have dropped errors that
	// are younger than its length.
	RecentCE, RecentUE             Opt[uint64]
	RecentCEWindow, RecentUEWindow time.Duration

	SizeMB   Opt[int]
	MemType  string
	DevType  string
	EdacMode string

	Source string
	RawRef string
}

// Placed reports whether the sample names its DIMM by socket, channel and
// DIMM numbers and not by an EDAC position.
func (c *CounterSample) Placed() bool { return c.SocketId.Valid }
