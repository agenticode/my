package redfish

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/testutil"
)

var (
	ctx = context.Background()
	now = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
)

func collect(t *testing.T, name string) (*adapter.Collected, adapter.Report) {
	t.Helper()
	var c adapter.Collected
	rep := New(testutil.Testdata(t, "redfish", name), Options{Now: now}).Collect(ctx, &c)
	return &c, rep
}

// got is one event or action in a form that is easy to compare.
type got struct {
	Ref   string // the last segment of the raw reference
	What  string
	Label string
	At    string
	Extra string
}

func summary(c *adapter.Collected) []got {
	var out []got
	short := func(ref string) string { return ref[strings.LastIndex(ref, "/")+1:] }
	for _, e := range c.Events {
		extra := string(e.Grade) + " " + e.Decoder
		if e.SocketId.Valid || e.ChannelId.Valid || e.ChannelSlotId.Valid || e.Rank.Valid {
			extra += " place " + e.SocketId.String() + "/" + e.ChannelId.String() + "/" + e.ChannelSlotId.String() + " rank " + e.Rank.String()
		}
		out = append(out, got{short(e.RawRef), string(e.ErrorSeverity) + ": " + e.ErrorType, e.Label, e.ErrorDT.Format("2006-01-02 15:04:05"), extra})
	}
	for _, a := range c.Actions {
		out = append(out, got{short(a.RawRef), "action " + string(a.Kind) + "/" + string(a.Result) + ": " + a.Detail, a.Label,
			a.At.Format("2006-01-02 15:04:05"), "by " + a.Method})
	}
	return out
}

func check(t *testing.T, c *adapter.Collected, want []got) {
	t.Helper()
	have := summary(c)
	if !reflect.DeepEqual(have, want) {
		t.Errorf("records differ")
		for _, g := range have {
			t.Logf("  have %+v", g)
		}
		for _, g := range want {
			t.Logf("  want %+v", g)
		}
	}
	for _, e := range c.Events {
		if e.Source == "" || e.Fingerprint == "" || e.Raw == "" || e.Count != 1 || e.TimeQuality != model.TimeExact {
			t.Errorf("event %s: source %q, count %d, time quality %q", e.RawRef, e.Source, e.Count, e.TimeQuality)
		}
	}
	for _, a := range c.Actions {
		if a.Source == "" || a.Fingerprint == "" || a.Raw == "" {
			t.Errorf("action %s lacks source, fingerprint or raw text", a.RawRef)
		}
	}
}

func wantNotes(t *testing.T, rep adapter.Report, parts ...string) {
	t.Helper()
	all := strings.Join(rep.Notes, "\n")
	for _, p := range parts {
		if !strings.Contains(all, p) {
			t.Errorf("notes lack %q:\n  %s", p, strings.Join(rep.Notes, "\n  "))
		}
	}
}

