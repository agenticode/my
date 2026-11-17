package explorer

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"testing"
)

// popSlot is one line of testdata/explorer/population/slots.tsv.
type popSlot struct {
	list, dumps, sockets int
	populated            bool
	locator, bank        string
	socket, channel      float64
}

func unescapeTSV(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	r := strings.NewReplacer(`\\`, `\`, `\t`, "\t", `\n`, "\n", `\r`, "\r")
	return r.Replace(s)
}

// readPopulation reads the slot lists of the public collection, by list.
func readPopulation(t testing.TB) [][]popSlot {
	t.Helper()
	f, err := os.Open(fixtureRoot + "/population/slots.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lists [][]popSlot
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "list\t") {
			continue
		}
		p := strings.Split(line, "\t")
		if len(p) != 8 {
			t.Fatalf("slots.tsv: %q has %d fields", line, len(p))
		}
		n := func(s string) int { v, _ := strconv.Atoi(s); return v }
		fl := func(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }
		s := popSlot{list: n(p[0]), dumps: n(p[1]), sockets: n(p[2]), populated: p[3] == "1",
			locator: unescapeTSV(p[4]), bank: unescapeTSV(p[5]), socket: fl(p[6]), channel: fl(p[7])}
		if len(lists) < s.list {
			lists = append(lists, nil)
		}
		lists[s.list-1] = append(lists[s.list-1], s)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return lists
}

// TestPopulationPlacement writes every slot list of the public collection
// as the board writes ledger slots (Size, Locator, Bank Locator) and reads
// it back: each slot gets the socket and channel the reference gave it in
// the dump itself.
func TestPopulationPlacement(t *testing.T) {
	lists := readPopulation(t)
	if len(lists) != 936 {
		t.Fatalf("%d slot lists, want 936", len(lists))
	}
	slots, dumps := 0, 0
	for _, list := range lists {
		in := make([]SlotText, len(list))
		for i, s := range list {
			in[i] = SlotText{Locator: s.locator, BankLocator: s.bank, Populated: s.populated, SizeMB: 16384}
			if s.locator == "Not Specified" {
				in[i].Locator = ""
			}
		}
		got := ParseDmidecodeMemory(WriteSlots(in), nil, nil)
		if len(got) != len(list) {
			t.Errorf("list %d: %d slots read back, want %d", list[0].list, len(got), len(list))
			continue
		}
		for i, s := range list {
			if float64(got[i].Socket) != s.socket || float64(got[i].Channel) != s.channel || got[i].Populated != s.populated {
				t.Errorf("list %d slot %q|%q: socket %v channel %v populated %v, the reference gave %v %v %v", s.list, s.locator, s.bank,
					got[i].Socket, got[i].Channel, got[i].Populated, s.socket, s.channel, s.populated)
			}
		}
		slots += len(list)
		dumps += list[0].dumps
	}
	if slots != 15103 || dumps != 2043 {
		t.Errorf("%d slots over %d dumps, want 15103 over 2043", slots, dumps)
	}
}
