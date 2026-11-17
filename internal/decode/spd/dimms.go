package spd

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/agenticode/my/internal/model"
)

// The lines decode-dimms (i2c-tools) prints around each module.
const (
	dimmsHead      = "Decoding EEPROM:"
	dimmsSeparator = "---=== SPD EEPROM Information ===---"
	dimmsCount     = "Number of SDRAM DIMMs detected and decoded:"
)

// IsDecodeDimms reports whether a text is decode-dimms output.
func IsDecodeDimms(text string) bool {
	return strings.Contains(text, dimmsSeparator) || strings.Contains(text, "decode-dimms version") && strings.Contains(text, dimmsCount)
}

// DimmsModule is what a decode-dimms text says of one module. It holds no
// bytes: the fields are taken as printed.
type DimmsModule struct {
	// Path is the value of its "Decoding EEPROM" line.
	Path       string `json:"path,omitzero"`
	MemoryType string `json:"memory_type,omitzero"`
	ModuleType string `json:"module_type,omitzero"`
	// CRCs are the checksum lines: the range as printed ("0-125"), OK or
	// not, and the value text.
	CRCs        []DimmsCRC       `json:"crcs,omitzero"`
	SizeBytes   model.Opt[int64] `json:"size_bytes,omitzero"`
	Ranks       model.Opt[int]   `json:"ranks,omitzero"`
	DeviceWidth model.Opt[int]   `json:"device_width,omitzero"`
	SpeedMTs    model.Opt[int]   `json:"speed_mts,omitzero"`
	Maker       string           `json:"maker,omitzero"`
	DRAMMaker   string           `json:"dram_maker,omitzero"`
	Location    string           `json:"location,omitzero"`
	Date        string           `json:"mfg_date,omitzero"`
	Serial      string           `json:"serial,omitzero"`
	PartNumber  string           `json:"part_number,omitzero"`
	Revision    string           `json:"revision,omitzero"`
	// Cut is set when the text ends inside this module.
	Cut bool `json:"cut,omitzero"`
}

// DimmsCRC is one checksum line of decode-dimms.
type DimmsCRC struct {
	Range string `json:"range"`
	OK    bool   `json:"ok"`
	Text  string `json:"text"`
}

var (
	dimmsLabel  = regexp.MustCompile(`^(\S.*?)\s{2,}(\S.*?)\s*$`)
	dimmsCRCRe  = regexp.MustCompile(`^EEPROM (?:CRC|Checksum) of bytes (\d+-\d+)$`)
	dimmsSize   = regexp.MustCompile(`^(\d+) (MB|GB)$`)
	dimmsNumber = regexp.MustCompile(`^(\d+)`)
	dimmsDate   = regexp.MustCompile(`^\d{4}-W\d{2}$`)
	dimmsSerial = regexp.MustCompile(`^0x([0-9A-Fa-f]{8})$`)
)

// ReadDecodeDimms reads the modules of a decode-dimms text in its default
// form (not side by side, not HTML). A module the text ends inside is kept
// and marked cut. The error of a text without a module is a *NotSPD.
func ReadDecodeDimms(text string) ([]DimmsModule, error) {
	if len(text) > MaxText {
		return nil, &NotSPD{What: LooksBigText}
	}
	var out []DimmsModule
	var cur *DimmsModule
	closed := false
	lastLabel := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		s := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(s, dimmsHead):
			rest := strings.TrimSpace(strings.TrimPrefix(s, dimmsHead))
			if strings.Contains(rest, "  ") {
				return nil, &NotSPD{What: LooksSideBySide}
			}
			out = append(out, DimmsModule{Path: rest})
			cur = &out[len(out)-1]
			lastLabel = ""
			continue
		case strings.HasPrefix(s, "Decoding EEPROM") && !strings.HasPrefix(s, dimmsHead):
			return nil, &NotSPD{What: LooksSideBySide}
		case strings.HasPrefix(s, dimmsCount):
			closed = true
			cur = nil
			continue
		case s == dimmsSeparator:
			if cur == nil || cur.MemoryType != "" || len(cur.CRCs) > 0 {
				out = append(out, DimmsModule{})
				cur = &out[len(out)-1]
			}
			lastLabel = ""
			continue
		case strings.HasPrefix(s, "---===") || s == "" || cur == nil:
			continue
		}
		if line != s && len(line)-len(strings.TrimLeft(line, " ")) >= 40 {
			// A value continued on the next line, under its column.
			if lastLabel != "" {
				continueValue(cur, lastLabel, s)
			}
			continue
		}
		m := dimmsLabel.FindStringSubmatch(line)
		if m == nil {
			lastLabel = ""
			continue
		}
		lastLabel = m[1]
		setDimmsField(cur, m[1], m[2])
	}
	if len(out) == 0 {
		return nil, &NotSPD{What: LooksNoModule}
	}
	if !closed {
		out[len(out)-1].Cut = true
	}
	return out, nil
}

