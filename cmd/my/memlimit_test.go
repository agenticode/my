package main

import (
	"runtime/debug"
	"testing"
	"testing/fstest"
)

func TestMemoryLimit(t *testing.T) {
	// The limit of the process is restored when the test ends.
	before := debug.SetMemoryLimit(-1)
	t.Cleanup(func() { debug.SetMemoryLimit(before) })

	none := func(string) string { return "" }
	meminfo := func(text string) fstest.MapFS { return fstest.MapFS{"proc/meminfo": {Data: []byte(text)}} }
	for name, tc := range map[string]struct {
		fsys fstest.MapFS
		want int64
	}{
		"a cloud host with 765 MB":  {meminfo("MemTotal:         783360 kB\nMemFree:          100000 kB\n"), 783360 << 10 / 4},
		"a server with 512 GB":      {meminfo("MemTotal:       536870912 kB\n"), 512 << 30 / 4},
		"a host with 128 MB":        {meminfo("MemTotal:         131072 kB\n"), 64 << 20},
		"no /proc (not Linux)":      {fstest.MapFS{}, 0},
		"a file that is no meminfo": {meminfo("hello\nMemTotal: lots\n"), 0},
		"another unit":              {meminfo("MemTotal: 16 GB\n"), 0},
		"a control group with 1 GiB on a large host": {fstest.MapFS{"proc/meminfo": {Data: []byte("MemTotal: 67108864 kB\n")},
			"sys/fs/cgroup/memory.max": {Data: []byte("1073741824\n")}}, 1 << 30 / 4},
		"a control group without a limit": {fstest.MapFS{"proc/meminfo": {Data: []byte("MemTotal: 4194304 kB\n")},
			"sys/fs/cgroup/memory.max": {Data: []byte("max\n")}}, 1 << 30},
		"a control group that allows more than the machine has": {fstest.MapFS{"proc/meminfo": {Data: []byte("MemTotal: 4194304 kB\n")},
			"sys/fs/cgroup/memory.max": {Data: []byte("9223372036854771712\n")}}, 1 << 30},
		"a systemd unit with MemoryMax=512M": {fstest.MapFS{"proc/meminfo": {Data: []byte("MemTotal: 16777216 kB\n")},
			"proc/self/cgroup":                                          {Data: []byte("0::/system.slice/my-exporter.service\n")},
			"sys/fs/cgroup/system.slice/memory.max":                     {Data: []byte("max\n")},
			"sys/fs/cgroup/system.slice/my-exporter.service/memory.max": {Data: []byte("536870912\n")}}, 512 << 20 / 4},
		"a slice that allows less than its unit": {fstest.MapFS{"proc/meminfo": {Data: []byte("MemTotal: 16777216 kB\n")},
			"proc/self/cgroup":                                        {Data: []byte("0::/work.slice/my-exporter.service\n")},
			"sys/fs/cgroup/work.slice/memory.max":                     {Data: []byte("402653184\n")},
			"sys/fs/cgroup/work.slice/my-exporter.service/memory.max": {Data: []byte("max\n")}}, 384 << 20 / 4},
		"control groups of version 1 without a limit file": {fstest.MapFS{"proc/meminfo": {Data: []byte("MemTotal: 4194304 kB\n")},
			"proc/self/cgroup": {Data: []byte("12:memory:/user.slice\n1:name=systemd:/user.slice/session-3.scope\n")}}, 1 << 30},
		"a container of version 1 with 512 MiB on a large host": {fstest.MapFS{"proc/meminfo": {Data: []byte("MemTotal: 65808384 kB\n")},
			"proc/self/cgroup": {Data: []byte("11:memory:/docker/0123abcd\n10:cpu,cpuacct:/docker/0123abcd\n")},
			// The container sees its own group at the mount point.
			"sys/fs/cgroup/memory/memory.limit_in_bytes": {Data: []byte("536870912\n")}}, 512 << 20 / 4},
		"version 1 without a limit": {fstest.MapFS{"proc/meminfo": {Data: []byte("MemTotal: 4194304 kB\n")},
			"proc/self/cgroup":                           {Data: []byte("4:memory:/\n")},
			"sys/fs/cgroup/memory/memory.limit_in_bytes": {Data: []byte("9223372036854771712\n")}}, 1 << 30},
		"a hybrid host: a unit with MemoryMax=512M": {fstest.MapFS{"proc/meminfo": {Data: []byte("MemTotal: 16777216 kB\n")},
			"proc/self/cgroup": {Data: []byte("0::/system.slice/my-exporter.service\n6:memory:/system.slice/my-exporter.service\n1:name=systemd:/system.slice/my-exporter.service\n")},
			"sys/fs/cgroup/memory/system.slice/my-exporter.service/memory.limit_in_bytes": {Data: []byte("536870912\n")},
			"sys/fs/cgroup/memory/memory.limit_in_bytes":                                  {Data: []byte("9223372036854771712\n")}}, 512 << 20 / 4},
		"a controller list that holds memory": {fstest.MapFS{"proc/meminfo": {Data: []byte("MemTotal: 16777216 kB\n")},
			"proc/self/cgroup": {Data: []byte("3:cpu,memory:/batch\n")},
			"sys/fs/cgroup/memory/batch/memory.limit_in_bytes": {Data: []byte("1073741824\n")}}, 1 << 30 / 4},
		"a group path that climbs": {fstest.MapFS{"proc/meminfo": {Data: []byte("MemTotal: 4194304 kB\n")},
			"proc/self/cgroup":         {Data: []byte("0::/../../etc\n")},
			"sys/fs/cgroup/memory.max": {Data: []byte("268435456\n")}}, 64 << 20},
	} {
		debug.SetMemoryLimit(before)
		got := setMemoryLimit(tc.fsys, none)
		if got != tc.want {
			t.Errorf("%s: limit %d, want %d", name, got, tc.want)
		}
		if now := debug.SetMemoryLimit(-1); tc.want != 0 && now != tc.want || tc.want == 0 && now != before {
			t.Errorf("%s: the runtime has the limit %d", name, now)
		}
	}

	// The environment decides when it says something.
	debug.SetMemoryLimit(before)
	env := func(k string) string { return map[string]string{"GOMEMLIMIT": "2GiB"}[k] }
	if got := setMemoryLimit(meminfo("MemTotal: 783360 kB\n"), env); got != 0 || debug.SetMemoryLimit(-1) != before {
		t.Errorf("with GOMEMLIMIT set: limit %d", got)
	}
}
