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
//
// The same code reads a copy of a host's files (a directory with var, sys
// and proc below it): only files are read then, no commands are run.
package scan

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/edacsysfs"
	"github.com/agenticode/my/internal/adapter/hostinfo"
	"github.com/agenticode/my/internal/adapter/klog"
	"github.com/agenticode/my/internal/adapter/rasdaemon"
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
	// Now is the time of the scan; zero means the current time.
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

// Sources returns the adapters of a scan, in the order they are read.
func Sources(ctx context.Context, opts Options) []adapter.Adapter {
	now := opts.Now
	hi := hostinfo.NewRoot(opts.root(), hostinfo.Options{Now: now})
	info, _ := hi.Read()
	out := []adapter.Adapter{hi}

	// rasdaemon: the first database that exists, or the usual path, so
	// that the inventory says it is not there.
	db := opts.path(RasdaemonPaths[0])
	for _, rel := range RasdaemonPaths {
		if p := opts.path(rel); exists(p) {
			db = p
			break
		}
	}
	out = append(out, rasdaemon.New(db, rasdaemon.Options{TempDir: opts.TempDir}))

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
	return out
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
