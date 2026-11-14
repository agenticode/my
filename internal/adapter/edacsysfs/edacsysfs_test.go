package edacsysfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/testutil"
)

var (
	ctx = context.Background()
	now = time.Date(2026, 11, 11, 1, 11, 40, 0, time.UTC)
)

func collectTree(t *testing.T, tree string) (*adapter.Collected, adapter.Report) {
	t.Helper()
	var c adapter.Collected
	rep := New(os.DirFS(testutil.Testdata(t, "edac-sysfs", tree)), "mc", "/sys/devices/system/edac/mc", Options{Now: now}).Collect(ctx, &c)
	return &c, rep
}

func TestIntelControllers(t *testing.T) {
	c, rep := collectTree(t, "intel-i10nm")
	if rep.Status != model.SourceOK || rep.Records != 6 || rep.Skipped != 0 || len(c.Counters) != 6 {
		t.Fatalf("report = %+v, %d samples; want ok, 6 records", rep, len(c.Counters))
	}
	since := model.Some(int64(90700))
	want := []model.CounterSample{
		{MC: 0, MCName: "Intel_10nm Socket#0 IMC#0", DIMMIndex: 0, Location: "0:0", Label: "CPU_SrcID#0_MC#0_Chan#0_DIMM#0", RawRef: "/sys/devices/system/edac/mc/mc0/dimm0"},
		{MC: 0, MCName: "Intel_10nm Socket#0 IMC#0", DIMMIndex: 2, Location: "1:0", Label: "CPU_SrcID#0_MC#0_Chan#1_DIMM#0", RawRef: "/sys/devices/system/edac/mc/mc0/dimm2"},
		{MC: 0, MCName: "Intel_10nm Socket#0 IMC#0", DIMMIndex: -1, RawRef: "/sys/devices/system/edac/mc/mc0/noinfo"},
		{MC: 1, MCName: "Intel_10nm Socket#0 IMC#1", DIMMIndex: 0, Location: "0:0", Label: "CPU_SrcID#0_MC#1_Chan#0_DIMM#0", CE: 4, UE: 1, RawRef: "/sys/devices/system/edac/mc/mc1/dimm0"},
		{MC: 1, MCName: "Intel_10nm Socket#0 IMC#1", DIMMIndex: 2, Location: "1:0", Label: "CPU_SrcID#0_MC#1_Chan#1_DIMM#0", CE: 1, RawRef: "/sys/devices/system/edac/mc/mc1/dimm2"},
		{MC: 1, MCName: "Intel_10nm Socket#0 IMC#1", DIMMIndex: -1, CE: 1, RawRef: "/sys/devices/system/edac/mc/mc1/noinfo"},
	}
	for i := range want {
		w := &want[i]
		w.ObservedAt, w.SecondsSinceReset, w.Source = now, since, Kind
		if w.DIMMIndex >= 0 {
			w.SizeMB, w.MemType, w.DevType, w.EdacMode = model.Some(32768), "Registered-DDR4", "x4", "S4ECD4ED"
		}
		if got := *c.Counters[i]; !reflect.DeepEqual(got, *w) {
			t.Errorf("sample %d\n got: %+v\nwant: %+v", i, got, *w)
		}
	}
}

func TestChipSelectController(t *testing.T) {
	c, rep := collectTree(t, "amd64")
	// rankN directories are read; the csrowN directories of the legacy
	// layout repeat the same counts and are not.
	if rep.Status != model.SourceOK || rep.Records != 4 || len(c.Counters) != 4 || len(rep.Notes) != 0 {
		t.Fatalf("report = %+v, %d samples; want ok, 4 records, no notes", rep, len(c.Counters))
	}
	r := c.Counters[2]
	if r.DIMMIndex != 25 || r.Location != "2:1" || r.Label != "mc#0csrow#2channel#1" || r.CE != 2 || r.UE != 0 ||
		r.MemType != "Registered-DDR5" || r.EdacMode != "S8ECD8ED" || r.MCName != "F19h_M10h" || r.SecondsSinceReset != model.Some(int64(270700)) {
		t.Errorf("rank25: %+v", *r)
	}
	if c.Counters[0].DIMMIndex != 0 || c.Counters[1].DIMMIndex != 1 || c.Counters[3].DIMMIndex != -1 {
		t.Errorf("sample order: %d %d %d", c.Counters[0].DIMMIndex, c.Counters[1].DIMMIndex, c.Counters[3].DIMMIndex)
	}
}

