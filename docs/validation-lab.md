# Validation in the lab

The adapters were first written against fixtures made by hand from the source code of the kernel and of rasdaemon. This page describes the check against running software: memory errors injected into a Linux kernel with rasdaemon next to it, what each source recorded, and what that changed in the tool.

The captures are in `testdata/real`, one directory per scenario with a `README.md` of its own. The scripts are in `tools/lab`. The tests are in `internal/labcase`.

## The lab

One virtual machine, and guests inside it.

| | Lab host | x86 guest | aarch64 guest |
| --- | --- | --- | --- |
| Machine | VMware guest, 16 CPUs, 48 GB | QEMU 10.2.2, CPU model Skylake-Server (family 6 model 85, ten machine check banks), 2 CPUs, 4 GB; for one capture with an emulated CXL memory device; for the repair capture QEMU 11.1.3 built from its release, with two such devices (10.2.2 emulates no repair) | QEMU 10.2.2 `virt,ras=on`, CPU model neoverse-n1, 8 CPUs, 8 GB as one DIMM |
| System | Fedora 44, systemd 259 | the file system of the lab host, copy-on-write, started with virtme-ng 1.41; no systemd | Fedora 44 cloud image |
| Kernel | 7.3.0 from kernel.org with `MEMORY_FAILURE`, `RAS_CEC`, `RAS_CEC_DEBUG`, `X86_MCE_INJECT`, `X86_MCELOG_LEGACY`, `EDAC_GHES`, `EDAC_DEBUG` and the CXL drivers | the same kernel | 6.19.10-300.fc44.aarch64, as the image ships it |
| rasdaemon | 0.8.0, package `rasdaemon-0.8.0-9.fc44`, started by systemd | the same program, started by hand with a database of its own; for three captures 1.0.0, built from its release tag | 0.8.0, the package, started by systemd |
| What can be injected | what the kernel does with a page | machine check records through the `mce-inject` module; event records of the CXL device through QMP, and with QEMU 11.1 a repair the guest asks for through the kernel's EDAC interface | firmware error records (CPER) through QMP |

The hypervisor shows the lab host no machine check banks, no memory controller and no ACPI error injection table. On the host itself only the last step of error handling can be driven: the kernel is told that a page is bad. Everything else is injected one level down, in guests that QEMU emulates without hardware virtualization, which is not available inside the lab host. The x86 guest boots in a minute, the aarch64 guest in twelve.

QEMU builds the ACPI tables of its guests. For an aarch64 guest with `ras=on` they hold a generic hardware error source, and QEMU can put a record into it from outside. That is the one way the lab gets errors with a DIMM, a row and a column: the kernel handles such a record as it handles one of real firmware, through `ghes` and `ghes_edac`.

## Method

1. A scenario is a script that injects a fixed set of errors and writes down what it injected: `tools/lab/vm.sh` on the lab host, `tools/lab/mce.sh`, `tools/lab/cxl.sh` and `tools/lab/cxl-repair.sh` in the x86 guest, `tools/lab/ghes.sh` for the aarch64 guest. Where the kernel is to act on a page, the address is that of a page a waiting process holds (`tools/lab/hwpoison.c`), so the kernel has something to move or to take away; in `6.19.10-ghes-multi` the page is a free one, checked in `/proc/kpageflags` before.
2. The result is an injection plan, `plan.yaml`: every injection with its time, its address, how many times, and what the kernel is expected to do with the page.
3. `tools/lab/capture.sh` collects `dmesg`, `journalctl -k -o json`, the rasdaemon database, the EDAC tree, `dmidecode -t 17`, the files under `/proc` and `/sys` that name the host, and what `my scan` and `my bundle --anonymize` say on the host as root.
4. `tools/lab/sanitize.py` replaces what names the lab and fails when a part of such a value is left anywhere, also inside the database and the bundle.
5. `internal/labcase` reads every capture three ways: as a copy of the host's files (`my --root`), as loose files (`my import`), and through the bundle written on the host (`my open`). Each time `injection.Compare` looks up every injection of the plan in the ledger. An injection passes when the ledger holds it the planned number of times, counted once however many sources report it, with every field it reports equal to the plan. Records beyond the plan fail the test, except what an earlier boot of the same host left: pages it took offline, and in the bundle of `6.19.10-ghes-clock-step` the errors of the boot before, which the journal of the guest still held.

