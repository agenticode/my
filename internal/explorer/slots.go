package explorer

// This file is MemorY's own: it writes what the ledger knows of a slot as
// the dmidecode text the carried reader reads.

import (
	"fmt"
	"strconv"
	"strings"
)

// SlotText is a slot as the board hands it to ParseDmidecodeMemory.
type SlotText struct {
	Locator     string // "" when the inventory gave none
	BankLocator string
	Populated   bool
	SizeMB      int64  // 0 when the size is not known
	Type        string // the memory type, "" when not known
}

// SlotHost is what the ledger knows of the system of the slots: the maker
// and product of the system, the board in one string, and its processor
// records. WriteSlotsOf writes them as the System, Base Board and Processor
// records of dmidecode, which the readers take hints from.
type SlotHost struct {
	Vendor, Product, Board string
	// Processor and Signature are the name and the CPUID signature
	// ("family/model/stepping") of the processor.
	Processor, Signature string
	// Sockets is the number of processor records, 0 when not known.
	Sockets int
	// Present has one character per processor record, "1" when the socket
	// holds a processor and "0" when not; "" when not known.
	Present string
}

// WriteSlotsOf writes the system, board and processor records of the host,
// when known, and then the slots as WriteSlots does. Each processor record
// carries the name and the signature of the host's processor and the status
// of its socket. Without a socket count a known processor name is written
// as one record without a status, as a text with one processor record.
func WriteSlotsOf(h SlotHost, slots []SlotText) string {
	var b strings.Builder
	if h.Vendor != "" || h.Product != "" {
		b.WriteString("Handle 0x0100, DMI type 1, 27 bytes\nSystem Information\n")
		if h.Vendor != "" {
			b.WriteString("\tManufacturer: " + dmiString(h.Vendor) + "\n")
		}
		if h.Product != "" {
			b.WriteString("\tProduct Name: " + dmiString(h.Product) + "\n")
		}
		b.WriteString("\n")
	}
	if h.Board != "" {
		b.WriteString("Handle 0x0200, DMI type 2, 15 bytes\nBase Board Information\n\tProduct Name: " + dmiString(h.Board) + "\n\n")
	}
	n := max(h.Sockets, len(h.Present))
	if n == 0 && h.Processor != "" {
		n = 1
	}
	sig := ""
	if f := strings.Split(h.Signature, "/"); len(f) == 3 {
		sig = "Family " + f[0] + ", Model " + f[1] + ", Stepping " + f[2]
	}
	for i := 0; i < n && i < MaxBoardSockets*8; i++ {
		b.WriteString(fmt.Sprintf("Handle 0x%04X, DMI type 4, 48 bytes\nProcessor Information\n", 0x0400+i))
		if sig != "" {
			b.WriteString("\tSignature: " + sig + "\n")
		}
		b.WriteString("\tVersion: " + dmiString(h.Processor) + "\n")
		if i < len(h.Present) {
			if h.Present[i] == '1' {
				b.WriteString("\tStatus: Populated, Enabled\n")
			} else {
				b.WriteString("\tStatus: Unpopulated\n")
			}
		}
		b.WriteString("\n")
	}
	return b.String() + WriteSlots(slots)
}

// WriteSlots writes one Memory Device block per slot with the fields the
// placement reads: Size, Locator, Bank Locator and, when known, Type. A missing locator is
// "Not Specified", as dmidecode writes it, and every byte below 0x20 or
// from 0x7F on is a dot, as dmidecode prints a string.
func WriteSlots(slots []SlotText) string {
	var b strings.Builder
	for i, s := range slots {
		size := "No Module Installed"
		if s.Populated {
			size = "Unknown"
			if s.SizeMB > 0 {
				size = strconv.FormatInt(s.SizeMB, 10) + " MB"
			}
		}
		b.WriteString(fmt.Sprintf("Handle 0x%04X, DMI type 17, 40 bytes\nMemory Device\n", 0x1100+i))
		b.WriteString("\tSize: " + size + "\n")
		b.WriteString("\tLocator: " + dmiString(s.Locator) + "\n")
		b.WriteString("\tBank Locator: " + dmiString(s.BankLocator) + "\n")
		if s.Type != "" {
			b.WriteString("\tType: " + dmiString(s.Type) + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func dmiString(s string) string {
	if s == "" {
		return "Not Specified"
	}
	b := []byte(s)
	for i, c := range b {
		if c < 0x20 || c >= 0x7F {
			b[i] = '.'
		}
	}
	return string(b)
}
