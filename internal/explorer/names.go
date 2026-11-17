package explorer

// This file is MemorY's own: the forms of the slot names of server boards
// (the fix name-forms). A name is read as a whole by the first form that
// matches all of it; the tokens of the form say what its parts are.

import (
	"strings"
)

// word is a processor word of a name and its number (P1, CPU0, PROC 1).
type word struct {
	w  string
	n  int
	ok bool
}

// optInt is a number a name may give.
type optInt struct {
	v  int
	ok bool
}

func some(v int) optInt { return optInt{v, true} }

// tokens are the parts of the two names of a slot. A part the locator
// gives wins; the bank locator's value of the same part is kept apart
// (bchl, bchn, bpos, bcard).
type tokens struct {
	lform, bform string // the forms; "" for no name, "?" for a name no form reads

	cpu  word // the processor word of the locator
	bcpu word // the processor word of the bank locator

	node, mc, chn, pos, num, bank, card optInt
	chassis, sub, bnum                  optInt
	chl, ord, cardl                     string
	// ln is a letter and a number (A1), dl a digit and a letter (1A):
	// what they mean the board decides.
	lnL  string
	lnN  int
	ln   bool
	dlN  int
	dlL  string
	dl   bool
	path [3]int
	// The bank locator's values of parts the locator gives.
	bchl              string
	bchn, bpos, bcard optInt
	// chname is the name of the channel when a stage gives one.
	chname string
	// chBank is set when the channel comes from the bank locator.
	chBank bool
}

// nameForm is one form of the name tables.
type nameForm struct {
	id   string
	re   *jsRegexp
	read func(m jsMatch, t *tokens)
}

func form(id, pattern, flags string, read func(m jsMatch, t *tokens)) nameForm {
	return nameForm{id: id, re: jsre("^(?:"+pattern+")$", flags), read: read}
}

func num(m jsMatch, i int) int { return int(ParseInt(m.Group(i))) }

func up(s string) string { return strings.ToUpper(s) }

