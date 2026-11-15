// Package nvidia reads the output of "nvidia-smi -q", as text and as XML
// (-x), and of its selective queries (--query-gpu, --query-remapped-rows
// with --format=csv): for each GPU its identity, the ECC error counts, the
// retired pages of older GPUs and the row remapping state of newer ones.
//
// The driver does not say where in the memory of a GPU an error was. What
// it reports is a budget: how many rows were remapped, whether a remap is
// pending or failed, and how many banks still have spare rows. One reading
// of that state is one snapshot record per GPU, keyed by the GPU serial
// number. The Xid lines of the kernel log, which the kernel log reader
// takes, are the events between two snapshots.
package nvidia

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
)

// Kind is the adapter name in the source inventory.
const Kind = "nvidia-smi"

// Options tell the adapter what the input itself cannot say.
type Options struct {
	// Now is when the output was captured. It is used when the output
	// carries no timestamp. Zero means the current time.
	Now time.Time
	// Location is the time zone of the timestamp in the output, which is
	// the local time of the host without a zone. Nil means UTC.
	Location *time.Location
	// Query is the field list of a selective query whose output was saved
	// without its header line ("index,uuid,remapped_rows.failure"). Output
	// with a header, or with its command line, names its columns itself.
	Query string
}

// DefaultTimeout bounds a run of nvidia-smi.
const DefaultTimeout = 30 * time.Second

// maxInput bounds the size of the output. A host with sixteen GPUs prints
// a few hundred kilobytes.
const maxInput = 64 << 20

// Adapter reads one output of nvidia-smi.
type Adapter struct {
	origin string
	opts   Options
	read   func(ctx context.Context) (data []byte, status model.SourceStatus, detail string)
}

func (a *Adapter) Kind() string   { return Kind }
func (a *Adapter) Origin() string { return a.origin }

func readAll(r io.Reader) ([]byte, model.SourceStatus, string) {
	data, err := io.ReadAll(io.LimitReader(r, maxInput+1))
	if err != nil {
		st, detail := adapter.StatusForError(err)
		return nil, st, detail
	}
	if len(data) > maxInput {
		return nil, model.SourceError, "larger than 64 MiB: not the output of nvidia-smi"
	}
	return data, "", ""
}

// NewFile returns an adapter for a file that holds the output.
func NewFile(path string, opts Options) *Adapter {
	return &Adapter{origin: path, opts: opts, read: func(context.Context) ([]byte, model.SourceStatus, string) {
		f, err := os.Open(path)
		if err != nil {
			st, detail := adapter.StatusForError(err)
			return nil, st, detail
		}
		defer f.Close()
		if fi, err := f.Stat(); err == nil && fi.IsDir() {
			return nil, model.SourceError, "is a directory"
		}
		return readAll(f)
	}}
}

// NewReader returns an adapter for output read from r.
func NewReader(r io.Reader, origin string, opts Options) *Adapter {
	return &Adapter{origin: origin, opts: opts, read: func(context.Context) ([]byte, model.SourceStatus, string) { return readAll(r) }}
}

// NewCommand returns an adapter that runs "prog -q -x" and reads what it
// prints. An empty prog looks nvidia-smi up in PATH; a zero timeout means
// DefaultTimeout. The query only reads the state of the driver. A host
// without the program gets the status absent.
func NewCommand(prog string, timeout time.Duration, opts Options) *Adapter {
	name := prog
	if name == "" {
		name = "nvidia-smi"
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Adapter{origin: name + " -q -x", opts: opts, read: func(ctx context.Context) ([]byte, model.SourceStatus, string) {
		path := prog
		if path == "" {
			p, err := exec.LookPath("nvidia-smi")
			if err != nil {
				return nil, model.SourceAbsent, "nvidia-smi is not installed: no NVIDIA GPU driver on this host"
			}
			path = p
		}
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, path, "-q", "-x")
		var out, errOut limited
		out.max, errOut.max = maxInput, 4<<10
		cmd.Stdout, cmd.Stderr = &out, &errOut
		// A child of the program that keeps its output open must not hold
		// up the scan once the program was stopped.
		cmd.WaitDelay = time.Second
		err := cmd.Run()
		switch {
		case ctx.Err() != nil:
			return nil, model.SourceError, fmt.Sprintf("nvidia-smi did not answer within %s", timeout)
		case err != nil && out.buf.Len() == 0:
			var notFound *exec.Error
			if errors.As(err, &notFound) || errors.Is(err, os.ErrNotExist) {
				return nil, model.SourceAbsent, "nvidia-smi is not installed: no NVIDIA GPU driver on this host"
			}
			if st, detail := adapter.StatusForError(err); st == model.SourceUnreadable {
				return nil, st, detail
			}
			return nil, model.SourceError, "nvidia-smi failed: " + firstLine(errOut.buf.String(), err.Error())
		}
		// With output, the output says what went wrong.
		return out.buf.Bytes(), "", ""
	}}
}

