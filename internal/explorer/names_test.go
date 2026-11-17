package explorer

import (
	"fmt"
	"strings"
	"testing"
)

// Each locator form reads a name that matches it whole, and not a name
// that just misses it.
func TestLocatorForms(t *testing.T) {
	for _, c := range []struct{ form, match, miss string }{
		{"p-dimm-letter", "P1-DIMMA1", "P1-DIMMA"},
		{"dimm-p-letter", "DIMM_P0_A1", "DIMM_P0_A"},
		{"p-umc-ch", "P0_D0_UMC0_CH_A0", "P0_UMC0_CH_0"},
		{"p-ch-dimm", "P0-ChA-DIMM0", "P0-ChA-DIMM"},
		{"cpu-dimm-letter", "CPU0_DIMM_A1", "CPU0_DIMM_A1_"},
		{"cpu-dimm-letter-only", "CPU0_DIMM_A", "CPU0_DIMM_"},
		{"cpu-u-dimm-u-letter", "CPU_0_DIMM_A_0", "CPU_0_DIMM_A_"},
		{"cpu-letter", "CPU0_A1", "CPU0_A"},
		{"cpu-c-d", "CPU0_C0D1", "CPU0_C0D"},
		{"cpu-ch-dimm", "CPU0_CH0_DIMM0", "CPU0_CH0_DIMM"},
		{"socket-channel-dimm", "SOCKET 0 CHANNEL 1 DIMM 0", "SOCKET 0 CHANNEL 1"},
		{"node-channel-dimm", "NODE 0 CHANNEL 0 DIMM 0 (C0A0)", "NODE 0 CHANNEL 0"},
		{"dimm-digits", "DIMM010 J11", "dimm010"},
		{"p-digit-letter", "P1-1A", "P1_DIMM1"},
		{"node-digit-letter", "NODE0-1A", "NODE0-1"},
		{"proc-dimm", "PROC  1 DIMM  9", "PROC 1 DIMM"},
		{"proc-dimm-letter", "PROC 1 DIMM 3A", "PROC 1 DIMM 3AB"},
		{"cpu-dimm-number", "CPU1_DIMM_5", "CPU1-DIMM"},
		{"dimm-cpu", "DIMM5_CPU0", "DIMM5_CPU"},
		{"socket-dimm", "Socket 0 DIMM 1", "Socket 0 DIMM"},
		{"node-dimm", "Node0_Dimm1", "Node0_Dimm"},
		{"c-dimm", "H0_DIMM1", "c0_DIMM1"},
		{"cpu-only", "CPU1", "CPU"},
		{"board-dimm", "Board 1, DIMM 1A", "Board 1, DIMM 1"},
		{"path", "1/0/0/1A", "1/0/0/1"},
		{"controller-channel-dimm", "Controller0-ChannelA-DIMM0", "Controller0-ChannelA-DIMM"},
		{"controller-channel", "Controller1-ChannelB", "Controller0-Channel"},
		{"channel-dimm", "ChannelA_Dimm1", "ChannelA-DIMM"},
		{"onboard-dimm", "ONBOARD DIMM_A1", "ONBOARD DIMM_A"},
		{"dimm-ch-letter", "DIMM-CHB2", "DIMM CH1"},
		{"ch-letter", "CHA_1", "CHA_"},
		{"ddr-letter", "DDR4_A1", "DDR_A1"},
		{"memory-letter", "MEMORY A1", "MEMORY A"},
		{"cpu-u-letter", "CPU_A1", "CPU_A"},
		{"dimm-letter", "DIMMA1", "DIMMA"},
		{"dimm-u-letter", "DIMM A1", "DIMM_A"},
		{"letter-number", "B12", "AB1"},
		{"dimm-number-u-letter", "DIMM1_A", "DIMM1_"},
		{"dimm-digit-letter", "DIMM - 1A", "DIMM-1"},
		{"mem-card", "MEM A, J1", "MEM A J1"},
		{"a-dimm", "A1_DIMM0", "A1_DIMM"},
		{"dimm-number", "DIMM  1", "DIMMX"},
	} {
		if got := readNames(c.match, "").lform; got != c.form {
			t.Errorf("%q: form %q, want %q", c.match, got, c.form)
		}
		if got := readNames(c.miss, "").lform; got == c.form {
			t.Errorf("%q: read by %q, which it misses", c.miss, c.form)
		}
	}
	if len(locatorForms) != 42 {
		t.Errorf("%d locator forms; every form needs a case here", len(locatorForms))
	}
}