The plan format and the comparison are in `internal/injection`. `my verify --expect plan.yaml` makes the same comparison from the command line ([verify.md](verify.md)).

## What was injected and what each source recorded

| Capture | Injected | Kernel log | rasdaemon | Ledger |
| --- | --- | --- | --- | --- |
| `7.3.0-baseline` | nothing | 1,486 lines, none about memory | tables without rows | no records |
| `7.3.0-hwpoison` | 4 pages, `madvise(MADV_HWPOISON)` | per page `Injecting memory failure for pfn ...` and `Memory failure: ... recovery action for ...: Recovered` | 4 rows in `memory_failure_event` | 4 pages offline hard, marked as asked for by a program |
| `7.3.0-soft-offline` | 2 pages, `madvise(MADV_SOFT_OFFLINE)`; 1 page through the corrected error collector at its threshold of 1,023 errors; 1 page with 5 errors | `Soft offlining pfn ...` twice, `RAS: Soft-offlining pfn: ...` once; nothing about the fourth page | nothing: the kernel traces no soft offline | 3 pages offline soft; the 4 pages of the boot before, from the database |
| `7.3.0-mce` | 7 corrected and 3 uncorrected machine checks | `Machine check events logged` per record and no record; 2 pages offlined by the collector, 3 by `memory_failure` | 10 rows in `mce_record`, 3 in `memory_failure_event` | CE 7, UE 3, grade B, 5 pages offline |
| `7.3.0-mce-print-all` | the same, kernel booted with `mce=print_all` | every record in three lines, as on a console after a fatal error | the same | the same: each error is in two sources and counts once |
| `7.3.0-mce-clock-step` | the same, clock set back by 10 seconds after rasdaemon started | as before, dated by the kernel's clock | `timestamp` 9 to 10 seconds ahead of `walltime` | the same |
| `7.3.0-mce-fatal` | 1 uncorrected error, processor context corrupt | the record and `Kernel panic - not syncing: Fatal machine check`, on the console only | nothing: the guest was gone | 1 fatal error from `console.log`, and the end of the boot: a stop that names that error |
| `7.3.0-mce-rasdaemon-1.0` | as `7.3.0-mce` | as `7.3.0-mce` | rasdaemon 1.0.0: the same rows in a database of 39 tables, and 3 rows in `signal_event` | the same |
| `7.3.0-cxl` | 9 DRAM event records, 3 general media event records, 1 memory module event record, 2 poison ranges of an emulated CXL device | nothing per record | rasdaemon 1.0.0: 9, 3, 1 and 2 rows in the CXL tables | CE 11, UE 1 on `cxl:mem0`, grade A 9 and B 3, a row fault, "replace now" |
| `7.3.0-cxl-repair` | 6 DRAM event records in one row of each of two CXL devices, a soft PPR of each row from the guest, 3 more records in the repaired row of one device | nothing per record or repair | rasdaemon 1.0.0: 15 rows in `cxl_dram_event`, 2 in `cxl_memory_sparing_event` | CE 15, grade A; `cxl:mem0` 6 errors before its repair and 3 after, R-REP-02, "consider replacement"; `cxl:mem1` 6 before and none after, "observe" |
| `6.19.10-ghes` | 26 firmware error records on one DIMM: a row with 15 corrected errors, 2 in the row next to it, 6 at one cell within a second, 1 with the threshold flag, 2 uncorrected | 21 firmware blocks, 26 EDAC lines, 2 `Memory failure` lines | 26 rows in `mc_event`, none in `memory_failure_event` | CE 24, UE 2 on `DIMM 0`, grade A, a multi-row fault, "replace now" |
| `6.19.10-ghes-clock-step` | 11 firmware error records in the next boot of that guest, after its clock was set back by 10 seconds while rasdaemon ran | 8 firmware blocks, 11 EDAC lines, 1 `Memory failure` line, dated by the kernel's clock | 11 rows in `mc_event`, 10 to 11 seconds ahead of the journal; no record that holds the kernel's time | CE 10, UE 1 on `DIMM 0`; the 11 rasdaemon records moved to the kernel's time |
| `6.19.10-ghes-multi` | 36 firmware error records on four of six DIMMs, over three boots a day apart: 10 in one row and 1 uncorrected found by the scrubber, 10 in one column, 12 scattered over one bank, 3 at one cell | the third boot: 14 firmware blocks, 14 EDAC lines, 1 `Memory failure` line; `journal-all.json` holds the three boots | 36 rows in `mc_event` from the three boots | CE 35, UE 1, grade A; a row, a column and a bank fault, "consider replacement", and a single-bit fault, "observe"; two DIMMs ok |
| `6.19.10-mce-loop` | a fatal machine check at `0x12345040` in each of three boots of a guest with a disk of its own | per panic a pstore dump (ERST) with the record and `Kernel panic - not syncing: Fatal machine check`; the next boot prints the record left in bank 7 | nothing about the errors | 3 boots that stopped, UE 3 tied to no DIMM, one place in 3 of 4 boots |
| `6.19.10-mce-loop-scatter` | the same at three addresses and channels, one per boot | the same | nothing about the errors | 3 boots that stopped, UE 3 at three places |
| `6.19.10-bert-loop` | a boot error region made by hand (one fatal memory error of DIMM 0 with row and column), written by QEMU at every reset; three boots | per boot `BERT: Error records from previous boot:` and the record | nothing | 3 boots that stopped, UE 3 on DIMM 0 at one cell, R-LOOP-01, "replace now" |
| `6.19.10-ghes-loop-spread` | a fatal firmware error record on CPU0_A1, CPU0_C1 and CPU0_E1, one per boot, in an aarch64 guest with six DIMMs | per panic the record and `Kernel panic - not syncing: GHES: Fatal hardware error`, in EFI variables as 16 parts | nothing about the fatal records | 3 boots that stopped, UE 3 on three DIMMs of socket 0, R-LOOP-03 on each, "hold replacement" |
| `6.19.10-mce-loop-kdump` | one fatal machine check with a crash kernel loaded | `vmcore-dmesg.txt` of the crash kernel with the record and the panic; no pstore dump; the next boot prints the record left in bank 7 | nothing about the errors | the boot that panicked stopped, UE 1 |

