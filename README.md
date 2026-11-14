# MemorY

MemorY is a read-only analysis tool for memory errors on Linux servers. It takes the records a host already keeps (the rasdaemon database, the kernel log, EDAC sysfs counters, SMBIOS) and stores them as one event ledger keyed by DIMM serial number, so error history survives reboots and follows a DIMM when it moves to another slot.

The command is `my`. It never offlines pages, triggers repairs or resets counters.

## Status

Early development. This tree holds the data model, the SQLite ledger, the input adapters and a synthetic data generator. Fault classification, terminal views, case bundles and the HTML report are not merged yet.

## Build

Go 1.26 or newer.

```
make build    # bin/my
make test
make cross    # dist/my-linux-amd64, dist/my-linux-arm64, dist/my-darwin-arm64
```

The binary is pure Go and statically linked (`CGO_ENABLED=0`).

## License

Apache-2.0. See [LICENSE](LICENSE).
