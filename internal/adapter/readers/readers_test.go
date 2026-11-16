package readers

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/testutil"
)

// TestEveryReaderIsListed: every package below internal/adapter that
// holds a reader has a line in List, and every line names a fixture that
// exists.
func TestEveryReaderIsListed(t *testing.T) {
	root := testutil.RepoRoot(t)
	listed := map[string]bool{}
	names := map[string]bool{}
	for _, r := range List() {
		listed[r.Package] = true
		if names[r.Name] {
			t.Errorf("reader %s is listed twice", r.Name)
		}
		names[r.Name] = true
		if _, err := os.Stat(filepath.Join(root, r.Fixture)); err != nil {
			t.Errorf("reader %s: fixture %s: %v", r.Name, r.Fixture, err)
		}
		if r.Main != "" {
			if _, err := os.Stat(filepath.Join(root, r.Fixture, r.Main)); err != nil {
				t.Errorf("reader %s: %v", r.Name, err)
			}
		}
	}
	dirs, err := os.ReadDir(filepath.Join(root, "internal", "adapter"))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs {
		if !d.IsDir() || d.Name() == "readers" || d.Name() == "testdata" {
			continue
		}
		files, _ := filepath.Glob(filepath.Join(root, "internal", "adapter", d.Name(), "*.go"))
		reader := false
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			reader = reader || bytes.Contains(b, []byte(") Collect(ctx context.Context, sink adapter.Sink) adapter.Report"))
		}
		if reader && !listed[d.Name()] {
			t.Errorf("the reader of internal/adapter/%s is not listed in readers.List", d.Name())
		}
	}
}

