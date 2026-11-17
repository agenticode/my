package synth

import (
	"context"
	"database/sql"
	"fmt"
	"math/bits"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // the driver the rasdaemon database is written with

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/smbios"
	"github.com/agenticode/my/internal/model"
)

// Flavor selects the platform a host tree imitates. The same history looks
// different on each: the flavor decides how much of every error's location
// survives into the files, and therefore the observation grade.
type Flavor string

const (
	// FlavorIntel: i10nm_edac with ADXL address translation. Rank, bank,
	// row and column reach the logs; the device does not. Grade A.
	FlavorIntel Flavor = "intel"
	// FlavorAMD: amd64_edac. Channel, DIMM and rank through the chip-select
	// layers, and a physical address. Grade B.
	FlavorAMD Flavor = "amd"
	// FlavorGHES: firmware-first reporting through ghes_edac, with every
	// CPER field filled in, including device and bit position. Grade A.
	FlavorGHES Flavor = "ghes"
)

// Flavors lists the host tree flavors.
var Flavors = []Flavor{FlavorIntel, FlavorAMD, FlavorGHES}

// Adapter returns an adapter that hands the data set to a ledger directly,
// every event with its full location. The inventory is reported once per
// boot, so DIMM moves show up as placements.
func (ds *Dataset) Adapter() adapter.Adapter { return dsAdapter{ds} }

type dsAdapter struct{ ds *Dataset }

func (a dsAdapter) Kind() string     { return "synth" }
func (a dsAdapter) Origin() string   { return "synth:" + a.ds.Scenario.Name }
func (a dsAdapter) HostHint() string { return a.ds.Scenario.Host.Name }

func (a dsAdapter) Collect(ctx context.Context, sink adapter.Sink) adapter.Report {
	ds, sc := a.ds, a.ds.Scenario
	rep := adapter.Report{Kind: a.Kind(), Origin: a.Origin(), HostHint: sc.Host.Name}
	fail := func(err error) adapter.Report {
		rep.Status, rep.Detail, rep.SinkErr = model.SourceError, "ledger rejected a record: "+err.Error(), err
		return rep
	}
	if err := ctx.Err(); err != nil {
		rep.Status, rep.Detail = model.SourceError, err.Error()
		return rep
	}
	if err := sink.Host(&model.Host{Arch: "x86_64", SystemVendor: sc.Host.SystemVendor, SystemProduct: sc.Host.SystemProduct,
		FirstSeen: sc.Start, LastSeen: sc.End}); err != nil {
		return fail(err)
	}
	for i := range ds.Boots {
		b := ds.Boots[i]
		b.Host = ""
		if err := sink.Boot(&b); err != nil {
			return fail(err)
		}
		// The inventory as it was shortly after the boot, and for the last
		// boot again at the end of the history.
		times := []time.Time{b.StartedAt.Add(10 * time.Minute)}
		if i == len(ds.Boots)-1 {
			times = append(times, sc.End)
		}
		for _, at := range times {
			for _, m := range ds.modulesAt(i, at) {
				if err := sink.Module(m); err != nil {
					return fail(err)
				}
			}
		}
		rep.Records++
	}
	for i := range ds.Events {
		if i%1024 == 0 && ctx.Err() != nil {
			rep.Status, rep.Detail = model.SourceError, ctx.Err().Error()
			return rep
		}
		e := ds.Events[i]
		// Slot and DIMM are for the ledger to work out from the label.
		e.Host, e.Slot, e.DIMMUID = "", "", ""
		if err := sink.Event(&e); err != nil {
			return fail(err)
		}
		rep.Records++
	}
	for i := range ds.Actions {
		act := ds.Actions[i]
		act.Host, act.Slot, act.DIMMUID = "", "", ""
		if err := sink.Action(&act); err != nil {
			return fail(err)
		}
		rep.Records++
	}
	rep.Status = model.SourceOK
	return rep
}

