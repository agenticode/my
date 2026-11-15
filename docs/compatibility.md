# Inputs

MemorY reads records a host and its BMC already keep. This page has one section per input: what is read, which observation grade the records get, and what is known to be missing.

The observation grade of a record says how much of the place of an error its source gave.

| Grade | Place known | What it allows |
| --- | --- | --- |
| A | bank, row and column | fault mode classification |
| B | physical address or rank | repetition per page and trends; a row fault is an estimate |
| C | the module, or a count only | trends and thresholds |

Every input gets an entry in the source inventory of a scan, also when it is absent or unreadable. The entry carries the status, the number of records read and skipped, and notes such as "3 records of other sensors were left out".

## What a platform gives

The same fault shows more or less of itself depending on who handles the error and which driver reports it.

| Environment | Inputs | Grade | Why |
| --- | --- | --- | --- |
| Intel Xeon from Skylake on, errors handled by the kernel | rasdaemon database, kernel log | A | `skx_edac` and `i10nm_edac` print rank, bank, row and column |
| Intel Xeon, Sandy Bridge to Broadwell | rasdaemon database, kernel log | B | `sb_edac` prints the rank and the address |
| AMD EPYC | rasdaemon database, kernel log | B | `amd64_edac` and the machine check give chip select, channel and address |
| Errors handled by firmware first (GHES), on x86 and ARM | rasdaemon database, kernel log | A to C | as much as the firmware put into the error record |
| CXL memory device | rasdaemon database | A | DRAM event records carry bank, row and column |
| Fleet with FMFM logging | FMFM logs | A to C | as much as the logging fills in |
| Host that cannot be reached | BMC event log, Redfish dump | C, B with a rank | a BMC names the module at best |
| No rasdaemon and no readable kernel log | EDAC counters | C | counts per DIMM since boot |
| NVIDIA GPU memory | Xid lines, `nvidia-smi -q` | C | the driver reports no location |

These grades are what the adapters and decoders give on the fixtures and test cases of this repository. They have not been confirmed on real hosts of each kind.

## How a file is recognised

`scan.Detect` picks the adapter for a file or a directory by its content. File names play no part.

| Content | Read as |
| --- | --- |
| directory with `proc`, `sys`, `var/log`, `var/lib/rasdaemon` or `sos_commands` below it | copy of a host's files, read like a live host |
| directory with Redfish resources in JSON files | Redfish dump |
| SQLite file | rasdaemon database |
| lines with a kernel stamp, `/dev/kmsg` records, `dmesg -T` dates or syslog lines | kernel log |
| JSON objects with `MESSAGE` and journal fields | kernel log from `journalctl -o json` |
| `# dmidecode` or `Handle 0x..., DMI type` lines | dmidecode output |
| lines or blocks of `ipmitool sel`, the table of `ipmi-sel`, 16-byte records, or a JSON list that names sensors | BMC event log |
| JSON with `@odata` properties | Redfish resource |
| `NVSMI LOG` text or an `nvidia_smi_log` XML document | `nvidia-smi -q` output |
| JSON with `ErrorDT`, `EcsModeRegisters`, `CurrentBootDT`, `InstalledMemory`, `SystemCrashDT` or `OsCrashDT` among its field names | FMFM log |

A file compressed with gzip is read through, except a database. A MemorY ledger, a zip archive and anything else are refused with a message that says what the content looks like.

What a file cannot say about itself is an option of the import:

| Option | `my import` | Applies to |
| --- | --- | --- |
| time zone of times that carry none | `--tz` | syslog files, `dmesg -T`, the text forms of a BMC event log, the timestamp of `nvidia-smi` output, times of an FMFM log written without a zone |
| capture time | `--captured-at` | a kernel log that has seconds since boot only |
| modification time of the file as its capture time | `--mtime` | the same log, and inventories that carry no time: dmidecode output, the memory resources of a Redfish dump, an FMFM boot record without a boot time |
| host | `--host` | a log that holds the records of several hosts (an FMFM log) |

## Which slot a record belongs to

Sources name the place of an error in their own way: an EDAC position, an SMBIOS handle, a label, or the numbers of socket, channel and slot. The ledger keeps every name a source used and ties each record to a slot through it.

An inventory defines the slots of a host and their names: the SMBIOS tables, the memory resources of a Redfish dump, the installed memory of an FMFM boot log. Redfish and FMFM also list each slot by its socket, channel and slot numbers; SMBIOS does not.

| A record names | It is tied to |
| --- | --- |
| a label | the slot with that name, ignoring case and separators: `DIMM A1` is the slot `DIMM_A1` |
| socket, channel and slot numbers | the slot an inventory lists at those numbers |
| a label or numbers no inventory knows | a slot of its own: the label as printed, or `socket0 channel2 dimm0` for numbers |
| no place | no slot; the record is counted for the host |

A record with a label and numbers is tied by the label when a slot has that name, and by the numbers otherwise.

A slot made from numbers lasts until an inventory lists the place. Its records then move to the slot of the inventory, so a runtime log imported before the inventory ends up in the same slots as one imported after it.

Numbers from two sources are taken to mean one place. Firmware logs and BMC inventories count channels within a socket. A source that counts them another way would be tied to the wrong slot; no input read today does so without also giving the memory controller.

EDAC positions carry the memory controller and are tied to SMBIOS slots on evidence only: equal labels, or socket, channel and position numbers that agree for every populated slot. The `slot-map` entry of the source inventory says what was tied and how.

