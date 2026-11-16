package view

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/agenticode/my/internal/decode/edac"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/memras"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// Inventory lists the DIMMs of a host, one per line: the slot under the
// name of every source, what the module is, how it runs, which memory RAS
// settings of the BIOS are in effect for it, and how many corrected errors
// the operating system and the BMC each have on record for it.
type Inventory struct {
	Host        string `json:"host"`
	Machine     string `json:"machine,omitzero"`
	BIOSVersion string `json:"bios_version,omitzero"`
	// BMC says which BMC was read, when one is on record.
	BMC *BIOSBMC `json:"bmc,omitzero"`
	// Lead are the sentences above the table.
	Lead    []Text           `json:"lead"`
	Columns InventoryColumns `json:"columns"`
	Rows    []InventoryRow   `json:"rows"`
	// DIMMs and Slots count the modules and the slots of the host; Empty
	// the slots without a module, which are listed only when asked for.
	DIMMs int `json:"dimms"`
	Slots int `json:"slots"`
	Empty int `json:"empty_slots"`
	// RASCommon is the RAS text of the modules when it is the same for
	// every one of them; the table then has no column for it.
	RASCommon string `json:"ras_common,omitzero"`
	// Legend says what the columns hold.
	Legend []Text `json:"legend"`
	// Notes say what the table cannot show.
	Notes []Text `json:"notes,omitzero"`
	// Sides is the comparison of what the operating system and the BMC
	// each recorded.
	Sides *Sides `json:"sides"`
}

// InventoryColumns are the heads of the table.
type InventoryColumns struct {
	Slot   string `json:"slot"`
	Serial string `json:"serial"`
	Maker  string `json:"maker"`
	Part   string `json:"part"`
	Size   string `json:"size"`
	Org    string `json:"org"`
	Speed  string `json:"speed"`
	RAS    string `json:"ras"`
	CEOS   string `json:"ce_os"`
	CEBMC  string `json:"ce_bmc"`
	// Empty is what stands in the line of a slot without a module.
	Empty string `json:"empty"`
}

// InventoryRow is one slot with its module.
type InventoryRow struct {
	Slot string `json:"slot"`
	// BMCNames are the names the BMC has for the slot where they differ
	// from Slot; EDACName is the label or the position EDAC has for it.
	BMCNames []string `json:"bmc_names,omitzero"`
	EDACName string   `json:"edac_name,omitzero"`
	// Populated is unset for an empty slot; the fields below are then empty.
	Populated bool   `json:"populated"`
	DIMMUID   string `json:"dimm_uid,omitzero"`

	Maker      string `json:"maker,omitzero"`
	PartNumber string `json:"part_number,omitzero"`
	Serial     string `json:"serial,omitzero"`
	SizeBytes  int64  `json:"size_bytes,omitzero"`
	Size       string `json:"size,omitzero"`
	// Type is the memory type and the module type, "DDR4 RDIMM".
	Type  string         `json:"type,omitzero"`
	Ranks model.Opt[int] `json:"ranks,omitzero"`
	// Width is the DRAM device width in bits. WidthSource names a field
	// of the BMC vendor it was read from; it is empty for a width the
	// EDAC driver or another host source reported.
	Width       model.Opt[int] `json:"device_width,omitzero"`
	WidthSource string         `json:"device_width_source,omitzero"`
	// Org is ranks and width in the usual short form: 2Rx4, 2R, x4.
	Org string `json:"org,omitzero"`
	// SpeedOperating and SpeedRated are the speed in use and the speed the
	// module is rated for, in MT/s.
	SpeedOperating model.Opt[int] `json:"speed_operating,omitzero"`
	SpeedRated     model.Opt[int] `json:"speed_rated,omitzero"`
	Speed          string         `json:"speed,omitzero"`

	// Settings are the memory RAS settings of the BIOS in effect for the
	// module, and RAS the same as one short text.
	Settings []RowSetting `json:"settings,omitzero"`
	RAS      string       `json:"ras,omitzero"`

	// CEOS counts the corrected errors in the records of the operating
	// system, CEBMC the entries about corrected errors in the logs of the
	// BMC, both over the period Sides names. A side that was not read has
	// no count.
	CEOS  model.Opt[int64] `json:"ce_os,omitzero"`
	CEBMC model.Opt[int64] `json:"ce_bmc,omitzero"`
	// BMCCount is the BMC's own count of the errors of the module.
	BMCCount *BMCCount `json:"bmc_count,omitzero"`
	// Events are the newest entries of the logs of the BMC about the slot.
	Events []RowEvent `json:"bmc_events,omitzero"`
	// Lines are the remarks below the line of the module.
	Lines []Text `json:"lines,omitzero"`
}

// RowSetting is one memory RAS setting as it bears on one module.
type RowSetting struct {
	Setting string `json:"setting"`
	State   string `json:"state"`
	// Effect is "none" for a setting that is on and does nothing for this
	// module (ADDDC on a module of x8 devices), "unknown" when the device
	// width that decides it is not on record, and "" otherwise.
	Effect string `json:"effect,omitzero"`
	Text   string `json:"text"`
}