// modulesAt returns the SMBIOS view of the board during a boot.
func (ds *Dataset) modulesAt(boot int, at time.Time) []*model.Module {
	h := ds.Scenario.Host
	out := make([]*model.Module, 0, len(ds.Slots))
	for _, s := range ds.Slots {
		m := &model.Module{ObservedAt: at, Locator: s.Name, BankLocator: s.BankLocator(h), Handle: model.Some(0x1100 + s.Index),
			Source: "synth", RawRef: fmt.Sprintf("synth:%s#%s", ds.Scenario.Name, s.Name)}
		if idx := ds.Occupancy[boot][s.Index]; idx >= 0 {
			m.Populated = true
			m.DIMM = ds.Modules[idx]
			m.DIMM.UID, m.DIMM.Identity = "", ""
		}
		out = append(out, m)
	}
	return out
}

// slotOf returns the board slot an event happened in.
func (ds *Dataset) slotOf(e *model.Event) BoardSlot {
	for _, s := range ds.Slots {
		if s.Name == e.Label {
			return s
		}
	}
	return BoardSlot{Name: e.Label}
}

// edacRecord is one error as an EDAC driver of a flavor reports it.
type edacRecord struct {
	mc       int
	label    string
	top, mid int
	location string // layer text of the kernel log line
	msg      string
	detail   string
	errType  string // rasdaemon's err_type
	kind     string // CE or UE
	syndrome uint64
	grain    int
}

func (ds *Dataset) edacRecord(fl Flavor, e *model.Event) edacRecord {
	h := ds.Scenario.Host
	s := ds.slotOf(e)
	ue := e.ErrorSeverity.Uncorrected()
	r := edacRecord{kind: "CE", errType: "Corrected", grain: 32}
	if ue {
		r.kind, r.errType = "UE", "Uncorrected"
	}
	scrub := e.ErrorScrubType == model.ScrubPatrol
	switch fl {
	case FlavorAMD:
		// One controller per socket; chip select = DIMM position * 2 + rank.
		channel := s.IMC*h.ChannelsPerIMC + s.Channel
		csrow := s.Position*2 + e.Rank.V%2
		r.mc, r.top, r.mid = s.Socket, csrow, channel
		r.label = fmt.Sprintf("mc#%dcsrow#%dchannel#%d", s.Socket, csrow, channel)
		r.location = fmt.Sprintf("csrow:%d channel:%d", csrow, channel)
		r.grain = 64
		r.syndrome = uint64(0x100 | e.DeviceId.V)
	case FlavorGHES:
		r.mc, r.top, r.mid = 0, s.Index, -1
		r.label = s.BankLocator(h) + " " + s.Name
		bit := 0
		fmt.Sscanf(e.ErrorVector, "dq:0x%x", &bit)
		pos := 0
		for bit > 1 {
			bit >>= 1
			pos++
		}
		r.location = fmt.Sprintf("node:%d card:%d module:%d rank:%d bank_group:%d bank_address:%d device:%d row:%d column:%d bit_position:%d DIMM location: %s",
			s.Socket, s.IMC*h.ChannelsPerIMC+s.Channel, s.Position, e.Rank.V, e.BankGroup.V, e.Bank.V, e.DeviceId.V, e.Row.V, e.Column.V,
			e.DeviceId.V*h.Geometry.DeviceWidth+pos, r.label)
		r.detail = "APEI location: " + r.location + " status(0x0000000000000400): Storage error in DRAM memory"
		r.grain = 64
		switch {
		case ue:
			r.msg = "multi-bit ECC"
		case scrub:
			r.msg = "scrub corrected error"
		default:
			r.msg = "single-bit ECC"
		}
	default: // Intel
		r.mc, r.top, r.mid = s.EdacMC(h), s.Channel, s.Position
		r.label = s.EdacLabel()
		r.location = fmt.Sprintf("channel:%d slot:%d", s.Channel, s.Position)
		code := "0x0101:0x009"
		r.msg = "memory read error"
		if scrub {
			code, r.msg = "0x0008:0x00c", "memory scrubbing error"
		}
		flag := ""
		if ue {
			flag = " recoverable"
		}
		r.detail = fmt.Sprintf("%s err_code:%s%x  SystemAddress:0x%x ProcessorSocketId:0x%x MemoryControllerId:0x%x ChannelId:0x%x PhysicalRankId:0x%x DimmSlotId:0x%x DimmRankId:0x%x Row:0x%x Column:0x%x Bank:0x%x BankGroup:0x%x ChipSelect:0x%x ChipId:0x0",
			flag, code, s.Channel, e.OsPhysicalAddress.V, s.Socket, s.IMC, s.Channel, s.Position*4+e.Rank.V, s.Position, e.Rank.V,
			e.Row.V, e.Column.V, e.Bank.V, e.BankGroup.V, s.Position*2+e.Rank.V)
	}
	return r
}

