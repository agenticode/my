package spd

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// Bounds of the inputs.
const (
	// MinBinary and MaxBinary bound a binary file taken for an SPD.
	MinBinary = 128
	MaxBinary = 4096
	// MaxText bounds a text taken for an SPD: hex text or decode-dimms
	// output.
	MaxText = 64 << 10
)

// Forms of an input.
const (
	FormBinary      = "binary"
	FormHex         = "hex"
	FormDecodeDimms = "decode-dimms"
)

// Layouts of hex text.
const (
	// LayoutBytes: rows of an offset and two-digit bytes.
	LayoutBytes = "bytes"
	// LayoutWords: rows of an offset and four-digit groups, the two bytes
	// of a group in file order (xxd).
	LayoutWords = "words"
	// LayoutWordsSwapped: the same with the two bytes of a group swapped,
	// as hexdump prints them on a little-endian machine.
	LayoutWordsSwapped = "words-swapped"
	// LayoutPlain: two-digit bytes or runs of hex digits without offsets,
	// or a table whose first column counts the offsets.
	LayoutPlain = "plain"
)

// Input is an SPD as a file gave it.
type Input struct {
	Form string `json:"form"`
	// Bytes are the bytes of a binary file or of hex text; nil for a
	// decode-dimms text.
	Bytes []byte `json:"-"`
	// Layout is the layout of hex text.
	Layout string `json:"layout,omitzero"`
	// Gap is the first offset hex text has no byte for, when rows are
	// missing; the bytes end there. -1 when there is none.
	Gap int `json:"gap"`
	// Back is the offset of a row that goes back to an earlier offset (a
	// second dump in the same text); the bytes end before it. -1 when
	// there is none.
	Back int `json:"back"`
	// Gzip is set when the file was compressed.
	Gzip bool `json:"gzip,omitzero"`
	// Modules are the modules of a decode-dimms text.
	Modules []DimmsModule `json:"modules,omitzero"`
}

// NotSPD says that an input is no SPD in a form this package reads, and
// what it looks like.
type NotSPD struct {
	// What is one of the Looks codes.
	What string
	// N is a size in bytes, Byte the value of byte 2, where they apply.
	N    int
	Byte int
}

// What an input that is no SPD looks like.
const (
	LooksEmpty      = "empty"
	LooksBigText    = "text-too-large"
	LooksText       = "text"
	LooksHTML       = "decode-dimms-html"
	LooksSideBySide = "decode-dimms-side-by-side"
	LooksNoModule   = "decode-dimms-empty"
	LooksSmall      = "binary-too-small"
	LooksBig        = "binary-too-large"
	LooksBlank      = "blank"
	LooksNoType     = "no-memory-type"
	LooksHexNoType  = "hex-no-memory-type"
	LooksHexBig     = "hex-too-large"
	LooksHexNoStart = "hex-no-start"
	LooksGzip       = "gzip-unreadable"
)

func (e *NotSPD) Error() string {
	switch e.What {
	case LooksEmpty:
		return "an empty file"
	case LooksBigText:
		return fmt.Sprintf("a text of more than %d bytes", MaxText)
	case LooksText:
		return "a text without hex rows or decode-dimms output"
	case LooksHTML:
		return "decode-dimms output in HTML, which is not read"
	case LooksSideBySide:
		return "decode-dimms output side by side, which is not read"
	case LooksNoModule:
		return "decode-dimms output without a module"
	case LooksSmall:
		return fmt.Sprintf("%d bytes of binary data, fewer than %d", e.N, MinBinary)
	case LooksBig:
		return fmt.Sprintf("%d bytes of binary data, more than %d", e.N, MaxBinary)
	case LooksBlank:
		return fmt.Sprintf("%d bytes of 0x%02X", e.N, e.Byte)
	case LooksNoType:
		return fmt.Sprintf("%d bytes whose byte 2 (0x%02X) names no memory type", e.N, e.Byte)
	case LooksHexNoType:
		return fmt.Sprintf("hex text of %d bytes whose byte 2 (0x%02X) names no memory type", e.N, e.Byte)
	case LooksHexBig:
		return fmt.Sprintf("hex text of more than %d bytes", MaxBinary)
	case LooksHexNoStart:
		return "hex text whose first row is not at offset 0"
	case LooksGzip:
		return "a gzip file that could not be read"
	}
	return "no SPD"
}

