package engine

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds the thresholds of the engine. Every value is a default that
// can be changed; none of them has been validated against failure analysis
// results yet, which is why the engine calls its output a finding.
//
// Each field has a key (see Params) under which a configuration file or a
// command line flag can set it.
type Config struct {
	// LineChance is the largest probability of a chance alignment at which
	// errors that share a row or a column are still taken as a line. With
	// many errors scattered over a bank some row or column collects several
	// of them by accident; a line is only reported when scatter explains it
	// with less than this probability.
	LineChance float64
	// RepeatGapHours and RepeatCount decide when a place counts as failing
	// again: two errors at least RepeatGapHours apart, or RepeatCount
	// errors in total. A single upset can be read several times before the
	// cell is rewritten, so errors close together are not a repetition.
	RepeatGapHours float64
	RepeatCount    int
	// MultiRowShare is the share of a row group's errors that must lie
	// outside its busiest row for the group to be a multi-row fault. Below
	// it the group is a row fault that has started to reach its neighbours.
	MultiRowShare float64
	// ScatterMinCells and ScatterWindowDays decide when errors at
	// unrelated places form a fault: at least ScatterMinCells different
	// places first seen within ScatterWindowDays. Fewer are separate upsets.
	ScatterMinCells   int
	ScatterWindowDays float64
	// WiderShare is the share of scattered places that must lie outside
	// the busiest bank (or device) for the fault to be a device (or DIMM)
	// fault. Below it the busiest bank or device is judged on its own.
	WiderShare float64
	// RowWindowBytes is the address distance within which errors that are
	// only known by physical address are taken to lie in one row. The mode
	// is then marked as estimated.
	RowWindowBytes uint64

	// RecentDays and BaselineDays are the two windows a trend compares: the
	// last RecentDays before the reference time, and the BaselineDays before
	// those.
	RecentDays   float64
	BaselineDays float64
	// MinBaselineDays is how much of the baseline window the ledger must
	// cover before a trend is stated. A history shorter than that cannot
	// tell a new fault from one that was always there.
	MinBaselineDays float64
	// RiseRatio and RiseMinRecent decide when errors are rising: the daily
	// rate of the recent window is at least RiseRatio times the rate of the
	// baseline window, and the recent window holds at least RiseMinRecent
	// errors.
	RiseRatio     float64
	RiseMinRecent int
	// SpreadDays and SpreadMinNew decide when a fault is spreading: at
	// least SpreadMinNew places failed for the first time within the last
	// SpreadDays, not counting the places of the fault's first day.
	SpreadDays   float64
	SpreadMinNew int
	// StormMinutes and StormCount define a burst of errors: StormCount
	// errors or more within StormMinutes.
	StormMinutes float64
	StormCount   int
	// MinPeers is the number of DIMMs of one part number, the DIMM itself
	// included, a ledger must hold before the DIMM is ranked among them.
	MinPeers int

	// RowMinErrors is the number of errors a row, column or bank fault (or
	// a wider one) must have before R-ROW-03 looks at its trend and spread.
	RowMinErrors int
	// OfflineMinPages, OfflineMinDays and OfflineRecentDays are the
	// conditions of R-OFF-02: at least OfflineMinPages pages taken offline,
	// on at least OfflineMinDays different days, the last of them within
	// OfflineRecentDays of the reference time.
	OfflineMinPages   int
	OfflineMinDays    int
	OfflineRecentDays float64
	// QuietDays is how long a fault must have been silent after a repair,
	// a page offline or a reseat before R-QUIET-01 and R-SYS-03 call it
	// quiet.
	QuietDays float64
	// SystemMinErrors is the number of errors that are not confined to one
	// cell, row, column or bank which each side needs before the R-SYS
	// rules compare them: each DIMM of a channel, each occupant of a slot,
	// the time before a reseat.
	SystemMinErrors int
	// SystemOverlapDays is how far apart the error periods of two DIMMs of
	// a channel may lie and still count as the same period.
	SystemOverlapDays float64
	// HostWideMinDIMMs is the number of DIMMs of one host with unconfined
	// errors in the same period from which a common cause is mentioned.
	HostWideMinDIMMs int
	// MaxEvidenceRecords caps the record references listed with one
	// statement. The first and the last records are kept.
	MaxEvidenceRecords int
}

