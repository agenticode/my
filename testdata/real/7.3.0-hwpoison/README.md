# 7.3.0-hwpoison

Four pages of a test process taken offline hard, in the boot of `7.3.0-baseline`, 11 seconds after that capture.

| | |
| --- | --- |
| Host | the lab host of `7.3.0-baseline` |
| Kernel, rasdaemon | 7.3.0-ras, 0.8.0 (`rasdaemon-0.8.0-9.fc44`) |
| Injected | `tools/lab/vm.sh hwpoison`: `madvise(MADV_HWPOISON)` on two anonymous pages, one locked page and one page of a file that was read |
| Captured | 2026-11-15 10:26:57 UTC |

`MADV_HWPOISON` makes the kernel handle a page as if an uncorrected error had hit it. No error record exists: nothing went through a machine check bank or a firmware table.

What the sources hold:

- `dmesg`, `journal-k.json`: for each page `Injecting memory failure for pfn ...`, then `Memory failure: 0x...: recovery action for dirty LRU page: Recovered`. The page of the file was clean and is reported as `clean LRU page`. The process got `SIGBUS` with `BUS_MCEERR_AR` for the three pages it had written, and `MCE: Killing ...` is logged when it reads them again.
- `var/lib/rasdaemon/ras-mc_event.db`: 4 rows in `memory_failure_event`. Two things in them are off:
  - The times are 11 seconds ahead of the kernel's. chronyd set the clock back by 10.76 seconds after rasdaemon had started, and rasdaemon keeps the offset it measured at its start.
  - The page type reads `dirty unevictable LRU page` where the kernel says `dirty LRU page`. rasdaemon 0.8.0 names the type from a table of its own, indexed by a kernel enum that has since lost entries.
- `extra/meminfo.txt`: `HardwareCorrupted: 16 kB`, four pages.
- `my/`: no errors, 4 pages offline on the timeline, exit code 0. That is what the tool printed when the capture was made. Since the host finding (R-HOST-02) the same records read "observe", exit code 1, with the check that a program asked for the 4 pages.

`formats/` holds the kernel log of this boot in seven more forms: `dmesg -T`, `dmesg --time-format=iso`, `dmesg -r`, a read of `/dev/kmsg`, and `journalctl -k` as `short`, `short-iso-precise` and `short-monotonic`.
