package view

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter/dimmspd"
	"github.com/agenticode/my/internal/decode/spd"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/scan"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/testledger/spdhost"
	"github.com/agenticode/my/internal/testutil"
)

func openSPD(t *testing.T, setups ...spdhost.Setup) (*Case, *store.Store) {
	t.Helper()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "l.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	for _, o := range setups {
		spdhost.Scan(t, s, o)
	}
	return openCase(t, s), s
}

func openCase(t *testing.T, s *store.Store) *Case {
	t.Helper()
	c, err := Open(ctx, s, i18n.EN, engine.Options{Now: time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func spdView(t *testing.T, c *Case, host string) *SPD {
	t.Helper()
	h, err := c.Host(host)
	if err != nil {
		t.Fatal(err)
	}
	v, err := c.SPD(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func texts(ts []Text) string {
	var out []string
	for _, t := range ts {
		out = append(out, t.Text)
	}
	return strings.Join(out, "\n")
}

// The view of a host whose 16 DIMMs each have an SPD with their serial:
// every reading tied, the head, the shared fields, no difference.
func TestSPDView(t *testing.T) {
	c, _ := openSPD(t, spdhost.Setup{})
	v := spdView(t, c, "")
	if v.Read != 16 || v.Tied != 16 || v.DIMMs != 16 || len(v.Modules) != 16 || v.Reason != nil || !v.Looked {
		t.Fatalf("read %d tied %d dimms %d modules %d reason %+v", v.Read, v.Tied, v.DIMMs, len(v.Modules), v.Reason)
	}
	if v.Head.Text != "SPD of 16 of 16 DIMMs (spd5118), tied by serial number" {
		t.Errorf("head %q", v.Head.Text)
	}
	if s := v.Shared; s.Count != 16 || s.MakerID != "bank 1, 0x2C" || s.Maker != "Micron Technology" || s.PartNumber != "EXM64G48R2X4-A" || s.Org != "2Rx4" || s.MemoryType != "DDR5" || s.ModuleType != "RDIMM" || s.SizeBytes != 64<<30 || s.DieMbit != 16384 {
		t.Errorf("shared %+v", s)
	}
	for _, m := range v.Modules {
		if m.Slot == "" || m.Tie != model.SPDTieSerial || len(m.Differences) != 0 || m.SPD.Date != "2022-W43" || m.MakerSource != "table" {
			t.Errorf("module %+v", m)
		}
	}
	if len(v.Notes) != 0 {
		t.Errorf("notes %s", texts(v.Notes))
	}
	// The DIMM rows took the date and kept their width.
	h, _ := c.Host("")
	for _, d := range h.DIMMs {
		if d.DIMM != nil && (d.DIMM.MfgDate != "2022-W43" || d.DIMM.DeviceWidth.V != 4) {
			t.Errorf("DIMM %s: %+v", d.Slot, d.DIMM)
		}
	}
	// The order is the order of the board.
	if v.Modules[0].Slot != "CPU0_A1" || v.Modules[15].Slot != "CPU1_H1" {
		t.Errorf("order %s .. %s", v.Modules[0].Slot, v.Modules[15].Slot)
	}
}

// Each difference with a case that gives it and the view of the case
// before that gives none (TestSPDView).
func TestSPDDifferences(t *testing.T) {
	part := func(slot string, b []byte) []byte {
		switch slot {
		case "CPU0_A1":
			return spd.Build(b, spd.Change{Offset: 521, Bytes: []byte(fmt.Sprintf("%-30s", "MTC40F2046S1RC48BA1"))})
		case "CPU0_B1":
			return spd.Build(b, spd.Change{Offset: 234, Bytes: []byte{b[234] &^ 0x38}}) // 1 rank: size and ranks
		case "CPU0_D1":
			return spd.Build(b, spd.Change{Offset: 6, Bytes: []byte{b[6]&0x1F | 1<<5}}) // x8: width and size
		case "CPU0_E1":
			return spd.Build(b, spd.Change{Offset: 3, Bytes: []byte{0x02}}) // UDIMM
		}
		return b
	}
	c, _ := openSPD(t, spdhost.Setup{Change: part})
	v := spdView(t, c, "")
	got := map[string]string{}
	for _, m := range v.Modules {
		got[m.Slot] = texts(m.Differences)
	}
	want := map[string]string{
		"CPU0_A1": "CPU0_A1: SMBIOS gives the part number EXM64G48R2X4-A, the SPD MTC40F2046S1RC48BA1.",
		"CPU0_B1": "CPU0_B1: SMBIOS reports 64 GB, the SPD describes a module of 32 GB.\nCPU0_B1: SMBIOS reports 2 ranks, the SPD 1.",
		"CPU0_D1": "CPU0_D1: SMBIOS reports 64 GB, the SPD describes a module of 32 GB.\nCPU0_D1: EDAC reports x4 devices, the SPD x8.",
		"CPU0_E1": "CPU0_E1: SMBIOS reports DDR5 RDIMM, the SPD DDR5 UDIMM.",
		"CPU0_F1": "",
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s:\n got %q\nwant %q", k, got[k], w)
		}
	}
	// The width of EDAC is replaced by that of the SPD in the DIMM row.
	h, _ := c.Host("")
	for _, d := range h.DIMMs {
		if d.Slot == "CPU0_D1" && d.DIMM.DeviceWidth.V != 8 {
			t.Errorf("DIMM of CPU0_D1: width %v", d.DIMM.DeviceWidth)
		}
	}
}

// The notes of the view: reversed serials, untied readings, DIMMs without
// a reading, no SMBIOS.
func TestSPDNotes(t *testing.T) {
	rev := func(slot, sn string) string {
		switch slot {
		case "CPU0_A1", "CPU0_B1":
			return sn[6:8] + sn[4:6] + sn[2:4] + sn[0:2]
		case "CPU0_C1":
			return "0BADBEEF"
		}
		return sn
	}
	c, _ := openSPD(t, spdhost.Setup{Serial: rev, Skip: func(slot string) bool { return slot == "CPU1_H1" }})
	v := spdView(t, c, "")
	want := "2 serial numbers are tied with their four bytes in reverse order: firmware prints them that way in SMBIOS.\n" +
		"1 SPD reading fits no serial number of SMBIOS and is listed by bus and address.\n" +
		"2 of 16 DIMMs have no SPD reading."
	if texts(v.Notes) != want {
		t.Errorf("notes:\n%s\nwant\n%s", texts(v.Notes), want)
	}
	last := v.Modules[len(v.Modules)-1]
	if last.Slot != "" || last.Place() != "i2c-0 0x52" {
		t.Errorf("the untied reading is listed as %q (%+v)", last.Place(), last)
	}
	c2, _ := openSPD(t, spdhost.Setup{NoSMBIOS: true})
	v2 := spdView(t, c2, "")
	if v2.Tied != 0 || !strings.Contains(texts(v2.Notes), "SMBIOS serial numbers were not read") || v2.Head.Text != "SPD of 16 modules (spd5118), tied to no slot" {
		t.Errorf("no SMBIOS: tied %d head %q notes %s", v2.Tied, v2.Head.Text, texts(v2.Notes))
	}
	if v2.Lots == nil || v2.Lots.Findings || v2.Lots.Why == nil || len(v2.Lots.Rows) != 1 || v2.Lots.Rows[0].DIMMs != 16 {
		t.Errorf("no SMBIOS: lots %+v", v2.Lots)
	}
}

// A reading of state crc and one of the first page are shown and fill no
// DIMM row; the reading of the first page names write disable.
func TestSPDStatesInTheView(t *testing.T) {
	ch := func(slot string, b []byte) []byte {
		switch slot {
		case "CPU0_A1":
			b[234] ^= 0x08
		case "CPU0_B1":
			return b[:128]
		}
		return b
	}
	c, _ := openSPD(t, spdhost.Setup{Change: ch})
	v := spdView(t, c, "")
	var crc, first *SPDModule
	for i := range v.Modules {
		switch v.Modules[i].State {
		case spd.StateCRC:
			crc = &v.Modules[i]
		case spd.StateShort:
			first = &v.Modules[i]
		}
	}
	if crc == nil || crc.Slot != "CPU0_A1" || crc.StateText == nil || !strings.HasPrefix(crc.StateText.Text, "base CRC bad: stored 0x") {
		t.Fatalf("crc: %+v", crc)
	}
	if first == nil || first.Slot != "" || first.StateText == nil || first.StateText.Text != "128 bytes read: the pages after the first could not be selected" || first.SPD.DeviceWidth.V != 4 {
		t.Fatalf("first page: %+v", first)
	}
	h, _ := c.Host("")
	for _, d := range h.DIMMs {
		if (d.Slot == "CPU0_A1" || d.Slot == "CPU0_B1") && d.DIMM.MfgDate != "" {
			t.Errorf("%s took the date %q", d.Slot, d.DIMM.MfgDate)
		}
	}
}

// The view of a host on which no SPD was read: the reason with its texts
// and the facts beside the probe; no lots.
func TestSPDReasonInTheView(t *testing.T) {
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "l.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root, _ := spdhost.Host(t, 1)
	probe := "spd-probe 1\ni2c present\nadapter i2c-0 \"SMBus I801 adapter at 0000:00:1f.4\"\npci 0000:00:1f.4 id=8086:a323 driver=i801_smbus\ndriver ee1004 registered\ndriver spd5118 registered\nbmc present\n"
	if err := os.WriteFile(filepath.Join(root, dimmspd.ProbeFile), []byte(probe), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := scan.Run(ctx, s, scan.Options{Root: root, TempDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	v := spdView(t, openCase(t, s), "")
	if v.Reason == nil || v.Reason.Code != dimmspd.ReasonNoSPDDevice || v.Lots != nil || len(v.Modules) != 0 {
		t.Fatalf("view %+v", v)
	}
	if v.Head.Text != "SPD not read: no SPD device on the bus" || !strings.HasPrefix(v.Reason.Text.Text, "The kernel shows no SPD device on i2c-0.") ||
		v.Reason.Step == nil || !strings.HasPrefix(v.Reason.Step.Text, "Ask the BMC") {
		t.Errorf("reason %+v", v.Reason)
	}
	if texts(v.Reason.Facts) != "SMBIOS lists 32 slots.\nThe host has a BMC." {
		t.Errorf("facts %q", texts(v.Reason.Facts))
	}
	ko := spdView(t, openCase(t, s).In(i18n.KO), "")
	if ko.Reason.Short.Text != "버스에 SPD 장치 없음" || !strings.Contains(ko.Reason.Text.Text, "i2c-0") {
		t.Errorf("Korean: %+v", ko.Reason)
	}
}

// The table of date codes for one host: the cluster line at two DIMMs
// with a finding, none at one; the closing sentence; the organisations.
func TestLotsOfOneHost(t *testing.T) {
	two := func(slot string) (int, int) {
		if slot == "CPU0_C1" || slot == "CPU1_D1" || slot == "CPU0_A1" {
			return 2022, 43
		}
		return 2023, 8
	}
	c, _ := openSPD(t, spdhost.Setup{Date: two})
	v := spdView(t, c, "")
	l := v.Lots
	if l == nil || !l.Findings || len(l.Rows) != 2 || l.Grade != model.VerdictReplaceNow {
		t.Fatalf("lots %+v", l)
	}
	r := l.Rows[0]
	if r.MfgDate != "2022-W43" || r.DIMMs != 3 || r.WithFinding != 2 || r.WithUE != 1 || strings.Join(r.Slots, " ") != "CPU0_C1 CPU1_D1" || r.DateSource != "spd" || r.MakerID != "bank 1, 0x2C" {
		t.Errorf("row %+v", r)
	}
	if r := l.Rows[1]; r.MfgDate != "2023-W08" || r.DIMMs != 13 || r.WithFinding != 0 {
		t.Errorf("row %+v", r)
	}
	want := "2 of the 2 DIMMs with a finding carry 2022-W43; 1 of the 14 DIMMs without one carries it too.\n" +
		"The table counts modules by the date code in their SPD. Whether a lot is at fault is for the maker's failure analysis to say."
	if texts(l.Lines) != want {
		t.Errorf("lines:\n%s", texts(l.Lines))
	}
	if len(l.ByOrganisation) != 1 || l.ByOrganisation[0].Organisation != "2Rx4" || l.ByOrganisation[0].DIMMs != 16 || l.ByOrganisation[0].WithFinding != 2 {
		t.Errorf("organisations %+v", l.ByOrganisation)
	}
	one := func(slot string) (int, int) {
		if slot == "CPU0_C1" {
			return 2022, 43
		}
		return 2023, 8
	}
	c1, _ := openSPD(t, spdhost.Setup{Date: one})
	l1 := spdView(t, c1, "").Lots
	if strings.Contains(texts(l1.Lines), "carry") {
		t.Errorf("a cluster line at one DIMM: %s", texts(l1.Lines))
	}
	// Without any date: one row of DIMMs without a date code.
	c0, _ := openSPD(t, spdhost.Setup{Date: func(string) (int, int) { return 0, 0 }})
	l0 := spdView(t, c0, "").Lots
	if len(l0.Rows) != 1 || l0.Rows[0].MfgDate != "" || l0.Rows[0].DIMMs != 16 || strings.Contains(texts(l0.Lines), "carry") {
		t.Errorf("no date: %+v", l0)
	}
}

// The table over a ledger of three hosts: counts per host in a row; a date
// a BMC gave is marked.
func TestLotsOfThreeHosts(t *testing.T) {
	c, s := openSPD(t,
		spdhost.Setup{Seed: 1, Host: "h1"},
		spdhost.Setup{Seed: 2, Host: "h2", Date: func(string) (int, int) { return 2023, 8 }},
		spdhost.Setup{Seed: 3, Host: "h3", Skip: func(slot string) bool { return slot == "CPU0_A1" }})
	// A date of a BMC for the DIMM of h3 that has no SPD.
	h3, _ := c.Host("h3")
	var uid string
	for _, d := range h3.DIMMs {
		if d.Slot == "CPU0_A1" {
			uid = d.UID
		}
	}
	err := s.Update(ctx, func(w *store.Writer) error {
		if err := w.UpsertDIMM(&model.DIMM{UID: uid, Identity: model.IdentitySerial, MfgDate: "2021-W05"}); err != nil {
			return err
		}
		_, err := w.InsertBMCMemory(&model.BMCMemory{Host: "h3", ObservedAt: time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC), Label: "CPU0_A1", Slot: "CPU0_A1", Source: "redfish", Fingerprint: "x"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	c = openCase(t, s)
	l, err := c.Lots(ctx, c.Result.Hosts)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(l.Hosts, " ") != "h1 h2 h3" || len(l.Rows) != 3 {
		t.Fatalf("lots %+v", l)
	}
	if r := l.Rows[0]; r.MfgDate != "2021-W05" || r.DateSource != "bmc" || r.DIMMs != 1 {
		t.Errorf("BMC row %+v", r)
	}
	r := l.Rows[1]
	if r.MfgDate != "2022-W43" || r.DIMMs != 31 || len(r.Slots) != 0 || len(r.Hosts) != 2 {
		t.Errorf("row %+v", r)
	}
	for _, x := range l.Rows[1].Hosts {
		if x.Host == "h1" && (x.DIMMs != 16 || x.WithFinding != 2) || x.Host == "h3" && x.DIMMs != 15 {
			t.Errorf("host %+v", x)
		}
	}
	if r := l.Rows[2]; r.MfgDate != "2023-W08" || r.DIMMs != 16 {
		t.Errorf("row %+v", r)
	}
}

// No text of the views spd and lots holds a word that judges a lot.
func TestLotWording(t *testing.T) {
	// The Korean words are matched where they stand alone: 결함 (defect)
	// is also the end of 연결함 (connects).
	banned := regexp.MustCompile(`(?i)\bdefect|\bbad lot|\bfaulty\b|root cause|\bcaused\b|\bproves?\b|\brecall|불량|(^|[^연])결함|원인|입증|증명|리콜|회수`)
	for code, m := range SPDMessages {
		for _, s := range []string{m.EN, m.KO} {
			if banned.MatchString(s) {
				t.Errorf("%s: %q", code, s)
			}
		}
		if strings.TrimSpace(m.EN) == "" || strings.TrimSpace(m.KO) == "" {
			t.Errorf("%s lacks a language", code)
		}
	}
	// Both sides in the cluster line, and the closing sentence.
	if s := SPDMessages["lots.cluster"].EN; !strings.Contains(s, "with a finding") || !strings.Contains(s, "without one") {
		t.Errorf("cluster line %q", s)
	}
}

// The fields of --json of the views spd and lots are those
// docs/data-model.md lists.
func TestSPDJSONFields(t *testing.T) {
	c, _ := openSPD(t, spdhost.Setup{})
	v := spdView(t, c, "")
	doc, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "docs", "data-model.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"spd", "lots"} {
		var x any = v
		if name == "lots" {
			x = v.Lots
		}
		b, _ := json.Marshal(x)
		var m map[string]any
		json.Unmarshal(b, &m)
		_, section, ok := strings.Cut(string(doc), "### The view `"+name+"`")
		if !ok {
			t.Fatalf("docs/data-model.md has no section on the view %s", name)
		}
		if i := strings.Index(section, "\n### "); i > 0 {
			section = section[:i]
		}
		for k := range m {
			if !strings.Contains(section, "`"+k+"`") && !strings.Contains(section, "`"+k+"[]`") {
				t.Errorf("docs/data-model.md does not list the field %q of the view %s", k, name)
			}
		}
	}
}
