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
| Fleet whose rasdaemons write to a MySQL or PostgreSQL server | rasdaemon store, read with `--fleet` | as the hosts' own databases | the rows are those each host would keep in its SQLite file, without serial numbers |
| Host that cannot be reached | BMC event log, Redfish dump | C, B with a rank | a BMC names the module at best |
| No rasdaemon and no readable kernel log | EDAC counters | C | counts per DIMM since boot |
| Host that runs the mcelog daemon in place of rasdaemon | `mcelog --client` output, kernel log | C | the daemon keeps counts per DIMM and no records |
| NVIDIA GPU memory | Xid lines, `nvidia-smi -q` or its queries | C | the driver reports no location |

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
| comma separated lines under a header with a GPU property (`gpu_uuid`, `pci.bus_id`, `remapped_rows.failure`), under the saved `nvidia-smi --query-gpu=...` command line, or with a GPU UUID among the values | output of an `nvidia-smi` query in CSV form |
| a `SOCKET n CHANNEL n DIMM n` line, or a line of the page table of the mcelog daemon | `mcelog --client` output |
| JSON with `ErrorDT`, `EcsModeRegisters`, `CurrentBootDT`, `InstalledMemory`, `SystemCrashDT` or `OsCrashDT` among its field names | FMFM log |

A file compressed with gzip is read through, except a database. A MemorY ledger, a zip archive and anything else are refused with a message that says what the content looks like.

A copy of a host's files is read as of the moment it was made, because its counters and its seconds since boot count back from that moment. The moment is the boot time in `proc/stat` plus the uptime in `proc/uptime` when the copy holds both. Otherwise it is the newest modification time among the files that are read, which is right for a copy that kept the times of its files and wrong for one that did not (an archive unpacked without them, a checkout of a repository).

What a file cannot say about itself is an option of the import:

| Option | `my import` | Applies to |
| --- | --- | --- |
| time zone of times that carry none | `--tz` | syslog files, `dmesg -T`, the text forms of a BMC event log, the timestamp of `nvidia-smi` output, times of an FMFM log written without a zone |
| capture time | `--captured-at` | a kernel log that has seconds since boot only |
| modification time of the file as its capture time | `--mtime` | the same log, and inputs that carry no time: dmidecode output, the memory resources of a Redfish dump, an FMFM boot record without a boot time, `mcelog --client` output |
| host | `--host` | a log that holds the records of several hosts (an FMFM log) |
| fields of the query | `--nvidia-query` | output of an `nvidia-smi` query that was saved with `noheader` and without its command line |

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

Adapter `rasdaemon`, package `internal/adapter/rasdaemon`. It reads the SQLite file rasdaemon keeps, `ras-mc_event.db`, with the tables of rasdaemon 0.6 to 1.0, and the store rasdaemon 1.0 keeps on a MySQL, MariaDB or PostgreSQL server (see "Store on a database server"). Columns are read by name, because a database that was upgraded in place has its newer columns in another order than a fresh one.

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

### Times

rasdaemon dates a record by the uptime of its trace event plus the difference between wall clock and uptime that it measured when it started. When the clock is set after that, as NTP does at boot on a host whose hardware clock was off, every later record is wrong by the step until rasdaemon starts again. On the lab host the step was between 6.7 and 10.8 seconds on seven boots. The kernel log dates the same errors by the kernel's clock, so the two would not be recognised as one error.

A machine check record also holds the time the kernel gave the error (`walltime`). Where the two times differ by more than a second, the record is dated by the kernel's time. Rows of other tables within ten minutes of such a record are moved by the same amount: the EDAC report of the same error, the page the kernel gave up after it. The source report says how far rasdaemon's clock was off and how many records were corrected.

A database without machine check records (ARM servers, firmware-first error handling) has no such time. There the ledger compares the rasdaemon records of a boot with the kernel log of the same boot, when the log carries dates (a journal, a syslog file). Two records are compared when they have the same cache line and severity class, or the same slot where neither has an address. A difference in time at which every such record has its kernel log line is taken for the step, and the rasdaemon records of the boot are moved by it, errors and page offlines alike. An error that repeats at one address also pairs with its other occurrences; the first and the last record of the boot rule those pairings out.

