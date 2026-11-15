package nvidia

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
)

// The selective queries of nvidia-smi print one line per GPU with the
// properties the caller asked for:
//
//	nvidia-smi --query-gpu=index,uuid,remapped_rows.failure --format=csv
//	index, uuid, remapped_rows.failure
//	0, GPU-b850f46d-d5ea-c752-ddf3-c4453e44d3f7, No
//
// The first line names the columns, with the unit of a value in brackets
// ("memory.total [MiB]"); "noheader" leaves that line out and "nounits" the
// units. The columns come in the order of the query and can be any subset,
// so they are read by name. The names are those "nvidia-smi
// --help-query-gpu" lists; some have a second name, and both are read.
// --query-remapped-rows prints the same row remapping columns and names the
// GPU by gpu_bus_id, gpu_name, gpu_serial and gpu_uuid.
//
// A value in square brackets ("[N/A]", "[Not Supported]") says that the GPU
// or its driver does not have it.

// csvName writes a column name the way the table below spells it: without
// the unit and in lower case.
func csvName(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, " ["); i > 0 && strings.HasSuffix(s, "]") {
		s = s[:i]
	}
	return strings.ToLower(s)
}

// csvAlias gives the first name of a property for its second name.
var csvAlias = map[string]string{
	"gpu_name":          "name",
	"gpu_serial":        "serial",
	"gpu_uuid":          "uuid",
	"gpu_bus_id":        "pci.bus_id",
	"retired_pages.sbe": "retired_pages.single_bit_ecc.count",
	"retired_pages.dbe": "retired_pages.double_bit.count",
	"remapped_rows.sbe": "remapped_rows.correctable",
	"remapped_rows.dbe": "remapped_rows.uncorrectable",
}

// csvRead lists the properties a snapshot is made of.
var csvRead = func() map[string]bool {
	m := map[string]bool{
		"timestamp": true, "index": true, "name": true, "serial": true, "uuid": true, "pci.bus_id": true,
		"driver_version": true, "kmd_version": true, "ecc.mode.current": true,
		"ecc.errors.uncorrected.aggregate.sram.thresholdexceeded": true,
		"retired_pages.single_bit_ecc.count":                      true, "retired_pages.double_bit.count": true, "retired_pages.pending": true,
		"remapped_rows.correctable": true, "remapped_rows.uncorrectable": true, "remapped_rows.pending": true, "remapped_rows.failure": true,
	}
	for _, kind := range []string{"corrected", "uncorrected"} {
		for _, period := range []string{"volatile", "aggregate"} {
			for _, part := range []string{"dram", "device_memory", "sram", "total"} {
				m["ecc.errors."+kind+"."+period+"."+part] = true
			}
		}
	}
	for _, period := range []string{"volatile", "aggregate"} {
		m["ecc.errors.uncorrected."+period+".sram.parity"] = true
		m["ecc.errors.uncorrected."+period+".sram.secded"] = true
	}
	for _, b := range []string{"max", "high", "partial", "low", "none"} {
		m["remapped_rows.histogram."+b] = true
	}
	return m
}()

// csvIdentity are the properties that name a GPU. A header line is told
// from a line of values by them.
var csvIdentity = map[string]bool{"index": true, "name": true, "serial": true, "uuid": true, "pci.bus_id": true, "timestamp": true}

// csvOther are properties of the other selective queries of nvidia-smi,
// which list processes and retired pages one by one.
var csvOther = map[string]string{
	"pid":                     "--query-compute-apps or --query-accounted-apps (a list of processes)",
	"process_name":            "--query-compute-apps (a list of processes)",
	"used_gpu_memory":         "--query-compute-apps (a list of processes)",
	"used_memory":             "--query-compute-apps (a list of processes)",
	"retired_pages.address":   "--query-retired-pages (a list of retired pages)",
	"retired_pages.cause":     "--query-retired-pages (a list of retired pages)",
	"retired_pages.timestamp": "--query-retired-pages (a list of retired pages)",
}

