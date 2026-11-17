package explorer

// Carried over from the Memory Explorer of aterm by its author. See NOTICE.

import "math"

var byteUnits = []string{"B", "KiB", "MiB", "GiB", "TiB"}

// FormatBytes writes a byte count in binary units with two, one or no
// decimals below 10, 100 and from 100 on. formatBytes, memory.ts 167-176.
func FormatBytes(bytes float64) string {
	if bytes == 0 {
		return "0 B"
	}
	if bytes < 0 {
		return "-" + FormatBytes(-bytes)
	}
	i := math.Floor(Log(bytes) / Log(1024))
	idx := i
	if !(idx <= 4) && !math.IsNaN(idx) {
		idx = 4
	}
	val := bytes / pow1024(idx)
	unit := "undefined"
	if idx >= 0 && idx <= 4 && idx == math.Trunc(idx) {
		unit = byteUnits[int(idx)]
	}
	var num string
	switch {
	case val < 10:
		num = ToFixed(val, 2)
	case val < 100:
		num = ToFixed(val, 1)
	default:
		num = ToFixed(val, 0)
	}
	return num + " " + unit
}

// Pct is part of total in percent, 0 without a total. pct, memory.ts 178-180.
func Pct(part, total float64) float64 {
	if total > 0 {
		return part / total * 100
	}
	return 0
}

// Clamp keeps v between lo and hi. clamp, memory.ts 182-184.
func Clamp(v, lo, hi float64) float64 { return jsMax(lo, jsMin(hi, v)) }

// FormatNumber writes a count with B, M or K and one decimal from a
// thousand on. formatNumber, memory.ts 192-197.
func FormatNumber(n float64) string {
	switch {
	case n >= 1e9:
		return ToFixed(n/1e9, 1) + "B"
	case n >= 1e6:
		return ToFixed(n/1e6, 1) + "M"
	case n >= 1e3:
		return ToFixed(n/1e3, 1) + "K"
	}
	return NumString(n)
}

// jsMax is Math.max of two: NaN wins, +0 above -0.
func jsMax(a, b float64) float64 {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.NaN()
	}
	return math.Max(a, b)
}

// jsMin is Math.min of two: NaN wins, -0 below +0.
func jsMin(a, b float64) float64 {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.NaN()
	}
	return math.Min(a, b)
}
