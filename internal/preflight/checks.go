package preflight

import (
	"fmt"
	"path"
	"strconv"
	"strings"
)

// journal: whether journald keeps the journal on disk.
//
// journald.conf(5): Storage= is volatile, persistent, auto or none; "auto"
// stores on disk when /var/log/journal exists. Lines below priority CRIT
// are synced every SyncIntervalSec= (5 minutes by default); CRIT, ALERT and
// EMERG at once.
func (r *run) journal() Check {
	c := Check{Name: "journal"}
	conf, files := r.h.systemdConfig("journald.conf")
	for _, f := range files {
		c.Evidence = append(c.Evidence, "/"+f)
	}
	storage := strings.ToLower(conf["Journal.Storage"])
	sync := conf["Journal.SyncIntervalSec"]
	if sync == "" {
		sync = "5min"
	}
	onDisk := r.h.isDir("var/log/journal")
	c.Evidence = append(c.Evidence, fmt.Sprintf("/var/log/journal: %s", map[bool]string{true: "a directory", false: "not there"}[onDisk]))
	if storage != "" {
		c.Evidence = append(c.Evidence, "Storage="+storage)
	}
	systemd := r.h.isDir("run/systemd/system") || r.h.there("usr/lib/systemd/systemd") || r.h.there("lib/systemd/systemd") || len(files) > 0
	switch {
	case !systemd && !onDisk && r.copyLacks("etc/systemd"):
		return r.notInCopy(c.Name, "etc/systemd")
	case !systemd && !onDisk:
		c.State, c.Detail = None, statement("pf.journal.no_systemd")
		return c
	case storage == "none":
		c.State, c.Detail = Lost, statement("pf.journal.none")
	case storage == "volatile":
		c.State, c.Detail = Lost, statement("pf.journal.volatile")
	case onDisk || storage == "persistent":
		c.State, c.Detail = Kept, statement("pf.journal.disk", "interval", sync)
		return c
	default:
		if storage == "" {
			c.State, c.Detail = Lost, statement("pf.journal.no_dir")
		} else {
			c.State, c.Detail = Lost, statement("pf.journal.no_dir_value", "storage", storage)
		}
	}
	const dropin = "/etc/systemd/journald.conf.d/90-my-persistent.conf"
	c.Fix = &Fix{
		What: statement("pf.fix.journal"),
		Commands: []string{
			"mkdir -p /etc/systemd/journald.conf.d",
			`printf '[Journal]\nStorage=persistent\n' > ` + dropin,
			"systemctl restart systemd-journald.service",
		},
		Undo:   []string{"rm " + dropin, "systemctl restart systemd-journald.service"},
		Cost:   statement("pf.cost.journal"),
		Source: "journald.conf(5): Storage=, SystemMaxUse=",
	}
	return c
}

