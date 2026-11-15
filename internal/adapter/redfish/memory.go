package redfish

import (
	"fmt"
	"strings"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
)

// locator returns the name of the slot a memory resource describes: the
// device locator, which is what is printed on the board, and failing that
// the service label, the name or the Id of the resource.
func locator(o object) string {
	if l := str(o, "DeviceLocator"); l != "" {
		return l
	}
	if loc := child(o, "Location"); loc != nil {
		if pl := child(loc, "PartLocation"); pl != nil {
			if l := str(pl, "ServiceLabel"); l != "" {
				return l
			}
		}
	}
	if n := str(o, "Name"); n != "" {
		return n
	}
	return str(o, "Id")
}

// otherNames returns the names a memory resource has for its slot beside
// the one the ledger uses: the service label, and the Id and the Name of
// the resource when they tell slots apart. A log entry or a second
// inventory that calls the slot by one of them means this slot.
func otherNames(o object, name string) []string {
	var out []string
	seen := map[string]bool{model.NormalizeLabel(name): true}
	add := func(s string, numbered bool) {
		k := model.NormalizeLabel(s)
		if k == "" || seen[k] {
			return
		}
		letters, digits := false, false
		for _, r := range k {
			letters = letters || r >= 'A' && r <= 'Z'
			digits = digits || r >= '0' && r <= '9'
		}
		// A bare number could mean anything. An Id or a Name without a
		// number ("DIMM Slot", "Memory") is the same for every module.
		if !letters || numbered && (!digits || len(k) < 3) {
			return
		}
		seen[k] = true
		out = append(out, s)
	}
	if pl := child(child(o, "Location"), "PartLocation"); pl != nil {
		add(str(pl, "ServiceLabel"), false)
	}
	add(str(o, "Id"), true)
	add(str(o, "Name"), true)
	return out
}

// memoryType turns a MemoryDeviceType into the short name the ledger uses:
// "DDR4_SDRAM" and "DDR4" are both DDR4.
func memoryType(t string) string {
	t = strings.TrimSuffix(t, "_SDRAM")
	switch {
	case t == "", t == "OEM", t == "Logical":
		return ""
	case t == "DDR4E":
		return "DDR4"
	case strings.HasPrefix(t, "DDR5"):
		return "DDR5"
	}
	return t
}

func optInt(o object, key string) model.Opt[int] {
	if n, ok := integer(o, key); ok && n > 0 && n < 1<<31 {
		return model.Some(int(n))
	}
	return model.Opt[int]{}
}

// placeInt reads a number of a MemoryLocation. Zero is a place.
func placeInt(o object, key string) model.Opt[int] {
	if n, ok := integer(o, key); ok && n >= 0 && n < 1<<16 {
		return model.Some(int(n))
	}
	return model.Opt[int]{}
}

// memoryPlace reads the socket, channel and slot numbers of a memory
// resource from its MemoryLocation.
func memoryPlace(o object) ([3]int, bool) {
	loc := child(o, "MemoryLocation")
	if loc == nil {
		return [3]int{}, false
	}
	socket, channel, slot := placeInt(loc, "Socket"), placeInt(loc, "Channel"), placeInt(loc, "Slot")
	if !socket.Valid || !channel.Valid || !slot.Valid {
		return [3]int{}, false
	}
	return [3]int{socket.V, channel.V, slot.V}, true
}

// isoWeek turns a ProductionDate into the manufacturing week.
func isoWeek(s string) string {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil && t.Year() >= 1990 {
			y, w := t.ISOWeek()
			return fmt.Sprintf("%04d-W%02d", y, w)
		}
	}
	return ""
}

