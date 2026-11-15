package ingest

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/smbios"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// The board of these tests: three slots SMBIOS calls A1, A2 and A3, with a
// DIMM in the first two. The BMC calls them DIMM.Socket.A1 and so on, has
// "DIMM A1" as the service label, writes the serial numbers its own way and
// words the maker differently.
var (
	joinBMCTime    = time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	joinSMBIOSTime = time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
)

func boardModule(at time.Time, handle int, locator, serial string) *model.Module {
	m := &model.Module{ObservedAt: at, Locator: locator, Handle: model.Some(handle), Populated: serial != "", Source: smbios.Kind,
		RawRef: "tables#" + locator}
	if serial != "" {
		m.DIMM = model.DIMM{Manufacturer: "Example Memory Inc.", PartNumber: "EXM32G29R2X4-B", Serial: serial, SizeBytes: 32 << 30,
			MemoryType: "DDR4", SpeedMTs: model.Some(2933)}
	}
	return m
}

func bmcModule(at time.Time, locator, serial string, aliases ...string) *model.Module {
	m := &model.Module{ObservedAt: at, Locator: locator, Aliases: aliases, Populated: serial != "", Source: "redfish", RawRef: "dump#" + locator}
	if serial != "" {
		m.DIMM = model.DIMM{Manufacturer: "Example", PartNumber: "EXM32G29R2X4-B", Serial: serial, SizeBytes: 32 << 30,
			Ranks: model.Some(2), SpeedMTs: model.Some(3200), ConfiguredSpeedMTs: model.Some(2666)}
	}
	return m
}

func joinBoard() adapter.Adapter {
	return &fake{kind: smbios.Kind, fn: func(sink adapter.Sink) error {
		return emitModules(sink, []*model.Module{
			boardModule(joinSMBIOSTime, 0x1100, "A1", "D0A10001"),
			boardModule(joinSMBIOSTime, 0x1101, "A2", "D0A20002"),
			boardModule(joinSMBIOSTime, 0x1102, "A3", ""),
		})
	}}
}

func joinBMC() adapter.Adapter {
	return &fake{kind: "redfish", fn: func(sink adapter.Sink) error {
		mods := []*model.Module{
			bmcModule(joinBMCTime, "DIMM.Socket.A1", "0xd0a10001", "DIMM A1"),
			bmcModule(joinBMCTime, "DIMM.Socket.A2", "D0A2-0002", "DIMM A2"),
			bmcModule(joinBMCTime, "DIMM.Socket.A3", "", "DIMM A3"),
		}
		for i, m := range mods {
			m.Socket, m.Channel, m.Position = model.Some(0), model.Some(i), model.Some(0)
		}
		if err := emitModules(sink, mods); err != nil {
			return err
		}
		// The event log of the BMC writes the service label with an
		// underscore, and the BMC counts errors per module.
		if err := sink.Event(&model.Event{ErrorDT: joinBMCTime.Add(-time.Hour), ErrorSeverity: model.SevCE, Source: "redfish/log",
			RawRef: "sel/1", Fingerprint: "sel/1", Label: "DIMM_A1"}); err != nil {
			return err
		}
		return sink.BMCMemory(&model.BMCMemory{ObservedAt: joinBMCTime, Label: "DIMM.Socket.A2", Metrics: "ok", CELifetime: model.Some[int64](3),
			Source: "redfish/memory", RawRef: "dump#metrics", Fingerprint: "metrics A2"})
	}}
}

// ledgerState is what the join decides, in a form two ledgers can be
// compared by.
type ledgerState struct {
	Slots, DIMMs, Placements, Events, Readings []string
	Aliases                                    map[string]string
}

