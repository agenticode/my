package memras_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/redfish"
	"github.com/agenticode/my/internal/memras"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/testutil"
)

// settingsOf reads the BIOS settings of a fixture tree, as the ledger
// would hold them.
func settingsOf(t *testing.T, tree string) []model.BIOSSetting {
	t.Helper()
	var c adapter.Collected
	rep := redfish.New(testutil.Testdata(t, "redfish", tree), redfish.Options{Now: time.Date(2026, 11, 12, 3, 0, 0, 0, time.UTC)}).Collect(context.Background(), &c)
	if rep.Status != model.SourceOK {
		t.Fatalf("%s: %+v", tree, rep)
	}
	out := make([]model.BIOSSetting, len(c.Settings))
	for i, s := range c.Settings {
		out[i] = *s
	}
	return out
}

// outcome writes a check as one line per setting.
func outcome(r *memras.Result) string {
	var b strings.Builder
	for _, it := range r.Items {
		b.WriteString(it.Setting + ": " + it.Status)
		if it.Attribute != "" {
			b.WriteString(" " + it.Attribute)
		}
		if it.Value != "" {
			b.WriteString("=" + it.Value)
		}
		if it.State != "" {
			b.WriteString(" (" + it.State + ")")
		}
		if it.HasPending {
			b.WriteString(" pending " + it.Pending + " (" + it.PendingState + ")")
		}
		if it.Reason != "" {
			b.WriteString(" [" + it.Reason + "]")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// The attribute names and values of the built-in profile are the ones of
// the fixture trees, which follow the vendors' documents.
func TestCheckFixtureTrees(t *testing.T) {
	p := memras.Builtin()

	dell := p.Check(memras.Input{Manufacturer: "Dell Inc.", Settings: settingsOf(t, "dell-shaped")})
	want := `patrol-scrub: ok MemPatrolScrub=Standard (on) pending Disabled (off)
adddc: reported AdddcSetting=Enabled (on)
memory-mode: reported MemOpMode=OptimizerMode (independent)
ce-logging: ok CorrEccSmi=Enabled (on)
ce-threshold: reported CECriticalSEL=Enabled (on)
leaky-bucket: unmapped [not-documented]
ppr: absent PostPackageRepair
memory-frequency: reported MemFrequency=2666MHz (limited)
refresh-rate: reported MemRefreshRate=1x (1x)
`
	if got := outcome(dell); got != want {
		t.Errorf("Dell-shaped:\n%s\nwant:\n%s", got, want)
	}
	if dell.Vendor != "Dell" || dell.By != "manufacturer" || dell.Mismatches != 0 || dell.Registry != "BiosAttributeRegistry.v1_0_0" || dell.Settings != 12 {
		t.Errorf("result = %+v", dell)
	}
	scrub := dell.Item(memras.PatrolScrub)
	if scrub.Note != "the whole memory is scrubbed once in 24 hours" || scrub.NoteKO == "" || scrub.Expected != "on" || !scrub.Registry ||
		!scrub.ResetRequired.Or(false) || scrub.Source == nil || !strings.Contains(scrub.Source.URL, "dl.dell.com") {
		t.Errorf("patrol scrub = %+v", scrub)
	}
	// What is on record and mapped to nothing is listed, not dropped.
	var related []string
	for _, r := range dell.Related {
		related = append(related, r.Name)
	}
	if got := strings.Join(related, " "); got != "MemTest NodeInterleave SysMemSize SysMemSpeed VideoMem" {
		t.Errorf("related: %s", got)
	}
	if len(dell.Sources) != 1 || dell.Sources[0].Title == "" {
		t.Errorf("sources = %+v", dell.Sources)
	}

	hpe := p.Check(memras.Input{Manufacturer: "HPE", Settings: settingsOf(t, "hpe-shaped")})
	want = `patrol-scrub: ok MemPatrolScrubbing=Enabled (on)
adddc: reported AdvancedMemProtection=FastFaultTolerantADDDC (on) pending AdvancedEcc (off)
memory-mode: reported AdvancedMemProtection=FastFaultTolerantADDDC (adddc) pending AdvancedEcc (advanced-ecc)
ce-logging: unmapped [not-documented]
ce-threshold: unmapped [not-documented]
leaky-bucket: unmapped [not-documented]
ppr: unmapped [not-documented]
memory-frequency: reported MaxMemBusFreqMHz=Auto (auto)
refresh-rate: reported MemRefreshRate=Refreshx1 (1x)
`
	if got := outcome(hpe); got != want {
		t.Errorf("HPE-shaped:\n%s\nwant:\n%s", got, want)
	}

	lenovo := p.Check(memras.Input{Manufacturer: "Lenovo", Settings: settingsOf(t, "lenovo-shaped")})
	if it := lenovo.Item(memras.MemoryFrequency); it.Status != memras.StatusReported || it.State != "auto" || it.Registry {
		t.Errorf("Lenovo memory frequency = %+v", it)
	}
	if it := lenovo.Item(memras.PatrolScrub); it.Status != memras.StatusUnmapped || it.Reason != memras.ReasonNotDocumented {
		t.Errorf("Lenovo patrol scrub = %+v", it)
	}
}
