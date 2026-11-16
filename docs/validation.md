# Validation on real hosts

This page records what the release binary did on five running Linux hosts, as an unprivileged user and as root, on x86_64 and aarch64. The hosts were run on 2026-11-15 in two passes and on 2026-11-16 in three; the numbers below are those of the last pass. All five are virtual machines without a memory controller in EDAC, so the runs prove the binary, its commands and the degraded paths of a scan. They do not prove the decoding of errors from real memory controllers. [validation-lab.md](validation-lab.md) covers injected errors in a lab, and the section "What these hosts cannot exercise" says what is still open.

The binary is the build of commit `73268e9` for linux/amd64 and linux/arm64 (`make dist`, Go 1.26.4, statically linked). The same build for darwin/arm64 opened the bundles afterwards.

## Hosts

| Host | Architecture | System | Kernel | Hypervisor | CPUs, memory | Up since boot | Scanned as |
| --- | --- | --- | --- | --- | --- | --- | --- |
| A | x86_64 | Rocky Linux 9.6 | 5.14.0-687.26.1.el9_8 | KVM | 1, 765 MB | 79 days | root, and the user `nobody` |
| B | x86_64 | Rocky Linux 9.6 | 5.14.0-570.33.2.el9_6 | KVM | 2, 764 MB | 108 days | a user, and root through `sudo` |
| C | aarch64 | Rocky Linux 9.6 | 5.14.0-570.33.2.el9_6 | KVM | 1, 5.5 GB | 138 days | a user, and root through `sudo` |
| D | aarch64 | Rocky Linux 9.6 | 5.14.0-570.33.2.el9_6 | KVM | 1, 5.5 GB | 128 days | a user, and root through `sudo` |
| lab | x86_64 | Fedora 44 | 7.3.0, built for the lab | VMware | 16, 48 GB | 22 hours | root, and the user `nobody` |

A to D are cloud instances that run other services. Nothing was installed or changed on them: the binary and the script were copied into one temporary directory, which was removed afterwards. The lab host is the machine of [validation-lab.md](validation-lab.md); it runs rasdaemon and keeps a journal of 21 boots with the page offlines of earlier injection runs.

What each host offers to a scan:

| | A | B | C | D | lab |
| --- | --- | --- | --- | --- | --- |
| EDAC memory controllers | none | none | none | none | none |
| Machine check banks in sysfs | 10 | 10 | none | none | none |
| ACPI error source (HEST), error injection (EINJ) | no | no | no | no | no |
| rasdaemon | not installed | not installed | not installed | not installed | running, 6 rows |
| mcelog daemon | running | not installed | not installed | not installed | installed, not running |
| Kernel lines in the journal | 3 | none | none | 39 | 33,542 from 21 boots |
| Kernel lines in the ring buffer | 653 | 650 | 2,445 | 5,357 | 1,541 |
| pstore, kdump | empty | empty | empty | empty | pstore empty; 6 records in the text logs of kdump |
| `kernel.dmesg_restrict` | 0 | 0 | 0 | 0 | 1 |
| SMBIOS tables | root only | root only | root only | root only | root only |
| Memory devices in SMBIOS | 1 | 1 | 1 | 1 | 3 in 128 slots |

## What was run

`tools/validate/host.py` runs the binary the way an operator would and records, per command, the exit code, the time, the peak memory and what was printed. One pass as the user who starts it holds 56 to 60 commands:

- the scan as text in English and Korean, in ASCII at 80 columns, and as JSON;
- `why` as text, as one line and as JSON, and `timeline`;
- `boots` as text and as JSON, `inventory`, `bios` (no BMC: no settings on record), and `preflight` as text, as JSON and as a script;
- `bundle` to a file, anonymized, and to the standard output;
- `open` on the bundles: overview, `why`, `timeline`, JSON;
- `report` from a scan and from a bundle, in English and in Korean for failure analysis, with `--figures`, and to the standard output;
- a scan into a ledger twice, and `open` on that ledger;
- `import` of `dmesg` output, of `journalctl -k -o json` and of `/var/log/messages` where the user may read it;
- `gpu`, `export fmfm`, `export labels` (no EDAC counters, so no label: exit code 70), `harvest status` on the ledger of the pass, `version`, four help texts;
- twelve command lines that must fail: an unknown command, an unknown flag, the same with `--lang ko` behind it (the error must be in Korean), a slot that does not exist, a file that is not there, a file that is no case, an output directory that is not there, a bundle that exists, a directory that holds nothing of a host, a root that is not there, a command after the flags, a threshold that is no number.

