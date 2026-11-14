package model

import "time"

// FaultMode is the Level 1 fault mode of a cluster of events, following the
// classification the FMFM analyzer output uses.
type FaultMode string

const (
	ModeSingleBit    FaultMode = "single-bit"
	ModeRow          FaultMode = "row"
	ModeMultiRow     FaultMode = "multi-row"
	ModeColumn       FaultMode = "column"
	ModeBank         FaultMode = "bank"
	ModeDevice       FaultMode = "device"
	ModeDIMM         FaultMode = "dimm"
	ModeSystemIssue  FaultMode = "system-issue"
	ModeUndetermined FaultMode = "undetermined" // the observation grade does not allow a mode
)

// FaultModes lists the Level 1 modes in order of growing extent.
var FaultModes = []FaultMode{
	ModeSingleBit, ModeRow, ModeMultiRow, ModeColumn, ModeBank, ModeDevice, ModeDIMM, ModeSystemIssue,
}

// Valid reports whether m is a defined fault mode.
func (m FaultMode) Valid() bool {
	if m == ModeUndetermined {
		return true
	}
	for _, x := range FaultModes {
		if m == x {
			return true
		}
	}
	return false
}

// Fault is a cluster of events that share a cause.
type Fault struct {
	ID      int64  `json:"id,omitzero"`
	Host    string `json:"host"`
	DIMMUID string `json:"dimm_uid,omitzero"`
	Slot    string `json:"slot,omitzero"`

	Mode FaultMode `json:"mode"`
	// Estimated is set when the mode was inferred from grade B data.
	Estimated bool  `json:"estimated,omitzero"`
	Grade     Grade `json:"grade"`

	Rank      Opt[int] `json:"Rank,omitzero"`
	DeviceId  Opt[int] `json:"DeviceId,omitzero"`
	BankGroup Opt[int] `json:"BankGroup,omitzero"`
	Bank      Opt[int] `json:"Bank,omitzero"`
	RowMin    Opt[int] `json:"row_min,omitzero"`
	RowMax    Opt[int] `json:"row_max,omitzero"`
	ColumnMin Opt[int] `json:"column_min,omitzero"`
	ColumnMax Opt[int] `json:"column_max,omitzero"`
	// AddressMin and AddressMax bound the physical addresses involved.
	AddressMin Opt[uint64] `json:"address_min,omitzero"`
	AddressMax Opt[uint64] `json:"address_max,omitzero"`

	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	// EventCount is the number of ledger records, ErrorCount the number of
	// errors they stand for.
	EventCount int `json:"event_count"`
	ErrorCount int `json:"error_count"`
	// Confidence is a number from 0 to 100.
	Confidence    int    `json:"confidence"`
	EngineVersion string `json:"engine_version"`

	// EventIDs are the member events.
	EventIDs []int64 `json:"event_ids,omitzero"`
}

// VerdictGrade is the finding for a DIMM.
type VerdictGrade string

const (
	VerdictOK                  VerdictGrade = "ok"
	VerdictObserve             VerdictGrade = "observe"
	VerdictConsiderReplacement VerdictGrade = "consider-replacement"
	VerdictReplaceNow          VerdictGrade = "replace-now"
	// VerdictHoldReplacement means a system-level cause is suspected and the
	// DIMM should not be pulled before that is checked. It is shown before
	// the other grades.
	VerdictHoldReplacement VerdictGrade = "hold-replacement"
)

// ExitCode returns the process exit code that stands for the grade:
// 0 ok, 1 observe, 2 consider replacement, 3 replace now, 4 hold replacement.
func (g VerdictGrade) ExitCode() int {
	switch g {
	case VerdictObserve:
		return 1
	case VerdictConsiderReplacement:
		return 2
	case VerdictReplaceNow:
		return 3
	case VerdictHoldReplacement:
		return 4
	}
	return 0
}

