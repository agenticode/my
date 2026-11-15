# Exporter

`my serve --metrics` runs MemorY as a Prometheus exporter. It scans the host, serves what the scan found at `/metrics`, and scans again after a pause. It reads the sources `my` reads and changes nothing on the host. The same server answers `/api/dimm/<slot or serial>` with the finding for one DIMM as JSON.

```
$ my serve --metrics
2026-11-15T05:25:59Z listening on http://127.0.0.1:10163
2026-11-15T05:25:59Z scan took 156ms: host r12-n034, finding replace now, 16 DIMMs, 2 with errors or a finding
```

| Flag | Meaning |
| --- | --- |
| `--metrics` | serve `/metrics` |
| `--listen ADDR` | where to listen. The default is `127.0.0.1:10163`; `:10163` listens on every interface |
| `--interval TIME` | pause between the end of one scan and the start of the next, such as `30s` or `5m`. The default is `5m`, the minimum `1s` |
| `--root DIR` | scan a copy of a host's files below `DIR` instead of this host |
| `--fleet KIND`, `--fleet-env FILE`, `--since TIME`, `--host NAME` | read the store of a fleet in place of this host: see "One exporter for a fleet" |
| `--config FILE`, `--set KEY=VALUE` | thresholds of the engine, as for the other commands |
| `--lang en\|ko` | language of the log |

The log goes to standard error. A scan is logged when it found something else than the scan before, and when it failed. SIGINT or SIGTERM stops the server with exit code 0. The exit code is 70 when the server cannot listen on its address.

## Metrics

Every series has a `host` label, the name of the machine the records belong to.

| Metric | Type | Labels | Value |
| --- | --- | --- | --- |
| `my_dimm_verdict` | gauge | `host`, `slot`, `serial` | the finding for the DIMM: 0 ok, 1 observe, 2 consider replacement, 3 replace now, 4 hold replacement. The numbers are the exit codes of the commands |
| `my_dimm_ce_total` | counter | `host`, `slot`, `serial` | corrected errors on record for the DIMM |
| `my_dimm_ue_total` | counter | `host`, `slot`, `serial` | uncorrected errors on record for the DIMM |
| `my_dimm_faults` | gauge | `host`, `slot`, `mode` | faults of the DIMM by mode: `single-bit`, `row`, `multi-row`, `column`, `bank`, `device`, `dimm`, `system-issue`, `undetermined` |
| `my_dimm_offlined_pages` | gauge | `host`, `slot` | pages of the DIMM that were taken offline |
| `my_dimm_repairs_total` | counter | `host`, `slot`, `type` | repairs on record for the DIMM by type: `sppr`, `hppr`, `sparing`, `row-remap`. Attempts that failed count too |
| `my_gpu_remap_banks` | gauge | `host`, `gpu`, `bucket` | memory banks of the GPU by the spare rows they have left for row remapping: `max`, `high`, `partial`, `low`, `none`, from the latest `nvidia-smi` state |
| `my_gpu_remap_failure` | gauge | `host`, `gpu` | 1 when a row remapping of the GPU failed (XID 64 in the kernel log, or the flag in the `nvidia-smi` state), 0 when the GPU reports none |
| `my_source_grade` | gauge | `host`, `source`, `status` | best observation grade of the errors a source gave in the latest scan: 3 A, 2 B, 1 C, 0 no errors. `status` says how reading the source went: `ok`, `partial`, `empty`, `absent`, `unreadable`, `error` |

No metric has an address, a row, a column or a bank as a label. The number of series of a host is bounded by its DIMMs, GPUs and sources. Where in a DIMM a fault sits is what `my dimm`, `my map` and `my why` show.

Which series exist:

- A DIMM that is in the host has `my_dimm_verdict`, both totals and `my_dimm_offlined_pages`, also when nothing is on record about it. A counter that starts at 0 lets `increase()` see the first error.
- A DIMM that was replaced has no series.
- `my_dimm_faults` and `my_dimm_repairs_total` have one series per mode or type that occurs.
- Errors that the records tie to a slot and to no DIMM are counted under an empty `serial`. Errors that name no place are counted under an empty `slot` as well. They belong to no finding.
- A GPU has `my_gpu_remap_failure` when its remapping state is known: from an `nvidia-smi` state or from XID 64. A GPU that only logged other XIDs has none.
- `serial` is empty when the scan could not read the SMBIOS tables, which need root. `gpu` is the serial number of the GPU, or its UUID, PCI address or index when the serial number is unknown.

The totals are counted from error records, as in the commands. They last across a reboot as long as the source that holds the records does: the rasdaemon database does, the journal does until it rotates, and EDAC counters start again at boot. A total goes down when the DIMM is replaced or when the only source of its old records is gone. Prometheus treats that as a counter reset.

## Metrics about the exporter

| Metric | Type | Labels | Value |
| --- | --- | --- | --- |
| `my_exporter_build_info` | gauge | `version`, `revision`, `engine` | 1. `engine` is the version of the analysis engine: it changes when the same records can give another finding |
| `my_exporter_last_scan_timestamp_seconds` | gauge | | when the scan behind the metrics finished, in seconds since the epoch |
| `my_exporter_last_scan_duration_seconds` | gauge | | how long that scan took |
| `my_exporter_scan_failures_total` | counter | | scans that failed since the exporter started |

A scan that fails leaves the metrics of the scan before in place and adds one to `my_exporter_scan_failures_total`. `time() - my_exporter_last_scan_timestamp_seconds` is the age of the metrics. Until the first scan has succeeded `/metrics` answers 503, so the target shows as down.

