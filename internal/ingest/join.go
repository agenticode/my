package ingest

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// Two inventories can describe one board: the SMBIOS tables the host reads
// and the memory resources of its BMC. They may call a slot by different
// names ("A1" and "DIMM.Socket.A1"), and they may word the maker or the
// part number of a DIMM differently, which gives the DIMM two identities.
// Left alone, the ledger would hold every slot and every DIMM twice, and a
// DIMM that both sources place in the same slot would look replaced.
//
// SMBIOS names the slots of the ledger and identifies its DIMMs, because
// every host has it and its strings are the same wherever the DIMM goes.
// What another inventory recorded is joined to it on evidence only:
//
//   - a DIMM with the serial number of a DIMM SMBIOS described is that DIMM;
//   - a slot that held a DIMM SMBIOS saw in exactly one of its slots is that
//     slot, and so is a slot whose name differs from the name of one SMBIOS
//     slot in separators only;
//   - a DIMM without a serial number in a slot SMBIOS describes is the DIMM
//     SMBIOS saw there.
//
// The join works from the ledger, so the result does not depend on which
// inventory arrived first, or on whether they arrived in one run.

// smbiosSource is the source of the placements the SMBIOS adapter recorded.
const smbiosSource = "smbios"

// serialKey folds a serial number for comparison between sources: upper
// case, without spaces, dashes and underscores and without a leading "0x".
// It is "" for a serial number that identifies nothing.
func serialKey(serial string) string {
	if !model.UsableSerial(serial) {
		return ""
	}
	var b strings.Builder
	for _, r := range strings.ToUpper(serial) {
		if r != ' ' && r != '-' && r != '_' {
			b.WriteRune(r)
		}
	}
	return strings.TrimPrefix(b.String(), "0X")
}

