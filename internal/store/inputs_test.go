package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
)

func TestGPUMemECCColumns(t *testing.T) {
	s := newStore(t)
	snap := &model.GPUMem{Host: "h1", ObservedAt: at(0), Kind: model.GPUSnapshot, GPUSerial: "1650000000977", GPUUUID: "GPU-5", GPUIndex: model.Some(5),
		PCIBus: "0000:86:00.0", Model: "Example GPU 80GB", ECCEnabled: model.Some(true),
		DRAMCorrectable: model.Some(12), DRAMUncorrectable: model.Some(1), DRAMCorrectableVolatile: model.Some(0), DRAMUncorrectableVolatile: model.Some(1),
		SRAMCorrectable: model.Some(0), SRAMUncorrectable: model.Some(3), SRAMCorrectableVolatile: model.Some(0), SRAMUncorrectableVolatile: model.Some(0),
		SRAMThresholdExceeded: model.Some(false), RemapPending: model.Some(true), RemapFailure: model.Some(true),
		Source: "nvidia-smi", RawRef: "smi.xml#gpu5", Raw: "<gpu/>", Fingerprint: "g5", ScanID: 0}
	mustUpdate(t, s, func(w *Writer) error {
		_, err := w.InsertGPUMem(snap)
		return err
	})
	got, err := s.GPUMem(ctx, "h1")
	if err != nil || len(got) != 1 {
		t.Fatalf("gpu_mem = %+v, %v", got, err)
	}
	if !reflect.DeepEqual(&got[0], snap) {
		t.Errorf("round trip\n got: %+v\nwant: %+v", got[0], *snap)
	}
	// A count of zero is a reported value; a GPU without ECC reports none.
	if !got[0].SRAMCorrectable.Valid || got[0].RetiredSBE.Valid {
		t.Errorf("validity: sram_correctable %v, retired_sbe %v", got[0].SRAMCorrectable, got[0].RetiredSBE)
	}
}

func TestGPUMemValidation(t *testing.T) {
	s := newStore(t)
	base := func() *model.GPUMem {
		return &model.GPUMem{Host: "h1", ObservedAt: at(0), Kind: model.GPUXID, XID: model.Some(48), Source: "klog/xid", RawRef: "L1", Fingerprint: "x1"}
	}
	for name, change := range map[string]func(*model.GPUMem){
		"no host":          func(g *model.GPUMem) { g.Host = "" },
		"unknown kind":     func(g *model.GPUMem) { g.Kind = "other" },
		"no fingerprint":   func(g *model.GPUMem) { g.Fingerprint = "" },
		"no time":          func(g *model.GPUMem) { g.ObservedAt = time.Time{} },
		"bad time quality": func(g *model.GPUMem) { g.TimeQuality = "guessed" },
		"xid without code": func(g *model.GPUMem) { g.XID = model.Opt[int]{} },
	} {
		g := base()
		change(g)
		err := s.Update(ctx, func(w *Writer) error {
			_, err := w.InsertGPUMem(g)
			return err
		})
		if !errors.Is(err, ErrInvalidRecord) {
			t.Errorf("%s: err = %v, want ErrInvalidRecord", name, err)
		}
	}
	// A known fingerprint reports the row that is there.
	first, second := base(), base()
	mustUpdate(t, s, func(w *Writer) error {
		if ok, err := w.InsertGPUMem(first); err != nil || !ok {
			t.Errorf("first insert = %v, %v", ok, err)
		}
		ok, err := w.InsertGPUMem(second)
		if ok || second.ID != first.ID || first.ID == 0 {
			t.Errorf("second insert = %v, id %d; want false and id %d", ok, second.ID, first.ID)
		}
		return err
	})
}