## DIMM API

`GET /api/dimm/<slot or serial>` answers with the JSON document that `my why <slot or serial> --json` prints on the same host: the finding, the rules that fired, the evidence with the records behind it, the checks, the missing data and the faults. The `slot` and `serial` labels of `my_dimm_verdict` are names it takes, so a dashboard can link a DIMM to its document.

| Request | Answer |
| --- | --- |
| `GET /api/dimm/CPU0_C1` | the DIMM in slot `CPU0_C1` |
| `GET /api/dimm/3B9C56` | the DIMM with that serial number. The last four or more characters of a serial number are enough when they fit one DIMM |
| `GET /api/dimm` | every DIMM with errors or a finding, the worst first. `?all=1` lists every DIMM |

`?lang=en` or `?lang=ko` chooses the language of the sentences. Without it they are in the language of the server (`--lang`). The codes and arguments behind the sentences are in the document too.

```
$ curl -s http://127.0.0.1:10163/api/dimm/CPU0_C1 | head -n 8
{
  "view": "why",
  "version": 1,
  "lang": "en",
  "host": "r12-n034",
  "grade": "replace-now",
  "exit_code": 3,
  "data": [
```

| Status | When |
| --- | --- |
| 200 | the document |
| 400 | the name fits more than one DIMM, or the language is unknown |
| 404 | no slot or DIMM has that name, or the slot is empty |
| 503 | no scan has succeeded yet |

An error is a JSON object with one field, `error`, that holds the sentence the command would print. The API is served with and without `--metrics`.

## One exporter for a fleet

`my serve --metrics --fleet mysql` (or `--fleet postgresql`) reads the store the rasdaemons of a fleet write to, in place of the host it runs on. One exporter then serves the findings of every host that has a row in the store, and the `host` label tells the hosts apart. [compatibility.md](compatibility.md#store-on-a-database-server) describes the store and its settings; `--fleet-env FILE` names a file with them.

The log line of a scan names the store and counts its hosts by finding. This one is from the store the tests use, three hosts on a stand-in for the server:

```
scan took 61ms: postgresql://db.example:5432/rasdaemon?schema=rasdaemon, 3 hosts: 2 replace now, 1 consider replacement
```

- The first scan reads every host. A later scan asks the store which hosts have rows above the row numbers it has seen and reads those hosts. Every twelfth scan lists all hosts again: that finds a host whose rows were removed, and a row the shorter listing did not show.
- A host without new rows is read again when its findings are six hours old. A finding is as of the time it is made: a fault that was rising is quiet a week later. These reads are spread over the scans.
- A host is read into a ledger in the temporary directory, which is removed when the findings are made. Between scans the exporter keeps the metrics of each host.
- `/api/dimm/<slot>?host=NAME` reads the rows of that host when it is asked, so the answer is as of the request. Without `host` the answer is 400 and names the hosts, unless the store holds one host.
- `--host NAME` serves one host of the store. `--since 30d` reads rows from then on, counted back from each scan.
- A host appears with its first record. A store has no SMBIOS tables, so `serial` is empty and a DIMM has series once it has a record.
- Rows without a host name belong to no host and are not read.

When a host cannot be read the scan fails and is counted; the host is read by the next scan and the findings for the other hosts stay.

## How a scan runs

Each scan reads the sources of the host into a new ledger in the temporary directory and removes the ledger of the scan before. The exporter keeps no file between scans or across a restart.

A scan costs what `my` costs on the same host, and `my_exporter_last_scan_duration_seconds` shows it. A scan reads the journal of every boot on record and gives that up to two minutes. On a host with a long journal, choose an `--interval` that leaves room for it.

The exporter reads what its user may read. As root it reads every source that is there. As another user it serves what that user can read, and the sources it could not read have `status="unreadable"` in `my_source_grade`.

## Endpoints

| Path | Answer |
| --- | --- |
| `/metrics` | the metrics, when `--metrics` is given. The body is gzip when the client accepts it |
| `/api/dimm/<slot or serial>`, `/api/dimm` | the finding for one DIMM, or for every DIMM with errors or a finding, as JSON. With `--fleet`: `?host=NAME` |
| `/-/healthy` | 200 while the process runs |
| `/-/ready` | 200 once the first scan has succeeded, 503 before |
| `/` | the list of these paths |

The server answers GET and HEAD only.

## Access

The default address is `127.0.0.1:10163`, which only the host itself can reach. `--listen :10163` or `--listen 10.0.0.5:10163` opens it to the network. The server has no TLS and no authentication. The metrics name slots and DIMM serial numbers, and the API also names the files the records came from.

Port 10163 is not registered in the Prometheus list of default port allocations.

## Alerts, dashboard and services

[contrib/](../contrib/README.md) holds files to take from: systemd units for the exporter of a host and of a fleet, a Prometheus scrape configuration, two alert rules with their rule tests, Alertmanager routes for them, and a Grafana dashboard.

The alert about memory is `MemoryFindingWorse`. It fires when `my_dimm_verdict` of a DIMM is higher than in the hour before, and on nothing else: error counts raise no alert, and a finding that stays as it is raises none. The second rule, `MemoryFindingsStale`, is about the exporter: its findings have not been renewed for 30 minutes.

## Scrape configuration

```yaml
scrape_configs:
  - job_name: memory
    scrape_interval: 1m
    static_configs:
      - targets: ['r12-n034:10163']
```

The values change once per scan at most. A scrape interval shorter than `--interval` reads the same values again.