The x86 hosts have no EDAC memory controller: their EDAC tree is empty. The errors of the machine check captures are therefore of observation grade B (an address and a channel) and belong to no DIMM. The aarch64 guests have the memory controller `ghes_edac` registers, with one DIMM, or six in `6.19.10-ghes-multi`, and their counters. QEMU gives every DIMM it describes the same serial number; `tools/lab/smbios17.py` gives each its own.

## Crash loops

A fatal machine check panics the kernel, and 30 seconds later the kernel restarts the host. The loops of `tools/lab/loop.sh` do that boot after boot in an x86 guest with a disk of its own (`lab-x86`: Fedora 44 cloud image, 4 GiB as one DIMM, 4 CPUs, `mce=print_all`, `systemd-pstore`, `rasdaemon` and `my harvest install` enabled). The guest has an ACPI error record store with a file behind it (`-device acpi-erst`), so the kernel registers `erst` as its pstore backend. Without `-no-reboot` the guest restarts inside the same QEMU process. A plan file says per boot which record goes into which bank (`tools/lab/loop-same.plan`, `loop-scatter.plan`); `loop.sh run` waits for the harvest of each boot, puts the record into the banks with the monitor command `mce -b` and waits for the next boot.

| What the lab showed | Where |
| --- | --- |
| QEMU keeps the contents of the banks over the reset of a guest: `mce_banks` lies after `end_reset_fields` in `CPUX86State`, which `x86_cpu_reset_hold()` leaves alone. The next boot polls the banks and prints the record as `CPU 0: Machine Check: 0 Bank 7: be00000000010090`, without ` Exception`, with `TSC 0` and the time of the poll as `TIME` | `dmesg`, `journal-all.json`, `console.log` of both loops |
| `mce` without `-b` gave a panic on `Timeout: Not all CPUs entered broadcast exception handler` and printed no record at the panic. With `-b` the panic is `Fatal machine check` with the record before it; the broadcast also writes `a000000000000000` into bank 1 of the other CPUs, a record of no memory | the first crash of the lab; `console.log` |
| Each panic left three ERST parts (`dmesg-erst-<id>`, `Panic#1 Part1` to `Part3`, consecutive ids) and the record `mce_panic()` wrote into ERST as `mce-erst-<id>`. systemd 259 put the files at the top of `/var/lib/systemd/pstore` with their times and wrote no `dmesg.txt` | `var/lib/systemd/pstore` |
| The harvest of a boot ran 8.3 s, its phase A ended 4.5 s after the unit started, at 11.1 MiB peak | the journal of the guest, during the first loop |
| With a crash kernel loaded (kdump-utils, `crashkernel=256M`) the panic went to the crash kernel before pstore: `/var/crash/<host>-<time>/` held `vmcore-dmesg.txt` and the crash kernel's own `kexec-dmesg.log`, and no `dmesg-erst-*` was written; `mce_panic()` had written `mce-erst-<id>` before. The bank kept the record through the crash kernel and the reset | `6.19.10-mce-loop-kdump` |