## rasdaemon database

Adapter `rasdaemon`, package `internal/adapter/rasdaemon`. It reads the SQLite file rasdaemon keeps, `ras-mc_event.db`, with the tables of rasdaemon 0.6 to 1.0. Columns are read by name, because a database that was upgraded in place has its newer columns in another order than a fresh one.

A scan looks for the file at `/var/lib/rasdaemon/ras-mc_event.db` and `/usr/local/var/lib/rasdaemon/ras-mc_event.db`. On a live host it reads a private copy, so it takes no lock on a file rasdaemon is writing to. An imported file is read in place.

| Table | Ledger record |
| --- | --- |
| `mc_event` | error event from the EDAC report: severity, count, address, syndrome, the EDAC position and label, and what the driver detail carries |
| `mce_record` | error event from a machine check that reports a memory error; other machine checks are counted and left out |
| `extlog_event` | error event from a firmware error record (CPER), with the location fields the record marks valid |
| `memory_failure_event` | action `page-offline`, method `hard`, with the result the kernel reports |
| `cxl_*` | see "CXL memory devices" |
| `aer_event`, `non_standard_event`, `arm_event`, `devlink_event`, `disk_errors`, `signal_event` | not about memory cells; the report says how many rows each holds |

The driver detail of `mc_event` is read in the dialect of its driver, chosen from the keys in the text before any number is read: `skx_edac` and `i10nm_edac` print row and column in hexadecimal, `ghes_edac` prints them in decimal under the same names. Text in no known dialect is kept and not decoded.

A machine check and the EDAC report decoded from it are one error. The machine check is marked as a second report of the `mc_event` row within a second of it that names the same cache line, or the same chip select and channel, and the error is counted once.

### Grade

A for a record with row, column and bank. B for a record with an address or a rank. C otherwise.

### Known gaps

- The vendor records in `non_standard_event` and the processor records in `arm_event` are not read, so the vendor decoding rasdaemon does for some ARM servers is not used.
- rasdaemon stores what it traced while it ran. Errors from before it was started, and from a boot in which it did not run, are in the kernel log only.
- A database that fails the SQLite integrity check is refused.

## Kernel log

Adapter `klog`, package `internal/adapter/klog`. The kernel log is a lossy source: the ring buffer wraps and some messages are rate limited. A scan reads it in addition to the rasdaemon database, and an error both report is counted once.

### Accepted forms

| Form | Times |
| --- | --- |
| `journalctl -k -o json`, also for several boots | exact |
| syslog files (`/var/log/messages`, `kern.log`, `syslog`), with or without ISO dates | exact; the zone has to be given when the lines have none |
| `dmesg -T` | taken as printed; `dmesg` computes them from the uptime, so they are off after a suspend |
| `/dev/kmsg` records, `dmesg` with seconds since boot | see "Times" below |

A scan of a live host reads the journal of every boot on record, as far as the current user may, for two minutes at most. When that is not enough, the running boot is read in full and older boots in part, and the source inventory says so. Where the journal cannot be read, the scan reads the ring buffer of the running boot from `/dev/kmsg`, and where that fails the first of `/var/log/kern.log`, `messages`, `syslog` and `dmesg`. One of them is read, because the same line from two of them would count twice.

### What is read

| Lines | Ledger record |
| --- | --- |
| `EDAC MC0: 1 CE ...` | error event with the EDAC position, label, page, offset, syndrome and the driver detail |
| `{1}[Hardware Error]: ...` blocks of GHES | error event per memory error section, with the fields the firmware filled |
| `mce: [Hardware Error]: CPU 3: Machine Check: ...` with its `ADDR`, `MISC` and `TIME` lines, and the decode `mce_amd` prints | error event when the machine check reports a memory error |
| `Memory failure: 0x...: recovery action for ...`, soft offline messages | action `page-offline`, method `hard` or `soft`, with the result |
| `NVRM: Xid ...` | see "NVIDIA GPU memory" |
| `Linux version ...`, `DMI: ...`, the command line | a boot, with kernel, board and BIOS |

A line read from `dmesg` and the same line read from the journal are one record: a record is identified by the kernel's own stamp and its text.

### Grade

As for the rasdaemon database: A with row, column and bank, B with an address or a rank, C otherwise.

### Times

A syslog date has no year. It gets the latest year that does not put the line after the capture time, or after the time of the import when no capture time is given.

A capture with seconds since boot only says when an error happened relative to the boot. The start of the boot is taken from the first of these that is known:

1. On a live host, the boot time in `/proc/stat`. Times are exact.
2. A machine check record in the log, which carries its own wall-clock time. Times are exact.
3. A capture time (`--captured-at`, or `--mtime` for the modification time of the file): the boot started at that time minus the last stamp. Times are marked as estimated.
4. The line in which the kernel sets the system clock from the hardware clock. Times are marked as estimated.

With none of them the records stay relative to their boot. They are then dated by the records of another source that reports the same errors with a date, when there is one; the `time-align` entry of the source inventory says what was done.

### Known gaps

- Records that were overwritten in the ring buffer or dropped by the rate limit are missing. The EDAC counters show how many errors are known as a count only.
- A log file that starts in the middle of a boot has no boot banner for that boot.
- Machine check decoding covers the memory errors of Intel and AMD processors. Other machine checks, and firmware error sections that are not about memory, are counted in the report and left out.

