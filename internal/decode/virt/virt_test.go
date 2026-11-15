package virt

import "testing"

func TestFromDMI(t *testing.T) {
	for _, tc := range []struct{ vendor, product, want string }{
		// Virtual machines, with the strings their hypervisors fill in.
		{"QEMU", "KVM Virtual Machine", "KVM"},
		{"QEMU", "Standard PC (i440FX + PIIX, 1996)", "QEMU"},
		{"QEMU", "Standard PC (Q35 + ICH9, 2009)", "QEMU"},
		{"VMware, Inc.", "VMware Virtual Platform", "VMware"},
		{"VMware, Inc.", "VMware20,1", "VMware"},
		{"Microsoft Corporation", "Virtual Machine", "Hyper-V"},
		{"Xen", "HVM domU", "Xen"},
		{"innotek GmbH", "VirtualBox", "VirtualBox"},
		{"Parallels International GmbH.", "Parallels ARM Virtual Machine", "Parallels"},
		{"Red Hat", "KVM", "KVM"},
		{"Red Hat", "RHEV Hypervisor", "KVM"},
		{"OpenStack Foundation", "OpenStack Nova", "OpenStack"},
		{"Red Hat", "OpenStack Compute", "OpenStack"},
		{"oVirt", "oVirt Node", "oVirt"},
		{"KubeVirt", "None", "KubeVirt"},
		{"Nutanix", "AHV", "Nutanix AHV"},
		{"Amazon EC2", "t3.micro", "Amazon EC2"},
		{"Google", "Google Compute Engine", "Google Compute Engine"},
		{"DigitalOcean", "Droplet", "DigitalOcean"},
		{"Hetzner", "vServer", "Hetzner vServer"},
		{"Apple Inc.", "Apple Virtualization Generic Platform", "Apple Virtualization"},
		{"Bochs", "Bochs", "Bochs"},
		{"", "BHYVE", "bhyve"},
		// Physical machines, and names that do not settle it.
		{"Dell Inc.", "PowerEdge R760", ""},
		{"Supermicro", "SYS-621C-TN12R", ""},
		{"HPE", "ProLiant DL380 Gen11", ""},
		{"Lenovo", "ThinkSystem SR650 V3", ""},
		{"Microsoft Corporation", "Surface Laptop 5", ""},
		{"Apple Inc.", "MacBookPro18,1", ""},
		{"Amazon EC2", "c5.metal", ""},
		{"Amazon EC2", "m7i.metal-24xl", ""},
		{"Red Hat", "Enterprise Linux", ""},
		{"Example Cloud", "mi1-g3;MICRO", ""},
		{"", "", ""},
	} {
		if got := FromDMI(tc.vendor, tc.product); got != tc.want {
			t.Errorf("FromDMI(%q, %q) = %q, want %q", tc.vendor, tc.product, got, tc.want)
		}
	}
}

func TestFromName(t *testing.T) {
	for name, want := range map[string]string{
		"QEMU Standard PC (Q35 + ICH9, 2009)":                                   "QEMU",
		"QEMU KVM Virtual Machine":                                              "KVM",
		"VMware, Inc. VMware Virtual Platform/440BX Desktop Reference Platform": "VMware",
		"Microsoft Corporation Virtual Machine/Virtual Machine":                 "Hyper-V",
		"Microsoft Corporation Virtual Machine":                                 "Hyper-V",
		"Xen HVM domU":                                                          "Xen",
		"innotek GmbH VirtualBox/VirtualBox":                                    "VirtualBox",
		"Amazon EC2 t3.micro/":                                                  "Amazon EC2",
		"Amazon EC2 c5.metal/":                                                  "",
		"Google Google Compute Engine/Google Compute Engine":                    "Google Compute Engine",
		"Red Hat KVM":                     "KVM",
		"Dell Inc. PowerEdge R740/0923K0": "",
		"Example Systems R760":            "",
		"":                                "",
	} {
		if got := FromName(name); got != want {
			t.Errorf("FromName(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestFromKernelLine(t *testing.T) {
	for msg, want := range map[string]string{
		"Hypervisor detected: KVM":                        "KVM",
		"Hypervisor detected: VMware":                     "VMware",
		"Hypervisor detected: Microsoft Hyper-V":          "Hyper-V",
		"Hypervisor detected: Xen HVM":                    "Xen HVM",
		"Booting paravirtualized kernel on bare hardware": "",
		"kvm: no hardware support":                        "",
		"Hypervisor detected:":                            "",
		"":                                                "",
	} {
		if got := FromKernelLine(msg); got != want {
			t.Errorf("FromKernelLine(%q) = %q, want %q", msg, got, want)
		}
	}
}

func TestFromCPUFlags(t *testing.T) {
	guest := "processor\t: 0\nvendor_id\t: GenuineIntel\nflags\t\t: fpu vme de pse tsc msr pae mce cx8 apic sep mtrr pge mca cmov pat hypervisor lahf_lm\nbugs\t\t: spectre_v1\n"
	metal := "processor\t: 0\nvendor_id\t: GenuineIntel\nflags\t\t: fpu vme de pse tsc msr pae mce cx8 apic sep mtrr pge mca cmov pat lahf_lm\n"
	arm := "processor\t: 0\nBogoMIPS\t: 50.00\nFeatures\t: fp asimd evtstrm aes pmull sha1 sha2 crc32\n"
	// A flag whose name only contains the word.
	other := "flags\t\t: fpu nohypervisor hypervisor_raw\n"
	for name, tc := range map[string]struct {
		text string
		want bool
	}{"guest": {guest, true}, "bare metal": {metal, false}, "arm64": {arm, false}, "a longer flag": {other, false}, "empty": {"", false}} {
		if got := FromCPUFlags(tc.text); got != tc.want {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}
