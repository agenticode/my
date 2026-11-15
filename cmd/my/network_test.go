package main

import (
	"errors"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/testutil"
)

// dialled is a transport that counts every request it is handed and
// answers none.
type dialled struct{ n atomic.Int32 }

func (d *dialled) RoundTrip(*http.Request) (*http.Response, error) {
	d.n.Add(1)
	return nil, errors.New("this test allows no network connection")
}

// The commands that do not name a BMC ask no BMC and nothing else on the
// network: with a transport that fails when it is used in place of every
// HTTP path of the binary, they run as before.
func TestCommandsWithoutABMCOpenNoConnection(t *testing.T) {
	guard := &dialled{}
	savedBMC, savedDefault := bmcTransport, http.DefaultTransport
	bmcTransport, http.DefaultTransport = guard, guard
	defer func() { bmcTransport, http.DefaultTransport = savedBMC, savedDefault }()

	root := hostTree(t)
	ledger := demo(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "case.mycase")
	for _, args := range [][]string{
		{"--root", root},
		{"scan", "--root", root, "--json"},
		{"dimm", "CPU0_C1", "--root", root},
		{"why", "--root", root},
		{"map", "CPU0_C1", "--root", root},
		{"timeline", "--root", root},
		{"gpu", "--root", root},
		{"bundle", "-o", file, "--root", root},
		{"open", file},
		{"open", file, "why"},
		{"open", ledger, "dimm", "CPU0_C1"},
		{"report", file, "-o", filepath.Join(dir, "report.html")},
		{"export", "fmfm", "--root", root},
		{"import", root, "-o", filepath.Join(dir, "imported.mycase")},
		{"import", testutil.Testdata(t, "redfish", "dmtf-rackmount1")},
		{"collect", "support", filepath.Join(dir, "none")},
		{"version"},
		{"help"},
		{"help", "collect"},
	} {
		code, _, stderr := runEnv(fixed, args...)
		if code == cli.ExitUsage {
			t.Errorf("%v: usage error: %s", args, stderr)
		}
		if n := guard.n.Load(); n != 0 {
			t.Fatalf("%v sent %d requests", args, n)
		}
	}

	// The guard is in the path of a command that names a BMC: the same
	// transport does see that one.
	code, _, _ := runEnv(fixed, "collect", "redfish", "--bmc", "https://192.0.2.1", "-o", filepath.Join(dir, "bmc"), "--retries", "0", "--quiet")
	if code != cli.ExitFailure || guard.n.Load() == 0 {
		t.Errorf("collect with the guard: exit code %d, %d requests", code, guard.n.Load())
	}
}

// Only a few packages may open a connection: the BMC client, the server of
// "my serve", and the reader of a fleet's rasdaemon store. A package that
// starts to use the network shows up here, so that it is a decision.
func TestOnlyKnownPackagesUseTheNetwork(t *testing.T) {
	allowed := map[string]bool{
		"internal/redfish/client":    true, // asks a BMC, when one is named
		"internal/redfish/collect":   true, // names the status codes of answers
		"internal/redfish/bmctest":   true, // stand-in BMC of the tests
		"internal/serve":             true, // "my serve" listens
		"internal/adapter/rasdaemon": true, // --fleet reads a database server
		"cmd/my":                     true, // wires the three above
	}
	network := map[string]bool{"net": true, "net/http": true, "crypto/tls": true, "net/rpc": true, "net/smtp": true,
		"github.com/go-sql-driver/mysql": true, "github.com/lib/pq": true}
	root := testutil.RepoRoot(t)
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); strings.HasPrefix(name, ".") || name == "testdata" || name == "examples" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, filepath.Dir(path))
		rel = filepath.ToSlash(rel)
		for _, imp := range f.Imports {
			name, _ := strconv.Unquote(imp.Path.Value)
			if !network[name] {
				continue
			}
			ok := allowed[rel]
			for dir := range allowed {
				if strings.HasPrefix(rel, dir+"/") {
					ok = true
				}
			}
			if !ok {
				t.Errorf("%s imports %s: package %s is not among those that may use the network", filepath.ToSlash(strings.TrimPrefix(path, root+string(os.PathSeparator))), name, rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
