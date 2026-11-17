package explorer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func block(locator, bank, size string) string {
	return "Memory Device\n\tSize: " + size + "\n\tLocator: " + locator + "\n\tBank Locator: " + bank + "\n"
}

func places(ds []Dimm) string {
	var p []string
	for _, d := range ds {
		p = append(p, fmt.Sprintf("%v/%v", d.Socket, d.Channel))
	}
	return strings.Join(p, " ")
}

// Each test of a slot name, with a name that fires it and one that just
// misses it. The numbers are what the reference gives (taken from node).
func TestSlotPatterns(t *testing.T) {
	carried(t)
	for _, c := range []struct {
		name, locator, bank string
		socket, channel     float64
	}{
		{"P_Node_Channel_Dimm", "P1-DIMMA1", "P1_Node1_Channel2_Dimm0", 1, 6},
		{"P_Node_Channel_Dimm miss", "SLOT7", "xP1_Node1_Channel2_Dimm0", 1, 2},
		{"_Node_Channel_Dimm", "DIMM000 J1", "_Node1_Channel3_Dimm0", 1, 3},
		{"_Node_Channel_Dimm miss", "SLOT7", "_Node1_Channel3_Dim0", 0, 3},
		{"DIMMxyz", "DIMM120 J9", "Not Specified", 1, 2},
		{"DIMMxyz miss", "DIMM12 J9", "Not Specified", 0, 0},
		{"letter-number", "B7", "Not Specified", 1, 0},
		{"letter-number miss", "B7x", "Not Specified", 0, 0},
		{"PROC", "PROC 2 DIMM 3", "Not Specified", 1, 0},
		{"PROC miss", "PRO 2 DIMM 3", "Not Specified", 0, 0},
		{"CPU", "CPU1_DIMM_B2", "Not Specified", 1, 3},
		{"CPU miss", "CP1_DIMM_B2", "Not Specified", 0, 3},
		{"P or Node", "P2-DIMMC1", "Not Specified", 1, 2},
		{"P or Node miss", "xP2-DIMMC1", "Not Specified", 0, 2},
		{"_CxDy", "CPU0_C3D1", "Not Specified", 0, 3},
		{"_CxDy miss", "CPU0C3D1", "Not Specified", 0, 0},
		{"_CHx_DIMMy", "CPU1_CH4_DIMM0", "Not Specified", 1, 4},
		{"_CHx_DIMMy miss", "CPU1_CH4DIMM0", "Not Specified", 1, 2},
		{"bank Channel n", "SLOT7", "Channel 5", 0, 5},
		{"bank Channel n miss", "SLOT7", "Chanel 5", 0, 0},
		{"bank Node n", "SLOT7", "NODE 6", 5, 6},
		{"bank Node n miss", "SLOT7", "NOD 6", 0, 0},
		{"DIMM letter", "SLOT DIMMD2", "Not Specified", 0, 3},
		{"DIMM letter miss", "SLOT DIMM2", "Not Specified", 0, 0},
		{"Channel or _ letter", "ChannelC-DIMM0", "Not Specified", 0, 2},
		{"Channel or _ letter miss", "ChanC-DIMM0", "Not Specified", 0, 0},
		{"bank Channel or CH char", "SLOT7", "CH 7", 0, 7},
		{"bank Channel or CH char miss", "SLOT7", "C 7", 0, 0},
	} {
		ds := ParseDmidecodeMemory(block(c.locator, c.bank, "16 GB"), nil, nil)
		if len(ds) != 1 || float64(ds[0].Socket) != c.socket || float64(ds[0].Channel) != c.channel {
			t.Errorf("%s: %q|%q gives %s, the reference %v/%v", c.name, c.locator, c.bank, places(ds), c.socket, c.channel)
		}
	}
}

func slotsOf(locators ...string) string {
	var b strings.Builder
	for _, l := range locators {
		b.WriteString(block(l, "Not Specified", "16 GB"))
	}
	return b.String()
}

