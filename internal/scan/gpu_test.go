package scan

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/synth"
	"github.com/agenticode/my/internal/testutil"
)

// A live host with the NVIDIA driver is asked for its GPU memory state.
func TestGPUMemoryOfALiveHost(t *testing.T) {
	root, ds := tree(t, synth.FlavorIntel)
	xml := testutil.Testdata(t, "nvidia", "nvidia-smi-q.xml")
	prog := filepath.Join(t.TempDir(), "nvidia-smi")
	write(t, prog, "#!/bin/sh\ncat \""+xml+"\"\n", 0o755)
	s := ledger(t)
	res, err := Run(ctx, s, Options{Root: root, Live: true, Now: ds.Scenario.End, TempDir: t.TempDir(), Journalctl: "-", NvidiaSMI: prog})
	if err != nil {
		t.Fatal(err)
	}
	g := source(t, res.Sources, "nvidia-smi")
	if g.Status != model.SourceOK || g.Samples != 2 || g.Records != 2 || g.Origin != prog+" -q -x" {
		t.Errorf("GPU source = %+v", g)
	}
	if res.Samples != 2 {
		t.Errorf("run added %d samples", res.Samples)
	}
	gpus, err := s.GPUMem(ctx, res.Host)
	if err != nil || len(gpus) != 2 || gpus[1].GPUSerial != "1650000000977" || !gpus[1].RemapFailure.V {
		t.Errorf("GPU records = %+v, %v", gpus, err)
	}

	// The program is there and the driver is not answering: the inventory says so.
	down := filepath.Join(t.TempDir(), "nvidia-smi")
	write(t, down, "#!/bin/sh\necho \"NVIDIA-SMI has failed because it couldn't communicate with the NVIDIA driver.\"\nexit 9\n", 0o755)
	res, err = Run(ctx, ledger(t), Options{Root: root, Live: true, Now: ds.Scenario.End, TempDir: t.TempDir(), Journalctl: "-", NvidiaSMI: down})
	if err != nil {
		t.Fatal(err)
	}
	if g := source(t, res.Sources, "nvidia-smi"); g.Status != model.SourceError || !strings.Contains(g.Detail, "NVIDIA-SMI has failed") {
		t.Errorf("GPU source with the driver down = %+v", g)
	}
}

// Most hosts have no GPU: a host without the program gets no entry, and
// "-" leaves the source out.
func TestGPUMemoryIsLeftOutWithoutTheProgram(t *testing.T) {
	root, ds := tree(t, synth.FlavorIntel)
	t.Setenv("PATH", t.TempDir())
	for name, prog := range map[string]string{"not installed": "", "left out": "-"} {
		res, err := Run(ctx, ledger(t), Options{Root: root, Live: true, Now: ds.Scenario.End, TempDir: t.TempDir(), Journalctl: "-", NvidiaSMI: prog})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(inventory(res.Sources), "nvidia-smi") {
			t.Errorf("%s: inventory = %s", name, inventory(res.Sources))
		}
	}
}

