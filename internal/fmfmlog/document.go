package fmfmlog

import (
	"bufio"
	"bytes"
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
	// VendorAnalyzer names the vendor analyzer that took part in the run:
	// the rows it named neighbours can change which errors count as one
	// fault. What it said beyond the public classification is not in this
	// log.
	VendorAnalyzer *VendorAnalyzer `json:"vendor_analyzer,omitempty"`
	HostID         string          `json:"HostID"`
	// ActionDescriptors says in the document itself what it does not hold:
	// it is always "none". Findings and suggestions are for a person.
	ActionDescriptors string     `json:"action_descriptors"`
	AnalysisLog       []Analysis `json:"AnalysisLog"`
	// RuntimeLog holds the error records of the host with the field names
	// of the runtime error log, each counted once.
	RuntimeLog []Runtime `json:"RuntimeLog,omitempty"`
}

// VendorAnalyzer names a vendor analyzer.
type VendorAnalyzer struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Vendor  string `json:"vendor,omitempty"`
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
	// VendorAnalyzer names the vendor analyzer that took part, if one did.
	VendorAnalyzer *VendorAnalyzer
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
		VendorAnalyzer:    opts.VendorAnalyzer,
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
	err = runtimeLog(ctx, s, h.Host, func(r *Runtime) error {
		doc.RuntimeLog = append(doc.RuntimeLog, *r)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return doc, nil
}

// runtimeLog hands the error records of a host to fn, one record per
// error: what the ledger counts, as it counts it.
func runtimeLog(ctx context.Context, s *store.Store, host string, fn func(*Runtime) error) error {
	for e, err := range s.Events(ctx, store.EventFilter{Host: host}) {
		if err != nil {
			return fmt.Errorf("fmfm runtime log: %w", err)
		}
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
		if err := fn(&r); err != nil {
			return err
		}
	}
	return nil
}

// Write writes the document as indented JSON.
func (doc *Document) Write(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

// Stream writes the analysis log of a host to w, byte for byte what Build
// and Write give, and returns the document without its runtime log and the
// number of runtime records written. The runtime log of a host with a
// million errors is hundreds of megabytes as a document in memory; here
// each record is written as it is read from the ledger.
func Stream(ctx context.Context, w io.Writer, s *store.Store, h *engine.HostResult, opts BuildOptions) (doc *Document, records int, err error) {
	withRecords := opts.Records
	opts.Records = false
	if doc, err = Build(ctx, s, h, opts); err != nil {
		return nil, 0, err
	}
	head, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, 0, err
	}
	bw := bufio.NewWriterSize(w, 256<<10)
	if withRecords {
		// The runtime log is the last field of the document and is left
		// out when it is empty: the document is opened up for it when the
		// first record is there.
		err = runtimeLog(ctx, s, h.Host, func(r *Runtime) error {
			b, err := json.MarshalIndent(r, "    ", "  ")
			if err != nil {
				return err
			}
			if records == 0 {
				bw.Write(bytes.TrimSuffix(head, []byte("\n}")))
				bw.WriteString(",\n  \"RuntimeLog\": [\n    ")
			} else {
				bw.WriteString(",\n    ")
			}
			records++
			_, err = bw.Write(b)
			return err
		})
		if err != nil {
			return nil, 0, err
		}
	}
	if records == 0 {
		bw.Write(head)
		bw.WriteString("\n")
	} else {
		bw.WriteString("\n  ]\n}\n")
	}
	if err := bw.Flush(); err != nil {
		return nil, 0, err
	}
	return doc, records, nil
}
