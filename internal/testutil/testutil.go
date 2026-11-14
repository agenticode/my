// Package testutil holds helpers shared by the tests of several packages.
package testutil

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite" // the driver the fixtures are built with
)

// RepoRoot returns the repository root: the nearest parent directory that
// holds go.mod.
func RepoRoot(tb testing.TB) string {
	tb.Helper()
	dir, err := os.Getwd()
	if err != nil {
		tb.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			tb.Fatal("go.mod not found above the working directory")
		}
		dir = parent
	}
}

// Testdata returns the path of a file under the repository's testdata
// directory.
func Testdata(tb testing.TB, elem ...string) string {
	tb.Helper()
	return filepath.Join(append([]string{RepoRoot(tb), "testdata"}, elem...)...)
}

// ReadTestdata returns the content of a file under testdata.
func ReadTestdata(tb testing.TB, elem ...string) []byte {
	tb.Helper()
	b, err := os.ReadFile(Testdata(tb, elem...))
	if err != nil {
		tb.Fatal(err)
	}
	return b
}

// BuildSQLite creates a SQLite database in a temporary directory by running
// the SQL script at scriptPath, and returns the path of the database.
// Database fixtures are kept as SQL text so that they can be reviewed.
func BuildSQLite(tb testing.TB, scriptPath, name string) string {
	tb.Helper()
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		tb.Fatal(err)
	}
	path := filepath.Join(tb.TempDir(), name)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		tb.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(string(script)); err != nil {
		tb.Fatalf("build %s from %s: %v", name, scriptPath, err)
	}
	return path
}