// pstoreBackend: whether a persistent store takes the panic text.
//
// fs/pstore/platform.c, pstore_register: one backend at a time; its name is
// the module parameter "backend" (mode 0444), shown as
// /sys/module/pstore/parameters/backend, and the kernel logs
// "pstore: Registered <name> as persistent store backend".
// kmsg_dump_desc (kernel/printk/printk.c) hands a panic to every dumper
// whose max_reason is at least KMSG_DUMP_PANIC; efi_pstore and erst set
// none, which counts as up to an oops.
func (r *run) pstoreBackend() Check {
	c := Check{Name: "pstore-backend"}
	const rel = "sys/module/pstore/parameters/backend"
	v, err := r.h.read(rel)
	c.Evidence = append(c.Evidence, r.evidence(rel, v, err))
	switch {
	case err == nil && v != "" && v != "(null)":
		r.backend = v
		c.State = Kept
		if v == "ramoops" {
			c.Detail = statement("pf.pstore.ramoops")
		} else {
			c.Detail = statement("pf.pstore.backend", "backend", v)
		}
		return c
	case err != nil && !missing(err):
		c.State, c.Detail = Unknown, statement("pf.unreadable", "file", "/"+rel)
		return c
	case missing(err) && !r.opts.Live && !r.h.isDir("sys/module"):
		return r.notInCopy(c.Name, rel)
	case missing(err) && r.cfg != nil && r.cfg["CONFIG_PSTORE"] == "":
		c.Evidence = append(c.Evidence, "/"+r.cfgFile+": CONFIG_PSTORE is not set")
		c.State, c.Detail = None, statement("pf.pstore.no_kernel")
		return c
	}

	// No backend. Which one could there be: ERST first, then the EFI
	// variables, then RAM that is kept over a reset.
	c.State = Lost
	erstTable := r.h.there("sys/firmware/acpi/tables/ERST")
	erstOff := r.cmd.has("erst_disable")
	efi := r.h.isDir("sys/firmware/efi")
	const efiParam = "sys/module/efi_pstore/parameters/pstore_disable"
	efiOff, efiErr := r.h.read(efiParam)
	if efi {
		c.Evidence = append(c.Evidence, r.evidence(efiParam, efiOff, efiErr))
	}
	c.Evidence = append(c.Evidence, fmt.Sprintf("/sys/firmware/acpi/tables/ERST: %s", map[bool]string{true: "there", false: "not there"}[erstTable]))
	erst := "@pf.why.erst_absent"
	switch {
	case erstTable && erstOff:
		erst = "@pf.why.erst_disabled"
	case erstTable:
		erst = "@pf.why.erst_unregistered"
	}
	efiWhy := "@pf.why.efi_absent"
	switch {
	case efi && efiErr == nil && strings.EqualFold(efiOff, "Y"):
		efiWhy = "@pf.why.efi_disabled"
	case efi && missing(efiErr):
		efiWhy = "@pf.why.efi_unloaded"
	case efi:
		efiWhy = "@pf.why.efi_unregistered"
	}
	c.Detail = statement("pf.pstore.none", "erst", erst, "efi", efiWhy)

	switch {
	case erstTable && erstOff:
		// drivers/acpi/apei/erst.c, setup_erst_disable and erst_init:
		// with erst_disable the table is not used and nothing registers.
		c.Fix = r.unparam("erst_disable", statement("pf.fix.erst"), statement("pf.cost.erst"),
			"Documentation/admin-guide/kernel-parameters.txt: erst_disable")
	case efi && efiWhy == "@pf.why.efi_disabled" || efi && efiWhy == "@pf.why.efi_unloaded":
		// drivers/firmware/efi/efi-pstore.c: pstore_disable is a parameter
		// of mode 0644 whose setter (efi_pstore_disable_set) registers the
		// backend at once; on the command line it holds from the next boot.
		kmsg := "10240"
		if s, err := r.h.read("sys/module/pstore/parameters/kmsg_bytes"); err == nil && s != "" {
			kmsg = s
		}
		c.Fix = r.param("efi-pstore", "efi_pstore.pstore_disable=0", statement("pf.fix.efi_pstore"),
			statement("pf.cost.efi_pstore", "kmsg", kmsg, "record", "1024"),
			"drivers/firmware/efi/efi-pstore.c (record_size, pstore_disable); fs/pstore/Kconfig (PSTORE_DEFAULT_KMSG_BYTES)")
		if efiErr == nil {
			c.Fix.Commands = append([]string{"echo N > /" + efiParam}, c.Fix.Commands...)
			c.Fix.Undo = append([]string{"echo Y > /" + efiParam}, c.Fix.Undo...)
		}
	case !erstTable:
		// Documentation/admin-guide/ramoops.rst, section D: a region
		// reserved with reserve_mem= and named with ramoops.mem_name.
		c.Fix = r.param("ramoops", "reserve_mem=2M:4096:oops ramoops.mem_name=oops ramoops.ecc=1",
			statement("pf.fix.ramoops"), statement("pf.cost.ramoops"),
			"Documentation/admin-guide/ramoops.rst (section D); kernel-parameters.txt: reserve_mem")
	}
	return c
}

// pstoreArchive: whether systemd-pstore moves the dumps out of the store.
//
// units/systemd-pstore.service.in: WantedBy=sysinit.target,
// ConditionDirectoryNotEmpty=/sys/fs/pstore. src/pstore/pstore.c: Storage=
// external (the default) copies the files to /var/lib/systemd/pstore,
// journal puts their text into the journal, none leaves them; Unlink=yes
// (the default) removes them from the store afterwards. fs/pstore/inode.c,
// pstore_unlink: removing a file erases the record in the backend; a store
// that is not emptied fills, and later panics are not saved.
func (r *run) pstoreArchive() Check {
	c := Check{Name: "pstore-archive"}
	files, lerr := r.h.list("sys/fs/pstore")
	var dumps []string
	for _, f := range files {
		dumps = append(dumps, f)
	}
	switch {
	case lerr == nil:
		c.Evidence = append(c.Evidence, fmt.Sprintf("/sys/fs/pstore: %d %s", len(dumps), plural(len(dumps), "file", "files")))
	default:
		c.Evidence = append(c.Evidence, r.evidence("sys/fs/pstore", "", lerr))
	}
	u := r.h.unit("systemd-pstore.service", "sysinit.target")
	conf, cfiles := r.h.systemdConfig("pstore.conf")
	for _, f := range cfiles {
		c.Evidence = append(c.Evidence, "/"+f)
	}
	storage := strings.ToLower(conf["PStore.Storage"])
	if storage == "" {
		storage = "external"
	}
	unlink := true
	if v, ok := conf["PStore.Unlink"]; ok {
		unlink = parseBool(v, true)
	}
	if u.file != "" {
		c.Evidence = append(c.Evidence, "/"+u.file)
	}
	if u.enabled != "" {
		c.Evidence = append(c.Evidence, "/"+u.enabled)
	}
	left := len(dumps)

	enable := &Fix{
		What:     statement("pf.fix.pstore_archive"),
		Commands: []string{"systemctl enable systemd-pstore.service"},
		Undo:     []string{"systemctl disable systemd-pstore.service"},
		Cost:     statement("pf.cost.pstore_archive"),
		Source:   "systemd-pstore.service(8); pstore.conf(5)",
	}
	external := &Fix{
		What: statement("pf.fix.pstore_external"),
		Commands: []string{
			"mkdir -p /etc/systemd/pstore.conf.d",
			`printf '[PStore]\nStorage=external\nUnlink=yes\n' > /etc/systemd/pstore.conf.d/90-my-external.conf`,
		},
		Undo:   []string{"rm /etc/systemd/pstore.conf.d/90-my-external.conf"},
		Cost:   statement("pf.cost.pstore_archive"),
		Source: "pstore.conf(5): Storage=, Unlink=",
	}
	switch {
	case !u.present && !u.masked && r.copyLacks("etc/systemd", "usr/lib/systemd", "lib/systemd"):
		return r.notInCopy(c.Name, "usr/lib/systemd/system")
	case !u.present && !u.masked:
		c.State, c.Detail = Lost, statement("pf.archive.no_unit")
		c.Fix = &Fix{What: statement("pf.fix.pstore_by_hand"), Cost: statement("pf.cost.by_hand"), Source: "fs/pstore/inode.c (pstore_unlink)"}
	case u.masked:
		c.State, c.Detail = Lost, statement("pf.archive.masked")
		enable.Commands = append([]string{"systemctl unmask systemd-pstore.service"}, enable.Commands...)
		enable.Undo = append(enable.Undo, "systemctl mask systemd-pstore.service")
		c.Fix = enable
	case storage == "none":
		c.State, c.Detail, c.Fix = Lost, statement("pf.archive.storage_none"), external
	case storage == "journal":
		c.State, c.Detail, c.Fix = Lost, statement("pf.archive.storage_journal"), external
	case !unlink:
		c.State, c.Detail, c.Fix = Lost, statement("pf.archive.no_unlink"), external
	case u.enabled == "":
		c.State, c.Detail, c.Fix = Lost, statement("pf.archive.disabled"), enable
	case lerr != nil:
		c.State, c.Detail = Kept, statement("pf.archive.kept_unlisted")
	case left > 0:
		c.State, c.Detail = Kept, statement("pf.archive.kept_files", "files", left)
	default:
		c.State, c.Detail = Kept, statement("pf.archive.kept")
	}
	return c
}

