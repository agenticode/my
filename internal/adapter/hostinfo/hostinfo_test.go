package hostinfo

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
)

var (
	ctx = context.Background()
	now = time.Date(2026, 11, 13, 5, 0, 0, 0, time.UTC)
)

func file(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }

func liveHost() fstest.MapFS {
	return fstest.MapFS{
		"proc/sys/kernel/hostname":       file("r12-n034\n"),
		"proc/sys/kernel/osrelease":      file("5.14.0-570.12.1.el9_6.x86_64\n"),
		"proc/sys/kernel/random/boot_id": file("6f1c0a8e-2b7d-4f0e-9a3b-5c7d9e1f2a3b\n"),
		"proc/cmdline":                   file("BOOT_IMAGE=(hd0,gpt2)/vmlinuz-5.14.0-570.12.1.el9_6.x86_64 root=/dev/mapper/rl-root ro\n"),
		"proc/stat":                      file("cpu  4705 150 1120 16250 520 0 30 0 0 0\ncpu0 1 2 3 4\nintr 114930548 113199788\nctxt 1990473\nbtime 1794268800\nprocesses 2915\n"),
		"sys/class/dmi/id/sys_vendor":    file("Example Systems\n"),
		"sys/class/dmi/id/product_name":  file("R650\n"),
		"sys/class/dmi/id/board_vendor":  file("Example Systems\n"),
		"sys/class/dmi/id/board_name":    file("0PYXKY\n"),
		"sys/class/dmi/id/bios_vendor":   file("Example BIOS Inc.\n"),
		"sys/class/dmi/id/bios_version":  file("1.5.5\n"),
		"sys/class/dmi/id/bios_date":     file("02/10/2022\n"),
	}
}

func TestLiveHost(t *testing.T) {
	a := New(liveHost(), "/", Options{Now: now})
	var c adapter.Collected
	rep := a.Collect(ctx, &c)
	if rep.Status != model.SourceOK || rep.Records != 2 || rep.HostHint != "r12-n034" || len(c.Hosts) != 1 || len(c.Boots) != 1 {
		t.Fatalf("report = %+v, %d hosts, %d boots", rep, len(c.Hosts), len(c.Boots))
	}
	h := c.Hosts[0]
	if h.Name != "" || h.Arch != "x86_64" || h.SystemVendor != "Example Systems" || h.SystemProduct != "R650" || h.Board != "Example Systems 0PYXKY" {
		t.Errorf("host: %+v", *h)
	}
	b := c.Boots[0]
	want := model.Boot{
		BootID: "6f1c0a8e2b7d4f0e9a3b5c7d9e1f2a3b", StartedAt: time.Unix(1794268800, 0).UTC(), StartQuality: model.TimeExact,
		LastSeen: now, Kernel: "5.14.0-570.12.1.el9_6.x86_64", BIOS: "Example BIOS Inc. 1.5.5 02/10/2022",
		Cmdline: "BOOT_IMAGE=(hd0,gpt2)/vmlinuz-5.14.0-570.12.1.el9_6.x86_64 root=/dev/mapper/rl-root ro", Source: Kind, RawRef: "/",
	}
	if *b != want {
		t.Errorf("boot\n got: %+v\nwant: %+v", *b, want)
	}
	if a.HostHint() != "r12-n034" {
		t.Errorf("HostHint() = %q", a.HostHint())
	}
}

func TestFallbacksAndPlaceholders(t *testing.T) {
	fsys := fstest.MapFS{
		"etc/hostname":                  file("edge-7\n"),
		"proc/version":                  file("Linux version 6.8.0-45-generic-64k (buildd@lcy02-arm64-046) (aarch64-linux-gnu-gcc-13) #45-Ubuntu SMP\n"),
		"proc/sys/kernel/arch":          file("aarch64\n"),
		"proc/stat":                     file("cpu 1 2 3\n"),
		"sys/class/dmi/id/sys_vendor":   file("To Be Filled By O.E.M.\n"),
		"sys/class/dmi/id/product_name": file("Default string\n"),
		"sys/class/dmi/id/bios_version": file("F31n\n"),
	}
	a := New(fsys, "/", Options{Now: now})
	info, missing := a.Read()
	if info.Hostname != "edge-7" || info.Kernel != "6.8.0-45-generic-64k" || info.Arch != "aarch64" || info.BootID != "" || !info.BootTime.IsZero() ||
		info.SystemVendor != "" || info.SystemProduct != "" || info.BIOS != "F31n" {
		t.Errorf("info: %+v", info)
	}
	if got := strings.Join(missing, ","); got != "boot ID,boot time" {
		t.Errorf("missing = %q", got)
	}
	var c adapter.Collected
	rep := a.Collect(ctx, &c)
	// Without a boot time there is no boot to report.
	if rep.Status != model.SourcePartial || len(c.Boots) != 0 || len(c.Hosts) != 1 || !strings.Contains(strings.Join(rep.Notes, " "), "could not read: boot ID, boot time") {
		t.Errorf("report = %+v, %d boots", rep, len(c.Boots))
	}
}

