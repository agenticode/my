# Evaluation on field data

`my eval` replays the error log of a production fleet through the analysis engine and compares the findings with the server failures that followed. This page names the dataset, says what it can and cannot show, describes the comparison and gives the command.

There are no results on this page yet. The dataset is distributed behind a login and has not been run through the harness for this repository. Until it has, the harness is exercised on generated rows, and the thresholds of the engine stay what the README calls them: defaults that have not been compared with failure analysis results.

## Dataset

The DRAM error dataset that Alibaba published with Cheng, Han, Lee, Li, Liu and Li, "An In-Depth Correlative Study Between DRAM Errors and Server Failures in Production Data Centers" (SRDS 2022).

| | |
| --- | --- |
| Description | [alibaba-edu/dcbrain, directory `dramdata`](https://github.com/alibaba-edu/dcbrain/tree/master/dramdata) |
| Files | [Alibaba Cloud Tianchi, dataset 132973](https://tianchi.aliyun.com/dataset/132973): one archive, `dramdata.zip`, 430,641,415 bytes. The download needs an account. |
| Size, as the description gives it | "more than 70 million DRAM errors", "more than 250K servers and 3 million DIMMs", "a eight-month span" |
| Analysis scripts of the authors | [zncheng/dramanalysis](https://github.com/zncheng/dramanalysis) |

The description lists three tables. The reader looks for them under the names `mcelog`, `inventory` and `trouble_tickets`.

| Table | Columns | One row is |
| --- | --- | --- |
| `mcelog` | `sid`, `memoryid`, `rankid`, `bankid`, `row`, `col`, `error_type`, `error_time` | one corrected error |
| `inventory` | `sid`, `server_manufacturer`, `DRAM_model`, `DIMM_number` | one server |
| `trouble_tickets` | `sid`, `failure_type`, `failed_time` | one server failure attributed to memory |

The description and the scripts of the authors spell three column names differently (`server_manufacturter`, `DIMM_num` and `failure_time` in the description). The reader finds columns by name and accepts both spellings.

Server manufacturers and DRAM models are coded: `M1` to `M4`, and `A1`, `A2`, `B1`, `B2`, `B3`, `C1`, `C2`, where the letter stands for the DRAM vendor. `error_type` is 1 for an error found by a read, 2 by the scrubber, 3 by a write (the description leaves out the number 2; the scripts of the authors use it for scrubbing). `failure_type` is 1 for a failure caused by an uncorrectable error, 2 by correctable errors, 3 for other failures. The scripts of the authors leave out tickets with `failure_type` 0, a value the description does not define; the harness counts them in a column of their own.

### License

The GitHub repository that describes the dataset is under Apache-2.0. The Tianchi page names two licenses for the files: "CC BY-SA 4.0" in its text and "CC-BY-SA-NC 4.0" in its license field. Both are share-alike and one forbids commercial use. This repository holds no row of the dataset. Its tests use generated rows in the same format (`testdata/dcbrain`, `my eval -generate`), and the evaluation reads the dataset from a path you give it.

### Calendar

The description says that the exact dates were anonymised and that "the date starts from the year 0001 month 01 day 01". The calendar that results has months of other lengths than a calendar year: the scripts of the authors start the eight months at days 0, 31, 61, 92, 123, 152, 183 and 213, so `0001-02-30` is a date on it. The reader therefore reads a time as a count of seconds from the first day. Spans between two times are exact. The real dates are not published, and nothing here tries to recover them.

The report prints times the way the dataset writes them. A server imported into a ledger gets real dates by placing the first day at 2001-01-01. The report also prints the largest day of each month it met, next to the month lengths it assumes.

## From a row to a record

| Column | Becomes |
| --- | --- |
| `sid` | the host |
| `memoryid` | the slot `DIMM<id>`; socket `id / 12`, channel `(id mod 12) / 2`, position `id mod 2` |
| `rankid` | `Rank` |
| `bankid` | `Bank`, 0 to 15 as given; bank group and bank address are not told apart |
| `row`, `col` | `Row`, `Column` |
| `error_type` | `ErrorScrubType`: Demand for 1 and 3, Patrol for 2 |
| `error_time` | `ErrorDT` |

Every row is a corrected error of observation grade A: rank, bank, row and column are known. Rows that are equal in every column are stored as one record with a count.

The dataset does not have the DRAM device, the failing bits, a physical address, DIMM serial numbers, uncorrected error records, repairs, page offlines or reboots. For the engine that means:

- A fault that spans several banks is reported as a device fault with the mode marked as estimated. It cannot be told from a DIMM fault.
- A DIMM is identified by server and slot. A replaced DIMM cannot be told from the one before it.
- Of the 13 rules, three can fire: R-ROW-03 (consider replacement), R-SOFT-01 (observe) and R-SYS-01 (hold replacement). No DIMM can reach "replace now", so that grade cannot be measured on this dataset.

The place of a `memoryid` on the board is not in the description. The layout above is the one the scripts of the authors use. The report counts what the errors say about it: how many pairs of DIMMs that fail on one server share a channel or a socket, next to what chance would give among 24 slots.

## Method

- Cut-offs. Findings are taken at the start of days 60, 90, 120, 150, 180 and 210 of the dataset calendar. At a cut-off the engine sees every error of a server up to that moment and nothing after it.
- Population. A server is in the population of a cut-off when it has an error up to the cut-off and no ticket up to it. Servers whose first error is later, and servers that failed before, are counted and left out.
- Outcome. The finding for a server is the worst finding among its DIMMs, with "hold replacement" above the others, as on the screen. It is compared with the tickets of the 30 days after the cut-off. Tickets name a server and no DIMM, which is why the comparison is per server. The windows of two cut-offs do not overlap, so a ticket is counted once. Tickets of servers that had no error before the cut-off are counted separately: no finding could precede them.
- Precision and recall. For a finding such as "consider replacement", precision is the share of the servers with that finding that got a ticket in the window, and recall the share of the servers with a ticket that had the finding. Both are given twice: against tickets of failure type 1, 2 or 3, and against tickets for an uncorrectable error alone. Each share comes with its 95% Wilson score interval.
- Two periods. Cut-offs before day 150 are the development period, the others the held-out period. A threshold may be chosen by looking at the development period. The held-out period shows whether the choice holds on data it was not made on. The report gives both, and a change of a default has to state both.
- Before a failure. A second replay takes every server with a ticket at fixed times before it (one second, 5 minutes, 1 hour, 1 day, 7 days, 30 days) and counts what the engine showed then, by failure type.
- Reference. Next to the findings the report counts the servers with at least one error in the 7 days before the cut-off, and their tickets. It is a flag anyone can raise without an engine, to compare the findings with.
- History of a server. The dataset is the error log of a fleet over eight months. A server without rows before its first error is taken to have had no errors until then, so a burst on such a server is a new fault at once. `-from-first-error` takes the other reading, in which nothing is known about a server before its first error and a trend needs a week of records. The report says which was used.
- Lower observation grades. `-degrade B` removes bank, row and column and keeps the rank; `-degrade C` turns the errors of a DIMM into one count per day, read at the end of the day. The same comparison then shows what a platform that reports less would give.
- Large servers. The error table is split by server on disk, and one server is analysed at a time, so memory use does not grow with the fleet. A server with more than 2,000,000 records (`-max-records`) is merged in time buckets of a minute, an hour or a day, whichever is the first that brings it under the limit. If none does, the server is left out. The report names every such server with its row count and its ticket.

## Command

```
my eval -data dramdata.zip
```

`-data` takes the archive as downloaded, or a directory that holds the three tables as `.csv` or `.tar.gz`. Nothing is unpacked: the tables are read as streams.

| Flag | Meaning |
| --- | --- |
| `-format text\|md\|json` | plain text, Markdown tables, or the whole result as JSON |
| `-config FILE`, `-set key=value` | thresholds of the engine, as for the other commands |
| `-cutoffs 60,90,...`, `-horizon 30`, `-split 150` | cut-off days, length of the outcome window, first day of the held-out period |
| `-leads 1s,5m,1h,24h,168h,720h` | times before a ticket at which the finding is taken |
| `-degrade B\|C`, `-from-first-error` | see Method |
| `-spool DIR` | keep the error table, split by server, for the next run |
| `-detail FILE` | one line per server and time: finding, rules, statements, widest fault mode, what followed |
| `-server SID` | analyse this server only; may be repeated |
| `-extract SID -ledger FILE` | import one server into a ledger |
| `-generate DIR` | write a made-up fleet in the format of the dataset |

A single case is looked at through the ledger. The date for `-as-of` is the day of the dataset calendar counted from 2001-01-01:

```
my eval -data dramdata.zip -extract <sid> -ledger case.db
my analyze -ledger case.db -as-of 2001-06-01
```

`my eval` is left out of the command list. It needs a copy of the dataset.

On a made-up fleet of 36,254,480 rows and 29,963 servers, packed like the archive, one run took 44 seconds and 1.2 GB of memory on an Apple M4 with two workers.

## Reading the report

The report has these parts: what was read and skipped; errors and DIMMs by observation grade; faults by mode; findings, rules and evidence statements over all records; the population at each cut-off; findings and what followed for each period, also by rule, by statement and by widest fault mode; and the findings before each failure.

The excerpt below comes from generated rows (`my eval -generate fleet`, then `my eval -data fleet`). The rows follow simple recipes, such as a row that fails at more and more columns before a ticket. The numbers show the layout of the report and say nothing about real DIMMs.

```
Findings and what followed, development period (cut-off days 60, 90, 120)

  By finding for the server:
    finding                              servers  UE ticket  CE ticket  misc ticket  type 0 ticket  types 1-3     UE
    observe                                  325          0          0            0              0      0.00%  0.00%
    consider replacement                      46          2          0            0              0      4.35%  4.35%
    replace now                                0          0          0            0              0          -      -
    hold replacement                          19          0          0            0              0      0.00%  0.00%
    all                                      390          2          0            0              0      0.51%  0.51%
    reference: error in the last 7 days       80          2          0            0              0      2.50%  2.50%
  replace now: no server
  consider replacement, against a ticket of type 1, 2 or 3: precision 4.35% (2 of 46, 95% interval 1.20% to 14.53%); recall 100.00% (2 of 2, 95% interval 34.24% to 100.00%)
```

A server counts once per cut-off at which it is in the population, so the 390 servers of the three cut-offs are 390 pairs of a server and a cut-off. "types 1-3" is the share of the row with a ticket of failure type 1, 2 or 3 in the window, and "UE" the share with a ticket for an uncorrectable error.

```
  Servers with a ticket of failure type 1 (uncorrectable error):
    time before the ticket  servers  no error yet  ok  observe  consider replacement  replace now  hold replacement  consider or replace now
    just before                  33             0   0        7                    26            0                 0                   78.79%
    5 minutes                    33             0   0        7                    26            0                 0                   78.79%
    1 hour                       33            14   0        0                    19            0                 0                   57.58%
    1 day                        33            14   0        0                    19            0                 0                   57.58%
    7 days                       33            14   0        0                    19            0                 0                   57.58%
    30 days                      33            30   0        0                     3            0                 0                    9.09%
```

"no error yet" counts the servers whose first error came later than that moment.

## Limits

- One operator's fleet over eight months. Nothing here carries over to another fleet without a run on its data.
- The error table lists servers that had at least one corrected error. Failures without a preceding corrected error show up as tickets without an earlier error; no analysis of error records could precede them.
- A ticket names a server. When a server has errors on several DIMMs, the comparison cannot say which of them failed.
- What the operator did between an error and a ticket is not in the data. A DIMM that was replaced in time leaves no ticket, and a server with a finding and no ticket may be one of those.
- The engine reads records and states what they show at one moment. It does not forecast. The comparison asks whether servers with a worse finding failed more often afterwards, and how long before a failure a finding was there.
- Without the DRAM device and the failing bits, the modes "device" and "DIMM" cannot be told apart and the check for several failing data lines never applies.
- The layout of `memoryid` on the board is an assumption taken from the scripts of the authors. R-SYS-01, which looks at the DIMMs of one channel, depends on it.
- The rules about uncorrected errors, firmware marks, repairs, page offlines, moved DIMMs and reseats cannot be evaluated on this dataset at all.
