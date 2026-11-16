// Package preflight says what a reset right after a fatal memory error
// would leave on a host, and which settings would change that.
//
// A fatal uncorrected memory error panics the kernel. User space does not
// run again, so the journal and rasdaemon write nothing more, and on x86
// the kernel restarts the host by itself 30 seconds later. What survives
// depends on settings: a persistent store for the panic text (pstore), a
// crash kernel (kdump), whether the next boot logs the machine check
// records left in the banks, a console that leaves the host. Each check
// reads the files that tell one of these and says whether the records
// would be kept, would be lost, are not offered by the platform, or
// cannot be read as this user.
//
// The package only reads. It opens no file for writing, runs no program
// and changes no setting. The fixes it names are text for a person.
package preflight

import (
	"fmt"
	"strings"
	"time"

	"github.com/agenticode/my/internal/adapter/hostinfo"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
)

// State is the outcome of a check.
type State string

const (
	// Kept: the records would survive a reset.
	Kept State = "kept"
	// Lost: they would not, and a setting can change that.
	Lost State = "lost"
	// None: the platform does not offer this way of keeping records.
	None State = "none"
	// Unknown: the files that tell cannot be read as this user, or are
	// not in the copy of the host's files.
	Unknown State = "unknown"
	// Info: a fact that bears on the others and is neither kept nor lost.
	Info State = "info"
)

// Names lists the checks in the order they are reported.
var Names = []string{
	"journal", "pstore-backend", "pstore-archive", "pstore-vs-kdump", "kdump",
	"mce-bootlog", "mce-print", "reset-kind", "bert", "erst-mce", "console",
	"rasdaemon", "harvest",
}

// Check is the outcome of one check.
type Check struct {
	Name  string `json:"name"`
	State State  `json:"state"`
	// Detail says in one sentence what was found.
	Detail model.Statement `json:"detail"`
	// Evidence lists what was read: "path: value".
	Evidence []string `json:"evidence,omitzero"`
	// Live says that the check reads the running host: in a copy of a
	// host's files it can only say what the copy holds.
	Live bool `json:"needs_live,omitzero"`
	// Fix is what a person can do about a lost check, or a candidate for
	// an info check. Nil when there is nothing to do.
	Fix *Fix `json:"fix,omitzero"`
}

// Fix is a change a person can make. MemorY makes none of them.
type Fix struct {
	// What says what the change does.
	What model.Statement `json:"what"`
	// Param is the kernel parameter the change adds or removes.
	Param string `json:"param,omitzero"`
	// Remove says that Param is taken off the command line.
	Remove bool `json:"remove,omitzero"`
	// Commands are shell commands for this distribution, in order. Empty
	// when the distribution is not known or there is no command.
	Commands []string `json:"commands,omitzero"`
	// Undo are the commands that take the change back.
	Undo []string `json:"undo,omitzero"`
	// Cost says what the change costs.
	Cost model.Statement `json:"cost"`
	// Source names the public document the cost is taken from.
	Source string `json:"source"`
	// Reboot says that the change takes effect at the next boot.
	Reboot bool `json:"reboot,omitzero"`
}

// Report is the outcome of all checks.
type Report struct {
	Host   string    `json:"host"`
	Kernel string    `json:"kernel,omitzero"`
	Arch   string    `json:"arch,omitzero"`
	CPU    string    `json:"cpu,omitzero"`
	Booted time.Time `json:"booted,omitzero"`
	// OS is the name of the distribution from /etc/os-release, and Family
	// the commands the fixes use: "grubby", "update-grub" or "".
	OS     string `json:"os,omitzero"`
	Family string `json:"family,omitzero"`
	// Root is the copy of a host's files that was read; empty for the
	// running host.
	Root   string  `json:"root,omitzero"`
	Checks []Check `json:"checks"`
	// Lost counts the checks in state Lost.
	Lost int `json:"lost"`
}

// Options describe a run.
type Options struct {
	// Root is the file system root of the host: "/" (the default) for the
	// running host, or the top of a copy of a host's files.
	Root string
	// Live says that Root is the running host.
	Live bool
	// Ledger is the kept ledger of "my harvest"; empty means
	// DefaultLedger.
	Ledger string
	// Processes returns the command names of the running processes; nil
	// reads /proc on a live host. Tests set it.
	Processes func() ([]string, error)
	// FreeBytes returns the free space of the file system of a path; nil
	// asks the system on a live host. Tests set it.
	FreeBytes func(path string) (uint64, error)
}

