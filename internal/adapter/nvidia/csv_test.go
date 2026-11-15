package nvidia

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/testutil"
)

var csvNow = time.Date(2026, 11, 14, 1, 2, 3, 0, time.UTC)

func readCSV(t *testing.T, text string, opts Options) (*adapter.Collected, adapter.Report) {
	t.Helper()
	if opts.Now.IsZero() {
		opts.Now = csvNow
	}
	var c adapter.Collected
	rep := NewReader(strings.NewReader(text), "query.csv", opts).Collect(ctx, &c)
	return &c, rep
}

func notesOf(rep adapter.Report) string { return strings.Join(rep.Notes, "\n") }

// The full query of a public capture: 241 properties of one H200.
func TestQueryGPUCapture(t *testing.T) {
	c, rep := collect(t, "query-gpu-h200.csv", Options{})
	if rep.Status != model.SourceOK || rep.Records != 1 || rep.Skipped != 0 || len(c.GPUs) != 1 {
		t.Fatalf("report %+v, %d GPUs", rep, len(c.GPUs))
	}
	no := model.Some(false)
	want := model.GPUMem{
		ObservedAt: time.Date(2026, 9, 1, 20, 48, 13, 731e6, time.UTC), Kind: model.GPUSnapshot,
		// The capture masks the serial number with zeros, which is no
		// serial number; the UUID names the GPU.
		GPUUUID: "GPU-00000000-0000-0000-0000-000000000000", GPUIndex: some(0), PCIBus: "0000:83:00.0", Model: "NVIDIA H200",
		RemapCorrectable: some(0), RemapUncorrectable: some(0), RemapPending: no, RemapFailure: no,
		BankMax: some(3072), BankHigh: some(0), BankPartial: some(0), BankLow: some(0), BankNone: some(0),
		ECCEnabled: model.Some(true), DRAMCorrectable: some(0), DRAMUncorrectable: some(0), DRAMCorrectableVolatile: some(0), DRAMUncorrectableVolatile: some(0),
		SRAMCorrectable: some(0), SRAMUncorrectable: some(0), SRAMCorrectableVolatile: some(0), SRAMUncorrectableVolatile: some(0),
		SRAMThresholdExceeded: no, TimeQuality: model.TimeExact, Source: "nvidia-smi",
	}
	if got := strip(c.GPUs); !reflect.DeepEqual(got[0], want) {
		t.Errorf("snapshot\n got %+v\nwant %+v", got[0], want)
	}
	g := c.GPUs[0]
	if !strings.HasSuffix(g.RawRef, "query-gpu-h200.csv#L2") || g.Fingerprint == "" {
		t.Errorf("reference %q, fingerprint %q", g.RawRef, g.Fingerprint)
	}
	// The raw record holds what the snapshot was made of, not the other
	// two hundred columns.
	for _, part := range []string{"uuid: GPU-00000000-0000-0000-0000-000000000000", "remapped_rows.histogram.max: 3072", "remapped_rows.failure: No", "ecc.mode.current: Enabled"} {
		if !strings.Contains(g.Raw, part) {
			t.Errorf("raw record lacks %q:\n%s", part, g.Raw)
		}
	}
	if strings.Contains(g.Raw, "temperature") || strings.Contains(g.Raw, "N/A") || strings.Count(g.Raw, "\n") > 60 {
		t.Errorf("raw record holds more than the memory columns:\n%s", g.Raw)
	}
	for _, part := range []string{
		"driver version 610.43.02", "1 GPUs", "local time of the host and was read as UTC",
		"columns about memory errors that were not read: ecc.errors.corrected.aggregate.cbu",
	} {
		if !strings.Contains(notesOf(rep), part) {
			t.Errorf("notes lack %q:\n%s", part, notesOf(rep))
		}
	}
	// The unread columns are named: among them the counts of remapped rows
	// that are no longer active, which a snapshot has no place for.
	c2, rep2 := readCSV(t, "uuid, remapped_rows.correctable_inactive, remapped_rows.uncorrectable_inactive\nGPU-b850f46d-d5ea-c752-ddf3-c4453e44d3f7, 1, 2\n", Options{})
	if len(c2.GPUs) != 1 || !strings.Contains(notesOf(rep2), "not read: remapped_rows.correctable_inactive, remapped_rows.uncorrectable_inactive") {
		t.Errorf("inactive remappings: %d GPUs, notes %q", len(c2.GPUs), rep2.Notes)
	}
}

