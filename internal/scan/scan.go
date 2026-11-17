// Package scan finds the memory error sources of a host and reads them
// into a ledger. It needs no configuration: it looks in the places a Linux
// host keeps these records and says, for each place, what it found.
//
// A scan only reads. It runs as any user; what a source denies to that
// user is recorded in the source inventory with the reason, so that a
// reader can tell what a finding did not see.
//
// Sources, in the order they are read:
//
//	host facts   /proc and /sys/class/dmi: host name, kernel, boot time
//	rasdaemon    the SQLite database rasdaemon keeps
//	kernel log   the systemd journal of every boot on record, and the
//	             kernel ring buffer of the running boot (/dev/kmsg);
//	             where neither can be read, the first log file found
//	             under /var/log
//	EDAC         the per-DIMM counters under /sys/devices/system/edac
//	SMBIOS       the memory device tables under /sys/firmware/dmi
//	module SPD   the eeprom files of the SPD drivers ee1004 and spd5118
//	             under /sys/bus/i2c (no root needed), at most 5 seconds;
//	             a virtual machine is not probed
//	boot errors  the Boot Error Region of the firmware
//	             (/sys/firmware/acpi/tables/data/BERT, root only)
//	crash logs   the kernel log of a boot that crashed, where it outlived
//	             the crash: pstore files in /sys/fs/pstore, their archive
//	             in /var/lib/systemd/pstore, the text logs of kdump in
//	             /var/crash
//	mcelog       on a live host with the mcelog program, the counts its
//	             daemon keeps per DIMM ("mcelog --client")
//	GPU memory   on a live host, what "nvidia-smi -q -x" prints
//	BMC log      in a copy of a host's files, a saved "ipmitool sel" listing
//
// The same code reads a copy of a host's files (a directory with var, sys
// and proc below it): only files are read then, no commands are run.
package scan

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/bert"
	"github.com/agenticode/my/internal/adapter/crashlog"
	"github.com/agenticode/my/internal/adapter/dimmspd"
	"github.com/agenticode/my/internal/adapter/edacsysfs"
	"github.com/agenticode/my/internal/adapter/hostinfo"
	"github.com/agenticode/my/internal/adapter/hostmem"
	"github.com/agenticode/my/internal/adapter/klog"
	"github.com/agenticode/my/internal/adapter/mcelog"
	"github.com/agenticode/my/internal/adapter/nvidia"
	"github.com/agenticode/my/internal/adapter/rasdaemon"
	"github.com/agenticode/my/internal/adapter/sel"
	"github.com/agenticode/my/internal/adapter/smbios"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/preflight"
	"github.com/agenticode/my/internal/store"
)