// BMCCount is what the BMC counts for a module (Redfish MemoryMetrics).
type BMCCount struct {
	// Lifetime counts over the life of the module, Current since the last
	// reset of the system or of the counters.
	CELifetime model.Opt[int64] `json:"ce_lifetime,omitzero"`
	CECurrent  model.Opt[int64] `json:"ce_current,omitzero"`
	UELifetime model.Opt[int64] `json:"ue_lifetime,omitzero"`
	UECurrent  model.Opt[int64] `json:"ue_current,omitzero"`
	// CurrentSince is the earliest time the current counts can have
	// started at.
	CurrentSince time.Time `json:"current_since,omitzero"`
	ReadAt       time.Time `json:"read_at"`
}

// RowEvent is one entry of a BMC log about a slot.
type RowEvent struct {
	At       time.Time      `json:"at"`
	Severity model.Severity `json:"severity"`
	Text     string         `json:"text"`
	Source   string         `json:"source"`
}

// InventoryOptions say what the inventory lists.
type InventoryOptions struct {
	// All lists the empty slots as well.
	All bool
	// Profile is the settings profile the RAS column is read with; nil
	// means the built-in one.
	Profile *memras.Profile
}

// Sides is the comparison of the two sides that record the corrected
// errors of a host: the operating system (rasdaemon, the kernel log, the
// EDAC counters) and the BMC (its event logs, and its own counts where it
// keeps them). A difference is reported with what the records show that
// can explain it. These are candidates: nothing here establishes a cause.
type Sides struct {
	// OS and BMC say what each side covers.
	OS  SideCover `json:"os"`
	BMC SideCover `json:"bmc"`
	// From and To bound the period both sides cover. They are zero when
	// one side was not read or the two share no period; the counts are
	// then those of everything each side has on record.
	From time.Time `json:"from,omitzero"`
	To   time.Time `json:"to,omitzero"`
	// Common is set when From and To are.
	Common bool `json:"common"`
	// Lead says what was compared.
	Lead []Text `json:"lead"`
	// Rows are the modules with a corrected error on either side.
	Rows []SidesRow `json:"rows"`
	// Candidates are the possible reasons for the differences, grouped
	// by the kind of difference.
	Candidates []CandidateGroup `json:"candidates,omitzero"`
}

// SideCover says what one side covers.
type SideCover struct {
	// Read is set when a source of the side was read.
	Read bool `json:"read"`
	// Sources names the sources of the side that were read.
	Sources []string `json:"sources,omitzero"`
	// From and To bound what the records of the side cover, as far as the
	// ledger can tell.
	From time.Time `json:"from,omitzero"`
	To   time.Time `json:"to,omitzero"`
}

// Outcomes of the comparison for one module.
const (
	SidesBoth    = "both"
	SidesOSOnly  = "os-only"
	SidesBMCOnly = "bmc-only"
	SidesCounted = "bmc-count-only"
)

// SidesRow is the comparison for one module.
type SidesRow struct {
	Slot string `json:"slot"`
	// OS and BMC are the counts of the table; Matched counts the entries
	// of the BMC that stand for an error the OS recorded at that time.
	OS      int64  `json:"ce_os"`
	BMC     int64  `json:"ce_bmc"`
	Matched int64  `json:"matched,omitzero"`
	Outcome string `json:"outcome"`
	Text    Text   `json:"text"`
	// Count compares the BMC's own count since the last reset with what
	// the OS recorded from then on, when both are known.
	Count *CountCompare `json:"count,omitzero"`
}

// CountCompare sets the BMC's count since the last reset of the system
// against the errors the OS recorded since then.
type CountCompare struct {
	Since time.Time `json:"since"`
	BMC   int64     `json:"bmc"`
	OS    int64     `json:"os"`
	Text  Text      `json:"text"`
}

// CandidateGroup lists what can explain one kind of difference.
type CandidateGroup struct {
	// Kind is os-only, bmc-only or count.
	Kind  string `json:"kind"`
	Head  Text   `json:"head"`
	Items []Text `json:"items"`
}

// inv renders a message of the inventory.
func (c *Case) inv(code string, kv ...any) Text {
	t := Text{Code: code}
	if len(kv) > 0 {
		t.Args = make(map[string]any, len(kv)/2)
		for i := 0; i+1 < len(kv); i += 2 {
			t.Args[kv[i].(string)] = kv[i+1]
		}
	}
	t.Text = InventoryMessages.Text(c.Lang, code, t.Args)
	return t
}

// osKinds are the adapters that read what the operating system recorded.
var osKinds = map[string]bool{"rasdaemon": true, "klog": true, "edac-sysfs": true, "mcelog": true, "synth": true}

// bmcKinds are the adapters that read what a BMC recorded.
var bmcKinds = map[string]bool{"redfish": true, "sel": true}

// sourceKind returns the adapter of a record source: "klog" for "klog/edac".
func sourceKind(source string) string {
	kind, _, _ := strings.Cut(source, "/")
	return kind
}

// stamp writes a time for a sentence.
func stamp(t time.Time) string { return t.UTC().Format("2006-01-02 15:04") + " UTC" }

