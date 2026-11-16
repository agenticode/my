package redfish

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
)

// A collection made by "my collect redfish" carries a manifest: what was
// asked of the BMC and what came back. A dump made by other means has
// none, and then only what is in the dump can be said.
const (
	manifestName   = "manifest.json"
	manifestFormat = "my-redfish-collection"
	// manifestVersion is the newest manifest this reader knows.
	manifestVersion = 1
)

// manifest is the part of a collection manifest this reader uses.
type manifest struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
	BMC     struct {
		Address string `json:"address"`
	} `json:"bmc"`
	StartedAt time.Time `json:"started_at"`
	TLS       string    `json:"tls"`
	Auth      string    `json:"auth"`
	Complete  bool      `json:"complete"`
	Stopped   string    `json:"stopped"`
	Service   struct {
		RedfishVersion string `json:"redfish_version"`
	} `json:"service"`
	Clock *struct {
		OffsetSeconds int64 `json:"offset_seconds"`
	} `json:"clock"`
	Classes []struct {
		Class string `json:"class"`
		State string `json:"state"`
	} `json:"classes"`
	Resources []struct {
		URI    string `json:"uri"`
		Class  string `json:"class"`
		Status string `json:"status"`
		Detail string `json:"detail"`
	} `json:"resources"`
	Notes []string `json:"notes"`
}

// parseManifest reads the manifest of a collection. A file that is none,
// or one written by a later version, yields a note and no manifest.
func parseManifest(data []byte) (*manifest, string) {
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil || m.Format != manifestFormat {
		return nil, "" // some other file named manifest.json
	}
	if m.Version < 1 || m.Version > manifestVersion {
		return nil, fmt.Sprintf("the collection has a manifest of version %d, which this version does not read: the dump is read without it", m.Version)
	}
	if y := m.StartedAt.UTC().Year(); !m.StartedAt.IsZero() && (y < 1970 || y > 9999) {
		m.StartedAt = time.Time{}
	}
	return &m, ""
}

// status returns what the manifest says became of a URI, or "".
func (m *manifest) status(uri string) (status, detail string) {
	if m == nil {
		return "", ""
	}
	uri = strings.TrimRight(uri, "/")
	for _, r := range m.Resources {
		if strings.TrimRight(r.URI, "/") == uri {
			return r.Status, r.Detail
		}
	}
	return "", ""
}

// logOf returns the URI of the log an entry belongs to: the part of its
// URI before "/Entries".
func logOf(entryID string) string {
	if i := strings.LastIndex(entryID, "/Entries"); i > 0 {
		return entryID[:i]
	}
	return ""
}

// logPath returns the log an entry counts for: the log its URI names, or,
// for entries that do not say which log they are from, the file they are
// in.
func logPath(res *resource) string {
	if p := logOf(res.id); p != "" {
		return p
	}
	return res.file
}

// entryTime reads the time of a log entry.
//
// EventTimestamp is when the event happened, Created when the entry was
// written: the time of the record is the first, when it is given. The
// text that identifies the entry is Created as it stands.
func entryTime(o object) (string, time.Time, bool) {
	created := str(o, "Created")
	if at, ok := parseTime(str(o, "EventTimestamp")); ok {
		if created == "" {
			created = str(o, "EventTimestamp")
		}
		return created, at, true
	}
	if at, ok := parseTime(created); ok {
		return created, at, true
	}
	return created, time.Time{}, false
}

// cover notes one entry of a log, of any kind, for the period the log
// covers.
func (r *run) cover(res *resource) {
	path := logPath(res)
	c := r.coverage[path]
	if c == nil {
		c = &model.BMCLog{ID: lastSegment(path), Path: path}
		r.coverage[path] = c
	}
	c.Entries++
	if _, at, ok := entryTime(res.obj); ok {
		if c.First.IsZero() || at.Before(c.First) {
			c.First = at
		}
		if at.After(c.Last) {
			c.Last = at
		}
	}
}

// coverMemory notes that an entry is about memory.
func (r *run) coverMemory(res *resource) {
	if c := r.coverage[logPath(res)]; c != nil {
		c.Memory++
	}
}

