package scan

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/dimmspd"
	"github.com/agenticode/my/internal/adapter/dimmspd/spdtest"
	"github.com/agenticode/my/internal/decode/spd"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/synth"
	"github.com/agenticode/my/internal/testutil"

	_ "modernc.org/sqlite"
)

var ds0 = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

// collectInto reads a source into a sink that keeps everything.
func collectInto(t *testing.T, a adapter.Adapter) *adapter.Collected {
	t.Helper()
	var c adapter.Collected
	a.Collect(ctx, &c)
	return &c
}

func spdHosts(t *testing.T) []spdtest.Host {
	return spdtest.Hosts(testutil.ReadTestdata(t, "spd", "illumos", "ddr4-rdimm-64g-2rx4.bin"),
		testutil.ReadTestdata(t, "spd", "illumos", "ddr5-rdimm-64g-2rx4.bin"),
		testutil.ReadTestdata(t, "spd", "rigred", "ddr3-sodimm.bin"))
}

// ledgerRows dumps every row of every table of a ledger file, but the SPD
// readings, the probe, the source row of the SPD, the identity of the
// ledger and the checks of my preflight (which ask the system the test
// runs on), with the columns that number rows left out.
func ledgerRows(t *testing.T, path string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT IN ('meta', 'spd_reading', 'capture_check') ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		tables = append(tables, n)
	}
	rows.Close()
	var out []string
	for _, tb := range tables {
		cols, err := db.Query(`SELECT name FROM pragma_table_info(?)`, tb)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for cols.Next() {
			var n string
			cols.Scan(&n)
			if n != "id" && n != "source_id" {
				names = append(names, `quote("`+n+`")`)
			}
		}
		cols.Close()
		where := ""
		switch tb {
		case "host_reading":
			where = ` WHERE kind != 'spd-probe'`
		case "source":
			where = ` WHERE kind != 'spd'`
		}
		r, err := db.Query(`SELECT ` + strings.Join(names, ` || '|' || `) + ` FROM "` + tb + `"` + where)
		if err != nil {
			t.Fatalf("%s: %v", tb, err)
		}
		for r.Next() {
			var s sql.NullString
			r.Scan(&s)
			out = append(out, tb+": "+s.String)
		}
		r.Close()
	}
	sort.Strings(out)
	return out
}

type spdOutcome struct {
	rows   []string
	grades []string
	host   model.VerdictGrade
	src    *model.Source
}

func scanWithSPD(t *testing.T, root string, ds *synth.Dataset, opts Options) spdOutcome {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ledger.db")
	s, err := store.Open(ctx, path, store.Create)
	if err != nil {
		t.Fatal(err)
	}
	opts.Root, opts.Live, opts.Now = root, true, ds.Scenario.End
	opts.Journalctl, opts.Mcelog, opts.NvidiaSMI, opts.Numactl, opts.Numastat, opts.Sar = "-", "-", "-", "-", "-", "-"
	res, err := Run(ctx, s, opts)
	if err != nil {
		t.Fatal(err)
	}
	var o spdOutcome
	for i := range res.Sources {
		if res.Sources[i].Kind == dimmspd.Kind {
			o.src = &res.Sources[i]
		}
	}
	r, err := engine.Run(ctx, s, engine.Options{Now: ds.Scenario.End})
	if err != nil {
		t.Fatal(err)
	}
	if h := r.Host(res.Host); h != nil {
		o.host = h.Grade
		for _, d := range h.DIMMs {
			if d.Verdict != nil {
				o.grades = append(o.grades, d.Slot+" "+string(d.Verdict.Grade))
			}
		}
	}
	sort.Strings(o.grades)
	s.Close()
	o.rows = ledgerRows(t, path)
	return o
}

// TestSPDLeavesTheRestAlone: a scan of a made host with each made SPD tree
// (every reason and every state of a reading) gives the same rows of every
// other table, the same findings and the same exit code as a scan without
// the SPD.
func TestSPDLeavesTheRestAlone(t *testing.T) {
	root, ds := tree(t, synth.FlavorIntel)
	// The scans as root and not: the scan row says which.
	without := map[bool]spdOutcome{}
	for _, root2 := range []bool{false, true} {
		without[root2] = scanWithSPD(t, root, ds, Options{NoSPD: true, Privileged: root2})
		if w := without[root2]; w.src != nil || len(w.grades) == 0 {
			t.Fatalf("without the SPD: source %+v, grades %v", w.src, w.grades)
		}
	}
	for _, h := range spdHosts(t) {
		want := without[h.Privileged]
		t.Run(h.Name, func(t *testing.T) {
			fsys := spdtest.NewFS(t, h.Tree)
			for k, v := range h.Faults {
				fsys.Faults[k] = v
			}
			got := scanWithSPD(t, root, ds, Options{SPDFS: fsys, SPDDeadline: spdtest.Deadline, Privileged: h.Privileged})
			if got.src == nil || got.src.Status != h.Status {
				t.Fatalf("source %+v, want status %s", got.src, h.Status)
			}
			if a, b := strings.Join(got.rows, "\n"), strings.Join(want.rows, "\n"); a != b {
				t.Errorf("the other rows differ:\n%s", diffLines(want.rows, got.rows))
			}
			if strings.Join(got.grades, ", ") != strings.Join(want.grades, ", ") || got.host != want.host || got.host.ExitCode() != want.host.ExitCode() {
				t.Errorf("grades %v (host %s), want %v (host %s)", got.grades, got.host, want.grades, want.host)
			}
		})
	}
}

