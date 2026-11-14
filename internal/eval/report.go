package eval

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agenticode/my/internal/adapter/dcbrain"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/model"
)

// Formats of a report.
const (
	FormatText     = "text"
	FormatMarkdown = "md"
	FormatJSON     = "json"
)

// Write prints the report in the given format: plain text, Markdown
// tables, or JSON.
func (r *Report) Write(w io.Writer, format string) error {
	switch format {
	case FormatJSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	case FormatText, FormatMarkdown:
		p := &printer{w: w, md: format == FormatMarkdown}
		r.print(p)
		return p.err
	}
	return fmt.Errorf("eval: unknown format %q; want text, md or json", format)
}

// printer writes sections, lines and tables as text or as Markdown.
type printer struct {
	w   io.Writer
	md  bool
	err error
}

func (p *printer) printf(format string, a ...any) {
	if p.err == nil {
		_, p.err = fmt.Fprintf(p.w, format, a...)
	}
}

func (p *printer) section(title string) {
	if p.md {
		p.printf("\n### %s\n\n", title)
		return
	}
	p.printf("\n%s\n", title)
}

// line prints one statement.
func (p *printer) line(format string, a ...any) {
	if p.md {
		p.printf("- "+format+"\n", a...)
		return
	}
	p.printf("  "+format+"\n", a...)
}

// table prints rows under a header, below a caption that says what the
// rows are. The first column is text, the others are numbers and are
// aligned to the right.
func (p *printer) table(caption string, head []string, rows [][]string) {
	if p.md {
		if caption != "" {
			p.printf("\n%s\n", caption)
		}
		p.printf("\n| %s |\n|", strings.Join(head, " | "))
		for i := range head {
			if i == 0 {
				p.printf(" --- |")
			} else {
				p.printf(" ---: |")
			}
		}
		p.printf("\n")
		for _, row := range rows {
			p.printf("| %s |\n", strings.Join(row, " | "))
		}
		p.printf("\n")
		return
	}
	if caption != "" {
		p.printf("\n  %s\n", caption)
	}
	width := make([]int, len(head))
	for i, h := range head {
		width[i] = len(h)
	}
	for _, row := range rows {
		for i, c := range row {
			width[i] = max(width[i], len(c))
		}
	}
	write := func(row []string) {
		var b strings.Builder
		b.WriteString("    ")
		for i, c := range row {
			if i == 0 {
				fmt.Fprintf(&b, "%-*s", width[i], c)
			} else {
				fmt.Fprintf(&b, "  %*s", width[i], c)
			}
		}
		p.printf("%s\n", strings.TrimRight(b.String(), " "))
	}
	write(head)
	for _, row := range rows {
		write(row)
	}
}

// num writes a count with thousands separators.
func num(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		return "-" + s
	}
	return s
}

// pct writes a share of a total as a percentage, or "-" when there is no
// total.
func pct(part, total int64) string {
	if total == 0 {
		return "-"
	}
	return strconv.FormatFloat(100*float64(part)/float64(total), 'f', 2, 64) + "%"
}

// leadText names a lead time.
func leadText(d time.Duration) string {
	switch {
	case d <= time.Second:
		return "just before"
	case d%(24*time.Hour) == 0:
		n := int(d / (24 * time.Hour))
		if n == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", n)
	case d%time.Hour == 0:
		n := int(d / time.Hour)
		if n == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", n)
	case d%time.Minute == 0:
		n := int(d / time.Minute)
		if n == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", n)
	}
	return d.String()
}

func gradeText(g model.VerdictGrade) string {
	return strings.ReplaceAll(string(g), "-", " ")
}

// Period sums the cut-offs of the development or of the held-out period.
type Period struct {
	Name string
	// Days are the cut-off days of the period.
	Days []int
	// Sum holds the cut-offs added up. A server counts once per cut-off
	// at which it is in the population.
	Sum CutoffStats
}

