package dimmspd

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/agenticode/my/internal/decode/spd"
)

// A probe is what the reader saw of the path to the SPD on a host, written
// as text in lines of this package's own form and kept as a host reading
// of kind spd-probe. The reason why no SPD was read is computed from the
// text (Reason), so that a later version reads the reason of an older case
// again. One line of each form:
//
//	spd-probe 1
//	virtual VMware
//	i2c present
//	adapter i2c-0 "SMBus I801 adapter at 0000:00:1f.4"
//	pci 0000:00:1f.4 id=8086:a323 driver=i801_smbus spd-wd=0
//	driver i801_smbus registered
//	driver ee1004 module
//	client 0-0050 name=ee1004 driver=ee1004 eeprom=512 read=512 state=ok
//	client 0-0051 name=ee1004 driver=- eeprom=- read=0 state=unbound
//	bmc present
//
// A probe of a virtual machine has the first two lines and no more.

// ProbeVersion is the version of the probe text this package writes.
const ProbeVersion = 1

// Driver states.
const (
	// DriverRegistered: its directory under /sys/bus/*/drivers exists (built
	// in or loaded).
	DriverRegistered = "registered"
	// DriverModule: not registered; a module file is listed for this
	// kernel.
	DriverModule = "module"
	// DriverAbsent: neither.
	DriverAbsent = "absent"
	// DriverUnknown: the module lists of the kernel could not be read.
	DriverUnknown = "unknown"
)

// Client states beside the states of a reading.
const (
	// ClientUnbound: the kernel made the client and no driver is bound.
	ClientUnbound = "unbound"
	// ClientNoFile: a driver is bound and gives no eeprom file.
	ClientNoFile = "no-file"
)

// Probe is the parsed form of a probe text.
type Probe struct {
	Version  int
	Virtual  string
	I2C      string // present, absent, or "" when not looked at
	Adapters []ProbeAdapter
	PCI      []ProbePCI
	// Drivers maps a driver name to its state; DriverOrder keeps the order
	// of the lines.
	Drivers     map[string]string
	DriverOrder []string
	Clients     []ProbeClient
	BMC         bool
}

// ProbeAdapter is an I2C adapter: "i2c-0" and its name.
type ProbeAdapter struct {
	ID, Name string
}

// ProbePCI is an SMBus controller on PCI: address, vendor:device, the
// bound driver ("-" for none), and the SPD write disable bit ("" when it
// was not read).
type ProbePCI struct {
	Address, ID, Driver, SPDWD string
}

// Vendor returns the PCI vendor ID.
func (p ProbePCI) Vendor() string { v, _, _ := strings.Cut(p.ID, ":"); return v }

// ProbeClient is an SPD device on a bus: its client name ("0-0050"), the
// client's name in the kernel (ee1004, spd5118, spd), the bound driver,
// the size the eeprom file reports and the bytes read, and the state of
// the read or of the client.
type ProbeClient struct {
	ID, Name, Driver string
	// EEPROM is the size of the eeprom file, -1 when there is none.
	EEPROM int
	Read   int
	State  string
	// Errno and At say where a read step failed; File is "nvmem" when the
	// nvmem file was read in place of eeprom; Bus is "i3c" for a device on
	// I3C.
	Errno string
	At    int
	File  string
	Bus   string
}

// Bound reports whether a driver is bound to the client.
func (c ProbeClient) Bound() bool { return c.Driver != "" && c.Driver != "-" }

