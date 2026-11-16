package bert

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/decode/cper"
	"github.com/agenticode/my/internal/model"
)

// The regions of these tests are made with cper.StatusBlockBytes and
// cper.MemSection from the layouts of the ACPI specification as Linux
// declares them (include/acpi/actbl1.h, include/linux/cper.h); no region
// of a real firmware is among them.

type sink struct {
	events []*model.Event
}

func (s *sink) Event(e *model.Event) error         { s.events = append(s.events, e); return nil }
func (*sink) Action(*model.Action) error           { return nil }
func (*sink) GPU(*model.GPUMem) error              { return nil }
func (*sink) ECS(*model.ECSSample) error           { return nil }
func (*sink) Boot(*model.Boot) error               { return nil }
func (*sink) Host(*model.Host) error               { return nil }
func (*sink) Module(*model.Module) error           { return nil }
func (*sink) Counter(*model.CounterSample) error   { return nil }
func (*sink) BMC(*model.BMC) error                 { return nil }
func (*sink) BIOSSetting(*model.BIOSSetting) error { return nil }
func (*sink) BMCMemory(*model.BMCMemory) error     { return nil }
func (*sink) Firmware(*model.FirmwareRecord) error { return nil }

var cell = cper.MemError{
	PhysicalAddress: model.Some(uint64(0x1a2f3c40)), Node: model.Some(0), Card: model.Some(2), Module: model.Some(1),
	Rank: model.Some(0), Device: model.Some(7), BankGroup: model.Some(1), Bank: model.Some(3),
	Row: model.Some(0x1a2f), Column: model.Some(0x238), ErrorType: model.Some(3),
}

var readAt = time.Date(2026, 11, 16, 3, 5, 0, 0, time.UTC)

func collect(t *testing.T, region []byte) (*sink, model.SourceStatus, []string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "BERT")
	if err := os.WriteFile(p, region, 0o400); err != nil {
		t.Fatal(err)
	}
	var s sink
	rep := New(p, Options{ReadAt: readAt, BootID: "0123456789abcdef0123456789abcdef"}).Collect(context.Background(), &s)
	if rep.SinkErr != nil {
		t.Fatal(rep.SinkErr)
	}
	return &s, rep.Status, append(rep.Notes, rep.Detail)
}

func TestOneBlock(t *testing.T) {
	region := cper.StatusBlockBytes(1, cper.SevFatal, cper.Entry{SectionType: cper.SectionPlatformMemory, Severity: cper.SevFatal, FRUText: "CPU0_C1", Data: cper.MemSection(cell)})
	s, status, notes := collect(t, append(region, make([]byte, 512)...))
	if status != model.SourceOK || len(s.events) != 1 {
		t.Fatalf("%s, %d events, %q", status, len(s.events), notes)
	}
	e := s.events[0]
	if e.ErrorSeverity != model.SevFatal || e.Grade != model.GradeA || !e.Carried || !e.ErrorDT.Equal(readAt) ||
		e.TimeQuality != model.TimeEstimated || e.BootID != "0123456789abcdef0123456789abcdef" || e.Label != "CPU0_C1" ||
		e.Row != cell.Row || e.Column != cell.Column || e.OsPhysicalAddress != cell.PhysicalAddress || e.Source != Kind || e.Decoder != "cper" {
		t.Errorf("event %+v", e)
	}
	if !strings.HasSuffix(e.RawRef, "#block1/entry1") || e.Raw == "" {
		t.Errorf("reference %q, raw %q", e.RawRef, e.Raw)
	}
}

