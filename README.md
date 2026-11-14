# MemorY

MemorY is a read-only analysis tool for memory errors on Linux servers. It takes the records a host already keeps (the rasdaemon database, the kernel log, EDAC sysfs counters, SMBIOS) and stores them as one event ledger keyed by DIMM serial number, so error history survives reboots and follows a DIMM when it moves to another slot.

The command is `my`. It never offlines pages, triggers repairs or resets counters.

## Status

Early development. This tree holds the data model, the SQLite ledger, the input adapters, a synthetic data generator and the fault classifier. Findings per DIMM, terminal views, case bundles and the HTML report are not merged yet.

## Layout

| Path | Content |
| --- | --- |
| `cmd/my` | the binary |
| `internal/model` | ledger records; the error event uses the FMFM runtime error log field names |
| `internal/store` | the ledger: one SQLite file, schema migrations, totals, consistency passes |
| `internal/adapter` | readers for the rasdaemon database, the kernel log, EDAC sysfs, SMBIOS and host facts |
| `internal/decode` | EDAC driver detail strings, CPER memory error records, machine check status |
| `internal/ingest` | runs adapters against a ledger and records what each source yielded |
| `internal/engine` | groups the errors of a DIMM into faults and gives each fault a mode: single bit, row, multi-row, column, bank, device or DIMM |
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

## License

Apache-2.0. See [LICENSE](LICENSE).