// Options describe a scan.
type Options struct {
	// Root is the file system root of the host: "/" (the default) for the
	// running host, or the top of a copy of a host's files.
	Root string
	// Live says that Root is the running host. Commands of the host (the
	// journal) and its devices (/dev/kmsg) are only used on a live host.
	Live bool
	// Privileged records whether the scan runs as root.
	Privileged bool
	// Now is the time of the scan. Zero means the current time on a live
	// host. For a copy of a host's files zero means the time the copy was
	// made: its boot time plus its uptime when it holds /proc/stat and
	// /proc/uptime, else the newest time of the files that are read.
	// Counters and seconds since boot in the copy count back from that
	// moment.
	Now time.Time
	// Host names the host in the ledger; empty takes the name the host
	// gives itself.
	Host string
	// TempDir is where the private copy of the rasdaemon database is made;
	// empty means the system temporary directory.
	TempDir string
	// Journalctl is the journalctl program. Empty looks it up in PATH; "-"
	// leaves the journal out.
	Journalctl string
	// JournalTimeout bounds the reading of the journal; zero means
	// DefaultJournalTimeout.
	JournalTimeout time.Duration
	// JournalSince starts the reading of the journal at this time; zero
	// reads every boot it holds.
	JournalSince time.Time
	// NvidiaSMI is the nvidia-smi program. Empty looks it up in PATH; "-"
	// leaves GPU memory out.
	NvidiaSMI string
	// Mcelog is the mcelog program. Empty looks it up in PATH; "-" leaves
	// the counts of the mcelog daemon out.
	Mcelog string
	// Numactl, Numastat and Sar are the programs whose output the Memory
	// Explorer shows (numactl --hardware, numastat, sar -B). Empty looks
	// each up in PATH; "-" does not run it. A copy of a host's files runs
	// none of them.
	Numactl, Numastat, Sar string
	// Location is the time zone of log files whose times carry none
	// (syslog files). Nil means the zone of this process on a live host
	// and UTC otherwise.
	Location *time.Location
	// Note is free text stored with the scan.
	Note string
	// Preflight changes what the checks of "my preflight" ask the running
	// system (the process list, free space); tests set it.
	Preflight func(*preflight.Options)
	// NoSPD leaves the SPD of the memory modules out: no probe of the I2C
	// bus, no eeprom file read, no spd source.
	NoSPD bool
	// SPDFS replaces the file system the SPD reader of a live scan looks
	// at, and SPDDeadline its deadline; tests give a made host.
	SPDFS       dimmspd.FS
	SPDDeadline time.Duration
	// Extra are sources read after those of the host, into the same run:
	// what the BMC of the host answered, when the caller asked it. A scan
	// finds no such source by itself.
	Extra []adapter.Adapter
}

// DefaultJournalTimeout bounds how long a scan reads the journal.
const DefaultJournalTimeout = 2 * time.Minute

// RasdaemonPaths lists where the rasdaemon database is looked for, below
// the root: the path distributions use, and the path of a build from
// source with the default prefix.
var RasdaemonPaths = []string{
	"var/lib/rasdaemon/ras-mc_event.db",
	"usr/local/var/lib/rasdaemon/ras-mc_event.db",
}

// LogFiles lists the kernel log files that are looked for below the root,
// in order of preference: files that carry dates and span several boots
// first, then captures of the running boot. Only the first one found is
// read, because the same message read from two of them would count twice.
var LogFiles = []string{
	"var/log/kern.log",
	"var/log/messages",
	"var/log/syslog",
	"var/log/dmesg",
	"sos_commands/kernel/dmesg",
	"dmesg",
}

// DmidecodeFiles lists where the text output of dmidecode is looked for in
// a copy of a host's files that has no SMBIOS tables.
var DmidecodeFiles = []string{
	"sos_commands/hardware/dmidecode",
	"dmidecode",
	"dmidecode.txt",
}

func (o *Options) root() string {
	if o.Root == "" {
		return "/"
	}
	return o.Root
}

func (o *Options) path(rel string) string {
	return filepath.Join(o.root(), filepath.FromSlash(rel))
}

func exists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// treeTime returns when a copy of a host's files was made.
//
// A copy that holds /proc/stat and /proc/uptime says so itself: the boot
// time plus the uptime, both read at the moment of the copy. That holds
// however the copy travelled. Without them the answer is the newest
// modification time among the files a scan reads, which is right as long
// as the copy kept the times of its files: an archive unpacked without
// them, or a checkout of a repository, gives every file the time of today.
func treeTime(opts *Options) time.Time {
	if t := uptimeTime(opts); !t.IsZero() {
		return t
	}
	var newest time.Time
	see := func(path string) {
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() && fi.ModTime().After(newest) {
			newest = fi.ModTime()
		}
	}
	for _, rel := range []string{"proc/stat", "proc/sys/kernel/random/boot_id", smbios.TablesDir + "/DMI"} {
		see(opts.path(rel))
	}
	for _, list := range [][]string{RasdaemonPaths, LogFiles, DmidecodeFiles} {
		for _, rel := range list {
			see(opts.path(rel))
		}
	}
	// The counters: a few files per DIMM, a few dozen DIMMs.
	_ = filepath.WalkDir(opts.path(edacsysfs.Dir), func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			see(path)
		}
		return nil
	})
	return newest
}

