package engine

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/synth"
)

// TestScenarioFindings: the finding for every generated history, over
// twelve seeds.
func TestScenarioFindings(t *testing.T) {
	type want struct {
		grade model.VerdictGrade
		rules []string
	}
	tests := map[string][]want{
		"single-bit":     {{model.VerdictObserve, []string{RuleSoft}}},
		"row":            {{model.VerdictConsiderReplacement, []string{RuleRow}}},
		"multi-row":      {{model.VerdictConsiderReplacement, []string{RuleRow}}},
		"column":         {{model.VerdictConsiderReplacement, []string{RuleRow}}},
		"bank":           {{model.VerdictConsiderReplacement, []string{RuleRow}}},
		"device":         {{model.VerdictReplaceNow, []string{RuleUE, RuleRow}}},
		"dimm":           {{model.VerdictConsiderReplacement, []string{RuleRow}}},
		"moved-dimm":     {{model.VerdictConsiderReplacement, []string{RuleRow}}},
		"repaired":       {{model.VerdictObserve, []string{RuleQuiet}}},
		"system-channel": {{model.VerdictHoldReplacement, []string{RuleChannel}}, {model.VerdictHoldReplacement, []string{RuleChannel}}},
		"system-slot":    {{model.VerdictHoldReplacement, []string{RuleSlot}}},
		"demo":           {{model.VerdictReplaceNow, []string{RuleUE, RuleRow, RuleRecurred}}, {model.VerdictObserve, []string{RuleSoft}}},
	}
	for _, name := range synth.Names() {
		wants, ok := tests[name]
		if !ok {
			t.Errorf("scenario %s has no expected finding", name)
			continue
		}
		for seed := uint64(1); seed <= 12; seed++ {
			ds := generate(t, name, seed, nil)
			res := analyze(t, fullInput(ds), Options{})
			h := res.Hosts[0]
			worst := model.VerdictOK
			for i, tr := range ds.Truth {
				d := h.DIMM(uidOf(tr.Serial))
				if d == nil {
					t.Errorf("%s/%d: no result for the DIMM of fault %d", name, seed, i)
					continue
				}
				v := d.Verdict
				// A stray error on the same DIMM may add R-SOFT-01 to a
				// finding of the observe grade.
				rules := v.Rules
				if len(rules) == len(wants[i].rules)+1 && v.Grade == model.VerdictObserve {
					rules = without(rules, RuleSoft)
				}
				if v.Grade != wants[i].grade || !equalStrings(rules, wants[i].rules) {
					t.Errorf("%s/%d fault %d: finding = %s %v, want %s %v\n evidence: %q", name, seed, i, v.Grade, v.Rules, wants[i].grade, wants[i].rules, codes(v))
				}
				if worse(v.Grade, worst) {
					worst = v.Grade
				}
			}
			if h.Grade != worst || res.Grade() != worst || res.ExitCode() != worst.ExitCode() {
				t.Errorf("%s/%d: host grade %s, exit code %d; the worst finding is %s", name, seed, h.Grade, res.ExitCode(), worst)
			}
			// Everything else on the host is stray errors or nothing.
			for _, d := range h.DIMMs {
				if isTruth(ds, d.UID) || name == "system-slot" {
					continue
				}
				if g := d.Verdict.Grade; g != model.VerdictOK && (g != model.VerdictObserve || !equalStrings(d.Verdict.Rules, []string{RuleSoft})) {
					t.Errorf("%s/%d: %s without a generated fault has finding %s %v", name, seed, d.Slot, g, d.Verdict.Rules)
				}
			}
		}
	}
}

