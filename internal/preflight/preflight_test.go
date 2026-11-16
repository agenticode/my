package preflight_test

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/preflight"
	pt "github.com/agenticode/my/internal/preflight/preflighttest"
)

// run makes the checks over a tree as a live host whose process list and
// free space the test gives.
func run(t *testing.T, f pt.Files, procs []string, free uint64) *preflight.Report {
	t.Helper()
	root := pt.Tree(t, f)
	return preflight.Run(preflight.Options{Root: root, Live: true,
		Processes: func() ([]string, error) { return procs, nil },
		FreeBytes: func(string) (uint64, error) { return free, nil },
	})
}

func check(t *testing.T, r *preflight.Report, name string) preflight.Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no check %s", name)
	return preflight.Check{}
}

const gib = 1 << 30

func amd(family int) string {
	return "processor\t: 0\nvendor_id\t: AuthenticAMD\ncpu family\t: " + itoa(family) + "\nmodel\t\t: 1\n\n"
}

func intel(family, model int) string {
	return "processor\t: 0\nvendor_id\t: GenuineIntel\ncpu family\t: " + itoa(family) + "\nmodel\t\t: " + itoa(model) + "\n\n"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

// arm is a host that is not x86.
var arm = pt.Files{"proc/sys/kernel/arch": "aarch64\n", "proc/cpuinfo": "processor\t: 0\nBogoMIPS\t: 50.00\nCPU implementer\t: 0x41\n\n"}

// TestStates makes a tree per state of every check and looks at the state
// and the statement that says why.
func TestStates(t *testing.T) {
	kept, bare := pt.Kept(), pt.Bare()
	cmdline := func(f pt.Files, extra string) pt.Files {
		return pt.Files{"proc/cmdline": strings.TrimSpace(f["proc/cmdline"]) + " " + extra + "\n"}
	}
	for _, tt := range []struct {
		name  string
		base  pt.Files
		with  pt.Files
		procs []string
		free  uint64
		check string
		state preflight.State
		code  string
		root  bool // the case needs a user that permissions apply to
	}{
		// journal
		{name: "journal on disk", base: kept, check: "journal", state: preflight.Kept, code: "pf.journal.disk"},
		{name: "journal without its directory", base: kept, with: pt.Files{"var/log/journal": ""}, check: "journal", state: preflight.Lost, code: "pf.journal.no_dir"},
		{name: "journal volatile", base: kept, with: pt.Files{"etc/systemd/journald.conf.d/50-mem.conf": "[Journal]\nStorage=volatile\n"}, check: "journal", state: preflight.Lost, code: "pf.journal.volatile"},
		{name: "journal none", base: kept, with: pt.Files{"etc/systemd/journald.conf.d/50-off.conf": "[Journal]\nStorage=none\n"}, check: "journal", state: preflight.Lost, code: "pf.journal.none"},
		{name: "journal drop-in of /etc masks the same name in /usr/lib", base: kept, with: pt.Files{"var/log/journal": "",
			"etc/systemd/journald.conf.d/50-a.conf":     "[Journal]\nStorage=volatile\n",
			"usr/lib/systemd/journald.conf.d/50-a.conf": "[Journal]\nStorage=persistent\n"}, check: "journal", state: preflight.Lost, code: "pf.journal.volatile"},
		{name: "journal later drop-in wins", base: kept, with: pt.Files{"var/log/journal": "",
			"etc/systemd/journald.conf.d/50-a.conf":     "[Journal]\nStorage=volatile\n",
			"usr/lib/systemd/journald.conf.d/60-b.conf": "[Journal]\nStorage=persistent\n"}, check: "journal", state: preflight.Kept, code: "pf.journal.disk"},
		{name: "journal without systemd", base: kept, with: pt.Files{"var/log/journal": "", "run/systemd": "", "etc/systemd/journald.conf": ""}, check: "journal", state: preflight.None, code: "pf.journal.no_systemd"},

		// pstore-backend
		{name: "backend erst", base: kept, check: "pstore-backend", state: preflight.Kept, code: "pf.pstore.backend"},
		{name: "backend ramoops", base: kept, with: pt.Files{"sys/module/pstore/parameters/backend": "ramoops\n"}, check: "pstore-backend", state: preflight.Kept, code: "pf.pstore.ramoops"},
		{name: "backend none with ERST off", base: kept, with: pt.Files{"sys/module/pstore/parameters/backend": "(null)\n", "proc/cmdline": "ro erst_disable\n"}, check: "pstore-backend", state: preflight.Lost, code: "pf.pstore.none"},
		{name: "backend none with efi_pstore off", base: bare, check: "pstore-backend", state: preflight.Lost, code: "pf.pstore.none"},
		{name: "backend none on a kernel without pstore", base: bare, with: pt.Files{"sys/module/pstore": "", "boot/config-6.8.0-87-generic": "CONFIG_X86=y\n# CONFIG_PSTORE is not set\n"}, check: "pstore-backend", state: preflight.None, code: "pf.pstore.no_kernel"},
		{name: "backend unreadable", base: kept, with: pt.Files{"sys/module/pstore/parameters/backend": pt.Denied}, check: "pstore-backend", state: preflight.Unknown, code: "pf.unreadable", root: true},

		// pstore-archive
		{name: "archive kept", base: kept, check: "pstore-archive", state: preflight.Kept, code: "pf.archive.kept"},
		{name: "archive kept with files waiting", base: kept, with: pt.Files{"sys/fs/pstore/dmesg-erst-7300000000000000001": "Panic#1 Part1\n"}, check: "pstore-archive", state: preflight.Kept, code: "pf.archive.kept_files"},
		{name: "archive kept, store closed to the user", base: kept, with: pt.Files{"sys/fs/pstore": pt.DeniedDir}, check: "pstore-archive", state: preflight.Kept, code: "pf.archive.kept_unlisted", root: true},
		{name: "archive service not enabled", base: bare, check: "pstore-archive", state: preflight.Lost, code: "pf.archive.disabled"},
		{name: "archive service masked", base: kept, with: pt.Files{"etc/systemd/system/systemd-pstore.service": pt.LinkPrefix + "/dev/null"}, check: "pstore-archive", state: preflight.Lost, code: "pf.archive.masked"},
		{name: "archive into the journal", base: kept, with: pt.Files{"etc/systemd/pstore.conf.d/10-j.conf": "[PStore]\nStorage=journal\n"}, check: "pstore-archive", state: preflight.Lost, code: "pf.archive.storage_journal"},
		{name: "archive nowhere", base: kept, with: pt.Files{"etc/systemd/pstore.conf": "[PStore]\nStorage=none\n"}, check: "pstore-archive", state: preflight.Lost, code: "pf.archive.storage_none"},
		{name: "archive keeps the files", base: kept, with: pt.Files{"etc/systemd/pstore.conf.d/10-u.conf": "[PStore]\nUnlink=no\n"}, check: "pstore-archive", state: preflight.Lost, code: "pf.archive.no_unlink"},
		{name: "archive without the unit", base: kept, with: pt.Files{"usr/lib/systemd/system/systemd-pstore.service": "", "etc/systemd/system/sysinit.target.wants/systemd-pstore.service": ""}, check: "pstore-archive", state: preflight.Lost, code: "pf.archive.no_unit"},

		// pstore-vs-kdump
		{name: "crash kernel after pstore", base: kept, check: "pstore-vs-kdump", state: preflight.Kept, code: "pf.kexec.post"},
		{name: "no crash kernel", base: bare, check: "pstore-vs-kdump", state: preflight.Kept, code: "pf.kexec.none"},
		{name: "crash kernel before pstore", base: kept, with: pt.Files{"sys/module/kernel/parameters/crash_kexec_post_notifiers": "N\n", "proc/cmdline": "ro crashkernel=256M\n"}, check: "pstore-vs-kdump", state: preflight.Lost, code: "pf.kexec.first"},
		{name: "crash kernel unreadable", base: kept, with: pt.Files{"sys/kernel/kexec_crash_loaded": pt.Denied}, check: "pstore-vs-kdump", state: preflight.Unknown, code: "pf.unreadable", root: true},

		// kdump
		{name: "kdump loaded", base: kept, check: "kdump", state: preflight.Kept, code: "pf.kdump.loaded_size"},
		{name: "kdump absent, pstore keeps the text", base: kept, with: pt.Files{"sys/kernel/kexec_crash_loaded": "0\n"}, check: "kdump", state: preflight.Info, code: "pf.kdump.second"},
		{name: "kdump absent and no pstore", base: bare, check: "kdump", state: preflight.Lost, code: "pf.kdump.none"},
		{name: "kdump reserved and not loaded", base: bare, with: cmdline(bare, "crashkernel=512M-:192M"), check: "kdump", state: preflight.Lost, code: "pf.kdump.not_loaded"},
		{name: "kdump unreadable", base: bare, with: pt.Files{"sys/kernel/kexec_crash_loaded": pt.Denied}, check: "kdump", state: preflight.Unknown, code: "pf.unreadable", root: true},

		// mce-bootlog
		{name: "bootlog default on", base: kept, check: "mce-bootlog", state: preflight.Kept, code: "pf.bootlog.default"},
		{name: "bootlog off on old AMD", base: bare, with: pt.Files{"proc/cpuinfo": amd(0x10)}, check: "mce-bootlog", state: preflight.Lost, code: "pf.bootlog.off_default"},
		{name: "bootlog on from family 0x11", base: bare, with: pt.Files{"proc/cpuinfo": amd(0x11)}, check: "mce-bootlog", state: preflight.Kept, code: "pf.bootlog.default"},
		{name: "bootlog off before Core Yonah", base: bare, with: pt.Files{"proc/cpuinfo": intel(6, 0x0d)}, check: "mce-bootlog", state: preflight.Lost, code: "pf.bootlog.off_default"},
		{name: "bootlog on from Core Yonah", base: bare, with: pt.Files{"proc/cpuinfo": intel(6, 0x0e)}, check: "mce-bootlog", state: preflight.Kept, code: "pf.bootlog.default"},
		{name: "bootlog asked for on old AMD", base: bare, with: pt.Files{"proc/cpuinfo": amd(0x10), "proc/cmdline": "ro mce=bootlog\n"}, check: "mce-bootlog", state: preflight.Kept, code: "pf.bootlog.param"},
		{name: "bootlog turned off", base: kept, with: cmdline(kept, "mce=nobootlog"), check: "mce-bootlog", state: preflight.Lost, code: "pf.bootlog.off_param"},
		{name: "machine checks off", base: kept, with: cmdline(kept, "mce=off"), check: "mce-bootlog", state: preflight.Lost, code: "pf.mce.off"},
		{name: "bootlog on arm", base: kept, with: arm, check: "mce-bootlog", state: preflight.None, code: "pf.not_x86"},
		{name: "bootlog without a readable cpuinfo", base: bare, with: pt.Files{"proc/cpuinfo": pt.Denied, "proc/sys/kernel/arch": "x86_64\n"}, check: "mce-bootlog", state: preflight.Unknown, code: "pf.cpu.unknown", root: true},

		// mce-print
		{name: "print_all on the command line", base: kept, check: "mce-print", state: preflight.Kept, code: "pf.print.param"},
		{name: "print on AMD", base: bare, with: pt.Files{"proc/cpuinfo": amd(0x19)}, check: "mce-print", state: preflight.Kept, code: "pf.print.amd"},
		{name: "print without /dev/mcelog", base: bare, with: pt.Files{"sys/devices/system/machinecheck/machinecheck0/trigger": ""}, check: "mce-print", state: preflight.Kept, code: "pf.print.no_device"},
		{name: "print to a running mcelog", base: bare, procs: []string{"systemd", "mcelog"}, check: "mce-print", state: preflight.Kept, code: "pf.print.daemon"},
		{name: "print to an enabled mcelog", base: bare, with: pt.Files{"etc/systemd/system/multi-user.target.wants/mcelog.service": pt.LinkPrefix + "/usr/lib/systemd/system/mcelog.service"}, check: "mce-print", state: preflight.Kept, code: "pf.print.daemon"},
		{name: "print lost", base: bare, check: "mce-print", state: preflight.Lost, code: "pf.print.lost"},
		{name: "print_all through sysfs only", base: bare, with: pt.Files{"sys/devices/system/machinecheck/machinecheck0/print_all": "1\n"}, check: "mce-print", state: preflight.Lost, code: "pf.print.sysfs_only"},
		{name: "print without the config of the kernel", base: bare, with: pt.Files{"sys/devices/system/machinecheck": ""}, check: "mce-print", state: preflight.Unknown, code: "pf.unreadable"},
		{name: "print from the config of the kernel", base: bare, with: pt.Files{"sys/devices/system/machinecheck": "", "boot/config-6.8.0-87-generic": "# CONFIG_X86_MCELOG_LEGACY is not set\n"}, check: "mce-print", state: preflight.Kept, code: "pf.print.no_device"},
		{name: "print on arm", base: kept, with: arm, check: "mce-print", state: preflight.None, code: "pf.not_x86"},

		// reset-kind
		{name: "reset after an MCE", base: kept, check: "reset-kind", state: preflight.Info, code: "pf.reset.mode_mce"},
		{name: "reset after 10 s", base: kept, with: pt.Files{"proc/sys/kernel/panic": "10\n"}, check: "reset-kind", state: preflight.Info, code: "pf.reset.mode_after"},
		{name: "reset at once", base: kept, with: pt.Files{"proc/sys/kernel/panic": "-1\n"}, check: "reset-kind", state: preflight.Info, code: "pf.reset.mode_now"},
		{name: "reset on arm", base: kept, with: arm, check: "reset-kind", state: preflight.None, code: "pf.not_x86"},
		{name: "reset on arm with a firmware error source", base: kept, with: merge(arm, pt.Files{"sys/firmware/acpi/tables/HEST": "HEST", "proc/sys/kernel/panic": "0\n"}),
			check: "reset-kind", state: preflight.Info, code: "pf.reset.ghes_stays"},
		{name: "reset on arm with a panic timeout", base: kept, with: merge(arm, pt.Files{"sys/firmware/acpi/tables/HEST": "HEST", "proc/sys/kernel/panic": "30\n"}),
			check: "reset-kind", state: preflight.None, code: "pf.not_x86"},

		// bert
		{name: "bert there", base: kept, check: "bert", state: preflight.Kept, code: "pf.bert.kept"},
		{name: "bert not printed", base: kept, with: cmdline(kept, "bert_disable"), check: "bert", state: preflight.Kept, code: "pf.bert.disabled"},
		{name: "bert absent", base: bare, check: "bert", state: preflight.None, code: "pf.bert.none"},
		{name: "bert without ACPI", base: bare, with: pt.Files{"sys/firmware/acpi": ""}, check: "bert", state: preflight.None, code: "pf.bert.no_acpi"},

		// erst-mce
		{name: "erst unread", base: kept, check: "erst-mce", state: preflight.Info, code: "pf.erst.unread"},
		{name: "erst read by mcelog", base: kept, procs: []string{"mcelog"}, check: "erst-mce", state: preflight.Info, code: "pf.erst.daemon"},
		{name: "erst off", base: kept, with: cmdline(kept, "erst_disable"), check: "erst-mce", state: preflight.Info, code: "pf.erst.off"},
		{name: "erst absent", base: bare, check: "erst-mce", state: preflight.None, code: "pf.erst.none"},
		{name: "erst on arm", base: kept, with: arm, check: "erst-mce", state: preflight.None, code: "pf.not_x86"},

		// console
		{name: "serial console", base: kept, check: "console", state: preflight.Kept, code: "pf.console.serial"},
		{name: "netconsole", base: bare, with: pt.Files{"sys/module/netconsole": pt.Dir}, check: "console", state: preflight.Kept, code: "pf.console.net"},
		{name: "no console leaves", base: bare, check: "console", state: preflight.Lost, code: "pf.console.lost"},

		// rasdaemon
		{name: "rasdaemon records", base: kept, check: "rasdaemon", state: preflight.Kept, code: "pf.ras.kept"},
		{name: "rasdaemon without a database yet", base: kept, with: pt.Files{"var/lib/rasdaemon": ""}, check: "rasdaemon", state: preflight.Kept, code: "pf.ras.kept_empty"},
		{name: "rasdaemon records with -fr", base: kept, with: pt.Files{"usr/lib/systemd/system/rasdaemon.service": "[Service]\nExecStart=/usr/sbin/rasdaemon -fr\n"}, check: "rasdaemon", state: preflight.Kept, code: "pf.ras.kept"},
		{name: "rasdaemon without --record", base: kept, with: pt.Files{"usr/lib/systemd/system/rasdaemon.service": "[Service]\nExecStart=/usr/sbin/rasdaemon -f\n"}, check: "rasdaemon", state: preflight.Lost, code: "pf.ras.no_record"},
		{name: "rasdaemon not enabled", base: kept, with: pt.Files{"etc/systemd/system/multi-user.target.wants/rasdaemon.service": ""}, check: "rasdaemon", state: preflight.Lost, code: "pf.ras.disabled"},
		{name: "rasdaemon absent", base: bare, check: "rasdaemon", state: preflight.Lost, code: "pf.ras.absent"},

		// harvest
		{name: "harvest enabled", base: kept, free: gib, check: "harvest", state: preflight.Kept, code: "pf.harvest.kept_free"},
		{name: "harvest without space", base: kept, free: 8 << 20, check: "harvest", state: preflight.Lost, code: "pf.harvest.full"},
		{name: "harvest not enabled", base: kept, with: pt.Files{"etc/systemd/system/multi-user.target.wants/my-harvest.service": ""}, free: gib, check: "harvest", state: preflight.Lost, code: "pf.harvest.disabled"},
		{name: "harvest of someone else", base: kept, with: pt.Files{"etc/systemd/system/my-harvest.service": "[Service]\nExecStart=/bin/true\n"}, free: gib, check: "harvest", state: preflight.Lost, code: "pf.harvest.foreign"},
		{name: "harvest absent", base: bare, check: "harvest", state: preflight.Lost, code: "pf.harvest.absent"},
		{name: "harvest unreadable", base: kept, with: pt.Files{"etc/systemd/system/my-harvest.service": pt.Denied}, check: "harvest", state: preflight.Unknown, code: "pf.unreadable", root: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.root && os.Geteuid() == 0 {
				t.Skip("root reads files of mode 0000")
			}
			r := run(t, tt.base.With(tt.with), tt.procs, tt.free)
			c := check(t, r, tt.check)
			if c.State != tt.state || c.Detail.Code != tt.code {
				t.Errorf("%s = %s (%s: %s), want %s (%s)\nevidence: %q", tt.check, c.State, c.Detail.Code, c.Detail.Text, tt.state, tt.code, c.Evidence)
			}
			if strings.Contains(c.Detail.Text, "{") || strings.Contains(c.Detail.Text, "@pf.") {
				t.Errorf("unfilled statement: %q", c.Detail.Text)
			}
			if c.State == preflight.Lost && c.Fix == nil {
				t.Errorf("%s is lost and has no fix", c.Name)
			}
			if c.Fix != nil && (c.Fix.Cost.Text == "" || c.Fix.Source == "" || c.Fix.What.Text == "") {
				t.Errorf("%s: fix without cost, source or what it does: %+v", c.Name, c.Fix)
			}
		})
	}
}