// uptimeTime returns the boot time of a copy plus its uptime, or the zero
// time when the copy does not hold both.
func uptimeTime(opts *Options) time.Time {
	info, _ := hostinfo.NewRoot(opts.root(), hostinfo.Options{}).Read()
	if info.BootTime.IsZero() {
		return time.Time{}
	}
	b, err := os.ReadFile(opts.path("proc/uptime"))
	if err != nil {
		return time.Time{}
	}
	// "82.54 1234.56": seconds since boot, and idle time summed over the CPUs.
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return time.Time{}
	}
	up, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || up < 0 || up > 100*365*24*3600 {
		return time.Time{}
	}
	return info.BootTime.Add(time.Duration(up * float64(time.Second))).Truncate(time.Second)
}

// Sources returns the adapters of a scan, in the order they are read.
func Sources(ctx context.Context, opts Options) []adapter.Adapter {
	var out []adapter.Adapter
	for _, t := range sourceList(ctx, opts) {
		out = append(out, t.a)
	}
	return out
}

// HarvestSources returns the sources of a live scan in the two phases of
// "my harvest". Phase A is what the boot before left, which the next
// reset could take away: the host facts of this boot, the pstore files
// and their archive, the boot error region, the text logs of kdump, and
// the ring buffer of this boot, which holds its first lines (records left
// in the machine check banks, the lines the kernel printed of the boot
// error region). Phase B is the rest of the scan: rasdaemon, the journal,
// the counters, SMBIOS and the GPUs.
func HarvestSources(ctx context.Context, opts Options) (a, b []adapter.Adapter) {
	for _, t := range sourceList(ctx, opts) {
		if t.early {
			a = append(a, t.a)
		} else {
			b = append(b, t.a)
		}
	}
	return a, b
}

// tagged is a source with the phase of the harvest it belongs to.
type tagged struct {
	a     adapter.Adapter
	early bool
}

func sourceList(ctx context.Context, opts Options) []tagged {
	now := opts.Now
	if now.IsZero() && !opts.Live {
		now = treeTime(&opts)
	}
	hi := hostinfo.NewRoot(opts.root(), hostinfo.Options{Now: now})
	info, _ := hi.Read()
	out := []tagged{{hi, true}}
	add := func(early bool, as ...adapter.Adapter) {
		for _, a := range as {
			out = append(out, tagged{a, early})
		}
	}

	// rasdaemon: the first database that is there. One that is there and
	// closed to this user is for the adapter to report. When there is none,
	// the inventory says what that means: it is the usual state of a host
	// nobody has set up for this.
	db, there := "", false
	for _, rel := range RasdaemonPaths {
		p := opts.path(rel)
		_, err := os.Stat(p)
		if err == nil || !errors.Is(err, fs.ErrNotExist) {
			db, there = p, true
			if err == nil {
				break
			}
		}
	}
	if there {
		add(false, rasdaemon.New(db, rasdaemon.Options{TempDir: opts.TempDir}))
	} else {
		add(false, report{kind: rasdaemon.Kind, origin: opts.path(RasdaemonPaths[0]), status: model.SourceAbsent,
			detail: "no rasdaemon database: rasdaemon is not installed or has stored nothing. Error records then come from the kernel log and the counters only"})
	}

	for _, k := range kernelLog(ctx, &opts, info) {
		// The ring buffer of this boot holds its first lines.
		_, ring := k.(*kmsg)
		add(ring, k)
	}
	add(true, CrashLogs(&opts)...)
	add(true, bootErrors(&opts, now, info)...)

	add(false, edacsysfs.NewRoot(opts.root(), edacsysfs.Options{Now: now}))
	add(false, daemonCounts(&opts, now)...)

	sm := adapter.Adapter(smbios.NewRoot(opts.root(), smbios.Options{Now: now}))
	if !opts.Live && !tablesReadable(opts.path(smbios.TablesDir+"/DMI")) {
		for _, rel := range DmidecodeFiles {
			if p := opts.path(rel); exists(p) {
				// Tables that are there and hold no structure are listed;
				// the text of dmidecode stands in for them.
				if exists(opts.path(smbios.TablesDir + "/DMI")) {
					add(false, report{kind: smbios.Kind, origin: opts.path(smbios.TablesDir + "/DMI"), status: model.SourceError,
						detail: "the SMBIOS tables of the copy hold no structure that can be read; " + rel + " was read in their place"})
				}
				sm = smbios.NewDmidecodeFile(p, smbios.Options{Now: now})
				break
			}
		}
	}
	add(false, sm)
	add(false, moduleSPD(ctx, &opts, now, info, sm)...)
	add(false, gpuMemory(&opts)...)
	add(false, bmcLog(&opts)...)
	if opts.Live || hostmem.Holds(opts.root()) {
		add(false, hostmem.New(opts.root(), hostmem.Options{Live: opts.Live, Now: now, Numactl: opts.Numactl, Numastat: opts.Numastat, Sar: opts.Sar}))
	}
	return out
}