// Periods returns the development and the held-out period. A period
// without cut-offs is left out.
func (r *Report) Periods() []Period {
	var out []Period
	for _, held := range []bool{false, true} {
		p := Period{Name: "development"}
		if held {
			p.Name = "held-out"
		}
		p.Sum = CutoffStats{HeldOut: held, ByRule: map[string]Counts{}, ByCode: map[string]Counts{}, ByMode: map[model.FaultMode]Counts{}}
		for i := range r.ByCut {
			c := &r.ByCut[i]
			if c.HeldOut != held {
				continue
			}
			p.Days = append(p.Days, c.Day)
			s := &p.Sum
			s.Servers += c.Servers
			s.FailedBefore += c.FailedBefore
			s.NotYet += c.NotYet
			for g := range c.ByGrade {
				s.ByGrade[g].add(c.ByGrade[g])
				s.DIMMs[g] += c.DIMMs[g]
			}
			for k, v := range c.ByRule {
				x := s.ByRule[k]
				x.add(v)
				s.ByRule[k] = x
			}
			for k, v := range c.ByCode {
				x := s.ByCode[k]
				x.add(v)
				s.ByCode[k] = x
			}
			for k, v := range c.ByMode {
				x := s.ByMode[k]
				x.add(v)
				s.ByMode[k] = x
			}
			s.Active.add(c.Active)
			s.Unseen.add(c.Unseen)
		}
		if len(p.Days) > 0 {
			out = append(out, p)
		}
	}
	return out
}

// All returns the outcomes of the whole population of the cut-off.
func (c *CutoffStats) All() Counts {
	var out Counts
	for g := range c.ByGrade {
		out.add(c.ByGrade[g])
	}
	return out
}

// Score is how one set of flagged servers compares with the failures.
type Score struct {
	// Flagged is the number of servers in the set and Hit the number among
	// them that failed. Failures is the number of failed servers in the
	// population.
	Flagged, Hit, Failures int64
}

// Precision is the share of the flagged servers that failed.
func (s Score) Precision() (float64, bool) {
	if s.Flagged == 0 {
		return 0, false
	}
	return float64(s.Hit) / float64(s.Flagged), true
}

// Recall is the share of the failed servers that were flagged.
func (s Score) Recall() (float64, bool) {
	if s.Failures == 0 {
		return 0, false
	}
	return float64(s.Hit) / float64(s.Failures), true
}

// ScoreOf compares the servers with the given finding against the
// failures of the population. With ueOnly set, only tickets for an
// uncorrectable error count as failures; otherwise the failure types 1, 2
// and 3 do.
func (c *CutoffStats) ScoreOf(g model.VerdictGrade, ueOnly bool) Score {
	hit := func(x Counts) int64 {
		if ueOnly {
			return x[FailedUE]
		}
		return x.Failed()
	}
	by := c.ByGrade[gradeIndex(g)]
	return Score{Flagged: by.Total(), Hit: hit(by), Failures: hit(c.All())}
}

func days(ds []int) string {
	parts := make([]string, len(ds))
	for i, d := range ds {
		parts[i] = strconv.Itoa(d)
	}
	return strings.Join(parts, ", ")
}

// outcomeRow writes the cells of one row of an outcome table: the number
// of servers, the tickets by type, and the share with a ticket of type 1,
// 2 or 3.
func outcomeRow(name string, c Counts) []string {
	return []string{name, num(c.Total()), num(c[FailedUE]), num(c[FailedCE]), num(c[FailedMisc]), num(c[FailedUnspecified]),
		pct(c.Failed(), c.Total()), pct(c[FailedUE], c.Total())}
}

