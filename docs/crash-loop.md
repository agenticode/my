# Crash loops: what a reset loses

A fatal uncorrected memory error panics the kernel. From that moment no program runs: journald writes nothing after the last lines it had written to disk, and rasdaemon stores no more records. On x86 the kernel then restarts the host by itself. When boot logging of machine checks is on, which is the default for current processors, `mca_bsp_init()` sets `mca_cfg.panic_timeout` to 30, and `mce_panic()` uses it when the panic timeout of the kernel is 0 (`arch/x86/kernel/cpu/mce/core.c`). A host with `panic=0` therefore restarts 30 seconds after a fatal machine check.

MemorY run after such a restart finds no record of the error in the journal or in rasdaemon's database. If the fault is hard, the next boot hits it again and loses its evidence the same way. `testdata/real/7.3.0-mce-fatal` is that case: only the serial console of the guest shows the error.

Some records do survive a reset:

| What survives | Written by | Read back by |
| --- | --- | --- |
| the contents of the machine check banks, after a reset that keeps power | the processor | the next boot polls every bank when boot logging is on (`__mcheck_cpu_init_prepare_banks()`), and prints what it finds when nothing else handled it |
| the panic text in pstore | `pstore_dump()` into EFI variables, the firmware's error record store (ERST) or reserved RAM | `/sys/fs/pstore/dmesg-<backend>-<id>`; `systemd-pstore` moves the files to `/var/lib/systemd/pstore` |
| the machine check record in ERST | `mce_panic()` through `apei_write_mce()` | a reader of `/dev/mcelog`, and the read clears it; with ERST as the pstore backend also pstore, as `mce-erst-<id>` (`erst_reader()`), which `systemd-pstore` archives. MemorY reads neither |
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
| `reset-kind` (another architecture with a firmware error source, `panic=0`) | information | none | a fatal firmware error leaves the host down: `__ghes_panic()` panics with the timeout as it is (`drivers/acpi/apei/ghes.c`); seen in the lab |
| `bert` | the firmware has a BERT table | none | the region stays readable as root also with `bert_disable`, which only stops the printing |
| `erst-mce` (x86) | information: with an ERST table `mce_panic()` keeps a copy of the record there | none | only a reader of `/dev/mcelog` takes it out, and reading clears it |
| `console` | a serial console on the command line or in `/proc/consoles`, or netconsole | named, no command: the serial log of the BMC, a console server, netconsole | netconsole does not capture the earliest panics of a boot (`netconsole.rst`) |
| `rasdaemon` | `rasdaemon.service` is enabled and runs with `--record` | install and enable rasdaemon | a daemon that stores every traced error in a SQLite database (`rasdaemon(8)`) |
| `harvest` | `my-harvest.service` is enabled and its ledger has 32 MiB free | `my harvest install` | a unit file below `/etc/systemd/system` and a run at every boot |

The paths of `/sys` and `/proc` the checks read were taken from the kernel source that creates them, and each is named in a comment of `internal/preflight`. They were read on the lab VM and on made trees in the tests; no bare-metal host was read.

## my harvest

`my harvest` is a scan of the running host into a kept ledger, `/var/lib/my/ledger.db` unless `--ledger` names another, made for the moment after a reset. It reads in two phases:

| Phase | Sources | Then |
| --- | --- | --- |
| A | the host facts of this boot, `/sys/fs/pstore`, `/var/lib/systemd/pstore`, the boot error region, the kdump text logs below `/var/crash`, and `/dev/kmsg`, which holds the first lines of this boot: the records the kernel found in the machine check banks and the lines it printed of the boot error region | a checkpoint of the ledger: the ledger runs with `synchronous(NORMAL)`, which syncs the write-ahead log only at a checkpoint (`PRAGMA synchronous`, sqlite.org), so without one a reset seconds later could take phase A with it |
| B | the journal, rasdaemon, the EDAC counters, the mcelog daemon, SMBIOS, the GPUs | the ledger passes: boots, carried records, duplicates, how each boot ended |

