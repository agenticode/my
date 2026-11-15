package testledger

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/klog"
	"github.com/agenticode/my/internal/adapter/nvidia"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/testutil"
)

// GPUHost is the name of the host of the GPU ledger, and GPUNow the time
// of its import.
const GPUHost = "gpu-01"

// GPUNow is the time the GPU ledger was imported at.
var GPUNow = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

// gpuQuery is a query output with one GPU per state of the repair budget,
// beside the two GPUs of the nvidia-smi fixtures: a remapping that waits
// for a reset, a bank without spare rows, rows remapped with room left,
// corrected errors and no repair, nothing at all, and an older GPU that
// retires pages. A second reading of the first GPU, made a day later with
// a query that asked for less, leaves its earlier values standing.
const gpuQuery = `timestamp, index, name, serial, uuid, pci.bus_id, ecc.mode.current, ecc.errors.corrected.aggregate.dram, ecc.errors.uncorrected.aggregate.dram, ecc.errors.corrected.volatile.dram, ecc.errors.uncorrected.volatile.dram, retired_pages.sbe, retired_pages.dbe, retired_pages.pending, remapped_rows.correctable, remapped_rows.uncorrectable, remapped_rows.pending, remapped_rows.failure, remapped_rows.histogram.max, remapped_rows.histogram.high, remapped_rows.histogram.partial, remapped_rows.histogram.low, remapped_rows.histogram.none
2026/11/13 09:30:00.000, 2, Example GPU 80GB, 1650000001201, GPU-11111111-2222-3333-4444-555555555501, 00000000:AF:00.0, Enabled, 12, 3, 0, 1, [N/A], [N/A], [N/A], 0, 3, Yes, No, 636, 1, 2, 1, 0
2026/11/13 09:30:00.000, 3, Example GPU 80GB, 1650000001302, GPU-11111111-2222-3333-4444-555555555502, 00000000:D8:00.0, Enabled, 40, 9, 0, 0, [N/A], [N/A], [N/A], 1, 9, No, No, 637, 0, 1, 1, 1
2026/11/13 09:30:00.000, 4, Example GPU 80GB, 1650000001403, GPU-11111111-2222-3333-4444-555555555503, 00000000:18:00.0, Enabled, 7, 0, 0, 0, [N/A], [N/A], [N/A], 2, 0, No, No, 640, 0, 0, 0, 0
2026/11/13 09:30:00.000, 5, Example GPU 80GB, 1650000001504, GPU-11111111-2222-3333-4444-555555555504, 00000000:19:00.0, Enabled, 31, 0, 2, 0, [N/A], [N/A], [N/A], 0, 0, No, No, 640, 0, 0, 0, 0
2026/11/13 09:30:00.000, 6, Example GPU 80GB, 1650000001605, GPU-11111111-2222-3333-4444-555555555505, 00000000:5E:00.0, Enabled, 0, 0, 0, 0, [N/A], [N/A], [N/A], 0, 0, No, No, 640, 0, 0, 0, 0
2026/11/13 09:30:00.000, 7, Example GPU 16GB, 0320000000716, GPU-11111111-2222-3333-4444-555555555506, 00000000:5F:00.0, Enabled, 5, 1, 0, 0, 3, 1, No, [N/A], [N/A], [N/A], [N/A], [N/A], [N/A], [N/A], [N/A], [N/A]
`

const gpuQueryLater = `timestamp, uuid, remapped_rows.pending, remapped_rows.failure
2026/11/14 08:00:00.000, GPU-11111111-2222-3333-4444-555555555501, Yes, No
`

// GPUPath returns the file of a ledger with GPU memory records: the Xid
// lines and the nvidia-smi output of the fixtures, and a query output with
// a GPU in every state of the repair budget.
func GPUPath(tb testing.TB) string {
	tb.Helper()
	shared.Lock()
	defer shared.Unlock()
	const key = "gpu"
	if p, ok := shared.paths[key]; ok {
		return p
	}
	if shared.dir == "" {
		dir, err := os.MkdirTemp("", "my-testledger-")
		if err != nil {
			tb.Fatal(err)
		}
		shared.dir, shared.ledgers, shared.paths = dir, map[string]*store.Store{}, map[string]string{}
	}
	dir := filepath.Join(shared.dir, key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		tb.Fatal(err)
	}
	ctx := context.Background()
	path := filepath.Join(dir, "ledger.db")
	s, err := store.Open(ctx, path, store.Create)
	if err != nil {
		tb.Fatal(err)
	}
	_, err = ingest.Run(ctx, s, []adapter.Adapter{
		// The fixtures are read under their names in the repository: a
		// record refers to where it was read from, and the path of one
		// checkout in a golden file fails the test in every other.
		klog.NewReader(bytes.NewReader(testutil.ReadTestdata(tb, "klog", "nvidia-xid.messages")), "testdata/klog/nvidia-xid.messages", klog.Options{}),
		nvidia.NewReader(bytes.NewReader(testutil.ReadTestdata(tb, "nvidia", "nvidia-smi-q.xml")), "testdata/nvidia/nvidia-smi-q.xml", nvidia.Options{}),
		nvidia.NewReader(strings.NewReader(gpuQuery), "query-gpu.csv", nvidia.Options{}),
		nvidia.NewReader(strings.NewReader(gpuQueryLater), "query-gpu-later.csv", nvidia.Options{}),
	}, ingest.Options{Host: GPUHost, Kind: model.ScanImport, Now: GPUNow})
	if cerr := s.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		tb.Fatalf("GPU ledger: %v", err)
	}
	shared.paths[key] = path
	return path
}

// OpenGPU returns the GPU ledger, opened read-only and shared by the tests
// of the binary.
func OpenGPU(tb testing.TB) *store.Store {
	tb.Helper()
	p := GPUPath(tb)
	shared.Lock()
	defer shared.Unlock()
	const key = "gpu"
	if s, ok := shared.ledgers[key]; ok {
		return s
	}
	s, err := store.Open(context.Background(), p, store.ReadOnly)
	if err != nil {
		tb.Fatal(err)
	}
	shared.ledgers[key] = s
	return s
}
