package labcase

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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
	// live are the entries of the scan on the host, where they are not
	// those of the copy: since e242aa0 a scan of the running host reads
	// the journal and the kernel's ring buffer, one entry each.
	live []string
	// finding is the grade of the host.
	finding model.VerdictGrade
	// said is the grade "my" printed on the host when the capture was
	// made, where it is not the finding of today: the captures of errors
	// and page offlines that name no DIMM were made before the engine had
	// a finding for them, and what was printed then stays as it was.
	said model.VerdictGrade
	// notes are remarks the sources must make, as "kind: text".
	notes []string
	// journal says that the capture holds journalctl -k -o json.
	journal bool
	// boots is the number of boots in journal-all.json, the kernel
	// messages of every boot the journal of the host held, in a capture
	// whose errors span several boots. The kernel log of the running boot
	// (dmesg, journal-k.json) has only the errors of the last one.
	boots int
	// clock is what the ledger adds to the times of the rasdaemon records,
	// to the second: on a host without machine check records whose clock
	// was set after rasdaemon started. The other captures must be left as
	// they are.
	clock time.Duration
	// before is what the journal of the host still held of the boot before
	// the capture: the host's own scan and its bundle count it on top.
	before state
	// reports are the sources that hold the injections of the plan, in a
	// scan of the copy.
	reports []string
	// noDIMM counts the errors that are tied to no DIMM.
	noDIMM int64
	// virtual is the hypervisor the host of the capture runs under: every
	// host of the lab is a virtual machine.
	virtual string
	// loose are the rules of the host finding, which speaks about the
	// errors and the pages taken offline that are tied to no DIMM. A
	// capture whose records all name a DIMM has no host finding. asked
	// says that the finding notes that a program asked for pages to be
	// taken offline.
	loose []string
	asked bool
	// dimms are the DIMMs the errors belong to, when the host names them.
	// Every other DIMM of the host must be without errors and found ok.
	dimms []dimm
	// forms says what the kernel log of the boot gives when it is read
	// alone, in each of the forms under formats/.
	forms *forms
}

// dimm is what the engine must say about a DIMM of a capture.
type dimm struct {
	slot, serial string
	ce, ue       int64
	grade        model.VerdictGrade
	mode         model.FaultMode
	rules        []string
	// repair is the kind of the repair the faults of the DIMM name, if
	// any; before and after count the errors of those faults before it and
	// at the repaired place since.
	repair        model.ActionKind
	before, after int64
}

// forms is what one form of the kernel log gives by itself.
type forms struct {
	events, actions int
	kernel          string
	note            string
	// idle is how long before the capture the kernel logged its last
	// line. A log with seconds since boot only is placed by the capture
	// time, as if its last line had been written then: its records are
	// dated late by this much.
	idle time.Duration
}

