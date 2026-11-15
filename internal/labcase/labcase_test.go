package labcase

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/bundle"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/injection"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/scan"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/testutil"
)

var ctx = context.Background()

// capture is one directory under testdata/real and what reading it gives.
type capture struct {
	dir  string
	host string
	// ce and ue are the errors of the host, each counted once.
	ce, ue int64
	// grades says how the error records split by observation grade.
	gradeA, gradeB, gradeC int64
	// hard and soft are the pages taken offline, each counted once.
	hard, soft int
	// earlier counts pages that were taken offline in an earlier boot of
	// the same host: the rasdaemon database remembers them, the plan of
	// this capture does not list them.
	earlier int
	// sources are the entries of the source inventory of a scan of the
	// copy, as "kind status".
	sources []string
	// finding is the grade of the host.
	finding model.VerdictGrade
	// notes are remarks the sources must make, as "kind: text".
	notes []string
	// journal says that the capture holds journalctl -k -o json.
	journal bool
	// reports are the sources that hold the injections of the plan, in a
	// scan of the copy.
	reports []string
}

var captures = []capture{
	{
		dir: "7.3.0-baseline", host: "lab-vm", journal: true,
		sources: []string{"hostinfo ok", "rasdaemon empty", "klog ok", "edac-sysfs empty", "smbios ok"},
		finding: model.VerdictOK,
		notes:   []string{"smbios: 128 memory slots, 3 populated"},
	},
	{
		dir: "7.3.0-hwpoison", host: "lab-vm", journal: true, hard: 4,
		reports: []string{"klog/offline", "rasdaemon/memory_failure_event"},
		sources: []string{"hostinfo ok", "rasdaemon ok", "klog ok", "edac-sysfs empty", "smbios ok"},
		finding: model.VerdictOK,
		notes:   []string{"klog: 4 page offlines were asked for by a program"},
	},
	{
		// The second boot of the host: the database still holds the four
		// pages of the boot before.
		dir: "7.3.0-soft-offline", host: "lab-vm", journal: true, soft: 3, earlier: 4,
		// The kernel traces no soft offline: rasdaemon has nothing of this boot.
		reports: []string{"klog/offline"},
		sources: []string{"hostinfo ok", "rasdaemon ok", "klog ok", "edac-sysfs empty", "smbios ok"},
		finding: model.VerdictOK,
	},
	{
		// No EDAC driver and one virtual DIMM: the errors have addresses
		// and no DIMM. A host whose errors cannot be tied to a DIMM gets
		// no finding from the engine today.
		dir: "7.3.0-mce", host: "lab-guest", ce: 7, ue: 3, gradeB: 10, hard: 3, soft: 2,
		// The kernel log has no machine check record: a logged record is
		// not printed by default.
		reports: []string{"klog/offline", "rasdaemon/mce_record", "rasdaemon/memory_failure_event"},
		sources: []string{"hostinfo ok", "rasdaemon ok", "klog ok", "edac-sysfs empty", "smbios ok"},
		finding: model.VerdictOK,
		notes:   []string{"klog: the machine check injector (mce-inject) was set up in a boot of this log"},
	},
	{
		// With mce=print_all the kernel log holds every record too: each
		// error is in two sources and counts once.
		dir: "7.3.0-mce-print-all", host: "lab-guest", ce: 7, ue: 3, gradeB: 10, hard: 3, soft: 2,
		reports: []string{"klog/mce", "klog/offline", "rasdaemon/mce_record", "rasdaemon/memory_failure_event"},
		sources: []string{"hostinfo ok", "rasdaemon ok", "klog ok", "edac-sysfs empty", "smbios ok"},
		finding: model.VerdictOK,
	},
	{
		// The clock was set back by 10 seconds after rasdaemon started: its
		// times are ahead of the kernel's for the whole run.
		dir: "7.3.0-mce-clock-step", host: "lab-guest", ce: 7, ue: 3, gradeB: 10, hard: 3, soft: 2,
		reports: []string{"klog/mce", "klog/offline", "rasdaemon/mce_record", "rasdaemon/memory_failure_event"},
		sources: []string{"hostinfo ok", "rasdaemon ok", "klog ok", "edac-sysfs empty", "smbios ok"},
		finding: model.VerdictOK,
		notes:   []string{"rasdaemon: 13 records are dated by the kernel's time"},
	},
}

