// Package preflighttest makes copies of a host's files for the tests of
// "my preflight": a file system tree with the files the checks read.
package preflighttest

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Values of Files with a meaning of their own.
const (
	// Dir makes a directory.
	Dir = "<dir>"
	// Denied makes a file that this user cannot read (mode 0000).
	Denied = "<denied>"
	// DeniedDir makes a directory that this user cannot list.
	DeniedDir = "<denied dir>"
	// LinkPrefix starts a symbolic link: "-> /dev/null".
	LinkPrefix = "-> "
)

// Files maps a path below the root (with slashes, no leading slash) to its
// contents. An empty string removes the path from a set it is merged into.
type Files map[string]string

// With returns a copy of f with the changes applied: a value "" removes
// the path and every path below it.
func (f Files) With(changes Files) Files {
	out := Files{}
	for k, v := range f {
		out[k] = v
	}
	for k, v := range changes {
		if v == "" {
			for p := range out {
				if p == k || strings.HasPrefix(p, k+"/") {
					delete(out, p)
				}
			}
			continue
		}
		out[k] = v
	}
	return out
}

// Tree writes the files below a new temporary directory and returns it.
func Tree(tb testing.TB, f Files) string {
	tb.Helper()
	root := tb.TempDir()
	names := make([]string, 0, len(f))
	for k := range f {
		names = append(names, k)
	}
	sort.Strings(names)
	var locked []string
	for _, rel := range names {
		v := f[rel]
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			tb.Fatal(err)
		}
		var err error
		switch {
		case v == Dir:
			err = os.MkdirAll(p, 0o755)
		case v == DeniedDir:
			if err = os.MkdirAll(p, 0o755); err == nil {
				locked = append(locked, p)
			}
		case v == Denied:
			if err = os.WriteFile(p, []byte("x\n"), 0o644); err == nil {
				locked = append(locked, p)
			}
		case strings.HasPrefix(v, LinkPrefix):
			err = os.Symlink(strings.TrimPrefix(v, LinkPrefix), p)
		default:
			err = os.WriteFile(p, []byte(v), 0o644)
		}
		if err != nil {
			tb.Fatal(err)
		}
	}
	for _, p := range locked {
		if err := os.Chmod(p, 0); err != nil {
			tb.Fatal(err)
		}
	}
	tb.Cleanup(func() {
		// A directory of mode 0000 cannot be removed by the test's cleanup.
		for _, p := range locked {
			os.Chmod(p, 0o755)
		}
	})
	return root
}

