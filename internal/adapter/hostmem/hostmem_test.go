package hostmem

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
)

var when = time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)

func write(t *testing.T, root, rel, text string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func collect(t *testing.T, a *Adapter) (map[string]*model.HostReading, adapter.Report) {
	t.Helper()
	var c adapter.Collected
	rep := a.Collect(context.Background(), &c)
	got := map[string]*model.HostReading{}
	for _, r := range c.Readings {
		got[r.Kind] = r
	}
	return got, rep
}

func cpuTree(t *testing.T, root string) {
	for cpu, pkg := range map[int]string{0: "0", 1: "0", 2: "1", 3: "1"} {
		d := filepath.Join("sys/devices/system/cpu", "cpu"+string(rune('0'+cpu)))
		write(t, root, d+"/topology/physical_package_id", pkg+"\n")
		for i, c := range []struct{ level, typ, size string }{{"1", "Data", "48K"}, {"1", "Instruction", "32K"}, {"2", "Unified", "2048K"}, {"3", "Unified", "107520K"}} {
			x := d + "/cache/index" + string(rune('0'+i))
			write(t, root, x+"/level", c.level+"\n")
			write(t, root, x+"/type", c.typ+"\n")
			write(t, root, x+"/size", c.size+"\n")
		}
	}
}

// A copy of a host's files with the plain names of a collection, and one
// with the names of an sos report: every reading is made from its file.
func TestCopyReadings(t *testing.T) {
	for _, names := range []map[string]string{
		{"numactl": "numactl-hardware.txt", "numastat": "numastat.txt", "lshw": "lshw.txt"},
		{"numactl": "sos_commands/numa/numactl_--hardware", "numastat": "sos_commands/numa/numastat", "lshw": "sos_commands/hardware/lshw"},
	} {
		root := t.TempDir()
		write(t, root, "proc/meminfo", "MemTotal:  1024 kB\n")
		write(t, root, "proc/vmstat", "pgfault 5\n")
		write(t, root, "proc/pressure/memory", "some avg10=0.00 avg60=0.00 avg300=0.00 total=0\n")
		var slab strings.Builder
		slab.WriteString("slabinfo - version: 2.1\n")
		for i := 0; i < 30; i++ {
			slab.WriteString("cache" + strings.Repeat("x", i) + " 1 " + string(rune('1'+i%9)) + " 8 1 1\n")
		}
		write(t, root, "proc/slabinfo", slab.String())
		write(t, root, names["numactl"], "node 0 size: 10 MB\n")
		write(t, root, names["numastat"], "  node0\nnuma_hit 1\n")
		write(t, root, names["lshw"], "*-memory\n")
		write(t, root, "sar-B.txt", "Linux 6.12 (h)\n")
		write(t, root, "sys/kernel/mm/lru_gen/enabled", "0x0007\n")
		cpuTree(t, root)
		if !Holds(root) {
			t.Fatal("Holds is false for a tree with readings")
		}
		got, rep := collect(t, New(root, Options{Now: when}))
		if rep.Status != model.SourceOK || len(rep.Notes) != 0 {
			t.Errorf("report %s %v", rep.Status, rep.Notes)
		}
		for _, k := range []string{model.ReadingMeminfo, model.ReadingVmstat, model.ReadingPressure, model.ReadingSlabinfo, model.ReadingNumactl,
			model.ReadingNumastat, model.ReadingSarB, model.ReadingLruGenEnabled, model.ReadingLshw, model.ReadingCPUCache} {
			if got[k] == nil || got[k].Text == "" || !got[k].TakenAt.Equal(when) {
				t.Errorf("%s: %+v", k, got[k])
			}
		}
		if n := strings.Count(got[model.ReadingSlabinfo].Text, "\n"); n != 21 {
			t.Errorf("slabinfo: %d lines, want the head line and 20", n)
		}
		want := "SOCKET:0 LEVEL:1 TYPE:Data SIZE:48K\nSOCKET:0 LEVEL:1 TYPE:Instruction SIZE:32K\nSOCKET:0 LEVEL:2 TYPE:Unified SIZE:2048K\nSOCKET:0 LEVEL:3 TYPE:Unified SIZE:107520K\n" +
			"SOCKET:1 LEVEL:1 TYPE:Data SIZE:48K\nSOCKET:1 LEVEL:1 TYPE:Instruction SIZE:32K\nSOCKET:1 LEVEL:2 TYPE:Unified SIZE:2048K\nSOCKET:1 LEVEL:3 TYPE:Unified SIZE:107520K\n"
		if got[model.ReadingCPUCache].Text != want {
			t.Errorf("cpu-cache:\n%s", got[model.ReadingCPUCache].Text)
		}
	}
}

// Missing, denied, over the bound, not UTF-8: each loses only its own
// reading, or is cut and says so.
func TestReadingsThatFail(t *testing.T) {
	root := t.TempDir()
	write(t, root, "proc/meminfo", "MemTotal: \xff\xfe 1 kB\n")
	write(t, root, "proc/pressure/memory", strings.Repeat("some avg10=0.00\n", 400))
	write(t, root, "proc/vmstat", "pgfault 5\n")
	if os.Geteuid() != 0 {
		if err := os.Chmod(filepath.Join(root, "proc/vmstat"), 0); err != nil {
			t.Fatal(err)
		}
	}
	got, rep := collect(t, New(root, Options{Now: when}))
	if got[model.ReadingMeminfo] == nil || !strings.Contains(got[model.ReadingMeminfo].Text, "�") {
		t.Errorf("meminfo: %+v", got[model.ReadingMeminfo])
	}
	if p := got[model.ReadingPressure]; p == nil || !p.Cut || len(p.Text) > 4<<10 || !strings.HasSuffix(p.Text, "\n") {
		t.Errorf("pressure: %+v", p)
	}
	notes := strings.Join(rep.Notes, "\n")
	if os.Geteuid() != 0 && (got[model.ReadingVmstat] != nil || !strings.Contains(notes, "vmstat: not readable")) {
		t.Errorf("vmstat: %+v, notes %s", got[model.ReadingVmstat], notes)
	}
	if !strings.Contains(notes, "numastat: not there") || !strings.Contains(notes, "cpu-cache: not there") {
		t.Errorf("notes %s", notes)
	}
	empty := t.TempDir()
	if Holds(empty) {
		t.Error("Holds is true for an empty tree")
	}
	if _, rep := collect(t, New(empty, Options{})); rep.Status != model.SourceAbsent {
		t.Errorf("empty tree: %s", rep.Status)
	}
}

func stand(t *testing.T, dir, name, script string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// On a live host the programs run: one that prints, one that fails, one
// that hangs past its limit and one that prints garbage. numastat runs
// with NUMASTAT_WIDTH set, so that one block holds every node.
func TestPrograms(t *testing.T) {
	bin := t.TempDir()
	old := []time.Duration{NumactlTimeout, NumastatTimeout, SarTimeout}
	NumactlTimeout, NumastatTimeout, SarTimeout = 3*time.Second, 3*time.Second, 1500*time.Millisecond
	defer func() { NumactlTimeout, NumastatTimeout, SarTimeout = old[0], old[1], old[2] }()
	root := t.TempDir()
	write(t, root, "proc/meminfo", "MemTotal: 1 kB\n")
	write(t, root, "lshw.txt", "*-memory\n")
	opts := Options{Live: true, Now: when,
		Numactl:  stand(t, bin, "numactl", "printf 'node 0 size: 10 MB\\n\\377\\376garbage\\n'\n"),
		Numastat: stand(t, bin, "numastat", "echo \"width=$NUMASTAT_WIDTH\"\n"),
		Sar:      stand(t, bin, "sar", "sleep 5\n"),
	}
	got, rep := collect(t, New(root, opts))
	if r := got[model.ReadingNumactl]; r == nil || !strings.HasPrefix(r.Text, "node 0 size: 10 MB\n") || !strings.Contains(r.Text, "�") || r.Origin != "numactl --hardware" {
		t.Errorf("numactl: %+v", r)
	}
	if r := got[model.ReadingNumastat]; r == nil || r.Text != "width=4096\n" {
		t.Errorf("numastat: %+v", r)
	}
	notes := strings.Join(rep.Notes, "\n")
	if got[model.ReadingSarB] != nil || !strings.Contains(notes, "sar -B did not answer within") {
		t.Errorf("sar: %+v, notes %s", got[model.ReadingSarB], notes)
	}
	if got[model.ReadingLshw] != nil {
		t.Error("a live host read lshw text")
	}
	opts.Sar = stand(t, bin, "sar2", "echo nope >&2; exit 3\n")
	opts.Numactl, opts.Numastat = "-", filepath.Join(bin, "missing")
	got, rep = collect(t, New(root, opts))
	notes = strings.Join(rep.Notes, "\n")
	if got[model.ReadingSarB] != nil || !strings.Contains(notes, "sar -B failed") || !strings.Contains(notes, "numactl-hardware: not run") ||
		!strings.Contains(notes, "numastat is not installed") || got[model.ReadingNumastat] != nil {
		t.Errorf("readings %v, notes %s", len(got), notes)
	}
}
