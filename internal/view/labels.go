package view

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/model"
)

// rasdaemon gives the DIMM objects of the EDAC driver the names printed
// on the board when it has a label file for the machine: "ras-mc-ctl
// --register-labels" reads /etc/ras/dimm_labels.d and writes the labels
// into the driver. The ledger knows which EDAC position is which slot, so
// it can write that file. From then on the kernel log and rasdaemon name a
// DIMM the way SMBIOS and the BMC do.
//
// The form of the file, as ras-mc-ctl reads it (rasdaemon, util/ras-mc-ctl):
//
//	Vendor: <mainboard vendor>
//	  Model: <mainboard model>[, <model>...]
//	    <label>: <mc>.<top>[.<mid>[.<low>]]; <label>: ...;
//	  Product: <system product name>[, <name>...]
//	    <label>: ...
//
// A line that starts with # is a comment. A position is the index of the
// memory controller and the numbers of the DIMM's location in the EDAC
// driver (dimm_location). Every label of a block has the same number of
// layers. The kernel takes a label of at most 31 bytes (EDAC_MC_LABEL_LEN).

// MaxLabelLen is the longest label the EDAC driver takes, in bytes.
const MaxLabelLen = 31

// Which name of a slot a label file gives the DIMM.
const (
	// LabelSlot is the name the ledger uses: the SMBIOS locator when
	// SMBIOS was read.
	LabelSlot = "slot"
	// LabelBMC is the service label of the BMC, where the BMC has one for
	// the slot, and the name of the ledger otherwise.
	LabelBMC = "bmc"
)

// LabelFile is the EDAC positions of a host with the names of their slots.
type LabelFile struct {
	Host string `json:"host"`
	// Vendor and Model are the mainboard vendor and model the file is
	// written for, Product the system product name. Model is empty when
	// the ledger cannot tell it from the vendor.
	Vendor  string `json:"vendor"`
	Model   string `json:"model,omitzero"`
	Product string `json:"product,omitzero"`
	// Board is the mainboard as the ledger holds it: vendor and model in
	// one string.
	Board string `json:"board,omitzero"`
	// VendorGuessed is set when Vendor is the system vendor because the
	// ledger does not hold the mainboard vendor on its own.
	VendorGuessed bool `json:"vendor_guessed,omitzero"`
	// Names says which names were written: LabelSlot or LabelBMC.
	Names string `json:"names"`
	// Layers is the number of layers of a position after the controller.
	Layers int     `json:"layers"`
	Labels []Label `json:"labels"`
	// Skipped lists the EDAC positions that got no label, with the reason.
	Skipped []SkippedLabel `json:"skipped,omitzero"`
}

// Label is one line of a label file.
type Label struct {
	Label string `json:"label"`
	// Position is the controller and the layers: "0.1.0".
	Position string `json:"position"`
	Slot     string `json:"slot"`
	// Also lists the other names sources have for the slot.
	Also []string `json:"also,omitzero"`
}

// SkippedLabel is an EDAC position without a label.
type SkippedLabel struct {
	Position string `json:"position"`
	Slot     string `json:"slot"`
	Reason   Text   `json:"reason"`
}

// LabelOptions say how a label file is written.
type LabelOptions struct {
	// Names is LabelSlot (the default) or LabelBMC.
	Names string
	// Vendor, Model and Product replace what the ledger says: the words
	// "ras-mc-ctl --mainboard" prints for the machine.
	Vendor, Model, Product string
}

// ErrNoLabels: the ledger ties no EDAC position to a named slot.
var ErrNoLabels = errors.New("no EDAC position is tied to a slot name")

// ErrNoBoard: the ledger does not say which machine the labels are for.
var ErrNoBoard = errors.New("the vendor of the machine is not on record")

// ErrBoardName: a name of the machine cannot be written into a label file.
var ErrBoardName = errors.New("the name cannot stand in a label file")

