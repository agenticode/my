package explorer

import (
	"strings"
	"testing"
)

// judge reads a text and places and judges its board.
func judge(text string) ([]Dimm, Facts, []Placement, Completeness) {
	d, f := ReadSlots(text, nil, nil)
	p := PlaceBoard(d, f)
	return d, f, p, Complete(d, f, p)
}

// judgeDump reads the slot list of a public dump as the ledger keeps it.
func judgeDump(t *testing.T, dump string) ([]Dimm, Facts, []Placement, Completeness) {
	t.Helper()
	list := popList(t, dump)
	return judge(WriteSlotsOf(popHost(list), slotTexts(list)))
}

// states counts the placements by state and reason.
func states(p []Placement) map[string]int {
	out := map[string]int{}
	for _, x := range p {
		out[x.State+"/"+x.Reason]++
	}
	return out
}

// Each check of the board with a public dump or a made-up board where it
// holds the board back, and a public dump where it does not.
func TestBoardChecks(t *testing.T) {
	full := func(name, dump string) {
		t.Helper()
		if _, _, p, c := judgeDump(t, dump); !c.Full {
			t.Errorf("%s (%s): not placed in full: %+v %v", name, dump, c, states(p))
		}
	}
	t.Run("same-place", func(t *testing.T) {
		// Phytium FT-2000/4: the firmware writes DIMM1 twice with one bank locator.
		_, _, p, c := judgeDump(t, "2114858A8611")
		if states(p)["unplaced/same-place"] == 0 || c.Full || c.Reason != ListDuplicates {
			t.Errorf("FT-2000/4: %v %+v", states(p), c)
		}
		full("Lenovo HR630X", "560D5AFC1336")
	})
	t.Run("no-table", func(t *testing.T) {
		// Tyan S4885: numbered slots on a board without a family table.
		_, _, p, c := judgeDump(t, "58FA2062A619")
		if states(p)["no-channel/no-table"] == 0 || c.Full {
			t.Errorf("S4885: %v %+v", states(p), c)
		}
		// Lenovo ThinkSystem SR650: numbered slots, and the family table gives the channels.
		_, _, p, c = judgeDump(t, "5D8AB08CFEB3")
		if states(p)["no-channel/no-table"] != 0 {
			t.Errorf("SR650: %v %+v", states(p), c)
		}
	})
	t.Run("more-channels", func(t *testing.T) {
		// Five channel letters on a Xeon E5 of four channels.
		_, _, p, _ := judge(board(xeonE5, 1, "CPU0_DIMM_A1", "CPU0_DIMM_B1", "CPU0_DIMM_C1", "CPU0_DIMM_D1", "CPU0_DIMM_E1"))
		if states(p)["no-channel/more-channels"] != 5 {
			t.Errorf("five channels: %v", states(p))
		}
		_, _, p, _ = judge(board(xeonE5, 1, "CPU0_DIMM_A1", "CPU0_DIMM_B1", "CPU0_DIMM_C1", "CPU0_DIMM_D1"))
		if states(p)["placed/"] != 4 {
			t.Errorf("four channels: %v", states(p))
		}
		full("Dell PowerEdge R340", "0013715F7C60")
	})
	t.Run("crowd", func(t *testing.T) {
		// Three slots in a channel of a Xeon Scalable, whose channels hold two.
		_, _, p, _ := judge(board(xeonGold, 1, "CPU0_DIMM_A1", "CPU0_DIMM_A2", "CPU0_DIMM_A3"))
		if states(p)["no-channel/crowd"] != 3 {
			t.Errorf("three in a channel of two: %v", states(p))
		}
		// Three slots in a channel of a Xeon E5, whose channels hold three.
		_, _, p, _ = judge(board(xeonE5, 1, "CPU0_DIMM_A1", "CPU0_DIMM_A2", "CPU0_DIMM_A3"))
		if states(p)["placed/"] != 3 {
			t.Errorf("three in a channel of three: %v", states(p))
		}
	})
	t.Run("unchecked", func(t *testing.T) {
		// AppliedMicro X-Gene 1: no channel count of the processor is known.
		_, _, p, c := judgeDump(t, "9417D45F0FED")
		if !c.Unchecked || c.Full || c.Reason != ListProcessorUnknown || states(p)["placed/"] == 0 {
			t.Errorf("X-Gene: %v %+v", states(p), c)
		}
		full("Dell PowerEdge R340", "0013715F7C60")
	})
	t.Run("absent", func(t *testing.T) {
		// Supermicro X8DTH: modules on the socket of a processor record that says not installed.
		_, _, _, c := judgeDump(t, "35A3317203E1")
		if c.Absent == 0 || c.Full || c.Reason != ListAbsent {
			t.Errorf("X8DTH: %+v", c)
		}
		// Supermicro X8DTU with one processor and its modules on it.
		full("X8DTU", "45AF16027E0C")
		// A ledger written before the populated flags: the check is skipped.
		d, f := ReadSlots(board(xeonE5, 2, "+CPU0_DIMM_A1", "+CPU1_DIMM_A1"), nil, nil)
		f.Present = []int{-1, -1}
		if c := Complete(d, f, PlaceBoard(d, f)); c.Absent != 0 {
			t.Errorf("unknown flags: %+v", c)
		}
	})
	t.Run("run", func(t *testing.T) {
		// IBM System x3650 M2 with one processor record: the table puts slots on a second processor.
		_, _, p, c := judgeDump(t, "B4AFB5C90D39")
		if states(p)["unplaced/run"] == 0 || c.Reason != ListRecordsMissing {
			t.Errorf("x3650 M2 with one record: %v %+v", states(p), c)
		}
		full("x3650 M2 with two records", "42AFC173F8EB")
	})
	t.Run("chipset", func(t *testing.T) {
		// Dell PowerEdge 2950: memory on the chipset, four channels of two.
		d, f, p, c := judge(fixtureText(t, "linuxhw-poweredge-2950-121ae0"))
		if !f.Hub || !c.Full || f.Channels != 4 || d[0].PlaceBy == "" || states(p)["placed/"] != 8 {
			t.Errorf("PowerEdge 2950: hub %v, %+v, %v", f.Hub, c, states(p))
		}
		// A front-side-bus processor on a board without a family row: no channel count.
		_, f, _, c = judge(board("Intel(R) Xeon(R) CPU            5160  @ 3.00GHz", 2, "DIMM_A1|Channel A", "DIMM_B1|Channel B"))
		if !f.Hub || c.Full || !c.Unchecked {
			t.Errorf("a chipset board without a family: hub %v, %+v", f.Hub, c)
		}
	})
}

