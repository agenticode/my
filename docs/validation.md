# Validation on real hosts

This page records what the release binary did on five running Linux hosts on 2026-11-15, as an unprivileged user and as root, on x86_64 and aarch64. All five are virtual machines without a memory controller in EDAC, so the runs prove the binary, its commands and the degraded paths of a scan. They do not prove the decoding of errors from real memory controllers. [validation-lab.md](validation-lab.md) covers injected errors in a lab, and the section "What these hosts cannot exercise" says what is still open.

The binary is the build of commit `a9c3a52` for linux/amd64 and linux/arm64 (`make dist`, Go 1.26.4, statically linked). The same build for darwin/arm64 opened the bundles afterwards.

## Hosts

| Host | Architecture | System | Kernel | Hypervisor | CPUs, memory | Up since boot | Scanned as |
| --- | --- | --- | --- | --- | --- | --- | --- |
| A | x86_64 | Rocky Linux 9.6 | 5.14.0-687.26.1.el9_8 | KVM | 1, 765 MB | 78 days | root, and the user `nobody` |
| B | x86_64 | Rocky Linux 9.6 | 5.14.0-570.33.2.el9_6 | KVM | 2, 764 MB | 108 days | a user, and root through `sudo` |
| C | aarch64 | Rocky Linux 9.6 | 5.14.0-570.33.2.el9_6 | KVM | 1, 5.5 GB | 137 days | a user, and root through `sudo` |
| D | aarch64 | Rocky Linux 9.6 | 5.14.0-570.33.2.el9_6 | KVM | 1, 5.5 GB | 127 days | a user, and root through `sudo` |
| lab | x86_64 | Fedora 44 | 7.3.0, built for the lab | VMware | 16, 48 GB | 7 hours | root, and the user `nobody` |

A to D are cloud instances that run other services. Nothing was installed or changed on them: the binary and the script were copied into one temporary directory, which was removed afterwards. The lab host is the machine of [validation-lab.md](validation-lab.md); it runs rasdaemon and keeps a journal of 20 boots with the page offlines of earlier injection runs.

What each host offers to a scan:

| | A | B | C | D | lab |
| --- | --- | --- | --- | --- | --- |
| EDAC memory controllers | none | none | none | none | none |
| Machine check banks in sysfs | 10 | 10 | none | none | none |
| ACPI error source (HEST), error injection (EINJ) | no | no | no | no | no |
| rasdaemon | not installed | not installed | not installed | not installed | running, 6 rows |
| mcelog daemon | running | not installed | not installed | not installed | installed, not running |
| Kernel lines in the journal | 3 | none | none | 39 | 33,472 from 20 boots |
| Kernel lines in the ring buffer | 653 | 650 | 2,445 | 5,331 | 1,491 |
| `kernel.dmesg_restrict` | 0 | 0 | 0 | 0 | 1 |
| SMBIOS tables | root only | root only | root only | root only | root only |
| Memory devices in SMBIOS | 1 | 1 | 1 | 1 | 3 in 128 slots |

## What was run

`tools/validate/host.py` runs the binary the way an operator would and records, per command, the exit code, the time, the peak memory and what was printed. One pass as the user who starts it holds 45 to 49 commands:

- the scan as text in English and Korean, in ASCII at 80 columns, and as JSON;
- `why` as text, as one line and as JSON, and `timeline`;
- `bundle` to a file, anonymized, and to the standard output;
- `open` on the bundles: overview, `why`, `timeline`, JSON;
- `report` from a scan and from a bundle, in English and in Korean for failure analysis, with `--figures`, and to the standard output;
- a scan into a ledger twice, and `open` on that ledger;
- `import` of `dmesg` output, of `journalctl -k -o json` and of `/var/log/messages` where the user may read it;
- `gpu`, `export fmfm`, `version`, four help texts;
- eleven command lines that must fail: an unknown command, an unknown flag, a slot that does not exist, a file that is not there, a file that is no case, an output directory that is not there, a bundle that exists, a directory that holds nothing of a host, a root that is not there, a command after the flags, a threshold that is no number.

