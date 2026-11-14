package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestOptJSON(t *testing.T) {
	type rec struct {
		A Opt[int]    `json:"a,omitzero"`
		B Opt[int]    `json:"b,omitzero"`
		C Opt[uint64] `json:"c"`
	}
	in := rec{A: Some(0), C: None[uint64]()}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	// A set zero must be written, an unset value omitted or null.
	if got, want := string(b), `{"a":0,"c":null}`; got != want {
		t.Fatalf("marshal = %s, want %s", got, want)
	}
	var out rec
	if err := json.Unmarshal([]byte(`{"a":0,"b":null,"c":18446744073709551615}`), &out); err != nil {
		t.Fatal(err)
	}
	if !out.A.Valid || out.A.V != 0 {
		t.Errorf("a = %+v, want set 0", out.A)
	}
	if out.B.Valid {
		t.Errorf("b = %+v, want unset", out.B)
	}
	if !out.C.Valid || out.C.V != 1<<64-1 {
		t.Errorf("c = %+v, want set max uint64", out.C)
	}
	if got := None[int]().String(); got != "-" {
		t.Errorf("unset String() = %q, want -", got)
	}
	if got := Some(7).Or(1); got != 7 {
		t.Errorf("Or on set = %d", got)
	}
	if got := None[int]().Or(1); got != 1 {
		t.Errorf("Or on unset = %d", got)
	}
}

