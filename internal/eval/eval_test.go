package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/dcbrain"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/testutil"
)

// fleet writes a generated fleet into a directory and opens it.
func fleet(t testing.TB, opts GenOptions) (*Generated, *dcbrain.Dataset) {
	t.Helper()
	g := Generate(opts)
	dir := t.TempDir()
	if err := g.WriteDir(dir); err != nil {
		t.Fatal(err)
	}
	ds, err := dcbrain.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return g, ds
}

func run(t testing.TB, ds *dcbrain.Dataset, opts Options) *Report {
	t.Helper()
	if opts.Scratch == "" {
		opts.Scratch = t.TempDir()
	}
	rep, err := Run(context.Background(), ds, opts)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

// expected holds what the tables say by themselves, counted without the
// engine and without the code under test.
type expected struct {
	servers, dimms, records int64
	byType                  [4]int64
	cut                     []CutoffStats
	leads                   []LeadStats
	ticketsByType, withRows [4]int64
}

func expect(g *Generated, cutoffs []int, horizon int, recentDays float64, leads []time.Duration) expected {
	var x expected
	first := map[string]dcbrain.Stamp{}
	times := map[string][]dcbrain.Stamp{}
	type dimm struct {
		sid string
		mem int
	}
	dimmFirst := map[dimm]dcbrain.Stamp{}
	distinct := map[dcbrain.Row]bool{}
	for _, r := range g.Rows {
		x.byType[r.Type]++
		if at, ok := first[r.SID]; !ok || r.At < at {
			first[r.SID] = r.At
		}
		times[r.SID] = append(times[r.SID], r.At)
		k := dimm{r.SID, r.MemoryID}
		if at, ok := dimmFirst[k]; !ok || r.At < at {
			dimmFirst[k] = r.At
		}
		r.Line = 0
		distinct[r] = true
	}
	x.servers, x.dimms, x.records = int64(len(first)), int64(len(dimmFirst)), int64(len(distinct))
	ticket := map[string]dcbrain.Ticket{}
	for _, t := range g.Tickets {
		x.ticketsByType[t.Type]++
		if old, ok := ticket[t.SID]; !ok || t.At < old.At {
			ticket[t.SID] = t
		}
	}
	for sid, t := range ticket {
		if _, ok := first[sid]; ok {
			x.withRows[t.Type]++
		}
	}
	h := dcbrain.StampOfDay(horizon)
	recent := dcbrain.Stamp(recentDays * 86400)
	for _, day := range cutoffs {
		c := dcbrain.StampOfDay(day)
		st := CutoffStats{Day: day}
		var all Counts
		dimmsIn := int64(0)
		for sid, at := range first {
			t, failed := ticket[sid]
			switch {
			case failed && t.At <= c:
				st.FailedBefore++
				continue
			case at > c:
				st.NotYet++
				if failed && t.At > c && t.At <= c+h {
					st.Unseen[outcomeOf(t.Type)]++
				}
				continue
			}
			st.Servers++
			out := NoFailure
			if failed && t.At > c && t.At <= c+h {
				out = outcomeOf(t.Type)
			}
			all[out]++
			for _, e := range times[sid] {
				if e <= c && e > c-recent {
					st.Active[out]++
					break
				}
			}
			for k, at := range dimmFirst {
				if k.sid == sid && at <= c {
					dimmsIn++
				}
			}
		}
		for sid, t := range ticket {
			if _, ok := first[sid]; !ok && t.At > c && t.At <= c+h {
				st.Unseen[outcomeOf(t.Type)]++
			}
		}
		// The test keeps the population split in ByGrade[0] and the DIMM
		// count in DIMMs[0]: the sums are what can be known here.
		st.ByGrade[0], st.DIMMs[0] = all, dimmsIn
		x.cut = append(x.cut, st)
	}
	for _, l := range leads {
		ls := LeadStats{Lead: l}
		for sid, t := range ticket {
			at, ok := first[sid]
			if !ok {
				continue
			}
			lc := &ls.ByType[outcomeOf(t.Type)]
			lc.Tickets++
			if at > t.At-dcbrain.Stamp(l/time.Second) {
				lc.NoErrors++
			}
		}
		x.leads = append(x.leads, ls)
	}
	return x
}

func TestRunCountsWhatTheTablesHold(t *testing.T) {
	g, ds := fleet(t, GenOptions{Seed: 11, Servers: 500})
	rep := run(t, ds, Options{Workers: 3})
	want := expect(g, DefaultCutoffs, DefaultHorizonDays, 7, DefaultLeads)

	d := &rep.Dataset
	if d.Rows != int64(len(g.Rows)) || d.ByType != want.byType || d.Servers != want.servers || d.DIMMs != want.dimms {
		t.Errorf("dataset: rows %d, by type %v, servers %d, DIMMs %d; want %d, %v, %d, %d",
			d.Rows, d.ByType, d.Servers, d.DIMMs, len(g.Rows), want.byType, want.servers, want.dimms)
	}
	if d.TicketsByType != want.ticketsByType || d.TicketsWithRows != want.withRows {
		t.Errorf("tickets: %v, with rows %v; want %v and %v", d.TicketsByType, d.TicketsWithRows, want.ticketsByType, want.withRows)
	}
	if d.InventoryServers != int64(len(g.Servers)) || d.NotInInventory != 0 || d.Errors.Skipped != 0 {
		t.Errorf("dataset = %+v", d)
	}
	c := &rep.Census
	if c.Servers != want.servers || c.DIMMs != want.dimms || c.Errors != int64(len(g.Rows)) || c.Records != want.records {
		t.Errorf("census: servers %d, DIMMs %d, errors %d, records %d; want %d, %d, %d, %d",
			c.Servers, c.DIMMs, c.Errors, c.Records, want.servers, want.dimms, len(g.Rows), want.records)
	}
	if c.GradeErrors != [3]int64{int64(len(g.Rows)), 0, 0} || c.GradeDIMMs != [3]int64{want.dimms, 0, 0} || c.NoDevice != c.Errors || c.NoBits != c.Errors {
		t.Errorf("census grades: %v, DIMMs %v, no device %d, no bits %d", c.GradeErrors, c.GradeDIMMs, c.NoDevice, c.NoBits)
	}
	var faultErrors, byLargest, byWidest, dimmGrades, serverGrades int64
	for _, m := range c.Modes {
		faultErrors += m.Errors
		byLargest += m.DIMMs
		byWidest += m.Widest
	}
	for i := range Grades {
		dimmGrades += c.DIMMGrades[i]
		serverGrades += c.ServerGrades[i]
	}
	if faultErrors != c.Errors || byLargest != c.DIMMs || byWidest != c.DIMMs || dimmGrades != c.DIMMs || serverGrades != c.Servers {
		t.Errorf("census sums: fault errors %d of %d, DIMMs by largest %d, by widest %d, by grade %d of %d, servers by grade %d of %d",
			faultErrors, c.Errors, byLargest, byWidest, dimmGrades, c.DIMMs, serverGrades, c.Servers)
	}

	if len(rep.ByCut) != len(want.cut) {
		t.Fatalf("%d cut-offs, want %d", len(rep.ByCut), len(want.cut))
	}
	for i := range want.cut {
		got, w := &rep.ByCut[i], &want.cut[i]
		if got.Day != w.Day || got.HeldOut != (w.Day >= DefaultSplitDay) {
			t.Errorf("cut-off %d: day %d, held out %v", i, got.Day, got.HeldOut)
		}
		if got.Servers != w.Servers || got.FailedBefore != w.FailedBefore || got.NotYet != w.NotYet {
			t.Errorf("day %d: population %d, failed before %d, not yet %d; want %d, %d, %d",
				w.Day, got.Servers, got.FailedBefore, got.NotYet, w.Servers, w.FailedBefore, w.NotYet)
		}
		if got.Servers+got.FailedBefore+got.NotYet != want.servers {
			t.Errorf("day %d: the three groups do not add up to the %d servers", w.Day, want.servers)
		}
		if got.All() != w.ByGrade[0] || got.Unseen != w.Unseen || got.Active != w.Active {
			t.Errorf("day %d: outcomes %v, unseen %v, active %v; want %v, %v, %v", w.Day, got.All(), got.Unseen, got.Active, w.ByGrade[0], w.Unseen, w.Active)
		}
		var dimms int64
		for _, n := range got.DIMMs {
			dimms += n
		}
		if dimms != w.DIMMs[0] {
			t.Errorf("day %d: %d DIMMs with a finding, want %d", w.Day, dimms, w.DIMMs[0])
		}
		// Every server of the population has one widest mode and at least
		// one rule.
		var byMode Counts
		for _, x := range got.ByMode {
			byMode.add(x)
		}
		if byMode != got.All() {
			t.Errorf("day %d: by mode %v, all %v", w.Day, byMode, got.All())
		}
		for g := range Grades {
			if n := got.ByGrade[g].Total(); n > 0 && Grades[g] == model.VerdictOK {
				t.Errorf("day %d: %d servers with errors have the finding ok", w.Day, n)
			}
		}
		if soft, row := got.ByRule[engine.RuleSoft], got.ByRule[engine.RuleRow]; soft.Total() < got.ByGrade[gradeIndex(model.VerdictObserve)].Total() ||
			row.Total() < got.ByGrade[gradeIndex(model.VerdictConsiderReplacement)].Total() {
			t.Errorf("day %d: fewer servers with a rule than with its grade: %v", w.Day, got.ByRule)
		}
	}
	for i := range want.leads {
		for out := range want.leads[i].ByType {
			got, w := rep.Leads[i].ByType[out], want.leads[i].ByType[out]
			var graded int64
			for _, n := range got.ByGrade {
				graded += n
			}
			if got.Tickets != w.Tickets || got.NoErrors != w.NoErrors || graded != got.Tickets-got.NoErrors {
				t.Errorf("lead %s, outcome %d: tickets %d, no errors %d, graded %d; want %d and %d",
					want.leads[i].Lead, out, got.Tickets, got.NoErrors, graded, w.Tickets, w.NoErrors)
			}
		}
	}
	if len(rep.Widened) != 0 || len(rep.Dropped) != 0 {
		t.Errorf("widened %v, dropped %v", rep.Widened, rep.Dropped)
	}

	// The periods add the cut-offs up.
	periods := rep.Periods()
	if len(periods) != 2 || periods[0].Name != "development" || periods[1].Name != "held-out" || len(periods[0].Days) != 3 {
		t.Fatalf("periods = %+v", periods)
	}
	var servers int64
	for _, p := range periods {
		servers += p.Sum.Servers
		sc := p.Sum.ScoreOf(model.VerdictConsiderReplacement, false)
		if sc.Flagged != p.Sum.ByGrade[2].Total() || sc.Hit != p.Sum.ByGrade[2].Failed() || sc.Failures != p.Sum.All().Failed() {
			t.Errorf("%s: score %+v", p.Name, sc)
		}
		ue := p.Sum.ScoreOf(model.VerdictConsiderReplacement, true)
		if ue.Hit != p.Sum.ByGrade[2][FailedUE] || ue.Failures != p.Sum.All()[FailedUE] {
			t.Errorf("%s: score against uncorrectable errors %+v", p.Name, ue)
		}
	}
	var total int64
	for i := range rep.ByCut {
		total += rep.ByCut[i].Servers
	}
	if servers != total {
		t.Errorf("periods hold %d servers, cut-offs %d", servers, total)
	}
}

func TestSameResultWhateverTheOrder(t *testing.T) {
	_, ds := fleet(t, GenOptions{Seed: 3, Servers: 200})
	var first []byte
	for _, workers := range []int{1, 4} {
		rep := run(t, ds, Options{Workers: workers})
		var buf bytes.Buffer
		if err := rep.Write(&buf, FormatJSON); err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = buf.Bytes()
			continue
		}
		if !bytes.Equal(first, buf.Bytes()) {
			t.Error("the report differs between one worker and four")
		}
	}
	var back Report
	if err := json.Unmarshal(first, &back); err != nil {
		t.Fatal(err)
	}
	if back.EngineVersion != engine.Version || len(back.ByCut) != len(DefaultCutoffs) || back.Dataset.Rows == 0 {
		t.Errorf("JSON reads back as %+v", back)
	}
}