Records that lie within two seconds of their kernel log line are left where they are. A later scan of the same boot therefore moves only the records it added, and a boot in which rasdaemon was started again after the step is corrected up to the restart. One or two errors are believed up to a difference of two minutes. A larger difference, up to 15 hours, needs three errors that agree. The `time-align` entry of the source inventory says how far rasdaemon's clock was off and how many records were moved.

### Store on a database server

rasdaemon 1.0 can write to a MySQL or MariaDB database, or to a schema of a PostgreSQL database, in place of the SQLite file. Every host of a fleet then writes to the same tables. They are the tables of the SQLite file with one more column, `hostname`, after the first, and with the time of a row in a time type of the server.

The same adapter reads such a store, one host at a time, with the code that reads the SQLite file: a host gives the same records either way. The connection settings are the variables rasdaemon itself takes, with its defaults, so the environment file rasdaemon is started with also points the reader at the store.

| | MySQL, MariaDB | PostgreSQL |
| --- | --- | --- |
| Backend | `RASDAEMON_DB_BACKEND=mysql` | `RASDAEMON_DB_BACKEND=postgresql` |
| Server | `RAS_MYSQL_HOST`, `RAS_MYSQL_PORT` (3306), or `RAS_MYSQL_SOCKET` | `RAS_PG_HOST`, `RAS_PG_PORT` (5432) |
| Account | `RAS_MYSQL_USER` (`rasdaemon`), `RAS_MYSQL_PASSWORD` | `RAS_PG_USER` (`rasdaemon`), `RAS_PG_PASSWORD` |
| Tables | database `RAS_MYSQL_DATABASE` (`rasdaemon`) | schema `RAS_PG_SCHEMA` (`rasdaemon`) of database `RAS_PG_DATABASE` (`rasdaemon`) |
| Time of a row | `DATETIME`, which rasdaemon fills in UTC | `TIMESTAMPTZ` |
| TLS | `RAS_MYSQL_USE_SSL=true` encrypts and, as in rasdaemon, does not check the certificate of the server | `RAS_PG_SSL_MODE`; without it `prefer`, the default of libpq. `RAS_PG_USE_SSL=true` means `require` |
| Timeout | `RAS_MYSQL_CONNECT_TIMEOUT` (10 s) | `RAS_PG_CONNECT_TIMEOUT` (10 s) |

With an empty host the server on the same machine is reached through its Unix socket.

`--fleet mysql` or `--fleet postgresql` points a command at the store in place of the host it runs on:

| Command | What it reads |
| --- | --- |
| `my --fleet mysql` | every host of the store: the list of hosts with the finding for each, the worst first |
| `my --fleet mysql --host web-01` | the rows of one host: its overview |
| `my dimm <slot> --fleet mysql --host web-01` | the same host; `why`, `map`, `timeline` and `bundle` take the two flags alike |
| `my --fleet mysql --ledger fleet.db` | every host, kept in a ledger that `my open fleet.db` shows without the server |

`--fleet-env FILE` names a file with the settings above; what the file does not set is taken from the environment. With the file alone, the backend is its `RASDAEMON_DB_BACKEND`. `--since 2026-10-01` or `--since 30d` reads rows from then on, which bounds what a large store sends. A command that shows one host asks for `--host` before it reads any row when the store holds several hosts.

The output below is from the store the tests of the commands use: three hosts made from the fixtures of this adapter, on a stand-in for the server.

```
$ my --fleet mysql
3 hosts in this ledger. Name one with --host to see its figures.

 HOST    FINDING               DIMM  FLAGGED  CE  UE  WORST SLOT                      DATA UNTIL
 db-07   replace now              2        2   7   3  NODE 1 CPU1_DIMM_B1             2026-11-13
 web-02  replace now              6        6   7   3  mc#0csrow#2channel#1            2026-11-13
 web-01  consider replacement     2        2   7   1  CPU_SrcID#0_MC#0_Chan#1_DIMM#0  2026-11-13
```

A store is read as it is now, so the findings are as of the time of the command: a host whose last record is a month old has been quiet for a month.

The rows of a host are read in one read-only transaction at the isolation level repeatable read, so the tables are seen as of one moment. Every statement the reader sends is a SELECT; an account that may only SELECT from the tables is enough. A record refers to its row by the store, the table and the row number, without the account: `mysql://db.example:3306/rasdaemon#mc_event/1234`.

What a store does not hold:

- The UTC offset of the host's clock. The SQLite file has local time with the offset; a store has the instant.
- A host without a row. A host appears with its first record.
- Rows without a host name, from a rasdaemon that could not learn the name of its host, cannot be told apart by host. They are counted and not read. `RASDAEMON_HOSTNAME` gives rasdaemon the name.
- Anything but what rasdaemon traced: no SMBIOS tables, no counters, no kernel log. A DIMM is known by the label of its slot and has no serial number.

The reader is tested against MySQL 8.4, MariaDB 11.4 and PostgreSQL 17, on tables created the way rasdaemon 1.0.0 creates them (the columns of its table descriptors, the types of its backends) and filled from the SQLite fixtures. Every fixture read from a server gives the records the file gives.

### Known gaps

- Rows that rasdaemon itself wrote to a MySQL or PostgreSQL server have not been read yet. The layout is taken from the source of rasdaemon 1.0.0.
- A database without machine check records is corrected against a kernel log with dates only. Next to a kernel log saved with `dmesg`, or to none, its records keep rasdaemon's time.
- Where the database and the kernel log hold no error in common, nothing shows how far rasdaemon's clock was off.
- The page type in `memory_failure_event` is rasdaemon's reading of a number the kernel traces, through a table that rasdaemon 0.8.0 took from an older kernel. On kernel 7.3 it writes `dirty unevictable LRU page` for what the kernel log calls `dirty LRU page`. The kernel log has the kernel's own words; the result column of rasdaemon is right. rasdaemon 1.0.0 names the type as the kernel does.
- The vendor records in `non_standard_event` and the processor records in `arm_event` are not read, so the vendor decoding rasdaemon does for some ARM servers is not used.
- rasdaemon stores what it traced while it ran. Errors from before it was started, and from a boot in which it did not run, are in the kernel log only.
- A database that fails the SQLite integrity check is refused.

## Kernel log

Adapter `klog`, package `internal/adapter/klog`. The kernel log is a lossy source: the ring buffer wraps and some messages are rate limited. A scan reads it in addition to the rasdaemon database, and an error both report is counted once.

### Accepted forms

| Form | Times |
| --- | --- |
| `journalctl -k -o json`, also for several boots | exact |
| `journalctl -k` in its text forms: `short`, `short-iso`, `short-iso-precise` | exact; the zone has to be given when the lines have none |
| `journalctl -k -o short-monotonic` | as `dmesg` with seconds since boot |
| syslog files (`/var/log/messages`, `kern.log`, `syslog`), with or without ISO dates | exact; the zone has to be given when the lines have none |
| `dmesg -T` | taken as printed; `dmesg` computes them from the uptime, so they are off after a suspend |
| `/dev/kmsg` records, `dmesg` with seconds since boot | see "Times" below |

A scan of a live host reads the journal of every boot on record, as far as the current user may, for two minutes at most. When that is not enough, the running boot is read in full and older boots in part, and the source inventory says so. It also reads the ring buffer of the running boot from `/dev/kmsg`. The journal holds earlier boots, and the ring buffer holds what the kernel said since this boot after the journal has dropped it to make room: on two of the four cloud hosts of [validation.md](validation.md) the journal held 3 and 39 kernel lines of a boot that the ring buffer still held from its first line. A line that is in both carries the same stamp and the same text in both and is stored once. Where neither can be read, the scan reads the first of `/var/log/kern.log`, `messages`, `syslog` and `dmesg`. Only one of those files is read, and none beside the journal: a syslog line has no kernel stamp, so the same message from a file and from the journal would count twice.

Every kernel log source notes when its first and its last line were written ("its lines run from 2026-11-09 03:12 to 2026-11-15 12:38 UTC"), and a log of one boot that does not start with the boot says how late its first line is. The notes are in the data quality section of a report and in the `notes` of a source in `--json`. "No errors on record" speaks of the time from the first line on: the header of the overview names the time since the oldest boot on record, also where the log that was read begins later. The last line is not the end of what the log of a running host covers; a kernel that has been silent for a month has a last line of a month ago.

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

A page a program asked the kernel to give up is told from a page the kernel gave up after an error. The kernel announces the first kind (`Injecting memory failure for pfn ...` for `madvise`, `Injecting memory failure at pfn ...` for the hwpoison injector); the action then says "asked for through madvise" and the source report counts such pages. A boot in which the machine check injector (`mce-inject`) or the ACPI error injection interface (EINJ) was set up is named in the source report, because its records may come from a test.