## EDAC counters

Adapter `edacsysfs`, package `internal/adapter/edacsysfs`. It takes one reading of the counters under `/sys/devices/system/edac/mc`: `dimm_ce_count` and `dimm_ue_count` of every DIMM with its label, position, size and type, and the counts the driver could not tie to a DIMM (`ce_noinfo_count`, `ue_noinfo_count`).

The counters say how many errors a DIMM had since the driver was loaded and nothing about where in the DIMM. They start at zero with every boot.

Each reading becomes a counter event with the growth since the reading before it, or since the counter started. The ledger then sets how many of these errors the located records of the same slot and time window already account for. Totals count the remainder, so a host with a rasdaemon database and EDAC counters is not counted twice, and errors that only the counter saw are still counted.

### Grade

C.

### Known gaps

- A host without an EDAC driver for its platform has no counters, which is usual in virtual machines. The entry in the source inventory says so.
- A memory controller that only has the `csrow` directories of kernels before 3.6 is reported and not read.
- The counters cannot tell two errors in one cell from two errors in two cells.

## SMBIOS

Adapter `smbios`, package `internal/adapter/smbios`. It reads the memory device records (type 17): the slots of the board, and manufacturer, part number, serial number, size, type, speed and rank count of the DIMM in each. The system and board names come from types 1 and 2.

| Form | Remark |
| --- | --- |
| raw tables under `/sys/firmware/dmi/tables` | readable by root on most systems |
| `dmidecode` output, all types or `-t 17` or `-t memory` | for an import, and in a copy of a host's files |

The locator of a slot is its name in every finding. Where two slots have the same locator, the bank locator is added.

SMBIOS gives no error records and no grade.

### Known gaps

- Without the raw tables or `dmidecode` output a scan has no serial numbers. DIMMs are then told apart by slot, and a replaced DIMM cannot be told from its predecessor.
- Serial numbers that are placeholders (`NO DIMM`, `Unknown`, all zeros and the like) are not taken as identities.
- SMBIOS does not say which EDAC position a slot is. The two are tied on evidence only, see "Which slot a record belongs to".

## Host facts

Adapter `hostinfo`, package `internal/adapter/hostinfo`. It reads what a running Linux host says about itself from files any user may read: the host name, the kernel release, the architecture, the boot ID and boot time, the kernel command line, and the system, board and BIOS names under `/sys/class/dmi/id`.

The boot time dates kernel log lines that carry seconds since boot. The host name is the name of the case unless one is given.

## BMC event log (SEL)

Adapter `sel`, package `internal/adapter/sel`. A SEL is the log to read when the host cannot be reached, and it holds the error that took a host down before the operating system could record it.

### Accepted forms

| Form | Made with |
| --- | --- |
| list | `ipmitool sel list` or `ipmitool sel elist`, with or without `-c` |
| verbose | `ipmitool sel list -v` or `ipmitool sel elist -v` |
| ipmi-sel | `ipmi-sel` of FreeIPMI, default table |
| raw | `ipmitool sel writeraw <file>` |
| JSON | a list of entries, described below |

A file written by `ipmitool sel save` is refused with a message: it holds the events without their times.

### What is read

Records of the Memory sensor type (0Ch). Records of other sensors, OEM records and the kernel panic record are counted in the report and left out.

| Event | Ledger record |
| --- | --- |
| Correctable ECC | corrected error, count 1 |
| Uncorrectable ECC | uncorrected error |
| Parity | uncorrected error |
| Memory Scrub Failed | corrected error found by the patrol scrubber |
| Memory Device Disabled | action `disable` |
| Spare | action `sparing` |
| Correctable ECC logging limit reached, Configuration Error, Throttled, Critical Overtemperature | informational event, counted in no total |
| Presence Detected | left out |
| any other text on a memory sensor | informational event with the text as printed |

A deasserted record adds no error. A deasserted "Memory Device Disabled" withdraws the `disable` action the same log recorded for that module; both records are kept as informational events.

The verbose, raw and JSON forms carry the event bytes. With them the event is decoded from its offset, and the generator ID tells who logged it: a `disable` logged by the BIOS or its SMI handler has the method `bios`, one logged by the BMC `bmc`.

### Grade

C. A SEL record has no address, rank or device.

### Which module

The module is taken from the first of these that the record has:

1. Text the tool decoded from vendor bytes: `Correctable ECC (@DIMMB1(CPU1))` gives `P1-DIMMB1`, `Uncorrectable ECC ( DIMMA1)` gives `DIMMA1`, the extra field `CPU 1 DIMM 3` is used as printed.
2. A sensor name that names one DIMM: `Memory DIMM_C1`, `Memory CPU0_DIMM0_Status`.
3. Event data 3, when event data 1 marks it as the module number the specification defines. The label is then the sensor with the number, for example `sensor 0x53 module 3`.

A record that names no module this way has no slot. It is counted for the host and tied to no DIMM. A shared sensor such as `Memory Mem ECC Warning` is the usual case.

One error record that names two modules is counted once and tied to neither. A `disable` record that names two modules gives one action per module.

A label is matched to a known slot by name, ignoring case and separators: `DIMM A1` is the slot `DIMM_A1`. A label no other source uses becomes a slot of its own.

### Times

ipmitool prints the local time of the machine it runs on. Versions up to 1.8.18 do not name the zone; later versions follow the locale of that machine and append the zone abbreviation.

