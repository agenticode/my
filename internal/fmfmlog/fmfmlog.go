// Package fmfmlog writes ledger records and findings in the forms of the
// OCP Fleet Memory Fault Management (FMFM) framework: the runtime error log
// (one record per error) and the analysis log an analyzer returns.
//
// The framework document names the fields of the runtime log; the ledger
// uses the same names, so an error record goes out as it is. For the
// analysis log the document lists what an analyzer should say (the serial
// number and place of the DIMM, the inferred fault mode, a risk and a
// severity class, a suggested action). The JSON schema that fixes the
// names and the nesting is not public. The names here are the document's
// words in the style of the runtime fields; additions of MemorY are in
// lower case. docs/fmfm-mapping.md lists every field.
//
// Nothing is filled in. A field a source did not report is left out, and
// each record says which fields are missing and which observation grade
// its data has. The analysis log holds findings and suggested actions. It
// never holds a Common Platform Action Descriptor (CPAD): this tool takes
// no action and asks for none.
package fmfmlog

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
)

// Runtime is an error record with the field names of the FMFM runtime
// error log, in the order the framework document lists them. The fields in
// lower case are additions of MemorY. A field a source did not report is
// left out.
type Runtime struct {
	ErrorDT           string `json:"ErrorDT,omitempty"`
	ErrorScrubType    string `json:"ErrorScrubType,omitempty"`
	ErrorSeverity     string `json:"ErrorSeverity"`
	HostID            string `json:"HostID"`
	SocketId          *int   `json:"SocketId,omitempty"`
	ChannelId         *int   `json:"ChannelId,omitempty"`
	ChannelSlotId     *int   `json:"ChannelSlotId,omitempty"`
	SubChannel        *int   `json:"SubChannel,omitempty"`
	Rank              *int   `json:"Rank,omitempty"`
	DeviceId          *int   `json:"DeviceId,omitempty"`
	BankGroup         *int   `json:"BankGroup,omitempty"`
	Bank              *int   `json:"Bank,omitempty"`
	Row               *int   `json:"Row,omitempty"`
	Column            *int   `json:"Column,omitempty"`
	ErrorVector       string `json:"ErrorVector,omitempty"`
	OsPhysicalAddress string `json:"OsPhysicalAddress,omitempty"`

	// SecondsSinceBoot stands in for ErrorDT on a record that has no date.
	SecondsSinceBoot *int64 `json:"seconds_since_boot,omitempty"`
	Count            int    `json:"count,omitempty"`
	Slot             string `json:"slot,omitempty"`
	Grade            string `json:"grade"`
	Source           string `json:"source"`
	RawRef           string `json:"raw_ref"`
}

// timeLayout is how times are written: UTC with milliseconds.
const timeLayout = "2006-01-02T15:04:05.000Z07:00"

func ptr(o model.Opt[int]) *int {
	if !o.Valid {
		return nil
	}
	v := o.V
	return &v
}

// RuntimeOf writes an error record with FMFM field names.
func RuntimeOf(e *model.Event) Runtime {
	r := Runtime{
		ErrorScrubType: string(e.ErrorScrubType), ErrorSeverity: string(e.ErrorSeverity), HostID: e.Host,
		SocketId: ptr(e.SocketId), ChannelId: ptr(e.ChannelId), ChannelSlotId: ptr(e.ChannelSlotId), SubChannel: ptr(e.SubChannel),
		Rank: ptr(e.Rank), DeviceId: ptr(e.DeviceId), BankGroup: ptr(e.BankGroup), Bank: ptr(e.Bank),
		Row: ptr(e.Row), Column: ptr(e.Column), ErrorVector: e.ErrorVector,
		Slot: e.Slot, Grade: string(e.Grade), Source: e.Source, RawRef: e.RawRef,
	}
	if e.TimeQuality == model.TimeRelative {
		s := e.ErrorDT.Unix()
		r.SecondsSinceBoot = &s
	} else if !e.ErrorDT.IsZero() {
		r.ErrorDT = e.ErrorDT.UTC().Format(timeLayout)
	}
	if e.OsPhysicalAddress.Valid {
		// As text: an address can be larger than a JSON reader keeps exact.
		r.OsPhysicalAddress = fmt.Sprintf("%#x", e.OsPhysicalAddress.V)
	}
	if e.Count != 1 {
		r.Count = e.Count
	}
	return r
}