// sideEvent is what the comparison keeps of a record of a corrected error.
type sideEvent struct {
	dupOf int64
	slot  string
	at    time.Time
	bmc   bool
	// errors is the number of errors the record stands for.
	errors int64
}

// sideData is what the comparison reads from the ledger.
type sideData struct {
	os, bmc  SideCover
	events   []sideEvent           // corrected errors of both sides, of a slot
	side     map[int64]bool        // the records of the BMC
	newest   map[string][]RowEvent // slot -> newest BMC entries of any severity
	bmcs     []model.BMC
	logLimit time.Time
	latest   []model.Source // sources of the latest scan with an OS source
}

// readSides reads what each side has on record for a host.
func (c *Case) readSides(ctx context.Context, host string) (*sideData, error) {
	d := &sideData{}
	scans, err := c.store.Scans(ctx)
	if err != nil {
		return nil, err
	}
	osNames, bmcNames := map[string]bool{}, map[string]bool{}
	var latestOS time.Time
	// Whether a scan of the host had a source of the BMC at all, in any
	// state: without one, no record is the BMC's.
	anyBMC := false
	for i := range scans {
		sc := &scans[i]
		if sc.Host != host {
			continue
		}
		srcs, err := c.store.Sources(ctx, sc.ID)
		if err != nil {
			return nil, err
		}
		hasOS := false
		for _, src := range srcs {
			if bmcKinds[src.Kind] {
				anyBMC = true
			}
			switch src.Status {
			case model.SourceOK, model.SourcePartial, model.SourceEmpty:
			default:
				continue
			}
			switch {
			case osKinds[src.Kind]:
				hasOS = true
				d.os.Read, osNames[src.Kind] = true, true
				if sc.StartedAt.After(d.os.To) {
					d.os.To = sc.StartedAt
				}
			case bmcKinds[src.Kind]:
				d.bmc.Read, bmcNames[src.Kind] = true, true
				if sc.StartedAt.After(d.bmc.To) {
					d.bmc.To = sc.StartedAt
				}
			}
		}
		if hasOS && !sc.StartedAt.Before(latestOS) {
			latestOS, d.latest = sc.StartedAt, srcs
		}
	}
	for k := range osNames {
		d.os.Sources = append(d.os.Sources, k)
	}
	for k := range bmcNames {
		d.bmc.Sources = append(d.bmc.Sources, k)
	}
	sort.Strings(d.os.Sources)
	sort.Strings(d.bmc.Sources)

	// The records. Those of the OS begin with the first of them, or with
	// the first boot on record; those of the BMC with the oldest entry of
	// its logs.
	early := func(dst *time.Time, t time.Time) {
		if !t.IsZero() && t.Year() > 1970 && (dst.IsZero() || t.Before(*dst)) {
			*dst = t
		}
	}
	// The records of a host in an error storm are millions, and the
	// comparison keeps a few numbers of each corrected error. A record of
	// the OS that repeats another one counts only when that other one is
	// the BMC's: on a host without a BMC source such records are not kept.
	// Should a record of the BMC turn up all the same, the records are read
	// again with all of them.
	read := func(keepRepeats bool) (sawBMC bool, err error) {
		d.side, d.newest, d.events = map[int64]bool{}, map[string][]RowEvent{}, nil
		d.os.From, d.bmc.From, d.logLimit = time.Time{}, time.Time{}, time.Time{}
		slots := map[string]string{}
		for e, err := range c.store.Events(ctx, store.EventFilter{Host: host, WithDuplicates: true}) {
			if err != nil {
				return false, err
			}
			kind := sourceKind(e.Source)
			isOS, isBMC := osKinds[kind], bmcKinds[kind]
			if !isOS && !isBMC {
				continue
			}
			if isBMC {
				d.side[e.ID], sawBMC = true, true
			}
			if e.TimeQuality != model.TimeRelative {
				if isOS {
					early(&d.os.From, e.ErrorDT)
				} else {
					early(&d.bmc.From, e.ErrorDT)
				}
			}
			if isBMC && e.Slot != "" && (e.Source == "redfish/log" || kind == "sel") {
				// An entry of a log; what the BMC says about the state of a
				// module at the time it was read is no entry.
				list := append(d.newest[e.Slot], RowEvent{At: e.ErrorDT, Severity: e.ErrorSeverity, Text: e.ErrorType, Source: e.Source})
				if len(list) > maxRowEvents {
					list = list[len(list)-maxRowEvents:]
				}
				d.newest[e.Slot] = list
			}
			if isBMC && strings.Contains(strings.ToLower(e.ErrorType), "logging limit") {
				early(&d.logLimit, e.ErrorDT)
			}
			// What the comparison counts: corrected errors of a slot.
			if e.ErrorSeverity != model.SevCE || e.Kind != model.KindError || e.Slot == "" || (isOS && e.DupOf != 0 && !keepRepeats) {
				continue
			}
			n := int64(e.Count - e.Covered)
			if n < 0 {
				n = 0
			}
			slot, ok := slots[e.Slot]
			if !ok {
				slot = e.Slot
				slots[slot] = slot
			}
			d.events = append(d.events, sideEvent{dupOf: e.DupOf, slot: slot, at: e.ErrorDT, bmc: isBMC, errors: n})
		}
		return sawBMC, nil
	}
	sawBMC, err := read(anyBMC)
	if err != nil {
		return nil, err
	}
	if sawBMC && !anyBMC {
		if _, err := read(true); err != nil {
			return nil, err
		}
	}
	boots, err := c.store.Boots(ctx, host)
	if err != nil {
		return nil, err
	}
	if d.os.Read {
		for _, b := range boots {
			if b.StartQuality != model.TimeRelative {
				early(&d.os.From, b.StartedAt)
			}
		}
	}
	if d.bmcs, err = c.store.BMCs(ctx, host); err != nil {
		return nil, err
	}
	sort.SliceStable(d.bmcs, func(i, j int) bool { return d.bmcs[i].ObservedAt.Before(d.bmcs[j].ObservedAt) })
	if n := len(d.bmcs); n > 0 {
		// The reading says when it was made; the scan that brought it into
		// the ledger may be much later.
		d.bmc.To = d.bmcs[n-1].ObservedAt
	}
	for _, b := range d.bmcs {
		for _, l := range b.Logs {
			if l.Entries > 0 {
				early(&d.bmc.From, l.First)
			}
		}
	}
	return d, nil
}

