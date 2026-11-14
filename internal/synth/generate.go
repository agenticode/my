package synth

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"time"

	"github.com/agenticode/my/internal/model"
)

// BoardSlot is one memory slot of the synthetic host.
type BoardSlot struct {
	Name     string
	Index    int // position in board order
	Socket   int
	IMC      int
	Channel  int // within the controller
	Position int
}

// EdacMC returns the EDAC controller index of the slot.
func (b BoardSlot) EdacMC(h Host) int { return b.Socket*h.IMCs + b.IMC }

// EdacLabel returns the default EDAC label of the slot on an Intel host.
func (b BoardSlot) EdacLabel() string {
	return fmt.Sprintf("CPU_SrcID#%d_MC#%d_Chan#%d_DIMM#%d", b.Socket, b.IMC, b.Channel, b.Position)
}

// BankLocator returns the SMBIOS bank locator of the slot, which counts
// channels across the controllers of the socket.
func (b BoardSlot) BankLocator(h Host) string {
	return fmt.Sprintf("P%d_Node%d_Channel%d_Dimm%d", b.Socket, b.Socket, b.IMC*h.ChannelsPerIMC+b.Channel, b.Position)
}

// Truth is what a fault of the scenario was generated as. Tests compare a
// classifier's answer with it.
type Truth struct {
	Fault  int // index into Scenario.Faults
	Mode   model.FaultMode
	Slot   string // slot at the start of the fault
	Serial string // DIMM in that slot at the start of the fault
	// Location of the fault; fields the mode does not fix are unset.
	Rank, Device, BankGroup, Bank, Row, Column model.Opt[int]
	// CE and UE are the numbers of events generated.
	CE, UE int
	// Spreading is set for a row fault that leaks into the next row.
	Spreading bool
}

// Dataset is a generated history.
type Dataset struct {
	Scenario *Scenario
	Slots    []BoardSlot
	// Modules are the DIMMs of the host; Occupancy[boot][slot index] is
	// the index of the module in the slot during that boot, or -1.
	Modules   []model.DIMM
	Occupancy [][]int
	Boots     []model.Boot
	Events    []model.Event
	Actions   []model.Action
	Truth     []Truth
}

// located is an error location inside a DIMM.
type located struct {
	rank, device, bg, bank, row, col int
	dq                               int // mask of failing data lines of the device
}

// Generate runs a scenario.
func Generate(sc *Scenario) (*Dataset, error) {
	if err := sc.validate(); err != nil {
		return nil, err
	}
	g := &generator{sc: sc, ds: &Dataset{Scenario: sc}, rng: rand.New(rand.NewPCG(sc.Seed, sc.Seed^0x9e3779b97f4a7c15)), byFault: map[int][]int{}}
	g.board()
	g.boots()
	if err := g.occupancy(); err != nil {
		return nil, err
	}
	for i := range sc.Faults {
		if err := g.fault(i); err != nil {
			return nil, err
		}
	}
	g.noise()
	if err := g.repairsAndOfflines(); err != nil {
		return nil, err
	}
	g.finish()
	return g.ds, nil
}

