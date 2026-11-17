package spd

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/testutil"
)

// docSection checks that the text between the markers <!-- NAME --> and
// <!-- /NAME --> of docs/compatibility.md is want; UPDATE_GOLDEN=1
// writes it there.
func docSection(t *testing.T, name, want string) {
	t.Helper()
	path := testutil.RepoRoot(t) + "/docs/compatibility.md"
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	open, end := "<!-- "+name+" -->\n", "<!-- /"+name+" -->"
	i, j := strings.Index(text, open), strings.Index(text, end)
	if i < 0 || j < i {
		t.Fatalf("docs/compatibility.md has no section %s", name)
	}
	if got := text[i+len(open) : j]; got != want {
		if os.Getenv(testutil.UpdateGoldenEnv) != "" {
			if err := os.WriteFile(path, []byte(text[:i+len(open)]+want+text[j:]), 0o644); err != nil {
				t.Fatal(err)
			}
			return
		}
		t.Errorf("the section %s of docs/compatibility.md differs from the code (run with UPDATE_GOLDEN=1 after checking the change)", name)
	}
}

func layoutTable(fields []Field) string {
	var b strings.Builder
	b.WriteString("| Byte | Bits | Field | Coding |\n| --- | --- | --- | --- |\n")
	for _, f := range fields {
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", f.Bytes, f.Bits, f.Name, f.Coding)
	}
	return b.String()
}

// TestLayoutInTheDocs holds the byte tables of docs/compatibility.md to
// the layout tables of the code, and the codings of those tables to the
// maps the decoder reads.
func TestLayoutInTheDocs(t *testing.T) {
	docSection(t, "spd-ddr4", layoutTable(DDR4Layout))
	docSection(t, "spd-ddr5", layoutTable(DDR5Layout))
	coding := func(m map[int]string) string {
		var keys []int
		for k := range m {
			keys = append(keys, k)
		}
		sort.Ints(keys)
		var parts []string
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%d %s", k, m[k]))
		}
		return strings.Join(parts, ", ")
	}
	for _, c := range []struct {
		fields []Field
		mem    string
	}{{DDR4Layout, "DDR4"}, {DDR5Layout, "DDR5"}} {
		for _, f := range c.fields {
			if f.Bytes == "3" && f.Coding != coding(ModuleTypes(c.mem)) {
				t.Errorf("%s module types: the layout says %q, the code %q", c.mem, f.Coding, coding(ModuleTypes(c.mem)))
			}
		}
	}
	// Every byte of the layout tables is a byte the decoder reads: a
	// change of the byte changes the decode.
	for _, c := range []struct {
		fields []Field
		base   string
	}{{DDR4Layout, "ddr4-rdimm-64g-2rx4"}, {DDR5Layout, "ddr5-rdimm-64g-2rx4"}} {
		b := dump(t, c.base)
		fields := func(b []byte) string {
			d := Decode(b)
			d.CRCs = nil
			return fmt.Sprintf("%+v", d)
		}
		want := fields(b)
		for _, f := range c.fields {
			for _, off := range byteList(t, f.Bytes) {
				x := append([]byte(nil), b...)
				x[off] ^= 0xFF
				WriteCRCs(x)
				if fields(x) == want && !strings.Contains(f.Name, "CRC") {
					t.Errorf("%s byte %d (%s): a change does not change the decode", c.base, off, f.Name)
				}
			}
		}
	}
	// No other byte is read: a change of a byte outside the tables leaves
	// the fields as they were.
	for _, c := range []struct {
		fields []Field
		base   string
	}{{DDR4Layout, "ddr4-rdimm-64g-2rx4"}, {DDR5Layout, "ddr5-rdimm-64g-2rx4"}} {
		b := dump(t, c.base)
		listed := map[int]bool{}
		for _, f := range c.fields {
			for _, off := range byteList(t, f.Bytes) {
				listed[off] = true
			}
		}
		fields := func(b []byte) string {
			d := Decode(b)
			d.CRCs = nil
			return fmt.Sprintf("%+v", d)
		}
		want := fields(b)
		for off := range b {
			if listed[off] {
				continue
			}
			x := append([]byte(nil), b...)
			x[off] ^= 0xFF
			WriteCRCs(x)
			if fields(x) != want {
				t.Errorf("%s byte %d is read and not in the table of the docs", c.base, off)
			}
		}
	}
	// The count of maker names the docs give.
	rows, err := Makers()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile(testutil.RepoRoot(t) + "/docs/compatibility.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), fmt.Sprintf("names the codes found on server memory modules, makers of DRAM, of modules, of SPD hubs, of registers and data buffers and of PMICs: %d rows", len(rows))) {
		t.Errorf("docs/compatibility.md does not give the %d rows of makers.tsv", len(rows))
	}
}

var byteRange = regexp.MustCompile(`^(\d+)(?:-(\d+))?$`)

func byteList(t *testing.T, s string) []int {
	var out []int
	for _, p := range strings.Split(s, ", ") {
		m := byteRange.FindStringSubmatch(p)
		if m == nil {
			t.Fatalf("bytes %q", s)
		}
		var a, z int
		fmt.Sscan(m[1], &a)
		z = a
		if m[2] != "" {
			fmt.Sscan(m[2], &z)
		}
		for i := a; i <= z; i++ {
			out = append(out, i)
		}
	}
	return out
}
