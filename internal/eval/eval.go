// Package eval measures the analysis engine on the DRAM error dataset of
// github.com/alibaba-edu/dcbrain (see internal/adapter/dcbrain): what the
// engine makes of the errors of a real fleet, and how its findings compare
// with the failure tickets that followed.
//
// The method is a replay. For every server the engine sees the errors up
// to a cut-off time and nothing after it. The finding it gives is then
// compared with the tickets of the outcome window that follows the
// cut-off. A second replay looks at each server that failed, at fixed
// times before its failure, and asks what the engine would have shown.
//
// The harness reads the dataset as a stream and analyses one server at a
// time; memory use does not grow with the size of the fleet.
package eval

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/agenticode/my/internal/adapter/dcbrain"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/model"
)

// Defaults of a run.
var (
	// DefaultCutoffs are the days of the dataset calendar findings are
	// taken at. The first 60 days give the engine the history its trend
	// windows need, and every outcome window ends within the 244 days.
	DefaultCutoffs = []int{60, 90, 120, 150, 180, 210}
	// DefaultLeads are the times before a failure at which the finding of
	// the failed server is taken. The first one, a second, stands for
	// "just before the failure".
	DefaultLeads = []time.Duration{time.Second, 5 * time.Minute, time.Hour, 24 * time.Hour, 7 * 24 * time.Hour, 30 * 24 * time.Hour}
)

const (
	// DefaultHorizonDays is the length of the outcome window.
	DefaultHorizonDays = 30
	// DefaultSplitDay separates the development period from the held-out
	// period: cut-offs before this day belong to the first.
	DefaultSplitDay = 150
	// DefaultMaxRecords is the largest number of records of one server
	// that is analysed.
	DefaultMaxRecords = 2_000_000
)

// Options describe a run. The zero value uses the defaults.
type Options struct {
	// Cutoffs are the days of the dataset calendar, counted from 0, at
	// whose start findings are taken.
	Cutoffs []int
	// HorizonDays is the length of the outcome window after a cut-off.
	HorizonDays int
	// SplitDay separates development and held-out cut-offs. Thresholds
	// may be chosen by looking at the development period; the held-out
	// period shows whether the choice holds on data it was not made on.
	SplitDay int
	// Leads are the times before a failure at which the finding of the
	// failed server is taken.
	Leads []time.Duration
	// Config holds the thresholds of the engine; nil means the defaults.
	Config *engine.Config
	// Base is the real time day 0 of the dataset calendar is placed at.
	Base time.Time
	// Degrade removes location detail before the engine sees a record, to
	// measure what a platform of a lower observation grade would give:
	// GradeB keeps the rank and drops bank, row and column; GradeC turns
	// the errors of a DIMM into one count per day. Empty keeps everything.
	Degrade model.Grade
	// FromFirstError takes the first error of a server as the start of
	// what is known about it. By default every server counts as watched
	// from the first day of the dataset: the dataset is the error log of
	// a fleet over eight months, so a server without rows before its
	// first error had no errors until then, and the engine may call a
	// burst of errors new.
	FromFirstError bool
	// Workers is the number of servers analysed at a time.
	Workers int
	// MaxRecords bounds the records of one server. A server with more is
	// merged in wider time buckets, and left out when that is not enough.
	MaxRecords int
	// Spool is a directory in which the error table, split by server, is
	// kept between runs. Empty means a temporary copy under Scratch.
	Spool string
	// Scratch is the directory temporary files are made in; empty means
	// the system's.
	Scratch string
	// Servers limits the run to these server IDs.
	Servers []string
	// Detail, when set, receives one line per server and time the engine
	// was asked at: the finding, the rules and what followed. The lines
	// are comma-separated under a header, ordered by server.
	Detail io.Writer
	// Progress receives lines about how far the run is; nil drops them.
	Progress func(format string, args ...any)
}

