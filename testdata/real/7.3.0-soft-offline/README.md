# 7.3.0-soft-offline

Three pages taken offline soft, in the boot after `7.3.0-hwpoison`. A soft offline moves the content of a page elsewhere and retires the page; nothing is lost and no process is signalled.

| | |
| --- | --- |
| Host | the lab host of `7.3.0-baseline`, one reboot later |
| Kernel, rasdaemon | 7.3.0-ras, 0.8.0 (`rasdaemon-0.8.0-9.fc44`) |
| Injected | `tools/lab/vm.sh soft`: `madvise(MADV_SOFT_OFFLINE)` on two pages; 1,023 corrected errors reported for a third page to the corrected error collector of the kernel through debugfs, which is its threshold; 5 reported for a fourth page |
| Captured | 2026-11-15 10:28:32 UTC |

What the sources hold:

- `dmesg`, `journal-k.json`: `Soft offlining pfn ... at process virtual address ...` for the two `madvise` pages and `RAS: Soft-offlining pfn: 0x110b5e` for the page of the collector. The kernel prints the outcome of a soft offline only when it fails. The fourth page stays in use and is not mentioned.
- `var/lib/rasdaemon/ras-mc_event.db`: nothing from this boot. The kernel has no trace event for a soft offline. The four rows of `7.3.0-hwpoison` are still there: the database outlives a reboot, `dmesg` does not.
- `extra/cec-array.txt`: the collector's own table, with 1,028 errors seen, one page offlined and the fourth page at a count of 5.
- `extra/held.jsonl`: the two pages of the collector test before and after. The content of the retired page is intact at another page frame.
- `my/`: no errors, 7 pages offline and one reboot on the timeline, exit code 0.

The plan lists the fourth page with `count: 0`: a page that must not be reported offline.