// IsText reports whether b is text: UTF-8 without control characters
// other than tab, carriage return and line feed.
func IsText(b []byte) bool {
	if !utf8.Valid(b) {
		return false
	}
	for _, c := range b {
		if c < 0x20 && c != '\t' && c != '\n' && c != '\r' || c == 0x7F {
			return false
		}
	}
	return true
}

// Parse recognises the form of an input and reads it. A binary file is
// taken when it holds 128 to 4,096 bytes and byte 2 names a memory type;
// hex text when byte 2 of its bytes names one. A gzip file is read through.
// The error of an input that is no SPD is a *NotSPD.
func Parse(data []byte) (*Input, error) {
	gz := false
	if len(data) >= 2 && data[0] == 0x1F && data[1] == 0x8B {
		r, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, &NotSPD{What: LooksGzip}
		}
		b, err := io.ReadAll(io.LimitReader(r, MaxText+1))
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, &NotSPD{What: LooksGzip}
		}
		data, gz = b, true
	}
	in, err := parse(data)
	if in != nil {
		in.Gzip = gz
	}
	return in, err
}

func parse(data []byte) (*Input, error) {
	if len(data) == 0 {
		return nil, &NotSPD{What: LooksEmpty}
	}
	if IsText(data) {
		if len(data) > MaxText {
			return nil, &NotSPD{What: LooksBigText}
		}
		text := string(data)
		if IsDecodeDimms(text) {
			return readDimmsInput(text)
		}
		in, err := ReadHex(text)
		if err != nil {
			return nil, err
		}
		if len(in.Bytes) == 0 && in.Gap == 0 {
			return nil, &NotSPD{What: LooksHexNoStart}
		}
		if len(in.Bytes) < 3 || MemoryType(in.Bytes[2]) == "" {
			b := -1
			if len(in.Bytes) >= 3 {
				b = int(in.Bytes[2])
			}
			return nil, &NotSPD{What: LooksHexNoType, N: len(in.Bytes), Byte: b}
		}
		return in, nil
	}
	n := len(data)
	switch {
	case n < MinBinary:
		return nil, &NotSPD{What: LooksSmall, N: n}
	case n > MaxBinary:
		return nil, &NotSPD{What: LooksBig, N: n}
	case blank(data) && allSame(data):
		return nil, &NotSPD{What: LooksBlank, N: n, Byte: int(data[0])}
	case MemoryType(data[2]) == "":
		return nil, &NotSPD{What: LooksNoType, N: n, Byte: int(data[2])}
	}
	return &Input{Form: FormBinary, Bytes: bytes.Clone(data), Gap: -1, Back: -1}, nil
}

func allSame(b []byte) bool {
	for _, c := range b {
		if c != b[0] {
			return false
		}
	}
	return true
}

func readDimmsInput(text string) (*Input, error) {
	if strings.Contains(strings.ToLower(text), "<html") || strings.Contains(text, "<table") {
		return nil, &NotSPD{What: LooksHTML}
	}
	mods, err := ReadDecodeDimms(text)
	if err != nil {
		return nil, err
	}
	return &Input{Form: FormDecodeDimms, Modules: mods, Gap: -1, Back: -1}, nil
}

// isHex reports whether s is all hex digits.
func isHex(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func hexVal(s string) int {
	v := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			v = v<<4 | int(c-'0')
		case c >= 'a' && c <= 'f':
			v = v<<4 | int(c-'a'+10)
		default:
			v = v<<4 | int(c-'A'+10)
		}
	}
	return v
}

// row is one line of hex text read as an offset and the values after it.
type row struct {
	off   int
	vals  []string // two-digit bytes or four-digit groups, as written
	words bool
}

// offsetField reads an offset token: 2 to 8 hex digits with a colon, or 4
// to 8 without one.
func offsetField(tok string) (int, bool) {
	if strings.HasSuffix(tok, ":") {
		h := tok[:len(tok)-1]
		if len(h) >= 2 && len(h) <= 8 && isHex(h) {
			return hexVal(h), true
		}
		return 0, false
	}
	if len(tok) >= 4 && len(tok) <= 8 && isHex(tok) {
		return hexVal(tok), true
	}
	return 0, false
}

