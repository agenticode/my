package dimmspd

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/decode/spd"
	"github.com/agenticode/my/internal/model"
)

// What the reader of a host reads, all of it below /sys and /lib/modules
// and all of it for reading (docs/compatibility.md, "Module SPD"):
//
//	sys/bus/i2c/devices/i2c-N/name                 the adapters
//	sys/bus/i2c/devices/N-00AA/{name,driver,eeprom} the SPD clients, 0x50 to 0x57
//	sys/bus/nvmem/devices/N-00AA/nvmem             the bytes, where eeprom is missing
//	sys/bus/i3c/devices/*/{driver,eeprom}          DDR5 hubs on I3C, tried
//	sys/bus/i2c/drivers/{ee1004,spd5118,at24}      whether a driver is registered
//	sys/bus/pci/drivers/{i801_smbus,piix4_smbus}
//	sys/bus/pci/devices/*/{class,vendor,device,driver} the SMBus controllers
//	sys/bus/pci/devices/<Intel SMBus>/config       byte 0x40, as root only
//	lib/modules/<release>/{modules.dep,modules.builtin}
//	sys/class/ipmi, sys/firmware/dmi/entries/38-*  whether there is a BMC
//
// It runs no program, loads no module, writes nowhere, opens no device
// under /dev and reads no hwmon file of spd5118: reading an alarm file
// there clears the alarm.

// Bounds of a read.
const (
	// Step is the size of one read of an eeprom file: half a page of
	// ee1004, one page of spd5118, so a step never crosses a page.
	Step = 128
	// MaxClients and MaxAdapters bound what is looked at.
	MaxClients  = 64
	MaxAdapters = 8
	maxPCI      = 4096
)

// DefaultDeadline is the time all readings of a host share.
const DefaultDeadline = 5 * time.Second

// FS is the file system the reader of a host looks at: read only, with
// symbolic links (the driver links of sysfs).
type FS interface {
	fs.ReadDirFS
	fs.StatFS
	fs.ReadLinkFS
}

// LiveOptions describe a host to probe.
type LiveOptions struct {
	// Now is the time of the readings.
	Now time.Time
	// Privileged: the scan runs as root and may read the PCI configuration
	// byte of SPD write disable.
	Privileged bool
	// Virtual names the hypervisor of a virtual machine; such a host is
	// not probed unless AllowVirtual is set (MY_DEBUG=spd-vm, for the lab).
	Virtual      string
	AllowVirtual bool
	// Release is the kernel release, for the module lists.
	Release string
	// Deadline bounds all readings; zero is DefaultDeadline.
	Deadline time.Duration
	// Origin is how the root is named in the readings ("" for "/").
	Origin string
}

// Device is one SPD device of a probe with what was read of it.
type Device struct {
	Client ProbeClient
	// Path is the file read, below the root.
	Path string
	Bus  string
	Addr int
	Raw  []byte
}

var (
	clientName  = regexp.MustCompile(`^(\d+)-00(5[0-7])$`)
	adapterName = regexp.MustCompile(`^i2c-(\d+)$`)
)

// spdNames are the client names the kernel gives SPD EEPROMs.
var spdNames = map[string]bool{"ee1004": true, "spd5118": true, "spd": true}