// A copy of a host's files is read where the saved output is found. No
// program is run.
func TestGPUMemoryInAHostTree(t *testing.T) {
	root, ds := tree(t, synth.FlavorIntel)
	res, err := Run(ctx, ledger(t), Options{Root: root, Now: ds.Scenario.End, TempDir: t.TempDir(), NvidiaSMI: "/nonexistent/nvidia-smi"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(inventory(res.Sources), "nvidia-smi") {
		t.Errorf("tree without saved output: inventory = %s", inventory(res.Sources))
	}
	saved := filepath.Join(root, "sos_commands", "nvidia", "nvidia-smi_-q")
	write(t, saved, string(testutil.ReadTestdata(t, "nvidia", "nvidia-smi-q.txt")), 0o644)
	res, err = Run(ctx, ledger(t), Options{Root: root, Now: ds.Scenario.End, TempDir: t.TempDir(), NvidiaSMI: "/nonexistent/nvidia-smi"})
	if err != nil {
		t.Fatal(err)
	}
	if g := source(t, res.Sources, "nvidia-smi"); g.Status != model.SourceOK || g.Samples != 2 || g.Origin != saved {
		t.Errorf("GPU source = %+v", g)
	}
}

// A copy of a host's files with a saved listing of the BMC event log: the
// listing is read, the one with sensor names when there are several. A
// tree without one gets no entry, and a live host is not asked.
func TestBMCLogInAHostTree(t *testing.T) {
	root, ds := tree(t, synth.FlavorIntel)
	opts := Options{Root: root, Now: ds.Scenario.End, TempDir: t.TempDir()}
	res, err := Run(ctx, ledger(t), opts)
	if err != nil {
		t.Fatal(err)
	}
	if hasKind(res.Sources, "sel") != 0 {
		t.Errorf("tree without a listing: inventory = %s", inventory(res.Sources))
	}
	before := res.Events

	dir := filepath.Join(root, "sos_commands", "ipmitool")
	write(t, filepath.Join(dir, "ipmitool_sel_list_-v"), string(testutil.ReadTestdata(t, "sel", "ipmitool-list-v.txt")), 0o644)
	write(t, filepath.Join(dir, "ipmitool_sel_elist"), string(testutil.ReadTestdata(t, "sel", "ipmitool-elist.txt")), 0o644)
	write(t, filepath.Join(dir, "ipmitool_sel_info"), "SEL Information\nVersion          : 1.5 (v1.5, v2 compliant)\n", 0o644)
	s := ledger(t)
	res, err = Run(ctx, s, opts)
	if err != nil {
		t.Fatal(err)
	}
	log := source(t, res.Sources, "sel")
	if log.Origin != filepath.Join(dir, "ipmitool_sel_elist") || log.Events == 0 || log.Actions == 0 {
		t.Errorf("BMC log source = %+v", log)
	}
	if n := hasKind(res.Sources, "sel"); n != 1 {
		t.Errorf("%d entries for the BMC log, want the one listing: %s", n, inventory(res.Sources))
	}
	if res.Events != before+log.Events {
		t.Errorf("run added %d events, want the %d of the host and the %d of the BMC log", res.Events, before, log.Events)
	}
	acts, err := s.Actions(ctx, store.ActionFilter{Host: res.Host})
	if err != nil {
		t.Fatal(err)
	}
	disabled := false
	for _, a := range acts {
		disabled = disabled || (a.Kind == model.ActionDisable && a.Source == "sel/list" && a.Slot != "")
	}
	if !disabled {
		t.Errorf("no disable action of the BMC log is tied to a slot: %+v", acts)
	}

	// With the options of the command in the file name, and the verbose
	// listing alone.
	other, _ := tree(t, synth.FlavorIntel)
	saved := filepath.Join(other, "sos_commands", "ipmitool", "ipmitool_-I_usb_sel_list_-v")
	write(t, saved, string(testutil.ReadTestdata(t, "sel", "ipmitool-list-v.txt")), 0o644)
	res, err = Run(ctx, ledger(t), Options{Root: other, Now: ds.Scenario.End, TempDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if log := source(t, res.Sources, "sel"); log.Origin != saved || log.Events == 0 {
		t.Errorf("verbose listing = %+v", log)
	}

	// What ipmitool said when it could not reach the BMC is no listing:
	// the inventory says that the log was not read.
	failed, _ := tree(t, synth.FlavorIntel)
	write(t, filepath.Join(failed, "sos_commands", "ipmitool", "ipmitool_sel_elist"),
		"Could not open device at /dev/ipmi0 or /dev/ipmi/0 or /dev/ipmidev/0: No such file or directory\n", 0o644)
	res, err = Run(ctx, ledger(t), Options{Root: failed, Now: ds.Scenario.End, TempDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if log := source(t, res.Sources, "sel"); log.Status == model.SourceOK || log.Events != 0 || log.Detail == "" {
		t.Errorf("failed listing = %+v", log)
	}

	// A live host is not asked for its event log.
	res, err = Run(ctx, ledger(t), Options{Root: root, Live: true, Now: ds.Scenario.End, TempDir: t.TempDir(), Journalctl: "-", NvidiaSMI: "-"})
	if err != nil {
		t.Fatal(err)
	}
	if hasKind(res.Sources, "sel") != 0 {
		t.Errorf("live host: inventory = %s", inventory(res.Sources))
	}
}

// hasKind counts the entries of a source inventory of one kind.
func hasKind(srcs []model.Source, kind string) (n int) {
	for _, s := range srcs {
		if s.Kind == kind {
			n++
		}
	}
	return n
}
