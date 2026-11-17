package explorer

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/testutil"
)

// popSlot is one line of testdata/explorer/population/slots.tsv.
type popSlot struct {
	list, dumps, sockets                                  int
	vendor, product, board, processor, signature, present string
	populated                                             bool
	typ, locator, bank                                    string
	refSocket, refChannel                                 float64
	// The place the board gives the slot.
	state, reason, socket, channel, chname, position, index, by string
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
		if len(p) != 23 {
			t.Fatalf("slots.tsv: %q has %d fields", line, len(p))
		}
		for i := range p {
			p[i] = unescapeTSV(p[i])
		}
		n := func(s string) int { v, _ := strconv.Atoi(s); return v }
		fl := func(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }
		s := popSlot{list: n(p[0]), dumps: n(p[1]), sockets: n(p[2]), vendor: p[3], product: p[4], board: p[5], processor: p[6],
			signature: p[7], present: p[8], populated: p[9] == "1", typ: p[10], locator: p[11], bank: p[12],
			refSocket: fl(p[13]), refChannel: fl(p[14]), state: p[15], reason: p[16], socket: p[17], channel: p[18],
			chname: p[19], position: p[20], index: p[21], by: p[22]}
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

// slotTexts are the slots of a list as the board hands them to the reader.
func slotTexts(list []popSlot) []SlotText {
	in := make([]SlotText, len(list))
	for i, s := range list {
		in[i] = SlotText{Locator: s.locator, BankLocator: s.bank, Populated: s.populated, SizeMB: 16384, Type: s.typ}
	}
	return in
}

// popDump is one line of testdata/explorer/population/dumps.tsv.
type popDump struct {
	list   int
	family string
}

// readDumps reads the slot list and the board family of every dump.
func readDumps(t testing.TB) map[string]popDump {
	t.Helper()
	b, err := os.ReadFile(fixtureRoot + "/population/dumps.tsv")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]popDump{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "dump\t") {
			continue
		}
		p := strings.Split(line, "\t")
		if len(p) != 3 {
			t.Fatalf("dumps.tsv: %q", line)
		}
		n, _ := strconv.Atoi(p[1])
		out[p[0]] = popDump{list: n, family: p[2]}
	}
	return out
}

// popList is the slot list of a dump of the collection.
func popList(t testing.TB, dump string) []popSlot {
	t.Helper()
	d, ok := readDumps(t)[dump]
	if !ok {
		t.Fatalf("no dump %s", dump)
	}
	return readPopulation(t)[d.list-1]
}

// popHost is what the ledger keeps of the host of a list.
func popHost(list []popSlot) SlotHost {
	s := list[0]
	return SlotHost{Vendor: s.vendor, Product: s.product, Board: s.board, Processor: s.processor, Signature: s.signature, Sockets: s.sockets, Present: s.present}
}

// readerPlaced tells a slot the reader placed before the checks.
func readerPlaced(s popSlot) bool {
	switch {
	case s.state == "placed":
		return true
	case s.reason == "same-place" || s.reason == "run" || s.reason == "more-channels" || s.reason == "crowd":
		return true
	}
	return false
}

// slotDiff is how the reader's slot differs from the expected one, "" when
// it does not.
func slotDiff(d Dimm, f Facts, s popSlot) string {
	if s.state == "not-a-slot" {
		if d.NotSlot != s.reason {
			return fmt.Sprintf("not a slot %q, got %q", s.reason, d.NotSlot)
		}
		return ""
	}
	if d.NotSlot != "" {
		return "got not a slot " + d.NotSlot
	}
	if !readerPlaced(s) {
		if d.PlaceBy != "" {
			return fmt.Sprintf("want %s/%s, got placed by %s at %v/%v", s.state, s.reason, d.PlaceBy, d.Socket, d.Channel)
		}
		if s.socket != "" && (d.SocketBy == "" || strconv.Itoa(int(d.Socket)) != s.socket) {
			return fmt.Sprintf("want socket %s without a channel, got %v by %q", s.socket, d.Socket, d.SocketBy)
		}
		return ""
	}
	if d.PlaceBy == "" {
		return fmt.Sprintf("want placed by %s at %s/%s, got not placed (socket by %q)", s.by, s.socket, s.channel, d.SocketBy)
	}
	sock := strconv.Itoa(int(d.Socket))
	if f.Hub {
		sock = "hub"
	}
	by := d.PlaceBy
	if d.Label != "" && s.by == "label" {
		by = "label"
	}
	got := fmt.Sprintf("%s %v %s %v %s %s", sock, d.Channel, d.ChannelName, d.Position, d.Idx, by)
	want := fmt.Sprintf("%s %s %s %s %s %s", s.socket, s.channel, s.chname, s.position, s.index, s.by)
	if got != want {
		return "got " + got + ", want " + want
	}
	return ""
}

// meFixes are the fixes of the readers of the first round, with the pass
// of the second round off.
const meFixes = AllFixes &^ FixNameForms