// ProbeHost looks at the path to the SPD of the host whose root is fsys and
// reads every SPD it can, within the deadline. It returns the probe and
// the devices with their bytes.
func ProbeHost(ctx context.Context, fsys FS, o LiveOptions) (*Probe, []Device) {
	p := &Probe{Drivers: map[string]string{}}
	if o.Virtual != "" && !o.AllowVirtual {
		p.Virtual = o.Virtual
		return p, nil
	}
	deadline := o.Deadline
	if deadline <= 0 {
		deadline = DefaultDeadline
	}
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	if _, err := fs.Stat(fsys, "sys/bus/i2c"); err != nil {
		p.I2C = "absent"
	} else {
		p.I2C = "present"
	}
	mods := moduleLists(fsys, o.Release)
	devs := clients(fsys, p)
	devs = append(devs, i3cClients(fsys)...)
	smbus(fsys, p, o.Privileged)

	// The drivers: of each SMBus controller, and of the SPD clients.
	addDriver := func(name, dir string) {
		if _, ok := p.Drivers[name]; ok {
			return
		}
		p.DriverOrder = append(p.DriverOrder, name)
		p.Drivers[name] = driverState(fsys, dir, name, mods)
	}
	for _, c := range p.PCI {
		if d, ok := SMBusDrivers[c.Vendor()]; ok {
			addDriver(d[0], "sys/bus/pci/drivers")
		}
	}
	spdDrivers := []string{"ee1004", "spd5118"}
	for _, d := range devs {
		if d.Client.Name == "spd" {
			spdDrivers = append(spdDrivers, "at24")
			break
		}
	}
	for _, d := range spdDrivers {
		addDriver(d, "sys/bus/i2c/drivers")
	}

	// The reads, in client order, under one deadline.
	timedOut := false
	for i := range devs {
		d := &devs[i]
		if d.Client.State != "" {
			continue // unbound, no file
		}
		if timedOut || ctx.Err() != nil {
			timedOut = true
			d.Client.State = spd.StateTimeout
			continue
		}
		readDevice(ctx, fsys, d)
		if d.Client.State == spd.StateTimeout {
			timedOut = true
		}
	}
	for _, d := range devs {
		p.Clients = append(p.Clients, d.Client)
	}
	p.BMC = bmc(fsys)
	return p, devs
}

// clients lists the adapters and the SPD clients of the I2C bus.
func clients(fsys FS, p *Probe) []Device {
	ents, err := fsys.ReadDir("sys/bus/i2c/devices")
	if err != nil {
		return nil
	}
	names := map[string]string{}
	var ids []string
	var devs []Device
	for _, e := range ents {
		n := e.Name()
		if m := adapterName.FindStringSubmatch(n); m != nil {
			names[m[1]] = readText(fsys, "sys/bus/i2c/devices/"+n+"/name")
			ids = append(ids, m[1])
			continue
		}
		m := clientName.FindStringSubmatch(n)
		if m == nil || len(devs) >= MaxClients {
			continue
		}
		dir := "sys/bus/i2c/devices/" + n
		name := readText(fsys, dir+"/name")
		if !spdNames[name] {
			continue
		}
		addr, _ := strconv.ParseInt(m[2], 16, 32)
		d := Device{Client: ProbeClient{ID: n, Name: name, Driver: linkName(fsys, dir+"/driver"), EEPROM: -1}, Bus: "i2c-" + m[1], Addr: int(addr)}
		if d.Client.Bound() {
			if fi, err := fs.Stat(fsys, dir+"/eeprom"); err == nil && !fi.IsDir() {
				d.Path, d.Client.EEPROM = dir+"/eeprom", int(fi.Size())
			} else if fi, err := fs.Stat(fsys, "sys/bus/nvmem/devices/"+n+"/nvmem"); err == nil && !fi.IsDir() {
				d.Path, d.Client.EEPROM, d.Client.File = "sys/bus/nvmem/devices/"+n+"/nvmem", int(fi.Size()), "nvmem"
			} else {
				d.Client.State = ClientNoFile
			}
		} else {
			d.Client.Driver = "-"
			d.Client.State = ClientUnbound
		}
		devs = append(devs, d)
	}
	sort.Slice(devs, func(i, j int) bool { return devs[i].Client.ID < devs[j].Client.ID })
	// The adapters that hold an SPD client, then the SMBus adapters.
	held := map[string]bool{}
	for _, d := range devs {
		held[strings.TrimPrefix(d.Bus, "i2c-")] = true
	}
	sort.Slice(ids, func(i, j int) bool {
		a, _ := strconv.Atoi(ids[i])
		b, _ := strconv.Atoi(ids[j])
		return a < b
	})
	for _, pass := range []func(string) bool{
		func(id string) bool { return held[id] },
		func(id string) bool { return !held[id] && strings.HasPrefix(names[id], "SMBus") },
	} {
		for _, id := range ids {
			if pass(id) && len(p.Adapters) < MaxAdapters {
				p.Adapters = append(p.Adapters, ProbeAdapter{ID: "i2c-" + id, Name: names[id]})
			}
		}
	}
	return devs
}

