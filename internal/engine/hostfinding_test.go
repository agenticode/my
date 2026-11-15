package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
)

// loose returns an error of host h1 that names no DIMM and no slot: what a
// machine check gives on a host without an EDAC driver.
func (b *builder) loose(at time.Time, sev model.Severity, addr uint64) *model.Event {
	e := b.event("", at, loc{-1, -1, -1, -1, -1, -1})
	e.DIMMUID, e.DIMMLink, e.Slot = "", "", ""
	e.ErrorSeverity = sev
	if addr != 0 {
		e.OsPhysicalAddress = model.Some(addr)
	}
	return e
}

// offline adds a page offline of host h1 that is tied to no DIMM.
func (b *builder) offline(at time.Time, addr uint64, mut func(*model.Action)) {
	b.in.host("h1")
	b.act("", model.ActionPageOffline, at, func(a *model.Action) {
		a.Slot, a.DIMMUID, a.Method = "", "", "soft"
		a.OsPhysicalAddress = model.Some(addr)
		if mut != nil {
			mut(a)
		}
	})
}

func hostOf(t *testing.T, b *builder) *HostResult {
	t.Helper()
	h := analyze(t, b.in, Options{AsOf: asOf}).Host("h1")
	if h == nil {
		t.Fatal("no result for host h1")
	}
	return h
}

