package ipmi

import (
	"fmt"
	"regexp"
	"strings"
)

// The texts tools and BMCs print for a memory event name the module in
// forms of their own. These helpers find the module in such a text.

var (
	// @DIMMA1(CPU1): the form ipmitool prints for Supermicro boards, which
	// the BMC of those boards also uses in its own messages.
	atDimmRE = regexp.MustCompile(`@DIMM([A-Z0-9]{1,3})\(CPU(\d{1,2})\)`)
	// One word that holds DIMM, with what is attached to it.
	dimmWordRE = regexp.MustCompile(`(?i)[A-Z0-9_.#-]*DIMM[A-Z0-9_.#-]*`)
	// DIMM 3, DIMM A1: the designator after a space.
	dimmSpacedRE = regexp.MustCompile(`(?i)\bDIMM\s+[A-Z]{0,2}\d{1,3}\b`)
	// CPU 1 DIMM 3: a processor and a module number.
	cpuDimmRE = regexp.MustCompile(`(?i)^(?:CPU|PROC|P|SOCKET)\s*\d{1,2}\s+DIMM\s*[A-Z]{0,2}\d{1,3}$`)
	// What boards append to the name of a per-DIMM sensor.
	sensorSuffixRE = regexp.MustCompile(`(?i)[\s_-]+(status|stat|state|ecc|err|error|fault|health)$`)
)

// IsProcessorModule reports whether text is a processor and a module
// number and nothing else, such as "CPU 1 DIMM 3".
func IsProcessorModule(text string) bool {
	return cpuDimmRE.MatchString(strings.TrimSpace(text))
}

// ModuleNames returns the DIMM names found in a text: words that hold
// "DIMM" with a digit at or after it, and "DIMM n" with the number apart.
// The form "@DIMMA1(CPU1)" is returned as "P1-DIMMA1", the name the boards
// that use this form print on the slot.
func ModuleNames(text string) []string {
	if m := atDimmRE.FindAllStringSubmatch(text, -1); m != nil {
		var out []string
		for _, x := range m {
			out = append(out, fmt.Sprintf("P%s-DIMM%s", x[2], x[1]))
		}
		return out
	}
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.Trim(s, ".,-#_")
		if k := strings.ToUpper(s); s != "" && !seen[k] {
			seen[k] = true
			out = append(out, s)
		}
	}
	for _, loc := range dimmSpacedRE.FindAllStringIndex(text, -1) {
		add(strings.Join(strings.Fields(text[loc[0]:loc[1]]), " "))
	}
	// Words with the designator attached. A spaced form found above leaves
	// the bare word "DIMM" behind, which has no digit and is dropped here.
	for _, w := range dimmWordRE.FindAllString(text, -1) {
		i := strings.Index(lowerASCII(w), "dimm")
		if i >= 0 && strings.ContainsAny(w[i:], "0123456789") {
			add(w)
		}
	}
	return out
}

// SensorModule returns the module a sensor name stands for, or "" for a
// sensor that is not per module: "DIMM_C1" and "CPU0_DIMM0_Status" name a
// module, "Mem ECC Warning" does not.
func SensorModule(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	base := sensorSuffixRE.ReplaceAllString(name, "")
	if cpuDimmRE.MatchString(base) {
		return strings.Join(strings.Fields(base), " ")
	}
	names := ModuleNames(base)
	if len(names) != 1 {
		return ""
	}
	if dimmSpacedRE.MatchString(base) {
		// "DIMM 3" inside a longer name: the words before it tell DIMMs of
		// different processors apart, so the whole name is the label.
		return strings.Join(strings.Fields(base), " ")
	}
	return sensorSuffixRE.ReplaceAllString(names[0], "")
}

// MemoryEventInText finds the name of a memory sensor state anywhere in a
// text, for messages that put other words before it ("DIMM_A1: Correctable
// ECC"). It returns the state and the text after the name, or MemUnknown.
// Only the names that cannot mean anything else are searched for: a
// message that merely holds the word "spare" or "parity" says too little.
func MemoryEventInText(text string) (ev MemoryEvent, rest string) {
	low := lowerASCII(text)
	for _, t := range memoryTexts {
		switch t.ev {
		case MemCorrectable, MemUncorrectable, MemLogLimit, MemDeviceDisabled, MemScrubFailed:
		default:
			continue
		}
		i := strings.Index(low, t.text)
		if i < 0 {
			continue
		}
		// The name must stand on its own.
		if i > 0 && isWordByte(low[i-1]) {
			continue
		}
		end := i + len(t.text)
		if end < len(low) && isWordByte(low[end]) {
			continue
		}
		return t.ev, strings.TrimSpace(text[end:])
	}
	return MemUnknown, ""
}

// lowerASCII lowers the letters A to Z and leaves every other byte as it
// is, so that a position in the result is the same position in s. The
// lower case of some letters outside ASCII has another length in bytes
// than the letter itself: a position found in strings.ToLower(s) can lie
// beside, or beyond the end of, s.
func lowerASCII(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 'A' && c <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if c := b[j]; c >= 'A' && c <= 'Z' {
					b[j] = c + 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}
