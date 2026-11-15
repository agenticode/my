package main

import (
	"bytes"
	"encoding/json"
	"html"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/bundle"
	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/synth"
	"github.com/agenticode/my/internal/testledger"
	"github.com/agenticode/my/internal/testutil"
)

func TestReportCommand(t *testing.T) {
	ledger := demo(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "r.html")
	code, stdout, stderr := runEnv(fixed, "report", ledger, "-o", out)
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("exit code %d, stderr %q", code, stderr)
	}
	wantLines(t, stdout,
		"report written: "+out+" (",
		"  host r12-n034, finding: replace now",
		"  2 DIMMs described, ",
		" records in the appendix, customer report",
		"open it in a browser; printing it there gives a PDF",
	)
	page, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<!DOCTYPE html>\n<html lang=\"en\">", "<title>MemorY report: r12-n034, replace now</title>",
		"customer report", "generated 2026-11-13 09:30 UTC", `id="dimm-CPU0_C1"`, `id="s8"`, `class="fig amap"`, "</html>\n",
	} {
		if !bytes.Contains(page, []byte(want)) {
			t.Errorf("the report lacks %q", want)
		}
	}
	// The report gets the permissions new files of this user get.
	umask := syscall.Umask(0)
	syscall.Umask(umask)
	if fi, _ := os.Stat(out); fi.Mode().Perm() != fs.FileMode(0o666&^umask) {
		t.Errorf("mode of the report: %v with umask %03o", fi.Mode(), umask)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".*")); len(left) != 0 {
		t.Errorf("a partial file was left behind: %v", left)
	}

	// A file that exists is kept unless --force is given.
	code, stdout, stderr = runEnv(fixed, "report", ledger, "-o", out)
	if code != cli.ExitUsage || stdout != "" || !strings.Contains(stderr, out+" exists; --force replaces it") {
		t.Errorf("an existing file: exit code %d, stderr %q", code, stderr)
	}
	if code, _, stderr = runEnv(fixed, "report", ledger, "-o", out, "--force"); code != cli.ExitOK {
		t.Errorf("--force: exit code %d, stderr %q", code, stderr)
	}
	again, _ := os.ReadFile(out)
	if !bytes.Equal(page, again) {
		t.Error("two reports of one case, made at the same time, differ")
	}

	// "-o -" writes the report to standard output, and nothing else.
	code, stdout, stderr = runEnv(fixed, "report", ledger, "-o", "-")
	if code != cli.ExitOK || stderr != "" || stdout != string(page) {
		t.Errorf("-o -: exit code %d, stderr %q, %d bytes (the file has %d)", code, stderr, len(stdout), len(page))
	}

	// Language and audience.
	ko := filepath.Join(dir, "ko.html")
	code, stdout, _ = runEnv(fixed, "report", ledger, "-o", ko, "--lang", "ko", "--audience", "fa")
	if code != cli.ExitOK {
		t.Fatalf("--lang ko --audience fa: exit code %d", code)
	}
	wantLines(t, stdout, "리포트 저장: "+ko, "호스트 r12-n034, 소견: 즉시 교체", "FA용 리포트", "브라우저로 엽니다.")
	page, _ = os.ReadFile(ko)
	for _, want := range []string{`<html lang="ko">`, "메모리 오류 리포트", "FA용 리포트", "즉시 교체", "신뢰도 높음, 91"} {
		if !bytes.Contains(page, []byte(want)) {
			t.Errorf("the Korean report lacks %q", want)
		}
	}
	// The language follows the locale when --lang is not given.
	env := map[string]string{"MY_NOW": fixed["MY_NOW"], "LANG": "ko_KR.UTF-8"}
	if _, stdout, _ = runEnv(env, "report", ledger, "-o", "-"); !strings.Contains(stdout, `<html lang="ko">`) {
		t.Error("the report does not follow the locale")
	}

	// The same through "my open".
	viaOpen := filepath.Join(dir, "open.html")
	if code, _, stderr = runEnv(fixed, "open", ledger, "report", "-o", viaOpen); code != cli.ExitOK {
		t.Fatalf("my open FILE report: exit code %d, stderr %q", code, stderr)
	}
	a, _ := os.ReadFile(out)
	b, _ := os.ReadFile(viaOpen)
	if !bytes.Equal(a, b) {
		t.Error("my open FILE report and my report FILE differ")
	}

	// --json says what was written.
	code, stdout, _ = runEnv(fixed, "report", ledger, "-o", filepath.Join(dir, "j.html"), "--json")
	var res struct {
		Path     string `json:"path"`
		Bytes    int    `json:"bytes"`
		Host     string `json:"host"`
		Grade    string `json:"grade"`
		Audience string `json:"audience"`
		Lang     string `json:"lang"`
		CaseID   string `json:"case_id"`
		DIMMs    []struct {
			Slot, Serial, Grade string
		} `json:"dimms"`
		Records int `json:"records"`
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil || code != cli.ExitOK {
		t.Fatalf("--json: exit code %d, %v\n%s", code, err, stdout)
	}
	if res.Host != "r12-n034" || res.Grade != "replace-now" || res.Audience != "customer" || res.Lang != "en" || len(res.CaseID) != 12 ||
		len(res.DIMMs) != 2 || res.DIMMs[0].Slot != "CPU0_C1" || res.DIMMs[0].Grade != "replace-now" || res.Records < 20 || res.Bytes != len(a) {
		t.Errorf("--json: %+v (the file has %d bytes)", res, len(a))
	}
}