// Several blocks, both revisions of the entry, a time stamp (the record
// is not carried), a block the kernel cleared, a section that is no memory
// error, a fatal block without a memory error.
func TestSeveralBlocks(t *testing.T) {
	var proc [16]byte
	proc[0] = 0x42
	ts := [8]byte{0x07, 0x14, 0x03, 0x01, 0x16, 0x11, 0x26, 0x20}
	region := cper.StatusBlockBytes(0, cper.SevFatal, cper.Entry{SectionType: cper.SectionPlatformMemory, Severity: cper.SevFatal, Data: cper.MemSection(cell)})
	region = append(region, cper.StatusBlockBytes(1, cper.SevCorrected,
		cper.Entry{SectionType: cper.SectionPlatformMemory, Severity: cper.SevCorrected, V300: true, Timestamp: ts, Data: cper.MemSection(cell)},
		cper.Entry{SectionType: proc, Severity: cper.SevCorrected, Data: make([]byte, 64)})...)
	region = append(region, cper.StatusBlockBytes(1, cper.SevFatal, cper.Entry{SectionType: proc, Severity: cper.SevFatal, Data: make([]byte, 64)})...)
	s, status, notes := collect(t, region)
	if status != model.SourceOK || len(s.events) != 3 {
		t.Fatalf("%s, %d events, %q", status, len(s.events), notes)
	}
	if !s.events[0].Carried || s.events[1].Carried || !s.events[1].ErrorDT.Equal(time.Date(2026, 11, 16, 3, 14, 7, 0, time.UTC)) {
		t.Errorf("times: %+v / %+v", s.events[0], s.events[1])
	}
	if b := s.events[2]; b.ErrorSeverity != model.SevInformational || b.ErrorType != model.ErrorTypeFatalBlock || !b.Carried {
		t.Errorf("fatal block: %+v", b)
	}
	all := strings.Join(notes, "\n")
	for _, w := range []string{"3 error status blocks", "printed 1 of them", "2 firmware error sections are not about memory"} {
		if !strings.Contains(all, w) {
			t.Errorf("notes lack %q:\n%s", w, all)
		}
	}
	// The same region with the block status set again gives the same
	// records.
	again := append([]byte(nil), region...)
	again[0] = 1
	s2, _, _ := collect(t, again)
	for i := range s.events {
		if s.events[i].Fingerprint != s2.events[i].Fingerprint {
			t.Errorf("record %d changed with the block status", i)
		}
	}
}

func TestBrokenRegions(t *testing.T) {
	good := cper.StatusBlockBytes(1, cper.SevFatal, cper.Entry{SectionType: cper.SectionPlatformMemory, Severity: cper.SevFatal, Data: cper.MemSection(cell)})
	for _, tt := range []struct {
		name   string
		region []byte
		status model.SourceStatus
		events int
	}{
		{"empty", nil, model.SourceEmpty, 0},
		{"zeros", make([]byte, 4096), model.SourceEmpty, 0},
		{"truncated", good[:len(good)-5], model.SourcePartial, 0},
		{"a good block and a broken one", append(append([]byte(nil), good...), good[:30]...), model.SourcePartial, 1},
		{"a section too short to be a memory error", cper.StatusBlockBytes(1, cper.SevFatal, cper.Entry{SectionType: cper.SectionPlatformMemory, Severity: cper.SevFatal, Data: make([]byte, 20)}), model.SourcePartial, 0},
	} {
		s, status, notes := collect(t, tt.region)
		if status != tt.status || len(s.events) != tt.events {
			t.Errorf("%s: %s, %d events, %q", tt.name, status, len(s.events), notes)
		}
	}
}

func TestUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every file")
	}
	p := filepath.Join(t.TempDir(), "BERT")
	os.WriteFile(p, []byte("x"), 0)
	rep := New(p, Options{}).Collect(context.Background(), &sink{})
	if rep.Status != model.SourceUnreadable || !strings.Contains(rep.Detail, "root only") {
		t.Errorf("%+v", rep)
	}
}

func FuzzCollect(f *testing.F) {
	f.Add(cper.StatusBlockBytes(1, cper.SevFatal, cper.Entry{SectionType: cper.SectionPlatformMemory, Severity: cper.SevFatal, Data: cper.MemSection(cell)}))
	f.Fuzz(func(t *testing.T, region []byte) {
		p := filepath.Join(t.TempDir(), "BERT")
		if err := os.WriteFile(p, region, 0o600); err != nil {
			t.Skip()
		}
		var s sink
		rep := New(p, Options{ReadAt: readAt}).Collect(context.Background(), &s)
		if rep.Status == model.SourceError {
			t.Fatalf("%+v", rep)
		}
		for _, e := range s.events {
			if e.Fingerprint == "" || e.ErrorDT.IsZero() || !e.ErrorSeverity.Valid() {
				t.Fatalf("event %+v", e)
			}
		}
	})
}