func parseBool(s string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "yes", "y", "true", "t", "on":
		return true
	case "0", "no", "n", "false", "f", "off":
		return false
	}
	return def
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// crashKernel reads whether a crash kernel is loaded: 1, 0, or -1 when the
// file cannot be read.
//
// kernel/kexec_core.c, crash_loaded_show: /sys/kernel/kexec/crash_loaded,
// with the link /sys/kernel/kexec_crash_loaded that older kernels had as a
// file of their own (kernel/ksysfs.c). Neither is there without
// CONFIG_CRASH_DUMP: then no crash kernel can be loaded.
func (r *run) crashKernel(c *Check) int {
	for _, rel := range []string{"sys/kernel/kexec_crash_loaded", "sys/kernel/kexec/crash_loaded"} {
		v, err := r.h.read(rel)
		if missing(err) {
			continue
		}
		c.Evidence = append(c.Evidence, r.evidence(rel, v, err))
		if err != nil {
			return -1
		}
		if v == "1" {
			return 1
		}
		return 0
	}
	if !r.opts.Live && !r.h.isDir("sys/kernel") {
		return -1
	}
	c.Evidence = append(c.Evidence, "/sys/kernel/kexec_crash_loaded: not there")
	return 0
}

// pstoreVsKdump: whether a loaded crash kernel leaves pstore empty.
//
// kernel/panic.c, vpanic: without crash_kexec_post_notifiers the kernel
// runs __crash_kexec() before the panic notifiers and before
// kmsg_dump_desc(KMSG_DUMP_PANIC), so pstore gets nothing when a crash
// kernel is loaded. The option is core_param(crash_kexec_post_notifiers,
// ..., 0644): /sys/module/kernel/parameters/crash_kexec_post_notifiers.
func (r *run) pstoreVsKdump() Check {
	c := Check{Name: "pstore-vs-kdump"}
	r.crashLoaded = r.crashKernel(&c)
	switch r.crashLoaded {
	case -1:
		if !r.opts.Live {
			return r.notInCopy(c.Name, "sys/kernel/kexec_crash_loaded")
		}
		c.State, c.Detail = Unknown, statement("pf.unreadable", "file", "/sys/kernel/kexec_crash_loaded")
		return c
	case 0:
		c.State, c.Detail = Kept, statement("pf.kexec.none")
		return c
	}
	const rel = "sys/module/kernel/parameters/crash_kexec_post_notifiers"
	v, err := r.h.read(rel)
	c.Evidence = append(c.Evidence, r.evidence(rel, v, err))
	if (err == nil && strings.EqualFold(v, "Y")) || r.cmd.has("crash_kexec_post_notifiers") {
		c.State, c.Detail = Kept, statement("pf.kexec.post")
		return c
	}
	c.State, c.Detail = Lost, statement("pf.kexec.first")
	c.Fix = r.param("post-notifiers", "crash_kexec_post_notifiers", statement("pf.fix.post_notifiers"),
		statement("pf.cost.post_notifiers"), "Documentation/admin-guide/kernel-parameters.txt: crash_kexec_post_notifiers")
	return c
}

