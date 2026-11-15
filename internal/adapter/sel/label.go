package sel

import (
	"fmt"
	"strings"

	"github.com/agenticode/my/internal/decode/ipmi"
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

// labels returns the modules a record names. rest is the text after the
// event name in the description.
func (r *reader) labels(e *entry, rest string) []string {
	// Text the tool decoded from vendor bytes.
	if extra := strings.TrimSpace(e.extra); ipmi.IsProcessorModule(extra) {
		return []string{strings.Join(strings.Fields(extra), " ")}
	}
	if names := ipmi.ModuleNames(rest); len(names) > 0 {
		return names
	}
	if l := ipmi.SensorModule(e.sensorName); l != "" {
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
