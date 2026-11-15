package collect

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The manifest of a collection.
const (
	// ManifestName is the file at the top of a collection that says what
	// was asked of the BMC and what came back.
	ManifestName = "manifest.json"
	// Format names the manifest.
	Format = "my-redfish-collection"
	// Version is the manifest version this package writes. A reader accepts
	// manifests up to its own version.
	Version = 1
)

// Status of one request, as the manifest lists it.
const (
	StatusOK          = "ok"
	StatusUnsupported = "unsupported" // 404, 405, 410, 501
	StatusDenied      = "denied"      // 401, 403
	StatusTimeout     = "timeout"
	StatusError       = "error"
	// StatusSkipped: the resource was not asked for, on purpose: a log
	// that holds no hardware events, or a limit of the run.
	StatusSkipped = "skipped"
)

// Classes of resources, in the order of the support table.
const (
	ClassRoot         = "service-root"
	ClassSystem       = "system"
	ClassMemory       = "memory"
	ClassMetrics      = "memory-metrics"
	ClassBios         = "bios"
	ClassBiosSettings = "bios-settings"
	ClassRegistry     = "attribute-registry"
	ClassManager      = "manager"
	ClassLogService   = "log-service"
	ClassLogEntries   = "log-entries"
	ClassFirmware     = "firmware-inventory"
	ClassChassis      = "chassis"
)

// Classes lists the classes in the order of the support table.
var Classes = []string{ClassRoot, ClassSystem, ClassMemory, ClassMetrics, ClassBios, ClassBiosSettings, ClassRegistry,
	ClassManager, ClassLogService, ClassLogEntries, ClassFirmware, ClassChassis}

// States of a class in one collection.
const (
	StateOK = "ok"
	// StatePartial: some resources of the class came back and some did not.
	StatePartial = "partial"
	// StateNotOffered: the service has no link to the resource, so it was
	// not asked for.
	StateNotOffered = "not offered"
	// StateNotReached: the run ended before the class was asked for.
	StateNotReached = "not reached"
)

// Manifest says what one collection asked of a BMC and what came back.
type Manifest struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
	// Tool is the version of the tool that collected.
	Tool string `json:"tool,omitzero"`
	BMC  BMC    `json:"bmc"`

	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	// TLS says how the certificate of the BMC was checked: verified,
	// ca-file, fingerprint, insecure, or none for plain HTTP.
	TLS string `json:"tls"`
	// Auth is none, basic or session.
	Auth string `json:"auth"`
	// Expand is the $expand query the run used on collections, or "".
	Expand string `json:"expand,omitzero"`
	// Complete is set when every resource the run wanted was asked for:
	// the run was not stopped. Single resources may still have failed;
	// Classes says which.
	Complete bool `json:"complete"`
	// Stopped says why a run ended early.
	Stopped string `json:"stopped,omitzero"`

	Service  Service    `json:"service"`
	Systems  []System   `json:"systems,omitzero"`
	Managers []Manager  `json:"managers,omitzero"`
	Firmware []Firmware `json:"firmware,omitzero"`
	// Clock compares the clock of the BMC with the clock of the collector.
	Clock *Clock `json:"clock,omitzero"`

	Requests int   `json:"requests"`
	Bytes    int64 `json:"bytes"`
	// Counts is the number of resources per status.
	Counts map[string]int `json:"counts"`
	// Classes summarises the resources class by class: the row of this
	// BMC in a support table.
	Classes []ClassSummary `json:"classes"`
	// Resources lists every URI the run dealt with.
	Resources []Resource `json:"resources"`
	// Notes are remarks for the reader.
	Notes []string `json:"notes,omitzero"`
	// SecretsRemoved counts the places where an answer held the password
	// or the session token of the run and it was taken out before the
	// answer was written.
	SecretsRemoved int    `json:"secrets_removed,omitzero"`
	Limits         Limits `json:"limits"`
}

// BMC names the service that was asked.
type BMC struct {
	// Address is the host name or IP address, URL the address with scheme
	// and port.
	Address string `json:"address"`
	URL     string `json:"url"`
	// Name is what the collection is called: the name given in a targets
	// file, or the address.
	Name string `json:"name,omitzero"`
}

