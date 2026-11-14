package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// Run reads a ledger and analyses it. The findings refer to the ledger
// records they rest on, with the raw_ref of each.
func Run(ctx context.Context, s *store.Store, opts Options) (*Result, error) {
	in, err := Load(ctx, s)
	if err != nil {
		return nil, err
	}
	res, err := Analyze(in, opts)
	if err != nil {
		return nil, err
	}
	if err := res.AttachRawRefs(ctx, s); err != nil {
		return nil, err
	}
	return res, nil
}

// Grade returns the finding for everything in the result: the worst grade
// among its hosts. Hold replacement outranks the other grades.
func (r *Result) Grade() model.VerdictGrade {
	g := model.VerdictOK
	for _, h := range r.Hosts {
		if worse(h.Grade, g) {
			g = h.Grade
		}
	}
	return g
}

// ExitCode returns the process exit code that stands for the result:
// 0 ok, 1 observe, 2 consider replacement, 3 replace now, 4 hold
// replacement.
func (r *Result) ExitCode() int { return r.Grade().ExitCode() }

// verdicts calls fn for every finding of the result.
func (r *Result) verdicts(fn func(*model.Verdict)) {
	for _, h := range r.Hosts {
		for _, d := range h.DIMMs {
			fn(d.Verdict)
		}
		for _, g := range h.GPUs {
			fn(g.Verdict)
		}
	}
}

// AttachRawRefs fills in, for every event a finding refers to, where its
// original record is. Analyze works on a compact form of the events that
// does not hold those references; they are looked up in the ledger.
func (r *Result) AttachRawRefs(ctx context.Context, s *store.Store) error {
	seen := map[int64]bool{}
	var ids []int64
	r.verdicts(func(v *model.Verdict) {
		for _, e := range v.Evidence {
			for _, ref := range e.Records {
				if ref.Kind == model.RecordEvent && ref.RawRef == "" && !seen[ref.ID] {
					seen[ref.ID] = true
					ids = append(ids, ref.ID)
				}
			}
		}
	})
	if len(ids) == 0 {
		return nil
	}
	refs, err := s.EventRawRefs(ctx, ids)
	if err != nil {
		return fmt.Errorf("engine: look up original records: %w", err)
	}
	r.verdicts(func(v *model.Verdict) {
		for i := range v.Evidence {
			for k := range v.Evidence[i].Records {
				ref := &v.Evidence[i].Records[k]
				if ref.Kind == model.RecordEvent && ref.RawRef == "" {
					ref.RawRef = refs[ref.ID]
				}
			}
		}
	})
	return nil
}

// Run records what the stored findings of a host were computed with.
type runRecord struct {
	EngineVersion string    `json:"engine_version"`
	ComputedAt    time.Time `json:"computed_at"`
	AsOf          time.Time `json:"as_of,omitzero"`
	Grade         string    `json:"grade"`
	// Thresholds lists the thresholds that differ from the defaults.
	Thresholds map[string]string `json:"thresholds,omitzero"`
}

// MetaKey returns the key under which the ledger keeps what the stored
// findings of a host were computed with: engine version, reference time and
// the thresholds that differ from the defaults, as JSON.
func MetaKey(host string) string { return "analysis." + host }

// Save stores the faults and findings of the result in the ledger. What the
// ledger held for the hosts of the result is replaced; other hosts are left
// alone. Faults get their ledger IDs.
func (r *Result) Save(ctx context.Context, s *store.Store) error {
	return s.Update(ctx, func(w *store.Writer) error {
		for _, h := range r.Hosts {
			if err := w.DeleteAnalysis(h.Host); err != nil {
				return fmt.Errorf("engine: save %s: %w", h.Host, err)
			}
			var computed time.Time
			for _, d := range h.DIMMs {
				for _, f := range d.Faults {
					// A fault is stored with the host its DIMM is in now.
					row := f.Fault
					row.ID, row.Host = 0, h.Host
					row.EventIDs = r.in.EventIDs(f)
					if err := w.InsertFault(&row); err != nil {
						return fmt.Errorf("engine: save %s: %w", h.Host, err)
					}
					f.ID = row.ID
				}
				v := *d.Verdict
				v.ID = 0
				if err := w.InsertVerdict(&v); err != nil {
					return fmt.Errorf("engine: save %s: %w", h.Host, err)
				}
				d.Verdict.ID = v.ID
				computed = v.ComputedAt
			}
			for _, g := range h.GPUs {
				v := *g.Verdict
				v.ID = 0
				if err := w.InsertVerdict(&v); err != nil {
					return fmt.Errorf("engine: save %s: %w", h.Host, err)
				}
				g.Verdict.ID = v.ID
				computed = v.ComputedAt
			}
			rec := runRecord{EngineVersion: r.EngineVersion, ComputedAt: computed, AsOf: h.AsOf, Grade: string(h.Grade)}
			if changed := r.Config.Changed(); len(changed) > 0 {
				rec.Thresholds = map[string]string{}
				for _, kv := range changed {
					rec.Thresholds[kv[0]] = kv[1]
				}
			}
			b, err := json.Marshal(rec)
			if err != nil {
				return err
			}
			if err := w.SetMeta(MetaKey(h.Host), string(b)); err != nil {
				return fmt.Errorf("engine: save %s: %w", h.Host, err)
			}
		}
		return nil
	})
}
