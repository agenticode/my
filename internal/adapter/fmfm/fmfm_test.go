package fmfm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/testutil"
)

var (
	ctx = context.Background()
	now = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
)

func collect(t *testing.T, name string, opts Options) (*adapter.Collected, adapter.Report) {
	t.Helper()
	if opts.Now.IsZero() {
		opts.Now = now
	}
	var c adapter.Collected
	rep := NewFile(testutil.Testdata(t, "fmfm", name), opts).Collect(ctx, &c)
	return &c, rep
}

func some(n int) model.Opt[int] { return model.Some(n) }

func short(ref string) string { return ref[strings.LastIndex(ref, "#")+1:] }

func wantNotes(t *testing.T, rep adapter.Report, parts ...string) {
	t.Helper()
	all := strings.Join(rep.Notes, "\n")
	for _, p := range parts {
		if !strings.Contains(all, p) {
			t.Errorf("notes lack %q:\n  %s", p, strings.Join(rep.Notes, "\n  "))
		}
	}
}

func TestRuntimeLog(t *testing.T) {
	c, rep := collect(t, "runtime.json", Options{})
	if rep.Status != model.SourcePartial || rep.Records != 14 || rep.Skipped != 5 || rep.HostHint != "r12-n034" {
		t.Errorf("report = %+v", rep)
	}
	wantNotes(t, rep,
		"records of other hosts left out: 1 (r12-n035); this run read the records of r12-n034, the first host the log names",
		"document 1[9]: record of another host than r12-n034",
		"records with a DIMM temperature: 1;",
		"fields that were not read: ProcessorMetadata",
		`document 1[10]: unknown ErrorSeverity "Corrected?"`,
		`document 1[11]: ErrorDT "yesterday" is missing or unreadable`,
		"document 1[12]: EcsModeRegisters is in a form this reader does not know",
		"document 1[13]: record has a time and neither an ErrorSeverity nor ECS registers",
	)

	if len(c.Events) != 6 {
		t.Fatalf("%d events, want 6", len(c.Events))
	}
	// The first record: every field of the runtime log, the row as text
	// in hexadecimal, the time in the form the framework recommends.
	e := *c.Events[0]
	want := model.Event{Kind: model.KindError, ErrorDT: time.Date(2026, 11, 10, 8, 0, 0, 123000000, time.UTC), ErrorSeverity: model.SevCE,
		ErrorScrubType: model.ScrubDemand, SocketId: some(0), ChannelId: some(2), ChannelSlotId: some(0), SubChannel: some(1), Rank: some(0),
		DeviceId: some(7), BankGroup: some(0), Bank: some(3), Row: some(0x1a2f), Column: some(568), ErrorVector: "dq:0x000c",
		OsPhysicalAddress: model.Some(uint64(0x1f8bd19c0)), Source: "fmfm/runtime", Decoder: "fmfm", Grade: model.GradeA, Count: 1,
		TimeQuality: model.TimeExact, RawRef: e.RawRef, Raw: e.Raw, Fingerprint: e.Fingerprint}
	if !reflect.DeepEqual(e, want) {
		t.Errorf("first event:\n got %+v\nwant %+v", e, want)
	}
	if short(e.RawRef) != "document 1[0]" || !strings.Contains(e.Raw, `"DimmTemp":41`) || e.Fingerprint == "" {
		t.Errorf("first event: reference %q, raw %q", e.RawRef, e.Raw)
	}

	type row struct {
		Ref, Sev, Scrub, Grade, At string
		Extra                      string
	}
	var rows []row
	for _, e := range c.Events[1:] {
		extra := e.ErrorVector
		if e.OsPhysicalAddress.Valid {
			extra += " addr"
		}
		if e.ErrorType != "" {
			extra += " " + e.ErrorType
		}
		rows = append(rows, row{short(e.RawRef), string(e.ErrorSeverity), string(e.ErrorScrubType), string(e.Grade),
			e.ErrorDT.Format("01-02 15:04:05.000"), strings.TrimSpace(extra)})
	}
	wantRows := []row{
		// The location is nested in an object of the record; the vector is a number.
		{"document 1[1]", "CE", "Patrol", "A", "11-10 09:00:00.500", "0xc"},
		// The time is a number of seconds.
		{"document 1[2]", "UE", "Demand", "B", "11-12 03:14:07.000", "addr"},
		{"document 1[4]", "CE", "", "C", "11-12 00:00:00.000", ""},
		{"document 1[7]", "Recoverable", "Other", "B", "11-13 01:00:00.000", ""},
		{"document 1[8]", "Informational", "", "C", "11-13 02:00:00.000", "CPAD received and acted upon"},
	}
	if !reflect.DeepEqual(rows, wantRows) {
		t.Errorf("events:\n got %+v\nwant %+v", rows, wantRows)
	}
	if n := c.Events[1]; n.Row != some(6703) || n.Column != some(570) || n.DeviceId != some(7) {
		t.Errorf("nested location = %+v", n)
	}

	// The log lists no installed memory: the reader makes up no module.
	// The ledger names the places of these errors by their numbers.
	if len(c.Modules) != 0 {
		t.Errorf("modules = %+v", c.Modules)
	}
}