func diffLines(a, b []string) string {
	in := func(xs []string) map[string]bool {
		m := map[string]bool{}
		for _, x := range xs {
			m[x] = true
		}
		return m
	}
	ma, mb := in(a), in(b)
	var out []string
	for _, x := range a {
		if !mb[x] {
			out = append(out, "- "+x)
		}
	}
	for _, x := range b {
		if !ma[x] {
			out = append(out, "+ "+x)
		}
	}
	if len(out) > 20 {
		out = append(out[:20], fmt.Sprintf("... %d more", len(out)-20))
	}
	return strings.Join(out, "\n")
}

// TestNoSPD: --no-spd leaves the source out of a live scan and of a scan
// of a copy; a harvest reads no SPD; a copy without spd-probe.txt and spd/
// gets no source.
func TestNoSPD(t *testing.T) {
	root, ds := tree(t, synth.FlavorIntel)
	has := func(opts Options) bool {
		for _, a := range Sources(ctx, opts) {
			if a.Kind() == dimmspd.Kind {
				return true
			}
		}
		return false
	}
	live := Options{Root: root, Live: true, Now: ds.Scenario.End, Journalctl: "-"}
	if !has(live) {
		t.Error("a live scan has no spd source")
	}
	live.NoSPD = true
	if has(live) {
		t.Error("--no-spd: a live scan has an spd source")
	}
	if has(Options{Root: root, Now: ds.Scenario.End}) {
		t.Error("a copy without SPD files has an spd source")
	}
	if err := os.WriteFile(filepath.Join(root, dimmspd.ProbeFile), []byte("spd-probe 1\ni2c absent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !has(Options{Root: root, Now: ds.Scenario.End}) || has(Options{Root: root, Now: ds.Scenario.End, NoSPD: true}) {
		t.Error("a copy with spd-probe.txt")
	}
	a, b := HarvestSources(ctx, Options{Root: root, Live: true, Now: ds.Scenario.End, Journalctl: "-", NoSPD: true})
	for _, x := range append(a, b...) {
		if x.Kind() == dimmspd.Kind {
			t.Error("a harvest with NoSPD reads the SPD")
		}
	}
}

// TestDetectSPD: every form of a dump is recognised; a binary file with a
// bad base CRC is not taken.
func TestDetectSPD(t *testing.T) {
	for _, c := range []struct {
		path []string
		what string
	}{
		{[]string{"spd", "illumos", "ddr4-rdimm-64g-2rx4.bin"}, WhatSPDDump},
		{[]string{"spd", "ubihazard", "ddr5-udimm-16g-1rx8-a.bin"}, WhatSPDDump},
		{[]string{"spd", "illumos", "M386AAK40B40-CWD70.spd"}, WhatSPDDump},
		{[]string{"spd", "illumos", "forms", "ddr4-rdimm-64g-2rx4.hexdump-C.txt"}, WhatSPDDump},
		{[]string{"spd", "illumos", "forms", "ddr4-rdimm-64g-2rx4.hexdump.txt"}, WhatSPDDump},
		{[]string{"spd", "illumos", "forms", "ddr5-rdimm-64g-2rx4.od.txt"}, WhatSPDDump},
		{[]string{"spd", "illumos", "forms", "ddr5-rdimm-64g-2rx4.xxd.txt"}, WhatSPDDump},
		{[]string{"spd", "illumos", "decode-dimms-4.4.txt"}, WhatSPDText},
	} {
		p := testutil.Testdata(t, c.path...)
		as, what, err := Detect(ctx, p, DetectOptions{})
		if err != nil || len(as) != 1 || as[0].Kind() != dimmspd.Kind || what != c.what {
			t.Errorf("%s: %v %q %v", filepath.Join(c.path...), kinds(as), what, err)
		}
	}
	bad := testutil.ReadTestdata(t, "spd", "illumos", "ddr4-rdimm-64g-2rx4.bin")
	bad[4] ^= 1
	p := filepath.Join(t.TempDir(), "bad.bin")
	if err := os.WriteFile(p, bad, 0o644); err != nil {
		t.Fatal(err)
	}
	if as, what, err := Detect(ctx, p, DetectOptions{}); err == nil {
		t.Errorf("a binary SPD with a bad CRC taken for %s (%v)", what, kinds(as))
	}
	// The DDR3 dump: its memory type is not decoded and its CRC is not one
	// this reader checks.
	if _, _, err := Detect(ctx, testutil.Testdata(t, "spd", "rigred", "ddr3-sodimm.bin"), DetectOptions{}); err == nil {
		t.Error("a DDR3 binary dump taken")
	}
}

// TestCollectHostSPD: a collection writes the probe and the bytes of each
// SPD device; a scan of the collection gives the same SPD rows and reason
// as a scan of the made host; every SPD step that fails leaves the
// collection whole.
func TestCollectHostSPD(t *testing.T) {
	for _, h := range spdHosts(t) {
		if len(h.Faults) > 0 {
			continue // a fault of a read cannot be put on disk
		}
		t.Run(h.Name, func(t *testing.T) {
			root := t.TempDir()
			h.Tree.File("proc/sys/kernel/osrelease", spdtest.Release+"\n")
			if err := h.Tree.Write(root); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(t.TempDir(), "out")
			res, err := CollectHost(context.Background(), CollectOptions{Root: root, Out: out, Privileged: h.Privileged,
				Programs: map[string]string{"dmidecode": "-", "lshw": "-", "numactl": "-", "numastat": "-", "sar": "-", "mcelog": "-"},
				Kmsg:     func() ([]byte, error) { return nil, os.ErrNotExist }})
			if err != nil {
				t.Fatal(err)
			}
			if res.Partial() {
				t.Errorf("partial: %+v", res.Entries)
			}
			b, err := os.ReadFile(filepath.Join(out, dimmspd.ProbeFile))
			if err != nil {
				t.Fatal(err)
			}
			fsys, _ := os.DirFS(root).(dimmspd.FS)
			p, devs := dimmspd.ProbeHost(ctx, fsys, dimmspd.LiveOptions{Privileged: h.Privileged, Release: spdtest.Release})
			if string(b) != p.Text() {
				t.Errorf("spd-probe.txt:\n%s\nprobe of the host:\n%s", b, p.Text())
			}
			var spdEntries int
			for _, e := range res.Entries {
				if e.Group == GroupSPD {
					spdEntries++
				}
			}
			files := 0
			for _, d := range devs {
				if d.Path != "" {
					files++
					got, err := os.ReadFile(filepath.Join(out, dimmspd.Dir, d.Client.ID+".bin"))
					if err != nil || string(got) != string(d.Raw) {
						t.Errorf("%s: %d bytes written, %d read (%v)", d.Client.ID, len(got), len(d.Raw), err)
					}
				}
			}
			if spdEntries != 1+files {
				t.Errorf("%d entries of the group spd, %d files", spdEntries, files)
			}
			// The copy: same readings and the same reason.
			var c, l []string
			copyRes := collectInto(t, dimmspd.NewCopy(out, ds0, h.Mem))
			liveRes := collectInto(t, dimmspd.NewLive(fsys, "/", dimmspd.LiveOptions{Privileged: h.Privileged, Release: spdtest.Release}, h.Mem))
			for _, r := range copyRes.SPDs {
				c = append(c, fmt.Sprintf("%s %s %d %s %s %x", r.Bus, r.Driver, r.Address, r.State, r.Detail, r.Raw))
			}
			for _, r := range liveRes.SPDs {
				l = append(l, fmt.Sprintf("%s %s %d %s %s %x", r.Bus, r.Driver, r.Address, r.State, r.Detail, r.Raw))
			}
			sort.Strings(c)
			sort.Strings(l)
			if strings.Join(c, "\n") != strings.Join(l, "\n") {
				t.Errorf("readings of the copy:\n%s\nof the host:\n%s", strings.Join(c, "\n"), strings.Join(l, "\n"))
			}
			pc, pl := dimmspd.ParseProbe(copyRes.Readings[0].Text), dimmspd.ParseProbe(liveRes.Readings[0].Text)
			if dimmspd.Reason(pc, h.Mem) != dimmspd.Reason(pl, h.Mem) || dimmspd.Reason(pc, h.Mem) != h.Reason {
				t.Errorf("reason of the copy %q, of the host %q, want %q", dimmspd.Reason(pc, h.Mem), dimmspd.Reason(pl, h.Mem), h.Reason)
			}
			for _, r := range copyRes.SPDs {
				if h.States[r.Bus[4:]+"-00"+fmt.Sprintf("%02x", r.Address)] == "" && spd.Read(r.State) {
					t.Errorf("reading %+v", r)
				}
			}
		})
	}
}
