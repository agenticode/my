// Package testutil holds helpers shared by the tests of several packages.
package testutil

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
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

// UpdateGoldenEnv names the environment variable that makes Golden write
// the files it would otherwise compare with:
//
//	UPDATE_GOLDEN=1 go test ./internal/term/
const UpdateGoldenEnv = "UPDATE_GOLDEN"

// Golden compares got with the file testdata/<name> next to the test. With
// UPDATE_GOLDEN set it writes the file instead, so that a change of the
// output is reviewed as a diff of the golden files.
func Golden(tb testing.TB, name string, got []byte) {
	tb.Helper()
	path := filepath.Join("testdata", filepath.FromSlash(name))
	// Output that names the directory of this checkout is the same on no
	// other machine: a golden file made of it passes here and fails
	// everywhere else. Read fixtures under a name of their own instead.
	if root := RepoRoot(tb); bytes.Contains(got, []byte(root)) {
		tb.Fatalf("%s: the output holds the path of this checkout (%s); it must not depend on where the repository lies", path, root)
	}
	if os.Getenv(UpdateGoldenEnv) != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			tb.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("%v (run the test with %s=1 to write it)", err, UpdateGoldenEnv)
	}
	if string(want) == string(got) {
		return
	}
	// Point at the first line that differs: the whole output can be long.
	w, g := strings.Split(string(want), "\n"), strings.Split(string(got), "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			tb.Fatalf("%s differs at line %d:\n want %q\n got  %q\n(run the test with %s=1 after checking the change)", path, i+1, wl, gl, UpdateGoldenEnv)
		}
	}
}
