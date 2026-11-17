package explorer

import "strings"

// LruGeneration is one generation of a node of a memory cgroup.
type LruGeneration struct {
	Gen       Num `json:"gen"`
	Birth     Num `json:"birth"`
	AnonPages Num `json:"anonPages"`
	FilePages Num `json:"filePages"`
}

// LruNode holds the generations of one node of a memory cgroup.
type LruNode struct {
	NodeID      Num             `json:"nodeId"`
	Generations []LruGeneration `json:"generations"`
}

// LruCgroup is one memory cgroup of /sys/kernel/debug/lru_gen.
type LruCgroup struct {
	ID    Num       `json:"id"`
	Path  string    `json:"path"`
	Nodes []LruNode `json:"nodes"`
}

// MglruResult is the state of MGLRU and its generations. Error is a code:
// "debug-unreadable" when MGLRU is on and its debug file gave nothing.
type MglruResult struct {
	Enabled      bool        `json:"enabled"`
	EnabledValue string      `json:"enabledValue"`
	Cgroups      []LruCgroup `json:"cgroups"`
	Error        string      `json:"error,omitempty"`
}

// MaxLruLines is what is read of lru_gen (head -5000).
const MaxLruLines = 5000

var (
	reMemcg  = jsre(`^memcg\s+(\d+)\s+(.+)$`, "")
	reLruNod = jsre(`^\s+node\s+(\d+)`, "")
	reLruGen = jsre(`^\s+(\d+)\s+(\d+)\s+(\d+)\s+(\d+)`, "")
)

// MglruOff tells the values of /sys/kernel/mm/lru_gen/enabled that mean
// off (NOT_AVAILABLE: the file is missing).
func MglruOff(enabled string) bool {
	v := Trim(enabled)
	return v == "NOT_AVAILABLE" || v == "0x0000" || v == "0"
}

// ParseLruGen reads the memcg, node and generation lines of lru_gen.
func ParseLruGen(raw string) []LruCgroup {
	cgroups := []LruCgroup{}
	var cg *LruCgroup
	var node *LruNode
	for _, line := range Lines(raw) {
		if m, ok := reMemcg.Match(line); ok {
			cgroups = append(cgroups, LruCgroup{ID: ParseInt(m.Group(1)), Path: Trim(m.Group(2)), Nodes: []LruNode{}})
			cg = &cgroups[len(cgroups)-1]
			node = nil
			continue
		}
		if m, ok := reLruNod.Match(line); ok && cg != nil {
			cg.Nodes = append(cg.Nodes, LruNode{NodeID: ParseInt(m.Group(1)), Generations: []LruGeneration{}})
			node = &cg.Nodes[len(cg.Nodes)-1]
			continue
		}
		if m, ok := reLruGen.Match(line); ok && node != nil {
			node.Generations = append(node.Generations, LruGeneration{Gen: ParseInt(m.Group(1)), Birth: ParseInt(m.Group(2)),
				AnonPages: ParseInt(m.Group(3)), FilePages: ParseInt(m.Group(4))})
		}
	}
	return cgroups
}

// Mglru gives the state of MGLRU and its generations: off when the enabled
// value is 0x0000, 0 or missing (enabled is then "NOT_AVAILABLE"); lruGen
// is the debug file cut at 5,000 lines, empty when it could not be read.
func Mglru(enabled, lruGen string) MglruResult {
	v := Trim(enabled)
	if v == "NOT_AVAILABLE" || v == "0x0000" || v == "0" {
		return MglruResult{EnabledValue: v, Cgroups: []LruCgroup{}}
	}
	if lruGen == "" || strings.Contains(lruGen, "NOT_AVAILABLE") {
		return MglruResult{Enabled: true, EnabledValue: v, Cgroups: []LruCgroup{}, Error: "debug-unreadable"}
	}
	return MglruResult{Enabled: true, EnabledValue: v, Cgroups: ParseLruGen(headLines(lruGen, MaxLruLines))}
}
