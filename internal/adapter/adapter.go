// Package adapter defines how input sources hand their records to the
// ledger. An adapter reads one source (a rasdaemon database, a kernel log,
// EDAC sysfs, SMBIOS) and pushes what it finds into a Sink. Adapters only
// read: they never write to the source or change system state.
package adapter

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"syscall"

	"github.com/agenticode/my/internal/model"
)

// Sink receives the records an adapter produces. A Sink error stops the
// adapter: it means the ledger could not take the record, which is not the
// source's fault.
//
// Adapters leave Host empty on events and actions; the receiver knows which
// host the source belongs to.
type Sink interface {
	Host(*model.Host) error
	Boot(*model.Boot) error
	Module(*model.Module) error
	Counter(*model.CounterSample) error
	Event(*model.Event) error
	Action(*model.Action) error
	// GPU takes a GPU memory record: a snapshot of the ECC and row
	// remapping state of a GPU, or one XID event.
	GPU(*model.GPUMem) error
	// ECS takes a reading of the DDR5 error check and scrub registers.
	ECS(*model.ECSSample) error
}

// Report says how reading a source went. Every adapter returns one, also
// when the source is missing or unreadable, so that the source inventory of
// a scan lists what was not seen.
type Report struct {
	Kind   string // adapter name
	Origin string // path or description of what was read
	Status model.SourceStatus
	// Detail explains a status other than ok.
	Detail string
	// Records is the number of original records looked at, Skipped the
	// number among them that could not be used.
	Records int
	Skipped int
	// Notes are remarks for the reader, such as skip reasons with counts.
	Notes []string
	// HostHint is a host name found in the source itself, if any.
	HostHint string
	// SinkErr is the error a Sink returned, if that is what stopped the read.
	SinkErr error
}

// Adapter reads one source.
type Adapter interface {
	// Kind names the adapter, for example "rasdaemon", "klog", "edac-sysfs",
	// "smbios" or "hostinfo".
	Kind() string
	// Origin says what is read, usually a path.
	Origin() string
	// Collect reads the source into sink. It does not return an error:
	// failures are part of the report.
	Collect(ctx context.Context, sink Sink) Report
}

// Tally counts skipped records by reason and keeps one example of each, so
// that a report can say what was skipped without listing every record.
type Tally struct {
	counts  map[string]int
	example map[string]string
	total   int
}

// Skip records that the record at ref could not be used for the given reason.
func (t *Tally) Skip(ref, reason string) {
	if t.counts == nil {
		t.counts = map[string]int{}
		t.example = map[string]string{}
	}
	if t.counts[reason] == 0 {
		t.example[reason] = ref
	}
	t.counts[reason]++
	t.total++
}

// Total returns the number of skipped records.
func (t *Tally) Total() int { return t.total }

// Notes returns one line per reason, most frequent first.
func (t *Tally) Notes() []string {
	reasons := make([]string, 0, len(t.counts))
	for r := range t.counts {
		reasons = append(reasons, r)
	}
	sort.Slice(reasons, func(i, j int) bool {
		if t.counts[reasons[i]] != t.counts[reasons[j]] {
			return t.counts[reasons[i]] > t.counts[reasons[j]]
		}
		return reasons[i] < reasons[j]
	})
	var out []string
	for _, r := range reasons {
		n := t.counts[r]
		if n == 1 {
			out = append(out, fmt.Sprintf("skipped %s: %s", t.example[r], r))
		} else {
			out = append(out, fmt.Sprintf("skipped %d records: %s (first: %s)", n, r, t.example[r]))
		}
	}
	return out
}

// StatusForError classifies a failure to open or read a source. A missing
// source is "absent", one that needs more privilege is "unreadable", and
// anything else is "error".
func StatusForError(err error) (model.SourceStatus, string) {
	switch {
	case err == nil:
		return model.SourceOK, ""
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
		return model.SourceAbsent, "not present"
	case errors.Is(err, fs.ErrPermission), errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return model.SourceUnreadable, "permission denied; readable as root"
	}
	return model.SourceError, err.Error()
}

// Collected is a Sink that keeps everything in memory. Tests and small
// tools use it; the ledger has its own Sink that writes as records arrive.
type Collected struct {
	Hosts    []*model.Host
	Boots    []*model.Boot
	Modules  []*model.Module
	Counters []*model.CounterSample
	Events   []*model.Event
	Actions  []*model.Action
	GPUs     []*model.GPUMem
	ECSs     []*model.ECSSample
}

func (c *Collected) Host(h *model.Host) error     { c.Hosts = append(c.Hosts, h); return nil }
func (c *Collected) Boot(b *model.Boot) error     { c.Boots = append(c.Boots, b); return nil }
func (c *Collected) Module(m *model.Module) error { c.Modules = append(c.Modules, m); return nil }
func (c *Collected) Counter(s *model.CounterSample) error {
	c.Counters = append(c.Counters, s)
	return nil
}
func (c *Collected) Event(e *model.Event) error   { c.Events = append(c.Events, e); return nil }
func (c *Collected) Action(a *model.Action) error { c.Actions = append(c.Actions, a); return nil }
func (c *Collected) GPU(g *model.GPUMem) error    { c.GPUs = append(c.GPUs, g); return nil }
func (c *Collected) ECS(s *model.ECSSample) error { c.ECSs = append(c.ECSs, s); return nil }
