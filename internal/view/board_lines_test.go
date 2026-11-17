package view

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/explorer"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/testutil"
)

// populationBoard draws the board of a dump of the public collection as a
// ledger of it gives it: its slot list and host from
// testdata/explorer/population.
func populationBoard(t *testing.T, lang i18n.Lang, dump string) *Board {
	t.Helper()
	root := testutil.Testdata(t, "explorer", "population")
	dumps, err := os.ReadFile(filepath.Join(root, "dumps.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	list := ""
	for _, line := range strings.Split(string(dumps), "\n") {
		if p := strings.Split(line, "\t"); len(p) == 3 && p[0] == dump {
			list = p[1]
		}
	}
	if list == "" {
		t.Fatalf("no dump %s", dump)
	}
	text, err := os.ReadFile(filepath.Join(root, "slots.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	var bh boardHost
	var slots []model.Slot
	for _, line := range strings.Split(string(text), "\n") {
		p := strings.Split(line, "\t")
		if len(p) != 23 || p[0] != list {
			continue
		}
		fmt.Sscanf(p[2], "%d", &bh.sockets)
		bh.vendor, bh.product, bh.board, bh.processor, bh.signature, bh.present = p[3], p[4], p[5], p[6], p[7], p[8]
		n := len(slots)
		slots = append(slots, slotNamed(fmt.Sprintf("%s#%d", p[11], n), p[11], p[12], 0x1100+n, p[9] == "1"))
	}
	return boardOf(lang, &engine.HostResult{Host: "h"}, slots, bh)
}

// madeBoard draws a made-up board: its host and its slots
// ("locator|bank", + for a module).
func madeBoard(lang i18n.Lang, bh boardHost, names ...string) *Board {
	var slots []model.Slot
	for i, n := range names {
		pop := strings.HasPrefix(n, "+")
		l, b, _ := strings.Cut(strings.TrimPrefix(n, "+"), "|")
		slots = append(slots, slotNamed(fmt.Sprintf("%s#%d", l, i), l, b, 0x1100+i, pop))
	}
	return boardOf(lang, &engine.HostResult{Host: "h"}, slots, bh)
}

// Every line of data quality the board writes, each on a board that has
// it, in English and Korean.
func TestBoardLinesOfTheChecks(t *testing.T) {
	e5 := "Intel(R) Xeon(R) CPU E5-2630 v4 @ 2.20GHz"
	for _, lang := range []i18n.Lang{i18n.EN, i18n.KO} {
		lines := map[string]bool{}
		add := func(b *Board) {
			for _, l := range b.Lines {
				lines[l.Code] = true
				if l.Text == "" || strings.Contains(l.Text, "{") {
					t.Errorf("%s: %s reads %q", lang, l.Code, l.Text)
				}
			}
		}
		for _, dump := range []string{
			"02C917C9A536", // Intel S5000PSL: memory on the chipset
			"17C5EE5E5187", // ETegro Hyperion RS125 G4: channel letters split over the processors
			"560D5AFC1336", // Lenovo HR630X: a bank locator against its locator
			"38BECEA381EC", // Supermicro X10DRi: empty slots named by the manual
			"9417D45F0FED", // AppliedMicro X-Gene: no channel count
			"35A3317203E1", // Supermicro X8DTH: modules on an absent processor
			"10A6C2EC2170", // Dell PowerEdge R910: no document says which processor owns a riser
			"F9FA241A07BB", // Dell CS24-SC: no document
			"0D53C64FD948", // HP ProLiant DL360 G5: the document gives no channel per slot
			"5E891354636F", // a Dell board known by a part number no named dump carries
			"FE20BCAD9039", // Dell C6100: names the document does not carry
			"38FCB3A1E58B", // Sun Fire X4270: one name for two slots
			"2114858A8611", // Phytium FT-2000/4: two slots in one place
			"58FA2062A619", // Tyan S4885: numbered slots without a table
			"0013715F7C60", // Dell PowerEdge R340: the table of the family
		} {
			add(populationBoard(t, lang, dump))
		}
		fujitsu := boardHost{vendor: "FUJITSU", product: "PRIMERGY TX200 S5", board: "FUJITSU D2709", processor: e5, sockets: 2, present: "11"}
		add(madeBoard(lang, fujitsu, "+DIMM-1A", "DIMM-3A"))
		add(madeBoard(lang, fujitsu, "+DIMM-1A", "+DIMM-3A"))
		two := boardHost{vendor: "Example Systems", product: "E1", processor: e5, sockets: 2, present: "11"}
		add(madeBoard(lang, two, "DIMM 1|NODE 0", "DIMM 2|NODE 1", "DIMM 3|NODE 2"))
		add(madeBoard(lang, two, "Slot#7 (front)|", "CPU0_DIMM_A1|"))
		add(madeBoard(lang, two, "CPU0_DIMM_A1", "CPU0_DIMM_B1", "CPU0_DIMM_C1", "CPU0_DIMM_D1", "CPU0_DIMM_E1"))
		gold := boardHost{vendor: "Example Systems", product: "E1", processor: "Intel(R) Xeon(R) Gold 6130 CPU @ 2.10GHz", sockets: 1, present: "1"}
		add(madeBoard(lang, gold, "CPU0_DIMM_A1", "CPU0_DIMM_A2", "CPU0_DIMM_A3"))
		for _, code := range []string{"board.hub", "board.family", "board.letters", "board.bank_disagrees", "board.labels",
			"board.phantoms", "board.phantoms_populated", "board.nodes", "board.unchecked", "board.absent",
			"board.why.no-processor-in-document", "board.why.no-document", "board.why.no-channel-in-document", "board.why.board-unknown",
			"board.why.names-not-in-document", "board.why.duplicated-names", "board.why.same-place", "board.why.unknown-form",
			"board.nochannel.no-table", "board.nochannel.more-channels", "board.nochannel.crowd"} {
			if !lines[code] {
				t.Errorf("%s: no board wrote %s", lang, code)
			}
		}
	}
}

// The board of memory on the chipset: one controller in the place of the
// CPUs, no stick on a CPU, and the channels the family table gives.
func TestBoardOfChipsetMemory(t *testing.T) {
	b := fixtureBoard(t, testutil.Testdata(t, "explorer", "linuxhw-poweredge-2950-121ae0"))
	if !b.Hub || !b.Complete.Full || b.Family != "dell-pe-1950-2950" || len(b.Documents) == 0 {
		t.Fatalf("hub %v, complete %+v, family %q", b.Hub, b.Complete, b.Family)
	}
	parts, sticks := 0, 0
	for _, r := range b.Rows {
		for _, p := range r.Parts {
			if p.CPU != nil {
				parts++
				if !p.CPU.Controller || len(p.CPU.Caches) != 0 {
					t.Errorf("the part in the place of the CPUs: %+v", p.CPU)
				}
			}
			for _, ch := range p.Channels {
				sticks += len(ch.Sticks)
			}
		}
	}
	if parts != 1 || sticks != 8 {
		t.Errorf("%d controllers, %d sticks", parts, sticks)
	}
}

// Empty slots without a name, drawn with the labels of the manual.
func TestBoardOfManualLabels(t *testing.T) {
	b := fixtureBoard(t, testutil.Testdata(t, "explorer", "netbox-sm-ssg-6028r"))
	labelled := 0
	for _, st := range sticksOf(b) {
		if st.Label != "" {
			labelled++
			if st.Place.State != explorer.Placed || st.State != SlotEmpty {
				t.Errorf("%s (%s): %+v", st.Slot, st.Label, st.Place)
			}
		}
	}
	if labelled != 12 || b.Complete.Full || b.Complete.Reason != explorer.ListUnnamed {
		t.Errorf("%d labelled, complete %+v", labelled, b.Complete)
	}
}