var outcomeHead = []string{"servers", "UE ticket", "CE ticket", "misc ticket", "type 0 ticket", "types 1-3", "UE"}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (r *Report) print(p *printer) {
	if p.md {
		p.printf("## MemorY evaluation on the dcbrain DRAM error dataset\n\n")
	} else {
		p.printf("MemorY evaluation on the dcbrain DRAM error dataset\n")
	}
	thresholds := "defaults"
	if len(r.Thresholds) > 0 {
		parts := make([]string, len(r.Thresholds))
		for i, kv := range r.Thresholds {
			parts[i] = kv[0] + "=" + kv[1]
		}
		thresholds = strings.Join(parts, ", ")
	}
	p.line("engine %s, thresholds: %s", r.EngineVersion, thresholds)
	p.line("cut-offs at the start of days %s of the dataset calendar; outcome window %d days; cut-offs from day %d on are held out",
		days(r.Cutoffs), r.HorizonDays, r.SplitDay)
	switch r.Degrade {
	case model.GradeB:
		p.line("records degraded to observation grade B: bank, row and column removed, rank kept")
	case model.GradeC:
		p.line("records degraded to observation grade C: one count per DIMM and day, read at the end of the day")
	}

	r.printDataset(p)
	r.printCensus(p)
	r.printCutoffs(p)
	r.printLeads(p)
	if !p.md && r.Elapsed > 0 {
		p.printf("\nrun time %s\n", r.Elapsed.Round(time.Second))
	}
}

func (r *Report) printDataset(p *printer) {
	d := &r.Dataset
	p.section("Dataset")
	table := func(name string, t dcbrain.TableReport) {
		if t.Name == "" {
			p.line("%s: not found", name)
			return
		}
		p.line("%s: %s, %s lines, %s skipped", name, t.Name, num(t.Rows), num(t.Skipped))
		for _, n := range t.Notes {
			p.line("  %s", n)
		}
	}
	table("error table", d.Errors)
	table("inventory", d.Inventory)
	table("tickets", d.Tickets)
	p.line("rows used: %s (found by a read %s, by the scrubber %s, by a write %s)", num(d.Rows), num(d.ByType[1]), num(d.ByType[2]), num(d.ByType[3]))
	p.line("servers with errors: %s; DIMMs with errors: %s", num(d.Servers), num(d.DIMMs))
	p.line("errors from %s to %s on the dataset calendar", d.First, d.Last)
	// The largest day of each month next to the length the reader's
	// calendar gives the month: a month of the table that ends earlier
	// than the calendar, with months after it, would mean the calendar has
	// days the data does not have.
	last, assumed := make([]string, len(d.MonthLastDay)), make([]string, len(d.MonthLastDay))
	for i, v := range d.MonthLastDay {
		last[i], assumed[i] = strconv.Itoa(v), strconv.Itoa(dcbrain.MonthDays(i+1))
	}
	p.line("largest day of each month in the error table: %s (the calendar the reader assumes: %s)", strings.Join(last, " "), strings.Join(assumed, " "))
	p.line("largest values: memoryid %d, rankid %d, bankid %d, row %d, col %d", d.MaxMemoryID, d.MaxRank, d.MaxBank, d.MaxRow, d.MaxCol)
	if d.Inventory.Name != "" {
		p.line("inventory lists %s servers; %s servers with errors are not in it", num(d.InventoryServers), num(d.NotInInventory))
	}
	var all, with int64
	for t := range d.TicketsByType {
		all += d.TicketsByType[t]
		with += d.TicketsWithRows[t]
	}
	p.line("tickets: %s (type 0: %s, type 1 uncorrectable error: %s, type 2 correctable errors: %s, type 3 miscellaneous: %s)",
		num(all), num(d.TicketsByType[0]), num(d.TicketsByType[1]), num(d.TicketsByType[2]), num(d.TicketsByType[3]))
	p.line("tickets of servers that have rows in the error table: %s (type 0: %s, type 1: %s, type 2: %s, type 3: %s)",
		num(with), num(d.TicketsWithRows[0]), num(d.TicketsWithRows[1]), num(d.TicketsWithRows[2]), num(d.TicketsWithRows[3]))
	if d.ExtraTickets > 0 {
		p.line("%s tickets are a second or later ticket of a server and are not used", num(d.ExtraTickets))
	}
	p.line("records after merging rows that are equal in every column: %s", num(r.Census.Records))
	for _, n := range r.Widened {
		p.line("server %s: %s rows merged in buckets of %d seconds into %s records", n.SID, num(n.Rows), n.BucketSeconds, num(int64(n.Records)))
	}
	for _, n := range r.Dropped {
		t := ""
		if n.Ticket != "" {
			t = "; " + n.Ticket
		}
		p.line("server %s: left out, %s rows are too many records even when merged by the day%s", n.SID, num(n.Rows), t)
	}
}

