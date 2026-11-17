package view

import (
	"context"
	"sort"
	"strings"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/explorer"
	"github.com/agenticode/my/internal/model"
)

// Explorer is the memory of a host as the kernel counted it when a scan
// ran, in the tabs of the Memory Explorer: the memory in use, the slab
// caches, the page counters and sar -B, the NUMA nodes and the state of
// MGLRU. It holds no finding; nothing in it changes a grade. A tab whose
// reading is missing is left out.
type Explorer struct {
	Host    string                   `json:"host"`
	System  *explorer.SystemSnapshot `json:"system,omitzero"`
	Hints   []ExplorerHint           `json:"hints,omitzero"`
	Slabs   []explorer.Slab          `json:"slabs,omitzero"`
	Page    *explorer.PageStats      `json:"page,omitzero"`
	Numa    []explorer.NumaNode      `json:"numa,omitzero"`
	NumaAll *NumaTotals              `json:"numa_totals,omitzero"`
	Mglru   *explorer.MglruResult    `json:"mglru,omitzero"`
	// Missing names the readings of the tabs that the ledger lacks.
	Missing []string `json:"missing"`
	// Faults names the readings a reader failed on.
	Faults []string `json:"faults,omitzero"`
}

// Empty tells an explorer with no tab to show.
func (e *Explorer) Empty() bool {
	return e == nil || e.System == nil && len(e.Slabs) == 0 && e.Page == nil && len(e.Numa) == 0 && e.Mglru == nil
}

// ExplorerHint is a hint of the explorer about a counter: a code, its
// level (critical, warning, healthy). The hints carry the thresholds of
// the explorer; no rule of the engine reads them.
type ExplorerHint struct {
	Code  string `json:"code"`
	Level string `json:"level"`
}

// NumaTotals are the sums of the numastat counters over the nodes and the
// shares the explorer judges.
type NumaTotals struct {
	HitRate   float64 `json:"hit_rate"`
	MissRate  float64 `json:"miss_rate"`
	Locality  float64 `json:"locality"`
	Remote    float64 `json:"remote"`
	Foreign   int64   `json:"foreign"`
	Spread    float64 `json:"spread"`
	HasCounts bool    `json:"has_counts"`
}

// Hints of the explorer (the diagnosis of its overview).
const (
	HintLowAvailable  = "low-available"
	HintPressureFull  = "pressure-full"
	HintPressureSome  = "pressure-some"
	HintUnreclaimable = "unreclaimable-slab"
	HintDirty         = "dirty"
	HintCache         = "file-cache"
	HintSwap          = "swap"
	HintHealthy       = "healthy"
	HintNone          = "none"
)

// Hints are the diagnosis of the overview of the explorer, with its
// thresholds: available memory below 10 %, full pressure above 1, some
// pressure above 5, unreclaimable slab above 15 % of used, dirty pages
// above 10 %, file cache above 50 %, swap above 1 GiB, and healthy with no
// swap in use and more than 20 % available.
func Hints(s *explorer.SystemSnapshot) []ExplorerHint {
	pct := func(a, b explorer.Num) float64 { return explorer.Pct(float64(a), float64(b)) }
	avail := pct(s.AvailableBytes, s.TotalBytes)
	var out []ExplorerHint
	add := func(code, level string) { out = append(out, ExplorerHint{code, level}) }
	if avail < 10 {
		add(HintLowAvailable, "critical")
	}
	if s.PsiFullAvg10 > 1 {
		add(HintPressureFull, "critical")
	}
	if s.PsiSomeAvg10 > 5 {
		add(HintPressureSome, "warning")
	}
	if pct(s.SunreclaimBytes, s.UsedBytes) > 15 {
		add(HintUnreclaimable, "warning")
	}
	if pct(s.DirtyBytes, s.TotalBytes) > 10 {
		add(HintDirty, "warning")
	}
	if pct(s.CachedBytes, s.TotalBytes) > 50 {
		add(HintCache, "healthy")
	}
	if s.SwapUsedBytes > 1<<30 {
		add(HintSwap, "warning")
	}
	if s.SwapUsedBytes == 0 && avail > 20 {
		add(HintHealthy, "healthy")
	}
	if len(out) == 0 {
		add(HintNone, "healthy")
	}
	return out
}

