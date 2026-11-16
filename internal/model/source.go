package model

import "time"

// Scan is one collection run: a live scan of a host, an import of files, or
// a synthetic data set.
type Scan struct {
	ID          int64     `json:"id,omitzero"`
	Host        string    `json:"host"`
	Kind        string    `json:"kind"` // ScanLive, ScanImport or ScanSynthetic
	StartedAt   time.Time `json:"started_at"`
	FinishedAt  time.Time `json:"finished_at,omitzero"`
	ToolVersion string    `json:"tool_version"`
	// Privileged is set when the scan ran as root.
	Privileged bool   `json:"privileged"`
	Note       string `json:"note,omitzero"`
}

// Scan kinds.
const (
	ScanLive      = "live"
	ScanImport    = "import"
	ScanSynthetic = "synthetic"
)

// SourceStatus is what happened when a source was read.
type SourceStatus string

const (
	// SourceOK: read completely.
	SourceOK SourceStatus = "ok"
	// SourcePartial: read, but some records could not be used.
	SourcePartial SourceStatus = "partial"
	// SourceEmpty: present and readable, with no relevant records.
	SourceEmpty SourceStatus = "empty"
	// SourceAbsent: not present on this host.
	SourceAbsent SourceStatus = "absent"
	// SourceUnreadable: present but not readable, usually for lack of privilege.
	SourceUnreadable SourceStatus = "unreadable"
	// SourceError: reading failed for another reason.
	SourceError SourceStatus = "error"
)

// Source is one entry of the source inventory of a scan. Every source that
// was looked for has an entry, including the ones that could not be read, so
// that a reader can tell what a finding did not see.
type Source struct {
	ID     int64  `json:"id,omitzero"`
	ScanID int64  `json:"scan_id"`
	Kind   string `json:"kind"`   // adapter name: rasdaemon, klog, edac-sysfs, smbios, hostinfo, ...
	Origin string `json:"origin"` // path or description of what was read

	Status SourceStatus `json:"status"`
	// Detail explains a status other than ok: the error text, or what would
	// make the source readable.
	Detail string `json:"detail,omitzero"`

	Records int `json:"records"` // original records read
	Events  int `json:"events"`  // events produced
	// Informational counts the events among Events that are no error: the
	// record of a stop of the host or of a panic. It is not kept in the
	// ledger.
	Informational int `json:"informational,omitzero"`
	Actions       int `json:"actions"` // actions produced
	Skipped       int `json:"skipped"` // records that could not be used
	// Unread counts the records among Skipped that the reader did not
	// understand: a record it could not parse, or one whose reading
	// failed.
	Unread int `json:"unread,omitzero"`
	// Faults counts the faults of the reader that were caught: a record
	// whose reading panicked, or the whole source when the reader failed.
	Faults int `json:"faults,omitzero"`
	// Samples counts the readings produced that are neither events nor
	// actions: GPU memory records and ECS register readings.
	Samples int `json:"samples,omitzero"`
	// GradeA, GradeB and GradeC count the events produced per observation grade.
	GradeA int `json:"grade_a"`
	GradeB int `json:"grade_b"`
	GradeC int `json:"grade_c"`

	FirstTS time.Time `json:"first_ts,omitzero"`
	LastTS  time.Time `json:"last_ts,omitzero"`
	// Notes are remarks for the reader: skipped record reasons with counts,
	// tables that were present but not interpreted, and similar.
	Notes []string `json:"notes,omitzero"`
}

// BestGrade returns the best observation grade among the events the source
// produced, or "" when it produced none.
func (s *Source) BestGrade() Grade {
	switch {
	case s.GradeA > 0:
		return GradeA
	case s.GradeB > 0:
		return GradeB
	case s.GradeC > 0:
		return GradeC
	}
	return ""
}

// CaptureCheck is what "my preflight" found for one check when a live scan
// ran: whether a reset right after a fatal memory error would keep the
// records of that kind.
type CaptureCheck struct {
	ScanID int64  `json:"scan_id,omitzero"`
	Host   string `json:"host,omitzero"`
	Name   string `json:"name"`
	// State is kept, lost, none, unknown or info.
	State    string    `json:"state"`
	Detail   Statement `json:"detail"`
	Evidence []string  `json:"evidence,omitzero"`
}
