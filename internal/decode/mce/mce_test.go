package mce

import (
	"reflect"
	"testing"

	"github.com/agenticode/my/internal/model"
)

const (
	val   = uint64(1) << 63
	over  = uint64(1) << 62
	uc    = uint64(1) << 61
	en    = uint64(1) << 60
	miscv = uint64(1) << 59
	addrv = uint64(1) << 58
	pcc   = uint64(1) << 57
	s     = uint64(1) << 56
	ar    = uint64(1) << 55
)

func TestDecodeIntel(t *testing.T) {
	tests := []struct {
		name   string
		status uint64
		want   Decoded
	}{
		{
			"corrected memory read error on channel 0 (a real-world status value)",
			0x8c00004000010090,
			Decoded{Memory: true, Severity: model.SevCE, Scrub: model.ScrubDemand, Class: "CE",
				Address: model.Some(uint64(0x1f8bd19c0)), Channel: model.Some(0), ErrorType: "memory read error"},
		},
		{
			"corrected patrol scrub error on channel 1, bank overflowed",
			val | over | en | miscv | addrv | 0x00c1,
			Decoded{Memory: true, Severity: model.SevCE, Scrub: model.ScrubPatrol, Class: "CE",
				Address: model.Some(uint64(0x1f8bd19c0)), Channel: model.Some(1), ErrorType: "memory scrubbing error", Overflow: true},
		},
		{
			"channel not specified",
			val | en | addrv | 0x009f,
			Decoded{Memory: true, Severity: model.SevCE, Scrub: model.ScrubDemand, Class: "CE",
				Address: model.Some(uint64(0x1f8bd19c0)), ErrorType: "memory read error"},
		},
		{
			"address not valid",
			val | en | 0x0090,
			Decoded{Memory: true, Severity: model.SevCE, Scrub: model.ScrubDemand, Class: "CE", Channel: model.Some(0), ErrorType: "memory read error"},
		},
		{
			"uncorrected, no action required (UCNA)",
			val | uc | en | miscv | addrv | 0x0090,
			Decoded{Memory: true, Severity: model.SevRecoverable, Scrub: model.ScrubDemand, Class: "UCNA",
				Address: model.Some(uint64(0x1f8bd19c0)), Channel: model.Some(0), ErrorType: "memory read error"},
		},
		{
			"uncorrected patrol scrub error, action optional (SRAO)",
			val | uc | en | miscv | addrv | s | 0x00c0,
			Decoded{Memory: true, Severity: model.SevRecoverable, Scrub: model.ScrubPatrol, Class: "SRAO",
				Address: model.Some(uint64(0x1f8bd19c0)), Channel: model.Some(0), ErrorType: "memory scrubbing error"},
		},
		{
			"data load of poisoned memory, action required (SRAR)",
			val | uc | en | miscv | addrv | s | ar | 0x0134,
			Decoded{Memory: true, Severity: model.SevRecoverable, Scrub: model.ScrubDemand, Class: "SRAR",
				Address: model.Some(uint64(0x1f8bd19c0)), ErrorType: "data load of poisoned memory"},
		},
		{
			"processor context corrupt",
			val | uc | en | pcc | addrv | 0x0090,
			Decoded{Memory: true, Severity: model.SevFatal, Scrub: model.ScrubDemand, Class: "Fatal",
				Address: model.Some(uint64(0x1f8bd19c0)), Channel: model.Some(0), ErrorType: "memory read error"},
		},
		{
			"an internal parity error is not a memory error",
			val | uc | en | pcc | 0x0005,
			Decoded{Memory: false, Severity: model.SevFatal, Class: "Fatal"},
		},
		{
			"a corrected cache error is not a memory error",
			val | en | 0x0135,
			Decoded{Memory: false, Severity: model.SevCE, Class: "CE"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Decode(Record{Vendor: VendorIntel, Bank: 7, Status: tt.status, Addr: 0x1f8bd19c0})
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("\n got: %+v\nwant: %+v", got, tt.want)
			}
			if !Valid(tt.status) {
				t.Error("status should be valid")
			}
		})
	}
	if Valid(0x0c00004000010090) {
		t.Error("a status without bit 63 is not valid")
	}
}