// TestCompleteGPUs checks how records that name their GPU by a PCI address
// take the identity other records of the host know.
func TestCompleteGPUs(t *testing.T) {
	const bus = "0000:3b:00.0"
	snap := func(d time.Duration, serial, uuid string) model.GPUMem {
		return model.GPUMem{Kind: model.GPUSnapshot, ObservedAt: at(d), TimeQuality: model.TimeExact, GPUSerial: serial, GPUUUID: uuid,
			PCIBus: bus, GPUIndex: model.Some(3), Model: "Example GPU"}
	}
	xid := func(d time.Duration, q model.TimeQuality, boot string) model.GPUMem {
		return model.GPUMem{Kind: model.GPUXID, ObservedAt: at(d), TimeQuality: q, PCIBus: bus, XID: model.Some(48), BootID: boot}
	}
	day := 24 * time.Hour

	t.Run("nearest in time", func(t *testing.T) {
		// The GPU in the slot was replaced between the two snapshots.
		recs := []model.GPUMem{snap(0, "S-OLD", "GPU-old"), xid(2*day, model.TimeExact, ""), xid(8*day, model.TimeExact, ""), snap(10*day, "S-NEW", "GPU-new")}
		completeGPUs(recs)
		if recs[1].GPUSerial != "S-OLD" || recs[1].GPUUUID != "GPU-old" || recs[2].GPUSerial != "S-NEW" || recs[2].GPUUUID != "GPU-new" {
			t.Errorf("serials = %q, %q; want S-OLD, S-NEW", recs[1].GPUSerial, recs[2].GPUSerial)
		}
		if recs[1].GPUIndex != model.Some(3) || recs[1].Model != "Example GPU" {
			t.Errorf("index and model were not completed: %+v", recs[1])
		}
	})
	t.Run("same boot wins over nearer time", func(t *testing.T) {
		near := snap(day+time.Minute, "S-NEAR", "GPU-near")
		same := xid(0, model.TimeExact, "boot-1")
		same.GPUUUID = "GPU-boot" // the driver printed the UUID once in this boot
		recs := []model.GPUMem{same, xid(day, model.TimeExact, "boot-1"), near}
		completeGPUs(recs)
		if recs[1].GPUUUID != "GPU-boot" {
			t.Errorf("uuid = %q, want GPU-boot", recs[1].GPUUUID)
		}
	})
	t.Run("relative time needs the same boot", func(t *testing.T) {
		rel := xid(0, model.TimeRelative, "derived-1")
		rel.ObservedAt = time.Unix(120, 0).UTC()
		recs := []model.GPUMem{snap(0, "S1", "GPU-1"), rel}
		completeGPUs(recs)
		if recs[1].GPUSerial != "" || recs[1].GPUUUID != "" {
			t.Errorf("a record without a date took an identity: %+v", recs[1])
		}
	})
	t.Run("serial follows the uuid", func(t *testing.T) {
		other := model.GPUMem{Kind: model.GPUXID, ObservedAt: at(0), TimeQuality: model.TimeExact, GPUUUID: "GPU-1", PCIBus: "0000:af:00.0", XID: model.Some(63)}
		recs := []model.GPUMem{other, snap(day, "S1", "GPU-1")}
		completeGPUs(recs)
		if recs[0].GPUSerial != "S1" || recs[0].PCIBus != "0000:af:00.0" {
			t.Errorf("record = %+v", recs[0])
		}
	})
	t.Run("nothing to go by", func(t *testing.T) {
		recs := []model.GPUMem{{Kind: model.GPUXID, ObservedAt: at(0), XID: model.Some(48)}, snap(0, "S1", "GPU-1")}
		completeGPUs(recs)
		if recs[0].GPUSerial != "" {
			t.Errorf("a record without any name took a serial: %+v", recs[0])
		}
	})
}

func ecs(ref string, when time.Time, label string) *model.ECSSample {
	return &model.ECSSample{Host: "h1", ObservedAt: when, Label: label, MR14: model.Some(0x20), MR15: model.Some(0x03), MR20: model.Some(0x01),
		Source: "fmfm/runtime", RawRef: ref, Fingerprint: model.Fingerprint("ecs", ref)}
}