// Text writes the probe in its line form.
func (p *Probe) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "spd-probe %d\n", ProbeVersion)
	if p.Virtual != "" {
		fmt.Fprintf(&b, "virtual %s\n", oneLine(p.Virtual))
		return b.String()
	}
	if p.I2C != "" {
		fmt.Fprintf(&b, "i2c %s\n", p.I2C)
	}
	for _, a := range p.Adapters {
		fmt.Fprintf(&b, "adapter %s %q\n", a.ID, oneLine(a.Name))
	}
	for _, c := range p.PCI {
		fmt.Fprintf(&b, "pci %s id=%s driver=%s", c.Address, c.ID, dash(c.Driver))
		if c.SPDWD != "" {
			fmt.Fprintf(&b, " spd-wd=%s", c.SPDWD)
		}
		b.WriteString("\n")
	}
	for _, d := range p.DriverOrder {
		fmt.Fprintf(&b, "driver %s %s\n", d, p.Drivers[d])
	}
	for _, c := range p.Clients {
		fmt.Fprintf(&b, "client %s", c.ID)
		if c.Bus != "" {
			fmt.Fprintf(&b, " bus=%s", c.Bus)
		}
		eeprom := "-"
		if c.EEPROM >= 0 {
			eeprom = strconv.Itoa(c.EEPROM)
		}
		fmt.Fprintf(&b, " name=%s driver=%s eeprom=%s read=%d state=%s", dash(c.Name), dash(c.Driver), eeprom, c.Read, c.State)
		if c.File != "" {
			fmt.Fprintf(&b, " file=%s", c.File)
		}
		if c.Errno != "" {
			fmt.Fprintf(&b, " errno=%s at=%d", c.Errno, c.At)
		}
		b.WriteString("\n")
	}
	if p.BMC {
		b.WriteString("bmc present\n")
	}
	return b.String()
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, `"`, "'")), " ")
}

// ParseProbe reads a probe text. Lines it does not know are left out, so
// that an older version reads the text of a newer one as far as it can.
func ParseProbe(text string) *Probe {
	p := &Probe{Drivers: map[string]string{}}
	lines := strings.Split(text, "\n")
	if len(lines) > 512 {
		lines = lines[:512]
	}
	for _, line := range lines {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		switch f[0] {
		case "spd-probe":
			if len(f) > 1 {
				p.Version, _ = strconv.Atoi(f[1])
			}
		case "virtual":
			p.Virtual = strings.Join(f[1:], " ")
			if p.Virtual == "" {
				p.Virtual = "hypervisor"
			}
		case "i2c":
			if len(f) > 1 {
				p.I2C = f[1]
			}
		case "adapter":
			if len(f) > 1 {
				name := ""
				if i := strings.Index(line, `"`); i >= 0 {
					name = strings.TrimSuffix(strings.TrimSpace(line[i+1:]), `"`)
				}
				p.Adapters = append(p.Adapters, ProbeAdapter{ID: f[1], Name: name})
			}
		case "pci":
			if len(f) > 1 {
				kv := pairs(f[2:])
				p.PCI = append(p.PCI, ProbePCI{Address: f[1], ID: kv["id"], Driver: kv["driver"], SPDWD: kv["spd-wd"]})
			}
		case "driver":
			if len(f) > 2 {
				if _, ok := p.Drivers[f[1]]; !ok {
					p.DriverOrder = append(p.DriverOrder, f[1])
				}
				p.Drivers[f[1]] = f[2]
			}
		case "client":
			if len(f) > 1 {
				kv := pairs(f[2:])
				c := ProbeClient{ID: f[1], Name: kv["name"], Driver: kv["driver"], State: kv["state"], Errno: kv["errno"], File: kv["file"], Bus: kv["bus"], EEPROM: -1}
				if v, err := strconv.Atoi(kv["eeprom"]); err == nil {
					c.EEPROM = v
				}
				c.Read, _ = strconv.Atoi(kv["read"])
				c.At, _ = strconv.Atoi(kv["at"])
				if c.Name == "-" {
					c.Name = ""
				}
				p.Clients = append(p.Clients, c)
			}
		case "bmc":
			p.BMC = len(f) > 1 && f[1] == "present"
		}
	}
	return p
}

func pairs(fields []string) map[string]string {
	kv := map[string]string{}
	for _, x := range fields {
		if k, v, ok := strings.Cut(x, "="); ok {
			kv[k] = v
		}
	}
	return kv
}

// Reason codes of a host on which no SPD was read. Reason tests them in
// the order of docs/compatibility.md; TestReasonOrder holds the order.
const (
	ReasonVirtual            = "virtual-machine"
	ReasonNoI2C              = "no-i2c"
	ReasonSMBusNotLoaded     = "smbus-driver-not-loaded"
	ReasonSMBusNotBuilt      = "smbus-driver-not-built"
	ReasonSMBusUnknown       = "smbus-driver-unknown"
	ReasonSMBusRefused       = "smbus-driver-refused"
	ReasonNoSMBusController  = "no-smbus-controller"
	ReasonSPDDriverNotLoaded = "spd-driver-not-loaded"
	ReasonSPDDriverNotBuilt  = "spd-driver-not-built"
	ReasonSPDDriverUnknown   = "spd-driver-unknown"
	ReasonSPDDriverRefused   = "spd-driver-refused"
	ReasonWriteDisabled      = "write-disabled"
	ReasonNoSPDDevice        = "no-spd-device"
	ReasonNoEEPROMFile       = "no-eeprom-file"
	ReasonReadDenied         = "read-denied"
	ReasonReadFailed         = "read-failed"
	ReasonNotDecoded         = "not-decoded"
	ReasonBlank              = "blank"
)

