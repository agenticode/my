package engine

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
)

// part adds a DIMM of the given part number that sits in a slot of h1.
func (b *builder) part(uid, partNumber string) {
	b.in.host("h1")
	b.in.DIMMs = append(b.in.DIMMs, model.DIMM{UID: uid, Identity: model.IdentitySerial, Manufacturer: "Example Memory", PartNumber: partNumber, Serial: uid})
	b.in.Placements = append(b.in.Placements, model.Placement{ID: int64(len(b.in.Placements) + 1), DIMMUID: uid, Host: "h1", Slot: "S-" + uid,
		FirstSeen: t0, LastSeen: t0.Add(100 * day)})
}

func TestOwnBaseline(t *testing.T) {
	asOf := t0.Add(60 * day)
	recentFrom, priorFrom := asOf.Add(-7*day), asOf.Add(-37*day)
	own := func(b *builder) OwnBaseline { return analyzeD1(t, b, asOf).Baseline.Own }

	// The same rate as before: in the middle.
	b := newBuilder()
	b.observedSince(t0)
	b.spreadOver(cellA, 30, priorFrom, recentFrom)
	b.spreadOver(cellA, 7, recentFrom, asOf)
	if got := own(b); !got.Valid || got.Percentile != 50 || got.RecentPerDay != 1 || got.PriorPerDay != 1 || got.PriorDays != 30 {
		t.Errorf("same rate: %+v", got)
	}
	// Three times the rate of every day before: above all of them.
	b = newBuilder()
	b.observedSince(t0)
	b.spreadOver(cellA, 30, priorFrom, recentFrom)
	b.spreadOver(cellA, 21, recentFrom, asOf)
	if got := own(b); !got.Valid || got.Percentile != 100 {
		t.Errorf("three times the rate: %+v", got)
	}
	// Ten busy days and twenty silent ones before; now a rate in between.
	b = newBuilder()
	b.observedSince(t0)
	b.spreadOver(cellA, 50, priorFrom, priorFrom.Add(10*day))
	b.spreadOver(cellA, 14, recentFrom, asOf)
	if got := own(b); !got.Valid || math.Abs(got.Percentile-100*20.0/30) > 1e-9 {
		t.Errorf("between silent and busy days: %+v", got)
	}
	// No errors then and none now: as usual.
	b = newBuilder()
	b.observedSince(t0)
	b.ce("d1", t0.Add(day), cellA)
	if got := own(b); !got.Valid || got.Percentile != 50 {
		t.Errorf("silent throughout: %+v", got)
	}
	// Too little history to compare with.
	b = newBuilder()
	b.observedSince(asOf.Add(-9 * day))
	b.spreadOver(cellA, 7, recentFrom, asOf)
	if got := own(b); got.Valid || got.Percentile != 0 || got.PriorDays != 2 {
		t.Errorf("nine days of history: %+v", got)
	}
}