// labelProblem says why a name cannot be a label, or returns "".
func labelProblem(name string) string {
	switch {
	case name == "":
		return "label.empty"
	case len(name) > MaxLabelLen:
		return "label.long"
	case strings.ContainsAny(name, ":;\n\r") || strings.HasPrefix(name, "#"):
		return "label.chars"
	}
	return ""
}

// LabelFile builds the label file of a host.
func (c *Case) LabelFile(ctx context.Context, h *engine.HostResult, opts LabelOptions) (*LabelFile, error) {
	names := opts.Names
	if names == "" {
		names = LabelSlot
	}
	if names != LabelSlot && names != LabelBMC {
		return nil, fmt.Errorf("unknown names %q", names)
	}
	f := &LabelFile{Host: h.Host, Names: names, Labels: []Label{}}
	hosts, err := c.store.Hosts(ctx)
	if err != nil {
		return nil, err
	}
	for _, x := range hosts {
		if x.Name != h.Host {
			continue
		}
		f.Board, f.Product = x.Board, x.SystemProduct
		// The ledger holds the mainboard as "vendor model". Where it
		// starts with the system vendor, the two can be told apart.
		switch {
		case x.SystemVendor != "" && strings.HasPrefix(x.Board, x.SystemVendor+" "):
			f.Vendor, f.Model = x.SystemVendor, strings.TrimSpace(strings.TrimPrefix(x.Board, x.SystemVendor))
		case x.SystemVendor != "":
			f.Vendor, f.VendorGuessed = x.SystemVendor, x.Board != ""
		}
	}
	if opts.Vendor != "" {
		f.Vendor, f.VendorGuessed = opts.Vendor, false
	}
	if opts.Model != "" {
		f.Model = opts.Model
	}
	if opts.Product != "" {
		f.Product = opts.Product
	}
	if f.Vendor == "" || (f.Model == "" && f.Product == "") {
		return nil, ErrNoBoard
	}
	// A label file reads a comma and a semicolon in these names as the
	// end of one name and the start of the next.
	for _, name := range []string{f.Vendor, f.Model, f.Product} {
		if strings.ContainsAny(name, ",;\n\r") || strings.HasPrefix(name, "#") {
			return nil, fmt.Errorf("%w: %q", ErrBoardName, name)
		}
	}

	slots, err := c.store.Slots(ctx, h.Host)
	if err != nil {
		return nil, err
	}
	readings, err := c.store.BMCMemory(ctx, h.Host)
	if err != nil {
		return nil, err
	}
	bmc := map[string]*model.BMCMemory{}
	for i := range readings {
		m := &readings[i]
		if cur := bmc[m.Slot]; m.Slot != "" && (cur == nil || !m.ObservedAt.Before(cur.ObservedAt)) {
			bmc[m.Slot] = m
		}
	}
	type entry struct {
		Label
		layers int
	}
	var entries []entry
	byLayers := map[int]int{}
	taken := map[string]string{}
	skip := func(pos, slot, code string, kv ...any) {
		f.Skipped = append(f.Skipped, SkippedLabel{Position: pos, Slot: slot, Reason: c.inv(code, kv...)})
	}
	for _, sl := range slots {
		if !sl.EdacMC.Valid || sl.EdacLocation == "" {
			continue
		}
		pos := fmt.Sprintf("%d.%s", sl.EdacMC.V, strings.ReplaceAll(sl.EdacLocation, ":", "."))
		if sl.Locator == "" && !sl.SMBIOSHandle.Valid {
			// Known through EDAC alone: the name is the label the driver
			// has already.
			skip(pos, sl.Name, "label.no_name")
			continue
		}
		label := sl.Name
		var also []string
		if m := bmc[sl.Name]; m != nil {
			seen := map[string]bool{model.NormalizeLabel(sl.Name): true}
			for _, n := range []string{m.ServiceLabel, m.DeviceLocator, m.ResourceID} {
				if k := model.NormalizeLabel(n); k != "" && !seen[k] {
					seen[k] = true
					also = append(also, n)
				}
			}
			if names == LabelBMC && m.ServiceLabel != "" && labelProblem(m.ServiceLabel) == "" {
				label = m.ServiceLabel
				also = also[:0]
				seen = map[string]bool{model.NormalizeLabel(label): true}
				for _, n := range []string{sl.Name, m.DeviceLocator, m.ResourceID} {
					if k := model.NormalizeLabel(n); k != "" && !seen[k] {
						seen[k] = true
						also = append(also, n)
					}
				}
			}
		}
		if code := labelProblem(label); code != "" {
			skip(pos, sl.Name, code, "name", label, "max", MaxLabelLen)
			continue
		}
		if other, dup := taken[pos]; dup {
			skip(pos, sl.Name, "label.twice", "other", other)
			continue
		}
		taken[pos] = sl.Name
		layers := strings.Count(pos, ".")
		byLayers[layers]++
		entries = append(entries, entry{Label{Label: label, Position: pos, Slot: sl.Name, Also: also}, layers})
	}
	// A block holds positions of one depth: the depth most positions have.
	for layers, n := range byLayers {
		if n > byLayers[f.Layers] || (n == byLayers[f.Layers] && layers > f.Layers) {
			f.Layers = layers
		}
	}
	for _, e := range entries {
		if e.layers != f.Layers {
			skip(e.Position, e.Slot, "label.layers", "layers", f.Layers)
			continue
		}
		f.Labels = append(f.Labels, e.Label)
	}
	if len(f.Labels) == 0 {
		return f, ErrNoLabels
	}
	sort.SliceStable(f.Labels, func(i, j int) bool { return positionLess(f.Labels[i].Position, f.Labels[j].Position) })
	return f, nil
}