func TestECSReadings(t *testing.T) {
	c, _ := collect(t, "runtime.json", Options{})
	type reading struct {
		Ref    string
		At     string
		Place  string
		Device model.Opt[int]
		MR     [7]model.Opt[int]
	}
	var got []reading
	for _, s := range c.ECSs {
		place := s.Label
		if s.SocketId.Valid {
			place = s.SocketId.String() + "/" + s.ChannelId.String() + "/" + s.ChannelSlotId.String()
		}
		got = append(got, reading{short(s.RawRef), s.ObservedAt.Format("01-02 15:04:05"), place, s.DeviceId, s.Registers()})
		if s.Source != "fmfm/runtime" || s.Fingerprint == "" || s.Raw == "" || s.TimeQuality != model.TimeExact {
			t.Errorf("reading %s: %+v", s.RawRef, s)
		}
	}
	var none model.Opt[int]
	want := []reading{
		// A list with one entry per device.
		{"document 1[3]", "11-11 00:00:00", "0/2/0", some(7), [7]model.Opt[int]{some(0x20), some(3), some(0x2f), some(0x1a), some(0x0c), some(16), some(4)}},
		{"document 1[3]", "11-11 00:00:00", "0/2/0", some(8), [7]model.Opt[int]{some(0x20), some(3), some(0), some(0), some(0), some(0), some(0)}},
		// The seven bytes as text, on a record that is also an error.
		{"document 1[4]", "11-12 00:00:00", "0/3/0", some(2), [7]model.Opt[int]{some(0x20), some(3), some(0), some(0), some(0), some(0), some(1)}},
		// MR14 to MR20 in order; the address registers were not read.
		{"document 1[5]", "11-13 00:00:00", "CPU1_D1", none, [7]model.Opt[int]{some(0xa0), some(3), none, none, none, some(0), some(2)}},
		// By device number.
		{"document 1[6]", "11-13 00:00:01", "1/0/0", some(3), [7]model.Opt[int]{none, none, none, none, none, some(0), some(0)}},
		{"document 1[6]", "11-13 00:00:01", "1/0/0", some(11), [7]model.Opt[int]{none, none, none, none, none, some(1), some(1)}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("readings:")
		for _, g := range got {
			t.Logf("  have %+v", g)
		}
	}
	// What the first reading says: code words are counted, the threshold
	// is 256 per Gb, the worst row is row 0x1a2f of bank 3 with 64 errors
	// or more, and four times the threshold was counted.
	s := c.ECSs[0]
	bg, bank, row := s.MaxRow()
	if s.CountMode() != model.ECSCountCodewords || s.ThresholdPerGb() != some(256) || bg != some(0) || bank != some(3) || row != some(0x1a2f) ||
		s.MaxRowErrorsMin() != some(64) || s.ErrorsPerGbMin() != some(1024) || s.Rank != some(0) {
		t.Errorf("first reading decodes to mode %q, threshold %v, row %v/%v/%v, row errors %v, per Gb %v",
			s.CountMode(), s.ThresholdPerGb(), bg, bank, row, s.MaxRowErrorsMin(), s.ErrorsPerGbMin())
	}
	seen := map[string]bool{}
	for _, s := range c.ECSs {
		if seen[s.Fingerprint] {
			t.Errorf("reading %s shares a fingerprint", s.RawRef)
		}
		seen[s.Fingerprint] = true
	}
}

func TestBootLog(t *testing.T) {
	c, rep := collect(t, "boot.json", Options{})
	if rep.Status != model.SourcePartial || rep.Records != 1 || rep.Skipped != 2 || rep.HostHint != "r12-n034" {
		t.Errorf("report = %+v", rep)
	}
	wantNotes(t, rep,
		"fields that were not read: MemoryControllerMetadata, PmicBootErrorLog, PrimordialBootDT, SystemMemoryConfig",
		"InstalledMemory[2]: entry of the installed memory names no slot",
		"InstalledMemory[3]: entry of the installed memory is not an object",
	)
	boot := time.Date(2026, 11, 9, 22, 0, 0, 0, time.UTC)
	if len(c.Boots) != 1 || !c.Boots[0].StartedAt.Equal(boot) || c.Boots[0].BIOS != "2.4.1" || !c.Boots[0].Derived || c.Boots[0].Source != "fmfm/boot" {
		t.Errorf("boots = %+v", c.Boots)
	}
	type mod struct {
		Slot, Serial, Part, Maker string
		Place                     string
		At                        time.Time
	}
	var mods []mod
	for _, m := range c.Modules {
		mods = append(mods, mod{m.Locator, m.DIMM.Serial, m.DIMM.PartNumber, m.DIMM.Manufacturer,
			m.Socket.String() + "/" + m.Channel.String() + "/" + m.Position.String(), m.ObservedAt})
	}
	want := []mod{
		{"CPU0_C1", "80A1C3", "EXM64G48R2X4-A", "Example Memory", "0/2/0", boot},
		// No locator in the record: the numbers name the slot.
		{"socket0 channel3 dimm0", "80A1F7", "EXM64G48R2X4-A", "Example Memory", "0/3/0", boot},
	}
	if !reflect.DeepEqual(mods, want) {
		t.Errorf("modules:\n got %+v\nwant %+v", mods, want)
	}
	if len(c.Events) != 1 {
		t.Fatalf("%d events, want the training failure", len(c.Events))
	}
	e := c.Events[0]
	if e.ErrorSeverity != model.SevInformational || e.ErrorType != "memory training failure at boot: write leveling" || !e.ErrorDT.Equal(boot) ||
		e.SocketId != some(1) || e.ChannelId != some(0) || e.ChannelSlotId != some(0) || e.SubChannel != some(0) || e.DeviceId != some(4) ||
		e.Source != "fmfm/boot" || short(e.RawRef) != "document 1/TrainingEventLogs[0]" {
		t.Errorf("training event = %+v", e)
	}
}

// What a boot record holds and the reader cannot use is reported, never
// dropped.
func TestBootRecordsReadInPart(t *testing.T) {
	read := func(doc string) (*adapter.Collected, adapter.Report) {
		var c adapter.Collected
		rep := NewReader(strings.NewReader(doc), "boot.json", Options{Now: now}).Collect(ctx, &c)
		return &c, rep
	}
	// Without a boot time the installed memory is still read, as seen at
	// the time of the import. A training failure has no time then.
	c, rep := read(`{"InstalledMemory": [{"SocketId": 0, "ChannelId": 1, "ChannelSlotId": 0, "DimmSerialNumber": "S1"}],
 "TrainingEventLogs": [{"FailureTrainingStep": "read leveling", "SocketId": 0, "ChannelId": 1, "ChannelSlotId": 0}]}`)
	if rep.Status != model.SourcePartial || rep.Skipped != 1 || len(c.Boots) != 0 || len(c.Events) != 0 ||
		len(c.Modules) != 1 || !c.Modules[0].ObservedAt.Equal(now) || c.Modules[0].DIMM.Serial != "S1" {
		t.Errorf("no boot time: report %+v, %d boots, %d events, modules %+v", rep, len(c.Boots), len(c.Events), c.Modules)
	}
	wantNotes(t, rep, "boot.json#document 1/TrainingEventLogs[0]: training event of a boot record without a readable CurrentBootDT")

	c, rep = read(`{"CurrentBootDT": "2026:11:09:22:00:00:000", "InstalledMemory": {"SocketId": 0},
 "TrainingEventLogs": ["write leveling", {"FailureTrainingStep": "write leveling"}]}`)
	if rep.Status != model.SourcePartial || rep.Skipped != 2 || len(c.Boots) != 1 || len(c.Events) != 1 || len(c.Modules) != 0 {
		t.Errorf("odd lists: report %+v, %d boots, %d events, %d modules", rep, len(c.Boots), len(c.Events), len(c.Modules))
	}
	wantNotes(t, rep, "InstalledMemory is not a list", "TrainingEventLogs[0]: entry of the training events is not an object")

	_, rep = read(`{"CurrentBootDT": "2026:11:09:22:00:00:000", "TrainingEventLogs": "none"}`)
	if rep.Status != model.SourcePartial || rep.Skipped != 1 {
		t.Errorf("training events as text: report %+v", rep)
	}
	wantNotes(t, rep, "TrainingEventLogs is not a list")

	// A field that is null is a field that is not there.
	if _, rep = read(`{"CurrentBootDT": "2026:11:09:22:00:00:000", "InstalledMemory": null, "TrainingEventLogs": null}`); rep.Status != model.SourceOK {
		t.Errorf("null lists: report %+v", rep)
	}

	_, rep = read(`{"CurrentBootDT": "soon", "InstalledFirmware": "2.4.1"}`)
	if rep.Status != model.SourcePartial || rep.Skipped != 1 {
		t.Errorf("unreadable boot time: report %+v", rep)
	}
	wantNotes(t, rep, `CurrentBootDT "soon" is unreadable`)
}

// A log with a record per boot lists the memory once per boot. Every
// listing is kept with its boot time, so a module that was replaced between
// two boots is seen in the slot from its first boot on.
func TestInstalledMemoryPerBoot(t *testing.T) {
	doc := `[
 {"CurrentBootDT": "2026:10:01:08:00:00:000", "InstalledMemory": [
   {"SocketId": 0, "ChannelId": 0, "ChannelSlotId": 0, "DeviceLocator": "A1", "DimmSerialNumber": "OLD"},
   {"SocketId": 0, "ChannelId": 1, "ChannelSlotId": 0, "DeviceLocator": "B1", "DimmSerialNumber": "KEPT"}]},
 {"CurrentBootDT": "2026:11:01:08:00:00:000", "InstalledMemory": [
   {"SocketId": 0, "ChannelId": 0, "ChannelSlotId": 0, "DeviceLocator": "A1", "DimmSerialNumber": "NEW"},
   {"SocketId": 0, "ChannelId": 1, "ChannelSlotId": 0, "DeviceLocator": "B1", "DimmSerialNumber": "KEPT"}]},
 {"ErrorDT": "2026-11-02T00:00:00Z", "SocketId": 0, "ChannelId": 0, "ChannelSlotId": 0, "EcsModeRegisters": [32, 3, 0, 0, 0, 0, 1]}
]`
	var c adapter.Collected
	rep := NewReader(strings.NewReader(doc), "boots.json", Options{Now: now}).Collect(ctx, &c)
	if rep.Status != model.SourceOK || len(c.Boots) != 2 {
		t.Fatalf("report %+v, %d boots", rep, len(c.Boots))
	}
	var got []string
	for _, m := range c.Modules {
		got = append(got, m.ObservedAt.Format("01-02")+" "+m.Locator+" "+m.DIMM.Serial+" "+m.DIMM.MemoryType)
	}
	// ECS registers were read from the place of A1: what sits there is DDR5.
	want := []string{"10-01 A1 OLD DDR5", "10-01 B1 KEPT ", "11-01 A1 NEW DDR5", "11-01 B1 KEPT "}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("modules = %q\nwant %q", got, want)
	}
}