func TestPeerBaseline(t *testing.T) {
	asOf := t0.Add(60 * day)
	b := newBuilder()
	b.observedSince(t0)
	for i := 1; i <= 6; i++ {
		b.part(fmt.Sprintf("d%d", i), "PN-A")
	}
	b.part("e1", "PN-B")
	b.part("e2", "PN-B")
	b.spreadOver(cellA, 50, asOf.Add(-20*day), asOf) // d1, inside the window
	b.ce("d2", t0.Add(day), cellA)                   // before the window: does not count
	b.ce("e1", asOf.Add(-day), cellA)                // another part number
	b.ce("x-slot-only", asOf.Add(-day), cellA)       // no part number at all
	res := Analyze(b.in, Options{AsOf: asOf})
	h := res.Host("h1")
	if len(h.DIMMs) != 9 {
		t.Fatalf("%d DIMMs in the result, want 9", len(h.DIMMs))
	}
	p := h.DIMM("d1").Baseline.Peers
	want := PeerBaseline{Valid: true, PartNumber: "PN-A", Peers: 6, WithErrors: 1, Errors: 50, Percentile: 100 * 5.5 / 6}
	if p != want {
		t.Errorf("d1: %+v\nwant %+v", p, want)
	}
	for _, uid := range []string{"d2", "d3", "d6"} {
		p := h.DIMM(uid).Baseline.Peers
		if !p.Valid || p.Errors != 0 || p.Peers != 6 || p.Percentile != 100*2.5/6 {
			t.Errorf("%s: %+v", uid, p)
		}
	}
	// Two DIMMs of a part number are too few to rank.
	if p := h.DIMM("e1").Baseline.Peers; p.Valid || p.Peers != 2 || p.WithErrors != 1 || p.Errors != 1 || p.Percentile != 0 {
		t.Errorf("e1: %+v", p)
	}
	// A DIMM without a part number has no peers.
	if p := h.DIMM("x-slot-only").Baseline.Peers; p != (PeerBaseline{}) {
		t.Errorf("slot-only DIMM: %+v", p)
	}
	// The threshold is configuration.
	cfg := DefaultConfig()
	cfg.MinPeers = 2
	if p := Analyze(b.in, Options{AsOf: asOf, Config: &cfg}).Host("h1").DIMM("e1").Baseline.Peers; !p.Valid || p.Percentile != 75 {
		t.Errorf("e1 with MinPeers 2: %+v", p)
	}
}

// reference is a vendor baseline for one part number.
type reference struct {
	part string
	seen []string
}

func (r *reference) Percentile(d *model.DIMM, errors int64, days float64) (float64, bool) {
	r.seen = append(r.seen, fmt.Sprintf("%s %d %g", d.UID, errors, days))
	if d.PartNumber != r.part {
		return 0, false
	}
	return float64(errors) * 3, true // above 100 for many errors: the engine clamps
}

func TestVendorBaselineHook(t *testing.T) {
	asOf := t0.Add(60 * day)
	b := newBuilder()
	b.observedSince(t0)
	b.part("d1", "PN-A")
	b.part("d2", "PN-A")
	b.part("e1", "PN-B")
	b.spreadOver(cellA, 50, asOf.Add(-20*day), asOf)
	b.ce("d2", asOf.Add(-day), cellA)
	ref := &reference{part: "PN-A"}
	h := Analyze(b.in, Options{AsOf: asOf, PartBaseline: ref}).Host("h1")
	if v := h.DIMM("d1").Baseline.Vendor; v != (VendorBaseline{Valid: true, Percentile: 100}) {
		t.Errorf("d1: %+v", v)
	}
	if v := h.DIMM("d2").Baseline.Vendor; v != (VendorBaseline{Valid: true, Percentile: 3}) {
		t.Errorf("d2: %+v", v)
	}
	if v := h.DIMM("e1").Baseline.Vendor; v.Valid {
		t.Errorf("e1 has no reference: %+v", v)
	}
	if len(ref.seen) != 3 {
		t.Errorf("the hook was asked %v", ref.seen)
	}
	// Without a vendor analyzer the layer is empty.
	if v := Analyze(b.in, Options{AsOf: asOf}).Host("h1").DIMM("d1").Baseline.Vendor; v.Valid {
		t.Errorf("no hook: %+v", v)
	}
}

