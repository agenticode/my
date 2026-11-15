# Vendor analyzers

MemorY classifies faults with public knowledge: the Level 1 fault modes of the OCP FMFM framework, from the rows, columns and banks the records name. Some of what decides a case is known to the maker of the DRAM device alone: which logical rows lie next to each other inside the device, what error rate is usual for a part number, what a pattern of failing data lines means. That knowledge stays outside the open core. A vendor analyzer supplies it as a separate program.

```
my dimm CPU0_C1 --plugin /opt/vendor/analyzer
my report case.mycase -o fa.html --audience fa --plugin /opt/vendor/analyzer
```

The program can be written in any language and carry any license. It never links with MemorY: the two talk through one JSON document each way. [examples/plugin](../examples/plugin) holds a small analyzer in Python, a request and its response.

| Flag | Meaning |
| --- | --- |
| `--plugin PROGRAM` | run PROGRAM on the error records before the engine. Every command that shows findings takes it: `my`, `dimm`, `why`, `map`, `timeline`, `bundle`, `report`, `export`, `import`, also behind `my open FILE` |
| `--plugin-timeout TIME` | how long the program may run (default 10s) |
| `--plugin-logs N` | how many records before the newest one are sent per DIMM (default 1000) |

## The contract in short

| | |
| --- | --- |
| name | `my-analyzer/1` |
| start | the program is run once per command, without arguments and without a shell |
| input | one request, a JSON document, on standard input |
| output | one response, a JSON document, on standard output |
| state | none: the program gets every record it needs in the request and keeps nothing between two runs |
| time | 10 seconds unless `--plugin-timeout` says otherwise; then the program is stopped |
| size | a response of at most 16 MiB |
| failure | a non-zero exit status, with one line on standard error that says why |
| environment | that of the command, and `MY_ANALYZER_CONTRACT=my-analyzer/1` |

This is the shape the FMFM framework gives its analyzers: the operator sends the newest error log and a number of logs before it, and the analyzer answers without a database of its own. An existing DRAM fault analyzer can be wrapped by a script that turns a request into its input and its output into a response.

## Request

One entry per DIMM that has error records. Errors tied to no DIMM are not sent.

```json
{
  "Contract": "my-analyzer/1",
  "Core": { "Name": "MemorY", "Version": "1.0.0", "EngineVersion": "2" },
  "RequestDT": "2026-11-13T09:30:00.000Z",
  "PreviousLogs": 1000,
  "Dimms": [
    {
      "DimmId": "d878e1a15b6e30a44057",
      "DimmSerialNumber": "3B9C56",
      "Manufacturer": "Example Memory",
      "PartNumber": "EXM64G48R2X4-A",
      "MemoryType": "DDR5",
      "ModuleType": "RDIMM",
      "SizeBytes": 68719476736,
      "Ranks": 2,
      "DeviceWidth": 4,
      "Location": { "HostID": "r12-n034", "SocketId": 0, "ChannelId": 2, "ChannelSlotId": 0, "slot": "CPU0_C1" },
      "Corrected": 1231,
      "Uncorrected": 1,
      "NewLog": { "ErrorDT": "2026-11-12T23:49:30.494Z", "ErrorSeverity": "CE", "Rank": 0, "DeviceId": 7, "Row": 6703, "...": "..." },
      "PreviousLogs": [ { "ErrorDT": "...", "...": "..." } ],
      "EarlierLogs": 231
    }
  ]
}
```

| Field | Meaning |
| --- | --- |
| `Contract` | the name and version of the contract |
| `Core` | the program that asks, and the version of its engine |
| `PreviousLogs` | how many records before the newest one are sent per DIMM at most (`--plugin-logs`, default 1000) |
| `DimmId` | the key of the DIMM in this request; the response names a DIMM by it. It is the ledger's key and no serial number |
| `DimmSerialNumber`, `Manufacturer`, `PartNumber`, `MemoryType`, `ModuleType`, `SizeBytes`, `Ranks`, `DeviceWidth` | what the ledger knows about the module; a field no source gave is left out |
| `Location` | host, socket, channel and slot numbers, and the name of the slot |
| `Corrected`, `Uncorrected` | the errors of the DIMM in the ledger, each counted once |
| `NewLog` | the newest error record |
| `PreviousLogs` | the records before it, the oldest first |
| `EarlierLogs` | the number of older records that were not sent |