// DefaultLedger is where "my harvest" keeps its ledger.
const DefaultLedger = "/var/lib/my/ledger.db"

// DefaultLedgerDoc is DefaultLedger for texts.
const DefaultLedgerDoc = DefaultLedger

// MinFree is the free space below which "my harvest" stores only what
// the boot before left. It is a chosen floor; nothing was measured.
const MinFree = 32 << 20

// HarvestUnit is the name of the unit "my harvest install" writes.
const HarvestUnit = "my-harvest.service"

// UnitMarker is the first line of the unit files "my harvest install"
// writes. A file of that name without it belongs to someone else.
const UnitMarker = `# Written by "my harvest install"`

// Run makes the checks.
func Run(opts Options) *Report {
	if opts.Root == "" {
		opts.Root = "/"
	}
	if opts.Ledger == "" {
		opts.Ledger = DefaultLedger
	}
	h := &host{root: opts.Root, live: opts.Live, procs: opts.Processes, freeBytes: opts.FreeBytes}
	if h.procs == nil && opts.Live {
		h.procs = h.processNames
	}
	if h.freeBytes == nil && opts.Live {
		h.freeBytes = freeBytes
	}
	info, _ := hostinfo.NewRoot(opts.Root, hostinfo.Options{}).Read()
	r := &run{h: h, opts: opts, info: info}
	r.cmd = parseCmdline(info.Cmdline)
	cpuText, _ := r.head("proc/cpuinfo", 64<<10)
	r.cpu = parseCPUInfo(cpuText)
	r.cfg, r.cfgFile = h.kernelConfig(info.Kernel)
	r.os = h.osRelease()
	switch {
	case r.os.is("fedora", "rhel", "centos"):
		r.family = familyGrubby
	case r.os.is("debian", "ubuntu"):
		r.family = familyDebian
	}

	rep := &Report{Host: info.Hostname, Kernel: info.Kernel, Arch: info.Arch, Booted: info.BootTime, OS: r.os.pretty, Family: string(r.family)}
	if !opts.Live {
		rep.Root = opts.Root
	}
	if r.cpu.known {
		rep.CPU = fmt.Sprintf("%s family %d model %d", vendorName(r.cpu.vendor), r.cpu.family, r.cpu.model)
	}
	for _, c := range []func() Check{
		r.journal, r.pstoreBackend, r.pstoreArchive, r.pstoreVsKdump, r.kdump,
		r.mceBootlog, r.mcePrint, r.resetKind, r.bert, r.erstMCE, r.console,
		r.rasdaemon, r.harvest,
	} {
		ck := c()
		if ck.State == Lost {
			rep.Lost++
		}
		rep.Checks = append(rep.Checks, ck)
	}
	return rep
}

// vendorName turns the vendor string of the CPU into a name.
func vendorName(v string) string {
	switch v {
	case "GenuineIntel":
		return "Intel"
	case "AuthenticAMD":
		return "AMD"
	case "HygonGenuine":
		return "Hygon"
	case "CentaurHauls", "  Shanghai  ":
		return "Zhaoxin"
	}
	return strings.TrimSpace(v)
}

type family string

const (
	familyUnknown family = ""
	familyGrubby  family = "grubby"
	familyDebian  family = "update-grub"
)

// run holds what the checks share.
type run struct {
	h       *host
	opts    Options
	info    hostinfo.Info
	cmd     cmdline
	cpu     cpu
	cfg     map[string]string
	cfgFile string
	os      osRelease
	family  family

	// backend is the registered pstore backend, as the pstore-backend
	// check found it.
	backend     string
	crashLoaded int // -1 unknown, 0 no, 1 yes
	mcelogd     bool
}

// x86 reports whether the host is an x86 machine: machine check banks and
// their boot logging are an x86 feature.
func (r *run) x86() bool {
	switch r.info.Arch {
	case "x86_64", "i386", "i486", "i586", "i686", "x86":
		return true
	case "":
		return r.cpu.known
	}
	return false
}

