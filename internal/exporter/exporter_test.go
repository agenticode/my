package exporter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/klog"
	"github.com/agenticode/my/internal/adapter/nvidia"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/testledger"
	"github.com/agenticode/my/internal/testutil"
)

func TestMain(m *testing.M) { testledger.Main(m) }

var ctx = context.Background()

// fixed is the time findings are stamped with in these tests.
var fixed = time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)

// text writes a set and checks that a Prometheus server would take it.
func text(t *testing.T, set *Set) (string, *testutil.Exposition) {
	t.Helper()
	var b bytes.Buffer
	n, err := set.WriteTo(&b)
	if err != nil || n != int64(b.Len()) {
		t.Fatalf("WriteTo = %d, %v; wrote %d bytes", n, err, b.Len())
	}
	return b.String(), testutil.MustParseExposition(t, b.String())
}

// hostSet runs the engine on a ledger and returns the metrics of its host.
func hostSet(t *testing.T, s *store.Store) (*Set, *engine.HostResult) {
	t.Helper()
	res, err := engine.Run(ctx, s, engine.Options{Now: fixed})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hosts) != 1 {
		t.Fatalf("%d hosts in the ledger", len(res.Hosts))
	}
	set, err := Host(ctx, s, res.Hosts[0])
	if err != nil {
		t.Fatal(err)
	}
	return set, res.Hosts[0]
}

func TestWriteTo(t *testing.T) {
	set := &Set{}
	set.Add(DIMMVerdict, 3, "r12-n034", "CPU0_C1", "3B9C56")
	set.Add(DIMMVerdict, 0, "r12-n034", "CPU0_A1", "3B9C01")
	// A label value with every character the format escapes.
	set.Add(DIMMVerdict, 1, "r12-n034", "DIMM \"A1\"\\B\nC", "")
	set.Add(DIMMCorrected, 1231, "r12-n034", "CPU0_C1", "3B9C56")
	set.Add(LastScan, 1763026200.25)
	set.Add(ScanFailures, 0)
	got, _ := text(t, set)
	want := `# HELP my_dimm_verdict Finding for the DIMM in the slot: 0 ok, 1 observe, 2 consider replacement, 3 replace now, 4 hold replacement.
# TYPE my_dimm_verdict gauge
my_dimm_verdict{host="r12-n034",slot="CPU0_A1",serial="3B9C01"} 0
my_dimm_verdict{host="r12-n034",slot="CPU0_C1",serial="3B9C56"} 3
my_dimm_verdict{host="r12-n034",slot="DIMM \"A1\"\\B\nC",serial=""} 1
# HELP my_dimm_ce_total Corrected errors on record for the DIMM, counted from error records. An empty serial counts errors of the slot that could not be tied to a DIMM.
# TYPE my_dimm_ce_total counter
my_dimm_ce_total{host="r12-n034",slot="CPU0_C1",serial="3B9C56"} 1231
# HELP my_exporter_last_scan_timestamp_seconds When the scan behind these metrics finished, in seconds since the epoch.
# TYPE my_exporter_last_scan_timestamp_seconds gauge
my_exporter_last_scan_timestamp_seconds 1763026200.25
# HELP my_exporter_scan_failures_total Scans that failed since the exporter started. The metrics stay those of the last scan that succeeded.
# TYPE my_exporter_scan_failures_total counter
my_exporter_scan_failures_total 0
`
	if got != want {
		t.Errorf("exposition:\n%s\nwant:\n%s", got, want)
	}

	// An empty set writes nothing, and that is valid too.
	if got, _ := text(t, &Set{}); got != "" {
		t.Errorf("empty set wrote %q", got)
	}
}