func TestTree(t *testing.T) {
	c, rep := collect(t, "bmc-tree")
	at := now.Format("2006-01-02 15:04:05")
	check(t, c, []got{
		{"dimm2", "Informational: module health Warning: State Enabled; Health Warning", "DIMM_C1", at, "C redfish/status"},
		// The numbers in the messages are the numbers the memory resources
		// of the same dump give their slots.
		{"1", "CE: correctable ECC", "DIMM_A1", "2026-11-02 14:07:55", "B redfish/openbmc place 0/0/0 rank 1"},
		{"2", "CE: correctable ECC", "DIMM_A1", "2026-11-09 22:41:03", "B redfish/openbmc place 0/0/0 rank 1"},
		{"3", "UE: uncorrectable ECC", "DIMM_B1", "2026-11-12 03:14:07", "B redfish/openbmc place 0/1/0 rank 0"},
		{"6", "UE: parity error", "", "2026-11-12 03:22:00", "C redfish/openbmc place 1/-/- rank -"},
		{"7", "Informational: Memory RAS Mode Select Enabled. Prior Mode=None Selected Mode=Sparing.", "", "2026-11-12 03:23:00", "C redfish/openbmc"},
		{"10", "Informational: The resource property CorrectableECCErrorCount has exceeded error threshold of value 100.", "DIMM_C1", "2026-11-13 11:00:00", "C "},
		{"dimm1", "action disable/ok: State Disabled; Health Critical; The resource property DIMM_B1 has detected errors of type 'uncorrectable ECC'.", "DIMM_B1", at, "by bmc"},
		{"dimm1", "action mark-failed/ok: State Disabled; Health Critical; The resource property DIMM_B1 has detected errors of type 'uncorrectable ECC'.", "DIMM_B1", at, "by bmc"},
		{"4", "action sparing/ok: spare", "DIMM_B1", "2026-11-12 03:20:00", "by bmc"},
	})
	if rep.Status != model.SourceOK || rep.Records != 15 || rep.Skipped != 0 || rep.HostHint != "r12-n034" || rep.Kind != Kind {
		t.Errorf("report = %+v", rep)
	}
	wantNotes(t, rep,
		"1 files that hold no JSON were left out",
		"7 JSON files read",
		"4 memory slots, 3 populated",
		"1 modules are reported as disabled by the BMC",
		"1 modules are reported as failed by the BMC",
		"1 modules are reported as warning by the BMC",
		"10 log entries read",
		"1 log entries are not about memory",
		"1 log entries of type CXL were not read",
		"1 entries only say that a state ended",
		`1 entries about memory say "OpenBMC.0.1.MemoryRASModeEnabled"`,
		`1 entries about memory say "ResourceEvent.1.3.ResourceErrorThresholdExceeded"`,
		"1 memory entries do not name a module",
	)

	if len(c.Hosts) != 1 || c.Hosts[0].SystemVendor != "Example Systems" || c.Hosts[0].SystemProduct != "R650" {
		t.Errorf("hosts = %+v", c.Hosts)
	}
	if len(c.Modules) != 4 {
		t.Fatalf("%d modules, want 4", len(c.Modules))
	}
	a1 := c.Modules[0]
	want := model.Module{ObservedAt: now, Locator: "DIMM_A1", Socket: model.Some(0), Channel: model.Some(0), Position: model.Some(0), Populated: true,
		Source: "redfish", RawRef: a1.RawRef,
		DIMM: model.DIMM{Manufacturer: "Example Memory", PartNumber: "EXM64G48R2X4-A", Serial: "80A1B0", ManufacturerID: "0x2C80", SizeBytes: 64 << 30,
			MemoryType: "DDR5", ModuleType: "RDIMM", Technology: "DRAM", Ranks: model.Some(2), DataWidth: model.Some(64), TotalWidth: model.Some(80),
			SpeedMTs: model.Some(5600), ConfiguredSpeedMTs: model.Some(4800), MfgDate: "2025-W25"}}
	if !reflect.DeepEqual(*a1, want) {
		t.Errorf("DIMM_A1 = %+v\n   want %+v", *a1, want)
	}
	if !strings.HasSuffix(a1.RawRef, "redfish/v1/Systems/system/Memory/dimm0/index.json#/redfish/v1/Systems/system/Memory/dimm0") {
		t.Errorf("raw reference = %q", a1.RawRef)
	}
	d1 := c.Modules[3]
	if d1.Locator != "DIMM_D1" || d1.Populated || d1.DIMM != (model.DIMM{}) || d1.Channel != model.Some(3) {
		t.Errorf("empty slot = %+v", d1)
	}
	// The whole resource is the raw record, vendor extensions included.
	if raw := c.Actions[0].Raw; !strings.Contains(raw, `"Conditions"`) || !strings.Contains(raw, `"SerialNumber":"80A1C3"`) {
		t.Errorf("raw record of the mark = %s", raw)
	}
}

func TestSELEntries(t *testing.T) {
	c, rep := collect(t, "sel-log.json")
	check(t, c, []got{
		{"1", "CE: correctable ECC", "sensor 0x53 module 3", "2026-11-12 03:14:07", "C redfish/sel"},
		{"2", "UE: uncorrectable ECC", "P1-DIMMB1", "2026-11-12 03:14:09", "C redfish/sel"},
		// Entry 5 says the module of entry 3 is in use again.
		{"3", "Informational: memory device disabled (enabled again later)", "DIMM_C1", "2026-11-12 03:20:00", "C redfish/sel"},
		{"5", "Informational: memory device disabled (deasserted: in use again)", "DIMM_C1", "2026-11-12 03:40:00", "C redfish/sel"},
		{"7", "Informational: correctable ECC logging limit reached", "DIMM_A1", "2026-11-12 04:00:00", "C redfish/sel"},
	})
	if rep.Status != model.SourcePartial || rep.Records != 7 || rep.Skipped != 1 {
		t.Errorf("report = %+v", rep)
	}
	wantNotes(t, rep, "Entries/6: log entry about memory has no readable time", "1 log entries are not about memory",
		"1 disable entries were followed by the module coming back into use", "the firmware stopped logging corrected errors 1 time(s), first on 2026-11-12 04:00 UTC")
}

