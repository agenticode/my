package explorer

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// TestC100Dev compares the reader with the expected place of every slot;
// MY_C100_DEV=1 prints the differences by kind.
func TestC100Dev(t *testing.T) {
	if os.Getenv("MY_C100_DEV") == "" {
		t.Skip("MY_C100_DEV is not set")
	}
	lists := readPopulation(t)
	type grp struct {
		n     int
		lists []int
		ex    string
	}
	groups := map[string]*grp{}
	bad, total := 0, 0
	for _, list := range lists {
		d, f := ReadSlots(WriteSlotsOf(popHost(list), slotTexts(list)), nil, nil)
		if len(d) != len(list) {
			t.Fatalf("list %d: %d slots", list[0].list, len(d))
		}
		fam := ""
		for _, s := range list {
			if strings.HasPrefix(s.by, "family:") {
				fam = s.by
			}
		}
		for i, s := range list {
			total++
			if diff := slotDiff(d[i], f, s); diff != "" {
				bad++
				k := fmt.Sprintf("%s|%s|%s -> %s", fam, s.state, s.reason, strings.SplitN(diff, " ", 3)[0]+" "+strings.SplitN(diff, " ", 3)[1])
				g := groups[k]
				if g == nil {
					g = &grp{ex: fmt.Sprintf("list %d %q|%q: %s", s.list, s.locator, s.bank, diff)}
					groups[k] = g
				}
				g.n++
				if len(g.lists) < 6 && (len(g.lists) == 0 || g.lists[len(g.lists)-1] != s.list) {
					g.lists = append(g.lists, s.list)
				}
			}
		}
	}
	var ks []string
	for k := range groups {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool { return groups[ks[i]].n > groups[ks[j]].n })
	var b strings.Builder
	for _, k := range ks {
		fmt.Fprintf(&b, "%5d %s lists %v\n      %s\n", groups[k].n, k, groups[k].lists, groups[k].ex)
	}
	os.WriteFile(os.Getenv("MY_C100_DEV"), []byte(fmt.Sprintf("%d of %d slots differ\n%s", bad, total, b.String())), 0o644)
	t.Logf("%d of %d slots differ", bad, total)
}