- A zone abbreviation with one meaning (UTC, KST, CEST, PDT and others) or a numeric zone (`+09`) is applied.
- A time without a zone, or with an abbreviation that has several meanings (CST, IST), is read as UTC, and the report says so. Pass the zone to the adapter (`sel.Options.Location`) to read such a dump correctly.
- Dates with slashes are read as month/day/year unless a date of the dump has a first number above 12. If no date tells the order, the report says so.
- A record from before the BMC had its clock set (`Pre-Init`, timestamps below 20000000h) is kept with a time relative to the start of the BMC and gets no date.
- The timestamps of the raw form are the BMC's clock and are read as UTC.

### Duplicates

Firmware often logs to the SEL an error the operating system also records. A SEL error record is marked as a duplicate when the host's own logs hold a record of the same severity class for the same slot within 2 minutes (`store.DuplicateTolerance.BMC`). A SEL record without such a counterpart is counted.

### JSON

A list of objects at the top level, or under one of the keys `entries`, `records`, `sel`, `events`, `items`, `members`, `log`, `data`. Keys are compared without case, spaces, underscores and dashes.

| Field | Keys |
| --- | --- |
| record ID | `id`, `record_id`, `record`, `index` |
| time | `timestamp`, `datetime`, `created`, or `date` with `time`; a number is seconds since 1970 |
| sensor type | `sensor_type`, `type`; a name or the type code |
| sensor name | `sensor`, `sensor_name`, `name` |
| sensor number | `sensor_number` |
| description | `event`, `description`, `message` |
| direction | `direction`, `state`, `entry_code`, or the boolean `asserted` |
| event bytes | `event_data` as `0xNNaabbcc` (direction and type byte, event data 1 to 3) or six digits of event data |
| whole record | `raw`: the 16 bytes of the record as hexadecimal |
| generator | `generator_id` |

Redfish log entries of type SEL are read by the Redfish adapter.

### Known gaps

- Most platforms log one "Correctable ECC" record when a count of corrected errors passes a limit. The number of corrected errors behind a record is unknown; it is counted as 1.
- After "Correctable ECC logging limit reached" or "Correctable memory error logging disabled" the log holds no further corrected errors. The report names the time of the first such record.
- OEM records (types C0h to FFh) are not decoded. Some vendors put the failing DIMM there.
- Vendor bytes in event data 2 and 3 are not decoded. The module is known only when the tool that printed the dump decoded it, or when the board has one sensor per DIMM.
- A BMC clock that differs from the host clock by more than the tolerance leaves SEL records and host records of the same error both counted.
- A `disable` imported from one dump stays in the ledger when a later dump of the same log shows the module back in use. The withdrawal only works within one dump.
- The text forms of other tools (vendor utilities, web interface exports) are not read.

## Redfish

Adapter `redfish`, package `internal/adapter/redfish`. It reads what a BMC serves over Redfish from files and does not talk to a BMC.

### Accepted forms

- One JSON file: a resource, a collection with its members expanded, an array of resources, or several JSON documents one after the other.
- A directory tree, as a mockup recorder or a crawl of the service leaves it. Every file that holds JSON is read, whatever its name. A resource that is in the tree twice is read once.

Three kinds of resource are read, recognised by `@odata.type`: `LogEntry`, `Memory` and `ComputerSystem`. A log entry or memory resource saved without its type is recognised by its properties. Vendor extensions under `Oem` are not interpreted. The whole resource, `Oem` included, is kept as the raw record of what was made from it.

### Memory resources

Each `Memory` resource is one slot.

| Property | Use |
| --- | --- |
| `DeviceLocator` | name of the slot; without it the service label, `Name` or `Id` |
| `Manufacturer`, `PartNumber`, `SerialNumber` | identity of the DIMM |
| `CapacityMiB`, `MemoryDeviceType`, `BaseModuleType`, `MemoryType`, `RankCount`, `DataWidthBits`, `BusWidthBits`, `OperatingSpeedMhz`, `AllowedSpeedsMHz`, `ModuleManufacturerID` | description of the DIMM |
| `ProductionDate` | manufacturing week |
| `MemoryLocation` | `Socket`, `Channel` and `Slot` become the place of the slot; `MemoryController` is left out |
| `Status.State` | `Absent` is an empty slot |
| `Status`, `Enabled` | marks, see below |

The mark a BMC puts on a module is the fact rule R-FW-01 reads:

| Status | Ledger record |
| --- | --- |
| `State` is `Disabled` or `UnavailableOffline`, or `Enabled` is false | action `disable`, method `bmc` |
| `Health` is `Critical` | action `mark-failed`, method `bmc` |
| `Health` is `Warning` | informational event |

A memory resource carries no time. The mark is recorded at the time of the dump (`redfish.Options.Now`, or the modification time of the files with `ModTimeAsObserved`), once per DIMM: reading the same state again adds nothing.

### Log entries

An entry is read in the first of these vocabularies that fits. Every state is mapped through the states of the IPMI memory sensor, so an event reads the same whether it came from a SEL dump or from Redfish.