// TestSubjects: every DIMM of a host has a result, with or without errors.
func TestSubjects(t *testing.T) {
	ds := generate(t, "demo", 1, nil)
	res := Analyze(load(t, sharedLedger(t, "demo", 1, "")), Options{})
	if res.EngineVersion != Version || res.Config != DefaultConfig() || len(res.Hosts) != 1 {
		t.Fatalf("result = %+v", res)
	}
	h := res.Hosts[0]
	if h.Host != "r12-n034" || !h.AsOf.Equal(ds.Scenario.End) || !h.ObservedFrom.Equal(ds.Boots[0].StartedAt) || len(h.DIMMs) != 16 || len(h.Unattributed) != 0 {
		t.Fatalf("host: %s as of %v, observed from %v, %d DIMMs", h.Host, h.AsOf, h.ObservedFrom, len(h.DIMMs))
	}
	// Most errors first.
	if h.DIMMs[0].Slot != "CPU0_C1" || h.DIMMs[1].Slot != "CPU1_D1" || h.DIMMs[0].DIMM.Serial != ds.Truth[0].Serial {
		t.Errorf("order: %s, %s", h.DIMMs[0].Slot, h.DIMMs[1].Slot)
	}
	if h.InSlot("CPU0_C1") != h.DIMMs[0] || h.InSlot("CPU0_C2") != nil || h.DIMM("no-such") != nil || res.Host("other") != nil {
		t.Error("lookup by slot or identity")
	}
	for i, d := range h.DIMMs {
		if !d.Present || d.Host != "r12-n034" || d.Slot == "" || d.DIMM == nil || d.DIMM.UID != d.UID {
			t.Errorf("DIMM %d: %+v", i, d)
		}
		if p := d.Baseline.Peers; !p.Valid || p.Peers != 16 || p.WithErrors != 2 {
			t.Errorf("DIMM %s: peers %+v", d.Slot, p)
		}
		if i < 2 {
			continue
		}
		// A DIMM without errors: no faults, nothing measured, as usual
		// against its own past and in the lower half of its peers.
		if len(d.Faults) != 0 || d.Features != (Features{Trend: Trend{Direction: TrendQuiet, PriorDays: 30}}) {
			t.Errorf("DIMM %s without errors: %+v", d.Slot, d.Features)
		}
		if !d.Baseline.Own.Valid || d.Baseline.Own.Percentile != 50 || d.Baseline.Peers.Percentile != 100*7.0/16 {
			t.Errorf("DIMM %s without errors: baseline %+v", d.Slot, d.Baseline)
		}
	}
}

// TestSubjectPlaces: where a DIMM is now, and whether it is still there.
func TestSubjectPlaces(t *testing.T) {
	// A DIMM that was moved is listed in its new slot, with all its errors.
	ds := generate(t, "moved-dimm", 1, noNoise)
	h := Analyze(load(t, sharedLedger(t, "moved-dimm", 1, "")), Options{}).Hosts[0]
	d := h.DIMM(uidOf(ds.Truth[0].Serial))
	if d == nil || d.Slot != "CPU0_D1" || !d.Present || h.InSlot("CPU0_D1") != d || d.Features.CE < 240 {
		t.Fatalf("moved DIMM: %+v", d)
	}
	if o := h.InSlot("CPU0_C1"); o == nil || o == d || !o.Present {
		t.Errorf("the DIMM that took its place: %+v", o)
	}

	// A DIMM that was replaced keeps its history and is not present.
	b := newBuilder()
	b.in.host("h1")
	b.in.Placements = []model.Placement{
		{ID: 1, DIMMUID: "old", Host: "h1", Slot: "CPU0_A1", FirstSeen: t0, LastSeen: t0.Add(10 * day)},
		{ID: 2, DIMMUID: "new", Host: "h1", Slot: "CPU0_A1", FirstSeen: t0.Add(12 * day), LastSeen: t0.Add(30 * day)},
		{ID: 3, DIMMUID: "gone", Host: "h1", Slot: "CPU0_B1", FirstSeen: t0, LastSeen: t0.Add(10 * day)},
	}
	// The latest inventory says CPU0_B1 is empty.
	b.in.Slots = []model.Slot{{Host: "h1", Name: "CPU0_A1", Populated: model.Some(true)}, {Host: "h1", Name: "CPU0_B1", Populated: model.Some(false)}}
	e := b.event("old", t0.Add(3*day), cellA)
	e.Slot = "CPU0_A1"
	b.add(e)
	// A DIMM no inventory names: it is known through its errors.
	e = b.event("s-placeholder", t0.Add(4*day), cellA)
	e.Slot, e.DIMMLink = "mc0 0:1", model.LinkSlot
	b.add(e)
	h = Analyze(b.in, Options{}).Host("h1")
	want := map[string]struct {
		slot    string
		present bool
	}{"old": {"CPU0_A1", false}, "new": {"CPU0_A1", true}, "gone": {"CPU0_B1", false}, "s-placeholder": {"mc0 0:1", true}}
	if len(h.DIMMs) != len(want) {
		t.Fatalf("%d DIMMs", len(h.DIMMs))
	}
	for _, d := range h.DIMMs {
		if w := want[d.UID]; d.Slot != w.slot || d.Present != w.present || d.Host != "h1" {
			t.Errorf("%s: slot %q present %v, want %q %v", d.UID, d.Slot, d.Present, w.slot, w.present)
		}
	}
	if d := h.InSlot("CPU0_A1"); d == nil || d.UID != "new" {
		t.Errorf("in CPU0_A1: %+v", d)
	}
	if d := h.DIMM("s-placeholder"); d.Features.SlotOnly != 1 || d.DIMM != nil {
		t.Errorf("slot-only DIMM: %+v", d.Features)
	}
	// A DIMM known through an action only.
	b = newBuilder()
	b.in.host("h1")
	b.in.Actions = []model.Action{{ID: 1, Host: "h1", Kind: model.ActionReplacement, At: t0, Slot: "CPU0_A1", DIMMUID: "acted"}}
	if d := Analyze(b.in, Options{}).Host("h1").DIMM("acted"); d == nil || d.Slot != "CPU0_A1" || !d.Present {
		t.Errorf("DIMM known through an action: %+v", d)
	}
}