func without(list []string, s string) []string {
	var out []string
	for _, x := range list {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}

func isTruth(ds *synth.Dataset, uid string) bool {
	for _, tr := range ds.Truth {
		if uidOf(tr.Serial) == uid {
			return true
		}
	}
	return false
}

// summary is the grade and the rules of every finding of a host.
func summary(h *HostResult) []string {
	var out []string
	for _, d := range h.DIMMs {
		out = append(out, d.Slot+" "+d.UID+" "+string(d.Verdict.Grade)+" "+strings.Join(d.Verdict.Rules, ","))
	}
	sort.Strings(out)
	return out
}

// TestFindingsFromLedgers: a ledger gives the findings the hand-over of the
// same history gives, and the findings carry what a finding must carry.
func TestFindingsFromLedgers(t *testing.T) {
	for _, name := range []string{"demo", "system-channel", "system-slot", "moved-dimm"} {
		ds := generate(t, name, 1, nil)
		s := sharedLedger(t, name, 1, "")
		res, err := Run(ctx, s, Options{Now: computedAt})
		if err != nil {
			t.Fatal(err)
		}
		direct := analyze(t, fullInput(ds), Options{})
		if got, want := summary(res.Hosts[0]), summary(direct.Hosts[0]); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: findings from the ledger\n %q\nfrom the hand-over\n %q", name, got, want)
		}
		for _, d := range res.Hosts[0].DIMMs {
			v := d.Verdict
			if v.Label != model.VerdictLabel || v.EngineVersion != Version || v.Host != d.Host || v.DIMMUID != d.UID || v.Slot != d.Slot || !v.ComputedAt.Equal(computedAt) {
				t.Errorf("%s %s: incomplete finding %+v", name, d.Slot, v)
			}
			if (v.Grade == model.VerdictOK) != (len(v.Rules) == 0) {
				t.Errorf("%s %s: grade %s with rules %v", name, d.Slot, v.Grade, v.Rules)
			}
			sum := 0
			for _, f := range v.ConfidenceFactors {
				sum += f.Delta
			}
			if clamp := min(max(sum, 5), 95); v.Confidence != clamp || len(v.ConfidenceFactors) == 0 {
				t.Errorf("%s %s: confidence %d, factors sum to %d", name, d.Slot, v.Confidence, sum)
			}
			for _, e := range v.Evidence {
				// Every statement of evidence belongs to a rule that is
				// listed, and every event it rests on can be traced to the
				// original record.
				if !has(v.Rules, e.Rule) || e.Text == "" || e.Code == "" {
					t.Errorf("%s %s: evidence %+v", name, d.Slot, e)
				}
				if e.Total < len(e.Records) || len(e.Records) > res.Config.MaxEvidenceRecords+1 {
					t.Errorf("%s %s: %d records listed of %d", name, d.Slot, len(e.Records), e.Total)
				}
				for _, r := range e.Records {
					if r.ID == 0 || r.RawRef == "" || !strings.HasPrefix(r.RawRef, "synth:"+name+"#") {
						t.Errorf("%s %s: record without its origin: %+v", name, d.Slot, r)
					}
				}
			}
		}
	}
}

// TestFindingsOnPlatforms: the demo as three platforms report it. The
// grade does not depend on the platform; how much stands behind it does.
func TestFindingsOnPlatforms(t *testing.T) {
	ds := generate(t, "demo", 1, nil)
	uid := uidOf(ds.Truth[0].Serial)
	conf := map[synth.Flavor]int{}
	for _, fl := range synth.Flavors {
		res, err := Run(ctx, sharedLedger(t, "demo", 1, fl), Options{Now: computedAt})
		if err != nil {
			t.Fatal(err)
		}
		h := res.Hosts[0]
		d := h.DIMM(uid)
		v := d.Verdict
		// The repair at boot is in no file of the host, so R-REP-02 has
		// nothing to go on.
		wantVerdict(t, v, model.VerdictReplaceNow, RuleUE, RuleRow)
		conf[fl] = v.Confidence
		if h.Grade != model.VerdictReplaceNow || res.ExitCode() != 3 || h.DIMMs[0] != d {
			t.Errorf("%s: host grade %s, exit code %d", fl, h.Grade, res.ExitCode())
		}
		miss := missingCodes(v)
		switch fl {
		case synth.FlavorIntel:
			if !has(miss, "missing.device") || !has(miss, "missing.bits") || has(miss, "missing.rowcol") || has(miss, "missing.scrub") {
				t.Errorf("%s: missing = %q", fl, miss)
			}
		case synth.FlavorAMD:
			if !has(miss, "missing.rowcol") || !has(miss, "missing.scrub") || has(miss, "missing.device") {
				t.Errorf("%s: missing = %q", fl, miss)
			}
			if d.Faults[0].Mode != model.ModeRow || !d.Faults[0].Estimated || !has(checkCodes(v), "weakens check.estimated") {
				t.Errorf("%s: fault %s, checks %q", fl, faultString(d.Faults[0]), checkCodes(v))
			}
		case synth.FlavorGHES:
			if has(miss, "missing.device") || has(miss, "missing.bits") || has(miss, "missing.rowcol") {
				t.Errorf("%s: missing = %q", fl, miss)
			}
			// A CPER record names one failing bit, so two data lines in
			// one record do not show.
			if has(codes(v), "R-ROW-03 row.multi_dq") || !d.Features.Lines.Known {
				t.Errorf("%s: evidence = %q, data lines %+v", fl, codes(v), d.Features.Lines)
			}
		}
		// A scan reads the inventory once: earlier errors are taken to be
		// the DIMM's.
		if !has(checkCodes(v), "open check.assumed") {
			t.Errorf("%s: checks = %q", fl, checkCodes(v))
		}
		for _, m := range []string{"missing.ecs", "missing.temperature"} {
			if !has(miss, m) {
				t.Errorf("%s: missing = %q, want %s", fl, miss, m)
			}
		}
		// The neighbour with the weak cell.
		n := h.DIMM(uidOf(ds.Truth[1].Serial))
		wantVerdict(t, n.Verdict, model.VerdictObserve, RuleSoft)
		// The DIMMs without errors.
		for _, o := range h.DIMMs[2:] {
			if o.Verdict.Grade != model.VerdictOK || o.Verdict.Confidence != 85 || len(o.Verdict.Missing) != 0 {
				t.Errorf("%s %s: %s, confidence %d, missing %q", fl, o.Slot, o.Verdict.Grade, o.Verdict.Confidence, missingCodes(o.Verdict))
			}
		}
	}
	if conf[synth.FlavorAMD] > conf[synth.FlavorIntel] {
		t.Errorf("confidence: %v", conf)
	}
}

