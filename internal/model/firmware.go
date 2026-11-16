package model

import "time"

// Origins of a firmware record: how it reached the ledger.
const (
	// OriginBMCLog: an entry of a log a BMC keeps, read over Redfish.
	OriginBMCLog = "bmc-log"
	// OriginBMCState: a state a BMC reports for a module or the system,
	// read at the time of the dump.
	OriginBMCState = "bmc-state"
	// OriginSEL: a record of the IPMI system event log.
	OriginSEL = "sel"
	// OriginToolExport: a line of the saved output of a vendor tool.
	OriginToolExport = "tool-export"
)

// Mappings: how the common event of a firmware record was found.
const (
	// MappingTable: a line of a mapping table names the message.
	MappingTable = "table"
	// MappingRegistry: the BMC's own registry text for the message was
	// read by the wording rules.
	MappingRegistry = "bmc-registry"
	// MappingWording: the text of the entry was read by the wording rules.
	MappingWording = "wording"
	// MappingIPMI: the state of the IPMI memory sensor.
	MappingIPMI = "ipmi"
	// MappingCPER: the memory section of CPER the entry carries.
	MappingCPER = "cper"
	// MappingNone: nothing explained the record.
	MappingNone = "none"
)

// FirmwareRecord is what the BIOS or a BMC said about memory, kept as the
// vendor said it beside the event or action made from it. A later version
// can classify the record again from it, and a reader can check the
// mapping. A record that makes neither an event nor an action (a module
// detected, the RAS mode in effect) is kept on its own, with its raw text.
type FirmwareRecord struct {
	ID       int64  `json:"id,omitzero"`
	Host     string `json:"host,omitzero"`
	ScanID   int64  `json:"scan_id,omitzero"`
	EventID  int64  `json:"event_id,omitzero"`
	ActionID int64  `json:"action_id,omitzero"`
	// At is the time of the record.
	At time.Time `json:"at"`

	// Common is the common event (a name of package fwevent), Flags
	// narrow it, Clears names the event a "cleared" record ends.
	Common string   `json:"event"`
	Flags  []string `json:"flags,omitzero"`
	Clears string   `json:"clears,omitzero"`

	// Vendor is the vendor or standard of the table that matched, or of
	// the system when none did.
	Vendor string `json:"vendor,omitzero"`
	// VendorID is the message key as the vendor gives it: a message ID, an
	// event code, a class and code.
	VendorID        string `json:"vendor_id,omitzero"`
	Registry        string `json:"registry,omitzero"`
	RegistryVersion string `json:"registry_version,omitzero"`
	// VendorSeverity is the vendor's own severity word. It decides
	// nothing: the common event does.
	VendorSeverity string `json:"vendor_severity,omitzero"`
	// Origin is one of the Origin constants.
	Origin string `json:"origin,omitzero"`
	// Log is the log inside the BMC (Sel, Lclog, IML, EventLog) or the
	// tool of an export.
	Log string `json:"log,omitzero"`
	// Location is the place as the vendor names it, unchanged.
	Location string   `json:"location,omitzero"`
	Args     []string `json:"args,omitzero"`
	// Text is the message text, cut at MaxFirmwareText bytes.
	Text string `json:"text,omitzero"`
	// Repeat is how many occurrences the entry folds; one otherwise.
	Repeat int `json:"repeat,omitzero"`
	// LastAt is the time of the latest occurrence, when the entry gives
	// one.
	LastAt time.Time `json:"last_at,omitzero"`
	// Resolved: the BMC marks the entry as resolved or repaired.
	Resolved bool `json:"resolved,omitzero"`
	// EntryKey identifies the entry inside its log: the same entry read
	// twice has the same key, whatever else changed.
	EntryKey string `json:"entry_key,omitzero"`

	// Mapping is one of the Mapping constants, MapRef the table, version
	// and line ("dell@1:MEM0804"), Basis how far that table was checked
	// (capture, sample, registry, documents).
	Mapping string `json:"mapping,omitzero"`
	MapRef  string `json:"map_ref,omitzero"`
	Basis   string `json:"basis,omitzero"`

	// Source and RawRef name the reader and the original record.
	Source string `json:"source,omitzero"`
	RawRef string `json:"raw_ref,omitzero"`
	// Fingerprint and Raw are set on a record that has neither an event
	// nor an action: what identifies it, and its text.
	Fingerprint string `json:"fingerprint,omitzero"`
	Raw         string `json:"raw,omitzero"`

	// DIMMUID and Slot are those of the event or action of the record, as
	// the ledger read them back; they are not stored with the record.
	DIMMUID string `json:"dimm_uid,omitzero"`
	Slot    string `json:"slot,omitzero"`
}

// MaxFirmwareText is how much of a message text a firmware record keeps.
const MaxFirmwareText = 512

// UnreadRecord is a record a reader could not use and whose text names
// memory. Its text is kept, cut at MaxUnreadText bytes, so that a later
// version or a person can read it.
type UnreadRecord struct {
	ID       int64  `json:"id,omitzero"`
	SourceID int64  `json:"source_id,omitzero"`
	Ref      string `json:"ref"`
	Reason   string `json:"reason"`
	Text     string `json:"text,omitzero"`
}

// Limits of what is kept of records that were not understood.
const (
	// MaxUnreadText is how much of the text of one unread record is kept.
	MaxUnreadText = 4 << 10
	// MaxUnreadKept is how many unread records of one source are kept.
	MaxUnreadKept = 50
)

// RegistrySeen is a message registry a BMC published, with how much of
// it this version can classify.
type RegistrySeen struct {
	ID       int64     `json:"id,omitzero"`
	Host     string    `json:"host,omitzero"`
	ScanID   int64     `json:"scan_id,omitzero"`
	SeenAt   time.Time `json:"seen_at"`
	Prefix   string    `json:"prefix"`
	Version  string    `json:"version,omitzero"`
	Owner    string    `json:"owner,omitzero"`
	Language string    `json:"language,omitzero"`
	// Messages is the number of messages of the registry, Memory the
	// number about memory, Mapped those a table names, Wording those the
	// wording rules class.
	Messages int `json:"messages"`
	Memory   int `json:"memory"`
	Mapped   int `json:"mapped"`
	Wording  int `json:"wording"`
	// Unmapped are the keys of memory messages neither a table nor the
	// wording rules class, at most MaxRegistryUnmapped.
	Unmapped []string `json:"unmapped,omitzero"`
	// Source names the dump the registry was read from.
	Source string `json:"source,omitzero"`
}

// MaxRegistryUnmapped is how many unmapped keys a registry row keeps.
const MaxRegistryUnmapped = 200