// Each bank locator form reads a name that matches it whole, and not a
// name that just misses it.
func TestBankForms(t *testing.T) {
	for _, c := range []struct{ form, match, miss string }{
		{"p-node-channel-dimm", "P0_Node0_Channel0_Dimm0", "P0_Node0_Channel0_Dimm"},
		{"p-channel-dimm", "P0_Channel0_Dimm0", "P0_Channel0_Dimm"},
		{"node-channel-dimm", "A1_Node0_Channel0_Dimm0", "Node0_Channel0_Dimm0"},
		{"node-channel-dimm-words", "NODE 0 CHANNEL 0 DIMM 0", "NODE 0 CHANNEL 0 DIMM"},
		{"socket-channel-dimm", "SOCKET 0 CHANNEL 0 DIMM 0", "SOCKET 0 CHANNEL 0"},
		{"node", "NODE 1", "NODE"},
		{"p-channel-letter", "P0 CHANNEL A", "P0 CHANNEL"},
		{"cpu-ch-letter", "CPU0ChA", "CPU0Ch"},
		{"p-ch-letter", "P0-CH-A", "P0-CH-"},
		{"board-cpu", "Secondary CPU Board/CPU 2", "Main Board/CPU 1"},
		{"cpu", "CPU 1", "CPU"},
		{"sys-mb-p", "/SYS/MB/P0", "/sys/mb/p0"},
		{"cpu-bank", "CPU0_Bank1", "CPU0_Bank"},
		{"p-bank", "P1_BANK0", "P1_BANK"},
		{"node-bank", "Node0_Bank1", "Node0_Bank"},
		{"node-bank-card", "Node 0 Bank 1 MemCard 2", "Node 0 Bank"},
		{"channel-letter", "Channel A", "CHANNEL"},
		{"chan-dimm", "CHAN A DIMM 1", "CHAN A DIMM"},
		{"channel-number", "Channel 1", "Channel 1A"},
		{"letter", "A", "a"},
		{"path", "1/0/0/1", "1/0/0"},
		{"dimm-number", "DIMM 1", "DIMM"},
		{"same-as-locator", "BANK_A2", "DIMM_A"},
		{"bank-number", "Slot 1", "BANK X"},
		{"number", "7", "7a"},
		{"bank-word", "BANK", "BANKS"},
	} {
		if got := readNames("", c.match).bform; got != c.form {
			t.Errorf("%q: form %q, want %q", c.match, got, c.form)
		}
		if got := readNames("", c.miss).bform; got == c.form {
			t.Errorf("%q: read by %q, which it misses", c.miss, c.form)
		}
	}
	if len(bankForms) != 26 {
		t.Errorf("%d bank locator forms; every form needs a case here", len(bankForms))
	}
}

// A name no form reads is of no form, and its slot is placed nowhere; a
// name that names nothing is no name.
func TestUnknownForm(t *testing.T) {
	if f := readNames("Slot#7 (front)", "?bank?").lform + "|" + readNames("Slot#7 (front)", "?bank?").bform; f != "?|?" {
		t.Errorf("forms %q", f)
	}
	for _, n := range []string{"", "NO DIMM", "Not Specified", "n/a", "None", "Unknown", "<BAD INDEX>", "<OUT OF SPEC>"} {
		if tk := readNames(n, n); tk.lform != "" || tk.bform != "" || !tk.unnamed() {
			t.Errorf("%q: forms %q %q", n, tk.lform, tk.bform)
		}
	}
	d := ParseDmidecodeMemory(slotsOf("Slot#7 (front)", "DIMM_A1"), nil, nil)
	if d[0].PlaceBy != "" || d[0].Aside != "unknown-form" {
		t.Errorf("unknown form: %+v", d[0])
	}
}

// board writes a made-up board: its processor records and its slots as
// "locator|bank locator" (a leading + marks a populated slot).
func board(cpu string, sockets int, names ...string) string {
	h := SlotHost{Vendor: "Example Systems", Product: "E1", Processor: cpu, Sockets: sockets, Present: strings.Repeat("1", sockets)}
	var s []SlotText
	for _, n := range names {
		pop := strings.HasPrefix(n, "+")
		l, b, _ := strings.Cut(strings.TrimPrefix(n, "+"), "|")
		s = append(s, SlotText{Locator: l, BankLocator: b, Populated: pop})
	}
	return WriteSlotsOf(h, s)
}