// Complete names why a board is not placed in full, in the order of the
// separate list.
func TestCompleteReasons(t *testing.T) {
	for _, c := range []struct{ dump, want string }{
		{"38BECEA381EC", ListUnnamed},
		{"2114858A8611", ListDuplicates},
		{"35A3317203E1", ListAbsent},
		{"9417D45F0FED", ListProcessorUnknown},
		{"B4AFB5C90D39", ListRecordsMissing},
		{"10A6C2EC2170", "no-processor-in-document"},
		{"F9FA241A07BB", "no-document"},
	} {
		if _, _, _, got := judgeDump(t, c.dump); got.Full || got.Reason != c.want {
			t.Errorf("%s: %+v, want %s", c.dump, got, c.want)
		}
	}
	_, _, _, got := judge(slotsFor("FUJITSU", "PRIMERGY TX200 S5", "FUJITSU D2709", xeonE5, 2, "+DIMM-1A", "+DIMM-3A"))
	if got.Reason != ListPhantom {
		t.Errorf("a module in a record the board lacks: %+v", got)
	}
	d := []Dimm{{NotSlot: NotSlotFlash}}
	if got := Complete(d, Facts{Pass: true}, PlaceBoard(d, Facts{Pass: true})); got.Reason != ListNoDevice {
		t.Errorf("no slot at all: %+v", got)
	}
	if !strings.HasPrefix(ListPhantom, "module-") {
		t.Error(ListPhantom)
	}
}
