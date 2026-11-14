package ingest

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/agenticode/my/internal/decode/edac"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// Sources name a memory slot in different ways. SMBIOS has a device locator
// ("CPU0_C1") and a handle; EDAC has a controller index, a layer position
// and a label; a decoded error has socket, controller, channel and DIMM
// numbers. The ledger keeps one row per physical slot and an alias for
// every name a source used, and ties each event to a slot through the alias
// of the name its source used.
//
// The SMBIOS locator is the name of a slot when SMBIOS was read, because it
// is what is printed on the board. Tying an EDAC position to it is only done
// on evidence: the EDAC label equals the locator, or the socket, channel and
// position numbers of both sides agree for every populated slot. A guess
// here would attribute errors to the wrong DIMM, so without evidence the
// EDAC position stays a slot of its own and the run says so.

// Alias keys.
func aliasHandle(h int) string { return "handle:" + strconv.Itoa(h) }
func aliasEDAC(mc int, loc string) string {
	return "edac:" + strconv.Itoa(mc) + ":" + loc
}
func aliasLabel(label string) string { return "label:" + model.NormalizeLabel(label) }
func aliasTopo(socket, imc, channel, position int) string {
	return fmt.Sprintf("topo:%d:%d:%d:%d", socket, imc, channel, position)
}

var bankLocatorREs = []*regexp.Regexp{
	// P0_Node0_Channel2_Dimm0, _Node0_Channel2_Dimm0
	regexp.MustCompile(`(?i)^(?:P(\d+))?_?Node(\d+)_Channel(\d+)_Dimm(\d+)$`),
	// NODE 0 CHANNEL 2 DIMM 0
	regexp.MustCompile(`(?i)^()NODE (\d+) CHANNEL (\d+) DIMM (\d+)$`),
}

// bankLocatorTopology reads socket, channel and position out of an SMBIOS
// bank locator that spells them out as numbers. Locators that use letters
// or board-specific names are not interpreted: vendors disagree on what
// the letters mean.
func bankLocatorTopology(bank string) (socket, channel, position model.Opt[int]) {
	bank = strings.TrimSpace(bank)
	for _, re := range bankLocatorREs {
		m := re.FindStringSubmatch(bank)
		if m == nil {
			continue
		}
		s := m[1]
		if s == "" {
			s = m[2]
		}
		sv, err1 := strconv.Atoi(s)
		cv, err2 := strconv.Atoi(m[3])
		pv, err3 := strconv.Atoi(m[4])
		if err1 != nil || err2 != nil || err3 != nil {
			return
		}
		return model.Some(sv), model.Some(cv), model.Some(pv)
	}
	return
}

// boardSlot is one SMBIOS slot of the current scan.
type boardSlot struct {
	name                      string
	mod                       *model.Module
	socket, channel, position model.Opt[int]
}

// edacDIMM is one EDAC DIMM object of the current scan.
type edacDIMM struct {
	mc      int
	loc     string
	label   string
	sizeMB  model.Opt[int]
	devType string
	parsed  edac.Label
}

// slotNames gives every SMBIOS slot a name that is unique on the host: the
// device locator, with the bank locator added where two slots share one.
func slotNames(mods []*model.Module) []boardSlot {
	count := map[string]int{}
	for _, m := range mods {
		count[model.NormalizeLabel(m.Locator)]++
	}
	out := make([]boardSlot, 0, len(mods))
	seen := map[string]int{}
	for _, m := range mods {
		name := strings.TrimSpace(m.Locator)
		if name == "" || count[model.NormalizeLabel(m.Locator)] > 1 {
			name = strings.TrimSpace(m.BankLocator + " " + m.Locator)
		}
		if name == "" && m.Handle.Valid {
			name = fmt.Sprintf("handle 0x%04X", m.Handle.V)
		}
		if name == "" {
			name = "slot"
		}
		key := model.NormalizeLabel(name)
		if seen[key]++; seen[key] > 1 {
			name = fmt.Sprintf("%s #%d", name, seen[key])
		}
		s := boardSlot{name: name, mod: m}
		s.socket, s.channel, s.position = bankLocatorTopology(m.BankLocator)
		out = append(out, s)
	}
	return out
}

