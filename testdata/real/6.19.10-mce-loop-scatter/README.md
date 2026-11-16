# 6.19.10-mce-loop-scatter

A crash loop with another place every time: a fatal machine check in three boots in a row, at three addresses on three channels. The guest of `6.19.10-mce-loop`, cleaned before the loop: no kept ledger, no archived dumps, no journal of earlier boots.

| | |
| --- | --- |
| Host | `lab-x86`, as in `6.19.10-mce-loop` |
| Kernel | 6.19.10-300.fc44.x86_64, booted with `mce=print_all`; `systemd-pstore`, `rasdaemon` and `my harvest install` enabled |
| Machine | QEMU 10.2.2, q35, `-cpu Skylake-Server`, emulated without hardware virtualization |
| Injected | `loop.sh run loop-scatter.plan`: boot 1 bank 7 `be00000000010090` at `0x0a2b3040`, boot 2 bank 8 `be00000000010091` at `0x1c4d5080`, boot 3 bank 7 `be00000000010092` at `0x2e6f70c0`, each through `mce -b`; `boots.jsonl` holds the boot IDs and the times |
| Captured | 2026-11-16 04:32:40 UTC, in boot 4 |

The files are those of `6.19.10-mce-loop`, and `journal-all.json` holds the kernel messages of the four boots (`journalctl -o json _TRANSPORT=kernel`). `my harvest` ran at every boot of this loop with the build named in `capture.env`; `var/lib/my/ledger.db` is its kept ledger as it was after the fourth run. `console.log` holds the serial console from the restart before boot 1 to boot 4.

Changed for the repository: as in `6.19.10-mce-loop`; the guest's journal names five machine IDs (the installed system's and one of the initrd per boot), replaced by `00000000000040008000000000000001` to `...0005`.

```
my open testdata/real/6.19.10-mce-loop-scatter/var/lib/my/ledger.db boots
```

Three stops, each with an uncorrected error that names no DIMM, at three addresses: no address comes back, so the host finding has no statement of a place over boots.
