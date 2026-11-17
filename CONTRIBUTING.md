# Contributing

This page says how to report a problem or send a sample of a BMC log, how to build and test, what a change needs, and how the source tree is laid out.

## Reporting a problem

Open an issue with the command line, what was printed, and the output of `my version`. For a wrong finding, the case itself helps most: `my bundle -o case.mycase --anonymize` writes it, and [docs/bundle-format.md](docs/bundle-format.md) says what the file holds and what `--anonymize` replaces. Look at the file before you attach it to a public issue.

A platform whose records are decoded wrongly, or not at all, is a report worth making even without a fix: the kernel release, the EDAC driver and a few of the original log lines are enough to start.

Security problems do not belong in an issue: see [SECURITY.md](SECURITY.md).

## Sending a sample of a BMC log

The tables that read the memory messages of each BMC vendor rest on registries and documents; none has been compared with a log of a real machine yet ([docs/vendors.md](docs/vendors.md)). A log of a machine with a memory event, of any vendor, is the most useful thing to send: a Redfish collection (`my collect redfish --bmc ADDRESS -o DIR`), the saved output of a vendor tool, or an exported log. Open an issue with the file attached, the vendor, model and firmware version of the BMC, and what happened to the DIMM if it is known (replaced, repaired, still in use). Replace host names, addresses and user names first; `my collect` leaves out passwords and session tokens by itself. The sample becomes a fixture, and the table that reads it moves to the basis `capture`.

## Building and testing

The build needs Go 1.26 or newer. The `toolchain` line of `go.mod` names the Go version of CI and of the release archives; the go command downloads it when the installed one is older. The binary is pure Go and statically linked (`CGO_ENABLED=0`).

| Command | What it does |
| --- | --- |
| `make build` | writes `bin/my` |
| `make test` | `go test ./...` |
| `make lint` | `gofmt` and `go vet` |
| `make test-race` | the tests with the race detector; it needs a C compiler and takes several minutes |
| `make cross` | builds linux/amd64, linux/arm64 and darwin/arm64 into `dist/` |
| `make fuzz` | runs every fuzz target for `FUZZTIME` (10 s by default); [docs/fuzzing.md](docs/fuzzing.md) lists the targets and what they check |
| `make golden` | writes the golden files of the figures again |
| `make examples` | writes the report and the figures under `examples/` again |
| `make dist-check` | builds the release archives twice and compares them |

CI runs `make lint`, `make test`, `make test-race` and `make cross` on every pull request, then runs the Linux binary on its own machine as a normal user and as root. A further job gives every fuzz target 20,000 inputs.

Nothing in the tests needs a server with memory errors. Parsers read fixtures under `testdata/`, and `my synth` makes hosts with a known fault for the engine and the figures. `my synth -list` names the scenarios, `my analyze` prints the engine result of a ledger, and `my eval` runs the comparison of [docs/evaluation.md](docs/evaluation.md).

## What a change needs

- Tests. A parser gets a fixture with the lines or rows it reads; a rule gets a history that fires it and one that does not.
- An error message that says what failed and with which input. No silent fallback: a source that cannot be read is listed with the reason.
- No write outside the output files of the command. MemorY never changes system state: no page offlining, no repair, no counter reset, no setting. A change that needs one of these does not fit the project.
- No network access unless the command line asks for it.
- Both languages. A message has an English and a Korean text in the message table of its package; a new one gets both.
- The pages that describe it. `docs/rules.md`, `docs/data-model.md` and `docs/bundle-format.md` have tests that fail when a rule, a threshold, a ledger table or a manifest field is missing from them, and the screens quoted in the README and in `docs/reading-guide.md` are compared with what the commands print.

After a change of the output, run `make golden` and `make examples` and read the diff: it is the review of what a user will see.

## Sending a change

One topic per pull request. The message of a commit says what changed and why, in plain sentences. `make lint` and `make test` pass before it is sent.

Contributions are accepted under the license of the project, Apache-2.0.

## Writing