| Vocabulary | How it is recognised | Read |
| --- | --- | --- |
| SEL | `EntryType` is `SEL` and `SensorType` is `Memory` | the event bytes in `MessageId` (`0xNNaabbcc`), or the name of the state in `Message`; `EntryCode` gives the direction, `GeneratorId` who logged it |
| OpenBMC registry | `MessageId` is `OpenBMC.x.y.Name` | `MemoryECCCorrectable`, `MemoryECCUncorrectable`, `MemoryParityCommandAndAddress`, `MemoryParityNotKnown`, `SparingRedundancyDegraded`, `MemoryThermTrip`; the arguments give socket, channel, DIMM and rank |
| `MEMnnnn` message IDs | `MessageId` ends in `MEM` and four digits | `MEM0000` to `MEM0010` follow the states of the IPMI memory sensor, `MEM0701` and `MEM0702` are a corrected error rate above a limit; the module comes from the arguments or from the text after "at location" |
| text | `Message` holds the name of an IPMI memory state, or names the module as `(Processor 1, Memory Module 4)` with "Uncorrectable Memory Error" or "Corrected Memory Error threshold exceeded" | the state and the module |

The states map to ledger records as in the SEL section above.

An entry that is about memory and fits no vocabulary is kept as an informational event with its message, and the report lists its message ID. An entry is taken to be about memory when its `OriginOfCondition` is a memory resource or its message mentions memory, a DIMM or ECC.

Entries of type `CXL` are counted and not read.

### Grade

C for most entries: they name a module at best. An OpenBMC registry entry that carries the rank has grade B.

### Which module

1. `Links.OriginOfCondition`, when it points at a memory resource. The label is the `DeviceLocator` of that resource if the dump holds it, and the last segment of the URI otherwise.
2. A DIMM name in the message or its arguments.
3. Socket, channel and DIMM numbers of an OpenBMC registry entry. They are matched with the `MemoryLocation` of the memory resources of the same dump. The numbers are also kept on the event, and name its slot when no inventory lists the place.
4. For SEL entries, event data 3 when the event bytes mark it as the module number.

### Times

`Created`, then `EventTimestamp`. A time without a zone is read as UTC. An entry about memory without a readable time is skipped and named in the report.

### Host

A `ComputerSystem` resource gives the host name (`HostName`), the system vendor and the model.

### Known gaps

- `MemoryMetrics` resources (error counts per module) are counted in the report and not read.
- The diagnostic data of an entry (`DiagnosticData`, CPER records) and the event record in an entry of type `CXL` are not decoded.
- Message registries of other vendors are not known. Their entries about memory are kept as informational events and listed in the report by message ID, so a missing class is visible.
- `MEM0008` ("is spare memory") is read as a spare taking over, like the IPMI state it follows.
- The mark on a module has the time of the dump, which is later than the time the BMC set it.
- `MemoryLocation.MemoryController` is not read. A BMC that counts channels per memory controller gives two slots the same socket, channel and slot numbers. Such numbers are left off both slots and the report counts them; a log entry that names its module by these numbers then has a slot named by the numbers.
- A `disable` in a log is withdrawn by a later deasserted entry for the same module only within one dump.

## NVIDIA GPU memory

Two inputs describe the memory of a GPU: the Xid lines the driver writes to the kernel log, and the state `nvidia-smi -q` prints. Both go to the `gpu_mem` table of the ledger, keyed by the GPU serial number, and stay apart from the DIMM events.

The driver does not report where in the memory of a GPU an error was. What can be read is a budget: rows remapped, a remap pending or failed, banks that still have spare rows. In the terms of the table at the top this is grade C.

### Xid lines

Read by the kernel log adapter (`klog`, source `klog/xid`) from every form of kernel log it reads.

```
NVRM: GPU at PCI:0000:3b:00: GPU-b850f46d-d5ea-c752-ddf3-c4453e44d3f7
NVRM: GPU Board Serial Number: 1650000000412
NVRM: Xid (PCI:0000:3b:00): 48, pid=7062, name=python3, An uncorrectable double bit error (DBE) has been detected ...
NVRM: Xid (PCI:0000:01:00 GPU-I:05): 94, pid=7194, Contained: CE User Channel (0x9). RST: No, D-RST: No
NVRM: Xid (0000:03:00): 14, Channel 00000001
```

The Xid numbers that report on GPU memory are stored, with the number and the text of the line (the process the driver names is left out of the text and stays in the raw record):

| Xid | Name in the vendor's catalog |
| --- | --- |
| 48 | double bit ECC error |
| 63 | GPU memory remapping event (on older GPUs: page retirement) |
| 64 | GPU memory remapping failure |
| 92 | high single-bit ECC error rate |
| 94 | contained memory error |
| 95 | uncontained memory error |
| 140 | ECC unrecovered error |
| 156, 157 | resource retirement event, failure |
| 160, 161 | channel retirement event, failure |
| 168 | reduced GPU memory capacity |
| 171, 172 | uncorrectable DRAM error, uncorrectable SRAM error |

Other Xids (application errors, bus and link errors) are counted in the report by number and left out.

A line names its GPU by the PCI address. The driver prints the UUID and the board serial number of the GPU at that address once per boot, before its first Xid; lines after that carry both. Times are those of the kernel log: exact with a journal or a syslog file, estimated from seconds since boot with a capture time, relative to boot without one.

### nvidia-smi -q

Adapter `nvidia-smi`, package `internal/adapter/nvidia`. It reads the text of `nvidia-smi -q` and the XML of `nvidia-smi -q -x`. One reading gives one snapshot per GPU.