// Two entries of one boot record with the same socket, channel and slot
// numbers: the numbers do not say which module a record means, so they are
// left off both. An entry with no other name cannot be kept.
func TestInstalledMemoryWithSharedNumbers(t *testing.T) {
	doc := `{"CurrentBootDT": "2026:11:01:08:00:00:000", "InstalledMemory": [
 {"SocketId": 0, "ChannelId": 0, "ChannelSlotId": 0, "DeviceLocator": "A1", "DimmSerialNumber": "S1"},
 {"SocketId": 0, "ChannelId": 0, "ChannelSlotId": 0, "DeviceLocator": "D1", "DimmSerialNumber": "S2"},
 {"SocketId": 0, "ChannelId": 0, "ChannelSlotId": 0, "DimmSerialNumber": "S3"},
 {"SocketId": 0, "ChannelId": 1, "ChannelSlotId": 0, "DeviceLocator": "B1", "DimmSerialNumber": "S4"},
 {"SocketId": 0, "ChannelId": 1, "ChannelSlotId": 0, "DeviceLocator": "B1", "DimmSerialNumber": "S4"}]}`
	var c adapter.Collected
	rep := NewReader(strings.NewReader(doc), "boot.json", Options{Now: now}).Collect(ctx, &c)
	if rep.Status != model.SourcePartial || rep.Skipped != 1 {
		t.Errorf("report = %+v", rep)
	}
	wantNotes(t, rep,
		"entries of the installed memory that have the socket, channel and slot numbers of another entry of their boot record: 3; these numbers place no records",
		"InstalledMemory[2]: entry of the installed memory has the socket, channel and slot numbers of another entry and no slot name",
	)
	var got []string
	for _, m := range c.Modules {
		got = append(got, m.Locator+" "+m.DIMM.Serial+" "+m.Socket.String()+"/"+m.Channel.String()+"/"+m.Position.String())
	}
	// B1 is listed twice with the same content: it is one module.
	none := model.Opt[int]{}.String()
	want := []string{"A1 S1 " + none + "/" + none + "/" + none, "D1 S2 " + none + "/" + none + "/" + none, "B1 S4 0/1/0"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("modules = %q\nwant %q", got, want)
	}
}