// locatorForms are the forms of a locator, in the order they are tried.
var locatorForms = []nameForm{
	// processor word, channel letter, slot number
	form("p-dimm-letter", `P(\d+)[-_ ]DIMM_?([A-Z])(\d+)`, "i", func(m jsMatch, t *tokens) {
		t.cpu, t.chl, t.pos = word{"P", num(m, 1), true}, up(m.Group(2)), some(num(m, 3))
	}),
	form("dimm-p-letter", `DIMM_P(\d+)_([A-Z])(\d+)`, "i", func(m jsMatch, t *tokens) {
		t.cpu, t.chl, t.pos = word{"P", num(m, 1), true}, up(m.Group(2)), some(num(m, 3))
	}),
	form("p-umc-ch", `P(\d+)_(?:D\d+_)?U?MC(\d+)_(?:DIM_)?CH_([A-Z])(\d+)`, "i", func(m jsMatch, t *tokens) {
		t.cpu, t.mc, t.chl, t.pos = word{"P", num(m, 1), true}, some(num(m, 2)), up(m.Group(3)), some(num(m, 4))
	}),
	form("p-ch-dimm", `P(\d+)-Ch([A-Z])-DIMM(\d+)`, "i", func(m jsMatch, t *tokens) {
		t.cpu, t.chl, t.pos = word{"P", num(m, 1), true}, up(m.Group(2)), some(num(m, 3))
	}),
	form("cpu-dimm-letter", `CPU(\d+)[_ ]DIMM[_ ]?([A-Z])(\d+)`, "i", func(m jsMatch, t *tokens) {
		t.cpu, t.chl, t.pos = word{"CPU", num(m, 1), true}, up(m.Group(2)), some(num(m, 3))
	}),
	form("cpu-dimm-letter-only", `CPU(\d+)_DIMM_([A-Z])`, "i", func(m jsMatch, t *tokens) {
		t.cpu, t.chl = word{"CPU", num(m, 1), true}, up(m.Group(2))
	}),
	form("cpu-u-dimm-u-letter", `CPU_(\d+)_DIMM_([A-Z])_(\d+)`, "i", func(m jsMatch, t *tokens) {
		t.cpu, t.chl, t.pos = word{"CPU", num(m, 1), true}, up(m.Group(2)), some(num(m, 3))
	}),
	form("cpu-letter", `CPU(\d+)_([A-Z])(\d+)`, "i", func(m jsMatch, t *tokens) {
		t.cpu, t.chl, t.pos = word{"CPU", num(m, 1), true}, up(m.Group(2)), some(num(m, 3))
	}),
	// processor word, channel number, slot number
	form("cpu-c-d", `CPU(\d+)_C(\d+)D(\d+)`, "i", func(m jsMatch, t *tokens) {
		t.cpu, t.chn, t.pos = word{"CPU", num(m, 1), true}, some(num(m, 2)), some(num(m, 3))
	}),
	form("cpu-ch-dimm", `CPU(\d+)_CH(\d+)_DIMM(\d+)`, "i", func(m jsMatch, t *tokens) {
		t.cpu, t.chn, t.pos = word{"CPU", num(m, 1), true}, some(num(m, 2)), some(num(m, 3))
	}),
	form("socket-channel-dimm", `SOCKET (\d+) CHANNEL (\d+) DIMM (\d+)`, "i", func(m jsMatch, t *tokens) {
		t.cpu, t.chn, t.pos = word{"SOCKET", num(m, 1), true}, some(num(m, 2)), some(num(m, 3))
	}),
	form("node-channel-dimm", `NODE (\d+) CHANNEL (\d+) DIMM (\d+)(?: \([A-Z0-9]+\))?`, "i", func(m jsMatch, t *tokens) {
		t.cpu, t.chn, t.pos = word{"NODE", num(m, 1), true}, some(num(m, 2)), some(num(m, 3))
	}),
	form("dimm-digits", `DIMM(\d)(\d)(\d)(?: J\d+)?`, "", func(m jsMatch, t *tokens) {
		t.cpu, t.chn, t.pos = word{"DIGIT", num(m, 1), true}, some(num(m, 2)), some(num(m, 3))
	}),
	// processor word, digit and letter
	form("p-digit-letter", `P(\d+)[-_](?:DIMM)?(\d+)([A-Z])`, "i", func(m jsMatch, t *tokens) {
		t.cpu = word{"P", num(m, 1), true}
		t.dl, t.dlN, t.dlL = true, num(m, 2), up(m.Group(3))
	}),
	form("node-digit-letter", `NODE(\d+)-(\d+)([A-Z])`, "i", func(m jsMatch, t *tokens) {
		t.cpu = word{"NODE", num(m, 1), true}
		t.dl, t.dlN, t.dlL = true, num(m, 2), up(m.Group(3))
	}),
	// processor word and a running number
	form("proc-dimm", `PROC\s+(\d+)\s+DIMM\s+(\d+)`, "i", func(m jsMatch, t *tokens) {
		t.cpu, t.num = word{"PROC", num(m, 1), true}, some(num(m, 2))
	}),
	form("proc-dimm-letter", `PROC\s+(\d+)\s+DIMM\s+(\d+)([A-Z])`, "i", func(m jsMatch, t *tokens) {
		t.cpu, t.num, t.ord = word{"PROC", num(m, 1), true}, some(num(m, 2)), up(m.Group(3))
	}),
	form("cpu-dimm-number", `CPU(\d+)[-_]DIMM_?(\d+)`, "i", func(m jsMatch, t *tokens) {
		t.cpu, t.num = word{"CPU", num(m, 1), true}, some(num(m, 2))
	}),
	form("dimm-cpu", `DIMM(\d+)_CPU(\d+)`, "i", func(m jsMatch, t *tokens) {
		t.cpu, t.num = word{"CPU", num(m, 2), true}, some(num(m, 1))
	}),
	form("socket-dimm", `Socket (\d+) DIMM (\d+)`, "i", func(m jsMatch, t *tokens) {
		t.cpu, t.num = word{"SOCKET", num(m, 1), true}, some(num(m, 2))
	}),
	form("node-dimm", `Node(\d+)_Dimm(\d+)`, "i", func(m jsMatch, t *tokens) {
		t.cpu, t.num = word{"NODE", num(m, 1), true}, some(num(m, 2))
	}),
	form("c-dimm", `([CH])(\d+)_DIMM(\d+)`, "", func(m jsMatch, t *tokens) {
		t.cpu, t.num = word{m.Group(1), num(m, 2), true}, some(num(m, 3))
	}),
	form("cpu-only", `CPU(\d+)`, "i", func(m jsMatch, t *tokens) { t.cpu = word{"CPU", num(m, 1), true} }),
	form("board-dimm", `Board (\d+), DIMM (\d+)([A-Z])`, "i", func(m jsMatch, t *tokens) {
		t.card, t.num, t.ord = some(num(m, 1)), some(num(m, 2)), up(m.Group(3))
	}),
	form("path", `(\d+)/(\d+)/(\d+)/(\d+)([A-Z])`, "", func(m jsMatch, t *tokens) {
		t.path = [3]int{num(m, 1), num(m, 2), num(m, 3)}
		t.num, t.ord = some(num(m, 4)), m.Group(5)
	}),
	// channel letter and slot number, no processor
	form("controller-channel-dimm", `Controller(\d+)-Channel([A-Z])-DIMM(\d+)`, "i", func(m jsMatch, t *tokens) {
		t.mc, t.chl, t.pos = some(num(m, 1)), up(m.Group(2)), some(num(m, 3))
	}),
	form("controller-channel", `Controller(\d+)-Channel([A-Z])`, "i", func(m jsMatch, t *tokens) {
		t.mc, t.chl = some(num(m, 1)), up(m.Group(2))
	}),
	form("channel-dimm", `Channel([A-Z])[-_]DIMM(\d+)`, "i", func(m jsMatch, t *tokens) { t.chl, t.pos = up(m.Group(1)), some(num(m, 2)) }),
	form("onboard-dimm", `ONBOARD DIMM_([A-Z])(\d+)`, "i", func(m jsMatch, t *tokens) { t.chl, t.pos = up(m.Group(1)), some(num(m, 2)) }),
	form("dimm-ch-letter", `DIMM[ -]CH([A-Z])(\d+)`, "i", func(m jsMatch, t *tokens) { t.chl, t.pos = up(m.Group(1)), some(num(m, 2)) }),
	form("ch-letter", `CH([A-Z])_(\d+)`, "i", func(m jsMatch, t *tokens) { t.chl, t.pos = up(m.Group(1)), some(num(m, 2)) }),
	form("ddr-letter", `DDR\d_([A-Z])(\d+)`, "i", func(m jsMatch, t *tokens) { t.chl, t.pos = up(m.Group(1)), some(num(m, 2)) }),
	form("memory-letter", `MEMORY ([A-Z])(\d+)`, "i", func(m jsMatch, t *tokens) { t.chl, t.pos = up(m.Group(1)), some(num(m, 2)) }),
	form("cpu-u-letter", `CPU_([A-Z])(\d+)`, "i", func(m jsMatch, t *tokens) { t.chl, t.pos = up(m.Group(1)), some(num(m, 2)) }),
	form("dimm-letter", `DIMM([A-Z])(\d+)`, "i", func(m jsMatch, t *tokens) { t.chl, t.pos = up(m.Group(1)), some(num(m, 2)) }),
	// a letter and a number, a digit and a letter: the board decides
	form("dimm-u-letter", `DIMM[_ ]([A-Z])(\d+)`, "i", func(m jsMatch, t *tokens) { t.ln, t.lnL, t.lnN = true, up(m.Group(1)), num(m, 2) }),
	form("letter-number", `([A-Z])(\d+)`, "i", func(m jsMatch, t *tokens) { t.ln, t.lnL, t.lnN = true, up(m.Group(1)), num(m, 2) }),
	form("dimm-number-u-letter", `DIMM(\d+)_([A-Z])`, "i", func(m jsMatch, t *tokens) { t.dl, t.dlN, t.dlL = true, num(m, 1), up(m.Group(2)) }),
	form("dimm-digit-letter", `DIMM(?: - |[-_ ])?(\d+)([A-Z])`, "i", func(m jsMatch, t *tokens) { t.dl, t.dlN, t.dlL = true, num(m, 1), up(m.Group(2)) }),
	// a running number only
	form("mem-card", `MEM ([A-Z]), J(\d+)`, "i", func(m jsMatch, t *tokens) { t.cardl, t.num = up(m.Group(1)), some(num(m, 2)) }),
	form("a-dimm", `[A-Z]\d+_DIMM(\d+)`, "i", func(m jsMatch, t *tokens) { t.num = some(num(m, 1)) }),
	form("dimm-number", `DIMM\s*[-_]?\s*(\d+)`, "i", func(m jsMatch, t *tokens) { t.num = some(num(m, 1)) }),
}

