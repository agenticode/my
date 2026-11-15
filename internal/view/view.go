// Package view builds what the screens and the report show from a ledger
// and the result of the analysis engine.
//
// Each figure answers one question and has one data structure here:
//
//	Overview    which DIMM: the slot map and the DIMMs ranked by risk
//	Card        what to do: the finding with its reasons
//	Topology    which branch: shares of the errors by rank, device, bank, row
//	AddressMap  which fault mode: errors by row and column within one bank
//	Timeline    rising or quiet: errors per day with boots, repairs and
//	            uncorrected errors
//
// The structures hold numbers, codes and sentences in the language of the
// reader. They do not know how they are drawn: the terminal renderer and
// the report draw the same data, and --json prints it as it is.
package view

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// Case is a ledger together with what the engine found in it. Every view
// is built from a Case.
type Case struct {
	store *store.Store
	// Result is the result of the engine for the ledger.
	Result *engine.Result
	// Lang is the language sentences are written in.
	Lang i18n.Lang
}

// Open runs the engine over the ledger and returns the Case the views are
// built from.
func Open(ctx context.Context, s *store.Store, lang i18n.Lang, opts engine.Options) (*Case, error) {
	res, err := engine.Run(ctx, s, opts)
	if err != nil {
		return nil, err
	}
	if lang == "" {
		lang = i18n.EN
	}
	return &Case{store: s, Result: res, Lang: lang}, nil
}

// Store returns the ledger of the case.
func (c *Case) Store() *store.Store { return c.store }

// Text is a sentence ready to be shown, with the code and the arguments it
// was made from. A program reads the code; a person reads the text.
type Text struct {
	Code string         `json:"code"`
	Args map[string]any `json:"args,omitzero"`
	// Text is the sentence in the language of the view.
	Text string `json:"text"`
}

// statement renders a statement of the engine.
func (c *Case) statement(st model.Statement) Text {
	return Text{Code: st.Code, Args: st.Args, Text: engine.Text(c.Lang, st)}
}

// say renders a message of this package: code, then pairs of argument name
// and value.
func (c *Case) say(code string, kv ...any) Text {
	t := Text{Code: code}
	if len(kv) > 0 {
		t.Args = make(map[string]any, len(kv)/2)
		for i := 0; i+1 < len(kv); i += 2 {
			t.Args[kv[i].(string)] = kv[i+1]
		}
	}
	t.Text = Messages.Text(c.Lang, code, t.Args)
	return t
}

// Errors of host and DIMM selection.
var (
	// ErrNoHost: the ledger holds no host.
	ErrNoHost = errors.New("the ledger holds no host")
	// ErrSeveralHosts: the ledger holds more than one host and none was named.
	ErrSeveralHosts = errors.New("the ledger holds several hosts")
	// ErrNoSuchHost: the named host is not in the ledger.
	ErrNoSuchHost = errors.New("no such host in the ledger")
	// ErrNoSuchDIMM: nothing in the host matches the slot or serial number.
	ErrNoSuchDIMM = errors.New("no such slot or DIMM")
	// ErrAmbiguousDIMM: more than one DIMM matches.
	ErrAmbiguousDIMM = errors.New("more than one DIMM matches")
	// ErrEmptySlot: the named slot holds no DIMM.
	ErrEmptySlot = errors.New("the slot holds no DIMM")
)

// HostNames returns the names of the hosts of the case.
func (c *Case) HostNames() []string {
	out := make([]string, len(c.Result.Hosts))
	for i, h := range c.Result.Hosts {
		out[i] = h.Host
	}
	return out
}

// Host returns the result of the named host. An empty name selects the
// host of a ledger that holds exactly one.
func (c *Case) Host(name string) (*engine.HostResult, error) {
	switch {
	case len(c.Result.Hosts) == 0:
		return nil, ErrNoHost
	case name == "" && len(c.Result.Hosts) == 1:
		return c.Result.Hosts[0], nil
	case name == "":
		return nil, fmt.Errorf("%w: %s", ErrSeveralHosts, strings.Join(c.HostNames(), ", "))
	}
	if h := c.Result.Host(name); h != nil {
		return h, nil
	}
	for _, h := range c.Result.Hosts {
		if strings.EqualFold(h.Host, name) {
			return h, nil
		}
	}
	return nil, fmt.Errorf("%w: %s (hosts: %s)", ErrNoSuchHost, name, strings.Join(c.HostNames(), ", "))
}

// minSerialSuffix is the shortest end of a serial number that selects a
// DIMM. The ranking prints the last six characters.
const minSerialSuffix = 4