// Level 1 fault modes in the words of the framework document.
var faultModes = map[model.FaultMode]string{
	model.ModeSingleBit:   "Single bit fault",
	model.ModeRow:         "Row fault",
	model.ModeMultiRow:    "Multi-Row fault",
	model.ModeColumn:      "Column fault",
	model.ModeBank:        "Bank fault",
	model.ModeDevice:      "Device fault",
	model.ModeDIMM:        "DIMM fault",
	model.ModeSystemIssue: "System issue",
}

// FaultMode returns the name the framework document gives a Level 1 fault
// mode, or "" for a mode the data does not determine.
func FaultMode(m model.FaultMode) string { return faultModes[m] }

// Risk classes of the framework document. "Risk to SDC" is never written:
// whether a fault pattern can escape the error correction of a platform
// depends on its ECC scheme, which is the knowledge of a vendor analyzer.
const (
	RiskUE       = "Risk to UE"
	RiskRepeated = "Risk to repeated CE or CE storm"
	RiskLow      = "Low Risk"
)

// Severity classes of the framework document.
const (
	SeverityHigh   = "high"
	SeverityMedium = "medium"
	SeverityLow    = "low"
)

// ActionRemoval is the one suggestion of the framework document's list
// that a finding of this tool maps to exactly.
const ActionRemoval = "Faulty DIMM Removal"

// Analyzer names what wrote an analysis log.
type Analyzer struct {
	Name          string `json:"Name"`
	Version       string `json:"Version"`
	EngineVersion string `json:"engine_version"`
	// Level is the level of fault classification the log holds: 1, the
	// public one.
	Level int `json:"level"`
}

// Location is the place of a DIMM in the terms of the runtime log.
type Location struct {
	HostID        string `json:"HostID"`
	SocketId      *int   `json:"SocketId,omitempty"`
	ChannelId     *int   `json:"ChannelId,omitempty"`
	ChannelSlotId *int   `json:"ChannelSlotId,omitempty"`
	SubChannel    *int   `json:"SubChannel,omitempty"`
	// Slot is the name of the slot on the board.
	Slot string `json:"slot,omitempty"`
}

// Fault is one fault of a DIMM: its mode and where it sits.
type Fault struct {
	FaultMode string `json:"FaultMode,omitempty"`
	// Mode is the mode in the ledger's own word; "undetermined" where the
	// observation grade allows none. Estimated says that the data does not
	// fix it.
	Mode      string `json:"mode"`
	Estimated bool   `json:"estimated,omitempty"`
	Rank      *int   `json:"Rank,omitempty"`
	DeviceId  *int   `json:"DeviceId,omitempty"`
	BankGroup *int   `json:"BankGroup,omitempty"`
	Bank      *int   `json:"Bank,omitempty"`
	RowMin    *int   `json:"row_min,omitempty"`
	RowMax    *int   `json:"row_max,omitempty"`
	ColumnMin *int   `json:"column_min,omitempty"`
	ColumnMax *int   `json:"column_max,omitempty"`
	// AddressMin and AddressMax bound the physical addresses involved.
	AddressMin  string `json:"address_min,omitempty"`
	AddressMax  string `json:"address_max,omitempty"`
	FirstSeenDT string `json:"first_seen,omitempty"`
	LastSeenDT  string `json:"last_seen,omitempty"`
	Corrected   int64  `json:"corrected"`
	Uncorrected int64  `json:"uncorrected"`
	Grade       string `json:"grade"`
}

// Suggestion is a suggested action in the form of the framework document:
// the action and what it applies to. It is a suggestion to a person. It is
// no action descriptor and nothing acts on it.
type Suggestion struct {
	Action string `json:"Action"`
	Target string `json:"Target"`
}

// Finding is what MemorY itself says about a DIMM, in its own terms.
type Finding struct {
	Grade      string   `json:"grade"`
	Label      string   `json:"label"`
	Rules      []string `json:"rules"`
	Confidence int      `json:"confidence"`
	// Evidence, Checks and Missing are the sentences of the finding.
	Evidence []string `json:"evidence,omitempty"`
	Checks   []string `json:"checks,omitempty"`
	Missing  []string `json:"missing,omitempty"`
	// NextAction is the step the finding names, as a code and a sentence.
	NextAction     string `json:"next_action,omitempty"`
	NextActionCode string `json:"next_action_code,omitempty"`
}