A second pass runs the scan, the views and the three bundles as the other user: as root through `sudo -n` where the first pass was unprivileged, as `nobody` where the login is root. That is 11 commands.

Beyond the exit code the script checks that no command printed the text of a crash, that the JSON parses and names the exit code the process ended with, that a bundle is a ZIP archive with its four members intact, that the temporary directory of the binary is empty after every command, and that a bundle sent to a pipe belongs to the reader of the pipe. In an anonymized bundle it searches every member for the host name, the fully qualified name, the IP addresses, the hardware addresses of the network interfaces and the machine ID of the host.

On hosts B, C, D and the lab a bundle and a report were also written to `/dev/null`, with a check that it is still the same character device afterwards. On the lab host the exporter was started on a loopback port, and a scan and a bundle ran under `strace`.

## Results

288 commands ran on the five hosts. None crashed, none ended with an exit code other than the expected one, none left a file in the temporary directory, and no anonymized bundle held a name or an address of its host.

| | A | B | C | D | lab |
| --- | --- | --- | --- | --- | --- |
| Commands, first and second pass | 45 + 11 | 46 + 11 | 46 + 11 | 47 + 11 | 49 + 11 |
| Host finding as root | ok | ok | ok | ok | observe |
| Exit code of the scan | 0 | 0 | 0 | 0 | 1 |
| Time of one scan as root, median | 0.20 s | 0.30 s | 0.08 s | 0.10 s | 1.67 s |
| Peak memory of the binary | 24 MB | 20 MB | 21 MB | 21 MB | 48 MB |
| Bundle of a scan as root, bytes | 10,964 | 10,824 | 10,803 | 11,017 | 31,819 |
| Bundle of a scan as an unprivileged user, bytes | 10,216 | 10,093 | 10,077 | 10,292 | 11,946 |
| Hypervisor the scan named | KVM | KVM | KVM | KVM | VMware |

The binary ran with `GOMEMLIMIT=200MiB` on A and B, the two hosts with 765 MB of memory. The peak is the largest resident set of any command of a pass.

The scan of host D as root, from its anonymized bundle:

```
host host-3b7bf9c1  QEMU KVM Virtual Machine  2026-07-11 to 2026-11-15  no errors on record
host finding: ok

    DIMM 0
    ░░

 ██ replace now   ▓▓ consider replacement   ▒▒ observe   ░░ ok   ?? hold replacement
 1 slot, 1 DIMM

 no DIMM has errors on record

 1 DIMM without errors
 sources  hostinfo 2 records, klog (journal) without records, klog (ring buffer) 3 records,
          edac-sysfs without records, smbios 1 record
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
| EDAC counters | no memory controller is registered | the same |

A scan that could not read a source names it on a "missing" line with the reason, and says what a scan as root would add. On the lab host the user `nobody` could read the rasdaemon database and neither kernel log: the scan showed 5 pages taken offline where root saw 62, with both kernel logs and the SMBIOS tables listed as not readable.

### The finding of the lab host

The lab host has no errors on record and 62 pages that the kernel took offline in earlier injection runs. No record ties them to a DIMM, so the host finding speaks about them:

```
host host-8ff0de20  tied to no DIMM  62 pages offline

 finding     observe (finding, confidence medium, 70)                                      R-HOST-02
 evidence    [R-HOST-02] 62 pages taken offline and tied to no DIMM, the last on 2026-11-15
             67 records: first journalctl -o json _TRANSPORT=kernel#L5997, last journalctl -o json
             _TRANSPORT=kernel#L15353
 checks      weakens: 16 of the pages were taken offline because a program asked the kernel to, as a
             test of that path does: no error of the hardware is behind them
 missing     this host is a virtual machine (VMware): a guest is not shown the corrected errors of
             the machine it runs on, and of the uncorrected ones at most those that hit its own
             memory
 next        No action in the guest. The DIMM behind such records is found by a scan of the
             hypervisor host.