No firmware of the lab writes a boot error region, so `tools/lab/bertregion` makes one: a Generic Error Status Block with a Platform Memory section, which QEMU writes into the guest at every reset (`-device loader`) and names in a BERT table (`-acpitable sig=BERT`). The guest keeps the page out of its RAM (`memmap=4K$ADDR`; GRUB needs `\$` in the entry) so that `bert_init()` can map it. The kernel printed the record at every boot, cleared its block status, and left the region in `/sys/firmware/acpi/tables/data/BERT` (`6.19.10-bert-loop`).

The guest names no DIMM for a machine check: the errors of the machine check loops are the host's. In `6.19.10-mce-loop` the host finding says that one address had uncorrected errors in 3 of the last 4 boots; in `6.19.10-mce-loop-scatter` it says nothing of the kind. R-LOOP-01, R-LOOP-02 and R-LOOP-03 need records that name a DIMM; the region of `6.19.10-bert-loop` names one, and R-LOOP-01 fires on it. The aarch64 loop of `tools/lab/loop-arm.sh` (`6.19.10-ghes-loop-spread`) has the firmware error records of lane B's guest with six DIMMs: one fatal record on another DIMM in each of three boots, and R-LOOP-03 fires on all three with "these DIMMs are all on socket 0, on more than one channel".

## Boards

`tools/lab/boards.sh` boots x86 guests whose SMBIOS memory devices carry slot names that no public dump under `testdata/explorer` has, and runs `capture.sh` in each with nothing injected. `smbios17.py` writes one structure per slot: as many populated slots of 16 GiB as QEMU describes for the guest's memory, then the empty ones. Each guest has a disk of its own over one disk prepared once from the Fedora 44 cloud image with `numactl`, `sysstat`, `lshw` and `dmidecode`. Before the capture `sa1 10 6` takes six samples ten seconds apart, so that `sar -B` has a table.

| Capture | Guest | Slots | What the board draws |
| --- | --- | --- | --- |
| `6.19.10-board-two-sockets` | 2 sockets of 2 cores, 2 NUMA nodes of 32 GiB | `CPU0_CH0_DIMM0` to `CPU1_CH1_DIMM1`, the `DIMM0` slots populated | 2 CPUs with 2 channels each, every slot placed |
| `6.19.10-board-digits` | 1 socket, 2 CPUs, 32 GiB | `DIMM000` and `DIMM010` populated, `DIMM001` and `DIMM011` empty | 1 CPU with 2 channels, every slot placed |
| `6.19.10-board-letters` | 1 socket, 2 CPUs, 32 GiB | `DIMMA1` and `DIMMB1` populated, `DIMMA2` and `DIMMB2` empty | 1 CPU with 2 channels, every slot placed |

| What the guests showed | Where |
| --- | --- |
| `lshw -c memory -c processor` did not answer within the 15 seconds `my collect host` gives it; run again without the limit it took 16.6 seconds in `6.19.10-board-digits` and 16.6 seconds in `6.19.10-board-letters`. The board then takes the cache sizes from sysfs | `collect.json`, `extra/lshw-seconds.txt` |
| `numastat` printed one column per node and `sar -B` a 12-hour clock with `AM` and `PM`, as the hand-written fixtures `made-two-nodes` and `made-sar-12h` assumed | `numastat.txt`, `sar-B.txt` |
| The memory array QEMU makes (type 16) keeps its own device count when the type 17 structures come from files: `Number Of Devices: 1` beside 8 memory devices | `extra/dmidecode-all.txt` |
| The journal filed every kernel message of the boot under `localhost` or the name of the prepared disk, `lab-prep`: cloud-init renamed the guest after the last one. An import of the journal alone names the host `lab-prep` | `journal-k.json` |
| Three guests booted at once on the lab host were so slow that systemd stopped waiting for the disk of `/home`, and two of them stayed in emergency mode. `boards.sh` runs one guest at a time | the consoles of the first attempt |

## What the captures changed

