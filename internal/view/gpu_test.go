package view

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/testledger"
)

func gpuCase(t *testing.T, lang i18n.Lang) (*Case, *engine.HostResult) {
	t.Helper()
	c, err := Open(ctx, testledger.OpenGPU(t), lang, engine.Options{Now: testledger.GPUNow})
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Host("")
	if err != nil {
		t.Fatal(err)
	}
	return c, h
}

func TestGPUs(t *testing.T) {
	c, h := gpuCase(t, i18n.EN)
	v, err := c.GPUs(ctx, h, GPUOptions{})
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "gpu.json", v)

	if v.Host != testledger.GPUHost || v.Days != GPUDays || v.Grade != model.VerdictReplaceNow || v.Detail {
		t.Errorf("view: host %q, days %d, grade %s, detail %v", v.Host, v.Days, v.Grade, v.Detail)
	}
	// The window ends at the latest reading, which is later than the last
	// error record of the host.
	if want := time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC); !v.AsOf.Equal(want) || !h.AsOf.Before(want) {
		t.Errorf("window ends %s, host reference time %s", v.AsOf, h.AsOf)
	}
	// The GPU with the least room first; within a state by index.
	var got []string
	for _, g := range v.GPUs {
		got = append(got, g.State+" "+g.Serial)
	}
	want := []string{
		"failed 1650000000412", "failed 1650000000977", "pending 1650000001201", "low 1650000001302",
		"repaired 1650000001403", "repaired 0320000000716", "errors 1650000001504", "quiet 1650000001605",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("states\n got %q\nwant %q", got, want)
	}
	by := map[string]*GPU{}
	for i := range v.GPUs {
		by[v.GPUs[i].Serial] = &v.GPUs[i]
	}

	// A failed remapping is a finding of the engine; the other states have
	// no rule and no grade.
	for serial, g := range by {
		failed := g.State == GPUFailed
		if (g.Grade == model.VerdictReplaceNow) != failed || (len(g.Rules) == 1 && g.Rules[0] == engine.RuleRepairGone) != failed || (len(g.Evidence) > 0) != failed {
			t.Errorf("GPU %s: state %s, grade %s, rules %v, %d evidence lines", serial, g.State, g.Grade, g.Rules, len(g.Evidence))
		}
	}

	// The GPU that logged Xid 64: its later reading says "no failure", and
	// the record of the failure stands.
	g := by["1650000000412"]
	if !g.RemapFailed.V || g.XIDTotal != 3 || !reflect.DeepEqual(g.XIDs, []XIDCount{
		{48, 1, "uncorrectable (double bit) ECC error"}, {63, 1, "row remapping recorded"}, {64, 1, "row remapping failed"},
	}) {
		t.Errorf("GPU 412: failed %v, Xids %+v", g.RemapFailed, g.XIDs)
	}
	if g.Banks == nil || *g.Banks != (GPUBanks{Max: 638, High: 2, ReadAt: time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)}) || g.Banks.Total() != 640 {
		t.Errorf("GPU 412: banks %+v", g.Banks)
	}
	if g.Next == nil || g.Next.Code != "next.gpu_replace" || !strings.HasPrefix(g.Evidence[0].Text, "[R-REP-01] row remapping failed: XID 64") {
		t.Errorf("GPU 412: next %+v, evidence %+v", g.Next, g.Evidence)
	}

	// The flag of the driver about SRAM errors has no rule; a note says so.
	if n := by["1650000000977"].Notes; len(n) != 1 || n[0].Code != "gpu.note.sram" {
		t.Errorf("GPU 977: notes %+v", n)
	}

	// A pending remapping: the latest reading asked for the flags alone,
	// so rows and banks are those of the reading before, and a note says so.
	g = by["1650000001201"]
	if g.Readings != 2 || !g.ReadAt.Equal(time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC)) || g.RemapUncorrectable != model.Some(3) || !g.RemapPending.V || g.Banks == nil || g.Banks.Low != 1 {
		t.Errorf("GPU 1201: %+v", g)
	}
	if len(g.Notes) != 1 || g.Notes[0].Code != "gpu.note.older" || !strings.Contains(g.Notes[0].Text, "remapped rows of 2026-11-13, bank histogram of 2026-11-13") {
		t.Errorf("GPU 1201: notes %+v", g.Notes)
	}
	if g.Next == nil || g.Next.Code != "gpu.next.pending" {
		t.Errorf("GPU 1201: next %+v", g.Next)
	}

	// A bank without a spare row.
	if g = by["1650000001302"]; g.Banks.None != 1 || g.Next == nil || g.Next.Code != "gpu.next.low" {
		t.Errorf("GPU 1302: %+v", g)
	}
	// An older GPU: retired pages, no row remapping, no bank histogram.
	if g = by["0320000000716"]; g.RetiredSBE != model.Some(3) || g.RetiredDBE != model.Some(1) || g.Banks != nil || g.RemapCorrectable.Valid {
		t.Errorf("GPU 716: %+v", g)
	}
	// Corrected errors and nothing repaired; and nothing at all.
	if g = by["1650000001504"]; g.DRAMCorrectable != model.Some(31) || g.DRAMCorrectableVolatile != model.Some(2) || g.Next != nil || len(g.Notes) != 0 {
		t.Errorf("GPU 1504: %+v", g)
	}
	if g = by["1650000001605"]; g.XIDTotal != 0 || g.Next != nil {
		t.Errorf("GPU 1605: %+v", g)
	}
}

