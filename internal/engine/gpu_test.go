package engine

import (
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
)

// A row remapping failure stands for the life of the GPU. A later reading
// that does not carry the flag, as a query that asked for other columns
// gives, takes nothing back; a later reading that carries it does.
func TestGPURemapFailureIsSticky(t *testing.T) {
	at := t0.Add(40 * day)
	gpu := func(id int64, when time.Time, failure model.Opt[bool]) model.GPUMem {
		return model.GPUMem{ID: id, Host: "h1", ObservedAt: when, Kind: model.GPUSnapshot, GPUSerial: "1320921000977", RemapFailure: failure, RawRef: "smi"}
	}
	for _, tc := range []struct {
		name     string
		recs     []model.GPUMem
		finding  bool
		recordID int64
	}{
		{"the flag, then a reading without it", []model.GPUMem{gpu(1, at, model.Some(true)), gpu(2, at.Add(time.Hour), model.Opt[bool]{})}, true, 1},
		{"a reading without it, then the flag", []model.GPUMem{gpu(1, at, model.Opt[bool]{}), gpu(2, at.Add(time.Hour), model.Some(true))}, true, 2},
		{"no reading carries the flag", []model.GPUMem{gpu(1, at, model.Opt[bool]{}), gpu(2, at.Add(time.Hour), model.Opt[bool]{})}, false, 0},
		{"the flag is clear in the latest reading that carries it", []model.GPUMem{gpu(1, at, model.Some(false)), gpu(2, at.Add(time.Hour), model.Opt[bool]{})}, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := ruleBuilder()
			b.in.host("h1")
			b.in.GPUs = tc.recs
			h := analyze(t, b.in, Options{AsOf: asOf}).Host("h1")
			if (len(h.GPUs) == 1) != tc.finding {
				t.Fatalf("GPU findings = %+v, want one: %v", h.GPUs, tc.finding)
			}
			if !tc.finding {
				return
			}
			v := h.GPUs[0].Verdict
			if v.Grade != model.VerdictReplaceNow || len(v.Evidence) != 1 || v.Evidence[0].Code != "gpu.remap_failure" || v.Evidence[0].Records[0].ID != tc.recordID {
				t.Errorf("finding = %+v", v)
			}
		})
	}
}
