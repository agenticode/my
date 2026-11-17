package explorer

// Carried over from the Memory Explorer of aterm by its author. See NOTICE.

import (
	"math"
	"sort"
	"strconv"
)

// CPUCache holds the cache sizes of one CPU package (CpuCacheInfo).
type CPUCache struct {
	Socket  Num `json:"socket"`
	L1Bytes Num `json:"l1Bytes"`
	L2Bytes Num `json:"l2Bytes"`
	L3Bytes Num `json:"l3Bytes"`
}

func (c CPUCache) any() bool { return c.L1Bytes > 0 || c.L2Bytes > 0 || c.L3Bytes > 0 }

var (
	reCacheLevel = jsre(`level=(\d+)`, "")
	reCacheSize  = jsre(`size:\s*(\d+)\s*(KiB|MiB|GiB|KB|MB|GB)`, "i")
	reCPUSection = jsre(`\*-cpu:\d+`, "")
	reCPUCut     = jsre(`\*-cpu:\d`, "")
	reCacheStart = jsre(`\*-cache:\d+`, "")
	reCacheEnd   = jsre(`\*-cache:|\*-cpu:|\*-memory:`, "")
	reSysSocket  = jsre(`SOCKET:(\d+)`, "")
	reSysLevel   = jsre(`LEVEL:(\d+)`, "")
	reSysSize    = jsre(`SIZE:(\d+)([KMG])`, "i")
)

// lshwCache reads the level and the size of one *-cache block; ok is false
// when either is missing.
func lshwCache(block string) (level, size Num, ok bool) {
	lm, ok1 := reCacheLevel.Match(block)
	sm, ok2 := reCacheSize.Match(block)
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	level = ParseInt(lm.Group(1))
	val := float64(ParseInt(sm.Group(1)))
	size = Num(val)
	switch upperASCII(sm.Group(2)) {
	case "KIB", "KB":
		size = Num(val * 1024)
	case "MIB", "MB":
		size = Num(val * 1024 * 1024)
	case "GIB", "GB":
		size = Num(val * 1024 * 1024 * 1024)
	}
	return level, size, true
}

// cutBeforeCPU is raw.split(/(?=\*-cpu:\d+)/): the text cut before every
// place where a *-cpu: node with a number starts, the start of the text
// excepted.
func cutBeforeCPU(raw string) []string {
	var parts []string
	from := 0
	for _, loc := range reCPUCut.re.FindAllStringIndex(raw, -1) {
		if loc[0] == 0 {
			continue
		}
		parts = append(parts, raw[from:loc[0]])
		from = loc[0]
	}
	return append(parts, raw[from:])
}

// ParseLshwCacheHierarchy reads the cache sizes per CPU package of lshw
// text: per *-cpu node when there are any, else by the order of the cache
// nodes (a level 1 or 2 after a level 3 starts the next package).
// parseLshwCacheHierarchy, window.ts 1200-1277.
func ParseLshwCacheHierarchy(raw string) []CPUCache {
	if Trim(raw) == "" {
		return nil
	}
	var sections []string
	for _, s := range cutBeforeCPU(raw) {
		if reCPUSection.Test(s) {
			sections = append(sections, s)
		}
		if len(sections) > MaxBlocks {
			break
		}
	}
	if len(sections) > 0 {
		var per []CPUCache
		for i, section := range sections {
			info := CPUCache{Socket: Num(i)}
			for _, b := range blocks(section, reCacheStart, reCacheEnd, MaxBlocks) {
				level, size, ok := lshwCache(b)
				if !ok {
					continue
				}
				switch level {
				case 1:
					info.L1Bytes += size
				case 2:
					info.L2Bytes = size
				case 3:
					info.L3Bytes = size
				}
			}
			if info.any() {
				per = append(per, info)
			}
		}
		if len(per) > 0 {
			return per
		}
	}
	var levels []struct{ level, size Num }
	for _, b := range blocks(raw, reCacheStart, reCacheEnd, MaxBlocks) {
		if level, size, ok := lshwCache(b); ok {
			levels = append(levels, struct{ level, size Num }{level, size})
		}
	}
	if len(levels) == 0 {
		return nil
	}
	var per []CPUCache
	cur := CPUCache{}
	sawL3 := false
	for _, cb := range levels {
		if sawL3 && cb.level < 3 && cur.any() {
			per = append(per, cur)
			cur = CPUCache{Socket: Num(len(per))}
			sawL3 = false
		}
		switch cb.level {
		case 1:
			cur.L1Bytes += cb.size
		case 2:
			cur.L2Bytes = cb.size
		case 3:
			cur.L3Bytes = cb.size
			sawL3 = true
		}
	}
	if cur.any() {
		per = append(per, cur)
	}
	return per
}

// ParseSysfsCacheInfo reads the lines "SOCKET:n LEVEL:n TYPE:t SIZE:s"
// the reference makes of sysfs: level 1 and 2 summed, level 3 the last
// one. parseSysfsCacheInfo, window.ts 1281-1311.
func ParseSysfsCacheInfo(raw string) []CPUCache {
	if Trim(raw) == "" {
		return nil
	}
	bySocket := map[float64]*CPUCache{}
	var order []float64
	for _, line := range Lines(raw) {
		sm, ok1 := reSysSocket.Match(line)
		lm, ok2 := reSysLevel.Match(line)
		zm, ok3 := reSysSize.Match(line)
		if !ok1 || !ok2 || !ok3 {
			continue
		}
		sock := float64(ParseInt(sm.Group(1)))
		level := ParseInt(lm.Group(1))
		val := float64(ParseInt(zm.Group(1)))
		size := val
		switch upperASCII(zm.Group(2)) {
		case "K":
			size = val * 1024
		case "M":
			size = val * 1024 * 1024
		case "G":
			size = val * 1024 * 1024 * 1024
		}
		info, ok := bySocket[sock]
		if !ok {
			if len(order) >= MaxBlocks {
				continue
			}
			info = &CPUCache{Socket: Num(sock)}
			bySocket[sock] = info
			order = append(order, sock)
		}
		switch level {
		case 1:
			info.L1Bytes += Num(size)
		case 2:
			info.L2Bytes += Num(size)
		case 3:
			info.L3Bytes = Num(size)
		}
	}
	out := make([]CPUCache, 0, len(order))
	for _, s := range order {
		out = append(out, *bySocket[s])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Socket < out[j].Socket })
	return out
}

func itoa(i int) string { return strconv.Itoa(i) }

func maxf(a, b float64) float64 { return math.Max(a, b) }

func ceil(x float64) float64 { return math.Ceil(x) }
