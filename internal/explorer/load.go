package explorer

import "strings"

// Load profiles of a host.
const (
	LoadNormal  = "normal"
	LoadHigh    = "high_load"
	LoadExtreme = "extreme_load"
)

// Thresholds of the load guard. A host above any one of them is loaded:
// high load skips the heavy readings, extreme load the JVMs as well.
const (
	HighEstab       = 50000
	HighCPURatio    = 0.75
	HighMemUsedPct  = 90
	ExtremeEstab    = 200000
	ExtremeCPURatio = 1.5
	ExtremeMemPct   = 95
	// FdWalkEstab is the number of established TCP sessions above which
	// the walk over open files is not made.
	FdWalkEstab = 30000
)

// LoadReason names one threshold a host is above.
type LoadReason struct {
	Kind  string `json:"kind"` // estab, cpu or mem
	Value Num    `json:"value"`
	Limit Num    `json:"limit"`
}

// ServerLoadResult is what detectServerLoad returns.
type ServerLoadResult struct {
	Profile      string       `json:"profile"`
	EstabCount   Num          `json:"estabCount"`
	CPULoadRatio Num          `json:"cpuLoadRatio"`
	MemUsedPct   Num          `json:"memUsedPct"`
	Reasons      []LoadReason `json:"reasons"`
}

var (
	reEstab    = jsre(`estab\s+(\d+)`, "")
	reMemTotal = jsre(`MemTotal:\s+(\d+)`, "")
	reMemAvail = jsre(`MemAvailable:\s+(\d+)`, "")
)

// LoadText is the text the command of detectServerLoad prints: the estab
// line of "ss -s", /proc/loadavg, nproc and the two lines of meminfo, with
// its markers between them.
func LoadText(ssEstab, loadavg, nproc, meminfo string) string {
	return ssEstab + "---LOADAVG---\n" + loadavg + "---NPROC---\n" + nproc + "---MEM---\n" + meminfo
}

// after is s.split(sep)[1]: the part between the first sep and the next.
func after(s, sep string) (string, bool) {
	parts := strings.Split(s, sep)
	if len(parts) < 2 {
		return "", false
	}
	return parts[1], true
}

// before returns the part of s before the first sep (all of s without one).
func before(s, sep string) string {
	if i := strings.Index(s, sep); i >= 0 {
		return s[:i]
	}
	return s
}

// ServerLoad reads the load of a host from the text LoadText makes and
// sorts it into a profile.
func ServerLoad(text string) ServerLoadResult {
	raw := Trim(text)
	var estab Num
	if m, ok := reEstab.Match(raw); ok {
		estab = ParseInt(m.Group(1))
	}
	var cpu Num
	loadPart, nprocPart := "", ""
	if a, ok := after(raw, "---LOADAVG---"); ok {
		loadPart = Trim(before(a, "---NPROC---"))
	}
	if a, ok := after(raw, "---NPROC---"); ok {
		nprocPart = Trim(before(a, "---MEM---"))
	}
	var load1 Num
	if loadPart != "" {
		load1 = ParseFloat(SplitSpace(loadPart)[0])
	}
	nproc := Num(1)
	if nprocPart != "" {
		nproc = ParseInt(nprocPart)
	}
	if nproc > 0 && load1 > 0 {
		cpu = load1 / nproc
	}
	var mem Num
	memPart := ""
	if a, ok := after(raw, "---MEM---"); ok {
		memPart = Trim(a)
	}
	tm, ok1 := reMemTotal.Match(memPart)
	am, ok2 := reMemAvail.Match(memPart)
	if ok1 && ok2 {
		total, avail := ParseInt(tm.Group(1)), ParseInt(am.Group(1))
		if total > 0 {
			mem = (total - avail) / total * 100
		}
	}
	r := ServerLoadResult{Profile: LoadNormal, EstabCount: estab, CPULoadRatio: cpu, MemUsedPct: mem, Reasons: []LoadReason{}}
	check := func(profile, kind string, v, limit Num) {
		if v > limit {
			r.Profile = profile
			r.Reasons = append(r.Reasons, LoadReason{Kind: kind, Value: v, Limit: limit})
		}
	}
	check(LoadExtreme, "estab", estab, ExtremeEstab)
	check(LoadExtreme, "cpu", cpu, ExtremeCPURatio)
	check(LoadExtreme, "mem", mem, ExtremeMemPct)
	if r.Profile == LoadNormal {
		check(LoadHigh, "estab", estab, HighEstab)
		check(LoadHigh, "cpu", cpu, HighCPURatio)
		check(LoadHigh, "mem", mem, HighMemUsedPct)
	}
	return r
}

// FdWalkDangerous tells a host on which the walk over open files is not
// made.
func FdWalkDangerous(tcpEstablished Num) bool { return tcpEstablished > FdWalkEstab }
