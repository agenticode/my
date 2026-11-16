# 6.19.10-mce-loop

A crash loop at one place: the same fatal machine check in three boots in a row. Each one panics the kernel, the guest restarts 30 seconds later in the same QEMU process, and the next boot finds the record still in the bank.

| | |
| --- | --- |
| Host | `lab-x86`: an x86 guest with a disk of its own, Fedora 44, 4 GiB as one DIMM, an ACPI error record store (`-device acpi-erst`) as pstore backend |
| Kernel | 6.19.10-300.fc44.x86_64, booted with `mce=print_all`; `systemd-pstore`, `rasdaemon` and `my harvest install` enabled |
| Machine | QEMU 10.2.2, q35, `-cpu Skylake-Server`, emulated without hardware virtualization |
| Injected | `loop.sh run same.plan` (kept with the lab tools): in each of boots 1 to 3, `mce -b 0 7 0xbe00000000010090 0x4 0x12345040 0x86` through the monitor; `boots.jsonl` holds the boot IDs and the times |
| Captured | 2026-11-16 04:14:16 UTC, in boot 4 |

What each survivor holds:

| File | What it holds of the loop |
| --- | --- |
| `var/lib/systemd/pstore/dmesg-erst-*` | three dumps of three parts each, one per panic: the end of the kernel log of boots 1 to 3, the machine check record and `Kernel panic - not syncing: Fatal machine check` |
| `var/lib/systemd/pstore/mce-erst-*` | the record `mce_panic()` wrote into ERST, three of them. MemorY does not read them |
| `dmesg`, `journal-k.json` | boot 4, which printed the record left in bank 7 by boot 3: `CPU 0: Machine Check: 0 Bank 7: be00000000010090`, `TSC 0`, the time of the poll as `TIME` |
| `console.log` | the serial console of boots 1 to 4, from the boot loader to the panic |
| `var/lib/my/ledger.db` | the kept ledger |

The kept ledger: during the loop `my harvest` ran at every boot with a build that did not yet read the records the next boot prints. After the loop the ledger was moved aside and `my harvest` ran once with the build named in `capture.env`; it read the journal of all four boots and the pstore archive. That ledger is the one here. The journal itself was not copied before the guest was cleaned for the next loop; `console.log` holds the same kernel lines.

Every boot printed, besides bank 7 of CPU 0, a record `a000000000000000` in bank 1 of CPUs 1 to 3: the broadcast of QEMU's `mce -b` writes it. It is no memory error and gives no record.

Changed for the repository: the UUID of the root file system and the serial of the EFI system partition of the Fedora cloud image, also in their systemd-escaped forms, are `00000000-0000-4000-8000-000000000001` and `0000-0001`; the two machine IDs of the guest are `00000000000040008000000000000001` and `...0002`. The files under `proc/sys`, `proc/version` were copied empty by `tar` and were written again from `journal-k.json` and `dmesg`; `proc/cpuinfo` is not here. Nothing else was changed.

```
my open testdata/real/6.19.10-mce-loop/var/lib/my/ledger.db boots
my boots --root testdata/real/6.19.10-mce-loop
```

The guest names no DIMM for a machine check (no EDAC driver for the emulated memory controller), so the errors are the host's: the host finding says "uncorrected errors at the address 0x12345040 in 3 of the last 4 boots", and the boots grid has the three stops on the line `no DIMM`.
