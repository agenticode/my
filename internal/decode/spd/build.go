package spd

import (
	"bytes"
	"encoding/hex"
	"fmt"
)

// Change sets bytes of an SPD, starting at Offset.
type Change struct {
	Offset int
	Bytes  []byte
}

// Build returns a copy of base with the changes made and the CRCs of its
// layout written again: DDR4 bytes 126-127 and 254-255, DDR5 bytes
// 510-511, where the copy holds them. Changes beyond the end of base are
// left out. The tests and the demo case make their dumps with it from the
// public dumps.
func Build(base []byte, changes ...Change) []byte {
	b := bytes.Clone(base)
	for _, c := range changes {
		for i, v := range c.Bytes {
			if o := c.Offset + i; o >= 0 && o < len(b) {
				b[o] = v
			}
		}
	}
	WriteCRCs(b)
	return b
}

// WriteCRCs writes the CRCs of the layout of b into b.
func WriteCRCs(b []byte) {
	if len(b) < 3 {
		return
	}
	put := func(from, to, at int) {
		if len(b) >= at+2 {
			c := CRC16(b[from : to+1])
			b[at], b[at+1] = byte(c), byte(c>>8)
		}
	}
	switch b[2] {
	case KeyDDR4:
		put(0, 125, 126)
		put(128, 253, 254)
	case KeyDDR5:
		put(0, 509, 510)
	}
}

// makerBlock is the offset of the manufacturing block of a memory type.
func makerBlock(b []byte) (int, error) {
	if len(b) < 3 {
		return 0, fmt.Errorf("%d bytes hold no memory type", len(b))
	}
	switch b[2] {
	case KeyDDR4:
		return 320, nil
	case KeyDDR5:
		return 512, nil
	}
	return 0, fmt.Errorf("memory type 0x%02X has no manufacturing block this package writes", b[2])
}

// SetSerial returns the change that writes a serial number of 8 hex
// digits into the manufacturing block of b.
func SetSerial(b []byte, serial string) (Change, error) {
	off, err := makerBlock(b)
	if err != nil {
		return Change{}, err
	}
	v, err := hex.DecodeString(serial)
	if err != nil || len(v) != 4 {
		return Change{}, fmt.Errorf("serial %q is no 8 hex digits", serial)
	}
	return Change{Offset: off + 5, Bytes: v}, nil
}

// SetDate returns the change that writes a manufacturing year (2000 to
// 2099) and week (1 to 53) into the manufacturing block of b, as BCD.
func SetDate(b []byte, year, week int) (Change, error) {
	off, err := makerBlock(b)
	if err != nil {
		return Change{}, err
	}
	if year < 2000 || year > 2099 || week < 1 || week > 53 {
		return Change{}, fmt.Errorf("year %d week %d is no date of the SPD", year, week)
	}
	y := year - 2000
	return Change{Offset: off + 3, Bytes: []byte{byte(y/10<<4 | y%10), byte(week/10<<4 | week%10)}}, nil
}