// line formats the kernel log message of the record.
func (r edacRecord) line(e *model.Event) string {
	msg := r.msg
	if msg != "" {
		msg += " "
	}
	addr := e.OsPhysicalAddress.V
	tail := fmt.Sprintf("page:0x%x offset:0x%x grain:%d", addr>>12, addr&0xfff, r.grain)
	if r.kind == "CE" {
		tail += fmt.Sprintf(" syndrome:0x%x", r.syndrome)
	}
	if r.detail != "" {
		tail += " - " + r.detail
	}
	return fmt.Sprintf("EDAC MC%d: %d %s %son %s (%s %s)", r.mc, e.Count, r.kind, msg, r.label, r.location, tail)
}

// intelLine is the kernel log line of an event on an Intel host. It is the
// raw record of events handed to a ledger directly.
func intelLine(_ Host, ds *Dataset, e *model.Event) string {
	return ds.edacRecord(FlavorIntel, e).line(e)
}

func writeFile(root, name, content string) error {
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

// WriteHostTree writes the files a host with this history would have at the
// end of the scenario, below root: the rasdaemon database, the kernel log
// of the running boot, the EDAC counters, the SMBIOS tables and the /proc
// and /sys files that identify host and boot. The input adapters read the
// tree like a real file system root.
//
// A tree shows what one machine knows at one moment. Slot moves before the
// last boot are not in it: SMBIOS only says where the DIMMs are now.
func (ds *Dataset) WriteHostTree(root string, fl Flavor) error {
	switch fl {
	case FlavorIntel, FlavorAMD, FlavorGHES:
	default:
		return fmt.Errorf("unknown flavor %q (known: %v)", fl, Flavors)
	}
	sc := ds.Scenario
	h := sc.Host
	last := len(ds.Boots) - 1
	boot := ds.Boots[last]

	// Host and boot identification. The BIOS is the one the running boot
	// came up with: a scenario can change it at a reboot.
	id := boot.BootID
	biosText := h.BIOS
	if boot.BIOS != "" {
		biosText = boot.BIOS
	}
	bios := strings.Fields(biosText)
	biosVendor, biosVersion, biosDate := biosText, "", ""
	if len(bios) >= 3 {
		biosVendor, biosVersion, biosDate = strings.Join(bios[:len(bios)-2], " "), bios[len(bios)-2], bios[len(bios)-1]
	}
	files := map[string]string{
		"proc/sys/kernel/hostname":       h.Name + "\n",
		"proc/sys/kernel/osrelease":      boot.Kernel + "\n",
		"proc/sys/kernel/random/boot_id": fmt.Sprintf("%s-%s-%s-%s-%s\n", id[0:8], id[8:12], id[12:16], id[16:20], id[20:32]),
		"proc/cmdline":                   boot.Cmdline + "\n",
		"proc/stat":                      fmt.Sprintf("cpu  4705 150 1120 16250 520 0 30 0 0 0\nintr 114930548\nctxt 1990473\nbtime %d\nprocesses 2915\n", boot.StartedAt.Unix()),
		"sys/class/dmi/id/sys_vendor":    h.SystemVendor + "\n",
		"sys/class/dmi/id/product_name":  h.SystemProduct + "\n",
		"sys/class/dmi/id/bios_vendor":   biosVendor + "\n",
		"sys/class/dmi/id/bios_version":  biosVersion + "\n",
		"sys/class/dmi/id/bios_date":     biosDate + "\n",
	}

	// SMBIOS tables.
	var b smbios.Builder
	b.AddSystem(0x0100, h.SystemVendor, h.SystemProduct)
	for _, s := range ds.Slots {
		md := smbios.MemoryDevice{Handle: uint16(0x1100 + s.Index), Locator: s.Name, BankLocator: s.BankLocator(h)}
		if idx := ds.Occupancy[last][s.Index]; idx >= 0 {
			d := ds.Modules[idx]
			md.SizeMB = int(d.SizeBytes >> 20)
			md.MemoryType, md.FormFactor, md.TypeDetail, md.Technology = smbios.TypeDDR5, smbios.FormDIMM, smbios.DetailRDIMM, smbios.TechnologyDRAM
			md.SpeedMTs, md.ConfSpeedMTs = d.SpeedMTs.Or(0), d.ConfiguredSpeedMTs.Or(0)
			md.Manufacturer, md.Serial, md.PartNumber = d.Manufacturer, d.Serial, d.PartNumber
			md.Ranks, md.TotalWidth, md.DataWidth = d.Ranks.Or(0), d.TotalWidth.Or(0), d.DataWidth.Or(0)
			md.ManufacturerID = jedecWord(d.ManufacturerID)
		} else {
			md.Manufacturer, md.Serial, md.PartNumber = "NO DIMM", "NO DIMM", "NO DIMM"
		}
		b.AddMemoryDevice(md)
	}
	table := b.Bytes()
	files["sys/firmware/dmi/tables/DMI"] = string(table)
	files["sys/firmware/dmi/tables/smbios_entry_point"] = string(smbios.EntryPoint64(3, 5, len(table)))

	// EDAC counters of the running boot.
	ds.edacSysfs(fl, files, last)

	// Kernel log of the running boot.
	var log strings.Builder
	stamp := func(t time.Time) string {
		d := t.Sub(boot.StartedAt)
		return fmt.Sprintf("[%5d.%06d] ", int64(d/time.Second), int64(d%time.Second/time.Microsecond))
	}
	fmt.Fprintf(&log, "[    0.000000] Linux version %s (mockbuild@build.example.org) (gcc (GCC) 11.5.0) #1 SMP PREEMPT_DYNAMIC\n", boot.Kernel)
	fmt.Fprintf(&log, "[    0.000000] Command line: %s\n", boot.Cmdline)
	fmt.Fprintf(&log, "[    0.000000] DMI: %s %s, BIOS %s %s\n", h.SystemVendor, h.SystemProduct, biosVersion, biosDate)
	type entry struct {
		at   time.Time
		text string
	}
	var entries []entry
	for i := range ds.Events {
		e := &ds.Events[i]
		if e.BootID == boot.BootID {
			entries = append(entries, entry{e.ErrorDT, ds.edacRecord(fl, e).line(e)})
		}
	}
	for i := range ds.Actions {
		a := &ds.Actions[i]
		if a.BootID != boot.BootID || a.Kind != model.ActionPageOffline {
			continue
		}
		pfn := a.OsPhysicalAddress.V >> 12
		if a.Method == "hard" {
			entries = append(entries, entry{a.At, fmt.Sprintf("Memory failure: %#x: recovery action for %s", pfn, a.Detail)})
		} else {
			entries = append(entries, entry{a.At, fmt.Sprintf("RAS: Soft-offlining pfn: %#x", pfn)})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].at.Before(entries[j].at) })
	for _, en := range entries {
		log.WriteString(stamp(en.at) + en.text + "\n")
	}
	files["var/log/dmesg"] = log.String()

	for name, content := range files {
		if err := writeFile(root, name, content); err != nil {
			return err
		}
	}
	db := filepath.Join(root, "var", "lib", "rasdaemon", "ras-mc_event.db")
	if err := ds.rasdaemonDB(db, fl); err != nil {
		return err
	}
	// The files are what the host held at the end of the scenario, so they
	// carry that time: a reader of the tree takes it as the time of the
	// copy, and the counters count back from it.
	for name := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.Chtimes(p, sc.End, sc.End); err != nil {
			return err
		}
	}
	return os.Chtimes(db, sc.End, sc.End)
}