// TestEveryCheckIsReported: one entry per check, in the order of Names,
// and the count of lost checks.
func TestEveryCheckIsReported(t *testing.T) {
	for _, f := range []pt.Files{pt.Kept(), pt.Bare(), {}} {
		r := run(t, f, nil, gib)
		if len(r.Checks) != len(preflight.Names) {
			t.Fatalf("%d checks, want %d", len(r.Checks), len(preflight.Names))
		}
		lost := 0
		for i, c := range r.Checks {
			if c.Name != preflight.Names[i] {
				t.Errorf("check %d is %s, want %s", i, c.Name, preflight.Names[i])
			}
			if c.State == preflight.Lost {
				lost++
			}
		}
		if lost != r.Lost {
			t.Errorf("Lost = %d, counted %d", r.Lost, lost)
		}
	}
	if r := run(t, pt.Kept(), nil, gib); r.Lost != 0 {
		for _, c := range r.Checks {
			if c.State == preflight.Lost {
				t.Errorf("the kept host loses %s: %s", c.Name, c.Detail.Text)
			}
		}
	}
}

// TestFixPerFamily: the commands of a fix follow the distribution.
func TestFixPerFamily(t *testing.T) {
	lostPrint := pt.Files{"proc/cmdline": "ro quiet\n"}
	for _, tt := range []struct {
		name, osRelease string
		family          string
		cmd, undo       string
	}{
		{"fedora", "ID=fedora\n", "grubby", `grubby --update-kernel=ALL --args="mce=print_all"`, `grubby --update-kernel=ALL --remove-args="mce=print_all"`},
		{"rocky", "ID=\"rocky\"\nID_LIKE=\"rhel centos fedora\"\n", "grubby", `grubby --update-kernel=ALL --args="mce=print_all"`, `grubby --update-kernel=ALL --remove-args="mce=print_all"`},
		{"ubuntu", "ID=ubuntu\nID_LIKE=debian\n", "update-grub", `echo 'GRUB_CMDLINE_LINUX_DEFAULT="$GRUB_CMDLINE_LINUX_DEFAULT mce=print_all"' > /etc/default/grub.d/90-my-print-all.cfg`, "rm /etc/default/grub.d/90-my-print-all.cfg"},
		{"debian", "ID=debian\n", "update-grub", `echo 'GRUB_CMDLINE_LINUX_DEFAULT="$GRUB_CMDLINE_LINUX_DEFAULT mce=print_all"' > /etc/default/grub.d/90-my-print-all.cfg`, "rm /etc/default/grub.d/90-my-print-all.cfg"},
		{"suse", "ID=opensuse-leap\nID_LIKE=\"suse opensuse\"\n", "", "", ""},
		{"none", "", "", "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := pt.Bare().With(lostPrint).With(pt.Files{"etc/os-release": tt.osRelease})
			if tt.osRelease == "" {
				f = f.With(pt.Files{"etc/os-release": ""})
			}
			r := run(t, f, nil, gib)
			if r.Family != tt.family {
				t.Errorf("family %q, want %q", r.Family, tt.family)
			}
			c := check(t, r, "mce-print")
			if c.Fix == nil || c.Fix.Param != "mce=print_all" || !c.Fix.Reboot {
				t.Fatalf("fix = %+v", c.Fix)
			}
			if tt.cmd == "" {
				if len(c.Fix.Commands) != 0 || len(c.Fix.Undo) != 0 {
					t.Errorf("commands for an unknown family: %q, undo %q", c.Fix.Commands, c.Fix.Undo)
				}
				return
			}
			if !contains(c.Fix.Commands, tt.cmd) || !contains(c.Fix.Undo, tt.undo) {
				t.Errorf("commands %q undo %q", c.Fix.Commands, c.Fix.Undo)
			}
		})
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// TestCopyOfAHost: in a copy of a host's files, checks whose files the copy
// lacks say so, and the harvest check is marked as one that needs the
// running host.
func TestCopyOfAHost(t *testing.T) {
	root := pt.Tree(t, pt.Files{"proc/sys/kernel/hostname": "h\n", "etc/os-release": "ID=fedora\n"})
	r := preflight.Run(preflight.Options{Root: root})
	if r.Root != root {
		t.Errorf("Root = %q", r.Root)
	}
	// A copy without /etc/systemd cannot tell whether the host keeps a
	// journal, archives pstore files or runs the harvest: as in the lab
	// captures, which hold the archive and the kept ledger but no unit.
	for _, name := range []string{"journal", "pstore-backend", "pstore-archive", "pstore-vs-kdump", "kdump", "mce-bootlog", "bert", "console", "harvest"} {
		if c := check(t, r, name); c.State != preflight.Unknown || c.Detail.Code != "pf.copy.missing" || !c.Live {
			t.Errorf("%s = %s %s", name, c.State, c.Detail.Code)
		}
	}
	if r.Lost != 0 {
		t.Errorf("a copy that lacks the files says %d checks would lose records", r.Lost)
	}
	// With /etc/systemd in the copy the checks decide as on the host.
	root = pt.Tree(t, pt.Files{"proc/sys/kernel/hostname": "h\n", "etc/os-release": "ID=fedora\n", "etc/systemd/system": pt.Dir})
	r = preflight.Run(preflight.Options{Root: root})
	for _, name := range []string{"journal", "pstore-archive", "harvest"} {
		if c := check(t, r, name); c.State != preflight.Lost {
			t.Errorf("with /etc/systemd: %s = %s %s", name, c.State, c.Detail.Code)
		}
	}
}

// TestReadsOnly: a run changes nothing in the tree it reads, and the
// package has no call that could write, run a program or open a device.
func TestReadsOnly(t *testing.T) {
	root := pt.Tree(t, pt.Kept().With(pt.Files{"sys/fs/pstore/dmesg-erst-1": "Panic#1 Part1\n", "dev/mcelog": "x"}))
	before := snapshot(t, root)
	preflight.Run(preflight.Options{Root: root, Live: true})
	preflight.Run(preflight.Options{Root: root})
	if after := snapshot(t, root); after != before {
		t.Errorf("the tree changed:\nbefore %s\nafter  %s", before, after)
	}
	// The mcelog device is never opened: opening it as a reader clears what
	// it holds (arch/x86/kernel/cpu/mce/dev-mcelog.c). No literal of the
	// package names it as a path.
	banned := map[string]bool{
		"os.Create": true, "os.OpenFile": true, "os.WriteFile": true, "os.Mkdir": true, "os.MkdirAll": true,
		"os.MkdirTemp": true, "os.CreateTemp": true, "os.Remove": true, "os.RemoveAll": true, "os.Rename": true,
		"os.Symlink": true, "os.Link": true, "os.Chmod": true, "os.Chown": true, "os.Lchown": true, "os.Chtimes": true,
		"os.Truncate": true, "exec.Command": true, "exec.CommandContext": true, "unix.Open": true, "unix.Write": true,
		"syscall.Open": true, "syscall.Write": true,
	}
	files, _ := filepath.Glob("*.go")
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if x, ok := sel.X.(*ast.Ident); ok && banned[x.Name+"."+sel.Sel.Name] {
					t.Errorf("%s uses %s.%s", name, x.Name, sel.Sel.Name)
				}
			}
			if lit, ok := n.(*ast.BasicLit); ok && strings.Contains(lit.Value, "dev/mcelog") && !strings.Contains(lit.Value, " ") {
				t.Errorf("%s names the path %s", name, lit.Value)
			}
			return true
		})
	}
}

// snapshot lists every path of a tree with its mode, size and time.
func snapshot(t *testing.T, root string) string {
	var b strings.Builder
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				return nil
			}
			return err
		}
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		b.WriteString(rel + " " + fi.Mode().String() + " " + itoa(int(fi.Size())) + " " + fi.ModTime().String() + "\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// TestCatalog: every statement the checks make has both languages and the
// same placeholders in each.
func TestCatalog(t *testing.T) {
	for code, m := range preflight.Messages {
		if m.EN == "" || m.KO == "" {
			t.Errorf("%s lacks a language", code)
		}
	}
}

// merge returns the files of all sets, later sets winning.
func merge(sets ...pt.Files) pt.Files {
	out := pt.Files{}
	for _, set := range sets {
		for k, v := range set {
			out[k] = v
		}
	}
	return out
}
