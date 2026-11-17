package spd

// The expected decode of every public copy under testdata/spd and of every
// synthetic dump the tests build: one row per dump, "-" for a field that
// is not set. Each public row was checked against a second decoder
// (docs/compatibility.md says which); no second decoder saw the synthetic
// dumps, whose rows hold the formulas of this package.

const expectedPublic = `name	bytes	type	module_type	spd_revision	crc_base	crc_module	date	date_raw	location	serial	part	revision	module_maker	dram_maker	dram_stepping	die_density	dies_per_package	package	device_width	package_ranks	logical_ranks	rank_mix	bus_width	ecc_bits	subchannels	size_bytes	organisation	speed_mts	short
ddr4-rdimm-64g-2rx4	512	DDR4	RDIMM	1.2	ok	ok	2021-W43	21 43	0x06	4D590001	36ASF8G72PZ-3G2E1	0x31	bank 1, 0x2C	bank 1, 0x2C	0x45	16 Gb	1	monolithic	4	2	2	symmetric	64	8	-	68719476736	2Rx4	3200	-
ddr4-lrdimm-128g-3ds	512	DDR4	LRDIMM	1.2	ok	ok	2023-W24	23 24	0x00	BAADCAFE	M386AAK40B40-CWD	0x72	bank 1, 0xCE	bank 1, 0xCE	0x27	8 Gb	4	3DS	4	2	8	symmetric	64	8	-	137438953472	2Rx4	2666	-
ddr4-udimm-32g-2rx8	512	DDR4	UDIMM	1.1	ok	ok	-	DA AD	0x00	99887766	AQD-D4U32N32-SBW	0x2B	bank 2, 0x7A	bank 1, 0xA4	0x95	16 Gb	1	monolithic	8	2	2	symmetric	64	0	-	34359738368	2Rx8	3200	-
ddr4-sodimm-16g-2rx8	512	DDR4	SODIMM	1.1	ok	ok	-	29 1D	0x00	E1BEE218	AQD-SD4U16GN32-SE1	0x45	bank 11, 0xC8	bank 11, 0xC8	0x54	8 Gb	1	monolithic	8	2	2	symmetric	64	0	-	17179869184	2Rx8	3200	-
ddr5-rdimm-64g-2rx4	1024	DDR5	RDIMM	1.0	ok	-	2022-W43	22 43	0x06	4D590002	MTC40F2046S1RC48BA1	0x31	bank 1, 0x2C	bank 1, 0x2C	0x41	16 Gb	1	monolithic	4	2	2	symmetric	32	8	2	68719476736	2Rx4	4800	-
ddr5-rdimm-16g-1rx8	1024	DDR5	RDIMM	1.0	ok	-	-	AF 82	0x00	13576428	AQD-D5V16GR48-SB	0x00	bank 5, 0xCB	bank 1, 0xCE	0xFF	16 Gb	1	monolithic	8	1	1	symmetric	32	8	2	17179869184	1Rx8	4800	-
ddr5-udimm-16g-1rx8-a	1024	DDR5	UDIMM	1.0	ok	-	2023-W37	23 37	0x00	4D590003	UD5-6000	0x00	bank 5, 0xEF	-	0xFF	16 Gb	1	monolithic	8	1	1	symmetric	32	0	2	17179869184	1Rx8	4800	-
ddr5-udimm-16g-1rx8-b	1024	DDR5	UDIMM	1.0	ok	-	2023-W37	23 37	0x00	4D590004	UD5-6000	0x00	bank 5, 0xEF	-	0xFF	16 Gb	1	monolithic	8	1	1	symmetric	32	0	2	17179869184	1Rx8	4800	-
ddr3-sodimm	256	DDR3	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-
`

