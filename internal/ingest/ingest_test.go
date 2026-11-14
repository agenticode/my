package ingest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/edacsysfs"
	"github.com/agenticode/my/internal/adapter/klog"
	"github.com/agenticode/my/internal/adapter/rasdaemon"
	"github.com/agenticode/my/internal/adapter/smbios"
	"github.com/agenticode/my/internal/decode/edac"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/testutil"
)

var ctx = context.Background()

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// fake is an adapter that emits what a test tells it to.
type fake struct {
	kind string
	hint string
	fn   func(sink adapter.Sink) error
	rep  adapter.Report
}

func (f *fake) Kind() string     { return f.kind }
func (f *fake) Origin() string   { return "fake:" + f.kind }
func (f *fake) HostHint() string { return f.hint }
func (f *fake) Collect(_ context.Context, sink adapter.Sink) adapter.Report {
	rep := f.rep
	rep.Kind, rep.Origin = f.Kind(), f.Origin()
	if rep.Status == "" {
		rep.Status = model.SourceOK
	}
	if f.fn != nil {
		if err := f.fn(sink); err != nil {
			rep.Status, rep.SinkErr = model.SourceError, err
		}
	}
	return rep
}

// The Intel host of the fixtures: one socket, two memory controllers with
// two channels each, one DIMM per channel. The kernel log, the EDAC counters
// and the SMBIOS table below describe the same machine at the same moment.
var (
	intelBoot = time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	// The EDAC driver registered 18 s after boot and has counted for 90700 s.
	intelNow = intelBoot.Add(90718 * time.Second)
)

func intelSMBIOS() fstest.MapFS {
	var b smbios.Builder
	b.AddSystem(0x0100, "Example Systems", "R650")
	for i, ch := range []string{"A", "B", "C", "D"} {
		serial := []string{"11AA01", "11AA02", "80A1C3", "80A1F7"}[i]
		b.AddMemoryDevice(smbios.MemoryDevice{Handle: uint16(0x1100 + 2*i), Locator: "CPU0_" + ch + "1",
			BankLocator: fmt.Sprintf("P0_Node0_Channel%d_Dimm0", i), SizeMB: 32768, MemoryType: smbios.TypeDDR4, FormFactor: smbios.FormDIMM,
			TypeDetail: smbios.DetailRDIMM, SpeedMTs: 3200, Manufacturer: "Example Memory", Serial: serial, PartNumber: "EXM32G32R2X4-A", Ranks: 2})
		b.AddMemoryDevice(smbios.MemoryDevice{Handle: uint16(0x1101 + 2*i), Locator: "CPU0_" + ch + "2",
			BankLocator: fmt.Sprintf("P0_Node0_Channel%d_Dimm1", i), Manufacturer: "NO DIMM", Serial: "NO DIMM"})
	}
	return fstest.MapFS{"DMI": {Data: b.Bytes()}}
}

func intelAdapters(t *testing.T) []adapter.Adapter {
	t.Helper()
	return []adapter.Adapter{
		klog.NewFile(testutil.Testdata(t, "klog", "intel-adxl.dmesg"), klog.Options{}),
		edacsysfs.New(os.DirFS(testutil.Testdata(t, "edac-sysfs", "intel-i10nm")), "mc", "/sys/devices/system/edac/mc", edacsysfs.Options{Now: intelNow}),
		smbios.NewTables(intelSMBIOS(), ".", "/sys/firmware/dmi/tables", smbios.Options{Now: intelNow}),
	}
}

type slotTotal struct {
	Slot, Serial string
	CE, UE       int64
}

