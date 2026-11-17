package view

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/population"
	"github.com/agenticode/my/internal/testutil"
)

// collectionDump is one dump of the public collection as a ledger of it
// holds it: the host, the slots and the module in each populated slot.
type collectionDump struct {
	id     string
	vendor string
	bh     boardHost
	slots  []model.Slot
	h      *engine.HostResult
}

func unescapeField(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	return strings.NewReplacer(`\\`, `\`, `\t`, "\t", `\n`, "\n", `\r`, "\r").Replace(s)
}

// readCollectionDumps reads testdata/explorer/population: the slot lists,
// the list of every dump and the modules of every dump.
func readCollectionDumps(t *testing.T) []collectionDump {
	t.Helper()
	root := testutil.Testdata(t, "explorer", "population")
	lists := map[string][][]string{}
	f, err := os.Open(filepath.Join(root, "slots.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		p := strings.Split(sc.Text(), "\t")
		if len(p) != 23 || strings.HasPrefix(p[0], "#") || p[0] == "list" {
			continue
		}
		for i := range p {
			p[i] = unescapeField(p[i])
		}
		lists[p[0]] = append(lists[p[0]], p)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	type key struct {
		dump, locator, bank string
		nth                 int
	}
	modules := map[key][]string{}
	b, err := os.ReadFile(filepath.Join(root, "modules.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		p := strings.Split(line, "\t")
		if len(p) != 9 || strings.HasPrefix(line, "#") || p[0] == "dump" {
			continue
		}
		n, _ := strconv.Atoi(p[3])
		modules[key{p[0], nameKey(unescapeField(p[1])), nameKey(unescapeField(p[2])), n}] = p
	}
	b, err = os.ReadFile(filepath.Join(root, "dumps.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	var out []collectionDump
	for _, line := range strings.Split(string(b), "\n") {
		d := strings.Split(line, "\t")
		if len(d) != 3 || d[0] == "dump" || strings.HasPrefix(line, "#") {
			continue
		}
		cd := collectionDump{id: d[0], h: &engine.HostResult{Host: "h"}}
		nth := map[[2]string]int{}
		for n, p := range lists[d[1]] {
			fmt.Sscanf(p[2], "%d", &cd.bh.sockets)
			cd.vendor = p[3]
			cd.bh.vendor, cd.bh.product, cd.bh.board, cd.bh.processor, cd.bh.signature, cd.bh.present = p[3], p[4], p[5], p[6], p[7], p[8]
			loc, bank := nameKey(p[11]), nameKey(p[12])
			k := [2]string{loc, bank}
			nth[k]++
			name := fmt.Sprintf("%s#%d", p[11], n)
			cd.slots = append(cd.slots, slotNamed(name, p[11], p[12], 0x1100+n, p[9] == "1"))
			if p[9] != "1" {
				continue
			}
			dimm := &model.DIMM{SizeBytes: 16 << 30, MemoryType: p[10]}
			if m := modules[key{d[0], loc, bank, nth[k]}]; m != nil {
				mb, _ := strconv.ParseInt(m[4], 10, 64)
				dimm.SizeBytes, dimm.ModuleType, dimm.PartNumber = mb<<20, m[5], m[8]
				if r, _ := strconv.Atoi(m[6]); r > 0 {
					dimm.Ranks = model.Some(r)
				}
				if s, _ := strconv.Atoi(m[7]); s > 0 {
					dimm.SpeedMTs = model.Some(s)
				}
			}
			cd.h.DIMMs = append(cd.h.DIMMs, &engine.DIMMResult{UID: name, Slot: name, Present: true, Verdict: &model.Verdict{Grade: model.VerdictOK}, DIMM: dimm})
		}
		out = append(out, cd)
	}
	// A dump without a memory device has no slot list; it is in the
	// separate list of the collection.
	b, err = os.ReadFile(filepath.Join(root, "unplaceable.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if p := strings.Split(line, "\t"); len(p) == 3 && p[1] == "no-memory-device" {
			out = append(out, collectionDump{id: p[0], h: &engine.HostResult{Host: "h"}})
		}
	}
	return out
}

// nameKey is a locator or bank locator as the two files can be joined on:
// trimmed, and empty for a placeholder the ledger does not keep.
func nameKey(s string) string {
	if model.Placeholder(s) {
		return ""
	}
	return strings.TrimSpace(s)
}

// makerOfDump is the name a dump gives its system maker, in one form (as
// the golden of the boards of the collection writes it).
func makerOfDump(v string) string {
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

// TestPopulationCoverage runs the population check over every server dump
// of the public collection, with the modules the ledger reads of each, and
// holds the counts: per maker the dumps whose rule set has an order table,
// has general rules only, the dumps without a rule set and those not
// placed in full; and the dumps with a finding, per rule id. The counts
// describe the behaviour of the rules on public uploads and nothing of a
// fleet. Every rule set that holds for some dump has a dump that agrees
// with it and, where the collection has one, a dump with a finding; the
// golden population-cases.txt names them.
func TestPopulationCoverage(t *testing.T) {
	dumps := readCollectionDumps(t)
	if len(dumps) != 2044 {
		t.Fatalf("%d dumps", len(dumps))
	}
	type tally struct{ dumps, order, general, noSet, notFull, none int }
	byMaker := map[string]*tally{}
	all := &tally{}
	byRule := map[string]int{}
	byNote := map[string]int{}
	withFinding := 0
	type example struct{ agrees, differs, findings string }
	cases := map[string]*example{}
	for _, d := range dumps {
		b := boardOf(i18n.EN, d.h, d.slots, d.bh)
		m := makerOfDump(d.vendor)
		if byMaker[m] == nil {
			byMaker[m] = &tally{}
		}
		for _, tl := range []*tally{byMaker[m], all} {
			tl.dumps++
			p := b.Population
			switch {
			case p == nil:
				tl.none++
			case p.NoRule == population.NotFull:
				tl.notFull++
			case p.NoRule == population.NoSet:
				tl.noSet++
			case p.Ordered:
				tl.order++
			default:
				tl.general++
			}
		}
		p := b.Population
		if p == nil || p.Set == "" {
			continue
		}
		rules := map[string]bool{}
		var ids []string
		for _, f := range p.Findings {
			if !rules[f.Rule] {
				rules[f.Rule] = true
				ids = append(ids, f.Rule)
			}
		}
		for r := range rules {
			byRule[r]++
		}
		if len(rules) > 0 {
			withFinding++
		}
		kinds := map[string]bool{}
		for _, n := range p.Notes {
			kinds[n.Kind] = true
		}
		for k := range kinds {
			byNote[k]++
		}
		ex := cases[p.Set]
		if ex == nil {
			ex = &example{}
			cases[p.Set] = ex
		}
		if ex.agrees == "" && len(p.Findings) == 0 && (kinds[population.Agrees] || kinds[population.Holds]) {
			ex.agrees = d.id
		}
		if ex.differs == "" && len(p.Findings) > 0 {
			sort.Strings(ids)
			ex.differs, ex.findings = d.id, strings.Join(ids, " ")
		}
	}

	var w strings.Builder
	w.WriteString("# The server dumps of the public collection by system maker and how the population check reads their board: a\n")
	w.WriteString("# rule set with an order table, a rule set of general rules only, no rule set for the board, the board not placed\n")
	w.WriteString("# in full (no check), no slot to check. Counts of public uploads; they say nothing of a fleet.\n")
	w.WriteString("maker\tdumps\torder table\tgeneral rules only\tno rule set\tnot placed in full\tno slot\n")
	var makers []string
	for m := range byMaker {
		makers = append(makers, m)
	}
	sort.Slice(makers, func(i, j int) bool {
		if byMaker[makers[i]].dumps != byMaker[makers[j]].dumps {
			return byMaker[makers[i]].dumps > byMaker[makers[j]].dumps
		}
		return makers[i] < makers[j]
	})
	row := func(name string, tl *tally) {
		fmt.Fprintf(&w, "%s\t%d\t%d\t%d\t%d\t%d\t%d\n", name, tl.dumps, tl.order, tl.general, tl.noSet, tl.notFull, tl.none)
	}
	for _, m := range makers {
		row(m, byMaker[m])
	}
	row("all", all)
	w.WriteString("\n# Dumps with a finding of the rule (a dump counts once per rule).\nrule\tdumps\n")
	for _, r := range population.Rules {
		fmt.Fprintf(&w, "%s\t%d\n", r, byRule[r])
	}
	fmt.Fprintf(&w, "any rule\t%d\n", withFinding)
	w.WriteString("\n# Dumps with a line that is no finding.\nline\tdumps\n")
	for _, k := range []string{population.Agrees, population.Holds, population.NotCompared, population.OtherMode, population.SecondSet} {
		fmt.Fprintf(&w, "%s\t%d\n", k, byNote[k])
	}
	testutil.Golden(t, "population-coverage.txt", []byte(w.String()))

	var c strings.Builder
	c.WriteString("# Per rule set a dump of the public collection whose modules stand as the rule set has them, and a dump with a\n")
	c.WriteString("# finding with its rule ids; \"-\" where the collection has none.\nset\tagrees\tdiffers\tfindings\n")
	for _, s := range population.Sets() {
		ex := cases[s.ID]
		if ex == nil {
			fmt.Fprintf(&c, "%s\t-\t-\t-\n", s.ID)
			continue
		}
		dash := func(s string) string {
			if s == "" {
				return "-"
			}
			return s
		}
		fmt.Fprintf(&c, "%s\t%s\t%s\t%s\n", s.ID, dash(ex.agrees), dash(ex.differs), dash(ex.findings))
	}
	testutil.Golden(t, "population-cases.txt", []byte(c.String()))
	if all.order+all.general < 1500 {
		t.Errorf("only %d dumps reach a rule set", all.order+all.general)
	}
}

// The counts of the population check in the docs are those of the golden
// of TestPopulationCoverage.
func TestPopulationCoverageInTheDocs(t *testing.T) {
	b, err := os.ReadFile("testdata/population-coverage.txt")
	if err != nil {
		t.Fatal(err)
	}
	var all []string
	rules := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		p := strings.Split(line, "\t")
		if p[0] == "all" && len(p) == 7 {
			all = p
		}
		if len(p) == 2 && (strings.HasPrefix(p[0], "P-") || p[0] == "any rule") {
			rules[p[0]] = p[1]
		}
	}
	if all == nil {
		t.Fatal("no line all in the golden")
	}
	n := func(s string) string { v, _ := strconv.Atoi(s); return commasOf(v) }
	order, general := n(all[2]), n(all[3])
	reach, _ := strconv.Atoi(all[2])
	g, _ := strconv.Atoi(all[3])
	root := testutil.RepoRoot(t)
	for page, wants := range map[string][]string{
		"docs/rules.md": {order + " dumps reach a rule set with a table", general + " one of general rules only", n(all[4]) + " have no rule set",
			n(all[5]) + " are not placed in full", n(rules["any rule"]) + " dumps have a finding", "P-ORDER-01 " + n(rules["P-ORDER-01"])},
		"README.md":    {commasOf(reach+g) + " of the 2,044 dumps reach a rule set"},
		"README.ko.md": {"덤프 2,044개 중 " + commasOf(reach+g) + "개가 규칙 세트에 닿습니다"},
		"CHANGELOG.md": {order + " reach a rule set with a table", general + " one of general rules", n(rules["any rule"]) + " dumps have a finding"},
	} {
		text, err := os.ReadFile(filepath.Join(root, page))
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range wants {
			if !strings.Contains(string(text), w) {
				t.Errorf("%s lacks %q", page, w)
			}
		}
	}
}

// commasOf writes a count with thousands separated, as the docs write it.
func commasOf(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
