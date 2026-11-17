# 6.19.10-board-two-sockets

An x86 guest with two sockets, two NUMA nodes and slot names of the form `CPU0_CH0_DIMM0`, with nothing injected. It shows the board of a host with two sockets and the readings of the Memory Explorer on a host with two nodes.

| | |
| --- | --- |
| Host | QEMU 10.2.2 on the lab host, without hardware virtualization: `q35`, CPU model Skylake-Server, 2 sockets of 2 cores, 64 GiB as two NUMA nodes of 32 GiB; shown here as `lab-two-sockets` |
| System | Fedora 44 cloud image with `numactl`, `sysstat`, `lshw` and `dmidecode` installed |
| Kernel | 6.19.10-300.fc44.x86_64, as the image ships it |
| SMBIOS | 8 memory devices written by `tools/lab/smbios17.py`: `CPU0_CH0_DIMM0`, `CPU0_CH1_DIMM0`, `CPU1_CH0_DIMM0`, `CPU1_CH1_DIMM0` with 16 GiB each (serial numbers `LABB0001` to `LABB0004`), and the same names with `DIMM1` empty; 2 processor records |
| my | built from commit `c3c5f1a` |
| Captured | 2026-11-17 06:15:44 UTC with `tools/lab/boards.sh collect two-sockets`, 429 seconds after the boot |

What the capture holds:

- What `my collect host` wrote (`collect.json`, `collect.txt`): the host facts, `dmesg` (723 lines, none about a memory error), the EDAC tree (no memory controller), the raw SMBIOS tables, the memory files of `/proc` and sysfs, `numactl-hardware.txt` (2 nodes), `numastat.txt` (one column per node), `sar-B.txt` (5 rows ten seconds apart from `sa1 10 6`, with a 12-hour clock). `lshw` did not answer within the 15 seconds `my collect host` gives it.
- What `tools/lab/capture.sh` added: `journal-k.json`, `dmidecode.txt` (`dmidecode -t 17`), `edac-sysfs.txt`, `extra/dmidecode-all.txt` (every type, for `testdata/explorer/lab-two-sockets`), and `my/`: `my` printed "no errors on record" and exit code 0.

The physical memory array QEMU makes (type 16) says `Number Of Devices: 1`; the slots are the 8 memory devices. The system serial number is the cloud-init seed URL on QEMU's user-network gateway, `10.0.2.2`, which every guest started this way has; it was left as it is.

The disk the guest boots from was prepared under the name `lab-prep`, and cloud-init gave the guest its name after the last kernel message of the boot. The journal therefore files the kernel messages under `localhost` (530) and `lab-prep` (193): a scan of the copy names the host `lab-two-sockets`, an import of `journal-k.json` alone names it `lab-prep`.