// fixtureRun evaluates the generated fixture of the reader, whose servers
// are described in testdata/dcbrain/README.md.
func fixtureRun(t *testing.T, opts Options) *Report {
	t.Helper()
	ds, err := dcbrain.Open(testutil.Testdata(t, "dcbrain"))
	if err != nil {
		t.Fatal(err)
	}
	return run(t, ds, opts)
}

func TestFixtureFindings(t *testing.T) {
	// Day 71: s0002 has a growing row fault and fails that day with an
	// uncorrectable error. Day 131: s0004 had its scattered bank a few days
	// before. Day 207: s0006 shows errors on both DIMMs of a channel.
	rep := fixtureRun(t, Options{Cutoffs: []int{71, 131, 207}, HorizonDays: 30, SplitDay: 200})
	consider, hold, observe := gradeIndex(model.VerdictConsiderReplacement), gradeIndex(model.VerdictHoldReplacement), gradeIndex(model.VerdictObserve)

	c := &rep.ByCut[0]
	// s0001, s0002 and s0007 have errors by day 71.
	if c.Servers != 3 || c.NotYet != 5 || c.FailedBefore != 0 || c.HeldOut {
		t.Errorf("day 71: %+v", c)
	}
	if c.ByGrade[consider] != (Counts{FailedUE: 1}) || c.ByGrade[observe] != (Counts{NoFailure: 2}) {
		t.Errorf("day 71 by grade: %v", c.ByGrade)
	}
	if c.ByRule[engine.RuleRow] != (Counts{FailedUE: 1}) || c.ByMode[model.ModeRow] != (Counts{FailedUE: 1}) {
		t.Errorf("day 71: by rule %v, by mode %v", c.ByRule, c.ByMode)
	}
	if c.DIMMs[consider] != 1 || c.DIMMs[observe] != 2 {
		t.Errorf("day 71 DIMMs: %v", c.DIMMs)
	}
	// The ticket of s0099, which has no error row, falls into this window.
	if c.Unseen != (Counts{FailedMisc: 1}) {
		t.Errorf("day 71 unseen: %v", c.Unseen)
	}

	c = &rep.ByCut[1]
	// s0002 failed on day 71 and is out; s0003, s0004 and s0008 are in.
	if c.Servers != 5 || c.FailedBefore != 1 || c.NotYet != 2 {
		t.Errorf("day 131: %+v", c)
	}
	if got := c.ByMode[model.ModeBank].Total(); got != 1 {
		t.Errorf("day 131: %d servers with a bank fault, want 1 (%v)", got, c.ByMode)
	}

	c = &rep.ByCut[2]
	// s0005 failed on day 182. s0007 gets a ticket of type 0 on day 222.
	if !c.HeldOut || c.FailedBefore != 2 || c.Servers != 6 || c.NotYet != 0 {
		t.Errorf("day 207: %+v", c)
	}
	if c.ByGrade[hold].Total() != 1 || c.DIMMs[hold] != 2 || c.ByRule[engine.RuleChannel].Total() != 1 || c.ByMode[model.ModeSystemIssue].Total() != 1 {
		t.Errorf("day 207: by grade %v, DIMMs %v, by rule %v", c.ByGrade, c.DIMMs, c.ByRule)
	}
	if c.All()[FailedUnspecified] != 1 {
		t.Errorf("day 207: outcomes %v, want one ticket of type 0", c.All())
	}

	// Before the failures: s0002 was flagged a day ahead; s0005 had its
	// burst three hours before the ticket.
	for i, l := range rep.LeadTimes {
		ue := rep.Leads[i].ByType[FailedUE]
		ce := rep.Leads[i].ByType[FailedCE]
		if ue.Tickets != 1 || ce.Tickets != 1 {
			t.Fatalf("lead %s: tickets %d and %d", l, ue.Tickets, ce.Tickets)
		}
		switch {
		case l <= 24*time.Hour:
			if ue.ByGrade[consider] != 1 || ce.ByGrade[consider] != 1 {
				t.Errorf("lead %s: uncorrectable %v, correctable %v; want both flagged", l, ue.ByGrade, ce.ByGrade)
			}
		case l == 30*24*time.Hour:
			if ue.NoErrors != 1 || ce.NoErrors != 1 {
				t.Errorf("lead %s: %+v and %+v; want no errors yet", l, ue, ce)
			}
		}
	}

	// The census sees every server and every row.
	if rep.Census.Servers != 8 || rep.Census.Errors != rep.Dataset.Rows || rep.Dataset.Rows != 440 {
		t.Errorf("census: %d servers, %d errors of %d rows", rep.Census.Servers, rep.Census.Errors, rep.Dataset.Rows)
	}
	// s0008 logged one error five times in one second: four records less.
	if rep.Census.Records != rep.Dataset.Rows-4 {
		t.Errorf("records = %d, want %d", rep.Census.Records, rep.Dataset.Rows-4)
	}
	if rep.Dataset.TicketsByType != [4]int64{1, 1, 1, 1} || rep.Dataset.TicketsWithRows != [4]int64{1, 1, 1, 0} {
		t.Errorf("tickets: %v, with rows %v", rep.Dataset.TicketsByType, rep.Dataset.TicketsWithRows)
	}
	if rep.Dataset.ByType[dcbrain.TypeWrite] != 4 || rep.Dataset.MonthLastDay != [8]int{21, 30, 30, 31, 5, 31, 23, 0} {
		t.Errorf("dataset: by type %v, month ends %v", rep.Dataset.ByType, rep.Dataset.MonthLastDay)
	}

	// The layout counts: s0006 has errors on memoryid 4 and 5, one channel;
	// s0007 on 1, 8 and 17, which are one pair on one socket and two pairs
	// across sockets.
	l := rep.Dataset.Layout
	if l.All != (Pairs{Servers: 2, Pairs: 4, SameChannel: 1, SameSocket: 1}) {
		t.Errorf("layout = %+v", l)
	}
	// Of the two, s0007 is listed with 24 DIMMs and s0006 with 12.
	if l.Full != (Pairs{Servers: 1, Pairs: 3, SameChannel: 0, SameSocket: 1}) {
		t.Errorf("layout of servers with 24 DIMMs = %+v", l.Full)
	}
	// By size of the server: s0001 (12 DIMMs, memoryid 3) and s0006 (12
	// DIMMs, memoryid 4 and 5) give one DIMM at position 0 and two at 1.
	if l.ByDIMMs[12] != [2]int64{1, 2} || l.ByDIMMs[8] != [2]int64{1, 0} {
		t.Errorf("layout by size = %v", l.ByDIMMs)
	}

	// One server only.
	rep = fixtureRun(t, Options{Cutoffs: []int{71}, Servers: []string{"s0002"}})
	if rep.Census.Servers != 1 || rep.ByCut[0].Servers != 1 || rep.ByCut[0].Unseen.Total() != 0 {
		t.Errorf("one server: census %d, population %d, unseen %v", rep.Census.Servers, rep.ByCut[0].Servers, rep.ByCut[0].Unseen)
	}
}

