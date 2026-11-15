package ipmi

import (
	"strings"
	"testing"
)

// FuzzLabels feeds arbitrary text to the functions that find a module or a
// memory event in the words of a BMC. The text is a sensor name or a
// message of an event log: whatever a BMC wrote. What comes back is part
// of the text, never more than it holds.
func FuzzLabels(f *testing.F) {
	for _, s := range []string{
		"Correctable ECC (@DIMMB1(CPU1))", "CPU 1 DIMM 3", "DIMM_C1 Status", "CPU0_DIMM0_Status", "Mem ECC Warning",
		"DIMM_A1: Correctable ECC logging limit reached", "P1-DIMMA1 uncorrectable ecc.", "DIMM 12, DIMM A1 and dimm3",
		"ȺȺȺȺ correctable ecc", "İ Uncorrectable ECC K", "correctable ecc", "",
		"@DIMM(CPU)", "DIMM" + strings.Repeat("_", 300) + "9", strings.Repeat("DIMM1 ", 200),
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		_ = IsProcessorModule(text)
		names := ModuleNames(text)
		if len(names) > len(text) {
			t.Errorf("%d names from %d bytes", len(names), len(text))
		}
		for _, n := range names {
			if n == "" || len(n) > len(text)+8 {
				t.Errorf("module name %q from %q", n, text)
			}
		}
		if m := SensorModule(text); len(m) > len(text) {
			t.Errorf("sensor module %q from %q", m, text)
		}
		ev, rest := MemoryEventInText(text)
		if ev == MemUnknown && rest != "" {
			t.Errorf("no event and the rest %q", rest)
		}
		if !strings.HasSuffix(strings.TrimSpace(text), rest) {
			t.Errorf("the rest %q is not the end of %q", rest, text)
		}
		ev, rest = MemoryEventFromText(text)
		if ev == MemUnknown && rest != "" {
			t.Errorf("no event and the rest %q", rest)
		}
		if !strings.HasSuffix(strings.TrimSpace(text), rest) {
			t.Errorf("the rest %q is not the end of %q", rest, text)
		}
	})
}
