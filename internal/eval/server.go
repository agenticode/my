package eval

import (
	"bufio"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agenticode/my/internal/adapter/dcbrain"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/model"
)

// view is what the engine found for one server as of one time.
type view struct {
	// grade is the finding for the server, as an index into Grades.
	grade int
	// dimms counts the DIMMs with errors by their finding.
	dimms [numGrades]int
	// rules and codes are the rules that fired on at least one DIMM and
	// the statements of their evidence.
	rules []string
	codes []string
	// widest is the widest fault mode on the server.
	widest model.FaultMode
	// act says how active the server was up to the time of the view.
	act activity
}

// activity is what the rows of a server say by themselves at one time,
// without the engine. The detail output carries it next to the finding,
// to see which servers a threshold separates.
type activity struct {
	// errors is the number of errors up to the time, recent the number
	// among them in the recent window of the trend.
	errors, recent int64
	// lastError is the age of the latest error in seconds.
	lastError int64
	// lastBurst is the age in seconds of the latest moment at which one
	// DIMM had a storm's worth of errors within the storm window, or -1
	// when there was none.
	lastBurst int64
}

// history holds running sums over the records of one server, for activity.
type history struct {
	at []dcbrain.Stamp
	// sum[i] is the number of errors in the first i records.
	sum []int64
	// bursts are the times, in order, at which some DIMM reached the storm
	// count within the storm window.
	bursts []dcbrain.Stamp
}

// newHistory prepares the sums over recs, which are in time order.
func (r *runner) newHistory(recs []dcbrain.Rec) *history {
	h := &history{at: make([]dcbrain.Stamp, len(recs)), sum: make([]int64, len(recs)+1)}
	window := dcbrain.Stamp(r.cfg.StormMinutes * 60)
	type run struct {
		idx []int32
		lo  int
		n   int64
	}
	var byDIMM [256]*run
	for i := range recs {
		rec := &recs[i]
		h.at[i] = rec.Stamp()
		h.sum[i+1] = h.sum[i] + int64(rec.Count)
		d := byDIMM[rec.MemoryID]
		if d == nil {
			d = &run{}
			byDIMM[rec.MemoryID] = d
		}
		d.idx = append(d.idx, int32(i))
		d.n += int64(rec.Count)
		for h.at[i]-h.at[d.idx[d.lo]] > window {
			d.n -= int64(recs[d.idx[d.lo]].Count)
			d.lo++
		}
		if d.n >= int64(r.cfg.StormCount) && (len(h.bursts) == 0 || h.bursts[len(h.bursts)-1] != h.at[i]) {
			h.bursts = append(h.bursts, h.at[i])
		}
	}
	return h
}

// activity returns what the rows say up to the given time.
func (h *history) activity(at, recent dcbrain.Stamp) activity {
	j := sort.Search(len(h.at), func(k int) bool { return h.at[k] > at })
	k := sort.Search(len(h.at), func(k int) bool { return h.at[k] > at-recent })
	a := activity{errors: h.sum[j], recent: h.sum[j] - h.sum[k], lastError: -1, lastBurst: -1}
	if j > 0 {
		a.lastError = int64(at - h.at[j-1])
	}
	if b := sort.Search(len(h.bursts), func(k int) bool { return h.bursts[k] > at }); b > 0 {
		a.lastBurst = int64(at - h.bursts[b-1])
	}
	return a
}

// Cut-off states of a server.
const (
	cutNotYet = iota // no error up to the cut-off
	cutFailed        // the ticket lies at or before the cut-off
	cutIn            // in the population
)

type cutObs struct {
	state   int
	view    view
	outcome Outcome
	active  bool
}

type leadObs struct {
	seen bool
	view view
}

// serverResult is what one server adds to the report.
type serverResult struct {
	note    ServerNote
	dropped bool
	dimms   int
	// memory has a bit set for every memoryid with errors.
	memory  uint32
	records int
	errors  int64
	ticket  *dcbrain.Ticket
	cuts    []cutObs
	leads   []leadObs
	census  *engine.HostResult
	// censusAct is the activity at the end of the dataset.
	censusAct activity
}

