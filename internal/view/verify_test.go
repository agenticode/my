package view

import (
	"reflect"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/injection"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/testcapture"
)

// verifyCase opens the ledger of the capture with firmware error records.
func verifyCase(t *testing.T, lang i18n.Lang) (*Case, *engine.HostResult) {
	t.Helper()
	path, host := testcapture.Ledger(t, testcapture.GHES)
	s, err := store.Open(ctx, path, store.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	c, err := Open(ctx, s, lang, engine.Options{})
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Host(host)
	if err != nil {
		t.Fatal(err)
	}
	return c, h
}

func readPlan(t *testing.T, path string) *injection.Plan {
	t.Helper()
	p, err := injection.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// The capture against its own plan: everything is found as planned, by
// three sources.
func TestVerifyAsPlanned(t *testing.T) {
	c, h := verifyCase(t, i18n.EN)
	v, err := c.Verify(ctx, h, readPlan(t, testcapture.PlanPath(t, testcapture.GHES)))
	if err != nil {
		t.Fatal(err)
	}
	if !v.OK || v.Passed != 23 || v.Failed != 0 || len(v.Checks) != 23 || v.Plan.Scenario != "ghes" || v.Plan.Injections != 23 || v.Host != "lab-arm" {
		t.Fatalf("verification: %+v", v)
	}
	if v.Summary.Code != "vf.summary.ok" || v.Summary.Text != "all 23 injections were found as planned" {
		t.Errorf("summary: %+v", v.Summary)
	}
	if v.UnplannedErrors != 0 || v.UnplannedOfflines != 0 || len(v.Unplanned) != 0 {
		t.Errorf("beyond the plan: %d errors, %d pages, %q", v.UnplannedErrors, v.UnplannedOfflines, v.Unplanned)
	}
	// One error of the first injection, in three sources, all grade A.
	first := v.Checks[0]
	if first.ID != "row-1" || first.Result != VerifyOK || first.ResultText != "as planned" || first.Planned != 1 || first.Found != 1 || first.Inject != injection.InjectError ||
		first.What != "CE" || first.Where != "0x100010140 DIMM 0 rank0 dev7 bg1 bank2 row 0x1a2f col 0x10" || first.Grades[model.GradeA] != 3 {
		t.Errorf("first check: %+v", first)
	}
	if !reflect.DeepEqual(first.Sources, []VerifySource{{"klog/edac", 1}, {"klog/ghes", 1}, {"rasdaemon/mc_event", 1}}) {
		t.Errorf("sources of the first check: %+v", first.Sources)
	}
	var total int
	for _, s := range v.Sources {
		total += s.Records
	}
	if len(v.Sources) != 4 || total < 3*23 {
		t.Errorf("records by source: %+v", v.Sources)
	}
}

// A plan that differs from the records in each way there is.
func TestVerifyMismatches(t *testing.T) {
	c, h := verifyCase(t, i18n.EN)
	v, err := c.Verify(ctx, h, readPlan(t, testcapture.MismatchedPlan(t)))
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "verify.json", v)
	if v.OK || v.Passed != 21 || v.Failed != 4 || v.Summary.Text != "4 of 25 injections were not found as planned" {
		t.Fatalf("verification: ok %v, %d passed, %d failed, %q", v.OK, v.Passed, v.Failed, v.Summary.Text)
	}
	by := map[string]*VerifyCheck{}
	for i := range v.Checks {
		by[v.Checks[i].ID] = &v.Checks[i]
	}
	got := map[string]string{}
	for id, ch := range by {
		if ch.Result != VerifyOK {
			got[id] = ch.Result + ": " + ch.ResultText
		}
	}
	want := map[string]string{
		"row-2": "decode: decoded differently", "row-10": "missing: not found", "row-13": "missing: not found", "ghost-1": "missing: not found",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("results\n got %v\nwant %v", got, want)
	}

	// The wrong row and column are named once each, with the three records
	// that show them.
	d := by["row-2"].Differences
	if len(d) != 2 || d[0].Field != "row" || d[0].Want != "0x1a30" || d[0].Got != "0x1a2f" || len(d[0].Records) != 3 ||
		d[1].Field != "column" || d[1].Want != "0x119" || d[1].Got != "0x118" || len(d[1].Records) != 3 {
		t.Fatalf("differences of row-2: %+v", d)
	}
	if !strings.HasPrefix(d[0].Text, "row: injected 0x1a30, recorded 0x1a2f in 3 records (rasdaemon/mc_event ") || !strings.Contains(d[0].Text, "#mc_event/2; klog/ghes ") {
		t.Errorf("text of the row difference: %q", d[0].Text)
	}
	// The injection that claims three errors has them; a page that was to
	// stay in use is in use.
	if ch := by["row-3"]; ch.Result != VerifyOK || ch.Planned != 3 || ch.Found != 3 {
		t.Errorf("row-3: %+v", ch)
	}
	if ch := by["stay-1"]; ch.Result != VerifyOK || ch.Planned != 0 || ch.Found != 0 || ch.What != "page offline hard" || ch.Where != "0x1ffff1000" {
		t.Errorf("stay-1: %+v", ch)
	}
	if ch := by["ghost-1"]; ch.Found != 0 || len(ch.Sources) != 0 || ch.Note != "never injected" || ch.Line == 0 {
		t.Errorf("ghost-1: %+v", ch)
	}

	// In Korean the field names stay, the sentences do not.
	ko, hk := verifyCase(t, i18n.KO)
	vk, err := ko.Verify(ctx, hk, readPlan(t, testcapture.MismatchedPlan(t)))
	if err != nil {
		t.Fatal(err)
	}
	if vk.Summary.Text != "주입 25건 중 4건이 계획과 다름" || !strings.HasPrefix(vk.Checks[1].Differences[0].Text, "row: 주입 0x1a30, 기록 0x1a2f, 기록 3건(") {
		t.Errorf("Korean: %q, %q", vk.Summary.Text, vk.Checks[1].Differences[0].Text)
	}
}

// A page the plan wants in use that is offline, and an empty plan.
func TestVerifyUnexpectedAndEmpty(t *testing.T) {
	c, h := verifyCase(t, i18n.EN)
	plan := readPlan(t, testcapture.PlanPath(t, testcapture.GHES))
	var offline *injection.Injection
	for i := range plan.Injections {
		if plan.Injections[i].Inject == injection.InjectOffline {
			offline = &plan.Injections[i]
			break
		}
	}
	if offline == nil {
		t.Fatal("the plan of the capture has no page offline")
	}
	offline.Count = 0
	v, err := c.Verify(ctx, h, plan)
	if err != nil {
		t.Fatal(err)
	}
	var got *VerifyCheck
	for i := range v.Checks {
		if v.Checks[i].ID == offline.ID {
			got = &v.Checks[i]
		}
	}
	if v.OK || v.Failed != 1 || got == nil || got.Result != VerifyUnexpected || got.ResultText != "offline, planned to stay in use" {
		t.Errorf("verification: %d failed, check %+v", v.Failed, got)
	}

	// Nothing planned: nothing to find, and what the host holds is beyond
	// the plan.
	v, err = c.Verify(ctx, h, &injection.Plan{Scenario: "nothing"})
	if err != nil {
		t.Fatal(err)
	}
	if !v.OK || len(v.Checks) != 0 || v.Checks == nil || v.Summary.Code != "vf.summary.empty" || v.UnplannedErrors != 26 || v.UnplannedOfflines != 2 || len(v.Unplanned) == 0 {
		t.Errorf("empty plan: %+v", v)
	}
}
