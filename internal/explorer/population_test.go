package explorer

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/model"
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
		// This release with the socket count unknown (a ledger written before
		// the processor records were kept): one board draws a CPU its records
		// lack, the IBM System x3650 M2 whose firmware writes one processor
		// record and whose family table puts slots 9 to 16 on the second
		// processor of the board.
		{AllFixes, false, boardTally{all: 1665, some: 25, noChannel: 6, none: 347, wrongCPU: 1}},
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

// docs/compatibility.md names the collection as the population file holds
// it; TestPopulationCountsInTheDocs holds its counts.
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
	for _, want := range []string{commas(dumps) + " dumps with memory devices", commas(len(lists)) + " distinct slot lists", "commit `57b21bc`",
		commas(len(families)) + " rows", commas(len(familyCited())) + " documents"} {
		if !strings.Contains(text, want) {
			t.Errorf("docs/compatibility.md lacks %q", want)
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

// TestPopulationExpected: every slot of the public collection gets from the
// reader and the checks of the board the state, reason, socket, channel,
// channel name, position, DIMM index and way of placing the file expects.
func TestPopulationExpected(t *testing.T) {
	lists := readPopulation(t)
	slots := 0
	for _, list := range lists {
		d, f := ReadSlots(WriteSlotsOf(popHost(list), slotTexts(list)), nil, nil)
		if len(d) != len(list) {
			t.Fatalf("list %d: %d slots, want %d", list[0].list, len(d), len(list))
		}
		p := PlaceBoard(d, f)
		for i, s := range list {
			if diff := slotDiff(d[i], f, s); diff != "" {
				t.Errorf("list %d slot %q|%q: %s", s.list, s.locator, s.bank, diff)
			}
			got := p[i].State + "/" + p[i].Reason
			if p[i].State == Placed && d[i].Label != "" {
				got = Placed + "/label"
			}
			if want := s.state + "/" + s.reason; got != want {
				t.Errorf("list %d slot %q|%q: %s, want %s", s.list, s.locator, s.bank, got, want)
			}
			slots++
		}
	}
	if slots != popSlots {
		t.Errorf("%d slots, want %d", slots, popSlots)
	}
}

// listed is one line of testdata/explorer/population/unplaceable.tsv.
type listed struct{ reason, evidence string }

func readUnplaceable(t testing.TB) map[string]listed {
	t.Helper()
	b, err := os.ReadFile(fixtureRoot + "/population/unplaceable.tsv")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]listed{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "dump\t") {
			continue
		}
		p := strings.Split(line, "\t")
		if len(p) != 3 || p[2] == "" {
			t.Fatalf("unplaceable.tsv: %q", line)
		}
		if _, dup := out[p[0]]; dup {
			t.Fatalf("unplaceable.tsv: %s twice", p[0])
		}
		out[p[0]] = listed{p[1], p[2]}
	}
	return out
}

// judged is how the board takes one dump: placed in full, or the reason it
// is not.
type judged struct {
	full   bool
	reason string
}

// separateList checks the dumps against the separate list: every dump is
// placed in full or listed with the reason the board gives; no listed dump is
// placed in full; a listed dump without a slot list has no memory device.
func separateList(dumps map[string]judged, list map[string]listed) []string {
	var out []string
	for id, j := range dumps {
		l, ok := list[id]
		switch {
		case j.full && ok:
			out = append(out, id+": placed in full, and listed as "+l.reason)
		case !j.full && !ok:
			out = append(out, id+": not placed in full ("+j.reason+"), and not listed")
		case !j.full && l.reason != j.reason:
			out = append(out, id+": listed as "+l.reason+", the board says "+j.reason)
		}
	}
	for id, l := range list {
		if _, ok := dumps[id]; !ok && l.reason != ListNoDevice {
			out = append(out, id+": listed as "+l.reason+", and no slot list")
		}
	}
	sort.Strings(out)
	return out
}

// minPlacedInFull is the least number of dumps the board places in full.
const minPlacedInFull = 1934