// TestGradesAndExitCodes: the grade of a host is the grade of its worst
// DIMM, with hold replacement above the others, and the exit code follows.
func TestGradesAndExitCodes(t *testing.T) {
	build := func(grades ...model.VerdictGrade) *Result {
		b := ruleBuilder()
		b.slot("A1", 0, 0)
		b.slot("A2", 0, 1)
		for i, g := range grades {
			uid := string(rune('a' + i))
			slot := "S-" + uid
			b.place(uid, slot, t0, asOf)
			switch g {
			case model.VerdictObserve:
				b.add(b.in1(uid, slot, t0.Add(10*day), cellA))
			case model.VerdictConsiderReplacement:
				b.add(severity(b.in1(uid, slot, t0.Add(10*day), cellA), model.SevRecoverable, model.ScrubPatrol))
			case model.VerdictReplaceNow:
				b.add(severity(b.in1(uid, slot, t0.Add(10*day), cellA), model.SevUE, model.ScrubDemand))
			case model.VerdictHoldReplacement:
				b.place(uid+"1", "A1", t0, asOf)
				b.place(uid+"2", "A2", t0, asOf)
				b.scatter(uid+"1", "A1", 10, t0.Add(10*day), t0.Add(20*day))
				b.scatter(uid+"2", "A2", 10, t0.Add(10*day), t0.Add(20*day))
			}
		}
		return analyze(t, b.in, Options{AsOf: asOf})
	}
	ok, obs, con, rep, hold := model.VerdictOK, model.VerdictObserve, model.VerdictConsiderReplacement, model.VerdictReplaceNow, model.VerdictHoldReplacement
	for _, tt := range []struct {
		grades []model.VerdictGrade
		want   model.VerdictGrade
		code   int
	}{
		{[]model.VerdictGrade{ok, ok}, ok, 0},
		{[]model.VerdictGrade{ok, obs}, obs, 1},
		{[]model.VerdictGrade{obs, con, ok}, con, 2},
		{[]model.VerdictGrade{con, rep, obs}, rep, 3},
		{[]model.VerdictGrade{rep, hold, con}, hold, 4},
		{[]model.VerdictGrade{hold}, hold, 4},
	} {
		res := build(tt.grades...)
		h := res.Host("h1")
		if h.Grade != tt.want || res.Grade() != tt.want || res.ExitCode() != tt.code {
			t.Errorf("%v: host grade %s, exit code %d; want %s, %d", tt.grades, h.Grade, res.ExitCode(), tt.want, tt.code)
		}
		// The findings are ordered worst first.
		for i := 1; i < len(h.DIMMs); i++ {
			if worse(h.DIMMs[i].Verdict.Grade, h.DIMMs[i-1].Verdict.Grade) {
				t.Errorf("%v: %s (%s) is listed after %s (%s)", tt.grades, h.DIMMs[i].Slot, h.DIMMs[i].Verdict.Grade, h.DIMMs[i-1].Slot, h.DIMMs[i-1].Verdict.Grade)
			}
		}
	}
	// A DIMM that was taken out keeps its finding and no longer counts for
	// the host.
	b := ruleBuilder()
	b.place("old", "A1", t0, t0.Add(20*day))
	b.place("new", "A1", t0.Add(21*day), asOf)
	b.add(severity(b.in1("old", "A1", t0.Add(10*day), cellA), model.SevUE, model.ScrubDemand))
	res := analyze(t, b.in, Options{AsOf: asOf})
	h := res.Host("h1")
	if v := h.DIMM("old").Verdict; v.Grade != rep || v.NextAction.Code != "next.removed" {
		t.Errorf("DIMM that was taken out: %s, next %q", v.Grade, v.NextAction.Code)
	}
	if h.Grade != ok || res.ExitCode() != 0 {
		t.Errorf("host grade %s, exit code %d after the replacement", h.Grade, res.ExitCode())
	}
	// An empty ledger is ok.
	if res := analyze(t, NewInput(), Options{}); res.Grade() != ok || res.ExitCode() != 0 || len(res.Hosts) != 0 {
		t.Errorf("empty input: %s", res.Grade())
	}
	// The rules of a finding are ordered by grade, hold first.
	want := []string{RuleChannel, RuleSlot, RuleReseat, RuleMapping, RuleUE, RuleFirmware, RuleRepairGone, RuleRow, RuleOffline, RuleScrub, RuleRecurred, RuleSoft, RuleQuiet}
	ids := make([]string, len(Rules))
	for i, r := range Rules {
		ids[i] = r.ID
	}
	sort.Slice(ids, func(i, j int) bool { return rulePosition(ids[i]) < rulePosition(ids[j]) })
	if !equalStrings(ids, want) || len(Rules) != 13 || rulePosition("R-NONE-00") <= rulePosition(RuleQuiet) {
		t.Errorf("rule order = %v", ids)
	}
	if r, ok := RuleByID(RuleRow); !ok || r.Grade != con {
		t.Errorf("RuleByID(%s) = %+v", RuleRow, r)
	}
	if _, ok := RuleByID("R-NONE-00"); ok {
		t.Error("unknown rule found")
	}
}

