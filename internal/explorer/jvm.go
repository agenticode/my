package explorer

import (
	"strings"
)

// JVM is one Java process and its memory.
type JVM struct {
	PID                Num      `json:"pid"`
	ProcessName        string   `json:"processName"`
	Cmdline            string   `json:"cmdline"`
	RssBytes           Num      `json:"rssBytes"`
	PssBytes           Num      `json:"pssBytes"`
	HeapUsedBytes      *Num     `json:"heapUsedBytes,omitempty"`
	HeapCommittedBytes *Num     `json:"heapCommittedBytes,omitempty"`
	HeapMaxBytes       *Num     `json:"heapMaxBytes,omitempty"`
	MetaspaceBytes     *Num     `json:"metaspaceBytes,omitempty"`
	DirectBytes        *Num     `json:"directBytes,omitempty"`
	GcCount            *Num     `json:"gcCount,omitempty"`
	GcTimeMs           *Num     `json:"gcTimeMs,omitempty"`
	Source             string   `json:"source"`
	Warnings           []string `json:"warnings"`
}

// Warning codes of a JVM.
const (
	WarnHeapCritical = "heap-critical" // heap in use above 85 % of -Xmx; the percentage follows after a colon
	WarnOffHeap      = "off-heap"      // RSS above twice the heap in use
	WarnOffXmx       = "off-xmx"       // RSS above twice -Xmx
)

var (
	rePgrepLine  = jsre(`^(\d+)\s+(.*)`, "")
	reXmx        = jsre(`-Xmx(\d+)([kmgKMG])`, "")
	reVmRSS      = jsre(`VmRSS:\s+(\d+)\s+kB`, "")
	reFlagXmx    = jsre(`-Xmx(\d+)([kmgKMG]?)`, "i")
	reFlagDirect = jsre(`-XX:MaxDirectMemorySize=(\d+)([kmgKMG]?)`, "i")
)

// JavaPID is one line of pgrep -a java.
type JavaPID struct {
	PID     Num    `json:"pid"`
	Cmdline string `json:"cmdline"`
}

// ParsePgrepJava reads "pgrep -a java".
func ParsePgrepJava(raw string) []JavaPID {
	var out []JavaPID
	for _, line := range Lines(raw) {
		if m, ok := rePgrepLine.Match(line); ok {
			if len(out) > MaxLines {
				break
			}
			out = append(out, JavaPID{PID: ParseInt(m.Group(1)), Cmdline: Trim(m.Group(2))})
		}
	}
	return out
}

func numPtr(n Num) *Num { return &n }

// ParseJstatGc reads the two lines of "jstat -gc": heap in use and
// committed, metaspace, collections and their time; -Xmx of the command
// line is the largest heap. nil when the text is not such a table.
func ParseJstatGc(pid Num, name, cmdline string, rss Num, raw string, warnings []string) *JVM {
	lines := strings.Split(Trim(raw), "\n")
	if len(lines) < 2 {
		return nil
	}
	headers := SplitSpace(Trim(lines[0]))
	values := SplitSpace(Trim(lines[1]))
	if len(headers) < 13 || len(values) < len(headers) {
		return nil
	}
	col := func(n string) Num {
		for i, h := range headers {
			if h == n {
				return Or(ParseFloat(values[i]), 0)
			}
		}
		return 0
	}
	used := (col("S0U") + col("S1U") + col("EU") + col("OU")) * 1024
	committed := (col("S0C") + col("S1C") + col("EC") + col("OC")) * 1024
	meta := col("MU") * 1024
	var heapMax *Num
	if m, ok := reXmx.Match(cmdline); ok {
		v := float64(ParseInt(m.Group(1)))
		switch lowerASCII(m.Group(2)) {
		case "g":
			heapMax = numPtr(Num(v * (1024 * 1024 * 1024)))
		case "m":
			heapMax = numPtr(Num(v * (1024 * 1024)))
		default:
			heapMax = numPtr(Num(v * 1024))
		}
	}
	gcCount := col("YGC") + col("FGC")
	gcMs := Num(Round(float64(col("GCT") * 1000)))
	if used != 0 && heapMax != nil && *heapMax != 0 && float64(used) > 0.85*float64(*heapMax) {
		warnings = append(warnings, WarnHeapCritical+":"+NumString(Round(float64(used / *heapMax * 100))))
	}
	if used != 0 && rss > used*2 {
		warnings = append(warnings, WarnOffHeap)
	}
	return &JVM{PID: pid, ProcessName: name, Cmdline: cmdline, RssBytes: rss, PssBytes: rss,
		HeapUsedBytes: numPtr(used), HeapCommittedBytes: numPtr(committed), HeapMaxBytes: heapMax,
		MetaspaceBytes: numPtr(meta), GcCount: numPtr(gcCount), GcTimeMs: numPtr(gcMs), Source: "jstat",
		Warnings: orEmpty(warnings)}
}