// TestSELDisableStands reads the same log without the entry that withdraws
// the disable: the module stays out of use, by the BIOS's doing.
func TestSELDisableStands(t *testing.T) {
	data := testutil.ReadTestdata(t, "redfish", "sel-log.json")
	cut := bytes.Replace(data, []byte(`"EntryCode": "Deassert"`), []byte(`"EntryCode": "Assert"`), 1)
	cut = bytes.Replace(cut, []byte(`"MessageId": "0xef04ffff"`), []byte(`"MessageId": "0x6f00ffff"`), 1)
	var c adapter.Collected
	rep := NewReader(bytes.NewReader(cut), "sel.json", Options{Now: now}).Collect(ctx, &c)
	if len(c.Actions) != 1 || rep.Origin != "sel.json" {
		t.Fatalf("actions = %+v, report %+v", c.Actions, rep)
	}
	a := c.Actions[0]
	if a.Kind != model.ActionDisable || a.Method != "bios" || a.Label != "DIMM_C1" || a.Source != "redfish/log" || a.RawRef != "sel.json#/redfish/v1/Managers/1/LogServices/SEL/Entries/3" {
		t.Errorf("action = %+v", a)
	}
}

func TestRegistryEntries(t *testing.T) {
	c, rep := collect(t, "registry-log.json")
	check(t, c, []got{
		{"101", "CE: correctable ECC", "DIMM_A1", "2026-11-05 19:02:44", "C redfish/registry"},
		{"102", "UE: uncorrectable ECC on one of DIMM_A1, DIMM_A2", "", "2026-11-05 19:40:10", "C redfish/registry"},
		{"105", "Informational: Memory self-test completed on DIMM_A1.", "DIMM_A1", "2026-11-06 14:05:00", "C redfish/registry"},
		{"103", "action disable/ok: memory device disabled", "DIMM_A1", "2026-11-05 19:41:00", "by bmc"},
		{"108", "action sparing/ok: spare", "DIMM_B3", "2026-11-06 15:00:00", "by bmc"},
	})
	if rep.Status != model.SourceOK || rep.Records != 8 {
		t.Errorf("report = %+v", rep)
	}
	wantNotes(t, rep, "1 log entries are not about memory", "1 presence entries were left out", "1 entries only say that a state ended",
		`1 entries about memory say "MEM9999"`, "1 error entries name more than one module")
}

func TestVendorText(t *testing.T) {
	c, rep := collect(t, "vendor-text-log.json")
	check(t, c, []got{
		{"12", "UE: uncorrectable ECC", "PROC 1 DIMM 4", "2026-11-09 04:55:37", "C redfish/text"},
		{"13", "CE: correctable ECC", "PROC 2 DIMM 12", "2026-11-09 05:10:00", "C redfish/text"},
		{"Members[3]", "Informational: Memory training took longer than expected (Processor 2, Memory Module 12)", "PROC 2 DIMM 12", "2026-11-09 05:30:00", "C redfish/text"},
	})
	if rep.Status != model.SourceOK || rep.Records != 4 {
		t.Errorf("report = %+v", rep)
	}
}

func TestExpandedMemoryCollection(t *testing.T) {
	c, rep := collect(t, "memory-expanded.json")
	if len(c.Modules) != 3 || rep.Status != model.SourceOK || rep.Records != 3 {
		t.Fatalf("%d modules, report %+v", len(c.Modules), rep)
	}
	m4, m5, m6 := c.Modules[0], c.Modules[1], c.Modules[2]
	// The memory controller in the location is left out: socket, channel
	// and slot are the place.
	if m4.Locator != "PROC 1 DIMM 4" || m4.Socket != model.Some(1) || m4.Channel != model.Some(4) || m4.Position != model.Some(4) || m4.DIMM.MemoryType != "DDR4" {
		t.Errorf("module 4 = %+v", m4)
	}
	if m5.DIMM.Technology != "NVDIMM-N" || m5.DIMM.ModuleType != "SO-DIMM" || m5.DIMM.MemoryType != "DDR4" || m5.Socket.Valid {
		t.Errorf("module 5 = %+v", m5)
	}
	// A resource saved without its type, with a placeholder serial number
	// and no capacity: an empty slot.
	if m6.Locator != "PROC 1 DIMM 6" || m6.Populated {
		t.Errorf("module 6 = %+v", m6)
	}
	check(t, c, []got{
		{"proc1dimm5", "action disable/ok: State Enabled; Health OK", "PROC 1 DIMM 5", now.Format("2006-01-02 15:04:05"), "by bmc"},
	})
	wantNotes(t, rep, "3 memory slots, 2 populated", "1 MemoryMetrics resources were not read")
}

