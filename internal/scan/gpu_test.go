package scan

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/model"
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
