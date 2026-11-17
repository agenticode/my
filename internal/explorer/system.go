package explorer

import "strings"

var (
	reMeminfoLine = jsre(`^([^:]+):\s+(\d+)(?:\s+kB)?`, "")
	reAvg10       = jsre(`avg10=([\d.]+)`, "")
	reAvg60       = jsre(`avg60=([\d.]+)`, "")
)

// ParseMeminfo reads /proc/meminfo: kB fields in bytes, the counts of huge
// pages as they are.
func ParseMeminfo(raw string) Record {
	var r Record
	for _, line := range Lines(raw) {
		m, ok := reMeminfoLine.Match(line)
		if !ok {
			continue
		}
		v := ParseInt(m.Group(2))
		if strings.Contains(line, "kB") {
			v *= 1024
		}
		r.Set(Trim(m.Group(1)), v)
	}
	return r
}

// Vmstat holds the four counters of /proc/vmstat the overview shows.
type Vmstat struct {
	Pswpin     Num `json:"pswpin"`
	Pswpout    Num `json:"pswpout"`
	Pgfault    Num `json:"pgfault"`
	Pgmajfault Num `json:"pgmajfault"`
}

// ParseVmstat reads lines of two fields of /proc/vmstat.
func ParseVmstat(raw string) Vmstat {
	var r Record
	for _, line := range Lines(raw) {
		if parts := SplitSpace(Trim(line)); len(parts) == 2 {
			r.Set(parts[0], Or(ParseInt(parts[1]), 0))
		}
	}
	return Vmstat{Pswpin: r.Or0("pswpin"), Pswpout: r.Or0("pswpout"), Pgfault: r.Or0("pgfault"), Pgmajfault: r.Or0("pgmajfault")}
}

// Psi holds the averages of /proc/pressure/memory.
type Psi struct {
	SomeAvg10 Num `json:"someAvg10"`
	SomeAvg60 Num `json:"someAvg60"`
	FullAvg10 Num `json:"fullAvg10"`
	FullAvg60 Num `json:"fullAvg60"`
}

// ParsePsi reads /proc/pressure/memory; the text "N/A" stands for a kernel
// without it.
func ParsePsi(raw string) Psi {
	var out Psi
	if Trim(raw) == "N/A" {
		return out
	}
	val := func(re *jsRegexp, line string) Num {
		if m, ok := re.Match(line); ok && m.Group(1) != "" {
			return ParseFloat(m.Group(1))
		}
		return ParseFloat("0")
	}
	for _, line := range Lines(raw) {
		switch {
		case strings.HasPrefix(line, "some"):
			out.SomeAvg10, out.SomeAvg60 = val(reAvg10, line), val(reAvg60, line)
		case strings.HasPrefix(line, "full"):
			out.FullAvg10, out.FullAvg60 = val(reAvg10, line), val(reAvg60, line)
		}
	}
	return out
}

// Slab is one line of /proc/slabinfo.
type Slab struct {
	Name       string `json:"name"`
	ActiveObjs Num    `json:"activeObjs"`
	TotalObjs  Num    `json:"totalObjs"`
	ObjSize    Num    `json:"objSize"`
	TotalSize  Num    `json:"totalSize"`
}

// MaxSlabs bounds the lines of slabinfo read.
const MaxSlabs = 4096

// ParseSlabinfo reads the lines of /proc/slabinfo, head lines skipped.
func ParseSlabinfo(raw string) []Slab {
	var out []Slab
	for _, line := range Lines(raw) {
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "slabinfo") || strings.HasPrefix(line, "name") {
			continue
		}
		parts := SplitSpace(Trim(line))
		if len(parts) < 4 {
			continue
		}
		if len(out) > MaxSlabs {
			break
		}
		total, size := Or(ParseInt(parts[2]), 0), Or(ParseInt(parts[3]), 0)
		out = append(out, Slab{Name: parts[0], ActiveObjs: Or(ParseInt(parts[1]), 0), TotalObjs: total, ObjSize: size, TotalSize: total * size})
	}
	return out
}

