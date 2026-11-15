// Package hostinfo reads what a running Linux host says about itself and
// its current boot: host name, kernel release, boot ID, boot time, kernel
// command line and the system, board and BIOS identification strings.
//
// All of it comes from world-readable files under /proc and /sys.
package hostinfo

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/decode/virt"
	"github.com/agenticode/my/internal/model"
)

// Kind is the adapter name in the source inventory.
const Kind = "hostinfo"

// Info is what the host reports about itself.
type Info struct {
	Hostname string
	Arch     string
	Kernel   string // kernel release
	Cmdline  string
	// BootID is the kernel boot ID without dashes, as the journal writes it.
	BootID string
	// BootTime is the boot time from /proc/stat, exact to the second.
	BootTime time.Time

	SystemVendor  string
	SystemProduct string
	Board         string
	BIOS          string // vendor, version and date
	// Virtual names the hypervisor the host runs under, is virt.Unnamed
	// where the CPU reports one that the DMI strings do not name, and is
	// empty for a host that is not known to be a virtual machine.
	Virtual string
}

// Options control a reading.
type Options struct {
	// Now is the time of the reading; zero means the current time.
	Now time.Time
}

// Adapter reads the host facts of the system rooted at fsys.
type Adapter struct {
	fsys   fs.FS
	origin string
	opts   Options
}

// New returns an adapter for the system whose root is fsys. origin names it
// in reports.
func New(fsys fs.FS, origin string, opts Options) *Adapter {
	return &Adapter{fsys: fsys, origin: origin, opts: opts}
}

// NewRoot returns an adapter for the system whose file system root is root.
func NewRoot(root string, opts Options) *Adapter {
	return New(os.DirFS(root), root, opts)
}

func (a *Adapter) Kind() string   { return Kind }
func (a *Adapter) Origin() string { return a.origin }

func (a *Adapter) text(name string) string {
	b, err := fs.ReadFile(a.fsys, name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimRight(string(b), "\x00"))
}

// Read collects the host facts. missing lists the pieces that could not be
// read.
func (a *Adapter) Read() (info Info, missing []string) {
	info.Hostname = a.text("proc/sys/kernel/hostname")
	if info.Hostname == "" {
		info.Hostname = a.text("etc/hostname")
	}
	if info.Hostname == "" {
		missing = append(missing, "host name")
	}

	info.Kernel = a.text("proc/sys/kernel/osrelease")
	if info.Kernel == "" {
		// "Linux version 5.14.0-570.el9.x86_64 (builder@host) ..."
		if f := strings.Fields(a.text("proc/version")); len(f) >= 3 && f[0] == "Linux" && f[1] == "version" {
			info.Kernel = f[2]
		}
	}
	if info.Kernel == "" {
		missing = append(missing, "kernel release")
	}

	info.Arch = a.text("proc/sys/kernel/arch")
	if info.Arch == "" {
		// Distribution kernels end their release with the architecture.
		for _, arch := range []string{"x86_64", "aarch64", "ppc64le", "s390x", "riscv64", "loongarch64", "arm64", "amd64"} {
			if strings.HasSuffix(info.Kernel, arch) || strings.Contains(info.Kernel, "."+arch+"+") || strings.Contains(info.Kernel, "-"+arch) {
				info.Arch = map[string]string{"arm64": "aarch64", "amd64": "x86_64"}[arch]
				if info.Arch == "" {
					info.Arch = arch
				}
				break
			}
		}
	}

	info.Cmdline = a.text("proc/cmdline")

	if id := strings.ReplaceAll(a.text("proc/sys/kernel/random/boot_id"), "-", ""); len(id) == 32 && isHex(id) {
		info.BootID = strings.ToLower(id)
	} else {
		missing = append(missing, "boot ID")
	}

	if f, err := a.fsys.Open("proc/stat"); err == nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64<<10), 4<<20)
		for sc.Scan() {
			if rest, ok := strings.CutPrefix(sc.Text(), "btime "); ok {
				if unix, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64); err == nil && unix > 0 {
					info.BootTime = time.Unix(unix, 0).UTC()
				}
				break
			}
		}
		f.Close()
	}
	if info.BootTime.IsZero() {
		missing = append(missing, "boot time")
	}

	dmi := func(name string) string {
		v := a.text("sys/class/dmi/id/" + name)
		switch strings.ToLower(v) {
		case "to be filled by o.e.m.", "default string", "not specified", "system manufacturer", "system product name", "none":
			return ""
		}
		return v
	}
	info.SystemVendor, info.SystemProduct = dmi("sys_vendor"), dmi("product_name")
	info.Board = strings.TrimSpace(dmi("board_vendor") + " " + dmi("board_name"))
	info.BIOS = strings.Join(strings.Fields(dmi("bios_vendor")+" "+dmi("bios_version")+" "+dmi("bios_date")), " ")

	// A virtual machine: named by the strings its hypervisor fills in, by
	// the Xen directory of sysfs, or only known through the flag an x86
	// CPU sets under a hypervisor.
	switch {
	case virt.FromDMI(info.SystemVendor, info.SystemProduct) != "":
		info.Virtual = virt.FromDMI(info.SystemVendor, info.SystemProduct)
	case strings.EqualFold(a.text("sys/hypervisor/type"), "xen"):
		info.Virtual = "Xen"
	case virt.FromCPUFlags(a.head("proc/cpuinfo", 64<<10)):
		info.Virtual = virt.Unnamed
	}
	return info, missing
}

