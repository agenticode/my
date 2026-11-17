package spd

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// makersTSV holds the names of the maker codes that appear on server
// memory modules, with the public tables each row was checked against.
//
//go:embed makers.tsv
var makersTSV string

// Maker is one row of the table of maker names.
type Maker struct {
	Code   string // bank 1, 0xCE
	Name   string
	Source string
}

var makers = sync.OnceValues(func() (map[string]Maker, error) { return parseMakers(makersTSV) })

func parseMakers(text string) (map[string]Maker, error) {
	out := map[string]Maker{}
	head := false
	for i, line := range strings.Split(text, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if !head {
			if strings.Join(f, " ") != "bank code name source" {
				return nil, fmt.Errorf("makers.tsv line %d: header %q", i+1, line)
			}
			head = true
			continue
		}
		if len(f) != 4 {
			return nil, fmt.Errorf("makers.tsv line %d: %d fields", i+1, len(f))
		}
		bank, err := strconv.Atoi(f[0])
		if err != nil || bank < 1 || bank > 128 {
			return nil, fmt.Errorf("makers.tsv line %d: bank %q", i+1, f[0])
		}
		code, err := strconv.ParseUint(strings.TrimPrefix(f[1], "0x"), 16, 8)
		if err != nil || !strings.HasPrefix(f[1], "0x") || len(f[1]) != 4 || f[1] != fmt.Sprintf("0x%02X", code) {
			return nil, fmt.Errorf("makers.tsv line %d: code %q", i+1, f[1])
		}
		if !oddParity(byte(code)) {
			return nil, fmt.Errorf("makers.tsv line %d: code %s has no odd parity", i+1, f[1])
		}
		if f[2] == "" || f[3] == "" {
			return nil, fmt.Errorf("makers.tsv line %d: empty name or source", i+1)
		}
		key := fmt.Sprintf("bank %d, %s", bank, f[1])
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("makers.tsv line %d: %s twice", i+1, key)
		}
		out[key] = Maker{Code: key, Name: f[2], Source: f[3]}
	}
	if !head {
		return nil, fmt.Errorf("makers.tsv: no header")
	}
	return out, nil
}

func oddParity(b byte) bool {
	n := 0
	for ; b != 0; b &= b - 1 {
		n++
	}
	return n%2 == 1
}

// MakerName returns the name of a maker code in the form "bank 1, 0xCE",
// or "" when the table does not name it.
func MakerName(code string) string {
	m, err := makers()
	if err != nil {
		return ""
	}
	return m[code].Name
}

// Makers returns the rows of the table of maker names, for documentation
// and tests.
func Makers() ([]Maker, error) {
	m, err := makers()
	if err != nil {
		return nil, err
	}
	out := make([]Maker, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out, nil
}

// MakerLabel returns a maker code with its name, as "Samsung (JEDEC bank
// 1, 0xCE)", or the code alone when the table does not name it.
func MakerLabel(code string) string {
	if code == "" {
		return ""
	}
	if n := MakerName(code); n != "" {
		return n + " (JEDEC " + code + ")"
	}
	return code
}
