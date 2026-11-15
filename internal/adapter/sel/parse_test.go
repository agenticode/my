package sel

import (
	"reflect"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/decode/ipmi"
	"github.com/agenticode/my/internal/model"
)

func TestClockParse(t *testing.T) {
	kst := time.FixedZone("KST", 9*3600)
	for _, c := range []struct {
		date, clock string
		dayFirst    bool
		loc         *time.Location
		want        string // UTC, "" when the time is not readable
		relative    bool
	}{
		{"11/12/2026", "03:14:07", false, nil, "2026-11-12 03:14:07", false},
		{"11/12/2026", "03:14:07", true, nil, "2026-12-11 03:14:07", false},
		{"11/12/26", "03:14:07", false, nil, "2026-11-12 03:14:07", false},
		{"11/12/99", "03:14:07", false, nil, "1999-11-12 03:14:07", false},
		{"2026-11-12", "03:14:07", false, nil, "2026-11-12 03:14:07", false},
		{"2026/11/12", "03:14:07", false, nil, "2026-11-12 03:14:07", false},
		{"12.11.2026", "03:14:07", false, nil, "2026-11-12 03:14:07", false},
		{"Nov-12-2026", "03:14:07", false, nil, "2026-11-12 03:14:07", false},
		{"11/12/2026", "03:14:07 PM", false, nil, "2026-11-12 15:14:07", false},
		{"11/12/2026", "12:14:07 AM", false, nil, "2026-11-12 00:14:07", false},
		{"11/12/2026", "12:14:07 PM", false, nil, "2026-11-12 12:14:07", false},
		{"11/12/2026", "12:14:07 KST", false, nil, "2026-11-12 03:14:07", false},
		{"11/12/2026", "03:14:07 UTC", false, nil, "2026-11-12 03:14:07", false},
		{"11/12/2026", "03:14:07 PM PDT", false, nil, "2026-11-12 22:14:07", false},
		{"11/12/2026", "07:14:07 +04", false, nil, "2026-11-12 03:14:07", false},
		{"11/12/2026", "23:44:07 -0330", false, nil, "2026-11-13 03:14:07", false},
		{"11/12/2026", "03:14:07 XYZ", false, nil, "2026-11-12 03:14:07", false}, // unknown zone: as printed
		{"11/12/2026", "12:14:07", false, kst, "2026-11-12 03:14:07", false},
		{"11/12/2026", "12:14:07 UTC", false, kst, "2026-11-12 03:14:07", false}, // the caller's zone wins
		{"02/29/2028", "00:00:00", false, nil, "2028-02-29 00:00:00", false},
		{"Pre-Init", "0000000042", false, nil, "1970-01-01 00:00:42", true},
		{"S+", "70/001 00:02:03", false, nil, "1970-01-01 00:02:03", true},
		{"S+", "70/002 00:00:01 UTC", false, nil, "1970-01-02 00:00:01", true},

		{"02/29/2026", "00:00:00", false, nil, "", false}, // no such day
		{"13/12/2026", "03:14:07", false, nil, "", false},
		{"11/12/2026", "25:14:07", false, nil, "", false},
		{"11/12/2026", "13:14:07 PM", false, nil, "", false},
		{"11/12/2026", "03:14", false, nil, "", false},
		{"11/12/2026", "", false, nil, "", false},
		{"11-12-2026", "03:14:07", false, nil, "", false},
		{"11/12/1969", "03:14:07", false, nil, "", false},
		{"Unspecified", "Unspecified", false, nil, "", false},
		{"PostInit", "PostInit", false, nil, "", false},
		{"Pre-Init", "notanumber", false, nil, "", false},
		{"Pre-Init", "0999999999", false, nil, "", false}, // not below the limit of BMC-relative times
		{"S+", "99/001 00:02:03", false, nil, "", false},
		{"", "", false, nil, "", false},
	} {
		ck := clock{dayFirst: c.dayFirst, loc: c.loc}
		got, wall, relative, ok := ck.parse(c.date, c.clock)
		text := ""
		if ok {
			text = got.UTC().Format("2006-01-02 15:04:05")
		}
		if text != c.want || relative != c.relative || (ok && wall == "") {
			t.Errorf("parse(%q, %q) = %q, relative %v, wall %q; want %q, relative %v", c.date, c.clock, text, relative, wall, c.want, c.relative)
		}
	}
}