const expectedSynthetic = `name	bytes	type	module_type	spd_revision	crc_base	crc_module	date	date_raw	location	serial	part	revision	module_maker	dram_maker	dram_stepping	die_density	dies_per_package	package	device_width	package_ranks	logical_ranks	rank_mix	bus_width	ecc_bits	subchannels	size_bytes	organisation	speed_mts	short	note	reason
ddr4-rdimm-1rx8	512	DDR4	RDIMM	1.2	ok	ok	2021-W43	21 43	0x06	4D590001	36ASF8G72PZ-3G2E1	0x31	bank 1, 0x2C	bank 1, 0x2C	0x45	16 Gb	1	monolithic	8	1	1	symmetric	64	8	-	17179869184	1Rx8	3200	-	byte 12 = 0x01 (1 package rank, x8), base CRC written again	-
ddr4-rdimm-asymmetric	512	DDR4	RDIMM	1.2	ok	ok	2021-W43	21 43	0x06	4D590001	36ASF8G72PZ-3G2E1	0x31	bank 1, 0x2C	bank 1, 0x2C	0x45	16 Gb	1	monolithic	4	2	2	asymmetric	64	8	-	68719476736	2Rx4	3200	-	byte 12 bit 6 set (rank mix), base CRC written again	-
ddr4-rdimm-ddp	512	DDR4	RDIMM	1.2	ok	ok	2021-W43	21 43	0x06	4D590001	36ASF8G72PZ-3G2E1	0x31	bank 1, 0x2C	bank 1, 0x2C	0x45	16 Gb	2	multi-load stack	4	2	2	symmetric	64	8	-	68719476736	2Rx4	3200	-	byte 6 = 0x91 (2 dies, multi-load stack), base CRC written again	-
ddr4-bad-base-crc	512	DDR4	RDIMM	1.2	bad (stored 0xA3FD, computed 0xE0A8)	ok	2021-W43	21 43	0x06	4D590001	36ASF8G72PZ-3G2E1	0x31	bank 1, 0x2C	bank 1, 0x2C	0x45	32 Gb	1	monolithic	4	2	2	symmetric	64	8	-	137438953472	2Rx4	3200	-	byte 4 changed, CRC left as it was	-
ddr4-bad-module-crc	512	DDR4	RDIMM	1.2	ok	bad (stored 0xF543, computed 0x9D8D)	2021-W43	21 43	0x06	4D590001	36ASF8G72PZ-3G2E1	0x31	bank 1, 0x2C	bank 1, 0x2C	0x45	16 Gb	1	monolithic	4	2	2	symmetric	64	8	-	68719476736	2Rx4	3200	-	byte 200 changed, CRC of bytes 128-253 left as it was	-
ddr4-first-page	256	DDR4	RDIMM	1.2	ok	ok	-	-	-	-	-	-	-	-	-	16 Gb	1	monolithic	4	2	2	symmetric	64	8	-	68719476736	2Rx4	3200	the manufacturing block ends at byte 352, the dump has 256 bytes	the first 256 bytes	-
ddr4-first-half-page	128	DDR4	RDIMM	1.2	ok	not in the dump	-	-	-	-	-	-	-	-	-	16 Gb	1	monolithic	4	2	2	symmetric	64	8	-	68719476736	2Rx4	3200	the manufacturing block ends at byte 352, the dump has 128 bytes	the first 128 bytes	-
ddr5-rdimm-3ds-4h	1024	DDR5	RDIMM	1.0	ok	-	2022-W43	22 43	0x06	4D590002	MTC40F2046S1RC48BA1	0x31	bank 1, 0x2C	bank 1, 0x2C	0x41	16 Gb	4	3DS	4	2	8	symmetric	32	8	2	274877906944	2Rx4	4800	-	byte 4 bits 7-5 = 3 (4 dies, 3DS), CRC written again	-
ddr5-rdimm-ddp	1024	DDR5	RDIMM	1.0	ok	-	2022-W43	22 43	0x06	4D590002	MTC40F2046S1RC48BA1	0x31	bank 1, 0x2C	bank 1, 0x2C	0x41	16 Gb	2	DDP	4	2	2	symmetric	32	8	2	137438953472	2Rx4	4800	-	byte 4 bits 7-5 = 1 (2 dies, DDP), CRC written again	-
ddr5-first-page	128	DDR5	RDIMM	1.0	not in the dump	-	-	-	-	-	-	-	-	-	-	16 Gb	1	monolithic	4	-	-	-	-	-	-	-	-	4800	ranks and the CRC need 512 bytes, the dump has 128	the first 128 bytes (what a host reads when pages cannot be selected)	-
ddr5-half	512	DDR5	RDIMM	1.0	ok	-	-	-	-	-	-	-	-	-	-	16 Gb	1	monolithic	4	2	2	symmetric	32	8	2	68719476736	2Rx4	4800	the manufacturing block ends at byte 554, the dump has 512 bytes	the first 512 bytes	-
ddr5-bad-crc	1024	DDR5	RDIMM	1.0	bad (stored 0x3353, computed 0x1C1B)	-	2022-W43	22 43	0x06	4D590002	MTC40F2046S1RC48BA1	0x31	bank 1, 0x2C	bank 1, 0x2C	0x41	16 Gb	1	monolithic	4	1	1	symmetric	32	8	2	34359738368	1Rx4	4800	-	byte 234 changed, CRC left as it was	-
blank-00	512	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	512 bytes of 0x00	blank: the first 128 bytes are all 0x00
blank-ff	512	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	-	512 bytes of 0xFF	blank: the first 128 bytes are all 0xFF
`
