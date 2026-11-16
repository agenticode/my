package sel

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// clock turns the dates and times of a text dump into times.
//
// ipmitool up to 1.8.18 prints "11/12/2026 | 03:14:07" through gmtime(): the
// clock of the BMC as it stands, without a zone (ipmi_sel.c of 1.8.18,
// ipmi_sel_timestamp). From 1.8.19 on it prints the local time of the
// machine it runs on in the locale of that machine: the date may be day
// first or two-digit, the time may have AM or PM, and the zone
// abbreviation follows.
type clock struct {
	loc *time.Location
	// dayFirst is set when the dates of the dump are day/month/year.
	dayFirst bool
	// ambiguous is set when no date of the dump tells the order.
	ambiguous bool
	slashed   bool
	// zones counts the zone names the dump printed.
	zones map[string]int
	// unknown counts the zone names that were not understood.
	unknown map[string]int
	bare    int // times printed without a zone
}

// zoneOffsets are zone abbreviations that stand for one offset wherever
// they are used. Abbreviations with several meanings (CST, IST, BST) are
// left out: for them the caller has to name the zone.
var zoneOffsets = map[string]int{
	"UTC": 0, "GMT": 0, "Z": 0, "WET": 0, "WEST": 1 * 3600,
	"CET": 1 * 3600, "CEST": 2 * 3600, "EET": 2 * 3600, "EEST": 3 * 3600, "MSK": 3 * 3600,
	"KST": 9 * 3600, "JST": 9 * 3600, "HKT": 8 * 3600, "SGT": 8 * 3600, "AWST": 8 * 3600,
	"AEST": 10 * 3600, "AEDT": 11 * 3600, "NZST": 12 * 3600, "NZDT": 13 * 3600,
	"PST": -8 * 3600, "PDT": -7 * 3600, "MST": -7 * 3600, "MDT": -6 * 3600,
	"EST": -5 * 3600, "EDT": -4 * 3600,
}

// decide looks at all dates of a dump to tell month/day from day/month.
func (c *clock) decide(dates []string) {
	monthFirst, dayFirst := false, false
	for _, d := range dates {
		p := strings.Split(d, "/")
		if len(p) != 3 || len(p[0]) > 2 {
			continue
		}
		c.slashed = true
		a, err1 := strconv.Atoi(p[0])
		b, err2 := strconv.Atoi(p[1])
		if err1 != nil || err2 != nil {
			continue
		}
		if a > 12 {
			dayFirst = true
		}
		if b > 12 {
			monthFirst = true
		}
	}
	c.dayFirst = dayFirst && !monthFirst
	c.ambiguous = c.slashed && !dayFirst && !monthFirst
}

