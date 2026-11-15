package view

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/memras"
	"github.com/agenticode/my/internal/model"
)

// BIOS is the check of the BIOS settings of a host that bear on memory
// errors, against a settings profile: what each setting is, what waits for
// a reboot, what the profile expects, and what the DIMMs of the host mean
// for it.
type BIOS struct {
	Host string `json:"host"`
	// Machine is the system vendor and product, BIOSVersion the version
	// the BMC reports.
	Machine     string `json:"machine,omitzero"`
	BIOSVersion string `json:"bios_version,omitzero"`
	// BMC says where the settings were read, when a BMC is on record.
	BMC *BIOSBMC `json:"bmc,omitzero"`
	// None is set when no BIOS settings are on record for the host.
	None bool `json:"none"`
	// Lead are the sentences above the table: what was read, with which
	// profile.
	Lead []Text `json:"lead"`
	// Columns are the heads of the table.
	Columns BIOSColumns `json:"columns"`
	Rows    []BIOSRow   `json:"rows"`
	// Unmapped says, reason by reason, which settings the profile has no
	// attribute for on this BIOS.
	Unmapped []Text `json:"unmapped,omitzero"`
	// Speed says which DIMMs run below their rated speed.
	Speed []Text `json:"speed,omitzero"`
	// Related lists the settings on record the profile maps to nothing.
	Related     []memras.Related `json:"related,omitzero"`
	RelatedHead Text             `json:"related_head,omitzero"`
	// Sources are the public documents the mappings and rules in use were
	// read from.
	Sources     []memras.Source `json:"sources,omitzero"`
	SourcesHead Text            `json:"sources_head,omitzero"`
	// Summary says how many expectations are met. Mismatches counts the
	// settings that are not as expected, PendingMismatches the pending
	// values that will not be.
	Summary           []Text `json:"summary"`
	Mismatches        int    `json:"mismatches"`
	PendingMismatches int    `json:"pending_mismatches"`
	// Check is the outcome as codes, for a program.
	Check *memras.Result `json:"check"`
}

// BIOSBMC says where the BIOS settings were read.
type BIOSBMC struct {
	Address  string    `json:"address,omitzero"`
	Firmware string    `json:"firmware,omitzero"`
	ReadAt   time.Time `json:"read_at"`
	// Live is set when this tool asked the BMC itself.
	Live bool `json:"live"`
}

// BIOSColumns are the heads of the table of settings.
type BIOSColumns struct {
	Setting  string `json:"setting"`
	State    string `json:"state"`
	Value    string `json:"value"`
	Expected string `json:"expected"`
	Check    string `json:"check"`
}

// BIOSRow is one setting.
type BIOSRow struct {
	Setting string `json:"setting"`
	Title   string `json:"title"`
	// Status is what became of the setting, as a code and in words.
	Status     string `json:"status"`
	StatusText string `json:"status_text"`
	// State is what the value means.
	State     string `json:"state,omitzero"`
	StateText string `json:"state_text,omitzero"`
	// Attribute and Value are the attribute of this BIOS and its value.
	Attribute string `json:"attribute,omitzero"`
	Value     string `json:"value,omitzero"`
	// Expected is what the profile expects, "" when it expects nothing.
	Expected     string `json:"expected,omitzero"`
	ExpectedText string `json:"expected_text,omitzero"`
	// Lines say more: the pending value, why a setting has no mapping,
	// what the DIMMs of the host mean for it.
	Lines []Text `json:"lines,omitzero"`
}

// BIOSOptions say how the settings are checked.
type BIOSOptions struct {
	// Profile is the settings profile; nil means the built-in one.
	Profile *memras.Profile
}

// ras renders a message of the settings check.
func (c *Case) ras(code string, kv ...any) Text {
	t := Text{Code: code}
	if len(kv) > 0 {
		t.Args = make(map[string]any, len(kv)/2)
		for i := 0; i+1 < len(kv); i += 2 {
			t.Args[kv[i].(string)] = kv[i+1]
		}
	}
	t.Text = memras.Messages.Text(c.Lang, code, t.Args)
	return t
}

func (c *Case) rasStatement(st model.Statement) Text {
	return Text{Code: st.Code, Args: st.Args, Text: memras.Text(c.Lang, st)}
}