// TestEqualSeriesBecomeOne guards the rule that a scrape with a series
// twice is refused as a whole.
func TestEqualSeriesBecomeOne(t *testing.T) {
	set := &Set{}
	set.Add(DIMMCorrected, 5, "h", "A1", "S1")
	set.Add(DIMMCorrected, 7, "h", "A1", "S1")
	set.Add(DIMMVerdict, 1, "h", "A1", "S1")
	set.Add(DIMMVerdict, 3, "h", "A1", "S1")
	set.Add(DIMMVerdict, 2, "h", "A1", "S1")
	// Bytes that are not UTF-8 would make the whole scrape unreadable.
	set.Add(DIMMOfflinedPages, 2, "h", "A\xff1")
	_, x := text(t, set)
	for series, want := range map[string]float64{
		`my_dimm_ce_total{host="h",slot="A1",serial="S1"}`:     12,
		`my_dimm_verdict{host="h",slot="A1",serial="S1"}`:      3,
		"my_dimm_offlined_pages{host=\"h\",slot=\"A\uFFFD1\"}": 2,
	} {
		if got, ok := x.Series[series]; !ok || got != want {
			t.Errorf("%s = %v (present %v), want %v", series, got, ok, want)
		}
	}
	if set.Series() != 3 {
		t.Errorf("Series() = %d, want 3", set.Series())
	}

	other := &Set{}
	other.Add(DIMMCorrected, 1, "h2", "A1", "S9")
	other.Add(DIMMCorrected, 1, "h", "A1", "S1")
	set.Merge(other)
	set.Merge(nil)
	if v, ok := set.Value(DIMMCorrected, "h", "A1", "S1"); !ok || v != 13 {
		t.Errorf("after Merge: %v, %v", v, ok)
	}
	if _, ok := set.Value(DIMMCorrected, "h3", "A1", "S1"); ok {
		t.Error("Value reports a series that is not there")
	}
	if _, ok := set.Value(GPURemapFailure, "h", "x"); ok {
		t.Error("Value reports a series of a metric without samples")
	}
}

type failWriter struct{ after int }

func (f *failWriter) Write(p []byte) (int, error) {
	if f.after <= 0 {
		return 0, errors.New("closed")
	}
	f.after--
	return len(p), nil
}

func TestWriteToReportsAFailedWrite(t *testing.T) {
	set := &Set{}
	set.Add(ScanFailures, 1)
	if _, err := set.WriteTo(&failWriter{}); err == nil {
		t.Error("no error from a writer that fails")
	}
}

func TestFormatValue(t *testing.T) {
	inf := math.Inf(1)
	for v, want := range map[float64]string{0: "0", 3: "3", 1231: "1231", 0.5: "0.5", 1e15: "1000000000000000", 1763026200.125: "1763026200.125", -1: "-1", inf: "+Inf", -inf: "-Inf"} {
		if got := formatValue(v); got != want {
			t.Errorf("formatValue(%v) = %q, want %q", v, got, want)
		}
	}
	if got := formatValue(math.NaN()); got != "NaN" {
		t.Errorf("formatValue(NaN) = %q", got)
	}
}

// TestDescs checks the metric definitions: names, types and help texts a
// Prometheus server accepts, every metric listed once, and no label that
// could carry an address.
func TestDescs(t *testing.T) {
	name := regexp.MustCompile(`^my_[a-z0-9_]+$`)
	label := regexp.MustCompile(`^[a-z_]+$`)
	// The labels the design allows. A row, a column, a bank or an address
	// as a label would make the number of series grow without bound.
	allowed := map[string]bool{"host": true, "slot": true, "serial": true, "mode": true, "type": true, "gpu": true, "bucket": true,
		"source": true, "status": true, "version": true, "revision": true, "engine": true}
	seen := map[string]bool{}
	for _, d := range Descs {
		if !name.MatchString(d.Name) || seen[d.Name] {
			t.Errorf("metric name %q is malformed or listed twice", d.Name)
		}
		seen[d.Name] = true
		if d.Type != Gauge && d.Type != Counter {
			t.Errorf("%s: type %q", d.Name, d.Type)
		}
		if (d.Type == Counter) != strings.HasSuffix(d.Name, "_total") {
			t.Errorf("%s: a counter, and only a counter, ends in _total", d.Name)
		}
		if d.Help == "" || !strings.HasSuffix(d.Help, ".") || strings.ContainsAny(d.Help, "\n\\") {
			t.Errorf("%s: help %q", d.Name, d.Help)
		}
		for _, l := range d.Labels {
			if !label.MatchString(l) || !allowed[l] {
				t.Errorf("%s: label %q is not one of the labels the metrics may carry", d.Name, l)
			}
		}
	}
	for _, want := range []string{"my_dimm_verdict", "my_dimm_ce_total", "my_dimm_ue_total", "my_dimm_faults", "my_dimm_offlined_pages",
		"my_dimm_repairs_total", "my_gpu_remap_banks", "my_gpu_remap_failure", "my_source_grade"} {
		if !seen[want] {
			t.Errorf("metric %s is not defined", want)
		}
	}
}