Each phase writes one line to standard error: the sources it read, the records it added, the seconds since the start. Under systemd the journal keeps the lines.

- One run at a time: a run takes a lock on `<ledger>.lock`; a second run ends at once with exit code 0 and says so.
- The journal is read from the start of the boot during which the last finished run ran; the lines before are in the ledger. Without a finished run it is read in full.
- A run that added no record removes its scan entry, its source inventory and the counter readings it took, and records the time of the run under `harvest.last_run` in `meta`. A ledger read at every boot does not grow by a scan entry per boot.
- Below 32 MiB of free space on the file system of the ledger only phase A is read. The floor is a choice; nothing was measured.
- A ledger that cannot be written (a read-only file system, a full one) ends the run with exit code 70 and the reason. SQLite rolls back the transaction that failed; what earlier transactions stored stays, and nothing else is written anywhere.
- `--deadline` (10 minutes) bounds the reading. Sources not read by then are named, the passes run, and the exit code is 0.
- A run that a reset cut leaves a scan entry without a finish time, and the passes did not run after it. `my harvest status` and `my open` on the ledger say that totals may count an error twice until a run completes.

The harvest asks no BMC and has no network flags.

### Install

```
sudo install -m 0755 my /usr/local/bin/my
sudo my harvest install --timer --dry-run
sudo my harvest install --timer
```

`my harvest install` is the one command of MemorY that changes the host. It writes `/etc/systemd/system/my-harvest.service`, and with `--timer` `/etc/systemd/system/my-harvest.timer`, runs `systemctl daemon-reload` and `systemctl enable` for what it wrote, and with `--now` starts the service. It prints every path and command before it acts; `--dry-run` prints them and stops. It refuses when it does not run as root, when systemd is not the init system (`/run/systemd/system` is not there), when the binary lies below `/home`, `/root`, `/tmp`, `/run` or `/var/tmp`, is not owned by root, or can be written by its group or by others: the unit runs it as root at every boot. It refuses a `--ledger` outside `/var/lib/my`, the one directory the unit may write to (`ProtectSystem=strict`, `StateDirectory=my`), and a binary or ledger path with a space, a quote, a backslash, `;`, `%` or `$`, which `ExecStart=` would split or expand. A unit file of that name that does not start with the line `# Written by "my harvest install"` is left alone, and nothing is installed.

`my harvest uninstall` runs `systemctl disable --now` for the units it wrote, removes them and runs `systemctl daemon-reload`. The ledger stays, and the command prints its path. `my harvest status` says whether the units are installed and enabled, the size of the ledger and the boots it holds, and when the last run was.

The same unit files are in [contrib/systemd](../contrib/systemd) for a person who installs them by hand; a test keeps them equal to what install writes. The service:

| Setting | Why |
| --- | --- |
| `Type=oneshot`, `WantedBy=multi-user.target` | one run at every boot |
| `After=local-fs.target systemd-journal-flush.service systemd-pstore.service rasdaemon.service` | after the journal is on disk and systemd-pstore has archived the dumps; ordering only, no `Requires=`, so the harvest runs on a host without any of them. Nothing is ordered after it, so it holds up no boot |
| root with `CapabilityBoundingSet=CAP_DAC_READ_SEARCH CAP_SYSLOG` | the boot error region and `/sys/fs/pstore` are readable by root only; with two capabilities and nothing else |
| `StateDirectory=my`, `ProtectSystem=strict`, `ProtectHome=yes`, `PrivateTmp=yes` | the ledger directory is the one place it can write |
| `PrivateNetwork=yes`, `RestrictAddressFamilies=AF_UNIX` | no network |
| `Nice=10`, `IOSchedulingClass=idle`, `TimeoutStartSec=15min` | it gives way to the services of the host |

`my-harvest.timer` (`OnBootSec=15min`, `OnUnitActiveSec=1h`) runs the harvest again during a long boot, so that counter readings and corrected errors of that boot are in the ledger before a reset. In a loop of short boots the run at boot is the one that matters.

