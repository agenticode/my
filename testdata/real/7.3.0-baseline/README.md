# 7.3.0-baseline

The lab host 79 seconds after a boot, with nothing injected. It is the reference for the other captures: what a whole boot log, an empty rasdaemon database and the SMBIOS tables of this host give when no memory error happened.

| | |
| --- | --- |
| Host | VMware guest, 16 CPUs, 48 GB, Fedora 44; shown here as `lab-vm` |
| Kernel | 7.3.0-ras (kernel.org 7.3 with `MEMORY_FAILURE`, `RAS_CEC`, `X86_MCE_INJECT`, `EDAC_DEBUG`) |
| rasdaemon | 0.8.0 (package `rasdaemon-0.8.0-9.fc44`), started by systemd |
| Captured | 2026-11-15 10:26:46 UTC with `tools/lab/vm.sh baseline` |

What the sources hold:

- `dmesg` and `journal-k.json`: 1,486 kernel messages, none about a memory error.
- `var/lib/rasdaemon/ras-mc_event.db`: the tables rasdaemon 0.8.0 creates, without rows.
- `sys/devices/system/edac`: no memory controller. The hypervisor shows no memory controller and no machine check banks to its guests, so no EDAC driver loads.
- `dmidecode.txt`: 128 memory devices, 64 named `RAM slot #n` and 64 named `NVD #n`; 3 hold 16 GiB each, without serial numbers.
- `my/`: `my` printed "no errors on record" and exit code 0.
