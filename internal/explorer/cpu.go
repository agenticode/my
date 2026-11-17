package explorer

// This file is MemorY's own: the memory channels of a processor.

import (
	"regexp"
	"strconv"
	"strings"
)

// Processor is what the board knows of a processor: its family, the
// memory channels of one processor, the most DIMM slots one channel of the
// platform holds, and whether the memory controller sits in the chipset.
type Processor struct {
	// Family is the id of the row of the table, "" for a processor the
	// table does not know.
	Family string `json:"family,omitempty"`
	// Channels is the number of memory channels of one processor, 0 when
	// not known (and for memory on the chipset, whose board says it).
	Channels int `json:"channels,omitempty"`
	// PerChannel is the most slots one channel holds, 0 when not known.
	PerChannel int `json:"perChannel,omitempty"`
	// Hub is set for a front-side-bus platform: the memory controller is in
	// the chipset and no processor owns a slot.
	Hub bool `json:"hub,omitempty"`
	// By is "name" or "signature": what decided the row.
	By string `json:"by,omitempty"`
}

type cpuRow struct {
	family         string
	channels, per  int
	hub            bool
	re             *regexp.Regexp
	notBeforeSpace string // a match followed by this text does not count
}

// cpuRows are the processors by name, in the order they are tried, over the
// name in capitals with its runs of white space as one space. The numbers
// and their documents are in docs/compatibility.md.
var cpuRows = func() []cpuRow {
	rows := []struct {
		family        string
		channels, per int
		hub           bool
		pattern, not  string
	}{
		{"epyc-sp5", 12, 2, false, `EPYC 9\d\d\d`, ""},
		{"epyc-sp6", 6, 2, false, `EPYC 8\d\d\d`, ""},
		{"epyc-sp3", 8, 2, false, `EPYC 7[0-9A-Z]\d\d`, ""},
		{"epyc-am5", 2, 2, false, `EPYC 4\d\d\d`, ""},
		{"epyc-3000-4ch", 4, 2, false, `EPYC 3[34]\d\d`, ""},
		{"epyc-3000-2ch", 2, 2, false, `EPYC 3[12]\d\d`, ""},
		{"threadripper-pro", 8, 1, false, `THREADRIPPER PRO`, ""},
		{"threadripper", 4, 2, false, `THREADRIPPER`, ""},
		{"ryzen", 2, 2, false, `RYZEN`, ""},
		{"opteron-g34", 4, 3, false, `OPTERON\(TM\) PROCESSOR 6[1-3]\d\d`, ""},
		{"opteron-c32", 2, 3, false, `OPTERON\(TM\) PROCESSOR 4[1-3]\d\d`, ""},
		{"opteron-am3", 2, 2, false, `OPTERON\(TM\) PROCESSOR 3[23]\d\d`, ""},
		{"opteron-x3000", 2, 2, false, `OPTERON\(TM\) X3\d\d\d`, ""},
		{"opteron-f", 2, 4, false, `OPTERON\(TM\) PROCESSOR [28][2-4]\d\d|OPTERON REV F`, ""},
		{"xeon6-ap", 12, 1, false, `XEON\(R\) 69\d\d`, ""},
		{"xeon6-sp", 8, 2, false, `XEON\(R\) 6[5-8]\d\d`, ""},
		{"xeon-sp-3h", 6, 2, false, `(?:PLATINUM|GOLD) \d3\d\dH`, ""},
		{"xeon-sp-1-2", 6, 2, false, `(?:PLATINUM|GOLD|SILVER|BRONZE) \d[12]\d\d`, ""},
		{"xeon-sp-3", 8, 2, false, `(?:PLATINUM|GOLD|SILVER|BRONZE) \d3\d\d`, ""},
		{"xeon-sp-4-5", 8, 2, false, `(?:PLATINUM|GOLD|SILVER|BRONZE) \d[45]\d\d`, ""},
		{"xeon-w-3100", 6, 2, false, `\bW-3[12]\d\d`, ""},
		{"xeon-w-3300", 8, 2, false, `\bW-3[3-5]\d\d`, ""},
		{"xeon-w-2000", 4, 2, false, `\bW-2[1-4]\d\d`, ""},
		{"xeon-w-1000", 2, 2, false, `\bW-1[1-3]\d\d\d?`, ""},
		{"xeon-d-2000", 4, 2, false, `\bD-2[1-9]\d\d`, ""},
		{"xeon-d-1700", 2, 2, false, `\bD-1736NT\b`, ""},
		{"xeon-d-1500", 2, 2, false, `\bD-1[56]\d\d|PENTIUM\(R\) CPU D15\d\d`, ""},
		{"xeon-e7-v2", 8, 3, false, `\bE7-[248]8\d\d[A-Z]* V[234]`, ""},
		{"xeon-7500", 8, 2, false, `\bE7- ?[L248]\d{3,4}\b`, " V"},
		{"xeon-7500", 8, 2, false, `\b[XEL]75\d\d\b|\b[XEL]65\d\d\b`, ""},
		{"xeon-e5-2400", 3, 2, false, `\bE5-[12]4\d\d`, ""},
		{"xeon-e5-2600", 4, 3, false, `\bE5-[124]6\d\d`, ""},
		{"xeon-5500", 3, 3, false, `\b[XELW]5[56]\d\d\b|\bW3[56]\d\d\b|\bE55400\b`, ""},
		{"xeon-3400", 2, 3, false, `\b[XL]34\d\d\b`, ""},
		{"xeon-e3", 2, 2, false, `\bE3-1\d\d\d|\bE31\d\d\d[A-Z]?\b|\bE-2[1-4]\d\d`, ""},
		{"atom-c", 2, 2, false, `ATOM\(TM\) CPU C[23]\d\d\d|ATOM\(R\) X6\d\d\d`, ""},
		{"core-x", 4, 2, false, `\bI7-[3-6][89]\d\dK|\bI7-3820\b|\bI7-[3-6]9[5-7]0X\b|\bI7-78[02]0X\b|\bI9-(?:7[89]|9[89]|109)\d\dX`, ""},
		{"core-mobile", 2, 1, false, `\b1[2-4]TH GEN INTEL\(R\) CORE\(TM\) I\d-1[2-4]\d{2,3}(?:H|HK|HL|P|U|UE|HE|PE|RE)\b`, ""},
		{"intel-n", 1, 0, false, `INTEL\(R\) N\d{2,3}\b`, ""},
		{"core", 2, 2, false, `CORE\(TM\) I\d|PENTIUM\(R\) CPU G\d|CELERON\(R\) [GN]\d`, ""},
		{"fsb", 0, 0, true, `XEON\(R\) CPU +[XEL]?5[0-4]\d\d\b|XEON\(R\) CPU +[XEL]3[0-3]\d\d\b|XEON\(R\) CPU +E3110\b|XEON\(R\) CPU +[EXL]7[2-4]\d\d\b|XEON\(TM\)|PENTIUM III|CORE\(TM\)2|PENTIUM\(R\) D\b`, ""},
		{"altra", 8, 2, false, `AMPERE\(R\) ALTRA`, ""},
		{"kunpeng-920-4ch", 4, 2, false, `KUNPENG 920[ -](?:3210|5220)\b`, ""},
		{"kunpeng-920", 8, 2, false, `KUNPENG 920`, ""},
		{"ft2000plus", 8, 2, false, `FT-?2000\+`, ""},
		{"ft2000-4", 2, 1, false, `FT-?2000/4|D2000`, ""},
		{"loongson-3c5000", 4, 2, false, `LOONGSON-3C5000`, ""},
	}
	out := make([]cpuRow, len(rows))
	for i, r := range rows {
		out[i] = cpuRow{family: r.family, channels: r.channels, per: r.per, hub: r.hub, re: regexp.MustCompile(r.pattern), notBeforeSpace: r.not}
	}
	return out
}()

