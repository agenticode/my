# 6.19.10-ghes-clock-step

Firmware error records on the guest of `6.19.10-ghes` in its next boot, after its clock was set back by 10 seconds while rasdaemon ran. The database of this guest has no machine check records, so nothing in it holds the kernel's time.

| | |
| --- | --- |
| Host | the aarch64 guest of `6.19.10-ghes`, second boot; shown as `lab-arm` |
| Kernel, rasdaemon | 6.19.10-300.fc44.aarch64, 0.8.0 (`rasdaemon-0.8.0-9.fc44.aarch64`) |
| Injected | `tools/lab/ghes.sh clock-step`: chronyd stopped, rasdaemon started with an empty database, `date -s` 10 seconds back (`extra/clock-step.txt`), then 11 firmware error records |
| Captured | 2026-11-15 12:21:56 UTC |

| Injection | Record |
| --- | --- |
| `row-1` to `row-6` | corrected, one row, four columns; two of the addresses twice, 9 and 12 seconds apart |
| `burst` | 4 corrected at one cell within a second |
| `ue-1` | uncorrected recoverable on a page in use; the kernel takes the page offline hard |

What the sources hold:

- `journal-k.json`, `dmesg`: 8 firmware blocks, 11 EDAC lines and 1 `Memory failure` line. The journal dates the first error 12:20:30.
- `var/lib/rasdaemon/ras-mc_event.db`: 11 rows in `mc_event`, the first at 12:20:40. Every row is 10 or 11 seconds ahead of its line in the journal.
- `sys/devices/system/edac/mc/mc0`: the counters 10 and 1.

What it shows:

- Read with rasdaemon's times, an error is more than 5 seconds from its own lines in the kernel log. The tool said "CE 18, UE 2" for 10 and 1 before it compared the clocks. Two of the corrected errors were paired with the line of the next error at the same address, which lies within 5 seconds of them.
- The ledger now compares the two sources. Each of the 11 rasdaemon records has a kernel log line of its error 10 seconds earlier, and no other difference fits them all, so the 11 records are moved by 10 seconds. The `time-align` entry of the source inventory says so, and the copy gives "CE 10, UE 1".
- `my/` was written on the guest, whose journal still held the boot before this one: its "CE 34, UE 3" are the 10 and 1 of this boot and the 24 and 2 of `6.19.10-ghes`.
