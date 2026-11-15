# 7.3.0-mce-print-all

The injections of `7.3.0-mce` with the kernel booted with `mce=print_all`, which makes it print every machine check record it logs. The kernel log then holds each error in the form a fatal machine check has on the console, and the same error is in the rasdaemon database: two reports of one error.

| | |
| --- | --- |
| Host | the nested guest of `7.3.0-mce` |
| Kernel, rasdaemon | 7.3.0-ras with `mce=print_all`, 0.8.0 (`rasdaemon-0.8.0-9.fc44`) |
| Injected | `tools/lab/mce.sh print-all` |
| Captured | 2026-11-15 10:39:49 UTC |

What the kernel log adds to `7.3.0-mce`, per error:

```
mce: [Hardware Error]: CPU 0: Machine Check: 0 Bank 7: 9c00004000010090
mce: [Hardware Error]: TSC 0 ADDR ADDRESS MISC 86 
mce: [Hardware Error]: PROCESSOR 0:50654 TIME SECONDS SOCKET 0 APIC 0 microcode 1
```

The two exceptions read `Machine Check Exception: 5 Bank 7: ...` and `Machine Check Exception: 7 Bank 1: ...`, the latter with a `RIP 33:<...>` line.

`my/` shows 7 corrected and 3 uncorrected errors: each record of the kernel log is recognised as the second report of a row of `mce_record`.
