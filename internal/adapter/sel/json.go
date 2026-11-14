package sel

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/agenticode/my/internal/decode/ipmi"
)

// A SEL exported as JSON has no standard shape. This reader takes a list
// of objects, at the top level or under one of a few common keys, and
// reads each object by the names tools give the fields of a SEL record.
// Keys are compared without case, spaces, underscores and dashes.
var (
	jsonListKeys = []string{"entries", "records", "sel", "events", "items", "members", "log", "data"}

	jsonID        = []string{"id", "recordid", "selrecordid", "record", "index"}
	jsonTime      = []string{"timestamp", "datetime", "created", "eventtimestamp"}
	jsonDate      = []string{"date"}
	jsonClock     = []string{"time"}
	jsonType      = []string{"sensortype", "type"}
	jsonSensor    = []string{"sensor", "sensorname", "name", "sensorid"}
	jsonNumber    = []string{"sensornumber", "sensornum"}
	jsonDesc      = []string{"event", "description", "eventdescription", "message", "desc"}
	jsonDirection = []string{"direction", "eventdirection", "state", "entrycode", "assertion"}
	jsonAsserted  = []string{"asserted"}
	jsonData      = []string{"eventdata", "eventdataraw", "messageid"}
	jsonRaw       = []string{"raw", "rawrecord", "hex"}
	jsonGenerator = []string{"generatorid", "generator"}
)

