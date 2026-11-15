package hostinfo

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
)

// FuzzCollect reads host files that hold arbitrary text. On a live host
// the kernel writes them; in a copy of a host's files (--root, an sos
// report) they are whatever was put there. Whatever they hold, the host
// and the boot the adapter hands on are records the ledger takes.
func FuzzCollect(f *testing.F) {
	live := liveHost()
	text := func(name string) string { return string(live[name].Data) }
	f.Add(text("proc/sys/kernel/hostname"), text("proc/sys/kernel/osrelease"), text("proc/sys/kernel/random/boot_id"), text("proc/cmdline"),
		text("proc/stat"), text("sys/class/dmi/id/sys_vendor"), text("sys/class/dmi/id/product_name"), "flags\t\t: fpu vme hypervisor\n", uint8(0))
	f.Add("web-7\n", "6.8.0-45-generic\n", "", "ro quiet\x00", "btime 99999999999999999\n", "QEMU", "Standard PC (Q35 + ICH9, 2009)", "", uint8(1))
	f.Add("", "", "6F1C0A8E2B7D4F0E9A3B5C7D9E1F2A3B", "", "cpu 1\nbtime -5\nbtime 7\n", "To Be Filled By O.E.M.", "Default string", "flags : hypervisor", uint8(2))
	f.Add("a\nb", "Linux version 5.14.0-570.el9.aarch64 (builder@host)", "zz", "x", "btime 9223372036854775807", "Xen", "HVM domU", "processor : 0\n", uint8(4))
	f.Add("h", "k", "0", "c", "btime 253402300800\n", "", "", strings.Repeat("x", 70000), uint8(8))
	f.Add("h", "k", "0", "c", strings.Repeat("9", 5000)+"\nbtime 1\n", "v", "p", "", uint8(16))

	f.Fuzz(func(t *testing.T, hostname, release, bootID, cmdline, stat, vendor, product, cpuinfo string, shape uint8) {
		tree := fstest.MapFS{
			"proc/sys/kernel/hostname":       file(hostname),
			"proc/sys/kernel/osrelease":      file(release),
			"proc/sys/kernel/random/boot_id": file(bootID),
			"proc/cmdline":                   file(cmdline),
			"proc/stat":                      file(stat),
			"proc/cpuinfo":                   file(cpuinfo),
			"sys/class/dmi/id/sys_vendor":    file(vendor),
			"sys/class/dmi/id/product_name":  file(product),
			"sys/class/dmi/id/board_vendor":  file(vendor),
			"sys/class/dmi/id/board_name":    file(product),
			"sys/class/dmi/id/bios_vendor":   file(vendor),
			"sys/class/dmi/id/bios_version":  file(release),
			"sys/class/dmi/id/bios_date":     file(bootID),
		}
		// The shape bits take files away and put others in their place.
		if shape&1 != 0 {
			delete(tree, "proc/sys/kernel/hostname")
			tree["etc/hostname"] = file(hostname)
		}
		if shape&2 != 0 {
			delete(tree, "proc/sys/kernel/osrelease")
			tree["proc/version"] = file(release)
		}
		if shape&4 != 0 {
			tree["proc/sys/kernel/arch"] = file(product)
			tree["sys/hypervisor/type"] = file(vendor)
		}
		if shape&8 != 0 {
			delete(tree, "proc/stat")
			delete(tree, "proc/sys/kernel/random/boot_id")
		}
		if shape&16 != 0 {
			for name := range tree {
				if strings.HasPrefix(name, "sys/") {
					delete(tree, name)
				}
			}
		}

		a := New(tree, "fuzz", Options{Now: now})
		var c adapter.Collected
		rep := a.Collect(ctx, &c)
		if rep.Status != model.SourceOK && rep.Status != model.SourcePartial {
			t.Fatalf("a tree with /proc was read with status %q: %+v", rep.Status, rep)
		}
		if rep.SinkErr != nil || rep.Kind != Kind || len(c.Hosts) != 1 || len(c.Boots) > 1 || rep.Records != 1+len(c.Boots) {
			t.Fatalf("report %+v, %d hosts, %d boots", rep, len(c.Hosts), len(c.Boots))
		}
		if (rep.Status == model.SourcePartial) != (rep.Skipped > 0) {
			t.Errorf("status %s with %d pieces missing", rep.Status, rep.Skipped)
		}
		if rep.HostHint != a.HostHint() || len(rep.HostHint) > maxAttr || strings.ContainsAny(rep.HostHint, "\n\x00") {
			t.Errorf("host name %q", rep.HostHint)
		}
		h := c.Hosts[0]
		for _, s := range []string{h.Arch, h.SystemVendor, h.SystemProduct, h.Board, h.Virtual} {
			if len(s) > 2*maxAttr+1 {
				t.Errorf("a text of %d bytes about the host", len(s))
			}
		}
		for _, b := range c.Boots {
			// What the ledger refuses: a boot out of the range of its times.
			if y := b.StartedAt.UTC().Year(); b.StartedAt.IsZero() || y < 1970 || y > 9999 {
				t.Errorf("boot at %v", b.StartedAt)
			}
			if b.BootID != "" && (len(b.BootID) != 32 || b.BootID != strings.ToLower(b.BootID) || !isHex(b.BootID)) {
				t.Errorf("boot ID %q", b.BootID)
			}
			if b.Derived != (b.BootID == "") || b.Source != Kind || b.StartQuality != model.TimeExact {
				t.Errorf("boot %+v", *b)
			}
			for _, s := range []string{b.Kernel, b.BIOS, b.Cmdline} {
				if len(s) > 3*maxAttr+2 {
					t.Errorf("a text of %d bytes about the boot", len(s))
				}
			}
		}
	})
}