`my open /var/lib/my/ledger.db` shows the kept ledger with every command, and the overview of `my` names `my open /var/lib/my/ledger.db boots` when the ledger is there.

## my boots

`my boots` draws the latest boots of the host, at most `loop.window_boots` (16): when each started, how long it ran, how it ended (`STOP` when a record shows a fatal hardware error in it), and per DIMM what it showed in each boot. `my boots <slot>` draws the places of one DIMM boot by boot. [reading-guide.md](reading-guide.md#boots) explains the screens with the example `examples/crash-loop.mycase`, and [rules.md](rules.md#boots) the rules that read them:

| Rule | Grade | Fires when |
| --- | --- | --- |
| R-LOOP-01 | replace now | uncorrected errors at one place of a DIMM in at least `rule.loop.min_boots` (2) boots |
| R-LOOP-02 | replace now | uncorrected errors on one DIMM in as many boots, at no place that repeats |
| R-LOOP-03 | hold replacement | uncorrected errors of at least `rule.loop.scatter_boots` (3) boots spread over DIMMs, none with more than `rule.loop.scatter_share` (0.5) of them |

The thresholds are defaults that were compared with no field data.

What MemorY never touches: kernel parameters, boot loader files, sysctls, files in `/sys` and `/proc`, the configuration of journald, systemd-pstore, kdump and rasdaemon, EFI variables, pstore files (removing one erases the record in the backend), the ERST and the boot error region. It never opens `/dev/mcelog`: a read hands the record over and clears it from ERST and from the kernel's buffer (`__mce_read_apei()`).


## What the lab shows, and what it does not

The lab of [validation-lab.md](validation-lab.md) runs crash loops in guests that QEMU emulates. What they showed:

- QEMU keeps the contents of the machine check banks over the reset of the guest, and the next boot prints the record left in the bank (`TSC 0`, the time of the poll as `TIME`). With `mce=print_all` the line is in the kernel log of every such boot. `testdata/real/6.19.10-mce-loop` and `6.19.10-mce-loop-scatter` hold three such boots each.
- With an ACPI error record store as the pstore backend, each panic left the end of the kernel log in three parts and the record of `mce_panic()`; systemd-pstore 259 archived both at the next boot.
- `my harvest` ran at every boot of a loop, kept what each boot left in `/var/lib/my/ledger.db`, and `my boots` of that ledger shows the stops (`testdata/real/*-loop*/var/lib/my/ledger.db`).
- With a crash kernel loaded, the panic went to kdump and not to pstore, as `my preflight` warns (`pstore-vs-kdump`); `vmcore-dmesg.txt` held the record, and the next boot still printed it from the bank (`testdata/real/6.19.10-mce-loop-kdump`).
- On aarch64 with a firmware error source, a fatal record panicked the kernel and the guest stayed down: `__ghes_panic()` leaves the panic timeout at 0. Such a host needs `panic=N`, a watchdog or the BMC to come back.

What the lab cannot show, and what MemorY has therefore not been checked against:

- whether a real board keeps a record in its banks over the reset after a fatal error; `my preflight` says so as `info` and names the first stop after `my harvest install` as the test;
- the boot error region of real firmware (the lab's region is made by hand, and the kernel read it), and an ERST or EFI variable store of real firmware under repeated panics;
- the entries a BMC makes about a crash, and its serial log;
- machine check records that name a DIMM: the guest has no EDAC driver for its memory controller, so the machine check loops of `lab-x86` give the host finding. R-LOOP-01 fired in the lab on the boot error region made by hand, which names a DIMM (`testdata/real/6.19.10-bert-loop`), and R-LOOP-03 on the firmware error records of an aarch64 guest with six DIMMs (`6.19.10-ghes-loop-spread`); the rules are also tested on synthetic ledgers (`internal/engine`) and on `examples/crash-loop.mycase`.
