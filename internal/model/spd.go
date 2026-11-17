package model

import "time"

// SPDReading is the SPD EEPROM of one memory module as MemorY got it: the
// bytes, where they came from and how the read went. The fields are
// decoded from the bytes each time a view is built (package
// internal/decode/spd), so that a corrected decoder reads an older case
// again; the ledger keeps only what was read.
type SPDReading struct {
	ID     int64  `json:"id,omitzero"`
	Host   string `json:"host"`
	ScanID int64  `json:"scan_id,omitzero"`
	// Origin is what was read: the eeprom file of a kernel driver, or the
	// file of a dump.
	Origin string `json:"origin"`
	// Bus and Address name the I2C client of a reading of a host ("i2c-0",
	// 0x50 to 0x57); Driver is the kernel driver bound to it (ee1004,
	// spd5118, at24). All three are empty for a dump file.
	Bus     string `json:"bus,omitzero"`
	Address int    `json:"address,omitzero"`
	Driver  string `json:"driver,omitzero"`
	// Form is SPDFormEEPROM and the others.
	Form string `json:"form"`
	// State is one of the states of package spd: ok, crc, short, blank,
	// type, denied, io, timeout.
	State string `json:"state"`
	// Detail holds facts of the read as key=value words: the errno and the
	// offset of a failed read step, the layout of hex text.
	Detail string `json:"detail,omitzero"`
	// Raw is the bytes read, at most MaxSPDBytes; nil for decode-dimms
	// text, whose fields are in Fields as JSON.
	Raw    []byte `json:"-"`
	Fields string `json:"-"`
	// SHA256 is of Raw, or of the text of decode-dimms output.
	SHA256    string    `json:"sha256"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	// DIMMUID and Tie are set by the join: the DIMM the reading belongs to
	// and the evidence (SPDTieSerial, SPDTieSerialReversed).
	DIMMUID string `json:"dimm_uid,omitzero"`
	Tie     string `json:"tie,omitzero"`
}

// MaxSPDBytes bounds the bytes of one SPD reading.
const MaxSPDBytes = 2048

// Forms of an SPD reading.
const (
	// SPDFormEEPROM: the eeprom (or nvmem) file of a kernel driver on the
	// host the scan ran on, or the copy a collection made of it.
	SPDFormEEPROM = "eeprom"
	// SPDFormBinary, SPDFormHex, SPDFormDecodeDimms: a dump file given to
	// an import.
	SPDFormBinary      = "binary"
	SPDFormHex         = "hex"
	SPDFormDecodeDimms = "decode-dimms"
)

// Ties of an SPD reading to a DIMM.
const (
	// SPDTieSerial: the serial of the SPD equals the SMBIOS serial.
	SPDTieSerial = "serial"
	// SPDTieSerialReversed: they are equal with the four bytes in reverse
	// order.
	SPDTieSerialReversed = "serial-reversed"
)

// ReadingSPDProbe is the kind of the host reading that holds what a scan
// saw of the path to the SPD: buses, controllers, drivers, clients.
const ReadingSPDProbe = "spd-probe"
