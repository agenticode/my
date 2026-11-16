package fwevent

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
)

var at = time.Date(2026, 11, 3, 2, 14, 7, 0, time.UTC)

func record(name Name, flags ...Flag) *Record {
	return &Record{Event: name, Flags: flags, At: at, Labels: []string{"DIMM A7"}, Source: "redfish/log", RawRef: "E1",
		Raw: `{"Id":"1"}`, Fingerprint: "fp", Text: "words",
		Firmware: model.FirmwareRecord{Vendor: "Example", VendorID: "MEM0804", Origin: model.OriginBMCLog, Mapping: model.MappingTable}}
}

// TestLedgerTable checks the table of section 4.1 of the design line by
// line: what each common event, with its flags, becomes in the ledger.
func TestLedgerTable(t *testing.T) {
	type want struct {
		// event
		sev   model.Severity
		scrub model.ScrubType
		count int
		// action
		kind   model.ActionKind
		result model.ActionResult
		method string
		// alone
		alone bool
	}
	cases := []struct {
		name  Name
		flags []Flag
		count int
		want  want
	}{
		{CE, nil, 0, want{sev: model.SevCE, count: 1}},
		{CE, nil, 12, want{sev: model.SevCE, count: 12}},
		{CEThreshold, nil, 500, want{sev: model.SevCE, count: 1}},
		{CELogLimit, nil, 0, want{sev: model.SevInformational, count: 1}},
		{UE, nil, 0, want{sev: model.SevUE, count: 1}},
		{UE, []Flag{Contained}, 0, want{sev: model.SevRecoverable, count: 1}},
		{UE, []Flag{Fatal}, 0, want{sev: model.SevFatal, count: 1}},
		{ScrubCE, nil, 3, want{sev: model.SevCE, scrub: model.ScrubPatrol, count: 3}},
		{ScrubUE, nil, 0, want{sev: model.SevUE, scrub: model.ScrubPatrol, count: 1}},
		{ScrubUE, []Flag{Contained}, 0, want{sev: model.SevRecoverable, scrub: model.ScrubPatrol, count: 1}},
		{PredictedFailure, nil, 0, want{kind: model.ActionPredictedFailure, result: model.ResultOK}},
		{Disabled, nil, 0, want{kind: model.ActionDisable, result: model.ResultOK}},
		{PPRScheduled, nil, 0, want{kind: model.ActionPPR, result: model.ResultPending}},
		{PPRScheduled, []Flag{Soft}, 0, want{kind: model.ActionSPPR, result: model.ResultPending}},
		{PPRDone, nil, 0, want{kind: model.ActionPPR, result: model.ResultOK}},
		{PPRDone, []Flag{Hard}, 0, want{kind: model.ActionHPPR, result: model.ResultOK}},
		{PPRDone, []Flag{Soft, Runtime}, 0, want{kind: model.ActionSPPR, result: model.ResultOK}},
		{PPRFailed, nil, 0, want{kind: model.ActionPPR, result: model.ResultFailed}},
		{PPRFailed, []Flag{Hard}, 0, want{kind: model.ActionHPPR, result: model.ResultFailed}},
		{Sparing, nil, 0, want{kind: model.ActionSparing, result: model.ResultOK}},
		{Sparing, []Flag{Rank}, 0, want{kind: model.ActionSparing, result: model.ResultOK}},
		{DeviceSparing, []Flag{ADDDC}, 0, want{kind: model.ActionDeviceSparing, result: model.ResultOK}},
		{DeviceSparing, []Flag{SDDC}, 0, want{kind: model.ActionDeviceSparing, result: model.ResultOK}},
		{SpareCopyStarted, nil, 0, want{kind: model.ActionSparing, result: model.ResultPending}},
		{SparingFailed, nil, 0, want{kind: model.ActionSparing, result: model.ResultFailed}},
		{SparingFailed, []Flag{Rank}, 0, want{kind: model.ActionSparing, result: model.ResultFailed}},
		{SparingFailed, []Flag{ADDDC}, 0, want{kind: model.ActionDeviceSparing, result: model.ResultFailed}},
		{SparingFailed, []Flag{Bank}, 0, want{kind: model.ActionDeviceSparing, result: model.ResultFailed}},
		{SparingFailed, []Flag{PCLS}, 0, want{kind: model.ActionDeviceSparing, result: model.ResultFailed}},
		{MirrorFailover, nil, 0, want{kind: model.ActionMirrorFailover, result: model.ResultOK}},
		{MirrorLost, nil, 0, want{sev: model.SevInformational, count: 1}},
		{TrainingFailure, []Flag{Boot}, 0, want{kind: model.ActionTrainingFailure, result: model.ResultOK}},
		{ConfigError, nil, 0, want{sev: model.SevInformational, count: 1}},
		{Replaced, nil, 0, want{kind: model.ActionReplacement, result: model.ResultOK, method: "bmc"}},
		{LogCleared, nil, 0, want{alone: true}},
		{LogFull, nil, 0, want{alone: true}},
		{Thermal, nil, 0, want{sev: model.SevInformational, count: 1}},
		{Presence, nil, 0, want{alone: true}},
		{ConfigState, nil, 0, want{alone: true}},
		{Cleared, nil, 0, want{sev: model.SevInformational, count: 1}},
		{Unclassified, nil, 0, want{sev: model.SevInformational, count: 1}},
	}
	covered := map[Name]bool{}
	for _, c := range cases {
		covered[c.name] = true
		r := record(c.name, c.flags...)
		r.Count = c.count
		r.Method = "bios"
		out, err := r.Ledger()
		if err != nil {
			t.Errorf("%s %v: %v", c.name, c.flags, err)
			continue
		}
		n := len(out.Events) + len(out.Actions)
		if out.Alone != nil {
			n++
		}
		if n != 1 {
			t.Errorf("%s %v: %d ledger records, want 1", c.name, c.flags, n)
			continue
		}
		var fw *model.FirmwareRecord
		switch {
		case c.want.alone:
			if out.Alone == nil {
				t.Errorf("%s: no firmware record alone", c.name)
				continue
			}
			fw = out.Alone
			if fw.Fingerprint == "" || fw.Raw != r.Raw {
				t.Errorf("%s: a record alone needs its fingerprint and raw text: %+v", c.name, fw)
			}
		case c.want.kind != "":
			if len(out.Actions) != 1 {
				t.Errorf("%s %v: no action", c.name, c.flags)
				continue
			}
			a := out.Actions[0]
			method := c.want.method
			if method == "" {
				method = "bios"
			}
			if a.Kind != c.want.kind || a.Result != c.want.result || a.Method != method || a.Label != "DIMM A7" || !a.At.Equal(at) ||
				a.Detail != "words" || a.Source != r.Source || a.RawRef != r.RawRef || a.Raw != r.Raw {
				t.Errorf("%s %v: action %+v, want kind %s result %s method %s", c.name, c.flags, a, c.want.kind, c.want.result, method)
			}
			if !a.Kind.Valid() {
				t.Errorf("%s: kind %s is not a valid action kind", c.name, a.Kind)
			}
			fw = a.Firmware
		default:
			if len(out.Events) != 1 {
				t.Errorf("%s %v: no event", c.name, c.flags)
				continue
			}
			e := out.Events[0]
			if e.ErrorSeverity != c.want.sev || e.ErrorScrubType != c.want.scrub || e.Count != c.want.count || e.Label != "DIMM A7" ||
				!e.ErrorDT.Equal(at) || e.Fingerprint != r.Fingerprint || e.Grade != model.GradeC || e.ErrorType != "words" {
				t.Errorf("%s %v: event %+v, want %s %s count %d", c.name, c.flags, e, c.want.sev, c.want.scrub, c.want.count)
			}
			fw = e.Firmware
		}
		if fw == nil {
			t.Errorf("%s: no firmware record", c.name)
			continue
		}
		var fl []string
		for _, f := range c.flags {
			fl = append(fl, string(f))
		}
		if fw.Common != string(c.name) || !slices.Equal(fw.Flags, fl) || fw.VendorID != "MEM0804" || fw.Mapping != model.MappingTable ||
			!fw.At.Equal(at) || fw.Source != r.Source || fw.RawRef != r.RawRef {
			t.Errorf("%s %v: firmware record %+v", c.name, c.flags, fw)
		}
	}
	for _, n := range Names() {
		if !covered[n] {
			t.Errorf("the table test has no line for %s", n)
		}
	}
	if len(Names()) != 27 {
		t.Errorf("%d common events, want 27", len(Names()))
	}
}