A second pass runs the scan, the views, `boots`, `inventory`, `bios`, `preflight` and the three bundles as the other user: as root through `sudo -n` where the first pass was unprivileged, as `nobody` where the login is root. That is 16 commands.

Beyond the exit code the script checks that no command printed the text of a crash, that the JSON parses and names the exit code the process ended with, that a bundle is a ZIP archive with its four members intact, that the temporary directory of the binary is empty after every command, and that a bundle sent to a pipe belongs to the reader of the pipe. In an anonymized bundle it searches every member for the host name, the fully qualified name, the IP addresses, the hardware addresses of the network interfaces and the machine ID of the host.

On hosts B, C, D and the lab a bundle and a report were also written to `/dev/null`, with a check that it is still the same character device afterwards. On the lab host the exporter was started on a loopback port, and a scan and a bundle ran under `strace`.

## Results

368 commands ran on the five hosts in the last pass. None crashed, none ended with an exit code other than the expected one, none left a file in the temporary directory, and no anonymized bundle held a name or an address of its host.

| | A | B | C | D | lab |
| --- | --- | --- | --- | --- | --- |
| Commands, first and second pass | 56 + 16 | 57 + 16 | 57 + 16 | 58 + 16 | 60 + 16 |
| Host finding as root | ok | ok | ok | ok | observe |
| Exit code of the scan | 0 | 0 | 0 | 0 | 1 |
| Time of one scan as root, median | 0.20 s | 0.33 s | 0.10 s | 0.11 s | 1.67 s |
| Peak memory of the binary | 28 MB | 25 MB | 24 MB | 23 MB | 52 MB |
| Bundle of a scan as root, bytes | 15,373 | 15,047 | 14,747 | 15,011 | 37,685 |
| Bundle of a scan as an unprivileged user, bytes | 14,677 | 14,392 | 14,095 | 14,357 | 16,495 |
| Hypervisor the scan named | KVM | KVM | KVM | KVM | VMware |

`GOMEMLIMIT` was not set. On A and B, the two hosts with 765 MB of memory, the binary set a soft limit of 191 MiB for its heap by itself ([performance.md](performance.md)). The peak is the largest resident set of any command of a pass.

`my preflight` exited with 1 on all five hosts, as a host without the settings of [crash-loop.md](crash-loop.md) does: none of them registers a pstore backend or runs `my harvest`, A to D run no rasdaemon, B, C and D keep their journal in memory only, and the lab host loads no crash kernel.

The scan of host D as root, from its anonymized bundle:

```
host host-347fefed  QEMU KVM Virtual Machine  2026-07-11 to 2026-11-16  no errors on record
host finding: ok

    DIMM 0
    ░░

 ██ replace now   ▓▓ consider replacement   ▒▒ observe   ░░ ok   ?? hold replacement
 1 slot, 1 DIMM

 no DIMM has errors on record

 1 DIMM without errors
 sources  hostinfo 2 records, klog (journal) without records, klog (ring buffer) 3 records, pstore
          (/sys/fs/pstore) without records, pstore (/var/lib/systemd/pstore) without records, kdump
          without records, edac-sysfs without records, smbios 1 record
 missing  rasdaemon: not present (no rasdaemon database: rasdaemon is not installed or has stored
          nothing. Error records then come from the kernel log and the counters only)
 note     This host is a virtual machine (KVM): a guest is not shown the corrected errors of the
          machine it runs on, and of the uncorrected ones at most those that hit its own memory
```

### What a user sees and what root sees

