# Test data of the Memory Explorer

`internal/explorer` reads the place of a memory slot from its SMBIOS locator strings, and the memory counters of the kernel. These directories hold what it reads, and under `want/` the answers of the first implementation of the readers, which was written in JavaScript. `TestReference` compares the two. A want file is the call (`fn`, `in`) and the answer (`out`); a value `@file:NAME` of `in` is the text of the file `NAME` of the same directory.

## Public dumps

Each dump below is a changed copy of a file of a public repository. What was changed is the same for all of them:

- Only the header lines and the records of DMI types 1, 2, 4, 16 and 17 (system, board, processor, memory array, memory device) are kept; the records of other types were removed.
- Maker, part number, serial number and asset tag of every memory module, the JEDEC ids of a module (`Module Manufacturer ID`, `Module Product ID` and the like), and the serial numbers, asset tags, part numbers, SKU numbers and UUID of system, board and processors were replaced with made-up values (`Example Memory`, `EXM32G32R2X4`, `EX000001`, `00000000-0000-4000-8000-000000000001`), whatever the field held. Values that name no module (`Not Specified`, `NO DIMM`, `Unknown`) were kept.
- The locator and bank locator strings, sizes, types and speeds are as published.

The `lshw` text of `coreboot-ga-g41m-es2l` kept every node; its host name, serial numbers and UUID were replaced the same way.

