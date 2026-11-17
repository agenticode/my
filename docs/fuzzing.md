# Fuzzing

MemorY reads what machines in trouble wrote and what other people sent: kernel logs, databases, firmware tables, BMC answers, case bundles, the output of a vendor analyzer. Every reader of such input has a fuzz target (Go native fuzzing). This page lists the targets, says what they check and how to run them.

## Run

```
make fuzz                       # each target for 10 s
make fuzz FUZZTIME=10m JOBS=2   # a long run, two workers per target
go test -run '^$' -fuzz '^FuzzCollect$' -fuzztime 10m -fuzzminimizetime 200x ./internal/adapter/klog/
```

CI runs every target on each push (job `fuzz`, `make fuzz FUZZTIME=20000x`): the seeds, the inputs kept under `testdata/fuzz`, and 20,000 new inputs per target.

The long run takes ten minutes per target, more than seven hours for all of them. Each target has run that long with two workers on a Mac mini M4 in November 2026, and a target that failed ran for ten minutes again after its fix. The inputs behind the fixes are kept under `testdata/fuzz`. A run on arm64 does not find what goes wrong on amd64 only: Go converts a float beyond the range of `int` to the smallest `int` on amd64 and to the largest on arm64, and the input `storm.count=1e99` of `FuzzConfigRead` failed in CI (amd64) and never on the Mac.

A run that is limited by a duration can end with the one line `context deadline exceeded` and no failing input: `go test` ran out of time while a worker was at work on an input. That is no finding, and `make fuzz` runs such a target once more. A run limited by a number of calls, as in CI, has no deadline to hit.

`go test` spends up to 60 s on making each input smaller that reached new code, and a worker that does so fuzzes nothing meanwhile. `make fuzz` bounds that work to 200 calls per input (`FUZZMINIMIZE`). On a Mac mini M4 with two workers, `FuzzParseText` of `internal/decode/cper` made 86,208 calls in its first 153 s with the default, and 1,222,676 calls in 27 s with the bound.

An input that fails is written to `testdata/fuzz/<Target>/` in the directory of the package, and `go test` runs every file there as a test from then on. Commit the file with the fix. The seeds are the fixtures of the repository, so an input derived from them holds nothing private; an input derived from a file of your own may.

`TestEveryFuzzTargetIsListed` (`internal/testutil`) fails when a fuzz function is missing from `FUZZ_TARGETS` in the Makefile or from the table below.

## Targets