func TestECSRoundTrip(t *testing.T) {
	s := newStore(t)
	mustUpdate(t, s, func(w *Writer) error {
		return w.UpsertDIMM(&model.DIMM{UID: "d1", Identity: model.IdentitySerial})
	})
	in := &model.ECSSample{Host: "h1", ObservedAt: at(time.Hour), TimeQuality: model.TimeEstimated,
		SocketId: model.Some(0), ChannelId: model.Some(2), ChannelSlotId: model.Some(0), SubChannel: model.Some(1), Rank: model.Some(0), DeviceId: model.Some(7),
		Label: "CPU0_C1", Slot: "CPU0_C1", DIMMUID: "d1",
		MR14: model.Some(0xA0), MR15: model.Some(0x03), MR16: model.Some(0x2F), MR17: model.Some(0x1A), MR18: model.Some(0x0C), MR19: model.Some(0), MR20: model.Some(0x02),
		BootID: "boot-1", Source: "fmfm/runtime", RawRef: "run.json#3", Raw: `{"EcsModeRegisters":"a0032f1a0c0002"}`, Fingerprint: "c1"}
	other := ecs("run.json#4", at(2*time.Hour), "CPU0_D1")
	other.Slot = "CPU0_D1"
	var again bool
	mustUpdate(t, s, func(w *Writer) error {
		for _, e := range []*model.ECSSample{in, other} {
			if ok, err := w.InsertECS(e); err != nil || !ok {
				t.Errorf("insert %s = %v, %v", e.RawRef, ok, err)
			}
		}
		dup := *in
		dup.ID = 0
		var err error
		again, err = w.InsertECS(&dup)
		if dup.ID != in.ID {
			t.Errorf("known reading: id %d, want %d", dup.ID, in.ID)
		}
		return err
	})
	if again {
		t.Error("a reading with a known fingerprint was inserted twice")
	}
	all, err := s.ECS(ctx, ECSFilter{Host: "h1"})
	if err != nil || len(all) != 2 {
		t.Fatalf("readings = %+v, %v", all, err)
	}
	if !reflect.DeepEqual(&all[0], in) {
		t.Errorf("round trip\n got: %+v\nwant: %+v", all[0], *in)
	}
	// A register that reads zero was read; one that was not read is unset.
	if !all[0].MR19.Valid || all[1].MR19.Valid || all[1].TimeQuality != model.TimeExact {
		t.Errorf("mr19 validity %v, %v; time quality %q", all[0].MR19, all[1].MR19, all[1].TimeQuality)
	}
	for name, f := range map[string]ECSFilter{"slot": {Slot: "CPU0_D1"}, "dimm": {DIMMUID: "d1"}, "host and slot": {Host: "h1", Slot: "CPU0_C1"}} {
		if got, err := s.ECS(ctx, f); err != nil || len(got) != 1 {
			t.Errorf("filter by %s = %d readings, %v; want 1", name, len(got), err)
		}
	}
	if none, err := s.ECS(ctx, ECSFilter{Host: "h9"}); err != nil || len(none) != 0 {
		t.Errorf("readings of an unknown host = %+v, %v", none, err)
	}
}

func TestECSValidation(t *testing.T) {
	s := newStore(t)
	for name, change := range map[string]func(*model.ECSSample){
		"no host":          func(e *model.ECSSample) { e.Host = "" },
		"no source":        func(e *model.ECSSample) { e.Source = "" },
		"no fingerprint":   func(e *model.ECSSample) { e.Fingerprint = "" },
		"no time":          func(e *model.ECSSample) { e.ObservedAt = time.Time{} },
		"year 10000":       func(e *model.ECSSample) { e.ObservedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) },
		"bad time quality": func(e *model.ECSSample) { e.TimeQuality = "guessed" },
		"no register": func(e *model.ECSSample) {
			e.MR14, e.MR15, e.MR20 = model.Opt[int]{}, model.Opt[int]{}, model.Opt[int]{}
		},
		"register 256": func(e *model.ECSSample) { e.MR16 = model.Some(256) },
		"register -1":  func(e *model.ECSSample) { e.MR19 = model.Some(-1) },
	} {
		e := ecs("r1", at(0), "CPU0_C1")
		change(e)
		err := s.Update(ctx, func(w *Writer) error {
			_, err := w.InsertECS(e)
			return err
		})
		if !errors.Is(err, ErrInvalidRecord) {
			t.Errorf("%s: err = %v, want ErrInvalidRecord", name, err)
		}
	}
}

