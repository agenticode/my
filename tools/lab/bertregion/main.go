// Command bertregion writes a boot error region made by hand, and the body
// of a BERT table that points at it, for an x86 guest of the lab:
//
//	bertregion -addr 0x7f000000 -o DIR [-row N -col N ...]
//
// DIR/region.bin holds one Generic Error Status Block of severity fatal
// with one Platform Memory section (ACPI 6.5, 18.3.2.7 and 18.3.2.9; the
// layouts of struct acpi_hest_generic_status and acpi_hest_generic_data in
// include/acpi/actbl1.h, and struct cper_sec_mem_err in include/linux/cper.h).
// DIR/bert.bin is the body of the table after its header: the length of the
// region (u32) and its address (u64), as struct acpi_table_bert
// (include/acpi/actbl1.h) has them. QEMU adds the header:
//
//	-acpitable sig=BERT,file=DIR/bert.bin
//	-device loader,file=DIR/region.bin,addr=0x7f000000
//
// and the guest keeps the page out of its RAM with memmap=4K$0x7f000000, so
// that bert_init (drivers/acpi/apei/bert.c) may map it. The loader writes the
// region again at every reset of the machine.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/agenticode/my/internal/decode/cper"
	"github.com/agenticode/my/internal/model"
)

func main() {
	addr := flag.Uint64("addr", 0x7f000000, "physical address of the region")
	out := flag.String("o", ".", "directory to write region.bin and bert.bin to")
	size := flag.Int("size", 4096, "length of the region the table names")
	pa := flag.Uint64("pa", 0x12345040, "physical address of the error")
	node := flag.Int("node", 0, "node (socket)")
	card := flag.Int("card", 0, "card (memory controller channel)")
	module := flag.Int("module", 0, "module")
	rank := flag.Int("rank", 0, "rank")
	bank := flag.Int("bank", 3, "bank address")
	bg := flag.Int("bank-group", 0, "bank group")
	dev := flag.Int("device", 7, "device")
	row := flag.Int("row", 0x1a2f, "row")
	col := flag.Int("col", 0x238, "column")
	fru := flag.String("fru", "DIMM 0", "FRU text of the entry")
	flag.Parse()

	m := cper.MemError{
		ErrorStatus: model.Some(uint64(0x0400)), PhysicalAddress: model.Some(*pa), PhysicalAddressMask: model.Some(^uint64(0x3f)),
		Node: model.Some(*node), Card: model.Some(*card), Module: model.Some(*module), Rank: model.Some(*rank),
		Bank: model.Some(*bank), BankGroup: model.Some(*bg), Device: model.Some(*dev), Row: model.Some(*row), Column: model.Some(*col),
		ErrorType: model.Some(3), // multi-bit ECC
	}
	region := cper.StatusBlockBytes(1, cper.SevFatal, cper.Entry{SectionType: cper.SectionPlatformMemory, Severity: cper.SevFatal,
		FRUText: *fru, Data: cper.MemSection(m)})
	if len(region) > *size {
		fmt.Fprintf(os.Stderr, "bertregion: the region needs %d bytes, more than -size %d\n", len(region), *size)
		os.Exit(1)
	}
	region = append(region, make([]byte, *size-len(region))...)
	body := make([]byte, 12)
	binary.LittleEndian.PutUint32(body[0:], uint32(*size))
	binary.LittleEndian.PutUint64(body[4:], *addr)
	for name, b := range map[string][]byte{"region.bin": region, "bert.bin": body} {
		if err := os.WriteFile(filepath.Join(*out, name), b, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "bertregion:", err)
			os.Exit(1)
		}
	}
}
