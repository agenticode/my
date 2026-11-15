package fmfm

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
)

func TestParseTime(t *testing.T) {
	kst := time.FixedZone("KST", 9*3600)
	for _, c := range []struct {
		in   any
		loc  *time.Location
		want string
	}{
		{"2026:11:12:03:14:07:123", nil, "2026-11-12 03:14:07.123"},
		{"2026:11:12:03:14:07", nil, "2026-11-12 03:14:07.000"},
		{"2026:11:12:12:14:07:123", kst, "2026-11-12 03:14:07.123"},
		{"2026-11-12T03:14:07Z", kst, "2026-11-12 03:14:07.000"},
		{"2026-11-12T12:14:07.5+09:00", nil, "2026-11-12 03:14:07.500"},
		{"2026-11-12T03:14:07.123456", nil, "2026-11-12 03:14:07.123"},
		{"2026-11-12 03:14:07", nil, "2026-11-12 03:14:07.000"},
		{"2026/11/12 03:14:07", nil, "2026-11-12 03:14:07.000"},
		{json.Number("1794453247"), nil, "2026-11-12 03:14:07.000"},
		{json.Number("1794453247123"), nil, "2026-11-12 03:14:07.123"},
		{"1794453247", kst, "2026-11-12 03:14:07.000"},

		{"2026:02:30:00:00:00:000", nil, ""}, // no such day
		{"2026:13:01:00:00:00:000", nil, ""},
		{"2026:11:12:24:00:00:000", nil, ""},
		{"2026:11:12:03:14", nil, ""},
		{"1969:12:31:23:59:59:999", nil, ""},
		{"0001-01-01T00:00:00Z", nil, ""},
		{json.Number("0"), nil, ""},
		{json.Number("-5"), nil, ""},
		{"yesterday", nil, ""},
		{"", nil, ""},
		{nil, nil, ""},
		{true, nil, ""},
	} {
		got, ok := parseTime(c.in, c.loc)
		text := ""
		if ok {
			text = got.Format("2006-01-02 15:04:05.000")
		}
		if text != c.want {
			t.Errorf("parseTime(%v) = %q, want %q", c.in, text, c.want)
		}
	}
}