// i3cClients lists DDR5 hubs on I3C: devices whose driver is spd5118_i3c
// and that have an eeprom file. No source confirms the path of that file
// on I3C, so it is tried and never required.
func i3cClients(fsys FS) []Device {
	ents, err := fsys.ReadDir("sys/bus/i3c/devices")
	if err != nil {
		return nil
	}
	var devs []Device
	for _, e := range ents {
		dir := "sys/bus/i3c/devices/" + e.Name()
		if linkName(fsys, dir+"/driver") != "spd5118_i3c" || len(devs) >= MaxClients {
			continue
		}
		fi, err := fs.Stat(fsys, dir+"/eeprom")
		if err != nil || fi.IsDir() {
			continue
		}
		devs = append(devs, Device{Client: ProbeClient{ID: e.Name(), Bus: "i3c", Name: "spd5118", Driver: "spd5118_i3c", EEPROM: int(fi.Size())},
			Path: dir + "/eeprom", Bus: "i3c"})
	}
	return devs
}

// smbus lists the SMBus controllers on PCI (class 0x0c05) with their
// drivers and, as root, the SPD write disable bit of an Intel controller.
func smbus(fsys FS, p *Probe, privileged bool) {
	ents, err := fsys.ReadDir("sys/bus/pci/devices")
	if err != nil {
		return
	}
	for i, e := range ents {
		if i >= maxPCI || len(p.PCI) >= MaxAdapters {
			break
		}
		dir := "sys/bus/pci/devices/" + e.Name()
		if !strings.HasPrefix(readText(fsys, dir+"/class"), "0x0c05") {
			continue
		}
		vendor := strings.TrimPrefix(readText(fsys, dir+"/vendor"), "0x")
		device := strings.TrimPrefix(readText(fsys, dir+"/device"), "0x")
		c := ProbePCI{Address: e.Name(), ID: vendor + ":" + device, Driver: dash(linkName(fsys, dir+"/driver"))}
		if privileged && vendor == "8086" {
			if b, err := readAt(fsys, dir+"/config", 0x40); err == nil {
				c.SPDWD = "0"
				if b&0x10 != 0 {
					c.SPDWD = "1"
				}
			}
		}
		p.PCI = append(p.PCI, c)
	}
}

// modules is what the module lists of the kernel hold: module names, with
// "-" read as "_".
type modules struct {
	known        bool
	dep, builtin map[string]bool
}

func moduleLists(fsys FS, release string) modules {
	m := modules{dep: map[string]bool{}, builtin: map[string]bool{}}
	if release == "" || strings.ContainsAny(release, "/\x00") || release == "." || release == ".." {
		return m
	}
	dir := "lib/modules/" + release
	read := func(name string, into map[string]bool) bool {
		b, err := adapter.ReadFile(fsys, dir+"/"+name, 32<<20)
		if err != nil {
			return false
		}
		for _, line := range strings.Split(string(b), "\n") {
			first, _, _ := strings.Cut(line, ":")
			base := path.Base(strings.TrimSpace(first))
			if i := strings.Index(base, ".ko"); i > 0 {
				into[strings.ReplaceAll(base[:i], "-", "_")] = true
			}
		}
		return true
	}
	dep := read("modules.dep", m.dep)
	builtin := read("modules.builtin", m.builtin)
	m.known = dep || builtin
	return m
}

// moduleOf maps a driver name to its module.
var moduleOf = map[string]string{"i801_smbus": "i2c_i801", "piix4_smbus": "i2c_piix4", "ee1004": "ee1004", "spd5118": "spd5118", "at24": "at24"}

func driverState(fsys FS, dir, name string, m modules) string {
	if fi, err := fs.Stat(fsys, dir+"/"+name); err == nil && fi.IsDir() {
		return DriverRegistered
	}
	mod := moduleOf[name]
	switch {
	case !m.known:
		return DriverUnknown
	case m.builtin[mod]:
		return DriverRegistered
	case m.dep[mod]:
		return DriverModule
	}
	return DriverAbsent
}

