package engine

import (
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestParamsCoverTheConfig: every threshold has a key, a description and a
// default inside its bounds, so that zero configuration is valid and every
// field can be set.
func TestParamsCoverTheConfig(t *testing.T) {
	ps := Params()
	if n := reflect.TypeOf(Config{}).NumField(); len(ps) != n {
		t.Errorf("%d keys for %d thresholds", len(ps), n)
	}
	seen := map[string]bool{}
	def := DefaultConfig()
	for _, p := range ps {
		if seen[p.Key] || p.Key != strings.ToLower(p.Key) || strings.ContainsAny(p.Key, " =#") || p.Help == "" || p.Default == "" {
			t.Errorf("param %+v", p)
		}
		seen[p.Key] = true
		got, ok := def.Get(p.Key)
		if !ok || got != p.Default {
			t.Errorf("%s: Get = %q, %v; default %q", p.Key, got, ok, p.Default)
		}
		// Setting a threshold to its default is accepted, and changing it
		// changes exactly that field.
		c := DefaultConfig()
		if err := c.Set(p.Key, p.Default); err != nil || c != def {
			t.Errorf("%s: Set to the default: %v", p.Key, err)
		}
	}
	if err := def.Validate(); err != nil {
		t.Errorf("the defaults are not valid: %v", err)
	}
	if got := def.Changed(); len(got) != 0 {
		t.Errorf("the defaults differ from the defaults: %v", got)
	}
	// The values the design names.
	for key, want := range map[string]string{"trend.recent_days": "7", "trend.baseline_days": "30", "cluster.line_chance": "0.01", "cluster.row_window_bytes": "65536"} {
		if got, _ := def.Get(key); got != want {
			t.Errorf("%s = %s, want %s", key, got, want)
		}
	}
}

func TestSetThreshold(t *testing.T) {
	c := DefaultConfig()
	for key, value := range map[string]string{
		"trend.rise_ratio": "3.5", " Rule.Row.Min_Errors ": " 25 ", "cluster.multi_row_share": "0", "cluster.row_window_bytes": "131072", "rule.quiet.days": "0",
	} {
		if err := c.Set(key, value); err != nil {
			t.Errorf("Set(%q, %q): %v", key, value, err)
		}
	}
	if c.RiseRatio != 3.5 || c.RowMinErrors != 25 || c.MultiRowShare != 0 || c.RowWindowBytes != 131072 || c.QuietDays != 0 {
		t.Errorf("config = %+v", c)
	}
	want := [][2]string{{"cluster.multi_row_share", "0"}, {"cluster.row_window_bytes", "131072"}, {"trend.rise_ratio", "3.5"},
		{"rule.row.min_errors", "25"}, {"rule.quiet.days", "0"}}
	if got := c.Changed(); !reflect.DeepEqual(got, want) {
		t.Errorf("changed = %v\nwant %v", got, want)
	}
	before := c
	for _, tt := range []struct{ key, value, want string }{
		{"trend.rise_ratoi", "3", `unknown threshold "trend.rise_ratoi"`},
		{"trend.rise_ratio", "fast", `trend.rise_ratio: "fast" is not a number`},
		{"trend.rise_ratio", "", `is not a number`},
		{"trend.rise_ratio", "NaN", `is not a number`},
		{"trend.rise_ratio", "+Inf", `is not a number`},
		{"trend.rise_ratio", "0.5", "trend.rise_ratio: 0.5 is below the minimum of 1"},
		{"rule.row.min_errors", "2.5", `rule.row.min_errors: "2.5" is not a whole number`},
		{"rule.row.min_errors", "0", "rule.row.min_errors: 0 is below the minimum of 1"},
		{"cluster.line_chance", "1.5", "cluster.line_chance: 1.5 is outside the range above 0 to 1"},
		{"cluster.line_chance", "0", "cluster.line_chance: 0 is not above zero"},
		{"trend.recent_days", "-1", "trend.recent_days: -1 is not above zero"},
		{"cluster.wider_share", "2", "cluster.wider_share: 2 is outside the range 0 to 1"},
		{"cluster.row_window_bytes", "8", "is outside the range 64 to 1099511627776"},
		// A whole number beyond the range of int converted to the smallest
		// int on amd64, and was accepted ("storm.count=1e99", found by the
		// fuzz job of CI).
		{"storm.count", "1e99", `storm.count: "1e99" is larger than 9007199254740992`},
		{"evidence.max_records", "9007199254740994", "is larger than 9007199254740992"},
	} {
		err := c.Set(tt.key, tt.value)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("Set(%q, %q): err = %v, want %q", tt.key, tt.value, err, tt.want)
		}
	}
	if c != before {
		t.Error("a refused value changed the configuration")
	}
	if err := c.Set("storm.count", "9007199254740992"); err != nil || c.StormCount != 1<<53 {
		t.Errorf("the largest whole number: %v, %d", err, c.StormCount)
	}
	if _, ok := c.Get("no.such.key"); ok {
		t.Error("Get of an unknown key")
	}
}

