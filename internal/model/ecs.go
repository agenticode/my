package model

import "time"

// ECSSample is one reading of the DDR5 error check and scrub (ECS) mode
// registers MR14 to MR20 of a DRAM device, or of a module when the source
// does not say which device the registers were read from.
//
// DDR5 devices correct single-bit errors themselves, so those errors never
// reach the memory controller. The ECS registers are the only view of them:
// a count per scrub pass and the row with the most errors, without the
// places of the single errors. A reading is therefore not an error event
// and is kept apart from the events.
//
// The registers are stored as read. The methods decode them following
// JESD79-5; a register that was not read leaves the values that depend on
// it unset.
type ECSSample struct {
	ID          int64       `json:"id,omitzero"`
	Host        string      `json:"host"`
	ObservedAt  time.Time   `json:"observed_at"`
	TimeQuality TimeQuality `json:"time_quality"`

	// The module, and the device on it, in FMFM field names.
	SocketId      Opt[int] `json:"SocketId,omitzero"`
	ChannelId     Opt[int] `json:"ChannelId,omitzero"`
	ChannelSlotId Opt[int] `json:"ChannelSlotId,omitzero"`
	SubChannel    Opt[int] `json:"SubChannel,omitzero"`
	Rank          Opt[int] `json:"Rank,omitzero"`
	DeviceId      Opt[int] `json:"DeviceId,omitzero"`
	// Label is the DIMM label as the source printed it, Slot the slot on
	// the host the reading was resolved to.
	Label   string `json:"label,omitzero"`
	Slot    string `json:"slot,omitzero"`
	DIMMUID string `json:"dimm_uid,omitzero"`

	// MR14 to MR20 as read, one byte each.
	MR14 Opt[int] `json:"mr14,omitzero"`
	MR15 Opt[int] `json:"mr15,omitzero"`
	MR16 Opt[int] `json:"mr16,omitzero"`
	MR17 Opt[int] `json:"mr17,omitzero"`
	MR18 Opt[int] `json:"mr18,omitzero"`
	MR19 Opt[int] `json:"mr19,omitzero"`
	MR20 Opt[int] `json:"mr20,omitzero"`

	BootID      string `json:"boot_id,omitzero"`
	Source      string `json:"source"`
	RawRef      string `json:"raw_ref"`
	Raw         string `json:"raw,omitzero"`
	Fingerprint string `json:"fingerprint"`
	ScanID      int64  `json:"scan_id,omitzero"`
}

// ECS counting modes.
const (
	// ECSCountRows: the error counter counts rows with at least one error.
	ECSCountRows = "rows"
	// ECSCountCodewords: the error counter counts code words with an error.
	ECSCountCodewords = "codewords"
)

// Registers returns MR14 to MR20 in order.
func (s *ECSSample) Registers() [7]Opt[int] {
	return [7]Opt[int]{s.MR14, s.MR15, s.MR16, s.MR17, s.MR18, s.MR19, s.MR20}
}

// SetRegister stores the value of mode register mr (14 to 20). It reports
// false for another register number or a value that is not a byte.
func (s *ECSSample) SetRegister(mr, value int) bool {
	if value < 0 || value > 0xFF {
		return false
	}
	v := Some(value)
	switch mr {
	case 14:
		s.MR14 = v
	case 15:
		s.MR15 = v
	case 16:
		s.MR16 = v
	case 17:
		s.MR17 = v
	case 18:
		s.MR18 = v
	case 19:
		s.MR19 = v
	case 20:
		s.MR20 = v
	default:
		return false
	}
	return true
}

// Empty reports whether no register was read.
func (s *ECSSample) Empty() bool {
	for _, r := range s.Registers() {
		if r.Valid {
			return false
		}
	}
	return true
}

// CountMode says what the error counter counts: ECSCountRows or
// ECSCountCodewords (MR14 OP[5]), or "" when MR14 was not read.
func (s *ECSSample) CountMode() string {
	if !s.MR14.Valid {
		return ""
	}
	if s.MR14.V&0x20 != 0 {
		return ECSCountCodewords
	}
	return ECSCountRows
}

// ThresholdPerGb returns the error threshold count per gigabit of memory
// cells the device was set to (MR15 OP[2:0]): 4, 16, 64, 256, 1024 or 4096.
// The device reports a count only from this threshold on.
func (s *ECSSample) ThresholdPerGb() Opt[int] {
	if !s.MR15.Valid {
		return Opt[int]{}
	}
	code := s.MR15.V & 0x07
	if code > 5 {
		return Opt[int]{} // reserved encodings
	}
	return Some(4 << (2 * code))
}

// MaxRow returns the address of the row with the most errors in the last
// scrub pass (MR16 to MR18): row address bits 0 to 17, bank address and
// bank group. All three registers must have been read.
func (s *ECSSample) MaxRow() (bankGroup, bank, row Opt[int]) {
	if !s.MR16.Valid || !s.MR17.Valid || !s.MR18.Valid {
		return
	}
	hi := s.MR18.V
	row = Some(s.MR16.V | s.MR17.V<<8 | (hi&0x03)<<16)
	bank = Some(hi >> 2 & 0x03)
	bankGroup = Some(hi >> 4 & 0x07)
	return bankGroup, bank, row
}

// rangeFloor returns the lower bound of the count range a register of
// range bits stands for: bit n set means at least unit times 2^n. No bit
// set means the count stayed below unit and gives 0.
func rangeFloor(bits, unit int) int {
	for n := 7; n >= 0; n-- {
		if bits&(1<<n) != 0 {
			return unit << n
		}
	}
	return 0
}

// MaxRowErrorsMin returns the least number of errors the row returned by
// MaxRow had (MR19 OP[5:0]). The register holds a range: bit n stands for
// 4 times 2^n errors or more, and no bit for fewer than 4.
func (s *ECSSample) MaxRowErrorsMin() Opt[int] {
	if !s.MR19.Valid {
		return Opt[int]{}
	}
	return Some(rangeFloor(s.MR19.V&0x3F, 4))
}

// ErrorsPerGbMin returns the least number of rows or code words with
// errors per gigabit that the last scrub pass counted (MR20 with the
// threshold of MR15). The register holds a range: bit n stands for the
// threshold times 2^n or more, and no bit for fewer than the threshold.
// What is counted is given by CountMode.
func (s *ECSSample) ErrorsPerGbMin() Opt[int] {
	th := s.ThresholdPerGb()
	if !s.MR20.Valid || !th.Valid {
		return Opt[int]{}
	}
	return Some(rangeFloor(s.MR20.V, th.V))
}