// moduleSPD picks the reader of the SPD of the memory modules: the eeprom
// files of the running host, or what a collection wrote (spd-probe.txt
// and spd/) in a copy of a host's files. A copy without them gets no
// entry, and NoSPD leaves the source out.
func moduleSPD(ctx context.Context, opts *Options, now time.Time, info hostinfo.Info, sm adapter.Adapter) []adapter.Adapter {
	if opts.NoSPD {
		return nil
	}
	if !opts.Live {
		if !dimmspd.Holds(opts.root()) {
			return nil
		}
		return []adapter.Adapter{dimmspd.NewCopy(opts.root(), now, memoryType(ctx, sm))}
	}
	fsys, ok := os.DirFS(opts.root()).(dimmspd.FS)
	if opts.SPDFS != nil {
		fsys, ok = opts.SPDFS, true
	}
	if !ok {
		return nil
	}
	lo := dimmspd.LiveOptions{Now: now, Privileged: opts.Privileged, Virtual: info.Virtual, AllowVirtual: dimmspd.DebugVirtual(), Release: info.Kernel,
		Deadline: opts.SPDDeadline}
	return []adapter.Adapter{dimmspd.NewLive(fsys, opts.root(), lo, memoryType(ctx, sm))}
}

// memoryType returns the memory type SMBIOS gives for the populated slots
// when all of them have the same one, else "". It reads the SMBIOS source
// once more into a sink that keeps nothing in the ledger.
func memoryType(ctx context.Context, sm adapter.Adapter) string {
	var c adapter.Collected
	adapter.Safely(ctx, sm, &c)
	t := ""
	for _, m := range c.Modules {
		if !m.Populated || m.DIMM.MemoryType == "" {
			continue
		}
		if t != "" && t != m.DIMM.MemoryType {
			return ""
		}
		t = m.DIMM.MemoryType
	}
	return t
}

// CrashPlaces are the places a host keeps the kernel log of a crashed boot
// in, below its root.
var CrashPlaces = []struct{ Kind, Dir string }{
	{crashlog.KindPstore, crashlog.StoreDir},
	{crashlog.KindPstore, crashlog.ArchiveDir},
	{crashlog.KindKdump, crashlog.KdumpDir},
}