func TestReportMistakes(t *testing.T) {
	ledger := demo(t)
	out := filepath.Join(t.TempDir(), "r.html")
	for _, tt := range []struct {
		name string
		args []string
		code int
		want string
	}{
		{"no output named", []string{"report", ledger}, cli.ExitUsage, "name the file to write with -o, for example -o report.html"},
		{"unknown audience", []string{"report", ledger, "-o", out, "--audience", "press"}, cli.ExitUsage, "--audience press: want customer or fa"},
		{"two files", []string{"report", ledger, ledger, "-o", out}, cli.ExitUsage, "unexpected argument"},
		{"a file and a scan", []string{"report", ledger, "-o", out, "--root", t.TempDir()}, cli.ExitUsage, "--root and --ledger go with a scan of the host"},
		{"a file after my open", []string{"open", ledger, "report", ledger, "-o", out}, cli.ExitUsage, "unexpected argument"},
		{"missing file", []string{"report", filepath.Join(t.TempDir(), "none.mycase"), "-o", out}, cli.ExitFailure, "no such file"},
		{"not a case", []string{"report", "report_test.go", "-o", out}, cli.ExitFailure, "neither a MemorY ledger nor a case bundle"},
		{"unknown host", []string{"report", ledger, "-o", out, "--host", "h9"}, cli.ExitUsage, "no host named h9"},
		{"a directory to write to", []string{"report", ledger, "-o", filepath.Join(t.TempDir(), "no", "such", "dir", "r.html")}, cli.ExitUsage, filepath.Join("no", "such", "dir") + " does not exist"},
	} {
		code, stdout, stderr := run(tt.args...)
		if code != tt.code || !strings.Contains(stderr, tt.want) || stdout != "" {
			t.Errorf("%s: exit code %d, stdout %q, stderr %q; want %d and %q", tt.name, code, stdout, stderr, tt.code, tt.want)
		}
		if _, err := os.Stat(out); err == nil {
			t.Errorf("%s: a report was written", tt.name)
		}
	}
	// In Korean.
	_, _, stderr := run("report", ledger, "--lang", "ko")
	if !strings.Contains(stderr, "저장할 파일을 -o로 지정해야 함") || !strings.Contains(stderr, `"my help report"`) {
		t.Errorf("Korean usage error: %q", stderr)
	}
}

var tagRE = regexp.MustCompile(`<[^>]*>`)

// textOf returns the text of a report without its markup.
func textOf(page []byte) string {
	return html.UnescapeString(tagRE.ReplaceAllString(string(page), ""))
}