// TestSameAsLedger checks that the harness gives the engine what an import
// of the server into a ledger gives it: the findings are the same.
func TestSameAsLedger(t *testing.T) {
	ctx := context.Background()
	ds, err := dcbrain.Open(testutil.Testdata(t, "dcbrain"))
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{}
	if err := opts.fill(); err != nil {
		t.Fatal(err)
	}
	r := &runner{opts: opts, cfg: engine.DefaultConfig(), base: opts.Base}
	sp, err := openSpool(ctx, ds, "", t.TempDir(), func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	defer sp.close()
	bySID := map[string][]dcbrain.Rec{}
	for b := 0; b < spoolBuckets; b++ {
		servers, err := sp.readBucket(b, DefaultMaxRecords)
		if err != nil {
			t.Fatal(err)
		}
		for _, sr := range servers {
			bySID[sp.info.Servers[sr.index]] = sr.recs
		}
	}
	if len(bySID) != 8 {
		t.Fatalf("%d servers in the spool, want 8", len(bySID))
	}

	type finding struct {
		Slot  string
		Grade model.VerdictGrade
		Rules string
		Modes string
		CE    int64
	}
	summary := func(h *engine.HostResult) (model.VerdictGrade, []finding) {
		var out []finding
		for _, d := range h.DIMMs {
			f := finding{Slot: d.Slot, Grade: d.Verdict.Grade, Rules: strings.Join(d.Verdict.Rules, ","), CE: d.Features.CE}
			var modes []string
			for _, x := range d.Faults {
				modes = append(modes, string(x.Mode))
			}
			f.Modes = strings.Join(modes, ",")
			out = append(out, f)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
		return h.Grade, out
	}
	for sid, recs := range bySID {
		s, err := store.Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), store.Create)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ingest.Run(ctx, s, []adapter.Adapter{dcbrain.NewServer(ds, sid, dcbrain.Options{})}, ingest.Options{}); err != nil {
			t.Fatal(err)
		}
		in := r.input(sid, recs)
		// Both ways of starting the history of a server: with its first
		// error, and with the first day of the dataset.
		for _, fromFirst := range []bool{true, false} {
			r.opts.FromFirstError = fromFirst
			for _, day := range []int{45, 71, 105, 131, 183, 207, 244} {
				at := dcbrain.StampOfDay(day)
				eo := engine.Options{AsOf: at.Time(opts.Base), Now: at.Time(opts.Base)}
				if !fromFirst {
					eo.ObservedFrom = opts.Base
				}
				res, err := engine.Run(ctx, s, eo)
				if err != nil {
					t.Fatal(err)
				}
				h, err := r.analyze(in, sid, at)
				if err != nil {
					t.Fatal(err)
				}
				wantGrade, want := summary(res.Host(sid))
				gotGrade, got := summary(h)
				if gotGrade != wantGrade || len(got) != len(want) {
					t.Errorf("%s day %d: harness %s %+v, ledger %s %+v", sid, day, gotGrade, got, wantGrade, want)
					continue
				}
				for i := range want {
					if got[i] != want[i] {
						t.Errorf("%s day %d: harness %+v, ledger %+v", sid, day, got[i], want[i])
					}
				}
			}
		}
		s.Close()
	}
}