// TestMissingData: what a finding did not have is stated with it.
func TestMissingData(t *testing.T) {
	scan := func(b *builder, sources ...model.Source) {
		b.in.Scans = []model.Scan{
			{ID: 1, Host: "h1", Kind: model.ScanLive, StartedAt: t0.Add(day)},
			{ID: 2, Host: "h1", Kind: model.ScanLive, StartedAt: asOf},
		}
		// The sources of the earlier scan do not matter any more.
		b.in.Sources = []model.Source{{ID: 9, ScanID: 1, Kind: "rasdaemon", Status: model.SourceError, Detail: "disk I/O error"}}
		for i := range sources {
			sources[i].ID, sources[i].ScanID = int64(i+1), 2
			b.in.Sources = append(b.in.Sources, sources[i])
		}
	}

	t.Run("sources that could not be read", func(t *testing.T) {
		b := newBuilderWith("d1")
		b.observedSince(t0)
		scan(b,
			model.Source{Kind: "rasdaemon", Origin: "/var/lib/rasdaemon/ras-mc_event.db", Status: model.SourceUnreadable, Detail: "permission denied; readable as root"},
			model.Source{Kind: "klog", Status: model.SourcePartial, Detail: "the log starts after boot"},
			model.Source{Kind: "edac-sysfs", Status: model.SourceAbsent, Detail: "not present"},
			model.Source{Kind: "smbios", Status: model.SourceOK},
			model.Source{Kind: "hostinfo", Status: model.SourceAbsent},
			model.Source{Kind: "nvidia-smi", Status: model.SourceEmpty},
		)
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictOK)
		var texts []string
		for _, m := range v.Missing {
			texts = append(texts, m.Text)
		}
		want := []string{
			"rasdaemon: not readable (permission denied; readable as root)",
			"klog: partly read (the log starts after boot)",
			"edac-sysfs: not present",
		}
		if !reflect.DeepEqual(texts, want) {
			t.Errorf("missing = %q\nwant %q", texts, want)
		}
		// "ok" with sources missing is worth less.
		if v.Confidence != 70 {
			t.Errorf("confidence = %d, want 85 - 15", v.Confidence)
		}
	})

	t.Run("no source of error records was read", func(t *testing.T) {
		b := newBuilderWith("d1")
		scan(b, model.Source{Kind: "rasdaemon", Status: model.SourceUnreadable}, model.Source{Kind: "smbios", Status: model.SourceOK})
		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictOK)
		if got := missingCodes(v); !equalStrings(got, []string{"missing.source", "missing.no_errors"}) {
			t.Errorf("missing = %q", got)
		}
		if v.Confidence > 40 || ConfidenceLevel(v.Confidence) != "low" {
			t.Errorf("confidence = %d", v.Confidence)
		}
	})

	t.Run("what the records of a DIMM do not say", func(t *testing.T) {
		b := ruleBuilder()
		b.slot("S-d1", 0, 0) // SMBIOS knows the slot
		// A located error without device and bits, an error known by
		// address, one known by its rank, counter growth and an undated one.
		b.ce("d1", t0.Add(40*day), loc{0, -1, 1, 2, 5000, 640})
		byAddr := b.event("d1", t0.Add(41*day), loc{0, -1, -1, -1, -1, -1})
		byAddr.OsPhysicalAddress = model.Some(uint64(0x1234000))
		b.add(byAddr)
		cnt := b.event("d1", t0.Add(42*day), loc{-1, -1, -1, -1, -1, -1})
		cnt.Kind, cnt.Count, cnt.WindowStart = model.KindCounter, 3, t0.Add(41*day)
		b.add(cnt)
		undated := b.event("d1", time.Unix(700, 0).UTC(), loc{0, -1, 1, 2, 5000, 640})
		undated.TimeQuality = model.TimeRelative
		b.add(undated)
		b.add(severity(b.event("d1", t0.Add(43*day), loc{0, -1, 1, 2, 5000, 640}), model.SevUE, ""))
		// Errors of the host that belong to no DIMM.
		loose := b.event("", t0.Add(44*day), cellA)
		loose.Slot, loose.DIMMLink = "", ""
		b.add(loose)
		amb := b.event("", t0.Add(45*day), cellA)
		amb.DIMMLink = model.LinkAmbiguous
		amb.Slot = "S-d1"
		b.add(amb)
		b.in.DIMMs = []model.DIMM{{UID: "d1", Identity: model.IdentitySlot, MemoryType: "DDR5"}}

		v := verdictOf(t, b, asOf, "d1")
		wantVerdict(t, v, model.VerdictReplaceNow, RuleUE)
		want := []string{"missing.serial", "missing.unplaced", "missing.ambiguous", "missing.rowcol", "missing.location", "missing.device", "missing.bits",
			"missing.scrub", "missing.dates", "missing.ecs", "missing.temperature"}
		if got := missingCodes(v); !equalStrings(got, want) {
			t.Errorf("missing = %q\nwant %q", got, want)
		}
		text := map[string]string{}
		for _, m := range v.Missing {
			text[m.Code] = m.Text
		}
		for code, want := range map[string]string{
			"missing.rowcol":    "1 error is known by physical address only (grade B): no row or column",
			"missing.location":  "3 errors are known as a count only (grade C)",
			"missing.dates":     "1 error carries seconds since boot and no date: left out of every trend",
			"missing.unplaced":  "1 error of this host names no slot and is counted for no DIMM",
			"missing.ambiguous": "1 error in this slot falls between two DIMMs whose swap time is unknown and is counted for neither",
		} {
			if text[code] != want {
				t.Errorf("%s = %q, want %q", code, text[code], want)
			}
		}
		// Without a serial number the finding is worth less.
		noSerial := false
		for _, f := range v.ConfidenceFactors {
			noSerial = noSerial || (f.Code == "conf.no_serial" && f.Delta == -10)
		}
		if !noSerial {
			t.Errorf("confidence factors = %+v", v.ConfidenceFactors)
		}
	})

	t.Run("no inventory at all", func(t *testing.T) {
		b := ruleBuilder()
		b.ce("d1", t0.Add(40*day), cellA)
		v := verdictOf(t, b, asOf, "d1")
		if got := missingCodes(v); !has(got, "missing.inventory") || has(got, "missing.unmapped") || has(got, "missing.serial") || has(got, "missing.ecs") {
			t.Errorf("missing = %q", got)
		}
	})

	t.Run("an EDAC position that is not tied to an SMBIOS slot", func(t *testing.T) {
		b := ruleBuilder()
		b.slot("CPU0_A1", 0, 0) // SMBIOS knows this slot, and not the one the errors are in
		e := b.event("s-placeholder", t0.Add(40*day), cellA)
		e.Slot, e.DIMMLink = "mc0 2:0", model.LinkSlot
		b.add(e)
		v := verdictOf(t, b, asOf, "s-placeholder")
		if got := missingCodes(v); !has(got, "missing.unmapped") || has(got, "missing.serial") || has(got, "missing.inventory") {
			t.Errorf("missing = %q", got)
		}
	})

	t.Run("too little history for a trend", func(t *testing.T) {
		b := newBuilder()
		b.observedSince(asOf.Add(-10 * day))
		b.rowFault("d1", 4, 5, asOf.Add(-3*day), asOf)
		v := verdictOf(t, b, asOf, "d1")
		var st *model.Statement
		for i := range v.Missing {
			if v.Missing[i].Code == "missing.history" {
				st = &v.Missing[i]
			}
		}
		if st == nil || st.Text != "the ledger covers 3 days before the recent window; a trend needs 7" {
			t.Errorf("missing = %q", missingCodes(v))
		}
		// Without a trend the fault is judged by its places alone.
		if has(v.Rules, RuleRow) {
			t.Errorf("rules = %v", v.Rules)
		}
	})
}

