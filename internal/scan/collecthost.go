package scan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agenticode/my/internal/adapter/dimmspd"
	"github.com/agenticode/my/internal/adapter/hostinfo"
	"github.com/agenticode/my/internal/adapter/mcelog"
	"github.com/agenticode/my/internal/buildinfo"
	"github.com/agenticode/my/internal/decode/spd"
)

// A collection of "my collect host" is a copy of the files of the running
// host that a scan reads (--root, my import): host facts, the kernel ring
// buffer as dmesg text, the EDAC tree, the rasdaemon database, the SMBIOS
// tables (or dmidecode text), the memory readings and the output of the
// read-only programs the Memory Explorer runs, and collect.json, which
// lists every step. It only reads the host.

// CollectOptions say what to collect and from where.
type CollectOptions struct {
	// Root is the root of the host ("/"); tests give a made tree.
	Root string
	// Out is the directory the collection is written to; it must be new
	// or empty.
	Out string
	// Sudo runs dmidecode -t memory and lshw -c memory -c processor as
	// "sudo -n COMMAND", and nothing else.
	Sudo bool
	// Deadline bounds the whole collection; zero is 2 minutes.
	Deadline time.Duration
	// Programs replaces programs by name (dmidecode, lshw, numactl,
	// numastat, sar, mcelog, sudo); a missing name is looked up in PATH,
	// "-" is not run.
	Programs map[string]string
	// Kmsg reads the kernel ring buffer; nil reads Root/dev/kmsg.
	Kmsg func() ([]byte, error)
	// Now is the time the collection says it was made.
	Now time.Time
	// Privileged says that the collection runs as root: the SPD probe then
	// reads the SPD write disable bit of an Intel SMBus controller.
	Privileged bool
	// NoSPD leaves the SPD of the memory modules out.
	NoSPD bool
}

// CollectEntry is one step of a collection.
type CollectEntry struct {
	Group string `json:"group"`
	// What is the file written, below the collection.
	What string `json:"what"`
	// From is the file or the command it came from.
	From string `json:"from"`
	// State is ok, absent (not there, not installed), denied, timeout or
	// failed.
	State  string `json:"state"`
	Bytes  int64  `json:"bytes,omitzero"`
	Millis int64  `json:"ms,omitzero"`
	Note   string `json:"note,omitzero"`
}

// States of a step.
const (
	StepOK      = "ok"
	StepAbsent  = "absent"
	StepDenied  = "denied"
	StepTimeout = "timeout"
	StepFailed  = "failed"
)

// Groups of the steps, in the order they are made.
const (
	GroupHost     = "host"
	GroupErrors   = "errors"
	GroupSMBIOS   = "smbios"
	GroupSPD      = "spd"
	GroupReadings = "readings"
	GroupMcelog   = "mcelog"
)

// CollectResult is what a collection did.
type CollectResult struct {
	Tool    string         `json:"tool"`
	Made    time.Time      `json:"made"`
	Sudo    bool           `json:"sudo,omitzero"`
	Entries []CollectEntry `json:"entries"`
}

// Partial tells a collection in which a step was denied, timed out or
// failed. A step of the SPD group never makes a collection partial: on
// most servers Linux has no path to the SPD, and the probe says why.
func (r *CollectResult) Partial() bool {
	for _, e := range r.Entries {
		if e.Group == GroupSPD {
			continue
		}
		if e.State == StepDenied || e.State == StepTimeout || e.State == StepFailed {
			return true
		}
	}
	return false
}

// Bounds of a collection.
const (
	maxCollectFile  = 64 << 20  // one copied file
	maxCollectDB    = 512 << 20 // the rasdaemon database
	maxEdacFiles    = 20000
	maxProgramBytes = 16 << 20
)

// hostFiles are the files that name the host and its boot.
var hostFiles = []string{"proc/stat", "proc/uptime", "proc/version", "proc/cmdline", "proc/cpuinfo",
	"proc/sys/kernel/hostname", "proc/sys/kernel/osrelease", "proc/sys/kernel/arch", "proc/sys/kernel/random/boot_id",
	"etc/hostname", "sys/class/dmi/id/sys_vendor", "sys/class/dmi/id/product_name", "sys/class/dmi/id/board_vendor",
	"sys/class/dmi/id/board_name", "sys/class/dmi/id/bios_vendor", "sys/class/dmi/id/bios_version", "sys/class/dmi/id/bios_date",
	"sys/hypervisor/type"}

// readingFiles are the files of the memory readings.
var readingFiles = []string{"proc/meminfo", "proc/vmstat", "proc/pressure/memory", "proc/slabinfo", "sys/kernel/mm/lru_gen/enabled"}

