package view

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/injection"
	"github.com/agenticode/my/internal/model"
)

// Verification compares an injection plan with what the sources of a host
// recorded. A plan says which errors a test put into the host: what, where
// and how many times. The comparison shows whether the path from the
// hardware to the ledger loses or changes anything: an error that is
// missing or counted twice, a row or a channel that was decoded as another.
type Verification struct {
	Host string `json:"host"`
	// Plan describes the plan: its scenario, how the errors were injected.
	Plan VerifyPlan `json:"plan"`
	// OK says that every injection was found as planned.
	OK bool `json:"ok"`
	// Passed and Failed count the injections that were and were not.
	Passed int `json:"passed"`
	Failed int `json:"failed"`
	// Checks holds one entry per injection, in the order of the plan.
	Checks []VerifyCheck `json:"checks"`
	// Sources counts the records that match an injection, by source, over
	// the whole plan.
	Sources []VerifySource `json:"sources,omitzero"`
	// Unreported lists the fields of the plan that no record of any
	// injection carries, with the number of injections that name them: the
	// platform does not expose them, which is no mismatch.
	Unreported []VerifyField `json:"unreported,omitzero"`
	// UnplannedErrors and UnplannedOfflines count what the ledger holds
	// beyond the plan, and Unplanned lists it, at most 50 lines. A host
	// may have errors of its own: they do not count against the plan.
	UnplannedErrors   int      `json:"unplanned_errors"`
	UnplannedOfflines int      `json:"unplanned_offlines"`
	Unplanned         []string `json:"unplanned,omitzero"`
	// Summary is the result in one sentence.
	Summary Text `json:"summary"`
}

// VerifyPlan is what a plan says about itself.
type VerifyPlan struct {
	Scenario string `json:"scenario"`
	Summary  string `json:"summary,omitzero"`
	Method   string `json:"method,omitzero"`
	// Meta holds the other lines of the plan's head, such as the kernel
	// and rasdaemon versions of the run.
	Meta       map[string]string `json:"meta,omitzero"`
	Injections int               `json:"injections"`
}

// Results of a check.
const (
	// VerifyOK: found, the planned number of times, decoded as injected.
	VerifyOK = "ok"
	// VerifyMissing: no record of the injection.
	VerifyMissing = "missing"
	// VerifyCount: found fewer times than planned.
	VerifyCount = "count"
	// VerifyDecode: found, and a field was decoded as something else.
	VerifyDecode = "decode"
	// VerifyUnexpected: a page that was planned to stay in use is offline.
	VerifyUnexpected = "unexpected"
)

// VerifyCheck is the comparison of one injection.
type VerifyCheck struct {
	N  int    `json:"n"`
	ID string `json:"id"`
	// Inject is "error" or "page-offline", and What names it for a reader:
	// the severity of an error, how a page was taken offline.
	Inject string `json:"inject"`
	What   string `json:"what"`
	// Where is the place the plan names: an address, a row, a DIMM.
	Where   string `json:"where"`
	Planned int    `json:"planned"`
	Found   int    `json:"found"`
	// Result is one of the Verify constants.
	Result     string `json:"result"`
	ResultText string `json:"result_text"`
	// Sources counts the records that match, by source. A record that the
	// ledger marked as a second report of another is counted here too.
	Sources []VerifySource `json:"sources,omitzero"`
	// Differences are the fields a matching record reports differently.
	Differences []VerifyDifference `json:"differences,omitzero"`
	// Unreported are the fields of the plan no matching record carries.
	Unreported []string `json:"unreported,omitzero"`
	// Grades counts the matching error records by observation grade.
	Grades map[model.Grade]int `json:"grades,omitzero"`
	Note   string              `json:"note,omitzero"`
	// Line is the line of the plan the injection starts on.
	Line int `json:"line"`
}

// VerifySource is the number of matching records of one source.
type VerifySource struct {
	Source  string `json:"source"`
	Records int    `json:"records"`
}

// VerifyDifference is a field in which records disagree with the plan:
// what was injected, what was recorded, and the records that say so, each
// named by its source and raw reference.
type VerifyDifference struct {
	Field   string   `json:"field"`
	Want    string   `json:"want"`
	Got     string   `json:"got"`
	Records []string `json:"records"`
	Text    string   `json:"text"`
}

// VerifyField is a field of the plan with the number of injections that
// name it and found no record that carries it.
type VerifyField struct {
	Field      string `json:"field"`
	Injections int    `json:"injections"`
}