// TestReportOfABundle makes the round trip of a case: a bundle written on
// one side, a report made from it on the other.
func TestReportOfABundle(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	if err := testledger.Generate(t, "demo", 1, nil).WriteHostTree(root, synth.FlavorIntel); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "case.mycase")
	if code, _, stderr := runEnv(fixed, "bundle", "CPU1_D1", "-o", file, "--root", root, "--anonymize"); code != cli.ExitOK {
		t.Fatalf("bundle: exit code %d, stderr %q", code, stderr)
	}
	m, err := bundle.ReadManifest(file)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "r.html")
	code, stdout, stderr := runEnv(fixed, "report", file, "-o", out, "--audience", "fa")
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("exit code %d, stderr %q", code, stderr)
	}
	wantLines(t, stdout, "finding: replace now", "failure analysis report")
	page, _ := os.ReadFile(out)
	text := textOf(page)
	host := m.Hosts[0].Name
	for _, want := range []string{
		// The identity of the case is the checksum of its ledger.
		"Case ID" + m.Ledger.SHA256[:12] + " the first 12 digits of the SHA-256 checksum of the ledger in the bundle",
		"host names and network addresses were replaced before it was sent",
		// The bundle was made from a copy of a host's files.
		"import of files, 2026-11-13",
		// The subject of the case leads, though another DIMM is worse.
		"CPU1_D1 S/N 2AEB41: observe",
		"subject of the case",
		"MemorY report: " + host + ", replace now",
		"rasdaemon/mc_event",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the report of the bundle lacks %q", want)
		}
	}
	if !strings.HasPrefix(host, "host-") || strings.Contains(text, "r12-n034") {
		t.Errorf("the report names the host the bundle replaced (%s)", host)
	}
	// The original records are in the report: a row of the rasdaemon
	// database, as the adapter kept it.
	if !strings.Contains(text, `"err_type":"Corrected"`) {
		t.Error("the report holds no original record of the rasdaemon database")
	}
}

// TestReportOfAScan writes a report without a case file: the command
// scans, here a copy of a host's files.
func TestReportOfAScan(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	if err := testledger.Generate(t, "demo", 1, nil).WriteHostTree(root, synth.FlavorIntel); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "r.html")
	code, stdout, stderr := runEnv(fixed, "report", "-o", out, "--root", root)
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("exit code %d, stderr %q\n%s", code, stderr, stdout)
	}
	page, _ := os.ReadFile(out)
	for _, want := range []string{"host r12-n034", `id="dimm-CPU0_C1"`, "the first 12 digits of the identity of the ledger", "klog/offline"} {
		if !bytes.Contains(page, []byte(want)) {
			t.Errorf("the report of a scan lacks %q", want)
		}
	}
	// Nothing is left on the host: the ledger of the scan is gone.
	if left, _ := filepath.Glob(filepath.Join(os.TempDir(), "my-scan-*")); len(left) != 0 {
		t.Logf("scan directories in the temporary directory: %v (other tests may be running)", left)
	}
}

func TestReportOfOneHostAmongSeveral(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "two.db")
	s, err := store.Open(t.Context(), ledger, store.Create)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"demo", "single-bit"} {
		ds := testledger.Generate(t, name, 1, nil)
		if _, err := ingest.Run(t.Context(), s, []adapter.Adapter{ds.Adapter()}, ingest.Options{Kind: model.ScanSynthetic, Now: ds.Scenario.End}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "r.html")
	code, _, stderr := run("report", ledger, "-o", out)
	if code != cli.ExitUsage || !strings.Contains(stderr, "the ledger holds several hosts: name one with --host (r12-n034, synth-single-bit)") {
		t.Errorf("several hosts: exit code %d, stderr %q", code, stderr)
	}
	code, stdout, _ := run("report", ledger, "-o", out, "--host", "synth-single-bit")
	if code != cli.ExitOK || !strings.Contains(stdout, "host synth-single-bit, finding: observe") {
		t.Errorf("--host: exit code %d\n%s", code, stdout)
	}
	page, _ := os.ReadFile(out)
	if !bytes.Contains(page, []byte("MemorY report: synth-single-bit, observe")) || bytes.Contains(page, []byte("r12-n034")) {
		t.Error("the report of one host names the other")
	}
}

