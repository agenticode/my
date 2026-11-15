# 7.3.0-mce

Ten machine checks about memory in a nested guest: 7 corrected and 3 uncorrected errors, with the kernel booted the way it boots by default.

| | |
| --- | --- |
| Host | guest of the lab host: QEMU 10.2.2 without hardware virtualization, CPU model Skylake-Server (family 6 model 85, ten machine check banks), 2 CPUs, 4 GB, started with virtme-ng on a copy-on-write view of the lab host's file system; shown as `lab-guest` |
| Kernel, rasdaemon | 7.3.0-ras, 0.8.0 (`rasdaemon-0.8.0-9.fc44`) started by hand with a database of its own |
| Injected | `tools/lab/mce.sh`: the `mce-inject` module hands the kernel machine check records as if it had read them from the banks |
| Captured | 2026-11-15 10:37:06 UTC |

The lab host has no machine check banks, so the records are injected one level down. Addresses are those of pages a waiting process of the guest holds.

| Injection | Record | What the kernel did |
| --- | --- | --- |
| `ce-1`, `ce-2` | 1 corrected read error each, bank 7, channel 0 and channel 1 | logged |
| `ce-3` | 2 corrected read errors on one page, bank 8 | page taken offline soft at the second: the threshold of the corrected error collector is 2 on Intel processors |
| `ce-4` | 3 corrected read errors on one page, bank 7 | page taken offline soft at the second |
| `ue-1` | uncorrected, found by the scrubber, no action required (UCNA) | page taken offline hard |
| `ue-2` | uncorrected, found by the scrubber, action optional (SRAO), as an exception | page taken offline hard |
| `ue-3` | uncorrected on a data load, action required (SRAR), bank 1, as an exception in user context | page taken offline hard |

What the sources hold:

- `var/lib/rasdaemon/ras-mc_event.db`: 10 rows in `mce_record` with rasdaemon's decoding for this CPU model (`MEMORY CONTROLLER RD_CHANNEL0_ERR Transaction: Memory read error`, `M2M`), and 3 rows in `memory_failure_event`. The status register is stored as a signed number. This build of rasdaemon has no `ppin`, `microcode`, `ipid` or `synd` column.
- `dmesg`: no machine check record. With `CONFIG_X86_MCELOG_LEGACY` a logged record counts as handled and is not printed; the log says `mce: [Hardware Error]: Machine check events logged` for each. It does hold the two `RAS: Soft-offlining pfn` lines, the three `Memory failure: ... recovery action` lines, `CPU0 BANK7 CMCI storm detected`, and `mce: Uncorrected hardware memory error in user-access at ...` for `ue-3`.
- No journal: the guest runs without systemd.
- `sys/devices/system/edac`: no memory controller. QEMU emulates no memory controller of a Xeon, so `skx_edac` has nothing to bind to. The errors therefore carry an address and a channel and no DIMM.
- `dmidecode.txt`: one memory device, `DIMM 0`, 4 GiB, manufacturer QEMU.
- `my/`: "CE 7, UE 3, observation grade B", 10 errors tied to no DIMM, host finding ok, exit code 0. That is what the tool printed when the capture was made. Since the host finding (R-HOST-01, R-HOST-02) the same records read "hold replacement", exit code 4.
