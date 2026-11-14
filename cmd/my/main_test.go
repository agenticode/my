package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/store"
)

func run(args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	env := &cli.Env{Stdout: &out, Stderr: &errb, Getenv: func(string) string { return "" }}
	code = app().Run(context.Background(), env, args)
	return code, out.String(), errb.String()
}

func TestVersionCommand(t *testing.T) {
	var out, errb bytes.Buffer
	env := &cli.Env{Stdout: &out, Stderr: &errb, Getenv: func(string) string { return "" }}
	if code := app().Run(context.Background(), env, []string{"version"}); code != cli.ExitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, errb.String())
	}
	if !strings.HasPrefix(out.String(), "my ") {
		t.Errorf("version output = %q, want it to start with %q", out.String(), "my ")
	}
}

func TestSynthIsHiddenAndListsScenarios(t *testing.T) {
	if _, usage, _ := run("help"); strings.Contains(usage, "synth") {
		t.Errorf("the command list shows the hidden synth command:\n%s", usage)
	}
	code, out, _ := run("synth", "-list")
	if code != cli.ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	for _, name := range []string{"demo", "single-bit", "row", "multi-row", "column", "bank", "device", "dimm", "system-channel", "system-slot"} {
		if !strings.Contains(out, name+"\n") {
			t.Errorf("scenario %q is not listed:\n%s", name, out)
		}
	}
}

func TestSynthWritesHostTreeAndLedger(t *testing.T) {
	dir := t.TempDir()
	tree, ledger := filepath.Join(dir, "host"), filepath.Join(dir, "demo.db")
	code, out, errOut := run("synth", "-scenario", "demo", "-seed", "1", "-out", tree, "-flavor", "intel", "-ledger", ledger)
	if code != cli.ExitOK {
		t.Fatalf("exit code = %d, stderr = %s", code, errOut)
	}
	for _, want := range []string{"host r12-n034", "1285 events", "fault 0: row in CPU0_C1", "1231 corrected, 1 uncorrected", "ledger written"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	for _, f := range []string{"var/lib/rasdaemon/ras-mc_event.db", "var/log/dmesg", "sys/firmware/dmi/tables/DMI",
		"sys/devices/system/edac/mc/mc1/dimm0/dimm_ce_count", "proc/sys/kernel/random/boot_id"} {
		if _, err := os.Stat(filepath.Join(tree, filepath.FromSlash(f))); err != nil {
			t.Errorf("host tree lacks %s: %v", f, err)
		}
	}
	s, err := store.Open(context.Background(), ledger, store.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	evs, _ := s.EventList(context.Background(), store.EventFilter{})
	scans, _ := s.Scans(context.Background())
	if len(evs) != 1285 || len(scans) != 1 || scans[0].Kind != "synthetic" || !strings.Contains(scans[0].Note, "seed 1") {
		t.Errorf("%d events, scans = %+v", len(evs), scans)
	}

	// The same seed writes the same kernel log.
	tree2 := filepath.Join(dir, "host2")
	if code, _, errOut := run("synth", "-out", tree2); code != cli.ExitOK {
		t.Fatalf("second run: exit code = %d, stderr = %s", code, errOut)
	}
	a, _ := os.ReadFile(filepath.Join(tree, "var", "log", "dmesg"))
	b, _ := os.ReadFile(filepath.Join(tree2, "var", "log", "dmesg"))
	if len(a) == 0 || !bytes.Equal(a, b) {
		t.Error("two runs with the same seed wrote different kernel logs")
	}
}

func TestSynthRefusesBadInput(t *testing.T) {
	dir := t.TempDir()
	full := filepath.Join(dir, "full")
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(full, "keep.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(dir, "existing.db")
	if err := os.WriteFile(existing, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"no output", []string{"synth"}, cli.ExitUsage, "give -out, -ledger or both"},
		{"unknown scenario", []string{"synth", "-scenario", "nope", "-out", filepath.Join(dir, "x")}, cli.ExitUsage, "unknown scenario"},
		{"unknown flag", []string{"synth", "-frobnicate"}, cli.ExitUsage, "flag provided but not defined"},
		{"stray argument", []string{"synth", "-out", filepath.Join(dir, "x"), "extra"}, cli.ExitUsage, "unexpected argument"},
		{"directory in use", []string{"synth", "-out", full}, cli.ExitUsage, "exists and is not empty"},
		{"ledger exists", []string{"synth", "-ledger", existing}, cli.ExitUsage, "exists"},
		{"unknown flavor", []string{"synth", "-out", filepath.Join(dir, "y"), "-flavor", "sparc"}, cli.ExitFailure, "unknown flavor"},
		{"help", []string{"synth", "-h"}, cli.ExitOK, "Usage: my synth"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _, errOut := run(tt.args...)
			if code != tt.code || !strings.Contains(errOut, tt.want) {
				t.Errorf("exit code = %d, stderr = %q; want %d and %q", code, errOut, tt.code, tt.want)
			}
		})
	}
	// Nothing that was there was touched.
	if b, _ := os.ReadFile(filepath.Join(full, "keep.txt")); string(b) != "mine" {
		t.Error("a file in an existing directory was changed")
	}
	if b, _ := os.ReadFile(existing); string(b) != "mine" {
		t.Error("an existing file was overwritten")
	}
}
