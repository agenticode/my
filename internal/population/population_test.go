package population

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Two rule sets written by hand for the tests. made-seq is a board of four
// channels of three slots per processor filled in one order, position 0
// first, with every rule; made-sets fills the last position first, lists
// four numbers of modules, has a table of another memory mode and a second
// set for one module.
const madeSets = `id	maker	match	when	first	order	strength	alt	rules	source
made-seq	made	^Made Seq\|	family=made-seq	0	seq: 0.0 1.0 2.0 3.0 0.1 1.1 2.1 3.1 0.2 1.2 2.2 3.2	req	-	P-FIRST-01=rec doc-a:10; P-CPU-01=req doc-a:11; P-BAL-01=rec doc-a:11; P-MIX-01=req doc-a:12; P-MIX-02=req doc-a:12; P-MIX-03=rec doc-a:13; P-MIX-04=rec doc-a:13; P-MIX-05=req doc-a:14	doc-a:10 doc-b:20
made-sets	made	^Made Sets\|	family=made-sets	last	mode=independent: sets: 1: 0.1; 2: 0.1 1.1; 4: 0.0 1.0 0.1 1.1	rec	mode=advanced-ecc: sets: 2: 0.1 0.0 || sets: 1: 1.1	P-FIRST-01=req doc-b:21; P-MIX-03=req doc-b:22	doc-b:21
made-rules	*	.	family=made-rules	0	-	-	-	P-MIX-04=rec doc-c:3	-
`