func TestCrashLog(t *testing.T) {
	c, rep := collect(t, "crash.json", Options{})
	if rep.Status != model.SourcePartial || rep.Records != 3 || rep.Skipped != 1 {
		t.Errorf("report = %+v", rep)
	}
	wantNotes(t, rep, "document 1[2]: crash record has no readable time", "fields that were not read: PmicCrashRegisters, Uptime")
	if len(c.Events) != 2 {
		t.Fatalf("%d events", len(c.Events))
	}
	// The time of the operating system is taken, not that of the firmware.
	f := c.Events[0]
	if f.ErrorSeverity != model.SevFatal || f.ErrorType != "fatal memory error at system crash: Command Address Parity" ||
		!f.ErrorDT.Equal(time.Date(2026, 11, 12, 3, 14, 7, 0, time.UTC)) || f.SocketId != some(0) || f.ChannelId != some(2) || f.Source != "fmfm/crash" {
		t.Errorf("fatal event = %+v", f)
	}
	// A crash without the flag is a mark on the timeline, counted in no total.
	m := c.Events[1]
	if m.ErrorSeverity != model.SevInformational || m.ErrorType != "system crash" || !m.ErrorDT.Equal(time.Date(2026, 11, 13, 1, 0, 0, 0, time.UTC)) {
		t.Errorf("crash mark = %+v", m)
	}
}