// TestWatchedFromTheStart: a server that was quiet for months and then
// logs a burst in one row has a new fault when the dataset counts as its
// history, and an unknown trend when its history starts with the burst.
func TestWatchedFromTheStart(t *testing.T) {
	var rows []dcbrain.Row
	at := dcbrain.StampOfDay(100)
	for i := 0; i < 30; i++ {
		rows = append(rows, dcbrain.Row{SID: "burst", MemoryID: 2, Bank: 1, Row: 77, Col: 8 * (i % 12), Type: dcbrain.TypeRead, At: at + dcbrain.Stamp(i*600)})
	}
	g := &Generated{Rows: rows, Tickets: []dcbrain.Ticket{{SID: "burst", Type: dcbrain.FailureUE, At: at + 30*3600}}}
	dir := t.TempDir()
	if err := g.WriteDir(dir); err != nil {
		t.Fatal(err)
	}
	ds, err := dcbrain.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	consider, observe := gradeIndex(model.VerdictConsiderReplacement), gradeIndex(model.VerdictObserve)
	rep := run(t, ds, Options{Cutoffs: []int{101}})
	if c := rep.ByCut[0]; c.ByGrade[consider] != (Counts{FailedUE: 1}) || c.ByCode["row.new"] != (Counts{FailedUE: 1}) || rep.FromFirstError {
		t.Errorf("watched from the start: by grade %v, by statement %v", c.ByGrade, c.ByCode)
	}
	rep = run(t, ds, Options{Cutoffs: []int{101}, FromFirstError: true})
	if c := rep.ByCut[0]; c.ByGrade[observe] != (Counts{FailedUE: 1}) || !rep.FromFirstError {
		t.Errorf("history from the first error: by grade %v, by statement %v", c.ByGrade, c.ByCode)
	}
	var a, b bytes.Buffer
	rep.Write(&a, FormatText)
	run(t, ds, Options{Cutoffs: []int{101}}).Write(&b, FormatText)
	if !strings.Contains(a.String(), "the history of a server starts with its first error") ||
		!strings.Contains(b.String(), "every server counts as watched from the first day of the dataset") {
		t.Error("the report does not say where the history of a server starts")
	}
}