// A copy of a host's files holds whatever was put there. A boot time the
// ledger cannot store went on to the ledger and was refused there: it is
// no boot time. A host name file with a second line gave a host name with
// a line break in it. A file far larger than the kernel writes is not
// read into memory. Found by FuzzCollect.
func TestValuesTheKernelNeverWrites(t *testing.T) {
	for _, btime := range []string{"253402300800", "99999999999999999", "9223372036854775807", "0", "-5", "soon"} {
		fsys := liveHost()
		fsys["proc/stat"] = file("cpu  1 2 3\nbtime " + btime + "\n")
		var c adapter.Collected
		rep := New(fsys, "/", Options{Now: now}).Collect(ctx, &c)
		if rep.Status != model.SourcePartial || rep.SinkErr != nil || len(c.Boots) != 0 || len(c.Hosts) != 1 ||
			len(rep.Notes) != 1 || rep.Notes[0] != "could not read: boot time" {
			t.Errorf("btime %s: report = %+v, %d boots", btime, rep, len(c.Boots))
		}
	}
	// The last second the ledger stores is a boot time.
	fsys := liveHost()
	fsys["proc/stat"] = file("btime 253402300799\n")
	var c adapter.Collected
	if rep := New(fsys, "/", Options{Now: now}).Collect(ctx, &c); rep.Status != model.SourceOK || len(c.Boots) != 1 || c.Boots[0].StartedAt.Year() != 9999 {
		t.Errorf("last second of the year 9999: report = %+v", rep)
	}

	// Host name files: more than a name, a megabyte, a zero byte.
	for content, want := range map[string]string{
		"web-7\nsecond line\n":            "web-7",
		"  web-7.example.org  \n":         "web-7.example.org",
		strings.Repeat("h", 253) + "\n":   strings.Repeat("h", 253),
		strings.Repeat("h", 254) + "\n":   "",
		strings.Repeat("h", 1<<20) + "\n": "",
		"web\x007\n":                      "",
		"\n":                              "",
	} {
		fsys := liveHost()
		fsys["proc/sys/kernel/hostname"] = file(content)
		if got := New(fsys, "/", Options{Now: now}).HostHint(); got != want {
			t.Errorf("host name file of %d bytes: %q, want %q", len(content), got, want)
		}
	}
	// A command line of a megabyte is left out; the boot is still read.
	fsys = liveHost()
	fsys["proc/cmdline"] = file(strings.Repeat("x", 1<<20))
	c = adapter.Collected{}
	if rep := New(fsys, "/", Options{Now: now}).Collect(ctx, &c); rep.Status != model.SourceOK || len(c.Boots) != 1 || c.Boots[0].Cmdline != "" {
		t.Errorf("large command line: report = %+v", rep)
	}
}

func TestArchFromKernelRelease(t *testing.T) {
	for rel, want := range map[string]string{
		"5.14.0-570.12.1.el9_6.x86_64":      "x86_64",
		"4.18.0-553.el8_10.aarch64":         "aarch64",
		"5.14.0-570.12.1.el9_6.aarch64+64k": "aarch64",
		"6.1.0-18-amd64":                    "x86_64",
		"6.1.0-18-arm64":                    "aarch64",
		"6.8.0-45-generic":                  "",
	} {
		a := New(fstest.MapFS{"proc/sys/kernel/osrelease": file(rel + "\n")}, "/", Options{})
		if info, _ := a.Read(); info.Arch != want {
			t.Errorf("%s: arch = %q, want %q", rel, info.Arch, want)
		}
	}
}

