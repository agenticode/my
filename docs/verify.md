# Verifying a host with an injection plan

`my verify --expect plan.yaml` compares what a test injected into a host with what the sources of that host recorded. It answers one question about the path from the hardware to the ledger: does it lose or change anything? An error that is missing, one that is counted twice, a row or a channel that was decoded as another all show up as a line of the result.

The command injects nothing. The errors are put in by other means (ACPI EINJ, the EDAC debug interface, `mce-inject`, the error injection of an emulator), and the plan is the note of what was done. `tools/lab` holds the scripts that made the captures under `testdata/real`, each with its `plan.yaml`.

```
$ my verify --root testdata/real/7.3.0-mce --expect testdata/real/7.3.0-mce/plan.yaml
host lab-guest  plan mce
all 12 injections were found as planned
machine checks in a nested guest: 7 corrected and 3 uncorrected memory errors

  #  ID            INJECTED            WHERE        PLANNED  FOUND  RESULT
  1  ce-1          CE Demand           0x1060bc040        1      1  as planned
  2  ce-2          CE Demand           0x1060bf980        1      1  as planned
  3  ce-3          CE Demand           0x1060c0100        2      2  as planned
  4  ce-3-offline  page offline soft   0x1060c0000        1      1  as planned
  5  ce-4          CE Demand           0x1060c1fc0        3      3  as planned
  6  ce-4-offline  page offline soft   0x1060c1000        1      1  as planned
  7  ue-1          Recoverable Patrol  0x1060c2200        1      1  as planned
  8  ue-1-offline  page offline hard   0x1060c2000        1      1  as planned
  9  ue-2          Recoverable Patrol  0x1060c3400        1      1  as planned
 10  ue-2-offline  page offline hard   0x1060c3000        1      1  as planned
 11  ue-3          Recoverable         0x1060c4340        1      1  as planned
 12  ue-3-offline  page offline hard   0x1060c4000        1      1  as planned

 records       klog/offline 5, rasdaemon/mce_record 10, rasdaemon/memory_failure_event 3
               records that match an injection, by source; an error several sources report is found
               once

 method        mce-inject module, records handed to the machine check poller and exception handler;
               cpu Skylake-SP (family 6 model 85) emulated by QEMU without hardware virtualization;
               kernel 7.3.0-ras; rasdaemon 0.8.0
```

Without `--root` the command scans the host it runs on. `my open case.mycase verify --expect plan.yaml` compares the plan with a case bundle, so the comparison can be made away from the host.

## The plan

A plan is a text file in a small subset of YAML: scalars at the top, then a list of injections, each a flat map. Values are plain or in double quotes; numbers are decimal or hexadecimal with `0x`; lines that start with `#` are comments.

```yaml
format: 1
scenario: ghes
summary: "firmware error records: 24 corrected and 2 uncorrected errors on one DIMM"
method: "QMP inject-ghes-v2-error of QEMU"
kernel: 6.19.10
injections:
  - id: row-1
    time: 2026-11-15T11:09:38.123Z
    inject: error
    severity: CE
    count: 1
    address: 0x100010140
    socket: 0
    channel: 0
    slot: 0
    rank: 0
    device: 7
    bank_group: 1
    bank: 2
    row: 0x1a2f
    column: 0x10
    bit: 5
    label: "DIMM 0"
  - id: ue-1-offline
    inject: page-offline
    how: hard
    count: 1
    address: 0x100238000
```

| Field | Meaning |
| --- | --- |
| `format` | 1 |
| `scenario` | a name for the plan |
| `summary`, `method` | free text, shown with the result |
| other scalars at the top | kept and shown (`kernel`, `rasdaemon`, `cpu`) |
| `id` | a name for the injection, unique in the plan |
| `time` | when it was injected (RFC 3339); records dated more than ten minutes before it belong to something else |
| `inject` | `error`, or `page-offline` for a page the test or the kernel took out of use |
| `severity` | of an error: `CE`, `Recoverable`, `UE`, `Fatal`, `Informational` |
| `scrub` | `Demand`, `Patrol` or `Other`, when the plan knows |
| `count` | how many errors were injected; for a page, 1 when it must be offline and 0 when it must stay in use |
| `how` | of a page offline: `hard` or `soft` |
| `address` | the system physical address |
| `dpa`, `device_name` | the address within a CXL memory device, and the kernel's name of the device |
| `socket`, `channel`, `slot`, `rank`, `device`, `bank_group`, `bank`, `row`, `column`, `bit` | the place inside the DIMM, as far as the test knows it |
| `label` | the DIMM, as the host names it |
| `note` | free text |

An error needs an address, a row, a label or a device to be found by.

## How an injection is found

An error is looked up by its address: records of the same cache line and severity class, dated no earlier than ten minutes before the injection. Without an address it is looked up by row, column and bank, and without those by its label among the records that carry no row. A record belongs to one injection. Several injections at one address are served in the order of their times, and each takes as many errors as it put in.

An error that several sources report (the kernel log, the rasdaemon database, a firmware record next to its EDAC line) is found once. The result still names every source that holds it.

A page offline is looked up by its page and by how the page was taken offline.

## The result

| Result | Meaning |
| --- | --- |
| as planned | the planned number was found, and every field a record carries equals the plan |
| not found | no record matches |
| found 2 of 3 | fewer errors on record than were injected |
| decoded differently | a record reports a field as something else than was injected; the lines below the list name the field, both values and the records |
| offline, planned to stay in use | a page with `count: 0` is offline |

Fields of the plan that no matching record carries are listed apart ("not reported"). A platform that does not expose the row of an error cannot decode it wrongly, so this is no mismatch; it is what the observation grade of the records says in other words.

Records beyond the plan are counted and listed, and do not fail the comparison: a host may have errors of its own.

The exit code is 0 when every injection was found as planned and 1 when one was not. It is no grade. `--json` prints the comparison with every record reference.