// Analysis is the analysis log record of one DIMM.
type Analysis struct {
	OutputDT         string   `json:"OutputDT"`
	DimmSerialNumber string   `json:"DimmSerialNumber,omitempty"`
	Location         Location `json:"Location"`
	// FaultMode is the Level 1 mode of the fault with the most errors.
	FaultMode           string       `json:"FaultMode,omitempty"`
	RiskClassifier      string       `json:"RiskClassifier,omitempty"`
	SeverityClassifier  string       `json:"SeverityClassifier,omitempty"`
	RasActionSuggestion []Suggestion `json:"RasActionSuggestion"`
	// SystemIssue says that a cause outside the DIMM is suspected.
	SystemIssue bool `json:"SystemIssue"`
	Confidence  int  `json:"Confidence"`

	// Additions of MemorY.
	Manufacturer string  `json:"manufacturer,omitempty"`
	PartNumber   string  `json:"part_number,omitempty"`
	DIMMUID      string  `json:"dimm_uid"`
	Present      bool    `json:"present"`
	Corrected    int64   `json:"corrected"`
	Uncorrected  int64   `json:"uncorrected"`
	Faults       []Fault `json:"faults,omitempty"`
	Finding      Finding `json:"finding"`
	// Observation counts the errors by observation grade: how much of
	// their place the sources gave.
	Observation Observation `json:"observation"`
	// NotFilled names the fields above that are left out because no source
	// gave what they need.
	NotFilled []string `json:"not_filled,omitempty"`
}

// Observation counts errors by observation grade.
type Observation struct {
	GradeA int64 `json:"grade_a"`
	GradeB int64 `json:"grade_b"`
	GradeC int64 `json:"grade_c"`
}

// Options say how an analysis record is written.
type Options struct {
	// Lang is the language of the sentences of the finding.
	Lang i18n.Lang
	// Now is the time of the output.
	Now time.Time
	// Slot gives the place numbers of the slot of a DIMM, when the ledger
	// has them.
	Slot func(slot string) (socket, channel, position model.Opt[int])
}