func TestNotALinuxSystem(t *testing.T) {
	var c adapter.Collected
	rep := New(fstest.MapFS{"Users/alan/file": file("x")}, "/", Options{}).Collect(ctx, &c)
	if rep.Status != model.SourceAbsent || !strings.Contains(rep.Detail, "not a running Linux system") || len(c.Hosts) != 0 {
		t.Errorf("report = %+v", rep)
	}
	// A malformed boot ID is not passed on.
	fsys := liveHost()
	fsys["proc/sys/kernel/random/boot_id"] = file("not-a-boot-id\n")
	if info, _ := New(fsys, "/", Options{}).Read(); info.BootID != "" {
		t.Errorf("boot ID = %q", info.BootID)
	}
}

type failingSink struct {
	adapter.Collected
	err error
}

func (f *failingSink) Boot(*model.Boot) error { return f.err }

func TestSinkErrorAndCancel(t *testing.T) {
	boom := errors.New("ledger is full")
	rep := New(liveHost(), "/", Options{}).Collect(ctx, &failingSink{err: boom})
	if rep.Status != model.SourceError || !errors.Is(rep.SinkErr, boom) {
		t.Errorf("report = %+v", rep)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	var c adapter.Collected
	if rep := New(liveHost(), "/", Options{}).Collect(cctx, &c); rep.Status != model.SourceError || len(c.Hosts) != 0 {
		t.Errorf("cancelled: %+v", rep)
	}
}

// A virtual machine is named by the strings its hypervisor fills in, by
// the Xen directory of sysfs, or only known through the flag of its CPU.
func TestVirtualMachine(t *testing.T) {
	flags := "processor\t: 0\nflags\t\t: fpu vme de pse tsc msr pae mce hypervisor lahf_lm\n"
	for _, tc := range []struct {
		name string
		set  func(fstest.MapFS)
		want string
	}{
		{"a physical host", func(fstest.MapFS) {}, ""},
		{"a physical host with its CPU flags", func(fs fstest.MapFS) {
			fs["proc/cpuinfo"] = file("processor\t: 0\nflags\t\t: fpu vme de pse tsc msr pae mce lahf_lm\n")
		}, ""},
		{"QEMU with KVM on arm64", func(fs fstest.MapFS) {
			fs["sys/class/dmi/id/sys_vendor"], fs["sys/class/dmi/id/product_name"] = file("QEMU\n"), file("KVM Virtual Machine\n")
		}, "KVM"},
		{"VMware", func(fs fstest.MapFS) {
			fs["sys/class/dmi/id/sys_vendor"], fs["sys/class/dmi/id/product_name"] = file("VMware, Inc.\n"), file("VMware Virtual Platform\n")
			fs["proc/cpuinfo"] = file(flags)
		}, "VMware"},
		{"a cloud whose names say nothing, on a CPU that reports a hypervisor", func(fs fstest.MapFS) {
			fs["sys/class/dmi/id/sys_vendor"], fs["sys/class/dmi/id/product_name"] = file("ExampleCloud\n"), file("mi1-g3;MICRO\n")
			fs["proc/cpuinfo"] = file(flags)
		}, "hypervisor"},
		{"a Xen guest", func(fs fstest.MapFS) { fs["sys/hypervisor/type"] = file("xen\n") }, "Xen"},
	} {
		fsys := liveHost()
		tc.set(fsys)
		a := New(fsys, "/", Options{Now: now})
		if info, _ := a.Read(); info.Virtual != tc.want {
			t.Errorf("%s: Virtual = %q, want %q", tc.name, info.Virtual, tc.want)
		}
		var c adapter.Collected
		if rep := a.Collect(ctx, &c); rep.Status != model.SourceOK || len(c.Hosts) != 1 || c.Hosts[0].Virtual != tc.want {
			t.Errorf("%s: host record %+v", tc.name, c.Hosts)
		}
	}
	// A cpuinfo of many CPUs: the flags of the first one are enough.
	fsys := liveHost()
	fsys["proc/cpuinfo"] = file(strings.Repeat(flags+"\n", 2000))
	if info, _ := New(fsys, "/", Options{Now: now}).Read(); info.Virtual != "hypervisor" {
		t.Errorf("a long cpuinfo: Virtual = %q", info.Virtual)
	}
}
