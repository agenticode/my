package view

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// labelCase is a ledger with one host, its slots and what a BMC says of them.
func labelCase(t *testing.T, host model.Host, slots []model.Slot, readings ...model.BMCMemory) (*Case, *engine.HostResult) {
	t.Helper()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	host.Name, host.FirstSeen, host.LastSeen = "node-a", sideBoot, sideRead
	err = s.Update(ctx, func(w *store.Writer) error {
		if err := w.UpsertHost(&host); err != nil {
			return err
		}
		for i := range slots {
			slots[i].Host = "node-a"
			if err := w.UpsertSlot(&slots[i]); err != nil {
				return err
			}
		}
		for i := range readings {
			m := readings[i]
			m.Host, m.ObservedAt, m.Label, m.Source, m.Fingerprint = "node-a", sideRead, m.Slot, "redfish/memory", "reading "+m.Slot
			if _, err := w.InsertBMCMemory(&m); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	c, err := Open(ctx, s, i18n.EN, engine.Options{Now: sideRead})
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Host("")
	if err != nil {
		t.Fatal(err)
	}
	return c, h
}

// slot is a slot SMBIOS names with its EDAC position.
func slot(name string, mc int, location string) model.Slot {
	return model.Slot{Name: name, Locator: name, SMBIOSHandle: model.Some(0x1100 + mc*16 + len(name)), EdacMC: model.Some(mc), EdacLocation: location,
		Populated: model.Some(true)}
}

func TestLabelFile(t *testing.T) {
	c, h := labelCase(t, model.Host{SystemVendor: "Example Systems", SystemProduct: "R760", Board: "Example Systems X13-B"},
		[]model.Slot{slot("DIMM_B1", 1, "0:0"), slot("DIMM_A2", 0, "1:0"), slot("DIMM_A1", 0, "0:0"), slot("DIMM_A10", 0, "10:0")},
		model.BMCMemory{Slot: "DIMM_A1", ServiceLabel: "DIMM A1", ResourceID: "DIMM.Socket.A1"})
	f, err := c.LabelFile(ctx, h, LabelOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if f.Vendor != "Example Systems" || f.Model != "X13-B" || f.Product != "R760" || f.VendorGuessed || f.Layers != 2 || len(f.Skipped) != 0 {
		t.Errorf("file = %+v", f)
	}
	// By position: the controller, then the layers as numbers.
	var got []string
	for _, l := range f.Labels {
		got = append(got, l.Label+"="+l.Position)
	}
	if strings.Join(got, " ") != "DIMM_A1=0.0.0 DIMM_A2=0.1.0 DIMM_A10=0.10.0 DIMM_B1=1.0.0" {
		t.Errorf("labels = %v", got)
	}
	text := string(f.Text(c, "my test"))
	for _, want := range []string{
		"# DIMM labels for rasdaemon, written by my test for host node-a.\n",
		"# Other names of the slots:\n#   DIMM_A1: DIMM A1, DIMM.Socket.A1\n#\n",
		"Vendor: Example Systems\n  Model: X13-B\n    DIMM_A1: 0.0.0;\n    DIMM_A2: 0.1.0;\n    DIMM_A10: 0.10.0;\n    DIMM_B1: 1.0.0;\n  Product: R760\n    DIMM_A1: 0.0.0;\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the file lacks %q:\n%s", want, text)
		}
	}
	for _, line := range strings.Split(text, "\n") {
		// ras-mc-ctl takes a line for a comment when # is its first character.
		if strings.Contains(line, "#") && !strings.HasPrefix(line, "#") {
			t.Errorf("a # that does not start the line: %q", line)
		}
	}

	// The labels of the BMC, where it has one.
	f, err = c.LabelFile(ctx, h, LabelOptions{Names: LabelBMC})
	if err != nil {
		t.Fatal(err)
	}
	if f.Labels[0].Label != "DIMM A1" || strings.Join(f.Labels[0].Also, ", ") != "DIMM_A1, DIMM.Socket.A1" || f.Labels[1].Label != "DIMM_A2" {
		t.Errorf("labels = %+v", f.Labels)
	}
	if _, err := c.LabelFile(ctx, h, LabelOptions{Names: "edac"}); err == nil {
		t.Error("unknown names are taken")
	}
}

// Positions that get no label are listed with the reason, in the data and
// as comments of the file.
func TestLabelFileSkips(t *testing.T) {
	long := strings.Repeat("L", MaxLabelLen+1)
	slots := []model.Slot{
		slot("DIMM_A1", 0, "0:0"),
		slot(strings.Repeat("M", MaxLabelLen), 0, "0:1"), // as long as a label may be
		slot(long, 0, "1:0"),
		slot("CPU1:DIMM_A1", 0, "1:1"),
		slot("DIMM;A3", 0, "2:0"),
		slot("#A4", 0, "2:1"),
		slot("DIMM\nA6", 0, "3:0"),
		slot("A7 on mainboard", 0, "3:1"),
		slot("DIMM_A5", 0, "0:0"), // the position of DIMM_A1
		slot("DIMM_C1", 2, "0:0:1"),
		{Name: "mc3 0:0", EdacMC: model.Some(3), EdacLocation: "0:0", Populated: model.Some(true)}, // known through EDAC alone
		slot("DIMM_Z9", 0, ""), // no location
		{Name: "DIMM_Z8", Locator: "DIMM_Z8", Populated: model.Some(false)},
	}
	c, h := labelCase(t, model.Host{SystemVendor: "Example Systems", SystemProduct: "R760", Board: "Other Maker X13-B"}, slots,
		model.BMCMemory{Slot: "DIMM_A1", ServiceLabel: long})
	f, err := c.LabelFile(ctx, h, LabelOptions{Names: LabelBMC})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Labels) != 2 || f.Labels[0].Label != "DIMM_A1" || len(f.Labels[1].Label) != MaxLabelLen || f.Layers != 2 {
		t.Errorf("labels = %+v", f.Labels)
	}
	reasons := map[string]string{}
	for _, s := range f.Skipped {
		reasons[s.Slot] = s.Reason.Code
	}
	want := map[string]string{long: "label.long", "CPU1:DIMM_A1": "label.chars", "DIMM;A3": "label.chars", "#A4": "label.chars", "DIMM\nA6": "label.chars",
		"A7 on mainboard": "label.word", "DIMM_A5": "label.twice",
		"DIMM_C1": "label.layers", "mc3 0:0": "label.no_name"}
	for name, code := range want {
		if reasons[name] != code {
			t.Errorf("%s: reason %q, want %q", name, reasons[name], code)
		}
	}
	if len(f.Skipped) != len(want) {
		t.Errorf("skipped = %+v", f.Skipped)
	}
	// The mainboard does not begin with the system vendor: the file says
	// that its Vendor line is the system vendor.
	text := string(f.Text(c, "my test"))
	if !f.VendorGuessed || f.Model != "" || !strings.Contains(text, `# The ledger holds the mainboard as one string, "Other Maker X13-B"`) ||
		!strings.Contains(text, "# no label for 0.1.1 (CPU1:DIMM_A1): the name \"CPU1:DIMM_A1\" holds a colon") || strings.Contains(text, "  Model:") {
		t.Errorf("file:\n%s", text)
	}
	// A line end in a name of a source stays inside its comment line.
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "A6") {
			t.Errorf("a name of a source starts a line of the file: %q", line)
		}
	}
	// The names of the command line replace those of the ledger.
	f, err = c.LabelFile(ctx, h, LabelOptions{Vendor: "Other Maker", Model: "X13-B"})
	if err != nil || f.VendorGuessed || f.Vendor != "Other Maker" || f.Model != "X13-B" || f.Product != "R760" {
		t.Errorf("file = %+v, %v", f, err)
	}
}

