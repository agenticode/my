# SPD in two lab guests

What `tools/lab/spd.sh` recorded on 2026-11-17 in an x86 guest of the QEMU q35 machine on the Fedora 44 cloud image, kernel `6.19.10-300.fc44.x86_64`: once with SMBIOS memory devices of DDR4, once of DDR5, two populated and two empty slots each. [docs/validation-lab.md](../../../docs/validation-lab.md#spd) describes the run and what it showed.

| File | What it holds |
| --- | --- |
| `collect/` | what `MY_DEBUG=spd-vm my collect host` wrote as root: `spd-probe.txt`, `spd/` (the DDR4 guest only, two empty files) and the files a scan reads |
| `kernel-log.txt` | the lines of the kernel log of the boot about the SMBus and the SPD clients, before MemorY ran |
| `i2c-devices.txt` | each entry of `/sys/bus/i2c/devices` with its name, its driver and the size of its `eeprom` file |
| `lsmod.txt` | the SMBus and SPD modules that were loaded |
| `my-spd.txt` | what `MY_DEBUG=spd-vm my spd` printed in the guest, with the texts of that day |

The EEPROMs QEMU puts on the SMBus of the q35 machine hold no SPD data, and nothing answers at the page select addresses 0x36 and 0x37. In the DDR4 guest `ee1004` bound both clients and every read ended in ENXIO; in the DDR5 guest `spd5118` bound neither. `TestLabGuests` in `cmd/my` reads both copies with `my spd --root`.

The host name `lab-spd` and the serial numbers `LABS0001` and `LABS0002` are those of the guest. The SMBIOS system serial number holds the address the guest fetched its cloud-init settings from, `10.0.2.2`, the gateway of QEMU's user network, which names no machine.