func testSets(t *testing.T) []Set {
	t.Helper()
	s, err := ParseSets(madeSets)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// made builds a board of family fam: socks processors, chans channels of
// per slots each; slot names run CPU<s>_<letter><position from 1>, the
// letters going on over the processors. mods are the modules by slot name.
func made(fam string, socks, chans, per int, mods map[string]Module) Board {
	product := map[string]string{"made-seq": "Made Seq", "made-sets": "Made Sets"}[fam]
	b := Board{Maker: "made", Product: product, BoardName: "X1", Family: fam, Sockets: socks, Full: true}
	for s := 0; s < socks; s++ {
		b.Present = append(b.Present, 1)
		for c := 0; c < chans; c++ {
			letter := string(rune('A' + s*chans + c))
			for p := 0; p < per; p++ {
				name := fmt.Sprintf("CPU%d_%s%d", s, letter, p+1)
				m, ok := mods[name]
				b.Slots = append(b.Slots, Slot{Name: name, Socket: s, Channel: c, Position: p, ChannelName: letter, Populated: ok, Module: m})
			}
		}
	}
	return b
}

// rdimm is a module of 16 GiB, two ranks, 2400 MT/s.
var rdimm = Module{Bytes: 16 << 30, Type: "RDIMM", Ranks: 2, Speed: 2400, Part: "EXM16G"}

func mods(names ...string) map[string]Module {
	out := map[string]Module{}
	for _, n := range names {
		out[n] = rdimm
	}
	return out
}

func with(m map[string]Module, name string, f func(*Module)) map[string]Module {
	x := m[name]
	f(&x)
	m[name] = x
	return m
}

// ids are the rule ids of the findings, the kinds of the notes and the
// rules not checked with their field.
func ids(r Result) (findings, notes, unchecked []string) {
	for _, f := range r.Findings {
		findings = append(findings, f.Rule)
	}
	for _, n := range r.Notes {
		notes = append(notes, n.Kind)
	}
	for _, u := range r.Unchecked {
		unchecked = append(unchecked, u.Rule+":"+u.Field)
	}
	return
}

func want(t *testing.T, name string, r Result, findings, notes, unchecked []string) {
	t.Helper()
	f, n, u := ids(r)
	if !reflect.DeepEqual(f, findings) || !reflect.DeepEqual(n, notes) || !reflect.DeepEqual(u, unchecked) {
		t.Errorf("%s: findings %v notes %v unchecked %v, want %v %v %v", name, f, n, u, findings, notes, unchecked)
	}
}

func TestOrderSeq(t *testing.T) {
	sets := testSets(t)
	// The first n places: A1, B1, C1, D1, A2 ...
	r := CheckWith(made("made-seq", 1, 4, 3, mods("CPU0_A1", "CPU0_B1")), sets)
	want(t, "two in order", r, nil, []string{Agrees}, nil)
	if r.Set != "made-seq" || !r.Ordered || r.Strength != Required || r.Notes[0].Modules != 2 {
		t.Errorf("result %+v", r)
	}
	r = CheckWith(made("made-seq", 1, 4, 3, mods("CPU0_A1", "CPU0_A2")), sets)
	want(t, "second slot of A before B1", r, []string{Order}, nil, nil)
	f := r.Findings[0]
	if !reflect.DeepEqual(f.Fill, []string{"CPU0_B1"}) || !reflect.DeepEqual(f.Leave, []string{"CPU0_A2"}) || f.Strength != Required ||
		f.Socket != 0 || !reflect.DeepEqual(f.Source, []Ref{{"doc-a", "10"}, {"doc-b", "20"}}) {
		t.Errorf("finding %+v", f)
	}
	if m := r.Marks(); !reflect.DeepEqual(m, map[string]string{"CPU0_B1": "+", "CPU0_A2": "-"}) {
		t.Errorf("marks %v", m)
	}
	p := r.Processors[0]
	if !reflect.DeepEqual(p.Installed, []string{"CPU0_A1", "CPU0_A2"}) || !reflect.DeepEqual(p.Recommended, []string{"CPU0_A1", "CPU0_B1"}) || len(p.Table) != 12 {
		t.Errorf("processor %+v", p)
	}
	// A board that lacks a place of the order (channel D has no slot 3):
	// the order skips it, and 12 modules are not listed.
	b := made("made-seq", 1, 4, 3, nil)
	var slots []Slot
	for _, s := range b.Slots {
		if s.Name != "CPU0_D3" {
			slots = append(slots, s)
		}
	}
	b.Slots = slots
	all := map[string]bool{}
	for _, s := range b.Slots {
		all[s.Name] = true
	}
	for i := range b.Slots {
		b.Slots[i].Populated, b.Slots[i].Module = true, rdimm
	}
	r = CheckWith(b, sets)
	want(t, "eleven places, eleven modules", r, nil, []string{Agrees}, nil)
	if n := len(r.Processors[0].Table); n != 11 {
		t.Errorf("the table of a board without D3 lists %d numbers", n)
	}
}

func TestOrderSets(t *testing.T) {
	sets := testSets(t)
	// made-sets lists 1, 2 and 4 modules; 3 is beside the table.
	r := CheckWith(made("made-sets", 1, 2, 2, mods("CPU0_A2", "CPU0_B2")), sets)
	want(t, "2 as listed", r, nil, []string{Agrees}, nil)
	r = CheckWith(made("made-sets", 1, 2, 2, mods("CPU0_A2", "CPU0_B2", "CPU0_A1")), sets)
	want(t, "3 not listed", r, nil, []string{NotCompared}, nil)
	if n := r.Notes[0]; !reflect.DeepEqual(n.Counts, []int{1, 2, 4}) || n.Modules != 3 || len(r.Marks()) != 0 {
		t.Errorf("not compared %+v marks %v", n, r.Marks())
	}
	r = CheckWith(made("made-sets", 1, 2, 2, mods("CPU0_A1", "CPU0_B1")), sets)
	want(t, "2 in the first slots", r, []string{Order, First, First}, nil, nil)
	if f := r.Findings[0]; f.Strength != Recommended || !reflect.DeepEqual(f.Fill, []string{"CPU0_A2", "CPU0_B2"}) {
		t.Errorf("finding %+v", f)
	}
	// The table of Advanced ECC pairs the two slots of channel A.
	r = CheckWith(made("made-sets", 1, 2, 2, mods("CPU0_A2", "CPU0_A1")), sets)
	want(t, "the other mode's table", r, nil, []string{OtherMode}, nil)
	if r.Notes[0].Mode != "advanced-ecc" || len(r.Marks()) != 0 {
		t.Errorf("note %+v", r.Notes[0])
	}
	// The second set the document allows for one module.
	r = CheckWith(made("made-sets", 1, 2, 2, mods("CPU0_B2")), sets)
	want(t, "second set", r, nil, []string{SecondSet}, nil)
	r = CheckWith(made("made-sets", 1, 2, 2, mods("CPU0_B1")), sets)
	want(t, "one module elsewhere", r, []string{Order, First}, nil, nil)
}

func TestMemoryMode(t *testing.T) {
	sets := testSets(t)
	pair := made("made-sets", 1, 2, 2, mods("CPU0_A2", "CPU0_A1"))
	pair.Mode = "advanced-ecc"
	want(t, "advanced-ecc as its table has it", CheckWith(pair, sets), nil, []string{Agrees}, nil)
	pair.Mode = "independent"
	r := CheckWith(pair, sets)
	want(t, "the pair under the independent mode", r, []string{Order}, nil, nil)
	spread := made("made-sets", 1, 2, 2, mods("CPU0_A2", "CPU0_B2"))
	spread.Mode = "advanced-ecc"
	want(t, "spread under advanced-ecc", CheckWith(spread, sets), []string{Order}, nil, nil)
	spread.Mode = "mirroring"
	r = CheckWith(spread, sets)
	want(t, "a mode without a table", r, nil, nil, []string{Order + ":" + FieldMode})
	if r.Unchecked[0].Mode != "mirroring" {
		t.Errorf("unchecked %+v", r.Unchecked)
	}
	// A rule set without a table of a mode reads no mode.
	seq := made("made-seq", 1, 4, 3, mods("CPU0_A1"))
	seq.Mode = "mirroring"
	want(t, "made-seq ignores the mode", CheckWith(seq, sets), nil, []string{Agrees}, nil)
}

func TestFirstSlot(t *testing.T) {
	sets := testSets(t)
	// made-seq fills position 0 first: A1 empty and A2 populated fires.
	r := CheckWith(made("made-seq", 1, 4, 3, mods("CPU0_A2")), sets)
	want(t, "position 0 empty", r, []string{Order, First}, nil, nil)
	if f := r.Findings[1]; f.Channel != "A" || f.First != "CPU0_A1" || !reflect.DeepEqual(f.Slots, []string{"CPU0_A2", "CPU0_A1"}) || f.Strength != Recommended {
		t.Errorf("first %+v", f)
	}
	want(t, "position 0 filled", CheckWith(made("made-seq", 1, 4, 3, mods("CPU0_A1", "CPU0_A2")), sets), []string{Order}, nil, nil)
	// made-sets fills the last position first.
	r = CheckWith(made("made-sets", 1, 2, 2, mods("CPU0_A1", "CPU0_A2", "CPU0_B1", "CPU0_B2")), sets)
	want(t, "last filled", r, nil, []string{Agrees}, nil)
	// On a board of three slots a channel, the table's 0.1 is A2 and the
	// last position A3.
	r = CheckWith(made("made-sets", 1, 2, 3, mods("CPU0_A2")), sets)
	want(t, "last position empty", r, []string{First}, nil, nil)
	if f := r.Findings[0]; f.First != "CPU0_A3" || f.Strength != Required {
		t.Errorf("first %+v", f)
	}
}

func TestProcessors(t *testing.T) {
	sets := testSets(t)
	for _, c := range []struct {
		name  string
		socks int
		names []string
		find  []string
	}{
		{"one", 1, []string{"CPU0_A1"}, nil},
		{"two equal", 2, []string{"CPU0_A1", "CPU1_E1"}, nil},
		{"two unequal", 2, []string{"CPU0_A1", "CPU0_B1", "CPU1_E1"}, []string{Balance}},
		{"four equal", 4, []string{"CPU0_A1", "CPU1_E1", "CPU2_I1", "CPU3_M1"}, nil},
		{"four unequal", 4, []string{"CPU0_A1", "CPU1_E1", "CPU2_I1", "CPU3_M1", "CPU3_N1"}, []string{Balance}},
		{"installed without a module", 2, []string{"CPU0_A1"}, []string{CPU, Balance}},
	} {
		r := CheckWith(made("made-seq", c.socks, 4, 3, mods(c.names...)), sets)
		f, _, _ := ids(r)
		if !reflect.DeepEqual(f, c.find) {
			t.Errorf("%s: findings %v, want %v", c.name, f, c.find)
		}
	}
	r := CheckWith(made("made-seq", 2, 4, 3, mods("CPU0_A1", "CPU0_B1", "CPU1_E1")), sets)
	if f := r.Findings[0]; !reflect.DeepEqual(f.Counts, []int{2, 1}) || f.Socket != -1 {
		t.Errorf("balance %+v", f)
	}
	// An absent processor holds no module and is no finding.
	b := made("made-seq", 2, 4, 3, mods("CPU0_A1"))
	b.Present[1] = 0
	want(t, "absent processor", CheckWith(b, sets), nil, []string{Agrees}, nil)
	// A processor record that does not say: not checked.
	b.Present[1] = -1
	want(t, "processor status unknown", CheckWith(b, sets), nil, []string{Agrees}, []string{CPU + ":" + FieldStatus, Balance + ":" + FieldStatus})
	b.Present = nil
	want(t, "no processor records", CheckWith(b, sets), nil, []string{Agrees}, []string{CPU + ":" + FieldStatus, Balance + ":" + FieldStatus})
	r = CheckWith(made("made-seq", 2, 4, 3, mods("CPU0_A1")), sets)
	if f := r.Findings[0]; f.Rule != CPU || f.Socket != 1 || f.Strength != Required {
		t.Errorf("cpu %+v", f)
	}
}

func TestModuleTypes(t *testing.T) {
	sets := testSets(t)
	types := []string{"RDIMM", "LRDIMM", "UDIMM"}
	for _, a := range types {
		for _, b := range types {
			m := mods("CPU0_A1", "CPU0_B1")
			m = with(m, "CPU0_A1", func(x *Module) { x.Type = a })
			m = with(m, "CPU0_B1", func(x *Module) { x.Type = b })
			r := CheckWith(made("made-seq", 1, 4, 3, m), sets)
			var w []string
			if a != b {
				w = []string{MixType}
			}
			f, _, _ := ids(r)
			if !reflect.DeepEqual(f, w) {
				t.Errorf("%s with %s: %v", a, b, f)
			}
			if a != b {
				got := r.Findings[0].Types
				exp := []string{a, b}
				sort.Slice(exp, func(i, j int) bool { return typeOrder(exp[i]) < typeOrder(exp[j]) })
				if !reflect.DeepEqual(got, exp) || r.Findings[0].Strength != Required {
					t.Errorf("%s with %s: types %v", a, b, got)
				}
			}
		}
	}
	m := with(mods("CPU0_A1", "CPU0_B1"), "CPU0_B1", func(x *Module) { x.Type = "" })
	want(t, "a type unknown", CheckWith(made("made-seq", 1, 4, 3, m), sets), nil, []string{Agrees}, []string{MixType + ":" + FieldType})
	for in, out := range map[string]string{"RDIMM": "RDIMM", "LRDIMM": "LRDIMM", "UDIMM": "UDIMM", "SO-DIMM": "UDIMM", "Mini-RDIMM": "RDIMM", "": "", "NVDIMM-P": ""} {
		if got := TypeClass(in); got != out {
			t.Errorf("TypeClass(%q) = %q, want %q", in, got, out)
		}
	}
}

func TestRanksAndSizes(t *testing.T) {
	sets := testSets(t)
	two := func(ra, rb int, sa, sb int64) map[string]Module {
		m := mods("CPU0_A1", "CPU0_A2", "CPU0_B1", "CPU0_C1", "CPU0_D1")
		m = with(m, "CPU0_A1", func(x *Module) { x.Ranks, x.Bytes = ra, sa })
		return with(m, "CPU0_A2", func(x *Module) { x.Ranks, x.Bytes = rb, sb })
	}
	g := int64(1 << 30)
	want(t, "more ranks first", CheckWith(made("made-seq", 1, 4, 3, two(2, 1, 16*g, 16*g)), sets), nil, []string{Agrees}, nil)
	r := CheckWith(made("made-seq", 1, 4, 3, two(1, 2, 16*g, 16*g)), sets)
	want(t, "fewer ranks first", r, []string{MixRank}, nil, nil)
	if f := r.Findings[0]; !reflect.DeepEqual(f.Slots, []string{"CPU0_A1", "CPU0_A2"}) || !reflect.DeepEqual(f.Ranks, []int{1, 2}) || f.First != "CPU0_A1" || f.Channel != "A" {
		t.Errorf("ranks %+v", f)
	}
	want(t, "ranks unknown", CheckWith(made("made-seq", 1, 4, 3, two(0, 2, 16*g, 16*g)), sets), nil, []string{Agrees}, []string{MixRank + ":" + FieldRanks})
	want(t, "larger first", CheckWith(made("made-seq", 1, 4, 3, two(2, 2, 32*g, 16*g)), sets), nil, []string{Agrees}, nil)
	r = CheckWith(made("made-seq", 1, 4, 3, two(2, 2, 16*g, 32*g)), sets)
	want(t, "smaller first", r, []string{MixSize}, nil, nil)
	if f := r.Findings[0]; !reflect.DeepEqual(f.Bytes, []int64{16 * g, 32 * g}) || f.Strength != Recommended {
		t.Errorf("sizes %+v", f)
	}
	want(t, "size unknown", CheckWith(made("made-seq", 1, 4, 3, two(2, 2, 0, 16*g)), sets), nil, []string{Agrees}, []string{MixSize + ":" + FieldSize})
	// made-sets fills the last position first: there the larger module
	// belongs in A2.
	m := mods("CPU0_A1", "CPU0_A2", "CPU0_B1", "CPU0_B2")
	m = with(m, "CPU0_A1", func(x *Module) { x.Bytes = 32 * g })
	r = CheckWith(made("made-sets", 1, 2, 2, m), sets)
	want(t, "larger in the second slot of a last-first board", r, []string{MixSize}, nil, nil)
	if f := r.Findings[0]; !reflect.DeepEqual(f.Slots, []string{"CPU0_A2", "CPU0_A1"}) || f.First != "CPU0_A2" || f.Strength != Required {
		t.Errorf("sizes last %+v", f)
	}
}

func TestSpeedsAndParts(t *testing.T) {
	sets := testSets(t)
	m := with(mods("CPU0_A1", "CPU0_B1"), "CPU0_B1", func(x *Module) { x.Speed = 2133 })
	r := CheckWith(made("made-seq", 1, 4, 3, m), sets)
	want(t, "two speeds", r, []string{MixSpeed}, nil, nil)
	if f := r.Findings[0]; !reflect.DeepEqual(f.Speeds, []int{2133, 2400}) || f.Strength != Recommended {
		t.Errorf("speeds %+v", f)
	}
	m = with(mods("CPU0_A1", "CPU0_B1"), "CPU0_B1", func(x *Module) { x.Speed = 0 })
	want(t, "speed unknown", CheckWith(made("made-seq", 1, 4, 3, m), sets), nil, []string{Agrees}, []string{MixSpeed + ":" + FieldSpeed})
	// Part numbers: in one channel.
	m = with(mods("CPU0_A1", "CPU0_A2", "CPU0_B1", "CPU0_C1", "CPU0_D1"), "CPU0_A2", func(x *Module) { x.Part = "EXM16H" })
	r = CheckWith(made("made-seq", 1, 4, 3, m), sets)
	want(t, "two parts in a channel", r, []string{MixPart}, nil, nil)
	if f := r.Findings[0]; f.Parts != 2 || f.Channel != "A" || f.Strength != Required {
		t.Errorf("parts %+v", f)
	}
	m = with(mods("CPU0_A1", "CPU0_B1"), "CPU0_B1", func(x *Module) { x.Part = "EXM16H" })
	want(t, "two parts in two channels", CheckWith(made("made-seq", 1, 4, 3, m), sets), nil, []string{Agrees}, nil)
	m = with(mods("CPU0_A1", "CPU0_A2", "CPU0_B1", "CPU0_C1", "CPU0_D1"), "CPU0_A2", func(x *Module) { x.Part = "" })
	want(t, "part unknown", CheckWith(made("made-seq", 1, 4, 3, m), sets), nil, []string{Agrees}, []string{MixPart + ":" + FieldPart})
}

func TestRulesOnly(t *testing.T) {
	sets := testSets(t)
	b := made("made-seq", 1, 2, 2, mods("CPU0_A2"))
	b.Family, b.Product = "made-rules", "Other"
	r := CheckWith(b, sets)
	want(t, "rules only, nothing fired", r, nil, []string{Holds}, nil)
	if r.Ordered || r.Notes[0].Modules != 1 || !reflect.DeepEqual(r.Notes[0].Source, []Ref{{"doc-c", "3"}}) {
		t.Errorf("rules only %+v", r)
	}
	b = made("made-seq", 1, 2, 2, with(mods("CPU0_A2", "CPU0_B1"), "CPU0_B1", func(x *Module) { x.Speed = 3200 }))
	b.Family, b.Product = "made-rules", "Other"
	want(t, "rules only, a speed", CheckWith(b, sets), []string{MixSpeed}, nil, nil)
}

func TestNoRule(t *testing.T) {
	sets := testSets(t)
	b := made("made-seq", 1, 4, 3, mods("CPU0_A2"))
	b.Full, b.NotPlaced = false, 3
	r := CheckWith(b, sets)
	if r.NoRule != NotFull || r.NotPlaced != 3 || r.Set != "" || len(r.Findings)+len(r.Notes) != 0 || len(r.Marks()) != 0 {
		t.Errorf("not full: %+v", r)
	}
	b = made("made-seq", 1, 4, 3, mods("CPU0_A2"))
	b.Family = "another"
	if r := CheckWith(b, sets); r.NoRule != NoSet || r.Set != "" || len(r.Findings) != 0 {
		t.Errorf("no set: %+v", r)
	}
	// The maker and the match of a row count too.
	b = made("made-seq", 1, 4, 3, mods("CPU0_A2"))
	b.Maker = "other"
	if r := CheckWith(b, sets); r.NoRule != NoSet {
		t.Errorf("another maker: %+v", r)
	}
	// A board without a module is checked: its installed processor holds
	// none.
	r = CheckWith(made("made-seq", 1, 4, 3, nil), sets)
	if f, n, _ := ids(r); r.Set != "made-seq" || !reflect.DeepEqual(f, []string{CPU}) || n != nil {
		t.Errorf("no module: %+v", r)
	}
}

func TestWhenConditions(t *testing.T) {
	s, err := ParseSets(`id	maker	match	when	first	order	strength	alt	rules	source
two	*	.	S=2;per=4	0	-	-	-	P-MIX-04=rec d:1	-
cpu	*	.	cpu=xeon-sp-1-2	0	-	-	-	P-MIX-04=rec d:2	-
`)
	if err != nil {
		t.Fatal(err)
	}
	b := made("made-seq", 2, 2, 2, nil)
	if r := CheckWith(b, s); r.Set != "two" {
		t.Errorf("S=2 per=4: %q", r.Set)
	}
	b = made("made-seq", 2, 2, 3, nil)
	b.Processor = "xeon-sp-1-2"
	if r := CheckWith(b, s); r.Set != "cpu" {
		t.Errorf("per=6 falls to cpu: %q", r.Set)
	}
	b.Processor = "epyc-sp3"
	if r := CheckWith(b, s); r.NoRule != NoSet {
		t.Errorf("nothing matches: %+v", r)
	}
}

func TestProcessorTables(t *testing.T) {
	s, err := ParseSets(`id	maker	match	when	first	order	strength	alt	rules	source
p	*	.	-	0	p1: sets: 2: 0.0 1.0	req	-	-	d:1
`)
	if err != nil {
		t.Fatal(err)
	}
	// The second processor has no table: nothing is compared there.
	r := CheckWith(made("made-seq", 2, 2, 2, mods("CPU0_A1", "CPU0_B1", "CPU1_C2")), s)
	want(t, "table of processor 1 alone", r, nil, []string{Agrees}, nil)
	if len(r.Processors) != 2 || r.Processors[1].Recommended != nil || r.Notes[0].Modules != 2 {
		t.Errorf("%+v", r)
	}
}

func TestParseErrors(t *testing.T) {
	head := "id\tmaker\tmatch\twhen\tfirst\torder\tstrength\talt\trules\tsource\n"
	for _, row := range []string{
		"x\t*\t.\t-\t0\tsets: 2: 0.0\treq\t-\t-\td:1",                       // a row of 2 with one place
		"x\t*\t.\t-\t0\tseq: 0.0 0.0\treq\t-\t-\td:1",                       // a place twice
		"x\t*\t.\t-\t0\tsets: 1: 0.0; 1: 1.0\treq\t-\t-\td:1",               // two rows of one count
		"x\t*\t.\t-\t0\tseq: 0.0\t-\t-\t-\td:1",                             // no strength
		"x\t*\t.\t-\t0\tseq: 0.0\treq\t-\t-\t-",                             // no source
		"x\t*\t.\t-\t-\t-\t-\t-\tP-FIRST-01=rec d:1",                        // first without the first slot
		"x\t*\t.\t-\t0\t-\t-\t-\tP-MIX-09=rec d:1",                          // no such rule
		"x\t*\t.\t-\t0\t-\t-\t-\tP-MIX-04=must d:1",                         // a strength the table does not know
		"x\t*\t.\t-\t0\t-\t-\t-\tP-MIX-04=rec",                              // a rule without a page
		"x\t*\t.\t-\t0\t-\t-\t-\t-\t-",                                      // nothing stated
		"x\t*\t(\t-\t0\t-\t-\t-\tP-MIX-04=rec d:1",                          // a broken pattern
		"x\t*\t.\tsize=2\t0\t-\t-\t-\tP-MIX-04=rec d:1",                     // an unknown condition
		"x\t*\t.\t-\t0\tseq: 0.0\treq\tmode=advanced-ecc: seq: 1.0\t-\td:1", // a mode beside an order without one
		"x\t*\t.\t-\tfirst\t-\t-\t-\tP-MIX-04=rec d:1",                      // first is 0 or last
		"x\t*\t.\t-\t0\t-\t-\tseq: 0.0\tP-MIX-04=rec d:1",                   // alt without an order
	} {
		if _, err := ParseSets(head + row + "\n"); err == nil {
			t.Errorf("%q: no error", row)
		}
	}
	if _, err := ParseSets(head + "x\t*\t.\n"); err == nil || !strings.Contains(err.Error(), "fields") {
		t.Errorf("short row: %v", err)
	}
	if _, err := ParseSets("id\tmaker\n"); err == nil {
		t.Error("wrong header: no error")
	}
}

// TestEmbeddedSets: the table of the package reads.
func TestEmbeddedSets(t *testing.T) {
	if _, err := ParseSets(setsTSV); err != nil {
		t.Fatal(err)
	}
	for _, s := range Sets() {
		if s.ID == "" {
			t.Error("a set without an id")
		}
	}
}

// TestCheckDoesNotPanic: malformed boards give a result, never a panic.
func TestCheckDoesNotPanic(t *testing.T) {
	sets := testSets(t)
	boards := []Board{
		{},
		{Full: true},
		{Full: true, Maker: "made", Product: "Made Seq", Family: "made-seq", Slots: []Slot{{Name: "x", Socket: -3, Channel: -1, Position: -9, Populated: true}}},
		{Full: true, Maker: "made", Product: "Made Seq", Family: "made-seq", Present: []int{1}, Slots: []Slot{{Name: "a", Socket: 7, Populated: true}, {Name: "a", Socket: 7, Populated: true}}},
	}
	for i, b := range boards {
		func() {
			defer func() {
				if v := recover(); v != nil {
					t.Errorf("board %d: panic %v", i, v)
				}
			}()
			CheckWith(b, sets)
		}()
	}
}