Documentation and help texts state what the tool does, with the command, the number or the file that shows it. They carry no praise of the tool and no claim that a test or a measurement does not back.

## Layout

| Path | Content |
| --- | --- |
| `cmd/my` | the binary and its commands |
| `internal/model` | ledger records; the error event uses the field names of the FMFM runtime error log |
| `internal/store` | the ledger: one SQLite file, schema migrations, totals, consistency passes |
| `internal/adapter` | one reader per input: the rasdaemon database and its store on a database server, the kernel log, crash logs (pstore, kdump), the boot error region (BERT), EDAC sysfs, the mcelog daemon, SMBIOS, host facts, the BMC event log, Redfish dumps, saved output of vendor tools (`racadm getsel`, `racadm lclog view`), `nvidia-smi` output, FMFM logs, and the dcbrain dataset |
| `internal/fwevent` | the common events a firmware record of any vendor is read as, and what each becomes in the ledger |
| `internal/fwmap` | the tables that map the messages of each BMC and BIOS vendor to common events, the wording rules, and the classifier that asks them |
| `internal/yamlite` | the small part of YAML the settings profile and the mapping tables are written in |
| `internal/decode` | EDAC driver detail strings, CPER memory error records, machine check status, IPMI memory sensor events, the signs of a virtual machine |
| `internal/scan` | finds the sources of a host, or of a copy of its files, and recognises loose files by their content |
| `internal/ingest` | runs adapters against a ledger and records what each source yielded |
| `internal/engine` | groups the errors of a DIMM into faults, measures trend, spread and baselines, and applies the rules |
| `internal/explorer` | the readers of the Memory Explorer: where a slot is drawn on the board, read from its locator strings, and the memory counters of the kernel |
| `internal/view` | the data behind every figure, built from a ledger and the engine result; also the JSON output |
| `internal/term` | draws the figures as text |
| `internal/report` | the HTML report: one file with inline SVG figures |
| `internal/fmfmlog` | records and findings in the forms of the FMFM framework |
| `internal/eventstream` | every record of a host as a line of JSON in the schema `my-events/1`, for `my export events` |
| `internal/plugin` | the vendor analyzer contract: request, response, the run of the program, the checks |
| `internal/bundle` | case bundles: format, checks, anonymizing |
| `internal/outfile` | writes output files whole or not at all, and never in place of a device |
| `internal/redfish` | the read-only Redfish client and the collection of `my collect redfish` |
| `internal/preflight` | what a reset right after a fatal memory error would leave on a host, and the settings that change it (`my preflight`) |
| `internal/memras` | the profile of BIOS settings that bear on memory errors, and the check of a host's settings against it (`my bios`) |
| `internal/exporter`, `internal/serve` | the Prometheus metrics and the HTTP server of `my serve` |
| `internal/eval` | replays a field dataset through the engine and compares the findings with the failure tickets |
| `internal/injection`, `internal/labcase` | injection plans, and tests over the captures of real hosts |
| `internal/i18n` | message templates in English and Korean |
| `internal/synth` | synthetic hosts and error histories |
| `internal/cli`, `internal/buildinfo` | flag parsing, exit codes, the version stamp |
| `internal/testutil`, `internal/testledger`, `internal/testcapture` | helpers of the tests |
| `testdata` | fixtures for every adapter; `testdata/real` holds captures of real hosts with their injection plans |
| `examples` | the example case with its report and figures, and an example vendor analyzer |
| `contrib` | for the exporter: systemd units, alert rules with their tests, Alertmanager routes, a Grafana dashboard |
| `tools/dist` | builds the release archives, the same bytes for the same commit |
| `tools/lab` | scripts that inject errors on a lab host and capture what it recorded |
| `tools/perf` | makes large synthetic inputs and measures time and peak memory of the commands on them ([docs/performance.md](docs/performance.md)) |
| `tools/screenlen` | counts the lines and characters of the default screens on the lab captures, in English and Korean |
| `tools/validate` | runs the binary against the host it is on ([docs/validation.md](docs/validation.md)) |
| `docs` | the pages listed in the README |
