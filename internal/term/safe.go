package term

import (
	"io"
	"sync"
	"unicode/utf8"
)

// Safe returns a writer for output that goes to a terminal. What the
// commands print holds text that came from elsewhere: a DIMM label of a
// BMC, a line of a kernel log, a serial number in a case someone sent. A
// terminal acts on control characters in such text: an escape sequence
// can move the cursor, clear the screen, change the title of the window
// or put text into the clipboard.
//
// The writer lets through printable text, line breaks, tabs and the
// colour codes of this package. It writes every other control character
// in caret notation ("^[" for the escape character), and a question mark
// for the control characters above ASCII, for U+2028 and U+2029, and for
// the characters that override the writing direction. Bytes that are no
// UTF-8 go through as they are: a terminal shows them as a replacement
// mark.
//
// Output that goes to a file or a pipe is not text for a terminal and is
// not passed through this writer.
func Safe(w io.Writer) io.Writer { return &safe{w: w} }

type safe struct {
	// mu makes writes from several goroutines (the log of "my serve")
	// come out whole.
	mu sync.Mutex
	w  io.Writer
	// held is the start of a sequence that the end of a write cut in two:
	// a colour code or a character of several bytes.
	held []byte
	out  []byte
}

// colourCode returns the length of the colour code p starts with: the
// escape character, "[", numbers this package uses separated by ";", and
// "m". It returns 0 when p starts with something else, and -1 when p is
// the start of such a code and ends before it is complete.
func colourCode(p []byte) int {
	if len(p) < 2 {
		return -1
	}
	if p[1] != '[' {
		return 0
	}
	n, digits := 0, 0
	for i := 2; i < len(p); i++ {
		c := p[i]
		switch {
		case c >= '0' && c <= '9':
			n = n*10 + int(c-'0')
			if digits++; digits > 2 {
				return 0
			}
		case c == ';' || c == 'm':
			if digits == 0 || !ownColour(n) {
				return 0
			}
			if c == 'm' {
				return i + 1
			}
			n, digits = 0, 0
		default:
			return 0
		}
		if i > 12 {
			return 0
		}
	}
	return -1
}

// ownColour reports whether n is a number of the colour codes of this
// package: reset, bold, dim and the six colours.
func ownColour(n int) bool {
	switch n {
	case 0, 1, 2, 31, 32, 33, 35, 36:
		return true
	}
	return false
}

// unsafeRune reports whether a character above ASCII changes what a
// terminal shows beyond its own place.
func unsafeRune(r rune) bool {
	switch {
	case r >= 0x80 && r < 0xa0:
		return true
	case r == 0x2028, r == 0x2029:
		return true
	case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
		return true
	}
	return false
}

func (s *safe) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(p)
	if len(s.held) > 0 {
		p = append(s.held, p...)
		s.held = nil
	}
	out := s.out[:0]
	for i := 0; i < len(p); {
		c := p[i]
		switch {
		case c == '\n' || c == '\t' || (c >= 0x20 && c < 0x7f):
			out = append(out, c)
			i++
		case c == 0x1b:
			switch l := colourCode(p[i:]); {
			case l > 0:
				out = append(out, p[i:i+l]...)
				i += l
			case l < 0:
				// The rest of the code may come with the next write.
				s.held = append(s.held, p[i:]...)
				i = len(p)
			default:
				out = append(out, '^', '[')
				i++
			}
		case c < 0x20:
			out = append(out, '^', c+'@')
			i++
		case c == 0x7f:
			out = append(out, '^', '?')
			i++
		default:
			if !utf8.FullRune(p[i:]) && len(p)-i < utf8.UTFMax {
				s.held = append(s.held, p[i:]...)
				i = len(p)
				break
			}
			r, size := utf8.DecodeRune(p[i:])
			if r != utf8.RuneError && unsafeRune(r) {
				out = append(out, '?')
			} else {
				out = append(out, p[i:i+size]...)
			}
			i += size
		}
	}
	s.out = out
	if _, err := s.w.Write(out); err != nil {
		return 0, err
	}
	return n, nil
}
