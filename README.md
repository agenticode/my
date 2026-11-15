# MemorY

[English](README.md) | [한국어](README.ko.md)

MemorY reads the memory error records a Linux server already keeps and shows which DIMM is failing, where inside the DIMM the fault sits, how it developed across reboots and repairs, and what the finding rests on.

It is for anyone who has to read memory errors and decide what to do: platform and datacenter engineers, SREs, field and support engineers of hardware vendors, system integrators, kernel and RAS developers.

The command is `my`: one static binary that needs no configuration. It only reads. It never takes a page offline, starts a repair or resets a counter.

![Slot map of the example host: CPU0_C1 is marked replace now, CPU1_D1 observe](examples/figures/slotmap.svg)

![Address map of CPU0_C1: a horizontal line of cells with errors in row 0x1a2f, one of them an uncorrected error](examples/figures/CPU0_C1-map.svg)

## Why

A server records memory errors in several places: the rasdaemon database, the kernel log, the EDAC counters, the event log of the BMC. Each has its own form. The counters start at zero with every boot, the logs rotate, and none of them shows where in a DIMM a fault sits or what shape it has.

What a person has to decide stays open: which DIMM it is, whether one cell, a row or the whole device fails, whether the DIMM is at fault or its slot, whether it is getting worse, whether a repair helped.

MemorY puts the records of every source into one ledger keyed by DIMM serial number, so the history of a DIMM survives reboots and follows the DIMM to another slot. It draws that history, and it states a finding per DIMM with the rule that made it, the records behind it, the checks against other explanations and the data it did not have.

## What it shows

The screens below are those of the example case in this repository: a synthetic host with a row fault that grew for seven weeks ([examples/](examples/README.md)). On a server, `my` without arguments scans the host and draws the same overview.

```
$ my open examples/demo.mycase
case bundle of 2026-11-13 09:30 UTC, written by my dev; subject CPU0_C1 S/N 3B9C56
host r12-n034  Example Systems R760  2026-07-31 to 2026-11-13  CE 1,284, UE 1  observation grade A
host finding: replace now

        A1   B1   C1   D1   E1   F1   G1   H1
 CPU0   ░░   ░░   ██   ░░   ░░   ░░   ░░   ░░
 CPU1   ░░   ░░   ░░   ▒▒   ░░   ░░   ░░   ░░

 ██ replace now   ▓▓ consider replacement   ▒▒ observe   ░░ ok   ?? hold replacement
 32 slots, 16 DIMMs (16 empty slots not drawn)

 #  SLOT     SERIAL     CE  UE  MODE        TREND 30D     FINDING      RULES
 1  CPU0_C1  3B9C56  1,231   1  row         ▃▄▄▄▅▅▆▆▇█ ↑  replace now  R-UE-01 R-ROW-03 R-REP-02
 2  CPU1_D1  2AEB41     53   0  single-bit  ▆▁▄█▃▄▃▄▄▁ ↓  observe      R-SOFT-01

 14 DIMMs without errors
 sources  synth 1,297 records

 my open examples/demo.mycase dimm CPU0_C1: examination of this DIMM
 my open examples/demo.mycase why CPU0_C1: what the finding rests on
 my help: how to read the figures
```

One DIMM, examined: the finding, the branch of the DIMM the errors sit on, the address map of the bank with the most errors, and the timeline across reboots.