// TestLedgerPlaces: several modules make one error tied to none and one
// action per module; numbers and an address reach the record.
func TestLedgerPlaces(t *testing.T) {
	r := record(UE)
	r.Labels = []string{"DIMM A1", "DIMM A2"}
	out, err := r.Ledger()
	if err != nil {
		t.Fatal(err)
	}
	if e := out.Events[0]; e.Label != "" || e.ErrorType != "words on one of DIMM A1, DIMM A2" {
		t.Errorf("error naming two modules: %+v", e)
	}
	r = record(Disabled)
	r.Labels = []string{"DIMM A1", "DIMM A2"}
	if out, err = r.Ledger(); err != nil {
		t.Fatal(err)
	}
	if len(out.Actions) != 2 || out.Actions[0].Label != "DIMM A1" || out.Actions[1].Label != "DIMM A2" ||
		out.Actions[0].Fingerprint == out.Actions[1].Fingerprint || out.Actions[0].Firmware == out.Actions[1].Firmware {
		t.Errorf("mark naming two modules: %+v", out.Actions)
	}
	r = record(UE)
	r.Address, r.Rank = model.Some[uint64](0x12345040), model.Some(1)
	out, _ = r.Ledger()
	if e := out.Events[0]; e.Grade != model.GradeB || e.OsPhysicalAddress.V != 0x12345040 || e.Rank.V != 1 {
		t.Errorf("error with an address: %+v", e)
	}
	r = record(PPRDone)
	r.Rank, r.Bank, r.Row, r.DeviceId = model.Some(0), model.Some(3), model.Some(0x1f2), model.Some(5)
	out, _ = r.Ledger()
	if a := out.Actions[0]; a.Rank.V != 0 || a.Bank.V != 3 || a.Row.V != 0x1f2 || a.DeviceId.V != 5 {
		t.Errorf("repair with its row: %+v", a)
	}
	r = record(CE)
	r.Labels = nil
	if out, _ = r.Ledger(); out.Events[0].Label != "" {
		t.Errorf("error without a module: %+v", out.Events[0])
	}
}