// module turns a memory resource into an inventory record and, when the
// BMC reports the module as failed or out of use, into the action that
// says so. It reports whether the slot is populated.
func (r *run) module(sink adapter.Sink, res *resource, marks map[string]int) (populated bool, err error) {
	o := res.obj
	name := locator(o)
	if name == "" {
		r.tally.Skip(res.ref, "memory resource names no slot")
		return false, nil
	}
	if id := str(o, "@odata.id"); id != "" {
		r.locators[strings.TrimRight(id, "/")] = name
	}
	status := child(o, "Status")
	state, health := str(status, "State"), str(status, "Health")
	capacity, _ := integer(o, "CapacityMiB")
	serial := str(o, "SerialNumber")
	switch {
	case strings.EqualFold(state, "Absent"):
	case state != "":
		populated = true
	default:
		populated = capacity > 0 || model.UsableSerial(serial)
	}

	m := &model.Module{ObservedAt: r.now, Locator: name, Populated: populated, Source: Kind, RawRef: res.ref, Aliases: otherNames(o, name)}
	if p, ok := memoryPlace(o); ok && !r.shared[p] {
		m.Socket, m.Channel, m.Position = model.Some(p[0]), model.Some(p[1]), model.Some(p[2])
		r.places[p] = name
	}
	if populated {
		d := &m.DIMM
		d.Manufacturer, d.PartNumber, d.Serial = str(o, "Manufacturer"), str(o, "PartNumber"), serial
		if capacity > 0 && capacity < 1<<40 {
			d.SizeBytes = capacity << 20
		}
		d.MemoryType = memoryType(str(o, "MemoryDeviceType"))
		d.ModuleType = strings.ReplaceAll(str(o, "BaseModuleType"), "_", "-")
		if t := str(o, "MemoryType"); t != "" {
			d.Technology = strings.ReplaceAll(t, "_", "-")
		}
		d.ManufacturerID = str(o, "ModuleManufacturerID")
		d.Ranks = optInt(o, "RankCount")
		d.DataWidth, d.TotalWidth = optInt(o, "DataWidthBits"), optInt(o, "BusWidthBits")
		d.ConfiguredSpeedMTs = optInt(o, "OperatingSpeedMhz")
		if speeds, ok := o["AllowedSpeedsMHz"].([]any); ok {
			best := 0
			for i := range speeds {
				if n, ok := integer(object{"v": speeds[i]}, "v"); ok && int(n) > best && n < 1<<31 {
					best = int(n)
				}
			}
			if best > 0 {
				d.SpeedMTs = model.Some(best)
			}
		}
		d.MfgDate = isoWeek(str(o, "ProductionDate"))
	}
	if err := sink.Module(m); err != nil {
		return populated, err
	}
	if !populated {
		return false, nil
	}

	// What the BMC says about the state of the module. A module it took out
	// of use and a module it reports as failed are the facts a finding
	// about firmware marks rests on.
	enabled, hasEnabled := o["Enabled"].(bool)
	disabled := strings.EqualFold(state, "Disabled") || strings.EqualFold(state, "UnavailableOffline") || (hasEnabled && !enabled)
	detail := statusDetail(status, state, health)
	mark := func(kind model.ActionKind, word string) error {
		marks[word]++
		return sink.Action(&model.Action{
			At: r.now, TimeQuality: model.TimeExact, Kind: kind, Method: "bmc", Result: model.ResultOK, Label: name, Detail: detail,
			Source: Kind + "/memory", RawRef: res.ref, Raw: raw(o),
			// One mark per module, whenever it is seen: the state of the
			// same DIMM read again is the same fact.
			Fingerprint: model.Fingerprint(Kind, "mark", string(kind), model.NormalizeLabel(name), strings.ToUpper(serial)),
		})
	}
	if disabled {
		if err := mark(model.ActionDisable, "disabled"); err != nil {
			return true, err
		}
	}
	switch {
	case strings.EqualFold(health, "Critical"):
		if err := mark(model.ActionMarkFailed, "failed"); err != nil {
			return true, err
		}
	case strings.EqualFold(health, "Warning"):
		marks["warning"]++
		e := &model.Event{
			Kind: model.KindError, ErrorDT: r.now, TimeQuality: model.TimeExact, ErrorSeverity: model.SevInformational, Count: 1,
			ErrorType: "module health Warning: " + detail, Label: name, Source: Kind + "/memory", Decoder: "redfish/status",
			RawRef: res.ref, Raw: raw(o),
			Fingerprint: model.Fingerprint(Kind, "warning", model.NormalizeLabel(name), strings.ToUpper(serial)),
		}
		e.Grade = e.ComputeGrade()
		if err := sink.Event(e); err != nil {
			return true, err
		}
	}
	return true, nil
}

// statusDetail writes what the Status object says, with the messages of
// its conditions.
func statusDetail(status object, state, health string) string {
	var parts []string
	if state != "" {
		parts = append(parts, "State "+state)
	}
	if health != "" {
		parts = append(parts, "Health "+health)
	}
	if conds, ok := status["Conditions"].([]any); ok {
		for i, c := range conds {
			co, ok := c.(object)
			if !ok || i >= 3 {
				break
			}
			if msg := str(co, "Message"); msg != "" {
				parts = append(parts, msg)
			} else if id := str(co, "MessageId"); id != "" {
				parts = append(parts, id)
			}
		}
	}
	d := strings.Join(parts, "; ")
	if len(d) > 300 {
		d = d[:300] + "..."
	}
	return d
}
