package nvidia

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/testutil"
)

var ctx = context.Background()

func collect(t *testing.T, name string, opts Options) (*adapter.Collected, adapter.Report) {
	t.Helper()
	var c adapter.Collected
	rep := NewFile(testutil.Testdata(t, "nvidia", name), opts).Collect(ctx, &c)
	return &c, rep
}

func some(n int) model.Opt[int] { return model.Some(n) }

// The two GPUs of the fixtures: one that remapped two rows after
// uncorrected errors, and one whose remap failed.
func wantGPUs(at time.Time) []model.GPUMem {
	yes, no := model.Some(true), model.Some(false)
	return []model.GPUMem{
		{ObservedAt: at, Kind: model.GPUSnapshot, GPUSerial: "1650000000412", GPUUUID: "GPU-b850f46d-d5ea-c752-ddf3-c4453e44d3f7",
			GPUIndex: some(0), PCIBus: "0000:3b:00.0", Model: "Example GPU 80GB",
			RemapCorrectable: some(0), RemapUncorrectable: some(2), RemapPending: no, RemapFailure: no,
			BankMax: some(638), BankHigh: some(2), BankPartial: some(0), BankLow: some(0), BankNone: some(0),
			ECCEnabled: yes, DRAMCorrectable: some(31), DRAMUncorrectable: some(2), DRAMCorrectableVolatile: some(0), DRAMUncorrectableVolatile: some(2),
			SRAMCorrectable: some(0), SRAMUncorrectable: some(0), SRAMCorrectableVolatile: some(0), SRAMUncorrectableVolatile: some(0),
			TimeQuality: model.TimeExact, Source: "nvidia-smi"},
		{ObservedAt: at, Kind: model.GPUSnapshot, GPUSerial: "1650000000977", GPUUUID: "GPU-5a1c2b3d-4e5f-6071-8293-a4b5c6d7e8f9",
			GPUIndex: some(1), PCIBus: "0000:86:00.0", Model: "Example GPU 80GB",
			RemapCorrectable: some(0), RemapUncorrectable: some(1), RemapPending: yes, RemapFailure: yes,
			BankMax: some(640), BankHigh: some(0), BankPartial: some(0), BankLow: some(0), BankNone: some(0),
			// The driver splits the uncorrectable SRAM errors in two; they add up.
			ECCEnabled: yes, DRAMCorrectable: some(0), DRAMUncorrectable: some(1), DRAMCorrectableVolatile: some(0), DRAMUncorrectableVolatile: some(1),
			SRAMCorrectable: some(4), SRAMUncorrectable: some(3), SRAMCorrectableVolatile: some(0), SRAMUncorrectableVolatile: some(3),
			SRAMThresholdExceeded: yes, TimeQuality: model.TimeExact, Source: "nvidia-smi"},
	}
}

// strip removes what differs between the text and the XML form of the
// same reading.
func strip(gs []*model.GPUMem) []model.GPUMem {
	out := make([]model.GPUMem, len(gs))
	for i, g := range gs {
		out[i] = *g
		out[i].RawRef, out[i].Raw, out[i].Fingerprint = "", "", ""
	}
	return out
}

