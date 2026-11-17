# Inputs

MemorY reads records a host and its BMC already keep. This page has one section per input: what is read, which observation grade the records get, and what is known to be missing.

The observation grade of a record says how much of the place of an error its source gave.

| Grade | Place known | What it allows |
| --- | --- | --- |
| A | bank, row and column | fault mode classification |
| B | physical address or rank | repetition per page and trends; a row fault is an estimate |
| C | the module, or a count only | trends and thresholds |

Every input a scan looks for gets an entry in the source inventory, also when it is absent or unreadable. The mcelog daemon and `nvidia-smi` get one only on a host where their program is installed. The entry carries the status, the number of records read and skipped, and notes such as "3 records of other sensors were left out".

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
| Host that cannot be reached | BMC event log, Redfish dump or the Redfish service | C, B with a rank | a BMC names the module at best |
| No rasdaemon and no readable kernel log | EDAC counters | C | counts per DIMM since boot |
| Host that runs the mcelog daemon in place of rasdaemon | `mcelog --client` output, kernel log | C | the daemon keeps counts per DIMM and no records |
| NVIDIA GPU memory | Xid lines, `nvidia-smi -q` or its queries | C | the driver reports no location |

These grades are what the adapters and decoders give on the fixtures and test cases of this repository. They have not been confirmed on real hosts of each kind.

## How a file is recognised

`scan.Detect` picks the adapter for a file or a directory by its content. File names play a part only for crash logs, whose names say what they are: a pstore file is named `<type>-<backend>-<id>` by the kernel, and kdump names its text log `vmcore-dmesg.txt` or `dmesg.<stamp>`.

