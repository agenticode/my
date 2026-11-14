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

// boardSlot is one slot the board has according to SMBIOS.
type boardSlot struct {
	name        string
	locator     string
	bankLocator string
	handle      model.Opt[int]
	populated   bool
	// sizeBytes is the size of the DIMM in the slot, 0 when unknown.
	sizeBytes int64
	// socket, channel and position as the bank locator spells them:
	// channel counts within the socket.
	socket, channel, position model.Opt[int]
}

// edacDIMM is one EDAC DIMM object of the latest counter reading.
type edacDIMM struct {
	mc      int
	loc     string
	label   string
	sizeMB  model.Opt[int]
	devType string
	parsed  edac.Label
}

// slotNames gives every SMBIOS slot of a scan a name that is unique on the
// host: the device locator, with the bank locator added where two slots
// share one.
func slotNames(mods []*model.Module) []string {
	count := map[string]int{}
	for _, m := range mods {
		count[model.NormalizeLabel(m.Locator)]++
	}
	out := make([]string, 0, len(mods))
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
		out = append(out, name)
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
		for _, n := range []string{s.name, s.locator, s.bankLocator + " " + s.locator} {
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
		if s.populated && s.socket.Valid && s.channel.Valid && s.position.Valid {
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
				if d.sizeMB.Valid && slots[j].sizeBytes > 0 && int64(d.sizeMB.V) != slots[j].sizeBytes>>20 {
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

// recordInventory stores what a scan saw in SMBIOS: the slots of the board,
// the identity of the DIMM in each populated slot, and that the DIMM was in
// that slot at the time of the scan.
func recordInventory(w *store.Writer, host string, mods []*model.Module) error {
	names := slotNames(mods)
	for i, m := range mods {
		name := names[i]
		row := model.Slot{Host: host, Name: name, Locator: m.Locator, BankLocator: m.BankLocator, SMBIOSHandle: m.Handle,
			Populated: model.Some(m.Populated)}
		row.Socket, row.Channel, row.Position = bankLocatorTopology(m.BankLocator)
		if err := w.UpsertSlot(&row); err != nil {
			return err
		}
		for _, n := range []string{name, m.Locator, strings.TrimSpace(m.BankLocator + " " + m.Locator)} {
			if strings.TrimSpace(n) != "" {
				if err := w.SetSlotAlias(host, aliasLabel(n), name); err != nil {
					return err
				}
			}
		}
		if m.Handle.Valid {
			if err := w.SetSlotAlias(host, aliasHandle(m.Handle.V), name); err != nil {
				return err
			}
		}
		if !m.Populated {
			continue
		}
		d := m.DIMM
		if model.UsableSerial(d.Serial) {
			d.UID, d.Identity = model.DIMMUID(d.Manufacturer, d.PartNumber, d.Serial), model.IdentitySerial
		} else {
			d.UID, d.Identity = model.SlotUID(host, name), model.IdentitySlot
			d.Serial = ""
		}
		d.FirstSeen, d.LastSeen = m.ObservedAt, m.ObservedAt
		if err := w.UpsertDIMM(&d); err != nil {
			return err
		}
		if err := w.ObservePlacement(d.UID, host, name, m.ObservedAt, m.Source); err != nil {
			return err
		}
	}
	return nil
}

// slotResult is what tying EDAC positions to SMBIOS slots found.
type slotResult struct {
	// mapped and unmapped count the EDAC DIMM objects that could and could
	// not be tied to an SMBIOS slot. Both are zero when one of the two
	// sides is not in the ledger.
	mapped, unmapped int
	notes            []string
}

// remapSlots ties the EDAC DIMM positions of the latest counter reading to
// the SMBIOS slots in the ledger. It works from the ledger, not from what
// the current run read, so the result is the same whether SMBIOS and the
// EDAC counters arrived in one run or in two, in either order.
func remapSlots(ctx context.Context, s *store.Store, host string) (slotResult, error) {
	var res slotResult
	rows, err := s.Slots(ctx, host)
	if err != nil {
		return res, err
	}
	occupants, err := s.SlotOccupants(ctx, host)
	if err != nil {
		return res, err
	}
	samples, err := s.LatestCounterSamples(ctx, host)
	if err != nil {
		return res, err
	}

	// The board as SMBIOS describes it.
	var slots []boardSlot
	var slotRows []model.Slot
	for _, r := range rows {
		if r.Locator == "" && !r.SMBIOSHandle.Valid {
			continue // a slot known only through EDAC or an event
		}
		b := boardSlot{name: r.Name, locator: r.Locator, bankLocator: r.BankLocator, handle: r.SMBIOSHandle, populated: r.Populated.Or(false)}
		b.socket, b.channel, b.position = bankLocatorTopology(r.BankLocator)
		if d, ok := occupants[r.Name]; ok && b.populated {
			b.sizeBytes = d.SizeBytes
		}
		slots = append(slots, b)
		slotRows = append(slotRows, r)
	}
	// The EDAC DIMM objects. Chip-select based drivers have one object per
	// rank; all of them are kept, they map to the same slot.
	var dimms []edacDIMM
	for _, c := range samples {
		dimms = append(dimms, edacDIMM{mc: c.MC, loc: c.Location, label: c.Label, sizeMB: c.SizeMB, devType: c.DevType, parsed: edac.ParseLabel(c.Label)})
	}
	mp := mapEDAC(slots, dimms)

	first := map[int]int{} // slot index -> index of the first EDAC DIMM tied to it
	for i, j := range mp.slot {
		if _, ok := first[j]; j >= 0 && !ok {
			first[j] = i
		}
	}

	err = s.Update(ctx, func(w *store.Writer) error {
		alias := func(a, slot string) error { return w.SetSlotAlias(host, a, slot) }

		// SMBIOS slots: rewrite the EDAC side of each row from the mapping.
		for j, b := range slots {
			row := model.Slot{Host: host, Name: b.name, Locator: b.locator, BankLocator: b.bankLocator, SMBIOSHandle: b.handle,
				Socket: b.socket, Channel: b.channel, Position: b.position, Populated: slotRows[j].Populated}
			if mp.chanPerIMC > 0 && b.channel.Valid {
				// Split the per-socket channel number the way the memory
				// controllers count, for empty slots too.
				row.IMC, row.Channel = model.Some(b.channel.V/mp.chanPerIMC), model.Some(b.channel.V%mp.chanPerIMC)
			}
			if i, ok := first[j]; ok {
				d := dimms[i]
				row.EdacMC, row.EdacLocation, row.EdacLabel, row.Mapping = model.Some(d.mc), d.loc, d.label, mp.method[i]
				if p := d.parsed; strings.HasPrefix(p.Kind, "intel") {
					row.Socket, row.IMC, row.Channel, row.Position = p.Socket, p.IMC, p.Channel, p.Slot
				}
				// The device width is something only EDAC reports.
				if occ, ok := occupants[b.name]; ok && !occ.DeviceWidth.Valid && strings.HasPrefix(d.devType, "x") {
					if wdt, err := strconv.Atoi(d.devType[1:]); err == nil && wdt > 0 {
						if err := w.UpsertDIMM(&model.DIMM{UID: occ.UID, Identity: occ.Identity, DeviceWidth: model.Some(wdt)}); err != nil {
							return err
						}
					}
				}
			}
			if err := w.ReplaceSlot(&row); err != nil {
				return err
			}
			if row.Socket.Valid && row.IMC.Valid && row.Channel.Valid && row.Position.Valid {
				if err := alias(aliasTopo(row.Socket.V, row.IMC.V, row.Channel.V, row.Position.V), b.name); err != nil {
					return err
				}
			}
		}

		// EDAC DIMMs: aliases for the mapped ones, slots of their own for the rest.
		for i, d := range dimms {
			var name string
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
					return err
				}
				if len(slots) > 0 {
					res.unmapped++
				}
			}
			if d.loc != "" {
				if err := alias(aliasEDAC(d.mc, d.loc), name); err != nil {
					return err
				}
			}
			if !edac.PlaceholderLabel(d.label) {
				if err := alias(aliasLabel(d.label), name); err != nil {
					return err
				}
			}
			if p := d.parsed; strings.HasPrefix(p.Kind, "intel") && p.Socket.Valid && p.IMC.Valid && p.Channel.Valid && p.Slot.Valid {
				if err := alias(aliasTopo(p.Socket.V, p.IMC.V, p.Channel.V, p.Slot.V), name); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return res, err
	}

	if len(slots) > 0 && len(dimms) > 0 {
		if res.unmapped == 0 {
			res.notes = append(res.notes, fmt.Sprintf("all %d EDAC DIMM positions are tied to SMBIOS slots", res.mapped))
		} else {
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
