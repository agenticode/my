package klog

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// line is one kernel message with what its framing says about time, boot
// and host. The kernel log reaches a reader in several shapes:
//
//	[ 1234.567890] msg                                   dmesg
//	[ 1234.567890] host kernel: msg                      journalctl -o short-monotonic
//	<6>[ 1234.567890] msg                                dmesg -r
//	[Thu Nov 12 03:14:07 2026] msg                       dmesg -T
//	2026-11-12T03:14:07,123456+09:00 msg                 dmesg --time-format=iso
//	Nov 12 03:14:07 host kernel: [ 1234.567890] msg      syslog, journalctl
//	2026-11-12T03:14:07.123456+09:00 host kernel: msg    syslog with RFC 3339 times
//	6,1234,5678901,-;msg                                 /dev/kmsg
//	{"MESSAGE":"msg","_BOOT_ID":...}                     journalctl -o json
type line struct {
	n   int    // line number in the input, from 1
	raw string // the line as read
	msg string // the kernel message without framing

	wall    time.Time     // wall-clock time, zero when the framing has none
	mono    time.Duration // time since boot
	hasMono bool
	bootID  string // kernel boot ID (journal only)
	// bootStart is the boot time a journal entry implies: its receive time
	// minus the journal's monotonic receive time.
	bootStart time.Time
	host      string
	kernel    bool // false for lines of other programs in a syslog file
}

var (
	bracketRE = regexp.MustCompile(`^(?:<\d+>)?\[\s*(\d+)\.(\d{6})\]\s?(.*)$`)
	ctimeRE   = regexp.MustCompile(`^\[([A-Z][a-z]{2} [A-Z][a-z]{2} [ \d]\d \d\d:\d\d:\d\d \d{4})\]\s?(.*)$`)
	isoRE     = regexp.MustCompile(`^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:[.,]\d+)?(?:Z|[+-]\d\d:?\d\d)?)\s(.*)$`)
	syslogRE  = regexp.MustCompile(`^([A-Z][a-z]{2}) +(\d{1,2}) (\d\d:\d\d:\d\d)(?:\.\d+)? (\S+) (\S+?)(?:\[\d+\])?: ?(.*)$`)
	tagRE     = regexp.MustCompile(`^(\S+) (\S+?)(?:\[\d+\])?: ?(.*)$`)
	kmsgRE    = regexp.MustCompile(`^(\d+),(\d+),(\d+),[^;]*;(.*)$`)
)

var isoLayouts = []string{
	"2006-01-02T15:04:05.999999999Z07:00",
	"2006-01-02T15:04:05.999999999Z0700",
	"2006-01-02T15:04:05.999999999",
}

// maxUptimeSeconds bounds a seconds-since-boot stamp. A larger number is
// not an uptime, and would overflow a duration.
const maxUptimeSeconds = 100 * 365 * 24 * 3600

// framer turns input lines into kernel messages.
type framer struct {
	// loc is the zone of times that carry none (syslog, dmesg -T).
	loc *time.Location
	// ref is a time at or after the last line, used to give syslog times,
	// which have no year, the latest year that does not put them after it.
	ref time.Time
	// syslog is set once a "host kernel:" line was seen.
	syslog bool
	// tagged is set once a line with a stamp in brackets was followed by
	// "host kernel:": the output of journalctl -o short-monotonic, which
	// holds the lines of other programs in the same shape.
	tagged bool
}

// stamp splits a leading "[ 1234.567890] " off a message.
func stamp(s string) (rest string, mono time.Duration, ok bool) {
	m := bracketRE.FindStringSubmatch(s)
	if m == nil {
		return s, 0, false
	}
	sec, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || sec > maxUptimeSeconds {
		return s, 0, false
	}
	usec, _ := strconv.ParseInt(m[2], 10, 64)
	return m[3], time.Duration(sec)*time.Second + time.Duration(usec)*time.Microsecond, true
}

