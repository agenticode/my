package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/adapter/dimmspd"
	"github.com/agenticode/my/internal/adapter/dimmspd/spdtest"
	"github.com/agenticode/my/internal/decode/spd"
	"github.com/agenticode/my/internal/term"
	"github.com/agenticode/my/internal/testledger/spdhost"
	"github.com/agenticode/my/internal/testutil"
)

// spdScreen runs my spd (or another command) on a made root and returns
// the exit code and the output with the root replaced by ROOT.
func spdScreen(t *testing.T, root string, args ...string) (int, string) {
	t.Helper()
	code, out, errOut := runEnv(nil, append(args, "--root", root)...)
	if errOut != "" {
		t.Fatalf("%v: %s", args, errOut)
	}
	return code, strings.ReplaceAll(out, root, "ROOT")
}

func checkWidth(t *testing.T, name, out string, width int) {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if term.Width(l) > width {
			t.Errorf("%s: a line of %d columns, wider than %d: %q", name, term.Width(l), width, l)
		}
	}
}

func twoWeeks(slot string) (int, int) {
	if slot == "CPU0_C1" || slot == "CPU1_D1" || slot == "CPU0_A1" {
		return 2022, 43
	}
	return 2023, 8
}

// The screens of my spd for made hosts, in both languages and three
// widths: every module tied, two makers, every state of a reading, a host
// without SMBIOS serial numbers.
func TestSPDScreens(t *testing.T) {
	samsung := func(slot string, b []byte) []byte {
		if strings.HasPrefix(slot, "CPU1") {
			return spd.Build(b, spd.Change{Offset: 512, Bytes: []byte{0x80, 0xCE}}, spd.Change{Offset: 521, Bytes: []byte(fmt.Sprintf("%-30s", "M321R8GA0BB0-CQK"))})
		}
		return b
	}
	ddr3 := testutil.ReadTestdata(t, "spd", "rigred", "ddr3-sodimm.bin")
	states := func(slot string, b []byte) []byte {
		switch slot {
		case "CPU0_A1":
			b[234] ^= 0x08 // crc
		case "CPU0_B1":
			return b[:128] // short: the first page
		case "CPU0_C1":
			return make([]byte, 1024) // blank
		case "CPU0_D1":
			return ddr3 // type
		}
		return b
	}
	cases := []struct {
		name  string
		setup spdhost.Setup
	}{
		{"tied", spdhost.Setup{Date: twoWeeks}},
		{"makers", spdhost.Setup{Date: twoWeeks, Change: samsung}},
		{"states", spdhost.Setup{Change: states}},
		{"no-smbios", spdhost.Setup{Date: twoWeeks, NoSMBIOS: true}},
	}
	for _, c := range cases {
		root := spdhost.Root(t, c.setup)
		if c.name == "states" {
			// Three reads that failed before the first byte: in the probe,
			// without a file.
			p, err := os.ReadFile(filepath.Join(root, dimmspd.ProbeFile))
			if err != nil {
				t.Fatal(err)
			}
			for i, st := range []string{"denied errno=EACCES at=0", "io errno=EIO at=0", "timeout errno=deadline at=0"} {
				id := fmt.Sprintf("1-00%02x", 0x50+i)
				os.Remove(filepath.Join(root, dimmspd.Dir, id+".bin"))
				p = []byte(strings.Replace(string(p), "client "+id+" name=spd5118 driver=spd5118 eeprom=1024 read=1024 state=ok",
					"client "+id+" name=spd5118 driver=spd5118 eeprom=1024 read=0 state="+st, 1))
			}
			if err := os.WriteFile(filepath.Join(root, dimmspd.ProbeFile), p, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		for _, lang := range []string{"en", "ko"} {
			for _, w := range []int{80, 104, 160} {
				name := fmt.Sprintf("spd/%s-%s-%d.txt", c.name, lang, w)
				code, out := spdScreen(t, root, "spd", "--lang", lang, "--width", fmt.Sprint(w))
				if code != 3 {
					t.Errorf("%s: exit %d, want 3 (the grade of the host)", name, code)
				}
				checkWidth(t, name, out, w)
				testutil.Golden(t, name, []byte(out))
			}
		}
		if c.name == "tied" {
			_, out := spdScreen(t, root, "spd", "--lang", "en", "--width", "80", "--ascii")
			testutil.Golden(t, "spd/tied-en-80-ascii.txt", []byte(out))
			for _, lang := range []string{"en", "ko"} {
				_, out := spdScreen(t, root, "spd", "--lang", lang, "--width", "104", "--detail")
				checkWidth(t, "detail", out, 104)
				testutil.Golden(t, "spd/tied-"+lang+"-104-detail.txt", []byte(out))
			}
		}
	}
}

// The screen of every reason: the probe of each made host of the reader's
// tests, as a copy of a host's files holds it.
func TestSPDReasonScreens(t *testing.T) {
	hosts := spdtest.Hosts(testutil.ReadTestdata(t, "spd", "illumos", "ddr4-rdimm-64g-2rx4.bin"),
		testutil.ReadTestdata(t, "spd", "illumos", "ddr5-rdimm-64g-2rx4.bin"), testutil.ReadTestdata(t, "spd", "rigred", "ddr3-sodimm.bin"))
	root, _ := spdhost.Host(t, 1)
	probes := []struct{ name, text string }{{"virtual-machine", "spd-probe 1\nvirtual VMware\n"}}
	for _, h := range hosts {
		if h.Reason == "" {
			continue
		}
		fsys := spdtest.NewFS(t, h.Tree)
		for k, v := range h.Faults {
			fsys.Faults[k] = v
		}
		p, _ := dimmspd.ProbeHost(context.Background(), fsys, dimmspd.LiveOptions{Privileged: h.Privileged, Release: spdtest.Release, Deadline: spdtest.Deadline})
		probes = append(probes, struct{ name, text string }{h.Name, p.Text()})
	}
	seen := map[string]bool{}
	for _, lang := range []string{"en", "ko"} {
		var all strings.Builder
		for _, p := range probes {
			if err := os.WriteFile(filepath.Join(root, dimmspd.ProbeFile), []byte(p.text), 0o644); err != nil {
				t.Fatal(err)
			}
			code, out := spdScreen(t, root, "spd", "--lang", lang, "--width", "80")
			if code != 3 {
				t.Errorf("%s: exit %d", p.name, code)
			}
			checkWidth(t, p.name, out, 80)
			fmt.Fprintf(&all, "== %s\n%s\n", p.name, out)
			_, js := spdScreen(t, root, "spd", "--json")
			code2 := dimmspd.Reason(dimmspd.ParseProbe(p.text), "DDR5")
			if !strings.Contains(js, `"code": "`+code2+`"`) {
				t.Errorf("%s: the JSON lacks the reason %s", p.name, code2)
			}
			seen[code2] = true
		}
		testutil.Golden(t, "spd/reasons-"+lang+".txt", []byte(all.String()))
	}
	for _, r := range dimmspd.Reasons {
		if !seen[r] {
			t.Errorf("no screen of the reason %s", r)
		}
	}
}

// my lots for one host and for a ledger of three hosts.
func TestLotsScreens(t *testing.T) {
	root := spdhost.Root(t, spdhost.Setup{Date: twoWeeks})
	for _, lang := range []string{"en", "ko"} {
		code, out := spdScreen(t, root, "lots", "--lang", lang, "--width", "80")
		if code != 3 {
			t.Errorf("exit %d", code)
		}
		checkWidth(t, "lots", out, 80)
		testutil.Golden(t, "spd/lots-one-"+lang+".txt", []byte(out))
	}
	ledger := filepath.Join(t.TempDir(), "fleet.db")
	for i, h := range []string{"h1", "h2", "h3"} {
		r := spdhost.Root(t, spdhost.Setup{Seed: uint64(i + 1), Date: twoWeeks})
		if code, _, errOut := runEnv(nil, "import", "--ledger", ledger, "--host", h, r); code != 3 {
			t.Fatalf("import %s: exit %d, %s", h, code, errOut)
		}
	}
	for _, lang := range []string{"en", "ko"} {
		code, out, errOut := runEnv(nil, "open", ledger, "lots", "--lang", lang, "--width", "80")
		if errOut != "" {
			t.Fatal(errOut)
		}
		if code != 3 {
			t.Errorf("exit %d, want the worst grade", code)
		}
		checkWidth(t, "lots of three", out, 80)
		testutil.Golden(t, "spd/lots-three-"+lang+".txt", []byte(out))
	}
}

// my spd FILE...: the public copies decode, a file that is no SPD gets a
// line, no file that decodes exits 70, files with --root are refused.
func TestSPDFiles(t *testing.T) {
	td := testutil.Testdata(t, "spd")
	var files []string
	for _, f := range []string{"illumos/ddr4-rdimm-64g-2rx4.bin", "illumos/ddr4-lrdimm-128g-3ds.bin", "illumos/ddr4-udimm-32g-2rx8.bin",
		"illumos/ddr4-sodimm-16g-2rx8.bin", "illumos/ddr5-rdimm-64g-2rx4.bin", "illumos/ddr5-rdimm-16g-1rx8.bin", "ubihazard/ddr5-udimm-16g-1rx8-a.bin",
		"ubihazard/ddr5-udimm-16g-1rx8-b.bin", "rigred/ddr3-sodimm.bin", "illumos/M386AAK40B40-CWD70.spd", "illumos/decode-dimms-4.4.txt", "README.md"} {
		files = append(files, filepath.Join(td, filepath.FromSlash(f)))
	}
	for _, lang := range []string{"en", "ko"} {
		code, out, errOut := runEnv(nil, append(append([]string{"spd"}, files...), "--lang", lang, "--width", "104")...)
		if code != 0 || errOut != "" {
			t.Errorf("exit %d, %s", code, errOut)
		}
		out = strings.ReplaceAll(out, td+string(filepath.Separator), "")
		checkWidth(t, "files", out, 104)
		testutil.Golden(t, "spd/files-"+lang+".txt", []byte(out))
	}
	if code, out, _ := runEnv(nil, "spd", filepath.Join(td, "README.md"), filepath.Join(td, "rigred", "ddr3-sodimm.bin")); code != exitNoSPD || !strings.Contains(out, "no SPD") {
		t.Errorf("no file decodes: exit %d\n%s", code, out)
	}
	if code, js, _ := runEnv(nil, "spd", files[0], "--json"); code != 0 || !strings.Contains(js, `"view": "spd-files"`) {
		t.Errorf("--json: exit %d\n%s", code, js)
	}
	if code, _, _ := runEnv(nil, "spd", files[0], "--root", td); code != 64 {
		t.Errorf("files with --root: exit %d", code)
	}
}

// The missing line and the card of a DIMM: with an SPD source that read
// nothing, the overview and the card say so; with readings, my dimm
// --detail has the line of the SPD; without an SPD source, neither.
func TestSPDOnOtherScreens(t *testing.T) {
	root, _ := spdhost.Host(t, 1)
	// No SPD source: nothing about the SPD.
	_, ov := spdScreen(t, root, "--lang", "en", "--width", "100")
	_, card := spdScreen(t, root, "dimm", "CPU0_C1", "--lang", "en", "--width", "100")
	if strings.Contains(ov, "spd") || strings.Contains(card, "SPD") {
		t.Errorf("without an SPD source:\n%s\n%s", ov, card)
	}
	// A virtual machine.
	if err := os.WriteFile(filepath.Join(root, dimmspd.ProbeFile), []byte("spd-probe 1\nvirtual VMware\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, ov = spdScreen(t, root, "--lang", "en", "--width", "100")
	if !strings.Contains(ov, "spd: not present (virtual machine)") {
		t.Errorf("overview:\n%s", ov)
	}
	_, card = spdScreen(t, root, "dimm", "CPU0_C1", "--detail", "--lang", "en", "--width", "100")
	if !strings.Contains(strings.Join(strings.Fields(card), " "), "no SPD of this DIMM (virtual machine): its manufacturing date is not on record") {
		t.Errorf("card:\n%s", card)
	}
	_, ko := spdScreen(t, root, "--lang", "ko", "--width", "100")
	if !strings.Contains(ko, "spd: 없음(가상 머신)") && !strings.Contains(ko, "가상 머신") {
		t.Errorf("Korean overview:\n%s", ko)
	}
	// Readings: the line of --detail.
	root2 := spdhost.Root(t, spdhost.Setup{Date: twoWeeks})
	_, card = spdScreen(t, root2, "dimm", "CPU0_C1", "--detail", "--lang", "en", "--width", "100")
	if !strings.Contains(card, "SPD: made 2022-W43, location code 0x06, 16 Gb dies, monolithic") {
		t.Errorf("dimm --detail:\n%s", card)
	}
	_, ov = spdScreen(t, root2, "--lang", "en", "--width", "100")
	if strings.Contains(ov, "spd:") {
		t.Errorf("an SPD that was read is no missing source:\n%s", ov)
	}
}
