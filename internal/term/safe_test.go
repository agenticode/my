package term

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func safeText(parts ...string) string {
	var b bytes.Buffer
	w := Safe(&b)
	for _, p := range parts {
		if n, err := w.Write([]byte(p)); err != nil || n != len(p) {
			panic("write failed")
		}
	}
	return b.String()
}

// Text for a terminal: what the figures are made of goes through, what a
// terminal would act on is shown and not obeyed.
func TestSafe(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"plain text and line breaks", "CPU0_C1  row 6703\n\tnext\n", "CPU0_C1  row 6703\n\tnext\n"},
		{"Korean and box drawing", "판정: 교체 권고 ▒▓█ │└┴ →\n", "판정: 교체 권고 ▒▓█ │└┴ →\n"},
		{"the colour codes of this package", cBoldRed + "replace now" + cReset + cDim + " x" + cReset + cBoldMag + cCyan + cGreen + cYellow + cMagenta + cRed + cBold,
			cBoldRed + "replace now" + cReset + cDim + " x" + cReset + cBoldMag + cCyan + cGreen + cYellow + cMagenta + cRed + cBold},
		{"clear screen", "DIMM\x1b[2J\x1b[H", "DIMM^[[2J^[[H"},
		{"window title", "\x1b]0;owned\x07", "^[]0;owned^G"},
		{"clipboard", "\x1b]52;c;ZWNobyBoaQ==\x1b\\", "^[]52;c;ZWNobyBoaQ==^[\\"},
		{"a colour this package does not use", "\x1b[8mhidden\x1b[0m", "^[[8mhidden\x1b[0m"},
		{"a colour code with a foreign part", "\x1b[1;8m", "^[[1;8m"},
		{"256 colours", "\x1b[38;5;196m", "^[[38;5;196m"},
		{"reset without a number", "\x1b[m", "^[[m"},
		{"carriage return and backspace", "ok\rFAILED\b\b", "ok^MFAILED^H^H"},
		{"bell, null, delete", "a\x07b\x00c\x7f", "a^Gb^@c^?"},
		{"control characters above ASCII", "a\u009b2Jb\u0085c", "a?2Jb?c"},
		{"overrides of the writing direction", "ok ‮deliaf‬ ⁦x⁩", "ok ?deliaf? ?x?"},
		{"line and paragraph separators", "a b c", "a?b?c"},
		{"bytes that are no UTF-8", "a\xffb\xc3", "a\xffb"},
		{"the escape character at the end", "x\x1b", "x"},
	} {
		if got := safeText(tc.in); got != tc.want {
			t.Errorf("%s: %q became %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

// A write can end in the middle of a colour code or of a character.
func TestSafeAcrossWrites(t *testing.T) {
	text := cBoldRed + "교체 권고" + cReset + " \x1b[2J 판정\n"
	want := cBoldRed + "교체 권고" + cReset + " ^[[2J 판정\n"
	for cut := 0; cut <= len(text); cut++ {
		if got := safeText(text[:cut], text[cut:]); got != want {
			t.Errorf("cut at %d: %q", cut, got)
		}
	}
	// Byte by byte.
	var parts []string
	for i := range len(text) {
		parts = append(parts, text[i:i+1])
	}
	if got := safeText(parts...); got != want {
		t.Errorf("byte by byte: %q", got)
	}
	// What looks like the start of a colour code and goes on as something
	// else is shown.
	if got := safeText("\x1b[3", "1", ";", "4m"); got != "^[[31;4m" {
		t.Errorf("a code that turns out foreign: %q", got)
	}
}

// The figures of this package come through unchanged, with and without
// colour: the writer takes nothing out of them.
func TestSafeLeavesTheFiguresAlone(t *testing.T) {
	for _, s := range []string{cReset, cBold, cDim, cRed, cGreen, cYellow, cMagenta, cCyan, cBoldRed, cBoldMag} {
		if l := colourCode([]byte(s)); l != len(s) {
			t.Errorf("colour code %q is not taken for one (%d)", s, l)
		}
	}
	for _, g := range []glyphs{unicodeGlyphs, asciiGlyphs} {
		all := strings.Join(append(append(g.shade[:], g.spark[:]...), g.hold, g.ue, g.bar, g.barDim, g.sep, g.up, g.flat, g.down, g.unknown,
			g.note, g.caret, g.gap, g.vert, g.corner, g.joint, g.cross, g.horiz, g.ellipsis, g.arrow), " ")
		if got := safeText(all); got != all {
			t.Errorf("glyphs %q became %q", all, got)
		}
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestSafeHandsOnTheErrorOfItsWriter(t *testing.T) {
	if n, err := Safe(brokenWriter{}).Write([]byte("x")); err == nil || n != 0 {
		t.Errorf("%d, %v", n, err)
	}
}

func FuzzSafe(f *testing.F) {
	f.Add("DIMM\x1b[2J", "\x1b[31mred\x1b[0m")
	f.Add("\x1b", "[31m")
	f.Add("\xe2\x80", "\xae")
	f.Add("a‮b", "\u009b")
	f.Fuzz(func(t *testing.T, a, b string) {
		out := safeText(a, b)
		// What comes out holds no control character but line breaks,
		// tabs and the escape characters of the colour codes of this
		// package, and it is the same for one write and for two.
		rest := out
		for _, c := range []string{cReset, cBold, cDim, cRed, cGreen, cYellow, cMagenta, cCyan, cBoldRed, cBoldMag} {
			rest = strings.ReplaceAll(rest, c, "")
		}
		for i := 0; i < len(rest); i++ {
			if c := rest[i]; (c < 0x20 && c != '\n' && c != '\t' && c != 0x1b) || c == 0x7f {
				t.Fatalf("control character %#x in %q", c, out)
			}
			if rest[i] == 0x1b && colourCode([]byte(rest[i:])) <= 0 {
				t.Fatalf("an escape character that starts no colour code of this package in %q", out)
			}
		}
		for _, r := range rest {
			if unsafeRune(r) {
				t.Fatalf("%U in %q", r, out)
			}
		}
		if one := safeText(a + b); one != out {
			t.Fatalf("one write gives %q, two give %q", one, out)
		}
	})
}