// TestHostDemo pins the metrics of the demo host, read the way a scan of
// an Intel host reads it.
func TestHostDemo(t *testing.T) {
	set, h := hostSet(t, testledger.Open(t, "demo", 1, "intel"))
	got, x := text(t, set)
	testutil.Golden(t, "demo-intel.prom", []byte(got))

	host := h.Host
	for series, want := range map[string]float64{
		`my_dimm_verdict{host="` + host + `",slot="CPU0_C1",serial="3B9C56"}`:                         3,
		`my_dimm_verdict{host="` + host + `",slot="CPU1_D1",serial="2AEB41"}`:                         1,
		`my_dimm_ce_total{host="` + host + `",slot="CPU0_C1",serial="3B9C56"}`:                        1231,
		`my_dimm_ue_total{host="` + host + `",slot="CPU0_C1",serial="3B9C56"}`:                        1,
		`my_dimm_ce_total{host="` + host + `",slot="CPU1_D1",serial="2AEB41"}`:                        53,
		`my_dimm_faults{host="` + host + `",slot="CPU0_C1",mode="row"}`:                               1,
		`my_source_grade{host="` + host + `",source="rasdaemon",status="ok"}`:                         3,
		`my_source_grade{host="` + host + `",source="smbios",status="ok"}`:                            0,
		`my_dimm_offlined_pages{host="` + host + `",slot="CPU0_A1"}`:                                  0,
		`my_dimm_offlined_pages{host="` + host + `",slot="CPU0_C1"}`:                                  float64(h.InSlot("CPU0_C1").Features.Offline.Pages),
		`my_dimm_ue_total{host="` + host + `",slot="CPU1_D1",serial="2AEB41"}`:                        0,
		`my_source_grade{host="` + host + `",source="edac-sysfs",status="ok"}`:                        1,
		`my_source_grade{host="` + host + `",source="hostinfo",status="ok"}`:                          0,
		`my_dimm_faults{host="` + host + `",slot="CPU1_D1",mode="single-bit"}`:                        1,
		`my_dimm_verdict{host="` + host + `",slot="CPU0_A1",serial="` + serialOf(h, "CPU0_A1") + `"}`: 0,
	} {
		if got, ok := x.Series[series]; !ok || got != want {
			t.Errorf("%s = %v (present %v), want %v", series, got, ok, want)
		}
	}

	// One verdict, one corrected and one uncorrected series per DIMM that
	// is in the host, also for the DIMMs without errors: a counter that
	// first shows up at 1 hides its first error from rate().
	n := 0
	for _, d := range h.DIMMs {
		if d.Present {
			n++
		}
	}
	for _, m := range []string{"my_dimm_verdict", "my_dimm_ce_total", "my_dimm_ue_total", "my_dimm_offlined_pages"} {
		if c := count(x, m); c != n || n != 16 {
			t.Errorf("%d series of %s for %d DIMMs", c, m, n)
		}
	}
	// No GPU in this host: no GPU metric at all.
	if count(x, "my_gpu_remap_banks")+count(x, "my_gpu_remap_failure") != 0 {
		t.Error("GPU series for a host without a GPU")
	}
	// What the metrics may say about a place stops at the slot.
	for series := range x.Series {
		for _, bad := range []string{"0x", "row=", "column=", "bank=", "address"} {
			if strings.Contains(series, bad) {
				t.Errorf("series %s carries a location", series)
			}
		}
	}
}