var captures = []capture{
	{
		dir: "7.3.0-baseline", host: "lab-vm", virtual: "VMware", journal: true,
		sources: []string{"hostinfo ok", "rasdaemon empty", "klog ok", "edac-sysfs empty", "smbios ok"},
		finding: model.VerdictOK,
		notes:   []string{"smbios: 128 memory slots, 3 populated"},
	},
	{
		dir: "7.3.0-hwpoison", host: "lab-vm", virtual: "VMware", journal: true, hard: 4,
		reports: []string{"klog/offline", "rasdaemon/memory_failure_event"},
		forms:   &forms{actions: 4, kernel: "7.3.0-ras", note: "klog: 4 page offlines were asked for by a program"},
		sources: []string{"hostinfo ok", "rasdaemon ok", "klog ok", "edac-sysfs empty", "smbios ok"},
		// The pages name no DIMM: the host finding watches them, and its
		// check says that a program asked for them.
		finding: model.VerdictObserve, said: model.VerdictOK, loose: []string{"R-HOST-02"}, asked: true,
		notes: []string{"klog: 4 page offlines were asked for by a program"},
	},
	{
		// The second boot of the host: the database still holds the four
		// pages of the boot before.
		dir: "7.3.0-soft-offline", host: "lab-vm", virtual: "VMware", journal: true, soft: 3, earlier: 4,
		// The kernel traces no soft offline: rasdaemon has nothing of this boot.
		reports: []string{"klog/offline"},
		sources: []string{"hostinfo ok", "rasdaemon ok", "klog ok", "edac-sysfs empty", "smbios ok"},
		// The kernel log of this boot does not say that a program asked
		// for a soft offline, and the four pages of the boot before are
		// known from the database alone: nothing marks them as asked for.
		finding: model.VerdictObserve, said: model.VerdictOK, loose: []string{"R-HOST-02"},
	},
	{
		// No EDAC driver and one virtual DIMM: the errors have addresses
		// and no DIMM. They are in the finding of no DIMM; the host finding
		// holds replacement for the uncorrected ones.
		dir: "7.3.0-mce", host: "lab-guest", virtual: "QEMU", ce: 7, ue: 3, gradeB: 10, hard: 3, soft: 2, noDIMM: 10,
		// The kernel log has no machine check record: a logged record is
		// not printed by default.
		reports: []string{"klog/offline", "rasdaemon/mce_record", "rasdaemon/memory_failure_event"},
		sources: []string{"hostinfo ok", "rasdaemon ok", "klog ok", "edac-sysfs empty", "smbios ok"},
		finding: model.VerdictHoldReplacement, said: model.VerdictOK, loose: []string{"R-HOST-01", "R-HOST-02"},
		notes: []string{"klog: the machine check injector (mce-inject) was set up in a boot of this log"},
	},
	{
		// With mce=print_all the kernel log holds every record too: each
		// error is in two sources and counts once.
		dir: "7.3.0-mce-print-all", host: "lab-guest", virtual: "QEMU", ce: 7, ue: 3, gradeB: 10, hard: 3, soft: 2, noDIMM: 10,
		reports: []string{"klog/mce", "klog/offline", "rasdaemon/mce_record", "rasdaemon/memory_failure_event"},
		sources: []string{"hostinfo ok", "rasdaemon ok", "klog ok", "edac-sysfs empty", "smbios ok"},
		finding: model.VerdictHoldReplacement, said: model.VerdictOK, loose: []string{"R-HOST-01", "R-HOST-02"},
	},
	{
		// The clock was set back by 10 seconds after rasdaemon started: its
		// times are ahead of the kernel's for the whole run.
		dir: "7.3.0-mce-clock-step", host: "lab-guest", virtual: "QEMU", ce: 7, ue: 3, gradeB: 10, hard: 3, soft: 2, noDIMM: 10,
		reports: []string{"klog/mce", "klog/offline", "rasdaemon/mce_record", "rasdaemon/memory_failure_event"},
		sources: []string{"hostinfo ok", "rasdaemon ok", "klog ok", "edac-sysfs empty", "smbios ok"},
		finding: model.VerdictHoldReplacement, said: model.VerdictOK, loose: []string{"R-HOST-01", "R-HOST-02"},
		notes: []string{"rasdaemon: 13 records are dated by the kernel's time"},
	},
	{
		// rasdaemon 1.0.0 built from source: 39 tables, more columns in
		// mce_record, and the signals the kernel sent in a table of their own.
		dir: "7.3.0-mce-rasdaemon-1.0", host: "lab-guest", virtual: "QEMU", ce: 7, ue: 3, gradeB: 10, hard: 3, soft: 2, noDIMM: 10,
		reports: []string{"klog/offline", "rasdaemon/mce_record", "rasdaemon/memory_failure_event"},
		sources: []string{"hostinfo ok", "rasdaemon ok", "klog ok", "edac-sysfs empty", "smbios ok"},
		finding: model.VerdictHoldReplacement, said: model.VerdictOK, loose: []string{"R-HOST-01", "R-HOST-02"},
		notes: []string{"rasdaemon: table signal_event: 3 rows not read"},
	},
	{
		// An emulated CXL memory device, recorded by rasdaemon 1.0.0. The
		// device is the DIMM: its serial number is its identity.
		dir: "7.3.0-cxl", host: "lab-guest", virtual: "QEMU", ce: 11, ue: 1, gradeA: 9, gradeB: 3,
		reports: []string{"rasdaemon/cxl_dram_event", "rasdaemon/cxl_general_media_event"},
		sources: []string{"hostinfo ok", "rasdaemon ok", "klog ok", "edac-sysfs empty", "smbios ok"},
		finding: model.VerdictReplaceNow,
		dimms: []dimm{{slot: "cxl:mem0", serial: "0x4c41420001", ce: 11, ue: 1, grade: model.VerdictReplaceNow, mode: model.ModeRow,
			rules: []string{"R-UE-01", "R-FW-01"}}},
	},
	{
		// Two emulated CXL devices under QEMU 11.1.3: errors in one row of
		// each, a soft PPR of that row through the kernel's EDAC interface,
		// then errors again in the repaired row of one of them. Each device
		// answers the PPR with a memory sparing record that leaves 2 spares.
		dir: "7.3.0-cxl-repair", host: "lab-guest", virtual: "QEMU", ce: 15, gradeA: 15,
		reports: []string{"rasdaemon/cxl_dram_event"},
		sources: []string{"hostinfo ok", "rasdaemon ok", "klog ok", "edac-sysfs empty", "smbios ok"},
		// The guest sees the files of the lab host. Its own scan read
		// pstore, mcelog and the crash dumps of the lab host as well, and
		// found a page taken offline in the kernel log of a crash of the
		// lab host three weeks before.
		live: []string{"hostinfo ok", "rasdaemon ok", "klog ok", "pstore empty", "pstore empty", "kdump ok", "edac-sysfs empty",
			"mcelog absent", "smbios ok"},
		before: state{hard: 1},
		finding: model.VerdictConsiderReplacement,
		dimms: []dimm{
			{slot: "cxl:mem0", serial: "0x4c41420012", ce: 9, grade: model.VerdictConsiderReplacement, mode: model.ModeRow,
				rules: []string{"R-REP-02"}, repair: model.ActionMemorySparing, before: 6, after: 3},
			{slot: "cxl:mem1", serial: "0x4c41420011", ce: 6, grade: model.VerdictObserve, mode: model.ModeRow,
				rules: []string{"R-SOFT-01"}, repair: model.ActionMemorySparing, before: 6, after: 0},
		},
	},
	{
		// Firmware error records on an aarch64 guest: every error is in
		// the rasdaemon database, in the firmware block of the kernel log
		// and in the EDAC line after it, and counts once. The kernel of
		// this guest traces no memory failure, so the two pages are in
		// the kernel log only.
		dir: "6.19.10-ghes", host: "lab-arm", virtual: "QEMU", journal: true, ce: 24, ue: 2, gradeA: 26, hard: 2,
		reports: []string{"klog/edac", "klog/ghes", "klog/offline", "rasdaemon/mc_event"},
		sources: []string{"hostinfo ok", "rasdaemon ok", "klog ok", "edac-sysfs ok", "smbios ok"},
		finding: model.VerdictReplaceNow,
		dimms: []dimm{{slot: "DIMM 0", serial: "LAB00A64", ce: 24, ue: 2, grade: model.VerdictReplaceNow, mode: model.ModeMultiRow,
			rules: []string{"R-UE-01", "R-SCRUB-01"}}},
		forms: &forms{events: 47, actions: 2, kernel: "6.19.10-300.fc44.aarch64", idle: 40 * time.Second},
	},
	{
		// The next boot of that guest. Its clock was set back by 10
		// seconds after rasdaemon started, and its database has no machine
		// check record that holds the kernel's time: the ledger learns
		// the step from the errors that the kernel log holds too. The
		// journal of the guest still had the boot before.
		dir: "6.19.10-ghes-clock-step", host: "lab-arm", virtual: "QEMU", journal: true, ce: 10, ue: 1, gradeA: 11, hard: 1,
		clock:   -10 * time.Second,
		before:  state{ce: 24, ue: 2, gradeA: 26, hard: 2},
		reports: []string{"klog/edac", "klog/ghes", "klog/offline", "rasdaemon/mc_event"},
		sources: []string{"hostinfo ok", "rasdaemon ok", "klog ok", "edac-sysfs ok", "smbios ok"},
		finding: model.VerdictReplaceNow,
		notes:   []string{"time-align: rasdaemon stamped 11 records of one boot"},
		dimms:   []dimm{{slot: "DIMM 0", serial: "LAB00A64", ce: 10, ue: 1, grade: model.VerdictReplaceNow, mode: model.ModeRow, rules: []string{"R-UE-01"}}},
	},
	{
		// Another guest with six DIMMs, booted three times a day apart. Four
		// DIMMs have a fault each, of another shape; two have nothing. The
		// kernel log of the copy is that of the third boot: the errors of
		// the first two are in the rasdaemon database alone.
		dir: "6.19.10-ghes-multi", host: "lab-arm6", virtual: "QEMU", journal: true, boots: 3, ce: 35, ue: 1, gradeA: 36, hard: 1,
		reports: []string{"klog/edac", "klog/ghes", "klog/offline", "rasdaemon/mc_event"},
		sources: []string{"hostinfo ok", "rasdaemon ok", "klog ok", "edac-sysfs ok", "smbios ok"},
		live:    []string{"hostinfo ok", "rasdaemon ok", "klog ok", "klog ok", "edac-sysfs ok", "smbios ok"},
		finding: model.VerdictConsiderReplacement,
		dimms: []dimm{
			{slot: "CPU0_A1", serial: "LABM0001", ce: 3, grade: model.VerdictObserve, mode: model.ModeSingleBit, rules: []string{"R-SOFT-01"}},
			{slot: "CPU0_B1", serial: "LABM0002", ce: 10, grade: model.VerdictConsiderReplacement, mode: model.ModeColumn, rules: []string{"R-ROW-03"}},
			{slot: "CPU0_D1", serial: "LABM0004", ce: 10, ue: 1, grade: model.VerdictConsiderReplacement, mode: model.ModeRow, rules: []string{"R-ROW-03", "R-SCRUB-01"}},
			{slot: "CPU0_E1", serial: "LABM0005", ce: 12, grade: model.VerdictConsiderReplacement, mode: model.ModeBank, rules: []string{"R-ROW-03"}},
		},
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
	return compareBeyond(t, c, s, host, 0, earlier)
}

// compareBeyond is compare for a ledger that holds more than the capture:
// errors and pages offline of an earlier boot.
func compareBeyond(t *testing.T, c capture, s *store.Store, host string, errors int64, earlier int) []string {
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
	if int64(res.UnplannedErrors) != errors || res.UnplannedOfflines != earlier {
		t.Errorf("beyond the plan: %d errors, %d pages offline (want %d and %d): %v", res.UnplannedErrors, res.UnplannedOfflines, errors, earlier, res.Unplanned)
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
			var moved time.Duration
			for _, cl := range res.Clocks {
				moved += cl.Offset
			}
			// The kernel log of a copy is dated through the boot time in
			// /proc/stat, which is a whole second.
			if (moved-c.clock).Abs() > time.Second || len(res.Clocks) > 1 || (c.clock == 0) != (len(res.Clocks) == 0) {
				t.Errorf("the ledger moved rasdaemon records: %+v, want by %s", res.Clocks, c.clock)
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
			if loose != c.noDIMM {
				t.Errorf("%d errors without a DIMM, want %d", loose, c.noDIMM)
			}
			// The host of every capture is a virtual machine, and the
			// ledger knows which hypervisor it runs under.
			hosts, err := s.Hosts(ctx)
			if err != nil || len(hosts) != 1 || hosts[0].Virtual != c.virtual || c.virtual == "" {
				t.Errorf("hypervisor of the host: %+v, %v; want %q", hosts, err, c.virtual)
			}
			// What names no DIMM has the host finding, and nothing else has one.
			v := out.Hosts[0].Loose
			switch {
			case len(c.loose) == 0:
				if v != nil {
					t.Errorf("host finding %v of a capture whose records all name a DIMM", v.Rules)
				}
			case v == nil:
				t.Errorf("no host finding, want %v", c.loose)
			default:
				asked := false
				for _, k := range v.CounterEvidence {
					asked = asked || k.Code == "check.host_asked"
				}
				if strings.Join(v.Rules, " ") != strings.Join(c.loose, " ") || asked != c.asked || v.NextAction.Code == "" {
					t.Errorf("host finding: rules %v, asked for by a program %v; want %v, %v", v.Rules, asked, c.loose, c.asked)
				}
				// The machine checks carry addresses: the statement names one.
				if c.ue > 0 && (len(v.Evidence) == 0 || v.Evidence[0].Code != "host.ue_located") {
					t.Errorf("first statement of the host finding: %+v", v.Evidence)
				}
			}
			listed := map[string]bool{}
			for _, want := range c.dimms {
				listed[want.slot] = true
				d := out.Hosts[0].InSlot(want.slot)
				if d == nil || d.Verdict == nil {
					t.Errorf("no finding for slot %q", want.slot)
					continue
				}
				modes := map[model.FaultMode]bool{}
				for _, f := range d.Faults {
					modes[f.Mode] = true
				}
				if d.DIMM.Serial != want.serial || d.Verdict.Grade != want.grade || !modes[want.mode] ||
					strings.Join(d.Verdict.Rules, " ") != strings.Join(want.rules, " ") {
					t.Errorf("slot %s: serial %q, finding %s, rules %v, fault modes %v\n want serial %q, %s, %v, a %s fault",
						want.slot, d.DIMM.Serial, d.Verdict.Grade, d.Verdict.Rules, modes, want.serial, want.grade, want.rules, want.mode)
				}
				if d.Features.CE != want.ce || d.Features.UE != want.ue {
					t.Errorf("slot %s: CE %d, UE %d; want %d, %d", want.slot, d.Features.CE, d.Features.UE, want.ce, want.ue)
				}
				var kinds []model.ActionKind
				var before, after int64
				for _, f := range d.Faults {
					for _, m := range f.Mitigations {
						if m.Kind == model.ActionPageOffline {
							continue
						}
						kinds = append(kinds, m.Kind)
						before += int64(f.ErrorCount) - m.FaultErrorsAfter
						after += m.ErrorsAfter
					}
				}
				wantKinds := []model.ActionKind(nil)
				if want.repair != "" {
					wantKinds = []model.ActionKind{want.repair}
				}
				if !slices.Equal(kinds, wantKinds) || before != want.before || after != want.after {
					t.Errorf("slot %s: repairs %v, %d errors before, %d after; want %v, %d, %d", want.slot, kinds, before, after, wantKinds, want.before, want.after)
				}
			}
			// The DIMMs that were left alone have nothing.
			for _, d := range out.Hosts[0].DIMMs {
				if !d.Present || listed[d.Slot] {
					continue
				}
				if d.Features.CE != 0 || d.Features.UE != 0 || len(d.Faults) != 0 || (d.Verdict != nil && d.Verdict.Grade != model.VerdictOK) {
					t.Errorf("slot %s: CE %d, UE %d, %d faults, finding %+v; want nothing", d.Slot, d.Features.CE, d.Features.UE, len(d.Faults), d.Verdict)
				}
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

// TestEveryBootOfTheJournal reads the kernel messages of every boot the
// journal of a host held, alone: a journal export of a host that was
// restarted between its errors holds them all, each boot under its own
// banner.
func TestEveryBootOfTheJournal(t *testing.T) {
	for _, c := range captures {
		if c.boots == 0 {
			continue
		}
		t.Run(c.dir, func(t *testing.T) {
			at := capturedAt(t, c)
			as, what, err := scan.Detect(ctx, dir(t, c, "journal-all.json"), scan.DetectOptions{Now: at, CapturedAt: at})
			if err != nil || what != scan.WhatJournal {
				t.Fatalf("detected as %q, %v", what, err)
			}
			s := ledger(t)
			res, err := ingest.Run(ctx, s, as, ingest.Options{Now: at})
			if err != nil {
				t.Fatal(err)
			}
			if res.Host != c.host {
				t.Errorf("host %q, want %q", res.Host, c.host)
			}
			if got, want := stateOf(t, s, res.Host), (state{ce: c.ce, ue: c.ue, gradeA: c.gradeA, gradeB: c.gradeB, gradeC: c.gradeC, hard: c.hard, soft: c.soft}); got != want {
				t.Errorf("ledger: %+v\n  want: %+v", got, want)
			}
			compare(t, c, s, res.Host, 0)
			boots, err := s.Boots(ctx, res.Host)
			if err != nil || len(boots) != c.boots {
				t.Fatalf("%d boots, want %d (%v)", len(boots), c.boots, err)
			}
			// Every boot started on a day of its own and holds errors.
			events, err := s.EventList(ctx, store.EventFilter{Host: res.Host, Kinds: []model.EventKind{model.KindError}})
			if err != nil {
				t.Fatal(err)
			}
			perBoot := map[string]int{}
			for _, e := range events {
				perBoot[e.BootID]++
			}
			days := map[string]bool{}
			for _, b := range boots {
				days[b.StartedAt.UTC().Format(time.DateOnly)] = true
				if perBoot[b.BootID] == 0 || b.Kernel == "" {
					t.Errorf("boot %s of %s, kernel %q: %d errors", b.BootID, b.StartedAt.Format(time.RFC3339), b.Kernel, perBoot[b.BootID])
				}
			}
			if len(days) != c.boots || perBoot[""] != 0 {
				t.Errorf("boots on %d days, %d errors outside every boot", len(days), perBoot[""])
			}
		})
	}
}

// TestKernelLogForms reads the kernel log of one boot in every form the
// host can print it: dmesg with its time formats, a read of /dev/kmsg, and
// the text forms of journalctl. Each form alone must give what the plan
// holds, the kernel of the boot, and times near the injections. In the
// capture with firmware error records every error spans a dozen lines, in
// each of the framings.
func TestKernelLogForms(t *testing.T) {
	for _, c := range captures {
		if c.forms == nil {
			continue
		}
		at := capturedAt(t, c)
		p := plan(t, c)
		first, last := p.Injections[0].Time, p.Injections[0].Time
		for _, in := range p.Injections {
			if !in.Time.IsZero() && in.Time.Before(first) {
				first = in.Time
			}
			if in.Time.After(last) {
				last = in.Time
			}
		}
		forms := []struct {
			file string
			host string // the journal names the host, dmesg does not
			// slack is how far the time of a record may lie outside the
			// span of the injections: the forms without dates are placed
			// by the capture time or by the hardware clock, which ran
			// ahead on the lab host.
			slack time.Duration
		}{
			{"dmesg-T.txt", "localhost", 3 * time.Second},
			{"dmesg-iso.txt", "localhost", 3 * time.Second},
			{"dmesg-raw.txt", "localhost", 15*time.Second + c.forms.idle},
			{"kmsg.txt", "localhost", 15*time.Second + c.forms.idle},
			{"journal-short.txt", c.host, 3 * time.Second},
			{"journal-short-iso.txt", c.host, 3 * time.Second},
			{"journal-short-monotonic.txt", c.host, 15*time.Second + c.forms.idle},
		}
		entries, err := os.ReadDir(dir(t, c, "formats"))
		if err != nil || len(entries) != len(forms) {
			t.Fatalf("%s: formats holds %d files, the test knows %d (%v)", c.dir, len(entries), len(forms), err)
		}
		for _, f := range forms {
			t.Run(c.dir+"/"+f.file, func(t *testing.T) {
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
				if src := res.Sources[0]; src.Status != model.SourceOK || src.Actions != c.forms.actions || src.Events != c.forms.events {
					t.Errorf("source: %s, %d events, %d actions (want %d, %d), notes %q", src.Status, src.Events, src.Actions,
						c.forms.events, c.forms.actions, src.Notes)
				}
				if c.forms.note != "" && !hasNote(res.Sources, c.forms.note) {
					t.Errorf("no note %q: %q", c.forms.note, res.Sources[0].Notes)
				}
				if got, want := stateOf(t, s, res.Host), (state{ce: c.ce, ue: c.ue, gradeA: c.gradeA, gradeB: c.gradeB, gradeC: c.gradeC, hard: c.hard, soft: c.soft}); got != want {
					t.Errorf("ledger: %+v\n  want: %+v", got, want)
				}
				compare(t, c, s, res.Host, 0)
				boots, err := s.Boots(ctx, res.Host)
				if err != nil || len(boots) != 1 || boots[0].Kernel != c.forms.kernel {
					t.Errorf("boots: %+v, %v", boots, err)
				}
				lo, hi := first.Add(-f.slack), last.Add(f.slack+10*time.Second)
				events, err := s.EventList(ctx, store.EventFilter{Host: res.Host, WithDuplicates: true})
				if err != nil {
					t.Fatal(err)
				}
				for _, e := range events {
					if e.ErrorDT.Before(lo) || e.ErrorDT.After(hi) {
						t.Errorf("%s is dated %s (%s), outside %s to %s", e.RawRef, e.ErrorDT.Format(time.RFC3339), e.TimeQuality,
							lo.Format(time.RFC3339), hi.Format(time.RFC3339))
						break
					}
				}
				actions, err := s.Actions(ctx, store.ActionFilter{Host: res.Host})
				if err != nil {
					t.Fatal(err)
				}
				for _, a := range actions {
					if a.At.Before(lo) || a.At.After(hi) {
						t.Errorf("%s is dated %s (%s), outside %s to %s", a.RawRef, a.At.Format(time.RFC3339), a.TimeQuality,
							lo.Format(time.RFC3339), hi.Format(time.RFC3339))
						break
					}
				}
			})
		}
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
			all, err := s.EventList(ctx, store.EventFilter{Host: res.Host})
			if err != nil {
				t.Fatal(err)
			}
			// The error, and the kernel's lines that it stops the host.
			var events, stops []*model.Event
			for _, e := range all {
				if e.ErrorType == model.ErrorTypeStop {
					stops = append(stops, e)
				} else {
					events = append(events, e)
				}
			}
			if len(events) != 1 || len(stops) != 1 {
				t.Fatalf("%d events, %d stops", len(events), len(stops))
			}
			e := events[0]
			if st := stops[0]; st.ErrorSeverity != model.SevInformational || st.CauseFingerprint != e.Fingerprint || st.Source != "klog/stop" {
				t.Errorf("stop: %+v", *st)
			}
			boots, err := s.Boots(ctx, res.Host)
			if err != nil || len(boots) != 1 || boots[0].End != model.BootStop || boots[0].EndRef != "event:"+strconv.FormatInt(e.ID, 10) {
				t.Errorf("boots %+v, %v", boots, err)
			}
			// The record carries its own time, so the log needs no date.
			if e.ErrorSeverity != c.severity || !e.OsPhysicalAddress.Valid || e.OsPhysicalAddress.V != c.address || e.Grade != model.GradeB ||
				e.TimeQuality != model.TimeExact || e.ErrorDT.Sub(p.Injections[0].Time).Abs() > time.Minute {
				t.Errorf("event: %+v", *e)
			}
			out, err := engine.Run(ctx, s, engine.Options{Now: e.ErrorDT.Add(time.Minute)})
			if err != nil || len(out.Hosts) != 1 {
				t.Fatalf("engine: %v", err)
			}
			// The host finding says that this error stopped the host.
			stated := false
			if v := out.Hosts[0].Loose; v != nil {
				for _, ev := range v.Evidence {
					stated = stated || ev.Code == "ue.stopped"
				}
			}
			if !stated {
				t.Errorf("no finding says that the error stopped the host: %+v", out.Hosts[0].Loose)
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
			ce, ue := c.ce+c.before.ce, c.ue+c.before.ue
			// What was printed then, which for errors that name no DIMM
			// is not the finding of today.
			said := c.finding
			if c.said != "" {
				said = c.said
			}
			if live.Data.Host.CE != ce || live.Data.Host.UE != ue || live.Data.Host.Grade != string(said) || live.ExitCode != said.ExitCode() {
				t.Errorf("live: CE %d, UE %d, finding %s, exit code %d; want %d, %d, %s", live.Data.Host.CE, live.Data.Host.UE,
					live.Data.Host.Grade, live.ExitCode, ce, ue, said)
			}
			var got []string
			for _, s := range live.Data.Sources {
				if s.Kind == ingest.SlotMapKind || s.Kind == ingest.TimeAlignKind {
					continue
				}
				got = append(got, s.Kind+" "+s.Status)
			}
			want := c.sources
			if c.live != nil {
				want = c.live
			}
			if strings.Join(got, ", ") != strings.Join(want, ", ") {
				t.Errorf("live sources: %s\n        want: %s", strings.Join(got, ", "), strings.Join(want, ", "))
			}
			if exit, _ := os.ReadFile(dir(t, c, "my", "scan.exit")); strings.TrimSpace(string(exit)) != "exit="+strconv.Itoa(said.ExitCode()) {
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
			want := c.want()
			want.ce, want.ue = want.ce+c.before.ce, want.ue+c.before.ue
			want.gradeA, want.gradeB, want.gradeC = want.gradeA+c.before.gradeA, want.gradeB+c.before.gradeB, want.gradeC+c.before.gradeC
			want.hard, want.soft = want.hard+c.before.hard, want.soft+c.before.soft
			if m.Hosts[0].CE != want.ce || m.Hosts[0].UE != want.ue {
				t.Errorf("manifest: CE %d, UE %d; want %d, %d", m.Hosts[0].CE, m.Hosts[0].UE, want.ce, want.ue)
			}
			s, err := store.Open(ctx, b.Ledger, store.ReadOnly)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			// On the host the journal of every boot was read, so the pages
			// of an earlier boot are in the bundle through both sources.
			if got := stateOf(t, s, m.Hosts[0].Name); got != want {
				t.Errorf("ledger of the bundle: %+v\n               want: %+v", got, want)
			}
			compareBeyond(t, c, s, m.Hosts[0].Name, c.before.ce+c.before.ue, c.earlier+c.before.hard+c.before.soft)
			// Nothing in it names the host. The SMBIOS product name is
			// kept by design, and the aarch64 guest was given its host
			// name as product name: there the name is looked for in the
			// host table only.
			hosts, err := s.Hosts(ctx)
			if err != nil || len(hosts) != 1 || hosts[0].Name != m.Hosts[0].Name {
				t.Fatalf("hosts of the bundle: %+v, %v", hosts, err)
			}
			if hosts[0].SystemProduct != c.host {
				raw, _ := os.ReadFile(b.Ledger)
				if strings.Contains(string(raw), c.host) {
					t.Errorf("the ledger of the bundle holds %q", c.host)
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
	for _, l := range loops {
		known[l.dir] = true
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