func TestSpoolIsKeptAndChecked(t *testing.T) {
	g, ds := fleet(t, GenOptions{Seed: 5, Servers: 60})
	keep := filepath.Join(t.TempDir(), "spool")
	scratch := t.TempDir()
	var lines []string
	opts := Options{Spool: keep, Scratch: scratch, Progress: func(f string, a ...any) { lines = append(lines, f) }}
	first := run(t, ds, opts)
	if _, err := os.Stat(filepath.Join(keep, spoolMeta)); err != nil {
		t.Fatalf("the spool was not kept: %v", err)
	}
	lines = nil
	second := run(t, ds, opts)
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "using the spool") {
		t.Errorf("the second run did not use the spool: %q", lines)
	}
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if !bytes.Equal(a, b) {
		t.Error("the report differs when the spool is used again")
	}

	// The error table changes: the spool is written again.
	g.Rows = append(g.Rows, dcbrain.Row{SID: "late", MemoryID: 1, Row: 5, Col: 8, Type: dcbrain.TypeRead, At: dcbrain.StampOfDay(200)})
	if err := g.WriteDir(ds.Path); err != nil {
		t.Fatal(err)
	}
	lines = nil
	third := run(t, ds, opts)
	if third.Dataset.Rows != first.Dataset.Rows+1 || third.Dataset.Servers != first.Dataset.Servers+1 {
		t.Errorf("after a change of the table: %d rows of %d servers, want %d of %d",
			third.Dataset.Rows, third.Dataset.Servers, first.Dataset.Rows+1, first.Dataset.Servers+1)
	}
	if len(lines) == 0 || !strings.Contains(lines[0], "another file or version") {
		t.Errorf("progress after a change of the table: %q", lines)
	}

	// Without a place to keep it, the spool is gone after the run.
	run(t, ds, Options{Scratch: scratch})
	left, err := os.ReadDir(scratch)
	if err != nil || len(left) != 0 {
		t.Errorf("scratch directory after the runs: %v, %v", left, err)
	}
}

func TestServersWithManyRecords(t *testing.T) {
	var rows []dcbrain.Row
	at := dcbrain.StampOfDay(100)
	// "minute": 300 errors of one cell at 300 different seconds of five
	// minutes. Merged by the minute they are 5 records.
	for i := 0; i < 300; i++ {
		rows = append(rows, dcbrain.Row{SID: "minute", MemoryID: 2, Bank: 1, Row: 7, Col: 8, Type: dcbrain.TypeRead, At: at + dcbrain.Stamp(i)})
	}
	// "wide": 400 different cells. No bucket brings them under the limit.
	for i := 0; i < 400; i++ {
		rows = append(rows, dcbrain.Row{SID: "wide", MemoryID: 3, Bank: i % 16, Row: 100 + i, Col: 8 * (i % 100), Type: dcbrain.TypeRead, At: at + dcbrain.Stamp(i*50)})
	}
	// "small": below the limit.
	for i := 0; i < 20; i++ {
		rows = append(rows, dcbrain.Row{SID: "small", MemoryID: 4, Bank: 2, Row: 9, Col: 16, Type: dcbrain.TypeScrub, At: at + dcbrain.Stamp(i*86400)})
	}
	g := &Generated{Rows: rows, Tickets: []dcbrain.Ticket{{SID: "wide", Type: dcbrain.FailureCE, At: at + 30000}}}
	dir := t.TempDir()
	if err := g.WriteDir(dir); err != nil {
		t.Fatal(err)
	}
	ds, err := dcbrain.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	rep := run(t, ds, Options{MaxRecords: 50, Cutoffs: []int{90, 150}})
	if len(rep.Widened) != 1 || rep.Widened[0] != (ServerNote{SID: "minute", Rows: 300, DIMMs: 1, Records: 5, BucketSeconds: 60}) {
		t.Errorf("widened = %+v", rep.Widened)
	}
	if len(rep.Dropped) != 1 || rep.Dropped[0].SID != "wide" || rep.Dropped[0].Rows != 400 || rep.Dropped[0].Records != 0 ||
		rep.Dropped[0].Ticket != "failure_type 2 at 0001-04-09 08:20:00" {
		t.Errorf("dropped = %+v", rep.Dropped)
	}
	c := &rep.Census
	if c.Servers != 2 || c.Errors != 320 || c.Records != 25 || rep.Dataset.Rows != 720 || rep.Dataset.Servers != 3 || rep.Dataset.DIMMs != 3 || c.DIMMs != 2 {
		t.Errorf("census: %d servers, %d errors, %d records; dataset: %d rows, %d servers, %d DIMMs",
			c.Servers, c.Errors, c.Records, rep.Dataset.Rows, rep.Dataset.Servers, rep.Dataset.DIMMs)
	}
	if rep.ByCut[1].Servers != 2 {
		t.Errorf("population at day 150 = %d, want 2", rep.ByCut[1].Servers)
	}
	var buf bytes.Buffer
	if err := rep.Write(&buf, FormatText); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"server minute: 300 rows on 1 DIMM merged in buckets of 60 seconds into 5 records",
		"server wide: left out, 400 rows on 1 DIMM are too many records even when merged by the day; failure_type 2 at 0001-04-09 08:20:00",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("the text report lacks %q", want)
		}
	}
}

