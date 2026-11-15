# Validation in the lab

The adapters were first written against fixtures made by hand from the source code of the kernel and of rasdaemon. This page describes the check against running software: memory errors injected into a Linux kernel with rasdaemon next to it, what each source recorded, and what that changed in the tool.

The captures are in `testdata/real`, one directory per scenario with a `README.md` of its own. The scripts are in `tools/lab`. The tests are in `internal/labcase`.

## The lab

One virtual machine, and guests inside it.

| | Lab host | x86 guest |
| --- | --- | --- |
| Machine | VMware guest, 16 CPUs, 48 GB | QEMU 10.2.2 without hardware virtualization, CPU model Skylake-Server (family 6 model 85, ten machine check banks), 2 CPUs, 4 GB |
| System | Fedora 44, systemd 259 | the file system of the lab host, copy-on-write, started with virtme-ng 1.41; no systemd |
| Kernel | 7.3.0 from kernel.org with `MEMORY_FAILURE`, `RAS_CEC`, `RAS_CEC_DEBUG`, `X86_MCE_INJECT`, `X86_MCELOG_LEGACY`, `EDAC_GHES`, `EDAC_DEBUG` | the same kernel |
| rasdaemon | 0.8.0, package `rasdaemon-0.8.0-9.fc44`, started by systemd | the same program, started by hand with a database of its own |
| What can be injected | what the kernel does with a page | machine check records, through the `mce-inject` module |

The hypervisor shows the lab host no machine check banks, no memory controller and no ACPI error injection table. On the host itself only the last step of error handling can be driven: the kernel is told that a page is bad. Machine checks are injected one level down, in a guest whose processor is emulated. Hardware virtualization is not available inside the lab host, so a guest boots in about a minute and runs slowly.

## Method

1. A scenario is a script that injects a fixed set of errors and writes down what it injected: `tools/lab/vm.sh` on the lab host, `tools/lab/mce.sh` in the x86 guest. Addresses are those of pages that a waiting process holds (`tools/lab/hwpoison.c`), so the kernel has something to move or to take away.
2. The result is an injection plan, `plan.yaml`: every injection with its time, its address, how many times, and what the kernel is expected to do with the page.
3. `tools/lab/capture.sh` collects `dmesg`, `journalctl -k -o json`, the rasdaemon database, the EDAC tree, `dmidecode -t 17`, the files under `/proc` and `/sys` that name the host, and what `my scan` and `my bundle --anonymize` say on the host as root.
4. `tools/lab/sanitize.py` replaces what names the lab and fails when a part of such a value is left anywhere, also inside the database and the bundle.
5. `internal/labcase` reads every capture three ways: as a copy of the host's files (`my --root`), as loose files (`my import`), and through the bundle written on the host (`my open`). Each time `injection.Compare` looks up every injection of the plan in the ledger. An injection passes when the ledger holds it the planned number of times, counted once however many sources report it, with every field it reports equal to the plan. Records beyond the plan fail the test, except pages that an earlier boot of the same host took offline.

The plan format and the comparison are in `internal/injection`.

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

No capture has an EDAC memory controller: the EDAC tree of every host is empty. All errors of the machine check captures are therefore of observation grade B (an address and a channel) and belong to no DIMM.

## What the captures changed

Each of these was wrong or missing before the capture showed it.

- `dmidecode` 3.7 prints `Size: 16 GiB`. The reader accepted `GB` only and saw no populated memory device in such an output.
- rasdaemon dates a record by the uptime of its trace event plus an offset it measured when it started. chronyd set the clock of the lab host back by 6.7 to 10.8 seconds on seven boots, a few seconds after rasdaemon had started. The kernel log dates the same errors by the kernel's clock, and two records more than 5 seconds apart were not taken for one error: every error of such a boot counted twice. A machine check record is now dated by the time the kernel put into it, and the rows of other tables next to it move by the same amount.
- The journal files the first kernel messages of a boot, 1,318 of 1,486 on the lab host, under the host name `localhost`. An import of a journal export was stored under that name. The name now comes from the end of the file.
- `journalctl -o short-monotonic` puts `host kernel:` between the stamp and the message. The boot banner was not recognised behind it.
- The kernel announces a page that a program asked it to give up (`Injecting memory failure for pfn ...`). Such a page is now marked in the ledger and counted in the source report. A boot in which `mce-inject` or EINJ was set up is named.
- With one day on record, the timeline drew one letter where an uncorrected error, the offline of its page and a reboot share a column, and dropped the other kinds with their text.
- An anonymized bundle kept `root=UUID=...` of the kernel command line.
- A copy of a host's files was dated by the newest of its files. A copy that lost the times of its files, as every checkout of `testdata/real` does, is now dated by its own boot time and uptime.