// bankTokens are the parts a bank locator form gives, before they are put
// beside those of the locator.
type bankTokens struct {
	bcpu                                word
	node, chn, pos, bank, card, chassis optInt
	sub, bnum                           optInt
	chl                                 string
}

type bankForm struct {
	id   string
	re   *jsRegexp
	read func(m jsMatch, b *bankTokens)
}

func bform(id, pattern, flags string, read func(m jsMatch, b *bankTokens)) bankForm {
	return bankForm{id: id, re: jsre("^(?:"+pattern+")$", flags), read: read}
}

// bankForms are the forms of a bank locator, in the order they are tried.
var bankForms = []bankForm{
	bform("p-node-channel-dimm", `P(\d+)_Node(\d+)_Channel(\d+)_Dimm(\d+)`, "i", func(m jsMatch, b *bankTokens) {
		b.bcpu, b.node, b.chn, b.pos = word{"BP", num(m, 1), true}, some(num(m, 2)), some(num(m, 3)), some(num(m, 4))
	}),
	bform("p-channel-dimm", `P(\d+)_Channel(\d+)_Dimm(\d+)`, "i", func(m jsMatch, b *bankTokens) {
		b.bcpu, b.chn, b.pos = word{"BP", num(m, 1), true}, some(num(m, 2)), some(num(m, 3))
	}),
	bform("node-channel-dimm", `(?:[A-Z]\d)?_Node(\d+)_Channel(\d+)_Dimm(\d+)`, "i", func(m jsMatch, b *bankTokens) {
		b.node, b.chn, b.pos = some(num(m, 1)), some(num(m, 2)), some(num(m, 3))
	}),
	bform("node-channel-dimm-words", `NODE (\d+) CHANNEL (\d+) DIMM (\d+)`, "i", func(m jsMatch, b *bankTokens) {
		b.node, b.chn, b.pos = some(num(m, 1)), some(num(m, 2)), some(num(m, 3))
	}),
	bform("socket-channel-dimm", `SOCKET (\d+) CHANNEL (\d+) DIMM (\d+)`, "i", func(m jsMatch, b *bankTokens) {
		b.bcpu, b.chn, b.pos = word{"SOCKET", num(m, 1), true}, some(num(m, 2)), some(num(m, 3))
	}),
	bform("node", `NODE (\d+)`, "i", func(m jsMatch, b *bankTokens) { b.node = some(num(m, 1)) }),
	bform("p-channel-letter", `P(\d+) CHANNEL ([A-Z])`, "i", func(m jsMatch, b *bankTokens) {
		b.bcpu, b.chl = word{"BP", num(m, 1), true}, up(m.Group(2))
	}),
	bform("cpu-ch-letter", `CPU(\d+)Ch([A-Z])`, "i", func(m jsMatch, b *bankTokens) {
		b.bcpu, b.chl = word{"BCPU", num(m, 1), true}, up(m.Group(2))
	}),
	bform("p-ch-letter", `P(\d+)-CH-([A-Z])`, "i", func(m jsMatch, b *bankTokens) {
		b.bcpu, b.chl = word{"P", num(m, 1), true}, up(m.Group(2))
	}),
	bform("board-cpu", `(?:Primary|Secondary) (?:Main|CPU) Board/CPU (\d+)`, "i", func(m jsMatch, b *bankTokens) {
		b.bcpu = word{"BOARDCPU", num(m, 1), true}
	}),
	bform("cpu", `CPU ?(\d+)`, "i", func(m jsMatch, b *bankTokens) { b.bcpu = word{"BCPU", num(m, 1), true} }),
	bform("sys-mb-p", `/SYS/MB/P(\d+)`, "", func(m jsMatch, b *bankTokens) { b.bcpu = word{"BP", num(m, 1), true} }),
	bform("cpu-bank", `CPU(\d+)_Bank(\d+)`, "i", func(m jsMatch, b *bankTokens) {
		b.bcpu, b.bank = word{"BCPU", num(m, 1), true}, some(num(m, 2))
	}),
	bform("p-bank", `P(\d+)_BANK(\d+)`, "i", func(m jsMatch, b *bankTokens) {
		b.bcpu, b.bank = word{"P", num(m, 1), true}, some(num(m, 2))
	}),
	bform("node-bank", `Node(\d+)_Bank(\d+)`, "i", func(m jsMatch, b *bankTokens) { b.node, b.bank = some(num(m, 1)), some(num(m, 2)) }),
	bform("node-bank-card", `Node (\d+) Bank (\d+)(?: MemCard (\d+))?`, "i", func(m jsMatch, b *bankTokens) {
		b.chassis, b.bank = some(num(m, 1)), some(num(m, 2))
		if m.Has(3) {
			b.card = some(num(m, 3))
		}
	}),
	bform("channel-letter", `CHANNEL ?([A-Z])`, "i", func(m jsMatch, b *bankTokens) { b.chl = up(m.Group(1)) }),
	bform("chan-dimm", `CHAN ([A-Z]) DIMM (\d+)`, "i", func(m jsMatch, b *bankTokens) { b.chl, b.pos = up(m.Group(1)), some(num(m, 2)) }),
	bform("channel-number", `Channel (\d+)`, "i", func(m jsMatch, b *bankTokens) { b.chn = some(num(m, 1)) }),
	bform("letter", `([A-Z])`, "", func(m jsMatch, b *bankTokens) { b.chl = m.Group(1) }),
	bform("path", `(\d+)/(\d+)/(\d+)/(\d+)`, "", func(m jsMatch, b *bankTokens) { b.sub = some(num(m, 4)) }),
	bform("dimm-number", `DIMM ?(\d+)`, "i", func(m jsMatch, b *bankTokens) { b.bnum = some(num(m, 1)) }),
	bform("same-as-locator", `(?:DIMM|BANK)_[A-Z]\d+|[A-Z]\d+`, "i", func(m jsMatch, b *bankTokens) {}),
	bform("bank-number", `(?:BANK|Slot) ?(\d+)`, "i", func(m jsMatch, b *bankTokens) { b.bank = some(num(m, 1)) }),
	bform("number", `(\d+)`, "", func(m jsMatch, b *bankTokens) { b.bank = some(num(m, 1)) }),
	bform("bank-word", `BANK`, "i", func(m jsMatch, b *bankTokens) {}),
}

