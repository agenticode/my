# Evaluation on field data

`my eval` replays the error log of a production fleet through the analysis engine and compares the findings with the server failures that followed. This page gives the results on one public dataset, says how they were obtained and what was changed because of them, and lists what they do not show.

## Summary

The dataset is the DRAM error log that Alibaba published in its dcbrain repository: 75,214,692 corrected errors on 35,240 DIMMs of 30,502 servers over eight months, and 3,017 tickets of server failures. The numbers below are from engine 6 with the default thresholds. Findings are taken at six cut-off days and compared with the tickets of the 30 days after each; the first three cut-offs are the development period, the last three the held-out period.

- A server with the finding "consider replacement" got a failure ticket within 30 days in 2.48% of the cases in the development period and in 2.56% in the held-out period. A server with "observe" got one in 0.26% and 0.23%.
- The finding was there for 39.4% and 34.6% of the servers that got a ticket and had an error before the cut-off.
- Most tickets were of servers without any error before the cut-off: 599 of the 797 tickets in the development windows and 513 of 756 in the held-out windows.
- Of the 567 servers with a ticket for an uncorrectable error, 442 had "consider replacement" one second before the ticket, 263 an hour before, 96 a day before and 54 a week before.
- The widest fault mode on a server orders the share with a ticket: bank 9.7% and 6.6% (development and held-out), row 1.0% and 0.9%, column 0.8% and 0.5%, single-bit 0.15% and 0.17%.
- Two conditions of rule R-ROW-03 were changed because of these measurements. The first cut the servers with "consider replacement" by more than half at 1.8 and 2.8 times the precision.
- With the location detail removed (rank only, or a count per DIMM and day) no server reaches "consider replacement".
- No DIMM can reach "replace now" on this dataset. It has no uncorrected error records, firmware marks or repairs.

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

### What the run read

| | |
| --- | --- |
| Rows of the error table | 75,214,692, none skipped |
| Found by a read, by the scrubber, by a write | 38,504,052, 36,623,454, 87,186 |
| Servers with errors | 30,502, all of them in the inventory |
| DIMMs with errors | 35,240 |
| Tickets | 3,017: type 0 880, type 1 567, type 2 809, type 3 761 |
| Tickets of servers that have rows in the error table | 2,139: type 0 2, type 1 567, type 2 809, type 3 761 |
| Largest day of each of the eight months | 31, 30, 31, 31, 29, 31, 30, 31: the month lengths the reader assumes |
| Records after merging rows that are equal in every column | 25,376,305 |

The table lists only servers that had at least one corrected error. Of the 880 tickets of type 0, 878 are of servers that are not in it.

Four servers were left out because they have too many records even when their rows are merged by the day. Together they hold 42,564,906 rows, 56.6% of the table:

| Server | rows | DIMMs | ticket |
| --- | ---: | ---: | --- |
| `Server_28769` | 31,609,988 | 1 | none |
| `Server_26851` | 4,554,976 | 6 | none |
| `Server_29962` | 3,654,353 | 1 | type 2 on `0001-08-17` |
| `Server_28736` | 2,745,589 | 5 | none |

Two more servers were merged by the minute: `Server_17065` (3,936,397 rows on 1 DIMM, 131,088 records) and `Server_28529` (3,152,141 rows on 3 DIMMs, 71,844 records). The analysed servers are 30,498 with 35,227 DIMMs and 32,649,786 errors.

### License

The GitHub repository that describes the dataset is under Apache-2.0. The Tianchi page names two licenses for the files: "CC BY-SA 4.0" in its text and "CC-BY-SA-NC 4.0" in its license field. Both are share-alike and one forbids commercial use. This repository holds no row of the dataset. Its tests use generated rows in the same format (`testdata/dcbrain`, `my eval -generate`), and the evaluation reads the dataset from a path you give it.

### Calendar

The description says that the exact dates were anonymised and that "the date starts from the year 0001 month 01 day 01". The calendar that results has months of other lengths than a calendar year: the scripts of the authors start the eight months at days 0, 31, 61, 92, 123, 152, 183 and 213, so `0001-02-30` is a date on it. The reader therefore reads a time as a count of seconds from the first day. Spans between two times are exact. The real dates are not published, and nothing here tries to recover them.

