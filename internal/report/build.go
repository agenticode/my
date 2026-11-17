// Package report writes the findings of a case as one HTML file.
//
// The file is self-contained: figures are inline SVG, the style sheet and
// a small script are inline, and nothing is loaded from elsewhere, so the
// report opens on a machine without network access and can be sent as it
// is. Printing it from a browser gives a PDF.
//
// The report draws the figures of package view, the ones the terminal
// draws as text, and adds what a document needs: a summary, the checks
// against a wrong diagnosis, the quality of the data, and an appendix with
// the original records the figures and findings point at.
//
// Build gathers the content of a report from a case; Document.Write
// writes it. The markup is HTML that is also well-formed XML, so that a
// test can parse a whole report.
package report

import (
	"context"
	"fmt"
	"github.com/agenticode/my/internal/adapter"
	"sort"
	"strings"
	"time"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/view"
)

// Audience says who a report is written for.
type Audience string

const (
	// Customer is a report to send to the operator of the host: the
	// findings with the public Level 1 fault modes.
	Customer Audience = "customer"
	// FA is a report for failure analysis: it adds the steps of the
	// confidence, the faults one by one and, when a vendor analyzer
	// supplied it, its Level 2 output.
	FA Audience = "fa"
)

// ParseAudience reads the name of an audience.
func ParseAudience(s string) (Audience, error) {
	switch a := Audience(strings.ToLower(strings.TrimSpace(s))); a {
	case Customer, FA:
		return a, nil
	}
	return "", fmt.Errorf("unknown audience %q (customer or fa)", s)
}

// CaseInfo describes the case a report is made from.
type CaseInfo struct {
	// ID identifies the case in a few characters. Empty means that the
	// identity of the ledger is used.
	ID string
	// Bundle is set when the case is a case bundle. Created, Tool and
	// Anonymized then describe the bundle.
	Bundle     bool
	Created    time.Time
	Tool       string
	Anonymized bool
	// SubjectUID is the ledger identity of the DIMM the case is about,
	// when the sender named one.
	SubjectUID string
}

// Level2 is what a vendor analyzer said about one DIMM beyond the public
// fault modes. A report shows it to the failure analysis audience only.
type Level2 struct {
	// DIMMUID is the ledger identity of the DIMM.
	DIMMUID string
	// Analyzer names the analyzer and its version.
	Analyzer string
	// Lines are the statements of the analyzer: a label and a value each.
	Lines []Level2Line
	// Notes are sentences of the analyzer.
	Notes []string
}

// Level2Line is one labelled value of a vendor analyzer.
type Level2Line struct {
	Label string
	Value string
}

// Options describe a report.
type Options struct {
	// Lang is the language of the report; empty means the language of the
	// case.
	Lang i18n.Lang
	// Audience is who the report is for; empty means Customer.
	Audience Audience
	// Now is the time the report is made; zero means the current time.
	Now time.Time
	// Tool names the program that makes the report, with its version.
	Tool string
	// Case describes the case; nil means a ledger without a bundle.
	Case *CaseInfo
	// Level2 holds the output of vendor analyzers.
	Level2 []Level2
	// AllFigures draws the figures of every DIMM with errors. Without it
	// a DIMM whose finding is "observe" and that has fewer than
	// DetailMinErrors errors has its finding in the report and no figures.
	AllFigures bool
}

// Limits of what a report lists. A limit that cuts something is stated in
// the report at the place where it cuts.
const (
	// MapCells is the width of an address map in cells.
	MapCells = 64
	// MaxLinkedCells is the number of cells of one address map that lead
	// to their records: the cells with the most errors.
	MaxLinkedCells = 24
	// MaxLinkedMarks is the number of events of one kind on a timeline
	// that lead to their records: the latest ones.
	MaxLinkedMarks = 24
	// MaxCellRows is the number of cells the table beside an address map
	// lists.
	MaxCellRows = 40
	// MaxActions is the number of repairs and isolation steps listed per
	// DIMM: the latest ones.
	MaxActions = 30
	// DetailMinErrors is the number of errors from which a DIMM whose
	// finding is "observe" gets figures of its own. A DIMM with a finding
	// above that, and the subject of the case, always get them.
	DetailMinErrors = 10
)

