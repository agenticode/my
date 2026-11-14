// Package edac decodes the text EDAC drivers attach to a memory error: the
// driver detail string, the layer location and the DIMM label.
//
// The same field name does not mean the same thing in every driver. The
// Intel skx and i10nm drivers print row and column in hexadecimal, at first
// without a 0x prefix; GHES prints them in decimal under the same names.
// Reading one as the other moves an error to another row, which turns a
// single-bit fault into a row fault on paper. The decoder therefore picks a
// dialect from the keys present before it reads any number, and leaves a
// string it does not recognise undecoded.
package edac

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/agenticode/my/internal/decode/cper"
	"github.com/agenticode/my/internal/model"
)

// Decoder names, stored in Event.Decoder.
const (
	// DecoderADXL: Intel address translation through the ACPI ADXL method
	// (skx_edac, i10nm_edac). Names are CamelCase, values hexadecimal.
	DecoderADXL = "edac/skx-adxl"
	// DecoderSKX: the skx and i10nm drivers' own decode, without ADXL.
	DecoderSKX = "edac/skx"
	// DecoderSB: sb_edac (Sandy Bridge to Broadwell). Socket, home agent
	// and rank; no row or column.
	DecoderSB = "edac/sb"
	// DecoderGHES: ghes_edac, which repeats the firmware's CPER fields.
	DecoderGHES = "edac/ghes"
	// DecoderCSROW: a chip-select based driver such as amd64_edac. The
	// layers give channel, DIMM and rank; there is no detail string.
	DecoderCSROW = "edac/csrow"
)

// Detail is what the text of one EDAC error report yields.
type Detail struct {
	// Decoder is the dialect that was recognised, or "" when none was.
	Decoder string

	Socket    model.Opt[int]
	IMC       model.Opt[int]
	Channel   model.Opt[int]
	Slot      model.Opt[int]
	Rank      model.Opt[int]
	Device    model.Opt[int]
	BankGroup model.Opt[int]
	Bank      model.Opt[int]
	Row       model.Opt[int]
	Column    model.Opt[int]

	BitPosition   model.Opt[int]
	SystemAddress model.Opt[uint64]
	SMBIOSHandle  model.Opt[int]
	// DIMMLocation is the DMI locator text of a GHES report.
	DIMMLocation string

	// Overflow is set when the machine check bank had overflowed: more
	// errors happened than were logged.
	Overflow bool
	// Recoverable is set when the driver marked an uncorrected error as
	// recoverable.
	Recoverable bool
	// MSCOD and MCACOD are the model specific and the architectural error
	// code of the machine check, from err_code.
	MSCOD  model.Opt[int]
	MCACOD model.Opt[int]
}

var (
	errCodeRE = regexp.MustCompile(`\berr_code:(?:0x)?([0-9a-fA-F]{1,4}):(?:0x)?([0-9a-fA-F]{1,4})\b`)
	// name:0xHEX pairs of the ADXL dialect.
	camelRE = regexp.MustCompile(`\b([A-Z][A-Za-z]+):0x([0-9a-fA-F]+)\b`)
	// key:value pairs of the older skx and sb dialects.
	lowerRE = regexp.MustCompile(`\b(socket|imc|ha|rank|bg|ba|row|col|channel_mask):((?:0x)?[0-9a-fA-F]+)\b`)
)