func parseISO(s string) (time.Time, bool) {
	s = strings.Replace(s, ",", ".", 1)
	for _, layout := range isoLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// year picks the year of a syslog time: the latest year in which the date
// is not after the reference time by more than a day.
func (f *framer) syslogTime(month, day, clock string) (time.Time, bool) {
	ref := f.ref
	if ref.IsZero() {
		ref = time.Now()
	}
	for y := ref.In(f.loc).Year(); y >= ref.Year()-1; y-- {
		t, err := time.ParseInLocation("2006 Jan 2 15:04:05", strconv.Itoa(y)+" "+month+" "+day+" "+clock, f.loc)
		if err != nil {
			return time.Time{}, false
		}
		if !t.After(ref.Add(24 * time.Hour)) {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// frame parses one text line. ok is false for a line that has no kernel
// message shape at all, such as a blank line.
func (f *framer) frame(n int, raw string) (l line, ok bool) {
	l = line{n: n, raw: raw, kernel: true}
	s := strings.TrimRight(raw, "\r\n")
	if strings.TrimSpace(s) == "" {
		return l, false
	}

	if rest, mono, ok := stamp(s); ok {
		l.msg, l.mono, l.hasMono = rest, mono, true
		// journalctl -o short-monotonic puts the host and the program
		// between the stamp and the message. A kernel message of plain
		// dmesg output does not start with "word kernel: ".
		if tm := tagRE.FindStringSubmatch(rest); tm != nil && !strings.Contains(tm[1], ":") {
			switch {
			case tm[2] == "kernel":
				f.tagged = true
				l.host, l.msg = tm[1], tm[3]
			case f.tagged && looksLikeTag(tm[2]):
				l.host, l.kernel, l.msg = tm[1], false, tm[3]
			}
		}
		return l, true
	}
	if m := ctimeRE.FindStringSubmatch(s); m != nil {
		if t, err := time.ParseInLocation("Mon Jan _2 15:04:05 2006", m[1], f.loc); err == nil {
			l.wall, l.msg = t.UTC(), m[2]
			return l, true
		}
	}
	if m := kmsgRE.FindStringSubmatch(s); m != nil {
		if usec, err := strconv.ParseInt(m[3], 10, 64); err == nil && usec <= maxUptimeSeconds*1e6 {
			l.msg, l.mono, l.hasMono = m[4], time.Duration(usec)*time.Microsecond, true
			return l, true
		}
	}
	if m := isoRE.FindStringSubmatch(s); m != nil {
		if t, ok := parseISO(m[1]); ok {
			l.wall = t
			rest := m[2]
			// "host kernel: msg" after the time is a syslog line. Once one
			// was seen the input is a syslog file, and lines of other
			// programs in it are not kernel messages. Before that, the
			// text after the time is the message itself, as printed by
			// dmesg --time-format=iso: "Memory failure: 0x1234: ..." must
			// not be read as host "Memory" and program "failure".
			if tm := tagRE.FindStringSubmatch(rest); tm != nil && !strings.Contains(tm[1], ":") {
				switch {
				case tm[2] == "kernel":
					f.syslog = true
					l.host, rest = tm[1], tm[3]
				case f.syslog && looksLikeTag(tm[2]):
					l.host, l.kernel, rest = tm[1], false, tm[3]
				}
			}
			l.setMessage(rest)
			return l, true
		}
	}
	if m := syslogRE.FindStringSubmatch(s); m != nil {
		if t, ok := f.syslogTime(m[1], m[2], m[3]); ok {
			l.wall, l.host = t, m[4]
			l.kernel = m[5] == "kernel"
			l.setMessage(m[6])
			return l, true
		}
	}
	// A bare message: dmesg -t, or a fragment pasted without framing.
	l.msg = s
	return l, true
}

// looksLikeTag reports whether s can be a syslog program name.
func looksLikeTag(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' || r == '/' || r == '(' || r == ')') {
			return false
		}
	}
	return s != ""
}

// setMessage stores a message that may start with a boot-relative stamp.
func (l *line) setMessage(s string) {
	if rest, mono, ok := stamp(s); ok {
		l.msg, l.mono, l.hasMono = rest, mono, true
		return
	}
	l.msg = s
}

// journalEntry is the part of a journalctl -o json object that is used.
type journalEntry struct {
	Message   json.RawMessage `json:"MESSAGE"`
	Realtime  string          `json:"__REALTIME_TIMESTAMP"`
	Monotonic string          `json:"__MONOTONIC_TIMESTAMP"`
	SrcMono   string          `json:"_SOURCE_MONOTONIC_TIMESTAMP"`
	BootID    string          `json:"_BOOT_ID"`
	Hostname  string          `json:"_HOSTNAME"`
	Transport string          `json:"_TRANSPORT"`
	Ident     string          `json:"SYSLOG_IDENTIFIER"`
}

// frameJSON parses one journal entry. The message of an entry is a string,
// or an array of byte values when it is not valid UTF-8, or null.
func frameJSON(n int, raw string) (l line, err error) {
	l = line{n: n, raw: raw}
	var e journalEntry
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		return l, err
	}
	switch {
	case len(e.Message) == 0 || string(e.Message) == "null":
	case e.Message[0] == '"':
		if err := json.Unmarshal(e.Message, &l.msg); err != nil {
			return l, err
		}
	case e.Message[0] == '[':
		var bytes []int
		if err := json.Unmarshal(e.Message, &bytes); err != nil {
			return l, err
		}
		b := make([]byte, len(bytes))
		for i, v := range bytes {
			b[i] = byte(v)
		}
		l.msg = strings.ToValidUTF8(string(b), "\uFFFD")
	default:
		return l, errors.New("MESSAGE is neither a string nor an array of byte values")
	}
	// A journal holds the messages of every program. Kernel messages have
	// transport "kernel"; an export made with -k holds nothing else.
	l.kernel = e.Transport == "kernel" || (e.Transport == "" && (e.Ident == "kernel" || e.Ident == ""))
	l.bootID, l.host = e.BootID, e.Hostname
	if us, err := strconv.ParseInt(e.Realtime, 10, 64); err == nil && us > 0 {
		l.wall = time.UnixMicro(us).UTC()
	}
	// The kernel's own stamp of the message is the source timestamp; the
	// journal's monotonic time is when journald received it.
	recv, rerr := strconv.ParseInt(e.Monotonic, 10, 64)
	if rerr == nil && recv > maxUptimeSeconds*1e6 {
		rerr = strconv.ErrRange
	}
	if rerr == nil && recv >= 0 && !l.wall.IsZero() {
		l.bootStart = l.wall.Add(-time.Duration(recv) * time.Microsecond)
	}
	if us, err := strconv.ParseInt(e.SrcMono, 10, 64); err == nil && us >= 0 && us <= maxUptimeSeconds*1e6 {
		l.mono, l.hasMono = time.Duration(us)*time.Microsecond, true
	} else if rerr == nil && recv >= 0 {
		l.mono, l.hasMono = time.Duration(recv)*time.Microsecond, true
	}
	return l, nil
}
