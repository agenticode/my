// Package exporter turns the findings about a host into Prometheus metrics
// and writes them in the text exposition format.
//
// The metrics carry findings and counts per DIMM, GPU and source. They
// never carry an address, a row or a column as a label: the number of time
// series stays bounded by the number of DIMMs, and the detail is what the
// commands and the JSON API show.
package exporter

import (
	"bufio"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Metric types of the exposition format.
const (
	Gauge   = "gauge"
	Counter = "counter"
)

// Desc describes one metric.
type Desc struct {
	Name string
	Type string
	// Help is the one-line description written with the metric.
	Help string
	// Labels are the names of its labels, in the order they are written.
	Labels []string
}

// The metrics about the memory of a host. Every one of them carries the
// host name: one exporter per machine repeats it on each series, and an
// exporter that reads the records of a fleet tells the machines apart by it.
var (
	DIMMVerdict = &Desc{Name: "my_dimm_verdict", Type: Gauge, Labels: []string{"host", "slot", "serial"},
		Help: "Finding for the DIMM in the slot: 0 ok, 1 observe, 2 consider replacement, 3 replace now, 4 hold replacement."}
	DIMMCorrected = &Desc{Name: "my_dimm_ce_total", Type: Counter, Labels: []string{"host", "slot", "serial"},
		Help: "Corrected errors on record for the DIMM, counted from error records. An empty serial counts errors of the slot that could not be tied to a DIMM."}
	DIMMUncorrected = &Desc{Name: "my_dimm_ue_total", Type: Counter, Labels: []string{"host", "slot", "serial"},
		Help: "Uncorrected errors on record for the DIMM, counted from error records. An empty serial counts errors of the slot that could not be tied to a DIMM."}
	DIMMFaults = &Desc{Name: "my_dimm_faults", Type: Gauge, Labels: []string{"host", "slot", "mode"},
		Help: "Faults of the DIMM in the slot by fault mode. A fault is a group of errors with one cause."}
	DIMMOfflinedPages = &Desc{Name: "my_dimm_offlined_pages", Type: Gauge, Labels: []string{"host", "slot"},
		Help: "Pages of the DIMM in the slot that were taken offline."}
	DIMMRepairs = &Desc{Name: "my_dimm_repairs_total", Type: Counter, Labels: []string{"host", "slot", "type"},
		Help: "Repairs on record for the DIMM in the slot by type: sppr, hppr, sparing, row-remap. Attempts that failed count too."}
	GPURemapBanks = &Desc{Name: "my_gpu_remap_banks", Type: Gauge, Labels: []string{"host", "gpu", "bucket"},
		Help: "Memory banks of the GPU by the spare rows they have left for row remapping: max, high, partial, low, none."}
	GPURemapFailure = &Desc{Name: "my_gpu_remap_failure", Type: Gauge, Labels: []string{"host", "gpu"},
		Help: "1 when a row remapping of the GPU failed, 0 when the GPU reports none."}
	SourceGrade = &Desc{Name: "my_source_grade", Type: Gauge, Labels: []string{"host", "source", "status"},
		Help: "Best observation grade of the error records a source gave in the latest scan: 3 A (bank, row, column), 2 B (address or rank), 1 C (counts), 0 no error records. status says how reading the source went."}
)

// The metrics about the exporter itself.
var (
	BuildInfo = &Desc{Name: "my_exporter_build_info", Type: Gauge, Labels: []string{"version", "revision", "engine"},
		Help: "1, with the version of the program and of its analysis engine."}
	LastScan = &Desc{Name: "my_exporter_last_scan_timestamp_seconds", Type: Gauge,
		Help: "When the scan behind these metrics finished, in seconds since the epoch."}
	ScanDuration = &Desc{Name: "my_exporter_last_scan_duration_seconds", Type: Gauge,
		Help: "How long the scan behind these metrics took."}
	ScanFailures = &Desc{Name: "my_exporter_scan_failures_total", Type: Counter,
		Help: "Scans that failed since the exporter started. The metrics stay those of the last scan that succeeded."}
)

// Descs lists every metric in the order they are written.
var Descs = []*Desc{
	DIMMVerdict, DIMMCorrected, DIMMUncorrected, DIMMFaults, DIMMOfflinedPages, DIMMRepairs,
	GPURemapBanks, GPURemapFailure, SourceGrade,
	BuildInfo, LastScan, ScanDuration, ScanFailures,
}

// sample is one time series of a metric.
type sample struct {
	labels []string
	value  float64
}

type family struct {
	samples []sample
	index   map[string]int
}

// Set holds samples of the metrics. The zero value is ready to use; a Set
// is not safe for use by several goroutines.
type Set struct {
	families map[*Desc]*family
}

// Add records a sample. values are the values of the labels of the metric,
// in order; missing ones are empty. Two samples of one metric with equal
// labels become one: counters add up, a gauge keeps the larger value. The
// exposition format allows a series only once, and a scrape that breaks
// that rule is refused as a whole.
func (s *Set) Add(d *Desc, value float64, values ...string) {
	if s.families == nil {
		s.families = map[*Desc]*family{}
	}
	f := s.families[d]
	if f == nil {
		f = &family{index: map[string]int{}}
		s.families[d] = f
	}
	labels := make([]string, len(d.Labels))
	for i := range labels {
		if i < len(values) {
			labels[i] = strings.ToValidUTF8(values[i], "�")
		}
	}
	key := strings.Join(labels, "\x00")
	if i, ok := f.index[key]; ok {
		if d.Type == Counter {
			f.samples[i].value += value
		} else {
			f.samples[i].value = math.Max(f.samples[i].value, value)
		}
		return
	}
	f.index[key] = len(f.samples)
	f.samples = append(f.samples, sample{labels: labels, value: value})
}

// Merge adds the samples of another set.
func (s *Set) Merge(other *Set) {
	if other == nil {
		return
	}
	for _, d := range Descs {
		f := other.families[d]
		if f == nil {
			continue
		}
		for _, sm := range f.samples {
			s.Add(d, sm.value, sm.labels...)
		}
	}
}

// Series returns the number of time series in the set.
func (s *Set) Series() int {
	n := 0
	for _, f := range s.families {
		n += len(f.samples)
	}
	return n
}

// Value returns the value of the series of a metric with the given label
// values, and whether the set holds it.
func (s *Set) Value(d *Desc, values ...string) (float64, bool) {
	f := s.families[d]
	if f == nil {
		return 0, false
	}
	labels := make([]string, len(d.Labels))
	copy(labels, values)
	i, ok := f.index[strings.Join(labels, "\x00")]
	if !ok {
		return 0, false
	}
	return f.samples[i].value, true
}

// ContentType is the media type of what WriteTo writes.
const ContentType = "text/plain; version=0.0.4; charset=utf-8"

// WriteTo writes the set in the Prometheus text exposition format: the
// metrics in the order of Descs, the series of a metric ordered by their
// labels. A metric without series is left out.
func (s *Set) WriteTo(w io.Writer) (int64, error) {
	cw := &countWriter{w: w}
	bw := bufio.NewWriter(cw)
	for _, d := range Descs {
		f := s.families[d]
		if f == nil || len(f.samples) == 0 {
			continue
		}
		bw.WriteString("# HELP ")
		bw.WriteString(d.Name)
		bw.WriteByte(' ')
		bw.WriteString(helpEscaper.Replace(d.Help))
		bw.WriteString("\n# TYPE ")
		bw.WriteString(d.Name)
		bw.WriteByte(' ')
		bw.WriteString(d.Type)
		bw.WriteByte('\n')

		order := make([]int, len(f.samples))
		for i := range order {
			order[i] = i
		}
		sort.Slice(order, func(a, b int) bool {
			x, y := f.samples[order[a]].labels, f.samples[order[b]].labels
			for i := range x {
				if x[i] != y[i] {
					return x[i] < y[i]
				}
			}
			return false
		})
		for _, i := range order {
			sm := f.samples[i]
			bw.WriteString(d.Name)
			if len(d.Labels) > 0 {
				bw.WriteByte('{')
				for j, name := range d.Labels {
					if j > 0 {
						bw.WriteByte(',')
					}
					bw.WriteString(name)
					bw.WriteString(`="`)
					bw.WriteString(labelEscaper.Replace(sm.labels[j]))
					bw.WriteByte('"')
				}
				bw.WriteByte('}')
			}
			bw.WriteByte(' ')
			bw.WriteString(formatValue(sm.value))
			bw.WriteByte('\n')
		}
	}
	err := bw.Flush()
	if err == nil {
		err = cw.err
	}
	return cw.n, err
}

// The text format escapes a backslash and a line feed in a help text, and
// a double quote as well in a label value.
var (
	helpEscaper  = strings.NewReplacer(`\`, `\\`, "\n", `\n`)
	labelEscaper = strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`)
)

// formatValue writes a sample value: whole numbers without a fraction, so
// that counts read as counts.
func formatValue(v float64) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

type countWriter struct {
	w   io.Writer
	n   int64
	err error
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	if err != nil && c.err == nil {
		c.err = err
	}
	return n, err
}
