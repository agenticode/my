package spd

// Field is one field of the SPD layout this package decodes, as
// docs/compatibility.md lists it.
type Field struct {
	Bytes  string // the byte or bytes: "4", "18, 125", "320-321"
	Bits   string // the bits within a byte, "" for whole bytes
	Name   string
	Coding string
}

// DDR4Layout lists the DDR4 bytes the decoder reads (JEDEC Standard 21-C,
// Annex L), in byte order.
var DDR4Layout = []Field{
	{"1", "7-4, 3-0", "SPD revision", "major, minor"},
	{"2", "", "memory type", "0x0C DDR4, 0x12 DDR5; other key bytes are named and not decoded"},
	{"3", "3-0, 7", "module type, hybrid", "1 RDIMM, 2 UDIMM, 3 SODIMM, 4 LRDIMM, 5 Mini-RDIMM, 6 Mini-UDIMM, 8 72b-SO-RDIMM, 9 72b-SO-UDIMM, 12 16b-SO-DIMM, 13 32b-SO-DIMM"},
	{"4", "3-0", "capacity of a die", "0 256 Mb, doubling up to 7 32 Gb; 8 12 Gb, 9 24 Gb"},
	{"5", "5-3, 2-0", "row and column address bits", "12 + n, 9 + n"},
	{"6", "7, 6-4, 1-0", "package", "monolithic or not; 1 + n dies; signal loading 1 multi-load stack, 2 single-load stack (3DS)"},
	{"12", "6, 5-3, 2-0", "rank mix, package ranks, device width", "symmetric or asymmetric; 1 + n; 0 x4, 1 x8, 2 x16, 3 x32"},
	{"13", "4-3, 2-0", "bus width extension, primary bus width", "0 none, 1 8 bits; 0 8, 1 16, 2 32, 3 64 bits"},
	{"18, 125", "", "minimum cycle time (tCKAVGmin)", "byte 18 in units of 125 ps plus byte 125 as a signed number of ps"},
	{"126-127", "", "CRC of bytes 0-125", "low byte first"},
	{"254-255", "", "CRC of bytes 128-253", "low byte first"},
	{"320-321", "", "module maker", "continuation count with parity, then the code"},
	{"322", "", "manufacturing location", "the maker's own coding"},
	{"323, 324", "", "manufacturing year, week", "BCD"},
	{"325-328", "", "serial number", "4 bytes, shown as 8 hex digits in stored order"},
	{"329-348", "", "part number", "20 ASCII characters, padded with spaces"},
	{"349", "", "module revision", ""},
	{"350-351", "", "DRAM maker", "as bytes 320-321"},
	{"352", "", "DRAM stepping", ""},
}

// DDR5Layout lists the DDR5 bytes the decoder reads (JESD400-5), in byte
// order.
var DDR5Layout = []Field{
	{"0", "6-4", "size of the SPD device", "1 256, 2 512, 3 1,024, 4 2,048 bytes"},
	{"1", "7-4, 3-0", "SPD revision", "major, minor"},
	{"2", "", "memory type", "0x12"},
	{"3", "3-0, 7", "module type, hybrid", "1 RDIMM, 2 UDIMM, 3 SODIMM, 4 LRDIMM, 5 CUDIMM, 6 CSODIMM, 7 MRDIMM, 8 CAMM2, 9 SOCAMM2, 10 DDIMM, 11 solder down"},
	{"4", "7-5, 4-0", "dies per package, capacity of a die", "0 one die, 1 two (DDP), 2 two, 3 four, 4 eight, 5 sixteen (3DS); 1 4 Gb, 2 8, 3 12, 4 16, 5 24, 6 32, 7 48, 8 64 Gb"},
	{"5", "7-5, 4-0", "column and row address bits", "10 + n, 16 + n"},
	{"6", "7-5", "device width", "0 x4, 1 x8, 2 x16, 3 x32"},
	{"20-21", "", "minimum cycle time (tCKAVGmin)", "ps, low byte first"},
	{"234", "6, 5-3", "rank mix, package ranks per sub-channel", "symmetric or asymmetric; 1 + n"},
	{"235", "7-5, 4-3, 2-0", "sub-channels, bus width extension, primary bus width per sub-channel", "1 + n; 0 none, 1 4, 2 8 bits; 0 8, 1 16, 2 32, 3 64 bits"},
	{"510-511", "", "CRC of bytes 0-509", "low byte first"},
	{"512-513", "", "module maker", "as DDR4"},
	{"514", "", "manufacturing location", ""},
	{"515, 516", "", "manufacturing year, week", "BCD"},
	{"517-520", "", "serial number", "4 bytes"},
	{"521-550", "", "part number", "30 ASCII characters"},
	{"551", "", "module revision", ""},
	{"552-553", "", "DRAM maker", "as bytes 512-513"},
	{"554", "", "DRAM stepping", ""},
}
