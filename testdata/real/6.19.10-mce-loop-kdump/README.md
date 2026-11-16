# 6.19.10-mce-loop-kdump

A fatal machine check on a guest with a crash kernel loaded. The panic boots the crash kernel instead of writing pstore; the crash kernel saves the kernel log of the boot that panicked and restarts the guest, and the next boot finds the record still in the bank.

| | |
| --- | --- |
| Host | `lab-x86`, as in `6.19.10-mce-loop`, cleaned before: no kept ledger, no archived dumps, no journal of earlier boots |
| Kernel | 6.19.10-300.fc44.x86_64 with `mce=print_all crashkernel=256M`; `kdump.service` (kdump-utils), `systemd-pstore`, `rasdaemon` and `my harvest install` enabled |
| Machine | QEMU 10.2.2, q35, `-cpu Skylake-Server`, emulated without hardware virtualization |
| Injected | `loop.sh run loop-kdump.plan`: in boot 2, `mce -b 0 7 0xbe00000000010090 0x4 0x12345040 0x86`; `boots.jsonl` holds the boot ID and the time |
| Captured | 2026-11-16 05:11:00 UTC, in boot 3 |

The boots: boot 1 after the guest was cleaned, which ended with the reboot that loaded the crash kernel; boot 2, which panicked at 561 s; the crash kernel; boot 3.

| File | What it holds |
| --- | --- |
| `var/crash/127.0.0.1-2026-11-16-045505/vmcore-dmesg.txt` | the kernel log of boot 2 up to `Kernel panic - not syncing: Fatal machine check`, with the record |
| `var/crash/.../kexec-dmesg.log` | the log of the crash kernel itself. MemorY does not read it |
| `var/lib/systemd/pstore/mce-erst-*` | the record `mce_panic()` wrote into ERST before the crash kernel started. No `dmesg-erst-*`: with `crash_kexec_post_notifiers` off the panic goes to the crash kernel before pstore |
| `dmesg`, `journal-k.json`, `journal-all.json` | boot 3 printed the record left in bank 7 (`CPU 0: Machine Check: 0 Bank 7: be00000000010090`, `TSC 0`): the bank kept it through the crash kernel and the reset |
| `var/lib/my/ledger.db` | the kept ledger |
| `console.log` | the serial console of boot 2, the crash kernel and boot 3 |

The `vmcore` (80 MB) is not here. kdump-utils names the directory `127.0.0.1-2026-11-16-04:55:05`; a colon is not allowed in a file name of a Go module, so the directory is `...-045505` here, and the ledger's references to it were changed alike.

This capture showed a fault of the tool. The poll of boot 3 printed `TIME 1794804995`, one second before the start of boot 3 as the ledger computes it (the guest's clock was set after the read), and the boot pass filed the record under boot 2 as the boot that read it: boot 1 read "stop" and boot 2 "panic". A carried record now keeps the time of its line and the boot its source gave it. During the run `my harvest` used the build before that fix; after it the ledger was moved aside and `my harvest` ran once with the fixed build, reading the journal of the three boots and `/var/crash`. That ledger is the one here.

Changed for the repository: as in `6.19.10-mce-loop`, and the machine IDs of the three boots' journal.

```
my open testdata/real/6.19.10-mce-loop-kdump/var/lib/my/ledger.db boots
my boots --root testdata/real/6.19.10-mce-loop-kdump
```