func (o *Options) fill() error {
	if len(o.Cutoffs) == 0 {
		o.Cutoffs = append([]int(nil), DefaultCutoffs...)
	}
	if o.HorizonDays == 0 {
		o.HorizonDays = DefaultHorizonDays
	}
	if o.SplitDay == 0 {
		o.SplitDay = DefaultSplitDay
	}
	if o.Leads == nil {
		o.Leads = append([]time.Duration(nil), DefaultLeads...)
	}
	if o.Base.IsZero() {
		o.Base = dcbrain.DefaultBase
	}
	o.Base = o.Base.UTC()
	if o.Workers <= 0 {
		o.Workers = min(2, runtime.GOMAXPROCS(0))
	}
	if o.MaxRecords <= 0 {
		o.MaxRecords = DefaultMaxRecords
	}
	if o.Progress == nil {
		o.Progress = func(string, ...any) {}
	}
	sort.Ints(o.Cutoffs)
	for i, c := range o.Cutoffs {
		if c < 1 || c >= dcbrain.SpanDays {
			return fmt.Errorf("eval: cut-off day %d is outside 1 to %d", c, dcbrain.SpanDays-1)
		}
		if i > 0 && c == o.Cutoffs[i-1] {
			return fmt.Errorf("eval: cut-off day %d is given twice", c)
		}
		if i > 0 && c-o.Cutoffs[i-1] < o.HorizonDays {
			// Windows that overlap would count one failure twice.
			return fmt.Errorf("eval: cut-off days %d and %d are closer than the outcome window of %d days", o.Cutoffs[i-1], c, o.HorizonDays)
		}
	}
	if o.HorizonDays < 1 {
		return fmt.Errorf("eval: an outcome window of %d days", o.HorizonDays)
	}
	for _, l := range o.Leads {
		if l < time.Second {
			return fmt.Errorf("eval: lead time %s is shorter than a second", l)
		}
	}
	switch o.Degrade {
	case "", model.GradeA:
		o.Degrade = ""
	case model.GradeB, model.GradeC:
	default:
		return fmt.Errorf("eval: cannot degrade to observation grade %q; want B or C", o.Degrade)
	}
	if o.Base.Year() < 1971 {
		return errors.New("eval: the base date must not lie before 1971")
	}
	return nil
}

// Outcome says what happened to a server in an outcome window.
type Outcome int

const (
	// NoFailure: no ticket in the window.
	NoFailure Outcome = iota
	// FailedUnspecified: a ticket of failure_type 0, which the dataset
	// description does not define.
	FailedUnspecified
	// FailedUE: a ticket for an uncorrectable error.
	FailedUE
	// FailedCE: a ticket for too many correctable errors.
	FailedCE
	// FailedMisc: a ticket for another memory failure.
	FailedMisc
	numOutcomes
)

// outcomeOf returns the outcome a ticket of the given type stands for.
func outcomeOf(t dcbrain.FailureType) Outcome {
	switch t {
	case dcbrain.FailureUE:
		return FailedUE
	case dcbrain.FailureCE:
		return FailedCE
	case dcbrain.FailureMisc:
		return FailedMisc
	}
	return FailedUnspecified
}

// String names the outcome.
func (o Outcome) String() string {
	switch o {
	case NoFailure:
		return "no ticket"
	case FailedUnspecified:
		return "type 0"
	case FailedUE:
		return "uncorrectable error"
	case FailedCE:
		return "correctable errors"
	case FailedMisc:
		return "miscellaneous"
	}
	return "?"
}

// Counts holds numbers of servers by outcome.
type Counts [numOutcomes]int64

// Total returns the number of servers.
func (c Counts) Total() int64 {
	var n int64
	for _, v := range c {
		n += v
	}
	return n
}

// Failed returns the number of servers with a ticket of failure type 1, 2
// or 3: the types the dataset description defines.
func (c Counts) Failed() int64 { return c[FailedUE] + c[FailedCE] + c[FailedMisc] }

func (c *Counts) add(o Counts) {
	for i := range c {
		c[i] += o[i]
	}
}

// Grades lists the findings in the order of their exit codes. Arrays
// indexed by grade use this order.
var Grades = []model.VerdictGrade{model.VerdictOK, model.VerdictObserve, model.VerdictConsiderReplacement, model.VerdictReplaceNow, model.VerdictHoldReplacement}

const numGrades = 5

