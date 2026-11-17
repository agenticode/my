package spdtest

import (
	"bytes"

	"github.com/agenticode/my/internal/model"
)

// Host is a made host for one reason of the SPD reader or one state of a
// reading.
type Host struct {
	Name       string
	Mem        string
	Privileged bool
	Tree       *Tree
	Faults     map[string]Fault
	// Reason is the reason code, "" for a host with a reading.
	Reason string
	Status model.SourceStatus
	// States are the states of the readings, by client.
	States map[string]string
}

func modTree() *Tree { return New().Modules(Release, AllModules, nil) }

// Hosts returns one made host per reason and per state of a reading,
// with the bytes of a DDR4, a DDR5 and a DDR3 dump in its eeprom files.
func Hosts(d4, d5, ddr3 []byte) []Host {
	bad := bytes.Clone(d4)
	bad[4] ^= 1
	intel := func() *Tree {
		return modTree().I2C().Adapter("0", "SMBus I801 adapter at 0000:00:1f.4").PCI("0000:00:1f.4", "8086", "a323", "i801_smbus").Other("0000:00:00.0")
	}
	return []Host{
		// Reasons.
		{Name: "no-i2c", Tree: (modTree().Other("0000:00:00.0")), Reason: "no-i2c", Status: model.SourceAbsent},
		{Name: "smbus-driver-not-loaded", Tree: (modTree().I2C().PCI("0000:00:1f.4", "8086", "a323", "-")), Reason: "smbus-driver-not-loaded", Status: model.SourceAbsent},
		{Name: "smbus-driver-not-built", Tree: (New().Modules(Release, Without("i2c-piix4"), nil).I2C().PCI("0000:00:14.0", "1022", "790b", "-")), Reason: "smbus-driver-not-built", Status: model.SourceAbsent},
		{Name: "smbus-driver-unknown", Tree: (New().I2C().PCI("0000:00:1f.4", "8086", "a323", "-")), Reason: "smbus-driver-unknown", Status: model.SourceAbsent},
		{Name: "smbus-driver-refused", Tree: (modTree().I2C().PCI("0000:00:1f.4", "8086", "a323", "-").Registered("pci", "i801_smbus")), Reason: "smbus-driver-refused", Status: model.SourceAbsent},
		{Name: "no-smbus-controller", Tree: (modTree().I2C().Adapter("3", "Synopsys DesignWare I2C adapter").Other("0000:00:00.0").BMC()), Reason: "no-smbus-controller", Status: model.SourceAbsent},
		{Name: "spd-driver-not-loaded", Mem: "DDR4", Tree: (intel().Client("0-0050", "ee1004", "-", nil).Client("0-0051", "ee1004", "-", nil)), Reason: "spd-driver-not-loaded", Status: model.SourceEmpty},
		{Name: "spd-driver-not-built", Mem: "DDR5", Tree: (New().Modules(Release, Without("spd5118"), nil).I2C().Adapter("0", "SMBus I801 adapter at 0000:00:1f.4").PCI("0000:00:1f.4", "8086", "a323", "i801_smbus").Client("0-0050", "spd5118", "-", nil)), Reason: "spd-driver-not-built", Status: model.SourceEmpty},
		{Name: "spd-driver-unknown", Mem: "DDR4", Tree: (New().I2C().Adapter("0", "SMBus I801 adapter at 0000:00:1f.4").PCI("0000:00:1f.4", "8086", "a323", "i801_smbus").Client("0-0050", "ee1004", "-", nil)), Reason: "spd-driver-unknown", Status: model.SourceEmpty},
		{Name: "spd-driver-refused", Mem: "DDR5", Tree: (intel().Client("0-0050", "spd5118", "-", nil).Registered("i2c", "spd5118")), Reason: "spd-driver-refused", Status: model.SourceEmpty},
		{Name: "write-disabled", Mem: "DDR5", Privileged: true, Tree: (intel().Config("0000:00:1f.4", 0x11).Client("0-0050", "spd5118", "-", nil).Registered("i2c", "spd5118")), Reason: "write-disabled", Status: model.SourceEmpty},
		{Name: "write-disabled-no-client", Mem: "DDR5", Privileged: true, Tree: (intel().Config("0000:00:1f.4", 0x10).Registered("i2c", "spd5118")), Reason: "write-disabled", Status: model.SourceAbsent},
		{Name: "no-spd-device", Mem: "DDR4", Tree: (intel().Registered("i2c", "ee1004").BMC()), Reason: "no-spd-device", Status: model.SourceAbsent},
		{Name: "no-eeprom-file", Mem: "DDR5", Tree: (intel().Client("0-0050", "spd5118", "spd5118", nil)), Reason: "no-eeprom-file", Status: model.SourceEmpty},
		{Name: "read-denied", Mem: "DDR4", Tree: intel().Client("0-0050", "ee1004", "ee1004", d4), Faults: map[string]Fault{"sys/bus/i2c/devices/0-0050/eeprom": {OpenErr: EACCES}}, Reason: "read-denied", Status: model.SourceEmpty, States: map[string]string{"0-0050": "denied"}},
		{Name: "read-failed", Mem: "DDR4", Tree: intel().Client("0-0050", "ee1004", "ee1004", d4).Client("0-0051", "ee1004", "ee1004", d4), Faults: map[string]Fault{
			"sys/bus/i2c/devices/0-0050/eeprom": {At: 0, Err: ENXIO},
			"sys/bus/i2c/devices/0-0051/eeprom": {OpenErr: EACCES}}, Reason: "read-failed", Status: model.SourceEmpty, States: map[string]string{"0-0050": "io", "0-0051": "denied"}},
		{Name: "not-decoded", Mem: "DDR3", Tree: (intel().Client("0-0050", "spd", "at24", ddr3)), Reason: "not-decoded", Status: model.SourceEmpty,
			States: map[string]string{"0-0050": "type"}},
		{Name: "blank", Mem: "DDR4", Tree: (intel().Client("0-0050", "ee1004", "ee1004", make([]byte, 512))), Reason: "blank", Status: model.SourceEmpty,
			States: map[string]string{"0-0050": "blank"}},
		// States of a reading.
		{Name: "state-ok", Mem: "DDR4", Tree: (intel().Client("0-0050", "ee1004", "ee1004", d4).Client("0-0051", "ee1004", "-", nil)), Status: model.SourceOK,
			States: map[string]string{"0-0050": "ok"}},
		{Name: "state-crc", Mem: "DDR4", Tree: (intel().Client("0-0050", "ee1004", "ee1004", bad)), Status: model.SourceOK,
			States: map[string]string{"0-0050": "crc"}},
		{Name: "state-short", Mem: "DDR5", Privileged: true, Tree: intel().Config("0000:00:1f.4", 0x10).Client("0-0050", "spd5118", "spd5118", d5), Faults: map[string]Fault{"sys/bus/i2c/devices/0-0050/eeprom": {At: 128, Err: EIO}}, Status: model.SourceOK, States: map[string]string{"0-0050": "short"}},
		{Name: "state-io", Mem: "DDR4", Tree: intel().Client("0-0050", "ee1004", "ee1004", d4), Faults: map[string]Fault{"sys/bus/i2c/devices/0-0050/eeprom": {At: 0, Err: EIO}}, Reason: "read-failed", Status: model.SourceEmpty, States: map[string]string{"0-0050": "io"}},
		{Name: "state-timeout", Mem: "DDR4", Tree: intel().Client("0-0050", "ee1004", "ee1004", d4).Client("0-0051", "ee1004", "ee1004", d4), Faults: map[string]Fault{"sys/bus/i2c/devices/0-0050/eeprom": {At: 0, Block: true}}, Reason: "read-failed", Status: model.SourceEmpty, States: map[string]string{"0-0050": "timeout", "0-0051": "timeout"}},
	}
}