// boardSerials returns the serial numbers of the DIMMs SMBIOS has listed
// for a host, folded for comparison.
func boardSerials(ctx context.Context, s *store.Store, host string) (map[string]bool, error) {
	out := map[string]bool{}
	placements, err := s.Placements(ctx, host, "")
	if err != nil || len(placements) == 0 {
		return out, err
	}
	dimms, err := s.PlacedDIMMs(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, p := range placements {
		if d := dimms[p.DIMMUID]; p.Source == smbiosSource && d.Identity == model.IdentitySerial {
			if k := serialKey(d.Serial); k != "" {
				out[k] = true
			}
		}
	}
	return out, nil
}

// otherMachine checks an inventory of a whole board against the serial
// numbers SMBIOS lists for the host. It returns the number of serial
// numbers the inventory lists when none of them is among those of SMBIOS,
// and 0 when at least one is, or when one of the two sides has none to
// compare.
func otherMachine(mods []*model.Module, board map[string]bool) int {
	if len(board) == 0 {
		return 0
	}
	listed := map[string]bool{}
	for _, m := range mods {
		if k := serialKey(m.DIMM.Serial); k != "" && m.Populated && !m.Handle.Valid {
			if board[k] {
				return 0
			}
			listed[k] = true
		}
	}
	return len(listed)
}

// joinResult says what joining the inventories of a host did.
type joinResult struct {
	// dimms counts DIMM identities another inventory made for DIMMs SMBIOS
	// describes, unnamed those among them that had no serial number.
	dimms, unnamed int
	// slots counts slots another inventory named differently from SMBIOS.
	slots int
	// empty counts empty slots of another inventory that were dropped
	// because SMBIOS lists the slots of the board under its own names.
	empty int
	// unclear lists slots that held DIMMs SMBIOS saw in several slots.
	unclear []string
}

// notes says in sentences what was joined.
func (r joinResult) notes() []string {
	var out []string
	if r.dimms > 0 {
		out = append(out, fmt.Sprintf("%d DIMMs that a second inventory lists are DIMMs SMBIOS describes, by serial number: they are kept under the identity SMBIOS gives them", r.dimms))
	}
	if r.unnamed > 0 {
		out = append(out, fmt.Sprintf("%d DIMMs that a second inventory lists without a serial number are taken to be the DIMMs SMBIOS saw in the same slots", r.unnamed))
	}
	if r.slots > 0 {
		out = append(out, fmt.Sprintf("%d slots have another name in a second inventory: the name SMBIOS uses is the name of the slot, and the other one is kept as a name records may use", r.slots))
	}
	if r.empty > 0 {
		out = append(out, fmt.Sprintf("%d empty slots of a second inventory have names SMBIOS does not use: SMBIOS lists the empty slots of the board under its own names, so these make no slots of their own", r.empty))
	}
	if len(r.unclear) > 0 {
		out = append(out, fmt.Sprintf("the slots %s of a second inventory held DIMMs that SMBIOS saw in more than one slot: they are kept as slots of their own",
			strings.Join(r.unclear, ", ")))
	}
	return out
}

// gaps returns what another description of a DIMM adds to the one that is
// kept: the fields the kept one leaves empty.
func gaps(kept, other model.DIMM) (model.DIMM, bool) {
	d := model.DIMM{UID: kept.UID, Identity: kept.Identity, FirstSeen: other.FirstSeen, LastSeen: other.LastSeen}
	found := false
	text := func(dst *string, have, add string) {
		if have == "" && add != "" {
			*dst, found = add, true
		}
	}
	num := func(dst *model.Opt[int], have, add model.Opt[int]) {
		if !have.Valid && add.Valid {
			*dst, found = add, true
		}
	}
	text(&d.Manufacturer, kept.Manufacturer, other.Manufacturer)
	text(&d.PartNumber, kept.PartNumber, other.PartNumber)
	text(&d.ManufacturerID, kept.ManufacturerID, other.ManufacturerID)
	text(&d.MemoryType, kept.MemoryType, other.MemoryType)
	text(&d.FormFactor, kept.FormFactor, other.FormFactor)
	text(&d.ModuleType, kept.ModuleType, other.ModuleType)
	text(&d.Technology, kept.Technology, other.Technology)
	text(&d.MfgDate, kept.MfgDate, other.MfgDate)
	num(&d.Ranks, kept.Ranks, other.Ranks)
	num(&d.DeviceWidth, kept.DeviceWidth, other.DeviceWidth)
	num(&d.DataWidth, kept.DataWidth, other.DataWidth)
	num(&d.TotalWidth, kept.TotalWidth, other.TotalWidth)
	num(&d.SpeedMTs, kept.SpeedMTs, other.SpeedMTs)
	num(&d.ConfiguredSpeedMTs, kept.ConfiguredSpeedMTs, other.ConfiguredSpeedMTs)
	if kept.SizeBytes == 0 && other.SizeBytes > 0 {
		d.SizeBytes, found = other.SizeBytes, true
	}
	return d, found || !other.FirstSeen.IsZero()
}

// joinInventories makes one DIMM and one slot of what two inventories of a
// board recorded as two. It does nothing for a host only one kind of
// inventory was read from.
func joinInventories(ctx context.Context, s *store.Store, host string) (joinResult, error) {
	var res joinResult
	placements, err := s.Placements(ctx, host, "")
	if err != nil {
		return res, err
	}
	board, other := false, false
	for _, p := range placements {
		if p.Source == smbiosSource {
			board = true
		} else {
			other = true
		}
	}
	if !board || !other {
		return res, nil
	}
	dimms, err := s.PlacedDIMMs(ctx, host)
	if err != nil {
		return res, err
	}

	// DIMMs. The identity SMBIOS gave each one, by serial number. A serial
	// number SMBIOS has under two identities settles nothing.
	canon := map[string]string{}
	twice := map[string]bool{}
	for _, p := range placements {
		d := dimms[p.DIMMUID]
		k := serialKey(d.Serial)
		if p.Source != smbiosSource || d.Identity != model.IdentitySerial || k == "" {
			continue
		}
		if uid, ok := canon[k]; ok && uid != p.DIMMUID {
			twice[k] = true
		}
		canon[k] = p.DIMMUID
	}
	adopt := map[string]string{}
	var adopted []string
	for _, p := range placements {
		d := dimms[p.DIMMUID]
		k := serialKey(d.Serial)
		if p.Source == smbiosSource || d.Identity != model.IdentitySerial || k == "" || twice[k] {
			continue
		}
		if to, ok := canon[k]; ok && to != p.DIMMUID {
			if _, done := adopt[p.DIMMUID]; !done {
				adopted = append(adopted, p.DIMMUID)
			}
			adopt[p.DIMMUID] = to
		}
	}
	if len(adopted) > 0 {
		sort.Strings(adopted)
		err := s.Update(ctx, func(w *store.Writer) error {
			for _, from := range adopted {
				to := adopt[from]
				if d, ok := gaps(dimms[to], dimms[from]); ok {
					if err := w.UpsertDIMM(&d); err != nil {
						return err
					}
				}
				if err := w.AdoptDIMM(host, from, to); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return res, err
		}
		res.dimms = len(adopted)
		if placements, err = s.Placements(ctx, host, ""); err != nil {
			return res, err
		}
	}

	// Slots. A slot SMBIOS does not describe that held a DIMM SMBIOS saw in
	// one of its slots is that slot.
	rows, err := s.Slots(ctx, host)
	if err != nil {
		return res, err
	}
	described := map[string]bool{} // slots SMBIOS describes
	for _, r := range rows {
		described[r.Name] = r.SMBIOSHandle.Valid
	}
	seenIn := map[string]map[string]bool{} // DIMM -> the slots SMBIOS saw it in
	held := map[string][]model.Placement{}
	for _, p := range placements {
		held[p.Slot] = append(held[p.Slot], p)
		if p.Source == smbiosSource && described[p.Slot] {
			if seenIn[p.DIMMUID] == nil {
				seenIn[p.DIMMUID] = map[string]bool{}
			}
			seenIn[p.DIMMUID][p.Slot] = true
		}
	}
	// The names of the slots SMBIOS describes, without their separators. A
	// name two of them share this way names neither.
	byName := map[string]string{}
	for _, r := range rows {
		if !r.SMBIOSHandle.Valid {
			continue
		}
		for _, n := range []string{r.Name, r.Locator, strings.TrimSpace(r.BankLocator + " " + r.Locator)} {
			k := squash(n)
			if k == "" {
				continue
			}
			if first, taken := byName[k]; taken && first != r.Name {
				byName[k] = ""
				continue
			}
			byName[k] = r.Name
		}
	}
	move := map[string]string{}
	var moved, dropped []string
	for _, r := range rows {
		if r.SMBIOSHandle.Valid {
			continue
		}
		targets := map[string]bool{}
		for _, p := range held[r.Name] {
			for slot := range seenIn[p.DIMMUID] {
				targets[slot] = true
			}
		}
		switch {
		case len(targets) == 1:
			for slot := range targets {
				move[r.Name] = slot
			}
			moved = append(moved, r.Name)
		case len(targets) > 1:
			res.unclear = append(res.unclear, r.Name)
		case r.Locator != "":
			// An inventory names this slot and SMBIOS saw none of its DIMMs.
			to := byName[squash(r.Name)]
			if to == "" {
				to = byName[squash(r.Locator)]
			}
			if to != "" {
				move[r.Name] = to
				moved = append(moved, r.Name)
			}
		}
	}
	// With slots of the other inventory tied to SMBIOS slots by their
	// DIMMs, both describe the same board: its empty slots under names
	// SMBIOS does not use are slots SMBIOS lists under its own names.
	if len(moved) > 0 {
		for _, r := range rows {
			if _, ok := move[r.Name]; ok || r.SMBIOSHandle.Valid || r.Locator == "" || r.EdacMC.Valid ||
				!r.Populated.Valid || r.Populated.V || len(held[r.Name]) > 0 {
				continue
			}
			dropped = append(dropped, r.Name)
		}
	}
	if len(moved) > 0 {
		err := s.Update(ctx, func(w *store.Writer) error {
			for _, from := range moved {
				if err := w.MoveSlot(host, from, move[from]); err != nil {
					return err
				}
			}
			for _, name := range dropped {
				ok, err := w.DropSlot(host, name)
				if err != nil {
					return err
				}
				if ok {
					res.empty++
				}
			}
			return nil
		})
		if err != nil {
			return res, err
		}
		res.slots = len(moved)
		if placements, err = s.Placements(ctx, host, ""); err != nil {
			return res, err
		}
	}

	// DIMMs without a serial number in slots SMBIOS describes: a source
	// that reads no serial numbers saw the DIMM SMBIOS saw there. Of several
	// DIMMs SMBIOS saw in the slot it is the one that was there at the time.
	type sighting struct {
		p  model.Placement
		to string
	}
	var unnamed []sighting
	bySlot := map[string][]model.Placement{}
	for _, p := range placements {
		if p.Source == smbiosSource && described[p.Slot] && dimms[p.DIMMUID].Identity == model.IdentitySerial {
			bySlot[p.Slot] = append(bySlot[p.Slot], p) // in time order
		}
	}
	for _, p := range placements {
		seen := bySlot[p.Slot]
		if p.Source == smbiosSource || len(seen) == 0 {
			continue
		}
		if dimms[p.DIMMUID].Identity != model.IdentitySlot {
			continue
		}
		to := seen[0].DIMMUID
		for _, q := range seen {
			if !q.FirstSeen.After(p.FirstSeen) {
				to = q.DIMMUID
			}
		}
		unnamed = append(unnamed, sighting{p, to})
	}
	if len(unnamed) > 0 {
		err := s.Update(ctx, func(w *store.Writer) error {
			for _, u := range unnamed {
				if d, ok := gaps(dimms[u.to], dimms[u.p.DIMMUID]); ok && dimms[u.to].UID != "" {
					if err := w.UpsertDIMM(&d); err != nil {
						return err
					}
				}
				if err := w.ReplacePlacement(u.p, u.to, u.p.Slot); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return res, err
		}
		res.unnamed = len(unnamed)
	}
	return res, nil
}