// limited is a writer that keeps the first max bytes.
type limited struct {
	buf bytes.Buffer
	max int
}

func (l *limited) Write(p []byte) (int, error) {
	if room := l.max - l.buf.Len(); room > 0 {
		l.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

func firstLine(s, fallback string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			if len(l) > 200 {
				l = l[:200] + "..."
			}
			return l
		}
	}
	return fallback
}

// Sniff reports whether data looks like the output of nvidia-smi -q in
// either form. It is used to pick the adapter for a file by its content;
// SniffCSV does the same for the output of a selective query.
func Sniff(data []byte) bool {
	head := data
	if len(head) > 64<<10 {
		head = head[:64<<10]
	}
	return bytes.Contains(head, []byte("NVSMI LOG")) || bytes.Contains(head, []byte("<nvidia_smi_log"))
}

// gpu is one GPU of the output: its PCI address and the values of the
// output by their place in it, such as "ecc errors/volatile/dram correctable".
type gpu struct {
	bus    string
	values map[string]string
	raw    []string
	line   int
}

// document is the output taken apart.
type document struct {
	timestamp string
	driver    string
	gpus      []gpu
	xml       bool
}

// Collect reads the output into sink.
func (a *Adapter) Collect(ctx context.Context, sink adapter.Sink) adapter.Report {
	rep := adapter.Report{Kind: Kind, Origin: a.origin}
	if err := ctx.Err(); err != nil {
		rep.Status, rep.Detail = model.SourceError, err.Error()
		return rep
	}
	data, status, detail := a.read(ctx)
	if status != "" {
		rep.Status, rep.Detail = status, detail
		return rep
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var doc document
	if !Sniff(data) && (SniffCSV(data) || a.opts.Query != "") {
		a.collectCSV(data, sink, &rep)
		return rep
	}
	if t := bytes.TrimLeft(data, " \t\r\n"); bytes.HasPrefix(t, []byte("<")) {
		var err error
		if doc, err = parseXML(data); err != nil {
			rep.Status, rep.Detail = model.SourceError, "not readable as the XML of nvidia-smi -q -x: "+err.Error()
			return rep
		}
	} else {
		doc = parseText(string(data))
	}
	rep.Records = len(doc.gpus)
	if len(doc.gpus) == 0 {
		text := string(data)
		switch {
		case strings.Contains(text, "No devices were found"):
			rep.Status, rep.Detail = model.SourceEmpty, "nvidia-smi found no GPU"
		case strings.Contains(text, "NVIDIA-SMI has failed"), strings.Contains(text, "Failed to initialize NVML"), strings.Contains(text, "Unable to determine the device handle"):
			rep.Status, rep.Detail = model.SourceError, firstLine(text, "nvidia-smi failed")
		case Sniff(data):
			rep.Status, rep.Detail = model.SourceEmpty, "the output lists no GPU"
		case len(bytes.TrimSpace(data)) == 0:
			rep.Status, rep.Detail = model.SourceEmpty, "no output"
		default:
			rep.Status, rep.Detail = model.SourceError, "not the output of nvidia-smi -q: "+firstLine(text, "no text")
		}
		return rep
	}

	// When the state was read.
	at, haveTime := time.Time{}, false
	if doc.timestamp != "" {
		loc := a.opts.Location
		if loc == nil {
			loc = time.UTC
		}
		if t, err := time.ParseInLocation("Mon Jan _2 15:04:05 2006", strings.Join(strings.Fields(doc.timestamp), " "), loc); err == nil && t.Year() >= 1970 {
			at, haveTime = t.UTC(), true
		}
	}
	if !haveTime {
		at = a.opts.Now.UTC()
		if a.opts.Now.IsZero() {
			at = time.Now().UTC()
		}
		rep.Notes = append(rep.Notes, "the output carries no readable timestamp; the snapshots have the time of the import")
	} else if a.opts.Location == nil {
		rep.Notes = append(rep.Notes, "the timestamp of the output is the local time of the host and was read as UTC")
	}
	if doc.driver != "" {
		rep.Notes = append(rep.Notes, "driver version "+doc.driver)
	}

	noData := 0
	for i := range doc.gpus {
		g := snapshot(&doc.gpus[i], doc.xml)
		g.ObservedAt, g.TimeQuality, g.GPUIndex = at, model.TimeExact, model.Some(i)
		g.Source = Kind
		g.RawRef = fmt.Sprintf("%s#gpu %d", a.origin, i)
		if !doc.xml && doc.gpus[i].line > 0 {
			g.RawRef = fmt.Sprintf("%s#L%d", a.origin, doc.gpus[i].line)
		}
		key := g.GPUUUID
		if key == "" {
			key = g.GPUSerial
		}
		if key == "" {
			key = g.PCIBus
		}
		if key == "" {
			rep.Skipped++
			rep.Notes = append(rep.Notes, fmt.Sprintf("skipped %s: the GPU has no UUID, serial number or PCI address", g.RawRef))
			continue
		}
		g.Fingerprint = model.Fingerprint(Kind, key, strconv.FormatInt(at.Unix(), 10))
		if !hasMemoryData(g) {
			noData++
		}
		if err := sink.GPU(g); err != nil {
			rep.Status, rep.Detail, rep.SinkErr = model.SourceError, "ledger rejected a record: "+err.Error(), err
			return rep
		}
	}
	rep.Notes = append(rep.Notes, fmt.Sprintf("%d GPUs", len(doc.gpus)))
	if noData > 0 {
		rep.Notes = append(rep.Notes, fmt.Sprintf("%d GPUs report no ECC counts, retired pages or remapped rows: the GPU or its driver does not keep them", noData))
	}
	if rep.Skipped > 0 {
		rep.Status = model.SourcePartial
	} else {
		rep.Status = model.SourceOK
	}
	return rep
}

func hasMemoryData(g *model.GPUMem) bool {
	return g.DRAMCorrectable.Valid || g.DRAMUncorrectable.Valid || g.DRAMCorrectableVolatile.Valid || g.DRAMUncorrectableVolatile.Valid ||
		g.RetiredSBE.Valid || g.RetiredDBE.Valid || g.RemapCorrectable.Valid || g.RemapUncorrectable.Valid || g.RemapPending.Valid || g.RemapFailure.Valid
}

var (
	gpuHeadRE = regexp.MustCompile(`^GPU ([0-9A-Fa-f]{4,8}:[0-9A-Fa-f]{2}:[0-9A-Fa-f]{2}\.[0-7])\s*$`)
	busRE     = regexp.MustCompile(`^([0-9A-Fa-f]{4,8}):([0-9A-Fa-f]{2}:[0-9A-Fa-f]{2}\.[0-7])$`)
	leadIntRE = regexp.MustCompile(`^-?\d+`)
)

// busAddress writes a PCI address the way the ledger keeps it: a four
// digit domain, in lower case. nvidia-smi prints eight digits.
func busAddress(s string) string {
	m := busRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return ""
	}
	domain := m[1]
	if len(domain) > 4 {
		if strings.Trim(domain[:len(domain)-4], "0") != "" {
			return strings.ToLower(domain + ":" + m[2]) // a domain above FFFF is kept whole
		}
		domain = domain[len(domain)-4:]
	}
	return strings.ToLower(domain + ":" + m[2])
}

