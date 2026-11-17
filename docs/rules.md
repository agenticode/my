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

The BMC or the BIOS marks the DIMM as failed or disabled. The records are a "memory device disabled" event of the BMC event log, the state and health of a Redfish memory resource, and the health record of a CXL device that says its hardware needs replacement. The rule also fires when the memory mirror failed over from the DIMM, and when the DIMM failed memory training in two different slots: the failure followed the module. A mark that a later record takes back (the module in use again) is history and fires nothing.

### R-REP-01

Repair resources are used up: a post package repair or a row remap failed, or sparing is in use, which leaves no spare. A CXL memory device says with each sparing how many spare resources are left: sparing that leaves spares is memory sparing and fires no rule here. A repair the firmware reports as failed counts whether it names a place or the module only; so do device sparing (ADDDC, SDDC) that failed or has no spare left, and a copy to a spare that failed. Device sparing in use does not fire the rule: it takes a device out of use and leaves the rest of the module's spare capacity. For a GPU the rule fires when a row remapping failed: XID 64 in the kernel log, or the failure flag in the state `nvidia-smi` reports.

### R-LOOP-01

Uncorrected errors at one place of the DIMM in at least `rule.loop.min_boots` boots of the window (see "Boots" below). One fatal error ends its boot, so on a hard fault every boot ends at the same place. What counts as one place depends on the records:

| Records | One place |
| --- | --- |
| bank, row and column known (grade A) | one cell, or one row or one column of a bank |
| physical address only (grade B) | one cache line, or addresses within `cluster.row_window_bytes`, taken as one row and marked "estimated" |
| a count per DIMM (grade C) | none: such records fire R-LOOP-02 |

A check supports the finding when no other DIMM of the host had an uncorrected error in these boots, and for grade A when the errors stay at one place: a fault of a contact or a trace corrupts whatever address is read. For a place known by address, a check speaks against it when the BIOS version or the DIMMs of the host changed between the boots: one address may then have been two cells.

Next step: replace the DIMM and attach a case bundle written from the kept ledger.

### R-LOOP-02

Uncorrected errors on the DIMM in at least `rule.loop.min_boots` boots of the window, at no place that repeats: the places differ, or the records name the DIMM only. A check stays open: a fault of one cell or row stays at its place, so the slot and the channel are not ruled out. When `my preflight` found on the host that a reset loses records, the finding lists those checks as missing.

Next step: before the DIMM is returned, swap it with a DIMM of another channel and see whether the stops follow it.

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

A repair that a firmware record reports names the module and no row in most logs. Such a repair stands for every fault of the module that had errors before it, and errors of one of those faults after it fire the rule; a fault first seen after the repair is a new fault and no recurrence. A repair that does not say whether it was soft or hard adds an open check, and so does a repair dated by a BMC whose clock was not compared with the host's when the errors came less than 15 hours later. Device sparing that engaged at a place is a repair of that place, and so is the memory sparing of a CXL device.

### R-FW-02

The firmware predicts a failure of the DIMM (predictive failure analysis, PFA), and no later record takes the prediction back. When the located records show errors at one cell only, a check weakens the finding: a repair of the row may be enough.

Next step: plan the replacement for the next maintenance window and attach a case bundle to the return.

## Observe

### R-SOFT-01

Corrected errors that no rule above spoke about: stray errors at places that did not fail again, one cell that fails repeatedly, a fault that is neither rising nor spreading, or errors whose records carry no row and column. It is not added to a finding that already holds a rule of "consider replacement" or "replace now".

Next step: none; look again when the count grows or a new place appears. For errors without rows and columns: collect located records.

### R-QUIET-01

A place failed repeatedly and has been quiet since a page offline or a repair: no error of the fault after it, for at least `rule.quiet.days`. Before that time has passed, a check says how many days are still needed. For a repair that names the module only, every fault of the module that had errors before it has to be quiet since. A repair that waits for the next boot (scheduled) is no repair yet: it fires no rule, and a check says that no record shows that it ran.

### R-HOST-02

Corrected errors or page offlines of the host that no record ties to a DIMM, and firmware records about memory that name no DIMM (a mirror that lost its redundancy, a sparing mode of the system). It is a rule of the host finding (see below).

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

### R-LOOP-03

Uncorrected errors tied to DIMMs in at least `rule.loop.scatter_boots` boots of the window, spread so that no DIMM had them in more than `rule.loop.scatter_share` of those boots. Every DIMM involved gets the rule, and the host finding carries the summary. A statement says what the DIMMs share:

| The DIMMs are on | Suspects named |
| --- | --- |
| one channel | the channel, or what the DIMMs share on it |
| one socket, several channels | the processor of that socket, its memory controller or its power |
| several sockets | the board, the power supply or the firmware |

A DIMM that had a confined fault of its own before the first of these boots gets a check against it. A DIMM that fires R-LOOP-01 or R-LOOP-02 as well keeps them, and hold replacement outranks them.

Next step: do not pull the DIMMs one after another; check the logs of the processor and the board, the power supply, and what changed before the first stop.


### R-FW-03

The DIMM failed memory training, the memory test or its initialisation at boot at least once, it has no uncorrected error and no fault located inside it (grade A or B) on record, and the failure was not seen with this module in a second slot. A module that fails training has shown no error of its own cells: a bad seat, a bent pin of the socket, the slot or a limit of the BIOS fit as well. When the failure follows the module into a second slot, R-FW-01 fires and this rule does not. When the slot failed training with another module before, a check supports the hold and names that module; when no reseat or slot change is on record since, an open check says so.

Next step: reseat the DIMM; if it fails again, swap it with a DIMM of another channel and see whether the failure follows it.

## The host finding

Some records name no DIMM: machine checks of a host whose memory controller has no EDAC driver, firmware records without a location, pages the kernel took offline by address. They are in the finding of no DIMM. The host finding speaks about them, one finding per host next to those of its DIMMs.

| Rule | Grade | Fires when |
| --- | --- | --- |
| R-HOST-01 | hold replacement | at least one uncorrected error names no DIMM |
| R-HOST-02 | observe | at least one corrected error names no DIMM, or at least one page was taken offline and is tied to no DIMM |
| R-LOOP-03 | hold replacement | the uncorrected errors of several boots are spread over DIMMs (the summary of the rule of the DIMMs) |

The finding lists why the records name no DIMM (no slot in them, or a slot that held two DIMMs with an unknown time of the swap), says where no EDAC driver reports a memory controller, and counts the pages a program asked the kernel to take offline, which is what a test of that path does. Its next step is to find the DIMM through the EDAC driver of the memory controller or the event log of the BMC. On a virtual machine the next step names the hypervisor host.

## Population rules

A vendor document says for a board how its DIMM slots are filled: which slots carry the modules for a given number of modules, which slot of a channel comes first, how the processors are balanced and what may stand together. A population rule holds the installed modules against such a statement. A population the vendor does not support can keep memory from training or lower its speed, and moving modules changes it, so the board shows it beside the findings of the DIMMs.

A population finding is no finding of a DIMM. It has no grade and changes no grade, ranking, confidence, check or exit code. Each one names the document and the page it rests on, and keeps the strength of the document's wording: required where the document says must, not supported or will not boot, recommended where it says should, recommended or for best performance.