// DefaultConfig returns the default thresholds.
func DefaultConfig() Config {
	return Config{
		LineChance:        0.01,
		RepeatGapHours:    24,
		RepeatCount:       10,
		MultiRowShare:     0.10,
		ScatterMinCells:   5,
		ScatterWindowDays: 30,
		WiderShare:        0.10,
		RowWindowBytes:    64 << 10,

		RecentDays:      7,
		BaselineDays:    30,
		MinBaselineDays: 7,
		RiseRatio:       2,
		RiseMinRecent:   10,
		SpreadDays:      30,
		SpreadMinNew:    2,
		StormMinutes:    60,
		StormCount:      100,
		MinPeers:        5,

		RowMinErrors:       10,
		OfflineMinPages:    3,
		OfflineMinDays:     2,
		OfflineRecentDays:  7,
		QuietDays:          7,
		SystemMinErrors:    5,
		SystemOverlapDays:  7,
		HostWideMinDIMMs:   3,
		MaxEvidenceRecords: 20,
	}
}

const microPerDay = int64(24 * time.Hour / time.Microsecond)

func days(d float64) int64 { return int64(d * float64(microPerDay)) }

func (c *Config) repeatGap() int64 {
	return int64(c.RepeatGapHours * float64(time.Hour/time.Microsecond))
}

func (c *Config) scatterWindow() int64 { return days(c.ScatterWindowDays) }

// Param describes one threshold: its key, what it decides and its bounds.
type Param struct {
	// Key is the name used in configuration files and with the -set flag.
	Key string
	// Help says what the threshold decides, in one line.
	Help string
	// Default is the default value as text.
	Default string
	// Min and Max bound the values that are accepted.
	Min, Max float64
	// Integer is set for thresholds that take whole numbers.
	Integer bool

	get func(*Config) float64
	set func(*Config, float64)
}

func floatParam(key, help string, lo, hi float64, p func(*Config) *float64) Param {
	return Param{Key: key, Help: help, Min: lo, Max: hi,
		get: func(c *Config) float64 { return *p(c) },
		set: func(c *Config, v float64) { *p(c) = v }}
}

func intParam(key, help string, lo, hi float64, p func(*Config) *int) Param {
	return Param{Key: key, Help: help, Min: lo, Max: hi, Integer: true,
		get: func(c *Config) float64 { return float64(*p(c)) },
		set: func(c *Config, v float64) { *p(c) = int(v) }}
}

var inf = math.Inf(1)

// tiny is the lower bound of thresholds that must be above zero.
const tiny = 1e-9

