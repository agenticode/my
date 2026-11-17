package explorer

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// TestPopulationFamilies: every dump gets the family the file names, and
// every row of the family table acts on at least one slot list.
func TestPopulationFamilies(t *testing.T) {
	family := map[int]string{}
	for _, d := range readDumps(t) {
		family[d.list] = d.family
	}
	used := map[string]int{}
	for _, list := range readPopulation(t) {
		_, f := ReadSlots(WriteSlotsOf(popHost(list), slotTexts(list)), nil, nil)
		for _, id := range []string{f.Family, f.Alias, f.Labels, f.PhantomRow} {
			if id != "" {
				used[id]++
			}
		}
		if want := family[list[0].list]; f.Family != want {
			t.Errorf("list %d (%s): family %q, want %q", list[0].list, list[0].product, f.Family, want)
		}
	}
	for _, fam := range families {
		if used[fam.id] == 0 {
			t.Errorf("row %s (%s) acts on no slot list", fam.id, fam.kind)
		}
	}
}

// The family table is whole: every row has a known kind and its documents,
// every document is cited, and a row that names a document sources.tsv
// lacks is refused.
func TestFamilyTable(t *testing.T) {
	if errs := checkFamilies(families, sources); len(errs) > 0 {
		t.Errorf("families.tsv: %v", errs)
	}
	cited := map[string]bool{}
	for _, f := range families {
		for _, s := range f.source {
			cited[s] = true
		}
	}
	for _, id := range sourceOrder {
		if !cited[id] {
			t.Errorf("sources.tsv: %s is cited by no row", id)
		}
		// A document whose copy was read without its address names its
		// number or date instead.
		if s := sources[id]; s.Title == "" || s.Pages == "" || s.Maker == "" || s.URL == "" && s.Number == "" && s.Date == "" ||
			s.URL != "" && !strings.HasPrefix(s.URL, "https://") && !strings.HasPrefix(s.URL, "http://") {
			t.Errorf("sources.tsv: %s lacks title, maker, pages, or both its address and its number: %+v", id, s)
		}
	}
	bad := strings.Replace(familiesTSV, "\tdell-e26s\n", "\tdell-e26s-missing\n", 1)
	rows, err := parseFamilies(bad)
	if err != nil {
		t.Fatal(err)
	}
	if errs := checkFamilies(rows, sources); len(errs) != 1 || !strings.Contains(errs[0].Error(), "dell-e26s-missing") {
		t.Errorf("a missing source: %v", errs)
	}
	if _, err := parseFamilies("x\tdell\t(\t\t\t\ttable\t\t\t\tdell-e26s\n"); err == nil {
		t.Error("a match that does not compile was taken")
	}
	if len(families) != 361 || len(sourceOrder) != 385 {
		t.Errorf("%d rows, %d documents", len(families), len(sourceOrder))
	}
}

// A row of a maker matches no board of another known maker.
func TestFamiliesOfOneMaker(t *testing.T) {
	seen := map[string]bool{}
	for _, list := range readPopulation(t) {
		s := list[0]
		h := textHints{sysVendor: s.vendor, product: s.product, board: s.board}
		key := asciiLower(s.product + "|" + s.board)
		maker := makerOf(h)
		for _, f := range families {
			// A board of no known maker is left to the rows of every maker.
			if f.maker == "*" || f.maker == maker || maker == "" || !f.re.MatchString(key) {
				continue
			}
			if k := f.id + "|" + key; !seen[k] {
				seen[k] = true
				t.Errorf("row %s of %s matches %q of %s", f.id, f.maker, key, maker)
			}
		}
	}
}

// slotsFor writes a made-up board of a family: its product and board, its
// processor records and its slots ("locator|bank", + for a module).
func slotsFor(vendor, product, board, cpu string, sockets int, names ...string) string {
	h := SlotHost{Vendor: vendor, Product: product, Board: board, Processor: cpu, Sockets: sockets, Present: strings.Repeat("1", sockets)}
	var s []SlotText
	for _, n := range names {
		pop := strings.HasPrefix(n, "+")
		l, b, _ := strings.Cut(strings.TrimPrefix(n, "+"), "|")
		s = append(s, SlotText{Locator: l, BankLocator: b, Populated: pop})
	}
	return WriteSlotsOf(h, s)
}