func serialOf(h *engine.HostResult, slot string) string {
	if d := h.InSlot(slot); d != nil && d.DIMM != nil {
		return d.DIMM.Serial
	}
	return ""
}

// count returns the number of series of a metric.
func count(x *testutil.Exposition, metric string) int {
	n := 0
	for series := range x.Series {
		if series == metric || strings.HasPrefix(series, metric+"{") {
			n++
		}
	}
	return n
}

// TestHostScenarios checks single facts on the scenarios that exist for
// them.
func TestHostScenarios(t *testing.T) {
	t.Run("offlined pages", func(t *testing.T) {
		set, h := hostSet(t, testledger.Open(t, "offline-growing", 1, ""))
		var slot string
		var pages int
		for _, d := range h.DIMMs {
			if d.Features.Offline.Pages > pages {
				slot, pages = d.Slot, d.Features.Offline.Pages
			}
		}
		if pages < 2 {
			t.Fatalf("the scenario took %d pages offline", pages)
		}
		if v, ok := set.Value(DIMMOfflinedPages, h.Host, slot); !ok || int(v) != pages {
			t.Errorf("my_dimm_offlined_pages of %s = %v, want %d", slot, v, pages)
		}
		text(t, set)
	})

	// Repairs are counted per slot and type from the actions the ledger
	// ties to the DIMM, whether they worked or not.
	for _, scenario := range []string{"repaired", "repair-failed", "demo"} {
		t.Run("repairs of "+scenario, func(t *testing.T) {
			s := testledger.Open(t, scenario, 1, "")
			set, h := hostSet(t, s)
			actions, err := s.Actions(ctx, store.ActionFilter{Host: h.Host, Kinds: repairKinds})
			if err != nil || len(actions) == 0 {
				t.Fatalf("%d repairs in the scenario, %v", len(actions), err)
			}
			want := map[[2]string]float64{}
			failed := false
			for _, a := range actions {
				failed = failed || a.Result == model.ResultFailed
				if d := h.DIMM(a.DIMMUID); d != nil && d.Present {
					want[[2]string{d.Slot, string(a.Kind)}]++
				}
			}
			if len(want) == 0 || (scenario == "repair-failed" && !failed) {
				t.Fatalf("the scenario has no such repair of a DIMM in the host: %+v", actions)
			}
			for k, n := range want {
				if v, ok := set.Value(DIMMRepairs, h.Host, k[0], k[1]); !ok || v != n {
					t.Errorf("my_dimm_repairs_total{slot=%q,type=%q} = %v, want %v", k[0], k[1], v, n)
				}
			}
			_, x := text(t, set)
			if c := count(x, "my_dimm_repairs_total"); c != len(want) {
				t.Errorf("%d repair series, want %d", c, len(want))
			}
		})
	}

	t.Run("hold replacement is 4", func(t *testing.T) {
		set, h := hostSet(t, testledger.Open(t, "system-channel", 1, ""))
		holds := 0
		for _, d := range h.DIMMs {
			if d.Present && d.Verdict.Grade == model.VerdictHoldReplacement {
				holds++
				if v, _ := set.Value(DIMMVerdict, h.Host, d.Slot, d.DIMM.Serial); v != 4 {
					t.Errorf("my_dimm_verdict of %s = %v, want 4", d.Slot, v)
				}
				if v, ok := set.Value(DIMMFaults, h.Host, d.Slot, string(model.ModeSystemIssue)); !ok || v < 1 {
					t.Errorf("my_dimm_faults of %s has no system-issue fault", d.Slot)
				}
			}
		}
		if holds < 2 {
			t.Fatalf("%d DIMMs on hold in the scenario", holds)
		}
	})

	t.Run("a replaced DIMM has no series", func(t *testing.T) {
		set, h := hostSet(t, testledger.Open(t, "moved-dimm", 1, ""))
		gone := 0
		for _, d := range h.DIMMs {
			if d.Present {
				continue
			}
			gone++
			if _, ok := set.Value(DIMMVerdict, h.Host, d.Slot, d.DIMM.Serial); ok {
				t.Errorf("DIMM %s is no longer in %s and has a verdict series", d.DIMM.Serial, d.Slot)
			}
		}
		_, x := text(t, set)
		present := len(h.DIMMs) - gone
		if c := count(x, "my_dimm_verdict"); c != present {
			t.Errorf("%d verdict series for %d DIMMs in the host", c, present)
		}
	})
}