// CrashLogs returns a source for each place of CrashPlaces that is there.
// A place that is not there gets no entry: most hosts keep no crash dump,
// and the inventory would say so on every scan. One that is closed to
// this user is reported as such.
//
// The files of the pstore file system carry the time of their record
// (fs/pstore/inode.c, pstore_mkfile), and systemd-pstore keeps that time
// when it moves them to its archive (seen with systemd 259 in the lab:
// docs/compatibility.md). The text logs of kdump carry the time they were
// written, after the crash.
func CrashLogs(opts *Options) []adapter.Adapter {
	var out []adapter.Adapter
	for _, c := range CrashPlaces {
		p := opts.path(c.Dir)
		fi, err := os.Stat(p)
		if errors.Is(err, fs.ErrNotExist) || (err == nil && !fi.IsDir()) {
			continue
		}
		out = append(out, crashlog.NewDir(c.Kind, p, crashlog.Options{ModTime: c.Kind == crashlog.KindPstore, TempDir: opts.TempDir}))
	}
	return out
}

// bootErrors picks the Boot Error Region of the firmware: on a host whose
// ACPI tables include BERT, the region the kernel shows at bert.Path. A
// host without the table gets no entry. The region is read as of the scan,
// which is in the boot that holds it; a copy of a host's files is read as
// of its own time.
func bootErrors(opts *Options, now time.Time, info hostinfo.Info) []adapter.Adapter {
	p := opts.path(bert.Path)
	if _, err := os.Lstat(p); err != nil {
		// drivers/acpi/sysfs.c makes the file for a BERT table only.
		if _, terr := os.Lstat(opts.path("sys/firmware/acpi/tables/BERT")); terr != nil || !opts.Live {
			return nil
		}
	}
	if now.IsZero() {
		now = time.Now()
	}
	return []adapter.Adapter{bert.New(p, bert.Options{ReadAt: now, BootID: info.BootID})}
}

// SELDir is where a copy of a host's files keeps the output of ipmitool, as
// sos saves it. SELListings are the endings of the file names of the event
// log listings there, in order of preference; sos puts the options of the
// command into the name ("ipmitool_sel_elist", "ipmitool_-I_usb_sel_elist").
// The listing with sensor names comes first: on most boards the sensor name
// is all that names the DIMM.
const SELDir = "sos_commands/ipmitool"

var SELListings = []string{"sel_elist", "sel_list_-v", "sel_list"}

// bmcLog picks the BMC event log of a copy of a host's files: the first
// saved listing that is found. One listing is read, because the same
// record from two of them would count twice. A tree without a listing gets
// no entry.
//
// A live host is not asked for its event log. Reading it through the BMC
// needs root and can take minutes; a listing saved with "ipmitool sel
// elist" is read by an import.
func bmcLog(opts *Options) []adapter.Adapter {
	if opts.Live {
		return nil
	}
	entries, err := os.ReadDir(opts.path(SELDir))
	if err != nil {
		return nil
	}
	for _, ending := range SELListings {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ending) {
				continue
			}
			return []adapter.Adapter{sel.NewFile(filepath.Join(opts.path(SELDir), e.Name()), sel.Options{Location: opts.Location})}
		}
	}
	return nil
}

// McelogClientFile is where a copy of a host's files keeps the answer of
// "mcelog --client".
const McelogClientFile = "mcelog-client.txt"

// daemonCounts picks the source of the error counts the mcelog daemon
// keeps. A live host that has the mcelog program is asked through its
// client, which only reads; a host without the program gets no entry,
// because most hosts run rasdaemon or nothing. A copy of a host's files
// is read from McelogClientFile when it has one.
func daemonCounts(opts *Options, now time.Time) []adapter.Adapter {
	if !opts.Live {
		// A collection of my collect host keeps the answer of the client.
		if p := opts.path(McelogClientFile); exists(p) {
			return []adapter.Adapter{mcelog.NewFile(p, mcelog.Options{Now: now})}
		}
		return nil
	}
	prog := opts.Mcelog
	switch prog {
	case "-":
		return nil
	case "":
		p, err := exec.LookPath("mcelog")
		if err != nil {
			return nil
		}
		prog = p
	}
	return []adapter.Adapter{mcelog.NewCommand(prog, 0, mcelog.Options{Now: now})}
}

