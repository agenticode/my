package fwevent

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
)

// Record is a firmware record as a reader understood it: the common event,
// where and when, and what the vendor said.
type Record struct {
	Event  Name
	Flags  []Flag
	Clears Name

	At          time.Time
	TimeQuality model.TimeQuality
	// Count is the number of corrected errors a "ce" or "scrub-ce" record
	// counts. Zero means one.
	Count int

	// Labels are the modules the record names. A record that names
	// several is one error tied to none of them, and one mark or step per
	// module.
	Labels []string
	// Place by numbers, when the record gives them.
	SocketId, ChannelId, ChannelSlotId, Rank, DeviceId, BankGroup, Bank, Row, Column model.Opt[int]
	Address                                                                       model.Opt[uint64]
	// Decoder names the format the place fields were read from.
	Decoder string
	// Method is the actor of a step: "bios", "bmc", or the generator of a
	// SEL record.
	Method string
	// Text is what the record says in a few words: the error type of an
	// event, the detail of an action.
	Text string

	Source      string
	RawRef      string
	Raw         string
	Fingerprint string

	// Firmware is what the vendor said. Its Common, Flags and Clears are
	// set from the fields above.
	Firmware model.FirmwareRecord
}

// Out is what a firmware record becomes in the ledger.
type Out struct {
	Events  []*model.Event
	Actions []*model.Action
	// Alone is set for a record that makes neither an event nor an
	// action.
	Alone *model.FirmwareRecord
}

// Ledger turns a record into ledger records by the table of section 4.1
// of the design:
//
//	ce, ce-threshold                  error event, CE
//	ue                                error event, UE (Recoverable when contained, Fatal when fatal)
//	scrub-ce, scrub-ue                error event found by the patrol scrubber
//	ce-log-limit, mirror-lost,
//	config-error, thermal, cleared,
//	unclassified                      informational event
//	predicted-failure                 action predicted-failure (a mark)
//	disabled                          action disable
//	ppr-scheduled, -done, -failed     action sppr, hppr or ppr: pending, ok, failed
//	sparing, spare-copy-started       action sparing: ok, pending
//	device-sparing                    action device-sparing
//	sparing-failed                    action sparing or device-sparing, failed
//	mirror-failover                   action mirror-failover
//	training-failure                  action training-failure (a mark)
//	replaced                          action replacement, method bmc
//	presence, config-state,
//	log-cleared, log-full             a firmware record alone
//
// The vendor's severity decides nothing.
func (r *Record) Ledger() (Out, error) {
	if !r.Event.Valid() {
		return Out{}, fmt.Errorf("%q is not a common event", r.Event)
	}
	if r.Clears != "" && !r.Clears.Valid() {
		return Out{}, fmt.Errorf("%q is not a common event", r.Clears)
	}
	for _, f := range r.Flags {
		if !f.Valid() {
			return Out{}, fmt.Errorf("%q is not a flag", f)
		}
	}
	if r.Fingerprint == "" {
		return Out{}, errors.New("the record has no fingerprint")
	}
	quality := r.TimeQuality
	if quality == "" {
		quality = model.TimeExact
	}
	fw := r.Firmware
	fw.Common, fw.Clears = string(r.Event), string(r.Clears)
	fw.Flags = nil
	for _, f := range r.Flags {
		fw.Flags = append(fw.Flags, string(f))
	}
	if fw.At.IsZero() {
		fw.At = r.At
	}
	if fw.Source == "" {
		fw.Source = r.Source
	}
	if fw.RawRef == "" {
		fw.RawRef = r.RawRef
	}
	repeat := max(fw.Repeat, 1)

	var out Out
	switch r.Event {
	case Presence, ConfigState, LogCleared, LogFull:
		alone := fw
		alone.Fingerprint = model.Fingerprint(r.Fingerprint, "fw")
		alone.Raw = r.Raw
		out.Alone = &alone
		return out, nil
	}

	if sev, scrub, ok := r.severity(); ok {
		e := &model.Event{Kind: model.KindError, ErrorDT: r.At, TimeQuality: quality, ErrorSeverity: sev, ErrorScrubType: scrub,
			ErrorType: r.Text, Count: 1, SocketId: r.SocketId, ChannelId: r.ChannelId, ChannelSlotId: r.ChannelSlotId,
			Rank: r.Rank, DeviceId: r.DeviceId, BankGroup: r.BankGroup, Bank: r.Bank, Row: r.Row, Column: r.Column,
			OsPhysicalAddress: r.Address, Source: r.Source, Decoder: r.Decoder, RawRef: r.RawRef, Raw: r.Raw,
			Fingerprint: r.Fingerprint}
		if e.ErrorType == "" {
			e.ErrorType = string(r.Event)
		}
		if sev.Class() != "" {
			if r.Event == CE || r.Event == ScrubCE {
				e.Count = max(r.Count, 1)
			}
			if repeat > 1 {
				// The entry folds repeats: each counts.
				e.Count *= repeat
				if !fw.LastAt.IsZero() && fw.LastAt.After(r.At) {
					e.WindowStart, e.ErrorDT = r.At, fw.LastAt
				}
			}
		}
		switch {
		case len(r.Labels) == 1:
			e.Label = r.Labels[0]
		case len(r.Labels) > 1:
			e.ErrorType += " on one of " + strings.Join(r.Labels, ", ")
		}
		e.Grade = e.ComputeGrade()
		f := fw
		e.Firmware = &f
		out.Events = append(out.Events, e)
		return out, nil
	}

	kind, result := r.action()
	method := r.Method
	if r.Event == Replaced {
		method = "bmc"
	}
	detail := r.Text
	if detail == "" {
		detail = string(r.Event)
	}
	targets := r.Labels
	if len(targets) == 0 {
		targets = []string{""}
	}
	for n, label := range targets {
		f := fw
		f.Args = append([]string(nil), fw.Args...)
		a := &model.Action{At: r.At, TimeQuality: quality, Kind: kind, Method: method, Result: result,
			OsPhysicalAddress: r.Address, Rank: r.Rank, DeviceId: r.DeviceId, BankGroup: r.BankGroup, Bank: r.Bank, Row: r.Row,
			Label: label, Detail: detail, Source: r.Source, RawRef: r.RawRef, Raw: r.Raw,
			Fingerprint: model.Fingerprint(r.Fingerprint, strconv.Itoa(n)), Firmware: &f}
		out.Actions = append(out.Actions, a)
	}
	return out, nil
}