// maxRowEvents is how many entries of the BMC logs a line of the inventory
// shows.
const maxRowEvents = 2

// Inventory builds the list of the DIMMs of a host.
func (c *Case) Inventory(ctx context.Context, h *engine.HostResult, opts InventoryOptions) (*Inventory, error) {
	_, machine, err := c.machineOf(ctx, h.Host)
	if err != nil {
		return nil, err
	}
	v := &Inventory{Host: h.Host, Machine: machine, Rows: []InventoryRow{}, Lead: []Text{},
		Columns: InventoryColumns{Slot: c.inv("inv.col.slot").Text, Serial: c.inv("inv.col.serial").Text, Maker: c.inv("inv.col.maker").Text, Part: c.inv("inv.col.part").Text,
			Size: c.inv("inv.col.size").Text, Org: c.inv("inv.col.org").Text, Speed: c.inv("inv.col.speed").Text, RAS: c.inv("inv.col.ras").Text,
			CEOS: c.inv("inv.col.ce_os").Text, CEBMC: c.inv("inv.col.ce_bmc").Text, Empty: c.inv("inv.empty").Text}}
	data, err := c.readSides(ctx, h.Host)
	if err != nil {
		return nil, err
	}
	if n := len(data.bmcs); n > 0 {
		b := data.bmcs[n-1]
		v.BIOSVersion = b.BIOSVersion
		v.BMC = &BIOSBMC{Address: b.Address, Firmware: b.Firmware, ReadAt: b.ObservedAt, Live: b.Live}
	}
	slots, err := c.store.Slots(ctx, h.Host)
	if err != nil {
		return nil, err
	}
	readings, err := c.store.BMCMemory(ctx, h.Host)
	if err != nil {
		return nil, err
	}
	// The newest reading of the BMC for each slot.
	bmcOf := map[string]*model.BMCMemory{}
	for i := range readings {
		m := &readings[i]
		if m.Slot == "" {
			continue
		}
		if cur := bmcOf[m.Slot]; cur == nil || !m.ObservedAt.Before(cur.ObservedAt) {
			bmcOf[m.Slot] = m
		}
	}
	check, err := c.CheckBIOS(ctx, h, BIOSOptions{Profile: opts.Profile})
	if err != nil {
		return nil, err
	}
	sides, counts := c.compareSides(data, check, bmcOf)
	v.Sides = sides

	present := map[string]*engine.DIMMResult{}
	for _, d := range h.DIMMs {
		if d.Present && d.Slot != "" {
			present[d.Slot] = d
		}
	}
	smbios, edacRead := false, false
	widthUnknown := 0
	listed := map[string]bool{}
	row := func(name string, sl *model.Slot) {
		listed[name] = true
		d := present[name]
		r := InventoryRow{Slot: name, Populated: d != nil}
		if sl != nil {
			smbios = smbios || sl.SMBIOSHandle.Valid
			if sl.EdacMC.Valid {
				edacRead = true
				switch {
				case !edac.PlaceholderLabel(sl.EdacLabel) && model.NormalizeLabel(sl.EdacLabel) != model.NormalizeLabel(name):
					r.EDACName = strings.TrimSpace(sl.EdacLabel)
				case sl.EdacLocation != "":
					r.EDACName = fmt.Sprintf("mc%d %s", sl.EdacMC.V, sl.EdacLocation)
				default:
					r.EDACName = fmt.Sprintf("mc%d", sl.EdacMC.V)
				}
			}
		}
		m := bmcOf[name]
		if m != nil {
			seen := map[string]bool{model.NormalizeLabel(name): true}
			for _, n := range []string{m.DeviceLocator, m.ServiceLabel, m.ResourceID, m.Label} {
				if k := model.NormalizeLabel(n); k != "" && !seen[k] {
					seen[k] = true
					r.BMCNames = append(r.BMCNames, n)
				}
			}
			if m.HasCounts() {
				r.BMCCount = &BMCCount{CELifetime: m.CELifetime, CECurrent: m.CECurrent, UELifetime: m.UELifetime, UECurrent: m.UECurrent,
					CurrentSince: m.CurrentSince, ReadAt: m.ObservedAt}
			}
		}
		if d == nil {
			v.Empty++
			if !opts.All {
				return
			}
			c.inventoryLines(&r)
			v.Rows = append(v.Rows, r)
			return
		}
		v.DIMMs++
		r.DIMMUID = d.UID
		if dm := d.DIMM; dm != nil {
			r.Maker, r.PartNumber, r.Serial = dm.Manufacturer, dm.PartNumber, dm.Serial
			if r.SizeBytes = dm.SizeBytes; dm.SizeBytes > 0 {
				r.Size = sizeText(dm.SizeBytes)
			}
			r.Type = strings.TrimSpace(dm.MemoryType + " " + dm.ModuleType)
			r.Ranks, r.Width = dm.Ranks, dm.DeviceWidth
			r.SpeedOperating, r.SpeedRated = dm.ConfiguredSpeedMTs, dm.SpeedMTs
		}
		if m != nil && !r.Width.Valid && m.DeviceWidth.Valid {
			r.Width, r.WidthSource = m.DeviceWidth, m.DeviceWidthSource
		}
		if !r.Width.Valid {
			widthUnknown++
		}
		switch {
		case r.Ranks.Valid && r.Width.Valid:
			r.Org = fmt.Sprintf("%dRx%d", r.Ranks.V, r.Width.V)
		case r.Ranks.Valid:
			r.Org = fmt.Sprintf("%dR", r.Ranks.V)
		case r.Width.Valid:
			r.Org = fmt.Sprintf("x%d", r.Width.V)
		}
		switch {
		case r.SpeedOperating.Valid && r.SpeedRated.Valid:
			r.Speed = fmt.Sprintf("%d/%d", r.SpeedOperating.V, r.SpeedRated.V)
		case r.SpeedOperating.Valid:
			r.Speed = fmt.Sprintf("%d/-", r.SpeedOperating.V)
		case r.SpeedRated.Valid:
			r.Speed = fmt.Sprintf("-/%d", r.SpeedRated.V)
		}
		r.Settings = c.rowSettings(check, r.Width)
		var parts []string
		for _, s := range r.Settings {
			parts = append(parts, s.Text)
		}
		r.RAS = strings.Join(parts, ", ")
		if n, ok := counts[name]; ok {
			if sides.OS.Read {
				r.CEOS = model.Some(n.os)
			}
			if sides.BMC.Read {
				r.CEBMC = model.Some(n.bmc)
			}
		} else {
			if sides.OS.Read {
				r.CEOS = model.Some[int64](0)
			}
			if sides.BMC.Read {
				r.CEBMC = model.Some[int64](0)
			}
		}
		r.Events = data.newest[name]
		c.inventoryLines(&r)
		v.Rows = append(v.Rows, r)
	}
	for i := range slots {
		row(slots[i].Name, &slots[i])
	}
	// DIMMs in places no inventory names: where an error record put them.
	var loose []string
	for name := range present {
		if !listed[name] {
			loose = append(loose, name)
		}
	}
	sort.Strings(loose)
	for _, name := range loose {
		row(name, nil)
	}
	v.Slots = v.DIMMs + v.Empty

	// What was read.
	if v.Empty > 0 && !opts.All {
		v.Lead = append(v.Lead, c.inv("inv.count_empty", "dimms", v.DIMMs, "empty", v.Empty))
	} else {
		v.Lead = append(v.Lead, c.inv("inv.count", "dimms", v.DIMMs, "slots", v.Slots))
	}
	var from []string
	if smbios {
		from = append(from, "SMBIOS")
	}
	if edacRead {
		from = append(from, "EDAC")
	}
	if v.BMC != nil {
		b := c.inv("inv.source_bmc", "when", stamp(v.BMC.ReadAt)).Text
		if v.BMC.Address != "" {
			b = c.inv("inv.source_bmc_at", "address", v.BMC.Address, "when", stamp(v.BMC.ReadAt)).Text
		}
		from = append(from, b)
	}
	if len(from) > 0 {
		v.Lead = append(v.Lead, c.inv("inv.sources", "sources", strings.Join(from, ", ")))
	} else {
		v.Lead = append(v.Lead, c.inv("inv.no_inventory"))
	}
	// The settings act on every module alike unless the modules differ in
	// what decides it: then each line says what holds for its module.
	same, first := true, true
	for i := range v.Rows {
		r := &v.Rows[i]
		if !r.Populated {
			continue
		}
		if first {
			v.RASCommon, first = r.RAS, false
		} else if r.RAS != v.RASCommon {
			same = false
		}
	}
	if !same {
		v.RASCommon = ""
	}
	switch {
	case check.Settings == 0 && biosWithoutMemory(data.bmcs):
		v.Lead = append(v.Lead, c.inv("inv.ras_none_memory"))
	case check.Settings == 0:
		v.Lead = append(v.Lead, c.inv("inv.ras_none"))
	case check.Vendor == "":
		v.Lead = append(v.Lead, c.inv("inv.ras_unmapped"))
	case v.RASCommon != "":
		v.Lead = append(v.Lead, c.inv("inv.ras_common", "ras", v.RASCommon))
	case same:
		v.Lead = append(v.Lead, c.inv("inv.ras_off"))
	}

	if v.RASCommon == "" && !same {
		v.Legend = append(v.Legend, c.inv("inv.legend_ras"))
	} else {
		v.Legend = append(v.Legend, c.inv("inv.legend"))
	}
	switch {
	case sides.Common:
		v.Legend = append(v.Legend, c.inv("inv.legend_ce", "from", stamp(sides.From), "to", stamp(sides.To)))
	case sides.OS.Read && sides.BMC.Read:
		v.Legend = append(v.Legend, c.inv("inv.legend_ce_apart"))
	case sides.OS.Read:
		v.Legend = append(v.Legend, c.inv("inv.legend_ce_os"))
	case sides.BMC.Read:
		v.Legend = append(v.Legend, c.inv("inv.legend_ce_bmc"))
	}
	if widthUnknown > 0 {
		v.Notes = append(v.Notes, c.inv("inv.note_width", "n", widthUnknown, "total", v.DIMMs))
	}
	return v, nil
}