func TestAggregator(t *testing.T) {
	a := &aggregator{limit: 3, m: map[aggKey]dcbrain.Rec{}}
	rec := func(at uint32, row uint32, line uint32) dcbrain.Rec {
		return dcbrain.Rec{At: at, Row: row, Count: 1, Line: line, Type: dcbrain.TypeRead}
	}
	// Four seconds of one cell: over the limit by the second, one record
	// by the minute.
	for i := uint32(0); i < 4; i++ {
		a.add(rec(120+i, 1, 10+i))
	}
	if a.level != 1 || len(a.m) != 1 {
		t.Fatalf("level %d with %d records, want level 1 with 1", a.level, len(a.m))
	}
	a.add(rec(100, 1, 5)) // an earlier row of the minute before
	a.add(rec(119, 1, 4))
	out := a.recs()
	if len(out) != 2 || out[0].At != 100 || out[0].Count != 2 || out[0].Line != 5 || out[1].At != 120 || out[1].Count != 4 || out[1].Line != 10 {
		t.Errorf("records = %+v", out)
	}
	// Cells that differ cannot be merged: the server is given up.
	for i := uint32(0); i < 10; i++ {
		a.add(rec(200, 100+i, 50+i))
	}
	if !a.gone {
		t.Error("the aggregator kept a server with more cells than the limit")
	}
	a.add(rec(1, 1, 1)) // must not panic
}

func TestDegrade(t *testing.T) {
	_, ds := fleet(t, GenOptions{Seed: 9, Servers: 150})
	full := run(t, ds, Options{})
	b := run(t, ds, Options{Degrade: model.GradeB})
	c := run(t, ds, Options{Degrade: model.GradeC})

	if b.Census.GradeErrors != [3]int64{0, full.Census.Errors, 0} || c.Census.GradeErrors != [3]int64{0, 0, full.Census.Errors} {
		t.Errorf("errors by grade: B run %v, C run %v, all %d", b.Census.GradeErrors, c.Census.GradeErrors, full.Census.Errors)
	}
	for _, rep := range []*Report{b, c} {
		// Without rows and columns no fault has a mode. The channel rule
		// needs none: errors on both DIMMs of a channel still make a
		// system issue.
		for _, m := range rep.Census.Modes {
			if m.Mode != model.ModeUndetermined && m.Mode != model.ModeSystemIssue && m.Faults != 0 {
				t.Errorf("degraded to %s: %d faults of mode %s", rep.Degrade, m.Faults, m.Mode)
			}
		}
		if rep.Census.Rules[engine.RuleRow] != 0 || rep.Census.Rules[engine.RuleChannel] == 0 {
			t.Errorf("degraded to %s: rules %v", rep.Degrade, rep.Census.Rules)
		}
		// The population does not depend on what the engine is shown.
		for i := range full.ByCut {
			if rep.ByCut[i].Servers != full.ByCut[i].Servers || rep.ByCut[i].All() != full.ByCut[i].All() {
				t.Errorf("degraded to %s, day %d: population %d %v, want %d %v", rep.Degrade, full.ByCut[i].Day,
					rep.ByCut[i].Servers, rep.ByCut[i].All(), full.ByCut[i].Servers, full.ByCut[i].All())
			}
		}
	}
	if full.Census.Rules[engine.RuleRow] == 0 {
		t.Error("the full run has no DIMM with R-ROW-03; the comparison shows nothing")
	}
	var buf bytes.Buffer
	c.Write(&buf, FormatText)
	if !strings.Contains(buf.String(), "records degraded to observation grade C") {
		t.Error("the report does not say that the records were degraded")
	}
}

