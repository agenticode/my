package fmfmlog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/fmfm"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/synth"
	"github.com/agenticode/my/internal/testledger"
)

func TestMain(m *testing.M) { testledger.Main(m) }

var ctx = context.Background()

var now = time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)

// host runs the engine over the ledger of a scenario.
func host(t *testing.T, scenario string, fl synth.Flavor) (*store.Store, *engine.HostResult) {
	t.Helper()
	s := testledger.Open(t, scenario, 1, fl)
	res, err := engine.Run(ctx, s, engine.Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hosts) != 1 {
		t.Fatalf("%d hosts", len(res.Hosts))
	}
	return s, res.Hosts[0]
}

func build(t *testing.T, scenario string, fl synth.Flavor, opts BuildOptions) *Document {
	t.Helper()
	s, h := host(t, scenario, fl)
	opts.Now, opts.Tool, opts.Version = now, "MemorY", "test"
	doc, err := Build(ctx, s, h, opts)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func bySlot(doc *Document) map[string]*Analysis {
	out := map[string]*Analysis{}
	for i := range doc.AnalysisLog {
		out[doc.AnalysisLog[i].Location.Slot] = &doc.AnalysisLog[i]
	}
	return out
}

func TestAnalysisLogOfTheDemoHost(t *testing.T) {
	doc := build(t, "demo", "", BuildOptions{})
	if doc.Format != Format || doc.FormatVersion != 1 || doc.HostID != "r12-n034" || doc.OutputDT != "2026-11-13T09:30:00.000Z" ||
		doc.Analyzer != (Analyzer{Name: "MemorY", Version: "test", EngineVersion: engine.Version, Level: 1}) || doc.ActionDescriptors != "none" {
		t.Errorf("document head: %+v", doc)
	}
	// The DIMMs with errors or a finding, the worst first; no runtime log
	// unless asked for.
	if len(doc.AnalysisLog) != 2 || doc.RuntimeLog != nil {
		t.Fatalf("%d analysis records, %d runtime records", len(doc.AnalysisLog), len(doc.RuntimeLog))
	}
	a := doc.AnalysisLog[0]
	zero, two := 0, 2
	want := Location{HostID: "r12-n034", SocketId: &zero, ChannelId: &two, ChannelSlotId: &zero, Slot: "CPU0_C1"}
	if !reflect.DeepEqual(a.Location, want) || a.DimmSerialNumber != "3B9C56" || a.PartNumber != "EXM64G48R2X4-A" || !a.Present {
		t.Errorf("identity and place: %+v", a)
	}
	if a.FaultMode != "Row fault" || a.RiskClassifier != RiskUE || a.SeverityClassifier != SeverityHigh || a.SystemIssue || a.Confidence != a.Finding.Confidence || a.Confidence < 75 {
		t.Errorf("classification: %+v", a)
	}
	if !reflect.DeepEqual(a.RasActionSuggestion, []Suggestion{{Action: "Faulty DIMM Removal", Target: "S/N 3B9C56, CPU0_C1"}}) {
		t.Errorf("suggestion: %+v", a.RasActionSuggestion)
	}
	if a.Finding.Grade != "replace-now" || a.Finding.Label != "finding" || !reflect.DeepEqual(a.Finding.Rules, []string{"R-UE-01", "R-ROW-03", "R-REP-02"}) ||
		a.Finding.NextActionCode != "next.replace" || len(a.Finding.Evidence) != 6 || !strings.HasPrefix(a.Finding.Evidence[0], "[R-UE-01] 1 uncorrected error") {
		t.Errorf("finding: %+v", a.Finding)
	}
	if a.Corrected != 1231 || a.Uncorrected != 1 || a.Observation != (Observation{GradeA: 1232}) {
		t.Errorf("counts: %d CE, %d UE, %+v", a.Corrected, a.Uncorrected, a.Observation)
	}
	if len(a.Faults) != 1 || a.Faults[0].FaultMode != "Row fault" || a.Faults[0].Mode != "row" || *a.Faults[0].DeviceId != 7 || *a.Faults[0].Bank != 3 || *a.Faults[0].RowMin != 0x1a2f ||
		a.Faults[0].Grade != "A" || !strings.HasPrefix(a.Faults[0].AddressMin, "0x") || a.Faults[0].FirstSeenDT == "" {
		t.Errorf("faults: %+v", a.Faults)
	}
	// The sub-channel is the one field no source gives.
	if !reflect.DeepEqual(a.NotFilled, []string{"Location.SubChannel"}) {
		t.Errorf("not filled: %q", a.NotFilled)
	}

	// The DIMM with one weak cell: keep watching, and no suggestion.
	b := doc.AnalysisLog[1]
	if b.Location.Slot != "CPU1_D1" || b.FaultMode != "Single bit fault" || b.RiskClassifier != RiskLow || b.SeverityClassifier != SeverityLow ||
		len(b.RasActionSuggestion) != 0 || b.RasActionSuggestion == nil || b.Finding.Grade != "observe" || b.Finding.NextActionCode != "next.observe" {
		t.Errorf("second DIMM: %+v", b)
	}

	// With All, the DIMMs without errors too: ok, nothing classified.
	all := build(t, "demo", "", BuildOptions{All: true})
	if len(all.AnalysisLog) != 16 {
		t.Fatalf("%d records with All", len(all.AnalysisLog))
	}
	quiet := all.AnalysisLog[15]
	if quiet.Finding.Grade != "ok" || quiet.FaultMode != "" || quiet.RiskClassifier != "" || quiet.SeverityClassifier != "" || quiet.Corrected != 0 || len(quiet.Faults) != 0 {
		t.Errorf("a DIMM without errors: %+v", quiet)
	}
	for _, f := range quiet.NotFilled {
		if f == "FaultMode" {
			t.Errorf("a DIMM without errors lists FaultMode as not filled: %q", quiet.NotFilled)
		}
	}
}

// Each grade in the classes of the framework document.
func TestClassification(t *testing.T) {
	for _, tc := range []struct {
		scenario string
		flavor   synth.Flavor
		slot     string // "" takes the worst DIMM
		grade    model.VerdictGrade
		mode     string
		risk     string
		severity string
		system   bool
		action   string
		missing  []string // fields that must be listed as not filled
	}{
		{scenario: "row", grade: model.VerdictConsiderReplacement, mode: "Row fault", risk: RiskRepeated, severity: SeverityMedium, action: ActionRemoval},
		{scenario: "single-bit", grade: model.VerdictObserve, mode: "Single bit fault", risk: RiskLow, severity: SeverityLow},
		{scenario: "bank", grade: model.VerdictConsiderReplacement, mode: "Bank fault", risk: RiskRepeated, severity: SeverityMedium, action: ActionRemoval},
		{scenario: "scrub-ue", grade: model.VerdictConsiderReplacement, risk: RiskUE, severity: SeverityMedium, action: ActionRemoval},
		{scenario: "fw-disabled", grade: model.VerdictReplaceNow, severity: SeverityHigh, action: ActionRemoval},
		// Quiet since a repair: keep watching.
		{scenario: "repaired", grade: model.VerdictObserve, risk: RiskLow, severity: SeverityLow},
		// A cause outside the DIMM is suspected: a system issue, and no
		// class for a DIMM nobody knows to be at fault.
		{scenario: "system-channel", grade: model.VerdictHoldReplacement, system: true, missing: []string{"SeverityClassifier", "RiskClassifier"}},
		{scenario: "reseated", grade: model.VerdictHoldReplacement, system: true, missing: []string{"SeverityClassifier", "RiskClassifier"}},
		// A platform that reports no rows: the mode is an estimate, or
		// none, and the record says which fields it could not fill.
		{scenario: "demo", flavor: synth.FlavorAMD, grade: model.VerdictReplaceNow, risk: RiskUE, severity: SeverityHigh, action: ActionRemoval},
	} {
		name := tc.scenario
		if tc.flavor != "" {
			name += "-" + string(tc.flavor)
		}
		t.Run(name, func(t *testing.T) {
			doc := build(t, tc.scenario, tc.flavor, BuildOptions{})
			if len(doc.AnalysisLog) == 0 {
				t.Fatal("no analysis record")
			}
			a := &doc.AnalysisLog[0]
			if tc.slot != "" {
				a = bySlot(doc)[tc.slot]
			}
			if a.Finding.Grade != string(tc.grade) {
				t.Fatalf("grade %s, want %s (rules %v)", a.Finding.Grade, tc.grade, a.Finding.Rules)
			}
			if tc.mode != "" && a.FaultMode != tc.mode {
				t.Errorf("fault mode %q, want %q", a.FaultMode, tc.mode)
			}
			if a.RiskClassifier != tc.risk && !(tc.risk == "" && tc.grade != model.VerdictHoldReplacement) {
				t.Errorf("risk %q, want %q", a.RiskClassifier, tc.risk)
			}
			if a.SeverityClassifier != tc.severity || a.SystemIssue != tc.system {
				t.Errorf("severity %q, system issue %v; want %q, %v", a.SeverityClassifier, a.SystemIssue, tc.severity, tc.system)
			}
			var action string
			if len(a.RasActionSuggestion) > 0 {
				action = a.RasActionSuggestion[0].Action
			}
			if action != tc.action || len(a.RasActionSuggestion) > 1 {
				t.Errorf("suggestion %+v, want %q", a.RasActionSuggestion, tc.action)
			}
			for _, f := range tc.missing {
				found := false
				for _, x := range a.NotFilled {
					found = found || x == f
				}
				if !found {
					t.Errorf("%s is not listed as not filled: %q", f, a.NotFilled)
				}
			}
			// What is not filled is left out, and the other way round.
			for field, empty := range map[string]bool{
				"DimmSerialNumber": a.DimmSerialNumber == "", "RiskClassifier": a.RiskClassifier == "" && tc.grade == model.VerdictHoldReplacement,
				"SeverityClassifier": a.SeverityClassifier == "" && tc.grade == model.VerdictHoldReplacement,
				"FaultMode":          a.FaultMode == "" && a.Corrected+a.Uncorrected > 0,
			} {
				listed := false
				for _, x := range a.NotFilled {
					listed = listed || x == field
				}
				if listed != empty {
					t.Errorf("%s: empty %v, listed as not filled %v", field, empty, listed)
				}
			}
			// Never an action descriptor, whatever the finding.
			raw, _ := json.Marshal(doc)
			if bytes.Contains(bytes.ToLower(raw), []byte("cpad")) || doc.ActionDescriptors != "none" {
				t.Errorf("the log names an action descriptor")
			}
		})
	}
}

// Errors that came back after a repair are a fault that is still there
// after an action in the field: the class "high" of the framework
// document, whatever grade the finding has.
func TestRecurrenceAfterRepairIsHighSeverity(t *testing.T) {
	d := &engine.DIMMResult{UID: "d1", Host: "h1", Slot: "DIMM_A1", Present: true,
		DIMM: &model.DIMM{UID: "d1", Identity: model.IdentitySerial, Serial: "S1"},
		Verdict: &model.Verdict{Grade: model.VerdictConsiderReplacement, Label: model.VerdictLabel, Rules: []string{engine.RuleRecurred},
			NextAction: model.Statement{Code: "next.plan"}, Confidence: 70}}
	d.Features.CE = 12
	a := AnalysisOf(d, Options{Now: now})
	if a.SeverityClassifier != SeverityHigh || a.RiskClassifier != RiskRepeated || len(a.RasActionSuggestion) != 1 || a.RasActionSuggestion[0] != (Suggestion{ActionRemoval, "S/N S1, DIMM_A1"}) {
		t.Errorf("analysis: %+v", a)
	}
	// Without a finding at all: ok, and nothing classified.
	d.Verdict = nil
	if a = AnalysisOf(d, Options{Now: now}); a.Finding.Grade != "ok" || a.SeverityClassifier != "" || a.Finding.Rules == nil || a.RasActionSuggestion == nil {
		t.Errorf("analysis without a finding: %+v", a)
	}
}

// A host whose errors are known as counts only: no mode can be told, and
// the record says so in place of guessing.
func TestCountsOnly(t *testing.T) {
	s, err := store.Open(ctx, t.TempDir()+"/ledger.db", store.Create)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := now.Add(-time.Hour)
	if err := s.Update(ctx, func(w *store.Writer) error {
		if err := w.UpsertHost(&model.Host{Name: "h1", FirstSeen: at, LastSeen: at}); err != nil {
			return err
		}
		if err := w.UpsertSlot(&model.Slot{Host: "h1", Name: "DIMM_A1"}); err != nil {
			return err
		}
		_, err := w.InsertEvent(&model.Event{Host: "h1", Kind: model.KindCounter, ErrorDT: at, ErrorSeverity: model.SevCE, Source: "edac-sysfs/counter",
			RawRef: "mc0/dimm0", Grade: model.GradeC, Count: 40, TimeQuality: model.TimeExact, Label: "DIMM_A1", Slot: "DIMM_A1", Fingerprint: "c1"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.LinkDIMMs(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	res, err := engine.Run(ctx, s, engine.Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Build(ctx, s, res.Hosts[0], BuildOptions{Now: now, Records: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.AnalysisLog) != 1 {
		t.Fatalf("%d analysis records", len(doc.AnalysisLog))
	}
	a := doc.AnalysisLog[0]
	if a.FaultMode != "" || a.DimmSerialNumber != "" || a.Location.SocketId != nil || a.Observation != (Observation{GradeC: 40}) || a.Corrected != 40 {
		t.Errorf("record: %+v", a)
	}
	want := []string{"DimmSerialNumber", "Location.SocketId", "Location.ChannelId", "Location.ChannelSlotId", "Location.SubChannel", "FaultMode"}
	if !reflect.DeepEqual(a.NotFilled, want) {
		t.Errorf("not filled %q, want %q", a.NotFilled, want)
	}
	if len(a.Faults) != 1 || a.Faults[0].FaultMode != "" || a.Faults[0].Mode != "undetermined" || a.Faults[0].Grade != "C" {
		t.Errorf("faults: %+v", a.Faults)
	}
	// The runtime record of a count: 40 errors, grade C, no place inside
	// the DIMM.
	if len(doc.RuntimeLog) != 1 {
		t.Fatalf("%d runtime records", len(doc.RuntimeLog))
	}
	r := doc.RuntimeLog[0]
	if r.Count != 40 || r.Grade != "C" || r.Row != nil || r.Rank != nil || r.OsPhysicalAddress != "" || r.ErrorSeverity != "CE" || r.Slot != "DIMM_A1" {
		t.Errorf("runtime record: %+v", r)
	}
}

// The runtime log reads back through the FMFM adapter: what goes out in
// FMFM field names is what an analyzer that reads that form gets.
func TestRuntimeLogReadsBack(t *testing.T) {
	doc := build(t, "demo", "", BuildOptions{Records: true})
	if len(doc.RuntimeLog) != 1285 {
		t.Fatalf("%d runtime records, want the 1,285 errors of the demo host", len(doc.RuntimeLog))
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	var c adapter.Collected
	rep := fmfm.NewReader(&buf, "export.json", fmfm.Options{Now: now}).Collect(ctx, &c)
	if rep.Status != model.SourceOK || len(c.Events) != len(doc.RuntimeLog) {
		t.Fatalf("read back: %+v, %d events", rep, len(c.Events))
	}
	// The analysis records are no error records.
	if rep.Records != len(doc.RuntimeLog) {
		t.Errorf("%d records read back, %d written", rep.Records, len(doc.RuntimeLog))
	}
	s, _ := host(t, "demo", "")
	var want []*model.Event
	for e, err := range s.Events(ctx, store.EventFilter{}) {
		if err != nil {
			t.Fatal(err)
		}
		if e.Errors() > 0 {
			want = append(want, e)
		}
	}
	for i, e := range c.Events {
		w := want[i]
		if !e.ErrorDT.Equal(w.ErrorDT.Truncate(time.Millisecond)) || e.ErrorSeverity != w.ErrorSeverity || e.ErrorScrubType != w.ErrorScrubType ||
			e.SocketId != w.SocketId || e.ChannelId != w.ChannelId || e.ChannelSlotId != w.ChannelSlotId || e.Rank != w.Rank || e.DeviceId != w.DeviceId ||
			e.BankGroup != w.BankGroup || e.Bank != w.Bank || e.Row != w.Row || e.Column != w.Column || e.OsPhysicalAddress != w.OsPhysicalAddress || e.ErrorVector != w.ErrorVector {
			t.Fatalf("record %d\nread back %+v\nledger    %+v", i, e, w)
		}
	}
}

func TestSentencesFollowTheLanguage(t *testing.T) {
	doc := build(t, "demo", "", BuildOptions{Lang: i18n.KO})
	a := doc.AnalysisLog[0]
	if a.Finding.NextAction != "DIMM 교체. 이 DIMM의 케이스 번들을 반품에 첨부." || a.Finding.NextActionCode != "next.replace" || !strings.Contains(a.Finding.Evidence[0], "UE 1건") {
		t.Errorf("Korean finding: %+v", a.Finding)
	}
	// The words of the framework document stay as they are.
	if a.FaultMode != "Row fault" || a.RiskClassifier != RiskUE || a.RasActionSuggestion[0].Action != ActionRemoval {
		t.Errorf("framework words: %+v", a)
	}
}

func TestRuntimeOf(t *testing.T) {
	e := &model.Event{Host: "h1", ErrorDT: time.Date(2026, 11, 12, 3, 14, 5, 123e6, time.UTC), ErrorSeverity: model.SevUE, ErrorScrubType: model.ScrubPatrol,
		SocketId: model.Some(0), ChannelId: model.Some(2), Rank: model.Some(0), Row: model.Some(0x1a2f), OsPhysicalAddress: model.Some(uint64(1) << 53),
		ErrorVector: "bit:5", Count: 1, Slot: "CPU0_C1", Grade: model.GradeB, Source: "klog/ghes", RawRef: "dmesg#L7"}
	b, _ := json.Marshal(RuntimeOf(e))
	want := `{"ErrorDT":"2026-11-12T03:14:05.123Z","ErrorScrubType":"Patrol","ErrorSeverity":"UE","HostID":"h1","SocketId":0,"ChannelId":2,"Rank":0,"Row":6703,` +
		`"ErrorVector":"bit:5","OsPhysicalAddress":"0x20000000000000","slot":"CPU0_C1","grade":"B","source":"klog/ghes","raw_ref":"dmesg#L7"}`
	if string(b) != want {
		t.Errorf("runtime record\n got %s\nwant %s", b, want)
	}
	// A record that has seconds since boot and no date, and a count.
	e.TimeQuality, e.ErrorDT, e.Count = model.TimeRelative, time.Unix(90, 0), 3
	r := RuntimeOf(e)
	if r.ErrorDT != "" || r.SecondsSinceBoot == nil || *r.SecondsSinceBoot != 90 || r.Count != 3 {
		t.Errorf("relative record: %+v", r)
	}
}

// Stream writes what Build and Write write, byte for byte, without the
// runtime log in memory: with and without records, for a host with errors
// and for one whose runtime log is empty.
func TestStreamWritesTheSameBytes(t *testing.T) {
	for _, scenario := range []string{"demo", "row", "system-channel"} {
		for _, opts := range []BuildOptions{{}, {Records: true}, {Records: true, All: true, Lang: i18n.KO}} {
			s, h := host(t, scenario, "")
			opts.Now, opts.Tool, opts.Version = now, "MemorY", "test"
			doc, err := Build(ctx, s, h, opts)
			if err != nil {
				t.Fatal(err)
			}
			var want, got bytes.Buffer
			if err := doc.Write(&want); err != nil {
				t.Fatal(err)
			}
			head, n, err := Stream(ctx, &got, s, h, opts)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got.Bytes(), want.Bytes()) {
				a, b := got.String(), want.String()
				i := 0
				for i < len(a) && i < len(b) && a[i] == b[i] {
					i++
				}
				t.Fatalf("%s %+v: the stream differs from the document at byte %d of %d:\n stream   %q\n document %q",
					scenario, opts, i, len(b), a[max(0, i-60):min(len(a), i+60)], b[max(0, i-60):min(len(b), i+60)])
			}
			if n != len(doc.RuntimeLog) || len(head.AnalysisLog) != len(doc.AnalysisLog) || head.RuntimeLog != nil {
				t.Errorf("%s %+v: %d records streamed, %d in the document", scenario, opts, n, len(doc.RuntimeLog))
			}
			if opts.Records && scenario == "demo" && n != 1285 {
				t.Errorf("demo: %d records", n)
			}
		}
	}

	// A host without an error has no runtime log: the field is left out.
	s, h := host(t, "demo", "")
	empty := *h
	empty.Host = "no-such-host"
	var got bytes.Buffer
	if _, n, err := Stream(ctx, &got, s, &empty, BuildOptions{Records: true, Now: now}); err != nil || n != 0 || strings.Contains(got.String(), "RuntimeLog") ||
		!strings.HasSuffix(got.String(), "\n}\n") {
		t.Errorf("host without records: %d records, %v, ends %q", n, err, got.String()[max(0, got.Len()-40):])
	}

	// A writer that fails ends the stream with its error.
	if _, _, err := Stream(ctx, failing{}, s, h, BuildOptions{Records: true, Now: now}); err == nil {
		t.Error("a writer that fails gave no error")
	}
}

type failing struct{}

func (failing) Write([]byte) (int, error) { return 0, errors.New("disk full") }
