# 6.19.10-board-digits

An x86 guest with one socket and slot names of the form `DIMM000`, three digits for socket, channel and slot, without a bank locator that names them. Nothing was injected.

| | |
| --- | --- |
| Host | QEMU 10.2.2 on the lab host, without hardware virtualization: `q35`, CPU model Skylake-Server, 2 CPUs, 32 GiB; shown here as `lab-digits` |
| System | Fedora 44 cloud image with `numactl`, `sysstat`, `lshw` and `dmidecode` installed |
| Kernel | 6.19.10-300.fc44.x86_64, as the image ships it |
| SMBIOS | 4 memory devices written by `tools/lab/smbios17.py`: `DIMM000` and `DIMM010` with 16 GiB each (serial numbers `LABB0001` and `LABB0002`), `DIMM001` and `DIMM011` empty; bank locators `Not Specified`; 1 processor record |
| my | built from commit `c3c5f1a` |
| Captured | 2026-11-17 06:24:00 UTC with `tools/lab/boards.sh collect digits`, 373 seconds after the boot |

What the capture holds:

- What `my collect host` wrote (`collect.json`, `collect.txt`): the host facts, `dmesg` (699 lines, none about a memory error), the EDAC tree (no memory controller), the raw SMBIOS tables, the memory files of `/proc` and sysfs, `numactl-hardware.txt` (1 node), `numastat.txt`, `sar-B.txt` (5 rows ten seconds apart, 12-hour clock). `lshw` did not answer within the 15 seconds `my collect host` gives it.
- What `tools/lab/capture.sh` and `boards.sh` added: `journal-k.json`, `dmidecode.txt` (`dmidecode -t 17`), `edac-sysfs.txt`, `extra/dmidecode-all.txt` (every type), `extra/lshw.txt` (`lshw -c memory -c processor` run again without a limit; it took 16.6 seconds, `extra/lshw-seconds.txt`), and `my/`: `my` printed "no errors on record" and exit code 0.

The journal files the kernel messages under `localhost` (512) and `lab-prep` (187), the name of the disk the guest boots from; cloud-init named the guest after the last of them. The system serial number is the cloud-init seed URL on QEMU's user-network gateway, `10.0.2.2`; it was left as it is.