// ParseDetail decodes a driver detail string (the driver_detail column of
// rasdaemon, or the text after " - " in a kernel log line).
func ParseDetail(detail string) Detail {
	var d Detail
	if strings.TrimSpace(detail) == "" {
		return d
	}
	d.Overflow = strings.Contains(detail, "OVERFLOW")
	d.Recoverable = strings.Contains(detail, " recoverable")
	if m := errCodeRE.FindStringSubmatch(detail); m != nil {
		if v, err := strconv.ParseUint(m[1], 16, 16); err == nil {
			d.MSCOD = model.Some(int(v))
		}
		if v, err := strconv.ParseUint(m[2], 16, 16); err == nil {
			d.MCACOD = model.Some(int(v))
		}
	}

	switch {
	case strings.Contains(detail, "APEI location:"):
		d.parseGHES(detail)
	case strings.Contains(detail, "ProcessorSocketId:") || strings.Contains(detail, "MemoryControllerId:") ||
		strings.Contains(detail, "SystemAddress:"):
		d.parseCamel(detail)
	case strings.Contains(detail, "socket:") && strings.Contains(detail, "imc:"):
		d.parseSKX(detail)
	case strings.Contains(detail, "socket:") && (strings.Contains(detail, "channel_mask:") || strings.Contains(detail, "ha:")):
		d.parseSB(detail)
	}
	return d
}

func (d *Detail) parseGHES(detail string) {
	m, ok := cper.ParseText(detail)
	if !ok {
		return
	}
	d.Decoder = DecoderGHES
	d.Socket, d.Channel, d.Slot = m.Node, m.Card, m.Module
	d.Rank, d.Device, d.BankGroup, d.Bank = m.Rank, m.Device, m.BankGroup, m.Bank
	d.Row, d.Column, d.BitPosition = m.Row, m.Column, m.BitPosition
	d.SMBIOSHandle, d.DIMMLocation = m.ModuleHandle, m.DIMMLocation
}

func (d *Detail) parseCamel(detail string) {
	vals := map[string]uint64{}
	for _, kv := range camelRE.FindAllStringSubmatch(detail, -1) {
		if v, err := strconv.ParseUint(kv[2], 16, 64); err == nil {
			if _, dup := vals[kv[1]]; !dup {
				vals[kv[1]] = v
			}
		}
	}
	if len(vals) == 0 {
		return
	}
	get := func(name string) model.Opt[int] {
		if v, ok := vals[name]; ok && v <= 1<<31 {
			return model.Some(int(v))
		}
		return model.Opt[int]{}
	}
	d.Decoder = DecoderSKX
	if _, ok := vals["SystemAddress"]; ok {
		d.Decoder = DecoderADXL
		d.SystemAddress = model.Some(vals["SystemAddress"])
	} else if _, ok := vals["ChannelId"]; ok {
		d.Decoder = DecoderADXL
	}
	d.Socket = get("ProcessorSocketId")
	d.IMC = get("MemoryControllerId")
	d.Channel = get("ChannelId")
	d.Slot = get("DimmSlotId")
	// DimmRankId counts ranks within the DIMM. Without it the driver or
	// firmware numbering of PhysicalRankId is kept as it is.
	d.Rank = get("DimmRankId")
	if !d.Rank.Valid {
		d.Rank = get("PhysicalRankId")
	}
	d.Row, d.Column = get("Row"), get("Column")
	d.Bank, d.BankGroup = get("Bank"), get("BankGroup")
}

func (d *Detail) parseSKX(detail string) {
	d.Decoder = DecoderSKX
	for _, kv := range lowerRE.FindAllStringSubmatch(detail, -1) {
		key, val := kv[1], kv[2]
		switch key {
		case "row", "col":
			// Hexadecimal in every version of the driver: "row:%x" up to
			// 5.10, "row:0x%x" after.
			v, err := strconv.ParseUint(strings.TrimPrefix(val, "0x"), 16, 31)
			if err != nil {
				continue
			}
			if key == "row" {
				d.Row = model.Some(int(v))
			} else {
				d.Column = model.Some(int(v))
			}
		default:
			v, err := strconv.ParseUint(val, 10, 31)
			if err != nil {
				continue // "%d" fields never carry a prefix or hex digits
			}
			o := model.Some(int(v))
			switch key {
			case "socket":
				d.Socket = o
			case "imc":
				d.IMC = o
			case "rank":
				d.Rank = o
			case "bg":
				d.BankGroup = o
			case "ba":
				d.Bank = o
			}
		}
	}
}

