package explorer

// Carried over from the Memory Explorer of aterm by its author. See NOTICE.

import (
	"sort"
	"strings"
)

var (
	reCgroupV2   = jsre(`^0::(.+)$`, "m")
	reCgroupV1   = jsre(`^\d+:memory:(.+)$`, "m")
	reStatRSS    = jsre(`VmRSS:\s+(\d+)\s+kB`, "")
	reStatThread = jsre(`Threads:\s+(\d+)`, "")
	reNumaStatKV = jsre(`^(N\d+)=(\d+)$`, "")
)

// ParseCgroupPath reads the memory cgroup of /proc/<pid>/cgroup: the
// cgroup v2 line, else the memory line of v1; "/" when neither.
// The cgroup path of memory:getProcessNuma, window.ts 1591-1607.
func ParseCgroupPath(raw string) string {
	path := ""
	if raw != "" {
		if m, ok := reCgroupV2.Match(raw); ok {
			path = Trim(m.Group(1))
		} else if m, ok := reCgroupV1.Match(raw); ok {
			path = Trim(m.Group(1))
		}
	}
	if path == "" || path == "/" {
		path = "/"
	}
	return path
}

// NumaStatPaths are the two files memory.numa_stat is read from, cgroup v2
// first.
func NumaStatPaths(cgroup string) []string {
	return []string{"/sys/fs/cgroup" + cgroup + "/memory.numa_stat", "/sys/fs/cgroup/memory" + cgroup + "/memory.numa_stat"}
}

// parseCommStatus reads the text "<comm>\n---\n<VmRSS and Threads lines>".
// The comm and status of memory:getProcessNuma, window.ts 1624-1629.
func parseCommStatus(raw string, pid Num) (name string, rssKB, threads Num) {
	parts := strings.Split(raw, "---")
	name = Trim(parts[0])
	if name == "" {
		name = "PID " + NumString(float64(pid))
	}
	status := ""
	if len(parts) > 1 {
		status = parts[1]
	}
	if m, ok := reStatRSS.Match(status); ok {
		rssKB = ParseInt(m.Group(1))
	}
	if m, ok := reStatThread.Match(status); ok {
		threads = ParseInt(m.Group(1))
	}
	return name, rssKB, threads
}

// NumaStatEntry is one line of memory.numa_stat: a key and its pages per node.
type NumaStatEntry struct {
	Key   string `json:"key"`
	Nodes Record `json:"nodes"`
}

// ParseNumaStat reads memory.numa_stat ("anon N0=20090880 N1=12185600") and
// the nodes it names, in the order of their numbers.
// The numa_stat lines of memory:getProcessNuma, window.ts 1646-1673.
func ParseNumaStat(raw string) ([]NumaStatEntry, []string) {
	entries := []NumaStatEntry{}
	seen := map[string]bool{}
	var nodes []string
	for _, line := range Lines(raw) {
		t := Trim(line)
		if t == "" {
			continue
		}
		parts := SplitSpace(t)
		if len(parts) < 2 {
			continue
		}
		if len(entries) > MaxLines {
			break
		}
		var rec Record
		for _, p := range parts[1:] {
			if m, ok := reNumaStatKV.Match(p); ok {
				rec.Set(m.Group(1), ParseInt(m.Group(2)))
				if !seen[m.Group(1)] {
					seen[m.Group(1)] = true
					nodes = append(nodes, m.Group(1))
				}
			}
		}
		entries = append(entries, NumaStatEntry{Key: parts[0], Nodes: rec})
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		return float64(ParseInt(nodes[i][1:])-ParseInt(nodes[j][1:])) < 0
	})
	return entries, orEmpty(nodes)
}

// ProcessNumaResult is what memory:getProcessNuma returns.
type ProcessNumaResult struct {
	PID         Num             `json:"pid"`
	ProcessName string          `json:"processName"`
	CgroupPath  string          `json:"cgroupPath"`
	RssKB       Num             `json:"rssKB"`
	Threads     Num             `json:"threads"`
	Available   bool            `json:"available"`
	Entries     []NumaStatEntry `json:"entries"`
	Nodes       []string        `json:"nodes"`
}

// ProcessNumaInput holds what the commands of memory:getProcessNuma
// printed: /proc/<pid>/cgroup, memory.numa_stat (the text NOT_AVAILABLE
// when neither file could be read), and the comm and status text.
type ProcessNumaInput struct {
	PID        Num
	Cgroup     string
	NumaStat   string
	CommStatus string
}

// ProcessNuma describes how the memory of one process lies over the NUMA
// nodes. memory:getProcessNuma, window.ts 1577-1689.
func ProcessNuma(in ProcessNumaInput) ProcessNumaResult {
	cg := ParseCgroupPath(in.Cgroup)
	name, rss, threads := parseCommStatus(in.CommStatus, in.PID)
	r := ProcessNumaResult{PID: in.PID, ProcessName: name, CgroupPath: cg, RssKB: rss, Threads: threads,
		Entries: []NumaStatEntry{}, Nodes: []string{}}
	if in.NumaStat == "" || strings.Contains(in.NumaStat, "NOT_AVAILABLE") {
		return r
	}
	r.Available = true
	r.Entries, r.Nodes = ParseNumaStat(in.NumaStat)
	return r
}
