# Inputs

MemorY reads records a host and its BMC already keep. This page has one section per input: what is read, which observation grade the records get, and what is known to be missing.

The observation grade of a record says how much of the place of an error its source gave.

| Grade | Place known | What it allows |
| --- | --- | --- |
| A | bank, row and column | fault mode classification |
| B | physical address or rank | repetition per page and trends; a row fault is an estimate |
| C | the module, or a count only | trends and thresholds |

Every input gets an entry in the source inventory of a scan, also when it is absent or unreadable. The entry carries the status, the number of records read and skipped, and notes such as "3 records of other sensors were left out".

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

A file compressed with gzip is read through, except a database. A MemorY ledger, a zip archive and anything else are refused with a message that says what the content looks like.

Three things a file cannot say about itself are options of the import: the time zone of times that carry none (syslog files, `dmesg -T`, the text forms of a BMC event log, the timestamp of `nvidia-smi` output), the time a kernel log with seconds since boot only was captured, and whether the modification time of a file may stand for the time it was saved.

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
3. Socket, channel and DIMM numbers of an OpenBMC registry entry. They are matched with the `MemoryLocation` of the memory resources of the same dump; the numbers are also kept on the event.
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