// console is a capture that holds nothing but the console of a host that
// went down: the lines a serial console server or the console log of a BMC
// keeps.
type console struct {
	dir string
	// severity and address are the error the console shows.
	severity model.Severity
	address  uint64
}

var consoles = []console{
	{dir: "7.3.0-mce-fatal", severity: model.SevFatal, address: 0x12345040},
}

func dir(t *testing.T, c capture, parts ...string) string {
	t.Helper()
	return testutil.Testdata(t, append([]string{"real", c.dir}, parts...)...)
}

// env reads capture.env, the versions and the time of a capture.
func env(t *testing.T, c capture) map[string]string {
	t.Helper()
	f, err := os.Open(dir(t, c, "capture.env"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok {
			out[k] = v
		}
	}
	return out
}

func capturedAt(t *testing.T, c capture) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, env(t, c)["captured_at"])
	if err != nil {
		t.Fatalf("%s: capture.env: %v", c.dir, err)
	}
	return at
}

func plan(t *testing.T, c capture) *injection.Plan {
	t.Helper()
	p, err := injection.ReadFile(dir(t, c, "plan.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func ledger(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// state is what a ledger holds about a host, in the terms of a capture.
type state struct {
	ce, ue                 int64
	gradeA, gradeB, gradeC int64
	hard, soft             int
}

func stateOf(t *testing.T, s *store.Store, host string) state {
	t.Helper()
	var st state
	totals, err := s.Totals(ctx, store.EventFilter{Host: host})
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range totals {
		st.ce += x.CE
		st.ue += x.UE
		st.gradeA += x.ErrorsA
		st.gradeB += x.ErrorsB
		st.gradeC += x.ErrorsC
	}
	actions, err := s.Actions(ctx, store.ActionFilter{Host: host, Kinds: []model.ActionKind{model.ActionPageOffline}})
	if err != nil {
		t.Fatal(err)
	}
	pages := map[string]bool{}
	for _, a := range actions {
		if !a.OsPhysicalAddress.Valid {
			continue
		}
		k := a.Method + " " + strconv.FormatUint(a.OsPhysicalAddress.V>>12, 16)
		if pages[k] {
			continue
		}
		pages[k] = true
		switch a.Method {
		case "hard":
			st.hard++
		case "soft":
			st.soft++
		}
	}
	return st
}

func (c capture) want() state {
	return state{ce: c.ce, ue: c.ue, gradeA: c.gradeA, gradeB: c.gradeB, gradeC: c.gradeC, hard: c.hard + c.earlier, soft: c.soft}
}

// compare checks the ledger against the plan of the capture: everything
// that was injected is there once, decoded as injected, and nothing else.
// It returns the sources that hold the injections.
func compare(t *testing.T, c capture, s *store.Store, host string, earlier int) []string {
	t.Helper()
	res, err := injection.Compare(ctx, s, host, plan(t, c))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	var reports []string
	for _, ch := range res.Checks {
		for src := range ch.Sources {
			if !seen[src] {
				seen[src] = true
				reports = append(reports, src)
			}
		}
	}
	sort.Strings(reports)
	for _, ch := range res.Failed() {
		t.Errorf("injection %s: found %d of %d, sources %v, differences %v", ch.Injection.ID, ch.Found, ch.Injection.Count, ch.Sources, ch.Differences)
	}
	if res.UnplannedErrors != 0 || res.UnplannedOfflines != earlier {
		t.Errorf("beyond the plan: %d errors, %d pages offline (want 0 and %d): %v", res.UnplannedErrors, res.UnplannedOfflines, earlier, res.Unplanned)
	}
	return reports
}

func inventory(srcs []model.Source) []string {
	var out []string
	for _, s := range srcs {
		if s.Kind == ingest.SlotMapKind || s.Kind == ingest.TimeAlignKind {
			continue
		}
		out = append(out, s.Kind+" "+string(s.Status))
	}
	return out
}

func hasNote(srcs []model.Source, want string) bool {
	kind, text, _ := strings.Cut(want, ": ")
	for _, s := range srcs {
		if s.Kind != kind {
			continue
		}
		for _, n := range s.Notes {
			if strings.Contains(n, text) {
				return true
			}
		}
	}
	return false
}

// TestCopyOfTheHost reads each capture the way "my --root DIR" does: as a
// copy of the host's files.
func TestCopyOfTheHost(t *testing.T) {
	for _, c := range captures {
		t.Run(c.dir, func(t *testing.T) {
			s := ledger(t)
			res, err := scan.Run(ctx, s, scan.Options{Root: dir(t, c), TempDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			if res.Host != c.host {
				t.Errorf("host %q, want %q", res.Host, c.host)
			}
			// The copy is read as of the moment it was made, though the
			// files of a checkout carry the time of the checkout.
			if at := capturedAt(t, c); res.Scan.StartedAt.Sub(at).Abs() > 5*time.Second {
				t.Errorf("read as of %s, captured at %s", res.Scan.StartedAt.Format(time.RFC3339), at.Format(time.RFC3339))
			}
			if got := inventory(res.Sources); strings.Join(got, ", ") != strings.Join(c.sources, ", ") {
				t.Errorf("sources: %s\n   want: %s", strings.Join(got, ", "), strings.Join(c.sources, ", "))
			}
			for _, n := range c.notes {
				if !hasNote(res.Sources, n) {
					t.Errorf("no source says %q", n)
				}
			}
			if got := stateOf(t, s, c.host); got != c.want() {
				t.Errorf("ledger: %+v\n  want: %+v", got, c.want())
			}
			if got := compare(t, c, s, c.host, c.earlier); strings.Join(got, ", ") != strings.Join(c.reports, ", ") {
				t.Errorf("the injections are reported by %v, want %v", got, c.reports)
			}

			out, err := engine.Run(ctx, s, engine.Options{Now: capturedAt(t, c)})
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Hosts) != 1 || out.Hosts[0].Grade != c.finding {
				t.Errorf("finding of the host: %+v, want %s", out.Hosts, c.finding)
			}
			var loose int64
			for _, u := range out.Hosts[0].Unattributed {
				loose += u.CE + u.UE
			}
			if loose != c.ce+c.ue {
				t.Errorf("%d errors without a DIMM, want %d: no source of these hosts names the DIMM", loose, c.ce+c.ue)
			}

			// A second reading of the same copy adds nothing.
			if _, err := scan.Run(ctx, s, scan.Options{Root: dir(t, c), TempDir: t.TempDir()}); err != nil {
				t.Fatal(err)
			}
			if got := stateOf(t, s, c.host); got != c.want() {
				t.Errorf("after a second reading: %+v\n  want: %+v", got, c.want())
			}
		})
	}
}

// TestLooseFiles reads the files of each capture one by one, the way
// "my import FILE..." does: the kernel log as dmesg printed it and as the
// journal exported it, the database and the dmidecode output. A line that
// is in both forms of the kernel log is one record.
func TestLooseFiles(t *testing.T) {
	for _, c := range captures {
		t.Run(c.dir, func(t *testing.T) {
			at := capturedAt(t, c)
			files := []string{"dmesg", filepath.Join("var", "lib", "rasdaemon", "ras-mc_event.db"), "dmidecode.txt"}
			if c.journal {
				files = append([]string{"journal-k.json"}, files...)
			}
			var adapters []adapter.Adapter
			for _, f := range files {
				as, _, err := scan.Detect(ctx, dir(t, c, f), scan.DetectOptions{Now: at, CapturedAt: at, TempDir: t.TempDir()})
				if err != nil {
					t.Fatalf("%s: %v", f, err)
				}
				adapters = append(adapters, as...)
			}
			s := ledger(t)
			res, err := ingest.Run(ctx, s, adapters, ingest.Options{Now: at})
			if err != nil {
				t.Fatal(err)
			}
			// The journal names the host; dmesg does not.
			want := "localhost"
			if c.journal {
				want = c.host
			}
			if res.Host != want {
				t.Errorf("host %q, want %q", res.Host, want)
			}
			if got := stateOf(t, s, res.Host); got != c.want() {
				t.Errorf("ledger: %+v\n  want: %+v", got, c.want())
			}
			compare(t, c, s, res.Host, c.earlier)
		})
	}
}

// TestKernelLogForms reads the kernel log of one boot in every form the
// host can print it: dmesg with its time formats, a read of /dev/kmsg, and
// the text forms of journalctl. Each form alone must give the four pages of
// the plan, the kernel of the boot, and times near the injection.
func TestKernelLogForms(t *testing.T) {
	var c capture
	for _, x := range captures {
		if x.dir == "7.3.0-hwpoison" {
			c = x
		}
	}
	at := capturedAt(t, c)
	p := plan(t, c)
	forms := []struct {
		file string
		host string // the journal names the host, dmesg does not
		// slack is how far the time of a record may lie from the
		// injection: the forms without dates are placed by the capture
		// time or by the hardware clock, which ran ahead on this host.
		slack time.Duration
	}{
		{"dmesg-T.txt", "localhost", 2 * time.Second},
		{"dmesg-iso.txt", "localhost", 2 * time.Second},
		{"dmesg-raw.txt", "localhost", 15 * time.Second},
		{"kmsg.txt", "localhost", 15 * time.Second},
		{"journal-short.txt", c.host, 2 * time.Second},
		{"journal-short-iso.txt", c.host, 2 * time.Second},
		{"journal-short-monotonic.txt", c.host, 15 * time.Second},
	}
	entries, err := os.ReadDir(dir(t, c, "formats"))
	if err != nil || len(entries) != len(forms) {
		t.Fatalf("formats holds %d files, the test knows %d (%v)", len(entries), len(forms), err)
	}
	for _, f := range forms {
		t.Run(f.file, func(t *testing.T) {
			as, what, err := scan.Detect(ctx, dir(t, c, "formats", f.file), scan.DetectOptions{Now: at, CapturedAt: at})
			if err != nil || what != scan.WhatKernelLog {
				t.Fatalf("detected as %q, %v", what, err)
			}
			s := ledger(t)
			res, err := ingest.Run(ctx, s, as, ingest.Options{Now: at})
			if err != nil {
				t.Fatal(err)
			}
			if res.Host != f.host {
				t.Errorf("host %q, want %q", res.Host, f.host)
			}
			if src := res.Sources[0]; src.Status != model.SourceOK || src.Actions != 4 || src.Events != 0 {
				t.Errorf("source: %s, %d actions, %d events, notes %q", src.Status, src.Actions, src.Events, src.Notes)
			}
			if !hasNote(res.Sources, "klog: 4 page offlines were asked for by a program") {
				t.Errorf("the offlines are not told from hardware errors: %q", res.Sources[0].Notes)
			}
			compare(t, c, s, res.Host, 0)
			boots, err := s.Boots(ctx, res.Host)
			if err != nil || len(boots) != 1 || boots[0].Kernel != "7.3.0-ras" {
				t.Errorf("boots: %+v, %v", boots, err)
			}
			actions, err := s.Actions(ctx, store.ActionFilter{Host: res.Host})
			if err != nil || len(actions) != len(p.Injections) {
				t.Fatalf("%d actions, %v", len(actions), err)
			}
			for i, a := range actions {
				if d := a.At.Sub(p.Injections[i].Time).Abs(); d > f.slack {
					t.Errorf("page %d is dated %s, injected at %s: %s apart (%s)", i+1, a.At.Format(time.RFC3339),
						p.Injections[i].Time.Format(time.RFC3339), d.Round(time.Millisecond), a.TimeQuality)
				}
			}
		})
	}
}

// TestConsoleOfAHostThatWentDown reads the console of a guest that a
// fatal machine check brought down. No other source exists: the kernel
// panicked before anything could be stored.
func TestConsoleOfAHostThatWentDown(t *testing.T) {
	for _, c := range consoles {
		t.Run(c.dir, func(t *testing.T) {
			path := testutil.Testdata(t, "real", c.dir, "console.log")
			as, what, err := scan.Detect(ctx, path, scan.DetectOptions{})
			if err != nil || what != scan.WhatKernelLog {
				t.Fatalf("detected as %q, %v", what, err)
			}
			s := ledger(t)
			res, err := ingest.Run(ctx, s, as, ingest.Options{})
			if err != nil {
				t.Fatal(err)
			}
			p, err := injection.ReadFile(testutil.Testdata(t, "real", c.dir, "plan.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			cmp, err := injection.Compare(ctx, s, res.Host, p)
			if err != nil {
				t.Fatal(err)
			}
			for _, ch := range cmp.Failed() {
				t.Errorf("injection %s: found %d of %d, differences %v", ch.Injection.ID, ch.Found, ch.Injection.Count, ch.Differences)
			}
			if cmp.UnplannedErrors != 0 || cmp.UnplannedOfflines != 0 {
				t.Errorf("beyond the plan: %v", cmp.Unplanned)
			}
			events, err := s.EventList(ctx, store.EventFilter{Host: res.Host})
			if err != nil || len(events) != 1 {
				t.Fatalf("%d events, %v", len(events), err)
			}
			e := events[0]
			// The record carries its own time, so the log needs no date.
			if e.ErrorSeverity != c.severity || !e.OsPhysicalAddress.Valid || e.OsPhysicalAddress.V != c.address || e.Grade != model.GradeB ||
				e.TimeQuality != model.TimeExact || e.ErrorDT.Sub(p.Injections[0].Time).Abs() > time.Minute {
				t.Errorf("event: %+v", *e)
			}
			out, err := engine.Run(ctx, s, engine.Options{Now: e.ErrorDT.Add(time.Minute)})
			if err != nil || len(out.Hosts) != 1 {
				t.Fatalf("engine: %v", err)
			}
		})
	}
}

// liveScan is the part of "my scan --json" that is compared.
type liveScan struct {
	Host     string `json:"host"`
	ExitCode int    `json:"exit_code"`
	Data     struct {
		Host struct {
			CE    int64  `json:"ce"`
			UE    int64  `json:"ue"`
			Grade string `json:"grade"`
			Scan  struct {
				Kind       string `json:"kind"`
				Privileged bool   `json:"privileged"`
			} `json:"scan"`
		} `json:"host"`
		Sources []struct {
			Kind   string `json:"kind"`
			Status string `json:"status"`
		} `json:"sources"`
	} `json:"data"`
}

// TestWhatMySaidOnTheHost compares what "my" printed on the lab host, as
// root and on the running system, with what the copy gives now.
func TestWhatMySaidOnTheHost(t *testing.T) {
	for _, c := range captures {
		t.Run(c.dir, func(t *testing.T) {
			b, err := os.ReadFile(dir(t, c, "my", "scan.json"))
			if err != nil {
				t.Fatal(err)
			}
			var live liveScan
			if err := json.Unmarshal(b, &live); err != nil {
				t.Fatal(err)
			}
			if live.Host != c.host || live.Data.Host.Scan.Kind != "live" || !live.Data.Host.Scan.Privileged {
				t.Errorf("scan: host %q, kind %q, privileged %v", live.Host, live.Data.Host.Scan.Kind, live.Data.Host.Scan.Privileged)
			}
			if live.Data.Host.CE != c.ce || live.Data.Host.UE != c.ue || live.Data.Host.Grade != string(c.finding) || live.ExitCode != c.finding.ExitCode() {
				t.Errorf("live: CE %d, UE %d, finding %s, exit code %d; want %d, %d, %s", live.Data.Host.CE, live.Data.Host.UE,
					live.Data.Host.Grade, live.ExitCode, c.ce, c.ue, c.finding)
			}
			var got []string
			for _, s := range live.Data.Sources {
				got = append(got, s.Kind+" "+s.Status)
			}
			if strings.Join(got, ", ") != strings.Join(c.sources, ", ") {
				t.Errorf("live sources: %s\n        want: %s", strings.Join(got, ", "), strings.Join(c.sources, ", "))
			}
			if exit, _ := os.ReadFile(dir(t, c, "my", "scan.exit")); strings.TrimSpace(string(exit)) != "exit="+strconv.Itoa(c.finding.ExitCode()) {
				t.Errorf("exit code on the host: %q", exit)
			}
		})
	}
}

// TestBundleWrittenOnTheHost opens the case bundle "my bundle --anonymize"
// wrote on the lab host. A bundle is read by later versions of the tool,
// and it holds what was injected.
func TestBundleWrittenOnTheHost(t *testing.T) {
	for _, c := range captures {
		t.Run(c.dir, func(t *testing.T) {
			b, err := bundle.Open(ctx, dir(t, c, "my", "case.mycase"), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			m := b.Manifest
			if !m.Anonymized || len(m.Hosts) != 1 || !strings.HasPrefix(m.Hosts[0].Name, "host-") {
				t.Fatalf("manifest: anonymized %v, hosts %+v", m.Anonymized, m.Hosts)
			}
			if m.Hosts[0].CE != c.ce || m.Hosts[0].UE != c.ue {
				t.Errorf("manifest: CE %d, UE %d; want %d, %d", m.Hosts[0].CE, m.Hosts[0].UE, c.ce, c.ue)
			}
			s, err := store.Open(ctx, b.Ledger, store.ReadOnly)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			// On the host the journal of every boot was read, so the pages
			// of an earlier boot are in the bundle through both sources.
			if got := stateOf(t, s, m.Hosts[0].Name); got != c.want() {
				t.Errorf("ledger of the bundle: %+v\n               want: %+v", got, c.want())
			}
			compare(t, c, s, m.Hosts[0].Name, c.earlier)
			// Nothing in it names the lab host.
			for _, name := range []string{c.host} {
				raw, _ := os.ReadFile(b.Ledger)
				if strings.Contains(string(raw), name) {
					t.Errorf("the ledger of the bundle holds %q", name)
				}
			}
		})
	}
}

// TestEveryCaptureIsListed fails for a directory under testdata/real that
// no test reads.
func TestEveryCaptureIsListed(t *testing.T) {
	entries, err := os.ReadDir(testutil.Testdata(t, "real"))
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, c := range captures {
		known[c.dir] = true
	}
	for _, c := range consoles {
		known[c.dir] = true
	}
	var missing []string
	for _, e := range entries {
		if e.IsDir() && !known[e.Name()] {
			missing = append(missing, e.Name())
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("captures without a test: %v", missing)
	}
	for name := range known {
		for _, f := range []string{"README.md", "plan.yaml", "capture.env"} {
			if _, err := os.Stat(testutil.Testdata(t, "real", name, f)); err != nil {
				t.Errorf("%s has no %s", name, f)
			}
		}
	}
}
