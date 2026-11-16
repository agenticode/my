package readers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/scan"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/testutil"
)

// notImported are the readers the import half of
// TestGarbageLeavesTheRestAlone leaves out, with the reason.
var notImported = map[string]string{
	"bert":          "a saved region is only taken for one when it holds an error status block",
	"dcbrain":       "the dataset is read by my eval only",
	"edac-sysfs":    "read from a copy of a host's files only",
	"hostinfo":      "read from a copy of a host's files only",
	"smbios/tables": "read from a copy of a host's files only",
	"redfish/tree":  "the reader of redfish/file, given a directory",
	"sel/binary":    "the reader of sel/text and sel/json; a file is only taken for one of 16-byte records when they are records",
	"nvidia/csv":    "the reader of nvidia/q",
}

// outcome is what the tests compare: the records of the good sources and
// the grades.
type outcome struct {
	rows   []string
	grades []string
	host   model.VerdictGrade
}

// readAll reads the inputs into a new ledger the way "my import" does, or
// the host tree at root the way a scan of a copy does, and returns the
// records whose reference does not start with one of skip.
func readAll(t *testing.T, adapters []adapter.Adapter, skip ...string) (outcome, []model.Source) {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	res, err := ingest.Run(ctx, s, adapters, ingest.Options{Host: "h1", Now: When})
	if err != nil {
		t.Fatalf("the run failed: %v", err)
	}
	keep := func(ref string) bool {
		for _, p := range skip {
			if strings.HasPrefix(ref, p) {
				return false
			}
		}
		return true
	}
	var o outcome
	evs, err := s.EventList(ctx, store.EventFilter{Host: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if keep(e.RawRef) {
			o.rows = append(o.rows, fmt.Sprintf("event %s %s %s %d %d", e.Fingerprint, e.Slot, e.ErrorSeverity, e.Count, e.DupOf))
		}
	}
	acts, _ := s.Actions(ctx, store.ActionFilter{Host: "h1", Duplicates: true})
	for _, a := range acts {
		if keep(a.RawRef) {
			o.rows = append(o.rows, fmt.Sprintf("action %s %s %s", a.Fingerprint, a.Slot, a.Kind))
		}
	}
	fws, _ := s.FirmwareRecords(ctx, store.FirmwareFilter{Host: "h1"})
	for _, f := range fws {
		if keep(f.RawRef) {
			o.rows = append(o.rows, fmt.Sprintf("fw %s %s", f.Common, f.RawRef))
		}
	}
	r, err := engine.Run(ctx, s, engine.Options{Now: When})
	if err != nil {
		t.Fatal(err)
	}
	if h := r.Host("h1"); h != nil {
		o.host = h.Grade
		for _, d := range h.DIMMs {
			if d.Verdict != nil {
				o.grades = append(o.grades, d.Slot+" "+string(d.Verdict.Grade))
			}
		}
	}
	sort.Strings(o.rows)
	sort.Strings(o.grades)
	return o, res.Sources
}

func same(t *testing.T, what string, got, want outcome) {
	t.Helper()
	if strings.Join(got.rows, "\n") != strings.Join(want.rows, "\n") {
		t.Errorf("%s: the records of the good sources differ:\n%s\nwant\n%s", what, strings.Join(got.rows, "\n"), strings.Join(want.rows, "\n"))
	}
	if strings.Join(got.grades, ", ") != strings.Join(want.grades, ", ") || got.host != want.host {
		t.Errorf("%s: grades %v (host %s), want %v (host %s)", what, got.grades, got.host, want.grades, want.host)
	}
	if got.host.ExitCode() != want.host.ExitCode() {
		t.Errorf("%s: exit code %d, want %d", what, got.host.ExitCode(), want.host.ExitCode())
	}
}

// detect recognises the inputs at paths as "my import" does.
func detect(t *testing.T, paths ...string) []adapter.Adapter {
	t.Helper()
	var out []adapter.Adapter
	for _, p := range paths {
		as, _, err := scan.Detect(context.Background(), p, scan.DetectOptions{Now: When})
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		out = append(out, as...)
	}
	return out
}

// TestGarbageLeavesTheRestAlone: an import of good inputs, with and
// without one input that a reader takes for its own and that holds
// garbage, and a scan of a copy of a host's files with and without garbage
// at the path a reader reads. The records of the good sources, the grades
// and the exit code are the same. Only the source rows may differ.
func TestGarbageLeavesTheRestAlone(t *testing.T) {
	if testing.Short() {
		t.Skip("imports and scans once per reader")
	}
	root := testutil.RepoRoot(t)
	good := []string{
		filepath.Join(root, "testdata/real/7.3.0-mce/dmesg"),
		filepath.Join(root, "testdata/real/7.3.0-mce/dmidecode.txt"),
		filepath.Join(root, "testdata/sel/ipmitool-elist.txt"),
	}
	want, _ := readAll(t, detect(t, good...))
	if len(want.rows) == 0 || len(want.grades) == 0 {
		t.Fatalf("the good inputs give no records or no findings: %+v", want)
	}

	tree := filepath.Join(t.TempDir(), "host")
	if err := CopyTree(filepath.Join(root, "testdata/real/7.3.0-mce"), tree); err != nil {
		t.Fatal(err)
	}
	scanOf := func(dir string) []adapter.Adapter {
		return scan.Sources(context.Background(), scan.Options{Root: dir, TempDir: t.TempDir()})
	}

	for _, r := range List() {
		t.Run(r.Name, func(t *testing.T) {
			if why, ok := notImported[r.Name]; ok {
				if r.HostPath == "" {
					t.Logf("not imported: %s", why)
				}
			} else {
				if r.Bad == nil {
					t.Fatalf("no bad input to import, and no reason in notImported")
				}
				name := r.BadName
				if name == "" {
					name = "garbage"
				}
				bad := filepath.Join(t.TempDir(), name)
				if err := os.WriteFile(bad, r.Bad, 0o644); err != nil {
					t.Fatal(err)
				}
				as := detect(t, bad)
				if len(as) != 1 || as[0].Kind() != r.Open(bad).Kind() {
					t.Fatalf("the bad input is read by %v, not by %s", kinds(as), r.Name)
				}
				got, srcs := readAll(t, append(detect(t, good...), as...), bad)
				same(t, "import", got, want)
				for _, src := range srcs {
					// A kdump log stands for a panic of the host whatever it
					// holds: such a record is no error and moves no grade.
					if src.Origin == bad && src.Events-src.Informational+src.Actions > 0 {
						t.Errorf("the bad input gave %d error records and %d actions", src.Events-src.Informational, src.Actions)
					}
				}
			}
			if r.HostPath == "" {
				return
			}
			// The garbage is compared with no file at that path: what the
			// file held before is no longer read either way.
			clean := filepath.Join(t.TempDir(), "host")
			if err := CopyTree(tree, clean); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(clean, filepath.FromSlash(r.HostPath))); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			wantTree, _ := readAll(t, scanOf(clean))
			for i, row := range wantTree.rows {
				wantTree.rows[i] = strings.ReplaceAll(row, clean, tree)
			}
			dir := filepath.Join(t.TempDir(), "host")
			if err := CopyTree(tree, dir); err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(dir, filepath.FromSlash(r.HostPath))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(garbageText), 0o644); err != nil {
				t.Fatal(err)
			}
			got, _ := readAll(t, scanOf(dir), p, filepath.Dir(p))
			// The records of a host tree name the copy they were read from.
			for i, row := range got.rows {
				got.rows[i] = strings.ReplaceAll(row, dir, tree)
			}
			same(t, "host tree", got, wantTree)
		})
	}
}

func kinds(as []adapter.Adapter) []string {
	var out []string
	for _, a := range as {
		out = append(out, a.Kind())
	}
	return out
}
