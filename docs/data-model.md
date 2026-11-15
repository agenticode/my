# Data model

MemorY keeps what it reads in a ledger: one SQLite file. This page says what the ledger holds, how a DIMM and a record are identified, and how the records of several sources become one count of errors.

A scan keeps its ledger for the length of the run. `--ledger FILE` keeps it and adds to it on every run, and a case bundle carries one ([bundle-format.md](bundle-format.md)). Any SQLite tool opens it. The errors per slot of the example case:

```
$ unzip -q examples/demo.mycase ledger.db
$ sqlite3 ledger.db 'SELECT slot, "ErrorSeverity", SUM(count - covered) FROM event WHERE dup_of IS NULL GROUP BY 1, 2'
CPU0_C1|CE|1231
CPU0_C1|UE|1
CPU1_D1|CE|53
```

## The file

| Property | Value |
| --- | --- |
| format | SQLite 3, one file |
| application ID | `0x4D594C47` ("MYLG"); a SQLite file with another ID is refused |
| schema version | `PRAGMA user_version`, 6 in this release |
| times | UTC text of the form `2026-11-12T03:14:07.000000Z`, which sorts as text |
| unsigned 64-bit values | physical addresses and syndromes are stored as the signed integer with the same bits |
| column names | a column that holds a field of the FMFM runtime error log keeps the FMFM spelling (`"ErrorDT"`, `"SocketId"`, `"OsPhysicalAddress"`); what MemorY adds is in lower case ([fmfm-mapping.md](fmfm-mapping.md)) |

A binary reads ledgers of its own schema version and of older ones. A ledger named with `--ledger` is brought up to date in place. A ledger or a bundle given to `my open` is never changed: an older one is copied and the copy is brought up to date. A ledger of a newer schema is refused with a message that says so.

## Tables

Records read from the sources:

| Table | A row is |
| --- | --- |
| `event` | one error report, or the growth of an error counter between two readings |
| `event_raw` | the original text of an error record: the log lines or the database row |
| `action` | a repair or isolation step the host recorded: a page taken offline, a post package repair, sparing, a row remap, a reseat, a replacement, a module that firmware disabled or marked as failed |
| `counter_sample` | one reading of a per-DIMM error counter (EDAC, the mcelog daemon) |
| `gpu_mem` | a GPU memory record: a snapshot of the row remapping state and the ECC counters, or an Xid event |
| `ecs_sample` | one reading of the DDR5 error check and scrub registers MR14 to MR20 |
| `bmc` | one reading of a BMC: its firmware, the BIOS version, which resources it gave, the period its logs cover |
| `bios_setting` | a setting of the system firmware that bears on memory errors, with the value that waits for the next boot |
| `bmc_memory` | a memory module as a BMC describes it, with the error counts the BMC keeps |

Identities and places:

| Table | A row is |
| --- | --- |
| `host` | a host: name, architecture, system vendor and product, and the hypervisor when it is a virtual machine |
| `boot` | one boot: start time, kernel release, BIOS and BMC version, kernel command line |
| `dimm` | a DIMM identity: manufacturer, part number, serial number, size, type, ranks, device width, speed |
| `slot` | a physical slot of a board, with the names the sources use for it |
| `slot_alias` | one name a source uses for a slot |
| `placement` | a DIMM seen in a slot, from when to when |

Collection runs:

| Table | A row is |
| --- | --- |
| `scan` | one run: a scan of a host, an import of files, or a synthetic data set; when, by which tool version, whether as root |
| `source` | one source the run looked for: what was read, its status, the number of records, events and actions, the observation grades, the first and the last time, and remarks |

Derived by the engine:

| Table | A row is |
| --- | --- |
| `fault` | a group of errors with one cause: mode, place, period, counts |
| `fault_event` | an error record that belongs to a fault |
| `verdict` | a finding: grade, rules, evidence, checks, missing data, next step and confidence, as JSON statements with a code, arguments and the English sentence |