func TestTextAndXMLGiveTheSameSnapshots(t *testing.T) {
	at := time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)
	want := wantGPUs(at)
	var prints [2][]string
	for i, name := range []string{"nvidia-smi-q.txt", "nvidia-smi-q.xml"} {
		c, rep := collect(t, name, Options{})
		if rep.Status != model.SourceOK || rep.Records != 2 || rep.Skipped != 0 {
			t.Errorf("%s: report %+v", name, rep)
		}
		if got := strip(c.GPUs); !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n got %+v\nwant %+v", name, got, want)
		}
		notes := strings.Join(rep.Notes, "\n")
		for _, part := range []string{"driver version 550.54.15", "2 GPUs", "local time of the host and was read as UTC"} {
			if !strings.Contains(notes, part) {
				t.Errorf("%s: notes lack %q:\n%s", name, part, notes)
			}
		}
		for _, g := range c.GPUs {
			if g.Fingerprint == "" || g.Raw == "" || g.RawRef == "" {
				t.Errorf("%s: record without fingerprint, raw text or reference: %+v", name, g)
			}
			prints[i] = append(prints[i], g.Fingerprint)
		}
	}
	// One reading is one record, whichever form it was saved in.
	if !reflect.DeepEqual(prints[0], prints[1]) || prints[0][0] == prints[0][1] {
		t.Errorf("fingerprints: text %v, XML %v", prints[0], prints[1])
	}

	c, _ := collect(t, "nvidia-smi-q.txt", Options{})
	if ref := c.GPUs[1].RawRef; !strings.HasSuffix(ref, "nvidia-smi-q.txt#L94") {
		t.Errorf("raw reference of the second GPU = %q", ref)
	}
	raw := c.GPUs[1].Raw
	for _, part := range []string{"Serial Number                         : 1650000000977", "Remapping Failure Occurred        : Yes", "SRAM Threshold Exceeded       : Yes", "None                          : 0 bank(s)"} {
		if !strings.Contains(raw, part) {
			t.Errorf("raw record lacks %q:\n%s", part, raw)
		}
	}
	// Parts of the output that are not about memory stay out of it.
	if strings.Contains(raw, "Minor Number") || strings.Contains(raw, "Bus Id") {
		t.Errorf("raw record holds more than the memory sections:\n%s", raw)
	}
	x, _ := collect(t, "nvidia-smi-q.xml", Options{})
	if raw := x.GPUs[0].Raw; !strings.Contains(raw, "remapped_rows/row_remapper_histogram/row_remapper_histogram_high: 2 bank(s)") || strings.Contains(raw, "fb_memory_usage") {
		t.Errorf("raw record of the XML form:\n%s", raw)
	}
}

func TestTimestampZone(t *testing.T) {
	kst := time.FixedZone("KST", 9*3600)
	c, rep := collect(t, "nvidia-smi-q.txt", Options{Location: kst})
	if !c.GPUs[0].ObservedAt.Equal(time.Date(2026, 11, 13, 0, 30, 0, 0, time.UTC)) {
		t.Errorf("observed at %v", c.GPUs[0].ObservedAt)
	}
	if strings.Contains(strings.Join(rep.Notes, "\n"), "read as UTC") {
		t.Errorf("notes say UTC although a zone was given: %v", rep.Notes)
	}
	// Output without a timestamp takes the time the caller gives.
	now := time.Date(2026, 11, 14, 1, 2, 3, 0, time.UTC)
	text := string(testutil.ReadTestdata(t, "nvidia", "nvidia-smi-q.txt"))
	cut := strings.Replace(text, "Timestamp                                 : Fri Nov 13 09:30:00 2026\n", "", 1)
	var d adapter.Collected
	rep = NewReader(strings.NewReader(cut), "saved output", Options{Now: now}).Collect(ctx, &d)
	if len(d.GPUs) != 2 || !d.GPUs[0].ObservedAt.Equal(now) || !strings.Contains(strings.Join(rep.Notes, "\n"), "no readable timestamp") || rep.Origin != "saved output" {
		t.Errorf("without timestamp: %d GPUs, report %+v", len(d.GPUs), rep)
	}
}

