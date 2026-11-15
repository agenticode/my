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
//	kernel log   the systemd journal of every boot on record; where that
//	             cannot be read, the kernel ring buffer of the running
//	             boot (/dev/kmsg); where that cannot be read either, the
//	             first log file found under /var/log
//	EDAC         the per-DIMM counters under /sys/devices/system/edac
//	SMBIOS       the memory device tables under /sys/firmware/dmi
//	GPU memory   on a live host, what "nvidia-smi -q -x" prints
//	BMC log      in a copy of a host's files, a saved "ipmitool sel" listing
//
// The same code reads a copy of a host's files (a directory with var, sys
// and proc below it): only files are read then, no commands are run.
package scan

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/edacsysfs"
	"github.com/agenticode/my/internal/adapter/hostinfo"
	"github.com/agenticode/my/internal/adapter/klog"
	"github.com/agenticode/my/internal/adapter/nvidia"
	"github.com/agenticode/my/internal/adapter/rasdaemon"
	"github.com/agenticode/my/internal/adapter/sel"
	"github.com/agenticode/my/internal/adapter/smbios"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
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
	// made, taken from the newest of the files that are read: counters and
	// seconds since boot in the copy count back from that moment.
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
	// NvidiaSMI is the nvidia-smi program. Empty looks it up in PATH; "-"
	// leaves GPU memory out.
	NvidiaSMI string
	// Location is the time zone of log files whose times carry none
	// (syslog files). Nil means the zone of this process on a live host
	// and UTC otherwise.
	Location *time.Location
	// Note is free text stored with the scan.
	Note string
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

// treeTime returns when a copy of a host's files was made: the newest
// modification time among the files a scan reads. A copy cannot be older
// than its newest file.
func treeTime(opts *Options) time.Time {
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

// Sources returns the adapters of a scan, in the order they are read.
func Sources(ctx context.Context, opts Options) []adapter.Adapter {
	now := opts.Now
	if now.IsZero() && !opts.Live {
		now = treeTime(&opts)
	}
	hi := hostinfo.NewRoot(opts.root(), hostinfo.Options{Now: now})
	info, _ := hi.Read()
	out := []adapter.Adapter{hi}

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
		out = append(out, rasdaemon.New(db, rasdaemon.Options{TempDir: opts.TempDir}))
	} else {
		out = append(out, report{kind: rasdaemon.Kind, origin: opts.path(RasdaemonPaths[0]), status: model.SourceAbsent,
			detail: "no rasdaemon database: rasdaemon is not installed or has stored nothing. Error records then come from the kernel log and the counters only"})
	}

	out = append(out, kernelLog(ctx, &opts, info)...)

	out = append(out, edacsysfs.NewRoot(opts.root(), edacsysfs.Options{Now: now}))

	sm := adapter.Adapter(smbios.NewRoot(opts.root(), smbios.Options{Now: now}))
	if !opts.Live && !exists(opts.path(smbios.TablesDir+"/DMI")) {
		for _, rel := range DmidecodeFiles {
			if p := opts.path(rel); exists(p) {
				sm = smbios.NewDmidecodeFile(p, smbios.Options{Now: now})
				break
			}
		}
	}
	out = append(out, sm)
	out = append(out, gpuMemory(&opts)...)
	out = append(out, bmcLog(&opts)...)
	return out
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
	kopts := klog.Options{BootTime: info.BootTime, BootID: info.BootID, Location: loc}
	var out []adapter.Adapter

	if opts.Live {
		if prog := journalctl(opts); prog != "" {
			j := &journal{prog: prog, timeout: opts.JournalTimeout, opts: kopts}
			switch status, detail := j.probe(ctx); status {
			case model.SourceOK:
				return []adapter.Adapter{j}
			case model.SourceUnreadable, model.SourceError:
				out = append(out, report{kind: klog.Kind, origin: j.Origin(), status: status, detail: detail})
			}
		}
		k := &kmsg{path: opts.path("dev/kmsg"), opts: kopts}
		switch status, detail := k.probe(); status {
		case model.SourceOK:
			return append(out, k)
		case model.SourceUnreadable, model.SourceError:
			out = append(out, report{kind: klog.Kind, origin: k.Origin(), status: status, detail: detail})
		}
	}

	var denied []adapter.Adapter
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
		f.Close()
		return append(out, klog.NewFile(p, kopts))
	}
	out = append(out, denied...)
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
	return ingest.Run(ctx, s, Sources(ctx, opts), ingest.Options{
		Host: opts.Host, Kind: kind, Now: opts.Now, Privileged: opts.Privileged, Note: opts.Note,
	})
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
