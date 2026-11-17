package explorer

import (
	"regexp"
	"strconv"
	"strings"
)

// Fix names one correction of the first implementation of the readers.
// Every fix is in force; a test may switch them off to show that the
// readers without them still give the answers kept in the want files.
type Fix uint32

const (
	// FixBinarySizes reads sizes in MiB, GiB and TiB, as dmidecode 3.7
	// and later write them.
	FixBinarySizes Fix = 1 << iota
	// FixDimmWord keeps the D of DIMM out of the channel letter:
	// CPU0_DIMM_A1 is channel A.
	FixDimmWord
	// FixNumberingBase counts a socket of P1, Node 1 or CPU 1 from 0 when
	// a slot of the same text has P0, Node 0 or CPU 0, and from 1 when none
	// has.
	FixNumberingBase
	// FixLetterBeforeNode takes the channel letter of the locator before
	// the node number of the bank locator.
	FixLetterBeforeNode
	// FixCPUWithSpace reads the socket of a bank locator CPU 1 (with a
	// space, as Lenovo writes it).
	FixCPUWithSpace
	// FixPLetter reads DIMM_P0_A0 as socket 0, channel A.
	FixPLetter
	// FixDellChannels gives a slot named like A1 the channel count of its
	// processor in place of half the slots of the socket.
	FixDellChannels
	// FixMoreSockets arranges every socket on the board, two to a row.
	FixMoreSockets
	// FixEdacControllers ties EDAC counts to slots only when the lines
	// come from one memory controller.
	FixEdacControllers
	// FixLineEnds reads text with CR or CRLF line ends.
	FixLineEnds
	// FixEmptyField reads an empty field as empty, without the next line.
	FixEmptyField
	// FixNumastatBlocks reads every block of nodes of numastat.
	FixNumastatBlocks
	// FixSupermicroNode counts the channels of P1_Node1_Channel0 from the
	// first node of the socket.
	FixSupermicroNode
	// FixSlabBySize picks the 20 slab caches that take the most memory.
	FixSlabBySize
	// FixDellDimm reads DIMM_A1 on a Dell system as A1.
	FixDellDimm
	// FixChanWord keeps CHAN out of the channel of CHAN A DIMM 0.
	FixChanWord
	// FixLettersFromA takes the letter of A1 as the socket only when the
	// letters start at A, and counts the slots of A0 from 0.
	FixLettersFromA

	lastFix
)

// AllFixes are every fix.
const AllFixes = lastFix - 1

var fixes = AllFixes

// SetFixes puts the given fixes in force and returns those in force
// before. For tests; not safe while readers run.
func SetFixes(f Fix) Fix {
	old := fixes
	fixes = f
	return old
}

func on(f Fix) bool { return fixes&f != 0 }

var fixNames = map[Fix]string{
	FixBinarySizes: "binary-sizes", FixDimmWord: "dimm-word", FixNumberingBase: "numbering-base",
	FixLetterBeforeNode: "letter-before-node", FixCPUWithSpace: "cpu-with-space", FixPLetter: "p-letter",
	FixDellChannels: "dell-channels", FixMoreSockets: "more-sockets", FixEdacControllers: "edac-controllers",
	FixLineEnds: "line-ends", FixEmptyField: "empty-field", FixNumastatBlocks: "numastat-blocks",
	FixSupermicroNode: "supermicro-node", FixSlabBySize: "slab-by-size", FixDellDimm: "dell-dimm",
	FixChanWord: "chan-word", FixLettersFromA: "letters-from-a",
}

// Name is the name of a fix, as the tests list it.
func (f Fix) Name() string { return fixNames[f] }

// FixNamed returns the fix of a name.
func FixNamed(name string) (Fix, bool) {
	for f, n := range fixNames {
		if n == name {
			return f, true
		}
	}
	return 0, false
}

// Fixes lists every fix.
func Fixes() []Fix {
	var out []Fix
	for f := Fix(1); f < lastFix; f <<= 1 {
		out = append(out, f)
	}
	return out
}

// lineEnds turns CRLF and CR into LF.
func lineEnds(s string) string {
	if !on(FixLineEnds) || !strings.ContainsRune(s, '\r') {
		return s
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// textHints are what a dmidecode text says of the system that the slots
// alone do not: its maker and the name of its processor.
type textHints struct {
	vendor, processor string
}

var (
	reSystemMaker = regexp.MustCompile(`(?m)^System Information\n(?:[ \t]+[^\n]*\n)*?[ \t]+Manufacturer:[ \t]*([^\n]*)`)
	reProcessor   = regexp.MustCompile(`(?m)^Processor Information\n(?:[ \t]+[^\n]*\n)*?[ \t]+Version:[ \t]*([^\n]*)`)
)

func hintsOf(raw string) textHints {
	var h textHints
	if m := reSystemMaker.FindStringSubmatch(raw); m != nil {
		h.vendor = strings.TrimSpace(m[1])
	}
	if m := reProcessor.FindStringSubmatch(raw); m != nil {
		h.processor = strings.TrimSpace(m[1])
	}
	return h
}

func (h textHints) dell() bool { return strings.HasPrefix(strings.ToLower(h.vendor), "dell") }

var (
	reEpyc     = regexp.MustCompile(`EPYC\s+(\d)\d{3}`)
	reXeonE    = regexp.MustCompile(`\bE[57]-\d{4}`)
	reXeonOld  = regexp.MustCompile(`\b[XEL]5[5-6]\d{2}\b`)
	reXeonMet  = regexp.MustCompile(`(?:Gold|Silver|Bronze|Platinum)\s+\d(\d)\d{2}`)
	reXeon6    = regexp.MustCompile(`Xeon\(R\)\s+6(\d)\d{2}`)
	reXeonDesk = regexp.MustCompile(`\bE3-\d{4}|\bE-2\d{3}`)
)

// ProcessorChannels is the number of memory channels a processor has,
// read from its name; ok is false for a name the table does not know.
func ProcessorChannels(name string) (int, bool) {
	if m := reEpyc.FindStringSubmatch(name); m != nil {
		switch m[1] {
		case "9":
			return 12, true
		case "8":
			return 6, true
		case "7":
			return 8, true
		case "4":
			return 2, true
		}
		return 0, false
	}
	if reXeonE.MatchString(name) {
		return 4, true
	}
	if reXeonOld.MatchString(name) {
		return 3, true
	}
	if m := reXeonMet.FindStringSubmatch(name); m != nil {
		if g, _ := strconv.Atoi(m[1]); g <= 2 {
			return 6, true
		}
		return 8, true
	}
	if m := reXeon6.FindStringSubmatch(name); m != nil {
		if m[1] == "9" {
			return 12, true
		}
		return 8, true
	}
	if reXeonDesk.MatchString(name) {
		return 2, true
	}
	return 0, false
}
