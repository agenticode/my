package store

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
)

func TestSlotAliases(t *testing.T) {
	s := newStore(t)
	mustUpdate(t, s, func(w *Writer) error {
		for alias, slot := range map[string]string{"edac:1:0:0": "CPU_SrcID#0_MC#1_Chan#0_DIMM#0", "handle:4352": "CPU0_C1"} {
			if err := w.SetSlotAlias("h1", alias, slot); err != nil {
				return err
			}
		}
		// A later observation replaces an earlier one; another host has its own table.
		if err := w.SetSlotAlias("h1", "edac:1:0:0", "CPU0_C1"); err != nil {
			return err
		}
		return w.SetSlotAlias("h2", "edac:1:0:0", "DIMM_A1")
	})
	got, err := s.SlotAliases(ctx, "h1")
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"edac:1:0:0": "CPU0_C1", "handle:4352": "CPU0_C1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("aliases = %v, want %v", got, want)
	}
	if err := s.Update(ctx, func(w *Writer) error { return w.SetSlotAlias("h1", "", "x") }); !errors.Is(err, ErrInvalidRecord) {
		t.Errorf("empty alias: err = %v, want ErrInvalidRecord", err)
	}
}

func TestReplaceSlotDropsWhatWasThere(t *testing.T) {
	s := newStore(t)
	mustUpdate(t, s, func(w *Writer) error {
		return w.UpsertSlot(&model.Slot{Host: "h1", Name: "CPU0_C1", Locator: "CPU0_C1", EdacMC: model.Some(1), EdacLocation: "0:0",
			EdacLabel: "CPU_SrcID#0_MC#1_Chan#0_DIMM#0", Mapping: model.MapBankLocator, IMC: model.Some(1), Populated: model.Some(true)})
	})
	// The mapping no longer holds: the row is rewritten without it.
	mustUpdate(t, s, func(w *Writer) error {
		return w.ReplaceSlot(&model.Slot{Host: "h1", Name: "CPU0_C1", Locator: "CPU0_C1", Populated: model.Some(false)})
	})
	slots, _ := s.Slots(ctx, "h1")
	want := model.Slot{Host: "h1", Name: "CPU0_C1", Locator: "CPU0_C1", Populated: model.Some(false)}
	if len(slots) != 1 || slots[0] != want {
		t.Errorf("slots = %+v, want %+v", slots, want)
	}
}

func TestLatestCounterSamplesAndOccupants(t *testing.T) {
	s := newStore(t)
	mustUpdate(t, s, func(w *Writer) error {
		for _, c := range []*model.CounterSample{
			{ObservedAt: at(0), MC: 0, DIMMIndex: 0, Label: "old-a", CE: 1},
			{ObservedAt: at(0), MC: 0, DIMMIndex: 5, Label: "removed since", CE: 9},
			{ObservedAt: at(time.Hour), MC: 0, DIMMIndex: 0, Location: "0:0", Label: "new-a", CE: 2, SizeMB: model.Some(32768), DevType: "x4"},
			{ObservedAt: at(time.Hour), MC: 1, DIMMIndex: 2, Location: "1:0", Label: "new-b"},
			{ObservedAt: at(time.Hour), MC: 1, DIMMIndex: -1, CE: 7}, // controller-level, not a DIMM
			{ObservedAt: at(2 * time.Hour), MC: 0, DIMMIndex: -1},    // a later reading without DIMM objects
		} {
			if _, err := w.InsertCounterSample("h1", c); err != nil {
				return err
			}
		}
		for _, d := range []model.DIMM{{UID: "dX", Identity: model.IdentitySerial, Serial: "X", SizeBytes: 32 << 30}, {UID: "dY", Identity: model.IdentitySerial, Serial: "Y"}} {
			if err := w.UpsertDIMM(&d); err != nil {
				return err
			}
		}
		if err := w.ObservePlacement("dX", "h1", "A1", at(0), "smbios"); err != nil {
			return err
		}
		if err := w.ObservePlacement("dY", "h1", "A1", at(time.Hour), "smbios"); err != nil {
			return err
		}
		return w.ObservePlacement("dX", "h1", "B1", at(time.Hour), "smbios")
	})

	latest, err := s.LatestCounterSamples(ctx, "h1")
	if err != nil {
		t.Fatal(err)
	}
	// Only the DIMM objects of the most recent reading that had any.
	if len(latest) != 2 || latest[0].Label != "new-a" || latest[1].Label != "new-b" || latest[0].SizeMB != model.Some(32768) ||
		latest[0].DevType != "x4" || !latest[0].ObservedAt.Equal(at(time.Hour)) {
		t.Errorf("latest samples = %+v", latest)
	}
	if none, _ := s.LatestCounterSamples(ctx, "other"); len(none) != 0 {
		t.Errorf("samples of an unknown host: %+v", none)
	}

	occ, err := s.SlotOccupants(ctx, "h1")
	if err != nil {
		t.Fatal(err)
	}
	if len(occ) != 2 || occ["A1"].UID != "dY" || occ["B1"].UID != "dX" || occ["B1"].SizeBytes != 32<<30 {
		t.Errorf("occupants = %+v", occ)
	}
}

