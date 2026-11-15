package plugin

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/model"
)

// fuzzRequest is a request for two DIMMs, one of which has more records on
// file than the request carries.
func fuzzRequest() *Request {
	return &Request{Contract: Contract, PreviousLogs: 2, Dimms: []RequestDIMM{
		{DimmId: "d1", EarlierLogs: 7},
		{DimmId: "d2"},
	}}
}

// shown reports why a text of a response may not be put on a screen or
// into a report, or "".
func shown(s string, limit int) string {
	switch {
	case len(s) > limit:
		return fmt.Sprintf("%d bytes, more than %d", len(s), limit)
	case !utf8.ValidString(s):
		return "not UTF-8"
	}
	for _, r := range s {
		if unicode.IsControl(r) || r == utf8.RuneError || r == 0x2028 || r == 0x2029 || (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) {
			return fmt.Sprintf("the character %U", r)
		}
	}
	return ""
}

// FuzzResponse feeds arbitrary bytes to the reader of an analyzer's
// response. A response is refused or it is one the rest of the program can
// use as it is: every text short and free of control characters, every
// number within its bounds, the rows symmetric.
func FuzzResponse(f *testing.F) {
	head := `{"Contract":"` + Contract + `","Analyzer":{"Name":"a","Version":"1"`
	for _, s := range []string{
		head + `},"Dimms":[]}`,
		head + `,"Vendor":"Example Memory","PreviousLogs":5000},"Dimms":[{"DimmId":"d1","Baseline":{"Percentile":97.5},"Level2":{"Lines":[{"Label":"records seen","Value":"3"}],"Notes":["values of a test analyzer"]}},{"DimmId":"d2"}]}`,
		head + `},"Dimms":[{"DimmId":"d1","AdjacentRows":[{"Rank":0,"DeviceId":7,"BankGroup":0,"Bank":3,"Row":100,"Neighbors":[164]},{"Rank":0,"DeviceId":7,"BankGroup":0,"Bank":3,"Row":101,"Neighbors":[]},{"Row":200,"Neighbors":[201,264]},{"Row":201,"Neighbors":[200]}]}]}`,
		head + `,"Build":7},"Signature":"x","Dimms":[{"DimmId":"d1","Level_2":{},"Confidence":3}]}`,
		head + `},"Dimms":[{"DimmId":"d1","AdjacentRows":[{"Row":2147483647,"Neighbors":[0]},{"Row":0,"Neighbors":[1]}]}]}`,
		head + `},"Dimms":[{"DimmId":"d1","Baseline":{"Percentile":1e999}}]}`,
		head + `},"Dimms":[{"DimmId":"d1","Level2":{"Lines":[{"Label":"a\u001b[2J","Value":"‮"}]}}],"\u001b]0;title\u0007":1}`,
		"\xef\xbb\xbf" + head + `},"Dimms":null}` + "\n",
		head + `},"Dimms":[{"DimmId":"d1"},{"DimmId":"d1"}]}`,
		`{"Contract":"my-analyzer/2"}`, `[]`, `{}{}`, "", "Segmentation fault (core dumped)\n",
	} {
		f.Add([]byte(s))
	}
	req := fuzzRequest()
	asked := map[string]bool{}
	for _, d := range req.Dimms {
		asked[d.DimmId] = true
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		// The reason a response is refused for is shown to the reader too.
		refused := func(err error) {
			if why := shown(err.Error(), 1000); why != "" {
				t.Fatalf("the reason holds %s: %q", why, err)
			}
		}
		resp, notes, err := parse(data)
		if err != nil {
			if resp != nil || notes != nil {
				t.Fatalf("an unreadable response left something behind: %v", err)
			}
			refused(err)
			return
		}
		res, err := check(req, resp)
		if err != nil {
			refused(err)
			return
		}
		if res.Analyzer.Name == "" || res.Analyzer.Version == "" || res.Analyzer.PreviousLogs < 0 {
			t.Fatalf("analyzer %+v was accepted", res.Analyzer)
		}
		for _, s := range []string{res.Analyzer.Name, res.Analyzer.Version, res.Analyzer.Vendor} {
			if why := shown(s, MaxName); why != "" {
				t.Fatalf("a name of the analyzer was accepted with %s", why)
			}
		}
		// The remarks go to the terminal as they are.
		for _, n := range append(notes, res.Notes...) {
			if why := shown(n, 1000); why != "" {
				t.Fatalf("a remark about the response holds %s: %q", why, n)
			}
		}
		for id, d := range res.DIMMs {
			if !asked[id] || d.DimmId != id {
				t.Fatalf("analysis of the DIMM %q, which the request does not hold", id)
			}
			if l := d.Level2; l != nil {
				if len(l.Lines) > MaxLines || len(l.Notes) > MaxNotes {
					t.Fatalf("%d lines and %d notes were accepted", len(l.Lines), len(l.Notes))
				}
				for _, line := range l.Lines {
					if why := shown(line.Label, MaxLabel); why != "" || strings.TrimSpace(line.Label) == "" {
						t.Fatalf("label %q was accepted (%s)", line.Label, why)
					}
					if why := shown(line.Value, MaxValue); why != "" {
						t.Fatalf("value %q was accepted (%s)", line.Value, why)
					}
				}
				for _, n := range l.Notes {
					if why := shown(n, MaxNote); why != "" || strings.TrimSpace(n) == "" {
						t.Fatalf("note %q was accepted (%s)", n, why)
					}
				}
			}
			if b := d.Baseline; b != nil && !(b.Percentile >= 0 && b.Percentile <= 100) {
				t.Fatalf("percentile %v was accepted", b.Percentile)
			}
			if len(d.AdjacentRows) > MaxRows {
				t.Fatalf("%d rows were accepted", len(d.AdjacentRows))
			}
			// What the engine is told about the rows the analyzer named,
			// and about the rows around them, is symmetric and within the
			// bounds of a row number.
			adj := res.Adjacency()
			for i, r := range d.AdjacentRows {
				if i == 64 {
					break
				}
				site := engine.Site{DIMM: &model.DIMM{UID: id}}
				for _, p := range []struct {
					from *int
					to   *model.Opt[int]
				}{{r.Rank, &site.Rank}, {r.DeviceId, &site.DeviceId}, {r.BankGroup, &site.BankGroup}, {r.Bank, &site.Bank}} {
					if p.from != nil {
						*p.to = model.Some(*p.from)
					}
				}
				rows := append([]int{r.Row}, r.Neighbors...)
				for _, a := range rows {
					near := adj.NeighborRows(site, a)
					if len(near) > 2*MaxNeighbors+2 {
						t.Fatalf("row %d has %d neighbours", a, len(near))
					}
					for _, b := range near {
						if b < 0 || b == a {
							t.Fatalf("row %d has the neighbour %d", a, b)
						}
						if back := adj.NeighborRows(site, b); !slices.Contains(back, a) {
							t.Fatalf("row %d names row %d, and row %d names %v", a, b, b, back)
						}
					}
				}
			}
		}
		_ = res.PartBaseline()
	})
}