func TestNoControllerAndNoDirectory(t *testing.T) {
	c, rep := collectTree(t, "vm")
	if rep.Status != model.SourceEmpty || len(c.Counters) != 0 || !strings.Contains(rep.Detail, "no EDAC driver is loaded") {
		t.Errorf("virtual machine: report = %+v", rep)
	}
	var none adapter.Collected
	rep = New(os.DirFS(t.TempDir()), "mc", "/sys/devices/system/edac/mc", Options{Now: now}).Collect(ctx, &none)
	if rep.Status != model.SourceAbsent || !strings.Contains(rep.Detail, "no EDAC memory controller directory") {
		t.Errorf("missing directory: report = %+v", rep)
	}
}

func TestNewRootReadsBelowTheRoot(t *testing.T) {
	root := t.TempDir()
	dst := filepath.Join(root, "sys", "devices", "system", "edac", "mc")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(dst, os.DirFS(testutil.Testdata(t, "edac-sysfs", "intel-i10nm", "mc"))); err != nil {
		t.Fatal(err)
	}
	var c adapter.Collected
	a := NewRoot(root, Options{})
	before := time.Now().Add(-time.Second)
	rep := a.Collect(ctx, &c)
	if rep.Status != model.SourceOK || len(c.Counters) != 6 || a.Origin() != dst {
		t.Fatalf("report = %+v, %d samples, origin %q", rep, len(c.Counters), a.Origin())
	}
	if c.Counters[0].RawRef != dst+"/mc0/dimm0" || c.Counters[0].ObservedAt.Before(before) {
		t.Errorf("sample = %+v", *c.Counters[0])
	}
}

func file(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s + "\n")} }

func TestDamagedAndPartialTrees(t *testing.T) {
	fsys := fstest.MapFS{
		"mc/mc0/mc_name":             file("test controller"),
		"mc/mc0/seconds_since_reset": file("not-a-number"),
		// A complete DIMM.
		"mc/mc0/dimm0/dimm_ce_count": file("7"),
		"mc/mc0/dimm0/dimm_ue_count": file("0"),
		"mc/mc0/dimm0/dimm_label":    file("DIMM_A1"),
		"mc/mc0/dimm0/dimm_location": file("channel 0 slot 0 "),
		// Counters that are not numbers.
		"mc/mc0/dimm1/dimm_ce_count": file("many"),
		"mc/mc0/dimm1/dimm_ue_count": file("0"),
		// A counter file is missing.
		"mc/mc0/dimm2/dimm_ce_count": file("1"),
		// Only counters, no descriptive files.
		"mc/mc0/dimm3/dimm_ce_count": file("2"),
		"mc/mc0/dimm3/dimm_ue_count": file("3"),
		// Things that are not controllers or DIMMs.
		"mc/mc0/dimm_extra":      file("x"),
		"mc/mcfoo/mc_name":       file("x"),
		"mc/mc7":                 file("a file named like a controller"),
		"mc/mc2/csrow0/ce_count": file("5"),
	}
	var c adapter.Collected
	rep := New(fsys, "mc", "sysfs", Options{Now: now}).Collect(ctx, &c)
	if rep.Status != model.SourcePartial || rep.Records != 4 || rep.Skipped != 2 || len(c.Counters) != 2 {
		t.Fatalf("report = %+v, %d samples; want partial, 4 records, 2 skipped, 2 samples", rep, len(c.Counters))
	}
	notes := strings.Join(rep.Notes, "\n")
	for _, want := range []string{
		`sysfs/mc0/dimm1: error counters are unreadable: dimm_ce_count holds "many", not a number`,
		"sysfs/mc0/dimm2: error counters are unreadable: file is missing",
		"1 memory controllers expose only the csrow layout",
	} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes lack %q:\n%s", want, notes)
		}
	}
	if s := c.Counters[0]; s.CE != 7 || s.Label != "DIMM_A1" || s.Location != "0:0" || s.SecondsSinceReset.Valid || s.SizeMB.Valid {
		t.Errorf("dimm0: %+v", *s)
	}
	if s := c.Counters[1]; s.DIMMIndex != 3 || s.CE != 2 || s.UE != 3 || s.Label != "" || s.Location != "" {
		t.Errorf("dimm3: %+v", *s)
	}
}

type failingSink struct {
	adapter.Collected
	err error
}

func (f *failingSink) Counter(*model.CounterSample) error { return f.err }

func TestSinkErrorAndCancel(t *testing.T) {
	fsys := os.DirFS(testutil.Testdata(t, "edac-sysfs", "intel-i10nm"))
	boom := errors.New("ledger is full")
	rep := New(fsys, "mc", "sysfs", Options{}).Collect(ctx, &failingSink{err: boom})
	if rep.Status != model.SourceError || !errors.Is(rep.SinkErr, boom) {
		t.Errorf("report = %+v", rep)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	var c adapter.Collected
	rep = New(fsys, "mc", "sysfs", Options{}).Collect(cctx, &c)
	if rep.Status != model.SourceError || len(c.Counters) != 0 {
		t.Errorf("cancelled: report = %+v", rep)
	}
}
