package sel

import (
	"fmt"
	"regexp"
	"strings"
)

// A SEL record names the module it is about in one of three places, none
// of them standard:
//
//   - the sensor name, on boards that have one sensor per DIMM
//     ("Memory DIMM_A1");
//   - text the tool decoded from vendor bytes and put after the event name
//     ("Correctable ECC (@DIMMA1(CPU1))", "Uncorrectable ECC ( DIMMB2)") or
//     in a field of its own ("CPU 1 DIMM 3");
//   - event data 3, when the platform fills it the way the specification
//     defines.
//
// A record that names its module in none of these ways is kept without a
// label: a shared sensor such as "Memory ECC" says nothing about which
// DIMM had the error.

var (
	// @DIMMA1(CPU1): the form ipmitool prints for Supermicro boards.
	supermicroRE = regexp.MustCompile(`@DIMM([A-Z0-9]{1,3})\(CPU(\d{1,2})\)`)
	// One word that holds DIMM, with what is attached to it.
	dimmWordRE = regexp.MustCompile(`(?i)[A-Z0-9_.#-]*DIMM[A-Z0-9_.#-]*`)
	// DIMM 3, DIMM A1: the designator after a space.
	dimmSpacedRE = regexp.MustCompile(`(?i)\bDIMM\s+[A-Z]{0,2}\d{1,3}\b`)
	// CPU 1 DIMM 3: the field ipmitool adds for a sensor without a number.
	cpuDimmRE = regexp.MustCompile(`(?i)^(?:CPU|PROC|P|SOCKET)\s*\d{1,2}\s+DIMM\s*[A-Z]{0,2}\d{1,3}$`)
	// What boards append to the name of a per-DIMM sensor.
	sensorSuffixRE = regexp.MustCompile(`(?i)[\s_-]+(status|stat|state|ecc|err|error|fault|health)$`)
)

// dimmNames returns the DIMM names found in a text: words that hold "DIMM"
// with a digit at or after it, and "DIMM n" with the number apart.
func dimmNames(text string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.Trim(s, ".,-#_")
		if k := strings.ToUpper(s); s != "" && !seen[k] {
			seen[k] = true
			out = append(out, s)
		}
	}
	rest := text
	for _, loc := range dimmSpacedRE.FindAllStringIndex(text, -1) {
		// A qualifier before it belongs to the name: "CPU 1 DIMM 3".
		add(strings.Join(strings.Fields(text[loc[0]:loc[1]]), " "))
	}
	// Words with the designator attached. A spaced form found above leaves
	// the bare word "DIMM" behind, which has no digit and is dropped here.
	for _, w := range dimmWordRE.FindAllString(rest, -1) {
		i := strings.Index(strings.ToUpper(w), "DIMM")
		if strings.ContainsAny(w[i:], "0123456789") {
			add(w)
		}
	}
	return out
}

// sensorLabel returns the module a sensor name stands for, or "" for a
// sensor that is not per module.
func sensorLabel(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	base := sensorSuffixRE.ReplaceAllString(name, "")
	if cpuDimmRE.MatchString(base) {
		return strings.Join(strings.Fields(base), " ")
	}
	names := dimmNames(base)
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

// labels returns the modules a record names. rest is the text after the
// event name in the description.
func (r *reader) labels(e *entry, rest string) []string {
	// Text the tool decoded from vendor bytes.
	if m := supermicroRE.FindAllStringSubmatch(rest, -1); m != nil {
		var out []string
		for _, x := range m {
			// These boards print the slot as P1-DIMMA1.
			out = append(out, fmt.Sprintf("P%s-DIMM%s", x[2], x[1]))
		}
		return out
	}
	if extra := strings.TrimSpace(e.extra); cpuDimmRE.MatchString(extra) {
		return []string{strings.Join(strings.Fields(extra), " ")}
	}
	if names := dimmNames(rest); len(names) > 0 {
		return names
	}
	if l := sensorLabel(e.sensorName); l != "" {
		return []string{l}
	}
	// Event data 3 in the form the specification defines: the module,
	// counted relative to what the sensor stands for.
	if e.rec != nil {
		if id := e.rec.ModuleID(); id.Valid {
			base := strings.TrimSpace(e.sensorName)
			if base == "" {
				base = fmt.Sprintf("sensor 0x%02x", e.rec.SensorNumber)
			}
			return []string{fmt.Sprintf("%s module %d", base, id.V)}
		}
	}
	return nil
}
