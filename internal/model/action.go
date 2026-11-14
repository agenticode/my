package model

import "time"

// ActionKind is a repair or isolation step taken on the host, by the kernel,
// the firmware, a daemon or a person. MemorY records actions; it never takes
// them.
type ActionKind string

const (
	ActionPageOffline ActionKind = "page-offline"
	ActionSPPR        ActionKind = "sppr" // soft post package repair
	ActionHPPR        ActionKind = "hppr" // hard post package repair
	ActionSparing     ActionKind = "sparing"
	ActionRowRemap    ActionKind = "row-remap"
	ActionReplacement ActionKind = "replacement"
	ActionReseat      ActionKind = "reseat"
)

// Valid reports whether k is a defined action kind.
func (k ActionKind) Valid() bool {
	switch k {
	case ActionPageOffline, ActionSPPR, ActionHPPR, ActionSparing, ActionRowRemap, ActionReplacement, ActionReseat:
		return true
	}
	return false
}

// ActionResult is the outcome of an action as the source reported it.
type ActionResult string

const (
	ResultOK      ActionResult = "ok"
	ResultFailed  ActionResult = "failed"
	ResultPending ActionResult = "pending"
	ResultUnknown ActionResult = "unknown"
)

// Action is one entry of the action history.
type Action struct {
	ID          int64       `json:"id,omitzero"`
	Host        string      `json:"host"`
	At          time.Time   `json:"at"`
	TimeQuality TimeQuality `json:"time_quality"`
	Kind        ActionKind  `json:"kind"`
	// Method narrows the kind: "soft" or "hard" for page offlining, and the
	// actor when known, for example "kernel-cec" or "bios".
	Method string       `json:"method,omitzero"`
	Result ActionResult `json:"result"`

	// Target of the action. A page offline names a physical address; a
	// repair names a location inside a DIMM.
	OsPhysicalAddress Opt[uint64] `json:"OsPhysicalAddress,omitzero"`
	Rank              Opt[int]    `json:"Rank,omitzero"`
	DeviceId          Opt[int]    `json:"DeviceId,omitzero"`
	BankGroup         Opt[int]    `json:"BankGroup,omitzero"`
	Bank              Opt[int]    `json:"Bank,omitzero"`
	Row               Opt[int]    `json:"Row,omitzero"`
	Label             string      `json:"label,omitzero"`
	Slot              string      `json:"slot,omitzero"`
	DIMMUID           string      `json:"dimm_uid,omitzero"`
	// Count is the number of units acted on (pages, rows). Zero means one.
	Count int `json:"count,omitzero"`

	BootID string `json:"boot_id,omitzero"`
	// Detail is the source's own wording, for example the page type and
	// result of a memory failure record.
	Detail string `json:"detail,omitzero"`

	Source      string `json:"source"`
	RawRef      string `json:"raw_ref"`
	Raw         string `json:"raw,omitzero"`
	Fingerprint string `json:"fingerprint"`
	ScanID      int64  `json:"scan_id,omitzero"`
}