// matches tells whether the row matches a name: some match of its pattern
// that the text after it does not rule out.
func (r cpuRow) matches(name string) bool {
	if r.notBeforeSpace == "" {
		return r.re.MatchString(name)
	}
	for _, m := range r.re.FindAllStringIndex(name, -1) {
		if !strings.HasPrefix(name[m[1]:], r.notBeforeSpace) {
			return true
		}
	}
	return false
}

type cpuModel struct{ family, model int }

// cpuByIntelModel are the Intel processors by CPUID family and model, for a
// blank or generic name.
var cpuByIntelModel = map[cpuModel]Processor{
	{6, 143}: {Family: "xeon-sp-4-5", Channels: 8, PerChannel: 2},
	{6, 207}: {Family: "xeon-sp-4-5", Channels: 8, PerChannel: 2},
	{6, 106}: {Family: "xeon-sp-3", Channels: 8, PerChannel: 2},
	{6, 45}:  {Family: "xeon-e5-2600", Channels: 4, PerChannel: 3},
	{6, 62}:  {Family: "xeon-e5-2600", Channels: 4, PerChannel: 3},
	{6, 63}:  {Family: "xeon-e5-2600", Channels: 4, PerChannel: 3},
	{6, 79}:  {Family: "xeon-e5-2600", Channels: 4, PerChannel: 3},
	{6, 26}:  {Family: "xeon-5500", Channels: 3, PerChannel: 3},
	{6, 44}:  {Family: "xeon-5500", Channels: 3, PerChannel: 3},
	{6, 15}:  {Family: "fsb", Hub: true},
	{6, 23}:  {Family: "fsb", Hub: true},
	{6, 29}:  {Family: "fsb", Hub: true},
	{6, 7}:   {Family: "fsb", Hub: true},
	{6, 8}:   {Family: "fsb", Hub: true},
	{6, 10}:  {Family: "fsb", Hub: true},
	{6, 11}:  {Family: "fsb", Hub: true},
}