// Modes lists the fault modes in order of growing extent, the mode
// "undetermined" first. Arrays indexed by mode use this order.
var Modes = append([]model.FaultMode{model.ModeUndetermined}, model.FaultModes...)

func modeIndex(m model.FaultMode) int {
	for i, x := range Modes {
		if x == m {
			return i
		}
	}
	return 0
}

// CutoffStats is what the engine found at one cut-off and what followed.
// Every count is a number of servers unless it says otherwise.
type CutoffStats struct {
	// Day is the cut-off: findings use the errors up to the start of this
	// day of the dataset calendar.
	Day int `json:"day"`
	// HeldOut is set for cut-offs of the held-out period.
	HeldOut bool `json:"held_out"`
	// Servers is the population: servers with an error up to the cut-off
	// and no ticket up to it.
	Servers int64 `json:"servers"`
	// FailedBefore counts servers left out because their ticket lies at or
	// before the cut-off, and NotYet servers whose first error is later.
	FailedBefore int64 `json:"failed_before"`
	NotYet       int64 `json:"not_yet"`
	// ByGrade splits the population by the finding for the server (the
	// worst finding among its DIMMs) and by what followed.
	ByGrade [numGrades]Counts `json:"by_grade"`
	// DIMMs counts the DIMMs of the population by their finding.
	DIMMs [numGrades]int64 `json:"dimms"`
	// ByRule counts, per rule, the servers on which the rule fired for at
	// least one DIMM. ByCode does the same per statement of the evidence.
	ByRule map[string]Counts `json:"by_rule"`
	ByCode map[string]Counts `json:"by_code"`
	// ByMode splits the population by the widest fault mode on the server.
	ByMode map[model.FaultMode]Counts `json:"by_mode"`
	// Active is a reference without the engine: the servers of the
	// population with an error in the recent window before the cut-off.
	Active Counts `json:"active"`
	// Unseen counts the tickets of the window whose server is outside the
	// population because it had no error up to the cut-off.
	Unseen Counts `json:"unseen"`
}

// LeadStats is what the engine showed at one lead time before the failures.
type LeadStats struct {
	Lead time.Duration `json:"lead"`
	// ByType is indexed by Outcome; NoFailure is unused.
	ByType [numOutcomes]LeadCounts `json:"by_type"`
}

// LeadCounts counts failed servers by what the engine showed before the
// failure.
type LeadCounts struct {
	// Tickets is the number of failed servers that were analysed.
	Tickets int64 `json:"tickets"`
	// NoErrors counts those without an error record at that time.
	NoErrors int64 `json:"no_errors"`
	// ByGrade splits the rest by the finding for the server.
	ByGrade [numGrades]int64 `json:"by_grade"`
}

// ModeStats counts the faults of one mode.
type ModeStats struct {
	Mode model.FaultMode `json:"mode"`
	// Faults is the number of faults, Estimated the number among them
	// whose mode is an estimate, and Errors the errors in them.
	Faults    int64 `json:"faults"`
	Estimated int64 `json:"estimated"`
	Errors    int64 `json:"errors"`
	// DIMMs is the number of DIMMs whose fault with the most errors has
	// this mode, and Widest the number whose widest fault has it.
	DIMMs  int64 `json:"dimms"`
	Widest int64 `json:"widest"`
}

// Census is what the engine makes of all records of the dataset.
type Census struct {
	// Servers and DIMMs that were analysed.
	Servers int64 `json:"servers"`
	DIMMs   int64 `json:"dimms"`
	// Records is the number of records after merging, Errors the errors
	// they stand for.
	Records int64 `json:"records"`
	Errors  int64 `json:"errors"`
	// GradeErrors counts the errors by observation grade A, B and C, and
	// GradeDIMMs the DIMMs by the best grade among their errors.
	GradeErrors [3]int64 `json:"grade_errors"`
	GradeDIMMs  [3]int64 `json:"grade_dimms"`
	// NoDevice counts the errors without a DRAM device and NoBits the
	// errors without failing bits.
	NoDevice int64 `json:"no_device"`
	NoBits   int64 `json:"no_bits"`
	// Modes is ordered like Modes.
	Modes []ModeStats `json:"modes"`
	// DIMMGrades and ServerGrades count findings as of the end of the data.
	DIMMGrades   [numGrades]int64 `json:"dimm_grades"`
	ServerGrades [numGrades]int64 `json:"server_grades"`
	// Rules counts, per rule, the DIMMs it fired on, and Codes the DIMMs
	// per statement of the evidence.
	Rules map[string]int64 `json:"rules"`
	Codes map[string]int64 `json:"codes"`
}

