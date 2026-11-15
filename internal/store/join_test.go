package store

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
)

// placed lists the placements of a host as "slot dimm first..last source",
// with times in hours after t0.
func placed(t *testing.T, s *Store, host string) []string {
	t.Helper()
	pl, err := s.Placements(ctx, host, "")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range pl {
		out = append(out, fmt.Sprintf("%s %s %d..%d %s", p.Slot, p.DIMMUID, int(p.FirstSeen.Sub(t0).Hours()), int(p.LastSeen.Sub(t0).Hours()), p.Source))
	}
	return out
}

// Two identities of one DIMM become one: the placements are those of the
// identity that is kept and join its own, records name it, and the other
// identity is gone.
func TestAdoptDIMM(t *testing.T) {
	s := newStore(t)
	hour := time.Hour
	mustUpdate(t, s, func(w *Writer) error {
		for _, d := range []*model.DIMM{
			{UID: "d-board", Identity: model.IdentitySerial, Manufacturer: "Example Memory Inc.", Serial: "80A1C3"},
			{UID: "d-bmc", Identity: model.IdentitySerial, Manufacturer: "Example", Serial: "0x80a1c3"},
			{UID: "d-new", Identity: model.IdentitySerial, Serial: "90B2D4"},
		} {
			if err := w.UpsertDIMM(d); err != nil {
				return err
			}
		}
		for _, p := range []struct {
			uid, host, slot string
			at              time.Duration
			source          string
		}{
			{"d-board", "h1", "A1", 10 * hour, "smbios"},
			{"d-board", "h1", "A1", 20 * hour, "smbios"},
			{"d-bmc", "h1", "A1", 5 * hour, "redfish"},  // before SMBIOS saw the DIMM
			{"d-bmc", "h1", "A1", 30 * hour, "redfish"}, // and after
			{"d-new", "h1", "A1", 40 * hour, "smbios"},  // the DIMM was replaced
			{"d-bmc", "h1", "DIMM A1", 15 * hour, "redfish"},
			{"d-bmc", "h2", "A1", 15 * hour, "redfish"}, // another host is left alone
		} {
			if err := w.ObservePlacement(p.uid, p.host, p.slot, at(p.at), p.source); err != nil {
				return err
			}
		}
		return nil
	})
	e := ev("redfish/log", "sel/1", at(12*hour), 0x1000)
	e.DIMMUID = "d-bmc"
	insert(t, s, e)

	dimms, err := s.PlacedDIMMs(ctx, "h1")
	if err != nil || len(dimms) != 3 || dimms["d-bmc"].Manufacturer != "Example" {
		t.Fatalf("placed DIMMs = %+v, %v", dimms, err)
	}
	mustUpdate(t, s, func(w *Writer) error { return w.AdoptDIMM("h1", "d-bmc", "d-board") })

	want := []string{"A1 d-board 5..30 smbios", "A1 d-new 40..40 smbios", "DIMM A1 d-board 15..15 redfish"}
	if got := placed(t, s, "h1"); !reflect.DeepEqual(got, want) {
		t.Errorf("placements = %q\n      want %q", got, want)
	}
	for got, err := range s.Events(ctx, EventFilter{Host: "h1"}) {
		if err != nil || got.DIMMUID != "d-board" {
			t.Errorf("event names DIMM %q, %v", got.DIMMUID, err)
		}
	}
	// The other host still refers to the identity, so it stays.
	if got := placed(t, s, "h2"); !reflect.DeepEqual(got, []string{"A1 d-bmc 15..15 redfish"}) {
		t.Errorf("placements of the other host = %q", got)
	}
	all, _ := s.DIMMs(ctx)
	if len(all) != 3 {
		t.Errorf("%d DIMM identities, want 3", len(all))
	}
	mustUpdate(t, s, func(w *Writer) error { return w.AdoptDIMM("h2", "d-bmc", "d-board") })
	if all, _ = s.DIMMs(ctx); len(all) != 2 {
		t.Errorf("%d DIMM identities after the last record was moved, want 2", len(all))
	}

	if err := s.Update(ctx, func(w *Writer) error { return w.AdoptDIMM("h1", "d-new", "d-new") }); !errors.Is(err, ErrInvalidRecord) {
		t.Errorf("a DIMM adopted by itself: err = %v", err)
	}
}