// kdump: whether a crash kernel is loaded. It is a second way next to
// pstore: when pstore keeps the panic text, its absence is no loss.
func (r *run) kdump() Check {
	c := Check{Name: "kdump"}
	loaded := r.crashLoaded
	if loaded == 0 || loaded == 1 {
		// The evidence of pstore-vs-kdump, read once.
		var scratch Check
		r.crashKernel(&scratch)
		c.Evidence = scratch.Evidence
	}
	switch loaded {
	case -1:
		if !r.opts.Live {
			return r.notInCopy(c.Name, "sys/kernel/kexec_crash_loaded")
		}
		c.State, c.Detail = Unknown, statement("pf.unreadable", "file", "/sys/kernel/kexec_crash_loaded")
		return c
	case 1:
		size := ""
		for _, rel := range []string{"sys/kernel/kexec_crash_size", "sys/kernel/kexec/crash_size"} {
			if v, err := r.h.read(rel); err == nil {
				size = v
				c.Evidence = append(c.Evidence, r.evidence(rel, v, nil))
				break
			}
		}
		if n, err := strconv.ParseInt(size, 10, 64); err == nil && n > 0 {
			c.State, c.Detail = Kept, statement("pf.kdump.loaded_size", "mib", n>>20)
		} else {
			c.State, c.Detail = Kept, statement("pf.kdump.loaded")
		}
		return c
	}
	reserved := r.cmd.has("crashkernel")
	if r.backend != "" {
		c.State, c.Detail = Info, statement("pf.kdump.second", "backend", r.backend)
		return c
	}
	if reserved {
		c.State, c.Detail = Lost, statement("pf.kdump.not_loaded")
	} else {
		c.State, c.Detail = Lost, statement("pf.kdump.none")
	}
	c.Fix = &Fix{What: statement("pf.fix.kdump"), Cost: statement("pf.cost.kdump"),
		Source: "Documentation/admin-guide/kdump/kdump.rst", Reboot: true}
	switch r.family {
	case familyGrubby:
		// kdumpctl(8) of kdump-utils: reset-crashkernel sets the default
		// crashkernel= for the kernels; get-default-crashkernel prints it.
		c.Fix.Commands = []string{"dnf install /usr/bin/kdumpctl", "kdumpctl reset-crashkernel --kernel=ALL", "systemctl enable kdump.service"}
		c.Fix.Undo = []string{"systemctl disable kdump.service", `grubby --update-kernel=ALL --remove-args="crashkernel"`}
		c.Fix.Cost = statement("pf.cost.kdump_fedora")
		c.Fix.Source = "kdumpctl(8) (kdump-utils); Documentation/admin-guide/kdump/kdump.rst"
	case familyDebian:
		// Debian kdump-tools: debian/rules installs
		// debian/kdump-tools.grub.default, which adds
		// crashkernel=512M-:192M, as /etc/default/grub.d/kdump-tools.cfg.
		c.Fix.Commands = []string{"apt install kdump-tools", "update-grub"}
		c.Fix.Undo = []string{"apt purge kdump-tools", "update-grub"}
		c.Fix.Cost = statement("pf.cost.kdump_debian")
		c.Fix.Source = "Debian kdump-tools: debian/kdump-tools.grub.default, debian/rules"
	}
	return c
}

// mceBootlog: whether the next boot logs what the machine check banks hold.
//
// arch/x86/kernel/cpu/mce/core.c: __mcheck_cpu_init_prepare_banks polls
// every bank with MCP_UC | MCP_QUEUE_LOG when mca_cfg.bootlog is set, and
// then clears the banks either way. bootlog starts at -1 (on);
// amd_apply_global_quirks sets it to 0 below AMD family 0x11 and
// intel_apply_global_quirks below Intel Core Yonah (family 6 model 0x0e);
// mcheck_enable takes mce=bootlog, mce=nobootlog and mce=off.
func (r *run) mceBootlog() Check {
	c := Check{Name: "mce-bootlog"}
	if nc, not := r.notX86(c.Name); not {
		return nc
	}
	vals := r.cmd.values("mce")
	if len(vals) > 0 {
		c.Evidence = append(c.Evidence, "/proc/cmdline: mce="+strings.Join(vals, ",mce="))
	}
	last := ""
	for _, v := range vals {
		if v == "bootlog" || v == "nobootlog" {
			last = v
		}
	}
	switch {
	case r.cmd.hasValue("mce", "off"):
		c.State, c.Detail = Lost, statement("pf.mce.off")
		c.Fix = r.unparam("mce=off", statement("pf.fix.mce_off"), statement("pf.cost.mce_off"), "Documentation/admin-guide/kernel-parameters.txt: mce=")
		return c
	case last == "nobootlog":
		c.State, c.Detail = Lost, statement("pf.bootlog.off_param")
		c.Fix = r.unparam("mce=nobootlog", statement("pf.fix.bootlog"), statement("pf.cost.bootlog"), "arch/x86/kernel/cpu/mce/core.c (amd_apply_global_quirks, mcheck_enable)")
		return c
	case last == "bootlog":
		c.State, c.Detail = Kept, statement("pf.bootlog.param")
		return c
	case !r.cpu.known:
		if !r.opts.Live && !r.h.there("proc/cpuinfo") {
			return r.notInCopy(c.Name, "proc/cpuinfo")
		}
		c.State, c.Detail = Unknown, statement("pf.cpu.unknown")
		return c
	}
	c.Evidence = append(c.Evidence, fmt.Sprintf("/proc/cpuinfo: %s family %d model %d", r.cpu.vendor, r.cpu.family, r.cpu.model))
	off := false
	switch r.cpu.vendor {
	case "AuthenticAMD":
		off = r.cpu.family < 0x11
	case "GenuineIntel":
		off = r.cpu.family < 6 || (r.cpu.family == 6 && r.cpu.model < 0x0e)
	}
	if !off {
		c.State, c.Detail = Kept, statement("pf.bootlog.default", "vendor", vendorName(r.cpu.vendor), "family", r.cpu.family)
		return c
	}
	c.State, c.Detail = Lost, statement("pf.bootlog.off_default", "vendor", vendorName(r.cpu.vendor), "family", r.cpu.family)
	c.Fix = r.param("bootlog", "mce=bootlog", statement("pf.fix.bootlog"), statement("pf.cost.bootlog"),
		"arch/x86/kernel/cpu/mce/core.c (amd_apply_global_quirks, intel_apply_global_quirks)")
	return c
}