The report prints times the way the dataset writes them. A server imported into a ledger gets real dates by placing the first day at 2001-01-01. The report also prints the largest day of each month it met, next to the month lengths it assumes; on the dataset the two agree.

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

- A fault that spans several banks of one rank is reported as a device fault with the mode marked as estimated: it may lie in more than one device. A fault across both ranks is a DIMM fault.
- A DIMM is identified by server and slot. A replaced DIMM cannot be told from the one before it.
- Of the 18 rules, three can fire: R-ROW-03 (consider replacement), R-SOFT-01 (observe) and R-SYS-01 (hold replacement). No DIMM can reach "replace now", so that grade cannot be measured on this dataset.

### The layout of memoryid

The place of a `memoryid` on the board is not in the description. The layout above is the one the scripts of the authors use. Two counts of the run speak for it.

On servers that the inventory lists with 8 or 12 DIMMs, every DIMM with errors sits at position 0, as it would with one DIMM per channel:

| DIMMs of the server | DIMMs with errors at position 0 | at position 1 |
| --- | ---: | ---: |
| 8 | 1,138 | 0 |
| 12 | 20,929 | 0 |
| 16 | 191 | 197 |
| 24 | 6,355 | 6,430 |

On the servers with 24 DIMMs, where every slot is filled, 1,975 servers have errors on two or more DIMMs. Of the 3,858 pairs of such DIMMs, 675 (17.50%) share a channel under the layout. Chance puts 4.35% of the pairs of 24 slots on one channel. Pairs on one socket but different channels are 42.77%, where chance gives 43.48%.

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

`-data` takes the archive as downloaded, or a directory that holds the three tables as `.csv` or `.tar.gz`. Nothing is unpacked: the tables are read as streams. On the dataset one run takes 60 seconds and 1.7 GB of memory on an Apple M4 with two workers.

| Flag | Meaning |
| --- | --- |
| `-format text\|md\|json` | plain text, Markdown tables, or the whole result as JSON |
| `-config FILE`, `-set key=value` | thresholds of the engine, as for the other commands |
| `-cutoffs 60,90,...`, `-horizon 30`, `-split 150` | cut-off days, length of the outcome window, first day of the held-out period |
| `-leads 1s,5m,1h,24h,168h,720h` | times before a ticket at which the finding is taken |
| `-degrade B\|C`, `-from-first-error` | see Method |
| `-spool DIR` | keep the error table, split by server, for the next run |
| `-detail FILE` | one line per server and time: finding, rules, statements, widest fault mode, what followed, errors so far and in the last 7 days, age of the latest error and of the latest burst |
| `-server SID` | analyse this server only; may be repeated |
| `-extract SID -ledger FILE` | import one server into a ledger |
| `-generate DIR` | write a made-up fleet in the format of the dataset |

A single case is looked at through the ledger. The date for `-as-of` is the day of the dataset calendar counted from 2001-01-01:

```
my eval -data dramdata.zip -extract <sid> -ledger case.db
my analyze -ledger case.db -as-of 2001-06-01
```

`my eval` is left out of the command list. It needs a copy of the dataset.

## Results

Engine 4, default thresholds, every server counted as watched from the first day of the dataset. A server counts once per cut-off at which it is in the population, so "servers" in the tables of a period are pairs of a server and a cut-off.

### Fault modes

Over all records of the analysed servers, as of the last day:

| Mode | faults | errors in them | share of the errors | DIMMs whose largest fault has the mode |
| --- | ---: | ---: | ---: | ---: |
| single-bit | 25,113 | 10,809,044 | 33.11% | 23,345 (66.3%) |
| row | 28,130 | 2,347,661 | 7.19% | 4,199 (11.9%) |
| multi-row | 11,184 | 1,359,756 | 4.16% | 946 (2.7%) |
| column | 28,026 | 16,859,418 | 51.64% | 6,136 (17.4%) |
| bank | 419 | 80,631 | 0.25% | 180 (0.5%) |
| device | 149 | 676,626 | 2.07% | 130 (0.4%) |
| dimm | 74 | 414,482 | 1.27% | 69 (0.2%) |
| system-issue | 2,117 | 102,168 | 0.31% | 222 (0.6%) |

All 149 device faults are estimates, for the lack of a device field. At that time 33,263 DIMMs have the finding "observe", 1,610 "consider replacement" and 354 "hold replacement".

