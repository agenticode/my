package eventstream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/fwevent"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/testledger"
)

var ctx = context.Background()

func TestMain(m *testing.M) { testledger.Main(m) }

// bmcLog is a made-up log of a BMC: each record is a common event about
// one module, in a log of the BMC.
type bmcLog []fwevent.Record

func (bmcLog) Kind() string   { return "redfish" }
func (bmcLog) Origin() string { return "made-up BMC log" }

func (l bmcLog) Collect(ctx context.Context, sink adapter.Sink) adapter.Report {
	rep := adapter.Report{Kind: "redfish", Origin: "made-up BMC log", Status: model.SourceOK}
	for i := range l {
		r := l[i]
		r.Fingerprint = fmt.Sprintf("bmc-%d", i)
		r.Source = "redfish/log"
		r.RawRef = fmt.Sprintf("log#%d", i)
		r.Raw = fmt.Sprintf(`{"Id":"%d","Message":"made-up entry"}`, i)
		if r.Firmware.Log == "" {
			r.Firmware.Log = "EventLog"
		}
		r.Firmware.VendorID, r.Firmware.Registry, r.Firmware.Vendor = fmt.Sprintf("EXM%04d", i+1), "ExampleEvent", "Example"
		r.Firmware.Mapping, r.Firmware.MapRef, r.Firmware.Basis = model.MappingTable, "example@1:"+string(r.Event), "registry"
		r.Firmware.Origin = model.OriginBMCLog
		rep.Records++
		if err := fwevent.Emit(sink, &r); err != nil {
			rep.SinkErr = err
			return rep
		}
	}
	return rep
}

// mixed is the crash loop with a log of its BMC: one uncorrected error
// in two logs of the BMC (one counts), a prediction a later record takes
// back, a repair, and the log cleared.
func mixed(t *testing.T) *store.Store {
	t.Helper()
	ds := testledger.Generate(t, "crash-loop", 1, nil)
	at := func(day, hour, min int) time.Time { return time.Date(2026, 11, day, hour, min, 0, 0, time.UTC) }
	log := bmcLog{
		{Event: fwevent.UE, At: at(12, 8, 40), Labels: []string{"CPU0_C1"}, Text: "uncorrectable ECC"},
		{Event: fwevent.UE, At: at(12, 8, 40), Labels: []string{"CPU0_C1"}, Text: "uncorrectable ECC", Firmware: model.FirmwareRecord{Log: "SEL"}},
		{Event: fwevent.PredictedFailure, At: at(12, 9, 30), Labels: []string{"CPU1_D1"}},
		{Event: fwevent.Cleared, Clears: fwevent.PredictedFailure, At: at(12, 9, 40), Labels: []string{"CPU1_D1"}},
		{Event: fwevent.PPRDone, At: at(12, 10, 0), Labels: []string{"CPU0_C1"}},
		{Event: fwevent.LogCleared, At: at(12, 10, 5)},
	}
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := ingest.Run(ctx, s, []adapter.Adapter{ds.Adapter(), log}, ingest.Options{Kind: model.ScanSynthetic, Now: ds.Scenario.End}); err != nil {
		t.Fatal(err)
	}
	return s
}

// lines writes the stream of the only host of a ledger and reads it back.
func lines(t *testing.T, s *store.Store, opts Options) ([]Line, []map[string]any) {
	t.Helper()
	hosts, err := s.Hosts(ctx)
	if err != nil || len(hosts) != 1 {
		t.Fatalf("hosts %v, %v", hosts, err)
	}
	var b bytes.Buffer
	n, err := Write(ctx, &b, s, hosts[0].Name, opts)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.TrimSuffix(b.String(), "\n")
	if text == "" {
		if n != 0 {
			t.Fatalf("%d lines counted, none written", n)
		}
		return nil, nil
	}
	var out []Line
	var raw []map[string]any
	for i, line := range strings.Split(text, "\n") {
		var l Line
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &l); err != nil {
			t.Fatalf("line %d is no JSON: %v\n%s", i+1, err, line)
		}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, l)
		raw = append(raw, m)
	}
	if n != len(out) {
		t.Errorf("%d lines counted, %d written", n, len(out))
	}
	return out, raw
}

