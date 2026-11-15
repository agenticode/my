# Validation in the lab

The adapters were first written against fixtures made by hand from the source code of the kernel and of rasdaemon. This page describes the check against running software: memory errors injected into a Linux kernel with rasdaemon next to it, what each source recorded, and what that changed in the tool.

The captures are in `testdata/real`, one directory per scenario with a `README.md` of its own. The scripts are in `tools/lab`. The tests are in `internal/labcase`.

## The lab

One virtual machine, and guests inside it.

| | Lab host | x86 guest | aarch64 guest |
| --- | --- | --- | --- |
| Machine | VMware guest, 16 CPUs, 48 GB | QEMU 10.2.2, CPU model Skylake-Server (family 6 model 85, ten machine check banks), 2 CPUs, 4 GB; for one capture with an emulated CXL memory device | QEMU 10.2.2 `virt,ras=on`, CPU model neoverse-n1, 8 CPUs, 8 GB as one DIMM |
| System | Fedora 44, systemd 259 | the file system of the lab host, copy-on-write, started with virtme-ng 1.41; no systemd | Fedora 44 cloud image |
| Kernel | 7.3.0 from kernel.org with `MEMORY_FAILURE`, `RAS_CEC`, `RAS_CEC_DEBUG`, `X86_MCE_INJECT`, `X86_MCELOG_LEGACY`, `EDAC_GHES`, `EDAC_DEBUG` and the CXL drivers | the same kernel | 6.19.10-300.fc44.aarch64, as the image ships it |
| rasdaemon | 0.8.0, package `rasdaemon-0.8.0-9.fc44`, started by systemd | the same program, started by hand with a database of its own; for two captures 1.0.0, built from its release tag | 0.8.0, the package, started by systemd |
| What can be injected | what the kernel does with a page | machine check records through the `mce-inject` module; event records of the CXL device through QMP | firmware error records (CPER) through QMP |

The hypervisor shows the lab host no machine check banks, no memory controller and no ACPI error injection table. On the host itself only the last step of error handling can be driven: the kernel is told that a page is bad. Everything else is injected one level down, in guests that QEMU emulates without hardware virtualization, which is not available inside the lab host. The x86 guest boots in a minute, the aarch64 guest in twelve.

QEMU builds the ACPI tables of its guests. For an aarch64 guest with `ras=on` they hold a generic hardware error source, and QEMU can put a record into it from outside. That is the one way the lab gets errors with a DIMM, a row and a column: the kernel handles such a record as it handles one of real firmware, through `ghes` and `ghes_edac`.

## Method

1. A scenario is a script that injects a fixed set of errors and writes down what it injected: `tools/lab/vm.sh` on the lab host, `tools/lab/mce.sh` and `tools/lab/cxl.sh` in the x86 guest, `tools/lab/ghes.sh` for the aarch64 guest. Where the kernel is to act on a page, the address is that of a page a waiting process holds (`tools/lab/hwpoison.c`), so the kernel has something to move or to take away.
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
| `7.3.0-mce-fatal` | 1 uncorrected error, processor context corrupt | the record and `Kernel panic - not syncing: Fatal machine check`, on the console only | nothing: the guest was gone | 1 fatal error from `console.log` |
| `7.3.0-mce-rasdaemon-1.0` | as `7.3.0-mce` | as `7.3.0-mce` | rasdaemon 1.0.0: the same rows in a database of 39 tables, and 3 rows in `signal_event` | the same |
| `7.3.0-cxl` | 9 DRAM event records, 3 general media event records, 1 memory module event record, 2 poison ranges of an emulated CXL device | nothing per record | rasdaemon 1.0.0: 9, 3, 1 and 2 rows in the CXL tables | CE 11, UE 1 on `cxl:mem0`, grade A 9 and B 3, a row fault, "replace now" |
| `6.19.10-ghes` | 26 firmware error records on one DIMM: a row with 15 corrected errors, 2 in the row next to it, 6 at one cell within a second, 1 with the threshold flag, 2 uncorrected | 21 firmware blocks, 26 EDAC lines, 2 `Memory failure` lines | 26 rows in `mc_event`, none in `memory_failure_event` | CE 24, UE 2 on `DIMM 0`, grade A, a multi-row fault, "replace now" |
| `6.19.10-ghes-clock-step` | 11 firmware error records in the next boot of that guest, after its clock was set back by 10 seconds while rasdaemon ran | 8 firmware blocks, 11 EDAC lines, 1 `Memory failure` line, dated by the kernel's clock | 11 rows in `mc_event`, 10 to 11 seconds ahead of the journal; no record that holds the kernel's time | CE 10, UE 1 on `DIMM 0`; the 11 rasdaemon records moved to the kernel's time |

