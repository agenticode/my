package bundle

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/agenticode/my/internal/store"
)

// archive writes a ZIP file with the given members, whole and with good
// checksums, so that what the reader meets is the content of the members.
func archive(tb testing.TB, path string, members [][2][]byte, deflate bool) {
	tb.Helper()
	f, err := os.Create(path)
	if err != nil {
		tb.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, m := range members {
		method := zip.Store
		if deflate {
			method = zip.Deflate
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: string(m[0]), Method: method})
		if err != nil {
			// A name the archive format does not take.
			continue
		}
		if _, err := w.Write(m[1]); err != nil {
			tb.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		tb.Fatal(err)
	}
	if err := f.Close(); err != nil {
		tb.Fatal(err)
	}
}

// membersOf returns the members of the bundle at path.
func membersOf(tb testing.TB, path string) map[string][]byte {
	tb.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		tb.Fatal(err)
	}
	defer zr.Close()
	out := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			tb.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			tb.Fatal(err)
		}
		out[f.Name] = b
	}
	return out
}

// FuzzMembers feeds the reader a well-formed archive whose members hold
// arbitrary bytes: the manifest, the source inventory and the ledger. A
// bundle is a file someone sent. Reading it ends with a case whose ledger
// this build reads, or with an error that leaves nothing behind.
// FuzzReadManifest feeds the reader arbitrary archives, most of which end
// at the checksums of the archive format; this target starts behind them.
func FuzzMembers(f *testing.F) {
	dir := f.TempDir()
	s, err := store.Open(ctx, filepath.Join(dir, "ledger.db"), store.Create)
	if err != nil {
		f.Fatal(err)
	}
	seed := filepath.Join(dir, "seed.mycase")
	if _, err := Write(ctx, s, seed, Options{Now: now, Tool: "my fuzz"}); err != nil {
		f.Fatal(err)
	}
	s.Close()
	m := membersOf(f, seed)
	manifest, sources, ledger := m[manifestName], m[sourcesName], m[ledgerName]
	if len(manifest) == 0 || len(ledger) == 0 {
		f.Fatal("the seed bundle has no manifest or no ledger")
	}
	// A manifest without the checksum of the ledger: a changed ledger is
	// then read as far as its own content allows.
	open := []byte(`{"format":"mycase","format_version":1,"ledger":{"file":"ledger.db"}}`)
	f.Add(manifest, sources, ledger, uint8(0))
	f.Add(open, sources, ledger, uint8(1))
	f.Add(open, []byte(`{"scans":[{"id":1,"host":"h","sources":[{"kind":"klog","status":"ok","notes":["a"]}]}]}`), ledger[:len(ledger)/2], uint8(0))
	f.Add(open, []byte(`{"scans":null}`), append(append([]byte{}, ledger[:100]...), make([]byte, 4096)...), uint8(2))
	f.Add([]byte(`{"format":"mycase","format_version":1,"ledger":{"file":"../../ledger.db"}}`), []byte(`[]`), ledger, uint8(0))
	f.Add([]byte(`{"format":"mycase","format_version":1,"ledger":{"file":"manifest.json","sha256":"00"}}`), []byte(``), []byte("SQLite format 3\x00"), uint8(4))
	f.Add([]byte(`{"format":"mycase","format_version":99}`), []byte(`{`), []byte{}, uint8(0))
	f.Add([]byte(`{"format":"mycase","format_version":1,"created_at":"x"}`), []byte(`{}`), []byte{}, uint8(8))

	work := f.TempDir()
	f.Fuzz(func(t *testing.T, manifest, sources, ledger []byte, shape uint8) {
		path := filepath.Join(work, "fuzz.mycase")
		members := [][2][]byte{{[]byte(manifestName), manifest}, {[]byte(sourcesName), sources}, {[]byte(ledgerName), ledger}, {[]byte(readmeName), []byte("x")}}
		switch {
		case shape&2 != 0:
			members = members[:2] // no ledger
		case shape&4 != 0:
			members = [][2][]byte{members[2], members[0]} // no source inventory
		case shape&8 != 0:
			members = append(members, members[2], members[0]) // members twice
		}
		archive(t, path, members, shape&1 != 0)

		man, err := ReadManifest(path)
		if err == nil && (man.Format != Format || man.FormatVersion < 1 || man.FormatVersion > Version) {
			t.Errorf("a manifest of format %q, version %d was accepted", man.Format, man.FormatVersion)
		}
		if src, err := ReadSources(path); err == nil && src == nil {
			t.Error("no source inventory and no error")
		}
		tmp := t.TempDir()
		c, err := Open(ctx, path, tmp)
		if err != nil {
			if c != nil {
				t.Error("a case and an error")
			}
			if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
				t.Errorf("a refused bundle left %d entries behind", len(entries))
			}
			return
		}
		defer c.Close()
		if man == nil {
			t.Fatal("a bundle was opened whose manifest cannot be read")
		}
		// What was taken out lies in the directory that was named, and is
		// the ledger member, whatever the manifest calls it.
		if rel, err := filepath.Rel(tmp, c.Ledger); err != nil || !filepath.IsLocal(rel) {
			t.Fatalf("the ledger was put at %s, outside %s", c.Ledger, tmp)
		}
		got, err := os.ReadFile(c.Ledger)
		if err != nil {
			t.Fatal(err)
		}
		if name := man.Ledger.File; (name == "" || name == ledgerName) && shape&8 == 0 && len(got) < len(ledger) {
			t.Errorf("the ledger taken out has %d bytes, the member %d", len(got), len(ledger))
		}
		// A case that opens holds a ledger this build reads. Its tables
		// may hold anything: reading them ends with records or an error.
		st, err := store.Open(ctx, c.Ledger, store.ReadOnly)
		if err != nil {
			t.Fatalf("the ledger of an opened case does not open: %v", err)
		}
		defer st.Close()
		hosts, _ := st.Hosts(ctx)
		for i, h := range hosts {
			if i == 4 {
				break
			}
			st.Boots(ctx, h.Name)
			st.Slots(ctx, h.Name)
			st.Faults(ctx, h.Name)
			st.Verdicts(ctx, h.Name)
			st.GPUMem(ctx, h.Name)
			st.LatestCounterSamples(ctx, h.Name)
		}
		st.DIMMs(ctx)
		st.Scans(ctx)
		st.Totals(ctx, store.EventFilter{})
		n := 0
		for _, err := range st.Events(ctx, store.EventFilter{WithRaw: true}) {
			if n++; err != nil || n > 1000 {
				break
			}
		}
		st.Actions(ctx, store.ActionFilter{})
	})
}
