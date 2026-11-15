package ingest

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/sel"
	"github.com/agenticode/my/internal/adapter/smbios"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/testutil"
)

// bmcModules is a board as a BMC inventory describes it: slots it names and
// numbers itself.
func bmcModules(at time.Time) []*model.Module {
	var out []*model.Module
	for i, name := range []string{"DIMM_A1", "DIMM_B1", "DIMM_C1"} {
		m := &model.Module{ObservedAt: at, Locator: name, Socket: model.Some(0), Channel: model.Some(i), Position: model.Some(0),
			Populated: i < 2, Source: "redfish", RawRef: "Memory/" + name}
		if m.Populated {
			m.DIMM = model.DIMM{Manufacturer: "Example Memory", PartNumber: "EXM64G48R2X4-A", Serial: []string{"S-A1", "S-B1"}[i], SizeBytes: 64 << 30}
		}
		out = append(out, m)
	}
	return out
}

func emitModules(sink adapter.Sink, mods []*model.Module) error {
	for _, m := range mods {
		if err := sink.Module(m); err != nil {
			return err
		}
	}
	return nil
}

func TestGPUAndECSRecords(t *testing.T) {
	s := newStore(t)
	t0 := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	inputs := &fake{kind: "inputs", fn: func(sink adapter.Sink) error {
		if err := sink.Boot(&model.Boot{BootID: "0123456789abcdef0123456789abcdef", StartedAt: t0, Source: "test"}); err != nil {
			return err
		}
		if err := emitModules(sink, bmcModules(t0.Add(time.Hour))); err != nil {
			return err
		}
		for _, g := range []*model.GPUMem{
			{ObservedAt: t0.Add(2 * time.Hour), Kind: model.GPUXID, XID: model.Some(48), XIDText: "double bit ECC error", PCIBus: "0000:3b:00.0",
				Source: "klog/xid", RawRef: "dmesg#L10", Fingerprint: "xid-1"},
			{ObservedAt: t0.Add(3 * time.Hour), Kind: model.GPUSnapshot, GPUSerial: "1650000000412", GPUUUID: "GPU-3", GPUIndex: model.Some(3), PCIBus: "0000:3b:00.0",
				RemapUncorrectable: model.Some(1), RemapPending: model.Some(true), Source: "nvidia-smi", RawRef: "smi#3", Fingerprint: "snap-3"},
			{ObservedAt: t0.Add(2 * time.Hour), Kind: model.GPUXID, Source: "klog/xid", RawRef: "dmesg#L11", Fingerprint: "xid-bad"}, // no XID number
		} {
			if err := sink.GPU(g); err != nil {
				return err
			}
		}
		ecs := func(ref string, d time.Duration) *model.ECSSample {
			return &model.ECSSample{ObservedAt: t0.Add(d), MR14: model.Some(0x20), MR15: model.Some(3), MR20: model.Some(1),
				Source: "fmfm/runtime", RawRef: ref, Fingerprint: ref}
		}
		byLabel, otherSpelling, byPlace, noPlace, unknown, empty := ecs("r1", 4*time.Hour), ecs("r2", 5*time.Hour), ecs("r3", 6*time.Hour),
			ecs("r4", 7*time.Hour), ecs("r5", 8*time.Hour), ecs("r6", 9*time.Hour)
		byLabel.Label = "DIMM_A1"
		otherSpelling.Label = "dimm a1"
		byPlace.SocketId, byPlace.ChannelId, byPlace.ChannelSlotId = model.Some(0), model.Some(1), model.Some(0)
		noPlace.SocketId, noPlace.ChannelId, noPlace.ChannelSlotId = model.Some(1), model.Some(5), model.Some(0)
		unknown.Label = "DIMM_Z9"
		empty.MR14, empty.MR15, empty.MR20 = model.Opt[int]{}, model.Opt[int]{}, model.Opt[int]{}
		for _, e := range []*model.ECSSample{byLabel, otherSpelling, byPlace, noPlace, unknown, empty} {
			if err := sink.ECS(e); err != nil {
				return err
			}
		}
		return nil
	}}
	res, err := Run(ctx, s, []adapter.Adapter{inputs}, Options{Host: "gpu-01", Now: t0.Add(24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	src := res.Sources[0]
	if res.Samples != 7 || src.Samples != 7 || src.Skipped != 2 || src.Status != model.SourcePartial {
		t.Errorf("samples: run %d, source %d, skipped %d, status %s; want 7, 7, 2, partial", res.Samples, src.Samples, src.Skipped, src.Status)
	}
	if !src.FirstTS.Equal(t0.Add(2*time.Hour)) || !src.LastTS.Equal(t0.Add(8*time.Hour)) {
		t.Errorf("period of the source = %v .. %v", src.FirstTS, src.LastTS)
	}
	notes := strings.Join(src.Notes, "\n")
	for _, want := range []string{"dmesg#L11: gpu_mem: an xid record needs the XID number", "r6: ecs: no register was read"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes lack %q:\n%s", want, notes)
		}
	}

	gpus, err := s.GPUMem(ctx, "gpu-01")
	if err != nil || len(gpus) != 2 {
		t.Fatalf("GPU records = %+v, %v", gpus, err)
	}
	// The XID line names the GPU by its PCI address; the snapshot tells
	// which GPU that is. Both belong to the boot they fall into.
	if x := gpus[0]; x.GPUSerial != "1650000000412" || x.GPUUUID != "GPU-3" || x.BootID != "0123456789abcdef0123456789abcdef" || x.ScanID != res.Scan.ID {
		t.Errorf("XID record = %+v", x)
	}

	readings, err := s.ECS(ctx, store.ECSFilter{Host: "gpu-01"})
	if err != nil || len(readings) != 5 {
		t.Fatalf("ECS readings = %+v, %v", readings, err)
	}
	type place struct{ Slot, Serial string }
	got := map[string]place{}
	for _, e := range readings {
		p := place{Slot: e.Slot}
		if e.DIMMUID != "" {
			d, err := s.DIMM(ctx, e.DIMMUID)
			if err != nil || d == nil {
				t.Fatalf("dimm %s: %v", e.DIMMUID, err)
			}
			p.Serial = d.Serial
		}
		got[e.RawRef] = p
	}
	want := map[string]place{
		"r1": {"DIMM_A1", "S-A1"},
		"r2": {"DIMM_A1", "S-A1"},            // the same label with other separators
		"r3": {"DIMM_B1", "S-B1"},            // by the numbers the inventory gave the slot
		"r4": {"socket1 channel5 dimm0", ""}, // numbers no inventory names: a slot named by them
		"r5": {"DIMM_Z9", ""},                // a label nothing else names is a slot of its own
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("slots of the readings = %v\nwant %v", got, want)
	}

	// Reading the same source again adds nothing.
	res2, err := Run(ctx, s, []adapter.Adapter{inputs}, Options{Host: "gpu-01", Now: t0.Add(25 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Samples != 0 || res2.Sources[0].Samples != 7 || !strings.Contains(strings.Join(res2.Sources[0].Notes, "\n"), "7 records were already in the ledger") {
		t.Errorf("second run: %d new samples, source %+v", res2.Samples, res2.Sources[0])
	}
	if again, _ := s.ECS(ctx, store.ECSFilter{Host: "gpu-01"}); !reflect.DeepEqual(again, readings) {
		t.Errorf("readings changed on the second run\n got: %+v\nwant: %+v", again, readings)
	}
}

// TestInventoryPlaces checks slots an inventory numbers itself: the numbers
// stay on the slot rows across runs, and error records that name a place by
// the same numbers are tied to the slot.
func TestInventoryPlaces(t *testing.T) {
	s := newStore(t)
	t0 := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	ev := func(ref string, d time.Duration, socket, channel, position int) *model.Event {
		return &model.Event{ErrorDT: t0.Add(d), ErrorSeverity: model.SevCE, Source: "fmfm/runtime", RawRef: ref, Fingerprint: ref,
			SocketId: model.Some(socket), ChannelId: model.Some(channel), ChannelSlotId: model.Some(position)}
	}
	inventory := &fake{kind: "redfish", fn: func(sink adapter.Sink) error { return emitModules(sink, bmcModules(t0)) }}
	errors1 := &fake{kind: "fmfm", fn: func(sink adapter.Sink) error {
		labelled := ev("e3", 3*time.Hour, 0, 2, 0)
		labelled.Label = "J17" // a name nothing else uses, with the numbers of DIMM_C1
		for _, e := range []*model.Event{ev("e1", time.Hour, 0, 1, 0), ev("e2", 2*time.Hour, 1, 0, 0), labelled} {
			if err := sink.Event(e); err != nil {
				return err
			}
		}
		return nil
	}}
	if _, err := Run(ctx, s, []adapter.Adapter{inventory, errors1}, Options{Host: "h1", Now: t0.Add(24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	// A later run reads errors only. The place aliases are still there.
	errors2 := &fake{kind: "fmfm", fn: func(sink adapter.Sink) error { return sink.Event(ev("e4", 30*time.Hour, 0, 0, 0)) }}
	if _, err := Run(ctx, s, []adapter.Adapter{errors2}, Options{Host: "h1", Now: t0.Add(48 * time.Hour)}); err != nil {
		t.Fatal(err)
	}

	events, err := s.EventList(ctx, store.EventFilter{Host: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range events {
		got[e.RawRef] = e.Slot
	}
	want := map[string]string{
		"e1": "DIMM_B1",
		"e2": "socket1 channel0 dimm0", // socket 1 is not in the inventory: a slot named by the numbers
		"e3": "DIMM_C1",                // the label is unknown, the numbers are the inventory's
		"e4": "DIMM_A1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("slots of the events = %v\nwant %v", got, want)
	}

	slots, err := s.Slots(ctx, "h1")
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		Socket, Channel, Position model.Opt[int]
		Populated                 model.Opt[bool]
	}
	rows := map[string]row{}
	for _, sl := range slots {
		rows[sl.Name] = row{sl.Socket, sl.Channel, sl.Position, sl.Populated}
	}
	zero := model.Some(0)
	wantRows := map[string]row{
		"DIMM_A1": {zero, model.Some(0), zero, model.Some(true)},
		"DIMM_B1": {zero, model.Some(1), zero, model.Some(true)},
		"DIMM_C1": {zero, model.Some(2), zero, model.Some(false)},
		// Known through an error only.
		"socket1 channel0 dimm0": {model.Some(1), model.Some(0), zero, model.Opt[bool]{}},
	}
	if !reflect.DeepEqual(rows, wantRows) {
		t.Errorf("slot rows after two runs = %v\nwant %v", rows, wantRows)
	}
	aliases, _ := s.SlotAliases(ctx, "h1")
	if aliases["place:0:2:0"] != "DIMM_C1" {
		t.Errorf("place 0:2:0 names %q, want DIMM_C1", aliases["place:0:2:0"])
	}
}

// TestPlaceNamedByNumbers follows a place that error records name by
// socket, channel and slot number only. It gets a slot named by the
// numbers. When an inventory later names the place, the records move to
// the inventory's slot and the numbered slot is gone.
func TestPlaceNamedByNumbers(t *testing.T) {
	s := newStore(t)
	t0 := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	ev := func(ref string, d time.Duration) *model.Event {
		return &model.Event{ErrorDT: t0.Add(d), ErrorSeverity: model.SevCE, Source: "fmfm/runtime", RawRef: ref, Fingerprint: ref,
			SocketId: model.Some(0), ChannelId: model.Some(1), ChannelSlotId: model.Some(0)}
	}
	errs := &fake{kind: "fmfm", fn: func(sink adapter.Sink) error {
		if err := sink.Event(ev("e1", time.Hour)); err != nil {
			return err
		}
		return sink.ECS(&model.ECSSample{ObservedAt: t0.Add(2 * time.Hour), SocketId: model.Some(0), ChannelId: model.Some(1), ChannelSlotId: model.Some(0),
			MR20: model.Some(1), Source: "fmfm/runtime", RawRef: "c1", Fingerprint: "c1"})
	}}
	if _, err := Run(ctx, s, []adapter.Adapter{errs}, Options{Host: "h1", Now: t0.Add(3 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	state := func() (eventSlot, ecsSlot, identity string, slots []string) {
		events, _ := s.EventList(ctx, store.EventFilter{Host: "h1"})
		readings, _ := s.ECS(ctx, store.ECSFilter{Host: "h1"})
		rows, _ := s.Slots(ctx, "h1")
		for _, r := range rows {
			slots = append(slots, r.Name)
		}
		if len(events) != 1 || len(readings) != 1 {
			t.Fatalf("%d events, %d readings", len(events), len(readings))
		}
		if d, _ := s.DIMM(ctx, events[0].DIMMUID); d != nil {
			identity = string(d.Identity) + " " + d.Serial
		}
		return events[0].Slot, readings[0].Slot, strings.TrimSpace(identity), slots
	}
	if e, c, id, slots := state(); e != "socket0 channel1 dimm0" || c != e || id != "slot" || !reflect.DeepEqual(slots, []string{"socket0 channel1 dimm0"}) {
		t.Errorf("before the inventory: event slot %q, reading slot %q, identity %q, slots %q", e, c, id, slots)
	}
	// The inventory arrives in a later run.
	inventory := &fake{kind: "redfish", fn: func(sink adapter.Sink) error { return emitModules(sink, bmcModules(t0)) }}
	if _, err := Run(ctx, s, []adapter.Adapter{inventory}, Options{Host: "h1", Now: t0.Add(24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if e, c, id, slots := state(); e != "DIMM_B1" || c != "DIMM_B1" || id != "serial S-B1" || !reflect.DeepEqual(slots, []string{"DIMM_A1", "DIMM_B1", "DIMM_C1"}) {
		t.Errorf("after the inventory: event slot %q, reading slot %q, identity %q, slots %q", e, c, id, slots)
	}
	dimms, _ := s.DIMMs(ctx)
	for _, d := range dimms {
		if d.Identity == model.IdentitySlot {
			t.Errorf("the placeholder identity of the numbered slot is still there: %+v", d)
		}
	}
}

// Records that are all second reports of records with a better name do
// not make a slot of their own: a machine check next to the EDAC record
// decoded from it.
func TestSecondReportsNameNoSlot(t *testing.T) {
	s := newStore(t)
	t0 := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	a := &fake{kind: "rasdaemon", fn: func(sink adapter.Sink) error {
		primary := &model.Event{ErrorDT: t0, ErrorSeverity: model.SevCE, Source: "rasdaemon/mc_event", RawRef: "mc_event/1", Label: "DIMM_A1", Fingerprint: "mc-1"}
		twin := &model.Event{ErrorDT: t0, ErrorSeverity: model.SevCE, Source: "rasdaemon/mce_record", RawRef: "mce_record/1", Fingerprint: "mce-1",
			DupOfFingerprint: "mc-1", SocketId: model.Some(0), ChannelId: model.Some(1), ChannelSlotId: model.Some(0)}
		if err := sink.Event(primary); err != nil {
			return err
		}
		return sink.Event(twin)
	}}
	if _, err := Run(ctx, s, []adapter.Adapter{a}, Options{Host: "h1", Now: t0}); err != nil {
		t.Fatal(err)
	}
	slots, _ := s.Slots(ctx, "h1")
	if len(slots) != 1 || slots[0].Name != "DIMM_A1" {
		t.Errorf("slots = %+v, want DIMM_A1 only", slots)
	}
}

func TestLookupLabel(t *testing.T) {
	aliases := map[string]string{
		"label:DIMM_A1":    "DIMM_A1",
		"label:P1-DIMMB1":  "P1-DIMMB1",
		"label:CPU1 DIMM2": "slot x",
		"label:CPU1_DIMM2": "slot y",
		"handle:4352":      "DIMM_A1",
		"label:A-1":        "A-1",
	}
	for label, want := range map[string]string{
		"DIMM_A1":   "DIMM_A1",
		"dimm_a1":   "DIMM_A1",
		"DIMM A1":   "DIMM_A1",
		"DIMM-A1":   "DIMM_A1",
		"DIMMA1":    "DIMM_A1",
		"P1_DIMMB1": "P1-DIMMB1",
		"A1":        "A-1",
	} {
		if got, ok := lookupLabel(aliases, label); !ok || got != want {
			t.Errorf("lookupLabel(%q) = %q, %v; want %q", label, got, ok, want)
		}
	}
	for _, label := range []string{
		"CPU1-DIMM2", // fits two slots when separators are ignored
		"DIMM_A2", "IMM_A1", "", "---", "4352",
	} {
		if got, ok := lookupLabel(aliases, label); ok {
			t.Errorf("lookupLabel(%q) = %q, want no slot", label, got)
		}
	}
	// The exact label wins where squashing would be ambiguous.
	if got, ok := lookupLabel(aliases, "CPU1 DIMM2"); !ok || got != "slot x" {
		t.Errorf("exact label = %q, %v", got, ok)
	}
}

// An action that names its target with other separators than the inventory
// is tied to the inventory's slot.
func TestActionLabelSeparators(t *testing.T) {
	s := newStore(t)
	t0 := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	a := &fake{kind: "sel", fn: func(sink adapter.Sink) error {
		if err := emitModules(sink, bmcModules(t0)); err != nil {
			return err
		}
		return sink.Action(&model.Action{At: t0.Add(time.Hour), Kind: model.ActionDisable, Method: "bios", Label: "DIMM B1",
			Source: "sel/list", RawRef: "sel#12", Fingerprint: "a1"})
	}}
	if _, err := Run(ctx, s, []adapter.Adapter{a}, Options{Host: "h1", Now: t0.Add(2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	acts, err := s.Actions(ctx, store.ActionFilter{Host: "h1"})
	if err != nil || len(acts) != 1 {
		t.Fatalf("actions = %+v, %v", acts, err)
	}
	d, _ := s.DIMM(ctx, acts[0].DIMMUID)
	if acts[0].Slot != "DIMM_B1" || d == nil || d.Serial != "S-B1" {
		t.Errorf("action slot %q, DIMM %+v; want DIMM_B1 and serial S-B1", acts[0].Slot, d)
	}
}

// TestSELWithInventory reads a BMC event log next to the SMBIOS table of
// the same board: the events and actions of the log follow the DIMM in the
// slot they name.
func TestSELWithInventory(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	var b smbios.Builder
	for i, name := range []string{"DIMM_A1", "DIMM_B1", "DIMM_C1", "DIMM_F1"} {
		b.AddMemoryDevice(smbios.MemoryDevice{Handle: uint16(0x1100 + i), Locator: name, SizeMB: 32768, MemoryType: smbios.TypeDDR4,
			FormFactor: smbios.FormDIMM, Manufacturer: "Example Memory", Serial: "SER-" + name[5:], PartNumber: "EXM32G32R2X4-A"})
	}
	adapters := []adapter.Adapter{
		smbios.NewTables(fstest.MapFS{"DMI": {Data: b.Bytes()}}, ".", "/sys/firmware/dmi/tables", smbios.Options{Now: now}),
		sel.NewFile(testutil.Testdata(t, "sel", "ipmitool-elist.txt"), sel.Options{}),
	}
	res, err := Run(ctx, s, adapters, Options{Host: "h1", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	src := res.Sources[1]
	if src.Kind != "sel" || src.Status != model.SourceOK || src.Events != 10 || src.Actions != 2 || src.GradeC != 10 || src.Records != 20 {
		t.Errorf("source inventory entry = %+v", src)
	}

	serialOf := func(uid string) string {
		if uid == "" {
			return ""
		}
		d, err := s.DIMM(ctx, uid)
		if err != nil || d == nil {
			t.Fatalf("dimm %s: %v", uid, err)
		}
		if d.Identity == model.IdentitySlot {
			return "(slot)"
		}
		return d.Serial
	}
	totals := map[string][2]int64{}
	ts, err := s.Totals(ctx, store.EventFilter{Host: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range ts {
		totals[x.Slot+"/"+serialOf(x.DIMMUID)] = [2]int64{x.CE, x.UE}
	}
	want := map[string][2]int64{
		"DIMM_C1/SER-C1": {2, 1},
		"/":              {1, 0}, // the record of the shared sensor
		"DIMM_A1/SER-A1": {1, 0}, // from before the BMC's clock was set
	}
	for k, v := range want {
		if totals[k] != v {
			t.Errorf("totals of %q = %v, want %v (all: %v)", k, totals[k], v, totals)
		}
	}

	acts, err := s.Actions(ctx, store.ActionFilter{Host: "h1"})
	if err != nil || len(acts) != 2 {
		t.Fatalf("actions = %+v, %v", acts, err)
	}
	got := map[model.ActionKind]string{}
	for _, a := range acts {
		got[a.Kind] = a.Slot + "/" + serialOf(a.DIMMUID)
	}
	if got[model.ActionDisable] != "DIMM_C1/SER-C1" || got[model.ActionSparing] != "DIMM_F1/SER-F1" {
		t.Errorf("actions = %v", got)
	}
}