### Findings and what followed

Development period, cut-off days 60, 90 and 120:

| Finding for the server | servers | UE ticket | CE ticket | misc ticket | with a ticket of type 1 to 3 | with a UE ticket |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| observe | 44,527 | 25 | 38 | 54 | 0.26% | 0.06% |
| consider replacement | 3,145 | 21 | 24 | 33 | 2.48% | 0.67% |
| replace now | 0 | 0 | 0 | 0 | - | - |
| hold replacement | 130 | 0 | 1 | 2 | 2.31% | 0.00% |
| all servers of the population | 47,802 | 46 | 63 | 89 | 0.41% | 0.10% |
| reference: an error in the last 7 days | 19,496 | 23 | 28 | 45 | 0.49% | 0.12% |

- "consider replacement" against a ticket of type 1 to 3: precision 2.48% (78 of 3,145, 95% interval 1.99% to 3.08%), recall 39.39% (78 of 198, 95% interval 32.85% to 46.34%).
- Against a ticket for an uncorrectable error: precision 0.67% (21 of 3,145, 95% interval 0.44% to 1.02%), recall 45.65% (21 of 46, 95% interval 32.15% to 59.82%).
- 599 more tickets of type 1 to 3 fall into these windows from servers without an error before the cut-off, 148 of them for an uncorrectable error.

Held-out period, cut-off days 150, 180 and 210:

| Finding for the server | servers | UE ticket | CE ticket | misc ticket | with a ticket of type 1 to 3 | with a UE ticket |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| observe | 66,942 | 31 | 62 | 60 | 0.23% | 0.05% |
| consider replacement | 3,285 | 19 | 38 | 27 | 2.56% | 0.58% |
| replace now | 0 | 0 | 0 | 0 | - | - |
| hold replacement | 192 | 2 | 0 | 4 | 3.12% | 1.04% |
| all servers of the population | 70,419 | 52 | 100 | 91 | 0.35% | 0.07% |
| reference: an error in the last 7 days | 20,682 | 21 | 50 | 42 | 0.55% | 0.10% |

- "consider replacement" against a ticket of type 1 to 3: precision 2.56% (84 of 3,285, 95% interval 2.07% to 3.15%), recall 34.57% (84 of 243, 95% interval 28.87% to 40.75%).
- Against a ticket for an uncorrectable error: precision 0.58% (19 of 3,285, 95% interval 0.37% to 0.90%), recall 36.54% (19 of 52, 95% interval 24.80% to 50.13%).
- 513 more tickets of type 1 to 3 fall into these windows from servers without an error before the cut-off, 138 of them for an uncorrectable error.

At 2.5%, 39 of 40 servers with "consider replacement" had no ticket in the following 30 days. Against the reference, the finding names a sixth of the servers at about five times the share with a ticket, and reaches fewer of the tickets (39.4% and 34.6%, where the reference reaches 48.5% and 46.5%).

Per cut-off:

| Cut-off day | period | servers | left out: ticket before | not yet in: first error later | tickets of type 1 to 3 in the window | tickets without an earlier error, type 0 included | servers with "consider replacement" |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 60 | development | 13,076 | 550 | 16,872 | 63 | 294 | 1,016 |
| 90 | development | 16,000 | 814 | 13,684 | 65 | 265 | 1,031 |
| 120 | development | 18,726 | 1,074 | 10,698 | 70 | 296 | 1,098 |
| 150 | held-out | 21,085 | 1,347 | 8,066 | 76 | 290 | 1,107 |
| 180 | held-out | 23,512 | 1,612 | 5,374 | 83 | 263 | 1,129 |
| 210 | held-out | 25,822 | 1,852 | 2,824 | 84 | 320 | 1,049 |

### By statement and by fault mode

Share of the servers with a ticket of type 1 to 3 within 30 days, by the statements of the evidence. A server can carry several statements. The `row.*` statements belong to R-ROW-03, `sys.channel` to R-SYS-01 and the `soft.*` statements to R-SOFT-01.

