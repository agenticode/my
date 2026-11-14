package edac

import (
	"testing"

	"github.com/agenticode/my/internal/model"
)

func some(v int) model.Opt[int] { return model.Some(v) }

var none = model.Opt[int]{}

func TestParseDetail(t *testing.T) {
	tests := []struct {
		name   string
		detail string
		want   Detail
	}{
		{
			name: "ADXL decode (skx_edac, i10nm_edac with ACPI address translation)",
			detail: " err_code:0x0101:0x0091  SystemAddress:0x1f8bd19c0 ProcessorSocketId:0x0 MemoryControllerId:0x1 " +
				"ChannelAddress:0x1b8bd19c0 ChannelId:0x0 RankAddress:0xdc5e8cc0 PhysicalRankId:0x1 DimmSlotId:0x0 " +
				"DimmRankId:0x1 Row:0x1a2f Column:0x3f8 Bank:0x3 BankGroup:0x1 ChipSelect:0x1 ChipId:0x0",
			want: Detail{
				Decoder: DecoderADXL, Socket: some(0), IMC: some(1), Channel: some(0), Slot: some(0), Rank: some(1),
				BankGroup: some(1), Bank: some(3), Row: some(0x1a2f), Column: some(0x3f8),
				SystemAddress: model.Some(uint64(0x1f8bd19c0)), MSCOD: some(0x0101), MCACOD: some(0x0091),
			},
		},
		{
			name: "ADXL decode without DimmRankId keeps PhysicalRankId, and ignores the retry log registers",
			detail: " err_code:0x0008:0x00c0  SystemAddress:0x8807f5d2c0 ProcessorSocketId:0x1 MemoryControllerId:0x3 " +
				"ChannelId:0x1 PhysicalRankId:0x4 DimmSlotId:0x1 Row:0x0 Column:0x0 Bank:0x0 BankGroup:0x0 " +
				"retry_rd_err_log[000228c0 20040000 01600000 0x0000000100 0x0000000000] correrrcnt[0001 0000 0000 0000 0000 0000 0000 0000]",
			want: Detail{
				Decoder: DecoderADXL, Socket: some(1), IMC: some(3), Channel: some(1), Slot: some(1), Rank: some(4),
				BankGroup: some(0), Bank: some(0), Row: some(0), Column: some(0),
				SystemAddress: model.Some(uint64(0x8807f5d2c0)), MSCOD: some(0x0008), MCACOD: some(0x00c0),
			},
		},
		{
			name:   "driver decode, 6.x field names (no ADXL)",
			detail: " err_code:0x0101:0x0090 ProcessorSocketId:0x0 MemoryControllerId:0x0 PhysicalRankId:0x1 Row:0x1a50 Column:0x3f8 Bank:0x3 BankGroup:0x1",
			want: Detail{
				Decoder: DecoderSKX, Socket: some(0), IMC: some(0), Rank: some(1),
				BankGroup: some(1), Bank: some(3), Row: some(0x1a50), Column: some(0x3f8),
				MSCOD: some(0x0101), MCACOD: some(0x0090),
			},
		},
		{
			name:   "driver decode, 5.x: row and column with 0x",
			detail: " err_code:0x0101:0x0090 socket:1 imc:2 rank:0 bg:1 ba:3 row:0x1a50 col:0x3f8",
			want: Detail{
				Decoder: DecoderSKX, Socket: some(1), IMC: some(2), Rank: some(0),
				BankGroup: some(1), Bank: some(3), Row: some(0x1a50), Column: some(0x3f8),
				MSCOD: some(0x0101), MCACOD: some(0x0090),
			},
		},
		{
			name:   "driver decode, 4.x: row and column are bare hexadecimal",
			detail: " err_code:0101:0090 socket:0 imc:1 rank:1 bg:0 ba:2 row:1000 col:10",
			want: Detail{
				Decoder: DecoderSKX, Socket: some(0), IMC: some(1), Rank: some(1),
				BankGroup: some(0), Bank: some(2), Row: some(0x1000), Column: some(0x10),
				MSCOD: some(0x0101), MCACOD: some(0x0090),
			},
		},
		{
			name:   "overflow and recoverable marks",
			detail: " OVERFLOW recoverable err_code:0x0001:0x00c1 socket:0 imc:0 rank:0 bg:0 ba:0 row:0x2 col:0x8",
			want: Detail{
				Decoder: DecoderSKX, Socket: some(0), IMC: some(0), Rank: some(0),
				BankGroup: some(0), Bank: some(0), Row: some(2), Column: some(8),
				Overflow: true, Recoverable: true, MSCOD: some(1), MCACOD: some(0xc1),
			},
		},
		{
			name:   "sb_edac: socket, home agent and rank only",
			detail: " area:DRAM err_code:0001:0090 socket:1 ha:0 channel_mask:4 rank:2",
			want: Detail{
				Decoder: DecoderSB, Socket: some(1), IMC: some(0), Rank: some(2),
				MSCOD: some(1), MCACOD: some(0x90),
			},
		},
		{
			name: "ghes_edac up to 5.x: decimal numbers, col and bit_pos",
			detail: "APEI location: node:0 card:1 module:0 rank:0 bank:3 row:6703 col:1016 bit_pos:5 " +
				"DIMM location:NODE 1 DIMM_A1 status(0x0000000000000400): Storage error in DRAM memory",
			want: Detail{
				Decoder: DecoderGHES, Socket: some(0), Channel: some(1), Slot: some(0), Rank: some(0),
				Bank: some(3), Row: some(6703), Column: some(1016), BitPosition: some(5), DIMMLocation: "NODE 1 DIMM_A1",
			},
		},
		{
			name: "ghes_edac 6.x: bank group and address, device, column and bit_position",
			detail: "APEI location: node:1 card:2 module:1 rank:1 bank_group:1 bank_address:3 device:7 row:6703 column:1016 " +
				"bit_position:12 DIMM location: CPU2 DIMM_C2 status(0x0000000000000400): Storage error in DRAM memory",
			want: Detail{
				Decoder: DecoderGHES, Socket: some(1), Channel: some(2), Slot: some(1), Rank: some(1), Device: some(7),
				BankGroup: some(1), Bank: some(3), Row: some(6703), Column: some(1016), BitPosition: some(12),
				DIMMLocation: "CPU2 DIMM_C2",
			},
		},
		{
			name:   "ghes_edac with a DMI handle the kernel could not resolve",
			detail: "APEI location: node:0 card:0 module:1 DIMM location: not present. DMI handle: 0x0012 status(0x0000000000000400): Storage error in DRAM memory",
			want:   Detail{Decoder: DecoderGHES, Socket: some(0), Channel: some(0), Slot: some(1), SMBIOSHandle: some(0x12)},
		},
		{
			name:   "amd64_edac passes no detail",
			detail: "",
			want:   Detail{},
		},
		{
			name:   "a string in no known dialect stays undecoded",
			detail: "row 7 column 9 addr = 0x00001000, cpu=3, Dimm=1, Channel=2, syndrome=0x00000041",
			want:   Detail{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseDetail(tt.detail); got != tt.want {
				t.Errorf("ParseDetail\n got: %+v\nwant: %+v", got, tt.want)
			}
		})
	}
}