// In MIG mode the driver reports the bank histogram and no row counts.
func TestQueryGPUCaptureMIG(t *testing.T) {
	c, rep := collect(t, "query-gpu-h200-mig.csv", Options{})
	if rep.Status != model.SourceOK || len(c.GPUs) != 1 {
		t.Fatalf("report %+v", rep)
	}
	g := c.GPUs[0]
	if g.RemapCorrectable.Valid || g.RemapUncorrectable.Valid || g.RemapPending.Valid || g.RemapFailure.Valid {
		t.Errorf("row counts of a GPU that reports none: %+v", g)
	}
	if !reflect.DeepEqual([]model.Opt[int]{g.BankMax, g.BankHigh, g.BankPartial, g.BankLow, g.BankNone}, []model.Opt[int]{some(3072), some(0), some(0), some(0), some(0)}) {
		t.Errorf("bank histogram: %+v", g)
	}
}

// A GPU without ECC, read by a driver that knows no row remapping.
func TestQueryGPUCaptureWithoutECC(t *testing.T) {
	c, rep := collect(t, "query-gpu-rtx2080-470.csv", Options{})
	if rep.Status != model.SourceOK || len(c.GPUs) != 1 {
		t.Fatalf("report %+v", rep)
	}
	g := c.GPUs[0]
	if g.GPUSerial != "" || g.GPUUUID == "" || g.Model != "NVIDIA GeForce RTX 2080 SUPER" || g.PCIBus != "0000:01:00.0" || hasMemoryData(g) || g.ECCEnabled.Valid {
		t.Errorf("snapshot: %+v", g)
	}
	if !strings.Contains(notesOf(rep), "1 GPUs report no ECC counts, retired pages or remapped rows") {
		t.Errorf("notes: %q", rep.Notes)
	}
}

// The published sample of --query-remapped-rows names its GPUs by their
// PCI address alone. Such a GPU cannot be keyed: the inventory says so and
// nothing is stored.
func TestQueryRemappedRowsSample(t *testing.T) {
	c, rep := collect(t, "query-remapped-rows.csv", Options{})
	if rep.Status != model.SourceError || len(c.GPUs) != 0 || rep.Records != 3 || rep.Skipped != 3 {
		t.Fatalf("report %+v, %d GPUs", rep, len(c.GPUs))
	}
	if !strings.Contains(rep.Detail, "no uuid and no serial column: its 2 GPUs cannot be told from the GPUs of another reading") {
		t.Errorf("detail: %q", rep.Detail)
	}
	// The line that stands for the rows its author left out is no row.
	if !strings.Contains(notesOf(rep), "a line with 1 value where the output has 6 columns") {
		t.Errorf("notes: %q", rep.Notes)
	}
}

// The other selective queries list processes and pages. They are told
// apart and not read.
func TestOtherQueries(t *testing.T) {
	c, rep := collect(t, "query-retired-pages-h200.csv", Options{})
	if rep.Status != model.SourceEmpty || len(c.GPUs) != 0 || !strings.Contains(rep.Detail, "--query-retired-pages (a list of retired pages): it is not read") {
		t.Errorf("retired pages: report %+v", rep)
	}
	c, rep = readCSV(t, "timestamp, gpu_name, gpu_uuid, pid, process_name, used_gpu_memory [MiB]\n2026/09/01 20:48:13.960, NVIDIA H200, GPU-b850f46d-d5ea-c752-ddf3-c4453e44d3f7, 4242, python, 1024 MiB\n", Options{})
	if rep.Status != model.SourceEmpty || len(c.GPUs) != 0 || !strings.Contains(rep.Detail, "a list of processes") {
		t.Errorf("compute apps: report %+v", rep)
	}
}