func TestOlderGPUs(t *testing.T) {
	c, rep := collect(t, "nvidia-smi-q-legacy.txt", Options{})
	if rep.Status != model.SourceOK || len(c.GPUs) != 2 {
		t.Fatalf("report %+v, %d GPUs", rep, len(c.GPUs))
	}
	g := c.GPUs[0]
	// Page retirement instead of row remapping; single and double bit
	// counts per kind of memory. The device memory is the DRAM, the rest
	// of the total is in the GPU.
	want := model.GPUMem{ObservedAt: time.Date(2026, 11, 12, 3, 14, 7, 0, time.UTC), Kind: model.GPUSnapshot, GPUSerial: "0320000000042",
		GPUUUID: "GPU-0d6f0b4a-7a0c-11ea-bc55-0242ac130003", GPUIndex: some(0), PCIBus: "0000:af:00.0", Model: "Example GPU 16GB",
		RetiredSBE: some(3), RetiredDBE: some(2), RetiredPending: model.Some(true), ECCEnabled: model.Some(true),
		DRAMCorrectable: some(57), DRAMUncorrectable: some(2), DRAMCorrectableVolatile: some(3), DRAMUncorrectableVolatile: some(0),
		SRAMCorrectable: some(2), SRAMUncorrectable: some(0), SRAMCorrectableVolatile: some(1), SRAMUncorrectableVolatile: some(0),
		TimeQuality: model.TimeExact, Source: "nvidia-smi"}
	if got := strip(c.GPUs[:1])[0]; !reflect.DeepEqual(got, want) {
		t.Errorf("older GPU:\n got %+v\nwant %+v", got, want)
	}
	// A GPU without ECC reports nothing; it is still listed.
	n := c.GPUs[1]
	if n.GPUSerial != "" || n.GPUUUID == "" || n.ECCEnabled.Valid || n.DRAMCorrectable.Valid || n.RetiredSBE.Valid || n.RemapPending.Valid || n.PCIBus != "0000:d8:00.0" {
		t.Errorf("GPU without ECC = %+v", n)
	}
	if !strings.Contains(strings.Join(rep.Notes, "\n"), "1 GPUs report no ECC counts, retired pages or remapped rows") {
		t.Errorf("notes = %v", rep.Notes)
	}
	_ = g
}

