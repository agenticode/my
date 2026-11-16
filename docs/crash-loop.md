# Crash loops: what a reset loses

A fatal uncorrected memory error panics the kernel. From that moment no program runs: journald writes nothing after the last lines it had written to disk, and rasdaemon stores no more records. On x86 the kernel then restarts the host by itself. When boot logging of machine checks is on, which is the default for current processors, `mca_bsp_init()` sets `mca_cfg.panic_timeout` to 30, and `mce_panic()` uses it when the panic timeout of the kernel is 0 (`arch/x86/kernel/cpu/mce/core.c`). A host with `panic=0` therefore restarts 30 seconds after a fatal machine check.

MemorY run after such a restart finds no record of the error in the journal or in rasdaemon's database. If the fault is hard, the next boot hits it again and loses its evidence the same way. `testdata/real/7.3.0-mce-fatal` is that case: only the serial console of the guest shows the error.

Some records do survive a reset:

| What survives | Written by | Read back by |
| --- | --- | --- |
| the contents of the machine check banks, after a reset that keeps power | the processor | the next boot polls every bank when boot logging is on (`__mcheck_cpu_init_prepare_banks()`), and prints what it finds when nothing else handled it |
| the panic text in pstore | `pstore_dump()` into EFI variables, the firmware's error record store (ERST) or reserved RAM | `/sys/fs/pstore/dmesg-<backend>-<id>`; `systemd-pstore` moves the files to `/var/lib/systemd/pstore` |
| the machine check record in ERST | `mce_panic()` through `apei_write_mce()` | only a reader of `/dev/mcelog`, and the read clears it |
| the kernel log of the crashed boot | a crash kernel (kdump) | `vmcore-dmesg.txt` or `dmesg.<stamp>` below `/var/crash` |
| boot error records of the firmware | firmware that handles errors before the operating system does | the BERT region, `/sys/firmware/acpi/tables/data/BERT` (readable as root); the kernel prints up to five records at boot |
| the console | a serial console server, the serial log of the BMC, netconsole | a file outside the host |

Whether each of these is there depends on settings of the kernel, the firmware and the distribution. `my preflight` reads them.

## my preflight

`my preflight` says, for each of these ways, whether a reset right after a fatal memory error would keep the records. It only reads: it changes no kernel parameter, no boot loader file, no file in `/sys` or `/proc`, and no configuration. It never opens `/dev/mcelog`.

On the lab VM of [validation-lab.md](validation-lab.md) (Fedora 44, kernel 7.3.0, QEMU without hardware virtualization):

```
host lab-vm  kernel 7.3.0-ras  x86_64  AMD family 23 model 1  booted 2026-11-15 10:27 UTC

 what a reset right after a fatal memory error would leave on this host

 kept     journal          on disk below /var/log/journal. A panic stops journald before it writes
                           the last lines, and lines below priority CRIT reach the disk every
                           SyncIntervalSec=5min
 lost     pstore-backend   no backend is registered: the firmware has no ERST table; the host did
                           not boot through EFI
 kept     pstore-archive   systemd-pstore.service is enabled and moves the dumps to
                           /var/lib/systemd/pstore at boot
 kept     pstore-vs-kdump  no crash kernel is loaded: a panic goes to pstore
 lost     kdump            no crash kernel is loaded and no crashkernel= is on the command line
 kept     mce-bootlog      on by default for this processor (AMD family 23)
 kept     mce-print        on AMD a record left in a bank is printed: /dev/mcelog does not take it
                           away from the kernel log
 info     reset-kind       reboot mode cold, the same after a panic. With panic=0 the kernel waits
                           after a panic, and after a fatal machine check it restarts in 30 s.
                           Whether the banks keep their contents over the reset depends on the board
                           and its BIOS; the first stop after my harvest is installed shows it
 none     bert             the firmware provides no BERT table
 none     erst-mce         the firmware provides no ERST table: mce_panic() keeps no copy of the
                           record
 kept     console          a serial console (ttyS0): the BMC's serial log or a console server keeps
                           the last lines
 kept     rasdaemon        rasdaemon.service is enabled and records to
                           /var/lib/rasdaemon/ras-mc_event.db
 lost     harvest          my-harvest.service is not installed: what the boot before left is read
                           only when someone runs my

 3 of 13 checks would lose records. The commands, with what each costs and how to undo it:
   my preflight --script
```

