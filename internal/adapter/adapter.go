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
	// BMC takes a reading of a management controller: what it is, what it
	// runs, and how complete the reading was.
	BMC(*model.BMC) error
	// BIOSSetting takes a setting of the system firmware as a BMC reports
	// it.
	BIOSSetting(*model.BIOSSetting) error
	// BMCMemory takes a memory module as a BMC describes it, with the
	// error counts the BMC keeps for it.
	BMCMemory(*model.BMCMemory) error
	// Firmware takes a firmware record about memory that makes neither an
	// event nor an action: a module detected, a configuration in effect.
	// A firmware record that makes an event or an action travels on it.
	Firmware(*model.FirmwareRecord) error
	// Registry takes a message registry a BMC published, with how much of
	// it this version classes.
	Registry(*model.RegistrySeen) error
	// Reading takes the text of a file or command about the memory of the
	// host (the Memory Explorer); it holds no error record.
	Reading(*model.HostReading) error
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
	// Unread is the number of records among Skipped the reader did not
	// understand; UnreadRecords keeps those whose text names memory.
	Unread        int
	UnreadRecords []model.UnreadRecord
	// Faults counts the faults of the reader that were caught.
	Faults int
	// Failed says that the reader itself failed: what it gave is rolled
	// back and the source counts as not read.
	Failed bool
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
	// Records not understood, faults caught, and the texts kept of the
	// records not understood that name memory.
	unread, faults int
	kept           []model.UnreadRecord
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
	Hosts      []*model.Host
	Boots      []*model.Boot
	Modules    []*model.Module
	Counters   []*model.CounterSample
	Events     []*model.Event
	Actions    []*model.Action
	GPUs       []*model.GPUMem
	ECSs       []*model.ECSSample
	BMCs       []*model.BMC
	Settings   []*model.BIOSSetting
	Memory     []*model.BMCMemory
	Firmwares  []*model.FirmwareRecord
	Registries []*model.RegistrySeen
	Readings   []*model.HostReading
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
func (c *Collected) BMC(b *model.BMC) error       { c.BMCs = append(c.BMCs, b); return nil }
func (c *Collected) BIOSSetting(s *model.BIOSSetting) error {
	c.Settings = append(c.Settings, s)
	return nil
}
func (c *Collected) BMCMemory(m *model.BMCMemory) error {
	c.Memory = append(c.Memory, m)
	return nil
}
func (c *Collected) Firmware(f *model.FirmwareRecord) error {
	c.Firmwares = append(c.Firmwares, f)
	return nil
}
func (c *Collected) Registry(r *model.RegistrySeen) error {
	c.Registries = append(c.Registries, r)
	return nil
}
func (c *Collected) Reading(r *model.HostReading) error {
	c.Readings = append(c.Readings, r)
	return nil
}

// KindUnknown is the source kind of an input no reader recognised. Such a
// source is listed with what the input looks like; it is no source that
// was missed, since nobody knows whether it held memory records.
const KindUnknown = model.SourceKindUnknown

// Unrecognised returns an adapter that reads nothing and reports an input
// no reader recognised, with the reason as its detail.
func Unrecognised(origin, reason string) Adapter {
	return &unrecognised{origin: origin, reason: reason}
}

type unrecognised struct{ origin, reason string }

func (u *unrecognised) Kind() string   { return KindUnknown }
func (u *unrecognised) Origin() string { return u.origin }
func (u *unrecognised) Collect(context.Context, Sink) Report {
	return Report{Kind: KindUnknown, Origin: u.origin, Status: model.SourceError, Detail: u.reason}
}