// TestUnattributedErrorsAreCounted reads a rasdaemon database alone: no
// SMBIOS, so nothing ties its records to a module. The errors still show
// in the totals, under the slot the records name and an empty serial.
func TestUnattributedErrorsAreCounted(t *testing.T) {
	set := &Set{}
	h := &engine.HostResult{Host: "h1", Unattributed: []engine.Unattributed{
		{Host: "h1", Slot: "CPU0_C1", CE: 7, UE: 1, Records: 8},
		{Host: "h1", CE: 3, Records: 3},
	}}
	s := emptyLedger(t)
	got, err := Host(ctx, s, h)
	if err != nil {
		t.Fatal(err)
	}
	set.Merge(got)
	_, x := text(t, set)
	for series, want := range map[string]float64{
		`my_dimm_ce_total{host="h1",slot="CPU0_C1",serial=""}`: 7,
		`my_dimm_ue_total{host="h1",slot="CPU0_C1",serial=""}`: 1,
		`my_dimm_ce_total{host="h1",slot="",serial=""}`:        3,
		`my_dimm_ue_total{host="h1",slot="",serial=""}`:        0,
	} {
		if got, ok := x.Series[series]; !ok || got != want {
			t.Errorf("%s = %v (present %v), want %v", series, got, ok, want)
		}
	}
	if c := count(x, "my_dimm_verdict"); c != 0 {
		t.Errorf("%d verdict series for errors that belong to no DIMM", c)
	}
}

