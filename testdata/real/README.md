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
| `6.19.10-ghes` | aarch64 guest with a firmware error source | 24 corrected and 2 uncorrected errors on one DIMM, as firmware error records with row and column |

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
| `var/lib/rasdaemon/ras-mc_event.db` | the rasdaemon database as it was on disk |
| `sys/devices/system/edac/`, `edac-sysfs.txt` | the EDAC tree file by file, and as one listing |
| `dmidecode.txt` | `dmidecode -t 17` |
| `proc/`, `sys/class/dmi/id/` | what names the host and its boot |
| `plan.yaml` | the injection plan: what, where, how many times |
| `capture.env` | the time of the capture and the versions |
| `my/` | what `my` printed on the host as root: `scan.json`, `scan.txt`, `timeline.txt`, and the bundle `case.mycase` written with `--anonymize` |
| `extra/` | what the injectors printed, what the kernel counts itself (`/proc/meminfo`, the table of the corrected error collector) and the output of `ras-mc-ctl` |
| `formats/` | in `7.3.0-hwpoison` and `6.19.10-ghes`: the kernel log of the boot in seven more forms |
| `console.log` | in `7.3.0-mce-fatal`: the kernel's lines on the serial console |

What named the lab was replaced with `tools/lab/sanitize.py`: host names, machine IDs, hardware and network addresses, UUIDs of file systems. Boot IDs are as recorded.
