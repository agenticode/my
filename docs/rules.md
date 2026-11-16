# Rules

A finding is a grade with the rules that led to it. This page lists every rule: when it fires, which thresholds it uses, and what follows from it. `my help rules` and `my help thresholds` print the short form.

A grade is a finding. The thresholds behind it are defaults; [evaluation.md](evaluation.md) says what they were compared with.

## Grades

| Grade | Exit code | Meaning |
| --- | --- | --- |
| ok | 0 | no errors on record |
| observe | 1 | errors are on record and nothing points at a growing fault |
| consider replacement | 2 | a fault is growing, or mitigations are being used up |
| replace now | 3 | an uncorrected error, a module that firmware gave up, or no repair left |
| hold replacement | 4 | check before a DIMM is pulled: a cause outside the DIMM is suspected, or the records do not say which DIMM |

A finding names every rule that fired. Its grade is the worst among them, and hold replacement outranks the others: what to check before a DIMM is pulled has to be read first.

The grade of a host is the worst grade among its DIMMs, its GPUs and the host finding. One exception: a DIMM or GPU that is "replace now" on records of its own stands before a hold of the host finding, which cannot name a DIMM and does not question that one. DIMMs that are no longer in the host do not count.

## From errors to faults

Rules speak about faults. A fault is a group of errors of one DIMM with one cause, and its mode says how far it reaches. What a platform reports decides how far the classification goes ([compatibility.md](compatibility.md)).

| Records | What is made of them |
| --- | --- |
| bank, row and column known (grade A) | every mode below |
| physical address only (grade B) | a repeating address is a single-bit fault, a run of addresses within `cluster.row_window_bytes` a row fault, both marked "estimated"; wider groups stay undetermined |
| a count per DIMM (grade C) | one fault of undetermined mode |

| Mode | Condition |
| --- | --- |
| single-bit | errors at one cell. It counts as failing again after `cluster.repeat_gap_hours` between two errors, or at `cluster.repeat_count` errors however close together |
| row | errors in one row of a bank at several columns, where chance does not explain the alignment (`cluster.line_chance`) |
| multi-row | a row and its neighbours, the neighbours with at least `cluster.multi_row_share` of the errors |
| column | errors in one column of a bank at several rows |
| bank | at least `cluster.scatter_min_cells` places scattered over one bank, first seen within `cluster.scatter_window_days` |
| device | such places in two or more banks of one device, with at least `cluster.wider_share` of them outside the busiest bank |
| DIMM | such places on two or more devices |
| system issue | set by R-SYS-01, R-SYS-02 or R-SYS-03 for errors whose cause lies outside the DIMM |
| undetermined | the records do not carry enough of the place |

Which rows are neighbours is taken from the row numbers, unless a vendor analyzer says otherwise ([plugin-contract.md](plugin-contract.md)). On a platform that does not report the device, scatter over several banks of one rank is called a device fault and marked "estimated".

Three measurements feed the rules:

| Measurement | Definition |
| --- | --- |
| trend | the daily rate of the last `trend.recent_days` against the rate of the `trend.baseline_days` before. "Rising" needs a ratio of `trend.rise_ratio` and `trend.rise_min_recent` recent errors. "New" means errors in the recent window and none in the baseline window; it counts as growth from `trend.rise_min_recent` errors. No trend is stated before the ledger covers `trend.min_baseline_days` of the baseline window |
| spread | places that failed for the first time within `spread.days`. From `spread.min_new` new places a fault is spreading |
| burst | `storm.count` errors within `storm.minutes` |

A rule that compares DIMMs counts unconfined errors: errors outside faults that stay within one cell, row, column or bank and came back there. A fault inside a DRAM device confines itself that way. A bad contact or a bad trace corrupts whatever address is being read.

## Replace now

### R-UE-01

An uncorrected error on the DIMM. Every record of an uncorrected error counts, fatal ones included, except an error the patrol scrubber found that was not fatal (R-SCRUB-01). A record that does not say how the error was found counts here.

When a record shows that one of these errors stopped the host, a second line says so and names it with the record of the stop: the kernel's panic lines after the error record (`Kernel panic - not syncing: Fatal machine check`), or the next boot that read the record from a machine check bank or the boot error region. The grade is the same.

Next step: replace the DIMM and attach a case bundle to the return.

### R-FW-01

The BMC or the BIOS marks the DIMM as failed or disabled. The records are a "memory device disabled" event of the BMC event log, the state and health of a Redfish memory resource, and the health record of a CXL device that says its hardware needs replacement.

### R-REP-01

Repair resources are used up: a post package repair or a row remap failed, or sparing is in use, which leaves no spare. For a GPU the rule fires when a row remapping failed: XID 64 in the kernel log, or the failure flag in the state `nvidia-smi` reports.

## Consider replacement

