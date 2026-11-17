package explorer

import (
	"math"
	"sort"
	"strings"
)

// Process is one process and its memory.
type Process struct {
	PID         Num     `json:"pid"`
	Name        string  `json:"name"`
	Cmdline     string  `json:"cmdline"`
	RssBytes    Num     `json:"rssBytes"`
	PssBytes    Num     `json:"pssBytes"`
	SwapBytes   Num     `json:"swapBytes"`
	AnonBytes   Num     `json:"anonBytes"`
	FileBytes   Num     `json:"fileBytes"`
	SharedBytes Num     `json:"sharedBytes"`
	ThreadCount Num     `json:"threadCount"`
	CgroupPath  *string `json:"cgroupPath,omitempty"`
}

// MaxProcesses is the number of processes kept, the largest first.
const MaxProcesses = 100

var (
	rePID    = jsre(`PID:(\d+)`, "")
	reName   = jsre(`NAME:([^\n]*)`, "")
	reCmd    = jsre(`CMD:([^\n]*)`, "")
	reCgroup = jsre(`CGROUP:([^\n]*)`, "")
	statusKb = map[string]*jsRegexp{}
	statusN  = map[string]*jsRegexp{}
)

func init() {
	for _, k := range []string{"VmRSS", "VmSwap", "RssAnon", "RssFile", "RssShmem"} {
		statusKb[k] = jsre(`^`+k+`:\s+(\d+)\s+kB`, "m")
	}
	statusN["Threads"] = jsre(`^Threads:\s+(\d+)`, "m")
}

// ParsePsOutput reads "ps -eo pid,rss,vsz,comm": RSS in bytes, PSS taken as
// RSS.
func ParsePsOutput(raw string) []Process {
	var out []Process
	for _, line := range Lines(raw) {
		parts := SplitSpace(Trim(line))
		if len(parts) < 4 {
			continue
		}
		pid := ParseInt(parts[0])
		rss := ParseInt(parts[1]) * 1024
		if pid == 0 || math.IsNaN(float64(pid)) {
			continue
		}
		if len(out) > MaxLines {
			break
		}
		out = append(out, Process{PID: pid, Name: parts[3], RssBytes: rss, PssBytes: rss})
	}
	return out
}

// parseStatusKb reads "Key:  N kB" of a status block, in bytes.
func parseStatusKb(block, key string) Num {
	if m, ok := statusKb[key].Match(block); ok {
		return ParseInt(m.Group(1)) * 1024
	}
	return 0
}

// parseStatusInt reads "Key:  N" of a status block.
func parseStatusInt(block, key string) Num {
	if m, ok := statusN[key].Match(block); ok {
		return ParseInt(m.Group(1))
	}
	return 0
}

// ParseStatusEntries reads the blocks "PID:n NAME:c CMD:..." with the
// status lines of each process, separated by "---": processes without a
// name or without resident memory are left out; the 100 largest by RSS.
func ParseStatusEntries(raw string) []Process {
	var out []Process
	for _, block := range strings.Split(raw, "---") {
		if Trim(block) == "" {
			continue
		}
		pm, ok := rePID.Match(block)
		if !ok {
			continue
		}
		name, cmd := "", ""
		if m, ok := reName.Match(block); ok {
			name = Trim(m.Group(1))
		}
		if m, ok := reCmd.Match(block); ok {
			cmd = Trim(m.Group(1))
		}
		if name == "" {
			continue
		}
		rss := parseStatusKb(block, "VmRSS")
		if rss == 0 {
			continue
		}
		p := Process{
			PID: ParseInt(pm.Group(1)), Name: name, Cmdline: cmd, RssBytes: rss, PssBytes: rss,
			SwapBytes: parseStatusKb(block, "VmSwap"), AnonBytes: parseStatusKb(block, "RssAnon"),
			FileBytes: parseStatusKb(block, "RssFile"), SharedBytes: parseStatusKb(block, "RssShmem"),
			ThreadCount: parseStatusInt(block, "Threads"),
		}
		if m, ok := reCgroup.Match(block); ok {
			cg := Trim(m.Group(1))
			p.CgroupPath = &cg
		}
		out = append(out, p)
	}
	sortByRss(out)
	if len(out) > MaxProcesses {
		out = out[:MaxProcesses]
	}
	return out
}

// sortByRss sorts largest first, keeping the order of equal ones;
// b - a of NaN counts as equal, as sort() of JavaScript takes it.
func sortByRss(p []Process) {
	sort.SliceStable(p, func(i, j int) bool { return float64(p[j].RssBytes-p[i].RssBytes) < 0 })
}

// ParsePssLines reads the lines "<pid> <pss kB>" into PSS in bytes per pid:
// a line without a pid or without a PSS above 0 is left out.
func ParsePssLines(raw string) map[Num]Num {
	m := map[Num]Num{}
	for _, line := range Lines(raw) {
		parts := SplitSpace(Trim(line))
		if len(parts) < 2 {
			continue
		}
		pid, kb := ParseInt(parts[0]), ParseInt(parts[1])
		if pid != 0 && !math.IsNaN(float64(pid)) && kb > 0 {
			m[pid+0] = kb * 1024
		}
	}
	return m
}

// ProcessInput holds what the commands of the process readings printed.
type ProcessInput struct {
	Load   string // normal, high_load or extreme_load
	Status string // the status blocks of the thread group leaders
	Ps     string // ps -eo pid,rss,vsz,comm --sort=-rss --no-headers, whole: the first 50 lines are read
	Pss    string // the lines "<pid> <pss kB>" of the first 30
}

// PssPIDs are the processes whose PSS is read: the first 30.
func PssPIDs(entries []Process) []Num {
	var out []Num
	for i, e := range entries {
		if i == 30 {
			break
		}
		out = append(out, e.PID)
	}
	return out
}

// ProcessMemory gives the processes: under load from ps alone; else from
// the status blocks, from ps when they give nothing, with the PSS of the
// first 30.
func ProcessMemory(in ProcessInput) []Process {
	in.Ps = headLines(in.Ps, 50)
	if in.Load != "normal" {
		if in.Ps == "" {
			return []Process{}
		}
		return orEmpty(ParsePsOutput(in.Ps))
	}
	var entries []Process
	if in.Status != "" && strings.Contains(in.Status, "PID:") {
		entries = ParseStatusEntries(in.Status)
	}
	if len(entries) == 0 && in.Ps != "" {
		entries = ParsePsOutput(in.Ps)
	}
	if len(entries) > 0 && in.Pss != "" {
		pss := ParsePssLines(in.Pss)
		for i := range entries {
			if v, ok := pss[entries[i].PID+0]; ok && v != 0 && !math.IsNaN(float64(v)) {
				entries[i].PssBytes = v
			}
		}
	}
	return orEmpty(entries)
}

// MaxLines bounds the lines of a listing a reader keeps.
const MaxLines = 4096