func (r *Report) printCensus(p *printer) {
	c := &r.Census
	p.section("Observation grade")
	p.table("", []string{"grade", "errors", "DIMMs"}, [][]string{
		{"A: bank, row and column", num(c.GradeErrors[0]), num(c.GradeDIMMs[0])},
		{"B: rank or address", num(c.GradeErrors[1]), num(c.GradeDIMMs[1])},
		{"C: count only", num(c.GradeErrors[2]), num(c.GradeDIMMs[2])},
	})
	p.line("errors without a DRAM device: %s of %s; without failing bits: %s", num(c.NoDevice), num(c.Errors), num(c.NoBits))

	p.section("Fault modes over all records")
	var rows [][]string
	for _, m := range c.Modes {
		if m.Faults == 0 && m.DIMMs == 0 {
			continue
		}
		rows = append(rows, []string{string(m.Mode), num(m.Faults), num(m.Estimated), num(m.Errors), pct(m.Errors, c.Errors), num(m.DIMMs), num(m.Widest)})
	}
	p.table("", []string{"mode", "faults", "of them estimated", "errors", "share of errors", "DIMMs by largest fault", "DIMMs by widest fault"}, rows)

	p.section("Findings over all records")
	rows = nil
	for i, g := range Grades {
		rows = append(rows, []string{gradeText(g), num(c.DIMMGrades[i]), num(c.ServerGrades[i])})
	}
	p.table("", []string{"finding", "DIMMs", "servers"}, rows)
	rows = nil
	for _, rule := range engine.Rules {
		rows = append(rows, []string{rule.ID, gradeText(rule.Grade), num(c.Rules[rule.ID])})
	}
	p.table("Rules:", []string{"rule", "grade", "DIMMs it fired on"}, rows)
	rows = nil
	for _, code := range sortedKeys(c.Codes) {
		rows = append(rows, []string{code, num(c.Codes[code])})
	}
	p.table("Statements of the evidence:", []string{"statement", "DIMMs"}, rows)
}