// The mark on a module is one fact however often the module is read.
func TestMarkFingerprintDoesNotDependOnTheTimeOfTheDump(t *testing.T) {
	path := testutil.Testdata(t, "redfish", "bmc-tree")
	var a, b adapter.Collected
	New(path, Options{Now: now}).Collect(ctx, &a)
	New(path, Options{Now: now.Add(72 * time.Hour)}).Collect(ctx, &b)
	if len(a.Actions) != 3 || len(b.Actions) != 3 {
		t.Fatalf("%d and %d actions", len(a.Actions), len(b.Actions))
	}
	for i := range a.Actions {
		if a.Actions[i].Fingerprint != b.Actions[i].Fingerprint {
			t.Errorf("action %d has another fingerprint in a later dump", i)
		}
	}
	if a.Actions[0].Fingerprint == a.Actions[1].Fingerprint {
		t.Error("the disable and the failure mark of one module share a fingerprint")
	}
	for i := range a.Events {
		if a.Events[i].Fingerprint != b.Events[i].Fingerprint {
			t.Errorf("event %s has another fingerprint in a later dump", a.Events[i].RawRef)
		}
	}
}

func TestObservationTime(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "memory.json")
	if err := os.WriteFile(file, testutil.ReadTestdata(t, "redfish", "memory-expanded.json"), 0o600); err != nil {
		t.Fatal(err)
	}
	saved := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	if err := os.Chtimes(file, saved, saved); err != nil {
		t.Fatal(err)
	}
	for name, a := range map[string]*Adapter{"file": New(file, Options{ModTimeAsObserved: true}), "directory": New(dir, Options{ModTimeAsObserved: true})} {
		var c adapter.Collected
		a.Collect(ctx, &c)
		if len(c.Modules) != 3 || !c.Modules[0].ObservedAt.Equal(saved) || !c.Actions[0].At.Equal(saved) {
			t.Errorf("%s: observed at %v, want the time the file was saved", name, c.Modules[0].ObservedAt)
		}
	}
	// Without either option the time of the read is used.
	var c adapter.Collected
	before := time.Now().Add(-time.Second)
	New(file, Options{}).Collect(ctx, &c)
	if c.Modules[0].ObservedAt.Before(before) {
		t.Errorf("observed at %v", c.Modules[0].ObservedAt)
	}
}

