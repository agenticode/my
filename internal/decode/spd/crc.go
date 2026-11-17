package spd

// CRC16 is the CRC of the SPD: polynomial 0x1021, start value 0, no
// reflection, no final XOR. The SPD stores it low byte first.
func CRC16(b []byte) uint16 {
	var crc uint16
	for _, c := range b {
		crc ^= uint16(c) << 8
		for range 8 {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// baseCRC tells whether the bytes hold a memory type that is decoded and
// a good CRC of the first block.
func baseCRC(b []byte) bool {
	if len(b) < 3 {
		return false
	}
	switch b[2] {
	case KeyDDR4:
		return crcOf(b, 0, 125, 126).State == CRCGood
	case KeyDDR5:
		return crcOf(b, 0, 509, 510).State == CRCGood
	}
	return false
}

// GoodBase reports whether b is a DDR4 or DDR5 SPD with a good base CRC.
func GoodBase(b []byte) bool { return baseCRC(b) }
