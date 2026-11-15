# 7.3.0-cxl

Event records of a CXL memory device: 11 corrected errors and 1 uncorrected error, a health record that asks for replacement, and two entries of the poison list.

| | |
| --- | --- |
| Host | the nested guest of `7.3.0-mce` with `-machine cxl=on`: a CXL host bridge, a root port and a type 3 device with 256 MB of volatile memory and the serial number `0x4c41420001`, all emulated by QEMU 10.2.2 |
| Kernel | 7.3.0-ras, CXL drivers built in |
| rasdaemon | 1.0.0 built from the release tag: the 0.8.0 package of Fedora 44 has no CXL tables |
| Injected | `tools/lab/cxl.sh`: `tools/lab/cxl.py` adds the records to the device through QMP, as its firmware would |
| Captured | 2026-11-15 11:06:41 UTC |

| Injection | Record |
| --- | --- |
| `dram-1` to `dram-8` | DRAM event records, corrected, rank 1, bank group 2, bank 1, row `0x2b3c`, on five columns; two of them found by the scrubber |
| `dram-ue` | a DRAM event record, uncorrectable, in the same row |
| `media-1` to `media-3` | general media event records, corrected, channel 1, rank 0, without a DRAM location |
| `module` | a memory module event record: maintenance needed, hardware replacement needed |
| `poison-1`, `poison-2` | two ranges of the poison list, read out through `trigger_poison_list` |

What the sources hold:

- `var/lib/rasdaemon/ras-mc_event.db`: 9 rows in `cxl_dram_event`, 3 in `cxl_general_media_event`, 1 in `cxl_memory_module_event`, 2 in `cxl_poison_event`. The device is not mapped into a region, so `hpa` is all ones: the records carry the address inside the device (`dpa`) and no address of the host.
- `dmesg`: nothing per record. The kernel traces CXL events and does not print them. One `WARNING` from `cxl_event_trace_record` is in the log.
- `extra/cxl-sysfs.txt`: the CXL bus as the kernel shows it, and the EDAC device `cxl_mem0` the kernel registers for the scrub controls of the device.
- `my/`: the device is the slot `cxl:mem0` with its serial number as identity. "CE 11, UE 1", a row fault, finding "replace now" by the uncorrected error and by the device's own request.

The 3 general media records follow the last DRAM records within seconds. Before the capture they were counted as second reports of those: "CE 8".
