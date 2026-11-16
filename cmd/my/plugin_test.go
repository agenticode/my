package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/buildinfo"
	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/plugin"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/testutil"
)

// The test binary plays the vendor analyzer: started with this variable
// set, it answers a request the way the value says (see TestMain). The
// tests then run the commands with --plugin pointing at the test binary,
// so that the whole path is exercised without an interpreter.
const fakeAnalyzerEnv = "MY_TEST_ANALYZER"

// fakeAnalyzer is the analyzer of the command tests. In the mode "pairs"
// it answers like examples/plugin/analyzer.py: rows 2k and 2k+1 are a
// pair and a row has no other neighbour.
func fakeAnalyzer(mode string) int {
	in, err := io.ReadAll(os.Stdin)
	if err != nil {
		return 9
	}
	var req plugin.Request
	if err := json.Unmarshal(in, &req); err != nil {
		fmt.Fprintln(os.Stderr, "request is not JSON:", err)
		return 9
	}
	resp := plugin.Response{Contract: plugin.Contract, Analyzer: plugin.Analyzer{Name: "test analyzer", Version: "0.1", Vendor: "Example Memory"}}
	switch mode {
	case "crash":
		fmt.Fprintln(os.Stderr, "license file not found: /opt/vendor/license.key")
		return 3
	case "slow":
		time.Sleep(30 * time.Second)
	case "want-more":
		resp.Analyzer.PreviousLogs = 5000
	}
	for _, d := range req.Dimms {
		rd := plugin.ResponseDIMM{DimmId: d.DimmId, Baseline: &plugin.Baseline{Percentile: 97.5}}
		type bank struct{ rank, dev, bg, bank int }
		rows := map[bank]map[int]bool{}
		at := func(p *int) int {
			if p == nil {
				return -1
			}
			return *p
		}
		for _, r := range append(d.PreviousLogs, d.NewLog) {
			if r.Row == nil {
				continue
			}
			b := bank{at(r.Rank), at(r.DeviceId), at(r.BankGroup), at(r.Bank)}
			if rows[b] == nil {
				rows[b] = map[int]bool{}
			}
			rows[b][*r.Row], rows[b][*r.Row^1] = true, true
		}
		n := 0
		if mode != "level2-only" {
			ptr := func(v int) *int {
				if v < 0 {
					return nil
				}
				return &v
			}
			for b, set := range rows {
				for row := range set {
					rd.AdjacentRows = append(rd.AdjacentRows, plugin.AdjacentRows{Rank: ptr(b.rank), DeviceId: ptr(b.dev), BankGroup: ptr(b.bg), Bank: ptr(b.bank), Row: row, Neighbors: []int{row ^ 1}})
					n++
				}
			}
		}
		rd.Level2 = &plugin.Level2{
			Lines: []plugin.Line{{Label: "records analysed", Value: fmt.Sprint(len(d.PreviousLogs) + 1)}, {Label: "impacted bit lines", Value: "BL 412, BL 413"}},
			Notes: []string{"Values of a test analyzer."},
		}
		resp.Dimms = append(resp.Dimms, rd)
	}
	if err := json.NewEncoder(os.Stdout).Encode(resp); err != nil {
		return 9
	}
	return 0
}

// analyzer points --plugin at the test binary in a mode.
func analyzer(t *testing.T, mode string) string {
	t.Helper()
	t.Setenv(fakeAnalyzerEnv, mode)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}

