// Package virt tells whether a machine is a virtual machine, from what the
// machine says about itself.
//
// It matters for memory errors because a guest sees little of them. The
// corrected errors of the machine are handled by the hypervisor host and
// are not passed on. Of the uncorrected ones a guest is told at most about
// those that hit its own memory. A scan of a guest that finds nothing says
// nothing about the DIMMs of the machine it runs on.
//
// Three things name a hypervisor: the DMI strings of the machine, which a
// hypervisor fills with its own name; the line the x86 kernel prints at
// boot ("Hypervisor detected: KVM"); and the "hypervisor" flag of an x86
// CPU, which says that there is one and not which.
package virt

import (
	"regexp"
	"strings"
)

// Unnamed stands for a hypervisor that is known to be there and not known
// by name: the CPU flag is set and the DMI strings are those of no known
// virtual machine.
const Unnamed = "hypervisor"

// has reports whether s contains sub, ignoring case.
func has(s, sub string) bool { return strings.Contains(strings.ToLower(s), strings.ToLower(sub)) }

// FromDMI names the hypervisor of a machine by its DMI system vendor and
// product name. It returns "" for strings that are those of no virtual
// machine it knows: a physical machine, or a hypervisor that fills in the
// names of a physical one.
//
// The list holds names that only a virtual machine carries. A cloud that
// uses one vendor string for its virtual and its bare metal instances is
// named only where the product tells the two apart.
func FromDMI(vendor, product string) string {
	v, p := strings.TrimSpace(vendor), strings.TrimSpace(product)
	switch {
	case has(v, "VMware") || strings.HasPrefix(p, "VMware"):
		return "VMware"
	case strings.EqualFold(v, "Microsoft Corporation") && strings.EqualFold(p, "Virtual Machine"):
		return "Hyper-V"
	case strings.EqualFold(v, "Xen") || strings.EqualFold(p, "HVM domU"):
		return "Xen"
	case has(v, "innotek") || strings.EqualFold(p, "VirtualBox"):
		return "VirtualBox"
	case has(v, "Parallels") || has(p, "Parallels"):
		return "Parallels"
	case strings.EqualFold(v, "QEMU"):
		if has(p, "KVM") {
			return "KVM"
		}
		return "QEMU"
	case strings.EqualFold(p, "KVM") || strings.EqualFold(p, "KVM Virtual Machine") || strings.EqualFold(p, "RHEV Hypervisor"):
		return "KVM"
	case has(v, "OpenStack") || strings.HasPrefix(p, "OpenStack"):
		return "OpenStack"
	case strings.EqualFold(v, "oVirt") || strings.HasPrefix(p, "oVirt"):
		return "oVirt"
	case strings.EqualFold(v, "KubeVirt"):
		return "KubeVirt"
	case strings.EqualFold(v, "Nutanix") && strings.EqualFold(p, "AHV"):
		return "Nutanix AHV"
	case strings.EqualFold(v, "Amazon EC2") && !has(p, "metal"):
		return "Amazon EC2"
	case strings.EqualFold(p, "Google Compute Engine"):
		return "Google Compute Engine"
	case strings.EqualFold(v, "DigitalOcean"):
		return "DigitalOcean"
	case strings.EqualFold(v, "Hetzner") && strings.EqualFold(p, "vServer"):
		return "Hetzner vServer"
	case strings.HasPrefix(p, "Apple Virtualization"):
		return "Apple Virtualization"
	case strings.EqualFold(v, "Bochs") || strings.EqualFold(p, "Bochs"):
		return "Bochs"
	case strings.EqualFold(p, "BHYVE"):
		return "bhyve"
	}
	return ""
}

// names are the starts of a machine name, vendor and product in one
// string, as the kernel prints it in its DMI line.
var names = []struct{ start, vendor string }{
	{"VMware, Inc. ", "VMware, Inc."},
	{"Microsoft Corporation ", "Microsoft Corporation"},
	{"innotek GmbH ", "innotek GmbH"},
	{"Parallels ", "Parallels"},
	{"QEMU ", "QEMU"},
	{"Xen ", "Xen"},
	{"Red Hat ", "Red Hat"},
	{"OpenStack Foundation ", "OpenStack Foundation"},
	{"oVirt ", "oVirt"},
	{"KubeVirt ", "KubeVirt"},
	{"Nutanix ", "Nutanix"},
	{"Amazon EC2 ", "Amazon EC2"},
	{"Google ", "Google"},
	{"DigitalOcean ", "DigitalOcean"},
	{"Hetzner ", "Hetzner"},
	{"Apple Inc. ", "Apple Inc."},
	{"Bochs ", "Bochs"},
}

// FromName names the hypervisor of a machine by the one string the kernel
// log gives for it: "vendor product" or "vendor product/board".
func FromName(name string) string {
	name = strings.TrimSpace(name)
	if i := strings.LastIndexByte(name, '/'); i > 0 {
		name = name[:i]
	}
	for _, n := range names {
		if product, ok := strings.CutPrefix(name, n.start); ok {
			return FromDMI(n.vendor, product)
		}
	}
	return FromDMI("", name)
}

// The kernel prints the name of a hypervisor from its own table: a few
// words in ASCII ("KVM", "Microsoft Hyper-V", "Xen HVM"). Text in that
// place that is longer, or holds anything else, is no such name.
var detectedRE = regexp.MustCompile(`^Hypervisor detected: ([A-Za-z0-9][A-Za-z0-9 ._+/()-]{0,39})$`)

// FromKernelLine names the hypervisor the kernel says it runs under, from a
// message of its log, or returns "". The x86 kernel prints the line once
// at boot; a kernel on bare hardware prints none.
func FromKernelLine(msg string) string {
	m := detectedRE.FindStringSubmatch(strings.TrimSpace(msg))
	if m == nil {
		return ""
	}
	name := strings.TrimSpace(m[1])
	if strings.EqualFold(name, "Microsoft Hyper-V") {
		return "Hyper-V"
	}
	return name
}

// FromCPUFlags reports whether the flags line of /proc/cpuinfo names a
// hypervisor: bit 31 of CPUID leaf 1 ECX, which no physical x86 CPU sets.
func FromCPUFlags(cpuinfo string) bool {
	for _, line := range strings.Split(cpuinfo, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) != "flags" {
			continue
		}
		for _, f := range strings.Fields(value) {
			if f == "hypervisor" {
				return true
			}
		}
		return false // every CPU has the same flags
	}
	return false
}