// mcePrint: whether a record polled at boot reaches the kernel log.
//
// arch/x86/kernel/cpu/mce/core.c, mce_default_notifier: a record is
// printed when mca_cfg.print_all is set or no consumer marked it handled
// (m.kflags). arch/x86/kernel/cpu/mce/dev-mcelog.c, dev_mce_log: the
// /dev/mcelog device (CONFIG_X86_MCELOG_LEGACY) marks every record
// MCE_HANDLED_MCELOG, except on AMD. The machinecheck devices have the
// attribute "trigger" only with that device (mce_device_attrs). The
// record polled at boot is processed at late_initcall (mcheck_late_init),
// before user space: print_all set through sysfs later does not reach it.
func (r *run) mcePrint() Check {
	c := Check{Name: "mce-print"}
	if nc, not := r.notX86(c.Name); not {
		return nc
	}
	const printAll = "sys/devices/system/machinecheck/machinecheck0/print_all"
	const trigger = "sys/devices/system/machinecheck/machinecheck0/trigger"
	if r.cmd.hasValue("mce", "print_all") {
		c.Evidence = append(c.Evidence, "/proc/cmdline: mce=print_all")
		c.State, c.Detail = Kept, statement("pf.print.param")
		return c
	}
	if r.cpu.known && r.cpu.vendor == "AuthenticAMD" {
		c.State, c.Detail = Kept, statement("pf.print.amd")
		return c
	}
	pa, paErr := r.h.read(printAll)
	c.Evidence = append(c.Evidence, r.evidence(printAll, pa, paErr))
	legacy := -1 // unknown
	switch {
	case r.h.there(trigger):
		legacy = 1
	case paErr == nil:
		// The machinecheck device is there and has no trigger attribute.
		legacy = 0
	case r.cfg != nil:
		legacy = 0
		if r.cfg["CONFIG_X86_MCELOG_LEGACY"] == "y" {
			legacy = 1
		}
		c.Evidence = append(c.Evidence, fmt.Sprintf("/%s: CONFIG_X86_MCELOG_LEGACY=%s", r.cfgFile, orNotSet(r.cfg["CONFIG_X86_MCELOG_LEGACY"])))
	}
	if legacy == 1 {
		c.Evidence = append(c.Evidence, "/"+trigger+": there")
	}
	daemon := r.mcelogDaemon(&c)
	switch {
	case legacy == 0:
		c.State, c.Detail = Kept, statement("pf.print.no_device")
	case daemon:
		c.State, c.Detail = Kept, statement("pf.print.daemon")
	case legacy == -1:
		if !r.opts.Live {
			return r.notInCopy(c.Name, printAll)
		}
		c.State, c.Detail = Unknown, statement("pf.unreadable", "file", "/"+printAll)
	default:
		c.State = Lost
		if paErr == nil && pa == "1" {
			c.Detail = statement("pf.print.sysfs_only")
		} else {
			c.Detail = statement("pf.print.lost")
		}
		c.Fix = r.param("print-all", "mce=print_all", statement("pf.fix.print_all"), statement("pf.cost.print_all"),
			"arch/x86/kernel/cpu/mce/core.c (mce_default_notifier, __print_mce)")
	}
	return c
}

func orNotSet(v string) string {
	if v == "" {
		return "not set"
	}
	return v
}

// mcelogDaemon reports whether the mcelog daemon reads /dev/mcelog: a
// process of that name on a live host, or its unit enabled.
func (r *run) mcelogDaemon(c *Check) bool {
	if r.h.procs != nil {
		names, err := r.h.procs()
		if err == nil {
			for _, n := range names {
				if n == "mcelog" {
					c.Evidence = append(c.Evidence, "a process named mcelog runs")
					r.mcelogd = true
					return true
				}
			}
		}
	}
	if u := r.h.unit("mcelog.service", "multi-user.target"); u.enabled != "" {
		c.Evidence = append(c.Evidence, "/"+u.enabled)
		r.mcelogd = true
		return true
	}
	return false
}