// KPILevel is the level of a card of the overview: available memory below
// 10 % critical and below 20 % warning; swap above 1 GiB warning; full
// pressure above 1 critical, some pressure above 5 warning.
func KPILevel(card string, s *explorer.SystemSnapshot) string {
	switch card {
	case "available":
		p := explorer.Pct(float64(s.AvailableBytes), float64(s.TotalBytes))
		if p < 10 {
			return "critical"
		}
		if p < 20 {
			return "warning"
		}
	case "swap":
		if s.SwapUsedBytes > 1<<30 {
			return "warning"
		}
	case "pressure":
		if s.PsiFullAvg10 > 1 {
			return "critical"
		}
		if s.PsiSomeAvg10 > 5 {
			return "warning"
		}
	}
	return ""
}

// MemorySegment is a part of the bar of the memory of the host.
type MemorySegment struct {
	Code  string `json:"code"`
	Bytes int64  `json:"bytes"`
}

// Segments are the parts of the memory of the host as the explorer draws
// them: anonymous, shared, page cache, reclaimable and unreclaimable slab,
// other, free; the parts of 0 bytes are left out.
func Segments(s *explorer.SystemSnapshot) []MemorySegment {
	f := func(n explorer.Num) float64 { return float64(n) }
	anon := max(0, f(s.ActiveAnonBytes)+f(s.InactiveAnonBytes)-f(s.ShmemBytes))
	shared := f(s.ShmemBytes)
	cache := max(0, f(s.CachedBytes)+f(s.BuffersBytes)-f(s.ShmemBytes))
	accounted := anon + shared + cache + f(s.SreclaimableBytes) + f(s.SunreclaimBytes) + f(s.FreeBytes)
	other := max(0, f(s.TotalBytes)-accounted)
	var out []MemorySegment
	for _, p := range []struct {
		code string
		v    float64
	}{{"anonymous", anon}, {"shared", shared}, {"page-cache", cache}, {"slab-r", f(s.SreclaimableBytes)}, {"slab-u", f(s.SunreclaimBytes)}, {"other", other}, {"free", f(s.FreeBytes)}} {
		if p.v > 0 {
			out = append(out, MemorySegment{p.code, int64(p.v)})
		}
	}
	return out
}

// CounterLevel is the class of a page counter: above 1,000,000 high,
// above 10,000 moderate.
func CounterLevel(v float64) string {
	switch {
	case v > 1_000_000:
		return "high"
	case v > 10_000:
		return "moderate"
	}
	return ""
}

// NodeLevel is the class of the bar of a NUMA node: above 90 % used
// critical, above 70 % warning.
func NodeLevel(used float64) string {
	switch {
	case used > 90:
		return "critical"
	case used > 70:
		return "warning"
	}
	return "ok"
}

// Assess is the level of a share of the numastat counters: higher is
// better above good and above fair, lower is better below good and below
// fair.
func Assess(v, good, fair float64, higher bool) string {
	if higher {
		switch {
		case v > good:
			return "ok"
		case v > fair:
			return "warn"
		}
		return "bad"
	}
	switch {
	case v < good:
		return "ok"
	case v < fair:
		return "warn"
	}
	return "bad"
}

// numaTotals sums the counters of the nodes.
func numaTotals(nodes []explorer.NumaNode) *NumaTotals {
	t := &NumaTotals{}
	var hit, miss, local, other, foreign float64
	var lo, hi float64 = 101, -1
	for _, n := range nodes {
		hit += float64(n.NumaHit)
		miss += float64(n.NumaMiss)
		local += float64(n.LocalNode)
		other += float64(n.OtherNode)
		foreign += float64(n.NumaForeign)
		u := explorer.Pct(float64(n.UsedBytes), float64(n.TotalBytes))
		lo, hi = min(lo, u), max(hi, u)
		t.HasCounts = t.HasCounts || n.NumaHit > 0 || n.LocalNode > 0
	}
	t.HitRate, t.MissRate, t.Locality, t.Remote = 100, 0, 100, 0
	if hit+miss > 0 {
		t.HitRate, t.MissRate = explorer.Pct(hit, hit+miss), explorer.Pct(miss, hit+miss)
	}
	if local+other > 0 {
		t.Locality, t.Remote = explorer.Pct(local, local+other), explorer.Pct(other, local+other)
	}
	t.Foreign = int64(foreign)
	if len(nodes) > 1 {
		t.Spread = hi - lo
	}
	return t
}