func TestGPUByName(t *testing.T) {
	c, h := gpuCase(t, i18n.KO)
	for ref, serial := range map[string]string{
		"1":             "1650000000977", // index
		"1650000001302": "1650000001302", // serial number
		"..1403":        "1650000001403", // the end of it, as the list prints it
		"GPU-11111111-2222-3333-4444-555555555504": "1650000001504",
		"11111111-2222-3333-4444-555555555505":     "1650000001605",
		"0000:3b:00.0":                             "1650000000412",
		"0000:3B:00":                               "1650000000412",
		"00000000:5F:00.0":                         "0320000000716",
	} {
		v, err := c.GPUs(ctx, h, GPUOptions{Ref: ref})
		if err != nil || len(v.GPUs) != 1 || v.GPUs[0].Serial != serial || !v.Detail || v.GPUs[0].N != 1 {
			t.Errorf("%q: %v, %+v", ref, err, v)
			continue
		}
		// The grade is that of the GPU shown.
		if want := v.GPUs[0].Grade; v.Grade != want {
			t.Errorf("%q: view grade %s, GPU grade %s", ref, v.Grade, want)
		}
	}
	if v, _ := c.GPUs(ctx, h, GPUOptions{Ref: "1650000000977"}); v.GPUs[0].StateText != "리페어 실패" || len(v.GPUs[0].Events) != 0 {
		t.Errorf("Korean state %q, events %+v", v.GPUs[0].StateText, v.GPUs[0].Events)
	}
	for _, ref := range []string{"9", "977", "GPU-0", "no such"} {
		if _, err := c.GPUs(ctx, h, GPUOptions{Ref: ref}); !errors.Is(err, ErrNoSuchGPU) {
			t.Errorf("%q: err = %v, want ErrNoSuchGPU", ref, err)
		}
	}
	// The end of a serial number that two GPUs share names neither.
	if _, err := c.GPUs(ctx, h, GPUOptions{Ref: "..0000"}); !errors.Is(err, ErrNoSuchGPU) {
		t.Errorf("a suffix no serial number ends in: %v", err)
	}
	if _, err := c.GPUs(ctx, h, GPUOptions{Ref: "16500000"}); !errors.Is(err, ErrNoSuchGPU) {
		t.Errorf("the start of a serial number: %v", err)
	}
}

// The Xid window: records older than it are on record and not counted.
func TestGPUWindow(t *testing.T) {
	c, h := gpuCase(t, i18n.EN)
	v, err := c.GPUs(ctx, h, GPUOptions{Days: 1, Ref: "0"})
	if err != nil {
		t.Fatal(err)
	}
	g := v.GPUs[0]
	// The Xid records are of 2026-11-12, the window is the day to
	// 2026-11-14 08:00.
	if v.Days != 1 || len(g.XIDs) != 0 || g.XIDTotal != 3 || len(g.Events) != 3 || g.Events[0].XID != 64 || g.State != GPUFailed {
		t.Errorf("window of one day: Xids %+v, total %d, events %+v, state %s", g.XIDs, g.XIDTotal, g.Events, g.State)
	}
}

// A host without GPU records gives an empty view and no error.
func TestGPUsOfAHostWithoutGPUs(t *testing.T) {
	c, err := Open(ctx, testledger.Open(t, "demo", 1, ""), i18n.EN, engine.Options{})
	if err != nil {
		t.Fatal(err)
	}
	h, _ := c.Host("")
	v, err := c.GPUs(ctx, h, GPUOptions{})
	if err != nil || len(v.GPUs) != 0 || v.Grade != model.VerdictOK || v.GPUs == nil {
		t.Errorf("view %+v, %v", v, err)
	}
	if _, err := c.GPUs(ctx, h, GPUOptions{Ref: "0"}); !errors.Is(err, ErrNoSuchGPU) {
		t.Errorf("a GPU of a host without GPUs: %v", err)
	}
}