// The counts of the public collection that the population tests hold.
const (
	popLists = 1711
	popSlots = 25605
	popDumps = 2043
)

// TestPopulationPlacement writes every slot list of the public collection
// as the board writes ledger slots (Size, Locator, Bank Locator) and reads
// it back: each slot gets the socket and channel the reference gave it in
// the dump itself.
func TestPopulationPlacement(t *testing.T) {
	carried(t)
	lists := readPopulation(t)
	if len(lists) != popLists {
		t.Fatalf("%d slot lists, want %d", len(lists), popLists)
	}
	slots, dumps := 0, 0
	for _, list := range lists {
		got := ParseDmidecodeMemory(WriteSlots(slotTexts(list)), nil, nil)
		if len(got) != len(list) {
			t.Errorf("list %d: %d slots read back, want %d", list[0].list, len(got), len(list))
			continue
		}
		for i, s := range list {
			if float64(got[i].Socket) != s.refSocket || float64(got[i].Channel) != s.refChannel || got[i].Populated != s.populated {
				t.Errorf("list %d slot %q|%q: socket %v channel %v populated %v, the reference gave %v %v %v", s.list, s.locator, s.bank,
					got[i].Socket, got[i].Channel, got[i].Populated, s.refSocket, s.refChannel, s.populated)
			}
		}
		slots += len(list)
		dumps += list[0].dumps
	}
	if slots != popSlots || dumps != popDumps {
		t.Errorf("%d slots over %d dumps, want %d over %d", slots, dumps, popSlots, popDumps)
	}
}

// popText writes a slot list as the board writes the slots of a ledger,
// with the system maker and the processor the ledger knows.
func popText(list []popSlot) string {
	return WriteSlotsOf(SlotHost{Vendor: list[0].vendor, Processor: list[0].processor}, slotTexts(list))
}

// boardTally counts the dumps of the collection by how their boards are
// drawn.
type boardTally struct {
	all, some, noChannel, none, wrongCPU int
}

func tallyPopulation(lists [][]popSlot, socketsKnown bool) boardTally {
	var b boardTally
	for _, list := range lists {
		sockets := 0
		if socketsKnown {
			sockets = list[0].sockets
		}
		text := popText(list)
		if on(FixNameForms) {
			// The pass of the second round reads what the ledger keeps of
			// the host: product, board and processor records.
			h := popHost(list)
			if !socketsKnown {
				h.Sockets, h.Present, h.Signature, h.Processor = 0, "", "", ""
			}
			text = WriteSlotsOf(h, slotTexts(list))
		}
		var p []Placement
		for _, x := range Place(ParseDmidecodeMemory(text, nil, nil), sockets, 0) {
			if x.State != NotASlot {
				p = append(p, x)
			}
		}
		placed, noch, un := 0, 0, 0
		drawn := map[int]bool{}
		for _, x := range p {
			switch x.State {
			case Placed:
				placed++
			case NoChannel:
				noch++
			default:
				un++
			}
			if x.State != Unplaced {
				drawn[x.Socket] = true
			}
		}
		n := list[0].dumps
		switch {
		case placed == len(p):
			b.all += n
		case un == len(p):
			b.none += n
		case noch > 0 && placed == 0:
			b.noChannel += n
		default:
			b.some += n
		}
		if k := list[0].sockets; k > 0 && len(drawn) > 0 {
			low, high := 1<<30, -1
			for s := range drawn {
				low, high = min(low, s), max(high, s)
			}
			if low == 1 {
				high--
			}
			if len(drawn) > k || high >= k {
				b.wrongCPU += n
			}
		}
	}
	return b
}

// TestPopulationBoards holds how the boards of the 2,043 dumps of the
// public collection are drawn, with the socket count known (the processor
// records) and not known, without the fixes (the numbers of the first
// implementation with the checks of the board) and with them.
func TestPopulationBoards(t *testing.T) {
	lists := readPopulation(t)
	for _, c := range []struct {
		fixes Fix
		known bool
		want  boardTally
	}{
		{0, true, boardTally{all: 1077, some: 48, noChannel: 226, none: 692}},
		{0, false, boardTally{all: 1135, some: 50, noChannel: 229, none: 629, wrongCPU: 5}},
		{meFixes, true, boardTally{all: 1502, some: 51, noChannel: 239, none: 251}},
		{meFixes, false, boardTally{all: 1567, some: 48, noChannel: 239, none: 189, wrongCPU: 8}},
	} {
		old := SetFixes(c.fixes)
		got := tallyPopulation(lists, c.known)
		SetFixes(old)
		if got != c.want {
			t.Errorf("fixes %v, sockets known %v: %+v, want %+v", c.fixes != 0, c.known, got, c.want)
		}
	}
}