func (d *Detail) parseSB(detail string) {
	d.Decoder = DecoderSB
	for _, kv := range lowerRE.FindAllStringSubmatch(detail, -1) {
		v, err := strconv.ParseUint(kv[2], 10, 31)
		if err != nil {
			continue
		}
		o := model.Some(int(v))
		switch kv[1] {
		case "socket":
			d.Socket = o
		case "ha":
			d.IMC = o
		case "rank":
			d.Rank = o
		}
	}
}

// Layers is the position of an error in the EDAC layer model: up to three
// indices whose meaning depends on the driver (channel and slot for the
// Intel drivers, chip-select row and channel for amd64_edac).
type Layers struct {
	// Names are the layer names when the source printed them ("channel",
	// "slot", "csrow"), in the order of Index. They are empty for sources
	// that only store the indices.
	Names []string
	Index []int
}

// Key returns the canonical text of the layer indices, for example "0:1".
// It is the form stored in the ledger: every source can produce it, whether
// it knows the layer names or not.
func (l Layers) Key() string {
	if len(l.Index) == 0 {
		return ""
	}
	parts := make([]string, len(l.Index))
	for i, v := range l.Index {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ":")
}

// Named returns the index of the layer with the given name.
func (l Layers) Named(name string) model.Opt[int] {
	for i, n := range l.Names {
		if n == name && i < len(l.Index) {
			return model.Some(l.Index[i])
		}
	}
	return model.Opt[int]{}
}

// LayersFromIndices builds Layers from the three layer columns rasdaemon
// stores, where -1 (or 255, an unsigned reading of -1) marks an unused layer.
func LayersFromIndices(top, middle, lower int) Layers {
	var l Layers
	for _, v := range []int{top, middle, lower} {
		if v < 0 || v == 255 {
			break
		}
		l.Index = append(l.Index, v)
	}
	return l
}

var layerRE = regexp.MustCompile(`\b(branch|channel|slot|csrow|all memory|memory)[: ](\d+)`)

// ParseLayers reads a layer location as the kernel log prints it
// ("channel:0 slot:1") or as sysfs shows it ("channel 0 slot 1").
func ParseLayers(s string) Layers {
	var l Layers
	for _, m := range layerRE.FindAllStringSubmatch(s, -1) {
		v, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		l.Names = append(l.Names, m[1])
		l.Index = append(l.Index, v)
		if len(l.Index) == 3 {
			break
		}
	}
	return l
}

// Label is what a DIMM label of a known default form says about the slot.
type Label struct {
	// Kind is "intel" for CPU_SrcID#s_MC#m_Chan#c_DIMM#d (skx, i10nm),
	// "intel-ha" for CPU_SrcID#s_Ha#h_Chan#c_DIMM#d (sb_edac), "csrow" for
	// mc#m csrow#r channel#c, or "" for a label in no known default form.
	Kind    string
	Socket  model.Opt[int]
	IMC     model.Opt[int]
	Channel model.Opt[int]
	Slot    model.Opt[int]
	Rank    model.Opt[int]
}

var (
	intelLabelRE = regexp.MustCompile(`^CPU_SrcID#(\d+)_(MC|Ha)#(\d+)_Chan#(\d+)_DIMM#(\d+)$`)
	csrowLabelRE = regexp.MustCompile(`^mc#(\d+)csrow#(\d+)channel#(\d+)$`)
)

