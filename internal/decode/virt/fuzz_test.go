package virt

import (
	"strings"
	"testing"
)

// FuzzNames feeds arbitrary text to the functions that name a hypervisor.
// The texts come from DMI strings, the kernel log and /proc/cpuinfo. A
// name that comes back is one line and no longer than its input allows.
func FuzzNames(f *testing.F) {
	f.Add("VMware, Inc.", "VMware Virtual Platform", "Hypervisor detected: VMware", "flags\t\t: fpu vme hypervisor\n")
	f.Add("QEMU", "Standard PC (Q35 + ICH9, 2009)", "Hypervisor detected: KVM", "processor : 0\nflags : fpu\n")
	f.Add("Microsoft Corporation", "Virtual Machine", "Hypervisor detected: Microsoft Hyper-V", "flags")
	f.Add("Amazon EC2", "c5.metal", "Hypervisor detected: ", ":::\nflags:hypervisor")
	f.Add("", "", "", "")
	f.Add("Xen ", " HVM domU/board", "Hypervisor detected: a\nb", "flags : hypervisor\x00")

	oneLine := func(t *testing.T, what, s string) {
		if strings.ContainsAny(s, "\n\r") || s != strings.TrimSpace(s) {
			t.Errorf("%s names the hypervisor %q", what, s)
		}
	}
	f.Fuzz(func(t *testing.T, vendor, product, line, cpuinfo string) {
		oneLine(t, "FromDMI", FromDMI(vendor, product))
		oneLine(t, "FromName", FromName(vendor+" "+product))
		oneLine(t, "FromName", FromName(product))
		got := FromKernelLine(line)
		oneLine(t, "FromKernelLine", got)
		if len(got) > len(line) {
			t.Errorf("FromKernelLine made %d bytes of %d", len(got), len(line))
		}
		// The names of the list are fixed words: none is made of the input.
		if name := FromDMI(vendor, product); name != "" && len(name) > 32 {
			t.Errorf("FromDMI names %q", name)
		}
		if FromCPUFlags(cpuinfo) && !strings.Contains(cpuinfo, "hypervisor") {
			t.Errorf("a hypervisor flag in %q", cpuinfo)
		}
	})
}
