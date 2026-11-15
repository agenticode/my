# contrib

Files for running `my serve --metrics` in a monitoring setup. They are examples to take parts from. [docs/exporter.md](../docs/exporter.md) describes the exporter and its metrics.

| File | What it is |
| --- | --- |
| `systemd/my-exporter.service` | the exporter for the host it runs on, as a service that can only read |
| `systemd/my-fleet-exporter.service` | one exporter for a fleet: it reads the store the rasdaemons of all hosts write to |
| `systemd/rasdaemon.env` | the settings of that store, in the variables rasdaemon takes |
| `prometheus/scrape.yml` | scrape configuration for both setups |
| `prometheus/my-findings.rules.yml` | the alert `MemoryFindingWorse`: the finding for a DIMM got worse |
| `prometheus/my-exporter.rules.yml` | the alert `MemoryFindingsStale`: an exporter has stopped renewing its findings |
| `prometheus/*.rules.test.yml` | tests of the two rule files for `promtool test rules` |
| `alertmanager/alertmanager.yml` | routes for the two alerts |
| `grafana/my-memory-findings.json` | a dashboard: DIMMs with a finding, the finding over time, errors, faults, repairs, GPU row remapping, sources |

## Alerts

`MemoryFindingWorse` fires when `my_dimm_verdict` of a DIMM is higher than it was in the hour before: ok to observe, observe to consider replacement, and so on. It does not fire on error counts and it does not fire while a finding stays as it is. A DIMM that shows up with a finding counts as worse than before, so the alert also fires for the findings that exist when the exporter is first set up.

The alert carries the new finding as the label `finding` (`observe`, `consider-replacement`, `replace-now`, `hold-replacement`) and a `severity` (`info`, `warning`, `critical`). It lasts ten minutes and then ends, while the finding stays. The Alertmanager example therefore sends no "resolved" message for it, and sends `replace-now` and `hold-replacement` to a receiver of their own.

`MemoryFindingsStale` is about the exporter: its last scan finished more than 30 minutes ago. Without it, no alert from a host could mean a healthy host or an exporter that reads nothing.

```
promtool check rules prometheus/my-findings.rules.yml prometheus/my-exporter.rules.yml
cd prometheus && promtool test rules my-findings.rules.test.yml my-exporter.rules.test.yml
amtool check-config alertmanager/alertmanager.yml
```

## Dashboard

Import `grafana/my-memory-findings.json` and pick the Prometheus data source. The variable "Host" filters every panel. "Exporter address" is the address the slot links in the table "DIMMs with a finding" open: `<address>/api/dimm/<slot>?host=<host>` is the document `my why <slot> --json` prints.

A finding is always written out next to its colour: ok, observe, consider replacement, replace now, hold replacement.

## Services

`my-exporter.service` runs the exporter as a user that exists only while the service runs, with two capabilities: reading files whatever their mode, and reading the kernel log. With those it reads what root reads. The file system is read-only for it.

`my-fleet-exporter.service` needs no capability: it talks to the database server. Put the settings of the store into `/etc/my/rasdaemon.env` (mode 0600). An account that may only `SELECT` is enough.

Both listen on `127.0.0.1:10163`. To let a Prometheus server on another machine scrape them, change `--listen` in the unit. The server has no TLS and no authentication.

These files are checked in CI with the programs they are for (`.github/scripts/contrib-check.sh`): promtool and amtool, systemd on the CI machine, and a Prometheus and a Grafana server that load the rules and the dashboard and run its queries against the exporter.