The x86 hosts have no EDAC memory controller: their EDAC tree is empty. The errors of the machine check captures are therefore of observation grade B (an address and a channel) and belong to no DIMM. The aarch64 guest has the memory controller `ghes_edac` registers, with its one DIMM and its counters.

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
- A CXL device reports an error as a DRAM event record or as a general media event record. Records of the two tables that followed each other within 5 seconds were taken for one error: "CE 8" for 11.
- An anonymized bundle kept `root=UUID=...` of the kernel command line.
- A copy of a host's files was dated by the newest of its files. A copy that lost the times of its files, as every checkout of `testdata/real` does, is now dated by its own boot time and uptime.
- A host whose errors cannot be tied to a DIMM had no finding for them: findings were made per DIMM. On `7.3.0-mce` the tool said "ok", with exit code 0, next to "CE 7, UE 3", and did not mention the 5 pages. The host finding now speaks about such records (rules R-HOST-01 and R-HOST-02): the capture reads `host finding: hold replacement`, the exit code is 4, and `my why` shows the 3 uncorrected errors, the 7 corrected errors and the 5 pages with the records behind them. The captures of pages taken offline alone (`7.3.0-hwpoison`, `7.3.0-soft-offline`) read "observe". The files under `my/` of these captures are what the tool printed when they were made: "ok" and exit code 0.

## What the kernel and rasdaemon do that the hand-written fixtures did not show

- rasdaemon stores the status register of a machine check as a signed number. The package of Fedora 44 creates seven tables and none for CXL; its `mce_record` has no `ppin`, `microcode`, `ipid` or `synd` column.
- rasdaemon 0.8.0 names the page type of a memory failure from a table of its own, indexed by a kernel enum that lost entries since. On kernel 7.3 it writes `dirty unevictable LRU page` where the kernel says `dirty LRU page`. The result (`Recovered`) is right. rasdaemon 1.0.0 names the type as the kernel does, and stores the signals the kernel sends for poisoned pages in a table of their own.
- The aarch64 kernel 6.19.10 of Fedora has no `ras:memory_failure_event` trace point. rasdaemon stores no page offline there; the kernel log is the only source.
- A soft offline that firmware asks for with the flag "error threshold exceeded" leaves nothing at all: no line in the kernel log, no trace event. The content of the page was found at another page frame afterwards.
- `ghes_edac` labels a DIMM with its bank locator and its device locator (`BANK 0 DIMM 0` for the device `DIMM 0`), and prints row and column in decimal under the names `bank_group` and `bank_address`.
- The kernel prints nothing for a CXL event record. The records are in the trace only, so a host without rasdaemon has no trace of them.
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
- Machine check banks of real hardware: corrected error interrupts, a record that stays in its bank across a reset and is logged by the next boot, the decoding of AMD processors (their memory banks lie above the ten banks QEMU emulates).
- ACPI error injection (EINJ), and firmware-first handling on x86: QEMU builds a firmware error source for aarch64 guests only.
- A BMC, a GPU, repairs by firmware. The CXL device is emulated: its records are real to the kernel, its memory cells are not.
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
$LAB/tools/ghes.sh image         # once, from a Fedora cloud image for aarch64 under $LAB/img
$LAB/tools/ghes.sh boot && $LAB/tools/ghes.sh prepare && $LAB/tools/ghes.sh run
$LAB/tools/ghes.sh clock-step    # in a boot of its own
python3 tools/lab/sanitize.py $LAB/cap/mce testdata/real/7.3.0-mce --replace <build host>=lab-vm
```

`vm.sh hwpoison` and `vm.sh soft` take pages of the host itself offline until the next reboot, a few kilobytes each time. Everything else happens inside a guest.
