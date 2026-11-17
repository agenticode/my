package explorer

// This file is MemorY's own: it writes what the ledger knows of a slot as
// the dmidecode text the carried reader reads.

import (
	"strconv"
	"strings"
)

// SlotText is a slot as the board hands it to ParseDmidecodeMemory.
type SlotText struct {
	Locator     string // "" when the inventory gave none
	BankLocator string
	Populated   bool
	SizeMB      int64 // 0 when the size is not known
}

// SlotHost is what the ledger knows of the system of the slots: the maker
// and the name of its processor. WriteSlotsOf writes them as the System and
// Processor records of dmidecode, which the readers take hints from.
type SlotHost struct {
	Vendor, Processor string
}

// WriteSlotsOf writes the system and processor records of the host, when
// known, and then the slots as WriteSlots does.
func WriteSlotsOf(h SlotHost, slots []SlotText) string {
	var b strings.Builder
	if h.Vendor != "" {
		b.WriteString("Handle 0x0100, DMI type 1, 27 bytes\nSystem Information\n\tManufacturer: " + dmiString(h.Vendor) + "\n\n")
	}
	if h.Processor != "" {
		b.WriteString("Handle 0x0400, DMI type 4, 48 bytes\nProcessor Information\n\tVersion: " + dmiString(h.Processor) + "\n\n")
	}
	return b.String() + WriteSlots(slots)
}

// WriteSlots writes one Memory Device block per slot with the three fields
// the placement reads: Size, Locator and Bank Locator. A missing locator is
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
		b.WriteString("Handle 0x" + strconv.FormatInt(int64(0x1100+i), 16) + ", DMI type 17, 40 bytes\nMemory Device\n")
		b.WriteString("\tSize: " + size + "\n")
		b.WriteString("\tLocator: " + dmiString(s.Locator) + "\n")
		b.WriteString("\tBank Locator: " + dmiString(s.BankLocator) + "\n\n")
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
