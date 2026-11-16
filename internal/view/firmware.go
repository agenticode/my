package view

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/agenticode/my/internal/fwevent"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// FirmwareRecord is what the BIOS or a BMC said about a DIMM, as the
// views give it in JSON.
type FirmwareRecord struct {
	At        time.Time `json:"time"`
	Event     string    `json:"event"`
	Flags     []string  `json:"flags,omitzero"`
	VendorID  string    `json:"vendor_id,omitzero"`
	Registry  string    `json:"registry,omitzero"`
	Location  string    `json:"location,omitzero"`
	Mapping   string    `json:"mapping"`
	Basis     string    `json:"basis,omitzero"`
	RawRef    string    `json:"raw_ref,omitzero"`
	Withdrawn bool      `json:"withdrawn,omitzero"`
	Duplicate bool      `json:"duplicate,omitzero"`
}

// firmware returns the firmware records of a host, read once per case.
func (c *Case) firmware(ctx context.Context, host string) ([]model.FirmwareRecord, error) {
	if c.fwByHost == nil {
		c.fwByHost = map[string][]model.FirmwareRecord{}
	}
	if list, ok := c.fwByHost[host]; ok {
		return list, nil
	}
	list, err := c.store.FirmwareRecords(ctx, store.FirmwareFilter{Host: host})
	if err != nil {
		return nil, err
	}
	c.fwByHost[host] = list
	return list, nil
}

// FirmwareOf returns the firmware records of a DIMM, oldest first, with
// whether a mark was taken back and whether an action repeats another.
func (c *Case) FirmwareOf(ctx context.Context, host, uid string) ([]FirmwareRecord, error) {
	if uid == "" {
		return nil, nil
	}
	return c.firmwareOf(ctx, host, uid, false)
}

// firmwareOf returns the firmware records of one DIMM, or with all those
// of the whole host.
func (c *Case) firmwareOf(ctx context.Context, host, uid string, all bool) ([]FirmwareRecord, error) {
	list, err := c.firmware(ctx, host)
	if err != nil {
		return nil, err
	}
	af := store.ActionFilter{Host: host, DIMMUID: uid, Duplicates: true}
	if all {
		af.DIMMUID = ""
	}
	acts, err := c.store.Actions(ctx, af)
	if err != nil {
		return nil, err
	}
	withdrawn, dup := map[int64]bool{}, map[int64]bool{}
	for _, a := range acts {
		withdrawn[a.ID], dup[a.ID] = a.WithdrawnBy != 0, a.DupOf != 0
	}
	var out []FirmwareRecord
	for _, f := range list {
		if !all && f.DIMMUID != uid {
			continue
		}
		out = append(out, FirmwareRecord{At: f.At, Event: f.Common, Flags: f.Flags, VendorID: f.VendorID, Registry: f.Registry,
			Location: f.Location, Mapping: f.Mapping, Basis: f.Basis, RawRef: f.RawRef, Withdrawn: withdrawn[f.ActionID],
			Duplicate: dup[f.ActionID]})
	}
	return out, nil
}

// markWord is the short word a firmware mark is shown with in the table
// of the DIMMs, or "" for records that are no mark or step.
func markWord(f FirmwareRecord) string {
	has := func(flag string) bool {
		for _, x := range f.Flags {
			if x == flag {
				return true
			}
		}
		return false
	}
	switch fwevent.Name(f.Event) {
	case fwevent.PPRDone:
		return "PPR ok"
	case fwevent.PPRFailed:
		return "PPR failed"
	case fwevent.PPRScheduled:
		return "PPR pending"
	case fwevent.DeviceSparing:
		switch {
		case has("sddc"):
			return "SDDC"
		case has("adddc"):
			return "ADDDC"
		}
		return "device sparing"
	case fwevent.Sparing, fwevent.SpareCopyStarted:
		return "sparing"
	case fwevent.SparingFailed:
		return "sparing failed"
	case fwevent.PredictedFailure:
		return "PFA"
	case fwevent.Disabled:
		return "disabled"
	case fwevent.MirrorFailover:
		return "mirror"
	case fwevent.TrainingFailure:
		return "training"
	}
	return ""
}

// marksText is the firmware marks of a DIMM in a few words, newest first:
// "PPR ok 11-03, PFA 11-09 +2". It leaves out marks taken back and steps
// that repeat another.
func marksText(list []FirmwareRecord) string {
	var words []string
	for i := len(list) - 1; i >= 0; i-- {
		f := list[i]
		w := markWord(f)
		if w == "" || f.Withdrawn || f.Duplicate {
			continue
		}
		words = append(words, w+" "+f.At.UTC().Format("01-02"))
	}
	if len(words) == 0 {
		return ""
	}
	if len(words) > 2 {
		return strings.Join(words[:2], ", ") + fmt.Sprintf(" +%d", len(words)-2)
	}
	return strings.Join(words, ", ")
}

// bootLetters ranks the firmware records a boot can be shown with: D
// disabled, V mirror failover, N training failure, E repair failed, A
// predicted failure, S sparing, P repair done.
var bootLetters = []struct {
	event  fwevent.Name
	letter string
}{
	{fwevent.Disabled, "D"}, {fwevent.MirrorFailover, "V"}, {fwevent.TrainingFailure, "N"}, {fwevent.PPRFailed, "E"},
	{fwevent.SparingFailed, "E"}, {fwevent.PredictedFailure, "A"}, {fwevent.Sparing, "S"}, {fwevent.DeviceSparing, "S"},
	{fwevent.PPRDone, "P"},
}

// bootLetter returns the letter of the firmware record of a boot that ranks
// highest, or "".
func bootLetter(list []FirmwareRecord) string {
	best, rank := "", len(bootLetters)
	for _, f := range list {
		if f.Withdrawn || f.Duplicate {
			continue
		}
		for i, b := range bootLetters {
			if string(b.event) == f.Event && i < rank {
				best, rank = b.letter, i
			}
		}
	}
	return best
}

// firmwareBoots gives each boot the letter of its firmware records: a
// record belongs to the boot that was running at its time.
func firmwareBoots(cols []BootCol, list []FirmwareRecord) {
	if len(cols) == 0 || len(list) == 0 {
		return
	}
	idx := make([]int, len(cols))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return cols[idx[a]].StartedAt.Before(cols[idx[b]].StartedAt) })
	per := map[int][]FirmwareRecord{}
	for _, f := range list {
		at := -1
		for _, i := range idx {
			if !cols[i].StartedAt.After(f.At) {
				at = i
			}
		}
		if at >= 0 {
			per[at] = append(per[at], f)
		}
	}
	for i, recs := range per {
		cols[i].Firmware = bootLetter(recs)
	}
}

// registryText says what a registry a BMC published covers.
func (c *Case) registryText(r model.RegistrySeen) Text {
	unmapped := r.Memory - r.Mapped - r.Wording
	if unmapped > 0 {
		return c.inv("inv.registry_unmapped", "prefix", r.Prefix, "version", r.Version, "memory", r.Memory, "mapped", r.Mapped,
			"wording", r.Wording, "unmapped", unmapped)
	}
	return c.inv("inv.registry", "prefix", r.Prefix, "version", r.Version, "memory", r.Memory, "mapped", r.Mapped)
}
