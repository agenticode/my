package view

import (
	"context"
	"sort"
	"strings"

	"github.com/agenticode/my/internal/decode/spd"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/model"
)

// Lots is the view lots: the DIMMs of one host or of every host of a
// ledger counted by lot key (maker code, part number, manufacturing week),
// with the DIMMs that have a finding set against those that do not. A
// shared week is a lead for the supplier to check, never proof that a lot
// is at fault; the lines of the view say both sides and draw no
// conclusion.
type Lots struct {
	Hosts []string `json:"hosts"`
	// Findings is false when the table has no finding columns: the
	// readings are tied to no DIMM (Why says so), or they come from dump
	// files that have no host.
	Findings bool     `json:"findings"`
	Rows     []LotRow `json:"rows"`
	// ByOrganisation counts the same numbers by ranks and device width.
	ByOrganisation []OrgRow `json:"by_organisation"`
	// Lines are the cluster lines and the closing sentence.
	Lines []Text `json:"lines"`
	Why   *Text  `json:"why,omitzero"`
	// Grade is the worst finding of a DIMM the table counts.
	Grade model.VerdictGrade `json:"grade,omitzero"`
}

// LotRow is one lot key. The last row of a table holds the DIMMs without a
// date code.
type LotRow struct {
	MakerID    string `json:"maker_id,omitzero"`
	Maker      string `json:"maker,omitzero"`
	PartNumber string `json:"part_number,omitzero"`
	MfgDate    string `json:"mfg_date,omitzero"`
	// DateSource is spd, bmc or inventory: where the date of the DIMMs of
	// the row comes from.
	DateSource  string `json:"date_source,omitzero"`
	DIMMs       int    `json:"dimms"`
	WithFinding int    `json:"with_finding"`
	WithUE      int    `json:"with_ue"`
	// Slots are the slots of the DIMMs with a finding (one host); Hosts
	// the hosts with their counts (several hosts).
	Slots []string  `json:"slots,omitzero"`
	Hosts []LotHost `json:"hosts,omitzero"`
}

// LotHost counts the DIMMs of a row on one host.
type LotHost struct {
	Host        string `json:"host"`
	DIMMs       int    `json:"dimms"`
	WithFinding int    `json:"with_finding"`
}

// OrgRow counts DIMMs of one organisation.
type OrgRow struct {
	Organisation string `json:"organisation"`
	DIMMs        int    `json:"dimms"`
	WithFinding  int    `json:"with_finding"`
	WithUE       int    `json:"with_ue"`
}

type lotKey struct{ maker, part, date string }

// lotDIMM is one DIMM the table counts.
type lotDIMM struct {
	host, slot           string
	makerID, maker, part string
	date, dateSource     string
	org                  string
	finding, ue          bool
	grade                model.VerdictGrade
}

// Lots builds the table of date codes over the DIMMs of the hosts given.
func (c *Case) Lots(ctx context.Context, hosts []*engine.HostResult) (*Lots, error) {
	var dimms []lotDIMM
	for _, h := range hosts {
		data, err := c.spdOf(ctx, h.Host)
		if err != nil {
			return nil, err
		}
		fromSPD := map[string]string{}
		for i, r := range data.readings {
			if r.DIMMUID != "" && data.decoded[i].State == spd.StateOK {
				fromSPD[r.DIMMUID] = data.decoded[i].Date
			}
		}
		bmc, err := c.store.BMCMemory(ctx, h.Host)
		if err != nil {
			return nil, err
		}
		for _, d := range h.DIMMs {
			if !d.Present || d.DIMM == nil {
				continue
			}
			x := lotDIMM{host: h.Host, slot: d.Slot, makerID: d.DIMM.ManufacturerID, maker: d.DIMM.Manufacturer, part: d.DIMM.PartNumber,
				date: d.DIMM.MfgDate, org: d.DIMM.Organization()}
			switch {
			case x.date == "":
			case fromSPD[d.UID] == x.date:
				x.dateSource = "spd"
			case len(bmc) > 0:
				x.dateSource = "bmc"
			default:
				x.dateSource = "inventory"
			}
			if d.Verdict != nil {
				x.grade = d.Verdict.Grade
				x.finding = d.Verdict.Grade != model.VerdictOK
			}
			x.ue = d.Features.UE > 0
			dimms = append(dimms, x)
		}
	}
	var names []string
	for _, h := range hosts {
		names = append(names, h.Host)
	}
	return c.lotsOf(names, dimms, true, nil), nil
}

// lotsOfModules counts the readings of a host by date code when none is
// tied to a DIMM: no finding columns.
func (c *Case) lotsOfModules(mods []SPDModule) *Lots {
	var dimms []lotDIMM
	for _, m := range mods {
		if !spd.Read(m.State) {
			continue
		}
		d := m.SPD
		dimms = append(dimms, lotDIMM{makerID: d.ModuleMaker, maker: m.MakerName, part: d.PartNumber, date: d.Date, dateSource: "spd", org: d.Organisation})
	}
	why := c.spdText("lots.no_tie")
	return c.lotsOf(nil, dimms, false, &why)
}