// The same digits under the same field name are different rows in different
// dialects. Mixing them up is the decode bug that turns single-bit faults
// into row faults.
func TestRowAndColumnRadixFollowsTheDialect(t *testing.T) {
	skx := ParseDetail(" err_code:0101:0090 socket:0 imc:0 rank:0 bg:0 ba:0 row:1016 col:1016")
	ghes := ParseDetail("APEI location: node:0 card:0 module:0 rank:0 bank:0 row:1016 col:1016 ")
	if skx.Row != some(0x1016) || skx.Column != some(0x1016) {
		t.Errorf("skx: row %v column %v, want 0x1016 (4118) for both", skx.Row, skx.Column)
	}
	if ghes.Row != some(1016) || ghes.Column != some(1016) {
		t.Errorf("ghes: row %v column %v, want decimal 1016 for both", ghes.Row, ghes.Column)
	}
	// Digits that are not decimal can only come from the hexadecimal dialect.
	if d := ParseDetail(" err_code:0101:0090 socket:0 imc:0 rank:0 bg:0 ba:0 row:1a2f col:3f8"); d.Row != some(0x1a2f) || d.Column != some(0x3f8) {
		t.Errorf("bare hexadecimal: row %v column %v", d.Row, d.Column)
	}
	// A decimal field must not accept hexadecimal digits.
	if d := ParseDetail(" err_code:0101:0090 socket:a imc:0 rank:0x1 bg:0 ba:0 row:1 col:1"); d.Socket.Valid || d.Rank.Valid {
		t.Errorf("malformed decimal fields were accepted: socket %v rank %v", d.Socket, d.Rank)
	}
}