// TestLinkInputs checks that GPU memory records and ECS readings take their
// boot from their time, and that ECS readings follow the DIMM of their slot.
func TestLinkInputs(t *testing.T) {
	s := newStore(t)
	day := 24 * time.Hour
	rel := &model.GPUMem{Host: "h1", ObservedAt: time.Unix(300, 0).UTC(), TimeQuality: model.TimeRelative, Kind: model.GPUXID, XID: model.Some(48),
		BootID: "derived-x", Source: "klog/xid", RawRef: "L9", Fingerprint: "x-rel"}
	mustUpdate(t, s, func(w *Writer) error {
		for i, start := range []time.Duration{0, 10 * day} {
			if _, err := w.UpsertBoot(&model.Boot{Host: "h1", BootID: []string{"boot-a", "boot-b"}[i], StartedAt: at(start)}); err != nil {
				return err
			}
		}
		for _, g := range []*model.GPUMem{
			{Host: "h1", ObservedAt: at(day), Kind: model.GPUXID, XID: model.Some(63), Source: "klog/xid", RawRef: "L1", Fingerprint: "x1"},
			{Host: "h1", ObservedAt: at(11 * day), Kind: model.GPUSnapshot, Source: "nvidia-smi", RawRef: "smi#0", Fingerprint: "s1"},
			{Host: "h1", ObservedAt: at(-day), Kind: model.GPUSnapshot, Source: "nvidia-smi", RawRef: "smi#old", Fingerprint: "s0", BootID: "stale"},
			rel,
		} {
			if _, err := w.InsertGPUMem(g); err != nil {
				return err
			}
		}
		// The DIMM in CPU0_C1 was replaced at the second boot.
		for _, d := range []struct {
			uid string
			at  time.Duration
		}{{"dOLD", day}, {"dNEW", 12 * day}} {
			if err := w.UpsertDIMM(&model.DIMM{UID: d.uid, Identity: model.IdentitySerial}); err != nil {
				return err
			}
			if err := w.ObservePlacement(d.uid, "h1", "CPU0_C1", at(d.at), "smbios"); err != nil {
				return err
			}
		}
		for i, d := range []time.Duration{2 * day, 9 * day, 13 * day} {
			e := ecs([]string{"r1", "r2", "r3"}[i], at(d), "CPU0_C1")
			e.Slot = "CPU0_C1"
			if _, err := w.InsertECS(e); err != nil {
				return err
			}
		}
		noSlot := ecs("r4", at(3*day), "")
		noSlot.DIMMUID = "dOLD"
		_, err := w.InsertECS(noSlot)
		return err
	})
	if err := s.LinkBoots(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	if err := s.LinkDIMMs(ctx, "h1"); err != nil {
		t.Fatal(err)
	}

	gpus, err := s.GPUMem(ctx, "h1")
	if err != nil {
		t.Fatal(err)
	}
	boots := map[string]string{}
	for _, g := range gpus {
		boots[g.RawRef] = g.BootID
	}
	want := map[string]string{"L1": "boot-a", "smi#0": "boot-b", "smi#old": "", "L9": "derived-x"}
	if !reflect.DeepEqual(boots, want) {
		t.Errorf("boots of GPU records = %v, want %v", boots, want)
	}

	readings, err := s.ECS(ctx, ECSFilter{Host: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][2]string{}
	for _, e := range readings {
		got[e.RawRef] = [2]string{e.DIMMUID, e.BootID}
	}
	wantECS := map[string][2]string{
		"r1": {"dOLD", "boot-a"}, // inside the first placement
		"r2": {"dOLD", "boot-a"}, // after it, before the boot at which the DIMM changed
		"r3": {"dNEW", "boot-b"},
		"r4": {"", "boot-a"}, // a reading without a slot has no DIMM
	}
	if !reflect.DeepEqual(got, wantECS) {
		t.Errorf("DIMM and boot of ECS readings = %v\nwant %v", got, wantECS)
	}
}

// TestECSKeepsSlotAndPlaceholder checks that a slot only ECS readings name,
// and the placeholder identity that stands for its DIMM, are not pruned.
func TestECSKeepsSlotAndPlaceholder(t *testing.T) {
	s := newStore(t)
	mustUpdate(t, s, func(w *Writer) error {
		if err := w.UpsertSlot(&model.Slot{Host: "h1", Name: "DIMM_B2"}); err != nil {
			return err
		}
		if err := w.UpsertSlot(&model.Slot{Host: "h1", Name: "unused"}); err != nil {
			return err
		}
		e := ecs("r1", at(0), "DIMM_B2")
		e.Slot = "DIMM_B2"
		_, err := w.InsertECS(e)
		return err
	})
	if err := s.LinkDIMMs(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	if err := s.PruneOrphans(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	slots, _ := s.Slots(ctx, "h1")
	if len(slots) != 1 || slots[0].Name != "DIMM_B2" {
		t.Errorf("slots after pruning = %+v, want DIMM_B2 only", slots)
	}
	readings, _ := s.ECS(ctx, ECSFilter{Slot: "DIMM_B2"})
	if len(readings) != 1 || readings[0].DIMMUID != model.SlotUID("h1", "DIMM_B2") {
		t.Fatalf("reading = %+v, want the placeholder identity of the slot", readings)
	}
	if d, err := s.DIMM(ctx, readings[0].DIMMUID); err != nil || d == nil || d.Identity != model.IdentitySlot {
		t.Errorf("placeholder DIMM = %+v, %v", d, err)
	}
}

func TestECSLocations(t *testing.T) {
	s := newStore(t)
	mustUpdate(t, s, func(w *Writer) error {
		for i := range 3 {
			e := ecs([]string{"a", "b", "c"}[i], at(time.Duration(i)*time.Hour), "")
			e.SocketId, e.ChannelId, e.ChannelSlotId = model.Some(0), model.Some(2), model.Some(0)
			if _, err := w.InsertECS(e); err != nil {
				return err
			}
		}
		_, err := w.InsertECS(ecs("d", at(0), "DIMM_A1"))
		return err
	})
	keys, err := s.ECSLocations(ctx, "h1")
	if err != nil || len(keys) != 2 {
		t.Fatalf("keys = %+v, %v", keys, err)
	}
	byLabel := map[string]LocationKey{}
	for _, k := range keys {
		byLabel[k.Label] = k
	}
	if k := byLabel[""]; k.Records != 3 || k.SocketId != model.Some(0) || k.ChannelId != model.Some(2) || k.ChannelSlotId != model.Some(0) {
		t.Errorf("numbered key = %+v", k)
	}
	mustUpdate(t, s, func(w *Writer) error {
		if err := w.SetECSSlot("h1", byLabel[""], "CPU0_C1"); err != nil {
			return err
		}
		return w.SetECSSlot("h1", byLabel["DIMM_A1"], "DIMM_A1")
	})
	for slot, n := range map[string]int{"CPU0_C1": 3, "DIMM_A1": 1} {
		if got, _ := s.ECS(ctx, ECSFilter{Slot: slot}); len(got) != n {
			t.Errorf("slot %s has %d readings, want %d", slot, len(got), n)
		}
	}
}

// TestMigrateVersion2 opens a ledger written with schema version 2 and
// checks that its GPU records and source inventory read back with the
// columns version 3 added.
func TestMigrateVersion2(t *testing.T) {
	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "v2.db")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		migs[0].sql,
		migs[1].sql,
		`PRAGMA application_id = 1297697863`,
		`PRAGMA user_version = 2`,
		`INSERT INTO scan (id, host, kind, started_at) VALUES (1, 'h1', 'import', '2026-11-01T00:00:00.000000Z')`,
		`INSERT INTO source (scan_id, kind, origin, status, records, events) VALUES (1, 'klog', 'dmesg.txt', 'ok', 12, 3)`,
		`INSERT INTO gpu_mem (host, observed_at, kind, gpu_serial, xid, xid_text, source, raw_ref, fingerprint)
		 VALUES ('h1', '2026-11-01T03:00:00.000000Z', 'xid', '1320000000412', 64, 'row remap failure', 'synth', 'x/1', 'g1')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("build version 2 ledger: %v\n%s", err, q)
		}
	}
	db.Close()

	if _, err := Open(ctx, p, ReadOnly); !errors.Is(err, ErrNeedsMigration) {
		t.Fatalf("read-only open of a version 2 ledger: err = %v, want ErrNeedsMigration", err)
	}
	s, err := Open(ctx, p, ReadWrite)
	if err != nil {
		t.Fatalf("open for writing: %v", err)
	}
	defer s.Close()
	if v, _ := s.Version(ctx); v != SchemaVersion() || v < 3 {
		t.Fatalf("version after migration = %d", v)
	}
	gpus, err := s.GPUMem(ctx, "h1")
	want := model.GPUMem{ID: 1, Host: "h1", ObservedAt: time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC), Kind: model.GPUXID, GPUSerial: "1320000000412",
		XID: model.Some(64), XIDText: "row remap failure", TimeQuality: model.TimeExact, Source: "synth", RawRef: "x/1", Fingerprint: "g1"}
	if err != nil || len(gpus) != 1 || !reflect.DeepEqual(gpus[0], want) {
		t.Errorf("GPU records after migration = %+v, %v\nwant %+v", gpus, err, want)
	}
	srcs, err := s.Sources(ctx, 1)
	if err != nil || len(srcs) != 1 || srcs[0].Samples != 0 || srcs[0].Records != 12 || srcs[0].Events != 3 {
		t.Errorf("source inventory after migration = %+v, %v", srcs, err)
	}
	mustUpdate(t, s, func(w *Writer) error {
		if err := w.InsertSource(&model.Source{ScanID: 1, Kind: "nvidia-smi", Origin: "smi.xml", Samples: 8}); err != nil {
			return err
		}
		_, err := w.InsertECS(ecs("r1", at(0), "CPU0_C1"))
		return err
	})
	srcs, _ = s.Sources(ctx, 1)
	if len(srcs) != 2 || srcs[1].Samples != 8 {
		t.Errorf("source inventory = %+v, want 8 samples on the second entry", srcs)
	}
}