// mapping is the result of tying EDAC DIMMs to SMBIOS slots.
type mapping struct {
	// slot[i] is the index of the SMBIOS slot of EDAC DIMM i, or -1.
	slot []int
	// method[i] says how EDAC DIMM i was tied: model.MapLabel or
	// model.MapBankLocator.
	method []string
	// chanPerIMC is the number of channels per memory controller found for
	// Intel style positions, or 0.
	chanPerIMC int
}

// channelsPerController values tried when Intel style positions are
// matched against per-socket channel numbers.
var channelsPerController = []int{1, 2, 3, 4, 6, 8, 12}

// mapEDAC ties the EDAC DIMMs of a scan to its SMBIOS slots.
func mapEDAC(slots []boardSlot, dimms []edacDIMM) mapping {
	mp := mapping{slot: make([]int, len(dimms)), method: make([]string, len(dimms))}
	for i := range mp.slot {
		mp.slot[i] = -1
	}
	if len(slots) == 0 || len(dimms) == 0 {
		return mp
	}

	// 1. The label is the locator: set by the administrator or by ghes_edac.
	byLabel := map[string]int{}
	for i, s := range slots {
		for _, n := range []string{s.name, s.mod.Locator, s.mod.BankLocator + " " + s.mod.Locator} {
			if k := model.NormalizeLabel(n); k != "" {
				if _, dup := byLabel[k]; !dup {
					byLabel[k] = i
				}
			}
		}
	}
	for i, d := range dimms {
		if j, ok := byLabel[model.NormalizeLabel(d.label)]; ok && !edac.PlaceholderLabel(d.label) {
			mp.slot[i], mp.method[i] = j, model.MapLabel
		}
	}

	// 2. The numbers agree: every EDAC DIMM lands on a populated slot and
	// every populated slot is hit.
	populated := map[[3]int]int{} // socket, channel, position -> slot index
	for i, s := range slots {
		if s.mod.Populated && s.socket.Valid && s.channel.Valid && s.position.Valid {
			populated[[3]int{s.socket.V, s.channel.V, s.position.V}] = i
		}
	}
	if len(populated) == 0 {
		return mp
	}
	kind := ""
	for _, d := range dimms {
		k := d.parsed.Kind
		if strings.HasPrefix(k, "intel") {
			k = "intel"
		}
		if k == "" || (kind != "" && kind != k) {
			return mp // labels in no default form, or of mixed forms
		}
		kind = k
	}

	try := func(k int) ([]int, bool) {
		assign := make([]int, len(dimms))
		hit := map[int]bool{}
		for i, d := range dimms {
			var key [3]int
			switch kind {
			case "intel":
				if !d.parsed.Socket.Valid || !d.parsed.IMC.Valid || !d.parsed.Channel.Valid || !d.parsed.Slot.Valid || d.parsed.Channel.V >= k {
					return nil, false
				}
				key = [3]int{d.parsed.Socket.V, d.parsed.IMC.V*k + d.parsed.Channel.V, d.parsed.Slot.V}
			default: // csrow: one controller per socket, two chip selects per DIMM
				if !d.parsed.Channel.Valid || !d.parsed.Slot.Valid {
					return nil, false
				}
				key = [3]int{d.mc, d.parsed.Channel.V, d.parsed.Slot.V}
			}
			j, ok := populated[key]
			if !ok {
				return nil, false
			}
			if kind == "intel" {
				if hit[j] {
					return nil, false // two DIMMs on one slot
				}
				// Sizes must agree where both sides report one.
				if d.sizeMB.Valid && slots[j].mod.DIMM.SizeBytes > 0 && int64(d.sizeMB.V) != slots[j].mod.DIMM.SizeBytes>>20 {
					return nil, false
				}
			}
			hit[j] = true
			assign[i] = j
		}
		return assign, len(hit) == len(populated)
	}

	var found [][]int
	var foundK []int
	ks := channelsPerController
	if kind != "intel" {
		ks = []int{1}
	}
	for _, k := range ks {
		assign, ok := try(k)
		if !ok {
			continue
		}
		same := false
		for _, f := range found {
			if equalInts(f, assign) {
				same = true
			}
		}
		if !same {
			found = append(found, assign)
			foundK = append(foundK, k)
		}
	}
	if len(found) != 1 {
		return mp // no fit, or more than one: no evidence
	}
	for i, j := range found[0] {
		if mp.slot[i] == -1 {
			mp.slot[i], mp.method[i] = j, model.MapBankLocator
		}
	}
	if kind == "intel" {
		mp.chanPerIMC = foundK[0]
	}
	return mp
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// slotResult is what building the slot table of a scan found.
type slotResult struct {
	// unmapped counts EDAC DIMMs that could not be tied to an SMBIOS slot
	// although SMBIOS slots were read.
	unmapped int
	mapped   int
	notes    []string
}

// buildSlots writes the slot table, the aliases, the DIMM identities and the
// placements a scan observed.
func buildSlots(w *store.Writer, host string, mods []*model.Module, samples []*model.CounterSample) (slotResult, error) {
	var res slotResult
	slots := slotNames(mods)

	// The EDAC DIMM objects of the scan. Chip-select based drivers have
	// one object per rank; all of them are kept, they map to the same slot.
	var dimms []edacDIMM
	seen := map[string]bool{}
	for _, c := range samples {
		if c.DIMMIndex < 0 {
			continue
		}
		key := aliasEDAC(c.MC, c.Location) + "|" + c.Label
		if seen[key] {
			continue
		}
		seen[key] = true
		dimms = append(dimms, edacDIMM{mc: c.MC, loc: c.Location, label: c.Label, sizeMB: c.SizeMB, devType: c.DevType, parsed: edac.ParseLabel(c.Label)})
	}
	mp := mapEDAC(slots, dimms)

	alias := func(a, slot string) error { return w.SetSlotAlias(host, a, slot) }

	// SMBIOS slots.
	type edacOfSlot struct {
		first   *edacDIMM
		method  string
		devType string
	}
	bySlot := map[int]*edacOfSlot{}
	for i, j := range mp.slot {
		if j < 0 {
			continue
		}
		e := bySlot[j]
		if e == nil {
			e = &edacOfSlot{first: &dimms[i], method: mp.method[i], devType: dimms[i].devType}
			bySlot[j] = e
		}
	}
	for j, s := range slots {
		row := model.Slot{
			Host: host, Name: s.name, Locator: s.mod.Locator, BankLocator: s.mod.BankLocator, SMBIOSHandle: s.mod.Handle,
			Socket: s.socket, Channel: s.channel, Position: s.position, Populated: model.Some(s.mod.Populated),
		}
		if mp.chanPerIMC > 0 && s.channel.Valid {
			// Split the per-socket channel number the way the memory
			// controllers count, for empty slots too.
			row.IMC, row.Channel = model.Some(s.channel.V/mp.chanPerIMC), model.Some(s.channel.V%mp.chanPerIMC)
		}
		if e := bySlot[j]; e != nil {
			row.EdacMC, row.EdacLocation, row.EdacLabel, row.Mapping = model.Some(e.first.mc), e.first.loc, e.first.label, e.method
			if p := e.first.parsed; strings.HasPrefix(p.Kind, "intel") {
				row.Socket, row.IMC, row.Channel, row.Position = p.Socket, p.IMC, p.Channel, p.Slot
			}
		}
		if err := w.UpsertSlot(&row); err != nil {
			return res, err
		}
		for _, n := range []string{s.name, s.mod.Locator, strings.TrimSpace(s.mod.BankLocator + " " + s.mod.Locator)} {
			if strings.TrimSpace(n) != "" {
				if err := alias(aliasLabel(n), s.name); err != nil {
					return res, err
				}
			}
		}
		if s.mod.Handle.Valid {
			if err := alias(aliasHandle(s.mod.Handle.V), s.name); err != nil {
				return res, err
			}
		}
		if row.Socket.Valid && row.IMC.Valid && row.Channel.Valid && row.Position.Valid {
			if err := alias(aliasTopo(row.Socket.V, row.IMC.V, row.Channel.V, row.Position.V), s.name); err != nil {
				return res, err
			}
		}

		// The DIMM in the slot.
		if !s.mod.Populated {
			continue
		}
		d := s.mod.DIMM
		if model.UsableSerial(d.Serial) {
			d.UID, d.Identity = model.DIMMUID(d.Manufacturer, d.PartNumber, d.Serial), model.IdentitySerial
		} else {
			d.UID, d.Identity = model.SlotUID(host, s.name), model.IdentitySlot
			d.Serial = ""
		}
		d.FirstSeen, d.LastSeen = s.mod.ObservedAt, s.mod.ObservedAt
		if e := bySlot[j]; e != nil && !d.DeviceWidth.Valid {
			if wdt, err := strconv.Atoi(strings.TrimPrefix(e.devType, "x")); err == nil && strings.HasPrefix(e.devType, "x") && wdt > 0 {
				d.DeviceWidth = model.Some(wdt)
			}
		}
		if err := w.UpsertDIMM(&d); err != nil {
			return res, err
		}
		if err := w.ObservePlacement(d.UID, host, s.name, s.mod.ObservedAt, s.mod.Source); err != nil {
			return res, err
		}
	}

	// EDAC DIMMs: aliases for the mapped ones, slots of their own for the rest.
	for i, d := range dimms {
		name := ""
		if j := mp.slot[i]; j >= 0 {
			name = slots[j].name
			res.mapped++
		} else {
			name = edacSlotName(d.mc, d.loc, d.label)
			p := d.parsed
			row := model.Slot{Host: host, Name: name, EdacMC: model.Some(d.mc), EdacLocation: d.loc, EdacLabel: d.label, Populated: model.Some(true)}
			if strings.HasPrefix(p.Kind, "intel") {
				row.Socket, row.IMC, row.Channel, row.Position = p.Socket, p.IMC, p.Channel, p.Slot
			} else if p.Kind == "csrow" {
				row.Socket, row.Channel, row.Position = model.Some(d.mc), p.Channel, p.Slot
			}
			if err := w.UpsertSlot(&row); err != nil {
				return res, err
			}
			if len(slots) > 0 {
				res.unmapped++
			}
		}
		if d.loc != "" {
			if err := alias(aliasEDAC(d.mc, d.loc), name); err != nil {
				return res, err
			}
		}
		if !edac.PlaceholderLabel(d.label) {
			if err := alias(aliasLabel(d.label), name); err != nil {
				return res, err
			}
		}
		if p := d.parsed; strings.HasPrefix(p.Kind, "intel") && p.Socket.Valid && p.IMC.Valid && p.Channel.Valid && p.Slot.Valid {
			if err := alias(aliasTopo(p.Socket.V, p.IMC.V, p.Channel.V, p.Slot.V), name); err != nil {
				return res, err
			}
		}
	}

	if len(slots) > 0 && len(dimms) > 0 {
		switch {
		case res.unmapped == 0:
			res.notes = append(res.notes, fmt.Sprintf("all %d EDAC DIMM positions are tied to SMBIOS slots", res.mapped))
		default:
			res.notes = append(res.notes, fmt.Sprintf(
				"%d of %d EDAC DIMM positions could not be tied to an SMBIOS slot: their errors are kept under the EDAC label and are not attributed to a DIMM serial number",
				res.unmapped, len(dimms)))
		}
	}
	return res, nil
}

// edacSlotName names a slot that is only known through EDAC.
func edacSlotName(mc int, loc, label string) string {
	if !edac.PlaceholderLabel(label) {
		return strings.TrimSpace(label)
	}
	if loc != "" {
		return fmt.Sprintf("mc%d %s", mc, loc)
	}
	return fmt.Sprintf("mc%d", mc)
}

// resolveEventSlots ties every event of the host to a slot through the
// alias table. A location no alias knows becomes a slot of its own, named
// by what the source called it, so that every place errors were reported
// for is listed.
func resolveEventSlots(ctx context.Context, s *store.Store, host string) error {
	keys, err := s.EventLocations(ctx, host)
	if err != nil {
		return err
	}
	aliases, err := s.SlotAliases(ctx, host)
	if err != nil {
		return err
	}
	// Deterministic order: creating slots for unknown locations must not
	// depend on map iteration.
	sort.SliceStable(keys, func(i, j int) bool { return keys[i].Records > keys[j].Records })

	return s.Update(ctx, func(w *store.Writer) error {
		for _, k := range keys {
			name, known := lookupSlot(aliases, k)
			if !known {
				name = unknownSlotName(k)
				if name != "" {
					row := model.Slot{Host: host, Name: name, EdacMC: k.EdacMC, EdacLocation: k.EdacLocation,
						Socket: k.SocketId, IMC: k.IMC, Channel: k.ChannelId, Position: k.ChannelSlotId}
					if !edac.PlaceholderLabel(k.Label) {
						row.EdacLabel = k.Label
					}
					if err := w.UpsertSlot(&row); err != nil {
						return err
					}
					for _, a := range keyAliases(k) {
						if _, taken := aliases[a]; !taken {
							aliases[a] = name
							if err := w.SetSlotAlias(host, a, name); err != nil {
								return err
							}
						}
					}
				}
			}
			if err := w.SetEventSlot(host, k, name); err != nil {
				return err
			}
		}
		return nil
	})
}

// keyAliases lists the aliases a location key can be looked up by, most
// specific first.
func keyAliases(k store.LocationKey) []string {
	var out []string
	if k.SMBIOSHandle.Valid {
		out = append(out, aliasHandle(k.SMBIOSHandle.V))
	}
	if k.EdacMC.Valid && k.EdacLocation != "" {
		out = append(out, aliasEDAC(k.EdacMC.V, k.EdacLocation))
	}
	if !edac.PlaceholderLabel(k.Label) {
		out = append(out, aliasLabel(k.Label))
	}
	if k.SocketId.Valid && k.IMC.Valid && k.ChannelId.Valid && k.ChannelSlotId.Valid {
		out = append(out, aliasTopo(k.SocketId.V, k.IMC.V, k.ChannelId.V, k.ChannelSlotId.V))
	}
	return out
}

func lookupSlot(aliases map[string]string, k store.LocationKey) (string, bool) {
	for _, a := range keyAliases(k) {
		if name, ok := aliases[a]; ok {
			return name, true
		}
	}
	return "", false
}

// unknownSlotName names a location no alias knows, or returns "" when the
// event says nothing about where it happened.
func unknownSlotName(k store.LocationKey) string {
	switch {
	case !edac.PlaceholderLabel(k.Label):
		return strings.TrimSpace(k.Label)
	case k.EdacMC.Valid && k.EdacLocation != "":
		return fmt.Sprintf("mc%d %s", k.EdacMC.V, k.EdacLocation)
	case k.SocketId.Valid && k.IMC.Valid && k.ChannelId.Valid && k.ChannelSlotId.Valid:
		return fmt.Sprintf("socket%d imc%d channel%d dimm%d", k.SocketId.V, k.IMC.V, k.ChannelId.V, k.ChannelSlotId.V)
	case k.SMBIOSHandle.Valid:
		return fmt.Sprintf("handle 0x%04X", k.SMBIOSHandle.V)
	}
	return ""
}