func (sc *Scenario) validate() error {
	h := sc.Host
	switch {
	case sc.Name == "":
		return errors.New("scenario has no name")
	case h.Name == "" || h.Sockets < 1 || h.IMCs < 1 || h.ChannelsPerIMC < 1 || h.SlotsPerChannel < 1:
		return errors.New("scenario host is incomplete")
	case h.Geometry.Ranks < 1 || h.Geometry.Devices < 2 || h.Geometry.BankGroups < 1 || h.Geometry.Banks < 2 ||
		h.Geometry.Rows < 16 || h.Geometry.Columns < 16 || h.Geometry.ColumnStep < 1 || h.Geometry.DeviceWidth < 1:
		return errors.New("scenario geometry is incomplete")
	case !sc.End.After(sc.Start):
		return errors.New("scenario ends before it starts")
	}
	for i, r := range sc.Reboots {
		if !r.At.After(sc.Start) || !r.At.Before(sc.End) {
			return fmt.Errorf("reboot %d is outside the scenario span", i)
		}
	}
	isReboot := func(t time.Time) bool {
		for _, r := range sc.Reboots {
			if r.At.Equal(t) {
				return true
			}
		}
		return false
	}
	for i, m := range sc.Moves {
		if !isReboot(m.At) {
			return fmt.Errorf("move %d does not happen at a reboot", i)
		}
	}
	for i, r := range sc.Repairs {
		if r.Fault < 0 || r.Fault >= len(sc.Faults) {
			return fmt.Errorf("repair %d names fault %d, which does not exist", i, r.Fault)
		}
		if r.Kind != model.ActionSPPR && r.Kind != model.ActionHPPR && r.Kind != model.ActionSparing {
			return fmt.Errorf("repair %d has kind %q", i, r.Kind)
		}
	}
	for i, o := range sc.Offlines {
		if o.Fault < 0 || o.Fault >= len(sc.Faults) || o.Pages < 1 {
			return fmt.Errorf("offline %d is incomplete", i)
		}
	}
	for i, r := range sc.Reseats {
		if !isReboot(r.At) {
			return fmt.Errorf("reseat %d does not happen at a reboot", i)
		}
	}
	for i, m := range sc.Marks {
		if m.Kind != model.ActionDisable && m.Kind != model.ActionMarkFailed {
			return fmt.Errorf("mark %d has kind %q", i, m.Kind)
		}
		if m.At.Before(sc.Start) || m.At.After(sc.End) {
			return fmt.Errorf("mark %d is outside the scenario span", i)
		}
	}
	for i, f := range sc.Faults {
		if !f.Mode.Valid() || f.Mode == model.ModeUndetermined {
			return fmt.Errorf("fault %d has mode %q", i, f.Mode)
		}
		if f.Events < 0 {
			return fmt.Errorf("fault %d has a negative event count", i)
		}
	}
	return nil
}

type generator struct {
	sc  *Scenario
	ds  *Dataset
	rng *rand.Rand
	// byFault keeps the indices of the corrected events of each fault, in
	// generation order, for the page offline actions.
	byFault map[int][]int
}

func (g *generator) board() {
	h := g.sc.Host
	for s := range h.Sockets {
		for imc := range h.IMCs {
			for ch := range h.ChannelsPerIMC {
				for pos := range h.SlotsPerChannel {
					g.ds.Slots = append(g.ds.Slots, BoardSlot{
						Name: h.SlotName(s, imc, ch, pos), Index: len(g.ds.Slots), Socket: s, IMC: imc, Channel: ch, Position: pos,
					})
				}
			}
		}
	}
}

func (g *generator) hex(n int) string {
	const digits = "0123456789ABCDEF"
	b := make([]byte, n)
	for i := range b {
		b[i] = digits[g.rng.IntN(16)]
	}
	return string(b)
}

func (g *generator) boots() {
	sc := g.sc
	reboots := append([]Reboot(nil), sc.Reboots...)
	sort.Slice(reboots, func(i, j int) bool { return reboots[i].At.Before(reboots[j].At) })
	// The boot that is running when the scenario starts began earlier.
	all := append([]Reboot{{At: sc.Start.Add(-37 * time.Hour)}}, reboots...)
	kernel, bios := sc.Host.Kernel, sc.Host.BIOS
	for i, r := range all {
		if r.Kernel != "" {
			kernel = r.Kernel
		}
		if r.BIOS != "" {
			bios = r.BIOS
		}
		b := model.Boot{
			Host: sc.Host.Name, BootID: stringsLower(g.hex(32)), StartedAt: r.At, StartQuality: model.TimeExact,
			Kernel: kernel, BIOS: bios, Cmdline: "BOOT_IMAGE=(hd0,gpt2)/vmlinuz-" + kernel + " root=/dev/mapper/rl-root ro",
			Source: "synth", RawRef: fmt.Sprintf("synth:%s#boot%d", sc.Name, i),
		}
		if i+1 < len(all) {
			b.LastSeen = all[i+1].At.Add(-4 * time.Minute)
		} else {
			b.LastSeen = sc.End
		}
		g.ds.Boots = append(g.ds.Boots, b)
	}
}

func stringsLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'F' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

func (g *generator) slotIndex(name string) int {
	for _, s := range g.ds.Slots {
		if s.Name == name {
			return s.Index
		}
	}
	return -1
}