func TestComputeGrade(t *testing.T) {
	tests := []struct {
		name string
		e    Event
		want Grade
	}{
		{"bank row column", Event{Bank: Some(3), Row: Some(0x1a2f), Column: Some(0x3f8)}, GradeA},
		{"bank group row column", Event{BankGroup: Some(1), Row: Some(0), Column: Some(0)}, GradeA},
		{"row zero and column zero still count", Event{Bank: Some(0), Row: Some(0), Column: Some(0)}, GradeA},
		{"row and column without bank", Event{Row: Some(5), Column: Some(9), OsPhysicalAddress: Some(uint64(0x1000))}, GradeB},
		{"row without column", Event{Bank: Some(1), Row: Some(5)}, GradeC},
		{"address only", Event{OsPhysicalAddress: Some(uint64(0x7f5d2c9c0))}, GradeB},
		{"rank only", Event{Rank: Some(1)}, GradeB},
		{"nothing", Event{}, GradeC},
		{"counter is always C", Event{Kind: KindCounter, Bank: Some(1), Row: Some(1), Column: Some(1)}, GradeC},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.e.ComputeGrade(); got != tt.want {
				t.Errorf("grade = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestGradeBetter(t *testing.T) {
	if !GradeA.Better(GradeB) || !GradeB.Better(GradeC) || !GradeC.Better("") {
		t.Error("expected A > B > C > empty")
	}
	if GradeC.Better(GradeA) || GradeB.Better(GradeB) {
		t.Error("Better must be strict")
	}
}

func TestSeverity(t *testing.T) {
	tests := []struct {
		s           Severity
		valid       bool
		uncorrected bool
		class       string
	}{
		{SevInformational, true, false, ""},
		{SevCE, true, false, "CE"},
		{SevRecoverable, true, true, "UE"},
		{SevUE, true, true, "UE"},
		{SevFatal, true, true, "UE"},
		{"corrected", false, false, ""},
	}
	for _, tt := range tests {
		if tt.s.Valid() != tt.valid || tt.s.Uncorrected() != tt.uncorrected || tt.s.Class() != tt.class {
			t.Errorf("%q: valid=%v uncorrected=%v class=%q", tt.s, tt.s.Valid(), tt.s.Uncorrected(), tt.s.Class())
		}
	}
}

func TestEventErrors(t *testing.T) {
	tests := []struct {
		name string
		e    Event
		want int
	}{
		{"single corrected error", Event{ErrorSeverity: SevCE, Count: 1}, 1},
		{"batched report", Event{ErrorSeverity: SevCE, Count: 12}, 12},
		{"duplicate adds nothing", Event{ErrorSeverity: SevCE, Count: 1, DupOf: 9}, 0},
		{"partly covered counter", Event{Kind: KindCounter, ErrorSeverity: SevCE, Count: 53, Covered: 50}, 3},
		{"fully covered counter", Event{Kind: KindCounter, ErrorSeverity: SevCE, Count: 53, Covered: 53}, 0},
		{"informational adds nothing", Event{ErrorSeverity: SevInformational, Count: 1}, 0},
	}
	for _, tt := range tests {
		if got := tt.e.Errors(); got != tt.want {
			t.Errorf("%s: Errors() = %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestFingerprint(t *testing.T) {
	if Fingerprint("ab", "c") == Fingerprint("a", "bc") {
		t.Error("fingerprint must separate parts")
	}
	if Fingerprint("x", "y") != Fingerprint("x", "y") {
		t.Error("fingerprint must be deterministic")
	}
	if n := len(Fingerprint("x")); n != 32 {
		t.Errorf("fingerprint length = %d, want 32", n)
	}
}

func TestDIMMUID(t *testing.T) {
	a := DIMMUID("Example Memory", "EXM64G48R2X4-A", "80A1C3")
	// Padding and case differ between sources that describe the same module.
	b := DIMMUID(" example  memory ", "exm64g48r2x4-a   ", "80a1c3")
	if a != b {
		t.Errorf("uid differs after normalization: %s vs %s", a, b)
	}
	if a == DIMMUID("Example Memory", "EXM64G48R2X4-A", "80A1F7") {
		t.Error("different serials must give different uids")
	}
	if !strings.HasPrefix(a, "d") || len(a) != 20 {
		t.Errorf("uid = %q, want d + 19 hex digits", a)
	}
	s := SlotUID("r12-n034", "CPU0_C1")
	if !strings.HasPrefix(s, "s") || len(s) != 20 {
		t.Errorf("slot uid = %q, want s + 19 hex digits", s)
	}
	if s == SlotUID("r12-n035", "CPU0_C1") {
		t.Error("slot uid must depend on the host")
	}
	if s != SlotUID("r12-n034", " cpu0_c1 ") {
		t.Error("slot uid must fold case and padding of the slot name")
	}
}

func TestUsableSerial(t *testing.T) {
	usable := []string{"80A1C3", "0x12345678", "36B2F1A0", "H8N1234567", "00000001", "A0000000"}
	for _, s := range usable {
		if !UsableSerial(s) {
			t.Errorf("UsableSerial(%q) = false, want true", s)
		}
	}
	unusable := []string{
		"", "   ", "NO DIMM", "No DIMM", "Unknown", "Not Specified", "N/A", "None",
		"00000000", "0x00000000", "FFFFFFFF", "ffffffff", "SerNum00", "SerNum3",
		"To Be Filled By O.E.M.", "Default string", "[Empty]", "0123456789",
	}
	for _, s := range unusable {
		if UsableSerial(s) {
			t.Errorf("UsableSerial(%q) = true, want false", s)
		}
	}
}

func TestOrganization(t *testing.T) {
	d := DIMM{Ranks: Some(2), DeviceWidth: Some(4)}
	if got := d.Organization(); got != "2Rx4" {
		t.Errorf("Organization() = %q, want 2Rx4", got)
	}
	if got := (&DIMM{Ranks: Some(2)}).Organization(); got != "" {
		t.Errorf("Organization() without width = %q, want empty", got)
	}
}

func TestVerdictGradeExitCodes(t *testing.T) {
	want := map[VerdictGrade]int{
		VerdictOK: 0, VerdictObserve: 1, VerdictConsiderReplacement: 2, VerdictReplaceNow: 3, VerdictHoldReplacement: 4,
	}
	for g, code := range want {
		if !g.Valid() {
			t.Errorf("%q should be valid", g)
		}
		if got := g.ExitCode(); got != code {
			t.Errorf("%q exit code = %d, want %d", g, got, code)
		}
	}
	if VerdictGrade("recommend").Valid() {
		t.Error("unknown grade must not be valid")
	}
}

func TestFaultModes(t *testing.T) {
	if len(FaultModes) != 8 {
		t.Fatalf("Level 1 has 8 modes, got %d", len(FaultModes))
	}
	for _, m := range append([]FaultMode{ModeUndetermined}, FaultModes...) {
		if !m.Valid() {
			t.Errorf("%q should be valid", m)
		}
	}
	if FaultMode("rowhammer").Valid() {
		t.Error("unknown mode must not be valid")
	}
}

func TestEventJSONUsesFMFMNames(t *testing.T) {
	e := Event{
		Host: "r12-n034", Kind: KindError,
		ErrorDT:       time.Date(2026, 11, 12, 3, 14, 7, 0, time.UTC),
		ErrorSeverity: SevUE, ErrorScrubType: ScrubDemand,
		SocketId: Some(0), ChannelId: Some(2), ChannelSlotId: Some(0),
		Rank: Some(0), DeviceId: Some(7), BankGroup: Some(0), Bank: Some(3),
		Row: Some(0x1a2f), Column: Some(0x3f8), ErrorVector: "0x000c",
		OsPhysicalAddress: Some(uint64(0x1f8bd19c0)),
		Source:            "rasdaemon/mc_event", RawRef: "mc_event/42", Grade: GradeA,
		Count: 1, TimeQuality: TimeExact, Fingerprint: "f",
	}
	b, err := json.Marshal(&e)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{
		"HostID", "ErrorDT", "ErrorSeverity", "ErrorScrubType", "SocketId", "ChannelId", "ChannelSlotId",
		"Rank", "DeviceId", "BankGroup", "Bank", "Row", "Column", "ErrorVector", "OsPhysicalAddress",
		"source", "raw_ref", "grade",
	} {
		if _, ok := m[k]; !ok {
			t.Errorf("JSON lacks field %q: %s", k, b)
		}
	}
	for _, k := range []string{"SubChannel", "boot_id", "dimm_uid", "dup_of", "syndrome"} {
		if _, ok := m[k]; ok {
			t.Errorf("JSON should omit unset field %q: %s", k, b)
		}
	}
	var back Event
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Row != e.Row || back.OsPhysicalAddress != e.OsPhysicalAddress || !back.ErrorDT.Equal(e.ErrorDT) {
		t.Errorf("round trip changed the event: %+v", back)
	}
}

func TestDerivedBootID(t *testing.T) {
	t0 := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	a := DerivedBootID("h", t0)
	if a != DerivedBootID("h", t0.Add(300*time.Millisecond)) {
		t.Error("derived boot id should not depend on sub-second precision")
	}
	if a == DerivedBootID("h", t0.Add(time.Second)) || a == DerivedBootID("g", t0) {
		t.Error("derived boot id must depend on host and start second")
	}
	if !strings.HasPrefix(a, "derived-") {
		t.Errorf("derived boot id = %q", a)
	}
}