// Find returns the DIMM a reader names: by the slot it is in, by its serial
// number or the end of it, by an alias of the slot (the EDAC label), or by
// its ledger identity. Slot names win over serial numbers, and DIMMs that
// are in the host win over DIMMs that were replaced.
func (c *Case) Find(ctx context.Context, h *engine.HostResult, ref string) (*engine.DIMMResult, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, ErrNoSuchDIMM
	}
	norm := model.NormalizeLabel(ref)

	// The slot name.
	for _, d := range h.DIMMs {
		if d.Present && model.NormalizeLabel(d.Slot) == norm {
			return d, nil
		}
	}
	slots, err := c.store.Slots(ctx, h.Host)
	if err != nil {
		return nil, err
	}
	for _, sl := range slots {
		if model.NormalizeLabel(sl.Name) == norm {
			// A slot whose module was taken out still has the history of
			// the last DIMM seen in it.
			if d := lastInSlot(h, sl.Name); d != nil {
				return d, nil
			}
			return nil, fmt.Errorf("%w: %s", ErrEmptySlot, sl.Name)
		}
	}

	// The serial number, the ledger identity, the end of the serial number.
	serial := strings.TrimLeft(norm, ".")
	var exact, suffix []*engine.DIMMResult
	for _, d := range h.DIMMs {
		if strings.EqualFold(d.UID, ref) {
			return d, nil
		}
		if d.DIMM == nil || d.DIMM.Serial == "" {
			continue
		}
		s := model.NormalizeLabel(d.DIMM.Serial)
		switch {
		case s == serial:
			exact = append(exact, d)
		case len(serial) >= minSerialSuffix && strings.HasSuffix(s, serial):
			suffix = append(suffix, d)
		}
	}
	for _, found := range [][]*engine.DIMMResult{exact, suffix} {
		if d, err := pick(found, ref); d != nil || err != nil {
			return d, err
		}
	}

	// A name another source uses for the slot.
	aliases, err := c.store.SlotAliases(ctx, h.Host)
	if err != nil {
		return nil, err
	}
	if slot, ok := aliases["label:"+norm]; ok {
		if d := h.InSlot(slot); d != nil {
			return d, nil
		}
		if d := lastInSlot(h, slot); d != nil {
			return d, nil
		}
		return nil, fmt.Errorf("%w: %s", ErrEmptySlot, slot)
	}
	return nil, fmt.Errorf("%w: %s", ErrNoSuchDIMM, ref)
}

// pick returns the one DIMM among the matches, preferring DIMMs that are in
// the host.
func pick(found []*engine.DIMMResult, ref string) (*engine.DIMMResult, error) {
	if len(found) == 0 {
		return nil, nil
	}
	var present []*engine.DIMMResult
	for _, d := range found {
		if d.Present {
			present = append(present, d)
		}
	}
	if len(present) == 1 {
		return present[0], nil
	}
	if len(found) == 1 {
		return found[0], nil
	}
	names := make([]string, len(found))
	for i, d := range found {
		names[i] = d.Slot
		if d.DIMM != nil && d.DIMM.Serial != "" {
			names[i] += " (S/N " + d.DIMM.Serial + ")"
		}
	}
	sort.Strings(names)
	return nil, fmt.Errorf("%w: %s matches %s", ErrAmbiguousDIMM, ref, strings.Join(names, ", "))
}

// lastInSlot returns the DIMM that is in the slot, or the one that was
// there last when the slot is empty now.
func lastInSlot(h *engine.HostResult, slot string) *engine.DIMMResult {
	if d := h.InSlot(slot); d != nil {
		return d
	}
	var last *engine.DIMMResult
	for _, d := range h.DIMMs {
		if d.Slot != slot {
			continue
		}
		if last == nil || d.Features.Last.After(last.Features.Last) {
			last = d
		}
	}
	return last
}

// Listed reports whether a DIMM belongs in a list of findings: it has a
// finding other than ok, or a check has something to say about it.
func Listed(d *engine.DIMMResult) bool {
	return d.Verdict.Grade != model.VerdictOK || len(d.Verdict.CounterEvidence) > 0 || d.Features.CE+d.Features.UE > 0
}

// ShortSerial returns the end of a serial number as the ranking prints it:
// the last six characters after two dots, or the whole number when it is
// short.
func ShortSerial(serial string) string {
	if len(serial) <= 8 {
		return serial
	}
	return ".." + serial[len(serial)-6:]
}

// DIMMInfo describes a module in the words of its label.
type DIMMInfo struct {
	UID          string `json:"dimm_uid"`
	Serial       string `json:"serial,omitzero"`
	Manufacturer string `json:"manufacturer,omitzero"`
	PartNumber   string `json:"part_number,omitzero"`
	// Kind is the type of the module in short: "DDR5 RDIMM 64 GB 2Rx4".
	Kind string `json:"kind,omitzero"`
	// Identity is "serial" when the module is known by its serial number,
	// and "slot" when the ledger only knows the slot.
	Identity string `json:"identity,omitzero"`
}

func dimmInfo(uid string, d *model.DIMM) DIMMInfo {
	info := DIMMInfo{UID: uid}
	if d == nil {
		return info
	}
	info.Serial, info.Manufacturer, info.PartNumber, info.Identity = d.Serial, d.Manufacturer, d.PartNumber, string(d.Identity)
	var parts []string
	for _, p := range []string{d.MemoryType, d.ModuleType} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if d.SizeBytes > 0 {
		parts = append(parts, sizeText(d.SizeBytes))
	}
	if org := d.Organization(); org != "" {
		parts = append(parts, org)
	}
	info.Kind = strings.Join(parts, " ")
	return info
}

// sizeText writes a capacity in binary units: "64 GB" for 64 GiB, as
// module labels do.
func sizeText(bytes int64) string {
	const gib = 1 << 30
	switch {
	case bytes >= gib && bytes%gib == 0:
		return fmt.Sprintf("%d GB", bytes/gib)
	case bytes >= gib:
		return fmt.Sprintf("%.1f GB", float64(bytes)/gib)
	}
	return fmt.Sprintf("%d MB", bytes>>20)
}
