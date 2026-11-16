# 7.3.0-cxl-repair

A memory repair on CXL memory devices, as the kernel and rasdaemon record it: errors in one row of each of two devices, a soft post package repair (PPR) of that row through the kernel's EDAC interface, then errors again in the repaired row of one device while the other stays quiet.

| | |
| --- | --- |
| Host | the nested guest of `7.3.0-cxl` with `-machine cxl=on`: one CXL host bridge with two root ports and two type 3 devices with 256 MB of volatile memory each, emulated by QEMU 11.1.3 built from the release archive (signature checked); QEMU 10.2.2, which made `7.3.0-cxl`, has no repair emulation |
| Devices | `mem1`, serial number `0x4c41420011`; `mem0`, serial number `0x4c41420012` |
| Kernel | 7.3.0-ras with `CONFIG_CXL_EDAC_MEM_REPAIR`: five repair controls per device under `/sys/bus/edac/devices/cxl_memN/` (cacheline, row, bank and rank sparing, and PPR as `mem_repair4`) |
| rasdaemon | 1.0.0 built from the release tag |
| Injected | `tools/lab/cxl-repair.sh`: `tools/lab/cxl-repair.py` adds the error records to the devices through QMP; the script makes the repairs inside the guest |
| Captured | 2026-11-16 23:34:34 UTC, with `my` d7515eb |

| Step | Records |
| --- | --- |
| `quiet-before-1` to `-6` | 6 DRAM event records of `mem1`, corrected, rank 0, bank group 1, bank 2, row `0x1f40`, on three columns; each asks for maintenance, a soft PPR |
| `again-before-1` to `-6` | 6 such records of `mem0`: rank 1, bank group 3, bank 0, row `0xa7c`, three columns |
| repair of `mem1` | soft PPR at the device address `0x1002000`, the first error of its row, 23:34:14 UTC (`extra/cxlr-repairs.jsonl`) |
| repair of `mem0` | soft PPR at `0x1001000`, 23:34:16 UTC |
| `again-after-1` to `-3` | 3 DRAM event records in the repaired row of `mem0`; `mem1` gets none |

The plan lists the 15 error records. The repairs are no injection: the guest asked for them, and the devices reported them.

What the sources hold:

- `var/lib/rasdaemon/ras-mc_event.db`: 15 rows in `cxl_dram_event`, and 2 in `cxl_memory_sparing_event`, one per repair: soft, result 0, 2 spare resources left, with channel, rank, nibble mask, bank group, bank, row and column. QEMU writes the record of a PPR with the maintenance class of sparing and the subclass of cacheline sparing (`cxl_perform_ppr()` in `hw/cxl/cxl-mailbox-utils.c`).
- `dmesg`: nothing per record and nothing about the repairs.
- `extra/cxlr-edac-before.txt`: the repair controls of both devices before the repairs. The devices are not mapped into a region, so the kernel lets a repair through without a matching error record of the boot (`cxl_mem_perform_ppr()` in `drivers/cxl/core/edac.c`).
- `my/`: `cxl:mem0` "consider replacement" by R-REP-02 (3 errors in the row after its repair), `cxl:mem1` "observe" by R-SOFT-01 with the repair line "6 errors before, 0 after". Before d7515eb the tool took each record for sparing that used the last spare and graded both devices "replace now" by R-REP-01.

The guest sees the files of the lab host. Its own scan in `my/` also read the crash dumps of the lab host and holds a page taken offline in one of them, from 2026-10-27.

The cells of the devices are emulated: a repair changes no data in QEMU, and the errors after it are records put in from outside. The capture spans one boot of two minutes, so a quiet row cannot reach the 7 days R-QUIET-01 asks for.
