package view

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// Overview is the first screen of a host: where its DIMMs sit and which of
// them to look at first.
type Overview struct {
	Host    Header  `json:"host"`
	SlotMap SlotMap `json:"slot_map"`
	// Ranking lists the DIMMs that have errors or a finding, the worst
	// first.
	Ranking []Rank `json:"ranking"`
	// Quiet is the number of DIMMs of the host that are not in the ranking:
	// nothing is on record about them.
	Quiet int `json:"quiet"`
	// GPUs lists the findings about GPU memory.
	GPUs []GPURow `json:"gpus,omitzero"`
	// Unattributed counts the errors of the host that are tied to no DIMM,
	// and UnattributedUE those of them that were not corrected. No finding
	// covers them: the grade of the host speaks of its DIMMs and GPUs.
	Unattributed   int64 `json:"unattributed,omitzero"`
	UnattributedUE int64 `json:"unattributed_ue,omitzero"`
	// LoosePages counts the pages taken offline that are tied to no DIMM,
	// as the timeline counts them.
	LoosePages int `json:"loose_pages,omitzero"`
	// Sources is the source inventory of the latest scan of the host.
	Sources []Source `json:"sources"`
	// Missing names the sources that could not be read, or only in part.
	Missing []Text `json:"missing,omitzero"`
	// Root says what a scan as root would add, when the scan ran without
	// root privileges and that kept a source from being read.
	Root *Text `json:"root,omitzero"`
}

// GradeWithLoose names the grade of a host in a line of text. The grade is
// that of the worst DIMM or GPU. Where errors of the host are tied to no
// DIMM (ce corrected, ue uncorrected), the text says that no finding
// covers them: the grade alone would be read as "no errors".
func GradeWithLoose(lang i18n.Lang, grade string, ce, ue int64) string {
	if ce+ue == 0 {
		return grade
	}
	return Messages.Text(lang, "ov.grade_loose", map[string]any{"grade": grade, "ce": ce, "ue": ue})
}

// Loose counts the corrected and the uncorrected errors of a host that
// are tied to no DIMM.
func Loose(h *engine.HostResult) (ce, ue int64) {
	for _, u := range h.Unattributed {
		ce, ue = ce+u.CE, ue+u.UE
	}
	return ce, ue
}

// Header describes a host and how much the ledger knows about it.
type Header struct {
	Name string `json:"name"`
	// System is the vendor and product name of the machine.
	System string `json:"system,omitzero"`
	// From and To bound the time the ledger knows something about the host.
	From time.Time `json:"from,omitzero"`
	To   time.Time `json:"to,omitzero"`
	CE   int64     `json:"ce"`
	UE   int64     `json:"ue"`
	// Records is the number of ledger records behind the counts.
	Records int64 `json:"records"`
	// GradeA, GradeB and GradeC split the errors by observation grade.
	GradeA int64 `json:"grade_a"`
	GradeB int64 `json:"grade_b"`
	GradeC int64 `json:"grade_c"`
	// Grade is the finding for the host: the grade of its worst DIMM.
	Grade     model.VerdictGrade `json:"grade"`
	GradeText string             `json:"grade_text"`
	Boots     int                `json:"boots"`
	// Scan is the latest collection run of the host.
	Scan *ScanInfo `json:"scan,omitzero"`
}

// ScanInfo says how the records of a host were collected.
type ScanInfo struct {
	// Kind is "live", "import" or "synthetic".
	Kind        string    `json:"kind"`
	At          time.Time `json:"at"`
	Privileged  bool      `json:"privileged"`
	ToolVersion string    `json:"tool_version,omitzero"`
	Note        string    `json:"note,omitzero"`
}

// SlotMap lays the slots of a board out by socket and channel.
type SlotMap struct {
	// Rows holds one row per socket, in board order. Slots whose socket
	// the ledger does not know are grouped by what their names share.
	Rows      []SlotRow `json:"rows"`
	Slots     int       `json:"slots"`
	Populated int       `json:"populated"`
}

// SlotRow is the slots of one socket.
type SlotRow struct {
	Socket model.Opt[int] `json:"socket,omitzero"`
	// Label is what the names of the slots of the row have in common, for
	// example "CPU0". Label and the label of a cell together give the slot
	// name. It is empty when the names share nothing.
	Label    string        `json:"label,omitzero"`
	Channels []SlotChannel `json:"channels"`
}