// where writes the place of each slot: socket/channel name/position, or
// the reason it has none.
func where(d []Dimm) string {
	var out []string
	for _, x := range d {
		switch {
		case x.PlaceBy != "":
			out = append(out, fmt.Sprintf("%v/%s/%v", x.Socket, x.ChannelName, x.Position))
		case x.SocketBy != "":
			out = append(out, fmt.Sprintf("%v/-", x.Socket))
		default:
			out = append(out, x.Aside)
		}
	}
	return strings.Join(out, " ")
}

const (
	xeonE5    = "Intel(R) Xeon(R) CPU E5-2630 v4 @ 2.20GHz"
	xeonGold  = "Intel(R) Xeon(R) Gold 6130 CPU @ 2.10GHz"
	xeonSP3rd = "Intel(R) Xeon(R) Gold 6330 CPU @ 2.00GHz"
)

// The rules of what a name means on a board, each with a public dump that
// shows it and a made-up board beside it.
func TestNameRules(t *testing.T) {
	read := func(text string) ([]Dimm, Facts) { return ReadSlots(text, nil, nil) }
	public := func(name, dump string) ([]Dimm, Facts, []popSlot) {
		list := popList(t, dump)
		d, f := read(WriteSlotsOf(popHost(list), slotTexts(list)))
		for i, s := range list {
			if diff := slotDiff(d[i], f, s); diff != "" {
				t.Errorf("%s (%s) slot %q|%q: %s", name, dump, s.locator, s.bank, diff)
			}
		}
		return d, f, list
	}
	slot := func(d []Dimm, list []popSlot, locator string) Dimm {
		for i, s := range list {
			if s.locator == locator {
				return d[i]
			}
		}
		t.Fatalf("no slot %s", locator)
		return Dimm{}
	}

	t.Run("letters pair with bank channels: the letter is the channel", func(t *testing.T) {
		// Intel S4600LH: DIMM_A1 beside NODE 0 CHANNEL 0 DIMM 0.
		_, f, _ := public("S4600LH", "500D53D4644B")
		if f.Letter != "letter" {
			t.Errorf("letter taken for %q", f.Letter)
		}
		d, f := read(board(xeonE5, 1, "DIMM_A1|NODE 0 CHANNEL 0 DIMM 0", "DIMM_A2|NODE 0 CHANNEL 0 DIMM 1", "DIMM_B1|NODE 0 CHANNEL 1 DIMM 0"))
		if w := where(d); w != "0/A/0 0/A/1 0/B/0" || f.Letter != "letter" {
			t.Errorf("made up: %s, %s", w, f.Letter)
		}
	})
	t.Run("digits pair with bank channels: the digit is the channel", func(t *testing.T) {
		// Supermicro X9DBL: P1-DIMM1A beside P0_Node0_Channel0_Dimm0, three channels 1, 2, 3.
		d, f := ReadSlots(fixtureText(t, "uroot-supermicro-x9dbl"), nil, nil)
		if w := where(d[:3]); f.Letter != "letter" || w != "0/A/0 0/B/0 0/C/0" {
			t.Errorf("X9DBL: %q %s", f.Letter, w)
		}
		d, f = read(board(xeonE5, 1, "DIMM1A|NODE 0 CHANNEL 0 DIMM 0", "DIMM1B|NODE 0 CHANNEL 0 DIMM 1", "DIMM2A|NODE 0 CHANNEL 1 DIMM 0", "DIMM2B|NODE 0 CHANNEL 1 DIMM 1"))
		if w := where(d); w != "0/1/0 0/1/1 0/2/0 0/2/1" || f.Letter != "digit" {
			t.Errorf("made up: %s, %s", w, f.Letter)
		}
	})
	t.Run("neither pairs: the name is a label and the bank locator places the slot", func(t *testing.T) {
		// Quanta Leopard: A0 to A7 beside _Node0_Channel0_Dimm0.
		_, f, _ := public("Leopard", "58B8D5234EEB")
		if f.Letter != "label" {
			t.Errorf("Leopard: %q", f.Letter)
		}
		d, f := read(board(xeonE5, 1, "A0|_Node0_Channel0_Dimm0", "A1|_Node0_Channel1_Dimm0", "A2|_Node0_Channel0_Dimm1"))
		if w := where(d); w != "0/0/0 0/1/0 0/0/1" || f.Letter != "label" {
			t.Errorf("made up: %s, %s", w, f.Letter)
		}
	})
	t.Run("P1_BANK groups the slots of one channel", func(t *testing.T) {
		d, f := read(board(xeonE5, 2, "P1_DIMM1A|P1_BANK0", "P1_DIMM1B|P1_BANK0", "P1_DIMM2A|P1_BANK1", "P1_DIMM2B|P1_BANK1"))
		if w := where(d); w != "0/1/0 0/1/1 0/2/0 0/2/1" || f.Letter != "digit" {
			t.Errorf("digits share a bank: %s, %s", w, f.Letter)
		}
		d, f = read(board(xeonE5, 2, "P1_DIMM1A|P1_BANK0", "P1_DIMM2A|P1_BANK0", "P1_DIMM1B|P1_BANK1", "P1_DIMM2B|P1_BANK1"))
		if w := where(d); w != "0/A/0 0/A/1 0/B/0 0/B/1" || f.Letter != "letter" {
			t.Errorf("letters share a bank: %s, %s", w, f.Letter)
		}
	})
	t.Run("without a bank locator 1A on Supermicro: the digit is the channel, A first", func(t *testing.T) {
		h := SlotHost{Vendor: "Supermicro", Product: "X8DTN", Processor: "Intel(R) Xeon(R) CPU E5620 @ 2.40GHz", Sockets: 1, Present: "1"}
		d, _ := read(WriteSlotsOf(h, []SlotText{{Locator: "P1-DIMM1A"}, {Locator: "P1-DIMM1B"}, {Locator: "P1-DIMM2A"}}))
		if w := where(d); w != "0/1/0 0/1/1 0/2/0" {
			t.Errorf("Supermicro 1A: %s", w)
		}
		d, _ = read(board("Intel(R) Xeon(R) CPU E5620 @ 2.40GHz", 1, "DIMM1A", "DIMM1B", "DIMM2A"))
		if w := where(d); w != "0/A/0 0/B/0 0/A/1" {
			t.Errorf("another maker's 1A: %s", w)
		}
	})
	t.Run("a processor word counts from 0 where some slot carries 0, PROC from 1", func(t *testing.T) {
		d, _ := read(board(xeonE5, 2, "CPU1_DIMM_A1", "CPU2_DIMM_A1"))
		if w := where(d); w != "0/A/0 1/A/0" {
			t.Errorf("CPU1, CPU2: %s", w)
		}
		d, _ = read(board(xeonE5, 2, "CPU0_DIMM_A1", "CPU1_DIMM_A1"))
		if w := where(d); w != "0/A/0 1/A/0" {
			t.Errorf("CPU0, CPU1: %s", w)
		}
		d, _ = read(board(xeonE5, 2, "PROC 1 DIMM 1|", "PROC 2 DIMM 1|"))
		if w := where(d); w != "0/- 1/-" {
			t.Errorf("PROC: %s", w)
		}
	})
	t.Run("the locator stands before the bank locator", func(t *testing.T) {
		// ASUS RS720A-E12: CPU1_DIMM_A1 beside P0 CHANNEL A, processors named 1 and 2.
		d, _, list := public("RS720A-E12", "6002D3D7ADE4")
		if s := slot(d, list, "CPU1_DIMM_A1"); s.Socket != 0 || s.PlaceBy != ByName {
			t.Errorf("CPU1_DIMM_A1: %+v", s)
		}
		d, _ = read(board(xeonE5, 2, "CPU2_DIMM_A1|P0 CHANNEL A", "CPU1_DIMM_A1|P1 CHANNEL A"))
		if w := where(d); w != "1/A/0 0/A/0" {
			t.Errorf("made up: %s", w)
		}
	})
	t.Run("a Node of the bank locator is a processor", func(t *testing.T) {
		// ASUS Z10PE-D16 WS: NODE 1 and NODE 3 on two processors.
		_, f, _ := public("Z10PE-D16", "730D20836AE3")
		if f.Sockets != 2 {
			t.Errorf("Z10PE: %d sockets", f.Sockets)
		}
		d, _ := read(board(xeonE5, 2, "DIMM_A1|NODE 1", "DIMM_E1|NODE 3"))
		if w := where(d); w != "0/A/0 1/E/0" {
			t.Errorf("as many nodes as processors: %s", w)
		}
		d, _ = read(board(xeonE5, 2, "DIMM_A1|NODE 1"))
		if w := where(d); w != "0/A/0" {
			t.Errorf("fewer nodes than processors, from 1: %s", w)
		}
	})
	t.Run("a Node of the bank locator is one of several memory controllers", func(t *testing.T) {
		// 3Logic Graviton TUNDRA: CPU1_DIMM_A1 beside NODE 1 to NODE 4 on two processors.
		public("Graviton", "1E34987A2608")
		d, _ := read(board(xeonSP3rd, 2, "DIMM_A1|NODE 1", "DIMM_C1|NODE 2", "DIMM_E1|NODE 3", "DIMM_G1|NODE 4"))
		if w := where(d); w != "0/A/0 0/C/0 1/E/0 1/G/0" {
			t.Errorf("two controllers a processor: %s", w)
		}
	})
	t.Run("a node count that names nothing", func(t *testing.T) {
		d, f := read(board(xeonE5, 2, "DIMM 1|NODE 0", "DIMM 2|NODE 1", "DIMM 3|NODE 2"))
		if w := where(d); w != "no-socket no-socket no-socket" || f.Nodes == "" {
			t.Errorf("3 nodes on 2 processors: %s, %q", w, f.Nodes)
		}
	})
	t.Run("one processor record is socket 0", func(t *testing.T) {
		d, _ := read(board(xeonE5, 1, "ChannelA-DIMM0", "ChannelB-DIMM0"))
		if w := where(d); w != "0/A/0 0/B/0" || d[0].PlaceBy != ByOneSocket {
			t.Errorf("one socket: %s %s", w, d[0].PlaceBy)
		}
		d, _ = read(board("Example CPU", 2, "ChannelA-DIMM0", "ChannelB-DIMM0"))
		if w := where(d); w != "no-socket no-socket" {
			t.Errorf("two sockets: %s", w)
		}
	})
	t.Run("slots that share a channel letter share the processor", func(t *testing.T) {
		// Cisco UCS C220 M4: DIMM_A2 with bank NO DIMM beside DIMM_A1 with NODE 0 CHANNEL 0 DIMM 0.
		d, _, list := public("C220 M4", "07077149A7D0")
		if s := slot(d, list, "DIMM_A2"); s.PlaceBy != ByMate && s.PlaceBy != "" {
			t.Errorf("DIMM_A2: %+v", s)
		}
		d, _ = read(board(xeonE5, 2, "DIMM_A1|NODE 0 CHANNEL 0 DIMM 0", "DIMM_A2|", "DIMM_E1|NODE 1 CHANNEL 0 DIMM 0", "DIMM_E2|"))
		if w := where(d); w != "0/A/0 0/A/1 1/E/0 1/E/1" || d[1].PlaceBy != ByMate {
			t.Errorf("made up: %s %s", w, d[1].PlaceBy)
		}
	})
	t.Run("channel letters run over the processors", func(t *testing.T) {
		// ETegro Hyperion RS125 G4: ChannelA_Dimm1 to ChannelH_Dimm2 on two Xeon E5.
		d, _, list := public("RS125 G4", "17C5EE5E5187")
		if s := slot(d, list, "ChannelE_Dimm1"); s.Socket != 1 || s.PlaceBy != ByLetters {
			t.Errorf("ChannelE_Dimm1: %+v", s)
		}
		d, _ = read(board(xeonE5, 2, "ChannelA-DIMM0", "ChannelB-DIMM0", "ChannelC-DIMM0", "ChannelD-DIMM0"))
		if w := where(d); w != "0/A/0 0/B/0 1/C/0 1/D/0" {
			t.Errorf("four letters on two processors: %s", w)
		}
		d, _ = read(board(xeonE5, 2, "ChannelA-DIMM0", "ChannelB-DIMM0", "ChannelC-DIMM0"))
		if w := where(d); w != "no-socket no-socket no-socket" {
			t.Errorf("three letters on two processors: %s", w)
		}
		d, _ = read(board("Example CPU", 2, "ChannelA-DIMM0", "ChannelB-DIMM0"))
		if w := where(d); w != "no-socket no-socket" {
			t.Errorf("a processor without a channel count: %s", w)
		}
	})
	t.Run("a locator that names the channel letter stands when the bank locator disagrees", func(t *testing.T) {
		// Lenovo HR630X: CPU1_DIMM_F2 with _Node0_Channel6_Dimm1 beside CPU1_DIMM_F1 with _Node0_Channel5_Dimm0.
		d, f, list := public("HR630X", "560D5AFC1336")
		if s := slot(d, list, "CPU1_DIMM_F2"); s.ChannelName != "F" || !f.BankDisagrees {
			t.Errorf("CPU1_DIMM_F2: %+v, disagrees %v", s, f.BankDisagrees)
		}
		d, f = read(board(xeonGold, 1, "CPU1_DIMM_A1|_Node0_Channel0_Dimm0", "CPU1_DIMM_A2|_Node0_Channel1_Dimm1"))
		if w := where(d); w != "0/A/0 0/A/1" || !f.BankDisagrees {
			t.Errorf("made up: %s, %v", w, f.BankDisagrees)
		}
	})
	t.Run("a memory controller and a channel number", func(t *testing.T) {
		// Tyrone DIT400TR-48RL: P0_MC0_DIM_CH_A0 to P1_MC1_DIM_CH_F0.
		public("DIT400TR-48RL", "1F5FDFD47B12")
		d, _ := read(board(xeonGold, 1, "CPU0_DIMM_A1|P0_Node0_Channel0_Dimm0", "CPU0_DIMM_D1|P0_Node1_Channel0_Dimm0"))
		if w := where(d); w != "0/A/0 0/D/0" {
			t.Errorf("letters beside two controllers: %s", w)
		}
		d, _ = read(board(xeonGold, 1, "DIMM 1|P0_Node0_Channel0_Dimm0", "DIMM 2|P0_Node0_Channel1_Dimm0", "DIMM 3|P0_Node1_Channel0_Dimm0"))
		if w := where(d); w != "0/0.0/0 0/0.1/0 0/1.0/0" {
			t.Errorf("numbers behind two controllers: %s", w)
		}
		d, _ = read(board("Intel(R) Core(TM) i7-12700H", 1, "Controller0-ChannelA-DIMM0", "Controller1-ChannelA-DIMM0"))
		if w := where(d); w != "0/0A/0 0/1A/0" {
			t.Errorf("controllers in the locator: %s", w)
		}
	})
	t.Run("ranks: a channel letter the board lacks takes no number", func(t *testing.T) {
		d, _ := read(board(xeonE5, 1, "DIMM_A1", "DIMM_C2", "DIMM_C1", "DIMM_D1"))
		var got []string
		for _, x := range d {
			got = append(got, fmt.Sprintf("%v.%v", x.Channel, x.Position))
		}
		if g := strings.Join(got, " "); g != "0.0 1.1 1.0 2.0" {
			t.Errorf("channels A, C, D: %s", g)
		}
	})
}

