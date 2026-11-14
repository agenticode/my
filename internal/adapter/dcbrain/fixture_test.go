package dcbrain

import (
	"bytes"
	"flag"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/agenticode/my/internal/testutil"
)

var update = flag.Bool("update", false, "rewrite the files under testdata/dcbrain")

// The fixture under testdata/dcbrain is generated: it has the format of the
// dataset and none of its rows. fixture builds it from fixed seeds, so the
// files can be rewritten with "go test ./internal/adapter/dcbrain -run
// TestFixtureIsCurrent -update" and must not change otherwise.

func day(d int, h, m, s int) Stamp {
	return StampOfDay(d) + Stamp(h*3600+m*60+s)
}

func fixture() (rows []Row, servers []Server, tickets []Ticket) {
	rng := rand.New(rand.NewPCG(20221, 132973))
	add := func(sid string, mem, rank, bank, row, col int, typ ErrorType, at Stamp) {
		rows = append(rows, Row{SID: sid, MemoryID: mem, Rank: rank, Bank: bank, Row: row, Col: col, Type: typ, At: at})
	}
	clock := func() (int, int, int) { return rng.IntN(24), rng.IntN(60), rng.IntN(60) }

	// s0001: one cell that fails again and again over three weeks.
	for i := 0; i < 24; i++ {
		h, m, s := clock()
		add("s0001", 3, 0, 5, 0x1c2a, 0x0f8, TypeRead, day(40+i, h, m, s))
	}

	// s0002: a row fault that grows, then the server fails with an
	// uncorrectable error. Days 55 to 60 of the dataset calendar include
	// 0001-02-29 and 0001-02-30.
	cols := []int{0x008, 0x040, 0x0c8, 0x130, 0x1f0, 0x268, 0x2d0, 0x358, 0x3e8}
	for d := 44; d <= 70; d++ {
		n := 1 + (d-44)/5
		for i := 0; i < n; i++ {
			h, m, s := clock()
			typ := TypeRead
			if rng.IntN(3) == 0 {
				typ = TypeScrub
			}
			add("s0002", 0, 1, 9, 0x1a2f, cols[rng.IntN(min(len(cols), 2+(d-44)/3))], typ, day(d, h, m, s))
		}
	}
	add("s0002", 0, 1, 9, 0x1a30, 0x0c8, TypeRead, day(69, 4, 10, 0))
	tickets = append(tickets, Ticket{SID: "s0002", Type: FailureUE, At: day(71, 3, 14, 7)})

	// s0003: a column fault the scrubber finds.
	for i := 0; i < 40; i++ {
		h, m, s := clock()
		add("s0003", 13, 0, 2, 0x0400+i*0x111, 0x2a0, TypeScrub, day(100+i/4, h, m, s))
	}

	// s0004: errors scattered over one bank within ten days.
	for i := 0; i < 36; i++ {
		h, m, s := clock()
		add("s0004", 22, 1, 14, rng.IntN(1<<17), rng.IntN(128)*8, TypeRead, day(118+rng.IntN(10), h, m, s))
	}

	// s0005: errors over many banks of one rank, a burst within an hour,
	// and a ticket for too many correctable errors. Day 182 is 0001-06-31.
	for i := 0; i < 60; i++ {
		h, m, s := clock()
		add("s0005", 6, 0, rng.IntN(16), rng.IntN(1<<17), rng.IntN(128)*8, TypeRead, day(170+rng.IntN(11), h, m, s))
	}
	for i := 0; i < 150; i++ {
		add("s0005", 6, 0, rng.IntN(16), rng.IntN(1<<17), rng.IntN(128)*8, TypeRead, day(182, 2, rng.IntN(50), rng.IntN(60)))
	}
	tickets = append(tickets, Ticket{SID: "s0005", Type: FailureCE, At: day(182, 5, 0, 0)})

	// s0006: both DIMMs of one channel (memoryid 4 and 5) show scattered
	// errors in the same days.
	for i := 0; i < 30; i++ {
		h, m, s := clock()
		add("s0006", 4+i%2, rng.IntN(2), rng.IntN(16), rng.IntN(1<<17), rng.IntN(128)*8, TypeRead, day(200+rng.IntN(6), h, m, s))
	}

	// s0007: single errors far apart on three DIMMs.
	for i, mem := range []int{1, 8, 17} {
		h, m, s := clock()
		add("s0007", mem, 0, 3+i, 0x2000+i*0x77, 0x100+i*8, TypeRead, day(20+i*70, h, m, s))
	}
	tickets = append(tickets, Ticket{SID: "s0007", Type: FailureUnspecified, At: day(222, 9, 30, 0)})

	// s0008: the same error logged five times in one second, and errors
	// found by writes. Day 122 is 0001-04-31.
	for i := 0; i < 5; i++ {
		add("s0008", 10, 0, 0, 0x0042, 0x010, TypeRead, day(122, 12, 0, 0))
	}
	for i := 0; i < 4; i++ {
		add("s0008", 10, 0, 0, 0x0042, 0x010, TypeWrite, day(121+i, 12, 0, 1))
	}

	// s0099: a failure ticket of a server that has no row in the error table.
	tickets = append(tickets, Ticket{SID: "s0099", Type: FailureMisc, At: day(90, 0, 0, 1)})

	sort.SliceStable(rows, func(i, j int) bool { return rows[i].At < rows[j].At })
	for i := range rows {
		rows[i].Line = int64(i + 2)
	}
	servers = []Server{
		{SID: "s0001", Manufacturer: "M1", DRAMModel: "A1", DIMMs: 12},
		{SID: "s0002", Manufacturer: "M2", DRAMModel: "B1", DIMMs: 24},
		{SID: "s0003", Manufacturer: "M2", DRAMModel: "B2", DIMMs: 24},
		{SID: "s0004", Manufacturer: "M3", DRAMModel: "A2", DIMMs: 24},
		{SID: "s0005", Manufacturer: "M1", DRAMModel: "C1", DIMMs: 16},
		{SID: "s0006", Manufacturer: "M4", DRAMModel: "C2", DIMMs: 12},
		{SID: "s0007", Manufacturer: "M2", DRAMModel: "B3", DIMMs: 24},
		{SID: "s0008", Manufacturer: "M3", DRAMModel: "A1", DIMMs: 8},
		{SID: "s0099", Manufacturer: "M1", DRAMModel: "A1", DIMMs: 12},
	}
	return rows, servers, tickets
}

func fixtureFiles(t testing.TB) map[string][]byte {
	t.Helper()
	rows, servers, tickets := fixture()
	var e, i, k bytes.Buffer
	if err := WriteErrors(&e, rows); err != nil {
		t.Fatal(err)
	}
	if err := WriteInventory(&i, servers); err != nil {
		t.Fatal(err)
	}
	if err := WriteTickets(&k, tickets); err != nil {
		t.Fatal(err)
	}
	return map[string][]byte{"mcelog.csv": e.Bytes(), "inventory.csv": i.Bytes(), "trouble_tickets.csv": k.Bytes()}
}

func TestFixtureIsCurrent(t *testing.T) {
	dir := testutil.Testdata(t, "dcbrain")
	for name, want := range fixtureFiles(t) {
		p := filepath.Join(dir, name)
		if *update {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, want, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("%v (run the test with -update to write the fixture)", err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs from what the generator writes; run the test with -update", p)
		}
	}
}