```
$ my open examples/demo.mycase dimm CPU0_C1
case bundle of 2026-11-13 09:30 UTC, written by my dev; subject CPU0_C1 S/N 3B9C56
CPU0_C1  DDR5 RDIMM 64 GB 2Rx4  Example Memory EXM64G48R2X4-A  S/N 3B9C56
in this slot since 2026-07-31  0 slot changes

 finding   replace now (finding, confidence high)                          R-UE-01 R-ROW-03 R-REP-02
 evidence  [R-UE-01] 1 uncorrected error, the latest on 2026-11-12 03:14 UTC at rank0 dev7 bg0 bank3
           row 0x1a2f col 0x238
           [R-ROW-03] row fault at rank0 dev7 bg0 bank3 row 0x1a2f: 421 errors in the last 7 days,
           2.3 times the daily rate of the 30 days before
           [R-ROW-03] row fault at rank0 dev7 bg0 bank3 row 0x1a2f: 13 new places in the last 30
           days; it now covers 9 columns in 2 rows
           [R-ROW-03] errors of the fault at rank0 dev7 bg0 bank3 row 0x1a2f involve 2 data lines of
           one device
           [R-REP-02] 764 errors at rank0 dev7 bg0 bank3 row 0x1a2f after the hard PPR of 2026-10-28
 checks    open: no other DIMM on this channel: there is nothing to compare with
           open: no slot move or reseat on record: a cause in the board is not ruled out
           supports: the other 15 DIMMs of this host have no unconfined errors: a cause in the board
           is unlikely
 missing   no ECS counters
           no DIMM temperature history
 baseline  own history: 60.1 errors a day over the last 7 days, percentile 100 of the 30 days before
           same part number (EXM64G48R2X4-A): percentile 97 among the 16 DIMMs in this ledger, 2 of
           them with errors
 next      Replace the DIMM. Attach a case bundle of this DIMM to the return.

 where the errors sit (width = share of the errors of this DIMM)
 rank    |rank0 ██████████████████████████████████████████████████████████████████████████████ 100%|
 device  |dev7 ███████████████████████████████████████████████████████████████████████████████ 100%|
 bank    |bg0 bank3 ██████████████████████████████████████████████████████████████████████████ 100%|
 row     |0x1a2f ████████████████████████████████████████████████████████████████████████████ 98%|█|

 rank0 dev7 bg0 bank3  row by column  one cell = 32 columns
 · 0   ░ 1-2   ▒ 3-9   ▓ 10-99   █ 100+   X uncorrected error

 row       0x000                                                      0x7ff  (column)
 0x1a2c    ································································
 0x1a2d    ································································
 0x1a2e    ································································
 0x1a2f    ··············▓··X·····█·▓··█····▓························▒···▒·  ← row fault
 0x1a30    ··············░··▒·····▒·░··▒···································  ← same fault
 0x1a31    ································································
 0x1a32    ································································
 0x1a33    ································································

 row fault at rank0 dev7 bg0 bank3 row 0x1a2f: 1,232 errors, 9 columns in 2 rows, data lines DQ2,
   DQ3, first 2026-09-24, latest 2026-11-12

 errors per day and events
           07-31     09-01     10-01     11-12
 CPU0_C1   ▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▂▂▂▂▃▃▃▃▄▄▄▅▅▆▆▇█  peak 73/day
 events            B           B        BP  OU
                   │           │        ││  │└ 2026-11-12 UE (demand read)
                   │           │        ││  └─ 7 pages offline, 2026-11-11 to 2026-11-12
                   │           │        │└──── 2026-10-28 hard PPR
                   └───────────┴────────┴───── 3 reboots

 1,231 corrected and 1 uncorrected error from 2026-07-31 to 2026-11-12
 790 of them since the boot of 2026-10-28: a counter that restarts at every boot shows only those
```

In the address map a horizontal line is a row fault, a vertical line a column fault, one cell that repeats a single-bit fault, and a cloud over the bank a bank fault. [docs/reading-guide.md](docs/reading-guide.md) explains every figure, and `my help reading` prints the same on a terminal.

`my report` writes a case as one HTML file with the figures as SVG, the finding of every DIMM, the checks against a wrong diagnosis and the original records. The file loads nothing from elsewhere, opens without network access and prints to PDF. The report of the example is [examples/demo-report.html](examples/demo-report.html); this is its timeline of `CPU0_C1`:

![Timeline of CPU0_C1: corrected errors per day rising from late September, with reboots, a hard PPR, page offlines and an uncorrected error marked below](examples/figures/CPU0_C1-timeline.svg)

## Quick start

No version is tagged yet, so the binary is built from source with Go 1.26 or newer:

```
go install github.com/agenticode/my/cmd/my@latest
```

In a checkout, `make build` writes `bin/my`, and `make cross` the binaries for linux/amd64, linux/arm64 and darwin/arm64. The binary is statically linked: copy the one file to the server.

On the server:

```
sudo my                          # slot map and DIMMs ranked by risk; the exit code is the grade
sudo my dimm CPU0_C1             # one DIMM: finding, topology bars, address map, timeline
sudo my why CPU0_C1 --oneline    # the finding as one line for a ticket
sudo my bundle -o case.mycase    # everything the scan read, as one file
```

Root is not required. Without it the scan reads what the host leaves open and names what it could not read: the SMBIOS tables with the DIMM serial numbers, and on some hosts the kernel log.

On any machine, Linux or macOS:

