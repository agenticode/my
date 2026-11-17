package explorer

// The reference reads some files through shell pipes (grep, head, sort).
// MemorY keeps the file as the kernel wrote it and makes the same choice
// of lines here, before a carried reader sees the text.

import (
	"sort"
	"strings"
)

// textLines are the lines of a file as the tools of a shell see them: the
// text after the last newline is a line when it is not empty.
func textLines(text string) []string {
	lines := strings.Split(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// GrepE is grep -E 'w1|w2|...' of the file: the lines that hold one of
// the words, each with its newline.
func GrepE(text string, words ...string) string {
	var b strings.Builder
	for _, l := range textLines(text) {
		for _, w := range words {
			if strings.Contains(l, w) {
				b.WriteString(l + "\n")
				break
			}
		}
	}
	return b.String()
}

// SlabTop is what the reference reads of /proc/slabinfo: the first line,
// then the 20 lines with the largest third field, as
// "head -1; sort -k3 -n -r | head -20" gives them in the C locale. Ties of
// the field are in the reverse byte order of the whole line.
func SlabTop(text string) string {
	lines := textLines(text)
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(lines[0] + "\n")
	sorted := append([]string(nil), lines...)
	keys := make(map[string]sortNum, len(sorted))
	for _, l := range sorted {
		keys[l] = sortKey(l, 3)
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		if c := compareSortNum(keys[sorted[i]], keys[sorted[j]]); c != 0 {
			return c > 0
		}
		return sorted[i] > sorted[j]
	})
	for i, l := range sorted {
		if i == 20 {
			break
		}
		b.WriteString(l + "\n")
	}
	return b.String()
}

// sortNum is a number as sort -n reads it: a sign, the digits before the
// point without leading zeros, the digits after it without trailing ones.
type sortNum struct {
	neg       bool
	int, frac string
}

func isBlank(c byte) bool { return c == ' ' || c == '\t' }

// sortKey reads the number at the start of field n (from 1) of the line.
// A field starts after the blanks that end the field before it.
func sortKey(line string, n int) sortNum {
	i := 0
	for f := 1; f < n; f++ {
		for i < len(line) && isBlank(line[i]) {
			i++
		}
		for i < len(line) && !isBlank(line[i]) {
			i++
		}
	}
	for i < len(line) && isBlank(line[i]) {
		i++
	}
	var k sortNum
	if i < len(line) && line[i] == '-' {
		k.neg = true
		i++
	}
	s := i
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	k.int = strings.TrimLeft(line[s:i], "0")
	if i < len(line) && line[i] == '.' {
		i++
		s = i
		for i < len(line) && line[i] >= '0' && line[i] <= '9' {
			i++
		}
		k.frac = strings.TrimRight(line[s:i], "0")
	}
	if k.int == "" && k.frac == "" {
		k.neg = false // -0 is 0
	}
	return k
}

func compareSortNum(a, b sortNum) int {
	if a.neg != b.neg {
		if a.neg {
			return -1
		}
		return 1
	}
	c := compareMag(a, b)
	if a.neg {
		return -c
	}
	return c
}

func compareMag(a, b sortNum) int {
	if len(a.int) != len(b.int) {
		if len(a.int) < len(b.int) {
			return -1
		}
		return 1
	}
	if c := strings.Compare(a.int, b.int); c != 0 {
		return c
	}
	return strings.Compare(a.frac, b.frac)
}