| Source | As an unprivileged user | As root |
| --- | --- | --- |
| Host facts (`/proc`, `/sys/class/dmi`) | read on all five | the same |
| SMBIOS tables | not readable on all five: no slot, no DIMM | read: 1 DIMM on A to D, 3 DIMMs in 128 slots on the lab host |
| Journal | open to the user on B, C and D, with kernel lines in it on D only; closed to `nobody` on A and on the lab host | read on A, D and the lab host; it holds no kernel line on B and C |
| Kernel ring buffer | read on A to D; closed on the lab host, where `kernel.dmesg_restrict` is 1 | read |
| rasdaemon database | read on the lab host, where the file is world-readable | the same |
| mcelog daemon | answers root only (A) | answered with nothing: no DIMM with errors |
| pstore (`/sys/fs/pstore`) | closed on all five | read on all five: empty |
| Text logs of kdump | closed to `nobody` on the lab host | read on the lab host: 6 records of the boots before |
| EDAC counters | no memory controller is registered | the same |

A scan that could not read a source names it on a "missing" line with the reason, and says what a scan as root would add. On the lab host the user `nobody` could read the rasdaemon database and neither kernel log: the scan showed 5 pages taken offline where root saw 63, with both kernel logs and the SMBIOS tables listed as not readable.

### The finding of the lab host

The lab host has no errors on record and 63 pages that the kernel took offline in earlier injection runs. No record ties them to a DIMM, so the host finding speaks about them:

```
host host-5244a2dd  tied to no DIMM  63 pages offline

 finding     observe (finding, confidence medium, 70)                                      R-HOST-02
 evidence    [R-HOST-02] 63 pages taken offline and tied to no DIMM, the last on 2026-11-16
             68 records: first journalctl -o json _TRANSPORT=kernel +
             SYSLOG_IDENTIFIER=mcelog#L5997, last journalctl -o json _TRANSPORT=kernel +
             SYSLOG_IDENTIFIER=mcelog#L15353
 checks      weakens: 16 of the pages were taken offline because a program asked the kernel to, as a
             test of that path does: no error of the hardware is behind them
 missing     this host is a virtual machine (VMware): a guest is not shown the corrected errors of
             the machine it runs on, and of the uncorrected ones at most those that hit its own
             memory
 next        No action in the guest. The DIMM behind such records is found by a scan of the
             hypervisor host.
```

The scan read 33,542 kernel lines of 21 boots from the journal and 1,541 from the ring buffer, and the text logs kdump left of the boots that ended in a crash.

### Files the binary wrote on the lab host

Under `strace -f`, a scan created 7 files and directories and `my bundle` 11. All of them lie below the directory the run was given for its output and its temporary files: the temporary ledger with the files SQLite keeps beside it, a copy of the rasdaemon database, and the bundle, which is written beside its target and moved into place. Nothing else was opened for writing, created, renamed, removed or changed in its permissions. The programs the binary runs are traced too (`-f`).

### The exporter on the lab host

`my serve --metrics` on a loopback port was ready after 1.8 seconds, answered `/metrics` with 43 series of 13 metric families, `/api/dimm` with the host finding, `/-/healthy` with 200, and ended with exit code 0 on SIGTERM.

### The bundles on another machine

The 30 bundles of the five hosts (two passes, three bundles each) were opened on macOS on arm64 with the build of the same commit: `my open`, `my open FILE why --json`, `my report` in English, and `my report --lang ko --audience fa --all`. Every command succeeded. The 30 English reports are well-formed XML (`xmllint`), and the 30 Korean reports have no line-break violation at widths of 1440, 1000, 820 and 560 pixels.

## What the runs changed

On 2026-11-15 the first pass ran the build of commit `4ba85eb`. Nothing crashed, and these things were wrong or missing. Each was fixed before the second pass of that day.