// TestConfidence: what raises and lowers the confidence of a finding.
func TestConfidence(t *testing.T) {
	factors := func(v *model.Verdict) map[string]int {
		out := map[string]int{}
		for _, f := range v.ConfidenceFactors {
			out[f.Code] = f.Delta
		}
		return out
	}
	// An uncorrected error on a DIMM with a serial number, alone on its
	// channel, never moved.
	b := channelBuilder("A1")
	b.in.Slots[1].Populated = model.Some(false)
	b.add(severity(b.in1("dA1", "A1", t0.Add(40*day), cellA), model.SevUE, model.ScrubDemand))
	v := verdictOf(t, b, asOf, "dA1")
	if got := factors(v); !reflect.DeepEqual(got, map[string]int{"conf.rule": 90, "conf.open": -4}) || v.Confidence != 86 {
		t.Errorf("factors = %v, confidence %d", got, v.Confidence)
	}
	if v.ConfidenceFactors[0].Text != "the grade rests on R-UE-01" || ConfidenceLevel(v.Confidence) != "high" {
		t.Errorf("first factor %q, level %s", v.ConfidenceFactors[0].Text, ConfidenceLevel(v.Confidence))
	}
	// A clean neighbour on the channel supports it.
	b = channelBuilder("A1", "A2")
	b.add(severity(b.in1("dA1", "A1", t0.Add(40*day), cellA), model.SevUE, model.ScrubDemand))
	v = verdictOf(t, b, asOf, "dA1")
	if got := factors(v); got["conf.supports"] != 10 || v.Confidence != 95 {
		t.Errorf("with a clean neighbour: factors = %v, confidence %d", got, v.Confidence)
	}
	// A neighbour in trouble speaks against it.
	b = channelBuilder("A1", "A2")
	b.add(severity(b.in1("dA1", "A1", t0.Add(40*day), cellA), model.SevUE, model.ScrubDemand))
	b.scatter("dA2", "A2", 30, t0.Add(30*day), t0.Add(50*day))
	v = verdictOf(t, b, asOf, "dA1")
	if got := factors(v); got["conf.weakens"] != -10 || !has(checkCodes(v), "weakens check.neighbors_active") {
		t.Errorf("with a failing neighbour: factors = %v, checks %q", got, checkCodes(v))
	}
	for c, want := range map[int]string{100: "high", 75: "high", 74: "medium", 50: "medium", 49: "low", 0: "low"} {
		if got := ConfidenceLevel(c); got != want {
			t.Errorf("ConfidenceLevel(%d) = %s, want %s", c, got, want)
		}
	}
}