// A slot two sources named differently becomes one: the names, the
// placements and the records of the first are those of the second.
func TestMoveSlot(t *testing.T) {
	s := newStore(t)
	hour := time.Hour
	mustUpdate(t, s, func(w *Writer) error {
		for _, sl := range []*model.Slot{
			{Host: "h1", Name: "A1", Locator: "A1", SMBIOSHandle: model.Some(0x1100), Populated: model.Some(true)},
			{Host: "h1", Name: "DIMM.Socket.A1", Locator: "DIMM.Socket.A1", Socket: model.Some(0), Channel: model.Some(0), Position: model.Some(0),
				Populated: model.Some(true)},
			{Host: "h2", Name: "DIMM.Socket.A1", Locator: "DIMM.Socket.A1", Populated: model.Some(true)},
		} {
			if err := w.UpsertSlot(sl); err != nil {
				return err
			}
		}
		for alias, slot := range map[string]string{"label:A1": "A1", "label:DIMM.SOCKET.A1": "DIMM.Socket.A1", "place:0:0:0": "DIMM.Socket.A1"} {
			if err := w.SetSlotAlias("h1", alias, slot); err != nil {
				return err
			}
		}
		if err := w.SetSlotAlias("h2", "label:DIMM.SOCKET.A1", "DIMM.Socket.A1"); err != nil {
			return err
		}
		if err := w.UpsertDIMM(&model.DIMM{UID: "d1", Identity: model.IdentitySerial, Serial: "80A1C3"}); err != nil {
			return err
		}
		if err := w.UpsertDIMM(&model.DIMM{UID: "d0", Identity: model.IdentitySerial, Serial: "70F0E1"}); err != nil {
			return err
		}
		for _, p := range []struct {
			uid, slot string
			at        time.Duration
			source    string
		}{
			{"d1", "A1", 10 * hour, "smbios"},
			{"d0", "DIMM.Socket.A1", 1 * hour, "redfish"}, // the DIMM that was there before
			{"d1", "DIMM.Socket.A1", 8 * hour, "redfish"},
			{"d1", "DIMM.Socket.A1", 12 * hour, "redfish"},
		} {
			if err := w.ObservePlacement(p.uid, "h1", p.slot, at(p.at), p.source); err != nil {
				return err
			}
		}
		_, err := w.InsertBMCMemory(&model.BMCMemory{Host: "h1", ObservedAt: at(8 * hour), Label: "DIMM.Socket.A1", Slot: "DIMM.Socket.A1",
			Source: "redfish/memory", RawRef: "m", Fingerprint: "m"})
		return err
	})
	e := ev("redfish/log", "sel/1", at(9*hour), 0x1000)
	e.Label, e.Slot = "DIMM.Socket.A1", "DIMM.Socket.A1"
	insert(t, s, e)

	mustUpdate(t, s, func(w *Writer) error { return w.MoveSlot("h1", "DIMM.Socket.A1", "A1") })

	slots, _ := s.Slots(ctx, "h1")
	if len(slots) != 1 || slots[0].Name != "A1" {
		t.Errorf("slots = %+v", slots)
	}
	aliases, _ := s.SlotAliases(ctx, "h1")
	if want := map[string]string{"label:A1": "A1", "label:DIMM.SOCKET.A1": "A1", "place:0:0:0": "A1"}; !reflect.DeepEqual(aliases, want) {
		t.Errorf("aliases = %v, want %v", aliases, want)
	}
	if got, want := placed(t, s, "h1"), []string{"A1 d0 1..1 redfish", "A1 d1 8..12 smbios"}; !reflect.DeepEqual(got, want) {
		t.Errorf("placements = %q\n      want %q", got, want)
	}
	for got, err := range s.Events(ctx, EventFilter{Host: "h1"}) {
		if err != nil || got.Slot != "A1" {
			t.Errorf("event names slot %q, %v", got.Slot, err)
		}
	}
	if mems, _ := s.BMCMemory(ctx, "h1"); len(mems) != 1 || mems[0].Slot != "A1" || mems[0].Label != "DIMM.Socket.A1" {
		t.Errorf("BMC readings = %+v", mems)
	}
	// The slot of the same name on another host is not touched.
	if other, _ := s.Slots(ctx, "h2"); len(other) != 1 {
		t.Errorf("slots of the other host = %+v", other)
	}
	if other, _ := s.SlotAliases(ctx, "h2"); len(other) != 1 {
		t.Errorf("aliases of the other host = %v", other)
	}

	for _, c := range [][2]string{{"A1", "A1"}, {"A1", "no such slot"}, {"", "A1"}} {
		if err := s.Update(ctx, func(w *Writer) error { return w.MoveSlot("h1", c[0], c[1]) }); !errors.Is(err, ErrInvalidRecord) {
			t.Errorf("MoveSlot(%q, %q): err = %v, want ErrInvalidRecord", c[0], c[1], err)
		}
	}
}

