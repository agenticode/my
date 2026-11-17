package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter/dimmspd"
	"github.com/agenticode/my/internal/adapter/dimmspd/spdtest"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/scan"
	"github.com/agenticode/my/internal/testutil"
)

// An import of SPD dumps alone ends as an import of dmidecode text alone:
// the bundle is written, the host has no error record and the finding ok,
// and the exit code is 0.
func TestImportOfDumpsAlone(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct {
		name string
		in   []string
	}{
		{"dmidecode", []string{testutil.Testdata(t, "real", "7.3.0-mce", "dmidecode.txt")}},
		{"spd", []string{testutil.Testdata(t, "spd", "illumos", "ddr4-rdimm-64g-2rx4.bin"), testutil.Testdata(t, "spd", "illumos", "decode-dimms-4.4.txt")}},
	} {
		args := append([]string{"import"}, c.in...)
		args = append(args, "-o", filepath.Join(dir, c.name+".mycase"), "--lang", "en")
		code, out, errOut := run(args...)
		if code != 0 || errOut != "" {
			t.Errorf("%s: exit %d, %s", c.name, code, errOut)
		}
		for _, want := range []string{"0 error records and 0 actions added", "case bundle written:", "finding: ok"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: no %q in\n%s", c.name, want, out)
			}
		}
	}
}

// A collection whose every SPD read is denied is whole: exit 0, the steps
// listed with their state, the probe written.
func TestCollectHostSPDFailed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a file of mode 000")
	}
	root, _, _ := hostRoot(t)
	collectHostPrograms["sar"] = "-"
	// The capture is of a virtual machine; the lab switch lets the probe run.
	t.Setenv("MY_DEBUG", "spd-vm")
	d4 := testutil.ReadTestdata(t, "spd", "illumos", "ddr4-rdimm-64g-2rx4.bin")
	tr := spdtest.New().I2C().Adapter("0", "SMBus I801 adapter at 0000:00:1f.4").Client("0-0050", "ee1004", "ee1004", d4).Client("0-0051", "ee1004", "ee1004", d4)
	if err := tr.Write(root); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"0-0050", "0-0051"} {
		p := filepath.Join(root, "sys", "bus", "i2c", "devices", c, "eeprom")
		if err := os.Chmod(p, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(p, 0o644) })
	}
	out := filepath.Join(t.TempDir(), "c")
	code, _, stderr := runEnv(nil, "collect", "host", "-o", out, "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var cj scan.CollectResult
	b, err := os.ReadFile(filepath.Join(out, "collect.json"))
	if err != nil || json.Unmarshal(b, &cj) != nil {
		t.Fatal(err)
	}
	denied := 0
	for _, e := range cj.Entries {
		if e.Group == scan.GroupSPD && e.State == scan.StepDenied {
			denied++
		}
	}
	if denied != 2 || cj.Partial() {
		t.Errorf("%d SPD steps denied, partial %v: %+v", denied, cj.Partial(), cj.Entries)
	}
	probe, err := os.ReadFile(filepath.Join(out, dimmspd.ProbeFile))
	if err != nil || dimmspd.Reason(dimmspd.ParseProbe(string(probe)), "DDR4") != dimmspd.ReasonReadDenied {
		t.Errorf("probe %q, %v", probe, err)
	}
	// --no-spd: no probe and no SPD step.
	out2 := filepath.Join(t.TempDir(), "c2")
	if code, _, stderr := runEnv(nil, "collect", "host", "-o", out2, "--no-spd"); code != 0 {
		t.Fatalf("--no-spd: exit %d: %s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(out2, dimmspd.ProbeFile)); err == nil {
		t.Error("--no-spd wrote spd-probe.txt")
	}
}

// my serve reads the SPD in its first scan; the later scans keep what it
// read and read nothing again.
func TestServeReadsSPDOnce(t *testing.T) {
	root, _, _ := hostRoot(t)
	d4 := testutil.ReadTestdata(t, "spd", "illumos", "ddr4-rdimm-64g-2rx4.bin")
	if err := os.WriteFile(filepath.Join(root, dimmspd.ProbeFile), []byte("spd-probe 1\ni2c present\nclient 0-0050 name=ee1004 driver=ee1004 eeprom=512 read=512 state=ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, dimmspd.Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, dimmspd.Dir, "0-0050.bin")
	if err := os.WriteFile(bin, d4, 0o644); err != nil {
		t.Fatal(err)
	}
	h := &hostSource{lang: i18n.EN, cfg: engine.DefaultConfig(), root: root, now: time.Date(2026, 11, 17, 5, 0, 0, 0, time.UTC)}
	spdOf := func() (int, string) {
		st, err := h.Scan(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		hs := st.(*hostState)
		rs, err := hs.ledger.SPDReadings(context.Background(), hs.h.Host)
		if err != nil {
			t.Fatal(err)
		}
		state := ""
		for _, r := range rs {
			state += r.State
		}
		return len(rs), state
	}
	if n, state := spdOf(); n != 1 || state != "ok" {
		t.Fatalf("first scan: %d readings %q", n, state)
	}
	// The file goes away: a second scan that read it would have nothing.
	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}
	if n, state := spdOf(); n != 1 || state != "ok" {
		t.Errorf("second scan: %d readings %q", n, state)
	}
	// --no-spd: none.
	h2 := &hostSource{lang: i18n.EN, cfg: engine.DefaultConfig(), root: root, now: h.now, noSPD: true}
	st, err := h2.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if rs, _ := st.(*hostState).ledger.SPDReadings(context.Background(), st.(*hostState).h.Host); len(rs) != 0 {
		t.Errorf("--no-spd: %d readings", len(rs))
	}
}