| Statement | servers, development | with a ticket | servers, held-out | with a ticket |
| --- | ---: | ---: | ---: | ---: |
| `row.spreading_area` | 142 | 12.68% | 118 | 9.32% |
| `row.new` | 554 | 3.61% | 557 | 3.59% |
| `row.rising` | 622 | 3.38% | 620 | 3.06% |
| `row.spreading_line` | 2,361 | 2.71% | 2,304 | 2.65% |
| `row.area` | 131 | 2.29% | 269 | 2.23% |
| `row.storm` | 431 | 1.62% | 455 | 3.74% |
| `sys.channel` | 130 | 2.31% | 192 | 3.12% |
| `soft.fault` | 7,638 | 0.75% | 15,166 | 0.45% |
| `soft.few` | 2,160 | 0.74% | 3,096 | 0.55% |
| `soft.more` | 762 | 0.92% | 1,654 | 0.54% |
| `soft.storm` | 3,716 | 0.48% | 8,022 | 0.26% |
| `soft.stray` | 1,123 | 0.62% | 1,758 | 0.40% |
| `soft.stray_once` | 13,495 | 0.29% | 20,265 | 0.25% |
| `soft.cell` | 23,784 | 0.14% | 33,688 | 0.18% |

By the widest fault mode on the server:

| Widest fault mode | servers, development | with a ticket | servers, held-out | with a ticket |
| --- | ---: | ---: | ---: | ---: |
| bank | 206 | 9.71% | 305 | 6.56% |
| device | 101 | 6.93% | 133 | 1.50% |
| dimm | 41 | 4.88% | 58 | 0.00% |
| system-issue | 130 | 2.31% | 192 | 3.12% |
| multi-row | 537 | 1.68% | 874 | 1.26% |
| row | 5,620 | 1.01% | 8,039 | 0.86% |
| column | 5,769 | 0.83% | 10,950 | 0.47% |
| single-bit | 35,398 | 0.15% | 49,868 | 0.17% |

### Before a ticket

Servers with a ticket for an uncorrectable error, and the finding at times before it:

| Time before the ticket | servers | no error yet | observe | consider replacement | hold replacement | share with "consider replacement" |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| just before | 567 | 0 | 103 | 442 | 22 | 77.95% |
| 5 minutes | 567 | 84 | 87 | 375 | 21 | 66.14% |
| 1 hour | 567 | 210 | 80 | 263 | 14 | 46.38% |
| 1 day | 567 | 377 | 85 | 96 | 9 | 16.93% |
| 7 days | 567 | 434 | 75 | 54 | 4 | 9.52% |
| 30 days | 567 | 470 | 54 | 40 | 3 | 7.05% |

Two thirds of these servers (377 of 567) had their first error within the last day before the ticket, and 210 within the last hour. Of the 103 servers that still had "observe" one second before the ticket, 43 had fewer than 10 errors by then.

Servers with a ticket of type 2 (808 analysed) had "consider replacement" in 587 cases just before the ticket, 228 an hour before and 127 a day before. Servers with a ticket of type 3 (761): 666, 615 and 163.

### Lower observation grades

The same replay with location detail removed before the engine sees the records. Development period; the held-out numbers follow in brackets.

| Records as | servers with "consider replacement" | servers with "hold replacement" | with a ticket | servers with the statement `soft.unlocated_rising` | with a ticket |
| --- | ---: | ---: | ---: | ---: | ---: |
| grade A, as the dataset has them | 3,145 (3,285) | 130 (192) | 2.31% (3.12%) | 0 | - |
| grade B: rank only | 0 (0) | 407 (659) | 1.72% (1.97%) | 738 (759) | 2.71% (2.24%) |
| grade C: one count per DIMM and day | 0 (0) | 407 (659) | 1.72% (1.97%) | 738 (759) | 2.71% (2.24%) |

Without rows and columns no fault has a mode, R-ROW-03 cannot fire, and every finding is "observe" or "hold replacement". R-SYS-01 fires three times as often: a cell or a row that repeats cannot be recognised as confined to the DIMM, so every DIMM with five errors counts on its channel. The statement that the count is rising stays under "observe"; the servers that carry it got a ticket about as often as the servers with "consider replacement" at grade A.

### Start of a server's history

| History of a server starts | period | servers with "consider replacement" | precision | recall | UE tickets with the finding just before, 1 hour and 1 day before |
| --- | --- | ---: | ---: | ---: | --- |
| with the first day of the dataset | development | 3,145 | 2.48% | 39.39% | 442, 263, 96 |
| | held-out | 3,285 | 2.56% | 34.57% | |
| with its first error (`-from-first-error`) | development | 3,013 | 2.42% | 36.87% | 331, 208, 80 |
| | held-out | 3,165 | 2.50% | 32.51% | |