// Each kind of row with a made-up board of its family.
func TestFamilyKinds(t *testing.T) {
	read := func(text string) ([]Dimm, Facts) { return ReadSlots(text, nil, nil) }
	t.Run("table", func(t *testing.T) {
		// Dell PowerEdge R630: slot 5 of a processor is the second slot of
		// its first channel.
		d, f := read(slotsFor("Dell Inc.", "PowerEdge R630", "Dell Inc. 0CNCJW", xeonE5, 2, "A1", "A5", "A2", "B1"))
		if w := where(d); w != "0/0/0 0/0/1 0/1/0 1/0/0" || f.Family != "dell-13g-24slot" {
			t.Errorf("%s, family %q", w, f.Family)
		}
	})
	t.Run("a slot outside the table: the names are read as the generic rules read them", func(t *testing.T) {
		d, f := read(slotsFor("Dell Inc.", "PowerEdge R630", "Dell Inc. 0CNCJW", xeonE5, 2, "A1", "A13", "B1"))
		if f.Family != "" || d[0].PlaceBy == "family:dell-13g-24slot" {
			t.Errorf("family %q, %s", f.Family, where(d))
		}
		if w := where(d); w != "0/A/0 0/A/1 1/B/0" {
			t.Errorf("generic reading: %s", w)
		}
	})
	t.Run("global", func(t *testing.T) {
		// IBM System x3550 M4: the slot numbers run over both processors.
		d, f := read(slotsFor("IBM", "System x3550 M4 -[7914B2A]-", "IBM 00J6242", xeonE5, 2, "DIMM 1|Bank 1", "DIMM 13|Bank 13"))
		if f.Family == "" || d[0].Socket != 0 || d[1].Socket != 1 || !strings.HasPrefix(d[1].PlaceBy, "family:") {
			t.Errorf("%s, family %q", where(d), f.Family)
		}
	})
	t.Run("cards", func(t *testing.T) {
		// HP ProLiant DL580 G7: cartridges 1 and 2 on processor 1, 3 and 4 on processor 2.
		d, f := read(slotsFor("HP", "ProLiant DL580 G7", "", "Intel(R) Xeon(R) CPU E7- 4870  @ 2.40GHz", 4, "Board 1, DIMM 1A", "Board 3, DIMM 1A", "Board 2, DIMM 1A"))
		if f.Family != "hp-dl580-g7-cartridges" || d[0].Socket != 0 || d[1].Socket != 1 || d[2].Socket != 0 || d[0].ChannelName == d[2].ChannelName {
			t.Errorf("%s %s %s, family %q", where(d), d[0].ChannelName, d[2].ChannelName, f.Family)
		}
	})
	t.Run("names: which word is which processor", func(t *testing.T) {
		// Supermicro X8DTU: P1_ is the first processor, P0_ the second.
		d, f := read(slotsFor("Supermicro", "X8DTU", "Supermicro X8DTU", "Intel(R) Xeon(R) CPU E5620 @ 2.40GHz", 2, "P1_DIMM1A|BANK0", "P0_DIMM1A|BANK6"))
		if d[0].Socket != 0 || d[1].Socket != 1 || f.Family != "supermicro-x8dtu-12slot" {
			t.Errorf("%s, family %q", where(d), f.Family)
		}
	})
	t.Run("labels", func(t *testing.T) {
		// Supermicro X10DRi: the empty slots the firmware names NO DIMM take
		// the labels of the manual no named slot carries.
		names := []string{"+P1_DIMMA1|P0_Node0_Channel0_Dimm0", "+P2_DIMME1|P1_Node1_Channel0_Dimm0"}
		for i := 0; i < 14; i++ {
			names = append(names, "NO DIMM|NO DIMM")
		}
		d, f := read(slotsFor("Supermicro", "X10DRi", "Supermicro X10DRi", xeonE5, 2, names...))
		if f.Labels != "sm-x10dr-16-labels" || f.Labelled != 14 || d[2].Label != "P1-DIMMA2" || !d[2].Unnamed || d[2].PlaceBy == "" {
			t.Errorf("labels %q %d, slot %+v", f.Labels, f.Labelled, d[2])
		}
		// A module in an unnamed slot: the labels are not given.
		names[3] = "+NO DIMM|NO DIMM"
		d, f = read(slotsFor("Supermicro", "X10DRi", "Supermicro X10DRi", xeonE5, 2, names...))
		if f.Labelled != 0 || d[2].Label != "" {
			t.Errorf("a populated unnamed slot: %d labelled", f.Labelled)
		}
	})
	t.Run("phantoms", func(t *testing.T) {
		// Fujitsu PRIMERGY TX200 S5: no slot DIMM-3x on the board.
		d, f := read(slotsFor("FUJITSU", "PRIMERGY TX200 S5", "FUJITSU D2709", xeonE5, 2, "+DIMM-1A", "DIMM-3A", "DIMM-3B"))
		if d[1].NotSlot != NotSlotPhantom || d[2].NotSlot != NotSlotPhantom || f.Phantoms != 2 || f.PhantomRow != "fujitsu-d2709-phantoms" {
			t.Errorf("%+v %+v, facts %+v", d[1], d[2], f)
		}
		_, f = read(slotsFor("FUJITSU", "PRIMERGY TX200 S5", "FUJITSU D2709", xeonE5, 2, "+DIMM-1A", "+DIMM-3A"))
		if f.PhantomsPopulated != 1 {
			t.Errorf("a module in a record the board lacks: %+v", f)
		}
	})
	t.Run("alias", func(t *testing.T) {
		// A board named by its part number alone takes the rule of its family.
		d, f := read(slotsFor("", "", "Dell Inc. 0PXXHP", xeonE5, 2, "DIMM_A1", "DIMM_A5", "DIMM_B1"))
		if f.Alias != "dell-pn-0pxxhp" || f.Family != "dell-12g-24slot" || where(d) != "0/0/0 0/0/1 1/0/0" {
			t.Errorf("%s, facts %q %q", where(d), f.Alias, f.Family)
		}
	})
	t.Run("aside", func(t *testing.T) {
		d, f := read(slotsFor("Dell", "CS24-SC", "", xeonE5, 2, "DIMM1", "DIMM2"))
		if f.Aside != "no-document" || d[0].Aside != "no-document" || d[0].PlaceBy != "" {
			t.Errorf("%+v, facts %+v", d[0], f)
		}
	})
}

// The fix board-families is a stage of the pass: without it no row acts.
func TestFamiliesOff(t *testing.T) {
	text := slotsFor("Dell Inc.", "PowerEdge R630", "Dell Inc. 0CNCJW", xeonE5, 2, "A1", "A5", "B1")
	old := SetFixes(AllFixes &^ FixBoardFamilies)
	defer SetFixes(old)
	d, f := ReadSlots(text, nil, nil)
	if f.Family != "" || strings.HasPrefix(d[0].PlaceBy, "family:") {
		t.Errorf("without the fix: %+v, %s", f, where(d))
	}
	var ids []string
	for _, id := range familiesOf("PowerEdge R630", "Dell Inc. 0CNCJW", "dell") {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if fmt.Sprint(ids) != "[dell-13g-24slot]" {
		t.Errorf("rows of the R630: %v", ids)
	}
}