Each of these was wrong or missing before the capture showed it.

- `dmidecode` 3.7 prints `Size: 16 GiB`. The reader accepted `GB` only and saw no populated memory device in such an output.
- rasdaemon dates a record by the uptime of its trace event plus an offset it measured when it started. chronyd set the clock of the lab host back by 6.7 to 10.8 seconds on seven boots, a few seconds after rasdaemon had started. The kernel log dates the same errors by the kernel's clock, and two records more than 5 seconds apart were not taken for one error: every error of such a boot counted twice. A machine check record is now dated by the time the kernel put into it, and the rows of other tables next to it move by the same amount. A database without machine check records, as on the aarch64 guest, is compared with the kernel log of the same boot: the difference in time that the errors in both agree on is the step. On that guest with its clock set back, the tool had said "CE 18, UE 2" for 10 and 1.
- The journal files the first kernel messages of a boot, 1,318 of 1,486 on the lab host, under the host name `localhost`. An import of a journal export was stored under that name. The name now comes from the end of the file.
- `journalctl -o short-monotonic` puts `host kernel:` between the stamp and the message. The boot banner was not recognised behind it.
- The kernel announces a page that a program asked it to give up (`Injecting memory failure for pfn ...`). Such a page is now marked in the ledger and counted in the source report. A boot in which `mce-inject` or EINJ was set up is named.
- With one day on record, the timeline drew one letter where an uncorrected error, the offline of its page and a reboot share a column, and dropped the other kinds with their text.
- Six corrected errors at one cell within a second are six equal lines in the kernel log. In the forms that date a line to the second (`dmesg -T`, syslog) they had equal stamps too and were stored as two records.
- The kernel prints the firmware block of a corrected error at most twice in five seconds, and the EDAC line of every error. Of six such errors the log has one block and six EDAC lines; the second line was taken for another report of the block, because the first, which the adapter had already tied to the block, did not count as the block's EDAC line.
- A CXL device answers a repair with a memory sparing event record that says how many spares are left. The reader took every such record for sparing that used the last spare, and R-REP-01 graded both devices of `7.3.0-cxl-repair` "replace now" although each had 2 spares left; no screen said what happened at the repaired row afterwards. Such a record is now memory sparing, a repair of the row it names, and the card lists each repair with the errors before and after it.
- A CXL device reports an error as a DRAM event record or as a general media event record. Records of the two tables that followed each other within 5 seconds were taken for one error: "CE 8" for 11.
- An anonymized bundle kept `root=UUID=...` of the kernel command line.
- A copy of a host's files was dated by the newest of its files. A copy that lost the times of its files, as every checkout of `testdata/real` does, is now dated by its own boot time and uptime.
- A pstore dump holds the end of the kernel log, without the boot banner, so the kernel log reader made no boot of it. It now gets one, placed by the time in a machine check record, by the RTC line, or by the time of the dump less the stamp of its last line.
- The record `mce_panic()` writes into ERST shows up in pstore as `mce-erst-<id>` and is archived by systemd-pstore. `my preflight` said that only a reader of `/dev/mcelog` takes it out.
- The `TIME` of a record that a boot read out of a bank was taken for its time. The kdump guest read the bank at 04:56:35 by its clock, one second before the start of that boot as the ledger computed it once the clock was set, and the boot pass filed the record under the boot before as the reading boot: the boot that had panicked read "panic" and the boot before it "stop". A carried record now keeps the time of its line and the boot its source gave it.
- The reader of the boot error region took a Generic Error Data Entry for 72 bytes (80 from revision 0x300), with the FRU text and the time stamp 8 bytes late. Its tests built their regions with a builder of the same layout and passed. The kernel of the lab refused a region from that builder (`BERT: [Firmware Bug]: Invalid error record`) and printed one with the layout of `struct acpi_hest_generic_data` (64 bytes, 72 from revision 0x300). A test now lays an entry out by hand from that struct.
- The parts of one EFI pstore dump were grouped by the second in their id. Each part carries the time it was written (`pstore_record_init()` per part in `pstore_dump()`), the 16 parts of one panic in the aarch64 guest spanned three seconds, systemd-pstore filed them under three directories, and the reader made three dumps and three boots of one panic: 15 boots for a loop of 5. A dump is now a run of parts from Part 1 up across the directories of one archive.
- Linux 6.19 panics on a fatal firmware error with the panic timeout as it is (`__ghes_panic()`, `drivers/acpi/apei/ghes.c`): the aarch64 guest printed `GHES: Fatal hardware error but panic disabled` and stayed down. An x86 host restarts 30 s after a fatal machine check; a host that gets its errors from firmware does not, unless `panic=` is set or a watchdog or the BMC restarts it. `my preflight` now says so on such a host.
- A host whose errors cannot be tied to a DIMM had no finding for them: findings were made per DIMM. On `7.3.0-mce` the tool said "ok", with exit code 0, next to "CE 7, UE 3", and did not mention the 5 pages. The host finding now speaks about such records (rules R-HOST-01 and R-HOST-02): the capture reads `host finding: hold replacement`, the exit code is 4, and `my why` shows the 3 uncorrected errors, the 7 corrected errors and the 5 pages with the records behind them. The captures of pages taken offline alone (`7.3.0-hwpoison`, `7.3.0-soft-offline`) read "observe". The files under `my/` of these captures are what the tool printed when they were made: "ok" and exit code 0.