func TestHostFinding(t *testing.T) {
	t.Run("a host without loose records has none", func(t *testing.T) {
		b := ruleBuilder()
		b.ce("d1", t0.Add(10*day), cellA)
		if h := hostOf(t, b); h.Loose != nil || h.Grade != model.VerdictObserve {
			t.Errorf("host finding %+v, grade %s", h.Loose, h.Grade)
		}
	})

	t.Run("uncorrected errors that name no DIMM hold replacement", func(t *testing.T) {
		b := ruleBuilder()
		b.add(b.loose(t0.Add(10*day), model.SevUE, 0))
		b.add(b.loose(t0.Add(12*day), model.SevUE, 0x1234000))
		b.add(b.loose(t0.Add(11*day), model.SevFatal, 0))
		h := hostOf(t, b)
		v := h.Loose
		if v == nil {
			t.Fatal("no host finding")
		}
		wantVerdict(t, v, model.VerdictHoldReplacement, RuleHostUE)
		if h.Grade != model.VerdictHoldReplacement || v.DIMMUID != "" || v.Slot != "" || v.GPU != "" || v.Host != "h1" {
			t.Errorf("host grade %s, finding %+v", h.Grade, v)
		}
		e := evidenceOf(t, v, RuleHostUE)
		if e.Code != "host.ue_located" || e.Text != "3 uncorrected errors tied to no DIMM, the latest on 2026-10-13 00:00 UTC at address 0x1234000" || e.Total != 3 || len(e.Records) != 3 {
			t.Errorf("evidence = %+v", e)
		}
		if got := Text(i18n.KO, e.Statement); got != "DIMM 미확인 UE 3건, 최근 2026-10-13 00:00 UTC, address 0x1234000" {
			t.Errorf("Korean: %q", got)
		}
		if !has(checkCodes(v), "open check.host_no_place") || v.NextAction.Code != "next.host_ue" || v.Confidence != 85 {
			t.Errorf("checks %v, next %q, confidence %d", checkCodes(v), v.NextAction.Code, v.Confidence)
		}
	})

	t.Run("an uncorrected error without an address", func(t *testing.T) {
		b := ruleBuilder()
		b.add(b.loose(t0.Add(10*day), model.SevUE, 0))
		if e := evidenceOf(t, hostOf(t, b).Loose, RuleHostUE); e.Code != "host.ue" || e.Text != "1 uncorrected error tied to no DIMM, the latest on 2026-10-11 00:00 UTC" {
			t.Errorf("evidence = %+v", e)
		}
	})

	t.Run("corrected errors that name no DIMM are watched", func(t *testing.T) {
		b := ruleBuilder()
		for k := range 7 {
			b.add(b.loose(t0.Add(time.Duration(10+k)*day), model.SevCE, uint64(0x1000*(k+1))))
		}
		h := hostOf(t, b)
		wantVerdict(t, h.Loose, model.VerdictObserve, RuleHostCE)
		e := evidenceOf(t, h.Loose, RuleHostCE)
		if e.Code != "host.ce" || e.Text != "7 corrected errors tied to no DIMM between 2026-10-11 and 2026-10-17" || h.Grade != model.VerdictObserve {
			t.Errorf("evidence = %+v, host grade %s", e, h.Grade)
		}
		if h.Loose.NextAction.Code != "next.host_ce" || h.Loose.Confidence != 70 {
			t.Errorf("next %q, confidence %d", h.Loose.NextAction.Code, h.Loose.Confidence)
		}
	})

	t.Run("corrected errors of one day, and without dates", func(t *testing.T) {
		b := ruleBuilder()
		b.add(b.loose(t0.Add(10*day), model.SevCE, 0))
		b.add(b.loose(t0.Add(10*day+time.Hour), model.SevCE, 0))
		if e := evidenceOf(t, hostOf(t, b).Loose, RuleHostCE); e.Code != "host.ce_day" || e.Text != "2 corrected errors tied to no DIMM on 2026-10-11" {
			t.Errorf("one day: %+v", e)
		}
		b = ruleBuilder()
		e := b.loose(time.Unix(90, 0).UTC(), model.SevCE, 0)
		e.TimeQuality = model.TimeRelative
		b.add(e)
		v := hostOf(t, b).Loose
		if e := evidenceOf(t, v, RuleHostCE); e.Code != "host.ce_undated" || !has(missingCodes(v), "missing.dates") {
			t.Errorf("without dates: %+v, missing %v", e, missingCodes(v))
		}
	})

	t.Run("pages taken offline that name no DIMM are watched", func(t *testing.T) {
		b := ruleBuilder()
		b.offline(t0.Add(20*day), 0x5000, nil)
		b.offline(t0.Add(21*day), 0x6000, nil)
		// The same page from a second source is one page.
		b.offline(t0.Add(21*day+time.Second), 0x6000, nil)
		// An offline that failed took no page away.
		b.offline(t0.Add(22*day), 0x7000, func(a *model.Action) { a.Result = model.ResultFailed })
		h := hostOf(t, b)
		wantVerdict(t, h.Loose, model.VerdictObserve, RuleHostCE)
		e := evidenceOf(t, h.Loose, RuleHostCE)
		if e.Code != "host.pages" || e.Text != "2 pages taken offline and tied to no DIMM, the last on 2026-10-22" || e.Total != 3 || h.Grade != model.VerdictObserve {
			t.Errorf("evidence = %+v, host grade %s", e, h.Grade)
		}
		for _, r := range e.Records {
			if r.Kind != model.RecordAction || r.RawRef == "" {
				t.Errorf("record %+v", r)
			}
		}
	})

	t.Run("a page a program asked to take offline weakens the finding", func(t *testing.T) {
		b := ruleBuilder()
		b.offline(time.Unix(80, 0).UTC(), 0x5000, func(a *model.Action) {
			a.TimeQuality, a.Detail = model.TimeRelative, "recovery action for dirty LRU page: Recovered; asked for through madvise"
		})
		v := hostOf(t, b).Loose
		wantVerdict(t, v, model.VerdictObserve, RuleHostCE)
		if e := evidenceOf(t, v, RuleHostCE); e.Code != "host.pages_undated" || e.Text != "1 page taken offline and tied to no DIMM" {
			t.Errorf("evidence = %+v", e)
		}
		if !has(checkCodes(v), "weakens check.host_asked") || v.Confidence != 40 {
			t.Errorf("checks %v, confidence %d", checkCodes(v), v.Confidence)
		}
		for _, c := range v.CounterEvidence {
			if c.Code == "check.host_asked" && (c.Effect != model.EffectWeakens || strings.Contains(c.Text, "?")) {
				t.Errorf("check = %+v", c)
			}
		}
	})

	t.Run("errors between two DIMMs of one slot", func(t *testing.T) {
		b := ruleBuilder()
		e := b.loose(t0.Add(10*day), model.SevCE, 0)
		e.Slot, e.DIMMLink = "A1", model.LinkAmbiguous
		b.add(e)
		v := hostOf(t, b).Loose
		wantVerdict(t, v, model.VerdictObserve, RuleHostCE)
		if !has(checkCodes(v), "open check.host_ambiguous") || has(checkCodes(v), "open check.host_no_place") {
			t.Errorf("checks %v", checkCodes(v))
		}
	})

	t.Run("what the sources of the scan say about it", func(t *testing.T) {
		b := ruleBuilder()
		b.add(b.loose(t0.Add(10*day), model.SevUE, 0))
		b.in.Scans = append(b.in.Scans, model.Scan{ID: 1, Host: "h1", Kind: model.ScanLive, StartedAt: t0.Add(30 * day)})
		b.in.Sources = append(b.in.Sources,
			model.Source{ID: 1, ScanID: 1, Kind: "klog", Status: model.SourceOK},
			model.Source{ID: 2, ScanID: 1, Kind: "edac-sysfs", Status: model.SourceEmpty, Detail: "no memory controller is registered"},
			model.Source{ID: 3, ScanID: 1, Kind: "smbios", Status: model.SourceUnreadable, Detail: "permission denied"})
		v := hostOf(t, b).Loose
		if got := missingCodes(v); !has(got, "missing.no_edac") || !has(got, "missing.source_detail") || v.Confidence != 80 {
			t.Errorf("missing %v, confidence %d", got, v.Confidence)
		}
	})

	t.Run("an error after the reference time is left out", func(t *testing.T) {
		b := ruleBuilder()
		b.add(b.loose(asOf.Add(2*day), model.SevUE, 0))
		if h := hostOf(t, b); h.Loose != nil {
			t.Errorf("host finding %+v", h.Loose)
		}
	})
}

