package model

import "time"

// A BMC knows things about a host that the host's own logs do not hold:
// which firmware runs, how the BIOS is set up for memory errors, and its
// own count of the errors of each module. The three types below carry
// what a source read from a BMC beyond events, actions and the inventory.

// BMC is one reading of a management controller: what it is, what it runs,
// and how complete the reading was.
type BMC struct {
	ID         int64     `json:"id,omitzero"`
	Host       string    `json:"host"`
	ObservedAt time.Time `json:"observed_at"`
	// Address is the host name or IP address the BMC was asked at, when
	// the source knows it.
	Address string `json:"address,omitzero"`
	// Manager is the model of the BMC, Firmware its firmware version.
	Manager  string `json:"manager,omitzero"`
	Firmware string `json:"firmware,omitzero"`
	// BIOSVersion is the version of the system firmware the BMC reports.
	BIOSVersion    string `json:"bios_version,omitzero"`
	RedfishVersion string `json:"redfish_version,omitzero"`
	// BIOSRegistry names the attribute registry the BIOS settings of this
	// reading refer to.
	BIOSRegistry string `json:"bios_registry,omitzero"`
	// Live is set when this tool asked the BMC itself. A dump made by
	// other means says nothing about what was asked and not answered.
	Live bool `json:"live,omitzero"`
	// Complete says whether a live reading asked for everything it wanted.
	Complete Opt[bool] `json:"complete,omitzero"`
	// TLS and Auth say how a live reading reached the BMC.
	TLS  string `json:"tls,omitzero"`
	Auth string `json:"auth,omitzero"`
	// ClockOffset is the clock of the BMC minus the clock of the machine
	// that read it, in seconds.
	ClockOffset Opt[int64] `json:"clock_offset_seconds,omitzero"`
	// LastReset is when the system was last reset, as the BMC reports it.
	// Counts "since reset" started no earlier.
	LastReset time.Time `json:"last_reset,omitzero"`
	// Classes says what became of each class of resources: the row of
	// this BMC in a support table.
	Classes []BMCClass `json:"classes,omitzero"`
	// Logs describes the event logs that were read.
	Logs []BMCLog `json:"logs,omitzero"`
	// Notes are remarks for the reader.
	Notes []string `json:"notes,omitzero"`

	Source      string `json:"source"`
	RawRef      string `json:"raw_ref,omitzero"`
	Fingerprint string `json:"-"`
	ScanID      int64  `json:"scan_id,omitzero"`
}

// BMCClass is the outcome for one class of resources of a reading.
type BMCClass struct {
	Class string `json:"class"`
	// State is ok, partial, unsupported, denied, timeout, error, skipped,
	// "not offered", "not reached", or "present" and "absent" for a dump
	// that has no record of what was asked.
	State string `json:"state"`
}

// BMCLog is one event log of a BMC as it was read.
type BMCLog struct {
	// ID is the Id of the log service, Path where it hangs.
	ID   string `json:"id"`
	Path string `json:"path,omitzero"`
	// Entries is the number of entries read, Memory the number among them
	// that are about memory.
	Entries int `json:"entries"`
	Memory  int `json:"memory"`
	// First and Last are the times of the oldest and the newest entry
	// read, of any kind: the period the log covers.
	First time.Time `json:"first,omitzero"`
	Last  time.Time `json:"last,omitzero"`
	// Total is the number of entries the log says it holds, when it says.
	Total Opt[int] `json:"total,omitzero"`
	// Truncated is set when not every entry of the log was read.
	Truncated bool `json:"truncated,omitzero"`
	// Policy is what the log does when it is full, as the service names
	// it: WrapsWhenFull, NeverOverWrites.
	Policy string `json:"policy,omitzero"`
	// Max is the number of entries the log can hold.
	Max Opt[int] `json:"max,omitzero"`
}