| Value | Text | XML |
| --- | --- | --- |
| identity | `Serial Number`, `GPU UUID`, `Product Name`, the `GPU <address>` line | `serial`, `uuid`, `product_name`, the `id` of `gpu` |
| ECC in use | `ECC Mode` / `Current` | `ecc_mode/current_ecc` |
| ECC counts | `ECC Errors` / `Volatile` and `Aggregate`: `DRAM Correctable`, `DRAM Uncorrectable`, `SRAM Correctable`, `SRAM Uncorrectable` (also split into `Parity` and `SEC-DED`, which are added) | `ecc_errors/volatile` and `aggregate` |
| ECC counts of older GPUs | `Single Bit` and `Double Bit` / `Device Memory` and `Total`: the device memory is counted as DRAM, the rest of the total as SRAM | `single_bit`, `double_bit` |
| SRAM limit passed | `SRAM Threshold Exceeded` | `sram_threshold_exceeded` |
| retired pages (older GPUs) | `Retired Pages`: `Single Bit ECC`, `Double Bit ECC`, `Pending Page Blacklist` or `Pending` | `retired_pages` |
| remapped rows | `Remapped Rows`: `Correctable Error`, `Uncorrectable Error`, `Pending`, `Remapping Failure Occurred` | `remapped_rows` |
| spare rows per bank | `Bank Remap Availability Histogram`: `Max`, `High`, `Partial`, `Low`, `None` | `row_remapper_histogram` |

`N/A` means the GPU does not report the value; the snapshot leaves it unset. A GPU that reports none of these values is still listed, and the report says how many there are.

The time of a snapshot is the `Timestamp` of the output, which is the local time of the host without a zone. It is read as UTC unless the zone is given (`nvidia.Options.Location`). Output without a timestamp takes the time of the import.

On a live host the scan runs `nvidia-smi -q -x` when the program is in the path, with a limit of 30 seconds. The query only reads. A host without the program has no NVIDIA driver and gets no entry in the source inventory; a program that is there and fails (driver not loaded, no permission) is reported with its message. In a copy of a host's files the output is read from `sos_commands/nvidia/nvidia-smi_-q`.

### One GPU under several names

A snapshot knows serial number, UUID and PCI address. An Xid line from a boot in which the driver did not print the identity knows the PCI address only. When GPU records are read from the ledger, a record without a UUID takes it from a record with the same PCI address, of the same boot if there is one and nearest in time otherwise; serial number, index and model follow the UUID. The stored rows stay as their sources gave them, so a snapshot imported later completes Xid records imported before it.

### Known gaps

- DCGM is not read.
- The text of an Xid line is kept and not taken apart: the partition a double bit error names, for example, is not a field.
- The default table of `nvidia-smi` (without `-q`) and the files of `nvidia-bug-report.sh` are not read.
- A GPU that was replaced in the same slot between two readings is told apart by its UUID; an Xid record with a PCI address only, between the two readings, goes to the nearer one.
- GPUs of other vendors are not read.

## CXL memory devices

Read by the rasdaemon adapter from the `cxl_*` tables of a rasdaemon database, which hold the event records of CXL memory devices as the kernel traces them.

| Table | Ledger record |
| --- | --- |
| `cxl_dram_event` | error event with channel, sub-channel, rank, bank group, bank, row, column and nibble mask |
| `cxl_general_media_event` | error event with channel, rank and device |
| `cxl_memory_module_event` | informational event with the health flags, media status, life used, temperature and corrected error count; and an action `mark-failed` (method `device`) when the device says it needs to be replaced, once per device |
| `cxl_memory_sparing_event` | action `sparing`: cacheline, row, bank or rank, `hard` or `soft`, with rank, bank group, bank and row where the record marks them valid |
| `cxl_poison_event` | informational event per poisoned range, and per injection and clearing |
| `cxl_overflow_event` | a note in the report: how many records the device lost, and when |
| `cxl_generic_event`, `cxl_aer_ue_event`, `cxl_aer_ce_event` | counted in the report, not read |

The two media records number their event types differently, and each table is read with its own numbers. A media ECC error, a scrub media ECC error, a data path error and the expiry of the corrected error counter are errors: uncorrected when the descriptor says so, corrected otherwise. An invalid address, a TE state violation and a CKID violation are kept as informational events, because no memory cell failed.

How an error was found comes from the transaction type: a host read or write is a demand error; host scan media, the internal media scrub and the internal error check scrub are scrub finds.

A sparing record that only answers a query for spare resources is no repair and is left out. A result code other than zero is stored as a failed operation with the code in the detail.

The poison list is read out on request, so the same range comes back with every reading. Each range is kept once, at its first listing. Poison is the trace of an uncorrected error whose record is in the media tables, so an entry of the list is counted in no total.

### Grade

A for a DRAM event with its location, B for a DRAM event without one and for a general media event with a host address or a rank, C otherwise.

### Which device

The kernel's name of the device is the slot (`cxl:mem0`). The serial number in the tables is the DIMM identity: the history of a device follows its serial number, as it does for a DIMM. A device is recorded as present from its first to its last record in the database.

### Known gaps

- rasdaemon does not store the validity flags of the two media tables. A DRAM location of all zeros without a correction mask is taken as "not reported"; a real error at bank group 0, bank 0, row 0, column 0 would lose its location.
- The corrected error counts in the media records (`cme_count`, `cvme_count`) are kept in the raw record and not used.
- Post package repair of a CXL device has no table in rasdaemon and is not seen.
- The error check scrub log of a CXL device and the scrub controls the kernel shows under `/sys/bus/edac/devices` are not read.
- CXL event records inside Redfish log entries are not decoded.

