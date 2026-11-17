package view

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/dimmspd"
	"github.com/agenticode/my/internal/decode/spd"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
)

// SPD is the view spd: what each memory module of a host says about itself
// in its SPD EEPROM, tied to its slot by serial number, with the
// differences to SMBIOS and the table of date codes. When no SPD was
// read, it holds the reason.
type SPD struct {
	Host   string `json:"host"`
	System string `json:"system,omitzero"`
	// Looked says that the latest scan of the host looked for the SPD.
	Looked bool `json:"looked"`
	// Read counts the readings in the states ok, crc and short; Devices
	// the SPD devices the scan looked at; DIMMs the DIMMs SMBIOS places on
	// the host; Tied the readings tied to one of them.
	Read    int      `json:"read"`
	Devices int      `json:"devices"`
	DIMMs   int      `json:"dimms"`
	Tied    int      `json:"tied"`
	Drivers []string `json:"drivers,omitzero"`
	// Head is the line under the host: how many were read and tied.
	Head    Text        `json:"head"`
	Reason  *SPDReason  `json:"reason,omitzero"`
	Modules []SPDModule `json:"modules"`
	Shared  SPDShared   `json:"shared"`
	Lots    *Lots       `json:"lots,omitzero"`
	// Notes are the differences of design that concern all modules: serial
	// numbers tied in reverse order, readings tied to no slot, DIMMs
	// without a reading, no SMBIOS serial numbers.
	Notes []Text `json:"notes,omitzero"`
}

// SPDReason says why no SPD was read: the code, the short text of the
// source, the long text, what was read beside it, and what the reader can
// do.
type SPDReason struct {
	Code  string `json:"code"`
	Short Text   `json:"short"`
	Text  Text   `json:"text"`
	Facts []Text `json:"facts,omitzero"`
	Step  *Text  `json:"step,omitzero"`
}

// SPDModule is one SPD reading.
type SPDModule struct {
	Slot    string `json:"slot,omitzero"`
	DIMMUID string `json:"dimm_uid,omitzero"`
	Tie     string `json:"tie,omitzero"`
	Origin  string `json:"origin"`
	Bus     string `json:"bus,omitzero"`
	Address int    `json:"address,omitzero"`
	Driver  string `json:"driver,omitzero"`
	Form    string `json:"form"`
	State   string `json:"state"`
	Detail  string `json:"detail,omitzero"`
	// Bytes is the number of bytes read.
	Bytes int         `json:"bytes"`
	SPD   spd.Decoded `json:"spd"`
	// MakerName is the name of the module maker and MakerSource where it
	// comes from: the table of maker codes, SMBIOS (the string of the
	// firmware of a tied DIMM), or the text of decode-dimms.
	MakerName   string `json:"maker_name,omitzero"`
	MakerSource string `json:"maker_source,omitzero"`
	// StateText says what the state means for a reading that is not ok.
	StateText   *Text  `json:"state_text,omitzero"`
	Differences []Text `json:"differences,omitzero"`
	// Grade is the finding of the tied DIMM, UE whether it has an
	// uncorrected error on record.
	Grade     model.VerdictGrade `json:"grade,omitzero"`
	GradeText string             `json:"grade_text,omitzero"`
	UE        bool               `json:"ue,omitzero"`
}

// Place returns the slot of a tied reading, else its bus and address, else
// its file.
func (m *SPDModule) Place() string {
	switch {
	case m.Slot != "":
		return m.Slot
	case m.Bus != "":
		return fmt.Sprintf("%s 0x%02x", m.Bus, m.Address)
	}
	return m.Origin
}

// SPDShared holds what every module of the view has in common, printed
// once in the head; a field is empty when the modules differ in it.
type SPDShared struct {
	Count      int    `json:"count"`
	Maker      string `json:"maker,omitzero"`
	MakerID    string `json:"maker_id,omitzero"`
	PartNumber string `json:"part_number,omitzero"`
	MemoryType string `json:"memory_type,omitzero"`
	ModuleType string `json:"module_type,omitzero"`
	SizeBytes  int64  `json:"size_bytes,omitzero"`
	Org        string `json:"organisation,omitzero"`
	DieMbit    int    `json:"die_mbit,omitzero"`
}