var (
	// A property name: letters, digits, dots and underscores, and the unit
	// some names carry in the header.
	csvNameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.]*( \[[^\]\[]{1,24}\])?$`)
	// The query on the command line, when the command was saved with its
	// output.
	csvQueryRE = regexp.MustCompile(`--query-(gpu|remapped-rows)=([A-Za-z0-9_.,]+)`)
)

// canonical returns the first name of a property.
func canonical(name string) string {
	n := csvName(name)
	if a, ok := csvAlias[n]; ok {
		return a
	}
	return n
}

// splitCSV cuts a line at its commas. nvidia-smi quotes nothing.
func splitCSV(line string) []string {
	parts := strings.Split(line, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// headerOf reports whether a line names columns: every part is a property
// name, and one of them is a property this reader knows. A line of values
// has a number, a PCI address or a bracket in it somewhere.
func headerOf(parts []string) bool {
	known := false
	for _, p := range parts {
		if !csvNameRE.MatchString(p) {
			return false
		}
		c := canonical(p)
		if csvRead[c] || csvOther[c] != "" {
			known = true
		}
	}
	return known
}

// csvDoc is a query output taken apart.
type csvDoc struct {
	// columns are the first names of the columns, in order.
	columns []string
	// fromHeader says that the columns are named by a header line of the
	// output; otherwise they come from a command line or from the caller.
	fromHeader bool
	rows       []csvRow
	// tally holds the lines that are not a row of the width of the columns.
	tally adapter.Tally
}

type csvRow struct {
	values []string
	line   int
	text   string
}

// isQueryLine reports whether a line is the command that was saved with
// the output, or a comment.
func isQueryLine(line string) bool {
	return strings.HasPrefix(line, "#") || strings.HasPrefix(line, "$") || strings.Contains(line, "nvidia-smi ")
}

// parseCSV takes a query output apart. query is the field list of the
// caller, for output that was saved without its header.
func parseCSV(data []byte, origin, query string) csvDoc {
	var doc csvDoc
	var asked []string
	if query != "" {
		asked = splitCSV(query)
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(strings.TrimRight(sc.Text(), "\r"))
		if line == "" {
			continue
		}
		if isQueryLine(line) {
			if m := csvQueryRE.FindStringSubmatch(line); m != nil && doc.columns == nil && asked == nil {
				asked = splitCSV(m[2])
			}
			continue
		}
		parts := splitCSV(line)
		if doc.columns == nil && headerOf(parts) {
			doc.fromHeader = true
			for _, p := range parts {
				doc.columns = append(doc.columns, canonical(p))
			}
			continue
		}
		if doc.columns == nil && asked != nil {
			for _, p := range asked {
				doc.columns = append(doc.columns, canonical(p))
			}
		}
		ref := fmt.Sprintf("%s#L%d", origin, n)
		switch {
		case doc.columns == nil:
			doc.rows = append(doc.rows, csvRow{values: parts, line: n, text: line})
		case headerOf(parts) && len(parts) == len(doc.columns):
			// The header again: a query that was run in a loop, or two
			// outputs in one file.
		case len(parts) != len(doc.columns):
			word := "values"
			if len(parts) == 1 {
				word = "value"
			}
			doc.tally.Skip(ref, fmt.Sprintf("a line with %d %s where the output has %d columns", len(parts), word, len(doc.columns)))
		default:
			doc.rows = append(doc.rows, csvRow{values: parts, line: n, text: line})
		}
	}
	if err := sc.Err(); err != nil {
		doc.tally.Skip(fmt.Sprintf("%s#L%d", origin, n+1), "a line longer than 4 MiB: the rest was not read")
	}
	// Lines that stood before the header were kept without a width to
	// hold them against.
	if doc.columns != nil {
		kept := doc.rows[:0]
		for _, r := range doc.rows {
			if len(r.values) == len(doc.columns) {
				kept = append(kept, r)
				continue
			}
			word := "values"
			if len(r.values) == 1 {
				word = "value"
			}
			doc.tally.Skip(fmt.Sprintf("%s#L%d", origin, r.line), fmt.Sprintf("a line with %d %s where the output has %d columns", len(r.values), word, len(doc.columns)))
		}
		doc.rows = kept
	}
	return doc
}

var (
	csvUUIDRE = regexp.MustCompile(`\bGPU-[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`)
	csvBusRE  = regexp.MustCompile(`(^|, ?)[0-9A-Fa-f]{8}:[0-9A-Fa-f]{2}:[0-9A-Fa-f]{2}\.[0-7](,|$)`)
)

// distinct reports whether a header names a property that only a query of
// nvidia-smi has: one of the gpu_ names, or a known property with a dot in
// its name. Words such as "name", "index", "uuid" and "serial" head the
// columns of many other files.
func distinct(parts []string) bool {
	for _, p := range parts {
		raw := csvName(p)
		c := canonical(p)
		switch {
		case strings.HasPrefix(raw, "gpu_") && raw != c:
			return true
		case strings.Contains(c, ".") && (csvRead[c] || csvOther[c] != ""):
			return true
		case c == "used_gpu_memory":
			return true
		}
	}
	return false
}

// SniffCSV reports whether data looks like the output of a selective query
// of nvidia-smi. It is recognised by the command that was saved with it,
// by a header line with a property only such a query has, or by a GPU UUID
// or a PCI address in the form nvidia-smi prints among its values. Output
// without a header is recognised by its values alone; its columns are then
// still unknown, which the reader reports.
func SniffCSV(data []byte) bool {
	head := data
	if len(head) > 256<<10 {
		head = head[:256<<10]
	}
	lines, header := 0, false
	for _, raw := range bytes.Split(head, []byte("\n")) {
		line := strings.TrimSpace(string(raw))
		if line == "" {
			continue
		}
		if isQueryLine(line) {
			if csvQueryRE.MatchString(line) {
				return true
			}
			continue
		}
		if lines++; lines > 3 {
			break
		}
		parts := splitCSV(line)
		if headerOf(parts) {
			if distinct(parts) {
				return true
			}
			header = true
			continue
		}
		if (len(parts) > 1 || header) && (csvUUIDRE.MatchString(line) || csvBusRE.MatchString(line)) {
			return true
		}
	}
	return false
}

// absent reports whether a value says that the GPU does not have it.
func absent(v string) bool {
	return v == "" || v == "N/A" || (strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]"))
}

// collectCSV reads a query output into sink.
func (a *Adapter) collectCSV(data []byte, sink adapter.Sink, rep *adapter.Report) {
	doc := parseCSV(data, a.origin, a.opts.Query)
	rep.Records = len(doc.rows) + doc.tally.Total()
	rep.Skipped = doc.tally.Total()
	finish := func() {
		rep.Notes = append(rep.Notes, doc.tally.Notes()...)
		switch {
		case rep.Status != "":
		case rep.Skipped > 0:
			rep.Status = model.SourcePartial
		default:
			rep.Status = model.SourceOK
		}
	}
	if doc.columns == nil {
		rep.Skipped = rep.Records
		rep.Status = model.SourceError
		rep.Detail = "the output has no header line and no command line: its columns are not known. Run the query without \"noheader\", or name its fields with --nvidia-query"
		return
	}
	col := map[string]int{}
	var other, unread []string
	for i, c := range doc.columns {
		if _, dup := col[c]; dup {
			continue // a property asked for twice: the first column is read
		}
		col[c] = i
		switch {
		case csvOther[c] != "":
			other = append(other, c)
		case !csvRead[c] && (strings.HasPrefix(c, "ecc.") || strings.HasPrefix(c, "retired_pages.") || strings.HasPrefix(c, "remapped_rows.")):
			unread = append(unread, c)
		}
	}
	if len(other) > 0 {
		rep.Skipped = rep.Records
		rep.Status = model.SourceEmpty
		rep.Detail = "the output of " + csvOther[other[0]] + ": it is not read. The counts of GPU memory errors are properties of --query-gpu"
		return
	}
	if len(doc.rows) == 0 {
		rep.Status, rep.Detail = model.SourceEmpty, "the output lists no GPU"
		finish()
		return
	}
	_, hasUUID := col["uuid"]
	_, hasSerial := col["serial"]
	if !hasUUID && !hasSerial {
		// Nothing in the output names a GPU for good: its index changes
		// with a boot and its PCI address belongs to the slot.
		rep.Skipped = rep.Records
		rep.Status = model.SourceError
		rep.Detail = fmt.Sprintf("the output has no uuid and no serial column: its %d GPUs cannot be told from the GPUs of another reading. Add uuid or serial to the query", len(doc.rows))
		rep.Notes = append(rep.Notes, doc.tally.Notes()...)
		return
	}

	loc := a.opts.Location
	if loc == nil {
		loc = time.UTC
	}
	now := a.opts.Now.UTC()
	if a.opts.Now.IsZero() {
		now = time.Now().UTC()
	}
	_, hasTime := col["timestamp"]
	drivers := map[string]bool{}
	seen := map[string]int{}
	noData, unkeyed, stamped := 0, []string{}, 0
	for i := range doc.rows {
		row := &doc.rows[i]
		ref := fmt.Sprintf("%s#L%d", a.origin, row.line)
		get := func(name string) string {
			j, ok := col[name]
			if !ok || absent(row.values[j]) {
				return ""
			}
			return row.values[j]
		}
		g := &model.GPUMem{Kind: model.GPUSnapshot, Source: Kind, RawRef: ref, TimeQuality: model.TimeExact, ObservedAt: now}
		g.Model = get("name")
		if s := get("serial"); model.UsableSerial(s) {
			g.GPUSerial = s
		}
		if u := get("uuid"); strings.HasPrefix(u, "GPU-") {
			g.GPUUUID = u
		}
		g.PCIBus = busAddress(get("pci.bus_id"))
		if v := get("index"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 && n < 1<<16 {
				g.GPUIndex = model.Some(n)
			}
		}
		for _, name := range []string{"kmd_version", "driver_version"} {
			if v := get(name); v != "" {
				drivers[v] = true
				break
			}
		}
		if v := get("timestamp"); v != "" {
			for _, layout := range []string{"2006/01/02 15:04:05.000", "2006/01/02 15:04:05"} {
				if t, err := time.ParseInLocation(layout, v, loc); err == nil && t.Year() >= 1970 {
					g.ObservedAt = t.UTC()
					stamped++
					break
				}
			}
		}
		key := g.GPUUUID
		if key == "" {
			key = g.GPUSerial
		}
		if key == "" {
			rep.Skipped++
			name := g.PCIBus
			if name == "" && g.GPUIndex.Valid {
				name = "index " + strconv.Itoa(g.GPUIndex.V)
			}
			if name == "" {
				name = "line " + strconv.Itoa(row.line)
			}
			unkeyed = append(unkeyed, name)
			continue
		}
		fillCSV(g, get)
		g.Raw = rawCSV(doc.columns, row.values)
		g.Fingerprint = model.Fingerprint(Kind, key, strconv.FormatInt(g.ObservedAt.Unix(), 10))
		if first, dup := seen[g.Fingerprint]; dup {
			rep.Skipped++
			reason := fmt.Sprintf("a second row for the GPU of line %d at the same time", first)
			if !hasTime {
				reason = fmt.Sprintf("a second row for the GPU of line %d: without a timestamp column the rows of a repeated query cannot be told apart", first)
			}
			doc.tally.Skip(ref, reason)
			continue
		}
		seen[g.Fingerprint] = row.line
		if !hasMemoryData(g) {
			noData++
		}
		if err := sink.GPU(g); err != nil {
			rep.Status, rep.Detail, rep.SinkErr = model.SourceError, "ledger rejected a record: "+err.Error(), err
			return
		}
	}

	switch {
	case !hasTime || stamped == 0:
		rep.Notes = append(rep.Notes, "the output carries no readable timestamp column; the snapshots have the time of the import")
	case a.opts.Location == nil:
		rep.Notes = append(rep.Notes, "the timestamp column is the local time of the host and was read as UTC")
	}
	if len(drivers) > 0 {
		list := make([]string, 0, len(drivers))
		for d := range drivers {
			list = append(list, d)
		}
		sort.Strings(list)
		rep.Notes = append(rep.Notes, "driver version "+strings.Join(list, ", "))
	}
	if !doc.fromHeader {
		rep.Notes = append(rep.Notes, "the output has no header line; its columns were taken from the query: "+strings.Join(doc.columns, ","))
	}
	rep.Notes = append(rep.Notes, fmt.Sprintf("%d GPUs", len(seen)))
	if len(unkeyed) > 0 {
		list := unkeyed
		if len(list) > 8 {
			list = append(append([]string{}, list[:8]...), "...")
		}
		rep.Notes = append(rep.Notes, fmt.Sprintf("skipped %d GPUs that report neither a UUID nor a serial number (%s): such a GPU cannot be told from another GPU in the same place later", len(unkeyed), strings.Join(list, ", ")))
	}
	if noData > 0 {
		rep.Notes = append(rep.Notes, fmt.Sprintf("%d GPUs report no ECC counts, retired pages or remapped rows: the GPU or its driver does not keep them, or the query did not ask for them", noData))
	}
	if len(unread) > 0 {
		sort.Strings(unread)
		if len(unread) > 12 {
			unread = append(unread[:12], "...")
		}
		rep.Notes = append(rep.Notes, "columns about memory errors that were not read: "+strings.Join(unread, ", "))
	}
	if len(seen) == 0 {
		rep.Status = model.SourceError
		rep.Detail = "no GPU of the output reports a UUID or a serial number"
	}
	finish()
}

// fillCSV sets the memory state of a snapshot from the values of a row.
func fillCSV(g *model.GPUMem, get func(string) string) {
	num := func(name string) model.Opt[int] {
		v := get(name)
		if v == "" {
			return model.Opt[int]{}
		}
		return number(v)
	}
	g.ECCEnabled = flag(get("ecc.mode.current"))

	// ECC counters. "dram" is the name GPUs with row remapping use for the
	// frame buffer memory and "device_memory" the one older GPUs use; a
	// driver that knows both prints the same count under both. The SRAM
	// counts are one column on newer drivers, with the uncorrectable ones
	// also split by the kind of the check; older GPUs have a total, of
	// which the device memory is the DRAM and the rest is in the GPU.
	counts := func(kind, period string) (dram, sram model.Opt[int]) {
		p := "ecc.errors." + kind + "." + period + "."
		dram = num(p + "dram")
		if !dram.Valid {
			dram = num(p + "device_memory")
		}
		sram = num(p + "sram")
		if !sram.Valid && kind == "uncorrected" {
			sram = sum(num(p+"sram.parity"), num(p+"sram.secded"))
		}
		if total := num(p + "total"); !sram.Valid && total.Valid && dram.Valid && total.V >= dram.V {
			sram = model.Some(total.V - dram.V)
		}
		return dram, sram
	}
	g.DRAMCorrectableVolatile, g.SRAMCorrectableVolatile = counts("corrected", "volatile")
	g.DRAMCorrectable, g.SRAMCorrectable = counts("corrected", "aggregate")
	g.DRAMUncorrectableVolatile, g.SRAMUncorrectableVolatile = counts("uncorrected", "volatile")
	g.DRAMUncorrectable, g.SRAMUncorrectable = counts("uncorrected", "aggregate")
	g.SRAMThresholdExceeded = flag(get("ecc.errors.uncorrected.aggregate.sram.thresholdexceeded"))

	g.RetiredSBE, g.RetiredDBE = num("retired_pages.single_bit_ecc.count"), num("retired_pages.double_bit.count")
	g.RetiredPending = flag(get("retired_pages.pending"))

	g.RemapCorrectable, g.RemapUncorrectable = num("remapped_rows.correctable"), num("remapped_rows.uncorrectable")
	g.RemapPending, g.RemapFailure = flag(get("remapped_rows.pending")), flag(get("remapped_rows.failure"))
	g.BankMax, g.BankHigh, g.BankPartial = num("remapped_rows.histogram.max"), num("remapped_rows.histogram.high"), num("remapped_rows.histogram.partial")
	g.BankLow, g.BankNone = num("remapped_rows.histogram.low"), num("remapped_rows.histogram.none")
}

// rawCSV keeps the columns of a row a snapshot is made of, as "name:
// value" lines. A row of a full query has more than two hundred columns.
func rawCSV(columns, values []string) string {
	var b strings.Builder
	for i, c := range columns {
		if !csvRead[c] || c == "timestamp" || absent(values[i]) {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(c + ": " + values[i])
	}
	return b.String()
}