// SlotChannel is the slots of one memory channel, by position.
type SlotChannel struct {
	IMC     model.Opt[int] `json:"imc,omitzero"`
	Channel model.Opt[int] `json:"channel,omitzero"`
	Slots   []SlotCell     `json:"slots"`
}

// Slot states of the slot map, beside the grades of a finding.
const (
	// SlotEmpty: no module in the slot.
	SlotEmpty = "empty"
	// SlotUnknown: the ledger does not know whether the slot holds a module.
	SlotUnknown = "unknown"
)

// SlotCell is one slot.
type SlotCell struct {
	Slot string `json:"slot"`
	// Label is the slot name without the label of its row.
	Label    string         `json:"label"`
	Position model.Opt[int] `json:"position,omitzero"`
	// State is the grade of the finding for the DIMM in the slot ("ok",
	// "observe", "consider-replacement", "replace-now",
	// "hold-replacement"), or "empty", or "unknown".
	State  string `json:"state"`
	Serial string `json:"serial,omitzero"`
	CE     int64  `json:"ce,omitzero"`
	UE     int64  `json:"ue,omitzero"`
}

// TrendDays is the span of the trend column of the ranking, and
// TrendBuckets the number of steps it is drawn in.
const (
	TrendDays    = 30
	TrendBuckets = 10
)

// Rank is one row of the ranking.
type Rank struct {
	N    int      `json:"n"`
	Slot string   `json:"slot"`
	DIMM DIMMInfo `json:"dimm"`
	// Present is false for a DIMM that is no longer in the host.
	Present bool  `json:"present"`
	CE      int64 `json:"ce"`
	UE      int64 `json:"ue"`
	// Mode is the mode of the fault with the most errors, or empty when the
	// DIMM has no fault. Estimated is set when the data does not fix it.
	Mode      model.FaultMode `json:"mode,omitzero"`
	ModeText  string          `json:"mode_text,omitzero"`
	Estimated bool            `json:"estimated,omitzero"`
	Faults    int             `json:"faults"`
	// Trend holds the errors of the last TrendDays days in TrendBuckets
	// steps, oldest first, and Direction what the engine makes of them:
	// "rising", "new", "steady", "falling", "quiet" or "unknown".
	Trend     []int64 `json:"trend"`
	Direction string  `json:"direction"`

	Grade      model.VerdictGrade `json:"grade"`
	GradeText  string             `json:"grade_text"`
	Rules      []string           `json:"rules"`
	Confidence int                `json:"confidence"`
}

// GPURow is the finding about the memory of one GPU.
type GPURow struct {
	GPU       string             `json:"gpu"`
	Index     model.Opt[int]     `json:"index,omitzero"`
	Grade     model.VerdictGrade `json:"grade"`
	GradeText string             `json:"grade_text"`
	Rules     []string           `json:"rules"`
	Evidence  []Text             `json:"evidence,omitzero"`
}

// Source is one entry of the source inventory.
type Source struct {
	Kind string `json:"kind"`
	// Name is what the source is called in a line of text: its kind, and
	// where a scan read several sources of one kind, what tells them apart
	// ("klog (journal)", "klog (ring buffer)").
	Name   string             `json:"name"`
	Origin string             `json:"origin"`
	Status model.SourceStatus `json:"status"`
	// StatusText is the status in the language of the view.
	StatusText string `json:"status_text"`
	Detail     string `json:"detail,omitzero"`
	Records    int    `json:"records"`
	Events     int    `json:"events"`
	Actions    int    `json:"actions"`
	Skipped    int    `json:"skipped"`
	// Grade is the best observation grade among the events of the source.
	Grade model.Grade `json:"grade,omitzero"`
	First time.Time   `json:"first,omitzero"`
	Last  time.Time   `json:"last,omitzero"`
	Notes []string    `json:"notes,omitzero"`
}

// latestScan returns the latest collection run of a host and its source
// inventory.
func (c *Case) latestScan(ctx context.Context, host string) (*model.Scan, []model.Source, error) {
	scans, err := c.store.Scans(ctx)
	if err != nil {
		return nil, nil, err
	}
	var latest *model.Scan
	for i := range scans {
		sc := &scans[i]
		if sc.Host != host {
			continue
		}
		if latest == nil || sc.StartedAt.After(latest.StartedAt) || (sc.StartedAt.Equal(latest.StartedAt) && sc.ID > latest.ID) {
			latest = sc
		}
	}
	if latest == nil {
		return nil, nil, nil
	}
	srcs, err := c.store.Sources(ctx, latest.ID)
	return latest, srcs, err
}