| Content | Read as |
| --- | --- |
| directory with `proc`, `sys`, `var/log`, `var/lib/rasdaemon`, `usr/local/var/lib/rasdaemon` or `sos_commands` below it | copy of a host's files, read like a live host |
| directory with pstore files, a `dmesg.txt` of systemd-pstore, or a kdump text log below it | crash logs |
| directory with Redfish resources in JSON files | Redfish dump |
| any other directory: an unpacked archive of a vendor tool, a folder of logs | read file by file: each file below it is recognised as a file given on its own would be (at most 2,000 files, 8 levels deep; files above 64 MiB are left out). The files no reader recognises are counted in one line, a source of the kind `unknown`; a directory in which no file is recognised is not read |
| SQLite file | rasdaemon database |
| lines with a kernel stamp, `/dev/kmsg` records, `dmesg -T` dates or syslog lines | kernel log |
| JSON objects with `MESSAGE` and journal fields | kernel log from `journalctl -o json` |
| a pstore file name, a first line such as `Panic#1 Part1`, `vmcore-dmesg.txt` or `dmesg.<12 digits>` | crash log: a pstore dump or a kdump text log |
| binary data that is a chain of Generic Error Status Blocks whose entries fill their data length | boot error region (BERT), a saved copy |
| `# dmidecode` or `Handle 0x..., DMI type` lines | dmidecode output |
| lines or blocks of `ipmitool sel`, the table of `ipmi-sel`, 16-byte records, or a JSON list that names sensors | BMC event log |
| JSON with `@odata` properties | Redfish resource |
| `NVSMI LOG` text or an `nvidia_smi_log` XML document | `nvidia-smi -q` output |
| comma separated lines under a header with a GPU property (`gpu_uuid`, `pci.bus_id`, `remapped_rows.failure`), under the saved `nvidia-smi --query-gpu=...` command line, or with a GPU UUID among the values | output of an `nvidia-smi` query in CSV form |
| a `SOCKET n CHANNEL n DIMM n` line, or a line of the page table of the mcelog daemon | `mcelog --client` output |
| JSON with `ErrorDT`, `EcsModeRegisters`, `CurrentBootDT`, `InstalledMemory`, `SystemCrashDT` or `OsCrashDT` among its field names | FMFM log |
| blocks of `Record:`, `Date/Time:` and `Description:` lines | `racadm getsel` output ([vendors.md](vendors.md#saved-output-of-vendor-tools)) |
| blocks of `SeqNumber =`, `Message ID =` and `Timestamp =` lines | `racadm lclog view` or `racadm getraclog` output |

A file compressed with gzip is read through, except a database. A MemorY ledger, a zip archive and anything else are not read; the message says what the content looks like. `my import` lists such an input as a source of the kind `unknown` with that message and reads the other inputs; it is no source a finding missed, since nobody knows whether it held memory records. With `--strict`, or when no input is recognised, the import stops with exit code 70.

A fault inside a reader stays there. A record that a reader cannot parse, or whose reading fails, is skipped and counted as not understood; its text is kept with the case when it names memory, a DIMM or ECC (at most 50 per source, 4 KiB each). A reader that fails as a whole loses its own source: nothing of it is added, its row in the source inventory says where it failed, and the other sources and the exit code are what they would be without it. A recogniser that fails does not recognise the input. `MY_DEBUG=panic` lets such a failure through with its whole stack, for a developer.

In a copy of a host's files, a kernel log file that holds no line of a kernel log is listed and the next one in the order of preference is read, and SMBIOS tables that hold no structure are listed and the text of dmidecode is read in their place.

A copy of a host's files is read as of the moment it was made, because its counters and its seconds since boot count back from that moment. The moment is the boot time in `proc/stat` plus the uptime in `proc/uptime` when the copy holds both. Otherwise it is the newest modification time among the files that are read, which is right for a copy that kept the times of its files and wrong for one that did not (an archive unpacked without them, a checkout of a repository).

What a file cannot say about itself is an option of the import:

| Option | `my import` | Applies to |
| --- | --- | --- |
| time zone of times that carry none | `--tz` | syslog files, `dmesg -T`, the text forms of a BMC event log, `racadm` output, the timestamp of `nvidia-smi` output, times of an FMFM log written without a zone |
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
| `Memory failure: 0x...: recovery action for ...`, soft offline messages (`Soft offline: 0x...: ...` up to 5.x; `Memory failure: 0x...: invalidated`, `... page migration failed ...`, `... isolation failed ...` and `... thp split failed` from 6.x) | action `page-offline`, method `hard` or `soft`, with the result |
| `mce: Uncorrected hardware memory error in user-access at <address>`, `mce: Kernel accessed poison in user space at <address>`, `Memory failure: 0x...: Sending SIGBUS to <program>:<pid> ...` | added to the detail of the `Memory failure` record of the same page: a read in user space or a copy by the kernel consumed the error, and which programs got the signal. Without such a record within 200 lines, a note |
| `NVRM: Xid ...` | see "NVIDIA GPU memory" |
| `Linux version ...`, `DMI: ...`, the command line | a boot, with kernel, board and BIOS |
| `mce: [Hardware Error]: Machine check: <why>` and `Kernel panic - not syncing: Fatal machine check`, with the other reasons `mce_panic()` gives in `arch/x86/kernel/cpu/mce/core.c` | a record of type `hardware stop` (source `klog/stop`): the boot ended there, and the machine check record printed just before it in the same boot stopped the host |
| `GHES: Fatal hardware error but panic disabled`, `Kernel panic - not syncing: GHES: Fatal hardware error` (Linux 7.3), `Kernel panic - not syncing: Fatal hardware error!` (up to 6.x) | the same, tied to the firmware error record printed just before |
| `Kernel panic - not syncing: ...` for another reason | a record of type `panic` (source `klog/panic`) without the text of the panic |

A line read from `dmesg` and the same line read from the journal are one record: a record is identified by the kernel's own stamp and its text.

A stop or a panic is one record per boot, whatever number of its lines the log kept: the severity line, the panic line, the `---[ end Kernel panic` line. It counts in no total. [How a boot ended](data-model.md#how-a-boot-ended) says how the ledger uses it; the finding of the error that stopped the host says so, and the timeline marks the next boot as a reboot after a stop. A machine check that `mce_panic()` reports with `fake_panic` set (a debugfs setting for tests) prints `Fake kernel panic: ...` and the host goes on: no stop is recorded, and the source report counts such lines. A stop is tied to the record printed before it only when that record is an uncorrected memory error of the same boot, at most 100 lines earlier, and not carried over from the boot before. A fatal machine check that is no memory error is left out, and the stop then names no record.

A page a program asked the kernel to give up is told from a page the kernel gave up after an error. The kernel announces the first kind (`Injecting memory failure for pfn ...` for `madvise`, `Injecting memory failure at pfn ...` for the hwpoison injector); the action then says "asked for through madvise" and the source report counts such pages. A boot in which the machine check injector (`mce-inject`) or the ACPI error injection interface (EINJ) was set up is named in the source report, because its records may come from a test.

### What the next boot prints of the boot before

Two kinds of record in a kernel log belong to the boot before the one that printed them. They are stored as carried records and moved into that boot ([data-model.md](data-model.md#records-kept-over-a-reset)).

| Lines | When a record is carried |
| --- | --- |
| a machine check record of `__print_mce()` | the head line has no ` Exception` (the record was polled) and the status is uncorrected with the processor context corrupt, or uncorrected and signaled. The running boot does not log such a record from a poll: `should_log_poll_error()` logs an uncorrected record only in the poll of every bank at CPU bring-up, which logs "the machine checks left over from the previous reset" (`__mcheck_cpu_init_prepare_banks()`), or when it is UCNA. Second rule, for a platform that keeps `MCG_STATUS.MCIP` over the reset: the head has ` Exception`, the status has PCC, `TSC 0`, and the boot goes on for at least 10 seconds after the record; a record with PCC raised in the boot ends it within the second |
| the block that follows `BERT: Error records from previous boot:` | always: the `[Hardware Error]:` lines without the `{N}` tag of a GHES source, in the form of `cper_estatus_print()` and `cper_print_mem()`, up to `BERT: Total records found` |

The `TIME` field of a carried machine check record is the time the boot read the bank, not the time of the error. Its fingerprint takes that field in: two boots that print the same record left in a bank at the same stamp are two records. Records that are not carried keep the fingerprint they always had. The lines of the boot error region are a second report of the region when the region was read too: the region is counted.

The lab guest shows the first rule on real kernel output (`testdata/klog/carried-lab.dmesg`: a fatal machine check, a panic, and the next boot printing the record from bank 7 as `CPU 0: Machine Check: 0 Bank 7: be00000000010090`, `TSC 0`). The second rule and the lines of the boot error region rest on the kernel source; QEMU clears `MCG_STATUS` at reset and builds no BERT table. On AMD processors with SMCA a poll logs any valid record (`smca_should_log_poll_error()`), so a polled uncorrected record with PCC could in principle come from the running boot; the decode block `mce_amd` prints does not say whether a record was polled and is never taken as carried.

### Lines of other programs

A syslog file and a journal export hold the lines of other programs beside the kernel's. These are read:

| Program | Lines | Ledger |
| --- | --- | --- |
| mcelog | `Offlining page <address>`, `Offlining page <address> failed: <error>`, `Soft offlining of page <address> failed, trying hard offlining` (`page.c`) | action `page-offline`, source `klog/mcelog`, without a method: the line does not say soft or hard |
| mcelog | `Corrected memory errors on page <address> exceed threshold <n> in <time>` (`page.c`, `memdb.c`) | the reason in the detail of the page offline of that page |
| sel-logger of OpenBMC | `Failed adding this event: <error>` (`src/sel_logger.cpp`) | a note: the event log of the BMC lacks these records |
| QEMU, through libvirt or its monitor | `Can't migrate this vm with hardware poisoned memory, please reboot the vm and try again` (`migration/migration.c`) | a note: an uncorrected error reached the memory of a guest |

The kernel prints nothing when a soft offline works, so the line of mcelog is then the only record of it. When the kernel's own record of the same page follows within 200 lines (a failed soft offline, a hard offline), that record is kept, with "asked for by mcelog" and the threshold in its detail. A threshold line with no page offline record of its page is counted in a note: with `memory-ce-action = account` mcelog counts and takes nothing offline. None of these lines counts as an error.

A live scan reads the journal entries of the kernel and of mcelog (`_TRANSPORT=kernel + SYSLOG_IDENTIFIER=mcelog`). The lines of sel-logger and QEMU are read from files given to `my import`: the journal of a BMC, the journal or syslog file of a hypervisor host.

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
- `Failed to add SEL entry` and `Failed to add SEL record` are not read: no public source of OpenBMC prints them. phosphor-sel-logger, in its history up to November 2026, prints `Failed adding this event`.
- The log file of mcelog (`/var/log/mcelog`) has lines without dates and is not read; its lines in syslog or the journal are.
- Panics of arm64 for an error that firmware did not handle first (`Asynchronous SError Interrupt`, a synchronous external abort) are read as panics for another reason: no stop is recorded.

## Crash logs (pstore, kdump)

Adapter `crashlog`, package `internal/adapter/crashlog`, source kinds `pstore` and `kdump`. A fatal memory error panics the kernel, and the journal and rasdaemon write nothing after that ([crash-loop.md](crash-loop.md)). Some kernel text of the crashed boot outlives it. The adapter finds it, puts it in order and hands it to the kernel log reader, so it is read as the kernel log of the boot that crashed.

| Place | What is there | How it is read |
| --- | --- | --- |
| `/sys/fs/pstore` | `dmesg-<backend>-<id>` files: the end of the kernel log at the panic, in parts of the size the backend takes; `console-ramoops-<n>`: the console of the boot before, with ramoops | the parts of one dump are grouped and read from the highest part number down; the header line of each part is taken out |
| `/var/lib/systemd/pstore` | the same files after `systemd-pstore` moved them. Its source (`src/pstore/pstore.c`) files a dump below `<time>/<count>/` for EFI variables and `<first record id>/` for ERST, with a `dmesg.txt` per dump; systemd 259 of Fedora 44 put the three ERST parts of the lab's dump at the top, with their times kept and no `dmesg.txt` | the original parts wherever they lie; the `dmesg.txt` only where the parts are gone |
| `/var/crash/<dir>/vmcore-dmesg.txt` | the kernel log kdump-utils takes out of the memory dump (`vmcore-dmesg-incomplete.txt` when that failed half way) | as one kernel log |
| `/var/crash/<stamp>/dmesg.<stamp>` | the same, by Debian's kdump-tools | as one kernel log |

A scan of a host reads each of the three places that exists, and a copy of a host's files is read the same way. `my import` takes a single pstore file, a `dmesg.txt`, a kdump text log, or a directory of any of them.

Grouping of the parts of one dump, from the ids the kernel gives:

| Backend | Rule |
| --- | --- |
| EFI variables (`efi_pstore`) | the id is (time × 100 + part) × 1000 + count (`efi-pstore.c`, `generic_id`): parts with the same time and count are one dump |
| ERST (`erst`) | the records of a boot get consecutive ids (`cper_next_record_id()` counts up from a seed of the time of its first use), and Part 1 is written first: consecutive ids are one dump, cut where the header starts another dump of the same boot |
| ramoops and others | one file is one dump |

Not read, each counted in a note of the source: `pmsg-*` (text user space wrote), `ftrace-*`, `mce-*` and the other record types of pstore, files ending in `.enc.z` (the kernel could not decompress the record), the memory dump (`vmcore`, `dump.<stamp>`), and `kexec-dmesg.log`, which is the log of the capture kernel and would enter the ledger as a boot of a few seconds. A file that is not readable as this user is skipped and named; `/sys/fs/pstore` is readable as root only.

### Grade

Whatever the lines give: a printed machine check record with an address is grade B, a firmware error record with row and column grade A. A panic text without a memory error record gives no error event.

### Times

The lines carry seconds since the start of the boot that crashed. The start is taken as the kernel log reader takes it (see "Times" under [Kernel log](#kernel-log)): a machine check record's own `TIME` field (exact), else a capture time, else the RTC line of the boot. The time of the dump is given as the capture time only when the text has neither a machine check record with a time nor an RTC line, because the reader ranks a capture time above the RTC line. The time of a dump is:

- in `/sys/fs/pstore`, the modification time of the file, which the kernel sets to the time of the record (`pstore_mkfile()`), and in the archive the same time, which systemd-pstore 259 kept when it moved the files;
- for EFI variables also in the archive, the time in the id;
- for a kdump log, the modification time of the file, which is later than the crash by the time the capture kernel took to start.

The ERST id is not used as a time: its seed is the time of the first record of the boot, which need not be the time of the dump. A dump with no time and no anchor in its text stays relative to its boot. A dump without a record and without the boot banner is still the end of a boot: the reader places that boot from the same anchors and stores it, so the panic has a boot to belong to.

### Duplicates

A line is identified by the kernel's stamp and its text, as in every kernel log. The same dump read from `/sys/fs/pstore` and from the archive, and a line that the journal also holds, give one record. The boot of a dump has no kernel boot ID; when its start lies within 30 seconds of a boot the journal holds, it is that boot.

### Known gaps

- No pstore file or kdump log of a real server was read. The tests read the three ERST files of a real panic of the lab guest (`internal/adapter/crashlog/testdata/erst-fedora44`) and pstore files built from the kernel lines of the lab capture `7.3.0-mce-fatal` with the framing of the kernel source. EFI variables and ramoops were not seen on a running kernel.
- Line numbers in the record references of a dump of several parts count in the text as it is read, Part N first; the reference names Part 1 and the number of further parts (`dmesg-efi_pstore-<id>+2#L12`).
- A copy of `/sys/fs/pstore` whose files lost their times, and an ERST archive, date a dump only by what its text says.

## Boot error region (BERT)

Adapter `bert`, package `internal/adapter/bert`, framing in `internal/decode/cper`. Firmware that handles errors before the operating system does keeps a record of an error it could not report, typically the one that reset the machine, in the Boot Error Region for the next boot. The ACPI table BERT points at it. Linux shows the region as `/sys/firmware/acpi/tables/data/BERT` (`acpi_bert_data_init()`), readable by root only, and prints up to five of its records at boot (`bert_print_all()`).

A scan reads the region on a host whose ACPI tables include BERT, and in a copy of a host's files that holds the file. `my import` takes a saved copy of the region.

| Part | Layout | Handling |
| --- | --- | --- |
| Generic Error Status Block | block status, raw data offset and length, data length, severity (20 bytes) | the blocks follow each other; the walk goes on while the data length is not zero and the entries fill it exactly, as `cper_estatus_check()` requires, and stops at the first block that does not. It does not stop at a block status of zero: the kernel clears that field in the region after printing a block |
| Generic Error Data Entry | section type, severity, revision, validation bits, length, FRU id and text (64 bytes); from revision 0x300 also a time stamp (72 bytes) | every length is checked against what is left before it is used |
| Platform memory error section | `struct cper_sec_mem_err` | decoded like a firmware error record of the kernel log: address, node, card, module, rank, device, bank group, bank, row, column, error type, SMBIOS handle |

One event per memory section, source `bert`, decoder `cper`, with the severity of the entry. A section without a time stamp of its own is carried: the error happened in the boot before the one that read the region, and the ledger puts it there ([data-model.md](data-model.md#records-kept-over-a-reset)). An entry of revision 0x300 with a valid time stamp is dated by it and is not carried; the specification does not state the zone of the time stamp, so the time is marked as estimated. The FRU text, when the firmware filled it, is kept as the label of the record. A fatal block without a memory section is kept as an `Informational` record that marks the boot before as stopped. Sections of other types are counted in a note. The block status is left out of what identifies a record, so the region read before and after the kernel printed it gives the same records.

### Grade

From the fields the firmware marked valid: A with row, column and bank, B with an address or a rank, C otherwise.

### Duplicates

The lines the kernel printed of the region are a second report of it: the region is counted. A record of the region and the machine check or firmware record of the same error in the boot before are one error: the best grade is counted ([data-model.md](data-model.md#records-kept-over-a-reset)).

### Known gaps

- No region of a real firmware was read. QEMU builds no BERT table; the tests use regions made from the layouts of the ACPI specification as Linux declares them (`include/acpi/actbl1.h`). Which fields real firmware fills, whether it writes a record after a machine check at all, and the zone of its time stamps are not known.
- Raw data after the entries of a block is skipped.

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

SMBIOS gives no error records and no grade. The board of `my board` and of the report is drawn from the locators and bank locators of the slots, see "Slot names of server vendors"; that drawing ties no record to a slot.

### Known gaps

- Without the raw tables or `dmidecode` output a scan has no serial numbers. DIMMs are then told apart by slot, and a replaced DIMM cannot be told from its predecessor.
- Serial numbers that are placeholders (`NO DIMM`, `Unknown`, all zeros and the like) are not taken as identities.
- SMBIOS does not say which EDAC position a slot is. The two are tied on evidence only, see "Which slot a record belongs to".

## Slot names of server vendors

The board places a slot by the two names SMBIOS gives it, the locator and the bank locator, and where a name gives only a slot number, by the table of the board's family. Every slot gets a processor, a channel and a position in the channel, counted from 0, or it is drawn without channels on its processor, or set aside by its name with the reason. A memory device of type Flash, ROM or EEPROM is a chip of the board and no slot.

A board placed in full whose family has a rule set is also held to the population its maker's documents give: which slots carry the modules for their number, which slot of a channel comes first, what may stand together. [rules.md](rules.md#population-rules) lists the rules, the rule sets and their documents; a board not placed in full gets no population check.

### Name forms

Each name is read whole, by the first form of the tables below that matches all of it; letters of any case match except where a form says capitals. A name that no form reads sets its slot aside. `NO DIMM`, `Not Specified`, `N/A`, `None`, `Unknown`, `<BAD INDEX>`, `<OUT OF SPEC>` and an empty string name nothing.

<!-- forms -->
| Locator form | Examples | Parts |
| --- | --- | --- |
| `p-dimm-letter` | `P1-DIMMA1`, `P1_DIMM_A1` | processor word `P`, channel letter, slot |
| `dimm-p-letter` | `DIMM_P0_A1` | processor word `P`, channel letter, slot |
| `p-umc-ch` | `P0_UMC0_CH_A0`, `P0_MC0_DIM_CH_A0` | processor word `P`, memory controller, channel letter, slot |
| `p-ch-dimm` | `P0-ChA-DIMM0` | processor word `P`, channel letter, slot |
| `cpu-dimm-letter` | `CPU0_DIMM_A1`, `CPU1 DIMM A1` | processor word `CPU`, channel letter, slot |
| `cpu-dimm-letter-only` | `CPU0_DIMM_A` | processor word `CPU`, channel letter |
| `cpu-u-dimm-u-letter` | `CPU_0_DIMM_A_0` | processor word `CPU`, channel letter, slot |
| `cpu-letter` | `CPU0_A1` | processor word `CPU`, channel letter, slot |
| `cpu-c-d` | `CPU0_C0D1` | processor word `CPU`, channel number, slot |
| `cpu-ch-dimm` | `CPU0_CH0_DIMM0` | processor word `CPU`, channel number, slot |
| `socket-channel-dimm` | `SOCKET 0 CHANNEL 1 DIMM 0` | processor word `SOCKET`, channel number, slot |
| `node-channel-dimm` | `NODE 0 CHANNEL 0 DIMM 0 (C0A0)` | processor word `NODE`, channel number, slot |
| `dimm-digits` | `DIMM010`, `DIMM000 J11` | processor, channel and slot as three digits (capitals only) |
| `p-digit-letter` | `P1_DIMM1A`, `P1-1A` | processor word `P`, a digit and a letter: what they are the board decides |
| `node-digit-letter` | `NODE0-1A` | processor word `NODE`, a digit and a letter |
| `proc-dimm` | `PROC 1 DIMM 9`, `PROC  1 DIMM  1` | processor word `PROC` (from 1), a slot number that says no channel |
| `proc-dimm-letter` | `PROC 1 DIMM 3A` | processor word `PROC`, slot number, population letter |
| `cpu-dimm-number` | `CPU1-DIMM5`, `CPU1_DIMM_5` | processor word `CPU`, a slot number that says no channel |
| `dimm-cpu` | `DIMM5_CPU0` | a slot number, processor word `CPU` |
| `socket-dimm` | `Socket 0 DIMM 1` | processor word `Socket`, a slot number |
| `node-dimm` | `Node0_Dimm1` | processor word `Node`, a slot number |
| `c-dimm` | `C0_DIMM1`, `H0_DIMM1` | processor word `C` or `H` (capitals only), a slot number |
| `cpu-only` | `CPU1` | processor word `CPU`; the bank locator gives the slot number |
| `board-dimm` | `Board 1, DIMM 1A` | memory card, slot number, population letter |
| `path` | `1/0/0/1A` | a path of the partition, slot number, population letter |
| `controller-channel-dimm` | `Controller0-ChannelA-DIMM0` | memory controller, channel letter, slot |
| `controller-channel` | `Controller1-ChannelB` | memory controller, channel letter |
| `channel-dimm` | `ChannelA-DIMM0`, `ChannelA_Dimm1` | channel letter, slot |
| `onboard-dimm` | `ONBOARD DIMM_A1` | channel letter, slot |
| `dimm-ch-letter` | `DIMM CHA1`, `DIMM-CHB2` | channel letter, slot |
| `ch-letter` | `CHA_1` | channel letter, slot |
| `ddr-letter` | `DDR4_A1` | channel letter, slot |
| `memory-letter` | `MEMORY A1` | channel letter, slot |
| `cpu-u-letter` | `CPU_A1` | channel letter, slot |
| `dimm-letter` | `DIMMA1` | channel letter, slot |
| `dimm-u-letter` | `DIMM_A1`, `DIMM A1` | a letter and a number: what they are the board decides |
| `letter-number` | `A1`, `B12` | a letter and a number: what they are the board decides |
| `dimm-number-u-letter` | `DIMM1_A` | a digit and a letter |
| `dimm-digit-letter` | `DIMM-1A`, `DIMM 1A`, `DIMM1A` | a digit and a letter |
| `mem-card` | `MEM A, J1` | memory card letter, slot number |
| `a-dimm` | `A1_DIMM0` | a slot number |
| `dimm-number` | `DIMM 1`, `DIMM1`, `Dimm 1` | a slot number that says no channel |

| Bank locator form | Examples | Parts |
| --- | --- | --- |
| `p-node-channel-dimm` | `P0_Node0_Channel0_Dimm0` | processor (from 0), node, channel number, slot |
| `p-channel-dimm` | `P0_Channel0_Dimm0` | processor (from 0), channel number, slot |
| `node-channel-dimm` | `_Node0_Channel0_Dimm0`, `A1_Node0_Channel0_Dimm0` | node, channel number, slot |
| `node-channel-dimm-words` | `NODE 0 CHANNEL 0 DIMM 0` | node, channel number, slot |
| `socket-channel-dimm` | `SOCKET 0 CHANNEL 0 DIMM 0` | processor, channel number, slot |
| `node` | `NODE 1` | node: a processor or a memory controller, the board decides |
| `p-channel-letter` | `P0 CHANNEL A` | processor (from 0), channel letter |
| `cpu-ch-letter` | `CPU0ChA` | processor, channel letter |
| `p-ch-letter` | `P0-CH-A` | processor, channel letter |
| `board-cpu` | `Primary Main Board/CPU 1` | processor |
| `cpu` | `CPU 1`, `CPU1` | processor |
| `sys-mb-p` | `/SYS/MB/P0` | processor (from 0) |
| `cpu-bank` | `CPU0_Bank1` | processor, bank |
| `p-bank` | `P1_BANK0` | processor, bank: the slots of a bank share their channel |
| `node-bank` | `Node0_Bank1` | node, bank |
| `node-bank-card` | `Node 0 Bank 1 MemCard 2` | chassis node, bank, memory card |
| `channel-letter` | `CHANNEL A`, `ChannelA` | channel letter |
| `chan-dimm` | `CHAN A DIMM 1` | channel letter, slot |
| `channel-number` | `Channel 1` | channel number |
| `letter` | `A` | channel letter (one capital) |
| `path` | `1/0/0/1` | a path of the partition |
| `dimm-number` | `DIMM1`, `DIMM 1` | the slot number of a locator `CPU1` |
| `same-as-locator` | `DIMM_A1`, `BANK_A2`, `A1` | the locator again: nothing |
| `bank-number` | `BANK 1`, `Bank 1`, `Slot 1` | a bank: nothing of the place |
| `number` | `7` | a bank number: nothing of the place |
| `bank-word` | `BANK` | nothing |
<!-- /forms -->

### Which processor, channel and slot

- A letter and a number (`A1`, `DIMM_A1`) or a digit and a letter (`1A`): where the bank locator spells channel and slot (`NODE 0 CHANNEL 0 DIMM 0`, `P0_Node0_Channel0_Dimm0`), the pairing decides. The letter is the channel when letters and bank channels pair one to one; for `1A` the digit is the channel when digits and bank channels pair one to one; otherwise the name is a label and the bank locator places the slot. A bank locator `P1_BANK0` groups the slots of one channel. Without such a bank locator the letter is the channel and the number the slot, except on Supermicro boards, whose `1A` has the channel in the digit and the slot in the letter. On Dell PowerEdge boards the letter is the processor: their families say so.
- The processor of a slot: the processor word of the locator, then that of the bank locator. A word counts from 0 when some slot of the board carries it with 0, else from 1; `PROC` counts from 1. Then a `Node` of the bank locator: with no more nodes than processor records a node is a processor, with a multiple of them it is one of several memory controllers of a processor (Intel Whitley boards write `NODE 1` to `NODE 4` on two processors). Then the only processor of a board with one record. Then the processor of the other slots of the same channel letter. Last, channel letters without a processor word split evenly over the processors in order, when their number divides by the processor records and no processor gets more letters than it has channels (`ChannelA` to `ChannelD` on the first of two Xeon E5, `ChannelE` to `ChannelH` on the second). A slot without a processor after that is set aside.
- A locator that names the channel letter stands when its bank locator names another channel; a line says so.
- The channels of a processor are numbered from 0 in the order of their names (letters, numbers, the memory controller first where several stand behind one processor), and the slots of a channel from 0 in the order of their numbers. The board draws the vendor's name of the channel under it.

### Board families

A family is a row of `internal/explorer/families.tsv` (361 rows), written from a public document of the board's maker. A row ties products to the document by the system product and the board name, and says what the document gives: which slot number sits on which processor, channel and position, which word of the names is which processor, how many channels a processor of the board has and how many slots a channel holds, which empty slots the manual labels where the firmware gives them no name, and which records the firmware writes for slots the board does not have. A board named only by its part number takes the row of the family whose named boards carry the same part number. A row whose document does not settle the place sets the slots of the board aside with the reason. Rows are tried in order; the first that fits a board decides. A board that fits no row is read by the rules above. The tables keep the facts of the documents in MemorY's own form; no text, table or drawing of a document is copied.

<!-- families -->
| Family | Maker | Boards | What the table gives | Documents |
| --- | --- | --- | --- | --- |
| `dell-cs24-sc` | dell | CS24-SC | set aside: no-document | none found |
| `board-strings-broken` | * | no product and no board | set aside: board-unknown | none found |
| `dell-board-unknown` | dell | `^(?:\|CounterACT\|OmniCube CN-3400-1\|AWS Elemental Appliance)\\|(?:.* )?(?:04N3DF\|0XWDCF\|04F3CJ\|0024FG)$` | set aside: board-unknown | none found |
| `dell-pn-0pxxhp` | dell | board 0PXXHP | a board known by its part number: the rule of `dell-12g-24slot` | `dell-e14s` |
| `dell-pn-0jp31p` | dell | board 0JP31P | a board known by its part number: the rule of `dell-12g-24slot` | `dell-e14s` |
| `dell-pn-0vrcy5` | dell | board 0VRCY5 | a board known by its part number: the rule of `dell-12g-24slot` | `dell-e14s` |
| `dell-pn-04jn2k` | dell | board 04JN2K | a board known by its part number: the rule of `dell-14g-16slot` | `dell-e45s` |
| `fujitsu-d2709-phantoms` | fujitsu | PRIMERGY TX200 S5 | records without a slot on the board: DIMM-3A, DIMM-3B, DIMM-3C, DIMM-3D, DIMM-3E, DIMM-3F | `fujitsu-d2709-tx200-s5` |
| `sm-x10dr-16-labels` | supermicro | X10DRi, X10DRH, X10DRW | labels of the manual for 16 slots, given to empty slots without a name; with 2 processor records, 16 memory devices | `sm-mnl-1491`, `sm-mnl-1628` |
| `sm-x10drg-24-labels` | supermicro | X10DRG | labels of the manual for 24 slots, given to empty slots without a name; with 2 processor records, 24 memory devices | `sm-mnl-1780` |
| `sm-x10sra-8-labels` | supermicro | X10SRA | labels of the manual for 8 slots, given to empty slots without a name; with 1 processor records, 8 memory devices | `sm-mnl-1643` |
| `gigabyte-mu71-su0-phantoms` | gigabyte | MU71-SU0-00 | records without a slot on the board: DIMM_P1_G0, DIMM_P1_G1, DIMM_P1_H0, DIMM_P1_I0, DIMM_P1_J0, DIMM_P1_J1, DIMM_P1_K0, DIMM_P1_L0 | `gigabyte-mu71-su0-user-manual` |
| `gigabyte-c621-su8-phantoms` | gigabyte | C621-SU8 | records without a slot on the board: CPU1_DIMM_B2, CPU1_DIMM_C2, CPU1_DIMM_E2, CPU1_DIMM_F2, CPU2_DIMM_A1, CPU2_DIMM_A2, CPU2_DIMM_B1, CPU2_DIMM_B2, CPU2_DIMM_C1, CPU2_DIMM_C2, CPU2_DIMM_D1, CPU2_DIMM_D2, CPU2_DIMM_E1, CPU2_DIMM_E2, CPU2_DIMM_F1, CPU2_DIMM_F2 | `gigabyte-c621-su8-user-manual` |
| `gigabyte-c621-sd8-phantoms` | gigabyte | C621-SD8 | records without a slot on the board: CPU1_DIMM_A2, CPU1_DIMM_B2, CPU1_DIMM_C1, CPU1_DIMM_C2, CPU1_DIMM_D2, CPU1_DIMM_E2, CPU1_DIMM_F1, CPU1_DIMM_F2, CPU2_DIMM_A2, CPU2_DIMM_B2, CPU2_DIMM_C1, CPU2_DIMM_C2, CPU2_DIMM_D2, CPU2_DIMM_E2, CPU2_DIMM_F1, CPU2_DIMM_F2 | `gigabyte-c621-sd8-user-manual` |
| `tyan-s4985-ck8` | tyan | CK8 (board S4985) | set aside: names-not-in-document | `tyan-s4985-user-manual`, `tyan-m4985-user-manual` |
| `msi-ms9661-mcp55` | msi | MCP55 (board MS-9661) | set aside: no-channel-in-document | `msi-ms9661-manual` |
| `msi-ms9280` | msi | MS-9280 (board MS-9280) | set aside: no-document | none found |
| `aopw-plus` | * | AOPW-PLUS (board AOPW-PLUS) | set aside: no-document | none found |
| `gmktec-nucbox8` | * | NucBox8 (board GMKtec) | set aside: no-document | none found |
| `dell-13g-24slot` | dell | PowerEdge R630, board PowerEdge R630, PowerEdge R730xd, board PowerEdge R730xd, PowerEdge R730, board PowerEdge R730, PowerEdge T630, board PowerEdge T630, PowerEdge R730XD, board PowerEdge R730XD, OmniCube CN-3400-1, board OmniCube CN-3400-1 | slot number to channel and position on the letter (A the first processor); channels 0, 1, 2, 3 | `dell-e26s` |
| `dell-12g-24slot` | dell | PowerEdge R720, board PowerEdge R720, PowerEdge R620, board PowerEdge R620, PowerEdge R720xd, board PowerEdge R720xd, PowerEdge T620, board PowerEdge T620, OEM-R 720xd, board OEM-R 720xd | slot number to channel and position on the letter (A the first processor); channels 0, 1, 2, 3 | `dell-e14s` |
| `dell-12g-e5-2400-2s-12slot` | dell | PowerEdge R520, board PowerEdge R520, PowerEdge R420, board PowerEdge R420, PowerEdge T420, board PowerEdge T420 | slot number to channel and position on the letter (A the first processor); channels 1, 2, 3 | `dell-e18s` |
| `dell-12g-e5-2400-1s-6slot` | dell | PowerEdge T320, board PowerEdge T320, PowerEdge R320, board PowerEdge R320 | slot number to channel and position on the letter (A the first processor); channels 1, 2, 3 | `dell-e20s` |
| `dell-13g-12slot` | dell | PowerEdge R430, board PowerEdge R430, PowerEdge R530, board PowerEdge R530, PowerEdge T430, board PowerEdge T430 | slot number to channel and position on the letter (A the first processor), another table for the second processor; channels 0, 1, 2, 3 | `dell-e28s` |
| `dell-11g-18slot` | dell | PowerEdge R710, board PowerEdge R710, PowerEdge T710, board PowerEdge T710 | slot number to channel and position on the letter (A the first processor); channels unnamed, unnamed, unnamed | `dell-r710-owner-manual` |
| `dell-11g-12slot` | dell | PowerEdge R610, board PowerEdge R610, PowerEdge T610, board PowerEdge T610 | slot number to channel and position on the letter (A the first processor); channels unnamed, unnamed, unnamed | `dell-r610-owner-manual` |
| `dell-11g-8slot` | dell | PowerEdge R510, board PowerEdge R510, PowerEdge R410, board PowerEdge R410, PowerEdge T410, board PowerEdge T410 | slot number to channel and position on the letter (A the first processor); channels 0, 1, 2 | `dell-r410-owner-manual` |
| `dell-11g-x3400-6slot` | dell | PowerEdge T310, board PowerEdge T310, PowerEdge R310, board PowerEdge R310 | slot number to channel and position on the letter (A the first processor); channels A, B | `dell-t310-owner-manual` |
| `dell-11g-x3400-4slot` | dell | PowerEdge R210, board PowerEdge R210, PowerEdge T110, board PowerEdge T110 | slot number to channel and position on the letter (A the first processor); channels unnamed, unnamed | `dell-r210-owner-manual` |
| `dell-e3-4slot-lettered` | dell | PowerEdge R210 II, board PowerEdge R210 II, PowerEdge T110 II, board PowerEdge T110 II | the names place the slots | `dell-e10s` |
| `dell-12g-r220` | dell | PowerEdge R220, board PowerEdge R220 | slot number to channel and position on the one processor; channels 1, 2 | `dell-e10s-2` |
| `dell-13g-1s-4slot` | dell | PowerEdge R230, board PowerEdge R230, PowerEdge T330, board PowerEdge T330, PowerEdge T130, board PowerEdge T130, PowerEdge R330, board PowerEdge R330 | slot number to channel and position on the letter (A the first processor); channels 0, 1 | `dell-e33s` |
| `dell-14g-1s-4slot` | dell | PowerEdge R340, board PowerEdge R340, PowerEdge R240, board PowerEdge R240, PowerEdge T340, board PowerEdge T340, PowerEdge T140, board PowerEdge T140 | slot number to channel and position on the letter (A the first processor); channels 0, 1 | `dell-e57s` |
| `dell-15g-1s-4slot` | dell | PowerEdge T150, board PowerEdge T150, PowerEdge R350, board PowerEdge R350 | slot number to channel and position on the letter (A the first processor); channels 0, 1 | `dell-e80s` |
| `dell-14g-24slot` | dell | PowerEdge R740, board PowerEdge R740, PowerEdge R640, board PowerEdge R640, PowerEdge R740xd, board PowerEdge R740xd, PowerEdge T640, board PowerEdge T640, XC640-10 CORE, board XC640-10 CORE, XC740xd-12 CORE, board XC740xd-12 CORE | slot number to channel and position on the letter (A the first processor); channels 0, 1, 2, 3, 4, 5 | `dell-e38s` |
| `dell-14g-16slot` | dell | PowerEdge R440, board PowerEdge R440, PowerEdge T440, board PowerEdge T440, PowerEdge R540, board PowerEdge R540, PowerEdge R740xd2, board PowerEdge R740xd2 | slot number to channel and position on the letter (A the first processor), another table for the second processor; channels 0, 1, 2, 3, 4, 5 | `dell-e45s` |
| `dell-14g-epyc7001-32slot` | dell | PowerEdge R7425, board PowerEdge R7425 | slot number to channel and position on the letter (A the first processor); channels 0, 1, 2, 3, 4, 5, 6, 7 | `dell-e38s-2` |
| `dell-15g-epyc-1s-16slot` | dell | PowerEdge R6515, board PowerEdge R6515, PowerEdge R7515, board PowerEdge R7515 | slot number to channel and position on the letter (A the first processor); channels A, B, C, D, E, F, G, H | `dell-e45s-2` |
| `dell-15g-epyc-2s-32slot` | dell | PowerEdge R7525, board PowerEdge R7525 | slot number to channel and position on the letter (A the first processor); channels A, B, C, D, E, F, G, H | `dell-e68s` |
| `dell-15g-icx-32slot` | dell | PowerEdge R750, board PowerEdge R750, PowerEdge R650, board PowerEdge R650 | slot number to channel and position on the letter (A the first processor); channels A, B, C, D, E, F, G, H | `dell-e70s` |
| `dell-15g-icx-16slot` | dell | PowerEdge R450, board PowerEdge R450, PowerEdge T550, board PowerEdge T550 | slot number to channel and position on the letter (A the first processor); channels A, B, C, D, E, F, G, H | `dell-e74s` |
| `dell-16g-epyc9004-2s-24slot` | dell | PowerEdge R7625, board PowerEdge R7625 | slot number to channel and position on the letter (A the first processor); channels A, B, C, D, E, F, G, H, I, J, K, L | `dell-e94s` |
| `dell-16g-epyc9004-1s-12slot` | dell | PowerEdge R6615, board PowerEdge R6615 | slot number to channel and position on the letter (A the first processor); channels A, B, C, D, E, F, G, H, I, J, K, L | `dell-e97s` |
| `dell-16g-spr-16slot` | dell | PowerEdge R660xs, board PowerEdge R660xs | slot number to channel and position on the letter (A the first processor); channels A, B, C, D, E, F, G, H | `dell-e87s` |
| `dell-xr4510c` | dell | PowerEdge XR4510c, board PowerEdge XR4510c | slot number to channel and position on the letter (A the first processor); channels A1, A2, A3, A4 | `dell-xr4510c-installation-service-manual` |
| `dell-precision-7920-tower` | dell | Precision 7920 Tower, board Precision 7920 Tower | slot number to channel and position on the processor word of the names; channels Ch0, Ch1, Ch2, Ch3, Ch4, Ch5 | `dell-d04x` |
| `dell-precision-7820-tower` | dell | Precision 7820 Tower, board Precision 7820 Tower | slot number to channel and position on the processor word of the names; channels Ch0, Ch1, Ch2, Ch3, Ch4, Ch5 | `dell-d02t` |
| `dell-precision-rack-7910` | dell | Precision Rack 7910, board Precision Rack 7910 | slot number to channel and position on the letter (A the first processor); channels 0, 1, 2, 3 | `dell-e31s` |
| `dell-precision-7920-rack` | dell | Precision 7920 Rack, board Precision 7920 Rack | slot number to channel and position on the letter (A the first processor); channels 0, 1, 2, 3, 4, 5 | `dell-e38s-3` |
| `dell-precision-7960-rack` | dell | Precision 7960 Rack, board Precision 7960 Rack | slot number to channel and position on the letter (A the first processor); channels CH_A, CH_B, CH_C, CH_D, CH_E, CH_F, CH_G, CH_H | `dell-e82s` |
| `dell-c4140` | dell | PowerEdge C4140, board PowerEdge C4140 | slot number to channel and position on the letter (A the first processor); channels 0, 1, 2, 3, 4, 5 | `dell-e53s` |
| `dell-c6520` | dell | PowerEdge C6520, board PowerEdge C6520 | slot number to channel and position on the letter (A the first processor); channels A, B, C, D, E, F, G, H | `dell-e64s` |
| `dell-c6525` | dell | PowerEdge C6525, board PowerEdge C6525 | slot number to channel and position on the letter (A the first processor); channels A, B, C, D, E, F, G, H | `dell-e63s` |
| `dell-c6220ii-c8220` | dell | PowerEdge C6220 II, board PowerEdge C6220 II, PowerEdge C8220, board PowerEdge C8220 | slot number to channel and position on the letter (A the first processor); channels CHA, CHB, CHC, CHD | `dell-c6220-owner-manual` |
| `dell-c6320` | dell | PowerEdge C6320, board PowerEdge C6320 | slot number to channel and position on the letter (A the first processor); channels CHA, CHB, CHC, CHD | `dell-b08s` |
| `dell-c5220` | dell | PowerEdge C5220, board PowerEdge C5220 | set aside: no-channel-in-document | `dell-b04s` |
| `dell-c6100-numbered` | dell | C6100, board C6100, XS23-TY35, board XS23-TY35 | set aside: names-not-in-document | `dell-c6100-owner-manual` |
| `dell-xs23-ty3-spelled` | dell | XS23-TY3, board XS23-TY3 | the names place the slots | `dell-c6100-owner-manual` |
| `dell-c1100-cs24-ty` | dell | CS24-TY, board CS24-TY | the names place the slots | `dell-c1100-owner-manual` |
| `dell-pe-1950-2950` | dell | PowerEdge 1950, board PowerEdge 1950, PowerEdge 2950, board PowerEdge 2950 | slot number to channel and position on the memory controller of the chipset; channels 0, 1, 2, 3 | `dell-1950-owner-manual` |
| `dell-pe-2900` | dell | PowerEdge 2900, board PowerEdge 2900 | slot number to channel and position on the memory controller of the chipset; channels 0, 1, 2, 3 | `dell-2900-owner-manual` |
| `dell-pe-sc1430` | dell | PowerEdge SC1430, board PowerEdge SC1430 | slot number to channel and position on the memory controller of the chipset; channels 0, 1 | `dell-sc1430-owner-manual` |
| `dell-precision-r5400` | dell | Precision WorkStation R5400, board Precision WorkStation R5400 | set aside: no-channel-in-document | `dell-r5400-service-manual` |
| `dell-pe-t300` | dell | PowerEdge T300, board PowerEdge T300 | slot number to channel and position on the memory controller of the chipset; channels A, B | `dell-t300-owner-manual` |
| `dell-pe-840-860-r200` | dell | PowerEdge 840, board PowerEdge 840, PowerEdge 860, board PowerEdge 860, PowerEdge R200, board PowerEdge R200 | memory on the chipset, the names place the slots; 2 channels of 2 slots | `dell-860-owner-manual` |
| `dell-pe-2850-sc1425` | dell | PowerEdge 2850, board PowerEdge 2850, PowerEdge SC1425, board PowerEdge SC1425 | memory on the chipset, the names place the slots; 2 channels of 3 slots | `dell-2850-installation-guide` |
| `dell-pe-2970` | dell | PowerEdge 2970, board PowerEdge 2970 | set aside: no-channel-in-document | `dell-2970-owner-manual` |
| `dell-pe-r805` | dell | PowerEdge R805, board PowerEdge R805 | set aside: no-channel-in-document | `dell-r805-owner-manual` |
| `dell-pe-r900` | dell | PowerEdge R900, board PowerEdge R900 | memory on the chipset, the names place the slots; 4 channels of 8 slots | `dell-r900-owner-manual` |
| `dell-pe-r910` | dell | PowerEdge R910, board PowerEdge R910 | set aside: no-processor-in-document | `dell-e06s` |
| `dell-pe-r810` | dell | PowerEdge R810, board PowerEdge R810 | slot number to channel and position on the letter (A the first processor); channels unnamed, unnamed, unnamed, unnamed | `dell-e05s` |
| `dell-pe-r815-r715` | dell | PowerEdge R815, board PowerEdge R815, PowerEdge R715, board PowerEdge R715 | slot number to channel and position on the letter (A the first processor); channels unnamed, unnamed, unnamed, unnamed | `dell-e05s-2` |
| `dell-pe-r515` | dell | PowerEdge R515, board PowerEdge R515 | slot number to channel and position on the letter (A the first processor); channels unnamed, unnamed | `dell-r515-owner-manual` |
| `dell-pe-r820` | dell | PowerEdge R820, board PowerEdge R820 | slot number to channel and position on the letter (A the first processor); channels 0, 1, 2, 3 | `dell-e21s` |
| `hp-gen9-24slot` | hp | ProLiant DL360 Gen9, board ProLiant DL360 Gen9, ProLiant DL380 Gen9, board ProLiant DL380 Gen9 | slot number to channel and position on the processor word of the names; channels 1, 2, 3, 4 | `hpe-767927-009`, `hpe-768830-401a` |
| `hp-gen8-24slot` | hp | ProLiant DL380p Gen8, board ProLiant DL380p Gen8, ProLiant DL360p Gen8, board ProLiant DL360p Gen8 | slot number to channel and position on the processor word of the names; channels 1, 2, 3, 4 | `hpe-dl380p-gen8-user-guide`, `hpe-664724-006`, `hpe-ddr3-gen8` |
| `hp-gen8-e5-2400-12slot` | hp | ProLiant DL360e Gen8, board ProLiant DL360e Gen8, ProLiant DL380e Gen8, board ProLiant DL380e Gen8 | slot number to channel and position on the processor word of the names; channels 1, 2, 3 | `hpe-675786-006`, `hpe-676151-006`, `hpe-ddr3-gen8` |
| `hp-gen10-24slot` | hp | ProLiant DL360 Gen10, board ProLiant DL360 Gen10, ProLiant DL380 Gen10, board ProLiant DL380 Gen10 | slot number to channel and position on the processor word of the names; channels CH1, CH2, CH3, CH4, CH5, CH6 | `hpe-population-gen10-technical`, `hpe-dl380-gen10-user-guide`, `hpe-dl360-gen10-maintenance-service-guide` |
| `hp-ml350-gen10` | hp | ProLiant ML350 Gen10, board ProLiant ML350 Gen10 | slot number to channel and position on the processor word of the names; channels CH1, CH2, CH3, CH4, CH5, CH6 | `hpe-population-gen10-technical`, `hpe-ml350-gen10-user-guide`, `hpe-870691-007` |
| `hp-bl460c-gen8` | hp | ProLiant BL460c Gen8, board ProLiant BL460c Gen8 | slot number to channel and position on the processor word of the names; channels 1, 2, 3, 4 | `hpe-656396-004`, `hpe-656396-002`, `hpe-ddr3-gen8` |
| `hp-bl460c-gen9` | hp | ProLiant BL460c Gen9, board ProLiant BL460c Gen9 | slot number to channel and position on the processor word of the names; channels 1, 2, 3, 4 | `hpe-768826-007` |
| `hp-bl460c-gen10` | hp | ProLiant BL460c Gen10, board ProLiant BL460c Gen10 | slot number to channel and position on the processor word of the names; channels CH1, CH2, CH3, CH4, CH5, CH6 | `hpe-876833-006`, `hpe-population-gen10-technical` |
| `hp-microserver-gen10` | hp | ProLiant MicroServer Gen10, board ProLiant MicroServer Gen10 | slot number to channel and position on the one processor; channels A, B | `hpe-872677-004`, `hpe-gen10-maintenance-service-guide`, `hpe-gen10` |
| `hp-ml10-gen9` | hp | ML10Gen9, board ML10Gen9 | the names place the slots | `hpe-833836-073`, `hpe-833835-004`, `hpe-ml10-generation9-gen9` |
| `hp-dl60-gen9` | hp | ProLiant DL60 Gen9, board ProLiant DL60 Gen9 | slot number to channel and position on the processor word of the names; channels 1, 2, 3, 4 | `hpe-786245-003` |
| `hp-dl20-gen9` | hp | ProLiant DL20 Gen9, board ProLiant DL20 Gen9 | slot number to channel and position on the processor word of the names; channels 1, 2 | `hpe-826318-003a` |
| `hp-dl160-gen8` | hp | ProLiant DL160 Gen8, board ProLiant DL160 Gen8 | slot number to channel and position on the processor word of the names, another table for the second processor; channels 1, 2, 3, 4 | `hpe-670519-006`, `hpe-670554-007` |
| `hp-dl385p-gen8` | hp | ProLiant DL385p Gen8, board ProLiant DL385p Gen8 | slot number to channel and position on the processor word of the names; channels Ch1, Ch2, Ch3, Ch4 | `hpe-dl385p-gen8-user-guide` |
| `hp-gen9-16slot` | hp | ProLiant DL160 Gen9, board ProLiant DL160 Gen9, ProLiant DL180 Gen9, board ProLiant DL180 Gen9 | slot number to channel and position on the processor word of the names; channels 1, 2, 3, 4 | `hpe-775786-004`, `hpe-775439-004` |
| `hp-dl120-gen9` | hp | ProLiant DL120 Gen9, board ProLiant DL120 Gen9 | slot number to channel and position on the processor word of the names; channels 1, 2, 3, 4 | `hpe-781878-003` |
| `hp-g6g7-18slot-lettered` | hp | ProLiant DL360 G7, board ProLiant DL360 G7, ProLiant DL380 G7, board ProLiant DL380 G7, ProLiant DL380 G6, board ProLiant DL380 G6 | slot number to channel and position on the processor word of the names; channels 1, 2, 3 | `hpe-495624-003` |
| `hp-dl360-g6-18slot-unlettered` | hp | ProLiant DL360 G6, board ProLiant DL360 G6 | slot number to channel and position on the processor word of the names; channels 1, 2, 3 | `hpe-517689-005` |
| `hp-g5-fbdimm-8slot` | hp | ProLiant DL360 G5, board ProLiant DL360 G5, ProLiant DL380 G5, board ProLiant DL380 G5 | set aside: no-channel-in-document | `hpe-406316-002` |
| `hp-z6-g4` | hp | HP Z6 G4 Workstation, board HP Z6 G4 Workstation | slot number to channel and position on the processor word of the names, another table for the second processor; channels 0.0, 0.1, 0.2, 1.0, 1.1, 1.2 | `hpe-setup-z6-g4-z8-g4`, `hp-z6-g4-tech-wp`, `hp-z6-g4-quickspecs`, `hp-z6-g4-msg` |
| `hp-z8-g4` | hp | HP Z8 G4 Workstation, board HP Z8 G4 Workstation | slot number to channel and position on the processor word of the names, another table for the second processor; channels 0.0, 0.1, 0.2, 1.0, 1.1, 1.2 | `hpe-setup-z6-g4-z8-g4` |
| `hp-dl320-g6-9slot` | hp | ProLiant DL320 G6, board ProLiant DL320 G6 | slot number to channel and position on the processor word of the names; channels 1, 2, 3 | `hpe-530514-002` |
| `hp-dl160-g6-18slot` | hp | ProLiant DL160 G6, board ProLiant DL160 G6 | slot number to channel and position on the processor word of the names; channels Chnl1, Chnl2, Chnl3 | `hpe-dl160-6-g6` |
| `hp-dl180-g6-12slot` | hp | ProLiant DL180 G6, board ProLiant DL180 G6 | slot number to channel and position on the processor word of the names; channels Chnl1, Chnl2, Chnl3 | `hpe-dl180-6-g6` |
| `hp-bl460c-g6g7-12slot` | hp | ProLiant BL460c G6, board ProLiant BL460c G6, ProLiant BL460c G7, board ProLiant BL460c G7 | slot number to channel and position on the processor word of the names; channels 1, 2, 3 | `hpe-501274-002` |
| `hp-dl120-g6-6slot` | hp | ProLiant DL120 G6, board ProLiant DL120 G6 | slot number to channel and position on the one processor; channels 1, 2 | `hpe-579574-004` |
| `hp-dl120-g7-4slot` | hp | ProLiant DL120 G7, board ProLiant DL120 G7 | slot number to channel and position on the one processor; channels A, B | `hpe-638025-003a` |
| `hp-dl385-g7-24slot` | hp | ProLiant DL385 G7, board ProLiant DL385 G7 | slot number to channel and position on the processor word of the names; channels A, B, C, D | `hpe-588708-001` |
| `hp-dl585-g7-48slot` | hp | ProLiant DL585 G7, board ProLiant DL585 G7 | slot number to channel and position on the processor word of the names; channels A, B, C, D | `hpe-617692-004` |
| `hp-bl685c-g7-32slot` | hp | ProLiant BL685c G7, board ProLiant BL685c G7 | slot number to channel and position on the processor word of the names; channels 1, 2, 3, 4 | `hpe-593961-001` |
| `hp-dl165-g7-24slot` | hp | ProLiant DL165 G7, board ProLiant DL165 G7 | slot number to channel and position on the processor word of the names; channels A, B, C, D | `hpe-601465-003` |
| `hp-dl145-g3-8slot` | hp | ProLiant DL145 G3, board ProLiant DL145 G3 | set aside: no-channel-in-document | `hpe-430047-004` |
| `hp-dl580-g7-cartridges` | hp | ProLiant DL580 G7, board ProLiant DL580 G7 | memory cards, 2 to a processor; slot number on the card to channel and position | `hpe-595656-005` |
| `hp-dl140-g2-8slot` | hp | ProLiant DL140 G2, board ProLiant DL140 G2 | set aside: no-channel-in-document | `hpe-381737-004` |
| `hp-g4-ddr2-6slot` | hp | ProLiant DL360 G4p, board ProLiant DL360 G4p, ProLiant DL380 G4, board ProLiant DL380 G4 | set aside: no-channel-in-document | `hpe-359226-008` |
| `hp-compaq-dl380-g1-4slot` | hp | ProLiant DL380, board ProLiant DL380 | set aside: no-channel-in-document | `hpe-136363-004` |
| `hp-superdome-x-bl920s-gen8` | hp | Superdome2 16s x86, board Superdome2 16s x86 | set aside: no-channel-in-document | `hpe-hpe-integrity-superdome` |
| `hp-netserver-lh4-memory-boards` | hp | HP NetServer, board HP NetServer | set aside: no-channel-in-document | `hpe-4-4r-user-guide` |
| `ibm-x3550m3-x3650m3-18slot` | ibm | `^System x3550 M3 -\[2145CG8\]-\\|\|^\\|(?:.* )?System x3550 M3 -\[2145CG8\]-$\|^System x3550 M3 -\[7944AC1\]-\\|\|^\\|(?:.* )?System x3550 M3 -\[7944AC1\]-$\|^System x3550 M3 -\[7042CR6\]-\\|\|^\\|(?:.* )?System x3550 M3 -\[7042CR6\]-$\|^System x3550 M3 -\[7944KFG\]-\\|\|^\\|(?:.* )?System x3550 M3 -\[7944KFG\]-$\|^System x3550 M3 -\[7944SCP\]-\\|\|^\\|(?:.* )?System x3550 M3 -\[7944SCP\]-$\|^System x3550 M3 -\[7944KAG\]-\\|\|^\\|(?:.* )?System x3550 M3 -\[7944KAG\]-$\|^System x3650 M3 -\[7945AC1\]-\\|\|^\\|(?:.* )?System x3650 M3 -\[7945AC1\]-$\|^System x3650 M3 -\[7945YBU\]-\\|\|^\\|(?:.* )?System x3650 M3 -\[7945YBU\]-$\|^System x3650 M3 -\[794562M\]-\\|\|^\\|(?:.* )?System x3650 M3 -\[794562M\]-$\|^System x3650 M3 -\[794522U\]-\\|\|^\\|(?:.* )?System x3650 M3 -\[794522U\]-$` | slot numbers that run over the processors, to processor, channel and position; channels CH0, CH1, CH2 | `ibm-x3550-m3-4254-7944-installation` |
| `ibm-x3550m2-x3650m2-16slot` | ibm | `^IBM System x -\[79463AM\]-\\|\|^\\|(?:.* )?IBM System x -\[79463AM\]-$\|^System x3550 M2 -\[794644M\]-\\|\|^\\|(?:.* )?System x3550 M2 -\[794644M\]-$\|^System x3550 M2 -\[794622V\]-\\|\|^\\|(?:.* )?System x3550 M2 -\[794622V\]-$\|^System x3650 M2 -\[7947AC1\]-\\|\|^\\|(?:.* )?System x3650 M2 -\[7947AC1\]-$` | slot numbers that run over the processors, to processor, channel and position; channels CH0, CH1, CH2 | `ibm-x3550-m2-4198-7946-installation` |
| `ibm-x3400m3-16slot` | ibm | `^IBM System x3400 M3 Server -\[737958G\]-\\|\|^\\|(?:.* )?IBM System x3400 M3 Server -\[737958G\]-$` | slot numbers that run over the processors, to processor, channel and position; channels CH0, CH1, CH2 | `ibm-x3400-m3-7378-7379-installation` |
| `ibm-x3550m4-x3650m4-24slot` | ibm | `^IBM System x3650 M4: -\[2145DH8\]-\\|\|^\\|(?:.* )?IBM System x3650 M4: -\[2145DH8\]-$\|^IBM System x3550 M4 Server -\[7914AC1\]-\\|\|^\\|(?:.* )?IBM System x3550 M4 Server -\[7914AC1\]-$\|^System x3550 M4 -\[7914B2A\]-\\|\|^\\|(?:.* )?System x3550 M4 -\[7914B2A\]-$\|^System x3650 M4 -\[7915E1G\]-\\|\|^\\|(?:.* )?System x3650 M4 -\[7915E1G\]-$\|^IBM System x3550 M4 Server -\[7914Z5Q\]-\\|\|^\\|(?:.* )?IBM System x3550 M4 Server -\[7914Z5Q\]-$` | slot numbers that run over the processors, to processor, channel and position; channels Ch0, Ch1, Ch2, Ch3 | `ibm-x3550-m4-7914-installation-x3550` |
| `ibm-x3300m4-12slot` | ibm | `^System x3300 M4 -\[7382E4G\]-\\|\|^\\|(?:.* )?System x3300 M4 -\[7382E4G\]-$\|^System x3300 M4 -\[7382E3G\]-\\|\|^\\|(?:.* )?System x3300 M4 -\[7382E3G\]-$` | slot numbers that run over the processors, to processor, channel and position; channels 0, 1, 2 | `ibm-x3300-m4-7382` |
| `ibm-flex-x220-12slot` | ibm | `^Flex System x220 Compute Node -\[7906D2G\]-\\|\|^\\|(?:.* )?Flex System x220 Compute Node -\[7906D2G\]-$` | slot numbers that run over the processors, to processor, channel and position; channels A, B, C, D, E, F | `ibm-x220-installation-service-guide-7906` |
| `ibm-flex-x240-24slot` | ibm | `^Flex System x240 Compute Node -\[8737AC1\]-\\|\|^\\|(?:.* )?Flex System x240 Compute Node -\[8737AC1\]-$` | slot numbers that run over the processors, to processor, channel and position; channels A, B, C, D | `ibm-x240-7863-8737-8738-8956` |
| `ibm-bladecenter-hs23-16slot` | ibm | `^BladeCenter HS23 -\[7875MFM\]-\\|\|^\\|(?:.* )?BladeCenter HS23 -\[7875MFM\]-$` | slot numbers that run over the processors, to processor, channel and position; channels 0, 1, 2, 3 | `ibm-hs23-installation-user-guide-7875` |
| `ibm-idataplex-dx360m4-16slot` | ibm | `^IBM System X iDataPlex dx360 M4 Server -\[7912AC1\]-\\|\|^\\|(?:.* )?IBM System X iDataPlex dx360 M4 Server -\[7912AC1\]-$` | slot numbers that run over the processors, to processor, channel and position; channels 0, 1, 2, 3 | `ibm-dx360-m4-7912-7913-user` |
| `ibm-x3250m3-6slot` | ibm | `^System x3250 M3 -\[4252K4G\]-\\|\|^\\|(?:.* )?System x3250 M3 -\[4252K4G\]-$\|^System x3250 M3 -\[4252PAW\]-\\|\|^\\|(?:.* )?System x3250 M3 -\[4252PAW\]-$` | slot number to channel and position on the one processor; channels X, Y | `ibm-x3250-m3-4251-4252-4261` |
| `ibm-x3250m4-4slot` | ibm | `^IBM System X3250 M4 -\[2583AC1\]-\\|\|^\\|(?:.* )?IBM System X3250 M4 -\[2583AC1\]-$` | slot number to channel and position on the one processor; channels X, Y | `ibm-x3250-m4-installation-service-guide` |
| `ibm-x3200m2-4slot` | ibm | `^IBM System x3200 M2 -\[4368IGS\]-\\|\|^\\|(?:.* )?IBM System x3200 M2 -\[4368IGS\]-$` | slot number to channel and position on the memory controller of the chipset; channels 0, 1 | `ibm-x3200-m2-4367-4368-installation` |
| `ibm-x3650-7979-12slot` | ibm | `^IBM System x3650 -\[7979AC1\]-\\|\|^\\|(?:.* )?IBM System x3650 -\[7979AC1\]-$\|^IBM System x3650 -\[7979B9U\]-\\|\|^\\|(?:.* )?IBM System x3650 -\[7979B9U\]-$` | slot number to channel and position on the memory controller of the chipset; channels 0, 1, 2, 3 | `ibm-x3650-7979-user-guide` |
| `ibm-xseries235-6slot` | ibm | `^eserver xSeries 235 -\[86712AX\]-\\|\|^\\|(?:.* )?eserver xSeries 235 -\[86712AX\]-$` | set aside: no-channel-in-document | `ibm-235-8671-installation-guide` |
| `ibm-x3850x5-64slot` | ibm | `^System x3850 X5 -\[7143Y61\]-\\|\|^\\|(?:.* )?System x3850 X5 -\[7143Y61\]-$` | set aside: no-channel-in-document | `ibm-x3850-x5-x3950-x5-7145` |
| `intel-s5000-fbd-8slot` | * | S5000PSL, board S5000PSL, S5000PAL, board S5000PAL, S5000XVN, board S5000XVN | memory on the chipset, the names place the slots; 4 channels of 2 slots | `intel-s5000psl-s5000xsl-technical` |
| `intel-s5000vsa` | * | S5000VSA, board S5000VSA, PR1710N, board PR1710N | memory on the chipset, the names place the slots; 2 channels of 4 slots | `intel-s5000vsa-technical` |
| `intel-s5000vcl` | * | S5000VCL, board S5000VCL | memory on the chipset, the names place the slots; 2 channels of 3 slots | `intel-s5000vcl-technical` |
| `intel-s3210sh` | * | S3210SH, board S3210SH | memory on the chipset, the names place the slots; 2 channels of 2 slots | `intel-e14960-009` |
| `intel-se7501wv2` | * | SE7501WV2S, board SE7501WV2S | memory on the chipset, the names place the slots; 2 channels of 3 slots | `intel-se7501wv2-technical` |
| `intel-s5520-12slot` | intel | S5520HC, board S5520HC, S5520SC, board S5520SC | the names place the slots | `intel-e39529-013` |
| `intel-s5500hcv` | intel | S5500HCV, board S5500HCV | the names place the slots | `intel-e39529-013` |
| `intel-s5520ur` | intel | S5520UR, board S5520UR | the names place the slots | `intel-e44031-012` |
| `intel-s5500bc` | intel | S5500BC, board S5500BC | the names place the slots | `intel-e42249-009` |
| `intel-s3420gp` | intel | S3420GP, board S3420GP, ......., board ......., S3420GPV, board S3420GPV | the names place the slots | `intel-e65697-010` |
| `intel-s1200bt` | intel | S1200BTL, board S1200BTL | the names place the slots | `intel-s1200bt-technical` |
| `intel-s1200v3rp` | intel | S1200RP, board S1200RP, S1200RP_SE, board S1200RP_SE | the names place the slots | `intel-s1200v3rp-technical` |
| `intel-s1200sp` | intel | S1200SP, board S1200SP, ParX T404i G6 Server, board ParX T404i G6 Server, ParX T100c G6 Server, board ParX T100c G6 Server | the names place the slots | `intel-s1200sp-technical` |
| `intel-s2600gz` | intel | S2600GZ, board S2600GZ | the names place the slots | `intel-s2600gz-technical` |
| `intel-s2600cp-w2600cr` | intel | S2600CP, board S2600CP, CYPRESS11, board CYPRESS11, S2600CP .........., board S2600CP .........., W2600CR, board W2600CR, W2600CR .........., board W2600CR .......... | the names place the slots | `intel-s2600cp-p4000cp-technical` |
| `intel-s4600lh2` | intel | S4600LH, board S4600LH, LH Pass, board LH Pass | the names place the slots | `intel-s4600lh2-t2-technical` |
| `intel-s2600wt` | intel | S2600WTTR, board S2600WTTR | the names place the slots | `intel-s2600wt-technical-tps` |
| `intel-s2600cw` | intel | S2600CW, board S2600CW | the names place the slots | `intel-s2600cw-technical` |
| `intel-s2600wf` | intel | S2600WFT, board S2600WFT | the names place the slots | `intel-s2600wf-technical` |
| `intel-s2600st` | intel | S2600STB, board S2600STB | the names place the slots | `intel-s2600st-technical` |
| `intel-m50cyp` | intel | M50CYP2SBSTD, board M50CYP2SBSTD | the names place the slots | `intel-m50cyp2sb-technical` |
| `intel-whitley-ref` | intel | WHITLEY, board WHITLEY | the names place the slots | `intel-m50cyp2sb-technical` |
| `intel-m70klp` | intel | M70KLP2SB M90598-001, board M70KLP2SB M90598-001 | the names place the slots | `intel-m70klp-technical` |
| `intel-eaglestream-ref` | intel | ArcherCity, board ArcherCity, EAGLESTREAM, board EAGLESTREAM, AST2600EVB, board AST2600EVB | the names place the slots | `intel-m50fcp2sbstd-technical` |
| `intel-birchstream-ref` | intel | BIRCHSTREAM, board BIRCHSTREAM | the names place the slots | `intel-no-document-of` |
| `amd-ethanol-x` | * | ETHANOL_DEBUG_X, board ETHANOL_DEBUG_X | the names place the slots | `amd-population-7003` |
| `amd-volcano` | * | Volcano, board Volcano | the names place the slots | `amd-9004-population` |
| `asus-p8b-m` | asus | board P8B-M Series, TERRA_PC, board TERRA_PC, Flagman TP100.3, board Flagman TP100.3 | the names place the slots | `asus-rs100-e7-pi2-user-guide` |
| `asus-p8b-x-e` | asus | TS100-E7-PI4, board TS100-E7-PI4, TS300-E7-PS4, board TS300-E7-PS4, board P8B-E Series | the names place the slots | `asus-ts300-e7-ps4-user-guide` |
| `asus-p9d-4slot` | asus | TS110-E8-PI4, board TS110-E8-PI4, TERRA_SERVER, board TERRA_SERVER, P9D-X Series, board P9D-X Series, P9D-V Series, board P9D-V Series, P9D-M Series, board P9D-M Series | the names place the slots | `asus-p9d-user-guide` |
| `asus-p12r` | asus | RS300-E11-PS4, board RS300-E11-PS4, RS100-E11-PI2, board RS100-E11-PI2, board P12R-E Series | the names place the slots | `asus-e28203` |
| `asus-e3-2slot` | asus | P9D-I Series, board P9D-I Series, P10S-I Series, board P10S-I Series | the names place the slots | `asus-p9d-user-guide-2` |
| `asus-z9-8slot` | asus | Z9PA-D8 Series, board Z9PA-D8 Series, Z9PE-D8 WS, board Z9PE-D8 WS | the names place the slots | `asus-z9pa-d8-user-guide` |
| `asus-z10-8slot` | asus | Z10PA-D8 Series, board Z10PA-D8 Series, Z10PE-D8 WS, board Z10PE-D8 WS | the names place the slots | `asus-e13692` |
| `asus-z9-16slot` | asus | ESC4000 G2 Series, board ESC4000 G2 Series, Z9PE-D16 Series, board Z9PE-D16 Series, GEG, board GEG | the names place the slots | `asus-z9pe-d16-10g-user-guide` |
| `asus-z10-16slot` | asus | Z10PE-D16 WS, board Z10PE-D16 WS, Silverback WS, board Silverback WS, GX4, board GX4, RS400-E8-PS2-F, board RS400-E8-PS2-F | the names place the slots | `asus-e13683` |
| `asus-z9pr-d12` | asus | RS700-X7-PS4, board RS700-X7-PS4 | the names place the slots | `asus-rs700-x7-ps4-user-guide` |
| `asus-z9na-d6` | asus | Z9NA-D6, board Z9NA-D6 | the names place the slots | `asus-z9na-d6-user-manual` |
| `asus-wsc621e-sage` | asus | WS-C621E-SAGE Series, board WS-C621E-SAGE Series | the names place the slots | `asus-e16279` |
| `asus-z11ph-d12` | asus | RS720Q-E9-RS24-S, board RS720Q-E9-RS24-S | the names place the slots | `asus-e16292` |
| `asus-z11pg-d16` | asus | Z11PG-D16 Series, board Z11PG-D16 Series, ESC4000 G4, board ESC4000 G4 | the names place the slots | `asus-e16560` |
| `asus-z11pg-d24` | asus | ESC8000 G4, board ESC8000 G4, ESC8000 G4-10G, board ESC8000 G4-10G | the names place the slots | `asus-e21686` |
| `asus-prows-c621-64l` | asus | Pro WS C621-64L SAGE-10G Series, board Pro WS C621-64L SAGE-10G Series | the names place the slots | `asus-e16276` |
| `asus-krpg-u8` | asus | ESC4000A-E10, board ESC4000A-E10 | the names place the slots | `asus-e21771` |
| `asus-k14pp-d24` | asus | RS720A-E12-RS12, board RS720A-E12-RS12 | the names place the slots | `asus-e28170` |
| `lenovo-thinkstation-p920` | lenovo | 30BDS0RA00, board 30BDS0RA00, 30BDS0RB00, board 30BDS0RB00, 30BDS2H804, board 30BDS2H804, 30BC001DGE, board 30BC001DGE, 30BC0048US, board 30BC0048US, 30BC000QGE, board 30BC000QGE, 30BC003KUS, board 30BC003KUS, 30BDS0N700, board 30BDS0N700 | slot number to channel and position on the processor word of the names; channels unnamed, unnamed, unnamed, unnamed, unnamed, unnamed | `lenovo-p920-p720` |
| `lenovo-thinkstation-p720` | lenovo | 30BBS1PF00, board 30BBS1PF00, 30BACTO1WW, board 30BACTO1WW, 30BBS6NG00, board 30BBS6NG00, 30BBS21C01, board 30BBS21C01 | slot number to channel and position on the processor word of the names; channels unnamed, unnamed, unnamed, unnamed, unnamed, unnamed | `lenovo-p920-p720` |
| `lenovo-thinksystem-sr590-sr570-16slot` | lenovo | `^ThinkSystem SR590 -\[7X99CTO1WW\]-\\|\|^\\|(?:.* )?ThinkSystem SR590 -\[7X99CTO1WW\]-$\|^ThinkSystem SR570 -\[7Y03CTO1WW\]-\\|\|^\\|(?:.* )?ThinkSystem SR570 -\[7Y03CTO1WW\]-$` | slot numbers that run over the processors, to processor, channel and position; channels 0.0, 0.1, 0.2, 1.0, 1.1, 1.2 | `lenovo-sr590-7x98-7x99-installation-installation` |
| `lenovo-thinksystem-sr650-sr630-24slot` | lenovo | `^ThinkSystem SR650 -\[7X06CTO1WW\]-\\|\|^\\|(?:.* )?ThinkSystem SR650 -\[7X06CTO1WW\]-$\|^ThinkSystem SR630 -\[7X02CTO1WW\]-\\|\|^\\|(?:.* )?ThinkSystem SR630 -\[7X02CTO1WW\]-$` | slot numbers that run over the processors, to processor, channel and position; channels 0.0, 0.1, 0.2, 1.0, 1.1, 1.2 | `lenovo-sr650-7x05-7x06-installation` |
| `lenovo-thinksystem-sr530-12slot` | lenovo | `^ThinkSystem SR530 -\[7X08CTO1WW\]-\\|\|^\\|(?:.* )?ThinkSystem SR530 -\[7X08CTO1WW\]-$` | slot numbers that run over the processors, to processor, channel and position; channels unnamed, unnamed, unnamed, unnamed, unnamed, unnamed, unnamed, unnamed, unnamed, unnamed, unnamed, unnamed | `lenovo-sr530-7x07-7x08-installation` |
| `lenovo-thinksystem-sr650v2-sr630v2-32slot` | lenovo | ThinkSystem SR650 V2, board ThinkSystem SR650 V2, ThinkSystem SR630 V2, board ThinkSystem SR630 V2 | slot numbers that run over the processors, to processor, channel and position; channels A, B, C, D, E, F, G, H | `lenovo-sr650-v2-installation` |
| `lenovo-thinksystem-st650v2-32slot` | lenovo | ThinkSystem ST650 V2, board ThinkSystem ST650 V2 | slot numbers that run over the processors, to processor, channel and position; channels A, B, C, D, E, F, G, H | `lenovo-st650-v2-technical` |
| `lenovo-thinksystem-sr645-32slot` | lenovo | ThinkSystem SR645, board ThinkSystem SR645 | slot numbers that run over the processors, to processor, channel and position; channels A, B, C, D, E, F, G, H | `lenovo-sr645-installation` |
| `lenovo-thinksystem-sr950v3-128slot` | lenovo | ThinkSystem SR950 V3, board ThinkSystem SR950 V3 | the names place the slots | `lenovo-sr950-v3-installation-installation` |
| `lenovo-thinksystem-sn550-24slot` | lenovo | `^ThinkSystem SN550 -\[7X16CTO1WW\]-\\|\|^\\|(?:.* )?ThinkSystem SN550 -\[7X16CTO1WW\]-$` | slot numbers that run over the processors, to processor, channel and position; channels 0.0, 0.1, 0.2, 1.0, 1.1, 1.2 | `lenovo-sn550` |
| `lenovo-systemx-m5-24slot` | lenovo | `^System x3650 M5: -\[8871AC1\]-\\|\|^\\|(?:.* )?System x3650 M5: -\[8871AC1\]-$\|^System x3650 M5: -\[5462IC1\]-\\|\|^\\|(?:.* )?System x3650 M5: -\[5462IC1\]-$\|^System x3550 M5: -\[8869AC1\]-\\|\|^\\|(?:.* )?System x3550 M5: -\[8869AC1\]-$\|^System x3550 M5: -\[5463AC1\]-\\|\|^\\|(?:.* )?System x3550 M5: -\[5463AC1\]-$` | slot numbers that run over the processors, to processor, channel and position; channels Ch0, Ch1, Ch2, Ch3 | `lenovo-x3650-m5-5462-installation-service` |
| `lenovo-flex-x240m5-24slot` | lenovo | `^Lenovo Flex System x240 M5 Compute Node -\[9532AC1\]-\\|\|^\\|(?:.* )?Lenovo Flex System x240 M5 Compute Node -\[9532AC1\]-$` | slot numbers that run over the processors, to processor, channel and position; channels A, B, C, D | `lenovo-x240-m5-9532-2951-installation` |
| `lenovo-systemx-x3250m6-4slot` | lenovo | `^LENOVO System x3250 M6 -\[3943AC1\]-\\|\|^\\|(?:.* )?LENOVO System x3250 M6 -\[3943AC1\]-$` | set aside: no-channel-in-document | `lenovo-x3250-m6-installation-service-guide` |
| `lenovo-thinksystem-sr250-4slot` | lenovo | `^ThinkSystem SR250 -\[7Y51CTO1WW\]-\\|\|^\\|(?:.* )?ThinkSystem SR250 -\[7Y51CTO1WW\]-$` | set aside: no-channel-in-document | `lenovo-sr250-installation` |
| `lenovo-thinksystem-sr250v2-4slot` | lenovo | ThinkSystem SR250 V2, board ThinkSystem SR250 V2 | set aside: no-channel-in-document | `lenovo-sr250-v2-technical` |
| `sun-x4170-x4270-gen1` | sun | SUN FIRE X4270 SERVER, board SUN FIRE X4270 SERVER | set aside: duplicated-names | `oracle-820-5830-13` |
| `sun-x4170-x4270-m2` | sun | SUN FIRE X4170 M2 SERVER, board SUN FIRE X4170 M2 SERVER, SUN FIRE X4270 M2 SERVER, board SUN FIRE X4270 M2 SERVER | slot number to channel and position on the bank locator; channels 0, 1, 2 | `oracle-e22369-02`, `oracle-e21671-04` |
| `sun-x4170-x4270-m3` | sun | SUN FIRE X4170 M3, board SUN FIRE X4170 M3, SUN FIRE X4270 M3, board SUN FIRE X4270 M3 | slot number to channel and position on the bank locator; channels A, B, C, D | `oracle-e22313-13`, `oracle-e23396-15` |
| `sun-x4-2` | sun | SUN SERVER X4-2, board SUN SERVER X4-2 | slot number to channel and position on the bank locator; channels A, B, C, D | `oracle-e38041-07` |
| `sun-x5-2` | sun | ORACLE SERVER X5-2, board ORACLE SERVER X5-2 | slot number to channel and position on the bank locator; channels A, B, C, D | `oracle-e48320-10` |
| `sun-x2270` | sun | SUN FIRE X2270, board SUN FIRE X2270 | set aside: no-channel-in-document | `oracle-820-5607-11`, `oracle-821-1345` |
| `sun-x2200-m2` | sun | Sun Fire X2200 M2, board Sun Fire X2200 M2, Sun Fire X2200 M2 with Quad Core Processor, board Sun Fire X2200 M2 with Quad Core Processor | set aside: no-channel-in-document | `oracle-819-6597-12`, `oracle-819-6601-22` |
| `sun-ultra40-m2` | sun | Sun Ultra 40 M2 Workstation, board Sun Ultra 40 M2 Workstation | set aside: no-channel-in-document | `oracle-820-0123-13` |
| `sun-ultra20-m2` | sun | Ultra20 M2, board Ultra20 M2 | set aside: no-channel-in-document | `oracle-819-6584-11` |
| `fujitsu-d2619-18slot` | fujitsu | PRIMERGY RX300 S5, board PRIMERGY RX300 S5, PRIMERGY TX300 S5, board PRIMERGY TX300 S5, PRIMERGY TX300 S6, board PRIMERGY TX300 S6 | the names place the slots | `fujitsu-d2619-tx300-s5-technical-manual`, `fujitsu-d2619-tx300-s6-technical-manual`, `fujitsu-rx300-s5-guide` |
| `fujitsu-d2709-12slot` | fujitsu | PRIMERGY TX200 S5, board PRIMERGY TX200 S5 | the names place the slots | `fujitsu-d2709-tx200-s5` |
| `fujitsu-d3031-12slot` | fujitsu | PRIMERGY RX200 S6, board PRIMERGY RX200 S6 | the names place the slots | `fujitsu-rx200-s6-guide`, `fujitsu-5600` |
| `fujitsu-d2759-6slot` | fujitsu | PRIMERGY TX150 S7, board PRIMERGY TX150 S7 | the names place the slots | `fujitsu-d2759-tx150-s7-technical-manual` |
| `fujitsu-d2863-4slot` | fujitsu | PRIMERGY RX100 S6, board PRIMERGY RX100 S6 | the names place the slots | `fujitsu-d2863-rx100-s6-technical-manual` |
| `fujitsu-d3049-4slot` | fujitsu | PRIMERGY TX140 S1p, board PRIMERGY TX140 S1p | the names place the slots | `fujitsu-d3049-tx140-s1-tx120-s3`, `fujitsu-tx140-s1-maintenance-manual` |
| `fujitsu-d3239-4slot` | fujitsu | PRIMERGY TX1320 M1, board PRIMERGY TX1320 M1, PRIMERGY TX1330 M1, board PRIMERGY TX1330 M1 | the names place the slots | `fujitsu-tx1320-m1-maintenance-manual`, `fujitsu-tx1330-m1-maintenance-manual` |
| `fujitsu-e5-2600-24slot-labelled` | fujitsu | PRIMERGY RX200 S7, board PRIMERGY RX200 S7, PRIMERGY RX200 S8, board PRIMERGY RX200 S8, PRIMERGY RX2530 M1, board PRIMERGY RX2530 M1 | the names place the slots | `fujitsu-rx200-s8-maintenance-manual`, `fujitsu-rx200-s7-maintenance-manual`, `fujitsu-rx2530-m1-maintenance-manual` |
| `fujitsu-d2939-24slot-node-names` | fujitsu | PRIMERGY RX300 S7, board PRIMERGY RX300 S7, PRIMERGY RX300 S8, board PRIMERGY RX300 S8 | slot number to channel and position on the processor word of the names, another table for the second processor; channels A, B, C, D, E, F, G, H | `fujitsu-rx300-s8-maintenance-manual` |
| `fujitsu-d3099-12slot-node-names` | fujitsu | PRIMERGY TX2540 M1, board PRIMERGY TX2540 M1 | slot number to channel and position on the processor word of the names, another table for the second processor; channels A, B, C, D, E, F | `fujitsu-tx2540-m1-maintenance-manual` |
| `fujitsu-d2119-fbdimm-8slot` | fujitsu | PRIMERGY RX300 S3, board PRIMERGY RX300 S3 | memory on the chipset, the names place the slots; 4 channels of 2 slots | `fujitsu-d2119-rx300-s3-technical-manual`, `fujitsu-rx300-s3-guide` |
| `fujitsu-d3384-24slot` | fujitsu | PRIMERGY RX2540 M4, board PRIMERGY RX2540 M4 | the names place the slots | `fujitsu-rx2540-m4-guide` |
| `fujitsu-d4129-24slot` | fujitsu | PRIMERGY RX2450 M2, board PRIMERGY RX2450 M2 | the names place the slots | `fujitsu-rx2450-m2-guide` |
| `nec-r120b-1` | nec | `^Express5800/R120b-1 \[N8100-1660F\]\\|\|^\\|(?:.* )?Express5800/R120b-1 \[N8100-1660F\]$` | slot number to channel and position on the processor word of the names; channels CH0, CH1, CH2 | `nec-express5800-r120b-1-user-guide` |
| `cisco-ucs-c220-c240-m4` | cisco | UCSC-C220-M4S, board UCSC-C220-M4S, UCSC-C240-M4L, board UCSC-C240-M4L | the names place the slots | `cisco-c220-m4-installation-service-guide`, `cisco-c220-m4`, `cisco-c240-m4-installation-service-guide`, `cisco-c240-m4` |
| `supermicro-x8dtu-12slot` | supermicro | X8DTU, board X8DTU | the names place the slots; P0 is processor 1, P1 is processor 0; with 2 processor records | `sm-mnl-1081` |
| `supermicro-x8-dp-12slot` | supermicro | X8DT6, board X8DT6, X8DT3, board X8DT3, X8DTH-i/6/iF/6F (board X8DTH), X8DTT, board X8DTT, X8DTT-H, board X8DTT-H, X8DTG-D, board X8DTG-D | the names place the slots | `sm-mnl-1062` |
| `supermicro-x8da3-12slot` | supermicro | X8DA3, board X8DA3 | the names place the slots | `sm-mnl-1031` |
| `supermicro-x8dtl-6slot` | supermicro | X8DTL, board X8DTL | the names place the slots | `sm-mnl-1076` |
| `supermicro-x8-dp-18slot` | supermicro | X8DTN+-F, board X8DTN+-F, X8DTN, board X8DTN, X8DTU-6+, board X8DTU-6+, X8DTU-LN4+, board X8DTU-LN4+ | the names place the slots | `sm-mnl-1199` |
| `supermicro-x8dah-18slot-before-pcb2` | supermicro | X8DAH, board X8DAH | the names place the slots | `sm-mnl-1060` |
| `supermicro-x8dah-18slot-pcb2` | supermicro | iX4224-846E16 (board X8DAH) | the names place the slots | `sm-mnl-1060` |
| `supermicro-h8-g34-dp-16slot` | supermicro | H8DG6/H8DGi, board H8DG6/H8DGi, H8DGU, board H8DGU, H8DGT, board H8DGT | the names place the slots | `sm-mnl-h8dg` |
| `supermicro-h8qg6-32slot` | supermicro | H8QG6, board H8QG6 | the names place the slots | `sm-mnl-h8qg-6` |
| `supermicro-h8qgl-16slot` | supermicro | H8QGL (board H8QGL-6F) | the names place the slots | `sm-mnl-h8qgl-6` |
| `supermicro-h8sgl-8slot` | supermicro | H8SGL, board H8SGL | the names place the slots | `sm-mnl-h8sgl-f` |
| `supermicro-h8scm-4slot` | supermicro | H8SCM, board H8SCM | the names place the slots | `sm-mnl-h8scm-f` |
| `supermicro-h8dcl-8slot` | supermicro | H8DCL, board H8DCL | the names place the slots | `sm-mnl-h8dcl` |
| `supermicro-x9dbl-6slot` | supermicro | X9DBL-3F/X9DBL-iF, board X9DBL-3F/X9DBL-iF | the names place the slots | `sm-mnl-1307` |
| `supermicro-x9-dp-16slot` | supermicro | `^X9DRW\\|\|^\\|(?:.* )?X9DRW$\|^X9DRD-7LN4F\\|(?:.* )?X9DRD-7LN4F$\|^X9DRD-7LN4F\(-JBOD\)/X9DRD-EF\\|(?:.* )?X9DRD-7LN4F$\|^X9DRFR\\|\|^\\|(?:.* )?X9DRFR$\|^X9DRW-7/iTPF\\|\|^\\|(?:.* )?X9DRW-7/iTPF$` | the names place the slots | `sm-mnl-1278` |
| `supermicro-x9-dp-24slot` | supermicro | X9DRi-LN4+/X9DR3-LN4+, board X9DRi-LN4+/X9DR3-LN4+, X9DRW-7TPF+, board X9DRW-7TPF+, X9DRE-TF+/X9DR7-TF+, board X9DRE-TF+/X9DR7-TF+ | the names place the slots | `sm-mnl-1258` |
| `supermicro-x9-dp-8slot` | supermicro | X9DRT, board X9DRT, HYVE-ZEUS (board X9DRD-iF), X9DRD-iF/LF (board X9DRD-iF) | the names place the slots | `sm-mnl-1488` |
| `supermicro-x9-up-8slot` | supermicro | X9SRE/X9SRE-3F/X9SRi/X9SRi-3F, board X9SRE/X9SRE-3F/X9SRi/X9SRi-3F, X9SRL-F, board X9SRL-F, X9SRH-7F/7TF, board X9SRH-7F/7TF | the names place the slots | `sm-mnl-1284` |
| `supermicro-x9sra-8slot` | supermicro | X9SRA/X9SRA-3, board X9SRA/X9SRA-3 | the names place the slots | `sm-mnl-1281` |
| `supermicro-x10-dp-16slot` | supermicro | Super Server (board X10DRW-i), SYS-6018R-WTR (board X10DRW-i), CALLEO (board X10DRW-i), SYS-6028R-WTR (board X10DRW-i), SYS-6028R-WTRT (board X10DRW-iT), SYS-1028TP-DC1R (board X10DRT-P), SYS-6028TP-DNCR (board X10DRT-P), Rackform_R4422.v6 (board X10DRT-P), Super Server (board X10DRX-CS045) | the names place the slots | `sm-mnl-1557` |
| `supermicro-x10dri-x10drh-16slot` | supermicro | X10DRi, board X10DRi, X10DRi (board X10DRI-T), X10DRH (board X10DRH-i), X10DRH (board X10DRH-iT), SSG-2028R-E1CR24L (board X10DRH-iT), SSG-6048R-E1CR24H (board X10DRH-iT) | the names place the slots | `sm-mnl-1491` |
| `supermicro-x10-dp-24slot` | supermicro | Super Server (board X10DRU-i+), transtec CALLEO (board X10DRC-LN4+), Super Server (board X10DRC-LN4+), CALLEO (board X10DRi-LN4+) | the names place the slots | `sm-mnl-1560` |
| `supermicro-x10drg-24slot` | supermicro | SYS-4028GR-TR (board X10DRG-O+-CPU), SYS-4028GR-TR2 (board X10DRG-O+-CPU) | the names place the slots | `sm-mnl-1780` |
| `supermicro-x10-dp-8slot` | supermicro | Super Server (board X10DRL-i), Rackform R309.v5 (board X10DRL-i), Super Server (board X10DRL-CT), Super Server (board X10DAL-i) | the names place the slots | `sm-mnl-1566` |
| `supermicro-x10-up-8slot` | supermicro | Super Server (board X10SRi-F), Super Server (board X10SRL-F), Super Server (board X10SRH-CF) | the names place the slots | `sm-mnl-1586` |
| `supermicro-x10sra-8slot` | supermicro | X10SRA, board X10SRA, X10SRA-F, board X10SRA-F | the names place the slots | `sm-mnl-1643` |
| `supermicro-x10srm-4slot` | supermicro | Super Server (board X10SRM-TF), Super Server (board X10SRM-F) | the names place the slots | `sm-mnl-1845` |
| `supermicro-x10sll-x10slm-4slot` | supermicro | X10SLL-F, board X10SLL-F, X10SLM-F, board X10SLM-F, X10SLL-HF-CKD, board X10SLL-HF-CKD | the names place the slots | `sm-mnl-1428` |
| `supermicro-x10sl7-4slot` | supermicro | X10SL7-F, board X10SL7-F | the names place the slots | `sm-mnl-1463` |
| `supermicro-x10slh-x10slmplus-4slot` | supermicro | X10SLH-F/X10SLM+-F, board X10SLH-F/X10SLM+-F, X10SLM+-LN4F, board X10SLM+-LN4F | the names place the slots | `sm-mnl-1464` |
| `supermicro-x10sae-4slot` | supermicro | X10SAE, board X10SAE | the names place the slots | `sm-mnl-1479` |
| `supermicro-x10sdv-4slot` | supermicro | Super Server (board X10SDV-4C-TLN2F), Super Server (board X10SDV-2C-TLN2F), Super Server (board X10SDV-TLN4F), Super Server (board X10SDV-8C-TLN4F), Super Server (board X10SDV-8C-TLN4F+), Super Server (board X10SDV-8C+-LN2F), SYS-5018D-FN8T (board X10SDV-TP8F), Super Server (board X10SDV-TP8F), Super Server (board X10SDV-2C-TP4F), Super Server (board X10SDV-2C-7TP4F) | the names place the slots | `sm-mnl-1726` |
| `supermicro-x11-lga1151-4slot-b2-first` | supermicro | Super Server (board X11SSL-F), 410041 (board X11SSL-F), Super Server (board X11SSL-CF), Super Server (board X11SSM-F), Super Server (board X11SSH-F), Super Server (board X11SSH-LN4F), Super Server (board X11SSH-CTF), Super Server (board X11SCL-F), SYS-5039MC-H12TRF (board X11SCE-F), Super Server (board X11SCH-F), Super Server (board X11SSA-F), Super Server (board X11SSZ-TLN4F), Super Server (board X11SAE-M) | the names place the slots | `sm-mnl-1778` |
| `supermicro-x11-lga1151-4slot-a2-first` | supermicro | PCplus T600-C246 (board X11SCA), Super Server (board X11SCA-F), Super Server (board X11SCA-W), Super Server (board X11SAE), Super Server (board X11SAE-F), Super Server (board X11SCZ-Q) | the names place the slots | `sm-mnl-2087` |
| `supermicro-x11scl-if-2slot` | supermicro | Super Server (board X11SCL-IF) | the names place the slots | `sm-mnl-2088` |
| `supermicro-x11ssv-q-2slot` | supermicro | X11SSV-Q, board X11SSV-Q | the names place the slots | `sm-mnl-1795` |
| `supermicro-x11-dp-16slot` | supermicro | `^X11DPi-N\\|(?:.* )?X11DPi-NT$\|^X11DPi-N\(T\)\\|(?:.* )?X11DPi-NT$\|^X11DPi-N\\|(?:.* )?X11DPi-N$\|^X11DPi-N\(T\)\\|(?:.* )?X11DPi-N$\|^SYS-6029P-TR\\|(?:.* )?X11DPi-N$\|^SYS-7049P-TR\\|(?:.* )?X11DPi-N$\|^SYS-7049GP-TRT\\|(?:.* )?X11DPG-QT$\|^Super Server\\|(?:.* )?X11DPG-QT$\|^Super Server\\|(?:.* )?X11DPH-T$\|^Joyent-S10G5\\|(?:.* )?X11DPH-T$\|^SSG-6049P-E1CR36L\\|(?:.* )?X11DPH-T$\|^Super Server\\|(?:.* )?X11DPH-i$\|^SYS-7039A-I\\|(?:.* )?X11DAi-N$\|^X11DAi-N\\|\|^\\|(?:.* )?X11DAi-N$` | the names place the slots | `sm-mnl-1773` |
| `supermicro-x11-dp-24slot` | supermicro | Super Server (board X11DPU), SYS-1029U-TR25M (board X11DPU), SYS-2029U-TN24R4T (board X11DPU), SYS-6019U-TN4R4T (board X11DPU), SYS-1029U-TRT (board X11DPU), SYS-1029U-TR25M-BK-LC019 (board X11DPU-LC019), SYS-2029BT-HNR-NS2-WW001 (board X11DPT-B), X11DPG-OT (board X11DPG-OT-CPU), SYS-4029GP-TRT2 (board X11DPG-OT-CPU), SYS-4029GP-TRT (board X11DPG-OT-CPU) | the names place the slots | `sm-x11-user-guide` |
| `supermicro-x11ddw-12slot` | supermicro | SYS-1029P-WTR (board X11DDW-L), SYS-6029P-WTR (board X11DDW-L), SYS-6019P-WTR (board X11DDW-L), SYS-6019P-WT (board X11DDW-L), SYS-6029P-WTRT (board X11DDW-NT), iPC-12-210 (board X11DDW-NT), Super Server (board X11DDW-NT) | the names place the slots | `sm-mnl-1907` |
| `supermicro-x11dpl-8slot` | supermicro | Super Server (board X11DPL-i) | the names place the slots | `sm-mnl-1946` |
| `supermicro-x11qph-48slot` | supermicro | SYS-2049U-TR4 (board X11QPH+) | the names place the slots | `sm-mnl-1983` |
| `supermicro-x11-up-8slot` | supermicro | Super Server (board X11SPi-TF), Super Server (board X11SPL-F), SSG-5029P-E1CTR12L (board X11SPH-nCTF) | the names place the slots | `sm-mnl-1900` |
| `supermicro-x11-up-6slot` | supermicro | Super Server (board X11SPM-F), X11-SPM-TF (board X11SPM-TF), egino BTO Konfig Server (board X11SPM-TF), SYS-5019P-WTR (board X11SPW-TF), SYS-5029P-WTR (board X11SPW-TF), SYS-1019P-WTR (board X11SPW-TF) | the names place the slots | `sm-mnl-1939` |
| `supermicro-b11spe-12slot` | supermicro | SBI-6119P-T3N (board B11SPE-CPU-TF) | the names place the slots | `sm-mnl-2135` |
| `supermicro-x11sra-8slot` | supermicro | Super Server (board X11SRA-RF) | the names place the slots | `sm-mnl-2005` |
| `supermicro-x11sdv-4slot` | supermicro | Super Server (board X11SDV-8C-TLN2F), Super Server (board X11SDV-4C-TP8F), SYS-E300-9D-8CN8TP (board X11SDV-8C-TP8F), Prime Server Pro (board X11SDV-8C-TP8F) | the names place the slots | `sm-mnl-2019` |
| `supermicro-a2sd-4slot` | supermicro | Super Server (board A2SDi-4C-HLN4F), Super Server (board A2SDi-8C-HLN4F), Super Server (board A2SDV-4C-LN8F) | the names place the slots | `sm-mnl-1951` |
| `supermicro-a1sa2-4slot` | supermicro | A1SA2-2750F, board A1SA2-2750F | the names place the slots | `sm-mnl-1600` |
| `supermicro-a1sam-a1srm-4slot` | supermicro | A1SAM-2550F, board A1SAM-2550F, A1SRM-2558F, board A1SRM-2558F | the names place the slots | `sm-mnl-1561` |
| `supermicro-x12dpi-18slot` | supermicro | Super Server (board X12DPi-N6) | the names place the slots | `sm-mnl-2233` |
| `supermicro-x12-dp-16slot` | supermicro | X12DAi-N6, board X12DAi-N6, SYS-620C-TN12R (board X12DDW-A6) | the names place the slots | `sm-mnl-2239` |
| `supermicro-x12dpu-32slot` | supermicro | SYS-620U-TNR (board X12DPU-6) | the names place the slots | `sm-mnl-2234` |
| `supermicro-x12-up-8slot` | supermicro | Super Server (board X12SPL-F), SYS-510P-MR (board X12SPO-F) | the names place the slots | `sm-mnl-2270` |
| `supermicro-x12sae-4slot` | supermicro | Super Server (board X12SAE-5) | the names place the slots | `sm-mnl-2358` |
| `supermicro-x12sdv-sp6f-4slot` | supermicro | Super Server (board X12SDV-8C-SP6F) | the names place the slots | `sm-mnl-2362` |
| `supermicro-x12sdv-spt8f-4slot` | supermicro | Super Server (board X12SDV-16C-SPT8F) | the names place the slots | `sm-mnl-2440` |
| `supermicro-x13-dp-16slot` | supermicro | Flagman KvX229.7 (board X13DEI), Super Server (board X13DEI), SYS-751A-I (board X13DAI-T) | the names place the slots | `sm-mnl-2509` |
| `supermicro-x13sew-8slot` | supermicro | SYS-511E-WR (board X13SEW-F) | the names place the slots | `sm-mnl-2413` |
| `supermicro-x13sae-4slot` | supermicro | Super Server (board X13SAE-F) | the names place the slots | `sm-mnl-2425` |
| `supermicro-x13san-2slot` | supermicro | Super Server (board X13SAN-L) | the names place the slots | `sm-mnl-2517` |
| `supermicro-h11dsi-16slot` | supermicro | Super Server (board H11DSi), Super Server (board H11DSi-NT) | the names place the slots | `sm-mnl-2027` |
| `supermicro-h12dsi-16slot` | supermicro | Super Server (board H12DSi-N6), Super Server (board H12DSi-NT6) | the names place the slots | `sm-mnl-2363` |
| `supermicro-h11ssl-8slot` | supermicro | Super Server (board H11SSL-i), Super Server (board H11SSL-C), GN#16917 (board H11SSL-NC), Super Server (board H11SSL-NC) | the names place the slots | `sm-mnl-2085` |
| `supermicro-h12-up-8slot` | supermicro | Super Server (board H12SSL-i), Supermicro motherboard-H12 Series (board H12SSL-i), Super Server (board H12SSL-NT), Super Server (board H12SSL-CT), AS -1014S-WTRT (board H12SSW-NT), 520012 (board H12SSW-NT), Super Server (board H12SSW-NT) | the names place the slots | `sm-mnl-2314` |
| `supermicro-h12dsg-32slot` | supermicro | AS -4124GS-TNR (board H12DSG-O-CPU) | the names place the slots | `sm-mnl-2299` |
| `supermicro-h13sae-mf-4slot` | supermicro | Super Server (board H13SAE-MF) | the names place the slots | `sm-mnl-2627` |
| `supermicro-m11sdv-4slot` | supermicro | Super Server (board M11SDV-8C-LN4F), Super Server (board M11SDV-8CT-LN4F) | the names place the slots | `sm-mnl-2172` |
| `supermicro-m12swa-8slot` | supermicro | AS -5014A-TT (board M12SWA-TF), M12SWA-TF, board M12SWA-TF, Super Server (board M12SWA-TF) | the names place the slots | `sm-mnl-2336` |
| `supermicro-c7x99-oce-8slot` | supermicro | C7X99-OCE, board C7X99-OCE | the names place the slots | `sm-mnl-1646` |
| `supermicro-c9x299-pg300f-8slot` | supermicro | C9X299-PG300F, board C9X299-PG300F | the names place the slots | `sm-mnl-2117` |
| `supermicro-c7-c9-lga1151-4slot` | supermicro | C7Z170-M, board C7Z170-M, C7Z270-CG-L, board C7Z270-CG-L, C7Z270-CG-M, board C7Z270-CG-M, C7Z270-PG, board C7Z270-PG, C9Z390-PGW, board C9Z390-PGW | the names place the slots | `sm-mnl-1813` |
| `supermicro-c7-c9-lga1151-2slot` | supermicro | C7Z370-CG-IW, board C7Z370-CG-IW, C9Z390-CG-IW, board C9Z390-CG-IW | the names place the slots | `sm-mnl-2018` |
| `gigabyte-epyc-sp3-2s-16ch-2dpc` | gigabyte | R182-Z90-00 (board MZ92-FS0-00), R182-Z91-00 (board MZ92-FS0-00), G482-Z54-00 (board MZ52-G41-00) | the names place the slots | `gigabyte-r182-z90-r182-z91-r182` |
| `gigabyte-epyc-sp3-1s-8ch-2dpc` | gigabyte | MZ31-AR0-00, board MZ31-AR0-00, MZ32-AR0-00, board MZ32-AR0-00, R162-ZA0-00 (board MZA2-CE0-00), R262-ZA2-00 (board MZA2-CE0-00) | the names place the slots | `gigabyte-mz32-ar0-user-manual`, `gigabyte-mz31-ar0-user-manual`, `gigabyte-r162-za0-user-manual`, `gigabyte-r262-za1-za2-user-manual` |
| `gigabyte-ampere-mp32-8ch-2dpc` | gigabyte | MP32-AR1-SW-HZ-001 (board MP32-AR1-00), E252-P30-00 (board MP32-AR1-00), MP32-AR1-00, board MP32-AR1-00, MP32-AR0, board MP32-AR0 | the names place the slots | `gigabyte-e252-p30-user-manual` |
| `gigabyte-mx33-xeon-e2300-4slot` | gigabyte | MX33-BS0-00, board MX33-BS0-00, MX33-BS1-V1, board MX33-BS1-V1 | the names place the slots | `gigabyte-mx33-bs0-user-manual` |
| `gigabyte-epyc-sp3-2s-16ch-1dpc` | gigabyte | MZ71-CE0-00, board MZ71-CE0-00, MZ71-CE1-00, board MZ71-CE1-00, MZ72-HB0-00, board MZ72-HB0-00 | the names place the slots | `gigabyte-mz71-ce0-mz71-ce1-user`, `gigabyte-mz72-hb0-user-manual` |
| `gigabyte-epyc3000-mj11-4slot` | gigabyte | G431-MM0-OT (board MJ11-EC1-OT) | the names place the slots | `gigabyte-g431-mm0-user-manual` |
| `gigabyte-am4-am5-mc1x-4slot` | gigabyte | MC13-LE0-000, board MC13-LE0-000, MC13-LE1-000, board MC13-LE1-000, MC12-LE0-00, board MC12-LE0-00 | the names place the slots | `gigabyte-mc13-le0-mc13-le1-user` |
| `gigabyte-mu71-su0-xeon-w3200-8slot` | gigabyte | MU71-SU0-00, board MU71-SU0-00 | the names place the slots | `gigabyte-mu71-su0-user-manual` |
| `gigabyte-c621-su8-8slot` | gigabyte | C621-SU8 M18818, board C621-SU8 M18818 | the names place the slots | `gigabyte-c621-su8-user-manual` |
| `gigabyte-c621-sd8-8slot` | gigabyte | C621-SD8 M18802, board C621-SD8 M18802 | the names place the slots | `gigabyte-c621-sd8-user-manual` |
| `gigabyte-mw50-sv0-e5-1s-8slot` | gigabyte | MW50-SV0, board MW50-SV0, OEM-F7342-00 (board MW50-SV0) | the names place the slots | `gigabyte-mw50-sv0-user-manual` |
| `gigabyte-mg51-g21-xeon-sp-2s-24slot` | gigabyte | G291-280-00 (board MG51-G21-00), G291-281-00 (board MG51-G21-00) | the names place the slots | `gigabyte-g291-280-g291-281-user` |
| `gigabyte-e5-2s-4ch-3dpc-24slot` | gigabyte | R280-G2O-XX (board MD90-FS0-XX), G250-G52-Sample-Dalco-002-XX (board MG50-G21-XX) | the names place the slots | `gigabyte-r280-f2o-r280-g2o-user` |
| `gigabyte-mu70-su0-e5-1s-12slot` | gigabyte | MU70-SU0-NV-XX, board MU70-SU0-NV-XX | the names place the slots | `gigabyte-mu70-su0-user-manual` |
| `gigabyte-xeon-sp-2s-6ch-uneven-16slot` | gigabyte | Relion XE2142 (board MH61-HD3-ZB) | the names place the slots | `gigabyte-h261-h60-user-manual` |
| `gigabyte-md72-hb0-xeon-sp3-2s-16slot` | gigabyte | SS4000HD (board MD72-HB0-00) | the names place the slots | `gigabyte-md72-hb0-md72-hb1-user` |
| `gigabyte-mu72-su0-xeon-w3300-8slot` | gigabyte | MU72-SU0-00, board MU72-SU0-00 | the names place the slots | `gigabyte-mu72-su0-user-manual` |
| `gigabyte-6pxsv-e5-1s-8slot` | gigabyte | GA-6PXSV1, board GA-6PXSV1 | the names place the slots | `gigabyte-6pxsvl-user-manual` |
| `gigabyte-mz12-hd3-epyc-1s-8slot` | gigabyte | G242-Z11-00 (board MZ12-HD3-00) | the names place the slots | `gigabyte-g242-z10-g242-z11-user` |
| `gigabyte-mc62-g40-trpro-8slot` | gigabyte | MC62-G40-00, board MC62-G40-00 | the names place the slots | `gigabyte-mc62-g40-user-manual` |
| `asrock-am5-b650d4u-4slot` | asrock | B650D4U-2L2T/BCM, board B650D4U-2L2T/BCM, board B650D4U, B650D4U | the names place the slots | `asrock-b650d4u-b650d4u-2l2t-b650d4u-2l2t` |
| `asrock-am4-x570-4slot` | asrock | X570D4U, board X570D4U, X570D4U-2L2T, board X570D4U-2L2T, X570D4U-2L2T/BCM (board X570D4U-2L2T), X570D4I-2T, board X570D4I-2T | the names place the slots | `asrock-x570d4u-x570d4u-2l2t-x570d4u-2l2t` |
| `asrock-romed8-2t-8slot` | asrock | ROMED8-2T, board ROMED8-2T, board ROMED8-2T/OVH | the names place the slots | `asrock-romed8-2t-romed8-romed8-2t` |
| `asrock-rome2d16-2t-16slot` | asrock | ROME2D16-2T, board ROME2D16-2T | the names place the slots | `asrock-rome2d16-2t-rome2d16-rome2d16-2l` |
| `asrock-genoad12m3-12slot` | asrock | GENOAD12M3-2Q/H, board GENOAD12M3-2Q/H | the names place the slots | `asrock-genoad12m3-2q-user-manual-genoad12m3` |
| `asrock-intel-ddr5-controller-4slot` | asrock | board W680D4U-1L, Z690D4U-2L2T/G5, board Z690D4U-2L2T/G5, EC266D4-4L, board EC266D4-4L | the names place the slots | `asrock-z690d4u-2l2t-w680d4u-2l2t-w680d4u` |
| `asrock-sp2c621d32lm3-32slot` | asrock | R1G3D32 (board SP2C621D32LM3) | the names place the slots | `asrock-sp2c621d32lm3-user-manual` |
| `asrock-altrad8ud-8slot` | asrock | ALTRAD8UD-1L2T, board ALTRAD8UD-1L2T | the names place the slots | `asrock-altrad8ud-1l2t-user-manual` |
| `msi-am5-d3051-d3052` | msi | MSIS336 MS-S336, board MSIS336 MS-S336, MSIS366, board MSIS366 | the names place the slots | `msi-d3052-s3661-user-guide` |
| `tyan-s7012` | tyan | board S7012 | the names place the slots | `tyan-s7012-user-manual` |
| `tyan-s7010` | tyan | board S7010 | the names place the slots | `tyan-s7010-user-manual` |
| `tyan-s8026` | tyan | board S8026GM2NRE, board S8026GM2NRE-HOV-B | the names place the slots | `tyan-s8026-user-manual` |
| `tyan-s8036` | tyan | B8036T65AV4E24HR-HOV-1 (board B8036GM2NE) | the names place the slots | `tyan-s8036-user-manual` |
| `tyan-s7109` | tyan | B7109F77DV14HR-2T-N (board S7109GM2NR-2T) | the names place the slots | `tyan-s7106-user-manual` |
| `tyan-s8812` | tyan | board S8812 | set aside: names-not-in-document | `tyan-s8812-user-manual` |
| `tyan-s4882` | tyan | S4882 (board S4882) | slot number to channel and position on the processor word of the names; channels high64, low64 | `tyan-k8qs-s4882-user-manual` |
| `huawei-rh-v3-16slot` | huawei | RH1288 V3, board RH1288 V3, RH2288 V3, board RH2288 V3 | the names place the slots | `huawei-rh1288-v3-user-guide`, `huawei-rh2288-v3-user-guide` |
| `huawei-rh2288hv3-24slot` | huawei | RH2288H V3, board RH2288H V3 | the names place the slots | `huawei-rh2288h-v3-user-guide` |
| `huawei-v5-24slot` | huawei | 1288H V5 (board BC11SPSC0), 1288H V5 (board BC11SPSCC0), 2288H V5, board 2288H V5 | the names place the slots | `huawei-1288h-v5-user-guide`, `huawei-2288h-v5-user-guide` |
| `lenovo-thinkserver-rd330` | lenovo | Lenovo ThinkServer RD330, board Lenovo ThinkServer RD330 | the names place the slots | `lenovo-rd330-maintenance-manual` |
| `lenovo-thinkserver-rd450` | lenovo | ThinkServer RD450, board ThinkServer RD450 | the names place the slots | `lenovo-rd450-user-guide-maintenance-manual` |
| `lenovo-thinkstation-px` | lenovo | 30EUA0EG00, board 30EUA0EG00 | slot number to channel and position on the processor word of the names, another table for the second processor; channels 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16 | `lenovo-memory-configurator-lenovo`, `lenovo-thinkstation-px-user-guide` |
| `fujitsu-d3521-tx1310m3` | fujitsu | PRIMERGY TX1310 M3, board PRIMERGY TX1310 M3 | the names place the slots | `fujitsu-tx1310-m3-guide` |
| `fujitsu-mx130s2-sample` | fujitsu | PRIMERGY MX130 S2, board PRIMERGY MX130 S2 | the names place the slots | `fujitsu-mx130-s2-maintenance-manual` |
| `inspur-nf5280m6-32slot` | inspur | NF5280M6 | the names place the slots | `inspur-nf5280m-lm6-user-manual` |
| `inspur-sa5212m4-16slot` | inspur | SA5212M4 (board Inspur Shuyu) | the names place the slots | `inspur-sa5212m4-sa5212m4-user-manual` |
| `quanta-t22hf-s5hf-16slot` | quanta | QuantaPlex T22HF-1U, board QuantaPlex T22HF-1U | the names place the slots | `quanta-t22hf-1u-service-guide` |
| `huawei-taishan200-2280-kunpeng920` | huawei | `^TaiShan 200\\|\|^\\|(?:.* )?TaiShan 200$\|^TaiShan 200 \(Model 2280\)\\|\|^\\|(?:.* )?TaiShan 200 \(Model 2280\)$\|^TaiShan 2280 V2\\|\|^\\|(?:.* )?TaiShan 2280 V2$` | the names place the slots | `huawei-200-maintenance-service-guide-2280` |
| `huawei-atlas500pro-3000` | huawei | `^Atlas 500 Pro\\|\|^\\|(?:.* )?Atlas 500 Pro$\|^Atlas 500 Pro \(Model 3000\)\\|\|^\\|(?:.* )?Atlas 500 Pro \(Model 3000\)$` | the names place the slots | `huawei-500-maintenance-service-guide-3000` |
| `quanta-ocp-leopard-16slot` | quanta | Leopard ORv2-DDR4, board Leopard ORv2-DDR4 | the names place the slots | `quanta-v3-1` |
| `quanta-ocp-windmill-winterfell-16slot` | quanta | Freedom (board Quanta Winterfell) | slot number to channel and position on the bank locator, another table for the second processor; channels 0, 1, 2, 3 | `quanta-v2-0` |
| `lenovo-thinkserver-rd450x` | lenovo | RD450X, board RD450X | the names place the slots | `lenovo-rd450-user-guide-maintenance-manual` |
| `hp-dl360-gen10plus-32slot` | hp | ProLiant DL360 Gen10 Plus, board ProLiant DL360 Gen10 Plus | slot number to channel and position on the processor word of the names; channels CH0, CH1, CH2, CH3, CH4, CH5, CH6, CH7 | `hpe-population-gen10-3rd-technical`, `hpe-dl360-gen10-maintenance-service-guide-2` |
| `hp-dl385-gen10plus-32slot` | hp | ProLiant DL385 Gen10 Plus, board ProLiant DL385 Gen10 Plus, ProLiant DL385 Gen10 Plus v2, board ProLiant DL385 Gen10 Plus v2 | slot number to channel and position on the processor word of the names; channels A, B, C, D, E, F, G, H | `hpe-population-gen10-gen10-7002-7003`, `hpe-dl385-gen10-maintenance-service-guide`, `hpe-dl385-gen10-v2-maintenance-service` |
| `hp-dl380-gen11-32slot` | hp | ProLiant DL380 Gen11, board ProLiant DL380 Gen11 | slot number to channel and position on the processor word of the names; channels pair1_2, pair3_4, pair5_6, pair7_8, pair9_10, pair11_12, pair13_14, pair15_16 | `hpe-population-gen11-5th-technical`, `hpe-population-gen11-4th`, `hpe-dl380-gen11`, `hpe-dl380-gen11-maintenance-service-guide` |
| `hp-dl160-gen10-16slot` | hp | ProLiant DL160 Gen10, board ProLiant DL160 Gen10 | slot number to channel and position on the processor word of the names; channels CH1, CH2, CH3, CH4, CH5, CH6 | `hpe-dl160-gen10`, `hpe-876833-006-2` |
| `hp-dl20-gen10plus-4slot` | hp | ProLiant DL20 Gen10 Plus, board ProLiant DL20 Gen10 Plus | slot number to channel and position on the processor word of the names; channels CH1, CH2 | `hpe-population-gen10-2300-technical` |
| `hp-cray-xd665-24slot` | hp | HPE Cray SC XD665 (board HPE Cray XD665 MB PCA) | the names place the slots | `hpe-xd665-maintenance-service-guide`, `hpe-xd665` |
| `gigabyte-mr92-fs1-32slot` | * | `^R182-N20-UNI\\|(?:NULL )?MR92-FS1-UNI$` | the names place the slots | `gigabyte-r182-n20-r182-na0-3rd` |
| `huawei-2288hv5-bc11spscb-unbranded` | * | 2288H V5 (board -- BC11SPSCB0), FPD-8-SP-H2K288G5-CTO (board -- BC11SPSCB10) | the names place the slots | `huawei-2288h-v5-user-guide` |
| `huawei-rh2288hv3-bc11hgsa-hve` | * | 3DGFX (board HVE BC11HGSA0) | the names place the slots | `huawei-rh2288h-v3-user-guide-2` |
| `asrock-sp2c621d32tm3-32slot-inferit` | * | RS224 R1G3D32RU (board INF SP2C621D32TM3) | the names place the slots | `asrock-sp2c621d32tm3-user-manual` |
| `aic-libra-16slot` | * | SB302-LB (board AIC LIBRA) | set aside: names-not-in-document | `other-user-manual` |
| `geekom-intel-h-2sodimm` | * | `^(?:GT13 Pro\|IT12\|IT13\|XT12 Pro\|XT13 Pro)\\|(?:GT13 Pro\|IT12\|IT13\|XT12 Pro\|XT13 Pro)$` | the names place the slots | `other-12th-1` |
| `neousys-nuvo-9000-10000-2sodimm` | * | Nuvo-9000 Series (board NVS-9000), Nuvo-10000 Series (board NVS-10000) | the names place the slots | `other-9000-user-manual` |
| `geekom-miniair11-jsl-2sodimm` | * | MiniAir 11 (board GEEKOM MiniAir 11) | the names place the slots | `other-11` |
| `neousys-nuvo-2600-1sodimm` | * | Nuvo-2600 Series | the names place the slots | `other-2600-user-manual` |
| `geekom-miniair12-n100` | * | Mini Air12 | the names place the slots | `other-1` |
| `ekf-sc9-toccata-tgl` | * | CompactPCI Serial (board EKF Elektronik GmbH SC9-TOCCATA) | the names place the slots | `other-631121-012` |
| `adlink-aadp-com-hpc-alt-6slot` | * | Ampere Altra Developer Platform (board ADLINK Ampere Altra Developer Platform) | slot number to channel and position on the processor word of the names; channels MCU0, MCU1, MCU2, MCU4, MCU5, MCU6 | `other-user-guide` |
| `besstar-atb15-2sodimm` | * | `^(?:X300\|X400\|X500)\\|BESSTAR TECH ATB15$` | the names place the slots | `other-x300-x400-x500-user-manual` |
| `gigabyte-rmb-mr91-openyard` | * | RS201I (board OpenYard RMB-MR91) | the names place the slots | `gigabyte-r182-n20-r182-na0-user` |
| `powerleader-psc621di-16f` | * | PR2710P-24 (board OEM PSC621DI-16F) | the names place the slots | `other-rs5230-user-manual-pr2710p-manual` |
| `quanta-d52b-1u-nokia-airframe` | * | AR-S5008A-5A/AF1294.05 | the names place the slots | `quanta-d52b` |
| `graviton-onega-16slot` | * | Server Graviton (board Graviton ONEGA) | the names place the slots | `other-2041-2122` |
| `yadro-vegman-rx20g2-32slot` | * | ; 9k (board w 9k w 9k) | the names place the slots | `other-rx20-g2` |
| `etegro-rs125g4-quanta-s2ms-16slot` | * | Hyperion RS125 G4 (board ETegro Technologies ETRS125G4) | the names place the slots | `other-rs125-g4` |
| `etegro-rs130g4-quanta-s2rq-24slot` | * | Hyperion RS130 G4 (board ETegro Technologies ETRS130G4) | the names place the slots | `other-rs130-g4` |
<!-- /families -->

The documents the rows cite (385 documents, `internal/explorer/sources.tsv`), with the pages read. Some were read from a public copy on web.archive.org or manualslib where the maker no longer serves them; ten were read from copies whose address was not recorded and are cited by title, number and date.

<!-- documents -->
| Document | Maker | Title | Number or revision | Date | Pages | Address |
| --- | --- | --- | --- | --- | --- | --- |
| `dell-e26s` | dell | Dell PowerEdge R630 Owner's Manual | Regulatory Model E26S Series, Rev. A08 | June 2022 | pp. 159-164 | <https://dl.dell.com/topicspdf/poweredge-r630_owners-manual_en-us.pdf> |
| `dell-e14s` | dell | Dell PowerEdge R720 and R720xd Owner's Manual | Regulatory Model E14S Series, Rev. A07 | 2018-10 | pp. 40-46 | <https://dl.dell.com/topicspdf/poweredge-r720_owners-manual_en-us.pdf> |
| `dell-e18s` | dell | Dell PowerEdge R420 Systems Owner's Manual | Regulatory Model E18S Series, Rev. A04 | 2013-11 | pp. 43-47 | <https://dl.dell.com/topicspdf/poweredge-r420_owners-manual_en-us.pdf> |
| `dell-e20s` | dell | Dell PowerEdge T320 Systems Owner's Manual | Regulatory Model E20S Series, Rev. A05 | 2017-08 | pp. 67-70 | <https://dl.dell.com/topicspdf/poweredge-t320_owners-manual_en-us.pdf> |
| `dell-e28s` | dell | Dell PowerEdge R430 Owner's Manual | Regulatory Model E28S Series, Rev. A07 | November 2020 | pp. 80-87 | <https://dl.dell.com/topicspdf/poweredge-r430_owners-manual_en-us.pdf> |
| `dell-r710-owner-manual` | dell | Dell PowerEdge R710 Systems Hardware Owner's Manual | Rev. A03 | September 2009 | pp. 129-133, 181-182 | <https://dl.dell.com/Manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-r710_owner's%20manual_en-us.pdf> |
| `dell-r610-owner-manual` | dell | Dell PowerEdge R610 Systems Hardware Owner's Manual | Rev. A04 | 2013-03 | pp. 116-119, 166 | <https://dl.dell.com/manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-r610_owner's%20manual2_en-us.pdf> |
| `dell-r410-owner-manual` | dell | Dell PowerEdge R410 Systems Hardware Owner's Manual | Rev. A00 | April 2009 | pp. 119-123, 171 | <https://dl.dell.com/Manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-r410_owner's%20manual_en-us.pdf> |
| `dell-t310-owner-manual` | dell | Dell PowerEdge T310 Systems Hardware Owner's Manual | Rev. A00 | July 2009 | pp. 108-110, 172 | <https://dl.dell.com/manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-t310_owner's%20manual_en-us.pdf> |
| `dell-r210-owner-manual` | dell | Dell PowerEdge R210 Systems Hardware Owner's Manual | Rev. A00 | August 2009 | pdf pp. 27, 41 (the file has no printed page numbers); pp. 27, 41 | <https://dl.dell.com/manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-r210_owner's%20manual_en-us.pdf> |
| `dell-e10s` | dell | Dell PowerEdge R210 II Systems Owner's Manual | Regulatory Model E10S Series, Rev. A01 | 2013-09 | pp. 69-70, 117 | <https://dl.dell.com/manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-r210-2_owner's%20manual_en-us.pdf> |
| `dell-e10s-2` | dell | Dell PowerEdge R220 Owner's Manual | Regulatory Model E10S, Rev. A02 | 2015-05 | pp. 47-49 | <https://dl.dell.com/topicspdf/poweredge-r220_owners-manual_en-us.pdf> |
| `dell-e33s` | dell | Dell PowerEdge R230 Owner's Manual | Regulatory Model E33S Series, Rev. A05 | 2019-10 | pp. 56-58 | <https://dl.dell.com/topicspdf/poweredge-r230_owners-manual_en-us.pdf> |
| `dell-e57s` | dell | Dell EMC PowerEdge R240 Installation and Service Manual | Regulatory Model E57S Series, Rev. A09 | August 2025 | pp. 40-42 | <https://dl.dell.com/topicspdf/poweredge-r240_owners-manual_en-us.pdf> |
| `dell-e80s` | dell | Dell EMC PowerEdge T150 Installation and Service Manual | Regulatory Model E80S series, Rev. A03 | June 2025 | pp. 39-41 | <https://dl.dell.com/topicspdf/poweredge-t150_owners-manual_en-us.pdf> |
| `dell-e38s` | dell | Dell EMC PowerEdge R740 Installation and Service Manual | Regulatory Model E38S Series, Rev. A20 | Sept. 2021 | pp. 61-71 | <https://dl.dell.com/topicspdf/poweredge-r740_owners-manual_en-us.pdf> |
| `dell-e45s` | dell | Dell EMC PowerEdge R440 Installation and Service Manual | Regulatory Model E45S Series, Rev. A17 | December 2021 | pp. 46-51 | <https://dl.dell.com/topicspdf/poweredge-r440_owners-manual9_en-us.pdf> |
| `dell-e38s-2` | dell | Dell EMC PowerEdge R7425 Installation and Service Manual | Regulatory Model E38S Series, Rev. A20 | March 2021 | pp. 101-105 | <https://dl.dell.com/topicspdf/poweredge-r7425_owners-manual_en-us.pdf> |
| `dell-e45s-2` | dell | Dell PowerEdge R6515 Installation and Service Manual | Regulatory Model E45S Series, Rev. A14 | July 2023 | pp. 47-52 | <https://dl.dell.com/topicspdf/poweredge-r6515_owners-manual_en-us.pdf> |
| `dell-e68s` | dell | Dell PowerEdge R7525 Installation and Service Manual | Regulatory Model E68S, Rev. A20 | July 2024 | pp. 72-77 | <https://dl.dell.com/topicspdf/poweredge-r7525_owners-manual_en-us.pdf> |
| `dell-e70s` | dell | Dell PowerEdge R750 Installation and Service Manual | Regulatory Model E70S, Rev. A11 | December 2024 | pp. 98-102 | <https://dl.dell.com/topicspdf/poweredge-r750_owners-manual_en-us.pdf> |
| `dell-e74s` | dell | Dell PowerEdge R450 Installation and Service Manual | Part Number E74S, Rev. A03 | July 2026 | pp. 51-52 | <https://dl.dell.com/topicspdf/poweredge-r450_owners-manual_en-us.pdf> |
| `dell-e94s` | dell | Dell PowerEdge R7625 Installation and Service Manual | Regulatory Model E94S, Rev. A09 | July 2026 | pp. 34-35, 173-176 | <https://dl.dell.com/content/manual25024882-dell-poweredge-r7625-installation-and-service-manual.pdf?language=en-us> |
| `dell-e97s` | dell | Dell PowerEdge R6615 Installation and Service Manual | Regulatory Model E97S, Rev. A09 | July 2026 | pp. 125-128 | <https://dl.dell.com/content/manual24843968-dell-poweredge-r6615-installation-and-service-manual.pdf?language=en-us> |
| `dell-e87s` | dell | Dell PowerEdge R660xs Installation and Service Manual | Part Number E87S, Rev. A07 | September 2026 | pp. 103-105 | <https://dl.dell.com/content/manual42812694-dell-poweredge-r660xs-installation-and-service-manual.pdf?language=en-us> |
| `dell-xr4510c-installation-service-manual` | dell | Dell PowerEdge XR4510c Installation and Service Manual | Rev. A02 | October 2024 | pp. 59-60 | <https://dl.dell.com/content/manual20158191-dell-poweredge-xr4510c-installation-and-service-manual.pdf?language=en-us> |
| `dell-d04x` | dell | Dell Precision 7920 Tower Owner's Manual | Regulatory Model D04X, Rev. A14 | October 2026 | pp. 89-90, 99-100, 102-104 | <https://dl.dell.com/topicspdf/precision-7920-workstation_Owners-Manual_en-us.pdf> |
| `dell-d02t` | dell | Dell Precision 7820 Tower Owner's Manual | Regulatory Model D02T, Rev. A09 | August 2023 | pp. 91-93, 101-102, 104-105 | <https://dl.dell.com/content/manual26341430-dell-precision-7820-tower-owner-s-manual.pdf?language=en-us> |
| `dell-e31s` | dell | Dell Precision Rack 7910 Owner's Manual | Regulatory Model E31S, Rev. A06 | September 2023 | pp. 19-22 | <https://dl.dell.com/topicspdf/precision-r7910-workstation_Owners-Manual_en-us.pdf> |
| `dell-e38s-3` | dell | Dell Precision 7920 Rack Owner's Manual | Regulatory Model E38S, Rev. A02 | November 2020 | pp. 26-27, 102-104 | <https://web.archive.org/web/20210715152400/https://dl.dell.com/topicspdf/precision-7920r-workstation_owners-manual_en-us.pdf> |
| `dell-e82s` | dell | Precision 7960 Rack Setup and Specifications | Regulatory Model E82S, Rev. A07 | August 2026 | pp. 12-15 | <https://dl.dell.com/content/manual42601925-precision-7960-rack-setup-and-specifications.pdf?language=en-us> |
| `dell-e53s` | dell | Dell EMC PowerEdge C4140 Installation and Service Manual | Regulatory Model E53S Series, Rev. A14 | August 2022 | pp. 41-44 | <https://dl.dell.com/topicspdf/poweredge-c4140_owners-manual_en-us.pdf> |
| `dell-e64s` | dell | Dell EMC PowerEdge C6520 Installation and Service Manual | Regulatory Model E64S Series, Rev. A03 | August 2022 | pp. 56-58 | <https://dl.dell.com/topicspdf/poweredge-c6520_owners-manual_en-us.pdf> |
| `dell-e63s` | dell | Dell PowerEdge C6525 Installation and Service Manual | Regulatory Model E63S Series, Rev. A15 | May 2024 | pp. 24-29 | <https://dl.dell.com/topicspdf/poweredge-c6525_owners-manual_en-us.pdf> |
| `dell-c6220-owner-manual` | dell | Dell PowerEdge C6220 II Systems Hardware Owner's Manual | Rev. A01 | October 2013 | pp. 230-232 | <https://dl.dell.com/manuals/all-products/esuprt_ser_stor_net/esuprt_cloud_products/poweredge-c6220-2_Owner's%20Manual_en-us.pdf> |
| `dell-b08s` | dell | Dell PowerEdge C6320 Owner's Manual | Regulatory Model B08S, Rev. A04 | 2017-08 | pp. 104-105, board connector table (txt lines 6148-6173) | <https://dl.dell.com/topicspdf/poweredge-c6320_owners-manual_en-us.pdf> |
| `dell-b04s` | dell | Dell PowerEdge C5220 Systems Hardware Owner's Manual | Regulatory Model B04S, Rev. A02 | 2012-06 | pp. 64-66 | <https://dl.dell.com/manuals/all-products/esuprt_ser_stor_net/esuprt_cloud_products/poweredge-c5220_owner's%20manual_en-us.pdf> |
| `dell-c6100-owner-manual` | dell | Dell PowerEdge C6100 Systems Hardware Owner's Manual | Regulatory Model XS23-TY3, Rev. A01 | January 2010 | pp. 77-79, 129; pp. 77-79 | <https://dl.dell.com/manuals/all-products/esuprt_ser_stor_net/esuprt_cloud_products/poweredge-c6100_owner's%20manual_en-us.pdf> |
| `dell-c1100-owner-manual` | dell | Dell PowerEdge C1100 Systems Hardware Owner's Manual | Regulatory Model CS24-TY, Rev. A01 | 2013-02 | pp. 63-66 | <https://dl.dell.com/manuals/all-products/esuprt_ser_stor_net/esuprt_cloud_products/poweredge-c1100_owner's%20manual_en-us.pdf> |
| `dell-1950-owner-manual` | dell | Dell PowerEdge 1950 Systems Hardware Owner's Manual | P/N HH176, Rev. A02 | September 2009 | pp. 26, 63-65 | <https://dl.dell.com/Manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-1950_owner's%20manual_en-us.pdf> |
| `dell-2900-owner-manual` | dell | Dell PowerEdge 2900 Systems Hardware Owner's Manual | December 2005, no revision printed | December 2005 | pp. 27, 82-85 | <https://dl.dell.com/Manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-2900_owner's%20manual_en-us.pdf> |
| `dell-sc1430-owner-manual` | dell | Dell PowerEdge SC1430 Systems Hardware Owner's Manual | P/N WH819, Rev. A00 | June 2006 | pp. 19, 76-77, 120-121 | <https://dl.dell.com/manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-1430sc_owner's%20manual_en-us.pdf> |
| `dell-r5400-service-manual` | dell | Dell Precision R5400 Service Manual | Model WMTE01, Rev. A00 | June 2008 | pdf p. 25 ("System Memory") | <https://dl.dell.com/manuals/all-products/esuprt_desktop/esuprtl_precision_workstation/precision-r5400_service%20manual_en-us.pdf> |
| `dell-t300-owner-manual` | dell | Dell PowerEdge T300 Systems Hardware Owner's Manual | P/N HR675, Rev. A01 | June 2009 | pp. 111-112, 169 | <https://dl.dell.com/Manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-t300_owner's%20manual_en-us.pdf> |
| `dell-860-owner-manual` | dell | Dell PowerEdge 860 Systems Hardware Owner's Manual | P/N GJ055, Rev. A00 | June 2006 | pp. 67-68 | <https://dl.dell.com/Manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-860_owner's%20manual_en-us.pdf> |
| `dell-2850-installation-guide` | dell | Dell PowerEdge 2850 Systems Installation and Troubleshooting Guide | June 2004, no revision printed | June 2004 | section "System Memory" (txt lines 3595-3700) | <https://dl.dell.com/manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-2850_user's%20guide11_en-us.pdf> |
| `dell-2970-owner-manual` | dell | Dell PowerEdge 2970 Systems Hardware Owner's Manual | February 2007, no revision printed | February 2007 | pp. 91-93, 142 | <https://dl.dell.com/Manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-2970_owner's%20manual_en-us.pdf> |
| `dell-r805-owner-manual` | dell | Dell PowerEdge R805 Systems Hardware Owner's Manual | P/N TT864, Rev. A01 | June 2009 | "System Memory" (txt lines 3090-3206), Table 6-2 (txt lines 4056-4122) | <https://dl.dell.com/Manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-r805_owner's%20manual_en-us.pdf> |
| `dell-r900-owner-manual` | dell | Dell PowerEdge R900 Systems Hardware Owner's Manual | P/N XK946, Rev. A00 | September 2007 | pp. 92-95, 98-99; pp. 92-95, 98 | <https://dl.dell.com/Manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-r900_owner's%20manual_en-us.pdf> |
| `dell-e06s` | dell | Dell PowerEdge R910 Hardware Owner's Manual | Regulatory Model E06S Series, Rev. A02 | October 2012 | pp. 46-51, 91-98, 200 | <https://dl.dell.com/Manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-r910_owner's%20manual_en-us.pdf> |
| `dell-e05s` | dell | Dell PowerEdge R810 Hardware Owner's Manual | Regulatory Model E05S, Rev. A00 | February 2010 | pp. 100-104, 187-188 | <https://dl.dell.com/Manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-r810_owner's%20manual_en-us.pdf> |
| `dell-e05s-2` | dell | Dell PowerEdge R815 Hardware Owner's Manual | Regulatory Model E05S, Rev. A01 | December 2013 | pp. 92-96, 177-178 | <https://dl.dell.com/Manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-r815_owner's%20manual_en-us.pdf> |
| `dell-r515-owner-manual` | dell | Dell PowerEdge R515 Systems Hardware Owner's Manual | Rev. A00 | August 2010 | pp. 104-107, 183 | <https://dl.dell.com/Manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-r515_owner's%20manual_en-us.pdf> |
| `dell-e21s` | dell | Dell PowerEdge R820 Owner's Manual | Regulatory Model E21S Series, Rev. A05 | 2014-03 | pp. 43-51 | <https://dl.dell.com/topicspdf/poweredge-r820_owners-manual_en-us.pdf> |
| `hpe-767927-009` | hp | HPE ProLiant DL360 Gen9 Server User Guide | 767927-009, edition 9, February 2018 | February 2018 | 15, 41-48 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c04441974> |
| `hpe-768830-401a` | hp | HPE ProLiant DL380 Gen9 Server User Guide | 768830-401a, edition 10, April 2018 | April 2018 | 22, 68-79 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c04436966> |
| `hpe-dl380p-gen8-user-guide` | hp | HPE ProLiant DL380p Gen8 Server User Guide | 661848-006R, edition 7, December 2015 | December 2015 | 16, 45-51 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c03231416> |
| `hpe-664724-006` | hp | HP ProLiant DL360p Gen8 Server User Guide | 664724-006, edition 6, February 2014 | February 2014 | 13, 42-49 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c03231393> |
| `hpe-ddr3-gen8` | hp | Configuring and using DDR3 memory with HP ProLiant Gen8 Servers (engineering white paper) | 2nd edition | 2012 | 11-14; 11-13 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c03293145> |
| `hpe-675786-006` | hp | HPE ProLiant DL360e Gen8 Server User Guide | 675786-006, edition 8, October 2019 | October 2019 | 10, 45-51 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c03401317> |
| `hpe-676151-006` | hp | HPE ProLiant DL380e Gen8 Server User Guide | 676151-006, edition 7, October 2019 | October 2019 | 14, 59-64 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c03413110> |
| `hpe-population-gen10-technical` | hp | Server Memory and Persistent Memory population rules for HPE Gen10 servers with Intel Xeon Scalable processors (technical white paper) | a00017079ENW, Rev. 16, copyright 2025 | 2025 | 4-13, 19, 23; 4-7, 14, 19; 4-8, 17, 21, 26 | <https://www.hpe.com/psnow/doc/a00017079enw> |
| `hpe-dl380-gen10-user-guide` | hp | HPE ProLiant DL380 Gen10 Server User Guide | 30-EFA1AAC3-401h, edition 20 | October 2024 | 30, 141-142 | <https://support.hpe.com/hpesc/public/docDisplay?docId=a00019109en_us> |
| `hpe-dl360-gen10-maintenance-service-guide` | hp | HPE ProLiant DL360 Gen10 Server Maintenance and Service Guide | docId a00115362en_us |  | topics "DIMM slot locations", "DIMM-processor compatibility" | <https://support.hpe.com/hpesc/public/docDisplay?docId=a00115362en_us> |
| `hpe-ml350-gen10-user-guide` | hp | HPE ProLiant ML350 Gen10 Server User Guide | 30-3266984C-008a, edition 8a | January 2024 | topics "System board components", "Memory options" (no DIMM slot topic) | <https://support.hpe.com/hpesc/public/docDisplay?docId=a00016366en_us> |
| `hpe-870691-007` | hp | HPE ProLiant ML350 Gen10 Server User Guide | 870691-007, edition 7, October 2020 | October 2020 | 15-17, 120-121 (no DIMM slot drawing either) | <https://itpfdoc.hitachi.co.jp/manuals/ha8000v/hard/Gen10/Server/ML350/870691-007_en.pdf> |
| `hpe-656396-004` | hp | HP ProLiant BL460c Gen8 Server Blade User Guide | 656396-004, edition 4, December 2012 | December 2012 | 9, 23, 27-33 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c03428245> |
| `hpe-656396-002` | hp | HP ProLiant BL460c Gen8 Server Blade User Guide | 656396-002, edition 2, August 2012 | August 2012 | 9, 27-33 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c03231200> |
| `hpe-768826-007` | hp | HPE ProLiant BL460c Gen9 Server Blade User Guide | 768826-007, edition 7, September 2018 | September 2018 | 12-13, 43-48 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c04410344> |
| `hpe-876833-006` | hp | HPE ProLiant BL460c Gen10 Server Blade User Guide | 876833-006, edition 6, May 2019 | May 2019 | 15, 60 | <https://support.hpe.com/hpesc/public/docDisplay?docId=a00018554en_us> |
| `hpe-872677-004` | hp | HPE ProLiant MicroServer Gen10 User Guide | 872677-004, edition 4, November 2018 | November 2018 | 9-11, 48 | <https://www.laptopsdirect.co.uk/PDFs/P07203-421_UserManual1.PDF> |
| `hpe-gen10-maintenance-service-guide` | hp | HPE ProLiant MicroServer Gen10 Maintenance and Service Guide | 30-9F0C0938-005, edition 5, January 2025 | January 2025 | topic "DIMM slot locations" | <https://support.hpe.com/hpesc/public/docDisplay?docId=sd00005613en_us> |
| `hpe-gen10` | hp | HPE ProLiant MicroServer Gen10 QuickSpecs | a00008701enw, version 10, 09-November-2020 (retired) | 2020 | 3, 10 | <https://support.hpe.com/hpesc/public/api/document/a00008701enw> |
| `hpe-833836-073` | hp | HPE ProLiant ML10 Gen9 Server User Guide, Spanish edition (Guía de usuario del servidor HPE ProLiant ML10 Gen9) | 833836-073, February 2017 | February 2017 | 35-38 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c04960478> |
| `hpe-833835-004` | hp | HPE ProLiant ML10 Gen9 Server Maintenance and Service Guide | 833835-004, edition 4, August 2018 | August 2018 | 67 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c04960486> |
| `hpe-ml10-generation9-gen9` | hp | HPE ProLiant ML10 Generation9 (Gen9) QuickSpecs | c04922941, version 8, 11-July-2017 (retired) | 2017 | 6, 17-18 | <https://support.hpe.com/hpesc/public/api/document/c04922941> |
| `hpe-786245-003` | hp | HPE ProLiant DL60 Gen9 Server User Guide | 786245-003, edition 3, April 2016 | April 2016 | 11, 49-54 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c04504816> |
| `hpe-826318-003a` | hp | HPE ProLiant DL20 Gen9 Server User Guide | 826318-003a, edition 4, June 2017 | June 2017 | 14, 65-69 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c04898522> |
| `hpe-670519-006` | hp | HP ProLiant DL160 Gen8 Server User Guide | 670519-006, edition 6, March 2013 | March 2013 | 10, 46-51 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c03242768> |
| `hpe-670554-007` | hp | HP ProLiant DL160 Gen8 Server Maintenance and Service Guide | 670554-007, edition 7, March 2013 | March 2013 | 74-75 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c03257139> |
| `hpe-dl385p-gen8-user-guide` | hp | HPE ProLiant DL385p Gen8 Server User Guide | 661850-002R, edition 3, December 2015 | December 2015 | 16, 51-57 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c03351259> |
| `hpe-775786-004` | hp | HPE ProLiant DL160 Gen9 Server User Guide | 775786-004, edition 4, March 2016 | March 2016 | 12, 52-58 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c04430543> |
| `hpe-775439-004` | hp | HPE ProLiant DL180 Gen9 Server User Guide | 775439-004, edition 5, March 2016 | March 2016 | 16, 82-87 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c04422844> |
| `hpe-781878-003` | hp | HPE ProLiant DL120 Gen9 Server User Guide | 781878-003, edition 3, April 2016 | April 2016 | 12, 57-62 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c04504822> |
| `hpe-495624-003` | hp | HP ProLiant DL380 G6 Server User Guide | part number 495624-003, edition 3, February 2011 | February 2011 | pp. 16, 41-47 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c01705681> |
| `hpe-517689-005` | hp | HPE ProLiant DL360 G6 Server User Guide | part number 517689-005, edition 5, October 2018 | October 2018 | pp. 12, 38-44 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c01705733> |
| `hpe-406316-002` | hp | HP ProLiant DL360 Generation 5 Server User Guide | part number 406316-002, second edition, May 2006 | May 2006 | pp. 11, 37-40 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c00710435> |
| `hpe-setup-z6-g4-z8-g4` | hp | Intel Optane DC Persistent Memory: Configuration and Setup on HP Z6 G4 and Z8 G4 Workstations, technical white paper | 4AA7-6622ENW | November 2019 | pp. 6-7, 21-23 | <https://web.archive.org/web/20210724215723/https://h20195.www2.hp.com/v2/getpdf.aspx/4AA7-6622ENW.pdf> |
| `hpe-530514-002` | hp | HP ProLiant DL320 G6 Server User Guide | part number 530514-002, second edition, March 2010 | March 2010 | pp. 10-11, 26-31 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c01736500> |
| `hpe-dl160-6-g6` | hp | QuickSpecs HP ProLiant DL160 Generation 6 (G6) | DA-13247, version 59 | September 24, 2012 | pp. 36-38 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c04284374> |
| `hpe-dl180-6-g6` | hp | QuickSpecs HP ProLiant DL180 Generation 6 (G6) | DA-13248, version 53 | September 24, 2012 | pp. 38-40 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c04284378> |
| `hpe-501274-002` | hp | HP ProLiant BL460c G6 Server Blade User Guide | part number 501274-002, edition 2, March 2011 | March 2011 | pp. 10, 27-33 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c01708947> |
| `hpe-579574-004` | hp | HP ProLiant DL120 G6 Server User Guide | part number 579574-004, edition 4, April 2012 | April 2012 | pp. 9, 23-25 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c01934840> |
| `hpe-638025-003a` | hp | HP ProLiant DL120 G7 Server User Guide | part number 638025-003a, edition 3, April 2014 | April 2014 | pp. 12-13, 28-30 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c02790682> |
| `hpe-588708-001` | hp | HP ProLiant DL385 G7 Server User Guide | part number 588708-001, first edition, March 2010 | March 2010 | pp. 12-13, 15, 47-50 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c02118792> |
| `hpe-617692-004` | hp | HP ProLiant DL585 G7 Server User Guide | part number 617692-004, edition 4, October 2012 | October 2012 | pp. 17-18, 43-46 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c02244320> |
| `hpe-593961-001` | hp | HP ProLiant BL685c G7 Server Blade User Guide | part number 593961-001, first edition, June 2010 | June 2010 | pp. 10, 28-31 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c02252425> |
| `hpe-601465-003` | hp | HP ProLiant DL165 G7 Server Maintenance and Service Guide | part number 601465-003, third edition, March 2013 | March 2013 | pp. 63-64, 95 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c02074307> |
| `hpe-430047-004` | hp | HP ProLiant DL145 Generation 3 Server Maintenance and Service Guide | part number 430047-004, fourth edition, April 2007 | April 2007 | pp. 33, 38-39, 63-64, 70 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c00856243> |
| `hpe-595656-005` | hp | HP ProLiant DL580 G7 Server User Guide | part number 595656-005, edition 5, July 2012 | July 2012 | pp. 17-18, 43-51; pp. 44-47 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c02267159> |
| `hpe-381737-004` | hp | HP ProLiant DL140 Generation 2 Server Maintenance and Service Guide | part number 381737-004, fourth edition, September 2006 | September 2006 | pp. 31, 58, 65 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c00368751> |
| `hpe-359226-008` | hp | HP ProLiant DL380 Generation 4 Server Maintenance and Service Guide | part number 359226-008, eighth edition, May 2006 | May 2006 | pp. 38, 80-81, 95 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c00687567> |
| `hpe-136363-004` | hp | ProLiant DL380 Maintenance and Service Guide (Compaq) | part number 136363-004, fourth edition, June 2000 | June 2000 | pp. 45-46, 142 (printed 2-29, 2-30, 5-4) | <https://support.hpe.com/hpesc/public/docDisplay?docId=c01129673> |
| `hpe-hpe-integrity-superdome` | hp | HPE Integrity Superdome X QuickSpecs | c04383189, version 20, 17 June 2019 (marked retired) | 17 June 2019 | pp. 8, 15-16, 23 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c04383189> |
| `hpe-4-4r-user-guide` | hp | HP NetServer LH 4/LH 4r User Guide | no part number on the title pages; copyright 1998, PDF created December 1999 | 1998 | pp. 29, 31-32 (printed 23, 25-26) | <https://www.manualslib.com/manual/439638/Hp-D7171a-Netserver-Lpr.html> |
| `ibm-x3550-m3-4254-7944-installation` | ibm | Lenovo System x3550 M3 (Types 4254, 7944) documentation pages "Installing a memory module", "DIMM installation sequence", "Memory mirroring", "Online-spare memory"; the same text in "System x3550 M3 Installation and User's Guide" | web pages carry no document number, fetched 2026-11-17; the guide is Part Number 00KC032, Thirteenth Edition (September 2023) | 2026-11-17 | guide pdf p61-67 (printed 51-57) | <https://pubs.lenovo.com/x3550-m3/c_dimm_rules> |
| `ibm-x3550-m2-4198-7946-installation` | ibm | IBM System x3550 M2 Types 4198 and 7946: Installation and User's Guide | Part Number 00D9279, Thirteenth Edition ( | July 2012 | pdf p68-72 (printed 52-56) | <https://public.dhe.ibm.com/systems/support/system_x_pdf/00d9279.pdf> |
| `ibm-x3400-m3-7378-7379-installation` | ibm | IBM System x3400 M3 Types 7378 and 7379: Installation and User's Guide | Part Number 00D3189, Ninth Edition ( | February 2012 | pdf p95-104 (printed 79-88) | <https://public.dhe.ibm.com/systems/support/system_x_pdf/00d3189.pdf> |
| `ibm-x3550-m4-7914-installation-x3550` | ibm | Lenovo System x3550 M4 (Type 7914) documentation pages "Installing a memory module", "DIMM installation sequence", "Memory mirrored channel", "Memory rank sparing"; the same text in "System x3550 M4 Installation and Service Guide" | web pages carry no document number, fetched 2026-11-17; the guide is the Twentieth Edition (November 2015), no part number printed | 2026-11-17 | guide pdf p59-64 (printed 45-50) | <https://pubs.lenovo.com/x3550-m4/c_dimm_rules> |
| `ibm-x3300-m4-7382` | ibm | Lenovo System x3300 M4 (Type 7382) documentation page "Installing a memory module" | web page carries no document number, fetched 2026-11-17; the Installation and Service Guide PDF is 62 MB at public.dhe.ibm.com and 73 MB at pubs.lenovo.com, over the 60 MB limit, not fetched | 2026-11-17 | whole page | <https://pubs.lenovo.com/x3300-m4/installing_memory> |
| `ibm-x220-installation-service-guide-7906` | ibm | Flex System x220 Compute Node Installation and Service Guide (Machine Types 7906, 2585) and the web pages "Installing a DIMM", "Independent-channel mode", "Rank-sparing mode", "Mirrored-channel mode" with the same text | Part Number 00YJ297, Tenth Edition ( | August 2016 | guide pdf p590-595 (printed 582-587) | <https://pubs.lenovo.com/x220/dw1kr_book.pdf> |
| `ibm-x240-7863-8737-8738-8956` | ibm | IBM Flex System x240 Compute Node Types 7863, 8737, 8738, and 8956: Installation and Service Guide | Part Number 00FH226, Seventh Edition | March 2017 | pdf p603-607 (printed 591-595) | <https://pubs.lenovo.com/x240/dw1ko_book_ibm.pdf> |
| `ibm-hs23-installation-user-guide-7875` | ibm | BladeCenter HS23 Blade Server Installation and User's Guide (Types 7875 and 1929) and the web page "Installing a memory module" with the same text | Part Number 00AK736, Fifth Edition ( | June 2017 | pdf p24 and p32-34 (printed 14 and 22-24) | <https://pubs.lenovo.com/bladecenter-hs23/cy1ec_hs23_iug.pdf> |
| `ibm-dx360-m4-7912-7913-user` | ibm | System x iDataPlex dx360 M4 Types 7912 and 7913: User's Guide | Part Number 00V9812, Second Edition ( | July 2012 | pdf p55-59 (printed 39-43) | <https://pubs.lenovo.com/idataplex/ch1inmst.pdf> |
| `ibm-x3250-m3-4251-4252-4261` | ibm | IBM System x3250 M3 Types 4251, 4252, and 4261: Installation and User's Guide | Part Number 81Y6118, Sixth Edition ( | August 2011 | pdf p46-50 (printed 30-34) | <https://public.dhe.ibm.com/systems/support/system_x_pdf/81y6118.pdf> |
| `ibm-x3250-m4-installation-service-guide` | ibm | System x3250 M4 Installation and Service Guide (Machine Type 2583) and the web pages "Memory module installation", "Unbuffered DIMMs (UDIMMs)", "Installing a DIMM" with the same text | Fourteenth Edition (November 2015), no part number printed | November 2015 | guide pdf p59-61 (printed 45-47) | <https://public.dhe.ibm.com/systems/support/system_x_pdf/x3250_m4_2583_isg_en.pdf> |
| `ibm-x3200-m2-4367-4368-installation` | ibm | System x3200 M2 Types 4367 and 4368: Installation Guide | Part Number 46M2038, Third Edition (September 2009) | 2038 | pdf p29-31 (printed 15-17) | <https://public.dhe.ibm.com/systems/support/system_x_pdf/46m2038.pdf> |
| `ibm-x3650-7979-user-guide` | ibm | System x3650 Type 7979: User's Guide | Part Number 44R5189, Fifth Edition ( | December 2007 | pdf p83-87 (printed 67-71) | <https://public.dhe.ibm.com/systems/support/system_x_pdf/44r5189.pdf> |
| `ibm-235-8671-installation-guide` | ibm | IBM xSeries 235 Type 8671: Option Installation Guide | Part Number 59P6537 (SC59-P653-70), Third Edition ( | January 2003 | pdf p15 and p54-55 (printed 3 and 42-43) | <https://public.dhe.ibm.com/systems/support/system_x_pdf/59p6537.pdf> |
| `ibm-x3850-x5-x3950-x5-7145` | ibm | IBM System x3850 X5 and x3950 X5 Types 7145, 7146, 7143, and 7191: Installation and User's Guide | Part Number 00FH255, Fourteenth Edition ( | October 2013 | pdf p48 and p88-93 (printed 34 and 74-79) | <https://pubs.lenovo.com/x3850-x6-6241/7145_iug_pdf.pdf> |
| `intel-s5000psl-s5000xsl-technical` | intel | Intel Server Boards S5000PSL and S5000XSL Technical Product Specification | D41763-008, Revision 1.7, August 2010 | August 2010 | p29-35; pp. 29-35 | <https://web.archive.org/web/20161130150538/https://www.intel.com/content/dam/support/us/en/documents/motherboards/server/s5000psl/sb/417435_d41763_008_s5000psl_tps_rev_17.pdf> |
| `intel-s5000vsa-technical` | intel | Intel Server Board S5000VSA Technical Product Specification | D36978-010, Revision 1.8, June 2010 | June 2010 | p20-22, p61 | <https://web.archive.org/web/20200120041744/https://www.intel.com/content/dam/support/us/en/documents/motherboards/server/s5000vsa/sb/d36978010_s5000vsa_tps_r1_9.pdf> |
| `intel-s5000vcl-technical` | intel | Intel Server Board S5000VCL Technical Product Specification | D64569-007, Revision 2.3, cover date May 2007 | May 2007 | p19-21 | <https://web.archive.org/web/20230201073947/https://www.intel.com/content/dam/support/us/en/documents/motherboards/server/s5000vcl/sb/s5000vcl_tps_r2_3.pdf> |
| `intel-e14960-009` | intel | Intel Server Boards S3200SH/S3210SH Technical Product Specification | E14960-009, Revision 1.8, May 2010 | May 2010 | p30-31, p38-40, p65 | <https://web.archive.org/web/20220316225735/https://www.intel.com/content/dam/support/us/en/documents/motherboards/server/s3200sh/sb/e14960009_s3210sh_tps_r1_8.pdf> |
| `intel-se7501wv2-technical` | intel | Intel Server Board SE7501WV2 Technical Product Specification | C25653-001, Revision 1.0, December 2002 | December 2002 | p24-26, p72-73, p90 | <https://web.archive.org/web/20081203072305/http://download.intel.com/support/motherboards/server/se7501wv2/tps.pdf> |
| `intel-e39529-013` | intel | Intel Server Boards S5520HC, S5500HCV, and S5520HCT Technical Product Specification | E39529-013, Revision 1.9, June 2011 | June 2011 | p41-43, p48-51; p41, p43, p49-51 | <https://web.archive.org/web/20160705103142/https://www.intel.com/content/dam/support/us/en/documents/motherboards/server/s5520hc/sb/e39529013_s5520hc_s5500hcv_s5520hct_tps_r19.pdf> |
| `intel-e44031-012` | intel | Intel Server Board S5520UR and S5520URT Technical Product Specification | E44031-012, Revision 1.9, May 2011 | May 2011 | p39-44 | <https://web.archive.org/web/20201112042143/https://www.intel.com/content/dam/support/us/en/documents/motherboards/server/s5520ur/sb/e44031012_s5520ur_s5520urt_tps_r1_9.pdf> |
| `intel-e42249-009` | intel | Intel Server Board S5500BC Technical Product Specification | E42249-009, Revision 1.8, June 2011 | June 2011 | p40-43 | <https://web.archive.org/web/20160910011329/https://www.intel.com/content/dam/support/us/en/documents/motherboards/server/s5500bc/sb/e42249009_s5500bc_tps_1_9.pdf> |
| `intel-e65697-010` | intel | Intel Server Board S3420GP Technical Product Specification | E65697-010, Revision 2.4, January 2011 | January 2011 | p33-36 | <https://web.archive.org/web/20220414191407/https://www.intel.com/content/dam/support/us/en/documents/motherboards/server/s3420gp/sb/s3420gp_tps_r2_4.pdf> |
| `intel-s1200bt-technical` | intel | Intel Server Board S1200BT Technical Product Specification | G13326-007, Revision 2.4, May 2013 | May 2013 | p30-34 | <https://web.archive.org/web/20220317015358/https://www.intel.com/content/dam/support/us/en/documents/motherboards/server/sb/g13326007_s1200bt_tps_r2_4.pdf> |
| `intel-s1200v3rp-technical` | intel | Intel Server Board S1200V3RP Technical Product Specification | G84364-004, Revision 2.0, June 2015 | June 2015 | p29-33 | <https://web.archive.org/web/20220126191535/https://www.intel.com/content/dam/support/us/en/documents/motherboards/server/sb/g84364004_s1200v3rp_tps_r2_0.pdf> |
| `intel-s1200sp-technical` | intel | Intel Server Board S1200SP Family Technical Product Specification | Revision 1.4, March 2017 (no order number printed) | March 2017 | p32-36 | <https://web.archive.org/web/20240807064334/https://www.intel.com/content/dam/support/us/en/documents/server-products/S1200SP_TPS_R1_4_AllSKU.pdf> |
| `intel-s2600gz-technical` | intel | Intel Server Board S2600GZ/GL Technical Product Specification | Revision 2.4, October 2014 (no order number on the cover) | October 2014 | p35-39 | <https://web.archive.org/web/20230127062822/https://www.intel.com/content/dam/support/us/en/documents/motherboards/server/sb/s2600gzgl_tps_r2_4.pdf> |
| `intel-s2600cp-p4000cp-technical` | intel | Intel Server Board S2600CP Family, Intel Server System P4000CP Family Technical Product Specification | G26942-005, Revision 1.9, March 2015 | March 2015 | p49-53 | <https://web.archive.org/web/20160327150014/https://www.intel.com/content/dam/support/us/en/documents/motherboards/server/s2600cp/sb/g26942005_s2600cp_p4000cp_tps_rev19.pdf> |
| `intel-s4600lh2-t2-technical` | intel | Intel Server Boards S4600LH2/T2 Technical Product Specification | Revision 2.0, March 2014 (no order number on the cover) | March 2014 | p37-40 | <https://web.archive.org/web/20240118024553/https://www.intel.com/content/dam/support/us/en/documents/motherboards/server/sb/s4600lh2_t2_tps_rev_2_02.pdf> |
| `intel-s2600wt-technical-tps` | intel | Intel Server Board S2600WT Technical Product Specification (TPS) | Revision 1.9 | February 2020 | p38-41, p44 | <https://web.archive.org/web/20220317050938/https://www.intel.com/content/dam/support/us/en/documents/server-products/server-boards/Intel_Server_Board_S2600WT_TPS.pdf> |
| `intel-s2600cw-technical` | intel | Intel Server Board S2600CW Family Technical Product Specification | Revision 2.61 | March 2020 | p38-40, p43 | <https://web.archive.org/web/20220205233726/https://www.intel.com/content/dam/support/us/en/documents/server-products/server-boards/S2600CW_TPS.pdf> |
| `intel-s2600wf-technical` | intel | Intel Server Board S2600WF Product Family Technical Product Specification | Rev 2.6 | October 2021 | p54-57, p137 | <https://web.archive.org/web/20250118081233/https://www.intel.com/content/dam/support/us/en/documents/server-products/server-boards/S2600WF_TPS.pdf> |
| `intel-s2600st-technical` | intel | Intel Server Board S2600ST Product Family Technical Product Specification | Rev 2.4 | March 2020 | p47-50 | <https://web.archive.org/web/20210921180702/https://www.intel.com/content/dam/support/us/en/documents/server-products/Intel_S2600ST_TPS.pdf> |
| `intel-m50cyp2sb-technical` | intel | Intel Server Board M50CYP2SB Technical Product Specification | Rev. 1.41 | August 2022 | p40, p45-48, p122; p46, p48 | <https://web.archive.org/web/20221024041852/https://www.intel.com/content/dam/support/us/en/documents/server-products/single-node-servers/m50cyp2sb-server-board-tps.pdf> |
| `intel-m70klp-technical` | intel | Intel Server System M70KLP Family Technical Product Specification | Rev. 1.0 | April 2021 | p34, p38-40 | <https://web.archive.org/web/20220326215456/https://www.intel.com/content/dam/support/us/en/documents/server-products/M70KLP_TPS.pdf> |
| `intel-m50fcp2sbstd-technical` | intel | Intel Server Board M50FCP2SBSTD Technical Product Specification | Rev. 1.5 | June 2024 | p38, p40-41 | <https://web.archive.org/web/20240711223041/https://www.intel.com/content/dam/support/us/en/documents/server-products/intel-server-board-m50fcp2sbstd-tps.pdf> |
| `intel-no-document-of` | intel | no document of the board itself; coreboot src/mainboard/intel/avenuecity_crb/dimm_slot.c | commit 9f21b10ec29fe946fc1abc75a76c74105eae7611 |  | line 11 | <https://github.com/coreboot/coreboot/tree/main/src/mainboard/intel/avenuecity_crb> |
| `amd-population-7003` | amd | Memory Population Guidelines for AMD EPYC 7003 Series Processors | Publication 56873, Revision 1.00 | April 2023 | p6-20 | <https://docs.amd.com/api/khub/documents/0bglRXRnwmLeDr6L0vSZVA/content> |
| `amd-9004-population` | amd | AMD EPYC 9004 Series Memory Population Recommendations | Publication 58269, Revision 1.1 | June 2023 | p5-12 | <https://docs.amd.com/api/khub/documents/zynTogiENLgLxWEd4075vQ/content> |
| `asus-rs100-e7-pi2-user-guide` | asus | RS100-E7/PI2 user guide (motherboard ASUS P8B-M) | E6489, First Edition | March 2011 | p28, p79 | <https://dlcdnets.asus.com/pub/ASUS/server/RS100-E7(PI2)/Manual/E6489_RS100_E7_PI2_web.pdf> |
| `asus-ts300-e7-ps4-user-guide` | asus | TS300-E7/PS4 user guide (motherboard ASUS P8B-E/4L) | E6468, First Edition | March 2011 | p29 | <https://dlcdnets.asus.com/pub/ASUS/server/TS300-E7_PS4/Manual/e6468_TS300-E7_PS4.pdf> |
| `asus-p9d-user-guide` | asus | P9D-X user guide | E7959, First Edition | April 2013 | p34 | <https://dlcdnets.asus.com/pub/ASUS/mb/socket1150/P9D-X/Manual&AVL/E7959_P9D-X_for_web.pdf> |
| `asus-e28203` | asus | P12R-M Series user guide | E28203, Revised Edition V7 | May 2026 | p31 | <https://dlcdnets.asus.com/pub/ASUS/server/P12R-M/Manual/E28203_P12R-M_Series_UM_V7_WEB.pdf> |
| `asus-p9d-user-guide-2` | asus | P9D-I user guide | E8351, First Edition | July 2013 | p33 | <https://dlcdnets.asus.com/pub/ASUS/mb/socket1150/P9D-I/Manual/E8351_P9D-I.pdf> |
| `asus-z9pa-d8-user-guide` | asus | Z9PA-D8 Series user guide | E7572, First Edition | August 2012 | p12, p33-34 | <https://dlcdnets.asus.com/pub/ASUS/mb/Socket2011/Z9PA-D8/Manual&QVL/E7572_Z9PA-D8_for_web.pdf> |
| `asus-e13692` | asus | Z10PA-D8 Series user guide | E13692, Revised Edition V3 | December 2017 | p13, p31-32 | <https://dlcdnets.asus.com/pub/ASUS/mb/Socket2011-R3/Z10PA-D8/Manual/E13692_Z10PA-D8_Series_UM_V3_WEB.pdf> |
| `asus-z9pe-d16-10g-user-guide` | asus | Z9PE-D16-10G Series user guide | E8158, First Edition | May 2013 | p34-35 | <https://dlcdnets.asus.com/pub/ASUS/mb/Socket2011/Z9PE-D16-10G/Manual/E8158_Z9PE-D16-10G_Series.pdf> |
| `asus-e13683` | asus | Z10PE-D16 WS user guide | E13683, Revised Edition V5 | December 2017 | p11, p32-33 | <https://dlcdnets.asus.com/pub/ASUS/mb/Socket2011-R3/Z10PE-D16_WS/Manual/E13683_Z10PE-D16_WS_UM_V5_WEB.pdf> |
| `asus-rs700-x7-ps4-user-guide` | asus | RS700-X7/PS4 user guide (motherboard ASUS Z9PR-D12/4L) | E7372, First Edition | April 2012 | p13, p27-28 | <https://dlcdnets.asus.com/pub/ASUS/server/RS700-X7_PS4/Manual/e7372_rs700-x7_ps4_for_web.pdf> |
| `asus-z9na-d6-user-manual` | asus | Z9NA-D6 series user manual (Chinese edition) | C7280, first edition | April 2012 | p12, p33-34, p79 | <https://dlcdnets.asus.com/pub/ASUS/mb/Socket1356/Z9NA-D6/Manual/C7280_Z9NA-D6.pdf> |
| `asus-e16279` | asus | WS C621E SAGE user guide | E16279, Revised Edition V7 | February 2020 | p10, p21-22 | <https://dlcdnets.asus.com/pub/ASUS/mb/Socket3647/WS_C621E_SAGE/Manual/E16279_WS_C621E_SAGE_UM_V7_WEB.pdf> |
| `asus-e16292` | asus | RS720Q-E9-RS24-S Series user guide (motherboard ASUS Z11PH-D12) | E16292, Revised Edition V5 | February 2020 | p16, p31 | <https://dlcdnets.asus.com/pub/ASUS/server/RS720Q-E9-RS24-S/Manual/E16292_RS720Q-E9-RS24-S_UM_V5_WEB.pdf> |
| `asus-e16560` | asus | ESC4000 G4 Series user guide (motherboard ASUS Z11PG-D16) | E16560, Revised Edition V9 | August 2022 | p14, p37-38 | <https://dlcdnets.asus.com/pub/ASUS/server/ESC4000_G4/Manual/E16560_ESC4000_G4_Series_UM_V9_WEB.pdf> |
| `asus-e21686` | asus | ESC8000 G4 Series user guide (motherboard ASUS Z11PG-D24) | E21686, Revised Edition V12 | February 2023 | p14, p31-33 | <https://dlcdnets.asus.com/pub/ASUS/server/ESC8000_G4/Manual/E21686_ESC8000_G4_UM_V12_WEB.pdf> |
| `asus-e16276` | asus | Pro WS C621-64L SAGE Series user guide | E16276, Revised Edition V3 | February 2020 | p19-20 | <https://dlcdnets.asus.com/pub/ASUS/mb/Socket3647/Pro_WS_C621-64L_SAGE_10G/MANUAL/E16276_Pro_WS_C621-64L_SAGE_Series_UM_V3_WEB.pdf> |
| `asus-e21771` | asus | ESC4000A-E10 user guide (motherboard ASUS KRPG-U8) | E21771, Revised Edition V9 | March 2023 | p13, p31 | <https://dlcdnets.asus.com/pub/ASUS/server/ESC4000A-E10/Manual/E21771_ESC4000A-E10_UM_V9_WEB.pdf> |
| `asus-e28170` | asus | RS720A-E12-RS12 user guide (motherboard ASUS K14PP-D24) | E28170, Revised Edition V5 | May 2026 | p14, p46-48 | <https://dlcdnets.asus.com/pub/ASUS/server/RS720A-E12-RS12/Manual/E28170_RS720A-E12-RS12_UM_V5_WEB.pdf> |
| `lenovo-p920-p720` | lenovo | Lenovo ThinkStation P920 and P720 Memory Configurator | version 1.0, 11/8/ | 2017 | pp.3-6; pp.7-8 | <https://download.lenovo.com/pccbbs/thinkcentre_pdf/thinkstation_p920_p720_memory_configurations_v1.0.pdf> |
| `lenovo-sr590-7x98-7x99-installation-installation` | lenovo | ThinkSystem SR590 (7X98, 7X99) online documentation, topics "Memory module installation rules", "DRAM DIMM installation rules", "Independent mode" | no document number; fetched | 2026-11-17 | whole topics | <https://pubs.lenovo.com/sr590/dimm_installation_rules> |
| `lenovo-sr650-7x05-7x06-installation` | lenovo | ThinkSystem SR650 (7X05, 7X06) online documentation, topics "Memory configuration", "DRAM DIMM installation rules", "Independent mode" | no document number; fetched | 2026-11-17 | whole topics | <https://pubs.lenovo.com/sr650/memory_configuration> |
| `lenovo-sr530-7x07-7x08-installation` | lenovo | ThinkSystem SR530 (7X07, 7X08) online documentation, topic "Memory module installation rules" | no document number; fetched | 2026-11-17 | whole topic | <https://pubs.lenovo.com/sr530/memory_module_installation_order> |
| `lenovo-sr650-v2-installation` | lenovo | ThinkSystem SR650 V2 online documentation, topics "Memory module installation rules and order", "Independent mode" | no document number; fetched | 2026-11-17 | whole topics | <https://pubs.lenovo.com/sr650-v2/dimm_installation_rules> |
| `lenovo-st650-v2-technical` | lenovo | ThinkSystem ST650 V2 online documentation, topics "Technical rules for memory modules", "Independent memory mode" | no document number; fetched | 2026-11-17 | whole topics | <https://pubs.lenovo.com/st650-v2/technical_rules_dimms> |
| `lenovo-sr645-installation` | lenovo | ThinkSystem SR645 online documentation, topics "Memory module installation rules and order", "Memory" | no document number; fetched | 2026-11-17 | whole topics and three figures | <https://pubs.lenovo.com/sr645/memory_module_install_rules> |
| `lenovo-sr950-v3-installation-installation` | lenovo | ThinkSystem SR950 V3 online documentation, topics "Memory module installation rules and order", "Independent memory mode installation order" | no document number; fetched | 2026-11-17 | whole topics and two figures | <https://pubs.lenovo.com/sr950-v3/memory_module_installation_order> |
| `lenovo-sn550` | lenovo | ThinkSystem SN550 online documentation, topics "Memory configuration", "Independent memory mode" | no document number; fetched | 2026-11-17 | whole topics | <https://pubs.lenovo.com/sn550/memory_configuration> |
| `lenovo-x3650-m5-5462-installation-service` | lenovo | Lenovo System x3650 M5 Type 5462 Installation and Service Guide, online edition, topics "Installing a memory module", "DIMM installation sequence" | no document number online; fetched | 2026-11-17 | whole topics, figure xinyi044b.jpg | <https://pubs.lenovo.com/x3650-m5-5462/c_dimm_rules> |
| `lenovo-x240-m5-9532-2951-installation` | lenovo | Lenovo Flex System x240 M5 Compute Node Types 9532 and 2951 Installation and Service Guide, online edition, topic "Installing a DIMM" | no document number online; fetched | 2026-11-17 | whole topic | <https://pubs.lenovo.com/x240-m5/installing_the_dimm> |
| `lenovo-x3250-m6-installation-service-guide` | lenovo | Lenovo System x3250 M6 Installation and Service Guide, online edition, topics "DIMM installation guidelines", "Unbuffered DIMMs (UDIMMs)", "System-board connectors" | no document number online; fetched | 2026-11-17 | whole topics | <https://pubs.lenovo.com/x3250-m6/r_memory_module_installation> |
| `lenovo-sr250-installation` | lenovo | ThinkSystem SR250 online documentation, topic "Memory module installation rules and order" | no document number; fetched | 2026-11-17 | whole topic | <https://pubs.lenovo.com/sr250/memory_module_installation_order> |
| `lenovo-sr250-v2-technical` | lenovo | ThinkSystem SR250 V2 online documentation, topic "Technical rules for memory modules" | no document number; fetched | 2026-11-17 | whole topic | <https://pubs.lenovo.com/sr250-v2/technical_rules_memory> |
| `oracle-820-5830-13` | sun | Sun Fire X4170, X4270, and X4275 Servers Service Manual | Part No. 820-5830-13, Revision A, December 2010 | 5830-13 | section 4.1, pp. 4-2 to 4-11 | <https://docs.oracle.com/cd/E19477-01/820-5830-13/820-5830-13.pdf> |
| `oracle-e22369-02` | sun | Sun Fire X4170 M2 Server Service Manual | Part No. E22369-02, Revision A, May 2011 | May 2011 | section 4.2, pp. 4-3 to 4-13 | <https://docs.oracle.com/cd/E19762-01/E22369-02/E22369-02.pdf> |
| `oracle-e21671-04` | sun | Sun Fire X4270 M2 Server Service Manual | Part No. E21671-04, January 2012 (page footers: May 2011) | January 2012 | section 4.2, pp. 4-3 to 4-13 | <https://docs.oracle.com/cd/E19245-01/E21671/E21671.pdf> |
| `oracle-e22313-13` | sun | Sun Server X3-2 (formerly Sun Fire X4170 M3) Service Manual | Part No. E22313-13, April 2014 | April 2014 | "Servicing the DIMMs (CRU)", pp. 58 to 70 | <https://docs.oracle.com/cd/E22368_01/pdf/E22313.pdf> |
| `oracle-e23396-15` | sun | Sun Server X3-2L (formerly Sun Fire X4270 M3) Service Manual | Part No. E23396-15, April 2014 | April 2014 | "Servicing the DIMMs (CRU)", pp. 63 to 76 | <https://docs.oracle.com/cd/E23393_01/pdf/E23396.pdf> |
| `oracle-e38041-07` | sun | Sun Server X4-2 Service Manual | Part No. E38041-07, May 2014 | May 2014 | "Servicing the DIMMs (CRU)", pp. 63 to 75 | <https://docs.oracle.com/cd/E36975_01/pdf/E38041.pdf> |
| `oracle-e48320-10` | sun | Oracle Server X5-2 Service Manual | Part No. E48320-10, January 2021 | January 2021 | "Servicing the DIMMs (CRU)", pp. 91 to 104 | <https://docs.oracle.com/cd/E41059_01/pdf/E48320.pdf> |
| `oracle-820-5607-11` | sun | Sun Fire X2270 Server Service Manual | Part No. 820-5607-11, Rev. A, June 2009 | 5607-11 | section 3.5.10, pp. 3-29 to 3-33 | <https://docs.oracle.com/cd/E19432-01/820-5607-11/820-5607-11.pdf> |
| `oracle-821-1345` | sun | Sun Fire X2270 M2 Server Service Manual (sibling) | 821-1345, HTML edition, copyright 2010, 2012 | 2010 | "DIMM Population Rules", "DIMM Slot Designations" | <https://docs.oracle.com/cd/E19902-01/html/821-1345/ajdef.html> |
| `oracle-819-6597-12` | sun | Sun Fire X2200 M2 Server Service Manual | Part No. 819-6597-12, July 2008 (page footer) | 6597-12 | section 1.3.2 p. 1-7, sections 4.4.10 and 4.4.11 pp. 4-31 to 4-36 | <https://docs.oracle.com/cd/E19121-01/sf.x2200m2/819-6597-12/819-6597-12.pdf> |
| `oracle-819-6601-22` | sun | Sun Fire X2200 M2 Server Product Notes | Part No. 819-6601-22, July 2009 (page footer) | 6601-22 | pp. 21, 24, 49 | <https://docs.oracle.com/cd/E19121-01/sf.x2200m2/819-6601-22/819-6601-22.pdf> |
| `oracle-820-0123-13` | sun | Sun Ultra 40 Workstation Service Manual (Ultra 40 and Ultra 40 M2) | Part No. 820-0123-13, Revision A, June 2008 | 0123-13 | section 5.2, pp. 5-5 to 5-16 | <https://docs.oracle.com/cd/E19127-01/ultra40m2.ws/820-0123-13/820-0123-13.pdf> |
| `oracle-819-6584-11` | sun | Sun Ultra 20 M2 Workstation Service Manual | Part No. 819-6584-11, Revision A, January 2007 | 6584-11 | section 5.8 pp. 5-22 to 5-24, appendix A.2 p. A-3 | <https://docs.oracle.com/cd/E19127-01/ultra20m2.ws/819-6584-11/819-6584-11.pdf> |
| `fujitsu-d2619-tx300-s5-technical-manual` | fujitsu | System Board D2619 for RX/TX300 S5, Technical Manual | Edition | April 2009 | section 3.2, pp. 18-23 | <https://web.archive.org/web/20111027141223/http://manuals.ts.fujitsu.com/file/8419/d2619-thb-en.pdf> |
| `fujitsu-d2619-tx300-s6-technical-manual` | fujitsu | System Board D2619-N for PRIMERGY RX/TX300 S6, Technical Manual | Edition | March 2011 | section 3.2, pp. 22-25 | <http://manuals.ts.fujitsu.com/file/9222/d2619-n-thb-en.pdf> |
| `fujitsu-rx300-s5-guide` | fujitsu | PRIMERGY RX300 S5 Options Guide | Edition | September 2009 | chapter 5, pp. 29-31 | <http://manuals.ts.fujitsu.com/file/8488/rx300s5-og-en.pdf> |
| `fujitsu-d2709-tx200-s5` | fujitsu | System Board D2709 für TX200 S5, Technisches Handbuch (German edition) | Ausgabe Mai | 2009 | section 3.2, pp. 17-19 | <https://web.archive.org/web/20110409144153/http://manuals.ts.fujitsu.com/file/8748/d2709-thb-de.pdf> |
| `fujitsu-rx200-s6-guide` | fujitsu | PRIMERGY RX200 S6 Server Options Guide | Edition | November 2011 | pp. 12, 32, 33, 35 | <https://web.archive.org/web/20180329025545/http://manuals.ts.fujitsu.com/file/9274/rx200s6-og-en.pdf> |
| `fujitsu-5600` | fujitsu | White Paper "Memory performance of Xeon 5600 (Westmere-EP) based systems" | Version 2.0 | 2011-06-06 | pp. 2, 5-7 | <https://sp.ts.fujitsu.com/dmsp/Publications/public/wp-westmere-ep-memory-performance-ww-en.pdf> |
| `fujitsu-d2759-tx150-s7-technical-manual` | fujitsu | System Board D2759 for PRIMERGY TX150 S7, Technical Manual | Edition | December 2009 | pp. 20-23, 29 | <https://web.archive.org/web/20160508195258/http://manuals.ts.fujitsu.com/file/9110/d2759-thb-en.pdf> |
| `fujitsu-d2863-rx100-s6-technical-manual` | fujitsu | System Board D2863 for PRIMERGY RX100 S6, Technical Manual | Edition | February 2011 | pp. 17, 20-23 | <https://web.archive.org/web/20180404025537/http://manuals.ts.fujitsu.com/file/9112/rx100s6-d2863-tm-en.pdf> |
| `fujitsu-d3049-tx140-s1-tx120-s3` | fujitsu | System board D3049 for PRIMERGY TX140 S1 / TX120 S3, Technical Manual (Japanese edition) |  | May 2012 | pp. 17, 18, 20, 24, 25 | <https://web.archive.org/web/20160804213347/http://manuals.ts.fujitsu.com/file/10153/tx140s1-tx120s3-d3049-tm-jp.pdf> |
| `fujitsu-tx140-s1-maintenance-manual` | fujitsu | PRIMERGY TX140 S1 Upgrade and Maintenance Manual (Japanese edition) |  | January 2013 | chapter 10, pp. 295-298 | <https://jp.fujitsu.com/platform/server/primergy/products/note/svsdvd/dvd/pdf/tx140s1-umm-jp.pdf> |
| `fujitsu-tx1320-m1-maintenance-manual` | fujitsu | FUJITSU Server PRIMERGY TX1320 M1 Upgrade and Maintenance Manual | Edition | November 2014 | chapter 10, pp. 215-218 | <https://web.archive.org/web/20150321081306/http://manuals.ts.fujitsu.com/file/11779/tx1320m1-umm-en.pdf> |
| `fujitsu-tx1330-m1-maintenance-manual` | fujitsu | FUJITSU Server PRIMERGY TX1330 M1 Upgrade and Maintenance Manual (Japanese edition) |  | August 2014 | chapter 10, pp. 225-228 | <https://jp.fujitsu.com/platform/server/primergy/products/note/svsdvd/dvd/pdf/tx1330m1-umm-jp.pdf> |
| `fujitsu-mx130-s2-maintenance-manual` | fujitsu | PRIMERGY MX130 S2 Server Upgrade and Maintenance Manual | Edition | November 2011 | pp. 179-181 | <https://web.archive.org/web/20140213173318/http://manuals.ts.fujitsu.com/file/10334/mx130s2-umm-en.pdf> |
| `fujitsu-rx200-s8-maintenance-manual` | fujitsu | PRIMERGY RX200 S8 Server Upgrade and Maintenance Manual |  | June 2015 | chapter 10, pp. 197-207 | <https://web.archive.org/web/20190711143807/http://manuals.ts.fujitsu.com/file/11453/rx200s8-umm-en.pdf> |
| `fujitsu-rx200-s7-maintenance-manual` | fujitsu | PRIMERGY RX200 S7 Upgrade and Maintenance Manual (Japanese edition) |  | October 2015 | chapter 10, pp. 236-245 | <https://web.archive.org/web/20220121104812/https://jp.fujitsu.com/platform/server/primergy/products/note/svsdvd/dvd/pdf/rx200s7-umm-jp.pdf> |
| `fujitsu-rx2530-m1-maintenance-manual` | fujitsu | FUJITSU Server PRIMERGY RX2530 M1 Upgrade and Maintenance Manual (Japanese edition) |  | August 2015 | chapter 10, pp. 226-234 | <https://jp.fujitsu.com/platform/server/primergy/products/note/svsdvd/dvd/pdf/rx2530m1-umm-jp.pdf> |
| `fujitsu-rx300-s8-maintenance-manual` | fujitsu | PRIMERGY RX300 S8 Upgrade and Maintenance Manual (Japanese edition) |  | October 2014 | chapter 10, pp. 253-263 | <https://web.archive.org/web/20150513075329/https://jp.fujitsu.com/platform/server/primergy/products/note/svsdvd/dvd/pdf/rx300s8-umm-jp.pdf> |
| `fujitsu-tx2540-m1-maintenance-manual` | fujitsu | FUJITSU Server PRIMERGY TX2540 M1 Upgrade and Maintenance Manual (Japanese edition) |  | December 2014 | chapter 10, pp. 264-266 | <https://web.archive.org/web/20220121105533/https://jp.fujitsu.com/platform/server/primergy/products/note/svsdvd/dvd/pdf/tx2540m1-umm-jp.pdf> |
| `fujitsu-d2119-rx300-s3-technical-manual` | fujitsu | System Board D2119 for RX300 S3, Technical Manual | Edition | May 2006 | section 3.2, pp. 16-17 | <https://web.archive.org/web/20170517071252/http://manuals.ts.fujitsu.com/file/3375/d2119-thb-en.pdf> |
| `fujitsu-rx300-s3-guide` | fujitsu | PRIMERGY RX300 S3 Server Options Guide | Edition | June 2007 | chapter 5, pp. 25-27 | <http://manuals.ts.fujitsu.com/file/4003/rx300s3-og-en.pdf> |
| `fujitsu-rx2540-m4-guide` | fujitsu | PRIMERGY RX2540 M4 System configurator and order information guide | date not on the saved pages |  | pp. 16-19 (chapter RAM) | <https://sp.ts.fujitsu.com/dmsp/Publications/public/cnfgRX2540M4.pdf> |
| `fujitsu-rx2450-m2-guide` | fujitsu | PRIMERGY RX2450 M2 System configurator and order information guide |  | 2026/10/26 | pp. 15-17 (chapter 4, DDR5 system memory) | <https://sp.ts.fujitsu.com/dmsp/Publications/public/cnfgRX2450M2.pdf> |
| `nec-express5800-r120b-1-user-guide` | nec | NEC Express5800/R120b-1 User's Guide | 3rd Edition, 10-2011, ONL-510_017_06_R120b1-100-99-1110 | 2011 | chapter 9, pp. 9-26 to 9-40; p. 4-13 | <https://www.58support.nec.co.jp/global/download/pdf/UGe_R120b1.pdf> |
| `cisco-c220-m4-installation-service-guide` | cisco | Cisco UCS C220 M4 Server Installation and Service Guide | OL-32473-01, September 04, 2015 | September 04, 2015 | chapter 3, "Replacing DIMMs", pp. 3-17 to 3-21 | <https://web.archive.org/web/20150925065807/http://www.cisco.com/c/en/us/td/docs/unified_computing/ucs/c/hw/C220M4/install/C220M4.pdf> |
| `cisco-c220-m4` | cisco | Cisco UCS C220 M4 High-Density Rack Server (Small Form Factor Disk Drive Model) Spec Sheet | REV E.12 | May 13, 2019 | pp. 18-23, 71-76 | <https://www.cisco.com/c/dam/en/us/products/collateral/servers-unified-computing/ucs-c-series-rack-servers/c220m4-sff-spec-sheet.pdf> |
| `cisco-c240-m4-installation-service-guide` | cisco | Cisco UCS C240 M4 Server Installation and Service Guide | OL-32474-01, August 12, 2015 | August 12, 2015 | "Replacing DIMMs" (same slot figure and rules, txt:164-173) | <http://www.cisco.com/c/en/us/td/docs/unified_computing/ucs/c/hw/C240M4/install/C240M4.pdf> |
| `cisco-c240-m4` | cisco | Cisco UCS C240 M4 High-Density Rack Server (Large Form Factor Disk Drive Model) Spec Sheet | REV F.19 | February 17, 2019 | not re-read beyond the title page | <https://www.cisco.com/c/dam/en/us/products/collateral/servers-unified-computing/ucs-c-series-rack-servers/c240m4-lff-spec-sheet.pdf> |
| `sm-mnl-1081` | supermicro | X8DTU / X8DTU-F User's Manual | MNL-1081, Revision 1.20 | Oct. 24, 2012 | pr. 1-3, 2-10 to 2-12 (p11, p30-32) | <https://www.supermicro.com/manuals/motherboard/5500/MNL-1081.pdf> |
| `sm-mnl-1062` | supermicro | X8DT3/X8DTi(-F, -LN4F) User's Manual | MNL-1062, Revision 1.2c | March 9, 2012 | pr. 2-7, 2-8 (p29-30) | <https://www.supermicro.com/manuals/motherboard/5500/MNL-1062.pdf> |
| `sm-mnl-1031` | supermicro | X8DA3/X8DAi User's Manual | MNL-1031 (revision page not read) |  | pr. 1-4 (layout), 2-7, 2-8 | <https://www.supermicro.com/manuals/motherboard/5500/MNL-1031.pdf> |
| `sm-mnl-1076` | supermicro | X8DTL-3/-i/-3F/-iF User's Manual | MNL-1076, Revision 1.2b | Oct. 25, 2013 | pr. 2-7, 2-8 (p29-30) | <https://www.supermicro.com/manuals/motherboard/5500/MNL-1076.pdf> |
| `sm-mnl-1199` | supermicro | X8DTN+-F / X8DTN+-F-LR User's Manual | MNL-1199, Revision 1.1 | Oct. 11, 2011 | pr. 3-3 to 3-5 (p34-36) | <https://www.supermicro.com/manuals/motherboard/5500/MNL-1199.pdf> |
| `sm-mnl-1060` | supermicro | X8DAH+ / X8DAH+-F / X8DAH+(-F)-LR User's Manual | MNL-1060, Revision 1.2b | May 31, 2012 | pr. 1-3, 2-7 to 2-10 (p11, p31-34) | <https://www.supermicro.com/manuals/motherboard/5500/MNL-1060.pdf> |
| `sm-mnl-h8dg` | supermicro | H8DG6/i(-F) Serverboard User's Manual | MNL-H8DGi(6)(-F), Revision 1.2a | March 06, 2014 | pr. 2-8, 2-9 (p28-29) | <https://www.supermicro.com/manuals/motherboard/SR56x0/MNL-H8DGi(6)(-F).pdf> |
| `sm-mnl-h8qg-6` | supermicro | H8QG6/i-F Serverboard User's Manual | MNL-H8QG(6)(i)-F, Revision 1.2a | March 07, 2014 | pr. 2-8 to 2-10 (p28-30) | <https://www.supermicro.com/manuals/motherboard/SR56x0/MNL-H8QG(6)(i)-F.pdf> |
| `sm-mnl-h8qgl-6` | supermicro | H8QGL-6/iF Serverboard User's Manual | MNL-H8QGL-(6)(i)F, Revision 1.1a | March 11, 2014 | pr. 2-8, 2-9 (p28-29) | <https://www.supermicro.com/manuals/motherboard/SR56x0/MNL-H8QGL-(6)(i)F.pdf> |
| `sm-mnl-h8sgl-f` | supermicro | H8SGL(-F) Serverboard User's Manual | MNL-H8SGL(-F), Revision 1.2c | March 11, 2014 | pr. 2-8, 2-9 (p28-29) | <https://www.supermicro.com/manuals/motherboard/SR56x0/MNL-H8SGL(-F).pdf> |
| `sm-mnl-h8scm-f` | supermicro | H8SCM(-F) Serverboard User's Manual | MNL-H8SCM(-F), Revision 1.2c | March 11, 2014 | pr. 2-8, 2-9 (p28-29) | <https://www.supermicro.com/manuals/motherboard/SR56x0/MNL-H8SCM(-F).pdf> |
| `sm-mnl-h8dcl` | supermicro | H8DCL-6/i(F) Serverboard User's Manual | MNL-H8DCL-(i)(6)(F), Revision 1.2 | March 04, 2014 | pr. 2-8, 2-9 (p30-31) | <https://www.supermicro.com/manuals/motherboard/SR56x0/MNL-H8DCL-(i)(6)(F).pdf> |
| `sm-mnl-1307` | supermicro | X9DBL-i/3/iF/3F User's Manual | MNL-1307, Revision 1.0c | May 23, 2013 | pr. 2-9 to 2-12 (p33-36) | <https://www.supermicro.com/manuals/motherboard/C606_602/MNL-1307.pdf> |
| `sm-mnl-1278` | supermicro | X9DRW-3F / X9DRW-iF User's Manual | MNL-1278, Revision 1.1c | June 28, 2021 | pr. 1-10, 2-11 to 2-16 (p17, p34-39) | <https://www.supermicro.com/manuals/motherboard/C606_602/MNL-1278.pdf> |
| `sm-mnl-1258` | supermicro | X9DR3-LN4F+ / X9DRi-LN4F+ User's Manual | MNL-1258, Revision 1.1c | April 1, 2014 | pr. 2-11, 2-12 (p35-36) | <https://www.supermicro.com/manuals/motherboard/C606_602/MNL-1258.pdf> |
| `sm-mnl-1488` | supermicro | X9DRD-iF / X9DRD-LF User's Manual | MNL-1488, Revision 1.0a | March 21, 2014 | pr. 2-12 (p40) | <https://www.supermicro.com/manuals/motherboard/C606_602/MNL-1488.pdf> |
| `sm-mnl-1284` | supermicro | X9SRi / X9SRE (-F, -3F) User's Manual | MNL-1284, Revision 1.0c | February 22, 2013 | pr. 2-8 to 2-10 (p36-38) | <https://www.supermicro.com/manuals/motherboard/C606_602/MNL-1284.pdf> |
| `sm-mnl-1281` | supermicro | X9SRA User's Manual | MNL-1281, Revision 1.1 | March 5, 2014 | pr. 2-8 to 2-10 (p28-30) | <https://www.supermicro.com/manuals/motherboard/C600/MNL-1281.pdf> |
| `sm-mnl-1557` | supermicro | X10DRW-i / X10DRW-iT User's Manual | MNL-1557, Revision 1.2b | January 22, 2020 | pr. 2-11 to 2-13 (p35-37) | <https://www.supermicro.com/manuals/motherboard/C600/MNL-1557.pdf> |
| `sm-mnl-1491` | supermicro | X10DRi / X10DRi-T User's Manual | MNL-1491, Revision 1.2a | October 22, 2020 | pr. 2-13 to 2-15 (p35-37) | <https://www.supermicro.com/manuals/motherboard/C606_602/MNL-1491.pdf> |
| `sm-mnl-1560` | supermicro | X10DRC-T4+ / X10DRC-LN4+ / X10DRi-T4+ / X10DRi-LN4+ User's Manual | MNL-1560, Revision 1.2a | January 31, 2020 | pr. 2-13, 2-14 (p36-37) | <https://www.supermicro.com/manuals/motherboard/C600/MNL-1560.pdf> |
| `sm-mnl-1780` | supermicro | X10DRG-O+-CPU / X10DRG-OT+-CPU / X10DRG-O-PCIE User's Manual | MNL-1780, Revision 1.1a | February 03, 2020 | pr. 2-13 to 2-15 (p38-40) | <https://www.supermicro.com/manuals/motherboard/C612/MNL-1780.pdf> |
| `sm-mnl-1566` | supermicro | X10DRL-i User's Manual | MNL-1566, Revision 1.1b | February 24, 2020 | pr. 2-11, 2-13 (p33, p35) | <https://www.supermicro.com/manuals/motherboard/C600/MNL-1566.pdf> |
| `sm-mnl-1586` | supermicro | X10SRi-F User's Manual | MNL-1586, Revision 1.2 (date not on the pages read) |  | pr. 2-13 to 2-15 (p33-35) | <https://www.supermicro.com/manuals/motherboard/C612/MNL-1586.pdf> |
| `sm-mnl-1643` | supermicro | X10SRA / X10SRA-F User's Manual | MNL-1643, Revision 1.1b | August 17, 2016 | pr. 2-9 to 2-12 (p39-42) | <https://www.supermicro.com/manuals/motherboard/C612/MNL-1643.pdf> |
| `sm-mnl-1845` | supermicro | X10SRM-F / X10SRM-TF User Manual | MNL-1845, Revision 1.1 | April 25, 2019 | pr. 31 to 33 (p31-33) | <https://www.supermicro.com/manuals/motherboard/C600/MNL-1845.pdf> |
| `sm-mnl-1428` | supermicro | X10SLM-F / X10SLL-F / X10SLL-SF / X10SLL-S User's Manual | MNL-1428, Revision 1.1b | June 21, 2016 | pr. 2-11, 2-12 (p35-36) | <https://www.supermicro.com/manuals/motherboard/C222/MNL-1428.pdf> |
| `sm-mnl-1463` | supermicro | X10SL7-F User's Manual | MNL-1463, Revision 1.1a | August 19, 2014 | pr. 2-10, 2-11 (p30-31) | <https://www.supermicro.com/manuals/motherboard/C222/MNL-1463.pdf> |
| `sm-mnl-1464` | supermicro | X10SLH-F User's Manual | MNL-1464, Revision 1.1 | December 22, 2016 | pr. 2-11, 2-12 (p33-34) | <https://www.supermicro.com/manuals/motherboard/C222/MNL-1464.pdf> |
| `sm-mnl-1479` | supermicro | X10SAE User's Manual | MNL-1479, Revision 1.1c | January 8, 2015 | pr. 2-10, 2-11 (p30-31) | <https://www.supermicro.com/manuals/motherboard/C226/MNL-1479.pdf> |
| `sm-mnl-1726` | supermicro | X10SDV Mini-ITX Series User's Manual | MNL-1726, 1.0c | February 12, 2020 | pr. 2-7, 2-8 (p34-35) | <https://www.supermicro.com/manuals/motherboard/D/MNL-1726.pdf> |
| `sm-mnl-1778` | supermicro | X11SSH-F / X11SSH-LN4F User Manual | MNL-1778, Revision 1.1 | July 30, 2018 | pr. 33 to 35 (p33-35) | <https://www.supermicro.com/manuals/motherboard/C236/MNL-1778.pdf> |
| `sm-mnl-2087` | supermicro | X11SCA / X11SCA-W / X11SCA-F User Manual | MNL-2087, Revision 1.2, January 13, 2022 | 2087 | pr. 34 (p34) | <https://www.supermicro.com/manuals/motherboard/X11/MNL-2087.pdf> |
| `sm-mnl-2088` | supermicro | X11SCL-IF User's Manual | MNL-2088, Revision 1.1, May 06, 2020 | 2088 | pr. 29, 30 (p29-30) | <https://www.supermicro.com/manuals/motherboard/X11/MNL-2088.pdf> |
| `sm-mnl-1795` | supermicro | X11SSV-Q User's Manual | MNL-1795, Revision 1.0a | July 6, 2018 | pr. 27 (p27) | <https://www.supermicro.com/manuals/motherboard/Q170/MNL-1795.pdf> |
| `sm-mnl-1773` | supermicro | X11DPi-N / X11DPi-NT User's Manual | MNL-1773, Revision 1.3 | July 30, 2021 | pr. 33 to 38 (p33-38) | <https://www.supermicro.com/manuals/motherboard/C620/MNL-1773.pdf> |
| `sm-x11-user-guide` | supermicro | Memory Configuration for X11 UP/DP/MP Motherboards, User's Guide | Revision 1.1a | February 7, 2020 | pp. 6-7, 19 | <https://www.supermicro.com/support/resources/memory/X11_memory_config_guide.pdf> |
| `sm-mnl-1907` | supermicro | X11DDW-L / X11DDW-NT User's Manual | MNL-1907, Revision 2.0, September 30, 2020 | 1907 | pr. 33, 37 (p33, p37) | <https://www.supermicro.com/manuals/motherboard/C620/MNL-1907.pdf> |
| `sm-mnl-1946` | supermicro | X11DPL-i User's Manual | MNL-1946, Revision 1.1b, April 10, 2020 | 1946 | pr. 30, 33 (p30, p33) | <https://www.supermicro.com/manuals/motherboard/C620/MNL-1946.pdf> |
| `sm-mnl-1983` | supermicro | X11QPH+ User's Manual | MNL-1983, Revision 1.1c, September 28, 2020 | 1983 | pr. 33, 35 (p33, p35) | <https://www.supermicro.com/manuals/motherboard/C620/MNL-1983.pdf> |
| `sm-mnl-1900` | supermicro | X11SPi-TF User's Manual | MNL-1900, Revision 1.1b, October 11, 2022 | 1900 | pr. 32 to 34 (p32-34) | <https://www.supermicro.com/manuals/motherboard/C620/MNL-1900.pdf> |
| `sm-mnl-1939` | supermicro | X11SPM-F / X11SPM-TF / X11SPM-TPF User's Manual | MNL-1939, Revision 1.1c, March 10, 2020 | 1939 | pr. 34 to 36 (p34-36) | <https://www.supermicro.com/manuals/motherboard/C620/MNL-1939.pdf> |
| `sm-mnl-2135` | supermicro | SBI-6119P-T3N SuperBlade Module User's Manual | MNL-2135, Revision 1.0b | April 19, 2023 | pr. 3-13 to 3-16 (p47-50) | <https://www.supermicro.com/manuals/superblade/MNL-2135.pdf> |
| `sm-mnl-2005` | supermicro | X11SRA / X11SRA-F / X11SRA-RF User Manual | MNL-2005, Revision 1.1b, January 29, 2021 | 2005 | pr. 30 to 32 (p30-32) | <https://www.supermicro.com/manuals/motherboard/C420/MNL-2005.pdf> |
| `sm-mnl-2019` | supermicro | X11SDV-TLN2F series User's Manual | MNL-2019, Revision 1.0a, April 02, 2021 | 2019 | pr. 23, 24 (p23-24) | <https://www.supermicro.com/manuals/motherboard/D/MNL-2019.pdf> |
| `sm-mnl-1951` | supermicro | A2SDi-HLN4F Series User's Manual | MNL-1951, Revision 1.0a, February 24, 2020 | 1951 | pr. 28, 29 (p28-29) | <https://www.supermicro.com/manuals/motherboard/Atom_on-chip/MNL-1951.pdf> |
| `sm-mnl-1600` | supermicro | A1SA2-2750F / A1SA2-2550F User's Manual | MNL-1600, Revision 1.0 | April 23, 2014 | pr. 2-5 (p25) | <https://www.supermicro.com/manuals/motherboard/Atom_on-chip/MNL-1600.pdf> |
| `sm-mnl-1561` | supermicro | A1SAM-2750F / A1SAM-2550F / A1SRM-2758F / A1SRM-2558F User's Manual | MNL-1561, Revision 1.0 (date not on the pages read) |  | pr. 2-5, 2-6 (p25-26) | <https://www.supermicro.com/manuals/motherboard/Atom_on-chip/MNL-1561.pdf> |
| `sm-mnl-2233` | supermicro | X12DPi-N6 / X12DPi-NT6 User's Manual | MNL-2233, Revision 2.0b | May 18, 2023 | pr. 39 to 41 (p39-41) | <https://www.supermicro.com/manuals/motherboard/X12/MNL-2233.pdf> |
| `sm-mnl-2239` | supermicro | X12DAi-N6 User's Manual | MNL-2239, Revision 1.1a | April 22, 2022 | pr. 42, 43 (p42-43) | <https://www.supermicro.com/manuals/motherboard/X12/MNL-2239.pdf> |
| `sm-mnl-2234` | supermicro | X12DPU-6 User's Manual | MNL-2234, Revision 1.0b | March 11, 2022 | pr. 41, 42 (p41-42) | <https://www.supermicro.com/manuals/motherboard/X12/MNL-2234.pdf> |
| `sm-mnl-2270` | supermicro | X12SPL-LN4F / X12SPL-F User's Manual | MNL-2270, Revision 1.1c | November 20, 2023 | pr. 33, 34 (p33-34) | <https://www.supermicro.com/manuals/motherboard/X12/MNL-2270.pdf> |
| `sm-mnl-2358` | supermicro | X12SAE-5 / X12SCA-5F User's Manual | MNL-2358, Revision 1.1c | September 30, 2026 | pr. 32, 33 (p32-33) | <https://www.supermicro.com/manuals/motherboard/X12/MNL-2358.pdf> |
| `sm-mnl-2362` | supermicro | X12SDV-4C/8C/10C-SP6F, X12SDV-8CE-SP4F, X12SDV-10CR-SP6F User's Manual | MNL-2362, Revision 1.0a | June 04, 2024 | pr. 13, 25 to 27 (p13, p25-27) | <https://www.supermicro.com/manuals/motherboard/X12/MNL-2362.pdf> |
| `sm-mnl-2440` | supermicro | X12SDV-4C/8C/14C/16C/20C-SPT8F User's Manual | MNL-2440, Revision 1.0c | May 02, 2025 | pr. 12, 24 to 26 (p12, p24-26) | <https://www.supermicro.com/manuals/motherboard/X12/MNL-2440.pdf> |
| `sm-mnl-2509` | supermicro | X13DEI / X13DEI-T User's Manual | MNL-2509, Revision 1.0c | August 25, 2025 | pr. 59 to 62 (p59-62) | <https://www.supermicro.com/manuals/motherboard/X13/MNL-2509.pdf> |
| `sm-mnl-2413` | supermicro | X13SEW-TF / X13SEW-F User's Manual | MNL-2413, Revision 1.1c | May 06, 2025 | pr. 33 to 35 (p33-35) | <https://www.supermicro.com/manuals/motherboard/X13/MNL-2413.pdf> |
| `sm-mnl-2425` | supermicro | X13SAE / X13SAE-F User's Manual | MNL-2425, Revision 1.2c | September 30, 2026 | pr. 34, 35 (p34-35) | <https://www.supermicro.com/manuals/motherboard/X13/MNL-2425.pdf> |
| `sm-mnl-2517` | supermicro | X13SAN-H/-E/-L/-C User's Manual | MNL-2517, Revision 1.0a | June 09, 2025 | pr. 29 (p29) | <https://www.supermicro.com/manuals/motherboard/X13/MNL-2517.pdf> |
| `sm-mnl-2027` | supermicro | H11DSi / H11DSi-NT User's Manual | MNL-2027, Revision 1.1a, August 06, 2020 | 2027 | pr. 31 to 33 (p31-33) | <https://www.supermicro.com/manuals/motherboard/EPYC7000/MNL-2027.pdf> |
| `sm-mnl-2363` | supermicro | H12DSi-N6 / H12DSi-NT6 User's Manual | MNL-2363, Revision 1.0c | June 04, 2025 | pr. 31 to 33 (p31-33) | <https://www.supermicro.com/manuals/motherboard/EPYC7000/MNL-2363.pdf> |
| `sm-mnl-2085` | supermicro | H11SSL-i / H11SSL-C / H11SSL-NC User's Manual | MNL-2085, Revision 1.1a, December 18, 2019 | 2085 | pr. 33, 34 (p33-34) | <https://www.supermicro.com/manuals/motherboard/EPYC7000/MNL-2085.pdf> |
| `sm-mnl-2314` | supermicro | H12SSL-i/C/CT/NT User's Manual | MNL-2314, Revision 1.0a | April 22, 2021 | pr. 31, 32 (p31-32) | <https://www.supermicro.com/manuals/motherboard/EPYC7000/MNL-2314.pdf> |
| `sm-mnl-2299` | supermicro | H12DSG-O-CPU User's Manual | MNL-2299, Revision 1.0a | April 14, 2021 | pr. 32, 33 (p32-33) | <https://www.supermicro.com/manuals/motherboard/EPYC7000/MNL-2299.pdf> |
| `sm-mnl-2627` | supermicro | H13SAE-MF User's Manual | MNL-2627, Revision 1.1b | November 03, 2026 | pr. 32 to 34 (p32-34) | <https://www.supermicro.com/manuals/motherboard/H13/MNL-2627.pdf> |
| `sm-mnl-2172` | supermicro | M11SDV-4C/4CT/8C/8CT/8C+-LN4F User Manual | MNL-2172, Revision 1.1a | December 30, 2021 | pr. 25 to 27 (p25-27) | <https://www.supermicro.com/manuals/motherboard/EPYC3000/MNL-2172.pdf> |
| `sm-mnl-2336` | supermicro | M12SWA-TF User's Manual | MNL-2336, Revision 1.1c | September 08, 2025 | pr. 32 to 34 (p32-34) | <https://www.supermicro.com/manuals/motherboard/M12/MNL-2336.pdf> |
| `sm-mnl-1646` | supermicro | C7X99-OCE / C7X99-OCE-F User's Manual | MNL-1646, Revision 1.1 | September 21, 2016 | pr. 2-9 to 2-11 (p39-41) | <https://www.supermicro.com/manuals/motherboard/X99/MNL-1646.pdf> |
| `sm-mnl-2117` | supermicro | C9X299-PG300F User's Manual | MNL-2117, Revision 1.0c | November 12, 2020 | pr. 30, 31 (p30-31) | <https://www.supermicro.com/manuals/motherboard/X299/MNL-2117.pdf> |
| `sm-mnl-1813` | supermicro | C7Z170-M / C7H170-M User's Manual | MNL-1813, Revision 1.0c | May 26, 2017 | pr. 2-9 to 2-11 (p39-41) | <https://www.supermicro.com/manuals/motherboard/Z170/MNL-1813.pdf> |
| `sm-mnl-2018` | supermicro | C9Z390-CG-IW User's Manual | MNL-2018, Revision 1.0a, May 14, 2019 | 2018 | pr. 2-8, 2-9 (p32-33) | <https://www.supermicro.com/manuals/motherboard/Z390/MNL-2018.pdf> |
| `gigabyte-r182-z90-r182-z91-r182` | gigabyte | R182-Z90 / R182-Z91 / R182-Z92 AMD EPYC 7003 DP Server System User Manual | Rev. 1.0 | 2021 | p. 32-35 | <https://download.gigabyte.com/FileList/Manual/server_manual_R182-Z90_Z91_Z92_e_A00.pdf> |
| `gigabyte-mz32-ar0-user-manual` | gigabyte | MZ32-AR0 User Manual | Rev. 1.0 and Rev. 3.0 | 2021 | p. 13-15 | <https://download.gigabyte.com/FileList/Manual/server_manual_MZ32-AR0_e_v10.pdf> |
| `gigabyte-e252-p30-user-manual` | gigabyte | E252-P30 Edge Server User Manual | Rev. 1.0 | 2021 | p. 25-28 | <https://download.gigabyte.com/FileList/Manual/server_manual_e_E252-P30_v10.pdf> |
| `gigabyte-mx33-bs0-user-manual` | gigabyte | MX33-BS0 Server Board User Manual | Rev. 1.0 | 2021 | p. 12-13 | <https://download.gigabyte.com/FileList/Manual/sever_manual_MX33-BS0_e_10.pdf> |
| `gigabyte-mz71-ce0-mz71-ce1-user` | gigabyte | MZ71-CE0 / MZ71-CE1 User Manual | Rev. 1.0 (2020) and Rev. 3.0 | 2020 | p. 13-15 | <https://download.gigabyte.com/FileList/Manual/server_manual_MZ71-CEx_e_v10.pdf> |
| `gigabyte-g431-mm0-user-manual` | gigabyte | G431-MM0 GPU Server User Manual | Rev. 1.0 | 2020 | p. 25-26 | <https://download.gigabyte.com/FileList/Manual/server_manual_e_G431-MM0_v1.0.pdf> |
| `gigabyte-mc13-le0-mc13-le1-user` | gigabyte | MC13-LE0 / MC13-LE1 Server Board User Manual | Rev. 1.0 | 2022 | p. 13-14 | <https://download.gigabyte.com/FileList/Manual/sever_manual_MC13-LE0_1_e_v1.0.pdf> |
| `gigabyte-mu71-su0-user-manual` | gigabyte | MU71-SU0 Server Board User Manual | Rev. 1.0 | 2019 | p. 9, 12-13 | <https://download.gigabyte.com/FileList/Manual/server_manual_MU71-SU0_e_v10.pdf> |
| `gigabyte-c621-su8-user-manual` | gigabyte | C621-SU8 User's Manual | Rev. 1001 | 2018 | p. 8, 12 | <https://download.gigabyte.com/FileList/Manual/server_manual_c621-su8_1001_e_v1.pdf> |
| `gigabyte-c621-sd8-user-manual` | gigabyte | C621-SD8 User's Manual | Rev. 1001 | 2018 | p. 8, 11 | <https://download.gigabyte.com/FileList/Manual/server_manual_c621-sd8_1001_e_v2.pdf> |
| `gigabyte-mw50-sv0-user-manual` | gigabyte | MW50-SV0 User's Manual | Rev. 1002 | 2019 | p. 8, 18 | <https://download.gigabyte.com/FileList/Manual/server_manual_mw50-sv0_e_v1002.pdf> |
| `gigabyte-g291-280-g291-281-user` | gigabyte | G291-280 / G291-281 GPU Server User Manual | Rev. 1.1 | 2019 | p. 27-29 | <https://download.gigabyte.com/FileList/Manual/server_system_manual_g291-28x_e_1.0.pdf> |
| `gigabyte-r280-f2o-r280-g2o-user` | gigabyte | R280-F2O / R280-G2O Server User Manual | Rev. 1.1 | 2016 | p. 12, 19-21, 36 | <https://download.gigabyte.com/FileList/Manual/server_manual_r280-f2o_g2o_a3c_f3c_e_11.pdf> |
| `gigabyte-mu70-su0-user-manual` | gigabyte | MU70-SU0 User's Manual | Rev. 1002 | 2016 | p. 7, 11, 17-18 | <https://download.gigabyte.com/FileList/Manual/server_manual_mu70-su0_en_1002.pdf> |
| `gigabyte-h261-h60-user-manual` | gigabyte | H261-H60 High Density Server User Manual | Rev. 1.0 | 2019 | p. 28-30 | <https://download.gigabyte.com/FileList/Manual/server_manual_h261-h60_e_1.0.pdf> |
| `gigabyte-md72-hb0-md72-hb1-user` | gigabyte | MD72-HB0 / MD72-HB1 Server Board User Manual | Rev. 1.0 | 2021 | p. 9, 13-17 | <https://download.gigabyte.com/FileList/Manual/server_mb_manual__md72hb0_1_e_1.0.pdf> |
| `gigabyte-mu72-su0-user-manual` | gigabyte | MU72-SU0 Server Board User Manual | Rev. 1.0 | 2021 | p. 9, 12-16 | <https://download.gigabyte.com/FileList/Manual/server_mb_manual_MU72-SU0_e_v1.0.pdf> |
| `gigabyte-6pxsvl-user-manual` | gigabyte | GA-6PXSVL User's Manual | Rev. 1001 | 2014 | p. 7, 11, 15 | <https://download.gigabyte.com/FileList/Manual/server_manual_6pxsvl_e_v1001.pdf> |
| `gigabyte-g242-z10-g242-z11-user` | gigabyte | G242-Z10 / G242-Z11 HPC Server User Manual | Rev. 1.0 | 2021 | p. 32-34 | <https://download.gigabyte.com/FileList/Manual/server_manual_e_G242-Z10_Z11_A00.pdf> |
| `gigabyte-mc62-g40-user-manual` | gigabyte | MC62-G40 Workstation Board User Manual | Rev. 1.0 | 2021 | p. 10, 13-14 | <https://download.gigabyte.com/FileList/Manual/server_manual__MC62-G40_e_1001.pdf> |
| `asrock-b650d4u-b650d4u-2l2t-b650d4u-2l2t` | asrock | B650D4U / B650D4U-2L2T / B650D4U-2L2T/BCM User Manual | Version 1.30, Jun. 2026 (first published August 2022) | Jun. 2026 | p9, p14-17, p23-25, p32 | <https://download.asrock.com/Manual/B650D4U.pdf> |
| `asrock-x570d4u-x570d4u-2l2t-x570d4u-2l2t` | asrock | X570D4U / X570D4U-2L2T / X570D4U-2L2T/BCM User Manual | Version 1.0 | September 2022 | p8, p13, p14, p29 | <https://download.asrock.com/Manual/X570D4U.pdf> |
| `asrock-romed8-2t-romed8-romed8-2t` | asrock | ROMED8-2T / ROMED8-NL / ROMED8-2T/BCM User Manual | Version 1.30, Apr. 2026 (first published February 2022) | Apr. 2026 | p9, p14, p30 | <https://download.asrock.com/Manual/ROMED8-2T.pdf> |
| `asrock-rome2d16-2t-rome2d16-rome2d16-2l` | asrock | ROME2D16-2T / ROME2D16-NL / ROME2D16-2L+ / ROME2D16-2T/BCM User Manual | Version 1.10 | Dec. 2024 | p8, p21, p31 | <https://download.asrock.com/Manual/ROME2D16-2T.pdf> |
| `asrock-genoad12m3-2q-user-manual-genoad12m3` | asrock | GENOAD12M3-2Q User Manual (covers GENOAD12M3-2Q, GENOAD12M3-2Q/H, GENOAD12M3/T) | Version 1.22, Oct. 2025 (first published April 2023) | Oct. 2025 | p8, p9, p13, p27 | <https://download.asrock.com/Manual/GENOAD12M3-2Q.pdf> |
| `asrock-z690d4u-2l2t-w680d4u-2l2t-w680d4u` | asrock | Z690D4U-2L2T / W680D4U-2L2T / W680D4U-1L / Z690D4U / W680D4U User Manual | Version 1.30 | Mar. 2026 | p11, p19, p23, p29, p36, p41 | <https://download.asrock.com/Manual/W680D4U-1L.pdf> |
| `asrock-sp2c621d32lm3-user-manual` | asrock | SP2C621D32LM3 User Manual | Version 1.0 | June 2023 | p8, p13, p20, p28-31 | <https://download.asrock.com/Manual/SP2C621D32LM3.pdf> |
| `asrock-altrad8ud-1l2t-user-manual` | asrock | ALTRAD8UD-1L2T User Manual | Version 1.20, Jul. 2025 (first published July 2023) | Jul. 2025 | p8, p12, p30 | <https://download.asrock.com/Manual/ALTRAD8UD-1L2T.pdf> |
| `msi-d3052-s3661-user-guide` | msi | D3052 (MS-S3661) Server Motherboard User Guide | Revision V2.0 | 2025/06 | p18 | <https://download-2.msi.com/archive/mnu_exe/server/S3661-D3052-v2.0-UG.pdf> |
| `tyan-s7012-user-manual` | tyan | S7012 user's manual | Version 1.1 (file S7012_UG_v1.1_06212012.pdf), (c) 2009 MiTAC | 2009 | p10, p26 | <https://web.archive.org/web/20121119035656id_/http://www.tyan.com:80/manuals/S7012_UG_v1.1_06212012.pdf> |
| `tyan-s7010-user-manual` | tyan | S7010 user's manual | Version 1.0, (c) 2009 MiTAC | 2009 | p12, p27-28 | <https://web.archive.org/web/20090419093950id_/http://www.tyan.com:80/manuals/S7010_UG_v1.0.pdf> |
| `tyan-s8026-user-manual` | tyan | S8026 user's manual | Version 1.0h, (c) 2017 MiTAC | 2017 | p15, p36-38 | <https://web.archive.org/web/20260611010444id_/https://ftp1.tyan.com/pub/doc/S8026_UG_v1.0h.pdf> |
| `tyan-s8036-user-manual` | tyan | S8036 user's manual (text copy on manualslib; the version is not shown) |  |  | p39 | <https://www.manualslib.com/manual/2447880/Tyan-S8036.html?page=39> |
| `tyan-s7106-user-manual` | tyan | S7106 user's manual (text copy on manualslib; the version is not shown) |  |  | p31 | <https://www.manualslib.com/manual/1307092/Tyan-S7106.html?page=31> |
| `tyan-s8812-user-manual` | tyan | S8812 user's manual | Version 1.2b, (c) 2013 MiTAC | 2013 | p10, p28-30 | <https://web.archive.org/web/20130810231136id_/http://www.tyan.com:80/manuals/S8812_UG_v1.2b.pdf> |
| `tyan-k8qs-s4882-user-manual` | tyan | Thunder K8QS Pro S4882 user's manual | Revision 1.01, (c) TYAN 2003-2004 |  | p9, p27-28 | <https://web.archive.org/web/20081207091103id_/http://www.tyan.com/manuals/m_s4882_101.pdf> |
| `huawei-rh1288-v3-user-guide` | huawei | FusionServer RH1288 V3 Server User Guide | Issue 04, 2023-02-28, xFusion | 2023-02-28 | pdf p. 57, 201-202 | not recorded |
| `huawei-rh2288h-v3-user-guide` | huawei | FusionServer RH2288H V3 Server User Guide | Issue 04, 2023-02-28, xFusion | 2023-02-28 | pdf p. 80, 233-234 | not recorded |
| `huawei-1288h-v5-user-guide` | huawei | FusionServer 1288H V5 Server User Guide | Issue 17, 2024-05-11, xFusion | 2024-05-11 | pdf p. 28-33 (p. 18-23) | not recorded |
| `lenovo-rd330-maintenance-manual` | lenovo | ThinkServer RD330 Hardware Maintenance Manual | Sixth Edition ( | March 2015 | p. 77-80 (pdf p. 91-94) | <https://download.lenovo.com/pccbbs/thinkservers/rd330hmm_en.pdf> |
| `lenovo-rd450-user-guide-maintenance-manual` | lenovo | ThinkServer RD450 User Guide and Hardware Maintenance Manual | Ninth Edition ( | November 2017 | p. 69-74 (pdf p. 81-88); p. 69-74 | <https://download.lenovo.com/pccbbs/thinkservers/rd450_ug_en.pdf> |
| `lenovo-memory-configurator-lenovo` | lenovo | Memory Configurator Lenovo ThinkStation PX | Version 1.3 | March 2025 | pp. 4-9 | not recorded |
| `fujitsu-tx1310-m3-guide` | fujitsu | PRIMERGY TX1310 M3 System configurator and order-information guide | March 2021 (status 2021-02-25) | March 2021 | configuration diagram and section III | not recorded |
| `inspur-nf5280m-lm6-user-manual` | inspur | NF5280M/LM6 User Manual (meta brain Server, IEIT SYSTEMS) | V1.6 | 2025-12-17 | p. 50-53 (pdf p. 59-64) | not recorded |
| `inspur-sa5212m4-sa5212m4-user-manual` | inspur | 浪潮英信服务器 SA5212M4 用户手册 (Inspur SA5212M4 user manual, Chinese) | V1.0 | December 2014 | p. 69-70 (pdf p. 75-76) | not recorded |
| `quanta-t22hf-1u-service-guide` | quanta | QuantaPlex Series T22HF-1U Service Guide | Version 1.0 | May 5, 2019 | pdf p. 49-50, 110 | not recorded |
| `huawei-200-maintenance-service-guide-2280` | huawei | TaiShan 200 Server Maintenance and Service Guide (Model 2280) | Issue 23, 2025-07-27, Huawei, EDOC1100093458 | 2025-07-27 | p. 37-41 (pdf p. 49-52) | <https://support.huawei.com/enterprise/en/doc/EDOC1100093458> |
| `huawei-500-maintenance-service-guide-3000` | huawei | Atlas 500 Pro AI Edge Server Maintenance and Service Guide (Model 3000) | Issue 18, 2023-12-06, EDOC1100141191 | 2023-12-06 | p. 19-20 | <https://support.huawei.com/enterprise/en/doc/EDOC1100141191> |
| `quanta-v3-1` | quanta | Facebook Server Intel Motherboard V3.1 (Leopard), Open Compute Project | Rev 1.00 | 2016-01-06 | p. 13, 45, 49-50, 68 | <https://opencompute.org/documents/facebook-xeon-motherboard-v31> |
| `quanta-v2-0` | quanta | Open Compute Project Intel Motherboard Hardware v2.0 (Windmill) |  | April 11, 2012 | p. 7-8, 30-31, 34 | <http://www.opencompute.org/wp/wp-content/uploads/2012/05/Open_Compute_Project_Intel_Motherboard_v2.0.pdf> |
| `hpe-population-gen10-3rd-technical` | hp | Server Memory and Persistent Memory population rules for HPE Gen10 Plus servers with 3rd Gen Intel Xeon Scalable processors (technical white paper) | a50003886ENW Rev. 6, PDF dated 3 September 2025 | 3 September 2025 | pp. 3-5, 9, 12 | <https://www.hpe.com/psnow/doc/a50003886enw> |
| `hpe-dl360-gen10-maintenance-service-guide-2` | hp | HPE ProLiant DL360 Gen10 Plus Server Maintenance and Service Guide | docId a00114988en_us (part number 30-C15B03A6-005, edition 5, February 2025 as the notes give it; not in the saved topics) | February 2025 | topics "DIMM slot locations", "DIMM-processor compatibility" | <https://support.hpe.com/hpesc/public/docDisplay?docId=a00114988en_us> |
| `hpe-population-gen10-gen10-7002-7003` | hp | Server memory population rules for HPE ProLiant Gen10 and Gen10 Plus servers with AMD EPYC 7002 and 7003-series processors (technical white paper) | a00038346ENW Rev. 7, PDF dated 4 August 2025 | 4 August 2025 | pp. 3-4, 6, 8, 11-12 | <https://www.hpe.com/psnow/doc/a00038346enw> |
| `hpe-dl385-gen10-maintenance-service-guide` | hp | HPE ProLiant DL385 Gen10 Plus Server Maintenance and Service Guide | docId a00094064en_us |  | topics "DIMM slot locations", "DIMM-processor compatibility" | <https://support.hpe.com/hpesc/public/docDisplay?docId=a00094064en_us> |
| `hpe-dl385-gen10-v2-maintenance-service` | hp | HPE ProLiant DL385 Gen10 Plus v2 Server Maintenance and Service Guide | docId a00112969en_us |  | same two topics | <https://support.hpe.com/hpesc/public/docDisplay?docId=a00112969en_us> |
| `hpe-population-gen11-5th-technical` | hp | Server memory population rules for HPE Gen11 servers with 5th Gen Intel Xeon Scalable processors (technical white paper) | a50010242ENW, no revision printed, PDF dated 10 March 2026 | 10 March 2026 | pp. 4, 6, 10-12, 15 | <https://www.hpe.com/psnow/doc/a50010242enw> |
| `hpe-population-gen11-4th` | hp | Server memory population rules for HPE Gen11 servers with 4th Gen Intel Xeon Scalable processors | a50007437ENW Rev. 1, PDF dated 12 March 2026 | 12 March 2026 | p. 14 | <https://www.hpe.com/psnow/doc/a50007437enw> |
| `hpe-dl380-gen11` | hp | HPE ProLiant DL380 Gen11 QuickSpecs | a50004307enw, version 48 | 5 October 2026 | pp. 41, 77 | <https://www.hpe.com/psnow/doc/a50004307enw> |
| `hpe-dl380-gen11-maintenance-service-guide` | hp | HPE ProLiant DL380 Gen11 Server Maintenance and Service Guide | docId sd00002447en_us |  | topic "DIMM slot locations" | <https://support.hpe.com/hpesc/public/docDisplay?docId=sd00002447en_us> |
| `hpe-dl160-gen10` | hp | HPE ProLiant DL160 Gen10 Server QuickSpecs | a00021860enw, version 32, 18 August 2025 (marked retired in the notes) | 18 August 2025 | pp. 8, 31 | <https://www.hpe.com/psnow/doc/a00021860enw> |
| `hpe-876833-006-2` | hp | HPE ProLiant BL460c Gen10 Server Blade User Guide (channel names only) | 876833-006, edition 6, May 2019 | May 2019 | p. 15 | <https://support.hpe.com/hpesc/public/docDisplay?docId=a00018554en_us> |
| `hpe-population-gen10-2300-technical` | hp | Server memory population rules for HPE ProLiant Gen10 Plus servers with Intel Xeon E-2300 processors (technical white paper) | a50005926ENW Rev. 2, PDF dated 6 August 2025 | 6 August 2025 | pp. 3-5 | <https://www.hpe.com/psnow/doc/a50005926enw> |
| `hpe-xd665-maintenance-service-guide` | hp | HPE Cray Supercomputing XD665 CTO Chassis Maintenance and Service Guide | part number 10-150126-Q425, edition 5 | August 2025 | topics "DIMM slot locations", "DIMM population rule", "Memory information" | <https://support.hpe.com/hpesc/public/docDisplay?docId=sd00003514en_us> |
| `hpe-xd665` | hp | HPE Cray XD665 QuickSpecs | a50007004enw, version 11 | 2 December 2024 | pp. 6-7 | <https://www.hpe.com/psnow/doc/a50007004enw> |
| `gigabyte-r182-n20-r182-na0-3rd` | gigabyte | R182-N20 / R182-NA0 3rd Gen. Intel Xeon Scalable DP Server System, User Manual | Rev. 1.0 | 2021 | PDF pp. 31-33 | <https://download.gigabyte.com/FileList/Manual/server_manual_R182-N20_NA0_e_10.pdf> |
| `huawei-2288h-v5-user-guide` | huawei | FusionServer 2288H V5 Server User Guide | Issue 21, 2024-05-11, XFUSION INTERNATIONAL PTE. LTD. | 2024-05-11 | PDF pp. 32-37 | not recorded |
| `huawei-rh2288h-v3-user-guide-2` | huawei | FusionServer RH2288H V3 Server User Guide | Issue 04, 2023-02-28, xFusion Digital Technologies | 2023-02-28 | PDF pp. 80, 233-234 | not recorded |
| `asrock-sp2c621d32tm3-user-manual` | asrock | SP2C621D32TM3 User Manual | Version 1.20 | 2024 | PDF pp. 18, 31-35 | <https://download.asrock.com/Manual/SP2C621D32TM3.pdf> |
| `other-user-manual` | other | Libra Server MotherBoard User's Manual | UM_Libra_v3.1_122216 (excerpt pp. 1-2, 8, 16-19) |  | manual pp. 16-18 | not recorded |
| `other-12th-1` | other | 12th Generation Intel Core Processors Datasheet, Volume 1 | 655258, rev. 011 | May 2025 | PDF pp. 108, 115-117 | <https://cdrdv2.intel.com/v1/dl/getContent/655258> |
| `other-9000-user-manual` | other | Nuvo-9000 Series User Manual | Rev. 1.4, July 2024 (distributor copy; neousys-tech.com answered 403) | July 2024 | PDF pp. 7, 58 | <https://elmark-automation.com/amfile/file/download/file/138702/product/72487> |
| `other-11` | other | GEEKOM MiniAir 11 product page | retrieved | 2026-11-17 | memory line | <https://www.geekompc.com/> |
| `other-2600-user-manual` | other | Nuvo-2600 Series User Manual | Rev. 1.0 | October 2022 | PDF pp. 11, 36, 47 | <https://nnz-ipc.ru/files/documentation/neousys/nuvo-2600-user-manual.pdf> |
| `other-1` | other | Intel Processor N-series (Alder Lake-N) Datasheet, Volume 1 | 759603, rev. 002 | January 2025 | PDF p. 159 | <https://cdrdv2.intel.com/v1/dl/getContent/759603> |
| `other-631121-012` | other | 11th Generation Intel Core Processor Datasheet, Volume 1 | 631121-012, May 2023 | May 2023 | PDF pp. 86-88 | <https://cdrdv2.intel.com/v1/dl/getContent/631121> |
| `other-user-guide` | other | COM-HPC-ALT User's Guide | P/N 50M-00115-1010, Rev. 1.1, 2025-07-30 | 2025-07-30 | PDF pp. 11, 13, 28, 69 | <https://www.adlinktech.com/Products/DownloadMDownload.aspx?lang=en&pdNo=1954&MainCategory=computer_on_modules&kind=M&isManual=yes> |
| `other-x300-x400-x500-user-manual` | other | Minisforum X300 / X400 / X500 user manual | PDF title "说明书 X500 2022.6.16", 24 pages, no text layer | 2022 | PDF p. 13 | <https://pc-file.s3.us-west-1.amazonaws.com/X300_X400_X500/user+manual/%E8%AF%B4%E6%98%8E%E4%B9%A6+X3-4-500+2022.6.16.pdf> |
| `gigabyte-r182-n20-r182-na0-user` | gigabyte | R182-N20 / R182-NA0 User Manual | Rev. 1.0 | 2021 | PDF pp. 31-33 | <https://download.gigabyte.com/FileList/Manual/server_manual_R182-N20_NA0_e_10.pdf> |
| `other-rs5230-user-manual-pr2710p-manual` | other | RS5230 User Manual (FS-branded edition of the Powerleader PR2710P manual; the text says "All Powerleader rack servers" and shows the "PR2710P-24-disk server") | PDF dated 2023-11-23, 59 pages | 2023-11-23 | PDF pp. 20-25 | <https://resource.fs.com/mall/doc/202311231103177tqpxp.pdf> |
| `quanta-d52b` | quanta | QuantaGrid Series D52B-1U User's Guide (copy on manualslib) | Version 1.0 | 2017 | PDF pp. 15, 22 | <https://www.manualslib.com/manual/3444966/Quanta-Computer-D52b-1u.html> |
| `other-2041-2122` | other | Graviton S2041IO and S2122IO server datasheets | undated |  | p. 1 | not recorded |
| `other-rx20-g2` | other | YADRO VEGMAN Rx20 G2 datasheet | v2.0 | 2026-05-14 | p. 2 | <https://st.yadro.com/docs/VEGMAN/YADRO_VEGMAN_Rx20-G2_DS_RUS.pdf> |
| `other-rs125-g4` | other | ETegro Hyperion RS125 G4 specification page | captured | 2013-02-17 | memory line | <https://web.archive.org/web/20130217123912/http://www.etegro.ru/configurator/servers/rackmount/base/rs125g4/spec> |
| `other-rs130-g4` | other | ETegro Hyperion RS130 G4 specification page | captured | 2012-11-28 | memory line | <https://web.archive.org/web/20121128030206/http://www.etegro.ru/configurator/servers/rackmount/performance/rs130g4/spec> |
| `sm-mnl-1628` | supermicro | X10DRH-C / X10DRH-CT / X10DRH-i / X10DRH-iT User's Manual | MNL-1628, Revision 2.0b | April 15, 2020 | pr. 2-13 to 2-15 (p35-37) | <https://www.supermicro.com/manuals/motherboard/C600/MNL-1628.pdf> |
| `tyan-s4985-user-manual` | tyan | Thunder K8QSD Pro S4985 user's manual (copy of tyan.com on web.archive.org) | Version 1.00 |  | p23-24 | <https://web.archive.org/web/20081010072845id_/http://www.tyan.com:80/manuals/m_s4985_100.pdf> |
| `tyan-m4985-user-manual` | tyan | M4985 processor and memory card manual (copy of tyan.com on web.archive.org) | Revision 1.00 |  | p16 | <https://web.archive.org/web/20100331082302id_/http://www.tyan.com:80/manuals/m_m4985_100.pdf> |
| `msi-ms9661-manual` | msi | MS-9661 server board user's guide (text copy on manualslib) |  |  | p20 | <https://www.manualslib.com/manual/3885410/Msi-Ms-9661.html?page=20> |
| `gigabyte-mz31-ar0-user-manual` | gigabyte | MZ31-AR0 User Manual | Rev. 2.0 (2019) and Rev. 3.0, 2021 | 2019 | p. 13-15 | <https://download.gigabyte.com/FileList/Manual/server_manual_mz31-ar0_e_v20.pdf> |
| `gigabyte-r162-za0-user-manual` | gigabyte | R162-ZA0 User Manual | Rev. 1.0 | 2021 | p. 29-31 | <https://download.gigabyte.com/FileList/Manual/server_manual_R162-ZA0_e_A00.pdf> |
| `gigabyte-r262-za1-za2-user-manual` | gigabyte | R262-ZA1 / R262-ZA2 User Manual | Rev. 1.0 | 2021 | p. 36-38 | <https://download.gigabyte.com/FileList/Manual/server_manual_R262-ZA1_ZA2_e_A00.pdf> |
| `gigabyte-mz72-hb0-user-manual` | gigabyte | MZ72-HB0 User Manual | Rev. 1.0 (2020) and Rev. 3.0, 2021 | 2020 | p. 13-15 | <https://download.gigabyte.com/FileList/Manual/server_manual_MZ72-HB0_e_v10.pdf> |
| `huawei-rh2288-v3-user-guide` | huawei | FusionServer RH2288 V3 Server User Guide | Issue 04 | 2023-02-28 | pdf p. 67, 215-216 | not recorded |
| `lenovo-thinkstation-px-user-guide` | lenovo | ThinkStation PX User Guide | Seventh Edition | June 2025 | p. 8, 87-88 | not recorded |
| `hp-z6-g4-tech-wp` | hp | HP Z6 G4 Workstation Technical White Paper | 4AA7-1335ENW | January 2021 | pp. 20-21 | <https://web.archive.org/web/20220216015225/https://h20195.www2.hp.com/v2/getpdf.aspx/4AA7-1335ENW.pdf> |
| `hp-z6-g4-quickspecs` | hp | HP Z6 G4 Workstation QuickSpecs | c05527761, DA-15955, version 59 | April 1, 2024 | pp. 14, 21-23 | <https://web.archive.org/web/20251122031240id_/https://www8.hp.com/h20195/v2/getpdf.aspx/c05527761.pdf> |
| `hp-z6-g4-msg` | hp | Maintenance and Service Guide HP Z6 G4 Workstation (syndication copy of the PDF) | part number 932089-002, second edition, November 2017 | November 2017 | pp. 69-72, 79 | <https://web.archive.org/web/20240730122704id_/http://content.etilize.com/User-Manual/1045331998.pdf> |
<!-- /documents -->

### Memory on the chipset

Servers with front-side-bus processors (Xeon 5000 to 5400, 3000 to 3300 and 7200 to 7400, the NetBurst Xeon, Core 2) keep the memory controller in the chipset: no processor owns a slot. The board draws one controller, `chipset`, in the place of the CPUs, and the channels and slots per channel come from the family of the board (four channels of two slots on a Dell PowerEdge 2950, for example). A front-side-bus board without a family row has no channel count, and a line says that the channels could not be checked.

### The checks

What the names and the tables gave is checked before the board is drawn:

- The sockets run from 0 without a gap, are no more than the processor records of the board and no more than 8. Otherwise every slot is set aside.
- No two slots stand in one place. Otherwise every slot is set aside: the names cannot tell the two apart.
- No socket has more channels than its processor (46 processor families, by name or, for a blank or generic name, by the CPUID signature) or its family row gives. Otherwise that socket is drawn without channels.
- No channel holds more slots than its platform: 2 or 1 where the processor table says so, 3 on the other DDR3, DDR4 and DDR5 platforms, and the number a document gives for an older platform that holds more (4 on Opteron of Socket F and 940; 4 on the FB-DIMM channels of the Intel S5000 boards and 8 per riser channel of the Dell PowerEdge R900, through their family rows). Otherwise that socket is drawn without channels.
- A slot number without a channel on a board without a family table: its socket is drawn without channels.
- A processor of no row of the processor table: the board is drawn as the names say, and a line says that the channels could not be checked.
- A module on a socket whose processor record says that no processor is installed: drawn as named, and a line says so.

A slot set aside is drawn below the board, as the slot map draws it, with the reason and an example of its names in the lines of data quality.

### Fixtures

The rows below are what the board draws of the first slot of each fixture under `testdata/explorer`; a test holds the table to the fixtures.

| Names | Locator | Bank locator | Board | Fixture |
| --- | --- | --- | --- | --- |
| Supermicro: `P` socket, node and channel in the bank locator | `P1-DIMMA1` | `P0_Node0_Channel0_Dimm0` | CPU 0, channel 0 | `linuxhw-sys-6028r-wtr-fac06e` |
| Supermicro with one processor word: socket from the bank locator, channel letter from the locator | `DIMMA2` | `P0_Node0_Channel0_Dimm1` | CPU 0, channel 0 | `netbox-sys-5039ms-h12trf-os012` |
| Huawei, Sugon, Inspur, Lenovo RD450X: node and channel in the bank locator | `DIMM000` | `_Node0_Channel0_Dimm0` | CPU 0, channel 0 | `linuxhw-1288h-v5-c98b42` |
| socket, channel and slot as three digits | `DIMM000` | `Not Specified` | CPU 0, channel 0 | `lab-digits` |
| Dell PowerEdge: a letter and a slot number | `A1` | `Not Specified` | CPU 0, no channels | `netbox-dell-poweredge-m630` |
| older Dell PowerEdge: the letter is the processor, the table of the family gives the channel | `DIMM_A1` | `Not Specified` | CPU 0, channel 0 | `dselans-dell-centos65` |
| HPE ProLiant: processor and a running slot number, the table of the family gives the channel | `PROC 1 DIMM 1` | `Not Specified` | CPU 0, channel 2 | `netbox-hp-bl460c-gen10` |
| HPE ProLiant Gen8, two spaces | `PROC  1 DIMM  1` | `Not Specified` | CPU 0, channel 2 | `netbox-hp-dl380p-gen8` |
| HPE ProLiant blade without a table of its family | `PROC 1 DIMM 8` | `Not Specified` | CPU 0, no channels | `netbox-hp-proliant-bl460c-gen10-graphics-exp` |
| Intel and ASUS boards: `CPU` and a channel letter | `CPU0_DIMM_A1` | `NODE 0` | CPU 0, channel 0 | `linuxhw-m50cyp2sbstd-d50fc4` |
| `CPU` counted from 1 and a channel letter, bank locator `P0 CHANNEL A` | `CPU1_DIMM_A1` | `P0 CHANNEL A` | CPU 0, channel 0 | `linuxhw-rs720a-e12-rs12-6002d3` |
| Intel reference boards, Inspur M5 and M6: `_C` channel `D` slot | `CPU0_C0D0` | `NODE 0` | CPU 0, channel 0 | `linuxhw-m70klp2sb-m90598-001-373530` |
| `_CH` channel `_DIMM` slot, on two sockets | `CPU0_CH0_DIMM0` | `Not Specified` | CPU 0, channel 0 | `lab-two-sockets` |
| Intel S2600: node, channel and slot in the bank locator | `DIMM_A1` | `NODE 0 CHANNEL 0 DIMM 0` | CPU 0, channel 0 | `linuxhw-s2600gz-925442` |
| AMD boards: `P` socket and a channel letter in the bank locator | `DIMM 0` | `P0 CHANNEL A` | CPU 0, channel 0 | `linuxhw-genoad12m3-2q-h-af39b8` |
| Gigabyte: `DIMM_P` socket, channel letter | `DIMM_P0_A0` | `BANK 0` | CPU 0, channel 0 | `linuxhw-g482-z54-00-7ca16e` |
| one socket: `ChannelA-DIMM0` | `ChannelA-DIMM0` | `BANK 0` | CPU 0, channel 0 | `ironlib-dmidecode` |
| one socket: channel letter in the bank locator | `DIMM A1` | `CHANNEL A` | CPU 0, channel 0 | `netbox-qct-x10e-9n` |
| one socket: `DIMM` and a channel letter | `DIMMA1` | `Not Specified` | CPU 0, channel 0 | `lab-letters` |
| lshw only, no SMBIOS text: four slots of one letter | `A0` | | CPU 0, no channels | `coreboot-ga-g41m-es2l` |
| Lenovo ThinkSystem and System x: `DIMM n`, socket in the bank locator, the table of the family gives the channel | `DIMM 1` | `CPU 1` | CPU 0, channel 2 | `linuxhw-thinksystem-sr650-7x06cto1ww-5d8ab0` |
| node, channel and slot in the locator, node in the bank locator | `NODE 0 CHANNEL 0 DIMM 0 (C0A0)` | `NODE 0` | CPU 0, channel 0 | `linuxhw-9k-23ba3b` |
| memory controller and channel letter in the locator, node in the bank locator | `P0_MC0_DIM_CH_A0` | `NODE 1` | CPU 0, channel 0 | `linuxhw-b7109f77dv14hr-2t-n-91971d` |
| `ChannelA-DIMM0` with `NODE 0` to `NODE 7` on two sockets: four memory controllers to a processor | `ChannelA-DIMM0` | `NODE 0` | CPU 0, channel 0 | `linuxhw-r1g3d32-ec54ac` |
| IBM System x: numbers that run over both processors, the table of the family gives processor and channel | `DIMM 1` | `Bank 1` | CPU 0, channel 0 | `linuxhw-system-x3550-m4-7914b2a-05b91b` |
| empty slots the firmware gives no name, on a board without the labels of its manual | `NO DIMM` | `NO DIMM` | set aside | `linuxhw-sa5212m4-4250b0` |
| a name of no form beside a channel and slot in the bank locator, on one socket | `J1MY` | `CHAN A DIMM 0` | set aside | `ironic-dmi-data` |

### The public collection

The server dumps of the Linux-Hardware.org DMI collection ([linuxhw/DMI](https://github.com/linuxhw/DMI), commit `57b21bc`, CC BY 4.0) are 2,044 dumps, of which 2,043 dumps with memory devices, in 1,711 distinct slot lists (`testdata/explorer/population/slots.tsv`, each list as the ledger keeps the dump). A dump is placed in full when every slot has a processor, a channel and a position from its own names or from the table of its family, every check holds, the channel count of the processor is known and no module stands on an absent processor. A test holds every slot to its expected place, and every dump to being placed in full or listed with its reason in `testdata/explorer/population/unplaceable.tsv`. The first two columns count the boards whose every slot the readers of the first version and of the first round of fixes put on a CPU and a channel, by their own checks.

<!-- population -->
| Maker | Dumps | Every slot placed, first version | Every slot placed, first round of fixes | Placed in full | Separate list |
| --- | ---: | ---: | ---: | ---: | ---: |
| Supermicro | 550 | 412 | 413 | 525 | 25 |
| Dell | 476 | 187 | 390 | 465 | 11 |
| other | 338 | 124 | 213 | 297 | 41 |
| HP | 270 | 235 | 235 | 251 | 19 |
| Intel | 103 | 16 | 94 | 103 | 0 |
| Lenovo | 52 | 8 | 8 | 49 | 3 |
| ASUS | 44 | 11 | 24 | 44 | 0 |
| IBM | 43 | 0 | 1 | 40 | 3 |
| Gigabyte | 40 | 11 | 40 | 40 | 0 |
| HPE | 36 | 32 | 33 | 36 | 0 |
| Fujitsu | 33 | 5 | 7 | 33 | 0 |
| Huawei | 27 | 27 | 27 | 26 | 1 |
| Quanta | 11 | 4 | 7 | 10 | 1 |
| Tyan | 7 | 0 | 0 | 2 | 5 |
| Inspur | 5 | 4 | 4 | 4 | 1 |
| ASRock | 4 | 0 | 4 | 4 | 0 |
| Cisco | 4 | 0 | 1 | 4 | 0 |
| Sugon | 1 | 1 | 1 | 1 | 0 |
| all | 2,044 | 1,077 | 1,502 | 1,934 | 110 |

| Reason | Dumps |
| --- | ---: |
| `no-channel-in-document` | 38 |
| `unnamed-empty` | 27 |
| `duplicated-names` | 12 |
| `board-unknown` | 10 |
| `names-not-in-document` | 6 |
| `no-document` | 5 |
| `absent-processor` | 4 |
| `no-processor-in-document` | 4 |
| `processor-unknown` | 2 |
| `no-memory-device` | 1 |
| `processor-records-missing` | 1 |

| Socket count unknown | Every slot placed | Some placed | Without channels | All set aside | A CPU the dump lacks |
| --- | ---: | ---: | ---: | ---: | ---: |
| first version | 1,135 | 50 | 229 | 629 | 5 |
| first round | 1,567 | 48 | 239 | 189 | 8 |
| this release | 1,665 | 25 | 6 | 347 | 1 |
<!-- /population -->

The reasons of the separate list:

| Reason | What the dump shows |
| --- | --- |
| `no-channel-in-document` | the maker's document gives banks, pairs or slot numbers and no channel for each slot (HP ProLiant G4 and G5, Sun Fire X2200 M2, IBM xSeries 235, Lenovo ThinkSystem SR250) |
| `unnamed-empty` | empty slots the firmware names `NO DIMM` or not at all (Supermicro X10 boards, Inspur SA5212M4); where the family lists the labels of the manual they are drawn with them, marked |
| `duplicated-names` | the firmware writes one name for two slots |
| `board-unknown` | the records name no model, and a part number of the board ties to no document |
| `names-not-in-document` | the firmware's slot names and the labels of the document do not meet |
| `no-document` | no public document of the board was found |
| `no-processor-in-document` | no document says which memory riser serves which processor (Dell PowerEdge R910) |
| `absent-processor` | modules on a socket whose processor record says not installed |
| `processor-unknown` | the processor is in no row of the processor table (AppliedMicro X-Gene 1, HiSilicon Hi1612) |
| `processor-records-missing` | one processor record, and the family table puts slots on a second processor |
| `no-memory-device` | the dump has no SMBIOS memory device |

`internal/explorer/testdata/population-boards.txt` holds the same counts. `testdata/explorer/population/sample.tsv` holds slots of one dump of every family placed by hand from the document, with its page; a test holds the board to them.

### What the board cannot know

- A family whose document was not found: its numbered slots are drawn without channels, or the board is set aside.
- Firmware that names no empty slot: such slots stand below the board, or with the labels of the manual where the family lists them.
- A ledger written before the processor records were kept, or text of `dmidecode -t memory`: no processor name, signature or populated flag. The channel count cannot be checked and the absent check is skipped; the CPU packages of the cache reading stand in for the socket count. One public board (an IBM System x3650 M2 whose firmware writes one processor record) is then drawn with a CPU its records lack.
- The documents are the revisions cited; a later revision with another table is not known.
- No board was compared with a real server. A wrong row of a family table draws a module in the wrong channel of the right processor, and no check sees that.
- Three readings rest on the dumps alone: the letters of the ETegro Hyperion RS125 G4 split over two processors as the guide of its Quanta sibling board draws them; `Board <c>` of the HP DL580 G7 read as memory cartridge c; `P0_` of the Supermicro X8DTU read as the second processor, as the order of the records and one dump with one processor show.

## Memory Explorer readings

Adapter `host-memory`, package `internal/adapter/hostmem`. It keeps texts about the memory of a host for the board and for section 7 of the report: no error record and no grade comes from them. The source inventory has one entry for it, with a note for every reading it did not find. A copy of a host's files without any of these files gets no entry.

| Reading | Running host | Copy of a host's files, the first that exists | Kept |
| --- | --- | --- | --- |
| `meminfo` | `/proc/meminfo` | `proc/meminfo` | 256 KiB |
| `vmstat` | `/proc/vmstat` | `proc/vmstat` | 256 KiB |
| `pressure-memory` | `/proc/pressure/memory` | `proc/pressure/memory` | 4 KiB |
| `slabinfo` | `/proc/slabinfo`, readable by root only | `proc/slabinfo` | the head line and the 20 caches that take the most memory, of at most 4 MiB read |
| `cpu-cache` | `level`, `type` and `size` of `sys/devices/system/cpu/cpuN/cache/index*` for the first CPU of each package | the same files | 64 KiB |
| `numactl-hardware` | `numactl --hardware`, 5 seconds | `numactl-hardware.txt`, `sos_commands/numa/numactl_--hardware` | 256 KiB |
| `numastat` | `numastat` with `NUMASTAT_WIDTH=4096`, so that one block holds every node, 5 seconds | `numastat.txt`, `sos_commands/numa/numastat` | 256 KiB |
| `sar-b` | `sar -B`, the paging table of the day's file of sysstat, 15 seconds | `sar-B.txt` | 1 MiB |
| `lru-gen-enabled` | `/sys/kernel/mm/lru_gen/enabled` | the same path | 64 bytes |
| `lshw` | only `my collect host` runs `lshw -c memory -c processor`, 15 seconds | `lshw.txt`, `sos_commands/hardware/lshw` | 4 MiB |

The names under `sos_commands` are those an sos report writes. An sos report has no `sar -B` text: its `sar` files hold every table, and only `sar-B.txt` is read. A scan runs `numactl`, `numastat` and `sar` when they are installed; `my serve` and `my harvest` run none of them. A program that is missing, fails or does not answer in time leaves its reading out with a note. Text that is not UTF-8 is kept with the invalid bytes replaced.

What a missing reading costs:

| Missing | Effect |
| --- | --- |
| `meminfo` | no overview tab |
| `slabinfo` | no slab tab |
| `vmstat` and `sar-b` | no page tab; with one of them the tab shows what it holds |
| `pressure-memory` | the pressure card of the overview says that it was not read |
| `numactl-hardware` | no NUMA tab |
| `numastat` | the NUMA tab has sizes and no hit, miss or locality counts |
| `lru-gen-enabled` | no MGLRU state |
| `cpu-cache` and `lshw` | the CPUs of the board carry no cache sizes |
| `lshw`, with no SMBIOS slot | no board |

The data quality part of the report names every reading the ledger lacks.

## Module SPD

Source `spd`, packages `internal/decode/spd` (the decoder) and `internal/adapter/dimmspd` (the reader of a host). The SPD EEPROM of a memory module holds what the module maker wrote about it: memory and module type, die capacity and package, ranks and device width, the codes of the module and DRAM makers, the manufacturing location and week, serial and part number. SMBIOS gives most of these and neither the week nor the device width. The SPD gives no error record and no grade.

### When Linux gives the SPD

Read in the sources of Linux 7.3 (`drivers/i2c/i2c-smbus.c`, `drivers/i2c/busses/i2c-i801.c` and `i2c-piix4.c`, `drivers/nvmem/ee1004.c`, `drivers/hwmon/spd5118.c`). Linux gives the SPD of a module only when all of these hold:

1. a driver of the SMBus controller is bound: `i2c_i801` (Intel) or `i2c_piix4` (AMD);
2. the kernel made an SPD client and its driver is bound: `ee1004` for DDR4, `spd5118` for DDR5, `at24` for older types. The SMBus driver makes the clients itself from the SMBIOS memory devices: at most 8 slots, at addresses 0x50 to 0x57, none when memory types are mixed, and on i801 not behind a multiplexer;
3. the BMC does not hold the bus and firmware does not hide the controller;
4. the host is no virtual machine.

On a board with more than eight slots, an SMBus that the BMC owns, or SPD wired to a bus of the processor's memory controller, the kernel has no client to read. That is the common case on servers; there MemorY names the reason and reads a dump given to it.

### What is read on a host

Source `spd`. Only files, only for reading, below `/sys` and `/lib/modules`; no root is needed for the SPD itself:

| File | For |
| --- | --- |
| `/sys/bus/i2c/devices/i2c-N/name` | the adapters |
| `/sys/bus/i2c/devices/N-00AA/name`, the link `driver`, `eeprom` (AA 50 to 57) | the SPD clients, whether a driver is bound, the SPD (mode 0444) |
| `/sys/bus/nvmem/devices/N-00AA/nvmem` | the same bytes, where a bound client has no `eeprom` file |
| `/sys/bus/i3c/devices/*/eeprom` of a device bound to `spd5118_i3c` | DDR5 hubs on I3C; no source confirms the path there, so it is tried and never required |
| `/sys/bus/i2c/drivers/{ee1004,spd5118,at24}`, `/sys/bus/pci/drivers/{i801_smbus,piix4_smbus}` | whether a driver is registered (built in or loaded) |
| `/sys/bus/pci/devices/*/class`, `vendor`, `device`, the link `driver` | the SMBus controllers (class 0x0c05) |
| byte 0x40 of `/sys/bus/pci/devices/<Intel SMBus>/config` | SPD write disable (bit 4); read only by a scan as root |
| `/lib/modules/<release>/modules.dep`, `modules.builtin` | whether a module that is not loaded exists for this kernel |
| `/sys/class/ipmi`, `/sys/firmware/dmi/entries/38-*` | whether the host has a BMC |

MemorY runs no program for the SPD, loads no module, writes no file, opens nothing under `/dev` (no `/dev/i2c-*`), and reads no hwmon file of `spd5118`: reading an alarm file there clears the alarm. `TestReasonsAndStates` runs the reader over a recording file system that fails it for a name outside this list.

An `eeprom` file is read in steps of 128 bytes, one read per step, up to the size the file reports and at most 2,048 bytes. 128 bytes is one page of `spd5118` and half a page of `ee1004`, so a step never crosses a page. For the kernel a read past the first page is a page command to the EEPROM or hub; it changes a volatile register and no stored byte. When a step fails, the bytes before it are kept: the first 128 bytes of DDR5 still give the device width. All readings of a host share one deadline of 5 seconds; a read the kernel does not return in time is left behind and the readings after it are not made.

A virtual machine is not probed: the probe ends at its first line. `MY_DEBUG=spd-vm` lets it run there, for a lab.

What the reader saw is kept as a probe, a reading of kind `spd-probe` in lines of MemorY's own form; `my collect host` writes it as `spd-probe.txt`:

```
spd-probe 1
i2c present
adapter i2c-0 "SMBus I801 adapter at 0000:00:1f.4"
pci 0000:00:1f.4 id=8086:a323 driver=i801_smbus spd-wd=0
driver i801_smbus registered
driver ee1004 module
client 0-0050 name=ee1004 driver=ee1004 eeprom=512 read=512 state=ok
client 0-0051 name=ee1004 driver=- eeprom=- read=0 state=unbound
bmc present
```

A driver is `registered`, `module` (not registered, a module file is listed for this kernel), `absent`, or `unknown` (the module lists could not be read). `spd-wd` is there only for a scan as root.

### When it is read

| Command | SPD |
| --- | --- |
| `my` and every view on the running host, `my bundle`, `my report`, `my collect host` | once per run; `--no-spd` leaves it out, and then there is no `spd` source |
| `my serve` | in the first scan; the later scans keep what it read and leave the bus alone |
| `my harvest` | never |
| `--root DIR`, `my import DIR` | the files `spd-probe.txt` and `spd/*` of a collection, nothing under `sys` of a copy; dumps in any form may be put into `spd/` |

`my collect host` writes `spd-probe.txt` and one file `spd/<bus>-<address>.bin` per SPD device with the bytes read, and lists each in `collect.json` with its state. No SPD step makes a collection partial or sets its exit code.

### Reasons

A host has a reason only when no reading has the state `ok`, `crc` or `short`. The reason is computed from the probe and the memory type of SMBIOS each time a view is built, so a later version reads the reason of an older case again. The tests run in this order:

<!-- spd-reasons -->
| Code | Read to tell | Short text |
| --- | --- | --- |
| `virtual-machine` | the hypervisor that the host facts name (DMI strings, the CPU flag) | virtual machine |
| `no-i2c` | `/sys/bus/i2c` is missing | no I2C bus in this kernel |
| `smbus-driver-not-loaded` | a PCI device of class 0x0c05 without a `driver` link, its driver not registered, a module file listed | SMBus driver i2c_i801 not loaded |
| `smbus-driver-not-built` | the same, and no module file listed | kernel built without i2c_i801 |
| `smbus-driver-unknown` | the same, and the module lists of the kernel could not be read | SMBus controller without driver |
| `smbus-driver-refused` | the same device, its driver registered | SMBus driver did not bind the controller |
| `no-smbus-controller` | no adapter holds an SPD client, and no PCI device has class 0x0c05 | no SMBus controller on PCI |
| `spd-driver-not-loaded` | clients named `ee1004`, `spd5118` or `spd` without a `driver` link, the driver not registered, a module file listed | SPD driver ee1004 not loaded |
| `spd-driver-not-built` | the same, and no module file listed | kernel built without ee1004 |
| `spd-driver-unknown` | the same, and the module lists could not be read | SPD device without driver |
| `spd-driver-refused` | the same clients, the driver registered | SPD driver did not bind |
| `write-disabled` | a scan as root read SPD write disable set, the modules are DDR5 (clients named `spd5118`, or the memory type of SMBIOS), and no client is bound | SPD pages blocked by firmware |
| `no-spd-device` | SMBus controllers with a driver, and no client at 0x50 to 0x57 named `ee1004`, `spd5118` or `spd` | no SPD device on the bus |
| `no-eeprom-file` | clients with a bound driver and no `eeprom` or `nvmem` file | SPD driver gives no eeprom file |
| `read-denied` | every open or read of an `eeprom` file ended in EACCES or EPERM | permission denied |
| `read-failed` | the reads of the bound clients ended in errors of more than one kind, in EIO, ENXIO, or the deadline | read failed |
| `not-decoded` | every reading has a memory type other than DDR4 and DDR5 | memory type not decoded |
| `blank` | every reading holds only 0x00 or only 0xFF in its first 128 bytes | only 0x00 or 0xFF bytes |
<!-- /spd-reasons -->

With SPD write disable set and DDR5 modules, `write-disabled` takes the place of `spd-driver-refused` and `no-spd-device`; a scan without root cannot read the setting. A DDR5 reading that ends after 128 bytes is a reading of state `short`, so its host has no reason. A reason is no fault: the source `spd` is `absent` where the probe lists no SPD client, `empty` where clients are there and gave nothing, and `ok` where a reading exists. It is never partial, unreadable or in error, so it lowers the confidence of no finding.

### States of one reading

| State | When |
| --- | --- |
| `ok` | read in full, base CRC good; the DIMM row of the ledger can take its fields |
| `crc` | base CRC bad; every field is shown with the two CRC values, nothing enters the DIMM row |
| `short` | fewer bytes than a decoded field needs; a reading without its serial bytes is tied to no DIMM |
| `blank` | the first 128 bytes are all 0x00 or all 0xFF |
| `type` | byte 2 is another memory type, or none |
| `denied`, `io`, `timeout` | the read failed before the first byte |

### Input forms

`my spd FILE` and `my import` read an SPD in these forms. A file compressed with gzip is read through.

| Form | Recognised by | Example line |
| --- | --- | --- |
| binary | 128 to 4,096 bytes, byte 2 a memory type of the key byte table | the `eeprom` file of the kernel, the file of a programmer or of a vendor tool |
| `hexdump -C` | rows of an offset and 16 bytes, an ASCII column after `\|` | `` 00000000  23 12 0c 01 86 31 00 08  00 60 00 03 08 0b 80 00  \|#....1...`......\| `` |
| `od -A x -t x1` | rows of an offset and 16 bytes | `000000 23 12 0c 01 86 31 00 08 00 60 00 03 08 0b 80 00` |
| `i2cdump` | rows of a two-digit offset with a colon and 16 bytes, an ASCII column | `` 00: 23 12 0c 01 86 31 00 08 00 60 00 03 08 0b 80 00    #?????.?.`.????? `` |
| the dumps of the illumos tests | rows of a four-digit offset with a colon, `#` comment lines | `0000: 23 12 0C 04 85 29 B2 08 00 60 00 03 08 0B 80 00` |
| `xxd` | rows of an offset with a colon and 8 groups of four digits, in file order | `` 00000000: 2312 0c01 8631 0008 0060 0003 080b 8000  #....1...`...... `` |
| `hexdump` | rows of an offset and 8 groups, the two bytes of each swapped on a little-endian machine | `0000000 1223 010c 3186 0800 6000 0300 0b08 0080` |
| plain hex | two-digit bytes or runs of hex digits without offsets | `23 12 0C 01 86 31 00 08 00 60 00 03 08 0B 80 00` |
| table with an offset column | 17 two-digit columns whose first counts 00, 10, 20 and so on, as some Windows tools print the SPD | `10 00 00 05 0D F8 FF 02 00 6E 6E 6E 11 00 6E F0 0A` |
| `decode-dimms` output | a line `Decoding EEPROM:` and the line `---=== SPD EEPROM Information ===---` per module | `Decoding EEPROM: /sys/bus/i2c/drivers/ee1004/0-0050` |

An offset is 2 to 8 hex digits with a colon, or 4 to 8 without one, so a line of two-digit bytes is plain hex. A row holds at most 16 bytes or 8 groups; what follows them, or a `|`, is the ASCII column. `*` repeats the row before it up to the offset of the next row, or up to a closing line that holds an offset alone, as `hexdump` and `od` end. Lines that start with `#`, lines before the first row and CRLF line ends are read past. A row may start with `0000 `, which decode-dimms allows for `eeprog`; no `eeprog` text was seen. Groups of four digits are read in both byte orders, and the order whose byte 2 is DDR4 or DDR5 with a good base CRC is taken; the reading says which.

The bytes of hex text end at the first offset its rows leave out, or at a row that goes back to an earlier offset (a second dump in the same text); the decode of such bytes is `short`. A text over 64 KiB, or with more than 4,096 bytes, is no SPD. Of `decode-dimms` output the default form is read, not the HTML of `-f` and not `--side-by-side`; the fields it prints are taken as printed, and it holds no bytes.

`my import` guesses the kind of every file, and takes a binary file for an SPD only when its base CRC is good. `my spd FILE` is told what the file is and also decodes one with a bad CRC.

### Bytes that are decoded

The offsets and codings are those of JEDEC Standard 21-C, Annex L (SPD for DDR4 SDRAM modules) and of JESD400-5 (DDR5 SPD contents). The standards were not read: they need a JEDEC account. The tables come from two public decoders that cite their sections and agree, `spd_ddr4.h` and `spd_ddr5.h` of illumos libjedec (illumos-gate `4a58223`, CDDL-1.0) and `decode_ddr4_sdram` of `decode-dimms` (i2c-tools, GPL-2.0-or-later), and were checked against the dumps below. MemorY writes them in tables of its own and copies no code of either.

DDR4:

<!-- spd-ddr4 -->
| Byte | Bits | Field | Coding |
| --- | --- | --- | --- |
| 1 | 7-4, 3-0 | SPD revision | major, minor |
| 2 |  | memory type | 0x0C DDR4, 0x12 DDR5; other key bytes are named and not decoded |
| 3 | 3-0, 7 | module type, hybrid | 1 RDIMM, 2 UDIMM, 3 SODIMM, 4 LRDIMM, 5 Mini-RDIMM, 6 Mini-UDIMM, 8 72b-SO-RDIMM, 9 72b-SO-UDIMM, 12 16b-SO-DIMM, 13 32b-SO-DIMM |
| 4 | 3-0 | capacity of a die | 0 256 Mb, doubling up to 7 32 Gb; 8 12 Gb, 9 24 Gb |
| 5 | 5-3, 2-0 | row and column address bits | 12 + n, 9 + n |
| 6 | 7, 6-4, 1-0 | package | monolithic or not; 1 + n dies; signal loading 1 multi-load stack, 2 single-load stack (3DS) |
| 12 | 6, 5-3, 2-0 | rank mix, package ranks, device width | symmetric or asymmetric; 1 + n; 0 x4, 1 x8, 2 x16, 3 x32 |
| 13 | 4-3, 2-0 | bus width extension, primary bus width | 0 none, 1 8 bits; 0 8, 1 16, 2 32, 3 64 bits |
| 18, 125 |  | minimum cycle time (tCKAVGmin) | byte 18 in units of 125 ps plus byte 125 as a signed number of ps |
| 126-127 |  | CRC of bytes 0-125 | low byte first |
| 254-255 |  | CRC of bytes 128-253 | low byte first |
| 320-321 |  | module maker | continuation count with parity, then the code |
| 322 |  | manufacturing location | the maker's own coding |
| 323, 324 |  | manufacturing year, week | BCD |
| 325-328 |  | serial number | 4 bytes, shown as 8 hex digits in stored order |
| 329-348 |  | part number | 20 ASCII characters, padded with spaces |
| 349 |  | module revision |  |
| 350-351 |  | DRAM maker | as bytes 320-321 |
| 352 |  | DRAM stepping |  |
<!-- /spd-ddr4 -->

Logical ranks are package ranks times dies for a 3DS package and package ranks otherwise. Module size in bytes is die capacity / 8 x primary bus width / device width x logical ranks. The organisation is package ranks and device width, as `2Rx4`. The rated speed is 2,000,000 / tCKAVGmin in ps, set to the nearest step of 400/3 MT/s: 625 ps gives 3200, 750 ps 2666.

DDR5:

<!-- spd-ddr5 -->
| Byte | Bits | Field | Coding |
| --- | --- | --- | --- |
| 0 | 6-4 | size of the SPD device | 1 256, 2 512, 3 1,024, 4 2,048 bytes |
| 1 | 7-4, 3-0 | SPD revision | major, minor |
| 2 |  | memory type | 0x12 |
| 3 | 3-0, 7 | module type, hybrid | 1 RDIMM, 2 UDIMM, 3 SODIMM, 4 LRDIMM, 5 CUDIMM, 6 CSODIMM, 7 MRDIMM, 8 CAMM2, 9 SOCAMM2, 10 DDIMM, 11 solder down |
| 4 | 7-5, 4-0 | dies per package, capacity of a die | 0 one die, 1 two (DDP), 2 two, 3 four, 4 eight, 5 sixteen (3DS); 1 4 Gb, 2 8, 3 12, 4 16, 5 24, 6 32, 7 48, 8 64 Gb |
| 5 | 7-5, 4-0 | column and row address bits | 10 + n, 16 + n |
| 6 | 7-5 | device width | 0 x4, 1 x8, 2 x16, 3 x32 |
| 20-21 |  | minimum cycle time (tCKAVGmin) | ps, low byte first |
| 234 | 6, 5-3 | rank mix, package ranks per sub-channel | symmetric or asymmetric; 1 + n |
| 235 | 7-5, 4-3, 2-0 | sub-channels, bus width extension, primary bus width per sub-channel | 1 + n; 0 none, 1 4, 2 8 bits; 0 8, 1 16, 2 32, 3 64 bits |
| 510-511 |  | CRC of bytes 0-509 | low byte first |
| 512-513 |  | module maker | as DDR4 |
| 514 |  | manufacturing location |  |
| 515, 516 |  | manufacturing year, week | BCD |
| 517-520 |  | serial number | 4 bytes |
| 521-550 |  | part number | 30 ASCII characters |
| 551 |  | module revision |  |
| 552-553 |  | DRAM maker | as bytes 512-513 |
| 554 |  | DRAM stepping |  |
<!-- /spd-ddr5 -->

Module size in bytes is sub-channels x primary bus width / device width x dies per package x die capacity / 8 x package ranks. The rated speed is 2,000,000 / tCKAVGmin, set to the nearest 400 MT/s: 416 ps gives 4800. Logical ranks count the dies of a 3DS package. A module with rank mix set is marked asymmetric, and its organisation and size are those of its first kind of rank. Of a DDR5 SPD whose first 128 bytes alone were read, the fields of bytes 0 to 21 are decoded: device width and die are known, ranks, CRC, date and identity are not.

Every other memory type is named by its key byte (byte 2) and not decoded: `FPM DRAM`, `EDO`, `SDRAM`, `DDR`, `DDR2`, `DDR3`, `DDR4E`, the LPDDR types and the others of the table in `internal/decode/spd/spd.go`.

The date is two BCD digits of year (2000 to 2099) and week (1 to 53). Bytes that are no such digits make no date; the two bytes are shown as stored (`0xDAAD`). decode-dimms then tries the bytes as binary numbers, and MemorY does not, so that the table of date codes holds no guessed week. The part number ends at the first NUL; one that holds a byte outside printable ASCII is not shown.

### CRC

CRC-16 with the polynomial 0x1021, start value 0, no reflection, stored low byte first: of DDR4 bytes 0-125 in 126-127 and bytes 128-253 in 254-255, of DDR5 bytes 0-509 in 510-511. The manufacturing block (DDR4 bytes 320 to 383, DDR5 512 to 554) is under no CRC.

A decode whose base CRC is bad has the state `crc`: its fields are shown with the stored and the computed value, and nothing of it enters the DIMM row of the ledger. A bad CRC of DDR4 bytes 128-253 is a note; MemorY reads no byte of that block.

### Maker codes

The codes of the module maker and of the DRAM maker are those of JEDEC JEP106, the list of manufacturer identification codes that JEDEC publishes. MemorY writes them as SMBIOS 3.2 does in `internal/adapter/smbios`: the bank (the continuation count plus one) and the code byte as stored, with its parity bit, `bank 1, 0xCE`. The two forms compare as text.

`internal/decode/spd/makers.tsv` names the codes found on server memory modules, makers of DRAM, of modules, of SPD hubs, of registers and data buffers and of PMICs: 60 rows, each checked against the vendor tables of `decode-dimms` (i2c-tools commit `dcc0e69`, GPL-2.0-or-later) and of illumos libjedec (`libjedec.c`, illumos-gate `4a58223`, CDDL-1.0), which agree on every row. MemorY wrote the rows from those facts and copies neither table. A maker is shown with its code in brackets, `Samsung (JEDEC bank 1, 0xCE)`; a code without a row is shown alone. `decode-dimms` output gives names and no codes: MemorY shows the names it prints.

### What was checked

| Dumps | Source, licence | Checked against |
| --- | --- | --- |
| 4 DDR4 (RDIMM 2Rx4, LRDIMM 3DS, UDIMM, SODIMM) and 2 DDR5 RDIMM (2Rx4, 1Rx8) | illumos-gate `4a58223`, CDDL-1.0 | the expected values of the illumos libjedec tests (`libjedec_hex2spd_ddr4.c`, `libjedec_hex2spd_ddr5.c`): type, module type, revision, rows and columns, die, package, width, ranks, bus widths, tCKAVGmin, serial, part number, revision, stepping, maker codes, year and week where the tests hold them |
| 2 DDR5 UDIMM 1Rx8, the modules of one kit | ubihazard/ddr5-spd-recovery `47baa29`, MIT | the decoded snapshots of the spdr project (Pactolus-Microelectronics/spdr `991c514`) for the first; the second differs from the first in its serial number and in byte 996, which no field holds |
| 1 DDR3 SODIMM | rigred/SPD `be3646f`, MIT | named DDR3 and not decoded |
| the 4 DDR4 dumps | as above | `decode-dimms -x` of i2c-tools 4.4 (the Fedora 44 package) over their `hexdump -C` text: size, ranks, device width, date, serial, part number and both CRCs agree. On the two dumps whose date bytes are no BCD, decode-dimms prints `0xDAAD` for one and reads the other as binary numbers (`2041-W29`); MemorY gives no date for either. decode-dimms 4.4 decodes no DDR5 dump: it checks a DDR5 SPD with the DDR4 CRC and drops it |

The 8 DDR4 and DDR5 dumps decode with good CRCs: 12 of 12 stored values equal the computed ones. `TestPublicDumps` holds every field of each to the values above, and `testdata/spd/README.md` names the files, their sources and every byte that was changed in the copies. `TestSynthetic` builds 14 dumps from the copies by changing named bytes (1Rx8, asymmetric ranks, a DDR4 multi-load stack, DDR5 3DS and DDP packages, bad CRCs, the first 128, 256 and 512 bytes, blank dumps); they hold the formulas, and no second decoder saw them. `tools/spd-fetch.sh` downloads 9 DDR5 UDIMM dumps of a repository that states no licence, and `TestFetchedDumps` decodes them where `MY_SPD_DIR` names their directory; all 9 have good CRCs and a date.

The reader ran against the sysfs trees that `internal/adapter/dimmspd/spdtest` builds for the reasons and for the states of a reading, with faults put into the reads: a refused open, an error at an offset, a read that does not return. In two guests of the lab, kernel 6.19.10 on the emulated SMBus of QEMU, the probe ran against a real sysfs: `ee1004` bound its clients and every read ended in ENXIO, and `spd5118` bound none ([validation-lab.md](validation-lab.md#spd)). Those EEPROMs hold no SPD, so no read of MemorY has returned SPD bytes from a kernel yet.

Not checked:

- every step on a real server: a read through `ee1004` or `spd5118` that returns bytes, the error a fault of a real bus gives, a read while the SPD write disable bit is set, an SPD behind I3C, AMD and arm64 boards;
- how long a read takes, and what it does on a bus that a BMC uses too ([performance.md](performance.md));
- which reason a real server gives;
- in which byte order and form firmware writes the SPD serial number into SMBIOS, which the tie rests on;
- DDR5 modules with 3DS or two-die packages, modules with asymmetric ranks, MRDIMM and CUDIMM, SPD revisions above 1.0 for DDR5 and above 1.2 for DDR4: no public dump was found;
- `decode-dimms` text of other versions than 4.4.

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

A scan of a live host does not run ipmitool. With `--bmc ADDRESS` it asks the BMC over Redfish, which serves the event log as log entries ([redfish.md](redfish.md)). Without it, save the log with `ipmitool sel elist > file` (it needs root and can take minutes) and import the file.

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
| Presence Detected | a firmware record alone, counted in no total |
| any other text on a memory sensor | informational event with the text as printed |

The states are read through the table `ipmi` ([vendors.md](vendors.md)), and each record keeps a row of `fw_record` with its event bytes. A deasserted record adds no error: it is kept as an informational event that a state ended. A deasserted "Memory Device Disabled" also withdraws the `disable` action the same log recorded for that module; both records are kept as informational events.

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

ipmitool up to 1.8.18 prints the stamp of a record through `gmtime()`: the clock of the BMC as it stands, without a zone. A BMC whose clock runs on UTC gives UTC; one set to local time gives that local time, and `--tz` names it. From 1.8.19 on, ipmitool turns the stamp into the local time of the machine it runs on (UTC with `-Z`), follows the locale of that machine and appends the zone abbreviation.

- A zone abbreviation with one meaning (UTC, KST, CEST, PDT and others) or a numeric zone (`+09`) is applied.
- A time without a zone, or with an abbreviation that has several meanings (CST, IST), is read as UTC, and the report says so. Give the zone with `my import --tz ZONE` to read such a dump correctly.
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

Adapter `redfish`, package `internal/adapter/redfish`. It reads what a BMC serves over Redfish from files and does not talk to a BMC. `my collect redfish` writes such files from a BMC, and `--bmc` does the same within the commands that scan; [redfish.md](redfish.md) describes both.

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

A memory resource carries no time. The mark is recorded at the time of the dump, once per DIMM: reading the same state again adds nothing. The time of the dump is the start time in the manifest of a collection, else the modification time of the files with `my import --mtime`, else the time of the import.

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

An attribute is kept when its name, its display name or its menu path holds a word of memory (`mem`, `dimm`, `ddr`, `ecc`, `scrub`, `ppr`, `adddc`, `sddc`, `spar`, `mirror`, `refresh`, `rank`, `correctable`, `uncorr`, `leaky`, `bucket`, `rowhammer`). It is never kept when its type is `Password`, when its name holds `password`, `passphrase`, `passwd`, `secret`, `token` or `credential`, or when `key` or `pin` is a word of its name or its end (`SetupPin`, `SETUPPIN`, `Tpm_Key`; `MemoryMapping` is kept). At most 400 attributes are kept, and a value longer than 200 characters is left out.

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

Every entry is read as one of the common events of [data-model.md](data-model.md#firmware-records), through the mapping tables of [vendors.md](vendors.md). The first step that answers decides:

| Step | How it is recognised | Read |
| --- | --- | --- |
| SEL bytes | `EntryType` is `SEL` and `SensorType` is `Memory`; or the message `SELEntryAdded` of the OpenBMC registry, whose argument is a SEL record in hex | the event bytes in `MessageId` (`0xNNaabbcc`, or 8 hex digits without `0x` as iDRAC writes them), or the name of the state in `Message`, through the table `ipmi`; `EntryCode` gives the direction, `GeneratorId` who logged it |
| a table | the registry prefix of `MessageId` is one a table applies to (`Platform`, `LogService`, `ResourceEvent`, `OpenBMC`, `IDRAC`), or the name is one of the OpenBMC registry under any prefix | the line of the message: the common event, its flags, the role of each argument (socket, channel, DIMM and rank of the OpenBMC messages; the location of the DMTF and iDRAC messages) |
| the BMC's registry | the dump holds the registry `MessageId` names | its text for the message, read by the wording rules |
| wording | the text of the entry is about memory | the wording rules: the names of the IPMI memory states, and words of any vendor ("post package repair ... completed", "corrected ... threshold ... exceeded", "predictive failure") |

An entry that is about memory and fits none of these is kept as an informational event with its message, and the report lists its message ID. An entry is about memory when a table says so (iDRAC keys that begin with `MEM`, a message of a table), when its `OriginOfCondition` is a memory resource, or when its message mentions memory, a DIMM or ECC. A message the table of its registry names as about something else is left out.

Every entry about memory also keeps a row of `fw_record`: message ID, registry and version, the vendor's severity, the place as written, the arguments, the text, the table line that classed it and how far that table was checked. A record that makes no event or action (a module detected, the RAS mode in effect, a log cleared) has that row alone.

Entries of type `CXL` are counted and not read.

A dump that holds message registries (`MessageRegistry` resources, or objects with `RegistryPrefix` and `Messages` saved without their type) gives each entry the registry its `MessageId` names: the one of the same major and minor version, else the newest of that prefix. The registry's `NumberOfArgs` picks the variant of a table line that is written for a number of arguments; when no variant fits, the roles of the line are not used and the place comes from `OriginOfCondition` or from the text. The registry's severity is the vendor's severity of an entry. A message no table names is classed by the wording of the registry's text (`fw_record.mapping` `bmc-registry`). Of each registry, at most 20,000 messages and 2 KiB of each text are read, and a file of more than 16 MiB is left out; a registry that cannot be read is named and the entries are read without it. Per registry, the ledger keeps how many of its messages are about memory and how many of those a table or the wording rules class (`registry_seen`, shown by `my vendors --seen`).

Further fields of an entry:

- `EventTimestamp` is the time of the event when it is given; `Created` is when the entry was written.
- `Resolved` is kept in the firmware record.
- `OverflowErrorCount` of an entry is told in the source inventory: the BMC lost that many entries of the log.
- `DiagnosticDataType` `CPER` or `CPERSection` with `DiagnosticData` in base64: the platform memory error section of the CPER record is read as the kernel's CPER records are. It gives the place (address, rank, bank, row, column, device, the SMBIOS handle of the module), and, for a message that is about no memory by itself (`PlatformErrorAtLocation`), the event: the section's severity and error type (`fw_record.mapping` `cper`). A section of another type is left alone. `AdditionalDataURI` is not followed.

### Grade

C for most entries: they name a module at best. An OpenBMC registry entry that carries the rank has grade B, and an entry whose arguments carry an address or a rank by their role in the table has grade B.

### Which module

1. `Links.OriginOfCondition`, when it points at a memory resource. The label is the `DeviceLocator` of that resource if the dump holds it, and the last segment of the URI otherwise.
2. The arguments that the line of the table names as the location, or, for a message of a table without roles, an argument that is the name of a slot.
3. A DIMM name in the message, the text after "at location", or a module named as `(Processor 1, Memory Module 4)`.
4. Socket, channel and DIMM numbers of an OpenBMC registry entry. They are matched with the `MemoryLocation` of the memory resources of the same dump. The numbers are also kept on the event, and name its slot when no inventory lists the place.
5. For SEL entries, event data 3 when the event bytes mark it as the module number.

### Times

`Created`, then `EventTimestamp`. A time without a zone is read as UTC. An entry about memory without a readable time is skipped and named in the report.

### Host

A `ComputerSystem` resource gives the host name (`HostName`), the system vendor and the model.

### Known gaps

- The error counts of a BMC are readings beside the ledger's events, and no total of the ledger includes them. `my inventory` sets them against the host's records ([redfish.md](redfish.md#operating-system-and-bmc-side-by-side)).
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

The time of a snapshot is the `Timestamp` of the output, which is the local time of the host without a zone. It is read as UTC unless `my import --tz ZONE` gives the zone. Output without a timestamp takes the time of the import.

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
| `cxl_memory_sparing_event` | in the maintenance class of post package repair: action `sppr` or `hppr`; in the class of sparing (cacheline, row, bank or rank, `hard` or `soft`): action `memory-sparing` when the record says spares are left, `sparing` when none is left, the record does not say, or the operation failed; with rank, bank group, bank and row where the record marks them valid |
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

A time without a zone is read as UTC unless `my import --tz ZONE` gives the zone.

### Host

A log may hold the records of several hosts. One import reads one host: the host named with `my import --host NAME` when the log names it, and the host of the first record that names one otherwise. Host names are compared without case.

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