func TestStream(t *testing.T) {
	s := mixed(t)
	all, _ := lines(t, s, Options{})

	// Every event and action of the host, and the firmware record alone.
	hosts, _ := s.Hosts(ctx)
	var events int
	for _, err := range s.Events(ctx, store.EventFilter{Host: hosts[0].Name, WithDuplicates: true}) {
		if err != nil {
			t.Fatal(err)
		}
		events++
	}
	acts, err := s.Actions(ctx, store.ActionFilter{Host: hosts[0].Name, Duplicates: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != events+len(acts)+1 {
		t.Errorf("%d lines for %d events, %d actions and 1 firmware record", len(all), events, len(acts))
	}

	// Oldest first; one schema; nothing without a fingerprint.
	var last time.Time
	kinds := map[string]int{}
	for i, l := range all {
		if l.Schema != Schema || l.Host == "" || l.Fingerprint == "" || l.Origin == "" || l.Source == "" || l.Event == "" {
			t.Errorf("line %d: %+v", i+1, l)
		}
		if l.TimeQuality == string(model.TimeRelative) {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, l.Time)
		if err != nil {
			t.Fatalf("line %d: time %q", i+1, l.Time)
		}
		if at.Before(last) {
			t.Errorf("line %d at %s comes after %s", i+1, at, last)
		}
		last = at
		kinds[l.Origin+" "+l.Record+" "+l.Event]++
		if l.Raw != "" {
			t.Errorf("line %d has its raw text without --raw", i+1)
		}
	}
	// The synthetic crash loop records its stops in machine check banks,
	// not as panic lines: TestHostEvent covers stop and panic.
	for _, want := range []string{"os event ce", "os event scrub-ce", "os event ue", "bmc-log event ue", "bmc-log action predicted-failure",
		"bmc-log event cleared", "bmc-log action ppr-done", "bmc-log firmware log-cleared"} {
		if kinds[want] == 0 {
			t.Errorf("no line %q in %v", want, kinds)
		}
	}

	// The error in two logs: both lines, the second points at the first.
	byFP := map[string]Line{}
	for _, l := range all {
		byFP[l.Fingerprint] = l
	}
	var dups, withdrawn int
	for _, l := range all {
		if l.DupOf != "" {
			dups++
			if _, ok := byFP[l.DupOf]; !ok {
				t.Errorf("%s is a duplicate of %s, which is not in the stream", l.Fingerprint, l.DupOf)
			}
		}
		if l.WithdrawnBy != "" {
			withdrawn++
			if o, ok := byFP[l.WithdrawnBy]; !ok || o.Event != "cleared" || l.Event != "predicted-failure" {
				t.Errorf("%+v is withdrawn by %+v", l, o)
			}
		}
		if l.Origin == "bmc-log" && l.Record != RecordFirmware && (l.Vendor == nil || l.Vendor.ID == "" || l.Mapping == nil || l.Mapping.By != "table") {
			t.Errorf("a BMC line without its vendor or mapping: %+v", l)
		}
		if l.Event == "ce" && l.Origin == "os" && l.Place == nil {
			t.Errorf("a located host error without a place: %+v", l)
		}
	}
	if dups == 0 || withdrawn != 1 {
		t.Errorf("%d duplicates, %d marks withdrawn", dups, withdrawn)
	}

	// --raw adds the raw text; --since cuts by time.
	withRaw, _ := lines(t, s, Options{Raw: true})
	n := 0
	for _, l := range withRaw {
		if l.Raw != "" {
			n++
		}
	}
	if n == 0 || len(withRaw) != len(all) {
		t.Errorf("--raw: %d of %d lines with raw text", n, len(withRaw))
	}
	since := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	cut, _ := lines(t, s, Options{Since: since})
	want := 0
	for _, l := range all {
		if at, err := time.Parse(time.RFC3339Nano, l.Time); err == nil && !at.Before(since) {
			want++
		}
	}
	if len(cut) != want || want == 0 {
		t.Errorf("--since: %d lines, want %d", len(cut), want)
	}
}

// TestSameLedgerSameStream: the stream of a ledger is the same every time.
func TestSameLedgerSameStream(t *testing.T) {
	s := testledger.Open(t, "demo", 1, "")
	hosts, _ := s.Hosts(ctx)
	var a, b bytes.Buffer
	if _, err := Write(ctx, &a, s, hosts[0].Name, Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(ctx, &b, s, hosts[0].Name, Options{}); err != nil {
		t.Fatal(err)
	}
	if a.Len() == 0 || !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Errorf("two streams of one ledger differ (%d and %d bytes)", a.Len(), b.Len())
	}
}

// TestCancel: a stream stops when its context ends.
func TestCancel(t *testing.T) {
	s := testledger.Open(t, "demo", 1, "")
	hosts, _ := s.Hosts(ctx)
	c, cancel := context.WithCancel(ctx)
	cancel()
	var b bytes.Buffer
	if _, err := Write(c, &b, s, hosts[0].Name, Options{}); err == nil {
		t.Error("a cancelled stream ran to its end")
	}
}

// TestHostEvent: the event of a record of the host's own logs.
func TestHostEvent(t *testing.T) {
	for _, tt := range []struct {
		e    model.Event
		want string
	}{
		{model.Event{ErrorSeverity: model.SevCE}, "ce"},
		{model.Event{ErrorSeverity: model.SevCE, ErrorScrubType: model.ScrubPatrol}, "scrub-ce"},
		{model.Event{ErrorSeverity: model.SevFatal}, "ue"},
		{model.Event{ErrorSeverity: model.SevRecoverable, ErrorScrubType: model.ScrubPatrol}, "scrub-ue"},
		{model.Event{Kind: model.KindCounter, ErrorSeverity: model.SevCE}, "counter"},
		{model.Event{ErrorSeverity: model.SevInformational, ErrorType: model.ErrorTypeStop}, "stop"},
		{model.Event{ErrorSeverity: model.SevInformational, ErrorType: model.ErrorTypePanic}, "panic"},
		{model.Event{ErrorSeverity: model.SevInformational}, "informational"},
	} {
		if got := hostEvent(&tt.e); got != tt.want {
			t.Errorf("%+v: %q, want %q", tt.e, got, tt.want)
		}
	}
}

// TestRelativeTime: a record that knows only the seconds since its boot
// has no date in the stream.
func TestRelativeTime(t *testing.T) {
	e := &model.Event{ErrorDT: time.Unix(4242, 0), TimeQuality: model.TimeRelative, ErrorSeverity: model.SevCE, Fingerprint: "f", Source: "klog"}
	l := EventLine("h", e, nil, nil, store.RecordLinks{}, false)
	if l.Time != "" || l.UptimeSeconds != 4242 || l.TimeQuality != "relative" || l.Origin != OriginOS {
		t.Errorf("%+v", l)
	}
}