// Reasons lists the 18 reason codes in the order of their table.
var Reasons = []string{ReasonVirtual, ReasonNoI2C, ReasonSMBusNotLoaded, ReasonSMBusNotBuilt, ReasonSMBusUnknown, ReasonSMBusRefused,
	ReasonNoSMBusController, ReasonSPDDriverNotLoaded, ReasonSPDDriverNotBuilt, ReasonSPDDriverUnknown, ReasonSPDDriverRefused,
	ReasonWriteDisabled, ReasonNoSPDDevice, ReasonNoEEPROMFile, ReasonReadDenied, ReasonReadFailed, ReasonNotDecoded, ReasonBlank}

// SMBusDrivers maps a PCI vendor of an SMBus controller to its driver and
// module: Intel to i801_smbus (module i2c_i801), AMD and ATI to
// piix4_smbus (module i2c_piix4).
var SMBusDrivers = map[string][2]string{"8086": {"i801_smbus", "i2c_i801"}, "1022": {"piix4_smbus", "i2c_piix4"}, "1002": {"piix4_smbus", "i2c_piix4"}}

// SPDDriverOf returns the driver of an SPD client name: at24 for "spd",
// the name itself for ee1004 and spd5118.
func SPDDriverOf(name string) string {
	if name == "spd" {
		return "at24"
	}
	return name
}

// Configs names the kernel option that builds a module.
var Configs = map[string]string{"i2c_i801": "CONFIG_I2C_I801", "i2c_piix4": "CONFIG_I2C_PIIX4", "ee1004": "CONFIG_EEPROM_EE1004",
	"spd5118": "CONFIG_SENSORS_SPD5118", "at24": "CONFIG_EEPROM_AT24"}

func byDriverState(prefix, state string) string {
	switch state {
	case DriverModule:
		return prefix + "-not-loaded"
	case DriverAbsent:
		return prefix + "-not-built"
	case DriverRegistered:
		return prefix + "-refused"
	}
	return prefix + "-unknown"
}

// Read reports whether the probe lists a reading in a state that counts as
// read (ok, crc, short).
func (p *Probe) Read() bool {
	for _, c := range p.Clients {
		if spd.Read(c.State) {
			return true
		}
	}
	return false
}

// WriteDisabled reports whether a scan as root read the SPD write disable
// bit set on an SMBus controller.
func (p *Probe) WriteDisabled() bool {
	for _, c := range p.PCI {
		if c.SPDWD == "1" {
			return true
		}
	}
	return false
}

// Reason returns the reason why no SPD was read, or "" when a reading is
// in the state ok, crc or short. memoryType is the memory type SMBIOS
// gives for the modules (DDR4, DDR5), or "".
func Reason(p *Probe, memoryType string) string {
	if p.Virtual != "" {
		return ReasonVirtual
	}
	if p.Read() {
		return ""
	}
	wd := p.WriteDisabled()
	if len(p.Clients) > 0 {
		var bound []ProbeClient
		for _, c := range p.Clients {
			if c.Bound() {
				bound = append(bound, c)
			}
		}
		if len(bound) == 0 {
			name := p.Clients[0].Name
			drv := SPDDriverOf(name)
			ddr5 := name == "spd5118" || memoryType == "DDR5"
			st := p.Drivers[drv]
			if st == "" {
				st = DriverUnknown
			}
			if st == DriverRegistered && wd && ddr5 {
				return ReasonWriteDisabled
			}
			return byDriverState("spd-driver", st)
		}
		all := func(test func(ProbeClient) bool) bool {
			for _, c := range bound {
				if !test(c) {
					return false
				}
			}
			return true
		}
		switch {
		case all(func(c ProbeClient) bool { return c.EEPROM < 0 }):
			return ReasonNoEEPROMFile
		case all(func(c ProbeClient) bool { return c.State == spd.StateDenied }):
			return ReasonReadDenied
		case all(func(c ProbeClient) bool { return c.State == spd.StateType }):
			return ReasonNotDecoded
		case all(func(c ProbeClient) bool { return c.State == spd.StateBlank }):
			return ReasonBlank
		}
		return ReasonReadFailed
	}
	if p.I2C == "absent" {
		return ReasonNoI2C
	}
	for _, c := range p.PCI {
		if c.Bound() {
			continue
		}
		drv := SMBusDrivers[c.Vendor()][0]
		st := p.Drivers[drv]
		if drv == "" || st == "" {
			st = DriverUnknown
		}
		return byDriverState("smbus-driver", st)
	}
	if len(p.PCI) == 0 {
		return ReasonNoSMBusController
	}
	if wd && memoryType == "DDR5" {
		return ReasonWriteDisabled
	}
	return ReasonNoSPDDevice
}