// resetKind: how the host restarts after a panic. Information only.
//
// kernel/reboot.c: mode_show gives reboot_mode as /sys/kernel/reboot/mode;
// reboot_setup takes reboot=, with the prefix "panic_" for the mode of a
// restart after a panic (panic_reboot_mode, applied in kernel/panic.c).
// /proc/sys/kernel/panic is panic_timeout; mce_panic copies
// mca_cfg.panic_timeout (30 when boot logging is on) into it when it is 0
// (arch/x86/kernel/cpu/mce/core.c).
func (r *run) resetKind() Check {
	c := Check{Name: "reset-kind", State: Info}
	if nc, not := r.notX86(c.Name); not {
		// A firmware error source on another architecture: a fatal
		// record ends in __ghes_panic (drivers/acpi/apei/ghes.c), which
		// panics with the timeout as it is; with panic=0 the host stays
		// down until something outside restarts it. The aarch64 guest of
		// the lab did (docs/validation-lab.md).
		if nc.State == None && r.h.there("sys/firmware/acpi/tables/HEST") {
			if v, err := r.h.read("proc/sys/kernel/panic"); err == nil && strings.TrimSpace(v) == "0" {
				c.Evidence = append(c.Evidence, "/sys/firmware/acpi/tables/HEST: there", "/proc/sys/kernel/panic: 0")
				c.Detail = statement("pf.reset.ghes_stays", "arch", r.info.Arch)
				return c
			}
		}
		return nc
	}
	const rel = "sys/kernel/reboot/mode"
	mode, err := r.h.read(rel)
	c.Evidence = append(c.Evidence, r.evidence(rel, mode, err))
	if err != nil {
		mode = "@pf.word.unknown"
	}
	// reboot_setup reads the first letter of the mode.
	panicMode := "same"
	for _, v := range r.cmd.values("reboot") {
		for _, part := range strings.Split(v, ",") {
			if m, ok := strings.CutPrefix(part, "panic_"); ok && m != "" {
				if w := map[byte]string{'w': "warm", 'c': "cold", 'h': "hard", 's': "soft", 'g': "gpio"}[m[0]]; w != "" {
					panicMode = w
				}
			}
		}
	}
	timeout := ""
	if v, err := r.h.read("proc/sys/kernel/panic"); err == nil {
		timeout = v
		c.Evidence = append(c.Evidence, "/proc/sys/kernel/panic: "+v)
	}
	switch n, err := strconv.Atoi(timeout); {
	case err != nil:
		c.Detail = statement("pf.reset.mode", "mode", mode, "panic", "@pf.after."+panicMode)
	case n == 0:
		c.Detail = statement("pf.reset.mode_mce", "mode", mode, "panic", "@pf.after."+panicMode)
	case n < 0:
		c.Detail = statement("pf.reset.mode_now", "mode", mode, "panic", "@pf.after."+panicMode)
	default:
		c.Detail = statement("pf.reset.mode_after", "mode", mode, "panic", "@pf.after."+panicMode, "seconds", n)
	}
	if panicMode != "warm" {
		c.Fix = r.param("panic-warm", "reboot=panic_warm", statement("pf.fix.panic_warm"), statement("pf.cost.panic_warm"),
			"kernel/reboot.c (reboot_setup); Documentation/admin-guide/kernel-parameters.txt: reboot=")
	}
	return c
}

// bert: whether the firmware leaves its boot error records for the next
// boot.
//
// drivers/acpi/sysfs.c: every ACPI table is a file below
// /sys/firmware/acpi/tables (acpi_table_attr_init, mode 0400), and
// acpi_bert_data_init adds the region as /sys/firmware/acpi/tables/data/BERT.
// bert_disable stops only the printing (drivers/acpi/apei/bert.c,
// bert_init); the region stays readable.
func (r *run) bert() Check {
	c := Check{Name: "bert"}
	switch {
	case r.h.there("sys/firmware/acpi/tables/BERT"):
		c.Evidence = append(c.Evidence, "/sys/firmware/acpi/tables/BERT: there")
		c.State = Kept
		if r.cmd.has("bert_disable") {
			c.Detail = statement("pf.bert.disabled")
		} else {
			c.Detail = statement("pf.bert.kept")
		}
	case r.h.isDir("sys/firmware/acpi/tables"):
		c.Evidence = append(c.Evidence, "/sys/firmware/acpi/tables/BERT: not there")
		c.State, c.Detail = None, statement("pf.bert.none")
	case !r.opts.Live && !r.h.isDir("sys/firmware"):
		return r.notInCopy(c.Name, "sys/firmware/acpi/tables")
	default:
		c.Evidence = append(c.Evidence, "/sys/firmware/acpi/tables: not there")
		c.State, c.Detail = None, statement("pf.bert.no_acpi")
	}
	return c
}