// edacSysfs adds the EDAC counter files of the running boot.
func (ds *Dataset) edacSysfs(fl Flavor, files map[string]string, boot int) {
	h := ds.Scenario.Host
	b := ds.Boots[boot]
	since := int64(ds.Scenario.End.Sub(b.StartedAt)/time.Second) - 18
	type key struct{ mc, idx int }
	type obj struct {
		label, location string
		ce, ue          int
		sizeMB          int64
	}
	objs := map[key]*obj{}
	names := map[int]string{}
	// The DIMM objects the driver creates for the populated slots.
	for _, s := range ds.Slots {
		m := ds.Occupancy[boot][s.Index]
		if m < 0 {
			continue
		}
		size := ds.Modules[m].SizeBytes >> 20
		switch fl {
		case FlavorAMD:
			channels := h.IMCs * h.ChannelsPerIMC
			channel := s.IMC*h.ChannelsPerIMC + s.Channel
			names[s.Socket] = "F19h_M10h"
			for rank := range h.Geometry.Ranks {
				csrow := s.Position*2 + rank
				objs[key{s.Socket, csrow*channels + channel}] = &obj{
					label:    fmt.Sprintf("mc#%dcsrow#%dchannel#%d", s.Socket, csrow, channel),
					location: fmt.Sprintf("csrow %d channel %d ", csrow, channel), sizeMB: size / int64(h.Geometry.Ranks)}
			}
		case FlavorGHES:
			names[0] = "ghes_edac"
			objs[key{0, s.Index}] = &obj{label: s.BankLocator(h) + " " + s.Name, location: fmt.Sprintf("memory %d ", s.Index), sizeMB: size}
		default:
			names[s.EdacMC(h)] = fmt.Sprintf("Intel_10nm Socket#%d IMC#%d", s.Socket, s.IMC)
			objs[key{s.EdacMC(h), s.Channel*h.SlotsPerChannel + s.Position}] = &obj{
				label: s.EdacLabel(), location: fmt.Sprintf("channel %d slot %d ", s.Channel, s.Position), sizeMB: size}
		}
	}
	// Their counts: the errors of this boot.
	byLabel := map[string]*obj{}
	for _, o := range objs {
		byLabel[o.label] = o
	}
	for i := range ds.Events {
		e := &ds.Events[i]
		if e.BootID != b.BootID {
			continue
		}
		if o := byLabel[ds.edacRecord(fl, e).label]; o != nil {
			if e.ErrorSeverity.Uncorrected() {
				o.ue += e.Count
			} else {
				o.ce += e.Count
			}
		}
	}
	totals := map[int][2]int{}
	const dir = "sys/devices/system/edac/mc"
	for k, o := range objs {
		kind := "dimm"
		if fl == FlavorAMD {
			kind = "rank"
		}
		d := fmt.Sprintf("%s/mc%d/%s%d/", dir, k.mc, kind, k.idx)
		files[d+"dimm_label"] = o.label + "\n"
		files[d+"dimm_location"] = o.location + "\n"
		files[d+"dimm_ce_count"] = fmt.Sprintf("%d\n", o.ce)
		files[d+"dimm_ue_count"] = fmt.Sprintf("%d\n", o.ue)
		files[d+"size"] = fmt.Sprintf("%d\n", o.sizeMB)
		files[d+"dimm_mem_type"] = "Registered-DDR5\n"
		files[d+"dimm_dev_type"] = fmt.Sprintf("x%d\n", h.Geometry.DeviceWidth)
		files[d+"dimm_edac_mode"] = "S4ECD4ED\n"
		t := totals[k.mc]
		totals[k.mc] = [2]int{t[0] + o.ce, t[1] + o.ue}
	}
	for mc, name := range names {
		d := fmt.Sprintf("%s/mc%d/", dir, mc)
		files[d+"mc_name"] = name + "\n"
		files[d+"seconds_since_reset"] = fmt.Sprintf("%d\n", since)
		files[d+"ce_count"] = fmt.Sprintf("%d\n", totals[mc][0])
		files[d+"ue_count"] = fmt.Sprintf("%d\n", totals[mc][1])
		files[d+"ce_noinfo_count"] = "0\n"
		files[d+"ue_noinfo_count"] = "0\n"
	}
}