func emptyLedger(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(ctx, t.TempDir()+"/ledger.db", store.Create)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestGPU reads the Xid lines of a kernel log and a snapshot of nvidia-smi:
// one GPU logged Xid 64, the other reports a failed remapping in its state.
func TestGPU(t *testing.T) {
	s := emptyLedger(t)
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	_, err := ingest.Run(ctx, s, []adapter.Adapter{
		klog.NewFile(testutil.Testdata(t, "klog", "nvidia-xid.messages"), klog.Options{}),
		nvidia.NewFile(testutil.Testdata(t, "nvidia", "nvidia-smi-q.xml"), nvidia.Options{}),
	}, ingest.Options{Host: "gpu-01", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	set, h := hostSet(t, s)
	if len(h.GPUs) != 2 {
		t.Fatalf("%d GPU findings", len(h.GPUs))
	}
	_, x := text(t, set)
	for series, want := range map[string]float64{
		`my_gpu_remap_banks{host="gpu-01",gpu="1650000000412",bucket="max"}`:     638,
		`my_gpu_remap_banks{host="gpu-01",gpu="1650000000412",bucket="high"}`:    2,
		`my_gpu_remap_banks{host="gpu-01",gpu="1650000000412",bucket="partial"}`: 0,
		`my_gpu_remap_banks{host="gpu-01",gpu="1650000000412",bucket="low"}`:     0,
		`my_gpu_remap_banks{host="gpu-01",gpu="1650000000412",bucket="none"}`:    0,
		`my_gpu_remap_banks{host="gpu-01",gpu="1650000000977",bucket="max"}`:     640,
		`my_gpu_remap_failure{host="gpu-01",gpu="1650000000412"}`:                1,
		`my_gpu_remap_failure{host="gpu-01",gpu="1650000000977"}`:                1,
		`my_source_grade{host="gpu-01",source="nvidia-smi",status="ok"}`:         0,
	} {
		if got, ok := x.Series[series]; !ok || got != want {
			t.Errorf("%s = %v (present %v), want %v", series, got, ok, want)
		}
	}
	if c := count(x, "my_gpu_remap_banks"); c != 10 {
		t.Errorf("%d bank series, want 5 per GPU", c)
	}
	// The findings and the metric agree on which GPUs have a failed remap.
	for _, g := range h.GPUs {
		if v, ok := set.Value(GPURemapFailure, "gpu-01", g.Key); !ok || v != 1 {
			t.Errorf("GPU %s has a finding and my_gpu_remap_failure = %v (present %v)", g.Key, v, ok)
		}
	}
}

func TestGPUMetrics(t *testing.T) {
	at := func(min int) time.Time { return fixed.Add(time.Duration(min) * time.Minute) }
	yes, no := model.Some(true), model.Some(false)
	recs := []model.GPUMem{
		// A healthy GPU: the flag is reported and clear.
		{Kind: model.GPUSnapshot, GPUSerial: "S1", ObservedAt: at(0), RemapFailure: no, BankMax: model.Some(640), BankNone: model.Some(0)},
		// The later snapshot of a GPU is the one that counts.
		{Kind: model.GPUSnapshot, GPUSerial: "S2", ObservedAt: at(0), RemapFailure: yes, BankMax: model.Some(600)},
		{Kind: model.GPUSnapshot, GPUSerial: "S2", ObservedAt: at(5), RemapFailure: no, BankMax: model.Some(639), BankHigh: model.Some(1)},
		// Xid 64 says a remapping failed, whatever a snapshot says later.
		{Kind: model.GPUXID, GPUSerial: "S3", ObservedAt: at(1), XID: model.Some(64)},
		{Kind: model.GPUSnapshot, GPUSerial: "S3", ObservedAt: at(9), RemapFailure: no},
		// Another Xid and no snapshot: the remapping state is not known.
		{Kind: model.GPUXID, GPUUUID: "GPU-u4", ObservedAt: at(2), XID: model.Some(48)},
		// A GPU model without row remapping reports neither flag nor banks.
		{Kind: model.GPUSnapshot, PCIBus: "0000:5e:00.0", ObservedAt: at(3), RetiredDBE: model.Some(1)},
		// A record that names no GPU at all cannot be a series.
		{Kind: model.GPUSnapshot, ObservedAt: at(4), RemapFailure: yes},
		{Kind: model.GPUXID, GPUIndex: model.Some(7), ObservedAt: at(6), XID: model.Some(64)},
	}
	set := &Set{}
	gpuMetrics(set, "h", recs)
	_, x := text(t, set)
	want := map[string]float64{
		`my_gpu_remap_banks{host="h",gpu="S1",bucket="max"}`:  640,
		`my_gpu_remap_banks{host="h",gpu="S1",bucket="none"}`: 0,
		`my_gpu_remap_banks{host="h",gpu="S2",bucket="max"}`:  639,
		`my_gpu_remap_banks{host="h",gpu="S2",bucket="high"}`: 1,
		`my_gpu_remap_failure{host="h",gpu="S1"}`:             0,
		`my_gpu_remap_failure{host="h",gpu="S2"}`:             0,
		`my_gpu_remap_failure{host="h",gpu="S3"}`:             1,
		`my_gpu_remap_failure{host="h",gpu="GPU 7"}`:          1,
	}
	if len(x.Series) != len(want) {
		t.Errorf("%d series, want %d: %v", len(x.Series), len(want), x.Series)
	}
	for series, v := range want {
		if got, ok := x.Series[series]; !ok || got != v {
			t.Errorf("%s = %v (present %v), want %v", series, got, ok, v)
		}
	}
}

// TestSourceInventory checks what the source metric says about sources
// that could not be read, and that it follows the latest scan of the host.
func TestSourceInventory(t *testing.T) {
	s := emptyLedger(t)
	scan := func(host string, at time.Time, srcs ...model.Source) {
		t.Helper()
		err := s.Update(ctx, func(w *store.Writer) error {
			sc := model.Scan{Host: host, Kind: model.ScanLive, StartedAt: at}
			if err := w.InsertScan(&sc); err != nil {
				return err
			}
			for i := range srcs {
				srcs[i].ScanID = sc.ID
				if err := w.InsertSource(&srcs[i]); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	scan("h1", fixed.Add(-time.Hour), model.Source{Kind: "rasdaemon", Status: model.SourceOK, GradeA: 4})
	scan("h1", fixed,
		model.Source{Kind: "rasdaemon", Status: model.SourceUnreadable},
		// Two kernel log sources of one scan: one closed to this user, one read.
		model.Source{Kind: "klog", Origin: "journal", Status: model.SourceUnreadable},
		model.Source{Kind: "klog", Origin: "/var/log/messages", Status: model.SourceUnreadable},
		model.Source{Kind: "klog", Origin: "/dev/kmsg", Status: model.SourcePartial, GradeB: 2, GradeC: 9},
		model.Source{Kind: "edac-sysfs", Status: model.SourceOK, GradeC: 3},
		model.Source{Kind: "smbios", Status: model.SourceAbsent},
	)
	scan("h2", fixed.Add(time.Hour), model.Source{Kind: "rasdaemon", Status: model.SourceOK, GradeA: 1})

	set := &Set{}
	if err := sourceMetrics(ctx, set, s, "h1"); err != nil {
		t.Fatal(err)
	}
	_, x := text(t, set)
	want := map[string]float64{
		`my_source_grade{host="h1",source="rasdaemon",status="unreadable"}`: 0,
		`my_source_grade{host="h1",source="klog",status="unreadable"}`:      0,
		`my_source_grade{host="h1",source="klog",status="partial"}`:         2,
		`my_source_grade{host="h1",source="edac-sysfs",status="ok"}`:        1,
		`my_source_grade{host="h1",source="smbios",status="absent"}`:        0,
	}
	if len(x.Series) != len(want) {
		t.Errorf("%d series, want %d: %v", len(x.Series), len(want), x.Series)
	}
	for series, v := range want {
		if got, ok := x.Series[series]; !ok || got != v {
			t.Errorf("%s = %v (present %v), want %v", series, got, ok, v)
		}
	}
	// A host the ledger has no scan of has no source series.
	none := &Set{}
	if err := sourceMetrics(ctx, none, s, "h9"); err != nil || none.Series() != 0 {
		t.Errorf("unknown host: %d series, %v", none.Series(), err)
	}
}

func TestHostReportsALedgerFailure(t *testing.T) {
	s := emptyLedger(t)
	s.Close()
	if _, err := Host(ctx, s, &engine.HostResult{Host: "h1"}); err == nil || !strings.Contains(err.Error(), "metrics of h1") {
		t.Errorf("error from a closed ledger = %v", err)
	}
}

// TestDocsListEveryMetric keeps docs/exporter.md true: every metric is in
// a table row with its type and its labels, and the page names no metric
// that does not exist.
func TestDocsListEveryMetric(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "docs", "exporter.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	known := map[string]bool{}
	for _, d := range Descs {
		known[d.Name] = true
		labels := make([]string, len(d.Labels))
		for i, l := range d.Labels {
			labels[i] = "`" + l + "`"
		}
		row := "| `" + d.Name + "` | " + d.Type + " | " + strings.Join(labels, ", ") + " |"
		if len(labels) == 0 {
			row = "| `" + d.Name + "` | " + d.Type + " | |"
		}
		if !strings.Contains(doc, row) {
			t.Errorf("docs/exporter.md lacks the row %q", row)
		}
	}
	for _, name := range regexp.MustCompile(`my_[a-z0-9_]+`).FindAllString(doc, -1) {
		if !known[name] {
			t.Errorf("docs/exporter.md names %s, which is not a metric", name)
		}
	}
}

// TestContribNamesRealMetrics keeps the files under contrib/ in step with
// the exporter: every metric a rule or a dashboard query names exists, and
// every label it uses is a label of the metrics or of a scrape.
func TestContribNamesRealMetrics(t *testing.T) {
	root := testutil.RepoRoot(t)
	known := map[string]bool{}
	labels := map[string]bool{"instance": true, "job": true}
	for _, d := range Descs {
		known[d.Name] = true
		for _, l := range d.Labels {
			labels[l] = true
		}
	}
	metric := regexp.MustCompile(`my_[a-z0-9_]+`)
	check := func(file, text string) {
		t.Helper()
		for _, name := range metric.FindAllString(text, -1) {
			// my_reader is the account of the example settings; the others
			// are file names.
			if !known[name] && name != "my_reader" && !strings.HasPrefix(name, "my_exporter.") && !strings.HasPrefix(name, "my_findings.") {
				t.Errorf("%s names %s, which is not a metric", file, name)
			}
		}
	}
	for _, rel := range []string{"prometheus/my-findings.rules.yml", "prometheus/my-findings.rules.test.yml", "prometheus/my-exporter.rules.yml",
		"prometheus/my-exporter.rules.test.yml", "prometheus/scrape.yml", "alertmanager/alertmanager.yml", "README.md"} {
		b, err := os.ReadFile(filepath.Join(root, "contrib", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		check("contrib/"+rel, string(b))
	}

	b, err := os.ReadFile(filepath.Join(root, "contrib", "grafana", "my-memory-findings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var dash struct {
		Title  string `json:"title"`
		UID    string `json:"uid"`
		Panels []struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Type        string `json:"type"`
			Targets     []struct {
				Expr   string `json:"expr"`
				Legend string `json:"legendFormat"`
			} `json:"targets"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(b, &dash); err != nil {
		t.Fatalf("contrib/grafana/my-memory-findings.json: %v", err)
	}
	if dash.UID == "" || len(dash.Panels) == 0 {
		t.Fatalf("dashboard %q has uid %q and %d panels", dash.Title, dash.UID, len(dash.Panels))
	}
	used := map[string]bool{}
	grouping := regexp.MustCompile(`\b(?:by|on|without|ignoring)\s*\(([^)]*)\)`)
	matcher := regexp.MustCompile(`[{,]\s*([a-z_]+)\s*(?:=~|!~|!=|=)`)
	legend := regexp.MustCompile(`\{\{\s*([a-z_]+)\s*\}\}`)
	for _, p := range dash.Panels {
		if p.Title == "" || p.Description == "" || len(p.Targets) == 0 {
			t.Errorf("panel %q (%s): a panel needs a title, a description and a query", p.Title, p.Type)
		}
		for _, tg := range p.Targets {
			check("panel "+p.Title, tg.Expr)
			for _, name := range metric.FindAllString(tg.Expr, -1) {
				used[name] = true
			}
			var names []string
			for _, m := range grouping.FindAllStringSubmatch(tg.Expr, -1) {
				for _, l := range strings.Split(m[1], ",") {
					names = append(names, strings.TrimSpace(l))
				}
			}
			for _, m := range matcher.FindAllStringSubmatch(tg.Expr, -1) {
				names = append(names, m[1])
			}
			for _, m := range legend.FindAllStringSubmatch(tg.Legend, -1) {
				names = append(names, m[1])
			}
			for _, l := range names {
				if l != "" && !labels[l] {
					t.Errorf("panel %q uses the label %q, which no metric has: %s", p.Title, l, tg.Expr)
				}
			}
		}
	}
	// The dashboard shows every metric about memory.
	for _, d := range []*Desc{DIMMVerdict, DIMMCorrected, DIMMUncorrected, DIMMFaults, DIMMOfflinedPages, DIMMRepairs, GPURemapBanks, GPURemapFailure, SourceGrade} {
		if !used[d.Name] {
			t.Errorf("no panel of the dashboard shows %s", d.Name)
		}
	}
}
