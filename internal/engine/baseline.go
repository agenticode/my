package engine

import (
	"sort"

	"github.com/agenticode/my/internal/model"
)

// Baseline says how unusual the errors of a DIMM are, in three layers. Each
// layer that has enough data gives a percentile from 0 to 100; 50 means
// "as usual".
type Baseline struct {
	// Own compares the last RecentDays of the DIMM with its own
	// BaselineDays before them.
	Own OwnBaseline `json:"own"`
	// Peers compares the DIMM with the DIMMs of the same part number in
	// the ledger.
	Peers PeerBaseline `json:"peers"`
	// Vendor is the reference a vendor analyzer supplies for the part
	// number.
	Vendor VendorBaseline `json:"vendor"`
}

// OwnBaseline is the first layer: the DIMM against its own past.
type OwnBaseline struct {
	// Valid is set when the ledger covers at least MinBaselineDays of the
	// baseline window.
	Valid bool `json:"valid"`
	// Percentile is the share of the days of the baseline window on which
	// the DIMM had fewer errors than the recent daily rate (days with the
	// same number count half).
	Percentile   float64 `json:"percentile"`
	RecentPerDay float64 `json:"recent_per_day"`
	PriorPerDay  float64 `json:"prior_per_day"`
	// PriorDays is the number of days of the baseline window the ledger
	// covers.
	PriorDays float64 `json:"prior_days"`
}

// PeerBaseline is the second layer: the DIMM among the DIMMs of its part
// number in the ledger.
type PeerBaseline struct {
	// Valid is set when the ledger holds at least MinPeers DIMMs of the
	// part number, this one included.
	Valid      bool   `json:"valid"`
	PartNumber string `json:"part_number,omitzero"`
	// Peers is the number of DIMMs compared, this one included, and
	// WithErrors how many of them have errors in the window.
	Peers      int `json:"peers"`
	WithErrors int `json:"with_errors"`
	// Errors is the number of errors of this DIMM in the window: the
	// recent and the baseline window together.
	Errors int64 `json:"errors"`
	// Percentile is the share of those DIMMs with fewer errors in the
	// window (DIMMs with the same number count half).
	Percentile float64 `json:"percentile"`
}

// VendorBaseline is the third layer.
type VendorBaseline struct {
	// Valid is set when a vendor analyzer gave a reference for the part.
	Valid      bool    `json:"valid"`
	Percentile float64 `json:"percentile"`
}

// PartBaseline is the hook for the third baseline layer. A vendor analyzer
// knows what error rates are usual for a part number across its fleet; the
// open core does not.
type PartBaseline interface {
	// Percentile places a DIMM that showed the given number of errors
	// within the given number of days among the reference population of
	// its part number, from 0 to 100. ok is false when there is no
	// reference for the part.
	Percentile(d *model.DIMM, errors int64, days float64) (percentile float64, ok bool)
}

// midRank returns the percentile of x among values: the share below x,
// plus half the share equal to x.
func midRank(values []float64, x float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var below, equal float64
	for _, v := range values {
		switch {
		case v < x:
			below++
		case v == x:
			equal++
		}
	}
	return 100 * (below + equal/2) / float64(len(values))
}

// ownBaseline computes the first layer for the records idx of a DIMM.
func (m *measurer) ownBaseline(idx []int32, t Trend) OwnBaseline {
	c := m.clk
	b := OwnBaseline{RecentPerDay: t.RecentPerDay, PriorPerDay: t.PriorPerDay, PriorDays: t.PriorDays}
	if t.PriorDays < m.cfg.MinBaselineDays {
		return b
	}
	// Daily counts of the covered part of the baseline window, counted
	// back from its end.
	n := int(t.PriorDays)
	if n < 1 {
		return b
	}
	daily := make([]float64, n)
	for _, i := range idx {
		e := &m.in.events[i]
		if e.relative || e.at > c.recentFrom || e.at <= c.priorFrom {
			continue
		}
		if d := int((c.recentFrom - e.at) / microPerDay); d < n {
			daily[d] += float64(e.errors)
		}
	}
	b.Valid = true
	b.Percentile = midRank(daily, t.RecentPerDay)
	return b
}

// windowErrors returns the errors of the records idx inside the recent and
// the baseline window.
func (m *measurer) windowErrors(idx []int32) int64 {
	var n int64
	for _, i := range idx {
		e := &m.in.events[i]
		if !e.relative && e.at > m.clk.priorFrom && e.at <= m.clk.asOf {
			n += int64(e.errors)
		}
	}
	return n
}

// partKey identifies a part number for peer comparison.
func partKey(d *model.DIMM) string {
	if d == nil || d.Identity != model.IdentitySerial || model.NormalizeLabel(d.PartNumber) == "" {
		return ""
	}
	return model.NormalizeLabel(d.Manufacturer) + "|" + model.NormalizeLabel(d.PartNumber)
}

// peerBaselines fills in the second and third layer for all DIMM results.
func peerBaselines(dimms []*DIMMResult, cfg *Config, vendor PartBaseline) {
	groups := map[string][]*DIMMResult{}
	for _, d := range dimms {
		if k := partKey(d.DIMM); k != "" {
			groups[k] = append(groups[k], d)
		}
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		g := groups[k]
		values := make([]float64, len(g))
		with := 0
		for i, d := range g {
			values[i] = float64(d.windowErrors)
			if d.windowErrors > 0 {
				with++
			}
		}
		for _, d := range g {
			d.Baseline.Peers = PeerBaseline{
				PartNumber: d.DIMM.PartNumber, Peers: len(g), WithErrors: with, Errors: d.windowErrors,
				Valid: cfg.MinPeers > 0 && len(g) >= cfg.MinPeers,
			}
			if d.Baseline.Peers.Valid {
				d.Baseline.Peers.Percentile = midRank(values, float64(d.windowErrors))
			}
		}
	}
	if vendor == nil {
		return
	}
	for _, d := range dimms {
		if d.DIMM == nil {
			continue
		}
		if p, ok := vendor.Percentile(d.DIMM, d.windowErrors, cfg.RecentDays+cfg.BaselineDays); ok {
			d.Baseline.Vendor = VendorBaseline{Valid: true, Percentile: min(max(p, 0), 100)}
		}
	}
}
