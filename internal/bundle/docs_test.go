package bundle

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/testutil"
)

// jsonNames returns the names the fields of a struct type have in JSON.
func jsonNames(v any) []string {
	t := reflect.TypeOf(v)
	var out []string
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			out = append(out, name)
		}
	}
	return out
}

// TestBundleFormatPage keeps docs/bundle-format.md in step with the
// format: the members, every field of the manifest, the format version,
// what a bundle never holds and the messages of a reader are the ones of
// this package.
func TestBundleFormatPage(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "docs", "bundle-format.md"))
	if err != nil {
		t.Fatal(err)
	}
	page := string(b)
	section := func(title string) string {
		_, s, ok := strings.Cut(page, "\n## "+title+"\n")
		if !ok {
			t.Fatalf("docs/bundle-format.md has no section %q", title)
		}
		if end := strings.Index(s, "\n## "); end >= 0 {
			s = s[:end]
		}
		return s
	}

	members := section("Members")
	for _, name := range []string{manifestName, sourcesName, readmeName, ledgerName} {
		if !strings.Contains(members, "| `"+name+"` |") {
			t.Errorf("docs/bundle-format.md: the section Members has no row for %s", name)
		}
	}

	manifest := section("manifest.json")
	if want := fmt.Sprintf("| `format` | `%s` |", Format); !strings.Contains(manifest, want) {
		t.Errorf("docs/bundle-format.md lacks the row %s", want)
	}
	if want := fmt.Sprintf("| `format_version` | %d. ", Version); !strings.Contains(manifest, want) {
		t.Errorf("docs/bundle-format.md lacks the row that starts with %s", want)
	}
	for _, name := range jsonNames(Manifest{}) {
		if !strings.Contains(manifest, "| `"+name+"` |") && !strings.Contains(manifest, "| `"+name+".") {
			t.Errorf("docs/bundle-format.md: the manifest table has no row for %s", name)
		}
	}
	for _, name := range jsonNames(LedgerInfo{}) {
		if !strings.Contains(manifest, "`ledger."+name+"`") {
			t.Errorf("docs/bundle-format.md: the manifest table does not name ledger.%s", name)
		}
	}
	// The fields of the parts are named in the row of the part.
	row := func(name string) string {
		_, r, ok := strings.Cut(manifest, "\n| `"+name+"` |")
		if !ok {
			return ""
		}
		r, _, _ = strings.Cut(r, "\n")
		return r
	}
	for part, fields := range map[string][]string{
		"anonymization": jsonNames(Anonymization{}),
		"subject":       jsonNames(Subject{}),
	} {
		for _, name := range fields {
			if !strings.Contains(row(part), "`"+name+"`") {
				t.Errorf("docs/bundle-format.md: the row of %s does not name its field %s", part, name)
			}
		}
	}

	holds := section("What a bundle holds, and what it never holds")
	for _, n := range NotCollected {
		if !strings.Contains(holds, "| "+n+" |") {
			t.Errorf("docs/bundle-format.md does not list what a bundle never holds: %q", n)
		}
	}

	checks := section("What a reader checks")
	for _, e := range []error{ErrNotBundle, ErrNewerFormat, ErrDamaged} {
		if !strings.Contains(checks, `"`+e.Error()+`"`) {
			t.Errorf("docs/bundle-format.md does not quote the message %q", e.Error())
		}
	}
	if want := fmt.Sprintf("at most %d GiB", maxLedger>>30); !strings.Contains(checks, want) {
		t.Errorf("docs/bundle-format.md does not name the largest ledger a reader takes: %s", want)
	}
}