| What is read | Package | Target |
| --- | --- | --- |
| detail string, layer position and label of the EDAC drivers | `internal/decode/edac` | `FuzzParseDetail` |
| CPER memory error section as the kernel prints it | `internal/decode/cper` | `FuzzParseText` |
| CPER memory error section in binary form, full and compact | `internal/decode/cper` | `FuzzParseBinary` |
| Generic Error Status Blocks of an error region: lengths, entry revisions, time stamps | `internal/decode/cper` | `FuzzParseStatusBlocks` |
| machine check registers, and the location text rasdaemon stores | `internal/decode/mce` | `FuzzDecode` |
| event log record of 16 bytes, event data, event description | `internal/decode/ipmi` | `FuzzParseRecord` |
| sensor names and messages of a BMC | `internal/decode/ipmi` | `FuzzLabels` |
| DMI strings, kernel log line and CPU flags that name a hypervisor | `internal/decode/virt` | `FuzzNames` |
| SPD dump in any form: binary, hex text, decode-dimms output, plain or gzip-compressed; and the decode of any bytes | `internal/decode/spd` | `FuzzParse` |
| hex text of hexdump, od, xxd, i2cdump and plain hex | `internal/decode/spd` | `FuzzReadHex` |
| `decode-dimms` output | `internal/decode/spd` | `FuzzDecodeDimms` |
| raw SMBIOS table | `internal/adapter/smbios` | `FuzzTables` |
| SMBIOS entry point | `internal/adapter/smbios` | `FuzzEntryPoint` |
| `dmidecode` output | `internal/adapter/smbios` | `FuzzDmidecode` |
| kernel log: `dmesg`, syslog file, `/dev/kmsg` records, journal export | `internal/adapter/klog` | `FuzzCollect` |
| pstore files and the archive of systemd-pstore: file names, the header of each part, the grouping of parts into dumps | `internal/adapter/crashlog` | `FuzzParts` |
| the Boot Error Region (BERT), as the file of sysfs or a saved copy | `internal/adapter/bert` | `FuzzCollect` |
| rows of the rasdaemon tables, with values of every SQLite type in every column | `internal/adapter/rasdaemon` | `FuzzRows` |
| rasdaemon database file, as bytes | `internal/adapter/rasdaemon` | `FuzzDatabase` |
| environment file with the settings of a rasdaemon store on a database server | `internal/adapter/rasdaemon` | `FuzzStoreSettings` |
| EDAC counter tree of sysfs | `internal/adapter/edacsysfs` | `FuzzCollect` |
| host files under `/proc` and `/sys` | `internal/adapter/hostinfo` | `FuzzCollect` |
| BMC event log: `ipmitool` and `ipmi-sel` text, CSV, raw records, JSON | `internal/adapter/sel` | `FuzzCollect` |
| Redfish resource or dump | `internal/adapter/redfish` | `FuzzCollect` |
| `nvidia-smi` output: text, XML, CSV query | `internal/adapter/nvidia` | `FuzzCollect` |
| `mcelog --client` output | `internal/adapter/mcelog` | `FuzzCollect` |
| FMFM boot, runtime and crash logs | `internal/adapter/fmfm` | `FuzzCollect` |
| tables of the dcbrain dataset | `internal/adapter/dcbrain` | `FuzzTables` |
| archive of the dcbrain dataset | `internal/adapter/dcbrain` | `FuzzArchive` |
| any file given to `my import`, plain or gzip-compressed: what it is taken for, and the reader that was chosen | `internal/scan` | `FuzzDetect` |
| injection plan of `my verify` | `internal/injection` | `FuzzRead` |
| settings profile of `my bios` | `internal/memras` | `FuzzParse` |
| table of firmware messages (`--event-map`) | `internal/fwmap` | `FuzzParse` |
| a firmware record asked of the tables and the wording rules: message ID, text, arguments, the BMC's registry text | `internal/fwmap` | `FuzzClassify` |
| a message registry in a Redfish dump, with an entry it is read for | `internal/adapter/redfish` | `FuzzRegistry` |
| a copy of a host's files that `my preflight --root` reads: kernel command line, processor, consoles, journald, systemd-pstore, kdump and unit files | `internal/preflight` | `FuzzHost` |
| engine configuration file | `internal/engine` | `FuzzConfigRead` |
| message template | `internal/i18n` | `FuzzFormat` |
| text that goes through the anonymizer of a bundle | `internal/bundle` | `FuzzText` |
| case bundle, as bytes | `internal/bundle` | `FuzzReadManifest` |
| case bundle with a well-formed archive around arbitrary manifest, source inventory and ledger | `internal/bundle` | `FuzzMembers` |
| response of a vendor analyzer | `internal/plugin` | `FuzzResponse` |
| text on its way to a terminal | `internal/term` | `FuzzSafe` |
| address of a BMC | `internal/redfish/client` | `FuzzParseTarget` |
| certificate fingerprint | `internal/redfish/client` | `FuzzParseFingerprint` |
| link inside a Redfish resource | `internal/redfish/client` | `FuzzResolve` |
| BMC answer that the secrets of a run are taken out of | `internal/redfish/client` | `FuzzScrub` |
| HTTP answer of a BMC: status, headers, body | `internal/redfish/client` | `FuzzAnswer` |
| URI of a resource, turned into a file name | `internal/redfish/collect` | `FuzzFilePath` |
| manifest of a Redfish collection | `internal/redfish/collect` | `FuzzParseManifest` |
| a Redfish service made of arbitrary resources: collected by `my collect redfish`, then read as a dump | `internal/redfish/collect` | `FuzzCrawl` |
| saved output of `racadm getsel` and `racadm lclog view`: records, wrapped messages, arguments run together, times | `internal/adapter/vendorlog` | `FuzzCollect` |
| a line of `my export events` made of an event, an action and a firmware record with arbitrary texts, times and counts: one line of JSON that reads back to them | `internal/eventstream` | `FuzzLine` |
| `dmidecode` memory devices and the memory banks of `lshw` text, as the Memory Explorer reads them for the board; each slot keeps the names of the tests that placed it | `internal/explorer` | `FuzzDimms` |
| slot names, system, board and processor records as the ledger writes them for the board: the forms of the names and the pass that decides socket, channel and position; every placed slot has a socket, channel and position from 0, and no slot is lost | `internal/explorer` | `FuzzNames` |
| cache nodes of `lshw` text and the cache lines of sysfs | `internal/explorer` | `FuzzCaches` |
| `numactl --hardware` and `numastat`, and the slots, nodes and caches put together | `internal/explorer` | `FuzzNuma` |
| `/proc/meminfo`, `/proc/vmstat`, `/proc/pressure/memory`, `/proc/slabinfo` | `internal/explorer` | `FuzzSystem` |
| the paging table of `sar -B` | `internal/explorer` | `FuzzPage` |
| status blocks of processes, `ps` lines, PSS lines | `internal/explorer` | `FuzzProcesses` |
| `pgrep -a java`, `jstat -gc` and the sizes on a Java command line | `internal/explorer` | `FuzzJVM` |
| lists of open files and the lines of `fincore`, `stat` and `find` | `internal/explorer` | `FuzzFileCache` |
| the cgroup of a process and its `memory.numa_stat` | `internal/explorer` | `FuzzProcessNuma` |
| `/sys/kernel/mm/lru_gen/enabled` and the debug file of MGLRU | `internal/explorer` | `FuzzMglru` |
| the load of a host: established TCP sessions, load average, CPUs, memory in use | `internal/explorer` | `FuzzLoad` |
| byte counts and numbers as the explorer writes them | `internal/explorer` | `FuzzFormat` |