// severity returns the severity of a record that becomes an event.
func (r *Record) severity() (model.Severity, model.ScrubType, bool) {
	switch r.Event {
	case CE, CEThreshold:
		return model.SevCE, "", true
	case UE:
		return r.uncorrected(), "", true
	case ScrubCE:
		return model.SevCE, model.ScrubPatrol, true
	case ScrubUE:
		return r.uncorrected(), model.ScrubPatrol, true
	case CELogLimit, MirrorLost, ConfigError, Thermal, Cleared, Unclassified:
		return model.SevInformational, "", true
	}
	return "", "", false
}

func (r *Record) uncorrected() model.Severity {
	for _, f := range r.Flags {
		switch f {
		case Fatal:
			return model.SevFatal
		case Contained:
			return model.SevRecoverable
		}
	}
	return model.SevUE
}

// action returns the kind and result of a record that becomes an action.
func (r *Record) action() (model.ActionKind, model.ActionResult) {
	ppr := model.ActionPPR
	for _, f := range r.Flags {
		switch f {
		case Soft:
			ppr = model.ActionSPPR
		case Hard:
			ppr = model.ActionHPPR
		}
	}
	switch r.Event {
	case PredictedFailure:
		return model.ActionPredictedFailure, model.ResultOK
	case Disabled:
		return model.ActionDisable, model.ResultOK
	case PPRScheduled:
		return ppr, model.ResultPending
	case PPRDone:
		return ppr, model.ResultOK
	case PPRFailed:
		return ppr, model.ResultFailed
	case Sparing:
		return model.ActionSparing, model.ResultOK
	case DeviceSparing:
		return model.ActionDeviceSparing, model.ResultOK
	case SpareCopyStarted:
		return model.ActionSparing, model.ResultPending
	case SparingFailed:
		if deviceSparing(r.Flags) {
			return model.ActionDeviceSparing, model.ResultFailed
		}
		return model.ActionSparing, model.ResultFailed
	case MirrorFailover:
		return model.ActionMirrorFailover, model.ResultOK
	case TrainingFailure:
		return model.ActionTrainingFailure, model.ResultOK
	case Replaced:
		return model.ActionReplacement, model.ResultOK
	}
	return "", model.ResultUnknown
}

// ErrRecord marks a record a reader built wrong: an unknown common event or
// flag, or no fingerprint. The record is skipped; the sink is not at fault.
var ErrRecord = errors.New("firmware record")

// Emit writes what a record becomes into sink. An error that wraps
// ErrRecord is about the record; any other error is the sink's.
func Emit(sink adapter.Sink, r *Record) error {
	out, err := r.Ledger()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRecord, err)
	}
	for _, e := range out.Events {
		if err := sink.Event(e); err != nil {
			return err
		}
	}
	for _, a := range out.Actions {
		if err := sink.Action(a); err != nil {
			return err
		}
	}
	if out.Alone != nil {
		return sink.Firmware(out.Alone)
	}
	return nil
}
