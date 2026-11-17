package view

import (
	"testing"

	"github.com/agenticode/my/internal/explorer"
)

func hintSet(s *explorer.SystemSnapshot) map[string]bool {
	m := map[string]bool{}
	for _, h := range Hints(s) {
		m[h.Code] = true
	}
	return m
}

// Each threshold of the explorer at its value and beside it: the hints
// hold at "above" and "below" as the explorer has them.
func TestExplorerThresholds(t *testing.T) {
	const gib = 1 << 30
	base := func() *explorer.SystemSnapshot {
		return &explorer.SystemSnapshot{TotalBytes: 100 * gib, AvailableBytes: 50 * gib, UsedBytes: 40 * gib, SwapTotalBytes: gib}
	}
	for _, c := range []struct {
		name string
		set  func(*explorer.SystemSnapshot)
		code string
		want bool
	}{
		{"available 9.99%", func(s *explorer.SystemSnapshot) { s.AvailableBytes = 9.99 * gib }, HintLowAvailable, true},
		{"available 10%", func(s *explorer.SystemSnapshot) { s.AvailableBytes = 10 * gib }, HintLowAvailable, false},
		{"full pressure 1", func(s *explorer.SystemSnapshot) { s.PsiFullAvg10 = 1 }, HintPressureFull, false},
		{"full pressure 1.01", func(s *explorer.SystemSnapshot) { s.PsiFullAvg10 = 1.01 }, HintPressureFull, true},
		{"some pressure 5", func(s *explorer.SystemSnapshot) { s.PsiSomeAvg10 = 5 }, HintPressureSome, false},
		{"some pressure 5.01", func(s *explorer.SystemSnapshot) { s.PsiSomeAvg10 = 5.01 }, HintPressureSome, true},
		{"unreclaimable 15%", func(s *explorer.SystemSnapshot) { s.SunreclaimBytes = 6 * gib }, HintUnreclaimable, false},
		{"unreclaimable 15.1%", func(s *explorer.SystemSnapshot) { s.SunreclaimBytes = 6.04 * gib }, HintUnreclaimable, true},
		{"dirty 10%", func(s *explorer.SystemSnapshot) { s.DirtyBytes = 10 * gib }, HintDirty, false},
		{"dirty 10.1%", func(s *explorer.SystemSnapshot) { s.DirtyBytes = 10.1 * gib }, HintDirty, true},
		{"cache 50%", func(s *explorer.SystemSnapshot) { s.CachedBytes = 50 * gib }, HintCache, false},
		{"cache 50.1%", func(s *explorer.SystemSnapshot) { s.CachedBytes = 50.1 * gib }, HintCache, true},
		{"swap 1 GiB", func(s *explorer.SystemSnapshot) { s.SwapUsedBytes = gib }, HintSwap, false},
		{"swap 1 GiB + 1", func(s *explorer.SystemSnapshot) { s.SwapUsedBytes = gib + 1 }, HintSwap, true},
		{"healthy at 20%", func(s *explorer.SystemSnapshot) { s.AvailableBytes = 20 * gib }, HintHealthy, false},
		{"healthy at 20.1%", func(s *explorer.SystemSnapshot) { s.AvailableBytes = 20.1 * gib }, HintHealthy, true},
	} {
		s := base()
		c.set(s)
		if got := hintSet(s)[c.code]; got != c.want {
			t.Errorf("%s: hint %s %v, want %v", c.name, c.code, got, c.want)
		}
	}
	for _, c := range []struct {
		v    float64
		want string
	}{{10000, ""}, {10001, "moderate"}, {1000000, "moderate"}, {1000001, "high"}} {
		if got := CounterLevel(c.v); got != c.want {
			t.Errorf("counter %v: %q, want %q", c.v, got, c.want)
		}
	}
	for _, c := range []struct {
		v    float64
		want string
	}{{70, "ok"}, {70.1, "warning"}, {90, "warning"}, {90.1, "critical"}} {
		if got := NodeLevel(c.v); got != c.want {
			t.Errorf("node %v: %q, want %q", c.v, got, c.want)
		}
	}
	for _, c := range []struct {
		v, good, fair float64
		higher        bool
		want          string
	}{
		{95, 95, 80, true, "warn"}, {95.1, 95, 80, true, "ok"}, {80, 95, 80, true, "bad"}, {80.1, 95, 80, true, "warn"},
		{5, 5, 20, false, "warn"}, {4.9, 5, 20, false, "ok"}, {20, 5, 20, false, "bad"}, {19.9, 5, 20, false, "warn"},
		{90, 90, 70, true, "warn"}, {90.1, 90, 70, true, "ok"}, {10, 10, 30, false, "warn"}, {9.9, 10, 30, false, "ok"},
	} {
		if got := Assess(c.v, c.good, c.fair, c.higher); got != c.want {
			t.Errorf("assess %v (%v, %v): %q, want %q", c.v, c.good, c.fair, got, c.want)
		}
	}
	s := base()
	s.AvailableBytes = 15 * gib
	s.PsiSomeAvg10 = 6
	if KPILevel("available", s) != "warning" || KPILevel("pressure", s) != "warning" || KPILevel("swap", s) != "" {
		t.Error("levels of the cards")
	}
	nodes := []explorer.NumaNode{{TotalBytes: 100, UsedBytes: 80}, {TotalBytes: 100, UsedBytes: 49}}
	if sp := numaTotals(nodes).Spread; sp <= 30 {
		t.Errorf("spread %v", sp)
	}
}
