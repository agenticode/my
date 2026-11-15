package exporter

import (
	"context"
	"fmt"
	"strings"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// repairKinds are the actions my_dimm_repairs_total counts, and the value
// of its type label for each.
var repairKinds = []model.ActionKind{model.ActionSPPR, model.ActionHPPR, model.ActionSparing, model.ActionRowRemap}

// gradeValue is the value of my_source_grade for an observation grade.
func gradeValue(g model.Grade) float64 {
	switch g {
	case model.GradeA:
		return 3
	case model.GradeB:
		return 2
	case model.GradeC:
		return 1
	}
	return 0
}

// Host returns the metrics of one host: what the engine found, from the
// result h, and what the ledger s holds beside it (repairs, GPU memory
// state, the source inventory of the latest scan).
//
// DIMMs that are no longer in the host have no series: the metrics describe
// the hardware that is there now, and the history of a replaced module is
// what the commands show.
func Host(ctx context.Context, s *store.Store, h *engine.HostResult) (*Set, error) {
	set := &Set{}
	host := h.Host

	present := map[string]*engine.DIMMResult{}
	for _, d := range h.DIMMs {
		if !d.Present || d.Slot == "" {
			continue
		}
		present[d.UID] = d
		serial := ""
		if d.DIMM != nil {
			serial = d.DIMM.Serial
		}
		grade := model.VerdictOK
		if d.Verdict != nil {
			grade = d.Verdict.Grade
		}
		set.Add(DIMMVerdict, float64(grade.ExitCode()), host, d.Slot, serial)
		set.Add(DIMMCorrected, float64(d.Features.CE), host, d.Slot, serial)
		set.Add(DIMMUncorrected, float64(d.Features.UE), host, d.Slot, serial)
		set.Add(DIMMOfflinedPages, float64(d.Features.Offline.Pages), host, d.Slot)
		for _, f := range d.Faults {
			set.Add(DIMMFaults, 1, host, d.Slot, string(f.Mode))
		}
	}
	// Errors that are tied to no DIMM belong to no finding. They are
	// counted all the same, so that the series of a host add up to what its
	// records hold.
	for _, u := range h.Unattributed {
		set.Add(DIMMCorrected, float64(u.CE), host, u.Slot, "")
		set.Add(DIMMUncorrected, float64(u.UE), host, u.Slot, "")
	}

	actions, err := s.Actions(ctx, store.ActionFilter{Host: host, Kinds: repairKinds})
	if err != nil {
		return nil, fmt.Errorf("metrics of %s: repairs: %w", host, err)
	}
	for _, a := range actions {
		// The findings are as of h.AsOf; a repair after that is not in them.
		if a.At.After(h.AsOf) && a.TimeQuality != model.TimeRelative {
			continue
		}
		if d := present[a.DIMMUID]; d != nil {
			set.Add(DIMMRepairs, 1, host, d.Slot, string(a.Kind))
		}
	}

	gpus, err := s.GPUMem(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("metrics of %s: GPU memory: %w", host, err)
	}
	gpuMetrics(set, host, gpus)

	if err := sourceMetrics(ctx, set, s, host); err != nil {
		return nil, fmt.Errorf("metrics of %s: sources: %w", host, err)
	}
	return set, nil
}

// GPUKey names a GPU the way the findings do: by its serial number, else
// by its UUID, its PCI address or its index.
func GPUKey(r *model.GPUMem) string {
	switch {
	case r.GPUSerial != "":
		return r.GPUSerial
	case r.GPUUUID != "":
		return r.GPUUUID
	case r.PCIBus != "":
		return r.PCIBus
	case r.GPUIndex.Valid:
		return fmt.Sprintf("GPU %d", r.GPUIndex.V)
	}
	return ""
}

// xidRemapFailure is the XID the driver logs when a row remapping failed.
const xidRemapFailure = 64

// gpuMetrics adds the row remapping state of the GPUs of a host: the bank
// histogram of the latest snapshot of each GPU, and whether a remapping
// failed. A failure is on record as XID 64 in the kernel log or as the flag
// of a snapshot; a GPU that reports neither the flag nor the XID has no
// failure series, because its state is not known.
func gpuMetrics(set *Set, host string, recs []model.GPUMem) {
	type state struct {
		// snapshot is the latest snapshot with a bank histogram, flag the
		// latest one that says whether a remapping failed. A reading made
		// with a query that asked for less takes neither back.
		snapshot, flag *model.GPUMem
		failed         bool
	}
	by := map[string]*state{}
	var keys []string
	for i := range recs {
		r := &recs[i]
		key := GPUKey(r)
		if key == "" {
			continue
		}
		g := by[key]
		if g == nil {
			g = &state{}
			by[key] = g
			keys = append(keys, key)
		}
		switch {
		case r.Kind == model.GPUXID && r.XID.Valid && r.XID.V == xidRemapFailure:
			g.failed = true
		case r.Kind == model.GPUSnapshot:
			if hasBanks(r) && (g.snapshot == nil || !r.ObservedAt.Before(g.snapshot.ObservedAt)) {
				g.snapshot = r
			}
			if r.RemapFailure.Valid && (g.flag == nil || !r.ObservedAt.Before(g.flag.ObservedAt)) {
				g.flag = r
			}
		}
	}
	for _, key := range keys {
		g := by[key]
		known := g.failed
		if sn := g.snapshot; sn != nil {
			for _, b := range []struct {
				name string
				n    model.Opt[int]
			}{{"max", sn.BankMax}, {"high", sn.BankHigh}, {"partial", sn.BankPartial}, {"low", sn.BankLow}, {"none", sn.BankNone}} {
				if b.n.Valid {
					set.Add(GPURemapBanks, float64(b.n.V), host, key, b.name)
				}
			}
		}
		if g.flag != nil {
			known = true
			g.failed = g.failed || g.flag.RemapFailure.V
		}
		if !known {
			continue
		}
		v := 0.0
		if g.failed {
			v = 1
		}
		set.Add(GPURemapFailure, v, host, key)
	}
}

// hasBanks reports whether a snapshot carries the bank histogram.
func hasBanks(r *model.GPUMem) bool {
	return r.BankMax.Valid || r.BankHigh.Valid || r.BankPartial.Valid || r.BankLow.Valid || r.BankNone.Valid
}

// sourceMetrics adds the source inventory of the latest scan of a host.
// Two entries of one kind with one status (two log files that could not be
// read) are one series, with the better grade.
func sourceMetrics(ctx context.Context, set *Set, s *store.Store, host string) error {
	scans, err := s.Scans(ctx)
	if err != nil {
		return err
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
		return nil
	}
	srcs, err := s.Sources(ctx, latest.ID)
	if err != nil {
		return err
	}
	// The inventory counts the records an adapter produced. Errors that
	// were worked out from counter readings are not among them, so the
	// ledger is asked as well.
	inLedger, err := s.SourceGrades(ctx, host)
	if err != nil {
		return err
	}
	for i := range srcs {
		src := &srcs[i]
		grade := src.BestGrade()
		for name, g := range inLedger {
			if (name == src.Kind || strings.HasPrefix(name, src.Kind+"/")) && g.Better(grade) {
				grade = g
			}
		}
		set.Add(SourceGrade, gradeValue(grade), host, src.Kind, string(src.Status))
	}
	return nil
}