// Service is what the service root says about the service.
type Service struct {
	RedfishVersion string `json:"redfish_version,omitzero"`
	Vendor         string `json:"vendor,omitzero"`
	Product        string `json:"product,omitzero"`
}

// System is one ComputerSystem of the service.
type System struct {
	ID           string `json:"id"`
	Manufacturer string `json:"manufacturer,omitzero"`
	Model        string `json:"model,omitzero"`
	HostName     string `json:"host_name,omitzero"`
	BiosVersion  string `json:"bios_version,omitzero"`
	// AttributeRegistry is the registry the Bios resource names.
	AttributeRegistry string `json:"attribute_registry,omitzero"`
}

// Manager is one Manager of the service: the BMC itself.
type Manager struct {
	ID              string `json:"id"`
	Model           string `json:"model,omitzero"`
	FirmwareVersion string `json:"firmware_version,omitzero"`
}

// Firmware is one entry of the firmware inventory.
type Firmware struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitzero"`
	Version string `json:"version,omitzero"`
}

// Clock compares two clocks at one moment.
type Clock struct {
	BMC       time.Time `json:"bmc"`
	Collector time.Time `json:"collector"`
	// OffsetSeconds is the time of the BMC minus the time of the
	// collector.
	OffsetSeconds int64 `json:"offset_seconds"`
}

// ClassSummary is the outcome for one class of resources.
type ClassSummary struct {
	Class string `json:"class"`
	// State is ok, partial, unsupported, denied, timeout, error, skipped,
	// "not offered" or "not reached".
	State       string `json:"state"`
	OK          int    `json:"ok"`
	Unsupported int    `json:"unsupported,omitzero"`
	Denied      int    `json:"denied,omitzero"`
	Timeout     int    `json:"timeout,omitzero"`
	Error       int    `json:"error,omitzero"`
	Skipped     int    `json:"skipped,omitzero"`
}

// Resource is one URI of a run.
type Resource struct {
	URI    string `json:"uri"`
	Class  string `json:"class"`
	Status string `json:"status"`
	// HTTP is the status code of the answer, 0 when there was none.
	HTTP int `json:"http,omitzero"`
	// Bytes is the size of what was written, Millis the time the request
	// took, Attempts how often it was sent.
	Bytes    int   `json:"bytes,omitzero"`
	Millis   int64 `json:"ms,omitzero"`
	Attempts int   `json:"attempts,omitzero"`
	// File is where the answer was written, relative to the collection.
	File string `json:"file,omitzero"`
	// Via is "expand" for a resource that was not asked for by itself: it
	// was part of the answer for its collection.
	Via string `json:"via,omitzero"`
	// Pages is the number of requests a paged collection took, Members
	// the number of members that were read of it.
	Pages   int `json:"pages,omitzero"`
	Members int `json:"members,omitzero"`
	// Detail says why a resource has another status than ok.
	Detail string `json:"detail,omitzero"`
}

// Limits are the bounds a run was made with.
type Limits struct {
	TimeoutSeconds  int   `json:"timeout_seconds"`
	DeadlineSeconds int   `json:"deadline_seconds"`
	MaxLogEntries   int   `json:"max_log_entries"`
	MaxBodyBytes    int64 `json:"max_body_bytes"`
	MaxTotalBytes   int64 `json:"max_total_bytes"`
}