```
my open case.mycase                     # the screens the sender saw
my report case.mycase -o report.html    # the same case as a document
```

Without a server at hand, `my open examples/demo.mycase` in a checkout shows the example above.

## How it relates to collectors and repair agents

MemorY collects no errors and acts on none. It reads what the tools that do have left behind.

| Tool | Its part | What MemorY does with it |
| --- | --- | --- |
| rasdaemon, mcelog | record error events and counts | reads the database of rasdaemon, its store on a MySQL or PostgreSQL server, and the counts of the mcelog daemon |
| The kernel: EDAC drivers, firmware error records, machine checks | detects, decodes and logs errors | reads the kernel log and the EDAC counters |
| Page offlining by the kernel, post package repair and sparing by firmware, repair services of a BMC | take pages away, repair rows, spare devices | shows on the timeline what was done and when, and whether errors came back; it does none of it |
| A BMC | keeps an event log outside the operating system | reads `ipmitool sel` listings and Redfish dumps, or asks the Redfish service itself and changes nothing on it (`my collect redfish`) |
| A fault analyzer of a memory vendor (OCP FMFM) | classifies faults with knowledge of the device | hands it the records in FMFM field names and shows its answer (`--plugin`) |
| Prometheus | stores series and alerts | `my serve --metrics` exports the finding per DIMM |

## Inputs and observation grades

| Input | Read from | Gives |
| --- | --- | --- |
| rasdaemon | `/var/lib/rasdaemon/ras-mc_event.db`; with `--fleet`, its store on MySQL, MariaDB or PostgreSQL | error records with the place the EDAC driver decoded, machine checks, page offlines, CXL events |
| Kernel log | the journal and `/dev/kmsg` on a host; `dmesg` output, syslog files and `journalctl -o json` as files | EDAC lines, firmware error records, machine checks, page offlines, boots, Xid lines of the NVIDIA driver |
| EDAC counters | `/sys/devices/system/edac/mc` | counts per DIMM since boot |
| mcelog daemon | `mcelog --client` | counts per DIMM |
| SMBIOS | `/sys/firmware/dmi/tables`, or `dmidecode` output | slots, DIMM serial and part numbers |
| BMC | `ipmitool sel` listings, Redfish dumps, or the Redfish service | memory events, modules that firmware disabled, sparing |
| GPU memory | `nvidia-smi -q` and its queries | row remapping state and ECC counters per GPU |
| FMFM logs | JSON files | runtime, boot and crash records, ECS readings |
| Copy of a host's files | a directory with `proc`, `sys` and `var` below it (`--root`, `my import`) | the inputs above, as files |

A file is recognised by its content, so `my import` takes them in any mix. Platforms report different amounts of detail about the place of an error. Every record therefore has an observation grade, and a finding says only what the grade of its records allows:

| Grade | Place known | What it allows |
| --- | --- | --- |
| A | bank, row and column | fault modes: single bit, row, multi-row, column, bank, device, DIMM |
| B | physical address or rank | repetition per page and trends; a row fault is an estimate |
| C | the module, or a count only | trends and thresholds |

[docs/compatibility.md](docs/compatibility.md) has a section per input and says which grade each platform gives.

## Commands

| Command | What it does |
| --- | --- |
| `my` or `my scan` | scan this host: slot map and DIMMs ranked by risk |
| `my dimm <slot\|serial>` | examine one DIMM: finding, topology bars, address map, timeline |
| `my why [slot\|serial]` | the finding with the records, checks and confidence behind it; `--oneline` gives one line for a ticket |
| `my map <slot\|serial>` | the address map of one bank; `--bank`, `--rank`, `--device` choose it, `--rows A-B` zooms |
| `my timeline [slot\|serial]` | errors per day with boots, repairs, page offlines, slot changes and uncorrected errors |
| `my gpu [gpu]` | GPU memory as a repair budget: rows remapped, a remapping pending or failed, spare rows left per bank |
| `my bundle [slot\|serial] -o case.mycase` | write the records of this host as one file to send or attach |
| `my open FILE [command ...]` | run the commands above on a bundle or a ledger file |
| `my report [FILE] -o report.html` | write a case, or this host, as one HTML file; `--audience fa` adds the detail failure analysis needs |
| `my diff BEFORE AFTER` | two cases of one host, before and after a reseat, a slot swap, a replacement or a firmware update: did the errors move with the DIMM or stay with the slot |
| `my import <file\|directory>...` | build a case from files, without the host they came from |
| `my collect redfish --bmc ADDRESS -o OUT` | ask a BMC over Redfish for memory, BIOS settings and logs |
| `my export fmfm` | write the findings as an analysis log in the form of the OCP FMFM framework ([docs/fmfm-mapping.md](docs/fmfm-mapping.md)) |
| `my verify --expect plan.yaml` | compare an error injection plan with what the host recorded ([docs/verify.md](docs/verify.md)) |
| `my serve --metrics` | run as a Prometheus exporter ([docs/exporter.md](docs/exporter.md)) |
| `my help [command]` | flags of a command and how to read what it prints |