// header builds the header of a host.
func (c *Case) header(ctx context.Context, h *engine.HostResult) (Header, *model.Scan, []model.Source, error) {
	hd := Header{Name: h.Host, From: h.ObservedFrom, To: h.AsOf, Grade: h.Grade, GradeText: engine.GradeText(c.Lang, h.Grade)}
	hosts, err := c.store.Hosts(ctx)
	if err != nil {
		return hd, nil, nil, err
	}
	for _, x := range hosts {
		if x.Name == h.Host {
			hd.System = strings.TrimSpace(x.SystemVendor + " " + x.SystemProduct)
		}
	}
	totals, err := c.store.Totals(ctx, store.EventFilter{Host: h.Host})
	if err != nil {
		return hd, nil, nil, err
	}
	for _, t := range totals {
		hd.CE += t.CE
		hd.UE += t.UE
		hd.Records += t.Records
		hd.GradeA += t.ErrorsA
		hd.GradeB += t.ErrorsB
		hd.GradeC += t.ErrorsC
	}
	boots, err := c.store.Boots(ctx, h.Host)
	if err != nil {
		return hd, nil, nil, err
	}
	hd.Boots = len(boots)
	scan, srcs, err := c.latestScan(ctx, h.Host)
	if err != nil {
		return hd, nil, nil, err
	}
	if scan != nil {
		hd.Scan = &ScanInfo{Kind: scan.Kind, At: scan.StartedAt, Privileged: scan.Privileged, ToolVersion: scan.ToolVersion, Note: scan.Note}
	}
	return hd, scan, srcs, nil
}

// Overview builds the first screen of a host. With all set, the ranking
// also lists the DIMMs nothing is on record about.
func (c *Case) Overview(ctx context.Context, h *engine.HostResult, all bool) (*Overview, error) {
	hd, scan, srcs, err := c.header(ctx, h)
	if err != nil {
		return nil, err
	}
	ov := &Overview{Host: hd, Ranking: []Rank{}, Sources: []Source{}}

	slots, err := c.store.Slots(ctx, h.Host)
	if err != nil {
		return nil, err
	}
	ov.SlotMap = slotMap(h, slots)

	for _, d := range h.DIMMs {
		if !Listed(d) && !all {
			if d.Present {
				ov.Quiet++
			}
			continue
		}
		r := Rank{
			N: len(ov.Ranking) + 1, Slot: d.Slot, DIMM: dimmInfo(d.UID, d.DIMM), Present: d.Present,
			CE: d.Features.CE, UE: d.Features.UE, Faults: len(d.Faults), Direction: d.Features.Trend.Direction,
			Grade: d.Verdict.Grade, GradeText: engine.GradeText(c.Lang, d.Verdict.Grade), Rules: d.Verdict.Rules, Confidence: d.Verdict.Confidence,
		}
		if r.Rules == nil {
			r.Rules = []string{}
		}
		if len(d.Faults) > 0 {
			f := d.Faults[0]
			r.Mode, r.ModeText, r.Estimated = f.Mode, engine.ModeText(c.Lang, f.Mode), f.Estimated
		}
		if r.Trend, err = c.trend(ctx, d.UID, h.AsOf); err != nil {
			return nil, err
		}
		ov.Ranking = append(ov.Ranking, r)
	}
	for _, g := range h.GPUs {
		row := GPURow{GPU: g.Key, Index: g.Index, Grade: g.Verdict.Grade, GradeText: engine.GradeText(c.Lang, g.Verdict.Grade), Rules: g.Verdict.Rules}
		for _, e := range g.Verdict.Evidence {
			row.Evidence = append(row.Evidence, c.statement(e.Statement))
		}
		ov.GPUs = append(ov.GPUs, row)
	}
	looseCE, looseUE := Loose(h)
	ov.Unattributed, ov.UnattributedUE = looseCE+looseUE, looseUE
	if !h.AsOf.IsZero() {
		// The same days and the same events as on the timeline of the host.
		offlines, err := c.store.Actions(ctx, store.ActionFilter{Host: h.Host, To: lastDay(h.AsOf).AddDate(0, 0, 1),
			Kinds: []model.ActionKind{model.ActionPageOffline}})
		if err != nil {
			return nil, err
		}
		for _, a := range actionEvents(offlines) {
			if a.DIMMUID == "" {
				ov.LoosePages += max(a.Count, 1)
			}
		}
	}

	var unreadable []string
	names := c.sourceNames(srcs)
	for i, s := range srcs {
		ov.Sources = append(ov.Sources, Source{
			Kind: s.Kind, Name: names[i], Origin: s.Origin, Status: s.Status, StatusText: c.statusText(s.Status), Detail: s.Detail,
			Records: s.Records, Events: s.Events, Actions: s.Actions, Skipped: s.Skipped, Grade: s.BestGrade(),
			First: s.FirstTS, Last: s.LastTS, Notes: s.Notes,
		})
		switch s.Status {
		case model.SourceUnreadable:
			unreadable = append(unreadable, s.Kind)
			fallthrough
		case model.SourcePartial, model.SourceError:
			ov.Missing = append(ov.Missing, c.missingSource(s))
		case model.SourceAbsent:
			if absentMatters[s.Kind] {
				ov.Missing = append(ov.Missing, c.missingSource(s))
			}
		}
	}
	if scan != nil && scan.Kind == model.ScanLive && !scan.Privileged && len(unreadable) > 0 {
		seen := map[string]bool{}
		var what []string
		for _, kind := range unreadable {
			if seen[kind] {
				continue
			}
			seen[kind] = true
			code := "adds." + kind
			if !Messages.Has(code) {
				what = append(what, kind)
				continue
			}
			what = append(what, Messages.Text(c.Lang, code, nil))
		}
		t := c.say("root.adds", "what", strings.Join(what, "; "))
		ov.Root = &t
	}
	return ov, nil
}