// The grade of a host: its worst finding, the host finding among them. A
// DIMM that is to be replaced on its own records stands before a hold of
// the host finding, which cannot name a DIMM; a hold about a DIMM outranks
// everything.
func TestHostFindingAndTheGradeOfTheHost(t *testing.T) {
	const (
		ok   = model.VerdictOK
		obs  = model.VerdictObserve
		con  = model.VerdictConsiderReplacement
		rep  = model.VerdictReplaceNow
		hold = model.VerdictHoldReplacement
	)
	for _, tc := range []struct {
		parts model.VerdictGrade
		loose model.VerdictGrade // "" for no host finding
		want  model.VerdictGrade
	}{
		{ok, "", ok}, {rep, "", rep},
		{ok, obs, obs}, {obs, obs, obs}, {con, obs, con}, {rep, obs, rep}, {hold, obs, hold},
		{ok, hold, hold}, {obs, hold, hold}, {con, hold, hold}, {rep, hold, rep}, {hold, hold, hold},
	} {
		var v *model.Verdict
		if tc.loose != "" {
			v = &model.Verdict{Grade: tc.loose}
		}
		if got := withHostFinding(tc.parts, v); got != tc.want {
			t.Errorf("DIMMs %s, host finding %q: %s, want %s", tc.parts, tc.loose, got, tc.want)
		}
	}

	// In a result: a DIMM with an uncorrected error of its own, and three
	// more that name no DIMM.
	b := ruleBuilder()
	b.place("d1", "S-d1", t0, asOf)
	b.add(severity(b.event("d1", t0.Add(50*day), cellA), model.SevUE, model.ScrubDemand))
	for k := range 3 {
		b.add(b.loose(t0.Add(time.Duration(51+k)*day), model.SevUE, 0))
	}
	res := analyze(t, b.in, Options{AsOf: asOf})
	h := res.Host("h1")
	if h.Grade != rep || res.ExitCode() != 3 || h.Loose == nil || h.Loose.Grade != hold || h.DIMM("d1").Verdict.Grade != rep {
		t.Errorf("host grade %s, exit code %d, host finding %+v", h.Grade, res.ExitCode(), h.Loose)
	}
	// The DIMM is told that errors of the host are counted for no DIMM.
	if !has(missingCodes(h.DIMM("d1").Verdict), "missing.unplaced") {
		t.Errorf("missing of the DIMM: %v", missingCodes(h.DIMM("d1").Verdict))
	}
	// Without the DIMM's own error the host holds, with exit code 4.
	b = ruleBuilder()
	b.place("d1", "S-d1", t0, asOf)
	b.add(b.loose(t0.Add(51*day), model.SevUE, 0))
	if res := analyze(t, b.in, Options{AsOf: asOf}); res.Grade() != hold || res.ExitCode() != 4 {
		t.Errorf("only loose errors: grade %s, exit code %d", res.Grade(), res.ExitCode())
	}
}

