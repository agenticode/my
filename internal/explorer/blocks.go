package explorer

// This file is MemorY's own.

import "strings"

// NumastatBlocks cuts numastat text into its blocks of nodes: numastat
// prints as many nodes side by side as fit its width and continues below.
// A block starts at a line that names nodes and holds the lines up to the
// next such line. Text of one block is one block.
func NumastatBlocks(text string) []string {
	var out []string
	var cur []string
	for _, l := range Lines(text) {
		if reNumaHeader.Test(l) && !strings.Contains(l, "_") && len(cur) > 0 && hasRows(cur) {
			out = append(out, strings.Join(cur, "\n")+"\n")
			cur = nil
		}
		cur = append(cur, l)
	}
	if len(cur) > 0 {
		out = append(out, strings.Join(cur, "\n"))
	}
	return out
}

func hasRows(lines []string) bool {
	for _, l := range lines {
		if strings.Contains(l, "_") {
			return true
		}
	}
	return false
}