// TestReportAllFigures checks --all: figures for every DIMM with errors.
func TestReportAllFigures(t *testing.T) {
	ledger := testledger.Path(t, "system-channel", 1, "")
	count := func(args ...string) int {
		_, stdout, _ := runEnv(fixed, append([]string{"report", ledger, "-o", "-"}, args...)...)
		return strings.Count(stdout, `class="fig topo"`)
	}
	if few, all := count(), count("--all"); few != 2 || all != 14 {
		t.Errorf("%d DIMMs with figures, %d with --all; want 2 and 14", few, all)
	}
}

// TestReportFigures checks --figures: the figures of the report as SVG
// files of their own.
func TestReportFigures(t *testing.T) {
	ledger := demo(t)
	dir := t.TempDir()
	out, figs := filepath.Join(dir, "r.html"), filepath.Join(dir, "figures")
	code, stdout, stderr := runEnv(fixed, "report", ledger, "-o", out, "--figures", figs)
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("exit code %d, stderr %q", code, stderr)
	}
	wantLines(t, stdout, "report written: "+out, "figures written: "+figs+" (8 SVG files)", "host r12-n034, finding: replace now")
	names, _ := filepath.Glob(filepath.Join(figs, "*.svg"))
	if len(names) != 8 {
		t.Fatalf("%d files in %s", len(names), figs)
	}
	for _, want := range []string{"slotmap.svg", "timeline.svg", "CPU0_C1-map.svg", "CPU0_C1-topology.svg", "CPU0_C1-timeline.svg", "CPU1_D1-map.svg"} {
		data, err := os.ReadFile(filepath.Join(figs, want))
		if err != nil {
			t.Errorf("%s: %v", want, err)
			continue
		}
		if !bytes.HasPrefix(data, []byte(`<svg xmlns="http://www.w3.org/2000/svg"`)) || !bytes.HasSuffix(data, []byte("</svg>\n")) || bytes.Contains(data, []byte("href=")) {
			t.Errorf("%s is not a picture of its own", want)
		}
	}
	// A file that exists is kept unless --force is given, and then
	// nothing is written at all.
	other := filepath.Join(dir, "other.html")
	code, stdout, stderr = runEnv(fixed, "report", ledger, "-o", other, "--figures", figs)
	if code != cli.ExitUsage || stdout != "" || !strings.Contains(stderr, "slotmap.svg exists; --force replaces it") {
		t.Errorf("figures that exist: exit code %d, stderr %q", code, stderr)
	}
	if _, err := os.Stat(other); err == nil {
		t.Error("the report was written though its figures could not be")
	}
	if code, _, stderr = runEnv(fixed, "report", ledger, "-o", other, "--figures", figs, "--force"); code != cli.ExitOK {
		t.Errorf("--force: exit code %d, stderr %q", code, stderr)
	}

	// Figures without a report, and in Korean.
	only := filepath.Join(dir, "only")
	code, stdout, stderr = runEnv(fixed, "report", ledger, "--figures", only, "--lang", "ko")
	if code != cli.ExitOK || stderr != "" || !strings.Contains(stdout, "그림 저장: "+only+" (SVG 파일 8개)") || strings.Contains(stdout, "리포트 저장") {
		t.Errorf("--figures alone: exit code %d, stderr %q\n%s", code, stderr, stdout)
	}
	if data, _ := os.ReadFile(filepath.Join(only, "slotmap.svg")); !bytes.Contains(data, []byte("슬롯 맵")) || !bytes.Contains(data, []byte("즉시 교체")) {
		t.Error("the Korean slot map is not in Korean")
	}

	// --json names the files.
	code, stdout, _ = runEnv(fixed, "report", ledger, "--figures", filepath.Join(dir, "j"), "--json")
	var res struct {
		Path    string   `json:"path"`
		Figures []string `json:"figures"`
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil || code != cli.ExitOK || len(res.Figures) != 8 || res.Path != "" || !strings.HasSuffix(res.Figures[0], "slotmap.svg") {
		t.Errorf("--json: exit code %d, %v, %+v", code, err, res)
	}

	// A file where the directory should be.
	code, _, stderr = runEnv(fixed, "report", ledger, "--figures", out)
	if code != cli.ExitUsage || !strings.Contains(stderr, "not a directory") {
		t.Errorf("--figures FILE: exit code %d, stderr %q", code, stderr)
	}
	// Neither a report nor figures.
	if code, _, stderr = run("report", ledger); code != cli.ExitUsage || !strings.Contains(stderr, "name the file to write with -o") {
		t.Errorf("nothing to write: exit code %d, stderr %q", code, stderr)
	}
}