The reading matters most right before a failure: a burst on a server without earlier errors is a new fault under the first reading and a fault with an unknown trend under the second.

## What was changed because of the results

### A burst counts while it is recent

R-ROW-03 fired for a fault whose densest hour ever held 100 errors or more, however long ago. In the development period 3,416 of the 6,407 servers with "consider replacement" rested on such a burst alone, and 0.47% of them got a ticket, where the whole population got one at 0.41%. A burst now has to lie within the last 7 days (`trend.recent_days`). One further back is stated under R-SOFT-01.

### A fault that covers a bank or more counts without growth

R-ROW-03 asked a bank, device or DIMM fault to be rising, spreading or bursting. In the development period 181 servers had such a fault as their widest and no statement of R-ROW-03; 5 of them (2.8%) got a ticket, as often as the servers the rule named and four times as often as servers with a row or column fault and no statement (63 of 9,072, 0.7%). A fault of that extent with at least 10 errors now fires the rule by itself (statement `row.area`).

| Engine | period | servers with "consider replacement" | of them with a ticket | precision | recall | of them with a UE ticket | recall, UE |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | development | 6,407 | 90 | 1.40% | 45.45% (90 of 198) | 23 | 50.00% (23 of 46) |
| | held-out | 10,443 | 95 | 0.91% | 39.09% (95 of 243) | 22 | 42.31% (22 of 52) |
| 2: a burst counts while recent | development | 3,021 | 75 | 2.48% | 37.88% (75 of 198) | 20 | 43.48% (20 of 46) |
| | held-out | 3,022 | 78 | 2.58% | 32.10% (78 of 243) | 17 | 32.69% (17 of 52) |
| 3: an area fault counts without growth | development | 3,145 | 78 | 2.48% | 39.39% (78 of 198) | 21 | 45.65% (21 of 46) |
| | held-out | 3,285 | 84 | 2.56% | 34.57% (84 of 243) | 19 | 36.54% (19 of 52) |

Both changes were proposed from numbers of the development period: the first from the age of the bursts there, the second from the counts above. The held-out period was not hidden while they were made. Its numbers for the rules as they were stood on the same printouts, and for the second change so did a held-out count of the servers it would add (8 of 351 with a ticket, 2.3%). What the changed rules give on the held-out period was computed after each change was written. Engine 1 is the tree before pull request #86 and engine 2 the tree before #88. Engine 4 added two rules for errors that name no DIMM. This dataset has none, and engine 4 gives the numbers of engine 3 on it. Engines 5 and 6 added the rules of crash loops and the statements about a host that stopped; they need boots that ended in a fatal error, which this dataset does not record, and engine 6 gives the numbers of engine 4 on it.

With engine 1 the number of servers with "consider replacement" grew with every cut-off, from 1,711 at day 60 to 3,891 at day 210, because a burst never expired. Since engine 3 it stays between 1,016 and 1,129.

### Thresholds that were tried and left

One threshold at a time, run with engine 3. Precision and recall are those of "consider replacement" against tickets of type 1 to 3.