// rowSettings returns the memory RAS settings in effect for a module of
// the given device width: the protections that are on. A setting the
// profile cannot read, or that is off, is not listed; "my bios" has all.
func (c *Case) rowSettings(check *memras.Result, width model.Opt[int]) []RowSetting {
	var out []RowSetting
	add := func(setting, state, effect, code string) {
		out = append(out, RowSetting{Setting: setting, State: state, Effect: effect, Text: c.inv(code).Text})
	}
	if it := check.Item(memras.PatrolScrub); it != nil && it.State == "on" {
		add(memras.PatrolScrub, "on", "", "inv.ras.scrub")
	}
	adddc := false
	if it := check.Item(memras.ADDDC); it != nil && it.State == "on" {
		adddc = true
	}
	if it := check.Item(memras.MemoryMode); it != nil {
		switch it.State {
		case "adddc":
			adddc = true
		case "mirroring", "rank-sparing", "advanced-ecc", "fault-resilient":
			add(memras.MemoryMode, it.State, "", "inv.ras.mode."+it.State)
		}
	}
	if adddc {
		// ADDDC works on modules of x4 devices.
		switch {
		case !width.Valid:
			add(memras.ADDDC, "on", "unknown", "inv.ras.adddc_unknown")
		case width.V == 4:
			add(memras.ADDDC, "on", "", "inv.ras.adddc")
		default:
			out = append(out, RowSetting{Setting: memras.ADDDC, State: "on", Effect: "none", Text: c.inv("inv.ras.adddc_none", "width", fmt.Sprint(width.V)).Text})
		}
	}
	if it := check.Item(memras.PPR); it != nil && it.State == "on" {
		add(memras.PPR, "on", "", "inv.ras.ppr")
	}
	return out
}