// cpuByAMDModel are the AMD processors by CPUID family and a range of
// models, in the order they are tried.
var cpuByAMDModel = []struct {
	family, lo, hi int
	p              Processor
}{
	{25, 0x10, 0x1F, Processor{Family: "epyc-sp5", Channels: 12, PerChannel: 2}},
	{25, 0xA0, 0xAF, Processor{Family: "epyc-sp5", Channels: 12, PerChannel: 2}},
	{25, 0x00, 0x0F, Processor{Family: "epyc-sp3", Channels: 8, PerChannel: 2}},
	{23, 0x30, 0x3F, Processor{Family: "epyc-sp3", Channels: 8, PerChannel: 2}},
	{23, 0x00, 0x0F, Processor{Family: "epyc-sp3", Channels: 8, PerChannel: 2}},
	{21, 0x00, 0x0F, Processor{Family: "opteron-g34", Channels: 4, PerChannel: 3}},
	{16, 0x00, 0x0A, Processor{Family: "opteron-f", Channels: 2, PerChannel: 4}},
	{15, 0x40, 0x7F, Processor{Family: "opteron-f", Channels: 2, PerChannel: 4}},
	{15, 0x00, 0x3F, Processor{Family: "opteron-940", Channels: 2, PerChannel: 4}},
}

var reSpaces = regexp.MustCompile(`\s+`)

// ProcessorFamily reads the family of a processor from its name and, when
// the table knows no name like it (a blank or generic name, an engineering
// sample), from its CPUID signature, "family/model/stepping". ok is false
// for a processor the table does not know.
func ProcessorFamily(name, signature string) (Processor, bool) {
	u := strings.ToUpper(strings.TrimSpace(reSpaces.ReplaceAllString(name, " ")))
	for _, r := range cpuRows {
		if r.matches(u) {
			return Processor{Family: r.family, Channels: r.channels, PerChannel: r.per, Hub: r.hub, By: "name"}, true
		}
	}
	f := strings.Split(signature, "/")
	if len(f) != 3 {
		return Processor{}, false
	}
	fam, err1 := strconv.Atoi(f[0])
	model, err2 := strconv.Atoi(f[1])
	if err1 != nil || err2 != nil {
		return Processor{}, false
	}
	if strings.Contains(u, "AMD") || strings.Contains(u, "OPTERON") {
		for _, r := range cpuByAMDModel {
			if fam == r.family && model >= r.lo && model <= r.hi {
				p := r.p
				p.By = "signature"
				return p, true
			}
		}
		return Processor{}, false
	}
	if fam == 15 {
		// NetBurst: every Xeon and Pentium 4 of it sits on a front-side bus.
		return Processor{Family: "fsb", Hub: true, By: "signature"}, true
	}
	if p, ok := cpuByIntelModel[cpuModel{fam, model}]; ok {
		p.By = "signature"
		return p, true
	}
	return Processor{}, false
}

// ProcessorChannels is the number of memory channels a processor has,
// read from its name; ok is false for a name the table does not know and
// for memory on the chipset.
func ProcessorChannels(name string) (int, bool) {
	p, ok := ProcessorFamily(name, "")
	if !ok || p.Channels == 0 {
		return 0, false
	}
	return p.Channels, true
}

// boardProcessor is the processor record that names the processor of the
// board, and its family: the first populated record with a name the table
// knows, else the first populated one the table knows by its signature,
// else the first populated record (every record when none is populated).
func boardProcessor(cpus []cpuRecord) (cpuRecord, Processor) {
	var pop []cpuRecord
	for _, c := range cpus {
		if c.status == 1 {
			pop = append(pop, c)
		}
	}
	if len(pop) == 0 {
		pop = cpus
	}
	var best *Processor
	var bestRec cpuRecord
	for _, c := range pop {
		if p, ok := ProcessorFamily(c.name, c.signature); ok {
			if p.By == "name" {
				return c, p
			}
			if best == nil {
				best, bestRec = &p, c
			}
		}
	}
	if best != nil {
		return bestRec, *best
	}
	if len(pop) > 0 {
		return pop[0], Processor{}
	}
	return cpuRecord{}, Processor{}
}