`meta` holds the identity of the ledger (`ledger_id`, `created_at`, `created_by`), one `migration.NNNN` entry per schema step, and under `analysis.<host>` what the stored findings were computed with: engine version, reference time, grade, and the thresholds that differ from the defaults.

Faults and findings are computed on every run and can always be computed again from the records. They are stored when a bundle is written from a scan, an import or a collection, so that the receiver sees what the sender saw. `my open` computes them again and says where its result differs from the stored one: another engine version, other thresholds, another grade.

## Identities

### DIMM

`dimm_uid` is "d" and 19 hexadecimal digits of a hash over manufacturer, part number and serial number, each in upper case with white space folded. The host and the slot are not part of it, so the history of a DIMM follows it to another slot and to another host. A serial number that is empty, a firmware placeholder ("NO DIMM", "Not Specified", "SerNum00") or all zeros or all F identifies nothing. Such a DIMM gets a placeholder, "s" and 19 digits of a hash over host and slot, with `identity = 'slot'`. Its history stays with the slot, and its finding says that no serial number is on record.

### Slot

A slot is named by the SMBIOS device locator when SMBIOS was read, with the bank locator added where two slots share a locator. Without SMBIOS it is named by the EDAC label or position, or by what the source of a record calls the place. `slot_alias` holds every other name: the SMBIOS handle, the EDAC memory controller and layer position, the label, and the numbers of socket, controller, channel and position. An error record is tied to a slot through the alias of the name its source used. An EDAC position is tied to an SMBIOS slot on evidence only: the EDAC label equals the locator, or the numbers of both sides agree for every populated slot. Without that the EDAC position stays a slot of its own, and the `slot-map` entry of the source inventory says so.

### Placement

Every reading of the inventory records that a DIMM was seen in a slot at that time. Sightings that agree extend a placement; a DIMM that appears in another slot starts a new one. The number of slot changes of a DIMM is counted from these rows.

### Boot

A boot is keyed by the kernel boot ID when a source recorded it, otherwise by an ID derived from host and start time ("derived-" and 16 digits). A boot seen once with its kernel ID and once without is one row: two start times within 30 seconds, at least one of them without a kernel ID, are the same boot.

### Record

`fingerprint` is a hash over what identifies the original record, and it is unique in the ledger: reading a source again adds nothing. `raw_ref` says where the record was read (file and line, table and row), and `event_raw.text` holds its text. Every statement of a finding points at records through these.

## From records to errors

An `event` row is one of two kinds. `kind = 'error'` is one report of a source that logs each error. `kind = 'counter'` is the growth of a counter between two readings: it has a `count`, the window `window_start` to `"ErrorDT"`, and no place inside the DIMM.

| Column | Meaning |
| --- | --- |
| `"ErrorSeverity"` | `CE` counts as a corrected error; `Recoverable`, `UE` and `Fatal` count as uncorrected errors; `Informational` records are never counted |
| `"ErrorScrubType"` | `Demand`, `Patrol` or `Other`; empty when the source does not say how the error was found |
| `grade` | the observation grade: `A` when row, column and bank are known, `B` for a physical address or a rank, `C` for a count or a module only |
| `count` | the errors the record stands for |
| `covered` | on a counter record: how many of its errors other records already count |
| `dup_of` | the record that reports the same error with more detail |
| `time_quality` | `exact` for a wall-clock time, `estimated` for a time worked out from seconds since boot, `relative` when only seconds since boot are known |
| `dimm_uid`, `dimm_link` | the DIMM the error is attributed to, and how |
| `decoder` | the format the place was decoded from (`edac/skx-adxl`, `cper`), which tells a reader how to check it |

Every total is computed from this table by one rule:

```
errors = SUM(count - covered) over rows with dup_of IS NULL
```

### Duplicates