func TestOptions(t *testing.T) {
	_, ds := fleet(t, GenOptions{Seed: 1, Servers: 20})
	ctx := context.Background()
	bad := []struct {
		opts Options
		want string
	}{
		{Options{Cutoffs: []int{60, 70}}, "closer than the outcome window"},
		{Options{Cutoffs: []int{0}}, "outside 1 to 243"},
		{Options{Cutoffs: []int{244}}, "outside 1 to 243"},
		{Options{Cutoffs: []int{60, 60}}, "given twice"},
		{Options{HorizonDays: -1}, "outcome window of -1 days"},
		{Options{Leads: []time.Duration{time.Millisecond}}, "shorter than a second"},
		{Options{Degrade: "D"}, "want B or C"},
		{Options{Base: time.Date(1960, 1, 1, 0, 0, 0, 0, time.UTC)}, "before 1971"},
		{Options{Servers: []string{"nobody"}}, `no row of server "nobody"`},
		{Options{Config: &engine.Config{}}, "thresholds"},
	}
	for _, c := range bad {
		c.opts.Scratch = t.TempDir()
		if _, err := Run(ctx, ds, c.opts); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: error %v, want one that mentions %q", c.opts, err, c.want)
		}
	}
	// Cut-offs may be given in any order, and no lead times at all.
	rep := run(t, ds, Options{Cutoffs: []int{150, 60}, Leads: []time.Duration{}})
	if rep.Cutoffs[0] != 60 || len(rep.Leads) != 0 || len(rep.Periods()) != 2 {
		t.Errorf("cut-offs %v, leads %v", rep.Cutoffs, rep.Leads)
	}
	// A period without cut-offs is left out.
	rep = run(t, ds, Options{Cutoffs: []int{60, 100}})
	if p := rep.Periods(); len(p) != 1 || p[0].Name != "development" {
		t.Errorf("periods = %+v", p)
	}

	// Without tickets there is nothing to compare with.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mcelog.csv"), []byte("sid,memoryid,rankid,bankid,row,col,error_type,error_time\ns,1,0,0,1,8,1,0001-01-01 00:00:00\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	only, _ := dcbrain.Open(dir)
	if _, err := Run(ctx, only, Options{Scratch: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "no ticket table") {
		t.Errorf("no ticket table: %v", err)
	}
	// A table without a usable row.
	os.WriteFile(filepath.Join(dir, "trouble_tickets.csv"), []byte("sid,failure_type,failed_time\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "mcelog.csv"), []byte("sid,memoryid,rankid,bankid,row,col,error_type,error_time\ns,99,0,0,1,8,1,0001-01-01 00:00:00\n"), 0o644)
	only, _ = dcbrain.Open(dir)
	if _, err := Run(ctx, only, Options{Scratch: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "no usable row") {
		t.Errorf("no usable row: %v", err)
	}
	// A run that is cancelled returns the context's error.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Run(cctx, ds, Options{Scratch: t.TempDir()}); err == nil {
		t.Error("a cancelled run returned no error")
	}
}

func TestReportFormats(t *testing.T) {
	_, ds := fleet(t, GenOptions{Seed: 2, Servers: 120})
	cfg := engine.DefaultConfig()
	if err := cfg.Set("rule.row.min_errors", "20"); err != nil {
		t.Fatal(err)
	}
	rep := run(t, ds, Options{Config: &cfg})
	var text, md bytes.Buffer
	if err := rep.Write(&text, FormatText); err != nil {
		t.Fatal(err)
	}
	if err := rep.Write(&md, FormatMarkdown); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"MemorY evaluation on the dcbrain DRAM error dataset",
		"engine " + engine.Version + ", thresholds: rule.row.min_errors=20",
		"cut-offs at the start of days 60, 90, 120, 150, 180, 210 of the dataset calendar; outcome window 30 days; cut-offs from day 150 on are held out",
		"largest day of each month in the error table: 31 30 31 31 29 31 30 31 (the calendar the reader assumes: 31 30 31 31 29 31 30 31)",
		"Findings and what followed, development period (cut-off days 60, 90, 120)",
		"Findings and what followed, held-out period (cut-off days 150, 180, 210)",
		"consider replacement, against a ticket for an uncorrectable error: precision ",
		"replace now: no server",
		"Servers with a ticket of failure type 1 (uncorrectable error):",
		"just before",
		"R-ROW-03",
	} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("the text report lacks %q", want)
		}
		if !strings.Contains(md.String(), want) {
			t.Errorf("the Markdown report lacks %q", want)
		}
	}
	if !strings.Contains(md.String(), "| finding | servers | UE ticket |") || !strings.Contains(md.String(), "| --- | ---: |") || !strings.Contains(md.String(), "### Dataset") {
		t.Errorf("the Markdown report has no tables:\n%s", md.String()[:min(md.Len(), 2000)])
	}
	if err := rep.Write(&text, "yaml"); err == nil {
		t.Error("an unknown format was accepted")
	}
	// The text form of small helpers.
	for d, want := range map[time.Duration]string{time.Second: "just before", 5 * time.Minute: "5 minutes", time.Minute: "1 minute",
		time.Hour: "1 hour", 6 * time.Hour: "6 hours", 24 * time.Hour: "1 day", 720 * time.Hour: "30 days", 90 * time.Second: "1m30s"} {
		if got := leadText(d); got != want {
			t.Errorf("leadText(%s) = %q, want %q", d, got, want)
		}
	}
	// The Wilson interval: known values, and the edges.
	for _, c := range []struct {
		part, total int64
		lo, hi      float64
	}{{20, 81, 0.1659, 0.3508}, {0, 10, 0, 0.2775}, {10, 10, 0.7225, 1}, {1, 1, 0.2065, 1}, {50, 100, 0.4038, 0.5962}} {
		lo, hi := Interval(c.part, c.total)
		if math.Abs(lo-c.lo) > 0.0005 || math.Abs(hi-c.hi) > 0.0005 {
			t.Errorf("Interval(%d, %d) = %.4f to %.4f, want %.4f to %.4f", c.part, c.total, lo, hi, c.lo, c.hi)
		}
	}
	if lo, hi := Interval(0, 0); lo != 0 || hi != 1 {
		t.Errorf("Interval(0, 0) = %v to %v", lo, hi)
	}
	if got := share(20, 81); got != "24.69% (20 of 81, 95% interval 16.59% to 35.08%)" {
		t.Errorf("share = %q", got)
	}
	if got := share(0, 0); got != "- (0 of 0)" {
		t.Errorf("share = %q", got)
	}
	if num(1234567) != "1,234,567" || num(-1000) != "-1,000" || num(12) != "12" || pct(1, 3) != "33.33%" || pct(1, 0) != "-" {
		t.Errorf("number formats: %s %s %s %s %s", num(1234567), num(-1000), num(12), pct(1, 3), pct(1, 0))
	}
}

