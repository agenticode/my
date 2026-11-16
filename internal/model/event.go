// Package model defines the records MemorY keeps in its ledger.
//
// The error event uses the field names of the OCP Fleet Memory Fault
// Management (FMFM) runtime error log, spelled exactly as in that schema
// (ErrorDT, SocketId, ChannelSlotId and so on), so that a ledger row and an
// FMFM record describe the same thing with the same words. Fields MemorY adds
// are lower-case in the ledger and in JSON.
package model

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// Severity is the FMFM ErrorSeverity vocabulary.
type Severity string

const (
	SevInformational Severity = "Informational"
	SevCE            Severity = "CE"          // corrected
	SevRecoverable   Severity = "Recoverable" // uncorrected, the system kept running
	SevUE            Severity = "UE"          // uncorrected
	SevFatal         Severity = "Fatal"       // uncorrected, context lost or machine check panic
)

// Valid reports whether s is one of the defined severities.
func (s Severity) Valid() bool {
	switch s {
	case SevInformational, SevCE, SevRecoverable, SevUE, SevFatal:
		return true
	}
	return false
}

// Uncorrected reports whether the error left data uncorrected. Recoverable,
// UE and Fatal all count: the DIMM returned wrong data that ECC could not fix.
func (s Severity) Uncorrected() bool {
	return s == SevRecoverable || s == SevUE || s == SevFatal
}

// Class groups severities for counting: "CE", "UE" or "" for informational
// records, which are never counted as errors.
func (s Severity) Class() string {
	switch {
	case s == SevCE:
		return "CE"
	case s.Uncorrected():
		return "UE"
	}
	return ""
}

// ScrubType is the FMFM ErrorScrubType vocabulary. The empty value means the
// source did not say how the error was found.
type ScrubType string

const (
	ScrubDemand ScrubType = "Demand" // found by a normal read or write
	ScrubPatrol ScrubType = "Patrol" // found by the patrol scrubber
	ScrubOther  ScrubType = "Other"
)

// Valid reports whether t is empty or one of the defined scrub types.
func (t ScrubType) Valid() bool {
	switch t {
	case "", ScrubDemand, ScrubPatrol, ScrubOther:
		return true
	}
	return false
}

// Grade is the observation grade of a record: how much of the error location
// the platform exposed. The grade limits which conclusions may be drawn.
type Grade string

const (
	// GradeA: bank, row and column are known. Fault mode classification is possible.
	GradeA Grade = "A"
	// GradeB: a physical address or a rank is known, row and column are not.
	// Page-level repetition and trends are possible.
	GradeB Grade = "B"
	// GradeC: only a count is known. Trends and thresholds are possible.
	GradeC Grade = "C"
)

// Valid reports whether g is A, B or C.
func (g Grade) Valid() bool { return g == GradeA || g == GradeB || g == GradeC }

// Better reports whether g carries more location detail than other. An empty
// grade is worse than any grade.
func (g Grade) Better(other Grade) bool {
	rank := func(x Grade) int {
		switch x {
		case GradeA:
			return 3
		case GradeB:
			return 2
		case GradeC:
			return 1
		}
		return 0
	}
	return rank(g) > rank(other)
}

// TimeQuality says how far ErrorDT can be trusted.
type TimeQuality string

const (
	// TimeExact: the source recorded a wall-clock time.
	TimeExact TimeQuality = "exact"
	// TimeEstimated: the source recorded seconds since boot and the boot time
	// was estimated from a reference such as the capture time of the log.
	TimeEstimated TimeQuality = "estimated"
	// TimeRelative: only seconds since boot are known. ErrorDT then counts
	// from the Unix epoch and must be shown as an offset, never as a date.
	TimeRelative TimeQuality = "relative"
)

// EventKind separates located error records from counter deltas.
type EventKind string

const (
	// KindError is one error report from a source that logs each error.
	KindError EventKind = "error"
	// KindCounter is the growth of a per-DIMM error counter between two
	// readings. It carries a count and a time window, and no location.
	KindCounter EventKind = "counter"
)

