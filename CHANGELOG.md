# Changelog

Releases of MemorY, the latest first. Version numbers follow [Semantic Versioning](https://semver.org/). `my version --json` prints the versions of what a binary reads and writes: the analysis engine, the ledger schema, the case bundle format and the `--json` output.

## [1.1.0] - 2026-11-17

Engine 8, ledger schema 12, bundle format 1, JSON output 1. Archives for linux/amd64, linux/arm64 and darwin/arm64, built with Go 1.26.11 as for 1.0.0. A ledger of 1.0.0 named with `--ledger` is brought up to schema 12 in place, and `my open` brings a copy of a 1.0.0 bundle up to date; 1.0.0 refuses a ledger of schema 12 ([docs/data-model.md](docs/data-model.md)).

### Added

- `my board`: the board of a host, its slots by CPU and channel as the SMBIOS slot names give them, with the grade and the errors of each DIMM. The report draws the same board in section 2, with a card per slot. [docs/compatibility.md](docs/compatibility.md#slot-names-of-server-vendors) lists the slot names that are read.
- The Memory Explorer: the memory readings of a host (meminfo, vmstat, memory pressure, slab caches, CPU caches, `numactl --hardware`, `numastat`, `sar -B`, the state of MGLRU, `lshw`) are kept in the ledger and drawn in a new section of the report after the checks against a wrong diagnosis (section 7, or 8 after Module SPD), in five tabs. A report of a case without readings has eight sections as before. Its thresholds mark values and change no grade. The hints that name a state of the host (low available memory, memory pressure, unreclaimable slab, dirty pages, a large file cache, swap in use) are shown only with `--audience fa`.
- `my collect host -o DIR`: what a scan reads of this host, with the memory readings, copied into a directory that `my import` and `--root` read elsewhere. `--sudo` runs `dmidecode` and `lshw` through `sudo -n`.
- A scan runs `numactl`, `numastat` and `sar` when they are installed, each with a time limit; `my serve` and `my harvest` do not.
- Ledger schema 10: the table `host_reading`, and the number of sockets and the processor name of a host. Bundles carry the readings.
- Ledger schema 11: the CPUID signature of the processor of a host and which of its sockets hold a processor. The processor name is now that of the first populated socket.
- `my spd` and a report section Module SPD show what each DIMM says about itself in its SPD EEPROM: the manufacturing week, the location code, the part and serial number, ranks and device width (x4, x8), the die density and the package. Each reading is tied to its slot by serial number. A table of date codes counts the DIMMs with a finding beside those without one; `my lots` prints it alone, for one host or for every host of a ledger. A date code that DIMMs with a finding share is a lead for the supplier of the modules to check, and names no lot of the DRAM dies. A scan reads the SPD from the `eeprom` files of the kernel drivers `ee1004` (DDR4) and `spd5118` (DDR5), without root and without a write, in at most 5 seconds; `--no-spd` leaves it out and `my harvest` does not read it. `my spd FILE...` and `my import` take a dump as raw bytes, hex text or `decode-dimms` output.
- When the SPD cannot be read, the missing line, `my spd` and the report give the reason: a virtual machine, a kernel without the SMBus or SPD driver, a driver that is not loaded, no SPD device on the bus, pages that firmware blocks, a read that failed: 18 reasons, each given by a test ([docs/compatibility.md](docs/compatibility.md#reasons)). The SPD changes no grade, no finding and no exit code; the engine stays at version 8.
- Ledger schema 12: the table `spd_reading` with the bytes of each reading, and the reading kind `spd-probe` with what the scan saw of the path to the SPD. A DIMM tied to a reading takes its manufacturing week and device width from it ([docs/data-model.md](docs/data-model.md)). Bundles carry the readings; `my collect host` copies them into `spd-probe.txt` and `spd/`.
- The exporter: `my_dimm_spd_info`, `my_host_spd_read` and `my_host_spd_unread` ([docs/exporter.md](docs/exporter.md)).

### Changed

- The SMBIOS reader skips a memory device of type Flash, ROM, EPROM, FEPROM or EEPROM (a chip of the board) with a note, and reads once a record that a text holds twice.
- The board reads every slot name whole, by a table of 42 locator forms and 26 bank locator forms, and decides processor, channel and position of every slot: the processor word, a `Node` of the bank locator, the only processor, the processor of the other slots of a channel letter, channel letters split over the processors in order. Channels and slots are numbered from 0 in the vendor's order, and the vendor's name of each channel is drawn under it.
- A slot number without a channel (Dell `A7`, HPE `PROC 1 DIMM 9`, Lenovo `DIMM 7`) takes its processor, channel and position from the table of the board's family: 361 tables, each written from a public document of the maker that `internal/explorer/sources.tsv` cites with title, number and pages. A numbered slot of a board without such a table is drawn without channels.
- On a board whose memory controller is in the chipset (front-side-bus Xeon), no processor owns a slot: the board draws one controller in the place of the CPUs.
- Over the 2,044 server dumps of the Linux-Hardware.org DMI collection the board places 1,934 in full (the first version drew every slot of 1,077); the other 110 stand in a separate list with the reason their own records do not tell the place (`testdata/explorer/population/unplaceable.tsv`): `no-channel-in-document` 38, `unnamed-empty` 27, `duplicated-names` 12, `board-unknown` 10, `names-not-in-document` 6, `no-document` 5, `absent-processor` 4, `no-processor-in-document` 4, `processor-unknown` 2, `no-memory-device` 1, `processor-records-missing` 1 ([docs/compatibility.md](docs/compatibility.md#the-public-collection)). Tests hold every slot of the collection to its expected place and the board to 1,447 slots placed by hand from the vendor documents.
- The board is checked against its processor: no more sockets than processor records, no more channels than the processor has (46 processor families, by name or by CPUID signature), no more slots in a channel than the platform holds, no two slots in one place. A socket that fails is drawn without channels. Lines of data quality say which document a table comes from, which slots the manual names, which modules stand on an absent processor, and what could not be checked.
- The population check: the board holds the installed modules to the population the public documents of its maker give (`internal/population/sets.tsv`, 276 rule sets, 188 with a table of slots per number of modules, citing 353 documents with their pages). It marks with `+` an empty slot the table fills for the number of modules and with `-` a module in a slot the table leaves empty, and names each finding with its rule (P-ORDER-01, P-FIRST-01, P-CPU-01, P-BAL-01, P-MIX-01 to P-MIX-05), the strength of the document's wording and the document with its page, in `my board`, in section 2 of the report and in `--json` (`population`); `my why` names a finding of the slot of the DIMM. A population finding has no grade and changes no finding, confidence or exit code; the engine stays at version 8. `--detail` and `--audience fa` add the rules not checked and the vendor's table. Over the 2,044 server dumps of the public collection a rule set applies to 1,682: 1,434 reach a rule set with a table and 248 one of general rules; 252 reach no rule set, 109 are not placed in full and 1 has no slot. 201 dumps have a finding (`internal/view/testdata/population-coverage.txt`). `testdata/explorer/population-demo` is the demo case.

### Known limits

- No board and no population rule was checked against a real server. The board was checked against the public dumps and the 1,447 slots placed by hand, the rules against the documents they cite.
- The dumps of the separate list are drawn in part: a slot without a place stands below the board, or carries the label of the manual where the family lists the labels.
- A numbered slot of a board family without a public slot table is drawn without channels.
- Population rules exist for the board and processor families whose documents are public, and follow the revisions [docs/rules.md](docs/rules.md#population-rules) cites; a later revision with another table is not known. Firmware that reports no rank count or module type leaves the rules that need them unchecked.
- No SPD has been read on a real server. The decoder was checked against 8 public DDR4 and DDR5 dumps, the reader against made sysfs trees and in two emulated guests whose EEPROMs hold no SPD ([docs/validation-lab.md](docs/validation-lab.md#spd)). On most server boards Linux has no path to the SPD; MemorY then names the reason and reads a dump.
- No public dump covers DDR5 modules with stacked dies (3DS, DDP), asymmetric modules, MRDIMM or CUDIMM, or memory types other than DDR4 and DDR5. The tests hold DDR5 3DS and DDP, DDR4 DDP and the asymmetric layout to dumps made from a public one; a DDR3 dump is named by its type and not decoded.

## [1.0.0] - 2026-11-16

The first release: engine 8, ledger schema 9, bundle format 1, JSON output 1. Archives for linux/amd64, linux/arm64 and darwin/arm64, built with Go 1.26.11, `CGO_ENABLED=0`, statically linked. Two builds of the tag give the same bytes ([docs/release.md](docs/release.md)).

### Inputs

- rasdaemon: its SQLite database, and with `--fleet` its store on MySQL, MariaDB or PostgreSQL.
- The kernel log from the journal or `/dev/kmsg`, or as `dmesg` output, syslog files and `journalctl -o json`: EDAC lines, firmware error records (CPER), machine checks, page offlines, boots, NVIDIA Xid lines.
- What a boot that ended in a panic left behind: pstore and its archive by systemd-pstore, kdump logs, the ACPI boot error region (BERT), records left in the machine check banks.
- EDAC counters in sysfs and the counts of the mcelog daemon.
- SMBIOS tables or `dmidecode` output, for slots and DIMM serial numbers.
- A BMC: `ipmitool sel` listings, Redfish dumps, saved `racadm getsel` and `racadm lclog view` output, and the Redfish service itself, read with GET requests (`--bmc`, `my collect redfish`).
- GPU memory from `nvidia-smi -q` and its queries.
- OCP FMFM logs in JSON.
- A copy of a host's files (`--root`), and a directory of logs from any source (`my import`), recognised by content.

Each record has an observation grade: A when bank, row and column are known, B for an address or a rank, C for a module or a count. A finding says only what the grade of its records allows.

### Commands

22 commands. On a host or a case: `scan` (the default), `dimm`, `why`, `map`, `timeline`, `boots`, `gpu`, `inventory`, `bios`, `vendors`. Cases: `bundle`, `open`, `report`, `diff`, `import`, `export fmfm|events|labels`. A BMC alone: `collect redfish`. Crash loops: `harvest` with `install`, `uninstall` and `status`, and `preflight`. Monitoring: `serve --metrics`. Error injection: `verify`. And `version`.

Output in English or Korean (`--lang`), as JSON (`--json`), or drawn with ASCII characters (`--ascii`).

### Findings

- One ledger keyed by DIMM serial number. The history of a DIMM survives reboots and follows the DIMM to another slot.
- Fault modes of OCP FMFM Level 1: single-bit, row, multi-row, column, bank, device, DIMM.
- 20 rules, each with one of four grades: replace now, consider replacement, observe, hold replacement. A finding names its rules, the records behind it, the checks against a cause outside the DIMM and the data that was missing ([docs/rules.md](docs/rules.md)).
- The exit code is the grade, 0 to 4. 64 is a wrong command line, 70 a command that failed or a scan that could read no source.
- `--set key=value` and `--config FILE` change the thresholds.
- Memory messages of a BMC or a BIOS become one of 27 common events through a table per vendor or standard: DMTF registries, OpenBMC, the IPMI memory sensor, Dell iDRAC, and rules on wording ([docs/vendors.md](docs/vendors.md)).
- A vendor analyzer runs as a separate program behind `--plugin` and gets the records of each DIMM in FMFM field names ([docs/plugin-contract.md](docs/plugin-contract.md)).

### Bundles, reports and exports

- `my bundle` writes a `.mycase` file: a ZIP archive with the ledger, a manifest, the source inventory and a `README.txt`. `--anonymize` replaces host names, IP and MAC addresses, disk identifiers and the names of home directories with keyed words; DIMM serial numbers stay. `my open` computes the findings again from the records ([docs/bundle-format.md](docs/bundle-format.md)).
- `my report` writes one HTML file with SVG figures, in English or Korean. It loads nothing from elsewhere and prints to PDF. `--audience fa` adds the detail failure analysis needs.
- `my diff` compares two cases of one host, before and after a reseat, a slot swap, a replacement or a firmware update.
- `my export events` writes every record as a line of JSON in the schema `my-events/1`; `my export fmfm` an FMFM analysis log; `my export labels` a DIMM label file for rasdaemon.

### Exporter

`my serve --metrics` serves 20 metrics named `my_*`, among them `my_dimm_verdict` for the finding of each DIMM, and the finding of one DIMM as JSON at `/api/dimm/<slot|serial>`. With `--fleet` one exporter reads the store of a fleet ([docs/exporter.md](docs/exporter.md)). [contrib/](contrib/) holds systemd units, Prometheus alert rules, Alertmanager routes and a Grafana dashboard.

### What it was checked against

- 17 lab captures in `testdata/real`, made on a lab host and in QEMU guests (x86 and aarch64, Linux 7.3.0 and 6.19.10) with injected page offlines, machine checks, firmware error records, CXL event records and crash loops. The tests read each capture as a copy of the host, as loose files and through the bundle written on the host, and compare it with its injection plan ([docs/validation-lab.md](docs/validation-lab.md)).
- The DRAM error log that Alibaba published in dcbrain: 75,214,692 corrected errors on 35,240 DIMMs of 30,502 servers, and 3,017 failure tickets. With engine 6, a server with "consider replacement" got a ticket within 30 days in 2.48% of the cases (2.56% in the held-out period), a server with "observe" in 0.26% (0.23%) ([docs/evaluation.md](docs/evaluation.md)).
- Five Linux hosts, x86_64 and aarch64, all of them virtual machines, as root and as an unprivileged user. The last pass ran 433 commands without a problem ([docs/validation.md](docs/validation.md)).
- 50 fuzz targets ([docs/fuzzing.md](docs/fuzzing.md)).

### Known limits

- No BMC of a real server has been read. The vendor tables rest on public registries and documents; none has been checked against the log of a real machine.
- Crash loops ran in emulated guests only. Whether a real board keeps a machine check record in its banks over the reset after a fatal error is not known, nor how the boot error region and the error record store of real firmware behave under repeated panics ([docs/crash-loop.md](docs/crash-loop.md)).
- The thresholds are defaults. They were compared with one public field dataset, not with failure analysis results.
- No host with the EDAC driver of a real memory controller has been run. The decoding of such records rests on fixtures written from the kernel and rasdaemon sources.
- No analyzer of a memory vendor has been run against the plugin contract.
- Under `--lang ko`, the stop reasons of the Redfish collector, the failure details of a plugin and some details of sources stay in English.

[1.1.0]: https://github.com/agenticode/my/releases/tag/v1.1.0
[1.0.0]: https://github.com/agenticode/my/releases/tag/v1.0.0
