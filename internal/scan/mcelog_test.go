package scan

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/synth"
	"github.com/agenticode/my/internal/testutil"
)

// slotTotals returns the errors of a host per slot, each counted once: all
// of them, or those from a time on.
func slotTotals(t *testing.T, s *store.Store, host string, from time.Time) map[string][2]int64 {
	t.Helper()
	totals, err := s.Totals(ctx, store.EventFilter{Host: host, From: from})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][2]int64{}
	for _, tt := range totals {
		v := out[tt.Slot]
		out[tt.Slot] = [2]int64{v[0] + tt.CE, v[1] + tt.UE}
	}
	return out
}

// daemon writes a program that answers like mcelog --client, with one
// block per line of "socket channel dimm name corrected uncorrected".
func daemon(t *testing.T, blocks ...string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("Memory errors\n")
	for _, line := range blocks {
		var socket, channel, dimm, name string
		var ce, ue int
		if _, err := fmt.Sscan(line, &socket, &channel, &dimm, &name, &ce, &ue); err != nil {
			t.Fatalf("block %q: %v", line, err)
		}
		fmt.Fprintf(&b, "SOCKET %s CHANNEL %s DIMM %s\n", socket, channel, dimm)
		if name != "-" {
			fmt.Fprintf(&b, "DMI_NAME \"%s\" DMI_LOCATION \"NODE %s CHANNEL %s DIMM %s\"\n", name, socket, channel, dimm)
		}
		fmt.Fprintf(&b, "corrected memory errors:\n\t%d total\n\t0 in 24h\nuncorrected memory errors:\n\t%d total\n\t0 in 24h\n\n", ce, ue)
	}
	out := filepath.Join(t.TempDir(), "answer.txt")
	write(t, out, b.String(), 0o644)
	prog := filepath.Join(t.TempDir(), "mcelog")
	write(t, prog, "#!/bin/sh\n[ \"$1\" = \"--client\" ] || exit 9\ncat \""+out+"\"\n", 0o755)
	return prog
}

// A host that keeps a rasdaemon database, the EDAC counters and the mcelog
// daemon reports every error three times. The ledger counts it once, and
// what only the daemon saw is still counted.
func TestDaemonCountsOfALiveHost(t *testing.T) {
	root, ds := tree(t, synth.FlavorIntel)
	opts := Options{Root: root, Live: true, Now: ds.Scenario.End, TempDir: t.TempDir(), Journalctl: "-", NvidiaSMI: "-", Mcelog: "-"}

	base := ledger(t)
	res, err := Run(ctx, base, opts)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(inventory(res.Sources), "mcelog") {
		t.Fatalf("left out, and in the inventory: %s", inventory(res.Sources))
	}
	before := slotTotals(t, base, res.Host, time.Time{})
	if before["CPU0_C1"][0] < 100 || before["CPU1_D1"][0] < 10 {
		t.Fatalf("the demo host changed: totals %v", before)
	}
	// The counts of the daemon live in its memory: it has seen the errors
	// of the running boot and none before.
	boots, err := base.Boots(ctx, res.Host)
	if err != nil || len(boots) == 0 {
		t.Fatalf("boots of the demo host: %d, %v", len(boots), err)
	}
	boot := boots[len(boots)-1].StartedAt
	running := slotTotals(t, base, res.Host, boot)
	c1, d1 := running["CPU0_C1"], running["CPU1_D1"]
	if c1[0] < 100 || c1[0] >= before["CPU0_C1"][0] || d1[0] < 2 {
		t.Fatalf("the demo host changed: since the boot of %s %v, in all %v", boot, running, before)
	}

	// The daemon saw what the host recorded on CPU0_C1, five errors more on
	// CPU1_D1, two on a DIMM nothing else reports, and four it could not
	// place.
	opts.Mcelog = daemon(t,
		fmt.Sprintf("0 any any - %d %d", c1[0]+2+4, c1[1]),
		fmt.Sprintf("0 2 0 CPU0_C1 %d %d", c1[0], c1[1]),
		"0 0 0 CPU0_A1 2 0",
		"0 1 0 CPU0_B1 0 0",
		fmt.Sprintf("1 any any - %d 0", d1[0]+5),
		fmt.Sprintf("1 3 0 CPU1_D1 %d 0", d1[0]+5),
	)
	s := ledger(t)
	res, err = Run(ctx, s, opts)
	if err != nil {
		t.Fatal(err)
	}
	src := source(t, res.Sources, "mcelog")
	if src.Status != model.SourceOK || src.Records != 6 || src.Skipped != 0 || src.Origin != opts.Mcelog+" --client" {
		t.Errorf("mcelog source = %+v", src)
	}
	after := slotTotals(t, s, res.Host, time.Time{})
	want := map[string][2]int64{}
	for k, v := range before {
		want[k] = v
	}
	want["CPU1_D1"] = [2]int64{before["CPU1_D1"][0] + 5, before["CPU1_D1"][1]}
	want["CPU0_A1"] = [2]int64{2, 0}
	// The rest of socket 0: its total less the blocks of its DIMMs. No
	// located record of the host is without a slot, so all of it counts.
	want[""] = [2]int64{before[""][0] + 4, before[""][1]}
	for slot, w := range want {
		if after[slot] != w {
			t.Errorf("slot %q: CE %d UE %d, want CE %d UE %d", slot, after[slot][0], after[slot][1], w[0], w[1])
		}
	}
	for slot := range after {
		if _, ok := want[slot]; !ok {
			t.Errorf("slot %q appeared with %v", slot, after[slot])
		}
	}

	// The counts are tied to the DIMM in the slot the DMI name gives.
	events, err := s.EventList(ctx, store.EventFilter{Host: res.Host, Kinds: []model.EventKind{model.KindCounter}})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range events {
		if e.Source != "mcelog/counter" {
			continue
		}
		seen[e.Slot] = true
		if e.Grade != model.GradeC || (e.Slot != "" && e.DIMMUID == "") {
			t.Errorf("counter event of slot %q: grade %s, DIMM %q", e.Slot, e.Grade, e.DIMMUID)
		}
	}
	for _, slot := range []string{"CPU0_C1", "CPU1_D1", "CPU0_A1", ""} {
		if !seen[slot] {
			t.Errorf("no mcelog counter event for slot %q (have %v)", slot, seen)
		}
	}

	// A second reading an hour later adds the growth alone.
	opts.Mcelog = daemon(t,
		fmt.Sprintf("0 2 0 CPU0_C1 %d %d", c1[0]+3, c1[1]),
		"0 0 0 CPU0_A1 2 0",
		fmt.Sprintf("1 3 0 CPU1_D1 %d 0", d1[0]+5),
	)
	opts.Now = ds.Scenario.End.Add(time.Hour)
	if _, err = Run(ctx, s, opts); err != nil {
		t.Fatal(err)
	}
	later := slotTotals(t, s, res.Host, time.Time{})
	if got := later["CPU0_C1"][0]; got != before["CPU0_C1"][0]+3 {
		t.Errorf("CPU0_C1 after the second reading: CE %d, want %d", got, before["CPU0_C1"][0]+3)
	}
	if later["CPU1_D1"] != after["CPU1_D1"] || later["CPU0_A1"] != after["CPU0_A1"] {
		t.Errorf("slots without growth changed: %v, was %v", later, after)
	}
}

