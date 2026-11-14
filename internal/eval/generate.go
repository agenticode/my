package eval

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"

	"github.com/agenticode/my/internal/adapter/dcbrain"
)

// GenOptions describe a generated fleet.
type GenOptions struct {
	// Seed selects the fleet: the same seed gives the same rows.
	Seed uint64
	// Servers is the number of servers with errors; zero means 300.
	Servers int
}

// Generated is a made-up fleet in the format of the dcbrain DRAM error
// dataset. It exists to run and test the harness without the dataset. Its
// rows follow simple recipes (a cell that repeats, a row that grows, a
// bank with scattered errors and so on) and say nothing about real DIMMs.
type Generated struct {
	Rows    []dcbrain.Row
	Servers []dcbrain.Server
	Tickets []dcbrain.Ticket
	// Kind names the recipe each server was made with.
	Kind map[string]string
}

// The recipes and how often each is used, in parts of a hundred.
var recipes = []struct {
	name  string
	share int
}{
	{"stray", 40}, {"cell", 22}, {"row", 10}, {"column", 8}, {"bank", 8}, {"device", 6}, {"channel", 3}, {"sudden", 3},
}

// Generate makes a fleet.
func Generate(opts GenOptions) *Generated {
	n := opts.Servers
	if n <= 0 {
		n = 300
	}
	rng := rand.New(rand.NewPCG(opts.Seed, 0x6d79))
	g := &Generated{Kind: map[string]string{}}
	const span = dcbrain.SpanDays * 86400
	models := []string{"A1", "A2", "B1", "B2", "B3", "C1", "C2"}
	makers := []string{"M1", "M2", "M3", "M4"}
	sizes := []int{8, 12, 16, 24}

	add := func(sid string, mem, rank, bank, row, col int, typ dcbrain.ErrorType, at int) {
		if at < 0 || at >= span {
			return
		}
		g.Rows = append(g.Rows, dcbrain.Row{SID: sid, MemoryID: mem, Rank: rank, Bank: bank, Row: row, Col: col, Type: typ, At: dcbrain.Stamp(at)})
	}
	ticket := func(sid string, typ dcbrain.FailureType, at int) {
		if at >= 0 && at < span {
			g.Tickets = append(g.Tickets, dcbrain.Ticket{SID: sid, Type: typ, At: dcbrain.Stamp(at)})
		}
	}
	typ := func(scrubShare int) dcbrain.ErrorType {
		if rng.IntN(100) < scrubShare {
			return dcbrain.TypeScrub
		}
		return dcbrain.TypeRead
	}
	col := func() int { return rng.IntN(128) * 8 }
	row := func() int { return rng.IntN(1 << 17) }

	for i := 0; i < n; i++ {
		sid := fmt.Sprintf("srv%05d", i+1)
		g.Servers = append(g.Servers, dcbrain.Server{SID: sid, Manufacturer: makers[rng.IntN(len(makers))],
			DRAMModel: models[rng.IntN(len(models))], DIMMs: sizes[rng.IntN(len(sizes))]})
		pick, kind := rng.IntN(100), ""
		for _, r := range recipes {
			if pick < r.share {
				kind = r.name
				break
			}
			pick -= r.share
		}
		g.Kind[sid] = kind
		mem, rank, bank := rng.IntN(24), rng.IntN(2), rng.IntN(16)
		start := rng.IntN(span - 20*86400)
		switch kind {
		case "stray":
			// One to three single errors, far apart, at unrelated places.
			for k, m := 0, 1+rng.IntN(3); k < m; k++ {
				add(sid, rng.IntN(24), rng.IntN(2), rng.IntN(16), row(), col(), typ(10), rng.IntN(span))
			}
		case "cell":
			// One cell that fails again on many days.
			r, c := row(), col()
			for d, m := 0, 5+rng.IntN(40); d < m; d++ {
				add(sid, mem, rank, bank, r, c, typ(8), start+d*86400+rng.IntN(86400))
			}
		case "row":
			// A row that fails at more and more columns, faster over time.
			r := row()
			cols := make([]int, 12)
			for k := range cols {
				cols[k] = col()
			}
			length := 14 + rng.IntN(40)
			for d := 0; d < length; d++ {
				for k, m := 0, 1+d/4; k < m; k++ {
					add(sid, mem, rank, bank, r, cols[rng.IntN(min(len(cols), 2+d/2))], typ(45), start+d*86400+rng.IntN(86400))
				}
			}
			if rng.IntN(100) < 35 {
				ticket(sid, dcbrain.FailureUE, start+length*86400+rng.IntN(5*86400))
			}
		case "column":
			// A column that fails at many rows; the scrubber finds most.
			c := col()
			length := 10 + rng.IntN(30)
			for d := 0; d < length; d++ {
				for k, m := 0, 1+rng.IntN(4); k < m; k++ {
					add(sid, mem, rank, bank, row(), c, typ(70), start+d*86400+rng.IntN(86400))
				}
			}
			if rng.IntN(100) < 35 {
				ticket(sid, dcbrain.FailureUE, start+length*86400+rng.IntN(5*86400))
			}
		case "bank":
			// Errors scattered over one bank.
			length := 7 + rng.IntN(25)
			for d := 0; d < length; d++ {
				for k, m := 0, 2+rng.IntN(5); k < m; k++ {
					add(sid, mem, rank, bank, row(), col(), typ(45), start+d*86400+rng.IntN(86400))
				}
			}
			if rng.IntN(100) < 30 {
				ticket(sid, dcbrain.FailureUE, start+length*86400+rng.IntN(5*86400))
			}
		case "device":
			// Errors over many banks of one rank, with a burst at the end.
			length := 5 + rng.IntN(20)
			for d := 0; d < length; d++ {
				for k, m := 0, 4+rng.IntN(10); k < m; k++ {
					add(sid, mem, rank, rng.IntN(16), row(), col(), typ(55), start+d*86400+rng.IntN(86400))
				}
			}
			burst := start + length*86400
			for k, m := 0, 120+rng.IntN(200); k < m; k++ {
				add(sid, mem, rank, rng.IntN(16), row(), col(), typ(55), burst+rng.IntN(3000))
			}
			switch p := rng.IntN(100); {
			case p < 45:
				ticket(sid, dcbrain.FailureCE, burst+3600+rng.IntN(86400))
			case p < 55:
				ticket(sid, dcbrain.FailureUE, burst+3600+rng.IntN(3*86400))
			}
		case "channel":
			// Scattered errors on both DIMMs of one channel.
			pair := mem &^ 1
			length := 4 + rng.IntN(10)
			for d := 0; d < length; d++ {
				for k, m := 0, 3+rng.IntN(6); k < m; k++ {
					add(sid, pair+rng.IntN(2), rng.IntN(2), rng.IntN(16), row(), col(), typ(30), start+d*86400+rng.IntN(86400))
				}
			}
			if rng.IntN(100) < 25 {
				ticket(sid, dcbrain.FailureMisc, start+length*86400+rng.IntN(3*86400))
			}
		case "sudden":
			// A few errors within the hour before an uncorrectable error.
			r := row()
			for k, m := 0, 2+rng.IntN(20); k < m; k++ {
				add(sid, mem, rank, bank, r, col(), typ(20), start+rng.IntN(3000))
			}
			ticket(sid, dcbrain.FailureUE, start+3000+rng.IntN(600))
		}
	}
	// Tickets of servers that have no row in the error table.
	for i := 0; i < max(n/20, 2); i++ {
		sid := fmt.Sprintf("srv%05d", n+i+1)
		g.Servers = append(g.Servers, dcbrain.Server{SID: sid, Manufacturer: makers[rng.IntN(len(makers))],
			DRAMModel: models[rng.IntN(len(models))], DIMMs: sizes[rng.IntN(len(sizes))]})
		g.Kind[sid] = "no-errors"
		ticket(sid, dcbrain.FailureType(rng.IntN(4)), rng.IntN(span))
	}
	// Rows in time order, as a log would hold them.
	sort.SliceStable(g.Rows, func(i, j int) bool { return g.Rows[i].At < g.Rows[j].At })
	for i := range g.Rows {
		g.Rows[i].Line = int64(i + 2)
	}
	sort.SliceStable(g.Tickets, func(i, j int) bool { return g.Tickets[i].SID < g.Tickets[j].SID })
	return g
}

// WriteDir writes the fleet as the three tables of the dataset into dir,
// which is created when it does not exist.
func (g *Generated) WriteDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	write := func(name string, fn func(f *os.File) error) error {
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if err := fn(f); err != nil {
			f.Close()
			return err
		}
		return f.Close()
	}
	if err := write("mcelog.csv", func(f *os.File) error { return dcbrain.WriteErrors(f, g.Rows) }); err != nil {
		return err
	}
	if err := write("inventory.csv", func(f *os.File) error { return dcbrain.WriteInventory(f, g.Servers) }); err != nil {
		return err
	}
	return write("trouble_tickets.csv", func(f *os.File) error { return dcbrain.WriteTickets(f, g.Tickets) })
}
