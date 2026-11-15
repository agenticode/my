package view

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/model"
)

// Card is the finding for one DIMM with everything it rests on: the answer
// to "what to do".
type Card struct {
	Host string   `json:"host"`
	Slot string   `json:"slot"`
	DIMM DIMMInfo `json:"dimm"`
	// Present is false for a DIMM that is no longer in the host.
	Present bool `json:"present"`
	// Since is when the ledger first saw the DIMM in this slot, and Moves
	// how often it changed slots after the ledger first saw it.
	Since time.Time `json:"since,omitzero"`
	Moves int       `json:"moves"`
	CE    int64     `json:"ce"`
	UE    int64     `json:"ue"`

	Grade     model.VerdictGrade `json:"grade"`
	GradeText string             `json:"grade_text"`
	// Label says what a grade is: a finding.
	Label string `json:"label"`
	// Confidence is a number from 0 to 100; ConfidenceLevel is "high",
	// "medium" or "low", and ConfidenceText that word in the language of
	// the view.
	Confidence      int    `json:"confidence"`
	ConfidenceLevel string `json:"confidence_level"`
	ConfidenceText  string `json:"confidence_text"`
	// Rules are the IDs of the rules that fired, the rules that set the
	// grade first.
	Rules    []string   `json:"rules"`
	Evidence []Evidence `json:"evidence"`
	// Checks are the tests of the finding against other explanations.
	Checks []Check `json:"checks"`
	// Missing lists the data the finding did not have.
	Missing []Text `json:"missing"`
	// Next is the step that follows from the finding.
	Next *Text `json:"next,omitzero"`
	// Baseline says how unusual the errors are: against the past of the
	// DIMM, against the DIMMs of its part number in the ledger and, where a
	// vendor analyzer supplies one, against a reference for the part.
	Baseline []Text `json:"baseline,omitzero"`
	// Factors are the steps that led to the confidence.
	Factors []Factor `json:"confidence_factors,omitzero"`
	// Faults are the groups of errors the engine made out, the largest
	// first.
	Faults []FaultRow `json:"faults"`

	EngineVersion string    `json:"engine_version"`
	AsOf          time.Time `json:"as_of,omitzero"`
}

// Evidence is one statement of a finding with the rule that made it.
type Evidence struct {
	Rule string `json:"rule"`
	Text
	// Records is the number of ledger records the statement rests on, and
	// First and Last say where the original of the first and of the last
	// of them is.
	Records int    `json:"records,omitzero"`
	First   string `json:"first,omitzero"`
	Last    string `json:"last,omitzero"`
}

// Check is the outcome of one test of a finding against another
// explanation.
type Check struct {
	// Effect is "supports", "weakens" or "open".
	Effect     string `json:"effect"`
	EffectText string `json:"effect_text"`
	Text
}

// Factor is one step in the confidence of a finding.
type Factor struct {
	Delta int `json:"delta"`
	Text
}

// FaultRow describes one fault of a DIMM.
type FaultRow struct {
	Mode      model.FaultMode `json:"mode"`
	ModeText  string          `json:"mode_text"`
	Estimated bool            `json:"estimated,omitzero"`
	// Where names the place, for example "rank0 dev7 bg0 bank3 row 0x1a2f".
	Where string      `json:"where,omitzero"`
	Grade model.Grade `json:"grade"`
	CE    int64       `json:"ce"`
	UE    int64       `json:"ue"`
	First time.Time   `json:"first,omitzero"`
	Last  time.Time   `json:"last,omitzero"`
	// Rows, Columns and Banks count the distinct places involved.
	Rows    int `json:"rows,omitzero"`
	Columns int `json:"columns,omitzero"`
	Banks   int `json:"banks,omitzero"`
	// Direction is the trend of the fault, as in the ranking.
	Direction string `json:"direction"`
	Recurring bool   `json:"recurring"`
	Confined  bool   `json:"confined"`
}