func TestLayers(t *testing.T) {
	tests := []struct {
		name string
		l    Layers
		key  string
	}{
		{"channel and slot", LayersFromIndices(0, 1, -1), "0:1"},
		{"three layers", LayersFromIndices(1, 2, 0), "1:2:0"},
		{"single layer", LayersFromIndices(3, -1, -1), "3"},
		{"no layer", LayersFromIndices(-1, -1, -1), ""},
		{"255 is an unsigned reading of -1", LayersFromIndices(0, 255, 255), "0"},
		{"kernel log form", ParseLayers("channel:0 slot:1"), "0:1"},
		{"sysfs form", ParseLayers("channel 2 slot 0"), "2:0"},
		{"chip-select form", ParseLayers("csrow:2 channel:1"), "2:1"},
		{"ghes location has no layers", ParseLayers("node:0 card:1 module:0 rank:0 bank:3"), ""},
		{"empty", ParseLayers(""), ""},
	}
	for _, tt := range tests {
		if got := tt.l.Key(); got != tt.key {
			t.Errorf("%s: key = %q, want %q", tt.name, got, tt.key)
		}
	}
	l := ParseLayers("csrow:2 channel:1")
	if l.Named("csrow") != some(2) || l.Named("channel") != some(1) || l.Named("slot").Valid {
		t.Errorf("named layers of %+v", l)
	}
	if LayersFromIndices(0, 1, -1).Named("channel").Valid {
		t.Error("layers built from indices have no names")
	}
}

func TestParseLabel(t *testing.T) {
	tests := []struct {
		label string
		want  Label
	}{
		{"CPU_SrcID#0_MC#1_Chan#0_DIMM#0", Label{Kind: "intel", Socket: some(0), IMC: some(1), Channel: some(0), Slot: some(0)}},
		{"CPU_SrcID#1_Ha#0_Chan#2_DIMM#1", Label{Kind: "intel-ha", Socket: some(1), IMC: some(0), Channel: some(2), Slot: some(1)}},
		{"mc#0csrow#2channel#1", Label{Kind: "csrow", Channel: some(1), Slot: some(1), Rank: some(0)}},
		{"mc#1csrow#3channel#0", Label{Kind: "csrow", Channel: some(0), Slot: some(1), Rank: some(1)}},
		{"CPU1_DIMM_A1", Label{}},
		{"unknown memory", Label{}},
		{"", Label{}},
	}
	for _, tt := range tests {
		if got := ParseLabel(tt.label); got != tt.want {
			t.Errorf("ParseLabel(%q) = %+v, want %+v", tt.label, got, tt.want)
		}
	}
	for _, l := range []string{"", "unknown memory", "unknown label", "any memory", " Unknown Memory "} {
		if !PlaceholderLabel(l) {
			t.Errorf("PlaceholderLabel(%q) = false", l)
		}
	}
	if PlaceholderLabel("CPU1_DIMM_A1") {
		t.Error("a real label was taken for a placeholder")
	}
}

