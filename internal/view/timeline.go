package view

import (
	"context"
	"sort"
	"time"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// Timeline shows the errors of a DIMM, or of every DIMM of a host, day by
// day, with what happened to the host in between: boots, repairs, pages
// taken offline, slot changes and uncorrected errors. The counts come from
// the ledger, so they run across reboots.
type Timeline struct {
	Host string `json:"host"`
	// Slot and DIMM are set on the timeline of one DIMM.
	Slot string    `json:"slot,omitzero"`
	DIMM *DIMMInfo `json:"dimm,omitzero"`
	// From and To are the first and the last day drawn (UTC), Days the
	// number of days from one to the other.
	From time.Time `json:"from,omitzero"`
	To   time.Time `json:"to,omitzero"`
	Days int       `json:"days"`
	// Series holds one row of daily counts per DIMM.
	Series []Series `json:"series"`
	// Markers are the events of the period, grouped by kind.
	Markers []MarkerGroup `json:"markers"`
	// CE and UE are the errors within the period.
	CE int64 `json:"ce"`
	UE int64 `json:"ue"`
	// SinceBoot is set when the host booted within the period.
	SinceBoot *SinceBoot `json:"since_boot,omitzero"`
	// Undated counts errors known by seconds since boot only. They cannot
	// be placed on the time axis.
	Undated int64 `json:"undated,omitzero"`
	// Notes are the sentences below the figure.
	Notes []Text `json:"notes"`
}

// Series is the daily corrected error count of one DIMM.
type Series struct {
	// Label is the slot of the DIMM, or what the row stands for.
	Label  string `json:"label"`
	Serial string `json:"serial,omitzero"`
	// CE holds the corrected errors per day, From first.
	CE    []int64 `json:"ce"`
	Total int64   `json:"total"`
	// Peak is the largest count of one day.
	Peak int64 `json:"peak"`
}

// SinceBoot counts the errors since the latest boot: what a counter that
// restarts at every boot can show.
type SinceBoot struct {
	At time.Time `json:"at"`
	CE int64     `json:"ce"`
	UE int64     `json:"ue"`
}

// Marker kinds beside the action kinds of the ledger.
const (
	MarkBoot = "boot"
	MarkUE   = "ue"
	MarkMove = "move"
)

// MarkerLetters gives every marker kind the letter it is drawn with.
var MarkerLetters = map[string]string{
	MarkBoot:                        "B",
	MarkUE:                          "U",
	MarkMove:                        "M",
	string(model.ActionPageOffline): "O",
	string(model.ActionSPPR):        "P",
	string(model.ActionHPPR):        "P",
	string(model.ActionSparing):     "S",
	string(model.ActionRowRemap):    "R",
	string(model.ActionReseat):      "T",
	string(model.ActionReplacement): "X",
	string(model.ActionDisable):     "D",
	string(model.ActionMarkFailed):  "F",
}

// MarkerGroup is the events of one kind within the period.
type MarkerGroup struct {
	Kind   string `json:"kind"`
	Letter string `json:"letter"`
	// Days are the days the events fall on, as offsets from Timeline.From,
	// in order and without repeats.
	Days []int `json:"days"`
	// Count is the number of events: boots, errors, pages, repairs.
	Count int `json:"count"`
	// Text describes the group in one line.
	Text Text `json:"text"`
}

// MaxSeries is the number of DIMMs a host timeline draws a row for. The
// DIMMs beyond that share one row.
const MaxSeries = 8

// TimelineOptions select the period.
type TimelineOptions struct {
	// Days limits the timeline to the last Days days; 0 draws everything
	// the ledger knows about the host.
	Days int
}

func dayOf(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// Timeline builds the timeline of a host, or of one of its DIMMs when d is
// not nil.
func (c *Case) Timeline(ctx context.Context, h *engine.HostResult, d *engine.DIMMResult, opts TimelineOptions) (*Timeline, error) {
	tl := &Timeline{Host: h.Host, Series: []Series{}, Markers: []MarkerGroup{}, Notes: []Text{}}
	if d != nil {
		info := dimmInfo(d.UID, d.DIMM)
		tl.Slot, tl.DIMM, tl.Undated = d.Slot, &info, d.Features.Undated
	} else {
		for _, x := range h.DIMMs {
			tl.Undated += x.Features.Undated
		}
	}
	undated := func() {
		if tl.Undated > 0 {
			tl.Notes = append(tl.Notes, c.say("tl.undated", "n", tl.Undated))
		}
	}
	if h.AsOf.IsZero() {
		undated()
		return tl, nil
	}
	tl.To = lastDay(h.AsOf)
	tl.From = dayOf(h.ObservedFrom)
	if tl.From.After(tl.To) {
		tl.From = tl.To
	}
	if opts.Days > 0 {
		if from := tl.To.AddDate(0, 0, -(opts.Days - 1)); from.After(tl.From) {
			tl.From = from
		}
	}
	tl.Days = int(tl.To.Sub(tl.From).Hours()/24) + 1
	end := tl.To.AddDate(0, 0, 1)
	index := func(t time.Time) (int, bool) {
		if t.Before(tl.From) || !t.Before(end) {
			return 0, false
		}
		return int(t.Sub(tl.From).Hours() / 24), true
	}

	// Daily counts.
	filter := store.EventFilter{Host: h.Host, From: tl.From, To: end}
	series := func(label, serial string, f store.EventFilter) (Series, int64, error) {
		s := Series{Label: label, Serial: serial, CE: make([]int64, tl.Days)}
		days, err := c.store.Daily(ctx, f, 0)
		if err != nil {
			return s, 0, err
		}
		var ue int64
		for _, dc := range days {
			t, err := time.Parse("2006-01-02", dc.Day)
			if err != nil {
				continue
			}
			if i, ok := index(t); ok {
				s.CE[i] += dc.CE
				s.Total += dc.CE
				s.Peak = max(s.Peak, s.CE[i])
				ue += dc.UE
			}
		}
		return s, ue, nil
	}
	if d != nil {
		filter = store.EventFilter{DIMMUID: d.UID, From: tl.From, To: end}
		serial := ""
		if d.DIMM != nil {
			serial = d.DIMM.Serial
		}
		s, ue, err := series(d.Slot, serial, filter)
		if err != nil {
			return nil, err
		}
		tl.Series = append(tl.Series, s)
		tl.CE, tl.UE = s.Total, ue
	} else {
		totals, err := c.store.Totals(ctx, filter)
		if err != nil {
			return nil, err
		}
		type subject struct {
			uid, label string
			n          int64
		}
		by := map[string]*subject{}
		var subjects []*subject
		for _, t := range totals {
			s := by[t.DIMMUID]
			if s == nil {
				s = &subject{uid: t.DIMMUID, label: t.Slot}
				if x := h.DIMM(t.DIMMUID); x != nil && x.Slot != "" {
					s.label = x.Slot
				}
				by[t.DIMMUID] = s
				subjects = append(subjects, s)
			}
			s.n += t.CE + t.UE
		}
		sort.SliceStable(subjects, func(i, j int) bool {
			if subjects[i].n != subjects[j].n {
				return subjects[i].n > subjects[j].n
			}
			return naturalLess(subjects[i].label, subjects[j].label)
		})
		var rest *Series
		folded := 0
		for i, sub := range subjects {
			f := filter
			f.DIMMUID = sub.uid
			label, serial := sub.label, ""
			if sub.uid == "" {
				// Errors tied to no DIMM have a row of their own.
				label = c.say("tl.unattributed").Text
				f = filter
			} else if x := h.DIMM(sub.uid); x != nil && x.DIMM != nil {
				serial = x.DIMM.Serial
			}
			var s Series
			var ue int64
			if sub.uid == "" {
				s, ue, err = c.looseSeries(ctx, tl, filter, label)
			} else {
				s, ue, err = series(label, serial, f)
			}
			if err != nil {
				return nil, err
			}
			tl.CE += s.Total
			tl.UE += ue
			if i < MaxSeries || len(subjects) == MaxSeries+1 {
				tl.Series = append(tl.Series, s)
				continue
			}
			if rest == nil {
				rest = &Series{CE: make([]int64, tl.Days)}
			}
			folded++
			for k, n := range s.CE {
				rest.CE[k] += n
				rest.Peak = max(rest.Peak, rest.CE[k])
			}
			rest.Total += s.Total
		}
		if rest != nil {
			rest.Label = c.say("tl.others", "n", folded).Text
			tl.Series = append(tl.Series, *rest)
		}
	}

	// Markers.
	type mark struct {
		kind string
		day  int
		at   time.Time
		n    int
		text Text
	}
	var marks []mark
	add := func(kind string, at time.Time, n int, text Text) {
		if i, ok := index(at); ok {
			marks = append(marks, mark{kind, i, at, n, text})
		}
	}
	when := func(t time.Time) string { return t.UTC().Format("2006-01-02") }
	slotOf := func(slot string, t Text) Text {
		// On the timeline of a host a marker says which slot it is about.
		if d == nil && slot != "" {
			t.Text = slot + ": " + t.Text
			if t.Args == nil {
				t.Args = map[string]any{}
			}
			t.Args["slot"] = slot
		}
		return t
	}

	boots, err := c.store.Boots(ctx, h.Host)
	if err != nil {
		return nil, err
	}
	var latest time.Time
	for _, b := range boots {
		if b.StartQuality == model.TimeRelative || b.StartedAt.After(h.AsOf) {
			continue
		}
		if b.StartedAt.After(latest) {
			latest = b.StartedAt
		}
		// The boot that was running when the period began is not a reboot
		// within it.
		if b.StartedAt.After(h.ObservedFrom) {
			add(MarkBoot, b.StartedAt, 1, c.say("tl.boot.one", "when", when(b.StartedAt)))
		}
	}

	ueFilter := filter
	ueFilter.Severities = []model.Severity{model.SevRecoverable, model.SevUE, model.SevFatal}
	for e, err := range c.store.Events(ctx, ueFilter) {
		if err != nil {
			return nil, err
		}
		n := e.Errors()
		if n == 0 {
			continue
		}
		code := "tl.ue.one"
		args := []any{"when", when(e.ErrorDT)}
		if e.ErrorScrubType != "" {
			code = "tl.ue.one_how"
			args = append(args, "how", Messages.Text(c.Lang, "how."+string(e.ErrorScrubType), nil))
		}
		add(MarkUE, e.ErrorDT, n, slotOf(e.Slot, c.say(code, args...)))
	}

	af := store.ActionFilter{Host: h.Host, From: tl.From, To: end}
	if d != nil {
		af = store.ActionFilter{DIMMUID: d.UID, From: tl.From, To: end}
	}
	actions, err := c.store.Actions(ctx, af)
	if err != nil {
		return nil, err
	}
	// One page offline that two sources report (the kernel log and the
	// rasdaemon database) is one event: the same page, the same outcome,
	// within two minutes. The same page taken offline again later, after a
	// reboot for example, is another event.
	type pageKey struct {
		page   uint64
		failed bool
	}
	lastOffline := map[pageKey]time.Time{}
	for _, a := range actions {
		if a.TimeQuality == model.TimeRelative {
			continue
		}
		if a.Kind == model.ActionPageOffline && a.OsPhysicalAddress.Valid {
			k := pageKey{a.OsPhysicalAddress.V >> 12, a.Result == model.ResultFailed}
			last, seen := lastOffline[k]
			lastOffline[k] = a.At
			if seen && a.At.Sub(last).Abs() <= 2*time.Minute {
				continue
			}
		}
		n := max(a.Count, 1)
		kind := c.kindText(string(a.Kind))
		var t Text
		switch {
		case a.Kind == model.ActionPageOffline && a.Result == model.ResultFailed:
			t = c.say("tl.offline.failed", "when", when(a.At), "n", n)
		case a.Kind == model.ActionPageOffline:
			t = c.say("tl.offline.one", "when", when(a.At), "n", n)
		case a.Result == model.ResultFailed:
			t = c.say("tl.action.failed", "when", when(a.At), "kind", kind)
		case a.Result == model.ResultPending:
			t = c.say("tl.action.pending", "when", when(a.At), "kind", kind)
		default:
			t = c.say("tl.action.one", "when", when(a.At), "kind", kind)
		}
		add(string(a.Kind), a.At, n, slotOf(a.Slot, t))
	}

	uid := ""
	if d != nil {
		uid = d.UID
	}
	places, err := c.store.Placements(ctx, "", uid)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(places, func(i, j int) bool {
		if places[i].DIMMUID != places[j].DIMMUID {
			return places[i].DIMMUID < places[j].DIMMUID
		}
		return places[i].FirstSeen.Before(places[j].FirstSeen)
	})
	for i := 1; i < len(places); i++ {
		p, q := places[i-1], places[i]
		if p.DIMMUID != q.DIMMUID || (p.Host == q.Host && p.Slot == q.Slot) {
			continue
		}
		if q.Host != h.Host && p.Host != h.Host {
			continue
		}
		from, to := p.Slot, q.Slot
		if p.Host != q.Host {
			from, to = p.Host+" "+p.Slot, q.Host+" "+q.Slot
		}
		add(MarkMove, q.FirstSeen, 1, c.say("tl.move.one", "when", when(q.FirstSeen), "from", from, "to", to))
	}

	// One group per kind. A group of one keeps the words of its event.
	sort.SliceStable(marks, func(i, j int) bool { return marks[i].at.Before(marks[j].at) })
	groups := map[string]*MarkerGroup{}
	first, last := map[string]time.Time{}, map[string]time.Time{}
	events := map[string]int{}
	var kinds []string
	for _, mk := range marks {
		g := groups[mk.kind]
		if g == nil {
			g = &MarkerGroup{Kind: mk.kind, Letter: MarkerLetters[mk.kind], Text: mk.text}
			if g.Letter == "" {
				g.Letter = "?"
			}
			groups[mk.kind] = g
			kinds = append(kinds, mk.kind)
			first[mk.kind] = mk.at
		}
		last[mk.kind] = mk.at
		events[mk.kind]++
		g.Count += mk.n
		if n := len(g.Days); n == 0 || g.Days[n-1] != mk.day {
			g.Days = append(g.Days, mk.day)
		}
	}
	for _, kind := range kinds {
		g := groups[kind]
		if events[kind] > 1 {
			a, b := when(first[kind]), when(last[kind])
			switch kind {
			case MarkBoot:
				g.Text = c.say("tl.boot.many", "n", g.Count)
			case MarkUE:
				g.Text = c.say("tl.ue.many", "n", g.Count, "first", a, "last", b)
			case MarkMove:
				g.Text = c.say("tl.move.many", "n", g.Count, "first", a, "last", b)
			case string(model.ActionPageOffline):
				g.Text = c.say("tl.offline.many", "n", g.Count, "days", len(g.Days), "first", a, "last", b)
			default:
				g.Text = c.say("tl.action.many", "n", g.Count, "kind", c.kindText(kind), "first", a, "last", b)
			}
		}
		tl.Markers = append(tl.Markers, *g)
	}

	// What the period adds up to.
	tl.Notes = append(tl.Notes, c.say("tl.total", "ce", tl.CE, "ue", tl.UE, "from", when(tl.From), "to", when(tl.To)))
	if !latest.IsZero() && latest.After(h.ObservedFrom) && tl.CE+tl.UE > 0 {
		sf := filter
		sf.From = latest
		totals, err := c.store.Totals(ctx, sf)
		if err != nil {
			return nil, err
		}
		sb := &SinceBoot{At: latest}
		for _, t := range totals {
			sb.CE += t.CE
			sb.UE += t.UE
		}
		tl.SinceBoot = sb
		tl.Notes = append(tl.Notes, c.say("tl.since_boot", "n", sb.CE+sb.UE, "when", when(latest)))
	}
	undated()
	return tl, nil
}

// kindText names an action kind on the timeline. What firmware did to a
// module is said in full; the repair kinds keep the names of the findings.
func (c *Case) kindText(kind string) string {
	if code := "tl.kind." + kind; Messages.Has(code) {
		return Messages.Text(c.Lang, code, nil)
	}
	return engine.Messages.Text(c.Lang, "kind."+kind, nil)
}

// looseSeries builds the row of the errors that are tied to no DIMM.
func (c *Case) looseSeries(ctx context.Context, tl *Timeline, f store.EventFilter, label string) (Series, int64, error) {
	s := Series{Label: label, CE: make([]int64, tl.Days)}
	var ue int64
	for e, err := range c.store.Events(ctx, f) {
		if err != nil {
			return s, 0, err
		}
		n := int64(e.Errors())
		if n == 0 || e.DIMMUID != "" {
			continue
		}
		if e.ErrorSeverity.Uncorrected() {
			ue += n
			continue
		}
		i := int(e.ErrorDT.Sub(tl.From).Hours() / 24)
		if i < 0 || i >= tl.Days {
			continue
		}
		s.CE[i] += n
		s.Total += n
		s.Peak = max(s.Peak, s.CE[i])
	}
	return s, ue, nil
}
