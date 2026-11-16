# 6.19.10-ghes-multi

Firmware error records about memory on an aarch64 guest with six DIMMs, over three boots a day apart. Four DIMMs have a fault each, every one of another shape; two have no error. The 35 corrected errors and 1 uncorrected error carry rank, device, bank group, bank, row, column and, for the corrected ones, the bit.

| | |
| --- | --- |
| Host | QEMU 10.2.2 `virt,ras=on`, CPU model neoverse-n1 emulated on the lab host, 8 CPUs, 96 GiB, which QEMU describes as six DIMMs of 16 GiB: `CPU0_A1` to `CPU0_F1`, bank locators `P0_Node0_Channel0_Dimm0` to `P0_Node0_Channel5_Dimm0`, serial numbers `LABM0001` to `LABM0006`, DDR4 RDIMM, two ranks; a copy of the disk of `6.19.10-ghes` with a name (`lab-arm6`) and a machine ID of its own |
| Kernel | 6.19.10-300.fc44.aarch64, as the image ships it |
| rasdaemon | 0.8.0 (package `rasdaemon-0.8.0-9.fc44.aarch64`), started by systemd at every boot |
| Injected | `tools/lab/ghes.sh multi`: `tools/lab/ghes.py` hands QEMU a CPER record with a memory error section (QMP `inject-ghes-v2-error`) |
| Boots | three, with the guest's clock 2 days, 1 day and 0 days behind the lab host: 2026-11-14, 2026-11-15 and 2026-11-16, chronyd disabled |
| Captured | 2026-11-16 02:26:28 UTC, in the third boot |

QEMU's own SMBIOS entries for the DIMMs would give all six one serial number, and the tool would take them for one DIMM. `tools/lab/smbios17.py` writes the six type 17 structures with a serial number each and the handles QEMU gives them (`0x1100` to `0x1105`); QEMU loads them with `-smbios file=`. Every record names its DIMM by that handle. That is what `ghes_edac` and the kernel's `DIMM location` text go by: the EDAC label is the bank locator and the device locator, `P0_Node0_Channel3_Dimm0 CPU0_D1`.

The kernel dates a firmware record by its own clock when it handles the record; the time stamp inside the record is printed in the firmware block and kept nowhere else. To spread the errors over days, the guest was started three times with its hardware clock set a day apart (QEMU `-rtc base=`), and each boot got the errors of its day.

| Slot | Injected | Per day | What the tool says |
| --- | --- | --- | --- |
| `CPU0_A1` | 3 corrected at one cell: rank 1, device 2, bank group 1, bank 2, row `0x4d2e`, column `0xf8`, bit 4 | 1, 1, 1 | single-bit fault, observe (R-SOFT-01) |
| `CPU0_B1` | 10 corrected on column `0x1c8` of rank 1, device 5, bank group 0, bank 3, in 9 rows; bit 3 each time | 3, 3, 4 | column fault, consider replacement (R-ROW-03) |
| `CPU0_C1` | nothing | | ok |
| `CPU0_D1` | 10 corrected on row `0x2b41` of rank 0, device 3, bank group 2, bank 1, in 8 columns; then 1 uncorrected at column `0x1f0` of that row, found by the patrol scrubber | 3, 3, 4 + 1 | row fault, consider replacement (R-ROW-03, R-SCRUB-01) |
| `CPU0_E1` | 12 corrected at 12 places of rank 0, device 6, bank group 3, bank 0, no two on one row or column | 4, 4, 4 | bank fault, consider replacement (R-ROW-03) |
| `CPU0_F1` | nothing | | ok |

The physical addresses are made up from the location (`ghes.py --address-base`): every DIMM has 16 GiB of its own, from `0x40000000` on, and within it the place gives the address without interleaving. The page of the uncorrected error was free (`/proc/kpageflags` shows the first page of a free block, `extra/page.txt`); the kernel took it offline (`Memory failure: 0xced052: recovery action for free buddy page: Recovered`). An uncorrected error the scrubber finds is "consider replacement" by rule R-SCRUB-01; one that a read consumed would be "replace now" by R-UE-01.

What the sources hold:

- `dmesg`, `journal-k.json`: the third boot only, with its 13 corrected and 1 uncorrected error, each as a firmware block (`{N}[Hardware Error]: ...`, 14 blocks) and an EDAC line (`EDAC MC0: 1 CE single-bit ECC on P0_Node0_Channel3_Dimm0 CPU0_D1 (...)`), and one `Memory failure` line.
- `journal-all.json`: the kernel messages of the three boots: 36 firmware blocks, 36 EDAC lines, the `Memory failure` line, three boot IDs.
- `var/lib/rasdaemon/ras-mc_event.db`: 36 rows in `mc_event` from the three boots, 11, 11 and 14 a day, dated 2026-11-14 01:57:58 to 2026-11-16 02:25:55 UTC. A copy is read with `dmesg`, so the errors of the first two boots are known from this database alone.
- `sys/devices/system/edac/mc/mc0`: the six DIMMs with their labels. The counters start at 0 at every boot: CE 1, 4, 0, 4, 4 and 0 from `CPU0_A1` to `CPU0_F1`, and UE 1 on `CPU0_D1`.
- `dmidecode.txt`: the six DIMMs with their serial numbers.
- `my/`: what `my` said on the guest in the third boot, from the journal of every boot, `/dev/kmsg` and the database: CE 35, UE 1, observation grade A; consider replacement for `CPU0_B1`, `CPU0_D1` and `CPU0_E1`, observe for `CPU0_A1`; exit code 2.

`extra/clock.jsonl` holds the boot ID of each boot and how far the guest's clock was from the lab host's; `extra/guest.txt` what the guest said about its DIMMs in the first boot.
