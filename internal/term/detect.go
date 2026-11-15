package term

import (
	"strconv"
	"strings"
)

// Colour modes of Detect.
const (
	ColorAuto   = "auto"
	ColorAlways = "always"
	ColorNever  = "never"
)

// Detect fills in what a style takes from the surroundings.
//
// Colour: with ColorAuto, colours are used on a terminal unless NO_COLOR
// is set to anything or TERM is "dumb". ColorAlways and ColorNever decide
// by themselves.
//
// Width: the width asked for, else the width of the terminal, else the
// COLUMNS variable, else DefaultWidth.
//
// Glyphs: ASCII when asked for, or when the locale names a character set
// other than UTF-8. A locale that is not set at all is taken as UTF-8.
func Detect(st Style, color string, terminal bool, columns int, getenv func(string) string) Style {
	switch color {
	case ColorAlways:
		st.Color = true
	case ColorNever:
		st.Color = false
	default:
		st.Color = terminal && getenv("NO_COLOR") == "" && getenv("TERM") != "dumb"
	}
	if st.Width == 0 {
		switch {
		case terminal && columns > 0:
			st.Width = columns
		default:
			if n, err := strconv.Atoi(strings.TrimSpace(getenv("COLUMNS"))); err == nil && n > 0 {
				st.Width = n
			}
		}
	}
	if !st.ASCII {
		st.ASCII = !utf8Locale(getenv)
	}
	return st
}

// utf8Locale reports whether the locale lets the terminal show UTF-8. The
// first of LC_ALL, LC_CTYPE and LANG that is set decides.
func utf8Locale(getenv func(string) string) bool {
	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		v := strings.ToLower(getenv(name))
		if v == "" {
			continue
		}
		return strings.Contains(v, "utf-8") || strings.Contains(v, "utf8")
	}
	return true
}