var params = []Param{
	floatParam("cluster.line_chance", "largest chance of an accidental alignment at which errors sharing a row or column are still a line", tiny, 1,
		func(c *Config) *float64 { return &c.LineChance }),
	floatParam("cluster.repeat_gap_hours", "hours between two errors at one place from which the place counts as failing again", 0, inf,
		func(c *Config) *float64 { return &c.RepeatGapHours }),
	intParam("cluster.repeat_count", "errors at one place from which the place counts as failing again, however close together", 2, inf,
		func(c *Config) *int { return &c.RepeatCount }),
	floatParam("cluster.multi_row_share", "share of a row group's errors outside its busiest row from which it is a multi-row fault", 0, 1,
		func(c *Config) *float64 { return &c.MultiRowShare }),
	intParam("cluster.scatter_min_cells", "different places within the scatter window from which scattered errors form a fault", 2, inf,
		func(c *Config) *int { return &c.ScatterMinCells }),
	floatParam("cluster.scatter_window_days", "days within which scattered places must first appear to form a fault", tiny, inf,
		func(c *Config) *float64 { return &c.ScatterWindowDays }),
	floatParam("cluster.wider_share", "share of scattered places outside the busiest bank or device from which the fault is a device or DIMM fault", 0, 1,
		func(c *Config) *float64 { return &c.WiderShare }),
	{Key: "cluster.row_window_bytes", Help: "address distance within which errors known by address only are taken to lie in one row", Min: 64, Max: 1 << 40, Integer: true,
		get: func(c *Config) float64 { return float64(c.RowWindowBytes) },
		set: func(c *Config, v float64) { c.RowWindowBytes = uint64(v) }},

	floatParam("trend.recent_days", "length of the recent window in days", tiny, inf,
		func(c *Config) *float64 { return &c.RecentDays }),
	floatParam("trend.baseline_days", "length of the baseline window before the recent window in days", tiny, inf,
		func(c *Config) *float64 { return &c.BaselineDays }),
	floatParam("trend.min_baseline_days", "days of the baseline window the ledger must cover before a trend is stated", 0, inf,
		func(c *Config) *float64 { return &c.MinBaselineDays }),
	floatParam("trend.rise_ratio", "recent daily rate divided by baseline daily rate from which errors are rising", 1, inf,
		func(c *Config) *float64 { return &c.RiseRatio }),
	intParam("trend.rise_min_recent", "errors in the recent window needed to call a trend rising", 1, inf,
		func(c *Config) *int { return &c.RiseMinRecent }),
	floatParam("spread.days", "days within which a place must have failed for the first time to count as new", tiny, inf,
		func(c *Config) *float64 { return &c.SpreadDays }),
	intParam("spread.min_new", "new places from which a fault is spreading", 1, inf,
		func(c *Config) *int { return &c.SpreadMinNew }),
	floatParam("storm.minutes", "length of the window a burst of errors is counted in, in minutes", tiny, inf,
		func(c *Config) *float64 { return &c.StormMinutes }),
	intParam("storm.count", "errors within the storm window from which a burst is a storm", 1, inf,
		func(c *Config) *int { return &c.StormCount }),
	intParam("baseline.min_peers", "DIMMs of one part number, the DIMM included, needed to rank it among them", 2, inf,
		func(c *Config) *int { return &c.MinPeers }),

	intParam("rule.row.min_errors", "R-ROW-03: errors a row, column, bank or wider fault needs before its trend and spread count", 1, inf,
		func(c *Config) *int { return &c.RowMinErrors }),
	intParam("rule.offline.min_pages", "R-OFF-02: pages taken offline", 1, inf,
		func(c *Config) *int { return &c.OfflineMinPages }),
	intParam("rule.offline.min_days", "R-OFF-02: different days on which pages were taken offline", 1, inf,
		func(c *Config) *int { return &c.OfflineMinDays }),
	floatParam("rule.offline.recent_days", "R-OFF-02: days before the reference time within which the last page was taken offline", tiny, inf,
		func(c *Config) *float64 { return &c.OfflineRecentDays }),
	floatParam("rule.quiet.days", "R-QUIET-01, R-SYS-03: days without errors after a repair, page offline or reseat", 0, inf,
		func(c *Config) *float64 { return &c.QuietDays }),
	intParam("rule.system.min_errors", "R-SYS-01, R-SYS-02, R-SYS-03: unconfined errors each side needs before it is compared", 1, inf,
		func(c *Config) *int { return &c.SystemMinErrors }),
	floatParam("rule.system.overlap_days", "R-SYS-01: days the error periods of two DIMMs of a channel may lie apart", 0, inf,
		func(c *Config) *float64 { return &c.SystemOverlapDays }),
	intParam("check.host_wide_min_dimms", "DIMMs of a host with unconfined errors in the same period from which a common cause is mentioned", 2, inf,
		func(c *Config) *int { return &c.HostWideMinDIMMs }),
	intParam("evidence.max_records", "record references listed with one statement", 1, inf,
		func(c *Config) *int { return &c.MaxEvidenceRecords }),
}