func totals(t *testing.T, s *store.Store, host string) []slotTotal {
	t.Helper()
	ts, err := s.Totals(ctx, store.EventFilter{Host: host})
	if err != nil {
		t.Fatal(err)
	}
	var out []slotTotal
	for _, x := range ts {
		st := slotTotal{Slot: x.Slot, CE: x.CE, UE: x.UE}
		if x.DIMMUID != "" {
			d, err := s.DIMM(ctx, x.DIMMUID)
			if err != nil || d == nil {
				t.Fatalf("dimm %s: %v", x.DIMMUID, err)
			}
			st.Serial = d.Serial
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
	return out
}

func TestIntelHostEndToEnd(t *testing.T) {
	s := newStore(t)
	res, err := Run(ctx, s, intelAdapters(t), Options{Host: "r12-n034", Kind: model.ScanLive, Now: intelNow})
	if err != nil {
		t.Fatal(err)
	}

	// Source inventory: the three sources and the slot mapping.
	var kinds []string
	for _, src := range res.Sources {
		kinds = append(kinds, fmt.Sprintf("%s:%s", src.Kind, src.Status))
	}
	if want := []string{"klog:partial", "edac-sysfs:ok", "smbios:ok", "slot-map:ok"}; !reflect.DeepEqual(kinds, want) {
		t.Errorf("sources = %v, want %v", kinds, want)
	}
	stored, _ := s.Sources(ctx, res.Scan.ID)
	if len(stored) != 4 || stored[0].GradeA != 4 || stored[0].GradeB != 1 || stored[0].Events != 5 || stored[0].Actions != 3 ||
		!strings.Contains(strings.Join(stored[3].Notes, " "), "all 4 EDAC DIMM positions are tied to SMBIOS slots") {
		t.Errorf("stored sources = %+v", stored)
	}
	if res.Events != 5 || res.Actions != 3 || res.Host != "r12-n034" || res.Scan.Kind != model.ScanLive || !res.Scan.FinishedAt.Equal(intelNow) {
		t.Errorf("result = %+v", res)
	}

	// The slot table is the board: SMBIOS names, EDAC positions tied by
	// socket, channel and position numbers, empty slots included.
	slots, err := s.Slots(ctx, "r12-n034")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, sl := range slots {
		names = append(names, sl.Name)
	}
	if want := []string{"CPU0_A1", "CPU0_A2", "CPU0_B1", "CPU0_B2", "CPU0_C1", "CPU0_C2", "CPU0_D1", "CPU0_D2"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("slots = %v, want %v", names, want)
	}
	c1 := slots[4]
	if c1.Mapping != model.MapBankLocator || c1.EdacMC != model.Some(1) || c1.EdacLocation != "0:0" || c1.IMC != model.Some(1) ||
		c1.Channel != model.Some(0) || c1.Position != model.Some(0) || c1.EdacLabel != "CPU_SrcID#0_MC#1_Chan#0_DIMM#0" || !c1.Populated.V {
		t.Errorf("CPU0_C1 = %+v", c1)
	}
	if c2 := slots[5]; c2.Populated.V || c2.IMC != model.Some(1) || c2.Channel != model.Some(0) || c2.Position != model.Some(1) || c2.Mapping != "" {
		t.Errorf("CPU0_C2 (empty) = %+v", c2)
	}

	// Totals: the kernel log holds 3 corrected errors of CPU0_C1 (one line
	// was cut off), the counter says 4. The fourth is counted once, as
	// grade C. The uncorrected error is in both and counted once.
	got := totals(t, s, "r12-n034")
	want := []slotTotal{
		{Slot: "", CE: 2}, // a machine check without DIMM, and the controller's no-info count
		{Slot: "CPU0_C1", Serial: "80A1C3", CE: 4, UE: 1},
		{Slot: "CPU0_D1", Serial: "80A1F7", CE: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("totals\n got: %+v\nwant: %+v", got, want)
	}

	events, _ := s.EventList(ctx, store.EventFilter{Host: "r12-n034", Slot: "CPU0_C1", WithDuplicates: true})
	boots, _ := s.Boots(ctx, "r12-n034")
	if len(boots) != 1 || !boots[0].StartedAt.Equal(intelBoot) {
		t.Fatalf("boots = %+v", boots)
	}
	for _, e := range events {
		if e.BootID != boots[0].BootID {
			t.Errorf("%s: boot %q, want %q", e.RawRef, e.BootID, boots[0].BootID)
		}
		// The DIMM was seen in the slot after the errors happened.
		if e.Kind == model.KindError && e.DIMMLink != model.LinkAssumed {
			t.Errorf("%s: link %q, want assumed", e.RawRef, e.DIMMLink)
		}
	}
	// The page offline actions name a page of CPU0_C1.
	acts, _ := s.Actions(ctx, store.ActionFilter{Host: "r12-n034"})
	if len(acts) != 3 {
		t.Fatalf("%d actions", len(acts))
	}
	for _, a := range acts {
		if a.Slot != "CPU0_C1" || a.DIMMUID == "" || a.BootID != boots[0].BootID {
			t.Errorf("action %s: slot %q dimm %q boot %q", a.RawRef, a.Slot, a.DIMMUID, a.BootID)
		}
	}
	// DIMM identities carry what SMBIOS and EDAC know together.
	d, _ := s.DIMM(ctx, model.DIMMUID("Example Memory", "EXM32G32R2X4-A", "80A1C3"))
	if d == nil || d.Identity != model.IdentitySerial || d.SizeBytes != 32<<30 || d.Organization() != "2Rx4" || d.MemoryType != "DDR4" {
		t.Errorf("DIMM of CPU0_C1 = %+v", d)
	}

	// Running the same sources again adds nothing and changes no total.
	res2, err := Run(ctx, s, intelAdapters(t), Options{Host: "r12-n034", Kind: model.ScanLive, Now: intelNow})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Events != 0 || res2.Actions != 0 {
		t.Errorf("second run added %d events and %d actions", res2.Events, res2.Actions)
	}
	if !strings.Contains(strings.Join(res2.Sources[0].Notes, " "), "8 records were already in the ledger") {
		t.Errorf("second run notes = %q", res2.Sources[0].Notes)
	}
	if again := totals(t, s, "r12-n034"); !reflect.DeepEqual(again, want) {
		t.Errorf("totals after a second run\n got: %+v\nwant: %+v", again, want)
	}
}

func TestSourceOrderDoesNotMatter(t *testing.T) {
	var ref []slotTotal
	for i, order := range [][]int{{0, 1, 2}, {2, 1, 0}, {1, 2, 0}, {2, 0, 1}} {
		s := newStore(t)
		all := intelAdapters(t)
		var as []adapter.Adapter
		for _, j := range order {
			as = append(as, all[j])
		}
		if _, err := Run(ctx, s, as, Options{Host: "r12-n034", Now: intelNow}); err != nil {
			t.Fatal(err)
		}
		got := totals(t, s, "r12-n034")
		if i == 0 {
			ref = got
			continue
		}
		if !reflect.DeepEqual(got, ref) {
			t.Errorf("order %v\n got: %+v\nwant: %+v", order, got, ref)
		}
	}
	// One source at a time, in separate runs, ends in the same ledger too.
	s := newStore(t)
	for _, a := range intelAdapters(t) {
		if _, err := Run(ctx, s, []adapter.Adapter{a}, Options{Host: "r12-n034", Now: intelNow}); err != nil {
			t.Fatal(err)
		}
	}
	if got := totals(t, s, "r12-n034"); !reflect.DeepEqual(got, ref) {
		t.Errorf("separate runs\n got: %+v\nwant: %+v", got, ref)
	}
}

func TestEDACPositionsWithoutEvidenceStayApart(t *testing.T) {
	// SMBIOS with locators only: nothing ties "CPU0_C1" to an EDAC position.
	var b smbios.Builder
	for i, loc := range []string{"DIMM 1", "DIMM 2", "DIMM 3", "DIMM 4"} {
		b.AddMemoryDevice(smbios.MemoryDevice{Handle: uint16(0x1100 + i), Locator: loc, SizeMB: 32768,
			MemoryType: smbios.TypeDDR4, FormFactor: smbios.FormDIMM, Manufacturer: "Example Memory", Serial: fmt.Sprintf("SER%d", i), PartNumber: "P"})
	}
	s := newStore(t)
	as := intelAdapters(t)
	as[2] = smbios.NewTables(fstest.MapFS{"DMI": {Data: b.Bytes()}}, ".", "tables", smbios.Options{Now: intelNow})
	res, err := Run(ctx, s, as, Options{Host: "h", Now: intelNow})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Sources[len(res.Sources)-1]
	if m.Kind != SlotMapKind || m.Status != model.SourcePartial || m.Skipped != 4 ||
		!strings.Contains(strings.Join(m.Notes, " "), "4 of 4 EDAC DIMM positions could not be tied to an SMBIOS slot") {
		t.Errorf("slot map entry = %+v", m)
	}
	// The errors are kept under the EDAC label, with a placeholder identity:
	// no serial number is blamed.
	got := totals(t, s, "h")
	want := []slotTotal{
		{Slot: "", CE: 2},
		{Slot: "CPU_SrcID#0_MC#1_Chan#0_DIMM#0", CE: 4, UE: 1},
		{Slot: "CPU_SrcID#0_MC#1_Chan#1_DIMM#0", CE: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("totals\n got: %+v\nwant: %+v", got, want)
	}
	events, _ := s.EventList(ctx, store.EventFilter{Host: "h", Slot: "CPU_SrcID#0_MC#1_Chan#0_DIMM#0"})
	for _, e := range events {
		if e.DIMMLink != model.LinkSlot || e.DIMMUID != model.SlotUID("h", "CPU_SrcID#0_MC#1_Chan#0_DIMM#0") {
			t.Errorf("%s: dimm %q (%s)", e.RawRef, e.DIMMUID, e.DIMMLink)
		}
	}
	slots, _ := s.Slots(ctx, "h")
	if len(slots) != 8 {
		t.Errorf("%d slots, want the 4 of SMBIOS and the 4 of EDAC", len(slots))
	}
}

func TestMapEDAC(t *testing.T) {
	mod := func(loc, bank string, sizeMB int) boardSlot {
		b := boardSlot{name: loc, locator: loc, bankLocator: bank, populated: sizeMB > 0, sizeBytes: int64(sizeMB) << 20}
		b.socket, b.channel, b.position = bankLocatorTopology(bank)
		return b
	}
	intel := func(s, imc, ch, slot, sizeMB int) edacDIMM {
		label := fmt.Sprintf("CPU_SrcID#%d_MC#%d_Chan#%d_DIMM#%d", s, imc, ch, slot)
		return edacDIMM{mc: s*4 + imc, loc: fmt.Sprintf("%d:%d", ch, slot), label: label, sizeMB: model.Some(sizeMB), parsed: edac.ParseLabel(label)}
	}
	csrow := func(mc, cs, ch int) edacDIMM {
		label := fmt.Sprintf("mc#%dcsrow#%dchannel#%d", mc, cs, ch)
		return edacDIMM{mc: mc, loc: fmt.Sprintf("%d:%d", cs, ch), label: label, parsed: edac.ParseLabel(label)}
	}
	bank := func(s, ch, pos int) string { return fmt.Sprintf("P%d_Node%d_Channel%d_Dimm%d", s, s, ch, pos) }

	tests := []struct {
		name   string
		mods   []boardSlot
		dimms  []edacDIMM
		slot   []int
		method string
		k      int
	}{
		{
			name:   "three channels per controller (6 channels per socket)",
			mods:   []boardSlot{mod("A1", bank(0, 0, 0), 16384), mod("D1", bank(0, 3, 0), 16384), mod("F1", bank(0, 5, 0), 16384), mod("F2", bank(0, 5, 1), 0)},
			dimms:  []edacDIMM{intel(0, 0, 0, 0, 16384), intel(0, 1, 0, 0, 16384), intel(0, 1, 2, 0, 16384)},
			slot:   []int{0, 1, 2},
			method: model.MapBankLocator, k: 3,
		},
		{
			name:   "two sockets, two channels per controller",
			mods:   []boardSlot{mod("P1-A1", bank(0, 0, 0), 32768), mod("P1-C1", bank(0, 2, 0), 32768), mod("P2-B1", bank(1, 1, 0), 32768)},
			dimms:  []edacDIMM{intel(0, 0, 0, 0, 32768), intel(0, 1, 0, 0, 32768), intel(1, 0, 1, 0, 32768)},
			slot:   []int{0, 1, 2},
			method: model.MapBankLocator, k: 2,
		},
		{
			name:  "a populated slot no EDAC DIMM lands on: no evidence",
			mods:  []boardSlot{mod("A1", bank(0, 0, 0), 16384), mod("B1", bank(0, 1, 0), 16384), mod("H1", bank(0, 7, 0), 16384)},
			dimms: []edacDIMM{intel(0, 0, 0, 0, 16384), intel(0, 0, 1, 0, 16384)},
			slot:  []int{-1, -1},
		},
		{
			name:  "sizes disagree: no evidence",
			mods:  []boardSlot{mod("A1", bank(0, 0, 0), 16384), mod("B1", bank(0, 1, 0), 32768)},
			dimms: []edacDIMM{intel(0, 0, 0, 0, 32768), intel(0, 0, 1, 0, 16384)},
			slot:  []int{-1, -1},
		},
		{
			name:   "one DIMM: every controller width gives the same answer",
			mods:   []boardSlot{mod("A1", bank(0, 0, 0), 16384)},
			dimms:  []edacDIMM{intel(0, 0, 0, 0, 16384)},
			slot:   []int{0},
			method: model.MapBankLocator, k: 1,
		},
		{
			name:  "one DIMM on the second controller: its channel number depends on the width",
			mods:  []boardSlot{mod("C1", bank(0, 2, 0), 16384)},
			dimms: []edacDIMM{intel(0, 1, 0, 0, 16384)},
			slot:  []int{0}, method: model.MapBankLocator, k: 2,
		},
		{
			name:   "labels registered by the administrator equal the locators",
			mods:   []boardSlot{mod("CPU1_DIMM_A1", "", 16384), mod("CPU1_DIMM_B1", "", 16384)},
			dimms:  []edacDIMM{{mc: 0, loc: "1:0", label: "cpu1_dimm_b1"}, {mc: 0, loc: "0:0", label: "CPU1_DIMM_A1"}},
			slot:   []int{1, 0},
			method: model.MapLabel,
		},
		{
			name:   "chip-select driver: two ranks of one DIMM are one slot",
			mods:   []boardSlot{mod("P0_DIMM_A1", bank(0, 0, 0), 32768), mod("P0_DIMM_B1", bank(0, 1, 0), 32768), mod("P0_DIMM_B2", bank(0, 1, 1), 32768)},
			dimms:  []edacDIMM{csrow(0, 0, 0), csrow(0, 1, 0), csrow(0, 0, 1), csrow(0, 2, 1), csrow(0, 3, 1)},
			slot:   []int{0, 0, 1, 2, 2},
			method: model.MapBankLocator,
		},
		{
			name:  "locators without numbers and default labels: nothing to go on",
			mods:  []boardSlot{mod("DIMM 1", "", 16384), mod("DIMM 2", "", 16384)},
			dimms: []edacDIMM{intel(0, 0, 0, 0, 16384), intel(0, 0, 1, 0, 16384)},
			slot:  []int{-1, -1},
		},
		{name: "no SMBIOS", dimms: []edacDIMM{intel(0, 0, 0, 0, 16384)}, slot: []int{-1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mp := mapEDAC(tt.mods, tt.dimms)
			if !reflect.DeepEqual(mp.slot, tt.slot) {
				t.Fatalf("slots = %v, want %v", mp.slot, tt.slot)
			}
			for i, j := range mp.slot {
				if j >= 0 && mp.method[i] != tt.method {
					t.Errorf("method[%d] = %q, want %q", i, mp.method[i], tt.method)
				}
			}
			if mp.chanPerIMC != tt.k {
				t.Errorf("channels per controller = %d, want %d", mp.chanPerIMC, tt.k)
			}
		})
	}
}

func TestBankLocatorTopology(t *testing.T) {
	type topo struct{ s, c, p model.Opt[int] }
	some := func(s, c, p int) topo { return topo{model.Some(s), model.Some(c), model.Some(p)} }
	for in, want := range map[string]topo{
		"P0_Node0_Channel2_Dimm0": some(0, 2, 0),
		"P1_Node1_Channel7_Dimm1": some(1, 7, 1),
		"_Node1_Channel3_Dimm0":   some(1, 3, 0),
		"NODE 0 CHANNEL 5 DIMM 1": some(0, 5, 1),
		"p0_node0_channel2_dimm0": some(0, 2, 0),
		"BANK 0":                  {},
		"P0 CHANNEL A":            {},
		"":                        {},
		"P0_Node0_ChannelX_Dimm0": {},
	} {
		s, c, p := bankLocatorTopology(in)
		if got := (topo{s, c, p}); got != want {
			t.Errorf("bankLocatorTopology(%q) = %+v, want %+v", in, got, want)
		}
	}
}

func TestSlotNames(t *testing.T) {
	mods := []*model.Module{
		{Locator: "DIMM 1", BankLocator: "CPU 1"}, {Locator: "DIMM 1", BankLocator: "CPU 2"}, // the same locator on two sockets
		{Locator: "A1"}, {Locator: "", BankLocator: "BANK 3"}, {Handle: model.Some(0x1105)}, {}, {},
	}
	got := slotNames(mods)
	want := []string{"CPU 1 DIMM 1", "CPU 2 DIMM 1", "A1", "BANK 3", "handle 0x1105", "slot", "slot #2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("names = %q, want %q", got, want)
	}
}

func TestDeclaredDuplicatesAndInvalidRecords(t *testing.T) {
	s := newStore(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := &fake{kind: "rasdaemon", fn: func(sink adapter.Sink) error {
		primary := &model.Event{ErrorDT: t0, ErrorSeverity: model.SevCE, Source: "rasdaemon/mc_event", RawRef: "mc_event/1", Label: "DIMM_A1",
			OsPhysicalAddress: model.Some(uint64(0x7f5d2c9c0)), Fingerprint: "mc-1"}
		dup := &model.Event{ErrorDT: t0, ErrorSeverity: model.SevCE, Source: "rasdaemon/mce_record", RawRef: "mce_record/1",
			Fingerprint: "mce-1", DupOfFingerprint: "mc-1"}
		noTime := &model.Event{ErrorSeverity: model.SevCE, Source: "rasdaemon/mc_event", RawRef: "mc_event/2", Fingerprint: "mc-2"}
		badSev := &model.Event{ErrorDT: t0, ErrorSeverity: "Corrected", Source: "rasdaemon/mc_event", RawRef: "mc_event/3", Fingerprint: "mc-3"}
		for _, e := range []*model.Event{dup, primary, noTime, badSev} {
			if err := sink.Event(e); err != nil {
				return err
			}
		}
		return sink.Action(&model.Action{At: t0, Kind: "format-disk", Source: "x", RawRef: "action/1", Fingerprint: "a-1"})
	}}
	res, err := Run(ctx, s, []adapter.Adapter{a}, Options{Host: "h1", Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	src := res.Sources[0]
	if src.Status != model.SourcePartial || src.Events != 2 || src.Skipped != 3 || res.Duplicates != 1 {
		t.Errorf("source = %+v, duplicates = %d", src, res.Duplicates)
	}
	notes := strings.Join(src.Notes, "\n")
	for _, want := range []string{"mc_event/2: event: ErrorDT", `mc_event/3: event: unknown ErrorSeverity "Corrected"`, `action/1: action: unknown kind "format-disk"`} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes lack %q:\n%s", want, notes)
		}
	}
	events, _ := s.EventList(ctx, store.EventFilter{Host: "h1", WithDuplicates: true})
	if len(events) != 2 {
		t.Fatalf("%d events", len(events))
	}
	byRef := map[string]*model.Event{}
	for _, e := range events {
		byRef[e.RawRef] = e
	}
	if byRef["mce_record/1"].DupOf != byRef["mc_event/1"].ID || byRef["mc_event/1"].DupOf != 0 {
		t.Errorf("declared duplicate was not resolved: %+v", byRef["mce_record/1"])
	}

	// The same record identities on another host are other records.
	if _, err := Run(ctx, s, []adapter.Adapter{a}, Options{Host: "h2", Now: t0}); err != nil {
		t.Fatal(err)
	}
	all, _ := s.EventList(ctx, store.EventFilter{WithDuplicates: true})
	if len(all) != 4 {
		t.Errorf("%d events after importing the same identities for a second host, want 4", len(all))
	}
}

func TestHostName(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		opt  string
		as   []adapter.Adapter
		want string
	}{
		{"given", "db-17", []adapter.Adapter{&fake{kind: "a", hint: "other"}}, "db-17"},
		{"from the first adapter that knows it", "", []adapter.Adapter{&fake{kind: "a"}, &fake{kind: "b", hint: " r12-n034 "}, &fake{kind: "c", hint: "late"}}, "r12-n034"},
		{"unknown", "", []adapter.Adapter{&fake{kind: "a"}}, "localhost"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t)
			res, err := Run(ctx, s, tt.as, Options{Host: tt.opt, Now: t0})
			if err != nil {
				t.Fatal(err)
			}
			hosts, _ := s.Hosts(ctx)
			if res.Host != tt.want || len(hosts) != 1 || hosts[0].Name != tt.want {
				t.Errorf("host = %q, ledger hosts = %+v, want %q", res.Host, hosts, tt.want)
			}
		})
	}
}

func TestUnreadableSourcesAreInTheInventory(t *testing.T) {
	s := newStore(t)
	dir := t.TempDir()
	as := []adapter.Adapter{
		rasdaemon.New(filepath.Join(dir, "ras-mc_event.db"), rasdaemon.Options{}),
		klog.NewFile(filepath.Join(dir, "dmesg"), klog.Options{}),
		edacsysfs.NewRoot(dir, edacsysfs.Options{}),
		smbios.NewRoot(dir, smbios.Options{}),
		&fake{kind: "broken", rep: adapter.Report{Status: model.SourceError, Detail: "database disk image is malformed"}},
	}
	res, err := Run(ctx, s, as, Options{Host: "vm-1", Now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("unreadable sources must not fail the run: %v", err)
	}
	stored, _ := s.Sources(ctx, res.Scan.ID)
	if len(stored) != 5 {
		t.Fatalf("%d inventory entries, want 5", len(stored))
	}
	for i, want := range []model.SourceStatus{model.SourceAbsent, model.SourceAbsent, model.SourceAbsent, model.SourceAbsent, model.SourceError} {
		if stored[i].Status != want || stored[i].Detail == "" {
			t.Errorf("source %s: status %s detail %q, want %s with a reason", stored[i].Kind, stored[i].Status, stored[i].Detail, want)
		}
	}
	if evs, _ := s.EventList(ctx, store.EventFilter{}); len(evs) != 0 {
		t.Errorf("%d events from sources that could not be read", len(evs))
	}
}

func TestLedgerFailureFailsTheRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	s, err := store.Open(ctx, path, store.Create)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	ro, err := store.Open(ctx, path, store.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if _, err := Run(ctx, ro, []adapter.Adapter{&fake{kind: "a"}}, Options{Host: "h"}); !errors.Is(err, store.ErrReadOnly) {
		t.Errorf("err = %v, want ErrReadOnly", err)
	}

	// A source that fails in the middle leaves none of its records behind.
	s2 := newStore(t)
	boom := errors.New("disk full")
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	half := &fake{kind: "half", fn: func(sink adapter.Sink) error {
		if err := sink.Event(&model.Event{ErrorDT: t0, ErrorSeverity: model.SevCE, Source: "x", RawRef: "1", Fingerprint: "1"}); err != nil {
			return err
		}
		return boom
	}}
	if _, err := Run(ctx, s2, []adapter.Adapter{half}, Options{Host: "h", Now: t0}); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the sink error", err)
	}
	if evs, _ := s2.EventList(ctx, store.EventFilter{}); len(evs) != 0 {
		t.Errorf("%d events of a failed source were kept", len(evs))
	}

	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Run(cctx, newStore(t), []adapter.Adapter{&fake{kind: "a"}}, Options{Host: "h"}); err == nil {
		t.Error("a cancelled run returned no error")
	}
}

func TestPlacementsFollowADIMMAcrossRuns(t *testing.T) {
	table := func(c1, d1 string) fstest.MapFS {
		var b smbios.Builder
		for i, serial := range []string{c1, d1} {
			loc := []string{"CPU0_C1", "CPU1_D1"}[i]
			if serial == "" {
				b.AddMemoryDevice(smbios.MemoryDevice{Handle: uint16(0x1100 + i), Locator: loc})
				continue
			}
			b.AddMemoryDevice(smbios.MemoryDevice{Handle: uint16(0x1100 + i), Locator: loc, SizeMB: 65536, MemoryType: smbios.TypeDDR5,
				FormFactor: smbios.FormDIMM, Manufacturer: "Example Memory", Serial: serial, PartNumber: "EXM64G48R2X4-A"})
		}
		return fstest.MapFS{"DMI": {Data: b.Bytes()}}
	}
	s := newStore(t)
	day := 24 * time.Hour
	t1 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	scan := func(at time.Time, c1, d1 string) {
		t.Helper()
		a := smbios.NewTables(table(c1, d1), ".", "tables", smbios.Options{Now: at})
		if _, err := Run(ctx, s, []adapter.Adapter{a}, Options{Host: "h", Now: at}); err != nil {
			t.Fatal(err)
		}
	}
	scan(t1, "80A1C3", "")            // the DIMM is in CPU0_C1
	scan(t1.Add(5*day), "80A1C3", "") // still there
	scan(t1.Add(9*day), "", "80A1C3") // moved to CPU1_D1 for a cross test
	scan(t1.Add(12*day), "90B2D4", "80A1C3")

	uid := model.DIMMUID("Example Memory", "EXM64G48R2X4-A", "80A1C3")
	pl, err := s.Placements(ctx, "h", uid)
	if err != nil {
		t.Fatal(err)
	}
	type span struct {
		slot        string
		first, last time.Duration
	}
	var got []span
	for _, p := range pl {
		got = append(got, span{p.Slot, p.FirstSeen.Sub(t1), p.LastSeen.Sub(t1)})
	}
	want := []span{{"CPU0_C1", 0, 5 * day}, {"CPU1_D1", 9 * day, 12 * day}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("placements of the moved DIMM = %+v, want %+v", got, want)
	}
	all, _ := s.Placements(ctx, "h", "")
	if len(all) != 3 {
		t.Errorf("%d placements, want 3 (the moved DIMM twice, the new DIMM once)", len(all))
	}
	// The DIMM row is one: its history follows it.
	dimms, _ := s.DIMMs(ctx)
	if len(dimms) != 2 {
		t.Errorf("%d DIMM identities, want 2", len(dimms))
	}
}

func TestRasdaemonAMDWithCounters(t *testing.T) {
	// The chip-select layers of amd64_edac: rank objects in sysfs, csrow
	// and channel in the database, numeric bank locators in SMBIOS.
	var b smbios.Builder
	for i, v := range []struct {
		loc    string
		ch, sl int
		serial string
	}{{"P0_DIMM_A1", 0, 0, "AM0001"}, {"P0_DIMM_B1", 1, 0, "AM0002"}, {"P0_DIMM_B2", 1, 1, "AM0003"}} {
		b.AddMemoryDevice(smbios.MemoryDevice{Handle: uint16(0x1100 + i), Locator: v.loc, BankLocator: fmt.Sprintf("P0_Node0_Channel%d_Dimm%d", v.ch, v.sl),
			SizeMB: 32768, MemoryType: smbios.TypeDDR5, FormFactor: smbios.FormDIMM, Manufacturer: "Example Memory", Serial: v.serial, PartNumber: "EXM32G48R2X4-A"})
	}
	db := testutil.BuildSQLite(t, testutil.Testdata(t, "rasdaemon", "amd-upgraded-0.8.sql"), "ras-mc_event.db")
	now := time.Date(2024, 6, 20, 0, 0, 0, 0, time.UTC)
	s := newStore(t)
	res, err := Run(ctx, s, []adapter.Adapter{
		rasdaemon.New(db, rasdaemon.Options{Direct: true}),
		edacsysfs.New(os.DirFS(testutil.Testdata(t, "edac-sysfs", "amd64")), "mc", "sysfs", edacsysfs.Options{Now: now}),
		smbios.NewTables(fstest.MapFS{"DMI": {Data: b.Bytes()}}, ".", "tables", smbios.Options{Now: now}),
	}, Options{Host: "epyc-a17", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if m := res.Sources[len(res.Sources)-1]; m.Kind != SlotMapKind || m.Status != model.SourceOK {
		t.Errorf("slot map = %+v", m)
	}
	// Events of chip select 2 on channel 1 belong to the second DIMM of
	// that channel.
	evs, _ := s.EventList(ctx, store.EventFilter{Host: "epyc-a17", Slot: "P0_DIMM_B2", Kinds: []model.EventKind{model.KindError}})
	if len(evs) != 3 {
		t.Fatalf("%d located events on P0_DIMM_B2, want 3 (two corrected, one deferred)", len(evs))
	}
	d, _ := s.DIMM(ctx, evs[0].DIMMUID)
	if d == nil || d.Serial != "AM0003" || d.DeviceWidth != model.Some(4) {
		t.Errorf("DIMM of P0_DIMM_B2 = %+v", d)
	}
	// The machine check records of the two corrected errors are duplicates.
	if res.Duplicates != 2 {
		t.Errorf("duplicates = %d, want 2", res.Duplicates)
	}
	// The counter reading (2 corrected errors since the last reset) is
	// covered by the two located events only if they fall in its window.
	// They are older than the window here, so the counter adds 2.
	var ce int64
	for _, tt := range totals(t, s, "epyc-a17") {
		if tt.Slot == "P0_DIMM_B2" {
			ce = tt.CE
		}
	}
	if ce != 4 {
		t.Errorf("corrected errors of P0_DIMM_B2 = %d, want 4 (2 located before the counting window, 2 counted in it)", ce)
	}
}