// Explorer builds the tabs of the explorer from the readings of a host.
func (c *Case) Explorer(ctx context.Context, h *engine.HostResult) (*Explorer, error) {
	rs, err := c.store.Readings(ctx, h.Host)
	if err != nil {
		return nil, err
	}
	return explorerOf(h.Host, rs), nil
}

// explorerKinds are the readings the tabs read, in the order of the tabs.
var explorerKinds = []string{model.ReadingMeminfo, model.ReadingVmstat, model.ReadingPressure, model.ReadingSlabinfo,
	model.ReadingSarB, model.ReadingNumactl, model.ReadingNumastat, model.ReadingLruGenEnabled}

func explorerOf(host string, rs []model.HostReading) *Explorer {
	e := &Explorer{Host: host, Missing: []string{}}
	text := map[string]string{}
	for _, r := range rs {
		if r.Key == "" {
			text[r.Kind] = r.Text
		}
	}
	for _, k := range explorerKinds {
		if _, ok := text[k]; !ok {
			e.Missing = append(e.Missing, k)
		}
	}
	if len(rs) == 0 {
		return e
	}
	fault := func(kind string) { e.Faults = append(e.Faults, kind) }
	if mi, ok := text[model.ReadingMeminfo]; ok {
		in := explorer.SystemInput{Meminfo: mi, Vmstat: text[model.ReadingVmstat], Slabinfo: text[model.ReadingSlabinfo]}
		if p, ok := text[model.ReadingPressure]; ok {
			in.Pressure = &p
		}
		if s := adapter.Guard(func() *explorer.SystemSnapshot { x := explorer.SystemMemory(in); return &x }); s != nil && s.TotalBytes > 0 {
			e.System = s
			e.Hints = Hints(s)
		} else {
			fault(model.ReadingMeminfo)
		}
	}
	if sl, ok := text[model.ReadingSlabinfo]; ok {
		slabs := adapter.Guard(func() []explorer.Slab { return explorer.ParseSlabinfo(sl) })
		sort.SliceStable(slabs, func(i, j int) bool { return slabs[i].TotalSize > slabs[j].TotalSize })
		if len(slabs) > 20 {
			slabs = slabs[:20]
		}
		e.Slabs = slabs
	}
	vm, hasVM := text[model.ReadingVmstat]
	sar, hasSar := text[model.ReadingSarB]
	if hasVM || hasSar {
		if !hasSar {
			sar = "NOT_AVAILABLE\n"
		}
		if p := adapter.Guard(func() *explorer.PageStats { x := explorer.PageStatsOf(vm, sar); return &x }); p != nil {
			e.Page = p
		} else {
			fault(model.ReadingVmstat)
		}
	}
	if hw, ok := text[model.ReadingNumactl]; ok {
		nodes := adapter.Guard(func() []explorer.NumaNode { return explorer.ParseNumaInfo(hw, text[model.ReadingNumastat]) })
		if len(nodes) > explorer.MaxNodes {
			nodes = nil
			fault(model.ReadingNumactl)
		}
		e.Numa = nodes
		if len(nodes) > 0 {
			e.NumaAll = numaTotals(nodes)
		}
	}
	if en, ok := text[model.ReadingLruGenEnabled]; ok {
		state := strings.TrimSpace(en)
		e.Mglru = &explorer.MglruResult{Enabled: !explorer.MglruOff(state), EnabledValue: state, Cgroups: []explorer.LruCgroup{}}
	}
	return e
}
