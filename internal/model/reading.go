package model

import "time"

// HostReading is the text of a file or of a read-only command about the
// memory of a host at the time of a scan: what the Memory Explorer shows.
// A reading holds no error record; no rule reads it.
type HostReading struct {
	Host   string `json:"host"`
	ScanID int64  `json:"scan_id,omitzero"`
	// Kind is what was read (ReadingMeminfo and the others).
	Kind string `json:"kind"`
	// Key tells readings of one kind apart: a pid, or empty.
	Key     string    `json:"key,omitzero"`
	TakenAt time.Time `json:"taken_at"`
	// Origin is where it came from: a path or a command.
	Origin string `json:"origin,omitzero"`
	Text   string `json:"text"`
	// Cut is set when the text was longer than the bound of its kind.
	Cut bool `json:"cut,omitzero"`
}

// Kinds of readings.
const (
	ReadingMeminfo       = "meminfo"
	ReadingVmstat        = "vmstat"
	ReadingPressure      = "pressure-memory"
	ReadingSlabinfo      = "slabinfo"
	ReadingCPUCache      = "cpu-cache"
	ReadingNumactl       = "numactl-hardware"
	ReadingNumastat      = "numastat"
	ReadingSarB          = "sar-b"
	ReadingLruGenEnabled = "lru-gen-enabled"
	ReadingLshw          = "lshw"
)