func gradeIndex(g model.VerdictGrade) int { return g.ExitCode() }

// degradeC turns the records of a server into one count per DIMM and day,
// read at the end of the day: what a daily reading of an error counter
// gives.
func (r *runner) degradeC(in *engine.Input, sid string, recs []dcbrain.Rec, slot func(uint8) (string, string)) {
	type key struct {
		memory uint8
		day    uint32
	}
	sums := map[key]int{}
	var order []key
	for i := range recs {
		k := key{recs[i].MemoryID, recs[i].At / 86400}
		if _, ok := sums[k]; !ok {
			order = append(order, k)
		}
		sums[k] += int(recs[i].Count)
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].day != order[j].day {
			return order[i].day < order[j].day
		}
		return order[i].memory < order[j].memory
	})
	for i, k := range order {
		name, uid := slot(k.memory)
		start := dcbrain.StampOfDay(int(k.day))
		e := model.Event{
			ID: int64(i + 1), Host: sid, Kind: model.KindCounter,
			ErrorDT: (start + 86400).Time(r.base), WindowStart: start.Time(r.base),
			ErrorSeverity: model.SevCE, Source: dcbrain.Source, Count: sums[k], TimeQuality: model.TimeExact,
			Label: name, Slot: name, DIMMUID: uid, DIMMLink: model.LinkSlot, Grade: model.GradeC,
		}
		in.Add(&e)
	}
}

// input builds what the engine reads for one server. It mirrors what an
// import of the server through the ledger gives: a slot per memoryid that
// has errors, with its place on the board, and a DIMM identity that
// stands for the slot because the dataset has no serial numbers.
func (r *runner) input(sid string, recs []dcbrain.Rec) *engine.Input {
	in := engine.NewInput()
	in.Hosts = []model.Host{{Name: sid}}
	var names, uids [256]string
	slot := func(memory uint8) (string, string) {
		if names[memory] == "" {
			name := dcbrain.SlotName(int(memory))
			names[memory], uids[memory] = name, model.SlotUID(sid, name)
			socket, channel, position := dcbrain.Topology(int(memory))
			in.Slots = append(in.Slots, model.Slot{Host: sid, Name: name, EdacLabel: name,
				Socket: model.Some(socket), Channel: model.Some(channel), Position: model.Some(position)})
			in.DIMMs = append(in.DIMMs, model.DIMM{UID: uids[memory], Identity: model.IdentitySlot})
		}
		return names[memory], uids[memory]
	}
	if r.opts.Degrade == model.GradeC {
		r.degradeC(in, sid, recs, slot)
		return in
	}
	var e model.Event
	for i := range recs {
		rec := &recs[i]
		dcbrain.Fill(&e, sid, rec, r.base)
		e.ID = int64(rec.Line)
		e.Slot, e.DIMMUID = slot(rec.MemoryID)
		e.DIMMLink = model.LinkSlot
		if r.opts.Degrade == model.GradeB {
			e.Bank, e.Row, e.Column = model.Opt[int]{}, model.Opt[int]{}, model.Opt[int]{}
			e.Grade = e.ComputeGrade()
		}
		in.Add(&e)
	}
	return in
}

// analyze runs the engine over the server as of the given time.
func (r *runner) analyze(in *engine.Input, sid string, at dcbrain.Stamp) (*engine.HostResult, error) {
	t := at.Time(r.base)
	opts := engine.Options{Config: &r.cfg, AsOf: t, Now: t}
	if !r.opts.FromFirstError {
		opts.ObservedFrom = r.base
	}
	res, err := engine.Analyze(in, opts)
	if err != nil {
		return nil, err
	}
	h := res.Host(sid)
	if h == nil {
		return nil, fmt.Errorf("the engine returned no result for the host")
	}
	return h, nil
}