type collector struct {
	o   CollectOptions
	res *CollectResult
}

// CollectHost makes a collection. The error is for a collection that could
// not be written at all; what a step lacked is in its entry.
func CollectHost(ctx context.Context, o CollectOptions) (*CollectResult, error) {
	if o.Root == "" {
		o.Root = "/"
	}
	if o.Deadline <= 0 {
		o.Deadline = 2 * time.Minute
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	if err := os.MkdirAll(o.Out, 0o755); err != nil {
		return nil, err
	}
	if ents, err := os.ReadDir(o.Out); err != nil {
		return nil, err
	} else if len(ents) > 0 {
		return nil, fmt.Errorf("%s is not empty", o.Out)
	}
	ctx, cancel := context.WithTimeout(ctx, o.Deadline)
	defer cancel()
	c := &collector{o: o, res: &CollectResult{Tool: buildinfo.String(), Made: o.Now.UTC(), Sudo: o.Sudo, Entries: []CollectEntry{}}}

	for _, f := range hostFiles {
		c.copyFile(GroupHost, f, false)
	}
	c.dmesg()
	c.edac()
	for _, rel := range RasdaemonPaths {
		if _, err := os.Stat(c.src(rel)); err == nil {
			c.copyFile(GroupErrors, rel, false)
			break
		}
	}
	tables := c.copyFile(GroupSMBIOS, "sys/firmware/dmi/tables/DMI", true)
	if tables {
		c.copyFile(GroupSMBIOS, "sys/firmware/dmi/tables/smbios_entry_point", true)
	} else {
		c.program(ctx, GroupSMBIOS, "dmidecode.txt", "dmidecode", []string{"-t", "memory"}, nil, 10*time.Second, o.Sudo)
	}
	if !o.NoSPD {
		c.spd(ctx)
	}
	for _, f := range readingFiles {
		c.copyFile(GroupReadings, f, true)
	}
	c.cpuFiles()
	c.program(ctx, GroupReadings, "numactl-hardware.txt", "numactl", []string{"--hardware"}, nil, 5*time.Second, false)
	c.program(ctx, GroupReadings, "numastat.txt", "numastat", nil, []string{"NUMASTAT_WIDTH=4096"}, 5*time.Second, false)
	c.program(ctx, GroupReadings, "sar-B.txt", "sar", []string{"-B"}, nil, 15*time.Second, false)
	c.program(ctx, GroupReadings, "lshw.txt", "lshw", []string{"-c", "memory", "-c", "processor"}, nil, 15*time.Second, o.Sudo)
	c.program(ctx, GroupMcelog, McelogClientFile, "mcelog", []string{"--client"}, nil, mcelog.DefaultTimeout, false)

	b, err := json.MarshalIndent(c.res, "", " ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(o.Out, "collect.json"), append(b, '\n'), 0o644); err != nil {
		return nil, err
	}
	return c.res, nil
}

func (c *collector) src(rel string) string { return filepath.Join(c.o.Root, filepath.FromSlash(rel)) }

// spd writes what the probe of the SPD saw (spd-probe.txt) and the bytes
// read of each SPD device (spd/<client>.bin), as a scan of the copy reads
// them. A failed read keeps its state in the entry and in the probe.
func (c *collector) spd(ctx context.Context) {
	start := time.Now()
	fsys, ok := os.DirFS(c.o.Root).(dimmspd.FS)
	if !ok {
		return
	}
	info, _ := hostinfo.NewRoot(c.o.Root, hostinfo.Options{}).Read()
	lo := dimmspd.LiveOptions{Now: c.o.Now, Privileged: c.o.Privileged, Virtual: info.Virtual, AllowVirtual: dimmspd.DebugVirtual(), Release: info.Kernel}
	p, devs := dimmspd.ProbeHost(ctx, fsys, lo)
	text := p.Text()
	e := CollectEntry{Group: GroupSPD, What: dimmspd.ProbeFile, From: c.src("sys/bus/i2c/devices"), State: StepOK}
	if _, err := c.write(dimmspd.ProbeFile, strings.NewReader(text)); err != nil {
		e.State, e.Note = StepFailed, shortErr(err)
	}
	e.Bytes, e.Millis = int64(len(text)), time.Since(start).Milliseconds()
	c.add(e)
	for _, d := range devs {
		if d.Path == "" {
			continue
		}
		rel := dimmspd.Dir + "/" + d.Client.ID + ".bin"
		e := CollectEntry{Group: GroupSPD, What: rel, From: c.src(d.Path), State: StepOK, Bytes: int64(len(d.Raw)), Note: "state=" + d.Client.State}
		if d.Client.Errno != "" {
			e.Note += " errno=" + d.Client.Errno + " at=" + strconv.Itoa(d.Client.At)
		}
		switch d.Client.State {
		case spd.StateDenied:
			e.State = StepDenied
		case spd.StateTimeout:
			e.State = StepTimeout
		case spd.StateIO:
			e.State = StepFailed
		}
		if _, err := c.write(rel, bytes.NewReader(d.Raw)); err != nil {
			e.State, e.Note = StepFailed, shortErr(err)
		}
		c.add(e)
	}
}

func (c *collector) dst(rel string) string { return filepath.Join(c.o.Out, filepath.FromSlash(rel)) }

func (c *collector) add(e CollectEntry) { c.res.Entries = append(c.res.Entries, e) }

func stateOf(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return StepAbsent
	case errors.Is(err, fs.ErrPermission):
		return StepDenied
	}
	return StepFailed
}