// TestReferenceTime: the result is computed as of a reference time, and a
// caller can ask for an earlier one to see what the ledger showed then.
func TestReferenceTime(t *testing.T) {
	ds := generate(t, "demo", 1, nil)
	in := load(t, sharedLedger(t, "demo", 1, ""))
	uid := uidOf(ds.Truth[0].Serial)

	now := Analyze(in, Options{}).Hosts[0]
	d := now.DIMM(uid)
	if !now.AsOf.Equal(ds.Scenario.End) || d.Features.UE != 1 || d.Features.CE != 1231 || d.Features.Offline.Pages != 6 {
		t.Fatalf("as of the end: %+v", d.Features)
	}

	// 20 October: before the repair, the page offlines and the
	// uncorrected error.
	asOf := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	then := Analyze(in, Options{AsOf: asOf}).Hosts[0]
	d = then.DIMM(uid)
	if !then.AsOf.Equal(asOf) || len(then.DIMMs) != 16 {
		t.Fatalf("as of %v: %d DIMMs, reference %v", asOf, len(then.DIMMs), then.AsOf)
	}
	ft := d.Features
	if ft.UE != 0 || ft.CE == 0 || ft.CE >= 400 || ft.Offline.Pages != 0 || ft.Last.After(asOf) || len(d.Faults[0].Mitigations) != 0 {
		t.Errorf("as of 20 October: %+v", ft)
	}
	if d.Faults[0].Mode != model.ModeRow || d.Faults[0].LastSeen.After(asOf) {
		t.Errorf("as of 20 October: %s", faultString(d.Faults[0]))
	}
	// The windows count back from the reference time.
	var recent int64
	for i := range ds.Events {
		e := &ds.Events[i]
		if e.DIMMUID == uid && e.ErrorDT.After(asOf.Add(-7*day)) && !e.ErrorDT.After(asOf) {
			recent++
		}
	}
	if ft.Trend.Recent != recent || recent == 0 {
		t.Errorf("recent errors as of 20 October = %d, want %d", ft.Trend.Recent, recent)
	}
	// The input itself is not changed by asking for an earlier time.
	if in.Events() != 1285 || len(in.Actions) != 8 {
		t.Errorf("input changed: %d events, %d actions", in.Events(), len(in.Actions))
	}

	// Without a live scan the reference time is the latest record: an
	// import says when files were read, not when the host was looked at.
	b := newBuilder()
	b.ce("d1", t0, cellA)
	b.ce("d1", t0.Add(9*day), cellA)
	b.in.Scans = []model.Scan{{ID: 1, Host: "h1", Kind: model.ScanImport, StartedAt: t0.Add(300 * day)}}
	if h := Analyze(b.in, Options{}).Host("h1"); !h.AsOf.Equal(t0.Add(9*day)) || !h.ObservedFrom.Equal(t0) {
		t.Errorf("import: as of %v, observed from %v", h.AsOf, h.ObservedFrom)
	}
	// A live scan looked at the host when it ran.
	b.in.Scans = append(b.in.Scans, model.Scan{ID: 2, Host: "h1", Kind: model.ScanLive, StartedAt: t0.Add(20 * day), FinishedAt: t0.Add(20*day + time.Minute)})
	if h := Analyze(b.in, Options{}).Host("h1"); !h.AsOf.Equal(t0.Add(20*day + time.Minute)) {
		t.Errorf("live scan: as of %v", h.AsOf)
	}
	// A host without any dated record has no reference time.
	b = newBuilder()
	e := b.event("d1", time.Unix(500, 0).UTC(), cellA)
	e.TimeQuality = model.TimeRelative
	b.add(e)
	if h := Analyze(b.in, Options{}).Host("h1"); !h.AsOf.IsZero() || h.DIMM("d1").Features.Trend.Direction != TrendUnknown || h.DIMM("d1").Features.CE != 1 {
		t.Errorf("undated host: as of %v, %+v", h.AsOf, h.DIMM("d1").Features)
	}
}