The licence of each source is in `LICENSES/`. The files of the Linux-Hardware.org DMI collection are from the Linux Hardware Project (https://linux-hardware.org), licensed under CC BY 4.0 (https://creativecommons.org/licenses/by/4.0/); the coreboot documentation is under CC BY 4.0 as `LICENSES/coreboot-documentation.md` says.

| Directory | System | Source (repository, commit, path) | Licence |
| --- | --- | --- | --- |
| `netbox-dell-dss7500` | Dell Inc. DSS7500 | [Solvik/netbox-agent](https://github.com/Solvik/netbox-agent) `641bb97`, `tests/fixtures/dmidecode/Dell_DSS7500` | Apache-2.0, [`LICENSES/netbox-agent.Apache-2.0.txt`](LICENSES/netbox-agent.Apache-2.0.txt) |
| `netbox-dell-poweredge-m630` | Dell Inc. PowerEdge M630 | [Solvik/netbox-agent](https://github.com/Solvik/netbox-agent) `641bb97`, `tests/fixtures/dmidecode/Dell_PowerEdge_M630` | Apache-2.0, [`LICENSES/netbox-agent.Apache-2.0.txt`](LICENSES/netbox-agent.Apache-2.0.txt) |
| `netbox-hp-bl460c-gen10` | HPE ProLiant BL460c Gen10 | [Solvik/netbox-agent](https://github.com/Solvik/netbox-agent) `641bb97`, `tests/fixtures/dmidecode/HP_BL460c_Gen10` | Apache-2.0, [`LICENSES/netbox-agent.Apache-2.0.txt`](LICENSES/netbox-agent.Apache-2.0.txt) |
| `netbox-hp-bl460c-gen9` | HP ProLiant BL460c Gen9 | [Solvik/netbox-agent](https://github.com/Solvik/netbox-agent) `641bb97`, `tests/fixtures/dmidecode/HP_BL460c_Gen9` | Apache-2.0, [`LICENSES/netbox-agent.Apache-2.0.txt`](LICENSES/netbox-agent.Apache-2.0.txt) |
| `netbox-hp-dl380p-gen8` | HP ProLiant DL380p Gen8 | [Solvik/netbox-agent](https://github.com/Solvik/netbox-agent) `641bb97`, `tests/fixtures/dmidecode/HP_DL380p_Gen8` | Apache-2.0, [`LICENSES/netbox-agent.Apache-2.0.txt`](LICENSES/netbox-agent.Apache-2.0.txt) |
| `netbox-hp-proliant-bl460c-gen10-graphics-exp` | HPE ProLiant BL460c Gen10 Graphics Exp | [Solvik/netbox-agent](https://github.com/Solvik/netbox-agent) `641bb97`, `tests/fixtures/dmidecode/HP_ProLiant_BL460c_Gen10_Graphics_Exp` | Apache-2.0, [`LICENSES/netbox-agent.Apache-2.0.txt`](LICENSES/netbox-agent.Apache-2.0.txt) |
| `netbox-hp-proliant-m710x` | HP ProLiant m710x Server Cartridge | [Solvik/netbox-agent](https://github.com/Solvik/netbox-agent) `641bb97`, `tests/fixtures/dmidecode/HP_ProLiant_m710x` | Apache-2.0, [`LICENSES/netbox-agent.Apache-2.0.txt`](LICENSES/netbox-agent.Apache-2.0.txt) |
| `netbox-hp-sl4540-gen8` | HP ProLiant SL4540 Gen8 | [Solvik/netbox-agent](https://github.com/Solvik/netbox-agent) `641bb97`, `tests/fixtures/dmidecode/HP_SL4540_Gen8` | Apache-2.0, [`LICENSES/netbox-agent.Apache-2.0.txt`](LICENSES/netbox-agent.Apache-2.0.txt) |
| `netbox-qct-x10e-9n` | Quanta Cloud Technology Inc. QuantaMicro X10E-9N | [Solvik/netbox-agent](https://github.com/Solvik/netbox-agent) `641bb97`, `tests/fixtures/dmidecode/QCT_X10E-9N` | Apache-2.0, [`LICENSES/netbox-agent.Apache-2.0.txt`](LICENSES/netbox-agent.Apache-2.0.txt) |
| `netbox-sm-ssg-6028r` | Supermicro SSG-6028R-OSD072P | [Solvik/netbox-agent](https://github.com/Solvik/netbox-agent) `641bb97`, `tests/fixtures/dmidecode/SM_SSG-6028R` | Apache-2.0, [`LICENSES/netbox-agent.Apache-2.0.txt`](LICENSES/netbox-agent.Apache-2.0.txt) |
| `netbox-sm-sys-6018r` | Supermicro SYS-6018R-TDTPR | [Solvik/netbox-agent](https://github.com/Solvik/netbox-agent) `641bb97`, `tests/fixtures/dmidecode/SM_SYS-6018R` | Apache-2.0, [`LICENSES/netbox-agent.Apache-2.0.txt`](LICENSES/netbox-agent.Apache-2.0.txt) |
| `netbox-sys-5039ms-h12trf-os012` | Supermicro SYS-5039MS-H12TRF-OS012 | [Solvik/netbox-agent](https://github.com/Solvik/netbox-agent) `641bb97`, `tests/fixtures/dmidecode/SYS-5039MS-H12TRF-OS012.txt` | Apache-2.0, [`LICENSES/netbox-agent.Apache-2.0.txt`](LICENSES/netbox-agent.Apache-2.0.txt) |
| `netbox-unknown` | Online Labs SR | [Solvik/netbox-agent](https://github.com/Solvik/netbox-agent) `641bb97`, `tests/fixtures/dmidecode/unknown.txt` | Apache-2.0, [`LICENSES/netbox-agent.Apache-2.0.txt`](LICENSES/netbox-agent.Apache-2.0.txt) |
| `linuxhw-system-x3550-m4-7914b2a-05b91b` | IBM System x3550 M4 -[7914B2A]- | [linuxhw/DMI](https://github.com/linuxhw/DMI) `57b21bc`, `Server/IBM/System/System x3550 M4 -[7914B2A]-/05B91B008DEE` | CC BY 4.0, [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt) |
| `linuxhw-birchstream-1f03c9` | Intel Corporation BIRCHSTREAM | [linuxhw/DMI](https://github.com/linuxhw/DMI) `57b21bc`, `Server/Intel/BIRCHSTREAM/BIRCHSTREAM/1F03C9158405` | CC BY 4.0, [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt) |
| `linuxhw-9k-23ba3b` | ; 9k ; 9k | [linuxhw/DMI](https://github.com/linuxhw/DMI) `57b21bc`, `Server/w 9k/Others/Others/23BA3B5E78E1` | CC BY 4.0, [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt) |
| `linuxhw-poweredge-r7625-292f80` | Dell Inc. PowerEdge R7625 | [linuxhw/DMI](https://github.com/linuxhw/DMI) `57b21bc`, `Server/Dell/PowerEdge/PowerEdge R7625/292F80B4F0BE` | CC BY 4.0, [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt) |
| `linuxhw-m70klp2sb-m90598-001-373530` | Intel Corporation M70KLP2SB M90598-001 | [linuxhw/DMI](https://github.com/linuxhw/DMI) `57b21bc`, `Server/Intel/M70KLP2SB/M70KLP2SB M90598-001/3735307E5C03` | CC BY 4.0, [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt) |
| `linuxhw-system-x3550-m5-8869ac1-3e3013` | LENOVO System x3550 M5: -[8869AC1]- | [linuxhw/DMI](https://github.com/linuxhw/DMI) `57b21bc`, `Server/Lenovo/System/System x3550 M5: -[8869AC1]-/3E30136DFAAF` | CC BY 4.0, [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt) |
| `linuxhw-sa5212m4-4250b0` | INSPUR SA5212M4 | [linuxhw/DMI](https://github.com/linuxhw/DMI) `57b21bc`, `Server/INSPUR/SA5212/SA5212M4/4250B01550A6` | CC BY 4.0, [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt) |
| `linuxhw-i620-g20-434362` | Sugon I620-G20 | [linuxhw/DMI](https://github.com/linuxhw/DMI) `57b21bc`, `Server/Sugon/I620/I620-G20/4343624390D9` | CC BY 4.0, [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt) |
| `linuxhw-nf5280m6-48e15d` | Inspur NF5280M6 | [linuxhw/DMI](https://github.com/linuxhw/DMI) `57b21bc`, `Server/Inspur/NF5280/NF5280M6/48E15DC00C22` | CC BY 4.0, [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt) |
| `linuxhw-volcano-59c634` | AMD Corporation Volcano | [linuxhw/DMI](https://github.com/linuxhw/DMI) `57b21bc`, `Server/AMD/Volcano/Volcano/59C634EF9C63` | CC BY 4.0, [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt) |
| `linuxhw-ts100-e7-pi4-e88885` | ASUSTek Computer INC. TS100-E7-PI4 (the text holds its table twice) | [linuxhw/DMI](https://github.com/linuxhw/DMI) `57b21bc`, `Server/ASUSTek Computer/TS100/TS100-E7-PI4/E888852CC4DB` | CC BY 4.0, [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt) |
| `linuxhw-thinksystem-sr650-7x06cto1ww-5d8ab0` | Lenovo ThinkSystem SR650 -[7X06CTO1WW]- | [linuxhw/DMI](https://github.com/linuxhw/DMI) `57b21bc`, `Server/Lenovo/ThinkSystem/ThinkSystem SR650 -[7X06CTO1WW]-/5D8AB08CFEB3` | CC BY 4.0, [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt) |
| `linuxhw-rs720a-e12-rs12-6002d3` | ASUSTeK COMPUTER INC. RS720A-E12-RS12 | [linuxhw/DMI](https://github.com/linuxhw/DMI) `57b21bc`, `Server/ASUSTek Computer/RS720/RS720A-E12-RS12/6002D3D7ADE4` | CC BY 4.0, [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt) |
| `linuxhw-hpe-cray-sc-xd665-64bb2d` | HPE HPE Cray SC XD665 | [linuxhw/DMI](https://github.com/linuxhw/DMI) `57b21bc`, `Server/HPE/Cray/Cray SC XD665/64BB2DE4318F` | CC BY 4.0, [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt) |
| `linuxhw-poweredge-r6615-71a1f0` | Dell Inc. PowerEdge R6615 | [linuxhw/DMI](https://github.com/linuxhw/DMI) `57b21bc`, `Server/Dell/PowerEdge/PowerEdge R6615/71A1F0F8BB87` | CC BY 4.0, [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt) |
| `linuxhw-g482-z54-00-7ca16e` | GIGABYTE G482-Z54-00 | [linuxhw/DMI](https://github.com/linuxhw/DMI) `57b21bc`, `Server/Gigabyte Technology/G482/G482-Z54-00/7CA16E0B16D7` | CC BY 4.0, [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt) |
| `linuxhw-rd450x-8535af` | Lenovo RD450X | [linuxhw/DMI](https://github.com/linuxhw/DMI) `57b21bc`, `Server/Lenovo/70FRR156CN/70FRR156CN RD450X/8535AF5D51BA` | CC BY 4.0, [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt) |
| `linuxhw-proliant-dl380-gen11-89bbba` | HPE ProLiant DL380 Gen11 | [linuxhw/DMI](https://github.com/linuxhw/DMI) `57b21bc`, `Server/HPE/ProLiant/ProLiant DL380 Gen11/89BBBA931D2E` | CC BY 4.0, [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt) |
| `linuxhw-sys-620u-tnr-8efbd8` | Supermicro SYS-620U-TNR | [linuxhw/DMI](https://github.com/linuxhw/DMI) `57b21bc`, `Server/Supermicro/SYS-620/SYS-620U-TNR/8EFBD85B63DE` | CC BY 4.0, [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt) |
| `linuxhw-b7109f77dv14hr-2t-n-91971d` | TYAN B7109F77DV14HR-2T-N | [linuxhw/DMI](https://github.com/linuxhw/DMI) `57b21bc`, `Server/TYAN Computer/B7109/B7109F77DV14HR-2T-N/91971D0E7746` | CC BY 4.0, [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt) |
| `linuxhw-s2600gz-925442` | Intel Corporation S2600GZ | [linuxhw/DMI](https://github.com/linuxhw/DMI) `57b21bc`, `Server/Intel/S2600/S2600GZ/9254420E4A2C` | CC BY 4.0, [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt) |
| `linuxhw-2288h-v5-94600f` | Huawei 2288H V5 | [linuxhw/DMI](https://github.com/linuxhw/DMI) `57b21bc`, `Server/Huawei/2288H/2288H V5/94600F3C6456` | CC BY 4.0, [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt) |
| `linuxhw-genoad12m3-2q-h-af39b8` | To Be Filled By O.E.M. GENOAD12M3-2Q/H | [linuxhw/DMI](https://github.com/linuxhw/DMI) `57b21bc`, `Server/ASRockRack/GENOAD12/GENOAD12M3-2Q-H/AF39B86BBAAF` | CC BY 4.0, [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt) |
| `linuxhw-1288h-v5-c98b42` | Huawei 1288H V5 | [linuxhw/DMI](https://github.com/linuxhw/DMI) `57b21bc`, `Server/HUAWEI/1288H/1288H V5/C98B42F4F0E0` | CC BY 4.0, [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt) |
| `linuxhw-m50cyp2sbstd-d50fc4` | Intel Corporation M50CYP2SBSTD | [linuxhw/DMI](https://github.com/linuxhw/DMI) `57b21bc`, `Server/Intel/M50/M50CYP2SBSTD/D50FC48A745B` | CC BY 4.0, [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt) |
| `linuxhw-thinksystem-sr645-d77032` | Lenovo ThinkSystem SR645 | [linuxhw/DMI](https://github.com/linuxhw/DMI) `57b21bc`, `Server/Lenovo/ThinkSystem/ThinkSystem SR645/D770320101E1` | CC BY 4.0, [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt) |
| `linuxhw-e3c252d4u-2t-e62cd4` | To Be Filled By O.E.M. E3C252D4U-2T | [linuxhw/DMI](https://github.com/linuxhw/DMI) `57b21bc`, `Server/ASRockRack/E3/E3C252D4U-2T-OVH/E62CD49F7BF3` | CC BY 4.0, [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt) |
| `linuxhw-r1g3d32-ec54ac` | INFERIT R1G3D32 | [linuxhw/DMI](https://github.com/linuxhw/DMI) `57b21bc`, `Server/INFERIT/R1/R1G3D32/EC54AC1F686F` | CC BY 4.0, [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt) |
| `linuxhw-rh2288h-v3-f74f17` | Huawei RH2288H V3 | [linuxhw/DMI](https://github.com/linuxhw/DMI) `57b21bc`, `Server/HUAWEI/RH2288H/RH2288H V3/F74F17899C13` | CC BY 4.0, [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt) |
| `linuxhw-sys-6028r-wtr-fac06e` | Supermicro SYS-6028R-WTR | [linuxhw/DMI](https://github.com/linuxhw/DMI) `57b21bc`, `Server/Supermicro/SYS-6028/SYS-6028R-WTR/FAC06E774FCC` | CC BY 4.0, [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt) |
| `uroot-supermicro-x9dbl` | Thomas-Krenn.AG X9DBL-3F/X9DBL-iF | [u-root/u-root](https://github.com/u-root/u-root) `beb9a36`, `cmds/exp/dmidecode/testdata/SuperMicro-X9DBL.orig.txt` | BSD-3-Clause, [`LICENSES/u-root.BSD-3-Clause.txt`](LICENSES/u-root.BSD-3-Clause.txt) |
| `uroot-synology-rs3614xsp` | Synology RS3614xs+ | [u-root/u-root](https://github.com/u-root/u-root) `beb9a36`, `cmds/exp/dmidecode/testdata/Synology-RS3614xsp.orig.txt` | BSD-3-Clause, [`LICENSES/u-root.BSD-3-Clause.txt`](LICENSES/u-root.BSD-3-Clause.txt) |
| `dselans-dell-centos65` | Dell Inc. PowerEdge R510 | [dselans/dmidecode](https://github.com/dselans/dmidecode) `65c3f9d`, `test_data/centos_6.5_64bit_dell.txt` | MIT, [`LICENSES/dselans-dmidecode.MIT.txt`](LICENSES/dselans-dmidecode.MIT.txt) |
| `ironlib-dmidecode` | Packet c3.small.x86 | [metal-toolbox/ironlib](https://github.com/metal-toolbox/ironlib) `69ba553`, `fixtures/asrr/e3c246d4i-nl/dmidecode` | Apache-2.0, [`LICENSES/ironlib.Apache-2.0.txt`](LICENSES/ironlib.Apache-2.0.txt) |
| `ironic-dmi-data` |  | [openstack/ironic-python-agent](https://github.com/openstack/ironic-python-agent) `b148c55`, `ironic_python_agent/tests/unit/dmi_inspector_data.py (the text DMI_DATA)` | Apache-2.0, [`LICENSES/ironic-python-agent.Apache-2.0.txt`](LICENSES/ironic-python-agent.Apache-2.0.txt) |
| `coreboot-ga-g41m-es2l` | Gigabyte GA-G41M-ES2L desktop board | [coreboot/coreboot](https://github.com/coreboot/coreboot) `f13a6c7`, `Documentation/mainboard/gigabyte/ga-g41m-es2l_lshw_before_coreboot.txt` | CC BY 4.0, [`LICENSES/coreboot-documentation.md`](LICENSES/coreboot-documentation.md) |

## Text written after the formats

The directories `made-*` hold text written by hand after the output formats of numactl, numastat, sysstat and the kernel (`/proc/meminfo`, `/proc/vmstat`, `/proc/slabinfo`, `/proc/pressure/memory`, `/sys/kernel/mm/lru_gen/enabled`), and lines in the form the reference makes of the cache files of sysfs (`cpu-cache.txt`). No host printed them; the numbers are made up.

| Directory | What it shows |
| --- | --- |
| `made-two-nodes` | two NUMA nodes and two CPU packages, memory counters, slab caches, `sar -B` with a 24-hour clock, MGLRU on |
| `made-eight-nodes` | eight nodes; `numastat` prints them in two blocks of four |
| `made-sar-12h` | `sar -B` with a 12-hour clock and a second head line |
| `made-old-kernel` | no `MemAvailable`, no pressure file, slab text with its head line only, MGLRU off |

## Lab guests

The directories `lab-*` hold what x86 guests of the lab printed (`tools/lab/boards.sh`, [docs/validation-lab.md](../../docs/validation-lab.md#boards)): QEMU 10.2.2 without hardware virtualization, the Fedora 44 cloud image, and SMBIOS memory devices that `tools/lab/smbios17.py` wrote with slot names no public dump above has. `dmidecode.txt` is the whole `dmidecode` output of the guest; the other files are what `my collect host` copied and ran there. `tools/lab/sanitize.py` replaced the host's addresses and IDs; the serial numbers `LABB0001` and up are made up. The full captures are under `testdata/real/6.19.10-board-*`.

| Directory | Guest | Slots |
| --- | --- | --- |
| `lab-two-sockets` | 2 sockets of 2 cores, 2 NUMA nodes of 32 GiB | `CPU0_CH0_DIMM0` to `CPU1_CH1_DIMM1`, the `DIMM0` slots populated |
| `lab-digits` | 1 socket, 2 CPUs, 32 GiB | `DIMM000` and `DIMM010` populated, `DIMM001` and `DIMM011` empty |
| `lab-letters` | 1 socket, 2 CPUs, 32 GiB | `DIMMA1` and `DIMMB1` populated, `DIMMA2` and `DIMMB2` empty |

## The population

`population/slots.tsv` is derived from the server dumps of the Linux-Hardware.org DMI collection (github.com/linuxhw/DMI, commit 57b21bc, CC BY 4.0): 2,044 dumps, of which 2,043 describe memory devices. A list is a dump as the ledger keeps it: the system maker and product, the board, the processor records (count, the name and CPUID signature of the processor, which sockets are populated), and every memory device once with whether it holds a module, its memory type, its locator and bank locator. It holds each list once (1,711 lists, 25,605 slots) with the number of dumps that have it; `population/dumps.tsv` names the list of each dump by the id of the collection. Per slot it holds the socket and channel the first implementation of the readers gives it, and the place the board gives it: state and reason, socket, channel, the vendor's channel name, position, the DIMM index of the bank locator, and how the place was found. It holds no module maker, part number or serial number. `TestPopulationPlacement` writes each list as the board writes the slots of a ledger and reads it back; `TestPopulationBoards` and `TestPopulationByMaker` count how the boards are drawn, without and with the fixes of the readers.
