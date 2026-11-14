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
	// Estimated is set when the data does not fix the mode: it was inferred
	// from physical addresses (grade B), or the platform does not report the
	// device and the mode is the smallest extent the data allows.
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

// VerdictLabel is what a verdict is called wherever it is shown: a finding.
// The thresholds behind the grades have not been compared with failure
// analysis results yet, so a grade is not a recommendation.
const VerdictLabel = "finding"

// Statement is one sentence of a finding. It is kept as a code with
// arguments, so that it can be shown in the reader's language, and with
// its English text for readers that do not have the message catalog.
type Statement struct {
	// Code names the sentence in the message catalog.
	Code string `json:"code"`
	// Args are the values the sentence mentions: numbers and strings.
	Args map[string]any `json:"args,omitzero"`
	// Text is the sentence in English.
	Text string `json:"text"`
}

// Kinds of ledger record a RecordRef can point at.
const (
	RecordEvent  = "event"
	RecordAction = "action"
	RecordGPU    = "gpu"
)

// RecordRef points at a ledger record and at the original record behind it.
type RecordRef struct {
	Kind string `json:"kind"`
	ID   int64  `json:"id"`
	// RawRef is the raw_ref of the ledger record: file and row or line of
	// the original record.
	RawRef string `json:"raw_ref,omitzero"`
}

// Evidence is one statement of a finding with the rule that made it and the
// records it rests on.
type Evidence struct {
	// Rule is the ID of the rule, for example "R-UE-01".
	Rule string `json:"rule"`
	Statement
	// Records are the ledger records the statement rests on. When there
	// are many, the first and the last of them are listed and Total says
	// how many there are.
	Records []RecordRef `json:"records,omitzero"`
	Total   int         `json:"total,omitzero"`
}

// Effects of a check on a finding.
const (
	// EffectSupports: the check speaks for the finding.
	EffectSupports = "supports"
	// EffectWeakens: the check speaks against the finding.
	EffectWeakens = "weakens"
	// EffectOpen: the check could not be made. The statement says what
	// would settle it.
	EffectOpen = "open"
)

// Check is the outcome of one test of a finding against another
// explanation: what speaks for it, what speaks against it, and what was
// not tested and would overturn it.
type Check struct {
	Statement
	Effect string `json:"effect"`
}

// Factor is one step in the confidence of a finding.
type Factor struct {
	Statement
	// Delta is what the step adds to the confidence or takes from it. The
	// first factor carries the starting value.
	Delta int `json:"delta"`
}

// Verdict is the finding for one DIMM, or for the memory of one GPU: a
// grade, the rules that fired, and what speaks for and against it.
type Verdict struct {
	ID      int64  `json:"id,omitzero"`
	Host    string `json:"host"`
	DIMMUID string `json:"dimm_uid,omitzero"`
	Slot    string `json:"slot,omitzero"`
	// GPU is set on findings about GPU memory: the serial number of the
	// GPU, or its UUID when the serial number is unknown.
	GPU string `json:"gpu,omitzero"`

	Grade VerdictGrade `json:"grade"`
	// Label is VerdictLabel.
	Label string `json:"label"`
	// Rules are the IDs of the rules that fired, the rules that set the
	// grade first.
	Rules    []string   `json:"rules"`
	Evidence []Evidence `json:"evidence,omitzero"`
	// CounterEvidence lists the checks of the finding against other
	// explanations.
	CounterEvidence []Check `json:"counter_evidence,omitzero"`
	// Missing lists data the finding would need and did not have.
	Missing    []Statement `json:"missing,omitzero"`
	NextAction Statement   `json:"next_action,omitzero"`
	// Confidence is a number from 0 to 100, and ConfidenceFactors the
	// steps that led to it.
	Confidence        int       `json:"confidence"`
	ConfidenceFactors []Factor  `json:"confidence_factors,omitzero"`
	EngineVersion     string    `json:"engine_version"`
	ComputedAt        time.Time `json:"computed_at"`
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