// Modules returns what the settings check needs to know of the DIMMs that
// are in the host: device width, ranks, channel and speeds.
func (c *Case) Modules(ctx context.Context, h *engine.HostResult) ([]memras.Module, error) {
	slots, err := c.store.Slots(ctx, h.Host)
	if err != nil {
		return nil, err
	}
	channel := map[string]string{}
	for _, s := range slots {
		if s.Socket.Valid && s.Channel.Valid {
			imc := "-"
			if s.IMC.Valid {
				imc = fmt.Sprint(s.IMC.V)
			}
			channel[s.Name] = fmt.Sprintf("%d/%s/%d", s.Socket.V, imc, s.Channel.V)
		}
	}
	var out []memras.Module
	for _, d := range h.DIMMs {
		if !d.Present || d.DIMM == nil {
			continue
		}
		out = append(out, memras.Module{Slot: d.Slot, Width: d.DIMM.DeviceWidth.Or(0), Ranks: d.DIMM.Ranks.Or(0), Channel: channel[d.Slot],
			Operating: d.DIMM.ConfiguredSpeedMTs.Or(0), Rated: d.DIMM.SpeedMTs.Or(0)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
	return out, nil
}

// machineOf returns the system manufacturer and the vendor and product of
// a host as one name.
func (c *Case) machineOf(ctx context.Context, host string) (manufacturer, machine string, err error) {
	hosts, err := c.store.Hosts(ctx)
	if err != nil {
		return "", "", err
	}
	for _, h := range hosts {
		if h.Name == host {
			return h.SystemVendor, strings.TrimSpace(h.SystemVendor + " " + h.SystemProduct), nil
		}
	}
	return "", "", nil
}

// CheckBIOS runs the settings check of a host and returns its outcome as
// codes, without sentences.
func (c *Case) CheckBIOS(ctx context.Context, h *engine.HostResult, opts BIOSOptions) (*memras.Result, error) {
	p := opts.Profile
	if p == nil {
		p = memras.Builtin()
	}
	all, err := c.store.BIOSSettings(ctx, h.Host)
	if err != nil {
		return nil, err
	}
	manufacturer, _, err := c.machineOf(ctx, h.Host)
	if err != nil {
		return nil, err
	}
	modules, err := c.Modules(ctx, h)
	if err != nil {
		return nil, err
	}
	return p.Check(memras.Input{Manufacturer: manufacturer, Settings: memras.Latest(all), Modules: modules}), nil
}

// BIOS builds the check of the BIOS memory RAS settings of a host.
func (c *Case) BIOS(ctx context.Context, h *engine.HostResult, opts BIOSOptions) (*BIOS, error) {
	res, err := c.CheckBIOS(ctx, h, opts)
	if err != nil {
		return nil, err
	}
	_, machine, err := c.machineOf(ctx, h.Host)
	if err != nil {
		return nil, err
	}
	v := &BIOS{Host: h.Host, Machine: machine, Check: res, Mismatches: res.Mismatches, Rows: []BIOSRow{}, Lead: []Text{},
		Columns: BIOSColumns{Setting: c.ras("check.col.setting").Text, State: c.ras("check.col.state").Text, Value: c.ras("check.col.value").Text,
			Expected: c.ras("check.col.expected").Text, Check: c.ras("check.col.check").Text}}
	bmcs, err := c.store.BMCs(ctx, h.Host)
	if err != nil {
		return nil, err
	}
	if n := len(bmcs); n > 0 {
		b := bmcs[n-1]
		v.BIOSVersion = b.BIOSVersion
		v.BMC = &BIOSBMC{Address: b.Address, Firmware: b.Firmware, ReadAt: b.ObservedAt, Live: b.Live}
	}
	if res.Settings == 0 {
		v.None = true
		v.Lead = append(v.Lead, c.ras("check.none"))
		v.Summary = []Text{c.ras("check.summary_none")}
		return v, nil
	}

	when := res.ObservedAt.UTC().Format("2006-01-02 15:04") + " UTC"
	if v.BMC != nil && v.BMC.Live && v.BMC.Address != "" {
		v.Lead = append(v.Lead, c.ras("check.read_from", "n", res.Settings, "when", when, "address", v.BMC.Address))
	} else {
		v.Lead = append(v.Lead, c.ras("check.read", "n", res.Settings, "when", when))
	}
	if res.Registry != "" {
		v.Lead = append(v.Lead, c.ras("check.registry", "registry", res.Registry))
	} else {
		v.Lead = append(v.Lead, c.ras("check.no_registry"))
	}
	if res.Vendor != "" {
		v.Lead = append(v.Lead, c.ras("check.profile", "profile", res.Profile, "vendor", res.Vendor, "by", "@check.by."+res.By))
	} else {
		manufacturer := res.Manufacturer
		if manufacturer == "" {
			manufacturer = "@check.unknown"
		}
		v.Lead = append(v.Lead, c.ras("check.no_vendor", "profile", res.Profile, "manufacturer", manufacturer))
	}

	expected, met := 0, 0
	unmapped := map[string][]string{}
	var reasons []string
	for i := range res.Items {
		it := &res.Items[i]
		row := BIOSRow{Setting: it.Setting, Title: memras.SettingText(c.Lang, it.Setting), Status: it.Status, StatusText: memras.StatusText(c.Lang, it.Status),
			State: it.State, StateText: memras.StateText(c.Lang, it.State), Attribute: it.Attribute, Value: it.Value,
			Expected: it.Expected, ExpectedText: memras.StateText(c.Lang, it.Expected)}
		switch it.Status {
		case memras.StatusOK:
			expected, met = expected+1, met+1
		case memras.StatusMismatch:
			expected++
		}
		if it.HasPending && it.Expected != "" && it.PendingState != "" && it.PendingState != it.Expected {
			v.PendingMismatches++
		}
		if it.Status == memras.StatusUnmapped {
			// The settings without a mapping share few reasons: each
			// reason is said once, below the table.
			if _, ok := unmapped[it.Reason]; !ok {
				reasons = append(reasons, it.Reason)
			}
			unmapped[it.Reason] = append(unmapped[it.Reason], row.Title)
		}
		row.Lines = c.biosLines(it)
		v.Rows = append(v.Rows, row)
	}
	for _, reason := range reasons {
		v.Unmapped = append(v.Unmapped, c.ras("check.unmapped", "settings", strings.Join(unmapped[reason], ", "), "reason", memras.ReasonText(c.Lang, reason)))
	}
	switch {
	case expected == 0:
		v.Summary = []Text{c.ras("check.summary_none")}
	case met == expected:
		v.Summary = []Text{c.ras("check.summary_ok", "expected", expected)}
	default:
		v.Summary = []Text{c.ras("check.summary_bad", "n", expected-met, "expected", expected)}
	}
	if v.PendingMismatches > 0 {
		v.Summary = append(v.Summary, c.ras("check.summary_pending", "n", v.PendingMismatches))
	}

	if sp := res.Speed; sp != nil {
		if len(sp.Slow) == 0 {
			v.Speed = append(v.Speed, c.ras("speed.all_rated", "known", sp.Known))
		} else {
			// DIMMs that share both speeds are named together.
			type pair struct{ operating, rated int }
			groups := map[pair][]string{}
			var order []pair
			for _, s := range sp.Slow {
				k := pair{s.Operating, s.Rated}
				if _, ok := groups[k]; !ok {
					order = append(order, k)
				}
				groups[k] = append(groups[k], s.Slot)
			}
			var parts []string
			for _, k := range order {
				names := groups[k]
				if len(names) > 8 {
					names = append(names[:8:8], "...")
				}
				parts = append(parts, c.ras("speed.item", "slots", strings.Join(names, ", "), "operating", fmt.Sprint(k.operating), "rated", fmt.Sprint(k.rated)).Text)
			}
			v.Speed = append(v.Speed, c.ras("speed.lead", "n", len(sp.Slow), "known", sp.Known, "list", strings.Join(parts, "; ")))
			if len(sp.Candidates) == 0 {
				v.Speed = append(v.Speed, c.ras("speed.none"))
			} else {
				v.Speed = append(v.Speed, c.ras("speed.candidates"))
				for _, cand := range sp.Candidates {
					v.Speed = append(v.Speed, c.rasStatement(cand))
				}
			}
		}
	}
	if len(res.Related) > 0 {
		v.Related, v.RelatedHead = res.Related, c.ras("check.related", "n", len(res.Related))
	}
	if len(res.Sources) > 0 {
		v.Sources, v.SourcesHead = res.Sources, c.ras("check.sources")
	}
	return v, nil
}

// biosLines says what there is to say about one setting beyond its value.
func (c *Case) biosLines(it *memras.Item) []Text {
	var out []Text
	switch it.Status {
	case memras.StatusUnmapped:
		return nil
	case memras.StatusAbsent:
		return append(out, c.ras("line.absent"))
	case memras.StatusUnknownValue:
		out = append(out, c.ras("line.unknown_value", "value", it.Value))
	}
	if it.Note != "" {
		note := it.Note
		if c.Lang == i18n.KO && it.NoteKO != "" {
			note = it.NoteKO
		}
		out = append(out, Text{Code: "note", Text: note})
	}
	if it.HasPending {
		if it.PendingState != "" {
			out = append(out, c.ras("line.pending", "value", it.Pending, "state", "@state."+it.PendingState))
		} else {
			out = append(out, c.ras("line.pending_plain", "value", it.Pending))
		}
		if it.Expected != "" && it.PendingState != "" {
			if it.PendingState == it.Expected {
				out = append(out, c.ras("line.pending_ok"))
			} else {
				out = append(out, c.ras("line.pending_bad", "expected", "@state."+it.Expected))
			}
		}
	}
	switch it.Status {
	case memras.StatusMismatch:
		if why := memras.WhyText(c.Lang, it); why != "" {
			out = append(out, c.ras("line.why", "expected", "@state."+it.Expected, "why", why))
		}
		if it.ReadOnly.Or(false) {
			out = append(out, c.ras("line.read_only"))
		}
		if !it.Registry {
			out = append(out, c.ras("line.no_registry"))
		}
	case memras.StatusNoFit:
		out = append(out, c.ras("line.allowed", "values", strings.Join(it.Allowed, ", ")))
	}
	for _, n := range it.Notes {
		out = append(out, c.rasStatement(n))
	}
	return out
}