// TestLedgerRepeat: an entry that folds n repeats counts n, from its
// creation to its latest occurrence.
func TestLedgerRepeat(t *testing.T) {
	r := record(CE)
	r.Firmware.Repeat, r.Firmware.LastAt = 3, at.Add(time.Hour)
	out, _ := r.Ledger()
	if e := out.Events[0]; e.Count != 3 || !e.WindowStart.Equal(at) || !e.ErrorDT.Equal(at.Add(time.Hour)) {
		t.Errorf("repeated entry: count %d window %v to %v", e.Count, e.WindowStart, e.ErrorDT)
	}
	r = record(Unclassified)
	r.Firmware.Repeat = 3
	if out, _ = r.Ledger(); out.Events[0].Count != 1 {
		t.Errorf("an informational record counts 1, got %d", out.Events[0].Count)
	}
}

func TestLedgerRefusesWhatIsNotTheVocabulary(t *testing.T) {
	for name, r := range map[string]*Record{
		"unknown event":  record("ppr-ok"),
		"unknown flag":   record(PPRDone, "softly"),
		"unknown clears": {Event: Cleared, Clears: "gone", Fingerprint: "x", At: at},
		"no fingerprint": {Event: CE, At: at},
	} {
		if _, err := r.Ledger(); err == nil {
			t.Errorf("%s: no error", name)
		}
		if err := Emit(&adapter.Collected{}, r); !errors.Is(err, ErrRecord) {
			t.Errorf("%s: Emit gave %v, want ErrRecord", name, err)
		}
	}
}

func TestEmit(t *testing.T) {
	var c adapter.Collected
	for _, n := range []Name{CE, PPRDone, Presence} {
		if err := Emit(&c, record(n)); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.Events) != 1 || len(c.Actions) != 1 || len(c.Firmwares) != 1 {
		t.Errorf("emitted %d events, %d actions, %d firmware records", len(c.Events), len(c.Actions), len(c.Firmwares))
	}
}

func TestNamesAndFlags(t *testing.T) {
	seen := map[Name]bool{}
	for _, n := range Names() {
		if seen[n] || !n.Valid() || n.Family() == "" {
			t.Errorf("name %q", n)
		}
		seen[n] = true
	}
	if Name("ppr").Valid() || Flag("x").Valid() {
		t.Error("an unknown name or flag is valid")
	}
	if len(Flags()) != 12 {
		t.Errorf("%d flags, want 12", len(Flags()))
	}
}
