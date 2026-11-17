# Demo case of the SPD

A made host of 8 slots, written as a copy of a host's files by the synthetic generator: SMBIOS tables, EDAC counters, a kernel log and a rasdaemon database with findings on three DIMMs (CPU0_B1, CPU0_C1, CPU1_A1). Beside them it holds what `my collect host` writes of the SPD: `spd-probe.txt` and one file `spd/0-00NN.bin` per module.

No server was read. Every SPD file is the public dump `../illumos/ddr5-rdimm-64g-2rx4.bin` (illumos-gate, CDDL-1.0, see [../README.md](../README.md)) replayed with two things written per slot: the serial number of the slot's DIMM and one of two manufacturing weeks, 2022-W43 or 2023-W08. Both lie outside the CRC of the dump, so each file decodes with a good CRC. The serial numbers, the weeks, the findings and the probe are made up.

SMBIOS agrees with the SPD: DDR5 RDIMM, 64 GB, the part number of the dump (MTC40F2046S1RC48BA1), and the maker as its JEDEC code alone (bank 1, 0x2C), so that no maker string stands beside a code it does not fit.

`TestWriteDemo` in `internal/testledger/spdhost` writes the case again (`MY_SPD_DEMO=DIR go test ./internal/testledger/spdhost -run TestWriteDemo`), and `TestDemoIsCurrent` holds the files of this directory to it.

```
my spd --root testdata/spd/demo
my lots --root testdata/spd/demo
my report --root testdata/spd/demo -o demo-spd.html
```