```

The scan read 33,472 kernel lines of 20 boots from the journal and 1,491 from the ring buffer. Three records were in both and were stored once.

### Files the binary wrote on the lab host

Under `strace -f`, a scan created 8 files and directories and `my bundle` 11. All of them lie below the directory the run was given for its output and its temporary files: the temporary ledger with the files SQLite keeps beside it, a copy of the rasdaemon database, and the bundle, which is written beside its target and moved into place. Nothing else was opened for writing, created, renamed, removed or changed in its permissions. The children of the binary are traced too: `journalctl` and `mcelog --client`.

### The exporter on the lab host

`my serve --metrics` on a loopback port was ready after 1.8 seconds, answered `/metrics` with 24 series of 9 metric families, `/api/dimm` with the host finding, `/-/healthy` with 200, and ended with exit code 0 on SIGTERM.

### The bundles on another machine

The 30 bundles of the five hosts (two passes, three bundles each) were opened on macOS on arm64 with the build of the same commit: `my open`, `my open FILE why --json`, `my report` in English, and `my report --lang ko --audience fa --all`. Every command succeeded. The 30 English reports are well-formed XML (`xmllint`), and the 30 Korean reports have no line-break violation at widths of 1440, 1000, 820 and 560 pixels.

## What the runs changed

The first pass ran the build of commit `4ba85eb`. Nothing crashed, and these things were wrong or missing. Each was fixed before the second pass, whose numbers are the ones above.

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

## Captures of injected errors

The lab captures under `testdata/real` are read by the tests on every run. The build of this page gives for them:

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

`my verify --expect plan.yaml --root <capture>` ends with exit code 0 for the ten captures that hold a plan and a copy of the host: every injection of the plan is in the records, the planned number of times.

## What these hosts cannot exercise

- An EDAC driver of a real memory controller. No host registers one, so no error with a DIMM, a rank, a row and a column came from hardware. `skx_edac`, `i10nm_edac` and `amd64_edac` are covered by hand-written fixtures; `ghes_edac` by the lab captures.
- A memory error of the hardware. The cloud hosts had none, and a guest would not be shown the corrected ones. The findings above "ok" come from the lab captures and from synthetic hosts.
- Machine check banks that hold a record. A and B have ten emulated banks and nothing in them.
- ACPI error injection (EINJ) and a firmware error source (HEST): absent on all five.
- A BMC, and a GPU with its driver.
- DIMM identities. SMBIOS of a guest lists one memory device without a serial number, so a history that follows a DIMM across slots was not seen on a real host.
- A rasdaemon database of months. The one on the lab host holds 6 rows.
- Large logs. The largest kernel log read was 33,472 journal lines in 1.7 seconds.
- Other distributions and kernels than Rocky Linux 9.6 with 5.14 and Fedora 44 with 7.3, and other architectures than x86_64 and aarch64.

## Next step

Error injection on bare metal: corrected and uncorrected errors at known addresses through ACPI EINJ on servers with the EDAC driver of their memory controller (Intel, AMD, and an ARM server with firmware-first handling), with rasdaemon recording. `my verify --expect plan.yaml` compares what was injected with what the tool read ([verify.md](verify.md)); the plan format and the capture scripts of the lab are in `tools/lab`. A capture of such a host goes into `testdata/real` like the ones above.

## Repeating it

On a Linux host, with a build of the binary:

```
python3 tools/validate/host.py --my ./my --out /tmp/my-test/out --sudo
```

`--sudo` adds the pass as root through `sudo -n`; started as root, `--as nobody` adds the pass as that user. `--devnull`, `--strace` and `--serve` add the checks that ran on some hosts only. The script needs Python 3.6 and installs nothing. It writes below `--out` and nowhere else, prints one line per command, and exits with 1 when a check failed. `results.json` holds the numbers of this page for the host it ran on.