func (g *generator) occupancy() error {
	sc := g.sc
	geo := sc.Host.Geometry
	first := make([]int, len(g.ds.Slots))
	seen := map[string]bool{}
	for i, s := range g.ds.Slots {
		first[i] = -1
		if s.Position > 0 && !sc.Host.PopulateAll {
			continue
		}
		serial := g.hex(6)
		for seen[serial] || !model.UsableSerial(serial) {
			serial = g.hex(6)
		}
		seen[serial] = true
		d := model.DIMM{
			Identity: model.IdentitySerial, Manufacturer: "Example Memory", PartNumber: "EXM64G48R2X4-A", Serial: serial,
			SizeBytes: geo.SizeBytes(), MemoryType: "DDR5", FormFactor: "DIMM", ModuleType: "RDIMM", Technology: "DRAM",
			Ranks: model.Some(geo.Ranks), DeviceWidth: model.Some(geo.DeviceWidth), DataWidth: model.Some(64), TotalWidth: model.Some(80),
			SpeedMTs: model.Some(4800), ConfiguredSpeedMTs: model.Some(4800),
			MfgDate: fmt.Sprintf("2025-W%02d", 1+g.rng.IntN(52)),
		}
		d.UID = model.DIMMUID(d.Manufacturer, d.PartNumber, d.Serial)
		first[i] = len(g.ds.Modules)
		g.ds.Modules = append(g.ds.Modules, d)
	}
	cur := first
	for _, b := range g.ds.Boots {
		next := append([]int(nil), cur...)
		for i, m := range sc.Moves {
			if !m.At.Equal(b.StartedAt) {
				continue
			}
			a, c := g.slotIndex(m.A), g.slotIndex(m.B)
			if a < 0 || c < 0 {
				return fmt.Errorf("move %d names a slot the host does not have (%s, %s)", i, m.A, m.B)
			}
			next[a], next[c] = next[c], next[a]
		}
		g.ds.Occupancy = append(g.ds.Occupancy, next)
		cur = next
	}
	return nil
}

// bootAt returns the index of the boot running at t.
func (g *generator) bootAt(t time.Time) int {
	n := 0
	for i, b := range g.ds.Boots {
		if !b.StartedAt.After(t) {
			n = i
		}
	}
	return n
}

// slotOfModule returns the slot the module sits in during a boot, or -1.
func (g *generator) slotOfModule(boot, module int) int {
	for slot, m := range g.ds.Occupancy[boot] {
		if m == module {
			return slot
		}
	}
	return -1
}

// address returns the system physical address of a location in a slot. The
// synthetic map gives every slot one contiguous range above 4 GiB; a real
// memory map interleaves channels.
func (g *generator) address(slot int, l located) uint64 {
	geo := g.sc.Host.Geometry
	base := uint64(4<<30) + uint64(slot)*uint64(geo.SizeBytes())
	bank := uint64(l.bg*geo.Banks + l.bank)
	off := uint64(l.rank)*uint64(geo.RankBytes()) + ((bank*uint64(geo.Rows)+uint64(l.row))*uint64(geo.Columns)+uint64(l.col))*8
	return base + off
}

func (g *generator) column() int {
	geo := g.sc.Host.Geometry
	return g.rng.IntN(geo.Columns/geo.ColumnStep) * geo.ColumnStep
}

func (g *generator) dq(bits int) int {
	w := g.sc.Host.Geometry.DeviceWidth
	mask := 0
	for popcount(mask) < min(bits, w) {
		mask |= 1 << g.rng.IntN(w)
	}
	return mask
}

func popcount(v int) int {
	n := 0
	for ; v != 0; v &= v - 1 {
		n++
	}
	return n
}

