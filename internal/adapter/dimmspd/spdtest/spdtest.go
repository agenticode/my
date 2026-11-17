// Package spdtest makes the sysfs trees of hosts for the tests of the SPD
// reader: in memory for the reader itself, on disk for the scans and
// commands that read a root directory.
package spdtest

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing/fstest"
)

// A made host: a table of files and links of sysfs and the module lists,
// built in the test (no link is committed). The names follow the Linux 7.3
// sources of i2c-smbus, i2c-i801, i2c-piix4, ee1004 and spd5118.
type Tree struct {
	M fstest.MapFS
}

// New returns an empty tree.
func New() *Tree { return &Tree{M: fstest.MapFS{}} }

func (t *Tree) Dir(p string) *Tree {
	t.M[p] = &fstest.MapFile{Mode: fs.ModeDir | 0o755}
	return t
}

func (t *Tree) File(p, data string) *Tree {
	t.M[p] = &fstest.MapFile{Data: []byte(data), Mode: 0o444}
	return t
}

func (t *Tree) Link(p, target string) *Tree {
	t.M[p] = &fstest.MapFile{Data: []byte(target), Mode: fs.ModeSymlink | 0o777}
	return t
}

// I2C makes the bus with an adapter of the Intel SMBus controller.
func (t *Tree) I2C() *Tree {
	return t.Dir("sys/bus/i2c").Dir("sys/bus/i2c/devices").Dir("sys/bus/i2c/drivers")
}

func (t *Tree) Adapter(n, name string) *Tree {
	return t.File("sys/bus/i2c/devices/i2c-"+n+"/name", name+"\n")
}

// PCI makes an SMBus controller (class 0x0c0500) with its driver, or none
// for "-".
func (t *Tree) PCI(addr, vendor, device, driver string) *Tree {
	d := "sys/bus/pci/devices/" + addr
	t.File(d+"/class", "0x0c0500\n").File(d+"/vendor", "0x"+vendor+"\n").File(d+"/device", "0x"+device+"\n")
	if driver != "-" {
		t.Link(d+"/driver", "../../../../bus/pci/drivers/"+driver)
		t.Dir("sys/bus/pci/drivers/" + driver)
	}
	return t
}

// Other makes a PCI device that is no SMBus controller.
func (t *Tree) Other(addr string) *Tree {
	d := "sys/bus/pci/devices/" + addr
	return t.File(d+"/class", "0x060000\n").File(d+"/vendor", "0x8086\n").File(d+"/device", "0x1234\n")
}

// Config gives an Intel controller the configuration byte 0x40.
func (t *Tree) Config(addr string, hostcfg byte) *Tree {
	b := make([]byte, 256)
	b[0x40] = hostcfg
	t.M["sys/bus/pci/devices/"+addr+"/config"] = &fstest.MapFile{Data: b, Mode: 0o644}
	return t
}

// Registered makes the directory of a driver that is built in or loaded.
func (t *Tree) Registered(bus, driver string) *Tree {
	return t.Dir("sys/bus/" + bus + "/drivers/" + driver)
}

// Client makes an I2C client the kernel created; driver "-" is none, eeprom
// nil is no eeprom file.
func (t *Tree) Client(id, name, driver string, eeprom []byte) *Tree {
	d := "sys/bus/i2c/devices/" + id
	t.File(d+"/name", name+"\n")
	if driver != "-" {
		t.Link(d+"/driver", "../../../../../bus/i2c/drivers/"+driver)
		t.Registered("i2c", driver)
	}
	if eeprom != nil {
		t.M[d+"/eeprom"] = &fstest.MapFile{Data: eeprom, Mode: 0o444}
	}
	return t
}

// Modules writes the module lists of a kernel release.
func (t *Tree) Modules(release string, dep, builtin []string) *Tree {
	t.File("lib/modules/"+release+"/modules.dep", strings.Join(dep, "\n")+"\n")
	return t.File("lib/modules/"+release+"/modules.builtin", strings.Join(builtin, "\n")+"\n")
}

func (t *Tree) BMC() *Tree { return t.Dir("sys/class/ipmi") }

// The module lists of the kernel of the made hosts, with every module of
// the SMBus and SPD drivers.
// Release is the kernel release of the made hosts.
const Release = "7.3.0-test"

// AllModules holds every module of the SMBus and SPD drivers.
var AllModules = []string{
	"kernel/drivers/i2c/busses/i2c-i801.ko.xz: kernel/drivers/i2c/i2c-smbus.ko.xz",
	"kernel/drivers/i2c/busses/i2c-piix4.ko.xz: kernel/drivers/i2c/i2c-smbus.ko.xz",
	"kernel/drivers/nvmem/ee1004.ko.xz:",
	"kernel/drivers/hwmon/spd5118.ko.xz:",
	"kernel/drivers/misc/eeprom/at24.ko.xz:",
}

// Without returns the module lists without the module named.
func Without(mod string) []string {
	var out []string
	for _, l := range AllModules {
		if !strings.Contains(l, "/"+mod+".ko") {
			out = append(out, l)
		}
	}
	return out
}

// BMC makes the class directory of a BMC interface.

// Write writes the tree below dir: directories, files and symbolic links.
func (t *Tree) Write(dir string) error {
	for name, f := range t.M {
		p := filepath.Join(dir, filepath.FromSlash(name))
		switch {
		case f.Mode.IsDir():
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
		case f.Mode&fs.ModeSymlink != 0:
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			// The target is relative to the link, as sysfs has it; on disk
			// the made tree has the driver directory at the same place.
			if err := os.Symlink(string(f.Data), p); err != nil {
				return err
			}
		default:
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(p, f.Data, 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}