// erstMCE: where mce_panic leaves a copy of the record, and who reads it.
//
// arch/x86/kernel/cpu/mce/core.c, mce_panic, calls apei_write_mce
// (arch/x86/kernel/cpu/mce/apei.c) for the records it prints. A reader of
// /dev/mcelog takes them back out, and the read clears them (dev-mcelog.c,
// __mce_read_apei). With erst as the pstore backend, pstore lists them as
// well: erst_reader (drivers/acpi/apei/erst.c) gives a record of section
// type CPER_SECTION_TYPE_MCE the pstore type PSTORE_TYPE_MCE, whose files
// are mce-erst-<id>; the lab showed systemd-pstore archiving them. MemorY
// never opens /dev/mcelog and does not read those files.
func (r *run) erstMCE() Check {
	c := Check{Name: "erst-mce"}
	if nc, not := r.notX86(c.Name); not {
		return nc
	}
	if !r.h.there("sys/firmware/acpi/tables/ERST") {
		if !r.opts.Live && !r.h.isDir("sys/firmware") {
			return r.notInCopy(c.Name, "sys/firmware/acpi/tables")
		}
		c.Evidence = append(c.Evidence, "/sys/firmware/acpi/tables/ERST: not there")
		c.State, c.Detail = None, statement("pf.erst.none")
		return c
	}
	c.Evidence = append(c.Evidence, "/sys/firmware/acpi/tables/ERST: there")
	if r.cmd.has("erst_disable") {
		c.State, c.Detail = Info, statement("pf.erst.off")
		return c
	}
	if !r.mcelogd {
		var scratch Check
		r.mcelogDaemon(&scratch)
	}
	c.State = Info
	if r.mcelogd {
		c.Detail = statement("pf.erst.daemon")
	} else {
		c.Detail = statement("pf.erst.unread")
	}
	return c
}

// console: whether the last lines before a reset leave the host.
//
// /proc/consoles lists the registered consoles (fs/proc/consoles.c,
// show_console_dev). console=ttyS<n> and the like on the command line
// send the kernel's messages to a serial port; netconsole sends them over
// UDP (Documentation/networking/netconsole.rst).
func (r *run) console() Check {
	c := Check{Name: "console"}
	var serial, net []string
	for _, v := range r.cmd.values("console") {
		name, _, _ := strings.Cut(v, ",")
		if serialName(name) {
			serial = append(serial, name)
		}
	}
	if s, err := r.h.read("proc/consoles"); err == nil {
		c.Evidence = append(c.Evidence, r.evidence("proc/consoles", s, nil))
		for _, line := range strings.Split(s, "\n") {
			if f := strings.Fields(line); len(f) > 0 {
				if serialName(f[0]) && !contains(serial, f[0]) {
					serial = append(serial, f[0])
				}
				if strings.HasPrefix(f[0], "netcon") {
					net = append(net, f[0])
				}
			}
		}
	} else if !r.opts.Live && len(r.cmd) == 0 {
		return r.notInCopy(c.Name, "proc/consoles")
	}
	if r.cmd.has("netconsole") || r.h.isDir("sys/module/netconsole") {
		net = append(net, "netconsole")
	}
	switch {
	case len(serial) > 0:
		c.State, c.Detail = Kept, statement("pf.console.serial", "console", strings.Join(serial, ", "))
	case len(net) > 0:
		c.State, c.Detail = Kept, statement("pf.console.net")
	default:
		names := r.cmd.values("console")
		shown := "@pf.word.none_given"
		if len(names) > 0 {
			shown = "console=" + strings.Join(names, " console=")
		}
		c.State, c.Detail = Lost, statement("pf.console.lost", "consoles", shown)
		c.Fix = &Fix{What: statement("pf.fix.console"), Cost: statement("pf.cost.console"),
			Source: "Documentation/admin-guide/serial-console.rst; Documentation/networking/netconsole.rst"}
	}
	return c
}