// nameless are the names that name nothing.
var nameless = map[string]bool{"": true, "NO DIMM": true, "NOT SPECIFIED": true, "N/A": true, "NONE": true, "UNKNOWN": true,
	"<BAD INDEX>": true, "<OUT OF SPEC>": true}

// readNames reads the two names of a slot.
func readNames(locator, bank string) tokens {
	var t tokens
	l, b := strings.TrimSpace(locator), strings.TrimSpace(bank)
	if !nameless[strings.ToUpper(l)] {
		t.lform = "?"
		for _, f := range locatorForms {
			if m, ok := f.re.Match(l); ok {
				f.read(m, &t)
				t.lform = f.id
				break
			}
		}
	}
	if !nameless[strings.ToUpper(b)] {
		t.bform = "?"
		for _, f := range bankForms {
			if m, ok := f.re.Match(b); ok {
				var bt bankTokens
				f.read(m, &bt)
				t.bform = f.id
				t.bcpu = bt.bcpu
				t.node, t.bank, t.chassis, t.sub, t.bnum = bt.node, bt.bank, bt.chassis, bt.sub, bt.bnum
				// A part the locator gives stays; the bank locator's own value is kept apart.
				if bt.chl != "" {
					if t.chl != "" {
						t.bchl = bt.chl
					} else {
						t.chl, t.chBank = bt.chl, !t.chn.ok
					}
				}
				if bt.chn.ok && !t.chn.ok && t.chl == "" {
					t.chBank = true
				}
				put := func(dst, keep *optInt, v optInt) {
					if !v.ok {
						return
					}
					if dst.ok {
						*keep = v
					} else {
						*dst = v
					}
				}
				put(&t.chn, &t.bchn, bt.chn)
				put(&t.pos, &t.bpos, bt.pos)
				put(&t.card, &t.bcard, bt.card)
				break
			}
		}
	}
	return t
}