func stateOf(t *testing.T, s *store.Store, host string) ledgerState {
	t.Helper()
	var st ledgerState
	slots, err := s.Slots(ctx, host)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range slots {
		st.Slots = append(st.Slots, fmt.Sprintf("%s populated=%v place=%v/%v/%v", r.Name, r.Populated.Or(false), r.Socket, r.Channel, r.Position))
	}
	dimms, err := s.DIMMs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range dimms {
		st.DIMMs = append(st.DIMMs, fmt.Sprintf("%s %s %q %q ranks=%v speed=%v/%v", d.UID, d.Identity, d.Manufacturer, d.Serial, d.Ranks, d.ConfiguredSpeedMTs, d.SpeedMTs))
	}
	pl, err := s.Placements(ctx, host, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pl {
		st.Placements = append(st.Placements, fmt.Sprintf("%s %s %s..%s %s", p.Slot, p.DIMMUID, p.FirstSeen.Format("01-02T15"), p.LastSeen.Format("01-02T15"), p.Source))
	}
	for e, err := range s.Events(ctx, store.EventFilter{Host: host}) {
		if err != nil {
			t.Fatal(err)
		}
		st.Events = append(st.Events, fmt.Sprintf("%s slot=%s dimm=%s", e.RawRef, e.Slot, e.DIMMUID))
	}
	mems, err := s.BMCMemory(ctx, host)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range mems {
		st.Readings = append(st.Readings, m.Label+" slot="+m.Slot)
	}
	if st.Aliases, err = s.SlotAliases(ctx, host); err != nil {
		t.Fatal(err)
	}
	for _, l := range [][]string{st.Slots, st.DIMMs, st.Placements, st.Events, st.Readings} {
		sort.Strings(l)
	}
	return st
}

func mustRun(t *testing.T, s *store.Store, at time.Time, adapters ...adapter.Adapter) *Result {
	t.Helper()
	res, err := Run(ctx, s, adapters, Options{Host: "node-a", Now: at})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// slotNotes returns the notes of the slot map of a run.
func slotNotes(res *Result) string {
	for _, src := range res.Sources {
		if src.Kind == SlotMapKind {
			return strings.Join(src.Notes, "\n")
		}
	}
	return ""
}

// SMBIOS and the BMC describe one board. The ledger holds each slot and
// each DIMM once, under the name and the identity SMBIOS gives it, and what
// the BMC calls a slot is a name records can use.
func TestInventoriesOfOneBoardAreJoined(t *testing.T) {
	if smbiosSource != smbios.Kind {
		t.Fatalf("the join looks for placements of %q, the SMBIOS adapter records them as %q", smbiosSource, smbios.Kind)
	}
	s := newStore(t)
	res := mustRun(t, s, joinSMBIOSTime, joinBoard(), joinBMC())
	st := stateOf(t, s, "node-a")

	a1 := model.DIMMUID("Example Memory Inc.", "EXM32G29R2X4-B", "D0A10001")
	a2 := model.DIMMUID("Example Memory Inc.", "EXM32G29R2X4-B", "D0A20002")
	want := ledgerState{
		// The numbers of the place come from the BMC, the only source that has them.
		Slots: []string{"A1 populated=true place=0/0/0", "A2 populated=true place=0/1/0", "A3 populated=false place=-/-/-"},
		// SMBIOS keeps its words and its rated speed; the ranks and the
		// speed in use are what the BMC adds.
		DIMMs: []string{
			a1 + ` serial "Example Memory Inc." "D0A10001" ranks=2 speed=2666/2933`,
			a2 + ` serial "Example Memory Inc." "D0A20002" ranks=2 speed=2666/2933`,
		},
		Placements: []string{"A1 " + a1 + " 11-14T09..11-15T09 smbios", "A2 " + a2 + " 11-14T09..11-15T09 smbios"},
		Events:     []string{"sel/1 slot=A1 dimm=" + a1},
		Readings:   []string{"DIMM.Socket.A2 slot=A2"},
	}
	sort.Strings(want.DIMMs)
	st.Aliases = nil
	if !reflect.DeepEqual(st, want) {
		t.Errorf("ledger:\n got %+v\nwant %+v", st, want)
	}
	aliases, _ := s.SlotAliases(ctx, "node-a")
	for alias, slot := range map[string]string{
		"label:A1": "A1", "label:DIMM.SOCKET.A1": "A1", "label:DIMM A1": "A1", "label:DIMM A2": "A2", "place:0:1:0": "A2", "handle:4352": "A1",
	} {
		if aliases[alias] != slot {
			t.Errorf("alias %s = %q, want %q", alias, aliases[alias], slot)
		}
	}
	for alias, slot := range aliases {
		if strings.Contains(alias, "A3") && slot != "A3" || !strings.HasPrefix(slot, "A") {
			t.Errorf("alias %s names %q", alias, slot)
		}
	}
	notes := slotNotes(res)
	for _, want := range []string{
		"2 DIMMs that a second inventory lists are DIMMs SMBIOS describes, by serial number",
		"2 slots have another name in a second inventory",
		"1 empty slots of a second inventory have names SMBIOS does not use",
	} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes lack %q:\n%s", want, notes)
		}
	}
}

// The result is the same whichever inventory arrives first, and reading
// either of them again changes nothing.
func TestJoinDoesNotDependOnTheOrder(t *testing.T) {
	together := newStore(t)
	mustRun(t, together, joinSMBIOSTime, joinBoard(), joinBMC())
	want := stateOf(t, together, "node-a")

	orders := map[string][][]adapter.Adapter{
		"the BMC in the same run, first": {{joinBMC(), joinBoard()}},
		"SMBIOS first":                   {{joinBoard()}, {joinBMC()}},
		"the BMC first":                  {{joinBMC()}, {joinBoard()}},
		"the BMC again":                  {{joinBoard()}, {joinBMC()}, {joinBMC()}},
		"SMBIOS again":                   {{joinBMC()}, {joinBoard()}, {joinBoard()}},
		"errors only, later":             {{joinBMC()}, {joinBoard()}, {&fake{kind: "klog"}}},
	}
	for name, runs := range orders {
		t.Run(name, func(t *testing.T) {
			s := newStore(t)
			for _, adapters := range runs {
				mustRun(t, s, joinSMBIOSTime, adapters...)
			}
			if got := stateOf(t, s, "node-a"); !reflect.DeepEqual(got, want) {
				t.Errorf("ledger:\n got %+v\nwant %+v", got, want)
			}
		})
	}
}