// The two second passes: the channel of A1 and of PROC m DIMM n is the
// number from 1 modulo half the slots of the socket.
func TestSecondPasses(t *testing.T) {
	carried(t)
	for _, c := range []struct {
		name string
		text string
		want string
	}{
		{"letter-number", slotsOf("A1", "A2", "A3", "A4", "A5", "A6", "A7", "A8", "B1", "B2"), "0/0 0/1 0/2 0/3 0/0 0/1 0/2 0/3 1/0 1/0"},
		{"letter-number miss", slotsOf("A1", "A2", "A3", "A4x"), "0/0 0/1 0/0 0/0"},
		{"PROC DIMM", slotsOf("PROC 1 DIMM 1", "PROC 1 DIMM 2", "PROC 1 DIMM 3", "PROC 1 DIMM 4", "PROC 1 DIMM 5", "PROC 1 DIMM 6", "PROC 2 DIMM 1", "PROC 2 DIMM 12"), "0/0 0/1 0/2 0/0 0/1 0/2 1/0 1/0"},
		{"PROC DIMM miss", slotsOf("PROC 1 DIM 1", "PROC 1 DIM 4"), "0/0 0/0"},
	} {
		if got := places(ParseDmidecodeMemory(c.text, nil, nil)); got != c.want {
			t.Errorf("%s: %s, the reference %s", c.name, got, c.want)
		}
	}
}