func TestInputsThatAreNotADump(t *testing.T) {
	dir := t.TempDir()
	read := func(content string) adapter.Report {
		var c adapter.Collected
		rep := NewReader(strings.NewReader(content), "input", Options{Now: now}).Collect(ctx, &c)
		if n := len(c.Events) + len(c.Actions) + len(c.Modules); n != 0 {
			t.Errorf("%d records from %q", n, content)
		}
		return rep
	}
	for name, c := range map[string]struct {
		content string
		status  model.SourceStatus
		detail  string
	}{
		"empty":        {"", model.SourceEmpty, "no log entries, memory resources or systems found"},
		"other json":   {`{"hello": ["world", {"a": 1}]}`, model.SourceEmpty, "no log entries"},
		"service root": {`{"@odata.type": "#ServiceRoot.v1_15_0.ServiceRoot", "Systems": {"@odata.id": "/redfish/v1/Systems"}}`, model.SourceEmpty, "no log entries"},
		"broken":       {`{"Members": [`, model.SourceError, "no file could be read as JSON"},
		"text":         {"   1 | 11/12/2026 | 03:14:07 | Memory DIMM_A1 | Correctable ECC | Asserted\n", model.SourceError, "no file could be read as JSON"},
	} {
		rep := read(c.content)
		if rep.Status != c.status || !strings.Contains(rep.Detail, c.detail) {
			t.Errorf("%s: status %s, detail %q; want %s, %q", name, rep.Status, rep.Detail, c.status, c.detail)
		}
	}

	var c adapter.Collected
	if rep := New(filepath.Join(dir, "missing"), Options{}).Collect(ctx, &c); rep.Status != model.SourceAbsent {
		t.Errorf("missing path: %+v", rep)
	}
	if rep := New(dir, Options{}).Collect(ctx, &c); rep.Status != model.SourceEmpty || rep.Detail != "no JSON files" {
		t.Errorf("empty directory: %+v", rep)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if rep := New(testutil.Testdata(t, "redfish", "bmc-tree"), Options{}).Collect(cancelled, &c); rep.Status != model.SourceError {
		t.Errorf("cancelled context: %+v", rep)
	}

	// A tree with one good file and one broken one: the good one is read,
	// the broken one is named.
	fsys := fstest.MapFS{
		"a/good.json":   {Data: testutil.ReadTestdata(t, "redfish", "vendor-text-log.json")},
		"a/broken.json": {Data: []byte(`{"Members": [`)},
		"notes.txt":     {Data: []byte("not JSON")},
	}
	var good adapter.Collected
	rep := NewFS(fsys, "dump", Options{Now: now}).Collect(ctx, &good)
	if rep.Status != model.SourcePartial || rep.Skipped != 1 || len(good.Events) != 3 {
		t.Errorf("mixed tree: %+v, %d events", rep, len(good.Events))
	}
	wantNotes(t, rep, "dump/a/broken.json: not readable as JSON", "1 files that hold no JSON were left out")
	if good.Events[0].RawRef != "dump/a/good.json#/redfish/v1/Systems/1/LogServices/IML/Entries/12" {
		t.Errorf("raw reference = %q", good.Events[0].RawRef)
	}
}

// A resource that is in the dump twice, as a member of an expanded
// collection and as a file of its own, is read once.
func TestResourceSeenTwice(t *testing.T) {
	entry := `{"@odata.id": "/redfish/v1/Systems/1/LogServices/Log/Entries/1", "@odata.type": "#LogEntry.v1_9_0.LogEntry", "Id": "1",
 "EntryType": "SEL", "SensorType": "Memory", "Created": "2026-11-12T03:14:07Z", "Message": "Uncorrectable ECC"}`
	fsys := fstest.MapFS{
		"Entries/index.json":   {Data: []byte(`{"@odata.type": "#LogEntryCollection.LogEntryCollection", "Members": [` + entry + `]}`)},
		"Entries/1/index.json": {Data: []byte(entry)},
		// Concatenated documents in one file are read one after the other.
		"stream.json": {Data: []byte(entry + "\n" + strings.Replace(entry, "Entries/1", "Entries/2", 1))},
	}
	var c adapter.Collected
	rep := NewFS(fsys, "dump", Options{Now: now}).Collect(ctx, &c)
	if rep.Status != model.SourceOK || rep.Records != 2 || len(c.Events) != 2 {
		t.Errorf("report %+v, %d events; want 2 entries", rep, len(c.Events))
	}
}

type failing struct {
	adapter.Collected
	err error
}

func (f *failing) Action(*model.Action) error { return f.err }

func TestSinkErrorStopsTheRead(t *testing.T) {
	boom := errors.New("ledger is full")
	rep := New(testutil.Testdata(t, "redfish", "bmc-tree"), Options{Now: now}).Collect(ctx, &failing{err: boom})
	if rep.Status != model.SourceError || !errors.Is(rep.SinkErr, boom) {
		t.Errorf("report = %+v", rep)
	}
}

func TestSniff(t *testing.T) {
	for _, name := range []string{"sel-log.json", "registry-log.json", "vendor-text-log.json", "memory-expanded.json",
		"bmc-tree/redfish/v1/Systems/system/index.json", "bmc-tree/redfish/v1/Systems/system/Memory/dimm0/index.json"} {
		if !Sniff(testutil.ReadTestdata(t, "redfish", name)) {
			t.Errorf("%s is not recognised", name)
		}
	}
	for name, data := range map[string]string{
		"other json": `{"entries": [{"id": 1, "sensor_type": "Memory"}]}`,
		"journal":    `{"MESSAGE":"EDAC MC0: 1 CE","_BOOT_ID":"0123"}`,
		"text":       "@odata.type is mentioned in this text",
		"empty":      "",
	} {
		if Sniff([]byte(data)) {
			t.Errorf("%s is taken for a Redfish dump", name)
		}
	}
}