// The host finding is stored with the findings of the DIMMs and comes back
// with the references to its records.
func TestHostFindingIsSaved(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	b := ruleBuilder()
	b.add(b.loose(t0.Add(10*day), model.SevUE, 0x1234000))
	res := analyze(t, b.in, Options{AsOf: asOf})
	if err := res.Save(ctx, s); err != nil {
		t.Fatal(err)
	}
	vs, err := s.Verdicts(ctx, "h1")
	if err != nil {
		t.Fatal(err)
	}
	var host []model.Verdict
	for _, v := range vs {
		if v.DIMMUID == "" && v.GPU == "" {
			host = append(host, v)
		}
	}
	if len(host) != 1 || host[0].Grade != model.VerdictHoldReplacement || !equalStrings(host[0].Rules, []string{RuleHostUE}) ||
		len(host[0].Evidence) != 1 || host[0].NextAction.Code != "next.host_ue" || host[0].ID == 0 || res.Host("h1").Loose.ID != host[0].ID {
		t.Fatalf("stored host findings: %+v", host)
	}
	// Saved again, the ledger holds one host finding, not two.
	if err := res.Save(ctx, s); err != nil {
		t.Fatal(err)
	}
	vs, _ = s.Verdicts(ctx, "h1")
	n := 0
	for _, v := range vs {
		if v.DIMMUID == "" && v.GPU == "" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d host findings after a second save", n)
	}
}

// On a virtual machine every finding says so: a guest is shown little of
// the errors of the machine it runs on. The host finding does not send the
// reader to an EDAC driver or a BMC, which a guest does not have, and
// names the hypervisor host instead.
func TestFindingsOfAVirtualMachine(t *testing.T) {
	guest := func(virtual string) *builder {
		b := ruleBuilder()
		b.in.host("h1")
		b.in.Hosts = append(b.in.Hosts, model.Host{Name: "h1", Virtual: virtual})
		b.in.Scans = append(b.in.Scans, model.Scan{ID: 1, Host: "h1", Kind: model.ScanLive, StartedAt: t0.Add(30 * day)})
		b.in.Sources = append(b.in.Sources,
			model.Source{ID: 1, ScanID: 1, Kind: "klog", Status: model.SourceOK},
			model.Source{ID: 2, ScanID: 1, Kind: "edac-sysfs", Status: model.SourceEmpty})
		return b
	}
	b := guest("KVM")
	b.place("d1", "S-d1", t0, asOf)
	b.ce("d1", t0.Add(10*day), cellA)
	b.add(b.loose(t0.Add(11*day), model.SevUE, 0))
	h := hostOf(t, b)

	v := h.Loose
	if got := missingCodes(v); !has(got, "missing.virtual") || has(got, "missing.no_edac") || v.NextAction.Code != "next.host_ue_vm" {
		t.Errorf("host finding: missing %v, next %q", got, v.NextAction.Code)
	}
	var said string
	for _, m := range v.Missing {
		if m.Code == "missing.virtual" {
			said = m.Text
		}
	}
	if said != "this host is a virtual machine (KVM): a guest is not shown the corrected errors of the machine it runs on, and of the uncorrected ones at most those that hit its own memory" {
		t.Errorf("statement: %q", said)
	}
	if d := h.DIMM("d1").Verdict; !has(missingCodes(d), "missing.virtual") {
		t.Errorf("the finding of a DIMM of the guest: missing %v", missingCodes(d))
	}

	// Corrected errors alone, on a guest whose hypervisor has no name.
	b = guest("hypervisor")
	b.add(b.loose(t0.Add(11*day), model.SevCE, 0))
	v = hostOf(t, b).Loose
	if v.NextAction.Code != "next.host_ce_vm" {
		t.Errorf("next = %q", v.NextAction.Code)
	}
	for _, m := range v.Missing {
		if m.Code != "missing.virtual" {
			continue
		}
		if en, ko := Text(i18n.EN, m), Text(i18n.KO, m); !strings.HasPrefix(en, "this host is a virtual machine (the CPU reports a hypervisor): ") ||
			!strings.HasPrefix(ko, "이 호스트는 가상 머신임(CPU가 하이퍼바이저를 보고함): ") {
			t.Errorf("a hypervisor without a name: %q / %q", en, ko)
		}
	}

	// A physical host is told about the EDAC driver, as before.
	b = guest("")
	b.add(b.loose(t0.Add(11*day), model.SevUE, 0))
	v = hostOf(t, b).Loose
	if got := missingCodes(v); has(got, "missing.virtual") || !has(got, "missing.no_edac") || v.NextAction.Code != "next.host_ue" {
		t.Errorf("a physical host: missing %v, next %q", got, v.NextAction.Code)
	}
}