// maker is the name a dump gives its system maker, in one form.
func maker(v string) string {
	l := strings.ToLower(v)
	for _, m := range []struct{ prefix, name string }{
		{"dell", "Dell"}, {"hpe", "HPE"}, {"hewlett packard enterprise", "HPE"}, {"hp", "HP"}, {"supermicro", "Supermicro"},
		{"huawei", "Huawei"}, {"xfusion", "xFusion"}, {"lenovo", "Lenovo"}, {"ibm", "IBM"}, {"intel", "Intel"},
		{"inspur", "Inspur"}, {"gigabyte", "Gigabyte"}, {"asus", "ASUS"}, {"asrock", "ASRock"}, {"quanta", "Quanta"},
		{"sugon", "Sugon"}, {"fujitsu", "Fujitsu"}, {"cisco", "Cisco"}, {"tyan", "Tyan"},
	} {
		if strings.HasPrefix(l, m.prefix) {
			return m.name
		}
	}
	return "other"
}

// TestPopulationByMaker holds, per system maker, how many dumps of the
// public collection have every slot placed, before and after the fixes,
// with the socket count known (testdata/population-boards.txt).
func TestPopulationByMaker(t *testing.T) {
	lists := readPopulation(t)
	type row struct{ dumps, before, after int }
	rows := map[string]*row{}
	full := func(list []popSlot) bool {
		for _, x := range Place(ParseDmidecodeMemory(popText(list), nil, nil), list[0].sockets, 0) {
			if x.State != Placed {
				return false
			}
		}
		return true
	}
	for _, list := range lists {
		m := maker(list[0].vendor)
		if rows[m] == nil {
			rows[m] = &row{}
		}
		r := rows[m]
		r.dumps += list[0].dumps
		old := SetFixes(0)
		if full(list) {
			r.before += list[0].dumps
		}
		SetFixes(meFixes)
		if full(list) {
			r.after += list[0].dumps
		}
		SetFixes(old)
	}
	var names []string
	for n := range rows {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if rows[names[i]].dumps != rows[names[j]].dumps {
			return rows[names[i]].dumps > rows[names[j]].dumps
		}
		return names[i] < names[j]
	})
	var b strings.Builder
	b.WriteString("maker\tdumps\tplaced in full before\tafter\n")
	var tot row
	for _, n := range names {
		r := rows[n]
		fmt.Fprintf(&b, "%s\t%d\t%d\t%d\n", n, r.dumps, r.before, r.after)
		tot.dumps, tot.before, tot.after = tot.dumps+r.dumps, tot.before+r.before, tot.after+r.after
	}
	fmt.Fprintf(&b, "all\t%d\t%d\t%d\n", tot.dumps, tot.before, tot.after)
	testutil.Golden(t, "population-boards.txt", []byte(b.String()))
}

// The counts of docs/compatibility.md are those of the population, with the
// socket count of each dump, without the fixes and with them.
func TestPopulationInTheDocs(t *testing.T) {
	doc, err := os.ReadFile(testutil.RepoRoot(t) + "/docs/compatibility.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(doc)
	lists := readPopulation(t)
	dumps := 0
	for _, l := range lists {
		dumps += l[0].dumps
	}
	for _, want := range []string{commas(dumps) + " dumps with memory devices", commas(len(lists)) + " distinct slot lists", "commit `57b21bc`"} {
		if !strings.Contains(text, want) {
			t.Errorf("docs/compatibility.md lacks %q", want)
		}
	}
	for _, c := range []struct {
		row   string
		fixes Fix
	}{{"first version", 0}, {"first round of fixes", meFixes}, {"this release", AllFixes}} {
		old := SetFixes(c.fixes)
		b := tallyPopulation(lists, true)
		SetFixes(old)
		row := fmt.Sprintf("| %s | %s | %s | %s | %s |", c.row, commas(b.all), commas(b.some), commas(b.noChannel), commas(b.none))
		if !strings.Contains(text, row+"\n") {
			t.Errorf("docs/compatibility.md lacks the row %q", row)
		}
	}
}

// commas writes a count with thousands separated, as the docs write it.
func commas(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// TestPopulationNames: every slot of a dump that no board family places
// gets from the reader the place the file expects: the names spell it.
func TestPopulationNames(t *testing.T) {
	lists := readPopulation(t)
	family := map[int]string{}
	for _, d := range readDumps(t) {
		family[d.list] = d.family
	}
	checked, dumps := 0, 0
	for _, list := range lists {
		if family[list[0].list] != "" {
			continue
		}
		d, f := ReadSlots(WriteSlotsOf(popHost(list), slotTexts(list)), nil, nil)
		if len(d) != len(list) {
			t.Fatalf("list %d: %d slots, want %d", list[0].list, len(d), len(list))
		}
		for i, s := range list {
			if diff := slotDiff(d[i], f, s); diff != "" {
				t.Errorf("list %d slot %q|%q: %s", s.list, s.locator, s.bank, diff)
			}
			checked++
		}
		dumps += list[0].dumps
	}
	if checked == 0 {
		t.Fatal("no list without a family")
	}
	t.Logf("%d slots of %d dumps", checked, dumps)
}
