package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/testutil"
)

// TestDataModelPage keeps docs/data-model.md in step with the schema: the
// page names the schema version of this binary, every table of a new
// ledger has its row, and no row names a table the ledger does not have.
func TestDataModelPage(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "docs", "data-model.md"))
	if err != nil {
		t.Fatal(err)
	}
	page := string(b)

	if want := fmt.Sprintf("| schema version | `PRAGMA user_version`, %d in this release |", SchemaVersion()); !strings.Contains(page, want) {
		t.Errorf("docs/data-model.md lacks the row of the schema version:\n%s", want)
	}
	if want := fmt.Sprintf("`0x%X`", applicationID); !strings.Contains(page, want) {
		t.Errorf("docs/data-model.md does not name the application ID %s", want)
	}

	// The section that lists the tables.
	_, section, ok := strings.Cut(page, "\n## Tables\n")
	if !ok {
		t.Fatal("docs/data-model.md has no section \"Tables\"")
	}
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	named := map[string]bool{}
	for _, m := range regexp.MustCompile("(?m)^\\| `([a-z_]+)` \\|").FindAllStringSubmatch(section, -1) {
		if named[m[1]] {
			t.Errorf("docs/data-model.md lists the table %s twice", m[1])
		}
		named[m[1]] = true
	}
	// meta is described in a paragraph of its own.
	if strings.Contains(section, "\n`meta` holds ") {
		named["meta"] = true
	}

	s := newStore(t)
	rows, err := s.db.QueryContext(context.Background(), `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	tables := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(tables) == 0 {
		t.Fatal("a new ledger has no tables")
	}
	var lacks, extra []string
	for name := range tables {
		if !named[name] {
			lacks = append(lacks, name)
		}
	}
	for name := range named {
		if !tables[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(lacks)
	sort.Strings(extra)
	if len(lacks) > 0 {
		t.Errorf("docs/data-model.md has no row for the tables %v", lacks)
	}
	if len(extra) > 0 {
		t.Errorf("docs/data-model.md lists %v, which a ledger does not have", extra)
	}
}
