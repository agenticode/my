package smbios

import (
	"encoding/binary"
	"fmt"
	"regexp"
	"strings"
)

// processor is what a processor record (type 4) tells of its socket.
type processor struct {
	name, signature string
	populated       bool
	// unknown is set when the record says nothing of its socket (a text
	// without a Status line).
	unknown bool
}

// hostCPU is the name, the signature and the populated flags the host
// keeps of its processor records: the name of the first record that is
// populated and has a name, else of the first record, with the signature of
// the same record; one character per record, "1" for a populated socket.
func hostCPU(ps []processor) (name, signature, present string) {
	if len(ps) == 0 {
		return "", "", ""
	}
	pick := ps[0]
	for _, p := range ps {
		if (p.populated || p.unknown) && p.name != "" {
			pick = p
			break
		}
	}
	var b strings.Builder
	for _, p := range ps {
		switch {
		case p.unknown:
			// One record that does not tell leaves them all unknown.
			return pick.name, pick.signature, ""
		case p.populated:
			b.WriteByte('1')
		default:
			b.WriteByte('0')
		}
	}
	return pick.name, pick.signature, b.String()
}

// processorOf reads a processor record of raw tables: the version string,
// the populated bit of the status byte and, for an x86 processor, the CPUID
// signature as dmidecode prints it.
func processorOf(s *Structure) processor {
	p := processor{name: clean(s.str(0x10))}
	if st, ok := s.u8(0x18); ok {
		p.populated = st&0x40 != 0
	}
	if len(s.Data) >= 0x10 {
		eax := binary.LittleEndian.Uint32(s.Data[0x08:])
		fam, _ := s.u8(0x06)
		family := uint16(fam)
		if fam == 0xFE {
			if f2, ok := s.u16(0x28); ok {
				family = f2
			}
		}
		switch x86Kind(family, s.str(0x07), s.str(0x10)) {
		case 'i':
			p.signature = fmt.Sprintf("%d/%d/%d", (eax>>20)&0xFF+(eax>>8)&0x0F, (eax>>12)&0xF0+(eax>>4)&0x0F, eax&0x0F)
		case 'a':
			f, m := (eax>>8)&0x0F, (eax>>4)&0x0F
			if f == 0x0F {
				f += (eax >> 20) & 0xFF
				m |= (eax >> 12) & 0xF0
			}
			p.signature = fmt.Sprintf("%d/%d/%d", f, m, eax&0x0F)
		}
	}
	return p
}

// x86Kind tells an Intel ('i') or AMD ('a') processor by the family code of
// its record, as dmidecode does, and by its maker where the family code is
// "Other" or "Unknown"; 0 for any other processor.
func x86Kind(family uint16, maker, version string) byte {
	in := func(lo, hi uint16) bool { return family >= lo && family <= hi }
	switch {
	case in(0x0B, 0x15), in(0x28, 0x2F), in(0xA1, 0xB3), family == 0xB5, in(0xB9, 0xC7), in(0xCD, 0xCF), in(0xD2, 0xDB), in(0xDD, 0xE0):
		return 'i'
	case in(0x18, 0x1D), family == 0x1F, in(0x38, 0x3F), in(0x46, 0x4F), in(0x66, 0x6B), in(0x83, 0x8F), in(0xB6, 0xB7), in(0xE4, 0xEF):
		return 'a'
	case family <= 0x02:
		m := strings.ToLower(maker + " " + version)
		switch {
		case strings.Contains(m, "intel"):
			return 'i'
		case strings.Contains(m, "amd") || strings.Contains(m, "advanced micro"):
			return 'a'
		}
	}
	return 0
}

var signatureRE = regexp.MustCompile(`Family (\d+), Model (\d+), Stepping (\d+)`)

// processorOfText reads a processor record of dmidecode text.
func processorOfText(f map[string]string) processor {
	p := processor{name: clean(f["Version"])}
	st, ok := f["Status"]
	p.populated, p.unknown = strings.HasPrefix(strings.ToLower(st), "populated"), !ok
	if m := signatureRE.FindStringSubmatch(f["Signature"]); m != nil {
		p.signature = m[1] + "/" + m[2] + "/" + m[3]
	}
	return p
}

// flashTypes are the memory types of a memory device that is a chip of
// the board (the firmware's flash), not a slot.
var flashTypes = map[string]bool{"FLASH": true, "ROM": true, "EPROM": true, "FEPROM": true, "EEPROM": true}

func isFlash(memoryType string) bool {
	return flashTypes[strings.ToUpper(strings.TrimSpace(memoryType))]
}