## What the kernel and rasdaemon do that the hand-written fixtures did not show

- rasdaemon stores the status register of a machine check as a signed number. The package of Fedora 44 creates seven tables and none for CXL; its `mce_record` has no `ppin`, `microcode`, `ipid` or `synd` column.
- rasdaemon 0.8.0 names the page type of a memory failure from a table of its own, indexed by a kernel enum that lost entries since. On kernel 7.3 it writes `dirty unevictable LRU page` where the kernel says `dirty LRU page`. The result (`Recovered`) is right.
- A soft offline that succeeds leaves one line in the kernel log, the request. There is no line for the outcome and no trace event.
- The corrected error collector takes a page offline at the second corrected error on an Intel processor. On the lab host, with an AMD processor, its threshold is 1,023.
- With `CONFIG_X86_MCELOG_LEGACY`, as in the lab kernel, a logged machine check is not printed: the kernel log says `Machine check events logged` and nothing else. The record is printed with `mce=print_all`, and on the console when the kernel panics.
- An uncorrected error that needs no action (UCNA) costs its page like one that needs action.
- `CPU0 BANK7 CMCI storm detected` and `... subsided` carry no `mce:` prefix. `MCE: Killing <program> due to hardware memory corruption fault at ...` is logged when a program touches a poisoned page.
- The journal of one boot holds two machine IDs, the one of the initrd and the one of the installed system, and `_SOURCE_MONOTONIC_TIMESTAMP` is `0` for the first messages of a boot.
- systemd writes the UUID of the root file system into unit names as `1b2c3d4e\x2d5f60...`, with `\x2d` for each dash, and `/dev/kmsg` escapes the backslash once more. A search for the plain UUID finds neither.

## What the lab cannot exercise

- An EDAC driver of a real memory controller (`skx_edac`, `i10nm_edac`, `amd64_edac`): no hardware and no emulation of it. DIMM labels from EDAC, the row and column these drivers decode, and the EDAC counters are covered by hand-written fixtures only.
- Machine check banks of real hardware: corrected error interrupts, a record that stays in its bank across a reset and is logged by the next boot, the decoding of AMD processors (their memory banks lie above the ten banks QEMU emulates).
- ACPI error injection (EINJ) and firmware-first handling on x86.
- A BMC, a GPU, CXL memory, repairs by firmware.
- Time: every capture spans minutes. Trends over days, the 30 day baseline and a database that grew for months are not in them.
- Other versions: one kernel and one rasdaemon package.

## Open points

The captures raised these and did not settle them.

- A host whose errors cannot be tied to a DIMM gets the finding "ok" next to "CE 7, UE 3": findings are made per DIMM. The overview does not mention pages taken offline at all.
- A page that a program asked the kernel to give up is marked, and still counts like any other offline.
- A rasdaemon database without machine check records, as on ARM servers, gives no way to see that rasdaemon's clock was off.

## Repeating it

On a host with root access, a kernel built with the options above, rasdaemon, QEMU and virtme-ng:

```
LAB=/root/my-lab
mkdir -p $LAB/bin $LAB/tools && cp tools/lab/* $LAB/tools/
make cross && cp dist/my-linux-amd64 $LAB/bin/my
gcc -O2 -o $LAB/bin/hwpoison tools/lab/hwpoison.c

$LAB/tools/vm.sh baseline        # then: hwpoison, soft
$LAB/tools/mce.sh                # then: print-all, clock-step, fatal
python3 tools/lab/sanitize.py $LAB/cap/mce testdata/real/7.3.0-mce --replace <build host>=lab-vm
```

`vm.sh hwpoison` and `vm.sh soft` take pages of the host itself offline until the next reboot, a few kilobytes each time. Everything else happens inside a guest.
