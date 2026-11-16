package scan

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/bert"
	"github.com/agenticode/my/internal/adapter/klog"
	"github.com/agenticode/my/internal/decode/cper"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// TestRegionAndItsLines: the boot error region read from sysfs and the
// lines the kernel printed of it in the same boot are one error; the
// region is counted, in the boot before the one that read it.
func TestRegionAndItsLines(t *testing.T) {
	cell := cper.MemError{PhysicalAddress: model.Some(uint64(0x12345040)), Node: model.Some(0), Card: model.Some(2), Module: model.Some(1),
		Rank: model.Some(0), Device: model.Some(7), BankGroup: model.Some(1), Bank: model.Some(3), Row: model.Some(6703), Column: model.Some(568), ErrorType: model.Some(3)}
	p := filepath.Join(t.TempDir(), "BERT")
	write(t, p, string(cper.StatusBlockBytes(0, cper.SevFatal, cper.Entry{SectionType: cper.SectionPlatformMemory, Severity: cper.SevFatal, Data: cper.MemSection(cell)})), 0o600)
	// The lines as bert_print_all and cper_estatus_print write them (source
	// text, drivers/acpi/apei/bert.c and drivers/firmware/efi/cper.c).
	lines := `[    0.000000] Linux version 7.3.0
[    4.000000] rtc_cmos 00:04: setting system clock to 2026-11-16T03:02:33 UTC (1794798153)
[    7.100000] BERT: Error records from previous boot:
[    7.100100] [Hardware Error]: event severity: fatal
[    7.100200] [Hardware Error]:  Error 0, type: fatal
[    7.100300] [Hardware Error]:   section_type: memory error
[    7.100500] [Hardware Error]:   physical_address: 0x0000000012345040
[    7.100600] [Hardware Error]:   node:0 card:2 module:1 rank:0 bank_group:1 bank_address:3 device:7 row:6703 column:568 
[    7.100700] [Hardware Error]:   error_type: 3, multi-bit ECC
[    7.100900] BERT: Total records found: 1
[   60.000000] systemd[1]: Reached target multi-user.target - Multi-User System.
`
	start := time.Unix(1794798153-4, 0).UTC()
	s := ledger(t)
	_, err := ingest.Run(ctx, s, []adapter.Adapter{
		klog.NewReader(strings.NewReader(lines), "dmesg", klog.Options{}),
		bert.New(p, bert.Options{ReadAt: start.Add(2 * time.Minute)}),
	}, ingest.Options{Host: "h1", Now: start.Add(3 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	evs, err := s.EventList(ctx, store.EventFilter{WithDuplicates: true})
	if err != nil || len(evs) != 2 {
		t.Fatalf("events %+v %v", evs, err)
	}
	var region, printed *model.Event
	for _, e := range evs {
		if e.Source == bert.Kind {
			region = e
		} else {
			printed = e
		}
	}
	if region == nil || printed == nil || region.DupOf != 0 || printed.DupOf != region.ID {
		t.Fatalf("region %+v, lines %+v", region, printed)
	}
	if !region.Carried || region.BootID == "" || region.ReadBootID == "" || region.BootID == region.ReadBootID {
		t.Errorf("the region is not in the boot before: %+v", region)
	}
	boots, _ := s.Boots(ctx, "h1")
	var before model.Boot
	for _, b := range boots {
		if b.BootID == region.BootID {
			before = b
		}
	}
	if before.StartQuality != store.QualityUnknown || before.End != model.BootStop {
		t.Errorf("the boot before: %+v", before)
	}
}