// copyFile copies a file of the host to the same path below the
// collection. A file that is not there is listed only when listed is set.
func (c *collector) copyFile(group, rel string, listed bool) bool {
	start := time.Now()
	in, err := os.Open(c.src(rel))
	if err == nil {
		defer in.Close()
		var fi os.FileInfo
		if fi, err = in.Stat(); err == nil && !fi.Mode().IsRegular() {
			err = fmt.Errorf("not a regular file")
		}
	}
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) && !listed {
			return false
		}
		c.add(CollectEntry{Group: group, What: rel, From: c.src(rel), State: stateOf(err), Note: shortErr(err)})
		return false
	}
	limit := int64(maxCollectFile)
	if group == GroupErrors {
		limit = maxCollectDB
	}
	n, err := c.write(rel, io.LimitReader(in, limit))
	e := CollectEntry{Group: group, What: rel, From: c.src(rel), State: StepOK, Bytes: n, Millis: time.Since(start).Milliseconds()}
	if err != nil {
		e.State, e.Note = stateOf(err), shortErr(err)
	}
	c.add(e)
	return err == nil
}

func (c *collector) write(rel string, r io.Reader) (int64, error) {
	p := c.dst(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return 0, err
	}
	f, err := os.Create(p)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return n, err
}

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// dmesg writes the kernel ring buffer of the running boot as dmesg prints
// it.
func (c *collector) dmesg() {
	start := time.Now()
	read := c.o.Kmsg
	if read == nil {
		read = func() ([]byte, error) {
			b, _, err := readKmsg(c.src("dev/kmsg"), maxKmsg)
			return b, err
		}
	}
	text, err := read()
	e := CollectEntry{Group: GroupErrors, What: "dmesg", From: c.src("dev/kmsg")}
	if err != nil {
		e.State, e.Note = stateOf(err), shortErr(err)
		c.add(e)
		return
	}
	n, err := c.write("dmesg", bytes.NewReader(KmsgText(text)))
	e.State, e.Bytes, e.Millis = StepOK, n, time.Since(start).Milliseconds()
	if err != nil {
		e.State, e.Note = StepFailed, shortErr(err)
	}
	c.add(e)
}

// KmsgText turns records of /dev/kmsg ("prio,seq,usec,flags;message") into
// the lines dmesg prints ("[    1.234567] message").
func KmsgText(rec []byte) []byte {
	var out bytes.Buffer
	for _, line := range bytes.Split(rec, []byte{'\n'}) {
		head, msg, ok := bytes.Cut(line, []byte{';'})
		if !ok || len(line) == 0 || line[0] == ' ' {
			continue
		}
		f := bytes.Split(head, []byte{','})
		if len(f) < 3 {
			continue
		}
		usec, err := strconv.ParseInt(string(f[2]), 10, 64)
		if err != nil {
			continue
		}
		fmt.Fprintf(&out, "[%5d.%06d] %s\n", usec/1e6, usec%1e6, msg)
	}
	return out.Bytes()
}

// edac copies the EDAC tree: every regular file below mc.
func (c *collector) edac() {
	root := c.src("sys/devices/system/edac/mc")
	start := time.Now()
	n, files := int64(0), 0
	denied := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				denied++
				return nil
			}
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 || d.IsDir() {
			return nil
		}
		if files >= maxEdacFiles {
			return filepath.SkipAll
		}
		rel, _ := filepath.Rel(c.o.Root, p)
		f, err := os.Open(p)
		if err != nil {
			denied++
			return nil
		}
		defer f.Close()
		w, err := c.write(filepath.ToSlash(rel), io.LimitReader(f, 64<<10))
		if err == nil {
			n += w
			files++
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	e := CollectEntry{Group: GroupErrors, What: "sys/devices/system/edac/mc", From: root, State: StepOK, Bytes: n, Millis: time.Since(start).Milliseconds(),
		Note: fmt.Sprintf("%d files", files)}
	if err != nil {
		e.State, e.Note = stateOf(err), shortErr(err)
	} else if denied > 0 {
		e.State, e.Note = StepDenied, fmt.Sprintf("%d files, %d not readable", files, denied)
	}
	c.add(e)
}

