package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/testledger"
	"github.com/agenticode/my/internal/testutil"
)

// gpuLedger is a ledger with a GPU in every state of the repair budget.
func gpuLedger(t *testing.T) string { return testledger.GPUPath(t) }

func TestGPUCommand(t *testing.T) {
	ledger := gpuLedger(t)

	// Every GPU: two of them with a failed remapping.
	code, out, errOut := run("open", ledger, "gpu")
	if code != 3 || errOut != "" {
		t.Fatalf("exit code %d, stderr %q\n%s", code, errOut, out)
	}
	wantLines(t, out,
		"host gpu-01  8 GPUs with memory records  Xid records of the 30 days to 2026-11-14",
		"GPU finding: replace now",
		"   0  ..000412  48x1 63x1 64x1  0 / 2        no       yes     repair failed   replace now R-REP-01",
		"   2  ..001201  -               0 / 3        yes      no      repair pending  -",
		" GPU 3  |max 637 ",
		" · max   ░ high   ▒ partial   ▓ low   █ none",
	)
	// The command prints what the renderer draws.
	if want := string(testutil.ReadTestdata(t, "..", "internal", "term", "testdata", "gpu.en.txt")); out != want {
		t.Errorf("the command and the renderer differ:\n%s", out)
	}

	// One GPU, by the names a reader has for it. The exit code is the
	// grade of the GPU that is shown.
	for _, tt := range []struct {
		ref  string
		code int
		want string
	}{
		{"0", 3, "   Xid       2026-11-12 09:30 UTC 64"},
		{"..0977", 3, "the driver reports a row remapping failure as of 2026-11-13 09:30 UTC"},
		{"2", 0, "no rule fired: only a failed row remapping has one (R-REP-01)"},
		{"0000:d8:00.0", 0, "Plan a replacement: a bank without a spare row cannot take another uncorrectable error."},
		{"GPU-11111111-2222-3333-4444-555555555506", 0, "   pages     3 retired after single bit errors, 1 after double bit errors"},
	} {
		code, out, errOut := run("open", ledger, "gpu", tt.ref)
		if code != tt.code || errOut != "" || !strings.Contains(out, tt.want) {
			t.Errorf("gpu %s: exit code %d, stderr %q, want %q in\n%s", tt.ref, code, errOut, tt.want, out)
		}
	}

	// Korean, ASCII, a window of one day.
	_, out, _ = run("open", ledger, "gpu", "--lang", "ko", "--ascii")
	wantLines(t, out, "GPU 소견: 즉시 교체", "리페어 대기", " . max   : high   + partial   * low   # none")
	_, out, _ = run("open", ledger, "gpu", "--days", "1")
	if strings.Contains(out, "48x1") || !strings.Contains(out, "Xid records of the 1 days to 2026-11-14") {
		t.Errorf("--days 1:\n%s", out)
	}

	// Mistakes in the command line.
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"gpu", "42"}, `no GPU "42" with memory records in host gpu-01; name a GPU by its index, its serial number, its UUID or its PCI address`},
		{[]string{"gpu", "0", "1"}, `unexpected argument "1"`},
		{[]string{"gpu", "--days", "-3"}, "--days -3: want a number of days"},
		{[]string{"gpu", "--rows", "1"}, "unknown flag"},
	} {
		code, out, errOut := run(append([]string{"open", ledger}, tt.args...)...)
		if code != cli.ExitUsage || out != "" || !strings.Contains(errOut, tt.want) || !strings.Contains(errOut, `run "my help gpu" for usage`) {
			t.Errorf("%v: exit code %d, stdout %q, stderr %q", tt.args, code, out, errOut)
		}
	}
}

func TestGPUCommandJSON(t *testing.T) {
	code, out, errOut := run("open", gpuLedger(t), "gpu", "--json")
	if code != 3 || errOut != "" {
		t.Fatalf("exit code %d, stderr %q", code, errOut)
	}
	var e struct {
		View     string `json:"view"`
		Version  int    `json:"version"`
		Host     string `json:"host"`
		Grade    string `json:"grade"`
		ExitCode int    `json:"exit_code"`
		Data     struct {
			Days int `json:"days"`
			GPUs []struct {
				GPU   string   `json:"gpu"`
				State string   `json:"state"`
				Grade string   `json:"grade"`
				Rules []string `json:"rules"`
				Banks *struct {
					Max, None int
				} `json:"banks"`
			} `json:"gpus"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &e); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if e.View != "gpu" || e.Version != jsonVersion || e.Host != "gpu-01" || e.Grade != "replace-now" || e.ExitCode != 3 || e.Data.Days != 30 || len(e.Data.GPUs) != 8 {
		t.Errorf("envelope: %+v", e)
	}
	if g := e.Data.GPUs[0]; g.GPU != "1650000000412" || g.State != "failed" || len(g.Rules) != 1 || g.Rules[0] != "R-REP-01" || g.Banks == nil || g.Banks.Max != 638 {
		t.Errorf("first GPU: %+v", g)
	}
	if g := e.Data.GPUs[3]; g.State != "low" || g.Grade != "ok" || g.Banks.None != 1 {
		t.Errorf("fourth GPU: %+v", g)
	}
	testutil.Golden(t, "json/gpu.json", []byte(out))
}

// A host without a GPU: one line, and the exit code of a host that has
// nothing to show.
func TestGPUCommandWithoutGPUs(t *testing.T) {
	code, out, errOut := run("open", demo(t), "gpu")
	if code != 0 || errOut != "" || !strings.Contains(out, "no GPU memory records: no Xid line about memory in the kernel log and no nvidia-smi output") {
		t.Errorf("exit code %d, stderr %q\n%s", code, errOut, out)
	}
	code, out, _ = run("open", demo(t), "gpu", "--json")
	if code != 0 || !strings.Contains(out, `"gpus": []`) || !strings.Contains(out, `"view": "gpu"`) {
		t.Errorf("--json: exit code %d\n%s", code, out)
	}
}
