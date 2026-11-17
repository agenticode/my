package scan

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/agenticode/my/internal/adapter/hostmem"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/synth"
	"github.com/agenticode/my/internal/testutil"
)

func mcelogCounts(t *testing.T, opts Options) []string {
	t.Helper()
	res, err := Run(ctx, ledger(t), opts)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, src := range res.Sources {
		if src.Kind == "mcelog" {
			out = append(out, fmt.Sprintf("%s records %d samples %d events %d", src.Status, src.Records, src.Samples, src.Events))
		}
	}
	return out
}

// A copy of a host's files with the answer of mcelog --client gives the
// counts the client gives on the live host.
func TestMcelogClientFileInACopy(t *testing.T) {
	answer := testutil.ReadTestdata(t, "mcelog", "client-x3850.txt")
	root, ds := tree(t, synth.FlavorIntel)
	if err := os.WriteFile(filepath.Join(root, McelogClientFile), answer, 0o644); err != nil {
		t.Fatal(err)
	}
	fromCopy := mcelogCounts(t, Options{Root: root})
	bin := t.TempDir()
	prog := filepath.Join(bin, "mcelog")
	if err := os.WriteFile(prog, append([]byte("#!/bin/sh\ncat <<'EOF'\n"), append(answer, []byte("EOF\n")...)...), 0o755); err != nil {
		t.Fatal(err)
	}
	live := mcelogCounts(t, Options{Root: root, Live: true, Now: ds.Scenario.End, Journalctl: "-", Mcelog: prog, Numactl: "-", Numastat: "-", Sar: "-"})
	if len(fromCopy) == 0 || len(fromCopy) != len(live) {
		t.Fatalf("copy %v, live %v", fromCopy, live)
	}
	for i := range live {
		if fromCopy[i] != live[i] {
			t.Errorf("copy %s, live %s", fromCopy[i], live[i])
		}
	}
}

// A scan with the programs of the explorer switched off runs none of them,
// though they are there; a copy of a host's files runs none at all.
func TestProgramsSwitchedOff(t *testing.T) {
	bin := t.TempDir()
	mark := filepath.Join(t.TempDir(), "ran")
	var progs [3]string
	for i, n := range []string{"numactl", "numastat", "sar"} {
		progs[i] = filepath.Join(bin, n)
		if err := os.WriteFile(progs[i], []byte("#!/bin/sh\necho "+n+" >> "+mark+"\necho node 0 size: 10 MB\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	root, ds := tree(t, synth.FlavorIntel)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	s := ledger(t)
	if _, err := Run(ctx, s, Options{Root: root, Live: true, Now: ds.Scenario.End, Journalctl: "-", Mcelog: "-", Numactl: "-", Numastat: "-", Sar: "-"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, ledger(t), Options{Root: root}); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(mark); err == nil {
		t.Errorf("programs ran: %s", b)
	}
	// With them on, the stand-ins answer and their readings are kept.
	if _, err := Run(ctx, s, Options{Root: root, Live: true, Now: ds.Scenario.End.Add(1), Journalctl: "-", Mcelog: "-", Numactl: progs[0], Numastat: progs[1], Sar: progs[2]}); err != nil {
		t.Fatal(err)
	}
	hosts, _ := s.Hosts(ctx)
	rs, err := s.Readings(ctx, hosts[0].Name)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, r := range rs {
		kinds[r.Kind] = true
	}
	if !kinds[model.ReadingNumactl] || !kinds[model.ReadingNumastat] || !kinds[model.ReadingSarB] {
		t.Errorf("readings %v", kinds)
	}
	if !hostmem.Holds(root) {
		t.Log("the synthetic tree holds no reading file")
	}
}