// sourceNames names the sources of a scan for a line of text. A kind that
// was read once is named by its kind. Where several sources of one kind
// were read, as the journal and the ring buffer of a live host are, each
// also says which one it is.
func (c *Case) sourceNames(srcs []model.Source) []string {
	read := func(s model.Source) bool {
		switch s.Status {
		case model.SourceOK, model.SourcePartial, model.SourceEmpty:
			return true
		}
		return false
	}
	count := map[string]int{}
	for _, s := range srcs {
		if read(s) {
			count[s.Kind]++
		}
	}
	names := make([]string, len(srcs))
	for i, s := range srcs {
		names[i] = s.Kind
		if !read(s) || count[s.Kind] < 2 {
			continue
		}
		var which string
		switch {
		case strings.HasPrefix(s.Origin, "journalctl "):
			which = Messages.Text(c.Lang, "src.journal", nil)
		case s.Origin == "/dev/kmsg" || strings.HasSuffix(s.Origin, "/dev/kmsg"):
			which = Messages.Text(c.Lang, "src.kmsg", nil)
		default:
			which = s.Origin
			if i := strings.LastIndexAny(which, `/\`); i >= 0 && i+1 < len(which) {
				which = which[i+1:]
			}
		}
		if which != "" {
			names[i] = s.Kind + " (" + which + ")"
		}
	}
	return names
}

// absentMatters lists the sources whose absence a reader has to know
// about: without them there are no located error records, or no DIMM
// identities.
var absentMatters = map[string]bool{"hostinfo": true, "rasdaemon": true, "klog": true, "edac-sysfs": true, "smbios": true}

func (c *Case) statusText(s model.SourceStatus) string {
	switch s {
	case model.SourceOK:
		return Messages.Text(c.Lang, "status.ok", nil)
	}
	return engine.Messages.Text(c.Lang, "status."+string(s), nil)
}

// missingSource states that a source could not be read, in the words the
// findings use for it.
func (c *Case) missingSource(s model.Source) Text {
	st := model.Statement{Code: "missing.source", Args: map[string]any{"kind": s.Kind, "status": "@status." + string(s.Status)}}
	// "not present" as the detail of a source that is not there adds nothing.
	if s.Detail != "" && s.Detail != "not present" {
		st.Code = "missing.source_detail"
		st.Args["detail"] = s.Detail
	}
	return c.statement(st)
}

// trend returns the errors of a DIMM over the TrendDays days up to asOf,
// in TrendBuckets steps.
func (c *Case) trend(ctx context.Context, uid string, asOf time.Time) ([]int64, error) {
	out := make([]int64, TrendBuckets)
	if asOf.IsZero() {
		return out, nil
	}
	last := lastDay(asOf)
	first := last.AddDate(0, 0, -(TrendDays - 1))
	days, err := c.store.Daily(ctx, store.EventFilter{DIMMUID: uid, From: first, To: last.AddDate(0, 0, 1)}, 0)
	if err != nil {
		return nil, err
	}
	per := TrendDays / TrendBuckets
	for _, d := range days {
		t, err := time.Parse("2006-01-02", d.Day)
		if err != nil {
			continue
		}
		if i := int(t.Sub(first).Hours()/24) / per; i >= 0 && i < TrendBuckets {
			out[i] += d.CE + d.UE
		}
	}
	return out, nil
}

// lastDay returns the last UTC day that lies at least partly before t: the
// day of t, or the day before when t is midnight.
func lastDay(t time.Time) time.Time {
	t = t.UTC().Add(-time.Nanosecond)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// slotMap lays the slots of a host out by socket and channel.
func slotMap(h *engine.HostResult, slots []model.Slot) SlotMap {
	sm := SlotMap{Rows: []SlotRow{}, Slots: len(slots)}
	type keyed struct {
		slot  model.Slot
		group string // row key of a slot without a socket
	}
	var known, loose []keyed
	for _, sl := range slots {
		if sl.Socket.Valid {
			known = append(known, keyed{slot: sl})
		} else {
			loose = append(loose, keyed{slot: sl, group: namePrefix(sl.Name)})
		}
	}
	sort.SliceStable(loose, func(i, j int) bool { return naturalLess(loose[i].slot.Name, loose[j].slot.Name) })

	// Rows: one per socket, then one per name prefix among the rest.
	type rowKey struct {
		socket model.Opt[int]
		group  string
	}
	var order []rowKey
	members := map[rowKey][]model.Slot{}
	add := func(k rowKey, sl model.Slot) {
		if _, ok := members[k]; !ok {
			order = append(order, k)
		}
		members[k] = append(members[k], sl)
	}
	for _, k := range known {
		add(rowKey{socket: k.slot.Socket}, k.slot)
	}
	// A name that shares its beginning with no other name stands in one
	// row with the others of its kind, under its whole name: a row of one
	// cell would split a name for nothing.
	groupSize := map[string]int{}
	for _, k := range loose {
		groupSize[k.group]++
	}
	for _, k := range loose {
		if groupSize[k.group] < 2 {
			k.group = ""
		}
		add(rowKey{group: k.group}, k.slot)
	}

	for _, k := range order {
		row := SlotRow{Socket: k.socket, Channels: []SlotChannel{}}
		names := make([]string, len(members[k]))
		for i, sl := range members[k] {
			names[i] = sl.Name
		}
		prefix := commonPrefix(names)
		if !k.socket.Valid && k.group == "" {
			prefix = "" // names that share nothing by design
		}
		row.Label = strings.TrimRightFunc(prefix, isSeparator)
		if row.Label == "" {
			prefix = ""
		}
		type chanKey struct{ imc, ch model.Opt[int] }
		idx := map[chanKey]int{}
		for _, sl := range members[k] {
			cell := SlotCell{Slot: sl.Name, Label: strings.TrimPrefix(sl.Name, prefix), Position: sl.Position, State: SlotUnknown}
			switch d := h.InSlot(sl.Name); {
			case d != nil && !(sl.Populated.Valid && !sl.Populated.V):
				cell.State = string(d.Verdict.Grade)
				cell.CE, cell.UE = d.Features.CE, d.Features.UE
				if d.DIMM != nil {
					cell.Serial = d.DIMM.Serial
				}
				sm.Populated++
			case sl.Populated.Valid && sl.Populated.V:
				cell.State = string(model.VerdictOK)
				sm.Populated++
			case sl.Populated.Valid:
				cell.State = SlotEmpty
			}
			ck := chanKey{sl.IMC, sl.Channel}
			i, ok := idx[ck]
			if !ok || !sl.Channel.Valid {
				// A slot whose channel is unknown stands alone.
				i = len(row.Channels)
				idx[ck] = i
				row.Channels = append(row.Channels, SlotChannel{IMC: sl.IMC, Channel: sl.Channel, Slots: []SlotCell{}})
			}
			row.Channels[i].Slots = append(row.Channels[i].Slots, cell)
		}
		sm.Rows = append(sm.Rows, row)
	}

	// Two rows with one label cannot be told apart: such rows go without,
	// and their cells carry the whole slot name.
	count := map[string]int{}
	for _, r := range sm.Rows {
		count[r.Label]++
	}
	for i := range sm.Rows {
		r := &sm.Rows[i]
		if r.Label == "" || count[r.Label] == 1 {
			continue
		}
		r.Label = ""
		for c := range r.Channels {
			for s := range r.Channels[c].Slots {
				r.Channels[c].Slots[s].Label = r.Channels[c].Slots[s].Slot
			}
		}
	}
	return sm
}

func isSeparator(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }

// namePrefix returns the part of a slot name up to and including its last
// separator: "CPU0_" for "CPU0_C1", and "" for "A1".
func namePrefix(name string) string {
	last := -1
	for i, r := range name {
		if isSeparator(r) {
			last = i + len(string(r))
		}
	}
	if last < 0 || last >= len(name) {
		return ""
	}
	return name[:last]
}

// commonPrefix returns what all names start with, cut back to end after a
// separator, so that the rest of every name is a whole word: "CPU0_" for
// CPU0_A1 and CPU0_B1, "" for DIMM10 and DIMM11.
func commonPrefix(names []string) string {
	if len(names) == 0 {
		return ""
	}
	p := names[0]
	for _, n := range names[1:] {
		for !strings.HasPrefix(n, p) {
			p = p[:len(p)-1]
		}
	}
	for p != "" {
		r := []rune(p)
		if isSeparator(r[len(r)-1]) {
			break
		}
		p = string(r[:len(r)-1])
	}
	for _, n := range names {
		if n == p {
			return ""
		}
	}
	return p
}

// naturalLess orders names so that numbers inside them count as numbers:
// A2 before A10.
func naturalLess(a, b string) bool {
	ar, br := []rune(a), []rune(b)
	i, j := 0, 0
	for i < len(ar) && j < len(br) {
		if unicode.IsDigit(ar[i]) && unicode.IsDigit(br[j]) {
			si, sj := i, j
			for i < len(ar) && unicode.IsDigit(ar[i]) {
				i++
			}
			for j < len(br) && unicode.IsDigit(br[j]) {
				j++
			}
			x, y := strings.TrimLeft(string(ar[si:i]), "0"), strings.TrimLeft(string(br[sj:j]), "0")
			if len(x) != len(y) {
				return len(x) < len(y)
			}
			if x != y {
				return x < y
			}
			continue
		}
		if ar[i] != br[j] {
			return ar[i] < br[j]
		}
		i++
		j++
	}
	return len(ar)-i < len(br)-j
}

// HostRow is one host of a ledger that holds several.
type HostRow struct {
	Name      string             `json:"name"`
	Grade     model.VerdictGrade `json:"grade"`
	GradeText string             `json:"grade_text"`
	DIMMs     int                `json:"dimms"`
	// Findings is the number of DIMMs with a finding other than ok.
	Findings int   `json:"findings"`
	CE       int64 `json:"ce"`
	UE       int64 `json:"ue"`
	// Worst is the slot of the DIMM with the worst finding, when there is
	// one.
	Worst string    `json:"worst,omitzero"`
	To    time.Time `json:"to,omitzero"`
}

// Hosts lists the hosts of a ledger with the finding for each, the worst
// first: by grade, then by uncorrected and corrected errors, then by name.
// In a ledger that holds a fleet the hosts to look at come first.
func (c *Case) Hosts() []HostRow {
	out := make([]HostRow, 0, len(c.Result.Hosts))
	for _, h := range c.Result.Hosts {
		row := HostRow{Name: h.Host, Grade: h.Grade, GradeText: engine.GradeText(c.Lang, h.Grade), To: h.AsOf}
		for _, d := range h.DIMMs {
			if !d.Present {
				continue
			}
			row.DIMMs++
			row.CE += d.Features.CE
			row.UE += d.Features.UE
			if d.Verdict.Grade != model.VerdictOK {
				row.Findings++
				if row.Worst == "" {
					row.Worst = d.Slot
				}
			}
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case a.Grade != b.Grade:
			return a.Grade.ExitCode() > b.Grade.ExitCode()
		case a.UE != b.UE:
			return a.UE > b.UE
		case a.CE != b.CE:
			return a.CE > b.CE
		}
		return a.Name < b.Name
	})
	return out
}