// times draws n event times between start and end, in order.
func (g *generator) times(n int, start, end time.Time, trend Trend) []time.Time {
	span := end.Sub(start)
	out := make([]time.Time, n)
	for i := range out {
		u := g.rng.Float64()
		switch trend {
		case Rising:
			u = math.Cbrt(u) // density grows with the square of the elapsed time
		case Burst:
			if g.rng.Float64() < 0.8 {
				// A storm in the last day and a half.
				storm := float64(36*time.Hour) / float64(span)
				u = 1 - math.Min(storm, 1)*g.rng.Float64()
			}
		}
		out[i] = start.Add(time.Duration(u * float64(span))).Truncate(time.Microsecond)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

func pick(o model.Opt[int], or int) int {
	if o.Valid {
		return o.V
	}
	return or
}

// fault generates the events of one fault.
func (g *generator) fault(index int) error {
	sc, geo := g.sc, g.sc.Host.Geometry
	f := sc.Faults[index]
	start, end := f.Start, f.End
	if start.IsZero() {
		start = sc.Start
	}
	if end.IsZero() {
		end = sc.End
	}
	if !end.After(start) {
		return fmt.Errorf("fault %d ends before it starts", index)
	}
	slot0 := g.slotIndex(f.Slot)
	if slot0 < 0 {
		return fmt.Errorf("fault %d names slot %q, which the host does not have", index, f.Slot)
	}
	module := g.ds.Occupancy[g.bootAt(start)][slot0]
	if module < 0 {
		return fmt.Errorf("fault %d names slot %q, which is empty", index, f.Slot)
	}

	// The fixed part of the location.
	fix := located{
		rank:   pick(f.Rank, g.rng.IntN(geo.Ranks)),
		device: pick(f.Device, g.rng.IntN(geo.Devices)),
		bg:     pick(f.BankGroup, g.rng.IntN(geo.BankGroups)),
		bank:   pick(f.Bank, g.rng.IntN(geo.Banks)),
		row:    pick(f.Row, 8+g.rng.IntN(geo.Rows-16)),
		col:    pick(f.Column, g.column()),
	}
	truth := Truth{Fault: index, Mode: f.Mode, Slot: f.Slot, Serial: g.ds.Modules[module].Serial,
		Spreading: f.Mode == model.ModeRow && f.SpreadToNextRow > 0}
	some := model.Some[int]

	// Pools the varying part is drawn from.
	colPool := make([]int, 6+g.rng.IntN(7))
	for i := range colPool {
		colPool[i] = g.column()
	}
	rowPool := make([]int, 8+g.rng.IntN(13))
	for i := range rowPool {
		rowPool[i] = g.rng.IntN(geo.Rows)
	}
	type bankID struct{ bg, bank int }
	bankPool := []bankID{{fix.bg, fix.bank}}
	for len(bankPool) < 2+g.rng.IntN(4) {
		b := bankID{g.rng.IntN(geo.BankGroups), g.rng.IntN(geo.Banks)}
		dup := false
		for _, x := range bankPool {
			dup = dup || x == b
		}
		if !dup {
			bankPool = append(bankPool, b)
		}
	}
	devPool := []int{fix.device}
	for len(devPool) < 2+g.rng.IntN(3) {
		d := g.rng.IntN(geo.Devices)
		dup := false
		for _, x := range devPool {
			dup = dup || x == d
		}
		if !dup {
			devPool = append(devPool, d)
		}
	}
	lineDQ := g.dq(1 + g.rng.IntN(2))
	if f.Mode == model.ModeRow && f.Device.Valid && f.Row.Valid {
		lineDQ = 0b1100 & (1<<geo.DeviceWidth - 1) // the demo names DQ 2 and 3
		if lineDQ == 0 {
			lineDQ = 1
		}
	}
	bitDQ := g.dq(1)

	// draw returns the location of an event; progress runs from 0 to 1
	// over the life of the fault and lets line faults spread.
	draw := func(progress float64) located {
		l := fix
		spread := func(pool []int) int {
			n := 1 + int(progress*float64(len(pool)-1)+0.5)
			// Earlier members of the pool fail more often.
			i := int(math.Abs(g.rng.NormFloat64()) * float64(n) / 2.5)
			return pool[min(i, n-1)]
		}
		switch f.Mode {
		case model.ModeSingleBit:
			l.dq = bitDQ
		case model.ModeRow:
			l.col, l.dq = spread(colPool), lineDQ
			if progress > 2.0/3 && g.rng.Float64() < f.SpreadToNextRow {
				l.row++
			}
		case model.ModeMultiRow:
			if g.rng.Float64() < 0.25 {
				l.row++
			}
			l.col, l.dq = spread(colPool), lineDQ
		case model.ModeColumn:
			l.row, l.dq = spread(rowPool), bitDQ
		case model.ModeBank:
			l.row, l.col, l.dq = g.rng.IntN(geo.Rows), g.column(), g.dq(1)
		case model.ModeDevice:
			b := bankPool[g.rng.IntN(len(bankPool))]
			l.bg, l.bank, l.row, l.col, l.dq = b.bg, b.bank, g.rng.IntN(geo.Rows), g.column(), g.dq(1+g.rng.IntN(2))
		case model.ModeDIMM:
			l.rank, l.device = g.rng.IntN(geo.Ranks), devPool[g.rng.IntN(len(devPool))]
			l.bg, l.bank, l.row, l.col, l.dq = g.rng.IntN(geo.BankGroups), g.rng.IntN(geo.Banks), g.rng.IntN(geo.Rows), g.column(), g.dq(1)
		default: // system issue: nothing in the DIMM is fixed
			l = located{rank: g.rng.IntN(geo.Ranks), device: g.rng.IntN(geo.Devices), bg: g.rng.IntN(geo.BankGroups),
				bank: g.rng.IntN(geo.Banks), row: g.rng.IntN(geo.Rows), col: g.column(), dq: g.dq(1)}
		}
		return l
	}
	switch f.Mode {
	case model.ModeSingleBit:
		truth.Rank, truth.Device, truth.BankGroup, truth.Bank, truth.Row, truth.Column = some(fix.rank), some(fix.device), some(fix.bg), some(fix.bank), some(fix.row), some(fix.col)
	case model.ModeRow, model.ModeMultiRow:
		truth.Rank, truth.Device, truth.BankGroup, truth.Bank, truth.Row = some(fix.rank), some(fix.device), some(fix.bg), some(fix.bank), some(fix.row)
	case model.ModeColumn:
		truth.Rank, truth.Device, truth.BankGroup, truth.Bank, truth.Column = some(fix.rank), some(fix.device), some(fix.bg), some(fix.bank), some(fix.col)
	case model.ModeBank:
		truth.Rank, truth.Device, truth.BankGroup, truth.Bank = some(fix.rank), some(fix.device), some(fix.bg), some(fix.bank)
	case model.ModeDevice:
		truth.Rank, truth.Device = some(fix.rank), some(fix.device)
	}

	// A successful repair of the row ends a fault that is set to stop.
	stop := time.Time{}
	if f.StopAfterRepair {
		for _, r := range sc.Repairs {
			if r.Fault == index && !r.Failed && (stop.IsZero() || r.At.Before(stop)) {
				stop = r.At
			}
		}
	}

	emit := func(t time.Time, sev model.Severity, scrub model.ScrubType, l located) bool {
		boot := g.bootAt(t)
		slot := slot0
		if !f.StaysWithSlot {
			slot = g.slotOfModule(boot, module)
		}
		if slot < 0 || g.ds.Occupancy[boot][slot] < 0 {
			return false // the DIMM is out of the machine, or the slot is empty
		}
		g.event(index, t, boot, slot, sev, scrub, l)
		return true
	}
	ts := g.times(f.Events, start, end, f.Trend)
	for i, t := range ts {
		l := draw(float64(i) / float64(max(len(ts)-1, 1)))
		if !stop.IsZero() && !t.Before(stop) {
			continue
		}
		scrub := model.ScrubDemand
		if g.rng.Float64() < f.PatrolShare {
			scrub = model.ScrubPatrol
		}
		before := len(g.ds.Events)
		if emit(t, model.SevCE, scrub, l) {
			g.byFault[index] = append(g.byFault[index], before)
			truth.CE++
		}
	}
	for _, t := range f.UE {
		l := draw(0)
		if emit(t.Truncate(time.Microsecond), model.SevUE, model.ScrubDemand, l) {
			truth.UE++
			// The kernel takes the page of an uncorrected error offline.
			e := g.ds.Events[len(g.ds.Events)-1]
			g.action(model.Action{
				At: e.ErrorDT.Add(300 * time.Millisecond), Kind: model.ActionPageOffline, Method: "hard", Result: model.ResultOK,
				OsPhysicalAddress: model.Some(e.OsPhysicalAddress.V &^ 0xfff), Slot: e.Slot, Label: e.Label, Detail: "dirty LRU page: Recovered",
			})
		}
	}
	g.ds.Truth = append(g.ds.Truth, truth)
	return nil
}

// event appends one error event. fault is the index of the fault it belongs
// to, or -1 for noise.
func (g *generator) event(_ int, t time.Time, boot, slot int, sev model.Severity, scrub model.ScrubType, l located) {
	h := g.sc.Host
	b := g.ds.Slots[slot]
	some := model.Some[int]
	addr := g.address(slot, l)
	errType := "memory read error"
	if scrub == model.ScrubPatrol {
		errType = "memory scrubbing error"
	}
	e := model.Event{
		Host: h.Name, Kind: model.KindError, ErrorDT: t, ErrorSeverity: sev, ErrorScrubType: scrub,
		SocketId: some(b.Socket), ChannelId: some(b.Channel), ChannelSlotId: some(b.Position), IMC: some(b.IMC),
		Rank: some(l.rank), DeviceId: some(l.device), BankGroup: some(l.bg), Bank: some(l.bank), Row: some(l.row), Column: some(l.col),
		ErrorVector: fmt.Sprintf("dq:0x%x", l.dq), OsPhysicalAddress: model.Some(addr),
		Source: "synth", Grade: model.GradeA, BootID: g.ds.Boots[boot].BootID, Count: 1, TimeQuality: model.TimeExact,
		EdacMC: some(b.EdacMC(h)), EdacLocation: fmt.Sprintf("%d:%d", b.Channel, b.Position),
		Label: b.Name, Slot: b.Name, ErrorType: errType, Decoder: "synth",
	}
	if m := g.ds.Occupancy[boot][slot]; m >= 0 {
		e.DIMMUID = g.ds.Modules[m].UID
	}
	g.ds.Events = append(g.ds.Events, e)
}

func (g *generator) action(a model.Action) {
	a.Host = g.sc.Host.Name
	a.TimeQuality = model.TimeExact
	a.Source = "synth"
	if a.Count == 0 {
		a.Count = 1
	}
	g.ds.Actions = append(g.ds.Actions, a)
}

// noise adds single corrected errors that belong to no fault.
func (g *generator) noise() {
	sc, geo := g.sc, g.sc.Host.Geometry
	days := sc.End.Sub(sc.Start).Hours() / 24
	n := int(sc.NoisePerDIMMDay*days*float64(len(g.ds.Modules)) + 0.5)
	for range n {
		t := sc.Start.Add(time.Duration(g.rng.Float64() * float64(sc.End.Sub(sc.Start)))).Truncate(time.Microsecond)
		boot := g.bootAt(t)
		slot := g.rng.IntN(len(g.ds.Slots))
		if g.ds.Occupancy[boot][slot] < 0 {
			continue
		}
		l := located{rank: g.rng.IntN(geo.Ranks), device: g.rng.IntN(geo.Devices), bg: g.rng.IntN(geo.BankGroups),
			bank: g.rng.IntN(geo.Banks), row: g.rng.IntN(geo.Rows), col: g.column(), dq: g.dq(1)}
		scrub := model.ScrubDemand
		if g.rng.Float64() < 0.5 {
			scrub = model.ScrubPatrol
		}
		g.event(-1, t, boot, slot, model.SevCE, scrub, l)
	}
}

func (g *generator) repairsAndOfflines() error {
	sc := g.sc
	for i, r := range sc.Repairs {
		t := g.ds.Truth[r.Fault]
		// A PPR repairs one row. Sparing replaces a rank or a device.
		if r.Kind == model.ActionSparing {
			if !t.Rank.Valid {
				return fmt.Errorf("repair %d targets fault %d, whose mode %q has no single rank", i, r.Fault, t.Mode)
			}
			t.BankGroup, t.Bank, t.Row = model.Opt[int]{}, model.Opt[int]{}, model.Opt[int]{}
		} else if !t.Row.Valid {
			return fmt.Errorf("repair %d targets fault %d, whose mode %q has no single row", i, r.Fault, t.Mode)
		}
		boot := g.bootAt(r.At)
		slot := g.slotIndex(t.Slot)
		if !sc.Faults[r.Fault].StaysWithSlot {
			for m := range g.ds.Modules {
				if g.ds.Modules[m].Serial == t.Serial {
					slot = g.slotOfModule(boot, m)
				}
			}
		}
		if slot < 0 {
			return fmt.Errorf("repair %d: the DIMM of fault %d is not in the machine at that time", i, r.Fault)
		}
		res := model.ResultOK
		if r.Failed {
			res = model.ResultFailed
		}
		name := g.ds.Slots[slot].Name
		g.action(model.Action{
			At: r.At.Add(40 * time.Second), Kind: r.Kind, Method: "bios", Result: res,
			Rank: t.Rank, DeviceId: t.Device, BankGroup: t.BankGroup, Bank: t.Bank, Row: t.Row,
			Slot: name, Label: name, Detail: "repair during memory initialisation",
		})
	}
	// Pages that are offline already are not taken offline again.
	offlined := map[int]map[uint64]bool{}
	offs := append([]Offline(nil), sc.Offlines...)
	sort.SliceStable(offs, func(a, b int) bool { return offs[a].At.Before(offs[b].At) })
	for i, o := range offs {
		if offlined[o.Fault] == nil {
			offlined[o.Fault] = map[uint64]bool{}
		}
		// The pages of the fault with the most corrected errors so far.
		count := map[uint64]int{}
		slotOf := map[uint64]string{}
		for _, idx := range g.byFault[o.Fault] {
			e := g.ds.Events[idx]
			page := e.OsPhysicalAddress.V &^ 0xfff
			if e.ErrorDT.After(o.At) || offlined[o.Fault][page] {
				continue
			}
			count[page]++
			slotOf[page] = e.Slot
		}
		pages := make([]uint64, 0, len(count))
		for p := range count {
			pages = append(pages, p)
		}
		sort.Slice(pages, func(a, b int) bool {
			if count[pages[a]] != count[pages[b]] {
				return count[pages[a]] > count[pages[b]]
			}
			return pages[a] < pages[b]
		})
		if len(pages) == 0 {
			return fmt.Errorf("offline %d: fault %d has no errors before that time", i, o.Fault)
		}
		for j, p := range pages[:min(o.Pages, len(pages))] {
			offlined[o.Fault][p] = true
			g.action(model.Action{
				At: o.At.Add(time.Duration(j) * 2 * time.Second), Kind: model.ActionPageOffline, Method: "soft", Result: model.ResultOK,
				OsPhysicalAddress: model.Some(p), Slot: slotOf[p], Label: slotOf[p], Detail: "corrected error threshold",
			})
		}
	}
	for i, r := range sc.Reseats {
		if g.slotIndex(r.Slot) < 0 {
			return fmt.Errorf("reseat %d names slot %q, which the host does not have", i, r.Slot)
		}
		g.action(model.Action{At: r.At, Kind: model.ActionReseat, Method: "operator", Result: model.ResultOK, Slot: r.Slot, Label: r.Slot,
			Detail: "DIMM reseated"})
	}
	for i, m := range sc.Marks {
		if g.slotIndex(m.Slot) < 0 {
			return fmt.Errorf("mark %d names slot %q, which the host does not have", i, m.Slot)
		}
		g.action(model.Action{At: m.At, Kind: m.Kind, Method: m.Method, Result: model.ResultOK, Slot: m.Slot, Label: m.Slot, Detail: m.Detail})
	}
	return nil
}

// finish orders the records and gives them their references.
func (g *generator) finish() {
	ds, sc := g.ds, g.sc
	sort.SliceStable(ds.Events, func(i, j int) bool { return ds.Events[i].ErrorDT.Before(ds.Events[j].ErrorDT) })
	for i := range ds.Events {
		e := &ds.Events[i]
		e.RawRef = fmt.Sprintf("synth:%s#event%d", sc.Name, i+1)
		e.Fingerprint = model.Fingerprint("synth", sc.Name, fmt.Sprint(sc.Seed), "event", fmt.Sprint(i+1))
		e.Raw = intelLine(sc.Host, ds, e)
	}
	sort.SliceStable(ds.Actions, func(i, j int) bool { return ds.Actions[i].At.Before(ds.Actions[j].At) })
	for i := range ds.Actions {
		a := &ds.Actions[i]
		a.BootID = ds.Boots[g.bootAt(a.At)].BootID
		a.RawRef = fmt.Sprintf("synth:%s#action%d", sc.Name, i+1)
		a.Fingerprint = model.Fingerprint("synth", sc.Name, fmt.Sprint(sc.Seed), "action", fmt.Sprint(i+1))
		a.Raw = fmt.Sprintf("%s %s %s %s", a.At.UTC().Format(time.RFC3339), a.Kind, a.Slot, a.Detail)
	}
}