// Several documents one after the other, with the records nested under
// keys of the operator's own.
func TestDocumentsInARow(t *testing.T) {
	c, rep := collect(t, "combined.jsonl", Options{})
	if rep.Records != 5 || len(c.Boots) != 1 || len(c.Events) != 5 {
		t.Fatalf("report %+v, %d boots, %d events", rep, len(c.Boots), len(c.Events))
	}
	var refs []string
	for _, e := range c.Events {
		refs = append(refs, short(e.RawRef))
	}
	want := []string{"document 1/Log/TrainingEventLogs[0]", "document 2", "document 3", "document 4", "document 5/CrashLogs[0]"}
	if !reflect.DeepEqual(refs, want) {
		t.Errorf("references = %q, want %q", refs, want)
	}
	// The modules are the ones the boot record lists.
	if len(c.Modules) != 2 || c.Modules[0].Locator != "CPU0_C1" || c.Modules[0].DIMM.MemoryType != "" {
		t.Errorf("modules = %+v", c.Modules)
	}
}

// A module a boot record lists, and ECS registers were read from, is a
// DDR5 module.
func TestECSMarksAListedModuleAsDDR5(t *testing.T) {
	boot := `{"CurrentBootDT": "2026-11-09T22:00:00Z", "InstalledMemory": [
 {"SocketId": 0, "ChannelId": 2, "ChannelSlotId": 0, "DimmSerialNumber": "80A1C3"},
 {"SocketId": 0, "ChannelId": 3, "ChannelSlotId": 0, "DimmSerialNumber": "80A1F7"}]}`
	ecs := `{"ErrorDT": "2026-11-11T00:00:00Z", "SocketId": 0, "ChannelId": 2, "ChannelSlotId": 0, "EcsModeRegisters": {"MR20": 1}}`
	var c adapter.Collected
	rep := NewReader(strings.NewReader(boot+"\n"+ecs), "x", Options{Now: now}).Collect(ctx, &c)
	if rep.Status != model.SourceOK || len(c.Modules) != 2 || len(c.ECSs) != 1 {
		t.Fatalf("report %+v, %d modules, %d readings", rep, len(c.Modules), len(c.ECSs))
	}
	if c.Modules[0].DIMM.MemoryType != "DDR5" || c.Modules[1].DIMM.MemoryType != "" {
		t.Errorf("memory types = %q, %q", c.Modules[0].DIMM.MemoryType, c.Modules[1].DIMM.MemoryType)
	}
}