// fixtureBytes returns the input of a reader as it reads it: the fixture
// file, a database built from a script, or the binary file a hex dump
// describes.
func fixtureBytes(t testing.TB, r Reader) []byte {
	root := testutil.RepoRoot(t)
	p := filepath.Join(root, r.Fixture)
	if r.Main != "" {
		p = filepath.Join(p, r.Main)
	}
	switch {
	case strings.HasSuffix(p, ".sql"):
		p = testutil.BuildSQLite(t, p, "fixture.db")
	case strings.HasSuffix(p, ".hex"):
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var out []byte
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.Join(strings.Fields(line), "")
			if line == "" || line[0] == '#' {
				continue
			}
			rec, err := hex.DecodeString(line)
			if err != nil {
				t.Fatalf("%s: %v", r.Fixture, err)
			}
			out = append(out, rec...)
		}
		return out
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// garbage is one input that is no valid input of a reader.
type garbage struct {
	name string
	data []byte
}

// garbageFor returns the ten kinds of garbage of the design for a reader
// whose valid input is fixture.
func garbageFor(fixture []byte) []garbage {
	rnd := rand.New(rand.NewSource(1))
	random := make([]byte, 4<<10)
	rnd.Read(random)
	out := []garbage{
		{"empty", nil},
		{"one byte", []byte("x")},
		{"4 KiB of random bytes", random},
	}
	for k := 1; k < 16; k++ {
		out = append(out, garbage{fmt.Sprintf("fixture cut at %d/16", k), fixture[:len(fixture)*k/16]})
	}
	if len(fixture) > 0 {
		for k := range 64 {
			b := bytes.Clone(fixture)
			i := rnd.Intn(len(b))
			b[i] ^= byte(1 << uint(k%8))
			out = append(out, garbage{fmt.Sprintf("fixture with byte %d flipped", i), b})
		}
	}
	invalid := bytes.Clone(fixture)
	for i := 0; i < len(invalid); i += 7 {
		invalid[i] = 0xff
	}
	out = append(out,
		garbage{"8 MiB of one byte", bytes.Repeat([]byte("a"), 8<<20)},
		garbage{"JSON arrays 10000 deep", []byte(strings.Repeat("[", 10000) + strings.Repeat("]", 10000))},
		garbage{"JSON objects 10000 deep", []byte(strings.Repeat(`{"a":`, 10000) + "1" + strings.Repeat("}", 10000))},
		garbage{"XML 10000 deep", []byte(`<?xml version="1.0"?>` + strings.Repeat("<a>", 10000) + strings.Repeat("</a>", 10000))},
		garbage{"invalid UTF-8", append([]byte("\xff\xfe\xc3\x28\xe2\x82"), invalid...)},
		garbage{"NUL bytes", make([]byte, 4<<10)},
		garbage{"one line of 2 MiB", append(bytes.Repeat([]byte("x"), 2<<20), '\n')},
	)
	return out
}

// place writes data where the reader reads it, below dir, and returns the
// path to open.
func place(t testing.TB, r Reader, dir string, data []byte) string {
	if r.Main == "" {
		p := filepath.Join(dir, "input")
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	tree := filepath.Join(dir, "tree")
	if err := CopyTree(filepath.Join(testutil.RepoRoot(t), r.Fixture), tree); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, r.Main), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return tree
}

// Limits of one reading of one input.
const (
	garbageTime = 20 * time.Second
	// garbageAlloc is what a reader may allocate beyond 32 times its input.
	garbageAlloc = 256 << 20
)

// TestGarbage feeds every reader the ten kinds of garbage. Each reading
// returns a report, within the time and allocation limits, and does not
// panic. The guards are not used here, so a panic fails the test.
func TestGarbage(t *testing.T) {
	if testing.Short() {
		t.Skip("reads every reader about 90 times")
	}
	for _, r := range List() {
		t.Run(r.Name, func(t *testing.T) {
			fixture := fixtureBytes(t, r)
			statuses := map[string]int{}
			defer func() {
				// The garbage reached the reader: some of it was not read
				// as a valid input.
				// Host facts that cannot be read are left out: the facts
				// are what a host says about itself, and none is needed.
				if statuses["ok"] == len(garbageFor(fixture)) && r.Name != "hostinfo" {
					t.Errorf("every garbage input was read as ok: %v", statuses)
				}
			}()
			for _, g := range garbageFor(fixture) {
				dir := t.TempDir()
				path := place(t, r, dir, g.data)
				a := r.Open(path)
				var before, after runtime.MemStats
				runtime.GC()
				runtime.ReadMemStats(&before)
				ctx, cancel := context.WithTimeout(context.Background(), 2*garbageTime)
				start := time.Now()
				rep, panicked := collect(ctx, a)
				took := time.Since(start)
				cancel()
				runtime.ReadMemStats(&after)
				if panicked != "" {
					t.Errorf("%s: panic: %s", g.name, panicked)
					continue
				}
				if rep.Status == "" {
					t.Errorf("%s: the report has no status", g.name)
				}
				statuses[string(rep.Status)]++
				if took > garbageTime {
					t.Errorf("%s: took %v", g.name, took)
				}
				if alloc := after.TotalAlloc - before.TotalAlloc; alloc > uint64(32*len(g.data)+garbageAlloc) {
					t.Errorf("%s: allocated %d MiB for %d bytes", g.name, alloc>>20, len(g.data))
				}
			}
		})
	}
}

// collect reads a reader into a sink that keeps nothing, and turns a panic
// into its text.
func collect(ctx context.Context, a adapter.Adapter) (rep adapter.Report, panicked string) {
	defer func() {
		if v := recover(); v != nil {
			buf := make([]byte, 8<<10)
			panicked = fmt.Sprintf("%v\n%s", v, buf[:runtime.Stack(buf, false)])
		}
	}()
	return a.Collect(ctx, discard{}), ""
}

// TestFixturesAreRead: the fixture of every reader is read without fault,
// so that the garbage above is garbage of a form the reader knows.
func TestFixturesAreRead(t *testing.T) {
	for _, r := range List() {
		path := place(t, r, t.TempDir(), fixtureBytes(t, r))
		rep, panicked := collect(context.Background(), r.Open(path))
		if panicked != "" || rep.Status == "error" || rep.Records == 0 {
			t.Errorf("%s: status %s, %d records, %s %s", r.Name, rep.Status, rep.Records, rep.Detail, panicked)
		}
	}
}