## What the kernel and rasdaemon do that the hand-written fixtures did not show

- rasdaemon stores the status register of a machine check as a signed number. The package of Fedora 44 creates seven tables and none for CXL; its `mce_record` has no `ppin`, `microcode`, `ipid` or `synd` column.
- rasdaemon 0.8.0 names the page type of a memory failure from a table of its own, indexed by a kernel enum that lost entries since. On kernel 7.3 it writes `dirty unevictable LRU page` where the kernel says `dirty LRU page`. The result (`Recovered`) is right. rasdaemon 1.0.0 names the type as the kernel does, and stores the signals the kernel sends for poisoned pages in a table of their own.
- The aarch64 kernel 6.19.10 of Fedora has no `ras:memory_failure_event` trace point. rasdaemon stores no page offline there; the kernel log is the only source.
- A soft offline that firmware asks for with the flag "error threshold exceeded" leaves nothing at all: no line in the kernel log, no trace event. The content of the page was found at another page frame afterwards.
- `ghes_edac` labels a DIMM with its bank locator and its device locator (`BANK 0 DIMM 0` for the device `DIMM 0`), and prints row and column in decimal under the names `bank_group` and `bank_address`.
- The kernel prints nothing for a CXL event record. The records are in the trace only, so a host without rasdaemon has no trace of them. The same holds for a repair asked for through `/sys/bus/edac/devices/cxl_memN/mem_repairX`: the only record of it is the memory sparing event record of the device.
- Kernel 7.3 registers a soft PPR control and four sparing controls per CXL device, and no hard PPR control. QEMU 11.1.3 reports a PPR in the maintenance class of sparing with the subclass of cacheline sparing, and a sparing asked for directly produces no record at all.
- A soft offline that succeeds leaves one line in the kernel log, the request. There is no line for the outcome and no trace event.
- The corrected error collector takes a page offline at the second corrected error on an Intel processor. On the lab host, with an AMD processor, its threshold is 1,023.
- With `CONFIG_X86_MCELOG_LEGACY`, as in the lab kernel, a logged machine check is not printed: the kernel log says `Machine check events logged` and nothing else. The record is printed with `mce=print_all`, and on the console when the kernel panics.
- An uncorrected error that needs no action (UCNA) costs its page like one that needs action.
- `CPU0 BANK7 CMCI storm detected` and `... subsided` carry no `mce:` prefix. `MCE: Killing <program> due to hardware memory corruption fault at ...` is logged when a program touches a poisoned page.
- The journal of one boot holds two machine IDs, the one of the initrd and the one of the installed system, and `_SOURCE_MONOTONIC_TIMESTAMP` is `0` for the first messages of a boot.
- Three clocks date one error on the aarch64 guest, and they agree to a second or two at best. In `6.19.10-ghes`, where no clock was set, the rows of rasdaemon carry whole seconds that lie 0.7 to 2.2 seconds before the stamps of the journal. In `6.19.10-ghes-clock-step` the boot time in `/proc/stat` plus the stamp of a kernel log line gives a time 1.2 seconds after the journal's stamp of that line, 30 minutes into the boot. Records of one error up to 2 seconds apart are therefore left where they are.
- systemd writes the UUID of the root file system into unit names as `1b2c3d4e\x2d5f60...`, with `\x2d` for each dash, and `/dev/kmsg` escapes the backslash once more. A search for the plain UUID finds neither.

## What the lab cannot exercise