func TestHostSelection(t *testing.T) {
	a := NewFile(testutil.Testdata(t, "fmfm", "runtime.json"), Options{Now: now})
	if got := a.HostHint(); got != "r12-n034" {
		t.Errorf("host hint = %q", got)
	}
	// The other host of the log, when asked for: leaving out the rest is
	// what the caller wanted, so nothing counts as skipped.
	for _, asked := range []string{"r12-n035", "R12-N035"} {
		c, rep := collect(t, "runtime.json", Options{HostID: asked})
		if len(c.Events) != 1 || rep.HostHint != asked || rep.Status != model.SourceOK || rep.Skipped != 0 {
			t.Errorf("asked for %s: %d events, report %+v", asked, len(c.Events), rep)
		}
		wantNotes(t, rep, "records of other hosts left out: 13 (r12-n034); this run read the records of r12-n035")
		if strings.Contains(strings.Join(rep.Notes, "\n"), "first host") {
			t.Errorf("asked for %s: notes = %q", asked, rep.Notes)
		}
	}
	// A name the log does not use is the caller's name for the host of the
	// log: its first host is read, and the record of the other one is
	// reported as skipped.
	c, rep := collect(t, "runtime.json", Options{HostID: "web-12"})
	if len(c.Events) != 6 || rep.HostHint != "web-12" || rep.Status != model.SourcePartial || rep.Skipped != 5 {
		t.Errorf("asked for a name the log does not use: %d events, report %+v", len(c.Events), rep)
	}
	wantNotes(t, rep, "records of other hosts left out: 1 (r12-n035); this run read the records of r12-n034, the first host the log names")
	if got := NewFile(testutil.Testdata(t, "fmfm", "runtime.json"), Options{HostID: "x"}).HostHint(); got != "x" {
		t.Errorf("host hint with a host given = %q", got)
	}
	if got := NewFile(filepath.Join(t.TempDir(), "missing"), Options{}).HostHint(); got != "" {
		t.Errorf("host hint of a missing file = %q", got)
	}
}

// Two records with the same content are two errors; reading the file
// again gives the same two.
func TestEqualRecordsStayApart(t *testing.T) {
	rec := `{"ErrorDT": "2026-11-10T08:00:00Z", "ErrorSeverity": "CE", "SocketId": 0, "ChannelId": 2, "ChannelSlotId": 0, "Rank": 0}`
	read := func() []string {
		var c adapter.Collected
		NewReader(strings.NewReader("["+rec+","+rec+","+rec+"]"), "x", Options{Now: now}).Collect(ctx, &c)
		var out []string
		for _, e := range c.Events {
			out = append(out, e.Fingerprint)
		}
		return out
	}
	a, b := read(), read()
	if len(a) != 3 || a[0] == a[1] || a[1] == a[2] || !reflect.DeepEqual(a, b) {
		t.Errorf("fingerprints %v and %v", a, b)
	}
}

