# Inputs

MemorY reads records a host and its BMC already keep. This page has one section per input: what is read, which observation grade the records get, and what is known to be missing.

The observation grade of a record says how much of the place of an error its source gave.

| Grade | Place known | What it allows |
| --- | --- | --- |
| A | bank, row and column | fault mode classification |
| B | physical address or rank | repetition per page and trends; a row fault is an estimate |
| C | the module, or a count only | trends and thresholds |

Every input gets an entry in the source inventory of a scan, also when it is absent or unreadable. The entry carries the status, the number of records read and skipped, and notes such as "3 records of other sensors were left out".

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