// inventoryLines writes the remarks below the line of a module: its type
// with the names other sources have for the slot, the count of the BMC
// when it counted something, and the newest entries of its logs.
func (c *Case) inventoryLines(r *InventoryRow) {
	var parts []string
	if r.Type != "" {
		parts = append(parts, r.Type)
	}
	if len(r.BMCNames) > 0 {
		parts = append(parts, c.inv("inv.line.bmc_names", "names", strings.Join(r.BMCNames, ", ")).Text)
	}
	if r.EDACName != "" {
		parts = append(parts, c.inv("inv.line.edac_name", "name", r.EDACName).Text)
	}
	if len(parts) > 0 {
		r.Lines = append(r.Lines, Text{Code: "inv.line.module", Text: strings.Join(parts, "; ")})
	}
	if b := r.BMCCount; b != nil && (b.CELifetime.Or(0) > 0 || b.CECurrent.Or(0) > 0) {
		var counts []string
		if b.CELifetime.Valid {
			counts = append(counts, c.inv("inv.count.lifetime", "n", b.CELifetime.V).Text)
		}
		if b.CECurrent.Valid {
			if b.CurrentSince.IsZero() {
				counts = append(counts, c.inv("inv.count.current", "n", b.CECurrent.V).Text)
			} else {
				counts = append(counts, c.inv("inv.count.current_since", "n", b.CECurrent.V, "since", stamp(b.CurrentSince)).Text)
			}
		}
		r.Lines = append(r.Lines, c.inv("inv.line.count", "counts", strings.Join(counts, ", ")))
	}
	for _, e := range r.Events {
		r.Lines = append(r.Lines, c.inv("inv.line.event", "when", stamp(e.At), "text", e.Text))
	}
}

// sideCount is what the two sides have for one slot.
type sideCount struct {
	os, bmc, matched int64
}

