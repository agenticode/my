package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/store"
)

// synthLedger writes the ledger of a built-in scenario and returns its path.
func synthLedger(t *testing.T, scenario string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), scenario+".db")
	if code, _, errOut := run("synth", "-scenario", scenario, "-seed", "1", "-ledger", path); code != cli.ExitOK {
		t.Fatalf("synth %s: exit code %d: %s", scenario, code, errOut)
	}
	return path
}

func wantLines(t *testing.T, out string, lines ...string) {
	t.Helper()
	for _, l := range lines {
		if !strings.Contains(out, l) {
			t.Errorf("output lacks %q:\n%s", l, out)
		}
	}
}

func TestAnalyzeDemo(t *testing.T) {
	ledger := synthLedger(t, "demo")
	before, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatal(err)
	}

	code, out, errOut := run("analyze", "-ledger", ledger)
	if code != 3 || errOut != "" {
		t.Fatalf("exit code %d, stderr %q", code, errOut)
	}
	wantLines(t, out,
		"host r12-n034, as of 2026-11-13 00:00 UTC, engine 1",
		"host finding: replace now",
		"CPU0_C1  S/N 3B9C56  CE 1,231  UE 1  row",
		"  finding   replace now (finding, confidence high, 91)  R-UE-01 R-ROW-03 R-REP-02",
		"  evidence  [R-UE-01] 1 uncorrected error, the latest on 2026-11-12 03:14 UTC at rank0 dev7 bg0 bank3 row 0x1a2f col 0x238",
		"            [R-REP-02] 764 errors at rank0 dev7 bg0 bank3 row 0x1a2f after the hard PPR of 2026-10-28",
		"  checks    open: no other DIMM on this channel: there is nothing to compare with",
		"            open: no slot move or reseat on record: a cause in the board is not ruled out",
		"            supports: the other 15 DIMMs of this host have no errors outside single confined places: a cause in the board is unlikely",
		"  missing   no ECS counters",
		"  next      Replace the DIMM. Attach a case bundle of this DIMM to the return.",
		"CPU1_D1  S/N 2AEB41  CE 53  UE 0  single-bit",
		"  finding   observe (finding, confidence medium, 65)  R-SOFT-01",
		"14 DIMMs without errors",
	)
	// A grade is a finding: the output never calls it a recommendation.
	if strings.Contains(strings.ToLower(out), "recommend") {
		t.Errorf("the output speaks of a recommendation:\n%s", out)
	}

	// In Korean.
	code, out, _ = run("analyze", "-ledger", ledger, "-lang", "ko")
	if code != 3 {
		t.Fatalf("ko: exit code %d", code)
	}
	wantLines(t, out,
		"호스트 r12-n034, 기준 시각 2026-11-13 00:00 UTC, 엔진 1",
		"호스트 소견: 즉시 교체",
		"  판정  즉시 교체 (소견, 신뢰도 높음, 91)  R-UE-01 R-ROW-03 R-REP-02",
		"  근거  [R-UE-01] UE 1건, 최근 2026-11-12 03:14 UTC, rank0 dev7 bg0 bank3 row 0x1a2f col 0x238",
		"        [R-REP-02] 2026-10-28 hPPR 뒤 rank0 dev7 bg0 bank3 row 0x1a2f에서 764건 재발",
		"  반례  미확인: 이 채널에 다른 DIMM 없음: 비교 대상 없음",
		"        미확인: 슬롯 교차 시험이나 reseat 기록 없음: 보드 원인을 배제하지 못함",
		"        뒷받침: 이 호스트의 다른 DIMM 15개에는 한 위치에 국한되지 않은 오류 없음: 보드 원인 가능성 낮음",
		"  부족  ECS 카운터 없음",
		"  다음  DIMM 교체. 이 DIMM의 케이스 번들을 반품에 첨부.",
		"오류 없는 DIMM 14개",
	)
	for _, w := range []string{"권고", "권장", "추천"} {
		if strings.Contains(out, w) {
			t.Errorf("the Korean output contains %q", w)
		}
	}

	// Every DIMM with -all.
	_, out, _ = run("analyze", "-ledger", ledger, "-all")
	if n := strings.Count(out, "  finding   ok (finding, confidence high, 85)"); n != 14 || strings.Contains(out, "without errors") {
		t.Errorf("-all lists %d DIMMs without errors:\n%s", n, out)
	}

	// As of an earlier time: before the repair and the uncorrected error.
	code, out, _ = run("analyze", "-ledger", ledger, "-as-of", "2026-10-25")
	if code != 2 {
		t.Errorf("as of 25 October: exit code %d\n%s", code, out)
	}
	wantLines(t, out, "as of 2026-10-25 00:00 UTC", "host finding: consider replacement", "R-ROW-03")
	if code, _, _ := run("analyze", "-ledger", ledger, "-as-of", "2026-09-01T12:00:00Z"); code != 1 {
		t.Errorf("as of 1 September: exit code %d, want 1 for the weak cell of the neighbour", code)
	}

	// Thresholds from flags and from a file change the finding.
	if code, _, _ := run("analyze", "-ledger", ledger, "-as-of", "2026-10-25", "-set", "rule.row.min_errors=100000"); code != 1 {
		t.Errorf("with a row threshold out of reach: exit code %d", code)
	}
	conf := filepath.Join(t.TempDir(), "my.conf")
	if err := os.WriteFile(conf, []byte("# a row fault needs more errors here\nrule.row.min_errors = 100000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := run("analyze", "-ledger", ledger, "-as-of", "2026-10-25", "-config", conf); code != 1 {
		t.Errorf("with a configuration file: exit code %d", code)
	}
	if code, _, _ := run("analyze", "-ledger", ledger, "-as-of", "2026-10-25", "-config", conf, "-set", "rule.row.min_errors=10"); code != 2 {
		t.Errorf("a setting on the command line wins over the file: exit code %d", code)
	}

	// The ledger was only read.
	after, err := os.ReadFile(ledger)
	if err != nil || string(after) != string(before) {
		t.Errorf("analyze changed the ledger (%v)", err)
	}
}