`--lang en|ko` chooses the language; the default follows the locale. `--json` prints the data behind a figure. `--ascii` draws with ASCII characters. A grade is told by the density of a glyph as well as by its colour, and colour is used on a terminal only, and not when `NO_COLOR` is set.

```
$ my open examples/demo.mycase why CPU0_C1 --oneline
case bundle of 2026-11-13 09:30 UTC, written by my dev; subject CPU0_C1 S/N 3B9C56
r12-n034 CPU0_C1 S/N 3B9C56: replace now (finding, confidence high; R-UE-01, R-ROW-03, R-REP-02). 1 uncorrected error, the latest on 2026-11-12 03:14 UTC at rank0 dev7 bg0 bank3 row 0x1a2f col 0x238. Replace the DIMM. Attach a case bundle of this DIMM to the return.
```

## Findings and exit codes

A command that shows findings exits with the grade of what it showed, the host or the DIMM it was asked about:

| Code | Grade | When |
| --- | --- | --- |
| 0 | ok | no errors on record |
| 1 | observe | corrected errors without a growing fault |
| 2 | consider replacement | a row or column fault is rising or spreading, a fault covers a bank or more, pages keep being taken offline, the patrol scrubber found an uncorrected error, or errors came back after a repair |
| 3 | replace now | an uncorrected error, a DIMM that firmware marks as failed, or repair resources that are used up |
| 4 | hold replacement | a cause outside the DIMM is suspected (the channel, the slot, the contact, slot names that disagree), or uncorrected errors name no DIMM |

Exit code 64 is a wrong command line and 70 a command that failed. A scan that could read no source of error records ends with 70 and names what it looked for: it does not answer "no errors on record".

The grade of a host is the worst grade among its DIMMs, its GPUs and the host finding, which speaks about errors and page offlines that the records tie to no DIMM. On a virtual machine every finding notes that a guest is shown little of the errors of the machine it runs on.

A grade is a finding. The thresholds behind it are defaults. They have been compared with the failure tickets of one public fleet dataset, with the results in [docs/evaluation.md](docs/evaluation.md): there a server with "consider replacement" got a failure ticket within 30 days about ten times as often as a server with "observe", in 2.5% of the cases. They have not been compared with failure analysis results. [docs/rules.md](docs/rules.md) lists the 15 rules with their conditions and thresholds, and `--set key=value` or `--config FILE` changes a threshold.

## Case bundles

A case travels as one file, from the person at the server to the person who reads it.

1. On the server: `sudo my bundle CPU0_C1 -o case.mycase --anonymize`. Naming a DIMM makes it the subject of the case; the bundle holds the whole host, because a finding about one DIMM rests on its neighbours.
2. The file is sent, or attached to the return of the DIMM. `ssh HOST sudo my bundle -o - > case.mycase` fetches it without leaving a file on the host.
3. The receiver runs `my open case.mycase` and sees the screens the sender saw, or `my report case.mycase -o report.html` for a document.

A bundle is a ZIP archive with the ledger, a manifest, the source inventory and a `README.txt` that says what is inside.

| A bundle holds | A bundle never holds |
| --- | --- |
| memory error records: time, severity, place, and the log line or database row each was read from | contents of memory, of files or of processes |
| repairs and page offlines the host recorded | user accounts, credentials or keys |
| DIMM identities: manufacturer, part number, serial number, size | network configuration |
| the slots of the board, and which DIMM sat where and when | kernel log lines that are not about memory errors, page offlining, machine checks or the boot banner |
| boots: time, kernel release, BIOS version, kernel command line | |
| the host name, its architecture and system product | |
| readings of the error counters per DIMM, GPU memory records, ECS readings | |
| every source that was looked for and what it yielded | |
| the findings of the sender's run | |