// SystemSnapshot holds the memory counters of the host.
type SystemSnapshot struct {
	TotalBytes        Num    `json:"totalBytes"`
	FreeBytes         Num    `json:"freeBytes"`
	AvailableBytes    Num    `json:"availableBytes"`
	UsedBytes         Num    `json:"usedBytes"`
	CachedBytes       Num    `json:"cachedBytes"`
	BuffersBytes      Num    `json:"buffersBytes"`
	ActiveFileBytes   Num    `json:"activeFileBytes"`
	InactiveFileBytes Num    `json:"inactiveFileBytes"`
	ActiveAnonBytes   Num    `json:"activeAnonBytes"`
	InactiveAnonBytes Num    `json:"inactiveAnonBytes"`
	SlabBytes         Num    `json:"slabBytes"`
	SreclaimableBytes Num    `json:"sreclaimableBytes"`
	SunreclaimBytes   Num    `json:"sunreclaimBytes"`
	DirtyBytes        Num    `json:"dirtyBytes"`
	WritebackBytes    Num    `json:"writebackBytes"`
	ShmemBytes        Num    `json:"shmemBytes"`
	SwapTotalBytes    Num    `json:"swapTotalBytes"`
	SwapFreeBytes     Num    `json:"swapFreeBytes"`
	SwapUsedBytes     Num    `json:"swapUsedBytes"`
	PsiSomeAvg10      Num    `json:"psiSomeAvg10"`
	PsiSomeAvg60      Num    `json:"psiSomeAvg60"`
	PsiFullAvg10      Num    `json:"psiFullAvg10"`
	PsiFullAvg60      Num    `json:"psiFullAvg60"`
	HugePagesTotal    Num    `json:"hugePagesTotal"`
	HugePagesFree     Num    `json:"hugePagesFree"`
	HugePageSize      Num    `json:"hugePageSize"`
	Vmstat            Vmstat `json:"vmstat"`
	TopSlabs          []Slab `json:"topSlabs"`
}

// SystemInput holds the files SystemMemory reads. Vmstat and
// Slabinfo are the files as the kernel writes them: SystemMemory makes the
// choice of lines a shell pipe would make. Pressure is nil
// on a kernel without the file.
type SystemInput struct {
	Meminfo, Vmstat, Slabinfo string
	Pressure                  *string
}

// SystemMemory puts the memory counters together; used memory is what is
// neither free, buffers, page cache nor reclaimable slab, at least 0.
func SystemMemory(in SystemInput) SystemSnapshot {
	mi := ParseMeminfo(in.Meminfo)
	vm := ParseVmstat(GrepE(in.Vmstat, "pswpin", "pswpout", "pgfault", "pgmajfault", "nr_dirty", "nr_writeback"))
	psiText := "N/A\n"
	if in.Pressure != nil {
		psiText = *in.Pressure
	}
	psi := ParsePsi(psiText)
	slabs := ParseSlabinfo(SlabTop(in.Slabinfo))
	g := mi.Or0
	total, free, avail, cached, buffers, srecl := g("MemTotal"), g("MemFree"), g("MemAvailable"), g("Cached"), g("Buffers"), g("SReclaimable")
	used := Num(maxf(0, float64(total-free-buffers-cached-srecl)))
	swapTotal, swapFree := g("SwapTotal"), g("SwapFree")
	return SystemSnapshot{
		TotalBytes: total, FreeBytes: free, AvailableBytes: avail, UsedBytes: used, CachedBytes: cached, BuffersBytes: buffers,
		ActiveFileBytes: g("Active(file)"), InactiveFileBytes: g("Inactive(file)"), ActiveAnonBytes: g("Active(anon)"),
		InactiveAnonBytes: g("Inactive(anon)"), SlabBytes: g("Slab"), SreclaimableBytes: srecl, SunreclaimBytes: g("SUnreclaim"),
		DirtyBytes: g("Dirty"), WritebackBytes: g("Writeback"), ShmemBytes: g("Shmem"), SwapTotalBytes: swapTotal,
		SwapFreeBytes: swapFree, SwapUsedBytes: swapTotal - swapFree, PsiSomeAvg10: psi.SomeAvg10, PsiSomeAvg60: psi.SomeAvg60,
		PsiFullAvg10: psi.FullAvg10, PsiFullAvg60: psi.FullAvg60, HugePagesTotal: g("HugePages_Total"),
		HugePagesFree: g("HugePages_Free"), HugePageSize: g("Hugepagesize"), Vmstat: vm, TopSlabs: orEmpty(slabs),
	}
}
