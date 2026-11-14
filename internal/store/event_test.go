package store

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
)

func TestEventRoundTrip(t *testing.T) {
	s := newStore(t)
	mustUpdate(t, s, func(w *Writer) error {
		return w.UpsertDIMM(&model.DIMM{UID: "d1", Identity: model.IdentitySerial})
	})
	in := &model.Event{
		Host: "r12-n034", Kind: model.KindError,
		ErrorDT:       time.Date(2026, 11, 12, 3, 14, 7, 123456000, time.UTC),
		ErrorSeverity: model.SevUE, ErrorScrubType: model.ScrubDemand,
		SocketId: model.Some(0), ChannelId: model.Some(0), ChannelSlotId: model.Some(0), SubChannel: model.Some(1),
		Rank: model.Some(0), DeviceId: model.Some(7), BankGroup: model.Some(0), Bank: model.Some(3),
		Row: model.Some(0x1a2f), Column: model.Some(0), ErrorVector: "0x000c",
		// An address with the top bit set must survive the signed column.
		OsPhysicalAddress: model.Some(uint64(0xfedcba9876543210)),
		Source:            "rasdaemon/mc_event", RawRef: "ras-mc_event.db#mc_event/42", Grade: model.GradeA,
		BootID: "0123456789abcdef0123456789abcdef", DIMMUID: "d1", DIMMLink: model.LinkObserved,
		Count: 1, TimeQuality: model.TimeExact, UTCOffset: model.Some(9 * 3600),
		IMC: model.Some(1), EdacMC: model.Some(1), EdacLocation: "channel:0 slot:0", SMBIOSHandle: model.Some(0x1100),
		Label: "CPU_SrcID#0_MC#1_Chan#0_DIMM#0", Slot: "CPU0_C1",
		Syndrome: model.Some(uint64(0xffffffffffffffff)), ErrorType: "memory read error", Decoder: "edac/skx-adxl",
		Raw: `{"id":42,"label":"CPU_SrcID#0_MC#1_Chan#0_DIMM#0"}`, Fingerprint: "fp-42",
	}
	insert(t, s, in)
	if in.ID == 0 {
		t.Fatal("insert did not set the event ID")
	}

	got, err := s.Event(ctx, in.ID)
	if err != nil || got == nil {
		t.Fatalf("Event(%d) = %v, %v", in.ID, got, err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Errorf("round trip changed the event\n got: %+v\nwant: %+v", got, in)
	}

	// Without WithRaw the raw text is left out, with it it is loaded.
	list, err := s.EventList(ctx, EventFilter{})
	if err != nil || len(list) != 1 || list[0].Raw != "" {
		t.Fatalf("EventList = %+v, %v", list, err)
	}
	list, _ = s.EventList(ctx, EventFilter{WithRaw: true})
	if len(list) != 1 || list[0].Raw != in.Raw {
		t.Errorf("WithRaw did not load the raw record: %+v", list)
	}
	if e, err := s.Event(ctx, 999); e != nil || err != nil {
		t.Errorf("unknown ID = %v, %v; want nil, nil", e, err)
	}
}

func TestUnsetFieldsStayUnset(t *testing.T) {
	s := newStore(t)
	in := &model.Event{
		Host: "h1", ErrorDT: at(0), ErrorSeverity: model.SevCE,
		Row: model.Some(0), Bank: model.Some(0), // reported as zero
		Source: "klog/ghes", RawRef: "L10", Fingerprint: "f1",
	}
	insert(t, s, in)
	got, _ := s.Event(ctx, in.ID)
	if !got.Row.Valid || got.Row.V != 0 || !got.Bank.Valid {
		t.Errorf("reported zeros were lost: row=%v bank=%v", got.Row, got.Bank)
	}
	for name, o := range map[string]model.Opt[int]{
		"SocketId": got.SocketId, "ChannelId": got.ChannelId, "Rank": got.Rank, "DeviceId": got.DeviceId,
		"BankGroup": got.BankGroup, "Column": got.Column, "IMC": got.IMC, "EdacMC": got.EdacMC,
	} {
		if o.Valid {
			t.Errorf("%s was not reported and must stay unset, got %v", name, o)
		}
	}
	if got.OsPhysicalAddress.Valid || got.Syndrome.Valid || got.ErrorScrubType != "" {
		t.Errorf("unset address, syndrome or scrub type came back set: %+v", got)
	}
	// Defaults were filled in.
	if got.Kind != model.KindError || got.Count != 1 || got.TimeQuality != model.TimeExact || got.Grade != model.GradeC {
		t.Errorf("defaults: kind=%s count=%d quality=%s grade=%s", got.Kind, got.Count, got.TimeQuality, got.Grade)
	}
}

func TestInsertEventIsIdempotent(t *testing.T) {
	s := newStore(t)
	a := ev("rasdaemon/mc_event", "mc_event/1", at(0), 0x1000)
	b := ev("rasdaemon/mc_event", "mc_event/1", at(0), 0x1000) // the same record read again
	var first, second bool
	mustUpdate(t, s, func(w *Writer) (err error) {
		if first, err = w.InsertEvent(a); err != nil {
			return err
		}
		second, err = w.InsertEvent(b)
		return err
	})
	if !first || second {
		t.Errorf("inserted = %v, %v; want true, false", first, second)
	}
	if b.ID != a.ID {
		t.Errorf("second insert got ID %d, want the existing %d", b.ID, a.ID)
	}
	if list, _ := s.EventList(ctx, EventFilter{}); len(list) != 1 {
		t.Errorf("ledger holds %d events, want 1", len(list))
	}
}

func TestInsertEventValidation(t *testing.T) {
	s := newStore(t)
	base := func() *model.Event { return ev("klog/edac", "L1", at(0), 0x1000) }
	tests := []struct {
		name string
		mut  func(e *model.Event)
		want string
	}{
		{"no host", func(e *model.Event) { e.Host = "" }, "host is empty"},
		{"no source", func(e *model.Event) { e.Source = "" }, "source is empty"},
		{"no fingerprint", func(e *model.Event) { e.Fingerprint = "" }, "fingerprint is empty"},
		{"zero time", func(e *model.Event) { e.ErrorDT = time.Time{} }, "out of range"},
		{"time before 1970", func(e *model.Event) { e.ErrorDT = time.Date(1969, 12, 31, 0, 0, 0, 0, time.UTC) }, "out of range"},
		{"unknown severity", func(e *model.Event) { e.ErrorSeverity = "corrected" }, "unknown ErrorSeverity"},
		{"unknown scrub type", func(e *model.Event) { e.ErrorScrubType = "patrol" }, "unknown ErrorScrubType"},
		{"unknown grade", func(e *model.Event) { e.Grade = "D" }, "unknown grade"},
		{"negative count", func(e *model.Event) { e.Count = -1 }, "not positive"},
		{"covered above count", func(e *model.Event) { e.Count, e.Covered = 2, 3 }, "outside 0..2"},
		{"unknown kind", func(e *model.Event) { e.Kind = "trend" }, "unknown kind"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := base()
			tt.mut(e)
			err := s.Update(ctx, func(w *Writer) error { _, err := w.InsertEvent(e); return err })
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
	if list, _ := s.EventList(ctx, EventFilter{}); len(list) != 0 {
		t.Errorf("%d invalid events were stored", len(list))
	}
}

func TestEventFilter(t *testing.T) {
	s := newStore(t)
	mustUpdate(t, s, func(w *Writer) error {
		return w.UpsertDIMM(&model.DIMM{UID: "dA", Identity: model.IdentitySerial})
	})
	e1 := ev("rasdaemon/mc_event", "1", at(1*time.Hour), 0x1000)
	e2 := ev("rasdaemon/mc_event", "2", at(2*time.Hour), 0x2000)
	e2.ErrorSeverity, e2.DIMMUID = model.SevUE, "dA"
	e3 := ev("rasdaemon/mc_event", "3", at(3*time.Hour), 0x3000)
	e3.Host, e3.Slot = "h2", "DIMM_B2"
	e4 := &model.Event{Host: "h1", Kind: model.KindCounter, ErrorDT: at(4 * time.Hour), ErrorSeverity: model.SevCE,
		Source: "edac-sysfs/counter", RawRef: "mc0/dimm2", Count: 5, Slot: "CPU0_C1", Fingerprint: "c1"}
	insert(t, s, e1, e2, e3, e4)

	ids := func(f EventFilter) []int64 {
		t.Helper()
		list, err := s.EventList(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		var out []int64
		for _, e := range list {
			out = append(out, e.ID)
		}
		return out
	}
	tests := []struct {
		name string
		f    EventFilter
		want []int64
	}{
		{"all in time order", EventFilter{}, []int64{e1.ID, e2.ID, e3.ID, e4.ID}},
		{"by host", EventFilter{Host: "h2"}, []int64{e3.ID}},
		{"by slot", EventFilter{Host: "h1", Slot: "CPU0_C1"}, []int64{e1.ID, e2.ID, e4.ID}},
		{"by dimm", EventFilter{DIMMUID: "dA"}, []int64{e2.ID}},
		{"from is inclusive, to exclusive", EventFilter{From: at(2 * time.Hour), To: at(4 * time.Hour)}, []int64{e2.ID, e3.ID}},
		{"by severity", EventFilter{Severities: []model.Severity{model.SevUE, model.SevFatal}}, []int64{e2.ID}},
		{"by kind", EventFilter{Kinds: []model.EventKind{model.KindCounter}}, []int64{e4.ID}},
		{"limit", EventFilter{Limit: 2}, []int64{e1.ID, e2.ID}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ids(tt.f); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ids = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEventsIteratorCanStopEarlyAndWriteInside(t *testing.T) {
	s := newStore(t)
	for i := range 20 {
		insert(t, s, ev("klog/edac", "L"+string(rune('a'+i)), at(time.Duration(i)*time.Minute), uint64(0x1000*(i+1))))
	}
	n := 0
	for e, err := range s.Events(ctx, EventFilter{}) {
		if err != nil {
			t.Fatal(err)
		}
		// Writing while a read is open must not deadlock.
		if err := s.SetMeta(ctx, "seen", e.RawRef); err != nil {
			t.Fatalf("write inside iteration: %v", err)
		}
		if n++; n == 3 {
			break
		}
	}
	if n != 3 {
		t.Fatalf("iterated %d events, want 3", n)
	}
	insert(t, s, ev("klog/edac", "after", at(time.Hour), 0x99000))
}

func TestTotalsCountFromLedger(t *testing.T) {
	s := newStore(t)
	mustUpdate(t, s, func(w *Writer) error {
		for _, uid := range []string{"dA", "dB"} {
			if err := w.UpsertDIMM(&model.DIMM{UID: uid, Identity: model.IdentitySerial}); err != nil {
				return err
			}
		}
		return nil
	})
	mk := func(ref string, d time.Duration, sev model.Severity, uid, slot string, count int, grade model.Grade) *model.Event {
		e := ev("rasdaemon/mc_event", ref, at(d), 0x1000)
		e.ErrorSeverity, e.DIMMUID, e.Slot, e.Count, e.Grade = sev, uid, slot, count, grade
		return e
	}
	primary := mk("1", 1*time.Hour, model.SevCE, "dA", "CPU0_C1", 1, model.GradeA)
	insert(t, s,
		primary,
		mk("2", 2*time.Hour, model.SevCE, "dA", "CPU0_C1", 4, model.GradeA), // one record standing for 4 errors
		mk("3", 3*time.Hour, model.SevRecoverable, "dA", "CPU0_C1", 1, model.GradeB),
		mk("4", 4*time.Hour, model.SevFatal, "dA", "CPU0_C1", 1, model.GradeA),
		mk("5", 5*time.Hour, model.SevInformational, "dA", "CPU0_C1", 1, model.GradeC),
		mk("6", 6*time.Hour, model.SevCE, "dB", "CPU1_D1", 2, model.GradeB),
		mk("7", 7*time.Hour, model.SevCE, "", "", 3, model.GradeC), // not attributed
	)
	// A duplicate of the first record, seen through the kernel log.
	dup := ev("klog/edac", "L9", at(1*time.Hour), 0x1000)
	dup.DIMMUID, dup.DupOf = "dA", primary.ID
	// A counter delta of 10 of which 5 are already in the ledger as located records.
	cnt := &model.Event{Host: "h1", Kind: model.KindCounter, ErrorDT: at(8 * time.Hour), ErrorSeverity: model.SevCE,
		Source: "edac-sysfs/counter", RawRef: "mc0/dimm2", Count: 10, Covered: 5, DIMMUID: "dA", Slot: "CPU0_C1", Fingerprint: "c1"}
	insert(t, s, dup, cnt)

	got, err := s.Totals(ctx, EventFilter{Host: "h1", WithDuplicates: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []Totals{
		{Host: "h1", DIMMUID: "", Slot: "", CE: 3, UE: 0, Records: 1, First: at(7 * time.Hour), Last: at(7 * time.Hour), ErrorsC: 3},
		{Host: "h1", DIMMUID: "dA", Slot: "CPU0_C1", CE: 1 + 4 + 5, UE: 2, Records: 5, First: at(1 * time.Hour), Last: at(8 * time.Hour),
			ErrorsA: 1 + 4 + 1, ErrorsB: 1, ErrorsC: 5},
		{Host: "h1", DIMMUID: "dB", Slot: "CPU1_D1", CE: 2, Records: 1, First: at(6 * time.Hour), Last: at(6 * time.Hour), ErrorsB: 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("totals\n got: %+v\nwant: %+v", got, want)
	}

	// The sum over records must equal Event.Errors, the definition used in memory.
	list, _ := s.EventList(ctx, EventFilter{DIMMUID: "dA", WithDuplicates: true})
	sum := 0
	for _, e := range list {
		sum += e.Errors()
	}
	if int64(sum) != want[1].CE+want[1].UE {
		t.Errorf("sum of Event.Errors = %d, totals = %d", sum, want[1].CE+want[1].UE)
	}
}

func TestDaily(t *testing.T) {
	s := newStore(t)
	day := func(d, h int) time.Time { return time.Date(2026, 11, d, h, 0, 0, 0, time.UTC) }
	a := ev("klog/edac", "1", day(1, 10), 0x1000)
	b := ev("klog/edac", "2", day(1, 23), 0x2000) // 08:00 next day at UTC+9
	b.Count = 3
	c := ev("klog/edac", "3", day(2, 12), 0x3000)
	c.ErrorSeverity = model.SevUE
	d := ev("klog/edac", "4", day(4, 0), 0x4000)
	insert(t, s, a, b, c, d)

	got, err := s.Daily(ctx, EventFilter{Host: "h1"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []DayCount{{"2026-11-01", 4, 0}, {"2026-11-02", 0, 1}, {"2026-11-04", 1, 0}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("UTC days = %+v, want %+v", got, want)
	}
	got, err = s.Daily(ctx, EventFilter{Host: "h1"}, 9*3600)
	if err != nil {
		t.Fatal(err)
	}
	want = []DayCount{{"2026-11-01", 1, 0}, {"2026-11-02", 3, 1}, {"2026-11-04", 1, 0}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("UTC+9 days = %+v, want %+v", got, want)
	}
}

func TestUpsertDIMMKeepsKnownAttributes(t *testing.T) {
	s := newStore(t)
	uid := model.DIMMUID("Example Memory", "EXM64G48R2X4-A", "80A1C3")
	mustUpdate(t, s, func(w *Writer) error {
		return w.UpsertDIMM(&model.DIMM{UID: uid, Identity: model.IdentitySerial, Manufacturer: "Example Memory",
			PartNumber: "EXM64G48R2X4-A", Serial: "80A1C3", SizeBytes: 64 << 30, MemoryType: "DDR5", Ranks: model.Some(2),
			FirstSeen: at(24 * time.Hour), LastSeen: at(24 * time.Hour)})
	})
	// A later source knows the device width and nothing else, and saw the DIMM earlier.
	mustUpdate(t, s, func(w *Writer) error {
		return w.UpsertDIMM(&model.DIMM{UID: uid, Identity: model.IdentitySerial, DeviceWidth: model.Some(4),
			FirstSeen: at(0), LastSeen: at(12 * time.Hour)})
	})
	d, err := s.DIMM(ctx, uid)
	if err != nil || d == nil {
		t.Fatalf("DIMM = %v, %v", d, err)
	}
	if d.Manufacturer != "Example Memory" || d.SizeBytes != 64<<30 || d.MemoryType != "DDR5" || d.Organization() != "2Rx4" {
		t.Errorf("attributes were lost or not merged: %+v", d)
	}
	if !d.FirstSeen.Equal(at(0)) || !d.LastSeen.Equal(at(24*time.Hour)) {
		t.Errorf("seen range = %v..%v, want the union", d.FirstSeen, d.LastSeen)
	}
	if missing, _ := s.DIMM(ctx, "nope"); missing != nil {
		t.Error("unknown uid should return nil")
	}
	all, _ := s.DIMMs(ctx)
	if len(all) != 1 {
		t.Errorf("DIMMs() returned %d rows", len(all))
	}
}

func TestSlotsMergeAndOrder(t *testing.T) {
	s := newStore(t)
	mustUpdate(t, s, func(w *Writer) error {
		for _, sl := range []model.Slot{
			{Host: "h1", Name: "CPU1_A1", Locator: "CPU1_A1", Socket: model.Some(1), Channel: model.Some(0), Position: model.Some(0), Populated: model.Some(true)},
			{Host: "h1", Name: "CPU0_B1", Locator: "CPU0_B1", Socket: model.Some(0), Channel: model.Some(1), Position: model.Some(0), Populated: model.Some(false)},
			{Host: "h1", Name: "CPU0_A1", Locator: "CPU0_A1", Socket: model.Some(0), Channel: model.Some(0), Position: model.Some(0), Populated: model.Some(true)},
			{Host: "h1", Name: "unknown memory"},
		} {
			if err := w.UpsertSlot(&sl); err != nil {
				return err
			}
		}
		// EDAC facts about a slot SMBIOS already described.
		return w.UpsertSlot(&model.Slot{Host: "h1", Name: "CPU0_A1", EdacMC: model.Some(0), EdacLocation: "channel:0 slot:0",
			EdacLabel: "CPU_SrcID#0_MC#0_Chan#0_DIMM#0", Mapping: model.MapBankLocator})
	})
	slots, err := s.Slots(ctx, "h1")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, sl := range slots {
		names = append(names, sl.Name)
	}
	if want := []string{"CPU0_A1", "CPU0_B1", "CPU1_A1", "unknown memory"}; !reflect.DeepEqual(names, want) {
		t.Errorf("slot order = %v, want %v", names, want)
	}
	a1 := slots[0]
	if a1.Locator != "CPU0_A1" || !a1.EdacMC.Valid || a1.EdacLabel == "" || a1.Mapping != model.MapBankLocator || !a1.Populated.V {
		t.Errorf("merged slot lost fields: %+v", a1)
	}
	if slots[1].Populated.V || !slots[1].Populated.Valid {
		t.Errorf("empty slot should be recorded as not populated: %+v", slots[1])
	}
}

func TestScanAndSourceInventory(t *testing.T) {
	s := newStore(t)
	sc := &model.Scan{Host: "h1", Kind: model.ScanLive, StartedAt: at(0), ToolVersion: "test", Privileged: false}
	mustUpdate(t, s, func(w *Writer) error {
		if err := w.InsertScan(sc); err != nil {
			return err
		}
		for _, src := range []*model.Source{
			{ScanID: sc.ID, Kind: "rasdaemon", Origin: "/var/lib/rasdaemon/ras-mc_event.db", Status: model.SourceOK,
				Records: 1290, Events: 1285, Skipped: 5, GradeA: 1285, FirstTS: at(0), LastTS: at(time.Hour),
				Notes: []string{"5 rows without a timestamp"}},
			{ScanID: sc.ID, Kind: "smbios", Origin: "/sys/firmware/dmi/tables/DMI", Status: model.SourceUnreadable,
				Detail: "permission denied; readable as root"},
		} {
			if err := w.InsertSource(src); err != nil {
				return err
			}
		}
		return w.FinishScan(sc.ID, at(2*time.Second))
	})
	scans, err := s.Scans(ctx)
	if err != nil || len(scans) != 1 || !scans[0].FinishedAt.Equal(at(2*time.Second)) || scans[0].Kind != model.ScanLive {
		t.Fatalf("scans = %+v, %v", scans, err)
	}
	srcs, err := s.Sources(ctx, sc.ID)
	if err != nil || len(srcs) != 2 {
		t.Fatalf("sources = %+v, %v", srcs, err)
	}
	if srcs[0].BestGrade() != model.GradeA || srcs[0].Skipped != 5 || len(srcs[0].Notes) != 1 {
		t.Errorf("first source = %+v", srcs[0])
	}
	// A source that could not be read is in the inventory with the reason.
	if srcs[1].Status != model.SourceUnreadable || srcs[1].Detail == "" || srcs[1].BestGrade() != "" {
		t.Errorf("unreadable source = %+v", srcs[1])
	}
}

func TestAnalysisRoundTrip(t *testing.T) {
	s := newStore(t)
	mustUpdate(t, s, func(w *Writer) error {
		return w.UpsertDIMM(&model.DIMM{UID: "dA", Identity: model.IdentitySerial})
	})
	e1 := ev("rasdaemon/mc_event", "1", at(0), 0x1000)
	e2 := ev("rasdaemon/mc_event", "2", at(time.Hour), 0x1040)
	insert(t, s, e1, e2)

	f := &model.Fault{Host: "h1", DIMMUID: "dA", Slot: "CPU0_C1", Mode: model.ModeRow, Grade: model.GradeA,
		Rank: model.Some(0), DeviceId: model.Some(7), BankGroup: model.Some(0), Bank: model.Some(3),
		RowMin: model.Some(0x1a2f), RowMax: model.Some(0x1a2f), ColumnMin: model.Some(8), ColumnMax: model.Some(0x3f8),
		AddressMin: model.Some(uint64(0x1000)), AddressMax: model.Some(uint64(0x1040)),
		FirstSeen: at(0), LastSeen: at(time.Hour), EventCount: 2, ErrorCount: 2, Confidence: 90, EngineVersion: "t",
		EventIDs: []int64{e1.ID, e2.ID}}
	v := &model.Verdict{Host: "h1", DIMMUID: "dA", Slot: "CPU0_C1", Grade: model.VerdictReplaceNow,
		Rules:           []string{"R-UE-01", "R-ROW-03"},
		Evidence:        []model.Evidence{{Rule: "R-UE-01", Text: "1 UE", EventIDs: []int64{e2.ID}}},
		CounterEvidence: []string{"no errors on neighbours of the channel"},
		Missing:         []string{"ECS counters"}, NextAction: "attach the case bundle to the RMA",
		Confidence: 85, EngineVersion: "t", ComputedAt: at(2 * time.Hour)}
	mustUpdate(t, s, func(w *Writer) error {
		if err := w.InsertFault(f); err != nil {
			return err
		}
		return w.InsertVerdict(v)
	})

	faults, err := s.Faults(ctx, "h1")
	if err != nil || len(faults) != 1 {
		t.Fatalf("faults = %+v, %v", faults, err)
	}
	if !reflect.DeepEqual(&faults[0], f) {
		t.Errorf("fault round trip\n got: %+v\nwant: %+v", faults[0], *f)
	}
	verdicts, err := s.Verdicts(ctx, "h1")
	if err != nil || len(verdicts) != 1 {
		t.Fatalf("verdicts = %+v, %v", verdicts, err)
	}
	if !reflect.DeepEqual(&verdicts[0], v) {
		t.Errorf("verdict round trip\n got: %+v\nwant: %+v", verdicts[0], *v)
	}

	// A new analysis run replaces the old result.
	mustUpdate(t, s, func(w *Writer) error { return w.DeleteAnalysis("h1") })
	faults, _ = s.Faults(ctx, "h1")
	verdicts, _ = s.Verdicts(ctx, "h1")
	if len(faults) != 0 || len(verdicts) != 0 {
		t.Errorf("after DeleteAnalysis: %d faults, %d verdicts", len(faults), len(verdicts))
	}
	var members int
	s.db.QueryRow(`SELECT count(*) FROM fault_event`).Scan(&members)
	if members != 0 {
		t.Errorf("%d fault_event rows left behind", members)
	}

	bad := *f
	bad.Mode = "rowhammer"
	if err := s.Update(ctx, func(w *Writer) error { return w.InsertFault(&bad) }); err == nil {
		t.Error("unknown fault mode was accepted")
	}
	badV := *v
	badV.Grade = "recommend-replace"
	if err := s.Update(ctx, func(w *Writer) error { return w.InsertVerdict(&badV) }); err == nil {
		t.Error("unknown verdict grade was accepted")
	}
}

func TestGPUMemRoundTrip(t *testing.T) {
	s := newStore(t)
	snap := &model.GPUMem{Host: "h1", ObservedAt: at(0), Kind: model.GPUSnapshot, GPUSerial: "1320000000412", GPUIndex: model.Some(3),
		RemapCorrectable: model.Some(0), RemapUncorrectable: model.Some(2), RemapPending: model.Some(false), RemapFailure: model.Some(false),
		BankMax: model.Some(638), BankHigh: model.Some(2), BankPartial: model.Some(0), BankLow: model.Some(0), BankNone: model.Some(0),
		Source: "nvidia-smi", RawRef: "nvidia-smi -q#gpu3", Fingerprint: "g1"}
	xid := &model.GPUMem{Host: "h1", ObservedAt: at(time.Hour), Kind: model.GPUXID, GPUSerial: "1320000000412", PCIBus: "0000:3b:00.0",
		XID: model.Some(64), XIDText: "Row remapping failure", Source: "klog/xid", RawRef: "L77", Raw: "NVRM: Xid (PCI:0000:3b:00): 64", Fingerprint: "g2"}
	var again bool
	mustUpdate(t, s, func(w *Writer) error {
		for _, g := range []*model.GPUMem{snap, xid} {
			if _, err := w.InsertGPUMem(g); err != nil {
				return err
			}
		}
		var err error
		again, err = w.InsertGPUMem(&model.GPUMem{Host: "h1", ObservedAt: at(0), Kind: model.GPUSnapshot, Source: "nvidia-smi", Fingerprint: "g1"})
		return err
	})
	if again {
		t.Error("a GPU record with a known fingerprint was inserted twice")
	}
	got, err := s.GPUMem(ctx, "h1")
	if err != nil || len(got) != 2 {
		t.Fatalf("gpu_mem = %+v, %v", got, err)
	}
	if !reflect.DeepEqual(&got[0], snap) || !reflect.DeepEqual(&got[1], xid) {
		t.Errorf("gpu_mem round trip\n got: %+v\nwant: %+v %+v", got, *snap, *xid)
	}
	// Zero spare banks is a reported value, different from not reported.
	if !got[0].BankNone.Valid || got[1].BankNone.Valid {
		t.Errorf("bank_none validity: snapshot %v, xid %v", got[0].BankNone, got[1].BankNone)
	}
}

func TestActionsRoundTripAndFilter(t *testing.T) {
	s := newStore(t)
	a1 := &model.Action{Host: "h1", At: at(time.Hour), Kind: model.ActionPageOffline, Method: "soft", Result: model.ResultOK,
		OsPhysicalAddress: model.Some(uint64(0x1f8bd1000)), Source: "klog/offline", RawRef: "L5", Raw: "RAS: Soft-offlining pfn: 0x1f8bd1", Fingerprint: "a1"}
	a2 := &model.Action{Host: "h1", At: at(2 * time.Hour), Kind: model.ActionHPPR, Method: "bios", Result: model.ResultFailed,
		Rank: model.Some(0), DeviceId: model.Some(7), BankGroup: model.Some(0), Bank: model.Some(3), Row: model.Some(0x1a2f),
		Slot: "CPU0_C1", Source: "synth", RawRef: "ppr/1", Fingerprint: "a2"}
	var dupInserted bool
	mustUpdate(t, s, func(w *Writer) error {
		for _, a := range []*model.Action{a1, a2} {
			if _, err := w.InsertAction(a); err != nil {
				return err
			}
		}
		var err error
		dupInserted, err = w.InsertAction(&model.Action{Host: "h1", At: at(time.Hour), Kind: model.ActionPageOffline, Source: "klog/offline", Fingerprint: "a1"})
		return err
	})
	if dupInserted {
		t.Error("an action with a known fingerprint was inserted twice")
	}
	all, err := s.Actions(ctx, ActionFilter{Host: "h1"})
	if err != nil || len(all) != 2 {
		t.Fatalf("actions = %+v, %v", all, err)
	}
	a1.Count, a2.Count = 1, 1 // default filled in on insert
	if !reflect.DeepEqual(&all[0], a1) || !reflect.DeepEqual(&all[1], a2) {
		t.Errorf("action round trip\n got: %+v\nwant: %+v %+v", all, *a1, *a2)
	}
	ppr, _ := s.Actions(ctx, ActionFilter{Kinds: []model.ActionKind{model.ActionHPPR, model.ActionSPPR}})
	if len(ppr) != 1 || ppr[0].ID != a2.ID {
		t.Errorf("filter by kind = %+v", ppr)
	}
	early, _ := s.Actions(ctx, ActionFilter{To: at(90 * time.Minute)})
	if len(early) != 1 || early[0].ID != a1.ID {
		t.Errorf("filter by time = %+v", early)
	}
	if err := s.Update(ctx, func(w *Writer) error {
		_, err := w.InsertAction(&model.Action{Host: "h1", At: at(0), Kind: "offline-dimm", Source: "x", Fingerprint: "a3"})
		return err
	}); err == nil {
		t.Error("unknown action kind was accepted")
	}
}