// DIMM link values: how an event was tied to a DIMM identity.
const (
	// LinkObserved: the DIMM was seen in that slot at a time that covers the event.
	LinkObserved = "observed"
	// LinkAssumed: the DIMM was seen in that slot, at a time that does not
	// cover the event, and nothing shows that the slot held another DIMM.
	LinkAssumed = "assumed"
	// LinkAmbiguous: the slot held different DIMMs before and after the
	// event and the swap time is unknown. The event has no DIMM identity.
	LinkAmbiguous = "ambiguous"
	// LinkSlot: no DIMM identity was available for the slot. The event is
	// tied to a placeholder identity derived from host and slot.
	LinkSlot = "slot"
)

// Event is one row of the ledger: an error report, or a counter delta.
//
// Location fields are optional because platforms expose different amounts of
// detail. A field that is not set was not reported; it is never guessed.
type Event struct {
	ID   int64     `json:"id,omitzero"`
	Host string    `json:"HostID"`
	Kind EventKind `json:"kind"`

	// FMFM runtime error log fields.

	// ErrorDT is when the error was detected, in UTC.
	ErrorDT        time.Time `json:"ErrorDT"`
	ErrorSeverity  Severity  `json:"ErrorSeverity"`
	ErrorScrubType ScrubType `json:"ErrorScrubType,omitzero"`
	// SocketId is the processor socket.
	SocketId Opt[int] `json:"SocketId,omitzero"`
	// ChannelId is the memory channel as the memory controller numbers it.
	// On platforms with several controllers per socket the controller index
	// is in IMC, and ChannelId counts within that controller.
	ChannelId Opt[int] `json:"ChannelId,omitzero"`
	// ChannelSlotId is the memory controller's view of the DIMM on the channel.
	ChannelSlotId Opt[int] `json:"ChannelSlotId,omitzero"`
	// SubChannel is the DDR5 sub-channel.
	SubChannel Opt[int] `json:"SubChannel,omitzero"`
	// Rank is the rank as the source numbers it (see Decoder for the scheme).
	Rank      Opt[int] `json:"Rank,omitzero"`
	DeviceId  Opt[int] `json:"DeviceId,omitzero"`
	BankGroup Opt[int] `json:"BankGroup,omitzero"`
	Bank      Opt[int] `json:"Bank,omitzero"`
	Row       Opt[int] `json:"Row,omitzero"`
	Column    Opt[int] `json:"Column,omitzero"`
	// ErrorVector identifies the failing bits, when the source reports
	// them, in a form that names what it is: "bit:<n>" is a bit position
	// within the data word (CPER), "dq:<hex mask>" a mask of the data lines
	// of the device.
	ErrorVector string `json:"ErrorVector,omitzero"`
	// OsPhysicalAddress is the system physical address of the error.
	OsPhysicalAddress Opt[uint64] `json:"OsPhysicalAddress,omitzero"`

	// Fields added by MemorY.

	// Source names the adapter and record class, for example
	// "rasdaemon/mc_event" or "klog/edac".
	Source string `json:"source"`
	// RawRef points at the original record: file and row or line.
	RawRef string `json:"raw_ref"`
	Grade  Grade  `json:"grade"`
	// BootID is the boot during which the error happened, when known.
	BootID string `json:"boot_id,omitzero"`
	// DIMMUID is the identity of the DIMM the error is attributed to.
	DIMMUID string `json:"dimm_uid,omitzero"`
	// DIMMLink says how DIMMUID was established (Link* constants).
	DIMMLink string `json:"dimm_link,omitzero"`

	// Count is the number of errors this record stands for.
	Count int `json:"count"`
	// Covered is set on counter records: the part of Count that located
	// error records in the same window already account for.
	Covered int `json:"covered,omitzero"`
	// DupOf is the ID of the record that reports the same error through a
	// source with more detail. Records with DupOf set are kept for
	// traceability and left out of totals.
	DupOf int64 `json:"dup_of,omitzero"`
	// DupOfFingerprint lets an adapter mark a record as a duplicate of
	// another record it emitted (same error seen through two tables or two
	// log lines). The ledger resolves it to DupOf.
	DupOfFingerprint string `json:"-"`

	TimeQuality TimeQuality `json:"time_quality"`
	// WindowStart is set on counter records: the start of the counting window.
	WindowStart time.Time `json:"window_start,omitzero"`
	// UTCOffset is the offset of the host's local time in seconds east of
	// UTC, when the source recorded local time.
	UTCOffset Opt[int] `json:"utc_offset,omitzero"`

	// IMC is the memory controller within the socket.
	IMC Opt[int] `json:"imc,omitzero"`
	// EdacMC is the EDAC memory controller index (mcN in sysfs).
	EdacMC Opt[int] `json:"edac_mc,omitzero"`
	// EdacLocation is the EDAC layer position, for example "channel:0 slot:1".
	EdacLocation string `json:"edac_location,omitzero"`
	// SMBIOSHandle is the SMBIOS type 17 handle named by a firmware record.
	SMBIOSHandle Opt[int] `json:"smbios_handle,omitzero"`
	// Label is the DIMM label exactly as the source printed it.
	Label string `json:"label,omitzero"`
	// Slot is the slot name on the host the event was resolved to.
	Slot string `json:"slot,omitzero"`

	// Syndrome is the ECC syndrome as reported.
	Syndrome Opt[uint64] `json:"syndrome,omitzero"`
	// ErrorType is the source's own description, for example
	// "memory read error" or "single-bit ECC".
	ErrorType string `json:"error_type,omitzero"`
	// Decoder names the format the location fields were decoded from, for
	// example "edac/skx-adxl" or "cper". It tells a reader how to check them.
	Decoder string `json:"decoder,omitzero"`

	// Carried says that hardware or firmware kept the record over a reset
	// and a later boot logged it: the contents of a machine check bank, a
	// boot error record of the firmware. BootID is then the boot before
	// the one that read it, ReadBootID that boot, and ErrorDT the latest
	// time the error can have happened. The time is estimated.
	Carried    bool   `json:"carried,omitzero"`
	ReadBootID string `json:"read_boot_id,omitzero"`
	// NotBefore is the earliest time the error can have happened, when
	// known: the start of the boot it belongs to.
	NotBefore time.Time `json:"not_before,omitzero"`

	// Raw is the original record text.
	Raw string `json:"raw,omitzero"`
	// Fingerprint identifies the original record so that importing the same
	// source twice does not add it twice.
	Fingerprint string `json:"fingerprint"`
	ScanID      int64  `json:"scan_id,omitzero"`
}