### Host name

Syslog lines and journal entries name the host. The first messages of a boot are filed under `localhost`, before the host has its name, so the name is taken from the end of the file. `dmesg` output names no host: an import of it alone is stored under `localhost` unless `--host` names it.

### Grade

As for the rasdaemon database: A with row, column and bank, B with an address or a rank, C otherwise.

### Times

A syslog date has no year. It gets the latest year that does not put the line after the capture time, or after the time of the import when no capture time is given.

A capture with seconds since boot only says when an error happened relative to the boot. The start of the boot is taken from the first of these that is known:

1. On a live host, the boot time in `/proc/stat`. Times are exact.
2. A machine check record in the log, which carries its own wall-clock time. Times are exact.
3. A capture time (`--captured-at`, or `--mtime` for the modification time of the file): the boot started at that time minus the last stamp. Times are marked as estimated.
4. The line in which the kernel sets the system clock from the hardware clock. Times are marked as estimated: they are wrong by as much as the hardware clock was (10 seconds on the lab host).

With none of them the records stay relative to their boot. They are then dated by the records of another source that reports the same errors with a date, when there is one; the `time-align` entry of the source inventory says what was done.

### Known gaps

- Records that were overwritten in the ring buffer or dropped by the rate limit are missing. The EDAC counters show how many errors are known as a count only.
- A log file that starts in the middle of a boot has no boot banner for that boot.
- Machine check decoding covers the memory errors of Intel and AMD processors. Other machine checks, and firmware error sections that are not about memory, are counted in the report and left out.

## EDAC counters

Adapter `edacsysfs`, package `internal/adapter/edacsysfs`. It takes one reading of the counters under `/sys/devices/system/edac/mc`: `dimm_ce_count` and `dimm_ue_count` of every DIMM with its label, position, size and type, and the counts the driver could not tie to a DIMM (`ce_noinfo_count`, `ue_noinfo_count`).

The counters say how many errors a DIMM had since the driver was loaded and nothing about where in the DIMM. They start at zero with every boot.

Each reading becomes a counter event with the growth since the reading before it, or since the counter started. The ledger then sets how many of these errors the located records of the same slot and time window already account for. Totals count the remainder, so a host with a rasdaemon database and EDAC counters is not counted twice, and errors that only the counter saw are still counted.