`NewLog` and `PreviousLogs` are records with the field names of the FMFM runtime error log, as [fmfm-mapping.md](fmfm-mapping.md#runtime-error-log) lists them. A field the platform did not report is left out: the records of a host that gives addresses and no rows have `OsPhysicalAddress` and no `Row`, and `grade` (`A`, `B`, `C`) says how much of the place a record has.

## Response

```json
{
  "Contract": "my-analyzer/1",
  "Analyzer": { "Name": "example-analyzer", "Version": "1.0", "Vendor": "Example Memory", "PreviousLogs": 1000 },
  "Dimms": [
    {
      "DimmId": "d878e1a15b6e30a44057",
      "Level2": {
        "Lines": [ { "Label": "rows with most errors", "Value": "row 0x1a2f: 1206, row 0x1a30: 26" } ],
        "Notes": [ "Example values: this analyzer knows no real device." ]
      },
      "AdjacentRows": [
        { "Rank": 0, "DeviceId": 7, "BankGroup": 0, "Bank": 3, "Row": 6703, "Neighbors": [6702] },
        { "Rank": 0, "DeviceId": 7, "BankGroup": 0, "Bank": 3, "Row": 6702, "Neighbors": [6703] }
      ],
      "Baseline": { "Percentile": 99.0 }
    }
  ]
}
```

A DIMM the analyzer has nothing to say about is left out. Each part of an analysis is optional.

| Field | What the core does with it |
| --- | --- |
| `Analyzer.Name`, `.Version`, `.Vendor` | named in the first line of a screen, in the JSON envelope (`analyzer`), in an FMFM export (`vendor_analyzer`) and beside the Level 2 output of a report |
| `Analyzer.PreviousLogs` | the number of previous records the analyzer works with. When it is larger than what was sent and a DIMM has more on record, the run says so |
| `Level2` | labelled values and notes beyond the public classification. Shown in the report for failure analysis (`my report --audience fa`) and nowhere else: no screen, no customer report, no export |
| `AdjacentRows` | the physical neighbours of a row. The engine uses them when it decides which rows are one fault |
| `Baseline.Percentile` | where the error count of the DIMM stands among the DIMMs of its part number that the vendor knows, 0 to 100. It becomes the third line of the baseline on the finding ("vendor reference for the part number") |

### Level 2

The FMFM framework calls what a memory vendor shares only under NDA "Level 2": bit line maps, impacted rows, miscorrection by the on-die ECC. The contract carries it as text the core does not interpret. Which report holds it is the one decision the core makes: the report for failure analysis.

### Adjacent rows

Error records carry logical row addresses. Two rows with neighbouring numbers can be far apart inside the device, and the other way round. The engine groups rows into one fault when they are neighbours, so the answer changes findings: rows the analyzer names neighbours become one multi-row fault, and rows it separates become two faults.

- The place is given the way the records give it: `Rank`, `DeviceId`, `BankGroup`, `Bank` as far as the records of the platform carry them, and `Row`.
- A row with an entry has the neighbours the entry names, and no others. An empty list means "no neighbour".
- A row without an entry keeps the rows numbered one below and one above it, less those whose entry does not name it, plus the rows whose entry names it.
- Two entries must agree: when row 7 names row 9 and row 9 has an entry, that entry names row 7. A response where they disagree is refused.

The example analyzer answers for every row with errors and for its partner, which is the simple way to stay consistent.

## What is checked

The core trusts a response no further than it can check it.

| Check | A response is refused when |
| --- | --- |
| form | it is no single JSON document, is not UTF-8, or a field has another type than the contract gives it |
| contract | `Contract` is not `my-analyzer/1` |
| analyzer | `Name` or `Version` is empty, longer than 120 bytes, or holds a control character |
| DIMMs | a `DimmId` is not one of the request, or stands twice |
| Level 2 | more than 64 lines or 16 notes for one DIMM; a label that is empty or longer than 80 bytes; a value longer than 400, a note longer than 600; a control character or a line break in any of them |
| rows | more than 20,000 rows for one DIMM or 16 neighbours for one row; a negative number; a row that is its own neighbour, stands twice, or names a neighbour twice; two entries that disagree |
| baseline | a percentile outside 0 to 100 |

Fields the contract does not know are allowed, as the FMFM framework allows vendors fields of their own. They are not read, and the run names them, so that a misspelt field does not vanish without a word.

Texts of an analyzer end up on a terminal and in an HTML file. Control characters are refused for that reason, and the report escapes every text.

## When it fails

A command that was given `--plugin` and cannot have the analysis stops with exit code 70 and prints the reason: the program cannot be run, did not answer in time, exited with an error (with the first line it wrote to standard error), or answered something the checks refuse. Nothing else is printed. A finding that silently lacks what the reader asked for would pass for something it is not; without `--plugin` the same command gives the findings of the open core.

## What the core supplies and what it does not

- The history. The ledger keeps every record; the analyzer gets the newest record and the ones before it on every run and needs no storage.
- The identity of the DIMM, its place and its totals.
- No Level 1 result. The analyzer sees the records, as an FMFM DRAM fault analyzer does, and the engine classifies after it has the analyzer's rows.
- No repair history and no ECS register readings in this version of the contract.

The exporter (`my serve`) runs the open core and takes no analyzer.

## Writing one

1. Read standard input to its end and parse it as JSON. Check `Contract`.
2. For each entry of `Dimms`, look at `NewLog` and `PreviousLogs`. Fields can be missing.
3. Write the response to standard output and exit with 0. On failure write one line to standard error and exit with another status.

To see a request, save what the program reads: `tee /tmp/request.json` in front of it in a wrapper script, or start from [examples/plugin/request.json](../examples/plugin/request.json). `internal/plugin` has the types (`Request`, `Response`) and the checks; its tests run a program through each way a response can be wrong that the table above lists.

## Known gaps

- No analyzer of a memory vendor has been run against this contract. The example knows no real device.
- One request holds every DIMM with errors of the ledger the command works on, also when that is a whole fleet. Requests are not split.
- The customer report does not name the analyzer, although the rows it named can change which errors count as one fault.
- The request carries error records only: no repairs, no page offlines, no ECS readings, no temperatures.
- A percentile is one number per DIMM. The contract has no way to say which population it was taken from.