func foldKey(k string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(k) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// object is a JSON object with folded keys.
type object map[string]json.RawMessage

func fold(m map[string]json.RawMessage) object {
	o := object{}
	for k, v := range m {
		if fk := foldKey(k); fk != "" {
			if _, dup := o[fk]; !dup {
				o[fk] = v
			}
		}
	}
	return o
}

// text returns the first of the keys that holds a string or a number.
func (o object) text(keys []string) (string, bool) {
	for _, k := range keys {
		v, ok := o[k]
		if !ok {
			continue
		}
		var s string
		if json.Unmarshal(v, &s) == nil {
			return strings.TrimSpace(s), true
		}
		var n json.Number
		d := json.NewDecoder(bytes.NewReader(v))
		d.UseNumber()
		if d.Decode(&n) == nil {
			return n.String(), true
		}
	}
	return "", false
}

func (o object) boolean(keys []string) (val, ok bool) {
	for _, k := range keys {
		if v, has := o[k]; has {
			var b bool
			if json.Unmarshal(v, &b) == nil {
				return b, true
			}
		}
	}
	return false, false
}

// readJSON parses a JSON list of SEL entries.
func (r *reader) readJSON(data []byte) error {
	data = bytes.TrimPrefix(bytes.TrimSpace(data), []byte("\xef\xbb\xbf"))
	var list []json.RawMessage
	if err := json.Unmarshal(data, &list); err != nil {
		var top map[string]json.RawMessage
		if err2 := json.Unmarshal(data, &top); err2 != nil {
			return err2
		}
		o := fold(top)
		found := false
		for _, k := range jsonListKeys {
			if v, ok := o[k]; ok && json.Unmarshal(v, &list) == nil {
				found = true
				break
			}
		}
		if !found {
			// One entry on its own.
			if _, isEntry := o.text(jsonDesc); !isEntry {
				if _, isRaw := o.text(jsonRaw); !isRaw {
					return errNoRecords
				}
			}
			list = []json.RawMessage{data}
		}
	}
	r.clock.loc = r.opts.Location
	var pend []pendingTime
	for i, item := range list {
		ref := fmt.Sprintf("%s#entry %d", r.origin, i+1)
		raw := string(item)
		if len(raw) > 4096 {
			raw = raw[:4096] + "..."
		}
		e := entry{ref: ref, raw: raw, sensorNum: -1, typeCode: -1}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(item, &m); err != nil {
			e.other = "entry that is not an object"
			r.entries = append(r.entries, e)
			continue
		}
		o := fold(m)

		// A whole record as 16 bytes of hexadecimal.
		if s, ok := o.text(jsonRaw); ok {
			if b, err := hex.DecodeString(stripHex(s)); err == nil && len(b) == ipmi.RecordSize {
				before := len(r.entries)
				r.readRaw(b)
				x := &r.entries[before]
				x.ref, x.raw = ref, raw
				continue
			}
		}

		if id, ok := o.text(jsonID); ok {
			e.id = normID(strings.TrimPrefix(strings.ToLower(id), "0x"))
			if v, err := strconv.ParseUint(id, 10, 32); err == nil && !strings.HasPrefix(strings.ToLower(id), "0x") {
				e.id = strconv.FormatUint(v, 16) // a plain number is decimal
			}
		}
		e.sensorType, _ = o.text(jsonType)
		e.sensorName, _ = o.text(jsonSensor)
		e.desc, _ = o.text(jsonDesc)
		if n, ok := o.text(jsonNumber); ok {
			if v, err := strconv.ParseUint(n, 0, 8); err == nil {
				e.sensorNum = int(v)
			}
		}
		if d, ok := o.text(jsonDirection); ok {
			e.deassert = strings.HasPrefix(strings.ToLower(d), "deassert")
		}
		if a, ok := o.boolean(jsonAsserted); ok {
			e.deassert = !a
		}
		if g, ok := o.text(jsonGenerator); ok {
			if v, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(g), "0x"), 16, 16); err == nil {
				e.generator = ipmi.Generator(uint16(v))
			}
		}
		// The sensor type may be given as its code.
		if v, err := strconv.ParseUint(e.sensorType, 0, 8); err == nil {
			e.typeCode, e.sensorType = int(v), ""
		}
		if d, ok := o.text(jsonData); ok {
			if et, deassert, bytes3, err := ipmi.ParseEventData(d); err == nil {
				code := e.typeCode
				switch {
				case sensorTypeIs(e.sensorType, "Memory"):
					code = ipmi.SensorMemory
				case sensorTypeIs(e.sensorType, "Event Logging Disabled"):
					code = ipmi.SensorEventLogging
				}
				if code >= 0 {
					rec := ipmi.Record{Type: ipmi.RecordSystemEvent, SensorType: uint8(code), EventType: et, Deassert: deassert || e.deassert, Data: bytes3}
					if e.sensorNum >= 0 {
						rec.SensorNumber = uint8(e.sensorNum)
					}
					e.rec, e.typeCode, e.deassert = &rec, code, rec.Deassert
				}
			}
		}

		if ts, ok := o.text(jsonTime); ok {
			if t, okT := parseJSONTime(ts); okT {
				e.hasTime, e.t, e.wall = true, t, t.Format("20060102150405")
				e.relative = t.Unix() < ipmi.TimeInitDone
			}
		} else if d, okD := o.text(jsonDate); okD {
			c, _ := o.text(jsonClock)
			pend = append(pend, pendingTime{index: len(r.entries), date: d, time: c})
		} else if c, okC := o.text(jsonClock); okC {
			// "time" alone holds the whole timestamp.
			if t, okT := parseJSONTime(c); okT {
				e.hasTime, e.t, e.wall = true, t, t.Format("20060102150405")
				e.relative = t.Unix() < ipmi.TimeInitDone
			}
		}
		r.entries = append(r.entries, e)
	}
	dates := make([]string, len(pend))
	for i, p := range pend {
		dates[i] = p.date
	}
	r.clock.decide(dates)
	for _, p := range pend {
		e := &r.entries[p.index]
		if t, wall, relative, ok := r.clock.parse(p.date, p.time); ok {
			e.hasTime, e.t, e.wall, e.relative = true, t, wall, relative
		}
	}
	return nil
}

// stripHex removes what tools put between the bytes of a hex dump.
func stripHex(s string) string {
	var b strings.Builder
	low := strings.ToLower(s)
	for i := 0; i < len(low); i++ {
		c := low[i]
		if c == '0' && i+1 < len(low) && low[i+1] == 'x' {
			i++
			continue
		}
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'f' {
			b.WriteByte(c)
		}
	}
	return b.String()
}

var jsonTimeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05Z07:00",
	"2006-01-02 15:04:05",
	"01/02/2006 15:04:05",
}

// parseJSONTime reads a timestamp given as one value: a time in one of the
// usual written forms, or seconds since 1970 (milliseconds for a number too
// large to be seconds). A time without a zone is read as UTC.
func parseJSONTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		switch {
		case n < 0:
			return time.Time{}, false
		case n > 1e11:
			n /= 1000
		}
		if n >= ipmi.TimeUnspecified {
			return time.Time{}, false
		}
		return time.Unix(n, 0).UTC(), true
	}
	for _, layout := range jsonTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			if y := t.UTC().Year(); y < 1970 || y > 9999 {
				return time.Time{}, false
			}
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}
