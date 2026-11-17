package explorer

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func dellText(processor string, locators ...string) string {
	return "System Information\n\tManufacturer: Dell Inc.\n\nProcessor Information\n\tVersion: " + processor + "\n\n" + slotsOf(locators...)
}

func numbered(prefix string, from, to int) []string {
	var l []string
	for i := from; i <= to; i++ {
		l = append(l, fmt.Sprintf("%s%d", prefix, i))
	}
	return l
}

// Each fix with an input it changes: the result with every fix, and the
// result with every fix but this one, which is what the first
// implementation gives for the part the fix corrects. A fix of the first
// round is shown with the pass of FixNameForms off.
func TestFixes(t *testing.T) {
	dimms := func(s string) string { return places(ParseDmidecodeMemory(s, nil, nil)) }
	numastat := "                           node0           node1\nnuma_hit                     1               2\n" +
		"                           node2           node3\nnuma_hit                     3               4\n"
	lshwA0 := "*-memory\n     description: System Memory\n"
	for i, s := range []string{"A0", "A1", "A2", "A3"} {
		lshwA0 += fmt.Sprintf("   *-bank:%d\n        description: DIMM DDR2 Synchronous 800 MHz\n        slot: %s\n        size: 1GiB\n", i, s)
	}
	for _, c := range []struct {
		fix       Fix
		run       func() string
		want, was string
	}{
		{FixBinarySizes, func() string {
			return NumString(float64(ParseDmidecodeMemory(block("A1", "x", "16 GiB"), nil, nil)[0].SizeBytes))
		}, "17179869184", "0"},
		{FixDimmWord, func() string { return dimms(slotsOf("CPU0_DIMM_A1", "CPU0_DIMM_B1", "CPU1_DIMM_A1")) }, "0/0 0/1 1/0", "0/3 0/3 1/3"},
		{FixNumberingBase, func() string {
			return dimms(block("DIMM 0", "P0 CHANNEL A", "16 GB") + block("DIMM 0", "P0 CHANNEL B", "16 GB") + block("DIMM 0", "P1 CHANNEL A", "16 GB"))
		}, "0/0 0/1 1/0", "-1/0 -1/1 0/0"},
		{FixLetterBeforeNode, func() string {
			return dimms(block("CPU0_DIMM_A1", "NODE 1", "16 GB") + block("CPU0_DIMM_B1", "NODE 1", "16 GB") + block("CPU0_DIMM_C1", "NODE 1", "16 GB"))
		}, "0/0 0/1 0/2", "0/1 0/1 0/1"},
		{FixCPUWithSpace, func() string { return dimms(block("DIMM 1", "CPU 1", "16 GB") + block("DIMM 13", "CPU 2", "16 GB")) }, "0/0 1/0", "0/0 0/0"},
		{FixPLetter, func() string {
			return dimms(block("DIMM_P0_A0", "BANK 0", "16 GB") + block("DIMM_P1_B0", "BANK 1", "16 GB"))
		}, "0/0 1/1", "0/15 0/15"},
		{FixDellChannels, func() string { return dimms(dellText("AMD EPYC 9254 24-Core Processor", numbered("A", 1, 12)...)) },
			"0/0 0/1 0/2 0/3 0/4 0/5 0/6 0/7 0/8 0/9 0/10 0/11", "0/0 0/1 0/2 0/3 0/4 0/5 0/0 0/1 0/2 0/3 0/4 0/5"},
		{FixMoreSockets, func() string {
			l := Board(ParseDmidecodeMemory(slotsOf("CPU0_C0D0", "CPU1_C0D0", "CPU2_C0D0", "CPU3_C0D0"), nil, nil), nil)
			return fmt.Sprint(len(l.Rows), " rows")
		}, "2 rows", "1 rows"},
		{FixEdacControllers, func() string {
			r := MemoryMap(MapInput{Dmidecode: slotsOf("A1", "A2", "B1"), EdacCE: "dimm0:5\ndimm1:7\ndimm0:2\n"})
			return fmt.Sprint(r.Dimms[0].CeCount, r.Dimms[1].CeCount, r.Dimms[2].CeCount)
		}, "0 0 0", "2 7 0"},
		{FixLineEnds, func() string {
			return fmt.Sprint(len(ParseDmidecodeMemory(strings.ReplaceAll(slotsOf("A1", "A2"), "\n", "\r\n"), nil, nil)))
		}, "2", "1"},
		{FixEmptyField, func() string {
			d := ParseDmidecodeMemory("Memory Device\n\tSize: 16 GB\n\tLocator:\n\tBank Locator: P0_Node0_Channel1_Dimm0\n", nil, nil)
			return fmt.Sprintf("%q", d[0].Locator)
		}, `""`, `"Bank Locator: P0_Node0_Channel1_Dimm0"`},
		{FixNumastatBlocks, func() string {
			n := ParseNumaInfo("node 0 size: 10 MB\nnode 1 size: 10 MB\nnode 2 size: 10 MB\nnode 3 size: 10 MB\n", numastat)
			return fmt.Sprint(n[0].NumaHit, n[1].NumaHit, n[2].NumaHit, n[3].NumaHit)
		}, "1 2 3 4", "3 4 0 0"},
		{FixSupermicroNode, func() string {
			return dimms(block("P1-DIMMA1", "P0_Node0_Channel0_Dimm0", "16 GB") + block("P2-DIMME1", "P1_Node1_Channel0_Dimm0", "16 GB"))
		}, "0/0 1/0", "0/0 1/4"},
		{FixSlabBySize, func() string {
			return ParseSlabinfo(SlabTop("slabinfo - version: 2.1\nbig 10 10 100000 1 1\nmany 900 900 8 1 1\n"))[0].Name
		}, "big", "many"},
		{FixDellDimm, func() string {
			return dimms(dellText("Intel(R) Xeon(R) CPU X5650 @ 2.67GHz", "DIMM_A1", "DIMM_A2", "DIMM_A3", "DIMM_A4", "DIMM_B1", "DIMM_B2"))
		},
			"0/0 0/1 0/2 0/0 1/0 1/1", "0/0 0/0 0/0 0/0 0/1 0/1"},
		{FixChanWord, func() string {
			return dimms(block("DIMM0", "CHAN A DIMM 0", "16 GB") + block("DIMM0", "CHAN B DIMM 0", "16 GB"))
		}, "0/0 0/1", "0/0 0/0"},
		{FixLettersFromA, func() string {
			return dimms(slotsOf("D0", "D1", "D2", "D3")) + " | " + places(ParseLshwDimms(lshwA0, nil, nil))
		}, "0/0 0/0 0/0 0/0 | 0/0 0/1 0/0 0/1", "3/-1 3/0 3/1 3/0 | 0/-1 0/0 0/1 0/0"},
		{FixRepeatedTable, func() string {
			text := fixtureText(t, "linuxhw-ts100-e7-pi4-e88885")
			return fmt.Sprint(len(ParseDmidecodeMemory(text, nil, nil)), " slots, ", len(hintsOf(text).cpus), " processor records")
		}, "4 slots, 1 processor records", "8 slots, 2 processor records"},
		{FixNameForms, func() string {
			return dimms(board(xeonE5, 2, "CPU1_DIMM_A1|P0 CHANNEL A", "CPU2_DIMM_A1|P1 CHANNEL A"))
		}, "0/0 1/0", "1/0 2/0"},
		{FixLetterMeaning, func() string {
			return where(ParseDmidecodeMemory(board(xeonE5, 1, "P1-DIMM1A|P0_Node0_Channel0_Dimm0", "P1-DIMM1B|P0_Node0_Channel1_Dimm0"), nil, nil))
		}, "0/A/0 0/B/0", "0/0/0 0/1/0"},
		{FixSocketRules, func() string {
			return where(ParseDmidecodeMemory(board(xeonE5, 2, "CPU0_DIMM_A1", "CPU1_DIMM_A1"), nil, nil))
		}, "0/A/0 1/A/0", "no-socket no-socket"},
		{FixChannelRank, func() string {
			var out []string
			for _, d := range ParseDmidecodeMemory(board(xeonE5, 1, "DIMM_A1", "DIMM_C1", "DIMM_C2"), nil, nil) {
				out = append(out, fmt.Sprintf("%v.%v", d.Channel, d.Position))
			}
			return strings.Join(out, " ")
		}, "0.0 1.0 1.1", "0.1 2.1 2.2"},
	} {
		t.Run(c.fix.Name(), func(t *testing.T) {
			// The fixes of the first round correct the readers of the first
			// round: they are shown with the pass of the second round off,
			// which replaces what they place.
			base := AllFixes
			if c.fix < FixRepeatedTable {
				base &^= FixNameForms
			}
			old := SetFixes(base)
			defer SetFixes(old)
			if got := c.run(); got != c.want {
				t.Errorf("with every fix: %s, want %s", got, c.want)
			}
			SetFixes(base &^ c.fix)
			if got := c.run(); got != c.was {
				t.Errorf("without %s: %s, want %s", c.fix.Name(), got, c.was)
			}
		})
	}
	if len(Fixes()) != 22 {
		t.Errorf("%d fixes; every fix needs a case here", len(Fixes()))
	}
}

