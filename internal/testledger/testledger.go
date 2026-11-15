// Package testledger builds ledgers of the synthetic scenarios for tests.
//
// Building a ledger through the input adapters takes seconds under the
// race detector, so a ledger is built once per test binary and shared.
// A test package that uses it hands its TestMain to Main, which removes
// the files afterwards:
//
//	func TestMain(m *testing.M) { testledger.Main(m) }
//
// Tests must not write to a shared ledger.
package testledger

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

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

var shared struct {
	sync.Mutex
	dir     string
	ledgers map[string]*store.Store
	paths   map[string]string
}

// Main runs the tests of a package and removes the shared ledgers.
func Main(m *testing.M) {
	code := m.Run()
	shared.Lock()
	for _, s := range shared.ledgers {
		s.Close()
	}
	if shared.dir != "" {
		os.RemoveAll(shared.dir)
	}
	shared.Unlock()
	os.Exit(code)
}

// Generate runs a built-in scenario of the synthetic generator. mut, when
// not nil, changes the scenario first.
func Generate(tb testing.TB, name string, seed uint64, mut func(*synth.Scenario)) *synth.Dataset {
	tb.Helper()
	sc, err := synth.Named(name, seed)
	if err != nil {
		tb.Fatal(err)
	}
	if mut != nil {
		mut(sc)
	}
	ds, err := synth.Generate(sc)
	if err != nil {
		tb.Fatalf("generate %s: %v", name, err)
	}
	return ds
}

// build creates the ledger of a scenario. With an empty flavor the data
// set is stored directly, with every location field. Otherwise the files a
// host of that platform would hold are written and read back through the
// input adapters.
func build(ctx context.Context, ds *synth.Dataset, fl synth.Flavor, dir string) (string, error) {
	path := filepath.Join(dir, "ledger.db")
	s, err := store.Open(ctx, path, store.Create)
	if err != nil {
		return "", err
	}
	at := ds.Scenario.End
	if fl == "" {
		_, err = ingest.Run(ctx, s, []adapter.Adapter{ds.Adapter()}, ingest.Options{Kind: model.ScanSynthetic, Now: at})
	} else {
		root, tmp := filepath.Join(dir, "root"), filepath.Join(dir, "tmp")
		if err = os.MkdirAll(tmp, 0o755); err == nil {
			err = ds.WriteHostTree(root, fl)
		}
		if err == nil {
			hi := hostinfo.NewRoot(root, hostinfo.Options{Now: at})
			info, _ := hi.Read()
			_, err = ingest.Run(ctx, s, []adapter.Adapter{
				hi,
				rasdaemon.New(filepath.Join(root, "var", "lib", "rasdaemon", "ras-mc_event.db"), rasdaemon.Options{TempDir: tmp}),
				klog.NewFile(filepath.Join(root, "var", "log", "dmesg"), klog.Options{BootTime: info.BootTime, BootID: info.BootID}),
				edacsysfs.NewRoot(root, edacsysfs.Options{Now: at}),
				smbios.NewRoot(root, smbios.Options{Now: at}),
			}, ingest.Options{Kind: model.ScanLive, Now: at, Privileged: true})
		}
	}
	if cerr := s.Close(); err == nil {
		err = cerr
	}
	return path, err
}

// Path returns the file of the ledger of a built-in scenario. With an
// empty flavor the ledger holds every location field of every error; with
// a flavor it holds what a host of that platform records.
func Path(tb testing.TB, scenario string, seed uint64, fl synth.Flavor) string {
	tb.Helper()
	shared.Lock()
	defer shared.Unlock()
	key := fmt.Sprintf("%s-%d-%s", scenario, seed, fl)
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
	p, err := build(context.Background(), Generate(tb, scenario, seed, nil), fl, dir)
	if err != nil {
		tb.Fatalf("ledger of %s: %v", key, err)
	}
	shared.paths[key] = p
	return p
}

// Open returns the ledger of a built-in scenario, opened read-only and
// shared by the tests of the binary.
func Open(tb testing.TB, scenario string, seed uint64, fl synth.Flavor) *store.Store {
	tb.Helper()
	p := Path(tb, scenario, seed, fl)
	shared.Lock()
	defer shared.Unlock()
	key := fmt.Sprintf("%s-%d-%s", scenario, seed, fl)
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