func TestTimeZoneOption(t *testing.T) {
	kst := time.FixedZone("KST", 9*3600)
	c, _ := collect(t, "runtime.json", Options{Location: kst})
	// A time without a zone is read in the zone given; a time with one,
	// and a number of seconds, are not moved.
	if !c.Events[0].ErrorDT.Equal(time.Date(2026, 11, 9, 23, 0, 0, 123000000, time.UTC)) {
		t.Errorf("time without a zone = %v", c.Events[0].ErrorDT)
	}
	if !c.Events[1].ErrorDT.Equal(time.Date(2026, 11, 10, 9, 0, 0, 500000000, time.UTC)) || !c.Events[2].ErrorDT.Equal(time.Date(2026, 11, 12, 3, 14, 7, 0, time.UTC)) {
		t.Errorf("times with a zone = %v, %v", c.Events[1].ErrorDT, c.Events[2].ErrorDT)
	}
}

func TestInputsThatHoldNoRecord(t *testing.T) {
	for name, c := range map[string]struct {
		content string
		status  model.SourceStatus
		detail  string
	}{
		"empty":      {"", model.SourceEmpty, "the file is empty"},
		"other json": {`{"hello": ["world", {"a": 1}]}`, model.SourceEmpty, "no FMFM boot, runtime or crash record found"},
		"broken":     {`[{"ErrorDT": `, model.SourceError, "not readable as JSON"},
		"text":       {"ErrorDT 2026-11-10\n", model.SourceError, "not readable as JSON"},
	} {
		var got adapter.Collected
		rep := NewReader(strings.NewReader(c.content), "x", Options{Now: now}).Collect(ctx, &got)
		if rep.Status != c.status || !strings.Contains(rep.Detail, c.detail) || len(got.Events) != 0 {
			t.Errorf("%s: status %s, detail %q; want %s, %q", name, rep.Status, rep.Detail, c.status, c.detail)
		}
	}
	// What was read before a broken document is kept.
	var c adapter.Collected
	rep := NewReader(strings.NewReader(`{"ErrorDT": "2026-11-10T08:00:00Z", "ErrorSeverity": "UE"}`+"\n{broken"), "x", Options{Now: now}).Collect(ctx, &c)
	if rep.Status != model.SourcePartial || len(c.Events) != 1 || !strings.Contains(strings.Join(rep.Notes, "\n"), "not readable as JSON") {
		t.Errorf("document after a good one is broken: %+v, %d events", rep, len(c.Events))
	}

	dir := t.TempDir()
	if rep := NewFile(filepath.Join(dir, "missing"), Options{}).Collect(ctx, &c); rep.Status != model.SourceAbsent {
		t.Errorf("missing file: %+v", rep)
	}
	if rep := NewFile(dir, Options{}).Collect(ctx, &c); rep.Status != model.SourceError || !strings.Contains(rep.Detail, "is a directory") {
		t.Errorf("directory: %+v", rep)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if rep := NewFile(testutil.Testdata(t, "fmfm", "runtime.json"), Options{}).Collect(cancelled, &c); rep.Status != model.SourceError {
		t.Errorf("cancelled context: %+v", rep)
	}
	_ = os.Remove
}

type failing struct {
	adapter.Collected
	err error
}

func (f *failing) ECS(*model.ECSSample) error { return f.err }

func TestSinkErrorStopsTheRead(t *testing.T) {
	boom := errors.New("ledger is full")
	rep := NewFile(testutil.Testdata(t, "fmfm", "runtime.json"), Options{Now: now}).Collect(ctx, &failing{err: boom})
	if rep.Status != model.SourceError || !errors.Is(rep.SinkErr, boom) {
		t.Errorf("report = %+v", rep)
	}
}

func TestSniff(t *testing.T) {
	for _, name := range []string{"runtime.json", "boot.json", "crash.json", "combined.jsonl"} {
		if !Sniff(testutil.ReadTestdata(t, "fmfm", name)) {
			t.Errorf("%s is not recognised", name)
		}
	}
	for name, data := range map[string]string{
		"sel json": `{"entries": [{"id": 1, "sensor_type": "Memory"}]}`,
		"redfish":  `{"@odata.type": "#LogEntry.v1_9_0.LogEntry", "Created": "2026-11-12T03:14:07Z"}`,
		"journal":  `{"MESSAGE":"EDAC MC0: 1 CE","_BOOT_ID":"0123"}`,
		"text":     `"ErrorDT" is the time of an error`,
		"empty":    "",
	} {
		if Sniff([]byte(data)) {
			t.Errorf("%s is taken for an FMFM log", name)
		}
	}
}