// fixtureText is the dmidecode text of a fixture of testdata/explorer.
func fixtureText(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(fixtureRoot + "/" + name + "/dmidecode.txt")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestProcessorChannels(t *testing.T) {
	for name, want := range map[string]int{
		"AMD EPYC 9254 24-Core Processor": 12, "AMD EPYC 7763 64-Core Processor": 8, "AMD EPYC 8534P": 6,
		"Intel(R) Xeon(R) CPU E5-2683 v3 @ 2.00GHz": 4, "Intel(R) Xeon(R) CPU X5650 @ 2.67GHz": 3,
		"Intel(R) Xeon(R) Gold 6126 CPU @ 2.60GHz": 6, "Intel(R) Xeon(R) Gold 6248 CPU": 6, "Intel(R) Xeon(R) Gold 6330 CPU": 8,
		"Intel(R) Xeon(R) Gold 6526Y": 8, "Intel(R) Xeon(R) 6980P": 12, "Intel(R) Xeon(R) 6767P": 8,
		"Intel(R) Xeon(R) CPU E3-1240 v6": 2, "Intel(R) Xeon(R) E-2278G": 2, "Example CPU": 0,
	} {
		if got, _ := ProcessorChannels(name); got != want {
			t.Errorf("%q: %d channels, want %d", name, got, want)
		}
	}
}