// TestPopulationFull: every dump of the public collection is placed in full
// by the check of the board, or stands in the separate list with the reason
// the board gives; the counts per maker and per reason are those of
// internal/explorer/testdata/population-boards.txt.
func TestPopulationFull(t *testing.T) {
	lists := readPopulation(t)
	dumps := readDumps(t)
	list := readUnplaceable(t)
	byList := map[int][]string{}
	for id, d := range dumps {
		byList[d.list] = append(byList[d.list], id)
	}
	type row struct{ dumps, first, me, full, listed int }
	rows := map[string]*row{}
	reasons := map[string]int{}
	judgedDumps := map[string]judged{}
	allPlaced := func(list []popSlot) bool {
		for _, x := range Place(ParseDmidecodeMemory(popText(list), nil, nil), list[0].sockets, 0) {
			if x.State != Placed {
				return false
			}
		}
		return true
	}
	for _, sl := range lists {
		m := maker(sl[0].vendor)
		if rows[m] == nil {
			rows[m] = &row{}
		}
		r := rows[m]
		n := sl[0].dumps
		r.dumps += n
		old := SetFixes(0)
		if allPlaced(sl) {
			r.first += n
		}
		SetFixes(meFixes)
		if allPlaced(sl) {
			r.me += n
		}
		SetFixes(old)
		d, f := ReadSlots(WriteSlotsOf(popHost(sl), slotTexts(sl)), nil, nil)
		c := Complete(d, f, PlaceBoard(d, f))
		if c.Full {
			r.full += n
		} else {
			r.listed += n
			reasons[c.Reason] += n
		}
		for _, id := range byList[sl[0].list] {
			judgedDumps[id] = judged{c.Full, c.Reason}
		}
	}
	for _, e := range separateList(judgedDumps, list) {
		t.Error(e)
	}
	// The dumps without a memory device have no slot list.
	for _, l := range list {
		if l.reason == ListNoDevice {
			reasons[ListNoDevice]++
			if rows["other"] == nil {
				rows["other"] = &row{}
			}
			rows["other"].dumps++
			rows["other"].listed++
		}
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
	b.WriteString("# The dumps of the public collection by system maker: every slot placed by the first version and by the\n")
	b.WriteString("# first round of fixes (the checks of the first round), placed in full by this release, and in the separate list.\n")
	b.WriteString("maker\tdumps\tfirst version\tfirst round\tplaced in full\tseparate list\n")
	var tot row
	for _, n := range names {
		r := rows[n]
		fmt.Fprintf(&b, "%s\t%d\t%d\t%d\t%d\t%d\n", n, r.dumps, r.first, r.me, r.full, r.listed)
		tot.dumps, tot.first, tot.me, tot.full, tot.listed = tot.dumps+r.dumps, tot.first+r.first, tot.me+r.me, tot.full+r.full, tot.listed+r.listed
	}
	fmt.Fprintf(&b, "all\t%d\t%d\t%d\t%d\t%d\n", tot.dumps, tot.first, tot.me, tot.full, tot.listed)
	b.WriteString("\n# The separate list by reason.\nreason\tdumps\n")
	var rs []string
	for r := range reasons {
		rs = append(rs, r)
	}
	sort.Slice(rs, func(i, j int) bool {
		if reasons[rs[i]] != reasons[rs[j]] {
			return reasons[rs[i]] > reasons[rs[j]]
		}
		return rs[i] < rs[j]
	})
	for _, r := range rs {
		fmt.Fprintf(&b, "%s\t%d\n", r, reasons[r])
	}
	b.WriteString("\n# How the boards are drawn when the socket count is not known (dmidecode -t memory text, a ledger written\n")
	b.WriteString("# before the processor records were kept): every slot placed, some, without channels, none, a CPU the dump lacks.\n")
	b.WriteString("fixes\tall\tsome\tno channels\tnone\twrong CPU\n")
	for _, c := range []struct {
		name  string
		fixes Fix
	}{{"first version", 0}, {"first round", meFixes}, {"this release", AllFixes}} {
		old := SetFixes(c.fixes)
		x := tallyPopulation(lists, false)
		SetFixes(old)
		fmt.Fprintf(&b, "%s\t%d\t%d\t%d\t%d\t%d\n", c.name, x.all, x.some, x.noChannel, x.none, x.wrongCPU)
		// One board of two sockets whose firmware writes one processor
		// record (TestPopulationBoards).
		if c.fixes == AllFixes && x.wrongCPU > 1 {
			t.Errorf("with the socket count unknown, %d boards draw a CPU the dump does not have", x.wrongCPU)
		}
	}
	if tot.full < minPlacedInFull {
		t.Errorf("%d dumps placed in full, at least %d were", tot.full, minPlacedInFull)
	}
	if tot.full+tot.listed != len(dumps)+1 {
		t.Errorf("%d placed in full and %d listed, of %d dumps", tot.full, tot.listed, len(dumps)+1)
	}
	testutil.Golden(t, "population-boards.txt", []byte(b.String()))
}

// The check of the separate list fails when a listed dump loses its row,
// when a listed dump is placed in full, when a dump placed in full is
// listed, and when the reasons differ.
func TestSeparateListCheck(t *testing.T) {
	dumps := map[string]judged{"A": {true, ""}, "B": {false, ListUnnamed}, "C": {false, ListAbsent}}
	list := map[string]listed{"B": {ListUnnamed, "e"}, "C": {ListAbsent, "e"}, "L": {ListNoDevice, "e"}}
	if e := separateList(dumps, list); len(e) != 0 {
		t.Fatalf("as listed: %v", e)
	}
	for name, change := range map[string]func(d map[string]judged, l map[string]listed){
		"a row removed":         func(d map[string]judged, l map[string]listed) { delete(l, "B") },
		"a listed dump placed":  func(d map[string]judged, l map[string]listed) { d["C"] = judged{true, ""} },
		"a placed dump listed":  func(d map[string]judged, l map[string]listed) { l["A"] = listed{ListDuplicates, "e"} },
		"another reason":        func(d map[string]judged, l map[string]listed) { l["C"] = listed{ListDuplicates, "e"} },
		"a listed dump unknown": func(d map[string]judged, l map[string]listed) { l["Z"] = listed{ListAbsent, "e"} },
	} {
		d, l := map[string]judged{}, map[string]listed{}
		for k, v := range dumps {
			d[k] = v
		}
		for k, v := range list {
			l[k] = v
		}
		change(d, l)
		if e := separateList(d, l); len(e) == 0 {
			t.Errorf("%s: no error", name)
		}
	}
}

// sampleSlot is one line of testdata/explorer/population/sample.tsv.
type sampleSlot struct {
	family, dump, locator, bank, processor, channel, position, source, page string
}

// TestPopulationSample: the slots placed by hand from the vendor's
// documents, one dump of every board family that places slots. The board
// keeps the grouping (the slots of one channel of the document share one
// channel of the board and no other), the processors up to one offset, and
// the positions.
func TestPopulationSample(t *testing.T) {
	b, err := os.ReadFile(fixtureRoot + "/population/sample.tsv")
	if err != nil {
		t.Fatal(err)
	}
	bySample := map[[2]string][]sampleSlot{}
	var order [][2]string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "family\t") {
			continue
		}
		p := strings.Split(line, "\t")
		if len(p) != 9 {
			t.Fatalf("sample.tsv: %q", line)
		}
		s := sampleSlot{p[0], p[1], p[2], p[3], p[4], p[5], p[6], p[7], p[8]}
		if _, ok := sources[s.source]; !ok || s.page == "" {
			t.Errorf("sample of %s: source %q page %q", s.family, s.source, s.page)
		}
		k := [2]string{s.family, s.dump}
		if _, ok := bySample[k]; !ok {
			order = append(order, k)
		}
		bySample[k] = append(bySample[k], s)
	}
	families := map[string]bool{}
	slots := 0
	for _, k := range order {
		samples := bySample[k]
		list := popList(t, k[1])
		d, f := ReadSlots(WriteSlotsOf(popHost(list), slotTexts(list)), nil, nil)
		p := PlaceBoard(d, f)
		docToBoard, boardToDoc := map[string]string{}, map[string]string{}
		offset := map[int]bool{}
		for _, s := range samples {
			i := -1
			bank := s.bank
			if model.Placeholder(bank) {
				bank = ""
			}
			for j, x := range list {
				if x.locator == s.locator && x.bank == bank {
					i = j
				}
			}
			if i < 0 {
				t.Errorf("%s %s: no slot %q|%q", k[0], k[1], s.locator, s.bank)
				continue
			}
			if p[i].State != Placed {
				t.Errorf("%s %s %s: %s/%s, the document places it", k[0], k[1], s.locator, p[i].State, p[i].Reason)
				continue
			}
			slots++
			board := fmt.Sprintf("%d/%d", p[i].Socket, p[i].Channel)
			if f.Hub {
				board = "hub/" + strconv.Itoa(p[i].Channel)
			}
			if s.channel != "-" && s.channel != "" {
				doc := s.processor + "/" + s.channel
				if b, ok := docToBoard[doc]; ok && b != board {
					t.Errorf("%s %s %s: channel %s of the document is %s and %s on the board", k[0], k[1], s.locator, doc, b, board)
				}
				if dd, ok := boardToDoc[board]; ok && dd != doc {
					t.Errorf("%s %s %s: board channel %s holds channels %s and %s of the document", k[0], k[1], s.locator, board, dd, doc)
				}
				docToBoard[doc], boardToDoc[board] = board, doc
			}
			if n, err := strconv.Atoi(s.processor); err == nil && !f.Hub {
				offset[p[i].Socket-n] = true
			}
			if s.position != "-" && s.position != "" && s.position != strconv.Itoa(p[i].Position) {
				t.Errorf("%s %s %s: position %d, the document %s", k[0], k[1], s.locator, p[i].Position, s.position)
			}
		}
		if len(offset) > 1 {
			t.Errorf("%s %s: the processors of the document and the board differ by %v", k[0], k[1], offset)
		}
		families[k[0]] = true
	}
	for _, fam := range families_() {
		if fam.kind == "table" || fam.kind == "global" || fam.kind == "cards" {
			if !families[fam.id] {
				t.Errorf("row %s (%s) has no sample", fam.id, fam.kind)
			}
		}
	}
	t.Logf("%d samples, %d slots", len(order), slots)
}

// families_ is the table of board families, for the tests.
func families_() []family { return families }
