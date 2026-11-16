package ingest

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// goodSource emits three corrected errors on one DIMM.
func goodSource(at time.Time) adapter.Adapter {
	return &fake{kind: "klog", fn: func(sink adapter.Sink) error {
		for i := range 3 {
			e := &model.Event{ErrorDT: at.Add(time.Duration(i) * time.Minute), ErrorSeverity: model.SevCE, Label: "CPU0_DIMM_A1",
				Source: "klog/edac", RawRef: fmt.Sprintf("good:%d", i), Fingerprint: fmt.Sprintf("good %d", i)}
			if err := sink.Event(e); err != nil {
				return err
			}
		}
		return nil
	}}
}

// faulty is a reader that fails in one of the ways a reader can.
type faulty struct {
	how string
	at  time.Time
}

func (f *faulty) Kind() string   { return "faulty" }
func (f *faulty) Origin() string { return "faulty:" + f.how }
func (f *faulty) Collect(ctx context.Context, sink adapter.Sink) adapter.Report {
	rep := adapter.Report{Kind: f.Kind(), Origin: f.Origin(), Status: model.SourceOK}
	var tally adapter.Tally
	switch f.how {
	case "open":
		var m map[string]int
		m["x"]++ // a panic before any record
	case "transaction":
		// Records are in the ledger's transaction when the reader fails.
		for i := range 4 {
			e := &model.Event{ErrorDT: f.at, ErrorSeverity: model.SevUE, Label: "CPU0_DIMM_B1", Source: "faulty/x",
				RawRef: fmt.Sprintf("bad:%d", i), Fingerprint: fmt.Sprintf("bad %d", i)}
			if err := sink.Event(e); err != nil {
				rep.SinkErr = err
				return rep
			}
		}
		var p *model.Event
		_ = p.Count
	case "record":
		// The record at index 2 panics; the others are read.
		for i := range 5 {
			rep.Records++
			err := adapter.Each(&tally, fmt.Sprintf("rec:%d", i), "DIMM B1 record", func() error {
				if i == 2 {
					var list []int
					_ = list[i]
				}
				return sink.Event(&model.Event{ErrorDT: f.at.Add(time.Duration(i) * time.Second), ErrorSeverity: model.SevCE,
					Label: "CPU0_DIMM_B1", Source: "faulty/x", RawRef: fmt.Sprintf("rec:%d", i), Fingerprint: fmt.Sprintf("rec %d", i)})
			})
			if err != nil {
				rep.SinkErr = err
				return rep
			}
		}
	case "block":
		<-ctx.Done()
		rep.Status, rep.Detail = model.SourceError, "stopped: "+ctx.Err().Error()
		return rep
	}
	rep.AddTally(&tally)
	if rep.Skipped > 0 {
		rep.Status = model.SourcePartial
	}
	return rep
}

// ledgerRows returns the fingerprints of the events of a host whose raw
// reference starts with prefix.
func ledgerRows(t *testing.T, s *store.Store, prefix string) []string {
	t.Helper()
	evs, err := s.EventList(ctx, store.EventFilter{Host: "node-a"})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		if strings.HasPrefix(e.RawRef, prefix) {
			out = append(out, fmt.Sprintf("%s %s %d %s", e.Fingerprint, e.Slot, e.Count, e.ErrorSeverity))
		}
	}
	return out
}

