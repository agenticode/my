package explorer

import "strings"

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
	// FixRepeatedTable reads once a record that stands again with the same
	// handle and the same fields (a text that holds its table twice).
	FixRepeatedTable
	// FixNameForms reads each slot name as a whole by the forms of the
	// name table and runs the pass that decides socket, channel and
	// position of every slot; the fixes after it are its stages.
	FixNameForms
	// FixBoardFamilies places the slots of a board family from its table,
	// leaves out the records its manual has no slot for, names the empty
	// slots without a name by the labels of the manual, and gives a board
	// known by a part number the rule of its family.
	FixBoardFamilies
	// FixLetterMeaning decides what the letter of A1 and the digit of 1A
	// are on a board.
	FixLetterMeaning
	// FixSocketRules decides the processor of a slot: the processor word, a
	// Node of the bank locator, one processor record, the processor of the
	// slots of one channel letter, the channel letters split in order.
	FixSocketRules
	// FixChannelRank numbers the channels of a processor and the slots of a
	// channel from 0 in the vendor's order.
	FixChannelRank

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

// stage tells a stage of the pass of FixNameForms: in force when the pass
// runs and the stage is on.
func stage(f Fix) bool { return on(FixNameForms) && on(f) }

var fixNames = map[Fix]string{
	FixBinarySizes: "binary-sizes", FixDimmWord: "dimm-word", FixNumberingBase: "numbering-base",
	FixLetterBeforeNode: "letter-before-node", FixCPUWithSpace: "cpu-with-space", FixPLetter: "p-letter",
	FixDellChannels: "dell-channels", FixMoreSockets: "more-sockets", FixEdacControllers: "edac-controllers",
	FixLineEnds: "line-ends", FixEmptyField: "empty-field", FixNumastatBlocks: "numastat-blocks",
	FixSupermicroNode: "supermicro-node", FixSlabBySize: "slab-by-size", FixDellDimm: "dell-dimm",
	FixChanWord: "chan-word", FixLettersFromA: "letters-from-a", FixRepeatedTable: "repeated-table",
	FixNameForms: "name-forms", FixBoardFamilies: "board-families", FixLetterMeaning: "letter-meaning",
	FixSocketRules: "socket-rules", FixChannelRank: "channel-rank",
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
