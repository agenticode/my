package sel

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/agenticode/my/internal/decode/ipmi"
)

// pendingTime is a date and a time as a text dump printed them. They are
// resolved once the whole dump was seen: whether "03/04/2026" is the third
// of April can only be told from the other dates.
type pendingTime struct {
	index      int // of the entry
	date, time string
}

// isHexID reports whether s is a record ID as ipmitool prints it.
func isHexID(s string) bool {
	if len(s) == 0 || len(s) > 4 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if _, ok := hexDigit(s[i]); !ok {
			return false
		}
	}
	return true
}

func hexDigit(c byte) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10, true
	}
	return 0, false
}

// splitListLine splits a line of "ipmitool sel list" into its fields:
//
//	   1 | 11/12/2026 | 03:14:07 | Memory DIMM_A1 | Correctable ECC | Asserted
//	1,11/12/2026,03:14:07,Memory DIMM_A1,Correctable ECC,Asserted          (-c)
//
// ok is false for a line that is not a record.
func splitListLine(l string) (fields []string, ok bool) {
	sep := "|"
	if !strings.Contains(l, "|") {
		if strings.Count(l, ",") < 2 {
			return nil, false
		}
		sep = ","
	}
	fields = strings.Split(l, sep)
	for i := range fields {
		fields[i] = strings.TrimSpace(fields[i])
	}
	if len(fields) < 2 || !isHexID(fields[0]) {
		return nil, false
	}
	return fields, true
}

// normID strips leading zeros from a record ID and folds its case.
func normID(s string) string {
	s = strings.ToLower(strings.TrimLeft(strings.TrimSpace(s), "0"))
	if s == "" {
		return "0"
	}
	return s
}

// splitSensor splits the sensor field of a list line into the sensor type
// and the sensor name or number: "Memory DIMM_A1", "Memory #0x53",
// "Memory". Only the two sensor types this reader acts on are told apart
// from their names; any other field is returned whole as the type.
func splitSensor(field string) (typ, name string, num int) {
	num = -1
	for _, t := range []string{"Memory", "Event Logging Disabled"} {
		if !strings.HasPrefix(field, t) {
			continue
		}
		rest := field[len(t):]
		if rest != "" && rest[0] != ' ' {
			continue
		}
		rest = strings.TrimSpace(rest)
		if h, ok := strings.CutPrefix(rest, "#0x"); ok {
			if v, err := strconv.ParseUint(h, 16, 8); err == nil {
				return t, "", int(v)
			}
		}
		return t, rest, -1
	}
	return field, "", -1
}

// readText parses the text forms.
func (r *reader) readText(text, form string) {
	text = strings.TrimPrefix(text, "\xef\xbb\xbf")
	lines := strings.Split(text, "\n")
	var pend []pendingTime
	switch form {
	case formVerbose:
		pend = r.readVerbose(lines)
	case formFree:
		pend = r.readFree(lines)
	default:
		pend = r.readList(lines)
	}
	dates := make([]string, len(pend))
	for i, p := range pend {
		dates[i] = p.date
	}
	r.clock.loc = r.opts.Location
	r.clock.decide(dates)
	for _, p := range pend {
		e := &r.entries[p.index]
		t, wall, relative, ok := r.clock.parse(p.date, p.time)
		if ok {
			e.hasTime, e.t, e.wall, e.relative = true, t, wall, relative
		}
	}
}