// spdText renders a message of the SPD views.
func (c *Case) spdText(code string, kv ...any) Text {
	t := Text{Code: code}
	if len(kv) > 0 {
		t.Args = make(map[string]any, len(kv)/2)
		for i := 0; i+1 < len(kv); i += 2 {
			t.Args[kv[i].(string)] = kv[i+1]
		}
	}
	t.Text = SPDMessages.Text(c.Lang, code, t.Args)
	return t
}

// spdData is what the ledger holds of the SPD of a host.
type spdData struct {
	looked   bool
	probe    *dimmspd.Probe
	readings []model.SPDReading
	decoded  []spd.Decoded
}

// spdOf reads the SPD of a host: whether its latest scan looked, the probe
// of the latest scan that looked, and the current reading of every
// origin. A reading of an eeprom file belongs to the scan that read it:
// one that a later scan of the host no longer made is left out.
func (c *Case) spdOf(ctx context.Context, host string) (*spdData, error) {
	out := &spdData{}
	_, srcs, err := c.latestScan(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, s := range srcs {
		if s.Kind == dimmspd.Kind {
			out.looked = true
		}
	}
	scans, err := c.store.Scans(ctx)
	if err != nil {
		return nil, err
	}
	var spdScan *model.Scan
	for i := range scans {
		sc := &scans[i]
		if sc.Host != host {
			continue
		}
		ss, err := c.store.Sources(ctx, sc.ID)
		if err != nil {
			return nil, err
		}
		for _, s := range ss {
			if s.Kind == dimmspd.Kind && (spdScan == nil || sc.StartedAt.After(spdScan.StartedAt) || sc.StartedAt.Equal(spdScan.StartedAt) && sc.ID > spdScan.ID) {
				spdScan = sc
			}
		}
	}
	rs, err := c.store.Readings(ctx, host)
	if err != nil {
		return nil, err
	}
	for i := range rs {
		if rs[i].Kind == model.ReadingSPDProbe {
			out.probe = adapter.Guard(func() *dimmspd.Probe { return dimmspd.ParseProbe(rs[i].Text) })
		}
	}
	all, err := c.store.SPDReadings(ctx, host)
	if err != nil {
		return nil, err
	}
	latest := map[string]int{}
	for i, r := range all {
		if j, ok := latest[r.Origin]; !ok || r.LastSeen.After(all[j].LastSeen) || r.LastSeen.Equal(all[j].LastSeen) && r.ID > all[j].ID {
			latest[r.Origin] = i
		}
	}
	for _, i := range latest {
		r := all[i]
		if r.Form == model.SPDFormEEPROM && spdScan != nil && r.ScanID != 0 && r.ScanID != spdScan.ID {
			continue
		}
		out.readings = append(out.readings, r)
	}
	sort.Slice(out.readings, func(i, j int) bool { return out.readings[i].Origin < out.readings[j].Origin })
	for _, r := range out.readings {
		d := adapter.Guard(func() spd.Decoded { return ingest.SPDDecode(r) })
		if d.State == "" {
			d.State = r.State
		}
		out.decoded = append(out.decoded, d)
	}
	return out, nil
}

// memoryTypeOf returns the memory type of the populated slots of a host
// when they all have the same.
func memoryTypeOf(h *engine.HostResult) string {
	t := ""
	for _, d := range h.DIMMs {
		if !d.Present || d.DIMM == nil || d.DIMM.MemoryType == "" {
			continue
		}
		if t != "" && t != d.DIMM.MemoryType {
			return ""
		}
		t = d.DIMM.MemoryType
	}
	return t
}

// SPD builds the view spd of a host.
func (c *Case) SPD(ctx context.Context, h *engine.HostResult) (*SPD, error) {
	data, err := c.spdOf(ctx, h.Host)
	if err != nil {
		return nil, err
	}
	v := &SPD{Host: h.Host, Looked: data.looked, Modules: []SPDModule{}}
	hosts, err := c.store.Hosts(ctx)
	if err != nil {
		return nil, err
	}
	for _, x := range hosts {
		if x.Name == h.Host {
			v.System = strings.TrimSpace(x.SystemVendor + " " + x.SystemProduct)
		}
	}
	byUID := map[string]*engine.DIMMResult{}
	serials := 0
	for _, d := range h.DIMMs {
		if !d.Present || d.DIMM == nil {
			continue
		}
		byUID[d.UID] = d
		v.DIMMs++
		if d.DIMM.Identity == model.IdentitySerial && model.UsableSerial(d.DIMM.Serial) {
			serials++
		}
	}
	widths, err := c.otherWidths(ctx, h.Host)
	if err != nil {
		return nil, err
	}
	drivers := map[string]bool{}
	reversed, untied := 0, 0
	for i, r := range data.readings {
		d := data.decoded[i]
		m := SPDModule{Origin: r.Origin, Bus: r.Bus, Address: r.Address, Driver: r.Driver, Form: r.Form, State: r.State, Detail: r.Detail,
			Bytes: len(r.Raw), SPD: d, Tie: r.Tie, DIMMUID: r.DIMMUID}
		if spd.Read(r.State) {
			v.Read++
		}
		if r.Driver != "" {
			drivers[r.Driver] = true
		}
		m.MakerName, m.MakerSource = d.ModuleMakerName, "table"
		if r.Form == model.SPDFormDecodeDimms {
			m.MakerSource = "decode-dimms"
		}
		if dr, ok := byUID[r.DIMMUID]; ok {
			m.Slot = dr.Slot
			v.Tied++
			if r.Tie == model.SPDTieSerialReversed {
				reversed++
			}
			if m.MakerName == "" && dr.DIMM.Manufacturer != "" && d.ModuleMaker != "" {
				m.MakerName, m.MakerSource = dr.DIMM.Manufacturer, "smbios"
			}
			if dr.Verdict != nil {
				m.Grade, m.GradeText = dr.Verdict.Grade, engine.GradeText(c.Lang, dr.Verdict.Grade)
			}
			m.UE = dr.Features.UE > 0
			if spd.Read(r.State) {
				m.Differences = c.spdDifferences(dr, d, widths[dr.Slot])
			}
		} else if spd.Read(r.State) {
			untied++
		}
		if m.MakerName == "" {
			m.MakerSource = ""
		}
		m.StateText = c.stateText(m, data.probe)
		v.Modules = append(v.Modules, m)
	}
	for d := range drivers {
		v.Drivers = append(v.Drivers, d)
	}
	sort.Strings(v.Drivers)
	v.Devices = len(data.readings)
	if data.probe != nil && len(data.probe.Clients) > v.Devices {
		v.Devices = len(data.probe.Clients)
	}
	sort.SliceStable(v.Modules, func(i, j int) bool {
		a, b := v.Modules[i], v.Modules[j]
		if (a.Slot == "") != (b.Slot == "") {
			return a.Slot != ""
		}
		if a.Slot != b.Slot {
			return naturalLess(a.Slot, b.Slot)
		}
		return a.Origin < b.Origin
	})
	v.Shared = sharedOf(v.Modules)
	switch {
	case v.Read == 0:
		if data.probe != nil {
			code := adapter.Guard(func() string { return dimmspd.Reason(data.probe, memoryTypeOf(h)) })
			if code != "" {
				slots, err := c.store.Slots(ctx, h.Host)
				if err != nil {
					return nil, err
				}
				n := 0
				for _, sl := range slots {
					if sl.SMBIOSHandle.Valid {
						n++
					}
				}
				v.Reason = c.spdReason(data.probe, code, n, typeOf(data.decoded))
				v.Head = c.spdText("spd.head.none", "short", v.Reason.Short.Text)
			}
		}
		if v.Reason == nil {
			v.Head = c.spdText("spd.head.nothing")
		}
	case serials == 0:
		v.Head = c.spdText("spd.head.untied", "read", v.Read, "drivers", strings.Join(v.Drivers, ", "))
		if len(v.Drivers) == 0 {
			v.Head = c.spdText("spd.head.untied_files", "read", v.Read)
		}
		v.Notes = append(v.Notes, c.spdText("spd.diff.no-smbios"))
	default:
		v.Head = c.spdText("spd.head.tied", "read", v.Tied, "dimms", v.DIMMs, "drivers", strings.Join(v.Drivers, ", "))
		if len(v.Drivers) == 0 {
			v.Head = c.spdText("spd.head.tied_files", "read", v.Tied, "dimms", v.DIMMs)
		}
	}
	if reversed > 0 {
		v.Notes = append(v.Notes, c.spdText("spd.diff.reversed", "n", reversed))
	}
	if untied > 0 && serials > 0 {
		v.Notes = append(v.Notes, c.spdText("spd.diff.untied", "n", untied))
	}
	if serials > 0 && v.Read > 0 && v.Tied < v.DIMMs {
		v.Notes = append(v.Notes, c.spdText("spd.diff.uncovered", "n", v.DIMMs-v.Tied, "m", v.DIMMs))
	}
	if v.Read > 0 {
		var lots *Lots
		if serials == 0 {
			lots = c.lotsOfModules(v.Modules)
		} else {
			lots, err = c.Lots(ctx, []*engine.HostResult{h})
			if err != nil {
				return nil, err
			}
		}
		v.Lots = lots
	}
	return v, nil
}

// stateText says what the state of a reading that is not ok means.
func (c *Case) stateText(m SPDModule, p *dimmspd.Probe) *Text {
	d := m.SPD
	var t Text
	kv := detailArgs(m.Detail)
	switch m.State {
	case spd.StateOK:
		return nil
	case spd.StateCRC:
		b, _ := d.BaseCRC()
		t = c.spdText("spd.state.crc", "stored", fmt.Sprintf("0x%04X", b.Stored), "computed", fmt.Sprintf("0x%04X", b.Computed))
	case spd.StateShort:
		t = c.spdText("spd.state.short", "n", m.Bytes)
		if d.MemoryType == "DDR5" && m.Bytes > 0 && m.Bytes <= dimmspd.Step && m.Form == model.SPDFormEEPROM {
			t = c.spdText("spd.state.first_page", "n", m.Bytes)
			if p != nil && p.WriteDisabled() {
				t = c.spdText("spd.state.first_page_wd", "n", m.Bytes)
			}
		}
	case spd.StateBlank:
		fill := 0
		if d.Fill.Valid {
			fill = d.Fill.V
		}
		t = c.spdText("spd.state.blank", "fill", fmt.Sprintf("0x%02X", fill))
	case spd.StateType:
		name := d.MemoryType
		if name == "" && d.KeyByte.Valid {
			name = fmt.Sprintf("0x%02X", d.KeyByte.V)
		}
		t = c.spdText("spd.state.type", "type", name)
	case spd.StateDenied, spd.StateIO:
		t = c.spdText("spd.state.failed", "errno", kv["errno"])
	case spd.StateTimeout:
		t = c.spdText("spd.state.timeout")
	default:
		t = c.spdText("spd.state.other", "state", m.State)
	}
	return &t
}

// detailArgs reads the key=value words of the detail of a reading.
func detailArgs(detail string) map[string]string {
	out := map[string]string{}
	for _, w := range strings.Fields(detail) {
		if k, v, ok := strings.Cut(w, "="); ok {
			out[k] = v
		}
	}
	return out
}

// sharedOf returns what every module has in common.
func sharedOf(mods []SPDModule) SPDShared {
	var s SPDShared
	first := true
	for _, m := range mods {
		if !spd.Read(m.State) {
			continue
		}
		d := m.SPD
		maker := m.MakerName
		if first {
			s = SPDShared{Maker: maker, MakerID: d.ModuleMaker, PartNumber: d.PartNumber, MemoryType: d.MemoryType, ModuleType: d.ModuleType,
				Org: d.Organisation, SizeBytes: d.SizeBytes.V, DieMbit: d.DieMbit.V}
			first = false
		} else {
			if s.MakerID != d.ModuleMaker || s.Maker != maker {
				s.MakerID, s.Maker = "", ""
			}
			if s.PartNumber != d.PartNumber {
				s.PartNumber = ""
			}
			if s.MemoryType != d.MemoryType || s.ModuleType != d.ModuleType || s.SizeBytes != d.SizeBytes.V || s.Org != d.Organisation {
				s.MemoryType, s.ModuleType, s.SizeBytes, s.Org = "", "", 0, ""
			}
			if s.DieMbit != d.DieMbit.V {
				s.DieMbit = 0
			}
		}
		s.Count++
	}
	return s
}

// otherWidths returns, per slot, the device widths EDAC and a BMC report.
func (c *Case) otherWidths(ctx context.Context, host string) (map[string]map[string]int, error) {
	out := map[string]map[string]int{}
	add := func(slot, src string, w int) {
		if slot == "" || w <= 0 {
			return
		}
		if out[slot] == nil {
			out[slot] = map[string]int{}
		}
		out[slot][src] = w
	}
	slots, err := c.store.Slots(ctx, host)
	if err != nil {
		return nil, err
	}
	samples, err := c.store.LatestCounterSamples(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, sl := range slots {
		for _, s := range samples {
			if sl.EdacMC.Valid && s.MC == sl.EdacMC.V && s.Location == sl.EdacLocation && strings.HasPrefix(s.DevType, "x") {
				var w int
				if _, err := fmt.Sscanf(s.DevType, "x%d", &w); err == nil {
					add(sl.Name, "EDAC", w)
				}
			}
		}
	}
	bm, err := c.store.BMCMemory(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, m := range bm {
		if m.DeviceWidth.Valid {
			add(m.Slot, "BMC", m.DeviceWidth.V)
		}
	}
	return out, nil
}

// spdDifferences lists where SMBIOS (or EDAC, a BMC) and the SPD of a tied
// DIMM say different things. The lines state the two values and draw no
// conclusion.
func (c *Case) spdDifferences(dr *engine.DIMMResult, d spd.Decoded, widths map[string]int) []Text {
	var out []Text
	x := dr.DIMM
	if x.PartNumber != "" && d.PartNumber != "" && !strings.EqualFold(strings.TrimSpace(x.PartNumber), d.PartNumber) {
		out = append(out, c.spdText("spd.diff.part", "slot", dr.Slot, "smbios", x.PartNumber, "spd", d.PartNumber))
	}
	if x.SizeBytes > 0 && d.SizeBytes.Valid && x.SizeBytes != d.SizeBytes.V {
		out = append(out, c.spdText("spd.diff.size", "slot", dr.Slot, "smbios", sizeText(x.SizeBytes), "spd", sizeText(d.SizeBytes.V)))
	}
	if x.Ranks.Valid && d.PackageRanks.Valid && x.Ranks.V != d.PackageRanks.V {
		out = append(out, c.spdText("spd.diff.ranks", "slot", dr.Slot, "smbios", x.Ranks.V, "spd", d.PackageRanks.V))
	}
	st, dt := strings.TrimSpace(x.MemoryType+" "+x.ModuleType), strings.TrimSpace(d.MemoryType+" "+d.ModuleType)
	if x.MemoryType != "" && d.MemoryType != "" && (x.MemoryType != d.MemoryType || x.ModuleType != "" && d.ModuleType != "" && x.ModuleType != d.ModuleType) {
		out = append(out, c.spdText("spd.diff.type", "slot", dr.Slot, "smbios", st, "spd", dt))
	}
	if d.DeviceWidth.Valid {
		var srcs []string
		for s := range widths {
			srcs = append(srcs, s)
		}
		sort.Strings(srcs)
		for _, s := range srcs {
			if widths[s] != d.DeviceWidth.V {
				out = append(out, c.spdText("spd.diff.width", "slot", dr.Slot, "source", s, "other", widths[s], "spd", d.DeviceWidth.V))
			}
		}
	}
	return out
}

// SizeText writes a module size as the SPD screens do: "64 GB".
func SizeText(b int64) string { return sizeText(b) }

// spdReason makes the reason of a host on which no SPD was read: the
// short and the long text, the facts the view has beside the probe, and
// the step.
func (c *Case) spdReason(p *dimmspd.Probe, code string, slots int, memType string) *SPDReason {
	args := reasonArgs(p, code)
	if code == dimmspd.ReasonNotDecoded {
		args = append(args, "type", dash(memType))
	}
	r := &SPDReason{Code: code, Short: Text{Code: "spd.short." + code, Text: engine.DetailText(c.Lang, dimmspd.Short(p, code))}}
	r.Text = c.spdText("spd.reason."+code, args...)
	if code == dimmspd.ReasonSPDDriverRefused && p.SPDDriver() == "spd5118" && !p.WriteDisabled() {
		extra := c.spdText("spd.reason.refused_ddr5")
		r.Text.Text += " " + extra.Text
	}
	if SPDMessages.Has("spd.step." + code) {
		t := c.spdText("spd.step."+code, args...)
		r.Step = &t
	}
	if code != dimmspd.ReasonVirtual {
		if slots > 0 {
			r.Facts = append(r.Facts, c.spdText("spd.fact.slots", "n", slots))
		}
		if p.BMC {
			r.Facts = append(r.Facts, c.spdText("spd.fact.bmc"))
		}
	}
	return r
}

// typeOf returns the memory type of the readings that are not decoded.
func typeOf(decoded []spd.Decoded) string {
	for _, d := range decoded {
		if d.State == spd.StateType {
			if d.MemoryType != "" {
				return d.MemoryType
			}
			if d.KeyByte.Valid {
				return fmt.Sprintf("0x%02X", d.KeyByte.V)
			}
		}
	}
	return ""
}

// reasonArgs are the values the texts of a reason name.
func reasonArgs(p *dimmspd.Probe, code string) []any {
	kv := []any{"what", p.Virtual}
	if c, ok := p.SMBusController(); ok {
		mod := dimmspd.SMBusDrivers[c.Vendor()]
		kv = append(kv, "pci", c.Address, "id", c.ID, "module", dash(mod[1]), "config", dash(dimmspd.Configs[mod[1]]))
	} else if len(p.PCI) > 0 {
		kv = append(kv, "pci", p.PCI[0].Address, "id", p.PCI[0].ID, "driver", dash(p.PCI[0].Driver))
	}
	switch code {
	case dimmspd.ReasonSMBusRefused:
		if c, ok := p.SMBusController(); ok {
			kv = append(kv, "driver", dash(dimmspd.SMBusDrivers[c.Vendor()][0]))
		}
	case dimmspd.ReasonSPDDriverNotLoaded, dimmspd.ReasonSPDDriverNotBuilt, dimmspd.ReasonSPDDriverUnknown, dimmspd.ReasonSPDDriverRefused,
		dimmspd.ReasonNoEEPROMFile, dimmspd.ReasonWriteDisabled, dimmspd.ReasonReadDenied, dimmspd.ReasonReadFailed:
		var ids []string
		path, errno := "", ""
		for _, cl := range p.Clients {
			ids = append(ids, cl.ID)
			if path == "" && cl.Errno != "" {
				path, errno = "/sys/bus/i2c/devices/"+cl.ID+"/eeprom", cl.Errno
			}
		}
		drv := p.SPDDriver()
		kv = append(kv, "n", len(p.Clients), "clients", strings.Join(ids, ", "), "driver", dash(drv), "config", dash(dimmspd.Configs[drv]),
			"path", dash(path), "errno", dash(errno))
	case dimmspd.ReasonNoSPDDevice:
		var ads []string
		for _, a := range p.Adapters {
			ads = append(ads, a.ID)
		}
		if len(ads) == 0 {
			for _, c := range p.PCI {
				ads = append(ads, c.Address)
			}
		}
		kv = append(kv, "adapters", dash(strings.Join(ads, ", ")))
	}
	return kv
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
