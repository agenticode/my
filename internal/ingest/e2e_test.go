package ingest

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/fmfm"
	"github.com/agenticode/my/internal/adapter/klog"
	"github.com/agenticode/my/internal/adapter/nvidia"
	"github.com/agenticode/my/internal/adapter/rasdaemon"
	"github.com/agenticode/my/internal/adapter/redfish"
	"github.com/agenticode/my/internal/adapter/sel"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/testutil"
)

// findings runs the engine on a ledger and returns the finding per slot.
func findings(t *testing.T, res *engine.Result, host string) map[string]*model.Verdict {
	t.Helper()
	h := res.Host(host)
	if h == nil {
		t.Fatalf("no result for host %s", host)
	}
	out := map[string]*model.Verdict{}
	for _, d := range h.DIMMs {
		out[d.Slot] = d.Verdict
	}
	return out
}

// TestBMCDumpReachesTheRules reads what a BMC serves and nothing else: no
// host log, no SMBIOS. The module the BMC reports as failed and out of use
// must come out with the rule about firmware marks, tied to its serial
// number.
func TestBMCDumpReachesTheRules(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	dump := redfish.New(testutil.Testdata(t, "redfish", "bmc-tree"), redfish.Options{Now: now})
	run, err := Run(ctx, s, []adapter.Adapter{dump}, Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	// The host name comes from the system resource of the dump.
	if run.Host != "r12-n034" {
		t.Errorf("host = %q, want the name in the dump", run.Host)
	}
	src := run.Sources[0]
	// The end of the sparing state is kept as information at its place
	// (grade B); the RAS mode in effect is a firmware record alone.
	if src.Kind != "redfish" || src.Status != model.SourceOK || src.Events != 7 || src.Actions != 3 || src.GradeB != 4 || src.GradeC != 3 {
		t.Errorf("source inventory entry = %+v", src)
	}

	res, err := engine.Run(ctx, s, engine.Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := findings(t, res, "r12-n034")
	b1 := got["DIMM_B1"]
	if b1 == nil {
		t.Fatalf("no finding for DIMM_B1; slots: %v", got)
	}
	for _, rule := range []string{engine.RuleFirmware, engine.RuleUE, engine.RuleRepairGone} {
		if !slices.Contains(b1.Rules, rule) {
			t.Errorf("DIMM_B1: rules %v lack %s", b1.Rules, rule)
		}
	}
	if b1.Grade != model.VerdictReplaceNow {
		t.Errorf("DIMM_B1: grade %s", b1.Grade)
	}
	d, err := s.DIMM(ctx, b1.DIMMUID)
	if err != nil || d == nil || d.Serial != "80A1C3" || d.MemoryType != "DDR5" {
		t.Errorf("DIMM of the finding = %+v, %v", d, err)
	}
	// The other modules carry no firmware mark.
	for _, slot := range []string{"DIMM_A1", "DIMM_C1"} {
		v := got[slot]
		if v == nil {
			t.Errorf("no finding for %s", slot)
			continue
		}
		if slices.Contains(v.Rules, engine.RuleFirmware) || v.Grade == model.VerdictReplaceNow {
			t.Errorf("%s: grade %s, rules %v", slot, v.Grade, v.Rules)
		}
	}
}

// TestSELDisableReachesTheRules does the same for a SEL dump: the module
// the firmware took out of use after an uncorrected error.
func TestSELDisableReachesTheRules(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	log := sel.NewFile(testutil.Testdata(t, "sel", "ipmitool-elist.txt"), sel.Options{})
	if _, err := Run(ctx, s, []adapter.Adapter{log}, Options{Host: "h1", Now: now}); err != nil {
		t.Fatal(err)
	}
	res, err := engine.Run(ctx, s, engine.Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := findings(t, res, "h1")
	c1 := got["DIMM_C1"]
	if c1 == nil || !slices.Contains(c1.Rules, engine.RuleFirmware) || !slices.Contains(c1.Rules, engine.RuleUE) || c1.Grade != model.VerdictReplaceNow {
		t.Errorf("DIMM_C1 = %+v", c1)
	}
	// DIMM_G1 was taken out of use and came back: no firmware mark stands.
	if g1 := got["DIMM_G1"]; g1 != nil && slices.Contains(g1.Rules, engine.RuleFirmware) {
		t.Errorf("DIMM_G1: rules %v", g1.Rules)
	}
	// The spare that took over for DIMM_F1 is a repair resource used up.
	if f1 := got["DIMM_F1"]; f1 == nil || !slices.Contains(f1.Rules, engine.RuleRepairGone) {
		t.Errorf("DIMM_F1 = %+v", f1)
	}
}

// TestGPUInputsReachTheRules reads the Xid lines of a kernel log and a
// snapshot of nvidia-smi. The Xid line after a reboot names its GPU by the
// PCI address only; the snapshot says which GPU that is, and the finding
// about the failed remap is under the serial number.
func TestGPUInputsReachTheRules(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	adapters := []adapter.Adapter{
		klog.NewFile(testutil.Testdata(t, "klog", "nvidia-xid.messages"), klog.Options{}),
		nvidia.NewFile(testutil.Testdata(t, "nvidia", "nvidia-smi-q.xml"), nvidia.Options{}),
	}
	run, err := Run(ctx, s, adapters, Options{Host: "gpu-01", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if run.Samples != 5 || run.Sources[0].Samples != 3 || run.Sources[1].Samples != 2 || run.Sources[1].Kind != "nvidia-smi" {
		t.Errorf("samples: run %d, sources %+v", run.Samples, run.Sources)
	}
	gpus, err := s.GPUMem(ctx, "gpu-01")
	if err != nil || len(gpus) != 5 {
		t.Fatalf("GPU records = %d, %v", len(gpus), err)
	}
	for _, g := range gpus {
		if g.PCIBus == "0000:3b:00.0" && (g.GPUSerial != "1650000000412" || g.GPUUUID != "GPU-b850f46d-d5ea-c752-ddf3-c4453e44d3f7") {
			t.Errorf("record %s of the GPU at 3b:00.0 has serial %q, UUID %q", g.RawRef, g.GPUSerial, g.GPUUUID)
		}
		if g.Kind == model.GPUXID && !g.GPUIndex.Valid {
			t.Errorf("Xid record %s did not take the index of its GPU", g.RawRef)
		}
	}

	res, err := engine.Run(ctx, s, engine.Options{})
	if err != nil {
		t.Fatal(err)
	}
	h := res.Host("gpu-01")
	if h == nil {
		t.Fatal("no result for the host")
	}
	got := map[string][]string{}
	for _, g := range h.GPUs {
		got[g.Key] = g.Verdict.Rules
		if g.Verdict.Grade != model.VerdictReplaceNow {
			t.Errorf("GPU %s: grade %s", g.Key, g.Verdict.Grade)
		}
	}
	// One GPU logged Xid 64, the other reports a failed remap in its state.
	for _, serial := range []string{"1650000000412", "1650000000977"} {
		if !slices.Contains(got[serial], engine.RuleRepairGone) {
			t.Errorf("GPU %s: rules %v", serial, got[serial])
		}
	}
	if len(got) != 2 {
		t.Errorf("findings for GPUs %v, want the two serial numbers", got)
	}
}

// TestCXLDeviceReachesTheRules reads a rasdaemon database with CXL tables:
// the device that asks to be replaced and had an uncorrected error comes
// out with the rules about firmware marks and uncorrected errors, under
// its serial number.
func TestCXLDeviceReachesTheRules(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	db := testutil.BuildSQLite(t, testutil.Testdata(t, "rasdaemon", "cxl-1.0.sql"), "ras-mc_event.db")
	run, err := Run(ctx, s, []adapter.Adapter{rasdaemon.New(db, rasdaemon.Options{Direct: true})}, Options{Host: "cxl-01", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if src := run.Sources[0]; src.Events != 11 || src.Actions != 3 || src.GradeA != 1 {
		t.Errorf("source inventory entry = %+v", src)
	}
	res, err := engine.Run(ctx, s, engine.Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := findings(t, res, "cxl-01")
	mem1 := got["cxl:mem1"]
	if mem1 == nil {
		t.Fatalf("no finding for cxl:mem1; slots: %v", got)
	}
	// Its uncorrected error was found by the device's own scrub.
	for _, rule := range []string{engine.RuleFirmware, engine.RuleScrub} {
		if !slices.Contains(mem1.Rules, rule) {
			t.Errorf("cxl:mem1: rules %v lack %s", mem1.Rules, rule)
		}
	}
	d, err := s.DIMM(ctx, mem1.DIMMUID)
	if err != nil || d == nil || d.Serial != "0x5678" || d.Identity != model.IdentitySerial || d.Technology != "CXL" {
		t.Errorf("identity of cxl:mem1 = %+v, %v", d, err)
	}
	// mem0 had one corrected error and a row spared for good: the spare is
	// taken. No firmware mark, no uncorrected error.
	mem0 := got["cxl:mem0"]
	if mem0 == nil || !slices.Contains(mem0.Rules, engine.RuleRepairGone) || slices.Contains(mem0.Rules, engine.RuleFirmware) || slices.Contains(mem0.Rules, engine.RuleUE) {
		t.Errorf("cxl:mem0 = %+v", mem0)
	}
	// Informational records are in the ledger and in no total.
	totals, err := s.Totals(ctx, store.EventFilter{Host: "cxl-01"})
	if err != nil {
		t.Fatal(err)
	}
	var ce, ue int64
	for _, x := range totals {
		ce, ue = ce+x.CE, ue+x.UE
	}
	if ce != 1 || ue != 1 {
		t.Errorf("totals: %d corrected, %d uncorrected; want 1 and 1", ce, ue)
	}
}

// TestFMFMLogsReachTheLedger reads the three FMFM logs of a host. The
// runtime log names the module of its errors by socket, channel and slot
// number; the boot log lists the module at those numbers with its serial
// number. Errors and ECS readings come out under that serial number, and a
// finding about the module no longer misses its ECS counts.
func TestFMFMLogsReachTheLedger(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	adapters := []adapter.Adapter{
		// The runtime log first: its places are not named yet.
		fmfm.NewFile(testutil.Testdata(t, "fmfm", "runtime.json"), fmfm.Options{Now: now}),
		fmfm.NewFile(testutil.Testdata(t, "fmfm", "crash.json"), fmfm.Options{Now: now}),
		fmfm.NewFile(testutil.Testdata(t, "fmfm", "boot.json"), fmfm.Options{Now: now}),
	}
	run, err := Run(ctx, s, adapters, Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if run.Host != "r12-n034" {
		t.Errorf("host = %q, want the HostID of the log", run.Host)
	}
	if src := run.Sources[0]; src.Kind != "fmfm" || src.Events != 6 || src.Samples != 6 || src.GradeA != 2 || src.GradeB != 2 || src.GradeC != 2 {
		t.Errorf("runtime log entry = %+v", src)
	}

	serialOf := func(uid string) string {
		if uid == "" {
			return ""
		}
		d, err := s.DIMM(ctx, uid)
		if err != nil || d == nil {
			t.Fatalf("dimm %s: %v", uid, err)
		}
		if d.Identity == model.IdentitySlot {
			return "(slot)"
		}
		return d.Serial
	}
	totals := map[string][2]int64{}
	ts, err := s.Totals(ctx, store.EventFilter{Host: "r12-n034"})
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range ts {
		totals[x.Slot+"/"+serialOf(x.DIMMUID)] = [2]int64{x.CE, x.UE}
	}
	want := map[string][2]int64{
		"CPU0_C1/80A1C3":                {2, 2}, // two corrected errors, one uncorrected, one fatal at the crash
		"socket0 channel3 dimm0/80A1F7": {1, 0}, // listed by numbers only
		"socket1 channel0 dimm0/(slot)": {0, 1}, // not in the boot log: named by the numbers
	}
	if !reflect.DeepEqual(totals, want) {
		t.Errorf("totals = %v\nwant %v", totals, want)
	}

	readings, err := s.ECS(ctx, store.ECSFilter{Host: "r12-n034"})
	if err != nil || len(readings) != 6 {
		t.Fatalf("%d ECS readings, %v", len(readings), err)
	}
	bySlot := map[string]int{}
	for _, e := range readings {
		bySlot[e.Slot+"/"+serialOf(e.DIMMUID)]++
	}
	wantECS := map[string]int{"CPU0_C1/80A1C3": 2, "socket0 channel3 dimm0/80A1F7": 1, "CPU1_D1/(slot)": 1, "socket1 channel0 dimm0/(slot)": 2}
	if !reflect.DeepEqual(bySlot, wantECS) {
		t.Errorf("ECS readings per slot = %v\nwant %v", bySlot, wantECS)
	}

	res, err := engine.Run(ctx, s, engine.Options{})
	if err != nil {
		t.Fatal(err)
	}
	c1 := findings(t, res, "r12-n034")["CPU0_C1"]
	if c1 == nil || !slices.Contains(c1.Rules, engine.RuleUE) || c1.Grade != model.VerdictReplaceNow {
		t.Fatalf("CPU0_C1 = %+v", c1)
	}
	for _, m := range c1.Missing {
		if m.Code == "missing.ecs" {
			t.Errorf("the finding misses ECS counts although the ledger has readings: %+v", c1.Missing)
		}
	}
}