// Card builds the finding card of a DIMM.
func (c *Case) Card(ctx context.Context, h *engine.HostResult, d *engine.DIMMResult) (*Card, error) {
	v := d.Verdict
	level := engine.ConfidenceLevel(v.Confidence)
	card := &Card{
		Host: h.Host, Slot: d.Slot, DIMM: dimmInfo(d.UID, d.DIMM), Present: d.Present,
		CE: d.Features.CE, UE: d.Features.UE,
		Grade: v.Grade, GradeText: engine.GradeText(c.Lang, v.Grade), Label: engine.Messages.Text(c.Lang, "label.finding", nil),
		Confidence: v.Confidence, ConfidenceLevel: level, ConfidenceText: engine.Messages.Text(c.Lang, "confidence."+level, nil),
		Rules: v.Rules, Evidence: []Evidence{}, Checks: []Check{}, Missing: []Text{}, Faults: []FaultRow{},
		EngineVersion: v.EngineVersion, AsOf: h.AsOf,
	}
	if card.Rules == nil {
		card.Rules = []string{}
	}
	for _, e := range v.Evidence {
		ev := Evidence{Rule: e.Rule, Text: c.statement(e.Statement), Records: e.Total}
		if n := len(e.Records); n > 0 {
			ev.First, ev.Last = e.Records[0].RawRef, e.Records[n-1].RawRef
			if ev.Records == 0 {
				ev.Records = n
			}
		}
		card.Evidence = append(card.Evidence, ev)
	}
	for _, k := range v.CounterEvidence {
		card.Checks = append(card.Checks, Check{Effect: k.Effect, EffectText: Messages.Text(c.Lang, "effect."+k.Effect, nil), Text: c.statement(k.Statement)})
	}
	for _, m := range v.Missing {
		card.Missing = append(card.Missing, c.statement(m))
	}
	if v.NextAction.Code != "" {
		t := c.statement(v.NextAction)
		card.Next = &t
	}
	for _, f := range v.ConfidenceFactors {
		card.Factors = append(card.Factors, Factor{Delta: f.Delta, Text: c.statement(f.Statement)})
	}
	card.Baseline = c.baseline(d)
	for _, f := range d.Faults {
		card.Faults = append(card.Faults, FaultRow{
			Mode: f.Mode, ModeText: engine.ModeText(c.Lang, f.Mode), Estimated: f.Estimated, Where: f.Where(), Grade: f.Grade,
			CE: f.CE, UE: f.UE, First: f.FirstSeen, Last: f.LastSeen,
			Rows: f.Shape.Rows, Columns: f.Shape.Columns, Banks: f.Shape.Banks,
			Direction: f.Trend.Direction, Recurring: f.Recurring, Confined: f.Confined,
		})
	}

	// Where the DIMM has been.
	places, err := c.store.Placements(ctx, "", d.UID)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(places, func(i, j int) bool { return places[i].FirstSeen.Before(places[j].FirstSeen) })
	for i, p := range places {
		if i > 0 && (p.Host != places[i-1].Host || p.Slot != places[i-1].Slot) {
			card.Moves++
		}
	}
	// The latest unbroken stay in the slot it is in now.
	for i := len(places) - 1; i >= 0; i-- {
		if places[i].Host != d.Host || places[i].Slot != d.Slot {
			break
		}
		card.Since = places[i].FirstSeen
	}
	return card, nil
}

// baseline states the baseline layers that have enough data.
func (c *Case) baseline(d *engine.DIMMResult) []Text {
	cfg := c.Result.Config
	var out []Text
	if b := d.Baseline.Own; b.Valid {
		out = append(out, c.say("base.own", "recent", round1(b.RecentPerDay), "days", cfg.RecentDays,
			"pct", math.Round(b.Percentile), "base", math.Round(b.PriorDays)))
	}
	if b := d.Baseline.Peers; b.Valid {
		out = append(out, c.say("base.peers", "part", b.PartNumber, "pct", math.Round(b.Percentile), "n", b.Peers, "with", b.WithErrors))
	}
	if b := d.Baseline.Vendor; b.Valid {
		out = append(out, c.say("base.vendor", "pct", math.Round(b.Percentile)))
	}
	return out
}

// round1 rounds to one decimal, so that a whole number prints as one.
func round1(x float64) float64 { return math.Round(x*10) / 10 }

// Examination is everything the screens show about one DIMM.
type Examination struct {
	Card       *Card       `json:"card"`
	Topology   *Topology   `json:"topology"`
	AddressMap *AddressMap `json:"address_map"`
	Timeline   *Timeline   `json:"timeline"`
}

// Examine builds the examination of a DIMM. The address map shows the bank
// with the most errors; cells is the width of its grid in cells.
func (c *Case) Examine(ctx context.Context, h *engine.HostResult, d *engine.DIMMResult, cells int) (*Examination, error) {
	x := &Examination{}
	var err error
	if x.Card, err = c.Card(ctx, h, d); err != nil {
		return nil, err
	}
	if x.Topology, err = c.Topology(ctx, d); err != nil {
		return nil, err
	}
	if x.AddressMap, err = c.AddressMap(ctx, h, d, MapOptions{Cells: cells, MaxLines: 16}); err != nil {
		return nil, err
	}
	if x.Timeline, err = c.Timeline(ctx, h, d, TimelineOptions{}); err != nil {
		return nil, err
	}
	return x, nil
}