func TestClockDecide(t *testing.T) {
	for _, c := range []struct {
		dates               []string
		dayFirst, ambiguous bool
	}{
		{[]string{"11/12/2026", "10/28/2026"}, false, false},
		{[]string{"11/12/2026", "28/10/2026"}, true, false},
		{[]string{"11/12/2026", "01/02/2026"}, false, true},
		{[]string{"13/01/2026", "01/13/2026"}, false, false}, // both orders: month first, the dump is mixed up
		{[]string{"2026-11-12", "Nov-12-2026", "Pre-Init"}, false, false},
		{nil, false, false},
	} {
		var ck clock
		ck.decide(c.dates)
		if ck.dayFirst != c.dayFirst || ck.ambiguous != c.ambiguous {
			t.Errorf("%v: day first %v, ambiguous %v; want %v, %v", c.dates, ck.dayFirst, ck.ambiguous, c.dayFirst, c.ambiguous)
		}
	}
}

func TestSensorLabel(t *testing.T) {
	for name, want := range map[string]string{
		"DIMM_A1":           "DIMM_A1",
		"DIMMA1":            "DIMMA1",
		"DIMM000":           "DIMM000",
		"CPU1_DIMM_A1":      "CPU1_DIMM_A1",
		"P1-DIMMA1":         "P1-DIMMA1",
		"DIMM_E1_Status":    "DIMM_E1",
		"CPU0_DIMM0 Status": "CPU0_DIMM0",
		"Mem DIMM_A1":       "DIMM_A1",
		"DIMM.Socket.A1":    "DIMM.Socket.A1",
		"DIMM 3":            "DIMM 3",
		"CPU 1 DIMM 3":      "CPU 1 DIMM 3",
		"PROC 2 DIMM 12":    "PROC 2 DIMM 12",
		"Board DIMM 3":      "Board DIMM 3",
		// Sensors that stand for all modules, or for something else.
		"":                "",
		"Memory":          "",
		"Mem ECC Warning": "",
		"ECC Corr Err":    "",
		"DIMM":            "",
		"DIMM Hot":        "",
		"DIMM Temp":       "",
		"SEL":             "",
		"DIMM_A1 DIMM_A2": "",
		"CPU1 Temp":       "",
	} {
		if got := sensorLabel(name); got != want {
			t.Errorf("sensorLabel(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestLabels(t *testing.T) {
	std := &ipmi.Record{Type: ipmi.RecordSystemEvent, SensorType: ipmi.SensorMemory, SensorNumber: 0x53, EventType: ipmi.EventTypeSensorSpecific, Data: [3]byte{0x30, 0xff, 4}}
	oem := &ipmi.Record{Type: ipmi.RecordSystemEvent, SensorType: ipmi.SensorMemory, SensorNumber: 0x53, EventType: ipmi.EventTypeSensorSpecific, Data: [3]byte{0xa0, 0xff, 4}}
	var r reader
	for _, c := range []struct {
		name string
		e    entry
		rest string
		want []string
	}{
		{"vendor text, one board style", entry{sensorName: ""}, "(@DIMMB1(CPU1))", []string{"P1-DIMMB1"}},
		{"vendor text wins over the sensor name", entry{sensorName: "DIMM_Z9"}, "(@DIMMA12(CPU2))", []string{"P2-DIMMA12"}},
		{"module in parentheses", entry{}, "( DIMMA1)", []string{"DIMMA1"}},
		{"two modules", entry{}, "( DIMMA1,DIMMA2)", []string{"DIMMA1", "DIMMA2"}},
		{"the same module twice", entry{}, "(DIMM_A1, dimm_a1)", []string{"DIMM_A1"}},
		{"module after a dash", entry{}, "- DIMM_B3.", []string{"DIMM_B3"}},
		{"number apart", entry{}, "on DIMM 7", []string{"DIMM 7"}},
		{"field of its own", entry{extra: "CPU 1 DIMM 3"}, "", []string{"CPU 1 DIMM 3"}},
		{"a reading is not a module", entry{sensorName: "CPU0 Temp", extra: "Reading 96 > Threshold 95 degrees C"}, "", nil},
		{"sensor per module", entry{sensorName: "DIMM_C1"}, "", []string{"DIMM_C1"}},
		{"sensor per module wins over event data", entry{sensorName: "DIMM_C1", rec: std}, "", []string{"DIMM_C1"}},
		{"event data 3 with a shared sensor name", entry{sensorName: "Mem ECC", rec: std}, "", []string{"Mem ECC module 4"}},
		{"event data 3 with a sensor number", entry{rec: std}, "", []string{"sensor 0x53 module 4"}},
		{"vendor bytes are not a module", entry{rec: oem}, "", nil},
		{"nothing names a module", entry{sensorName: "Memory"}, "", nil},
	} {
		if got := r.labels(&c.e, c.rest); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: labels = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSplitSensor(t *testing.T) {
	for field, want := range map[string]struct {
		typ, name string
		num       int
	}{
		"Memory DIMM_A1":                   {"Memory", "DIMM_A1", -1},
		"Memory #0x53":                     {"Memory", "", 0x53},
		"Memory":                           {"Memory", "", -1},
		"Memory Mem ECC Warning":           {"Memory", "Mem ECC Warning", -1},
		"Event Logging Disabled SEL":       {"Event Logging Disabled", "SEL", -1},
		"Event Logging Disabled #0x72":     {"Event Logging Disabled", "", 0x72},
		"Temperature CPU0 Temp":            {"Temperature CPU0 Temp", "", -1},
		"MemoryX DIMM_A1":                  {"MemoryX DIMM_A1", "", -1},
		"POST Memory Resize #0x01":         {"POST Memory Resize #0x01", "", -1},
		"Memory #0xZZ":                     {"Memory", "#0xZZ", -1},
		"":                                 {"", "", -1},
		"Event Logging Disabled":           {"Event Logging Disabled", "", -1},
		"Memory  DIMM_A1":                  {"Memory", "DIMM_A1", -1},
		"Critical Interrupt #0x91":         {"Critical Interrupt #0x91", "", -1},
		"Memory DIMM #0x10":                {"Memory", "DIMM #0x10", -1},
		"Event Logging Disabled Something": {"Event Logging Disabled", "Something", -1},
	} {
		typ, name, num := splitSensor(field)
		if typ != want.typ || name != want.name || num != want.num {
			t.Errorf("splitSensor(%q) = %q, %q, %d; want %q, %q, %d", field, typ, name, num, want.typ, want.name, want.num)
		}
	}
}

func TestParseJSONTime(t *testing.T) {
	for in, want := range map[string]string{
		"2026-11-12T03:14:07Z":       "2026-11-12 03:14:07",
		"2026-11-12T12:14:07+09:00":  "2026-11-12 03:14:07",
		"2026-11-12T03:14:07.123456": "2026-11-12 03:14:07",
		"2026-11-12 03:14:07":        "2026-11-12 03:14:07",
		"11/12/2026 03:14:07":        "2026-11-12 03:14:07",
		"1794453247":                 "2026-11-12 03:14:07",
		"1794453247000":              "2026-11-12 03:14:07", // milliseconds
		"42":                         "1970-01-01 00:00:42",
		"-5":                         "",
		"4294967295":                 "", // the value of a time that was not recorded
		"yesterday":                  "",
		"":                           "",
		"0001-01-01T00:00:00Z":       "",
	} {
		got, ok := parseJSONTime(in)
		text := ""
		if ok {
			text = got.Format("2006-01-02 15:04:05")
		}
		if text != want {
			t.Errorf("parseJSONTime(%q) = %q, want %q", in, text, want)
		}
	}
}

// A record from before the BMC had its clock set must not be given a date.
func TestRelativeTimesStayRelative(t *testing.T) {
	c, _ := collect(t, "ipmitool-elist.txt", Options{Location: time.FixedZone("KST", 9*3600)})
	var rel *model.Event
	for _, e := range c.Events {
		if e.TimeQuality == model.TimeRelative {
			rel = e
		}
	}
	if rel == nil || !rel.ErrorDT.Equal(time.Unix(42, 0)) {
		t.Errorf("relative event = %+v", rel)
	}
	var _ adapter.Adapter = NewFile("x", Options{})
}
