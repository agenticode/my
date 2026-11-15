# 6.19.10-ghes

Firmware error records about memory on an aarch64 guest: 24 corrected and 2 uncorrected errors on one DIMM, with row, column and bit of each. This is the only capture in which a source names the DIMM and the place inside it.

| | |
| --- | --- |
| Host | QEMU 10.2.2 `virt,ras=on`, CPU model neoverse-n1 emulated on the lab host, 8 CPUs, 8 GB as one DIMM (`DIMM 0`, bank `BANK 0`, serial number `LAB00A64`); Fedora 44 cloud image, SELinux permissive; shown as `lab-arm` |
| Kernel | 6.19.10-300.fc44.aarch64, as the image ships it |
| rasdaemon | 0.8.0 (package `rasdaemon-0.8.0-9.fc44.aarch64`), started by systemd |
| Injected | `tools/lab/ghes.sh run`: `tools/lab/ghes.py` hands QEMU a CPER record with a memory error section (QMP `inject-ghes-v2-error`); QEMU puts it into the generic hardware error source of its ACPI tables and raises the interrupt |
| Captured | 2026-11-15 11:11:33 UTC |

With `ras=on` QEMU gives the guest a HEST table. The kernel handles a record from it as it handles one of real firmware.

| Injection | Record | What the kernel did |
| --- | --- | --- |
| `row-1` to `row-15` | corrected, rank 0, device 7, bank group 1, bank 2, row `0x1a2f`, five columns, bits 5 and 6; three found by the scrubber | printed, reported through `ghes_edac` |
| `row-16`, `row-17` | corrected, the same bank, row `0x1a30` | the same |
| `burst` | 6 corrected at one cell of rank 1 within a second | 1 firmware block printed, 6 EDAC lines |
| `thr` | corrected, flag "error threshold exceeded", on a page in use | page taken offline soft, without a line or a trace event |
| `ue-1` | uncorrected recoverable, multi-bit ECC, on a page in use | page taken offline hard |
| `ue-2` | uncorrected recoverable, found by the scrubber | page taken offline hard |

What the sources hold:

- `dmesg`, `journal-k.json`: 21 firmware blocks of eleven lines each (`{16}[Hardware Error]: ...` with `physical_address`, `node:0 card:0 module:0 rank:0 bank_group:1 bank_address:2 device:7 row:6703 column:280 bit_position:5`, `DIMM location: BANK 0 DIMM 0`), 26 lines `EDAC MC0: 1 CE single-bit ECC on BANK 0 DIMM 0 (...)`, and two `Memory failure` lines. Row and column are decimal. The kernel prints the firmware block of a corrected error at most twice in five seconds, so the burst has one block for six errors.
- `var/lib/rasdaemon/ras-mc_event.db`: 26 rows in `mc_event` with the firmware's fields in `driver_detail`. No row in `memory_failure_event`: this kernel has no `ras:memory_failure_event` trace point for rasdaemon to enable.
- `sys/devices/system/edac/mc/mc0`: the memory controller `ghes_edac` registers, with one DIMM labelled `BANK 0 DIMM 0` and the counters 24 and 2.
- `dmidecode.txt`: the DIMM with its serial number.
- `my/`: "CE 24, UE 2, observation grade A", a multi-row fault on `DIMM 0`, finding "replace now" by rules R-UE-01 and R-SCRUB-01. Each error is in three places (the database, the firmware block, the EDAC line) and counts once.

`extra/held.jsonl` shows what became of the three pages: the content of the first moved to another page frame, the other two answer a read with `SIGBUS`.

`formats/` holds the kernel log of this boot in seven more forms, as in `7.3.0-hwpoison`.
