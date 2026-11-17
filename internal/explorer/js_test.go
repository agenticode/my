package explorer

import (
	"encoding/json"
	"math"
	"os"
	"reflect"
	"strconv"
	"testing"
)

// The tables in testdata/js.json were taken from node (v26): what
// JavaScript gives for each input. The helpers must give the same.
type jsTables struct {
	Whitespace []int       `json:"whitespace"`
	Trim       []int       `json:"trim"`
	ParseInt   [][2]string `json:"parseInt"`
	ParseFloat [][2]string `json:"parseFloat"`
	ToFixed    [][3]any    `json:"toFixed"`
	Round      [][2]string `json:"round"`
	String     [][2]string `json:"string"`
	Log        [][3]string `json:"log"`
	Mod        [][3]string `json:"mod"`
	TrimCases  [][3]string `json:"trimCases"`
	Split      []([2]any)  `json:"split"`
	Regex      []regexCase `json:"regex"`
}

type regexCase struct {
	Src    string    `json:"src"`
	Flags  string    `json:"flags"`
	Input  string    `json:"input"`
	Groups []*string `json:"groups"`
}

func loadTables(t *testing.T) jsTables {
	t.Helper()
	b, err := os.ReadFile("testdata/js.json")
	if err != nil {
		t.Fatal(err)
	}
	var tb jsTables
	if err := json.Unmarshal(b, &tb); err != nil {
		t.Fatal(err)
	}
	return tb
}

// jsNum reads a number as the tables write it.
func jsNum(t *testing.T, s string) float64 {
	switch s {
	case "NaN":
		return math.NaN()
	case "Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	case "-0":
		return math.Copysign(0, -1)
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("table number %q: %v", s, err)
	}
	return f
}

func same(a, b float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.IsNaN(a) && math.IsNaN(b)
	}
	return a == b && math.Signbit(a) == math.Signbit(b)
}

func TestWhiteSpaceSet(t *testing.T) {
	tb := loadTables(t)
	want := map[int]bool{}
	for _, c := range tb.Whitespace {
		want[c] = true
	}
	trim := map[int]bool{}
	for _, c := range tb.Trim {
		trim[c] = true
	}
	for c := 0; c <= 0xFFFF; c++ {
		if c >= 0xD800 && c <= 0xDFFF {
			continue
		}
		if isJSSpace(rune(c)) != want[c] {
			t.Errorf("U+%04X: isJSSpace %v, /\\s/ of JavaScript %v", c, isJSSpace(rune(c)), want[c])
		}
		if want[c] != trim[c] {
			t.Errorf("U+%04X: \\s and trim() differ in the table", c)
		}
	}
	re := jsre(`\s`, "")
	for c := 0; c <= 0xFFFF; c++ {
		if c >= 0xD800 && c <= 0xDFFF {
			continue
		}
		if re.Test(string(rune(c))) != want[c] {
			t.Errorf("U+%04X: translated \\s gives %v", c, !want[c])
		}
	}
}

func TestParseIntTable(t *testing.T) {
	for _, c := range loadTables(t).ParseInt {
		if got, want := float64(ParseInt(c[0])), jsNum(t, c[1]); !same(got, want) {
			t.Errorf("parseInt(%q) = %v, JavaScript %v", c[0], got, want)
		}
	}
}

func TestParseFloatTable(t *testing.T) {
	for _, c := range loadTables(t).ParseFloat {
		if got, want := float64(ParseFloat(c[0])), jsNum(t, c[1]); !same(got, want) {
			t.Errorf("parseFloat(%q) = %v, JavaScript %v", c[0], got, want)
		}
	}
}

func TestToFixedTable(t *testing.T) {
	for _, c := range loadTables(t).ToFixed {
		x := jsNum(t, c[0].(string))
		d := int(c[1].(float64))
		if got := ToFixed(x, d); got != c[2].(string) {
			t.Errorf("(%v).toFixed(%d) = %q, JavaScript %q", x, d, got, c[2])
		}
	}
}

func TestRoundTable(t *testing.T) {
	for _, c := range loadTables(t).Round {
		x := jsNum(t, c[0])
		if got, want := Round(x), jsNum(t, c[1]); !same(got, want) {
			t.Errorf("Math.round(%v) = %v, JavaScript %v", x, got, want)
		}
	}
}

func TestNumStringTable(t *testing.T) {
	for _, c := range loadTables(t).String {
		x := jsNum(t, c[0])
		if got := NumString(x); got != c[1] {
			t.Errorf("String(%v) = %q, JavaScript %q", x, got, c[1])
		}
	}
}

func TestLogTable(t *testing.T) {
	for _, c := range loadTables(t).Log {
		x := jsNum(t, c[0])
		if got, want := Log(x), jsNum(t, c[1]); !same(got, want) {
			t.Errorf("Math.log(%v) = %v, JavaScript %v", x, got, want)
		}
		if got, want := math.Floor(Log(x)/Log(1024)), jsNum(t, c[2]); !same(got, want) {
			t.Errorf("Math.floor(Math.log(%v) / Math.log(1024)) = %v, JavaScript %v", x, got, want)
		}
	}
}

func TestModTable(t *testing.T) {
	for _, c := range loadTables(t).Mod {
		a, b := jsNum(t, c[0]), jsNum(t, c[1])
		if got, want := Mod(a, b), jsNum(t, c[2]); !same(got, want) {
			t.Errorf("%v %% %v = %v, JavaScript %v", a, b, got, want)
		}
	}
}

func TestTrimAndSplitTable(t *testing.T) {
	tb := loadTables(t)
	for _, c := range tb.TrimCases {
		if got := Trim(c[0]); got != c[1] {
			t.Errorf("%q.trim() = %q, JavaScript %q", c[0], got, c[1])
		}
		if got := TrimStart(c[0]); got != c[2] {
			t.Errorf("%q.trimStart() = %q, JavaScript %q", c[0], got, c[2])
		}
	}
	for _, c := range tb.Split {
		var want []string
		for _, p := range c[1].([]any) {
			want = append(want, p.(string))
		}
		if got := SplitSpace(c[0].(string)); !reflect.DeepEqual(got, want) {
			t.Errorf("%q.split(/\\s+/) = %q, JavaScript %q", c[0], got, want)
		}
	}
	if got := SplitSpace(""); len(got) != 1 || got[0] != "" {
		t.Errorf(`"".split(/\s+/) = %q, JavaScript [""]`, got)
	}
}

func TestRegexTable(t *testing.T) {
	for _, c := range loadTables(t).Regex {
		re := jsre(c.Src, c.Flags)
		m, ok := re.Match(c.Input)
		if ok != (c.Groups != nil) {
			t.Errorf("/%s/%s on %q: matched %v, JavaScript %v", c.Src, c.Flags, c.Input, ok, c.Groups != nil)
			continue
		}
		for i, g := range c.Groups {
			if (g == nil) == m.Has(i) || g != nil && m.Group(i) != *g {
				t.Errorf("/%s/%s on %q: group %d = %q, JavaScript %v", c.Src, c.Flags, c.Input, i, m.Group(i), g)
			}
		}
	}
}

func TestNumJSON(t *testing.T) {
	for _, c := range []struct {
		n    float64
		want string
	}{{0, "0"}, {math.Copysign(0, -1), "0"}, {math.NaN(), "null"}, {math.Inf(1), "null"}, {1e21, "1e+21"}, {-1.5, "-1.5"}, {34359738368, "34359738368"}} {
		b, _ := json.Marshal(Num(c.n))
		if string(b) != c.want {
			t.Errorf("JSON of %v = %s, want %s", c.n, b, c.want)
		}
	}
}