A check is `kept`, `lost`, `none` (the platform does not offer it), `unknown` (a file that tells is not readable as this user, or is not in a copy of the host's files) or `info`. The exit code is 0 when no check is lost and 1 when one is; it is no grade. `--json` prints the same as data, with the files each check read under `evidence`.

`my preflight --script` prints the fixes of the lost checks as a shell script on standard output. Above the commands of each fix stand what it does, what it costs, the public document that says so, and the commands that undo it. `my` never runs the script. The commands follow the distribution named in `/etc/os-release`: `grubby` on Fedora, RHEL and their relatives, a file in `/etc/default/grub.d` and `update-grub` on Debian and Ubuntu. On other distributions the script names the kernel parameter and says that the boot loader of the host sets it.

`--root DIR` reads a copy of a host's files. The checks that read the running host (the free space of the harvest ledger, the list of processes, the consoles) are marked, and a check whose files the copy lacks is `unknown`.

### The checks

| Check | Kept when | Fix printed | Cost, and where it is written down |
| --- | --- | --- | --- |
| `journal` | `/var/log/journal` exists and `Storage=` is not `volatile` or `none` | a drop-in with `Storage=persistent` | journal files on disk, by default up to 10% of the file system and at most 4G (`journald.conf(5)`, `SystemMaxUse=`) |
| `pstore-backend` | `/sys/module/pstore/parameters/backend` names a backend | in this order: take `erst_disable` off when the firmware has an ERST table; `efi_pstore.pstore_disable=0` on an EFI host; ramoops in a region named with `reserve_mem=` | EFI variables: each panic writes the last `kmsg_bytes` (10240 by default) of the kernel log into the variable store as variables of 1024 bytes (`efi-pstore.c`, `fs/pstore/Kconfig`). ramoops: 2 MiB of RAM, a region that the kernel documents may move between boots, and RAM that may be on the failing DIMM (`ramoops.rst`, section D): try it with a test panic |
| `pstore-archive` | `systemd-pstore.service` is enabled, `Storage=external` and `Unlink=yes` | `systemctl enable systemd-pstore.service`, or a drop-in with `Storage=external` | a few files per panic below `/var/lib/systemd/pstore` (`systemd-pstore.service(8)`, `pstore.conf(5)`). Without it the files stay in `/sys/fs/pstore` and a full store saves no later panic |
| `pstore-vs-kdump` | no crash kernel is loaded, or `crash_kexec_post_notifiers` is set | `crash_kexec_post_notifiers` | "This option increases the risks of a kdump failure, since some panic notifiers can make the crashed kernel more unstable." (`kernel-parameters.txt`) |
| `kdump` | `/sys/kernel/kexec_crash_loaded` is 1 | Fedora family: `kdumpctl reset-crashkernel` and the kdump service; Debian family: the `kdump-tools` package | memory reserved for the crash kernel (`kdump.rst`); `kdump-tools` reserves 192 MB on hosts with 512 MB or more (its `/etc/default/grub.d/kdump-tools.cfg`). When pstore keeps the panic text the check is `info`: kdump is a second way, and preflight does not ask for both |
| `mce-bootlog` (x86) | boot logging is on: by default from AMD family 0x11 and Intel Core Yonah, or `mce=bootlog` | `mce=bootlog` | the kernel turns it off on older processors because some BIOS leave stale records in the banks (`amd_apply_global_quirks()`, `intel_apply_global_quirks()`) |
| `mce-print` (x86) | `mce=print_all`, an AMD processor, a kernel without `/dev/mcelog`, or a running mcelog daemon | `mce=print_all` | every machine check record, corrected ones included, is printed at emergency level (`mce_default_notifier()`, `__print_mce()`) |
| `reset-kind` (x86) | information | `reboot=panic_warm` as a candidate | whether a board keeps the banks over a warm reset is not documented by the kernel; the first stop after `my harvest` is installed shows it |
| `bert` | the firmware has a BERT table | none | the region stays readable as root also with `bert_disable`, which only stops the printing |
| `erst-mce` (x86) | information: with an ERST table `mce_panic()` keeps a copy of the record there | none | only a reader of `/dev/mcelog` takes it out, and reading clears it |
| `console` | a serial console on the command line or in `/proc/consoles`, or netconsole | named, no command: the serial log of the BMC, a console server, netconsole | netconsole does not capture the earliest panics of a boot (`netconsole.rst`) |
| `rasdaemon` | `rasdaemon.service` is enabled and runs with `--record` | install and enable rasdaemon | a daemon that stores every traced error in a SQLite database (`rasdaemon(8)`) |
| `harvest` | `my-harvest.service` is enabled and its ledger has 32 MiB free | `my harvest install` | a unit file below `/etc/systemd/system` and a run at every boot |

The paths of `/sys` and `/proc` the checks read were taken from the kernel source that creates them, and each is named in a comment of `internal/preflight`. They were read on the lab VM and on made trees in the tests; no bare-metal host was read.