// cpuFiles copies the package of each CPU and the cache files of the
// first CPU of each package, the files the cache reading is made from.
func (c *collector) cpuFiles() {
	dirs, _ := filepath.Glob(c.src("sys/devices/system/cpu/cpu[0-9]*"))
	sort.Strings(dirs)
	start := time.Now()
	n, files := int64(0), 0
	seen := map[string]bool{}
	for _, d := range dirs {
		b, err := os.ReadFile(filepath.Join(d, "topology", "physical_package_id"))
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(c.o.Root, d)
		w, _ := c.write(filepath.ToSlash(rel)+"/topology/physical_package_id", bytes.NewReader(b))
		n, files = n+w, files+1
		pkg := strings.TrimSpace(string(b))
		if seen[pkg] {
			continue
		}
		seen[pkg] = true
		idx, _ := filepath.Glob(filepath.Join(d, "cache", "index*"))
		for _, ix := range idx {
			for _, name := range []string{"level", "type", "size"} {
				v, err := os.ReadFile(filepath.Join(ix, name))
				if err != nil {
					continue
				}
				r, _ := filepath.Rel(c.o.Root, filepath.Join(ix, name))
				w, _ := c.write(filepath.ToSlash(r), bytes.NewReader(v))
				n, files = n+w, files+1
			}
		}
	}
	e := CollectEntry{Group: GroupReadings, What: "sys/devices/system/cpu", From: c.src("sys/devices/system/cpu"), State: StepOK, Bytes: n,
		Millis: time.Since(start).Milliseconds(), Note: fmt.Sprintf("%d files", files)}
	if files == 0 {
		e.State, e.Note = StepAbsent, ""
	}
	c.add(e)
}

// program runs a read-only program with a time limit and writes what it
// printed. With sudo it runs as "sudo -n PROGRAM ...".
func (c *collector) program(ctx context.Context, group, what, name string, args, env []string, timeout time.Duration, sudo bool) {
	start := time.Now()
	e := CollectEntry{Group: group, What: what, From: strings.TrimSpace(name + " " + strings.Join(args, " "))}
	prog, ok := c.o.Programs[name]
	switch {
	case ok && prog == "-":
		e.State, e.Note = StepAbsent, "not run"
		c.add(e)
		return
	case !ok || prog == "":
		p, err := exec.LookPath(name)
		if err != nil {
			e.State, e.Note = StepAbsent, name+" is not installed"
			c.add(e)
			return
		}
		prog = p
	}
	if sudo {
		s, ok := c.o.Programs["sudo"]
		if !ok || s == "" {
			p, err := exec.LookPath("sudo")
			if err != nil {
				e.State, e.Note = StepAbsent, "sudo is not installed"
				c.add(e)
				return
			}
			s = p
		}
		args = append([]string{"-n", prog}, args...)
		prog = s
		e.From = "sudo -n " + e.From
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, prog, args...)
	cmd.Env = append(os.Environ(), env...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &limitedBuf{&out, maxProgramBytes}, &limitedBuf{&errOut, 4 << 10}
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	e.Millis = time.Since(start).Milliseconds()
	switch {
	case cctx.Err() != nil:
		e.State, e.Note = StepTimeout, fmt.Sprintf("no answer within %s", timeout)
	case err != nil && out.Len() == 0:
		msg := strings.TrimSpace(firstLineOf(errOut.String()))
		if msg == "" {
			msg = err.Error()
		}
		e.State, e.Note = StepFailed, msg
		if sudo || strings.Contains(strings.ToLower(msg), "permission denied") || strings.Contains(msg, "password is required") {
			e.State = StepDenied
		}
	case out.Len() == 0:
		e.State, e.Note = StepFailed, "printed nothing"
	default:
		n, werr := c.write(what, &out)
		e.State, e.Bytes = StepOK, n
		if werr != nil {
			e.State, e.Note = StepFailed, shortErr(werr)
		}
	}
	c.add(e)
}

func firstLineOf(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}

type limitedBuf struct {
	b   *bytes.Buffer
	max int
}

func (l *limitedBuf) Write(p []byte) (int, error) {
	if room := l.max - l.b.Len(); room > 0 {
		if len(p) > room {
			l.b.Write(p[:room])
		} else {
			l.b.Write(p)
		}
	}
	return len(p), nil
}