// Bound reports whether a driver is bound to the controller.
func (p ProbePCI) Bound() bool { return p.Driver != "" && p.Driver != "-" }

// Short returns the short English text of a reason: the detail of the
// source in the inventory and of the "missing" line. The view has the
// same texts in both languages.
func Short(p *Probe, code string) string {
	switch code {
	case ReasonVirtual:
		return "virtual machine"
	case ReasonNoI2C:
		return "no I2C bus in this kernel"
	case ReasonSMBusNotLoaded:
		return "SMBus driver " + p.SMBusModule() + " not loaded"
	case ReasonSMBusNotBuilt:
		return "kernel built without " + p.SMBusModule()
	case ReasonSMBusUnknown:
		return "SMBus controller without driver"
	case ReasonSMBusRefused:
		return "SMBus driver did not bind the controller"
	case ReasonNoSMBusController:
		return "no SMBus controller on PCI"
	case ReasonSPDDriverNotLoaded:
		return "SPD driver " + p.SPDDriver() + " not loaded"
	case ReasonSPDDriverNotBuilt:
		return "kernel built without " + p.SPDDriver()
	case ReasonSPDDriverUnknown:
		return "SPD device without driver"
	case ReasonSPDDriverRefused:
		return "SPD driver did not bind"
	case ReasonWriteDisabled:
		return "SPD pages blocked by firmware"
	case ReasonNoSPDDevice:
		return "no SPD device on the bus"
	case ReasonNoEEPROMFile:
		return "SPD driver gives no eeprom file"
	case ReasonReadDenied:
		return "permission denied"
	case ReasonReadFailed:
		return "read failed"
	case ReasonNotDecoded:
		return "memory type not decoded"
	case ReasonBlank:
		return "only 0x00 or 0xFF bytes"
	}
	return code
}

// ShortTexts lists every short text a source detail can hold, for the
// translation of source details and its test.
func ShortTexts() []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, code := range Reasons {
		switch code {
		case ReasonSMBusNotLoaded, ReasonSMBusNotBuilt:
			for _, v := range []string{"8086", "1022"} {
				add(Short(&Probe{PCI: []ProbePCI{{ID: v + ":0000", Driver: "-"}}}, code))
			}
		case ReasonSPDDriverNotLoaded, ReasonSPDDriverNotBuilt:
			for _, n := range []string{"ee1004", "spd5118", "spd"} {
				add(Short(&Probe{Clients: []ProbeClient{{Name: n}}}, code))
			}
		default:
			add(Short(&Probe{}, code))
		}
	}
	sort.Strings(out)
	return out
}

// SMBusModule returns the module of the first SMBus controller without a
// driver, "" when it is not known.
func (p *Probe) SMBusModule() string {
	for _, c := range p.PCI {
		if !c.Bound() {
			return SMBusDrivers[c.Vendor()][1]
		}
	}
	return ""
}

// SMBusController returns the first SMBus controller without a driver.
func (p *Probe) SMBusController() (ProbePCI, bool) {
	for _, c := range p.PCI {
		if !c.Bound() {
			return c, true
		}
	}
	return ProbePCI{}, false
}

// SPDDriver returns the driver of the SPD clients the probe lists.
func (p *Probe) SPDDriver() string {
	if len(p.Clients) == 0 {
		return ""
	}
	return SPDDriverOf(p.Clients[0].Name)
}
