package spdhost

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/agenticode/my/internal/testutil"
)

// TestWriteDemo writes the demo case to the directory MY_SPD_DEMO names;
// it is skipped without it.
func TestWriteDemo(t *testing.T) {
	dir := os.Getenv("MY_SPD_DEMO")
	if dir == "" {
		t.Skip("MY_SPD_DEMO is not set")
	}
	if err := WriteDemo(dir, testutil.ReadTestdata(t, "spd", "illumos", "ddr5-rdimm-64g-2rx4.bin")); err != nil {
		t.Fatal(err)
	}
}

// TestDemoIsCurrent: the generator writes the SPD files, the probe and the
// SMBIOS tables of testdata/spd/demo as they are.
func TestDemoIsCurrent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo")
	if err := WriteDemo(dir, testutil.ReadTestdata(t, "spd", "illumos", "ddr5-rdimm-64g-2rx4.bin")); err != nil {
		t.Fatal(err)
	}
	have := testutil.Testdata(t, "spd", "demo")
	files, _ := filepath.Glob(filepath.Join(dir, "spd", "*.bin"))
	if len(files) != 8 {
		t.Fatalf("%d SPD files", len(files))
	}
	for _, f := range append(files, filepath.Join(dir, "spd-probe.txt"), filepath.Join(dir, "sys", "firmware", "dmi", "tables", "DMI")) {
		rel, _ := filepath.Rel(dir, f)
		a, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(have, rel))
		if err != nil || !bytes.Equal(a, b) {
			t.Errorf("%s differs from what the generator writes (%v); run TestWriteDemo", rel, err)
		}
	}
}