// A query that asks for what "nvidia-smi -q" prints gives the snapshot
// that output gives, and the same record identity.
func TestQueryAndFullOutputAgree(t *testing.T) {
	text := "$ nvidia-smi --query-gpu=timestamp,index,name,serial,uuid,pci.bus_id,ecc.mode.current," +
		"ecc.errors.corrected.volatile.dram,ecc.errors.uncorrected.volatile.dram,ecc.errors.corrected.aggregate.dram,ecc.errors.uncorrected.aggregate.dram," +
		"ecc.errors.corrected.volatile.sram,ecc.errors.uncorrected.volatile.sram.parity,ecc.errors.uncorrected.volatile.sram.secded," +
		"ecc.errors.corrected.aggregate.sram,ecc.errors.uncorrected.aggregate.sram.parity,ecc.errors.uncorrected.aggregate.sram.secded," +
		"ecc.errors.uncorrected.aggregate.sram.thresholdExceeded,remapped_rows.correctable,remapped_rows.uncorrectable,remapped_rows.pending,remapped_rows.failure," +
		"remapped_rows.histogram.max,remapped_rows.histogram.high,remapped_rows.histogram.partial,remapped_rows.histogram.low,remapped_rows.histogram.none --format=csv,noheader\n" +
		"2026/11/13 09:30:00.000, 0, Example GPU 80GB, 1650000000412, GPU-b850f46d-d5ea-c752-ddf3-c4453e44d3f7, 00000000:3B:00.0, Enabled, 0, 2, 31, 2, 0, 0, 0, 0, 0, 0, [N/A], 0, 2, No, No, 638, 2, 0, 0, 0\n" +
		"2026/11/13 09:30:00.000, 1, Example GPU 80GB, 1650000000977, GPU-5a1c2b3d-4e5f-6071-8293-a4b5c6d7e8f9, 00000000:86:00.0, Enabled, 0, 1, 0, 1, 0, 1, 2, 4, 1, 2, Yes, 0, 1, Yes, Yes, 640, 0, 0, 0, 0\n"
	c, rep := readCSV(t, text, Options{})
	if rep.Status != model.SourceOK || len(c.GPUs) != 2 {
		t.Fatalf("report %+v, %d GPUs", rep, len(c.GPUs))
	}
	if !strings.Contains(notesOf(rep), "no header line; its columns were taken from the query: timestamp,index,name,serial,uuid,pci.bus_id,") {
		t.Errorf("notes: %q", rep.Notes)
	}
	want := wantGPUs(time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC))
	// The first GPU of the full output reports no threshold flag either.
	if got := strip(c.GPUs); !reflect.DeepEqual(got, want) {
		t.Errorf("snapshots\n got %+v\nwant %+v", got, want)
	}
	full, _ := collect(t, "nvidia-smi-q.txt", Options{})
	for i := range full.GPUs {
		if full.GPUs[i].Fingerprint != c.GPUs[i].Fingerprint {
			t.Errorf("GPU %d: one reading, two record identities", i)
		}
	}
}

