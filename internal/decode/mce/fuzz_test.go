package mce

import (
	"testing"

	"github.com/agenticode/my/internal/model"
)

// FuzzDecode feeds arbitrary register values and location texts to the
// decoders. A machine check record is whatever a database row or a log
// line holds: every bit pattern must give a class, and a memory error a
// severity the ledger knows.
func FuzzDecode(f *testing.F) {
	// Intel: corrected read error on channel 1, patrol scrub UCNA, SRAR
	// data load. AMD: DRAM ECC with a syndrome, deferred.
	f.Add(0, 7, uint64(0x8c00004000010091), uint64(0x12345000), uint64(0x86), uint64(0), uint64(0), "memory_channel=1,csrow=0", "MEMORY CONTROLLER RD_CHANNEL1_ERR")
	f.Add(0, 9, uint64(0xac000000000100c1), uint64(0x7f000), uint64(0), uint64(0), uint64(0), "memory_channel=2", "")
	f.Add(0, 1, uint64(0xbd80000000100134), uint64(0x1000), uint64(0x8c), uint64(0), uint64(0), "", "Data CACHE Level-0 Data-Read Error")
	f.Add(2, 18, uint64(0x9c2040000000011b), uint64(0x4d8d3a00), uint64(0), uint64(0x0000009600150f00), uint64(0x0a000005), "memory_channel=1,csrow=5", "DRAM ECC error")
	f.Add(9, 0, uint64(0x9400100000000000), uint64(0), uint64(0), uint64(0), uint64(0), "csrow=-1 memory_channel=99999999999999999999", "dram ecc")
	f.Add(-1, -1, ^uint64(0), ^uint64(0), ^uint64(0), ^uint64(0), ^uint64(0), "=,=1,a=b=c,,x=0x10", "")

	f.Fuzz(func(t *testing.T, vendor, bank int, status, addr, misc, ipid, synd uint64, location, text string) {
		d := Decode(Record{Vendor: vendor, Bank: bank, Status: status, Addr: addr, Misc: misc, IPID: ipid, Synd: synd})
		if d.Class == "" || !d.Severity.Valid() {
			t.Errorf("status %#x of vendor %d: class %q, severity %q", status, vendor, d.Class, d.Severity)
		}
		if !d.Scrub.Valid() {
			t.Errorf("status %#x: scrub type %q", status, d.Scrub)
		}
		if d.Memory && d.ErrorType == "" {
			t.Errorf("status %#x: a memory error without a type", status)
		}
		if d.Address.Valid != (status&statusAddrV != 0) || (d.Address.Valid && d.Address.V != addr) {
			t.Errorf("status %#x: address %+v", status, d.Address)
		}
		if d.Overflow != (status&statusOver != 0) {
			t.Errorf("status %#x: overflow %v", status, d.Overflow)
		}
		for name, o := range map[string]model.Opt[int]{"channel": d.Channel, "chip select": d.ChipSelect} {
			if o.Valid && (o.V < 0 || o.V > 0xfff) {
				t.Errorf("status %#x, ipid %#x, synd %#x: %s %d", status, ipid, synd, name, o.V)
			}
		}
		// An uncorrected error is never reported as a corrected one.
		if status&statusUC != 0 && d.Severity == model.SevCE {
			t.Errorf("status %#x has the UC bit and was decoded as corrected", status)
		}
		_ = Valid(status)

		loc := ParseLocation(location)
		if len(loc) > len(location) {
			t.Errorf("%d keys from %d bytes", len(loc), len(location))
		}
		_ = IsMemoryText(text, location)
	})
}
