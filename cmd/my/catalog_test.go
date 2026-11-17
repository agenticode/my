package main

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/memras"
	"github.com/agenticode/my/internal/preflight"
	"github.com/agenticode/my/internal/report"
	"github.com/agenticode/my/internal/view"
)

// catalogs are every message catalog of the tool, by the name a reader of
// the code finds them under.
var catalogs = map[string]i18n.Catalog{
	"engine":          engine.Messages,
	"view":            view.Messages,
	"view.inventory":  view.InventoryMessages,
	"view.board":      view.BoardMessages,
	"view.spd":        view.SPDMessages,
	"report":          report.Words,
	"preflight":       preflight.Messages,
	"memras":          memras.Messages,
	"cli":             cli.Messages,
	"main.words":      words,
	"main.labels":     labels,
	"main.labelWords": labelWords,
	"main.collect":    collectWords,
	"main.inventory":  inventoryWords,
	"main.bios":       biosWords,
}

// TestKoreanCatalog: every message that has an English template has a
// Korean one. With MY_KO_DUMP=FILE it writes every Korean template to
// FILE, one per paragraph, for the Korean lint (ko_lint.py) to read:
// the screens speak in these sentences.
func TestKoreanCatalog(t *testing.T) {
	var out []string
	for _, name := range sortedKeys(catalogs) {
		c := catalogs[name]
		for _, code := range c.Codes() {
			m := c[code]
			if m.KO == "" {
				t.Errorf("%s %s: no Korean", name, code)
				continue
			}
			out = append(out, "# "+name+" "+code+"\n"+m.KO)
		}
	}
	if file := os.Getenv("MY_KO_DUMP"); file != "" {
		if err := os.WriteFile(file, []byte(strings.Join(out, "\n\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func sortedKeys(m map[string]i18n.Catalog) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