- An EDAC driver of a real memory controller (`skx_edac`, `i10nm_edac`, `amd64_edac`): no hardware and no emulation of it. The labels, rows and columns these drivers decode are covered by hand-written fixtures only. Row, column and DIMM come from firmware error records in the lab, through `ghes_edac`.
- Machine check banks of real hardware: corrected error interrupts, whether a board keeps a record in its bank across the reset after a fatal error (QEMU does), the decoding of AMD processors (their memory banks lie above the ten banks QEMU emulates).
- The boot error region of real firmware, and an ERST or EFI variable store of real firmware under repeated panics; the entries a BMC makes about a crash.
- ACPI error injection (EINJ), and firmware-first handling on x86: QEMU builds a firmware error source for aarch64 guests only.
- A BMC, a GPU, repairs by the BIOS or a BMC. The CXL devices are emulated: their records and repairs are real to the kernel, their memory cells are not.
- Time: every capture spans minutes. Trends over days, the 30 day baseline and a database that grew for months are not in them.
- Other versions: two kernels (7.3.0 on x86, 6.19.10 on aarch64) and two versions of rasdaemon (0.8.0 as packaged, 1.0.0).

## Open points

The captures raised these and did not settle them.

- A page that a program asked the kernel to give up is marked, and still counts like any other offline. The host finding says how many of its pages were asked for and loses confidence when all of them were; its grade stays "observe".

## Repeating it

On a host with root access, a kernel built with the options above, rasdaemon, QEMU and virtme-ng:

```
LAB=/root/my-lab
mkdir -p $LAB/bin $LAB/tools && cp tools/lab/* $LAB/tools/
make cross && cp dist/my-linux-amd64 $LAB/bin/my
gcc -O2 -o $LAB/bin/hwpoison tools/lab/hwpoison.c

$LAB/tools/vm.sh baseline        # then: hwpoison, soft
$LAB/tools/mce.sh                # then: print-all, clock-step, fatal, rasdaemon-1.0
$LAB/tools/cxl.sh                # needs rasdaemon with CXL support at $RASDAEMON
$LAB/tools/cxl-repair.sh         # also QEMU 11.1 or later at $QEMU (default $LAB/qemu/bin), see the script
$LAB/tools/ghes.sh image         # once, from a Fedora cloud image for aarch64 under $LAB/img
$LAB/tools/ghes.sh boot && $LAB/tools/ghes.sh prepare && $LAB/tools/ghes.sh run
$LAB/tools/ghes.sh clock-step    # in a boot of its own
$LAB/tools/ghes.sh multi         # another guest: six DIMMs, three boots a day apart
LOOP=$LAB/loop $LAB/tools/loop.sh image && $LAB/tools/loop.sh boot && $LAB/tools/loop.sh prepare
$LAB/tools/loop.sh run $LAB/tools/loop-same.plan && $LAB/tools/loop.sh capture same
$LAB/tools/loop.sh clean && $LAB/tools/loop.sh run $LAB/tools/loop-scatter.plan && $LAB/tools/loop.sh capture scatter
$LAB/tools/loop.sh kdump && $LAB/tools/loop.sh run $LAB/tools/loop-kdump.plan && $LAB/tools/loop.sh capture kdump
go run ./tools/lab/bertregion -o $LAB/loop/x86/bert && $LAB/tools/loop.sh stop
BERT=$LAB/loop/x86/bert $LAB/tools/loop.sh boot && $LAB/tools/loop.sh bert    # then reboot the guest and capture bert
$LAB/tools/loop-arm.sh disk && $LAB/tools/loop-arm.sh boot && $LAB/tools/loop-arm.sh prepare
$LAB/tools/loop-arm.sh run $LAB/tools/loop-arm-spread.plan && $LAB/tools/loop-arm.sh capture spread    # set kernel.panic=30 first: see 6.19.10-ghes-loop-spread
$LAB/tools/boards.sh image && $LAB/tools/boards.sh prepare
for g in two-sockets digits letters; do $LAB/tools/boards.sh boot $g && $LAB/tools/boards.sh collect $g && $LAB/tools/boards.sh stop $g; done
python3 tools/lab/sanitize.py $LAB/cap/mce testdata/real/7.3.0-mce --replace <build host>=lab-vm
```

`vm.sh hwpoison` and `vm.sh soft` take pages of the host itself offline until the next reboot, a few kilobytes each time. Everything else happens inside a guest.