## FMFM logs

Adapter `fmfm`, package `internal/adapter/fmfm`. The Fleet Memory Fault Management framework of the Open Compute Project asks an operator to keep three logs per host: a boot log, a runtime error log and a crash log. The error event of the ledger uses the field names of the runtime log, so a runtime record goes in field by field.

### Accepted forms

The framework names the fields of each log and leaves open how they are written as JSON. The adapter finds records by their field names:

- A record is a JSON object that has one of the marking fields in the table below.
- It may be the whole document, an element of a list, or sit under any key of an enclosing object. A file may hold several documents one after the other, one per line for example.
- A field may be in the record itself or in an object nested in it, such as `MemoryErrorLocation` in the second example.
- Field names are compared without case, spaces, underscores and dashes.

| Log | Marking fields |
| --- | --- |
| runtime | `ErrorDT`, `EcsModeRegisters` |
| boot | `CurrentBootDT`, `InstalledMemory`, `InstalledFirmware`, `SystemMemoryConfig`, `PrimordialBootDT`, `TrainingEventLogs` |
| crash | `SystemCrashDT`, `OsCrashDT` |

```json
{"ErrorDT": "2026:11:10:08:00:00:123", "ErrorSeverity": "CE", "ErrorScrubType": "Demand", "HostID": "r12-n034",
 "SocketId": 0, "ChannelId": 2, "ChannelSlotId": 0, "SubChannel": 1, "Rank": 0, "DeviceId": 7,
 "BankGroup": 0, "Bank": 3, "Row": "0x1a2f", "Column": 568, "ErrorVector": "dq:0x000c", "OsPhysicalAddress": "0x1f8bd19c0"}
{"ErrorDT": "2026-11-10T09:00:00.500Z", "ErrorSeverity": "CE", "ErrorScrubType": "Patrol scrub error",
 "MemoryErrorLocation": {"HostID": "r12-n034", "SocketId": 0, "ChannelId": 2, "ChannelSlotId": 0, "Rank": 0}}
```

A number may be a JSON number or text, decimal or with the prefix `0x`. A field that is `null` counts as absent. One file may be up to 256 MiB.

For the import to recognise a file, one of `ErrorDT`, `EcsModeRegisters`, `CurrentBootDT`, `InstalledMemory`, `SystemCrashDT` and `OsCrashDT` has to be in its first 256 KiB, spelled as here.

### Runtime records

| Field | Use |
| --- | --- |
| `ErrorDT` | time of the error; a record without a readable time is skipped |
| `ErrorSeverity` | `Informational`, `CE`, `Recoverable`, `UE` or `Fatal`; another word is reported and the record gives no event |
| `ErrorScrubType` | text that starts with `Demand` is a demand error, with `Patrol` a scrub find, anything else is kept as other |
| `SocketId`, `ChannelId`, `ChannelSlotId` | the module |
| `SubChannel`, `Rank`, `DeviceId`, `BankGroup`, `Bank`, `Row`, `Column` | the place in the module |
| `ErrorVector` | kept as written; a number is stored in hexadecimal |
| `OsPhysicalAddress` | physical address |
| `EcsModeRegisters` | ECS readings, see the next section |
| `HostID` | the host of the record |
| `DimmTemp` | stays in the raw record; the report says how many records have one |
| `ProcessorMetadata`, `PmicRuntimeRegisters` | not read; the report names them |

A record with `EcsModeRegisters` and no `ErrorSeverity` gives readings and no event. The framework names a module by numbers. A slot name is also read when a record has one, from `DeviceLocator`, `Locator`, `SlotLabel`, `DimmLabel` or `Label`.

### Boot records

| Field | Use |
| --- | --- |
| `CurrentBootDT` | a boot of the host that started then |
| `InstalledFirmware` | the firmware version of that boot: the text, or the field of the object whose name ends in `Version` |
| `InstalledMemory` | one module per entry, as seen at the boot time: socket, channel and slot numbers, a slot name if the entry has one, and the fields whose names end in `Supplier`, `PartNumber` and `SerialNumber` |
| `TrainingEventLogs` | one informational event per entry at the boot time, with the step that failed and the module the entry names |
| `SystemMemoryConfig`, `PrimordialBootDT`, `PmicBootErrorLog`, `DramOffsetRegisters`, `RcdControlWords`, `DataBufferControlWords`, `MemoryControllerMetadata` | not read; the report names them |

A log with one record per boot lists the memory once per boot. Every listing is kept with its boot time, so a module that was replaced between two boots is in its slot from the first boot that lists it.

A record without a readable `CurrentBootDT` still gives its modules, as seen at the time of the import. Its training events have no time and are reported as skipped.

A module in a place ECS registers were read from is recorded as DDR5.

Two entries of one record with the same socket, channel and slot numbers do not say which module a runtime record means. The numbers are left off both, and an entry without a slot name of its own is skipped. The report counts such entries.

### Crash records

| Field | Use |
| --- | --- |
| `OsCrashDT`, `SystemCrashDT` | time of the crash; the time of the operating system is used when the record has both |
| `FatalError` | set: a fatal error at the crash time, at the place the record names, with a text value kept as the error type. Not set: an informational event "system crash" |
| `PmicCrashRegisters`, `Uptime` | not read; the report names them |

