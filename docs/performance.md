# Performance

Wall time and peak memory of the commands on large inputs: one host with up to 1,000,000 corrected errors, and a fleet of 200 hosts with as many. The inputs are made up by `tools/perf`, which also runs the commands and prints the tables of this page. Nothing here comes from a real host.

## Reproduce

```
go build -o /tmp/my ./cmd/my
go run ./tools/perf run -my /tmp/my -dir /tmp/my-perf -events 100000,300000,1000000 -format json > runs.json
go run ./tools/perf run -my /tmp/my -dir /tmp/my-perf -events 100000,300000,1000000 -only scan,bundle,serve,open,report -gomemlimit 191MiB -format json > small.json
go run ./tools/perf table < runs.json
```

`perf run` makes the inputs below `-dir` once and takes them from there on later runs. It runs each command with its output thrown away and records the wall time and the peak resident set size (RSS) of the process as the kernel accounts it. Without `-format json` it prints the table at once; `-only` picks commands by the names in the first column of the table; `perf gen` writes one input on its own. With the three sizes the directory holds 3.4 GB at the end. A scan of the 1,000,000 host copy needs 2.15 GB more in `$TMPDIR` for its temporary ledger, and `my open` of its case as much to unpack the ledger.

## Inputs

The host copy is the files of one host as `my scan --root` reads them: a rasdaemon database, a kernel log, the EDAC counters and the SMBIOS table of a server with two sockets and 16 DIMMs. All errors belong to four faults of that host, a machine in an error storm. rasdaemon and the kernel log both report every error, so the ledger holds two event rows per error and the duplicate pass pairs them. The host case is the bundle `my bundle` writes from the host copy.

The fleet ledger holds 200 hosts over 180 days, written into one ledger without files. One host in five has no fault, the others one to three. The shares of the fault modes keep the order of the field studies of DRAM errors (faults of one cell most often, of a whole DIMM least often); the errors per fault follow a Pareto distribution of shape 1, cut so that no fault holds more than two fifths of the fleet. The same seed gives the same data.

| Errors | Host copy | Host case | Fleet ledger | Largest host of the fleet |
| ---: | ---: | ---: | ---: | ---: |
| 100,000 | 69.9 MB | 23.4 MB | 96.6 MB | 14,681 errors |
| 300,000 | 209.3 MB | 68.4 MB | 280.7 MB | 44,038 errors |
| 1,000,000 | 698.7 MB | 224.7 MB | 926.0 MB | 146,787 errors |

## Results

Mac mini M4 (10 cores, 16 GB), macOS 26.4, internal SSD with APFS; `my` of commit 7f17bcc built with Go 1.26.4; measured on 2026-11-16. Other builds and tests ran on the machine at the same time (load average 4.8 at the start, 2.3 at the end). A column is the number of corrected errors asked for; the inputs hold a few more. Two runs an hour apart differed by up to 20% in the time of commands that take a few seconds, by 6% in the time of the five-minute scans, and by up to 14% in peak memory.

| Command | Input | 100,000 events: time | peak RSS | 300,000 events: time | peak RSS | 1,000,000 events: time | peak RSS |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `scan` | host copy | 16.4 s | 86.6 MB | 51.7 s | 162.8 MB | 334 s | 408.3 MB |
| `bundle` | host copy | 16.8 s | 91.0 MB | 64.4 s | 158.8 MB | 366 s | 368.5 MB |
| `open` | host case | 1.33 s | 64.5 MB | 4.20 s | 129.4 MB | 13.1 s | 326.8 MB |
| `dimm` | host case | 1.70 s | 65.7 MB | 5.25 s | 147.8 MB | 16.7 s | 349.5 MB |
| `report` | host case | 4.69 s | 79.9 MB | 15.7 s | 179.6 MB | 46.6 s | 451.5 MB |
| `export` | host case | 1.04 s | 64.9 MB | 3.67 s | 130.9 MB | 10.5 s | 349.4 MB |
| `export --records` | host case | 1.66 s | 67.6 MB | 5.17 s | 131.9 MB | 16.0 s | 327.3 MB |
| `serve` | host copy | 16.1 s to ready, 0.3 ms scrape | 92.1 MB | 49.8 s to ready, 0.2 ms scrape | 170.8 MB | 329 s to ready, 0.3 ms scrape | 390.5 MB |
| `open` | fleet ledger | 0.88 s | 75.8 MB | 2.42 s | 136.5 MB | 8.34 s | 389.8 MB |
| `open --host` | fleet ledger | 0.86 s | 75.4 MB | 2.31 s | 137.9 MB | 7.35 s | 389.0 MB |
| `report --host` | fleet ledger | 1.17 s | 88.0 MB | 3.23 s | 141.0 MB | 10.2 s | 388.4 MB |
| `bundle` | fleet ledger | 1.75 s | 87.6 MB | 4.65 s | 159.2 MB | 15.6 s | 396.8 MB |
| `open` | fleet case | 1.02 s | 76.3 MB | 2.64 s | 156.2 MB | 8.57 s | 390.3 MB |
| `export --host` | fleet ledger | 0.84 s | 77.1 MB | 2.26 s | 149.9 MB | 7.14 s | 389.1 MB |

`serve` is `my serve --metrics`: the time from its start until its first scan has finished (`/-/ready` answers 200), then one request for `/metrics`, which is answered from that scan. `--host` on the fleet ledger still reads the whole ledger, so that a DIMM that moved between hosts keeps its history: its memory follows the size of the ledger.