// LotsOfDecodes counts SPD dumps by date code, as my spd FILE... does: no
// host, no finding columns.
func (c *Case) LotsOfDecodes(decoded []spd.Decoded) *Lots {
	var dimms []lotDIMM
	for _, d := range decoded {
		if !spd.Read(d.State) {
			continue
		}
		maker := d.ModuleMakerName
		dimms = append(dimms, lotDIMM{makerID: d.ModuleMaker, maker: maker, part: d.PartNumber, date: d.Date, dateSource: "spd", org: d.Organisation})
	}
	why := c.spdText("lots.files")
	return c.lotsOf(nil, dimms, false, &why)
}

func (c *Case) lotsOf(hosts []string, dimms []lotDIMM, findings bool, why *Text) *Lots {
	l := &Lots{Hosts: hosts, Findings: findings, Why: why, Rows: []LotRow{}, ByOrganisation: []OrgRow{}, Lines: []Text{}}
	if l.Hosts == nil {
		l.Hosts = []string{}
	}
	several := len(hosts) > 1
	rows := map[lotKey]*LotRow{}
	var keys []lotKey
	orgs := map[string]*OrgRow{}
	var orgKeys []string
	total, with := 0, 0
	for _, d := range dimms {
		maker := d.makerID
		if maker == "" {
			maker = d.maker
		}
		k := lotKey{maker, d.part, d.date}
		r, ok := rows[k]
		if !ok {
			r = &LotRow{MakerID: d.makerID, Maker: d.maker, PartNumber: d.part, MfgDate: d.date, DateSource: d.dateSource}
			rows[k] = r
			keys = append(keys, k)
		}
		if r.DateSource != d.dateSource && d.dateSource == "bmc" {
			r.DateSource = "bmc"
		}
		r.DIMMs++
		total++
		if d.finding {
			r.WithFinding++
			with++
			if several {
				addHost(r, d.host, true)
			} else if d.slot != "" {
				r.Slots = append(r.Slots, d.slot)
			}
		} else if several {
			addHost(r, d.host, false)
		}
		if d.ue {
			r.WithUE++
		}
		if worse(d.grade, l.Grade) {
			l.Grade = d.grade
		}
		org := d.org
		if org == "" {
			org = "-"
		}
		o, ok := orgs[org]
		if !ok {
			o = &OrgRow{Organisation: org}
			orgs[org] = o
			orgKeys = append(orgKeys, org)
		}
		o.DIMMs++
		if d.finding {
			o.WithFinding++
		}
		if d.ue {
			o.WithUE++
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if (a.date == "") != (b.date == "") {
			return a.date != ""
		}
		if a.date != b.date {
			return a.date < b.date
		}
		if a.maker != b.maker {
			return a.maker < b.maker
		}
		return a.part < b.part
	})
	for _, k := range keys {
		r := rows[k]
		sort.Slice(r.Slots, func(i, j int) bool { return naturalLess(r.Slots[i], r.Slots[j]) })
		sort.Slice(r.Hosts, func(i, j int) bool { return r.Hosts[i].Host < r.Hosts[j].Host })
		l.Rows = append(l.Rows, *r)
	}
	sort.Strings(orgKeys)
	for _, k := range orgKeys {
		l.ByOrganisation = append(l.ByOrganisation, *orgs[k])
	}
	if findings {
		for _, r := range l.Rows {
			if r.MfgDate != "" && r.WithFinding >= 2 {
				l.Lines = append(l.Lines, c.spdText("lots.cluster", "k", r.WithFinding, "n", with, "date", r.MfgDate, "m", r.DIMMs-r.WithFinding, "h", total-with))
			}
		}
	}
	if len(l.Rows) > 0 {
		l.Lines = append(l.Lines, c.spdText("lots.close"))
	}
	return l
}

func addHost(r *LotRow, host string, finding bool) {
	for i := range r.Hosts {
		if r.Hosts[i].Host == host {
			r.Hosts[i].DIMMs++
			if finding {
				r.Hosts[i].WithFinding++
			}
			return
		}
	}
	h := LotHost{Host: host, DIMMs: 1}
	if finding {
		h.WithFinding = 1
	}
	r.Hosts = append(r.Hosts, h)
}

// worse tells whether grade a is worse than b, by exit code.
func worse(a, b model.VerdictGrade) bool {
	if a == "" {
		return false
	}
	if b == "" {
		return true
	}
	return a.ExitCode() > b.ExitCode()
}

// LotMaker writes the maker of a row: the name with the code in brackets,
// the code alone, or the name a source gave.
func LotMaker(r LotRow) string {
	switch {
	case r.MakerID != "" && spd.MakerName(r.MakerID) != "":
		return spd.MakerLabel(r.MakerID)
	case r.MakerID != "":
		return r.MakerID
	}
	return strings.TrimSpace(r.Maker)
}
