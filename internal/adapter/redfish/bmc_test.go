package redfish

import (
	"fmt"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/store"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
)

// setting returns the setting of a name, or fails.
func setting(t *testing.T, c *adapter.Collected, name string) *model.BIOSSetting {
	t.Helper()
	for _, s := range c.Settings {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no setting %s among %s", name, settingNames(c))
	return nil
}

func settingNames(c *adapter.Collected) string {
	var names []string
	for _, s := range c.Settings {
		names = append(names, s.Name)
	}
	sort.Strings(names)
	return strings.Join(names, " ")
}

// reading returns what the BMC says about the module in a slot, or fails.
func reading(t *testing.T, c *adapter.Collected, label string) *model.BMCMemory {
	t.Helper()
	for _, m := range c.Memory {
		if m.Label == label {
			return m
		}
	}
	t.Fatalf("no reading of %s", label)
	return nil
}

func classes(b *model.BMC) string {
	var out []string
	for _, c := range b.Classes {
		out = append(out, c.Class+"="+c.State)
	}
	return strings.Join(out, " ")
}

func TestDellShapedTree(t *testing.T) {
	c, rep := collect(t, "dell-shaped")
	if rep.Status != model.SourceOK || rep.HostHint != "node-d" {
		t.Fatalf("report %+v", rep)
	}

	// The settings that bear on memory, and nothing else of the BIOS: no
	// asset tag, no contact, nothing that is a secret. NodeInterleave is
	// kept for the menu it is in, not for its name.
	if got, want := settingNames(c), "AdddcSetting CECriticalSEL CorrEccSmi MemFrequency MemOpMode MemPatrolScrub MemRefreshRate MemTest NodeInterleave SysMemSize SysMemSpeed VideoMem"; got != want {
		t.Errorf("settings kept: %s\nwant: %s", got, want)
	}
	scrub := setting(t, c, "MemPatrolScrub")
	want := &model.BIOSSetting{ObservedAt: now, Name: "MemPatrolScrub", Value: "Standard", HasValue: true, Pending: "Disabled", HasPending: true,
		Type: "enumeration", Registry: "BiosAttributeRegistry.v1_0_0", DisplayName: "Memory Patrol Scrub", MenuPath: "./SysProfileSettingsRef",
		ReadOnly: model.Some(false), ResetRequired: model.Some(true), Allowed: []string{"Standard", "Extended", "Disabled"},
		Source: "redfish/bios", RawRef: scrub.RawRef}
	if !reflect.DeepEqual(scrub, want) {
		t.Errorf("MemPatrolScrub\n got: %+v\nwant: %+v", scrub, want)
	}
	if !strings.HasSuffix(scrub.RawRef, "#/redfish/v1/Systems/System.Embedded.1/Bios") {
		t.Errorf("raw reference %q", scrub.RawRef)
	}
	// The pending settings list the same frequency: nothing waits.
	if f := setting(t, c, "MemFrequency"); f.Value != "2666MHz" || f.HasPending || len(f.Allowed) != 6 {
		t.Errorf("MemFrequency = %+v", f)
	}
	// An attribute the pending settings do not list has no pending value.
	if a := setting(t, c, "AdddcSetting"); a.Value != "Enabled" || a.HasPending || a.Pending != "" {
		t.Errorf("AdddcSetting = %+v", a)
	}
	// A read-only attribute of the memory menu is kept and says so.
	if s := setting(t, c, "SysMemSize"); !s.ReadOnly.Or(false) || s.Type != "string" || s.Value != "128 GB" {
		t.Errorf("SysMemSize = %+v", s)
	}

	// Each module as the BMC describes it, with the BMC's counts.
	if len(c.Memory) != 4 {
		t.Fatalf("%d readings", len(c.Memory))
	}
	a1 := reading(t, c, "A1")
	wantA1 := &model.BMCMemory{ObservedAt: now, Label: "A1", DeviceLocator: "A1", ServiceLabel: "DIMM A1", ResourceID: "DIMM.Socket.A1",
		State: "Enabled", Health: "Warning", ErrorCorrection: "MultiBitECC", OperatingSpeed: model.Some(2666), AllowedSpeeds: []int{2933},
		Metrics: "ok", CECurrent: model.Some(int64(3)), UECurrent: model.Some(int64(0)), CELifetime: model.Some(int64(1250)), UELifetime: model.Some(int64(0)),
		AlarmCE: model.Some(true), AlarmUE: model.Some(false), CurrentSince: time.Date(2026, 10, 2, 8, 15, 0, 0, time.UTC),
		Source: "redfish/memory", RawRef: a1.RawRef, Raw: a1.Raw, Fingerprint: a1.Fingerprint}
	if !reflect.DeepEqual(a1, wantA1) {
		t.Errorf("A1\n got: %+v\nwant: %+v", a1, wantA1)
	}
	if !strings.Contains(a1.Raw, `"LifeTime"`) || a1.Fingerprint == "" {
		t.Errorf("raw %q", a1.Raw)
	}
	// A count of zero is a count; a module that links no metrics has none.
	if a2 := reading(t, c, "A2"); !a2.CELifetime.Valid || a2.CELifetime.V != 0 || a2.Metrics != "ok" {
		t.Errorf("A2 = %+v", a2)
	}
	if b2 := reading(t, c, "B2"); b2.HasCounts() || b2.Metrics != "not offered" || !b2.CurrentSince.IsZero() {
		t.Errorf("B2 = %+v", b2)
	}
	// The module facts of the inventory.
	for _, m := range c.Modules {
		d := m.DIMM
		if d.Ranks.V != 2 || d.DataWidth.V != 64 || d.TotalWidth.V != 72 || d.ConfiguredSpeedMTs.V != 2666 || d.SpeedMTs.V != 2933 ||
			d.SizeBytes != 32<<30 || d.Manufacturer != "Example Memory" || d.DeviceWidth.Valid {
			t.Errorf("module %s = %+v", m.Locator, d)
		}
	}

	// The BMC itself. A dump without a manifest says what it holds and
	// nothing about what was asked.
	if len(c.BMCs) != 1 {
		t.Fatalf("%d BMC records", len(c.BMCs))
	}
	b := c.BMCs[0]
	if b.Live || b.Complete.Valid || b.Address != "" || b.Firmware != "4.40.00.00" || b.Manager != "14G Monolithic" || b.BIOSVersion != "2.12.2" ||
		b.RedfishVersion != "1.9.0" || b.BIOSRegistry != "BiosAttributeRegistry.v1_0_0" || b.Source != "redfish" || !b.ObservedAt.Equal(now) ||
		!b.LastReset.Equal(time.Date(2026, 10, 2, 8, 15, 0, 0, time.UTC)) {
		t.Errorf("bmc = %+v", b)
	}
	if got, want := classes(b), "service-root=present system=present memory=present memory-metrics=present bios=present bios-settings=present attribute-registry=present manager=present log-service=present log-entries=present"; got != want {
		t.Errorf("classes: %s", got)
	}
	// The log: four entries of any kind give the period, two are about memory.
	wantLog := model.BMCLog{ID: "Sel", Path: "/redfish/v1/Managers/iDRAC.Embedded.1/LogServices/Sel", Entries: 4, Memory: 2,
		First: time.Date(2026, 10, 2, 8, 16, 10, 0, time.UTC), Last: time.Date(2026, 11, 11, 19, 22, 31, 0, time.UTC),
		Total: model.Some(4), Policy: "WrapsWhenFull", Max: model.Some(1024)}
	if len(b.Logs) != 1 || !reflect.DeepEqual(b.Logs[0], wantLog) {
		t.Errorf("logs = %+v\nwant  %+v", b.Logs, wantLog)
	}
	wantNotes(t, rep,
		"19 BIOS attributes read, 12 kept as settings that bear on memory",
		"attribute registry BiosAttributeRegistry.v1_0_0 read: 19 attributes described",
		"1 memory settings have a pending value that differs: it takes effect after a reboot",
		"the BMC keeps error counts for 3 of 4 modules: 1252 corrected and 0 uncorrected errors in all, 2 modules with corrected errors; these counts are readings and are in no total",
		"1 modules crossed an error threshold of the BMC",
		"1 modules link no MemoryMetrics")
	// The two log entries about memory are events as before.
	if len(c.Events) != 3 { // two log entries and the health warning of A1
		t.Errorf("%d events", len(c.Events))
	}
}

func TestHPEShapedTree(t *testing.T) {
	c, rep := collect(t, "hpe-shaped")
	if rep.Status != model.SourceOK {
		t.Fatalf("report %+v", rep)
	}
	if got, want := settingNames(c), "AdvancedMemProtection ExtendedMemTest MaxMemBusFreqMHz MemFastTraining MemMirrorMode MemPatrolScrubbing MemRefreshRate OpportunisticSelfRefresh"; got != want {
		t.Errorf("settings kept: %s", got)
	}
	// The registry is found where the registry file resource places it.
	amp := setting(t, c, "AdvancedMemProtection")
	if amp.Value != "FastFaultTolerantADDDC" || amp.Pending != "AdvancedEcc" || !amp.HasPending || amp.Registry != "BiosAttributeRegistryU30.v1_2_68" ||
		!reflect.DeepEqual(amp.Allowed, []string{"FastFaultTolerantADDDC", "AdvancedEcc", "OnlineSpareAdvancedEcc", "MirroredAdvancedEcc"}) {
		t.Errorf("AdvancedMemProtection = %+v", amp)
	}
	// The pending settings repeat every attribute: only the one that
	// differs is pending.
	for _, s := range c.Settings {
		if s.Name != "AdvancedMemProtection" && s.HasPending {
			t.Errorf("%s has a pending value %q", s.Name, s.Pending)
		}
	}
	if m := setting(t, c, "MemMirrorMode"); !m.ReadOnly.Or(false) {
		t.Errorf("MemMirrorMode = %+v", m)
	}
	// No module links metrics: the BMC keeps no counts, and that is said.
	for _, m := range c.Memory {
		if m.Metrics != "not offered" || m.HasCounts() || m.ServiceLabel != "" || !strings.HasPrefix(m.DeviceLocator, "PROC ") {
			t.Errorf("reading %+v", m)
		}
	}
	b := c.BMCs[0]
	if b.Firmware != "iLO 5 v3.09" || b.Manager != "iLO 5" || b.BIOSVersion != "U30 v2.90 (07/20/2023)" || !b.LastReset.IsZero() {
		t.Errorf("bmc = %+v", b)
	}
	if !strings.Contains(classes(b), "memory-metrics=absent") || !strings.Contains(classes(b), "attribute-registry=present") {
		t.Errorf("classes: %s", classes(b))
	}
	if len(b.Logs) != 1 || b.Logs[0].ID != "IML" || b.Logs[0].Entries != 3 || b.Logs[0].Memory != 2 || b.Logs[0].Truncated {
		t.Errorf("logs = %+v", b.Logs)
	}
	wantNotes(t, rep, "4 modules link no MemoryMetrics: this BMC keeps no error counts for them", "13 BIOS attributes read, 8 kept")
}

func TestLenovoShapedTree(t *testing.T) {
	c, rep := collect(t, "lenovo-shaped")
	if rep.Status != model.SourceOK {
		t.Fatalf("report %+v", rep)
	}
	if got, want := settingNames(c), "Memory_AdvMemTestOptions Memory_MemorySpeed Memory_MemoryTest"; got != want {
		t.Errorf("settings kept: %s", got)
	}
	// Without a registry the kind of value is what the JSON says.
	speed := setting(t, c, "Memory_MemorySpeed")
	if speed.Value != "MaxPerformance" || speed.Type != "string" || speed.Registry != "" || speed.ReadOnly.Valid || speed.Allowed != nil || speed.HasPending {
		t.Errorf("Memory_MemorySpeed = %+v", speed)
	}
	// A number, and its pending value at the resource named Pending.
	amt := setting(t, c, "Memory_AdvMemTestOptions")
	if amt.Value != "0" || amt.Type != "integer" || amt.Pending != "983040" || !amt.HasPending {
		t.Errorf("Memory_AdvMemTestOptions = %+v", amt)
	}
	if len(c.Memory) != 0 || len(c.BMCs) != 1 || c.BMCs[0].BIOSRegistry != "BiosAttributeRegistry.1.0.0" || c.BMCs[0].BIOSVersion != "ESE128B" {
		t.Errorf("readings %d, bmc %+v", len(c.Memory), c.BMCs)
	}
	wantNotes(t, rep, "the attribute registry BiosAttributeRegistry.1.0.0 is not in the dump", "7 BIOS attributes read, 3 kept")
}

// tree builds a dump from the JSON of its resources by URI.
func tree(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for name, text := range files {
		if strings.HasPrefix(name, "/") {
			name = strings.TrimPrefix(name, "/") + "/index.json"
		}
		fsys[name] = &fstest.MapFile{Data: []byte(text)}
	}
	return fsys
}

const (
	memoryA  = `{"@odata.id":"/redfish/v1/Systems/1/Memory/1","@odata.type":"#Memory.v1_10_0.Memory","Id":"1","DeviceLocator":"DIMM_A1","CapacityMiB":16384,"SerialNumber":"T0001","Status":{"State":"Enabled","Health":"OK"},"Metrics":{"@odata.id":"/redfish/v1/Systems/1/Memory/1/MemoryMetrics"}}`
	memoryB  = `{"@odata.id":"/redfish/v1/Systems/1/Memory/2","@odata.type":"#Memory.v1_10_0.Memory","Id":"2","DeviceLocator":"DIMM_A2","CapacityMiB":16384,"SerialNumber":"T0002","Status":{"State":"Enabled","Health":"OK"},"Metrics":{"@odata.id":"/redfish/v1/Systems/1/Memory/2/MemoryMetrics"}}`
	metricsA = `{"@odata.id":"/redfish/v1/Systems/1/Memory/1/MemoryMetrics","@odata.type":"#MemoryMetrics.v1_4_0.MemoryMetrics","Id":"MemoryMetrics","CurrentPeriod":{"CorrectableECCErrorCount":5,"UncorrectableECCErrorCount":1}}`
)

// A collection made by "my collect redfish": the manifest says when it was
// made, how, and what did not come back.
func TestCollectionWithManifest(t *testing.T) {
	manifestText := `{"format":"my-redfish-collection","version":1,"tool":"my test",
 "bmc":{"address":"bmc-node-a.example.net","url":"https://bmc-node-a.example.net","name":"node-a"},
 "started_at":"2026-11-10T04:00:00Z","finished_at":"2026-11-10T04:03:00Z","tls":"fingerprint","auth":"session",
 "complete":false,"stopped":"the BMC stopped answering after 4 resources (no answer within 30s)",
 "service":{"redfish_version":"1.11.0"},"clock":{"bmc":"2026-11-10T04:01:30Z","collector":"2026-11-10T04:00:00Z","offset_seconds":90},
 "classes":[{"class":"service-root","state":"ok","ok":1},{"class":"memory","state":"ok","ok":3},{"class":"memory-metrics","state":"partial","ok":1,"unsupported":1},{"class":"bios","state":"not reached"}],
 "resources":[{"uri":"/redfish/v1/Systems/1/Memory/1/MemoryMetrics","class":"memory-metrics","status":"ok"},
  {"uri":"/redfish/v1/Systems/1/Memory/2/MemoryMetrics","class":"memory-metrics","status":"unsupported","http":404,"detail":"404 Not Found"},
  {"uri":"/redfish/v1/Systems/1/LogServices/SEL/Entries","class":"log-entries","status":"ok","members":2,"detail":"2 of 40 entries read: the limit of this run is 2"}],
 "notes":["/redfish/v1/Systems/1/LogServices/SEL/Entries: 2 of 40 entries read: the limit of this run is 2"]}`
	entries := `{"@odata.id":"/redfish/v1/Systems/1/LogServices/SEL/Entries","Members@odata.count":40,"Members":[
 {"@odata.id":"/redfish/v1/Systems/1/LogServices/SEL/Entries/1","@odata.type":"#LogEntry.v1_6_0.LogEntry","Id":"1","EntryType":"SEL","Created":"2026-11-01T00:00:00Z","SensorType":"Memory","Message":"Correctable ECC DIMM_A1"},
 {"@odata.id":"/redfish/v1/Systems/1/LogServices/SEL/Entries/2","@odata.type":"#LogEntry.v1_6_0.LogEntry","Id":"2","EntryType":"SEL","Created":"2026-11-02T00:00:00Z","SensorType":"Fan","Message":"Fan 3 failed"}]}`
	fsys := tree(map[string]string{
		"manifest.json":                                 manifestText,
		"/redfish/v1/Systems/1/Memory/1":                memoryA,
		"/redfish/v1/Systems/1/Memory/2":                memoryB,
		"/redfish/v1/Systems/1/Memory/1/MemoryMetrics":  metricsA,
		"/redfish/v1/Systems/1/LogServices/SEL/Entries": entries,
	})
	var c adapter.Collected
	rep := NewFS(fsys, "bmc:bmc-node-a.example.net", Options{}).Collect(ctx, &c)
	if rep.Status != model.SourceOK || len(c.BMCs) != 1 {
		t.Fatalf("report %+v", rep)
	}
	b := c.BMCs[0]
	when := time.Date(2026, 11, 10, 4, 0, 0, 0, time.UTC)
	// The time of the dump is the time of the collection.
	if !b.ObservedAt.Equal(when) || !c.Modules[0].ObservedAt.Equal(when) || !c.Memory[0].ObservedAt.Equal(when) {
		t.Errorf("observed at %s, modules at %s", b.ObservedAt, c.Modules[0].ObservedAt)
	}
	if !b.Live || b.Complete != model.Some(false) || b.Address != "bmc-node-a.example.net" || b.TLS != "fingerprint" || b.Auth != "session" ||
		b.ClockOffset != model.Some(int64(90)) || b.RedfishVersion != "1.11.0" || b.Source != "redfish/manifest" ||
		b.RawRef != "bmc:bmc-node-a.example.net/manifest.json" {
		t.Errorf("bmc = %+v", b)
	}
	if got, want := classes(b), "service-root=ok memory=ok memory-metrics=partial bios=not reached"; got != want {
		t.Errorf("classes: %s", got)
	}
	if len(b.Notes) != 2 || !strings.HasPrefix(b.Notes[0], "the collection stopped early: the BMC stopped answering after 4 resources") {
		t.Errorf("notes = %q", b.Notes)
	}
	wantNotes(t, rep, "the collection from the BMC stopped early")
	// The metrics that did not come back are named with the reason, and
	// are not taken for a BMC that keeps no counts.
	if m := reading(t, &c, "DIMM_A1"); m.Metrics != "ok" || m.CECurrent.V != 5 || m.UECurrent.V != 1 || m.CELifetime.Valid || !m.CurrentSince.IsZero() {
		t.Errorf("DIMM_A1 = %+v", m)
	}
	if m := reading(t, &c, "DIMM_A2"); m.Metrics != "unsupported" || m.HasCounts() {
		t.Errorf("DIMM_A2 = %+v", m)
	}
	wantNotes(t, rep, "the MemoryMetrics of 1 modules did not come back from the BMC (unsupported)")
	// The log holds 40 entries and 2 were read.
	if len(b.Logs) != 1 || b.Logs[0].Entries != 2 || b.Logs[0].Memory != 1 || b.Logs[0].Total != model.Some(40) || !b.Logs[0].Truncated ||
		b.Logs[0].ID != "SEL" || !b.Logs[0].Last.Equal(time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("logs = %+v", b.Logs)
	}

	// The caller's time wins over the manifest's.
	c = adapter.Collected{}
	NewFS(fsys, "x", Options{Now: now}).Collect(ctx, &c)
	if !c.BMCs[0].ObservedAt.Equal(now) {
		t.Errorf("observed at %s with Options.Now", c.BMCs[0].ObservedAt)
	}

	// A manifest of a later version is not read, and that is said.
	fsys["manifest.json"] = &fstest.MapFile{Data: []byte(strings.Replace(manifestText, `"version":1`, `"version":7`, 1))}
	c = adapter.Collected{}
	rep = NewFS(fsys, "x", Options{Now: now}).Collect(ctx, &c)
	if c.BMCs[0].Live || c.BMCs[0].Address != "" || reading(t, &c, "DIMM_A2").Metrics != "" {
		t.Errorf("bmc = %+v", c.BMCs[0])
	}
	wantNotes(t, rep, "a manifest of version 7, which this version does not read", "1 modules link MemoryMetrics that are not in the dump")

	// Another file that happens to be called manifest.json is a file of
	// the dump like any other.
	fsys["manifest.json"] = &fstest.MapFile{Data: []byte(`{"name":"something else","version":3}`)}
	c = adapter.Collected{}
	if rep = NewFS(fsys, "x", Options{Now: now}).Collect(ctx, &c); rep.Status != model.SourceOK || c.BMCs[0].Live {
		t.Errorf("report %+v", rep)
	}
}

func bios(id, registry, pendingLink, attrs string) string {
	link := ""
	if pendingLink != "" {
		link = fmt.Sprintf(`,"@Redfish.Settings":{"SettingsObject":{"@odata.id":%q}}`, pendingLink)
	}
	reg := ""
	if registry != "" {
		reg = fmt.Sprintf(`,"AttributeRegistry":%q`, registry)
	}
	return fmt.Sprintf(`{"@odata.id":%q,"@odata.type":"#Bios.v1_1_0.Bios","Id":%q%s,"Attributes":{%s}%s}`, id, id[strings.LastIndex(id, "/")+1:], reg, attrs, link)
}

func TestSettingsThatAreKept(t *testing.T) {
	read := func(t *testing.T, files map[string]string) (*adapter.Collected, adapter.Report) {
		t.Helper()
		var c adapter.Collected
		rep := NewFS(tree(files), "dump", Options{Now: now}).Collect(ctx, &c)
		return &c, rep
	}

	t.Run("values", func(t *testing.T) {
		c, rep := read(t, map[string]string{"/redfish/v1/Systems/1/Bios": bios("/redfish/v1/Systems/1/Bios", "R.v1", "",
			`"MemScrubHours":24,"MemTest":true,"MemNote":null,"MemList":["a","b"],"MemObject":{"x":1},"MemLong":"`+strings.Repeat("x", 300)+`","MemEmpty":""`)})
		if got := settingNames(c); got != "MemEmpty MemNote MemScrubHours MemTest" {
			t.Errorf("settings kept: %s", got)
		}
		if s := setting(t, c, "MemScrubHours"); s.Value != "24" || s.Type != "integer" || !s.HasValue {
			t.Errorf("MemScrubHours = %+v", s)
		}
		if s := setting(t, c, "MemTest"); s.Value != "true" || s.Type != "boolean" {
			t.Errorf("MemTest = %+v", s)
		}
		// Null is no value; the empty text is one.
		if s := setting(t, c, "MemNote"); s.HasValue || s.Value != "" {
			t.Errorf("MemNote = %+v", s)
		}
		if s := setting(t, c, "MemEmpty"); !s.HasValue || s.Value != "" {
			t.Errorf("MemEmpty = %+v", s)
		}
		wantNotes(t, rep, "7 BIOS attributes read, 4 kept", "3 memory attributes hold no single value, or a text longer than 200 characters",
			"the attribute registry R.v1 is not in the dump", "the dump holds no pending BIOS settings")
	})

	t.Run("what is about memory", func(t *testing.T) {
		registry := `{"@odata.type":"#AttributeRegistry.v1_3_0.AttributeRegistry","Id":"R.v1_0_0","RegistryEntries":{"Attributes":[
 {"AttributeName":"Setup042","DisplayName":"Patrol Scrub Interval","MenuPath":"./Advanced","Type":"Integer","LowerBound":0,"UpperBound":24,"DefaultValue":24,"ReadOnly":false},
 {"AttributeName":"Setup043","DisplayName":"Quiet Boot","MenuPath":"./Boot","Type":"Boolean"},
 {"AttributeName":"Setup044","DisplayName":"Fan Mode","MenuPath":"./Advanced/Memory Configuration","Type":"Enumeration","Value":[{"ValueName":"Auto"},{"ValueName":"Full"}]},
 {"AttributeName":"Setup045","DisplayName":"Memory Encryption Passphrase","MenuPath":"./Security","Type":"Password"},
 {"AttributeName":"MemKeyRotation","DisplayName":"Memory key rotation","MenuPath":"./Security","Type":"String"}]}}`
		c, rep := read(t, map[string]string{
			"/redfish/v1/Systems/1/Bios": bios("/redfish/v1/Systems/1/Bios", "R.v1_0_0", "",
				`"Setup042":12,"Setup043":true,"Setup044":"Auto","Setup045":"hunter2hunter2","MemKeyRotation":"weekly","IscsiInitiatorName":"iqn.2026-11.net.example:node","BootMode":"Uefi"`),
			"/redfish/v1/Registries/R/R.json": registry,
		})
		// By display name, and by menu; a password and a key are never kept.
		if got := settingNames(c); got != "Setup042 Setup044" {
			t.Errorf("settings kept: %s", got)
		}
		s := setting(t, c, "Setup042")
		if s.DisplayName != "Patrol Scrub Interval" || s.Type != "integer" || s.LowerBound != model.Some(int64(0)) || s.UpperBound != model.Some(int64(24)) ||
			s.Default != "24" || !s.HasDefault || s.ResetRequired.Valid || s.ReadOnly != model.Some(false) || s.Registry != "R.v1_0_0" {
			t.Errorf("Setup042 = %+v", s)
		}
		for _, set := range c.Settings {
			if strings.Contains(set.Value, "hunter2") {
				t.Errorf("a password was kept: %+v", set)
			}
		}
		wantNotes(t, rep, "attribute registry R.v1_0_0 read: 5 attributes described")
	})

	t.Run("a profile can name attributes", func(t *testing.T) {
		saved := ExtraAttributes
		defer func() { ExtraAttributes = saved }()
		ExtraAttributes = func(name string) bool { return name == "Setup099" }
		c, _ := read(t, map[string]string{"/redfish/v1/Systems/1/Bios": bios("/redfish/v1/Systems/1/Bios", "", "", `"Setup099":"Enabled","Setup100":"Enabled"`)})
		if got := settingNames(c); got != "Setup099" {
			t.Errorf("settings kept: %s", got)
		}
	})

	t.Run("pending by name", func(t *testing.T) {
		// No link from the settings in effect: the resource named SD below
		// the Bios resource holds the pending ones.
		c, rep := read(t, map[string]string{
			"/redfish/v1/Systems/1/Bios":    bios("/redfish/v1/Systems/1/Bios", "", "", `"MemPatrolScrub":"Enabled"`),
			"/redfish/v1/Systems/1/Bios/SD": bios("/redfish/v1/Systems/1/Bios/SD", "", "", `"MemPatrolScrub":"Disabled"`),
		})
		if s := setting(t, c, "MemPatrolScrub"); s.Value != "Enabled" || s.Pending != "Disabled" || len(c.Settings) != 1 {
			t.Errorf("MemPatrolScrub = %+v", s)
		}
		wantNotes(t, rep, "1 memory settings have a pending value that differs")
	})

	t.Run("several systems", func(t *testing.T) {
		c, rep := read(t, map[string]string{
			"/redfish/v1/Systems/1/Bios": bios("/redfish/v1/Systems/1/Bios", "", "", `"MemPatrolScrub":"Enabled"`),
			"/redfish/v1/Systems/2/Bios": bios("/redfish/v1/Systems/2/Bios", "", "", `"MemPatrolScrub":"Disabled"`),
		})
		if len(c.Settings) != 1 || c.Settings[0].Value != "Enabled" {
			t.Errorf("settings = %+v", c.Settings)
		}
		wantNotes(t, rep, "2 Bios resources are in the dump; the settings of /redfish/v1/Systems/1/Bios are read")
	})

	t.Run("more than the bound", func(t *testing.T) {
		var attrs []string
		for i := 0; i < maxSettings+25; i++ {
			attrs = append(attrs, fmt.Sprintf(`"MemSlot%03d":"x"`, i))
		}
		c, rep := read(t, map[string]string{"/redfish/v1/Systems/1/Bios": bios("/redfish/v1/Systems/1/Bios", "", "", strings.Join(attrs, ","))})
		if len(c.Settings) != maxSettings {
			t.Errorf("%d settings", len(c.Settings))
		}
		wantNotes(t, rep, "25 more attributes look like memory settings and were left out: the limit is 400")
	})

	t.Run("without attributes", func(t *testing.T) {
		c, rep := read(t, map[string]string{"/redfish/v1/Systems/1/Bios": `{"@odata.id":"/redfish/v1/Systems/1/Bios","@odata.type":"#Bios.v1_1_0.Bios","Id":"Bios"}`})
		if len(c.Settings) != 0 || rep.Status != model.SourcePartial {
			t.Errorf("settings %d, report %+v", len(c.Settings), rep)
		}
	})
}

// Metrics that lie below the module without a link to them are found, and
// odd counts are not taken for counts.
func TestMetricsShapes(t *testing.T) {
	files := map[string]string{
		"/redfish/v1/Systems/1":                        `{"@odata.id":"/redfish/v1/Systems/1","@odata.type":"#ComputerSystem.v1_12_0.ComputerSystem","Id":"1","Manufacturer":"Example","Model":"EX","LastResetTime":"2031-01-01T00:00:00Z"}`,
		"/redfish/v1/Systems/1/Memory/1":               strings.Replace(memoryA, `,"Metrics":{"@odata.id":"/redfish/v1/Systems/1/Memory/1/MemoryMetrics"}`, "", 1),
		"/redfish/v1/Systems/1/Memory/1/MemoryMetrics": `{"@odata.type":"#MemoryMetrics.v1_4_0.MemoryMetrics","@odata.id":"/redfish/v1/Systems/1/Memory/1/MemoryMetrics","LifeTime":{"CorrectableECCErrorCount":"17","UncorrectableECCErrorCount":-1},"CurrentPeriod":{"CorrectableECCErrorCount":1.5}}`,
	}
	var c adapter.Collected
	rep := NewFS(tree(files), "dump", Options{Now: now}).Collect(ctx, &c)
	m := reading(t, &c, "DIMM_A1")
	// A count given as text is read; a count below zero and a fraction are none.
	if m.Metrics != "ok" || m.CELifetime != model.Some(int64(17)) || m.UELifetime.Valid || m.CECurrent.Valid || !m.CurrentSince.IsZero() {
		t.Errorf("reading = %+v (report %+v)", m, rep)
	}
	// A reset time after the dump was made is not believed.
	if !c.BMCs[0].LastReset.IsZero() {
		t.Errorf("last reset = %s", c.BMCs[0].LastReset)
	}
}

// One error that stands in two logs of the BMC is one error: the entry of
// the second log is declared a second report of the entry of the first.
// Two entries of one log are two errors, however close they are.
func TestOneErrorInTwoLogs(t *testing.T) {
	entry := func(log, id, created, dimm, text string) string {
		return fmt.Sprintf(`{"@odata.id":"%s/Entries/%s","@odata.type":"#LogEntry.v1_6_0.LogEntry","Id":%q,"EntryType":"SEL","Created":%q,"SensorType":"Memory","Message":"%s %s"}`,
			log, id, id, created, text, dimm)
	}
	log := func(path string, entries ...string) string {
		return fmt.Sprintf(`{"@odata.id":"%s/Entries","Members@odata.count":%d,"Members":[%s]}`, path, len(entries), strings.Join(entries, ","))
	}
	const sel, mgr = "/redfish/v1/Systems/1/LogServices/SEL", "/redfish/v1/Managers/1/LogServices/EventLog"
	fsys := tree(map[string]string{
		"/redfish/v1/Systems/1/Memory/1": memoryA,
		"/redfish/v1/Systems/1/Memory/2": memoryB,
		sel + "/Entries": log(sel,
			entry(sel, "1", "2026-11-01T00:00:00Z", "DIMM_A1", "Correctable ECC"),
			entry(sel, "2", "2026-11-01T00:00:02Z", "DIMM_A1", "Correctable ECC"),   // a second error, two seconds later
			entry(sel, "3", "2026-11-02T00:00:00Z", "DIMM_A2", "Correctable ECC"),   // another module
			entry(sel, "4", "2026-11-03T00:00:00Z", "DIMM_A1", "Uncorrectable ECC"), // another severity
			entry(sel, "5", "2026-11-04T00:00:00Z", "DIMM_A1", "Correctable ECC")),  // in no other log
		mgr + "/Entries": log(mgr,
			entry(mgr, "11", "2026-11-01T00:00:01Z", "DIMM_A1", "Correctable ECC"),  // the first error again
			entry(mgr, "12", "2026-11-01T00:00:03Z", "DIMM_A1", "Correctable ECC"),  // the second error again
			entry(mgr, "13", "2026-11-02T00:00:04Z", "DIMM_A2", "Correctable ECC"),  // the error of the other module
			entry(mgr, "14", "2026-11-03T00:00:00Z", "DIMM_A1", "Correctable ECC"),  // corrected, where the other log has an uncorrected error
			entry(mgr, "15", "2026-11-04T00:00:09Z", "DIMM_A1", "Correctable ECC")), // nine seconds off: another error
	})
	// The ledger finds them (store.FirmwareDuplicates): the reader keeps
	// every entry as it is.
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := ingest.Run(ctx, s, []adapter.Adapter{NewFS(fsys, "dump", Options{Now: now})}, ingest.Options{Host: "h1", Now: now}); err != nil {
		t.Fatal(err)
	}
	evs, err := s.EventList(ctx, store.EventFilter{Host: "h1", WithDuplicates: true})
	if err != nil || len(evs) != 10 {
		t.Fatalf("%d events, want 10 (%v)", len(evs), err)
	}
	byID := map[int64]string{}
	for _, e := range evs {
		byID[e.ID] = e.RawRef[strings.LastIndex(e.RawRef, "/")+1:]
	}
	pairs := map[string]bool{}
	for _, e := range evs {
		if e.DupOf == 0 {
			continue
		}
		a, b := byID[e.ID], byID[e.DupOf]
		if a > b {
			a, b = b, a
		}
		pairs[a+"="+b] = true
	}
	if want := map[string]bool{"1=11": true, "12=2": true, "13=3": true}; !reflect.DeepEqual(pairs, want) {
		t.Errorf("entries that repeat another = %v, want %v", pairs, want)
	}
}

// TestLifecycleCopyOfASELEvent: an iDRAC writes a memory event into its
// SEL and copies it into the Lifecycle log with the agent "SEL". The copy
// keeps its message ID and is marked as a SEL event; the ledger counts the
// event once.
func TestLifecycleCopyOfASELEvent(t *testing.T) {
	const sel, lc = "/redfish/v1/Managers/iDRAC.Embedded.1/LogServices/Sel", "/redfish/v1/Managers/iDRAC.Embedded.1/LogServices/Lclog"
	fsys := tree(map[string]string{
		"/redfish/v1/Systems/System.Embedded.1/Memory/DIMM.Socket.A7": `{"@odata.id":"/redfish/v1/Systems/System.Embedded.1/Memory/DIMM.Socket.A7","Id":"DIMM.Socket.A7","Name":"DIMM A7","DeviceLocator":"DIMM A7","SerialNumber":"0F1E2D3C","Manufacturer":"Example Memory","PartNumber":"EX-PART-1"}`,
		sel + "/Entries": `{"@odata.id":"` + sel + `/Entries","Members@odata.count":1,"Members":[` +
			`{"@odata.id":"` + sel + `/Entries/31","@odata.type":"#LogEntry.v1_16_1.LogEntry","Id":"31","EntryType":"SEL","EntryCode":"Assert","GeneratorId":"0x0001","SensorType":"Memory","SensorNumber":1,"MessageId":"6f01ffa7","Created":"2026-11-02T04:11:27-06:00","Severity":"Critical","Message":"Multi-bit memory errors are detected on the memory device at location(s) DIMM_A7."}]}`,
		lc + "/Entries": `{"@odata.id":"` + lc + `/Entries","Members@odata.count":1,"Members":[` +
			`{"@odata.id":"` + lc + `/Entries/2191","@odata.type":"#LogEntry.v1_16_1.LogEntry","Id":"2191","EntryType":"Oem","OemRecordFormat":"Dell","MessageId":"IDRAC.2.9.MEM0001","MessageArgs":["DIMM_A7"],"Created":"2026-11-02T04:11:27-06:00","Severity":"Critical","Message":"Multi-bit memory errors are detected on the memory device at location(s) DIMM_A7.","Oem":{"Dell":{"@odata.type":"#DellLCLogEntry.v1_2_0.DellLCLogEntry","AgentID":"SEL","Category":"SystemHealth"}}}]}`,
	})
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := ingest.Run(ctx, s, []adapter.Adapter{NewFS(fsys, "dump", Options{Now: now})}, ingest.Options{Host: "h1", Now: now}); err != nil {
		t.Fatal(err)
	}
	evs, err := s.EventList(ctx, store.EventFilter{Host: "h1", WithDuplicates: true})
	if err != nil || len(evs) != 2 {
		t.Fatalf("%d events, want 2 (%v)", len(evs), err)
	}
	counted := 0
	for _, e := range evs {
		if e.DupOf == 0 {
			counted++
		}
		if e.ErrorSeverity.Class() != "UE" {
			t.Errorf("event %+v", e)
		}
	}
	if counted != 1 {
		t.Errorf("%d of the two records count", counted)
	}
	fws, err := s.FirmwareRecords(ctx, store.FirmwareFilter{Host: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	origins := map[string]string{}
	for _, f := range fws {
		origins[f.Log] = f.Origin + " " + f.VendorID
	}
	if origins["Lclog"] != "sel MEM0001" {
		t.Errorf("firmware records by log %v", origins)
	}
}