// AnalysisOf writes the analysis log record of one DIMM from its finding.
func AnalysisOf(d *engine.DIMMResult, opts Options) Analysis {
	lang := opts.Lang
	if lang == "" {
		lang = i18n.EN
	}
	a := Analysis{
		OutputDT: opts.Now.UTC().Format(timeLayout),
		Location: Location{HostID: d.Host, Slot: d.Slot},
		DIMMUID:  d.UID, Present: d.Present,
		Corrected: d.Features.CE, Uncorrected: d.Features.UE,
		RasActionSuggestion: []Suggestion{},
		Observation:         Observation{GradeA: d.Features.GradeA, GradeB: d.Features.GradeB, GradeC: d.Features.GradeC},
	}
	if d.DIMM != nil {
		a.Manufacturer, a.PartNumber = d.DIMM.Manufacturer, d.DIMM.PartNumber
		if d.DIMM.Identity == model.IdentitySerial {
			a.DimmSerialNumber = d.DIMM.Serial
		}
	}
	if a.DimmSerialNumber == "" {
		a.NotFilled = append(a.NotFilled, "DimmSerialNumber")
	}
	if opts.Slot != nil {
		socket, channel, position := opts.Slot(d.Slot)
		a.Location.SocketId, a.Location.ChannelId, a.Location.ChannelSlotId = ptr(socket), ptr(channel), ptr(position)
	}
	for _, f := range []struct {
		name string
		v    *int
	}{{"SocketId", a.Location.SocketId}, {"ChannelId", a.Location.ChannelId}, {"ChannelSlotId", a.Location.ChannelSlotId}} {
		if f.v == nil {
			a.NotFilled = append(a.NotFilled, "Location."+f.name)
		}
	}
	// No source of this tool reports the sub-channel of a DDR5 DIMM for
	// the DIMM as a whole.
	a.NotFilled = append(a.NotFilled, "Location.SubChannel")

	// Faults, the one with the most errors first.
	faults := append([]*engine.Fault{}, d.Faults...)
	sort.SliceStable(faults, func(i, j int) bool { return faults[i].CE+faults[i].UE > faults[j].CE+faults[j].UE })
	for _, f := range faults {
		x := Fault{
			FaultMode: FaultMode(f.Mode), Mode: string(f.Mode), Estimated: f.Estimated,
			Rank: ptr(f.Rank), DeviceId: ptr(f.DeviceId), BankGroup: ptr(f.BankGroup), Bank: ptr(f.Bank),
			RowMin: ptr(f.RowMin), RowMax: ptr(f.RowMax), ColumnMin: ptr(f.ColumnMin), ColumnMax: ptr(f.ColumnMax),
			Corrected: f.CE, Uncorrected: f.UE, Grade: string(f.Grade),
		}
		if f.AddressMin.Valid {
			x.AddressMin = fmt.Sprintf("%#x", f.AddressMin.V)
		}
		if f.AddressMax.Valid {
			x.AddressMax = fmt.Sprintf("%#x", f.AddressMax.V)
		}
		if !f.FirstSeen.IsZero() && f.FirstSeen.Year() > 1970 {
			x.FirstSeenDT = f.FirstSeen.UTC().Format(timeLayout)
		}
		if !f.LastSeen.IsZero() && f.LastSeen.Year() > 1970 {
			x.LastSeenDT = f.LastSeen.UTC().Format(timeLayout)
		}
		a.Faults = append(a.Faults, x)
	}
	if len(faults) > 0 {
		a.FaultMode = FaultMode(faults[0].Mode)
	}
	if a.FaultMode == "" && d.Features.CE+d.Features.UE > 0 {
		// Errors on record and no mode: the observation grade allows none.
		a.NotFilled = append(a.NotFilled, "FaultMode")
	}

	v := d.Verdict
	if v == nil {
		a.Finding = Finding{Grade: string(model.VerdictOK), Label: model.VerdictLabel, Rules: []string{}}
		return a
	}
	a.Confidence = v.Confidence
	a.Finding = Finding{Grade: string(v.Grade), Label: v.Label, Rules: append([]string{}, v.Rules...), Confidence: v.Confidence}
	for _, e := range v.Evidence {
		text := engine.Text(lang, e.Statement)
		if e.Rule != "" {
			text = "[" + e.Rule + "] " + text
		}
		a.Finding.Evidence = append(a.Finding.Evidence, text)
	}
	for _, c := range v.CounterEvidence {
		a.Finding.Checks = append(a.Finding.Checks, string(c.Effect)+": "+engine.Text(lang, c.Statement))
	}
	for _, m := range v.Missing {
		a.Finding.Missing = append(a.Finding.Missing, engine.Text(lang, m))
	}
	if v.NextAction.Code != "" {
		a.Finding.NextAction, a.Finding.NextActionCode = engine.Text(lang, v.NextAction), v.NextAction.Code
	}

	// A system issue: a rule that holds replacement fired, or the engine
	// named the fault so.
	a.SystemIssue = v.Grade == model.VerdictHoldReplacement
	for _, f := range faults {
		if f.Mode == model.ModeSystemIssue {
			a.SystemIssue = true
		}
	}

	fired := map[string]bool{}
	for _, r := range v.Rules {
		fired[r] = true
	}
	// Severity. High stands for a fault no action in the field recovers,
	// or one that is still there after a repair; medium for a fault an
	// action may recover; low for "keep watching". A suspected cause
	// outside the DIMM has no class: what is wrong is not known yet.
	switch {
	case v.Grade == model.VerdictHoldReplacement:
		a.NotFilled = append(a.NotFilled, "SeverityClassifier")
	case v.Grade == model.VerdictReplaceNow, fired[engine.RuleRecurred]:
		a.SeverityClassifier = SeverityHigh
	case v.Grade == model.VerdictConsiderReplacement:
		a.SeverityClassifier = SeverityMedium
	case v.Grade == model.VerdictObserve:
		a.SeverityClassifier = SeverityLow
	}
	// Risk.
	switch {
	case v.Grade == model.VerdictHoldReplacement:
		a.NotFilled = append(a.NotFilled, "RiskClassifier")
	case d.Features.UE > 0:
		a.RiskClassifier = RiskUE
	case fired[engine.RuleRow], fired[engine.RuleOffline], fired[engine.RuleRecurred]:
		a.RiskClassifier = RiskRepeated
	case d.Features.CE > 0:
		a.RiskClassifier = RiskLow
	}
	// The suggestion: only where the step of the finding is one of the
	// actions the framework document lists.
	switch v.NextAction.Code {
	case "next.replace", "next.plan":
		a.RasActionSuggestion = append(a.RasActionSuggestion, Suggestion{Action: ActionRemoval, Target: target(&a)})
	}
	return a
}

// target names a DIMM for a suggestion: its serial number and its slot.
func target(a *Analysis) string {
	var parts []string
	if a.DimmSerialNumber != "" {
		parts = append(parts, "S/N "+a.DimmSerialNumber)
	}
	if a.Location.Slot != "" {
		parts = append(parts, a.Location.Slot)
	}
	return strings.Join(parts, ", ")
}