// A slot is dropped with its names when nothing else refers to it.
func TestDropSlot(t *testing.T) {
	s := newStore(t)
	mustUpdate(t, s, func(w *Writer) error {
		for _, name := range []string{"empty", "held", "named in an event"} {
			if err := w.UpsertSlot(&model.Slot{Host: "h1", Name: name, Locator: name, Populated: model.Some(false)}); err != nil {
				return err
			}
			if err := w.SetSlotAlias("h1", "label:"+name, name); err != nil {
				return err
			}
		}
		if err := w.UpsertDIMM(&model.DIMM{UID: "d1", Identity: model.IdentitySerial, Serial: "80A1C3"}); err != nil {
			return err
		}
		return w.ObservePlacement("d1", "h1", "held", at(0), "redfish")
	})
	e := ev("redfish/log", "sel/1", at(time.Hour), 0x1000)
	e.Label, e.Slot = "named in an event", "named in an event"
	insert(t, s, e)

	got := map[string]bool{}
	mustUpdate(t, s, func(w *Writer) error {
		for _, name := range []string{"empty", "held", "named in an event", "no such slot"} {
			ok, err := w.DropSlot("h1", name)
			if err != nil {
				return err
			}
			got[name] = ok
		}
		return nil
	})
	if want := map[string]bool{"empty": true, "held": false, "named in an event": false, "no such slot": false}; !reflect.DeepEqual(got, want) {
		t.Errorf("dropped = %v, want %v", got, want)
	}
	slots, _ := s.Slots(ctx, "h1")
	aliases, _ := s.SlotAliases(ctx, "h1")
	if len(slots) != 2 || len(aliases) != 2 || aliases["label:empty"] != "" {
		t.Errorf("slots = %+v, aliases = %v", slots, aliases)
	}
}

// FillHost adds what is not known about a host and keeps what is.
func TestFillHost(t *testing.T) {
	s := newStore(t)
	mustUpdate(t, s, func(w *Writer) error {
		// A host the kernel log named in one string, and one SMBIOS described.
		if err := w.UpsertHost(&model.Host{Name: "h1", SystemProduct: "Example Systems R650/0PYXKY", FirstSeen: at(time.Hour), LastSeen: at(time.Hour)}); err != nil {
			return err
		}
		if err := w.UpsertHost(&model.Host{Name: "h2", SystemVendor: "Example Systems", SystemProduct: "R760", Arch: "x86_64"}); err != nil {
			return err
		}
		for _, name := range []string{"h1", "h2", "h3"} {
			if err := w.FillHost(&model.Host{Name: name, SystemVendor: "Example", SystemProduct: "EX-2000", Board: "B1", FirstSeen: at(0), LastSeen: at(2 * time.Hour)}); err != nil {
				return err
			}
		}
		return nil
	})
	hosts, err := s.Hosts(ctx)
	if err != nil || len(hosts) != 3 {
		t.Fatalf("hosts = %+v, %v", hosts, err)
	}
	got := map[string]string{}
	for _, h := range hosts {
		got[h.Name] = h.SystemVendor + " | " + h.SystemProduct + " | " + h.Board + " | " + h.Arch
	}
	want := map[string]string{
		// Vendor and product apart are better than one string for both.
		"h1": "Example | EX-2000 | B1 | ",
		"h2": "Example Systems | R760 | B1 | x86_64",
		"h3": "Example | EX-2000 | B1 | ",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("hosts = %v\n  want %v", got, want)
	}
	if !hosts[0].FirstSeen.Equal(at(0)) || !hosts[0].LastSeen.Equal(at(2*time.Hour)) {
		t.Errorf("seen %s to %s", hosts[0].FirstSeen, hosts[0].LastSeen)
	}
	if err := s.Update(ctx, func(w *Writer) error { return w.FillHost(&model.Host{}) }); !errors.Is(err, ErrInvalidRecord) {
		t.Errorf("a host without a name: err = %v", err)
	}
}
