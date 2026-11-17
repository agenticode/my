package report

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/adapter/dimmspd"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/scan"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/testledger/spdhost"
	"github.com/agenticode/my/internal/testutil"
)

func spdLedger(t *testing.T, o spdhost.Setup, probe string) *store.Store {
	t.Helper()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if probe == "" {
		spdhost.Scan(t, s, o)
		return s
	}
	// A host whose probe names a reason and that has no reading.
	root, _ := spdhost.Host(t, 1)
	if err := os.WriteFile(filepath.Join(root, dimmspd.ProbeFile), []byte(probe), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := scan.Run(ctx, s, scan.Options{Root: root, TempDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	return s
}

func headings(root *elem) []string {
	var out []string
	for _, h := range root.find("section.sec") {
		if hs := h.find("h2"); len(hs) > 0 {
			out = append(out, strings.Join(strings.Fields(hs[0].content()), " "))
		}
	}
	return out
}

// The section Module SPD stands before the Memory Explorer in a case with
// a reading, in both languages and for both audiences; the report for
// failure analysis adds the fields and the bytes of each module.
func TestSPDSection(t *testing.T) {
	weeks := func(slot string) (int, int) {
		if slot == "CPU0_C1" || slot == "CPU1_D1" {
			return 2022, 43
		}
		return 2023, 8
	}
	ddr3 := testutil.ReadTestdata(t, "spd", "rigred", "ddr3-sodimm.bin")
	states := func(slot string, b []byte) []byte {
		switch slot {
		case "CPU0_A1":
			b[234] ^= 0x08
		case "CPU0_B1":
			return b[:128]
		case "CPU0_C1":
			return make([]byte, 1024)
		case "CPU0_D1":
			return ddr3
		}
		return b
	}
	for _, c := range []struct {
		name  string
		setup spdhost.Setup
	}{{"tied", spdhost.Setup{Date: weeks}}, {"states", spdhost.Setup{Change: states}}} {
		s := spdLedger(t, c.setup, "")
		for _, lang := range []i18n.Lang{i18n.EN, i18n.KO} {
			for _, aud := range []Audience{Customer, FA} {
				doc := buildFrom(t, s, options(lang, aud))
				raw, root := render(t, doc)
				for _, bad := range checkHTML(root, raw) {
					t.Errorf("%s %s %s: %s", c.name, lang, aud, bad)
				}
				hs := headings(root)
				i := -1
				for k, h := range hs {
					if strings.HasSuffix(h, doc.say("sec.spd")) {
						i = k
					}
				}
				// The made host has no reading of the explorer: data quality
				// follows; with one the explorer stands between them.
				next := hs[i+1]
				if doc.hasExplorer() {
					next = hs[i+2]
				}
				if i != 6 || !strings.HasPrefix(hs[i], "7 ") || !strings.HasSuffix(next, doc.say("sec.7")) {
					t.Errorf("%s %s %s: sections %q", c.name, lang, aud, hs)
				}
				spd := root.find("table.spd")
				if len(spd) != 1 || len(spd[0].find("tr")) != 17 {
					t.Errorf("%s: %d tables of modules", c.name, len(spd))
				}
				if lots := root.find("table.lots"); len(lots) != 1 {
					t.Errorf("%s: %d tables of date codes", c.name, len(lots))
				}
				if text := root.content(); c.name == "tied" && lang == i18n.EN && !strings.Contains(text, "2 of the 2 DIMMs with a finding carry 2022-W43; 0 of the 14 DIMMs without one carry it too.") {
					t.Errorf("no cluster line")
				}
				hex := len(root.find("pre.hex"))
				if aud == FA && hex == 0 || aud == Customer && hex > 0 {
					t.Errorf("%s %s %s: %d hex dumps", c.name, lang, aud, hex)
				}
				if aud == FA && c.name == "tied" && hex != 16 {
					t.Errorf("%d hex dumps, want one per module", hex)
				}
				if c.name == "tied" {
					out := rootRE.ReplaceAllString(sectionOutline(outline(root), "section#s7"), "ROOT/spd/")
					testutil.Golden(t, "spd-"+string(lang)+"-"+string(aud)+".txt", []byte(out))
				}
			}
		}
	}
}

// A case with a reason and no reading has the sections of today and one
// paragraph in data quality; a case without an SPD source has neither.
func TestSPDReasonInDataQuality(t *testing.T) {
	s := spdLedger(t, spdhost.Setup{}, "spd-probe 1\ni2c present\nadapter i2c-0 \"SMBus I801 adapter at 0000:00:1f.4\"\npci 0000:00:1f.4 id=8086:a323 driver=i801_smbus\ndriver spd5118 registered\nbmc present\n")
	for _, lang := range []i18n.Lang{i18n.EN, i18n.KO} {
		doc := buildFrom(t, s, options(lang, Customer))
		raw, root := render(t, doc)
		for _, bad := range checkHTML(root, raw) {
			t.Errorf("%s: %s", lang, bad)
		}
		for _, h := range headings(root) {
			if strings.HasSuffix(h, doc.say("sec.spd")) {
				t.Errorf("a section Module SPD without a reading: %q", headings(root))
			}
		}
		ps := root.find("p.spd-reason")
		if len(ps) != 1 {
			t.Fatalf("%s: %d paragraphs of the reason", lang, len(ps))
		}
		if lang == i18n.EN && !strings.Contains(ps[0].content(), "The kernel shows no SPD device on i2c-0.") {
			t.Errorf("paragraph %q", ps[0].content())
		}
	}
	// The demo ledger has no SPD source: no section, no paragraph.
	doc := build(t, "demo", "", i18n.EN, Customer)
	_, root := render(t, doc)
	if len(root.find("p.spd-reason"))+len(root.find("table.spd")) > 0 {
		t.Error("a case without an SPD source shows the SPD")
	}
}

// On paper the section is printed: the print style hides nothing of it.
func TestSPDPrints(t *testing.T) {
	b, err := os.ReadFile("style.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b) + spdSheet
	for _, block := range strings.Split(css, "@media print")[1:] {
		end := strings.Index(block, "\n}")
		if end < 0 {
			end = len(block)
		}
		for _, cls := range []string{".spd", ".lots", ".spd-lines", ".spd-shared", ".spd-lots"} {
			for _, rule := range strings.Split(block[:end], "}") {
				if strings.Contains(rule, cls) && strings.Contains(rule, "display: none") {
					t.Errorf("the print style hides %s: %q", cls, rule)
				}
			}
		}
	}
	if !strings.Contains(css, "@media print { .spd-module { break-inside: avoid; } table.spd, table.lots { display: table; } }") {
		t.Error("the print style of the section is missing")
	}
}

// rootRE finds the directory a made host was written to.
var rootRE = regexp.MustCompile(`\S*/root/spd/`)

// sectionOutline cuts the lines of one section out of the outline of a
// report.
func sectionOutline(all, head string) string {
	var out []string
	in := false
	for _, l := range strings.Split(all, "\n") {
		if strings.HasPrefix(strings.TrimLeft(l, " "), "section#") {
			in = strings.HasPrefix(strings.TrimLeft(l, " "), head)
		}
		if in {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n") + "\n"
}

// With readings of the explorer as well: Module SPD is section 7, the
// Memory Explorer 8, data quality 9, the appendix 10.
func TestSPDBeforeTheExplorer(t *testing.T) {
	s := spdLedger(t, spdhost.Setup{}, "")
	hosts, _ := s.Hosts(ctx)
	b, err := os.ReadFile(testutil.Testdata(t, "explorer", "made-two-nodes", "proc", "meminfo"))
	if err != nil {
		t.Fatal(err)
	}
	err = s.Update(ctx, func(w *store.Writer) error {
		return w.PutReading(&model.HostReading{Host: hosts[0].Name, Kind: model.ReadingMeminfo, TakenAt: now, Text: string(b)})
	})
	if err != nil {
		t.Fatal(err)
	}
	doc := buildFrom(t, s, options(i18n.EN, Customer))
	raw, root := render(t, doc)
	for _, bad := range checkHTML(root, raw) {
		t.Error(bad)
	}
	hs := headings(root)
	want := []string{"7 Module SPD", "8 Memory Explorer", "9 Data quality", "10 Appendix"}
	if len(hs) != 10 || strings.Join(hs[6:], "|") != strings.Join(want, "|") {
		t.Errorf("sections %q", hs)
	}
	toc := root.find("ol.toc")
	if len(toc) != 1 || !strings.Contains(toc[0].content(), "7 Module SPD") || !strings.Contains(toc[0].content(), "10 Appendix") {
		t.Errorf("contents %q", toc[0].content())
	}
}

// A report with the section carries its style; one without carries the
// style sheet of today alone.
func TestSPDStyleOnlyWithTheSection(t *testing.T) {
	with, _ := render(t, buildFrom(t, spdLedger(t, spdhost.Setup{}, ""), options(i18n.EN, Customer)))
	without, _ := render(t, build(t, "demo", "", i18n.EN, Customer))
	if !strings.Contains(string(with), ".spd-fields") || strings.Contains(string(without), ".spd-fields") {
		t.Error("the style of the section")
	}
}
