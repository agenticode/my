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

// CounterSample is one reading of a per-DIMM (or per-controller) error
// counter. Counters restart at zero on every boot, so a sample is only
// meaningful together with the time the count started.
type CounterSample struct {
	ObservedAt time.Time
	// MC is the EDAC memory controller index.
	MC     int
	MCName string
	// DIMMIndex is the index of the dimmN or rankN directory, or -1 for the
	// controller-level counters of errors EDAC could not tie to a DIMM.
	DIMMIndex int
	// Location is the EDAC layer position, for example "channel:0 slot:1".
	Location string
	Label    string
	CE, UE   uint64
	// SecondsSinceReset is how long the counters have been running.
	SecondsSinceReset Opt[int64]

	SizeMB   Opt[int]
	MemType  string
	DevType  string
	EdacMode string

	Source string
	RawRef string
}