func TestReadConfigFile(t *testing.T) {
	const file = `# thresholds of the night shift
trend.rise_ratio = 3      # three times the baseline

  rule.row.min_errors=20
storm.count = 250
`
	c := DefaultConfig()
	if err := c.Read(strings.NewReader(file), "my.conf"); err != nil {
		t.Fatal(err)
	}
	if c.RiseRatio != 3 || c.RowMinErrors != 20 || c.StormCount != 250 || len(c.Changed()) != 3 {
		t.Errorf("config = %+v", c.Changed())
	}
	for _, tt := range []struct{ file, want string }{
		{"trend.rise_ratio = 3\nstorm.cuont = 5\n", `my.conf:2: unknown threshold "storm.cuont"`},
		{"\n\nstorm.count 250\n", `my.conf:3: want "key = value", found "storm.count 250"`},
		{"storm.count = 250\n# again\nstorm.count = 300\n", "my.conf:3: storm.count is already set on line 1"},
		{"storm.count = many\n", `my.conf:1: storm.count: "many" is not a number`},
		{"storm.count = 0\n", "my.conf:1: storm.count: 0 is below the minimum of 1"},
	} {
		c := DefaultConfig()
		if err := c.Read(strings.NewReader(tt.file), "my.conf"); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%q: err = %v, want %q", tt.file, err, tt.want)
		}
	}
	// An empty file and a file of comments leave the defaults.
	for _, f := range []string{"", "# nothing\n\n   \n"} {
		c := DefaultConfig()
		if err := c.Read(strings.NewReader(f), "my.conf"); err != nil || c != DefaultConfig() {
			t.Errorf("%q: %v", f, err)
		}
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "my.conf")
	if err := os.WriteFile(path, []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(path)
	if err != nil || loaded != c {
		t.Errorf("LoadConfig: %v", err)
	}
	if _, err := LoadConfig(filepath.Join(dir, "absent.conf")); err == nil || !strings.Contains(err.Error(), "configuration file") {
		t.Errorf("missing file: err = %v", err)
	}
	if _, err := LoadConfig(dir); err == nil {
		t.Error("a directory was read as a configuration file")
	}
}

func TestConfigFlags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "my.conf")
	if err := os.WriteFile(path, []byte("trend.rise_ratio = 3\nstorm.count = 250\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	parse := func(args ...string) (Config, error) {
		var f ConfigFlags
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		f.Register(fs)
		if err := fs.Parse(args); err != nil {
			t.Fatal(err)
		}
		return f.Config()
	}
	// No flags: the defaults.
	if c, err := parse(); err != nil || c != DefaultConfig() {
		t.Errorf("no flags: %v", err)
	}
	// A file, then single settings, which win over the file.
	c, err := parse("-config", path, "-set", "storm.count=400", "-set", "rule.quiet.days = 14")
	if err != nil || c.RiseRatio != 3 || c.StormCount != 400 || c.QuietDays != 14 {
		t.Errorf("file and settings: %+v, %v", c.Changed(), err)
	}
	// The last setting of a key wins.
	if c, err := parse("-set", "storm.count=400", "-set", "storm.count=500"); err != nil || c.StormCount != 500 {
		t.Errorf("two settings: %d, %v", c.StormCount, err)
	}
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"-set", "storm.count"}, "-set storm.count: want key=value"},
		{[]string{"-set", "storm.cuont=4"}, `-set unknown threshold "storm.cuont"`},
		{[]string{"-set", "storm.count=0"}, "-set storm.count: 0 is below the minimum of 1"},
		{[]string{"-config", filepath.Join(dir, "absent.conf")}, "configuration file"},
	} {
		if _, err := parse(tt.args...); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%v: err = %v, want %q", tt.args, err, tt.want)
		}
	}
}

func TestConfigJSON(t *testing.T) {
	c := DefaultConfig()
	c.StormCount = 250
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]float64
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if len(m) != len(Params()) || m["storm.count"] != 250 || m["cluster.line_chance"] != 0.01 || m["trend.recent_days"] != 7 {
		t.Errorf("thresholds as JSON = %s", b)
	}
}

func TestValidate(t *testing.T) {
	c := DefaultConfig()
	c.SpreadMinNew = 0
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "spread.min_new") {
		t.Errorf("err = %v", err)
	}
	c = Config{}
	if err := c.Validate(); err == nil {
		t.Error("the zero Config is valid")
	}
}
