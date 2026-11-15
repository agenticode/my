# FMFM forms

The OCP Fleet Memory Fault Management (FMFM) framework describes what a fleet operator should log about memory errors and what an analyzer should answer. MemorY meets it at three points.

| Point | What MemorY does |
| --- | --- |
| reading | `my import` reads FMFM boot, runtime and crash logs ([compatibility.md](compatibility.md#fmfm-logs)) |
| runtime error log | error records from any input are written with the field names of the FMFM runtime error log: in the appendix of a report, and by `my export fmfm --records` |
| analysis log | `my export fmfm` writes the findings in the form the framework asks of an analyzer |

The framework document (FMFM Framework Requirements v1.0) names the fields of the runtime log and lists what an analysis log should say. The JSON schemas that fix names, nesting and types are kept on a shared drive of the workgroup and are not public. This page therefore says, field by field, which names are the document's and which are this tool's. Fields in lower case are additions of MemorY.

## What is never written

A Common Platform Action Descriptor (CPAD). The framework has analyzers emit CPADs to drive repairs and part replacements. MemorY takes no action and asks for none: a suggested action in its log is a sentence for a person. The document says so itself with `"action_descriptors": "none"`.

## Runtime error log

One record per error. The ledger keeps an error under these names, so nothing is translated.

| Field | In the framework document | Filled from |
| --- | --- | --- |
| `ErrorDT` | yes | the time of the record, UTC with milliseconds |
| `ErrorScrubType` | yes: `Demand`, `Patrol`, `Other` | where the source says how the error was found |
| `ErrorSeverity` | yes: `Informational`, `CE`, `Recoverable`, `UE`, `Fatal` | every record |
| `HostID` | yes | the host name of the ledger |
| `SocketId`, `ChannelId`, `ChannelSlotId` | yes | the place numbers a source gave |
| `SubChannel` | yes | FMFM logs only; no other input reports it |
| `Rank`, `DeviceId`, `BankGroup`, `Bank`, `Row`, `Column` | yes | as far as the platform decodes them ([grades](compatibility.md)) |
| `ErrorVector` | yes | the failing bits as the source names them: `bit:<n>`, `dq:0x<mask>` or `nibble:0x<mask>` |
| `OsPhysicalAddress` | yes | the system physical address, as text in hexadecimal |
| `seconds_since_boot` | no | in place of `ErrorDT` for a record of a kernel log without dates |
| `count` | no | more than one error in one record: a counter reading, a record the source counted |
| `slot` | no | the name of the slot on the board |
| `grade` | no | the observation grade of the record: A, B or C |
| `source`, `raw_ref` | no | where the record came from: the source and the place in it |

The fields `ProcessorMetadata`, `EcsModeRegisters` and `DimmTemp` of the runtime log are not written. ECS register readings are kept in the ledger as readings of their own.

A field a source did not report is left out of the record. Nothing is filled in: a record of an AMD host has an address and no row, a counter reading has neither, and `grade` says which case it is.

Each error is written once. Where two sources report one error (a kernel log line and a rasdaemon row), the record that counts is written. A counter reading is written with the errors that located records do not already account for.

## Analysis log

`my export fmfm` writes one JSON document per host.

```json
{
  "format": "fmfm-analysis-log",
  "format_version": 1,
  "OutputDT": "2026-11-13T09:30:00.000Z",
  "Analyzer": { "Name": "MemorY", "Version": "...", "engine_version": "...", "level": 1 },
  "HostID": "r12-n034",
  "action_descriptors": "none",
  "AnalysisLog": [ ... ],
  "RuntimeLog": [ ... ]
}
```

`AnalysisLog` has one record per DIMM that has errors or a finding (`--all`: every DIMM). `RuntimeLog` is there with `--records`. `level` is 1: the log holds the public fault classification and nothing a vendor keeps under NDA.

When the run used a [vendor analyzer](plugin-contract.md) (`--plugin`), the document names it in `vendor_analyzer` (`name`, `version`, `vendor`): the rows it named neighbours can change which errors count as one fault. Its Level 2 output is not written.

The framework document lists these items for the output of an analyzer (section 6, "Recommended SFA Log Output Data Format"). The names are this tool's, in the style of the runtime fields.

| Field | Item of the document | Filled from |
| --- | --- | --- |
| `OutputDT` | timestamp of the analyzer output | the time of the run |
| `DimmSerialNumber` | faulty DIMM serial number | the serial number, when a source gave a usable one |
| `Location.HostID`, `.SocketId`, `.ChannelId`, `.ChannelSlotId`, `.SubChannel` | faulty DIMM location | the place numbers of the slot |
| `FaultMode` | inferred fault mode, Level 1 | the mode of the fault with the most errors |
| `RiskClassifier` | risk classifier | see below |
| `SeverityClassifier` | severity classifier | see below |
| `RasActionSuggestion` | RAS action suggestion, `[action, target]` | see below |
| `SystemIssue` | "system issue Y/N" of the DRAM fault analyzer output | a rule that holds replacement fired |
| `Confidence` | confidence level, 0 to 100 | the confidence of the finding |

Additions: `manufacturer`, `part_number`, `dimm_uid`, `present` (false for a DIMM that is no longer in the host), `corrected`, `uncorrected`, `faults` (every fault with its mode and place), `finding` (grade, rules, evidence, checks, missing data, next step), `observation` (errors by observation grade) and `not_filled`.

### Fault mode

| Ledger | `FaultMode` |
| --- | --- |
| `single-bit` | `Single bit fault` |
| `row` | `Row fault` |
| `multi-row` | `Multi-Row fault` |
| `column` | `Column fault` |
| `bank` | `Bank fault` |
| `device` | `Device fault` |
| `dimm` | `DIMM fault` |
| `system-issue` | `System issue` |
| `undetermined` | left out |

"Potential Row Hammering", the ninth mode of the document, is never written: nothing in the records tells it from a row fault.

A fault whose mode is an estimate (errors known by address only, or a platform that does not report the device) carries `"estimated": true`.

### Severity

The document has three classes: high for a fault that no action in the field recovers or that is still there after one, medium for a fault an action may recover, low for "keep monitoring".

| Finding | `SeverityClassifier` |
| --- | --- |
| replace now | `high` |
| consider replacement, with errors back after a repair (R-REP-02) | `high` |
| consider replacement | `medium` |
| observe | `low` |
| hold replacement | left out: what is at fault is not known yet |
| ok | left out |

### Risk

| Records and finding | `RiskClassifier` |
| --- | --- |
| an uncorrected error on record | `Risk to UE` |
| corrected errors with R-ROW-03, R-OFF-02 or R-REP-02 | `Risk to repeated CE or CE storm` |
| other corrected errors | `Low Risk` |
| hold replacement | left out |

`Risk to SDC` is never written. Whether a fault pattern can pass the error correction of a platform unseen depends on its ECC scheme, which the open core does not know. A [vendor analyzer](plugin-contract.md) is the place for it.

### Suggested action

The list of the document has sPPR, hPPR, page offline, bank sparing, fast boot, cold boot, DIMM reseat and faulty DIMM removal. A finding of MemorY names a next step in its own words (`finding.next_action`). Only where that step is one of the document's actions is it also written as a suggestion:

| Next step of the finding | `RasActionSuggestion` |
| --- | --- |
| replace the DIMM; plan a replacement | `{"Action": "Faulty DIMM Removal", "Target": "S/N <serial>, <slot>"}` |
| any other | empty list |

MemorY suggests no repair. Which row a post package repair should take is a decision of the platform and its vendor analyzer.

### What is not filled

`not_filled` names the fields of a record that are left out because no source gave what they need, for example:

```json
"not_filled": ["DimmSerialNumber", "Location.SocketId", "Location.ChannelId", "Location.ChannelSlotId", "Location.SubChannel", "FaultMode"]
```

This is the record of a DIMM known through EDAC counters alone: a count per DIMM, no serial number, no place numbers, no mode. `observation` says the same from the other side (`"grade_c": 40`).

`Location.SubChannel` is in the list of every record. No source of this tool gives the sub-channel of a DIMM.

## Reading it back

A document written with `--records` is itself an FMFM log: `my import analysis.json` finds the runtime records in it and reads them as it reads any FMFM runtime log. The analysis records are no error records and are passed over. A test writes the 1,285 errors of the demo host and reads them back field by field (`internal/fmfmlog`, `TestRuntimeLogReadsBack`).

## Known gaps

- The names of the analysis log are not checked against the schema of the workgroup, which is not public. A consumer that has the schema needs a mapping of names.
- An analysis record is per DIMM. The document's analyzer answers per new error log; a record here is the state of the DIMM at the time of the run.
- The host finding (rules R-HOST-01 and R-HOST-02), which speaks about errors and page offlines that name no DIMM, is not in the analysis log: it has no DIMM to be a record of. With `--records` its errors are in the runtime records, without the fields of a place.
- `Risk to SDC` and "Potential Row Hammering" are never written.
- The sub-channel, the chip select ID of stacked DIMMs and the history of failed devices, which the document lists for the DRAM fault analyzer, are not written.
