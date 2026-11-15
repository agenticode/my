package term

import (
	"strings"
	"unicode"
)

// runeWidth returns the number of terminal columns a rune takes: two for
// Hangul and other wide East Asian characters, none for combining marks,
// one otherwise.
func runeWidth(r rune) int {
	switch {
	case r < 0x20 || (r >= 0x7f && r < 0xa0):
		return 0
	case r < 0x1100:
		if unicode.Is(unicode.Mn, r) {
			return 0
		}
		return 1
	case r <= 0x115f, // Hangul Jamo initial consonants
		r >= 0x2e80 && r <= 0xa4cf, // CJK radicals to Yi
		r >= 0xac00 && r <= 0xd7a3, // Hangul syllables
		r >= 0xf900 && r <= 0xfaff, // CJK compatibility ideographs
		r >= 0xfe30 && r <= 0xfe4f, // CJK compatibility forms
		r >= 0xff00 && r <= 0xff60, // fullwidth forms
		r >= 0xffe0 && r <= 0xffe6,
		r >= 0x1f300 && r <= 0x1faff, // pictographs
		r >= 0x20000 && r <= 0x3fffd: // CJK extensions
		return 2
	}
	if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Cf, r) {
		return 0
	}
	return 1
}

// Width returns the number of terminal columns a string takes. The string
// must not hold escape sequences: measure first, colour afterwards.
func Width(s string) int {
	n := 0
	for _, r := range s {
		n += runeWidth(r)
	}
	return n
}

// padRight fills s with spaces on the right up to w columns.
func padRight(s string, w int) string {
	if n := Width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// padLeft fills s with spaces on the left up to w columns.
func padLeft(s string, w int) string {
	if n := Width(s); n < w {
		return strings.Repeat(" ", w-n) + s
	}
	return s
}

// truncate cuts s to at most w columns, ending in the ellipsis when
// something was cut.
func truncate(s string, w int, ellipsis string) string {
	if Width(s) <= w {
		return s
	}
	ew := Width(ellipsis)
	if w <= ew {
		return strings.Repeat(".", max(w, 0))
	}
	var b strings.Builder
	n := 0
	for _, r := range s {
		rw := runeWidth(r)
		if n+rw > w-ew {
			break
		}
		b.WriteRune(r)
		n += rw
	}
	return strings.TrimRight(b.String(), " ") + ellipsis
}

// wrap breaks text into lines of at most w columns. It breaks at spaces
// only, which is right for English words and for Korean eojeol alike; a
// word longer than the line is put on a line of its own and left whole,
// so that an address or a path is never cut.
func wrap(text string, w int) []string {
	if w < 1 {
		w = 1
	}
	var lines []string
	for _, para := range strings.Split(text, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			lines = append(lines, "")
			continue
		}
		line, n := words[0], Width(words[0])
		for _, word := range words[1:] {
			ww := Width(word)
			if n+1+ww > w {
				lines = append(lines, line)
				line, n = word, ww
				continue
			}
			line += " " + word
			n += 1 + ww
		}
		lines = append(lines, line)
	}
	return lines
}

// flow joins fields with a gap into lines of at most w columns, breaking
// between fields only.
func flow(fields []string, gap string, w int) []string {
	var lines []string
	line, n := "", 0
	gw := Width(gap)
	for _, f := range fields {
		if f == "" {
			continue
		}
		fw := Width(f)
		switch {
		case line == "":
			line, n = f, fw
		case n+gw+fw > w:
			lines = append(lines, line)
			line, n = f, fw
		default:
			line += gap + f
			n += gw + fw
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// spark draws values as a sparkline, one glyph per value, scaled to the
// largest value. Zero is drawn with the lowest glyph and anything above
// zero with at least the second, so that one error is never mistaken for
// none.
func (st Style) spark(values []float64) string {
	g := st.g()
	var top float64
	for _, v := range values {
		top = max(top, v)
	}
	var b strings.Builder
	for _, v := range values {
		level := 0
		if v > 0 && top > 0 {
			level = 1 + int(v/top*float64(len(g.spark)-2)+0.5)
			level = min(level, len(g.spark)-1)
		}
		b.WriteString(g.spark[level])
	}
	return b.String()
}
