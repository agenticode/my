package main

import (
	"bufio"
	"io"
	"io/fs"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
)

// The Go runtime lets the heap grow to twice what is in use before it
// collects. On a host with little memory that headroom is what a large
// case does not have: a scan that holds 150 MB would take 300. A soft
// limit makes the collector run earlier as the heap comes near it, and
// costs nothing while the heap is small.
//
// The limit is a quarter of the memory of the machine, or of the memory
// its control group allows when that is less, and at least minMemoryLimit.
// On a host with 16 GB it is 4 GB, which no run comes near. GOMEMLIMIT in
// the environment overrides it, as it does for every Go program.
const (
	minMemoryLimit = 64 << 20
	memoryShare    = 4
)

// setMemoryLimit applies the limit and returns it; 0 means that none was
// set: the environment sets its own, or the memory of the machine could
// not be read (it is read from /proc, so on Linux only).
func setMemoryLimit(fsys fs.FS, getenv func(string) string) int64 {
	if getenv("GOMEMLIMIT") != "" {
		return 0
	}
	total := machineMemory(fsys)
	if total <= 0 {
		return 0
	}
	limit := max(total/memoryShare, minMemoryLimit)
	debug.SetMemoryLimit(limit)
	return limit
}

// machineMemory returns the memory this process may use in bytes: MemTotal
// of /proc/meminfo, or the limit of its control group (version 2) when
// that is smaller. It returns 0 when neither can be read.
func machineMemory(fsys fs.FS) int64 {
	var total int64
	if f, err := fsys.Open("proc/meminfo"); err == nil {
		sc := bufio.NewScanner(io.LimitReader(f, 64<<10))
		for sc.Scan() {
			// "MemTotal:       16303388 kB"
			if rest, ok := strings.CutPrefix(sc.Text(), "MemTotal:"); ok {
				fields := strings.Fields(rest)
				if len(fields) == 2 && fields[1] == "kB" {
					if kb, err := strconv.ParseInt(fields[0], 10, 64); err == nil && kb > 0 && kb < 1<<40 {
						total = kb << 10
					}
				}
				break
			}
		}
		f.Close()
	}
	if b, err := fs.ReadFile(fsys, "sys/fs/cgroup/memory.max"); err == nil && len(b) < 64 {
		// "max", or a number of bytes.
		if n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil && n > 0 && (total == 0 || n < total) {
			total = n
		}
	}
	return total
}

// rootFS is the file system of the machine.
func rootFS() fs.FS { return os.DirFS("/") }