func bmc(fsys FS) bool {
	if _, err := fs.Stat(fsys, "sys/class/ipmi"); err == nil {
		return true
	}
	ents, err := fsys.ReadDir("sys/firmware/dmi/entries")
	if err != nil {
		return false
	}
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "38-") {
			return true
		}
	}
	return false
}

func readText(fsys FS, name string) string {
	b, err := adapter.ReadFile(fsys, name, 4096)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.ToValidUTF8(string(b), "?"))
}

func linkName(fsys FS, name string) string {
	t, err := fsys.ReadLink(name)
	if err != nil {
		return ""
	}
	return path.Base(t)
}

// readAt reads the byte at off of a file, with one read of off+1 bytes.
func readAt(fsys FS, name string, off int) (byte, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	b := make([]byte, off+1)
	n, err := io.ReadFull(f, b)
	if n <= off {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return 0, err
	}
	return b[off], nil
}

type stepResult struct {
	b   []byte
	err error
}

// readDevice reads the eeprom file of a device in steps of 128 bytes, one
// read per step, up to the size the file reports and at most 2,048 bytes.
// The bytes before a failed step are kept. A step that does not return
// before the deadline is left behind.
func readDevice(ctx context.Context, fsys FS, d *Device) {
	f, err := fsys.Open(d.Path)
	if err != nil {
		d.Client.State, d.Client.Errno = failState(err), errnoName(err)
		return
	}
	size := d.Client.EEPROM
	if size <= 0 || size > model.MaxSPDBytes {
		size = model.MaxSPDBytes
	}
	done := make(chan struct{})
	defer func() {
		// A step left behind closes the file when it returns.
		go func() { <-done; f.Close() }()
	}()
	var raw []byte
	var failed error
	timedOut := false
	results := make(chan stepResult, 1)
	go func() {
		defer close(done)
		for off := 0; off < size; off += Step {
			buf := make([]byte, min(Step, size-off))
			n, err := f.Read(buf)
			select {
			case results <- stepResult{buf[:n], err}:
			case <-ctx.Done():
				return
			}
			if err != nil || n == 0 {
				return
			}
		}
		close(results)
	}()
loop:
	for {
		select {
		case <-ctx.Done():
			timedOut = true
			break loop
		case r, ok := <-results:
			if !ok {
				break loop
			}
			raw = append(raw, r.b...)
			if r.err != nil {
				if !errors.Is(r.err, io.EOF) {
					failed = r.err
				}
				break loop
			}
			if len(r.b) == 0 {
				break loop
			}
		}
	}
	d.Raw = raw
	d.Client.Read = len(raw)
	switch {
	case timedOut:
		d.Client.Errno, d.Client.At = "deadline", len(raw)
		if len(raw) == 0 {
			d.Client.State = spd.StateTimeout
			return
		}
	case failed != nil:
		d.Client.Errno, d.Client.At = errnoName(failed), len(raw)
		if len(raw) == 0 {
			d.Client.State = failState(failed)
			return
		}
	}
	d.Client.State = spd.Decode(raw).State
}

func failState(err error) string {
	if errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
		return spd.StateDenied
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, syscall.ETIMEDOUT) {
		return spd.StateTimeout
	}
	return spd.StateIO
}

var errnos = map[syscall.Errno]string{
	syscall.EIO: "EIO", syscall.ENXIO: "ENXIO", syscall.EACCES: "EACCES", syscall.EPERM: "EPERM", syscall.ETIMEDOUT: "ETIMEDOUT",
	syscall.EAGAIN: "EAGAIN", syscall.ENODEV: "ENODEV", syscall.EINVAL: "EINVAL", syscall.EBUSY: "EBUSY", syscall.ENOENT: "ENOENT",
	syscall.EOPNOTSUPP: "EOPNOTSUPP",
}

// errnoName names the error of a failed open or read.
func errnoName(err error) string {
	var e syscall.Errno
	if errors.As(err, &e) {
		if n, ok := errnos[e]; ok {
			return n
		}
		return "errno" + strconv.Itoa(int(e))
	}
	switch {
	case errors.Is(err, fs.ErrPermission):
		return "EACCES"
	case errors.Is(err, fs.ErrNotExist):
		return "ENOENT"
	}
	return "error"
}