| Setting | servers with "consider replacement", development | precision | recall | servers, held-out | precision | recall |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| defaults | 3,145 | 2.48% | 39.4% | 3,285 | 2.56% | 34.6% |
| `spread.min_new=1` | 3,668 | 2.29% | 42.4% | 3,932 | 2.24% | 36.2% |
| `spread.min_new=3` | 2,850 | 2.60% | 37.4% | 2,943 | 2.75% | 33.3% |
| `spread.min_new=5` | 2,454 | 2.97% | 36.9% | 2,570 | 3.04% | 32.1% |
| `spread.min_new=10` | 1,996 | 3.26% | 32.8% | 2,174 | 3.45% | 30.9% |
| `spread.min_new=20` | 1,681 | 3.63% | 30.8% | 1,859 | 3.50% | 26.7% |
| `spread.days=14` | 2,317 | 3.06% | 35.9% | 2,404 | 2.87% | 28.4% |
| `spread.days=60` | 4,359 | 2.20% | 48.5% | 4,472 | 2.24% | 41.2% |
| `rule.row.min_errors=5` | 3,329 | 2.49% | 41.9% | 3,473 | 2.48% | 35.4% |
| `rule.row.min_errors=20` | 2,789 | 2.51% | 35.4% | 2,913 | 2.64% | 31.7% |
| `rule.row.min_errors=50` | 2,280 | 2.54% | 29.3% | 2,377 | 2.73% | 26.7% |
| `trend.recent_days=3` | 2,853 | 2.70% | 38.9% | 2,971 | 2.79% | 34.2% |
| `trend.recent_days=14` | 3,634 | 2.20% | 40.4% | 3,800 | 2.29% | 35.8% |
| `trend.rise_ratio=1.5` | 3,213 | 2.43% | 39.4% | 3,347 | 2.54% | 35.0% |
| `trend.rise_ratio=3` | 3,115 | 2.50% | 39.4% | 3,238 | 2.59% | 34.6% |
| `trend.rise_min_recent=5` | 3,212 | 2.46% | 39.9% | 3,390 | 2.48% | 34.6% |
| `trend.rise_min_recent=30` | 3,007 | 2.49% | 37.9% | 3,108 | 2.54% | 32.5% |
| `storm.count=50` | 3,148 | 2.48% | 39.4% | 3,291 | 2.55% | 34.6% |
| `storm.count=300` | 3,134 | 2.49% | 39.4% | 3,272 | 2.54% | 34.2% |
| `cluster.scatter_min_cells=3` | 3,164 | 2.47% | 39.4% | 3,325 | 2.53% | 34.6% |
| `cluster.scatter_min_cells=10` | 3,128 | 2.49% | 39.4% | 3,248 | 2.56% | 34.2% |

No value stands out. A higher `spread.min_new` or a shorter `spread.days` raises precision and lowers recall in even steps; `rule.row.min_errors` removes servers and tickets in the same proportion; the other thresholds change little. The defaults stay.

### Speed

Finding the lines of a bank took a pass over all its cells for every line. The analysis of the dataset took about 180 seconds; it takes 52 seconds since that step keeps counts in two heaps. The findings are the same, byte for byte in the report.

## Trying it without the dataset

`my eval -generate fleet` writes a made-up fleet in the format of the dataset, and `my eval -data fleet` evaluates it. The rows follow simple recipes, such as a row that fails at more and more columns before a ticket. The excerpts below come from these two commands. They show the layout of the text report and say nothing about real DIMMs.

```
Findings and what followed, development period (cut-off days 60, 90, 120)

  By finding for the server:
    finding                              servers  UE ticket  CE ticket  misc ticket  type 0 ticket  types 1-3     UE
    observe                                  312          0          0            0              0      0.00%  0.00%
    consider replacement                      59          2          0            0              0      3.39%  3.39%
    replace now                                0          0          0            0              0          -      -
    hold replacement                          19          0          0            0              0      0.00%  0.00%
    all                                      390          2          0            0              0      0.51%  0.51%
    reference: error in the last 7 days       80          2          0            0              0      2.50%  2.50%
  replace now: no server
  consider replacement, against a ticket of type 1, 2 or 3: precision 3.39% (2 of 59, 95% interval 0.93% to 11.54%); recall 100.00% (2 of 2, 95% interval 34.24% to 100.00%)
```

"types 1-3" is the share of the row with a ticket of failure type 1, 2 or 3 in the window, and "UE" the share with a ticket for an uncorrectable error.

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
- Without the DRAM device and the failing bits, a fault within one rank cannot be told to be in one device or in several, and the check for several failing data lines never applies.
- The layout of `memoryid` on the board is an assumption taken from the scripts of the authors. R-SYS-01, which looks at the DIMMs of one channel, depends on it.
- The rules about uncorrected errors, firmware marks, repairs, page offlines, moved DIMMs and reseats cannot be evaluated on this dataset at all.
- Four servers with 56.6% of the rows are not in the results. Their DIMMs scatter errors over more places than the run holds in memory.
- "hold replacement" says that a cause outside the DIMM is suspected. Tickets cannot confirm or refute that, so its numbers are given without a reading.
- The two changed conditions were chosen on the first five months of this dataset and checked on the last three. They have not been checked on another fleet.