// TestReaderFault: a reader that panics when it opens, in the middle of
// the transaction of its source, or at one record, and one that blocks
// until its context ends. The run ends normally, the source row says what
// happened, and the records of the other sources are those of a run
// without the faulty reader.
func TestReaderFault(t *testing.T) {
	at := time.Date(2026, 11, 3, 2, 0, 0, 0, time.UTC)
	clean := newStore(t)
	mustRun(t, clean, at.Add(time.Hour), goodSource(at))
	want := ledgerRows(t, clean, "good:")
	if len(want) != 3 {
		t.Fatalf("clean run: %v", want)
	}
	for _, how := range []string{"open", "transaction", "record", "block"} {
		t.Run(how, func(t *testing.T) {
			s := newStore(t)
			opts := Options{Host: "node-a", Now: at.Add(time.Hour)}
			if how == "block" {
				opts.ReadDeadline = time.Now().Add(300 * time.Millisecond)
			}
			order := []adapter.Adapter{&faulty{how: how, at: at}, goodSource(at)}
			if how == "block" {
				// The deadline ends the reading of the run: the sources
				// after the one that blocked are not read at all.
				order[0], order[1] = order[1], order[0]
			}
			res, err := Run(ctx, s, order, opts)
			if err != nil {
				t.Fatalf("the run failed: %v", err)
			}
			if got := ledgerRows(t, s, "good:"); strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("records of the good source:\n%v\nwant\n%v", got, want)
			}
			src := res.Sources[0]
			if how == "block" {
				src = res.Sources[1]
			}
			switch how {
			case "open", "transaction":
				if src.Status != model.SourceError || src.Faults != 1 || !strings.Contains(src.Detail, "the reader failed: ") ||
					!strings.Contains(src.Detail, "nothing of this source was added") || !strings.Contains(src.Detail, "fault_test.go:") {
					t.Errorf("source: %+v", src)
				}
				if strings.Contains(src.Detail, "/Users/") || strings.Contains(src.Detail, "goroutine") {
					t.Errorf("the detail holds a path or a stack: %s", src.Detail)
				}
				if bad := ledgerRows(t, s, "bad:"); len(bad) != 0 || src.Events != 0 {
					t.Errorf("records of the failed reader are in the ledger: %v", bad)
				}
			case "record":
				if src.Status != model.SourcePartial || src.Unread != 1 || src.Faults != 1 || src.Events != 4 {
					t.Errorf("source: %+v", src)
				}
				if got := ledgerRows(t, s, "rec:"); len(got) != 4 {
					t.Errorf("records around the bad one: %v", got)
				}
				un, _ := s.UnreadRecords(ctx, src.ID)
				if len(un) != 1 || un[0].Ref != "rec:2" || !strings.Contains(un[0].Reason, "index out of range") {
					t.Errorf("unread records: %+v", un)
				}
			case "block":
				if src.Status != model.SourceError || !strings.Contains(src.Detail, "deadline") {
					t.Errorf("source: %+v", src)
				}
			}
		})
	}
}

// TestUnreadRecordsKept: the text of a record that was not understood is
// kept only when it names memory, at most 50 per source.
func TestUnreadRecordsKept(t *testing.T) {
	s := newStore(t)
	a := &fake{kind: "redfish", fn: func(sink adapter.Sink) error { return nil }}
	var tally adapter.Tally
	for i := range 120 {
		text := fmt.Sprintf("fan %d speed below limit", i)
		if i%2 == 0 {
			text = fmt.Sprintf("Memory DIMM_A%d: %s", i, strings.Repeat("z", 5000))
		}
		_ = adapter.Each(&tally, fmt.Sprintf("entry %d", i), text, func() error { return adapter.Unreadable("message has no readable time") })
	}
	a.rep.AddTally(&tally)
	res := mustRun(t, s, time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC), a)
	src := res.Sources[0]
	if src.Unread != 120 || src.Skipped != 120 || src.Status != model.SourcePartial {
		t.Errorf("source: %+v", src)
	}
	un, err := s.UnreadRecords(ctx, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(un) != model.MaxUnreadKept {
		t.Fatalf("%d unread records kept, want %d", len(un), model.MaxUnreadKept)
	}
	for _, u := range un {
		if !strings.HasPrefix(u.Text, "Memory DIMM_A") || len(u.Text) > model.MaxUnreadText || u.Reason != "message has no readable time" {
			t.Errorf("kept: %s %q (%d bytes)", u.Ref, u.Reason, len(u.Text))
		}
	}
}