func TestCalendarLine(t *testing.T) {
	// The report shows the largest day of each month next to the calendar
	// the reader assumes, so that a table with other month lengths is seen.
	rows := []dcbrain.Row{
		{SID: "a", MemoryID: 1, Row: 1, Col: 8, Type: dcbrain.TypeRead, At: dcbrain.StampOfDay(30)},
		{SID: "a", MemoryID: 1, Row: 1, Col: 8, Type: dcbrain.TypeRead, At: dcbrain.StampOfDay(58)},
		{SID: "a", MemoryID: 1, Row: 1, Col: 8, Type: dcbrain.TypeRead, At: dcbrain.StampOfDay(91)},
		{SID: "a", MemoryID: 1, Row: 1, Col: 8, Type: dcbrain.TypeRead, At: dcbrain.StampOfDay(100)},
	}
	dir := t.TempDir()
	if err := (&Generated{Rows: rows}).WriteDir(dir); err != nil {
		t.Fatal(err)
	}
	ds, _ := dcbrain.Open(dir)
	rep := run(t, ds, Options{})
	if rep.Dataset.MonthLastDay != [8]int{31, 28, 31, 9, 0, 0, 0, 0} {
		t.Fatalf("month ends = %v", rep.Dataset.MonthLastDay)
	}
	var buf bytes.Buffer
	rep.Write(&buf, FormatText)
	if want := "largest day of each month in the error table: 31 28 31 9 0 0 0 0 (the calendar the reader assumes: 31 30 31 31 29 31 30 31)"; !strings.Contains(buf.String(), want) {
		t.Errorf("the report lacks %q:\n%s", want, buf.String())
	}
}

func TestGenerateIsStable(t *testing.T) {
	a, b := Generate(GenOptions{Seed: 4, Servers: 80}), Generate(GenOptions{Seed: 4, Servers: 80})
	if len(a.Rows) == 0 || len(a.Rows) != len(b.Rows) || len(a.Tickets) != len(b.Tickets) {
		t.Fatalf("rows %d and %d, tickets %d and %d", len(a.Rows), len(b.Rows), len(a.Tickets), len(b.Tickets))
	}
	for i := range a.Rows {
		if a.Rows[i] != b.Rows[i] {
			t.Fatalf("row %d differs between two runs with one seed", i)
		}
	}
	if c := Generate(GenOptions{Seed: 5, Servers: 80}); len(c.Rows) == len(a.Rows) && c.Rows[0] == a.Rows[0] {
		t.Error("another seed gave the same fleet")
	}
	kinds := map[string]int{}
	for _, k := range a.Kind {
		kinds[k]++
	}
	for _, r := range recipes {
		if kinds[r.name] == 0 && r.share >= 8 {
			t.Errorf("no server of kind %s among 80", r.name)
		}
	}
	if kinds["no-errors"] == 0 {
		t.Error("no ticket of a server without errors")
	}
	if d := Generate(GenOptions{}); len(d.Servers) < 300 {
		t.Errorf("the default fleet has %d servers", len(d.Servers))
	}
	for _, r := range a.Rows {
		if r.At < 0 || r.At.Day() >= dcbrain.SpanDays {
			t.Fatalf("row outside the calendar: %+v", r)
		}
	}
}

func TestDetail(t *testing.T) {
	var buf bytes.Buffer
	fixtureRun(t, Options{Cutoffs: []int{71, 131, 207}, Leads: []time.Duration{time.Second, 24 * time.Hour}, Detail: &buf, Workers: 3})
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if lines[0] != DetailHeader {
		t.Fatalf("header = %q", lines[0])
	}
	// Eight servers at three cut-offs and over all records, and two lead
	// times for each of the three servers with a ticket.
	if want := 1 + 8*4 + 3*2; len(lines) != want {
		t.Fatalf("%d lines, want %d:\n%s", len(lines), want, buf.String())
	}
	for _, want := range []string{
		"s0001,cutoff,71,observe,R-SOFT-01,soft.cell,single-bit,1,no ticket,24,,24,0,7.349,",
		"s0002,cutoff,71,consider-replacement,R-ROW-03,row.rising row.spreading_line,row,1,uncorrectable error,88,failure_type 1 at 0001-03-11 03:14:07,88,38,0.106,",
		"s0002,cutoff,131,,,,,0,failed before,88,failure_type 1 at 0001-03-11 03:14:07,,,,",
		"s0003,cutoff,71,,,,,0,first error later,40,,,,,",
		"s0002,lead,86400,consider-replacement,R-ROW-03,row.rising row.spreading_line,row,1,uncorrectable error,88,failure_type 1 at 0001-03-11 03:14:07,82,36,0.417,",
		"s0006,cutoff,207,hold-replacement,R-SYS-01,sys.channel,system-issue,2,no ticket,30,,30,30,1.106,",
		"s0007,cutoff,207,observe,R-SOFT-01,soft.stray_once,single-bit,3,type 0,3,failure_type 0 at 0001-08-10 09:30:00,3,0,46.179,",
		"s0008,all,244,observe,R-SOFT-01,soft.cell,single-bit,1,,9,,9,0,119.500,",
		// s0005 had its burst of 150 errors about two hours before its ticket.
		"s0005,lead,1,consider-replacement,R-ROW-03,row.rising row.spreading_area row.storm,device,1,correctable errors,210,failure_type 2 at 0001-06-31 05:00:00,210,184,0.090,0.090",
	} {
		found := false
		for _, l := range lines {
			found = found || l == want
		}
		if !found {
			t.Errorf("no line %q in:\n%s", want, buf.String())
		}
	}
	// Ordered by server, then cut-offs, lead times, all records.
	if !strings.HasPrefix(lines[1], "s0001,cutoff,71,") || !strings.HasPrefix(lines[len(lines)-1], "s0008,all,") {
		t.Errorf("first line %q, last line %q", lines[1], lines[len(lines)-1])
	}
	// A writer that fails is an error of the run.
	ds, _ := dcbrain.Open(testutil.Testdata(t, "dcbrain"))
	if _, err := Run(context.Background(), ds, Options{Detail: failWriter{}, Scratch: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "detail") {
		t.Errorf("failing detail writer: %v", err)
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, os.ErrClosed }