// A host without the program gets no entry; one where the program is
// there and no daemon answers gets an entry that says so.
func TestDaemonCountsWithoutADaemon(t *testing.T) {
	root, ds := tree(t, synth.FlavorIntel)
	t.Setenv("PATH", t.TempDir())
	opts := Options{Root: root, Live: true, Now: ds.Scenario.End, TempDir: t.TempDir(), Journalctl: "-", NvidiaSMI: "-"}
	res, err := Run(ctx, ledger(t), opts)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(inventory(res.Sources), "mcelog") {
		t.Errorf("not installed, and in the inventory: %s", inventory(res.Sources))
	}

	opts.Mcelog = filepath.Join(t.TempDir(), "mcelog")
	write(t, opts.Mcelog, "#!/bin/sh\necho 'mcelog: client connect: No such file or directory' >&2\n", 0o755)
	res, err = Run(ctx, ledger(t), opts)
	if err != nil {
		t.Fatal(err)
	}
	if src := source(t, res.Sources, "mcelog"); src.Status != model.SourceAbsent || !strings.Contains(src.Detail, "no mcelog daemon is running") {
		t.Errorf("mcelog source = %+v", src)
	}

	// A copy of a host's files is not asked: no command is run for it.
	opts.Live = false
	res, err = Run(ctx, ledger(t), opts)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(inventory(res.Sources), "mcelog") {
		t.Errorf("a copy was asked for the daemon: %s", inventory(res.Sources))
	}
}

// A saved answer is recognised by its content and read as of the import.
func TestDetectDaemonCounts(t *testing.T) {
	at := time.Date(2026, 11, 14, 9, 30, 0, 0, time.UTC)
	as, what, err := Detect(ctx, testutil.Testdata(t, "mcelog", "client-x3850.txt"), DetectOptions{Now: at})
	if err != nil || what != WhatMcelog || len(as) != 1 {
		t.Fatalf("Detect: %v, %q, %v", as, what, err)
	}
	var c adapter.Collected
	if rep := as[0].Collect(ctx, &c); rep.Status != model.SourceOK || len(c.Counters) != 2 || !c.Counters[0].ObservedAt.Equal(at) {
		t.Errorf("report = %+v, readings %+v", rep, c.Counters)
	}

	// Compressed, the same.
	packed := filepath.Join(t.TempDir(), "client.txt.gz")
	write(t, packed, string(gz(t, testutil.ReadTestdata(t, "mcelog", "client-paper.txt"))), 0o644)
	as, what, err = Detect(ctx, packed, DetectOptions{Now: at})
	if err != nil || what != WhatMcelog {
		t.Fatalf("Detect of a compressed file: %q, %v", what, err)
	}
	c = adapter.Collected{}
	if rep := as[0].Collect(ctx, &c); rep.Status != model.SourceOK || len(c.Counters) != 2 {
		t.Errorf("compressed: report = %+v", rep)
	}
}