// The defects of the first implementation, with the fixes switched off:
// the readers still give its answers. TestFixes shows each fixed result.
func TestDefectsHeld(t *testing.T) {
	carried(t)
	parse := func(s string) []Dimm { return ParseDmidecodeMemory(s, nil, nil) }
	blocks := func(loc string, banks ...string) string {
		var b strings.Builder
		for _, bk := range banks {
			b.WriteString(block(loc, bk, "16 GB"))
		}
		return b.String()
	}
	t.Run("D1 size in GiB is 0 bytes", func(t *testing.T) {
		if d := parse(block("A1", "x", "16 GiB")); d[0].SizeBytes != 0 || !d[0].Populated {
			t.Errorf("got %v bytes, populated %v", d[0].SizeBytes, d[0].Populated)
		}
	})
	t.Run("D2 the _D of _DIMM is channel D", func(t *testing.T) {
		if got := places(parse(slotsOf("CPU0_DIMM_A1", "CPU0_DIMM_B1", "CPU1_DIMM_A1"))); got != "0/3 0/3 1/3" {
			t.Error(got)
		}
	})
	t.Run("D3 P0 counts from 1", func(t *testing.T) {
		if got := places(parse(blocks("DIMM 0", "P0 CHANNEL A", "P0 CHANNEL B", "P1 CHANNEL A"))); got != "-1/0 -1/1 0/0" {
			t.Error(got)
		}
	})
	t.Run("D4 NODE 0 counts from 1", func(t *testing.T) {
		if got := places(parse(blocks("DIMM_A1", "NODE 0 CHANNEL 0 DIMM 0", "NODE 1 CHANNEL 0 DIMM 0"))); got != "-1/0 0/0" {
			t.Error(got)
		}
	})
	t.Run("D5 the node of the bank locator is the channel", func(t *testing.T) {
		text := block("CPU0_DIMM_A1", "NODE 1", "16 GB") + block("CPU0_DIMM_B1", "NODE 1", "16 GB") + block("CPU0_DIMM_C1", "NODE 1", "16 GB")
		if got := places(parse(text)); got != "0/1 0/1 0/1" {
			t.Error(got)
		}
	})
	t.Run("D6 DIMM n with bank CPU n is not placed", func(t *testing.T) {
		if got := places(parse(block("DIMM 1", "CPU 1", "16 GB") + block("DIMM 13", "CPU 2", "16 GB"))); got != "0/0 0/0" {
			t.Error(got)
		}
	})
	t.Run("D7 _P of DIMM_P0_A0 is channel P", func(t *testing.T) {
		if got := places(parse(block("DIMM_P0_A0", "BANK 0", "16 GB") + block("DIMM_P1_B0", "BANK 1", "16 GB"))); got != "0/15 0/15" {
			t.Error(got)
		}
	})
	t.Run("D8 NO DIMM", func(t *testing.T) {
		if got := places(parse(block("NO DIMM", "NO DIMM", "No Module Installed"))); got != "0/0" {
			t.Error(got)
		}
	})
	t.Run("D9 two slots a channel on a board of one", func(t *testing.T) {
		var l []string
		for i := 1; i <= 12; i++ {
			l = append(l, fmt.Sprintf("A%d", i))
		}
		if got := places(parse(slotsOf(l...))); got != "0/0 0/1 0/2 0/3 0/4 0/5 0/0 0/1 0/2 0/3 0/4 0/5" {
			t.Error(got)
		}
	})
	t.Run("D11 counts by position, a later controller wins", func(t *testing.T) {
		r := MemoryMap(MapInput{Dmidecode: slotsOf("A1", "A2", "B1"), EdacCE: "dimm0:5\ndimm1:7\ndimm0:2\n"})
		if got := fmt.Sprint(r.Dimms[0].CeCount, r.Dimms[1].CeCount, r.Dimms[2].CeCount); got != "2 7 0" {
			t.Error(got)
		}
	})
	t.Run("D12 CRLF is one slot", func(t *testing.T) {
		text := strings.ReplaceAll(slotsOf("A1", "A2"), "\n", "\r\n")
		if n := len(parse(text)); n != 1 {
			t.Errorf("%d slots", n)
		}
	})
	t.Run("D13 an empty locator takes the next line", func(t *testing.T) {
		d := parse("Memory Device\n\tSize: 16 GB\n\tLocator:\n\tBank Locator: P0_Node0_Channel1_Dimm0\n")
		if d[0].Locator != "Bank Locator: P0_Node0_Channel1_Dimm0" || d[0].Channel != 1 {
			t.Errorf("%q %v", d[0].Locator, d[0].Channel)
		}
	})
	t.Run("D14 a second block of numastat overwrites the first", func(t *testing.T) {
		stat := "                           node0           node1\nnuma_hit                     1               2\n" +
			"                           node2           node3\nnuma_hit                     3               4\n"
		n := ParseNumaInfo("node 0 size: 10 MB\nnode 1 size: 10 MB\nnode 2 size: 10 MB\nnode 3 size: 10 MB\n", stat)
		if got := fmt.Sprint(n[0].NumaHit, n[1].NumaHit, n[2].NumaHit, n[3].NumaHit); got != "3 4 0 0" {
			t.Error(got)
		}
	})
	t.Run("D15 the second node of a Supermicro CPU is channel 4", func(t *testing.T) {
		if got := places(parse(block("P2-DIMME1", "P1_Node1_Channel0_Dimm0", "16 GB"))); got != "1/4" {
			t.Error(got)
		}
	})
	t.Run("D16 slab lines by object count", func(t *testing.T) {
		text := "slabinfo - version: 2.1\nbig 10 10 100000 1 1\nmany 900 900 8 1 1\n"
		if s := ParseSlabinfo(SlabTop(text)); len(s) != 2 || s[0].Name != "many" {
			t.Errorf("%+v", s)
		}
	})
	t.Run("D17 DIMM_A1 with no bank locator is one socket", func(t *testing.T) {
		if got := places(parse(slotsOf("DIMM_A1", "DIMM_A2", "DIMM_B1", "DIMM_B2"))); got != "0/0 0/0 0/1 0/1" {
			t.Error(got)
		}
	})
	t.Run("D18 the A of CHAN is the channel", func(t *testing.T) {
		if got := places(parse(blocks("DIMM0", "CHAN A DIMM 0", "CHAN B DIMM 0"))); got != "0/0 0/0" {
			t.Error(got)
		}
	})
	t.Run("D19 a slot that counts from 0", func(t *testing.T) {
		if got := places(parse(slotsOf("D0", "D1", "D2", "D3"))); got != "3/-1 3/0 3/1 3/0" {
			t.Error(got)
		}
		var lshw strings.Builder
		lshw.WriteString("*-memory\n     description: System Memory\n")
		for i, s := range []string{"A0", "A1", "A2", "A3"} {
			fmt.Fprintf(&lshw, "   *-bank:%d\n        description: DIMM DDR2 Synchronous 800 MHz\n        slot: %s\n        size: 1GiB\n", i, s)
		}
		if got := places(ParseLshwDimms(lshw.String(), nil, nil)); got != "0/-1 0/0 0/1 0/0" {
			t.Error(got)
		}
	})
}

// SocketBy and ChannelBy: empty only where the number stayed 0.
func TestTestNames(t *testing.T) {
	for _, f := range []Fix{0, AllFixes} {
		old := SetFixes(f)
		testNames(t)
		SetFixes(old)
	}
}