// positionLess orders positions by their numbers.
func positionLess(a, b string) bool {
	x, y := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(x) && i < len(y); i++ {
		if x[i] != y[i] {
			if len(x[i]) != len(y[i]) {
				return len(x[i]) < len(y[i])
			}
			return x[i] < y[i]
		}
	}
	return len(x) < len(y)
}

// Text writes the label file. tool names the program that wrote it.
func (f *LabelFile) Text(c *Case, tool string) []byte {
	var b strings.Builder
	comment := func(t Text) {
		for _, line := range strings.Split(t.Text, "\n") {
			b.WriteString("# " + line + "\n")
		}
	}
	comment(c.inv("label.file.head", "tool", tool, "host", f.Host))
	comment(c.inv("label.file.install"))
	comment(c.inv("label.file.check"))
	if f.Names == LabelBMC {
		comment(c.inv("label.file.names_bmc"))
	} else {
		comment(c.inv("label.file.names_slot"))
	}
	if f.VendorGuessed {
		comment(c.inv("label.file.vendor_guessed", "board", f.Board))
	}
	b.WriteString("#\n")
	// Other names of a slot, for the reader of the file.
	any := false
	for _, l := range f.Labels {
		if len(l.Also) > 0 {
			if !any {
				comment(c.inv("label.file.also"))
				any = true
			}
			b.WriteString("#   " + l.Label + ": " + strings.Join(l.Also, ", ") + "\n")
		}
	}
	for _, s := range f.Skipped {
		b.WriteString("# " + c.inv("label.file.skipped", "position", s.Position, "slot", s.Slot, "reason", s.Reason.Text).Text + "\n")
	}
	if any || len(f.Skipped) > 0 {
		b.WriteString("#\n")
	}
	b.WriteString("Vendor: " + f.Vendor + "\n")
	block := func(key, name string) {
		if name == "" {
			return
		}
		b.WriteString("  " + key + ": " + name + "\n")
		for _, l := range f.Labels {
			b.WriteString("    " + l.Label + ": " + l.Position + ";\n")
		}
	}
	block("Model", f.Model)
	block("Product", f.Product)
	return []byte(b.String())
}