// Without evidence nothing is joined: a BMC alone keeps its names, a DIMM
// SMBIOS does not know keeps its slot, and a DIMM SMBIOS saw in two slots
// says nothing about which of them a slot of the BMC is.
func TestJoinNeedsEvidence(t *testing.T) {
	t.Run("a BMC alone", func(t *testing.T) {
		s := newStore(t)
		res := mustRun(t, s, joinBMCTime, joinBMC())
		st := stateOf(t, s, "node-a")
		if len(st.Slots) != 3 || !strings.HasPrefix(st.Slots[0], "DIMM.Socket.A1 ") || len(st.DIMMs) != 2 {
			t.Errorf("ledger = %+v", st)
		}
		// The event names the slot by the service label, written another way.
		if len(st.Events) != 1 || !strings.Contains(st.Events[0], "slot=DIMM.Socket.A1 ") {
			t.Errorf("events = %v", st.Events)
		}
		if notes := slotNotes(res); notes != "" {
			t.Errorf("notes = %q", notes)
		}
	})

	t.Run("a DIMM SMBIOS does not list", func(t *testing.T) {
		// The BMC lists a module in a fourth slot. SMBIOS has no such slot
		// and no such DIMM.
		s := newStore(t)
		extra := &fake{kind: "redfish", fn: func(sink adapter.Sink) error {
			return emitModules(sink, []*model.Module{
				bmcModule(joinBMCTime, "DIMM.Socket.A1", "D0A10001"),
				bmcModule(joinBMCTime, "DIMM.Socket.B1", "D0B10009"),
			})
		}}
		mustRun(t, s, joinSMBIOSTime, joinBoard(), extra)
		st := stateOf(t, s, "node-a")
		names := strings.Join(st.Slots, "; ")
		if len(st.Slots) != 4 || !strings.Contains(names, "DIMM.Socket.B1 populated=true") || strings.Contains(names, "DIMM.Socket.A1") {
			t.Errorf("slots = %v", st.Slots)
		}
		if len(st.DIMMs) != 3 {
			t.Errorf("DIMMs = %v", st.DIMMs)
		}
	})

	t.Run("a DIMM SMBIOS saw in two slots", func(t *testing.T) {
		s := newStore(t)
		day := 24 * time.Hour
		board := func(at time.Time, a1, a2 string) adapter.Adapter {
			return &fake{kind: smbios.Kind, fn: func(sink adapter.Sink) error {
				return emitModules(sink, []*model.Module{boardModule(at, 0x1100, "A1", a1), boardModule(at, 0x1101, "A2", a2)})
			}}
		}
		// The DIMM was moved from A1 to A2 between two readings of SMBIOS,
		// and the BMC was read in between.
		mustRun(t, s, joinBMCTime.Add(-day), board(joinBMCTime.Add(-day), "D0A10001", ""))
		mustRun(t, s, joinBMCTime.Add(day), board(joinBMCTime.Add(day), "", "D0A10001"))
		res := mustRun(t, s, joinBMCTime, &fake{kind: "redfish", fn: func(sink adapter.Sink) error {
			return emitModules(sink, []*model.Module{bmcModule(joinBMCTime, "DIMM.Socket.A9", "D0A10001")})
		}})
		st := stateOf(t, s, "node-a")
		if len(st.Slots) != 3 || len(st.DIMMs) != 1 || len(st.Placements) != 3 {
			t.Errorf("ledger = %+v", st)
		}
		if notes := slotNotes(res); !strings.Contains(notes, "the slots DIMM.Socket.A9 of a second inventory held DIMMs that SMBIOS saw in more than one slot") {
			t.Errorf("notes = %q", notes)
		}
	})

	t.Run("a serial number SMBIOS has twice", func(t *testing.T) {
		s := newStore(t)
		board := &fake{kind: smbios.Kind, fn: func(sink adapter.Sink) error {
			one, two := boardModule(joinSMBIOSTime, 0x1100, "A1", "D0A10001"), boardModule(joinSMBIOSTime, 0x1101, "A2", "D0A10001")
			two.DIMM.Manufacturer = "Another Maker"
			return emitModules(sink, []*model.Module{one, two})
		}}
		bmc := &fake{kind: "redfish", fn: func(sink adapter.Sink) error {
			return emitModules(sink, []*model.Module{bmcModule(joinBMCTime, "DIMM.Socket.A1", "D0A10001")})
		}}
		mustRun(t, s, joinSMBIOSTime, board, bmc)
		st := stateOf(t, s, "node-a")
		if len(st.Slots) != 3 || len(st.DIMMs) != 3 {
			t.Errorf("ledger = %+v", st)
		}
	})
}