// Document is the content of a report.
type Document struct {
	Lang      i18n.Lang
	Audience  Audience
	Generated time.Time
	Tool      string
	Case      CaseInfo
	// CaseID identifies the case on the cover.
	CaseID string

	Overview *view.Overview
	// Board is the board of the host as the Memory Explorer draws it, or
	// nil when it could not be built.
	Board *view.Board
	// Explorer is the memory of the host when the scan ran, in the tabs
	// of the Memory Explorer; empty when the case holds no reading.
	Explorer *view.Explorer
	// Timeline is the timeline of the host, with one row per DIMM.
	Timeline *view.Timeline
	// Boots is the window of the latest boots of the host.
	Boots *view.Boots
	// DIMMs are the DIMMs the report describes: those with errors or a
	// finding, the worst first, and the subject of the case.
	DIMMs []*DIMM
	// GPUs are the findings about GPU memory, the worst first.
	GPUs []*GPU
	// Host is the card of the host finding, which speaks about the errors
	// and page offlines that are tied to no DIMM, or nil.
	Host *view.Card
	// Inventory lists the DIMMs of the host one per line and sets what the
	// operating system and the BMC each recorded side by side.
	Inventory *view.Inventory
	// Present is the number of DIMMs in the host.
	Present int

	EngineVersion string
	// Thresholds lists the thresholds that differ from the defaults, as
	// pairs of key and value.
	Thresholds [][2]string

	// Records are the ledger records the report points at, in time order.
	Records []*Record
	// TotalRecords is the number of records the ledger holds for the
	// host; Duplicates is how many of them repeat an error another source
	// reports with more detail.
	TotalRecords int64
	Duplicates   int64
	// Undated counts the errors of the host known by seconds since boot.
	Undated int64
	// NoSerial counts the DIMMs of the host without a serial number.
	NoSerial int

	// Firmware are the firmware records of the host, Registries the
	// message registries its BMCs published.
	Firmware   []model.FirmwareRecord
	Registries []model.RegistrySeen

	records map[string]*Record
}

// DIMM is everything a report says about one DIMM.
type DIMM struct {
	// Anchor is the name of the DIMM in links within the report.
	Anchor string
	// Subject is set on the DIMM the case is about.
	Subject bool
	// Detail is set on a DIMM the report draws figures for: topology bars,
	// address map, timeline and the table of checks. Without it Topology,
	// Map and Timeline are nil.
	Detail   bool
	Card     *view.Card
	Topology *view.Topology
	Map      *view.AddressMap
	Timeline *view.Timeline
	// Boots holds the places of the DIMM boot by boot, when it had errors
	// in two or more boots of the window.
	Boots *view.Boots
	// Linked says which cells of the map lead to their records, by index
	// in Map.Refs.
	Linked map[int]bool
	// Actions are the latest repairs and isolation steps of the DIMM,
	// oldest first; ActionCount is how many the ledger holds.
	Actions     []model.Action
	ActionCount int
	// GradeA, GradeB and GradeC split the errors by observation grade.
	GradeA, GradeB, GradeC int64
	Level2                 []Level2
}

// GPU is the finding about the memory of one GPU.
type GPU struct {
	// Anchor is the name of the GPU in links within the report.
	Anchor string
	// Key identifies the GPU: its serial number, or its UUID.
	Key       string
	Grade     model.VerdictGrade
	GradeText string
	Rules     []string
	Evidence  []view.Evidence
	// Next is the step that follows from the finding.
	Next *view.Text
}