// Valid reports whether g is a defined verdict grade.
func (g VerdictGrade) Valid() bool {
	switch g {
	case VerdictOK, VerdictObserve, VerdictConsiderReplacement, VerdictReplaceNow, VerdictHoldReplacement:
		return true
	}
	return false
}

// Evidence ties one statement of a verdict to the rule that produced it and
// the events it rests on.
type Evidence struct {
	Rule     string  `json:"rule"`
	Text     string  `json:"text"`
	EventIDs []int64 `json:"event_ids,omitzero"`
}

// Verdict is the finding for one DIMM: a grade, the rules that fired, and
// what speaks for and against it.
type Verdict struct {
	ID      int64  `json:"id,omitzero"`
	Host    string `json:"host"`
	DIMMUID string `json:"dimm_uid,omitzero"`
	Slot    string `json:"slot,omitzero"`

	Grade VerdictGrade `json:"grade"`
	// Rules are the IDs of the rules that fired, for example "R-UE-01".
	Rules    []string   `json:"rules"`
	Evidence []Evidence `json:"evidence,omitzero"`
	// CounterEvidence lists observations that weaken the finding or would
	// overturn it.
	CounterEvidence []string `json:"counter_evidence,omitzero"`
	// Missing lists data the finding would need and did not have.
	Missing    []string `json:"missing,omitzero"`
	NextAction string   `json:"next_action,omitzero"`
	// Confidence is a number from 0 to 100.
	Confidence    int       `json:"confidence"`
	EngineVersion string    `json:"engine_version"`
	ComputedAt    time.Time `json:"computed_at"`
}

// GPUMem is one observation of GPU memory health: a snapshot of the row
// remapping state, or one XID event.
type GPUMem struct {
	ID         int64     `json:"id,omitzero"`
	Host       string    `json:"host"`
	ObservedAt time.Time `json:"observed_at"`
	// Kind is "snapshot" or "xid".
	Kind string `json:"kind"`

	GPUSerial string   `json:"gpu_serial,omitzero"`
	GPUUUID   string   `json:"gpu_uuid,omitzero"`
	GPUIndex  Opt[int] `json:"gpu_index,omitzero"`
	PCIBus    string   `json:"pci_bus,omitzero"`
	Model     string   `json:"model,omitzero"`

	// XID is set on xid records.
	XID     Opt[int] `json:"xid,omitzero"`
	XIDText string   `json:"xid_text,omitzero"`

	// Row remapping counters and state, set on snapshots.
	RemapCorrectable   Opt[int]  `json:"remap_correctable,omitzero"`
	RemapUncorrectable Opt[int]  `json:"remap_uncorrectable,omitzero"`
	RemapPending       Opt[bool] `json:"remap_pending,omitzero"`
	RemapFailure       Opt[bool] `json:"remap_failure,omitzero"`
	// Bank remap availability histogram: how many banks still have the
	// given amount of spare rows.
	BankMax     Opt[int] `json:"bank_max,omitzero"`
	BankHigh    Opt[int] `json:"bank_high,omitzero"`
	BankPartial Opt[int] `json:"bank_partial,omitzero"`
	BankLow     Opt[int] `json:"bank_low,omitzero"`
	BankNone    Opt[int] `json:"bank_none,omitzero"`
	// Retired pages, on GPU models whose driver retires pages and has no
	// row remapping.
	RetiredSBE     Opt[int]  `json:"retired_sbe,omitzero"`
	RetiredDBE     Opt[int]  `json:"retired_dbe,omitzero"`
	RetiredPending Opt[bool] `json:"retired_pending,omitzero"`

	BootID      string `json:"boot_id,omitzero"`
	Source      string `json:"source"`
	RawRef      string `json:"raw_ref"`
	Raw         string `json:"raw,omitzero"`
	Fingerprint string `json:"fingerprint"`
}

// GPU memory record kinds.
const (
	GPUSnapshot = "snapshot"
	GPUXID      = "xid"
)