// A vendor analyzer that knows which rows are neighbours changes which
// errors count as one fault, and gives the baseline its third layer. The
// screen says that an analyzer took part.
func TestPluginReachesTheEngine(t *testing.T) {
	ledger := demo(t)
	_, plain, _ := run("open", ledger, "dimm", "CPU0_C1", "--detail")
	exe := analyzer(t, "pairs")
	code, out, errOut := run("open", ledger, "dimm", "CPU0_C1", "--detail", "--plugin", exe)
	if code != 3 || errOut != "" {
		t.Fatalf("exit code %d, stderr %q\n%s", code, errOut, out)
	}
	if !strings.HasPrefix(out, "vendor analyzer test analyzer 0.1 (Example Memory): analyses of 2 DIMMs\n") {
		t.Errorf("the screen does not start with the analyzer:\n%s", out)
	}
	// Without the analyzer rows 0x1a2f and 0x1a30 are neighbours by their
	// numbers and one fault. The analyzer puts them into different pairs.
	for _, want := range []string{
		"row fault at rank0 dev7 bg0 bank3 row 0x1a30:",
		"it now covers 9 columns in 1 row",
		"vendor reference for the part number: percentile 98",
	} {
		if !strings.Contains(out, want) || strings.Contains(plain, want) {
			t.Errorf("%q: with the analyzer %v, without %v", want, strings.Contains(out, want), strings.Contains(plain, want))
		}
	}
	if !strings.Contains(plain, "it now covers 9 columns in 2 rows") || strings.Contains(plain, "vendor analyzer") {
		t.Errorf("the screen without an analyzer:\n%s", plain)
	}

	// As JSON the analyzer is named in the envelope.
	_, out, _ = run("open", ledger, "why", "CPU0_C1", "--plugin", exe, "--json")
	var e struct {
		Analyzer *analyzerInfo `json:"analyzer"`
		Grade    string        `json:"grade"`
	}
	if err := json.Unmarshal([]byte(out), &e); err != nil || e.Analyzer == nil || e.Analyzer.Name != "test analyzer" || e.Analyzer.Version != "0.1" ||
		e.Analyzer.Vendor != "Example Memory" || len(e.Analyzer.Notes) != 0 || e.Grade != "replace-now" {
		t.Errorf("envelope: %+v, %v", e.Analyzer, err)
	}
	_, out, _ = run("open", ledger, "why", "CPU0_C1", "--json")
	if strings.Contains(out, `"analyzer"`) {
		t.Errorf("an analyzer in the envelope of a run without one")
	}

	// An analyzer that only adds Level 2 output leaves the findings alone.
	exe = analyzer(t, "level2-only")
	_, out, _ = run("open", ledger, "dimm", "CPU0_C1", "--detail", "--plugin", exe)
	if !strings.Contains(out, "it now covers 9 columns in 2 rows") || !strings.HasPrefix(out, "vendor analyzer test analyzer") {
		t.Errorf("an analyzer without adjacency changed the faults:\n%s", out)
	}

	// The Korean line.
	_, out, _ = run("open", ledger, "why", "CPU0_C1", "--plugin", exe, "--lang", "ko")
	if !strings.HasPrefix(out, "벤더 분석기 test analyzer 0.1 (Example Memory): DIMM 2개 분석\n") {
		t.Errorf("Korean:\n%s", out)
	}
}

