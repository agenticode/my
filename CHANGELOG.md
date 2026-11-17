# Changelog

Releases of MemorY, the latest first. Version numbers follow [Semantic Versioning](https://semver.org/). `my version --json` prints the versions of what a binary reads and writes: the analysis engine, the ledger schema, the case bundle format and the `--json` output.

## [Unreleased]

Ledger schema 11.

### Added

- `my board`: the board of a host, its slots by CPU and channel as the SMBIOS slot names give them, with the grade and the errors of each DIMM. The report draws the same board in section 2, with a card per slot. [docs/compatibility.md](docs/compatibility.md#slot-names-of-server-vendors) lists the slot names that are read.
- The Memory Explorer: the memory readings of a host (meminfo, vmstat, memory pressure, slab caches, CPU caches, `numactl --hardware`, `numastat`, `sar -B`, the state of MGLRU, `lshw`) are kept in the ledger and drawn in a new section 7 of the report, in five tabs. A report of a case without readings has eight sections as before. Its thresholds mark values and change no grade; `--audience fa` adds its hints in words.
- `my collect host -o DIR`: what a scan reads of this host, with the memory readings, copied into a directory that `my import` and `--root` read elsewhere. `--sudo` runs `dmidecode` and `lshw` through `sudo -n`.
- A scan runs `numactl`, `numastat` and `sar` when they are installed, each with a time limit; `my serve` and `my harvest` do not.
- Ledger schema 10: the table `host_reading`, and the number of sockets and the processor name of a host. Bundles carry the readings.
- Ledger schema 11: the CPUID signature of the processor of a host and which of its sockets hold a processor. The processor name is now that of the first populated socket.

### Changed

- The SMBIOS reader skips a memory device of type Flash, ROM, EPROM, FEPROM or EEPROM (a chip of the board) with a note, and reads once a record that a text holds twice.
- The board reads every slot name whole, by a table of 42 locator forms and 26 bank locator forms, and decides processor, channel and position of every slot: the processor word, a `Node` of the bank locator, the only processor, the processor of the other slots of a channel letter, channel letters split over the processors in order. Channels and slots are numbered from 0 in the vendor's order, and the vendor's name of each channel is drawn under it.
- A slot number without a channel (Dell `A7`, HPE `PROC 1 DIMM 9`, Lenovo `DIMM 7`) takes its processor, channel and position from the table of the board's family: 361 tables, each written from a public document of the maker that `internal/explorer/sources.tsv` cites with title, number and pages. A numbered slot of a board without such a table is drawn without channels.
- On a board whose memory controller is in the chipset (front-side-bus Xeon), no processor owns a slot: the board draws one controller in the place of the CPUs.
- The board is checked against its processor: no more sockets than processor records, no more channels than the processor has (46 processor families, by name or by CPUID signature), no more slots in a channel than the platform holds, no two slots in one place. A socket that fails is drawn without channels. Lines of data quality say which document a table comes from, which slots the manual names, which modules stand on an absent processor, and what could not be checked.

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

[1.0.0]: https://github.com/agenticode/my/releases/tag/v1.0.0