// Every row of the processor table, by name and, where the table has
// one, by signature; and a name outside it.
func TestProcessorFamily(t *testing.T) {
	for _, c := range []struct {
		name, signature, family string
		channels, per           int
	}{
		{"AMD EPYC 9254 24-Core Processor", "", "epyc-sp5", 12, 2},
		{"AMD EPYC 8534P 64-Core Processor", "", "epyc-sp6", 6, 2},
		{"AMD EPYC 7763 64-Core Processor", "", "epyc-sp3", 8, 2},
		{"AMD EPYC 4344P 8-Core Processor", "", "epyc-am5", 2, 2},
		{"AMD EPYC 3451 16-Core Processor", "", "epyc-3000-4ch", 4, 2},
		{"AMD EPYC 3251 8-Core Processor", "", "epyc-3000-2ch", 2, 2},
		{"AMD Ryzen Threadripper PRO 5975WX 32-Cores", "", "threadripper-pro", 8, 1},
		{"AMD Ryzen Threadripper 3970X 32-Core Processor", "", "threadripper", 4, 2},
		{"AMD Ryzen 9 5900X 12-Core Processor", "", "ryzen", 2, 2},
		{"AMD Opteron(TM) Processor 6272", "", "opteron-g34", 4, 3},
		{"AMD Opteron(tm) Processor 4274 HE", "", "opteron-c32", 2, 3},
		{"AMD Opteron(tm) Processor 3280", "", "opteron-am3", 2, 2},
		{"AMD Opteron(tm) X3216 APU", "", "opteron-x3000", 2, 2},
		{"Dual-Core AMD Opteron(tm) Processor 2214", "", "opteron-f", 2, 4},
		{"Intel(R) Xeon(R) 6980P", "", "xeon6-ap", 12, 1},
		{"Intel(R) Xeon(R) 6767P", "", "xeon6-sp", 8, 2},
		{"Intel(R) Xeon(R) Gold 6330H CPU @ 2.00GHz", "", "xeon-sp-3h", 6, 2},
		{"Intel(R) Xeon(R) Gold 6248 CPU @ 2.50GHz", "", "xeon-sp-1-2", 6, 2},
		{"Intel(R) Xeon(R) Silver 4309Y CPU @ 2.80GHz", "", "xeon-sp-3", 8, 2},
		{"Intel(R) Xeon(R) Gold 6526Y", "", "xeon-sp-4-5", 8, 2},
		{"Intel(R) Xeon(R) W-3235 CPU @ 3.30GHz", "", "xeon-w-3100", 6, 2},
		{"Intel(R) Xeon(R) w5-3435X", "", "", 0, 0},
		{"Intel(R) Xeon(R) W-3365 CPU @ 2.70GHz", "", "xeon-w-3300", 8, 2},
		{"Intel(R) Xeon(R) W-2145 CPU @ 3.70GHz", "", "xeon-w-2000", 4, 2},
		{"Intel(R) Xeon(R) W-1370P @ 3.60GHz", "", "xeon-w-1000", 2, 2},
		{"Intel(R) Xeon(R) D-2146NT CPU @ 2.30GHz", "", "xeon-d-2000", 4, 2},
		{"Intel(R) Xeon(R) D-1736NT CPU @ 2.70GHz", "", "xeon-d-1700", 2, 2},
		{"Intel(R) Xeon(R) CPU D-1541 @ 2.10GHz", "", "xeon-d-1500", 2, 2},
		{"Intel(R) Xeon(R) CPU E7-2890 v2 @ 2.80GHz", "", "xeon-e7-v2", 8, 3},
		{"Intel(R) Xeon(R) CPU E7- 4870  @ 2.40GHz", "", "xeon-7500", 8, 2},
		{"Intel(R) Xeon(R) CPU X7560 @ 2.27GHz", "", "xeon-7500", 8, 2},
		{"Intel(R) Xeon(R) CPU E5-2420 0 @ 1.90GHz", "", "xeon-e5-2400", 3, 2},
		{"Intel(R) Xeon(R) CPU E5-2683 v3 @ 2.00GHz", "", "xeon-e5-2600", 4, 3},
		{"Intel(R) Xeon(R) CPU X5650 @ 2.67GHz", "", "xeon-5500", 3, 3},
		{"Intel(R) Xeon(R) CPU X3450 @ 2.67GHz", "", "xeon-3400", 2, 3},
		{"Intel(R) Xeon(R) CPU E3-1240 v6 @ 3.70GHz", "", "xeon-e3", 2, 2},
		{"Intel(R) Atom(TM) CPU C2758 @ 2.40GHz", "", "atom-c", 2, 2},
		{"Intel(R) Core(TM) i7-5820K CPU @ 3.30GHz", "", "core-x", 4, 2},
		{"12th Gen Intel(R) Core(TM) i7-12650H", "", "core-mobile", 2, 1},
		{"Intel(R) N100", "", "intel-n", 1, 0},
		{"Intel(R) Core(TM) i7-8700 CPU @ 3.20GHz", "", "core", 2, 2},
		{"Intel(R) Xeon(R) CPU 5150 @ 2.66GHz", "", "fsb", 0, 0},
		{"Ampere(R) Altra(R) Processor", "", "altra", 8, 2},
		{"HUAWEI Kunpeng 920 5220", "", "kunpeng-920-4ch", 4, 2},
		{"Kunpeng 920-4826", "", "kunpeng-920", 8, 2},
		{"FT-2000+/64", "", "ft2000plus", 8, 2},
		{"Phytium FT2000/4", "", "ft2000-4", 2, 1},
		{"Loongson-3C5000L", "", "loongson-3c5000", 4, 2},
		// By signature, for a blank or generic name.
		{"Intel(R) Xeon(R) CPU 0000%@", "6/143/8", "xeon-sp-4-5", 8, 2},
		{"", "6/207/2", "xeon-sp-4-5", 8, 2},
		{"Genuine Intel(R) CPU", "6/106/6", "xeon-sp-3", 8, 2},
		{"", "6/45/7", "xeon-e5-2600", 4, 3},
		{"", "6/62/4", "xeon-e5-2600", 4, 3},
		{"", "6/63/2", "xeon-e5-2600", 4, 3},
		{"Intel Xeon", "6/79/1", "xeon-e5-2600", 4, 3},
		{"", "6/26/5", "xeon-5500", 3, 3},
		{"", "6/44/2", "xeon-5500", 3, 3},
		{"", "6/15/11", "fsb", 0, 0},
		{"", "6/23/6", "fsb", 0, 0},
		{"", "6/29/1", "fsb", 0, 0},
		{"", "6/8/6", "fsb", 0, 0},
		{"", "15/4/1", "fsb", 0, 0},
		{"AMD", "25/17/1", "epyc-sp5", 12, 2},
		{"AMD Eng Sample", "25/160/2", "epyc-sp5", 12, 2},
		{"AMD", "25/1/1", "epyc-sp3", 8, 2},
		{"AMD", "23/49/0", "epyc-sp3", 8, 2},
		{"AMD", "23/1/2", "epyc-sp3", 8, 2},
		{"AMD", "21/2/0", "opteron-g34", 4, 3},
		{"AMD", "16/4/2", "opteron-f", 2, 4},
		{"AMD", "15/65/2", "opteron-f", 2, 4},
		{"AMD", "15/33/2", "opteron-940", 2, 4},
		// Outside the table.
		{"Example CPU", "", "", 0, 0},
		{"Example CPU", "7/1/0", "", 0, 0},
		{"AMD", "26/1/0", "", 0, 0},
	} {
		p, ok := ProcessorFamily(c.name, c.signature)
		if p.Family != c.family || p.Channels != c.channels || p.PerChannel != c.per || ok != (c.family != "") || p.Hub != (c.family == "fsb") {
			t.Errorf("%q %q: %+v %v, want %s %d %d", c.name, c.signature, p, ok, c.family, c.channels, c.per)
		}
		if want := "signature"; c.signature != "" && ok && p.By != want && c.family != "" {
			if q, _ := ProcessorFamily(c.name, ""); q.Family != "" {
				continue
			}
			t.Errorf("%q %q: decided by %s", c.name, c.signature, p.By)
		}
	}
	rows := map[string]bool{}
	for _, r := range cpuRows {
		rows[r.family] = true
	}
	if len(rows) != 46 {
		t.Errorf("%d families by name", len(rows))
	}
}

