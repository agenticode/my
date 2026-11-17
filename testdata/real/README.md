# Captures of real hosts

Each directory holds what one host recorded after a known set of errors was injected into it, and the plan of that injection. The tests in `internal/labcase` read every capture and compare the ledger with its plan. `docs/validation-lab.md` describes the lab, the method and what the captures changed in the tool.

| Directory | Host | Injected |
| --- | --- | --- |
| `7.3.0-baseline` | lab host | nothing |
| `7.3.0-hwpoison` | lab host, same boot | 4 pages taken offline hard through `madvise` |
| `7.3.0-soft-offline` | lab host, next boot | 3 pages taken offline soft, 2 through `madvise` and 1 by the corrected error collector |
| `7.3.0-mce` | nested guest, emulated Skylake-SP | 7 corrected and 3 uncorrected machine checks |
| `7.3.0-mce-print-all` | the same guest booted with `mce=print_all` | the same; the kernel log holds every record too |
| `7.3.0-mce-clock-step` | the same guest, clock set back after rasdaemon started | the same; rasdaemon's times are 10 seconds ahead |
| `7.3.0-mce-fatal` | the same guest | 1 fatal machine check; only the console is left |
| `7.3.0-mce-rasdaemon-1.0` | the same guest, rasdaemon 1.0.0 | the machine checks of `7.3.0-mce` |
| `7.3.0-cxl` | the same guest with an emulated CXL memory device, rasdaemon 1.0.0 | 11 corrected and 1 uncorrected error of the device, a health record, 2 poison ranges |
| `7.3.0-cxl-repair` | the same guest with two CXL memory devices under QEMU 11.1.3, rasdaemon 1.0.0 | errors in one row of each device, a soft post package repair of that row through the kernel's EDAC interface, errors again in the repaired row of one device |
| `6.19.10-ghes` | aarch64 guest with a firmware error source | 24 corrected and 2 uncorrected errors on one DIMM, as firmware error records with row and column |
| `6.19.10-ghes-clock-step` | the same guest in its next boot, clock set back after rasdaemon started | 10 corrected and 1 uncorrected error; rasdaemon's times are 10 seconds ahead and no record holds the kernel's time |
| `6.19.10-ghes-multi` | another aarch64 guest with six DIMMs, booted three times a day apart | a row, a column, a bank and a single cell fault on four DIMMs, 35 corrected and 1 uncorrected error; two DIMMs without errors |
| `6.19.10-mce-loop` | an x86 guest with a disk of its own, in a crash loop | a fatal machine check at one address in three boots in a row; each boot panics, the next reads the record from the bank |
| `6.19.10-mce-loop-scatter` | the same guest | a fatal machine check at another address and channel in each of three boots |
| `6.19.10-mce-loop-kdump` | the same guest with a crash kernel loaded | one fatal machine check; the crash kernel saves the log, the next boot reads the bank |
| `6.19.10-bert-loop` | the same guest with a boot error region made by hand | one fatal memory error of DIMM 0 with row and column, read by each of three boots |
| `6.19.10-ghes-loop-spread` | an aarch64 guest with six DIMMs, in a crash loop | a fatal firmware error record on another DIMM in each of three boots; the panic text in EFI variables |
| `6.19.10-board-two-sockets` | an x86 guest with two sockets and two NUMA nodes, slots named `CPU0_CH0_DIMM0` | nothing; the board and the memory readings of `my collect host` |
| `6.19.10-board-digits` | an x86 guest with one socket, slots named `DIMM000` | nothing |
| `6.19.10-board-letters` | an x86 guest with one socket, slots named `DIMMA1` | nothing |

The number in a name is the kernel version; every directory has a `README.md` with the versions of everything that took part.

A directory is laid out as a copy of the host's files, so the tool reads it as it is:

```
my --root testdata/real/7.3.0-mce
my import testdata/real/7.3.0-mce/dmesg testdata/real/7.3.0-mce/var/lib/rasdaemon/ras-mc_event.db
my open testdata/real/7.3.0-mce/my/case.mycase
```

| File | What it is |
| --- | --- |
| `dmesg` | the kernel log of the running boot as `dmesg` prints it |
| `journal-k.json` | `journalctl -k -o json`, on hosts with a journal |
| `journal-all.json` | in `6.19.10-ghes-multi`: the kernel messages of every boot the journal holds (`journalctl -o json _TRANSPORT=kernel`); `-k` gives the running boot only |
| `var/lib/rasdaemon/ras-mc_event.db` | the rasdaemon database as it was on disk |
| `sys/devices/system/edac/`, `edac-sysfs.txt` | the EDAC tree file by file, and as one listing |
| `dmidecode.txt` | `dmidecode -t 17` |
| `proc/`, `sys/class/dmi/id/` | what names the host and its boot |
| `plan.yaml` | the injection plan: what, where, how many times |
| `capture.env` | the time of the capture and the versions |
| `collect.json`, `collect.txt`, `numactl-hardware.txt`, `numastat.txt`, `sar-B.txt` | in the board captures: what `my collect host` wrote and ran |
| `my/` | what `my` printed on the host as root: `scan.json`, `scan.txt`, `timeline.txt`, and the bundle `case.mycase` written with `--anonymize` |
| `extra/` | what the injectors printed, what the kernel counts itself (`/proc/meminfo`, the table of the corrected error collector) and the output of `ras-mc-ctl` |
| `formats/` | in `7.3.0-hwpoison` and `6.19.10-ghes`: the kernel log of the boot in seven more forms |
| `console.log` | in `7.3.0-mce-fatal` and the loops: the kernel's lines on the serial console |
| `var/lib/systemd/pstore/` | in the loops: the dumps `systemd-pstore` archived, one per panic |
| `var/lib/my/ledger.db` | in the loops: the kept ledger of `my harvest` |
| `boots.jsonl` | in the loops: per boot of the plan its boot ID and what was put into the banks |

What named the lab was replaced with `tools/lab/sanitize.py`: host names, machine IDs, hardware and network addresses, UUIDs of file systems. Boot IDs are as recorded.