// readList parses "ipmitool sel list" and "sel elist" lines.
func (r *reader) readList(lines []string) (pend []pendingTime) {
	for n, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		f, ok := splitListLine(strings.TrimSpace(line))
		if !ok {
			continue
		}
		e := entry{ref: r.refLine(n + 1), raw: strings.TrimSpace(line), id: normID(f[0]), sensorNum: -1, typeCode: -1}
		switch {
		case strings.HasPrefix(f[1], "Linux kernel panic"):
			e.other = "Linux kernel panic"
		case strings.HasPrefix(f[1], "OEM record"):
			e.other = f[1]
		case len(f) < 4:
			e.other = "incomplete line"
		case strings.HasPrefix(f[3], "OEM record"):
			e.other = f[3]
		default:
			pend = append(pend, pendingTime{index: len(r.entries), date: f[1], time: f[2]})
			e.sensorType, e.sensorName, e.sensorNum = splitSensor(f[3])
			// The description may itself hold the separator. The direction
			// marks where it ends.
			rest := f[4:]
			end := len(rest)
			for i, x := range rest {
				if x == "Asserted" || x == "Deasserted" {
					e.deassert = x == "Deasserted"
					end = i
					break
				}
			}
			sep := " | "
			if !strings.Contains(line, "|") {
				sep = ","
			}
			e.desc = strings.Join(rest[:end], sep)
			if end+1 < len(rest) {
				e.extra = strings.Join(rest[end+1:], sep)
			}
		}
		r.entries = append(r.entries, e)
	}
	return pend
}

// readVerbose parses the blocks "ipmitool sel list -v" prints:
//
//	SEL Record ID          : 0001
//	 Record Type           : 02
//	 Timestamp             : 11/12/2026 03:14:07
//	 Generator ID          : 0033
//	 EvM Revision          : 04
//	 Sensor Type           : Memory
//	 Sensor Number         : 53
//	 Event Type            : Sensor-specific Discrete
//	 Event Direction       : Assertion Event
//	 Event Data            : a0ff01
//	 Description           : Correctable ECC
func (r *reader) readVerbose(lines []string) (pend []pendingTime) {
	type block struct {
		first, last int
		kv          map[string]string
		raw         []string
	}
	var cur *block
	flush := func() {
		if cur == nil {
			return
		}
		b := cur
		cur = nil
		ref := r.refLine(b.first)
		if b.last > b.first {
			ref = fmt.Sprintf("%s#L%d-L%d", r.origin, b.first, b.last)
		}
		e := entry{ref: ref, raw: strings.Join(b.raw, "\n"), id: normID(b.kv["SEL Record ID"]), sensorNum: -1, typeCode: -1}
		rt := b.kv["Record Type"]
		typ, err := strconv.ParseUint(firstField(rt), 16, 8)
		switch {
		case strings.Contains(rt, "kernel panic"):
			e.other = "Linux kernel panic"
		case err != nil:
			e.other = "record of unreadable type"
		case typ != ipmi.RecordSystemEvent:
			e.other = fmt.Sprintf("OEM record %02x", typ)
		}
		if e.other != "" {
			r.entries = append(r.entries, e)
			return
		}
		if ts := b.kv["Timestamp"]; ts != "" {
			d, t, _ := strings.Cut(ts, " ")
			if strings.HasPrefix(ts, "S+") {
				// "S+ 70/001 00:02:03": counted from the start of the BMC.
				d, t = "S+", strings.TrimSpace(strings.TrimPrefix(ts, "S+"))
			}
			pend = append(pend, pendingTime{index: len(r.entries), date: d, time: strings.TrimSpace(t)})
		}
		e.sensorType = b.kv["Sensor Type"]
		e.desc = b.kv["Description"]
		e.deassert = strings.HasPrefix(b.kv["Event Direction"], "Deassert")

		rec := ipmi.Record{Type: ipmi.RecordSystemEvent, Deassert: e.deassert}
		known := true
		switch {
		case sensorTypeIs(e.sensorType, "Memory"):
			rec.SensorType = ipmi.SensorMemory
		case sensorTypeIs(e.sensorType, "Event Logging Disabled"):
			rec.SensorType = ipmi.SensorEventLogging
		default:
			known = false
		}
		if v, err := strconv.ParseUint(b.kv["Sensor Number"], 16, 8); err == nil {
			rec.SensorNumber, e.sensorNum = uint8(v), int(v)
		}
		if v, err := strconv.ParseUint(b.kv["Generator ID"], 16, 16); err == nil {
			rec.GeneratorID = uint16(v)
			e.generator = ipmi.Generator(rec.GeneratorID)
		}
		switch et := b.kv["Event Type"]; {
		case strings.HasPrefix(et, "Sensor-specific"):
			rec.EventType = ipmi.EventTypeSensorSpecific
		case strings.HasPrefix(et, "Threshold"):
			rec.EventType = 0x01
		case strings.HasPrefix(et, "OEM"):
			rec.EventType = 0x70
		case et != "":
			rec.EventType = 0x02 // one of the generic discrete types
		default:
			known = false
		}
		data := b.kv["Event Data (RAW)"]
		if data == "" {
			data = b.kv["Event Data"]
		}
		if _, _, d, err := ipmi.ParseEventData(data); err == nil && len(strings.TrimSpace(data)) == 6 {
			rec.Data = d
		} else {
			known = false
		}
		// The second "Event Data" line of an extended listing is text.
		if x := b.kv["Event Data text"]; x != "" {
			e.extra = x
		}
		if known {
			e.rec, e.typeCode = &rec, int(rec.SensorType)
		}
		r.entries = append(r.entries, e)
	}

	for n, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		k, v, ok := strings.Cut(line, ":")
		key := strings.TrimSpace(k)
		if ok && key == "SEL Record ID" {
			flush()
			cur = &block{first: n + 1, kv: map[string]string{}}
		}
		if cur == nil || !ok {
			continue
		}
		v = strings.TrimSpace(v)
		cur.last = n + 1
		cur.raw = append(cur.raw, strings.TrimSpace(line))
		if key == "Event Data" {
			if _, _, _, err := ipmi.ParseEventData(v); err != nil || len(v) != 6 {
				key = "Event Data text"
			}
		}
		if _, dup := cur.kv[key]; !dup {
			cur.kv[key] = v
		}
	}
	flush()
	return pend
}

