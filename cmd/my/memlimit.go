package main

import (
	"bufio"
	"io"
	"io/fs"
	"os"
	"path"
	"runtime/debug"
	"slices"
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
// the control groups of the process allow when that is less (a container,
// a systemd unit with MemoryMax=), and at least minMemoryLimit.
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
// of /proc/meminfo, or the smallest limit of its control groups when that
// is smaller. It returns 0 when neither can be read.
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
	if n := cgroupMemory(fsys); n > 0 && (total == 0 || n < total) {
		total = n
	}
	return total
}

// cgroupMemory returns the smallest memory limit of the control group of
// the process and of the groups above it, or 0 when none sets one.
// /proc/self/cgroup names the group (cgroups(7)): the line "0::/path" in
// version 2, the line of the memory controller ("4:memory:/path") in
// version 1 and on hosts that mount both. The path is "/" in a container
// with a group of its own and "/system.slice/x.service" for a systemd
// unit. Version 2 keeps the limit in memory.max, version 1 in
// memory/<path>/memory.limit_in_bytes, whose "no limit" is a number larger
// than any machine. Inside a container the path of the host may not exist
// below the mount: the walk up ends at the container's own group.
func cgroupMemory(fsys fs.FS) int64 {
	v2, v1 := "/", "/"
	if f, err := fsys.Open("proc/self/cgroup"); err == nil {
		sc := bufio.NewScanner(io.LimitReader(f, 64<<10))
		for sc.Scan() {
			fields := strings.SplitN(sc.Text(), ":", 3)
			if len(fields) != 3 || !strings.HasPrefix(fields[2], "/") {
				continue
			}
			switch {
			case fields[0] == "0" && fields[1] == "":
				v2 = path.Clean(fields[2])
			case slices.Contains(strings.Split(fields[1], ","), "memory"):
				v1 = path.Clean(fields[2])
			}
		}
		f.Close()
	}
	least := groupLimit(fsys, "sys/fs/cgroup", v2, "memory.max")
	if n := groupLimit(fsys, "sys/fs/cgroup/memory", v1, "memory.limit_in_bytes"); n > 0 && (least == 0 || n < least) {
		least = n
	}
	return least
}

// groupLimit returns the smallest number in the file name of the group
// below base and of the groups above it, or 0 when no file holds one.
func groupLimit(fsys fs.FS, base, group, name string) int64 {
	var least int64
	for depth := 0; depth < 64; depth++ {
		if b, err := fs.ReadFile(fsys, path.Join(base, group, name)); err == nil && len(b) < 64 {
			// "max", or a number of bytes.
			if n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil && n > 0 && (least == 0 || n < least) {
				least = n
			}
		}
		if group == "/" {
			break
		}
		group = path.Dir(group)
	}
	return least
}

// rootFS is the file system of the machine.
func rootFS() fs.FS { return os.DirFS("/") }