func TestSeverityAndScrubType(t *testing.T) {
	sev := []struct {
		typ         string
		recoverable bool
		want        model.Severity
		ok          bool
	}{
		{"Corrected", false, model.SevCE, true},
		{"CE", false, model.SevCE, true},
		{"Uncorrected", false, model.SevUE, true},
		{"UE", true, model.SevRecoverable, true},
		{"Deferred", false, model.SevRecoverable, true},
		{"Fatal", false, model.SevFatal, true},
		{"Info", false, model.SevInformational, true},
		{"Bogus", false, "", false},
	}
	for _, tt := range sev {
		got, ok := Severity(tt.typ, tt.recoverable)
		if got != tt.want || ok != tt.ok {
			t.Errorf("Severity(%q, %v) = %q, %v; want %q, %v", tt.typ, tt.recoverable, got, ok, tt.want, tt.ok)
		}
	}
	scrub := map[string]model.ScrubType{
		"memory read error":                   model.ScrubDemand,
		"memory write error":                  model.ScrubDemand,
		"memory scrubbing error":              model.ScrubPatrol,
		"Scrub corrected error":               model.ScrubPatrol,
		"addr/cmd error":                      model.ScrubOther,
		"Single-bit ECC":                      "",
		"generic undef request error":         "",
		"":                                    "",
		"Failed to map error addr to a csrow": "",
	}
	for msg, want := range scrub {
		if got := ScrubType(msg); got != want {
			t.Errorf("ScrubType(%q) = %q, want %q", msg, got, want)
		}
	}
}

func TestApplyKeepsWhatIsAlreadySet(t *testing.T) {
	e := model.Event{SocketId: some(1), ChannelId: some(5), Decoder: "cper"}
	d := ParseDetail(" err_code:0x0101:0x0091  SystemAddress:0x1f8bd19c0 ProcessorSocketId:0x0 MemoryControllerId:0x1 ChannelId:0x0 DimmSlotId:0x0 Row:0x1a2f Column:0x3f8 Bank:0x3 BankGroup:0x1")
	d.Apply(&e)
	if e.SocketId != some(1) || e.ChannelId != some(5) || e.Decoder != "cper" {
		t.Errorf("Apply overwrote set fields: %+v", e)
	}
	if e.IMC != some(1) || e.Row != some(0x1a2f) || e.Column != some(0x3f8) || e.Bank != some(3) || e.BankGroup != some(1) ||
		e.OsPhysicalAddress != model.Some(uint64(0x1f8bd19c0)) {
		t.Errorf("Apply did not fill unset fields: %+v", e)
	}
	if got := e.ComputeGrade(); got != model.GradeA {
		t.Errorf("grade after ADXL decode = %s, want A", got)
	}

	var amd model.Event
	ParseLabel("mc#0csrow#3channel#2").Apply(&amd)
	if amd.ChannelId != some(2) || amd.ChannelSlotId != some(1) || amd.Rank != some(1) || amd.Decoder != DecoderCSROW {
		t.Errorf("label apply: %+v", amd)
	}
	if amd.Row != none || amd.ComputeGrade() != model.GradeB {
		t.Errorf("a chip-select position is grade B: row %v grade %s", amd.Row, amd.ComputeGrade())
	}

	var g model.Event
	gd := ParseDetail("APEI location: node:0 card:1 module:0 rank:0 bank:3 row:6703 col:1016 bit_pos:5 DIMM DMI handle: 0x1100 ")
	gd.Apply(&g)
	if g.ErrorVector != "bit:5" || g.SMBIOSHandle != some(0x1100) || g.Decoder != DecoderGHES {
		t.Errorf("ghes apply: %+v", g)
	}
}