## What a target checks

A target fails on a panic, on a call that does not return, and when the code breaks one of its promises:

- An adapter returns a report with a status. Every record it hands on is one the ledger takes: a time between 1970 and 9999, a known severity and grade, a fingerprint, a count of at least 1. The records it skipped are part of the records it counted.
- A reader that works on a copy leaves the source unchanged and removes the copy. A reader that refuses its input leaves nothing behind.
- The Redfish client sends GET requests, one login and one logout, to the BMC it was given and to no other host. It gives up after a bounded number of requests and pauses. Neither the password nor the session token is in an error, in the manifest or in a file of the collection, and no file is written outside the collection directory.
- A response of a vendor analyzer is refused, or every text in it is short and free of control characters, every number is within its bounds, and the rows it names as neighbours are symmetric.
- A case bundle is refused, or the ledger that was taken out of it opens with this build.
- `my preflight` on a copy of a host's files gives every check one of its five states and a sentence in both languages, writes its script, and changes nothing in the copy.
- A reader of the Memory Explorer returns at most its bound of slots, nodes, lines or rows, and a slot that no test placed has socket and channel 0.
- Text that goes to a terminal holds no control character but line breaks, tabs and the colour codes of the figures, whatever the text was and however the writes cut it up.

## What is not covered

- The ledger as an input. `FuzzMembers` hands the reader a damaged ledger file; it does not put arbitrary rows into a well-formed one and run the analysis and the views on them.
- The SQLite library itself. `FuzzDatabase` and `FuzzMembers` reach it with damaged files, and what they check is what MemorY does with its answers.
- Live sources: the journal and `/dev/kmsg` are read through the kernel log reader, which `FuzzCollect` covers; the programs that are run (`journalctl`, `nvidia-smi`, `mcelog`) are not stood in for.