// Verify compares a plan with the ledger of a host.
func (c *Case) Verify(ctx context.Context, h *engine.HostResult, p *injection.Plan) (*Verification, error) {
	res, err := injection.Compare(ctx, c.store, h.Host, p)
	if err != nil {
		return nil, err
	}
	v := &Verification{
		Host:   h.Host,
		Plan:   VerifyPlan{Scenario: p.Scenario, Summary: p.Summary, Method: p.Method, Meta: p.Meta, Injections: len(p.Injections)},
		Checks: []VerifyCheck{}, UnplannedErrors: res.UnplannedErrors, UnplannedOfflines: res.UnplannedOfflines, Unplanned: res.Unplanned,
	}
	if len(v.Plan.Meta) == 0 {
		v.Plan.Meta = nil
	}
	unreported, sources := map[string]int{}, map[string]int{}
	for i := range res.Checks {
		ch := &res.Checks[i]
		in := ch.Injection
		vc := VerifyCheck{
			N: i + 1, ID: in.ID, Inject: in.Inject, What: verifyWhat(in), Where: verifyWhere(in),
			Planned: in.Count, Found: ch.Found, Unreported: ch.Unreported, Grades: ch.Grades, Note: in.Note, Line: in.Line,
		}
		if len(vc.Grades) == 0 {
			vc.Grades = nil
		}
		for src, n := range ch.Sources {
			vc.Sources = append(vc.Sources, VerifySource{Source: src, Records: n})
			sources[src] += n
		}
		sort.Slice(vc.Sources, func(a, b int) bool { return vc.Sources[a].Source < vc.Sources[b].Source })
		// Several sources report one error: a field they all decode alike
		// is one difference with several records.
		for _, d := range ch.Differences {
			merged := false
			for k := range vc.Differences {
				if x := &vc.Differences[k]; x.Field == d.Field && x.Want == d.Want && x.Got == d.Got {
					x.Records, merged = append(x.Records, d.Record), true
					break
				}
			}
			if !merged {
				vc.Differences = append(vc.Differences, VerifyDifference{Field: d.Field, Want: d.Want, Got: d.Got, Records: []string{d.Record}})
			}
		}
		for k := range vc.Differences {
			d := &vc.Differences[k]
			d.Text = Messages.Text(c.Lang, "vf.diff", map[string]any{"field": d.Field, "want": d.Want, "got": d.Got, "n": len(d.Records), "records": strings.Join(d.Records, "; ")})
		}
		switch {
		case ch.OK():
			vc.Result = VerifyOK
		case in.Count == 0 && ch.Found > 0:
			vc.Result = VerifyUnexpected
		case ch.Found == 0:
			vc.Result = VerifyMissing
		case ch.Found != in.Count:
			vc.Result = VerifyCount
		default:
			vc.Result = VerifyDecode
		}
		vc.ResultText = Messages.Text(c.Lang, "vf.result."+vc.Result, map[string]any{"found": vc.Found, "planned": vc.Planned, "n": len(vc.Differences)})
		if vc.Result == VerifyOK {
			v.Passed++
		} else {
			v.Failed++
		}
		for _, f := range ch.Unreported {
			unreported[f]++
		}
		v.Checks = append(v.Checks, vc)
	}
	for src, n := range sources {
		v.Sources = append(v.Sources, VerifySource{Source: src, Records: n})
	}
	sort.Slice(v.Sources, func(a, b int) bool { return v.Sources[a].Source < v.Sources[b].Source })
	for f, n := range unreported {
		v.Unreported = append(v.Unreported, VerifyField{Field: f, Injections: n})
	}
	sort.Slice(v.Unreported, func(a, b int) bool {
		if v.Unreported[a].Injections != v.Unreported[b].Injections {
			return v.Unreported[a].Injections > v.Unreported[b].Injections
		}
		return v.Unreported[a].Field < v.Unreported[b].Field
	})
	v.OK = v.Failed == 0
	switch {
	case len(v.Checks) == 0:
		v.Summary = c.say("vf.summary.empty")
	case v.OK:
		v.Summary = c.say("vf.summary.ok", "n", len(v.Checks))
	default:
		v.Summary = c.say("vf.summary.failed", "n", len(v.Checks), "failed", v.Failed)
	}
	return v, nil
}

// verifyWhat names what an injection put into the host.
func verifyWhat(in *injection.Injection) string {
	if in.Inject == injection.InjectOffline {
		return "page offline " + in.How
	}
	what := string(in.Severity)
	if in.Scrub != "" {
		what += " " + string(in.Scrub)
	}
	return what
}

// verifyWhere names the place of an injection as the plan gives it: the
// address first, then what it says about the place inside the DIMM.
func verifyWhere(in *injection.Injection) string {
	var parts []string
	if in.Address.Valid {
		parts = append(parts, fmt.Sprintf("%#x", in.Address.V))
	}
	if in.DeviceName != "" {
		dev := in.DeviceName
		if in.DPA.Valid {
			dev += fmt.Sprintf(" dpa %#x", in.DPA.V)
		}
		parts = append(parts, dev)
	}
	if in.Label != "" {
		parts = append(parts, in.Label)
	}
	num := func(name string, o model.Opt[int], hex bool) {
		if !o.Valid {
			return
		}
		if hex {
			parts = append(parts, fmt.Sprintf("%s %#x", name, o.V))
		} else {
			parts = append(parts, fmt.Sprintf("%s%d", name, o.V))
		}
	}
	if in.Row.Valid {
		num("rank", in.Rank, false)
		num("dev", in.Device, false)
		num("bg", in.BankGroup, false)
		num("bank", in.Bank, false)
		num("row", in.Row, true)
		num("col", in.Column, true)
	}
	return strings.Join(parts, " ")
}