A host can have a second set of counts per DIMM, those of the [mcelog daemon](#mcelog-daemon). The readings of each source are kept apart, and an error both sets count is counted once: the EDAC counters come first, and what they count beyond the located records covers the counts of the daemon for the same slot and time.

### Grade

C.

### Known gaps

- A host without an EDAC driver for its platform has no counters, which is usual in virtual machines. The entry in the source inventory says so.
- A memory controller that only has the `csrow` directories of kernels before 3.6 is reported and not read.
- The counters cannot tell two errors in one cell from two errors in two cells.

## mcelog daemon

Adapter `mcelog`, package `internal/adapter/mcelog`. It reads what `mcelog --client` prints: the error counts the mcelog daemon keeps per DIMM. A live scan runs the command when the `mcelog` program is installed; a saved answer (`mcelog --client > mcelog-client.txt`) is read by `my import`. A host without the program gets no entry in the source inventory.

The daemon decodes each machine check, counts it for the DIMM it names and keeps no record of it. Its answer has one block per DIMM:

```
SOCKET 0 CHANNEL 1 DIMM 0
DMI_NAME "DIMM_B1" DMI_LOCATION "NODE 0 CHANNEL 1 DIMM 0"
corrected memory errors:
	12 total
	3 in 24h
uncorrected memory errors:
	0 total
	0 in 24h
```

| Line | Read as |
| --- | --- |
| `SOCKET`, `CHANNEL`, `DIMM` | the place by numbers; `any` where the daemon could not tell |
| `DMI_NAME` | the slot: the device locator of the SMBIOS table, which ties the counts to the DIMM in that slot and to its serial number |
| `DMI_LOCATION` | the bank locator of the SMBIOS table, kept with the reading |
| `total` | errors since the daemon started |
| `in 24h` | the rate window of the daemon: the length is the one of its threshold setting (`ce-error-threshold`, `uc-error-threshold`), 24 hours unless changed |

The counts are read like the EDAC counters. Each reading becomes counter events with the growth since the reading before it, and the ledger sets how many of these errors located records and the EDAC counters of the same slot already account for. A host with a kernel log, EDAC counters and the mcelog daemon reports one error three times and has it counted once.

The counts live in the memory of the daemon. They start at zero when the daemon starts, which is at boot or when the service is restarted, and the answer does not say when that was. The first reading is therefore taken to cover the time since the boot it was made in. A later reading with a smaller count is taken as a new start.

The number of the rate window counts the errors since the daemon last aged the window, which is less than the length of the window ago (`leaky-bucket.c`, `bucket_age`). At least that many errors are that recent, so a reading gives them a window of their own: `12 total` and `3 in 24h` are 3 errors in the last 24 hours and 9 before them. A trend can then be told from one reading.

A block for `CHANNEL any DIMM any` is the total of a socket and holds every error of its DIMMs again. The reader keeps what is left after the blocks of that socket: the errors the daemon could not place. They count for the host and for no DIMM, as do the errors of a block that names a channel without a DIMM.

A DIMM without a `DMI_NAME` line is kept under its numbers, as `socket0 channel1 dimm0`. The daemon prints the line when it could read the SMBIOS table at its start and understood the bank locators of the BIOS (`dmi-prepopulate`).

### Grade

C.

### Known gaps

- The page table of the answer (`Per page corrected memory statistics:`, one line per page with its count and `online`, `offline` or `offline-failed`) is counted in a note of the source inventory and not read into the ledger. Its lines have no time. The kernel log records each page offlining with its time.
- The log file of the daemon (`/var/log/mcelog`, decoded machine checks one by one) is not read.
- The reader was written from the source of mcelog (`memdb.c`, `page.c`, `leaky-bucket.c`, `client.c`) and checked on two published samples of the output. It has not read the answer of a running daemon.
- A daemon that was restarted between two readings and has counted more since than before is taken for a daemon that kept running: the growth is then too small by the count at the restart.
- A ledger made from this input alone, with no error in it, lists "no source of error records was read" among the missing data of a DIMM.

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

### Virtual machines

A guest is not shown the corrected errors of the machine it runs on: the hypervisor host handles them. Of the uncorrected ones it is told at most about those that hit its own memory. A scan of a guest that finds nothing therefore says nothing about the DIMMs of the machine, and the tool says so: the overview has a `note` line, a report states it next to the grade, and every finding lists it among what it did not have. The host finding of a guest names the hypervisor host as the place to look for the DIMM.

A host counts as a virtual machine when one of these says so:

| Sign | Where | Gives |
| --- | --- | --- |
| the DMI system vendor and product name are those a hypervisor fills in (QEMU, KVM, VMware, Hyper-V, Xen, VirtualBox, Parallels, OpenStack, oVirt, KubeVirt, Nutanix AHV, Amazon EC2 except `metal` instances, Google Compute Engine, DigitalOcean, Hetzner vServer, Apple Virtualization, Bochs, bhyve) | `/sys/class/dmi/id`, or the `DMI:` line of a kernel log | the name |
| `Hypervisor detected: <name>` | the kernel log of an x86 boot | the name |
| `/sys/hypervisor/type` is `xen` | sysfs | Xen |
| the `hypervisor` flag of the CPU | `/proc/cpuinfo` on x86 | that there is one, without a name |

The name from the kernel log stands before the name from the DMI strings. A cloud whose DMI strings are the same on virtual and bare metal instances is not in the list; on x86 the CPU flag still tells. On other architectures a guest whose DMI strings name no known hypervisor is taken for a physical host.

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

A scan of a copy of a host's files reads the listing sos saves under `sos_commands/ipmitool`: the output of `sel elist` if it is there, else of `sel list -v`, else of `sel list`. One listing is read, because the same record from two of them would count twice. A saved file that holds the error message of ipmitool is reported as not read.

A scan of a live host does not ask the BMC. Reading the event log needs root and can take minutes; save it with `ipmitool sel elist > file` and import the file.

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

Adapter `redfish`, package `internal/adapter/redfish`. It reads what a BMC serves over Redfish from files and does not talk to a BMC. `my collect redfish` writes such files from a BMC.

### Accepted forms

- One JSON file: a resource, a collection with its members expanded, an array of resources, or several JSON documents one after the other.
- A directory tree, as a mockup recorder, a crawl of the service or `my collect redfish` leaves it. Every file that holds JSON is read, whatever its name. A resource that is in the tree twice is read once.

These resources are read, recognised by `@odata.type`: `LogEntry`, `Memory`, `MemoryMetrics`, `Bios` (the settings in effect and the pending ones), `AttributeRegistry`, `ComputerSystem`, `Manager`, `LogService` and `ServiceRoot`. A log entry, a memory resource, a Bios resource or an attribute registry saved without its type is recognised by its properties. Vendor extensions under `Oem` are not interpreted. The whole resource, `Oem` included, is kept as the raw record of what was made from it.

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

A memory resource carries no time. The mark is recorded at the time of the dump, once per DIMM: reading the same state again adds nothing. The time of the dump is `redfish.Options.Now`, else the start time in the manifest of a collection, else the modification time of the files with `ModTimeAsObserved`, else the time of the import.

### The module as the BMC describes it

Each populated `Memory` resource also becomes one row of the ledger table `bmc_memory`, apart from the inventory: what the BMC itself says about the module at the time of the dump.

| Property | Use |
| --- | --- |
| `DeviceLocator`, `Location.PartLocation.ServiceLabel`, `Id` | the names the BMC has for the slot, each kept |
| `Status.State`, `Status.Health` | state and health |
| `ErrorCorrection` | the scheme the BMC names: `NoECC`, `SingleBitECC`, `MultiBitECC`, `AddressParity` |
| `OperatingSpeedMhz`, `AllowedSpeedsMHz` | the speed in use and the speeds the module supports |
| `IsRankSpareEnabled`, `IsSpareDeviceEnabled` | sparing in use |
| `Metrics` | the link to the error counts |

The `MemoryMetrics` resource a module links gives the BMC's own error counts:

| Property | Use |
| --- | --- |
| `CurrentPeriod.CorrectableECCErrorCount`, `CurrentPeriod.UncorrectableECCErrorCount` | counts since the last reset of the system or of the counters |
| `LifeTime.CorrectableECCErrorCount`, `LifeTime.UncorrectableECCErrorCount` | counts for the life of the module |
| `HealthData.AlarmTrips.CorrectableECCError`, `.UncorrectableECCError` | the module crossed a threshold of the BMC |

The schema defines the current period as the time since the last system reset or `ClearCurrentPeriod` action, and gives no time for either. `LastResetTime` of the system, when the dump has it, is kept as the earliest start of the current period.

These counts are readings. They are not turned into events, and no total of the ledger includes them: the lifetime count has no period, and the host's own records may hold the same errors.

For each module the ledger says what became of its counts: read; `not offered` when the module links no metrics, which is a BMC that keeps no counts per module; and for a collection, why they did not come back (`unsupported`, `denied`, `timeout`, `error`).

Redfish has no property for the width of the DRAM devices (x4, x8). `DataWidthBits` and `BusWidthBits` are the widths of the module. The width is known when the EDAC driver of the host reports it.

### BIOS settings

The `Bios` resource of a system lists every attribute of the system firmware. The settings that bear on memory are kept, in the ledger table `bios_setting`; the rest is not, because a ledger travels in a case bundle and a bundle holds no network configuration and no accounts.

An attribute is kept when its name, its display name or its menu path holds a word of memory (`mem`, `dimm`, `ddr`, `ecc`, `scrub`, `ppr`, `adddc`, `sddc`, `spar`, `mirror`, `refresh`, `rank`, `correctable`, `uncorr`, `leaky`, `bucket`, `rowhammer`). It is never kept when its type is `Password` or its name holds `password`, `passphrase`, `secret`, `token`, `credential`, `key` or `pin`. At most 400 attributes are kept, and a value longer than 200 characters is left out.

| Source | Use |
| --- | --- |
| `Bios.Attributes` | the value in effect |
| the resource `@Redfish.Settings.SettingsObject` points to, else a Bios resource named `Settings`, `Pending` or `SD` below it | the pending value, kept when it differs: it takes effect after a reboot |
| the `AttributeRegistry` named by `Bios.AttributeRegistry` | display name, menu path, type, `ReadOnly`, `ResetRequired`, the allowed values, bounds and default |

Without the registry the kind of a value is what the JSON says, and whether a setting can be changed is not known. Without a pending resource a change that waits for a reboot does not show. The report of the source says which of the two the dump lacks.

### The BMC

One row of the ledger table `bmc` per dump: the firmware version and model of the first `Manager` that names its firmware, `BiosVersion` and `LastResetTime` of the system, `RedfishVersion` of the service root, the attribute registry the BIOS settings refer to, and for every log the number of entries read, how many of them are about memory, the times of the oldest and the newest entry of any kind, how many entries the log says it holds, and `OverWritePolicy` and `MaxNumberOfRecords` of its `LogService`.

A collection made by `my collect redfish` has a `manifest.json`. From it come the address of the BMC, how its certificate was checked, the authentication, the clock of the BMC against the collector's, whether the run was complete, and what became of each class of resources. A dump without a manifest says for each class only whether resources of it are in the dump.

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

- The error counts of a BMC are readings beside the ledger's events. They are not compared with the host's records yet.
- `MemoryMetrics` properties other than the four counts and the two alarm flags are not read. A `MemoryMetrics` resource that no module of the dump links is counted in the report and not read.
- Of several systems behind one BMC, the BIOS settings of the first are read.
- A BIOS setting about memory whose name, display name and menu say nothing of memory is not kept.
- The diagnostic data of an entry (`DiagnosticData`, CPER records) and the event record in an entry of type `CXL` are not decoded.
- Message registries of other vendors are not known. Their entries about memory are kept as informational events and listed in the report by message ID, so a missing class is visible.
- `MEM0008` ("is spare memory") is read as a spare taking over, like the IPMI state it follows.
- The mark on a module has the time of the dump, which is later than the time the BMC set it.
- `MemoryLocation.MemoryController` is not read. A BMC that counts channels per memory controller gives two slots the same socket, channel and slot numbers. Such numbers are left off both slots and the report counts them; a log entry that names its module by these numbers then has a slot named by the numbers.
- A `disable` in a log is withdrawn by a later deasserted entry for the same module only within one dump.

## NVIDIA GPU memory

Two inputs describe the memory of a GPU: the Xid lines the driver writes to the kernel log, and the state `nvidia-smi` prints, in full with `-q` or as the columns of a query. Both go to the `gpu_mem` table of the ledger, keyed by the GPU serial number, and stay apart from the DIMM events.

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

### nvidia-smi queries

The same adapter reads the CSV output of `nvidia-smi --query-gpu=<properties> --format=csv` and of `--query-remapped-rows`. One line is one GPU and gives one snapshot. The columns are read by name, in any order and any subset. The names are those `nvidia-smi --help-query-gpu` lists; where a property has two names, both are read.

| Value | Properties |
| --- | --- |
| identity | `uuid` or `gpu_uuid`, `serial` or `gpu_serial`, `pci.bus_id` or `gpu_bus_id`, `name` or `gpu_name`, `index` |
| time | `timestamp` (`YYYY/MM/DD HH:MM:SS.msec`, local time of the host) |
| ECC in use | `ecc.mode.current` |
| ECC counts | `ecc.errors.corrected` and `ecc.errors.uncorrected`, each with `.volatile` and `.aggregate`, then `.dram` and `.sram` |
| uncorrectable SRAM errors by check | `.sram.parity` and `.sram.secded`, added where the driver has no `.sram` column |
| ECC counts of older GPUs | `.device_memory` is counted as DRAM where `.dram` is absent, and `.total` less the DRAM count as SRAM |
| SRAM limit passed | `ecc.errors.uncorrected.aggregate.sram.thresholdExceeded` |
| retired pages (older GPUs) | `retired_pages.single_bit_ecc.count` or `retired_pages.sbe`, `retired_pages.double_bit.count` or `retired_pages.dbe`, `retired_pages.pending` |
| remapped rows | `remapped_rows.correctable` or `remapped_rows.sbe`, `remapped_rows.uncorrectable` or `remapped_rows.dbe`, `remapped_rows.pending`, `remapped_rows.failure` |
| spare rows per bank | `remapped_rows.histogram.max`, `.high`, `.partial`, `.low`, `.none` |

A value in square brackets (`[N/A]`, `[Not Supported]`) or a bare `N/A` means the GPU does not have it; the snapshot leaves it unset. The unit a header carries (`memory.total [MiB]`) and the units of values are ignored, so output with and without `nounits` reads alike. Columns about memory errors that have no place in a snapshot (the counts of single caches, `remapped_rows.correctable_inactive`) are named in a note of the source inventory.

A snapshot is keyed by the UUID of its GPU, or by its serial number. Output that has neither column is not read, and the source inventory says so: the index of a GPU changes with a boot and its PCI address belongs to the slot, so such a row cannot be told from the row of another GPU in the same place later. In output that has the columns, a GPU that reports neither value is skipped and named in a note.

Output saved with `noheader` does not name its columns. They are taken from the command line when it was saved with the output (a line with `nvidia-smi --query-gpu=...`), or from `my import --nvidia-query index,uuid,remapped_rows.failure`. Without either the input is reported as unreadable for that reason.

A query that ran in a loop (`-l`) gives one snapshot per line when it has a `timestamp` column. Without one, the lines after the first for a GPU are skipped and counted. The other selective queries (`--query-retired-pages`, `--query-compute-apps`, `--query-accounted-apps`) list pages and processes one by one; they are recognised, named in the source inventory and not read.

A reading saved once with `-q` and once as a query is one record: both forms give a snapshot the identity of GPU and second.

### The repair budget

`my gpu` reads these records as a budget. Every bank of the memory of a GPU has spare rows, a row that fails is remapped to one of them, and a bank without a spare row cannot take another failure. The command lists per GPU the Xid records of the last 30 days, the rows remapped after correctable and after uncorrectable errors, whether a remapping is pending or failed, and the banks by the spare rows they have left (`max`, `high`, `partial`, `low`, `none`, the buckets of the driver). `my gpu <index|serial|uuid|pci-address>` adds the ECC counts and the latest Xid records of one GPU.

Each GPU has a state, the worst first:

| State | When |
| --- | --- |
| repair failed | Xid 64 on record, or the failure flag of a reading |
| repair pending | a remapping or a page retirement waits for a GPU reset |
| little room | a bank at `low` or `none` |
| repaired | rows remapped or pages retired, and room left |
| errors | ECC counts or Xid records about memory, and no repair on record |
| no errors | records, and none of the above |

A state is a reading of the budget and no finding. One rule of the engine speaks about GPU memory: R-REP-01, replace now, when a row remapping failed. The exit code of `my gpu` is therefore 3 or 0. The failure flag stands for the life of the GPU: a later reading that does not carry it (a query without that column) takes nothing back, and where the latest reading lacks other values, those of the reading before it are shown with a note.

### One GPU under several names

A snapshot knows serial number, UUID and PCI address. An Xid line from a boot in which the driver did not print the identity knows the PCI address only. When GPU records are read from the ledger, a record without a UUID takes it from a record with the same PCI address, of the same boot if there is one and nearest in time otherwise; serial number, index and model follow the UUID. The stored rows stay as their sources gave them, so a snapshot imported later completes Xid records imported before it.

### Known gaps

- DCGM is not read.
- The text of an Xid line is kept and not taken apart: the partition a double bit error names, for example, is not a field.
- The default table of `nvidia-smi` (without `-q` and without a query) and the files of `nvidia-bug-report.sh` are not read.
- The list of `--query-retired-pages` (address, time and cause of each retired page) is not read; a snapshot has the counts.
- The property names of the queries are those of the help text of drivers 470 to 610, and the reader was checked on published output of `--query-gpu` from three GPUs. The help text of `--query-remapped-rows` was not available; its columns are read by the same names, which a published sample of its output uses.
- A GPU that was replaced in the same slot between two readings is told apart by its UUID; an Xid record with a PCI address only, between the two readings, goes to the nearer one.
- GPUs of other vendors are not read.
- No rule grades a pending remapping, a bank without spare rows or the `SRAM Threshold Exceeded` flag of the driver. `my gpu` shows them as states and notes; the exit code does not tell them from a GPU without errors.

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

### One error, one record

A device reports an error as a DRAM event record or as a general media event record, never as both. Records of the CXL tables are therefore never taken for two reports of one error, however close in time they are: without a host address the slot is all they have in common. A record of another source for the same device may still be a second report.

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
