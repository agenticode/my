# 7.3.0-mce-rasdaemon-1.0

The injections of `7.3.0-mce`, recorded by rasdaemon 1.0.0 where the other captures have the 0.8.0 package of Fedora 44.

| | |
| --- | --- |
| Host | the nested guest of `7.3.0-mce` |
| Kernel | 7.3.0-ras |
| rasdaemon | 1.0.0, built from the release tag `v1.0.0` with its default options, started by hand |
| Injected | `tools/lab/mce.sh rasdaemon-1.0` |
| Captured | 2026-11-15 11:09:09 UTC |

What differs from the database of `7.3.0-mce`:

- 39 tables where the package creates 7, among them the CXL tables.
- `mce_record` has the columns `ppin` and `microcode`, in the middle of the row. Columns are read by name.
- `memory_failure_event` names the page type as the kernel does (`dirty LRU page`). The 0.8.0 package names the neighbouring type.
- `signal_event` holds the three `SIGBUS` the kernel sent to the process that read its poisoned pages. The table is counted and not read.

`my/` shows the same 7 corrected and 3 uncorrected errors.