A rule fires on a board only where the rule set of its board family states it ([compatibility.md](compatibility.md#slot-names-of-server-vendors) says how a board gets its family). A board gets no population check when no rule set matches it, or when the board is not placed in full; the board then says which of the two it is, once, among its lines of data quality. A rule whose input the ledger lacks (module type, ranks, rated speed or part number on older firmware, the status of a processor record) does not fire and is listed as not checked, with the number of modules that lack it.

### P-ORDER-01

The modules of a processor do not stand in the slots the vendor's table fills for that number of modules. The table is a fill order (n modules stand in the first n slots of the order that the board has) or a list of slots per number of modules. The finding names the empty slots the table fills, marked `+` on the board, and the modules in slots the table leaves empty, marked `-`. When the table lists no population for the number, nothing is compared, and one line says which numbers it lists. A document often gives one table per memory mode, or two sets for one number; modules that stand as another of these tables has them give one line and no mark. When the ledger holds the BIOS memory mode, the table of that mode alone counts. The strength is that of the table.

### P-FIRST-01

A channel holds a module in a later slot while the slot the vendor fills first is empty. Which slot comes first (position 0, or the highest position on boards whose documents fill the higher label number first) is part of the rule set.

### P-CPU-01

A processor that is installed holds no module.

### P-BAL-01

The installed processors hold different numbers of modules.

### P-MIX-01

Modules of types the vendor does not allow together stand in one system: RDIMMs with LRDIMMs, registered with unbuffered modules.

### P-MIX-02

In one channel the module with more ranks does not stand in the slot the vendor fills first.

### P-MIX-03

In one channel the module with more capacity does not stand in the slot the vendor fills first.

### P-MIX-04

Modules of different rated speed stand in one system, on a board whose documents ask for one speed.

### P-MIX-05

The modules of one channel carry different part numbers, on a board whose documents ask for identical modules.

### Rule sets

A rule set (`internal/population/sets.tsv`) holds for one board family of [compatibility.md](compatibility.md#board-families) what its public documents state: the slot of a channel they fill first, the table of slots per number of modules (a fill order, or the slots for each number the document lists), the tables of other memory modes and the second sets a document allows for one number, and the rules beside the table, each with the strength of the document's wording and the page it stands on. Places are written as channel and position, the channel counted from 0 among the channels of its processor. A family whose documents say nothing a rule can check has no rule set. The strength is required only where the cited words say must, not supported, cannot or will not, or forbid with "do not"; an instruction in the imperative is recommended. A rule set matches a board only through its family, so the board family decides which documents apply.

<!-- population-sets -->
| Rule set | First slot | Table | Order | Other tables | Rules | Documents |
| --- | --- | --- | --- | --- | --- | --- |
| `dell-13g-24slot` | position 0 | 1, 2, 4, 6, 8, 10, 12 modules | recommended | - | P-FIRST-01 recommended, P-BAL-01 recommended, P-MIX-01 required, P-MIX-03 recommended | `dell-e26s` 162-164, `dell-e26s` 160 |
| `dell-12g-24slot` | position 0 | 1, 2, 4, 6, 8, 10, 12 modules (independent) | recommended | 2, 6 modules (advanced-ecc) | P-FIRST-01 recommended, P-BAL-01 recommended, P-MIX-01 required, P-MIX-02 recommended | `dell-e14s` 44-46, `dell-r620-owners-manual` 43, `dell-t620-owners-manual` 50, `dell-e14s` 42 |
| `dell-12g-e5-2400-2s-12slot` | position 0 | 1, 2, 3, 4, 5, 6 modules | recommended | - | P-FIRST-01 recommended, P-BAL-01 required, P-MIX-01 required, P-MIX-02 recommended | `dell-e18s` 46-47, `dell-r520-owners-manual` 40, `dell-t420-owners-manual` 70, `dell-e18s` 45 |
| `dell-12g-e5-2400-1s-6slot` | position 0 | 1, 2, 3, 4, 6 modules | recommended | - | P-FIRST-01 recommended, P-MIX-01 required, P-MIX-02 recommended | `dell-e20s` 70, `dell-r320-owners-manual` 41, `dell-e20s` 69 |
| `dell-13g-12slot` | position 0 | 1, 2, 3, 4, 6, 8 modules | recommended | - | P-FIRST-01 recommended, P-BAL-01 recommended, P-MIX-01 required, P-MIX-03 recommended | `dell-e28s` 83-87, `dell-r530-owners-manual` 74, `dell-t430-owners-manual` 110, `dell-e28s` 82, `dell-t430-owners-manual` 109 |
| `dell-11g-18slot` | position 0 | order of 9 slots | recommended | - | P-FIRST-01 required, P-BAL-01 required, P-MIX-01 required, P-MIX-02 required | `dell-r710-owner-manual` 130,132-133, `dell-r710-technical-guide` 32, `dell-t710-guidebook-technical-guide` 26, `dell-r710-owner-manual` 129, `dell-r710-owner-manual` 130 |
| `dell-11g-12slot` | position 0 | order of 6 slots | recommended | - | P-FIRST-01 required, P-BAL-01 required, P-MIX-01 required, P-MIX-02 required | `dell-r610-owner-manual` 116,118-119, `dell-r610-technical-guide` 29, `dell-r610-owner-manual` 116, `dell-r610-owner-manual` 117 |
| `dell-11g-8slot` | position 0 | order of 4 slots | recommended | - | P-FIRST-01 recommended, P-BAL-01 required, P-MIX-01 required, P-MIX-02 required | `dell-r410-owner-manual` 119,121-122, `dell-r510-owners-manual` 117-118, `dell-r410-owner-manual` 119, `dell-r510-owners-manual` 115, `dell-r410-owner-manual` 120 |
| `dell-11g-x3400-6slot` | position 0 | order of 6 slots | recommended | - | P-FIRST-01 recommended, P-MIX-01 required, P-MIX-02 required | `dell-r310-owners-manual` 106-108, `dell-t310-owner-manual` 110, `dell-r310-owners-manual` 106, `dell-t310-owner-manual` 108, `dell-t310-owner-manual` 109 |
| `dell-11g-x3400-4slot` | position 0 | order of 4 slots | recommended | - | P-FIRST-01 recommended | `dell-r210-owner-manual` 27, `dell-t110-owners-manual` 80-81, `dell-t110-owners-manual` 80 |
| `dell-e3-4slot-lettered` | position 0 | - | - | - | P-FIRST-01 recommended | `dell-e10s` 69, `dell-t110-ii-owners-manual` 86 |
| `dell-12g-r220` | position 0 | 1, 2, 4 modules | recommended | - | P-FIRST-01 recommended, P-MIX-02 recommended | `dell-e10s-2` 49, `dell-e10s-2` 48 |
| `dell-13g-1s-4slot` | position 0 | 1, 2, 4 modules | recommended | - | P-FIRST-01 recommended, P-MIX-03 recommended | `dell-e33s` 58,60,71,86, `dell-e33s` 57 |
| `dell-14g-1s-4slot` | position 0 | order of 4 slots | recommended | - | P-FIRST-01 recommended, P-MIX-03 recommended, P-MIX-05 recommended | `dell-e57s` 42, `dell-r340-service-manual` 37, `dell-t140-service-manual` 53, `dell-t340-service-manual` 54 |
| `dell-15g-1s-4slot` | position 0 | order of 4 slots | recommended | - | P-FIRST-01 recommended | `dell-e80s` 41, `dell-r350-service-manual` 48 |
| `dell-14g-24slot` | position 0 | 4, 8 modules | required | - | P-FIRST-01 recommended, P-BAL-01 required, P-MIX-01 required, P-MIX-03 recommended, P-MIX-05 recommended | `dell-e38s` 69-70, `dell-r640-service-manual` 61, `dell-t640-service-manual` 76, `dell-xc640-service-manual` 75, `dell-xc740xd-service-manual` 84, `dell-e38s` 63, `dell-e38s` 63,69 |
| `dell-14g-16slot` | position 0 | 4, 8 modules | recommended | - | P-FIRST-01 recommended, P-BAL-01 required, P-MIX-01 required, P-MIX-03 recommended, P-MIX-05 recommended | `dell-e45s` 50-51, `dell-t440-service-manual` 98, `dell-r540-service-manual` 66-67, `dell-r740xd2-service-manual` 71-72, `dell-e45s` 48 |
| `dell-14g-epyc7001-32slot` | position 0 | 4, 8 modules | recommended | - | P-FIRST-01 recommended, P-BAL-01 required, P-MIX-01 required, P-MIX-03 recommended | `dell-e38s-2` 104-105, `dell-e38s-2` 102,104, `dell-e38s-2` 104 |
| `dell-15g-epyc-1s-16slot` | position 0 | order of 16 slots | recommended | - | P-FIRST-01 recommended | `dell-e45s-2` 49, `dell-r7515-service-manual` 57 |
| `dell-15g-epyc-2s-32slot` | position 0 | order of 16 slots | recommended | - | P-FIRST-01 recommended, P-BAL-01 required | `dell-e68s` 74 |
| `dell-15g-icx-32slot` | position 0 | order of 16 slots | recommended | - | P-FIRST-01 recommended, P-MIX-01 required | `dell-e70s` 100, `dell-r650-service-manual` 71, `dell-e70s` 101 |
| `dell-15g-icx-16slot` | position 0 | order of 8 slots | recommended | - | P-BAL-01 recommended | `dell-t550-service-manual` 108, `dell-e74s` 4,52, `dell-e74s` 52 |
| `dell-16g-epyc9004-2s-24slot` | position 0 | order of 12 slots | recommended | - | P-BAL-01 required, P-MIX-01 required | `dell-e94s` 176 |
| `dell-16g-epyc9004-1s-12slot` | position 0 | order of 12 slots | recommended | - | P-MIX-01 required, P-MIX-05 recommended | `dell-e97s` 128 |
| `dell-16g-spr-16slot` | position 0 | order of 8 slots | recommended | - | P-BAL-01 recommended | `dell-e87s` 105 |
| `dell-xr4510c` | position 0 | order of 4 slots | recommended | - | - | `dell-xr4510c-installation-service-manual` 60 |
| `dell-precision-7920-tower` | position 0 | 1, 2, 4, 6, 8, 12 modules | recommended | - | P-FIRST-01 recommended, P-CPU-01 recommended, P-BAL-01 recommended | `dell-d04x` 90, `dell-d04x` 89, `dell-d04x` 100 |
| `dell-precision-7820-tower` | position 0 | - | - | - | P-FIRST-01 recommended | `dell-d02t` 92 |
| `dell-precision-rack-7910` | position 0 | 1, 2, 4, 6, 8, 10 modules | recommended | - | P-FIRST-01 recommended, P-BAL-01 recommended, P-MIX-01 required, P-MIX-02 recommended, P-MIX-03 recommended | `dell-e31s` 21-22, `dell-e31s` 20 |
| `dell-precision-7920-rack` | position 0 | - | - | - | P-FIRST-01 recommended, P-BAL-01 recommended, P-MIX-01 required | `dell-e38s-3` 103 |
| `dell-precision-7960-rack` | position 0 | 1, 2, 4, 6, 8, 12, 16 modules | recommended | - | P-FIRST-01 recommended | `dell-e82s` 14-15, `dell-e82s` 14 |
| `dell-c4140` | position 0 | - | - | - | P-FIRST-01 recommended, P-BAL-01 required, P-MIX-01 required, P-MIX-03 recommended, P-MIX-05 recommended | `dell-e53s` 42, `dell-e53s` 43 |
| `dell-c6520` | position 0 | order of 8 slots | recommended | - | - | `dell-e64s` 5,58 |
| `dell-c6525` | position 0 | order of 8 slots | recommended | - | P-BAL-01 required | `dell-e63s` 3,24-25, `dell-e63s` 25 |
| `dell-c6220ii-c8220` | position 0 | 1, 2, 3, 4, 6, 8 modules | recommended | - | P-FIRST-01 required, P-MIX-01 required, P-MIX-04 recommended | `dell-c6220-owner-manual` 231-232, `dell-c8220-owners-manual` 162, `dell-c8220-owners-manual` 160 |
| `dell-c6320` | position 0 | 1, 2, 3, 4, 6, 8 modules | recommended | - | P-FIRST-01 recommended | `dell-b08s` 105 |
| `dell-xs23-ty3-spelled` | position 0 | - | - | - | P-FIRST-01 required, P-MIX-04 recommended | `dell-c6100-owner-manual` 77, `dell-c6100-owner-manual` 79 |
| `dell-c1100-cs24-ty` | position 0 | - | - | - | P-FIRST-01 required, P-BAL-01 required, P-MIX-02 required, P-MIX-04 recommended | `dell-c1100-owner-manual` 66, `dell-c1100-owner-manual` 63, `dell-c1100-owner-manual` 64 |
| `dell-pe-1950-2950` | position 0 | order of 8 slots | required | - | P-FIRST-01 recommended | `dell-1950-owner-manual` 64, `dell-2950-owners-manual` 89 |
| `dell-pe-2900` | position 0 | order of 12 slots | required | - | P-FIRST-01 recommended | `dell-2900-owner-manual` 84, `dell-2900-owner-manual` 83 |
| `dell-pe-sc1430` | position 0 | order of 4 slots | required | - | - | `dell-sc1430-owner-manual` 19,77 |
| `dell-pe-t300` | position 0 | 1, 2, 4, 6 modules | recommended | - | P-FIRST-01 recommended | `dell-t300-owner-manual` 111-112, `dell-t300-owner-manual` 112 |
| `dell-pe-840-860-r200` | position 0 | 1, 2, 4 modules | recommended | - | P-FIRST-01 recommended | `dell-860-owner-manual` 68, `dell-r200-owners-manual` 82, `dell-840-owners-manual` 76, `dell-r200-owners-manual` 81 |
| `dell-pe-2850-sc1425` | position 0 | - | - | - | P-FIRST-01 recommended | `dell-2850-installation-guide` 50, `dell-sc1425-troubleshooting-installation-guide` 49 |
| `dell-pe-r900` | position 0 | 1 modules | recommended | - | P-FIRST-01 required | `dell-r900-owner-manual` 94-95, `dell-r900-owner-manual` 92 |
| `dell-pe-r810` | position 0 | 2, 4, 6, 8 modules | recommended | - | P-FIRST-01 recommended, P-BAL-01 recommended, P-MIX-02 required | `dell-e05s` 102-103, `dell-e05s` 100, `dell-r810-technical-guide` 31, `dell-e05s` 101, `dell-r810-technical-guide` 32 |
| `dell-pe-r815-r715` | position 0 | 4, 8 modules | recommended | - | P-FIRST-01 recommended, P-BAL-01 recommended, P-MIX-01 required, P-MIX-02 recommended | `dell-e05s-2` 94-96, `dell-r715-owners-manual` 94, `dell-e05s-2` 92, `dell-r715-owners-manual` 92, `dell-e05s-2` 93, `dell-r715-owners-manual` 93 |
| `dell-pe-r515` | position 0 | 1, 2, 4 modules | recommended | - | P-FIRST-01 recommended, P-BAL-01 required, P-MIX-01 required, P-MIX-02 required | `dell-r515-owner-manual` 107, `dell-r515-owner-manual` 104, `dell-r515-owner-manual` 105, `dell-r515-owner-manual` 106 |
| `dell-pe-r820` | position 0 | 1, 2, 4, 6, 8, 10, 12 modules | recommended | - | P-FIRST-01 recommended, P-BAL-01 required, P-MIX-01 required, P-MIX-02 recommended | `dell-e21s` 48-51, `dell-e21s` 43, `dell-e21s` 46 |
| `hp-gen9-24slot` | position 0 | order of 12 slots | required | - | P-FIRST-01 recommended, P-BAL-01 recommended, P-MIX-01 required, P-MIX-02 recommended | `hpe-767927-009` 48, `hpe-768830-401a` 79, `hpe-768830-401a` 78, `hpe-767927-009` 41,47, `hpe-768830-401a` 69,77, `hpe-767927-009` 47 |
| `hp-gen8-24slot` | position 0 | order of 12 slots | recommended | - | P-FIRST-01 recommended, P-MIX-01 required, P-MIX-02 recommended | `hpe-dl380p-gen8-user-guide` 51, `hpe-664724-006` 49, `hpe-dl380p-gen8-user-guide` 50, `hpe-664724-006` 48, `hpe-dl380p-gen8-user-guide` 46,50, `hpe-664724-006` 42,48, `hpe-ddr3-gen8` 12, `hpe-ddr3-gen8` 11 |
| `hp-gen8-e5-2400-12slot` | position 0 | order of 6 slots | recommended | - | P-FIRST-01 recommended, P-MIX-01 required, P-MIX-02 recommended | `hpe-675786-006` 50, `hpe-676151-006` 64, `hpe-675786-006` 49, `hpe-676151-006` 63, `hpe-675786-006` 45,49, `hpe-676151-006` 59,63, `hpe-ddr3-gen8` 11 |
| `hp-gen10-24slot` | position 0 | 1, 2, 3, 4 modules | recommended | - | P-FIRST-01 recommended, P-BAL-01 recommended, P-MIX-01 required, P-MIX-02 recommended | `hpe-population-gen10-technical` 7,19, `hpe-population-gen10-technical` 4, `hpe-population-gen10-technical` 4,7, `hpe-population-gen10-technical` 5, `hpe-dl380-gen10-user-guide` 141-142 |
| `hp-ml350-gen10` | position 0 | 1, 2, 3, 4 modules | recommended | - | P-FIRST-01 recommended, P-MIX-01 required | `hpe-population-gen10-technical` 19, `hpe-population-gen10-technical` 4, `hpe-ml350-gen10-user-guide` 130-131 |
| `hp-bl460c-gen8` | position 0 | order of 8 slots | recommended | - | P-FIRST-01 recommended, P-BAL-01 recommended, P-MIX-01 required, P-MIX-02 recommended | `hpe-656396-004` 32-33, `hpe-656396-004` 32, `hpe-656396-004` 27,32, `hpe-ddr3-gen8` 11 |
| `hp-bl460c-gen9` | position 0 | order of 8 slots | recommended | - | P-FIRST-01 recommended, P-MIX-01 required, P-MIX-02 required | `hpe-768826-007` 45,47-48, `hpe-768826-007` 48, `hpe-768826-007` 43,47 |
| `hp-bl460c-gen10` | position 0 | 1, 2, 3, 4 modules | recommended | - | P-FIRST-01 recommended, P-MIX-01 required, P-MIX-02 recommended | `hpe-population-gen10-technical` 21, `hpe-population-gen10-technical` 4, `hpe-876833-006` 60, `hpe-population-gen10-technical` 5 |
| `hp-microserver-gen10` | position 0 | order of 2 slots | recommended | - | - | `hpe-gen10` 10, `hpe-872677-004` 48 |
| `hp-ml10-gen9` | position 0 | - | - | - | P-FIRST-01 recommended, P-MIX-02 required | `hpe-ml10-generation9-gen9` 17, `hpe-833836-073` 37 |
| `hp-dl60-gen9` | position 0 | order of 4 slots | recommended | - | P-MIX-01 required | `hpe-786245-003` 51,53, `hpe-786245-003` 49,53 |
| `hp-dl20-gen9` | position 0 | order of 4 slots | recommended | - | P-FIRST-01 recommended | `hpe-826318-003a` 67,69, `hpe-826318-003a` 69 |
| `hp-dl160-gen8` | position 0 | - | - | - | P-FIRST-01 recommended, P-BAL-01 recommended, P-MIX-01 required | `hpe-670519-006` 50, `hpe-670519-006` 49-50, `hpe-670519-006` 46,50 |
| `hp-dl385p-gen8` | position 0 | order of 12 slots | recommended | - | P-FIRST-01 recommended, P-BAL-01 recommended, P-MIX-01 required | `hpe-dl385p-gen8-user-guide` 54, `hpe-dl385p-gen8-user-guide` 53, `hpe-dl385p-gen8-user-guide` 56, `hpe-dl385p-gen8-user-guide` 51,53,56 |
| `hp-gen9-16slot` | position 0 | order of 8 slots | recommended | - | P-FIRST-01 recommended, P-MIX-01 required, P-MIX-02 required | `hpe-775786-004` 57, `hpe-775439-004` 86, `hpe-775439-004` 87, `hpe-775786-004` 52,57, `hpe-775439-004` 82,86 |
| `hp-dl120-gen9` | position 0 | order of 8 slots | recommended | - | P-FIRST-01 recommended, P-MIX-01 required, P-MIX-02 required | `hpe-781878-003` 59, `hpe-781878-003` 61, `hpe-781878-003` 57,61 |
| `hp-g6g7-18slot-lettered` | position 0 | order of 9 slots | recommended | - | P-FIRST-01 required, P-MIX-01 required, P-MIX-02 required | `hpe-495624-003` 45, `hpe-dl360-g7-user-guide` 41-42, `hpe-dl380-g7-user-guide` 49, `hpe-495624-003` 41-42 |
| `hp-dl360-g6-18slot-unlettered` | position 0 | order of 9 slots | recommended | - | P-FIRST-01 recommended, P-MIX-01 required, P-MIX-02 recommended | `hpe-517689-005` 42, `hpe-517689-005` 38-39 |
| `hp-z6-g4` | position 0 | order of 6 slots | recommended | - | P-FIRST-01 recommended, P-MIX-03 recommended | `hp-z6-g4-tech-wp` 21, `hp-z6-g4-quickspecs` 21-22 |
| `hp-z8-g4` | position 0 | order of 12 slots | recommended | - | P-FIRST-01 recommended, P-MIX-01 required, P-MIX-03 recommended | `hpe-z8-g4-workstation-white-paper` 19-20,24, `hpe-z8-g4-workstation-quickspecs` 21-23, `hpe-z8-g4-workstation-white-paper` 20, `hpe-z8-g4-workstation-quickspecs` 14,24 |
| `hp-dl320-g6-9slot` | position 0 | order of 9 slots | recommended | - | P-FIRST-01 required, P-MIX-01 required, P-MIX-02 required | `hpe-530514-002` 30, `hpe-530514-002` 26 |
| `hp-dl160-g6-18slot` | position 0 | order of 9 slots | required | - | P-FIRST-01 recommended, P-CPU-01 required, P-BAL-01 recommended, P-MIX-01 required, P-MIX-02 recommended | `hpe-dl160-g6-maintenance-service-guide` 75, `hpe-dl160-6-g6` 36-37, `hpe-dl160-6-g6` 36, `hpe-dl160-g6-maintenance-service-guide` 78 |
| `hp-dl180-g6-12slot` | position 0 | order of 6 slots | required | - | P-FIRST-01 recommended, P-CPU-01 required, P-BAL-01 recommended, P-MIX-01 required, P-MIX-02 recommended | `hpe-dl180-g6-maintenance-service-guide` 83, `hpe-dl180-6-g6` 38-39, `hpe-dl180-6-g6` 38 |
| `hp-bl460c-g6g7-12slot` | position 0 | order of 6 slots | recommended | - | P-FIRST-01 recommended, P-MIX-01 required, P-MIX-02 required | `hpe-501274-002` 31, `hpe-bl460c-g7-blade-user-guide` 32, `hpe-501274-002` 27 |
| `hp-dl120-g6-6slot` | position 0 | 2, 4, 6 modules | recommended | - | P-FIRST-01 recommended, P-MIX-01 required, P-MIX-02 required | `hpe-579574-004` 25, `hpe-579574-004` 23 |
| `hp-dl120-g7-4slot` | position 0 | - | - | - | P-FIRST-01 required | `hpe-638025-003a` 30 |
| `hp-dl385-g7-24slot` | position 0 | order of 12 slots | required | - | P-FIRST-01 recommended, P-MIX-01 required | `hpe-588708-001` 48, `hpe-588708-001` 49 |
| `hp-dl585-g7-48slot` | position 0 | order of 12 slots | required | - | P-FIRST-01 recommended | `hpe-617692-004` 43, `hpe-617692-004` 44 |
| `hp-bl685c-g7-32slot` | position 0 | order of 8 slots | recommended | - | P-FIRST-01 recommended | `hpe-593961-001` 30-31 |
| `hp-dl165-g7-24slot` | position 0 | order of 12 slots | required | - | P-FIRST-01 recommended | `hpe-601465-003` 64 |
| `hp-dl580-g7-cartridges` | position 0 | - | - | - | P-FIRST-01 required, P-MIX-02 required | `hpe-595656-005` 45 |
| `ibm-x3550m3-x3650m3-18slot` | position 0 | order of 9 slots | recommended | - | P-FIRST-01 recommended, P-MIX-02 recommended | `ibm-x3550-m3-4254-7944-installation` 64, `ibm-x3650-m3-4255-installation-guide` 132-133, `ibm-x3550-m3-4254-7944-installation` topic |
| `ibm-x3550m2-x3650m2-16slot` | position 0 | order of 8 slots | recommended | - | P-FIRST-01 recommended | `ibm-x3550-m2-4198-7946-installation` 69-70, `ibm-x3650-m2-4199-installation-guide` 105 |
| `ibm-x3400m3-16slot` | position 0 | - | - | - | P-FIRST-01 recommended | `ibm-x3400-m3-7378-7379-installation` 94 |
| `ibm-x3550m4-x3650m4-24slot` | position 0 | - | - | - | P-FIRST-01 recommended, P-MIX-02 recommended | `ibm-x3550-m4-7914-installation-x3550` topic |
| `ibm-x3300m4-12slot` | position 0 | - | - | - | P-FIRST-01 recommended, P-MIX-02 required | `ibm-x3300-m4-7382` topic |
| `ibm-flex-x220-12slot` | position 0 | - | - | - | P-FIRST-01 recommended | `ibm-x220-installation-service-guide-7906` topic |
| `ibm-flex-x240-24slot` | position 0 | - | - | - | P-FIRST-01 recommended, P-MIX-01 required | `ibm-x240-7863-8737-8738-8956` 605 |
| `ibm-bladecenter-hs23-16slot` | position 0 | - | - | - | P-FIRST-01 recommended | `ibm-hs23-installation-user-guide-7875` 33 |
| `ibm-idataplex-dx360m4-16slot` | position 0 | - | - | - | P-FIRST-01 recommended, P-MIX-01 required, P-MIX-02 recommended | `ibm-dx360-m4-7912-7913-user` 57, `ibm-dx360-m4-7912-7913-user` 56 |
| `ibm-x3250m3-6slot` | position 0 | 1, 2, 4, 6 modules | recommended | - | P-FIRST-01 recommended | `ibm-x3250-m3-4251-4252-4261` 48-50, `ibm-x3250-m3-4251-4252-4261` 50 |
| `ibm-x3250m4-4slot` | position 0 | 1, 2, 4 modules | recommended | - | P-FIRST-01 recommended | `ibm-x3250-m4-installation-service-guide` 60-61, `ibm-x3250-m4-installation-service-guide` topic |
| `ibm-x3200m2-4slot` | position 0 | 1, 2, 4 modules | required | - | P-FIRST-01 recommended, P-MIX-02 required | `ibm-x3200-m2-4367-4368-installation` 30, `ibm-x3200-m2-4367-4368-installation` 29-30 |
| `ibm-x3650-7979-12slot` | position 0 | 2, 4, 6, 8, 10, 12 modules | recommended | - | P-FIRST-01 recommended | `ibm-x3650-7979-user-guide` 83 |
| `intel-s5000-fbd-8slot` | position 0 | - | - | - | P-FIRST-01 recommended | `intel-s5000psl-s5000xsl-technical` 31,33 |
| `intel-s5000vsa` | position 0 | - | - | - | P-FIRST-01 required, P-MIX-04 recommended | `intel-s5000vsa-technical` 21 |
| `intel-s5000vcl` | position 0 | - | - | - | P-FIRST-01 required | `intel-s5000vcl-technical` 21 |
| `intel-s3210sh` | position 0 | - | - | - | P-FIRST-01 recommended | `intel-e14960-009` 39 |
| `intel-se7501wv2` | position 0 | - | - | - | P-FIRST-01 recommended, P-MIX-04 required | `intel-se7501wv2-technical` 25, `intel-board-se7501wv2-product-product-guide` 30 |
| `intel-s5520-12slot` | position 0 | 1, 2, 3, 4, 6 modules | recommended | 2, 4 modules | P-FIRST-01 required, P-MIX-01 required, P-MIX-02 required | `intel-e39529-013` 51, `intel-workstation-board-s5520sc-tps` 47, `intel-e39529-013` 49 |
| `intel-s5500hcv` | position 0 | processor 1: 1, 2, 3, 4, 6 modules | recommended | processor 1: 2, 4 modules | P-FIRST-01 required, P-MIX-01 required, P-MIX-02 required | `intel-e39529-013` 51, `intel-e39529-013` 49 |
| `intel-s5520ur` | position 0 | - | - | - | P-FIRST-01 recommended, P-MIX-01 required | `intel-e44031-012` 40,43, `intel-e44031-012` 43 |
| `intel-s5500bc` | position 0 | - | - | - | P-FIRST-01 required | `intel-e42249-009` 40,42 |
| `intel-s3420gp` | position 0 | 1, 2, 3, 4, 5, 6 modules | recommended | 2, 3, 4 modules; 1, 2, 3, 4, 5 modules; 3, 4 modules | P-FIRST-01 required, P-MIX-01 required | `intel-e65697-010` 34-35, `intel-e65697-010` 34 |
| `intel-s1200bt` | position 0 | 1, 2, 3, 4 modules | recommended | 1, 2, 3 modules; 2 modules | P-FIRST-01 required, P-MIX-01 required | `intel-s1200bt-technical` 33, `intel-s1200bt-technical` 32, `intel-s1200bt-technical` 31 |
| `intel-s1200v3rp` | position 0 | - | - | - | P-FIRST-01 required, P-MIX-01 required | `intel-s1200v3rp-technical` 30,33, `intel-s1200v3rp-technical` 30 |
| `intel-s1200sp` | last position | 1, 2, 4 modules | required | 1 modules | P-FIRST-01 recommended, P-MIX-01 required | `intel-s1200sp-technical` 33,36, `intel-s1200sp-technical` 33 |
| `intel-s2600gz` | position 0 | - | - | - | P-FIRST-01 required, P-MIX-01 required, P-MIX-02 required | `intel-s2600gz-technical` 36,38, `intel-s2600gz-technical` 38 |
| `intel-s2600cp-w2600cr` | position 0 | - | - | - | P-FIRST-01 required, P-MIX-01 required, P-MIX-02 required | `intel-s2600cp-p4000cp-technical` 52, `intel-board-s2600ip-workstation-tps` 45 |
| `intel-s4600lh2` | position 0 | - | - | - | P-FIRST-01 required, P-MIX-01 required, P-MIX-02 required | `intel-s4600lh2-t2-technical` 38,40, `intel-s4600lh2-t2-technical` 39 |
| `intel-s2600wt` | position 0 | - | - | - | P-FIRST-01 required, P-MIX-01 required, P-MIX-02 required, P-MIX-05 required | `intel-s2600wt-technical-tps` 40,44, `intel-s2600wt-technical-tps` 40, `intel-s2600wt-technical-tps` 38 |
| `intel-s2600cw` | position 0 | - | - | - | P-FIRST-01 required, P-MIX-01 required, P-MIX-02 required | `intel-s2600cw-technical` 39-40,43, `intel-s2600cw-technical` 39, `intel-s2600cw-technical` 39-40 |
| `intel-s2600wf` | position 0 | 1, 2, 3, 4, 6, 8, 12 modules | recommended | - | P-FIRST-01 required, P-MIX-01 required, P-MIX-02 required | `intel-s2600wf-technical` 56, `intel-s2600wf-technical` 54-55, `intel-s2600wf-technical` 54, `intel-s2600wf-technical` 55 |
| `intel-s2600st` | position 0 | 1, 2, 3, 4, 6, 8 modules | recommended | - | P-FIRST-01 required, P-BAL-01 recommended, P-MIX-01 required | `intel-s2600st-technical` 50, `intel-s2600st-technical` 48-49, `intel-s2600st-technical` 49 |
| `intel-m50cyp` | position 0 | 1, 2, 4, 6, 8, 12, 16 modules | recommended | 12 modules | P-FIRST-01 recommended, P-MIX-01 required, P-MIX-02 required | `intel-m50cyp2sb-technical` 48, `intel-m50cyp2sb-technical` 46 |
| `intel-m70klp` | position 0 | - | - | - | P-FIRST-01 recommended, P-MIX-01 required, P-MIX-02 required | `intel-m70klp-technical` 39 |
| `amd-ethanol-x` | last position | - | - | - | P-FIRST-01 recommended | `amd-population-7003` 18 |
| `amd-volcano` | position 0 | - | - | - | P-MIX-01 required | `amd-9004-population` 8 |
| `asus-p8b-m` | position 0 | - | - | - | P-FIRST-01 recommended | `asus-rs100-e7-pi2-user-guide` 28 |
| `asus-p8b-x-e` | last position | - | - | - | P-FIRST-01 required | `asus-ts300-e7-ps4-user-guide` 29 |
| `asus-p9d-4slot` | last position | - | - | - | P-FIRST-01 recommended | `asus-p9d-user-guide` 34, `asus-p9d-v-user-guide` 32, `asus-p9d-m-user-guide` 27,36 |
| `asus-p12r` | last position | 1, 2, 4 modules | recommended | - | P-FIRST-01 recommended | `asus-e28203` 31, `asus-rs300-e11-motherboard-user-guide` 32, `asus-p12r-e-user-guide` 31 |
| `asus-z9-16slot` | position 0 | - | - | - | P-FIRST-01 recommended | `asus-z9pe-d16-10g-user-guide` 34 |
| `asus-z10-16slot` | position 0 | - | - | - | P-FIRST-01 required | `asus-e13683` 32 |
| `asus-z9pr-d12` | position 0 | - | - | - | P-FIRST-01 recommended | `asus-rs700-x7-ps4-user-guide` 27 |
| `asus-z9na-d6` | position 0 | 1, 2, 3 modules | recommended | - | - | `asus-z9na-d6-user-manual` 33-34 |
| `asus-z11pg-d16` | position 0 | - | - | - | P-FIRST-01 recommended | `asus-e16560` 38 |
| `asus-z11pg-d24` | position 0 | - | - | - | P-FIRST-01 recommended | `asus-e21686` 32 |
| `asus-prows-c621-64l` | position 0 | - | - | - | P-FIRST-01 recommended | `asus-e16276` 20 |
| `asus-k14pp-d24` | position 0 | 2, 4, 6, 8, 10, 12 modules | recommended | - | - | `asus-e28170` 47-48 |
| `lenovo-thinkstation-p920` | position 0 | 1, 2, 3, 4, 6, 8 modules | recommended | - | P-FIRST-01 recommended, P-BAL-01 recommended, P-MIX-03 recommended | `lenovo-p920-p720` 6, `lenovo-p920-machine-30bd-user-guide` 20,117, `lenovo-p920-machine-30bd-user-guide` 118, `lenovo-p920-p720` 5 |
| `lenovo-thinkstation-p720` | position 0 | 1, 2, 3, 4, 6 modules | required | - | P-BAL-01 recommended, P-MIX-03 recommended | `lenovo-p920-p720` 8, `lenovo-p720-machine-30ba-user-guide` 20,139 |
| `lenovo-thinksystem-sr590-sr570-16slot` | position 0 | 1, 2, 3, 4, 5, 6, 7, 8 modules | required | - | P-FIRST-01 recommended, P-MIX-01 required, P-MIX-02 recommended, P-MIX-03 recommended | `lenovo-sr590-7x98-7x99-installation-installation` topic |
| `lenovo-thinksystem-sr650-sr630-24slot` | position 0 | 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12 modules | required | - | P-FIRST-01 recommended, P-MIX-03 recommended | `lenovo-sr650-7x05-7x06-installation` topic |
| `lenovo-thinksystem-sr530-12slot` | position 0 | 1, 2, 3, 4, 5, 6 modules | required | - | P-MIX-01 required | `lenovo-sr530-7x07-7x08-installation` topic, `lenovo-sr530-xeon-sp-product-guide` topic |
| `lenovo-thinksystem-sr650v2-sr630v2-32slot` | position 0 | 1, 2, 4, 6, 8, 12, 16 modules | required | - | P-FIRST-01 recommended, P-CPU-01 required, P-MIX-02 required | `lenovo-sr650-v2-installation` topic |
| `lenovo-thinksystem-st650v2-32slot` | position 0 | 1, 2, 4, 6, 8, 12, 16 modules | recommended | - | P-FIRST-01 recommended, P-CPU-01 required, P-BAL-01 required, P-MIX-01 required, P-MIX-04 recommended | `lenovo-st650-v2-technical` topic |
| `lenovo-thinksystem-sr645-32slot` | position 0 | order of 16 slots | recommended | - | P-FIRST-01 required, P-BAL-01 recommended, P-MIX-02 recommended | `lenovo-sr645-installation` topic |
| `lenovo-thinksystem-sr950v3-128slot` | position 0 | - | - | - | P-FIRST-01 recommended, P-CPU-01 required, P-BAL-01 required, P-MIX-04 required | `lenovo-sr950-v3-installation-installation` topic |
| `lenovo-thinksystem-sn550-24slot` | position 0 | 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12 modules | required | - | P-FIRST-01 required | `lenovo-sn550` topic |
| `lenovo-systemx-m5-24slot` | position 0 | order of 12 slots | recommended | - | P-FIRST-01 recommended | `lenovo-x3650-m5-5462-installation-service` topic |
| `lenovo-flex-x240m5-24slot` | position 0 | order of 12 slots | recommended | - | P-FIRST-01 recommended, P-MIX-01 required, P-MIX-02 recommended | `lenovo-x240-m5-9532-2951-installation` topic |
| `sun-x4170-x4270-m2` | position 0 | - | - | - | P-FIRST-01 recommended, P-BAL-01 recommended, P-MIX-02 recommended | `oracle-e22369-02` 83 |
| `sun-x4170-x4270-m3` | position 0 | order of 8 slots | recommended | - | P-FIRST-01 recommended, P-BAL-01 recommended, P-MIX-02 required, P-MIX-03 recommended | `oracle-e22313-13` 62, `oracle-e23396-15` 67, `oracle-e22313-13` 76, `oracle-e22313-13` 75 |
| `sun-x4-2` | position 0 | order of 8 slots | recommended | - | P-FIRST-01 recommended, P-BAL-01 recommended, P-MIX-01 required, P-MIX-02 recommended, P-MIX-03 recommended | `oracle-e38041-07` 66, `oracle-e38041-07` 82 |
| `sun-x5-2` | position 0 | order of 12 slots | recommended | - | P-FIRST-01 required, P-MIX-01 required, P-MIX-03 recommended | `oracle-e48320-10` 96, `oracle-e48320-10` 94 |
| `fujitsu-d2619-18slot` | position 0 | order of 9 slots | required | - | P-FIRST-01 recommended, P-MIX-02 required | `fujitsu-rx300-s5-guide` 30, `fujitsu-d2619-tx300-s6-technical-manual` 24 |
| `fujitsu-d2709-12slot` | position 0 | - | - | - | P-FIRST-01 required, P-MIX-01 required, P-MIX-03 recommended | `fujitsu-d2709-tx200-s5` 18, `fujitsu-d2709-tx200-s5` 17 |
| `fujitsu-d3031-12slot` | position 0 | - | - | - | P-FIRST-01 required, P-MIX-01 required | `fujitsu-rx200-s6-guide` 32 |
| `fujitsu-d2759-6slot` | position 0 | - | - | - | P-FIRST-01 required, P-MIX-01 required | `fujitsu-d2759-tx150-s7-technical-manual` 22 |
| `fujitsu-d2863-4slot` | position 0 | - | - | - | P-FIRST-01 required, P-MIX-01 required, P-MIX-02 required | `fujitsu-d2863-rx100-s6-technical-manual` 21 |
| `fujitsu-d3049-4slot` | last position | - | - | - | P-FIRST-01 recommended, P-MIX-03 recommended | `fujitsu-tx140-s1-maintenance-manual` 296, `fujitsu-d3049-tx140-s1-tx120-s3` 24 |
| `fujitsu-d3239-4slot` | position 0 | 1, 2, 3, 4 modules | recommended | - | P-FIRST-01 required, P-MIX-03 recommended | `fujitsu-tx1320-m1-maintenance-manual` 217, `fujitsu-tx1330-m1-maintenance-manual` 226 |
| `fujitsu-e5-2600-24slot-labelled` | position 0 | order of 12 slots | recommended | - | P-FIRST-01 required, P-MIX-01 required, P-MIX-02 required, P-MIX-03 recommended | `fujitsu-rx200-s8-maintenance-manual` 201, `fujitsu-rx200-s7-maintenance-manual` 239, `fujitsu-rx2530-m1-maintenance-manual` 229, `fujitsu-rx200-s8-maintenance-manual` 200 |
| `fujitsu-d2939-24slot-node-names` | position 0 | - | - | - | P-FIRST-01 required, P-MIX-01 required, P-MIX-02 required, P-MIX-03 recommended | `fujitsu-rx300-s8-maintenance-manual` 255 |
| `fujitsu-d3099-12slot-node-names` | position 0 | - | - | - | P-FIRST-01 recommended, P-MIX-01 required, P-MIX-03 recommended | `fujitsu-tx2540-m1-maintenance-manual` 265 |
| `fujitsu-d2119-fbdimm-8slot` | position 0 | 2, 4, 6, 8 modules | recommended | - | P-FIRST-01 recommended | `fujitsu-d2119-rx300-s3-technical-manual` 17, `fujitsu-rx300-s3-guide` 26 |
| `fujitsu-d3384-24slot` | position 0 | - | - | - | P-FIRST-01 required, P-MIX-01 required | `fujitsu-rx2540-m4-guide` 4, `fujitsu-rx2540-m4-guide` 16 |
| `fujitsu-d4129-24slot` | position 0 | - | - | - | P-CPU-01 required | `fujitsu-rx2450-m2-guide` 15 |
| `nec-r120b-1` | position 0 | order of 6 slots | recommended | - | P-FIRST-01 recommended | `nec-express5800-r120b-1-user-guide` 383 |
| `cisco-ucs-c220-c240-m4` | position 0 | - | - | - | P-FIRST-01 recommended, P-BAL-01 required, P-MIX-01 required, P-MIX-02 recommended | `cisco-c220-m4` 71, `cisco-c220-m4` 4,7, `cisco-c220-m4-installation-service-guide` 5-6, `cisco-c220-m4` 6, `cisco-c220-m4-installation-service-guide` 6, `cisco-c220-m4` 7 |
| `supermicro-x8dtu-12slot` | position 0 | 3, 6 modules | recommended | - | P-FIRST-01 recommended, P-MIX-04 recommended | `sm-mnl-1081` 30 |
| `supermicro-x8-dp-12slot` | position 0 | 3, 6 modules | required | - | P-FIRST-01 required | `sm-mnl-1062` 30, `sm-mnl-1062` 29 |
| `supermicro-x8da3-12slot` | position 0 | 3, 6 modules | recommended | - | P-FIRST-01 recommended | `sm-mnl-1031` 27 |
| `supermicro-x8dtl-6slot` | position 0 | 3 modules | recommended | - | - | `sm-mnl-1076` 30 |
| `supermicro-x8-dp-18slot` | position 0 | 3, 6, 9 modules | recommended | - | P-FIRST-01 required | `sm-mnl-1199` 36 |
| `supermicro-x8dah-18slot-before-pcb2` | position 0 | 3, 6, 9 modules | recommended | - | P-FIRST-01 required | `sm-mnl-1060` 33, `sm-mnl-1060` 11,33 |
| `supermicro-x8dah-18slot-pcb2` | position 0 | 3, 6, 9 modules | recommended | - | P-FIRST-01 recommended | `sm-mnl-1060` 34 |
| `supermicro-h8-g34-dp-16slot` | position 0 | 4, 8 modules | required | - | P-FIRST-01 required | `sm-mnl-h8dg` 29 |
| `supermicro-h8qg6-32slot` | position 0 | 4, 8 modules | required | - | P-FIRST-01 recommended | `sm-mnl-h8qg-6` 29 |
| `supermicro-h8qgl-16slot` | position 0 | 4 modules | required | - | - | `sm-mnl-h8qgl-6` 29 |
| `supermicro-h8sgl-8slot` | position 0 | 4, 8 modules | recommended | - | P-FIRST-01 recommended | `sm-mnl-h8sgl-f` 29 |
| `supermicro-h8scm-4slot` | position 0 | 2, 4 modules | recommended | - | P-FIRST-01 recommended | `sm-mnl-h8scm-f` 28 |
| `supermicro-h8dcl-8slot` | position 0 | 2, 4 modules | recommended | - | P-FIRST-01 recommended | `sm-mnl-h8dcl` 31 |
| `supermicro-x9dbl-6slot` | position 0 | 1, 2, 3 modules | required | - | P-MIX-01 required | `sm-mnl-1307` 34, `sm-mnl-1307` 36 |
| `supermicro-x9-dp-16slot` | position 0 | 2, 4 modules | required | - | P-FIRST-01 required, P-MIX-01 required | `sm-mnl-1278` 36, `sm-mnl-1278` 39 |
| `supermicro-x9-dp-24slot` | position 0 | 2, 4 modules | required | - | P-FIRST-01 required, P-MIX-01 required | `sm-mnl-1258` 36, `sm-mnl-1258` 39 |
| `supermicro-x9-dp-8slot` | position 0 | 2, 4 modules | required | - | - | `sm-mnl-1488` 40 |
| `supermicro-x9-up-8slot` | position 0 | order of 8 slots | recommended | - | P-FIRST-01 recommended | `sm-mnl-1284` 38 |
| `supermicro-x9sra-8slot` | position 0 | - | - | - | P-FIRST-01 recommended | `sm-mnl-1281` 30 |
| `supermicro-x10-dp-16slot` | position 0 | 2, 4 modules | recommended | - | P-FIRST-01 required, P-MIX-01 required, P-MIX-02 required | `sm-mnl-1542` 34, `sm-x10-memory-guide` 2, `sm-mnl-1557` 37, `sm-mnl-1542` 35 |
| `supermicro-x10dri-x10drh-16slot` | position 0 | - | - | - | P-FIRST-01 recommended, P-MIX-01 required | `sm-x10-memory-guide` 2, `sm-mnl-1491` 37 |
| `supermicro-x10-dp-24slot` | position 0 | 2, 4 modules | recommended | - | P-FIRST-01 required, P-MIX-01 required | `sm-mnl-1560` 37, `sm-x10-memory-guide` 2, `sm-mnl-1560` 38 |
| `supermicro-x10drg-24slot` | position 0 | - | - | - | P-FIRST-01 recommended, P-MIX-01 required | `sm-mnl-1780` 38, `sm-x10-memory-guide` 2, `sm-mnl-1780` 40 |
| `supermicro-x10-up-8slot` | position 0 | order of 8 slots | required | - | P-FIRST-01 required, P-MIX-01 required | `sm-mnl-1586` 34, `sm-mnl-1586` 34,36 |
| `supermicro-x10sra-8slot` | position 0 | - | - | - | P-FIRST-01 recommended, P-MIX-01 required | `sm-mnl-1643` 40, `sm-mnl-1643` 41 |
| `supermicro-x10srm-4slot` | position 0 | order of 4 slots | recommended | - | - | `sm-mnl-1845` 31,33 |
| `supermicro-x10sll-x10slm-4slot` | last position | - | - | - | P-FIRST-01 recommended, P-MIX-01 required | `sm-mnl-1428` 35, `sm-mnl-1428` 36 |
| `supermicro-x10sl7-4slot` | position 0 | - | - | - | P-MIX-01 required | `sm-mnl-1463` 31 |
| `supermicro-x10slh-x10slmplus-4slot` | last position | 2, 4 modules | recommended | - | P-FIRST-01 required, P-MIX-01 required | `sm-mnl-1464` 33,37, `sm-mnl-1464` 33, `sm-mnl-1464` 34 |
| `supermicro-x10sae-4slot` | last position | order of 4 slots | recommended | - | P-FIRST-01 required, P-MIX-01 required | `sm-mnl-1479` 30, `sm-mnl-1479` 31 |
| `supermicro-x10sdv-4slot` | position 0 | order of 4 slots | recommended | - | P-FIRST-01 recommended, P-MIX-01 required | `sm-mnl-1726` 35 |
| `supermicro-x11-lga1151-4slot-b2-first` | last position | 1, 2, 3 modules | recommended | - | P-FIRST-01 recommended | `sm-mnl-1778` 33-34, `sm-mnl-1778` 35 |
| `supermicro-x11-lga1151-4slot-a2-first` | last position | order of 4 slots | recommended | - | P-FIRST-01 recommended, P-MIX-01 required | `sm-mnl-2087` 30,34,37, `sm-mnl-2087` 34, `sm-mnl-2087` 36 |
| `supermicro-x11scl-if-2slot` | position 0 | 1, 2 modules | recommended | - | - | `sm-mnl-2088` 29 |
| `supermicro-x11ssv-q-2slot` | position 0 | order of 2 slots | required | - | P-MIX-01 required | `sm-mnl-1795` 27 |
| `supermicro-x11-dp-16slot` | position 0 | 1, 2, 3, 4, 5 modules | recommended | - | P-FIRST-01 recommended, P-MIX-01 required | `sm-mnl-1773` 16,37, `sm-mnl-1773` 33 |
| `supermicro-x11-dp-24slot` | position 0 | 1, 2, 3, 4, 5 modules | recommended | - | P-FIRST-01 recommended, P-MIX-01 required | `sm-x11-user-guide` 19, `sm-x11-user-guide` 6 |
| `supermicro-x11ddw-12slot` | position 0 | 1, 2, 3, 4, 5 modules | recommended | - | P-MIX-01 required | `sm-mnl-1907` 37, `sm-mnl-1907` 33 |
| `supermicro-x11dpl-8slot` | position 0 | 1, 2, 3 modules | recommended | - | P-MIX-01 required | `sm-mnl-1946` 33, `sm-mnl-1946` 30 |
| `supermicro-x11qph-48slot` | position 0 | 6, 12 modules | recommended | - | P-FIRST-01 recommended | `sm-mnl-1983` 35, `sm-mnl-1983` 33 |
| `supermicro-x11-up-8slot` | position 0 | 1, 2, 3, 4, 5 modules | recommended | - | P-FIRST-01 required | `sm-mnl-1900` 32-33, `sm-mnl-1900` 34 |
| `supermicro-x11-up-6slot` | position 0 | 1, 2, 3, 4, 5 modules | recommended | - | - | `sm-mnl-1939` 34 |
| `supermicro-b11spe-12slot` | position 0 | 2, 3, 4, 5 modules | recommended | - | P-FIRST-01 recommended | `sm-mnl-2135` 50 |
| `supermicro-x11sra-8slot` | position 0 | order of 8 slots | recommended | - | P-FIRST-01 recommended | `sm-mnl-2005` 31 |
| `supermicro-x11sdv-4slot` | position 0 | order of 4 slots | recommended | - | - | `sm-mnl-2019` 24-25 |
| `supermicro-a2sd-4slot` | position 0 | order of 4 slots | recommended | - | P-FIRST-01 required | `sm-mnl-1951` 27-28, `sm-mnl-1951` 28-29 |
| `supermicro-a1sa2-4slot` | position 0 | 2, 4 modules | recommended | - | P-FIRST-01 recommended | `sm-mnl-1600` 25 |
| `supermicro-a1sam-a1srm-4slot` | position 0 | order of 4 slots | recommended | - | P-FIRST-01 recommended, P-MIX-01 required | `sm-mnl-1561` 25-26, `sm-mnl-1561` 26, `sm-mnl-1561` 25 |
| `supermicro-x12dpi-18slot` | position 0 | - | - | - | P-MIX-01 required | `sm-x12-memory-guide` 4 |
| `supermicro-x12-dp-16slot` | position 0 | 1, 2, 4, 6, 8 modules | recommended | - | P-MIX-01 required | `sm-mnl-2239` 41,43, `sm-x12-memory-guide` 4 |
| `supermicro-x12dpu-32slot` | position 0 | 1, 2, 4, 6, 8, 12, 16 modules | recommended | - | P-FIRST-01 recommended, P-MIX-01 required | `sm-mnl-2234` 42, `sm-x12-memory-guide` 4 |
| `supermicro-x12-up-8slot` | position 0 | 1, 2, 4, 6, 8 modules | recommended | - | - | `sm-mnl-2270` 33 |
| `supermicro-x12sae-4slot` | last position | order of 4 slots | recommended | - | P-FIRST-01 recommended | `sm-mnl-2358` 32-33, `sm-mnl-2358` 32 |
| `supermicro-x12sdv-sp6f-4slot` | position 0 | order of 4 slots | recommended | - | P-FIRST-01 recommended | `sm-mnl-2362` 27, `sm-mnl-2362` 26-27 |
| `supermicro-x12sdv-spt8f-4slot` | position 0 | 1, 2, 4 modules | recommended | - | - | `sm-mnl-2440` 24,26 |
| `supermicro-x13-dp-16slot` | position 0 | 1, 2, 4, 6, 8 modules | recommended | 1, 2, 6 modules; 1, 6 modules; 1, 6 modules | - | `sm-mnl-2509` 62 |
| `supermicro-x13sew-8slot` | position 0 | 1, 2, 4, 6, 8 modules | recommended | 1, 2, 6 modules; 6 modules; 6 modules | - | `sm-mnl-2413` 33 |
| `supermicro-x13sae-4slot` | last position | order of 4 slots | recommended | - | P-FIRST-01 recommended | `sm-mnl-2425` 34 |
| `supermicro-x13san-2slot` | position 0 | - | - | - | P-FIRST-01 required | `sm-mnl-2517` 29 |
| `supermicro-h12-up-8slot` | position 0 | 1, 2, 4, 8 modules | recommended | - | - | `sm-mnl-2314` 31 |
| `supermicro-h12dsg-32slot` | last position | order of 16 slots | recommended | - | P-FIRST-01 required | `sm-mnl-2299` 33 |
| `supermicro-h13sae-mf-4slot` | position 0 | 1, 2, 4 modules | recommended | - | P-FIRST-01 recommended | `sm-mnl-2627` 33 |
| `supermicro-m11sdv-4slot` | last position | order of 4 slots | recommended | - | P-FIRST-01 recommended | `sm-mnl-2172` 27 |
| `supermicro-m12swa-8slot` | position 0 | 1, 2, 4, 6, 8 modules | recommended | - | - | `sm-mnl-2336` 32-33 |
| `supermicro-c7x99-oce-8slot` | position 0 | order of 8 slots | recommended | - | P-FIRST-01 recommended, P-MIX-01 required | `sm-mnl-1646` 40, `sm-mnl-1646` 41 |
| `supermicro-c9x299-pg300f-8slot` | position 0 | 1, 2, 4, 6, 8 modules | recommended | - | P-FIRST-01 recommended | `sm-mnl-2117` 30, `sm-mnl-2117` 31 |
| `supermicro-c7-c9-lga1151-4slot` | last position | order of 4 slots | recommended | - | P-FIRST-01 required, P-MIX-01 required | `sm-mnl-1813` 41, `sm-mnl-1813` 39,41, `sm-mnl-1813` 40 |
| `supermicro-c7-c9-lga1151-2slot` | position 0 | order of 2 slots | recommended | - | P-MIX-01 required | `sm-mnl-2018` 32-33,36, `sm-mnl-2018` 37 |
| `gigabyte-epyc-sp3-2s-16ch-2dpc` | last position | 16 modules | recommended | - | P-FIRST-01 recommended, P-BAL-01 recommended | `gigabyte-r182-z90-r182-z91-r182` 34, `gigabyte-g482-z54-hpc-manual` 35, `gigabyte-memory-population-guidelines-manual` 7-13, `gigabyte-memory-population-guidelines-manual` 14, `gigabyte-memory-population-guidelines-manual` 14-15 |
| `gigabyte-epyc-sp3-1s-8ch-2dpc` | last position | 2, 4, 8, 16 modules | required | - | P-FIRST-01 required | `gigabyte-mz32-ar0-user-manual` 14, `gigabyte-mz31-ar0-user-manual` 15, `gigabyte-r162-za0-user-manual` 14,31 |
| `gigabyte-ampere-mp32-8ch-2dpc` | position 0 | 1 modules | recommended | - | P-FIRST-01 required | `gigabyte-e252-p30-user-manual` 28 |
| `gigabyte-mx33-xeon-e2300-4slot` | position 0 | - | - | - | P-FIRST-01 required | `gigabyte-mx33-bs0-user-manual` 13 |
| `gigabyte-epyc3000-mj11-4slot` | last position | - | - | - | P-FIRST-01 required | `gigabyte-g431-mm0-user-manual` 26 |
| `gigabyte-am4-am5-mc1x-4slot` | last position | 1 modules | recommended | - | P-FIRST-01 required | `gigabyte-mc13-le0-mc13-le1-user` 14 |
| `gigabyte-mu71-su0-xeon-w3200-8slot` | position 0 | - | - | - | P-FIRST-01 required | `gigabyte-mu71-su0-user-manual` 13 |
| `gigabyte-c621-su8-8slot` | position 0 | - | - | - | P-FIRST-01 recommended | `gigabyte-c621-su8-user-manual` 12 |
| `gigabyte-mw50-sv0-e5-1s-8slot` | position 0 | - | - | - | P-FIRST-01 required | `gigabyte-mw50-sv0-user-manual` 18 |
| `gigabyte-e5-2s-4ch-3dpc-24slot` | position 0 | - | - | - | P-FIRST-01 required | `gigabyte-r280-f2o-r280-g2o-user` 21 |
| `gigabyte-mu70-su0-e5-1s-12slot` | position 0 | - | - | - | P-FIRST-01 required | `gigabyte-mu70-su0-user-manual` 17 |
| `gigabyte-md72-hb0-xeon-sp3-2s-16slot` | position 0 | - | - | - | P-FIRST-01 required | `gigabyte-md72-hb0-md72-hb1-user` 14-15 |
| `gigabyte-mu72-su0-xeon-w3300-8slot` | - | 1, 2, 4, 8 modules | recommended | - | - | `gigabyte-mu72-su0-user-manual` 14 |
| `asrock-am5-b650d4u-4slot` | last position | - | - | - | P-FIRST-01 recommended | `asrock-b650d4u-b650d4u-2l2t-b650d4u-2l2t` 16,32 |
| `asrock-am4-x570-4slot` | last position | - | - | - | P-FIRST-01 recommended | `asrock-x570d4i-2t-x570d4i-manual` 22,29 |
| `asrock-intel-ddr5-controller-4slot` | last position | - | - | - | P-FIRST-01 recommended | `asrock-z690d4u-2l2t-w680d4u-2l2t-w680d4u` 41, `asrock-ec262d4-4l-ec266d4-manual` 27 |
| `asrock-sp2c621d32lm3-32slot` | position 0 | 1, 2, 4, 6, 8, 12, 16 modules | recommended | - | P-FIRST-01 required | `asrock-sp2c621d32lm3-user-manual` 29-32, `asrock-sp2c621d32lm3-user-manual` 29-31 |
| `asrock-altrad8ud-8slot` | position 0 | 1, 2, 4, 8 modules | recommended | - | - | `asrock-altrad8ud-1l2t-user-manual` 30 |
| `msi-am5-d3051-d3052` | last position | - | - | - | P-FIRST-01 required | `msi-d3052-s3661-user-guide` 18 |
| `tyan-s7012` | position 0 | - | - | - | P-FIRST-01 recommended | `tyan-s7012-user-manual` 26 |
| `tyan-s7010` | position 0 | - | - | - | P-FIRST-01 required | `tyan-s7010-user-manual` 27 |
| `tyan-s4882` | position 0 | - | - | - | P-FIRST-01 required | `tyan-k8qs-s4882-user-manual` 27 |
| `huawei-rh-v3-16slot` | position 0 | order of 8 slots | recommended | - | P-MIX-01 required, P-MIX-05 required | `huawei-rh1288-v3-user-guide` 194,202, `huawei-rh2288-v3-user-guide` 216, `huawei-rh1288-v3-user-guide` 57, `huawei-rh2288-v3-user-guide` 67 |
| `huawei-rh2288hv3-24slot` | position 0 | order of 12 slots | recommended | - | P-MIX-01 required, P-MIX-05 required | `huawei-rh2288h-v3-user-guide` 225,234, `huawei-rh2288h-v3-user-guide` 80 |
| `huawei-v5-24slot` | position 0 | 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12 modules | recommended | - | P-BAL-01 recommended, P-MIX-01 required, P-MIX-05 required | `huawei-1288h-v5-user-guide` 32-33, `huawei-1288h-v5-user-guide` 31, `huawei-1288h-v5-user-guide` 29 |
| `lenovo-thinkserver-rd330` | position 0 | 2, 3, 4, 6 modules | recommended | - | P-FIRST-01 recommended, P-MIX-02 required | `lenovo-rd330-maintenance-manual` 79-80, `lenovo-rd330-maintenance-manual` 93 |
| `lenovo-thinkserver-rd450` | position 0 | order of 8 slots | recommended | - | P-FIRST-01 recommended, P-MIX-02 recommended, P-MIX-03 required | `lenovo-rd450-user-guide-maintenance-manual` 83-84, `lenovo-rd450-user-guide-maintenance-manual` 84, `lenovo-rd450-user-guide-maintenance-manual` 82 |
| `fujitsu-mx130s2-sample` | position 0 | - | - | - | P-FIRST-01 recommended, P-MIX-03 required | `fujitsu-mx130-s2-maintenance-manual` 180 |
| `inspur-nf5280m6-32slot` | position 0 | 1, 2, 4, 6, 8, 12, 16 modules | recommended | - | P-FIRST-01 recommended, P-MIX-01 required | `inspur-nf5280m-lm6-user-manual` 62, `inspur-nf5280m-lm6-user-manual` 59 |
| `inspur-sa5212m4-16slot` | position 0 | - | - | - | P-FIRST-01 required | `inspur-sa5212m4-sa5212m4-user-manual` 75 |
| `quanta-t22hf-s5hf-16slot` | position 0 | - | - | - | P-FIRST-01 recommended | `quanta-t22hf-1u-service-guide` 49 |
| `huawei-taishan200-2280-kunpeng920` | position 0 | order of 16 slots | recommended | - | P-FIRST-01 recommended, P-BAL-01 recommended, P-MIX-05 required | `huawei-taishan200-2280-dimm-rules` topic, `huawei-200-maintenance-service-guide-2280` 52 |
| `huawei-atlas500pro-3000` | position 0 | - | - | - | P-CPU-01 required, P-MIX-05 required | `huawei-500-maintenance-service-guide-3000` 29, `huawei-500-maintenance-service-guide-3000` 49 |
| `quanta-ocp-leopard-16slot` | position 0 | - | - | - | P-FIRST-01 required | `quanta-v3-1` 68 |
| `quanta-ocp-windmill-winterfell-16slot` | position 0 | - | - | - | P-FIRST-01 required | `quanta-v2-0` 23 |
| `hp-dl360-gen10plus-32slot` | position 0 | 1, 2, 4, 6, 8 modules | required | - | P-FIRST-01 recommended, P-MIX-02 recommended | `hpe-population-gen10-3rd-technical` 12, `hpe-population-gen10-3rd-technical` 3-4, `hpe-population-gen10-3rd-technical` 3 |
| `hp-dl385-gen10plus-32slot` | position 0 | 1, 2, 3, 4, 5 modules | recommended | - | P-FIRST-01 recommended, P-BAL-01 recommended, P-MIX-01 required, P-MIX-02 recommended | `hpe-population-gen10-gen10-7002-7003` 4,6,8, `hpe-population-gen10-gen10-7002-7003` 3, `hpe-population-gen10-gen10-7002-7003` 4 |
| `hp-dl380-gen11-32slot` | position 0 | 1, 2, 4, 6, 8, 12, 16 modules | required | - | P-FIRST-01 recommended, P-MIX-01 required | `hpe-population-gen11-5th-technical` 15, `hpe-population-gen11-5th-technical` 4,77, `hpe-dl380-gen11` 41,77 |
| `hp-dl160-gen10-16slot` | position 0 | 1, 2, 3, 4 modules | recommended | - | P-FIRST-01 recommended, P-MIX-01 required | `hpe-dl160-gen10` 31 |
| `hp-dl20-gen10plus-4slot` | position 0 | order of 4 slots | recommended | - | P-FIRST-01 recommended | `hpe-population-gen10-2300-technical` 3,5, `hpe-population-gen10-2300-technical` 3 |
| `hp-cray-xd665-24slot` | position 0 | - | - | - | P-MIX-01 required | `hpe-xd665` 7 |
| `gigabyte-mr92-fs1-32slot` | position 0 | 1, 2, 4, 6, 8, 12, 16 modules | required | - | P-FIRST-01 required | `gigabyte-r182-n20-r182-na0-3rd` 33, `gigabyte-r182-n20-r182-na0-3rd` 32-33 |
| `huawei-2288hv5-bc11spscb-unbranded` | position 0 | 1, 2, 3, 4, 6, 8, 12 modules | recommended | - | P-BAL-01 recommended, P-MIX-01 required, P-MIX-05 required | `huawei-2288h-v5-user-guide` 37, `huawei-2288h-v5-user-guide` 36, `huawei-2288h-v5-user-guide` 33 |
| `huawei-rh2288hv3-bc11hgsa-hve` | position 0 | - | - | - | P-FIRST-01 recommended | `huawei-rh2288h-v3-user-guide-2` 234 |
| `asrock-sp2c621d32tm3-32slot-inferit` | position 0 | - | - | - | P-FIRST-01 required | `asrock-sp2c621d32tm3-user-manual` 32-34 |
| `neousys-nuvo-2600-1sodimm` | - | order of 1 slots | recommended | - | - | `other-2600-user-manual` 11 |
| `ekf-sc9-toccata-tgl` | position 0 | - | - | - | P-FIRST-01 required, P-MIX-05 required | `other-631121-012` 1,88, `other-631121-012` 88 |
| `powerleader-psc621di-16f` | position 0 | order of 8 slots | recommended | - | P-FIRST-01 recommended, P-MIX-01 required | `other-rs5230-user-manual-pr2710p-manual` 25, `other-rs5230-user-manual-pr2710p-manual` 24 |
<!-- /population-sets -->

The pages are those of the PDF file; a web page is cited whole.

<!-- population-documents -->
| Document | Maker | Title | Number or revision | Date | Pages cited | Address |
| --- | --- | --- | --- | --- | --- | --- |
| `amd-9004-population` | amd | AMD EPYC 9004 Series Memory Population Recommendations | Publication 58269, Revision 1.1 | June 2023 | 8 | <https://docs.amd.com/api/khub/documents/zynTogiENLgLxWEd4075vQ/content> |
| `amd-population-7003` | amd | Memory Population Guidelines for AMD EPYC 7003 Series Processors | Publication 56873, Revision 1.00 | April 2023 | 18 | <https://docs.amd.com/api/khub/documents/0bglRXRnwmLeDr6L0vSZVA/content> |
| `asrock-altrad8ud-1l2t-user-manual` | asrock | ALTRAD8UD-1L2T User Manual | Version 1.20, Jul. 2025 (first published July 2023) | Jul. 2025 | 30 | <https://download.asrock.com/Manual/ALTRAD8UD-1L2T.pdf> |
| `asrock-b650d4u-b650d4u-2l2t-b650d4u-2l2t` | asrock | B650D4U / B650D4U-2L2T / B650D4U-2L2T/BCM User Manual | Version 1.30, Jun. 2026 (first published August 2022) | Jun. 2026 | 16,32 | <https://download.asrock.com/Manual/B650D4U.pdf> |
| `asrock-ec262d4-4l-ec266d4-manual` | asrock | EC262D4-4L / EC266D4-4L User Manual | Version 2.30 | Nov. 2026 | 27 | <https://download.asrock.com/Manual/EC266D4-4L.pdf> |
| `asrock-sp2c621d32lm3-user-manual` | asrock | SP2C621D32LM3 User Manual | Version 1.0 | June 2023 | 29-31, 29-32 | <https://download.asrock.com/Manual/SP2C621D32LM3.pdf> |
| `asrock-sp2c621d32tm3-user-manual` | asrock | SP2C621D32TM3 User Manual | Version 1.20 | 2024 | 32-34 | <https://download.asrock.com/Manual/SP2C621D32TM3.pdf> |
| `asrock-x570d4i-2t-x570d4i-manual` | asrock | X570D4I-2T / X570D4I-NL User Manual | Version 1.0 | March 2022 | 22,29 | <https://download.asrock.com/Manual/X570D4I-2T.pdf> |
| `asrock-z690d4u-2l2t-w680d4u-2l2t-w680d4u` | asrock | Z690D4U-2L2T / W680D4U-2L2T / W680D4U-1L / Z690D4U / W680D4U User Manual | Version 1.30 | Mar. 2026 | 41 | <https://download.asrock.com/Manual/W680D4U-1L.pdf> |
| `asus-e13683` | asus | Z10PE-D16 WS user guide | E13683, Revised Edition V5 | December 2017 | 32 | <https://dlcdnets.asus.com/pub/ASUS/mb/Socket2011-R3/Z10PE-D16_WS/Manual/E13683_Z10PE-D16_WS_UM_V5_WEB.pdf> |
| `asus-e16276` | asus | Pro WS C621-64L SAGE Series user guide | E16276, Revised Edition V3 | February 2020 | 20 | <https://dlcdnets.asus.com/pub/ASUS/mb/Socket3647/Pro_WS_C621-64L_SAGE_10G/MANUAL/E16276_Pro_WS_C621-64L_SAGE_Series_UM_V3_WEB.pdf> |
| `asus-e16560` | asus | ESC4000 G4 Series user guide (motherboard ASUS Z11PG-D16) | E16560, Revised Edition V9 | August 2022 | 38 | <https://dlcdnets.asus.com/pub/ASUS/server/ESC4000_G4/Manual/E16560_ESC4000_G4_Series_UM_V9_WEB.pdf> |
| `asus-e21686` | asus | ESC8000 G4 Series user guide (motherboard ASUS Z11PG-D24) | E21686, Revised Edition V12 | February 2023 | 32 | <https://dlcdnets.asus.com/pub/ASUS/server/ESC8000_G4/Manual/E21686_ESC8000_G4_UM_V12_WEB.pdf> |
| `asus-e28170` | asus | RS720A-E12-RS12 user guide (motherboard ASUS K14PP-D24) | E28170, Revised Edition V5 | May 2026 | 47-48 | <https://dlcdnets.asus.com/pub/ASUS/server/RS720A-E12-RS12/Manual/E28170_RS720A-E12-RS12_UM_V5_WEB.pdf> |
| `asus-e28203` | asus | P12R-M Series user guide | E28203, Revised Edition V7 | May 2026 | 31 | <https://dlcdnets.asus.com/pub/ASUS/server/P12R-M/Manual/E28203_P12R-M_Series_UM_V7_WEB.pdf> |
| `asus-p12r-e-user-guide` | asus | P12R-E Series user guide | E28201, Revised Edition V6 | May 2026 | 31 | <https://dlcdnets.asus.com/pub/ASUS/server/P12R-E_Series/Manual/E28201_P12R-E_Series_UM_V6_WEB.pdf> |
| `asus-p9d-m-user-guide` | asus | P9D-M Series user guide | E8096, First Edition | April 2013 | 27,36 | <https://dlcdnets.asus.com/pub/ASUS/mb/socket1150/P9D-M/Manual&AVL/E8096_P9D-M_series_VER8096.pdf> |
| `asus-p9d-user-guide` | asus | P9D-X user guide | E7959, First Edition | April 2013 | 34 | <https://dlcdnets.asus.com/pub/ASUS/mb/socket1150/P9D-X/Manual&AVL/E7959_P9D-X_for_web.pdf> |
| `asus-p9d-v-user-guide` | asus | P9D-V user guide | E7958, First Edition | April 2013 | 32 | <https://dlcdnets.asus.com/pub/ASUS/mb/socket1150/P9D-V/Manual&AVL/e7958_P9D-V_for_web.pdf> |
| `asus-rs100-e7-pi2-user-guide` | asus | RS100-E7/PI2 user guide (motherboard ASUS P8B-M) | E6489, First Edition | March 2011 | 28 | <https://dlcdnets.asus.com/pub/ASUS/server/RS100-E7(PI2)/Manual/E6489_RS100_E7_PI2_web.pdf> |
| `asus-rs300-e11-motherboard-user-guide` | asus | RS300-E11 user guide (motherboard ASUS P12R-M/SYS) | E28186, Revised Edition V10 | May 2026 | 32 | <https://dlcdnets.asus.com/pub/ASUS/server/RS300-E11/Manual/E28186_RS300-E11_UM_V10_WEB.pdf> |
| `asus-rs700-x7-ps4-user-guide` | asus | RS700-X7/PS4 user guide (motherboard ASUS Z9PR-D12/4L) | E7372, First Edition | April 2012 | 27 | <https://dlcdnets.asus.com/pub/ASUS/server/RS700-X7_PS4/Manual/e7372_rs700-x7_ps4_for_web.pdf> |
| `asus-ts300-e7-ps4-user-guide` | asus | TS300-E7/PS4 user guide (motherboard ASUS P8B-E/4L) | E6468, First Edition | March 2011 | 29 | <https://dlcdnets.asus.com/pub/ASUS/server/TS300-E7_PS4/Manual/e6468_TS300-E7_PS4.pdf> |
| `asus-z9na-d6-user-manual` | asus | Z9NA-D6 series user manual (Chinese edition) | C7280, first edition | April 2012 | 33-34 | <https://dlcdnets.asus.com/pub/ASUS/mb/Socket1356/Z9NA-D6/Manual/C7280_Z9NA-D6.pdf> |
| `asus-z9pe-d16-10g-user-guide` | asus | Z9PE-D16-10G Series user guide | E8158, First Edition | May 2013 | 34 | <https://dlcdnets.asus.com/pub/ASUS/mb/Socket2011/Z9PE-D16-10G/Manual/E8158_Z9PE-D16-10G_Series.pdf> |
| `cisco-c220-m4` | cisco | Cisco UCS C220 M4 High-Density Rack Server (Small Form Factor Disk Drive Model) Spec Sheet | REV E.12 | May 13, 2019 | 4,7, 6, 7, 71 | <https://www.cisco.com/c/dam/en/us/products/collateral/servers-unified-computing/ucs-c-series-rack-servers/c220m4-sff-spec-sheet.pdf> |
| `cisco-c220-m4-installation-service-guide` | cisco | Cisco UCS C220 M4 Server Installation and Service Guide | OL-32473-01, September 04, 2015 | September 04, 2015 | 5-6, 6 | <https://web.archive.org/web/20150925065807/http://www.cisco.com/c/en/us/td/docs/unified_computing/ucs/c/hw/C220M4/install/C220M4.pdf> |
| `dell-1950-owner-manual` | dell | Dell PowerEdge 1950 Systems Hardware Owner's Manual | P/N HH176, Rev. A02 | September 2009 | 64 | <https://dl.dell.com/Manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-1950_owner's%20manual_en-us.pdf> |
| `dell-2850-installation-guide` | dell | Dell PowerEdge 2850 Systems Installation and Troubleshooting Guide | June 2004, no revision printed | June 2004 | 50 | <https://dl.dell.com/manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-2850_user's%20guide11_en-us.pdf> |
| `dell-2900-owner-manual` | dell | Dell PowerEdge 2900 Systems Hardware Owner's Manual | December 2005, no revision printed | December 2005 | 83, 84 | <https://dl.dell.com/Manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-2900_owner's%20manual_en-us.pdf> |
| `dell-2950-owners-manual` | dell | Dell PowerEdge 2950 Systems Hardware Owner's Manual | January 2006, no revision printed | January 2006 | 89 | <https://dl.dell.com/Manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-2950_owner's%20manual_en-us.pdf> |
| `dell-840-owners-manual` | dell | Dell PowerEdge 840 Systems Hardware Owner's Manual | P/N DJ894, Rev. A00 | June 2006 | 76 | <https://dl.dell.com/Manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-840_Owner's%20Manual_en-us.pdf> |
| `dell-860-owner-manual` | dell | Dell PowerEdge 860 Systems Hardware Owner's Manual | P/N GJ055, Rev. A00 | June 2006 | 68 | <https://dl.dell.com/Manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-860_owner's%20manual_en-us.pdf> |
| `dell-b08s` | dell | Dell PowerEdge C6320 Owner's Manual | Regulatory Model B08S, Rev. A04 | 2017-08 | 105 | <https://dl.dell.com/topicspdf/poweredge-c6320_owners-manual_en-us.pdf> |
| `dell-c1100-owner-manual` | dell | Dell PowerEdge C1100 Systems Hardware Owner's Manual | Regulatory Model CS24-TY, Rev. A01 | 2013-02 | 63, 64, 66 | <https://dl.dell.com/manuals/all-products/esuprt_ser_stor_net/esuprt_cloud_products/poweredge-c1100_owner's%20manual_en-us.pdf> |
| `dell-c6100-owner-manual` | dell | Dell PowerEdge C6100 Systems Hardware Owner's Manual | Regulatory Model XS23-TY3, Rev. A01 | January 2010 | 77, 79 | <https://dl.dell.com/manuals/all-products/esuprt_ser_stor_net/esuprt_cloud_products/poweredge-c6100_owner's%20manual_en-us.pdf> |
| `dell-c6220-owner-manual` | dell | Dell PowerEdge C6220 II Systems Hardware Owner's Manual | Rev. A01 | October 2013 | 231-232 | <https://dl.dell.com/manuals/all-products/esuprt_ser_stor_net/esuprt_cloud_products/poweredge-c6220-2_Owner's%20Manual_en-us.pdf> |
| `dell-c8220-owners-manual` | dell | Dell PowerEdge C8220 Hardware Owner's Manual | Regulatory Model B05B, Rev. A05 | 2014-01 | 160, 162 | <https://dl.dell.com/manuals/all-products/esuprt_ser_stor_net/esuprt_cloud_products/poweredge-c8000_owner's%20manual3_en-us.pdf> |
| `dell-d02t` | dell | Dell Precision 7820 Tower Owner's Manual | Regulatory Model D02T, Rev. A09 | August 2023 | 92 | <https://dl.dell.com/content/manual26341430-dell-precision-7820-tower-owner-s-manual.pdf?language=en-us> |
| `dell-d04x` | dell | Dell Precision 7920 Tower Owner's Manual | Regulatory Model D04X, Rev. A14 | October 2026 | 89, 90, 100 | <https://dl.dell.com/topicspdf/precision-7920-workstation_Owners-Manual_en-us.pdf> |
| `dell-e05s` | dell | Dell PowerEdge R810 Hardware Owner's Manual | Regulatory Model E05S, Rev. A00 | February 2010 | 100, 101, 102-103 | <https://dl.dell.com/Manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-r810_owner's%20manual_en-us.pdf> |
| `dell-e05s-2` | dell | Dell PowerEdge R815 Hardware Owner's Manual | Regulatory Model E05S, Rev. A01 | December 2013 | 92, 93, 94-96 | <https://dl.dell.com/Manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-r815_owner's%20manual_en-us.pdf> |
| `dell-e10s` | dell | Dell PowerEdge R210 II Systems Owner's Manual | Regulatory Model E10S Series, Rev. A01 | 2013-09 | 69 | <https://dl.dell.com/manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-r210-2_owner's%20manual_en-us.pdf> |
| `dell-e10s-2` | dell | Dell PowerEdge R220 Owner's Manual | Regulatory Model E10S, Rev. A02 | 2015-05 | 48, 49 | <https://dl.dell.com/topicspdf/poweredge-r220_owners-manual_en-us.pdf> |
| `dell-e14s` | dell | Dell PowerEdge R720 and R720xd Owner's Manual | Regulatory Model E14S Series, Rev. A07 | 2018-10 | 42, 44-46 | <https://dl.dell.com/topicspdf/poweredge-r720_owners-manual_en-us.pdf> |
| `dell-e18s` | dell | Dell PowerEdge R420 Systems Owner's Manual | Regulatory Model E18S Series, Rev. A04 | 2013-11 | 45, 46-47 | <https://dl.dell.com/topicspdf/poweredge-r420_owners-manual_en-us.pdf> |
| `dell-e20s` | dell | Dell PowerEdge T320 Systems Owner's Manual | Regulatory Model E20S Series, Rev. A05 | 2017-08 | 69, 70 | <https://dl.dell.com/topicspdf/poweredge-t320_owners-manual_en-us.pdf> |
| `dell-e21s` | dell | Dell PowerEdge R820 Owner's Manual | Regulatory Model E21S Series, Rev. A05 | 2014-03 | 43, 46, 48-51 | <https://dl.dell.com/topicspdf/poweredge-r820_owners-manual_en-us.pdf> |
| `dell-e26s` | dell | Dell PowerEdge R630 Owner's Manual | Regulatory Model E26S Series, Rev. A08 | June 2022 | 160, 162-164 | <https://dl.dell.com/topicspdf/poweredge-r630_owners-manual_en-us.pdf> |
| `dell-e28s` | dell | Dell PowerEdge R430 Owner's Manual | Regulatory Model E28S Series, Rev. A07 | November 2020 | 82, 83-87 | <https://dl.dell.com/topicspdf/poweredge-r430_owners-manual_en-us.pdf> |
| `dell-e31s` | dell | Dell Precision Rack 7910 Owner's Manual | Regulatory Model E31S, Rev. A06 | September 2023 | 20, 21-22 | <https://dl.dell.com/topicspdf/precision-r7910-workstation_Owners-Manual_en-us.pdf> |
| `dell-e33s` | dell | Dell PowerEdge R230 Owner's Manual | Regulatory Model E33S Series, Rev. A05 | 2019-10 | 57, 58,60,71,86 | <https://dl.dell.com/topicspdf/poweredge-r230_owners-manual_en-us.pdf> |
| `dell-e38s` | dell | Dell EMC PowerEdge R740 Installation and Service Manual | Regulatory Model E38S Series, Rev. A20 | Sept. 2021 | 63, 63,69, 69-70 | <https://dl.dell.com/topicspdf/poweredge-r740_owners-manual_en-us.pdf> |
| `dell-e38s-2` | dell | Dell EMC PowerEdge R7425 Installation and Service Manual | Regulatory Model E38S Series, Rev. A20 | March 2021 | 102,104, 104, 104-105 | <https://dl.dell.com/topicspdf/poweredge-r7425_owners-manual_en-us.pdf> |
| `dell-e38s-3` | dell | Dell Precision 7920 Rack Owner's Manual | Regulatory Model E38S, Rev. A02 | November 2020 | 103 | <https://web.archive.org/web/20210715152400/https://dl.dell.com/topicspdf/precision-7920r-workstation_owners-manual_en-us.pdf> |
| `dell-e45s` | dell | Dell EMC PowerEdge R440 Installation and Service Manual | Regulatory Model E45S Series, Rev. A17 | December 2021 | 48, 50-51 | <https://dl.dell.com/topicspdf/poweredge-r440_owners-manual9_en-us.pdf> |
| `dell-e45s-2` | dell | Dell PowerEdge R6515 Installation and Service Manual | Regulatory Model E45S Series, Rev. A14 | July 2023 | 49 | <https://dl.dell.com/topicspdf/poweredge-r6515_owners-manual_en-us.pdf> |
| `dell-e53s` | dell | Dell EMC PowerEdge C4140 Installation and Service Manual | Regulatory Model E53S Series, Rev. A14 | August 2022 | 42, 43 | <https://dl.dell.com/topicspdf/poweredge-c4140_owners-manual_en-us.pdf> |
| `dell-e57s` | dell | Dell EMC PowerEdge R240 Installation and Service Manual | Regulatory Model E57S Series, Rev. A09 | August 2025 | 42 | <https://dl.dell.com/topicspdf/poweredge-r240_owners-manual_en-us.pdf> |
| `dell-e63s` | dell | Dell PowerEdge C6525 Installation and Service Manual | Regulatory Model E63S Series, Rev. A15 | May 2024 | 3,24-25, 25 | <https://dl.dell.com/topicspdf/poweredge-c6525_owners-manual_en-us.pdf> |
| `dell-e64s` | dell | Dell EMC PowerEdge C6520 Installation and Service Manual | Regulatory Model E64S Series, Rev. A03 | August 2022 | 5,58 | <https://dl.dell.com/topicspdf/poweredge-c6520_owners-manual_en-us.pdf> |
| `dell-e68s` | dell | Dell PowerEdge R7525 Installation and Service Manual | Regulatory Model E68S, Rev. A20 | July 2024 | 74 | <https://dl.dell.com/topicspdf/poweredge-r7525_owners-manual_en-us.pdf> |
| `dell-e70s` | dell | Dell PowerEdge R750 Installation and Service Manual | Regulatory Model E70S, Rev. A11 | December 2024 | 100, 101 | <https://dl.dell.com/topicspdf/poweredge-r750_owners-manual_en-us.pdf> |
| `dell-e74s` | dell | Dell PowerEdge R450 Installation and Service Manual | Part Number E74S, Rev. A03 | July 2026 | 4,52, 52 | <https://dl.dell.com/topicspdf/poweredge-r450_owners-manual_en-us.pdf> |
| `dell-e80s` | dell | Dell EMC PowerEdge T150 Installation and Service Manual | Regulatory Model E80S series, Rev. A03 | June 2025 | 41 | <https://dl.dell.com/topicspdf/poweredge-t150_owners-manual_en-us.pdf> |
| `dell-e82s` | dell | Precision 7960 Rack Setup and Specifications | Regulatory Model E82S, Rev. A07 | August 2026 | 14, 14-15 | <https://dl.dell.com/content/manual42601925-precision-7960-rack-setup-and-specifications.pdf?language=en-us> |
| `dell-e87s` | dell | Dell PowerEdge R660xs Installation and Service Manual | Part Number E87S, Rev. A07 | September 2026 | 105 | <https://dl.dell.com/content/manual42812694-dell-poweredge-r660xs-installation-and-service-manual.pdf?language=en-us> |
| `dell-e94s` | dell | Dell PowerEdge R7625 Installation and Service Manual | Regulatory Model E94S, Rev. A09 | July 2026 | 176 | <https://dl.dell.com/content/manual25024882-dell-poweredge-r7625-installation-and-service-manual.pdf?language=en-us> |
| `dell-e97s` | dell | Dell PowerEdge R6615 Installation and Service Manual | Regulatory Model E97S, Rev. A09 | July 2026 | 128 | <https://dl.dell.com/content/manual24843968-dell-poweredge-r6615-installation-and-service-manual.pdf?language=en-us> |
| `dell-r200-owners-manual` | dell | Dell PowerEdge R200 Systems Hardware Owner's Manual | P/N NM176, Rev. A00 | August 2007 | 81, 82 | <https://dl.dell.com/Manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-r200_owner's%20manual_en-us.pdf> |
| `dell-r210-owner-manual` | dell | Dell PowerEdge R210 Systems Hardware Owner's Manual | Rev. A00 | August 2009 | 27 | <https://dl.dell.com/manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-r210_owner's%20manual_en-us.pdf> |
| `dell-r310-owners-manual` | dell | Dell PowerEdge R310 Systems Hardware Owner's Manual | Regulatory Model E07S Series, Rev. A00 | March 2010 | 106, 106-108 | <https://dl.dell.com/manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-r310_owner's%20manual_en-us.pdf> |
| `dell-r320-owners-manual` | dell | Dell PowerEdge R320 Systems Owner's Manual | E18S Series, Rev. A05 | 2013-11 | 41 | <https://dl.dell.com/topicspdf/poweredge-r320_owners-manual_en-us.pdf> |
| `dell-r340-service-manual` | dell | Dell EMC PowerEdge R340 Installation and Service Manual | E58S Series, Rev. A09 | February 2022 | 37 | <https://dl.dell.com/topicspdf/poweredge-r340_owners-manual_en-us.pdf> |
| `dell-r350-service-manual` | dell | Dell EMC PowerEdge R350 Installation and Service Manual | Regulatory Model E77S, Rev. A00 | October 2021 | 48 | <https://dl.dell.com/topicspdf/poweredge-350_Owners-Manual_en-us.pdf> |
| `dell-r410-owner-manual` | dell | Dell PowerEdge R410 Systems Hardware Owner's Manual | Rev. A00 | April 2009 | 119, 119,121-122, 120 | <https://dl.dell.com/Manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-r410_owner's%20manual_en-us.pdf> |
| `dell-r510-owners-manual` | dell | Dell PowerEdge R510 Systems Hardware Owner's Manual | Regulatory Model E12S Series and E13S Series, Rev. A00 | November 2009 | 115, 117-118 | <https://dl.dell.com/Manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-r510_owner's%20manual_en-us.pdf> |
| `dell-r515-owner-manual` | dell | Dell PowerEdge R515 Systems Hardware Owner's Manual | Rev. A00 | August 2010 | 104, 105, 106, 107 | <https://dl.dell.com/Manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-r515_owner's%20manual_en-us.pdf> |
| `dell-r520-owners-manual` | dell | Dell PowerEdge R520 Owner's Manual | E19S Series, Rev. A04 | 2013-11 | 40 | <https://dl.dell.com/topicspdf/poweredge-r520_owners-manual_en-us.pdf> |
| `dell-r530-owners-manual` | dell | Dell PowerEdge R530 Owner's Manual | E29S Series, Rev. A03 | August 2020 | 74 | <https://dl.dell.com/topicspdf/poweredge-r530_owners-manual_en-us.pdf> |
| `dell-r540-service-manual` | dell | Dell PowerEdge R540 Installation and Service Manual | E46S Series, Rev. A13 | September 2026 | 66-67 | <https://dl.dell.com/topicspdf/poweredge-r540_owners-manual_en-us.pdf> |
| `dell-r610-owner-manual` | dell | Dell PowerEdge R610 Systems Hardware Owner's Manual | Rev. A04 | 2013-03 | 116, 116,118-119, 117 | <https://dl.dell.com/manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-r610_owner's%20manual2_en-us.pdf> |
| `dell-r610-technical-guide` | dell | Dell PowerEdge R610 Technical Guide | Version 4.0 | December 2012 | 29 | <https://i.dell.com/sites/doccontent/business/solutions/engineering-docs/en/Documents/server-poweredge-r610-tech-guidebook.pdf> |
| `dell-r620-owners-manual` | dell | Dell PowerEdge R620 Owner's Manual | E16S Series, Rev. A06 | 2018-11 | 43 | <https://dl.dell.com/topicspdf/poweredge-r620_owners-manual_en-us.pdf> |
| `dell-r640-service-manual` | dell | Dell EMC PowerEdge R640 Installation and Service Manual | E39S Series, Rev. A19 | August 2026 | 61 | <https://dl.dell.com/topicspdf/poweredge-r640_owners-manual_en-us.pdf> |
| `dell-r650-service-manual` | dell | Dell PowerEdge R650 Installation and Service Manual | Regulatory Model E69S, Rev. A09 | July 2026 | 71 | <https://dl.dell.com/topicspdf/poweredge-r650_owners-manual_en-us.pdf> |
| `dell-r710-owner-manual` | dell | Dell PowerEdge R710 Systems Hardware Owner's Manual | Rev. A03 | September 2009 | 129, 130, 130,132-133 | <https://dl.dell.com/Manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-r710_owner's%20manual_en-us.pdf> |
| `dell-r710-technical-guide` | dell | Dell PowerEdge R710 Technical Guide | Version 4.0 | December 2012 | 32 | <https://i.dell.com/sites/doccontent/business/solutions/engineering-docs/en/Documents/server-poweredge-r710-tech-guidebook.pdf> |
| `dell-r715-owners-manual` | dell | Dell PowerEdge R715 Hardware Owner's Manual | Regulatory Model E05S, Rev. A00 | May 2010 | 92, 93, 94 | <https://dl.dell.com/Manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-r715_owner's%20manual_en-us.pdf> |
| `dell-r740xd2-service-manual` | dell | Dell EMC PowerEdge R740xd2 Installation and Service Manual | E56S Series, Rev. A22 | June 2023 | 71-72 | <https://dl.dell.com/topicspdf/poweredge-r740xd2_owners-manual_en-us.pdf> |
| `dell-r7515-service-manual` | dell | Dell PowerEdge R7515 Installation and Service Manual | Regulatory Model E46S, Rev. A13 | August 2024 | 57 | <https://dl.dell.com/topicspdf/poweredge-r7515_owners-manual_en-us.pdf> |
| `dell-r810-technical-guide` | dell | Dell PowerEdge R810 Technical Guide | Version 4.0 | December 2012 | 31, 32 | <https://i.dell.com/sites/doccontent/shared-content/data-sheets/en/Documents/poweredge-r810-technical-guide-en.pdf> |
| `dell-r900-owner-manual` | dell | Dell PowerEdge R900 Systems Hardware Owner's Manual | P/N XK946, Rev. A00 | September 2007 | 92, 94-95 | <https://dl.dell.com/Manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-r900_owner's%20manual_en-us.pdf> |
| `dell-sc1425-troubleshooting-installation-guide` | dell | Dell PowerEdge SC1425 Systems Installation and Troubleshooting Guide | initial release | 15 Sept 2004 | 49 | <https://dl.dell.com/manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-1425sc_user's%20guide2_en-us.pdf> |
| `dell-sc1430-owner-manual` | dell | Dell PowerEdge SC1430 Systems Hardware Owner's Manual | P/N WH819, Rev. A00 | June 2006 | 19,77 | <https://dl.dell.com/manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-1430sc_owner's%20manual_en-us.pdf> |
| `dell-t110-ii-owners-manual` | dell | Dell PowerEdge T110 II Systems Owner's Manual | Regulatory Model E11S Series, Rev. A01 | October 2014 | 86 | <https://dl.dell.com/manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-t110-2_owner's%20manual_en-us.pdf> |
| `dell-t110-owners-manual` | dell | Dell PowerEdge T110 Systems Hardware Owner's Manual | Rev. A00 | August 2009 | 80, 80-81 | <https://dl.dell.com/manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-t110_owner's%20manual_en-us.pdf> |
| `dell-t140-service-manual` | dell | Dell EMC PowerEdge T140 Installation and Service Manual | E59S Series, Rev. A08 | February 2022 | 53 | <https://dl.dell.com/topicspdf/poweredge-t140_owners-manual_en-us.pdf> |
| `dell-t300-owner-manual` | dell | Dell PowerEdge T300 Systems Hardware Owner's Manual | P/N HR675, Rev. A01 | June 2009 | 111-112, 112 | <https://dl.dell.com/Manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-t300_owner's%20manual_en-us.pdf> |
| `dell-t310-owner-manual` | dell | Dell PowerEdge T310 Systems Hardware Owner's Manual | Rev. A00 | July 2009 | 108, 109, 110 | <https://dl.dell.com/manuals/all-products/esuprt_ser_stor_net/esuprt_poweredge/poweredge-t310_owner's%20manual_en-us.pdf> |
| `dell-t340-service-manual` | dell | Dell EMC PowerEdge T340 Installation and Service Manual | E60S, Rev. A08 | February 2022 | 54 | <https://dl.dell.com/topicspdf/poweredge-t340_owners-manual_en-us.pdf> |
| `dell-t420-owners-manual` | dell | Dell PowerEdge T420 Systems Owner's Manual | E20S Series, Rev. A05 | 2013-11 | 70 | <https://dl.dell.com/topicspdf/poweredge-t420_owners-manual_en-us.pdf> |
| `dell-t430-owners-manual` | dell | Dell EMC PowerEdge T430 Owner's Manual | E30S Series, Rev. A07 | February 2021 | 109, 110 | <https://dl.dell.com/topicspdf/poweredge-t430_owners-manual_en-us.pdf> |
| `dell-t440-service-manual` | dell | Dell EMC PowerEdge T440 Installation and Service Manual | E30S Series, Rev. A04 | 2019-12 | 98 | <https://dl.dell.com/topicspdf/poweredge-t440_Owners-Manual6_en-us.pdf> |
| `dell-t550-service-manual` | dell | Dell PowerEdge T550 Installation and Service Manual | Regulatory Model E76S, Rev. A08 | July 2025 | 108 | <https://dl.dell.com/topicspdf/t550_ism_en-us.pdf> |
| `dell-t620-owners-manual` | dell | Dell PowerEdge T620 Systems Owner's Manual | E17S Series, Rev. A07 | 2014-07 | 50 | <https://dl.dell.com/topicspdf/poweredge-t620_owners-manual_en-us.pdf> |
| `dell-t640-service-manual` | dell | Dell Technologies PowerEdge T640 Installation and Service Manual | E47S Series, Rev. A16 | March 2023 | 76 | <https://dl.dell.com/topicspdf/poweredge-t640_owners-manual_en-us.pdf> |
| `dell-t710-guidebook-technical-guide` | dell | Dell PowerEdge T710 Technical Guidebook | no revision printed |  | 26 | <https://i.dell.com/sites/doccontent/business/solutions/engineering-docs/en/Documents/server-poweredge-t710-technical-guide-book.pdf> |
| `dell-xc640-service-manual` | dell | Dell EMC XC640 Series XC Series Appliance and XC Core System Installation and Service Manual | E39S Series, Rev. A04 | 2018-10 | 75 | <https://dl.dell.com/topicspdf/dell-xc640-ent-xc_Owners-Manual_en-us.pdf> |
| `dell-xc740xd-service-manual` | dell | Dell EMC XC740xd XC Series Appliance and XC Core System Installation and Service Manual | E38S Series, Rev. A04 | 2018-10 | 84 | <https://dl.dell.com/topicspdf/dell-xc740xd-ent-xc_Owners-Manual_en-us.pdf.pdf> |
| `dell-xr4510c-installation-service-manual` | dell | Dell PowerEdge XR4510c Installation and Service Manual | Rev. A02 | October 2024 | 60 | <https://dl.dell.com/content/manual20158191-dell-poweredge-xr4510c-installation-and-service-manual.pdf?language=en-us> |
| `fujitsu-d2119-rx300-s3-technical-manual` | fujitsu | System Board D2119 for RX300 S3, Technical Manual | Edition | May 2006 | 17 | <https://web.archive.org/web/20170517071252/http://manuals.ts.fujitsu.com/file/3375/d2119-thb-en.pdf> |
| `fujitsu-d2619-tx300-s6-technical-manual` | fujitsu | System Board D2619-N for PRIMERGY RX/TX300 S6, Technical Manual | Edition | March 2011 | 24 | <http://manuals.ts.fujitsu.com/file/9222/d2619-n-thb-en.pdf> |
| `fujitsu-d2709-tx200-s5` | fujitsu | System Board D2709 für TX200 S5, Technisches Handbuch (German edition) | Ausgabe Mai | 2009 | 17, 18 | <https://web.archive.org/web/20110409144153/http://manuals.ts.fujitsu.com/file/8748/d2709-thb-de.pdf> |
| `fujitsu-d2759-tx150-s7-technical-manual` | fujitsu | System Board D2759 for PRIMERGY TX150 S7, Technical Manual | Edition | December 2009 | 22 | <https://web.archive.org/web/20160508195258/http://manuals.ts.fujitsu.com/file/9110/d2759-thb-en.pdf> |
| `fujitsu-d2863-rx100-s6-technical-manual` | fujitsu | System Board D2863 for PRIMERGY RX100 S6, Technical Manual | Edition | February 2011 | 21 | <https://web.archive.org/web/20180404025537/http://manuals.ts.fujitsu.com/file/9112/rx100s6-d2863-tm-en.pdf> |
| `fujitsu-d3049-tx140-s1-tx120-s3` | fujitsu | System board D3049 for PRIMERGY TX140 S1 / TX120 S3, Technical Manual (Japanese edition) |  | May 2012 | 24 | <https://web.archive.org/web/20160804213347/http://manuals.ts.fujitsu.com/file/10153/tx140s1-tx120s3-d3049-tm-jp.pdf> |
| `fujitsu-mx130-s2-maintenance-manual` | fujitsu | PRIMERGY MX130 S2 Server Upgrade and Maintenance Manual | Edition | November 2011 | 180 | <https://web.archive.org/web/20140213173318/http://manuals.ts.fujitsu.com/file/10334/mx130s2-umm-en.pdf> |
| `fujitsu-rx200-s6-guide` | fujitsu | PRIMERGY RX200 S6 Server Options Guide | Edition | November 2011 | 32 | <https://web.archive.org/web/20180329025545/http://manuals.ts.fujitsu.com/file/9274/rx200s6-og-en.pdf> |
| `fujitsu-rx200-s7-maintenance-manual` | fujitsu | PRIMERGY RX200 S7 Upgrade and Maintenance Manual (Japanese edition) |  | October 2015 | 239 | <https://web.archive.org/web/20220121104812/https://jp.fujitsu.com/platform/server/primergy/products/note/svsdvd/dvd/pdf/rx200s7-umm-jp.pdf> |
| `fujitsu-rx200-s8-maintenance-manual` | fujitsu | PRIMERGY RX200 S8 Server Upgrade and Maintenance Manual |  | June 2015 | 200, 201 | <https://web.archive.org/web/20190711143807/http://manuals.ts.fujitsu.com/file/11453/rx200s8-umm-en.pdf> |
| `fujitsu-rx2450-m2-guide` | fujitsu | PRIMERGY RX2450 M2 System configurator and order information guide |  | 2026/10/26 | 15 | <https://sp.ts.fujitsu.com/dmsp/Publications/public/cnfgRX2450M2.pdf> |
| `fujitsu-rx2530-m1-maintenance-manual` | fujitsu | FUJITSU Server PRIMERGY RX2530 M1 Upgrade and Maintenance Manual (Japanese edition) |  | August 2015 | 229 | <https://jp.fujitsu.com/platform/server/primergy/products/note/svsdvd/dvd/pdf/rx2530m1-umm-jp.pdf> |
| `fujitsu-rx2540-m4-guide` | fujitsu | PRIMERGY RX2540 M4 System configurator and order information guide | date not on the saved pages |  | 4, 16 | <https://sp.ts.fujitsu.com/dmsp/Publications/public/cnfgRX2540M4.pdf> |
| `fujitsu-rx300-s3-guide` | fujitsu | PRIMERGY RX300 S3 Server Options Guide | Edition | June 2007 | 26 | <http://manuals.ts.fujitsu.com/file/4003/rx300s3-og-en.pdf> |
| `fujitsu-rx300-s5-guide` | fujitsu | PRIMERGY RX300 S5 Options Guide | Edition | September 2009 | 30 | <http://manuals.ts.fujitsu.com/file/8488/rx300s5-og-en.pdf> |
| `fujitsu-rx300-s8-maintenance-manual` | fujitsu | PRIMERGY RX300 S8 Upgrade and Maintenance Manual (Japanese edition) |  | October 2014 | 255 | <https://web.archive.org/web/20150513075329/https://jp.fujitsu.com/platform/server/primergy/products/note/svsdvd/dvd/pdf/rx300s8-umm-jp.pdf> |
| `fujitsu-tx1320-m1-maintenance-manual` | fujitsu | FUJITSU Server PRIMERGY TX1320 M1 Upgrade and Maintenance Manual | Edition | November 2014 | 217 | <https://web.archive.org/web/20150321081306/http://manuals.ts.fujitsu.com/file/11779/tx1320m1-umm-en.pdf> |
| `fujitsu-tx1330-m1-maintenance-manual` | fujitsu | FUJITSU Server PRIMERGY TX1330 M1 Upgrade and Maintenance Manual (Japanese edition) |  | August 2014 | 226 | <https://jp.fujitsu.com/platform/server/primergy/products/note/svsdvd/dvd/pdf/tx1330m1-umm-jp.pdf> |
| `fujitsu-tx140-s1-maintenance-manual` | fujitsu | PRIMERGY TX140 S1 Upgrade and Maintenance Manual (Japanese edition) |  | January 2013 | 296 | <https://jp.fujitsu.com/platform/server/primergy/products/note/svsdvd/dvd/pdf/tx140s1-umm-jp.pdf> |
| `fujitsu-tx2540-m1-maintenance-manual` | fujitsu | FUJITSU Server PRIMERGY TX2540 M1 Upgrade and Maintenance Manual (Japanese edition) |  | December 2014 | 265 | <https://web.archive.org/web/20220121105533/https://jp.fujitsu.com/platform/server/primergy/products/note/svsdvd/dvd/pdf/tx2540m1-umm-jp.pdf> |
| `gigabyte-c621-su8-user-manual` | gigabyte | C621-SU8 User's Manual | Rev. 1001 | 2018 | 12 | <https://download.gigabyte.com/FileList/Manual/server_manual_c621-su8_1001_e_v1.pdf> |
| `gigabyte-e252-p30-user-manual` | gigabyte | E252-P30 Edge Server User Manual | Rev. 1.0 | 2021 | 28 | <https://download.gigabyte.com/FileList/Manual/server_manual_e_E252-P30_v10.pdf> |
| `gigabyte-g431-mm0-user-manual` | gigabyte | G431-MM0 GPU Server User Manual | Rev. 1.0 | 2020 | 26 | <https://download.gigabyte.com/FileList/Manual/server_manual_e_G431-MM0_v1.0.pdf> |
| `gigabyte-g482-z54-hpc-manual` | gigabyte | G482-Z54 HPC Server User Manual | Rev. 1.0 | 2020 | 35 | <https://download.gigabyte.com/FileList/Manual/server_manual_e_G482-Z54_A00.pdf> |
| `gigabyte-mc13-le0-mc13-le1-user` | gigabyte | MC13-LE0 / MC13-LE1 Server Board User Manual | Rev. 1.0 | 2022 | 14 | <https://download.gigabyte.com/FileList/Manual/sever_manual_MC13-LE0_1_e_v1.0.pdf> |
| `gigabyte-md72-hb0-md72-hb1-user` | gigabyte | MD72-HB0 / MD72-HB1 Server Board User Manual | Rev. 1.0 | 2021 | 14-15 | <https://download.gigabyte.com/FileList/Manual/server_mb_manual__md72hb0_1_e_1.0.pdf> |
| `gigabyte-memory-population-guidelines-manual` | gigabyte | Memory Population Guidelines, AMD EPYC 7003 Series Processors, User Manual | Rev. 1.0 | March 2021 | 7-13, 14, 14-15 | <https://download.gigabyte.com/FileList/Manual/server_manual_Memory_Population_Guidelines_e_A00.pdf> |
| `gigabyte-mu70-su0-user-manual` | gigabyte | MU70-SU0 User's Manual | Rev. 1002 | 2016 | 17 | <https://download.gigabyte.com/FileList/Manual/server_manual_mu70-su0_en_1002.pdf> |
| `gigabyte-mu71-su0-user-manual` | gigabyte | MU71-SU0 Server Board User Manual | Rev. 1.0 | 2019 | 13 | <https://download.gigabyte.com/FileList/Manual/server_manual_MU71-SU0_e_v10.pdf> |
| `gigabyte-mu72-su0-user-manual` | gigabyte | MU72-SU0 Server Board User Manual | Rev. 1.0 | 2021 | 14 | <https://download.gigabyte.com/FileList/Manual/server_mb_manual_MU72-SU0_e_v1.0.pdf> |
| `gigabyte-mw50-sv0-user-manual` | gigabyte | MW50-SV0 User's Manual | Rev. 1002 | 2019 | 18 | <https://download.gigabyte.com/FileList/Manual/server_manual_mw50-sv0_e_v1002.pdf> |
| `gigabyte-mx33-bs0-user-manual` | gigabyte | MX33-BS0 Server Board User Manual | Rev. 1.0 | 2021 | 13 | <https://download.gigabyte.com/FileList/Manual/sever_manual_MX33-BS0_e_10.pdf> |
| `gigabyte-mz31-ar0-user-manual` | gigabyte | MZ31-AR0 User Manual | Rev. 2.0 (2019) and Rev. 3.0, 2021 | 2019 | 15 | <https://download.gigabyte.com/FileList/Manual/server_manual_mz31-ar0_e_v20.pdf> |
| `gigabyte-mz32-ar0-user-manual` | gigabyte | MZ32-AR0 User Manual | Rev. 1.0 and Rev. 3.0 | 2021 | 14 | <https://download.gigabyte.com/FileList/Manual/server_manual_MZ32-AR0_e_v10.pdf> |
| `gigabyte-r162-za0-user-manual` | gigabyte | R162-ZA0 User Manual | Rev. 1.0 | 2021 | 14,31 | <https://download.gigabyte.com/FileList/Manual/server_manual_R162-ZA0_e_A00.pdf> |
| `gigabyte-r182-n20-r182-na0-3rd` | gigabyte | R182-N20 / R182-NA0 3rd Gen. Intel Xeon Scalable DP Server System, User Manual | Rev. 1.0 | 2021 | 32-33, 33 | <https://download.gigabyte.com/FileList/Manual/server_manual_R182-N20_NA0_e_10.pdf> |
| `gigabyte-r182-z90-r182-z91-r182` | gigabyte | R182-Z90 / R182-Z91 / R182-Z92 AMD EPYC 7003 DP Server System User Manual | Rev. 1.0 | 2021 | 34 | <https://download.gigabyte.com/FileList/Manual/server_manual_R182-Z90_Z91_Z92_e_A00.pdf> |
| `gigabyte-r280-f2o-r280-g2o-user` | gigabyte | R280-F2O / R280-G2O Server User Manual | Rev. 1.1 | 2016 | 21 | <https://download.gigabyte.com/FileList/Manual/server_manual_r280-f2o_g2o_a3c_f3c_e_11.pdf> |
| `hp-z6-g4-quickspecs` | hp | HP Z6 G4 Workstation QuickSpecs | c05527761, DA-15955, version 59 | April 1, 2024 | 21-22 | <https://web.archive.org/web/20251122031240id_/https://www8.hp.com/h20195/v2/getpdf.aspx/c05527761.pdf> |
| `hp-z6-g4-tech-wp` | hp | HP Z6 G4 Workstation Technical White Paper | 4AA7-1335ENW | January 2021 | 21 | <https://web.archive.org/web/20220216015225/https://h20195.www2.hp.com/v2/getpdf.aspx/4AA7-1335ENW.pdf> |
| `hpe-495624-003` | hp | HP ProLiant DL380 G6 Server User Guide | part number 495624-003, edition 3, February 2011 | February 2011 | 41-42, 45 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c01705681> |
| `hpe-501274-002` | hp | HP ProLiant BL460c G6 Server Blade User Guide | part number 501274-002, edition 2, March 2011 | March 2011 | 27, 31 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c01708947> |
| `hpe-517689-005` | hp | HPE ProLiant DL360 G6 Server User Guide | part number 517689-005, edition 5, October 2018 | October 2018 | 38-39, 42 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c01705733> |
| `hpe-530514-002` | hp | HP ProLiant DL320 G6 Server User Guide | part number 530514-002, second edition, March 2010 | March 2010 | 26, 30 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c01736500> |
| `hpe-579574-004` | hp | HP ProLiant DL120 G6 Server User Guide | part number 579574-004, edition 4, April 2012 | April 2012 | 23, 25 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c01934840> |
| `hpe-588708-001` | hp | HP ProLiant DL385 G7 Server User Guide | part number 588708-001, first edition, March 2010 | March 2010 | 48, 49 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c02118792> |
| `hpe-593961-001` | hp | HP ProLiant BL685c G7 Server Blade User Guide | part number 593961-001, first edition, June 2010 | June 2010 | 30-31 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c02252425> |
| `hpe-595656-005` | hp | HP ProLiant DL580 G7 Server User Guide | part number 595656-005, edition 5, July 2012 | July 2012 | 45 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c02267159> |
| `hpe-601465-003` | hp | HP ProLiant DL165 G7 Server Maintenance and Service Guide | part number 601465-003, third edition, March 2013 | March 2013 | 64 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c02074307> |
| `hpe-617692-004` | hp | HP ProLiant DL585 G7 Server User Guide | part number 617692-004, edition 4, October 2012 | October 2012 | 43, 44 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c02244320> |
| `hpe-638025-003a` | hp | HP ProLiant DL120 G7 Server User Guide | part number 638025-003a, edition 3, April 2014 | April 2014 | 30 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c02790682> |
| `hpe-656396-004` | hp | HP ProLiant BL460c Gen8 Server Blade User Guide | 656396-004, edition 4, December 2012 | December 2012 | 27,32, 32, 32-33 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c03428245> |
| `hpe-664724-006` | hp | HP ProLiant DL360p Gen8 Server User Guide | 664724-006, edition 6, February 2014 | February 2014 | 42,48, 48, 49 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c03231393> |
| `hpe-670519-006` | hp | HP ProLiant DL160 Gen8 Server User Guide | 670519-006, edition 6, March 2013 | March 2013 | 46,50, 49-50, 50 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c03242768> |
| `hpe-675786-006` | hp | HPE ProLiant DL360e Gen8 Server User Guide | 675786-006, edition 8, October 2019 | October 2019 | 45,49, 49, 50 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c03401317> |
| `hpe-676151-006` | hp | HPE ProLiant DL380e Gen8 Server User Guide | 676151-006, edition 7, October 2019 | October 2019 | 59,63, 63, 64 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c03413110> |
| `hpe-767927-009` | hp | HPE ProLiant DL360 Gen9 Server User Guide | 767927-009, edition 9, February 2018 | February 2018 | 41,47, 47, 48 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c04441974> |
| `hpe-768826-007` | hp | HPE ProLiant BL460c Gen9 Server Blade User Guide | 768826-007, edition 7, September 2018 | September 2018 | 43,47, 45,47-48, 48 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c04410344> |
| `hpe-768830-401a` | hp | HPE ProLiant DL380 Gen9 Server User Guide | 768830-401a, edition 10, April 2018 | April 2018 | 69,77, 78, 79 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c04436966> |
| `hpe-775439-004` | hp | HPE ProLiant DL180 Gen9 Server User Guide | 775439-004, edition 5, March 2016 | March 2016 | 82,86, 86, 87 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c04422844> |
| `hpe-775786-004` | hp | HPE ProLiant DL160 Gen9 Server User Guide | 775786-004, edition 4, March 2016 | March 2016 | 52,57, 57 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c04430543> |
| `hpe-781878-003` | hp | HPE ProLiant DL120 Gen9 Server User Guide | 781878-003, edition 3, April 2016 | April 2016 | 57,61, 59, 61 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c04504822> |
| `hpe-786245-003` | hp | HPE ProLiant DL60 Gen9 Server User Guide | 786245-003, edition 3, April 2016 | April 2016 | 49,53, 51,53 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c04504816> |
| `hpe-826318-003a` | hp | HPE ProLiant DL20 Gen9 Server User Guide | 826318-003a, edition 4, June 2017 | June 2017 | 67,69, 69 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c04898522> |
| `hpe-833836-073` | hp | HPE ProLiant ML10 Gen9 Server User Guide, Spanish edition (Guía de usuario del servidor HPE ProLiant ML10 Gen9) | 833836-073, February 2017 | February 2017 | 37 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c04960478> |
| `hpe-872677-004` | hp | HPE ProLiant MicroServer Gen10 User Guide | 872677-004, edition 4, November 2018 | November 2018 | 48 | <https://www.laptopsdirect.co.uk/PDFs/P07203-421_UserManual1.PDF> |
| `hpe-876833-006` | hp | HPE ProLiant BL460c Gen10 Server Blade User Guide | 876833-006, edition 6, May 2019 | May 2019 | 60 | <https://support.hpe.com/hpesc/public/docDisplay?docId=a00018554en_us> |
| `hpe-bl460c-g7-blade-user-guide` | hp | HP ProLiant BL460c G7 Server Blade User Guide | part number 613018-002, edition 2 | March 2011 | 32 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c02498009> |
| `hpe-ddr3-gen8` | hp | Configuring and using DDR3 memory with HP ProLiant Gen8 Servers (engineering white paper) | 2nd edition | 2012 | 11, 12 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c03293145> |
| `hpe-dl160-6-g6` | hp | QuickSpecs HP ProLiant DL160 Generation 6 (G6) | DA-13247, version 59 | September 24, 2012 | 36, 36-37 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c04284374> |
| `hpe-dl160-g6-maintenance-service-guide` | hp | HP ProLiant DL160 G6 Server Maintenance and Service Guide | part number 516315-006, sixth edition | February 2011 | 75, 78 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c01728029> |
| `hpe-dl160-gen10` | hp | HPE ProLiant DL160 Gen10 Server QuickSpecs | a00021860enw, version 32, 18 August 2025 (marked retired in the notes) | 18 August 2025 | 31 | <https://www.hpe.com/psnow/doc/a00021860enw> |
| `hpe-dl180-6-g6` | hp | QuickSpecs HP ProLiant DL180 Generation 6 (G6) | DA-13248, version 53 | September 24, 2012 | 38, 38-39 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c04284378> |
| `hpe-dl180-g6-maintenance-service-guide` | hp | HP ProLiant DL180 G6 Server Maintenance and Service Guide | part number 516365-007, seventh edition | February 2011 | 83 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c01727924> |
| `hpe-dl360-g7-user-guide` | hp | HP ProLiant DL360 G7 Server User Guide | part number 608341-005, edition 5 | February 2011 | 41-42 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c02065265> |
| `hpe-dl380-g7-user-guide` | hp | HP ProLiant DL380 G7 Server User Guide | part number 594660-001, first edition | March 2010 | 49 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c02159872> |
| `hpe-dl380-gen10-user-guide` | hp | HPE ProLiant DL380 Gen10 Server User Guide | 30-EFA1AAC3-401h, edition 20 | October 2024 | 141-142 | <https://support.hpe.com/hpesc/public/docDisplay?docId=a00019109en_us> |
| `hpe-dl380-gen11` | hp | HPE ProLiant DL380 Gen11 QuickSpecs | a50004307enw, version 48 | 5 October 2026 | 41,77 | <https://www.hpe.com/psnow/doc/a50004307enw> |
| `hpe-dl380p-gen8-user-guide` | hp | HPE ProLiant DL380p Gen8 Server User Guide | 661848-006R, edition 7, December 2015 | December 2015 | 46,50, 50, 51 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c03231416> |
| `hpe-dl385p-gen8-user-guide` | hp | HPE ProLiant DL385p Gen8 Server User Guide | 661850-002R, edition 3, December 2015 | December 2015 | 51,53,56, 53, 54, 56 | <https://support.hpe.com/hpesc/public/docDisplay?docId=c03351259> |
| `hpe-gen10` | hp | HPE ProLiant MicroServer Gen10 QuickSpecs | a00008701enw, version 10, 09-November-2020 (retired) | 2020 | 10 | <https://support.hpe.com/hpesc/public/api/document/a00008701enw> |
| `hpe-ml10-generation9-gen9` | hp | HPE ProLiant ML10 Generation9 (Gen9) QuickSpecs | c04922941, version 8, 11-July-2017 (retired) | 2017 | 17 | <https://support.hpe.com/hpesc/public/api/document/c04922941> |
| `hpe-ml350-gen10-user-guide` | hp | HPE ProLiant ML350 Gen10 Server User Guide | 30-3266984C-008a, edition 8a | January 2024 | 130-131 | <https://support.hpe.com/hpesc/public/docDisplay?docId=a00016366en_us> |
| `hpe-population-gen10-2300-technical` | hp | Server memory population rules for HPE ProLiant Gen10 Plus servers with Intel Xeon E-2300 processors (technical white paper) | a50005926ENW Rev. 2, PDF dated 6 August 2025 | 6 August 2025 | 3, 3,5 | <https://www.hpe.com/psnow/doc/a50005926enw> |
| `hpe-population-gen10-3rd-technical` | hp | Server Memory and Persistent Memory population rules for HPE Gen10 Plus servers with 3rd Gen Intel Xeon Scalable processors (technical white paper) | a50003886ENW Rev. 6, PDF dated 3 September 2025 | 3 September 2025 | 3, 3-4, 12 | <https://www.hpe.com/psnow/doc/a50003886enw> |
| `hpe-population-gen10-gen10-7002-7003` | hp | Server memory population rules for HPE ProLiant Gen10 and Gen10 Plus servers with AMD EPYC 7002 and 7003-series processors (technical white paper) | a00038346ENW Rev. 7, PDF dated 4 August 2025 | 4 August 2025 | 3, 4, 4,6,8 | <https://www.hpe.com/psnow/doc/a00038346enw> |
| `hpe-population-gen10-technical` | hp | Server Memory and Persistent Memory population rules for HPE Gen10 servers with Intel Xeon Scalable processors (technical white paper) | a00017079ENW, Rev. 16, copyright 2025 | 2025 | 4, 4,7, 5, 7,19, 19, 21 | <https://www.hpe.com/psnow/doc/a00017079enw> |
| `hpe-population-gen11-5th-technical` | hp | Server memory population rules for HPE Gen11 servers with 5th Gen Intel Xeon Scalable processors (technical white paper) | a50010242ENW, no revision printed, PDF dated 10 March 2026 | 10 March 2026 | 4,77, 15 | <https://www.hpe.com/psnow/doc/a50010242enw> |
| `hpe-xd665` | hp | HPE Cray XD665 QuickSpecs | a50007004enw, version 11 | 2 December 2024 | 7 | <https://www.hpe.com/psnow/doc/a50007004enw> |
| `hpe-z8-g4-workstation-quickspecs` | hp | HP Z8 G4 Workstation QuickSpecs | c05527763, DA-15956, version 70 | September 26, 2024 | 14,24, 21-23 | <https://web.archive.org/web/20251203072515id_/https://www8.hp.com/h20195/v2/GetPDF.aspx/c05527763.pdf> |
| `hpe-z8-g4-workstation-white-paper` | hp | HP Z8 G4 Workstation Technical White Paper | 4AA7-1334ENW | May 2019 | 19-20,24, 20 | <https://h20195.www2.hp.com/v2/getpdf.aspx/4AA7-1334ENW.pdf> |
| `huawei-1288h-v5-user-guide` | huawei | FusionServer 1288H V5 Server User Guide | Issue 17, 2024-05-11, xFusion | 2024-05-11 | 29, 31, 32-33 | not recorded |
| `huawei-200-maintenance-service-guide-2280` | huawei | TaiShan 200 Server Maintenance and Service Guide (Model 2280) | Issue 23, 2025-07-27, Huawei, EDOC1100093458 | 2025-07-27 | 52 | <https://support.huawei.com/enterprise/en/doc/EDOC1100093458> |
| `huawei-2288h-v5-user-guide` | huawei | FusionServer 2288H V5 Server User Guide | Issue 21, 2024-05-11, XFUSION INTERNATIONAL PTE. LTD. | 2024-05-11 | 33, 36, 37 | not recorded |
| `huawei-500-maintenance-service-guide-3000` | huawei | Atlas 500 Pro AI Edge Server Maintenance and Service Guide (Model 3000) | Issue 18, 2023-12-06, EDOC1100141191 | 2023-12-06 | 29, 49 | <https://support.huawei.com/enterprise/en/doc/EDOC1100141191> |
| `huawei-rh1288-v3-user-guide` | huawei | FusionServer RH1288 V3 Server User Guide | Issue 04, 2023-02-28, xFusion | 2023-02-28 | 57, 194,202 | not recorded |
| `huawei-rh2288-v3-user-guide` | huawei | FusionServer RH2288 V3 Server User Guide | Issue 04 | 2023-02-28 | 67, 216 | not recorded |
| `huawei-rh2288h-v3-user-guide` | huawei | FusionServer RH2288H V3 Server User Guide | Issue 04, 2023-02-28, xFusion | 2023-02-28 | 80, 225,234 | not recorded |
| `huawei-rh2288h-v3-user-guide-2` | huawei | FusionServer RH2288H V3 Server User Guide | Issue 04, 2023-02-28, xFusion Digital Technologies | 2023-02-28 | 234 | not recorded |
| `huawei-taishan200-2280-dimm-rules` | huawei | DIMM Installation Rules (TaiShan 200 Server (Model 2280)) | Memory Configuration Assistant, sheet 1 |  | the whole page | <https://info.support.huawei.com/computing/infogateway/gallery/v1/images?name=en/DIMM%20Installation%20Rules%20(TaiShan%20200%20Server%20(Model%202280)).xlsx> |
| `ibm-dx360-m4-7912-7913-user` | ibm | System x iDataPlex dx360 M4 Types 7912 and 7913: User's Guide | Part Number 00V9812, Second Edition ( | July 2012 | 56, 57 | <https://pubs.lenovo.com/idataplex/ch1inmst.pdf> |
| `ibm-hs23-installation-user-guide-7875` | ibm | BladeCenter HS23 Blade Server Installation and User's Guide (Types 7875 and 1929) and the web page "Installing a memory module" with the same text | Part Number 00AK736, Fifth Edition ( | June 2017 | 33 | <https://pubs.lenovo.com/bladecenter-hs23/cy1ec_hs23_iug.pdf> |
| `ibm-x220-installation-service-guide-7906` | ibm | Flex System x220 Compute Node Installation and Service Guide (Machine Types 7906, 2585) and the web pages "Installing a DIMM", "Independent-channel mode", "Rank-sparing mode", "Mirrored-channel mode" with the same text | Part Number 00YJ297, Tenth Edition ( | August 2016 | the whole page | <https://pubs.lenovo.com/x220/dw1kr_book.pdf> |
| `ibm-x240-7863-8737-8738-8956` | ibm | IBM Flex System x240 Compute Node Types 7863, 8737, 8738, and 8956: Installation and Service Guide | Part Number 00FH226, Seventh Edition | March 2017 | 605 | <https://pubs.lenovo.com/x240/dw1ko_book_ibm.pdf> |
| `ibm-x3200-m2-4367-4368-installation` | ibm | System x3200 M2 Types 4367 and 4368: Installation Guide | Part Number 46M2038, Third Edition (September 2009) | 2038 | 29-30, 30 | <https://public.dhe.ibm.com/systems/support/system_x_pdf/46m2038.pdf> |
| `ibm-x3250-m3-4251-4252-4261` | ibm | IBM System x3250 M3 Types 4251, 4252, and 4261: Installation and User's Guide | Part Number 81Y6118, Sixth Edition ( | August 2011 | 48-50, 50 | <https://public.dhe.ibm.com/systems/support/system_x_pdf/81y6118.pdf> |
| `ibm-x3250-m4-installation-service-guide` | ibm | System x3250 M4 Installation and Service Guide (Machine Type 2583) and the web pages "Memory module installation", "Unbuffered DIMMs (UDIMMs)", "Installing a DIMM" with the same text | Fourteenth Edition (November 2015), no part number printed | November 2015 | the whole page, 60-61 | <https://public.dhe.ibm.com/systems/support/system_x_pdf/x3250_m4_2583_isg_en.pdf> |
| `ibm-x3300-m4-7382` | ibm | Lenovo System x3300 M4 (Type 7382) documentation page "Installing a memory module" | web page carries no document number, fetched 2026-11-17; the Installation and Service Guide PDF is 62 MB at public.dhe.ibm.com and 73 MB at pubs.lenovo.com, over the 60 MB limit, not fetched | 2026-11-17 | the whole page | <https://pubs.lenovo.com/x3300-m4/installing_memory> |
| `ibm-x3400-m3-7378-7379-installation` | ibm | IBM System x3400 M3 Types 7378 and 7379: Installation and User's Guide | Part Number 00D3189, Ninth Edition ( | February 2012 | 94 | <https://public.dhe.ibm.com/systems/support/system_x_pdf/00d3189.pdf> |
| `ibm-x3550-m2-4198-7946-installation` | ibm | IBM System x3550 M2 Types 4198 and 7946: Installation and User's Guide | Part Number 00D9279, Thirteenth Edition ( | July 2012 | 69-70 | <https://public.dhe.ibm.com/systems/support/system_x_pdf/00d9279.pdf> |
| `ibm-x3550-m3-4254-7944-installation` | ibm | Lenovo System x3550 M3 (Types 4254, 7944) documentation pages "Installing a memory module", "DIMM installation sequence", "Memory mirroring", "Online-spare memory"; the same text in "System x3550 M3 Installation and User's Guide" | web pages carry no document number, fetched 2026-11-17; the guide is Part Number 00KC032, Thirteenth Edition (September 2023) | 2026-11-17 | the whole page, 64 | <https://pubs.lenovo.com/x3550-m3/c_dimm_rules> |
| `ibm-x3550-m4-7914-installation-x3550` | ibm | Lenovo System x3550 M4 (Type 7914) documentation pages "Installing a memory module", "DIMM installation sequence", "Memory mirrored channel", "Memory rank sparing"; the same text in "System x3550 M4 Installation and Service Guide" | web pages carry no document number, fetched 2026-11-17; the guide is the Twentieth Edition (November 2015), no part number printed | 2026-11-17 | the whole page | <https://pubs.lenovo.com/x3550-m4/c_dimm_rules> |
| `ibm-x3650-7979-user-guide` | ibm | System x3650 Type 7979: User's Guide | Part Number 44R5189, Fifth Edition ( | December 2007 | 83 | <https://public.dhe.ibm.com/systems/support/system_x_pdf/44r5189.pdf> |
| `ibm-x3650-m2-4199-installation-guide` | ibm | System x3650 M2 Types 4199 and 7947: Installation and User's Guide | Part Number 00D3225, Tenth Edition | February 2012 | 105 | <https://public.dhe.ibm.com/systems/support/system_x_pdf/00d3225.pdf> |
| `ibm-x3650-m3-4255-installation-guide` | ibm | System x3650 M3 Types 4255, 7945, and 7949: Installation and User's Guide | Part Number 00KC218, Eleventh Edition | June 2014 | 132-133 | <https://pubs.lenovo.com/x3650-m4/PDF_4255_7945_7949_iug.pdf> |
| `inspur-nf5280m-lm6-user-manual` | inspur | NF5280M/LM6 User Manual (meta brain Server, IEIT SYSTEMS) | V1.6 | 2025-12-17 | 59, 62 | not recorded |
| `inspur-sa5212m4-sa5212m4-user-manual` | inspur | 浪潮英信服务器 SA5212M4 用户手册 (Inspur SA5212M4 user manual, Chinese) | V1.0 | December 2014 | 75 | not recorded |
| `intel-board-s2600ip-workstation-tps` | intel | Intel Server Board S2600IP and Intel Workstation Board W2600CR Technical Product Specification | G34153-004, Revision 1.5 | March 2015 | 45 | <https://web.archive.org/web/20220121081239/https://www.intel.com/content/dam/support/us/en/documents/motherboards/server/sb/g34153004_s2600ip_w2600cr_tps_rev151.pdf> |
| `intel-board-se7501wv2-product-product-guide` | intel | Intel Server Board SE7501WV2 Product Guide | C14587-002 |  | 30 | <https://web.archive.org/web/20120812210102/http://download.intel.com/support/motherboards/server/se7501wv2/se7501wv2_pg.pdf> |
| `intel-e14960-009` | intel | Intel Server Boards S3200SH/S3210SH Technical Product Specification | E14960-009, Revision 1.8, May 2010 | May 2010 | 39 | <https://web.archive.org/web/20220316225735/https://www.intel.com/content/dam/support/us/en/documents/motherboards/server/s3200sh/sb/e14960009_s3210sh_tps_r1_8.pdf> |
| `intel-e39529-013` | intel | Intel Server Boards S5520HC, S5500HCV, and S5520HCT Technical Product Specification | E39529-013, Revision 1.9, June 2011 | June 2011 | 49, 51 | <https://web.archive.org/web/20160705103142/https://www.intel.com/content/dam/support/us/en/documents/motherboards/server/s5520hc/sb/e39529013_s5520hc_s5500hcv_s5520hct_tps_r19.pdf> |
| `intel-e42249-009` | intel | Intel Server Board S5500BC Technical Product Specification | E42249-009, Revision 1.8, June 2011 | June 2011 | 40,42 | <https://web.archive.org/web/20160910011329/https://www.intel.com/content/dam/support/us/en/documents/motherboards/server/s5500bc/sb/e42249009_s5500bc_tps_1_9.pdf> |
| `intel-e44031-012` | intel | Intel Server Board S5520UR and S5520URT Technical Product Specification | E44031-012, Revision 1.9, May 2011 | May 2011 | 40,43, 43 | <https://web.archive.org/web/20201112042143/https://www.intel.com/content/dam/support/us/en/documents/motherboards/server/s5520ur/sb/e44031012_s5520ur_s5520urt_tps_r1_9.pdf> |
| `intel-e65697-010` | intel | Intel Server Board S3420GP Technical Product Specification | E65697-010, Revision 2.4, January 2011 | January 2011 | 34, 34-35 | <https://web.archive.org/web/20220414191407/https://www.intel.com/content/dam/support/us/en/documents/motherboards/server/s3420gp/sb/s3420gp_tps_r2_4.pdf> |
| `intel-m50cyp2sb-technical` | intel | Intel Server Board M50CYP2SB Technical Product Specification | Rev. 1.41 | August 2022 | 46, 48 | <https://web.archive.org/web/20221024041852/https://www.intel.com/content/dam/support/us/en/documents/server-products/single-node-servers/m50cyp2sb-server-board-tps.pdf> |
| `intel-m70klp-technical` | intel | Intel Server System M70KLP Family Technical Product Specification | Rev. 1.0 | April 2021 | 39 | <https://web.archive.org/web/20220326215456/https://www.intel.com/content/dam/support/us/en/documents/server-products/M70KLP_TPS.pdf> |
| `intel-s1200bt-technical` | intel | Intel Server Board S1200BT Technical Product Specification | G13326-007, Revision 2.4, May 2013 | May 2013 | 31, 32, 33 | <https://web.archive.org/web/20220317015358/https://www.intel.com/content/dam/support/us/en/documents/motherboards/server/sb/g13326007_s1200bt_tps_r2_4.pdf> |
| `intel-s1200sp-technical` | intel | Intel Server Board S1200SP Family Technical Product Specification | Revision 1.4, March 2017 (no order number printed) | March 2017 | 33, 33,36 | <https://web.archive.org/web/20240807064334/https://www.intel.com/content/dam/support/us/en/documents/server-products/S1200SP_TPS_R1_4_AllSKU.pdf> |
| `intel-s1200v3rp-technical` | intel | Intel Server Board S1200V3RP Technical Product Specification | G84364-004, Revision 2.0, June 2015 | June 2015 | 30, 30,33 | <https://web.archive.org/web/20220126191535/https://www.intel.com/content/dam/support/us/en/documents/motherboards/server/sb/g84364004_s1200v3rp_tps_r2_0.pdf> |
| `intel-s2600cp-p4000cp-technical` | intel | Intel Server Board S2600CP Family, Intel Server System P4000CP Family Technical Product Specification | G26942-005, Revision 1.9, March 2015 | March 2015 | 52 | <https://web.archive.org/web/20160327150014/https://www.intel.com/content/dam/support/us/en/documents/motherboards/server/s2600cp/sb/g26942005_s2600cp_p4000cp_tps_rev19.pdf> |
| `intel-s2600cw-technical` | intel | Intel Server Board S2600CW Family Technical Product Specification | Revision 2.61 | March 2020 | 39, 39-40, 39-40,43 | <https://web.archive.org/web/20220205233726/https://www.intel.com/content/dam/support/us/en/documents/server-products/server-boards/S2600CW_TPS.pdf> |
| `intel-s2600gz-technical` | intel | Intel Server Board S2600GZ/GL Technical Product Specification | Revision 2.4, October 2014 (no order number on the cover) | October 2014 | 36,38, 38 | <https://web.archive.org/web/20230127062822/https://www.intel.com/content/dam/support/us/en/documents/motherboards/server/sb/s2600gzgl_tps_r2_4.pdf> |
| `intel-s2600st-technical` | intel | Intel Server Board S2600ST Product Family Technical Product Specification | Rev 2.4 | March 2020 | 48-49, 49, 50 | <https://web.archive.org/web/20210921180702/https://www.intel.com/content/dam/support/us/en/documents/server-products/Intel_S2600ST_TPS.pdf> |
| `intel-s2600wf-technical` | intel | Intel Server Board S2600WF Product Family Technical Product Specification | Rev 2.6 | October 2021 | 54, 54-55, 55, 56 | <https://web.archive.org/web/20250118081233/https://www.intel.com/content/dam/support/us/en/documents/server-products/server-boards/S2600WF_TPS.pdf> |
| `intel-s2600wt-technical-tps` | intel | Intel Server Board S2600WT Technical Product Specification (TPS) | Revision 1.9 | February 2020 | 38, 40, 40,44 | <https://web.archive.org/web/20220317050938/https://www.intel.com/content/dam/support/us/en/documents/server-products/server-boards/Intel_Server_Board_S2600WT_TPS.pdf> |
| `intel-s4600lh2-t2-technical` | intel | Intel Server Boards S4600LH2/T2 Technical Product Specification | Revision 2.0, March 2014 (no order number on the cover) | March 2014 | 38,40, 39 | <https://web.archive.org/web/20240118024553/https://www.intel.com/content/dam/support/us/en/documents/motherboards/server/sb/s4600lh2_t2_tps_rev_2_02.pdf> |
| `intel-s5000psl-s5000xsl-technical` | intel | Intel Server Boards S5000PSL and S5000XSL Technical Product Specification | D41763-008, Revision 1.7, August 2010 | August 2010 | 31,33 | <https://web.archive.org/web/20161130150538/https://www.intel.com/content/dam/support/us/en/documents/motherboards/server/s5000psl/sb/417435_d41763_008_s5000psl_tps_rev_17.pdf> |
| `intel-s5000vcl-technical` | intel | Intel Server Board S5000VCL Technical Product Specification | D64569-007, Revision 2.3, cover date May 2007 | May 2007 | 21 | <https://web.archive.org/web/20230201073947/https://www.intel.com/content/dam/support/us/en/documents/motherboards/server/s5000vcl/sb/s5000vcl_tps_r2_3.pdf> |
| `intel-s5000vsa-technical` | intel | Intel Server Board S5000VSA Technical Product Specification | D36978-010, Revision 1.8, June 2010 | June 2010 | 21 | <https://web.archive.org/web/20200120041744/https://www.intel.com/content/dam/support/us/en/documents/motherboards/server/s5000vsa/sb/d36978010_s5000vsa_tps_r1_9.pdf> |
| `intel-se7501wv2-technical` | intel | Intel Server Board SE7501WV2 Technical Product Specification | C25653-001, Revision 1.0, December 2002 | December 2002 | 25 | <https://web.archive.org/web/20081203072305/http://download.intel.com/support/motherboards/server/se7501wv2/tps.pdf> |
| `intel-workstation-board-s5520sc-tps` | intel | Intel Workstation Board S5520SC Technical Product Specification | E39530-010, Revision 1.7 | May 2010 | 47 | <https://web.archive.org/web/20221206042904/https://www.intel.com/content/dam/support/us/en/documents/motherboards/server/s5520sc/sb/e39530010_s5520sc_tps_r1_7.pdf> |
| `lenovo-p720-machine-30ba-user-guide` | lenovo | P720 User Guide, Machine Types 30BA, 30BB, and 30BU | Eighth Edition | March 2022 | 20,139 | <https://download.lenovo.com/pccbbs/thinkcentre_pdf/p720_ug_en.pdf> |
| `lenovo-p920-machine-30bd-user-guide` | lenovo | P920 User Guide, Machine Types 30BD, 30BV, and 30BC | Eighth Edition | March 2022 | 20,117, 118 | <https://download.lenovo.com/pccbbs/thinkcentre_pdf/p920_ug_en.pdf> |
| `lenovo-p920-p720` | lenovo | Lenovo ThinkStation P920 and P720 Memory Configurator | version 1.0, 11/8/ | 2017 | 5, 6, 8 | <https://download.lenovo.com/pccbbs/thinkcentre_pdf/thinkstation_p920_p720_memory_configurations_v1.0.pdf> |
| `lenovo-rd330-maintenance-manual` | lenovo | ThinkServer RD330 Hardware Maintenance Manual | Sixth Edition ( | March 2015 | 79-80, 93 | <https://download.lenovo.com/pccbbs/thinkservers/rd330hmm_en.pdf> |
| `lenovo-rd450-user-guide-maintenance-manual` | lenovo | ThinkServer RD450 User Guide and Hardware Maintenance Manual | Ninth Edition ( | November 2017 | 82, 83-84, 84 | <https://download.lenovo.com/pccbbs/thinkservers/rd450_ug_en.pdf> |
| `lenovo-sn550` | lenovo | ThinkSystem SN550 online documentation, topics "Memory configuration", "Independent memory mode" | no document number; fetched | 2026-11-17 | the whole page | <https://pubs.lenovo.com/sn550/memory_configuration> |
| `lenovo-sr530-7x07-7x08-installation` | lenovo | ThinkSystem SR530 (7X07, 7X08) online documentation, topic "Memory module installation rules" | no document number; fetched | 2026-11-17 | the whole page | <https://pubs.lenovo.com/sr530/memory_module_installation_order> |
| `lenovo-sr530-xeon-sp-product-guide` | lenovo | Lenovo ThinkSystem SR530 Server (Xeon SP Gen 1 / Gen 2) Product Guide | LP1045, updated 12 Dec 2023 | 12 Dec 2023 | the whole page | <https://lenovopress.lenovo.com/lp1045> |
| `lenovo-sr590-7x98-7x99-installation-installation` | lenovo | ThinkSystem SR590 (7X98, 7X99) online documentation, topics "Memory module installation rules", "DRAM DIMM installation rules", "Independent mode" | no document number; fetched | 2026-11-17 | the whole page | <https://pubs.lenovo.com/sr590/dimm_installation_rules> |
| `lenovo-sr645-installation` | lenovo | ThinkSystem SR645 online documentation, topics "Memory module installation rules and order", "Memory" | no document number; fetched | 2026-11-17 | the whole page | <https://pubs.lenovo.com/sr645/memory_module_install_rules> |
| `lenovo-sr650-7x05-7x06-installation` | lenovo | ThinkSystem SR650 (7X05, 7X06) online documentation, topics "Memory configuration", "DRAM DIMM installation rules", "Independent mode" | no document number; fetched | 2026-11-17 | the whole page | <https://pubs.lenovo.com/sr650/memory_configuration> |
| `lenovo-sr650-v2-installation` | lenovo | ThinkSystem SR650 V2 online documentation, topics "Memory module installation rules and order", "Independent mode" | no document number; fetched | 2026-11-17 | the whole page | <https://pubs.lenovo.com/sr650-v2/dimm_installation_rules> |
| `lenovo-sr950-v3-installation-installation` | lenovo | ThinkSystem SR950 V3 online documentation, topics "Memory module installation rules and order", "Independent memory mode installation order" | no document number; fetched | 2026-11-17 | the whole page | <https://pubs.lenovo.com/sr950-v3/memory_module_installation_order> |
| `lenovo-st650-v2-technical` | lenovo | ThinkSystem ST650 V2 online documentation, topics "Technical rules for memory modules", "Independent memory mode" | no document number; fetched | 2026-11-17 | the whole page | <https://pubs.lenovo.com/st650-v2/technical_rules_dimms> |
| `lenovo-x240-m5-9532-2951-installation` | lenovo | Lenovo Flex System x240 M5 Compute Node Types 9532 and 2951 Installation and Service Guide, online edition, topic "Installing a DIMM" | no document number online; fetched | 2026-11-17 | the whole page | <https://pubs.lenovo.com/x240-m5/installing_the_dimm> |
| `lenovo-x3650-m5-5462-installation-service` | lenovo | Lenovo System x3650 M5 Type 5462 Installation and Service Guide, online edition, topics "Installing a memory module", "DIMM installation sequence" | no document number online; fetched | 2026-11-17 | the whole page | <https://pubs.lenovo.com/x3650-m5-5462/c_dimm_rules> |
| `msi-d3052-s3661-user-guide` | msi | D3052 (MS-S3661) Server Motherboard User Guide | Revision V2.0 | 2025/06 | 18 | <https://download-2.msi.com/archive/mnu_exe/server/S3661-D3052-v2.0-UG.pdf> |
| `nec-express5800-r120b-1-user-guide` | nec | NEC Express5800/R120b-1 User's Guide | 3rd Edition, 10-2011, ONL-510_017_06_R120b1-100-99-1110 | 2011 | 383 | <https://www.58support.nec.co.jp/global/download/pdf/UGe_R120b1.pdf> |
| `oracle-e22313-13` | sun | Sun Server X3-2 (formerly Sun Fire X4170 M3) Service Manual | Part No. E22313-13, April 2014 | April 2014 | 62, 75, 76 | <https://docs.oracle.com/cd/E22368_01/pdf/E22313.pdf> |
| `oracle-e22369-02` | sun | Sun Fire X4170 M2 Server Service Manual | Part No. E22369-02, Revision A, May 2011 | May 2011 | 83 | <https://docs.oracle.com/cd/E19762-01/E22369-02/E22369-02.pdf> |
| `oracle-e23396-15` | sun | Sun Server X3-2L (formerly Sun Fire X4270 M3) Service Manual | Part No. E23396-15, April 2014 | April 2014 | 67 | <https://docs.oracle.com/cd/E23393_01/pdf/E23396.pdf> |
| `oracle-e38041-07` | sun | Sun Server X4-2 Service Manual | Part No. E38041-07, May 2014 | May 2014 | 66, 82 | <https://docs.oracle.com/cd/E36975_01/pdf/E38041.pdf> |
| `oracle-e48320-10` | sun | Oracle Server X5-2 Service Manual | Part No. E48320-10, January 2021 | January 2021 | 94, 96 | <https://docs.oracle.com/cd/E41059_01/pdf/E48320.pdf> |
| `other-2600-user-manual` | other | Nuvo-2600 Series User Manual | Rev. 1.0 | October 2022 | 11 | <https://nnz-ipc.ru/files/documentation/neousys/nuvo-2600-user-manual.pdf> |
| `other-631121-012` | other | 11th Generation Intel Core Processor Datasheet, Volume 1 | 631121-012, May 2023 | May 2023 | 1,88, 88 | <https://cdrdv2.intel.com/v1/dl/getContent/631121> |
| `other-rs5230-user-manual-pr2710p-manual` | other | RS5230 User Manual (FS-branded edition of the Powerleader PR2710P manual; the text says "All Powerleader rack servers" and shows the "PR2710P-24-disk server") | PDF dated 2023-11-23, 59 pages | 2023-11-23 | 24, 25 | <https://resource.fs.com/mall/doc/202311231103177tqpxp.pdf> |
| `quanta-t22hf-1u-service-guide` | quanta | QuantaPlex Series T22HF-1U Service Guide | Version 1.0 | May 5, 2019 | 49 | not recorded |
| `quanta-v2-0` | quanta | Open Compute Project Intel Motherboard Hardware v2.0 (Windmill) |  | April 11, 2012 | 23 | <http://www.opencompute.org/wp/wp-content/uploads/2012/05/Open_Compute_Project_Intel_Motherboard_v2.0.pdf> |
| `quanta-v3-1` | quanta | Facebook Server Intel Motherboard V3.1 (Leopard), Open Compute Project | Rev 1.00 | 2016-01-06 | 68 | <https://opencompute.org/documents/facebook-xeon-motherboard-v31> |
| `sm-mnl-1031` | supermicro | X8DA3/X8DAi User's Manual | MNL-1031 (revision page not read) |  | 27 | <https://www.supermicro.com/manuals/motherboard/5500/MNL-1031.pdf> |
| `sm-mnl-1060` | supermicro | X8DAH+ / X8DAH+-F / X8DAH+(-F)-LR User's Manual | MNL-1060, Revision 1.2b | May 31, 2012 | 11,33, 33, 34 | <https://www.supermicro.com/manuals/motherboard/5500/MNL-1060.pdf> |
| `sm-mnl-1062` | supermicro | X8DT3/X8DTi(-F, -LN4F) User's Manual | MNL-1062, Revision 1.2c | March 9, 2012 | 29, 30 | <https://www.supermicro.com/manuals/motherboard/5500/MNL-1062.pdf> |
| `sm-mnl-1076` | supermicro | X8DTL-3/-i/-3F/-iF User's Manual | MNL-1076, Revision 1.2b | Oct. 25, 2013 | 30 | <https://www.supermicro.com/manuals/motherboard/5500/MNL-1076.pdf> |
| `sm-mnl-1081` | supermicro | X8DTU / X8DTU-F User's Manual | MNL-1081, Revision 1.20 | Oct. 24, 2012 | 30 | <https://www.supermicro.com/manuals/motherboard/5500/MNL-1081.pdf> |
| `sm-mnl-1199` | supermicro | X8DTN+-F / X8DTN+-F-LR User's Manual | MNL-1199, Revision 1.1 | Oct. 11, 2011 | 36 | <https://www.supermicro.com/manuals/motherboard/5500/MNL-1199.pdf> |
| `sm-mnl-1258` | supermicro | X9DR3-LN4F+ / X9DRi-LN4F+ User's Manual | MNL-1258, Revision 1.1c | April 1, 2014 | 36, 39 | <https://www.supermicro.com/manuals/motherboard/C606_602/MNL-1258.pdf> |
| `sm-mnl-1278` | supermicro | X9DRW-3F / X9DRW-iF User's Manual | MNL-1278, Revision 1.1c | June 28, 2021 | 36, 39 | <https://www.supermicro.com/manuals/motherboard/C606_602/MNL-1278.pdf> |
| `sm-mnl-1281` | supermicro | X9SRA User's Manual | MNL-1281, Revision 1.1 | March 5, 2014 | 30 | <https://www.supermicro.com/manuals/motherboard/C600/MNL-1281.pdf> |
| `sm-mnl-1284` | supermicro | X9SRi / X9SRE (-F, -3F) User's Manual | MNL-1284, Revision 1.0c | February 22, 2013 | 38 | <https://www.supermicro.com/manuals/motherboard/C606_602/MNL-1284.pdf> |
| `sm-mnl-1307` | supermicro | X9DBL-i/3/iF/3F User's Manual | MNL-1307, Revision 1.0c | May 23, 2013 | 34, 36 | <https://www.supermicro.com/manuals/motherboard/C606_602/MNL-1307.pdf> |
| `sm-mnl-1428` | supermicro | X10SLM-F / X10SLL-F / X10SLL-SF / X10SLL-S User's Manual | MNL-1428, Revision 1.1b | June 21, 2016 | 35, 36 | <https://www.supermicro.com/manuals/motherboard/C222/MNL-1428.pdf> |
| `sm-mnl-1463` | supermicro | X10SL7-F User's Manual | MNL-1463, Revision 1.1a | August 19, 2014 | 31 | <https://www.supermicro.com/manuals/motherboard/C222/MNL-1463.pdf> |
| `sm-mnl-1464` | supermicro | X10SLH-F User's Manual | MNL-1464, Revision 1.1 | December 22, 2016 | 33, 33,37, 34 | <https://www.supermicro.com/manuals/motherboard/C222/MNL-1464.pdf> |
| `sm-mnl-1479` | supermicro | X10SAE User's Manual | MNL-1479, Revision 1.1c | January 8, 2015 | 30, 31 | <https://www.supermicro.com/manuals/motherboard/C226/MNL-1479.pdf> |
| `sm-mnl-1488` | supermicro | X9DRD-iF / X9DRD-LF User's Manual | MNL-1488, Revision 1.0a | March 21, 2014 | 40 | <https://www.supermicro.com/manuals/motherboard/C606_602/MNL-1488.pdf> |
| `sm-mnl-1491` | supermicro | X10DRi / X10DRi-T User's Manual | MNL-1491, Revision 1.2a | October 22, 2020 | 37 | <https://www.supermicro.com/manuals/motherboard/C606_602/MNL-1491.pdf> |
| `sm-mnl-1542` | supermicro | X10DRT-P/PT/PIBQ/PIBF User's Manual | MNL-1542, Revision 1.1b |  | 34, 35 | <https://www.supermicro.com/manuals/motherboard/C600/MNL-1542.pdf> |
| `sm-mnl-1557` | supermicro | X10DRW-i / X10DRW-iT User's Manual | MNL-1557, Revision 1.2b | January 22, 2020 | 37 | <https://www.supermicro.com/manuals/motherboard/C600/MNL-1557.pdf> |
| `sm-mnl-1560` | supermicro | X10DRC-T4+ / X10DRC-LN4+ / X10DRi-T4+ / X10DRi-LN4+ User's Manual | MNL-1560, Revision 1.2a | January 31, 2020 | 37, 38 | <https://www.supermicro.com/manuals/motherboard/C600/MNL-1560.pdf> |
| `sm-mnl-1561` | supermicro | A1SAM-2750F / A1SAM-2550F / A1SRM-2758F / A1SRM-2558F User's Manual | MNL-1561, Revision 1.0 (date not on the pages read) |  | 25, 25-26, 26 | <https://www.supermicro.com/manuals/motherboard/Atom_on-chip/MNL-1561.pdf> |
| `sm-mnl-1586` | supermicro | X10SRi-F User's Manual | MNL-1586, Revision 1.2 (date not on the pages read) |  | 34, 34,36 | <https://www.supermicro.com/manuals/motherboard/C612/MNL-1586.pdf> |
| `sm-mnl-1600` | supermicro | A1SA2-2750F / A1SA2-2550F User's Manual | MNL-1600, Revision 1.0 | April 23, 2014 | 25 | <https://www.supermicro.com/manuals/motherboard/Atom_on-chip/MNL-1600.pdf> |
| `sm-mnl-1643` | supermicro | X10SRA / X10SRA-F User's Manual | MNL-1643, Revision 1.1b | August 17, 2016 | 40, 41 | <https://www.supermicro.com/manuals/motherboard/C612/MNL-1643.pdf> |
| `sm-mnl-1646` | supermicro | C7X99-OCE / C7X99-OCE-F User's Manual | MNL-1646, Revision 1.1 | September 21, 2016 | 40, 41 | <https://www.supermicro.com/manuals/motherboard/X99/MNL-1646.pdf> |
| `sm-mnl-1726` | supermicro | X10SDV Mini-ITX Series User's Manual | MNL-1726, 1.0c | February 12, 2020 | 35 | <https://www.supermicro.com/manuals/motherboard/D/MNL-1726.pdf> |
| `sm-mnl-1773` | supermicro | X11DPi-N / X11DPi-NT User's Manual | MNL-1773, Revision 1.3 | July 30, 2021 | 16,37, 33 | <https://www.supermicro.com/manuals/motherboard/C620/MNL-1773.pdf> |
| `sm-mnl-1778` | supermicro | X11SSH-F / X11SSH-LN4F User Manual | MNL-1778, Revision 1.1 | July 30, 2018 | 33-34, 35 | <https://www.supermicro.com/manuals/motherboard/C236/MNL-1778.pdf> |
| `sm-mnl-1780` | supermicro | X10DRG-O+-CPU / X10DRG-OT+-CPU / X10DRG-O-PCIE User's Manual | MNL-1780, Revision 1.1a | February 03, 2020 | 38, 40 | <https://www.supermicro.com/manuals/motherboard/C612/MNL-1780.pdf> |
| `sm-mnl-1795` | supermicro | X11SSV-Q User's Manual | MNL-1795, Revision 1.0a | July 6, 2018 | 27 | <https://www.supermicro.com/manuals/motherboard/Q170/MNL-1795.pdf> |
| `sm-mnl-1813` | supermicro | C7Z170-M / C7H170-M User's Manual | MNL-1813, Revision 1.0c | May 26, 2017 | 39,41, 40, 41 | <https://www.supermicro.com/manuals/motherboard/Z170/MNL-1813.pdf> |
| `sm-mnl-1845` | supermicro | X10SRM-F / X10SRM-TF User Manual | MNL-1845, Revision 1.1 | April 25, 2019 | 31,33 | <https://www.supermicro.com/manuals/motherboard/C600/MNL-1845.pdf> |
| `sm-mnl-1900` | supermicro | X11SPi-TF User's Manual | MNL-1900, Revision 1.1b, October 11, 2022 | 1900 | 32-33, 34 | <https://www.supermicro.com/manuals/motherboard/C620/MNL-1900.pdf> |
| `sm-mnl-1907` | supermicro | X11DDW-L / X11DDW-NT User's Manual | MNL-1907, Revision 2.0, September 30, 2020 | 1907 | 33, 37 | <https://www.supermicro.com/manuals/motherboard/C620/MNL-1907.pdf> |
| `sm-mnl-1939` | supermicro | X11SPM-F / X11SPM-TF / X11SPM-TPF User's Manual | MNL-1939, Revision 1.1c, March 10, 2020 | 1939 | 34 | <https://www.supermicro.com/manuals/motherboard/C620/MNL-1939.pdf> |
| `sm-mnl-1946` | supermicro | X11DPL-i User's Manual | MNL-1946, Revision 1.1b, April 10, 2020 | 1946 | 30, 33 | <https://www.supermicro.com/manuals/motherboard/C620/MNL-1946.pdf> |
| `sm-mnl-1951` | supermicro | A2SDi-HLN4F Series User's Manual | MNL-1951, Revision 1.0a, February 24, 2020 | 1951 | 27-28, 28-29 | <https://www.supermicro.com/manuals/motherboard/Atom_on-chip/MNL-1951.pdf> |
| `sm-mnl-1983` | supermicro | X11QPH+ User's Manual | MNL-1983, Revision 1.1c, September 28, 2020 | 1983 | 33, 35 | <https://www.supermicro.com/manuals/motherboard/C620/MNL-1983.pdf> |
| `sm-mnl-2005` | supermicro | X11SRA / X11SRA-F / X11SRA-RF User Manual | MNL-2005, Revision 1.1b, January 29, 2021 | 2005 | 31 | <https://www.supermicro.com/manuals/motherboard/C420/MNL-2005.pdf> |
| `sm-mnl-2018` | supermicro | C9Z390-CG-IW User's Manual | MNL-2018, Revision 1.0a, May 14, 2019 | 2018 | 32-33,36, 37 | <https://www.supermicro.com/manuals/motherboard/Z390/MNL-2018.pdf> |
| `sm-mnl-2019` | supermicro | X11SDV-TLN2F series User's Manual | MNL-2019, Revision 1.0a, April 02, 2021 | 2019 | 24-25 | <https://www.supermicro.com/manuals/motherboard/D/MNL-2019.pdf> |
| `sm-mnl-2087` | supermicro | X11SCA / X11SCA-W / X11SCA-F User Manual | MNL-2087, Revision 1.2, January 13, 2022 | 2087 | 30,34,37, 34, 36 | <https://www.supermicro.com/manuals/motherboard/X11/MNL-2087.pdf> |
| `sm-mnl-2088` | supermicro | X11SCL-IF User's Manual | MNL-2088, Revision 1.1, May 06, 2020 | 2088 | 29 | <https://www.supermicro.com/manuals/motherboard/X11/MNL-2088.pdf> |
| `sm-mnl-2117` | supermicro | C9X299-PG300F User's Manual | MNL-2117, Revision 1.0c | November 12, 2020 | 30, 31 | <https://www.supermicro.com/manuals/motherboard/X299/MNL-2117.pdf> |
| `sm-mnl-2135` | supermicro | SBI-6119P-T3N SuperBlade Module User's Manual | MNL-2135, Revision 1.0b | April 19, 2023 | 50 | <https://www.supermicro.com/manuals/superblade/MNL-2135.pdf> |
| `sm-mnl-2172` | supermicro | M11SDV-4C/4CT/8C/8CT/8C+-LN4F User Manual | MNL-2172, Revision 1.1a | December 30, 2021 | 27 | <https://www.supermicro.com/manuals/motherboard/EPYC3000/MNL-2172.pdf> |
| `sm-mnl-2234` | supermicro | X12DPU-6 User's Manual | MNL-2234, Revision 1.0b | March 11, 2022 | 42 | <https://www.supermicro.com/manuals/motherboard/X12/MNL-2234.pdf> |
| `sm-mnl-2239` | supermicro | X12DAi-N6 User's Manual | MNL-2239, Revision 1.1a | April 22, 2022 | 41,43 | <https://www.supermicro.com/manuals/motherboard/X12/MNL-2239.pdf> |
| `sm-mnl-2270` | supermicro | X12SPL-LN4F / X12SPL-F User's Manual | MNL-2270, Revision 1.1c | November 20, 2023 | 33 | <https://www.supermicro.com/manuals/motherboard/X12/MNL-2270.pdf> |
| `sm-mnl-2299` | supermicro | H12DSG-O-CPU User's Manual | MNL-2299, Revision 1.0a | April 14, 2021 | 33 | <https://www.supermicro.com/manuals/motherboard/EPYC7000/MNL-2299.pdf> |
| `sm-mnl-2314` | supermicro | H12SSL-i/C/CT/NT User's Manual | MNL-2314, Revision 1.0a | April 22, 2021 | 31 | <https://www.supermicro.com/manuals/motherboard/EPYC7000/MNL-2314.pdf> |
| `sm-mnl-2336` | supermicro | M12SWA-TF User's Manual | MNL-2336, Revision 1.1c | September 08, 2025 | 32-33 | <https://www.supermicro.com/manuals/motherboard/M12/MNL-2336.pdf> |
| `sm-mnl-2358` | supermicro | X12SAE-5 / X12SCA-5F User's Manual | MNL-2358, Revision 1.1c | September 30, 2026 | 32, 32-33 | <https://www.supermicro.com/manuals/motherboard/X12/MNL-2358.pdf> |
| `sm-mnl-2362` | supermicro | X12SDV-4C/8C/10C-SP6F, X12SDV-8CE-SP4F, X12SDV-10CR-SP6F User's Manual | MNL-2362, Revision 1.0a | June 04, 2024 | 26-27, 27 | <https://www.supermicro.com/manuals/motherboard/X12/MNL-2362.pdf> |
| `sm-mnl-2413` | supermicro | X13SEW-TF / X13SEW-F User's Manual | MNL-2413, Revision 1.1c | May 06, 2025 | 33 | <https://www.supermicro.com/manuals/motherboard/X13/MNL-2413.pdf> |
| `sm-mnl-2425` | supermicro | X13SAE / X13SAE-F User's Manual | MNL-2425, Revision 1.2c | September 30, 2026 | 34 | <https://www.supermicro.com/manuals/motherboard/X13/MNL-2425.pdf> |
| `sm-mnl-2440` | supermicro | X12SDV-4C/8C/14C/16C/20C-SPT8F User's Manual | MNL-2440, Revision 1.0c | May 02, 2025 | 24,26 | <https://www.supermicro.com/manuals/motherboard/X12/MNL-2440.pdf> |
| `sm-mnl-2509` | supermicro | X13DEI / X13DEI-T User's Manual | MNL-2509, Revision 1.0c | August 25, 2025 | 62 | <https://www.supermicro.com/manuals/motherboard/X13/MNL-2509.pdf> |
| `sm-mnl-2517` | supermicro | X13SAN-H/-E/-L/-C User's Manual | MNL-2517, Revision 1.0a | June 09, 2025 | 29 | <https://www.supermicro.com/manuals/motherboard/X13/MNL-2517.pdf> |
| `sm-mnl-2627` | supermicro | H13SAE-MF User's Manual | MNL-2627, Revision 1.1b | November 03, 2026 | 33 | <https://www.supermicro.com/manuals/motherboard/H13/MNL-2627.pdf> |
| `sm-mnl-h8dcl` | supermicro | H8DCL-6/i(F) Serverboard User's Manual | MNL-H8DCL-(i)(6)(F), Revision 1.2 | March 04, 2014 | 31 | <https://www.supermicro.com/manuals/motherboard/SR56x0/MNL-H8DCL-(i)(6)(F).pdf> |
| `sm-mnl-h8dg` | supermicro | H8DG6/i(-F) Serverboard User's Manual | MNL-H8DGi(6)(-F), Revision 1.2a | March 06, 2014 | 29 | <https://www.supermicro.com/manuals/motherboard/SR56x0/MNL-H8DGi(6)(-F).pdf> |
| `sm-mnl-h8qg-6` | supermicro | H8QG6/i-F Serverboard User's Manual | MNL-H8QG(6)(i)-F, Revision 1.2a | March 07, 2014 | 29 | <https://www.supermicro.com/manuals/motherboard/SR56x0/MNL-H8QG(6)(i)-F.pdf> |
| `sm-mnl-h8qgl-6` | supermicro | H8QGL-6/iF Serverboard User's Manual | MNL-H8QGL-(6)(i)F, Revision 1.1a | March 11, 2014 | 29 | <https://www.supermicro.com/manuals/motherboard/SR56x0/MNL-H8QGL-(6)(i)F.pdf> |
| `sm-mnl-h8scm-f` | supermicro | H8SCM(-F) Serverboard User's Manual | MNL-H8SCM(-F), Revision 1.2c | March 11, 2014 | 28 | <https://www.supermicro.com/manuals/motherboard/SR56x0/MNL-H8SCM(-F).pdf> |
| `sm-mnl-h8sgl-f` | supermicro | H8SGL(-F) Serverboard User's Manual | MNL-H8SGL(-F), Revision 1.2c | March 11, 2014 | 29 | <https://www.supermicro.com/manuals/motherboard/SR56x0/MNL-H8SGL(-F).pdf> |
| `sm-x10-memory-guide` | supermicro | Memory Configuration Guide, X10 Series DP Motherboards (Socket R3) |  | 2014-09-08 | 2 | <https://www.supermicro.com/support/resources/memory/X10_memory_config_guide.pdf> |
| `sm-x11-user-guide` | supermicro | Memory Configuration for X11 UP/DP/MP Motherboards, User's Guide | Revision 1.1a | February 7, 2020 | 6, 19 | <https://www.supermicro.com/support/resources/memory/X11_memory_config_guide.pdf> |
| `sm-x12-memory-guide` | supermicro | Memory Configuration for X12 motherboards, User's Guide | Revision 1.0c | May 21, 2021 | 4 | <https://www.supermicro.com/support/resources/memory/X12_memory_config_guide.pdf> |
| `tyan-k8qs-s4882-user-manual` | tyan | Thunder K8QS Pro S4882 user's manual | Revision 1.01, (c) TYAN 2003-2004 |  | 27 | <https://web.archive.org/web/20081207091103id_/http://www.tyan.com/manuals/m_s4882_101.pdf> |
| `tyan-s7010-user-manual` | tyan | S7010 user's manual | Version 1.0, (c) 2009 MiTAC | 2009 | 27 | <https://web.archive.org/web/20090419093950id_/http://www.tyan.com:80/manuals/S7010_UG_v1.0.pdf> |
| `tyan-s7012-user-manual` | tyan | S7012 user's manual | Version 1.1 (file S7012_UG_v1.1_06212012.pdf), (c) 2009 MiTAC | 2009 | 26 | <https://web.archive.org/web/20121119035656id_/http://www.tyan.com:80/manuals/S7012_UG_v1.1_06212012.pdf> |
<!-- /population-documents -->

## Boots

A fatal memory error ends its boot. A host that resets after it and fails again shows the same place in boot after boot, and the records of each stop are read by the next boot ([crash-loop.md](crash-loop.md)). The engine looks at the latest `loop.window_boots` boots of every host, oldest first. Boots whose start is known only relative to another boot are left out.

For every DIMM and boot it counts the corrected and the uncorrected errors, at the finest place the records give. A boot ended in a stop when a record shows a fatal hardware error in it: a fatal machine check, a panic text that names one, or a record the next boot read out of the banks or the boot error region. The stop is tied to the DIMM when exactly one DIMM had an uncorrected error in that boot. The stops line of a host says how many boots of the window ended in a fatal hardware error, when the latest did and how long that boot ran, and on a second line where the uncorrected errors lie (one place, one DIMM, spread, or no record names a DIMM). The rules R-LOOP-01, R-LOOP-02 and R-LOOP-03 read the window.

Two boots with an uncorrected error each on two different DIMMs fire no rule; each DIMM gets a check that names the other, since a cause the two share fits as well. A fault of corrected errors that had errors in two or more boots of the window says so in its R-ROW-03 or R-SOFT-01 statement.

On the host finding, an address that had uncorrected errors in at least `rule.loop.min_boots` boots gets a statement of its own. When boots stopped and no record of them names a DIMM, the finding lists what `my preflight` found a reset loses on the host.

The thresholds of the R-LOOP rules are defaults. They have not been compared with field data: the lab loops of [validation-lab.md](validation-lab.md) show that the rules fire on the records a loop leaves, and say nothing about how often a host in service loops.

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
| R-LOOP-01 | 90 | R-HOST-02 | 70 |
| R-LOOP-02 | 80 | R-LOOP-03 | 70 |
| R-FW-02 | 75 | R-FW-03 | 65 |
| no rule (ok) | 85 | | |

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
| the firmware record that decided the grade was classed by its wording, no table has it | -5 |

A firmware record is read through a mapping table whose basis is `registry` or `documents` ([vendors.md](vendors.md)): a finding whose deciding record came through such a table carries an open check that says the meaning was not checked on real hardware. The firmware's own limit of corrected errors (`ce-threshold`) is an evidence line of R-SOFT-01 or R-ROW-03 and fires no rule by itself: a count without a place decides no replacement here, and a prediction has a rule of its own. A log of the BMC that was cleared or is full, and firmware records about the DIMM no table classes, are listed among the missing data.

R-FW-02, R-FW-03 and the extensions of R-FW-01, R-REP-01, R-REP-02 and R-QUIET-01 for firmware records use the threshold `rule.quiet.days` and no threshold of their own. They were compared with no field data: no public log of a real machine holds these firmware records ([vendors.md](vendors.md)).

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
| `loop.window_boots` | 16 | latest boots of a host the recurrence across boots looks at |
| `rule.loop.min_boots` | 2 | R-LOOP-01, R-LOOP-02: boots with uncorrected errors at one place or on one DIMM |
| `rule.loop.scatter_boots` | 3 | R-LOOP-03: boots with uncorrected errors tied to a DIMM before their spread is judged |
| `rule.loop.scatter_share` | 0.5 | R-LOOP-03: largest share of those boots one DIMM may have for the errors to count as spread |
| `check.host_wide_min_dimms` | 3 | DIMMs of a host with unconfined errors in the same period from which a common cause is mentioned |
| `evidence.max_records` | 20 | record references listed with one statement |

`--set KEY=VALUE` changes one threshold for a run, and `--config FILE` reads `key = value` lines. A bundle records the thresholds its findings were computed with when they differ from the defaults, and `my open` says so.

"Recent" and "quiet since" are counted back from the reference time of a host: the time of its latest scan, or of its latest record in a case that was imported. A bundle therefore gives the same finding whenever it is opened.