// summarize fills Counts and Classes from the resources. offered names the
// classes the service linked to; reached the classes the run got to.
func (m *Manifest) summarize(offered, reached map[string]bool) {
	m.Counts = map[string]int{}
	by := map[string]*ClassSummary{}
	for _, class := range Classes {
		by[class] = &ClassSummary{Class: class}
	}
	for _, r := range m.Resources {
		m.Counts[r.Status]++
		c := by[r.Class]
		if c == nil {
			continue
		}
		switch r.Status {
		case StatusOK:
			c.OK++
		case StatusUnsupported:
			c.Unsupported++
		case StatusDenied:
			c.Denied++
		case StatusTimeout:
			c.Timeout++
		case StatusSkipped:
			c.Skipped++
		default:
			c.Error++
		}
	}
	m.Classes = m.Classes[:0]
	for _, class := range Classes {
		c := by[class]
		failed := c.Unsupported + c.Denied + c.Timeout + c.Error
		switch {
		case c.OK > 0 && failed == 0:
			c.State = StateOK
		case c.OK > 0:
			c.State = StatePartial
		case failed > 0:
			// The reason most resources of the class share; a reason a
			// person can act on wins a tie.
			c.State = StatusError
			best := c.Error
			for _, alt := range []struct {
				n     int
				state string
			}{{c.Timeout, StatusTimeout}, {c.Unsupported, StatusUnsupported}, {c.Denied, StatusDenied}} {
				if alt.n >= best && alt.n > 0 {
					best, c.State = alt.n, alt.state
				}
			}
		case c.Skipped > 0:
			c.State = StatusSkipped
		case reached[class] && !offered[class]:
			c.State = StateNotOffered
		case offered[class] && reached[class]:
			// Offered and reached, and nothing to ask for: an empty
			// collection.
			c.State = StateOK
		case offered[class]:
			c.State = StateNotReached
		case m.Complete:
			c.State = StateNotOffered
		default:
			c.State = StateNotReached
		}
		m.Classes = append(m.Classes, *c)
	}
}

// Class returns the summary of a class, or nil.
func (m *Manifest) Class(class string) *ClassSummary {
	for i := range m.Classes {
		if m.Classes[i].Class == class {
			return &m.Classes[i]
		}
	}
	return nil
}

// Failed returns the number of resources that were asked for and did not
// come back.
func (m *Manifest) Failed() int {
	return m.Counts[StatusUnsupported] + m.Counts[StatusDenied] + m.Counts[StatusTimeout] + m.Counts[StatusError]
}

// Partial reports whether the collection lacks something a person may be
// able to get: the run was stopped, or a resource was denied, timed out or
// failed. A resource the service does not have is no such lack.
func (m *Manifest) Partial() bool {
	return !m.Complete || m.Counts[StatusDenied]+m.Counts[StatusTimeout]+m.Counts[StatusError] > 0
}

// maxManifest bounds the size of a manifest that is read.
const maxManifest = 64 << 20

// ErrNoManifest: the data is not a collection manifest.
var ErrNoManifest = errors.New("not a manifest of a Redfish collection")

// ParseManifest reads a manifest.
func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoManifest, err)
	}
	if m.Format != Format {
		return nil, ErrNoManifest
	}
	if m.Version < 1 || m.Version > Version {
		return nil, fmt.Errorf("manifest version %d: this tool reads versions up to %d", m.Version, Version)
	}
	if m.Counts == nil {
		m.Counts = map[string]int{}
	}
	return &m, nil
}

// ReadManifest reads the manifest of the collection in dir.
func ReadManifest(dir string) (*Manifest, error) {
	f, err := os.Open(filepath.Join(dir, ManifestName))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxManifest+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxManifest {
		return nil, fmt.Errorf("%s: larger than %d bytes", ManifestName, maxManifest)
	}
	return ParseManifest(data)
}

// FindManifests returns the manifests of the collections at and below the
// given paths, in the order of their names. A path may be a collection, a
// directory that holds one collection per BMC, or a manifest file.
func FindManifests(paths ...string) ([]*Manifest, error) {
	var out []*Manifest
	seen := map[string]bool{}
	add := func(file string) error {
		abs, err := filepath.Abs(file)
		if err != nil || seen[abs] {
			return err
		}
		seen[abs] = true
		m, err := ReadManifest(filepath.Dir(file))
		if err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
		out = append(out, m)
		return nil
	}
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !fi.IsDir() {
			if err := add(p); err != nil {
				return nil, err
			}
			continue
		}
		if _, err := os.Stat(filepath.Join(p, ManifestName)); err == nil {
			if err := add(filepath.Join(p, ManifestName)); err != nil {
				return nil, err
			}
			continue
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			return nil, err
		}
		found := 0
		for _, e := range entries {
			file := filepath.Join(p, e.Name(), ManifestName)
			if _, err := os.Stat(file); err != nil {
				if errors.Is(err, fs.ErrNotExist) || !e.IsDir() {
					continue
				}
				return nil, err
			}
			if err := add(file); err != nil {
				return nil, err
			}
			found++
		}
		if found == 0 {
			return nil, fmt.Errorf("%s: no %s here or one directory below; a collection is what \"my collect redfish -o DIR\" writes", p, ManifestName)
		}
	}
	return out, nil
}