// ComputeGrade returns the observation grade the location fields of e justify.
func (e *Event) ComputeGrade() Grade {
	if e.Kind == KindCounter {
		return GradeC
	}
	if e.Row.Valid && e.Column.Valid && (e.Bank.Valid || e.BankGroup.Valid) {
		return GradeA
	}
	if e.OsPhysicalAddress.Valid || e.Rank.Valid {
		return GradeB
	}
	return GradeC
}

// Errors returns the number of errors the record adds to totals: its count
// minus the part already covered by located records. Duplicates add nothing.
func (e *Event) Errors() int {
	if e.DupOf != 0 || e.ErrorSeverity.Class() == "" {
		return 0
	}
	if n := e.Count - e.Covered; n > 0 {
		return n
	}
	return 0
}

// Fingerprint hashes the parts that identify an original record. Parts are
// length-prefixed so that ("ab","c") and ("a","bc") differ.
func Fingerprint(parts ...string) string {
	h := sha256.New()
	var n [4]byte
	for _, p := range parts {
		l := len(p)
		n[0], n[1], n[2], n[3] = byte(l>>24), byte(l>>16), byte(l>>8), byte(l)
		h.Write(n[:])
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// NormalizeLabel folds a slot or DIMM label for comparison: upper case with
// runs of white space collapsed.
func NormalizeLabel(s string) string {
	return strings.ToUpper(strings.Join(strings.Fields(s), " "))
}
