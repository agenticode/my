package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/fmfmlog"
	"github.com/agenticode/my/internal/testutil"
)

var fixedClock = map[string]string{"MY_NOW": "2026-11-13T09:30:00Z"}

func TestExportFMFM(t *testing.T) {
	ledger := demo(t)

	// The log goes to standard output, as JSON.
	code, out, errOut := runEnv(fixedClock, "open", ledger, "export", "fmfm")
	if code != cli.ExitOK || errOut != "" {
		t.Fatalf("exit code %d, stderr %q", code, errOut)
	}
	var doc fmfmlog.Document
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if doc.Format != fmfmlog.Format || doc.HostID != "r12-n034" || doc.OutputDT != "2026-11-13T09:30:00.000Z" || doc.Analyzer.Name != "MemorY" ||
		len(doc.AnalysisLog) != 2 || len(doc.RuntimeLog) != 0 || doc.ActionDescriptors != "none" {
		t.Errorf("document: %+v", doc)
	}
	if a := doc.AnalysisLog[0]; a.DimmSerialNumber != "3B9C56" || a.FaultMode != "Row fault" || a.SeverityClassifier != "high" || a.Finding.Grade != "replace-now" {
		t.Errorf("first record: %+v", a)
	}
	testutil.Golden(t, "json/export-fmfm.json", []byte(out))

	// --json changes nothing: the log is JSON. "-o -" is standard output.
	for _, extra := range [][]string{{"--json"}, {"-o", "-"}} {
		_, again, _ := runEnv(fixedClock, append([]string{"open", ledger, "export", "fmfm"}, extra...)...)
		if again != out {
			t.Errorf("%v changed the log", extra)
		}
	}

	// With the runtime log: every error of the host once; with --all the
	// DIMMs without errors too.
	_, out, _ = runEnv(fixedClock, "open", ledger, "export", "fmfm", "--records", "--all")
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.RuntimeLog) != 1285 || len(doc.AnalysisLog) != 16 || doc.RuntimeLog[0].ErrorDT == "" || doc.RuntimeLog[0].Grade != "A" {
		t.Errorf("%d runtime records, %d analysis records", len(doc.RuntimeLog), len(doc.AnalysisLog))
	}

	// The sentences follow --lang.
	_, out, _ = runEnv(fixedClock, "open", ledger, "export", "fmfm", "--lang", "ko")
	if !strings.Contains(out, `"next_action": "DIMM 교체. 이 DIMM의 케이스 번들을 반품에 첨부."`) || !strings.Contains(out, `"FaultMode": "Row fault"`) {
		t.Errorf("--lang ko:\n%s", out)
	}
}

func TestExportFMFMToAFile(t *testing.T) {
	ledger := demo(t)
	file := filepath.Join(t.TempDir(), "analysis.json")

	code, out, errOut := runEnv(fixedClock, "open", ledger, "export", "fmfm", "-o", file, "--records")
	if code != cli.ExitOK || errOut != "" {
		t.Fatalf("exit code %d, stderr %q", code, errOut)
	}
	wantLines(t, out,
		"FMFM analysis log written: "+file+" (",
		"  host r12-n034, finding replace now: 2 DIMMs analysed, 1,285 error records in the runtime log",
		"  the log holds findings and suggestions for a person, and no action descriptor (CPAD)",
	)
	b, err := os.ReadFile(file)
	var doc fmfmlog.Document
	if err != nil || json.Unmarshal(b, &doc) != nil || len(doc.RuntimeLog) != 1285 {
		t.Fatalf("the file: %v, %d runtime records", err, len(doc.RuntimeLog))
	}

	// The file is there: it is not replaced unless asked.
	code, out, errOut = runEnv(fixedClock, "open", ledger, "export", "fmfm", "-o", file)
	if code != cli.ExitUsage || out != "" || !strings.Contains(errOut, "--force") {
		t.Errorf("an existing file: exit code %d, stdout %q, stderr %q", code, out, errOut)
	}
	code, out, _ = runEnv(fixedClock, "open", ledger, "export", "fmfm", "-o", file, "--force", "--json")
	var res struct {
		Path, Format, Host string
		Bytes, DIMMs       int
		Records            int
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || code != cli.ExitOK {
		t.Fatalf("--json: exit code %d, %v\n%s", code, err, out)
	}
	if res.Path != file || res.Format != fmfmlog.Format || res.Host != "r12-n034" || res.DIMMs != 2 || res.Records != 0 || res.Bytes < 1000 {
		t.Errorf("--json: %+v", res)
	}
	if fi, _ := os.Stat(file); fi == nil || int(fi.Size()) != res.Bytes {
		t.Errorf("the file has %v bytes, the result says %d", fi, res.Bytes)
	}

	// Korean.
	_, out, _ = runEnv(fixedClock, "open", ledger, "export", "fmfm", "-o", file, "--force", "--lang", "ko")
	wantLines(t, out, "FMFM 분석 로그 저장: ", "호스트 r12-n034, 소견 즉시 교체: 분석한 DIMM 2개, runtime 로그의 오류 기록 0건", "조치 기술자(CPAD)는 없음")
}

func TestExportCommandLine(t *testing.T) {
	ledger := demo(t)
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"export"}, "name the format to write: my export fmfm"},
		{[]string{"export", "cper"}, `unknown format "cper"; the format this version writes is fmfm`},
		{[]string{"export", "fmfm", "more"}, `unexpected argument "more"`},
		{[]string{"export", "fmfm", "--audience", "fa"}, "flag provided but not defined"},
	} {
		code, out, errOut := run(append([]string{"open", ledger}, tt.args...)...)
		if code != cli.ExitUsage || out != "" || !strings.Contains(errOut, tt.want) || !strings.Contains(errOut, `run "my help export" for usage`) {
			t.Errorf("%v: exit code %d, stdout %q, stderr %q", tt.args, code, out, errOut)
		}
	}
}