// Record is one ledger record with its original text.
type Record struct {
	// Key names the record in the report: "e" and the ID of an error
	// record, "a" and the ID of an action, "g" and the ID of a GPU record.
	Key  string
	Kind string
	ID   int64
	At   time.Time
	// TimeQuality says how far At can be trusted.
	TimeQuality model.TimeQuality
	// Class is "ce", "ue", "info" or "action".
	Class string
	// What names the record: a severity, or the kind of an action.
	What   string
	Slot   string
	Where  string
	Count  int
	Grade  model.Grade
	Source string
	RawRef string
	Raw    string
	// Cited is set on a record a finding cites.
	Cited bool
	// Event is the ledger record of an error.
	Event *model.Event
}

// Build gathers the content of a report about one host of a case.
func Build(ctx context.Context, c *view.Case, h *engine.HostResult, opts Options) (*Document, error) {
	doc := &Document{
		Lang: opts.Lang, Audience: opts.Audience, Generated: opts.Now, Tool: opts.Tool,
		EngineVersion: c.Result.EngineVersion, Thresholds: c.Result.Config.Changed(),
		records: map[string]*Record{},
	}
	if doc.Lang == "" {
		doc.Lang = c.Lang
	}
	if doc.Lang != c.Lang {
		return nil, fmt.Errorf("report: the case speaks %s, the report was asked for in %s", c.Lang, doc.Lang)
	}
	if doc.Audience == "" {
		doc.Audience = Customer
	}
	if doc.Generated.IsZero() {
		doc.Generated = time.Now()
	}
	if doc.Tool == "" {
		doc.Tool = "my"
	}
	doc.Generated = doc.Generated.UTC().Truncate(time.Second)
	if opts.Case != nil {
		doc.Case = *opts.Case
	}
	s := c.Store()
	doc.CaseID = doc.Case.ID
	if doc.CaseID == "" {
		id, err := s.Meta(ctx, "ledger_id")
		if err != nil {
			return nil, err
		}
		doc.CaseID = shortID(id)
	}

	var err error
	if doc.Overview, err = c.Overview(ctx, h, false); err != nil {
		return nil, err
	}
	doc.Explorer = adapter.Guard(func() *view.Explorer {
		e, err := c.Explorer(ctx, h)
		if err != nil {
			return nil
		}
		return e
	})
	// A board that fails leaves section 2 as the slot map draws it.
	doc.Board = adapter.Guard(func() *view.Board {
		b, err := c.Board(ctx, h)
		if err != nil {
			return nil
		}
		return b
	})
	if doc.Firmware, err = s.FirmwareRecords(ctx, store.FirmwareFilter{Host: h.Host}); err != nil {
		return nil, err
	}
	if doc.Registries, err = s.RegistriesSeen(ctx, h.Host); err != nil {
		return nil, err
	}
	if doc.Timeline, err = c.Timeline(ctx, h, nil, view.TimelineOptions{}); err != nil {
		return nil, err
	}
	doc.Undated = doc.Timeline.Undated
	if doc.Boots, err = c.BootsView(ctx, h, nil); err != nil {
		return nil, err
	}

	level2 := map[string][]Level2{}
	for _, l := range opts.Level2 {
		level2[l.DIMMUID] = append(level2[l.DIMMUID], l)
	}
	anchors := map[string]bool{}
	for _, d := range h.DIMMs {
		if d.Present {
			doc.Present++
			if d.DIMM == nil || d.DIMM.Serial == "" {
				doc.NoSerial++
			}
		}
		subject := doc.Case.SubjectUID != "" && d.UID == doc.Case.SubjectUID
		if !view.Listed(d) && !subject {
			continue
		}
		sec := &DIMM{
			Anchor: anchor(d.Slot, d.UID, anchors), Subject: subject,
			GradeA: d.Features.GradeA, GradeB: d.Features.GradeB, GradeC: d.Features.GradeC,
		}
		sec.Detail = subject || opts.AllFigures || d.Verdict.Grade.ExitCode() > model.VerdictObserve.ExitCode() ||
			d.Features.CE+d.Features.UE >= DetailMinErrors
		if doc.Audience == FA {
			sec.Level2 = level2[d.UID]
		}
		doc.DIMMs = append(doc.DIMMs, sec)
		if !sec.Detail {
			if sec.Card, err = c.Card(ctx, h, d); err != nil {
				return nil, err
			}
			continue
		}
		x, err := c.Examine(ctx, h, d, MapCells)
		if err != nil {
			return nil, err
		}
		sec.Card, sec.Topology, sec.Map, sec.Timeline, sec.Boots = x.Card, x.Topology, x.AddressMap, x.Timeline, x.Boots
		if x.Boots != nil && x.AddressMap != nil && x.AddressMap.Bank != nil {
			// The map counts the boots of each cell as well: cells with
			// errors in two or more boots are outlined.
			if sec.Map, err = c.AddressMap(ctx, h, d, view.MapOptions{Cells: MapCells, MaxLines: 16, Boots: true}); err != nil {
				return nil, err
			}
		}
		actions, err := s.Actions(ctx, store.ActionFilter{DIMMUID: d.UID})
		if err != nil {
			return nil, err
		}
		sec.ActionCount = len(actions)
		if len(actions) > MaxActions {
			actions = actions[len(actions)-MaxActions:]
		}
		sec.Actions = actions
	}

	text := func(st model.Statement) view.Text {
		return view.Text{Code: st.Code, Args: st.Args, Text: engine.Text(doc.Lang, st)}
	}
	for _, g := range h.GPUs {
		v := g.Verdict
		gpu := &GPU{Key: g.Key, Grade: v.Grade, GradeText: engine.GradeText(doc.Lang, v.Grade), Rules: v.Rules}
		gpu.Anchor = strings.Replace(anchor(g.Key, g.Key, anchors), "dimm-", "gpu-", 1)
		for _, e := range v.Evidence {
			ev := view.Evidence{Rule: e.Rule, Text: text(e.Statement), Records: e.Total, Cited: e.Records}
			if ev.Records == 0 {
				ev.Records = len(e.Records)
			}
			gpu.Evidence = append(gpu.Evidence, ev)
		}
		if v.NextAction.Code != "" {
			t := text(v.NextAction)
			gpu.Next = &t
		}
		doc.GPUs = append(doc.GPUs, gpu)
	}
	sort.SliceStable(doc.GPUs, func(i, j int) bool { return doc.GPUs[i].Grade.ExitCode() > doc.GPUs[j].Grade.ExitCode() })
	if doc.Host, err = c.HostCard(ctx, h); err != nil {
		return nil, err
	}
	if doc.Inventory, err = c.Inventory(ctx, h, view.InventoryOptions{}); err != nil {
		return nil, err
	}

	if err := doc.gather(ctx, s, h); err != nil {
		return nil, err
	}
	return doc, nil
}