func viewOf(h *engine.HostResult) view {
	v := view{grade: gradeIndex(h.Grade), widest: model.ModeUndetermined}
	rules, codes := map[string]bool{}, map[string]bool{}
	widest := -1
	for _, d := range h.DIMMs {
		if d.Features.CE+d.Features.UE == 0 {
			continue
		}
		v.dimms[gradeIndex(d.Verdict.Grade)]++
		for _, id := range d.Verdict.Rules {
			rules[id] = true
		}
		for _, ev := range d.Verdict.Evidence {
			codes[ev.Code] = true
		}
		for _, f := range d.Faults {
			if i := modeIndex(f.Mode); i > widest {
				widest, v.widest = i, f.Mode
			}
		}
	}
	for id := range rules {
		v.rules = append(v.rules, id)
	}
	for c := range codes {
		v.codes = append(v.codes, c)
	}
	sort.Strings(v.rules)
	sort.Strings(v.codes)
	return v
}

// server replays one server: the findings at every cut-off, the findings
// before its failure, and the census over all its records.
func (r *runner) server(sid string, sr *serverRecs) (*serverResult, error) {
	res := &serverResult{note: ServerNote{SID: sid, Rows: sr.rows, Records: len(sr.recs), BucketSeconds: int(sr.bucket)}}
	if t, ok := r.tickets[sid]; ok {
		res.ticket = &t
		res.note.Ticket = fmt.Sprintf("failure_type %d at %s", int(t.Type), t.At)
	}
	if sr.dropped {
		res.dropped = true
		return res, nil
	}
	recs := sr.recs
	if len(recs) == 0 {
		return res, nil
	}
	first := recs[0].Stamp()
	seenDIMM := [256]bool{}
	for i := range recs {
		if id := recs[i].MemoryID; !seenDIMM[id] {
			seenDIMM[id] = true
			res.dimms++
			if id < 32 {
				res.memory |= 1 << id
			}
		}
		res.errors += int64(recs[i].Count)
	}
	res.records = len(recs)
	in := r.input(sid, recs)
	hist := r.newHistory(recs)

	res.cuts = make([]cutObs, len(r.cutoffs))
	for i, c := range r.cutoffs {
		o := &res.cuts[i]
		switch {
		case res.ticket != nil && res.ticket.At <= c:
			o.state = cutFailed
			continue
		case first > c:
			o.state = cutNotYet
			continue
		}
		o.state = cutIn
		h, err := r.analyze(in, sid, c)
		if err != nil {
			return nil, err
		}
		o.view = viewOf(h)
		o.view.act = hist.activity(c, r.recent)
		if t := res.ticket; t != nil && t.At > c && t.At <= c+r.horizon {
			o.outcome = outcomeOf(t.Type)
		}
		// The reference: an error in the recent window before the cut-off.
		o.active = o.view.act.recent > 0
	}

	if t := res.ticket; t != nil {
		res.leads = make([]leadObs, len(r.opts.Leads))
		for i, l := range r.opts.Leads {
			at := t.At - dcbrain.Stamp(l/time.Second)
			if first > at {
				continue
			}
			h, err := r.analyze(in, sid, at)
			if err != nil {
				return nil, err
			}
			res.leads[i] = leadObs{seen: true, view: viewOf(h)}
			res.leads[i].view.act = hist.activity(at, r.recent)
		}
	}

	h, err := r.analyze(in, sid, r.end)
	if err != nil {
		return nil, err
	}
	res.census = h
	res.censusAct = hist.activity(r.end, r.recent)
	return res, nil
}

// detail is one line of the detail output: what the engine found for one
// server at one time.
type detail struct {
	sid string
	// kind is "cutoff", "lead" or "all"; at is the cut-off day, the lead
	// time in seconds, or the last day.
	kind string
	at   int64
	view view
	// seen is false when the server had no error yet.
	seen    bool
	outcome string
	errors  int64
	ticket  string
}