func TestQueryForms(t *testing.T) {
	const a, b = "GPU-b850f46d-d5ea-c752-ddf3-c4453e44d3f7", "GPU-5a1c2b3d-4e5f-6071-8293-a4b5c6d7e8f9"
	yes := model.Some(true)
	for _, tc := range []struct {
		name, text string
		opts       Options
		status     model.SourceStatus
		detail     string // part of the detail
		note       string // part of a note
		records    int
		skipped    int
		check      func(t *testing.T, g []*model.GPUMem)
	}{
		{
			name:   "second names, in another order",
			text:   "remapped_rows.dbe, gpu_bus_id, remapped_rows.sbe, gpu_serial, retired_pages.dbe, gpu_uuid, gpu_name, retired_pages.sbe\n2, 00000000:3B:00.0, 5, 1650000000412, 1, " + a + ", Example GPU, 7\n",
			status: model.SourceOK, records: 1,
			check: func(t *testing.T, g []*model.GPUMem) {
				x := g[0]
				if x.RemapUncorrectable != some(2) || x.RemapCorrectable != some(5) || x.RetiredDBE != some(1) || x.RetiredSBE != some(7) ||
					x.GPUSerial != "1650000000412" || x.GPUUUID != a || x.PCIBus != "0000:3b:00.0" || x.Model != "Example GPU" || x.GPUIndex.Valid {
					t.Errorf("snapshot: %+v", x)
				}
			},
		},
		{
			name:   "the row remapping query with a UUID",
			text:   "gpu_uuid, gpu_bus_id, remapped_rows.correctable, remapped_rows.uncorrectable, remapped_rows.pending, remapped_rows.failure\n" + a + ", 00000000:3B:00.0, 0, 2, No, No\n" + b + ", 00000000:86:00.0, 0, 1, Yes, Yes\n",
			status: model.SourceOK, records: 2, note: "2 GPUs",
			check: func(t *testing.T, g []*model.GPUMem) {
				if g[1].RemapFailure != yes || g[1].RemapPending != yes || g[0].RemapUncorrectable != some(2) || g[0].RemapFailure != model.Some(false) {
					t.Errorf("snapshots: %+v %+v", g[0], g[1])
				}
			},
		},
		{
			name:   "units in the header and in the values",
			text:   "uuid, memory.total [MiB], ecc.errors.corrected.aggregate.dram, temperature.gpu\n" + a + ", 81559 MiB, 31, 38\n",
			status: model.SourceOK, records: 1,
			check: func(t *testing.T, g []*model.GPUMem) {
				if g[0].DRAMCorrectable != some(31) {
					t.Errorf("snapshot: %+v", g[0])
				}
			},
		},
		{
			name:   "values the GPU does not have",
			text:   "uuid, serial, remapped_rows.failure, remapped_rows.pending, retired_pages.sbe, retired_pages.pending, ecc.errors.corrected.aggregate.dram\n" + a + ", [N/A], [Not Supported], [N/A], N/A, [Unknown Error], [Requested functionality has been deprecated]\n",
			status: model.SourceOK, records: 1, note: "1 GPUs report no ECC counts",
			check: func(t *testing.T, g []*model.GPUMem) {
				if hasMemoryData(g[0]) || g[0].GPUSerial != "" || g[0].RetiredPending.Valid {
					t.Errorf("snapshot: %+v", g[0])
				}
			},
		},
		{
			name:   "an older GPU: device memory and a total",
			text:   "uuid, ecc.errors.corrected.aggregate.device_memory, ecc.errors.corrected.aggregate.total, ecc.errors.uncorrected.aggregate.device_memory, ecc.errors.uncorrected.aggregate.total, ecc.errors.corrected.aggregate.dram, ecc.errors.corrected.aggregate.sram\n" + a + ", 12, 15, 1, 1, [N/A], [N/A]\n",
			status: model.SourceOK, records: 1,
			check: func(t *testing.T, g []*model.GPUMem) {
				if g[0].DRAMCorrectable != some(12) || g[0].SRAMCorrectable != some(3) || g[0].DRAMUncorrectable != some(1) || g[0].SRAMUncorrectable != some(0) {
					t.Errorf("snapshot: %+v", g[0])
				}
			},
		},
		{
			name:   "uncorrectable SRAM errors in one column and in two",
			text:   "uuid, ecc.errors.uncorrected.aggregate.sram, ecc.errors.uncorrected.aggregate.sram.parity, ecc.errors.uncorrected.aggregate.sram.secded, ecc.errors.uncorrected.volatile.sram.parity, ecc.errors.uncorrected.volatile.sram.secded\n" + a + ", 3, 1, 2, 1, 2\n",
			status: model.SourceOK, records: 1,
			check: func(t *testing.T, g []*model.GPUMem) {
				// The one column is the sum of the two; it is not added to them.
				if g[0].SRAMUncorrectable != some(3) || g[0].SRAMUncorrectableVolatile != some(3) {
					t.Errorf("snapshot: %+v", g[0])
				}
			},
		},
		{
			name:   "no header, the fields from the caller",
			text:   "0, " + a + ", Yes\n1, " + b + ", No\n",
			opts:   Options{Query: "index,uuid,remapped_rows.failure"},
			status: model.SourceOK, records: 2, note: "its columns were taken from the query: index,uuid,remapped_rows.failure",
			check: func(t *testing.T, g []*model.GPUMem) {
				if g[0].RemapFailure != yes || g[0].GPUIndex != some(0) || g[1].GPUIndex != some(1) || g[1].GPUUUID != b {
					t.Errorf("snapshots: %+v %+v", g[0], g[1])
				}
			},
		},
		{
			name:   "no header and nothing that names the columns",
			text:   "0, " + a + ", Yes\n1, " + b + ", No\n",
			status: model.SourceError, detail: "no header line and no command line: its columns are not known", records: 2, skipped: 2,
		},
		{
			name:   "a header wins over the fields of the caller",
			text:   "uuid, remapped_rows.failure\n" + a + ", Yes\n",
			opts:   Options{Query: "remapped_rows.failure,uuid"},
			status: model.SourceOK, records: 1,
			check: func(t *testing.T, g []*model.GPUMem) {
				if g[0].GPUUUID != a || g[0].RemapFailure != yes {
					t.Errorf("snapshot: %+v", g[0])
				}
			},
		},
		{
			name:   "rows that do not fit the fields of the caller",
			text:   "0, " + a + "\n1, " + b + ", No\n",
			opts:   Options{Query: "index,uuid,remapped_rows.failure"},
			status: model.SourcePartial, records: 2, skipped: 1, note: "a line with 2 values where the output has 3 columns",
		},
		{
			name:   "a GPU without UUID and serial number among others",
			text:   "index, uuid, serial, pci.bus_id, remapped_rows.failure\n0, " + a + ", 1650000000412, 00000000:3B:00.0, No\n1, [N/A], [N/A], 00000000:86:00.0, Yes\n2, [N/A], [N/A], [N/A], Yes\n",
			status: model.SourcePartial, records: 3, skipped: 2,
			note: "skipped 2 GPUs that report neither a UUID nor a serial number (0000:86:00.0, index 2)",
			check: func(t *testing.T, g []*model.GPUMem) {
				if len(g) != 1 || g[0].GPUUUID != a {
					t.Errorf("snapshots: %+v", g)
				}
			},
		},
		{
			name:   "no GPU with a UUID or serial number",
			text:   "index, uuid, remapped_rows.failure\n0, [N/A], Yes\n",
			status: model.SourceError, detail: "no GPU of the output reports a UUID or a serial number", records: 1, skipped: 1,
		},
		{
			name:   "only the index",
			text:   "index, remapped_rows.failure\n0, Yes\n1, No\n",
			status: model.SourceError, detail: "no uuid and no serial column: its 2 GPUs cannot be told", records: 2, skipped: 2,
		},
		{
			name:   "a query in a loop",
			text:   "timestamp, uuid, ecc.errors.corrected.volatile.dram\n2026/11/13 09:30:00.000, " + a + ", 3\ntimestamp, uuid, ecc.errors.corrected.volatile.dram\n2026/11/13 09:30:05.000, " + a + ", 4\n",
			status: model.SourceOK, records: 2,
			check: func(t *testing.T, g []*model.GPUMem) {
				if len(g) != 2 || g[1].ObservedAt.Sub(g[0].ObservedAt) != 5*time.Second || g[1].DRAMCorrectableVolatile != some(4) || g[0].Fingerprint == g[1].Fingerprint {
					t.Errorf("snapshots: %+v", g)
				}
			},
		},
		{
			name:   "a query in a loop without a timestamp",
			text:   "uuid, ecc.errors.corrected.volatile.dram\n" + a + ", 3\n" + a + ", 4\n",
			status: model.SourcePartial, records: 2, skipped: 1, note: "without a timestamp column the rows of a repeated query cannot be told apart",
			check: func(t *testing.T, g []*model.GPUMem) {
				if len(g) != 1 || !g[0].ObservedAt.Equal(csvNow) {
					t.Errorf("snapshots: %+v", g)
				}
			},
		},
		{
			name:   "carriage returns, a byte order mark, a comment",
			text:   "\xef\xbb\xbf# saved on the host\r\nuuid, remapped_rows.failure\r\n" + a + ", Yes\r\n\r\n",
			status: model.SourceOK, records: 1,
			check: func(t *testing.T, g []*model.GPUMem) {
				if g[0].RemapFailure != yes {
					t.Errorf("snapshot: %+v", g[0])
				}
			},
		},
		{
			name:   "a line before the header",
			text:   "0\nuuid, remapped_rows.failure\n" + a + ", Yes\n",
			status: model.SourcePartial, records: 2, skipped: 1, note: "a line with 1 value where the output has 2 columns",
			check: func(t *testing.T, g []*model.GPUMem) {
				if len(g) != 1 || g[0].RemapFailure != yes {
					t.Errorf("snapshots: %+v", g)
				}
			},
		},
		{
			name:   "a timestamp that is none",
			text:   "timestamp, uuid\nyesterday, " + a + "\n",
			status: model.SourceOK, records: 1, note: "no readable timestamp column",
			check: func(t *testing.T, g []*model.GPUMem) {
				if !g[0].ObservedAt.Equal(csvNow) {
					t.Errorf("observed at %v", g[0].ObservedAt)
				}
			},
		},
		{
			name:   "a header and no GPU",
			text:   "index, uuid, remapped_rows.failure\n",
			status: model.SourceEmpty, detail: "the output lists no GPU",
		},
		{
			name:   "counts that are no counts",
			text:   "uuid, index, remapped_rows.correctable, remapped_rows.uncorrectable, remapped_rows.failure\n" + a + ", -1, -5, 99999999999999999999, Maybe\n",
			status: model.SourceOK, records: 1,
			check: func(t *testing.T, g []*model.GPUMem) {
				if g[0].GPUIndex.Valid || g[0].RemapCorrectable.Valid || g[0].RemapUncorrectable.Valid || g[0].RemapFailure.Valid {
					t.Errorf("snapshot: %+v", g[0])
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, rep := readCSV(t, tc.text, tc.opts)
			if rep.Status != tc.status || !strings.Contains(rep.Detail, tc.detail) || rep.Records != tc.records || rep.Skipped != tc.skipped {
				t.Errorf("status %s, detail %q, records %d, skipped %d; want %s, %q, %d, %d\nnotes: %q",
					rep.Status, rep.Detail, rep.Records, rep.Skipped, tc.status, tc.detail, tc.records, tc.skipped, rep.Notes)
			}
			if tc.note != "" && !strings.Contains(notesOf(rep), tc.note) {
				t.Errorf("no note with %q in %q", tc.note, rep.Notes)
			}
			if tc.check != nil && len(c.GPUs) > 0 {
				tc.check(t, c.GPUs)
			} else if tc.check != nil {
				t.Errorf("no snapshot")
			}
			if rep.Status != model.SourceOK && rep.Status != model.SourcePartial && len(c.GPUs) != 0 {
				t.Errorf("%d snapshots from a source with status %s", len(c.GPUs), rep.Status)
			}
			for _, g := range c.GPUs {
				if g.Kind != model.GPUSnapshot || g.Source != Kind || g.Fingerprint == "" || g.RawRef == "" || (g.GPUUUID == "" && g.GPUSerial == "") {
					t.Errorf("snapshot the ledger could not key: %+v", g)
				}
			}
		})
	}
}

func TestTimestampColumnZone(t *testing.T) {
	kst := time.FixedZone("KST", 9*3600)
	c, rep := readCSV(t, "timestamp, uuid\n2026/11/13 09:30:00.250, GPU-b850f46d-d5ea-c752-ddf3-c4453e44d3f7\n", Options{Location: kst})
	if len(c.GPUs) != 1 || !c.GPUs[0].ObservedAt.Equal(time.Date(2026, 11, 13, 0, 30, 0, 250e6, time.UTC)) {
		t.Errorf("observed at %v", c.GPUs)
	}
	if strings.Contains(notesOf(rep), "read as UTC") {
		t.Errorf("notes say UTC although a zone was given: %q", rep.Notes)
	}
}

func TestSniffCSV(t *testing.T) {
	for _, name := range []string{"query-gpu-h200.csv", "query-gpu-h200-mig.csv", "query-gpu-rtx2080-470.csv", "query-remapped-rows.csv", "query-retired-pages-h200.csv"} {
		data := testutil.ReadTestdata(t, "nvidia", name)
		if !SniffCSV(data) || Sniff(data) {
			t.Errorf("%s: SniffCSV %v, Sniff %v", name, SniffCSV(data), Sniff(data))
		}
	}
	for _, name := range []string{"nvidia-smi-q.txt", "nvidia-smi-q.xml", "nvidia-smi-q-legacy.txt"} {
		if data := testutil.ReadTestdata(t, "nvidia", name); !Sniff(data) {
			t.Errorf("%s is not recognised as full output", name)
		}
	}
	for text, want := range map[string]bool{
		"uuid\nGPU-b850f46d-d5ea-c752-ddf3-c4453e44d3f7\n":                  true,
		"remapped_rows.failure\nNo\n":                                       true,
		"0, GPU-b850f46d-d5ea-c752-ddf3-c4453e44d3f7, Yes\n":                true, // no header: recognised, and reported as such
		"NVIDIA H200, 00000000:59:00.0, No, No, 0, 0\n":                     true,
		"$ nvidia-smi --query-gpu=index,uuid --format=csv,noheader\n0, x\n": true,
		"name\nvalue\n": false, // one word says nothing
		"host, kernel, uptime\nweb-17, 6.8.0, 12\n": false,
		"0, 1, 2, 3\n": false,
		"1,11/13/2026,09:30:00,Memory #0x02,Correctable ECC,Asserted\n":                    false,
		"[  12.5] EDAC MC0: 1 CE memory read error on CPU_SrcID#0_Ha#0_Chan#1_DIMM#0, x\n": false,
		"Handle 0x1100, DMI type 17, 84 bytes\n":                                           false,
		"":                                                                                 false,
	} {
		if got := SniffCSV([]byte(text)); got != want {
			t.Errorf("SniffCSV(%q) = %v, want %v", text, got, want)
		}
	}
}