func formatValue(v float64, integer bool) string {
	if integer || v == math.Trunc(v) {
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// Params lists the thresholds in the order they are documented, with their
// defaults.
func Params() []Param {
	def := DefaultConfig()
	out := make([]Param, len(params))
	for i, p := range params {
		p.Default = formatValue(p.get(&def), p.Integer)
		out[i] = p
	}
	return out
}

func findParam(key string) *Param {
	key = strings.ToLower(strings.TrimSpace(key))
	for i := range params {
		if params[i].Key == key {
			return &params[i]
		}
	}
	return nil
}

// Get returns the value of the threshold with the given key as text.
func (c *Config) Get(key string) (string, bool) {
	p := findParam(key)
	if p == nil {
		return "", false
	}
	return formatValue(p.get(c), p.Integer), true
}

// Set changes the threshold with the given key. It returns an error for an
// unknown key and for a value that is not a number or lies outside the
// bounds of the threshold.
func (c *Config) Set(key, value string) error {
	p := findParam(key)
	if p == nil {
		return fmt.Errorf("unknown threshold %q", strings.TrimSpace(key))
	}
	value = strings.TrimSpace(value)
	v, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return fmt.Errorf("%s: %q is not a number", p.Key, value)
	}
	if p.Integer && v != math.Trunc(v) {
		return fmt.Errorf("%s: %q is not a whole number", p.Key, value)
	}
	if err := p.check(v); err != nil {
		return err
	}
	p.set(c, v)
	return nil
}

func (p *Param) check(v float64) error {
	switch {
	case math.IsNaN(v):
		return fmt.Errorf("%s: not a number", p.Key)
	case v >= p.Min && v <= p.Max:
		return nil
	case p.Min == tiny && v <= 0:
		return fmt.Errorf("%s: %s is not above zero", p.Key, formatValue(v, p.Integer))
	case math.IsInf(p.Max, 1):
		return fmt.Errorf("%s: %s is below the minimum of %s", p.Key, formatValue(v, p.Integer), formatValue(p.Min, p.Integer))
	}
	lo := formatValue(p.Min, p.Integer)
	if p.Min == tiny {
		lo = "above 0"
	}
	return fmt.Errorf("%s: %s is outside the range %s to %s", p.Key, formatValue(v, p.Integer), lo, formatValue(p.Max, p.Integer))
}

// Validate checks every threshold against its bounds. A Config built in
// code can hold values Set would refuse.
func (c *Config) Validate() error {
	for i := range params {
		if err := params[i].check(params[i].get(c)); err != nil {
			return err
		}
	}
	return nil
}

// Changed returns the keys whose values differ from the defaults, with
// their values, in the order of Params.
func (c *Config) Changed() [][2]string {
	def := DefaultConfig()
	var out [][2]string
	for i := range params {
		p := &params[i]
		if v := p.get(c); v != p.get(&def) {
			out = append(out, [2]string{p.Key, formatValue(v, p.Integer)})
		}
	}
	return out
}

// Read applies a configuration file to c. The file holds one "key = value"
// per line; "#" starts a comment. name is used in error messages. An
// unknown key, a bad value and a key that is set twice are errors: a typo
// must not leave a default in place without notice.
func (c *Config) Read(r io.Reader, name string) error {
	seen := map[string]int{}
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("%s:%d: want \"key = value\", found %q", name, n, line)
		}
		p := findParam(key)
		if p == nil {
			return fmt.Errorf("%s:%d: unknown threshold %q", name, n, strings.TrimSpace(key))
		}
		if first, dup := seen[p.Key]; dup {
			return fmt.Errorf("%s:%d: %s is already set on line %d", name, n, p.Key, first)
		}
		seen[p.Key] = n
		if err := c.Set(key, value); err != nil {
			return fmt.Errorf("%s:%d: %w", name, n, err)
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// LoadConfig returns the defaults with the configuration file at path
// applied.
func LoadConfig(path string) (Config, error) {
	c := DefaultConfig()
	f, err := os.Open(path)
	if err != nil {
		return c, fmt.Errorf("configuration file: %w", err)
	}
	defer f.Close()
	if err := c.Read(f, path); err != nil {
		return c, err
	}
	return c, nil
}

// ConfigFlags are the command line flags that set thresholds: a
// configuration file, and single thresholds as key=value. A command
// registers them on its flag set and calls Config after parsing. Without
// either flag the defaults apply.
type ConfigFlags struct {
	file string
	sets settings
}

type settings []string

func (s *settings) String() string     { return strings.Join(*s, ",") }
func (s *settings) Set(v string) error { *s = append(*s, v); return nil }

// Register adds the -config and -set flags to fs.
func (f *ConfigFlags) Register(fs *flag.FlagSet) {
	fs.StringVar(&f.file, "config", "", "read thresholds from `file` (one \"key = value\" per line)")
	fs.Var(&f.sets, "set", "set one threshold, as `key=value`; may be given several times")
}

// Config returns the thresholds: the defaults, then the configuration
// file, then the single settings in the order they were given.
func (f *ConfigFlags) Config() (Config, error) {
	c := DefaultConfig()
	if f.file != "" {
		var err error
		if c, err = LoadConfig(f.file); err != nil {
			return c, err
		}
	}
	for _, s := range f.sets {
		key, value, ok := strings.Cut(s, "=")
		if !ok {
			return c, fmt.Errorf("-set %s: want key=value", s)
		}
		if err := c.Set(key, value); err != nil {
			return c, fmt.Errorf("-set %w", err)
		}
	}
	return c, nil
}
