package model

import (
	"fmt"
	"strings"
	"time"
)

// IdentityKind says what a DIMM identity is based on.
type IdentityKind string

const (
	// IdentitySerial: manufacturer, part number and serial number were read.
	IdentitySerial IdentityKind = "serial"
	// IdentitySlot: no usable serial number was available, so the identity is
	// a placeholder derived from host and slot. It does not follow the DIMM
	// when it is moved.
	IdentitySlot IdentityKind = "slot"
)

// DIMM is the identity and static description of one memory module.
type DIMM struct {
	// UID is the ledger key: a hash of manufacturer, part number and serial
	// number (DIMMUID), or a slot placeholder (SlotUID).
	UID      string       `json:"dimm_uid"`
	Identity IdentityKind `json:"identity"`

	Manufacturer string `json:"manufacturer,omitzero"`
	PartNumber   string `json:"part_number,omitzero"`
	Serial       string `json:"serial,omitzero"`
	// ManufacturerID is the JEDEC manufacturer code when the source exposes
	// it separately from the name, for example "bank 1, 0xCE".
	ManufacturerID string `json:"manufacturer_id,omitzero"`

	SizeBytes  int64  `json:"size_bytes,omitzero"`
	MemoryType string `json:"memory_type,omitzero"` // DDR4, DDR5, ...
	FormFactor string `json:"form_factor,omitzero"` // DIMM, SODIMM, ...
	ModuleType string `json:"module_type,omitzero"` // RDIMM, LRDIMM, UDIMM
	Technology string `json:"technology,omitzero"`  // DRAM, NVDIMM-N, ...
	// Ranks is the number of ranks on the module.
	Ranks Opt[int] `json:"ranks,omitzero"`
	// DeviceWidth is the DRAM device width in bits (4 for x4).
	DeviceWidth Opt[int] `json:"device_width,omitzero"`
	DataWidth   Opt[int] `json:"data_width,omitzero"`
	TotalWidth  Opt[int] `json:"total_width,omitzero"`
	// SpeedMTs is the rated speed, ConfiguredSpeedMTs the speed in use.
	SpeedMTs           Opt[int] `json:"speed_mts,omitzero"`
	ConfiguredSpeedMTs Opt[int] `json:"configured_speed_mts,omitzero"`
	// MfgDate is the manufacturing week as "YYYY-Www", when a source has it.
	MfgDate string `json:"mfg_date,omitzero"`

	FirstSeen time.Time `json:"first_seen,omitzero"`
	LastSeen  time.Time `json:"last_seen,omitzero"`
}

// Organization returns the rank and device width in the usual short form,
// for example "2Rx4", or "" when either is unknown.
func (d *DIMM) Organization() string {
	if !d.Ranks.Valid || !d.DeviceWidth.Valid {
		return ""
	}
	return fmt.Sprintf("%dRx%d", d.Ranks.V, d.DeviceWidth.V)
}

// DIMMUID returns the ledger key for a DIMM with the given identity strings.
// The key does not depend on the slot or the host, so the history of a DIMM
// follows it when it is moved.
func DIMMUID(manufacturer, partNumber, serial string) string {
	return "d" + Fingerprint("dimm", normIdentity(manufacturer), normIdentity(partNumber), normIdentity(serial))[:19]
}

// SlotUID returns the placeholder key used when a slot has no readable DIMM
// identity.
func SlotUID(host, slot string) string {
	return "s" + Fingerprint("slot", host, NormalizeLabel(slot))[:19]
}

func normIdentity(s string) string {
	return strings.ToUpper(strings.Join(strings.Fields(s), " "))
}

// placeholderSerials are values firmware puts in the serial number field when
// it has nothing to report. They must not be used as an identity.
var placeholderSerials = map[string]bool{
	"":                       true,
	"NO DIMM":                true,
	"NODIMM":                 true,
	"UNKNOWN":                true,
	"NOT SPECIFIED":          true,
	"NOT PROVIDED":           true,
	"NOT AVAILABLE":          true,
	"N/A":                    true,
	"NONE":                   true,
	"EMPTY":                  true,
	"[EMPTY]":                true,
	"DEFAULT STRING":         true,
	"TO BE FILLED BY O.E.M.": true,
	"SERNUM":                 true,
	"0123456789":             true,
	"1234567890":             true,
}