var months = map[string]time.Month{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

// parseDate reads the date forms the tools print.
func (c *clock) parseDate(s string) (y int, m time.Month, d int, ok bool) {
	num := func(x string) (int, bool) {
		if x == "" || len(x) > 4 {
			return 0, false
		}
		v, err := strconv.Atoi(x)
		return v, err == nil && v >= 0
	}
	year := func(x string) (int, bool) {
		v, ok := num(x)
		if ok && len(x) <= 2 {
			// Two digits: the years a SEL can hold start in 1970.
			if v >= 70 {
				v += 1900
			} else {
				v += 2000
			}
		}
		return v, ok
	}
	var a, b, yr string
	switch {
	case strings.Count(s, "/") == 2:
		p := strings.Split(s, "/")
		if len(p[0]) == 4 { // 2026/11/12
			yr, a, b = p[0], p[1], p[2]
		} else {
			a, b, yr = p[0], p[1], p[2]
			if c.dayFirst {
				a, b = b, a
			}
		}
	case strings.Count(s, "-") == 2:
		p := strings.Split(s, "-")
		if mon, isName := months[strings.ToLower(p[0])]; isName { // Nov-12-2026
			dd, ok1 := num(p[1])
			yy, ok2 := year(p[2])
			return yy, mon, dd, ok1 && ok2 && validDay(yy, mon, dd)
		}
		if len(p[0]) != 4 { // 12-11-2026 is not a form any of the tools prints
			return 0, 0, 0, false
		}
		yr, a, b = p[0], p[1], p[2]
	case strings.Count(s, ".") == 2: // 12.11.2026
		p := strings.Split(s, ".")
		b, a, yr = p[0], p[1], p[2]
	default:
		return 0, 0, 0, false
	}
	mm, ok1 := num(a)
	dd, ok2 := num(b)
	yy, ok3 := year(yr)
	if !ok1 || !ok2 || !ok3 || mm < 1 || mm > 12 {
		return 0, 0, 0, false
	}
	return yy, time.Month(mm), dd, validDay(yy, time.Month(mm), dd)
}

func validDay(y int, m time.Month, d int) bool {
	if y < 1970 || y > 9999 || d < 1 {
		return false
	}
	return d <= time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// parseClock reads "03:14:07", "03:14:07 PM" and either with a zone after
// it. It returns the seconds of the day and the zone name, if any.
func parseClock(s string) (sec int, zone string, ok bool) {
	f := strings.Fields(s)
	if len(f) == 0 || len(f) > 3 {
		return 0, "", false
	}
	p := strings.Split(f[0], ":")
	if len(p) != 3 {
		return 0, "", false
	}
	var hms [3]int
	for i, x := range p {
		if len(x) == 0 || len(x) > 2 {
			return 0, "", false
		}
		v, err := strconv.Atoi(x)
		if err != nil || v < 0 {
			return 0, "", false
		}
		hms[i] = v
	}
	rest := f[1:]
	if len(rest) > 0 {
		switch strings.ToUpper(rest[0]) {
		case "AM", "PM":
			if hms[0] < 1 || hms[0] > 12 {
				return 0, "", false
			}
			hms[0] %= 12
			if strings.ToUpper(rest[0]) == "PM" {
				hms[0] += 12
			}
			rest = rest[1:]
		}
	}
	if hms[0] > 23 || hms[1] > 59 || hms[2] > 60 {
		return 0, "", false
	}
	if len(rest) > 1 {
		return 0, "", false
	}
	if len(rest) == 1 {
		zone = rest[0]
	}
	return hms[0]*3600 + hms[1]*60 + hms[2], zone, true
}

// numericZone reads zone names of the form "+09", "+0900" and "-03:30".
func numericZone(z string) (offset int, ok bool) {
	if len(z) < 3 || (z[0] != '+' && z[0] != '-') {
		return 0, false
	}
	digits := strings.ReplaceAll(z[1:], ":", "")
	if len(digits) != 2 && len(digits) != 4 {
		return 0, false
	}
	v, err := strconv.Atoi(digits)
	if err != nil {
		return 0, false
	}
	h, m := v, 0
	if len(digits) == 4 {
		h, m = v/100, v%100
	}
	if h > 14 || m > 59 {
		return 0, false
	}
	offset = h*3600 + m*60
	if z[0] == '-' {
		offset = -offset
	}
	return offset, true
}

// parse turns a date and a time of a dump into a time. wall is the time as
// printed, without any zone applied, for the fingerprint of the record.
// relative is set for a time counted from the start of the BMC.
func (c *clock) parse(date, clockText string) (t time.Time, wall string, relative, ok bool) {
	date = strings.TrimSpace(date)
	switch date {
	case "Pre-Init":
		// The time field holds the seconds since the BMC started.
		sec, err := strconv.ParseInt(strings.TrimSpace(clockText), 10, 64)
		if err != nil || sec < 0 || sec >= 0x20000000 {
			return time.Time{}, "", false, false
		}
		return time.Unix(sec, 0).UTC(), fmt.Sprintf("preinit:%d", sec), true, true
	case "S+":
		// "70/001 00:02:03": year and day of the year of a count from zero.
		f := strings.Fields(clockText)
		if len(f) < 2 {
			return time.Time{}, "", false, false
		}
		yd := strings.Split(f[0], "/")
		sec, _, okClock := parseClock(f[1])
		if len(yd) != 2 || !okClock {
			return time.Time{}, "", false, false
		}
		yy, err1 := strconv.Atoi(yd[0])
		doy, err2 := strconv.Atoi(yd[1])
		if err1 != nil || err2 != nil || yy < 70 || yy > 87 || doy < 1 || doy > 366 {
			return time.Time{}, "", false, false
		}
		t = time.Date(1900+yy, 1, doy, 0, 0, sec, 0, time.UTC)
		return t, fmt.Sprintf("preinit:%d", t.Unix()), true, true
	}
	y, m, d, okDate := c.parseDate(date)
	sec, zone, okClock := parseClock(clockText)
	if !okDate || !okClock {
		return time.Time{}, "", false, false
	}
	naive := time.Date(y, m, d, 0, 0, sec, 0, time.UTC)
	wall = naive.Format("20060102150405")

	if zone == "" {
		c.bare++
	} else {
		if c.zones == nil {
			c.zones = map[string]int{}
		}
		c.zones[zone]++
	}
	switch {
	case c.loc != nil:
		t = time.Date(y, m, d, 0, 0, sec, 0, c.loc).UTC()
	case zone == "":
		t = naive
	default:
		off, known := zoneOffsets[strings.ToUpper(zone)]
		if !known {
			off, known = numericZone(zone)
		}
		if !known {
			if c.unknown == nil {
				c.unknown = map[string]int{}
			}
			c.unknown[zone]++
		}
		t = naive.Add(-time.Duration(off) * time.Second)
	}
	if yr := t.Year(); yr < 1970 || yr > 9999 {
		return time.Time{}, "", false, false
	}
	return t, wall, false, true
}

// notes says how the times of the dump were read, where that is a choice.
func (c *clock) notes(loc *time.Location) []string {
	var out []string
	if c.ambiguous {
		out = append(out, "no date of the dump tells month/day from day/month; they were read as month/day/year")
	}
	if c.dayFirst {
		out = append(out, "the dates of the dump are day/month/year")
	}
	if loc != nil {
		if c.bare > 0 || len(c.zones) > 0 {
			out = append(out, fmt.Sprintf("times were read in the zone %s as requested", loc))
		}
		return out
	}
	if c.bare > 0 {
		out = append(out, fmt.Sprintf("%d times carry no zone and were read as UTC; ipmitool prints the local time of the machine it runs on", c.bare))
	}
	var names []string
	for z := range c.unknown {
		names = append(names, z)
	}
	sort.Strings(names)
	for _, z := range names {
		out = append(out, fmt.Sprintf("%d times are marked %q, a zone name with more than one meaning or none known here; they were read as UTC", c.unknown[z], z))
	}
	return out
}
