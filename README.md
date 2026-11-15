# MemorY

MemorY is a read-only analysis tool for memory errors on Linux servers. It reads the records a host already keeps (the rasdaemon database, the kernel log, EDAC counters, SMBIOS, and dumps of a BMC) and answers three questions about them: which DIMM, what kind of fault, and what to do about it, with the reasons.

The records go into one event ledger keyed by DIMM serial number, so the history of a DIMM survives reboots and follows the DIMM when it moves to another slot. Every finding names the rule that made it, the records it rests on, the checks against other explanations and the data it did not have.

The command is `my`. It never offlines pages, triggers repairs or resets counters.

## What it shows

The output below comes from a synthetic host that anyone can generate: `my synth -scenario demo -ledger demo.db`. On a Linux server, `my` without arguments scans the host itself and draws the same screen.

```
$ my open demo.db
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

 my open demo.db dimm CPU0_C1: examination of this DIMM
 my open demo.db why CPU0_C1: what the finding rests on   my help: how to read the figures
```

One DIMM, examined: the finding, the branch of the DIMM the errors sit on, the address map of the bank with the most errors, and the timeline across reboots.

```
$ my open demo.db dimm CPU0_C1
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

In the address map a horizontal line is a row fault, a vertical line a column fault, one cell that repeats a single-bit fault, and a cloud over the bank a bank fault. `my help reading` explains every figure.

## Commands

| Command | What it does |
| --- | --- |
| `my` or `my scan` | scan this host: slot map and DIMMs ranked by risk |
| `my dimm <slot\|serial>` | examine one DIMM: finding, topology bars, address map, timeline |
| `my why [slot\|serial]` | the finding with the records, checks and confidence behind it; `--oneline` gives one line for a ticket |
| `my map <slot\|serial>` | the address map of one bank; `--bank`, `--rank`, `--device` choose it, `--rows A-B` zooms |
| `my timeline [slot\|serial]` | errors per day with boots, repairs, page offlines, slot changes and uncorrected errors |
| `my bundle [slot\|serial] -o case.mycase` | write the records of this host as one file to send or attach |
| `my report [FILE] -o report.html` | write the findings of a case, or of this host, as one HTML file with the figures as SVG; it prints to PDF, and `--audience fa` adds the detail failure analysis needs |
| `my open FILE [command ...]` | run the commands above on a bundle or a ledger file instead of on this host |
| `my import <file\|directory>...` | build a case from files: a rasdaemon database, kernel logs, dmidecode output, BMC dumps, `nvidia-smi -q` output, FMFM logs, a copy of a host's files |
| `my help [command]` | flags of a command and how to read what it prints |

A scan needs no configuration and no root. It looks for the rasdaemon database, the systemd journal (or `/dev/kmsg`, or a log file under `/var/log`), the EDAC counters and the SMBIOS tables. A source it may not read is named on a "missing" line together with what a scan as root would add. A scan keeps nothing on the host unless `--ledger FILE` asks it to.

`--lang en|ko` chooses the language; the default follows the locale. `--json` prints the data behind a figure. `--ascii` draws with ASCII characters, and a grade is always told by the density of a glyph as well as by its colour. By default colour is used on a terminal only, and not when `NO_COLOR` is set.

```
$ my open demo.db why CPU0_C1 --oneline
r12-n034 CPU0_C1 S/N 3B9C56: replace now (finding, confidence high; R-UE-01, R-ROW-03, R-REP-02). 1 uncorrected error, the latest on 2026-11-12 03:14 UTC at rank0 dev7 bg0 bank3 row 0x1a2f col 0x238. Replace the DIMM. Attach a case bundle of this DIMM to the return.
```

## Findings and exit codes

A command that shows findings exits with the grade of what it showed, the host or the DIMM it was asked about:

| Code | Grade | When |
| --- | --- | --- |
| 0 | ok | no errors on record |
| 1 | observe | corrected errors without a growing fault |
| 2 | consider replacement | a fault is rising or spreading, pages keep being taken offline, the patrol scrubber found an uncorrected error, or errors came back after a repair |
| 3 | replace now | an uncorrected error, a DIMM that firmware marks as failed, or repair resources that are used up |
| 4 | hold replacement | a cause outside the DIMM is suspected: the channel, the slot, the contact, or slot names that disagree |

A grade is a finding. The thresholds behind it are defaults that have not been compared with failure analysis results. `my help rules` lists the 13 rules, `my help thresholds` the thresholds, and `--set key=value` or `--config FILE` changes them. [docs/evaluation.md](docs/evaluation.md) describes how findings are compared with the failure tickets of a public fleet dataset.

Platforms report different amounts of location detail, so every record has an observation grade and a finding only says what the grade allows: A (bank, row and column known: fault modes can be told), B (physical address or rank: a row is an estimate), C (a count per DIMM: trends only). [docs/compatibility.md](docs/compatibility.md) says what each input gives.

## Case bundles

`my bundle` writes what a scan found as one `.mycase` file, and `my open` shows it elsewhere with the screens the sender saw. Here the demo ledger is bundled; on a host the command is `my bundle CPU0_C1 -o case.mycase --anonymize`.

```
$ my open demo.db bundle CPU0_C1 -o case.mycase --anonymize
case bundle written: case.mycase (163.4 kB)
  host host-d712d956: 16 DIMMs, CE 1,284, UE 1, finding: replace now
  subject of the case: CPU0_C1 S/N 3B9C56, finding: replace now
  host names replaced:
    r12-n034 -> host-d712d956
  Keep these lines: the bundle does not hold the names, and an answer about it will use the new
  ones.