// TestFirmwareChangeIsNoted: errors that begin right after a BIOS or kernel
// change may come from the change.
func TestFirmwareChangeIsNoted(t *testing.T) {
	build := func(bios2, kernel2 string, first time.Duration) *model.Verdict {
		b := newBuilder()
		b.in.Boots = []model.Boot{
			{Host: "h1", BootID: "a", StartedAt: t0, StartQuality: model.TimeExact, BIOS: "2.4.4", Kernel: "5.14.0-570"},
			{Host: "h1", BootID: "b", StartedAt: t0.Add(30 * day), StartQuality: model.TimeExact, BIOS: bios2, Kernel: kernel2},
		}
		b.rowFault("d1", 4, 5, t0.Add(first), asOf)
		return verdictOf(t, b, asOf, "d1")
	}
	find := func(v *model.Verdict) *model.Check {
		for i := range v.CounterEvidence {
			if v.CounterEvidence[i].Code == "check.after_change" {
				return &v.CounterEvidence[i]
			}
		}
		return nil
	}
	c := find(build("2.5.1", "5.14.0-570", 32*day))
	if c == nil || c.Effect != model.EffectWeakens ||
		c.Text != "the first error came 2 days after the BIOS changed on 2026-10-31 (2.4.4 to 2.5.1), with none in the 30 days of history before" {
		t.Errorf("BIOS change: %+v", c)
	}
	if c := find(build("2.4.4", "6.12.0-55", 32*day)); c == nil || c.Args["what"] != "@word.kernel" {
		t.Errorf("kernel change: %+v", c)
	}
	if c := find(build("2.4.4", "5.14.0-570", 32*day)); c != nil {
		t.Errorf("no change: %+v", c)
	}
	// Errors that were there before the change did not come from it.
	if c := find(build("2.5.1", "5.14.0-570", 10*day)); c != nil {
		t.Errorf("errors before the change: %+v", c)
	}
}