// head returns the first bytes of a file: enough of /proc/cpuinfo for the
// flags of its first CPU, however many CPUs follow.
func (a *Adapter) head(name string, limit int) string {
	f, err := a.fsys.Open(name)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, limit)
	n, _ := io.ReadFull(f, buf)
	return string(buf[:n])
}

func isHex(s string) bool {
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

// HostHint returns the host name of the system, or "" when it cannot be read.
func (a *Adapter) HostHint() string {
	info, _ := a.Read()
	return info.Hostname
}

// Collect sends the host facts and the current boot to sink.
func (a *Adapter) Collect(ctx context.Context, sink adapter.Sink) adapter.Report {
	rep := adapter.Report{Kind: Kind, Origin: a.origin}
	if err := ctx.Err(); err != nil {
		rep.Status, rep.Detail = model.SourceError, err.Error()
		return rep
	}
	if _, err := fs.Stat(a.fsys, "proc"); err != nil {
		rep.Status, rep.Detail = adapter.StatusForError(err)
		if rep.Status == model.SourceAbsent {
			rep.Detail = "no /proc; not a running Linux system"
		}
		return rep
	}
	info, missing := a.Read()
	rep.HostHint = info.Hostname
	now := a.opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	rep.Records = 1
	host := &model.Host{Arch: info.Arch, SystemVendor: info.SystemVendor, SystemProduct: info.SystemProduct, Board: info.Board,
		Virtual: info.Virtual, FirstSeen: now.UTC(), LastSeen: now.UTC()}
	if err := sink.Host(host); err != nil {
		rep.Status, rep.Detail, rep.SinkErr = model.SourceError, "ledger rejected a record: "+err.Error(), err
		return rep
	}
	if !info.BootTime.IsZero() {
		rep.Records++
		b := &model.Boot{
			BootID: info.BootID, Derived: info.BootID == "", StartedAt: info.BootTime, StartQuality: model.TimeExact,
			LastSeen: now.UTC(), Kernel: info.Kernel, BIOS: info.BIOS, Cmdline: info.Cmdline, Source: Kind, RawRef: a.origin,
		}
		if err := sink.Boot(b); err != nil {
			rep.Status, rep.Detail, rep.SinkErr = model.SourceError, "ledger rejected a record: "+err.Error(), err
			return rep
		}
	}
	rep.Status = model.SourceOK
	if len(missing) > 0 {
		rep.Status = model.SourcePartial
		rep.Skipped = len(missing)
		rep.Notes = append(rep.Notes, fmt.Sprintf("could not read: %s", strings.Join(missing, ", ")))
	}
	return rep
}