One error often reaches the ledger through several sources: the rasdaemon database, the kernel log, the event log of the BMC. An adapter marks two of its own records as one error where it knows that (a machine check record and the EDAC record decoded from it). Records of different sources are one error when they have the same severity class and the same cache line address, or the same slot where neither has an address, and their times lie within 5 seconds. The tolerance is 2 minutes when a time was worked out from seconds since boot, and 2 minutes between a BMC log and the logs of the host, which two clocks stamped. A group holds at most one record per source. The record with the best observation grade is counted; the others stay in the ledger and point at it.

### Counters

A counter reading is not an error record. The ledger turns two readings of one counter into a counter event for the growth between them; a counter that went backwards or was reset starts a new period. `covered` is the part of that growth the ledger already counts: the located error records of the same severity class on the same slot inside the window, and what the counter of another source counts beyond those. A host with a rasdaemon database and EDAC counters is therefore not counted twice, and errors that only a counter saw are still counted.

### Times

A kernel log saved with `dmesg` carries seconds since boot. Its records get dates when another source holds the same errors with dates: pairs of records with the same cache line and severity class vote for the start of the boot, and when enough agree the records of the boot are moved and marked `estimated`. Records that stay `relative` are in the counts and not on the timeline. rasdaemon records are given the time of the kernel log of the same boot where the two show that the clock of the daemon was off, as it is when NTP sets the clock after the daemon started. The `time-align` entry of the source inventory says what was moved.

### Attribution

An event is tied to a slot first and to a DIMM through the placements of that slot:

| `dimm_link` | Condition |
| --- | --- |
| `observed` | the DIMM was seen in the slot at a time that covers the event |
| `assumed` | the event lies before the first or after the last sighting, and nothing shows another DIMM in the slot |
| `ambiguous` | the slot held different DIMMs before and after the event and the time of the swap is unknown; the event has no DIMM and counts for the host finding |
| `slot` | no identity was read for the slot; the event belongs to the placeholder |

A swap happens at a boot. With one boot between the sightings of two DIMMs, the events are split there. A page offline names an address and no slot; it takes the slot of an error on the same page.

These passes run after every read, in a fixed order, and each computes its result from the rows of the ledger. Reading the sources in another order, or reading one again, gives the same ledger.

## Readings that are not events

`counter_sample`, `gpu_mem`, `ecs_sample`, `bmc`, `bios_setting` and `bmc_memory` hold readings. No total of the ledger includes them. Counter readings become counter events as described above. The GPU records are what the finding of a GPU rests on. ECS readings and BIOS settings are read for what they say about the records of a DIMM: a finding notes a DDR5 DIMM without ECS readings, a patrol scrubber that the BIOS has switched off, and a firmware that logs no corrected errors to the BMC. `my bios` checks the settings against a profile. The error counts a BMC keeps per module are stored as the BMC gives them.

## Actions

`action.kind` is one of `page-offline`, `sppr`, `hppr`, `sparing`, `row-remap`, `replacement`, `reseat`, `disable` and `mark-failed`, with a `result` of `ok`, `failed`, `pending` or `unknown`. They are steps the kernel, the firmware, a daemon or a person took and a source recorded. MemorY records them and takes none.

## JSON output

`--json` prints the data behind a screen in one object:

| Field | Content |
| --- | --- |
| `view` | what `data` holds: `overview`, `hosts`, `dimm`, `why`, `map`, `timeline`, `gpu`, `diff`, `verify` |
| `version` | the version of this form, 1. It changes when a field is renamed or removed; new fields do not change it |
| `lang` | the language of the sentences |
| `host` | the host the data is about |
| `grade`, `exit_code` | the finding the exit code stands for, and the exit code |
| `case` | the bundle the data comes from, when it comes from one |
| `analyzer` | the vendor analyzer the run used ([plugin-contract.md](plugin-contract.md)) |
| `data` | the data of the screen |

A statement in `data` carries its `code`, its `args` and the sentence in `text`, so that a program can read the finding without parsing sentences.
