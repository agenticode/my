package engine

import (
	"strings"
	"testing"
)

// FuzzConfigRead feeds arbitrary text to the configuration file reader. It
// must not panic, and what it accepts must be within the bounds of every
// threshold.
func FuzzConfigRead(f *testing.F) {
	f.Add("# thresholds\ntrend.rise_ratio = 3\nstorm.count=250\n")
	f.Add("cluster.line_chance = 1e-3\n\n  rule.quiet.days = 0 # none\n")
	f.Add("storm.count = 1e999\n")
	f.Add("=\n==\nkey\n")
	f.Add("trend.recent_days = 0x10\nspread.min_new = 2.0\n")
	f.Add(strings.Repeat("storm.count = 5\n", 3))
	f.Fuzz(func(t *testing.T, text string) {
		c := DefaultConfig()
		if err := c.Read(strings.NewReader(text), "fuzz.conf"); err != nil {
			if !strings.HasPrefix(err.Error(), "fuzz.conf") {
				t.Errorf("error without the file name: %v", err)
			}
			return
		}
		if err := c.Validate(); err != nil {
			t.Errorf("accepted %q, which is not valid: %v", text, err)
		}
		// A configuration that was accepted can be used.
		if _, err := Analyze(NewInput(), Options{Config: &c}); err != nil {
			t.Errorf("accepted %q, which Analyze refuses: %v", text, err)
		}
	})
}
