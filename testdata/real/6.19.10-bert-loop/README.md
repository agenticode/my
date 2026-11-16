# 6.19.10-bert-loop

A boot error region made by hand. No firmware of the lab writes one, so QEMU does: `tools/lab/bertregion` wrote `lab/region.bin`, one Generic Error Status Block of severity fatal with one Platform Memory section (DIMM 0, rank 0, device 7, bank group 0, bank 3, row 0x1a2f, column 0x238, address 0x12345040, multi-bit ECC, the SMBIOS handle 0x1100 of the guest's one DIMM), and `lab/bert.bin`, the body of a BERT table that points at it. QEMU puts the table among the guest's ACPI tables (`-acpitable sig=BERT`) and writes the region at 0x20000000 at every reset (`-device loader`); the guest keeps that page out of its RAM with `memmap=4K$0x20000000`. Every boot therefore finds the same fatal error of the boot before, as a firmware would that saw the same cell fail in every boot.

| | |
| --- | --- |
| Host | `lab-x86`, as in `6.19.10-mce-loop`, cleaned before the first boot (no kept ledger, no journal of earlier boots) |
| Kernel | 6.19.10-300.fc44.x86_64 with `mce=print_all crashkernel=256M memmap=4K$0x20000000`; `my harvest install` enabled |
| Machine | QEMU 10.2.2, q35, `-cpu Skylake-Server`, emulated without hardware virtualization |
| Boots | three, each ended by `systemctl reboot`; boot 1 started 05:50:51 UTC |
| Captured | 2026-11-16 06:03:14 UTC, in boot 3 |

What the kernel did with the region, in `dmesg`, `journal-all.json` and `console.log` of every boot:

```
BERT: Error records from previous boot:
[Hardware Error]: event severity: fatal
[Hardware Error]:  Error 0, type: fatal
[Hardware Error]:  fru_text: DIMM 0
[Hardware Error]:   section_type: memory error
[Hardware Error]:    error_status: Storage error in DRAM memory (0x0000000000000400)
[Hardware Error]:   physical_address: 0x0000000012345040
[Hardware Error]:   physical_address_mask: 0xffffffffffffffc0
[Hardware Error]:   node:0 card:0 module:0 rank:0 bank_group:0 bank_address:3 device:7 row:6703 column:568
[Hardware Error]:   error_type: 3, multi-bit ECC
[Hardware Error]:   DIMM location: BANK 0 DIMM 0
BERT: Total records found: 1
```

`sys/firmware/acpi/tables/data/BERT` is the region as the guest read it after the boot, and `sys/firmware/acpi/tables/BERT` the table. The block status of the region there is 0, and `lab/region.bin` has 1: the kernel clears it after printing the block (`bert_print_all()`, `drivers/acpi/apei/bert.c`).

The kept ledger holds a stop of DIMM 0 for each boot before a reading boot; the first of them is the boot before boot 1, which the ledger knows only as "the boot before" (start unknown). The region of boot 1 and the lines boot 1 printed of it are one record; in boots 2 and 3 the region holds the same bytes, which the ledger has already, and the lines are the record. R-LOOP-01 fires on DIMM 0.

The first region the builder made was refused by the kernel (`BERT: [Firmware Bug]: Invalid error record`): the reader and the builder of the tool put the FRU text and the time stamp of a data entry 8 bytes late. They were fixed before this capture; see `docs/validation-lab.md`.

Changed for the repository: as in `6.19.10-mce-loop`, and the machine IDs of the journal.

```
my open testdata/real/6.19.10-bert-loop/var/lib/my/ledger.db boots
my import testdata/real/6.19.10-bert-loop/sys/firmware/acpi/tables/data/BERT
```
