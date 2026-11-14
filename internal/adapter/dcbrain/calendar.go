package dcbrain

import (
	"fmt"
	"time"
)

// The dataset hides its real dates. Every time is written on a calendar
// of its own that starts at "0001-01-01 00:00:00" and runs for eight
// months of 31, 30, 31, 31, 29, 31, 30 and 31 days. Four of its dates
// (02-29, 02-30, 04-31, 06-31) do not exist in a calendar year that starts
// in January, so the text cannot be parsed as a date. A time is read as a
// count of seconds from the first day instead.

// monthDays holds the length of the months 1 to 8 of the dataset calendar.
var monthDays = [...]int{31, 30, 31, 31, 29, 31, 30, 31}

// monthStart holds the number of days before each month.
var monthStart = func() (out [len(monthDays) + 1]int) {
	for i, d := range monthDays {
		out[i+1] = out[i] + d
	}
	return out
}()

// Months is the number of months the dataset calendar has, and SpanDays
// the number of days.
const (
	Months   = len(monthDays)
	SpanDays = 244
)

const daySeconds = 24 * 60 * 60

// Stamp is a time on the dataset calendar: seconds since its first day.
type Stamp int64

// StampOfDay returns the start of the given day, counted from 0.
func StampOfDay(day int) Stamp { return Stamp(day) * daySeconds }

// Day returns the day the time lies in, counted from 0.
func (t Stamp) Day() int {
	if t < 0 {
		return int((int64(t) - daySeconds + 1) / daySeconds)
	}
	return int(int64(t) / daySeconds)
}

// Days returns the time as a number of days since the first day.
func (t Stamp) Days() float64 { return float64(t) / daySeconds }

// Time places the time on a real time line whose day 0 starts at base.
func (t Stamp) Time(base time.Time) time.Time {
	return base.Add(time.Duration(t) * time.Second)
}

// StampOf is the inverse of Stamp.Time, rounded down to a second.
func StampOf(at, base time.Time) Stamp {
	d := at.Sub(base)
	s := d / time.Second
	if d < 0 && d%time.Second != 0 {
		s--
	}
	return Stamp(s)
}

// String writes the time the way the dataset does, "0001-02-30 13:05:00".
// A time outside the eight months is written as a day offset.
func (t Stamp) String() string {
	day := t.Day()
	if day < 0 || day >= SpanDays {
		return fmt.Sprintf("day %+d %s", day, clock(int(int64(t)-int64(day)*daySeconds)))
	}
	m := 0
	for m+1 < Months && day >= monthStart[m+1] {
		m++
	}
	return fmt.Sprintf("0001-%02d-%02d %s", m+1, day-monthStart[m]+1, clock(int(int64(t)%daySeconds)))
}

func clock(sec int) string {
	return fmt.Sprintf("%02d:%02d:%02d", sec/3600, sec/60%60, sec%60)
}

// ParseStamp reads a time in the form "YYYY-MM-DD hh:mm:ss" on the dataset
// calendar. The year must be 0001 and the date must exist on that calendar.
func ParseStamp(s string) (Stamp, error) {
	return parseStamp([]byte(s))
}

func parseStamp(b []byte) (Stamp, error) {
	if len(b) != 19 || b[4] != '-' || b[7] != '-' || b[10] != ' ' || b[13] != ':' || b[16] != ':' {
		return 0, fmt.Errorf("time %q is not in the form YYYY-MM-DD hh:mm:ss", b)
	}
	num := func(lo, hi int) int {
		n := 0
		for _, c := range b[lo:hi] {
			if c < '0' || c > '9' {
				return -1
			}
			n = n*10 + int(c-'0')
		}
		return n
	}
	year, month, day := num(0, 4), num(5, 7), num(8, 10)
	hour, minute, second := num(11, 13), num(14, 16), num(17, 19)
	switch {
	case year < 0 || month < 0 || day < 0 || hour < 0 || minute < 0 || second < 0:
		return 0, fmt.Errorf("time %q is not in the form YYYY-MM-DD hh:mm:ss", b)
	case year != 1:
		return 0, fmt.Errorf("time %q: the dataset calendar has the year 0001 only", b)
	case month < 1 || month > Months:
		return 0, fmt.Errorf("time %q: the dataset calendar has the months 01 to %02d", b, Months)
	case day < 1 || day > monthDays[month-1]:
		return 0, fmt.Errorf("time %q: month %02d of the dataset calendar has %d days", b, month, monthDays[month-1])
	case hour > 23 || minute > 59 || second > 59:
		return 0, fmt.Errorf("time %q: no such time of day", b)
	}
	return Stamp((monthStart[month-1]+day-1)*daySeconds + hour*3600 + minute*60 + second), nil
}
