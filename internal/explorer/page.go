package explorer

import (
	"math"
	"strings"
)

// SarRow is one line of the paging table of sar -B.
type SarRow struct {
	Time   string `json:"time"`
	Values Record `json:"values"`
}

// PageStats are the page counters and the paging table.
type PageStats struct {
	Vmstat Record   `json:"vmstat"`
	SarB   []SarRow `json:"sarB"`
}

// MaxSarRows bounds the rows of sar -B read.
const MaxSarRows = 20000

var reSarTime = jsre(`(\d{2}:\d{2})`, "")

// ParsePageVmstat reads lines of two fields: name and count.
func ParsePageVmstat(raw string) Record {
	var r Record
	for _, line := range Lines(raw) {
		if parts := SplitSpace(Trim(line)); len(parts) == 2 {
			r.Set(parts[0], Or(ParseInt(parts[1]), 0))
		}
	}
	return r
}

// ParseSarB reads the paging table of sar -B: a head line names the
// columns (a column AM or PM of a 12-hour clock is left out), each later
// line of numbers is a row; lines Linux and Average are skipped, and a
// text that holds NOT_AVAILABLE has no rows.
func ParseSarB(raw string) []SarRow {
	out := []SarRow{}
	if raw == "" || strings.Contains(raw, "NOT_AVAILABLE") {
		return out
	}
	var headers []string
	for _, line := range Lines(raw) {
		t := Trim(line)
		if t == "" || strings.HasPrefix(t, "Linux") || strings.HasPrefix(t, "Average") {
			continue
		}
		if strings.Contains(t, "pgpgin/s") || strings.Contains(t, "fault/s") {
			headers = SplitSpace(t)[1:]
			if len(headers) > 0 && (headers[0] == "AM" || headers[0] == "PM") {
				headers = headers[1:]
			}
			continue
		}
		if len(headers) == 0 {
			continue
		}
		parts := SplitSpace(t)
		if len(parts) < 3 {
			continue
		}
		timeStr, start := parts[0], 1
		if parts[1] == "AM" || parts[1] == "PM" {
			timeStr, start = parts[0]+" "+parts[1], 2
		}
		tm := timeStr
		if m, ok := reSarTime.Match(timeStr); ok {
			tm = m.Group(1)
		}
		vals := make([]Num, 0, len(parts)-start)
		bad := false
		for _, p := range parts[start:] {
			v := ParseFloat(p)
			if math.IsNaN(float64(v)) {
				bad = true
			}
			vals = append(vals, v)
		}
		if bad || len(vals) < len(headers) {
			continue
		}
		if len(out) >= MaxSarRows {
			break
		}
		var values Record
		for i, h := range headers {
			values.Set(h, vals[i])
		}
		out = append(out, SarRow{Time: tm, Values: values})
	}
	return out
}

// PageStatsOf reads /proc/vmstat (the lines of the page counters)
// and the output of sar -B.
func PageStatsOf(vmstat, sarB string) PageStats {
	return PageStats{
		Vmstat: ParsePageVmstat(GrepE(vmstat, "pgscan", "pgsteal", "pgfault", "pgmajfault", "workingset", "pgdemote")),
		SarB:   ParseSarB(sarB),
	}
}