### R-ROW-03

A fault wider than a cell with at least `rule.row.min_errors` errors, when one of these holds:

- its errors are rising, or it is new (trend);
- it is spreading (spread);
- it had a burst within the last `trend.recent_days`;
- it covers a bank or more (bank, device or DIMM mode). Such a fault does not have to grow: no row repair reaches it.

A fault of undetermined mode never fires this rule; its finding is "observe" with the step to collect located records. When the errors of the fault involve two or more data lines of one device, the finding says so.

Next step: plan a replacement for the next maintenance window and watch the fault until then.

### R-OFF-02

Pages keep being taken offline: at least `rule.offline.min_pages` pages on at least `rule.offline.min_days` different days, the last within `rule.offline.recent_days` of the reference time. A page counts once however many sources report it, and an offline that failed does not count.

### R-SCRUB-01

The patrol scrubber found an uncorrected error that was not fatal: the memory was read by the scrubber before anything used the data.

### R-REP-02

Errors came back at the place of a repair: at least one error at the repaired row, device or rank after a post package repair, a sparing or a row remap. When the repair was a soft PPR and the host restarted before the errors came back, a check says so: a soft PPR is lost at power off.

## Observe

### R-SOFT-01

Corrected errors that no rule above spoke about: stray errors at places that did not fail again, one cell that fails repeatedly, a fault that is neither rising nor spreading, or errors whose records carry no row and column. It is not added to a finding that already holds a rule of "consider replacement" or "replace now".

Next step: none; look again when the count grows or a new place appears. For errors without rows and columns: collect located records.

### R-QUIET-01

A place failed repeatedly and has been quiet since a page offline or a repair: no error of the fault after it, for at least `rule.quiet.days`. Before that time has passed, a check says how many days are still needed.

### R-HOST-02

Corrected errors or page offlines of the host that no record ties to a DIMM. It is a rule of the host finding (see below).

## Hold replacement

### R-SYS-01

Two or more DIMMs of one channel each have at least `rule.system.min_errors` unconfined errors, in periods that lie at most `rule.system.overlap_days` apart. The channel, or what the DIMMs share, is the first suspect.

Next step: do not pull a DIMM yet; swap one DIMM with a DIMM of another channel and see whether the errors follow it.

### R-SYS-02

A DIMM was moved and the errors stayed in the slot: the slot showed at least `rule.system.min_errors` unconfined errors with one DIMM and again with the next DIMM in it.

Next step: do not return the DIMM; inspect the slot, its channel and the board.

### R-SYS-03

The errors stopped after a reseat: at least `rule.system.min_errors` unconfined errors before the DIMM was reseated or moved to another slot, and none for `rule.quiet.days` after. A few stray errors afterwards do not undo it when they are fewer than `rule.system.min_errors` and at most a tenth of what there was before.

Next step: keep the DIMM in service and watch it; the contact may have been the cause.

### R-MAP-01

The slot names of the sources disagree: a record that was tied to one slot carries a label, an SMBIOS handle, an EDAC position or socket and channel numbers that belong to another slot.

Next step: before a DIMM is pulled, confirm which physical slot the errors belong to.

### R-HOST-01

Uncorrected errors of the host that no record ties to a DIMM. It is a rule of the host finding. As under R-UE-01, a second line says which of them stopped the host.

## The host finding

Some records name no DIMM: machine checks of a host whose memory controller has no EDAC driver, firmware records without a location, pages the kernel took offline by address. They are in the finding of no DIMM. The host finding speaks about them, one finding per host next to those of its DIMMs.

| Rule | Grade | Fires when |
| --- | --- | --- |
| R-HOST-01 | hold replacement | at least one uncorrected error names no DIMM |
| R-HOST-02 | observe | at least one corrected error names no DIMM, or at least one page was taken offline and is tied to no DIMM |

The finding lists why the records name no DIMM (no slot in them, or a slot that held two DIMMs with an unknown time of the swap), says where no EDAC driver reports a memory controller, and counts the pages a program asked the kernel to take offline, which is what a test of that path does. Its next step is to find the DIMM through the EDAC driver of the memory controller or the event log of the BMC. On a virtual machine the next step names the hypervisor host.

## What a finding carries beside its rules

| Part | Content |
| --- | --- |
| evidence | one statement per thing a rule saw, with the ledger records behind it: the first and the last when there are more than `evidence.max_records` |
| checks | tests against other explanations. "supports" speaks for the finding, "weakens" against it, "open" could not be tested and names what would settle it. Examples: the neighbours on the channel have no errors; no slot move or reseat is on record; `check.host_wide_min_dimms` DIMMs of the host have unconfined errors in the same period |
| missing | data the finding did not have: a source that could not be read, no serial number, no rows and columns, no device, too little history for a trend, a virtual machine, a BIOS setting that keeps errors out of the records (patrol scrub off, no corrected errors logged to the BMC) |
| next | the step that follows |
| confidence | a number from 5 to 95; 75 and more reads "high", 50 and more "medium" |