func testNames(t *testing.T) {
	check := func(where string, ds []Dimm) {
		for _, d := range ds {
			if d.SocketBy == "" && d.Socket != 0 || d.ChannelBy == "" && d.Channel != 0 {
				t.Errorf("%s: %q|%q socket %v by %q, channel %v by %q", where, d.Locator, d.BankLocator, d.Socket, d.SocketBy, d.Channel, d.ChannelBy)
			}
		}
	}
	files, _ := filepath.Glob(filepath.Join(fixtureRoot, "*", "dmidecode.txt"))
	for _, f := range files {
		b, _ := os.ReadFile(f)
		check(f, ParseDmidecodeMemory(string(b), nil, nil))
	}
	files, _ = filepath.Glob(filepath.Join(fixtureRoot, "*", "lshw.txt"))
	for _, f := range files {
		b, _ := os.ReadFile(f)
		check(f, ParseLshwDimms(string(b), nil, nil))
	}
	for _, list := range readPopulation(t) {
		var in []SlotText
		for _, s := range list {
			in = append(in, SlotText{Locator: s.locator, BankLocator: s.bank, Populated: s.populated})
		}
		check(fmt.Sprintf("population list %d", list[0].list), ParseDmidecodeMemory(WriteSlots(in), nil, nil))
	}
	words := []string{"P0", "P1_Node1_Channel2_Dimm0", "_Node0_Channel1_Dimm1", "DIMM012", "A7", "PROC 2 DIMM 4", "CPU1", "Node 2", "_C3D1", "_CH2_DIMM0", "Channel 4", "NODE 5", "DIMMC", "_D", "CH B", "-0", ""}
	for _, a := range words {
		for _, b := range words {
			check(a+"|"+b, ParseDmidecodeMemory(block(a, b, "8 GB")+block(b, a, "No Module Installed"), nil, nil))
		}
	}
}

// Every reader stops one past its bound.
func TestBounds(t *testing.T) {
	var b strings.Builder
	for i := 0; i < MaxSlots+50; i++ {
		b.WriteString(block(fmt.Sprintf("A%d", i), "x", "8 GB"))
	}
	if n := len(ParseDmidecodeMemory(b.String(), nil, nil)); n != MaxSlots+1 {
		t.Errorf("dmidecode: %d slots", n)
	}
	b.Reset()
	b.WriteString("*-memory\n")
	for i := 0; i < MaxSlots+50; i++ {
		fmt.Fprintf(&b, "  *-bank:%d\n     slot: A%d\n     size: 8GiB\n", i, i)
	}
	if n := len(ParseLshwDimms(b.String(), nil, nil)); n != MaxSlots+1 {
		t.Errorf("lshw: %d slots", n)
	}
	b.Reset()
	for i := 0; i < MaxNodes+50; i++ {
		fmt.Fprintf(&b, "node %d size: 10 MB\n", i)
	}
	if n := len(ParseNumaInfo(b.String(), "")); n != MaxNodes+1 {
		t.Errorf("numa: %d nodes", n)
	}
	b.Reset()
	for i := 0; i < MaxSlabs+50; i++ {
		fmt.Fprintf(&b, "s%d 1 2 3 4 5\n", i)
	}
	if n := len(ParseSlabinfo(b.String())); n != MaxSlabs+1 {
		t.Errorf("slabinfo: %d lines", n)
	}
	b.Reset()
	b.WriteString("memcg 1 /\n node 0\n")
	for i := 0; i < MaxLruLines+50; i++ {
		fmt.Fprintf(&b, "   %d 1 2 3\n", i)
	}
	if r := Mglru("0x0007", b.String()); len(r.Cgroups[0].Nodes[0].Generations) != MaxLruLines-2 {
		t.Errorf("lru_gen: %d generations", len(r.Cgroups[0].Nodes[0].Generations))
	}
	b.Reset()
	for i := 0; i < 600; i++ {
		fmt.Fprintf(&b, "PID:%d NAME:p CMD:x\nVmRSS: %d kB\n---\n", i+1, i+1)
	}
	if n := len(ParseStatusEntries(b.String())); n != MaxProcesses {
		t.Errorf("processes: %d", n)
	}
}

// carried switches the fixes off for the test.
func carried(t *testing.T) {
	old := SetFixes(0)
	t.Cleanup(func() { SetFixes(old) })
}
