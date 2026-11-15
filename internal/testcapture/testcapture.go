// Package testcapture reads the captures of real hosts under testdata/real
// into ledgers, for tests of what is built on such a ledger.
package testcapture

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/scan"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/testutil"
)

// GHES is the capture of firmware error records with row and column: 24
// corrected and 2 uncorrected errors on one DIMM, reported by three
// sources each.
const GHES = "6.19.10-ghes"

// Dir returns the directory of a capture, relative to the directory of the
// test. The path becomes part of every record reference of the ledger, and
// through them of golden files: it must not name the machine of the test.
func Dir(tb testing.TB, name string) string {
	tb.Helper()
	abs := testutil.Testdata(tb, "real", name)
	wd, err := os.Getwd()
	if err != nil {
		tb.Fatal(err)
	}
	rel, err := filepath.Rel(wd, abs)
	if err != nil {
		tb.Fatal(err)
	}
	return rel
}

// PlanPath returns the injection plan of a capture.
func PlanPath(tb testing.TB, name string) string {
	tb.Helper()
	return filepath.Join(Dir(tb, name), "plan.yaml")
}

// Ledger reads a capture, as a copy of the host's files, into a new ledger
// and returns the file and the name of the host.
func Ledger(tb testing.TB, name string) (path, host string) {
	tb.Helper()
	ctx := context.Background()
	path = filepath.Join(tb.TempDir(), "ledger.db")
	s, err := store.Open(ctx, path, store.Create)
	if err != nil {
		tb.Fatal(err)
	}
	res, err := scan.Run(ctx, s, scan.Options{Root: Dir(tb, name), TempDir: tb.TempDir()})
	if cerr := s.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		tb.Fatalf("capture %s: %v", name, err)
	}
	return path, res.Host
}

var countOfRow3 = regexp.MustCompile(`(  - id: row-3\n(?:    .*\n)*?    count: )1`)

// MismatchedPlan writes a plan that differs from the GHES capture in the
// four ways a plan and the records can differ, and returns its path:
//
//   - row-2 names another row and column than was injected, as a decoding
//     bug would make the records differ from the plan;
//   - row-3 claims three errors where one was injected, so it takes the
//     records of the two later injections at its address (row-10, row-13),
//     which are then missing;
//   - ghost-1 was never injected;
//   - stay-1 is a page that must stay in use, and does.
func MismatchedPlan(tb testing.TB) string {
	tb.Helper()
	b, err := os.ReadFile(PlanPath(tb, GHES))
	if err != nil {
		tb.Fatal(err)
	}
	text := string(b)
	const was, now = "    row: 0x1a2f\n    column: 0x118\n", "    row: 0x1a30\n    column: 0x119\n"
	if !strings.Contains(text, was) || !countOfRow3.MatchString(text) {
		tb.Fatal("the plan of the GHES capture changed: MismatchedPlan no longer fits it")
	}
	text = strings.Replace(text, was, now, 1)
	text = countOfRow3.ReplaceAllString(text, "${1}3")
	text += `  - id: ghost-1
    time: 2026-11-15T11:20:00Z
    inject: error
    severity: UE
    count: 1
    address: 0x1ffff0000
    label: "DIMM 0"
    note: "never injected"
  - id: stay-1
    inject: page-offline
    how: hard
    count: 0
    address: 0x1ffff1000
`
	path := filepath.Join(tb.TempDir(), "plan-mismatched.yaml")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		tb.Fatal(err)
	}
	return path
}
