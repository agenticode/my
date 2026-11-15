package engine

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// randomInput builds an input no real host would produce: records with any
// mix of fields set, DIMMs that come and go, actions of every kind, sources
// in every state. The engine has to give a consistent result for all of it.
func randomInput(rng *rand.Rand) (*Input, int64) {
	b := newBuilder()
	hosts := []string{"h1", "h2"}[:1+rng.IntN(2)]
	type slotRef struct{ host, name string }
	var slots []slotRef
	for _, h := range hosts {
		b.in.host(h)
		for i := range 2 + rng.IntN(6) {
			sl := model.Slot{Host: h, Name: fmt.Sprintf("CPU%d_%c%d", i/4, 'A'+rune(i%4), 1+i%2)}
			if rng.IntN(4) > 0 {
				sl.Locator = sl.Name
				sl.SMBIOSHandle = model.Some(0x1100 + i)
			}
			if rng.IntN(5) > 0 {
				sl.Socket, sl.Channel, sl.Position = model.Some(i/4), model.Some(i%4/2), model.Some(i%2)
			}
			if rng.IntN(3) == 0 {
				sl.IMC = model.Some(i % 2)
			}
			if rng.IntN(3) > 0 {
				sl.Populated = model.Some(rng.IntN(5) > 0)
			}
			if rng.IntN(3) == 0 {
				sl.EdacMC, sl.EdacLocation, sl.EdacLabel = model.Some(i/4), fmt.Sprintf("%d:%d", i%4/2, i%2), "label "+sl.Name
			}
			b.in.Slots = append(b.in.Slots, sl)
			slots = append(slots, slotRef{h, sl.Name})
		}
	}
	span := 120 * day
	at := func() time.Time { return t0.Add(time.Duration(rng.Int64N(int64(span)))) }

	var uids []string
	for i := range 1 + rng.IntN(8) {
		uid := fmt.Sprintf("dimm-%d", i)
		uids = append(uids, uid)
		d := model.DIMM{UID: uid, Identity: model.IdentitySerial, Manufacturer: "Example Memory", PartNumber: []string{"PN-A", "PN-B", ""}[rng.IntN(3)], Serial: fmt.Sprintf("SN%04d", i)}
		if rng.IntN(5) == 0 {
			d.Identity, d.Serial, d.PartNumber = model.IdentitySlot, "", ""
		}
		if rng.IntN(2) == 0 {
			d.MemoryType, d.DeviceWidth = "DDR5", model.Some(4)
		}
		if rng.IntN(6) > 0 {
			b.in.DIMMs = append(b.in.DIMMs, d)
		}
		for range rng.IntN(3) {
			s := slots[rng.IntN(len(slots))]
			first := at()
			b.in.Placements = append(b.in.Placements, model.Placement{ID: int64(len(b.in.Placements) + 1), DIMMUID: uid, Host: s.host, Slot: s.name,
				FirstSeen: first, LastSeen: first.Add(time.Duration(rng.Int64N(int64(40 * day))))})
		}
	}

	var total int64
	opt := func(max int) int {
		if rng.IntN(4) == 0 {
			return -1
		}
		return rng.IntN(max)
	}
	// A few places that many records share, so that faults of every kind form.
	hot := []loc{{0, 3, 1, 2, 500, 64}, {1, 7, 0, 3, 0x1a2f, 128}, {0, -1, 2, 1, 4000, 8}}
	for range rng.IntN(400) {
		var l loc
		switch rng.IntN(6) {
		case 0:
			l = hot[rng.IntN(len(hot))]
		case 1:
			l = hot[rng.IntN(len(hot))]
			l.col = 8 * rng.IntN(256) // a row
		case 2:
			l = hot[rng.IntN(len(hot))]
			l.row = rng.IntN(1 << 16) // a column
		default:
			l = loc{opt(2), opt(20), opt(8), opt(4), opt(1 << 16), opt(256) * 8}
			if l.col < 0 {
				l.col = -1
			}
		}
		uid := ""
		if rng.IntN(10) > 0 {
			uid = uids[rng.IntN(len(uids))]
		}
		e := b.event(uid, at(), l)
		s := slots[rng.IntN(len(slots))]
		e.Host, e.Slot = s.host, s.name
		switch rng.IntN(12) {
		case 0:
			e.Slot = ""
		case 1:
			e.TimeQuality = model.TimeRelative
			e.ErrorDT = time.Unix(rng.Int64N(100000), 0).UTC()
		case 2:
			e.TimeQuality = model.TimeEstimated
		}
		if uid == "" {
			e.DIMMLink = []string{"", model.LinkAmbiguous}[rng.IntN(2)]
		} else {
			e.DIMMLink = []string{model.LinkObserved, model.LinkAssumed, model.LinkSlot}[rng.IntN(3)]
		}
		e.ErrorSeverity = []model.Severity{model.SevCE, model.SevCE, model.SevCE, model.SevCE, model.SevCE, model.SevCE, model.SevRecoverable, model.SevUE, model.SevFatal, model.SevInformational}[rng.IntN(10)]
		e.ErrorScrubType = []model.ScrubType{"", model.ScrubDemand, model.ScrubPatrol, model.ScrubOther}[rng.IntN(4)]
		e.Count = 1 + rng.IntN(4)
		if rng.IntN(3) == 0 {
			e.OsPhysicalAddress = model.Some(uint64(4<<30) + uint64(rng.Int64N(1<<30)))
		}
		switch rng.IntN(6) {
		case 0:
			e.ErrorVector = fmt.Sprintf("dq:0x%x", 1+rng.IntN(15))
		case 1:
			e.ErrorVector = fmt.Sprintf("bit:%d", rng.IntN(80))
		case 2:
			e.ErrorVector = "nibble:0x3"
		case 3:
			e.ErrorVector = "garbage"
		}
		if rng.IntN(10) == 0 {
			e.Kind, e.WindowStart = model.KindCounter, e.ErrorDT.Add(-time.Duration(rng.Int64N(int64(3*day))))
			e.Count = 1 + rng.IntN(300)
			e.Covered = rng.IntN(e.Count + 1)
		}
		if rng.IntN(25) == 0 {
			e.DupOf = 1
		}
		total += int64(e.Errors())
		b.add(e)
	}

	kinds := []model.ActionKind{model.ActionPageOffline, model.ActionSPPR, model.ActionHPPR, model.ActionSparing, model.ActionRowRemap,
		model.ActionReplacement, model.ActionReseat, model.ActionDisable, model.ActionMarkFailed}
	for range rng.IntN(12) {
		s := slots[rng.IntN(len(slots))]
		a := model.Action{ID: int64(len(b.in.Actions) + 1), Host: s.host, Slot: s.name, At: at(), Kind: kinds[rng.IntN(len(kinds))],
			Result: []model.ActionResult{model.ResultOK, model.ResultFailed, model.ResultPending, model.ResultUnknown}[rng.IntN(4)],
			Method: []string{"", "bios", "bmc", "soft", "hard"}[rng.IntN(5)], Source: "test", RawRef: fmt.Sprintf("action#%d", len(b.in.Actions)+1)}
		if rng.IntN(5) > 0 {
			a.DIMMUID = uids[rng.IntN(len(uids))]
		}
		if rng.IntN(2) == 0 {
			h := hot[rng.IntN(len(hot))]
			a.Rank, a.BankGroup, a.Bank, a.Row = opt2(h.rank), opt2(h.bg), opt2(h.bank), opt2(h.row)
		}
		if rng.IntN(2) == 0 {
			a.OsPhysicalAddress = model.Some(uint64(4<<30) + uint64(rng.Int64N(1<<30)))
		}
		if rng.IntN(3) == 0 {
			a.Detail = "detail from the source"
		}
		b.in.Actions = append(b.in.Actions, a)
	}
	for i, h := range hosts {
		for k := range rng.IntN(4) {
			b.in.Boots = append(b.in.Boots, model.Boot{Host: h, BootID: fmt.Sprintf("boot-%d-%d", i, k), StartedAt: at(), StartQuality: model.TimeExact,
				BIOS: []string{"", "2.4.4", "2.5.1"}[rng.IntN(3)], Kernel: []string{"", "5.14.0-570", "6.12.0-55"}[rng.IntN(3)]})
		}
		for k := range rng.IntN(3) {
			id := int64(len(b.in.Scans) + 1)
			b.in.Scans = append(b.in.Scans, model.Scan{ID: id, Host: h, Kind: []string{model.ScanLive, model.ScanImport, model.ScanSynthetic}[rng.IntN(3)], StartedAt: at()})
			for _, kind := range []string{"rasdaemon", "klog", "edac-sysfs", "smbios", "hostinfo"}[:rng.IntN(6)] {
				b.in.Sources = append(b.in.Sources, model.Source{ID: int64(len(b.in.Sources) + 1), ScanID: id, Kind: kind,
					Status: []model.SourceStatus{model.SourceOK, model.SourcePartial, model.SourceEmpty, model.SourceAbsent, model.SourceUnreadable, model.SourceError}[rng.IntN(6)],
					Detail: []string{"", "permission denied"}[rng.IntN(2)]})
			}
			_ = k
		}
		for range rng.IntN(3) {
			g := model.GPUMem{ID: int64(len(b.in.GPUs) + 1), Host: h, ObservedAt: at(), Kind: []string{model.GPUXID, model.GPUSnapshot}[rng.IntN(2)],
				GPUSerial: []string{"", "1320921000977"}[rng.IntN(2)], GPUUUID: []string{"", "GPU-7a1c"}[rng.IntN(2)], RawRef: "gpu"}
			if g.Kind == model.GPUXID {
				g.XID = model.Some([]int{48, 63, 64, 94}[rng.IntN(4)])
			} else if rng.IntN(2) == 0 {
				g.RemapFailure = model.Some(rng.IntN(2) == 0)
			}
			b.in.GPUs = append(b.in.GPUs, g)
		}
	}
	for range rng.IntN(4) {
		s, o := slots[rng.IntN(len(slots))], slots[rng.IntN(len(slots))]
		b.in.Places = append(b.in.Places, store.EventPlace{Host: s.host, Slot: s.name, FirstID: 1, LastID: 2,
			LocationKey: store.LocationKey{Label: []string{o.name, "unknown memory", "label " + o.name}[rng.IntN(3)], Records: 2}})
	}
	return b.in, total
}