// MaxCited is the number of records of one statement of a finding the
// report shows: the first and the last of those the finding lists.
const MaxCited = 4

// cited returns the records of a statement the report shows.
func cited(e *view.Evidence) []model.RecordRef {
	if len(e.Cited) <= MaxCited {
		return e.Cited
	}
	half := MaxCited / 2
	out := append([]model.RecordRef{}, e.Cited[:half]...)
	return append(out, e.Cited[len(e.Cited)-half:]...)
}

// linkedMarks returns the events of a marker group that lead to their
// records: the latest MaxLinkedMarks of those that have one.
func linkedMarks(g *view.MarkerGroup) []*view.Mark {
	var out []*view.Mark
	for i := len(g.Marks) - 1; i >= 0 && len(out) < MaxLinkedMarks; i-- {
		if g.Marks[i].Record != nil {
			out = append(out, &g.Marks[i])
		}
	}
	return out
}

// detailed returns the DIMMs the report draws figures for.
func (doc *Document) detailed() []*DIMM {
	var out []*DIMM
	for _, d := range doc.DIMMs {
		if d.Detail {
			out = append(out, d)
		}
	}
	return out
}

// shortID returns the first 12 characters of an identity.
func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// anchor makes the name of a DIMM in links: its slot name, reduced to
// letters, digits, "-" and "_". Two DIMMs that would share a name get a
// number.
func anchor(slot, uid string, used map[string]bool) string {
	name := slot
	if name == "" {
		name = shortID(uid)
	}
	var b strings.Builder
	b.WriteString("dimm-")
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	base := b.String()
	out := base
	for i := 2; used[out]; i++ {
		out = fmt.Sprintf("%s-%d", base, i)
	}
	used[out] = true
	return out
}