// compareSides sets the corrected errors of the two sides side by side.
func (c *Case) compareSides(d *sideData, check *memras.Result, bmcOf map[string]*model.BMCMemory) (*Sides, map[string]sideCount) {
	s := &Sides{OS: d.os, BMC: d.bmc, Rows: []SidesRow{}, Lead: []Text{}}
	// The period both cover.
	if d.os.Read && d.bmc.Read && !d.os.From.IsZero() && !d.bmc.From.IsZero() {
		from, to := d.os.From, d.os.To
		if d.bmc.From.After(from) {
			from = d.bmc.From
		}
		if d.bmc.To.Before(to) {
			to = d.bmc.To
		}
		if !to.Before(from) {
			s.From, s.To, s.Common = from, to, true
		}
	}
	in := func(t time.Time) bool { return !s.Common || (!t.Before(s.From) && !t.After(s.To)) }

	// An OS record counts unless it repeats another OS record. An entry of
	// the BMC counts unless it repeats another entry of the BMC; one that
	// repeats an OS record is the BMC's report of that error.
	osErrors := func(e sideEvent) int64 {
		if e.bmc || (e.dupOf != 0 && !d.side[e.dupOf]) {
			return 0
		}
		return e.errors
	}
	counts := map[string]sideCount{}
	for _, e := range d.events {
		if e.slot == "" || !in(e.at) {
			continue
		}
		n := counts[e.slot]
		n.os += osErrors(e)
		switch {
		case !e.bmc:
		case e.dupOf == 0:
			n.bmc++
		case !d.side[e.dupOf]:
			n.bmc++
			n.matched++
		}
		counts[e.slot] = n
	}

	// What was compared.
	switch {
	case !d.os.Read && !d.bmc.Read:
		s.Lead = append(s.Lead, c.inv("sides.nothing"))
	case !d.bmc.Read:
		s.Lead = append(s.Lead, c.inv("sides.no_bmc"))
	case !d.os.Read:
		s.Lead = append(s.Lead, c.inv("sides.no_os"))
	case s.Common:
		s.Lead = append(s.Lead, c.inv("sides.window", "from", stamp(s.From), "to", stamp(s.To), "os_from", stamp(d.os.From), "bmc_from", stamp(d.bmc.From)))
	case d.bmc.From.IsZero():
		s.Lead = append(s.Lead, c.inv("sides.bmc_empty"))
	case d.os.From.IsZero():
		s.Lead = append(s.Lead, c.inv("sides.os_empty"))
	default:
		s.Lead = append(s.Lead, c.inv("sides.apart", "os_from", stamp(d.os.From), "os_to", stamp(d.os.To), "bmc_from", stamp(d.bmc.From), "bmc_to", stamp(d.bmc.To)))
	}
	var bmc *model.BMC
	if n := len(d.bmcs); n > 0 {
		bmc = &d.bmcs[n-1]
		if off := bmc.ClockOffset; off.Valid && (off.V >= 60 || off.V <= -60) {
			code, secs := "sides.clock_ahead", off.V
			if secs < 0 {
				code, secs = "sides.clock_behind", -secs
			}
			// An offset of days is told in days: a BMC whose clock was
			// never set can be years off.
			if secs >= 2*86400 {
				s.Lead = append(s.Lead, c.inv(code+"_days", "days", (secs+43200)/86400))
			} else {
				s.Lead = append(s.Lead, c.inv(code, "minutes", (secs+30)/60))
			}
		}
	}
	if !d.os.Read || !d.bmc.Read {
		return s, counts
	}
	// The BMC's own counts: whether it keeps any, and what they cover.
	if len(bmcOf) > 0 {
		counted, life := false, false
		states := map[string]bool{}
		for _, m := range bmcOf {
			counted = counted || m.HasCounts()
			life = life || m.CELifetime.Valid
			if m.Metrics != "" && m.Metrics != "ok" {
				states[m.Metrics] = true
			}
		}
		switch {
		case !counted && len(states) > 0:
			list := make([]string, 0, len(states))
			for st := range states {
				list = append(list, st)
			}
			sort.Strings(list)
			s.Lead = append(s.Lead, c.inv("sides.no_metrics_state", "state", strings.Join(list, ", ")))
		case !counted:
			s.Lead = append(s.Lead, c.inv("sides.no_metrics"))
		case life:
			s.Lead = append(s.Lead, c.inv("sides.lifetime"))
		}
	}

	// The modules with a corrected error on either side.
	names := make([]string, 0, len(counts))
	// A module the BMC counted errors for since the last reset, with no
	// record on either side. A lifetime count alone says nothing about
	// this period.
	for name, m := range bmcOf {
		if n := counts[name]; n.os == 0 && n.bmc == 0 && m.CECurrent.Or(0) > 0 {
			counts[name] = n
		}
	}
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	kinds := map[string]bool{}
	for _, name := range names {
		n := counts[name]
		r := SidesRow{Slot: name, OS: n.os, BMC: n.bmc, Matched: n.matched}
		switch {
		case n.os > 0 && n.bmc > 0 && n.matched > 0:
			r.Outcome, r.Text = SidesBoth, c.inv("sides.row.both_matched", "slot", name, "os", n.os, "bmc", n.bmc, "matched", n.matched)
		case n.os > 0 && n.bmc > 0:
			r.Outcome, r.Text = SidesBoth, c.inv("sides.row.both", "slot", name, "os", n.os, "bmc", n.bmc)
		case n.os > 0:
			r.Outcome, r.Text = SidesOSOnly, c.inv("sides.row.os_only", "slot", name, "os", n.os)
		case n.bmc > 0:
			r.Outcome, r.Text = SidesBMCOnly, c.inv("sides.row.bmc_only", "slot", name, "bmc", n.bmc)
		case bmcOf[name] != nil && bmcOf[name].CECurrent.Or(0) > 0:
			r.Outcome, r.Text = SidesCounted, c.inv("sides.row.counted", "slot", name, "n", bmcOf[name].CECurrent.V)
		default:
			continue // records outside the period, or of another severity
		}
		kinds[r.Outcome] = true
		// The BMC's own count since the last reset, against what the OS
		// recorded from then on. It needs the time of the reset, and OS
		// records that reach back to it.
		if m := bmcOf[name]; m != nil && m.CECurrent.Valid && !m.CurrentSince.IsZero() && !d.os.From.IsZero() && !d.os.From.After(m.CurrentSince) {
			var os int64
			for _, e := range d.events {
				if e.slot == name && !e.at.Before(m.CurrentSince) && !e.at.After(m.ObservedAt) {
					os += osErrors(e)
				}
			}
			cmp := &CountCompare{Since: m.CurrentSince, BMC: m.CECurrent.V, OS: os}
			if cmp.BMC == cmp.OS {
				cmp.Text = c.inv("sides.count.same", "slot", name, "n", cmp.BMC, "since", stamp(cmp.Since))
			} else {
				cmp.Text = c.inv("sides.count.differ", "slot", name, "bmc", cmp.BMC, "os", cmp.OS, "since", stamp(cmp.Since))
				kinds["count"] = true
			}
			r.Count = cmp
			if r.Outcome == SidesCounted {
				// The comparison of the counts says all there is to say.
				r.Text = cmp.Text
			}
		}
		s.Rows = append(s.Rows, r)
	}
	if len(s.Rows) == 0 {
		s.Lead = append(s.Lead, c.inv("sides.none"))
		return s, counts
	}

	// What can explain the differences. Every line rests on something the
	// ledger holds; none of them is established as the cause.
	setting := func(name, state string) *memras.Item {
		if it := check.Item(name); it != nil && it.State == state && it.Attribute != "" {
			return it
		}
		return nil
	}
	if kinds[SidesOSOnly] {
		g := CandidateGroup{Kind: SidesOSOnly, Head: c.inv("sides.cand.os_only")}
		if it := setting(memras.CELogging, "off"); it != nil {
			g.Items = append(g.Items, c.inv("cand.ce_logging_off", "attribute", it.Attribute, "value", it.Value))
		}
		threshold := false
		if it := setting(memras.CEThreshold, "on"); it != nil {
			threshold = true
			g.Items = append(g.Items, c.inv("cand.threshold", "attribute", it.Attribute, "value", it.Value))
		}
		if it := setting(memras.LeakyBucket, "on"); it != nil {
			threshold = true
			g.Items = append(g.Items, c.inv("cand.leaky_bucket", "attribute", it.Attribute, "value", it.Value))
		}
		if !threshold {
			g.Items = append(g.Items, c.inv("cand.threshold_unknown"))
		}
		if !d.logLimit.IsZero() {
			g.Items = append(g.Items, c.inv("cand.log_limit", "when", stamp(d.logLimit)))
		}
		if bmc != nil {
			for _, l := range bmc.Logs {
				held := l.Total.Or(l.Entries)
				switch {
				case l.Truncated:
					g.Items = append(g.Items, c.inv("cand.log_cut", "log", l.ID, "read", l.Entries, "total", held))
				case strings.EqualFold(l.Policy, "WrapsWhenFull") && l.Max.Valid && held >= l.Max.V:
					g.Items = append(g.Items, c.inv("cand.log_full", "log", l.ID, "max", l.Max.V))
				}
			}
		}
		s.Candidates = append(s.Candidates, g)
	}
	if kinds[SidesBMCOnly] {
		g := CandidateGroup{Kind: SidesBMCOnly, Head: c.inv("sides.cand.bmc_only")}
		g.Items = append(g.Items, c.inv("cand.firmware_first"))
		for _, src := range d.latest {
			if !osKinds[src.Kind] {
				continue
			}
			switch src.Status {
			case model.SourceAbsent:
				if src.Kind == "rasdaemon" {
					g.Items = append(g.Items, c.inv("cand.no_rasdaemon"))
				}
			case model.SourceOK, model.SourcePartial, model.SourceEmpty:
			default:
				g.Items = append(g.Items, c.inv("cand.os_unread", "source", src.Kind, "status", engine.Messages.Text(c.Lang, "status."+string(src.Status), nil)))
			}
		}
		s.Candidates = append(s.Candidates, g)
	}
	if kinds["count"] {
		g := CandidateGroup{Kind: "count", Head: c.inv("sides.cand.count")}
		g.Items = append(g.Items, c.inv("cand.count_firmware"), c.inv("cand.count_cleared"))
		s.Candidates = append(s.Candidates, g)
	}
	return s, counts
}
