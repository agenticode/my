package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/edacsysfs"
	"github.com/agenticode/my/internal/adapter/hostinfo"
	"github.com/agenticode/my/internal/adapter/klog"
	"github.com/agenticode/my/internal/adapter/rasdaemon"
	"github.com/agenticode/my/internal/adapter/smbios"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/synth"
)

var ctx = context.Background()

// t0 is the reference time of the hand-built fixtures.
var t0 = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

const day = 24 * time.Hour

func newStore(t testing.TB) *store.Store {
	t.Helper()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// Ledgers that several tests read are built once per test run. Building one
// through the adapters takes seconds under the race detector.
var shared struct {
	sync.Mutex
	dir     string
	ledgers map[string]*store.Store
}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "my-engine-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	shared.dir, shared.ledgers = dir, map[string]*store.Store{}
	code := m.Run()
	for _, s := range shared.ledgers {
		s.Close()
	}
	os.RemoveAll(dir)
	os.Exit(code)
}

// sharedLedger returns the ledger of a built-in scenario, read back from
// the files of a host of the given platform, or stored directly when the
// flavor is empty. Tests must not write to it.
func sharedLedger(t testing.TB, scenario string, seed uint64, fl synth.Flavor) *store.Store {
	t.Helper()
	shared.Lock()
	defer shared.Unlock()
	key := fmt.Sprintf("%s-%d-%s", scenario, seed, fl)
	if s, ok := shared.ledgers[key]; ok {
		return s
	}
	ds := generate(t, scenario, seed, nil)
	dir := filepath.Join(shared.dir, key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(ctx, filepath.Join(dir, "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	if fl == "" {
		_, err = ingest.Run(ctx, s, []adapter.Adapter{ds.Adapter()}, ingest.Options{Kind: model.ScanSynthetic, Now: ds.Scenario.End})
	} else {
		err = importTree(s, ds, fl, filepath.Join(dir, "root"), filepath.Join(dir, "tmp"))
	}
	if err != nil {
		t.Fatal(err)
	}
	shared.ledgers[key] = s
	return s
}

// importTree writes the files a host of the given platform would hold for
// the data set below root and reads them back through the input adapters.
func importTree(s *store.Store, ds *synth.Dataset, fl synth.Flavor, root, tmp string) error {
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	if err := ds.WriteHostTree(root, fl); err != nil {
		return err
	}
	at := ds.Scenario.End
	hi := hostinfo.NewRoot(root, hostinfo.Options{Now: at})
	info, _ := hi.Read()
	_, err := ingest.Run(ctx, s, []adapter.Adapter{
		hi,
		rasdaemon.New(filepath.Join(root, "var", "lib", "rasdaemon", "ras-mc_event.db"), rasdaemon.Options{TempDir: tmp}),
		klog.NewFile(filepath.Join(root, "var", "log", "dmesg"), klog.Options{BootTime: info.BootTime, BootID: info.BootID}),
		edacsysfs.NewRoot(root, edacsysfs.Options{Now: at}),
		smbios.NewRoot(root, smbios.Options{Now: at}),
	}, ingest.Options{Kind: model.ScanLive, Now: at})
	return err
}

func noNoise(sc *synth.Scenario) { sc.NoisePerDIMMDay = 0 }

// generate runs a built-in scenario of the synthetic generator.
func generate(t testing.TB, name string, seed uint64, mut func(*synth.Scenario)) *synth.Dataset {
	t.Helper()
	sc, err := synth.Named(name, seed)
	if err != nil {
		t.Fatal(err)
	}
	if mut != nil {
		mut(sc)
	}
	ds, err := synth.Generate(sc)
	if err != nil {
		t.Fatalf("generate %s: %v", name, err)
	}
	return ds
}

// ledgerOf stores a data set with every location field, the way a platform
// with full decoding would report it.
func ledgerOf(t testing.TB, ds *synth.Dataset) *store.Store {
	t.Helper()
	s := newStore(t)
	if _, err := ingest.Run(ctx, s, []adapter.Adapter{ds.Adapter()}, ingest.Options{Kind: model.ScanSynthetic, Now: ds.Scenario.End}); err != nil {
		t.Fatal(err)
	}
	return s
}

// inputOf hands a data set to the engine directly, with every location
// field, without going through a ledger. The records carry the DIMM
// identities the generator knows.
func inputOf(ds *synth.Dataset) *Input {
	in := NewInput()
	in.DIMMs = append(in.DIMMs, ds.Modules...)
	for i := range ds.Events {
		e := ds.Events[i]
		e.ID = int64(i + 1)
		e.DIMMLink = model.LinkObserved
		in.Add(&e)
	}
	return in
}

// platformInput hands a data set to the engine with the location fields a
// platform reports, as the input adapters produce them for that platform
// (TestPlatformViewsMatchTheAdapters compares the two):
//
//   - ghes: every field, the failing bit as a bit position.
//   - intel: no device and no failing bits.
//   - amd: the physical address and the rank, nothing else.
func platformInput(ds *synth.Dataset, fl synth.Flavor) *Input {
	in := NewInput()
	in.DIMMs = append(in.DIMMs, ds.Modules...)
	for i := range ds.Events {
		e := ds.Events[i]
		e.ID = int64(i + 1)
		e.DIMMLink = model.LinkObserved
		none := model.Opt[int]{}
		switch fl {
		case synth.FlavorIntel:
			e.DeviceId, e.ErrorVector = none, ""
		case synth.FlavorAMD:
			e.DeviceId, e.BankGroup, e.Bank, e.Row, e.Column, e.ErrorVector = none, none, none, none, none, ""
		}
		e.Grade = e.ComputeGrade()
		in.Add(&e)
	}
	return in
}

func load(t testing.TB, s *store.Store) *Input {
	t.Helper()
	in, err := Load(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

// uidOf returns the identity of the synthetic DIMM with the given serial.
func uidOf(serial string) string {
	return model.DIMMUID("Example Memory", "EXM64G48R2X4-A", serial)
}

func faultsOf(c *Classification, uid string) []*Fault {
	for _, d := range c.DIMMs {
		if d.UID == uid {
			return d.Faults
		}
	}
	return nil
}

func describe(fs []*Fault) string {
	out := ""
	for _, f := range fs {
		out += fmt.Sprintf("\n  %s", faultString(f))
	}
	return out
}

func faultString(f *Fault) string {
	s := fmt.Sprintf("%-12s grade %s errors %d (ce %d ue %d) cells %d rows %d cols %d banks %d devs %d ranks %d",
		f.Mode, f.Grade, f.ErrorCount, f.CE, f.UE, f.Shape.Cells, f.Shape.Rows, f.Shape.Columns, f.Shape.Banks, f.Shape.Devices, f.Shape.Ranks)
	if f.Estimated {
		s += " estimated(" + f.EstimatedBy + ")"
	}
	if f.Recurring {
		s += " recurring"
	}
	s += fmt.Sprintf(" rank %s dev %s bg %s bank %s row %s..%s col %s..%s conf %d", f.Rank, f.DeviceId, f.BankGroup, f.Bank, f.RowMin, f.RowMax, f.ColumnMin, f.ColumnMax, f.Confidence)
	return s
}

// builder makes Inputs by hand for tests that need exact control over
// locations and times.
type builder struct {
	in *Input
	n  int64
}

func newBuilder() *builder { return &builder{in: NewInput()} }

// loc is a location inside a DIMM; negative fields are not reported.
type loc struct {
	rank, dev, bg, bank, row, col int
}

func opt(v int) model.Opt[int] {
	if v < 0 {
		return model.Opt[int]{}
	}
	return model.Some(v)
}

// event adds one corrected error of DIMM uid at l and returns it for
// further changes before it is added: call add.
func (b *builder) event(uid string, at time.Time, l loc) *model.Event {
	b.n++
	return &model.Event{
		ID: b.n, Host: "h1", Kind: model.KindError, ErrorDT: at, ErrorSeverity: model.SevCE,
		Rank: opt(l.rank), DeviceId: opt(l.dev), BankGroup: opt(l.bg), Bank: opt(l.bank), Row: opt(l.row), Column: opt(l.col),
		Source: "test", RawRef: fmt.Sprintf("test#%d", b.n), Count: 1, TimeQuality: model.TimeExact,
		DIMMUID: uid, DIMMLink: model.LinkObserved, Slot: "S-" + uid, Fingerprint: fmt.Sprint(b.n),
	}
}

func (b *builder) add(e *model.Event) {
	e.Grade = e.ComputeGrade()
	b.in.Add(e)
}

// ce adds one corrected error.
func (b *builder) ce(uid string, at time.Time, l loc) {
	b.add(b.event(uid, at, l))
}

func (b *builder) classify(opts Options) *Classification { return Classify(b.in, opts) }

// modes returns the modes of faults in their order.
func modes(fs []*Fault) []model.FaultMode {
	out := make([]model.FaultMode, len(fs))
	for i, f := range fs {
		out[i] = f.Mode
	}
	return out
}

// TestLoad reads a ledger into an Input: the records that count, and the
// context records the later steps need.
func TestLoad(t *testing.T) {
	in := load(t, sharedLedger(t, "demo", 1, synth.FlavorIntel))
	// The kernel log repeats the errors of the last boot and the counters
	// cover them once more: 2082 error records and 3 counter records are in
	// the ledger, 1285 of them count.
	if in.Events() != 1285 {
		t.Errorf("%d records loaded, want 1285", in.Events())
	}
	if len(in.Hosts) != 1 || in.Hosts[0].Name != "r12-n034" || len(in.DIMMs) != 16 || len(in.Slots) != 32 ||
		len(in.Placements) != 16 || len(in.Boots) != 1 || len(in.Scans) != 1 || len(in.Sources) != 6 || len(in.Actions) != 8 {
		t.Errorf("hosts %d, DIMMs %d, slots %d, placements %d, boots %d, scans %d, sources %d, actions %d",
			len(in.Hosts), len(in.DIMMs), len(in.Slots), len(in.Placements), len(in.Boots), len(in.Scans), len(in.Sources), len(in.Actions))
	}
	// A ledger that cannot be read is an error, not an empty result.
	s2 := newStore(t)
	s2.Close()
	if _, err := Load(ctx, s2); err == nil {
		t.Error("Load on a closed ledger returned no error")
	}
}

func TestParseVector(t *testing.T) {
	tests := []struct {
		in   string
		kind uint8
		v    uint32
	}{
		{"dq:0xc", vecDQ, 0xc}, {"dq:0x1", vecDQ, 1}, {"bit:69", vecBit, 69}, {"bit:0", vecBit, 0},
		{"nibble:0x30", vecNibble, 0x30},
		{"", vecNone, 0}, {"dq:0x0", vecNone, 0}, {"dq:zz", vecNone, 0}, {"bit:-1", vecNone, 0}, {"bit:x", vecNone, 0},
		{"syndrome:0x12", vecNone, 0}, {"0x12", vecNone, 0},
	}
	for _, tt := range tests {
		if k, v := parseVector(tt.in); k != tt.kind || v != tt.v {
			t.Errorf("parseVector(%q) = %d, %#x; want %d, %#x", tt.in, k, v, tt.kind, tt.v)
		}
	}
}

// TestOptionsDefaults: a nil configuration means the defaults, and a given
// one is not changed by the engine.
func TestOptionsDefaults(t *testing.T) {
	if got := (Options{}).config(); *got != DefaultConfig() {
		t.Errorf("default config = %+v", *got)
	}
	cfg := DefaultConfig()
	cfg.ScatterMinCells = 3
	o := Options{Config: &cfg}
	c := o.config()
	c.ScatterMinCells = 99
	if cfg.ScatterMinCells != 3 {
		t.Error("the caller's configuration was changed")
	}
	if _, ok := (Options{}).adjacency().(LogicalAdjacency); !ok {
		t.Error("default adjacency is not logical")
	}
}