func opt2(v int) model.Opt[int] {
	if v < 0 {
		return model.Opt[int]{}
	}
	return model.Some(v)
}

// TestAnyInputGivesAConsistentResult feeds the engine random inputs and
// checks what must hold for every result.
func TestAnyInputGivesAConsistentResult(t *testing.T) {
	seen := map[string]bool{}
	grades := map[model.VerdictGrade]int{}
	for seed := range 300 {
		rng := rand.New(rand.NewPCG(uint64(seed), 7))
		in, total := randomInput(rng)
		name := fmt.Sprintf("seed %d", seed)
		opts := Options{Now: computedAt}
		if seed%4 == 3 {
			opts.AsOf = t0.Add(60 * day)
		}
		res, err := Analyze(in, opts)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// Twice the same input: twice the same result.
		again, _ := Analyze(in, opts)
		a, _ := json.Marshal(res)
		b, _ := json.Marshal(again)
		if string(a) != string(b) {
			t.Fatalf("%s: two runs differ", name)
		}
		checkStatements(t, name, res, seen)

		var counted int64
		ids := map[int64]bool{}
		for _, h := range res.Hosts {
			worst := model.VerdictOK
			for _, d := range h.DIMMs {
				v := d.Verdict
				if v == nil {
					t.Fatalf("%s: %s has no finding", name, d.UID)
				}
				grades[v.Grade]++
				counted += d.Features.CE + d.Features.UE
				if d.Features.GradeA+d.Features.GradeB+d.Features.GradeC != d.Features.CE+d.Features.UE {
					t.Errorf("%s %s: errors by grade do not add up: %+v", name, d.UID, d.Features)
				}
				// Faults: every error in exactly one, modes within what
				// the grade allows.
				var inFaults int64
				for _, f := range d.Faults {
					inFaults += int64(f.ErrorCount)
					if !f.Mode.Valid() || !f.Grade.Valid() || f.ErrorCount <= 0 || f.Confidence < 0 || f.Confidence > 100 {
						t.Errorf("%s %s: fault %s", name, d.UID, faultString(f))
					}
					if f.Grade != model.GradeA && f.Mode != model.ModeUndetermined && f.Mode != model.ModeSystemIssue &&
						(!f.Estimated || (f.Mode != model.ModeSingleBit && f.Mode != model.ModeRow)) {
						t.Errorf("%s %s: mode from undecoded addresses: %s", name, d.UID, faultString(f))
					}
					for _, id := range in.EventIDs(f) {
						if ids[id] {
							t.Errorf("%s: event %d is in two faults", name, id)
						}
						ids[id] = true
					}
				}
				if inFaults != d.Features.CE+d.Features.UE {
					t.Errorf("%s %s: %d errors in faults, %d on the DIMM", name, d.UID, inFaults, d.Features.CE+d.Features.UE)
				}
				// The finding: the grade is that of the worst rule, rules
				// are unique and in order, evidence belongs to them.
				want := model.VerdictOK
				for i, r := range v.Rules {
					rule, ok := RuleByID(r)
					if !ok || (i > 0 && rulePosition(v.Rules[i-1]) >= rulePosition(r)) {
						t.Errorf("%s %s: rules %v", name, d.UID, v.Rules)
					}
					if worse(rule.Grade, want) {
						want = rule.Grade
					}
				}
				if v.Grade != want || (v.Grade == model.VerdictOK) != (len(v.Evidence) == 0) {
					t.Errorf("%s %s: grade %s with rules %v and %d statements", name, d.UID, v.Grade, v.Rules, len(v.Evidence))
				}
				for _, e := range v.Evidence {
					if !has(v.Rules, e.Rule) || e.Total < len(e.Records) && e.Rule != RuleMapping {
						t.Errorf("%s %s: evidence %+v", name, d.UID, e)
					}
				}
				sum := 0
				for _, f := range v.ConfidenceFactors {
					sum += f.Delta
				}
				if v.Confidence != min(max(sum, 5), 95) || v.Label != model.VerdictLabel || v.Host != h.Host || v.DIMMUID != d.UID {
					t.Errorf("%s %s: confidence %d (factors sum to %d), label %q", name, d.UID, v.Confidence, sum, v.Label)
				}
				if (v.NextAction.Code == "") != (v.Grade == model.VerdictOK) {
					t.Errorf("%s %s: grade %s with next step %q", name, d.UID, v.Grade, v.NextAction.Code)
				}
				if d.Present && worse(v.Grade, worst) {
					worst = v.Grade
				}
			}
			for _, g := range h.GPUs {
				grades[g.Verdict.Grade]++
				if g.Verdict.GPU != g.Key || g.Verdict.DIMMUID != "" || len(g.Verdict.Evidence) == 0 {
					t.Errorf("%s: GPU finding %+v", name, g.Verdict)
				}
				if worse(g.Verdict.Grade, worst) {
					worst = g.Verdict.Grade
				}
			}
			// The host finding: about what names no DIMM, and only then.
			var loose int64
			for _, u := range h.Unattributed {
				loose += u.CE + u.UE
			}
			if l := h.Loose; l != nil {
				grades[l.Grade]++
				sum := 0
				for _, f := range l.ConfidenceFactors {
					sum += f.Delta
				}
				if l.DIMMUID != "" || l.GPU != "" || l.Slot != "" || l.Host != h.Host || len(l.Evidence) == 0 || len(l.Rules) == 0 ||
					l.Grade == model.VerdictOK || l.NextAction.Code == "" || l.Confidence != min(max(sum, 5), 95) {
					t.Errorf("%s: host finding %+v", name, l)
				}
				for _, r := range l.Rules {
					if !HostRule(r) {
						t.Errorf("%s: host finding with rule %s", name, r)
					}
				}
				switch {
				case l.Grade == model.VerdictHoldReplacement && worst == model.VerdictReplaceNow:
					// A DIMM to replace on its own records stands first.
				case worse(l.Grade, worst):
					worst = l.Grade
				}
			} else if loose > 0 {
				t.Errorf("%s: %d errors tied to no DIMM and no host finding", name, loose)
			}
			if h.Grade != worst {
				t.Errorf("%s: host grade %s, worst finding %s", name, h.Grade, worst)
			}
			for _, u := range h.Unattributed {
				counted += u.CE + u.UE
			}
		}
		// No error is lost and none is counted twice.
		if opts.AsOf.IsZero() && counted != total {
			t.Errorf("%s: %d errors in the result, %d in the input", name, counted, total)
		}
	}
	// The random inputs reach every grade and most sentences.
	for _, g := range []model.VerdictGrade{model.VerdictOK, model.VerdictObserve, model.VerdictConsiderReplacement, model.VerdictReplaceNow, model.VerdictHoldReplacement} {
		if grades[g] == 0 {
			t.Errorf("no random input gave the grade %s", g)
		}
	}
	if len(seen) < 60 {
		t.Errorf("the random inputs state only %d different sentences", len(seen))
	}
}
