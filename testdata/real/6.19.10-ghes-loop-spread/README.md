# 6.19.10-ghes-loop-spread

A crash loop spread over three DIMMs: in each of three boots a fatal firmware error record on another DIMM of socket 0 (CPU0_A1, CPU0_C1, CPU0_E1, on channels 0, 2 and 4). The kernel prints the record with its DIMM, row and column and panics; the panic text goes to EFI variables; the next boot's systemd-pstore archives it and `my harvest` reads it.

| | |
| --- | --- |
| Host | `lab-arm-loop`: a copy of the disk of `6.19.10-ghes` with a name of its own, 96 GiB as six DIMMs (`tools/lab/smbios17.py`, serials `LABL0001` to `LABL0006`), as in `6.19.10-ghes-multi` |
| Kernel | 6.19.10-300.fc44.aarch64 with `efi_pstore.pstore_disable=0` (the Fedora kernel registers efi_pstore only with it); `systemd-pstore`, `rasdaemon` and `my harvest install` enabled |
| Machine | QEMU 10.2.2 `virt,ras=on`, CPU model neoverse-n1, 8 CPUs, emulated without hardware virtualization |
| Injected | `loop-arm.sh run loop-arm-spread.plan`: per boot one CPER memory error section of severity fatal, multi-bit ECC, through QMP `inject-ghes-v2-error`; `plan.yaml` and `boots.jsonl` hold the place, the time and the boot ID |
| Captured | 2026-11-16 06:32:14 UTC, in boot 5 |

What the boots were: boot 1 the first boot of the copied disk, in which `my harvest install` ran; boots 2, 3 and 4 the boots of the plan, each ended by a fatal record; boot 5 the one after.

The kernel panicked on each record without a panic timeout (`GHES: Fatal hardware error but panic disabled`, then `Kernel panic - not syncing: GHES: Fatal hardware error`): `__ghes_panic()` in Linux 6.19 leaves `panic_timeout` at 0, and the guest stayed down. Boots 2 and 4 were restarted from outside, by QMP `system_reset` (`resets.jsonl`); boot 3 had `kernel.panic=30` set by `sysctl` and restarted 30 s after its panic. On a host in service a watchdog or the BMC would do what `system_reset` did here.

| File | What it holds |
| --- | --- |
| `var/lib/systemd/pstore/<seconds>/<count>/` | the EFI parts of each panic and the `dmesg.txt` systemd-pstore put together; one panic's 16 parts lie in three such directories, one per second the parts were written |
| `journal-all.json` | the kernel messages of the five boots (`journalctl -o json _TRANSPORT=kernel`) |
| `dmesg` | boot 5 |
| `console.log` | the kernel's lines on the serial console of the five boots; systemd's status lines are left out |
| `var/lib/my/ledger.db` | the kept ledger |
| `var/lib/rasdaemon/` | the rasdaemon database: nothing of the fatal records, which end the boot before rasdaemon gets them |

This capture showed a fault of the tool: the reader of pstore took the parts of one EFI dump for one dump per second, and made three boots of each panic. During the loop `my harvest` ran with a build before that fix (and before the kernel's stop lines were read); after it the ledger was moved aside and `my harvest` ran once with the build named in `capture.env`, reading the journal of the five boots and the archive. That ledger is the one here.

Changed for the repository: the UUID of the root file system of the image, also in its systemd-escaped forms, is `00000000-0000-4000-8000-000000000002` as in `6.19.10-ghes-multi`, the serial of its EFI system partition `0000-0002`, and the machine IDs of the journal stand-ins. `console.log` holds only the lines with a kernel stamp, and `journal-k.json` is left out: `journal-all.json` holds it.

```
my open testdata/real/6.19.10-ghes-loop-spread/var/lib/my/ledger.db boots
my boots --root testdata/real/6.19.10-ghes-loop-spread
```

R-LOOP-03 fires on the three DIMMs: three boots with uncorrected errors, no DIMM in more than one, all on socket 0.