func TestOutputsThatHoldNoGPU(t *testing.T) {
	for name, c := range map[string]struct {
		content string
		status  model.SourceStatus
		detail  string
	}{
		"empty":         {"", model.SourceEmpty, "no output"},
		"no devices":    {"No devices were found\n", model.SourceEmpty, "nvidia-smi found no GPU"},
		"driver down":   {"NVIDIA-SMI has failed because it couldn't communicate with the NVIDIA driver. Make sure that the latest NVIDIA driver is installed and running.\n\n", model.SourceError, "NVIDIA-SMI has failed because it couldn't communicate with the NVIDIA driver"},
		"nvml":          {"Failed to initialize NVML: Driver/library version mismatch\n", model.SourceError, "Failed to initialize NVML: Driver/library version mismatch"},
		"header only":   {"\n==============NVSMI LOG==============\n\nTimestamp : Fri Nov 13 09:30:00 2026\nAttached GPUs : 0\n", model.SourceEmpty, "the output lists no GPU"},
		"xml no gpu":    {`<?xml version="1.0" ?><nvidia_smi_log><timestamp>Fri Nov 13 09:30:00 2026</timestamp><attached_gpus>0</attached_gpus></nvidia_smi_log>`, model.SourceEmpty, "the output lists no GPU"},
		"other xml":     {`<?xml version="1.0"?><html><body>hello</body></html>`, model.SourceError, "no nvidia_smi_log element"},
		"other text":    {"Handle 0x1100, DMI type 17, 92 bytes\nMemory Device\n", model.SourceError, "not the output of nvidia-smi -q: Handle 0x1100"},
		"default table": {"+-----------------------------------------------------------------------------+\n| NVIDIA-SMI 550.54.15    Driver Version: 550.54.15    CUDA Version: 12.4     |\n", model.SourceError, "not the output of nvidia-smi -q"},
	} {
		var got adapter.Collected
		rep := NewReader(strings.NewReader(c.content), "x", Options{}).Collect(ctx, &got)
		if rep.Status != c.status || !strings.Contains(rep.Detail, c.detail) || len(got.GPUs) != 0 {
			t.Errorf("%s: status %s, detail %q, %d GPUs; want %s, %q", name, rep.Status, rep.Detail, len(got.GPUs), c.status, c.detail)
		}
	}
	// A GPU element that was cut off before anything named the GPU.
	var cut adapter.Collected
	rep := NewReader(strings.NewReader(`<nvidia_smi_log><gpu id="x"><uuid>`), "x", Options{}).Collect(ctx, &cut)
	if rep.Status != model.SourcePartial || rep.Skipped != 1 || len(cut.GPUs) != 0 || !strings.Contains(strings.Join(rep.Notes, "\n"), "the GPU has no UUID, serial number or PCI address") {
		t.Errorf("cut XML: %+v", rep)
	}

	var c adapter.Collected
	dir := t.TempDir()
	if rep := NewFile(filepath.Join(dir, "missing"), Options{}).Collect(ctx, &c); rep.Status != model.SourceAbsent {
		t.Errorf("missing file: %+v", rep)
	}
	if rep := NewFile(dir, Options{}).Collect(ctx, &c); rep.Status != model.SourceError || rep.Detail != "is a directory" {
		t.Errorf("directory: %+v", rep)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if rep := NewFile(testutil.Testdata(t, "nvidia", "nvidia-smi-q.txt"), Options{}).Collect(cancelled, &c); rep.Status != model.SourceError {
		t.Errorf("cancelled context: %+v", rep)
	}
}

// A GPU element that is cut off still yields the GPUs before it.
func TestDamagedXMLKeepsWhatWasRead(t *testing.T) {
	data := testutil.ReadTestdata(t, "nvidia", "nvidia-smi-q.xml")
	i := bytes.LastIndex(data, []byte("<remapped_rows>"))
	var c adapter.Collected
	rep := NewReader(bytes.NewReader(data[:i+40]), "cut", Options{}).Collect(ctx, &c)
	if rep.Status != model.SourceOK || len(c.GPUs) != 2 {
		t.Fatalf("report %+v, %d GPUs", rep, len(c.GPUs))
	}
	// The second GPU lost its remapping state, not its ECC counts.
	if g := c.GPUs[1]; g.RemapFailure.Valid || g.DRAMUncorrectable != some(1) {
		t.Errorf("cut GPU = %+v", g)
	}
}

type failing struct {
	adapter.Collected
	err error
}

func (f *failing) GPU(*model.GPUMem) error { return f.err }

func TestSinkErrorStopsTheRead(t *testing.T) {
	boom := errors.New("ledger is full")
	rep := NewFile(testutil.Testdata(t, "nvidia", "nvidia-smi-q.txt"), Options{}).Collect(ctx, &failing{err: boom})
	if rep.Status != model.SourceError || !errors.Is(rep.SinkErr, boom) {
		t.Errorf("report = %+v", rep)
	}
}

func TestBusAddress(t *testing.T) {
	for in, want := range map[string]string{
		"00000000:3B:00.0":   "0000:3b:00.0",
		"0000:3b:00.0":       "0000:3b:00.0",
		"00010000:3B:00.0":   "00010000:3b:00.0", // a domain that needs more than four digits
		"0001:AF:1F.7":       "0001:af:1f.7",
		" 00000000:07:00.0 ": "0000:07:00.0",
		"3B:00.0":            "",
		"N/A":                "",
		"":                   "",
	} {
		if got := busAddress(in); got != want {
			t.Errorf("busAddress(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValues(t *testing.T) {
	for in, want := range map[string]model.Opt[int]{
		"0": some(0), "31": some(31), "638 bank(s)": some(638), " 2 ": some(2), "N/A": {}, "": {}, "-1": {}, "Yes": {},
		"99999999999999999999": {},
	} {
		if got := number(in); got != want {
			t.Errorf("number(%q) = %v, want %v", in, got, want)
		}
	}
	for in, want := range map[string]model.Opt[bool]{
		"Yes": model.Some(true), "No": model.Some(false), "Enabled": model.Some(true), "Disabled": model.Some(false),
		"N/A": {}, "": {}, "Requested functionality has been deprecated": {},
	} {
		if got := flag(in); got != want {
			t.Errorf("flag(%q) = %v, want %v", in, got, want)
		}
	}
	if got := sum(model.Opt[int]{}, model.Opt[int]{}); got.Valid {
		t.Errorf("sum of nothing = %v", got)
	}
	if got := sum(some(1), model.Opt[int]{}, some(2)); got != some(3) {
		t.Errorf("sum = %v", got)
	}
}

func TestSniff(t *testing.T) {
	for _, name := range []string{"nvidia-smi-q.txt", "nvidia-smi-q.xml", "nvidia-smi-q-legacy.txt"} {
		if !Sniff(testutil.ReadTestdata(t, "nvidia", name)) {
			t.Errorf("%s is not recognised", name)
		}
	}
	for name, data := range map[string]string{
		"table": "| NVIDIA-SMI 550.54.15    Driver Version: 550.54.15 |\n",
		"dmesg": "[    0.000000] NVRM: loading NVIDIA UNIX x86_64 Kernel Module\n",
		"xml":   `<?xml version="1.0"?><root/>`,
		"empty": "",
	} {
		if Sniff([]byte(data)) {
			t.Errorf("%s is taken for the output of nvidia-smi -q", name)
		}
	}
}

// script writes an executable that stands in for nvidia-smi.
func script(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a shell")
	}
	p := filepath.Join(t.TempDir(), "nvidia-smi")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCommand(t *testing.T) {
	xmlPath := testutil.Testdata(t, "nvidia", "nvidia-smi-q.xml")
	// The program is asked for the XML form and nothing else.
	ok := script(t, `[ "$1" = "-q" ] && [ "$2" = "-x" ] && [ $# -eq 2 ] || { echo "unexpected arguments: $*" >&2; exit 2; }
cat "`+xmlPath+`"`)
	var c adapter.Collected
	a := NewCommand(ok, 0, Options{})
	rep := a.Collect(ctx, &c)
	if rep.Status != model.SourceOK || len(c.GPUs) != 2 || a.Origin() != ok+" -q -x" || a.Kind() != Kind {
		t.Errorf("report %+v, %d GPUs, origin %q", rep, len(c.GPUs), a.Origin())
	}
	if c.GPUs[0].RawRef != ok+" -q -x#gpu 0" {
		t.Errorf("raw reference = %q", c.GPUs[0].RawRef)
	}

	for name, tc := range map[string]struct {
		body   string
		status model.SourceStatus
		detail string
	}{
		"driver down": {`echo "NVIDIA-SMI has failed because it couldn't communicate with the NVIDIA driver."; exit 9`, model.SourceError, "NVIDIA-SMI has failed"},
		"no devices":  {`echo "No devices were found"; exit 6`, model.SourceEmpty, "nvidia-smi found no GPU"},
		"stderr only": {`echo "cannot open shared object file" >&2; exit 127`, model.SourceError, "nvidia-smi failed: cannot open shared object file"},
		"silent":      {`exit 3`, model.SourceError, "nvidia-smi failed: exit status 3"},
		"hangs":       {`sleep 60`, model.SourceError, "nvidia-smi did not answer within 1s"},
	} {
		// The first run of a new program can take a while on some systems;
		// only the program that hangs gets a short limit.
		timeout := 30 * time.Second
		if name == "hangs" {
			timeout = time.Second
		}
		var got adapter.Collected
		start := time.Now()
		rep := NewCommand(script(t, tc.body), timeout, Options{}).Collect(ctx, &got)
		if rep.Status != tc.status || !strings.Contains(rep.Detail, tc.detail) || len(got.GPUs) != 0 {
			t.Errorf("%s: status %s, detail %q; want %s, %q", name, rep.Status, rep.Detail, tc.status, tc.detail)
		}
		if d := time.Since(start); name == "hangs" && d > 10*time.Second {
			t.Errorf("%s: took %v", name, d)
		}
	}

	// A host without the program.
	missing := NewCommand(filepath.Join(t.TempDir(), "nvidia-smi"), 0, Options{})
	if rep := missing.Collect(ctx, &c); rep.Status != model.SourceAbsent || !strings.Contains(rep.Detail, "not installed") {
		t.Errorf("missing program: %+v", rep)
	}
	t.Setenv("PATH", t.TempDir())
	lookup := NewCommand("", 0, Options{})
	if rep := lookup.Collect(ctx, &c); rep.Status != model.SourceAbsent || lookup.Origin() != "nvidia-smi -q -x" {
		t.Errorf("program not in PATH: %+v, origin %q", rep, lookup.Origin())
	}
	// A program that may not be run.
	denied := filepath.Join(t.TempDir(), "nvidia-smi")
	if err := os.WriteFile(denied, []byte("#!/bin/sh\nexit 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if rep := NewCommand(denied, 0, Options{}).Collect(ctx, &c); rep.Status != model.SourceUnreadable {
		t.Errorf("program without execute permission: %+v", rep)
	}
}
