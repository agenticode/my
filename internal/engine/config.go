package engine

import "time"

// Config holds the thresholds of the engine. Every value is a default that
// can be changed; none of them has been validated against failure analysis
// results yet, which is why the engine calls its output a finding.
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
	}
}

func (c *Config) repeatGap() int64 {
	return int64(c.RepeatGapHours * float64(time.Hour/time.Microsecond))
}

func (c *Config) scatterWindow() int64 {
	return int64(c.ScatterWindowDays * 24 * float64(time.Hour/time.Microsecond))
}