// maxDepth bounds the nesting of sections that is followed. The output
// nests five levels deep.
const maxDepth = 12

// memorySections are the parts of the text output a snapshot is made of.
// Their lines are the raw record.
var memorySections = map[string]bool{
	"product name": true, "serial number": true, "gpu uuid": true,
	"ecc mode": true, "ecc errors": true, "retired pages": true, "remapped rows": true,
}

// parseText takes the text output apart. Lines are "Key : Value" or the
// name of a section; the indentation, four spaces a level, says which
// section a line belongs to.
func parseText(text string) document {
	var doc document
	var cur *gpu
	var stack []string
	for n, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, " \t\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		body := strings.TrimSpace(line)
		if indent == 0 {
			if m := gpuHeadRE.FindStringSubmatch(body); m != nil {
				doc.gpus = append(doc.gpus, gpu{bus: busAddress(m[1]), values: map[string]string{}, line: n + 1})
				cur = &doc.gpus[len(doc.gpus)-1]
				stack = nil
				continue
			}
			k, v, ok := cutKey(body)
			if ok && cur == nil {
				switch strings.ToLower(k) {
				case "timestamp":
					doc.timestamp = v
				case "driver version":
					doc.driver = v
				}
			}
			if ok || strings.HasPrefix(body, "=") {
				// A line at the left margin that is no GPU ends the last one.
				cur = nil
			}
			continue
		}
		if cur == nil {
			continue
		}
		level := indent/4 - 1
		if level < 0 || level > maxDepth {
			continue // deeper than any section of the output
		}
		if level < len(stack) {
			stack = stack[:level]
		}
		k, v, ok := cutKey(body)
		key := strings.ToLower(k)
		if !ok {
			// The name of a section.
			for len(stack) < level {
				stack = append(stack, "")
			}
			stack = append(stack, strings.ToLower(body))
		} else {
			path := strings.Join(append(append([]string{}, stack...), key), "/")
			if _, dup := cur.values[path]; !dup {
				cur.values[path] = v
			}
		}
		top := key
		if len(stack) > 0 {
			top = stack[0]
		}
		if !ok && level == 0 {
			top = strings.ToLower(body)
		}
		if memorySections[top] {
			cur.raw = append(cur.raw, body)
		}
	}
	return doc
}

