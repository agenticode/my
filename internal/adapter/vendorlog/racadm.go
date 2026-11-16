package vendorlog

import (
	"fmt"
	"regexp"
	"strings"
)

// splitGetSEL splits the output of racadm getsel into its records:
//
//	Record:      2
//	Date/Time:   09/13/2022 15:44:41
//	Source:      system
//	Severity:    Critical
//	Description: <the sentence of the message>
//	-------------------------------------------------------------------------------
//
// A description that runs over several lines is joined. A record without
// a date or a description is kept as malformed: it is counted, not read.
func splitGetSEL(data []byte, origin string) []entry {
	var out []entry
	var cur *entry
	var field string
	var raw []string
	flush := func() {
		if cur == nil {
			return
		}
		cur.raw = strings.Join(raw, "\n")
		switch {
		case cur.stamp == "":
			cur.malformed = "record without a Date/Time line"
		case cur.message == "":
			cur.malformed = "record without a Description line"
		}
		out = append(out, *cur)
		cur, field, raw = nil, "", nil
	}
	for _, line := range lines(data) {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "Record:") {
			flush()
			n := len(out) + 1
			cur = &entry{n: n, ref: fmt.Sprintf("%s#record/%d", origin, n)}
			if v := strings.TrimSpace(strings.TrimPrefix(t, "Record:")); v != "" {
				cur.ref = fmt.Sprintf("%s#record/%s", origin, v)
			}
			raw = append(raw, line)
			continue
		}
		if cur == nil {
			continue
		}
		if dashes(t) {
			flush()
			continue
		}
		if len(raw) >= maxRecordLines {
			cur.malformed = fmt.Sprintf("a record of more than %d lines", maxRecordLines)
			continue
		}
		raw = append(raw, line)
		key, value, ok := strings.Cut(t, ":")
		switch k := strings.TrimSpace(key); {
		case ok && k == "Date/Time":
			cur.stamp, field = strings.TrimSpace(value), k
		case ok && k == "Source":
			field = k
		case ok && k == "Severity":
			cur.severity, field = strings.TrimSpace(value), k
		case ok && k == "Description":
			cur.message, field = strings.TrimSpace(value), k
		case field == "Description" && t != "":
			cur.message += " " + t
		}
	}
	flush()
	return out
}

// argRE finds the arguments of an lclog record; the tool can print several
// of them on one line.
var argRE = regexp.MustCompile(`Message Arg\s*(\d{1,2})\s*=\s*`)

// splitLCLog splits the output of racadm lclog view into its records:
//
//	SeqNumber = 286
//	Message ID = USR0005
//	Category = Audit
//	AgentID = RACLOG
//	Severity = Information
//	Timestamp = 2017-05-15 06:25:27
//	Message = <the sentence of the message>
//	Message Arg 1 = <argument>
//	FQDD = iDRAC.Embedded.1
//
// A record starts with its SeqNumber line. A message that runs over
// several lines is joined; dashed lines between records are skipped.
func splitLCLog(data []byte, origin string) []entry {
	var out []entry
	var cur *entry
	var field string
	var raw []string
	args := map[int]string{}
	flush := func() {
		if cur == nil {
			return
		}
		cur.raw = strings.Join(raw, "\n")
		for i := 1; i <= len(args); i++ {
			v, ok := args[i]
			if !ok {
				break
			}
			cur.args = append(cur.args, v)
		}
		switch {
		case cur.id == "":
			cur.malformed = "record without a Message ID line"
		case cur.stamp == "":
			cur.malformed = "record without a Timestamp line"
		}
		out = append(out, *cur)
		cur, field, raw, args = nil, "", nil, map[int]string{}
	}
	for _, line := range lines(data) {
		t := strings.TrimSpace(line)
		key, value, ok := strings.Cut(t, "=")
		k := strings.TrimSpace(key)
		if ok && k == "SeqNumber" {
			flush()
			seq := strings.TrimSpace(value)
			cur = &entry{n: len(out) + 1, ref: fmt.Sprintf("%s#seq/%s", origin, seq)}
			raw = append(raw, line)
			continue
		}
		if cur == nil || dashes(t) || t == "" {
			continue
		}
		if len(raw) >= maxRecordLines {
			cur.malformed = fmt.Sprintf("a record of more than %d lines", maxRecordLines)
			continue
		}
		raw = append(raw, line)
		if loc := argRE.FindAllStringSubmatchIndex(t, -1); loc != nil && loc[0][0] == 0 {
			for i, m := range loc {
				end := len(t)
				if i+1 < len(loc) {
					end = loc[i+1][0]
				}
				var n int
				fmt.Sscanf(t[m[2]:m[3]], "%d", &n)
				if n >= 1 && n <= 32 {
					args[n] = strings.TrimSpace(t[m[1]:end])
				}
			}
			field = "arg"
			continue
		}
		switch {
		case ok && k == "Message ID":
			cur.id, field = strings.TrimSpace(value), k
		case ok && k == "Timestamp":
			cur.stamp, field = strings.TrimSpace(value), k
		case ok && k == "Severity":
			cur.severity, field = strings.TrimSpace(value), k
		case ok && k == "AgentID":
			cur.agent, field = strings.TrimSpace(value), k
		case ok && k == "FQDD":
			cur.fqdd, field = strings.TrimSpace(value), k
		case ok && (k == "Category" || k == "Comment"):
			field = k
		case ok && k == "Message":
			cur.message, field = strings.TrimSpace(value), k
		case field == "Message":
			cur.message += " " + t
		}
	}
	flush()
	return out
}