// Support is a support table: which BMC gave which class of resources.
// BMCs of the same machine and firmware that gave the same are one group,
// so that a fleet reads as a few columns.
type Support struct {
	// Classes are the rows of the table, in order.
	Classes []string       `json:"classes"`
	Groups  []SupportGroup `json:"groups"`
}

// SupportGroup is the BMCs of one machine model and firmware with the same
// outcome for every class.
type SupportGroup struct {
	Vendor         string `json:"vendor,omitzero"`
	Model          string `json:"model,omitzero"`
	Firmware       string `json:"firmware,omitzero"`
	BiosVersion    string `json:"bios_version,omitzero"`
	RedfishVersion string `json:"redfish_version,omitzero"`
	// States has the state of each class, in the order of Classes.
	States []string     `json:"states"`
	BMCs   []SupportBMC `json:"bmcs"`
}

// SupportBMC is one collection of a group.
type SupportBMC struct {
	Name        string    `json:"name"`
	Address     string    `json:"address,omitzero"`
	CollectedAt time.Time `json:"collected_at"`
	Complete    bool      `json:"complete"`
	Stopped     string    `json:"stopped,omitzero"`
}

// State returns the state of a class in the group.
func (g *SupportGroup) State(class string) string {
	for i, c := range Classes {
		if c == class && i < len(g.States) {
			return g.States[i]
		}
	}
	return StateNotReached
}

// NewSupport builds the support table of some collections.
func NewSupport(manifests []*Manifest) *Support {
	s := &Support{Classes: append([]string{}, Classes...), Groups: []SupportGroup{}}
	sorted := append([]*Manifest{}, manifests...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].BMC.Name < sorted[j].BMC.Name })
	index := map[string]int{}
	for _, m := range sorted {
		g := SupportGroup{RedfishVersion: m.Service.RedfishVersion, Vendor: m.Service.Vendor}
		if len(m.Systems) > 0 {
			g.Model, g.BiosVersion = m.Systems[0].Model, m.Systems[0].BiosVersion
			if v := m.Systems[0].Manufacturer; v != "" {
				g.Vendor = v
			}
		}
		if len(m.Managers) > 0 {
			g.Firmware = m.Managers[0].FirmwareVersion
		}
		for _, class := range Classes {
			state := StateNotReached
			if c := m.Class(class); c != nil && c.State != "" {
				state = c.State
			}
			g.States = append(g.States, state)
		}
		b := SupportBMC{Name: m.BMC.Name, Address: m.BMC.Address, CollectedAt: m.StartedAt, Complete: m.Complete, Stopped: m.Stopped}
		if b.Name == "" {
			b.Name = m.BMC.Address
		}
		key := strings.Join(append([]string{g.Vendor, g.Model, g.Firmware, g.BiosVersion, g.RedfishVersion}, g.States...), "\x00")
		i, ok := index[key]
		if !ok {
			i = len(s.Groups)
			index[key] = i
			s.Groups = append(s.Groups, g)
		}
		s.Groups[i].BMCs = append(s.Groups[i].BMCs, b)
	}
	return s
}

// SafeName turns the name of a BMC into a name for a directory: letters,
// digits, dots, dashes and underscores stay, everything else becomes an
// underscore.
func SafeName(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "._")
	if out == "" {
		return "bmc"
	}
	if len(out) > 100 {
		out = out[:100]
	}
	return out
}