// key names a ledger record in the report.
func key(kind string, id int64) string {
	switch kind {
	case model.RecordAction:
		return fmt.Sprintf("a%d", id)
	case model.RecordGPU:
		return fmt.Sprintf("g%d", id)
	}
	return fmt.Sprintf("e%d", id)
}

// refKey names the record a reference points at.
func refKey(r model.RecordRef) string { return key(r.Kind, r.ID) }

// gather collects the records the report points at and reads them from
// the ledger with their original text.
func (doc *Document) gather(ctx context.Context, s *store.Store, h *engine.HostResult) error {
	type want struct {
		kind  string
		id    int64
		cited bool
	}
	wanted := map[string]*want{}
	need := func(kind string, id int64, cited bool) {
		if id == 0 {
			return
		}
		k := key(kind, id)
		w := wanted[k]
		if w == nil {
			w = &want{kind: kind, id: id}
			wanted[k] = w
		}
		w.cited = w.cited || cited
	}
	marks := func(tl *view.Timeline) {
		if tl == nil {
			return
		}
		for _, g := range tl.Markers {
			for _, m := range linkedMarks(&g) {
				need(m.Record.Kind, m.Record.ID, false)
			}
		}
	}
	marks(doc.Timeline)
	for _, g := range doc.GPUs {
		for i := range g.Evidence {
			for _, r := range cited(&g.Evidence[i]) {
				need(r.Kind, r.ID, true)
			}
		}
	}
	if doc.Host != nil {
		for i := range doc.Host.Evidence {
			for _, r := range cited(&doc.Host.Evidence[i]) {
				need(r.Kind, r.ID, true)
			}
		}
	}
	for _, d := range doc.DIMMs {
		for i := range d.Card.Evidence {
			for _, r := range cited(&d.Card.Evidence[i]) {
				need(r.Kind, r.ID, true)
			}
		}
		marks(d.Timeline)
		for _, a := range d.Actions {
			need(model.RecordAction, a.ID, false)
		}
		if d.Map == nil {
			continue
		}
		// The cells with the most errors lead to their records.
		order := make([]int, len(d.Map.Refs))
		for i := range order {
			order[i] = i
		}
		sort.SliceStable(order, func(a, b int) bool { return d.Map.Refs[order[a]].Errors > d.Map.Refs[order[b]].Errors })
		d.Linked = map[int]bool{}
		for n, i := range order {
			if n >= MaxLinkedCells {
				break
			}
			d.Linked[i] = true
			r := d.Map.Refs[i]
			need(model.RecordEvent, r.First, false)
			need(model.RecordEvent, r.Last, false)
			for _, id := range r.UE {
				need(model.RecordEvent, id, false)
			}
		}
	}

	// Actions and GPU records come with their text; they are read once.
	actions := map[int64]*model.Action{}
	loadActions := func(f store.ActionFilter) error {
		list, err := s.Actions(ctx, f)
		if err != nil {
			return err
		}
		for i := range list {
			actions[list[i].ID] = &list[i]
		}
		return nil
	}
	if err := loadActions(store.ActionFilter{Host: h.Host}); err != nil {
		return err
	}
	doc.TotalRecords = int64(len(actions))
	for _, d := range doc.DIMMs {
		if !d.Detail {
			continue
		}
		if err := loadActions(store.ActionFilter{DIMMUID: d.Card.DIMM.UID}); err != nil {
			return err
		}
	}
	var gpus map[int64]*model.GPUMem
	for _, w := range wanted {
		if w.kind != model.RecordGPU {
			continue
		}
		list, err := s.GPUMem(ctx, h.Host)
		if err != nil {
			return err
		}
		gpus = map[int64]*model.GPUMem{}
		for i := range list {
			gpus[list[i].ID] = &list[i]
		}
		break
	}

	for k, w := range wanted {
		var r *Record
		switch w.kind {
		case model.RecordAction:
			if a := actions[w.id]; a != nil {
				r = doc.actionRecord(a)
			}
		case model.RecordGPU:
			if g := gpus[w.id]; g != nil {
				r = doc.gpuRecord(g)
			}
		default:
			e, err := s.Event(ctx, w.id)
			if err != nil {
				return err
			}
			if e != nil {
				r = doc.eventRecord(e)
			}
		}
		if r == nil {
			// A finding that cites a record the ledger does not hold is a
			// defect of the ledger; the report says which record is gone.
			r = &Record{Kind: w.kind, ID: w.id, Class: "info", What: "?"}
		}
		r.Key, r.Cited = k, w.cited
		doc.records[k] = r
		doc.Records = append(doc.Records, r)
	}
	sort.Slice(doc.Records, func(i, j int) bool {
		a, b := doc.Records[i], doc.Records[j]
		// Records dated by seconds since boot come first, as in the ledger.
		if ra, rb := a.TimeQuality == model.TimeRelative, b.TimeQuality == model.TimeRelative; ra != rb {
			return ra
		}
		if !a.At.Equal(b.At) {
			return a.At.Before(b.At)
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.ID < b.ID
	})

	// How many records the ledger holds for the host, and how many of
	// them repeat an error that another source reports.
	for e, err := range s.Events(ctx, store.EventFilter{Host: h.Host, WithDuplicates: true}) {
		if err != nil {
			return err
		}
		doc.TotalRecords++
		if e.DupOf != 0 {
			doc.Duplicates++
		}
	}
	return nil
}

// record returns the record a key names, or nil.
func (doc *Document) record(k string) *Record { return doc.records[k] }

// say renders a text of the report.
func (doc *Document) say(code string, kv ...any) string {
	var args map[string]any
	if len(kv) > 0 {
		args = make(map[string]any, len(kv)/2)
		for i := 0; i+1 < len(kv); i += 2 {
			args[kv[i].(string)] = kv[i+1]
		}
	}
	return Words.Text(doc.Lang, code, args)
}

// vsay renders a text of the figures, from the catalog of package view.
func (doc *Document) vsay(code string, kv ...any) string {
	var args map[string]any
	if len(kv) > 0 {
		args = make(map[string]any, len(kv)/2)
		for i := 0; i+1 < len(kv); i += 2 {
			args[kv[i].(string)] = kv[i+1]
		}
	}
	return view.Messages.Text(doc.Lang, code, args)
}

// esay renders a word of the engine catalog.
func (doc *Document) esay(code string) string { return engine.Messages.Text(doc.Lang, code, nil) }

// placeText names a place inside a DIMM the way the findings do.
func placeText(rank, dev, bg, bank, row, col model.Opt[int]) string {
	var parts []string
	if rank.Valid {
		parts = append(parts, fmt.Sprintf("rank%d", rank.V))
	}
	if dev.Valid {
		parts = append(parts, fmt.Sprintf("dev%d", dev.V))
	}
	if bg.Valid {
		parts = append(parts, fmt.Sprintf("bg%d", bg.V))
	}
	if bank.Valid {
		parts = append(parts, fmt.Sprintf("bank%d", bank.V))
	}
	if row.Valid {
		parts = append(parts, fmt.Sprintf("row %#x", row.V))
	}
	if col.Valid {
		parts = append(parts, fmt.Sprintf("col %#x", col.V))
	}
	return strings.Join(parts, " ")
}

func (doc *Document) eventRecord(e *model.Event) *Record {
	r := &Record{
		Kind: model.RecordEvent, ID: e.ID, At: e.ErrorDT, TimeQuality: e.TimeQuality, Slot: e.Slot,
		Count: e.Count, Grade: e.Grade, Source: e.Source, RawRef: e.RawRef, Raw: e.Raw, Event: e,
	}
	switch e.ErrorSeverity.Class() {
	case "CE":
		r.Class, r.What = "ce", doc.say("sev.CE")
	case "UE":
		r.Class, r.What = "ue", doc.say("sev.UE")
	default:
		r.Class, r.What = "info", doc.say("sev.info")
	}
	if r.Slot == "" {
		r.Slot = e.Label
	}
	r.Where = placeText(e.Rank, e.DeviceId, e.BankGroup, e.Bank, e.Row, e.Column)
	if !e.Row.Valid && e.OsPhysicalAddress.Valid {
		r.Where = strings.TrimSpace(r.Where + " " + doc.say("rec.address", "addr", fmt.Sprintf("%#x", e.OsPhysicalAddress.V)))
	}
	return r
}

// kindText names an action kind as the timeline does.
func (doc *Document) kindText(kind string) string {
	if code := "tl.kind." + kind; view.Messages.Has(code) {
		return doc.vsay(code)
	}
	return doc.esay("kind." + kind)
}

func (doc *Document) actionRecord(a *model.Action) *Record {
	r := &Record{
		Kind: model.RecordAction, ID: a.ID, At: a.At, TimeQuality: a.TimeQuality, Class: "action", Slot: a.Slot,
		What: doc.kindText(string(a.Kind)), Count: max(a.Count, 1), Source: a.Source, RawRef: a.RawRef, Raw: a.Raw,
	}
	if r.Slot == "" {
		r.Slot = a.Label
	}
	if a.Result == model.ResultFailed || a.Result == model.ResultPending {
		r.What += ", " + doc.say("res."+string(a.Result))
	}
	r.Where = doc.actionWhere(a)
	return r
}

// actionWhere names the target of an action.
func (doc *Document) actionWhere(a *model.Action) string {
	w := placeText(a.Rank, a.DeviceId, a.BankGroup, a.Bank, a.Row, model.Opt[int]{})
	if w == "" && a.OsPhysicalAddress.Valid {
		w = doc.say("rec.page", "page", fmt.Sprintf("%#x", a.OsPhysicalAddress.V>>12))
	}
	return w
}

func (doc *Document) gpuRecord(g *model.GPUMem) *Record {
	r := &Record{
		Kind: model.RecordGPU, ID: g.ID, At: g.ObservedAt, TimeQuality: g.TimeQuality, Class: "info",
		What: "GPU", Count: 1, Source: g.Source, RawRef: g.RawRef, Raw: g.Raw,
	}
	r.Slot = g.GPUSerial
	if r.Slot == "" {
		r.Slot = g.GPUUUID
	}
	if g.XID.Valid {
		r.What = fmt.Sprintf("XID %d", g.XID.V)
		r.Where = g.XIDText
	}
	return r
}

// timeText writes the time of a record: a UTC time, or seconds since boot
// for a record that has no date.
func (doc *Document) timeText(t time.Time, q model.TimeQuality) string {
	switch {
	case t.IsZero():
		return "?"
	case q == model.TimeRelative:
		return doc.say("rec.relative", "s", t.Unix())
	}
	return t.UTC().Format("2006-01-02 15:04:05")
}

// day writes a day.
func day(t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	return t.UTC().Format("2006-01-02")
}

// minute writes a time to the minute, in UTC.
func minute(t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	return t.UTC().Format("2006-01-02 15:04") + " UTC"
}