// ParseLabel reads the slot position out of a default EDAC DIMM label.
// Labels set by the administrator (the silk-screen name) carry no position
// and yield an empty Label.
func ParseLabel(label string) Label {
	label = strings.TrimSpace(label)
	atoi := func(s string) model.Opt[int] {
		v, err := strconv.Atoi(s)
		if err != nil {
			return model.Opt[int]{}
		}
		return model.Some(v)
	}
	if m := intelLabelRE.FindStringSubmatch(label); m != nil {
		l := Label{Kind: "intel", Socket: atoi(m[1]), IMC: atoi(m[3]), Channel: atoi(m[4]), Slot: atoi(m[5])}
		if m[2] == "Ha" {
			l.Kind = "intel-ha"
		}
		return l
	}
	if m := csrowLabelRE.FindStringSubmatch(label); m != nil {
		l := Label{Kind: "csrow", Channel: atoi(m[3])}
		if cs := atoi(m[2]); cs.Valid {
			// amd64_edac: two chip selects per DIMM, one per rank.
			l.Slot, l.Rank = model.Some(cs.V>>1), model.Some(cs.V&1)
		}
		return l
	}
	return Label{}
}

// PlaceholderLabel reports whether a label says that EDAC could not tell
// which DIMM the error belongs to.
func PlaceholderLabel(label string) bool {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "", "unknown memory", "unknown label", "any memory", "unknown":
		return true
	}
	return false
}

// Severity maps the EDAC error type (as rasdaemon stores it, or CE/UE from
// the kernel log) and the driver's recoverable mark to a ledger severity.
func Severity(errType string, recoverable bool) (model.Severity, bool) {
	switch strings.ToLower(strings.TrimSpace(errType)) {
	case "corrected", "ce":
		return model.SevCE, true
	case "uncorrected", "ue":
		if recoverable {
			return model.SevRecoverable, true
		}
		return model.SevUE, true
	case "deferred":
		// The error was detected and its consumption deferred; the system
		// kept running.
		return model.SevRecoverable, true
	case "fatal":
		return model.SevFatal, true
	case "info":
		return model.SevInformational, true
	}
	return "", false
}

// ScrubType tells how the error was found, from the driver's message.
func ScrubType(msg string) model.ScrubType {
	m := strings.ToLower(msg)
	switch {
	case strings.Contains(m, "scrub"):
		return model.ScrubPatrol
	case strings.Contains(m, "memory read error"), strings.Contains(m, "memory write error"):
		return model.ScrubDemand
	case strings.Contains(m, "addr/cmd error"):
		return model.ScrubOther
	}
	return ""
}

// Apply copies the decoded fields into an event without overwriting fields
// that are already set.
func (d *Detail) Apply(e *model.Event) {
	fill := func(dst *model.Opt[int], src model.Opt[int]) {
		if !dst.Valid && src.Valid {
			*dst = src
		}
	}
	fill(&e.SocketId, d.Socket)
	fill(&e.IMC, d.IMC)
	fill(&e.ChannelId, d.Channel)
	fill(&e.ChannelSlotId, d.Slot)
	fill(&e.Rank, d.Rank)
	fill(&e.DeviceId, d.Device)
	fill(&e.BankGroup, d.BankGroup)
	fill(&e.Bank, d.Bank)
	fill(&e.Row, d.Row)
	fill(&e.Column, d.Column)
	fill(&e.SMBIOSHandle, d.SMBIOSHandle)
	if !e.OsPhysicalAddress.Valid && d.SystemAddress.Valid {
		e.OsPhysicalAddress = d.SystemAddress
	}
	if d.BitPosition.Valid && e.ErrorVector == "" {
		e.ErrorVector = fmt.Sprintf("bit:%d", d.BitPosition.V)
	}
	if e.Decoder == "" {
		e.Decoder = d.Decoder
	}
}

// ApplyLabel fills slot position fields from a default-form label where the
// detail string did not provide them.
func (l Label) Apply(e *model.Event) {
	fill := func(dst *model.Opt[int], src model.Opt[int]) {
		if !dst.Valid && src.Valid {
			*dst = src
		}
	}
	fill(&e.SocketId, l.Socket)
	fill(&e.IMC, l.IMC)
	fill(&e.ChannelId, l.Channel)
	fill(&e.ChannelSlotId, l.Slot)
	fill(&e.Rank, l.Rank)
	if e.Decoder == "" && l.Kind == "csrow" {
		e.Decoder = DecoderCSROW
	}
}