// parseRow reads a line as an offset and up to 16 bytes or 8 groups. The
// values end at the first token that is neither, at a "|" (the ASCII
// column of hexdump -C) or after 16 bytes (i2cdump, xxd).
func parseRow(fields []string, form string) (row, bool) {
	if len(fields) == 0 {
		return row{}, false
	}
	off, ok := offsetField(fields[0])
	if !ok {
		return row{}, false
	}
	r := row{off: off}
	n := 0
	for _, t := range fields[1:] {
		if strings.HasPrefix(t, "|") {
			break
		}
		switch {
		case len(t) == 2 && isHex(t) && form != LayoutWords && !r.words:
			r.vals = append(r.vals, t)
			n++
		case len(t) == 4 && isHex(t) && form != LayoutBytes && (len(r.vals) == 0 || r.words):
			r.vals = append(r.vals, t)
			r.words = true
			n += 2
		default:
			return r, len(r.vals) > 0
		}
		if n >= 16 {
			break
		}
	}
	return r, len(r.vals) > 0
}

// endLine reads a line that holds an offset alone: the closing line of
// hexdump and od.
func endLine(fields []string) (int, bool) {
	if len(fields) != 1 {
		return 0, false
	}
	return offsetField(fields[0])
}

// ReadHex reads hex text: rows of an offset and bytes or four-digit
// groups (hexdump -C, hexdump, od, xxd, i2cdump, the dumps of the illumos
// tests), or plain hex without offsets. Lines that start with "#" and
// lines before the first row are skipped; "*" repeats the row before it.
// The bytes end at the first offset the rows leave out (Gap) or at a row
// that goes back (Back).
func ReadHex(text string) (*Input, error) {
	if len(text) > MaxText {
		return nil, &NotSPD{What: LooksBigText}
	}
	in := &Input{Form: FormHex, Gap: -1, Back: -1}
	var buf []string // values in file order
	form := ""       // LayoutBytes or LayoutWords once the first row is read
	next := 0        // offset after the last row
	var last *row    // the row before a "*"
	repeat := false
	done := false // a gap or a row going back ended the bytes
	var plain [][]string
	saw := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		if done {
			continue
		}
		if s == "*" {
			if saw {
				repeat = true
			}
			continue
		}
		fields := strings.Fields(s)
		if saw {
			if end, ok := endLine(fields); ok {
				if repeat && last != nil {
					if fillRepeat(&buf, last, next, end, form) {
						return nil, &NotSPD{What: LooksHexBig}
					}
					next = end
				}
				repeat = false
				continue
			}
		}
		r, ok := pickRow(fields, form, next, saw)
		if !ok {
			if !saw {
				if p, ok := plainRow(fields); ok {
					plain = append(plain, p)
				}
			}
			continue
		}
		if !saw {
			saw = true
			form = LayoutBytes
			if r.words {
				form = LayoutWords
			}
			if r.off != 0 {
				// The first row does not start at 0: nothing before it.
				in.Gap = 0
				done = true
				continue
			}
		}
		if repeat && last != nil && r.off > next {
			if fillRepeat(&buf, last, next, r.off, form) {
				return nil, &NotSPD{What: LooksHexBig}
			}
			next = r.off
		}
		repeat = false
		switch {
		case r.off > next:
			in.Gap = next
			done = true
			continue
		case r.off < next:
			in.Back = r.off
			done = true
			continue
		}
		width := len(r.vals)
		if r.words {
			width *= 2
		}
		if r.off+width > MaxBinary {
			return nil, &NotSPD{What: LooksHexBig}
		}
		buf = append(buf, r.vals...)
		next = r.off + width
		rr := r
		last = &rr
	}
	if !saw {
		return readPlain(in, plain)
	}
	if form == LayoutBytes {
		in.Layout = LayoutBytes
		in.Bytes = make([]byte, len(buf))
		for i, v := range buf {
			in.Bytes[i] = byte(hexVal(v))
		}
		return in, nil
	}
	in.Bytes, in.Layout = pickOrder(buf)
	return in, nil
}

