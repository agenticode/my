// Package mce decodes the x86 machine check status register as far as
// memory errors need: whether the record is a memory error, how severe it
// is, and how it was found.
package mce

import (
	"strconv"
	"strings"

	"github.com/agenticode/my/internal/model"
)

// CPU vendor codes as the kernel reports them in the mce record.
const (
	VendorIntel   = 0
	VendorAMD     = 2
	VendorHygon   = 9
	VendorZhaoxin = 10
)

// MCi_STATUS bits common to Intel and AMD.
const (
	statusVal   = 1 << 63
	statusOver  = 1 << 62
	statusUC    = 1 << 61
	statusEN    = 1 << 60
	statusMiscV = 1 << 59
	statusAddrV = 1 << 58
	statusPCC   = 1 << 57
)

// Intel bits.
const (
	intelS  = 1 << 56 // signaled
	intelAR = 1 << 55 // action required
)

// AMD bits.
const (
	amdDeferred = 1 << 44
	amdScrub    = 1 << 40
)

// Record is the part of a machine check record the decoder uses.
type Record struct {
	Vendor int
	Bank   int
	Status uint64
	Addr   uint64
	Misc   uint64
	// IPID and Synd are the AMD SMCA registers; zero when not reported.
	IPID uint64
	Synd uint64
}

// Decoded is what a machine check record says about a memory error.
type Decoded struct {
	// Memory is set when the record reports an error in memory: a memory
	// controller error, or the consumption of poisoned memory.
	Memory   bool
	Severity model.Severity
	Scrub    model.ScrubType
	// Class is the machine check class: "CE", "UCNA", "SRAO", "SRAR",
	// "Deferred" or "Fatal".
	Class string
	// Address is the reported address when the status says it is valid.
	// On AMD systems with the unified memory controller this is a
	// normalized address, different from the system physical address.
	Address model.Opt[uint64]
	// Channel and ChipSelect are set when the record encodes them.
	Channel    model.Opt[int]
	ChipSelect model.Opt[int]
	// ErrorType is a short description of the error code.
	ErrorType string
	Overflow  bool
}

// Valid reports whether the status register holds an error (bit 63).
func Valid(status uint64) bool { return status&statusVal != 0 }

// Decode classifies a machine check record.
func Decode(r Record) Decoded {
	var d Decoded
	st := r.Status
	d.Overflow = st&statusOver != 0
	if st&statusAddrV != 0 {
		d.Address = model.Some(r.Addr)
	}
	switch r.Vendor {
	case VendorAMD, VendorHygon:
		decodeAMD(r, &d)
	default:
		decodeIntel(r, &d)
	}
	return d
}

func decodeIntel(r Record, d *Decoded) {
	st := r.Status
	mcacod := uint16(st & 0xffff)

	// Memory controller errors: 000F 0000 1MMM CCCC (F is the filter bit).
	if mcacod&0xef80 == 0x0080 {
		d.Memory = true
		op := (mcacod >> 4) & 0x7
		switch op {
		case 1:
			d.ErrorType, d.Scrub = "memory read error", model.ScrubDemand
		case 2:
			d.ErrorType, d.Scrub = "memory write error", model.ScrubDemand
		case 3:
			d.ErrorType, d.Scrub = "address/command error", model.ScrubOther
		case 4:
			d.ErrorType, d.Scrub = "memory scrubbing error", model.ScrubPatrol
		default:
			d.ErrorType = "memory controller error"
		}
		if ch := int(mcacod & 0xf); ch != 0xf {
			d.Channel = model.Some(ch)
		}
	}

	switch {
	case st&statusUC == 0:
		d.Class, d.Severity = "CE", model.SevCE
	case st&statusPCC != 0:
		d.Class, d.Severity = "Fatal", model.SevFatal
	case st&intelS == 0:
		// Uncorrected, no action required: found and logged, not consumed.
		d.Class, d.Severity = "UCNA", model.SevRecoverable
	case st&intelAR == 0:
		// Action optional: found by the patrol scrubber or a cache writeback.
		d.Class, d.Severity = "SRAO", model.SevRecoverable
	default:
		// Action required: the poisoned data was about to be consumed.
		d.Class, d.Severity = "SRAR", model.SevRecoverable
	}

	// Data load (0x134) and instruction fetch (0x150) errors with action
	// required are how the consumption of an uncorrected memory error
	// shows up in the core's cache bank.
	if !d.Memory && d.Class == "SRAR" && (mcacod == 0x0134 || mcacod == 0x0150) {
		d.Memory = true
		d.Scrub = model.ScrubDemand
		if mcacod == 0x0134 {
			d.ErrorType = "data load of poisoned memory"
		} else {
			d.ErrorType = "instruction fetch of poisoned memory"
		}
	}
}

// smcaHWIDUMC is the SMCA hardware ID of the unified memory controller.
const smcaHWIDUMC = 0x96

func decodeAMD(r Record, d *Decoded) {
	st := r.Status
	xec := int(st>>16) & 0x3f

	if r.IPID != 0 {
		hwid := uint32(r.IPID>>32) & 0xfff
		if hwid == smcaHWIDUMC && xec == 0 {
			d.Memory = true
			d.ErrorType = "DRAM ECC error"
			d.Channel = model.Some(int(uint32(r.IPID) >> 20))
			if r.Synd != 0 {
				d.ChipSelect = model.Some(int(r.Synd & 0x7))
			}
		}
	}
	if st&amdScrub != 0 {
		d.Scrub = model.ScrubPatrol
	}

	switch {
	case st&statusUC != 0 && st&statusPCC != 0:
		d.Class, d.Severity = "Fatal", model.SevFatal
	case st&statusUC != 0:
		d.Class, d.Severity = "UC", model.SevUE
	case st&amdDeferred != 0:
		d.Class, d.Severity = "Deferred", model.SevRecoverable
	default:
		d.Class, d.Severity = "CE", model.SevCE
	}
}

// ParseLocation reads the key=value list rasdaemon stores in the
// mc_location column of mce_record, for example
// "memory_channel=1,csrow=0" or "memory_channel=2".
func ParseLocation(s string) map[string]int {
	out := map[string]int{}
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			continue
		}
		out[k] = n
	}
	return out
}

// IsMemoryText reports whether rasdaemon's own decode of a machine check
// names a DRAM error. It is the fallback for rows whose registers alone do
// not tell, such as AMD rows stored without the IPID register. rasdaemon
// writes "MEMORY CONTROLLER <op>_CHANNEL<n>_ERR" for Intel memory controller
// errors and "DRAM ECC error" for the AMD unified memory controller. Other
// errors of the AMD controller (parity errors on its interfaces) do not
// count: they are not errors of the memory cells.
func IsMemoryText(errorMsg, mcaStatusMsg string) bool {
	for _, t := range []string{errorMsg, mcaStatusMsg} {
		if strings.Contains(t, "MEMORY CONTROLLER") || strings.Contains(strings.ToLower(t), "dram ecc") {
			return true
		}
	}
	return false
}