// BIOSSetting is one setting of the system firmware as a BMC reports it:
// the value in effect, the value that waits for the next boot, and what
// the attribute registry of that firmware says about the setting.
type BIOSSetting struct {
	ID         int64     `json:"id,omitzero"`
	Host       string    `json:"host"`
	ObservedAt time.Time `json:"observed_at"`
	// Name is the attribute name of this firmware.
	Name string `json:"name"`
	// Value is the value in effect, as text; HasValue is unset when the
	// firmware lists the attribute without a value.
	Value    string `json:"value"`
	HasValue bool   `json:"has_value"`
	// Pending is the value that takes effect after the next boot, when the
	// BMC holds one that differs from Value.
	Pending    string `json:"pending,omitzero"`
	HasPending bool   `json:"has_pending,omitzero"`
	// Type is the kind of value: enumeration, string, integer, boolean.
	Type string `json:"type,omitzero"`

	// What the registry says. Registry is empty when none was read, and
	// the fields below are then unknown.
	Registry      string    `json:"registry,omitzero"`
	DisplayName   string    `json:"display_name,omitzero"`
	MenuPath      string    `json:"menu_path,omitzero"`
	ReadOnly      Opt[bool] `json:"read_only,omitzero"`
	ResetRequired Opt[bool] `json:"reset_required,omitzero"`
	// Allowed lists the values an enumeration may take.
	Allowed    []string   `json:"allowed,omitzero"`
	LowerBound Opt[int64] `json:"lower_bound,omitzero"`
	UpperBound Opt[int64] `json:"upper_bound,omitzero"`
	Default    string     `json:"default,omitzero"`
	HasDefault bool       `json:"has_default,omitzero"`

	Source string `json:"source"`
	RawRef string `json:"raw_ref,omitzero"`
	ScanID int64  `json:"scan_id,omitzero"`
}

// BMCMemory is one memory module as a BMC describes it at one moment: its
// names, its state, and the error counts the BMC keeps for it. The static
// description of the module (maker, part number, size) is in the DIMM
// inventory.
type BMCMemory struct {
	ID         int64     `json:"id,omitzero"`
	Host       string    `json:"host"`
	ObservedAt time.Time `json:"observed_at"`
	// Label is the name the source uses for the slot. Slot is the slot of
	// the ledger it was tied to.
	Label string `json:"label"`
	Slot  string `json:"slot,omitzero"`
	// The names of the slot one by one: what is printed on the board, the
	// service label, and the Id of the resource.
	DeviceLocator string `json:"device_locator,omitzero"`
	ServiceLabel  string `json:"service_label,omitzero"`
	ResourceID    string `json:"resource_id,omitzero"`

	State  string `json:"state,omitzero"`
	Health string `json:"health,omitzero"`
	// ErrorCorrection is the scheme the BMC names: NoECC, SingleBitECC,
	// MultiBitECC, AddressParity.
	ErrorCorrection string `json:"error_correction,omitzero"`
	// OperatingSpeed is the speed in use and AllowedSpeeds the speeds the
	// module supports, in MHz or MT/s as the BMC gives them.
	OperatingSpeed Opt[int] `json:"operating_speed,omitzero"`
	AllowedSpeeds  []int    `json:"allowed_speeds,omitzero"`
	// RankSpare and SpareDevice say whether rank sparing and device
	// sparing are in use for this module.
	RankSpare   Opt[bool] `json:"rank_spare,omitzero"`
	SpareDevice Opt[bool] `json:"spare_device,omitzero"`
	// DeviceWidth is the width of the DRAM devices in bits (4 for x4),
	// when the BMC gives it in a field of its vendor, which
	// DeviceWidthSource names.
	DeviceWidth       Opt[int] `json:"device_width,omitzero"`
	DeviceWidthSource string   `json:"device_width_source,omitzero"`

	// Metrics says what became of the error counts of the module: "ok",
	// "not offered" when the BMC links none, or why they did not come back
	// (unsupported, denied, timeout, error), or "" when the source cannot
	// tell.
	Metrics string `json:"metrics,omitzero"`
	// The counts. "Current" counts since the last reset of the system or
	// of the counters; "Lifetime" for the life of the module.
	CECurrent  Opt[int64] `json:"ce_current,omitzero"`
	UECurrent  Opt[int64] `json:"ue_current,omitzero"`
	CELifetime Opt[int64] `json:"ce_lifetime,omitzero"`
	UELifetime Opt[int64] `json:"ue_lifetime,omitzero"`
	// AlarmCE and AlarmUE are set when the BMC reports that the module
	// crossed its threshold for corrected or uncorrected errors.
	AlarmCE Opt[bool] `json:"alarm_ce,omitzero"`
	AlarmUE Opt[bool] `json:"alarm_ue,omitzero"`
	// CurrentSince is the earliest time the current counts can have
	// started at: the last reset of the system, when the BMC reports it.
	CurrentSince time.Time `json:"current_since,omitzero"`

	Source      string `json:"source"`
	RawRef      string `json:"raw_ref,omitzero"`
	Raw         string `json:"-"`
	Fingerprint string `json:"-"`
	ScanID      int64  `json:"scan_id,omitzero"`
}

// HasCounts reports whether the BMC gave any error count for the module.
func (m *BMCMemory) HasCounts() bool {
	return m.CECurrent.Valid || m.UECurrent.Valid || m.CELifetime.Valid || m.UELifetime.Valid
}
