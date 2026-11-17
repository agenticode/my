# SPD test dumps

`internal/decode/spd` reads these files. `TestPublicDumps` holds each dump to the values a second decoder gave for it, `TestInputForms` reads the hex texts back to the bytes of their dump, `TestDecodeDimmsText` reads the decode-dimms output, and `TestTestdataHashes` checks every file against the SHA-256 in the tables below.

Each dump is a copy of a file of a public repository. Two kinds of bytes were changed, both outside every CRC of the layout:

- the serial number of the dumps that were read from real modules (DDR4 bytes 325-328, DDR5 bytes 517-520, DDR3 bytes 122-125) is a made-up value `4D5900nn`;
- the DRAM maker code of the two DDR5 UDIMM dumps (bytes 552-553) is `00 00`.

Part numbers are as published: they tie a dump to the datasheet of its module. The two illumos hex files are verbatim copies; their serial numbers were made up by the source, as their head lines say.

The synthetic dumps of the tests (`TestSynthetic`) are no files: the tests build them from these copies by changing named bytes and writing the CRC again. No second decoder saw them.

## Dumps

| File | Module | Source: repository, commit, path | Licence | Bytes changed | SHA-256 |
| --- | --- | --- | --- | --- | --- |
| `illumos/ddr4-rdimm-64g-2rx4.bin` | DDR4 RDIMM 64 GiB 2Rx4 3200 | [illumos/illumos-gate](https://github.com/illumos/illumos-gate) `4a58223`, `usr/src/test/util-tests/tests/libjedec/hex2spd/data/ddr4/36ASF8G72PZ-3G2E1.spd` (hex text) | CDDL-1.0 | serial, bytes 325-328 | `03dee8648eac8e010d5b75627f648ca44056e781a101df29d7d67c2938117c51` |
| `illumos/ddr4-lrdimm-128g-3ds.bin` | DDR4 LRDIMM 128 GiB 2Rx4 2666, 3DS, 4 dies | illumos-gate `4a58223`, `.../data/ddr4/M386AAK40B40-CWD70.spd` (hex text) | CDDL-1.0 | none | `90821a684a2599cd147e8dfe91aab5c509cd6ae24f30601365414e93026f8610` |
| `illumos/ddr4-udimm-32g-2rx8.bin` | DDR4 UDIMM 32 GiB 2Rx8 3200 | illumos-gate `4a58223`, `.../data/ddr4/AQD-D4U32N32-SBW.spd` (hex text) | CDDL-1.0 | none | `030a8b263b65403642dd9a2dfaa698ecfd99caf3e32576febeabc24aef0d02b2` |
| `illumos/ddr4-sodimm-16g-2rx8.bin` | DDR4 SODIMM 16 GiB 2Rx8 3200 | illumos-gate `4a58223`, `.../data/ddr4/AQD-SD4U16GN32-SE1.spd` (hex text) | CDDL-1.0 | none | `5fd7f3bd03882964d46a0b37658270c8d38619cd7260dd4e5330b74ae4c879fb` |
| `illumos/ddr5-rdimm-64g-2rx4.bin` | DDR5 RDIMM 64 GiB 2Rx4 4800 | illumos-gate `4a58223`, `.../data/ddr5/MTC40F2046S1RC48BA1.spd` (hex text) | CDDL-1.0 | serial, bytes 517-520 | `9ed6807ad293789cff99e1d2718f32a65c98e616552ac443abd3a5b417b6b6b5` |
| `illumos/ddr5-rdimm-16g-1rx8.bin` | DDR5 RDIMM 16 GiB 1Rx8 4800 | illumos-gate `4a58223`, `.../data/ddr5/AQD-D5V16GR48-SB.spd` (hex text) | CDDL-1.0 | none | `2db96693971066882e762dc6ca1966ee862f64d8607f6b24d43b505afce07bdf` |
| `illumos/M386AAK40B40-CWD70.spd` | the source of `ddr4-lrdimm-128g-3ds.bin` | illumos-gate `4a58223`, `.../data/ddr4/M386AAK40B40-CWD70.spd` | CDDL-1.0 | none (verbatim) | `11d2beac603a6f7b89c179da958c0afdd716d4b690058819ca2d6719e04fea96` |
| `illumos/AQD-D5V16GR48-SB.spd` | the source of `ddr5-rdimm-16g-1rx8.bin` | illumos-gate `4a58223`, `.../data/ddr5/AQD-D5V16GR48-SB.spd` | CDDL-1.0 | none (verbatim) | `439ba5837fc7c57e2ea2604515f109c49e67820fcc6063b67140b91512090107` |
| `ubihazard/ddr5-udimm-16g-1rx8-a.bin` | DDR5 UDIMM 16 GiB 1Rx8, first module of a kit of two | [ubihazard/ddr5-spd-recovery](https://github.com/ubihazard/ddr5-spd-recovery) `47baa29`, `dumps/teamgroup/t-create-expert_6000_38-38-38-78_1.25_1x8_16x2_[ctced532g6000hc38adc01]/ud5-6000_0104eef6.spd` (binary) | MIT | serial, bytes 517-520; DRAM maker code, bytes 552-553 | `992b8e6eff935b06b434d240745ddb7889ef4f16a10b9d9da286b8907a045867` |
| `ubihazard/ddr5-udimm-16g-1rx8-b.bin` | the second module of the kit | ubihazard/ddr5-spd-recovery `47baa29`, `.../ud5-6000_0104eeff.spd` (binary) | MIT | serial, bytes 517-520; DRAM maker code, bytes 552-553 | `9a247661d1d277eef7d6ec7784e86ad83e0c657e69601359f0e65a42186abea7` |
| `rigred/ddr3-sodimm.bin` | DDR3 SODIMM, for a memory type that is not decoded | [rigred/SPD](https://github.com/rigred/SPD) `be3646f`, `dumps/KINGSTON-KVR16LS11S6-2-001-A00LF.SPD` (binary) | MIT | serial, bytes 122-125 | `f7dd76dff1bb068ff56f302366dd83db3ed588d37dd376341c6289a5bd76e7bd` |

`.../` stands for `usr/src/test/util-tests/tests/libjedec/hex2spd/data/` in the illumos rows. The full illumos commit is `4a5822399e3fef44493aeafd9551614e8530b2e8`, the ubihazard commit `47baa293f3274b19133ca7c8bc8788c802c0937a`, the rigred commit `be3646ff8de8e1cddf7832e8c864d6b2d66c09aa`.

## Texts made from the dumps

The hex texts were printed from `illumos/ddr4-rdimm-64g-2rx4.bin` and `illumos/ddr5-rdimm-64g-2rx4.bin` on 2026-11-17: `hexdump -C`, `hexdump` and `od -A x -t x1` of util-linux 2.41.6 and coreutils 9.10 on Fedora 44, `xxd` of 2025-08-24 on macOS. `decode-dimms-4.4.txt` is what `decode-dimms -x` of i2c-tools 4.4 (Fedora 44 package `i2c-tools-perl-4.4-4.fc44`) printed for the `hexdump -C` text of the four DDR4 copies. They hold the data of the CDDL dumps and stand under the same licence.

| File | Made with | SHA-256 |
| --- | --- | --- |
| `illumos/forms/ddr4-rdimm-64g-2rx4.hexdump-C.txt` | `hexdump -C` | `aba50f13831127986840060ab399df88fd0dde17a3992a202d97020a068fc5da` |
| `illumos/forms/ddr4-rdimm-64g-2rx4.hexdump.txt` | `hexdump` | `4f2b609af895e4f79c974eab696c6dbad1ba49baf7c5af4f816427a005b8bd02` |
| `illumos/forms/ddr4-rdimm-64g-2rx4.od.txt` | `od -A x -t x1` | `d1ec42d54ee0df69892f884c7306d4fafde5595c4cfee67eaa350562c684d70f` |
| `illumos/forms/ddr4-rdimm-64g-2rx4.xxd.txt` | `xxd` | `48c6a1c7b23f8fbe8ee28c3cd5ffde47f331f4d69b8147d4375b7dc4e8073b17` |
| `illumos/forms/ddr5-rdimm-64g-2rx4.hexdump-C.txt` | `hexdump -C` | `ea3498ac44aab1f56470a219af574952d554df2bd885fb984db17e959dc95f94` |
| `illumos/forms/ddr5-rdimm-64g-2rx4.hexdump.txt` | `hexdump` | `33a00f1f6cdfdd2b07e3c591231e1c77b4f14305a954e509064199d38c0a97e8` |
| `illumos/forms/ddr5-rdimm-64g-2rx4.od.txt` | `od -A x -t x1` | `65c69dd4c33ed87ef72ae75b6e6a1eb01718565bf8335726e194d9d057f0d522` |
| `illumos/forms/ddr5-rdimm-64g-2rx4.xxd.txt` | `xxd` | `ab2dbe029fb5b4abc66e3b260f9f5102a8fee28f94a58e180bbd59a1c2298356` |
| `illumos/decode-dimms-4.4.txt` | `decode-dimms -x` over the `hexdump -C` text of the four DDR4 copies | `f3a2563ee0cfa74264538b3b1bdd95bd59c1501ad604b8b83abc07ae148a7c6f` |

## Demo case

[`demo`](demo/README.md) is a made host for `my spd --root testdata/spd/demo`. Its eight files `demo/spd/0-0050.bin` to `0-0057.bin` are copies of `illumos/ddr5-rdimm-64g-2rx4.bin` with the serial number of a made DIMM (bytes 517-520) and a week (bytes 515-516) written in; `TestDemoIsCurrent` in `internal/testledger/spdhost` holds them to the generator.

## Lab guests

[`lab`](lab/README.md) holds what `tools/lab/spd.sh` recorded in two QEMU guests, one with DDR4 and one with DDR5 memory devices in SMBIOS: the copies `my collect host` wrote, the kernel log lines about the SMBus and the SPD clients, and what `my spd` printed. QEMU's EEPROMs hold no SPD, so these files hold no dump. `TestLabGuests` in `cmd/my` reads them.

## Licences

| Directory | Licence | Text |
| --- | --- | --- |
| `illumos` | CDDL-1.0. The data files carry no header of their own; `usr/src/OPENSOLARIS.LICENSE` of the repository puts every file without another note under the CDDL. | [`illumos/OPENSOLARIS.LICENSE`](illumos/OPENSOLARIS.LICENSE), from commit `4a58223` |
| `ubihazard` | MIT, Copyright Ubihazard | [`ubihazard/LICENSE.md`](ubihazard/LICENSE.md) |
| `rigred` | MIT | [`rigred/LICENSE`](rigred/LICENSE) |
| `demo/spd` | CDDL-1.0, as the illumos file they are copies of | [`illumos/OPENSOLARIS.LICENSE`](illumos/OPENSOLARIS.LICENSE) |

## Dumps that are not copied

`tools/spd-fetch.sh` downloads nine DDR5 UDIMM dumps of [iron2love/DDR5_SPD_Data](https://github.com/iron2love/DDR5_SPD_Data), which states no licence, into a directory outside the tree and checks their SHA-256. `MY_SPD_DIR=<that directory> go test ./internal/decode/spd -run TestFetchedDumps` then decodes each one.