// DetailHeader names the columns of the detail output.
const DetailHeader = "sid,kind,at,finding,rules,statements,widest_mode,dimms_with_errors,outcome,errors_of_server,ticket," +
	"errors_so_far,errors_recent,last_error_days,last_burst_days"

func (r *runner) keepDetail(res *serverResult) {
	if r.opts.Detail == nil || res.dropped {
		return
	}
	for i := range res.cuts {
		o := &res.cuts[i]
		d := detail{sid: res.note.SID, kind: "cutoff", at: int64(r.opts.Cutoffs[i]), errors: res.errors, ticket: res.note.Ticket}
		switch o.state {
		case cutFailed:
			d.outcome = "failed before"
		case cutNotYet:
			d.outcome = "first error later"
		default:
			d.seen, d.view, d.outcome = true, o.view, o.outcome.String()
		}
		r.details = append(r.details, d)
	}
	for i := range res.leads {
		d := detail{sid: res.note.SID, kind: "lead", at: int64(r.opts.Leads[i] / time.Second), errors: res.errors, ticket: res.note.Ticket,
			seen: res.leads[i].seen, view: res.leads[i].view}
		if res.ticket != nil {
			d.outcome = outcomeOf(res.ticket.Type).String()
		}
		r.details = append(r.details, d)
	}
	if res.census != nil {
		v := viewOf(res.census)
		v.act = res.censusAct
		r.details = append(r.details, detail{sid: res.note.SID, kind: "all", at: dcbrain.SpanDays, errors: res.errors, ticket: res.note.Ticket,
			seen: true, view: v})
	}
}

// writeDetail writes the detail lines in a fixed order.
func (r *runner) writeDetail() error {
	w := r.opts.Detail
	if w == nil {
		return nil
	}
	rank := map[string]int{"cutoff": 0, "lead": 1, "all": 2}
	sort.SliceStable(r.details, func(i, j int) bool {
		a, b := &r.details[i], &r.details[j]
		if a.sid != b.sid {
			return a.sid < b.sid
		}
		if a.kind != b.kind {
			return rank[a.kind] < rank[b.kind]
		}
		return a.at < b.at
	})
	bw := bufio.NewWriter(w)
	bw.WriteString(DetailHeader + "\n")
	for i := range r.details {
		d := &r.details[i]
		finding, mode, dimms := "", "", 0
		if d.seen {
			finding, mode = string(Grades[d.view.grade]), string(d.view.widest)
			for _, n := range d.view.dimms {
				dimms += n
			}
		}
		// Ages are written in days; a column is empty when there is nothing
		// to say (no error yet, no burst so far).
		age := func(sec int64) string {
			if !d.seen || sec < 0 {
				return ""
			}
			return strconv.FormatFloat(float64(sec)/86400, 'f', 3, 64)
		}
		count := func(n int64) string {
			if !d.seen {
				return ""
			}
			return strconv.FormatInt(n, 10)
		}
		a := d.view.act
		fmt.Fprintf(bw, "%s,%s,%d,%s,%s,%s,%s,%d,%s,%d,%s,%s,%s,%s,%s\n", d.sid, d.kind, d.at, finding, strings.Join(d.view.rules, " "),
			strings.Join(d.view.codes, " "), mode, dimms, d.outcome, d.errors, d.ticket,
			count(a.errors), count(a.recent), age(a.lastError), age(a.lastBurst))
	}
	return bw.Flush()
}