On the fleet, where no host has more than 146,787 errors, time grows in step with the input. On the one host it grows faster from 300,000 errors on. At 1,000,000 the temporary ledger of the scan is 2.15 GB, about 1.1 KB per event row; in the ledger of the 100,000 host the original records take 43%, the event table 33% and its indexes 19%. The passes that link boots, slots, DIMMs and duplicates each go over every row of the host; at that size a sample of the process (macOS `sample`) showed them waiting on reads from the disk. 10,000,000 errors were not measured: the scan of 1,000,000 took more than five minutes, and the time grew faster than the input.

## Memory readings of a scan

A scan of a running host makes the readings of the Memory Explorer and runs `numactl --hardware`, `numastat` and `sar -B` where they are installed. `TestTimeOfTheReadings` in `internal/adapter/hostmem` measures the adapter on the host it runs on, 21 times with the programs and 21 times without:

```
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c -o hostmem.test ./internal/adapter/hostmem
sudo MY_READINGS_TIME=1 ./hostmem.test -test.run TestTimeOfTheReadings -test.v
```

On the lab host of [validation-lab.md](validation-lab.md) (a VMware guest with an AMD Ryzen Threadripper 1950X, one socket, two NUMA nodes, Fedora 44, kernel 7.3.0, as root), on 2026-11-17, with `numactl` and `numastat` installed and `sar` not:

| Run | With the programs: median | shortest | longest | Without: median | shortest | longest |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| lab host idle | 11.1 ms | 8.5 ms | 39.5 ms | 2.6 ms | 2.0 ms | 9.6 ms |
| three emulated guests booting on it | 14.0 ms | 8.3 ms | 39.8 ms | 3.3 ms | 2.5 ms | 5.6 ms |

The two programs add 8.5 ms to the median there, 10.7 ms with the guests running. `numactl` and `numastat` are stopped after 5 seconds and `sar` after 15, so programs that hang add at most 25 seconds.

## Small hosts

At start the binary sets a soft limit for the Go heap: a quarter of the memory the process may use (`MemTotal` of `/proc/meminfo`, or the smallest limit of its control groups when that is less: `memory.max` of version 2, `memory.limit_in_bytes` of version 1, as in a container or a systemd unit with `MemoryMax=`), at least 64 MiB. `GOMEMLIMIT` overrides it. On a host with 765 MiB the limit is 191 MiB, and `-gomemlimit 191MiB` runs the commands that way on any machine:

| Command | Input | 100,000 events: time | peak RSS | 300,000 events: time | peak RSS | 1,000,000 events: time | peak RSS |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `scan` | host copy | 14.9 s | 89.8 MB | 49.0 s | 163.7 MB | 332 s | 370.7 MB |
| `bundle` | host copy | 17.0 s | 83.5 MB | 57.4 s | 163.7 MB | 395 s | 390.0 MB |
| `open` | host case | 1.34 s | 65.4 MB | 3.93 s | 129.2 MB | 13.4 s | 269.9 MB |
| `report` | host case | 4.69 s | 80.0 MB | 14.1 s | 177.7 MB | 46.6 s | 351.4 MB |
| `serve` | host copy | 14.8 s to ready, 0.3 ms scrape | 90.9 MB | 52.2 s to ready, 0.3 ms scrape | 176.5 MB | 326 s to ready, 1.1 ms scrape | 345.5 MB |
| `open` | fleet ledger | 0.84 s | 76.2 MB | 2.34 s | 136.4 MB | 7.71 s | 282.9 MB |
| `bundle` | fleet ledger | 1.67 s | 86.4 MB | 4.67 s | 145.4 MB | 14.6 s | 327.5 MB |
| `open` | fleet case | 0.96 s | 76.8 MB | 2.65 s | 137.9 MB | 8.50 s | 388.4 MB |

Every command of the table stayed below 400 MB, up to 1,000,000 errors on one host and in the fleet: all of these inputs fit in 512 MB. The limit is soft. The collector runs more often near it, and what lies outside the Go heap (SQLite's page cache, the runtime) comes on top, so a peak above 191 MiB is no failure. What a small host makes slow is a host with hundreds of thousands of errors: its scan takes minutes, and its ledger (2.15 GB per million errors) is larger than the memory that could cache it.

The temporary ledger of a scan lies in `$TMPDIR`, or `/tmp` when that is not set. Where `/tmp` is a tmpfs, the ledger is held in memory: 2.15 GB for 1,000,000 errors on a host whose rasdaemon and kernel log both record every error. Point `TMPDIR` at a directory on disk, or keep the ledger in a file of its own with `--ledger FILE`.

## Profiles of a run

Three environment variables make a run write profiles of itself, for the question why a command is slow or large on an input that cannot be sent along:

```
MY_CPUPROFILE=cpu.out my scan
go tool pprof -top /usr/local/bin/my cpu.out
MY_PEAKPROFILE=peak.out my report case.mycase -o case.html
go tool pprof -sample_index=inuse_space -top /usr/local/bin/my peak.out
```

`MY_CPUPROFILE` is the CPU profile of the run, `MY_MEMPROFILE` the allocations of the whole run, and `MY_PEAKPROFILE` the heap at the moment it was largest: the heap is looked at ten times a second, and the profile is written again whenever it has grown by a tenth. A profile that cannot be written is reported on standard error and the command runs on. The CPU profile counts time on the CPU; a run that waits on the disk shows little there, and `/usr/bin/time` (wall time against user and system time) tells the two apart.