// ServerNote names a server that was not analysed like the others.
type ServerNote struct {
	SID  string `json:"sid"`
	Rows int64  `json:"rows"`
	// DIMMs is the number of DIMMs of the server that have rows.
	DIMMs int `json:"dimms"`
	// Records is the number of records after merging; 0 for a server that
	// was left out.
	Records int `json:"records"`
	// BucketSeconds is the width of the time buckets its rows were merged in.
	BucketSeconds int `json:"bucket_seconds"`
	// Ticket describes the failure ticket of the server, if it has one.
	Ticket string `json:"ticket,omitzero"`
}

// DatasetInfo says what was read.
type DatasetInfo struct {
	Path      string              `json:"path"`
	Errors    dcbrain.TableReport `json:"errors"`
	Inventory dcbrain.TableReport `json:"inventory"`
	Tickets   dcbrain.TableReport `json:"tickets"`
	// Rows is the number of usable rows of the error table.
	Rows int64 `json:"rows"`
	// ByType counts rows by error_type 1, 2 and 3 (index 0 is unused).
	ByType [4]int64 `json:"by_type"`
	// Servers is the number of servers with rows, and DIMMs the number of
	// different DIMMs (server and memoryid) with rows.
	Servers int64 `json:"servers"`
	DIMMs   int64 `json:"dimms"`
	// First and Last are the earliest and the latest error time on the
	// dataset calendar.
	First string `json:"first"`
	Last  string `json:"last"`
	// MonthLastDay is the largest day of each month of the dataset
	// calendar that occurs in the error table. It shows whether the
	// calendar the reader assumes fits the data.
	MonthLastDay [dcbrain.Months]int `json:"month_last_day"`
	MaxMemoryID  int                 `json:"max_memoryid"`
	MaxRank      int                 `json:"max_rank"`
	MaxBank      int                 `json:"max_bank"`
	MaxRow       int                 `json:"max_row"`
	MaxCol       int                 `json:"max_col"`
	// InventoryServers is the number of servers the inventory lists, and
	// NotInInventory the number of servers with rows it does not list.
	InventoryServers int64 `json:"inventory_servers"`
	NotInInventory   int64 `json:"not_in_inventory"`
	// TicketsByType counts tickets by failure_type 0 to 3.
	TicketsByType [4]int64 `json:"tickets_by_type"`
	// TicketsWithRows counts, by failure type, the tickets whose server
	// has rows in the error table.
	TicketsWithRows [4]int64 `json:"tickets_with_rows"`
	// ExtraTickets is the number of tickets beyond the first of a server.
	// Only the first ticket of a server is used.
	ExtraTickets int64 `json:"extra_tickets"`
	// Layout holds what the errors say about the place of a memoryid on
	// the board, which the dataset description does not give.
	Layout Layout `json:"layout"`
}

// Layout counts how the DIMMs with errors lie on the board that
// dcbrain.Topology assumes: two sockets, six channels each, two DIMMs per
// channel. If the assumption holds, DIMMs that fail together should share
// a channel more often than chance puts them there, and servers with one
// DIMM per channel should use one position only.
type Layout struct {
	// All counts the pairs of DIMMs with errors on one server, over all
	// servers. Full counts them on the servers the inventory lists with
	// 24 DIMMs: there every slot is filled, and chance puts 12 of the 276
	// pairs of slots on one channel and 120 on one socket but different
	// channels.
	All  Pairs `json:"all"`
	Full Pairs `json:"full"`
	// ByDIMMs counts the DIMMs with errors by the number of DIMMs of their
	// server (from the inventory; 0 when unknown) and by the position on
	// the channel the layout gives them (0 or 1).
	ByDIMMs map[int][2]int64 `json:"by_dimm_count"`
}