func TestDecodeAMD(t *testing.T) {
	const (
		cecc     = uint64(1) << 46
		deferred = uint64(1) << 44
		scrub    = uint64(1) << 40
		// Unified memory controller, instance for channel 2.
		ipidUMC = uint64(0x0000009600250f00)
	)
	tests := []struct {
		name string
		rec  Record
		want Decoded
	}{
		{
			"corrected DRAM ECC error",
			Record{Vendor: VendorAMD, Bank: 17, Status: val | en | miscv | addrv | cecc | 0x011b, Addr: 0x7f5d2c9c0, IPID: ipidUMC, Synd: 0x0a801203},
			Decoded{Memory: true, Severity: model.SevCE, Class: "CE", Address: model.Some(uint64(0x7f5d2c9c0)),
				Channel: model.Some(2), ChipSelect: model.Some(3), ErrorType: "DRAM ECC error"},
		},
		{
			"found by the scrubber",
			Record{Vendor: VendorAMD, Bank: 17, Status: val | en | addrv | cecc | scrub | 0x011b, Addr: 0x1000, IPID: ipidUMC},
			Decoded{Memory: true, Severity: model.SevCE, Scrub: model.ScrubPatrol, Class: "CE", Address: model.Some(uint64(0x1000)),
				Channel: model.Some(2), ErrorType: "DRAM ECC error"},
		},
		{
			"deferred error",
			Record{Vendor: VendorHygon, Bank: 17, Status: val | en | addrv | deferred | 0x011b, Addr: 0x1000, IPID: ipidUMC},
			Decoded{Memory: true, Severity: model.SevRecoverable, Class: "Deferred", Address: model.Some(uint64(0x1000)),
				Channel: model.Some(2), ErrorType: "DRAM ECC error"},
		},
		{
			"uncorrected with context corrupt",
			Record{Vendor: VendorAMD, Bank: 17, Status: val | uc | en | pcc | addrv | 0x011b, Addr: 0x1000, IPID: ipidUMC},
			Decoded{Memory: true, Severity: model.SevFatal, Class: "Fatal", Address: model.Some(uint64(0x1000)),
				Channel: model.Some(2), ErrorType: "DRAM ECC error"},
		},
		{
			"another error of the memory controller (extended code 1) is not a DRAM error",
			Record{Vendor: VendorAMD, Bank: 17, Status: val | uc | en | 1<<16 | 0x011b, IPID: ipidUMC},
			Decoded{Memory: false, Severity: model.SevUE, Class: "UC"},
		},
		{
			"a bank of another type",
			Record{Vendor: VendorAMD, Bank: 0, Status: val | en | 0x0135, IPID: 0x000000b000000000},
			Decoded{Memory: false, Severity: model.SevCE, Class: "CE"},
		},
		{
			"no IPID: the registers alone do not say it is memory",
			Record{Vendor: VendorAMD, Bank: 17, Status: val | en | cecc | 0x011b},
			Decoded{Memory: false, Severity: model.SevCE, Class: "CE"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Decode(tt.rec); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("\n got: %+v\nwant: %+v", got, tt.want)
			}
		})
	}
}

func TestParseLocation(t *testing.T) {
	tests := []struct {
		in   string
		want map[string]int
	}{
		{"memory_channel=2,csrow=0", map[string]int{"memory_channel": 2, "csrow": 0}},
		{"memory_channel=1", map[string]int{"memory_channel": 1}},
		{"memory_channel=1, rank=3", map[string]int{"memory_channel": 1, "rank": 3}},
		{"ranks=0 and 1", map[string]int{"ranks": 0}},
		{"n_errors=12", map[string]int{"n_errors": 12}},
		{"", map[string]int{}},
		{"garbage, x=y", map[string]int{}},
	}
	for _, tt := range tests {
		if got := ParseLocation(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("ParseLocation(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestIsMemoryText(t *testing.T) {
	yes := [][2]string{
		{"MEMORY CONTROLLER RD_CHANNEL1_ERR", ""},
		{"MEMORY CONTROLLER MS_CHANNELunspecified_ERR", "Transaction: Memory scrubbing error"},
		{"", "DRAM ECC error."},
		{"DRAM ECC error", ""},
	}
	for _, c := range yes {
		if !IsMemoryText(c[0], c[1]) {
			t.Errorf("IsMemoryText(%q, %q) = false", c[0], c[1])
		}
	}
	no := [][2]string{
		{"", ""},
		{"Internal parity error", "Generic CACHE Level-2 Generic Error"},
		{"", "Write data parity error."}, // an interface error of the AMD memory controller
		{"BUS Level-3 Generic Generic IO Request-did-not-timeout Error", ""},
	}
	for _, c := range no {
		if IsMemoryText(c[0], c[1]) {
			t.Errorf("IsMemoryText(%q, %q) = true", c[0], c[1])
		}
	}
}