func continueValue(m *DimmsModule, label, v string) {
	if dimmsCRCRe.MatchString(label) && len(m.CRCs) > 0 {
		c := &m.CRCs[len(m.CRCs)-1]
		c.Text = strings.TrimSpace(c.Text + " " + v)
	}
}

func setDimmsField(m *DimmsModule, label, v string) {
	if c := dimmsCRCRe.FindStringSubmatch(label); c != nil {
		m.CRCs = append(m.CRCs, DimmsCRC{Range: c[1], OK: strings.HasPrefix(v, "OK"), Text: v})
		return
	}
	switch label {
	case "Fundamental Memory type":
		m.MemoryType = strings.TrimSuffix(v, " SDRAM")
	case "Module Type":
		m.ModuleType = v
	case "Size":
		if s := dimmsSize.FindStringSubmatch(v); s != nil {
			// At most 1 PiB: no module comes near it, and the bytes stay
			// within an int64.
			n, err := strconv.ParseInt(s[1], 10, 64)
			if s[2] == "GB" && n < 1<<30 {
				n <<= 10
			} else if s[2] == "GB" {
				err = strconv.ErrRange
			}
			if err == nil && n <= 1<<30 {
				m.SizeBytes = model.Some(n << 20)
			}
		}
	case "Ranks":
		m.Ranks = number(v)
	case "SDRAM Device Width":
		m.DeviceWidth = number(v)
	case "Maximum module speed":
		m.SpeedMTs = number(v)
	case "Module Manufacturer", "Manufacturer":
		m.Maker = v
	case "DRAM Manufacturer":
		m.DRAMMaker = v
	case "Manufacturing Location Code":
		m.Location = v
	case "Manufacturing Date":
		m.Date = v
	case "Assembly Serial Number":
		if s := dimmsSerial.FindStringSubmatch(v); s != nil {
			m.Serial = strings.ToUpper(s[1])
		} else {
			m.Serial = v
		}
	case "Part Number":
		if v != "Undefined" {
			m.PartNumber = v
		}
	case "Revision Code":
		m.Revision = v
	}
}

func number(v string) model.Opt[int] {
	s := dimmsNumber.FindString(v)
	if s == "" || len(s) > 9 {
		return model.Opt[int]{}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return model.Opt[int]{}
	}
	return model.Some(n)
}

// Decoded returns what the text says of the module in the form of a
// decode of bytes. Maker names are those the text prints; it gives no
// codes. The date is taken only in the form 2021-W43. The state is crc
// when a checksum line is not OK, short when the text ends inside the
// module, type when the memory type is no DDR4 or DDR5, else ok.
func (m DimmsModule) Decoded() Decoded {
	d := Decoded{MemoryType: m.MemoryType, ModuleType: m.ModuleType, SizeBytes: m.SizeBytes, DeviceWidth: m.DeviceWidth,
		PackageRanks: m.Ranks, SpeedMTs: m.SpeedMTs, ModuleMakerName: m.Maker, DRAMMakerName: m.DRAMMaker,
		Location: m.Location, Serial: m.Serial, PartNumber: m.PartNumber, ModuleRevision: m.Revision}
	if dimmsDate.MatchString(m.Date) {
		d.Date = m.Date
	} else if m.Date != "" {
		d.DateBytes = m.Date
		d.Notes = append(d.Notes, NoteNoDate)
	}
	if m.Ranks.Valid && m.DeviceWidth.Valid {
		d.Organisation = strconv.Itoa(m.Ranks.V) + "Rx" + strconv.Itoa(m.DeviceWidth.V)
	}
	d.State = StateOK
	switch {
	case m.Cut && m.MemoryType == "":
		d.State = StateShort
		d.Missing = append(d.Missing, Gap{Part: GapBase})
	case m.MemoryType != "DDR4" && m.MemoryType != "DDR5":
		d.State = StateType
	case len(m.CRCs) > 0 && !m.CRCs[0].OK:
		d.State = StateCRC
	case m.Cut:
		d.State = StateShort
		d.Missing = append(d.Missing, Gap{Part: GapMaker})
	}
	return d
}