// logCoverage completes what was read of each log with what the log
// service and the collection say about it, and returns the logs in the
// order of their paths.
func (r *run) logCoverage() []model.BMCLog {
	// What the log services say.
	for i := range r.logSvcs {
		o := r.logSvcs[i].obj
		path := strings.TrimRight(str(o, "@odata.id"), "/")
		c := r.coverage[path]
		if c == nil {
			continue // a log that was not read, or holds no entry
		}
		if id := str(o, "Id"); id != "" {
			c.ID = id
		}
		c.Policy = str(o, "OverWritePolicy")
		if n, ok := integer(o, "MaxNumberOfRecords"); ok && n > 0 && n < 1<<31 {
			c.Max = model.Some(int(n))
		}
	}
	paths := make([]string, 0, len(r.coverage))
	for p := range r.coverage {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	out := make([]model.BMCLog, 0, len(paths))
	for _, p := range paths {
		c := r.coverage[p]
		// How many entries the log holds, as its collection says.
		if n, ok := r.counts[p+"/Entries"]; ok && n < 1<<31 {
			c.Total = model.Some(int(n))
			if int(n) > c.Entries {
				c.Truncated = true
			}
		}
		// A collection that was cut short, or lost a page, says so.
		if status, detail := r.manifest.status(p + "/Entries"); status != "" && (status != "ok" || strings.Contains(detail, " read")) && detail != "" {
			c.Truncated = true
		}
		out = append(out, *c)
	}
	return out
}

// classesOf says what became of each class of resources. A collection
// says it in its manifest; for a dump without one, only whether resources
// of the class are in the dump can be said.
func (r *run) classesOf(pending bool) []model.BMCClass {
	if r.manifest != nil && len(r.manifest.Classes) > 0 {
		out := make([]model.BMCClass, 0, len(r.manifest.Classes))
		for _, c := range r.manifest.Classes {
			if c.Class != "" && c.State != "" {
				out = append(out, model.BMCClass{Class: c.Class, State: c.State})
			}
		}
		return out
	}
	present := func(n int) string {
		if n > 0 {
			return "present"
		}
		return "absent"
	}
	b := 0
	if pending {
		b = 1
	}
	return []model.BMCClass{
		{Class: "service-root", State: present(len(r.roots))},
		{Class: "system", State: present(len(r.systems))},
		{Class: "memory", State: present(len(r.modules))},
		{Class: "memory-metrics", State: present(len(r.metrics))},
		{Class: "bios", State: present(len(r.bios) - b)},
		{Class: "bios-settings", State: present(b)},
		{Class: "attribute-registry", State: present(len(r.registries))},
		{Class: "manager", State: present(len(r.managers))},
		{Class: "log-service", State: present(len(r.logSvcs))},
		{Class: "log-entries", State: present(len(r.entries))},
	}
}

// controller records the reading of the BMC itself: what it is, what it
// runs, how complete the reading was and what its logs cover.
func (r *run) controller(sink adapter.Sink) error {
	if r.found() == 0 {
		// Nothing was read of any BMC. A file that is no JSON counts as
		// a record of the report, and makes no BMC.
		return nil
	}
	b := &model.BMC{ObservedAt: r.now, Source: Kind, RawRef: r.origin, LastReset: r.lastReset}
	for _, root := range r.roots {
		if v := str(root.obj, "RedfishVersion"); v != "" {
			b.RedfishVersion = v
			break
		}
	}
	for _, m := range r.managers {
		// The BMC among the managers: the first one that names its firmware.
		if v := str(m.obj, "FirmwareVersion"); v != "" {
			b.Firmware, b.Manager = v, str(m.obj, "Model")
			break
		}
	}
	for _, sys := range r.systems {
		if v := str(sys.obj, "BiosVersion"); v != "" {
			b.BIOSVersion = v
			break
		}
	}
	current, pending := r.biosResources()
	if current != nil {
		b.BIOSRegistry = str(current.obj, "AttributeRegistry")
	}
	b.Classes = r.classesOf(pending != nil)
	b.Logs = r.logCoverage()
	if m := r.manifest; m != nil {
		b.Live, b.Source, b.RawRef = true, Kind+"/manifest", r.origin+"/"+manifestName
		b.Address, b.TLS, b.Auth = m.BMC.Address, m.TLS, m.Auth
		b.Complete = model.Some(m.Complete)
		if b.RedfishVersion == "" {
			b.RedfishVersion = m.Service.RedfishVersion
		}
		if m.Clock != nil {
			b.ClockOffset = model.Some(m.Clock.OffsetSeconds)
		}
		if m.Stopped != "" {
			b.Notes = append(b.Notes, "the collection stopped early: "+m.Stopped)
			r.rep.Notes = append(r.rep.Notes, "the collection from the BMC stopped early: "+m.Stopped)
		}
		b.Notes = append(b.Notes, m.Notes...)
	}
	b.Fingerprint = model.Fingerprint(Kind, "bmc", b.Address, r.now.Format(time.RFC3339))
	return sink.BMC(b)
}
