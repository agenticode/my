package model

import "time"

// ActionKind is a repair or isolation step taken on the host, by the kernel,
// the firmware, a daemon or a person, or a mark firmware put on a module.
// MemorY records actions; it never takes them.
type ActionKind string

const (
	ActionPageOffline ActionKind = "page-offline"
	ActionSPPR        ActionKind = "sppr" // soft post package repair
	ActionHPPR        ActionKind = "hppr" // hard post package repair
	ActionSparing     ActionKind = "sparing"
	ActionRowRemap    ActionKind = "row-remap"
	ActionReplacement ActionKind = "replacement"
	ActionReseat      ActionKind = "reseat"
	// ActionDisable: the BIOS or the BMC took the module, or a rank or
	// channel that holds it, out of use.
	ActionDisable ActionKind = "disable"
	// ActionMarkFailed: the BIOS or the BMC reports the module as failed
	// and left it in use.
	ActionMarkFailed ActionKind = "mark-failed"
	// ActionPPR: a post package repair the record does not call soft or
	// hard.
	ActionPPR ActionKind = "ppr"
	// ActionDeviceSparing: ADDDC, SDDC, bank sparing or partial cache line
	// sparing took a device, a bank or a part of a cache line out of use.
	ActionDeviceSparing ActionKind = "device-sparing"
	// ActionPredictedFailure: firmware expects the module to fail. A mark.
	ActionPredictedFailure ActionKind = "predicted-failure"
	// ActionMirrorFailover: the memory mirror took over from the module.
	ActionMirrorFailover ActionKind = "mirror-failover"
	// ActionTrainingFailure: the module failed training, the memory test or
	// its initialisation at boot. A mark.
	ActionTrainingFailure ActionKind = "training-failure"
)

// Valid reports whether k is a defined action kind.
func (k ActionKind) Valid() bool {
	switch k {
	case ActionPageOffline, ActionSPPR, ActionHPPR, ActionSparing, ActionRowRemap, ActionReplacement, ActionReseat,
		ActionDisable, ActionMarkFailed, ActionPPR, ActionDeviceSparing, ActionPredictedFailure, ActionMirrorFailover,
		ActionTrainingFailure:
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

	// DupOf is the ID of the action that reports the same step through
	// another log or reader of the same BMC. Such an action is kept for
	// traceability and left out of what views and rules read.
	DupOf int64 `json:"dup_of,omitzero"`
	// WithdrawnBy is the ID of the informational event that took a mark
	// back: the module was enabled again, the prediction was cleared. A
	// withdrawn mark is history; it no longer holds.
	WithdrawnBy int64 `json:"withdrawn_by,omitzero"`

	Source      string `json:"source"`
	RawRef      string `json:"raw_ref"`
	Raw         string `json:"raw,omitzero"`
	Fingerprint string `json:"fingerprint"`
	ScanID      int64  `json:"scan_id,omitzero"`

	// Firmware is what the BIOS or the BMC said, when the action was made
	// from a firmware record. The ledger keeps it in fw_record.
	Firmware *FirmwareRecord `json:"firmware,omitzero"`
}