// pickRow reads a line as a row. A line that starts with "0000 " is read
// with and without that prefix (decode-dimms allows it for eeprog): of the
// readings whose offset can follow the rows before, the one with more
// bytes is taken.
func pickRow(fields []string, form string, next int, saw bool) (row, bool) {
	r, ok := parseRow(fields, form)
	if len(fields) < 2 || fields[0] != "0000" {
		return r, ok
	}
	r2, ok2 := parseRow(fields[1:], form)
	fits := func(x row) bool { return !saw && x.off == 0 || saw && x.off >= next }
	size := func(x row) int {
		if x.words {
			return 2 * len(x.vals)
		}
		return len(x.vals)
	}
	switch {
	case ok && fits(r) && ok2 && fits(r2):
		if size(r2) > size(r) {
			return r2, true
		}
		return r, true
	case ok && fits(r):
		return r, true
	case ok2 && fits(r2):
		return r2, true
	case ok:
		return r, true
	}
	return r2, ok2
}

// fillRepeat repeats the row last from offset from up to offset to. It
// reports whether the bytes would pass MaxBinary.
func fillRepeat(buf *[]string, last *row, from, to int, form string) bool {
	if to > MaxBinary {
		return true
	}
	vals := last.vals
	if form == LayoutWords {
		// Repeat in bytes of the groups, kept as groups: a repeated row is
		// whole groups.
		for a := from; a < to; a += 2 * len(vals) {
			for i, v := range vals {
				if a+2*i >= to {
					break
				}
				*buf = append(*buf, v)
			}
		}
		return false
	}
	for a := from; a < to; a += len(vals) {
		for i, v := range vals {
			if a+i >= to {
				break
			}
			*buf = append(*buf, v)
		}
	}
	return false
}

// plainRow reads a line of plain hex: two-digit bytes, or one run of hex
// digits of even length.
func plainRow(fields []string) ([]string, bool) {
	if len(fields) == 1 && len(fields[0]) > 2 && len(fields[0])%2 == 0 && isHex(fields[0]) {
		var out []string
		for i := 0; i < len(fields[0]); i += 2 {
			out = append(out, fields[0][i:i+2])
		}
		return out, true
	}
	for _, t := range fields {
		if len(t) != 2 || !isHex(t) {
			return nil, false
		}
	}
	return fields, true
}

// readPlain makes the bytes of plain hex. A table whose lines hold 17
// two-digit values and whose first column counts 00, 10, 20 and so on is
// 16 bytes per line with an offset column; a head line 00 01 ... 0F above
// it is skipped.
func readPlain(in *Input, rows [][]string) (*Input, error) {
	if len(rows) == 0 {
		return nil, &NotSPD{What: LooksText}
	}
	if len(rows) > 1 && isColumnHead(rows[0]) {
		rest := rows[1:]
		if table(rest) {
			rows = rest
		}
	}
	in.Layout = LayoutPlain
	if table(rows) {
		for _, r := range rows {
			for _, v := range r[1:] {
				in.Bytes = append(in.Bytes, byte(hexVal(v)))
			}
		}
	} else {
		for _, r := range rows {
			for _, v := range r {
				in.Bytes = append(in.Bytes, byte(hexVal(v)))
			}
		}
	}
	if len(in.Bytes) > MaxBinary {
		return nil, &NotSPD{What: LooksHexBig}
	}
	return in, nil
}

func isColumnHead(r []string) bool {
	if len(r) != 16 {
		return false
	}
	for i, v := range r {
		if hexVal(v) != i {
			return false
		}
	}
	return true
}

func table(rows [][]string) bool {
	if len(rows) < 2 {
		return false
	}
	for i, r := range rows {
		if len(r) != 17 || hexVal(r[0]) != (i*16)&0xFF {
			return false
		}
	}
	return true
}

// pickOrder makes bytes of four-digit groups in both orders and takes the
// one that looks most like an SPD: a decoded memory type with a good base
// CRC, then a decoded memory type, then any memory type at byte 2. File
// order wins a tie.
func pickOrder(groups []string) ([]byte, string) {
	file := make([]byte, 0, 2*len(groups))
	swapped := make([]byte, 0, 2*len(groups))
	for _, g := range groups {
		hi, lo := byte(hexVal(g[:2])), byte(hexVal(g[2:]))
		file = append(file, hi, lo)
		swapped = append(swapped, lo, hi)
	}
	score := func(b []byte) int {
		switch {
		case len(b) < 3 || MemoryType(b[2]) == "":
			return 0
		case baseCRC(b):
			return 3
		case b[2] == KeyDDR4 || b[2] == KeyDDR5:
			return 2
		}
		return 1
	}
	if score(swapped) > score(file) {
		return swapped, LayoutWordsSwapped
	}
	return file, LayoutWords
}