func serialName(s string) bool {
	for _, p := range []string{"ttyS", "ttyAMA", "ttySAC", "ttyPS", "ttymxc", "ttyMSM", "ttyUSB", "hvc", "ttysclp", "ttyLP"} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// rasdaemonPaths are where the rasdaemon database is kept: the path of
// distribution packages and that of a build with the default prefix.
var rasdaemonPaths = []string{"var/lib/rasdaemon/ras-mc_event.db", "usr/local/var/lib/rasdaemon/ras-mc_event.db"}

// rasdaemon: whether rasdaemon records the errors of a running boot.
// rasdaemon(8): -r / --record stores the events in its database; the unit
// rasdaemon ships starts "rasdaemon -f -r" (misc/rasdaemon.service.in).
func (r *run) rasdaemon() Check {
	c := Check{Name: "rasdaemon"}
	db := ""
	for _, p := range rasdaemonPaths {
		if ok, err := r.h.exists(p); ok || (err != nil && denied(err)) {
			db = "/" + p
			break
		}
	}
	u := r.h.unit("rasdaemon.service", "multi-user.target")
	if db != "" {
		c.Evidence = append(c.Evidence, db+": there")
	}
	if u.enabled != "" {
		c.Evidence = append(c.Evidence, "/"+u.enabled)
	}
	record := true
	if u.file != "" {
		if s, err := r.h.read(u.file); err == nil {
			for _, line := range strings.Split(s, "\n") {
				if v, ok := strings.CutPrefix(strings.TrimSpace(line), "ExecStart="); ok {
					record = hasFlag(v, "-r", "--record")
				}
			}
		}
	}
	install := map[family][]string{
		familyGrubby: {"dnf install rasdaemon", "systemctl enable --now rasdaemon.service"},
		familyDebian: {"apt install rasdaemon", "systemctl enable --now rasdaemon.service"},
	}
	fix := &Fix{What: statement("pf.fix.rasdaemon"), Commands: install[r.family],
		Undo: []string{"systemctl disable --now rasdaemon.service"}, Cost: statement("pf.cost.rasdaemon"), Source: "rasdaemon(8): --record"}
	switch {
	case u.enabled != "" && record && db != "":
		c.State, c.Detail = Kept, statement("pf.ras.kept", "db", db)
	case u.enabled != "" && record:
		c.State, c.Detail = Kept, statement("pf.ras.kept_empty")
	case u.enabled != "":
		c.State, c.Detail = Lost, statement("pf.ras.no_record")
		fix.Commands = nil
	case u.present:
		c.State, c.Detail = Lost, statement("pf.ras.disabled")
		fix.Commands = []string{"systemctl enable --now rasdaemon.service"}
	case r.copyLacks("etc/systemd"):
		return r.notInCopy(c.Name, "etc/systemd/system")
	default:
		c.State, c.Detail = Lost, statement("pf.ras.absent")
	}
	if c.State == Lost {
		c.Fix = fix
	}
	return c
}

// hasFlag reports whether a command line holds one of the flags, alone or
// among the letters of a group of short flags ("-fr").
func hasFlag(cmd string, flags ...string) bool {
	for _, w := range strings.Fields(cmd) {
		for _, f := range flags {
			if w == f {
				return true
			}
			if len(f) == 2 && strings.HasPrefix(w, "-") && !strings.HasPrefix(w, "--") && strings.ContainsRune(w[1:], rune(f[1])) {
				return true
			}
		}
	}
	return false
}

// harvest: whether "my harvest" runs at every boot and can write its
// ledger.
func (r *run) harvest() Check {
	c := Check{Name: "harvest", Live: true}
	rel := "etc/systemd/system/" + HarvestUnit
	text, err := r.h.read(rel)
	ours := err == nil && strings.HasPrefix(text, UnitMarker)
	wants := "etc/systemd/system/multi-user.target.wants/" + HarvestUnit
	enabled := r.h.there(wants)
	c.Evidence = append(c.Evidence, r.evidence(rel, map[bool]string{true: "written by my harvest install", false: "another file"}[ours], err))
	if enabled {
		c.Evidence = append(c.Evidence, "/"+wants)
	}
	fix := &Fix{What: statement("pf.fix.harvest"), Commands: []string{"my harvest install"}, Undo: []string{"my harvest uninstall"},
		Cost: statement("pf.cost.harvest"), Source: "docs/crash-loop.md"}
	switch {
	case missing(err) && r.copyLacks("etc/systemd"):
		return r.notInCopy(c.Name, "etc/systemd/system")
	case missing(err):
		c.State, c.Detail, c.Fix = Lost, statement("pf.harvest.absent"), fix
		return c
	case err != nil:
		c.State, c.Detail = Unknown, statement("pf.unreadable", "file", "/"+rel)
		return c
	case !ours:
		// "my harvest install" leaves a file of that name alone.
		c.State, c.Detail = Lost, statement("pf.harvest.foreign", "file", "/"+rel)
		c.Fix = &Fix{What: statement("pf.fix.harvest_foreign", "file", "/"+rel), Cost: statement("pf.cost.harvest"), Source: "docs/crash-loop.md"}
		return c
	case !enabled:
		c.State, c.Detail, c.Fix = Lost, statement("pf.harvest.disabled"), fix
		fix.Commands = []string{"systemctl enable " + HarvestUnit}
		return c
	}
	ledger := r.opts.Ledger
	dir := path.Dir(ledger)
	if r.h.freeBytes == nil {
		c.State, c.Detail = Kept, statement("pf.harvest.kept", "ledger", ledger)
		return c
	}
	probe := dir
	for probe != "/" && probe != "." && !r.h.isDir(strings.TrimPrefix(probe, "/")) {
		probe = path.Dir(probe)
	}
	free, ferr := r.h.freeBytes(r.h.path(strings.TrimPrefix(probe, "/")))
	switch {
	case ferr != nil:
		c.State, c.Detail = Kept, statement("pf.harvest.kept", "ledger", ledger)
	case free < MinFree:
		c.Evidence = append(c.Evidence, fmt.Sprintf("%s: %d MiB free", dir, free>>20))
		c.State, c.Detail = Lost, statement("pf.harvest.full", "ledger", ledger, "mib", int64(free>>20))
		c.Fix = &Fix{What: statement("pf.fix.harvest_space"), Cost: statement("pf.cost.harvest_space"), Source: "docs/crash-loop.md"}
	default:
		c.Evidence = append(c.Evidence, fmt.Sprintf("%s: %d MiB free", dir, free>>20))
		c.State, c.Detail = Kept, statement("pf.harvest.kept_free", "ledger", ledger, "mib", int64(free>>20))
	}
	return c
}

// notX86 returns the outcome of a check about x86 machine checks on a host
// that is not x86, or whose architecture is not known.
func (r *run) notX86(name string) (Check, bool) {
	if r.x86() {
		return Check{}, false
	}
	if r.info.Arch == "" && !r.cpu.known {
		if !r.opts.Live {
			return r.notInCopy(name, "proc/cpuinfo"), true
		}
		return Check{Name: name, State: Unknown, Detail: statement("pf.cpu.unknown")}, true
	}
	return Check{Name: name, State: None, Detail: statement("pf.not_x86", "arch", r.info.Arch)}, true
}
