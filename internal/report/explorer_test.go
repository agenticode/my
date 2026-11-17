package report

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/testledger"
	"github.com/agenticode/my/internal/testutil"
)

// withReadings copies the demo ledger and adds readings of the host,
// taken from the hand-made fixtures of the explorer.
func withReadings(t *testing.T, kinds map[string]string) *store.Store {
	t.Helper()
	src := testledger.Path(t, "demo", 1, "")
	dst := filepath.Join(t.TempDir(), "ledger.db")
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := os.Create(dst)
	io.Copy(out, in)
	in.Close()
	out.Close()
	s, err := store.Open(ctx, dst, store.ReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	hosts, _ := s.Hosts(ctx)
	at := time.Date(2026, 11, 12, 23, 0, 0, 0, time.UTC)
	err = s.Update(ctx, func(w *store.Writer) error {
		for kind, file := range kinds {
			b, err := os.ReadFile(testutil.Testdata(t, strings.Split(file, "/")...))
			if err != nil {
				return err
			}
			if err := w.PutReading(&model.HostReading{Host: hosts[0].Name, Kind: kind, TakenAt: at, Text: string(b)}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

var everyReading = map[string]string{
	model.ReadingMeminfo:       "explorer/made-two-nodes/proc/meminfo",
	model.ReadingVmstat:        "explorer/made-two-nodes/proc/vmstat",
	model.ReadingPressure:      "explorer/made-two-nodes/proc/pressure/memory",
	model.ReadingSlabinfo:      "explorer/made-two-nodes/proc/slabinfo",
	model.ReadingNumactl:       "explorer/made-eight-nodes/numactl-hardware.txt",
	model.ReadingNumastat:      "explorer/made-eight-nodes/numastat.txt",
	model.ReadingSarB:          "explorer/made-sar-12h/sar-B.txt",
	model.ReadingLruGenEnabled: "explorer/made-two-nodes/lru-gen-enabled",
}

func TestExplorerSection(t *testing.T) {
	for _, c := range []struct {
		name     string
		kinds    map[string]string
		sections int
		panels   int
	}{
		{"every reading", everyReading, 9, 5},
		{"one reading", map[string]string{model.ReadingLruGenEnabled: everyReading[model.ReadingLruGenEnabled]}, 9, 1},
	} {
		s := withReadings(t, c.kinds)
		for _, lang := range []i18n.Lang{i18n.EN, i18n.KO} {
			for _, aud := range []Audience{Customer, FA} {
				doc := buildFrom(t, s, options(lang, aud))
				raw, root := render(t, doc)
				for _, bad := range checkHTML(root, raw) {
					t.Errorf("%s %s %s: %s", c.name, lang, aud, bad)
				}
				if n := len(root.find("section.sec")); n != c.sections {
					t.Errorf("%s: %d sections", c.name, n)
				}
				panels := 0
				root.walk(func(e *elem) {
					if strings.Contains(" "+e.attrs["class"]+" ", " mx-panel ") {
						panels++
					}
				})
				if panels != c.panels {
					t.Errorf("%s %s: %d panels, want %d", c.name, lang, panels, c.panels)
				}
				hints := len(root.find("ul.mx-hints"))
				if c.panels == 5 && (aud == FA) != (hints == 1) {
					t.Errorf("%s %s %s: %d lists of hints; hints are for failure analysis only", c.name, lang, aud, hints)
				}
				if c.panels == 5 {
					if n := len(root.find("svg.mx-chart")); n != 4 {
						t.Errorf("%d charts of sar -B, want 4", n)
					}
				}
			}
		}
	}
}

// A sar -B table with a 12-hour clock gives four charts with one point
// per row and the times without AM and PM.
func TestSarChartsOf12HourClock(t *testing.T) {
	s := withReadings(t, map[string]string{model.ReadingSarB: everyReading[model.ReadingSarB]})
	doc := buildFrom(t, s, options(i18n.EN, Customer))
	_, root := render(t, doc)
	charts := root.find("svg.mx-chart")
	if len(charts) != 4 {
		t.Fatalf("%d charts", len(charts))
	}
	if pts := len(charts[0].find("circle.mx-dot-pt")); pts != 7 {
		t.Errorf("%d points, want the 7 rows of the table", pts)
	}
	if text := charts[0].content(); !strings.Contains(text, "12:10") || strings.Contains(text, "PM") {
		t.Errorf("times: %s", text)
	}
}

// On paper every panel is printed.
func TestExplorerPrintsEveryPanel(t *testing.T) {
	b, err := os.ReadFile("style.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	i := strings.Index(css, "@media print {\n  .mx-tabs { display: none; }")
	if i < 0 || !strings.Contains(css[i:], ".mx-panel, .mx-panels.js .mx-panel { display: block;") {
		t.Error("the print style does not show every panel of the explorer")
	}
}