// The board orders the slots of a channel by their position when the pass
// placed them, and by the number of the name without the fix.
func TestBoardOrdersByPosition(t *testing.T) {
	// The bank locator counts the slots of channel A the other way round.
	text := board(xeonGold, 1, "+CPU0_DIMM_A1|P0_Node0_Channel0_Dimm1", "+CPU0_DIMM_A2|P0_Node0_Channel0_Dimm0", "+CPU0_DIMM_B1|P0_Node0_Channel1_Dimm0")
	order := func() string {
		l := Board(ParseDmidecodeMemory(text, nil, nil), nil)
		var out []string
		for _, r := range l.Rows {
			for _, p := range r.Parts {
				for _, c := range p.Channels {
					for _, s := range c.Sticks {
						out = append(out, s.Locator)
					}
				}
			}
		}
		return strings.Join(out, " ")
	}
	// The left bank is drawn from the CPU outwards, the last slot first.
	if got := order(); got != "CPU0_DIMM_A2 CPU0_DIMM_A1 CPU0_DIMM_B1" {
		t.Errorf("with the fix: %s", got)
	}
	old := SetFixes(AllFixes &^ FixChannelRank)
	defer SetFixes(old)
	if got := order(); got != "CPU0_DIMM_A1 CPU0_DIMM_A2 CPU0_DIMM_B1" {
		t.Errorf("without the fix: %s", got)
	}
}