// Kept is a Fedora host on x86 where every check keeps its records.
func Kept() Files {
	return Files{
		"proc/sys/kernel/hostname":  "r12-n034\n",
		"proc/sys/kernel/osrelease": "6.12.0-55.el10.x86_64\n",
		"proc/sys/kernel/arch":      "x86_64\n",
		"proc/sys/kernel/panic":     "0\n",
		"proc/stat":                 "cpu  1 2 3 4\nbtime 1794805260\n",
		"proc/cmdline":              "BOOT_IMAGE=(hd0,gpt2)/vmlinuz-6.12.0-55.el10.x86_64 root=UUID=0 ro crashkernel=2G-64G:256M console=tty0 console=ttyS0,115200n8 mce=print_all crash_kexec_post_notifiers\n",
		"proc/cpuinfo":              "processor\t: 0\nvendor_id\t: GenuineIntel\ncpu family\t: 6\nmodel\t\t: 143\nmodel name\t: Example CPU\n\nprocessor\t: 1\nvendor_id\t: GenuineIntel\n",
		"proc/consoles":             "tty0                 -WU (E  p  )    4:1\nttyS0                -W- (E  p a)    4:64\n",
		"etc/os-release":            "NAME=\"Fedora Linux\"\nID=fedora\nPRETTY_NAME=\"Fedora Linux 44 (Server Edition)\"\n",

		"run/systemd/system":        Dir,
		"etc/systemd/journald.conf": "[Journal]\n#Storage=auto\n",
		"var/log/journal/0123456789abcdef0123456789abcdef/system.journal": "LPKSHHRH",

		"sys/module/pstore/parameters/backend":    "erst\n",
		"sys/module/pstore/parameters/kmsg_bytes": "10240\n",
		"sys/fs/pstore": Dir,
		"usr/lib/systemd/system/systemd-pstore.service":                  "[Unit]\nDescription=Platform Persistent Storage Archival\n",
		"etc/systemd/system/sysinit.target.wants/systemd-pstore.service": LinkPrefix + "/usr/lib/systemd/system/systemd-pstore.service",
		"usr/lib/systemd/pstore.conf":                                    "[PStore]\n#Storage=external\n#Unlink=yes\n",

		"sys/kernel/kexec_crash_loaded":                           "1\n",
		"sys/kernel/kexec_crash_size":                             "268435456\n",
		"sys/module/kernel/parameters/crash_kexec_post_notifiers": "Y\n",

		"sys/devices/system/machinecheck/machinecheck0/print_all": "1\n",
		"sys/devices/system/machinecheck/machinecheck0/trigger":   "\n",
		"sys/kernel/reboot/mode":                                  "cold\n",

		"sys/firmware/acpi/tables/BERT": "BERT",
		"sys/firmware/acpi/tables/ERST": "ERST",
		"sys/firmware/efi":              Dir,

		"var/lib/rasdaemon/ras-mc_event.db":                            "SQLite format 3\x00",
		"usr/lib/systemd/system/rasdaemon.service":                     "[Service]\nExecStart=/usr/sbin/rasdaemon -f -r\n",
		"etc/systemd/system/multi-user.target.wants/rasdaemon.service": LinkPrefix + "/usr/lib/systemd/system/rasdaemon.service",

		"etc/systemd/system/my-harvest.service":                         "# Written by \"my harvest install\"\n[Service]\nType=oneshot\n",
		"etc/systemd/system/multi-user.target.wants/my-harvest.service": LinkPrefix + "/etc/systemd/system/my-harvest.service",
	}
}

// Bare is an Ubuntu host on x86 with the settings of a fresh install,
// where most checks lose their records.
func Bare() Files {
	return Files{
		"proc/sys/kernel/hostname":  "web-07\n",
		"proc/sys/kernel/osrelease": "6.8.0-87-generic\n",
		"proc/sys/kernel/arch":      "x86_64\n",
		"proc/sys/kernel/panic":     "0\n",
		"proc/stat":                 "cpu  1 2 3 4\nbtime 1794805260\n",
		"proc/cmdline":              "BOOT_IMAGE=/boot/vmlinuz-6.8.0-87-generic root=UUID=0 ro quiet splash\n",
		"proc/cpuinfo":              "processor\t: 0\nvendor_id\t: GenuineIntel\ncpu family\t: 6\nmodel\t\t: 143\n\n",
		"proc/consoles":             "tty0                 -WU (EC p  )    4:7\n",
		"etc/os-release":            "NAME=\"Ubuntu\"\nID=ubuntu\nID_LIKE=debian\nPRETTY_NAME=\"Ubuntu 24.04.3 LTS\"\n",
		"run/systemd/system":        Dir,
		"etc/systemd/journald.conf": "[Journal]\n#Storage=auto\n",

		"sys/module/pstore/parameters/backend":            "(null)\n",
		"sys/module/pstore/parameters/kmsg_bytes":         "10240\n",
		"sys/module/efi_pstore/parameters/pstore_disable": "Y\n",
		"sys/fs/pstore": Dir,
		"usr/lib/systemd/system/systemd-pstore.service": "[Unit]\nDescription=Platform Persistent Storage Archival\n",
		"sys/firmware/efi":                                        Dir,
		"sys/firmware/acpi/tables/DSDT":                           "DSDT",
		"sys/kernel/kexec_crash_loaded":                           "0\n",
		"sys/kernel/kexec_crash_size":                             "0\n",
		"sys/module/kernel/parameters/crash_kexec_post_notifiers": "N\n",

		"sys/devices/system/machinecheck/machinecheck0/print_all": "0\n",
		"sys/devices/system/machinecheck/machinecheck0/trigger":   "\n",
		"sys/kernel/reboot/mode":                                  "cold\n",
	}
}