// Pairs counts pairs of DIMMs with errors on one server.
type Pairs struct {
	// Servers is the number of servers with errors on two or more DIMMs,
	// and Pairs the number of pairs of such DIMMs.
	Servers int64 `json:"servers"`
	Pairs   int64 `json:"pairs"`
	// SameChannel and SameSocket count the pairs the layout puts on one
	// channel, and on one socket but different channels.
	SameChannel int64 `json:"same_channel"`
	SameSocket  int64 `json:"same_socket"`
}

// Report is the result of a run.
type Report struct {
	EngineVersion string `json:"engine_version"`
	// Thresholds lists the thresholds that differ from the defaults.
	Thresholds [][2]string `json:"thresholds"`
	// The settings of the run.
	Cutoffs     []int           `json:"cutoffs"`
	HorizonDays int             `json:"horizon_days"`
	SplitDay    int             `json:"split_day"`
	LeadTimes   []time.Duration `json:"lead_times"`
	Degrade     model.Grade     `json:"degrade,omitzero"`
	// FromFirstError is set when a server's history starts with its first
	// error instead of the first day of the dataset.
	FromFirstError bool    `json:"from_first_error,omitzero"`
	RecentDays     float64 `json:"recent_days"`

	Dataset DatasetInfo   `json:"dataset"`
	Census  Census        `json:"census"`
	ByCut   []CutoffStats `json:"by_cutoff"`
	Leads   []LeadStats   `json:"leads"`
	// Widened lists the servers whose rows were merged in buckets wider
	// than a second, and Dropped the servers that were left out because
	// they have too many records.
	Widened []ServerNote `json:"widened,omitzero"`
	Dropped []ServerNote `json:"dropped,omitzero"`
	// Elapsed is how long the run took. It is not part of the JSON form,
	// so that two runs over the same data give the same output.
	Elapsed time.Duration `json:"-"`
}

// heavyRecords is the number of records from which a server is analysed
// alone: the engine holds several copies of the records of a server, and
// two large servers at a time would double the memory the run needs.
const heavyRecords = 200_000

// runner holds what a run shares between servers.
type runner struct {
	// heavy is held while a server with many records is analysed.
	heavy   sync.Mutex
	details []detail
	opts    Options
	cfg     engine.Config
	base    time.Time
	end     dcbrain.Stamp
	cutoffs []dcbrain.Stamp
	horizon dcbrain.Stamp
	recent  dcbrain.Stamp
	tickets map[string]dcbrain.Ticket
	// dimmCount is the number of DIMMs of each server per the inventory.
	dimmCount map[string]int
}