// TestErrorsThatNameNoDIMM runs the commands on a capture of a real guest
// without a memory controller driver: 7 corrected and 3 uncorrected machine
// checks that name no DIMM. No finding covers them, the grade of the host
// stays "ok", and every line that names that grade says what it leaves out:
// the overview, the line of a bundle (which read "CE 0, UE 0, finding: ok"
// on the lab host) and the line and the summary of a report.
func TestErrorsThatNameNoDIMM(t *testing.T) {
	root := filepath.Join(testutil.Testdata(t), "real", "7.3.0-mce")
	const loose = "CE 7, UE 3 tied to no DIMM are in no finding"

	code, out, stderr := run("--root", root, "--color", "never")
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("scan: exit code %d, stderr %q", code, stderr)
	}
	wantLines(t, out, "  2026-11-15  CE 7, UE 3  observation grade B", "host finding: ok; "+loose,
		" 10 errors of this host are tied to no DIMM", " 5 page offlines of this host are tied to no DIMM")

	code, out, _ = run("--root", root, "--json")
	var scan struct {
		Grade    string `json:"grade"`
		ExitCode int    `json:"exit_code"`
		Data     struct {
			Unattributed   int64 `json:"unattributed"`
			UnattributedUE int64 `json:"unattributed_ue"`
			LoosePages     int   `json:"loose_pages"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &scan); err != nil || code != cli.ExitOK {
		t.Fatalf("scan --json: exit code %d, %v", code, err)
	}
	if scan.Grade != "ok" || scan.ExitCode != 0 || scan.Data.Unattributed != 10 || scan.Data.UnattributedUE != 3 || scan.Data.LoosePages != 5 {
		t.Errorf("scan --json: %+v", scan)
	}

	dir := t.TempDir()
	file := filepath.Join(dir, "case.mycase")
	code, out, stderr = run("bundle", "--root", root, "-o", file)
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("bundle: exit code %d, stderr %q", code, stderr)
	}
	wantLines(t, out, "  host lab-guest: 1 DIMM, CE 7, UE 3, finding: ok; "+loose)

	page := filepath.Join(dir, "r.html")
	code, out, stderr = run("report", file, "-o", page)
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("report: exit code %d, stderr %q", code, stderr)
	}
	wantLines(t, out, "  host lab-guest, finding: ok; "+loose)
	b, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(strings.Fields(textOf(b)), " ")
	wantLines(t, text, "10 errors of this host (CE 7, UE 3) are tied to no DIMM, and no finding covers them. 5 page offlines of this host are tied to no DIMM.",
		"No DIMM of this host has errors on record.", "2026-11-15, 1 boot", "7 corrected and 3 uncorrected errors on 2026-11-15")
	if strings.Contains(text, "2026-11-15 to 2026-11-15, 1 boot") {
		t.Error("the report names a period of one day by two days")
	}

	code, out, _ = run("report", file, "-o", page, "--force", "--lang", "ko")
	if code != cli.ExitOK {
		t.Fatalf("report --lang ko: exit code %d", code)
	}
	wantLines(t, out, "  호스트 lab-guest, 소견: 정상. 어느 DIMM에도 묶이지 않은 CE 7건, UE 3건은 소견에 들어가지 않음")
}
