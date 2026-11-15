package scan

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/testutil"
)

// FuzzDetect is "my import FILE" for a file of arbitrary bytes: the file
// is recognised by its content and read by the adapter that was chosen,
// plain or compressed. A file is taken for one of the inputs or refused
// with a reason; the adapter that reads it returns a report; and no record
// it hands on lies outside the times the ledger stores.
func FuzzDetect(f *testing.F) {
	root := testutil.Testdata(f)
	n := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "real/") || strings.HasPrefix(rel, "dcbrain/") || strings.HasPrefix(rel, "edac-sysfs/") ||
			strings.HasPrefix(rel, "redfish/dmtf-") || strings.HasSuffix(rel, ".md") {
			return nil
		}
		if fi, err := d.Info(); err != nil || fi.Size() > 48<<10 {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		n++
		f.Add(b, n%4 == 0)
		return nil
	})
	if err != nil || n < 20 {
		f.Fatalf("%d fixtures as seeds, %v", n, err)
	}
	for _, name := range []string{"intel-skx-0.6.sql", "cxl-1.0.sql"} {
		db, err := os.ReadFile(testutil.BuildSQLite(f, testutil.Testdata(f, "rasdaemon", name), "seed.db"))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(db, false)
	}
	f.Add([]byte("\x1f\x8b\x08\x00"), false)
	f.Add([]byte("SQLite format 3\x00"), false)
	f.Add([]byte("PK\x03\x04"), true)
	f.Add([]byte(`{"MESSAGE":"EDAC MC0: 1 CE x on y (page:0x1 offset:0x0 grain:1 syndrome:0x0)","_TRANSPORT":"kernel","__REALTIME_TIMESTAMP":"1"}`), true)
	f.Add(bytes.Repeat([]byte{0}, 300<<10), true)

	dir := f.TempDir()
	at := time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, data []byte, zipped bool) {
		path := filepath.Join(dir, "input")
		content := data
		if zipped {
			var b bytes.Buffer
			zw := gzip.NewWriter(&b)
			zw.Write(data)
			zw.Close()
			content = b.Bytes()
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		adapters, what, err := Detect(ctx, path, DetectOptions{Now: at, CapturedAt: at, TempDir: dir, NvidiaQuery: "uuid,index"})
		if err != nil {
			if len(adapters) != 0 || what != "" {
				t.Errorf("%d adapters for %q and an error: %v", len(adapters), what, err)
			}
			if !errors.Is(err, ErrUnknownInput) && !strings.Contains(err.Error(), "gzip") {
				t.Errorf("a file that can be read was refused with: %v", err)
			}
			if len(err.Error()) > 1000 {
				t.Errorf("a reason of %d bytes", len(err.Error()))
			}
			return
		}
		if len(adapters) != 1 || what == "" {
			t.Fatalf("%d adapters for %q", len(adapters), what)
		}
		a := adapters[0]
		var c adapter.Collected
		rep := a.Collect(ctx, &c)
		if rep.Status == "" || rep.Kind != a.Kind() || rep.SinkErr != nil {
			t.Fatalf("%s: report %+v", what, rep)
		}
		if rep.Skipped > rep.Records {
			t.Errorf("%s: %d of %d records skipped", what, rep.Skipped, rep.Records)
		}
		okTime := func(at time.Time) bool { y := at.UTC().Year(); return !at.IsZero() && y >= 1970 && y <= 9999 }
		for _, e := range c.Events {
			if !okTime(e.ErrorDT) || e.Fingerprint == "" || e.Source == "" || !e.ErrorSeverity.Valid() || e.Count < 1 {
				t.Errorf("%s: event the ledger would refuse: %+v", what, e)
			}
		}
		for _, x := range c.Actions {
			if !okTime(x.At) || x.Fingerprint == "" || x.Source == "" || !x.Kind.Valid() {
				t.Errorf("%s: action the ledger would refuse: %+v", what, x)
			}
		}
		for _, b := range c.Boots {
			if !okTime(b.StartedAt) {
				t.Errorf("%s: boot the ledger would refuse: %+v", what, b)
			}
		}
		for _, g := range c.GPUs {
			if !okTime(g.ObservedAt) || g.Fingerprint == "" {
				t.Errorf("%s: GPU record the ledger would refuse: %+v", what, g)
			}
		}
		for _, m := range c.Modules {
			if !okTime(m.ObservedAt) || m.DIMM.SizeBytes < 0 {
				t.Errorf("%s: module the ledger would refuse: %+v", what, m)
			}
		}
		for _, s := range c.ECSs {
			if !okTime(s.ObservedAt) {
				t.Errorf("%s: ECS reading the ledger would refuse: %+v", what, s)
			}
		}
		// Nothing but the input is left in the directory: a reader that
		// copies its source cleans up after itself.
		if entries, _ := os.ReadDir(dir); len(entries) != 1 {
			t.Errorf("%s: %d entries in the working directory after the read", what, len(entries))
		}
	})
}