// cutKey splits "Key   : Value". ok is false for a line without a value,
// which is the name of a section.
func cutKey(body string) (key, value string, ok bool) {
	i := strings.Index(body, " : ")
	if i < 0 {
		if k, found := strings.CutSuffix(body, " :"); found {
			return strings.TrimSpace(k), "", true
		}
		return body, "", false
	}
	return strings.TrimSpace(body[:i]), strings.TrimSpace(body[i+3:]), true
}

// parseXML takes the XML output apart: the values of each gpu element by
// the path of element names below it.
func parseXML(data []byte) (document, error) {
	doc := document{xml: true}
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	var path []string
	var cur *gpu
	var text strings.Builder
	sawRoot := false
	// deeper counts the open elements below a gpu that are nested deeper
	// than maxDepth; they are passed over.
	deeper := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			if len(doc.gpus) > 0 {
				// What was read before the damage is kept.
				return doc, nil
			}
			return doc, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			text.Reset()
			switch {
			case name == "nvidia_smi_log":
				sawRoot = true
			case name == "gpu" && cur == nil:
				doc.gpus = append(doc.gpus, gpu{values: map[string]string{}})
				cur = &doc.gpus[len(doc.gpus)-1]
				for _, a := range t.Attr {
					if strings.EqualFold(a.Name.Local, "id") {
						cur.bus = busAddress(a.Value)
					}
				}
				path = path[:0]
			case cur != nil:
				if len(path) < maxDepth && deeper == 0 {
					path = append(path, name)
				} else {
					deeper++
				}
			}
		case xml.CharData:
			if text.Len() < 4096 {
				text.Write(t)
			}
		case xml.EndElement:
			name := strings.ToLower(t.Name.Local)
			value := strings.TrimSpace(text.String())
			text.Reset()
			switch {
			case cur == nil:
				switch name {
				case "timestamp":
					doc.timestamp = value
				case "driver_version":
					doc.driver = value
				}
			case deeper > 0:
				deeper--
			case name == "gpu" && len(path) == 0:
				cur = nil
			case len(path) > 0:
				if value != "" {
					p := strings.Join(path, "/")
					if _, dup := cur.values[p]; !dup {
						cur.values[p] = value
						if xmlMemory[path[0]] {
							cur.raw = append(cur.raw, p+": "+value)
						}
					}
				}
				path = path[:len(path)-1]
			}
		}
	}
	if !sawRoot {
		return doc, errors.New("no nvidia_smi_log element")
	}
	return doc, nil
}

// xmlMemory are the elements of a gpu a snapshot is made of.
var xmlMemory = map[string]bool{
	"product_name": true, "serial": true, "uuid": true,
	"ecc_mode": true, "ecc_errors": true, "retired_pages": true, "remapped_rows": true,
}

// number reads a count. "N/A" and anything else that is not a number mean
// that the GPU does not report the value.
func number(s string) model.Opt[int] {
	m := leadIntRE.FindString(strings.TrimSpace(s))
	if m == "" {
		return model.Opt[int]{}
	}
	n, err := strconv.Atoi(m)
	if err != nil || n < 0 {
		return model.Opt[int]{}
	}
	return model.Some(n)
}

// flag reads "Yes", "No", "Enabled" and "Disabled".
func flag(s string) model.Opt[bool] {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "yes", "enabled", "true":
		return model.Some(true)
	case "no", "disabled", "false":
		return model.Some(false)
	}
	return model.Opt[bool]{}
}

// sum adds counts that are reported; it is unset when none is.
func sum(parts ...model.Opt[int]) model.Opt[int] {
	total, any := 0, false
	for _, p := range parts {
		if p.Valid {
			total, any = total+p.V, true
		}
	}
	if !any {
		return model.Opt[int]{}
	}
	return model.Some(total)
}

