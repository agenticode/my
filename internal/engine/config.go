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
	}
}

const microPerDay = int64(24 * time.Hour / time.Microsecond)

func days(d float64) int64 { return int64(d * float64(microPerDay)) }

func (c *Config) repeatGap() int64 {
	return int64(c.RepeatGapHours * float64(time.Hour/time.Microsecond))
}

func (c *Config) scatterWindow() int64 {
	return int64(c.ScatterWindowDays * 24 * float64(time.Hour/time.Microsecond))
}