func TestLabelFileErrors(t *testing.T) {
	// No EDAC position with a name.
	c, h := labelCase(t, model.Host{SystemVendor: "Example Systems", SystemProduct: "R760"},
		[]model.Slot{{Name: "DIMM_A1", Locator: "DIMM_A1", Populated: model.Some(true)}})
	if _, err := c.LabelFile(ctx, h, LabelOptions{}); !errors.Is(err, ErrNoLabels) {
		t.Errorf("no positions: err = %v", err)
	}
	// A machine nothing names.
	c, h = labelCase(t, model.Host{}, []model.Slot{slot("DIMM_A1", 0, "0:0")})
	if _, err := c.LabelFile(ctx, h, LabelOptions{}); !errors.Is(err, ErrNoBoard) {
		t.Errorf("no machine: err = %v", err)
	}
	if f, err := c.LabelFile(ctx, h, LabelOptions{Vendor: "Example Systems", Product: "R760"}); err != nil || len(f.Labels) != 1 {
		t.Errorf("machine from the options: %+v, %v", f, err)
	}
	// The vendor line takes the rest of the line: a comma is part of it.
	if f, err := c.LabelFile(ctx, h, LabelOptions{Vendor: "Example, Inc.", Model: "X13-B"}); err != nil || f.Vendor != "Example, Inc." {
		t.Errorf("vendor with a comma: %+v, %v", f, err)
	}
	// A name a label file would read as two.
	for _, o := range []LabelOptions{{Vendor: "Example\nInc.", Model: "X"}, {Vendor: "Example", Model: "X13;B"}, {Vendor: "Example", Product: "R7, R8"}} {
		if _, err := c.LabelFile(ctx, h, o); !errors.Is(err, ErrBoardName) {
			t.Errorf("%+v: err = %v", o, err)
		}
	}
}