// TestSeveralHosts: one ledger can hold several hosts. A DIMM that moved
// from one to another is listed where it is now, with its whole history.
func TestSeveralHosts(t *testing.T) {
	b := newBuilder()
	place := func(id int64, uid, host, slot string, from, to time.Duration) {
		b.in.Placements = append(b.in.Placements, model.Placement{ID: id, DIMMUID: uid, Host: host, Slot: slot, FirstSeen: t0.Add(from), LastSeen: t0.Add(to)})
	}
	place(1, "mover", "h1", "CPU0_A1", 0, 10*day)
	place(2, "mover", "h2", "CPU1_B1", 12*day, 30*day)
	place(3, "stays", "h1", "CPU0_B1", 0, 30*day)
	for k := range 8 {
		e := b.event("mover", t0.Add(time.Duration(k)*day), cellA)
		e.Slot = "CPU0_A1"
		b.add(e)
	}
	for k := range 5 {
		e := b.event("mover", t0.Add(time.Duration(13+k)*day), cellA)
		e.Host, e.Slot = "h2", "CPU1_B1"
		b.add(e)
	}
	b.ce("stays", t0.Add(25*day), cellA)
	// An error of h2 that belongs to no DIMM.
	e := b.event("", t0.Add(14*day), cellA)
	e.Host, e.Slot, e.DIMMLink = "h2", "", ""
	b.add(e)

	res := Analyze(b.in, Options{})
	if len(res.Hosts) != 2 || res.Hosts[0].Host != "h1" || res.Hosts[1].Host != "h2" {
		t.Fatalf("hosts = %+v", res.Hosts)
	}
	h1, h2 := res.Hosts[0], res.Hosts[1]
	if len(h1.DIMMs) != 1 || h1.DIMMs[0].UID != "stays" || !h1.AsOf.Equal(t0.Add(30*day)) {
		t.Errorf("h1: %+v as of %v", h1.DIMMs, h1.AsOf)
	}
	d := h2.DIMM("mover")
	if len(h2.DIMMs) != 1 || d == nil || d.Slot != "CPU1_B1" || !d.Present || d.Features.CE != 13 || len(d.Faults) != 1 ||
		d.Faults[0].ErrorCount != 13 || len(d.Faults[0].Slots) != 2 || d.Faults[0].Host != "h2" {
		t.Errorf("h2: %+v", d)
	}
	if len(h2.Unattributed) != 1 || h2.Unattributed[0].CE != 1 || len(h1.Unattributed) != 0 {
		t.Errorf("unattributed: h1 %+v, h2 %+v", h1.Unattributed, h2.Unattributed)
	}
	// Asking for one host returns that host; the DIMM keeps the history
	// it has from the other.
	only := Analyze(b.in, Options{Host: "h2"})
	if len(only.Hosts) != 1 || only.Hosts[0].Host != "h2" || only.Hosts[0].DIMM("mover").Features.CE != 13 {
		t.Errorf("host filter: %+v", only.Hosts)
	}
	if got := Analyze(b.in, Options{Host: "h9"}); len(got.Hosts) != 0 {
		t.Errorf("unknown host: %+v", got.Hosts)
	}
}