`FatalError` is set when it is `true`, a number other than zero, or text other than `0`, `no`, `false`, `none` and `n/a`.

### Grade

A for a record with row, column and bank or bank group. B for a record with a physical address or a rank. C otherwise: crash records and training failures name a module at best.

### Which module

By the socket, channel and slot numbers, as described under "Which slot a record belongs to". The boot log lists the installed memory by the same numbers, so a runtime log imported with the boot log of its host has the errors under the serial numbers of the modules. Without a boot log or another inventory that lists slots by numbers, the slot is named by the numbers.

### Times

| Form | Example |
| --- | --- |
| the form the framework recommends | `2026:11:10:08:00:00:123`, with or without the milliseconds |
| RFC 3339 | `2026-11-10T08:00:00.123Z` |
| date and time with a space or slashes | `2026-11-10 08:00:00`, `2026/11/10 08:00:00` |
| a number | seconds since 1970; a number above 100000000000 is milliseconds |

A time without a zone is read as UTC unless a zone is given (`fmfm.Options.Location`).

### Host

A log may hold the records of several hosts. One import reads one host: the host that is asked for (`fmfm.Options.HostID`) when the log names it, and the host of the first record that names one otherwise. Host names are compared without case.

Records of the other hosts are left out, and the report names those hosts. When the host was not asked for, the records left out count as skipped and the source is marked as partly read.

A name the log does not use is taken as the name of the case, and the first host of the log is read into it.

### Reading a log again

The logs give their records no number. A record is identified by its content, and records with equal content by their order in the file. Reading a file again adds nothing, and a log that has grown can be read again for the new records. Two records that are equal in every field stay two errors.

An error that is also in the host's own logs is counted once when both records name the same cache line within 5 seconds, or the same slot when neither has an address.

### Known gaps

- The reader was written from the framework document and tested on files made for it. No log of a production fleet has been read yet.
- A log that uses other field names than the framework is not recognised.
- `DimmTemp` is not stored as a series, so a finding still lists the temperature history as missing.
- PMIC registers, RCD and data buffer control words, DRAM offset registers, the memory controller and processor metadata, the system memory configuration, the first boot time and the uptime are not read.
- `InstalledFirmware` gives one version. Versions of other firmware in the same field are not stored.
- Capacity, speed and type of a module are not read from the boot log.
- The same record exported twice with its time written in two forms is counted twice.
- A crash record gives the time and the fatal flag. What the registers of the record say about the crash is not decoded.

## ECS registers

A DDR5 device corrects single-bit errors in its own cells and counts them during its error check and scrub (ECS) passes. These errors do not reach the memory controller, so no error record exists for them. The counts are in the mode registers MR14 to MR20 of each device.

The ledger keeps the readings of these registers as a series of their own, in the table `ecs_sample`, apart from the error events. One reading is the registers of one device at one time. A reading counts in no total.

### Where readings come from

The field `EcsModeRegisters` of an FMFM runtime record, in one of these forms:

| Form | Example |
| --- | --- |
| registers by name | `{"MR14": 32, "MR15": "0x03", "MR19": 16, "MR20": 4}` |
| MR14 to MR20 in order | `[32, 3, 47, 26, 12, 16, 4]`, with `null` for a register that was not read |
| the seven bytes as hexadecimal text | `"20032f1a0c1004"`, also with spaces, commas or colons between the bytes |
| a list with one entry per device | `[{"DeviceId": 7, "MR14": 32, "MR20": 4}, {"DeviceId": 8, "MR14": 32, "MR20": 0}]` |
| an object keyed by device number | `{"3": {"MR20": 0}, "Device11": {"MR20": 1}}` |

In the first three forms the device is the `DeviceId` of the record. A list entry without a `DeviceId` takes its position in the list as the device number.

A register value is a number, or text: decimal, or hexadecimal with the prefix `0x`. Text that is no decimal number is read as hexadecimal, so `"2f"` is 47 and `"10"` is 10.

The time, the module and the rank of a reading are those of its record. A reading is tied to a slot and a DIMM the way an error event is.

### What the registers say

The registers are stored as read. `model.ECSSample` decodes them:

| Register | Value |
| --- | --- |
| MR14 | bit 5: the counter counts rows with errors, or code words with errors |
| MR15 | bits 2 to 0: the count per gigabit of cells from which the device reports, one of 4, 16, 64, 256, 1024 and 4096 |
| MR16, MR17, MR18 | the address of the row with the most errors in the last pass: row, bank and bank group |
| MR19 | the least number of errors that row had: bit n stands for 4 times 2^n or more |
| MR20 | the least number of rows or code words with errors per gigabit: bit n stands for the MR15 count times 2^n or more |

A register that was not read leaves the values that depend on it unset.

### Use

A finding that asks for more than watching lists the ECS counts of a DDR5 DIMM as missing data. It no longer does when the ledger holds readings of that DIMM.

`store.ECS` returns the readings of a host, a slot or a DIMM in time order.

### Known gaps

- The FMFM runtime log is the only input. The ECS controls the kernel shows under `/sys/bus/edac/devices` and the ECS log of a CXL device are not read.
- No rule of the engine reads the counts, and no command draws the series yet. The readings are in the ledger and in case bundles.
- The other bits of MR14 and MR15 (mode, counter reset, chip ID) are not decoded.
