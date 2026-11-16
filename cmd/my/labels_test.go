package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/testutil"
	"github.com/agenticode/my/internal/view"
)

// parseLabels reads a label file the way ras-mc-ctl does (rasdaemon,
// util/ras-mc-ctl): comments start a line with #, "Vendor:" opens a
// vendor, "Model:" or "Product:" a list of machines, and every other line
// holds labels with their positions, split by ";". It returns the labels
// by vendor, kind of block, machine and position, and fails on what
// ras-mc-ctl refuses: a block without a vendor, positions of two depths.
func parseLabels(t *testing.T, text string) map[string]string {
	t.Helper()
	out := map[string]string{}
	position := regexp.MustCompile(`^\d+(?:[.:]\d+)+$`)
	vendor, kind := "", ""
	var machines []string
	layers := 0
	for n, raw := range strings.Split(text, "\n") {
		if strings.HasPrefix(raw, "#") {
			continue
		}
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			// The Perl version tests for # before it trims the line.
			t.Errorf("line %d: a comment that does not start the line: %q", n+1, raw)
			continue
		}
		low := strings.ToLower(line)
		switch {
		case strings.HasPrefix(low, "vendor:"):
			vendor, machines, layers = strings.ToLower(strings.TrimSpace(line[len("vendor:"):])), nil, 0
			continue
		case strings.HasPrefix(low, "model:"), strings.HasPrefix(low, "product:"):
			if vendor == "" {
				t.Fatalf("line %d: a block without a vendor", n+1)
			}
			k, rest, _ := strings.Cut(line, ":")
			kind, machines, layers = strings.ToLower(k), nil, 0
			for _, m := range regexp.MustCompile(`[,;]+`).Split(rest, -1) {
				if m = strings.ToLower(strings.TrimSpace(m)); m != "" {
					machines = append(machines, m)
				}
			}
			continue
		}
		for _, item := range strings.Split(line, ";") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			// The Python version splits at the first colon.
			label, pos, ok := strings.Cut(item, ":")
			label, pos = strings.TrimSpace(label), strings.TrimSpace(pos)
			if !ok || !position.MatchString(pos) {
				t.Errorf("line %d: no label with a position: %q", n+1, item)
				continue
			}
			if len(label) > 31 {
				t.Errorf("line %d: label %q is longer than the EDAC driver takes", n+1, label)
			}
			depth := strings.Count(strings.ReplaceAll(pos, ":", "."), ".")
			if layers == 0 {
				layers = depth
			} else if layers != depth {
				t.Fatalf("line %d: inconsistent number of layers", n+1)
			}
			for _, m := range machines {
				out[vendor+"|"+kind+"|"+m+"|"+pos] = label
			}
		}
	}
	return out
}

// The label file of the demo host: every EDAC position gets the name of
// its slot, and ras-mc-ctl can read the file.
func TestExportLabels(t *testing.T) {
	root := hostTree(t)
	code, stdout, stderr := runEnv(fixed, "export", "labels", "--root", root)
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("exit code %d, stderr %q\n%s", code, stderr, stdout)
	}
	testutil.Golden(t, "labels-host.txt", []byte(stdout))
	labels := parseLabels(t, stdout)
	// 16 DIMMs under the product name: the copy of the host does not say
	// what its mainboard is.
	if len(labels) != 16 {
		t.Errorf("%d labels, want 16:\n%s", len(labels), stdout)
	}
	// The position of a DIMM is its memory controller and its location in
	// the EDAC driver, as the inventory shows them.
	code, inv, _ := runEnv(fixed, "inventory", "--root", root, "--json")
	if code != cli.ExitOK {
		t.Fatalf("inventory: exit code %d", code)
	}
	var e struct{ Data view.Inventory }
	if err := json.Unmarshal([]byte(inv), &e); err != nil {
		t.Fatal(err)
	}
	edac := regexp.MustCompile(`^CPU_SrcID#(\d+)_MC#(\d+)_Chan#(\d+)_DIMM#(\d+)$`)
	for _, r := range e.Data.Rows {
		m := edac.FindStringSubmatch(r.EDACName)
		if m == nil {
			t.Fatalf("%s: EDAC name %q", r.Slot, r.EDACName)
		}
		found := false
		for key, label := range labels {
			if label == r.Slot && strings.HasPrefix(key, "example systems|product|r760|") {
				found = true
			}
		}
		if !found {
			t.Errorf("no label %s under the product of the host", r.Slot)
		}
	}

	// Into a file: what was written, and how to install it.
	file := filepath.Join(t.TempDir(), "labels")
	code, stdout, stderr = runEnv(fixed, "export", "labels", "--root", root, "-o", file, "--vendor", "Example Board Co.", "--model", "X13-B")
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("-o: exit code %d, stderr %q", code, stderr)
	}
	wantLines(t, stdout,
		"label file written: "+file+" (16 labels for Example Board Co. X13-B)",
		"  install on the host: sudo cp "+file+" /etc/ras/dimm_labels.d/ && sudo ras-mc-ctl --register-labels")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	// Under the model of the mainboard and under the product name.
	named := parseLabels(t, string(data))
	if named["example board co.|model|x13-b|0.0.0"] != "CPU0_A1" || named["example board co.|product|r760|7.1.0"] != "CPU1_H1" || len(named) != 32 {
		t.Errorf("labels under the names of the command line: %v", named)
	}
	// The file is not replaced without --force.
	if code, _, stderr = runEnv(fixed, "export", "labels", "--root", root, "-o", file); code != cli.ExitUsage || !strings.Contains(stderr, "exists") {
		t.Errorf("existing file: exit code %d, stderr %q", code, stderr)
	}

	// As JSON: the same labels as data.
	code, stdout, _ = runEnv(fixed, "export", "labels", "--root", root, "--json")
	var f view.LabelFile
	if err := json.Unmarshal([]byte(stdout), &f); err != nil || code != cli.ExitOK {
		t.Fatalf("--json: exit code %d, %v\n%s", code, err, stdout)
	}
	if len(f.Labels) != 16 || f.Layers != 2 || f.Vendor != "Example Systems" || f.Product != "R760" || f.Names != view.LabelSlot || len(f.Skipped) != 0 {
		t.Errorf("label file: %+v", f)
	}
}

