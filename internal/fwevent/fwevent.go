// Package fwevent is the vocabulary MemorY reads firmware records in. A
// BMC or a BIOS of any vendor says what happened to memory in its own
// words: a message ID of a registry, a SEL record, a sentence of a log. A
// reader turns each such record into one of the common events of this
// package, and Ledger turns the common event into what the ledger keeps:
// an error event, an informational event, an action, a mark, or a firmware
// record alone.
//
// A name appears in the ledger, in JSON and in the event stream. It does
// not change once released.
package fwevent

import "slices"

// Name is a common event.
type Name string

// The common events.
const (
	// CE: one corrected error, or as many as the record counts.
	CE Name = "ce"
	// CEThreshold: the firmware's own limit or rate of corrected errors
	// was passed. It counts one error; the number the message gives is
	// the vendor's limit and stays in the firmware record.
	CEThreshold Name = "ce-threshold"
	// CELogLimit: firmware stopped logging corrected errors.
	CELogLimit Name = "ce-log-limit"
	// UE: an uncorrected error. Flag Contained: the system kept running.
	// Flag Fatal: the context was lost.
	UE Name = "ue"
	// ScrubCE and ScrubUE: an error the patrol scrubber found.
	ScrubCE Name = "scrub-ce"
	ScrubUE Name = "scrub-ue"
	// PredictedFailure: firmware expects the module to fail.
	PredictedFailure Name = "predicted-failure"
	// Disabled: the module, a rank or a channel was taken out of use or
	// mapped out.
	Disabled Name = "disabled"
	// PPRScheduled: a post package repair waits for the next boot.
	PPRScheduled Name = "ppr-scheduled"
	// PPRDone: the repair ran and succeeded.
	PPRDone Name = "ppr-done"
	// PPRFailed: the repair failed, or no spare row was left.
	PPRFailed Name = "ppr-failed"
	// Sparing: rank sparing, or sparing of a kind the record does not
	// name, took over.
	Sparing Name = "sparing"
	// DeviceSparing: ADDDC, SDDC, bank sparing or partial cache line
	// sparing engaged.
	DeviceSparing Name = "device-sparing"
	// SpareCopyStarted: the copy to the spare began.
	SpareCopyStarted Name = "spare-copy-started"
	// SparingFailed: sparing or the copy failed, or no spare is left.
	SparingFailed Name = "sparing-failed"
	// MirrorFailover: the memory mirror took over from a module.
	MirrorFailover Name = "mirror-failover"
	// MirrorLost: redundancy was lost and no module is named, or the
	// failover failed.
	MirrorLost Name = "mirror-lost"
	// TrainingFailure: the module failed training, the memory test or its
	// initialisation at boot.
	TrainingFailure Name = "training-failure"
	// ConfigError: population or configuration the firmware rejects.
	ConfigError Name = "config-error"
	// Replaced: firmware saw another module in the slot.
	Replaced Name = "replaced"
	// LogCleared: the log was cleared.
	LogCleared Name = "log-cleared"
	// LogFull: the log is full and takes no more records.
	LogFull Name = "log-full"
	// Thermal: throttled, or over temperature.
	Thermal Name = "thermal"
	// Presence: a module was added, removed or detected.
	Presence Name = "presence"
	// ConfigState: a configuration without an error, such as the RAS mode
	// in effect or a module that is the spare.
	ConfigState Name = "config-state"
	// Cleared: a state ended. The record names the event it ends when the
	// table knows it.
	Cleared Name = "cleared"
	// Unclassified: about memory, in words no table and no rule explains.
	Unclassified Name = "unclassified"
)

// Family groups the common events for screens and counts.
type Family string

// The families.
const (
	FamilyCorrected   Family = "corrected"
	FamilyUncorrected Family = "uncorrected"
	FamilyScrub       Family = "scrub"
	FamilyPrediction  Family = "prediction"
	FamilyOutOfUse    Family = "out-of-use"
	FamilyRepair      Family = "repair"
	FamilySparing     Family = "sparing"
	FamilyMirror      Family = "mirror"
	FamilyBoot        Family = "boot"
	FamilyReplacement Family = "replacement"
	FamilyLog         Family = "log"
	FamilyOther       Family = "other"
)

// names lists the common events in the order of section 4.1 of the
// design, with their families.
var names = []struct {
	name   Name
	family Family
}{
	{CE, FamilyCorrected}, {CEThreshold, FamilyCorrected}, {CELogLimit, FamilyCorrected},
	{UE, FamilyUncorrected},
	{ScrubCE, FamilyScrub}, {ScrubUE, FamilyScrub},
	{PredictedFailure, FamilyPrediction},
	{Disabled, FamilyOutOfUse},
	{PPRScheduled, FamilyRepair}, {PPRDone, FamilyRepair}, {PPRFailed, FamilyRepair},
	{Sparing, FamilySparing}, {DeviceSparing, FamilySparing}, {SpareCopyStarted, FamilySparing}, {SparingFailed, FamilySparing},
	{MirrorFailover, FamilyMirror}, {MirrorLost, FamilyMirror},
	{TrainingFailure, FamilyBoot}, {ConfigError, FamilyBoot},
	{Replaced, FamilyReplacement},
	{LogCleared, FamilyLog}, {LogFull, FamilyLog},
	{Thermal, FamilyOther}, {Presence, FamilyOther}, {ConfigState, FamilyOther}, {Cleared, FamilyOther},
	{Unclassified, FamilyOther},
}

// Names returns every common event in a fixed order.
func Names() []Name {
	out := make([]Name, len(names))
	for i, n := range names {
		out[i] = n.name
	}
	return out
}

// Valid reports whether n is a common event.
func (n Name) Valid() bool { return n.Family() != "" }

// Family returns the family of n, or "" for a name that is not a common
// event.
func (n Name) Family() Family {
	for _, x := range names {
		if x.name == n {
			return x.family
		}
	}
	return ""
}

// Flag narrows a common event without making a new one.
type Flag string

// The flags.
const (
	// Contained: the system kept running after an uncorrected error.
	Contained Flag = "contained"
	// Fatal: the context was lost.
	Fatal Flag = "fatal"
	// Soft and Hard: a soft or a hard post package repair.
	Soft Flag = "soft"
	Hard Flag = "hard"
	// Runtime and Boot: the step was taken while the system ran, or at
	// boot.
	Runtime Flag = "runtime"
	Boot    Flag = "boot"
	// ADDDC, SDDC, Rank, Bank and PCLS name the kind of sparing.
	ADDDC Flag = "adddc"
	SDDC  Flag = "sddc"
	Rank  Flag = "rank"
	Bank  Flag = "bank"
	PCLS  Flag = "pcls"
	// PreviousBoot: the record reports what happened in the boot before.
	PreviousBoot Flag = "previous-boot"
)

var flags = []Flag{Contained, Fatal, Soft, Hard, Runtime, Boot, ADDDC, SDDC, Rank, Bank, PCLS, PreviousBoot}

// Flags returns every flag in a fixed order.
func Flags() []Flag { return slices.Clone(flags) }

// Valid reports whether f is a defined flag.
func (f Flag) Valid() bool { return slices.Contains(flags, f) }

// deviceSparing reports whether the flags name a kind of sparing that
// takes a device, a bank or a part of a cache line out of use.
func deviceSparing(fs []Flag) bool {
	for _, f := range fs {
		switch f {
		case ADDDC, SDDC, Bank, PCLS:
			return true
		}
	}
	return false
}