// Level 2 output is for failure analysis: the FA report shows it, the
// customer report does not, and no screen does.
func TestPluginLevel2IsForFailureAnalysis(t *testing.T) {
	ledger := demo(t)
	exe := analyzer(t, "pairs")
	dir := t.TempDir()
	read := func(audience string) string {
		out := filepath.Join(dir, audience+".html")
		code, _, errOut := runEnv(fixed, "report", ledger, "-o", out, "--audience", audience, "--plugin", exe, "--plugin-logs", "10")
		if code != cli.ExitOK || errOut != "" {
			t.Fatalf("%s report: exit code %d, stderr %q", audience, code, errOut)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	fa, customer := read("fa"), read("customer")
	for _, want := range []string{"test analyzer 0.1", "impacted bit lines", "BL 412, BL 413", "Values of a test analyzer."} {
		if !strings.Contains(fa, want) {
			t.Errorf("the FA report lacks %q", want)
		}
		if strings.Contains(customer, want) {
			t.Errorf("the customer report holds %q", want)
		}
	}
	// --plugin-logs 10: the analyzer saw the newest record and ten before it.
	if !strings.Contains(fa, ">11<") && !strings.Contains(fa, "11</") {
		t.Errorf("the FA report does not show the 11 records the analyzer got")
	}
	// An export names the analyzer that took part, and holds none of its
	// Level 2 output.
	_, out, _ := run("open", ledger, "export", "fmfm", "--plugin", exe)
	if !strings.Contains(out, `"vendor_analyzer": {`) || !strings.Contains(out, `"name": "test analyzer"`) {
		t.Errorf("the export does not name the analyzer:\n%.600s", out)
	}
	if _, plain, _ := run("open", ledger, "export", "fmfm"); strings.Contains(plain, "vendor_analyzer") {
		t.Errorf("an export without an analyzer names one")
	}
	for _, screen := range [][]string{{"dimm", "CPU0_C1"}, {"why", "CPU0_C1"}, {"export", "fmfm"}} {
		_, out, _ := run(append(append([]string{"open", ledger}, screen...), "--plugin", exe)...)
		if strings.Contains(out, "impacted bit lines") || strings.Contains(out, "BL 412") {
			t.Errorf("%v shows Level 2 output", screen)
		}
	}
}

// A run that asked for an analyzer and cannot have it stops with the
// reason and prints nothing else.
func TestPluginFailures(t *testing.T) {
	ledger := demo(t)
	for _, tt := range []struct {
		name string
		mode string
		args []string
		code int
		want []string
	}{
		{"the analyzer fails", "crash", nil, cli.ExitFailure,
			[]string{"failed (exit status 3): license file not found: /opt/vendor/license.key", "Nothing was analysed. Without --plugin the findings are those of the open core."}},
		{"the analyzer takes too long", "slow", []string{"--plugin-timeout", "400ms"}, cli.ExitFailure,
			[]string{"no answer within 400ms; --plugin-timeout sets the limit", "Nothing was analysed."}},
		{"a timeout that is no time", "pairs", []string{"--plugin-timeout", "soon"}, cli.ExitUsage,
			[]string{"--plugin-timeout soon: want a time of at least 100ms, such as 5s or 2m"}},
		{"a timeout that is too short", "pairs", []string{"--plugin-timeout", "1ms"}, cli.ExitUsage, []string{"--plugin-timeout 1ms: want a time of at least 100ms"}},
		{"a negative number of records", "pairs", []string{"--plugin-logs", "-1"}, cli.ExitUsage, []string{"--plugin-logs -1: want a number of records"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			exe := analyzer(t, tt.mode)
			code, out, errOut := run(append([]string{"open", ledger, "dimm", "CPU0_C1", "--plugin", exe}, tt.args...)...)
			if code != tt.code || out != "" {
				t.Errorf("exit code %d, stdout %q, stderr %q", code, out, errOut)
			}
			for _, want := range tt.want {
				if !strings.Contains(errOut, want) {
					t.Errorf("stderr lacks %q:\n%s", want, errOut)
				}
			}
		})
	}

	// The options of an analyzer without an analyzer, and a program that
	// is not there.
	code, out, errOut := run("open", ledger, "dimm", "CPU0_C1", "--plugin-timeout", "5s")
	if code != cli.ExitUsage || out != "" || !strings.Contains(errOut, "--plugin-timeout and --plugin-logs go with --plugin") {
		t.Errorf("--plugin-timeout alone: exit code %d, stderr %q", code, errOut)
	}
	missing := filepath.Join(t.TempDir(), "analyzer")
	code, out, errOut = run("open", ledger, "dimm", "CPU0_C1", "--plugin", missing)
	if code != cli.ExitFailure || out != "" || !strings.Contains(errOut, "vendor analyzer "+missing+": cannot be run") {
		t.Errorf("a missing program: exit code %d, stderr %q", code, errOut)
	}
	// Korean: the reason is the analyzer's, what to do about it is ours.
	exe := analyzer(t, "crash")
	_, _, errOut = run("open", ledger, "dimm", "CPU0_C1", "--plugin", exe, "--lang", "ko")
	if !strings.Contains(errOut, "분석하지 않았습니다. --plugin 없이 실행하면 공개 코어의 소견이 나옵니다.") {
		t.Errorf("Korean failure: %q", errOut)
	}
}

// An analyzer that works with more previous records than it got says so,
// and the screen passes it on.
func TestPluginAsksForMoreRecords(t *testing.T) {
	exe := analyzer(t, "want-more")
	_, out, _ := run("open", demo(t), "why", "CPU0_C1", "--plugin", exe, "--plugin-logs", "100")
	// The note is wrapped: compare it without the line breaks.
	if flat := strings.Join(strings.Fields(out), " "); !strings.Contains(flat, "analyzer: the analyzer works with 5000 previous records per DIMM; 100 were sent, and 1 DIMMs have more on record (--plugin-logs sends more)") {
		t.Errorf("the screen does not pass the note on:\n%s", out)
	}
}

// An import runs the analyzer too.
func TestPluginOnImport(t *testing.T) {
	exe := analyzer(t, "pairs")
	root := filepath.Join(t.TempDir(), "root")
	if code, _, errOut := run("synth", "-scenario", "demo", "-out", root); code != cli.ExitOK {
		t.Fatalf("synth: %s", errOut)
	}
	code, out, errOut := run("import", root, "--plugin", exe)
	if code != 3 || errOut != "" || !strings.Contains(out, "vendor analyzer test analyzer 0.1 (Example Memory): analyses of 2 DIMMs") {
		t.Errorf("exit code %d, stderr %q\n%s", code, errOut, out)
	}
}

// python returns the Python interpreter of this machine, or skips the test.
func python(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3 on this machine: the example analyzer is a Python program")
	}
	return p
}

// The example analyzer of the repository speaks the contract.
func TestExampleAnalyzer(t *testing.T) {
	python(t)
	example := filepath.Join(testutil.RepoRoot(t), "examples", "plugin", "analyzer.py")
	code, out, errOut := run("open", demo(t), "dimm", "CPU0_C1", "--detail", "--plugin", example)
	if code != 3 || errOut != "" {
		t.Fatalf("exit code %d, stderr %q\n%s", code, errOut, out)
	}
	for _, want := range []string{
		"vendor analyzer example-analyzer 1.0 (Example Memory): analyses of 2 DIMMs",
		"row fault at rank0 dev7 bg0 bank3 row 0x1a30:",
		"vendor reference for the part number: percentile 99",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the screen lacks %q:\n%s", want, out)
		}
	}
	// It asks for 1,000 previous records: with fewer it says so.
	_, out, _ = run("open", demo(t), "why", "CPU0_C1", "--plugin", example, "--plugin-logs", "50")
	if flat := strings.Join(strings.Fields(out), " "); !strings.Contains(flat, "analyzer: the analyzer works with 1000 previous records per DIMM; 50 were sent") {
		t.Errorf("the note about previous records:\n%s", out)
	}
}