// With the BMC of the host on record the labels can be the service labels
// of the BMC, and the file names the other names of each slot.
func TestExportLabelsWithBMCNames(t *testing.T) {
	root := hostTree(t)
	alone, _ := overview(t, fixed, "--root", root)
	srv := bmcOf(t, alone)
	code, stdout, stderr := runEnv(account(), "export", "labels", "--root", root, "--bmc", srv.URL, "--insecure", "--names", "bmc", "--lang", "ko")
	if code != cli.ExitOK {
		t.Fatalf("exit code %d\n%s\n%s", code, stdout, stderr)
	}
	testutil.Golden(t, "labels-host-bmc.ko.txt", []byte(stdout))
	labels := parseLabels(t, stdout)
	got := map[string]bool{}
	for _, label := range labels {
		got[label] = true
	}
	// The stand-in BMC calls its modules DIMM A1 to DIMM A16.
	for i := 1; i <= 16; i++ {
		if !got[fmt.Sprintf("DIMM A%d", i)] {
			t.Errorf("no label DIMM A%d:\n%s", i, stdout)
		}
	}
	if !strings.Contains(stdout, "#   DIMM A1: CPU0_C1, BMC.Slot.1, DIMM1") {
		t.Errorf("the other names of the first slot are not in the file:\n%s", stdout)
	}
	noSecret(t, "", stdout, stderr)
}

func TestExportLabelsErrors(t *testing.T) {
	root := hostTree(t)
	// A case from a BMC alone has no EDAC position.
	file := bmcCase(t, "dell-shaped")
	code, stdout, stderr := runEnv(fixed, "open", file, "export", "labels")
	if code != cli.ExitFailure || stdout != "" || !strings.Contains(stderr, "host node-d: no EDAC position is tied to a slot that SMBIOS or a BMC names") {
		t.Errorf("BMC alone: exit code %d\n%s\n%s", code, stdout, stderr)
	}
	for _, tt := range []struct {
		name string
		args []string
		code int
		want string
	}{
		{"unknown names", []string{"export", "labels", "--root", root, "--names", "edac"}, cli.ExitUsage, "--names edac: want slot"},
		{"a name with a comma", []string{"export", "labels", "--root", root, "--model", "X13, rev B"}, cli.ExitFailure, "a label file reads a comma and a semicolon as the end of a name"},
		{"an argument too many", []string{"export", "labels", "extra", "--root", root}, cli.ExitUsage, `unexpected argument "extra"`},
		{"an unknown format", []string{"export", "csv", "--root", root}, cli.ExitUsage, "this version writes fmfm (an FMFM analysis log) and labels (a DIMM label file for rasdaemon)"},
		{"no format", []string{"export", "--root", root}, cli.ExitUsage, "my export fmfm, or my export labels"},
	} {
		code, stdout, stderr := runEnv(fixed, tt.args...)
		if code != tt.code || stdout != "" || !strings.Contains(stderr, tt.want) {
			t.Errorf("%s: exit code %d, stderr %q", tt.name, code, stderr)
		}
	}
}
