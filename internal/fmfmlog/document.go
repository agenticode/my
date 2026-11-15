package fmfmlog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// Format names the document "my export fmfm" writes, and FormatVersion its
// version. The version changes when a field is renamed or removed; new
// fields do not change it.
const (
	Format        = "fmfm-analysis-log"
	FormatVersion = 1
)

// Document is an analysis log of one host: one record per DIMM, and, when
// asked for, the error records the analysis rests on as a runtime log.
type Document struct {
	Format        string   `json:"format"`
	FormatVersion int      `json:"format_version"`
	OutputDT      string   `json:"OutputDT"`
	Analyzer      Analyzer `json:"Analyzer"`
	HostID        string   `json:"HostID"`
	// ActionDescriptors says in the document itself what it does not hold:
	// it is always "none". Findings and suggestions are for a person.
	ActionDescriptors string     `json:"action_descriptors"`
	AnalysisLog       []Analysis `json:"AnalysisLog"`
	// RuntimeLog holds the error records of the host with the field names
	// of the runtime error log, each counted once.
	RuntimeLog []Runtime `json:"RuntimeLog,omitempty"`
}

// BuildOptions choose what a document holds.
type BuildOptions struct {
	// Lang is the language of the sentences of the findings.
	Lang i18n.Lang
	// Now is the time of the output; zero means the current time.
	Now time.Time
	// Tool is the name and version of the program that writes the log.
	Tool, Version string
	// All includes the DIMMs without errors and without a finding.
	All bool
	// Records adds the runtime log.
	Records bool
}

// Build writes the analysis log of a host from its findings.
func Build(ctx context.Context, s *store.Store, h *engine.HostResult, opts BuildOptions) (*Document, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	doc := &Document{
		Format: Format, FormatVersion: FormatVersion, OutputDT: now.UTC().Format(timeLayout), HostID: h.Host,
		Analyzer:          Analyzer{Name: opts.Tool, Version: opts.Version, EngineVersion: engine.Version, Level: 1},
		ActionDescriptors: "none",
		AnalysisLog:       []Analysis{},
	}
	slots, err := s.Slots(ctx, h.Host)
	if err != nil {
		return nil, fmt.Errorf("fmfm analysis log: slots: %w", err)
	}
	place := map[string]model.Slot{}
	for _, sl := range slots {
		place[sl.Name] = sl
	}
	aopts := Options{Lang: opts.Lang, Now: now, Slot: func(slot string) (socket, channel, position model.Opt[int]) {
		sl, ok := place[slot]
		if !ok {
			return
		}
		return sl.Socket, sl.Channel, sl.Position
	}}
	for _, d := range h.DIMMs {
		listed := d.Verdict != nil && (d.Verdict.Grade != model.VerdictOK || len(d.Verdict.CounterEvidence) > 0)
		if !opts.All && !listed && d.Features.CE+d.Features.UE == 0 {
			continue
		}
		doc.AnalysisLog = append(doc.AnalysisLog, AnalysisOf(d, aopts))
	}
	if !opts.Records {
		return doc, nil
	}
	for e, err := range s.Events(ctx, store.EventFilter{Host: h.Host}) {
		if err != nil {
			return nil, fmt.Errorf("fmfm runtime log: %w", err)
		}
		// One record per error: what the ledger counts, as it counts it.
		n := e.Errors()
		if n <= 0 {
			continue
		}
		// The count of a counter record is what located records do not
		// already account for.
		r := RuntimeOf(e)
		r.Count = 0
		if n != 1 {
			r.Count = n
		}
		doc.RuntimeLog = append(doc.RuntimeLog, r)
	}
	return doc, nil
}

// Write writes the document as indented JSON.
func (doc *Document) Write(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}