// Run evaluates the engine on the dataset.
func Run(ctx context.Context, ds *dcbrain.Dataset, opts Options) (*Report, error) {
	started := time.Now()
	if err := opts.fill(); err != nil {
		return nil, err
	}
	cfg := engine.DefaultConfig()
	if opts.Config != nil {
		cfg = *opts.Config
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("eval: thresholds: %w", err)
	}
	r := &runner{opts: opts, cfg: cfg, base: opts.Base, end: dcbrain.StampOfDay(dcbrain.SpanDays),
		horizon: dcbrain.StampOfDay(opts.HorizonDays), recent: dcbrain.Stamp(cfg.RecentDays * 86400), tickets: map[string]dcbrain.Ticket{},
		dimmCount: map[string]int{}}
	for _, c := range opts.Cutoffs {
		r.cutoffs = append(r.cutoffs, dcbrain.StampOfDay(c))
	}
	rep := &Report{EngineVersion: engine.Version, Thresholds: cfg.Changed(), Cutoffs: opts.Cutoffs, HorizonDays: opts.HorizonDays,
		SplitDay: opts.SplitDay, LeadTimes: opts.Leads, Degrade: opts.Degrade, FromFirstError: opts.FromFirstError, RecentDays: cfg.RecentDays}
	rep.Dataset.Path = ds.Path
	rep.Dataset.Layout.ByDIMMs = map[int][2]int64{}
	rep.Census.Rules, rep.Census.Codes = map[string]int64{}, map[string]int64{}
	for _, m := range Modes {
		rep.Census.Modes = append(rep.Census.Modes, ModeStats{Mode: m})
	}
	for _, c := range opts.Cutoffs {
		rep.ByCut = append(rep.ByCut, CutoffStats{Day: c, HeldOut: c >= opts.SplitDay,
			ByRule: map[string]Counts{}, ByCode: map[string]Counts{}, ByMode: map[model.FaultMode]Counts{}})
	}
	for _, l := range opts.Leads {
		rep.Leads = append(rep.Leads, LeadStats{Lead: l})
	}

	inventory, err := r.readSideTables(ds, rep)
	if err != nil {
		return nil, err
	}

	sp, err := openSpool(ctx, ds, opts.Spool, opts.Scratch, opts.Progress)
	if err != nil {
		return nil, err
	}
	defer sp.close()
	info := &sp.info
	d := &rep.Dataset
	d.Errors, d.Rows, d.ByType, d.Servers = info.Table, info.Rows, info.ByType, int64(len(info.Servers))
	d.First, d.Last = dcbrain.Stamp(info.First).String(), dcbrain.Stamp(info.Last).String()
	d.MonthLastDay, d.MaxMemoryID, d.MaxRank, d.MaxBank, d.MaxRow, d.MaxCol = info.MonthLastDay, info.MaxMemoryID, info.MaxRank, info.MaxBank, info.MaxRow, info.MaxCol
	if info.Rows == 0 {
		return nil, fmt.Errorf("eval: %s holds no usable row", ds.Name(dcbrain.TableErrors))
	}

	withRows := map[string]bool{}
	for i, sid := range info.Servers {
		withRows[sid] = true
		if inventory != nil && !inventory[sid] {
			d.NotInInventory++
		}
		d.DIMMs += int64(bits.OnesCount32(info.ServerDIMMs[i]))
		d.Layout.add(info.ServerDIMMs[i], r.dimmCount[sid])
	}
	for sid, t := range r.tickets {
		if withRows[sid] {
			d.TicketsWithRows[t.Type]++
		}
	}
	only := map[string]bool{}
	for _, sid := range opts.Servers {
		if !withRows[sid] {
			return nil, fmt.Errorf("eval: the error table has no row of server %q", sid)
		}
		only[sid] = true
	}

	// Tickets of servers that never appear in the error table belong to
	// no population. They are counted with the windows they fall in.
	if len(only) == 0 {
		for sid, t := range r.tickets {
			if !withRows[sid] {
				r.unseen(rep, t)
			}
		}
	}

	opts.Progress("analysing %d servers", len(info.Servers))
	// Workers analyse the servers of one bucket at a time; one goroutine
	// adds the results up, so the sums do not depend on the order.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	buckets := make(chan int)
	results := make(chan *serverResult, 64)
	var wg sync.WaitGroup
	var firstErr error
	var errOnce sync.Once
	fail := func(err error) {
		errOnce.Do(func() { firstErr = err; cancel() })
	}
	for w := 0; w < opts.Workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for b := range buckets {
				servers, err := sp.readBucket(b, opts.MaxRecords)
				if err != nil {
					fail(err)
					return
				}
				for i := range servers {
					if ctx.Err() != nil {
						return
					}
					sr := &servers[i]
					sid := info.Servers[sr.index]
					if len(only) > 0 && !only[sid] {
						continue
					}
					if len(sr.recs) >= heavyRecords {
						r.heavy.Lock()
					}
					res, err := r.server(sid, sr)
					if len(sr.recs) >= heavyRecords {
						r.heavy.Unlock()
					}
					if err != nil {
						fail(fmt.Errorf("eval: server %s: %w", sid, err))
						return
					}
					select {
					case results <- res:
					case <-ctx.Done():
						return
					}
					// The records of the server are not needed any more.
					sr.recs = nil
				}
			}
		}()
	}
	go func() {
		defer close(buckets)
		for b := 0; b < spoolBuckets; b++ {
			select {
			case buckets <- b:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()
	done := 0
	for res := range results {
		r.merge(rep, res)
		done++
		if done%5000 == 0 {
			opts.Progress("analysed %d of %d servers", done, len(info.Servers))
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := r.writeDetail(); err != nil {
		return nil, fmt.Errorf("eval: detail: %w", err)
	}
	sort.Slice(rep.Widened, func(i, j int) bool { return rep.Widened[i].SID < rep.Widened[j].SID })
	sort.Slice(rep.Dropped, func(i, j int) bool { return rep.Dropped[i].SID < rep.Dropped[j].SID })
	rep.Elapsed = time.Since(started)
	return rep, nil
}

// readSideTables reads the inventory and the tickets. Both are small.
func (r *runner) readSideTables(ds *dcbrain.Dataset, rep *Report) (inventory map[string]bool, err error) {
	d := &rep.Dataset
	if ds.Has(dcbrain.TableInventory) {
		rc, err := ds.OpenTable(dcbrain.TableInventory)
		if err != nil {
			return nil, fmt.Errorf("eval: %w", err)
		}
		servers, tr, err := dcbrain.ReadInventory(rc, ds.Name(dcbrain.TableInventory))
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("eval: %w", err)
		}
		d.Inventory = tr
		inventory = map[string]bool{}
		for _, s := range servers {
			inventory[s.SID] = true
			r.dimmCount[s.SID] = s.DIMMs
		}
		d.InventoryServers = int64(len(inventory))
	}
	if !ds.Has(dcbrain.TableTickets) {
		return nil, fmt.Errorf("eval: no ticket table (trouble_tickets.csv or trouble_tickets.tar.gz) under %s; without tickets there is nothing to compare the findings with", ds.Path)
	}
	rc, err := ds.OpenTable(dcbrain.TableTickets)
	if err != nil {
		return nil, fmt.Errorf("eval: %w", err)
	}
	tickets, tr, err := dcbrain.ReadTickets(rc, ds.Name(dcbrain.TableTickets))
	rc.Close()
	if err != nil {
		return nil, fmt.Errorf("eval: %w", err)
	}
	d.Tickets = tr
	for _, t := range tickets {
		d.TicketsByType[t.Type]++
		// The first ticket of a server is the failure the findings are
		// compared with.
		if old, ok := r.tickets[t.SID]; ok {
			d.ExtraTickets++
			if old.At <= t.At {
				continue
			}
		}
		r.tickets[t.SID] = t
	}
	return inventory, nil
}

// window returns the index of the cut-off whose outcome window holds the
// time, or -1.
func (r *runner) window(at dcbrain.Stamp) int {
	for i, c := range r.cutoffs {
		if at > c && at <= c+r.horizon {
			return i
		}
	}
	return -1
}

// unseen counts a ticket whose server had no error before the window.
func (r *runner) unseen(rep *Report, t dcbrain.Ticket) {
	if i := r.window(t.At); i >= 0 {
		rep.ByCut[i].Unseen[outcomeOf(t.Type)]++
	}
}

// add counts the DIMMs with errors of one server: memory has a bit set for
// every memoryid with errors, and dimms is the number of DIMMs the
// inventory gives the server.
func (l *Layout) add(memory uint32, dimms int) {
	var ids []int
	for id := 0; id < 32; id++ {
		if memory&(1<<id) != 0 {
			ids = append(ids, id)
		}
	}
	by := l.ByDIMMs[dimms]
	for _, id := range ids {
		_, _, position := dcbrain.Topology(id)
		by[position]++
	}
	l.ByDIMMs[dimms] = by
	if len(ids) < 2 {
		return
	}
	count := func(p *Pairs) {
		p.Servers++
		for i, a := range ids {
			for _, b := range ids[i+1:] {
				p.Pairs++
				sa, ca, _ := dcbrain.Topology(a)
				sb, cb, _ := dcbrain.Topology(b)
				switch {
				case sa == sb && ca == cb:
					p.SameChannel++
				case sa == sb:
					p.SameSocket++
				}
			}
		}
	}
	count(&l.All)
	if dimms == 24 {
		count(&l.Full)
	}
}
