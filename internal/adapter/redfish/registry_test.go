package redfish

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/fwevent"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/testutil"
)

func entryJSON(id int, extra map[string]any) []byte {
	e := map[string]any{"@odata.type": "#LogEntry.v1_22_0.LogEntry", "@odata.id": fmt.Sprintf("/redfish/v1/Systems/1/LogServices/EventLog/Entries/%d", id),
		"Id": fmt.Sprint(id), "EntryType": "Event", "Created": fmt.Sprintf("2026-11-12T03:%02d:00Z", id)}
	for k, v := range extra {
		e[k] = v
	}
	b, _ := json.Marshal(e)
	return b
}

func readFixture(t *testing.T, elem ...string) []byte {
	t.Helper()
	b, err := os.ReadFile(testutil.Testdata(t, append([]string{"redfish"}, elem...)...))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// exampleRegistry is a vendor registry written by hand: messages a table
// does not name, one about memory in its registry text only.
const exampleRegistry = `{"RegistryPrefix": "ExampleCo", "RegistryVersion": "1.0.0", "OwningEntity": "Example Co", "Language": "en",
 "Messages": {
  "MemFault7": {"Message": "Post package repair of DIMM %1 failed.", "MessageSeverity": "OK", "NumberOfArgs": 1},
  "MemFault8": {"Message": "Something about memory module %1.", "Severity": "Warning", "NumberOfArgs": 1},
  "Fan1": {"Message": "Fan %1 is slow.", "NumberOfArgs": 1}}}`

// TestRegistriesInADump: the message registries of a dump are recognised
// with and without their type, tell the classifier what a key no table
// names means, and are recorded with what this version classes of them.
func TestRegistriesInADump(t *testing.T) {
	untyped := regexp.MustCompile(`"@odata.type": "#MessageRegistry[^"]*",`).ReplaceAllString(string(readFixture(t, "registries", "dmtf", "LogService.1.2.1.json")), "")
	if strings.Contains(untyped, "#MessageRegistry") {
		t.Fatal("the type of the LogService registry is still there")
	}
	fsys := fstest.MapFS{
		"Registries/Platform.json":      {Data: readFixture(t, "registries", "dmtf", "Platform.1.5.0.json")},
		"Registries/LogService.json":    {Data: []byte(untyped)},
		"Registries/ResourceEvent.json": {Data: readFixture(t, "registries", "dmtf", "ResourceEvent.1.5.0.json")},
		"Registries/OpenBMC.json":       {Data: readFixture(t, "registries", "openbmc", "OpenBMC.0.5.0.json")},
		"Registries/ExampleCo.json":     {Data: []byte(exampleRegistry)},
		"Registries/Broken.json":        {Data: []byte(`{"@odata.type": "#MessageRegistry.v1_6_2.MessageRegistry", "Messages": {}}`)},
		"Log/1.json": {Data: entryJSON(1, map[string]any{"MessageId": "ExampleCo.1.0.MemFault7", "MessageArgs": []string{"DIMM_A1"},
			"Message": "Event 7 for DIMM_A1."})},
		"Log/2.json": {Data: entryJSON(2, map[string]any{"MessageId": "ExampleCo.1.0.MemFault8", "MessageArgs": []string{"DIMM_A2"},
			"Message": "Event 8 for DIMM_A2."})},
		"Log/3.json": {Data: entryJSON(3, map[string]any{"MessageId": "Platform.1.5.MemoryUncorrectableError", "MessageArgs": []string{"DIMM_A3"},
			"Message": "Memory device 'DIMM_A3' detected an uncorrectable error."})},
	}
	var c adapter.Collected
	rep := NewFS(fsys, "dump", Options{Now: now}).Collect(ctx, &c)
	if rep.Status != model.SourceOK {
		t.Fatalf("report: %+v", rep)
	}
	byRef := map[string]*model.Event{}
	for _, e := range c.Events {
		byRef[lastSegment(e.RawRef)] = e
	}
	for _, a := range c.Actions {
		if strings.HasSuffix(a.RawRef, "/1") && (a.Kind != model.ActionPPR || a.Result != model.ResultFailed || a.Firmware.Mapping != model.MappingRegistry ||
			a.Firmware.VendorSeverity != "OK" || a.Label != "DIMM_A1") {
			t.Errorf("a key no table has, read by the registry's text: %+v %+v", a, a.Firmware)
		}
	}
	if len(c.Actions) != 1 {
		t.Errorf("%d actions, want the repair of entry 1", len(c.Actions))
	}
	if e := byRef["2"]; e == nil || e.Firmware.Common != string(fwevent.Unclassified) || e.Firmware.VendorSeverity != "Warning" {
		t.Errorf("entry 2: %+v", e)
	}
	if e := byRef["3"]; e == nil || e.ErrorSeverity != model.SevUE || e.Firmware.MapRef != "dmtf@1:MemoryUncorrectableError" {
		t.Errorf("entry 3: %+v", e)
	}
	seen := map[string]*model.RegistrySeen{}
	for _, r := range c.Registries {
		seen[r.Prefix] = r
	}
	if len(seen) != 5 {
		t.Fatalf("registries seen: %+v", c.Registries)
	}
	if p := seen["Platform"]; p.Version != "1.5.0" || p.Owner != "DMTF" || p.Messages != 51 || p.Mapped != 10 {
		t.Errorf("Platform: %+v", p)
	}
	if p := seen["LogService"]; p.Messages != 7 || p.Mapped != 1 {
		t.Errorf("LogService, read without its type: %+v", p)
	}
	if p := seen["OpenBMC"]; p.Messages != 192 || p.Mapped != 15 {
		t.Errorf("OpenBMC: %+v", p)
	}
	if p := seen["ExampleCo"]; p.Messages != 3 || p.Memory != 2 || p.Mapped != 0 || p.Wording != 1 || strings.Join(p.Unmapped, ",") != "MemFault8" {
		t.Errorf("ExampleCo: %+v", p)
	}
	if !strings.Contains(strings.Join(rep.Notes, "\n"), "the message registry dump/Registries/Broken.json") {
		t.Errorf("notes lack the registry that was not read:\n%s", strings.Join(rep.Notes, "\n"))
	}
}

// TestRegistryBounds: a registry with more messages than are read is left
// out with a note, and the entries are read without it.
func TestRegistryBounds(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"RegistryPrefix": "Big", "RegistryVersion": "1.0", "Messages": {`)
	for i := range 20001 {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `"M%d": {"Message": "x", "NumberOfArgs": 0}`, i)
	}
	b.WriteString("}}")
	fsys := fstest.MapFS{
		"Registries/Big.json": {Data: []byte(b.String())},
		"Log/1.json":          {Data: entryJSON(1, map[string]any{"MessageId": "Big.1.0.M1", "Message": "Uncorrectable memory error on DIMM_B1"})},
	}
	var c adapter.Collected
	rep := NewFS(fsys, "dump", Options{Now: now}).Collect(ctx, &c)
	if !strings.Contains(strings.Join(rep.Notes, "\n"), "more than 20000") || len(c.Registries) != 0 {
		t.Errorf("notes %v, registries %v", rep.Notes, c.Registries)
	}
	if len(c.Events) != 1 || c.Events[0].ErrorSeverity != model.SevUE || c.Events[0].Firmware.Mapping != model.MappingWording {
		t.Errorf("the entry read without the registry: %+v", c.Events)
	}
}

// TestEntryFields: EventTimestamp dates the event, Resolved and the
// overflow counters are kept, and a CPER memory section in DiagnosticData
// gives the place.
func TestEntryFields(t *testing.T) {
	record := func(name string) string {
		h, err := hex.DecodeString(strings.Join(strings.Fields(string(readFixture(t, "cper", name))), ""))
		if err != nil {
			t.Fatal(err)
		}
		return base64.StdEncoding.EncodeToString(h)
	}
	fsys := fstest.MapFS{
		"1.json": {Data: entryJSON(1, map[string]any{"MessageId": "Platform.1.5.MemoryContainedError", "MessageArgs": []string{"DIMM_C1"},
			"Message": "x", "EventTimestamp": "2026-11-12T02:59:30Z", "Resolved": true, "OverflowErrorCount": 12})},
		"2.json": {Data: entryJSON(2, map[string]any{"MessageId": "Platform.1.0.PlatformErrorAtLocation", "MessageArgs": []string{"CPU0"},
			"Message": "A platform error occurred at location CPU0.", "DiagnosticDataType": "CPER", "DiagnosticData": record("memory.cperhex")})},
		"3.json": {Data: entryJSON(3, map[string]any{"MessageId": "Platform.1.0.PlatformErrorAtLocation", "MessageArgs": []string{"CPU0"},
			"Message": "A platform error occurred at location CPU0.", "DiagnosticDataType": "CPER", "DiagnosticData": record("memory2.cperhex")})},
		"4.json": {Data: entryJSON(4, map[string]any{"MessageId": "Platform.1.0.PlatformErrorAtLocation", "MessageArgs": []string{"CPU0"},
			"Message": "A platform error occurred at location CPU0.", "DiagnosticDataType": "CPER", "DiagnosticData": "not base64!"})},
	}
	var c adapter.Collected
	rep := NewFS(fsys, "dump", Options{Now: now}).Collect(ctx, &c)
	byRef := map[string]*model.Event{}
	for _, e := range c.Events {
		byRef[lastSegment(e.RawRef)] = e
	}
	if e := byRef["1"]; e == nil || e.ErrorDT.Format("15:04:05") != "02:59:30" || !e.Firmware.Resolved || e.ErrorSeverity != model.SevRecoverable {
		t.Errorf("entry 1: %+v", e)
	}
	if !strings.Contains(strings.Join(rep.Notes, "\n"), "12 entries were lost when it overflowed") {
		t.Errorf("notes lack the overflow:\n%s", strings.Join(rep.Notes, "\n"))
	}
	// The memory section of libcper's example: recoverable, with bank and
	// row, and the SMBIOS handle of the module in place of a name.
	if e := byRef["2"]; e == nil || e.Decoder != "cper" || e.Firmware.Mapping != model.MappingCPER || e.ErrorSeverity != model.SevRecoverable ||
		e.Bank.V != 52608 || e.Row.V != 24942 || e.SMBIOSHandle.V != 21116 || e.Label != "" {
		t.Errorf("entry 2 with a CPER memory section: %+v", e)
	}
	// A section of another type (the memory section 2 of libcper's second
	// example) and a CPER that cannot be read leave the entry as it is: a
	// platform error that names no memory.
	for _, id := range []string{"3", "4"} {
		if e := byRef[id]; e != nil {
			t.Errorf("entry %s was read as about memory: %+v", id, e)
		}
	}
}

func FuzzRegistry(f *testing.F) {
	f.Add([]byte(exampleRegistry))
	f.Add([]byte(`{"RegistryPrefix": "X", "Messages": {"A": {"NumberOfArgs": 9999999999}}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		fsys := fstest.MapFS{"r.json": {Data: data},
			"1.json": {Data: entryJSON(1, map[string]any{"MessageId": "ExampleCo.1.0.MemFault7", "MessageArgs": []string{"DIMM_A1"}, "Message": "e"})}}
		var c adapter.Collected
		rep := NewFS(fsys, "dump", Options{Now: now}).Collect(ctx, &c)
		if rep.Status == "" {
			t.Fatal("no status")
		}
	})
}