// merge adds the result of one server to the report.
func (r *runner) merge(rep *Report, res *serverResult) {
	r.keepDetail(res)
	if res.dropped {
		rep.Dropped = append(rep.Dropped, res.note)
		return
	}
	if res.note.BucketSeconds > 1 {
		rep.Widened = append(rep.Widened, res.note)
	}
	rep.Dataset.DIMMs += int64(res.dimms)
	r.layout(&rep.Dataset.Layout, res)

	for i := range res.cuts {
		o, st := &res.cuts[i], &rep.ByCut[i]
		switch o.state {
		case cutFailed:
			st.FailedBefore++
			continue
		case cutNotYet:
			st.NotYet++
			if t := res.ticket; t != nil && r.window(t.At) == i {
				st.Unseen[outcomeOf(t.Type)]++
			}
			continue
		}
		st.Servers++
		st.ByGrade[o.view.grade][o.outcome]++
		for g, n := range o.view.dimms {
			st.DIMMs[g] += int64(n)
		}
		for _, id := range o.view.rules {
			c := st.ByRule[id]
			c[o.outcome]++
			st.ByRule[id] = c
		}
		for _, code := range o.view.codes {
			c := st.ByCode[code]
			c[o.outcome]++
			st.ByCode[code] = c
		}
		c := st.ByMode[o.view.widest]
		c[o.outcome]++
		st.ByMode[o.view.widest] = c
		if o.active {
			st.Active[o.outcome]++
		}
	}

	if t := res.ticket; t != nil {
		out := outcomeOf(t.Type)
		for i := range res.leads {
			lc := &rep.Leads[i].ByType[out]
			lc.Tickets++
			if !res.leads[i].seen {
				lc.NoErrors++
				continue
			}
			lc.ByGrade[res.leads[i].view.grade]++
		}
	}

	h := res.census
	if h == nil {
		return
	}
	c := &rep.Census
	c.Servers++
	c.Records += int64(res.records)
	c.ServerGrades[gradeIndex(h.Grade)]++
	for _, d := range h.DIMMs {
		ft := &d.Features
		n := ft.CE + ft.UE
		if n == 0 {
			continue
		}
		c.DIMMs++
		c.Errors += n
		c.GradeErrors[0] += ft.GradeA
		c.GradeErrors[1] += ft.GradeB
		c.GradeErrors[2] += ft.GradeC
		switch {
		case ft.GradeA > 0:
			c.GradeDIMMs[0]++
		case ft.GradeB > 0:
			c.GradeDIMMs[1]++
		default:
			c.GradeDIMMs[2]++
		}
		if !ft.Lines.Known {
			c.NoBits += n
		}
		c.DIMMGrades[gradeIndex(d.Verdict.Grade)]++
		for _, id := range d.Verdict.Rules {
			c.Rules[id]++
		}
		codes := map[string]bool{}
		for _, ev := range d.Verdict.Evidence {
			codes[ev.Code] = true
		}
		for code := range codes {
			c.Codes[code]++
		}
		widest, device := -1, false
		for i, f := range d.Faults {
			m := &c.Modes[modeIndex(f.Mode)]
			m.Faults++
			m.Errors += int64(f.ErrorCount)
			if f.Estimated {
				m.Estimated++
			}
			if i == 0 {
				m.DIMMs++
			}
			widest = max(widest, modeIndex(f.Mode))
			device = device || f.Shape.Devices > 0
		}
		if widest >= 0 {
			c.Modes[widest].Widest++
		}
		if !device {
			c.NoDevice += n
		}
	}
}

// layout adds the DIMMs with errors of one server to the layout counts.
func (r *runner) layout(l *Layout, res *serverResult) {
	var ids []int
	for id := 0; id < 32; id++ {
		if res.memory&(1<<id) != 0 {
			ids = append(ids, id)
		}
	}
	by := l.ByDIMMs[r.dimmCount[res.note.SID]]
	for _, id := range ids {
		_, _, position := dcbrain.Topology(id)
		by[position]++
	}
	l.ByDIMMs[r.dimmCount[res.note.SID]] = by
	if len(ids) < 2 {
		return
	}
	l.MultiDIMM++
	for i, a := range ids {
		for _, b := range ids[i+1:] {
			l.Pairs++
			sa, ca, _ := dcbrain.Topology(a)
			sb, cb, _ := dcbrain.Topology(b)
			switch {
			case sa == sb && ca == cb:
				l.SameChannel++
			case sa == sb:
				l.SameSocket++
			}
		}
	}
}