`--anonymize` replaces host names, IP addresses, MAC addresses and the disk identifiers of the kernel command line with words made from a keyed hash, and prints the replaced names for the sender to keep. DIMM serial numbers stay, because a return is about them. [docs/bundle-format.md](docs/bundle-format.md) describes the format.

## Vendor analyzers

The engine classifies faults with public knowledge: the Level 1 fault modes of the OCP FMFM framework. What only the maker of a DRAM device knows (which rows are neighbours inside the device, what error rate is usual for a part number, what a pattern of failing data lines means) comes from a vendor analyzer: a separate program, in any language and under its own license.

```
my dimm CPU0_C1 --plugin /opt/vendor/analyzer
my report case.mycase -o fa.html --audience fa --plugin /opt/vendor/analyzer
```

The program gets the error records of each DIMM in FMFM field names on its standard input and writes its analysis to its standard output. It keeps no state. Its answer about neighbouring rows goes into the fault classification, its percentile into the baseline of a finding, and its Level 2 output into the report for failure analysis and nowhere else. [docs/plugin-contract.md](docs/plugin-contract.md) is the reference, and [examples/plugin](examples/plugin) holds a small analyzer with a request and its response.

## Monitoring

`my serve --metrics` runs the same scan on a schedule and serves the finding and the error counts per DIMM to Prometheus, with the finding of one DIMM as JSON at `/api/dimm/<slot|serial>`. With `--fleet` one exporter reads the store that the rasdaemons of a fleet write to. No metric has an address, a row or a column as a label. [contrib/](contrib/README.md) holds systemd units, an alert rule that fires when a finding gets worse, Alertmanager routes and a Grafana dashboard.

## Status

Early development; no version is tagged.

- Tested with synthetic hosts and fixtures: every fault mode, every rule and every input has tests.
- Run on five real hosts, x86_64 and aarch64, as an unprivileged user and as root: [docs/validation.md](docs/validation.md). All five are virtual machines.
- Checked against captures of a lab host with injected page offlines, machine checks, firmware error records and CXL event records, each compared with the plan of its injection: [docs/validation-lab.md](docs/validation-lab.md).
- Findings compared with the failure tickets of a public fleet dataset: [docs/evaluation.md](docs/evaluation.md).
- Not validated yet: hosts with the EDAC driver of a real memory controller. The decoding of their records rests on fixtures written from kernel and rasdaemon sources.
- No analyzer of a memory vendor has been run against the plugin contract.

## Roadmap

- Error injection on bare metal with the EDAC drivers of Intel, AMD and ARM servers, compared with `my verify`.
- A first tagged release with archives for linux/amd64, linux/arm64 and darwin/arm64 ([docs/release.md](docs/release.md)).
- Thresholds compared with failure analysis results.
- ECS readings of DDR5 and the repair budget of a DIMM as figures.
- An interactive terminal view.

## Documentation

| Page | Content |
| --- | --- |
| [docs/reading-guide.md](docs/reading-guide.md) | how to read each figure |
| [docs/rules.md](docs/rules.md) | every rule with its condition, its thresholds and what the finding says |
| [docs/compatibility.md](docs/compatibility.md) | every input: what is read, which observation grade, what is missing |
| [docs/data-model.md](docs/data-model.md) | the ledger: tables, identities, how records of several sources become one history |
| [docs/bundle-format.md](docs/bundle-format.md) | the case bundle: members, manifest, anonymizing |
| [docs/plugin-contract.md](docs/plugin-contract.md) | the vendor analyzer contract |
| [docs/fmfm-mapping.md](docs/fmfm-mapping.md) | how records and findings map to the FMFM field names |
| [docs/exporter.md](docs/exporter.md) | the metrics and the HTTP API |
| [docs/verify.md](docs/verify.md) | comparing an injection plan with the records |
| [docs/evaluation.md](docs/evaluation.md) | the comparison with a field dataset |
| [docs/validation.md](docs/validation.md), [docs/validation-lab.md](docs/validation-lab.md) | runs on real hosts, and injected errors in a lab |
| [docs/release.md](docs/release.md) | how a release is built and verified |

## Contributing

[CONTRIBUTING.md](CONTRIBUTING.md) says how to build, test and send a change, and how the source tree is laid out. Security reports go the way [SECURITY.md](SECURITY.md) describes.

## License

Apache-2.0. See [LICENSE](LICENSE).
