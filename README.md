# MemorY

MemorY is a read-only analysis tool for memory errors on Linux servers. It takes the records a host already keeps (the rasdaemon database, the kernel log, EDAC sysfs counters, SMBIOS) and stores them as one event ledger keyed by DIMM serial number, so error history survives reboots and follows a DIMM when it moves to another slot.

The command is `my`. It never offlines pages, triggers repairs or resets counters.

## Status

Early development. This tree holds the data model, the SQLite ledger, the input adapters, a synthetic data generator and the analysis engine, which gives one finding per DIMM. Terminal views, case bundles and the HTML report are not merged yet.

## Layout

| Path | Content |
| --- | --- |
| `cmd/my` | the binary |
| `internal/model` | ledger records; the error event uses the FMFM runtime error log field names |
| `internal/store` | the ledger: one SQLite file, schema migrations, totals, consistency passes |
| `internal/adapter` | readers for the rasdaemon database, the kernel log, EDAC sysfs, SMBIOS and host facts |
| `internal/decode` | EDAC driver detail strings, CPER memory error records, machine check status |
| `internal/ingest` | runs adapters against a ledger and records what each source yielded |
| `internal/engine` | the analysis engine: groups the errors of a DIMM into faults with a mode (single bit, row, multi-row, column, bank, device, DIMM, system issue), measures trend, spread and baselines, and applies 13 rules with fixed IDs to reach a finding per DIMM |
| `internal/i18n` | message templates in English and Korean |
| `internal/synth` | synthetic hosts and error histories for tests and demos |
| `testdata` | fixtures for every adapter |

## Build

Go 1.26 or newer.

```
make build    # bin/my
make test
make fuzz     # each parser fuzz target for 10 s (FUZZTIME=1m for longer)
make cross    # dist/my-linux-amd64, dist/my-linux-arm64, dist/my-darwin-arm64
```

The binary is pure Go and statically linked (`CGO_ENABLED=0`).

## Try the engine

Until the scan and view commands are merged, two development commands run the engine on synthetic data. Both are left out of the command list.

```
$ bin/my synth -scenario demo -ledger demo.db
$ bin/my analyze -ledger demo.db
host r12-n034, as of 2026-11-13 00:00 UTC, engine 1
host finding: replace now

CPU0_C1  S/N 3B9C56  CE 1,231  UE 1  row
  finding   replace now (finding, confidence high, 91)  R-UE-01 R-ROW-03 R-REP-02
  evidence  [R-UE-01] 1 uncorrected error, the latest on 2026-11-12 03:14 UTC at rank0 dev7 bg0 bank3 row 0x1a2f col 0x238
            [R-ROW-03] row fault at rank0 dev7 bg0 bank3 row 0x1a2f: 421 errors in the last 7 days, 2.3 times the daily rate of the 30 days before
            [R-ROW-03] row fault at rank0 dev7 bg0 bank3 row 0x1a2f: 13 new places in the last 30 days; it now covers 9 columns in 2 rows
            [R-ROW-03] errors of the fault at rank0 dev7 bg0 bank3 row 0x1a2f involve 2 data lines of one device
            [R-REP-02] 764 errors at rank0 dev7 bg0 bank3 row 0x1a2f after the hard PPR of 2026-10-28
  checks    open: no other DIMM on this channel: there is nothing to compare with
            open: no slot move or reseat on record: a cause in the board is not ruled out
            supports: the other 15 DIMMs of this host have no errors outside single confined places: a cause in the board is unlikely
  missing   no ECS counters
            no DIMM temperature history
  next      Replace the DIMM. Attach a case bundle of this DIMM to the return.
...
$ echo $?
3
```

The exit code is the grade of the worst finding: 0 ok, 1 observe, 2 consider replacement, 3 replace now, 4 hold replacement. A grade is a finding. The thresholds behind it are defaults that have not been compared with failure analysis results; `my analyze -thresholds` lists them, and `-config file` or `-set key=value` changes them. `my analyze -rules` lists the 13 rules, `-lang ko` prints Korean, `-json` prints the whole result.

## License

Apache-2.0. See [LICENSE](LICENSE).