// TestLookingBack: the finding as of an earlier time is what the ledger
// would have given then. That is how a finding is compared with what
// happened afterwards.
func TestLookingBack(t *testing.T) {
	ds := generate(t, "demo", 1, nil)
	in := fullInput(ds)
	uid := uidOf(ds.Truth[0].Serial)
	at := func(y int, m time.Month, d int) *model.Verdict {
		return analyze(t, in, Options{AsOf: time.Date(y, m, d, 0, 0, 0, 0, time.UTC)}).Hosts[0].DIMM(uid).Verdict
	}
	// Before the fault began.
	wantVerdict(t, at(2026, 9, 20), model.VerdictOK)
	// Growing, before the repair of 28 October.
	wantVerdict(t, at(2026, 10, 25), model.VerdictConsiderReplacement, RuleRow)
	// After the repair the errors are back; the uncorrected error of
	// 12 November has not happened yet.
	wantVerdict(t, at(2026, 11, 11), model.VerdictConsiderReplacement, RuleRow, RuleRecurred)
	// The end of the history.
	wantVerdict(t, at(2026, 11, 13), model.VerdictReplaceNow, RuleUE, RuleRow, RuleRecurred)
}

// TestSave: findings and faults are stored in the ledger and replace what
// an earlier run stored.
func TestSave(t *testing.T) {
	ds := generate(t, "demo", 1, nil)
	s := ledgerOf(t, ds)
	cfg := DefaultConfig()
	cfg.StormCount = 500
	res, err := Run(ctx, s, Options{Now: computedAt, Config: &cfg})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 { // the second save replaces the first
		if err := res.Save(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	host := res.Hosts[0]
	verdicts, err := s.Verdicts(ctx, host.Host)
	if err != nil || len(verdicts) != 16 {
		t.Fatalf("%d stored verdicts, %v", len(verdicts), err)
	}
	byUID := map[string]model.Verdict{}
	for _, v := range verdicts {
		byUID[v.DIMMUID] = v
	}
	for _, d := range host.DIMMs {
		got := byUID[d.UID]
		want := *d.Verdict
		if got.ID == 0 || got.ID != want.ID {
			t.Errorf("%s: stored ID %d, result ID %d", d.Slot, got.ID, want.ID)
		}
		// Arguments come back from JSON as float64 and []any; the texts
		// and everything else are the same.
		gj, _ := json.Marshal(got)
		wj, _ := json.Marshal(want)
		if string(gj) != string(wj) {
			t.Errorf("%s: stored verdict\n %s\nresult\n %s", d.Slot, gj, wj)
		}
	}
	faults, err := s.Faults(ctx, host.Host)
	if err != nil || len(faults) != 2 {
		t.Fatalf("%d stored faults, %v", len(faults), err)
	}
	d := host.DIMMs[0]
	var row *model.Fault
	for i := range faults {
		if faults[i].DIMMUID == d.UID {
			row = &faults[i]
		}
	}
	f := d.Faults[0]
	if row == nil || row.ID != f.ID || row.Mode != model.ModeRow || row.ErrorCount != 1232 || len(row.EventIDs) != 1232 || row.EngineVersion != Version ||
		row.RowMin != model.Some(0x1a2f) || row.Confidence != f.Confidence || !row.FirstSeen.Equal(f.FirstSeen) {
		t.Errorf("stored fault = %+v", row)
	}
	// What the findings were computed with is on record.
	meta, err := s.Meta(ctx, MetaKey(host.Host))
	if err != nil {
		t.Fatal(err)
	}
	var rec struct {
		EngineVersion string            `json:"engine_version"`
		ComputedAt    time.Time         `json:"computed_at"`
		AsOf          time.Time         `json:"as_of"`
		Grade         string            `json:"grade"`
		Thresholds    map[string]string `json:"thresholds"`
	}
	if err := json.Unmarshal([]byte(meta), &rec); err != nil || rec.EngineVersion != Version || !rec.ComputedAt.Equal(computedAt) || !rec.AsOf.Equal(ds.Scenario.End) ||
		rec.Grade != "replace-now" || !reflect.DeepEqual(rec.Thresholds, map[string]string{"storm.count": "500"}) {
		t.Errorf("meta = %s (%v)", meta, err)
	}

	// Saving one host leaves the findings of another alone.
	err = s.Update(ctx, func(w *store.Writer) error {
		return w.InsertVerdict(&model.Verdict{Host: "other-host", Grade: model.VerdictObserve, ComputedAt: computedAt})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := res.Save(ctx, s); err != nil {
		t.Fatal(err)
	}
	if other, _ := s.Verdicts(ctx, "other-host"); len(other) != 1 {
		t.Errorf("%d verdicts of the other host", len(other))
	}
	if again, _ := s.Verdicts(ctx, host.Host); len(again) != 16 {
		t.Errorf("%d verdicts after saving again", len(again))
	}

	// A ledger that cannot be written reports that.
	path := s.Path()
	s.Close()
	ro, err := store.Open(ctx, path, store.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if err := res.Save(ctx, ro); err == nil {
		t.Error("saving to a read-only ledger returned no error")
	}
	// Reading and analysing a read-only ledger works.
	again, err := Run(ctx, ro, Options{Now: computedAt, Config: &cfg})
	if err != nil || !reflect.DeepEqual(summary(again.Hosts[0]), summary(host)) {
		t.Errorf("analysis of the read-only ledger: %v", err)
	}
	// A closed ledger is an error.
	ro.Close()
	if _, err := Run(ctx, ro, Options{}); err == nil {
		t.Error("Run on a closed ledger returned no error")
	}
	if err := res.AttachRawRefs(ctx, ro); err != nil {
		// Every reference is filled in already: nothing is looked up.
		t.Errorf("AttachRawRefs with nothing to look up: %v", err)
	}
}

// TestSaveGPUFinding: a finding about GPU memory is stored without a DIMM.
func TestSaveGPUFinding(t *testing.T) {
	s := newStore(t)
	err := s.Update(ctx, func(w *store.Writer) error {
		if err := w.UpsertHost(&model.Host{Name: "h1"}); err != nil {
			return err
		}
		_, err := w.InsertGPUMem(&model.GPUMem{Host: "h1", ObservedAt: t0, Kind: model.GPUXID, GPUSerial: "1320921000977", XID: model.Some(64),
			Source: "klog/xid", RawRef: "dmesg#L88", Fingerprint: "x1"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(ctx, s, Options{Now: computedAt})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode() != 3 {
		t.Fatalf("exit code %d, hosts %+v", res.ExitCode(), res.Hosts)
	}
	if err := res.Save(ctx, s); err != nil {
		t.Fatal(err)
	}
	vs, err := s.Verdicts(ctx, "h1")
	if err != nil || len(vs) != 1 || vs[0].GPU != "1320921000977" || vs[0].DIMMUID != "" || vs[0].Grade != model.VerdictReplaceNow ||
		vs[0].Evidence[0].Records[0] != (model.RecordRef{Kind: model.RecordGPU, ID: 1, RawRef: "dmesg#L88"}) {
		t.Errorf("stored GPU verdict = %+v, %v", vs, err)
	}
}

// TestInvalidThresholds: thresholds outside their bounds are refused before
// anything is analysed.
func TestInvalidThresholds(t *testing.T) {
	for _, mut := range []func(*Config){
		func(c *Config) { c.RecentDays = 0 },
		func(c *Config) { c.LineChance = 2 },
		func(c *Config) { c.RepeatCount = 1 },
		func(c *Config) { c.RiseRatio = 0.5 },
		func(c *Config) { *c = Config{} },
	} {
		cfg := DefaultConfig()
		mut(&cfg)
		if _, err := Analyze(NewInput(), Options{Config: &cfg}); err == nil || !strings.Contains(err.Error(), "thresholds") {
			t.Errorf("Analyze with %+v: err = %v", cfg, err)
		}
	}
}