// examples/plugin holds a request as the tool sends it and the response
// of the example analyzer to it. Both are kept true here: "make golden"
// writes them anew.
func TestExampleRequestAndResponse(t *testing.T) {
	dir := filepath.Join(testutil.RepoRoot(t), "examples", "plugin")
	s, err := store.Open(t.Context(), demo(t), store.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at, _ := parseTime(fixed["MY_NOW"])
	req, err := plugin.BuildRequest(t.Context(), s, plugin.RequestOptions{Tool: "MemorY", Version: buildinfo.Version, Now: at, PreviousLogs: 2})
	if err != nil {
		t.Fatal(err)
	}
	request, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	request = append(request, '\n')
	same(t, filepath.Join(dir, "request.json"), request)

	cmd := exec.Command(python(t), filepath.Join(dir, "analyzer.py"))
	cmd.Stdin = bytes.NewReader(request)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		t.Fatalf("the example analyzer: %v: %s", err, errOut.String())
	}
	same(t, filepath.Join(dir, "response.json"), out.Bytes())
}

// same compares a file of the repository with what it should hold, or
// writes it when the golden files are being rewritten.
func same(t *testing.T, path string, want []byte) {
	t.Helper()
	if os.Getenv(testutil.UpdateGoldenEnv) != "" {
		if err := os.WriteFile(path, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run \"make golden\" to write it)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is not what the tool gives now; run \"make golden\" and review the change", path)
	}
}