func TestAnalyzeJSON(t *testing.T) {
	ledger := synthLedger(t, "demo")
	code, out, errOut := run("analyze", "-ledger", ledger, "-json")
	if code != 3 {
		t.Fatalf("exit code %d, stderr %q", code, errOut)
	}
	var res struct {
		EngineVersion string             `json:"engine_version"`
		Config        map[string]float64 `json:"config"`
		Hosts         []struct {
			Host  string `json:"host"`
			Grade string `json:"grade"`
			DIMMs []struct {
				Slot    string `json:"slot"`
				Present bool   `json:"present"`
				Verdict struct {
					Grade    string   `json:"grade"`
					Label    string   `json:"label"`
					Rules    []string `json:"rules"`
					Evidence []struct {
						Rule    string         `json:"rule"`
						Code    string         `json:"code"`
						Args    map[string]any `json:"args"`
						Text    string         `json:"text"`
						Records []struct {
							Kind   string `json:"kind"`
							ID     int64  `json:"id"`
							RawRef string `json:"raw_ref"`
						} `json:"records"`
					} `json:"evidence"`
					Confidence int `json:"confidence"`
				} `json:"verdict"`
				Faults []struct {
					Mode string `json:"mode"`
				} `json:"faults"`
			} `json:"dimms"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("output is not JSON: %v\n%.400s", err, out)
	}
	if res.EngineVersion != "1" || res.Config["trend.recent_days"] != 7 || len(res.Hosts) != 1 || res.Hosts[0].Grade != "replace-now" || len(res.Hosts[0].DIMMs) != 16 {
		t.Fatalf("result = %+v", res)
	}
	d := res.Hosts[0].DIMMs[0]
	v := d.Verdict
	if d.Slot != "CPU0_C1" || !d.Present || v.Grade != "replace-now" || v.Label != "finding" || strings.Join(v.Rules, " ") != "R-UE-01 R-ROW-03 R-REP-02" ||
		d.Faults[0].Mode != "row" || v.Confidence != 91 {
		t.Errorf("first DIMM = %+v", d)
	}
	e := v.Evidence[0]
	if e.Rule != "R-UE-01" || e.Code != "ue.located" || e.Args["n"] != float64(1) || len(e.Records) != 1 || e.Records[0].Kind != "event" ||
		!strings.HasPrefix(e.Records[0].RawRef, "synth:demo#event") {
		t.Errorf("evidence = %+v", e)
	}
}

func TestAnalyzeExitCodes(t *testing.T) {
	for scenario, want := range map[string]int{"single-bit": 1, "column": 2, "device": 3, "system-channel": 4, "system-slot": 4, "repaired": 1,
		"reseated": 4, "fw-disabled": 3, "repair-failed": 3, "offline-growing": 2} {
		code, out, errOut := run("analyze", "-ledger", synthLedger(t, scenario))
		if code != want {
			t.Errorf("%s: exit code %d, want %d\n%s%s", scenario, code, want, out, errOut)
		}
	}
	// A ledger without records: nothing to find.
	empty := filepath.Join(t.TempDir(), "empty.db")
	s, err := store.Open(context.Background(), empty, store.Create)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	code, out, _ := run("analyze", "-ledger", empty)
	if code != 0 || strings.TrimSpace(out) != "no host in the ledger" {
		t.Errorf("empty ledger: exit code %d, output %q", code, out)
	}
}

func TestAnalyzeSave(t *testing.T) {
	ledger := synthLedger(t, "system-channel")
	code, out, errOut := run("analyze", "-ledger", ledger, "-save")
	if code != 4 {
		t.Fatalf("exit code %d, stderr %q", code, errOut)
	}
	wantLines(t, out, "faults and findings stored in "+ledger)
	s, err := store.Open(context.Background(), ledger, store.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	verdicts, _ := s.Verdicts(context.Background(), "synth-system-channel")
	faults, _ := s.Faults(context.Background(), "synth-system-channel")
	hold, system := 0, 0
	for _, v := range verdicts {
		if v.Grade == "hold-replacement" && v.Rules[0] == "R-SYS-01" {
			hold++
		}
	}
	for _, f := range faults {
		if f.Mode == "system-issue" {
			system++
		}
	}
	if len(verdicts) != 32 || hold != 2 || system != 2 {
		t.Errorf("%d verdicts (%d hold), %d system issue faults of %d", len(verdicts), hold, system, len(faults))
	}
}

func TestAnalyzeUsage(t *testing.T) {
	ledger := synthLedger(t, "single-bit")
	for _, tt := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"analyze"}, cli.ExitUsage, "give the ledger with -ledger"},
		{[]string{"analyze", "-ledger", ledger, "extra"}, cli.ExitUsage, `unexpected argument "extra"`},
		{[]string{"analyze", "-ledger", ledger, "-lang", "fr"}, cli.ExitUsage, `unknown language "fr"`},
		{[]string{"analyze", "-ledger", ledger, "-set", "trend.rise_ratoi=3"}, cli.ExitUsage, `unknown threshold "trend.rise_ratoi"`},
		{[]string{"analyze", "-ledger", ledger, "-set", "trend.rise_ratio=0"}, cli.ExitUsage, "trend.rise_ratio: 0 is below the minimum of 1"},
		{[]string{"analyze", "-ledger", ledger, "-config", filepath.Join(t.TempDir(), "absent.conf")}, cli.ExitUsage, "configuration file"},
		{[]string{"analyze", "-ledger", ledger, "-as-of", "yesterday"}, cli.ExitUsage, "-as-of yesterday: want a date"},
		{[]string{"analyze", "-no-such-flag"}, cli.ExitUsage, "flag provided but not defined"},
		{[]string{"analyze", "-ledger", filepath.Join(t.TempDir(), "absent.db")}, cli.ExitFailure, "absent.db"},
		{[]string{"analyze", "-ledger", ledger, "-host", "no-such-host"}, cli.ExitFailure, "no host named no-such-host in the ledger"},
	} {
		code, out, errOut := run(tt.args...)
		if code != tt.code || !strings.Contains(errOut, tt.want) || out != "" {
			t.Errorf("%v: exit code %d, stdout %q, stderr %q; want %d and %q", tt.args[1:], code, out, errOut, tt.code, tt.want)
		}
	}
	// A file that is not a ledger.
	notLedger := filepath.Join(t.TempDir(), "notes.txt")
	os.WriteFile(notLedger, []byte("not a ledger"), 0o644)
	if code, _, errOut := run("analyze", "-ledger", notLedger); code != cli.ExitFailure || !strings.Contains(errOut, "not a MemorY ledger") {
		t.Errorf("not a ledger: exit code %d, stderr %q", code, errOut)
	}
	if code, _, errOut := run("analyze", "-h"); code != cli.ExitOK || !strings.Contains(errOut, "Exit code: 0 ok, 1 observe, 2 consider replacement, 3 replace now, 4 hold replacement") {
		t.Errorf("-h: exit code %d, stderr %q", code, errOut)
	}
	// The command is for development and stays out of the command list.
	if _, usage, _ := run("help"); strings.Contains(usage, "analyze") {
		t.Errorf("the command list shows the hidden analyze command:\n%s", usage)
	}
	// One host of the ledger by name.
	if code, out, _ := run("analyze", "-ledger", ledger, "-host", "synth-single-bit"); code != 1 || !strings.Contains(out, "host synth-single-bit") {
		t.Errorf("-host: exit code %d\n%s", code, out)
	}
}

func TestAnalyzeListsRulesAndThresholds(t *testing.T) {
	code, out, _ := run("analyze", "-rules")
	if code != cli.ExitOK || strings.Count(out, "\n") != 13 {
		t.Fatalf("exit code %d, %d lines:\n%s", code, strings.Count(out, "\n"), out)
	}
	wantLines(t, out,
		"R-UE-01     replace now           an uncorrected error on this DIMM",
		"R-ROW-03    consider replacement",
		"R-SOFT-01   observe",
		"R-MAP-01    hold replacement      the slot names of EDAC, SMBIOS and the BMC disagree",
	)
	_, out, _ = run("analyze", "-rules", "-lang", "ko")
	wantLines(t, out, "R-UE-01", "즉시 교체", "이 DIMM에서 UE 발생", "교체 보류", "reseat 뒤 오류 소멸")

	code, out, _ = run("analyze", "-thresholds", "-set", "storm.count=250")
	if code != cli.ExitOK {
		t.Fatalf("exit code %d", code)
	}
	wantLines(t, out,
		"trend.recent_days            = 7\n    length of the recent window in days",
		"storm.count                  = 250 (default 100)",
		"rule.row.min_errors          = 10\n    R-ROW-03:",
	)
}

func TestDisplayWidth(t *testing.T) {
	for s, want := range map[string]int{"": 0, "finding": 7, "판정": 4, "근거 A": 6, "ＡＢ": 4} {
		if got := displayWidth(s); got != want {
			t.Errorf("displayWidth(%q) = %d, want %d", s, got, want)
		}
	}
}