look at it: my open case.mycase
```

A bundle is a ZIP archive: `manifest.json` (what was collected, with the checksum of the ledger), `sources.json` (every source that was looked for and what it yielded), `ledger.db` (the SQLite ledger) and `README.txt`. It holds records about memory errors and never the contents of memory. `--anonymize` replaces host names, IP addresses and MAC addresses by words made from a keyed hash; DIMM serial numbers stay, because a return is about them.

## Status

Early development. In this tree: the ledger, the input adapters, the analysis engine, the commands above with their terminal figures, case bundles and the HTML report. Not merged yet: the Prometheus exporter and the vendor analyzer plugin contract. The commands are tested on synthetic hosts and fixtures, and CI scans its own machine, a virtual machine without memory controllers, as a normal user and as root. Hosts with EDAC drivers and rasdaemon have not been validated yet.

## Build

Go 1.26 or newer.

```
make build    # bin/my
make test
make golden   # rewrite the golden files of the figures after an intended change
make fuzz     # each fuzz target for 10 s (FUZZTIME=1m for longer)
make cross    # dist/my-linux-amd64, dist/my-linux-arm64, dist/my-darwin-arm64
```

The binary is pure Go and statically linked (`CGO_ENABLED=0`).

## Layout

| Path | Content |
| --- | --- |
| `cmd/my` | the binary and its commands |
| `internal/model` | ledger records; the error event uses the FMFM runtime error log field names |
| `internal/store` | the ledger: one SQLite file, schema migrations, totals, consistency passes |
| `internal/adapter` | readers for the rasdaemon database, the kernel log, EDAC sysfs, SMBIOS, host facts, the BMC event log, Redfish dumps, `nvidia-smi` output and FMFM logs, and for the dcbrain DRAM error dataset |
| `internal/decode` | EDAC driver detail strings, CPER memory error records, machine check status, IPMI memory sensor events |
| `internal/scan` | finds the sources of a host, or of a copy of its files, and recognises loose files by their content |
| `internal/ingest` | runs adapters against a ledger and records what each source yielded |
| `internal/engine` | the analysis engine: groups the errors of a DIMM into faults with a mode (single bit, row, multi-row, column, bank, device, DIMM, system issue), measures trend, spread and baselines, and applies 13 rules with fixed IDs to reach a finding per DIMM |
| `internal/view` | the data behind every figure, built from a ledger and the engine result; also the JSON output |
| `internal/term` | draws the figures as text: colour, glyph density, ASCII, width |
| `internal/report` | the HTML report: one file with inline SVG figures, the records behind them, and nothing loaded from elsewhere |
| `internal/bundle` | case bundles: format, checks, anonymizing |
| `internal/eval` | replays the dcbrain DRAM error dataset through the engine and compares the findings with the failure tickets that followed |
| `internal/i18n` | message templates in English and Korean |
| `internal/synth` | synthetic hosts and error histories for tests and demos |
| `testdata` | fixtures for every adapter |

Three development commands are left out of the command list: `my synth` generates synthetic hosts (`my synth -list` names the scenarios), `my analyze` prints the engine result of a ledger, and `my eval` runs the comparison of [docs/evaluation.md](docs/evaluation.md).

## License

Apache-2.0. See [LICENSE](LICENSE).
