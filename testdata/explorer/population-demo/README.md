# population-demo

The demo case of the population check (docs/rules.md, "Population rules"): an HP ProLiant DL360 Gen9 with two Xeon E5-2680 v4 processors and two 16 GB registered modules, one per processor, in `PROC 1 DIMM 1` and `PROC 2 DIMM 1`. For one module per processor the table of the HPE ProLiant DL360 Gen9 Server User Guide (767927-009, p. 48) fills DIMM 12 first: the board marks `PROC n DIMM 12` with `+` and `PROC n DIMM 1` with `-`, and `my board` and `my report` give one P-ORDER-01 line per processor.

```
my board --root testdata/explorer/population-demo
my report --root testdata/explorer/population-demo -o population-demo.html
```

| | |
| --- | --- |
| Source | [linuxhw/DMI](https://github.com/linuxhw/DMI) (the Linux Hardware Project, https://linux-hardware.org), commit `57b21bc`, `Server/Hewlett-Packard/ProLiant/ProLiant DL360 Gen9/1579B6FEB060` |
| Licence | CC BY 4.0, [`../LICENSES/CC-BY-4.0.txt`](../LICENSES/CC-BY-4.0.txt) |
| `dmidecode.txt` | the dump with the changes listed in [`../README.md`](../README.md#public-dumps): the records of DMI types 1, 2, 4, 16 and 17 alone, and every maker, part number, serial number, asset tag, SKU number and UUID replaced with made-up values |
| `var/log/messages` | empty: the case holds no error record, so that `--root` finds a kernel log to read |