// A BMC that reads no serial numbers saw, in a slot SMBIOS describes, the
// DIMM SMBIOS saw there: the slot does not look as if its DIMM was changed.
// Names that differ in separators only are one slot.
func TestJoinWithoutSerialNumbers(t *testing.T) {
	for _, bmcName := range []string{"A1", "A-1"} {
		t.Run(bmcName, func(t *testing.T) {
			s := newStore(t)
			bmc := &fake{kind: "redfish", fn: func(sink adapter.Sink) error {
				m := bmcModule(joinBMCTime, bmcName, "NO DIMM")
				m.Populated = true
				m.DIMM = model.DIMM{SizeBytes: 32 << 30, Ranks: model.Some(2)}
				return emitModules(sink, []*model.Module{m})
			}}
			mustRun(t, s, joinSMBIOSTime, joinBoard(), bmc)
			st := stateOf(t, s, "node-a")
			a1 := model.DIMMUID("Example Memory Inc.", "EXM32G29R2X4-B", "D0A10001")
			if len(st.Slots) != 3 || len(st.DIMMs) != 2 || len(st.Placements) != 2 {
				t.Fatalf("ledger = %+v", st)
			}
			if st.Placements[0] != "A1 "+a1+" 11-14T09..11-15T09 smbios" {
				t.Errorf("placement of A1 = %q", st.Placements[0])
			}
			if !strings.Contains(strings.Join(st.DIMMs, "\n"), a1+` serial "Example Memory Inc." "D0A10001" ranks=2`) {
				t.Errorf("DIMMs = %v", st.DIMMs)
			}
			if st.Aliases["label:"+bmcName] != "A1" {
				t.Errorf("aliases = %v", st.Aliases)
			}
		})
	}
}

// A second name of a slot does not take a name from another slot: not from
// a slot of the same run, not from one the ledger has from an inventory.
func TestSecondNamesDoNotTakeNames(t *testing.T) {
	s := newStore(t)
	mustRun(t, s, joinSMBIOSTime, joinBoard())
	bmc := &fake{kind: "redfish", fn: func(sink adapter.Sink) error {
		return emitModules(sink, []*model.Module{
			// "A2" is the SMBIOS name of another slot; "Slot" is claimed twice.
			bmcModule(joinBMCTime, "DIMM.Socket.A1", "D0A10001", "A2", "Slot", "DIMM A1"),
			bmcModule(joinBMCTime, "DIMM.Socket.A2", "D0A20002", "Slot"),
		})
	}}
	mustRun(t, s, joinSMBIOSTime, bmc)
	aliases, err := s.SlotAliases(ctx, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	if aliases["label:A2"] != "A2" || aliases["label:DIMM A1"] != "A1" {
		t.Errorf("aliases = %v", aliases)
	}
	if slot, ok := aliases["label:SLOT"]; ok {
		t.Errorf("a name two slots claim names %q", slot)
	}
}

func TestSerialKey(t *testing.T) {
	for in, want := range map[string]string{
		"D0A10001": "D0A10001", " d0a1-0001 ": "D0A10001", "0xd0a10001": "D0A10001", "0XD0A1_0001": "D0A10001",
		"": "", "NO DIMM": "", "00000000": "", "0xFFFFFFFF": "", "Not Specified": "",
	} {
		if got := serialKey(in); got != want {
			t.Errorf("serialKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGaps(t *testing.T) {
	kept := model.DIMM{UID: "d1", Identity: model.IdentitySerial, Manufacturer: "Example Memory Inc.", Ranks: model.Some(1), SizeBytes: 32 << 30}
	other := model.DIMM{UID: "d2", Manufacturer: "Example", PartNumber: "EX-1", Ranks: model.Some(2), DeviceWidth: model.Some(4), SizeBytes: 16 << 30,
		MfgDate: "2025-W10"}
	got, ok := gaps(kept, other)
	want := model.DIMM{UID: "d1", Identity: model.IdentitySerial, PartNumber: "EX-1", DeviceWidth: model.Some(4), MfgDate: "2025-W10"}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("gaps = %+v, %v\n want %+v", got, ok, want)
	}
	if _, ok := gaps(kept, model.DIMM{Manufacturer: "Example"}); ok {
		t.Error("a description that adds nothing is reported as adding something")
	}
}