// jvmFlag reads a size flag of the command line; unit is the unit of a
// number without one.
func jvmFlag(re *jsRegexp, cmdline, unit string) *Num {
	m, ok := re.Match(cmdline)
	if !ok {
		return nil
	}
	v := float64(ParseInt(m.Group(1)))
	u := m.Group(2)
	if u == "" {
		u = unit
	}
	switch lowerASCII(u) {
	case "g":
		return numPtr(Num(v * (1024 * 1024 * 1024)))
	case "m":
		return numPtr(Num(v * (1024 * 1024)))
	case "k":
		return numPtr(Num(v * 1024))
	}
	return numPtr(Num(v))
}

// ParseJvmProcFallback describes a JVM from its command line alone: -Xmx
// and -XX:MaxDirectMemorySize.
func ParseJvmProcFallback(pid Num, name, cmdline string, rss Num, warnings []string) JVM {
	heapMax := jvmFlag(reFlagXmx, cmdline, "m")
	direct := jvmFlag(reFlagDirect, cmdline, "m")
	if rss != 0 && heapMax != nil && *heapMax != 0 && rss > *heapMax*2 {
		warnings = append(warnings, WarnOffXmx)
	}
	return JVM{PID: pid, ProcessName: name, Cmdline: cmdline, RssBytes: rss, PssBytes: rss, HeapMaxBytes: heapMax,
		DirectBytes: direct, Source: "proc-fallback", Warnings: orEmpty(warnings)}
}

// parseVmRSS reads VmRSS of a status text, in bytes.
func parseVmRSS(raw string) Num {
	if m, ok := reVmRSS.Match(raw); ok {
		return ParseInt(m.Group(1)) * 1024
	}
	return 0
}

// JVMInput holds what the commands of the JVM readings printed: pgrep -a
// java (whole: its first 20 lines are read), and per pid the VmRSS line
// and jstat -gc.
type JVMInput struct {
	Load  string
	Pgrep string
	VmRSS map[Num]string
	Jstat map[Num]string
}

// jvmName is the first word of the command line that ends in .jar or is
// java.
func jvmName(cmdline string) string {
	for _, p := range SplitSpace(cmdline) {
		if strings.HasSuffix(p, ".jar") || p == "java" {
			return p
		}
	}
	return "java"
}

// JvmMemory describes the Java processes: from jstat when it answers, else
// from the command line. Nothing under extreme load.
func JvmMemory(in JVMInput) []JVM {
	out := []JVM{}
	if in.Load == "extreme_load" || Trim(in.Pgrep) == "" {
		return out
	}
	for _, j := range ParsePgrepJava(headLines(in.Pgrep, 20)) {
		name := jvmName(j.Cmdline)
		rss := parseVmRSS(in.VmRSS[j.PID+0])
		if js := in.Jstat[j.PID+0]; js != "" && len(strings.Split(Trim(js), "\n")) >= 2 {
			if e := ParseJstatGc(j.PID, name, j.Cmdline, rss, js, nil); e != nil {
				out = append(out, *e)
				continue
			}
		}
		out = append(out, ParseJvmProcFallback(j.PID, name, j.Cmdline, rss, nil))
	}
	return out
}
