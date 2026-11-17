package explorer

// Carried over from the Memory Explorer of aterm by its author. See NOTICE.

import "sort"

// NumaNode is one NUMA node as the reference describes it (NumaNodeInfo).
type NumaNode struct {
	NodeID        Num `json:"nodeId"`
	TotalBytes    Num `json:"totalBytes"`
	FreeBytes     Num `json:"freeBytes"`
	UsedBytes     Num `json:"usedBytes"`
	NumaHit       Num `json:"numaHit"`
	NumaMiss      Num `json:"numaMiss"`
	NumaForeign   Num `json:"numaForeign"`
	InterleaveHit Num `json:"interleaveHit"`
	LocalNode     Num `json:"localNode"`
	OtherNode     Num `json:"otherNode"`
}

var (
	reNumaSize   = jsre(`node\s+(\d+)\s+size:\s+(\d+)\s+MB`, "i")
	reNumaFree   = jsre(`node\s+(\d+)\s+free:\s+(\d+)\s+MB`, "i")
	reNumaHeader = jsre(`node(\d+)`, "i")
)

// numaFields are the rows of numastat the reference reads.
var numaFields = map[string]func(*NumaNode) *Num{
	"numa_hit":       func(n *NumaNode) *Num { return &n.NumaHit },
	"numa_miss":      func(n *NumaNode) *Num { return &n.NumaMiss },
	"numa_foreign":   func(n *NumaNode) *Num { return &n.NumaForeign },
	"interleave_hit": func(n *NumaNode) *Num { return &n.InterleaveHit },
	"local_node":     func(n *NumaNode) *Num { return &n.LocalNode },
	"other_node":     func(n *NumaNode) *Num { return &n.OtherNode },
}

// ParseNumaInfo reads the node sizes of "numactl --hardware" and the
// counters of numastat. Only the first line of the numastat text names the
// nodes; a second block of nodes is read under the names of the first.
// Without numactl text there are no nodes. parseNumaInfo, window.ts
// 1099-1198.
func ParseNumaInfo(numaHw, numaStat string) []NumaNode {
	if Trim(numaHw) == "" {
		return nil
	}
	nodes := map[float64]*NumaNode{}
	node := func(id float64) *NumaNode {
		n, ok := nodes[id]
		if !ok {
			if len(nodes) > MaxNodes {
				return &NumaNode{}
			}
			n = &NumaNode{NodeID: Num(id)}
			nodes[id] = n
		}
		return n
	}
	for _, m := range reNumaSize.All(numaHw) {
		node(float64(ParseInt(m.Group(1)))).TotalBytes = Num(float64(ParseInt(m.Group(2))) * 1024 * 1024)
	}
	for _, m := range reNumaFree.All(numaHw) {
		node(float64(ParseInt(m.Group(1)))).FreeBytes = Num(float64(ParseInt(m.Group(2))) * 1024 * 1024)
	}
	if Trim(numaStat) != "" {
		var lines []string
		for _, l := range Lines(numaStat) {
			if l = Trim(l); l != "" {
				lines = append(lines, l)
			}
		}
		var ids []float64
		if len(lines) > 0 {
			for _, m := range reNumaHeader.All(lines[0]) {
				ids = append(ids, float64(ParseInt(m.Group(1))))
			}
		}
		for _, line := range lines[min(1, len(lines)):] {
			parts := SplitSpace(line)
			if len(parts) < 2 {
				continue
			}
			field, known := numaFields[parts[0]]
			if !known && !protoKeys[parts[0]] {
				continue
			}
			for j := 0; j < len(ids) && j+1 < len(parts); j++ {
				n := node(ids[j])
				if known {
					*field(n) = Or(ParseInt(parts[j+1]), 0)
				}
			}
		}
	}
	ids := make([]float64, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Float64s(ids)
	out := make([]NumaNode, 0, len(ids))
	for _, id := range ids {
		n := *nodes[id]
		n.UsedBytes = n.TotalBytes - n.FreeBytes
		out = append(out, n)
	}
	return out
}