// NvidiaSMIFiles lists where saved output of "nvidia-smi -q" is looked for
// in a copy of a host's files.
var NvidiaSMIFiles = []string{
	"sos_commands/nvidia/nvidia-smi_-q",
}

// gpuMemory picks the source of GPU memory state. A live host is asked
// through nvidia-smi, which only reads; a host without the program has no
// NVIDIA driver and gets no entry, because most hosts have no GPU. A copy
// of a host's files is read where the saved output is found.
func gpuMemory(opts *Options) []adapter.Adapter {
	nopts := nvidia.Options{Now: opts.Now, Location: opts.Location}
	if opts.Live {
		if nopts.Location == nil {
			nopts.Location = time.Local
		}
		prog := opts.NvidiaSMI
		switch prog {
		case "-":
			return nil
		case "":
			p, err := exec.LookPath("nvidia-smi")
			if err != nil {
				return nil
			}
			prog = p
		}
		return []adapter.Adapter{nvidia.NewCommand(prog, 0, nopts)}
	}
	for _, rel := range NvidiaSMIFiles {
		if p := opts.path(rel); exists(p) {
			return []adapter.Adapter{nvidia.NewFile(p, nopts)}
		}
	}
	return nil
}

// kernelLog picks the kernel log source of a scan. What was tried and
// could not be read stays in the list as a source of its own, so that the
// inventory names it.
func kernelLog(ctx context.Context, opts *Options, info hostinfo.Info) []adapter.Adapter {
	loc := opts.Location
	if loc == nil {
		loc = time.UTC
		if opts.Live {
			loc = time.Local
		}
	}
	kopts := klog.Options{BootTime: info.BootTime, BootID: info.BootID, Location: loc, TempDir: opts.TempDir}
	var out []adapter.Adapter

	if opts.Live {
		// The journal and the ring buffer are read together. The journal
		// holds the boots before this one; the ring buffer holds what the
		// kernel said since this boot, also after the journal has dropped
		// it to make room. A line that is in both has the same stamp and
		// the same text in both and is stored once.
		journalRead := false
		if prog := journalctl(opts); prog != "" {
			j := &journal{prog: prog, timeout: opts.JournalTimeout, since: opts.JournalSince, opts: kopts}
			switch status, detail := j.probe(ctx); status {
			case model.SourceOK:
				out = append(out, j)
				journalRead = true
			case model.SourceUnreadable, model.SourceError:
				out = append(out, report{kind: klog.Kind, origin: j.Origin(), status: status, detail: detail})
			}
		}
		k := &kmsg{path: opts.path("dev/kmsg"), opts: kopts}
		switch status, detail := k.probe(); status {
		case model.SourceOK:
			return append(out, k)
		case model.SourceUnreadable, model.SourceError:
			// With the journal read, a ring buffer that is closed takes
			// little away and is no line of its own.
			if !journalRead {
				out = append(out, report{kind: klog.Kind, origin: k.Origin(), status: status, detail: detail})
			}
		}
		if journalRead {
			return out
		}
	}

	var denied []adapter.Adapter
	var notLog []adapter.Adapter
	for _, rel := range LogFiles {
		p := opts.path(rel)
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() {
			continue
		}
		f, err := os.Open(p)
		if err != nil {
			// A log file that is there and closed to this user is worth a
			// line: it is what root would add.
			if status, detail := adapter.StatusForError(err); status == model.SourceUnreadable {
				denied = append(denied, report{kind: klog.Kind, origin: p, status: status, detail: detail})
			}
			continue
		}
		head := make([]byte, 64<<10)
		n, _ := io.ReadFull(f, head)
		f.Close()
		// A file that holds no line of a kernel log does not stand in for
		// the next one: it is listed, and the next one is read.
		if text := bytes.TrimLeft(head[:n], " \t\r\n\xef\xbb\xbf"); len(text) > 0 && !looksLikeKernelLog(text) {
			notLog = append(notLog, report{kind: klog.Kind, origin: p, status: model.SourceError,
				detail: "holds no line of a kernel log; the next kernel log file was read"})
			continue
		}
		return append(append(out, klog.NewFile(p, kopts)), notLog...)
	}
	out = append(append(out, notLog...), denied...)
	if len(out) == 0 {
		// Nothing was found at all: one entry says so.
		where := opts.path(LogFiles[0])
		detail := "no kernel log file found (looked for " + filepath.ToSlash(filepath.Join("var", "log")) + "/{kern.log,messages,syslog,dmesg})"
		if opts.Live {
			where = "kernel log"
			detail = "no journal, no /dev/kmsg and no kernel log file under /var/log"
		}
		out = append(out, report{kind: klog.Kind, origin: where, status: model.SourceAbsent, detail: detail})
	}
	return out
}