func firstField(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// readFree parses the default output of FreeIPMI's ipmi-sel, a table whose
// first line names the columns:
//
//	ID  | Date        | Time     | Name             | Type                     | Event
//	1   | Nov-12-2026 | 03:14:07 | DIMM_A1          | Memory                   | Correctable memory error
func (r *reader) readFree(lines []string) (pend []pendingTime) {
	col := map[string]int{}
	for n, raw := range lines {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if line == "" {
			continue
		}
		f := strings.Split(line, "|")
		for i := range f {
			f[i] = strings.TrimSpace(f[i])
		}
		if len(col) == 0 {
			if f[0] != "ID" {
				continue
			}
			for i, name := range f {
				col[name] = i
			}
			continue
		}
		get := func(name string) string {
			i, ok := col[name]
			if !ok || i >= len(f) {
				return ""
			}
			return f[i]
		}
		id := get("ID")
		if _, err := strconv.ParseUint(id, 10, 32); err != nil {
			continue
		}
		// The table numbers records in decimal.
		v, _ := strconv.ParseUint(id, 10, 32)
		e := entry{ref: r.refLine(n + 1), raw: line, id: strconv.FormatUint(v, 16), sensorNum: -1, typeCode: -1}
		e.sensorType, e.sensorName = get("Type"), get("Name")
		// The event is the last column; text after it belongs to it.
		event := get("Event")
		if i, ok := col["Event"]; ok && i < len(f)-1 {
			event = strings.Join(f[i:], " | ")
		}
		e.desc, e.extra, _ = strings.Cut(event, " ; ")
		e.desc, e.extra = strings.TrimSpace(e.desc), strings.TrimSpace(e.extra)
		e.deassert = strings.Contains(event, "Deassertion")
		pend = append(pend, pendingTime{index: len(r.entries), date: get("Date"), time: get("Time")})
		r.entries = append(r.entries, e)
	}
	return pend
}