| Seen | Change |
| --- | --- |
| A file is written beside its target and moved into place. As root, `my bundle -o /dev/null --force` would have replaced the device with a regular file. Found by reading the code before the first run as root | A device, a pipe or a socket is written to as it is, and a symbolic link stays a link (#79) |
| A missing output directory was reported after the scan, with the name of a temporary file in the message | The target is checked before the scan: exit code 64 and the name of the directory (#79) |
| Output files were 0644 whatever the umask said | New files follow the umask (#79) |
| No way to fetch a bundle without leaving a file on the host, and a file written through `sudo` belongs to root | `my bundle -o -` writes to the standard output (#79) |
| On A and D the journal held 3 and 39 kernel lines of a boot that the ring buffer still held from its first line. A scan as root read the journal only and knew less than a scan as an unprivileged user | The journal and the ring buffer are both read; a line that is in both is stored once (#84) |
| The header named the time since boot, while the kernel log that was read began 72 and 126 days later | Every kernel log source notes when its first and its last line were written, and how late after boot it starts (#84) |
| `my --root DIR` on a directory that holds nothing of a host, and `my` of the macOS build, answered "host finding: ok" with exit code 0 | A scan that read no source of error records ends with exit code 70 and lists what it looked for (#93) |
| Errors and page offlines that name no DIMM left the finding at "ok", exit code 0, as on the lab captures with machine checks | The host finding, rules R-HOST-01 and R-HOST-02 (#90) |
| Every host is a guest and got "ok" without a word about what a guest is shown | A note in the overview, in every finding and in the report (#96) |
| The mcelog daemon of A answers its client with nothing on a host without errors; the source said "or its answer was not saved" | The source says that the daemon answered and lists no DIMM (#84) |
| `my --root DIR why` answered `unexpected argument "why"` | The message says that the command comes before its flags (#79) |
| `my report --figures DIR` on a host with nothing to draw printed nothing about figures and left an empty directory | It says that no figure was written and makes no directory (#79) |

On 2026-11-16 the script also ran the commands added since (`boots`, `inventory`, `bios`, `preflight`, `harvest status`, `export labels`). The first pass, with the build of commit `3dea86d`, found:

| Seen | Change |
| --- | --- |
| `my --frobnicate --lang ko` answered in English on every host: the flags are parsed up to the first wrong one, so the `--lang` behind it was never read | When the flags cannot be parsed, `--lang` is read from the arguments by hand (#153) |
| The script took `/proc/sys/kernel/panic: 0`, a line of evidence of `my preflight --json`, for the text of a crash | The script looks for what the Go runtime prints when a program crashes, at the start of a line (#153) |

The second pass, with the build of `f7b15ca`, found no problem of the binary. It showed one wrong answer: on A and B a crash kernel is loaded and pstore has no backend, and `my preflight` called `pstore-vs-kdump` lost and printed `crash_kexec_post_notifiers` as the fix, which does nothing while pstore has no backend. That check is now `info` there (#155). The third pass, with the build of `73268e9`, gave the numbers above.

## Captures of injected errors

The lab captures under `testdata/real` are read by the tests on every run. The captures of crash loops are read through the kept ledger that `my harvest` wrote in the guest (`my open <capture>/var/lib/my/ledger.db`), the others with `--root`. The build of this page gives for them:

| Capture | Exit code | Finding | Errors | What decides |
| --- | --- | --- | --- | --- |
| `7.3.0-baseline` | 0 | ok | none | nothing on record |
| `7.3.0-hwpoison` | 1 | observe | none | host finding R-HOST-02: 4 pages offline, all asked for by a program |
| `7.3.0-soft-offline` | 1 | observe | none | host finding R-HOST-02: 7 pages offline |
| `7.3.0-mce` | 4 | hold replacement | CE 7, UE 3, grade B | host finding R-HOST-01: the machine checks name no DIMM |
| `7.3.0-mce-print-all` | 4 | hold replacement | CE 7, UE 3, grade B | the same; each error is in two sources and counts once |
| `7.3.0-mce-clock-step` | 4 | hold replacement | CE 7, UE 3, grade B | the same, with rasdaemon's clock 10 seconds ahead |
| `7.3.0-mce-rasdaemon-1.0` | 4 | hold replacement | CE 7, UE 3, grade B | the same, from a rasdaemon 1.0.0 database |
| `7.3.0-mce-fatal` | 4 | hold replacement | UE 1, grade B | host finding R-HOST-01, from the console log alone (`my import`) |
| `7.3.0-cxl` | 3 | replace now | CE 11, UE 1, grade A 9 and B 3 | `cxl:mem0`: R-UE-01, R-FW-01 |
| `6.19.10-ghes` | 3 | replace now | CE 24, UE 2, grade A | `DIMM 0`: R-UE-01, R-SCRUB-01 |
| `6.19.10-ghes-clock-step` | 3 | replace now | CE 10, UE 1, grade A | `DIMM 0`: R-UE-01 |
| `6.19.10-ghes-multi` | 2 | consider replacement | CE 35, UE 1, grade A | `CPU0_B1`, `CPU0_D1`, `CPU0_E1`: R-ROW-03, and R-SCRUB-01 for `CPU0_D1`; `CPU0_A1` observe, R-SOFT-01 |
| `6.19.10-mce-loop` | 4 | hold replacement | UE 3, grade B | host finding R-HOST-01: 3 of 4 boots stopped, and no record names a DIMM |
| `6.19.10-mce-loop-scatter` | 4 | hold replacement | UE 3, grade B | the same, at another address in every boot |
| `6.19.10-mce-loop-kdump` | 4 | hold replacement | UE 1, grade B | host finding R-HOST-01: 1 of 3 boots stopped, with a crash kernel loaded |
| `6.19.10-bert-loop` | 3 | replace now | UE 3, grade A | `DIMM 0`: R-UE-01, R-LOOP-01: 3 of 4 boots stopped at one place |
| `6.19.10-ghes-loop-spread` | 4 | hold replacement | UE 3, grade A | `CPU0_A1`, `CPU0_C1`, `CPU0_E1`: R-LOOP-03: 3 of 5 boots stopped, each on another DIMM |

`my verify --expect plan.yaml` ends with exit code 0 for all 17 captures: every injection of the plan is in the records, the planned number of times. 15 are verified with `--root`, `6.19.10-bert-loop` through its kept ledger (its copy holds the last boot only), and `7.3.0-mce-fatal` through a case that `my import` made of its console log.

## What these hosts cannot exercise

- An EDAC driver of a real memory controller. No host registers one, so no error with a DIMM, a rank, a row and a column came from hardware. `skx_edac`, `i10nm_edac` and `amd64_edac` are covered by hand-written fixtures; `ghes_edac` by the lab captures.
- A memory error of the hardware. The cloud hosts had none, and a guest would not be shown the corrected ones. The findings above "ok" come from the lab captures and from synthetic hosts.
- Machine check banks that hold a record. A and B have ten emulated banks and nothing in them.
- ACPI error injection (EINJ) and a firmware error source (HEST): absent on all five.
- A BMC, and a GPU with its driver.
- DIMM identities. SMBIOS of a guest lists one memory device without a serial number, so a history that follows a DIMM across slots was not seen on a real host.
- A rasdaemon database of months. The one on the lab host holds 6 rows.
- Large logs. The largest kernel log read was 33,542 journal lines in 1.8 seconds.
- A reset after a fatal error. No host was made to crash; the crash loops ran in emulated guests ([crash-loop.md](crash-loop.md)).
- Other distributions and kernels than Rocky Linux 9.6 with 5.14 and Fedora 44 with 7.3, and other architectures than x86_64 and aarch64.

## Next step

Error injection on bare metal: corrected and uncorrected errors at known addresses through ACPI EINJ on servers with the EDAC driver of their memory controller (Intel, AMD, and an ARM server with firmware-first handling), with rasdaemon recording. `my verify --expect plan.yaml` compares what was injected with what the tool read ([verify.md](verify.md)); the plan format and the capture scripts of the lab are in `tools/lab`. A capture of such a host goes into `testdata/real` like the ones above.

## Repeating it

On a Linux host, with a build of the binary:

```
python3 tools/validate/host.py --my ./my --out /tmp/my-test/out --sudo
```

CI runs this on its own machine on every change, an Ubuntu runner that is a Hyper-V guest: 73 commands as a normal user and as root, with the `/dev/null` check and the exporter, on the change that added the newer commands (#153).

`--sudo` adds the pass as root through `sudo -n`; started as root, `--as nobody` adds the pass as that user. `--devnull`, `--strace` and `--serve` add the checks that ran on some hosts only. The script needs Python 3.6 and installs nothing. It writes below `--out` and nowhere else, prints one line per command, and exits with 1 when a check failed. `results.json` holds the numbers of this page for the host it ran on.
