package model

import (
	"encoding/json"
	"testing"
)

func TestECSDecode(t *testing.T) {
	// MR14 0x20: code word counting. MR15 0x03: 256 per Gb. MR16..MR18:
	// row 0x21A2F, bank address 3, bank group 5. MR19 0x10: bit 4, the row
	// had 64 errors or more. MR20 0x04: bit 2, four times the threshold.
	s := ECSSample{MR14: Some(0x20), MR15: Some(0x03), MR16: Some(0x2F), MR17: Some(0x1A), MR18: Some(0x5E), MR19: Some(0x10), MR20: Some(0x04)}
	if got := s.CountMode(); got != ECSCountCodewords {
		t.Errorf("count mode = %q", got)
	}
	if got := s.ThresholdPerGb(); got != Some(256) {
		t.Errorf("threshold = %v, want 256", got)
	}
	bg, bank, row := s.MaxRow()
	if bg != Some(5) || bank != Some(3) || row != Some(0x21A2F) {
		t.Errorf("max row = bank group %v, bank %v, row %#x", bg, bank, row.V)
	}
	if got := s.MaxRowErrorsMin(); got != Some(64) {
		t.Errorf("errors on the row = %v, want 64", got)
	}
	if got := s.ErrorsPerGbMin(); got != Some(1024) {
		t.Errorf("errors per Gb = %v, want 1024", got)
	}
	if s.Empty() {
		t.Error("a full reading counts as empty")
	}
}

func TestECSThresholdCodes(t *testing.T) {
	for code, want := range []int{4, 16, 64, 256, 1024, 4096} {
		// The bits above OP[2:0] hold other settings.
		s := ECSSample{MR15: Some(code | 0x08)}
		if got := s.ThresholdPerGb(); got != Some(want) {
			t.Errorf("MR15 code %d: threshold = %v, want %d", code, got, want)
		}
	}
	for _, reserved := range []int{6, 7} {
		s := ECSSample{MR15: Some(reserved), MR20: Some(1)}
		if s.ThresholdPerGb().Valid || s.ErrorsPerGbMin().Valid {
			t.Errorf("MR15 code %d is reserved and gave a threshold", reserved)
		}
	}
}

func TestECSRanges(t *testing.T) {
	for _, c := range []struct {
		mr19, mr20       int
		rowMin, perGbMin int
	}{
		{0x00, 0x00, 0, 0},      // below the row threshold of 4, below the count threshold
		{0x01, 0x01, 4, 64},     // the lowest range
		{0x20, 0x80, 128, 8192}, // the highest range
		{0x03, 0x06, 8, 256},    // more than one bit: the highest one counts
		{0xC1, 0x01, 4, 64},     // MR19 OP[7:6] are not part of the count
	} {
		s := ECSSample{MR15: Some(0x02), MR19: Some(c.mr19), MR20: Some(c.mr20)}
		if got := s.MaxRowErrorsMin(); got != Some(c.rowMin) {
			t.Errorf("MR19 %#02x: row errors = %v, want %d", c.mr19, got, c.rowMin)
		}
		if got := s.ErrorsPerGbMin(); got != Some(c.perGbMin) {
			t.Errorf("MR20 %#02x: errors per Gb = %v, want %d", c.mr20, got, c.perGbMin)
		}
	}
}

func TestECSPartialReading(t *testing.T) {
	var s ECSSample
	if !s.Empty() || s.CountMode() != "" || s.ThresholdPerGb().Valid || s.MaxRowErrorsMin().Valid || s.ErrorsPerGbMin().Valid {
		t.Errorf("an empty reading decoded to something: %+v", s)
	}
	if _, _, row := s.MaxRow(); row.Valid {
		t.Error("an empty reading has a row")
	}
	// The row needs all three address registers.
	s = ECSSample{MR16: Some(1), MR17: Some(2)}
	if _, _, row := s.MaxRow(); row.Valid {
		t.Error("two of three address registers gave a row")
	}
	// The count needs the threshold it is a multiple of.
	s = ECSSample{MR20: Some(0x02)}
	if s.ErrorsPerGbMin().Valid {
		t.Error("MR20 without MR15 gave a count")
	}
	s = ECSSample{MR14: Some(0x80)}
	if s.CountMode() != ECSCountRows || s.Empty() {
		t.Errorf("MR14 0x80: mode %q, empty %v", s.CountMode(), s.Empty())
	}
}

func TestECSSetRegister(t *testing.T) {
	var s ECSSample
	for mr := 14; mr <= 20; mr++ {
		if !s.SetRegister(mr, mr-14) {
			t.Errorf("MR%d was refused", mr)
		}
	}
	for i, r := range s.Registers() {
		if r != Some(i) {
			t.Errorf("MR%d = %v, want %d", 14+i, r, i)
		}
	}
	for _, c := range [][2]int{{13, 0}, {21, 0}, {14, 256}, {14, -1}} {
		if s.SetRegister(c[0], c[1]) {
			t.Errorf("SetRegister(%d, %d) was accepted", c[0], c[1])
		}
	}
}

func TestECSJSON(t *testing.T) {
	s := ECSSample{Host: "h1", MR19: Some(0), Source: "fmfm/runtime", RawRef: "r1", Fingerprint: "f"}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	// A register that reads zero is written; one that was not read is left out.
	if v, ok := m["mr19"]; !ok || v != float64(0) {
		t.Errorf("mr19 = %v, %v", v, ok)
	}
	if _, ok := m["mr20"]; ok {
		t.Errorf("mr20 is in the JSON of a reading without it: %s", b)
	}
	var back ECSSample
	if err := json.Unmarshal(b, &back); err != nil || back.MR19 != Some(0) || back.MR20.Valid {
		t.Errorf("round trip = %+v, %v", back, err)
	}
}