func (r *Report) printCutoffs(p *printer) {
	p.section(fmt.Sprintf("Population at each cut-off and tickets within %d days", r.HorizonDays))
	var rows [][]string
	for i := range r.ByCut {
		c := &r.ByCut[i]
		period := "development"
		if c.HeldOut {
			period = "held-out"
		}
		all := c.All()
		rows = append(rows, []string{strconv.Itoa(c.Day), period, num(c.Servers), num(c.FailedBefore), num(c.NotYet),
			num(all[FailedUE]), num(all[FailedCE]), num(all[FailedMisc]), num(all[FailedUnspecified]), num(c.Unseen.Total())})
	}
	p.table("Servers in the population and their tickets in the window:",
		[]string{"cut-off day", "period", "servers", "failed before", "first error later", "UE ticket", "CE ticket", "misc ticket", "type 0 ticket", "tickets without an earlier error"}, rows)

	head := []string{"cut-off day"}
	for _, g := range Grades {
		head = append(head, gradeText(g))
	}
	rows = nil
	for i := range r.ByCut {
		c := &r.ByCut[i]
		row := []string{strconv.Itoa(c.Day)}
		for g := range Grades {
			row = append(row, num(c.ByGrade[g].Total()))
		}
		rows = append(rows, row)
	}
	p.table("Servers by finding:", head, rows)
	rows = nil
	for i := range r.ByCut {
		c := &r.ByCut[i]
		row := []string{strconv.Itoa(c.Day)}
		for g := range Grades {
			row = append(row, num(c.DIMMs[g]))
		}
		rows = append(rows, row)
	}
	p.table("DIMMs by finding:", head, rows)

	for _, per := range r.Periods() {
		s := &per.Sum
		p.section(fmt.Sprintf("Findings and what followed, %s period (cut-off days %s)", per.Name, days(per.Days)))
		rows = nil
		for g, grade := range Grades {
			if s.ByGrade[g].Total() == 0 && grade != model.VerdictReplaceNow && grade != model.VerdictConsiderReplacement {
				continue
			}
			rows = append(rows, outcomeRow(gradeText(grade), s.ByGrade[g]))
		}
		rows = append(rows, outcomeRow("all", s.All()))
		rows = append(rows, outcomeRow(fmt.Sprintf("reference: error in the last %g days", r.RecentDays), s.Active))
		p.table("By finding for the server:", append([]string{"finding"}, outcomeHead...), rows)
		for _, g := range []model.VerdictGrade{model.VerdictReplaceNow, model.VerdictConsiderReplacement, model.VerdictHoldReplacement} {
			for _, ue := range []bool{false, true} {
				sc := s.ScoreOf(g, ue)
				what := "a ticket of type 1, 2 or 3"
				if ue {
					what = "a ticket for an uncorrectable error"
				}
				if sc.Flagged == 0 {
					if !ue {
						p.line("%s: no server", gradeText(g))
					}
					continue
				}
				p.line("%s, against %s: precision %s (%s of %s), recall %s (%s of %s)", gradeText(g), what,
					pct(sc.Hit, sc.Flagged), num(sc.Hit), num(sc.Flagged), pct(sc.Hit, sc.Failures), num(sc.Hit), num(sc.Failures))
			}
		}
		if n := s.Unseen.Total(); n > 0 {
			p.line("%s more tickets fall into these windows from servers without an error before the cut-off (uncorrectable error: %s); no finding could precede them",
				num(n), num(s.Unseen[FailedUE]))
		}

		rows = nil
		for _, rule := range engine.Rules {
			if c, ok := s.ByRule[rule.ID]; ok {
				rows = append(rows, outcomeRow(rule.ID, c))
			}
		}
		p.table("By rule that fired on at least one DIMM of the server:", append([]string{"rule"}, outcomeHead...), rows)
		rows = nil
		for _, code := range sortedKeys(s.ByCode) {
			rows = append(rows, outcomeRow(code, s.ByCode[code]))
		}
		p.table("By statement of the evidence:", append([]string{"statement"}, outcomeHead...), rows)
		rows = nil
		for _, m := range Modes {
			if c, ok := s.ByMode[m]; ok {
				rows = append(rows, outcomeRow(string(m), c))
			}
		}
		p.table("By widest fault mode on the server:", append([]string{"mode"}, outcomeHead...), rows)
	}
}

func (r *Report) printLeads(p *printer) {
	if len(r.Leads) == 0 {
		return
	}
	p.section("Finding for a failed server at times before its ticket")
	for _, out := range []Outcome{FailedUE, FailedCE, FailedMisc, FailedUnspecified} {
		if r.Leads[0].ByType[out].Tickets == 0 {
			continue
		}
		var rows [][]string
		for i := range r.Leads {
			lc := &r.Leads[i].ByType[out]
			row := []string{leadText(r.Leads[i].Lead), num(lc.Tickets), num(lc.NoErrors)}
			for g := range Grades {
				row = append(row, num(lc.ByGrade[g]))
			}
			flagged := lc.ByGrade[gradeIndex(model.VerdictConsiderReplacement)] + lc.ByGrade[gradeIndex(model.VerdictReplaceNow)]
			row = append(row, pct(flagged, lc.Tickets))
			rows = append(rows, row)
		}
		head := []string{"time before the ticket", "servers", "no error yet"}
		for _, g := range Grades {
			head = append(head, gradeText(g))
		}
		head = append(head, "consider or replace now")
		p.table(fmt.Sprintf("Servers with a ticket of failure type %d (%s):", failureType(out), out), head, rows)
	}
}

// failureType returns the failure_type value of the dataset an outcome
// stands for.
func failureType(o Outcome) int {
	switch o {
	case FailedUE:
		return int(dcbrain.FailureUE)
	case FailedCE:
		return int(dcbrain.FailureCE)
	case FailedMisc:
		return int(dcbrain.FailureMisc)
	}
	return int(dcbrain.FailureUnspecified)
}
