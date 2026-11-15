//go:build unix

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/synth"
)

// The fleet ledger holds the hosts and about the events that were asked
// for, and a second ledger made with the same options holds the same.
func TestFleetLedger(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	o := synth.FleetOptions{Seed: 5, Hosts: 12, Events: 3000}
	m, err := fleetLedger(ctx, filepath.Join(dir, "a.db"), o)
	if err != nil {
		t.Fatal(err)
	}
	if m.Hosts != 12 || m.Events < 2850 || m.Events > 3300 || m.Largest == "" || m.Slot == "" || m.Bytes == 0 || m.LargestEvents > m.Events {
		t.Fatalf("made %+v", m)
	}
	again, err := fleetLedger(ctx, filepath.Join(dir, "b.db"), o)
	if err != nil || again.Events != m.Events || again.Actions != m.Actions || again.Largest != m.Largest || again.Slot != m.Slot {
		t.Errorf("the same options made %+v, then %+v (%v)", m, again, err)
	}
	if _, err := fleetLedger(ctx, filepath.Join(dir, "a.db"), o); err == nil {
		t.Error("a ledger that exists was written over")
	}

	s, err := store.Open(ctx, filepath.Join(dir, "a.db"), store.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	hosts, err := s.Hosts(ctx)
	if err != nil || len(hosts) != 12 {
		t.Fatalf("%d hosts in the ledger, %v", len(hosts), err)
	}
	totals, err := s.Totals(ctx, store.EventFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var errs int64
	for _, x := range totals {
		errs += x.CE + x.UE
	}
	if int(errs) != m.Events {
		t.Errorf("the ledger counts %d errors, the generator %d events", errs, m.Events)
	}
}

func TestHostTree(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "host")
	m, err := hostTree(dir, synth.FleetOptions{Seed: 5, Hosts: 40, Events: 2000}, synth.FlavorIntel)
	if err != nil {
		t.Fatal(err)
	}
	if m.Hosts != 1 || m.Events < 2000 || m.Events > 2100 || m.Bytes == 0 {
		t.Fatalf("made %+v", m)
	}
	for _, name := range []string{"var/lib/rasdaemon/ras-mc_event.db", "var/log/dmesg", "proc/stat", "sys/firmware/dmi/tables/DMI"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name))); err != nil {
			t.Errorf("the copy lacks %s: %v", name, err)
		}
	}
	if _, err := hostTree(dir, synth.FleetOptions{Seed: 5, Hosts: 1, Events: 10}, synth.FlavorIntel); err == nil {
		t.Error("a directory that is not empty was written into")
	}
}

// A measurement takes the time and the peak memory of a child process and
// tells a finding (an exit code below 64) from a failure.
func TestMeasure(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	out := filepath.Join(t.TempDir(), "out")
	m := Measurement{Command: []string{sh, "-c", "echo 12345 > " + out + "; exit 2"}}
	measure(&m, out)
	if m.Exit != 2 || m.Error != "" || m.PeakRSS < 100<<10 || m.Seconds <= 0 || m.Output != 6 {
		t.Errorf("a command that ends with a finding: %+v", m)
	}
	m = Measurement{Command: []string{sh, "-c", "echo broken >&2; exit 70"}}
	measure(&m, "")
	if m.Exit != 70 || !strings.Contains(m.Error, "broken") {
		t.Errorf("a command that fails: %+v", m)
	}
	m = Measurement{Command: []string{filepath.Join(t.TempDir(), "none")}}
	measure(&m, "")
	if m.Error == "" {
		t.Errorf("a program that is not there: %+v", m)
	}
}

func TestTable(t *testing.T) {
	var b strings.Builder
	table(&b, []Measurement{
		{Name: "scan", Input: "host copy", Events: 100007, Seconds: 20.66, PeakRSS: 104 << 20},
		{Name: "serve", Input: "host copy", Events: 100007, Seconds: 21, PeakRSS: 105 << 20, Ready: 19.7, Scrape: 0.004},
		{Name: "scan", Input: "host copy", Events: 1000012, Seconds: 231.2, PeakRSS: 300 << 20},
		{Name: "open", Input: "fleet ledger", Events: 1013950, Seconds: 0.5, PeakRSS: 512 << 10, Error: "boom"},
	})
	want := "| Command | Input | 100,000 events: time | peak RSS | 1,000,000 events: time | peak RSS |\n" +
		"| --- | --- | ---: | ---: | ---: | ---: |\n" +
		"| `scan` | host copy | 20.7 s | 104.0 MB | 231 s | 300.0 MB |\n" +
		"| `serve` | host copy | 19.7 s to ready, 0.00 s scrape | 105.0 MB | | |\n" +
		"| `open` | fleet ledger | | | failed | |\n"
	if b.String() != want {
		t.Errorf("table:\n%s\nwant:\n%s", b.String(), want)
	}
	for n, want := range map[int64]string{0: "0", 2048: "2 kB", 1 << 20: "1.0 MB", 1536 << 20: "1536.0 MB"} {
		if got := size(n); got != want {
			t.Errorf("size(%d) = %q, want %q", n, got, want)
		}
	}
	if thousands(1234567) != "1,234,567" || thousands(999) != "999" || seconds(0.123) != "0.12 s" {
		t.Error("number forms")
	}
}