// head reads the start of a file that can be larger than a page:
// /proc/cpuinfo of a host with many CPUs.
func (r *run) head(rel string, n int64) (string, error) { return r.h.head(rel, n) }

// statement builds a statement of the catalog with its English text.
func statement(code string, kv ...any) model.Statement {
	var args map[string]any
	if len(kv) > 0 {
		args = make(map[string]any, len(kv)/2)
		for i := 0; i+1 < len(kv); i += 2 {
			args[kv[i].(string)] = kv[i+1]
		}
	}
	return model.Statement{Code: code, Args: args, Text: Messages.Text(i18n.EN, code, args)}
}

// Text renders a statement in a language.
func Text(lang i18n.Lang, s model.Statement) string {
	if !Messages.Has(s.Code) {
		return s.Text
	}
	return Messages.Text(lang, s.Code, s.Args)
}

// evidence notes what a file held: its value, "not there", or "not
// readable as this user".
func (r *run) evidence(rel, value string, err error) string {
	switch {
	case err == nil:
		if len(value) > 120 {
			value = value[:117] + "..."
		}
		return "/" + rel + ": " + strings.ReplaceAll(value, "\n", " ")
	case missing(err):
		return "/" + rel + ": not there"
	case denied(err):
		return "/" + rel + ": not readable as this user"
	}
	return "/" + rel + ": " + err.Error()
}

// param returns the fix that adds a kernel parameter, with the commands of
// the distribution. A parameter takes effect at the next boot.
func (r *run) param(name, param string, what, cost model.Statement, source string) *Fix {
	f := &Fix{What: what, Param: param, Cost: cost, Source: source, Reboot: true}
	switch r.family {
	case familyGrubby:
		// grubby(8): --args adds to and --remove-args takes from the
		// command line of the kernels named by --update-kernel.
		f.Commands = []string{fmt.Sprintf("grubby --update-kernel=ALL --args=%q", param)}
		f.Undo = []string{fmt.Sprintf("grubby --update-kernel=ALL --remove-args=%q", param)}
	case familyDebian:
		// update-grub reads /etc/default/grub.d/*.cfg after
		// /etc/default/grub; Debian's kdump-tools package sets its
		// crashkernel= the same way (debian/rules installs
		// debian/kdump-tools.grub.default as
		// /etc/default/grub.d/kdump-tools.cfg).
		file := "/etc/default/grub.d/90-my-" + name + ".cfg"
		f.Commands = []string{
			"mkdir -p /etc/default/grub.d",
			fmt.Sprintf(`echo 'GRUB_CMDLINE_LINUX_DEFAULT="$GRUB_CMDLINE_LINUX_DEFAULT %s"' > %s`, param, file),
			"update-grub",
		}
		f.Undo = []string{"rm " + file, "update-grub"}
	}
	return f
}

// unparam returns the fix that takes a kernel parameter off the command
// line.
func (r *run) unparam(param string, what, cost model.Statement, source string) *Fix {
	f := &Fix{What: what, Param: param, Remove: true, Cost: cost, Source: source, Reboot: true}
	if r.family == familyGrubby {
		f.Commands = []string{fmt.Sprintf("grubby --update-kernel=ALL --remove-args=%q", param)}
		f.Undo = []string{fmt.Sprintf("grubby --update-kernel=ALL --args=%q", param)}
	}
	// Elsewhere the parameter sits in a file of the boot loader that a
	// person has to edit; What says so.
	return f
}

// notInCopy is the outcome of a check whose file a copy of a host's files
// does not hold.
// copyLacks reports whether the host is a copy of its files that holds
// none of the directories: a check that decides by them cannot tell what
// the host has.
func (r *run) copyLacks(dirs ...string) bool {
	if r.opts.Live {
		return false
	}
	for _, d := range dirs {
		if r.h.isDir(d) {
			return false
		}
	}
	return true
}

func (r *run) notInCopy(name, rel string) Check {
	return Check{Name: name, State: Unknown, Live: true, Detail: statement("pf.copy.missing", "file", "/"+rel),
		Evidence: []string{"/" + rel + ": not there"}}
}
