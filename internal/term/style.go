// Package term draws the figures of package view as text for a terminal.
//
// Every figure is complete without colour: a grade is told by the density
// of a glyph as well as by its colour, and with ASCII set the glyphs are
// plain ASCII. Output that does not go to a terminal carries no escape
// sequences. The width of the output follows the terminal.
package term

import (
	"strings"

	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/view"
)

// Style says how to draw.
type Style struct {
	// Lang is the language of labels and column heads.
	Lang i18n.Lang
	// Color adds ANSI colours.
	Color bool
	// ASCII restricts the output to ASCII glyphs. Korean text stays as it
	// is: it is content, not decoration.
	ASCII bool
	// Width is the number of columns to fill; values below MinWidth are
	// raised to it, and 0 means DefaultWidth.
	Width int
	// Detail draws everything a screen has: every statement of a finding
	// with the records behind it, and the notes a short screen counts.
	Detail bool
}

// Widths of the output.
const (
	DefaultWidth = 100
	MinWidth     = 60
	MaxWidth     = 200
)

func (st Style) width() int {
	switch {
	case st.Width == 0:
		return DefaultWidth
	case st.Width < MinWidth:
		return MinWidth
	case st.Width > MaxWidth:
		return MaxWidth
	}
	return st.Width
}

func (st Style) lang() i18n.Lang {
	if st.Lang == "" {
		return i18n.EN
	}
	return st.Lang
}

// say renders a message of the view catalog.
func (st Style) say(code string, kv ...any) string {
	var args map[string]any
	if len(kv) > 0 {
		args = make(map[string]any, len(kv)/2)
		for i := 0; i+1 < len(kv); i += 2 {
			args[kv[i].(string)] = kv[i+1]
		}
	}
	if view.BoardMessages.Has(code) {
		return view.BoardMessages.Text(st.lang(), code, args)
	}
	if view.SPDMessages.Has(code) {
		return view.SPDMessages.Text(st.lang(), code, args)
	}
	return view.Messages.Text(st.lang(), code, args)
}

// glyphs is one set of drawing characters.
type glyphs struct {
	// shade holds the five densities shared by the slot map and the heat
	// map: nothing, light, medium, dark, full.
	shade [5]string
	// spark holds the eight heights of a sparkline. The first stands for
	// zero, so that a day without errors still shows as a day.
	spark    [8]string
	hold     string // slot map: hold replacement
	ue       string // heat map: uncorrected error
	bar      string // topology: filled part of a segment
	barDim   string // topology: branch that is not reported, or folded
	sep      string // topology: segment border
	up       string
	flat     string
	down     string
	unknown  string
	note     string // address map: points at a line
	caret    string // address map: points at a column
	gap      string // address map: rows left out
	vert     string // timeline callouts
	corner   string
	joint    string
	cross    string
	horiz    string
	ellipsis string
	arrow    string // comparison: from one state to the next
}

var unicodeGlyphs = glyphs{
	shade: [5]string{"·", "░", "▒", "▓", "█"},
	spark: [8]string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"},
	hold:  "?", ue: "X", bar: "█", barDim: "░", sep: "|",
	up: "↑", flat: "→", down: "↓", unknown: "?",
	note: "←", caret: "^", gap: "⋮",
	vert: "│", corner: "└", joint: "┴", cross: "┼", horiz: "─", ellipsis: "…", arrow: "→",
}

var asciiGlyphs = glyphs{
	shade: [5]string{".", ":", "+", "*", "#"},
	spark: [8]string{"_", ".", ":", "-", "=", "+", "*", "#"},
	hold:  "?", ue: "X", bar: "#", barDim: ".", sep: "|",
	up: "^", flat: "=", down: "v", unknown: "?",
	note: "<-", caret: "^", gap: ":",
	vert: "|", corner: "+", joint: "+", cross: "+", horiz: "-", ellipsis: "..", arrow: "->",
}

func (st Style) g() *glyphs {
	if st.ASCII {
		return &asciiGlyphs
	}
	return &unicodeGlyphs
}

// ANSI colours. Only the eight basic colours and bold are used: they exist
// on every terminal and follow its colour scheme.
const (
	cReset   = "\x1b[0m"
	cBold    = "\x1b[1m"
	cDim     = "\x1b[2m"
	cRed     = "\x1b[31m"
	cGreen   = "\x1b[32m"
	cYellow  = "\x1b[33m"
	cMagenta = "\x1b[35m"
	cCyan    = "\x1b[36m"
	cBoldRed = "\x1b[1;31m"
	cBoldMag = "\x1b[1;35m"
)

// paint wraps s in a colour when colours are on.
func (st Style) paint(color, s string) string {
	if !st.Color || color == "" || s == "" {
		return s
	}
	return color + s + cReset
}

func (st Style) bold(s string) string { return st.paint(cBold, s) }
func (st Style) dim(s string) string  { return st.paint(cDim, s) }

// gradeColor returns the colour of a grade, or of a slot state.
func gradeColor(state string) string {
	switch model.VerdictGrade(state) {
	case model.VerdictReplaceNow:
		return cBoldRed
	case model.VerdictConsiderReplacement:
		return cYellow
	case model.VerdictObserve:
		return cCyan
	case model.VerdictOK:
		return cGreen
	case model.VerdictHoldReplacement:
		return cBoldMag
	}
	return cDim
}

// gradeGlyph returns the two-column cell of a grade, or of a slot state,
// in the slot map. The density grows with the grade; hold replacement is a
// question, and is drawn as one.
func (st Style) gradeGlyph(state string) string {
	g := st.g()
	level := 0
	switch model.VerdictGrade(state) {
	case model.VerdictOK:
		level = 1
	case model.VerdictObserve:
		level = 2
	case model.VerdictConsiderReplacement:
		level = 3
	case model.VerdictReplaceNow:
		level = 4
	case model.VerdictHoldReplacement:
		return strings.Repeat(g.hold, 2)
	}
	if state == view.SlotUnknown {
		// Not empty and not a module: the ledger does not know.
		return "--"
	}
	return strings.Repeat(g.shade[level], 2)
}

// heatLevel returns the shade of a count on the scale of the heat map.
func heatLevel(n int64, scale []int64) int {
	level := 0
	for i, lo := range scale {
		if n >= lo {
			level = i + 1
		}
	}
	return min(level, 4)
}

// heatColor returns the colour of a shade of the heat map.
func heatColor(level int) string {
	switch level {
	case 0:
		return cDim
	case 1:
		return cCyan
	case 2:
		return cGreen
	case 3:
		return cYellow
	}
	return cRed
}

// Glyphs returns the drawing characters of the style by name, for texts
// that explain a figure: g0 to g4 are the five densities, hold the mark of
// a hold finding, ue the mark of an uncorrected error, up, flat and down
// the trend arrows, arrow the mark between two states of a comparison.
func (st Style) Glyphs() map[string]any {
	g := st.g()
	return map[string]any{
		"g0": g.shade[0], "g1": g.shade[1], "g2": g.shade[2], "g3": g.shade[3], "g4": g.shade[4],
		"hold": g.hold, "ue": g.ue, "up": g.up, "flat": g.flat, "down": g.down, "zero": g.spark[0],
		"arrow": g.arrow,
	}
}

// Wrap breaks text into lines of at most the width of the style less
// indent, and puts indent spaces before each.
func (st Style) Wrap(text string, indent int) []string {
	lines := wrap(text, st.width()-indent)
	for i, l := range lines {
		if l != "" {
			lines[i] = strings.Repeat(" ", indent) + l
		}
	}
	return lines
}
