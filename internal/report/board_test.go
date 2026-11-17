package report

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/scan"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/testutil"
	"github.com/agenticode/my/internal/view"
)

// boardReport imports one input and builds its report in both languages.
func boardReport(t *testing.T, in string) []*elem {
	t.Helper()
	as, _, err := scan.Detect(ctx, in, scan.DetectOptions{Now: now, TempDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := ingest.Run(ctx, s, as, ingest.Options{Kind: model.ScanImport, Now: now, Host: "fixture-host"}); err != nil {
		t.Fatal(err)
	}
	var roots []*elem
	for _, lang := range []i18n.Lang{i18n.EN, i18n.KO} {
		c, err := view.Open(ctx, s, lang, engine.Options{Now: now})
		if err != nil {
			t.Fatal(err)
		}
		doc, err := Build(ctx, c, c.Result.Hosts[0], options(lang, Customer))
		if err != nil {
			t.Fatal(err)
		}
		raw, root := render(t, doc)
		for _, bad := range checkHTML(root, raw) {
			t.Errorf("%s %s: %s", filepath.Base(filepath.Dir(in)), lang, bad)
		}
		roots = append(roots, root)
	}
	return roots
}

// The board in the report: every stick names a card, the CPUs drawn are
// the board's, and slots set aside are drawn by name below it.
func TestBoardInTheReport(t *testing.T) {
	for _, c := range []struct {
		input          string
		cpus, sticks   int
		aside, slotmap bool
	}{
		{"explorer/linuxhw-m70klp2sb-m90598-001-373530/dmidecode.txt", 4, 48, false, false},
		{"explorer/netbox-sm-ssg-6028r/dmidecode.txt", 1, 4, true, false},
		{"explorer/linuxhw-r1g3d32-ec54ac/dmidecode.txt", 0, 0, false, true},
		{"klog/intel-skx.messages", 0, 0, false, true},
	} {
		for _, root := range boardReport(t, testutil.Testdata(t, strings.Split(c.input, "/")...)) {
			boards := root.find("svg.board")
			cpus, sticks := 0, 0
			for _, b := range boards {
				cpus += len(b.find("g.mb-cpu-die"))
				sticks += len(b.find(".mb-dimm-stick"))
			}
			if cpus != c.cpus || sticks != c.sticks {
				t.Errorf("%s: %d CPUs and %d sticks, want %d and %d", c.input, cpus, sticks, c.cpus, c.sticks)
			}
			if got := len(root.find("svg.slotmap")) - len(boards); (got > 0) != (c.aside || c.slotmap) {
				t.Errorf("%s: %d slot maps beside the board", c.input, got)
			}
			cards := 0
			root.walk(func(e *elem) {
				if e.attrs["id"] == "mx-cards" {
					cards++
				}
			})
			if c.cpus > 0 && cards != 1 {
				t.Errorf("%s: no list of cards", c.input)
			}
		}
	}
}