func TestNumbersAndWords(t *testing.T) {
	for in, want := range map[any]model.Opt[int]{
		json.Number("7"): some(7), json.Number("0"): some(0), "0x1a2f": some(0x1a2f), "6703": some(6703), json.Number("7.0"): some(7),
		json.Number("7.5"): {}, json.Number("-1"): {}, "": {}, "seven": {}, json.Number("99999999999"): {}, true: {},
	} {
		if got := small(in); got != want {
			t.Errorf("small(%v) = %v, want %v", in, got, want)
		}
	}
	if n, ok := number("0xfedcba9876543210"); !ok || n != 0xfedcba9876543210 {
		t.Errorf("a 64-bit address = %#x, %v", n, ok)
	}
	for in, want := range map[string]model.Severity{
		"CE": model.SevCE, "ce": model.SevCE, "Corrected": model.SevCE, "UE": model.SevUE, "Uncorrectable": model.SevUE,
		"Recoverable": model.SevRecoverable, "Fatal": model.SevFatal, "Informational": model.SevInformational, " fatal ": model.SevFatal,
	} {
		if got, ok := severity(in); !ok || got != want {
			t.Errorf("severity(%q) = %q, %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "Critical", "Corrected?", "3"} {
		if got, ok := severity(in); ok {
			t.Errorf("severity(%q) = %q", in, got)
		}
	}
	for in, want := range map[string]model.ScrubType{
		"Demand": model.ScrubDemand, "Demand read error": model.ScrubDemand, "patrol": model.ScrubPatrol, "Patrol scrub error": model.ScrubPatrol,
		"Other": model.ScrubOther, "write": model.ScrubOther, "": "",
	} {
		if got := scrubType(in); got != want {
			t.Errorf("scrubType(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[any]bool{
		true: true, false: false, nil: false, json.Number("1"): true, json.Number("0"): false, "": false, "no": false, "None": false,
		"yes": true, "Command Address Parity": true,
	} {
		if got, _ := truthy(in); got != want {
			t.Errorf("truthy(%v) = %v", in, got)
		}
	}
}

func parse(t *testing.T, doc string) any {
	t.Helper()
	d := json.NewDecoder(strings.NewReader(doc))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestECSForms(t *testing.T) {
	var none model.Opt[int]
	full := [7]model.Opt[int]{some(0x20), some(3), some(0x2f), some(0x1a), some(0x0c), some(0), some(2)}
	for name, c := range map[string]struct {
		doc     string
		devices []model.Opt[int]
		regs    [][7]model.Opt[int]
	}{
		"by name":             {`{"MR14": 32, "MR15": "0x03", "MR16": "2f", "MR17": 26, "MR18": 12, "MR19": 0, "MR20": 2}`, []model.Opt[int]{some(5)}, [][7]model.Opt[int]{full}},
		"by name, other case": {`{"mr14": 32, "Mr15": 3, "ModeRegister16": 47, "MR_17": 26, "mr-18": 12, "MR19": 0, "MR20": 2}`, []model.Opt[int]{some(5)}, [][7]model.Opt[int]{full}},
		"in order":            {`[32, 3, 47, 26, 12, 0, 2]`, []model.Opt[int]{some(5)}, [][7]model.Opt[int]{full}},
		"bytes as text":       {`"20032f1a0c0002"`, []model.Opt[int]{some(5)}, [][7]model.Opt[int]{full}},
		"bytes with prefixes": {`"0x20,0x03,0x2f,0x1a,0x0c,0x00,0x02"`, []model.Opt[int]{some(5)}, [][7]model.Opt[int]{full}},
		"two of seven":        {`{"MR19": 1, "MR20": 4, "MR21": 9, "Comment": "x"}`, []model.Opt[int]{some(5)}, [][7]model.Opt[int]{{none, none, none, none, none, some(1), some(4)}}},
		"per device": {`[{"DeviceId": 3, "MR20": 1}, {"Device": 9, "Registers": [32, 3, 47, 26, 12, 0, 2]}, {"MR20": 0}]`,
			[]model.Opt[int]{some(3), some(9), some(2)}, [][7]model.Opt[int]{{none, none, none, none, none, none, some(1)}, full, {none, none, none, none, none, none, some(0)}}},
		"by device number": {`{"dev7": {"MR20": 8}, "0": "20032f1a0c0002"}`, []model.Opt[int]{some(0), some(7)},
			[][7]model.Opt[int]{full, {none, none, none, none, none, none, some(8)}}},
	} {
		got, ok := ecsReadings(parse(t, c.doc), some(5))
		if !ok || len(got) != len(c.regs) {
			t.Errorf("%s: %d readings, %v", name, len(got), ok)
			continue
		}
		for i, s := range got {
			if s.Registers() != c.regs[i] || s.DeviceId != c.devices[i] {
				t.Errorf("%s: reading %d = device %v, %v; want device %v, %v", name, i, s.DeviceId, s.Registers(), c.devices[i], c.regs[i])
			}
		}
	}
	for name, doc := range map[string]string{
		"six values":        `[32, 3, 47, 26, 12, 0]`,
		"six bytes":         `"20032f1a0c00"`,
		"a value above 255": `{"MR20": 256}`,
		"no register":       `{"Threshold": 256}`,
		"text":              `"not registers"`,
		"number":            `7`,
		"empty list":        `[]`,
		"list of text":      `["a", "b"]`,
		"device unreadable": `{"first": {"MR20": 1}}`,
		"null":              `null`,
	} {
		if got, ok := ecsReadings(parse(t, doc), some(5)); ok {
			t.Errorf("%s: read as %d readings", name, len(got))
		}
	}
}

func TestFlattenPrefersTheRecordsOwnFields(t *testing.T) {
	v := parse(t, `{"ErrorDT": "2026-11-10T08:00:00Z", "Rank": 1, "Location": {"Rank": 0, "Row": 5, "Deeper": {"Column": 9}}, "List": [{"Row": 77}]}`)
	flat := map[string]any{}
	flatten(folded(v.(map[string]any)), flat, 0)
	if small(flat["rank"]) != some(1) || small(flat["row"]) != some(5) || small(flat["column"]) != some(9) {
		t.Errorf("flat = %v", flat)
	}
	if _, has := flat["list"]; has {
		t.Error("a list was flattened into the record")
	}
}

func TestSuffixField(t *testing.T) {
	o := object{"dimmsupplier": "Example Memory", "dimmpartnumber": "EXM64", "moduleserialnumber": "80A1C3", "count": json.Number("2")}
	if got := suffixField(o, "supplier", "manufacturer"); got != "Example Memory" {
		t.Errorf("supplier = %q", got)
	}
	if got := suffixField(o, "serialnumber", "serial"); got != "80A1C3" {
		t.Errorf("serial = %q", got)
	}
	if got := suffixField(o, "locator"); got != "" {
		t.Errorf("locator = %q", got)
	}
	var _ adapter.Adapter = NewFile("x", Options{})
	_ = reflect.DeepEqual
}