// snapshot builds the record of one GPU. The text and the XML output name
// the same values differently; names lists both, text first.
func snapshot(g *gpu, isXML bool) *model.GPUMem {
	get := func(text, xmlPath string) string {
		if isXML {
			return g.values[xmlPath]
		}
		return g.values[text]
	}
	rec := &model.GPUMem{Kind: model.GPUSnapshot, PCIBus: g.bus, Raw: strings.Join(g.raw, "\n")}
	rec.Model = get("product name", "product_name")
	if s := get("serial number", "serial"); model.UsableSerial(s) {
		rec.GPUSerial = s
	}
	if u := get("gpu uuid", "uuid"); strings.HasPrefix(u, "GPU-") {
		rec.GPUUUID = u
	}
	if rec.PCIBus == "" {
		rec.PCIBus = busAddress(get("pci/bus id", "pci/pci_bus_id"))
	}
	rec.ECCEnabled = flag(get("ecc mode/current", "ecc_mode/current_ecc"))

	// ECC counters. GPUs with row remapping count DRAM and SRAM errors;
	// newer drivers split the uncorrectable SRAM errors in two. Older GPUs
	// count single and double bit errors per kind of memory, where the
	// device memory is the DRAM and the rest is in the GPU itself.
	counts := func(period string) (dramCE, dramUE, sramCE, sramUE model.Opt[int]) {
		t, x := "ecc errors/"+period+"/", "ecc_errors/"+period+"/"
		n := func(text, xmlName string) model.Opt[int] { return number(get(t+text, x+xmlName)) }
		dramCE, dramUE = n("dram correctable", "dram_correctable"), n("dram uncorrectable", "dram_uncorrectable")
		sramCE = n("sram correctable", "sram_correctable")
		sramUE = sum(n("sram uncorrectable", "sram_uncorrectable"), n("sram uncorrectable parity", "sram_uncorrectable_parity"),
			n("sram uncorrectable sec-ded", "sram_uncorrectable_secded"))
		if !dramCE.Valid && !dramUE.Valid && !sramCE.Valid && !sramUE.Valid {
			sbeDev, dbeDev := n("single bit/device memory", "single_bit/device_memory"), n("double bit/device memory", "double_bit/device_memory")
			sbeAll, dbeAll := n("single bit/total", "single_bit/total"), n("double bit/total", "double_bit/total")
			dramCE, dramUE = sbeDev, dbeDev
			if sbeAll.Valid && sbeDev.Valid && sbeAll.V >= sbeDev.V {
				sramCE = model.Some(sbeAll.V - sbeDev.V)
			}
			if dbeAll.Valid && dbeDev.Valid && dbeAll.V >= dbeDev.V {
				sramUE = model.Some(dbeAll.V - dbeDev.V)
			}
		}
		return
	}
	rec.DRAMCorrectableVolatile, rec.DRAMUncorrectableVolatile, rec.SRAMCorrectableVolatile, rec.SRAMUncorrectableVolatile = counts("volatile")
	rec.DRAMCorrectable, rec.DRAMUncorrectable, rec.SRAMCorrectable, rec.SRAMUncorrectable = counts("aggregate")
	rec.SRAMThresholdExceeded = flag(get("ecc errors/aggregate/sram threshold exceeded", "ecc_errors/aggregate/sram_threshold_exceeded"))

	rec.RetiredSBE = number(get("retired pages/single bit ecc", "retired_pages/multiple_single_bit_retirement/retired_count"))
	rec.RetiredDBE = number(get("retired pages/double bit ecc", "retired_pages/double_bit_retirement/retired_count"))
	rec.RetiredPending = flag(get("retired pages/pending page blacklist", "retired_pages/pending_blacklist"))
	if !rec.RetiredPending.Valid {
		rec.RetiredPending = flag(get("retired pages/pending", "retired_pages/pending_retirement"))
	}

	rec.RemapCorrectable = number(get("remapped rows/correctable error", "remapped_rows/remapped_row_corr"))
	rec.RemapUncorrectable = number(get("remapped rows/uncorrectable error", "remapped_rows/remapped_row_unc"))
	rec.RemapPending = flag(get("remapped rows/pending", "remapped_rows/remapped_row_pending"))
	rec.RemapFailure = flag(get("remapped rows/remapping failure occurred", "remapped_rows/remapped_row_failure"))
	bank := func(name string) model.Opt[int] {
		return number(get("remapped rows/bank remap availability histogram/"+name, "remapped_rows/row_remapper_histogram/row_remapper_histogram_"+name))
	}
	rec.BankMax, rec.BankHigh, rec.BankPartial, rec.BankLow, rec.BankNone = bank("max"), bank("high"), bank("partial"), bank("low"), bank("none")
	return rec
}