// UsableSerial reports whether s can identify a DIMM. Empty strings, firmware
// placeholders and all-zero or all-F values cannot.
func UsableSerial(s string) bool {
	n := normIdentity(s)
	if placeholderSerials[n] {
		return false
	}
	if strings.HasPrefix(n, "SERNUM") && len(n) <= 9 {
		return false // "SerNum00" style placeholders
	}
	hex := strings.TrimPrefix(n, "0X")
	if hex != "" && (strings.Trim(hex, "0") == "" || strings.Trim(hex, "F") == "") {
		return false
	}
	return true
}

// Slot is one physical memory slot of a host, with the names different
// sources use for it.
type Slot struct {
	Host string `json:"host"`
	// Name is the name used throughout the ledger: the SMBIOS device locator
	// when one is known, otherwise the EDAC label.
	Name string `json:"slot"`

	Locator      string   `json:"locator,omitzero"`      // SMBIOS Device Locator
	BankLocator  string   `json:"bank_locator,omitzero"` // SMBIOS Bank Locator
	SMBIOSHandle Opt[int] `json:"smbios_handle,omitzero"`

	Socket Opt[int] `json:"socket,omitzero"`
	IMC    Opt[int] `json:"imc,omitzero"`
	// Channel counts within the memory controller when IMC is set, and
	// within the socket otherwise. Position is the DIMM position on the
	// channel. Sorting by socket, controller, channel and position gives
	// the board order either way.
	Channel  Opt[int] `json:"channel,omitzero"`
	Position Opt[int] `json:"position,omitzero"`

	EdacMC       Opt[int] `json:"edac_mc,omitzero"`
	EdacLocation string   `json:"edac_location,omitzero"`
	EdacLabel    string   `json:"edac_label,omitzero"`
	// Mapping says how the EDAC position was tied to the SMBIOS locator:
	// "label", "bank-locator" or "" when the two were not tied.
	Mapping string `json:"mapping,omitzero"`

	Populated Opt[bool] `json:"populated,omitzero"`
}

// Slot mapping methods.
const (
	MapLabel       = "label"        // the EDAC label equals the SMBIOS locator
	MapBankLocator = "bank-locator" // socket, channel and position parsed from both sides agree
)

// Module is one slot as an inventory source described it at one moment: the
// slot and, if populated, the DIMM in it.
type Module struct {
	ObservedAt  time.Time
	Locator     string
	BankLocator string
	Handle      Opt[int]
	// Aliases are other names the source has for the slot: the service
	// label of a BMC next to its device locator, the Id of its resource.
	// Records that name the slot by one of them are tied to it.
	Aliases []string
	// Socket, Channel and Position are set by inventory sources that number
	// the place of the slot themselves (a BMC, a firmware log): the socket,
	// the channel within the socket and the position on the channel, in the
	// source's own numbering. Error records of the same source family name
	// the place by these numbers. Sources that only name the slot leave
	// them unset.
	Socket    Opt[int]
	Channel   Opt[int]
	Position  Opt[int]
	Populated bool
	// DIMM describes the module. UID and Identity are filled in by the
	// ledger, which knows the host.
	DIMM   DIMM
	Source string
	RawRef string
}

// PlaceName is the name of a slot that is known by numbers only: the
// socket, the channel within the socket and the position on the channel.
// The ledger gives this name to a place error records name by such numbers
// when no inventory names the slot, and an inventory that lists a module by
// numbers without a locator uses it, so both mean the same slot.
func PlaceName(socket, channel, position int) string {
	return fmt.Sprintf("socket%d channel%d dimm%d", socket, channel, position)
}

// Placement records that a DIMM was seen in a slot of a host over a period.
type Placement struct {
	ID        int64     `json:"id,omitzero"`
	DIMMUID   string    `json:"dimm_uid"`
	Host      string    `json:"host"`
	Slot      string    `json:"slot"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	Source    string    `json:"source,omitzero"`
}
