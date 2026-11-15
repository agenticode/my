# 7.3.0-mce-clock-step

The injections of `7.3.0-mce-print-all` on a host whose clock was set back by 10 seconds after rasdaemon had started.

| | |
| --- | --- |
| Host | the nested guest of `7.3.0-mce` |
| Kernel, rasdaemon | 7.3.0-ras with `mce=print_all`, 0.8.0 (`rasdaemon-0.8.0-9.fc44`) |
| Injected | `tools/lab/mce.sh clock-step`: `date -s` 10 seconds back, then the ten machine checks |
| Captured | 2026-11-15 10:42:21 UTC |

The lab host does this by itself at every boot: its hardware clock runs ahead and chronyd steps the clock back by 7 to 11 seconds a few seconds after rasdaemon started. rasdaemon dates a record by the uptime of its trace event plus the offset between wall clock and uptime that it measured at its start, so its times stay ahead for the rest of its run. The guest has no network time, so the step is made by hand (`extra/clock-step.txt`).

What it shows:

- `mce_record.timestamp` is 9 to 10 seconds ahead of `mce_record.walltime`, the time the kernel gave the same record.
- The kernel log dates the errors by the kernel's clock. Read with rasdaemon's times, each error is more than 5 seconds from its line in the kernel log and is counted twice: "CE 14, UE 6" for 7 and 3.
- `my/` shows 7 and 3: a machine check record is dated by `walltime`, and the rows of `memory_failure_event` move with the record next to them.