// TestUntil: the view of an input at an earlier time leaves out everything
// that was recorded later.
func TestUntil(t *testing.T) {
	b := newBuilder()
	b.ce("d1", t0, cellA)
	b.ce("d1", t0.Add(20*day), cellA)
	undated := b.event("d1", time.Unix(900, 0).UTC(), cellA)
	undated.TimeQuality = model.TimeRelative
	b.add(undated)
	b.in.Actions = []model.Action{action(1, model.ActionHPPR, t0.Add(5*day)), action(2, model.ActionHPPR, t0.Add(15*day))}
	b.in.Placements = []model.Placement{
		{ID: 1, DIMMUID: "d1", Host: "h1", Slot: "S-d1", FirstSeen: t0, LastSeen: t0.Add(30 * day)},
		{ID: 2, DIMMUID: "d2", Host: "h1", Slot: "S-d2", FirstSeen: t0.Add(12 * day), LastSeen: t0.Add(30 * day)},
	}
	b.in.Boots = []model.Boot{
		{Host: "h1", BootID: "a", StartedAt: t0.Add(-day), LastSeen: t0.Add(11 * day), StartQuality: model.TimeExact},
		{Host: "h1", BootID: "b", StartedAt: t0.Add(11 * day), LastSeen: t0.Add(30 * day), StartQuality: model.TimeExact},
	}
	b.in.Scans = []model.Scan{{ID: 1, Host: "h1", Kind: model.ScanLive, StartedAt: t0.Add(2 * day)}, {ID: 2, Host: "h1", Kind: model.ScanLive, StartedAt: t0.Add(30 * day)}}
	b.in.Sources = []model.Source{{ID: 1, ScanID: 1, Kind: "klog"}, {ID: 2, ScanID: 2, Kind: "klog"}, {ID: 3, ScanID: 2, Kind: "smbios"}}
	b.in.GPUs = []model.GPUMem{{ID: 1, Host: "h1", ObservedAt: t0.Add(day)}, {ID: 2, Host: "h1", ObservedAt: t0.Add(25 * day)}}

	cut := t0.Add(10 * day)
	v := b.in.until(cut)
	if v.Events() != 2 || len(v.Actions) != 1 || v.Actions[0].ID != 1 || len(v.Placements) != 1 || !v.Placements[0].LastSeen.Equal(cut) ||
		len(v.Boots) != 1 || !v.Boots[0].LastSeen.Equal(cut) || len(v.Scans) != 1 || len(v.Sources) != 1 || v.Sources[0].ID != 1 || len(v.GPUs) != 1 {
		t.Errorf("view at day 10: %d events, %d actions, %+v, %+v, %d scans, %d sources, %d GPU records",
			v.Events(), len(v.Actions), v.Placements, v.Boots, len(v.Scans), len(v.Sources), len(v.GPUs))
	}
	// The input itself keeps everything.
	if b.in.Events() != 3 || len(b.in.Actions) != 2 || len(b.in.Placements) != 2 || !b.in.Placements[0].LastSeen.Equal(t0.Add(30*day)) ||
		len(b.in.Boots) != 2 || len(b.in.Scans) != 2 || len(b.in.Sources) != 3 || len(b.in.GPUs) != 2 {
		t.Error("until changed the input")
	}
	// The reference time of the view is what was asked for.
	if h := Analyze(b.in, Options{AsOf: cut}).Host("h1"); !h.AsOf.Equal(cut) || h.DIMM("d1").Features.CE != 2 || h.DIMM("d2") != nil {
		t.Errorf("analysis at day 10: %+v", h)
	}
}