// named tells a slot whose names place nothing: no locator, and a bank
// locator that is at most a bank number.
func (t tokens) unnamed() bool {
	return t.lform == "" && (t.bform == "" || t.bform == "bank-number" || t.bform == "bank-word" || t.bform == "number")
}

// formNotes are examples of each form and what its parts are, for the
// table of docs/compatibility.md.
var formNotes = map[string][2]string{
	"p-dimm-letter":           {"`P1-DIMMA1`, `P1_DIMM_A1`", "processor word `P`, channel letter, slot"},
	"dimm-p-letter":           {"`DIMM_P0_A1`", "processor word `P`, channel letter, slot"},
	"p-umc-ch":                {"`P0_UMC0_CH_A0`, `P0_MC0_DIM_CH_A0`", "processor word `P`, memory controller, channel letter, slot"},
	"p-ch-dimm":               {"`P0-ChA-DIMM0`", "processor word `P`, channel letter, slot"},
	"cpu-dimm-letter":         {"`CPU0_DIMM_A1`, `CPU1 DIMM A1`", "processor word `CPU`, channel letter, slot"},
	"cpu-dimm-letter-only":    {"`CPU0_DIMM_A`", "processor word `CPU`, channel letter"},
	"cpu-u-dimm-u-letter":     {"`CPU_0_DIMM_A_0`", "processor word `CPU`, channel letter, slot"},
	"cpu-letter":              {"`CPU0_A1`", "processor word `CPU`, channel letter, slot"},
	"cpu-c-d":                 {"`CPU0_C0D1`", "processor word `CPU`, channel number, slot"},
	"cpu-ch-dimm":             {"`CPU0_CH0_DIMM0`", "processor word `CPU`, channel number, slot"},
	"socket-channel-dimm":     {"`SOCKET 0 CHANNEL 1 DIMM 0`", "processor word `SOCKET`, channel number, slot"},
	"node-channel-dimm":       {"`NODE 0 CHANNEL 0 DIMM 0 (C0A0)`", "processor word `NODE`, channel number, slot"},
	"dimm-digits":             {"`DIMM010`, `DIMM000 J11`", "processor, channel and slot as three digits (capitals only)"},
	"p-digit-letter":          {"`P1_DIMM1A`, `P1-1A`", "processor word `P`, a digit and a letter: what they are the board decides"},
	"node-digit-letter":       {"`NODE0-1A`", "processor word `NODE`, a digit and a letter"},
	"proc-dimm":               {"`PROC 1 DIMM 9`, `PROC  1 DIMM  1`", "processor word `PROC` (from 1), a slot number that says no channel"},
	"proc-dimm-letter":        {"`PROC 1 DIMM 3A`", "processor word `PROC`, slot number, population letter"},
	"cpu-dimm-number":         {"`CPU1-DIMM5`, `CPU1_DIMM_5`", "processor word `CPU`, a slot number that says no channel"},
	"dimm-cpu":                {"`DIMM5_CPU0`", "a slot number, processor word `CPU`"},
	"socket-dimm":             {"`Socket 0 DIMM 1`", "processor word `Socket`, a slot number"},
	"node-dimm":               {"`Node0_Dimm1`", "processor word `Node`, a slot number"},
	"c-dimm":                  {"`C0_DIMM1`, `H0_DIMM1`", "processor word `C` or `H` (capitals only), a slot number"},
	"cpu-only":                {"`CPU1`", "processor word `CPU`; the bank locator gives the slot number"},
	"board-dimm":              {"`Board 1, DIMM 1A`", "memory card, slot number, population letter"},
	"path":                    {"`1/0/0/1A`", "a path of the partition, slot number, population letter"},
	"controller-channel-dimm": {"`Controller0-ChannelA-DIMM0`", "memory controller, channel letter, slot"},
	"controller-channel":      {"`Controller1-ChannelB`", "memory controller, channel letter"},
	"channel-dimm":            {"`ChannelA-DIMM0`, `ChannelA_Dimm1`", "channel letter, slot"},
	"onboard-dimm":            {"`ONBOARD DIMM_A1`", "channel letter, slot"},
	"dimm-ch-letter":          {"`DIMM CHA1`, `DIMM-CHB2`", "channel letter, slot"},
	"ch-letter":               {"`CHA_1`", "channel letter, slot"},
	"ddr-letter":              {"`DDR4_A1`", "channel letter, slot"},
	"memory-letter":           {"`MEMORY A1`", "channel letter, slot"},
	"cpu-u-letter":            {"`CPU_A1`", "channel letter, slot"},
	"dimm-letter":             {"`DIMMA1`", "channel letter, slot"},
	"dimm-u-letter":           {"`DIMM_A1`, `DIMM A1`", "a letter and a number: what they are the board decides"},
	"letter-number":           {"`A1`, `B12`", "a letter and a number: what they are the board decides"},
	"dimm-number-u-letter":    {"`DIMM1_A`", "a digit and a letter"},
	"dimm-digit-letter":       {"`DIMM-1A`, `DIMM 1A`, `DIMM1A`", "a digit and a letter"},
	"mem-card":                {"`MEM A, J1`", "memory card letter, slot number"},
	"a-dimm":                  {"`A1_DIMM0`", "a slot number"},
	"dimm-number":             {"`DIMM 1`, `DIMM1`, `Dimm 1`", "a slot number that says no channel"},
	// bank locators
	"b:p-node-channel-dimm":     {"`P0_Node0_Channel0_Dimm0`", "processor (from 0), node, channel number, slot"},
	"b:p-channel-dimm":          {"`P0_Channel0_Dimm0`", "processor (from 0), channel number, slot"},
	"b:node-channel-dimm":       {"`_Node0_Channel0_Dimm0`, `A1_Node0_Channel0_Dimm0`", "node, channel number, slot"},
	"b:node-channel-dimm-words": {"`NODE 0 CHANNEL 0 DIMM 0`", "node, channel number, slot"},
	"b:socket-channel-dimm":     {"`SOCKET 0 CHANNEL 0 DIMM 0`", "processor, channel number, slot"},
	"b:node":                    {"`NODE 1`", "node: a processor or a memory controller, the board decides"},
	"b:p-channel-letter":        {"`P0 CHANNEL A`", "processor (from 0), channel letter"},
	"b:cpu-ch-letter":           {"`CPU0ChA`", "processor, channel letter"},
	"b:p-ch-letter":             {"`P0-CH-A`", "processor, channel letter"},
	"b:board-cpu":               {"`Primary Main Board/CPU 1`", "processor"},
	"b:cpu":                     {"`CPU 1`, `CPU1`", "processor"},
	"b:sys-mb-p":                {"`/SYS/MB/P0`", "processor (from 0)"},
	"b:cpu-bank":                {"`CPU0_Bank1`", "processor, bank"},
	"b:p-bank":                  {"`P1_BANK0`", "processor, bank: the slots of a bank share their channel"},
	"b:node-bank":               {"`Node0_Bank1`", "node, bank"},
	"b:node-bank-card":          {"`Node 0 Bank 1 MemCard 2`", "chassis node, bank, memory card"},
	"b:channel-letter":          {"`CHANNEL A`, `ChannelA`", "channel letter"},
	"b:chan-dimm":               {"`CHAN A DIMM 1`", "channel letter, slot"},
	"b:channel-number":          {"`Channel 1`", "channel number"},
	"b:letter":                  {"`A`", "channel letter (one capital)"},
	"b:path":                    {"`1/0/0/1`", "a path of the partition"},
	"b:dimm-number":             {"`DIMM1`, `DIMM 1`", "the slot number of a locator `CPU1`"},
	"b:same-as-locator":         {"`DIMM_A1`, `BANK_A2`, `A1`", "the locator again: nothing"},
	"b:bank-number":             {"`BANK 1`, `Bank 1`, `Slot 1`", "a bank: nothing of the place"},
	"b:number":                  {"`7`", "a bank number: nothing of the place"},
	"b:bank-word":               {"`BANK`", "nothing"},
}