// rasdaemonDB writes the database rasdaemon would hold: every error of the
// history as an mc_event row, and the pages the kernel took offline after
// uncorrected errors as memory_failure_event rows.
func (ds *Dataset) rasdaemonDB(path string, fl Flavor) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS mc_event (id INTEGER PRIMARY KEY, timestamp TEXT, err_count INTEGER, err_type TEXT, err_msg TEXT, label TEXT, mc INTEGER, top_layer INTEGER, middle_layer INTEGER, lower_layer INTEGER, address INTEGER, grain INTEGER, syndrome INTEGER, driver_detail TEXT);
CREATE TABLE IF NOT EXISTS memory_failure_event (id INTEGER PRIMARY KEY, timestamp TEXT, pfn TEXT, page_type TEXT, action_result TEXT);`); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ins, err := tx.Prepare(`INSERT INTO mc_event (timestamp, err_count, err_type, err_msg, label, mc, top_layer, middle_layer, lower_layer, address, grain, syndrome, driver_detail)
 VALUES (?, ?, ?, ?, ?, ?, ?, ?, -1, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer ins.Close()
	const layout = "2006-01-02 15:04:05 -0700"
	for i := range ds.Events {
		e := &ds.Events[i]
		r := ds.edacRecord(fl, e)
		grainBits := 5
		if r.grain == 64 {
			grainBits = 6
		}
		if _, err := ins.Exec(e.ErrorDT.UTC().Format(layout), e.Count, r.errType, r.msg, r.label, r.mc, r.top, r.mid,
			int64(e.OsPhysicalAddress.V), grainBits, int64(r.syndrome), r.detail); err != nil {
			return err
		}
	}
	for i := range ds.Actions {
		a := &ds.Actions[i]
		if a.Kind != model.ActionPageOffline || a.Method != "hard" {
			continue
		}
		pageType, result, _ := strings.Cut(a.Detail, ": ")
		if _, err := tx.Exec(`INSERT INTO memory_failure_event (timestamp, pfn, page_type, action_result) VALUES (?, ?, ?, ?)`,
			a.At.UTC().Format(layout), fmt.Sprintf("%#x", a.OsPhysicalAddress.V>>12), pageType, result); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// jedecWord returns the SMBIOS word of a JEDEC maker code written as
// "bank 1, 0x2C": the ID code in the high byte, the continuation count with
// its odd parity bit in the low byte. It is 0 for no code.
func jedecWord(code string) uint16 {
	var bank, id int
	if _, err := fmt.Sscanf(code, "bank %d, 0x%X", &bank, &id); err != nil || bank < 1 || bank > 128 || id < 0 || id > 0xFF {
		return 0
	}
	count := byte(bank - 1)
	if bits.OnesCount8(count)%2 == 0 {
		count |= 0x80
	}
	return uint16(id)<<8 | uint16(count)
}