The confidence starts at a value of the rule that set the grade and moves with the data:

| Rule | Start | Rule | Start |
| --- | --- | --- | --- |
| R-UE-01 | 90 | R-QUIET-01 | 70 |
| R-FW-01 | 90 | R-SOFT-01 | 65 |
| R-REP-01 | 85 | R-SYS-02 | 80 |
| R-SCRUB-01 | 80 | R-MAP-01 | 75 |
| R-REP-02 | 80 | R-SYS-01 | 65 |
| R-ROW-03 | 75 | R-SYS-03 | 60 |
| R-OFF-02 | 70 | R-HOST-01 | 85 |
| no rule (ok) | 85 | R-HOST-02 | 70 |

| Condition | Change |
| --- | --- |
| the deciding errors are known as counts only | -15 |
| they are known by address only | -10, and -15 when the mode is an estimate as well |
| the mode of the deciding fault is an estimate | -10 |
| the DIMM has no serial number on record | -10 |
| at least half of the errors fall at times when no inventory shows the DIMM in its slot | -5 |
| checks that support the finding | +5 each, at most +10 |
| checks that speak against it | -10 each, at most -30 |
| checks that could not be made | -2 each, at most -6 |
| sources that could not be read in full | -5 each, at most -15 |
| "ok" without any source of error records | -45 |

`--audience fa` of a report and `my why` list these steps for a finding.

## Thresholds

| Key | Default | Meaning |
| --- | --- | --- |
| `cluster.line_chance` | 0.01 | largest chance of an accidental alignment at which errors sharing a row or column are still a line |
| `cluster.repeat_gap_hours` | 24 | hours between two errors at one place from which the place counts as failing again |
| `cluster.repeat_count` | 10 | errors at one place from which the place counts as failing again, however close together |
| `cluster.multi_row_share` | 0.1 | share of a row group's errors outside its busiest row from which it is a multi-row fault |
| `cluster.scatter_min_cells` | 5 | different places within the scatter window from which scattered errors form a fault |
| `cluster.scatter_window_days` | 30 | days within which scattered places must first appear to form a fault |
| `cluster.wider_share` | 0.1 | share of scattered places outside the busiest bank or device from which the fault is a device or DIMM fault |
| `cluster.row_window_bytes` | 65536 | address distance within which errors known by address only are taken to lie in one row |
| `trend.recent_days` | 7 | length of the recent window in days |
| `trend.baseline_days` | 30 | length of the baseline window before the recent window in days |
| `trend.min_baseline_days` | 7 | days of the baseline window the ledger must cover before a trend is stated |
| `trend.rise_ratio` | 2 | recent daily rate divided by baseline daily rate from which errors are rising |
| `trend.rise_min_recent` | 10 | errors in the recent window needed to call a trend rising |
| `spread.days` | 30 | days within which a place must have failed for the first time to count as new |
| `spread.min_new` | 2 | new places from which a fault is spreading |
| `storm.minutes` | 60 | length of the window a burst of errors is counted in, in minutes |
| `storm.count` | 100 | errors within the storm window from which a burst is a storm |
| `baseline.min_peers` | 5 | DIMMs of one part number, the DIMM included, needed to rank it among them |
| `rule.row.min_errors` | 10 | R-ROW-03: errors a row, column, bank or wider fault needs before its trend and spread count |
| `rule.offline.min_pages` | 3 | R-OFF-02: pages taken offline |
| `rule.offline.min_days` | 2 | R-OFF-02: different days on which pages were taken offline |
| `rule.offline.recent_days` | 7 | R-OFF-02: days before the reference time within which the last page was taken offline |
| `rule.quiet.days` | 7 | R-QUIET-01, R-SYS-03: days without errors after a repair, page offline or reseat |
| `rule.system.min_errors` | 5 | R-SYS-01, R-SYS-02, R-SYS-03: unconfined errors each side needs before it is compared |
| `rule.system.overlap_days` | 7 | R-SYS-01: days the error periods of two DIMMs of a channel may lie apart |
| `check.host_wide_min_dimms` | 3 | DIMMs of a host with unconfined errors in the same period from which a common cause is mentioned |
| `evidence.max_records` | 20 | record references listed with one statement |

`--set KEY=VALUE` changes one threshold for a run, and `--config FILE` reads `key = value` lines. A bundle records the thresholds its findings were computed with when they differ from the defaults, and `my open` says so.

"Recent" and "quiet since" are counted back from the reference time of a host: the time of its latest scan, or of its latest record in a case that was imported. A bundle therefore gives the same finding whenever it is opened.