// journalctl returns the journalctl program to run, or "".
func journalctl(opts *Options) string {
	switch opts.Journalctl {
	case "-":
		return ""
	case "":
		p, err := exec.LookPath("journalctl")
		if err != nil {
			return ""
		}
		return p
	}
	return opts.Journalctl
}

// Run scans the host into the ledger and returns what each source gave.
func Run(ctx context.Context, s *store.Store, opts Options) (*ingest.Result, error) {
	kind := model.ScanImport
	if opts.Live {
		kind = model.ScanLive
	}
	if opts.Now.IsZero() && !opts.Live {
		opts.Now = treeTime(&opts)
	}
	res, err := ingest.Run(ctx, s, append(Sources(ctx, opts), opts.Extra...), ingest.Options{
		Host: opts.Host, Kind: kind, Now: opts.Now, Privileged: opts.Privileged, Note: opts.Note,
	})
	if err != nil || !opts.Live {
		return res, err
	}
	// What a reset would lose on this host, as "my preflight" says it: a
	// bundle of the scan tells its reader why crash records may be
	// missing. Only a live host is asked; a copy of a host's files lacks
	// what several checks read.
	if err := s.Update(ctx, func(w *store.Writer) error {
		return w.PutCaptureChecks(res.Scan.ID, res.Host, CaptureChecks(opts.Root, opts.Preflight))
	}); err != nil {
		return nil, fmt.Errorf("scan: capture checks: %w", err)
	}
	return res, nil
}

// CaptureChecks makes the checks of "my preflight" on the running host
// below root and returns them as ledger rows.
func CaptureChecks(root string, probe func(*preflight.Options)) []model.CaptureCheck {
	po := preflight.Options{Root: root, Live: true}
	if probe != nil {
		probe(&po)
	}
	rep := preflight.Run(po)
	out := make([]model.CaptureCheck, 0, len(rep.Checks))
	for _, c := range rep.Checks {
		out = append(out, model.CaptureCheck{Name: c.Name, State: string(c.State), Detail: c.Detail, Evidence: c.Evidence})
	}
	return out
}

// report is a source that was looked for and could not be read. It adds
// nothing to the ledger but its line in the source inventory.
type report struct {
	kind, origin string
	status       model.SourceStatus
	detail       string
}

func (r report) Kind() string   { return r.kind }
func (r report) Origin() string { return r.origin }
func (r report) Collect(context.Context, adapter.Sink) adapter.Report {
	return adapter.Report{Kind: r.kind, Origin: r.origin, Status: r.status, Detail: r.detail}
}

// tablesReadable reports whether the SMBIOS tables of a copy are there and
// hold at least one structure.
func tablesReadable(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() || fi.Size() > 16<<20 {
		return false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	structs := adapter.Guard(func() []smbios.Structure { st, _ := smbios.ParseTable(b); return st })
	return len(structs) > 0
}