func TestPruneOrphans(t *testing.T) {
	s := newStore(t)
	mustUpdate(t, s, func(w *Writer) error {
		for _, sl := range []model.Slot{
			{Host: "h1", Name: "CPU0_C1", Locator: "CPU0_C1"}, // from SMBIOS: stays even when unused
			{Host: "h1", Name: "label with events"},           // referenced by an event
			{Host: "h1", Name: "label with alias"},            // referenced by an alias
			{Host: "h1", Name: "stale label"},                 // nothing refers to it
			{Host: "h2", Name: "stale on another host"},       // not this host
		} {
			if err := w.UpsertSlot(&sl); err != nil {
				return err
			}
		}
		for _, d := range []model.DIMM{
			{UID: model.SlotUID("h1", "label with events"), Identity: model.IdentitySlot},
			{UID: model.SlotUID("h1", "stale label"), Identity: model.IdentitySlot},
			{UID: "serial-unused", Identity: model.IdentitySerial}, // a real identity is never pruned
		} {
			if err := w.UpsertDIMM(&d); err != nil {
				return err
			}
		}
		return w.SetSlotAlias("h1", "edac:0:0:0", "label with alias")
	})
	e := ev("klog/edac", "L1", at(0), 0x1000)
	e.Slot, e.DIMMUID = "label with events", model.SlotUID("h1", "label with events")
	insert(t, s, e)

	if err := s.PruneOrphans(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, host := range []string{"h1", "h2"} {
		slots, _ := s.Slots(ctx, host)
		for _, sl := range slots {
			names = append(names, host+"/"+sl.Name)
		}
	}
	want := []string{"h1/CPU0_C1", "h1/label with alias", "h1/label with events", "h2/stale on another host"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("slots after pruning = %q, want %q", names, want)
	}
	dimms, _ := s.DIMMs(ctx)
	var uids []string
	for _, d := range dimms {
		uids = append(uids, d.UID)
	}
	if len(uids) != 2 {
		t.Errorf("DIMMs after pruning = %q, want the used placeholder and the serial identity", uids)
	}
}

func TestValidationErrorsAreMarked(t *testing.T) {
	s := newStore(t)
	checks := map[string]func(w *Writer) error{
		"event":     func(w *Writer) error { _, err := w.InsertEvent(&model.Event{}); return err },
		"action":    func(w *Writer) error { _, err := w.InsertAction(&model.Action{}); return err },
		"boot":      func(w *Writer) error { _, err := w.UpsertBoot(&model.Boot{}); return err },
		"dimm":      func(w *Writer) error { return w.UpsertDIMM(&model.DIMM{}) },
		"slot":      func(w *Writer) error { return w.UpsertSlot(&model.Slot{}) },
		"placement": func(w *Writer) error { return w.ObservePlacement("", "", "", time.Time{}, "") },
		"host":      func(w *Writer) error { return w.UpsertHost(&model.Host{}) },
		"counter":   func(w *Writer) error { _, err := w.InsertCounterSample("", &model.CounterSample{}); return err },
	}
	for name, fn := range checks {
		if err := s.Update(ctx, fn); !errors.Is(err, ErrInvalidRecord) {
			t.Errorf("%s: err = %v, want ErrInvalidRecord", name, err)
		}
	}
}
